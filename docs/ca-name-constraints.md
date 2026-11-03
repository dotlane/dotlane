# Plan: a certificate authority limited to the domain endings

Status: proposed (2026-11-02). Core roadmap, Later: "A name-constrained CA, limited to the
configured endings". Goal: the local certificate authority can sign only for the configured
endings, so its key, if stolen, cannot impersonate any other website to this machine. Ships after
`v0.3.0` (the domain endings, [`tld-management.md`](tld-management.md)).

## Why

Dotlane trusts its local root in the system store (and NSS on Linux). Today that root carries no
name constraints, so whoever reads its key, or the intermediate's (both in the state folder,
mode 0600), can mint a trusted certificate for `bank.com`. [`security.md`](security.md) states
this. An X.509 name constraint (RFC 5280, 4.2.1.10) on the root limits every certificate below it
to the listed DNS names: a certificate for `bank.com` chained to it is rejected, whoever signed it.

## What exists today

- **Caddy makes the CA.** The `pki` app's `local` CA ([`proxy.go`](../internal/proxy/proxy.go))
  generates a root (ECDSA P-256, about 10 years) and a 7-day intermediate on first start, in
  `<state>/caddy/pki/authorities/local/`, and renews the intermediate itself. Caddy 2.11.7's
  templates (`caddypki/certificates.go`) set **no name constraints**, and there is no option for
  them.
- **Caddy accepts a root it did not make**: `pki.certificate_authorities.<id>.root =
  {certificate, private_key}`. It then still generates and renews intermediates from that root.
  If `intermediate` is given as well, Caddy never renews it.
- **Caddy reloads a stored intermediate without checking which root signed it**
  (`loadOrGenIntermediate`), and the internal issuer files its leaf certificates under the CA's
  ID (`InternalIssuer.IssuerKey` returns `ca.ID`).
- **Trust** is per OS and works on one file, `proxy.CADir(cfg)/root.crt`: `add-trusted-cert` to
  the System keychain on macOS, the system anchors and NSS on Linux, the machine's Root store
  through one UAC prompt on Windows. Uninstall removes it by that file's fingerprint.
- **Endings** change through `dotlane tld add | rm | use`, which save `config.toml`, call
  `Installer.SyncRoutes` (sudo or UAC) and restart the daemon.

## Measured: do clients enforce a constraint on the root?

RFC 5280 lets a verifier ignore name constraints on the trust anchor itself. Measured on this
Mac (2026-11-02): a root constrained to `dotlane` (and to no IP addresses), an **unconstrained**
intermediate like Caddy's, and leaf certificates for `web.dotlane` and `web.example.com`:

| Verifier | `web.dotlane` | `web.example.com` |
| --- | --- | --- |
| Go `crypto/x509` | accepted | rejected: not permitted by any constraint |
| macOS SecTrust (`security verify-cert`; Safari, system tools) | accepted | rejected (an unconstrained control root accepts it) |
| LibreSSL 3.3.6 (`/usr/bin/curl`, `/usr/bin/openssl`) | accepted | rejected: permitted subtree violation |
| OpenSSL 3.5.9, 4.1.0 | accepted | rejected: permitted subtree violation |
| Node 25.9 (TLS handshake) | accepted | rejected: permitted subtree violation |
| Python (OpenSSL 4.1, TLS handshake) | accepted | rejected: permitted subtree violation |

So a constraint on the root alone protects these clients, even with Caddy's unconstrained
intermediates. **Not measured yet** (C1 below): Chrome's own verifier on each OS, Firefox and NSS,
Windows CryptoAPI/Schannel (Edge, `curl.exe`), and Java (its PKIX validator is known to skip a
trust anchor's constraints; such a client would still accept the right names, just without the
protection). A client that **rejects** a constrained root for an allowed name would change the
design (see Decisions), so C1 comes first.

## Decisions

### Dotlane makes the root; Caddy keeps making intermediates

Dotlane generates the root itself (`crypto/x509`) and hands it to Caddy as `root`; Caddy keeps
generating and renewing 7-day intermediates from it, as now. The root:

- ECDSA P-256, 10 years, common name `Dotlane Local Authority - ECC Root <n>` (`n` its
  generation, so the System keychain and the Root store show which one is current);
- `nameConstraints`, **critical**: permitted DNS names = the configured endings (`dotlane` permits
  `dotlane` and every name under it); excluded IP ranges `0.0.0.0/0` and `::/0`, so it cannot
  vouch for an IP address either;
- `basicConstraints` CA with path length 1: it signs intermediates, and they sign only leaves.

Constraining the intermediates too adds nothing: both keys sit in the same folder with the same
mode, and the root's constraint already covers the whole path.

If C1 finds a mainstream client that rejects a critical constraint on a root, the fallback is to
mark it non-critical (as the CA/Browser Forum allows for compatibility), at the cost of clients
that skip non-critical extensions they do not know.

### One generation per set of endings

A **generation** is a root, its key, and the endings it covers, in
`<state>/ca/<n>/{root.crt,root.key,endings.json}` (private, through `fsperm`); `<state>/ca/active`
names the current one. Its Caddy CA ID is `dotlane-<n>`, so Caddy keeps that generation's
intermediate and leaf certificates apart from every other's: switching generations needs no
deleting, and Caddy never serves a chain from the previous root.

**Invariant: the active generation covers exactly the configured endings.** Not a superset:
`tld rm` already asks for administrator rights to remove the route, so narrowing the CA in the
same prompt costs nothing extra, and "covers exactly" is easy to state and to check in `doctor`.
`tld use` changes nothing (same set). The daemon reports the active generation's endings in
`/v1/status` (`ca_tlds`), for `doctor` and the desktop app.

### What `dotlane tld add` must do

When the ending is new to the active generation (every `add` of a new ending):

1. **Check the ending** (`tld.Check`), as now.
2. **Generate the next generation** for the new set, inactive, in the state folder. No privileges.
3. **Save `config.toml`** (`SetTLDs`), as now.
4. **Explain, then one administrator step** (one sudo session on macOS and Linux, one UAC prompt
   on Windows):
   - route the new ending (`SyncRoutes`), as now;
   - trust the new root: System keychain / system anchors and NSS / machine Root store;
   - remove the previous generation's root from the same places.

   The explanation says why: *"Adding .test means a new certificate authority. Dotlane's is
   limited to its endings, so its key cannot be used to impersonate other websites; each change
   of endings replaces it, and every site gets a new certificate."*
5. **Activate** the new generation and restart the daemon (`Refresh`): Caddy loads the new root
   under `dotlane-<n>`, makes an intermediate, and issues new leaf certificates (registered sites
   up front, others at first handshake). Between steps 4 and 5, for a few seconds, sites present
   certificates from the removed root.
6. **Verify**: a TLS handshake for `dotlane-check.<ending>` on every ending, against the system's
   trust (`verifyTLS` in `common.go`, which only macOS's `migrate` uses today; C5 gives Linux and
   Windows the same check), plus the DNS lookup `tld add` already does.
7. **Delete the previous generation's files** once step 6 passed. Until then they stay, so
   `dotlane install` can repair from either.

Run again with nothing to change, `tld add` checks that the active generation covers the endings
and that its root is trusted, and does nothing else, so **re-running is the repair** for a
declined prompt or a failed step, as for routes. `tld rm` does the same with the smaller set
(steps 2 to 7, with the route removed in step 4).

Browsers keep TLS sessions and cached intermediates for a while; a site opened before the change
may need a reload after it. `doctor` names that if a handshake fails right after a change.

### Existing installs

Installs from before this change trust Caddy's unconstrained `local` root. That cannot change
without a password, so `install --refresh` and the automatic upgrade leave it alone; the daemon
keeps the `local` CA while `<state>/ca/active` is missing.

- `dotlane install` (and `doctor --fix`) **migrates**: it makes generation 1 for the configured
  endings and runs steps 4 to 7 with the `local` root as the previous one, then deletes
  `<state>/caddy/pki/authorities/local/` and Caddy's `local` certificates.
- `doctor` warns until then: *"Certificate authority can sign for any website; dotlane install
  limits it to your endings"* (repairable).
- A hand-edited `config.toml` with an ending the active generation does not cover: the daemon
  logs it, `doctor` fails with the fix (`dotlane tld add <ending>`, or `dotlane install`), and
  sites on that ending fail certificate checks until then.

### Uninstall

Uninstall removes **every** Dotlane root it finds, not only the active one (the same rule as
routes, #31): every `<state>/ca/<n>/root.crt`, the legacy `local` root, and, by name, any
`Dotlane Local Authority` root of this service left in the System keychain, the system anchors,
NSS or the Root store.

## Tasks

| # | Task |
| --- | --- |
| C1 | **Client check.** `scripts/ca-constraints-check.sh` (and `.ps1`): builds the chain above and checks, on each CI OS, Chrome (headless, against the system trust), Firefox/NSS (`vfychain`), `curl` (Schannel on Windows), Edge or CryptoAPI (`certutil -verify`) and Java (`keytool`/a small `HttpsURLConnection` check). Run on demand in the full tier; fills in the table above. Decides critical or not |
| C2 | **`internal/ca`.** `Generate(endings) (Generation, error)`, `Covers(root, endings) bool`, load, store, `Activate(n)`, `Previous()`; files through `fsperm`. Table tests: constraints present and critical, IP excluded, path length, a leaf for a covered name verifies and one for another name does not (Go's verifier, as measured) |
| C3 | **Caddy on the active generation.** `proxy.go` configures `dotlane-<n>` with `root` from the active generation, and the internal issuer with `"ca": "dotlane-<n>"`; the legacy `local` CA while there is none. `proxy.CADir` becomes the active root's folder. `/v1/status` gains `ca_tlds`. The daemon logs uncovered endings |
| C4 | **Trust and untrust a given root, per OS.** `trustCA(root)` / `untrustCA(root)` instead of the one `root.crt`; Windows puts trust-new and untrust-old in one elevated step. Uninstall removes every Dotlane root (all generations, legacy, and by name). Integration: after install, a leaf for `web.example.com` signed with the intermediate key from the state folder is **rejected** by `curl` (and Safari's SecTrust, Schannel) on each OS: the protection, end to end |
| C5 | **Rotation.** Steps 2 to 7 for `tld add` and `tld rm`, shared with `install`'s migration and `doctor --fix`; idempotent re-run. Integration: `tld add` asks once, sites on both endings verify against the system trust, the old root is gone from the store; `tld rm` narrows; an upgrade from a `local`-root install keeps working until `dotlane install` migrates it |
| C6 | **Doctor.** Unconstrained (legacy) CA: warning, repairable. Active generation not covering the config: failure with the fix. Previous roots still trusted: warning. Root expiring within a year: warning, `dotlane install` rotates |
| C7 | **Docs.** `security.md` (the CA section: what is protected now, which clients enforce it, from C1), `how-it-works.md`, `cli.md` (`tld add`/`rm` ask for a password once more, and why), `setup*.md`; changelog lines in the fragment |

## Pull request

One pull request for the whole feature, `feat/ca-name-constraints` from `main`, with **one commit
per task**, in order ([`branching.md`](branching.md): one pull request per feature, one commit
per task). Each commit builds and passes `scripts/check.sh` on its own, so the history of the
branch reads as the plan and a failing step is easy to find:

| Commit | Task | What it leaves working |
| --- | --- | --- |
| 1 | C1 | the client check script; the measured table filled in; critical or not decided |
| 2 | C2 | `internal/ca`, unused yet |
| 3 | C3 | the daemon on the active generation, `local` while there is none (nothing activates one yet) |
| 4 | C4 | trust and untrust by root; uninstall removes every Dotlane root |
| 5 | C5 | rotation in `tld add`/`rm`, `install`'s migration |
| 6 | C6 | the doctor checks |
| 7 | C7 | the docs |

Commits 4 to 7 each add their lines to the branch's fragment, `changes/ca-name-constraints.md`,
where a user would notice the change.

- **Draft while it grows:** pushed after every commit, so the quick tier runs once it is marked
  ready. Run the full tier on demand (`gh workflow run ci.yml --ref feat/ca-name-constraints`)
  after commits 3, 4 and 5: they change the daemon, the trust store and the installer on every OS.
- **Merging:** the squash merge turns the seven commits into one on `main`, titled for the
  feature, so the pull request description sums up all seven; the step-by-step history stays on
  the pull request.
- **Review:** commit by commit (GitHub's "Commits" tab), since each is one task.

Release: a minor version after `v0.3.0` (`v0.4.0`), with a release candidate: it changes what
`install`, `tld add` and `tld rm` ask for, and every existing install migrates on its next
`dotlane install`.

## Open questions

- **Firefox on macOS and Windows** uses its own store unless `security.enterprise_roots.enabled`
  is on (`doctor` already hints at it); with a new root per change, that hint matters more.
- **Root key at rest.** The constraint limits what a stolen key can do; it does not make the key
  harder to steal. A Keychain- or TPM-held key would, but Caddy needs the key in a file to renew
  intermediates. Out of scope here.
- **Many endings at once.** `tld add a b` would rotate once for both; worth adding with C5, since
  each rotation is a prompt.
