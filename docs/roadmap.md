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

- **Linux.** systemd-resolved routing (with a fallback where it is missing), the CA trusted in
  the system store and NSS (Firefox, Chromium), ports 80/443 through `CAP_NET_BIND_SERVICE`, a
  systemd user unit. Tested on Ubuntu, Fedora and Arch. npm and other packages follow
  ([`npm/ROADMAP.md`](../npm/ROADMAP.md)).
- **Windows.** An NRPT rule (which needs the DNS responder on `127.0.0.1:53`), the Windows
  certificate store, a service, and checks for `http.sys` reservations. **WSL2:** documented
  paths for a browser on Windows and an app in WSL.
- **FrankenPHP** as an optional PHP runtime, downloaded and supervised rather than embedded
  (embedding needs CGO, which breaks cross-compilation).
- **Phones and other devices.** `bind = "lan"`, a LAN DNS answer or mDNS, or a tunnel, and a way
  to trust the CA on a phone.
- **A name-constrained CA**, limited to the configured endings ([`security.md`](security.md)).
- **Teams.** An organization's domain ending and shared site lists, set up in one command.
- **AI agents.** Stable URLs, `ls --json`, and an MCP-style endpoint that lists sites.

## Done

`v0.1.0` (2026-11-01), macOS on Apple Silicon and Intel. The full list is in the
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
- **Windows port 53:** require it, or keep a hosts-file fallback for locked-down machines?

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
