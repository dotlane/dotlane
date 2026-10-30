# Changelog

All notable changes to Dotlane are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

Every version gets its own section before it is tagged: changes collect under
**Unreleased** as they land, and cutting a release renames that heading to the version and date
([`docs/releasing.md`](docs/releasing.md)). The release workflow publishes the version's section as
the release notes, and refuses a tag that has none.

## [Unreleased]

The first release candidate, `0.1.0`, will contain everything below: Phase 1 of the
[roadmap](docs/roadmap.md), macOS only.

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

[Unreleased]: https://github.com/dotlane/dotlane/commits/main
