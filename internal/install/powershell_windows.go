//go:build windows

package install

// PowerShell and Task Scheduler plumbing for the Windows installer: the runner that starts
// commands and the one elevated script, and the pure functions that build scripts and read their
// output. Nothing here touches the system except ExecWinRunner.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

// taskFolder is the Task Scheduler folder every Dotlane task lives in.
const taskFolder = `\Dotlane\`

// WinRunner runs the commands the Windows installer needs. Tests replace it; nothing else should.
type WinRunner interface {
	// Run runs an unprivileged command and returns its combined output.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	// Elevated runs a PowerShell script as administrator and waits for it: with one Windows
	// permission prompt, or none when this process is already elevated. It returns the error the
	// script reported, if any.
	Elevated(ctx context.Context, script string) error
	// IsElevated reports whether this process already has administrator rights.
	IsElevated() bool
}

// ExecWinRunner runs real commands. Elevated writes the script into Dir (a folder only the
// current user and SYSTEM can write, or another account could swap the script before it runs
// elevated) and starts it with UAC's "runas". The elevated process shows nothing in this
// terminal, so the script reports through a result file that is read back.
type ExecWinRunner struct {
	Dir     string
	Admin   bool                   // this process is elevated: run the script directly
	Private func(dir string) error // makes Dir private (fsperm.PrivateDir)
}

// Run implements WinRunner.
func (ExecWinRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// IsElevated implements WinRunner.
func (r ExecWinRunner) IsElevated() bool { return r.Admin }

// Elevated implements WinRunner.
func (r ExecWinRunner) Elevated(ctx context.Context, body string) error {
	if err := r.Private(r.Dir); err != nil {
		return err
	}
	var id [6]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	base := filepath.Join(r.Dir, "admin-"+hex.EncodeToString(id[:]))
	script, result := base+".ps1", base+".result"
	defer os.Remove(script)
	defer os.Remove(result)
	// A byte order mark makes Windows PowerShell 5.1 read the file as UTF-8, not as the ANSI
	// code page, so paths with accents survive.
	if err := os.WriteFile(script, []byte("\uFEFF"+wrapScript(body, result)), 0o600); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var out []byte
	var runErr error
	if r.Admin {
		out, runErr = r.Run(ctx, powershellExe(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	} else {
		out, runErr = r.Run(ctx, powershellExe(), queryArgs(launcherScript(script))...)
	}
	data, err := os.ReadFile(result)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		return fmt.Errorf("the administrator step did not run: %s\n(the Windows permission prompt may have been declined, or this session cannot show it)", msg)
	}
	return parseResult(data)
}

// system32 is the path of a program in Windows' System32 folder, so a PATH entry cannot stand in
// for it.
func system32(parts ...string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(append([]string{root, "System32"}, parts...)...)
}

// powershellExe is Windows PowerShell 5.1, which every supported Windows has (PowerShell 7 does
// not ship with it).
func powershellExe() string { return system32("WindowsPowerShell", "v1.0", "powershell.exe") }

// psQuote is s as a single-quoted PowerShell string. Inside one, only the quote character is
// special, and it is doubled; PowerShell also counts the typographic quotes U+2018 to U+201B as
// quotes, so those are doubled too.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		b.WriteRune(r)
		if r == '\'' || (r >= '\u2018' && r <= '\u201b') {
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// winQuote is s as an argument on a command line that is shown to the user (cmd.exe or
// PowerShell): double quotes when it has a space.
func winQuote(s string) string {
	if strings.ContainsAny(s, " \t&()^;,'") {
		return `"` + s + `"`
	}
	return s
}

// queryArgs are the arguments that run script in Windows PowerShell. The script travels as
// -EncodedCommand (UTF-16 in Base64), which has no quoting rules to get wrong.
func queryArgs(script string) []string {
	u := utf16.Encode([]rune(script))
	raw := make([]byte, 0, 2*len(u))
	for _, c := range u {
		raw = append(raw, byte(c), byte(c>>8))
	}
	return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand",
		base64.StdEncoding.EncodeToString(raw)}
}

// queryPreamble starts every read-only script: UTF-8 output, no progress bars (redirected, they
// turn into CLIXML on stderr), errors printed as a line the Go side can read.
const queryPreamble = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
`

// jsonLine returns the last line of out that starts a JSON array or object, or nil. PowerShell
// can add other lines (a CLIXML header, warnings) around a script's output.
func jsonLine(out []byte) []byte {
	lines := bytes.Split(out, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		l := bytes.TrimSpace(bytes.TrimPrefix(lines[i], []byte("\uFEFF")))
		if len(l) > 0 && (l[0] == '[' || l[0] == '{') {
			return l
		}
	}
	return nil
}

// decodeList reads JSON that PowerShell wrote as an array or, for a single item, as an object.
func decodeList[T any](out []byte) ([]T, error) {
	line := jsonLine(out)
	if line == nil {
		return nil, fmt.Errorf("unexpected output: %s", bytes.TrimSpace(out))
	}
	var list []T
	if line[0] == '[' {
		return list, json.Unmarshal(line, &list)
	}
	var one T
	if err := json.Unmarshal(line, &one); err != nil {
		return nil, err
	}
	return append(list, one), nil
}

// nativeHelper runs a program, and throws when it fails, with what it printed. PowerShell 5.1 turns
// a program's stderr into errors under $ErrorActionPreference = 'Stop', hence the local setting.
const nativeHelper = `function Invoke-Native {
    $ErrorActionPreference = 'Continue'
    $exe = $args[0]
    $rest = @($args | Select-Object -Skip 1)
    $out = (& $exe @rest 2>&1 | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) { throw "$exe failed with exit code ${LASTEXITCODE}: $out" }
}
`

// wrapScript is the script that runs body as administrator. It writes "ok", or "error: <step>:
// <message>" (body sets $step as it goes), to the result file, because an elevated window's output
// does not reach the terminal that started it.
func wrapScript(body, result string) string {
	return `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$result = ` + psQuote(result) + `
$step = 'Dotlane'
` + nativeHelper + `try {
` + body + `
    Set-Content -LiteralPath $result -Value 'ok' -Encoding UTF8
} catch {
    Set-Content -LiteralPath $result -Value ('error: ' + $step + ': ' + $_.Exception.Message) -Encoding UTF8
    exit 1
}
`
}

// launcherScript starts script in an elevated PowerShell (a UAC prompt) and waits for it. Each
// argument is quoted for PowerShell, and the script path once more for the new process, because
// Start-Process joins its arguments with spaces and does not quote them.
func launcherScript(script string) string {
	return `$ErrorActionPreference = 'Stop'
Start-Process powershell.exe -Verb RunAs -Wait -WindowStyle Hidden -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File',` +
		psQuote(`"`+script+`"`) + "\n"
}

// parseResult turns the result file into an error: nil for "ok".
func parseResult(data []byte) error {
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\uFEFF"))
	if text == "ok" {
		return nil
	}
	return errors.New(strings.TrimPrefix(text, "error: "))
}

// adminStep is one change that needs administrator rights. Install, uninstall and refresh collect
// the ones they need, show each, and run them together as one elevated script.
type adminStep struct {
	Label string
	Why   string   // what it does and why, for the user
	Shown []string // the commands, as they are shown
	Body  string   // PowerShell; sets $step first
	// Done reports the step's outcome once the script has run, and may check it worked.
	Done func(ctx context.Context) error
}

// stepBody is the body of the elevated script for steps: each sets $step to its label first, so an
// error names the step. The bodies are not indented, as a here-string's closing line must start in
// the first column.
func stepBody(steps []adminStep) string {
	var parts []string
	for _, s := range steps {
		parts = append(parts, "$step = "+psQuote(s.Label)+"\n"+strings.TrimRight(s.Body, "\n"))
	}
	return strings.Join(parts, "\n")
}

// taskSpec is what the login service's scheduled task runs, and for whom.
type taskSpec struct {
	Service string // the service name; the task's name inside taskFolder
	SID     string // the user the task runs as
	Exe     string // the copy of the binary
	Config  string
	Log     string
}

// specVersion changes when the task's XML does, so an installed task is replaced by the new form.
const specVersion = "1"

// commandLine is the argument line of cmd.exe, which runs the daemon and appends its output to the
// log: a task started with S4U has no console, so the daemon's own log would be lost. cmd.exe
// strips the outer pair of quotes (/s), leaving each path quoted. A path with a double quote cannot
// exist on Windows; one with % would be expanded by cmd.exe, so it is refused.
func (s taskSpec) commandLine() (string, error) {
	for _, p := range []string{s.Exe, s.Config, s.Log} {
		if strings.ContainsAny(p, `%"`) {
			return "", fmt.Errorf("the path %s contains a character (%% or \") that the login service cannot pass on; use a folder without it", p)
		}
	}
	return fmt.Sprintf(`/d /s /c ""%s" --config "%s" serve >> "%s" 2>&1"`, s.Exe, s.Config, s.Log), nil
}

// marker is the text in the task's description that says which spec it was made from. Comparing
// it with the wanted one tells whether the task needs replacing, without parsing the XML Windows
// writes back (which it reorders and fills with defaults).
func (s taskSpec) marker() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{specVersion, s.Service, s.SID, s.Exe, s.Config, s.Log}, "\x00")))
	return "spec=" + hex.EncodeToString(sum[:8])
}

func esc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// xml is the task definition: at logon of the user, run the daemon with S4U (no console window, no
// stored password, no admin rights), restart it every minute if it exits, and never stop it on
// a timer, on battery or on idle.
func (s taskSpec) xml() (string, error) {
	args, err := s.commandLine()
	if err != nil {
		return "", err
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>` + esc("Dotlane login service. Written by dotlane install; dotlane uninstall removes it. "+s.marker()) + `</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>` + esc(s.SID) + `</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + esc(s.SID) + `</UserId>
      <LogonType>S4U</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>cmd.exe</Command>
      <Arguments>` + esc(args) + `</Arguments>
    </Exec>
  </Actions>
</Task>
`, nil
}

// taskArgs are the -TaskName and -TaskPath arguments every task cmdlet takes.
func taskArgs(service string) string {
	return "-TaskName " + psQuote(service) + " -TaskPath " + psQuote(taskFolder)
}

// stopWait stops the task and waits until its process is gone, so starting it again is not
// ignored (the task runs one instance at a time).
func stopWait(service string) string {
	return `Stop-ScheduledTask ` + taskArgs(service) + ` -ErrorAction SilentlyContinue
for ($i = 0; $i -lt 50 -and (Get-ScheduledTask ` + taskArgs(service) + `).State -eq 'Running'; $i++) { Start-Sleep -Milliseconds 200 }
`
}

// registerBody replaces the task with the definition in xml, lets its owner start and stop it (so
// refresh and upgrade need no elevation; best effort), and starts it.
func registerBody(service, xml, sid string) string {
	return `$xml = @'
` + xml + `'@
Register-ScheduledTask ` + taskArgs(service) + ` -Xml $xml -Force | Out-Null
try {
    $svc = New-Object -ComObject Schedule.Service
    $svc.Connect()
    $svc.GetFolder(` + psQuote(strings.TrimSuffix(taskFolder, `\`)) + `).GetTask(` + psQuote(service) + `).SetSecurityDescriptor(` +
		psQuote("D:(A;;FA;;;BA)(A;;FA;;;SY)(A;;FA;;;"+sid+")") + `, 0)
} catch { }
` + stopWait(service) + `Start-ScheduledTask ` + taskArgs(service) + "\n"
}

// restartBody stops the task and starts it again.
func restartBody(service string) string {
	return stopWait(service) + `Start-ScheduledTask ` + taskArgs(service) + "\n"
}

// unregisterBody stops and removes the task, and its folder when it was the last one in it.
func unregisterBody(service string) string {
	return `Stop-ScheduledTask ` + taskArgs(service) + ` -ErrorAction SilentlyContinue
Unregister-ScheduledTask ` + taskArgs(service) + ` -Confirm:$false
try {
    if (-not (Get-ScheduledTask -TaskPath ` + psQuote(taskFolder) + ` -ErrorAction SilentlyContinue)) {
        $svc = New-Object -ComObject Schedule.Service
        $svc.Connect()
        $svc.GetFolder('\').DeleteFolder(` + psQuote(strings.Trim(taskFolder, `\`)) + `, 0)
    }
} catch { }
`
}

// nrptComment marks the DNS rules of one service.
func nrptComment(service string) string { return "dotlane:" + service }

// addRuleBody replaces Dotlane's rule for tld with one that sends it to 127.0.0.1, then clears the
// DNS cache so earlier answers (a failed lookup) are forgotten.
func addRuleBody(service, tld string) string {
	return `$comment = ` + psQuote(nrptComment(service)) + `
Get-DnsClientNrptRule | Where-Object { $_.Comment -eq $comment -and $_.Namespace -contains ` + psQuote("."+tld) + ` } | Remove-DnsClientNrptRule -Force
Add-DnsClientNrptRule -Namespace ` + psQuote("."+tld) + ` -NameServers '127.0.0.1' -Comment $comment
Clear-DnsClientCache
`
}

// removeRulesBody removes every DNS rule of the service, whichever endings it was installed with.
func removeRulesBody(service string) string {
	return `$comment = ` + psQuote(nrptComment(service)) + `
Get-DnsClientNrptRule | Where-Object { $_.Comment -eq $comment } | Remove-DnsClientNrptRule -Force
Clear-DnsClientCache
`
}

// trustBody waits for the daemon to create its certificate authority (a first install starts it
// in this same script), then adds it to the machine's trusted roots.
func trustBody(cert, log string) string {
	return `$ca = ` + psQuote(cert) + `
for ($i = 0; $i -lt 150 -and -not (Test-Path -LiteralPath $ca); $i++) { Start-Sleep -Milliseconds 200 }
if (-not (Test-Path -LiteralPath $ca)) { throw ('the service has not created its certificate authority (' + $ca + '); see ' + ` + psQuote(log) + `) }
Invoke-Native certutil.exe -addstore -f Root $ca
`
}

// untrustBody removes the certificate with this SHA-1 thumbprint from the machine's trusted roots.
func untrustBody(thumbprint string) string {
	return "Invoke-Native certutil.exe -delstore Root " + psQuote(thumbprint) + "\n"
}

// trustedScript exits 0 when the machine's Root store holds the thumbprint. Only hex digits get
// in, as the thumbprint is computed, but the check is cheap.
func trustedScript(thumbprint string) string {
	return queryPreamble + `if (Test-Path -LiteralPath ` + psQuote(`Cert:\LocalMachine\Root\`+thumbprint) + `) { exit 0 } else { exit 1 }
`
}

// nrptRule is a DNS client rule as the query below prints it.
type nrptRule struct {
	Name        string
	Namespace   string // comma-joined
	NameServers string // comma-joined
	Comment     string
}

// nrptScript prints the machine's NRPT rules as a JSON array.
const nrptScript = queryPreamble + `$rules = @(Get-DnsClientNrptRule | ForEach-Object {
    [pscustomobject]@{ Name = [string]$_.Name; Namespace = ($_.Namespace -join ','); NameServers = ($_.NameServers -join ','); Comment = [string]$_.Comment }
})
ConvertTo-Json -Compress -InputObject $rules
`

func parseRules(out []byte) ([]nrptRule, error) { return decodeList[nrptRule](out) }

// ruleState is how one domain ending's DNS rule stands.
type ruleState int

const (
	ruleMissing ruleState = iota
	ruleOK
	ruleWrong   // Dotlane's rule, but it points elsewhere
	ruleForeign // another tool's rule covers the ending
)

func (r nrptRule) covers(tld string) bool {
	for _, ns := range strings.Split(r.Namespace, ",") {
		if strings.EqualFold(strings.TrimSpace(ns), "."+tld) {
			return true
		}
	}
	return false
}

// ruleFor judges the rules for tld. The rule it blames, for ruleWrong and ruleForeign, comes back
// too.
func ruleFor(rules []nrptRule, service, tld string) (ruleState, nrptRule) {
	state, found := ruleMissing, nrptRule{}
	for _, r := range rules {
		if !r.covers(tld) {
			continue
		}
		switch {
		case r.Comment != nrptComment(service):
			return ruleForeign, r
		case strings.TrimSpace(r.NameServers) == "127.0.0.1":
			state, found = ruleOK, r
		case state != ruleOK:
			state, found = ruleWrong, r
		}
	}
	return state, found
}
