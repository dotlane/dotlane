package proxy

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/fcgi"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

// fpm stands in for PHP-FPM: Go's FastCGI server on a Unix socket, answering with what PHP
// would see.
func fpm(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dlfcgi") // short: macOS caps Unix socket paths at 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "fpm.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := fcgi.ProcessEnv(r)
		if r.URL.Query().Get("status") == "teapot" {
			w.WriteHeader(http.StatusTeapot)
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Script", env["SCRIPT_FILENAME"])
		// net/http/fcgi keeps SCRIPT_NAME and PATH_INFO to itself; DOCUMENT_URI is both.
		fmt.Fprintf(w, "script=%s doc=%s uri=%s host=%s https=%v body=%s",
			filepath.Base(env["SCRIPT_FILENAME"]), env["DOCUMENT_URI"], r.URL.RequestURI(), r.Host, r.TLS != nil, body)
	}))
	return socket
}

func site(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"index.php":       "<?php",
		"info.php":        "<?php",
		"admin/index.php": "<?php",
		"css/app.css":     "body{}",
		".env":            "SECRET=1",
		"old.php.txt":     "text",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(content), 0o644)
	}
	return root
}

func TestPHPFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PHP-FPM does not exist on Windows")
	}
	root := site(t)
	h := &PHPFiles{Root: root, Socket: fpm(t), logger: zap.NewNop()}
	for _, tc := range []struct {
		method, target, body string
		status               int
		want                 string
	}{
		{"GET", "/css/app.css", "", 200, "body{}"},
		{"GET", "/", "", 200, "script=index.php doc=/index.php uri=/ host=shop.dotlane https=true"},
		{"GET", "/users/42?tab=1", "", 200, "script=index.php doc=/index.php uri=/users/42?tab=1"},
		{"GET", "/info.php/extra", "", 200, "script=info.php doc=/info.php/extra uri=/info.php/extra"},
		{"GET", "/admin/", "", 200, "script=index.php doc=/admin/index.php"},
		{"GET", "/old.php.txt", "", 200, "text"},
		{"POST", "/login", "user=a", 200, "uri=/login host=shop.dotlane https=true body=user=a"},
		{"GET", "/?status=teapot", "", http.StatusTeapot, "script=index.php"},
	} {
		req := httptest.NewRequest(tc.method, "https://shop.dotlane"+tc.target, strings.NewReader(tc.body))
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rec := httptest.NewRecorder()
		if err := h.ServeHTTP(rec, req, nil); err != nil {
			t.Errorf("%s %s: %v", tc.method, tc.target, err)
			continue
		}
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s %s: %d %q, want %d containing %q", tc.method, tc.target, rec.Code, rec.Body, tc.status, tc.want)
		}
	}

	// A folder with an index.php gets its trailing slash first, keeping the query.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "https://shop.dotlane/admin?x=1", nil), nil)
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/admin/?x=1" {
		t.Errorf("/admin: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	// Hidden files are never sent, nor run.
	for _, target := range []string{"/.env", "/.git/config", "/css/../.env"} {
		var he caddyhttp.HandlerError
		err := h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "https://shop.dotlane"+target, nil), nil)
		if !errors.As(err, &he) || he.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %v, want 404", target, err)
		}
	}

	// PHP-FPM not running: a 502, which gets Dotlane's page.
	down := &PHPFiles{Root: root, Socket: filepath.Join(t.TempDir(), "none.sock"), logger: zap.NewNop()}
	var he caddyhttp.HandlerError
	if err := down.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "https://shop.dotlane/", nil), nil); !errors.As(err, &he) || he.StatusCode != http.StatusBadGateway {
		t.Errorf("PHP-FPM down: %v, want 502", err)
	}
}

func TestSplitPHP(t *testing.T) {
	for _, tc := range []struct{ path, script, info string }{
		{"/index.php", "/index.php", ""},
		{"/index.php/a/b", "/index.php", "/a/b"},
		{"/a.phpx/b.php/c", "/a.phpx/b.php", "/c"},
		{"/style.css", "", ""},
	} {
		script, info, ok := splitPHP(tc.path)
		if script != tc.script || info != tc.info || ok != (tc.script != "") {
			t.Errorf("%s: %q %q %v", tc.path, script, info, ok)
		}
	}
}
