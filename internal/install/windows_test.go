//go:build windows

package install

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/dotlane/dotlane/internal/api"
	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/proxy"
	"github.com/dotlane/dotlane/internal/ui"
)

const winDaemonPID = 4242

const testCAName = "Dotlane Local Authority - ECC Root"

// testCA is a self-signed root certificate (DER), named like the one the daemon creates.
func testCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: testCAName},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// fakeWin simulates Task Scheduler, the Root store, the NRPT, the port scan and the elevated
// script, so tests never touch the real system. It reads the scripts the installer builds for
// the commands they contain, which is crude but keeps the tests next to what runs.
type fakeWin struct {
	t        *testing.T
	elevated bool
	// denyTaskControl: this account may not stop or start the task (schtasks /End and /Run fail).
	denyTaskControl bool
	elevatedErr     error // what Elevated returns instead of running the script (a declined prompt)

	registered bool
	taskXML    string
	running    bool
	pid        int
	started    time.Time
	trusted    bool
	rules      []nrptRule
	// listen maps "tcp:443" to the listeners there; while the daemon runs it also listens on
	// daemonPorts.
	listen      map[string][]winListener
	daemonPorts []portSpec

	calls    []string
	scripts  []string // the elevated scripts
	versions string   // the version the daemon reports
}

func decodeCommand(t *testing.T, args []string) string {
	t.Helper()
	for i, a := range args {
		if a == "-EncodedCommand" {
			raw, err := base64.StdEncoding.DecodeString(args[i+1])
			if err != nil {
				t.Fatal(err)
			}
			u := make([]uint16, len(raw)/2)
			for j := range u {
				u[j] = uint16(raw[2*j]) | uint16(raw[2*j+1])<<8
			}
			return string(utf16.Decode(u))
		}
	}
	t.Fatalf("no -EncodedCommand in %v", args)
	return ""
}

func (f *fakeWin) IsElevated() bool { return f.elevated }

func (f *fakeWin) startDaemon() {
	f.running = true
	f.pid++
	f.started = time.Now().Add(time.Hour) // after anything the test writes, so it never looks stale
}

var _ WinRunner = (*fakeWin)(nil)

func (f *fakeWin) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	f.calls = append(f.calls, base+" "+strings.Join(args, " "))
	switch base {
	case "schtasks.exe":
		switch args[0] {
		case "/Query":
			if !f.registered {
				return []byte("ERROR: The system cannot find the file specified."), errors.New("exit status 1")
			}
			return []byte(f.taskXML), nil
		case "/End":
			if f.denyTaskControl {
				return []byte("ERROR: Access is denied."), errors.New("exit status 1")
			}
			if !f.running {
				return nil, errors.New("exit status 1")
			}
			f.running = false
		case "/Run":
			if f.denyTaskControl {
				return []byte("ERROR: Access is denied."), errors.New("exit status 1")
			}
			f.startDaemon()
		}
	case "powershell.exe":
		script := ""
		for _, a := range args {
			if a == "-EncodedCommand" {
				script = decodeCommand(f.t, args)
			}
		}
		switch {
		case strings.Contains(script, "Get-DnsClientNrptRule"):
			out, _ := json.Marshal(append([]nrptRule{}, f.rules...))
			return out, nil
		case strings.Contains(script, "Get-NetTCPConnection"):
			var rows []listenerRow
			add := func(key string, l winListener) {
				rows = append(rows, listenerRow{Proto: l.Proto, Port: l.Port, PID: l.PID, Name: l.Name, Path: l.Path, Services: l.Label})
			}
			for _, ls := range f.listen {
				for _, l := range ls {
					add("", l)
				}
			}
			if f.running {
				for _, p := range f.daemonPorts {
					add("", winListener{Process: Process{PID: f.pid}, Proto: p.proto, Port: p.port, Name: "dev.dotlane.test"})
				}
			}
			out, _ := json.Marshal(rows)
			return out, nil
		case strings.Contains(script, `X509Store]::new('Root', 'LocalMachine')`):
			if !f.trusted {
				return nil, errors.New("exit status 1")
			}
		}
	}
	return nil, nil
}

var (
	hereString = regexp.MustCompile(`(?s)\$xml = @'\n(.*?)'@`)
	namespace  = regexp.MustCompile(`Add-DnsClientNrptRule -Namespace '([^']+)'`)
	commentVar = regexp.MustCompile(`\$comment = '([^']+)'`)
)

func (f *fakeWin) Elevated(_ context.Context, body string) error {
	f.scripts = append(f.scripts, body)
	f.calls = append(f.calls, "elevated")
	if f.elevatedErr != nil {
		return f.elevatedErr
	}
	switch {
	case strings.Contains(body, "Register-ScheduledTask"):
		f.registered = true
		f.taskXML = hereString.FindStringSubmatch(body)[1]
		f.startDaemon()
	case strings.Contains(body, "Unregister-ScheduledTask"):
		f.registered, f.running = false, false
	case strings.Contains(body, "Start-ScheduledTask"):
		f.startDaemon()
	}
	if strings.Contains(body, "-addstore -f Root") {
		f.trusted = true
	}
	if strings.Contains(body, "-delstore Root") {
		f.trusted = false
	}
	if m := namespace.FindStringSubmatch(body); m != nil {
		comment := commentVar.FindStringSubmatch(body)[1]
		f.removeRules(comment, m[1])
		f.rules = append(f.rules, nrptRule{Name: "{" + m[1] + "}", Namespace: m[1], NameServers: "127.0.0.1", Comment: comment})
	} else if strings.Contains(body, "Remove-DnsClientNrptRule") {
		f.removeRules(commentVar.FindStringSubmatch(body)[1], "")
	}
	return nil
}

func (f *fakeWin) removeRules(comment, ns string) {
	var keep []nrptRule
	for _, r := range f.rules {
		if r.Comment != comment || (ns != "" && r.Namespace != ns) {
			keep = append(keep, r)
		}
	}
	f.rules = keep
}

func (f *fakeWin) ran(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeWin) elevatedCalls() int {
	n := 0
	for _, c := range f.calls {
		if c == "elevated" {
			n++
		}
	}
	return n
}

func newTestWindows(t *testing.T) (*Windows, *fakeWin, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		TLDs: []string{"dldev"}, DNSPort: 53, DNSPortSet: false, HTTPPort: 9480, HTTPSPort: 9443,
		StateDir: filepath.Join(dir, "state"), ServiceName: "dev.dotlane.test",
	}
	// A root certificate as the daemon would have created it.
	if err := os.MkdirAll(proxy.CADir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(proxy.CADir(cfg), "root.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: testCA(t)}), 0o644)

	fake := &fakeWin{t: t, listen: map[string][]winListener{}, pid: winDaemonPID - 1, versions: "test"}
	fake.daemonPorts = []portSpec{{"tcp", cfg.HTTPSPort}, {"tcp", cfg.HTTPPort}, {"udp", 53}, {"tcp", 53}}
	out := &bytes.Buffer{}
	// A binary to install. The temp folder moves away, or install would call it a temporary build.
	exe := filepath.Join(dir, "build", "dotlane.exe")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("dotlane build 1"), 0o755)
	for _, env := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(env, filepath.Join(dir, "nonexistent-tmp"))
	}
	w := &Windows{
		Cfg: cfg, ConfigPath: filepath.Join(dir, "config.toml"), Exe: exe,
		ServiceDir: filepath.Join(dir, "home", "AppData", "Local", "Programs", "dotlane"),
		LogDir:     filepath.Join(dir, "home", "AppData", "Local", "dotlane", "logs"),
		UserSID:    "S-1-5-21-1-2-3-1001",
		UI:         ui.Plain(out), Runner: fake,
		Daemon: func(context.Context) (api.Status, error) {
			if !fake.running {
				return api.Status{}, errors.New("not running")
			}
			return api.Status{PID: fake.pid, Version: fake.versions, Started: fake.started}, nil
		},
		Bindable:   func(string, int) error { return nil },
		Lookup:     func(context.Context, string) ([]string, error) { return []string{"127.0.0.1", "::1"}, nil },
		PrivateDir: func(d string) error { return os.MkdirAll(d, 0o700) },
		Owner:      func(string) (bool, error) { return true, nil },
		Firefox:    func() bool { return false },
		Pause:      func(time.Duration) {},
		Timeout:    20 * time.Millisecond,
	}
	return w, fake, out
}

func (f *fakeWin) holder(proto string, port int, l winListener) {
	l.Proto, l.Port = proto, port
	key := fmt.Sprintf("%s:%d", proto, port)
	f.listen[key] = append(f.listen[key], l)
}

func TestInstallThenReinstall(t *testing.T) {
	w, fake, out := newTestWindows(t)
	if err := w.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	// Everything that needs administrator rights ran in one script.
	if n := fake.elevatedCalls(); n != 1 {
		t.Fatalf("%d elevated scripts, want 1:\n%s", n, out)
	}
	script := fake.scripts[0]
	register, trust, dns := strings.Index(script, "Register-ScheduledTask"), strings.Index(script, "-addstore"), strings.Index(script, "Add-DnsClientNrptRule")
	if register < 0 || trust < register || dns < trust {
		t.Errorf("the script should register the task, then trust the CA, then add the DNS rule:\n%s", script)
	}
	for _, want := range []string{"<LogonType>S4U</LogonType>", "dev.dotlane.test.exe", "serve", "dev.dotlane.test.log"} {
		if !strings.Contains(fake.taskXML, want) {
			t.Errorf("task XML lacks %s", want)
		}
	}
	if data, _ := os.ReadFile(w.serviceExe()); string(data) != "dotlane build 1" {
		t.Errorf("the service's copy of the binary = %q", data)
	}
	if !fake.running || !fake.trusted || len(fake.rules) != 1 || fake.rules[0].Namespace != ".dldev" || fake.rules[0].Comment != "dotlane:dev.dotlane.test" {
		t.Errorf("running=%v trusted=%v rules=%+v", fake.running, fake.trusted, fake.rules)
	}
	text := out.String()
	for _, want := range []string{"$ certutil -addstore -f Root", "$ Add-DnsClientNrptRule -Namespace \".dldev\" -NameServers \"127.0.0.1\"",
		"$ Register-ScheduledTask", "needs administrator approval", "Windows asks you once", "Sends lookups for *.dldev",
		"dotlane-check.dldev → this PC", "Dotlane is installed."} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	// Running it again changes nothing and asks for nothing.
	out.Reset()
	if err := w.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := fake.elevatedCalls(); n != 1 {
		t.Errorf("re-install ran %d more elevated scripts:\n%s", n-1, out)
	}
	if n := strings.Count(out.String(), "already"); n != 3 {
		t.Errorf("want 3 'already' lines, got %d:\n%s", n, out)
	}
}

func TestInstallWhenAlreadyElevatedAsksNothing(t *testing.T) {
	w, fake, out := newTestWindows(t)
	fake.elevated = true
	if err := w.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if text := out.String(); strings.Contains(text, "needs administrator approval") || !strings.Contains(text, "already has administrator rights") {
		t.Errorf("wrong wording for an elevated run:\n%s", text)
	}
}

func TestInstallRefusesAnotherDNSPort(t *testing.T) {
	w, fake, out := newTestWindows(t)
	w.Cfg.DNSPort = 10053
	err := w.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "port 53") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if len(fake.calls) != 0 {
		t.Errorf("ran %v before refusing", fake.calls)
	}
	if _, err := os.Stat(w.ServiceDir); err == nil {
		t.Error("the binary was copied")
	}
}

func TestInstallNamesWhoHoldsPort53(t *testing.T) {
	w, fake, out := newTestWindows(t)
	fake.holder("udp", 53, winListener{Process: Process{PID: 1180, Label: "SharedAccess"}, Name: "svchost"})
	err := w.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	for _, want := range []string{"DNS port", ":53 is in use by Internet Connection Sharing", "Stop-Service SharedAccess"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if fake.elevatedCalls() != 0 || fake.registered {
		t.Error("a refused install changed something")
	}
}

func TestInstallNamesHttpSysOnPort443(t *testing.T) {
	w, fake, out := newTestWindows(t)
	fake.holder("tcp", 9443, winListener{Process: Process{PID: 4}, Name: "System"})
	if err := w.Install(context.Background()); err == nil {
		t.Fatalf("installed over http.sys\n%s", out)
	}
	for _, want := range []string{"HTTPS port", "http.sys", "netsh http show servicestate"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestInstallRefusesAnotherToolsRule(t *testing.T) {
	w, fake, out := newTestWindows(t)
	fake.rules = []nrptRule{{Name: "{VPN}", Namespace: ".dldev", NameServers: "10.1.1.1", Comment: "corporate vpn"}}
	err := w.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "DNS rule {VPN} sends it to 10.1.1.1") || !strings.Contains(out.String(), "Remove-DnsClientNrptRule -Name '{VPN}'") {
		t.Errorf("output:\n%s", out)
	}
	if fake.elevatedCalls() != 0 {
		t.Error("a refused install ran a script")
	}
}

func TestInstallRefusesTemporaryBinary(t *testing.T) {
	w, _, _ := newTestWindows(t)
	w.Exe = filepath.Join(os.TempDir(), "go-build123", "dotlane.exe")
	if err := w.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "temporary build") {
		t.Errorf("err = %v", err)
	}
}

func TestInstallRefusesAPathCmdWouldExpand(t *testing.T) {
	w, fake, _ := newTestWindows(t)
	w.ConfigPath = `C:\100%\config.toml`
	if err := w.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "%") {
		t.Errorf("err = %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("ran %v", fake.calls)
	}
}

func TestInstallReportsDeclinedPrompt(t *testing.T) {
	w, fake, out := newTestWindows(t)
	fake.elevatedErr = errors.New("the administrator step did not run: The operation was canceled by the user.")
	err := w.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "administrator steps") || !strings.Contains(err.Error(), "canceled") {
		t.Errorf("err = %v\n%s", err, out)
	}
}

// A rebuilt binary replaces the service's copy (the running one is renamed aside) and restarts the
// daemon through schtasks, without asking for administrator rights.
func TestInstallRestartsAfterRebuild(t *testing.T) {
	w, fake, out := newTestWindows(t)
	if err := w.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	fake.started = time.Now().Add(-time.Hour) // the daemon predates the next build
	pid := fake.pid
	os.WriteFile(w.Exe, []byte("dotlane build 2"), 0o755)

	out.Reset()
	if err := w.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "restarted (to load the rebuilt binary)") {
		t.Errorf("no restart:\n%s", out)
	}
	if fake.pid == pid || fake.elevatedCalls() != 1 {
		t.Errorf("pid %d → %d, elevated scripts %d", pid, fake.pid, fake.elevatedCalls())
	}
	if data, _ := os.ReadFile(w.serviceExe()); string(data) != "dotlane build 2" {
		t.Errorf("the service's copy = %q", data)
	}
	if _, err := os.Stat(w.oldExe()); err == nil {
		t.Error("the previous copy was left behind")
	}
	if !fake.ran("schtasks.exe /End") || !fake.ran("schtasks.exe /Run") {
		t.Errorf("calls: %v", fake.calls)
	}

	// Unchanged: nothing restarts.
	fake.started = time.Now().Add(time.Hour)
	out.Reset()
	if err := w.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "restarted") {
		t.Errorf("an unchanged install restarted the daemon:\n%s", out)
	}
}

func TestInstallRestartsAStuckService(t *testing.T) {
	w, fake, out := newTestWindows(t)
	if err := w.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.running = false
	out.Reset()
	if err := w.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "restarted (it was not answering)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestInstallReplacesATaskMadeForAnotherBinary(t *testing.T) {
	w, fake, out := newTestWindows(t)
	if err := w.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.ConfigPath = filepath.Join(filepath.Dir(w.ConfigPath), "other.toml")
	out.Reset()
	if err := w.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := fake.elevatedCalls(); n != 2 || !strings.Contains(fake.scripts[1], "Register-ScheduledTask") {
		t.Errorf("the task was not replaced (%d scripts)", n)
	}
	if strings.Contains(fake.scripts[1], "-addstore") || strings.Contains(fake.scripts[1], "Add-DnsClientNrptRule") {
		t.Errorf("the second script redid trusted steps:\n%s", fake.scripts[1])
	}
}

func TestRefreshWhenNotInstalled(t *testing.T) {
	w, fake, out := newTestWindows(t)
	if err := w.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing to refresh") {
		t.Errorf("output:\n%s", out)
	}
	if fake.elevatedCalls() != 0 || fake.registered {
		t.Error("refresh installed something")
	}
	if _, err := os.Stat(w.ServiceDir); err == nil {
		t.Error("refresh copied the binary")
	}
}

func TestRefreshRestartsWithoutAdminRights(t *testing.T) {
	w, fake, out := newTestWindows(t)
	if err := w.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.started = time.Now().Add(-time.Hour)
	os.WriteFile(w.Exe, []byte("dotlane build 2"), 0o755)
	out.Reset()
	if err := w.Refresh(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "restarted (to load the rebuilt binary)") || fake.elevatedCalls() != 1 {
		t.Errorf("elevated scripts %d:\n%s", fake.elevatedCalls(), out)
	}
	if !strings.Contains(out.String(), "Dotlane is up to date.") {
		t.Errorf("output:\n%s", out)
	}
}

// When Windows will not let this account stop and start its task, an explicit refresh asks for
// administrator rights, saying why; the automatic upgrade does not ask, and fails with a message.
func TestRefreshAsksWhenWindowsRefuses(t *testing.T) {
	w, fake, out := newTestWindows(t)
	if err := w.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.started = time.Now().Add(-time.Hour)
	fake.denyTaskControl = true
	os.WriteFile(w.Exe, []byte("dotlane build 2"), 0o755)
	pid := fake.pid
	out.Reset()
	if err := w.Refresh(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fake.elevatedCalls() != 2 || !strings.Contains(fake.scripts[1], "Stop-ScheduledTask") || strings.Contains(fake.scripts[1], "Register-ScheduledTask") {
		t.Errorf("want a restart-only script, got %d scripts", fake.elevatedCalls())
	}
	if fake.pid == pid {
		t.Error("the daemon was not restarted")
	}
	for _, want := range []string{"may not stop and start its own task", "$ Stop-ScheduledTask", "restarted (to load the rebuilt binary)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestUpgrade(t *testing.T) {
	w, fake, out := newTestWindows(t)
	ctx := context.Background()
	if ok, err := w.Upgrade(ctx, "0.2.0", out); ok || err != nil {
		t.Fatalf("Upgrade before install = %v, %v", ok, err)
	}
	fake.versions = "0.1.0"
	if err := w.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := w.Upgrade(ctx, "0.1.0", out); ok || err != nil {
		t.Errorf("Upgrade to the same version = %v, %v", ok, err)
	}
	fake.started = time.Now().Add(-time.Hour)
	os.WriteFile(w.Exe, []byte("dotlane build 2"), 0o755)
	var upgraded bytes.Buffer
	pid := fake.pid
	ok, err := w.Upgrade(ctx, "0.2.0", &upgraded)
	if !ok || err != nil {
		t.Fatalf("Upgrade = %v, %v\n%s", ok, err, upgraded.String())
	}
	if fake.pid == pid || fake.elevatedCalls() != 1 {
		t.Errorf("pid %d → %d, elevated scripts %d", pid, fake.pid, fake.elevatedCalls())
	}
	if !strings.Contains(upgraded.String(), "it runs 0.1.0, this is 0.2.0") {
		t.Errorf("output:\n%s", upgraded.String())
	}
}

func TestUpgradeDoesNotAskForAdminRights(t *testing.T) {
	w, fake, out := newTestWindows(t)
	ctx := context.Background()
	fake.versions = "0.1.0"
	if err := w.Install(ctx); err != nil {
		t.Fatal(err)
	}
	fake.started = time.Now().Add(-time.Hour)
	fake.denyTaskControl = true
	os.WriteFile(w.Exe, []byte("dotlane build 2"), 0o755)
	ok, err := w.Upgrade(ctx, "0.2.0", out)
	if ok || err == nil || !strings.Contains(err.Error(), "administrator rights") {
		t.Errorf("Upgrade = %v, %v", ok, err)
	}
	if fake.elevatedCalls() != 1 {
		t.Error("an automatic upgrade ran an elevated script")
	}
}

func TestUninstall(t *testing.T) {
	w, fake, out := newTestWindows(t)
	ctx := context.Background()
	if err := w.Install(ctx); err != nil {
		t.Fatal(err)
	}
	other := nrptRule{Name: "{X}", Namespace: ".corp", NameServers: "10.0.0.1", Comment: "vpn"}
	fake.rules = append(fake.rules, other)
	os.MkdirAll(w.LogDir, 0o755)
	os.WriteFile(w.logPath(), []byte("log"), 0o644)

	out.Reset()
	if err := w.Uninstall(ctx, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := fake.elevatedCalls(); n != 2 { // the install's, and one for everything here
		t.Errorf("%d elevated scripts", n)
	}
	if fake.registered || fake.running || fake.trusted || len(fake.rules) != 1 || fake.rules[0] != other {
		t.Errorf("registered=%v running=%v trusted=%v rules=%+v", fake.registered, fake.running, fake.trusted, fake.rules)
	}
	for _, path := range []string{w.serviceExe(), w.Cfg.StateDir, w.logPath(), w.LogDir} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s left behind", path)
		}
	}
	for _, want := range []string{"$ certutil -delstore Root", "$ Unregister-ScheduledTask", "stopped and removed",
		"removed from the Trusted Root store", "Sites and CA keys", "deleted", "Dotlane is uninstalled."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// Nothing left: a second run asks for nothing.
	out.Reset()
	before := fake.elevatedCalls()
	if err := w.Uninstall(ctx, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fake.elevatedCalls() != before {
		t.Errorf("a second uninstall ran a script:\n%s", out)
	}
	if !strings.Contains(out.String(), "not installed") {
		t.Errorf("output:\n%s", out)
	}
}

func TestUninstallKeepsStateWithoutPurge(t *testing.T) {
	w, _, out := newTestWindows(t)
	ctx := context.Background()
	if err := w.Install(ctx); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := w.Uninstall(ctx, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(w.Cfg.StateDir); err != nil {
		t.Errorf("state deleted without --purge: %v", err)
	}
	if !strings.Contains(out.String(), "dotlane uninstall --purge") {
		t.Errorf("output:\n%s", out)
	}
}

func TestUninstallLeavesAnotherToolsRule(t *testing.T) {
	w, fake, out := newTestWindows(t)
	fake.rules = []nrptRule{{Name: "{VPN}", Namespace: ".dldev", NameServers: "10.1.1.1", Comment: "vpn"}}
	if err := w.Uninstall(context.Background(), false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(fake.rules) != 1 || fake.elevatedCalls() != 0 {
		t.Errorf("rules=%+v, scripts=%d", fake.rules, fake.elevatedCalls())
	}
	if !strings.Contains(out.String(), "is not Dotlane's; left in place") {
		t.Errorf("output:\n%s", out)
	}
}

func TestDoctor(t *testing.T) {
	w, fake, _ := newTestWindows(t)
	ctx := context.Background()

	byName := func(checks []Check) map[string]Check {
		m := map[string]Check{}
		for _, c := range checks {
			m[c.Name] = c
		}
		return m
	}
	before := byName(w.Doctor(ctx))
	if before["Login service"].Status != Failed || before["Login service"].Detail != "not installed" ||
		before["Daemon"].Status != Skipped || before["Certificate authority"].Status != Failed || before["DNS for .dldev"].Status != Failed {
		t.Errorf("before install: %+v", before)
	}

	if err := w.Install(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range w.Doctor(ctx) {
		if c.Status != Pass {
			t.Errorf("after install: %+v", c)
		}
	}

	// Each failure is named and says whether install fixes it.
	fake.rules = nil
	fake.trusted = false
	fake.running = false
	w.Owner = func(string) (bool, error) { return false, nil }
	after := byName(w.Doctor(ctx))
	for name, repairable := range map[string]bool{"Daemon": true, "Certificate authority": true, "DNS for .dldev": true, "Private files": true} {
		if c := after[name]; c.Status != Failed || c.Repairable != repairable {
			t.Errorf("%s: %+v", name, c)
		}
	}
	if !strings.Contains(after["Private files"].Fix, "doctor --fix") {
		t.Errorf("private files fix: %+v", after["Private files"])
	}
}

func TestDoctorFirefoxIsAWarning(t *testing.T) {
	w, _, _ := newTestWindows(t)
	w.Firefox = func() bool { return true }
	for _, c := range w.Doctor(context.Background()) {
		if c.Name == "Firefox" {
			if c.Status != Warning || c.Repairable || !strings.Contains(c.Fix, "security.enterprise_roots.enabled") {
				t.Errorf("Firefox check: %+v", c)
			}
			return
		}
	}
	t.Error("no Firefox check")
}

func TestDoctorDNSPortOtherThan53(t *testing.T) {
	w, _, _ := newTestWindows(t)
	w.Cfg.DNSPort = 10053
	for _, c := range w.Doctor(context.Background()) {
		if c.Name == "DNS port" {
			if c.Status != Failed || !c.Conflict || !strings.Contains(c.Fix, "remove dns_port") {
				t.Errorf("DNS port check: %+v", c)
			}
			return
		}
	}
	t.Error("no DNS port check")
}

func TestDoctorSeesAnExcludedPortRange(t *testing.T) {
	w, _, _ := newTestWindows(t)
	w.Bindable = func(proto string, port int) error {
		if port == w.Cfg.HTTPSPort {
			return fmt.Errorf("listen tcp: %w", wsaeacces)
		}
		return nil
	}
	for _, c := range w.Doctor(context.Background()) {
		if c.Name == "HTTPS port" {
			if c.Status != Failed || !c.Conflict || !strings.Contains(c.Detail, "reserved by Windows") || !strings.Contains(c.Fix, "excludedportrange") {
				t.Errorf("HTTPS port check: %+v", c)
			}
			return
		}
	}
}

func TestDoctorNamesTheOtherDaemonOnOurPort(t *testing.T) {
	w, fake, _ := newTestWindows(t)
	if err := w.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.holder("tcp", 9443, winListener{Process: Process{PID: 999}, Name: "nginx"})
	for _, c := range w.Doctor(context.Background()) {
		if c.Name == "HTTPS port" {
			if c.Status != Failed || !strings.Contains(c.Detail, "nginx") || !strings.Contains(c.Detail, "next to Dotlane") {
				t.Errorf("HTTPS port check: %+v", c)
			}
			return
		}
	}
}

func TestDoctorSeesAnOutdatedCopy(t *testing.T) {
	w, _, _ := newTestWindows(t)
	if err := w.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(w.Exe, []byte("dotlane build 2"), 0o755)
	for _, c := range w.Doctor(context.Background()) {
		if c.Name == "Login service" {
			if c.Status != Warning || c.Detail != "runs an older copy of this binary" {
				t.Errorf("service check: %+v", c)
			}
			return
		}
	}
}

func TestTightenPermissions(t *testing.T) {
	w, _, out := newTestWindows(t)
	os.MkdirAll(w.Cfg.StateDir, 0o700)
	calls := 0
	w.Owner = func(string) (bool, error) { return false, nil }
	w.PrivateDir = func(string) error { calls++; return nil }
	if err := w.tightenPermissions(); err != nil || calls != 1 || !strings.Contains(out.String(), "limited to you and SYSTEM") {
		t.Errorf("err=%v calls=%d\n%s", err, calls, out)
	}
	w.Owner = func(string) (bool, error) { return true, nil }
	if err := w.tightenPermissions(); err != nil || calls != 1 {
		t.Errorf("tightened a private folder: err=%v calls=%d", err, calls)
	}
}

func TestMigrateHasNothingToDo(t *testing.T) {
	w, _, _ := newTestWindows(t)
	if err := w.Migrate(context.Background()); err == nil || !strings.Contains(err.Error(), "nothing to migrate") {
		t.Errorf("err = %v", err)
	}
}

func TestThumbprintIsTheCertificatesSHA1(t *testing.T) {
	der := testCA(t)
	path := filepath.Join(t.TempDir(), "root.crt")
	os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	sum := sha1.Sum(der)
	hash, name, err := thumbprint(path)
	if err != nil || hash != strings.ToUpper(hex.EncodeToString(sum[:])) || name != testCAName {
		t.Errorf("thumbprint = %s, %s, %v", hash, name, err)
	}
	if _, _, err := thumbprint(filepath.Join(t.TempDir(), "none.crt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
}
