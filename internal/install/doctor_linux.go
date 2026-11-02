//go:build linux

package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/proxy"
)

// Doctor implements Installer: the installation's state, and anything in its way.
func (m *Linux) Doctor(ctx context.Context) []Check {
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
			Fix: "dotlane doctor --fix (restarts it); its log: " + m.journalCmd(), Repairable: true})
	} else {
		add(Check{Name: "Daemon", Status: Skipped, Detail: "not installed yet"})
	}

	add(m.portsCheck())
	add(m.portCheck(ctx, "HTTPS port", "tcp", m.Cfg.HTTPSPort, pid))
	add(m.portCheck(ctx, "HTTP port", "tcp", m.Cfg.HTTPPort, pid))
	dns := m.portCheck(ctx, "DNS port", "udp", m.Cfg.DNSPort, pid)
	if dns.Conflict && !m.Cfg.DNSPortSet && !installed {
		// Not in the way: install moves to a free port when the default one is taken.
		dns.Status, dns.Conflict = Warning, false
		dns.Fix = "nothing to do: dotlane install will pick a free port"
	}
	add(dns)
	if ca := m.caCheck(); installed || ca.Status == Pass {
		add(ca)
	} else {
		add(Check{Name: "Certificate authority", Status: Skipped, Detail: "created and trusted by dotlane install"})
	}
	add(m.browsersCheck(ctx, installed))
	add(m.permissionsCheck())

	resolved := m.resolvedCheck(ctx)
	add(resolved)
	dnsOK := resolved.Status == Pass
	for _, tld := range m.Cfg.TLDs {
		c := m.dnsCheck(ctx, tld)
		if c.Status == Pass && pid == 0 {
			// The right setting, but someone else may be answering on that port.
			for _, p := range m.listeners(ctx, "udp", m.Cfg.DNSPort) {
				if !m.ours(p, pid) {
					c.Status = Warning
					c.Detail += ", answered by " + m.identify(p, m.Cfg.DNSPort).Name + " for now"
					break
				}
			}
		}
		dnsOK = dnsOK && c.Status == Pass
		add(c)
	}
	if pid != 0 && dnsOK {
		for _, tld := range m.Cfg.TLDs {
			add(m.lookupCheck(ctx, tld))
		}
	}
	return checks
}

// ours reports whether p is this service's daemon: by PID, or by unit when it is running but not
// answering. A PID of 0 (a socket ss cannot attribute) is never ours.
func (m *Linux) ours(p Process, daemonPID int) bool {
	return (p.PID != 0 && p.PID == daemonPID) || p.Label == "user/"+m.unitName()
}

func (m *Linux) serviceCheck(ctx context.Context) Check {
	c := Check{Name: "Login service", Fix: "dotlane install", Repairable: true}
	have, err := os.ReadFile(m.unitPath())
	if errors.Is(err, os.ErrNotExist) {
		c.Status, c.Detail = Failed, "not installed"
		return c
	}
	if mgrErr := m.userManager(ctx); mgrErr != nil {
		c.Status, c.Detail, c.Repairable = Failed, mgrErr.Error(), false
		c.Fix = strings.ReplaceAll(userManagerHelp, "\n    ", "; ")
		return c
	}
	_, enabledErr := m.userctl(ctx, "is-enabled", m.unitName())
	switch {
	case !bytes.Equal(have, m.unit()):
		c.Status, c.Detail = Warning, "installed for another dotlane binary or config file"
		c.Fix = "dotlane install (updates it to this one)"
	case enabledErr != nil:
		c.Status, c.Detail = Failed, "installed but not enabled"
	case !sameFile(m.Exe, m.serviceExe()):
		c.Status, c.Detail = Warning, "runs an older copy of this binary"
		c.Fix = "dotlane install (updates the copy and restarts the service)"
	default:
		c.Status, c.Detail, c.Fix, c.Repairable = Pass, m.Cfg.ServiceName+" · starts at login", "", false
	}
	return c
}

// portsCheck is whether a normal user may listen on the lowest port Dotlane uses.
func (m *Linux) portsCheck() Check {
	c := Check{Name: "Low ports"}
	low := m.lowestPort()
	if m.portsAllowed() {
		start, _ := m.portStart()
		c.Status, c.Detail = Pass, fmt.Sprintf("port %d is open to normal users (%s=%d)", low, portStartKey, start)
		return c
	}
	start, _ := m.portStart()
	c.Status, c.Detail = Failed, fmt.Sprintf("%s=%d: normal users cannot listen on port %d", portStartKey, start, low)
	c.Fix, c.Repairable = "dotlane install (asks for your password)", true
	return c
}

// portCheck reports who holds port. daemonPID is the answering daemon's, or 0.
func (m *Linux) portCheck(ctx context.Context, name, proto string, port, daemonPID int) Check {
	c := Check{Name: name}
	label := fmt.Sprintf(":%d", port)
	var others, fixes []string
	ours := false
	for _, p := range m.listeners(ctx, proto, port) {
		if m.ours(p, daemonPID) {
			ours = true
			continue
		}
		t := m.identify(p, port)
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
		c.Fix = "see its log: " + m.journalCmd()
	default:
		if err := m.Bindable(proto, port); errors.Is(err, syscall.EACCES) {
			// Not in the way: install lowers the limit (see Low ports).
			c.Status, c.Detail = Warning, label+" · free, once install has opened low ports"
			c.Fix, c.Repairable = "dotlane install", true
		} else if err != nil {
			c.Status, c.Conflict = Failed, true
			c.Detail = label + " is in use by a process of another user"
			c.Fix = fmt.Sprintf("find it with: sudo ss -lntup 'sport = :%d'", port)
		} else {
			c.Status, c.Detail = Pass, label+" · free"
		}
	}
	return c
}

func (m *Linux) caCheck() Check {
	c := Check{Name: "Certificate authority", Fix: "dotlane install (asks for your password)", Repairable: true}
	if _, err := os.Stat(m.rootCert()); err != nil {
		c.Status, c.Detail = Failed, "not created yet (the daemon creates it when it starts)"
	} else if !m.caTrusted() {
		c.Status, c.Detail = Failed, "not trusted by the system"
	} else {
		c.Status, c.Detail, c.Fix, c.Repairable = Pass, "trusted in the system's store", "", false
	}
	return c
}

// browsersCheck is Firefox's and Chromium's own lists. Only a warning: curl and Go do not need them.
func (m *Linux) browsersCheck(ctx context.Context, installed bool) Check {
	c := Check{Name: "Browsers"}
	if _, err := os.Stat(m.rootCert()); err != nil || !installed {
		c.Status, c.Detail = Skipped, "set up by dotlane install"
		return c
	}
	if !m.has("certutil") {
		pkg := "libnss3-tools (Debian, Ubuntu), nss-tools (Fedora), nss (Arch)"
		if s, err := m.store(); err == nil {
			pkg = s.NSSPackage
		}
		c.Status, c.Detail = Warning, "certutil is not installed, so Firefox and Chromium may not trust Dotlane"
		c.Fix = "install the " + pkg + " package, then dotlane install"
		return c
	}
	var missing []string
	dirs := m.nssDirs(true)
	for _, dir := range dirs {
		if !m.nssHolds(ctx, dir) {
			missing = append(missing, m.UI.Path(dir))
		}
	}
	if len(missing) > 0 {
		c.Status, c.Detail = Warning, "not trusted in "+strings.Join(missing, ", ")
		c.Fix, c.Repairable = "dotlane install", true
		return c
	}
	c.Status, c.Detail = Pass, fmt.Sprintf("trusted in %d Firefox and Chromium databases", len(dirs))
	return c
}

var systemdVersion = regexp.MustCompile(`systemd (\d+)`)

// resolvedCheck is whether systemd-resolved can route the domain endings: running, with the stub
// in /etc/resolv.conf, and new enough for a DNS server with a port (247).
func (m *Linux) resolvedCheck(ctx context.Context) Check {
	c := Check{Name: "System DNS", Status: Failed, Conflict: true}
	manual := "make systemd-resolved the system's resolver:\n" +
		"  sudo systemctl enable --now systemd-resolved\n" +
		"  sudo ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf"
	switch {
	case !m.has("resolvectl"):
		c.Detail = "systemd-resolved is not installed"
		c.Fix = "install it (apt install systemd-resolved, dnf install systemd-resolved), then:\n" + manual
		return c
	case !m.has("ip"):
		c.Detail = "ip is not installed"
		c.Fix = "install the iproute2 package (iproute on Fedora)"
		return c
	}
	if _, err := m.Runner.Run(ctx, m.tool("systemctl"), "is-active", "--quiet", "systemd-resolved"); err != nil {
		c.Detail, c.Fix = "systemd-resolved is not running", manual
		return c
	}
	resolv, _ := os.ReadFile(m.ResolvConf)
	stub := false
	for _, l := range strings.Split(string(resolv), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "nameserver" {
			stub = f[1] == "127.0.0.53"
			break
		}
	}
	if !stub {
		c.Detail = m.ResolvConf + " does not point at systemd-resolved's stub (127.0.0.53), so programs skip it"
		c.Fix = manual
		return c
	}
	if out, err := m.Runner.Run(ctx, m.tool("resolvectl"), "--version"); err == nil {
		if v := systemdVersion.FindSubmatch(out); v != nil {
			if n, _ := strconv.Atoi(string(v[1])); n < 247 {
				c.Detail = fmt.Sprintf("systemd %d is too old: a DNS server with a port needs 247", n)
				c.Fix = "upgrade systemd, or give Dotlane port 53 in " + m.UI.Path(m.ConfigPath) + " (needs the stub off)"
				return c
			}
		}
	}
	c.Status, c.Conflict, c.Detail = Pass, false, "systemd-resolved · "+m.ResolvConf+" uses its stub"
	return c
}

// foreignRoute names the link or file that already sends tld somewhere else, or "".
func (m *Linux) foreignRoute(ctx context.Context, tld string) string {
	out, err := m.Runner.Run(ctx, m.tool("resolvectl"), "domain")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		who, domains, ok := strings.Cut(line, ": ")
		if !ok || strings.Contains(who, "("+m.dnsLink()+")") {
			continue
		}
		if containsWord(domains, "~"+tld) || containsWord(domains, tld) {
			return strings.TrimSpace(who)
		}
	}
	return ""
}

func (m *Linux) dnsCheck(ctx context.Context, tld string) Check {
	c := Check{Name: "DNS for ." + tld}
	have, err := os.ReadFile(m.dnsUnitPath())
	switch {
	case errors.Is(err, os.ErrNotExist):
		c.Status, c.Detail, c.Fix, c.Repairable = Failed, m.dnsUnitPath()+" missing", "dotlane install (asks for your password)", true
	case err != nil:
		c.Status, c.Detail = Failed, err.Error()
	case func() bool { who := m.foreignRoute(ctx, tld); c.Detail = who; return who != "" }():
		c.Status, c.Conflict = Failed, true
		c.Detail = fmt.Sprintf("systemd-resolved already sends .%s to %s", tld, c.Detail)
		c.Fix = fmt.Sprintf("remove that setting (see: resolvectl status), then dotlane install")
	case !bytes.Equal(have, m.dnsUnit()):
		c.Status, c.Detail, c.Repairable = Failed, m.dnsUnitPath()+" is for another port or domain list", true
		c.Fix = "dotlane install (asks for your password)"
	case !m.dnsEnabled(ctx) || !m.dnsActive(ctx) || !m.routed(ctx):
		c.Status, c.Detail, c.Repairable = Failed, m.dnsUnitName()+" is written but not routing", true
		c.Fix = "dotlane install (asks for your password); log: journalctl -u " + m.dnsUnitName()
	default:
		c.Status, c.Detail = Pass, fmt.Sprintf("%s → 127.0.0.1:%d (link %s)", m.UI.Path(m.dnsUnitPath()), m.Cfg.DNSPort, m.dnsLink())
	}
	return c
}

func (m *Linux) lookupCheck(ctx context.Context, tld string) Check {
	host := "dotlane-check." + tld
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := m.Lookup(lookupCtx, host)
	if err == nil && loopbackIn(addrs) {
		return Check{Name: "Lookup ." + tld, Status: Pass, Detail: host + " → this machine"}
	}
	return Check{Name: "Lookup ." + tld, Status: Failed, Detail: host + " does not resolve to this machine",
		Fix: "dotlane doctor --fix; check with: resolvectl query " + host}
}

// errConflicts is preflight's refusal.
var errConflicts = errors.New("other tools are in the way")

// preflight runs before install changes anything. It stops on conflicts with other tools, except
// a taken default DNS port: then it picks a free one and saves it in the config file.
func (m *Linux) preflight(ctx context.Context) error {
	var conflicts []Check
	if c := m.resolvedCheck(ctx); c.Status != Pass {
		conflicts = append(conflicts, c)
	}
	if err := m.userManager(ctx); err != nil {
		conflicts = append(conflicts, Check{Name: "Login service", Detail: err.Error(),
			Fix: strings.ReplaceAll(userManagerHelp, "\n    ", "; ")})
	}
	pid := 0
	if st, err := m.Daemon(ctx); err == nil {
		pid = st.PID
	}
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
	for _, tld := range m.Cfg.TLDs {
		if who := m.foreignRoute(ctx, tld); who != "" {
			conflicts = append(conflicts, Check{Name: "DNS for ." + tld, Conflict: true,
				Detail: fmt.Sprintf("systemd-resolved already sends .%s to %s", tld, who),
				Fix:    "remove that setting (see: resolvectl status), or pick another domain ending in " + m.UI.Path(m.ConfigPath)})
		}
	}

	if len(conflicts) == 0 {
		if dnsNote != "" {
			why := "chosen by dotlane install: " + strings.TrimPrefix(dnsNote, fmt.Sprintf(":%d ", m.Cfg.DNSPort))
			if err := config.SetDNSPort(m.ConfigPath, dnsPort, why); err != nil {
				return err
			}
			m.UI.Warn("DNS port", fmt.Sprintf("%s; using :%d (saved in %s)", dnsNote, dnsPort, m.UI.Path(m.ConfigPath)))
			m.Cfg.DNSPort, m.Cfg.DNSPortSet = dnsPort, true
		}
		return nil
	}
	for _, c := range conflicts {
		m.UI.Fail(c.Name, c.Detail)
		m.UI.Hint(c.Fix)
	}
	return fmt.Errorf("%w; nothing was changed\nfix the above, then run dotlane install again", errConflicts)
}

// freeDNSPort returns the first port after the configured one, up to 10099, that is free for
// both UDP and TCP; 0 if there is none.
func (m *Linux) freeDNSPort(ctx context.Context) int {
	for port := m.Cfg.DNSPort + 1; port <= 10099; port++ {
		if port == m.Cfg.HTTPPort || port == m.Cfg.HTTPSPort {
			continue
		}
		if len(m.listeners(ctx, "udp", port)) > 0 || len(m.listeners(ctx, "tcp", port)) > 0 {
			continue
		}
		if m.Bindable("udp", port) == nil && m.Bindable("tcp", port) == nil {
			return port
		}
	}
	return 0
}

// privateFiles are the state that must be this user's alone, with the widest mode each may have:
// the state directory (sites, socket, CA), the control socket and the CA's root key.
func (m *Linux) privateFiles() []struct {
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
func (m *Linux) permissionsCheck() Check {
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
func (m *Linux) tightenPermissions() error {
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
