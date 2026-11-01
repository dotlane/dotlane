# Homebrew

`brew install dotlane/tap/dotlane`: a cask in [`dotlane/homebrew-tap`](https://github.com/dotlane/homebrew-tap)
that installs the binary from a GitHub release. **On hold since 2026-11-01:** the tap is public
but empty, and `v0.1.0` shipped through npm and source builds only.

## Where it lives

Everything is built and tested in this repository; the tap only receives the finished cask.

| What | Where |
| --- | --- |
| The cask, written by every release and attached to its draft | `homebrew_casks` in [`.goreleaser.yaml`](../.goreleaser.yaml) |
| Committing it to the tap when a release is published | [`.github/workflows/tap.yml`](../.github/workflows/tap.yml), [`scripts/tap-update.sh`](../scripts/tap-update.sh) |
| Install, site, upgrade and zap on a macOS runner, for every pull request that changes code | [`scripts/brew-e2e.sh`](../scripts/brew-e2e.sh), the *homebrew* job in `ci.yml` |
| What the cask does, the deploy key, publishing by hand | [`releasing.md`](releasing.md#homebrew) |

The switch is the repository variable `HOMEBREW_PUBLISH`, now `false`: publishing a release
starts `tap.yml`, which skips. The cask is still built and attached to every release.

## Roadmap

Grouped like the [core roadmap](roadmap.md): Now, Next, Later and Done.

### Now

- On hold. Nothing is being worked on.

### Next: resume the tap

1. Set `gh variable set HOMEBREW_PUBLISH -R dotlane/dotlane --body true` before publishing a
   release ([`launch.md`](launch.md#3-homebrew-tap-on-hold)).
2. Publish a release and approve the *Homebrew tap* run; the tap gets `Casks/dotlane.rb`.
3. Run the exit check on a new user account with `brew install dotlane/tap/dotlane`, then
   `brew uninstall --zap dotlane` ([`launch.md`](launch.md#8-exit-check-on-a-clean-account)).
4. Drop "coming soon" from the README, [`setup.md`](setup.md) and the changelog's next section.

### Later

- **Signed binaries.** Once the core signs and notarizes releases (the
  [core roadmap](roadmap.md#now)), the cask stops removing macOS's quarantine attribute.
- **Linux.** A Homebrew-on-Linux formula, once the core runs on Linux.
- **homebrew-core** (`brew install dotlane`, no tap): a formula that builds from source. Its
  policy asks for a notable project (at least 30 forks, 30 watchers or 75 stars, and 90 / 90 /
  225 when the owner submits it) and a repository at least 30 days old. A milestone, not a
  blocker.

### Done

- The cask, built by every release, with caveats, `zap`, and upgrades that
  restart the service through the next `dotlane` command (2026-10-31).
- `tap.yml` and its deploy key, gated by `HOMEBREW_PUBLISH` and the `release` environment.
- The tap repository, public since 2026-11-01.
