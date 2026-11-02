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
	// CanElevate reports whether a permission prompt could give this account administrator rights:
	// false for a standard user, whose prompt asks for another account's password.
	CanElevate() bool
}

// ExecWinRunner runs real commands. Elevated hands the script to PowerShell as -EncodedCommand, never
// as a file: a file would sit on disk while the permission prompt is open, and another process of
// the same user could rewrite it before it runs as administrator. The elevated process shows
// nothing in this terminal, so the script reports through a result file in Dir (private to the
// user) that is read back.
type ExecWinRunner struct {
	Dir           string
	SID           string                 // the account that must run the script (the user's own)
	Admin         bool                   // this process is elevated: run the script directly
	NoAdminRights bool                   // this account is not an administrator: a prompt cannot help
	Private       func(dir string) error // makes Dir private (fsperm.PrivateDir)
}

// Run implements WinRunner.
func (ExecWinRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// IsElevated implements WinRunner.
func (r ExecWinRunner) IsElevated() bool { return r.Admin }

// CanElevate implements WinRunner.
func (r ExecWinRunner) CanElevate() bool { return r.Admin || !r.NoAdminRights }

// exitWrongAccount is the exit code of an elevated script that found itself running as someone
// other than the user (see wrapScript).
const exitWrongAccount = 3

// maxEncodedCommand is how long the Base64 script may be: CreateProcess takes 32767 characters for
// the whole command line, and the launcher's own text and the PowerShell path come on top.
const maxEncodedCommand = 30000

// Elevated implements WinRunner.
func (r ExecWinRunner) Elevated(ctx context.Context, body string) error {
	if err := r.Private(r.Dir); err != nil {
		return err
	}
	var id [6]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	result := filepath.Join(r.Dir, "admin-"+hex.EncodeToString(id[:])+".result")
	defer os.Remove(result)
	encoded := encodeCommand(wrapScript(body, result, r.SID))
	if len(encoded) > maxEncodedCommand {
		return fmt.Errorf("the administrator script is too long for a command line (%d characters; the limit is %d)", len(encoded), maxEncodedCommand)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var out []byte
	var runErr error
	if r.Admin {
		out, runErr = r.Run(ctx, powershellExe(), encodedArgs(encoded)...)
	} else {
		// -Command, not -EncodedCommand: the launcher carries the script's Base64, which another
		// layer of Base64 would stretch past the command-line limit.
		out, runErr = r.Run(ctx, powershellExe(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
			"-Command", launcherScript(powershellExe(), encoded))
	}
	data, err := os.ReadFile(result)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) && exit.ExitCode() == exitWrongAccount {
			return errors.New(wrongAccountMessage)
		}
		msg := strings.TrimSpace(string(out))
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		return fmt.Errorf("the administrator step did not run: %s\n(the Windows permission prompt may have been declined, or this session cannot show it)", msg)
	}
	return parseResult(data)
}

// wrongAccountMessage is what the user reads when Windows ran the elevated script as another
// account (a standard user asked for an administrator's name and password).
const wrongAccountMessage = "the administrator prompt ran as a different account than yours, so nothing was changed. " +
	"Dotlane installs per user: the login service and its files belong to your own account, so your own account " +
	"needs to be an administrator (ask an administrator to add it to the Administrators group), then run this again"

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

// encodeCommand is script as -EncodedCommand wants it: UTF-16 in Base64, which has no quoting rules
// to get wrong.
func encodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	raw := make([]byte, 0, 2*len(u))
	for _, c := range u {
		raw = append(raw, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// encodedArgs are the arguments that run an encoded command in Windows PowerShell.
func encodedArgs(encoded string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded}
}

// queryArgs are the arguments that run script in Windows PowerShell.
func queryArgs(script string) []string { return encodedArgs(encodeCommand(script)) }

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

// wrapScript is the script that runs body as administrator. It first checks that it runs as the
// account that asked (sid): a standard user who gives an administrator's credentials to the prompt
// gets a process that cannot read the user's private folder and would register the task for the
// wrong account, so it changes nothing and exits with exitWrongAccount. Otherwise it writes "ok", or
// "error: <step>: <message>" (body sets $step as it goes), to the result file, because an elevated
// window's output does not reach the terminal that started it.
func wrapScript(body, result, sid string) string {
	return `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$result = ` + psQuote(result) + `
$me = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
if ($me -ne ` + psQuote(sid) + `) {
    try { Set-Content -LiteralPath $result -Value ('error: ' + ` + psQuote(wrongAccountMessage) + `) -Encoding UTF8 } catch { }
    exit ` + fmt.Sprint(exitWrongAccount) + `
}
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

// launcherScript is a one-line script that starts the encoded command in an elevated PowerShell (a
// UAC prompt), waits for it and exits with its exit code. psExe is absolute: ShellExecute would
// search the current folder and the PATH for a bare name, and elevate whatever it found there. It
// uses single quotes only, as it travels as a -Command argument.
func launcherScript(psExe, encoded string) string {
	return `$ErrorActionPreference = 'Stop'; $p = Start-Process -FilePath ` + psQuote(psExe) +
		` -Verb RunAs -Wait -PassThru -WindowStyle Hidden -ArgumentList '-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-EncodedCommand',` +
		psQuote(encoded) + `; exit $p.ExitCode`
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

// killOld ends the running daemon with this PID, if the process still has the service's name (a
// PID can be reused while a permission prompt is open). Stopping the task ends only the cmd.exe that
// started the daemon, not the daemon, which keeps its ports and socket and would make the new one
// fail to bind.
func killOld(service string, pid int) string {
	if pid == 0 {
		return ""
	}
	id := fmt.Sprint(pid)
	return `$old = Get-Process -Id ` + id + ` -ErrorAction SilentlyContinue
if ($old -and $old.ProcessName -eq ` + psQuote(service) + `) { Stop-Process -Id ` + id + ` -Force -ErrorAction SilentlyContinue; $old.WaitForExit(10000) | Out-Null }
`
}

// stopWait stops the task and the daemon with pid (0: none known), and waits until the task has
// ended, so starting it again is not ignored (the task runs one instance at a time).
func stopWait(service string, pid int) string {
	return `Stop-ScheduledTask ` + taskArgs(service) + ` -ErrorAction SilentlyContinue
` + killOld(service, pid) + `for ($i = 0; $i -lt 50 -and (Get-ScheduledTask ` + taskArgs(service) + `).State -eq 'Running'; $i++) { Start-Sleep -Milliseconds 200 }
`
}

// taskSDDL lets administrators, SYSTEM and the user start, stop and change the task. Without it only
// administrators can, and every refresh after an upgrade would ask for a permission prompt. The task
// runs as the user without elevation, so changing it gives the user nothing they lack.
func taskSDDL(sid string) string { return "D:(A;;FA;;;BA)(A;;FA;;;SY)(A;;FA;;;" + sid + ")" }

// registerBody replaces the task with the definition in xml, lets its owner start and stop it (so
// refresh and upgrade need no elevation; best effort), ends the daemon of the old task (pid, or 0),
// and starts the new one.
func registerBody(service, xml, sid string, pid int) string {
	return `$xml = @'
` + xml + `'@
Register-ScheduledTask ` + taskArgs(service) + ` -Xml $xml -Force | Out-Null
try {
    $svc = New-Object -ComObject Schedule.Service
    $svc.Connect()
    $svc.GetFolder(` + psQuote(strings.TrimSuffix(taskFolder, `\`)) + `).GetTask(` + psQuote(service) + `).SetSecurityDescriptor(` +
		psQuote(taskSDDL(sid)) + `, 0)
} catch { }
` + stopWait(service, pid) + `Start-ScheduledTask ` + taskArgs(service) + "\n"
}

// registerShown is registerBody as the user reads it.
func registerShown(service, sid string, pid int) []string {
	shown := []string{
		"Register-ScheduledTask -TaskName " + winQuote(service) + " -TaskPath " + winQuote(taskFolder) + " -Xml <the task definition> -Force",
		"<Schedule.Service COM>.GetFolder(" + winQuote(strings.TrimSuffix(taskFolder, `\`)) + ").GetTask(" + winQuote(service) +
			").SetSecurityDescriptor(\"" + taskSDDL(sid) + "\", 0)    # your account may start and stop the task",
	}
	return append(append(shown, stopShown(service, pid)...), startShown(service))
}

// stopShown is stopWait as the user reads it.
func stopShown(service string, pid int) []string {
	shown := []string{"Stop-ScheduledTask -TaskName " + winQuote(service) + " -TaskPath " + winQuote(taskFolder)}
	if pid != 0 {
		shown = append(shown, fmt.Sprintf("Stop-Process -Id %d -Force    # the running Dotlane service (%s)", pid, service))
	}
	return shown
}

func startShown(service string) string {
	return "Start-ScheduledTask -TaskName " + winQuote(service) + " -TaskPath " + winQuote(taskFolder)
}

// restartBody stops the task and the daemon (pid, or 0) and starts the task again.
func restartBody(service string, pid int) string {
	return stopWait(service, pid) + `Start-ScheduledTask ` + taskArgs(service) + "\n"
}

// unregisterBody stops the task and the daemon (pid, or 0), removes the task, and its folder when
// it was the last one in it.
func unregisterBody(service string, pid int) string {
	return `Stop-ScheduledTask ` + taskArgs(service) + ` -ErrorAction SilentlyContinue
` + killOld(service, pid) + `Unregister-ScheduledTask ` + taskArgs(service) + ` -Confirm:$false
try {
    if (-not (Get-ScheduledTask -TaskPath ` + psQuote(taskFolder) + ` -ErrorAction SilentlyContinue)) {
        $svc = New-Object -ComObject Schedule.Service
        $svc.Connect()
        $svc.GetFolder('\').DeleteFolder(` + psQuote(strings.Trim(taskFolder, `\`)) + `, 0)
    }
} catch { }
`
}

// unregisterShown is unregisterBody as the user reads it.
func unregisterShown(service string, pid int) []string {
	shown := stopShown(service, pid)
	return append(shown,
		"Unregister-ScheduledTask -TaskName "+winQuote(service)+" -TaskPath "+winQuote(taskFolder)+" -Confirm:$false",
		"<Schedule.Service COM>.GetFolder('\\').DeleteFolder("+winQuote(strings.Trim(taskFolder, `\`))+", 0)    # only if no other task is in it")
}

// nrptComment marks the DNS rules of one service.
func nrptComment(service string) string { return "dotlane:" + service }

// addRuleShown is addRuleBody as the user reads it.
func addRuleShown(service, tld string) []string {
	comment := nrptComment(service)
	return []string{
		fmt.Sprintf(`Get-DnsClientNrptRule | Where-Object { $_.Comment -eq "%s" -and $_.Namespace -contains ".%s" } | Remove-DnsClientNrptRule -Force    # Dotlane's earlier rule for it, if any`, comment, tld),
		fmt.Sprintf(`Add-DnsClientNrptRule -Namespace ".%s" -NameServers "127.0.0.1" -Comment "%s"`, tld, comment),
		"Clear-DnsClientCache",
	}
}

// addRuleBody replaces Dotlane's rule for tld with one that sends it to 127.0.0.1, then clears the
// DNS cache so earlier answers (a failed lookup) are forgotten.
func addRuleBody(service, tld string) string {
	return `$comment = ` + psQuote(nrptComment(service)) + `
Get-DnsClientNrptRule | Where-Object { $_.Comment -eq $comment -and $_.Namespace -contains ` + psQuote("."+tld) + ` } | Remove-DnsClientNrptRule -Force
Add-DnsClientNrptRule -Namespace ` + psQuote("."+tld) + ` -NameServers '127.0.0.1' -Comment $comment
Clear-DnsClientCache
`
}

// removeRuleShown is removeRuleBody as the user reads it.
func removeRuleShown(service, tld string) []string {
	return []string{
		fmt.Sprintf(`Get-DnsClientNrptRule | Where-Object { $_.Comment -eq "%s" -and $_.Namespace -contains ".%s" } | Remove-DnsClientNrptRule -Force`, nrptComment(service), tld),
		"Clear-DnsClientCache",
	}
}

// removeRuleBody removes the service's DNS rule for tld, leaving its other rules and other
// tools' rules alone.
func removeRuleBody(service, tld string) string {
	return `$comment = ` + psQuote(nrptComment(service)) + `
Get-DnsClientNrptRule | Where-Object { $_.Comment -eq $comment -and $_.Namespace -contains ` + psQuote("."+tld) + ` } | Remove-DnsClientNrptRule -Force
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
	// .NET's store, not the Cert: drive: in a fresh Windows PowerShell 5.1 started with
	// -EncodedCommand, the drive does not exist until its module loads, and Test-Path on it then
	// says false for a certificate that is there.
	return queryPreamble + `$store = [Security.Cryptography.X509Certificates.X509Store]::new('Root', 'LocalMachine')
$store.Open('ReadOnly')
$found = $store.Certificates.Find('FindByThumbprint', ` + psQuote(thumbprint) + `, $false).Count
$store.Close()
if ($found -gt 0) { exit 0 } else { exit 1 }
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

// ruleFor judges the rules for tld. Dotlane's own correct rule wins wherever it is in the list;
// otherwise another tool's rule for the ending counts, then Dotlane's wrong one. The rule it blames,
// for ruleWrong and ruleForeign, comes back too.
func ruleFor(rules []nrptRule, service, tld string) (ruleState, nrptRule) {
	state, found := ruleMissing, nrptRule{}
	for _, r := range rules {
		if !r.covers(tld) {
			continue
		}
		switch {
		case r.Comment != nrptComment(service):
			if state != ruleOK {
				state, found = ruleForeign, r
			}
		case strings.TrimSpace(r.NameServers) == "127.0.0.1":
			return ruleOK, r
		case state == ruleMissing:
			state, found = ruleWrong, r
		}
	}
	return state, found
}
