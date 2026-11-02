//go:build linux

package install

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

var errAccess = syscall.EACCES

const ssSample = `udp   UNCONN 0      0       127.0.0.53%lo:53        0.0.0.0:*    users:(("systemd-resolve",pid=585,fd=13))
tcp   LISTEN 0      4096    127.0.0.53%lo:53        0.0.0.0:*    users:(("systemd-resolve",pid=585,fd=14))
tcp   LISTEN 0      511           0.0.0.0:80        0.0.0.0:*    users:(("nginx",pid=700,fd=5),("nginx",pid=701,fd=5))
tcp   LISTEN 0      4096             [::]:443          [::]:*
tcp   LISTEN 0      4096        127.0.0.1:8080      0.0.0.0:*    users:(("docker-proxy",pid=900,fd=4))
`

func TestParseSS(t *testing.T) {
	for _, tc := range []struct {
		proto string
		port  int
		name  string
		pids  []int
	}{
		{"udp", 53, "systemd-resolve", []int{585}},
		{"tcp", 80, "nginx", []int{700, 701}},
		{"tcp", 443, "", nil},
		{"tcp", 8080, "docker-proxy", []int{900}},
	} {
		got := parseSS([]byte(ssSample), tc.proto, tc.port)
		if len(got) != 1 || got[0].name != tc.name || len(got[0].pids) != len(tc.pids) {
			t.Errorf("%s:%d = %+v", tc.proto, tc.port, got)
		}
	}
	if got := parseSS([]byte(ssSample), "udp", 80); len(got) != 0 {
		t.Errorf("udp:80 = %+v", got)
	}
}

func TestUnitOf(t *testing.T) {
	for in, want := range map[string]string{
		"0::/system.slice/nginx.service\n":                                         "nginx.service",
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/foo.service\n": "user/foo.service",
		"0::/user.slice/user-1000.slice/session-3.scope\n":                         "",
		"0::/\n": "",
	} {
		if got := unitOf([]byte(in)); got != want {
			t.Errorf("unitOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentify(t *testing.T) {
	m, _, _ := newTestLinux(t)
	for _, tc := range []struct {
		p    Process
		name string
	}{
		{Process{PID: 1, Path: "/usr/sbin/dnsmasq"}, "dnsmasq"},
		{Process{PID: 1, Path: "/usr/bin/caddy", Label: "caddy.service"}, "Caddy"},
		{Process{PID: 1, Path: "/usr/sbin/nginx"}, "nginx"},
		{Process{PID: 1, Path: "/usr/sbin/apache2"}, "Apache"},
		{Process{PID: 1, Path: "/usr/bin/docker-proxy"}, "a Docker container"},
		{Process{PID: 1, Path: "/usr/lib/systemd/systemd-resolved"}, "systemd-resolved"},
		{Process{}, "a process of another user"},
		{Process{PID: 5, Path: "/opt/x/thing"}, "thing (pid 5)"},
	} {
		if got := m.identify(tc.p, 80); got.Name != tc.name {
			t.Errorf("identify(%+v) = %q, want %q", tc.p, got.Name, tc.name)
		}
	}
	os.MkdirAll(filepath.Join(m.Home, ".valet"), 0o755)
	if got := m.identify(Process{PID: 1, Path: "/usr/sbin/nginx"}, 80); got.Name != "Laravel Valet" {
		t.Errorf("nginx next to Valet = %q", got.Name)
	}
}

// Without ss, /proc/net and the fd links find the owner.
func TestProcListeners(t *testing.T) {
	m, _, _ := newTestLinux(t)
	net := filepath.Join(m.ProcDir, "net")
	os.MkdirAll(net, 0o755)
	os.WriteFile(filepath.Join(net, "tcp"), []byte("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
		"   0: 0100007F:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 1 0 0\n"), 0o644)
	fd := filepath.Join(m.ProcDir, "321", "fd")
	os.MkdirAll(fd, 0o755)
	os.Symlink("socket:[12345]", filepath.Join(fd, "7"))
	os.WriteFile(filepath.Join(m.ProcDir, "321", "comm"), []byte("caddy\n"), 0o644)

	got := m.procListeners("tcp", 443)
	if len(got) != 1 || got[0].PID != 321 || got[0].Path != "caddy" {
		t.Errorf("listeners = %+v", got)
	}
	if got := m.procListeners("tcp", 80); len(got) != 0 {
		t.Errorf("port 80 = %+v", got)
	}
}
