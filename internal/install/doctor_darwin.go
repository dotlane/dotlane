//go:build darwin

package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/proxy"
)

// Doctor implements Installer: the installation's state, and anything in its way.
func (m *Mac) Doctor(ctx context.Context) []Check {
	var checks []Check
	add := func(c Check) { checks = append(checks, c) }

	source := "defaults (no config file)"
	if m.Cfg.Path != "" {
		source = m.UI.Path(m.Cfg.Path)
	}
	add(Check{Name: "Config", Status: Pass, Detail: fmt.Sprintf("%s · %s · DNS :%d · HTTPS :%d",
		source, m.endings(), m.Cfg.DNSPort, m.Cfg.HTTPSPort)})

	service := m.serviceCheck(ctx)
	add(service)
	installed := service.Detail != "not installed"
	pid := 0
	if st, err := m.Daemon(ctx); err == nil {
		pid = st.PID
		if stale := m.stale(st); stale != "" {
			add(Check{Name: "Daemon", Status: Warning, Detail: fmt.Sprintf("pid %d started before the %s", st.PID, stale),
				Fix: "dotlane doctor --fix (restarts it)", Repairable: installed})
		} else {
			add(Check{Name: "Daemon", Status: Pass, Detail: fmt.Sprintf("running · pid %d · %s", st.PID, st.Version)})
		}
	} else if installed {
		add(Check{Name: "Daemon", Status: Failed, Detail: "not answering on its socket",
			Fix: "dotlane doctor --fix (restarts it); its log: " + m.UI.Path(m.logPath()), Repairable: true})
	} else {
		add(Check{Name: "Daemon", Status: Skipped, Detail: "not installed yet"})
	}

	add(m.portCheck(ctx, "HTTPS port", "tcp", m.Cfg.HTTPSPort, pid))
	add(m.portCheck(ctx, "HTTP port", "tcp", m.Cfg.HTTPPort, pid))
	dns := m.portCheck(ctx, "DNS port", "udp", m.Cfg.DNSPort, pid)
	if dns.Conflict && !m.Cfg.DNSPortSet && !installed {
		// Not in the way: install moves to a free port when the default one is taken.
		dns.Status, dns.Conflict = Warning, false
		dns.Fix = "nothing to do: dotlane install will pick a free port"
	}
	add(dns)
	if ca := m.caCheck(ctx); installed || ca.Status == Pass {
		add(ca)
	} else {
		add(Check{Name: "Certificate authority", Status: Skipped, Detail: "created and trusted by dotlane install"})
	}
	add(m.permissionsCheck())
	resolversOK := true
	for _, tld := range m.Cfg.TLDs {
		c := m.resolverCheck(ctx, tld)
		if c.Status == Pass && pid == 0 {
			// The right file, but someone else may be answering on that port.
			for _, p := range listeners(ctx, m.Runner, "udp", m.Cfg.DNSPort) {
				if p.Label != m.Cfg.ServiceName {
					c.Status = Warning
					c.Detail += ", answered by " + identify(p, m.Cfg.DNSPort, m.UID, m.Home).Name + " for now"
					break
				}
			}
		}
		resolversOK = resolversOK && c.Status == Pass
		add(c)
	}
	if pid != 0 && resolversOK {
		for _, tld := range m.Cfg.TLDs {
			add(m.lookupCheck(ctx, tld))
		}
	}
	return checks
}

func (m *Mac) endings() string {
	tlds := make([]string, len(m.Cfg.TLDs))
	for i, t := range m.Cfg.TLDs {
		tlds[i] = "." + t
	}
	return strings.Join(tlds, " ")
}

func (m *Mac) serviceCheck(ctx context.Context) Check {
	c := Check{Name: "Login service", Fix: "dotlane install", Repairable: true}
	have, err := os.ReadFile(m.plistPath())
	_, printErr := m.Runner.Run(ctx, "/bin/launchctl", "print", m.target())
	switch {
	case errors.Is(err, os.ErrNotExist):
		c.Status, c.Detail = Failed, "not installed"
	case !bytes.Equal(have, m.plist()):
		c.Status, c.Detail = Warning, "installed for another dotlane binary or config file"
		c.Fix = "dotlane install (updates it to this one)"
	case printErr != nil:
		c.Status, c.Detail = Failed, "installed but not loaded"
	case !sameFile(m.Exe, m.serviceExe()):
		c.Status, c.Detail = Warning, "runs an older copy of this binary"
		c.Fix = "dotlane install (updates the copy and restarts the service)"
	default:
		c.Status, c.Detail, c.Fix, c.Repairable = Pass, m.Cfg.ServiceName+" · starts at login", "", false
	}
	return c
}

// portCheck reports who holds port. daemonPID is the answering daemon's, or 0.
func (m *Mac) portCheck(ctx context.Context, name, proto string, port, daemonPID int) Check {
	c := Check{Name: name}
	label := fmt.Sprintf(":%d", port)
	var others []string
	var fixes []string
	ours := false
	for _, p := range listeners(ctx, m.Runner, proto, port) {
		// Ours by PID, or by launchd label when the daemon is running but not answering.
		if p.PID == daemonPID || p.Label == m.Cfg.ServiceName {
			ours = true
			continue
		}
		t := identify(p, port, m.UID, m.Home)
		others = append(others, t.Name)
		fixes = append(fixes, t.Fix)
	}
	switch {
	case len(others) > 0:
		c.Status, c.Conflict = Failed, true
		c.Detail = label + " is in use by " + strings.Join(others, " and ")
		if ours {
			c.Detail += ", next to Dotlane (they would split its traffic)"
		}
		c.Fix = strings.Join(fixes, "\n")
	case ours:
		c.Status, c.Detail = Pass, label+" · Dotlane"
	case daemonPID != 0:
		c.Status, c.Detail = Failed, label+" · Dotlane is running but not listening here"
		c.Fix = "see its log: " + m.UI.Path(m.logPath())
	default:
		if err := m.Bindable(proto, port); errors.Is(err, syscall.EACCES) {
			c.Status, c.Conflict = Failed, true
			c.Detail = label + " needs administrator rights to listen on"
			c.Fix = "use a port of 1024 or more in " + m.UI.Path(m.ConfigPath)
		} else if err != nil {
			c.Status, c.Conflict = Failed, true
			c.Detail = label + " is in use by a process of another user"
			c.Fix = fmt.Sprintf("find it with: sudo lsof -nP -i%s:%d", strings.ToUpper(proto), port)
		} else {
			c.Status, c.Detail = Pass, label+" · free"
		}
	}
	return c
}

func (m *Mac) caCheck(ctx context.Context) Check {
	c := Check{Name: "Certificate authority", Fix: "dotlane install (asks for your password)", Repairable: true}
	if _, err := os.Stat(m.rootCert()); err != nil {
		c.Status, c.Detail = Failed, "not created yet (the daemon creates it when it starts)"
	} else if !m.caTrusted(ctx) {
		c.Status, c.Detail = Failed, "not trusted by macOS"
	} else {
		c.Status, c.Detail, c.Fix, c.Repairable = Pass, "trusted in the System keychain", "", false
	}
	return c
}

func (m *Mac) resolverCheck(ctx context.Context, tld string) Check {
	c := Check{Name: "DNS for ." + tld}
	path := m.resolverPath(tld)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		c.Status, c.Detail, c.Fix, c.Repairable = Failed, path+" missing", "dotlane install (asks for your password)", true
	case err != nil:
		c.Status, c.Detail = Failed, err.Error()
	case bytes.Equal(data, m.resolverFile()):
		c.Status, c.Detail = Pass, fmt.Sprintf("%s → 127.0.0.1:%d", path, m.Cfg.DNSPort)
	default:
		c.Status, c.Conflict = Failed, true
		host, port := resolverTarget(data)
		c.Detail = fmt.Sprintf("%s sends .%s to %s:%d", path, tld, host, port)
		c.Fix = fmt.Sprintf("sudo rm %s, then dotlane install", path)
		if host == "127.0.0.1" || host == "::1" {
			if procs := listeners(ctx, m.Runner, "udp", port); len(procs) > 0 {
				t := identify(procs[0], port, m.UID, m.Home)
				c.Detail += ", answered by " + t.Name
				c.Fix = t.Fix + "\n" + c.Fix
			} else {
				c.Detail += ", where nothing answers (left behind by an uninstalled tool)"
			}
		}
	}
	return c
}

func (m *Mac) lookupCheck(ctx context.Context, tld string) Check {
	host := "dotlane-check." + tld
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := m.Lookup(lookupCtx, host)
	if err == nil && containsLoopback(addrs) {
		return Check{Name: "Lookup ." + tld, Status: Pass, Detail: host + " → this Mac"}
	}
	return Check{Name: "Lookup ." + tld, Status: Failed, Detail: host + " does not resolve to this Mac",
		Fix: "dotlane doctor --fix; macOS sometimes needs a few seconds after a change"}
}

// preflight runs before install changes anything. It stops on conflicts with other tools, except
// a taken default DNS port: then it picks a free one and saves it in the config file.
func (m *Mac) preflight(ctx context.Context) error {
	pid := 0
	if st, err := m.Daemon(ctx); err == nil {
		pid = st.PID
	}
	var conflicts []Check
	for _, c := range []Check{
		m.portCheck(ctx, "HTTPS port", "tcp", m.Cfg.HTTPSPort, pid),
		m.portCheck(ctx, "HTTP port", "tcp", m.Cfg.HTTPPort, pid),
	} {
		if c.Conflict {
			conflicts = append(conflicts, c)
		}
	}

	// A taken default DNS port moves to a free one, but is only saved once nothing else stops
	// the install: a refused install changes nothing.
	dnsPort, dnsNote := m.Cfg.DNSPort, ""
	if c := m.portCheck(ctx, "DNS port", "udp", m.Cfg.DNSPort, pid); c.Conflict {
		if port := m.freeDNSPort(ctx); m.Cfg.DNSPortSet || port == 0 {
			conflicts = append(conflicts, c)
		} else {
			dnsPort, dnsNote = port, c.Detail
		}
	}

	// Resolver files are judged against the port that will be used.
	configured := m.Cfg.DNSPort
	m.Cfg.DNSPort = dnsPort
	for _, tld := range m.Cfg.TLDs {
		if c := m.resolverCheck(ctx, tld); c.Conflict {
			conflicts = append(conflicts, c)
		}
	}

	if len(conflicts) == 0 {
		if dnsNote != "" {
			why := "chosen by dotlane install: " + strings.TrimPrefix(dnsNote, fmt.Sprintf(":%d ", configured))
			if err := config.SetDNSPort(m.ConfigPath, dnsPort, why); err != nil {
				return err
			}
			m.UI.Warn("DNS port", fmt.Sprintf("%s; using :%d (saved in %s)", dnsNote, dnsPort, m.UI.Path(m.ConfigPath)))
			m.Cfg.DNSPortSet = true
		}
		return nil
	}
	m.Cfg.DNSPort = configured
	for _, c := range conflicts {
		m.UI.Fail(c.Name, c.Detail)
		m.UI.Hint(c.Fix)
	}
	return fmt.Errorf("%w; nothing was changed\nfix the above, then run dotlane install again", errInTheWay)
}

// errInTheWay is preflight's refusal.
var errInTheWay = errors.New("other tools are in the way")

// freeDNSPort returns the first port after the configured one, up to 10099, that is free for
// both UDP and TCP; 0 if there is none.
func (m *Mac) freeDNSPort(ctx context.Context) int {
	for port := m.Cfg.DNSPort + 1; port <= 10099; port++ {
		if port == m.Cfg.HTTPPort || port == m.Cfg.HTTPSPort {
			continue
		}
		if len(listeners(ctx, m.Runner, "udp", port)) > 0 || len(listeners(ctx, m.Runner, "tcp", port)) > 0 {
			continue
		}
		if m.Bindable("udp", port) == nil && m.Bindable("tcp", port) == nil {
			return port
		}
	}
	return 0
}

// bindable tries to listen where the daemon will (proxy.ListenAddrs for HTTP and HTTPS, loopback
// for DNS), which catches ports held by other users' processes that lsof cannot see.
func bindable(proto string, port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if proto == "udp" {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return err
		}
		return pc.Close()
	}
	ln, err := net.Listen("tcp", proxy.ListenAddrs(port)[0])
	if err != nil {
		return err
	}
	return ln.Close()
}

// privateFiles are the state that must be this user's alone, with the widest mode each may have:
// the state directory (sites, socket, CA), the control socket and the CA's root key.
func (m *Mac) privateFiles() []struct {
	path string
	max  os.FileMode
} {
	return []struct {
		path string
		max  os.FileMode
	}{
		{m.Cfg.StateDir, 0o700},
		{m.Cfg.SocketPath(), 0o600},
		{filepath.Join(proxy.CADir(m.Cfg), "root.key"), 0o600},
	}
}

// permissionsCheck reports state that other users could read or that belongs to someone else.
func (m *Mac) permissionsCheck() Check {
	c := Check{Name: "Private files"}
	var problems []string
	for _, f := range m.privateFiles() {
		info, err := os.Stat(f.path)
		if err != nil {
			continue // not created yet
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != m.UID {
			problems = append(problems, fmt.Sprintf("%s belongs to another user", m.UI.Path(f.path)))
			continue
		}
		if perm := info.Mode().Perm(); perm&^f.max != 0 {
			problems = append(problems, fmt.Sprintf("%s is mode %o, want %o", m.UI.Path(f.path), perm, f.max))
			c.Repairable = true
		}
	}
	if len(problems) == 0 {
		c.Status, c.Detail = Pass, "state, socket and CA key are yours alone · "+m.UI.Path(m.Cfg.StateDir)
		return c
	}
	c.Status, c.Detail = Failed, strings.Join(problems, "; ")
	if c.Repairable {
		c.Fix = "dotlane doctor --fix (tightens the modes)"
	} else {
		c.Fix = "sudo chown -R $USER " + m.UI.Path(m.Cfg.StateDir)
	}
	return c
}

// tightenPermissions removes other users' access to the private files.
func (m *Mac) tightenPermissions() error {
	for _, f := range m.privateFiles() {
		info, err := os.Stat(f.path)
		if err != nil || info.Mode().Perm()&^f.max == 0 {
			continue
		}
		if err := os.Chmod(f.path, info.Mode().Perm()&f.max); err != nil {
			return err
		}
		m.UI.OK("Private files", fmt.Sprintf("%s tightened to %o", m.UI.Path(f.path), info.Mode().Perm()&f.max))
	}
	return nil
}
