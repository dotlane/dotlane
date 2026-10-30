//go:build darwin

package install

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrodee/dotlane/internal/api"
	"github.com/pedrodee/dotlane/internal/proxy"
	"github.com/pedrodee/dotlane/internal/sites"
)

const (
	caddyPID   = 2779
	dnsmasqPID = 2588
)

// newPrototypeMac is this Mac before migrating: the prototype serves myapp.traix with
// Homebrew's Caddy (on 443 and 80) and the dnsmasq agent (on 10053), its CA is trusted, the
// resolver file points at 10053, and ~/.local/bin/dotlane is the Bash script.
func newPrototypeMac(t *testing.T) (*Mac, *fakeMac, *bytes.Buffer, *[]string) {
	t.Helper()
	m, fake, out := newTestMac(t)
	m.Cfg.TLDs, m.Cfg.HTTPSPort, m.Cfg.HTTPPort, m.Cfg.DNSPort = []string{"traix"}, 443, 80, 10053
	fake.daemonPorts = daemonPorts(m.Cfg)
	os.RemoveAll(proxy.CADir(m.Cfg)) // the daemon has never run

	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(m.sitesDir(), "myapp.caddy"),
		"# Managed by dotlane: https://myapp.traix -> localhost:5173\nmyapp.traix {\n\treverse_proxy localhost:5173\n}\n")
	write(filepath.Join(m.sitesDir(), "notes.caddy"), "notes.traix {\n\trespond \"hi\"\n}\n")
	m.BrewPrefix = filepath.Join(t.TempDir(), "homebrew")
	write(filepath.Join(m.prototypeCADir(), "root.crt"), "the prototype's root certificate")
	write(filepath.Join(m.prototypeCADir(), "root.key"), "its key")
	write(filepath.Join(m.ResolverDir, "traix"), "nameserver 127.0.0.1\nport 10053\n")
	write(filepath.Join(m.Home, ".local", "bin", "dotlane"), "#!/usr/bin/env bash\n"+prototypeScript+"\n")
	write(filepath.Join(m.Home, ".config", "dnsmasq", "traix.conf"), "port=10053\n")
	fake.trusted, fake.inKeychain = true, true

	fake.others["sh.brew.caddy"], fake.others[prototypeDNSLabel] = true, true
	fake.labels[caddyPID], fake.labels[dnsmasqPID] = "sh.brew.caddy", prototypeDNSLabel
	fake.paths[caddyPID], fake.paths[dnsmasqPID] = "/opt/homebrew/opt/caddy/bin/caddy", "/opt/homebrew/sbin/dnsmasq"
	fake.ports["sh.brew.caddy"] = []string{"tcp:443", "tcp:80", "udp:443"}
	fake.ports[prototypeDNSLabel] = []string{"udp:10053"}

	m.Sites = registryClient{filepath.Join(m.Cfg.StateDir, "sites.json")}

	verified := &[]string{}
	m.VerifyTLS = func(_ context.Context, host string, port int) error {
		*verified = append(*verified, host)
		return nil
	}
	return m, fake, out, verified
}

// registryClient stands in for the daemon's API: it edits the registry file the daemon loads.
type registryClient struct{ path string }

func (c registryClient) Sites(context.Context) ([]api.Site, error) {
	r, err := sites.Open(c.path)
	if err != nil {
		return nil, err
	}
	var out []api.Site
	for _, s := range r.List() {
		out = append(out, api.Site{Name: s.Name, Port: s.Port, Project: s.Project})
	}
	return out, nil
}

func (c registryClient) AddSite(_ context.Context, name string, port int, project string) (api.Site, error) {
	r, err := sites.Open(c.path)
	if err != nil {
		return api.Site{}, err
	}
	return api.Site{Name: name, Port: port}, r.Put(sites.Site{Name: name, Port: port, Project: project})
}

func TestMigrate(t *testing.T) {
	m, fake, out, verified := newPrototypeMac(t)
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if fake.sudoCalls != 0 {
		t.Errorf("asked for a password %d times: %v", fake.sudoCalls, fake.calls)
	}
	ours, _ := os.ReadFile(m.rootCert())
	if string(ours) != "the prototype's root certificate" {
		t.Errorf("root.crt = %q, want the prototype's", ours)
	}
	if info, err := os.Stat(filepath.Join(proxy.CADir(m.Cfg), "root.key")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("root.key: %v %v", info, err)
	}
	r, err := sites.Open(filepath.Join(m.Cfg.StateDir, "sites.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.List(); len(got) != 1 || got[0] != (sites.Site{Name: "myapp", Port: 5173}) {
		t.Errorf("sites = %+v (the unmanaged notes.caddy must be left out)", got)
	}
	if fake.others["sh.brew.caddy"] || fake.others[prototypeDNSLabel] || !fake.disabled[prototypeDNSLabel] {
		t.Errorf("prototype still running or dnsmasq not disabled: %v %v", fake.others, fake.disabled)
	}
	if !fake.loaded {
		t.Error("Dotlane's service not loaded")
	}
	if len(*verified) != 1 || (*verified)[0] != "myapp.traix" {
		t.Errorf("verified = %v", *verified)
	}
	if target, err := os.Readlink(filepath.Join(m.Home, ".local", "bin", "dotlane")); err != nil || target != m.Exe {
		t.Errorf("~/.local/bin/dotlane → %q, %v; want the binary", target, err)
	}
	// Nothing of the prototype is deleted.
	for _, p := range []string{filepath.Join(m.sitesDir(), "myapp.caddy"), filepath.Join(m.prototypeCADir(), "root.key")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	text := out.String()
	for _, want := range []string{
		"notes.caddy", "not written by dotlane add", "already trusted", "imported · → localhost:5173",
		"$ brew services stop caddy", "kept from starting at login", "already routed",
		"https://myapp.traix → localhost:5173", "(was the prototype script)", "is not on your PATH",
		"/.config/caddy/sites\n", "brew uninstall caddy dnsmasq", "Moved to Dotlane.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	// Again: everything is already done.
	out.Reset()
	fake.calls = nil
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if fake.sudoCalls != 0 || fake.ran("brew") {
		t.Errorf("second run changed things: %v", fake.calls)
	}
	for _, want := range []string{"already the prototype's", "already in Dotlane"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("second run lacks %q:\n%s", want, out)
		}
	}
}

func TestMigratePutsThePrototypeBack(t *testing.T) {
	m, fake, out, _ := newPrototypeMac(t)
	// Something else also listens on 443, so the install refuses.
	fake.listen["tcp:443"] = []int{77}
	fake.paths[77] = "/usr/local/bin/myserver"

	err := m.Migrate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the prototype is running again") ||
		!strings.Contains(err.Error(), "run dotlane migrate again") || strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !fake.others["sh.brew.caddy"] || !fake.others[prototypeDNSLabel] || fake.disabled[prototypeDNSLabel] {
		t.Errorf("prototype not restored: %v %v", fake.others, fake.disabled)
	}
	if fake.loaded {
		t.Error("Dotlane's service left running next to the prototype")
	}
	if fake.sudoCalls != 0 || !fake.trusted {
		t.Errorf("the prototype's CA trust was touched: %v", fake.calls)
	}
	if !strings.Contains(out.String(), "myserver (pid 77)") {
		t.Errorf("output does not name what is in the way:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(m.Home, ".local", "bin", "dotlane")); err != nil {
		t.Fatal(err)
	} else if target, _ := os.Readlink(filepath.Join(m.Home, ".local", "bin", "dotlane")); target != "" {
		t.Error("the prototype script was replaced although migrate failed")
	}
}

func TestMigrateOtherEnding(t *testing.T) {
	m, fake, out, _ := newPrototypeMac(t)
	os.WriteFile(filepath.Join(m.sitesDir(), "shop.caddy"), []byte("# Managed by dotlane: https://shop.test -> localhost:3000\n"), 0o644)
	err := m.Migrate(context.Background())
	if err == nil || !strings.Contains(err.Error(), `tlds = ["test"]`) {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if fake.ran("brew") || fake.ran("launchctl bootout") || fake.ran("launchctl bootstrap") {
		t.Errorf("changed things before refusing: %v", fake.calls)
	}
	if _, err := os.Stat(m.rootCert()); err == nil {
		t.Error("CA copied before refusing")
	}
}

func TestMigrateWithoutPrototype(t *testing.T) {
	m, _, _ := newTestMac(t)
	m.BrewPrefix = t.TempDir()
	if err := m.Migrate(context.Background()); err == nil || !strings.Contains(err.Error(), "no Dotlane prototype") {
		t.Errorf("err = %v", err)
	}
}
