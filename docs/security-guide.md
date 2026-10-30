# Security guide

How the Dotlane project protects its code, secrets, releases and website. Written 2026-10-30,
when the repositories moved into the `dotlane` organization. The product's own security model
(the CA, listeners, control socket) is in [`how-it-works.md`](how-it-works.md) and
[`security.md`](security.md); how to report a vulnerability, in [`../SECURITY.md`](../SECURITY.md).

## Status (2026-10-30)

What is applied, what waits for the tool's repository to go public (the free plan offers those
features only on public repositories), and what an owner sets by hand in the web settings.

**Applied through the API:**

| Where | Setting |
| --- | --- |
| Organization | Base permission `none`; only owners create repositories; members cannot create Pages sites; Dependabot alerts, security updates and the dependency graph on for new repositories |
| All three repositories | Actions limited to GitHub-owned and verified-creator actions; `GITHUB_TOKEN` read-only by default and unable to approve pull requests; Dependabot alerts and security updates on |
| `dotlane/dotlane` | `release` environment that accepts only `v*` tags |
| All three repositories (local) | Commits use the GitHub noreply address (`git config user.email`), so nothing new carries a personal address |
| `dotlane/dotlane` CI | `permissions: contents: read`; actions pinned to full commit SHAs; `govulncheck` (it found five reachable HTTP/2 vulnerabilities in `golang.org/x/net` v0.59.0, fixed by v0.60.0); Dependabot version updates for Go modules and actions (`.github/dependabot.yml`) |

**When `dotlane/dotlane` goes public** (refused on private repositories on the free plan):

- [ ] The ruleset on `main`: `gh api -X POST repos/dotlane/dotlane/rulesets --input .github/settings/ruleset-main.json`
- [ ] Secret scanning and push protection
- [ ] Private vulnerability reporting (`SECURITY.md` points to it)
- [ ] Fork pull requests: approval required for all outside contributors (`approval_policy=all_external_contributors`)
- [ ] Required reviewer (a maintainer) on the `release` environment

**By hand, in the organization's settings** (not settable through the API, or not with the
current token, or accepted by the API but not saved):

- [ ] Authentication security → **Require two-factor authentication**
- [ ] Actions → General → **Allow GitHub-owned and verified-creator actions** for the whole
      organization (each repository already is; this covers new ones)
- [ ] Member privileges → turn off **changing repository visibility**, **deleting repositories**
      and **inviting outside collaborators** (no effect while you are the only member and owner,
      since owners can always do these)
- [ ] Your account → Emails → **Keep my email address private** and **Block command line pushes
      that expose my email** (step 1 of the go-public procedure below; the repositories already
      commit as the noreply address, so pushes keep working once blocking is on)

## The repositories and their boundaries

Repositories are split by who may see them and which secrets they may use. GitHub sets visibility
per repository, which is why there are four, not one.

| Repository | Visibility | Holds | May use |
| --- | --- | --- | --- |
| `dotlane/dotlane` | private now, **public** at `v0.1.0` | The tool (CLI and daemon), its docs, the brand assets, the integration test | The `release` environment (signing, notarization); npm through trusted publishing; a token limited to `homebrew-tap` |
| `dotlane/website` | private | The website | One deploy credential, in its own `production` environment |
| `dotlane/desktop` | private until launch | The desktop apps | The `release` environment |
| `dotlane/homebrew-tap` (Phase 2) | public | The cask, written by the tool's release job | Nothing |

Rules that follow from the split:

- A repository never holds another repository's secrets, and nothing in the public repository
  can reach the website server or the desktop code.
- The desktop app talks to the daemon's control API and bundles a *released*, checksummed tool;
  it never copies the tool's source. The website copies brand assets from a tagged release.

## Organization settings

Set once, in the organization's settings, and checked before the tool goes public:

- [ ] **Require two-factor authentication** for every member and outside collaborator.
- [ ] **Base permission: none.** Members get access per repository.
- [ ] **Repository creation:** members cannot create public repositories.
- [ ] **Actions:** allow only actions from GitHub and verified creators, plus those pinned by
      the workflows; default `GITHUB_TOKEN` permission **read-only**; **require approval for
      workflows from fork pull requests** (all outside contributors).
- [ ] **No self-hosted runners.** A pull request from a fork could run its code on the machine.
      Every workflow uses GitHub-hosted runners.
- [ ] Owners: at least two people once there is a second maintainer, each with 2FA and a
      recovery method stored offline, so the organization cannot be lost with one account.

## Settings for the public repository

- [ ] **Ruleset on `main`:** changes through pull requests only, the `test` and `integration`
      checks must pass, no force pushes, no deletion. Tags `v*` can be created only by
      maintainers.
- [ ] **Secret scanning and push protection** on (free for public repositories): a push that
      contains a recognised secret is refused.
- [ ] **Private vulnerability reporting** on (what `SECURITY.md` points to).
- [ ] **Dependabot** alerts and version updates for `gomod` and `github-actions`.
- [ ] **`govulncheck ./...`** in CI, failing on vulnerabilities in code that is actually called.

## Secrets

| Secret | Stored as | Who can use it | Rotation and revocation |
| --- | --- | --- | --- |
| Apple Developer ID Application certificate (`.p12`) and its password | Secrets of the `release` environment | `dotlane`, `desktop`, after a maintainer approves the run | Revoke in the Apple Developer portal; already notarized releases stay valid, new ones need a new certificate |
| App Store Connect API key (notarization) | `release` environment | Same | Revoke the key in App Store Connect; issue a new one |
| npm publishing | **None**: npm trusted publishing (OIDC) from the release workflow | The release workflow on a tag | Remove the trusted publisher in npm's package settings |
| Tap token | Fine-grained token, write access to `dotlane/homebrew-tap` only | The release workflow | Revoke and recreate; expiry at most 90 days |
| Website deploy credential | `production` environment of `dotlane/website` | That repository, after approval | Replace the key on the server and in the environment |

- The `release` environment requires a maintainer's approval and accepts only runs from `v*`
  tags.
- No secret is ever written to a file in a repository, a log or an artifact; workflows mask them
  and never `echo` them.
- Developer machines: the Apple certificate's private key does not live on a laptop once CI
  signs; if it must, it stays in the login keychain, not in a file.

## Workflow rules

- Every workflow declares `permissions:` with the least it needs (`contents: read` by default;
  `contents: write` only for the release job; `id-token: write` only for npm publishing).
- Third-party actions are pinned to a full commit SHA, with the version in a comment, and
  Dependabot updates them.
- Never `pull_request_target` combined with checking out the pull request's code.
- Release workflows run only on `v*` tags, after the unit and integration tests pass, and
  publish checksums and an SBOM with every release.
- The integration test changes the system (login service, System keychain, `/etc/resolver`). It
  runs only on GitHub's throwaway macOS runners, never on a maintainer's Mac.

## The website

- Hosting: **self-hosted** on the project's own web server (decided 2026-10-30).
- The website repository holds no secrets except its deploy credential, in a `production`
  environment that needs approval.
- On the server: a dedicated deploy user whose SSH key can only write the web root
  (`restrict` and a forced command in `authorized_keys`, no shell), the server's host key pinned
  in CI, automatic security updates, and nothing on the server that can reach the other
  repositories or the signing keys.
- Served over HTTPS only, with HSTS and a Content-Security-Policy that allows the page's inline
  style and script (by hash) and Google Fonts, and nothing else. No analytics or third-party
  scripts.

## The desktop apps

- Large files through Git LFS; build output never committed.
- Signed with the same Developer ID through the `release` environment; never with a personal
  certificate.
- The bundled `dotlane` binary is a tagged release, verified against its `checksums.txt`.

## Before the tool's repository goes public

The current private history (26 commits as of 2026-10-30) was scanned:

- **Contents:** no secrets, personal paths or personal email addresses, in the tree or in any
  diff of the history. Home paths appear only as placeholders (`/Users/<you>`, `/Users/me` in
  tests).
- **Commit metadata:** every commit carries the author's personal address. This is what a public
  history would expose.
- **The example app:** a real project's name was used throughout the docs, tests and the
  prototype's files as the running example. Replaced with a neutral `myapp` (2026-10-30).

So the public repository starts from **a fresh history**; the full history stays private:

1. **Email.** In GitHub's email settings, turn on *Keep my email address private* and *Block
   command line pushes that expose my email*. Commit as the noreply address:
   `git config --global user.email 5757275+pedrodee@users.noreply.github.com`.
2. **Archive.** Rename the current repository to `dotlane/dotlane-history` (private, read-only
   by convention). Nothing is lost.
3. **Clean the tree** on a branch (the example app is already neutral);
   `git grep -n -e "/Users/" -e "@gmail" -e "BEGIN .*PRIVATE KEY"`, plus any other private names
   (employers, clients), must find only placeholders.
4. **Scan** with [gitleaks](https://github.com/gitleaks/gitleaks) (`gitleaks detect --source .`)
   and review every finding.
5. **New history.** Create a new, empty `dotlane/dotlane`, and commit the cleaned tree as one
   initial commit (author: the noreply address), tagged `v0.1.0`.
6. **Check before publishing:** `git log --format='%an <%ae>' | sort -u` shows only the noreply
   address; `gitleaks detect` is clean; CI is green in the new repository; links in the docs
   point to the new repository (links to old CI runs are only valid in the archive).
7. **Publish:** make it public, apply the repository settings above, and enable the checks of
   the ruleset.

## When something goes wrong

- **A secret leaked** (in a log, a commit, a screenshot): revoke it first (table above), then
  remove it. Rewriting history does not un-leak a secret that was public.
- **A bad release:** delete the GitHub release and its tag, mark the npm version deprecated
  (`npm deprecate`), revert the tap's cask, then publish a fixed release and a GitHub security
  advisory if users are at risk.
- **A compromised account:** remove it from the organization, review the audit log for new
  tokens, deploy keys, webhooks and workflow changes, then rotate every secret it could reach.
