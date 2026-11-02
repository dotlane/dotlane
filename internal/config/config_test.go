package config

import (
	"os"
	"path/filepath"
	"runtime"
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
	if !slices.Equal(cfg.TLDs, []string{"dotlane"}) || cfg.TLDsSet || cfg.DNSPort != DefaultDNSPort || cfg.HTTPPort != 80 || cfg.HTTPSPort != 443 ||
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
	if !slices.Equal(cfg.TLDs, []string{"test"}) || !cfg.TLDsSet || cfg.DNSPort != 10055 {
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
		{`tlds = ["abc", "abc"]`, "twice"},
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

// Endings dotlane tld add would refuse or warn about still load from a file, with a warning,
// so an ending chosen before those rules keeps working.
func TestWarnings(t *testing.T) {
	cfg, err := Load(write(t, `tlds = ["traix", "dev", "lan"]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings) != 2 || !strings.Contains(cfg.Warnings[0], "public top-level domain") ||
		!strings.Contains(cfg.Warnings[1], "routers") {
		t.Errorf("Warnings = %q", cfg.Warnings)
	}
	cfg, err = Load(write(t, `tld = "dotlane"`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("dotlane: Warnings = %q", cfg.Warnings)
	}
}

func TestResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DOTLANE_CONFIG", "")
	def, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
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

func TestDefaultPathsPerOS(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	def, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	state, err := defaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "linux":
		if want := filepath.Join(home, ".config", "dotlane", "config.toml"); def != want {
			t.Errorf("DefaultPath = %q, want %q", def, want)
		}
		if want := filepath.Join(home, ".local", "state", "dotlane"); state != want {
			t.Errorf("state dir = %q, want %q", state, want)
		}
		xdg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		t.Setenv("XDG_STATE_HOME", xdg)
		if def, _ := DefaultPath(); def != filepath.Join(xdg, "dotlane", "config.toml") {
			t.Errorf("DefaultPath with XDG_CONFIG_HOME = %q", def)
		}
		if state, _ := defaultStateDir(); state != filepath.Join(xdg, "dotlane") {
			t.Errorf("state dir with XDG_STATE_HOME = %q", state)
		}
	case "darwin":
		if want := filepath.Join(home, "Library", "Application Support", "dotlane"); state != want {
			t.Errorf("state dir = %q, want %q", state, want)
		}
	}
	if filepath.Dir(def) == state {
		t.Errorf("config and state share %q", state)
	}
}

func TestSetTLDs(t *testing.T) {
	for _, tc := range []struct{ name, have, want string }{
		{"missing file", "", "tlds = [\"dotlane\", \"test\"]\n"},
		{"tld, with its comment",
			"# mine\ntld = \"traix\"   # the old default\ndns_port = 10055\n",
			"# mine\ntlds = [\"dotlane\", \"test\"]   # the old default\ndns_port = 10055\n"},
		{"tlds on several lines",
			"dns_port = 10055\ntlds = [\n  \"traix\", # first\n  \"test\",\n]\nhttp_port = 9480\n",
			"dns_port = 10055\ntlds = [\"dotlane\", \"test\"]\nhttp_port = 9480\n"},
		{"both, the second dropped",
			"tld = \"traix\"\nhttp_port = 9480\ntlds = [\"x\"]\n",
			"tlds = [\"dotlane\", \"test\"]\nhttp_port = 9480\n"},
		{"none yet, no final newline",
			"# mine\ndns_port = 10055",
			"# mine\ndns_port = 10055\ntlds = [\"dotlane\", \"test\"]\n"},
		{"lookalikes left alone",
			"# tld = \"x\"\n  tlds = [\"traix\"]\n",
			"# tld = \"x\"\n  tlds = [\"dotlane\", \"test\"]\n"},
	} {
		p := filepath.Join(t.TempDir(), "config.toml")
		if tc.have != "" {
			os.WriteFile(p, []byte(tc.have), 0o600)
		}
		if err := SetTLDs(p, []string{"dotlane", "test"}); err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		got, _ := os.ReadFile(p)
		if string(got) != tc.want {
			t.Errorf("%s:\ngot  %q\nwant %q", tc.name, got, tc.want)
		}
		cfg, err := Load(p)
		if err != nil || !slices.Equal(cfg.TLDs, []string{"dotlane", "test"}) || cfg.Primary() != "dotlane" {
			t.Errorf("%s: reloaded %+v, %v", tc.name, cfg, err)
		}
		if info, err := os.Stat(p); tc.have != "" && runtime.GOOS != "windows" && (err != nil || info.Mode().Perm() != 0o600) {
			t.Errorf("%s: mode %v, %v; want it kept", tc.name, info.Mode(), err)
		}
	}

	for _, bad := range [][]string{nil, {"Bad"}, {"test", "test"}} {
		if err := SetTLDs(filepath.Join(t.TempDir(), "config.toml"), bad); err == nil {
			t.Errorf("SetTLDs(%q): no error", bad)
		}
	}
}

// A config file kept in a dotfiles repository and symlinked stays a symlink.
func TestSetTLDsThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need developer mode on Windows")
	}
	target := write(t, "tld = \"traix\"\n")
	link := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := SetTLDs(link, []string{"dotlane"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link replaced: %v %v", info.Mode(), err)
	}
	if got, _ := os.ReadFile(target); string(got) != "tlds = [\"dotlane\"]\n" {
		t.Errorf("target = %q", got)
	}
}

func TestPinTLDs(t *testing.T) {
	p := write(t, "dns_port = 10055\n")
	if err := PinTLDs(p, []string{"traix"}, "kept: the old default"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "dns_port = 10055\n\n# kept: the old default\ntlds = [\"traix\"]\n" {
		t.Errorf("file = %q", got)
	}
	cfg, err := Load(p)
	if err != nil || !slices.Equal(cfg.TLDs, []string{"traix"}) || !cfg.TLDsSet {
		t.Errorf("reloaded %+v, %v", cfg, err)
	}
	if err := PinTLDs(p, []string{"Bad"}, "why"); err == nil {
		t.Error("pinned an invalid ending")
	}
}
