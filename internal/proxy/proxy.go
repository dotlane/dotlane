// Package proxy runs Caddy inside the Dotlane process: HTTPS with a local certificate authority,
// and one reverse-proxy route per site.
package proxy

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/caddyserver/caddy/v2"
	// Only the Caddy modules Dotlane uses; modules/standard adds ~8 MB (S3, metrics, ACME
	// server and more) to the binary.
	_ "github.com/caddyserver/caddy/v2/modules/caddyevents"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	_ "github.com/caddyserver/caddy/v2/modules/caddypki"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls/standardstek"
	_ "github.com/caddyserver/caddy/v2/modules/filestorage"

	"github.com/pedrodee/dotlane/internal/config"
	"github.com/pedrodee/dotlane/internal/sites"
)

// Hosts are the names a site answers on: one per domain ending.
func Hosts(cfg *config.Config, s sites.Site) []string {
	hosts := make([]string, len(cfg.TLDs))
	for i, tld := range cfg.TLDs {
		hosts[i] = s.Name + "." + tld
	}
	return hosts
}

// CADir holds the local certificate authority's root and intermediate.
func CADir(cfg *config.Config) string {
	return filepath.Join(cfg.StateDir, "caddy", "pki", "authorities", "local")
}

type obj = map[string]any

// ListenAddrs are the addresses Dotlane listens on for port. macOS lets a normal user listen on
// ports below 1024 only on every interface, not on 127.0.0.1 alone, so 80 and 443 are bound
// wide and guarded (see thisMacOnly); other ports stay on loopback.
func ListenAddrs(port int) []string {
	if port < 1024 {
		return []string{fmt.Sprintf(":%d", port)}
	}
	return []string{fmt.Sprintf("127.0.0.1:%d", port), fmt.Sprintf("[::1]:%d", port)}
}

// thisMacOnly is every server's first route: a request from another machine is dropped without
// an answer. On a wide listener another device can still connect and, over HTTPS, complete a
// handshake for a known name; it never reaches a site.
var thisMacOnly = obj{
	"match": []obj{{"not": []obj{{"remote_ip": obj{
		"ranges": []string{"127.0.0.0/8", "::1/128", "::ffff:127.0.0.0/104"},
	}}}}},
	"handle":   []obj{{"handler": "static_response", "abort": true}},
	"terminal": true,
}

// Config builds Caddy's JSON configuration for the given sites.
func Config(cfg *config.Config, list []sites.Site) ([]byte, error) {
	routes := []obj{thisMacOnly}
	var subjects []string
	for _, s := range list {
		hosts := Hosts(cfg, s)
		subjects = append(subjects, hosts...)
		routes = append(routes, obj{
			"match": []obj{{"host": hosts}},
			"handle": []obj{{
				"handler":   "reverse_proxy",
				"upstreams": []obj{{"dial": fmt.Sprintf("localhost:%d", s.Port)}},
			}},
			"terminal": true,
		})
	}

	// Plain HTTP only redirects. Caddy's own redirect server is turned off: it would listen where
	// the HTTPS server does, and point at 443 whatever https_port says.
	location := "https://{http.request.host}{http.request.uri}"
	if cfg.HTTPSPort != 443 {
		location = fmt.Sprintf("https://{http.request.host}:%d{http.request.uri}", cfg.HTTPSPort)
	}
	apps := obj{
		"http": obj{
			"http_port":  cfg.HTTPPort,
			"https_port": cfg.HTTPSPort,
			// Dev servers keep websockets open (Vite's live reload); without a limit, stopping
			// or restarting the daemon would wait for them forever.
			"grace_period": "2s",
			"servers": obj{
				"dotlane": obj{
					"listen":          ListenAddrs(cfg.HTTPSPort),
					"routes":          routes,
					"automatic_https": obj{"disable_redirects": true},
				},
				"dotlane-http": obj{
					"listen": ListenAddrs(cfg.HTTPPort),
					"routes": []obj{thisMacOnly, {
						"handle": []obj{{
							"handler":     "static_response",
							"status_code": 308,
							"headers":     obj{"Location": []string{location}},
						}},
					}},
				},
			},
		},
		"pki": obj{
			"certificate_authorities": obj{
				"local": obj{
					"name":                     "Dotlane Local Authority",
					"root_common_name":         "Dotlane Local Authority - ECC Root",
					"intermediate_common_name": "Dotlane Local Authority - ECC Intermediate",
					// Trusting the root needs the user's password; `dotlane install` does it,
					// once, with an explanation. Caddy must never prompt on its own.
					"install_trust": false,
				},
			},
		},
	}
	if len(subjects) > 0 {
		apps["tls"] = obj{
			"automation": obj{
				"policies": []obj{{
					"subjects": subjects,
					"issuers":  []obj{{"module": "internal"}},
				}},
			},
		}
	}

	return json.Marshal(obj{
		// No admin endpoint: the daemon reconfigures Caddy in-process, and port 2019 stays free
		// (the prototype's Caddy uses it).
		"admin":   obj{"disabled": true, "config": obj{"persist": false}},
		"storage": obj{"module": "file_system", "root": filepath.Join(cfg.StateDir, "caddy")},
		"apps":    apps,
	})
}

// Apply loads the configuration for these sites into the running Caddy, starting it if needed.
func Apply(cfg *config.Config, list []sites.Site) error {
	data, err := Config(cfg, list)
	if err != nil {
		return err
	}
	return caddy.Load(data, false)
}

// Stop shuts Caddy down.
func Stop() error { return caddy.Stop() }
