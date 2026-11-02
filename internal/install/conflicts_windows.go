//go:build windows

package install

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// portSpec is one listening port to look for: proto is "tcp" or "udp".
type portSpec struct {
	proto string
	port  int
}

// winListener is a process holding a port Dotlane wants. Label (from Process) lists the Windows
// services that run inside it, which is how svchost.exe, one file for dozens of services, tells
// IIS from Internet Connection Sharing.
type winListener struct {
	Process
	Proto string
	Port  int
	Name  string // the process name
}

// listenersScript prints, as a JSON array, the processes listening on the ports on an address
// that 127.0.0.1 would clash with: loopback, or every interface. PID 4 is the System process,
// where http.sys (IIS, and anything else that registers an HTTP URL) listens.
func listenersScript(ports []portSpec) string {
	var want []string
	for _, p := range ports {
		want = append(want, fmt.Sprintf("@('%s',%d)", p.proto, p.port))
	}
	return queryPreamble + `$rows = @()
foreach ($w in @(` + strings.Join(want, ",") + `)) {
    if ($w[0] -eq 'tcp') { $eps = Get-NetTCPConnection -State Listen -LocalPort $w[1] -ErrorAction SilentlyContinue }
    else { $eps = Get-NetUDPEndpoint -LocalPort $w[1] -ErrorAction SilentlyContinue }
    foreach ($e in $eps) {
        if ($e.LocalAddress -in '127.0.0.1', '0.0.0.0', '::') {
            $id = [int]$e.OwningProcess
            $p = Get-Process -Id $id -ErrorAction SilentlyContinue
            $path = ''
            try { $path = [string]$p.Path } catch { }
            $svc = @(Get-CimInstance Win32_Service -Filter "ProcessId=$id" -ErrorAction SilentlyContinue | ForEach-Object { $_.Name }) -join ','
            $rows += [pscustomobject]@{ Proto = $w[0]; Port = [int]$w[1]; PID = $id; Name = [string]$p.ProcessName; Path = $path; Services = $svc }
        }
    }
}
ConvertTo-Json -Compress -InputObject $rows
`
}

type listenerRow struct {
	Proto    string
	Port     int
	PID      int
	Name     string
	Path     string
	Services string
}

// parseListeners reads listenersScript's output, one entry per process and port.
func parseListeners(out []byte) ([]winListener, error) {
	rows, err := decodeList[listenerRow](out)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var list []winListener
	for _, r := range rows {
		key := fmt.Sprintf("%s:%d:%d", r.Proto, r.Port, r.PID)
		if seen[key] { // an address on each of several interfaces lists the process once each
			continue
		}
		seen[key] = true
		list = append(list, winListener{
			Process: Process{PID: r.PID, Path: r.Path, Label: r.Services},
			Proto:   r.Proto, Port: r.Port, Name: r.Name,
		})
	}
	return list, nil
}

// listeners returns the processes listening on ports, from a single PowerShell run.
func listeners(ctx context.Context, r WinRunner, ports ...portSpec) ([]winListener, error) {
	out, err := r.Run(ctx, powershellExe(), queryArgs(listenersScript(ports))...)
	if err != nil {
		return nil, fmt.Errorf("listing listeners: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return parseListeners(out)
}

// on returns the listeners of one port.
func on(list []winListener, proto string, port int) []winListener {
	var out []winListener
	for _, l := range list {
		if l.Proto == proto && l.Port == port {
			out = append(out, l)
		}
	}
	return out
}

// identify names the tools Dotlane users most often meet on these ports, with the fix for each.
// configPath is the config file, for the generic advice to move to other ports.
func identify(l winListener, configPath string) Tool {
	services := strings.ToLower(l.Label)
	name := strings.ToLower(l.Name)
	path := strings.ToLower(l.Path)
	service := func(names ...string) bool {
		for _, s := range strings.Split(services, ",") {
			for _, n := range names {
				if s == n {
					return true
				}
			}
		}
		return false
	}
	has := func(s string) bool { return strings.Contains(name, s) || strings.Contains(path, s) }
	stop := func(svc string) string {
		return fmt.Sprintf("Stop-Service %s   (in a PowerShell run as administrator)", svc)
	}

	switch {
	case l.PID == 4 || name == "system":
		return Tool{"Windows' HTTP Server API (http.sys): IIS, SQL Server Reporting Services or another service that registered this port",
			"find who with: netsh http show servicestate view=requestq   and   netsh http show urlacl\n" +
				"for IIS: iisreset /stop (in a PowerShell run as administrator)"}
	case service("w3svc", "was") || name == "w3wp" || name == "inetinfo":
		return Tool{"IIS (World Wide Web Publishing Service)", stop("W3SVC") + ", or change its bindings in IIS Manager"}
	case service("sharedaccess"):
		return Tool{"Internet Connection Sharing (SharedAccess), which also serves Hyper-V's Default Switch, Mobile Hotspot and WSL's NAT",
			stop("SharedAccess") + "; if you need sharing, move the shared network to another computer or use the Hotspot's own DNS"}
	case service("reportserver", "sqlserverreportingservices", "powerbireportserver") || strings.HasPrefix(services, "reportserver"):
		return Tool{"SQL Server Reporting Services", "stop it in SQL Server Reporting Services Configuration Manager, or move its URL off this port"}
	case service("winrm"):
		return Tool{"Windows Remote Management (WinRM)", stop("WinRM") + "   (or: winrm delete winrm/config/listener?Address=*+Transport=HTTP)"}
	case has("docker") || has("vpnkit") || has("com.docker"):
		return Tool{"Docker Desktop (a container publishing the port)",
			fmt.Sprintf("stop the container: docker ps --filter publish=%d   then docker stop <id>", l.Port)}
	case has("wslrelay") || has("wslservice") || has("wslhost") || has("wsl.exe"):
		return Tool{"WSL (it forwards ports between Windows and Linux)",
			"stop the Linux program that listens on this port, or run: wsl --shutdown"}
	case has("dnsmasq"):
		return Tool{"dnsmasq", fmt.Sprintf("stop it: Stop-Process -Id %d (or its service)", l.PID)}
	case has("acrylic"):
		return Tool{"Acrylic DNS Proxy", stop("AcrylicDNSProxySvc") + ", or use its own hosts rules instead of Dotlane's"}
	case has("unbound") || has("technitium") || has("pihole") || has("adguard") || has("simpledns") || name == "named":
		return Tool{"a local DNS server (" + l.Name + ")", fmt.Sprintf("stop it: Stop-Process -Id %d (or its service)", l.PID)}
	case has("skype"):
		return Tool{"Skype", "quit Skype (older versions listen on 80 and 443)"}
	case has("vmware-hostd"):
		return Tool{"VMware Workstation Shared VMs", "turn off Shared VMs in VMware (Edit, Preferences, Shared VMs)"}
	case has("caddy"):
		return Tool{"Caddy", fmt.Sprintf("stop it: Stop-Process -Id %d (or its service)", l.PID)}
	case has("nginx"):
		return Tool{"nginx", "stop it: nginx -s stop"}
	case has("httpd") || has("apache") || has("xampp"):
		return Tool{"Apache", "stop it (XAMPP's control panel, or its service)"}
	}
	display := l.Name
	if display == "" {
		display = filepath.Base(l.Path)
	}
	if display == "" || display == "." {
		display = "a process"
	}
	fix := "stop it, or give Dotlane other ports in " + configPath
	if l.Port == 53 {
		fix = "stop it: Windows sends names to a DNS server by address only, so Dotlane needs port 53"
	}
	return Tool{fmt.Sprintf("%s (pid %d)", display, l.PID), fix}
}
