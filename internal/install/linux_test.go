//go:build linux

package install

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dotlane/dotlane/internal/api"
	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/proxy"
	"github.com/dotlane/dotlane/internal/ui"
)

const linuxDaemonPID = 4242

// linuxCA is a self-signed root certificate as PEM, valid now.
func linuxCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Dotlane Local Authority - ECC Root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// fakeLinux simulates systemd, systemd-resolved, ss, certutil, the trust tools and sudo, over
// files in a temporary folder, so tests never touch the real system.
type fakeLinux struct {
	m                       *Linux
	noManager, noResolved   bool
	userEnabled, userActive bool
	dnsEnabled, dnsActive   bool
	dnsOn                   bool              // resolved routes the link
	leak                    bool              // resolved makes the link a default route, until it is set again
	otherDomains            string            // lines of `resolvectl domain` that are not ours
	ss                      string            // output of ss
	nss                     map[string][]byte // database dir → PEM under the nickname
	calls                   []string
	sudoCalls               int
}

func (f *fakeLinux) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, filepath.Base(name)+" "+strings.Join(args, " "))
	fail := errors.New("exit status 1")
	switch filepath.Base(name) {
	case "systemctl":
		if args[0] == "--user" {
			switch args[1] {
			case "show-environment":
				if f.noManager {
					return []byte("Failed to connect to bus"), fail
				}
			case "is-enabled":
				if !f.userEnabled {
					return []byte("disabled"), fail
				}
			case "is-active":
				if !f.userActive {
					return []byte("inactive"), fail
				}
			case "enable":
				f.userEnabled, f.userActive = true, true
			case "restart":
				f.userActive = true
			case "disable":
				f.userEnabled, f.userActive = false, false
			}
			return nil, nil
		}
		switch {
		case args[0] == "is-active" && args[len(args)-1] == "systemd-resolved":
			if f.noResolved {
				return nil, fail
			}
		case args[0] == "is-active" && !f.dnsActive, args[0] == "is-enabled" && !f.dnsEnabled:
			return nil, fail
		}
	case "resolvectl":
		switch {
		case args[0] == "--version":
			return []byte("systemd 259 (259.5)\n"), nil
		case args[0] == "dns":
			if !f.dnsOn {
				return []byte("Link 3 (" + f.m.dnsLink() + "):\n"), nil
			}
			return []byte(fmt.Sprintf("Link 3 (%s): 127.0.0.1:%d\n", f.m.dnsLink(), f.m.Cfg.DNSPort)), nil
		case args[0] == "default-route":
			if f.leak {
				return []byte("Link 3 (" + f.m.dnsLink() + "): yes\n"), nil
			}
			return []byte("Link 3 (" + f.m.dnsLink() + "): no\n"), nil
		case args[0] == "domain":
			out := f.otherDomains
			if f.dnsOn {
				out += fmt.Sprintf("Link 3 (%s): ~%s\n", f.m.dnsLink(), strings.Join(f.m.Cfg.TLDs, " ~"))
			}
			return []byte(out), nil
		}
	case "ss":
		out := f.ss
		if f.userActive { // the daemon's own sockets
			c := f.m.Cfg
			out += fmt.Sprintf("tcp LISTEN 0 4096 127.0.0.1:%d 0.0.0.0:* users:((\"dotlane\",pid=%d,fd=3))\n", c.HTTPSPort, linuxDaemonPID)
			out += fmt.Sprintf("tcp LISTEN 0 4096 127.0.0.1:%d 0.0.0.0:* users:((\"dotlane\",pid=%d,fd=4))\n", c.HTTPPort, linuxDaemonPID)
			out += fmt.Sprintf("udp UNCONN 0 0 127.0.0.1:%d 0.0.0.0:* users:((\"dotlane\",pid=%d,fd=5))\n", c.DNSPort, linuxDaemonPID)
		}
		return []byte(out), nil
	case "certutil":
		dir := ""
		for i, a := range args {
			if a == "-d" {
				dir = strings.TrimPrefix(args[i+1], "sql:")
			}
		}
		switch args[0] {
		case "-L":
			if pem, ok := f.nss[dir]; ok {
				return pem, nil
			}
			return nil, fail
		case "-N":
			f.nss[dir] = nil
			os.WriteFile(filepath.Join(dir, "cert9.db"), nil, 0o600)
			delete(f.nss, dir)
		case "-A":
			f.nss[dir], _ = os.ReadFile(args[len(args)-1])
		case "-D":
			delete(f.nss, dir)
		}
	}
	return nil, nil
}

func (f *fakeLinux) Sudo(_ context.Context, stdin []byte, name string, args ...string) error {
	f.sudoCalls++
	f.calls = append(f.calls, "sudo "+filepath.Base(name)+" "+strings.Join(args, " "))
	switch filepath.Base(name) {
	case "mkdir":
		return os.MkdirAll(args[len(args)-1], 0o755)
	case "tee":
		return os.WriteFile(args[0], stdin, 0o644)
	case "rm":
		return os.Remove(args[0])
	case "sysctl":
		if args[0] == "-p" {
			data, _ := os.ReadFile(args[1])
			var v int
			fmt.Sscanf(string(data[bytes.Index(data, []byte("="))+1:]), "%d", &v)
			return os.WriteFile(f.m.PortStartFile, []byte(fmt.Sprintln(v)), 0o644)
		}
		return os.WriteFile(f.m.PortStartFile, []byte("1024\n"), 0o644)
	case "systemctl":
		unit := args[len(args)-1]
		switch args[0] {
		case "enable":
			f.dnsEnabled = unit == f.m.dnsUnitName() || f.dnsEnabled
		case "restart":
			f.dnsActive, f.dnsOn = true, true
		case "disable":
			f.dnsEnabled, f.dnsActive, f.dnsOn = false, false, false
		}
	case "resolvectl":
		if args[0] == "default-route" {
			f.leak = false
		}
	case "update-ca-certificates", "update-ca-trust":
		// Rebuild the bundle from every anchor folder.
		var bundle []byte
		for _, s := range f.m.Stores {
			files, _ := filepath.Glob(filepath.Join(s.Dir, "*.crt"))
			for _, file := range files {
				data, _ := os.ReadFile(file)
				bundle = append(bundle, data...)
			}
		}
		return os.WriteFile(f.m.Bundles[0], bundle, 0o644)
	}
	return nil
}

func (f *fakeLinux) ran(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func newTestLinux(t *testing.T) (*Linux, *fakeLinux, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		TLDs: []string{"dldev"}, DNSPort: 10054, DNSPortSet: true, HTTPPort: 9480, HTTPSPort: 9443,
		StateDir: filepath.Join(dir, "state"), ServiceName: "dev.dotlane.test",
	}
	if err := os.MkdirAll(proxy.CADir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(proxy.CADir(cfg), "root.crt"), linuxCA(t), 0o644)

	exe := filepath.Join(dir, "build", "dotlane")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("dotlane build 1"), 0o755)
	t.Setenv("TMPDIR", "/nonexistent-tmp") // or install would call the binary a temporary build

	anchors := filepath.Join(dir, "anchors")
	os.MkdirAll(anchors, 0o755)
	os.WriteFile(filepath.Join(dir, "resolv.conf"), []byte("# stub\nnameserver 127.0.0.53\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "port_start"), []byte("1024\n"), 0o644)
	out := &bytes.Buffer{}
	fake := &fakeLinux{nss: map[string][]byte{}}
	m := &Linux{
		Cfg: cfg, ConfigPath: filepath.Join(dir, "config.toml"), Exe: exe,
		ServiceDir: filepath.Join(dir, "home", ".local", "share", "dotlane", "bin"),
		Home:       filepath.Join(dir, "home"), UID: os.Getuid(),
		UnitDir:       filepath.Join(dir, "home", ".config", "systemd", "user"),
		SystemUnitDir: filepath.Join(dir, "system"), SysctlFile: filepath.Join(dir, "50-dotlane-dev.dotlane.test.conf"),
		PortStartFile: filepath.Join(dir, "port_start"), ResolvConf: filepath.Join(dir, "resolv.conf"),
		ProcDir:      filepath.Join(dir, "proc"),
		Stores:       []TrustStore{{Tool: "update-ca-trust", Dir: anchors, NSSPackage: "nss-tools"}},
		Bundles:      []string{filepath.Join(dir, "bundle.crt")},
		FirefoxGlobs: []string{".mozilla/firefox/*"},
		UI:           ui.Plain(out), Runner: fake,
		LookPath:   func(n string) (string, error) { return "/usr/bin/" + n, nil },
		LookSystem: func(n string) (string, error) { return "/usr/bin/" + n, nil },
		Daemon: func(context.Context) (api.Status, error) {
			if !fake.userActive {
				return api.Status{}, errors.New("not running")
			}
			// Started after anything the test writes, so it never looks stale.
			return api.Status{PID: linuxDaemonPID, Version: "test", Started: time.Now().Add(time.Hour)}, nil
		},
		Bindable: func(string, int) error { return nil },
		Lookup:   func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil },
	}
	os.MkdirAll(m.SystemUnitDir, 0o755)
	fake.m = m
	return m, fake, out
}

func TestLinuxInstallThenReinstall(t *testing.T) {
	m, fake, out := newTestLinux(t)
	m.Cfg.HTTPPort = 80 // low, so the sysctl step has work to do
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	unit, _ := os.ReadFile(m.unitPath())
	for _, want := range []string{"ExecStart=" + m.serviceExe() + " --config " + m.ConfigPath + " serve", "Restart=on-failure", "WantedBy=default.target"} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	dns, _ := os.ReadFile(m.dnsUnitPath())
	for _, want := range []string{"resolvectl dns " + m.dnsLink() + " 127.0.0.1:10054", "resolvectl domain " + m.dnsLink() + " ~dldev", "default-route " + m.dnsLink() + " no", "link add " + m.dnsLink() + " type dummy"} {
		if !strings.Contains(string(dns), want) {
			t.Errorf("DNS unit lacks %q:\n%s", want, dns)
		}
	}
	if sysctl, _ := os.ReadFile(m.SysctlFile); !strings.Contains(string(sysctl), portStartKey+"=80\n") {
		t.Errorf("sysctl file = %q", sysctl)
	}
	if _, err := os.Stat(m.anchorPath(m.Stores[0])); err != nil {
		t.Errorf("no anchor: %v", err)
	}
	if !m.caTrusted() {
		t.Error("CA not trusted after install")
	}
	if len(fake.nss) != 1 {
		t.Errorf("NSS databases = %v", fake.nss)
	}
	text := out.String()
	for _, want := range []string{"$ sudo /usr/bin/tee " + m.SysctlFile, "$ sudo /usr/bin/update-ca-trust", "Sends lookups for *.dldev", "dotlane-check.dldev → this machine", "Dotlane is installed."} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	// Again: nothing changes, nothing asks for a password.
	before := fake.sudoCalls
	out.Reset()
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fake.sudoCalls != before {
		t.Errorf("re-install ran sudo %d more times:\n%s", fake.sudoCalls-before, out)
	}
	if n := strings.Count(out.String(), "already"); n != 5 {
		t.Errorf("want 5 'already' lines, got %d:\n%s", n, out)
	}
}

func TestLinuxInstallSkipsOpenPorts(t *testing.T) {
	m, fake, out := newTestLinux(t) // ports 9480 and 9443: the default limit of 1024 allows them
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fake.ran("sudo tee " + m.SysctlFile) {
		t.Error("wrote the sysctl file for high ports")
	}
	if !strings.Contains(out.String(), "Low ports") {
		t.Errorf("output:\n%s", out)
	}
}

func TestLinuxInstallWithoutCertutil(t *testing.T) {
	m, _, out := newTestLinux(t)
	m.LookPath = func(n string) (string, error) {
		if n == "certutil" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + n, nil
	}
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "Install the nss-tools package") {
		t.Errorf("output:\n%s", out)
	}
}

func TestLinuxInstallRefusesWithoutResolved(t *testing.T) {
	for name, tc := range map[string]func(*Linux, *fakeLinux){
		"stopped":     func(_ *Linux, f *fakeLinux) { f.noResolved = true },
		"no stub":     func(m *Linux, _ *fakeLinux) { os.WriteFile(m.ResolvConf, []byte("nameserver 192.168.1.1\n"), 0o644) },
		"no manager":  func(_ *Linux, f *fakeLinux) { f.noManager = true },
		"foreign tld": func(_ *Linux, f *fakeLinux) { f.otherDomains = "Link 2 (docker0): ~dldev\n" },
	} {
		t.Run(name, func(t *testing.T) {
			m, fake, out := newTestLinux(t)
			tc(m, fake)
			err := m.Install(context.Background())
			if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
				t.Fatalf("err = %v\n%s", err, out)
			}
			if fake.sudoCalls > 0 || fake.ran("systemctl --user enable") {
				t.Errorf("changed things before refusing: %v", fake.calls)
			}
		})
	}
}

func TestLinuxInstallRefusesTakenPort(t *testing.T) {
	m, fake, out := newTestLinux(t)
	fake.ss = `tcp LISTEN 0 511 0.0.0.0:9443 0.0.0.0:* users:(("nginx",pid=77,fd=6),("nginx",pid=78,fd=6))` + "\n"
	pdir := filepath.Join(m.ProcDir, "77")
	os.MkdirAll(pdir, 0o755)
	os.WriteFile(filepath.Join(pdir, "cgroup"), []byte("0::/system.slice/nginx.service\n"), 0o644)

	if err := m.Install(context.Background()); err == nil {
		t.Fatal("installed with 9443 taken")
	}
	for _, want := range []string{"✗ HTTPS port", ":9443 is in use by nginx", "fix: sudo systemctl disable --now nginx.service"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if fake.sudoCalls > 0 {
		t.Error("ran sudo despite the conflict")
	}
}

func TestLinuxInstallPicksFreeDNSPort(t *testing.T) {
	m, fake, out := newTestLinux(t)
	m.Cfg.DNSPort, m.Cfg.DNSPortSet = 10053, false
	fake.ss = `udp UNCONN 0 0 127.0.0.1:10053 0.0.0.0:* users:(("dnsmasq",pid=9,fd=4))` + "\n" +
		`udp UNCONN 0 0 *:10054 *:*` + "\n"
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), ":10053 is in use by dnsmasq; using :10055") {
		t.Errorf("output:\n%s", out)
	}
	if saved, _ := os.ReadFile(m.ConfigPath); !strings.Contains(string(saved), "dns_port = 10055") {
		t.Errorf("config file = %q", saved)
	}
	if dns, _ := os.ReadFile(m.dnsUnitPath()); !strings.Contains(string(dns), "127.0.0.1:10055") {
		t.Errorf("DNS unit:\n%s", dns)
	}
}

func TestLinuxInstallRefusesTemporaryBinary(t *testing.T) {
	m, _, _ := newTestLinux(t)
	m.Exe = filepath.Join(os.TempDir(), "go-build123", "dotlane")
	if err := m.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "temporary build") {
		t.Fatalf("err = %v", err)
	}
}

func TestLinuxInstallRestartsAfterRebuild(t *testing.T) {
	m, fake, out := newTestLinux(t)
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	m.Daemon = func(context.Context) (api.Status, error) { // started before the binary changed
		return api.Status{PID: linuxDaemonPID, Version: "test", Started: time.Now().Add(-time.Hour)}, nil
	}
	os.WriteFile(m.Exe, []byte("dotlane build 2"), 0o755)
	out.Reset()
	fake.calls = nil
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "restarted (to load the rebuilt binary)") || !fake.ran("systemctl --user restart") {
		t.Errorf("no restart:\n%s", out)
	}
	if i, j := slices.Index(fake.calls, "systemctl --user reset-failed "+m.unitName()), slices.Index(fake.calls, "systemctl --user restart "+m.unitName()); i < 0 || i > j {
		t.Errorf("the start counter is not reset before a restart: %q", fake.calls)
	}
	if got, _ := os.ReadFile(m.serviceExe()); string(got) != "dotlane build 2" {
		t.Errorf("service runs %q", got)
	}
}

func TestLinuxRefresh(t *testing.T) {
	m, fake, out := newTestLinux(t)
	if err := m.Refresh(context.Background()); err != nil || !strings.Contains(out.String(), "nothing to refresh") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if fake.ran("systemctl") || fake.sudoCalls > 0 {
		t.Errorf("refresh with nothing installed ran %v", fake.calls)
	}
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := fake.sudoCalls
	m.Daemon = func(context.Context) (api.Status, error) {
		return api.Status{PID: linuxDaemonPID, Started: time.Now().Add(-time.Hour)}, nil
	}
	os.WriteFile(m.Exe, []byte("dotlane build 2"), 0o755)
	out.Reset()
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fake.sudoCalls != before || !strings.Contains(out.String(), "restarted") {
		t.Errorf("sudo calls %d→%d\n%s", before, fake.sudoCalls, out)
	}
}

func TestLinuxUpgrade(t *testing.T) {
	m, fake, _ := newTestLinux(t)
	var buf bytes.Buffer
	if did, err := m.Upgrade(context.Background(), "0.2.0", &buf); did || err != nil {
		t.Fatalf("upgraded when not installed: %v %v", did, err)
	}
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Daemon = func(context.Context) (api.Status, error) {
		return api.Status{PID: linuxDaemonPID, Version: "0.1.0", Started: time.Now().Add(-time.Hour)}, nil
	}
	fake.calls = nil
	did, err := m.Upgrade(context.Background(), "0.2.0", &buf)
	if err != nil || !did || !fake.ran("systemctl --user restart") {
		t.Fatalf("did=%v err=%v calls=%v", did, err, fake.calls)
	}
	if did, _ := m.Upgrade(context.Background(), "0.1.0", &buf); did {
		t.Error("upgraded to the version it runs")
	}
}

func TestLinuxUninstall(t *testing.T) {
	m, fake, out := newTestLinux(t)
	m.Cfg.HTTPPort = 80
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := m.Uninstall(context.Background(), true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, p := range []string{m.unitPath(), m.serviceExe(), m.dnsUnitPath(), m.SysctlFile, m.anchorPath(m.Stores[0]), m.Cfg.StateDir} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left behind", p)
		}
	}
	if start, _ := m.portStart(); start != 1024 {
		t.Errorf("port start = %d", start)
	}
	if len(fake.nss) != 0 || m.caTrusted() || fake.userEnabled || fake.dnsOn {
		t.Errorf("nss=%v trusted=%v enabled=%v dns=%v", fake.nss, m.caTrusted(), fake.userEnabled, fake.dnsOn)
	}
}

func TestLinuxUninstallLeavesForeignFiles(t *testing.T) {
	m, fake, out := newTestLinux(t)
	os.WriteFile(m.SysctlFile, []byte("net.ipv4.ip_unprivileged_port_start=0\n"), 0o644)
	os.WriteFile(m.dnsUnitPath(), []byte("[Unit]\nDescription=mine\n"), 0o644)
	if err := m.Uninstall(context.Background(), false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, p := range []string{m.SysctlFile, m.dnsUnitPath()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", p, err)
		}
	}
	if fake.sudoCalls > 0 {
		t.Errorf("sudo ran: %v", fake.calls)
	}
}

func linuxStatuses(checks []Check) map[string]CheckStatus {
	got := map[string]CheckStatus{}
	for _, c := range checks {
		got[c.Name] = c.Status
	}
	return got
}

func TestLinuxDoctor(t *testing.T) {
	m, fake, out := newTestLinux(t)
	before := linuxStatuses(m.Doctor(context.Background()))
	for name, want := range map[string]CheckStatus{"Login service": Failed, "Daemon": Skipped, "System DNS": Pass, "DNS for .dldev": Failed, "Certificate authority": Skipped} {
		if before[name] != want {
			t.Errorf("before install: %s = %s, want %s", name, before[name], want)
		}
	}
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range m.Doctor(context.Background()) {
		if c.Status != Pass {
			t.Errorf("after install: %s = %s (%s)", c.Name, c.Status, c.Detail)
		}
	}
	fake.noResolved = true
	c := linuxStatuses(m.Doctor(context.Background()))
	if c["System DNS"] != Failed {
		t.Errorf("System DNS with resolved stopped = %s", c["System DNS"])
	}
	fake.noResolved = false
	fake.dnsOn = false
	if c := linuxStatuses(m.Doctor(context.Background())); c["DNS for .dldev"] != Failed {
		t.Errorf("DNS with the link gone = %s", c["DNS for .dldev"])
	}
}

func TestLinuxDoctorLowPorts(t *testing.T) {
	m, _, _ := newTestLinux(t)
	m.Cfg.HTTPSPort = 443
	if c := m.portsCheck(); c.Status != Failed || !c.Repairable {
		t.Errorf("check = %+v", c)
	}
	m.Bindable = func(string, int) error { return fmt.Errorf("listen: %w", errAccess) }
	if c := m.portCheck(context.Background(), "HTTPS port", "tcp", 443, 0); c.Conflict || c.Status != Warning {
		t.Errorf("EACCES before install = %+v", c)
	}
}

func TestLinuxPrivateFiles(t *testing.T) {
	m, _, _ := newTestLinux(t)
	os.MkdirAll(m.Cfg.StateDir, 0o755)
	os.Chmod(m.Cfg.StateDir, 0o755)
	c := m.permissionsCheck()
	if c.Status != Failed || !c.Repairable {
		t.Fatalf("check = %+v", c)
	}
	if err := m.tightenPermissions(); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(m.Cfg.StateDir); info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}
}

func TestLinuxMigrate(t *testing.T) {
	m, _, _ := newTestLinux(t)
	if err := m.Migrate(context.Background()); err == nil || !strings.Contains(err.Error(), "nothing to migrate") {
		t.Errorf("err = %v", err)
	}
}

func TestLinuxNoUserManager(t *testing.T) {
	m, fake, _ := newTestLinux(t)
	fake.noManager = true
	err := m.installService(context.Background())
	if err == nil || !strings.Contains(err.Error(), "systemd user manager is not running") || !strings.Contains(err.Error(), "enable-linger") {
		t.Errorf("err = %v", err)
	}
}

func TestUnitQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/a/b":         "/a/b",
		"/my dir/x":    `"/my dir/x"`,
		"/100%/x":      "/100%%/x",
		`/a"b`:         `"/a\"b"`,
		"/home/$USER/": "/home/$$USER/",
	} {
		if got := unitQuote(in); got != want {
			t.Errorf("unitQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// toolDir is a folder with the tools the test names, as files with mode.
func toolDir(t *testing.T, mode os.FileMode, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		path := filepath.Join(dir, n)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		os.Chmod(path, mode)
	}
	return dir
}

func TestLookToolIn(t *testing.T) {
	me := uint32(os.Getuid())
	safe := toolDir(t, 0o755, "tee")
	if got, err := lookToolIn([]string{safe}, me, "tee"); err != nil || got != filepath.Join(safe, "tee") {
		t.Errorf("safe tool = %q, %v", got, err)
	}
	if _, err := lookToolIn([]string{safe}, me, "nosuch"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing tool: %v", err)
	}
	for _, name := range []string{"", "../tee", "a/b"} {
		if _, err := lookToolIn([]string{safe}, me, name); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
	// Owned by someone else than root (here: not by the owner the lookup wants).
	if _, err := lookToolIn([]string{safe}, me+1, "tee"); err == nil || !strings.Contains(err.Error(), "not owned by root") {
		t.Errorf("foreign owner: %v", err)
	}
	for _, mode := range []os.FileMode{0o775, 0o757, 0o777} {
		dir := toolDir(t, mode, "tee")
		if _, err := lookToolIn([]string{dir}, me, "tee"); err == nil || !strings.Contains(err.Error(), "written by users other than root") {
			t.Errorf("mode %o: %v", mode, err)
		}
	}
	// A link out of the system folders is refused; one inside them is fine.
	outside := toolDir(t, 0o755, "evil")
	links := t.TempDir()
	os.Symlink(filepath.Join(outside, "evil"), filepath.Join(links, "tee"))
	os.Symlink(filepath.Join(safe, "tee"), filepath.Join(links, "ok"))
	if _, err := lookToolIn([]string{links, safe}, me, "tee"); err != nil {
		t.Errorf("the next folder has a good tee, and the bad link should not hide it: %v", err)
	}
	if _, err := lookToolIn([]string{links}, me, "tee"); err == nil || !strings.Contains(err.Error(), "outside the system folders") {
		t.Errorf("link outside: %v", err)
	}
	if _, err := lookToolIn([]string{links, safe}, me, "ok"); err != nil {
		t.Errorf("a link into an allowed folder was refused: %v", err)
	}
	if _, err := lookToolIn([]string{links}, me, "ok"); err == nil {
		t.Error("a link into a folder that is not allowed was accepted")
	}
	// A directory or a non-executable file is not a tool.
	os.Mkdir(filepath.Join(safe, "adir"), 0o755)
	if _, err := lookToolIn([]string{safe}, me, "adir"); err == nil {
		t.Error("directory accepted")
	}
}

func TestLookSystemToolIgnoresPath(t *testing.T) {
	evil := toolDir(t, 0o755, "tee", "ip")
	t.Setenv("PATH", evil+":"+os.Getenv("PATH"))
	if got, err := lookSystemTool("tee"); err == nil && strings.HasPrefix(got, evil) {
		t.Errorf("tee came from PATH: %s", got)
	}
	if got, err := lookSystemTool("ip"); err == nil && !strings.HasPrefix(got, "/usr/") && !strings.HasPrefix(got, "/sbin/") && !strings.HasPrefix(got, "/bin/") {
		t.Errorf("ip = %s", got)
	}
}

func TestLinuxRootToolsComeFromTheSystem(t *testing.T) {
	m, fake, out := newTestLinux(t)
	m.Cfg.HTTPPort = 80
	// PATH has a poisoned tee, ip and resolvectl first; only the unprivileged lookup sees them.
	m.LookPath = func(n string) (string, error) { return "/home/u/bin/" + n, nil }
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	unit, _ := os.ReadFile(m.dnsUnitPath())
	if strings.Contains(string(unit), "/home/u/bin") || !strings.Contains(string(unit), "ExecStart=/usr/bin/resolvectl dns") {
		t.Errorf("DNS unit:\n%s", unit)
	}
	for _, c := range fake.calls {
		if strings.HasPrefix(c, "sudo ") && strings.Contains(c, "/home/u/bin") {
			t.Errorf("sudo ran %s", c)
		}
	}
	if !strings.Contains(out.String(), "$ sudo /usr/bin/tee") || strings.Contains(out.String(), "/home/u/bin") {
		t.Errorf("output:\n%s", out)
	}
}

func TestLinuxInstallRefusesUntrustedRootTool(t *testing.T) {
	for _, tool := range []string{"ip", "resolvectl", "tee", "sysctl"} {
		m, fake, out := newTestLinux(t)
		m.Cfg.HTTPPort = 80
		m.LookSystem = func(n string) (string, error) {
			if n == tool {
				return "", errors.New("refusing to run " + n + " as root: /x/" + n + " can be written by users other than root")
			}
			return "/usr/bin/" + n, nil
		}
		err := m.Install(context.Background())
		if err == nil || !strings.Contains(err.Error(), "refusing to run "+tool) {
			t.Errorf("%s: err = %v\n%s", tool, err, out)
		}
		if tool == "ip" || tool == "resolvectl" {
			if _, statErr := os.Stat(m.dnsUnitPath()); statErr == nil {
				t.Errorf("%s: a DNS unit was written", tool)
			}
			if fake.dnsOn {
				t.Errorf("%s: DNS was routed", tool)
			}
		}
	}
}

func TestLinuxSysctlFileIsPerService(t *testing.T) {
	cfg := &config.Config{ServiceName: "dev.dotlane.x"}
	t.Setenv("HOME", t.TempDir())
	m, err := NewLinux(cfg, "config.toml", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.SysctlFile != "/etc/sysctl.d/50-dotlane-dev.dotlane.x.conf" {
		t.Errorf("SysctlFile = %s", m.SysctlFile)
	}
}

func TestLinuxUninstallKeepsPortsForAnotherInstall(t *testing.T) {
	m, fake, out := newTestLinux(t)
	m.Cfg.HTTPPort = 80
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(m.SysctlFile), "50-dotlane-dev.dotlane.real.conf")
	os.WriteFile(other, m.sysctlFile(), 0o644)
	fake.calls = nil
	out.Reset()
	if err := m.Uninstall(context.Background(), false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(m.SysctlFile); !errors.Is(err, os.ErrNotExist) {
		t.Error("own sysctl file left behind")
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("the other install's file was removed: %v", err)
	}
	if fake.ran("sudo sysctl") {
		t.Errorf("the sysctl was reset: %v", fake.calls)
	}
	if start, _ := m.portStart(); start != 80 {
		t.Errorf("port start = %d, want 80", start)
	}
}

func TestLinuxUninstallLeavesForeignUserUnit(t *testing.T) {
	m, fake, out := newTestLinux(t)
	os.MkdirAll(m.UnitDir, 0o755)
	os.WriteFile(m.unitPath(), []byte("[Unit]\nDescription=mine\n"), 0o644)
	os.MkdirAll(m.ServiceDir, 0o755)
	os.WriteFile(m.serviceExe(), []byte("mine"), 0o755)
	if err := m.Uninstall(context.Background(), false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, p := range []string{m.unitPath(), m.serviceExe()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", p, err)
		}
	}
	if fake.ran("systemctl --user disable") {
		t.Errorf("stopped a unit that is not Dotlane's: %v", fake.calls)
	}
	if !strings.Contains(out.String(), "is not Dotlane's") {
		t.Errorf("output:\n%s", out)
	}
}

func TestLinuxInstallSetsDefaultRouteAgain(t *testing.T) {
	m, fake, out := newTestLinux(t)
	fake.leak = true // resolved made the link a default route after the unit's command
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !fake.ran("sudo resolvectl default-route " + m.dnsLink() + " no") {
		t.Errorf("default-route was not set again: %v", fake.calls)
	}
}

// Routes follow the config: the DNS unit is rewritten with the new endings, and left alone when
// nothing changed.
func TestLinuxSyncRoutes(t *testing.T) {
	m, fake, out := newTestLinux(t)
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Cfg.TLDs = []string{"dldev", "extra"}
	if err := m.SyncRoutes(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, err := m.Routed(context.Background()); err != nil || strings.Join(got, " ") != "dldev extra" {
		t.Errorf("Routed after adding = %q, %v", got, err)
	}
	if !fake.ran("sudo systemctl restart " + m.dnsUnitName()) {
		t.Errorf("DNS unit not restarted: %q", fake.calls)
	}

	m.Cfg.TLDs = []string{"extra"}
	if err := m.SyncRoutes(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, err := m.Routed(context.Background()); err != nil || strings.Join(got, " ") != "extra" {
		t.Errorf("Routed after removing = %q, %v", got, err)
	}

	before := fake.sudoCalls
	if err := m.SyncRoutes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.sudoCalls != before {
		t.Errorf("nothing to change, yet sudo ran %d times", fake.sudoCalls-before)
	}

	// Another primary is the same endings in another order: nothing to change either.
	m.Cfg.TLDs = []string{"dldev", "extra"}
	if err := m.SyncRoutes(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Cfg.TLDs = []string{"extra", "dldev"}
	before = fake.sudoCalls
	if err := m.SyncRoutes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.sudoCalls != before {
		t.Errorf("reordering the endings ran sudo %d times", fake.sudoCalls-before)
	}
	unit, _ := os.ReadFile(m.dnsUnitPath())
	if !bytes.Contains(unit, []byte("\nStartLimitIntervalSec=0\n")) {
		t.Errorf("the DNS unit can hit systemd's start limit:\n%s", unit)
	}
}

// Routed reports nothing for a DNS unit that is not Dotlane's, or none at all.
func TestLinuxRoutedIgnoresForeignUnit(t *testing.T) {
	m, _, _ := newTestLinux(t)
	if got, err := m.Routed(context.Background()); err != nil || len(got) != 0 {
		t.Errorf("no unit: %q, %v", got, err)
	}
	os.WriteFile(m.dnsUnitPath(), []byte("[Service]\nExecStart=/usr/bin/resolvectl domain "+m.dnsLink()+" ~corp\n"), 0o644)
	if got, err := m.Routed(context.Background()); err != nil || len(got) != 0 {
		t.Errorf("foreign unit: %q, %v", got, err)
	}
}
