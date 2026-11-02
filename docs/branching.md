# Branches and versions

How changes move from a pull request to a release. Two long-lived branches:

- **`dev`**: where work lands. Every feature and fix is a pull request into `dev`, squash-merged.
  It may hold unreleased work at any time, but it always builds and passes the quick tests.
- **`main`**: what was released, or is about to be. It changes only through a release, a hotfix
  or a Dependabot security update, each a pull request merged with a **merge commit**. Every
  `v*` tag is on `main`.

Short-lived branches come off one of those two and are deleted when merged.

| Branch | From | Into | Merge |
| --- | --- | --- | --- |
| `feat/…`, `fix/…`, `docs/…`, `chore/…` | `dev` | `dev` | squash |
| `release/vX.Y.Z` | `dev` | `main` | merge commit |
| `hotfix/vX.Y.Z` | `main` | `main` | merge commit |

`main` takes merge commits only, never squash: a squashed release would give `main` commits that
`dev` does not have, and every later release would conflict with them. With merge commits, `dev`
is always an ancestor of `main` after a sync, so the sync is a fast-forward.

```
feat/x ──squash──┐   feat/y ──squash──┐
                 ▼                    ▼
dev   ───●───────●────────────────────●───────────●──────▶
          \                                      ▲ (sync: fast-forward)
           release/v0.3.0 ─ changelog ─┐         │
                                       ▼         │
main  ─────────────────────────────────●─────────┘──────▶
                                   tag v0.3.0
```

## Versions

[Semantic Versioning](https://semver.org/), as [`CHANGELOG.md`](../CHANGELOG.md) already says.
While Dotlane is `0.x`:

- **Minor** (`0.3.0`): new features, or anything that breaks a command, flag, config file or the
  control API. Comes from `dev` through a release branch.
- **Patch** (`0.3.1`): fixes only, nothing a user must change for. Usually a hotfix from `main`;
  a release branch is fine too if `dev` holds only fixes.
- **Pre-release** (`0.3.0-rc.1`): a candidate for testers on the same commit that will become
  the release. Optional for a patch, expected for a minor.

From `1.0.0`, breaking changes bump the major, and features the minor.

The version lives only in the tag: GoReleaser stamps it into the binary (`dotlane version`), and
the release workflow publishes that version's section of the changelog. Nothing in the code
needs bumping.

## Day to day

```bash
git switch dev && git pull --ff-only
git switch -c feat/port-retry
# … commit, scripts/check.sh, push …
gh pr create --base dev --draft      # draft: CI does not run yet
gh pr ready                          # now it does
gh pr merge --squash --auto --delete-branch   # merges once the gate check passes
```

Each pull request adds its line under `## [Unreleased]` in `CHANGELOG.md` (see
[`CONTRIBUTING.md`](../CONTRIBUTING.md)), so the changelog on `dev` is always the next release's
notes.

## Releasing

The full steps, and what a release contains, are in [`releasing.md`](releasing.md). In short:

1. `git switch -c release/v0.3.0 origin/dev`, rename `[Unreleased]` to the version in
   `CHANGELOG.md`, commit, push, and open a pull request into **`main`**. This runs the full CI
   suite once for everything since the last release. Work keeps landing on `dev` meanwhile.
2. Merge it with a merge commit. Tag `main`: `v0.3.0-rc.1` for testers, or `v0.3.0` straight away.
3. **Sync `dev`:** bring the release (its changelog commit) back, so the next release starts from
   it. A fast-forward when `dev` has not moved, else a merge commit:

   ```bash
   git switch dev && git pull --ff-only
   git merge --no-edit origin/main    # fast-forwards when it can
   git push origin dev                # maintainers only: the dev ruleset lets admins push
   ```

4. A candidate that needs a fix gets it through a hotfix branch into `main` (below), then
   `-rc.2`, never through `dev`: `dev` may already hold the next release's work.
5. Delete the release branch.

## Hotfixes

For a bug in a released version that cannot wait for the next release:

1. `git switch -c hotfix/v0.3.1 origin/main`, fix it, add the changelog section `## [0.3.1]`,
   and open a pull request into `main` (full CI).
2. Merge with a merge commit, tag `v0.3.1`.
3. Sync `dev` as in step 3 above. This is a real merge when `dev` has moved on; resolve any
   conflict in `CHANGELOG.md` by keeping both sections.

## CI: what runs when

Expensive runners are the cost: a macOS minute costs ten Linux minutes on a private repository,
a Windows minute two. So the macOS, Windows, distro and package jobs run **once per release**,
on the release pull request, instead of on every feature pull request.

| Event | Runs |
| --- | --- |
| Push to any branch | nothing |
| Draft pull request | nothing (the ten-second job that decides, and `gate`, which fails on purpose) |
| Pull request into `dev`, docs only | nothing (the same ten-second job; `gate` is skipped) |
| Pull request into `dev` | **quick tier**: `test`, `integration-linux` (Linux only) |
| Pull request into `main`, docs only | quick tier |
| Pull request into `main` (release, hotfix) | **full tier**: also `install-tests`, `integration`, `homebrew` (macOS), `test-windows`, `integration-windows`, the Fedora and Arch integration, `release-snapshot`, `linux-packages` |
| Merge into `dev` or `main` | nothing: its pull request ran it |
| `v*` tag | full tier, then the release (`release.yml`) |
| `gh workflow run ci.yml --ref <branch>` | full tier, on demand |
| Publishing a release | the Homebrew tap update (`tap.yml`) |

The `changes` job in [`ci.yml`](../.github/workflows/ci.yml) picks the tier, and the `gate` job
checks the outcome against it (Guards, below). Also keeping runs down:

- **Drafts**: open a pull request as a draft while still pushing to it; CI starts when it is
  marked ready. Until then its `gate` check is red, which only says the tests have not run.
- **Cancel in progress**: a new push to a pull request cancels its older run.
- **Docs-only** pull requests into `dev` run nothing, and into `main` skip the full tier. Docs
  only means every file is a `*.md`, or under `docs/`, `assets/demo/` or `.github/ISSUE_TEMPLATE/`.
- **Dependabot** opens one grouped pull request per ecosystem a week, into `dev`.
- **Run the full tier early when it matters**: a change to the installer, DNS or certificates on
  a feature branch can be checked on macOS and Windows before it waits for the release:
  `gh workflow run ci.yml --ref feat/…`.

The cost is that a macOS- or Windows-only break shows up on the release pull request rather than
on the feature that caused it. Releases every week or two keep that batch small; the manual run
above covers the risky changes.

## Guards

- **Rulesets** ([`ruleset-main.json`](../.github/settings/ruleset-main.json),
  [`ruleset-dev.json`](../.github/settings/ruleset-dev.json)): both branches take changes only
  through pull requests and refuse force-pushes and deletion. `main` allows merge commits only,
  `dev` squash only. Admins may push to `dev` directly, for the sync after a release.
- **One required check, `gate`**: both rulesets require only the `gate` job, which passes when
  every job the tier picked by `changes` passed and nothing failed. Requiring the tier's jobs
  directly does not work, for two reasons: a ruleset counts a skipped check as passed, and it
  reads the newest check of each name on the head commit, even one from an older run. A draft
  skips its jobs, so marking one ready and enabling auto-merge at once merged it on the draft's
  skipped checks, before the new run's had even started
  ([#34](https://github.com/dotlane/dotlane/pull/34), 2026-11-02). So `gate` fails on a draft
  instead: that failure stays the newest `gate` until the ready run's `gate` has seen the tier
  pass, and auto-merge waits. `gate` is skipped, so passes, only for a docs-only pull request
  into `dev`, the one case where nothing should run. It also fails when a job it needs failed or
  the run was cancelled.
- **Wrong base**: the `test` job fails a pull request into `main` unless its branch starts with
  `release/`, `hotfix/` or `dependabot/` (security updates go to the default branch).

Applying the rulesets (once, and again after changing the files; `PUT` with the id to update):

```bash
gh api -X POST repos/dotlane/dotlane/rulesets --input .github/settings/ruleset-dev.json
gh api repos/dotlane/dotlane/rulesets --jq '.[] | "\(.id) \(.name)"'
gh api -X PUT repos/dotlane/dotlane/rulesets/<main id> --input .github/settings/ruleset-main.json
```

`main` stays the default branch, so the README and docs people land on describe the released
version.

<!-- throwaway: gate verification, do not merge -->
