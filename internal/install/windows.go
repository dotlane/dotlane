//go:build windows

package install

import (
	"context"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotlane/dotlane/internal/api"
	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/proxy"
	"github.com/dotlane/dotlane/internal/ui"
)

// Windows installs Dotlane on Windows: a scheduled task that starts the daemon at logon, the CA
// in the machine's Root store, and one name resolution policy (NRPT) rule per domain ending.
//
// Everything that needs administrator rights is collected and run in one elevated PowerShell, so
// Windows asks for permission once (see WinRunner.Elevated).
type Windows struct {
	Cfg        *config.Config
	ConfigPath string // passed to the service, absolute
	Exe        string // this dotlane binary, absolute; install copies it to ServiceDir
	// ServiceDir holds the copy of the binary the login service runs, so rebuilding or upgrading
	// the binary on the PATH does not change what is running until the next refresh.
	ServiceDir string
	// LogDir holds the daemon's log. A task that runs as S4U has no console, so the task appends
	// the daemon's output to the log itself (see taskSpec.commandLine).
	LogDir  string
	UserSID string // the account the service runs as
	UI      *ui.Printer
	Runner  WinRunner
	// Daemon asks the running daemon for its status; an error means it is not answering.
	Daemon func(ctx context.Context) (api.Status, error)
	// Bindable reports whether 127.0.0.1:port could be bound ("tcp" or "udp").
	Bindable func(proto string, port int) error
	// Lookup resolves a name the way other programs on this PC do; used to verify DNS.
	Lookup func(ctx context.Context, host string) ([]string, error)
	// PrivateDir limits a folder to the current user and SYSTEM; Owner checks it.
	PrivateDir func(dir string) error
	Owner      func(dir string) (bool, error)
	// Firefox reports whether Firefox is installed.
	Firefox func() bool
	// Kill ends a process by PID (the old daemon); nil means the real thing. Tests replace it.
	Kill func(pid int) error
	// Pause waits between polls; tests replace it.
	Pause func(time.Duration)
	// Timeout is how long to wait for the daemon to answer after a start.
	Timeout time.Duration
}

// errNeedsAdmin means Windows would not let this account do something to the login service
// without administrator rights.
var errNeedsAdmin = errors.New("Windows asks for administrator rights")

func (w *Windows) summary() string { return summary(w.Cfg) }

func (w *Windows) pause(d time.Duration) {
	if w.Pause != nil {
		w.Pause(d)
		return
	}
	time.Sleep(d)
}

// serviceExe is the binary the login service runs: a copy of Exe, one per service name, so a
// development install and a real one keep their own.
func (w *Windows) serviceExe() string { return filepath.Join(w.ServiceDir, w.Cfg.ServiceName+".exe") }

// oldExe is where the previous copy waits while it still runs: Windows lets a running program be
// renamed, but not replaced or deleted.
func (w *Windows) oldExe() string { return w.serviceExe() + ".old" }

func (w *Windows) logPath() string { return filepath.Join(w.LogDir, w.Cfg.ServiceName+".log") }

func (w *Windows) rootCert() string { return filepath.Join(proxy.CADir(w.Cfg), "root.crt") }

// taskName is the task as schtasks names it.
func (w *Windows) taskName() string { return taskFolder + w.Cfg.ServiceName }

func (w *Windows) spec() taskSpec {
	return taskSpec{Service: w.Cfg.ServiceName, SID: w.UserSID, Exe: w.serviceExe(), Config: w.ConfigPath, Log: w.logPath()}
}

// copyBinary puts this binary at serviceExe (see copyBinary). A running copy is renamed aside
// first, and removed once the service runs the new one (see removeOld).
func (w *Windows) copyBinary() (bool, error) {
	if sameFile(w.Exe, w.serviceExe()) {
		return false, nil
	}
	renamed := false
	if _, err := os.Stat(w.serviceExe()); err == nil {
		os.Remove(w.oldExe()) // from an earlier upgrade; fails while that copy still runs
		if err := os.Rename(w.serviceExe(), w.oldExe()); err != nil {
			return false, err
		}
		renamed = true
	}
	changed, err := copyBinary(w.Exe, w.serviceExe())
	if err != nil && renamed {
		os.Rename(w.oldExe(), w.serviceExe())
	}
	return changed, err
}

// kill ends the process pid, which a moment ago answered as the daemon.
func (w *Windows) kill(pid int) error {
	if w.Kill != nil {
		return w.Kill(pid)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer p.Release()
	return p.Kill()
}

// waitReplaced waits until a daemon other than oldPID answers (0: no old daemon, nothing to wait
// for). The old one answering means the restart did not replace it, and a new one could not bind its
// ports.
func (w *Windows) waitReplaced(ctx context.Context, oldPID int) error {
	if oldPID == 0 {
		return nil
	}
	for i := 0; i < 75; i++ {
		if st, err := w.Daemon(ctx); err == nil && st.PID != oldPID {
			return nil
		}
		w.pause(200 * time.Millisecond)
	}
	return fmt.Errorf("the old daemon (pid %d) is still running, so the new build is not: end it (Task Manager, or Stop-Process -Id %d -Force), then run dotlane install again\nsee its log: %s",
		oldPID, oldPID, w.UI.Path(w.logPath()))
}

// removeOld deletes the previous copy of the binary, if the program that ran it has exited.
func (w *Windows) removeOld() { os.Remove(w.oldExe()) }

// taskState reports whether the login service's task exists, and was made from the current spec.
func (w *Windows) taskState(ctx context.Context) (registered, current bool) {
	out, err := w.Runner.Run(ctx, system32("schtasks.exe"), "/Query", "/TN", w.taskName(), "/XML")
	if err != nil {
		return false, false
	}
	return true, strings.Contains(string(out), w.spec().marker())
}

// Refresh implements Installer: install's login-service step alone (copy the binary, replace the
// task if needed, restart when stale). The CA and DNS steps are left out. Restarting the task is
// tried without administrator rights; when Windows refuses, Refresh asks, saying so.
func (w *Windows) Refresh(ctx context.Context) error {
	w.UI.Title("Refreshing Dotlane", w.summary())
	registered, _ := w.taskState(ctx)
	if _, err := os.Stat(w.serviceExe()); !registered && err != nil {
		w.UI.Skip("Login service", "not installed; nothing to refresh (set it up with: dotlane install)")
		return nil
	}
	if temporaryBuild(w.Exe) {
		return fmt.Errorf("%s is a temporary build; refresh from a built binary", w.Exe)
	}
	if err := w.finishService(ctx, true); err != nil {
		return err
	}
	if err := w.tightenPermissions(); err != nil {
		return err
	}
	w.UI.Done("Dotlane is up to date.", "")
	return nil
}

// finishService brings the login service to this binary. When that needs administrator rights and
// elevate is false, it fails instead of asking.
func (w *Windows) finishService(ctx context.Context, elevate bool) error {
	step, err := w.serviceStep(ctx)
	if err != nil {
		return err
	}
	if step != nil {
		if !elevate {
			return fmt.Errorf("%s needs administrator rights, which an automatic update does not ask for", strings.ToLower(step.Label))
		}
		if err := w.runSteps(ctx, []adminStep{*step}); err != nil {
			return err
		}
	}
	return w.running(ctx)
}

// Upgrade implements Installer.
func (w *Windows) Upgrade(ctx context.Context, version string, out io.Writer) (bool, error) {
	if _, err := os.Stat(w.serviceExe()); err != nil {
		return false, nil
	}
	st, err := w.Daemon(ctx)
	if err != nil || !Newer(version, st.Version) {
		return false, nil
	}
	if registered, _ := w.taskState(ctx); !registered {
		return false, nil
	}
	w.UI = ui.New(out)
	w.UI.Title("Updating the Dotlane service", fmt.Sprintf("it runs %s, this is %s", st.Version, version))
	if err := w.finishService(ctx, false); err != nil {
		return false, err
	}
	return true, w.tightenPermissions()
}

// Install sets everything up; already-done steps are reported and skipped.
func (w *Windows) Install(ctx context.Context) error {
	w.UI.Title("Installing Dotlane", w.summary())

	if temporaryBuild(w.Exe) {
		return fmt.Errorf("%s is a temporary build; build dotlane first (go build -o bin\\dotlane.exe .\\cmd\\dotlane) and install from it", w.Exe)
	}
	if w.Cfg.DNSPort != 53 {
		return fmt.Errorf("dns_port is %d, but Windows can send a domain ending only to a DNS server on port 53 "+
			"(its name resolution policy takes an address, not a port)\nremove dns_port from %s, then run dotlane install again; nothing was changed",
			w.Cfg.DNSPort, w.ConfigPath)
	}
	if _, err := w.spec().xml(); err != nil {
		return err
	}
	if err := w.preflight(ctx); err != nil {
		return err
	}

	var steps []adminStep
	svc, err := w.serviceStep(ctx)
	if err != nil {
		return err
	}
	if svc != nil {
		steps = append(steps, *svc)
	}
	ca, err := w.trustStep(ctx)
	if err != nil {
		return err
	}
	if ca != nil {
		steps = append(steps, *ca)
	}
	dns, err := w.dnsSteps(ctx)
	if err != nil {
		return err
	}
	steps = append(steps, dns...)
	if err := w.runSteps(ctx, steps); err != nil {
		return err
	}
	if err := w.running(ctx); err != nil {
		return err
	}
	if err := w.tightenPermissions(); err != nil {
		return err
	}
	w.verifyDNS(ctx)

	w.UI.Done("Dotlane is installed.", "Add a site: dotlane add <name> <port>")
	return nil
}

// running waits for the daemon to answer, and reports where its log is.
func (w *Windows) running(ctx context.Context) error {
	timeout := w.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	if err := waitForDaemon(ctx, w.Daemon, timeout); err != nil {
		return fmt.Errorf("the service did not start: %v\nsee its log: %s", err, w.UI.Path(w.logPath()))
	}
	w.removeOld()
	w.UI.OK("Running", "log: "+w.UI.Path(w.logPath()))
	return nil
}

// runSteps shows each step with its commands, then runs them together as one elevated script.
func (w *Windows) runSteps(ctx context.Context, steps []adminStep) error {
	if len(steps) == 0 {
		return nil
	}
	if !w.Runner.IsElevated() && !w.Runner.CanElevate() {
		var labels []string
		for _, s := range steps {
			labels = append(labels, strings.ToLower(s.Label))
		}
		return fmt.Errorf("this needs administrator rights (%s), and your account is not an administrator. "+
			"Dotlane installs per user, so your own account has to be one: Windows would ask for another account's "+
			"password, and the service and its files would end up with that account, not yours.\n"+
			"Ask an administrator to add you to the Administrators group, then run this again; nothing was changed", strings.Join(labels, ", "))
	}
	for _, s := range steps {
		if w.Runner.IsElevated() {
			w.UI.Line("\n  › %s", s.Label)
			w.UI.Line("    %s", s.Why)
		} else {
			w.UI.Ask(s.Label, s.Why)
		}
		for _, c := range s.Shown {
			w.UI.Command(c)
		}
	}
	if w.Runner.IsElevated() {
		w.UI.Line("\n  Dotlane already has administrator rights; running the steps above.")
	} else {
		w.UI.Line("\n  The steps above run together in one PowerShell as administrator: Windows asks you once.")
	}
	if err := w.Runner.Elevated(ctx, stepBody(steps)); err != nil {
		return fmt.Errorf("administrator steps: %w", err)
	}
	for _, s := range steps {
		if s.Done != nil {
			if err := s.Done(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// serviceStep makes the login service current as far as an unprivileged user can: it copies the
// binary and, when the task is current, restarts it if the daemon is stale or not answering. It
// returns the administrator step that is still needed (the task must be registered or replaced,
// or Windows refused to restart it), or nil.
func (w *Windows) serviceStep(ctx context.Context) (*adminStep, error) {
	if err := os.MkdirAll(w.LogDir, 0o755); err != nil {
		return nil, err
	}
	if _, err := w.copyBinary(); err != nil {
		return nil, fmt.Errorf("copying the binary to %s: %w", w.UI.Path(w.serviceExe()), err)
	}
	xml, err := w.spec().xml()
	if err != nil {
		return nil, err
	}
	name := w.Cfg.ServiceName
	registered, current := w.taskState(ctx)
	if !registered || !current {
		why := "Starts Dotlane when you sign in, restarts it if it stops, and starts it now. It runs as you, with\n" +
			"    no console window, and without storing your password. Windows lets only an administrator add a task\n" +
			"    like that; dotlane uninstall removes it. Your account may then start and stop the task, so updates need\n" +
			"    no permission prompt; a service still running from an earlier task is ended, to be replaced."
		pid := 0
		if st, err := w.Daemon(ctx); err == nil {
			pid = st.PID
		}
		return &adminStep{
			Label: "Login service", Why: why,
			Shown: registerShown(name, w.UserSID, pid),
			Body:  registerBody(name, xml, w.UserSID, pid),
			Done: func(ctx context.Context) error {
				if err := w.waitReplaced(ctx, pid); err != nil {
					return err
				}
				w.UI.OK("Login service", "installed · task "+w.taskName())
				return nil
			},
		}, nil
	}

	w.UI.OK("Login service", "already installed")
	pid, why := 0, ""
	if st, err := w.Daemon(ctx); err != nil {
		why = "it was not answering"
	} else if s := stale(st, w.serviceExe(), w.ConfigPath); s != "" {
		pid, why = st.PID, "to load the "+s
	}
	if why == "" {
		return nil, nil
	}
	err = w.restartTask(ctx, pid)
	switch {
	case errors.Is(err, errNeedsAdmin):
		return &adminStep{
			Label: "Login service",
			Why: "Restarts the Dotlane service (" + why + "). This account may not stop and start its own task; Windows\n" +
				"    asks for administrator rights for that.",
			Shown: append(stopShown(name, pid), startShown(name)),
			Body:  restartBody(name, pid),
			Done: func(ctx context.Context) error {
				if err := w.waitReplaced(ctx, pid); err != nil {
					return err
				}
				w.UI.OK("Login service", "restarted ("+why+")")
				return nil
			},
		}, nil
	case err != nil:
		return nil, err
	}
	w.UI.OK("Login service", "restarted ("+why+")")
	return nil, nil
}

// restartTask ends the task and runs it again with schtasks, without administrator rights. oldPID
// is the daemon being replaced, or 0. It returns errNeedsAdmin (wrapped) if Windows refused, which
// shows as the old daemon still answering, or as /Run failing.
func (w *Windows) restartTask(ctx context.Context, oldPID int) error {
	schtasks := system32("schtasks.exe")
	w.Runner.Run(ctx, schtasks, "/End", "/TN", w.taskName()) // fails when the task is not running
	if oldPID != 0 {
		// /End ends the task's cmd.exe, not the daemon it started: that keeps its ports and socket,
		// and a new daemon could not bind them. End it by PID.
		killErr := w.kill(oldPID)
		stopped := false
		for i := 0; i < 50 && !stopped; i++ {
			st, err := w.Daemon(ctx)
			if stopped = err != nil || st.PID != oldPID; !stopped {
				w.pause(200 * time.Millisecond)
			}
		}
		if !stopped {
			return fmt.Errorf("%w: the old daemon (pid %d) kept running (ending it: %v)", errNeedsAdmin, oldPID, killErr)
		}
	}
	timeout := w.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	// A task that is still ending ignores a start, so ask again a few times.
	for i := 0; i < 3; i++ {
		if out, err := w.Runner.Run(ctx, schtasks, "/Run", "/TN", w.taskName()); err != nil {
			return fmt.Errorf("%w: schtasks /Run: %v: %s", errNeedsAdmin, err, strings.TrimSpace(string(out)))
		}
		if waitForDaemon(ctx, w.Daemon, timeout) == nil {
			return nil
		}
	}
	return fmt.Errorf("the service did not start\nsee its log: %s", w.UI.Path(w.logPath()))
}

// thumbprint is the SHA-1 of the certificate at path, as Windows shows it (upper case hex), and
// its common name.
func thumbprint(path string) (hash, name string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", "", fmt.Errorf("%s is not a PEM certificate", path)
	}
	sum := sha1.Sum(block.Bytes)
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		name = cert.Subject.CommonName
	}
	return strings.ToUpper(hex.EncodeToString(sum[:])), name, nil
}

// caTrusted reports whether the machine's Root store holds the certificate with this thumbprint.
func (w *Windows) caTrusted(ctx context.Context, hash string) bool {
	_, err := w.Runner.Run(ctx, powershellExe(), queryArgs(trustedScript(hash))...)
	return err == nil
}

// trustStep returns the step that trusts the CA, or nil when it is trusted already.
func (w *Windows) trustStep(ctx context.Context) (*adminStep, error) {
	hash, _, err := thumbprint(w.rootCert())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil && w.caTrusted(ctx, hash) {
		w.UI.OK("Certificate authority", "already trusted")
		return nil, nil
	}
	return &adminStep{
		Label: "Certificate authority",
		Why: "Trusts Dotlane's local certificate authority, so browsers and tools accept its HTTPS\n" +
			"    certificates. Added to the computer's Trusted Root store; dotlane uninstall removes it.",
		Shown: []string{"certutil -addstore -f Root " + winQuote(w.rootCert())},
		Body:  trustBody(w.rootCert(), w.logPath()),
		Done: func(ctx context.Context) error {
			hash, _, err := thumbprint(w.rootCert())
			if err != nil {
				return err
			}
			if !w.caTrusted(ctx, hash) {
				return errors.New("the certificate authority was added but Windows still does not trust it")
			}
			w.UI.OK("Certificate authority", "trusted")
			return nil
		},
	}, nil
}

// rules lists the machine's NRPT rules.
func (w *Windows) rules(ctx context.Context) ([]nrptRule, error) {
	out, err := w.Runner.Run(ctx, powershellExe(), queryArgs(nrptScript)...)
	if err != nil {
		return nil, fmt.Errorf("listing the DNS rules: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return parseRules(out)
}

// dnsSteps returns one step per domain ending that is not routed to Dotlane yet.
func (w *Windows) dnsSteps(ctx context.Context) ([]adminStep, error) {
	rules, err := w.rules(ctx)
	if err != nil {
		rules = nil // unknown: add the rules, which replaces any of Dotlane's own first
	}
	var steps []adminStep
	for _, tld := range w.Cfg.TLDs {
		tld := tld
		label := "DNS for ." + tld
		if state, _ := ruleFor(rules, w.Cfg.ServiceName, tld); state == ruleOK {
			w.UI.OK(label, "already routed · 127.0.0.1")
			continue
		}
		steps = append(steps, adminStep{
			Label: label,
			Why:   fmt.Sprintf("Sends lookups for *.%s, and only those, to Dotlane's DNS on 127.0.0.1.", tld),
			Shown: addRuleShown(w.Cfg.ServiceName, tld),
			Body:  addRuleBody(w.Cfg.ServiceName, tld),
			Done: func(ctx context.Context) error {
				rules, err := w.rules(ctx)
				if err != nil {
					return err
				}
				if state, _ := ruleFor(rules, w.Cfg.ServiceName, tld); state != ruleOK {
					return fmt.Errorf("the DNS rule for .%s was added but Windows does not list it", tld)
				}
				w.UI.OK(label, "routed to 127.0.0.1")
				return nil
			},
		})
	}
	return steps, nil
}

// verifyDNS checks the whole path through the Windows resolver. A failure is reported, not fatal:
// the DNS client can take a moment to pick up a new rule.
func (w *Windows) verifyDNS(ctx context.Context) {
	for _, tld := range w.Cfg.TLDs {
		host := "dotlane-check." + tld
		var addrs []string
		var err error
		for i := 0; i < 20; i++ {
			lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
			addrs, err = w.Lookup(lookupCtx, host)
			cancel()
			if err == nil && containsLoopback(addrs) {
				w.UI.OK("DNS check", host+" → this PC")
				break
			}
			w.pause(250 * time.Millisecond)
		}
		if err != nil || !containsLoopback(addrs) {
			w.UI.Warn("DNS check", fmt.Sprintf("%s does not resolve yet; Windows can take a few seconds.\n"+
				"    Check with: Resolve-DnsName %s", host, host))
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
func (w *Windows) Uninstall(ctx context.Context, purge bool) error {
	w.UI.Title("Uninstalling Dotlane", w.summary())
	name := w.Cfg.ServiceName

	var steps []adminStep
	registered, _ := w.taskState(ctx)
	if registered {
		pid := 0
		if st, err := w.Daemon(ctx); err == nil {
			pid = st.PID
		}
		steps = append(steps, adminStep{
			Label: "Login service",
			Why:   "Stops the Dotlane service (and ends the daemon it started) and removes its scheduled task, and Dotlane's\n    folder in Task Scheduler when no other task is in it.",
			Shown: unregisterShown(name, pid),
			Body:  unregisterBody(name, pid),
			Done:  func(context.Context) error { return w.removeBinary("stopped and removed") },
		})
	} else if err := w.removeBinary("removed (its task was gone)"); err != nil {
		return err
	}

	ca, err := w.untrustStep(ctx)
	if err != nil {
		return err
	}
	if ca != nil {
		steps = append(steps, *ca)
	}
	dns, err := w.removeRulesStep(ctx)
	if err != nil {
		return err
	}
	if dns != nil {
		steps = append(steps, *dns)
	}
	if err := w.runSteps(ctx, steps); err != nil {
		return err
	}

	if purge {
		if err := removeAll(w.Cfg.StateDir); err != nil {
			return err
		}
		w.UI.OK("Sites and CA keys", "deleted · "+w.UI.Path(w.Cfg.StateDir))
		if err := removeAll(w.logPath()); err != nil {
			return err
		}
		os.Remove(w.LogDir)               // the logs folder, if no other service uses it
		os.Remove(filepath.Dir(w.LogDir)) // and Dotlane's, if nothing else is in it
		w.UI.OK("Log", "deleted · "+w.UI.Path(w.logPath()))
	} else {
		w.UI.Skip("Sites and CA keys", "kept · "+w.UI.Path(w.Cfg.StateDir))
		w.UI.Skip("Log", "kept · "+w.UI.Path(w.logPath()))
	}

	next := ""
	if !purge {
		next = w.UI.Dim("To delete the kept files too: dotlane uninstall --purge")
	}
	w.UI.Done("Dotlane is uninstalled.", next)
	return nil
}

// removeBinary deletes the service's copy of the binary and the one waiting aside, retrying a
// moment while the stopped daemon lets go of them. done is reported when something was removed.
func (w *Windows) removeBinary(done string) error {
	removed := false
	for _, path := range []string{w.serviceExe(), w.oldExe()} {
		err := w.removeFile(path)
		if err == nil {
			removed = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing %s: %w (is the service still running? stop it in Task Manager)", w.UI.Path(path), err)
		}
	}
	if removed {
		os.Remove(w.ServiceDir) // only if no other service's copy is there
		w.UI.OK("Login service", done)
	} else {
		w.UI.Skip("Login service", "not installed")
	}
	return nil
}

// removeFile deletes path, trying again for a few seconds if the file is in use.
func (w *Windows) removeFile(path string) error {
	var err error
	for i := 0; i < 25; i++ {
		if err = os.Remove(path); err == nil || errors.Is(err, os.ErrNotExist) {
			return err
		}
		w.pause(200 * time.Millisecond)
	}
	return err
}

// removeAll deletes path and everything in it, trying again for a few seconds while a stopped
// process lets go of its files.
func removeAll(path string) error {
	var err error
	for i := 0; i < 25; i++ {
		if err = os.RemoveAll(path); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}

// untrustStep returns the step that removes the CA from the Root store, or nil.
func (w *Windows) untrustStep(ctx context.Context) (*adminStep, error) {
	hash, _, err := thumbprint(w.rootCert())
	if errors.Is(err, os.ErrNotExist) {
		w.UI.Skip("Certificate authority", "none created")
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !w.caTrusted(ctx, hash) {
		w.UI.Skip("Certificate authority", "not in the Trusted Root store")
		return nil, nil
	}
	return &adminStep{
		Label: "Certificate authority",
		Why:   "Removes Dotlane's certificate authority from the computer's Trusted Root store, so its certificates\n    are no longer trusted.",
		Shown: []string{"certutil -delstore Root " + hash},
		Body:  untrustBody(hash),
		Done: func(ctx context.Context) error {
			if w.caTrusted(ctx, hash) {
				return fmt.Errorf("the certificate authority is still in the Trusted Root store (SHA-1 %s); remove it in certlm.msc", hash)
			}
			w.UI.OK("Certificate authority", "removed from the Trusted Root store")
			return nil
		},
	}, nil
}

// removeRulesStep returns the step that removes Dotlane's DNS rules, or nil when there are none.
// Another tool's rule for the same ending is left alone.
func (w *Windows) removeRulesStep(ctx context.Context) (*adminStep, error) {
	rules, err := w.rules(ctx)
	if err != nil {
		return nil, err
	}
	mine := 0
	for _, r := range rules {
		if r.Comment == nrptComment(w.Cfg.ServiceName) {
			mine++
		}
	}
	for _, tld := range w.Cfg.TLDs {
		if state, other := ruleFor(rules, w.Cfg.ServiceName, tld); state == ruleForeign {
			w.UI.Skip("DNS for ."+tld, fmt.Sprintf("rule %s is not Dotlane's; left in place", other.Name))
		} else if state == ruleMissing {
			w.UI.Skip("DNS for ."+tld, "not set")
		}
	}
	if mine == 0 {
		return nil, nil
	}
	return &adminStep{
		Label: "DNS rules",
		Why:   "Stops sending *." + strings.Join(w.Cfg.TLDs, ", *.") + " lookups to Dotlane.",
		Shown: []string{
			fmt.Sprintf(`Get-DnsClientNrptRule | Where-Object { $_.Comment -eq "%s" } | Remove-DnsClientNrptRule -Force`, nrptComment(w.Cfg.ServiceName)),
			"Clear-DnsClientCache",
		},
		Body: removeRulesBody(w.Cfg.ServiceName),
		Done: func(ctx context.Context) error {
			left, err := w.rules(ctx)
			if err != nil {
				return err
			}
			for _, r := range left {
				if r.Comment == nrptComment(w.Cfg.ServiceName) {
					return fmt.Errorf("the DNS rule %s is still there; remove it with Remove-DnsClientNrptRule", r.Name)
				}
			}
			w.UI.OK("DNS rules", "removed")
			return nil
		},
	}, nil
}

// Migrate implements Installer.
func (w *Windows) Migrate(ctx context.Context) error {
	return errors.New("nothing to migrate on Windows: the Bash prototype ran on macOS only")
}
