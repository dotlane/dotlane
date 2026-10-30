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
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedrodee/dotlane/internal/config"
	"github.com/pedrodee/dotlane/internal/proxy"
	"github.com/pedrodee/dotlane/internal/ui"
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
	UI          *ui.Printer
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
		UI:          ui.New(os.Stdout),
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

// summary names what is being installed, for the title line.
func (m *Mac) summary() string {
	tlds := make([]string, len(m.Cfg.TLDs))
	for i, t := range m.Cfg.TLDs {
		tlds[i] = "." + t
	}
	return m.Cfg.ServiceName + " · " + strings.Join(tlds, " ")
}

func (m *Mac) target() string { return fmt.Sprintf("gui/%d/%s", m.UID, m.Cfg.ServiceName) }

func (m *Mac) rootCert() string { return filepath.Join(proxy.CADir(m.Cfg), "root.crt") }

func (m *Mac) resolverPath(tld string) string { return filepath.Join(m.ResolverDir, tld) }

// resolverFile is what /etc/resolver/<tld> must contain. resolver(5) has no comment syntax, so the
// file cannot carry a marker; a file with exactly this content is treated as Dotlane's.
func (m *Mac) resolverFile() []byte {
	return []byte(fmt.Sprintf("nameserver 127.0.0.1\nport %d\n", m.Cfg.DNSPort))
}

// sudo shows one privileged command, then runs it. Callers announce the step with UI.Ask first.
func (m *Mac) sudo(ctx context.Context, stdin []byte, name string, args ...string) error {
	shown := make([]string, len(args))
	for i, a := range args {
		shown[i] = m.UI.Path(a)
	}
	m.UI.Command("sudo " + name + " " + strings.Join(quoteAll(shown), " "))
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
	m.UI.Title("Installing Dotlane", m.summary())

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

	m.UI.Done("Dotlane is installed.", "Add a site: dotlane add <name> <port>")
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
		m.UI.OK("Login service", "already installed")
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
		m.UI.OK("Login service", "installed · "+m.UI.Path(m.plistPath()))
	}

	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := m.Ready(waitCtx); err != nil {
		return fmt.Errorf("the service did not start: %v\nsee %s", err, m.logPath())
	}
	m.UI.OK("Running", "log: "+m.UI.Path(m.logPath()))
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
		m.UI.OK("Certificate authority", "already trusted")
		return nil
	}
	m.UI.Ask("Certificate authority",
		"Trusts Dotlane's local certificate authority, so browsers and tools accept its HTTPS\n"+
			"    certificates. Added to the System keychain; dotlane uninstall removes it.\n"+
			"    macOS may also show a password dialog.")
	err := m.sudo(ctx, nil, "/usr/bin/security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", systemKeychain, m.rootCert())
	if err != nil {
		return err
	}
	if !m.caTrusted(ctx) {
		return errors.New("the certificate authority was added but macOS still does not trust it")
	}
	m.UI.OK("Certificate authority", "trusted")
	return nil
}

func (m *Mac) installResolver(ctx context.Context, tld string) error {
	path := m.resolverPath(tld)
	if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, m.resolverFile()) {
		m.UI.OK("DNS for ."+tld, "already routed · "+path)
		return nil
	}
	m.UI.Ask("DNS for ."+tld,
		fmt.Sprintf("Sends lookups for *.%s, and only those, to Dotlane's DNS on 127.0.0.1:%d.", tld, m.Cfg.DNSPort))
	if err := m.sudo(ctx, nil, "/bin/mkdir", "-p", m.ResolverDir); err != nil {
		return err
	}
	if err := m.sudo(ctx, m.resolverFile(), "/usr/bin/tee", path); err != nil {
		return err
	}
	m.UI.OK("DNS for ."+tld, path+" written")
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
				m.UI.OK("DNS check", host+" → this Mac")
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if err != nil || !containsLoopback(addrs) {
			m.UI.Warn("DNS check", fmt.Sprintf("%s does not resolve yet; macOS can take a few seconds.\n"+
				"    Check with: dscacheutil -q host -a name %s", host, host))
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
	m.UI.Title("Uninstalling Dotlane", m.summary())

	removed := false
	if _, err := m.Runner.Run(ctx, "/bin/launchctl", "print", m.target()); err == nil {
		if out, err := m.Runner.Run(ctx, "/bin/launchctl", "bootout", m.target()); err != nil {
			return fmt.Errorf("launchctl bootout: %v: %s", err, bytes.TrimSpace(out))
		}
		removed = true
	}
	if err := os.Remove(m.plistPath()); err == nil {
		removed = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if removed {
		m.UI.OK("Login service", "stopped and removed")
	} else {
		m.UI.Skip("Login service", "not installed")
	}

	if err := m.untrustCA(ctx); err != nil {
		return err
	}

	for _, tld := range m.Cfg.TLDs {
		label := "DNS for ." + tld
		path := m.resolverPath(tld)
		have, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			m.UI.Skip(label, "not set")
		case err != nil:
			return err
		case !bytes.Equal(have, m.resolverFile()):
			m.UI.Skip(label, path+" has other settings, so it is not Dotlane's; left in place")
		default:
			m.UI.Ask(label, fmt.Sprintf("Stops sending *.%s lookups to Dotlane.", tld))
			if err := m.sudo(ctx, nil, "/bin/rm", path); err != nil {
				return err
			}
			m.UI.OK(label, path+" removed")
		}
	}

	if purge {
		if err := os.RemoveAll(m.Cfg.StateDir); err != nil {
			return err
		}
		m.UI.OK("Sites and CA keys", "deleted · "+m.UI.Path(m.Cfg.StateDir))
		if err := os.RemoveAll(m.logPath()); err != nil {
			return err
		}
		os.Remove(filepath.Dir(m.logPath())) // the logs folder, if no other service uses it
		m.UI.OK("Log", "deleted · "+m.UI.Path(m.logPath()))
	} else {
		m.UI.Skip("Sites and CA keys", "kept · "+m.UI.Path(m.Cfg.StateDir))
		m.UI.Skip("Log", "kept · "+m.UI.Path(m.logPath()))
	}

	next := ""
	if !purge {
		next = m.UI.Dim("To delete the kept files too: dotlane uninstall --purge")
	}
	m.UI.Done("Dotlane is uninstalled.", next)
	return nil
}

func (m *Mac) untrustCA(ctx context.Context) error {
	data, err := os.ReadFile(m.rootCert())
	if errors.Is(err, os.ErrNotExist) {
		m.UI.Skip("Certificate authority", "none created")
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
		m.UI.Skip("Certificate authority", "not in the System keychain")
		return nil
	}
	m.UI.Ask("Certificate authority",
		"Removes Dotlane's certificate authority from the System keychain, so its certificates\n"+
			"    are no longer trusted.")
	// Trust settings first, then the certificate itself; one password covers both (sudo caches it).
	if err := m.sudo(ctx, nil, "/usr/bin/security", "remove-trusted-cert", "-d", m.rootCert()); err != nil {
		return err
	}
	if err := m.sudo(ctx, nil, "/usr/bin/security", "delete-certificate", "-Z", hash, systemKeychain); err != nil {
		return err
	}
	m.UI.OK("Certificate authority", "removed from the System keychain")
	return nil
}

func indent(b []byte) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}
