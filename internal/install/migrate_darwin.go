//go:build darwin

package install

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/pedrodee/dotlane/internal/proxy"
	"github.com/pedrodee/dotlane/internal/sites"
)

// The Bash prototype (files/bin/dotlane): Homebrew's Caddy service with one file per site, and a
// dnsmasq launchd agent answering the domain ending.
const (
	prototypeDNSLabel = "dev.traix.dnsmasq"
	prototypeScript   = "# dotlane: local https://<name>.<tld> sites in front of dev servers, via Caddy."
)

// Homebrew's label for its Caddy service: sh.brew.* in current versions, homebrew.mxcl.* before.
var prototypeCaddyLabels = []string{"sh.brew.caddy", "homebrew.mxcl.caddy"}

// sitePattern reads a site file's first line, as dotlane add wrote it.
var sitePattern = regexp.MustCompile(`^# Managed by dotlane: https://([a-z0-9-]+)\.([a-z0-9-]+) -> localhost:(\d+)\s*$`)

// prototype is what migrate found of the Bash version.
type prototype struct {
	sites      []sites.Site
	caDir      string // Homebrew Caddy's local CA; empty if there is none
	caddyLabel string // the Homebrew Caddy service, when loaded
	dnsmasq    bool   // the dnsmasq agent is loaded
}

func (m *Mac) sitesDir() string { return filepath.Join(m.Home, ".config", "caddy", "sites") }

func (m *Mac) prototypeCADir() string {
	return filepath.Join(m.BrewPrefix, "var", "lib", "caddy", "pki", "authorities", "local")
}

func (m *Mac) loaded(ctx context.Context, label string) bool {
	_, err := m.Runner.Run(ctx, "/bin/launchctl", "print", fmt.Sprintf("gui/%d/%s", m.UID, label))
	return err == nil
}

// findPrototype looks for the prototype without changing anything. Site files for a domain
// ending Dotlane is not configured for stop the migration: those sites would silently vanish.
func (m *Mac) findPrototype(ctx context.Context) (*prototype, error) {
	p := &prototype{}
	files, _ := filepath.Glob(filepath.Join(m.sitesDir(), "*.caddy"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		line, _, _ := bytes.Cut(data, []byte("\n"))
		match := sitePattern.FindSubmatch(line)
		if match == nil {
			m.UI.Skip(filepath.Base(f), "not written by dotlane add; left out")
			continue
		}
		name, tld := string(match[1]), string(match[2])
		port, _ := strconv.Atoi(string(match[3]))
		if !slices.Contains(m.Cfg.TLDs, tld) {
			return nil, fmt.Errorf("%s serves https://%s.%s, but Dotlane is set up for %s\n"+
				"add tlds = [\"%s\"] to %s, then run dotlane migrate again",
				m.UI.Path(f), name, tld, m.endings(), tld, m.UI.Path(m.ConfigPath))
		}
		site := sites.Site{Name: name, Port: port}
		if err := site.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", m.UI.Path(f), err)
		}
		p.sites = append(p.sites, site)
	}
	if _, err := os.Stat(filepath.Join(m.prototypeCADir(), "root.key")); err == nil {
		p.caDir = m.prototypeCADir()
	}
	for _, label := range prototypeCaddyLabels {
		if m.loaded(ctx, label) {
			p.caddyLabel = label
			break
		}
	}
	p.dnsmasq = m.loaded(ctx, prototypeDNSLabel)
	return p, nil
}

// Migrate moves this Mac from the Bash prototype to Dotlane: it keeps the prototype's sites and
// its already-trusted certificate authority, stops Homebrew's Caddy and the dnsmasq agent, and
// installs Dotlane in their place. If the install fails, the prototype is started again. Nothing
// of the prototype is deleted; the files that can go are listed at the end.
func (m *Mac) Migrate(ctx context.Context) error {
	m.UI.Title("Migrating from the prototype", m.summary())
	p, err := m.findPrototype(ctx)
	if err != nil {
		return err
	}
	if len(p.sites) == 0 && p.caDir == "" && p.caddyLabel == "" && !p.dnsmasq {
		return errors.New("no Dotlane prototype found on this Mac\nto set Dotlane up from scratch: dotlane install")
	}

	installed := m.loaded(ctx, m.Cfg.ServiceName) // a re-run: keep the service on failure
	if err := m.adoptCA(ctx, p); err != nil {
		return err
	}
	if err := m.importSites(ctx, p.sites); err != nil {
		return err
	}
	if err := m.stopPrototype(ctx, p); err != nil {
		m.restartPrototype(ctx, p)
		return err
	}

	m.UI.Line("")
	if err := m.Install(ctx); err != nil {
		m.UI.Line("")
		m.UI.Title("Putting the prototype back", "")
		if !installed {
			m.removeService(ctx)
		}
		m.restartPrototype(ctx, p)
		return fmt.Errorf("%w\nthe prototype is running again; its files were not touched", err)
	}

	m.UI.Line("")
	m.UI.Title("Checking the sites", "trusted HTTPS, without new passwords")
	for _, s := range p.sites {
		host := proxy.Hosts(m.Cfg, s)[0]
		url := "https://" + host
		if m.Cfg.HTTPSPort != 443 {
			url += ":" + strconv.Itoa(m.Cfg.HTTPSPort)
		}
		if err := m.VerifyTLS(ctx, host, m.Cfg.HTTPSPort); err != nil {
			m.UI.Warn(url, err.Error())
		} else {
			m.UI.SiteOK(url, s.Port)
		}
	}
	m.replaceScript()
	m.listLeftovers()
	m.UI.Done("Moved to Dotlane.", "Projects with a dotlane.toml: run dotlane up in each to claim their sites.")
	return nil
}

// adoptCA copies the prototype's root certificate and key into Dotlane's state before the daemon
// first starts, so Caddy signs with the root macOS already trusts. A Dotlane CA that already
// exists is kept: its key may have signed certificates, and replacing it would be a surprise.
func (m *Mac) adoptCA(ctx context.Context, p *prototype) error {
	const label = "Certificate authority"
	if p.caDir == "" {
		m.UI.Skip(label, "the prototype has none; dotlane install creates one")
		return nil
	}
	theirs, err := os.ReadFile(filepath.Join(p.caDir, "root.crt"))
	if err != nil {
		return err
	}
	if ours, err := os.ReadFile(m.rootCert()); err == nil {
		if bytes.Equal(ours, theirs) {
			m.UI.OK(label, "already the prototype's")
		} else {
			m.UI.Warn(label, "Dotlane already has its own; keeping it (trusting it asks for your password)")
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	dir := proxy.CADir(m.Cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"root.crt", "root.key"} {
		data, err := os.ReadFile(filepath.Join(p.caDir, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
	}
	detail := "copied from " + m.UI.Path(p.caDir)
	if m.caTrusted(ctx) {
		detail += " · already trusted"
	}
	m.UI.OK(label, detail)
	return nil
}

// importSites registers the prototype's sites, through the daemon when it runs, otherwise
// straight into the registry it will load. A name Dotlane already has is left as it is.
func (m *Mac) importSites(ctx context.Context, list []sites.Site) error {
	if len(list) == 0 {
		m.UI.Skip("Sites", "the prototype has none")
		return nil
	}
	registered := map[string]sites.Site{}
	var add func(sites.Site) error
	if _, err := m.Daemon(ctx); err == nil {
		c := m.Sites
		current, err := c.Sites(ctx)
		if err != nil {
			return err
		}
		for _, s := range current {
			registered[s.Name] = sites.Site{Name: s.Name, Port: s.Port, Project: s.Project}
		}
		add = func(s sites.Site) error { _, err := c.AddSite(ctx, s.Name, s.Port, ""); return err }
	} else {
		if err := os.MkdirAll(m.Cfg.StateDir, 0o700); err != nil {
			return err
		}
		r, err := sites.Open(filepath.Join(m.Cfg.StateDir, "sites.json"))
		if err != nil {
			return err
		}
		for _, s := range r.List() {
			registered[s.Name] = s
		}
		add = r.Put
	}

	for _, s := range list {
		label := "Site " + s.Name
		if have, ok := registered[s.Name]; ok {
			if have.Port == s.Port {
				m.UI.OK(label, "already in Dotlane")
			} else {
				m.UI.Warn(label, fmt.Sprintf("Dotlane already has it on localhost:%d; keeping that (the prototype used %d)", have.Port, s.Port))
			}
			continue
		}
		if err := add(s); err != nil {
			return fmt.Errorf("importing %s: %w", s.Name, err)
		}
		m.UI.OK(label, fmt.Sprintf("imported · → localhost:%d", s.Port))
	}
	return nil
}

func (m *Mac) brew() string { return filepath.Join(m.BrewPrefix, "bin", "brew") }

// stopPrototype stops Homebrew's Caddy and the dnsmasq agent, which hold the ports Dotlane
// takes over. The agent is disabled, not deleted, so it does not come back at login.
func (m *Mac) stopPrototype(ctx context.Context, p *prototype) error {
	if p.caddyLabel != "" {
		m.UI.Command("brew services stop caddy")
		if out, err := m.Runner.Run(ctx, m.brew(), "services", "stop", "caddy"); err != nil {
			return fmt.Errorf("brew services stop caddy: %v: %s", err, bytes.TrimSpace(out))
		}
		m.UI.OK("Prototype's Caddy", "stopped")
	}
	if p.dnsmasq {
		target := fmt.Sprintf("gui/%d/%s", m.UID, prototypeDNSLabel)
		m.UI.Command("launchctl bootout " + target + " && launchctl disable " + target)
		if out, err := m.Runner.Run(ctx, "/bin/launchctl", "bootout", target); err != nil {
			return fmt.Errorf("launchctl bootout: %v: %s", err, bytes.TrimSpace(out))
		}
		if out, err := m.Runner.Run(ctx, "/bin/launchctl", "disable", target); err != nil {
			return fmt.Errorf("launchctl disable: %v: %s", err, bytes.TrimSpace(out))
		}
		m.UI.OK("Prototype's dnsmasq", "stopped, and kept from starting at login")
	}

	// launchd returns before the processes have closed their sockets. Only the prototype's are
	// waited for; anything else in the way is for install's checks to report.
	ports := []struct {
		proto string
		port  int
	}{{"tcp", m.Cfg.HTTPSPort}, {"tcp", m.Cfg.HTTPPort}, {"udp", m.Cfg.DNSPort}}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		busy := false
		for _, pp := range ports {
			for _, l := range listeners(ctx, m.Runner, pp.proto, pp.port) {
				busy = busy || (l.Label != "" && (l.Label == p.caddyLabel || l.Label == prototypeDNSLabel))
			}
		}
		if !busy {
			break
		}
	}
	return nil
}

// restartPrototype undoes stopPrototype. Failures are reported, with the command to retry.
func (m *Mac) restartPrototype(ctx context.Context, p *prototype) {
	if p.caddyLabel != "" {
		if out, err := m.Runner.Run(ctx, m.brew(), "services", "start", "caddy"); err != nil {
			m.UI.Fail("Prototype's Caddy", fmt.Sprintf("did not start: %v: %s", err, bytes.TrimSpace(out)))
			m.UI.Hint("brew services start caddy")
		} else {
			m.UI.OK("Prototype's Caddy", "running again")
		}
	}
	if p.dnsmasq {
		target := fmt.Sprintf("gui/%d/%s", m.UID, prototypeDNSLabel)
		plist := filepath.Join(m.Home, "Library", "LaunchAgents", prototypeDNSLabel+".plist")
		m.Runner.Run(ctx, "/bin/launchctl", "enable", target)
		if out, err := m.Runner.Run(ctx, "/bin/launchctl", "bootstrap", fmt.Sprintf("gui/%d", m.UID), plist); err != nil {
			m.UI.Fail("Prototype's dnsmasq", fmt.Sprintf("did not start: %v: %s", err, bytes.TrimSpace(out)))
			m.UI.Hint(fmt.Sprintf("launchctl enable %s && launchctl bootstrap gui/%d %s", target, m.UID, m.UI.Path(plist)))
		} else {
			m.UI.OK("Prototype's dnsmasq", "running again")
		}
	}
}

// removeService stops and removes Dotlane's login service, so the prototype gets its ports back.
// Unlike Uninstall it leaves the CA's trust alone: that CA is the prototype's.
func (m *Mac) removeService(ctx context.Context) {
	if _, err := m.Runner.Run(ctx, "/bin/launchctl", "print", m.target()); err == nil {
		m.Runner.Run(ctx, "/bin/launchctl", "bootout", m.target())
	}
	if err := os.Remove(m.plistPath()); err == nil {
		m.UI.OK("Login service", "removed")
	}
}

// replaceScript points ~/.local/bin/dotlane, where the prototype script lives, at this binary,
// so `dotlane` keeps working from the shell. Any other file there is left alone.
func (m *Mac) replaceScript() {
	path := filepath.Join(m.Home, ".local", "bin", "dotlane")
	data, err := os.ReadFile(path)
	if info, lerr := os.Lstat(path); lerr != nil || info.Mode()&fs.ModeSymlink != 0 || err != nil {
		return
	}
	if !bytes.Contains(data, []byte(prototypeScript)) {
		return
	}
	tmp := path + ".new"
	os.Remove(tmp)
	if err := os.Symlink(m.Exe, tmp); err != nil {
		m.UI.Warn("dotlane command", "could not replace the prototype script: "+err.Error())
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		m.UI.Warn("dotlane command", "could not replace the prototype script: "+err.Error())
		return
	}
	m.UI.OK("dotlane command", m.UI.Path(path)+" now runs "+m.UI.Path(m.Exe)+" (was the prototype script)")
}

// listLeftovers names the prototype's files that nothing uses any more. Deleting them is left
// to the user.
func (m *Mac) listLeftovers() {
	var paths []string
	for _, p := range []string{
		m.sitesDir(),
		filepath.Join(m.Home, ".config", "dnsmasq", "traix.conf"),
		filepath.Join(m.Home, "Library", "LaunchAgents", prototypeDNSLabel+".plist"),
		filepath.Join(m.BrewPrefix, "etc", "Caddyfile"),
		filepath.Join(m.BrewPrefix, "var", "lib", "caddy"),
	} {
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, m.UI.Path(p))
		}
	}
	if len(paths) == 0 {
		return
	}
	m.UI.Line("")
	m.UI.Line("%s", m.UI.Dim("The prototype's files are no longer used. Delete them once you are happy (no password needed):"))
	for _, p := range paths {
		m.UI.Command("rm -r " + p)
	}
	m.UI.Command("brew uninstall caddy dnsmasq   # if nothing else uses them")
}

// verifyTLS completes a TLS handshake with host on 127.0.0.1:port, checked against the system's
// trusted roots: what a browser would accept.
func verifyTLS(ctx context.Context, host string, port int) error {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 2 * time.Second},
		Config:    &tls.Config{ServerName: host},
	}
	var err error
	for i := 0; i < 25; i++ { // a new site's certificate is issued in the background
		var conn net.Conn
		if conn, err = dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			return conn.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	var untrusted *tls.CertificateVerificationError
	if errors.As(err, &untrusted) {
		return errors.New("its certificate is not trusted; run dotlane doctor")
	}
	return err
}

// brewPrefix is Homebrew's prefix: /opt/homebrew on Apple Silicon, /usr/local on Intel.
func brewPrefix() string {
	for _, p := range []string{"/opt/homebrew", "/usr/local"} {
		if _, err := os.Stat(filepath.Join(p, "bin", "brew")); err == nil {
			return p
		}
	}
	return "/opt/homebrew"
}
