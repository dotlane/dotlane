package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseLsof(t *testing.T) {
	// Real output for UDP 443 on a Mac: Caddy listens, browsers hold outgoing QUIC connections.
	out := []byte("p2779\nn*:443\np1396\nn[2001:db8::1]:63466->[2603:1026::2]:443\np55114\nn10.0.0.2:58249->142.250.0.1:443\n")
	if got := parseLsof(out); !slices.Equal(got, []int{2779}) {
		t.Errorf("parseLsof = %v, want only the listener", got)
	}
	// Two processes sharing a port (SO_REUSEPORT), one with two sockets.
	out = []byte("p10\nn127.0.0.1:443\nn[::1]:443\np20\nn*:443\n")
	if got := parseLsof(out); !slices.Equal(got, []int{10, 20}) {
		t.Errorf("parseLsof = %v", got)
	}
	if got := parseLsof(nil); len(got) != 0 {
		t.Errorf("parseLsof(nil) = %v", got)
	}
}

func TestLaunchdLabel(t *testing.T) {
	list := []byte("PID\tStatus\tLabel\n-\t0\tcom.apple.x\n2588\t0\tdev.traix.dnsmasq\n71950\t-9\tdev.yerd.daemon\n")
	if got := launchdLabel(list, 71950); got != "dev.yerd.daemon" {
		t.Errorf("label = %q", got)
	}
	if got := launchdLabel(list, 1); got != "" {
		t.Errorf("label = %q", got)
	}
}

func TestIdentify(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		p              Process
		name, fixStart string
	}{
		{Process{PID: 2779, Path: "/opt/homebrew/opt/caddy/bin/caddy", Label: "sh.brew.caddy"}, "Caddy", "brew services stop caddy"},
		{Process{PID: 2588, Path: "/opt/homebrew/sbin/dnsmasq", Label: "dev.traix.dnsmasq"}, "the Dotlane prototype's dnsmasq", "launchctl bootout gui/501/dev.traix.dnsmasq"},
		{Process{PID: 3, Path: "/opt/homebrew/sbin/dnsmasq"}, "dnsmasq", "brew services stop dnsmasq"},
		{Process{PID: 71950, Path: "yerdd", Label: "dev.yerd.daemon"}, "Yerd", "quit Yerd, or: launchctl bootout gui/501/dev.yerd.daemon"},
		{Process{PID: 4, Path: "/Applications/Herd.app/Contents/Resources/nginx"}, "Laravel Herd", "quit Herd"},
		{Process{PID: 5, Path: "/Users/me/.composer/vendor/laravel/valet/nginx"}, "Laravel Valet", "valet stop"},
		{Process{PID: 6, Path: "/Applications/Docker.app/Contents/MacOS/com.docker.backend"}, "a Docker container", "stop the container that publishes port 443"},
		{Process{PID: 7, Path: "/usr/local/bin/myserver"}, "myserver (pid 7)", "stop it"},
	} {
		got := identify(tc.p, 443, 501, home)
		if got.Name != tc.name || !strings.HasPrefix(got.Fix, tc.fixStart) {
			t.Errorf("%s: got %+v, want %q / %q…", tc.p.Path, got, tc.name, tc.fixStart)
		}
	}

	// With the prototype's site files present, Caddy is named as the prototype's.
	sites := filepath.Join(home, ".config", "caddy", "sites")
	os.MkdirAll(sites, 0o755)
	os.WriteFile(filepath.Join(sites, "a.caddy"), []byte("# Managed by dotlane: https://a.traix -> localhost:1\n"), 0o644)
	if got := identify(Process{Path: "/opt/homebrew/opt/caddy/bin/caddy"}, 443, 501, home); got.Name != "the Dotlane prototype's Caddy" {
		t.Errorf("prototype Caddy: %+v", got)
	}
}

func TestResolverTarget(t *testing.T) {
	for _, tc := range []struct {
		in   string
		host string
		port int
	}{
		{"nameserver 127.0.0.1\nport 1053\n", "127.0.0.1", 1053},
		{"nameserver 10.0.0.1\n", "10.0.0.1", 53},
		{"domain x\nnameserver ::1\nnameserver 127.0.0.1\nport bad\n", "::1", 53},
	} {
		if h, p := resolverTarget([]byte(tc.in)); h != tc.host || p != tc.port {
			t.Errorf("%q: got %s:%d", tc.in, h, p)
		}
	}
}
