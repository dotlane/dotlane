# Changelog

All notable changes to Dotlane are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

Every version gets its own section before it is tagged: changes collect under
**Unreleased** as they land, and cutting a release renames that heading to the version and date
([`docs/releasing.md`](docs/releasing.md)). The release workflow publishes the version's section as
the release notes, and refuses a tag that has none.

## [Unreleased]

## [0.1.0] - 2026-11-01

The first public release: macOS only (Apple Silicon and Intel), installed with Homebrew or npm,
or built from source. Phases 1 and 2 of the [roadmap](docs/roadmap.md).

### Added

- `dotlane`, one Go binary: a daemon with a DNS responder for the domain ending, Caddy embedded
  as a library with its own certificate authority, and a control API on a Unix socket.
- `install` / `uninstall [--purge]`: a login service, the CA trusted in the System keychain and
  `/etc/resolver` routing, each step explained before it asks for a password; conflicts with
  other tools are detected and named before anything changes.
- `install --refresh`: updates an installed service to a new binary without a password. After an
  upgrade through Homebrew or npm, the next `add`, `rm`, `ls`, `up` or `down` does the same on
  its own, since package managers replace the binary but cannot restart the service; only for a
  newer release, never a downgrade or a development build. `DOTLANE_NO_AUTO_REFRESH=1` turns it
  off.
- `doctor [--fix | --json]`: checks every piece, names what is in the way and repairs what
  `install` can.
- `add`, `rm`, `ls [--json]`, and per-project sites in `dotlane.toml` with `up` / `down`.
- `migrate`: moves a Bash-prototype install to Dotlane, keeping its sites and its trusted CA.
- Pages instead of browser errors for a name with no site and for a dev server that is down.
- **Homebrew:** `brew install dotlane/tap/dotlane`, a cask built with each release and
  published to the `dotlane/homebrew-tap` tap when the release is.
- **npm:** `npm install -g dotlane` or `npx dotlane`: a Node launcher with the binary in
  `@dotlane/darwin-arm64` or `@dotlane/darwin-x64`, with no install scripts.
- Releases: archives for macOS arm64 and amd64, SHA-256 checksums and SPDX SBOMs, with the cask
  and the npm packages built from the same binaries.
- `NOTICE` lists every module the binary embeds, with its licence, and carries their own
  notices; it ships in every archive and npm package.
- Docs: install with Homebrew, npm or source ([`docs/setup.md`](docs/setup.md)), the command
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
- Binaries are not yet signed or notarized by Apple; the Homebrew cask removes macOS's
  quarantine attribute instead, and npm downloads are not quarantined.

[Unreleased]: https://github.com/dotlane/dotlane/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/dotlane/dotlane/releases/tag/v0.1.0
