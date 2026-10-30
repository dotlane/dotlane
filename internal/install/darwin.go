//go:build darwin

package install

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedrodee/dotlane/internal/config"
	"github.com/pedrodee/dotlane/internal/proxy"
)

const systemKeychain = "/Library/Keychains/System.keychain"

// Mac installs Dotlane on macOS: a launchd user agent, the CA in the System keychain, and one
// /etc/resolver file per domain ending.
type Mac struct {
	Cfg        *config.Config
	ConfigPath string // passed to the service, absolute
	Exe        string // the dotlane binary the service runs, absolute
	Home       string
	UID        int
	// ResolverDir is /etc/resolver; tests point it elsewhere.
	ResolverDir string
	Out         io.Writer
	Runner      Runner
	// Ready waits until the daemon answers on its socket.
	Ready func(ctx context.Context) error
	// Lookup resolves a name the way other programs on the Mac do; used to verify DNS.
	Lookup func(ctx context.Context, host string) ([]string, error)
}

// New returns the installer for this machine.
func New(cfg *config.Config, configPath string, ready func(context.Context) error) (Installer, error) {
	return NewMac(cfg, configPath, ready)
}

// NewMac fills Mac with the real system's values.
func NewMac(cfg *config.Config, configPath string, ready func(context.Context) error) (*Mac, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if configPath, err = filepath.Abs(configPath); err != nil {
		return nil, err
	}
	return &Mac{
		Cfg: cfg, ConfigPath: configPath, Exe: exe, Home: home, UID: os.Getuid(),
		ResolverDir: "/etc/resolver",
		Out:         os.Stdout,
		Runner:      ExecRunner{},
		Ready:       ready,
		Lookup:      net.DefaultResolver.LookupHost,
	}, nil
}

func (m *Mac) plistPath() string {
	return filepath.Join(m.Home, "Library", "LaunchAgents", m.Cfg.ServiceName+".plist")
}

func (m *Mac) logPath() string {
	return filepath.Join(m.Home, "Library", "Logs", "dotlane", m.Cfg.ServiceName+".log")
}

func (m *Mac) target() string { return fmt.Sprintf("gui/%d/%s", m.UID, m.Cfg.ServiceName) }

func (m *Mac) rootCert() string { return filepath.Join(proxy.CADir(m.Cfg), "root.crt") }

func (m *Mac) resolverPath(tld string) string { return filepath.Join(m.ResolverDir, tld) }

// resolverFile is what /etc/resolver/<tld> must contain. resolver(5) has no comment syntax, so the
// file cannot carry a marker; a file with exactly this content is treated as Dotlane's.
func (m *Mac) resolverFile() []byte {
	return []byte(fmt.Sprintf("nameserver 127.0.0.1\nport %d\n", m.Cfg.DNSPort))
}

func (m *Mac) say(format string, args ...any) { fmt.Fprintf(m.Out, format+"\n", args...) }

// sudo explains, shows and runs one privileged command.
func (m *Mac) sudo(ctx context.Context, why string, stdin []byte, name string, args ...string) error {
	if why != "" {
		m.say("\n%s", why)
	}
	m.say("  $ sudo %s %s", name, strings.Join(quoteAll(args), " "))
	if err := m.Runner.Sudo(ctx, stdin, name, args...); err != nil {
		return fmt.Errorf("sudo %s: %w (run dotlane in a terminal so sudo can ask for your password)",
			filepath.Base(name), err)
	}
	return nil
}

func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t'\"$") {
			a = strconv.Quote(a)
		}
		out[i] = a
	}
	return out
}

// Install sets everything up; already-done steps are reported and skipped.
func (m *Mac) Install(ctx context.Context) error {
	if strings.Contains(m.Exe, "go-build") || strings.HasPrefix(m.Exe, os.TempDir()) {
		return fmt.Errorf("%s is a temporary build; build dotlane first (go build -o bin/dotlane ./cmd/dotlane) and install from it", m.Exe)
	}
	// Refuse before changing anything if another tool owns one of the resolver files.
	for _, tld := range m.Cfg.TLDs {
		if have, err := os.ReadFile(m.resolverPath(tld)); err == nil && !bytes.Equal(have, m.resolverFile()) {
			return fmt.Errorf("%s already exists with other settings, so another tool answers .%s:\n%s\nremove it (sudo rm %[1]s) or choose another domain ending",
				m.resolverPath(tld), tld, indent(have))
		}
	}

	if err := m.installService(ctx); err != nil {
		return err
	}
	if err := m.trustCA(ctx); err != nil {
		return err
	}
	for _, tld := range m.Cfg.TLDs {
		if err := m.installResolver(ctx, tld); err != nil {
			return err
		}
	}
	m.verifyDNS(ctx)

	m.say("\nDotlane is installed. Add a site with: dotlane add <name> <port>")
	return nil
}

func (m *Mac) installService(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(m.logPath()), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.plistPath()), 0o755); err != nil {
		return err
	}
	want := m.plist()
	have, _ := os.ReadFile(m.plistPath())
	_, printErr := m.Runner.Run(ctx, "/bin/launchctl", "print", m.target())
	loaded := printErr == nil

	if bytes.Equal(have, want) && loaded {
		m.say("ok    login service %s already installed", m.Cfg.ServiceName)
	} else {
		if err := os.WriteFile(m.plistPath(), want, 0o644); err != nil {
			return err
		}
		if loaded { // reinstall or upgrade: replace the running copy
			m.Runner.Run(ctx, "/bin/launchctl", "bootout", m.target())
		}
		if out, err := m.Runner.Run(ctx, "/bin/launchctl", "bootstrap", fmt.Sprintf("gui/%d", m.UID), m.plistPath()); err != nil {
			return fmt.Errorf("launchctl bootstrap: %v: %s", err, bytes.TrimSpace(out))
		}
		m.say("ok    login service %s installed (%s)", m.Cfg.ServiceName, m.plistPath())
	}

	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := m.Ready(waitCtx); err != nil {
		return fmt.Errorf("the service did not start: %v\nsee %s", err, m.logPath())
	}
	m.say("ok    service running, logging to %s", m.logPath())
	return nil
}

// plist is the launchd agent: start at login, restart on exit.
func (m *Mac) plist() []byte {
	esc := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Written by dotlane install; dotlane uninstall removes it. -->
<plist version="1.0">
<dict>
  <key>Label</key><string>` + esc(m.Cfg.ServiceName) + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + esc(m.Exe) + `</string>
    <string>--config</string>
    <string>` + esc(m.ConfigPath) + `</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>` + esc(m.logPath()) + `</string>
  <key>StandardErrorPath</key><string>` + esc(m.logPath()) + `</string>
</dict>
</plist>
`)
}

func (m *Mac) caTrusted(ctx context.Context) bool {
	_, err := m.Runner.Run(ctx, "/usr/bin/security", "verify-cert", "-c", m.rootCert())
	return err == nil
}

func (m *Mac) trustCA(ctx context.Context) error {
	// The daemon creates the CA when it first starts; give it a moment.
	for i := 0; ; i++ {
		if _, err := os.Stat(m.rootCert()); err == nil {
			break
		}
		if i == 50 {
			return fmt.Errorf("the service has not created its certificate authority (%s); see %s", m.rootCert(), m.logPath())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if m.caTrusted(ctx) {
		m.say("ok    certificate authority already trusted")
		return nil
	}
	err := m.sudo(ctx,
		"Trust Dotlane's local certificate authority, so browsers and tools accept its HTTPS certificates.\n"+
			"It is added to the System keychain; dotlane uninstall removes it. (macOS may also show a password dialog.)",
		nil, "/usr/bin/security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", systemKeychain, m.rootCert())
	if err != nil {
		return err
	}
	if !m.caTrusted(ctx) {
		return errors.New("the certificate authority was added but macOS still does not trust it")
	}
	m.say("ok    certificate authority trusted")
	return nil
}

func (m *Mac) installResolver(ctx context.Context, tld string) error {
	path := m.resolverPath(tld)
	if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, m.resolverFile()) {
		m.say("ok    %s already sends .%s lookups to Dotlane", path, tld)
		return nil
	}
	why := fmt.Sprintf("Send lookups for *.%s, and only those, to Dotlane's DNS on 127.0.0.1:%d.", tld, m.Cfg.DNSPort)
	if err := m.sudo(ctx, why, nil, "/bin/mkdir", "-p", m.ResolverDir); err != nil {
		return err
	}
	if err := m.sudo(ctx, "", m.resolverFile(), "/usr/bin/tee", path); err != nil {
		return err
	}
	m.say("ok    %s written", path)
	return nil
}

// verifyDNS checks the whole path through the macOS resolver. A failure is reported, not fatal:
// macOS sometimes takes a few seconds to pick up a new resolver file.
func (m *Mac) verifyDNS(ctx context.Context) {
	for _, tld := range m.Cfg.TLDs {
		host := "dotlane-check." + tld
		var addrs []string
		var err error
		for i := 0; i < 20; i++ {
			lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
			addrs, err = m.Lookup(lookupCtx, host)
			cancel()
			if err == nil && containsLoopback(addrs) {
				m.say("ok    %s resolves to this Mac", host)
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if err != nil || !containsLoopback(addrs) {
			m.say("warn  %s does not resolve yet (%v %v); try again in a few seconds with: dscacheutil -q host -a name %s",
				host, addrs, err, host)
		}
	}
}

func containsLoopback(addrs []string) bool {
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.IsLoopback() {
			return true
		}
	}
	return false
}

// Uninstall reverses Install. With purge, it also deletes the state directory (sites and the
// CA's keys) and the log.
func (m *Mac) Uninstall(ctx context.Context, purge bool) error {
	if _, err := m.Runner.Run(ctx, "/bin/launchctl", "print", m.target()); err == nil {
		if out, err := m.Runner.Run(ctx, "/bin/launchctl", "bootout", m.target()); err != nil {
			return fmt.Errorf("launchctl bootout: %v: %s", err, bytes.TrimSpace(out))
		}
		m.say("ok    login service %s stopped", m.Cfg.ServiceName)
	}
	if err := os.Remove(m.plistPath()); err == nil {
		m.say("ok    %s removed", m.plistPath())
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := m.untrustCA(ctx); err != nil {
		return err
	}

	for _, tld := range m.Cfg.TLDs {
		path := m.resolverPath(tld)
		have, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return err
		case !bytes.Equal(have, m.resolverFile()):
			m.say("skip  %s has other settings, so it is not Dotlane's; left in place", path)
		default:
			if err := m.sudo(ctx, fmt.Sprintf("Stop sending *.%s lookups to Dotlane.", tld), nil, "/bin/rm", path); err != nil {
				return err
			}
			m.say("ok    %s removed", path)
		}
	}

	if purge {
		for _, p := range []string{m.Cfg.StateDir, m.logPath()} {
			if err := os.RemoveAll(p); err != nil {
				return err
			}
			m.say("ok    %s deleted", p)
		}
	} else {
		m.say("\nKept %s (sites and the certificate authority's keys) and the log at %s;\n"+
			"dotlane uninstall --purge deletes them.", m.Cfg.StateDir, m.logPath())
	}
	m.say("\nDotlane is uninstalled.")
	return nil
}

func (m *Mac) untrustCA(ctx context.Context) error {
	data, err := os.ReadFile(m.rootCert())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("%s is not a PEM certificate", m.rootCert())
	}
	sum := sha1.Sum(block.Bytes)
	hash := strings.ToUpper(hex.EncodeToString(sum[:]))

	out, _ := m.Runner.Run(ctx, "/usr/bin/security", "find-certificate", "-a", "-Z", systemKeychain)
	if !bytes.Contains(out, []byte(hash)) {
		return nil
	}
	why := "Remove Dotlane's certificate authority from the System keychain, so its certificates are no longer trusted."
	// Trust settings first, then the certificate itself; one password covers both (sudo caches it).
	if err := m.sudo(ctx, why, nil, "/usr/bin/security", "remove-trusted-cert", "-d", m.rootCert()); err != nil {
		return err
	}
	if err := m.sudo(ctx, "", nil, "/usr/bin/security", "delete-certificate", "-Z", hash, systemKeychain); err != nil {
		return err
	}
	m.say("ok    certificate authority removed from the System keychain")
	return nil
}

func indent(b []byte) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}
