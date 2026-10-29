# Dotlane

> Your own lane after the dot.

**Dotlane gives every local project a real HTTPS address, like `https://myapp.traix`, for any
language, on macOS, Linux and Windows.** (macOS today; Linux and Windows are on the
[roadmap](docs/roadmap.md).)

Technically, it is a local domain service (DNS, a trusted certificate authority and a reverse proxy
in one program), controlled from a CLI, a per-project config file or a menu-bar app.

Every local dev server gets a real, trusted HTTPS address on a private domain ending of your
choice — `https://myapp.traix` instead of `http://localhost:5173` — in one command:

```bash
dotlane add myapp 5173
# https://myapp.traix -> localhost:5173
```

No hosts-file edits, no per-site certificates, no root daemon, and no change to how the dev server
itself is started. Adding a domain takes about a second.

## Works with any language

Dotlane needs only a name and a port: it forwards `https://<name>.traix` to whatever listens on
`localhost:<port>`. Python, Node, Go, Ruby, Java, Rust, .NET or a Docker container all work the same
way, started however you already start them.

The exception is PHP. Most PHP apps are a folder of files rather than a running server, so something
must run PHP per request. That support (PHP-FPM, per-site PHP versions) is planned for Phase 3 of the
[roadmap](docs/roadmap.md); until then, use PHP's built-in server or any PHP server on a port.

## Status

Working prototype on one Mac (macOS 26.6, Apple Silicon), built 2026-10-29 for the MyApp
project and verified end to end: DNS, trusted TLS in `curl` and Chrome, Caddy → Vite → FastAPI,
and Vite's live-reload websocket through the proxy.

It is a set of configuration files plus a ~80-line Bash helper, not yet a packaged product.

## How it works, in one picture

![Dotlane: tools, libraries and request flow](docs/diagrams/dotlane-components.svg)

The same flow as text:

```text
  browser / curl / any tool
          │  myapp.traix ?
          ▼
  macOS resolver ── /etc/resolver/traix ──▶ dnsmasq 127.0.0.1:10053
          │                                   "*.traix → 127.0.0.1, ::1"
          ▼
  https://127.0.0.1:443  ──▶  Caddy  (one site file per domain, local CA certificates)
                                │  reverse_proxy
                                ▼
                         localhost:5173  (the dev server, unchanged)
```

## Documentation

| File | What it covers |
| --- | --- |
| [`docs/architecture.md`](docs/architecture.md) | Components, request flow, and why each design choice was made |
| [`docs/diagrams/dotlane-components.svg`](docs/diagrams/dotlane-components.svg) | Diagram of the tools and libraries used and how a request flows (light and dark mode) |
| [`docs/setup.md`](docs/setup.md) | Reproducible install, verification and uninstall on macOS |
| [`docs/cli.md`](docs/cli.md) | `dotlane` command reference |
| [`docs/app-integration.md`](docs/app-integration.md) | What an app needs (allowed hosts, origins, cookies, live reload), with MyApp as the example |
| [`docs/security.md`](docs/security.md) | Trust model of the local certificate authority, what runs with which privileges |
| [`docs/troubleshooting.md`](docs/troubleshooting.md) | Failure modes found while building it, and their fixes |
| [`docs/product-notes.md`](docs/product-notes.md) | Landscape, differentiators, gaps between this prototype and a product |
| [`docs/roadmap.md`](docs/roadmap.md) | Phases from prototype to product: Go core, Homebrew/npm, PHP, Linux/Windows, desktop app |
| [`docs/phase-1.md`](docs/phase-1.md) | Phase 1 in milestones: the Go daemon, install, `doctor`, `dotlane.toml`, migration |

## Files

`files/` holds the exact configuration in use, so the setup can be reproduced or packaged:

| Path | Installed at |
| --- | --- |
| `files/bin/dotlane` | `~/.local/bin/dotlane` |
| `files/dnsmasq/traix.conf` | `~/.config/dnsmasq/traix.conf` |
| `files/launchd/dev.traix.dnsmasq.plist` | `~/Library/LaunchAgents/dev.traix.dnsmasq.plist` (with `$HOME` expanded) |
| `files/caddy/Caddyfile` | `/opt/homebrew/etc/Caddyfile` (with `$HOME` expanded) |
| `files/caddy/sites/myapp.caddy` | `~/.config/caddy/sites/myapp.caddy` (written by `dotlane add`) |
| `files/resolver/traix` | `/etc/resolver/traix` (root-owned) |
