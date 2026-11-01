# Changelog

All notable changes to Dotlane are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

Every version gets its own section before it is tagged: changes collect under
**Unreleased** as they land, and cutting a release renames that heading to the version and date
([`docs/releasing.md`](docs/releasing.md)). The release workflow publishes the version's section as
the release notes, and refuses a tag that has none.

## [Unreleased]

### Added

- npm packages: `dotlane` (a Node launcher) with the binary in `@dotlane/darwin-arm64` or
  `@dotlane/darwin-x64`, built from each release and attached to it. `0.1.0-rc.2` is on npm under
  the `next` tag (`npx dotlane@next`).
- A Homebrew cask for `brew install dotlane/tap/dotlane`, built with each release and published
  to the `dotlane/homebrew-tap` tap when the release is.

### Changed

- Help, docs and examples use a neutral app name (`myapp`).
- After an upgrade through Homebrew or npm, the next `add`, `rm`, `ls`, `up` or `down` restarts
  the service on the new version, as `dotlane install --refresh` does: package managers replace
  the binary but cannot restart the service. Only for a newer release, never a downgrade or a
  development build; `DOTLANE_NO_AUTO_REFRESH=1` turns it off.

### Security

- `golang.org/x/net` v0.62.0, fixing two vulnerabilities in its `html` package (GO-2026-6860,
  GO-2026-6866), in code Dotlane does not call.

## [0.1.0] - 2026-10-30

The first release: Phase 1 of the [roadmap](docs/roadmap.md), macOS only (Apple Silicon and
Intel), built from source or downloaded from the release.

### Added

- `dotlane`, one Go binary: a daemon with a DNS responder for the domain ending, Caddy embedded
  as a library with its own certificate authority, and a control API on a Unix socket.
- `install` / `uninstall [--purge]`: a login service, the CA trusted in the System keychain and
  `/etc/resolver` routing, each step explained before it asks for a password; conflicts with
  other tools are detected and named before anything changes.
- `install --refresh`: updates an installed service to a new binary without a password.
- `doctor [--fix | --json]`: checks every piece, names what is in the way and repairs what
  `install` can.
- `add`, `rm`, `ls [--json]`, and per-project sites in `dotlane.toml` with `up` / `down`.
- `migrate`: moves a Bash-prototype install to Dotlane, keeping its sites and its trusted CA.
- Pages instead of browser errors for a name with no site and for a dev server that is down.
- Release pipeline: archives for macOS arm64 and amd64, SHA-256 checksums and SPDX SBOMs.

### Security

- Ports 80 and 443 listen on all interfaces (macOS allows a normal user nothing else) and drop
  every request that does not come from this Mac.
- The login service runs a copy of the binary outside the folders macOS protects.
- `golang.org/x/net` v0.60.0, fixing five HTTP/2 vulnerabilities (GO-2026-6603, -6610, -6611,
  -6612, -6617).
- `github.com/go-jose/go-jose/v4` v4.1.6 (a security update from Dependabot, in code Dotlane does
  not call).

[Unreleased]: https://github.com/dotlane/dotlane/compare/v0.1.0-rc.2...HEAD
[0.1.0]: https://github.com/dotlane/dotlane/releases/tag/v0.1.0-rc.2
