# Phase 2: Distribution

Plan, written 2026-10-30, for getting Dotlane from "build it from source" to
`brew install …/dotlane` and `npx dotlane` on a clean Mac, with signed binaries. It follows Phase 2
of the [roadmap](roadmap.md) (≈ 1–2 weeks) and builds on Phase 1 ([`phase-1.md`](phase-1.md)).

## Where we start

- The binary already cross-compiles everywhere Phase 4 needs (checked 2026-10-30, `CGO_ENABLED=0`,
  `-trimpath -ldflags "-s -w"`):

  | Target | Size | | Target | Size |
  | --- | --- | --- | --- | --- |
  | darwin/arm64 | 45 MB (15.7 MB gzipped) | | linux/arm64 | 43 MB |
  | darwin/amd64 | 48 MB | | windows/amd64 | 48 MB |
  | linux/amd64 | 47 MB | | windows/arm64 | 44 MB |

  Only `install`, `uninstall`, `doctor` and `migrate` are macOS-only (`internal/install`); the
  daemon, CLI and DNS build on all six.
- `main.version` is set with `-ldflags "-X main.version=…"` and shown by `dotlane version`.
- `dotlane install` copies the binary to `~/Library/Application Support/dotlane/bin/` and the
  login service runs that copy. This matters for packages: the service keeps working when
  Homebrew moves its files on upgrade, and `npx` can install from a temporary cache.
- Licence Apache-2.0, `LICENSE` and `NOTICE` in the repository. Name `dotlane` free on npm and
  Homebrew when last checked (2026-10-29).

## Decisions before starting

| Decision | Options | Recommendation |
| --- | --- | --- |
| Tap and npm owner | **Decided 2026-10-30: the `dotlane` organization.** The repository is `github.com/dotlane/dotlane` (Go module path too), the tap `dotlane/homebrew-tap` (`brew install dotlane/tap/dotlane`), the npm scope `@dotlane`. `traix` was not available (an unrelated personal account). | Done before any release, since moving a tap or module path later breaks every install command. |
| Platforms shipped | macOS only, or all six now | macOS (arm64 and amd64) only. Linux and Windows builds stay in CI so they keep compiling, but are not published until Phase 4 makes `install` work there. |
| Apple Developer Program | join now ($99/year), or ship unsigned first | Join now: enrolment can take days, and signing is what keeps background-item approvals stable across updates (below). |
| Repository visibility | public at the first release, or after | Public at `v0.1.0`; release candidates can come from the private repo. |

## Milestones

### P2-M1: Release pipeline with GoReleaser (≈ 2 days)

- `.goreleaser.yaml` (GoReleaser v2, version pinned in the workflow):
  - builds: `darwin` × `arm64`, `amd64`, `CGO_ENABLED=0`, `-trimpath`, ldflags
    `-s -w -X main.version={{.Version}}`;
  - archives: `dotlane_<version>_<os>_<arch>.tar.gz` holding the binary, `LICENSE`, `NOTICE`
    and `README.md`;
  - `checksums.txt` (SHA-256), an SBOM per archive (Syft), a changelog from commit subjects.
- `.github/workflows/release.yml`: on a `v*` tag, run the unit tests and the integration test, then
  `goreleaser release`. On every pull request: `goreleaser check` and a `--snapshot` build, so the
  config cannot rot between releases.
- `dotlane version` prints version, commit and date (also set by ldflags).
- **Done when** tagging `v0.1.0-rc.1` produces a GitHub release whose archives match
  `checksums.txt`, and the downloaded binary passes `scripts/integration.sh`.

### P2-M2: Signing and notarization (≈ 2 days, plus Apple's enrolment time)

- A Developer ID Application certificate and an App Store Connect API key, stored as repository
  secrets (`.p12` with its password, key ID, issuer ID, private key).
- GoReleaser's `notarize` step signs each macOS binary with the hardened runtime and submits it to
  Apple's notary service. It runs on a Linux runner (Anchore's Quill), so no Mac or keychain is needed
  in CI.
- A bare executable cannot have the notarization ticket stapled to it, as only bundles, `.dmg` and
  `.pkg` files can. Gatekeeper fetches the ticket online at first launch, so the first run after a
  browser download needs the network. Homebrew and npm downloads are not quarantined, so they are
  unaffected.
- **Why it matters beyond Gatekeeper:** with an ad-hoc signature, every build is a new identity
  to macOS. Phase 1 found that a launchd job started from `~/Documents` waits for a privacy approval
  each time the binary changes. Developer ID signing gives Dotlane one stable identity: Login Items
  shows the developer's name instead of a bare executable, and approvals survive updates.
- **Done when** `codesign -dv --verbose=4` shows the Developer ID and hardened runtime,
  `spctl --assess --type execute -vv` accepts a binary downloaded with a browser, and Login Items
  lists Dotlane under the developer's name.

### P2-M3: Homebrew tap (≈ 2 days)

- The `dotlane/homebrew-tap` repository. GoReleaser updates it on each release
  (`homebrew_casks`, which recent GoReleaser versions generate in place of formulas; the cask
  installs the binary onto the PATH).
- **Upgrades.** `brew upgrade` replaces the binary on the PATH, not the copy the service runs.
  The fix is `dotlane install --refresh` (built 2026-10-30, ahead of this milestone, with unit
  tests and an integration step): when Dotlane is installed, it updates the service's copy and
  restarts it, never asks for a password, and does nothing otherwise. The cask runs it after
  install and upgrade. Until it runs, `doctor` already warns that the service runs an older
  copy.
- **Removal.** `brew uninstall` does not undo `dotlane install`, and the service would keep
  running from its copy. Caveats tell users to run `dotlane uninstall` first; the cask's `zap`
  removes the state directory and log for `brew uninstall --zap`.
- Caveats on first install: run `dotlane install`, which explains each of its two password steps.
- **Done when** on a Mac without Dotlane, `brew install dotlane/tap/dotlane && dotlane install &&
  dotlane add …` serves a site; `brew upgrade` to a newer release restarts the daemon into it
  (checked the way the integration test checks a reinstall); `dotlane uninstall && brew uninstall
  --zap` leaves nothing behind.

### P2-M4: npm packages (≈ 3 days)

The esbuild/Biome pattern, with no install scripts:

- `dotlane`: a small package whose `bin` is a Node shim, plus the platform packages as
  `optionalDependencies` in lockstep versions.
- `@dotlane/darwin-arm64` and `@dotlane/darwin-x64`, each holding one binary, with `os` and `cpu`
  fields so npm installs only the matching one (about 16 MB compressed each).
- The shim resolves the platform package with `require.resolve` and `spawnSync`s the binary with
  inherited stdio. It passes the exit code and signals through, and on an unsupported platform it
  says which platforms are supported.
- Published from GitHub Actions with npm's trusted publishing (OIDC, no long-lived token) and
  `--provenance`, in the same workflow as the GitHub release, so all three channels carry the
  same version.
- `npx dotlane install` works because install copies the binary out of npx's cache.
- **Done when** on a clean Mac, `npx dotlane install` then `npx dotlane add …` serves a site, and
  a project with `"devDependencies": {"dotlane": "…"}` runs `npx dotlane up` from its
  `dotlane.toml`.

### P2-M5: Public release (≈ 2 days)

- Before the repository goes public: the fresh-history procedure and checklist in
  [`security-guide.md`](security-guide.md#before-the-tools-repository-goes-public), the
  organization and repository settings there, and `NOTICE` covering Caddy and the other embedded
  modules (from `go-licenses`).
- The website (its own repository, `dotlane/website`) published; the README gets a 30-second terminal recording (VHS, which
  scripts it so it can be re-recorded each release).
- `SECURITY.md` covers where the CA key lives and how to report a vulnerability. Also
  `CONTRIBUTING.md`, plus issue templates that ask for `dotlane doctor --json`.
- Install instructions in `setup.md`, the README and the site switch from "build from source" to
  Homebrew and npm, keeping the source build as a third option.
- Reserve the names before announcing: npm `dotlane` and the `@dotlane` scope (an npm organization
  named `dotlane`) and the tap repository. The GitHub org exists (2026-10-30).
- **Done when** `v0.1.0` is public on GitHub Releases, the tap and npm, and the site is live.

## Exit criteria (from the roadmap)

`brew install dotlane/tap/dotlane` and `npx dotlane` both work on a clean machine. Checked on a
fresh macOS runner, and on a new user account on a real Mac (which also exercises Gatekeeper and
Login Items): each channel goes from nothing to `https://<name>.traix` serving a dev server, then
upgrades and uninstalls cleanly.

## Risks

- **Apple enrolment and notarization delays.** Enrolment takes days; notary submissions
  usually take minutes but can queue. Start enrolment first; M1, M3 and M4 do not depend on it.
- **Quarantine on the cask.** If signing is not ready when the tap ships, a browser-free
  `brew install` still works, but a cask may need `--no-quarantine`. Ship the tap after M2, or
  document the flag for release candidates only.
- **Package size.** About 16 MB compressed per platform, fine for npm and Homebrew. Most of it
  is Caddy; trimming modules further is possible but not needed for Phase 2.
- **Two install locations.** The PATH binary (Homebrew or npm) and the service's copy can differ
  until `--refresh` runs. `doctor` reports it, and the integration test covers the upgrade.

## Out of scope

Linux and Windows `install` (Phase 4), homebrew-core (needs the notability Homebrew requires, see
the [roadmap](roadmap.md)), the menu-bar app and an app bundle with `SMAppService` (Phase 5), and
auto-update.
