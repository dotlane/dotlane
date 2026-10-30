package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.TLDs, []string{"traix"}) || cfg.DNSPort != 10053 || cfg.HTTPPort != 80 || cfg.HTTPSPort != 443 ||
		cfg.ServiceName != "dev.dotlane" {
		t.Errorf("defaults = %+v", cfg)
	}
	if cfg.Path != "" || !strings.HasSuffix(cfg.StateDir, "dotlane") {
		t.Errorf("Path = %q, StateDir = %q", cfg.Path, cfg.StateDir)
	}
}

// The file the Bash prototype reads must keep working.
func TestPrototypeFile(t *testing.T) {
	cfg, err := Load(write(t, "# mine\ntld = \"test\"   # comment\ndns_port = 10055\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.TLDs, []string{"test"}) || cfg.DNSPort != 10055 {
		t.Errorf("got %+v", cfg)
	}
}

func TestAllSettings(t *testing.T) {
	p := write(t, `tlds = ["traix", "test"]
http_port = 9480
https_port = 9443
state_dir = "state"
service_name = "dev.dotlane.dev"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.TLDs, []string{"traix", "test"}) || cfg.HTTPPort != 9480 || cfg.HTTPSPort != 9443 ||
		cfg.ServiceName != "dev.dotlane.dev" {
		t.Errorf("got %+v", cfg)
	}
	if want := filepath.Join(filepath.Dir(p), "state"); cfg.StateDir != want {
		t.Errorf("relative state_dir = %q, want %q (next to the file)", cfg.StateDir, want)
	}
	if cfg.SocketPath() != filepath.Join(cfg.StateDir, "dotlane.sock") {
		t.Errorf("SocketPath = %q", cfg.SocketPath())
	}
}

func TestRejected(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`tld = "Bad.TLD"`, "lowercase"},
		{`tld = "local"`, "Bonjour"},
		{`tlds = ["a1", "a1"]`, "twice"},
		{"tld = \"traix\"\ntlds = [\"test\"]", "not both"},
		{`dns_port = 70000`, "dns_port"},
		{"http_port = 9443\nhttps_port = 9443", "differ"},
		{`tdl = "traix"`, "unknown setting"},
		{`service_name = "My Service"`, "reverse-DNS"},
		{`tld = traix`, "config.toml"}, // not TOML: values need quotes
	} {
		_, err := Load(write(t, tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: error %v, want it to mention %q", tc.body, err, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOTLANE_CONFIG", "")
	def := filepath.Join(home, ".config", "dotlane", "config.toml")
	other := filepath.Join(t.TempDir(), "other.toml")
	os.WriteFile(other, []byte("tld = \"test\"\n"), 0o644)

	for _, tc := range []struct {
		flag, env, want, err string
	}{
		{want: def},            // nothing named: the default, missing is fine
		{flag: def, want: def}, // the login service names the default: still optional
		{env: def, want: def},
		{flag: other, want: other}, // another file that exists
		{env: other, want: other},
		{flag: other, env: "/nope", want: other},                // the flag wins
		{flag: "/nope/x.toml", err: "config file /nope/x.toml"}, // a typo is an error
		{env: "/nope/x.toml", err: "config file /nope/x.toml"},
	} {
		t.Setenv("DOTLANE_CONFIG", tc.env)
		got, err := Resolve(tc.flag)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%+v: err = %v, want %q", tc, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%+v: got %q, %v; want %q", tc, got, err, tc.want)
		}
	}
}

func TestSetDNSPort(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.toml")
	cfg, err := Load(p)
	if err != nil || cfg.DNSPortSet {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	if err := SetDNSPort(p, 10054, "chosen by dotlane install: 10053 was taken"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(p)
	if err != nil || cfg.DNSPort != 10054 || !cfg.DNSPortSet {
		t.Errorf("after SetDNSPort: %+v %v", cfg, err)
	}

	// Appends to an existing file without disturbing it.
	q := write(t, "tld = \"test\"\n")
	if err := SetDNSPort(q, 10060, "why"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(q)
	if err != nil || cfg.DNSPort != 10060 || cfg.TLDs[0] != "test" {
		t.Errorf("append: %+v %v", cfg, err)
	}
}
