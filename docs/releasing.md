# Releasing

How a Dotlane release is made. The pipeline is GoReleaser ([`.goreleaser.yaml`](../.goreleaser.yaml))
in [`.github/workflows/release.yml`](../.github/workflows/release.yml), planned in
[`phase-2.md`](phase-2.md) (P2-M1). Signing and notarization (P2-M2), the Homebrew tap (P2-M3)
and npm (P2-M4) join it later.

## What a release contains

For each Mac architecture (`arm64`, `amd64`):

- `dotlane_<version>_darwin_<arch>.tar.gz`: the binary, `LICENSE`, `NOTICE` and `README.md`;
- `dotlane_<version>_darwin_<arch>.tar.gz.sbom.json`: an SPDX SBOM of everything compiled in
  (Syft);

plus `checksums.txt` (SHA-256 of every file). The release notes are the version's section of
[`CHANGELOG.md`](../CHANGELOG.md).

Binaries are built with `CGO_ENABLED=0 -trimpath`, stamped with the version, short commit and
commit date (`dotlane version` prints them), and timestamped with the commit, so building the
same commit twice gives the same bytes (checked 2026-10-30).

## Cutting a release

1. `main` is green in CI, and the version's changes are in the docs.
2. In `CHANGELOG.md`, rename `## [Unreleased]` to `## [0.1.0] - <date>`, start a new empty
   `## [Unreleased]` above it, and update the links at the bottom. Commit and push that. A
   pre-release tag (`v0.1.0-rc.1`) uses its own section if it has one, else its version's.
   Check with `scripts/changelog-section.sh v0.1.0`.
3. Tag and push:

   ```bash
   git tag -a v0.1.0-rc.1 -m "v0.1.0-rc.1"
   git push origin v0.1.0-rc.1
   ```

4. The *Release* workflow first checks that the version has a changelog section (and stops if
   not), then runs the full test suite (unit tests, `govulncheck`, the integration
   test on a macOS runner), then GoReleaser builds and uploads everything to a **draft** release.
   Tags with a suffix (`-rc.1`) are marked as pre-releases.
5. Review the draft: the files are all there, `checksums.txt` verifies
   (`shasum -a 256 -c checksums.txt` after downloading), the notes read well. Then publish it.

A tag that fails the tests produces no release; delete the tag (`git push --delete origin <tag>`),
fix, and tag again.

## Dry run

Every pull request runs `goreleaser check` and a snapshot build (the *release-snapshot* job in
`ci.yml`), verifies the checksums and archive contents, and keeps the files as a workflow
artifact for seven days. Locally (on macOS, `go install` builds the same versions from source):

```bash
go install github.com/goreleaser/goreleaser/v2@v2.19.0
go install github.com/anchore/syft/cmd/syft@v1.55.0
goreleaser release --snapshot --clean    # into dist/ (ignored by git)
```

In CI, `scripts/install-release-tools.sh` downloads the tools' prebuilt Linux releases and
checks each against a SHA-256 pinned in the script, rather than using third-party Actions
([`security-guide.md`](security-guide.md), workflow rules) or compiling them (which took over
six minutes per run). Updating a tool means changing its version and checksum there, and the
version here.
