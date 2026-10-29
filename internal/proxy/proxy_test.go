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
		`"disabled":true`,                 // no admin endpoint on :2019
		`"install_trust":false`,           // never prompts for a password
		`"127.0.0.1:443","[::1]:443"`,     // loopback only
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
