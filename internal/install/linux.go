//go:build linux

package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dotlane/dotlane/internal/api"
	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/proxy"
	"github.com/dotlane/dotlane/internal/ui"
)

const (
	// portStartKey is the sysctl that decides the lowest port a normal user may listen on.
	portStartKey = "net.ipv4.ip_unprivileged_port_start"
	// defaultPortStart is the kernel's own value for it.
	defaultPortStart = 1024
	// dnsLinkAddress is the address of Dotlane's DNS link, in the IPv4 link-local range: with no
	// address, or with a loopback one, systemd-resolved gives the link no DNS scope at all.
	dnsLinkAddress = "169.254.53.53/32"
	// marker starts every file install writes, so uninstall can tell its own from a user's.
	marker = "# Written by dotlane install; dotlane uninstall removes it."
)

// Linux installs Dotlane on Linux with systemd: a user unit as the login service, a system unit
// that sends the domain endings to Dotlane's DNS through systemd-resolved, a sysctl file for ports
// 80 and 443, and the certificate authority in the system store and in the browsers' NSS databases.
type Linux struct {
	Cfg        *config.Config
	ConfigPath string // passed to the service, absolute
	Exe        string // this dotlane binary, absolute; install copies it to ServiceDir
	// ServiceDir holds the copy of the binary the login service runs, in a stable place that an
	// upgrade of the package, or a rebuild, does not touch under a running daemon.
	ServiceDir string
	Home       string
	UID        int
	// UnitDir holds the login service's unit ($XDG_CONFIG_HOME/systemd/user).
	UnitDir string
	// SystemUnitDir is /etc/systemd/system, for the DNS unit. The paths below are the system's
	// own; tests point them elsewhere.
	SystemUnitDir string
	SysctlFile    string // /etc/sysctl.d/50-dotlane-<service name>.conf: one per service, so uninstalling one leaves the other's
	PortStartFile string // /proc/sys/net/ipv4/ip_unprivileged_port_start
	ResolvConf    string // /etc/resolv.conf
	ProcDir       string // /proc
	// Stores are the system trust stores this distribution family may have, in order of
	// preference; Bundles are the files the extracted trust lists end up in.
	Stores  []TrustStore
	Bundles []string
	// FirefoxGlobs are where Firefox keeps profiles, relative to Home.
	FirefoxGlobs []string
	UI           *ui.Printer
	Runner       Runner
	// LookPath finds a tool the user runs as themselves (systemctl --user, certutil, ss); it also
	// looks in the sbin folders a normal user's PATH can lack. Never use it for what root runs.
	LookPath func(name string) (string, error)
	// LookSystem finds a tool that runs as root, through sudo or from a system unit: only in the
	// system folders, and only a file root owns and nobody else can write (see lookSystemTool).
	LookSystem func(name string) (string, error)
	// Daemon asks the running daemon for its status; an error means it is not answering.
	Daemon func(ctx context.Context) (api.Status, error)
	// Bindable reports whether 127.0.0.1:port could be bound ("tcp" or "udp").
	Bindable func(proto string, port int) error
	// Lookup resolves a name the way other programs on this machine do; used to verify DNS.
	Lookup func(ctx context.Context, host string) ([]string, error)
}

// New returns the installer for this machine.
func New(cfg *config.Config, configPath string, daemon func(context.Context) (api.Status, error)) (Installer, error) {
	return NewLinux(cfg, configPath, daemon)
}

// NewLinux fills Linux with the real system's values.
func NewLinux(cfg *config.Config, configPath string, daemon func(context.Context) (api.Status, error)) (*Linux, error) {
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
	return &Linux{
		Cfg: cfg, ConfigPath: configPath, Exe: exe, Home: home, UID: os.Getuid(),
		ServiceDir:    filepath.Join(xdgDir("XDG_DATA_HOME", filepath.Join(home, ".local", "share")), "dotlane", "bin"),
		UnitDir:       filepath.Join(xdgDir("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "systemd", "user"),
		SystemUnitDir: "/etc/systemd/system",
		SysctlFile:    "/etc/sysctl.d/50-dotlane-" + cfg.ServiceName + ".conf",
		PortStartFile: "/proc/sys/net/ipv4/ip_unprivileged_port_start",
		ResolvConf:    "/etc/resolv.conf",
		ProcDir:       "/proc",
		Stores:        defaultStores(),
		Bundles:       defaultBundles(),
		FirefoxGlobs: []string{
			".mozilla/firefox/*",
			"snap/firefox/common/.mozilla/firefox/*",
			".var/app/org.mozilla.firefox/.mozilla/firefox/*",
		},
		UI:         ui.New(os.Stdout),
		Runner:     sudoRunner{},
		LookPath:   lookTool,
		LookSystem: lookSystemTool,
		Daemon:     daemon,
		Bindable:   bindable,
		Lookup:     net.DefaultResolver.LookupHost,
	}, nil
}

// xdgDir is the folder an XDG variable names, or def when it is unset or not absolute.
func xdgDir(env, def string) string {
	if dir := os.Getenv(env); filepath.IsAbs(dir) {
		return dir
	}
	return def
}

// lookTool finds name on the PATH, then in the system folders a normal user's PATH may lack
// (sysctl, ip and update-ca-certificates live in sbin on Debian).
func lookTool(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, dir := range []string{"/usr/local/sbin", "/usr/sbin", "/sbin", "/usr/local/bin", "/usr/bin", "/bin"} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: not found", name)
}

// systemToolDirs are the only folders root-run tools come from. /usr/local is left out on
// purpose: it is where a user-installed program is most likely to shadow a system one.
var systemToolDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"}

// lookSystemTool finds a tool that will run as root (through sudo, or in a system unit's ExecStart,
// where it runs at every boot): never through PATH, which a user can put their own folder first
// in. The file must be a regular file, or a symlink that resolves into the same folders, owned by
// root and not writable by group or others.
func lookSystemTool(name string) (string, error) { return lookToolIn(systemToolDirs, 0, name) }

// lookToolIn is lookSystemTool over dirs, with owner as the user id that must own the file.
func lookToolIn(dirs []string, owner uint32, name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("%q is not a tool name", name)
	}
	allowed := map[string]bool{}
	for _, dir := range dirs {
		allowed[dir] = true
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			allowed[real] = true
		}
	}
	var problem error
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue // a dangling link
		}
		info, err := os.Stat(real)
		if err != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		switch {
		case !allowed[filepath.Dir(real)] && !lockedDown(real, owner):
			// Ubuntu's Rust coreutils are links to /usr/lib/cargo/bin/coreutils: fine, because no
			// one but root can change anything on the way there.
			problem = fmt.Errorf("%s resolves to %s, outside the system folders", path, real)
		case !info.Mode().IsRegular():
			problem = fmt.Errorf("%s is not a regular file", real)
		case info.Mode().Perm()&0o111 == 0:
			continue // not a program
		case !ok || st.Uid != owner:
			problem = fmt.Errorf("%s is not owned by root", real)
		case info.Mode().Perm()&0o022 != 0:
			problem = fmt.Errorf("%s can be written by users other than root", real)
		default:
			return path, nil
		}
	}
	if problem != nil {
		return "", fmt.Errorf("refusing to run %s as root: %v", name, problem)
	}
	return "", fmt.Errorf("%s: not found in %s", name, strings.Join(dirs, ", "))
}

// lockedDown reports whether path and every folder above it belong to owner and cannot be written
// by group or others, so no one else can swap what a link to it leads to.
func lockedDown(path string, owner uint32) bool {
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Stat(p)
		if err != nil {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != owner || info.Mode().Perm()&0o022 != 0 {
			return false
		}
		if p == "/" || p == filepath.Dir(p) {
			return true
		}
	}
}

// sudoRunner is ExecRunner for Linux: sudo is /usr/bin/sudo, or wherever else it is installed,
// and root needs none.
type sudoRunner struct{ ExecRunner }

// Sudo implements Runner.
func (r sudoRunner) Sudo(ctx context.Context, stdin []byte, name string, args ...string) error {
	if os.Getuid() != 0 {
		if _, err := os.Stat("/usr/bin/sudo"); err == nil {
			return r.ExecRunner.Sudo(ctx, stdin, name, args...)
		}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if os.Getuid() != 0 {
		sudo, err := lookSystemTool("sudo")
		if err != nil {
			return errors.New("sudo is not installed")
		}
		cmd = exec.CommandContext(ctx, sudo, append([]string{name}, args...)...)
	}
	if stdin != nil {
		cmd.Stdin = strings.NewReader(string(stdin))
		cmd.Stdout = io.Discard // tee echoes its input
	} else {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// tool is the path of a tool, or its bare name when it is not installed (the command then fails
// with a message that names it).
func (m *Linux) tool(name string) string {
	if path, err := m.LookPath(name); err == nil {
		return path
	}
	return name
}

// sysTool is the path of a tool that runs as root, or an error that says why it may not.
func (m *Linux) sysTool(name string) (string, error) {
	path, err := m.LookSystem(name)
	if err != nil {
		return "", fmt.Errorf("%v (Dotlane runs only root-owned tools from the system folders as root)", err)
	}
	return path, nil
}

func (m *Linux) has(name string) bool {
	_, err := m.LookPath(name)
	return err == nil
}

// unitName is the login service's unit.
func (m *Linux) unitName() string { return m.Cfg.ServiceName + ".service" }

func (m *Linux) unitPath() string { return filepath.Join(m.UnitDir, m.unitName()) }

// journalCmd is how to read the daemon's log: the journal has it, there is no log file.
func (m *Linux) journalCmd() string { return "journalctl --user -u " + m.unitName() }

func (m *Linux) summary() string { return summary(m.Cfg) }

func (m *Linux) endings() string {
	tlds := make([]string, len(m.Cfg.TLDs))
	for i, t := range m.Cfg.TLDs {
		tlds[i] = "." + t
	}
	return strings.Join(tlds, " ")
}

// serviceExe is the binary the login service runs: a copy of Exe, one per service name, so a
// development install and a real one keep their own.
func (m *Linux) serviceExe() string { return filepath.Join(m.ServiceDir, m.Cfg.ServiceName) }

// copyBinary puts this binary at serviceExe (see copyBinary).
func (m *Linux) copyBinary() (bool, error) { return copyBinary(m.Exe, m.serviceExe()) }

func (m *Linux) rootCert() string { return filepath.Join(proxy.CADir(m.Cfg), "root.crt") }

// stale is what the running daemon is behind on (see stale).
func (m *Linux) stale(st api.Status) string { return stale(st, m.serviceExe(), m.ConfigPath) }

func (m *Linux) waitForDaemon(ctx context.Context, timeout time.Duration) error {
	return waitForDaemon(ctx, m.Daemon, timeout)
}

// userctl runs systemctl --user.
func (m *Linux) userctl(ctx context.Context, args ...string) ([]byte, error) {
	return m.Runner.Run(ctx, m.tool("systemctl"), append([]string{"--user"}, args...)...)
}

// sudo shows one privileged command, then runs it. name is the tool's bare name, found by sysTool.
// Callers announce the step with UI.Ask first.
func (m *Linux) sudo(ctx context.Context, stdin []byte, tool string, args ...string) error {
	name, err := m.sysTool(tool)
	if err != nil {
		return err
	}
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

// Migrate implements Installer.
func (m *Linux) Migrate(ctx context.Context) error {
	return errors.New("there is nothing to migrate on Linux: the Bash prototype only ever ran on macOS")
}

// Refresh implements Installer: install's login-service step alone (copy the binary, rewrite the
// unit if needed, restart when stale). The steps that need sudo are left out.
func (m *Linux) Refresh(ctx context.Context) error {
	m.UI.Title("Refreshing Dotlane", m.summary())
	if _, err := os.Stat(m.unitPath()); errors.Is(err, os.ErrNotExist) {
		m.UI.Skip("Login service", "not installed; nothing to refresh (set it up with: dotlane install)")
		return nil
	}
	if temporaryBuild(m.Exe) {
		return fmt.Errorf("%s is a temporary build; refresh from a built binary", m.Exe)
	}
	unlock, err := lockService(ctx, m.Cfg, serviceLockWait)
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.installService(ctx); err != nil {
		return err
	}
	if err := m.tightenPermissions(); err != nil {
		return err
	}
	m.UI.Done("Dotlane is up to date.", "")
	return nil
}

// Upgrade implements Installer.
func (m *Linux) Upgrade(ctx context.Context, version string, out io.Writer) (bool, error) {
	if _, err := os.Stat(m.unitPath()); err != nil {
		return false, nil
	}
	st, err := m.Daemon(ctx)
	if err != nil || !Newer(version, st.Version) {
		return false, nil
	}
	// Only a command that would restart the service waits for the lock; then it asks again, since
	// the command it waited for may have done it.
	unlock, err := lockService(ctx, m.Cfg, serviceLockWait)
	if err != nil {
		return false, err
	}
	defer unlock()
	if st, err = m.Daemon(ctx); err != nil || !Newer(version, st.Version) {
		return false, nil
	}
	m.UI = ui.New(out)
	m.UI.Title("Updating the Dotlane service", fmt.Sprintf("it runs %s, this is %s", st.Version, version))
	if err := m.installService(ctx); err != nil {
		return false, err
	}
	return true, m.tightenPermissions()
}

// Install sets everything up; already-done steps are reported and skipped.
func (m *Linux) Install(ctx context.Context) error {
	m.UI.Title("Installing Dotlane", m.summary())

	if temporaryBuild(m.Exe) {
		return fmt.Errorf("%s is a temporary build; build dotlane first (go build -o bin/dotlane ./cmd/dotlane) and install from it", m.Exe)
	}
	unlock, err := lockService(ctx, m.Cfg, serviceLockWait)
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.preflight(ctx); err != nil {
		return err
	}

	// The ports first: the daemon cannot bind 80 and 443 until they are allowed.
	if err := m.installPorts(ctx); err != nil {
		return err
	}
	if err := m.installService(ctx); err != nil {
		return err
	}
	if err := m.tightenPermissions(); err != nil {
		return err
	}
	if err := m.trustCA(ctx); err != nil {
		return err
	}
	if err := m.installDNS(ctx); err != nil {
		return err
	}
	m.verifyDNS(ctx)

	m.UI.Done("Dotlane is installed.", "Add a site: dotlane add <name> <port>")
	return nil
}

// userManager reports why systemctl --user cannot be used, or nil.
func (m *Linux) userManager(ctx context.Context) error {
	if out, err := m.userctl(ctx, "show-environment"); err != nil {
		return fmt.Errorf("the systemd user manager is not running (%v: %s)", err, bytes.TrimSpace(out))
	}
	return nil
}

// userManagerHelp says what to do when there is no user manager.
const userManagerHelp = "Dotlane's login service is a systemd user unit. Log in with a normal session (a desktop or\n" +
	"    ssh login), or run: sudo loginctl enable-linger $USER, then log in again"

func (m *Linux) installService(ctx context.Context) error {
	if err := m.userManager(ctx); err != nil {
		return fmt.Errorf("%v\n%s", err, userManagerHelp)
	}
	if err := os.MkdirAll(m.UnitDir, 0o755); err != nil {
		return err
	}
	if _, err := m.copyBinary(); err != nil {
		return fmt.Errorf("copying the binary to %s: %w", m.UI.Path(m.serviceExe()), err)
	}
	want := m.unit()
	have, _ := os.ReadFile(m.unitPath())
	_, enabledErr := m.userctl(ctx, "is-enabled", m.unitName())
	enabled := enabledErr == nil

	if bytes.Equal(have, want) && enabled {
		m.UI.OK("Login service", "already installed")
		why := ""
		if st, err := m.Daemon(ctx); err != nil {
			why = "it was not answering"
		} else if stale := m.stale(st); stale != "" {
			why = "to load the " + stale
		}
		if why != "" {
			if err := m.restart(ctx); err != nil {
				return err
			}
			m.UI.OK("Login service", "restarted ("+why+")")
		}
	} else {
		if err := os.WriteFile(m.unitPath(), want, 0o644); err != nil {
			return err
		}
		if out, err := m.userctl(ctx, "daemon-reload"); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %v: %s", err, bytes.TrimSpace(out))
		}
		_, activeErr := m.userctl(ctx, "is-active", m.unitName())
		if out, err := m.userctl(ctx, "enable", "--now", m.unitName()); err != nil {
			return fmt.Errorf("systemctl enable: %v: %s\nsee its log: %s", err, bytes.TrimSpace(out), m.journalCmd())
		}
		if activeErr == nil { // reinstall or upgrade: enable --now leaves a running copy alone
			if err := m.restart(ctx); err != nil {
				return err
			}
		}
		m.UI.OK("Login service", "installed · "+m.UI.Path(m.unitPath()))
	}

	if err := m.waitForDaemon(ctx, 15*time.Second); err != nil {
		return fmt.Errorf("the service did not start: %v\nsee its log: %s", err, m.journalCmd())
	}
	m.UI.OK("Running", "log: "+m.journalCmd())
	return nil
}

// unit is the systemd user unit: start at login, restart on failure.
// restart restarts the login service on purpose. reset-failed first flushes systemd's start
// counter, so restarts Dotlane asks for (a few dotlane tld changes in a row) do not use up the
// limit of five starts in ten seconds that stops a daemon that keeps crashing.
func (m *Linux) restart(ctx context.Context) error {
	m.userctl(ctx, "reset-failed", m.unitName()) // best effort: only the counter is at stake
	if out, err := m.userctl(ctx, "restart", m.unitName()); err != nil {
		return fmt.Errorf("systemctl restart: %v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (m *Linux) unit() []byte {
	return []byte(marker + `
[Unit]
Description=Dotlane: trusted HTTPS addresses for local dev servers (` + m.Cfg.ServiceName + `)

[Service]
ExecStart=` + strings.Join([]string{unitQuote(m.serviceExe()), "--config", unitQuote(m.ConfigPath), "serve"}, " ") + `
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`)
}

// unitQuote is s as one word of an ExecStart line: quoted when it holds spaces or quotes, with
// the characters systemd expands ("%" and "$") doubled.
func unitQuote(s string) string {
	s = strings.NewReplacer("%", "%%", "$", "$$").Replace(s)
	if !strings.ContainsAny(s, " \t'\"\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// lowestPort is the lowest port the daemon listens on.
func (m *Linux) lowestPort() int { return min(m.Cfg.HTTPPort, m.Cfg.HTTPSPort) }

// portStart is the kernel's lowest port for normal users.
func (m *Linux) portStart() (int, error) {
	data, err := os.ReadFile(m.PortStartFile)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// portsAllowed reports whether the daemon, as a normal user, may listen on its lowest port.
func (m *Linux) portsAllowed() bool {
	start, err := m.portStart()
	return err != nil || start <= m.lowestPort() // unreadable: assume the best, the bind will say
}

func (m *Linux) sysctlFile() []byte {
	return []byte(fmt.Sprintf("%s\n%s=%d\n", marker, portStartKey, m.lowestPort()))
}

// installPorts lets a normal user listen on the ports Dotlane serves (80 and 443 by default).
// The proxy binds loopback only on Linux, so lowering the limit does not expose anything; it is a
// machine-wide setting, though, which is why the step is explained. setcap on the binary would
// stay local, but is lost on every upgrade and cannot be done by the package managers.
func (m *Linux) installPorts(ctx context.Context) error {
	low := m.lowestPort()
	if start, err := m.portStart(); err == nil && start <= low {
		m.UI.OK("Low ports", fmt.Sprintf("port %d is already open to normal users (from %d)", low, start))
		return nil
	}
	m.UI.Ask("Low ports",
		fmt.Sprintf("Lets Dotlane, without root, listen on port %d and up (%s=%d), only on\n"+
			"    127.0.0.1 and ::1. This is a machine-wide setting; dotlane uninstall restores %d.",
			low, portStartKey, low, defaultPortStart))
	if err := m.sudo(ctx, m.sysctlFile(), "tee", m.SysctlFile); err != nil {
		return err
	}
	// -p loads this file alone; --system would also reapply every other sysctl file.
	if err := m.sudo(ctx, nil, "sysctl", "-p", m.SysctlFile); err != nil {
		return err
	}
	if !m.portsAllowed() {
		return fmt.Errorf("%s was written but %s is still not %d or lower", m.SysctlFile, portStartKey, low)
	}
	m.UI.OK("Low ports", m.SysctlFile+" written")
	return nil
}

// dnsLink is the name of the network link that carries the DNS setting: "dl-" and a hash of the
// service name, so two installs (a development one and a real one) do not share a link.
func (m *Linux) dnsLink() string {
	sum := sha256.Sum256([]byte(m.Cfg.ServiceName))
	return "dl-" + hex.EncodeToString(sum[:4])
}

func (m *Linux) dnsUnitName() string { return "dotlane-dns-" + m.Cfg.ServiceName + ".service" }

func (m *Linux) dnsUnitPath() string { return filepath.Join(m.SystemUnitDir, m.dnsUnitName()) }

// dnsUnit is the system unit that gives systemd-resolved the setting, at boot and whenever
// systemd-resolved restarts.
//
// Why not the obvious drop-in, /etc/systemd/resolved.conf.d/dotlane.conf with DNS=127.0.0.1:port
// and Domains=~tld? Measured on Ubuntu, Fedora and Arch (systemd 259 and 260): DNS= in
// resolved.conf is global, and the global scope is a default route. Every lookup on the machine
// (example.com included) went to Dotlane first, and where no other server answers, such as an
// Arch machine with no DNS on its links, lookups failed after the refusals. The setting has to
// live on a link, with DefaultRoute off. The loopback link is refused ("Link lo is loopback
// device"), so the unit creates a dummy link of its own. It needs an IPv4 link-local address:
// without one resolved gives the link no DNS scope, and with a loopback one too. networkd would
// do the same declaratively, but it is often not installed, or not enabled, next to
// NetworkManager. Nothing here needs it.
//
// The unit runs as root, at every boot, so its tools are the system's own (see lookSystemTool),
// and the unit cannot be made if one is missing or not trustworthy.
func (m *Linux) dnsUnit() ([]byte, error) {
	ip, err := m.sysTool("ip")
	if err != nil {
		return nil, err
	}
	rc, err := m.sysTool("resolvectl")
	if err != nil {
		return nil, err
	}
	link := m.dnsLink()
	// Sorted: the order of the endings means nothing to systemd-resolved, so choosing another
	// primary (dotlane tld use) leaves the unit as it is.
	sorted := slices.Sorted(slices.Values(m.Cfg.TLDs))
	tlds, shown := make([]string, len(sorted)), make([]string, len(sorted))
	for i, t := range sorted {
		tlds[i], shown[i] = "~"+t, "."+t
	}
	// StartLimitIntervalSec=0: Dotlane restarts the unit whenever the endings change, and a few
	// changes in a row (a script running dotlane tld) would otherwise hit systemd's limit of five
	// starts in ten seconds, made for services that crash.
	return []byte(marker + `
[Unit]
Description=Dotlane: send ` + strings.Join(shown, " ") + ` lookups to 127.0.0.1:` + strconv.Itoa(m.Cfg.DNSPort) + ` (` + m.Cfg.ServiceName + `)
After=systemd-resolved.service systemd-networkd.service
PartOf=systemd-resolved.service
StartLimitIntervalSec=0

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=-` + ip + ` link add ` + link + ` type dummy
ExecStart=` + ip + ` addr replace ` + dnsLinkAddress + ` dev ` + link + `
ExecStart=` + ip + ` link set ` + link + ` up
ExecStart=` + rc + ` dns ` + link + ` 127.0.0.1:` + strconv.Itoa(m.Cfg.DNSPort) + `
ExecStart=` + rc + ` domain ` + link + ` ` + strings.Join(tlds, " ") + `
ExecStart=` + rc + ` default-route ` + link + ` no
ExecStop=` + ip + ` link del ` + link + `

[Install]
WantedBy=multi-user.target systemd-resolved.service
`), nil
}

// routed reports whether systemd-resolved now sends every domain ending, and only those, to
// Dotlane's DNS on the link.
func (m *Linux) routed(ctx context.Context) bool {
	rc := m.tool("resolvectl")
	dns, err := m.Runner.Run(ctx, rc, "dns", m.dnsLink())
	if err != nil || !strings.Contains(string(dns), fmt.Sprintf("127.0.0.1:%d", m.Cfg.DNSPort)) {
		return false
	}
	domains, err := m.Runner.Run(ctx, rc, "domain", m.dnsLink())
	if err != nil {
		return false
	}
	for _, tld := range m.Cfg.TLDs {
		if !containsWord(string(domains), "~"+tld) {
			return false
		}
	}
	return m.defaultRouteOff(ctx)
}

// defaultRouteOff reports whether the link is not a default route: if it were, every lookup on
// the machine would go to Dotlane first.
func (m *Linux) defaultRouteOff(ctx context.Context) bool {
	out, err := m.Runner.Run(ctx, m.tool("resolvectl"), "default-route", m.dnsLink())
	return err == nil && strings.HasSuffix(strings.TrimSpace(string(out)), ": no")
}

// containsWord reports whether s has word between spaces or line ends.
func containsWord(s, word string) bool {
	for _, f := range strings.Fields(s) {
		if f == word {
			return true
		}
	}
	return false
}

func (m *Linux) dnsActive(ctx context.Context) bool {
	_, err := m.Runner.Run(ctx, m.tool("systemctl"), "is-active", m.dnsUnitName())
	return err == nil
}

func (m *Linux) dnsEnabled(ctx context.Context) bool {
	_, err := m.Runner.Run(ctx, m.tool("systemctl"), "is-enabled", m.dnsUnitName())
	return err == nil
}

func (m *Linux) installDNS(ctx context.Context) error {
	label := "DNS for " + m.endings()
	want, err := m.dnsUnit()
	if err != nil {
		return err
	}
	if have, err := os.ReadFile(m.dnsUnitPath()); err == nil && bytes.Equal(have, want) &&
		m.dnsEnabled(ctx) && m.dnsActive(ctx) && m.routed(ctx) {
		m.UI.OK(label, "already routed · "+m.dnsUnitPath())
		return nil
	}
	m.UI.Ask(label,
		fmt.Sprintf("Sends lookups for %s, and only those, to Dotlane's DNS on 127.0.0.1:%d, through\n"+
			"    systemd-resolved. A small network link (%s) carries the setting; other lookups are left alone.",
			m.wildcards(), m.Cfg.DNSPort, m.dnsLink()))
	if err := m.sudo(ctx, want, "tee", m.dnsUnitPath()); err != nil {
		return err
	}
	if err := m.sudo(ctx, nil, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := m.sudo(ctx, nil, "systemctl", "enable", m.dnsUnitName()); err != nil {
		return err
	}
	if err := m.sudo(ctx, nil, "systemctl", "restart", m.dnsUnitName()); err != nil {
		return fmt.Errorf("%w\nsee: journalctl -u %s", err, m.dnsUnitName())
	}
	// systemd-resolved learns of a new link, and networkd may configure one, a moment after the
	// unit's commands ran: if a setting did not hold, set it again.
	for i := 0; i < 5 && !m.routed(ctx); i++ {
		time.Sleep(200 * time.Millisecond)
		if !m.defaultRouteOff(ctx) {
			if err := m.sudo(ctx, nil, "resolvectl", "default-route", m.dnsLink(), "no"); err != nil {
				return err
			}
		}
	}
	if !m.defaultRouteOff(ctx) {
		return fmt.Errorf("systemd-resolved keeps %s as a default route, so every lookup would go to Dotlane first; see: resolvectl status %s", m.dnsLink(), m.dnsLink())
	}
	if !m.routed(ctx) {
		return fmt.Errorf("the DNS unit started but systemd-resolved does not route %s to Dotlane; see: resolvectl status %s", m.wildcards(), m.dnsLink())
	}
	m.UI.OK(label, m.dnsUnitPath()+" written")
	return nil
}

// Installed implements Installer.
func (m *Linux) Installed(ctx context.Context) bool {
	_, err := os.Stat(m.unitPath())
	return err == nil
}

// SyncRoutes implements Installer: the DNS unit lists every ending, so routing one more or one
// fewer rewrites it.
func (m *Linux) SyncRoutes(ctx context.Context) error { return m.installDNS(ctx) }

// Routed implements Installer: the endings in the DNS unit's resolvectl domain line.
func (m *Linux) Routed(ctx context.Context) ([]string, error) {
	have, err := os.ReadFile(m.dnsUnitPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(have, []byte(marker)) {
		return nil, nil
	}
	var tlds []string
	for _, line := range strings.Split(string(have), "\n") {
		f := strings.Fields(strings.TrimPrefix(line, "ExecStart="))
		if len(f) < 3 || !strings.HasPrefix(line, "ExecStart=") || f[1] != "domain" || f[2] != m.dnsLink() {
			continue
		}
		for _, d := range f[3:] {
			tlds = append(tlds, strings.TrimPrefix(d, "~"))
		}
	}
	return tlds, nil
}

// wildcards is "*.dotlane, *.test".
func (m *Linux) wildcards() string {
	w := make([]string, len(m.Cfg.TLDs))
	for i, t := range m.Cfg.TLDs {
		w[i] = "*." + t
	}
	return strings.Join(w, ", ")
}

// verifyDNS checks the whole path through the system resolver. A failure is reported, not fatal.
func (m *Linux) verifyDNS(ctx context.Context) {
	for _, tld := range m.Cfg.TLDs {
		host := "dotlane-check." + tld
		var addrs []string
		var err error
		for i := 0; i < 20; i++ {
			lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
			addrs, err = m.Lookup(lookupCtx, host)
			cancel()
			if err == nil && loopbackIn(addrs) {
				m.UI.OK("DNS check", host+" → this machine")
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if err != nil || !loopbackIn(addrs) {
			m.UI.Warn("DNS check", fmt.Sprintf("%s does not resolve yet.\n"+
				"    Check with: resolvectl query %s", host, host))
		}
	}
}

// loopbackIn reports whether addrs has a loopback address.
func loopbackIn(addrs []string) bool {
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.IsLoopback() {
			return true
		}
	}
	return false
}

// Uninstall reverses Install. With purge, it also deletes the state directory (sites and the
// CA's keys).
func (m *Linux) Uninstall(ctx context.Context, purge bool) error {
	m.UI.Title("Uninstalling Dotlane", m.summary())
	unlock, err := lockUninstall(ctx, m.Cfg)
	if err != nil {
		return err
	}
	defer unlock()

	removed := false
	if have, err := os.ReadFile(m.unitPath()); err == nil && !bytes.HasPrefix(have, []byte(marker)) {
		m.UI.Skip("Login service", m.UI.Path(m.unitPath())+" is not Dotlane's; left in place")
		return m.uninstallRest(ctx, purge, unlock)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, enabledErr := m.userctl(ctx, "is-enabled", m.unitName())
	_, activeErr := m.userctl(ctx, "is-active", m.unitName())
	if enabledErr == nil || activeErr == nil {
		if out, err := m.userctl(ctx, "disable", "--now", m.unitName()); err != nil {
			return fmt.Errorf("systemctl disable: %v: %s", err, bytes.TrimSpace(out))
		}
		removed = true
	}
	if err := os.Remove(m.unitPath()); err == nil {
		removed = true
		m.userctl(ctx, "daemon-reload") // best effort: the file is gone either way
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(m.serviceExe()); err == nil {
		removed = true
		os.Remove(m.ServiceDir) // only if no other service's copy is there
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if removed {
		m.UI.OK("Login service", "stopped and removed")
	} else {
		m.UI.Skip("Login service", "not installed")
	}
	return m.uninstallRest(ctx, purge, unlock)
}

// uninstallRest is Uninstall after the login service: the trust, the DNS unit, the ports, and the
// kept files, deleted after unlock releases the lock that is among them.
func (m *Linux) uninstallRest(ctx context.Context, purge bool, unlock func()) error {
	if err := m.untrustCA(ctx); err != nil {
		return err
	}
	if err := m.uninstallDNS(ctx); err != nil {
		return err
	}
	if err := m.uninstallPorts(ctx); err != nil {
		return err
	}

	if purge {
		unlock()
		if err := os.RemoveAll(m.Cfg.StateDir); err != nil {
			return err
		}
		m.UI.OK("Sites and CA keys", "deleted · "+m.UI.Path(m.Cfg.StateDir))
	} else {
		m.UI.Skip("Sites and CA keys", "kept · "+m.UI.Path(m.Cfg.StateDir))
	}
	m.UI.Skip("Log", "the journal keeps it until it rotates · "+m.journalCmd())

	next := ""
	if !purge {
		next = m.UI.Dim("To delete the kept files too: dotlane uninstall --purge")
	}
	m.UI.Done("Dotlane is uninstalled.", next)
	return nil
}

func (m *Linux) uninstallDNS(ctx context.Context) error {
	label := "DNS for " + m.endings()
	have, err := os.ReadFile(m.dnsUnitPath())
	switch {
	case errors.Is(err, os.ErrNotExist):
		m.UI.Skip(label, "not set")
		return nil
	case err != nil:
		return err
	case !bytes.HasPrefix(have, []byte(marker)):
		m.UI.Skip(label, m.dnsUnitPath()+" is not Dotlane's; left in place")
		return nil
	}
	m.UI.Ask(label, fmt.Sprintf("Stops sending %s lookups to Dotlane, and removes the %s link.", m.wildcards(), m.dnsLink()))
	if err := m.sudo(ctx, nil, "systemctl", "disable", "--now", m.dnsUnitName()); err != nil {
		return err
	}
	if err := m.sudo(ctx, nil, "rm", m.dnsUnitPath()); err != nil {
		return err
	}
	if err := m.sudo(ctx, nil, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	m.UI.OK(label, m.dnsUnitPath()+" removed")
	return nil
}

// otherSysctlFiles are the other Dotlane installs' sysctl files (another service name's, or the
// shared name an earlier build used), next to this install's own.
func (m *Linux) otherSysctlFiles() []string {
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(m.SysctlFile), "50-dotlane*.conf"))
	var others []string
	for _, f := range files {
		if f == m.SysctlFile {
			continue
		}
		if data, err := os.ReadFile(f); err == nil && bytes.HasPrefix(data, []byte(marker)) {
			others = append(others, f)
		}
	}
	return others
}

func (m *Linux) uninstallPorts(ctx context.Context) error {
	have, err := os.ReadFile(m.SysctlFile)
	switch {
	case errors.Is(err, os.ErrNotExist):
		m.UI.Skip("Low ports", "not changed by Dotlane")
		return nil
	case err != nil:
		return err
	case !bytes.HasPrefix(have, []byte(marker)):
		m.UI.Skip("Low ports", m.SysctlFile+" is not Dotlane's; left in place")
		return nil
	}
	others := m.otherSysctlFiles()
	if len(others) > 0 {
		m.UI.Ask("Low ports", fmt.Sprintf("Removes %s. Another Dotlane install relies on %s, so it is left as it is.", m.SysctlFile, portStartKey))
	} else {
		m.UI.Ask("Low ports", fmt.Sprintf("Restores %s to %d, the kernel's default.", portStartKey, defaultPortStart))
	}
	if err := m.sudo(ctx, nil, "rm", m.SysctlFile); err != nil {
		return err
	}
	if len(others) > 0 {
		m.UI.OK("Low ports", m.SysctlFile+" removed; "+portStartKey+" kept for "+filepath.Base(others[0]))
		return nil
	}
	if err := m.sudo(ctx, nil, "sysctl", "-w", fmt.Sprintf("%s=%d", portStartKey, defaultPortStart)); err != nil {
		return err
	}
	m.UI.OK("Low ports", m.SysctlFile+" removed, "+portStartKey+" back to "+strconv.Itoa(defaultPortStart))
	return nil
}
