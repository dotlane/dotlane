// Package proxy runs Caddy inside the Dotlane process: HTTPS with a local certificate authority,
// and one route per site: a reverse proxy to its port, or its PHP runtime in front of its files.
package proxy

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddytls"
	// Only the Caddy modules Dotlane uses; modules/standard adds ~8 MB (S3, metrics, ACME
	// server and more) to the binary.
	_ "github.com/caddyserver/caddy/v2/modules/caddyevents"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	_ "github.com/caddyserver/caddy/v2/modules/caddypki"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls/standardstek"
	_ "github.com/caddyserver/caddy/v2/modules/filestorage"

	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/php"
	"github.com/dotlane/dotlane/internal/sites"
)

func init() { caddy.RegisterModule(Permission{}) }

// Permission lets Caddy issue a certificate on demand for any single-label name under the
// domain endings (unknown.dotlane), so a name without a site gets a page saying so instead of a
// TLS error. Anything else is refused. Only this Mac resolves the endings to Dotlane, and other
// machines are dropped, so this cannot be used to mint certificates for someone else.
type Permission struct {
	TLDs []string `json:"tlds"`
}

// CaddyModule implements caddy.Module.
func (Permission) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "tls.permission.dotlane", New: func() caddy.Module { return new(Permission) }}
}

var labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// CertificateAllowed implements caddytls.OnDemandPermission.
func (p Permission) CertificateAllowed(_ context.Context, name string) error {
	label, tld, ok := strings.Cut(name, ".")
	for _, t := range p.TLDs {
		if ok && tld == t && len(label) <= 63 && labelPattern.MatchString(label) {
			return nil
		}
	}
	return fmt.Errorf("%s is not a name under %v", name, p.TLDs)
}

var _ caddytls.OnDemandPermission = Permission{}

// The pages Dotlane serves itself: one layout (page.html), with the mark and an adaptive favicon
// (favicon.svg, written by scripts/brandgen), filled per page. Both are embedded, so the pages
// need nothing from the network.
var (
	//go:embed page.html
	pageLayout string
	//go:embed favicon.svg
	faviconSVG string
)

// faviconURI is favicon.svg as a data: URI for <link rel="icon">.
var faviconURI = "data:image/svg+xml," + strings.NewReplacer(
	"\n", "", `"`, "'", "#", "%23", "<", "%3C", ">", "%3E", "{", "%7B", "}", "%7D",
).Replace(faviconSVG)

// page fills the layout. light and dark colour the dot in the page's address bar: the state of
// the address in each colour scheme.
func page(title, light, dark, body string) string {
	return strings.NewReplacer(
		"%TITLE%", title, "%FAVICON%", faviconURI, "%STATE_LIGHT%", light, "%STATE_DARK%", dark, "%BODY%", body,
	).Replace(pageLayout)
}

// The pages use the TLS server name, which Permission has checked, rather than the Host header,
// which a client could fill with markup.
var (
	noSitePage = page("No site here", "#94a3b8", "#64748b", `<p class="status">404 · no site</p>
<h1>No site at <code>{http.request.tls.server_name}</code></h1>
<p>Nothing is registered under this name yet. To serve a dev server here:</p>
<pre>dotlane add &lt;name&gt; &lt;port&gt;</pre>
<p>Or list it in the project's <code>dotlane.toml</code> and run <code>dotlane up</code>. <code>dotlane ls</code> shows the sites there are.</p>`)
	downPage = page("Dev server not running", "#d97706", "#f59e0b", `<p class="status">502 · not answering</p>
<h1>The dev server is not answering</h1>
<p><code>{http.request.tls.server_name}</code> forwards to <code>{http.reverse_proxy.upstream.hostport}</code>, and nothing answered there.</p>
<p>Start the dev server, then reload this page.</p>`)
)

// phpProblemPage is for a PHP site that cannot be served: no PHP for it, or no folder.
func phpProblemPage(up php.Upstream) string {
	return page("PHP site not available", "#dc2626", "#ef4444", `<p class="status">503 · `+text(up.Name)+` not available</p>
<h1><code>{http.request.tls.server_name}</code> cannot be served</h1>
<pre>`+text(up.Problem)+`</pre>
<p>Then run <code>dotlane up</code> in the project again (or <code>dotlane add</code>), and reload this page.</p>`)
}

// phpDownPage is for a PHP site whose runtime is not answering: starting, or exited.
func phpDownPage(up php.Upstream) string {
	return page("PHP not answering", "#d97706", "#f59e0b", `<p class="status">502 · not answering</p>
<h1>`+text(up.Name)+` is not answering</h1>
<p><code>{http.request.tls.server_name}</code> is served by `+text(up.Name)+` (PHP `+text(up.PHPVersion)+`), which Dotlane runs and restarts when it stops. It is not answering now.</p>
<p><code>dotlane doctor</code> says why. Its log: <code>`+text(up.Log)+`</code></p>`)
}

// text escapes s for a page: HTML, and the braces Caddy would read as placeholders.
func text(s string) string {
	return strings.NewReplacer("{", "&#123;", "}", "&#125;").Replace(html.EscapeString(s))
}

var htmlType = obj{"Content-Type": []string{"text/html; charset=utf-8"}}

// phpHandler serves a PHP site: PHP-FPM through PHPFiles; FrankenPHP, which serves the files
// itself, behind a reverse proxy.
func phpHandler(s sites.Site, up php.Upstream) obj {
	switch {
	case up.Problem != "":
		return obj{"handler": "static_response", "status_code": 503, "headers": htmlType, "body": phpProblemPage(up)}
	case up.Runtime == sites.RuntimeFrankenPHP:
		return obj{"handler": "reverse_proxy", "upstreams": []obj{{"dial": "unix/" + up.Socket}}}
	}
	return obj{"handler": "dotlane_php", "root": s.PHP.Root, "socket": up.Socket}
}

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
// ports below 1024 only on every interface, not on 127.0.0.1 alone, so there 80 and 443 are
// bound wide and guarded (see thisMachineOnly). Everything else stays on loopback: Linux allows
// low ports once install has lowered net.ipv4.ip_unprivileged_port_start, and Windows always
// does. ::1 is left out where IPv6 is turned off (ipv6.disable=1, some containers and WSL
// setups): binding it would fail and stop Caddy.
func ListenAddrs(port int) []string {
	if port < 1024 && wideLowPorts {
		return []string{fmt.Sprintf(":%d", port)}
	}
	addrs := []string{fmt.Sprintf("127.0.0.1:%d", port)}
	if ipv6Loopback() {
		addrs = append(addrs, fmt.Sprintf("[::1]:%d", port))
	}
	return addrs
}

// ipv6Loopback reports whether this machine can listen on ::1, checked once.
var ipv6Loopback = sync.OnceValue(func() bool {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
})

// wideLowPorts is whether ports below 1024 must be bound on every interface (macOS).
var wideLowPorts = runtime.GOOS == "darwin"

// thisMachineOnly is every server's first route: a request from another machine is dropped without
// an answer. On a wide listener another device can still connect and, over HTTPS, complete a
// handshake for a known name; it never reaches a site.
var thisMachineOnly = obj{
	"match": []obj{{"not": []obj{{"remote_ip": obj{
		"ranges": []string{"127.0.0.0/8", "::1/128", "::ffff:127.0.0.0/104"},
	}}}}},
	"handle":   []obj{{"handler": "static_response", "abort": true}},
	"terminal": true,
}

// Config builds Caddy's JSON configuration for the given sites. phpSites says how to reach each
// PHP site, by name (php.Supervisor.Sync).
func Config(cfg *config.Config, list []sites.Site, phpSites map[string]php.Upstream) ([]byte, error) {
	routes := []obj{thisMachineOnly}
	var errorRoutes []obj
	var subjects []string
	for _, s := range list {
		hosts := Hosts(cfg, s)
		subjects = append(subjects, hosts...)
		handler := obj{
			"handler":   "reverse_proxy",
			"upstreams": []obj{{"dial": fmt.Sprintf("localhost:%d", s.Port)}},
		}
		if s.IsPHP() {
			up, ok := phpSites[s.Name]
			if !ok {
				up = php.Upstream{Name: "PHP", Problem: "Dotlane has not started PHP for this site; run dotlane doctor"}
			}
			handler = phpHandler(s, up)
			if up.Problem == "" {
				errorRoutes = append(errorRoutes, obj{
					"match": []obj{{"host": hosts, "vars": obj{"{http.error.status_code}": []string{"502"}}}},
					"handle": []obj{{
						"handler":     "static_response",
						"status_code": 502,
						"headers":     htmlType,
						"body":        phpDownPage(up),
					}},
				})
			}
		}
		routes = append(routes, obj{
			"match":    []obj{{"host": hosts}},
			"handle":   []obj{handler},
			"terminal": true,
		})
	}
	errorRoutes = append(errorRoutes, obj{
		"match": []obj{{"vars": obj{"{http.error.status_code}": []string{"502"}}}},
		"handle": []obj{{
			"handler":     "static_response",
			"status_code": 502,
			"headers":     htmlType,
			"body":        downPage,
		}},
	})

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
					"listen": ListenAddrs(cfg.HTTPSPort),
					// Sites first; every other name gets the page that says so.
					"routes": append(routes, obj{"handle": []obj{{
						"handler":     "static_response",
						"status_code": 404,
						"headers":     htmlType,
						"body":        noSitePage,
					}}}),
					"errors":          obj{"routes": errorRoutes},
					"automatic_https": obj{"disable_redirects": true},
				},
				"dotlane-http": obj{
					"listen": ListenAddrs(cfg.HTTPPort),
					"routes": []obj{thisMachineOnly, {
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
	// Registered sites get their certificates up front; any other name under the endings gets
	// one at its first handshake, for the no-site page.
	var policies []obj
	if len(subjects) > 0 {
		policies = append(policies, obj{"subjects": subjects, "issuers": []obj{{"module": "internal"}}})
	}
	policies = append(policies, obj{"issuers": []obj{{"module": "internal"}}, "on_demand": true})
	apps["tls"] = obj{
		"automation": obj{
			"policies":  policies,
			"on_demand": obj{"permission": obj{"module": "dotlane", "tlds": cfg.TLDs}},
		},
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
func Apply(cfg *config.Config, list []sites.Site, phpSites map[string]php.Upstream) error {
	data, err := Config(cfg, list, phpSites)
	if err != nil {
		return err
	}
	return caddy.Load(data, false)
}

// Stop shuts Caddy down.
func Stop() error { return caddy.Stop() }
