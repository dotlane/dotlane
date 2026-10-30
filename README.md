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

It is a set of configuration files plus a ~100-line Bash helper, not yet a packaged product.

The Go version that replaces it is in progress ([`docs/phase-1.md`](docs/phase-1.md)): milestones
M1 (a daemon with DNS, embedded Caddy and a control socket), M2 (`dotlane install` /
`uninstall`), M3 (`dotlane doctor`, conflict checks) and M4 (project files: `dotlane.toml`,
`up` / `down`) and M5 (`dotlane migrate`) work: this Mac moved from the prototype to the binary
on 2026-10-30 with no new password. M6 (hardening, release candidate) is built; its integration
test on GitHub's macOS runners is the last check.

## Development (Go version)

Go 1.27. The dev instance uses spare ports and `dev/state/`, so it runs next to a real install
without touching it. `scripts/integration.sh` is the end-to-end test (install, HTTPS through the
system's DNS and trust, uninstall); it changes the system, so it runs on CI's throwaway macOS
runners, not on a development Mac.

```bash
go test -race ./...
go build -o bin/dotlane ./cmd/dotlane

./bin/dotlane --config dev/config.toml serve             # terminal 1: DNS :10054, HTTPS :9443
./bin/dotlane --config dev/config.toml add myapp 5173 # terminal 2
./bin/dotlane --config dev/config.toml ls --json

curl --cacert dev/state/caddy/pki/authorities/local/root.crt \
  --resolve myapp.dldev:9443:127.0.0.1 https://myapp.dldev:9443/
dig +short anything.dldev @127.0.0.1 -p 10054             # 127.0.0.1
```

The dev CA is not trusted by the system until the dev instance is installed, hence `--cacert`. To
test the full path (login service, CA trust, `/etc/resolver/dldev`), run in a terminal, since
sudo asks for a password:

```bash
./bin/dotlane --config dev/config.toml install              # then: curl https://<name>.dldev:9443/
./bin/dotlane --config dev/config.toml uninstall --purge
```

Output uses colour on a terminal only; `NO_COLOR=1` turns it off, `CLICOLOR_FORCE=1` forces it
(for `less -R`).

| Package | Role |
| --- | --- |
| `cmd/dotlane` | CLI (`install`, `uninstall`, `doctor`, `migrate`, `add`, `rm`, `ls`, `up`, `down`, `serve`, `version`) |
| `internal/install` | Login service, CA trust and resolver files; `doctor` checks and naming the tools in the way; `migrate` from the prototype. macOS now, other OSes in Phase 4 |
| `internal/ui` | Output on Lip Gloss: one line per step, markers, aligned labels, the `ls` table, colours adapted to light/dark terminals. Help and usage are styled by Fang |
| `internal/daemon` | Runs DNS, Caddy and the control socket around the site registry |
| `internal/dns` | Answers `*.<tld>` with 127.0.0.1 / ::1, refuses everything else |
| `internal/proxy` | Builds Caddy's JSON config; Caddy runs in-process, admin endpoint off |
| `internal/api` | JSON over a Unix socket (mode 0600): handler and client |
| `internal/sites` | Site registry, saved atomically to `sites.json`; which project owns each name |
| `internal/project` | `dotlane.toml`: finding it, reading it, what `up` and `down` change |
| `internal/config` | `config.toml`: endings, ports, state directory |

## How it works, in one picture

The picture shows the Bash prototype, which this Mac ran until 2026-10-30. The Go version keeps the same
flow with DNS, Caddy and the CA inside one `dotlane` process: see
[`docs/how-it-works.md`](docs/how-it-works.md).

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
| [`docs/how-it-works.md`](docs/how-it-works.md) | What the Go version does and how: the daemon, a request end to end, certificates, install, projects, `doctor`, `migrate` |
| [`docs/architecture.md`](docs/architecture.md) | The Bash prototype: components, request flow, and why each design choice was made |
| [`docs/diagrams/dotlane-components.svg`](docs/diagrams/dotlane-components.svg) | Diagram of the tools and libraries used and how a request flows (light and dark mode) |
| [`docs/setup.md`](docs/setup.md) | Build, install, first site, upgrade, migrate from the prototype, uninstall (macOS) |
| [`docs/cli.md`](docs/cli.md) | Command reference: every command, `dotlane.toml`, `config.toml`, output and exit codes |
| [`docs/prototype/`](docs/prototype/) | The Bash prototype's setup and command reference, kept for reference |
| [`docs/app-integration.md`](docs/app-integration.md) | What an app needs (allowed hosts, origins, cookies, live reload), with MyApp as the example |
| [`docs/security.md`](docs/security.md) | Trust model of the local certificate authority, what runs with which privileges |
| [`docs/troubleshooting.md`](docs/troubleshooting.md) | Common problems and their fixes, and lessons from the prototype |
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

## Licence

Apache-2.0 ([`LICENSE`](LICENSE), [`NOTICE`](NOTICE)), the same licence as Caddy, which the binary
embeds.
