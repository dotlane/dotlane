# Roadmap

Built from the gaps in [`product-notes.md`](product-notes.md), plus three questions: can Dotlane
ship on Homebrew and npm, can it serve PHP alongside Python, and how do configuration and a desktop
app work on macOS, Windows and Linux. Short answer to all three: **yes**, and they share one
prerequisite — a single native binary instead of a Bash script driving Caddy and dnsmasq.

Sizes are rough, for one developer, and assume part-time-to-full-time focus. Researched 2026-10-29.

## Where we are: Phase 0 — prototype (done)

macOS only. Bash `dotlane` CLI (add / rm / ls / doctor), Homebrew Caddy with its local CA,
dnsmasq on port 10053 as a user LaunchAgent, `/etc/resolver/traix`. Verified end to end with
MyApp (Vite + FastAPI), including live reload through the proxy.

## The one architectural decision: a single Go binary

Everything below gets easier if Dotlane becomes **one Go program** that embeds its parts instead of
installing and orchestrating them:

| Today | In the binary | Why it works |
| --- | --- | --- |
| Caddy (Homebrew service) | Caddy as a Go library, configured through its JSON API | Caddy is built to be embedded and extended (★78k, Apache-2.0) |
| dnsmasq (LaunchAgent) | A ~100-line DNS responder on `miekg/dns` | The job is one wildcard rule; `miekg/dns` (★8.8k) is the standard Go DNS library |
| `caddy trust` | Same trust-store code Caddy uses (`smallstep/truststore`) | Handles the macOS Keychain, Windows store, Linux stores and NSS (Firefox) |
| Bash `dotlane` | Go CLI talking to the daemon over a local socket | One code path for CLI, desktop app and editor integrations |

Results: one file to install per OS, no Homebrew dependency, no dnsmasq, cross-compilation to
macOS / Linux / Windows on amd64 and arm64, and a local API the desktop app can use.

Alternative considered: Rust (smaller binaries, pairs with Tauri). Rejected for the core because
Caddy — TLS automation, local CA, HTTP/3, WebSockets, FastCGI for PHP — would have to be rebuilt or
run as a separate process. Go keeps Caddy in-process.

## Feasibility of the three asks

### 1. Homebrew and npm packages — yes

- **Homebrew tap, from day one.** `brew install dotlane/tap/dotlane` from a `homebrew-tap` repo, with
  the formula pointing at release binaries. GoReleaser generates the formula on each release.
- **homebrew-core later** (`brew install dotlane`). Homebrew's acceptance policy requires notability:
  at least 30 forks, 30 watchers or 75 stars — and **90 / 90 / 225 when the repository owner
  submits it** — and a repository at least 30 days old. A milestone, not a blocker.
- **npm, the esbuild/biome way.** A small `dotlane` package with platform packages
  (`@dotlane/darwin-arm64`, `@dotlane/linux-x64`, `@dotlane/win32-x64`, …) as
  `optionalDependencies`; npm installs only the one matching the machine. Makes
  `npx dotlane add …` and `"devDependencies": { "dotlane": … }` work for JavaScript teams.
- `dotlane` was free on both registries when checked (2026-10-29). Reserve both names early.
- macOS binaries downloaded outside Homebrew need signing and notarization (Apple Developer ID,
  paid account) to pass Gatekeeper cleanly. Windows binaries benefit from code signing too.

### 2. PHP alongside Python — yes, and it differs by language

- **Python, Node, Go, Ruby, anything with its own server: already works.** Dotlane proxies to a
  port (that is how MyApp's FastAPI is reached). What can be added is *optional* process
  management: a `command` in the project config that Dotlane starts, restarts and logs — without
  making wrapping the default.
- **PHP is different:** most PHP apps are a folder, not a long-running server; something must run
  PHP per request. Two proven routes, both inside the Caddy world:
  - **PHP-FPM + Caddy's built-in `php_fastcgi`.** Same model as Laravel Valet/Herd. Dotlane points a
    site at a folder (`dotlane add shop --php ~/code/shop/public`) and at a PHP-FPM socket per PHP
    version. PHP itself comes from Homebrew (`shivammathur/php` tap for several versions) or mise.
    Dotlane manages the FPM pools.
  - **FrankenPHP** (★11.5k, a PHP app server *built on Caddy*). Modern, worker mode for Laravel and
    Symfony. Embedding it in the Dotlane binary means linking libphp (CGO), which complicates
    cross-compilation; better offered as an optional runtime Dotlane downloads and supervises.
- Recommendation: ship PHP-FPM first — it is what former Herd/Valet users expect, including
  per-site PHP versions — then FrankenPHP as an option. This makes Dotlane a direct, cross-platform
  replacement for Herd's core (local domains + HTTPS + PHP), which Herd itself does not offer on
  Linux.

### 3. Configuration and a desktop app on macOS, Windows and Linux — yes, with per-OS work

**Configuration** (same on every OS):

```toml
# ~/.config/dotlane/config.toml  — the machine (Windows: %APPDATA%\dotlane\config.toml)
tld = "traix"                # or several: tlds = ["traix", "test"]
bind = "loopback"            # "lan" to let phones and other devices in

# <project>/dotlane.toml       — committed, shared by the team
[sites.myapp]
port = 5173

[sites.api]
port = 8000

[sites.shop]
php = "8.4"
root = "public"

[sites.worker]               # optional process management
command = "uv run uvicorn app:app --port {port}"
```

`dotlane up` in a project registers its sites (and starts commands, if any); `dotlane down`
removes them. The CLI and the desktop app edit the same files through the daemon.

**What differs per OS:**

| Concern | macOS | Linux | Windows |
| --- | --- | --- | --- |
| Route `*.tld` to Dotlane's DNS | `/etc/resolver/<tld>` with a port (done) | systemd-resolved drop-in: `DNS=127.0.0.1:10053`, `Domains=~<tld>` (ports supported); NetworkManager/dnsmasq fallback | NRPT rule (`Add-DnsClientNrptRule -Namespace .<tld>`); takes server addresses, not ports — **the DNS responder must listen on 127.0.0.1:53** |
| Trust the local CA | System Keychain | system store + NSS for Firefox/Chromium | Windows certificate store |
| Ports 80/443 as a user | allowed | needs `CAP_NET_BIND_SERVICE` on the binary, or a lower `ip_unprivileged_port_start` | generally allowed; may clash with IIS / `http.sys` reservations |
| Run at login | launchd user agent | systemd user unit | Windows service or scheduled task |
| One-time admin steps | resolver file, CA trust | resolved drop-in, capability, CA trust | NRPT rule, CA trust, service install |
| Special cases | — | distributions without systemd-resolved | WSL2: Linux side resolves through Windows; browser on Windows, server in WSL |

**Desktop app:** a menu-bar / system-tray app listing sites, status, logs and toggles, talking to
the same daemon. Two good fits:

- **Wails** (★36.8k) — Go back end, web UI. Shares code and language with the core; one toolchain.
- **Tauri** (★112k) — Rust back end, web UI, very small bundles, mature tray support on all three
  OSes. Would talk to the Go daemon over its local API.

Recommendation: Wails, to stay in one language, unless the team is stronger in Rust.

## Phases

### Phase 1 — Go core on macOS (≈ 4–6 weeks)

Goal: replace the prototype with the single binary, same behaviour, better reliability.
Broken into milestones in [`phase-1.md`](phase-1.md).

- Daemon embedding Caddy + DNS responder; CLI over a Unix socket (`add`, `rm`, `ls --json`, `doctor`).
- `dotlane install` / `uninstall`: resolver file, CA trust, launchd agent — each admin prompt
  explained in one sentence.
- Conflict scan with a clear report: existing resolvers for the TLD, listeners on 80/443 and the
  DNS port, known tools (Herd, Valet, Yerd, Docker on 443); automatic DNS port selection.
- `doctor --fix`; configurable TLD(s); proxy bound to loopback by default; admin API not on TCP.
- `dotlane.toml` per project + `dotlane up` / `down`.
- Migration from the prototype (reads `~/.config/caddy/sites/*.caddy`).
- **Exit:** MyApp runs on it end to end; install → first site in under a minute on a clean Mac.

### Phase 2 — Distribution (≈ 1–2 weeks, overlaps Phase 1's end)

- GoReleaser: macOS / Linux / Windows × amd64 / arm64 binaries on GitHub Releases, checksums,
  SBOM.
- Homebrew tap; npm packages (main + platform packages); reserve the names now.
- macOS signing and notarization.
- Public repository, README with a 30-second demo, docs site.
- **Exit:** `brew install dotlane/tap/dotlane` and `npx dotlane` both work on a clean machine.

### Phase 3 — Runtimes: PHP and managed processes (≈ 3–5 weeks)

- PHP sites: folder roots, `php_fastcgi` to PHP-FPM, per-site PHP version, FPM pools supervised by
  Dotlane, detection of installed PHP versions (Homebrew, mise).
- Static sites (`root = "dist"`).
- Optional `command` sites: start, restart on crash, logs, `{port}` injection — Python, Node, Go.
- **Exit:** a Laravel app, a FastAPI app and a Vite app run side by side from one `dotlane.toml`.
  A former Herd user can move over in minutes.

### Phase 4 — Linux and Windows (≈ 5–8 weeks)

- Linux: systemd-resolved integration (plus fallback), NSS trust for Firefox/Chromium,
  `CAP_NET_BIND_SERVICE`, systemd user unit. Test on Ubuntu, Fedora, Arch.
- Windows: NRPT rule, DNS on 127.0.0.1:53, certificate store, service, `http.sys` checks.
- WSL2: documented paths for "browser on Windows, app in WSL".
- Distribution: Linux packages (deb/rpm) and Scoop/winget alongside npm.
- **Exit:** the Phase 3 exit scenario passes on all three OSes in CI.

### Phase 5 — Desktop app (≈ 4–6 weeks)

- Tray / menu-bar app (Wails): sites and their health, open in browser, start/stop commands, logs,
  install/repair wizard with the admin steps explained.
- Auto-update.
- **Exit:** a non-CLI user installs and adds a site without a terminal.

### Phase 6 — Teams and reach (ongoing)

- Organisation domain endings and shared site lists (e.g. everyone at Traix gets `*.traix` the same
  way, onboarding in one command).
- Phones and other devices: `bind = "lan"` plus mDNS or a LAN DNS answer, or a tunnel; serving the
  CA to phones for trust.
- Name-constrained local CA, limited to the configured TLDs (see `security.md`).
- homebrew-core submission once notability is met.
- AI-agent friendliness: stable URLs, `ls --json`, an MCP-style endpoint listing sites.

## Suggested order and what to cut

Phases 1 → 2 → 3 give a shippable, differentiated macOS product: the Herd replacement plus any
language behind HTTPS. Phase 4 is the most effort per user gained; Phase 5 can come before it if
the target is Mac developers first. If time is short, cut the desktop app before cross-platform —
the CLI and a good `doctor` cover most of it.

## Open decisions

- Target first: individual developers (free, open source) or teams (paid features in Phase 6)?
- Desktop framework: Wails (one language) or Tauri (smaller, larger ecosystem)?
- PHP: PHP-FPM only at first, or FrankenPHP from the start?
- Windows port 53: acceptable to require it, or keep a hosts-file fallback for locked-down machines?
- Licence: Apache-2.0 (like Caddy) or MIT for the open part?

## Risks

- **portless** (Vercel Labs) moves fast and has distribution reach; differentiation must stay clear
  (any language, PHP, no wrapping by default, wildcard DNS for every tool, branded TLDs).
- Each OS needs one-time admin steps; a confusing first run loses users — the installer and
  `doctor` are product, not plumbing.
- The trusted local CA is a security liability if mishandled; name constraints and clear docs
  matter more as users grow.
- Windows networking (NRPT, port 53, `http.sys`, corporate policies, VPNs) is the least
  predictable part; prototype it early in Phase 4.
