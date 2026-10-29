// Package daemon runs Dotlane's background service: the DNS responder, the embedded Caddy and
// the control socket, around one site registry.
package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/pedrodee/dotlane/internal/api"
	"github.com/pedrodee/dotlane/internal/config"
	"github.com/pedrodee/dotlane/internal/dns"
	"github.com/pedrodee/dotlane/internal/proxy"
	"github.com/pedrodee/dotlane/internal/sites"
)

// macOS limits Unix socket paths to 104 bytes, including the terminating NUL.
const maxSocketPath = 103

// Daemon implements api.Backend.
type Daemon struct {
	cfg      *config.Config
	version  string
	registry *sites.Registry
	dnsAddr  string

	mu sync.Mutex // serialises registry changes with the Caddy reload that follows them
}

// Run serves until ctx is cancelled, then shuts everything down.
func Run(ctx context.Context, cfg *config.Config, version string) error {
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return err
	}
	socket := cfg.SocketPath()
	if len(socket) > maxSocketPath {
		return fmt.Errorf("socket path is too long for macOS (%d bytes, max %d): %s; set a shorter state_dir",
			len(socket), maxSocketPath, socket)
	}
	if err := claimSocket(socket); err != nil {
		return err
	}

	registry, err := sites.Open(filepath.Join(cfg.StateDir, "sites.json"))
	if err != nil {
		return err
	}
	d := &Daemon{cfg: cfg, version: version, registry: registry}

	resolver, err := dns.Start(net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.DNSPort)), cfg.TLDs)
	if err != nil {
		return fmt.Errorf("DNS: %w", err)
	}
	defer resolver.Shutdown()
	d.dnsAddr = resolver.Addr()

	if err := proxy.Apply(cfg, registry.List()); err != nil {
		return fmt.Errorf("Caddy: %w", err)
	}
	defer proxy.Stop()

	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0o600); err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: api.Handler(d), ReadHeaderTimeout: 5 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	log.Printf("dotlane %s: DNS on %s for .%v, HTTPS on port %d, %d site(s), control socket %s",
		version, d.dnsAddr, cfg.TLDs, cfg.HTTPSPort, len(registry.List()), socket)

	select {
	case <-ctx.Done():
	case err := <-served:
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// claimSocket refuses to start a second daemon, and clears a socket left by one that crashed.
func claimSocket(path string) error {
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		conn.Close()
		return fmt.Errorf("dotlane is already running (%s)", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (d *Daemon) view(s sites.Site) api.Site {
	hosts := proxy.Hosts(d.cfg, s)
	urls := make([]string, len(hosts))
	for i, h := range hosts {
		urls[i] = "https://" + h
		if d.cfg.HTTPSPort != 443 {
			urls[i] += ":" + strconv.Itoa(d.cfg.HTTPSPort)
		}
	}
	return api.Site{Name: s.Name, Port: s.Port, URLs: urls, Ready: true}
}

// Status implements api.Backend.
func (d *Daemon) Status() api.Status {
	return api.Status{
		Version:   d.version,
		TLDs:      d.cfg.TLDs,
		DNSAddr:   d.dnsAddr,
		HTTPSPort: d.cfg.HTTPSPort,
		StateDir:  d.cfg.StateDir,
	}
}

// Sites implements api.Backend.
func (d *Daemon) Sites() []api.Site {
	list := d.registry.List()
	out := make([]api.Site, len(list))
	for i, s := range list {
		out[i] = d.view(s)
	}
	return out
}

// AddSite implements api.Backend. If Caddy rejects the new configuration, the registry is
// restored, so a failed add leaves nothing behind.
func (d *Daemon) AddSite(ctx context.Context, s sites.Site) (api.Site, error) {
	if err := s.Validate(); err != nil {
		return api.Site{}, err
	}
	d.mu.Lock()
	prev, had := d.registry.Get(s.Name)
	if err := d.registry.Put(s); err != nil {
		d.mu.Unlock()
		return api.Site{}, err
	}
	if err := proxy.Apply(d.cfg, d.registry.List()); err != nil {
		if had {
			d.registry.Put(prev)
		} else {
			d.registry.Delete(s.Name)
		}
		d.mu.Unlock()
		return api.Site{}, fmt.Errorf("Caddy rejected the change: %w", err)
	}
	d.mu.Unlock()

	v := d.view(s)
	v.Ready = d.waitForTLS(ctx, proxy.Hosts(d.cfg, s)[0])
	return v, nil
}

// RemoveSite implements api.Backend.
func (d *Daemon) RemoveSite(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	prev, ok := d.registry.Get(name)
	if !ok {
		return fmt.Errorf("%w: %s", sites.ErrNotFound, name)
	}
	if err := d.registry.Delete(name); err != nil {
		return err
	}
	if err := proxy.Apply(d.cfg, d.registry.List()); err != nil {
		d.registry.Put(prev)
		return fmt.Errorf("Caddy rejected the change: %w", err)
	}
	return nil
}

// waitForTLS returns once host completes a TLS handshake, or false after about 5 seconds.
// Caddy issues a new site's certificate in the background after a reload; waiting here means the
// URL the CLI prints works on the first click (the prototype's wait_for_tls).
func (d *Daemon) waitForTLS(ctx context.Context, host string) bool {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(d.cfg.HTTPSPort))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		dialer := &tls.Dialer{
			NetDialer: &net.Dialer{Timeout: time.Second},
			// Only checking that a certificate is served, not who issued it: trust may not be
			// installed yet (dotlane install), and the server is our own process.
			Config: &tls.Config{ServerName: host, InsecureSkipVerify: true},
		}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
