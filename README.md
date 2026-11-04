<img src="assets/brand/mark.svg" width="72" alt="Dotlane">

# Dotlane

> Your own lane after the dot.

**Dotlane gives every local project a real HTTPS address, like `https://myapp.dotlane`, for any
language, on macOS, Linux and Windows.** (Linux and Windows from the next release; see
[`docs/compatibility.md`](docs/compatibility.md).)

Technically, it is a local domain service (DNS, a trusted certificate authority and a reverse proxy
in one program), controlled from a CLI, a per-project config file or a menu-bar app.

Every local dev server gets a real, trusted HTTPS address on a private domain ending of your
choice — `https://myapp.dotlane` instead of `http://localhost:5173` — in one command:

```bash
dotlane add myapp 5173
# https://myapp.dotlane -> localhost:5173
```

No hosts-file edits, no per-site certificates, no root daemon, and no change to how the dev server
itself is started. Adding a domain takes about a second.

<img src="assets/demo/dotlane.gif" width="720" alt="A terminal running dotlane install, then dotlane add myapp 5173, then curl https://myapp.dotlane, which prints the dev server's answer over trusted HTTPS">

## Install

On macOS, Linux or Windows (x64 or arm64), with npm:

```bash
npm install -g dotlane
dotlane install                       # once: login service, trusted CA, DNS for .dotlane
dotlane add myapp 5173                # https://myapp.dotlane -> localhost:5173
```

`dotlane install` explains each step that needs administrator rights before running it. Or try
it without installing anything, with `npx dotlane install`. Homebrew
(`brew install dotlane/tap/dotlane`) is coming soon. Building from source, upgrading and
uninstalling are in [`docs/setup.md`](docs/setup.md) (macOS), [`docs/setup-linux.md`](docs/setup-linux.md)
(deb, rpm and Arch packages too) and [`docs/setup-windows.md`](docs/setup-windows.md); WSL2 in
[`docs/wsl.md`](docs/wsl.md).

Another ending, or several: `dotlane tld add test`, `dotlane tld use <ending>`
([`docs/cli.md`](docs/cli.md#domain-endings)).

## Works with any language

Dotlane needs only a name and a port: it forwards `https://<name>.dotlane` to whatever listens on
`localhost:<port>`. Python, Node, Go, Ruby, Java, Rust, .NET or a Docker container all work the same
way, started however you already start them.

PHP apps are usually a folder of files rather than a running server, so Dotlane runs PHP for them:
`dotlane add shop --php ~/code/shop/public`, or `root = "public"` and `php = "8.4"` in the project's
`dotlane.toml`. It uses the PHP versions you already have (Homebrew, mise, your package manager)
through PHP-FPM, one pool per version, so each site can have its own, as with Laravel Valet or Herd.
FrankenPHP works too, worker mode included: `dotlane frankenphp install`, then
`runtime = "frankenphp"` ([`docs/cli.md`](docs/cli.md#php-sites)).

## Status

**Phase 1 is complete (2026-10-30):** one Go binary on macOS
([`docs/history/phase-1.md`](docs/history/phase-1.md)). It covers:
- the daemon: DNS, embedded Caddy, a control socket;
- `install` / `uninstall` / `doctor`;
- `dotlane.toml` with `up` / `down`;
- `migrate` from the Bash prototype;
- hardening.

The integration test on GitHub's macOS runners goes from install to the first site in about a
second, then checks upgrades and a clean uninstall. A real Vite + FastAPI project runs on it day to day.

The Bash prototype it replaced (Caddy and dnsmasq around a ~100-line script, built 2026-10-29)
is kept in [`docs/prototype/`](docs/prototype/).

**`v0.1.0` is public (2026-11-01)** ([`docs/history/phase-2.md`](docs/history/phase-2.md)):
releases with GoReleaser, on npm with provenance. The Homebrew cask is built and tested but on
hold. What comes next is in each product's roadmap: the [core](docs/roadmap.md) (signing, then
PHP), [npm](npm/ROADMAP.md) and [Homebrew](docs/homebrew.md).

## Development (Go version)

Go 1.27. The dev instance uses spare ports and `dev/state/`, so it runs next to a real install
without touching it. `scripts/integration.sh` is the end-to-end test (install, HTTPS through the
system's DNS and trust, uninstall); it changes the system, so it runs on CI's throwaway macOS
runners, not on a development Mac. `go run ./scripts/brandgen` redraws every icon file from the
numbers in it (see [`docs/brand.md`](docs/brand.md)).

Before pushing, `scripts/check.sh` runs what CI's `test` job runs (gofmt, build, vet and
`govulncheck` for Linux, macOS and Windows, `go test -race`, the npm tests, `goreleaser check`); about a
minute the first time, while it fetches its pinned tools, then under 20 seconds.

```bash
scripts/check.sh
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
| `internal/install` | Login service, CA trust and DNS routing per OS (launchd and `/etc/resolver`; systemd and systemd-resolved; Task Scheduler and NRPT); `doctor` checks and naming the tools in the way; `migrate` from the prototype |
| `internal/fsperm` | Keeps the state folder private: file modes on macOS and Linux, an access control list on Windows |
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
          │  myapp.dotlane ?
          ▼
  macOS resolver ── /etc/resolver/dotlane ──▶ dnsmasq 127.0.0.1:10053
          │                                   "*.dotlane → 127.0.0.1, ::1"
          ▼
  https://127.0.0.1:443  ──▶  Caddy  (one site file per domain, local CA certificates)
                                │  reverse_proxy
                                ▼
                         localhost:5173  (the dev server, unchanged)
```

## Documentation

| File | What it covers |
| --- | --- |
| [`docs/brand.md`](docs/brand.md) | The mark, colours, type and Dotlane's own pages; the files are in `assets/brand/` |
| [`docs/how-it-works.md`](docs/how-it-works.md) | What the Go version does and how: the daemon, a request end to end, certificates, install, projects, `doctor`, `migrate` |
| [`docs/architecture.md`](docs/architecture.md) | The design decisions: one Go binary, the daemon and its clients, configuration, what differs per OS |
| [`docs/diagrams/dotlane-components.svg`](docs/diagrams/dotlane-components.svg) | Diagram of the tools and libraries used and how a request flows (light and dark mode) |
| [`docs/diagrams/branching-flow.svg`](docs/diagrams/branching-flow.svg) | Animated diagram of branches, pull requests, the merge queue, CI and release tags ([`docs/branching.md`](docs/branching.md)); generated by `branching-flow.py` |
| [`docs/setup.md`](docs/setup.md) | Install (npm or source; Homebrew coming soon), first site, upgrade, migrate from the prototype, uninstall (macOS) |
| [`docs/setup-linux.md`](docs/setup-linux.md), [`docs/setup-windows.md`](docs/setup-windows.md), [`docs/wsl.md`](docs/wsl.md) | What differs on Linux, Windows and WSL2 |
| [`docs/compatibility.md`](docs/compatibility.md) | Where each OS stands, item by item |
| [`docs/cli.md`](docs/cli.md) | Command reference: every command, `dotlane.toml`, `config.toml`, output and exit codes |
| [`docs/prototype/`](docs/prototype/) | The Bash prototype's architecture, setup and command reference, kept for reference |
| [`docs/app-integration.md`](docs/app-integration.md) | What an app needs (allowed hosts, origins, cookies, live reload), with a Vite + FastAPI app as the example |
| [`SECURITY.md`](SECURITY.md) | How to report a vulnerability, where the CA key lives, and what is in scope |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Issues, working on the code, what a pull request needs |
| [`CHANGELOG.md`](CHANGELOG.md) | What changed in each version; every version gets a section before it is tagged |
| [`docs/distribution.md`](docs/distribution.md) | One binary, many channels: how a tag becomes the GitHub Release, the Homebrew cask and the npm packages; Linux, Windows and the desktop apps |
| [`docs/launch.md`](docs/launch.md) | Launch day: the steps that take Dotlane public, in order, with the exact commands |
| [`docs/branching.md`](docs/branching.md) | How changes land: branches from `main`, the merge queue, changelog fragments, what CI runs when, versions and patches |
| [`docs/releasing.md`](docs/releasing.md) | What a release contains and how to cut one (GoReleaser, `v*` tags, draft releases, npm packages) |
| [`docs/security-guide.md`](docs/security-guide.md) | How the repositories, secrets, releases and website are protected; the checklist before going public |
| [`docs/security.md`](docs/security.md) | Trust model of the local certificate authority, what runs with which privileges |
| [`docs/troubleshooting.md`](docs/troubleshooting.md) | Common problems and their fixes, and lessons from the prototype |
| [`docs/product-notes.md`](docs/product-notes.md) | Landscape, differentiators, gaps between this prototype and a product |
| [`docs/roadmap.md`](docs/roadmap.md) | The core's roadmap: signing, PHP and managed processes, Linux and Windows, phones, teams |
| [`npm/ROADMAP.md`](npm/ROADMAP.md) | The npm packages' roadmap |
| [`docs/homebrew.md`](docs/homebrew.md) | Homebrew: where the cask is built, why it is on hold, how to resume, its roadmap |
| [`docs/history/`](docs/history/README.md) | Finished plans: the first roadmap in phases, Phase 1 and Phase 2 |

## Files

`files/` holds the exact configuration in use, so the setup can be reproduced or packaged:

| Path | Installed at |
| --- | --- |
| `files/bin/dotlane` | `~/.local/bin/dotlane` |
| `files/dnsmasq/dotlane.conf` | `~/.config/dnsmasq/dotlane.conf` |
| `files/launchd/dev.dotlane.dnsmasq.plist` | `~/Library/LaunchAgents/dev.dotlane.dnsmasq.plist` (with `$HOME` expanded) |
| `files/caddy/Caddyfile` | `/opt/homebrew/etc/Caddyfile` (with `$HOME` expanded) |
| `files/caddy/sites/myapp.caddy` | `~/.config/caddy/sites/myapp.caddy` (written by `dotlane add`) |
| `files/resolver/dotlane` | `/etc/resolver/dotlane` (root-owned) |

## Support

Dotlane is free and open source. If it saves you time, you can buy me a coffee:

<a href="https://www.buymeacoffee.com/pedrodee" target="_blank"><img src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" alt="Buy Me a Coffee" height="60" width="217"></a>

## Licence

Apache-2.0 ([`LICENSE`](LICENSE), [`NOTICE`](NOTICE)), the same licence as Caddy, which the binary
embeds.
