//go:build linux

package install

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	ssPID  = regexp.MustCompile(`pid=(\d+)`)
	ssName = regexp.MustCompile(`\(\("([^"]*)"`)
)

// listeners returns the processes listening on port; proto is "tcp" or "udp". There can be
// several: Caddy binds with SO_REUSEPORT. ss lists every socket but names only the user's own
// processes; a socket of another user comes back as a Process with PID 0.
func (m *Linux) listeners(ctx context.Context, proto string, port int) []Process {
	out, err := m.Runner.Run(ctx, m.tool("ss"), "-H", "-lntup")
	if err != nil {
		return m.procListeners(proto, port)
	}
	var procs []Process
	seen := map[int]bool{}
	for _, l := range parseSS(out, proto, port) {
		if len(l.pids) == 0 {
			procs = append(procs, Process{Path: l.name})
		}
		for _, pid := range l.pids {
			if !seen[pid] {
				seen[pid] = true
				procs = append(procs, m.process(pid, l.name))
			}
		}
	}
	return procs
}

type ssListener struct {
	name string
	pids []int
}

// parseSS returns the sockets in `ss -H -lntup` output that listen on port with proto.
func parseSS(out []byte, proto string, port int) []ssListener {
	var list []ssListener
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || f[0] != proto {
			continue
		}
		i := strings.LastIndex(f[4], ":")
		if i < 0 || f[4][i+1:] != strconv.Itoa(port) {
			continue
		}
		var l ssListener
		if len(f) > 6 {
			users := strings.Join(f[6:], " ")
			if n := ssName.FindStringSubmatch(users); n != nil {
				l.name = n[1]
			}
			for _, p := range ssPID.FindAllStringSubmatch(users, -1) {
				pid, _ := strconv.Atoi(p[1])
				l.pids = append(l.pids, pid)
			}
		}
		list = append(list, l)
	}
	return list
}

// process describes pid from /proc: its executable and the systemd unit it runs in.
func (m *Linux) process(pid int, name string) Process {
	p := Process{PID: pid, Path: name}
	if exe, err := os.Readlink(filepath.Join(m.ProcDir, strconv.Itoa(pid), "exe")); err == nil {
		p.Path = strings.TrimSuffix(exe, " (deleted)")
	}
	if cg, err := os.ReadFile(filepath.Join(m.ProcDir, strconv.Itoa(pid), "cgroup")); err == nil {
		p.Label = unitOf(cg)
	}
	return p
}

// unitOf is the systemd service in /proc/<pid>/cgroup output: "nginx.service", or
// "user/app.service" for a unit of a user manager. Login sessions and containers have none.
func unitOf(cgroup []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(cgroup))
	for sc.Scan() {
		_, path, ok := strings.Cut(sc.Text(), "::")
		if !ok {
			continue
		}
		parts := strings.Split(path, "/")
		last := parts[len(parts)-1]
		if !strings.HasSuffix(last, ".service") {
			continue
		}
		if strings.Contains(path, "/user@") {
			return "user/" + last
		}
		return last
	}
	return ""
}

// procListeners is listeners without ss: /proc/net/{tcp,udp}{,6} for the sockets, and the
// /proc/<pid>/fd links for their owners.
func (m *Linux) procListeners(proto string, port int) []Process {
	state := "0A" // TCP_LISTEN
	if proto == "udp" {
		state = "07" // an unconnected UDP socket
	}
	inodes := map[string]bool{}
	for _, file := range []string{proto, proto + "6"} {
		data, err := os.ReadFile(filepath.Join(m.ProcDir, "net", file))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) < 10 || f[3] != state {
				continue
			}
			if i := strings.LastIndex(f[1], ":"); i >= 0 && strings.EqualFold(f[1][i+1:], fmt.Sprintf("%04X", port)) {
				inodes[f[9]] = true
			}
		}
	}
	if len(inodes) == 0 {
		return nil
	}
	var procs []Process
	dirs, _ := filepath.Glob(filepath.Join(m.ProcDir, "[0-9]*"))
	for _, dir := range dirs {
		pid, err := strconv.Atoi(filepath.Base(dir))
		if err != nil {
			continue
		}
		fds, _ := os.ReadDir(filepath.Join(dir, "fd"))
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err == nil && strings.HasPrefix(link, "socket:[") && inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] {
				comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
				procs = append(procs, m.process(pid, strings.TrimSpace(string(comm))))
				break
			}
		}
	}
	if len(procs) == 0 { // sockets of other users, whose fds cannot be read
		procs = append(procs, Process{})
	}
	return procs
}

// stopFix is the command that stops the unit p runs in, or alt when it has none.
func stopFix(p Process, alt string) string {
	switch {
	case strings.HasPrefix(p.Label, "user/"):
		return "systemctl --user disable --now " + strings.TrimPrefix(p.Label, "user/")
	case p.Label != "":
		return "sudo systemctl disable --now " + p.Label
	}
	return alt
}

// identify names the tools Dotlane users most often have on these ports, with the fix for each.
func (m *Linux) identify(p Process, port int) Tool {
	path, label := strings.ToLower(p.Path), strings.ToLower(p.Label)
	has := func(s string) bool { return strings.Contains(path, s) || strings.Contains(label, s) }
	_, valet1 := os.Stat(filepath.Join(m.Home, ".valet"))
	_, valet2 := os.Stat(filepath.Join(m.Home, ".config", "valet"))
	valet := valet1 == nil || valet2 == nil

	switch {
	case has("valet"), valet && (has("nginx") || has("dnsmasq")):
		return Tool{"Laravel Valet", "valet stop"}
	case has("systemd-resolve"):
		return Tool{"systemd-resolved", fmt.Sprintf("its stub owns port 53; give Dotlane another dns_port in %s", m.UI.Path(m.ConfigPath))}
	case has("dnsmasq"):
		return Tool{"dnsmasq", stopFix(p, "stop dnsmasq")}
	case has("caddy"):
		return Tool{"Caddy", stopFix(p, "stop Caddy")}
	case has("nginx"):
		return Tool{"nginx", stopFix(p, "stop nginx")}
	case has("apache"), has("httpd"):
		return Tool{"Apache", stopFix(p, "stop Apache")}
	case has("docker"):
		return Tool{"a Docker container", fmt.Sprintf("stop the container that publishes port %d: docker ps --filter publish=%d", port, port)}
	case p.PID == 0 && p.Path == "":
		return Tool{"a process of another user", fmt.Sprintf("find it with: sudo ss -lntup 'sport = :%d'", port)}
	}
	name := filepath.Base(p.Path)
	if name == "" || name == "." {
		name = "a process"
	}
	return Tool{
		fmt.Sprintf("%s (pid %d)", name, p.PID),
		"stop it, or give Dotlane other ports in " + m.UI.Path(m.ConfigPath),
	}
}
