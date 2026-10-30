package install

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Process is something holding a port.
type Process struct {
	PID   int
	Path  string // executable, as ps reports it
	Label string // launchd job label, when launchd started it
}

// Tool is what a Process turned out to be, and how to get it out of the way.
type Tool struct {
	Name string
	Fix  string
}

// listeners returns the processes listening on port; proto is "tcp" or "udp". There can be
// several: Caddy binds with SO_REUSEPORT, so two Caddys can share a port and split its traffic.
// lsof only sees the user's own processes; ports held by other users show up as bind failures
// (see Mac.Bindable).
func listeners(ctx context.Context, r Runner, proto string, port int) []Process {
	args := []string{"-nP", "-F", "pn"}
	if proto == "tcp" {
		args = append(args, fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN")
	} else {
		args = append(args, fmt.Sprintf("-iUDP:%d", port))
	}
	out, _ := r.Run(ctx, "/usr/sbin/lsof", args...) // exits 1 when nothing matches
	pids := parseLsof(out)
	if len(pids) == 0 {
		return nil
	}
	list, _ := r.Run(ctx, "/bin/launchctl", "list")
	procs := make([]Process, len(pids))
	for i, pid := range pids {
		procs[i] = Process{PID: pid, Label: launchdLabel(list, pid)}
		if path, err := r.Run(ctx, "/bin/ps", "-o", "comm=", "-p", strconv.Itoa(pid)); err == nil {
			procs[i].Path = strings.TrimSpace(string(path))
		}
	}
	return procs
}

// parseLsof returns the processes in lsof -F pn output that listen, skipping those that only have
// outgoing connections on the same port (UDP lists both; their names contain "->").
func parseLsof(out []byte) []int {
	var pids []int
	pid := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n") && !strings.Contains(line, "->") && pid != 0:
			if len(pids) == 0 || pids[len(pids)-1] != pid {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

// launchdLabel finds pid in `launchctl list` output (PID, status, label per line).
func launchdLabel(list []byte, pid int) string {
	want := strconv.Itoa(pid)
	sc := bufio.NewScanner(bytes.NewReader(list))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 3 && f[0] == want {
			return f[2]
		}
	}
	return ""
}

// identify names the tools Dotlane users most often have on these ports, with the fix for each.
func identify(p Process, port, uid int, home string) Tool {
	path, label := strings.ToLower(p.Path), strings.ToLower(p.Label)
	has := func(s string) bool { return strings.Contains(path, s) || strings.Contains(label, s) }
	bootout := func() string { return fmt.Sprintf("launchctl bootout gui/%d/%s", uid, p.Label) }

	switch {
	case has("caddy"):
		if prototypeSites(home) {
			return Tool{"the Dotlane prototype's Caddy", "brew services stop caddy   (dotlane migrate will do this, with your sites)"}
		}
		return Tool{"Caddy", "brew services stop caddy"}
	case has("dnsmasq"):
		if p.Label == "dev.traix.dnsmasq" {
			return Tool{"the Dotlane prototype's dnsmasq", bootout()}
		}
		return Tool{"dnsmasq", "brew services stop dnsmasq"}
	case has("yerd"):
		fix := "quit Yerd"
		if p.Label != "" {
			fix += ", or: " + bootout()
		}
		return Tool{"Yerd", fix}
	case has("herd"):
		return Tool{"Laravel Herd", "quit Herd, or stop its services in Herd's settings"}
	case has("valet"):
		return Tool{"Laravel Valet", "valet stop"}
	case has("docker"), has("vpnkit"), has("orbstack"):
		return Tool{"a Docker container", fmt.Sprintf("stop the container that publishes port %d: docker ps --filter publish=%d", port, port)}
	}
	name := filepath.Base(p.Path)
	if name == "" || name == "." {
		name = "a process"
	}
	return Tool{
		fmt.Sprintf("%s (pid %d)", name, p.PID),
		"stop it, or give Dotlane other ports in ~/.config/dotlane/config.toml",
	}
}

// prototypeSites reports whether the Bash prototype's site files exist.
func prototypeSites(home string) bool {
	files, _ := filepath.Glob(filepath.Join(home, ".config", "caddy", "sites", "*.caddy"))
	for _, f := range files {
		if data, err := os.ReadFile(f); err == nil && bytes.HasPrefix(data, []byte("# Managed by dotlane")) {
			return true
		}
	}
	return false
}

// resolverTarget reads the nameserver and port from a resolver(5) file.
func resolverTarget(data []byte) (host string, port int) {
	port = 53
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "nameserver":
			if host == "" {
				host = f[1]
			}
		case "port":
			if n, err := strconv.Atoi(f[1]); err == nil {
				port = n
			}
		}
	}
	return host, port
}
