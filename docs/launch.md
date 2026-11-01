# Launch day

The steps that take Dotlane public, in order, with the exact commands. Run them from a clone of
this repository with `gh` logged in as an owner of the `dotlane` organization. Why each setting
exists is in [`security-guide.md`](security-guide.md); how a release flows through the channels,
in [`distribution.md`](distribution.md) and [`releasing.md`](releasing.md).

**Before starting,** decide on signing (P2-M2). These steps launch unsigned: the Homebrew cask
removes macOS's quarantine attribute, and npm downloads are not quarantined.

**Launched on 2026-11-01:** steps 0 to 7 are done (step 5 was not needed: the tag matched the
changelog's date), except what waits for Homebrew. `v0.1.0` is public on GitHub Releases and npm.
The exit check (step 8) passed on a new user account the same day, through npm, and npm was
locked down (step 9). Only the Homebrew items are left.

**Homebrew is on hold** (decided 2026-11-01): the launch ships through npm only. Step 3 and the tap
check in step 7 wait until it resumes; the exit check uses npm.

**Steps 4 to 7 must happen within two days:** npm discards a trusted publisher that has not
published within two days of being set.

## 0. Pre-flight

- [x] `main` is green and nothing is open:

  ```bash
  git switch main && git pull --ff-only
  gh run list --branch main --workflow ci.yml --limit 1     # completed success
  gh pr list                                                 # empty
  ```

- [x] The old history stays private, and the history has no secrets:

  ```bash
  gh api repos/dotlane/dotlane-history --jq .private        # true
  gitleaks git --log-opts=--all --redact                     # no leaks found
  ```

## 1. Make the repository public

- [x] Make it public:

  ```bash
  gh repo edit dotlane/dotlane --visibility public --accept-visibility-change-consequences
  ```

## 2. Public-repository settings

- [x] The ruleset on `main`: changes through pull requests only, the five required checks, no
  force pushes, no deletion:

  ```bash
  gh api -X POST repos/dotlane/dotlane/rulesets --input .github/settings/ruleset-main.json
  ```

- [x] Secret scanning and push protection:

  ```bash
  gh api -X PATCH repos/dotlane/dotlane --input - <<'EOF'
  {"security_and_analysis":{"secret_scanning":{"status":"enabled"},"secret_scanning_push_protection":{"status":"enabled"}}}
  EOF
  ```

- [x] Private vulnerability reporting, which [`SECURITY.md`](../SECURITY.md) points to:

  ```bash
  gh api -X PUT repos/dotlane/dotlane/private-vulnerability-reporting
  ```

- [x] Approval for workflows from forks, for all outside contributors:

  ```bash
  gh api -X PUT repos/dotlane/dotlane/actions/permissions/fork-pr-contributor-approval -f approval_policy=all_external_contributors
  ```

- [x] A required reviewer on the `release` environment. With one maintainer, that maintainer
  approves their own releases, so self-review stays allowed; 5757275 is the GitHub user ID of
  `pedrodee` (`gh api users/<login> --jq .id` for another maintainer):

  ```bash
  gh api -X PUT repos/dotlane/dotlane/environments/release --input - <<'EOF'
  {"reviewers":[{"type":"User","id":5757275}],"prevent_self_review":false,"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}
  EOF
  ```

- [x] Check them all:

  ```bash
  gh api repos/dotlane/dotlane --jq '{visibility, ss: .security_and_analysis.secret_scanning.status, pp: .security_and_analysis.secret_scanning_push_protection.status}'
  gh api repos/dotlane/dotlane/rulesets --jq '.[].name'                                    # main
  gh api repos/dotlane/dotlane/private-vulnerability-reporting --jq .enabled               # true
  gh api repos/dotlane/dotlane/environments/release --jq '[.protection_rules[].type]'      # includes required_reviewers
  gh api repos/dotlane/dotlane/environments/release/deployment-branch-policies --jq '.branch_policies[].name'   # v*
  ```

## 3. Homebrew tap (on hold)

The tap repository is already public (2026-11-01) and empty; `HOMEBREW_PUBLISH` is `false`, so
publishing a release leaves it alone. Each release still builds the cask and attaches it.

- [ ] **To resume:** turn publishing on before publishing a release; that release then commits
  the cask to the tap (step 7), and the README, `setup.md` and the changelog drop "coming soon":

  ```bash
  gh variable set HOMEBREW_PUBLISH -R dotlane/dotlane --body true
  ```

## 4. npm trusted publishing (the two days start here)

- [x] On npmjs.com, for each of `dotlane`, `@dotlane/darwin-arm64` and `@dotlane/darwin-x64`:
  *Settings → Trusted publisher → GitHub Actions*:

  | Field | Value |
  | --- | --- |
  | Organization or user | `dotlane` |
  | Repository | `dotlane` |
  | Workflow filename | `release.yml` |
  | Environment name | `release` |
  | Allowed actions | **npm publish** ticked |

- [x] Turn on publishing from the release workflow:

  ```bash
  gh variable set NPM_PUBLISH -R dotlane/dotlane --body true
  ```

## 5. The changelog date

Only if the release is not tagged on the date in `CHANGELOG.md`'s `## [0.1.0]` heading. Not
needed for `v0.1.0`: it was tagged on that date.

- [ ] Change the date, open a pull request, and merge it once CI is green (docs only, so it
  skips the macOS jobs):

  ```bash
  git switch -c changelog-date
  sed -i '' -E "s/^## \[0\.1\.0\] - [0-9-]+$/## [0.1.0] - $(date +%F)/" CHANGELOG.md
  git commit -am "CHANGELOG: date 0.1.0" && git push -u origin HEAD && gh pr create --fill
  gh pr merge --squash --delete-branch && git switch main && git pull --ff-only
  ```

## 6. Tag v0.1.0

- [x] Tag it and watch the release workflow:

  ```bash
  scripts/changelog-section.sh v0.1.0 > /dev/null && echo notes ok
  git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0
  gh run watch "$(gh run list --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')"
  ```

- [x] When it stops at the `release` job, approve it on the run's page: *Review deployments →
  release → Approve and deploy*.

- [x] The run creates the draft release and publishes the three npm packages under `latest`, with
  provenance. Check:

  ```bash
  npm view dotlane dist-tags                                  # latest: 0.1.0
  npm view dotlane@0.1.0 dist.attestations.provenance         # present
  npx --yes dotlane@latest version                            # 0.1.0 (...)
  ```

- [x] Check the draft: every file is there, the archives match `checksums.txt`, and each npm
  platform package holds the same binary as its archive:

  ```bash
  d=$(mktemp -d) && gh release download v0.1.0 -R dotlane/dotlane -D "$d" && cd "$d"
  shasum -a 256 -c checksums.txt
  for a in "arm64 arm64" "amd64 x64"; do set -- $a
    tar xzOf "dotlane_0.1.0_darwin_$1.tar.gz" dotlane | shasum -a 256
    tar xzOf "dotlane-darwin-$2-0.1.0.tgz" package/bin/dotlane | shasum -a 256
  done                                                        # each pair identical
  cd - && rm -rf "$d"
  ```

## 7. Publish the release

- [x] Publish the draft:

  ```bash
  gh release edit v0.1.0 -R dotlane/dotlane --draft=false --latest
  ```

- [ ] **On hold with Homebrew.** Publishing also starts the *Homebrew tap* workflow, which skips
  while `HOMEBREW_PUBLISH` is `false`. Once Homebrew resumes, approve that run (*Review
  deployments → release*), then check:

  ```bash
  gh api repos/dotlane/homebrew-tap/commits --jq '.[0].commit.message'     # dotlane v0.1.0
  brew update && brew info --cask dotlane/tap/dotlane                         # 0.1.0
  ```

## 8. Exit check on a clean account

Ports 80 and 443 can be held by only one install, so stop yours first.

- [x] In your own account: `dotlane uninstall`. It keeps your sites and the CA; `dotlane install`
  restores the service afterwards, with one password.
- [x] Create a test user (*System Settings → Users & Groups*), log in as it, and run (with
  Homebrew back, repeat with `brew install dotlane/tap/dotlane` and `brew uninstall --zap dotlane`):

  ```bash
  npm install -g dotlane
  dotlane install
  mkdir -p /tmp/myapp && echo 'Hello from myapp' > /tmp/myapp/index.html
  python3 -m http.server 5173 --bind 127.0.0.1 --directory /tmp/myapp &
  dotlane add myapp 5173
  curl https://myapp.traix                     # Hello from myapp
  ```

- [x] Open `https://myapp.traix` in Safari: the page loads with a trusted certificate.
- [x] Remove everything, then try `npx` with nothing installed:

  ```bash
  kill %1
  dotlane uninstall --purge && npm uninstall -g dotlane
  npx --yes dotlane version                    # 0.1.0 (...)
  ```

- [x] Log out, delete the test user, and run `dotlane install` in your own account again.

## 9. Lock down npm

- [x] On each of the three packages: *Settings → Publishing access → Require two-factor
  authentication and disallow tokens*.
- [x] Revoke any npm token left on a maintainer's machine:

  ```bash
  npm login && npm token list    # then: npm token revoke <id>, for each
  npm logout
  ```

## 10. After launch

- [x] Mark the launch in [`security-guide.md`](security-guide.md) (the status section and the
  public-repository settings) and in [`phase-2.md`](phase-2.md) (P2-M5 done).
- [ ] Keep `dotlane/dotlane-history` private.
