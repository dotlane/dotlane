# Branches and versions

How changes move from a pull request to a release. One long-lived branch, built so that many
people and agents can work in parallel without conflicts or a CI run for every merge:

- **`main`**: where everything lands, and where every `v*` tag goes. Each change is a pull request
  merged through the **merge queue**, squashed into one commit. `main` always builds and passes
  the tests the queue ran.
- **Short-lived branches** (`feat/…`, `fix/…`, `docs/…`, `chore/…`) come off `main`, live hours
  or a few days, go back into `main`, and are deleted on merge.
- **`release/vX.Y`**, only when needed: a maintenance branch cut from a tag, for a patch to a
  version that `main` has already moved past (Patches, below).

```
feat/x ─┐  feat/y ─┐  fix/z ─┐
        ▼          ▼         ▼
      ┌─────── merge queue ───────┐   one CI run for the group, on top of main
      └─────────────┬─────────────┘
main  ───●──────────●─────●───────────●──────▶
         │       (x, y, z squashed)   │
      v0.3.0-rc.1                  v0.3.0
```

## Day to day

```bash
git fetch origin
git switch -c feat/port-retry origin/main
# … commit, scripts/check.sh …
git fetch origin && git rebase origin/main    # conflicts show up here, not in the queue
git push -u origin feat/port-retry
gh pr create --draft                          # draft: CI does not run yet
gh pr ready                                   # now the quick tier runs
gh pr merge --squash --auto                   # joins the merge queue once gate passes
```

**One pull request per feature, one commit per task.** A feature gets one branch and one pull
request, however many tasks it takes; each task is its own commit on that branch, so the
history reads step by step while the review stays in one place. Open a second pull request only
when it helps: an unrelated fix found on the way, or a change too large to review as one. The
squash merge turns the branch into a single commit on `main`, so the pull request title becomes
its message and the description sums up all of its commits.

A user-visible change adds a **changelog fragment** instead of editing `CHANGELOG.md`
(Changelog, below).

## Parallel work

Several people or agents working at once is the normal case. What keeps them out of each
other's way:

- **One worktree and one branch each**, from `origin/main`:
  `git worktree add ../dotlane-port-retry -b feat/port-retry origin/main`. Never two writers on one
  branch.
- **Split work by area, not by step.** Hand each worker its own packages and files; two branches
  that edit the same function will conflict whatever the branching model. When one piece needs
  another's change, **stack** it: branch from the first branch, open the second pull request
  against it, and once the first merges, `git rebase --onto origin/main <first-branch>` and
  retarget it at `main`.
- **No shared append-only files.** The changelog is fragments, one file per pull request. Do the
  same for anything new that every feature would add a line to: a registry each package fills
  from its own `init()`, a list generated at build time, a folder of one file per entry.
- **Rebase before marking ready**, so each branch is tested on the `main` it will land on, and a
  conflict is the author's to fix while the change is fresh.
- **Keep branches short.** A branch that lives for a week collects conflicts; merge in pieces
  behind a flag rather than hold a big branch open.

The merge queue does the rest: pull requests do not have to be up to date with `main` to join it,
so one merging never forces the others to rebase and rerun CI.

## The merge queue

`gh pr merge --squash --auto` (or *Merge when ready* on GitHub) puts a pull request in the queue
once its `gate` check has passed. The queue groups waiting pull requests, builds each group on
top of `main` as a temporary `gh-readonly-queue/main/…` branch, runs CI on it (a `merge_group`
event), and merges the group when `gate` passes. If a group fails, the queue takes the failing
pull request out and tests the rest again; the author fixes it and queues it again.

The settings live in [`ruleset-main.json`](../.github/settings/ruleset-main.json):

| Setting | Value | Why |
| --- | --- | --- |
| Merge method | squash | One commit per pull request on `main` |
| Group size | 2 to 5 | Up to five pull requests share one full CI run |
| Wait for a second | 15 minutes | A lone pull request waits a little for company, then goes alone |
| Groups built at once | 2 | Bounds the macOS runners in use |
| Grouping | all green | Every pull request in a group must pass, not only the last |
| Timeout | 90 minutes | The full tier, including time queued for a macOS runner |

Turn the group size up when many pull requests land a day, down if failing groups keep
bouncing good pull requests.

## Changelog

Every pull request a user would notice adds one file under [`changes/`](../changes/README.md),
named after its branch (`feat/port-retry` → `changes/port-retry.md`), with only
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) sections:

```markdown
### Fixed

- **`dotlane up` retrying a busy port forever**: it now gives up after five tries.
```

Pull requests never edit `CHANGELOG.md`, so they never conflict over it. Internal changes need
no fragment. [`scripts/changelog.sh`](../scripts/changelog.sh) does the rest:

```bash
scripts/changelog.sh check            # every fragment is well formed (scripts/check.sh runs it)
scripts/changelog.sh preview          # the next release's notes
scripts/changelog.sh release v0.3.0   # writes them into CHANGELOG.md, deletes the fragments
```

## Versions

[Semantic Versioning](https://semver.org/). While Dotlane is `0.x`:

- **Minor** (`0.3.0`): new features, or anything that breaks a command, flag, config file or the
  control API.
- **Patch** (`0.3.1`): fixes only, nothing a user must change for.
- **Pre-release** (`0.3.0-rc.1`): a candidate for testers. Optional for a patch, expected for a
  minor.

From `1.0.0`, breaking changes bump the major, and features the minor.

The version lives only in the tag: GoReleaser stamps it into the binary (`dotlane version`), and
the release workflow publishes that version's section of the changelog. Nothing in the code
needs bumping.

## Releasing

The full steps are in [`releasing.md`](releasing.md). In short, everything is a tag on `main`:

1. **Candidate:** tag `v0.3.0-rc.1` on a green `main`. Its release notes come from the fragments
   (`scripts/changelog.sh preview`). A fix for the candidate is an ordinary pull request, then
   `-rc.2`.
2. **Release:** one pull request, `chore/release-v0.3.0`, that runs
   `scripts/changelog.sh release v0.3.0` and commits the result. Once the queue merges it, tag
   that commit `v0.3.0`.

Work keeps landing on `main` the whole time. If something has landed since the candidate that
should not ship yet, cut `release/v0.3` from the candidate's tag and release from there, as for a
patch (below).

## Patches

**Fix forward by default:** a bug fix goes into `main` like any change, and the next tag on
`main` (`v0.3.1`) ships it. That works whenever `main` holds nothing unready for release.

Only when `main` has moved on to work that cannot ship yet, patch the old version on a
maintenance branch:

1. Create it from the tag, once per minor: `git push origin v0.3.0:refs/heads/release/v0.3`.
2. Fix the bug on `main` first (with its fragment), then cherry-pick the fix onto a branch from
   `release/v0.3` and open a pull request into **`release/v0.3`**. It runs the full tier, since a
   `release/` branch has no queue.
3. On `release/v0.3`, a pull request with the `## [0.3.1]` section written into `CHANGELOG.md`
   by hand (the fragment stays on `main`, for the next minor's notes). Merge, tag `v0.3.1` on
   `release/v0.3`.
4. Bring the `## [0.3.1]` section and its link back to `main` in a small pull request, so
   `CHANGELOG.md` on `main` lists every release.

## CI: what runs when

Expensive runners are the cost: a macOS minute costs ten Linux minutes on a private repository,
a Windows minute two, and the plan allows few macOS runners at once. So a pull request runs only
the quick Linux tier, and the macOS, Windows, distro and package jobs run **once per merge group**,
for every pull request in it.

| Event | Runs |
| --- | --- |
| Push to any branch | nothing |
| Draft pull request | nothing (the ten-second job that decides, and `gate`, which fails on purpose) |
| Pull request, docs only | nothing (the same ten-second job; `gate` is skipped) |
| Pull request into `main` | **quick tier**: `test`, `integration-linux` (Linux only) |
| Merge group, docs only | nothing |
| Merge group | **full tier**: also `install-tests`, `integration`, `homebrew` (macOS), `test-windows`, `integration-windows`, the Fedora and Arch integration, `release-snapshot`, `linux-packages` |
| Pull request into `release/…` | full tier (no queue there) |
| Merge into `main` | nothing: its merge group ran it on the same tree |
| `v*` tag | full tier, then the release (`release.yml`) |
| `gh workflow run ci.yml --ref <branch>` | full tier, on demand |
| Publishing a release | the Homebrew tap update (`tap.yml`) |

The `changes` job in [`ci.yml`](../.github/workflows/ci.yml) picks the tier, and the `gate` job
checks the outcome against it (Guards, below). Also keeping runs down:

- **Drafts**: open a pull request as a draft while still pushing to it; CI starts when it is
  marked ready. Until then its `gate` check is red, which only says the tests have not run.
- **Cancel in progress**: a new push to a pull request cancels its older run.
- **Docs only** means every file is a `*.md`, or under `docs/`, `changes/`, `assets/demo/` or
  `.github/ISSUE_TEMPLATE/`.
- **No "update branch" reruns**: the queue tests on top of the latest `main` itself, so pull
  requests need not be kept up to date with it.
- **Dependabot** opens one grouped pull request per ecosystem a week.
- **Run the full tier early when it matters**: a change to the installer, DNS or certificates can
  be checked on macOS and Windows before it joins the queue: `gh workflow run ci.yml --ref feat/…`.

The cost is that a macOS- or Windows-only break shows up in the merge group rather than on the
pull request, and takes the group back out of the queue. The manual run above catches it earlier
for risky changes.

## Guards

- **Rulesets** ([`ruleset-main.json`](../.github/settings/ruleset-main.json),
  [`ruleset-release.json`](../.github/settings/ruleset-release.json)): `main` and `release/**`
  take changes only through pull requests, squash-merged, and refuse force-pushes and deletion.
  `main` also requires the merge queue.
- **One required check, `gate`**: both rulesets require only the `gate` job, which passes when
  every job the tier picked by `changes` passed and nothing failed. It runs on pull requests and on
  merge groups, so the same check guards both. Requiring the tier's jobs directly does not work,
  for two reasons: a ruleset counts a skipped check as passed, and it reads the newest check of
  each name on the head commit, even one from an older run. A draft skips its jobs, so marking one
  ready and enabling auto-merge at once merged it on the draft's skipped checks, before the new
  run's had even started ([#34](https://github.com/dotlane/dotlane/pull/34), 2026-11-02). So
  `gate` fails on a draft instead: that failure stays the newest `gate` until the ready run's
  `gate` has seen the tier pass. `gate` is skipped, so passes, only for a docs-only pull request
  or merge group, the cases where nothing should run. It also fails when a job it needs failed or
  the run was cancelled.

Applying the rulesets (once, and again after changing the files; `PUT` with the id to update):

```bash
gh api repos/dotlane/dotlane/rulesets --jq '.[] | "\(.id) \(.name)"'
gh api -X PUT repos/dotlane/dotlane/rulesets/<main id> --input .github/settings/ruleset-main.json
gh api -X POST repos/dotlane/dotlane/rulesets --input .github/settings/ruleset-release.json
```

## History

Until 2026-11-03 work landed on a `dev` branch and went to `main` through release branches.
With several agents working in parallel, every pull request edited the changelog's `[Unreleased]`
section, every merge forced the others to rebase and rerun CI, and keeping two branches in step
took a sync after each release. Trunk-based work on `main`, the merge queue and changelog
fragments replace all three.
