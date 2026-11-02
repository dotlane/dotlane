package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/install"
)

func TestWithTLD(t *testing.T) {
	for _, tc := range []struct {
		list    []string
		tld     string
		primary bool
		want    []string
	}{
		{[]string{"traix"}, "test", false, []string{"traix", "test"}},
		{[]string{"traix", "test"}, "test", false, []string{"traix", "test"}}, // already there: unchanged
		{[]string{"traix"}, "dotlane", true, []string{"dotlane", "traix"}},
		{[]string{"traix", "test"}, "test", true, []string{"test", "traix"}},
		{[]string{"test"}, "test", true, []string{"test"}},
	} {
		if got := withTLD(tc.list, tc.tld, tc.primary); !slices.Equal(got, tc.want) {
			t.Errorf("withTLD(%q, %q, %v) = %q, want %q", tc.list, tc.tld, tc.primary, got, tc.want)
		}
	}
	list := []string{"a1b", "c2d"}
	withTLD(list, "a1b", true)
	if !slices.Equal(list, []string{"a1b", "c2d"}) {
		t.Errorf("withTLD changed its argument: %q", list)
	}
}

func TestWithoutTLD(t *testing.T) {
	if got, err := withoutTLD([]string{"traix", "test"}, "traix"); err != nil || !slices.Equal(got, []string{"test"}) {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := withoutTLD([]string{"traix"}, "test"); err != nil || !slices.Equal(got, []string{"traix"}) {
		t.Errorf("not listed: got %q, %v", got, err)
	}
	if _, err := withoutTLD([]string{"traix"}, "traix"); err == nil || !strings.Contains(err.Error(), "only domain ending") {
		t.Errorf("last one: %v", err)
	}
}

func TestCheckNew(t *testing.T) {
	if err := checkNew("test", false); err != nil {
		t.Error(err)
	}
	if err := checkNew("dev", true); err == nil || !strings.Contains(err.Error(), "public top-level domain") {
		t.Errorf("dev with --force: %v, want refused anyway", err)
	}
	if err := checkNew("lan", false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("lan: %v, want it to ask for --force", err)
	}
	if err := checkNew("lan", true); err != nil {
		t.Errorf("lan with --force: %v", err)
	}
}

// fakeInstaller records what the tld commands ask of the machine.
type fakeInstaller struct {
	install.Installer // the methods the tld commands never call
	installed         bool
	routed            []string
	calls             []string
	cfg               func() *config.Config
}

func (f *fakeInstaller) Installed(context.Context) bool { return f.installed }
func (f *fakeInstaller) Routed(context.Context) ([]string, error) {
	return f.routed, nil
}
func (f *fakeInstaller) SyncRoutes(context.Context) error {
	f.calls = append(f.calls, "sync")
	f.routed = slices.Clone(f.cfg().TLDs)
	return nil
}
func (f *fakeInstaller) Refresh(context.Context) error {
	f.calls = append(f.calls, "refresh")
	return nil
}

func newTestEnv(t *testing.T, body string, installed bool) (tldEnv, *fakeInstaller, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(body), 0o644)
	load := func() (*config.Config, error) { return config.Load(path) }
	fake := &fakeInstaller{installed: installed}
	fake.cfg = func() *config.Config { cfg, _ := load(); return cfg }
	return tldEnv{
		loadConfig: load,
		configPath: func() string { return path },
		installer:  func() (install.Installer, error) { return fake, nil },
		lookup:     func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil },
	}, fake, path
}

// run runs a tld subcommand against env, returning its output.
func run(t *testing.T, env tldEnv, args ...string) (string, error) {
	t.Helper()
	cmd := newTLDCommand(env)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestTLDAddWhenInstalled(t *testing.T) {
	env, fake, path := newTestEnv(t, "# mine\ntld = \"traix\"\n", true)
	out, err := run(t, env, "add", ".test")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(path); string(got) != "# mine\ntlds = [\"traix\", \"test\"]\n" {
		t.Errorf("config = %q", got)
	}
	if strings.Join(fake.calls, " ") != "sync refresh" {
		t.Errorf("calls = %q, want sync then refresh", fake.calls)
	}
	if !strings.Contains(out, "dotlane-check.test → this machine") {
		t.Errorf("no lookup check:\n%s", out)
	}

	// Again: the file stays as it is and the daemon is not restarted, but routes are synced.
	fake.calls = nil
	if out, err := run(t, env, "add", "test"); err != nil || strings.Join(fake.calls, " ") != "sync" {
		t.Errorf("second add: %v, calls %q\n%s", err, fake.calls, out)
	}
}

func TestTLDAddWhenNotInstalled(t *testing.T) {
	env, fake, path := newTestEnv(t, "", false)
	out, err := run(t, env, "add", "test")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), `tlds = ["traix", "test"]`) {
		t.Errorf("config = %q", got)
	}
	if len(fake.calls) != 0 || !strings.Contains(out, "dotlane install routes") {
		t.Errorf("calls = %q\n%s", fake.calls, out)
	}
}

func TestTLDAddRefused(t *testing.T) {
	env, fake, path := newTestEnv(t, "tld = \"traix\"\n", true)
	for _, args := range [][]string{{"add", "dev"}, {"add", "lan"}, {"use", "com"}, {"add", "dl"}} {
		if _, err := run(t, env, args...); err == nil {
			t.Errorf("%q: accepted", args)
		}
	}
	if got, _ := os.ReadFile(path); string(got) != "tld = \"traix\"\n" || len(fake.calls) != 0 {
		t.Errorf("a refused ending changed something: %q, calls %q", got, fake.calls)
	}
	if _, err := run(t, env, "add", "lan", "--force"); err != nil {
		t.Errorf("lan --force: %v", err)
	}
}

func TestTLDUseAndRm(t *testing.T) {
	env, fake, path := newTestEnv(t, "tlds = [\"traix\", \"test\"]\n", true)
	if out, err := run(t, env, "use", "test"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(path); string(got) != "tlds = [\"test\", \"traix\"]\n" {
		t.Errorf("after use: %q", got)
	}
	if out, err := run(t, env, "rm", "traix"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(path); string(got) != "tlds = [\"test\"]\n" {
		t.Errorf("after rm: %q", got)
	}
	if !slices.Equal(fake.routed, []string{"test"}) {
		t.Errorf("routed = %q", fake.routed)
	}
	if _, err := run(t, env, "rm", "test"); err == nil {
		t.Error("removed the last ending")
	}
}

func TestTLDLsJSON(t *testing.T) {
	env, fake, _ := newTestEnv(t, "tlds = [\"test\", \"lan\"]\n", true)
	fake.routed = []string{"test", "old"}
	out, err := run(t, env, "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"tld": "test",
    "primary": true,
    "configured": true,
    "routed": true`,
		`"tld": "lan",
    "primary": false,
    "configured": true,
    "routed": false,
    "warning": ".lan is often answered`,
		`"tld": "old",
    "primary": false,
    "configured": false,
    "routed": true`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ls --json lacks %s:\n%s", want, out)
		}
	}
}
