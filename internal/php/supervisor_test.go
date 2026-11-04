package php

import (
	"context"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dotlane/dotlane/internal/sites"
)

// TestMain lets the test binary stand in for php-fpm and frankenphp (DOTLANE_FAKE_PHP=listen):
// it reads the socket from the configuration Dotlane wrote and listens there until stopped.
// DOTLANE_FAKE_PHP=crash exits at once, as a runtime with a broken configuration does.
func TestMain(m *testing.M) {
	switch os.Getenv("DOTLANE_FAKE_PHP") {
	case "listen":
		fakeRuntime()
	case "crash":
		os.Stderr.WriteString("ERROR: failed to parse the configuration\n")
		os.Exit(78)
	}
	os.Exit(m.Run())
}

var socketSetting = regexp.MustCompile(`(?m)^\s*(?:listen = "|bind "unix/)([^"]+)"`)

func fakeRuntime() {
	var config string
	for i, a := range os.Args {
		if (a == "--fpm-config" || a == "--config") && i+1 < len(os.Args) {
			config = os.Args[i+1]
		}
	}
	data, err := os.ReadFile(config)
	m := socketSetting.FindSubmatch(data)
	if err != nil || m == nil {
		os.Exit(2)
	}
	ln, err := net.Listen("unix", string(m[1]))
	if err != nil {
		os.Exit(3)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	<-stop
	ln.Close()
	os.Exit(0)
}

// shortDir is a temporary folder with a short path: macOS caps Unix socket paths at 104 bytes.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dlphp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestSupervisor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake runtime uses Unix signals")
	}
	t.Setenv("DOTLANE_FAKE_PHP", "listen")
	state := shortDir(t)
	root := filepath.Join(state, "app")
	os.MkdirAll(root, 0o755)
	os.WriteFile(filepath.Join(root, "worker.php"), nil, 0o644)

	self, _ := os.Executable()
	s := NewSupervisor(state, 443)
	s.find = func(context.Context) []Install {
		return []Install{
			{Runtime: sites.RuntimeFPM, PHPVersion: "8.4.13", Binary: self, Source: "Homebrew"},
			{Runtime: sites.RuntimeFrankenPHP, PHPVersion: "8.5.12", Version: "1.13.4", Binary: self, Source: "Dotlane"},
		}
	}
	defer s.Stop()
	ctx := context.Background()

	list := []sites.Site{
		{Name: "a", PHP: sites.PHP{Root: root, Version: "8.4"}},
		{Name: "b", PHP: sites.PHP{Root: root, Version: "latest", Runtime: sites.RuntimeFPM}},
		{Name: "c", PHP: sites.PHP{Root: root, Version: "8.5", Runtime: sites.RuntimeFrankenPHP, Worker: "worker.php"}},
		{Name: "gone", PHP: sites.PHP{Root: filepath.Join(state, "nope"), Version: "8.4"}},
		{Name: "old", PHP: sites.PHP{Root: root, Version: "7.4", Runtime: sites.RuntimeFPM}},
		{Name: "web", Port: 5173},
	}
	ups := s.Sync(ctx, list)
	if len(ups) != 5 {
		t.Fatalf("upstreams: %+v", ups)
	}
	if ups["a"].Socket != ups["b"].Socket || ups["a"].Name != "PHP-FPM 8.4" || ups["a"].PHPVersion != "8.4.13" {
		t.Errorf("a and b share the PHP 8.4 pool: %+v %+v", ups["a"], ups["b"])
	}
	if ups["c"].Name != "FrankenPHP 1.13.4" || ups["c"].Runtime != sites.RuntimeFrankenPHP {
		t.Errorf("c: %+v", ups["c"])
	}
	if !strings.Contains(ups["gone"].Problem, "no folder at") || !strings.Contains(ups["old"].Problem, "PHP 7.4 with PHP-FPM is not installed") {
		t.Errorf("problems: %q, %q", ups["gone"].Problem, ups["old"].Problem)
	}
	for _, name := range []string{"a", "c"} {
		if conn, err := net.Dial("unix", ups[name].Socket); err != nil {
			t.Errorf("%s: not listening: %v", name, err)
		} else {
			conn.Close()
		}
	}
	conf, _ := os.ReadFile(filepath.Join(state, "php", "frankenphp-c.Caddyfile"))
	if !strings.Contains(string(conf), "worker "+caddyQuote(filepath.Join(root, "worker.php"))) {
		t.Errorf("FrankenPHP configuration:\n%s", conf)
	}

	status := s.Status()
	if len(status) != 2 || status[0].Name != "FrankenPHP 1.13.4" || status[1].Name != "PHP-FPM 8.4" ||
		!status[1].Running || strings.Join(status[1].Sites, ",") != "a,b" {
		t.Errorf("status: %+v", status)
	}

	// Without c, its FrankenPHP stops; the pool keeps running for a.
	ups = s.Sync(ctx, list[:1])
	if st := s.Status(); len(st) != 1 || strings.Join(st[0].Sites, ",") != "a" {
		t.Errorf("after removing sites: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(state, "php", "frankenphp-c.sock")); !os.IsNotExist(err) {
		t.Errorf("FrankenPHP socket left behind: %v", err)
	}
	s.Stop()
	if _, err := os.Stat(ups["a"].Socket); !os.IsNotExist(err) {
		t.Errorf("PHP-FPM socket left behind: %v", err)
	}
}

func TestSupervisorReportsACrash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake runtime uses Unix signals")
	}
	t.Setenv("DOTLANE_FAKE_PHP", "crash")
	state := shortDir(t)
	self, _ := os.Executable()
	s := NewSupervisor(state, 443)
	s.find = func(context.Context) []Install {
		return []Install{{Runtime: sites.RuntimeFPM, PHPVersion: "8.4.13", Binary: self}}
	}
	defer s.Stop()
	s.Sync(context.Background(), []sites.Site{{Name: "a", PHP: sites.PHP{Root: state, Version: "8.4"}}})
	var st []Status
	for range 50 {
		if st = s.Status(); len(st) == 1 && strings.Contains(st[0].Problem, "exited") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(st) != 1 || st[0].Running || !strings.Contains(st[0].Problem, "PHP-FPM 8.4 exited (exit status 78): ERROR: failed to parse the configuration") {
		t.Errorf("status: %+v", st)
	}
}

func TestSocketPathLength(t *testing.T) {
	s := NewSupervisor("/"+strings.Repeat("s", 60), 443)
	if p := s.socket("fpm-8.4"); p != filepath.Join("/"+strings.Repeat("s", 60), "php", "fpm-8.4.sock") {
		t.Errorf("short: %s", p)
	}
	if p := s.socket("frankenphp-" + strings.Repeat("n", 63)); len(p) > maxSocket || !strings.HasSuffix(p, ".sock") {
		t.Errorf("long: %s (%d bytes)", p, len(p))
	}
}

func TestConfigs(t *testing.T) {
	fpm := fpmConfig("8.4", "/Library/Application Support/dotlane/php/fpm-8.4.sock", "/l/fpm-8.4.log")
	for _, want := range []string{`listen = "/Library/Application Support/dotlane/php/fpm-8.4.sock"`, "listen.mode = 0600", "daemonize = no", "pm = ondemand"} {
		if !strings.Contains(fpm, want) {
			t.Errorf("PHP-FPM configuration lacks %s:\n%s", want, fpm)
		}
	}
	site := sites.Site{Name: "shop", PHP: sites.PHP{Root: `/code/my "shop"/public`, Version: "8.5", Worker: "frankenphp-worker.php"}}
	fp := frankenPHPConfig(site, "/s/php/frankenphp-shop.sock", "/s/php/frankenphp-data", 9443)
	for _, want := range []string{
		"admin off", "auto_https off",
		`bind "unix//s/php/frankenphp-shop.sock"`,
		`root "/code/my \"shop\"/public"`,
		"env HTTPS on", "env SERVER_PORT 9443",
		"respond @hidden 404",
		`try_files {path} "frankenphp-worker.php"`,
		"worker " + caddyQuote(filepath.Join(site.PHP.Root, "frankenphp-worker.php")),
	} {
		if !strings.Contains(fp, want) {
			t.Errorf("FrankenPHP configuration lacks %s:\n%s", want, fp)
		}
	}
}
