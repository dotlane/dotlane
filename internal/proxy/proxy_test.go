package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pedrodee/dotlane/internal/config"
	"github.com/pedrodee/dotlane/internal/sites"
)

func TestConfig(t *testing.T) {
	cfg := &config.Config{TLDs: []string{"traix", "test"}, HTTPPort: 80, HTTPSPort: 443, StateDir: "/s"}
	data, err := Config(cfg, []sites.Site{{Name: "web", Port: 5173}})
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
		`"host":["web.traix","web.test"]`, // every ending
		`"dial":"localhost:5173"`,
		`"subjects":["web.traix","web.test"]`,
		`"root":"/s/caddy"`,
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
	cfg := &config.Config{TLDs: []string{"traix"}, HTTPPort: 80, HTTPSPort: 443, StateDir: "/s"}
	data, err := Config(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"tls"`) {
		t.Errorf("no sites should mean no certificate policy: %s", data)
	}
}

func TestListenAddrs(t *testing.T) {
	if got := ListenAddrs(443); len(got) != 1 || got[0] != ":443" {
		t.Errorf("443: %v", got)
	}
	if got := ListenAddrs(9443); len(got) != 2 || got[0] != "127.0.0.1:9443" || got[1] != "[::1]:9443" {
		t.Errorf("9443: %v", got)
	}
	cfg := &config.Config{TLDs: []string{"dldev"}, HTTPPort: 9480, HTTPSPort: 9443, StateDir: "/s"}
	data, _ := Config(cfg, nil)
	if !strings.Contains(string(data), `https://{http.request.host}:9443{http.request.uri}`) {
		t.Errorf("redirect ignores the HTTPS port: %s", data)
	}
}
