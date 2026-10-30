//go:build darwin

package install

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedrodee/dotlane/internal/config"
	"github.com/pedrodee/dotlane/internal/proxy"
)

// fakeMac simulates launchd, the keychain and sudo, so tests never touch the real system.
type fakeMac struct {
	loaded, trusted, inKeychain bool
	hash                        string
	calls                       []string
	sudoCalls                   int
}

func (f *fakeMac) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, filepath.Base(name)+" "+strings.Join(args, " "))
	switch filepath.Base(name) + " " + args[0] {
	case "launchctl print":
		if !f.loaded {
			return nil, errors.New("not loaded")
		}
	case "launchctl bootstrap":
		f.loaded = true
	case "launchctl bootout":
		f.loaded = false
	case "security verify-cert":
		if !f.trusted {
			return nil, errors.New("CSSMERR_TP_NOT_TRUSTED")
		}
	case "security find-certificate":
		if f.inKeychain {
			return []byte("SHA-1 hash: " + f.hash + "\n"), nil
		}
	}
	return nil, nil
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

func newTestMac(t *testing.T) (*Mac, *fakeMac, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		TLDs: []string{"dldev"}, DNSPort: 10054, HTTPPort: 9480, HTTPSPort: 9443,
		StateDir: filepath.Join(dir, "state"), ServiceName: "dev.dotlane.test",
	}
	// A root certificate as the daemon would have created it.
	if err := os.MkdirAll(proxy.CADir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	der := []byte("not really DER, but hashed the same way")
	os.WriteFile(filepath.Join(proxy.CADir(cfg), "root.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	sum := sha1.Sum(der)

	fake := &fakeMac{hash: strings.ToUpper(hex.EncodeToString(sum[:]))}
	out := &bytes.Buffer{}
	m := &Mac{
		Cfg: cfg, ConfigPath: "/cfg/config.toml", Exe: "/opt/dotlane/bin/dotlane",
		Home: filepath.Join(dir, "home"), UID: 501,
		ResolverDir: filepath.Join(dir, "resolver"),
		Out:         out, Runner: fake,
		Ready:  func(context.Context) error { return nil },
		Lookup: func(context.Context, string) ([]string, error) { return []string{"127.0.0.1", "::1"}, nil },
	}
	return m, fake, out
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
		"<string>/cfg/config.toml</string>", "<string>serve</string>", "<key>KeepAlive</key><true/>",
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
	for _, want := range []string{"$ sudo /usr/bin/security add-trusted-cert", "$ sudo /usr/bin/tee", "Send lookups for *.dldev", "resolves to this Mac"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	// Running it again changes nothing and asks for no password.
	before := fake.sudoCalls
	out.Reset()
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.sudoCalls != before {
		t.Errorf("re-install ran sudo %d more times:\n%s", fake.sudoCalls-before, out)
	}
	if n := strings.Count(out.String(), "already"); n != 3 {
		t.Errorf("want 3 'already' lines, got %d:\n%s", n, out)
	}
}

func TestInstallRefusesForeignResolver(t *testing.T) {
	m, fake, _ := newTestMac(t)
	os.MkdirAll(m.ResolverDir, 0o755)
	os.WriteFile(filepath.Join(m.ResolverDir, "dldev"), []byte("nameserver 127.0.0.1\nport 1053\n"), 0o644)

	err := m.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "another tool") || !strings.Contains(err.Error(), "port 1053") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("changed things before refusing: %v", fake.calls)
	}
}

func TestInstallRefusesTemporaryBinary(t *testing.T) {
	m, _, _ := newTestMac(t)
	m.Exe = "/private/var/folders/x/go-build123/b001/exe/dotlane"
	if err := m.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "temporary build") {
		t.Errorf("err = %v", err)
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

	// Uninstalling twice is harmless; purge then deletes the state.
	if err := m.Uninstall(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Cfg.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state kept with --purge")
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
