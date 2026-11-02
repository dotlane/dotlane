# Roadmap: core

What is planned for the core: the `dotlane` binary, its CLI and its daemon. The other products
have their own roadmaps: the [npm packages](../npm/ROADMAP.md), [Homebrew](homebrew.md), and the
website and desktop apps in their own repositories. The design decisions behind all of them are
in [`architecture.md`](architecture.md); the first plan, in phases, is in
[`history/`](history/README.md).

Features are grouped as **Now** (being worked on or decided next), **Next**, **Later** and
**Done**. Each says what it gives a user; a feature another product depends on says which.

## Now

- **Signed and notarized binaries.** Releases signed with an Apple Developer ID and notarized,
  so macOS accepts a binary downloaded with a browser, and Login Items shows Dotlane under the
  developer's name and keeps its approval across updates. Waits on one decision: joining the
  Apple Developer Program. The plan is in [`history/phase-2.md`](history/phase-2.md#p2-m2-signing-and-notarization--2-days-plus-apples-enrolment-time).
  npm and Homebrew ship the signed binary once it exists; the Homebrew cask then stops removing
  the quarantine attribute.

## Next

- **A stable, documented control API.** The daemon's JSON API on its Unix socket (`/v1/status`,
  `/v1/sites`) documented as a public, versioned contract, with breaking changes going to `/v2`.
  The desktop app (`dotlane/desktop`) needs it before its first version; editor integrations
  and scripts can then be clients of the daemon like the CLI.
- **PHP sites.** `dotlane add shop --php ~/code/shop/public`, or `php = "8.4"` and
  `root = "public"` in `dotlane.toml`: Caddy's `php_fastcgi` in front of PHP-FPM, one pool per
  PHP version, supervised by Dotlane, using the PHP versions already installed (Homebrew, mise).
  What Laravel Valet and Herd users expect, including per-site PHP versions.
- **Static sites.** `root = "dist"` serves a folder with no dev server.
- **Managed processes.** An optional `command` per site that Dotlane starts, restarts on a crash
  and logs, with the port injected (`{port}`). Never the default: a site still only needs a
  port.
- **The runtimes are done when** a Laravel app, a FastAPI app and a Vite app run side by side
  from one `dotlane.toml`, and a Herd user moves over in minutes.

## Later

- **More Linux and Windows channels.** Scoop and winget manifests, Fedora and Arch on CI, a
  NetworkManager-dnsmasq fallback where systemd-resolved is missing, and signed Windows binaries.
  What is left per OS is in [`compatibility.md`](compatibility.md#still-open).
- **FrankenPHP** as an optional PHP runtime, downloaded and supervised rather than embedded
  (embedding needs CGO, which breaks cross-compilation).
- **Phones and other devices.** `bind = "lan"`, a LAN DNS answer or mDNS, or a tunnel, and a way
  to trust the CA on a phone.
- **A name-constrained CA**, limited to the configured endings ([`security.md`](security.md)).
- **Teams.** An organization's domain ending and shared site lists, set up in one command.
- **AI agents.** Stable URLs, `ls --json`, and an MCP-style endpoint that lists sites.

## Done

**Linux and Windows** (in the next release; [`compatibility.md`](compatibility.md) tracks each
item):

- Linux: a systemd user unit, DNS through systemd-resolved on a link that carries only the
  domain endings, the CA in the system store and NSS (Firefox, Chromium), ports 80/443 through
  `ip_unprivileged_port_start`. Tested end to end on Ubuntu, Fedora and Arch.
- Windows: a Task Scheduler task, NRPT rules with DNS on `127.0.0.1:53`, the CA in the machine's
  Root store, every admin step in one UAC prompt.
- Releases for both (archives, zip, deb, rpm, Arch packages, npm), and CI end to end on Ubuntu
  and Windows. WSL2 setups documented in [`wsl.md`](wsl.md).

**`v0.1.0`** 
(2026-11-01), macOS on Apple Silicon and Intel. The full list is in the
[changelog](../CHANGELOG.md):

- One Go binary: a DNS responder, Caddy embedded with its own CA, and a control API on a Unix
  socket.
- `install` / `uninstall`, each password step explained; conflicts with other tools found before
  anything changes; `install --refresh` and the automatic refresh after an upgrade.
- `doctor [--fix | --json]`, `add` / `rm` / `ls [--json]`, `dotlane.toml` with `up` / `down`,
  `migrate` from the Bash prototype, and Dotlane's own pages for unknown names and stopped dev
  servers.
- Releases: archives, checksums and SBOMs for both CPUs, built by GoReleaser on a tag.

## Open decisions

- **Who first:** individual developers (free, open source), or teams, with paid features later?
- **PHP:** PHP-FPM only at first, or FrankenPHP from the start?

## Risks

- **portless** (Vercel Labs) moves fast and has distribution reach. Dotlane's difference must
  stay clear: any language, PHP, no wrapping by default, wildcard DNS for every tool, any domain
  ending.
- Every OS needs one-time admin steps; a confusing first run loses users. The installer and
  `doctor` are product, not plumbing.
- The trusted local CA is a liability if mishandled; name constraints and clear docs matter more
  as users grow.
- Windows networking (NRPT, port 53, `http.sys`, corporate policies, VPNs) is the least
  predictable part; prototype it first.
