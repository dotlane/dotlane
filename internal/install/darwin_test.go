//go:build darwin

package install

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pedrodee/dotlane/internal/api"
	"github.com/pedrodee/dotlane/internal/config"
	"github.com/pedrodee/dotlane/internal/proxy"
	"github.com/pedrodee/dotlane/internal/ui"
)

const daemonPID = 4242

// fakeMac simulates launchd, the keychain, lsof and sudo, so tests never touch the real system.
type fakeMac struct {
	loaded, trusted, inKeychain bool
	hash                        string
	calls                       []string
	sudoCalls                   int
	// listen maps "tcp:443" or "udp:10053" to the PIDs listening there; while the service is
	// loaded, the daemon also listens on daemonPorts.
	listen      map[string][]int
	daemonPorts []string
	paths       map[int]string // pid → executable
	labels      map[int]string // pid → launchd label
	// Other launchd jobs, by label (the prototype's Caddy and dnsmasq): loaded or not, the ports
	// each listens on while loaded, and the ones disabled.
	service  string
	others   map[string]bool
	ports    map[string][]string
	disabled map[string]bool
}

// label is the job a launchctl target ("gui/501/x") or plist path ("…/x.plist") names.
func label(arg string) string {
	return strings.TrimSuffix(filepath.Base(arg), ".plist")
}

func (f *fakeMac) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, filepath.Base(name)+" "+strings.Join(args, " "))
	switch filepath.Base(name) + " " + args[0] {
	case "launchctl print":
		if l := label(args[1]); l != f.service {
			if !f.others[l] {
				return nil, errors.New("not loaded")
			}
		} else if !f.loaded {
			return nil, errors.New("not loaded")
		}
	case "launchctl bootstrap":
		if l := label(args[2]); l != f.service {
			if f.disabled[l] {
				return nil, errors.New("service is disabled")
			}
			f.others[l] = true
		} else {
			f.loaded = true
		}
	case "launchctl bootout":
		if l := label(args[1]); l != f.service {
			f.others[l] = false
		} else {
			f.loaded = false
		}
	case "launchctl disable":
		f.disabled[label(args[1])] = true
	case "launchctl enable":
		f.disabled[label(args[1])] = false
	case "brew services":
		f.others["sh.brew.caddy"] = args[1] == "start"
	case "launchctl list":
		var b strings.Builder
		for pid, label := range f.labels {
			fmt.Fprintf(&b, "%d\t0\t%s\n", pid, label)
		}
		return []byte(b.String()), nil
	case "security verify-cert":
		if !f.trusted {
			return nil, errors.New("CSSMERR_TP_NOT_TRUSTED")
		}
	case "security find-certificate":
		if f.inKeychain {
			return []byte("SHA-1 hash: " + f.hash + "\n"), nil
		}
	case "lsof -nP":
		key := strings.ToLower(strings.TrimPrefix(args[3], "-i")) // "-iTCP:443" → "tcp:443"
		pids := append([]int(nil), f.listen[key]...)
		if f.loaded && slicesContains(f.daemonPorts, key) {
			pids = append(pids, daemonPID)
		}
		for pid, l := range f.labels {
			if f.others[l] && slicesContains(f.ports[l], key) {
				pids = append(pids, pid)
			}
		}
		if len(pids) == 0 {
			return nil, errors.New("exit status 1")
		}
		var b strings.Builder
		for _, pid := range pids {
			fmt.Fprintf(&b, "p%d\nn127.0.0.1:%s\n", pid, strings.Split(key, ":")[1])
		}
		return []byte(b.String()), nil
	case "ps -o":
		pid, _ := strconv.Atoi(args[3])
		return []byte(f.paths[pid] + "\n"), nil
	}
	return nil, nil
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (f *fakeMac) Sudo(_ context.Context, stdin []byte, name string, args ...string) error {
	f.sudoCalls++
	f.calls = append(f.calls, "sudo "+filepath.Base(name)+" "+strings.Join(args, " "))
	switch filepath.Base(name) {
	case "mkdir":
		return os.MkdirAll(args[len(args)-1], 0o755)
	case "tee":
		return os.WriteFile(args[0], stdin, 0o644)
	case "rm":
		return os.Remove(args[0])
	case "security":
		switch args[0] {
		case "add-trusted-cert":
			f.trusted, f.inKeychain = true, true
		case "remove-trusted-cert":
			f.trusted = false
		case "delete-certificate":
			f.inKeychain = false
		}
	}
	return nil
}

func (f *fakeMac) ran(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func newTestMac(t *testing.T) (*Mac, *fakeMac, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		TLDs: []string{"dldev"}, DNSPort: 10054, DNSPortSet: true, HTTPPort: 9480, HTTPSPort: 9443,
		StateDir: filepath.Join(dir, "state"), ServiceName: "dev.dotlane.test",
	}
	// A root certificate as the daemon would have created it.
	if err := os.MkdirAll(proxy.CADir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	der := []byte("not really DER, but hashed the same way")
	os.WriteFile(filepath.Join(proxy.CADir(cfg), "root.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	sum := sha1.Sum(der)

	fake := &fakeMac{
		hash:   strings.ToUpper(hex.EncodeToString(sum[:])),
		listen: map[string][]int{}, paths: map[int]string{},
		labels:  map[int]string{daemonPID: cfg.ServiceName},
		service: cfg.ServiceName, others: map[string]bool{}, ports: map[string][]string{}, disabled: map[string]bool{},
	}
	out := &bytes.Buffer{}
	m := &Mac{
		Cfg: cfg, ConfigPath: filepath.Join(dir, "config.toml"), Exe: "/opt/dotlane/bin/dotlane",
		Home: filepath.Join(dir, "home"), UID: 501,
		ResolverDir: filepath.Join(dir, "resolver"),
		UI:          ui.Plain(out), Runner: fake,
		Daemon: func(context.Context) (api.Status, error) {
			if !fake.loaded {
				return api.Status{}, errors.New("not running")
			}
			return api.Status{PID: daemonPID, Version: "test"}, nil
		},
		Bindable: func(string, int) error { return nil },
		Lookup:   func(context.Context, string) ([]string, error) { return []string{"127.0.0.1", "::1"}, nil },
	}
	fake.daemonPorts = daemonPorts(cfg)
	return m, fake, out
}

func daemonPorts(cfg *config.Config) []string {
	return []string{
		fmt.Sprintf("tcp:%d", cfg.HTTPSPort), fmt.Sprintf("tcp:%d", cfg.HTTPPort), fmt.Sprintf("udp:%d", cfg.DNSPort),
	}
}

func TestInstallThenReinstall(t *testing.T) {
	m, fake, out := newTestMac(t)
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	plist, err := os.ReadFile(m.plistPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<string>dev.dotlane.test</string>", "<string>/opt/dotlane/bin/dotlane</string>",
		"<string>serve</string>", "<key>KeepAlive</key><true/>",
	} {
		if !bytes.Contains(plist, []byte(want)) {
			t.Errorf("plist lacks %s", want)
		}
	}
	resolver, _ := os.ReadFile(filepath.Join(m.ResolverDir, "dldev"))
	if string(resolver) != "nameserver 127.0.0.1\nport 10054\n" {
		t.Errorf("resolver file = %q", resolver)
	}
	if !fake.loaded || !fake.trusted {
		t.Errorf("loaded=%v trusted=%v", fake.loaded, fake.trusted)
	}
	text := out.String()
	for _, want := range []string{"$ sudo /usr/bin/security add-trusted-cert", "$ sudo /usr/bin/tee", "Sends lookups for *.dldev", "dotlane-check.dldev → this Mac", "Dotlane is installed."} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	// Running it again changes nothing and asks for no password: the daemon's own ports are
	// not conflicts.
	before := fake.sudoCalls
	out.Reset()
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fake.sudoCalls != before {
		t.Errorf("re-install ran sudo %d more times:\n%s", fake.sudoCalls-before, out)
	}
	if n := strings.Count(out.String(), "already"); n != 3 {
		t.Errorf("want 3 'already' lines, got %d:\n%s", n, out)
	}
}

// The reference install: Herd's uninstaller left /etc/resolver/<tld> pointing at port 1053, where
// Yerd now answers.
func TestInstallNamesResolverOwner(t *testing.T) {
	m, fake, out := newTestMac(t)
	os.MkdirAll(m.ResolverDir, 0o755)
	os.WriteFile(filepath.Join(m.ResolverDir, "dldev"), []byte("nameserver 127.0.0.1\nport 1053\n"), 0o644)
	fake.listen["udp:1053"] = []int{71950}
	fake.paths[71950] = "yerdd"
	fake.labels[71950] = "dev.yerd.daemon"

	err := m.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"sends .dldev to 127.0.0.1:1053, answered by Yerd", "launchctl bootout gui/501/dev.yerd.daemon", "sudo rm "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if fake.ran("launchctl bootstrap") || fake.sudoCalls > 0 {
		t.Errorf("changed things before refusing: %v", fake.calls)
	}
}

func TestInstallNamesLeftoverResolver(t *testing.T) {
	m, _, out := newTestMac(t)
	os.MkdirAll(m.ResolverDir, 0o755)
	os.WriteFile(filepath.Join(m.ResolverDir, "dldev"), []byte("nameserver 127.0.0.1\nport 1053\n"), 0o644)
	if err := m.Install(context.Background()); err == nil {
		t.Fatal("installed over a foreign resolver file")
	}
	if !strings.Contains(out.String(), "where nothing answers (left behind by an uninstalled tool)") {
		t.Errorf("output:\n%s", out)
	}
}

// The prototype's Caddy holds 443: install must refuse, name it and say how to stop it.
func TestInstallRefusesTakenHTTPSPort(t *testing.T) {
	m, fake, out := newTestMac(t)
	fake.listen["tcp:9443"] = []int{2779}
	fake.paths[2779] = "/opt/homebrew/opt/caddy/bin/caddy"
	fake.labels[2779] = "sh.brew.caddy"
	sites := filepath.Join(m.Home, ".config", "caddy", "sites")
	os.MkdirAll(sites, 0o755)
	os.WriteFile(filepath.Join(sites, "myapp.caddy"), []byte("# Managed by dotlane: https://myapp.traix -> localhost:5173\n"), 0o644)

	if err := m.Install(context.Background()); err == nil {
		t.Fatal("installed with 443 taken")
	}
	for _, want := range []string{"✗ HTTPS port", ":9443 is in use by the Dotlane prototype's Caddy", "fix: brew services stop caddy"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if fake.ran("launchctl bootstrap") {
		t.Error("service installed despite the conflict")
	}
}

// A default DNS port held by another tool is not a reason to stop: pick the next free one, save
// it, and write the resolver file for it.
func TestInstallPicksFreeDNSPort(t *testing.T) {
	m, fake, out := newTestMac(t)
	m.Cfg.DNSPort, m.Cfg.DNSPortSet = 10053, false
	fake.listen["udp:10053"] = []int{2588}
	fake.paths[2588] = "/opt/homebrew/sbin/dnsmasq"
	fake.labels[2588] = "dev.traix.dnsmasq"
	fake.listen["udp:10054"] = []int{999} // also taken: skip it
	fake.paths[999] = "/usr/local/bin/something"
	fake.daemonPorts[2] = "udp:10055"

	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), ":10053 is in use by the Dotlane prototype's dnsmasq; using :10055") {
		t.Errorf("output:\n%s", out)
	}
	saved, _ := os.ReadFile(m.ConfigPath)
	if !strings.Contains(string(saved), "dns_port = 10055") || !strings.Contains(string(saved), "prototype's dnsmasq") {
		t.Errorf("config file = %q", saved)
	}
	resolver, _ := os.ReadFile(filepath.Join(m.ResolverDir, "dldev"))
	if string(resolver) != "nameserver 127.0.0.1\nport 10055\n" {
		t.Errorf("resolver file = %q", resolver)
	}
}

// A refused install must not have saved a DNS port either.
func TestRefusedInstallSavesNothing(t *testing.T) {
	m, fake, _ := newTestMac(t)
	m.Cfg.DNSPort, m.Cfg.DNSPortSet = 10053, false
	fake.listen["udp:10053"] = []int{2588}
	fake.paths[2588] = "/opt/homebrew/sbin/dnsmasq"
	fake.listen["tcp:9443"] = []int{2779}
	fake.paths[2779] = "/opt/homebrew/opt/caddy/bin/caddy"
	if err := m.Install(context.Background()); err == nil {
		t.Fatal("installed with 443 taken")
	}
	if _, err := os.Stat(m.ConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config file written by a refused install")
	}
	if m.Cfg.DNSPort != 10053 {
		t.Errorf("DNSPort = %d", m.Cfg.DNSPort)
	}
}

func TestInstallKeepsExplicitDNSPort(t *testing.T) {
	m, fake, _ := newTestMac(t)
	fake.listen["udp:10054"] = []int{2588}
	fake.paths[2588] = "/opt/homebrew/sbin/dnsmasq"
	if err := m.Install(context.Background()); err == nil {
		t.Error("moved a DNS port the config file chose")
	}
}

func TestInstallRefusesTemporaryBinary(t *testing.T) {
	m, _, _ := newTestMac(t)
	m.Exe = "/private/var/folders/x/go-build123/b001/exe/dotlane"
	if err := m.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "temporary build") {
		t.Errorf("err = %v", err)
	}
}

func TestInstallRestartsStuckService(t *testing.T) {
	m, fake, out := newTestMac(t)
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Daemon = func(context.Context) (api.Status, error) {
		if !fake.ran("launchctl kickstart") {
			return api.Status{}, errors.New("not answering")
		}
		return api.Status{PID: daemonPID}, nil
	}
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !fake.ran("launchctl kickstart -k gui/501/dev.dotlane.test") || !strings.Contains(out.String(), "restarted") {
		t.Errorf("not restarted:\n%s", out)
	}
}

func statuses(checks []Check) map[string]CheckStatus {
	m := map[string]CheckStatus{}
	for _, c := range checks {
		m[c.Name] = c.Status
	}
	return m
}

func TestDoctor(t *testing.T) {
	m, fake, out := newTestMac(t)

	// Nothing installed: each missing piece is reported, fixable by install.
	before := statuses(m.Doctor(context.Background()))
	for name, want := range map[string]CheckStatus{
		"Config": Pass, "Login service": Failed, "Daemon": Skipped, "HTTPS port": Pass,
		"Certificate authority": Skipped, "DNS for .dldev": Failed,
	} {
		if before[name] != want {
			t.Errorf("before install, %s = %s, want %s", name, before[name], want)
		}
	}

	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range m.Doctor(context.Background()) {
		if c.Status != Pass {
			t.Errorf("after install: %+v", c)
		}
	}

	// Another server joins the daemon on 443 (SO_REUSEPORT): a conflict, not "Dotlane".
	fake.listen["tcp:9443"] = []int{2779}
	fake.paths[2779] = "/opt/homebrew/opt/caddy/bin/caddy"
	for _, c := range m.Doctor(context.Background()) {
		if c.Name == "HTTPS port" && (c.Status != Failed || !c.Conflict || !strings.Contains(c.Detail, "next to Dotlane")) {
			t.Errorf("shared port: %+v", c)
		}
	}
}

// Before install, a taken default DNS port is only a warning; once the port is explicit, it is a
// conflict.
func TestDoctorDNSPortBeforeInstall(t *testing.T) {
	m, fake, _ := newTestMac(t)
	fake.listen["udp:10054"] = []int{2588}
	fake.paths[2588] = "/opt/homebrew/sbin/dnsmasq"
	fake.labels[2588] = "dev.traix.dnsmasq"
	m.Cfg.DNSPortSet = false
	if got := statuses(m.Doctor(context.Background()))["DNS port"]; got != Warning {
		t.Errorf("default port: %s, want warn", got)
	}
	m.Cfg.DNSPortSet = true
	if got := statuses(m.Doctor(context.Background()))["DNS port"]; got != Failed {
		t.Errorf("explicit port: %s, want fail", got)
	}

	// The prototype's resolver file matches, but dnsmasq answers it.
	os.MkdirAll(m.ResolverDir, 0o755)
	os.WriteFile(filepath.Join(m.ResolverDir, "dldev"), m.resolverFile(), 0o644)
	for _, c := range m.Doctor(context.Background()) {
		if c.Name == "DNS for .dldev" && (c.Status != Warning || !strings.Contains(c.Detail, "answered by the Dotlane prototype's dnsmasq")) {
			t.Errorf("%+v", c)
		}
	}
}

func TestDoctorSeesHiddenListener(t *testing.T) {
	m, _, _ := newTestMac(t)
	m.Bindable = func(proto string, port int) error {
		if port == 9480 {
			return errors.New("address already in use")
		}
		return nil
	}
	found := false
	for _, c := range m.Doctor(context.Background()) {
		if c.Name == "HTTP port" {
			found = true
			if c.Status != Failed || !strings.Contains(c.Fix, "sudo lsof -nP -iTCP:9480") {
				t.Errorf("%+v", c)
			}
		}
	}
	if !found {
		t.Error("no HTTP port check")
	}
}

func TestUninstall(t *testing.T) {
	m, fake, out := newTestMac(t)
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := m.Uninstall(context.Background(), false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fake.loaded || fake.trusted || fake.inKeychain {
		t.Errorf("left behind: loaded=%v trusted=%v inKeychain=%v", fake.loaded, fake.trusted, fake.inKeychain)
	}
	for _, p := range []string{m.plistPath(), filepath.Join(m.ResolverDir, "dldev")} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists", p)
		}
	}
	if _, err := os.Stat(m.Cfg.StateDir); err != nil {
		t.Errorf("state removed without --purge: %v", err)
	}
	if !strings.Contains(out.String(), "--purge") {
		t.Errorf("output does not mention --purge:\n%s", out)
	}

	// Uninstalling twice is harmless; purge then deletes the state and the logs folder.
	if err := m.Uninstall(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{m.Cfg.StateDir, filepath.Dir(m.logPath())} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s kept with --purge", p)
		}
	}
}

func TestUninstallLeavesForeignResolver(t *testing.T) {
	m, _, out := newTestMac(t)
	os.MkdirAll(m.ResolverDir, 0o755)
	foreign := filepath.Join(m.ResolverDir, "dldev")
	os.WriteFile(foreign, []byte("nameserver 10.0.0.1\n"), 0o644)
	if err := m.Uninstall(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("foreign resolver removed")
	}
	if !strings.Contains(out.String(), "not Dotlane's") {
		t.Errorf("output:\n%s", out)
	}
}
