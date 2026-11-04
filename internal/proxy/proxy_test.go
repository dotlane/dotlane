package proxy

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/php"
	"github.com/dotlane/dotlane/internal/sites"
)

func TestConfig(t *testing.T) {
	defer func(wide bool) { wideLowPorts = wide }(wideLowPorts)
	wideLowPorts = true
	cfg := &config.Config{TLDs: []string{"dotlane", "test"}, HTTPPort: 80, HTTPSPort: 443, StateDir: "/s"}
	data, err := Config(cfg, []sites.Site{{Name: "web", Port: 5173}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`"disabled":true`,       // no admin endpoint on :2019
		`"install_trust":false`, // never prompts for a password
		`"listen":[":443"]`,     // macOS: below 1024 only on every interface
		`"listen":[":80"]`,
		`"127.0.0.0/8","::1/128"`, // so other machines are dropped
		`"Location":["https://{http.request.host}{http.request.uri}"]`,
		`"host":["web.dotlane","web.test"]`, // every ending
		`"dial":"localhost:5173"`,
		`"subjects":["web.dotlane","web.test"]`,
		`"root":` + jsonString(filepath.Join("/s", "caddy")),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("config lacks %s\n%s", want, text)
		}
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
}

func TestConfigWithoutSites(t *testing.T) {
	cfg := &config.Config{TLDs: []string{"dotlane"}, HTTPPort: 80, HTTPSPort: 443, StateDir: "/s"}
	data, err := Config(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"subjects"`) || !strings.Contains(string(data), `"on_demand":true`) {
		t.Errorf("no sites: want only the on-demand policy: %s", data)
	}
}

func TestListenAddrs(t *testing.T) {
	defer func(wide bool) { wideLowPorts = wide }(wideLowPorts)
	wideLowPorts = true // macOS
	if got := ListenAddrs(443); len(got) != 1 || got[0] != ":443" {
		t.Errorf("443: %v", got)
	}
	wideLowPorts = false // Linux, Windows
	if got := ListenAddrs(443); len(got) != 2 || got[0] != "127.0.0.1:443" || got[1] != "[::1]:443" {
		t.Errorf("443 on loopback: %v", got)
	}
	ipv6Loopback = func() bool { return false } // IPv6 turned off
	if got := ListenAddrs(443); len(got) != 1 || got[0] != "127.0.0.1:443" {
		t.Errorf("443 without IPv6: %v", got)
	}
	defer func(v6 func() bool) { ipv6Loopback = v6 }(ipv6Loopback)
	ipv6Loopback = func() bool { return true }
	if got := ListenAddrs(9443); len(got) != 2 || got[0] != "127.0.0.1:9443" || got[1] != "[::1]:9443" {
		t.Errorf("9443: %v", got)
	}
	cfg := &config.Config{TLDs: []string{"dldev"}, HTTPPort: 9480, HTTPSPort: 9443, StateDir: "/s"}
	data, _ := Config(cfg, nil, nil)
	if !strings.Contains(string(data), `https://{http.request.host}:9443{http.request.uri}`) {
		t.Errorf("redirect ignores the HTTPS port: %s", data)
	}
}

func TestPermission(t *testing.T) {
	p := Permission{TLDs: []string{"dotlane", "test"}}
	for _, name := range []string{"web.dotlane", "a.test", "my-app2.dotlane"} {
		if err := p.CertificateAllowed(context.Background(), name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"dotlane", "a.b.dotlane", "web.com", "-a.dotlane", "Web.dotlane", "web.dotlane.evil", ""} {
		if p.CertificateAllowed(context.Background(), name) == nil {
			t.Errorf("%s allowed", name)
		}
	}
}

// jsonString is s as a JSON string literal: Windows paths have backslashes, which JSON escapes.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestConfigPHP(t *testing.T) {
	cfg := &config.Config{TLDs: []string{"dotlane"}, HTTPPort: 80, HTTPSPort: 443, StateDir: "/s"}
	list := []sites.Site{
		{Name: "fpm", PHP: sites.PHP{Root: "/code/fpm/public", Version: "8.4"}},
		{Name: "fp", PHP: sites.PHP{Root: "/code/fp/public", Version: "latest", Runtime: sites.RuntimeFrankenPHP}},
		{Name: "missing", PHP: sites.PHP{Root: "/code/missing", Version: "7.4"}},
	}
	ups := map[string]php.Upstream{
		"fpm":     {Runtime: sites.RuntimeFPM, Name: "PHP-FPM 8.4", PHPVersion: "8.4.13", Socket: "/s/php/fpm-8.4.sock", Log: "/s/php/fpm-8.4.log"},
		"fp":      {Runtime: sites.RuntimeFrankenPHP, Name: "FrankenPHP 1.13.4", PHPVersion: "8.5.12", Socket: "/s/php/frankenphp-fp.sock"},
		"missing": {Runtime: sites.RuntimeFPM, Name: "PHP-FPM 7.4", Problem: "PHP 7.4 with PHP-FPM is not installed\ninstall it: brew install php@7.4 {x}"},
	}
	data, err := Config(cfg, list, ups)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`"handler":"dotlane_php","root":"/code/fpm/public","socket":"/s/php/fpm-8.4.sock"`,
		`"dial":"unix//s/php/frankenphp-fp.sock"`,
		`"status_code":503`,
		`PHP 7.4 with PHP-FPM is not installed`,
		"brew install php@7.4 \\u0026#123;x\\u0026#125;", // braces are not Caddy placeholders
		`PHP-FPM 8.4 is not answering`,
		`/s/php/fpm-8.4.log`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("config lacks %s", want)
		}
	}
	// The PHP sites' own 502 pages come before the dev servers' one.
	if strings.Index(text, "PHP-FPM 8.4 is not answering") > strings.Index(text, "The dev server is not answering") {
		t.Error("the generic 502 page comes first")
	}
}
