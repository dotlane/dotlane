package install

// OS-neutral pieces every Installer shares: the files it copies, the daemon it waits for, the
// ports and TLS it checks. Each OS's own steps are in <os>.go and doctor_<os>.go.

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dotlane/dotlane/internal/api"
	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/proxy"
)

// Process is something holding a port.
type Process struct {
	PID   int
	Path  string // executable, as ps reports it
	Label string // the service manager's name for it (launchd label, systemd unit), when known
}

// Tool is what a Process turned out to be, and how to get it out of the way.
type Tool struct {
	Name string
	Fix  string
}

// summary names what is being installed, for the title line: "dev.dotlane · .dotlane .test".
func summary(cfg *config.Config) string {
	tlds := make([]string, len(cfg.TLDs))
	for i, t := range cfg.TLDs {
		tlds[i] = "." + t
	}
	return cfg.ServiceName + " · " + strings.Join(tlds, " ")
}

// EndingChecks are doctor's checks of the domain endings, the same on every OS: endings the
// config sets that dotlane tld add would refuse or warn about, and endings Dotlane still routes
// that the config no longer lists (routed, from Installer.Routed).
func EndingChecks(cfg *config.Config, routed []string) []Check {
	var checks []Check
	for _, w := range cfg.Warnings {
		checks = append(checks, Check{Name: "Domain ending", Status: Warning, Detail: w,
			Fix: "choose another: dotlane tld use <ending>, then dotlane tld rm <this one>"})
	}
	for _, t := range routed {
		if !slices.Contains(cfg.TLDs, t) {
			checks = append(checks, Check{Name: "DNS for ." + t, Status: Warning,
				Detail: "still sent to Dotlane, but config.toml no longer lists it",
				Fix:    fmt.Sprintf("dotlane tld rm %s (or dotlane tld add %s to keep it)", t, t)})
		}
	}
	return checks
}

// union is a, then the entries of b that a lacks.
func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, s := range b {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// temporaryBuild reports whether exe is a `go run` or test binary, which a login service must not
// point at: it disappears.
func temporaryBuild(exe string) bool {
	return strings.Contains(exe, "go-build") || strings.HasPrefix(exe, os.TempDir())
}

// copyBinary puts exe at dst when the copy there differs, through a temporary file in dst's
// folder and a rename, so a running daemon keeps its file. It reports whether it changed anything.
func copyBinary(exe, dst string) (bool, error) {
	if sameFile(exe, dst) {
		return false, nil
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return false, err
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".dotlane-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), dst)
}

// sameFile reports whether a and b have the same content.
func sameFile(a, b string) bool {
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	if err1 != nil || err2 != nil || ia.Size() != ib.Size() {
		return false
	}
	da, err1 := os.ReadFile(a)
	db, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && bytes.Equal(da, db)
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

// stale names what changed on disk after the daemon started, so it is not what the daemon runs:
// "rebuilt binary" (exe) or "changed config" (configPath); empty if neither.
func stale(st api.Status, exe, configPath string) string {
	changed := func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.ModTime().After(st.Started)
	}
	switch {
	case st.Started.IsZero(): // a daemon from before M6, which did not report it
		return "rebuilt binary"
	case changed(exe):
		return "rebuilt binary"
	case changed(configPath):
		return "changed config"
	}
	return ""
}

// waitForDaemon polls daemon until it answers or timeout passes.
func waitForDaemon(ctx context.Context, daemon func(context.Context) (api.Status, error), timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := daemon(ctx)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
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
	// Every address the daemon will bind: on Linux and Windows that is ::1 too, which another
	// program can hold while 127.0.0.1 is free.
	for _, addr := range proxy.ListenAddrs(port) {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		ln.Close()
	}
	return nil
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
