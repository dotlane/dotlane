package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dotlane/dotlane/internal/api"
	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/php"
	"github.com/dotlane/dotlane/internal/proxy"
	"github.com/dotlane/dotlane/internal/sites"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// End to end, in one process: start the daemon on spare ports, add a site in front of a test
// server, fetch it over verified HTTPS, remove it, stop. Caddy is a process-wide singleton, so
// this is the package's only daemon test.
func TestDaemon(t *testing.T) {
	// Short state path: macOS caps Unix socket paths at 104 bytes, and t.TempDir() can exceed it.
	state, err := os.MkdirTemp("", "dl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(state) })

	cfg := &config.Config{
		TLDs:      []string{"dotlane"},
		DNSPort:   freePort(t),
		HTTPPort:  freePort(t),
		HTTPSPort: freePort(t),
		StateDir:  state,
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello from "+r.Host)
	}))
	defer upstream.Close()
	upstreamPort, _ := strconv.Atoi(strings.TrimPrefix(upstream.URL, "http://127.0.0.1:"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, "test") }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("daemon did not stop")
		}
		if _, err := os.Stat(cfg.SocketPath()); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("socket left behind: %v", err)
		}
	}()

	client := api.NewClient(cfg.SocketPath())
	var status api.Status
	for i := 0; ; i++ {
		if status, err = client.Status(ctx); err == nil {
			break
		}
		if i == 300 { // 30 s: shared CI runners can be slow
			t.Fatalf("daemon not answering: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status.Version != "test" || status.HTTPSPort != cfg.HTTPSPort || status.PrimaryTLD != "dotlane" {
		t.Errorf("status = %+v", status)
	}

	// Windows has no modes; the state folder's access control list covers the socket (fsperm).
	if info, err := os.Stat(cfg.SocketPath()); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Errorf("socket mode: %v %v", info.Mode(), err)
	}
	if err := Run(ctx, cfg, "test"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second daemon: %v, want 'already running'", err)
	}

	site, err := client.AddSite(ctx, sites.Site{Name: "demo", Port: upstreamPort})
	if err != nil {
		t.Fatal(err)
	}
	wantURL := "https://demo.dotlane:" + strconv.Itoa(cfg.HTTPSPort)
	if !site.Ready || len(site.URLs) != 1 || site.URLs[0] != wantURL {
		t.Errorf("added = %+v, want ready at %s", site, wantURL)
	}
	if _, err := client.AddSite(ctx, sites.Site{Name: "Bad", Port: 1}); err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Errorf("invalid name: %v", err)
	}

	// The certificate must chain to the daemon's own root CA, for the right name.
	rootPEM, err := os.ReadFile(filepath.Join(proxy.CADir(cfg), "root.crt"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(rootPEM)
	httpsAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.HTTPSPort))
	browser := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, httpsAddr) // what DNS would do
		},
	}}
	resp, err := browser.Get(wantURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello from demo.dotlane:"+strconv.Itoa(cfg.HTTPSPort) {
		t.Errorf("GET = %d %q (the Host header must pass through)", resp.StatusCode, body)
	}

	// A name without a site gets Dotlane's page, over a certificate issued on the spot.
	resp, err = browser.Get("https://nosuch.dotlane:" + strconv.Itoa(cfg.HTTPSPort) + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 404 || !strings.Contains(string(body), "No site at <code>nosuch.dotlane</code>") {
		t.Errorf("unknown name = %d %q", resp.StatusCode, body)
	}
	// Caddy fills {placeholders} in the body; the page's CSS and favicon must come through as written.
	for _, want := range []string{":root{--bg:#f8fafc;", "@media (prefers-color-scheme:dark){:root{", `href="data:image/svg+xml,%3Csvg`, "https://nosuch.dotlane</span>"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("page lacks %q", want)
		}
	}

	// A site whose dev server is down gets a page naming the port.
	downPort := freePort(t)
	if _, err := client.AddSite(ctx, sites.Site{Name: "down", Port: downPort}); err != nil {
		t.Fatal(err)
	}
	resp, err = browser.Get("https://down.dotlane:" + strconv.Itoa(cfg.HTTPSPort) + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 502 || !strings.Contains(string(body), "localhost:"+strconv.Itoa(downPort)) {
		t.Errorf("dev server down = %d %q", resp.StatusCode, body)
	}
	if err := client.RemoveSite(ctx, "down"); err != nil {
		t.Fatal(err)
	}

	testPHP(t, ctx, client, browser, cfg)

	// Plain HTTP redirects to the HTTPS port actually in use.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(cfg.HTTPPort)+"/x?y=1", nil)
	req.Host = "demo.dotlane"
	if resp, err := noFollow.Do(req); err != nil {
		t.Error(err)
	} else {
		resp.Body.Close()
		if loc := resp.Header.Get("Location"); resp.StatusCode != 308 || loc != wantURL+"/x?y=1" {
			t.Errorf("HTTP = %d → %q, want 308 → %s/x?y=1", resp.StatusCode, loc, wantURL)
		}
	}

	list, err := client.Sites(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "demo" {
		t.Errorf("Sites = %+v, %v", list, err)
	}

	// dotlane up adopts the site added by hand; then the name is that project's.
	if site, err := client.AddSite(ctx, sites.Site{Name: "demo", Port: upstreamPort, Project: "/p/a"}); err != nil || site.Project != "/p/a" {
		t.Fatalf("adopt: %+v, %v", site, err)
	}
	if _, err := client.AddSite(ctx, sites.Site{Name: "demo", Port: upstreamPort, Project: "/p/b"}); err == nil || !strings.Contains(err.Error(), "already used by the project in /p/a") {
		t.Errorf("another project: %v", err)
	}
	if _, err := client.AddSite(ctx, sites.Site{Name: "demo", Port: 1}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Errorf("add over a project's site: %v", err)
	}
	if site, err := client.AddSite(ctx, sites.Site{Name: "demo", Port: upstreamPort, Project: "/p/a"}); err != nil || !site.Ready {
		t.Errorf("unchanged re-add: %+v, %v", site, err)
	}

	if err := client.RemoveSite(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveSite(ctx, "demo"); err == nil || !strings.Contains(err.Error(), "no such site") {
		t.Errorf("second remove: %v", err)
	}
	browser.CloseIdleConnections()
	// Caddy may still be swapping in the config without the site, and resets a connection
	// that arrives meanwhile; a request a moment later gets the new config.
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if resp, err = browser.Get(wantURL + "/"); err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Error(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("removed site: %d, want the no-site page", resp.StatusCode)
		}
	}
}

// testPHP serves a PHP site with the PHP-FPM installed on this machine, if any: a page that PHP
// runs, a file sent as it is, and the refusals for a missing folder and PHP version.
func testPHP(t *testing.T, ctx context.Context, client *api.Client, browser *http.Client, cfg *config.Config) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "index.php"), []byte(`<?php echo "php ", PHP_MAJOR_VERSION, " ", $_SERVER["REQUEST_URI"], " https=", $_SERVER["HTTPS"];`), 0o644)
	os.WriteFile(filepath.Join(root, "app.css"), []byte("body{}"), 0o644)

	if _, err := client.AddSite(ctx, sites.Site{Name: "nophp", PHP: sites.PHP{Root: filepath.Join(root, "none"), Version: "latest"}}); err == nil || !strings.Contains(err.Error(), "no folder at") {
		t.Errorf("missing folder: %v", err)
	}
	if _, err := client.AddSite(ctx, sites.Site{Name: "nophp", PHP: sites.PHP{Root: root, Version: "5.2", Runtime: sites.RuntimeFPM}}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("missing PHP: %v", err)
	}

	if _, err := php.Choose(php.Find(ctx, cfg.StateDir), sites.PHP{Version: sites.LatestPHP, Runtime: sites.RuntimeFPM}); err != nil {
		t.Logf("no PHP-FPM here, so no PHP site served: %v", err)
		return
	}
	site, err := client.AddSite(ctx, sites.Site{Name: "shop", PHP: sites.PHP{Root: root, Version: sites.LatestPHP, Runtime: sites.RuntimeFPM}})
	if err != nil {
		t.Fatal(err)
	}
	if site.PHP == nil || site.PHP.PHPVersion == "" || site.PHP.Problem != "" || !site.Ready {
		t.Errorf("PHP site: %+v %+v", site, site.PHP)
	}
	base := "https://shop.dotlane:" + strconv.Itoa(cfg.HTTPSPort)
	for path, want := range map[string]string{"/users/1?x=2": " /users/1?x=2 https=on", "/app.css": "body{}"} {
		resp, err := browser.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasSuffix(string(body), want) {
			t.Errorf("GET %s = %d %q, want …%q", path, resp.StatusCode, body, want)
		}
	}
	st, err := client.Status(ctx)
	if err != nil || len(st.PHP) != 1 || !st.PHP[0].Running || st.PHP[0].Sites[0] != "shop" {
		t.Errorf("status: %+v, %v", st.PHP, err)
	}
	if err := client.RemoveSite(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	if st, _ := client.Status(ctx); len(st.PHP) != 0 {
		t.Errorf("PHP-FPM still running without PHP sites: %+v", st.PHP)
	}
}

func TestClientWhenNotRunning(t *testing.T) {
	c := api.NewClient(filepath.Join(t.TempDir(), "none.sock"))
	if _, err := c.Sites(context.Background()); !errors.Is(err, api.ErrNotRunning) {
		t.Errorf("err = %v, want ErrNotRunning", err)
	}
}
