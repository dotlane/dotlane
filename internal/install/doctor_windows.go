//go:build windows

package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

// wsaeacces is WSAEACCES, which Windows answers to a bind on a port inside an excluded range
// (reserved for Hyper-V, WinNAT or the like) even for an administrator.
const wsaeacces = syscall.Errno(10013)

// Doctor implements Installer: the installation's state, and anything in its way.
func (w *Windows) Doctor(ctx context.Context) []Check {
	var checks []Check
	add := func(c Check) { checks = append(checks, c) }

	source := "defaults (no config file)"
	if w.Cfg.Path != "" {
		source = w.UI.Path(w.Cfg.Path)
	}
	add(Check{Name: "Config", Status: Pass, Detail: fmt.Sprintf("%s · %s · DNS :%d · HTTPS :%d",
		source, w.endings(), w.Cfg.DNSPort, w.Cfg.HTTPSPort)})

	service := w.serviceCheck(ctx)
	add(service)
	installed := service.Detail != "not installed"
	pid := 0
	if st, err := w.Daemon(ctx); err == nil {
		pid = st.PID
		if s := stale(st, w.serviceExe(), w.ConfigPath); s != "" {
			add(Check{Name: "Daemon", Status: Warning, Detail: fmt.Sprintf("pid %d started before the %s", st.PID, s),
				Fix: "dotlane doctor --fix (restarts it)", Repairable: installed})
		} else {
			add(Check{Name: "Daemon", Status: Pass, Detail: fmt.Sprintf("running · pid %d · %s", st.PID, st.Version)})
		}
	} else if installed {
		add(Check{Name: "Daemon", Status: Failed, Detail: "not answering on its socket",
			Fix: "dotlane doctor --fix (restarts it); its log: " + w.UI.Path(w.logPath()), Repairable: true})
	} else {
		add(Check{Name: "Daemon", Status: Skipped, Detail: "not installed yet"})
	}

	found, _ := listeners(ctx, w.Runner, w.ports()...)
	add(w.portCheck("HTTPS port", found, pid, portSpec{"tcp", w.Cfg.HTTPSPort}))
	add(w.portCheck("HTTP port", found, pid, portSpec{"tcp", w.Cfg.HTTPPort}))
	add(w.dnsPortCheck(found, pid))
	add(w.caCheck(ctx, installed))
	add(w.permissionsCheck())

	rules, rulesErr := w.rules(ctx)
	rulesOK := rulesErr == nil
	if rulesErr != nil {
		add(Check{Name: "DNS rules", Status: Failed, Detail: rulesErr.Error()})
	}
	for _, tld := range w.Cfg.TLDs {
		if rulesErr != nil {
			break
		}
		c := w.ruleCheck(rules, tld)
		rulesOK = rulesOK && c.Status == Pass
		add(c)
	}
	if pid != 0 && rulesOK {
		for _, tld := range w.Cfg.TLDs {
			add(w.lookupCheck(ctx, tld))
		}
	}

	if w.Firefox() {
		add(Check{Name: "Firefox", Status: Warning,
			Detail: "keeps its own list of trusted authorities, apart from Windows'",
			Fix: "recent versions also read Windows' list (about:config, security.enterprise_roots.enabled: true, the default on Windows),\n" +
				"so this is probably fine; if Firefox warns about a Dotlane address, check that setting"})
	}
	return checks
}

func (w *Windows) endings() string {
	tlds := make([]string, len(w.Cfg.TLDs))
	for i, t := range w.Cfg.TLDs {
		tlds[i] = "." + t
	}
	return strings.Join(tlds, " ")
}

// ports are the ports Dotlane needs, for one scan of who holds them.
func (w *Windows) ports() []portSpec {
	return []portSpec{{"tcp", w.Cfg.HTTPSPort}, {"tcp", w.Cfg.HTTPPort}, {"udp", w.Cfg.DNSPort}, {"tcp", w.Cfg.DNSPort}}
}

func (w *Windows) serviceCheck(ctx context.Context) Check {
	c := Check{Name: "Login service", Fix: "dotlane install", Repairable: true}
	registered, current := w.taskState(ctx)
	_, exeErr := os.Stat(w.serviceExe())
	switch {
	case !registered && exeErr != nil:
		c.Status, c.Detail = Failed, "not installed"
	case !registered:
		c.Status, c.Detail = Failed, "installed but its scheduled task is missing"
	case !current:
		c.Status, c.Detail = Warning, "installed for another dotlane binary or config file"
		c.Fix = "dotlane install (updates it to this one)"
	case !sameFile(w.Exe, w.serviceExe()):
		c.Status, c.Detail = Warning, "runs an older copy of this binary"
		c.Fix = "dotlane install (updates the copy and restarts the service)"
	default:
		c.Status, c.Detail, c.Fix, c.Repairable = Pass, w.Cfg.ServiceName+" · starts when you sign in", "", false
	}
	return c
}

// dnsPortCheck is the DNS port's portCheck, which on Windows must be 53 for both UDP and TCP.
func (w *Windows) dnsPortCheck(found []winListener, daemonPID int) Check {
	if w.Cfg.DNSPort != 53 {
		return Check{Name: "DNS port", Status: Failed, Conflict: true,
			Detail: fmt.Sprintf(":%d is not 53; Windows sends a domain ending to a DNS server by address only", w.Cfg.DNSPort),
			Fix:    "remove dns_port from " + w.UI.Path(w.ConfigPath) + ", then run dotlane install"}
	}
	return w.portCheck("DNS port", found, daemonPID, portSpec{"udp", 53}, portSpec{"tcp", 53})
}

// portCheck reports who holds a port (all specs name the same port). daemonPID is the answering
// daemon's, or 0.
func (w *Windows) portCheck(name string, found []winListener, daemonPID int, specs ...portSpec) Check {
	c := Check{Name: name}
	port := specs[0].port
	label := fmt.Sprintf(":%d", port)
	var others, fixes []string
	seen := map[string]bool{}
	ours := false
	for _, s := range specs {
		for _, l := range on(found, s.proto, s.port) {
			// Ours by PID, or by the file it runs when the daemon is running but not answering.
			if (daemonPID != 0 && l.PID == daemonPID) || strings.EqualFold(l.Path, w.serviceExe()) {
				ours = true
				continue
			}
			t := identify(l, w.UI.Path(w.ConfigPath))
			if !seen[t.Name] {
				seen[t.Name] = true
				others = append(others, t.Name)
				fixes = append(fixes, t.Fix)
			}
		}
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
		c.Fix = "see its log: " + w.UI.Path(w.logPath())
	default:
		for _, s := range specs {
			err := w.Bindable(s.proto, s.port)
			if err == nil {
				continue
			}
			c.Status, c.Conflict = Failed, true
			if errors.Is(err, wsaeacces) {
				c.Detail = label + " is reserved by Windows (an excluded port range, usually Hyper-V or WinNAT)"
				c.Fix = fmt.Sprintf("see the ranges: netsh int ipv4 show excludedportrange protocol=%s\n"+
					"the usual cure, in a PowerShell run as administrator: net stop winnat, start Dotlane, net start winnat", s.proto)
			} else {
				c.Detail = label + " is in use by a process that this user cannot see"
				c.Fix = fmt.Sprintf("find it in a PowerShell run as administrator: Get-Process -Id (Get-NetTCPConnection -LocalPort %d).OwningProcess\n"+
					"and for http.sys: netsh http show servicestate", port)
			}
			return c
		}
		c.Status, c.Detail = Pass, label+" · free"
	}
	return c
}

func (w *Windows) caCheck(ctx context.Context, installed bool) Check {
	c := Check{Name: "Certificate authority", Fix: "dotlane install (asks for administrator rights)", Repairable: true}
	hash, _, err := thumbprint(w.rootCert())
	switch {
	case errors.Is(err, os.ErrNotExist):
		if !installed {
			return Check{Name: c.Name, Status: Skipped, Detail: "created and trusted by dotlane install"}
		}
		c.Status, c.Detail = Failed, "not created yet (the daemon creates it when it starts)"
	case err != nil:
		c.Status, c.Detail = Failed, err.Error()
	case !w.caTrusted(ctx, hash):
		c.Status, c.Detail = Failed, "not in the Trusted Root store"
	default:
		c.Status, c.Detail, c.Fix, c.Repairable = Pass, "trusted in the Trusted Root store", "", false
	}
	return c
}

// ruleCheck judges the DNS rule for one domain ending.
func (w *Windows) ruleCheck(rules []nrptRule, tld string) Check {
	c := Check{Name: "DNS for ." + tld}
	state, r := ruleFor(rules, w.Cfg.ServiceName, tld)
	switch state {
	case ruleMissing:
		c.Status, c.Detail, c.Fix, c.Repairable = Failed, "no DNS rule sends it to Dotlane", "dotlane install (asks for administrator rights)", true
	case ruleOK:
		c.Status, c.Detail = Pass, "DNS rule → 127.0.0.1"
	case ruleWrong:
		c.Status, c.Detail, c.Repairable = Failed, fmt.Sprintf("Dotlane's DNS rule sends it to %s", r.NameServers), true
		c.Fix = "dotlane install (replaces the rule)"
	case ruleForeign:
		c.Status, c.Conflict = Failed, true
		c.Detail = fmt.Sprintf("DNS rule %s sends it to %s", r.Name, r.NameServers)
		if r.Comment != "" {
			c.Detail += fmt.Sprintf(" (%q)", r.Comment)
		}
		c.Fix = fmt.Sprintf("in a PowerShell run as administrator: Remove-DnsClientNrptRule -Name %s -Force, then dotlane install", psQuote(r.Name))
	}
	return c
}

func (w *Windows) lookupCheck(ctx context.Context, tld string) Check {
	host := "dotlane-check." + tld
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := w.Lookup(lookupCtx, host)
	if err == nil && containsLoopback(addrs) {
		return Check{Name: "Lookup ." + tld, Status: Pass, Detail: host + " → this PC"}
	}
	return Check{Name: "Lookup ." + tld, Status: Failed, Detail: host + " does not resolve to this PC",
		Fix: "dotlane doctor --fix; Windows sometimes needs a few seconds after a change (ipconfig /flushdns)"}
}

// permissionsCheck reports state that other accounts could read.
func (w *Windows) permissionsCheck() Check {
	c := Check{Name: "Private files"}
	if _, err := os.Stat(w.Cfg.StateDir); err != nil { // not created yet
		c.Status, c.Detail = Pass, "state, socket and CA key are yours alone · "+w.UI.Path(w.Cfg.StateDir)
		return c
	}
	ok, err := w.Owner(w.Cfg.StateDir)
	switch {
	case err != nil:
		c.Status, c.Detail = Failed, fmt.Sprintf("cannot read the access rights of %s: %v", w.UI.Path(w.Cfg.StateDir), err)
	case !ok:
		c.Status, c.Detail, c.Repairable = Failed, w.UI.Path(w.Cfg.StateDir)+" can be read by other accounts", true
		c.Fix = "dotlane doctor --fix (limits it to you and SYSTEM)"
	default:
		c.Status, c.Detail = Pass, "state, socket and CA key are yours alone · "+w.UI.Path(w.Cfg.StateDir)
	}
	return c
}

// tightenPermissions limits the state directory to the current user and SYSTEM, if it is not.
func (w *Windows) tightenPermissions() error {
	if _, err := os.Stat(w.Cfg.StateDir); err != nil {
		return nil
	}
	if ok, err := w.Owner(w.Cfg.StateDir); err == nil && ok {
		return nil
	}
	if err := w.PrivateDir(w.Cfg.StateDir); err != nil {
		return err
	}
	w.UI.OK("Private files", w.UI.Path(w.Cfg.StateDir)+" limited to you and SYSTEM")
	return nil
}

// preflight runs before install changes anything. It stops on conflicts with other tools: Windows
// takes no other DNS port, so a taken port 53 cannot be worked around.
func (w *Windows) preflight(ctx context.Context) error {
	pid := 0
	if st, err := w.Daemon(ctx); err == nil {
		pid = st.PID
	}
	found, _ := listeners(ctx, w.Runner, w.ports()...)
	var conflicts []Check
	for _, c := range []Check{
		w.portCheck("HTTPS port", found, pid, portSpec{"tcp", w.Cfg.HTTPSPort}),
		w.portCheck("HTTP port", found, pid, portSpec{"tcp", w.Cfg.HTTPPort}),
		w.dnsPortCheck(found, pid),
	} {
		if c.Conflict {
			conflicts = append(conflicts, c)
		}
	}
	if rules, err := w.rules(ctx); err == nil {
		for _, tld := range w.Cfg.TLDs {
			if c := w.ruleCheck(rules, tld); c.Conflict {
				conflicts = append(conflicts, c)
			}
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	for _, c := range conflicts {
		w.UI.Fail(c.Name, c.Detail)
		w.UI.Hint(c.Fix)
	}
	return fmt.Errorf("%w; nothing was changed\nfix the above, then run dotlane install again", errInTheWay)
}

// errInTheWay is preflight's refusal.
var errInTheWay = errors.New("other tools are in the way")
