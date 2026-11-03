# Releasing

How a Dotlane release is made. The pipeline is GoReleaser ([`.goreleaser.yaml`](../.goreleaser.yaml))
in [`.github/workflows/release.yml`](../.github/workflows/release.yml), planned in
[`history/phase-2.md`](history/phase-2.md) (P2-M1). Each release also builds the Homebrew cask
([`homebrew.md`](homebrew.md)) and the npm packages ([`npm/ROADMAP.md`](../npm/ROADMAP.md));
signing and notarization join it later ([core roadmap](roadmap.md#now)). How the channels fit
together, in a diagram: [`distribution.md`](distribution.md).

## What a release contains

For macOS, Linux and Windows, each on `arm64` and `amd64`:

- `dotlane_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows, with `dotlane.exe`): the binary,
  `LICENSE`, `NOTICE` and `README.md`;
- the same name plus `.sbom.json`: an SPDX SBOM of everything compiled in (Syft);
- for Linux, `.deb`, `.rpm` and `.pkg.tar.zst` packages that put the binary in `/usr/bin`, with no
  scripts (`dotlane install` stays a per-user step).
  Every merge group installs the snapshot's packages on Ubuntu, Fedora and Arch, runs the
  end-to-end test from `/usr/bin/dotlane`, and removes them again (CI's `linux-packages`).

macOS is one GoReleaser build and Linux with Windows another (`dotlane-linux-windows`), so the
Homebrew cask, which picks archives by build, stays macOS-only.

plus `checksums.txt` (SHA-256 of every file). The release notes are the version's section of
[`CHANGELOG.md`](../CHANGELOG.md).

Binaries are built with `CGO_ENABLED=0 -trimpath`, stamped with the version, short commit and
commit date (`dotlane version` prints them), and timestamped with the commit, so building the
same commit twice gives the same bytes (checked 2026-10-30).

## Cutting a release

Releases are tags on `main`; which version number to pick, patches to older versions, and what
CI runs at each step are in [`branching.md`](branching.md).

1. **A candidate.** `main` is green and the version's changes are in the docs. Check the notes
   with `scripts/changelog.sh preview`, then tag the commit and push:

   ```bash
   git fetch origin
   git tag -a v0.1.0-rc.1 -m "v0.1.0-rc.1" origin/main
   git push origin v0.1.0-rc.1
   ```

   A pre-release tag without a changelog section takes its notes from the fragments
   (`scripts/changelog-section.sh v0.1.0-rc.1` shows them). A fix the candidate needs is an
   ordinary pull request into `main`, then `-rc.2`. A patch release may skip the candidate.
2. **The release commit.** On a branch from `origin/main`, run
   `scripts/changelog.sh release v0.1.0`: it writes `## [0.1.0] - <date>` with every fragment
   into `CHANGELOG.md`, adds the version's link at the bottom, and deletes the fragments. Edit
   the section if it needs an introduction or reordering, then open the pull request:
   `gh pr create --title "Release v0.1.0"` and `gh pr merge --squash --auto`. It is docs only, so
   the queue merges it without running the suites again.
3. **Tag it** once merged, on the release commit itself (not a later one):

   ```bash
   git fetch origin
   git tag -a v0.1.0 -m "v0.1.0" "$(git log -1 --format=%H --grep='^Release v0.1.0' origin/main)"
   git push origin v0.1.0
   ```

4. The *Release* workflow first checks that the version has a changelog section (and stops if
   not), then runs the full test suite (unit tests, `govulncheck`, the integration
   test on a macOS runner), then GoReleaser builds and uploads everything to a **draft** release.
   Tags with a suffix (`-rc.1`) are marked as pre-releases. The same job then **publishes the npm
   packages** (when `NPM_PUBLISH` is on, see [npm](#npm)): a release goes out under `latest`, a
   pre-release under `next`, before anyone reviews the draft. npm users get it from that moment,
   and a version once published cannot be published again, so a broken one is fixed with a new
   tag.
5. Review the draft: the files are all there, `checksums.txt` verifies
   (`shasum -a 256 -c checksums.txt` after downloading), the notes read well. Then publish it.
   Publishing makes the GitHub release public and runs the Homebrew tap update; npm already has
   the version.

A tag that fails the tests produces no release; delete the tag (`git push --delete origin <tag>`),
fix through a pull request into `main`, and tag again.

## Dry run

Every merge group that changes code runs a snapshot build (the *release-snapshot* job in
`ci.yml`; every pull request also runs `goreleaser check`, in `scripts/check.sh`), verifies the checksums and archive contents, and keeps the files as a workflow
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

## Homebrew

Every release also writes a cask, `dotlane.rb` (GoReleaser's `homebrew_casks` in
[`.goreleaser.yaml`](../.goreleaser.yaml)), and attaches it to the draft. **Publishing** the
draft runs [`tap.yml`](../.github/workflows/tap.yml), which commits it to
[`dotlane/homebrew-tap`](https://github.com/dotlane/homebrew-tap) as `Casks/dotlane.rb`
([`scripts/tap-update.sh`](../scripts/tap-update.sh)). Not earlier, because the cask's URLs point
at the release's files, which cannot be downloaded while it is a draft; and not for
pre-releases. Users then get it with `brew install dotlane/tap/dotlane` or `brew upgrade`.

What the cask does:

- Puts `dotlane` on the PATH and shows caveats: run `dotlane install` once, and `dotlane
  uninstall` before `brew uninstall`.
- Removes macOS's quarantine attribute from the binary (`postflight_steps`), which an unsigned
  download needs. Remove this once releases are signed and notarized (P2-M2).
- `brew uninstall --zap` also trashes `~/Library/Application Support/dotlane`,
  `~/Library/Logs/dotlane` and `~/.config/dotlane`.
- **Upgrades:** the cask does not restart the service. Homebrew runs cask hooks in a sandbox with
  a temporary home, and its unsandboxed Ruby hooks are deprecated (checked with Homebrew 7.0.11,
  2026-10-31). Instead, the next `add`, `rm`, `ls`, `up` or `down` finds the service running an
  older release and updates it, as `dotlane install --refresh` does (no password). npm upgrades
  get the same, since its packages have no install scripts.

Tests: every merge group installs the snapshot's cask from a local tap on a macOS runner, sets
Dotlane up, serves a site, upgrades to a second build, and removes everything
([`scripts/brew-e2e.sh`](../scripts/brew-e2e.sh), the *homebrew* job in `ci.yml`).

**Publishing to the tap is off** until these one-time steps are done:

1. **The repository:** `dotlane/homebrew-tap`, private until the tool goes public (Homebrew cannot
   download a private repository's release files).
2. **The deploy key**, which can write to the tap and nothing else. The organization must allow
   deploy keys (Settings → Repository → Deploy keys, or
   `gh api -X PATCH orgs/dotlane -F deploy_keys_enabled_for_repositories=true`); otherwise
   adding it is refused. Each command runs only if the one before it worked, so the secret is
   replaced only by a key that can read the tap:

   ```bash
   d=$(mktemp -d) && cd "$d" && \
   ssh-keygen -q -t ed25519 -N "" -C "dotlane release → homebrew-tap" -f tap-key && \
   gh repo deploy-key add tap-key.pub -R dotlane/homebrew-tap --allow-write --title "dotlane release" && \
   GIT_SSH_COMMAND="ssh -F /dev/null -i $d/tap-key -o IdentitiesOnly=yes -o IdentityAgent=none -o UserKnownHostsFile=$HOME/.ssh/known_hosts" \
     git ls-remote git@github.com:dotlane/homebrew-tap.git && \
   gh secret set HOMEBREW_TAP_DEPLOY_KEY -R dotlane/dotlane --env release < tap-key; \
   cd ~ && rm -rf "$d"
   ```

   `-F /dev/null` and `IdentityAgent=none` make SSH offer only the new key. Without them, a key
   from `~/.ssh/config` or the agent can log in first as another GitHub account, and the check
   fails with "Repository not found" although the deploy key is fine. If the check fails, the
   secret is left as it was; delete the new deploy key on the tap (its private half is gone) and
   start over.

3. **When the tool goes public:** make the tap public, and set the repository variable
   `HOMEBREW_PUBLISH` to `true`. The next published release fills the tap.

By hand (for example if the workflow failed): `scripts/tap-update.sh v0.1.0`, with SSH access to
the tap.

## npm

Every release also builds the npm packages from its own binaries
([`scripts/npm-build.mjs`](../scripts/npm-build.mjs)) and attaches their tarballs to the draft:

| Package | Holds |
| --- | --- |
| `dotlane` | The launcher, [`npm/dotlane/bin/dotlane.js`](../npm/dotlane/bin/dotlane.js); depends on exactly the same version of each platform package, as optional dependencies |
| `@dotlane/{darwin,linux}-{arm64,x64}`, `@dotlane/win32-{arm64,x64}` | One binary each; `os` and `cpu` make npm install only the one for the machine. The build requires all six |

No package has install scripts. A pre-release goes to the `next` npm tag, a release to `latest`.
Tests: `node --test 'npm/test/*.test.*'` (the launcher and the build), and
[`scripts/npm-e2e.sh`](../scripts/npm-e2e.sh), which packs real binaries, installs them into a
scratch project and runs `npx dotlane` (on the machine's own OS and CPU; CI runs it on macOS,
Ubuntu and Windows).

The release workflow publishes with [`scripts/npm-publish.sh`](../scripts/npm-publish.sh),
platform packages first, in the same job that creates the draft: npm gets the version before the
draft is reviewed, not when it is published. Every merge group runs the same script with
`--dry-run`. The registry can take a few minutes to list a new version (`v0.3.0`'s Windows
packages took six); `npm view @dotlane/win32-x64@<version> version --prefer-online` shows when it
has.

**Publishing is off** until these one-time steps are done (the *Publish to npm* step checks the
repository variable `NPM_PUBLISH`):

1. ~~**Claim the name `dotlane`.**~~ Done 2026-10-31.
2. ~~**First publish, by hand**~~ (npm needs a package to exist before a trusted publisher can be
   set on it): `v0.1.0-rc.2` of all three packages, under `next` (npm also points `latest` at
   it until `0.1.0`). Done 2026-10-31.
3. **Make the repository public** ([`security-guide.md`](security-guide.md), *Before the tool's
   repository goes public*): npm provenance (`--provenance`) requires a public source
   repository, and the public repository is a new one.
4. **Trusted publishing, then a release within two days.** On each of the three packages on
   npmjs.com, *Settings → Trusted publisher → GitHub Actions*:

   | Field | Value |
   | --- | --- |
   | Organization or user | `dotlane` |
   | Repository | `dotlane` |
   | Workflow filename | `release.yml` |
   | Environment name | `release` |
   | Allowed actions | `npm publish` ticked (the workflow does not stage) |

   npm discards a trusted publisher that has not published within two days of being set, and
   the first publish binds it to the repository's identity; so set it only once the public
   repository exists, then set the repository variable `NPM_PUBLISH` to `true` and tag the next
   release right away. From then on the release workflow publishes with a short-lived OIDC
   token; there is no npm token to store or steal.
5. **Lock it down:** on each package, *Settings → Publishing access → Require two-factor
   authentication and disallow tokens*, then revoke any npm token left on a maintainer's
   machine (`npm token list`).

**The four Linux and Windows packages** (`@dotlane/linux-*`, `@dotlane/win32-*`) went through the
same steps on 2026-11-02, before `v0.2.0-rc.1`. Their first publish by hand is `0.0.0-placeholder`
(a `package.json` and a README, no binary): npm reserves `0.0.0-stage`, the version it creates
itself while it processes a new package's first publish, and refuses it from anyone else. With
`NPM_PUBLISH` off while doing it, so a tag cannot publish half the packages, and logged in with
`npm login`, the rest is on the command line for each package:

```bash
npm access set mfa=publish @dotlane/linux-x64        # 2FA required, tokens disallowed
npm trust github @dotlane/linux-x64 --file release.yml --repository dotlane/dotlane \
  --environment release --allow-publish
```

then `npm logout`, `NPM_PUBLISH` back on, and the release tagged within two days.
