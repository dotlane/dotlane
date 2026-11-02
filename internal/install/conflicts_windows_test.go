//go:build windows

package install

import (
	"strings"
	"testing"
)

func TestListenersScriptAsksForEachPort(t *testing.T) {
	got := listenersScript([]portSpec{{"tcp", 443}, {"udp", 53}})
	for _, want := range []string{"@('tcp',443),@('udp',53)", "Get-NetTCPConnection -State Listen", "Get-NetUDPEndpoint", "Win32_Service"} {
		if !strings.Contains(got, want) {
			t.Errorf("script lacks %q", want)
		}
	}
}

func TestParseListeners(t *testing.T) {
	// ConvertTo-Json -Compress prints one line.
	out := []byte(`[{"Proto":"tcp","Port":80,"PID":4,"Name":"System","Path":"","Services":""},` +
		`{"Proto":"tcp","Port":80,"PID":4,"Name":"System","Path":"","Services":""},` +
		`{"Proto":"udp","Port":53,"PID":1180,"Name":"svchost","Path":"C:\\Windows\\System32\\svchost.exe","Services":"SharedAccess"}]`)
	list, err := parseListeners(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 { // the same process listening on two addresses counts once
		t.Fatalf("got %d listeners: %+v", len(list), list)
	}
	if got := on(list, "udp", 53); len(got) != 1 || got[0].Label != "SharedAccess" || got[0].PID != 1180 {
		t.Errorf("udp 53 = %+v", got)
	}
	if len(on(list, "tcp", 443)) != 0 {
		t.Error("tcp 443 has a listener")
	}
}

func TestIdentify(t *testing.T) {
	for name, tc := range map[string]struct {
		l       winListener
		tool    string
		fixHas  string
		toolNot string
	}{
		"http.sys": {winListener{Process: Process{PID: 4}, Name: "System", Port: 80}, "http.sys", "netsh http show servicestate", ""},
		"iis": {winListener{Process: Process{PID: 4120, Label: "W3SVC,WAS", Path: `C:\Windows\System32\svchost.exe`}, Name: "svchost", Port: 80},
			"IIS", "Stop-Service W3SVC", ""},
		"ics": {winListener{Process: Process{PID: 1180, Label: "SharedAccess"}, Name: "svchost", Port: 53},
			"Internet Connection Sharing", "Stop-Service SharedAccess", ""},
		"ssrs": {winListener{Process: Process{PID: 9, Label: "SQLServerReportingServices"}, Name: "ReportingServicesService", Port: 80},
			"Reporting Services", "Configuration Manager", ""},
		"docker": {winListener{Process: Process{PID: 77, Path: `C:\Program Files\Docker\Docker\resources\com.docker.backend.exe`}, Name: "com.docker.backend", Port: 443},
			"Docker Desktop", "docker ps --filter publish=443", ""},
		"wsl":       {winListener{Process: Process{PID: 78}, Name: "wslrelay", Port: 80}, "WSL", "wsl --shutdown", ""},
		"dnsmasq":   {winListener{Process: Process{PID: 79}, Name: "dnsmasq", Port: 53}, "dnsmasq", "Stop-Process -Id 79", ""},
		"acrylic":   {winListener{Process: Process{PID: 80}, Name: "AcrylicService", Port: 53}, "Acrylic", "AcrylicDNSProxySvc", ""},
		"skype":     {winListener{Process: Process{PID: 81}, Name: "Skype", Port: 443}, "Skype", "quit", ""},
		"generic":   {winListener{Process: Process{PID: 82}, Name: "node", Port: 443}, "node (pid 82)", "config.toml", ""},
		"generic53": {winListener{Process: Process{PID: 83}, Name: "node", Port: 53}, "node (pid 83)", "port 53", ""},
		// "named" is a DNS server only when it is the whole name.
		"renamed": {winListener{Process: Process{PID: 84}, Name: "renamed-tool", Port: 443}, "renamed-tool (pid 84)", "", "DNS"},
	} {
		got := identify(tc.l, "config.toml")
		if !strings.Contains(got.Name, tc.tool) || !strings.Contains(got.Fix, tc.fixHas) {
			t.Errorf("%s: identify = %+v, want %q / %q", name, got, tc.tool, tc.fixHas)
		}
		if tc.toolNot != "" && strings.Contains(got.Name, tc.toolNot) {
			t.Errorf("%s: identify = %+v", name, got)
		}
	}
}
