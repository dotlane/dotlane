//go:build windows

package install

import (
	"encoding/base64"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestPsQuote(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\Ana\x.exe`:       `'C:\Users\Ana\x.exe'`,
		`C:\Users\Ana Silva\x.exe`: `'C:\Users\Ana Silva\x.exe'`,
		`C:\Users\O'Brien\x`:       `'C:\Users\O''Brien\x'`,
		"it\u2019s":                "'it\u2019\u2019s'", // PowerShell reads typographic quotes as quotes
		"\u2018a\u201b":            "'\u2018\u2018a\u201b\u201b'",
		`$env:Path; Remove-Item *`: `'$env:Path; Remove-Item *'`, // nothing expands inside single quotes
		``:                         `''`,
	} {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestWinQuote(t *testing.T) {
	for in, want := range map[string]string{
		`C:\a\b.crt`:     `C:\a\b.crt`,
		`C:\a b\b.crt`:   `"C:\a b\b.crt"`,
		`C:\a&b\b.crt`:   `"C:\a&b\b.crt"`,
		`C:\Ana's\b.crt`: `"C:\Ana's\b.crt"`,
	} {
		if got := winQuote(in); got != want {
			t.Errorf("winQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestQueryArgsEncodeUTF16(t *testing.T) {
	script := "Write-Output 'ação'\n"
	args := queryArgs(script)
	if args[len(args)-2] != "-EncodedCommand" {
		t.Fatalf("args = %v", args)
	}
	raw, err := base64.StdEncoding.DecodeString(args[len(args)-1])
	if err != nil {
		t.Fatal(err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if got := string(utf16.Decode(u)); got != script {
		t.Errorf("decoded %q, want %q", got, script)
	}
}

func TestLauncherScriptQuotesThePath(t *testing.T) {
	got := launcherScript(`C:\Users\O'Brien\AppData\Local\dotlane\admin-1.ps1`)
	for _, want := range []string{
		"Start-Process powershell.exe -Verb RunAs -Wait -WindowStyle Hidden",
		`'-File','"C:\Users\O''Brien\AppData\Local\dotlane\admin-1.ps1"'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("launcher lacks %s:\n%s", want, got)
		}
	}
}

func TestWrapScriptReportsErrors(t *testing.T) {
	got := wrapScript("    $step = 'A'\n    Write-Output 1", `C:\x y\r.result`)
	for _, want := range []string{"$result = 'C:\\x y\\r.result'", "function Invoke-Native", "Write-Output 1",
		"Set-Content -LiteralPath $result -Value 'ok'", "'error: ' + $step"} {
		if !strings.Contains(got, want) {
			t.Errorf("script lacks %q:\n%s", want, got)
		}
	}
}

func TestParseResult(t *testing.T) {
	if err := parseResult([]byte("\uFEFFok\r\n")); err != nil {
		t.Errorf("ok gave %v", err)
	}
	err := parseResult([]byte("\uFEFFerror: Certificate authority: certutil.exe failed with exit code 5\r\n"))
	if err == nil || err.Error() != "Certificate authority: certutil.exe failed with exit code 5" {
		t.Errorf("error result gave %v", err)
	}
	if parseResult(nil) == nil {
		t.Error("an empty result file counts as success")
	}
}

func TestStepBody(t *testing.T) {
	got := stepBody([]adminStep{
		{Label: "Login service", Body: "A\nB\n"},
		{Label: "Ana's CA", Body: "C\n"},
	})
	want := "$step = 'Login service'\nA\nB\n$step = 'Ana''s CA'\nC"
	if got != want {
		t.Errorf("stepBody =\n%s\nwant\n%s", got, want)
	}
}

func testSpec() taskSpec {
	return taskSpec{
		Service: "dev.dotlane", SID: "S-1-5-21-1-2-3-1001",
		Exe:    `C:\Users\Ana Silva\AppData\Local\Programs\dotlane\dev.dotlane.exe`,
		Config: `C:\Users\Ana Silva\AppData\Roaming\dotlane\config.toml`,
		Log:    `C:\Users\Ana Silva\AppData\Local\dotlane\logs\dev.dotlane.log`,
	}
}

func TestTaskCommandLine(t *testing.T) {
	got, err := testSpec().commandLine()
	if err != nil {
		t.Fatal(err)
	}
	want := `/d /s /c ""C:\Users\Ana Silva\AppData\Local\Programs\dotlane\dev.dotlane.exe" --config "C:\Users\Ana Silva\AppData\Roaming\dotlane\config.toml" serve >> "C:\Users\Ana Silva\AppData\Local\dotlane\logs\dev.dotlane.log" 2>&1"`
	if got != want {
		t.Errorf("commandLine =\n%s\nwant\n%s", got, want)
	}
	for _, bad := range []string{`C:\100%\x.exe`, `C:\a"b\x.exe`} {
		s := testSpec()
		s.Config = bad
		if _, err := s.commandLine(); err == nil {
			t.Errorf("commandLine accepted %s", bad)
		}
		if _, err := s.xml(); err == nil {
			t.Errorf("xml accepted %s", bad)
		}
	}
	s := testSpec()
	s.Exe = `C:\Users\O'Brien & Co\dotlane.exe` // ' and & are literal inside the quotes
	if _, err := s.commandLine(); err != nil {
		t.Errorf("commandLine refused an apostrophe and an ampersand: %v", err)
	}
}

func TestTaskXML(t *testing.T) {
	s := testSpec()
	text, err := s.xml()
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		Description string `xml:"RegistrationInfo>Description"`
		Trigger     string `xml:"Triggers>LogonTrigger>UserId"`
		User        string `xml:"Principals>Principal>UserId"`
		LogonType   string `xml:"Principals>Principal>LogonType"`
		RunLevel    string `xml:"Principals>Principal>RunLevel"`
		Limit       string `xml:"Settings>ExecutionTimeLimit"`
		Battery     string `xml:"Settings>DisallowStartIfOnBatteries"`
		Count       int    `xml:"Settings>RestartOnFailure>Count"`
		Interval    string `xml:"Settings>RestartOnFailure>Interval"`
		Command     string `xml:"Actions>Exec>Command"`
		Arguments   string `xml:"Actions>Exec>Arguments"`
	}
	d := xml.NewDecoder(strings.NewReader(text))
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil } // UTF-16 declared, a string given
	if err := d.Decode(&task); err != nil {
		t.Fatalf("not well-formed: %v\n%s", err, text)
	}
	wantArgs, _ := s.commandLine()
	for name, pair := range map[string][2]any{
		"trigger user": {task.Trigger, s.SID}, "principal": {task.User, s.SID}, "logon type": {task.LogonType, "S4U"},
		"run level": {task.RunLevel, "LeastPrivilege"}, "time limit": {task.Limit, "PT0S"}, "battery": {task.Battery, "false"},
		"restarts": {task.Count, 999}, "interval": {task.Interval, "PT1M"}, "command": {task.Command, "cmd.exe"},
		"arguments": {task.Arguments, wantArgs},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
	if !strings.Contains(task.Description, s.marker()) {
		t.Errorf("description %q lacks the marker %s", task.Description, s.marker())
	}
	if strings.Contains(text, "\n'@") {
		t.Error("the XML would end the PowerShell here-string it is embedded in")
	}
}

func TestMarkerChangesWithTheSpec(t *testing.T) {
	base := testSpec().marker()
	for name, mutate := range map[string]func(*taskSpec){
		"service": func(s *taskSpec) { s.Service += "x" }, "user": func(s *taskSpec) { s.SID += "1" },
		"exe": func(s *taskSpec) { s.Exe += "x" }, "config": func(s *taskSpec) { s.Config += "x" }, "log": func(s *taskSpec) { s.Log += "x" },
	} {
		s := testSpec()
		mutate(&s)
		if s.marker() == base {
			t.Errorf("changing the %s kept the marker", name)
		}
	}
	if testSpec().marker() != base {
		t.Error("the marker is not stable")
	}
}

// A here-string ends at a line that starts with '@, so the task XML must reach PowerShell
// unindented.
func TestStepBodyKeepsHereStringsAtColumnZero(t *testing.T) {
	got := wrapScript(stepBody([]adminStep{{Label: "x", Body: registerBody("s", "<?xml version=\"1.0\"?>\n<Task/>\n", "S-1")}}), `C:\r`)
	if !strings.Contains(got, "\n$xml = @'\n<?xml version=\"1.0\"?>\n<Task/>\n'@\n") {
		t.Errorf("the here-string is not at column 0:\n%s", got)
	}
}

func TestBodiesQuoteTheirNames(t *testing.T) {
	got := addRuleBody("dev.dotlane", "dltest")
	for _, want := range []string{"$comment = 'dotlane:dev.dotlane'", "$_.Namespace -contains '.dltest'",
		"Add-DnsClientNrptRule -Namespace '.dltest' -NameServers '127.0.0.1' -Comment $comment", "Clear-DnsClientCache"} {
		if !strings.Contains(got, want) {
			t.Errorf("addRuleBody lacks %q:\n%s", want, got)
		}
	}
	if got := trustBody(`C:\O'B\root.crt`, `C:\O'B\x.log`); !strings.Contains(got, `$ca = 'C:\O''B\root.crt'`) ||
		!strings.Contains(got, `'C:\O''B\x.log'`) || !strings.Contains(got, "Invoke-Native certutil.exe -addstore -f Root $ca") {
		t.Errorf("trustBody:\n%s", got)
	}
	if got := untrustBody("AB12"); got != "Invoke-Native certutil.exe -delstore Root 'AB12'\n" {
		t.Errorf("untrustBody = %q", got)
	}
	reg := registerBody("dev.dotlane", "<Task/>\n", "S-1-5-21-1")
	for _, want := range []string{"$xml = @'\n<Task/>\n'@", "Register-ScheduledTask -TaskName 'dev.dotlane' -TaskPath '\\Dotlane\\' -Xml $xml -Force",
		"Start-ScheduledTask -TaskName 'dev.dotlane'", "(A;;FA;;;S-1-5-21-1)"} {
		if !strings.Contains(reg, want) {
			t.Errorf("registerBody lacks %q:\n%s", want, reg)
		}
	}
}

func TestJSONLineSkipsNoise(t *testing.T) {
	out := []byte("#< CLIXML\r\n<Objs/>\r\n\uFEFF[{\"Name\":\"a\"}]\r\n")
	if got := string(jsonLine(out)); got != `[{"Name":"a"}]` {
		t.Errorf("jsonLine = %q", got)
	}
	if jsonLine([]byte("nothing here")) != nil {
		t.Error("jsonLine found JSON in plain text")
	}
}

func TestParseRules(t *testing.T) {
	// PowerShell prints one rule as an object, none as an empty array (-InputObject keeps it).
	one, err := parseRules([]byte(`{"Name":"{1}","Namespace":".dltest","NameServers":"127.0.0.1","Comment":"dotlane:dev.dotlane"}`))
	if err != nil || len(one) != 1 || one[0].Comment != "dotlane:dev.dotlane" {
		t.Errorf("one rule: %+v, %v", one, err)
	}
	none, err := parseRules([]byte("[]\r\n"))
	if err != nil || len(none) != 0 {
		t.Errorf("no rules: %+v, %v", none, err)
	}
	if _, err := parseRules([]byte("Get-DnsClientNrptRule : Access is denied")); err == nil {
		t.Error("an error message parsed as rules")
	}
}

func TestRuleFor(t *testing.T) {
	mine := nrptRule{Name: "{1}", Namespace: ".dltest", NameServers: "127.0.0.1", Comment: "dotlane:dev.dotlane"}
	for name, tc := range map[string]struct {
		rules []nrptRule
		want  ruleState
	}{
		"none":    {nil, ruleMissing},
		"ours":    {[]nrptRule{mine}, ruleOK},
		"other":   {[]nrptRule{{Name: "{2}", Namespace: ".corp", NameServers: "10.0.0.1"}}, ruleMissing},
		"foreign": {[]nrptRule{{Name: "{2}", Namespace: ".DLTEST", NameServers: "10.0.0.1", Comment: "vpn"}}, ruleForeign},
		"wrong":   {[]nrptRule{{Name: "{1}", Namespace: ".dltest", NameServers: "10.0.0.1", Comment: "dotlane:dev.dotlane"}}, ruleWrong},
		"several": {[]nrptRule{{Name: "{3}", Namespace: ".a,.dltest", NameServers: "10.0.0.1", Comment: "dotlane:dev.dotlane"}, mine}, ruleOK},
		// Another service's rule is another tool's as far as this one is concerned.
		"sibling": {[]nrptRule{{Name: "{4}", Namespace: ".dltest", NameServers: "127.0.0.1", Comment: "dotlane:dev.other"}}, ruleForeign},
	} {
		if got, _ := ruleFor(tc.rules, "dev.dotlane", "dltest"); got != tc.want {
			t.Errorf("%s: ruleFor = %d, want %d", name, got, tc.want)
		}
	}
}
