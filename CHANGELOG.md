# Changelog

All notable changes to Dotlane are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

Every version gets its own section before it is tagged. Until then, each change waits as a
fragment in [`changes/`](changes/README.md), so pull requests never edit this file; cutting a
release collects the fragments into the version's section ([`docs/releasing.md`](docs/releasing.md)).
The release workflow publishes that section as the release notes, and refuses a tag that has none.

## [0.3.0] - 2026-11-03

Domain endings from the CLI, with `.dotlane` as the new default, and safer upgrades: the CLI and
the service check that they match, and commands take turns with the login service.

**Upgrading from 0.2.0 without an ending set in `config.toml`:** the service moves your sites to
`.dotlane` on its own, but routing a new ending needs your password, so run `dotlane install`
once. `dotlane doctor` then shows the old ending's route; remove it with `dotlane tld rm <ending>`,
or keep that ending as the primary with `dotlane tld use <ending>`.

### Added

- **A way to support Dotlane**: a Buy Me a Coffee link in the README, in `npm fund` and on the
  GitHub Sponsor button, and one dimmed line at the end of `dotlane install` (never on
  `--refresh` or any other command).
- **Warnings about risky domain endings.** Every command warns when `config.toml` sets an ending
  that is a public top-level domain (`.dev`, `.com`, from an embedded copy of IANA's list), a
  two-letter ending, a special-use name (`.localhost`, `.invalid`, `.onion`, `.alt`, `.arpa`)
  or one routers often answer (`.lan`, `.home`, `.corp`). The ending keeps working.
- **`dotlane tld ls | add | rm | use`**: list the domain endings, add one, remove one and its
  route, or choose the primary. Each change is saved in `config.toml` and, when Dotlane is
  installed, applied at once: the DNS routes follow and the service restarts. Endings that would
  hide real websites are refused; ones a router may already answer need `--force`.
- **`dotlane install --tld <ending>`** makes an ending the primary before installing.
- **`doctor`** warns about a risky ending and about a route left for an ending no longer
  configured.
- **`primary_tld` in `GET /v1/status`**: the first of the configured endings, whose URL comes
  first in each site's `urls`.
- **`api` in `GET /v1/status`**: the version of the control API the daemon serves. Every command
  that uses the daemon checks it first, and stops with what to update when this `dotlane` and the
  service do not match: a service that was not refreshed, or an older command left on the PATH
  (another Node version, a project's `npx`). The command gets the update command for how it was
  installed (npm, npx, pnpm, yarn, bun or bunx, globally, in a project or a workspace, Volta,
  Homebrew, `go install` or a clone), in a form any shell runs, Windows PowerShell 5.1 included.
  A command older than the service, but compatible, warns.

### Changed

- **The default domain ending is now `.dotlane`**: `https://myapp.dotlane`.

### Fixed

- **`uninstall` on macOS leaving `/etc/resolver` files behind** for endings removed from
  `config.toml` after installing: it now removes every resolver file of Dotlane's, configured or
  not. Linux and Windows already removed all of theirs.
- **Commands started together after an upgrade restarting the service over each other**: they now
  take turns, and the ones that wait find it already updated. `install`, `doctor --fix` and
  `uninstall` take their turn too, so an automatic update no longer restarts the service in the
  middle of one, or puts back a service `uninstall` is removing.

## [0.2.0] - 2026-11-02

Linux and Windows join macOS, each on x64 and arm64: installed with npm, from the release's
archives, or on Linux with a deb, rpm or Arch package. Where each OS stands, item by item, is in
[`docs/compatibility.md`](docs/compatibility.md).

### Added

- **Linux** (x64 and arm64): `install`, `uninstall`, `doctor`, `install --refresh` and the
  automatic upgrade. A systemd user unit runs the service; a system unit gives systemd-resolved
  a link that sends only the domain endings to Dotlane; the CA is trusted in the system store and,
  with `certutil`, in NSS for Firefox and Chromium; `/etc/sysctl.d/50-dotlane-<service>.conf`
  lets the service listen on ports 80 and 443. Needs systemd-resolved. Tested on Ubuntu, Fedora
  and Arch.
- **Windows** (x64 and arm64): the same commands. A Task Scheduler task runs the service; NRPT
  rules send the domain endings to Dotlane's DNS on `127.0.0.1:53`; the CA is trusted in the
  machine's Root store. Every step that needs administrator rights runs in one elevated
  PowerShell, so one UAC prompt.
- Release archives for Linux (tar.gz, deb, rpm, Arch) and Windows (zip), and the npm packages
  `@dotlane/linux-x64`, `@dotlane/linux-arm64`, `@dotlane/win32-x64` and `@dotlane/win32-arm64`.
- [`docs/compatibility.md`](docs/compatibility.md), where each OS stands item by item, and setup
  pages for [Linux](docs/setup-linux.md), [Windows](docs/setup-windows.md) and
  [WSL2](docs/wsl.md).

### Changed

- On Linux, state lives in `$XDG_STATE_HOME/dotlane` (`~/.local/state/dotlane`), apart from the
  config; on Windows, in `%LocalAppData%\dotlane`, with the config in `%AppData%\dotlane`. macOS
  is unchanged.
- Ports 80 and 443 are bound on every interface only on macOS, which requires it; Linux and
  Windows bind them on loopback.

## [0.1.0] - 2026-11-01

The first public release: macOS only (Apple Silicon and Intel), installed with npm or built from
source; Homebrew is coming soon. Phases 1 and 2 of the [roadmap](docs/roadmap.md).

### Added

- `dotlane`, one Go binary: a daemon with a DNS responder for the domain ending, Caddy embedded
  as a library with its own certificate authority, and a control API on a Unix socket.
- `install` / `uninstall [--purge]`: a login service, the CA trusted in the System keychain and
  `/etc/resolver` routing, each step explained before it asks for a password; conflicts with
  other tools are detected and named before anything changes.
- `install --refresh`: updates an installed service to a new binary without a password. After an
  upgrade through npm (or Homebrew, once available), the next `add`, `rm`, `ls`, `up` or `down`
  does the same on its own, since package managers replace the binary but cannot restart the
  service; only for a newer release, never a downgrade or a development build.
  `DOTLANE_NO_AUTO_REFRESH=1` turns it off.
- `doctor [--fix | --json]`: checks every piece, names what is in the way and repairs what
  `install` can.
- `add`, `rm`, `ls [--json]`, and per-project sites in `dotlane.toml` with `up` / `down`.
- `migrate`: moves a Bash-prototype install to Dotlane, keeping its sites and its trusted CA.
- Pages instead of browser errors for a name with no site and for a dev server that is down.
- **npm:** `npm install -g dotlane` or `npx dotlane`: a Node launcher with the binary in
  `@dotlane/darwin-arm64` or `@dotlane/darwin-x64`, with no install scripts.
- **Homebrew (coming soon):** each release already builds a cask for
  `brew install dotlane/tap/dotlane`; the `dotlane/homebrew-tap` tap is published after launch.
- Releases: archives for macOS arm64 and amd64, SHA-256 checksums and SPDX SBOMs, with the cask
  and the npm packages built from the same binaries.
- `NOTICE` lists every module the binary embeds, with its licence, and carries their own
  notices; it ships in every archive and npm package.
- Docs: install with npm or source ([`docs/setup.md`](docs/setup.md)), the command
  reference, how it works, troubleshooting; `CONTRIBUTING.md` and issue forms that ask for
  `dotlane doctor --json`; `SECURITY.md` says where the CA's key lives and what to do if it
  leaks.

### Security

- Ports 80 and 443 listen on all interfaces (macOS allows a normal user nothing else) and drop
  every request that does not come from this Mac.
- The login service runs a copy of the binary outside the folders macOS protects.
- The CA's keys, the state folder and the control socket are readable by their owner only;
  `doctor` checks them and `doctor --fix` tightens them.
- Dependencies are at versions without known vulnerabilities in code Dotlane calls
  (`govulncheck` in CI). Three `go-jose/v3` advisories reach the binary through Caddy's PKI
  module, in code Dotlane never runs; why, and when it goes, is in
  [`docs/security-guide.md`](docs/security-guide.md).
- Binaries are not yet signed or notarized by Apple. npm downloads are not quarantined, so macOS
  runs them as they are; the Homebrew cask will remove the quarantine attribute.

[0.3.0]: https://github.com/dotlane/dotlane/releases/tag/v0.3.0
[0.2.0]: https://github.com/dotlane/dotlane/releases/tag/v0.2.0
[0.1.0]: https://github.com/dotlane/dotlane/releases/tag/v0.1.0
