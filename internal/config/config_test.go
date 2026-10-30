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

func TestDefaultPath(t *testing.T) {
	t.Setenv("DOTLANE_CONFIG", "/x/y.toml")
	if p, _ := DefaultPath(); p != "/x/y.toml" {
		t.Errorf("DefaultPath = %q", p)
	}
	t.Setenv("DOTLANE_CONFIG", "")
	if p, _ := DefaultPath(); !strings.HasSuffix(p, filepath.Join(".config", "dotlane", "config.toml")) {
		t.Errorf("DefaultPath = %q", p)
	}
}
