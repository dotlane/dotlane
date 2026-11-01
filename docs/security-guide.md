# Security guide

How the Dotlane project protects its code, secrets, releases and website. Written 2026-10-30,
when the repositories moved into the `dotlane` organization. The product's own security model
(the CA, listeners, control socket) is in [`how-it-works.md`](how-it-works.md) and
[`security.md`](security.md); how to report a vulnerability, in [`../SECURITY.md`](../SECURITY.md).

## Status (updated 2026-11-01)

What is applied, what waits for the tool's repository to go public (the free plan offers those
features only on public repositories), and what an owner sets by hand in the web settings.

**Applied through the API:**

| Where | Setting |
| --- | --- |
| Organization | Two-factor authentication required for every member; Actions limited to GitHub-owned and verified-creator actions for every repository, new ones included; base permission `none`; only owners create repositories; members cannot create Pages sites; Dependabot alerts, security updates and the dependency graph on for new repositories; deploy keys allowed (only the tap has one, see the secrets table) |
| All repositories | Actions limited to GitHub-owned and verified-creator actions; `GITHUB_TOKEN` read-only by default and unable to approve pull requests; Dependabot alerts and security updates on, except in `homebrew-tap`, which has no dependencies |
| `dotlane/dotlane` | `release` environment that accepts only `v*` tags |
| The owner's account | Two-factor authentication on; primary email private |
| All three repositories (local) | Commits use the GitHub noreply address (`git config user.email`), so nothing new carries a personal address |
| `dotlane/dotlane` CI | `permissions: contents: read`; actions pinned to full commit SHAs; `govulncheck` (it found five reachable HTTP/2 vulnerabilities in `golang.org/x/net` v0.59.0, fixed by v0.60.0); Dependabot version updates for Go modules and actions (`.github/dependabot.yml`) |

**When `dotlane/dotlane` goes public** (refused on private repositories on the free plan):

- [ ] The ruleset on `main`: `gh api -X POST repos/dotlane/dotlane/rulesets --input .github/settings/ruleset-main.json`
- [ ] Secret scanning and push protection
- [ ] Private vulnerability reporting (`SECURITY.md` points to it)
- [ ] Fork pull requests: approval required for all outside contributors (`approval_policy=all_external_contributors`)
- [ ] Required reviewer (a maintainer) on the `release` environment

**By hand, in the web settings** (not settable through the API). Done on 2026-11-01:
two-factor authentication required in the organization, after turning it on for the owner's
account. Set through the API with the `admin:org` and `user` scopes, added for the change and
removed afterwards: the organization's Actions policy and the private primary email.

Skipped by choice (2026-11-01):

- **Member privileges** (turning off changing repository visibility, deleting repositories and
  inviting outside collaborators). These limit members only; owners can always do all three,
  and the organization has no members besides its one owner. Its base permission is `none`, so
  a future member reaches only the repositories they are given. Revisit before adding a member
  who is not an owner.
- **Block command line pushes that expose my email.** It applies to the whole GitHub account,
  not only this organization, and would refuse pushes from the owner's other repositories,
  which commit with a personal address. The owner does not mind that address being public.
  Dotlane's repositories commit as the noreply address anyway (`git config user.email` in each).

### Advisories that do not affect Dotlane

Dependabot alerts dismissed as *vulnerable code not used*, with the reason; revisit each when its
fix ships.

| Advisory | Dependency | Why Dotlane is not affected | Remove when |
| --- | --- | --- | --- |
| GHSA-mgcc-cww5-fp2q (high), GHSA-4w3g-jfj2-cw2q, GHSA-vvm8-gf78-28g4 (2026-10-27/28; no v3 fix) | `github.com/go-jose/go-jose/v3` v3.0.5, imported only by `go.step.sm/crypto/jose`, through Caddy's internal CA (`caddytls` → `caddypki` → `smallstep/certificates`) | All three need untrusted JOSE input (nested JSON, a CBC ciphertext, a JWK with an RSA key). Dotlane parses none: no ACME server is compiled in, and the internal issuer signs a CSR certmagic made locally, without tokens. A VTA call graph (2026-10-31) finds v3 reached only to encrypt, generate keys, and `jwt.ParseSigned` in `newLinkedCAClient`, which runs only with a linked-CA token (Caddy never sets one). `govulncheck` cannot check them yet: they are not in the Go vulnerability database | [smallstep/crypto#1157](https://github.com/smallstep/crypto/pull/1157) (v3 → v4, approved) is released, then a `smallstep/certificates` release built on it (v0.30.2 does not compile against #1157). Upgrade both; `go mod why -m github.com/go-jose/go-jose/v3` must then find nothing outside tests |

## The repositories and their boundaries

Repositories are split by who may see them and which secrets they may use. GitHub sets visibility
per repository, which is why there are four, not one.

| Repository | Visibility | Holds | May use |
| --- | --- | --- | --- |
| `dotlane/dotlane` | private now, **public** at `v0.1.0` | The tool (CLI and daemon), its docs, the brand assets, the integration test | The `release` environment (signing, notarization); npm through trusted publishing; a deploy key for `homebrew-tap` |
| `dotlane/website` | private | The website | One deploy credential, in its own `production` environment |
| `dotlane/desktop` | private until launch | The desktop apps | The `release` environment |
| `dotlane/homebrew-tap` | private now, **public** with the tool | The cask, pushed by the tool's `tap.yml` when a release is published | Nothing |

Rules that follow from the split:

- A repository never holds another repository's secrets, and nothing in the public repository
  can reach the website server or the desktop code.
- The desktop app talks to the daemon's control API and bundles a *released*, checksummed tool;
  it never copies the tool's source. The website copies brand assets from a tagged release.

## Organization settings

Set once, in the organization's settings, and checked before the tool goes public:

- [x] **Require two-factor authentication** for every member and outside collaborator.
- [x] **Base permission: none.** Members get access per repository.
- [x] **Repository creation:** members cannot create public repositories.
- [ ] **Actions:** allow only actions from GitHub and verified creators, plus those pinned by
      the workflows; default `GITHUB_TOKEN` permission **read-only**; **require approval for
      workflows from fork pull requests** (all outside contributors). The allow-list and the
      read-only token are done; fork approval waits for the repository to go public.
- [x] **Deploy keys allowed**, for the tap's key (`HOMEBREW_TAP_DEPLOY_KEY`). Any other deploy
      key on any repository is a finding.
- [x] **No self-hosted runners.** A pull request from a fork could run its code on the machine.
      Every workflow uses GitHub-hosted runners.
- [ ] Owners: at least two people once there is a second maintainer, each with 2FA and a
      recovery method stored offline, so the organization cannot be lost with one account.

## Settings for the public repository

- [ ] **Ruleset on `main`:** changes through pull requests only, the `test`, `install-tests`,
      `integration`, `release-snapshot` and `homebrew` checks must pass, no force pushes, no
      deletion. Tags `v*` can be created only by
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
| Tap deploy key | SSH deploy key with write access, on `dotlane/homebrew-tap` only; its private half is the `release` environment's secret `HOMEBREW_TAP_DEPLOY_KEY` | `tap.yml`, when a release is published | Delete the deploy key on the tap and the secret; generate a new pair. A deploy key reaches one repository by construction, unlike a token, and does not expire, so rotate it yearly |
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

1. **Email.** *Keep my email address private* is on (2026-11-01). Commit as the noreply address
   in the new repository: `git config user.email 5757275+pedrodee@users.noreply.github.com`.
   Blocking pushes that expose an email is skipped by choice (see the status above), so step 6
   is what catches a wrong address.
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
8. **Homebrew:** make `dotlane/homebrew-tap` public, and set the repository variable
   `HOMEBREW_PUBLISH` to `true` ([`releasing.md`](releasing.md#homebrew)).
9. **npm:** set the trusted publishers and `NPM_PUBLISH`, then tag a release within two days
   ([`releasing.md`](releasing.md#npm), steps 4 and 5). Not before: npm discards a trusted
   publisher that has not published within two days, and binds it to the repository it first
   publishes from.

## When something goes wrong

- **A secret leaked** (in a log, a commit, a screenshot): revoke it first (table above), then
  remove it. Rewriting history does not un-leak a secret that was public.
- **A bad release:** delete the GitHub release and its tag, mark the npm version deprecated
  (`npm deprecate`), revert the tap's cask, then publish a fixed release and a GitHub security
  advisory if users are at risk.
- **A compromised account:** remove it from the organization, review the audit log for new
  tokens, deploy keys, webhooks and workflow changes, then rotate every secret it could reach.
