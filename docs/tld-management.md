# Plan: managing domain endings

Status: agreed (2026-11-02). Goal: choose, add, remove and switch domain endings (TLDs) from
the CLI, refuse endings that cannot work or would break the machine, and make `.dotlane` the
default. Ships in `v0.3.0`.

## What exists today

- `config.toml` already takes `tld = "x"` or `tlds = ["x", "y"]`; the default is `dotlane`
  ([`config.go`](../internal/config/config.go)). Validation: the label pattern, no duplicates,
  and `local` refused.
- Every site answers on every ending (`proxy.go` builds one host per ending); DNS answers
  `*.<tld>` for each ([`dns.go`](../internal/dns/dns.go)).
- OS routing is per ending: `/etc/resolver/<tld>` on macOS, one `resolvectl domain` line with
  every `~tld` in the systemd unit on Linux, one NRPT rule per ending on Windows.
- The daemon reads the endings at start only. `install`/`refresh` restart it when `config.toml`
  is newer than the daemon (`stale` in `common.go`), so a config edit plus a refresh already
  applies a change; the routing steps (sudo/UAC) only run in `install`.
- Gaps: the only way to change endings is editing the file and re-running `install`; nothing
  removes the route of an ending dropped from the file (uninstall walks the *current* list, so
  it leaves `/etc/resolver/<old>` or an old NRPT rule behind); the only refused ending is `local`.

## Decisions

### Default: `.dotlane`, not `.dl`

`.dl` is two letters. Every two-letter ending is reserved for country codes (ISO 3166-1); `DL`
is unassigned today but can be assigned at any time. `.dotlane` is the brand, is not delegated
(re-checked against IANA's list when implemented) and is long enough that a collision is
unlikely. The development instance keeps `dldev`.


### Switching means choosing the primary ending

The list stays ordered and its first entry is the **primary** ending: the one `add`, `ls` and
`up` print, and the one a desktop app shows. Switching moves an ending to the front; the others
keep answering, so old links keep working until they are removed. A per-site ending
(`myapp.dotlane` but `api.test`) is out of scope.

### Blocklist plus warnings, no allowlist

An allowlist would forbid the private endings people already use (company names, `.dotlane`).
Instead, three tiers:

| Tier | Endings | Why |
| --- | --- | --- |
| **Refused** | `local` | Multicast DNS (RFC 6762): mDNSResponder and Avahi take it before `/etc/resolver` or systemd-resolved routing |
| | `localhost` | RFC 6761: browsers, systemd-resolved and macOS answer it themselves with loopback; Dotlane's DNS is never asked |
| | `invalid`, `onion`, `arpa`, `alt` | RFC 6761 / 7686 / 9476 and reverse DNS: resolvers and browsers must fail or special-case them |
| | Any delegated public TLD (`com`, `dev`, `app`, `io`, … about 1,450) | Routing it to 127.0.0.1 shadows real websites for the whole machine. A snapshot of IANA's list, embedded and refreshed by a `go generate` script |
| | Any two-letter ending | Reserved for country codes; may be delegated later |
| | `xn--…` | Internationalized endings; not worth the edge cases |
| **Warned** (needs `--force`) | `lan`, `home`, `corp`, `mail`, `localdomain`, `intranet`, `private` | Routers (OpenWrt, ISP boxes) and corporate DNS often answer these; Dotlane would hide the network's own names |
| **Suggested** | `dotlane` (default), `test`, `internal` | `test` is reserved by RFC 2606; `internal` is reserved by ICANN for private use (2024). Neither can collide with a public site |

## The commands

```
dotlane tld ls [--json]         endings, the primary marked, and whether each is routed
dotlane tld add <tld> [--force] validate, write config, route it (sudo/UAC), restart
dotlane tld rm <tld>            unroute it (sudo/UAC), write config, restart; never the last one
dotlane tld use <tld>           make it the primary; adds it first when missing
dotlane install --tld <tld>     choose the ending on first install (default: dotlane)
```

`rm` keeps the sites: they are names and ports, not tied to an ending. Old certificates for the
removed ending stay in Caddy's storage, unused, and go with `uninstall --purge`.

The daemon stays unprivileged and does not change routing, so the control API only gains
`primary_tld` in `/v1/status`; a desktop app runs `dotlane tld …` the way it runs `install`.

## Tasks

| # | Task |
| --- | --- |
| T1 | **Ending rules.** New `internal/tld` package: `Check(tld) (warning string, err error)` with the three tiers; embedded IANA snapshot and a `go generate` script that refreshes it; table tests per tier. `config.validate` calls it (warnings printed, not fatal, for a hand-edited file) |
| T2 | **Config writer.** `config.SetTLDs(path, []string)` rewrites the `tld`/`tlds` line in place, keeping comments and other keys (line-based like `SetDNSPort`); creates the file when missing; writes the `tlds = [...]` form. `Config.Primary()` returns `TLDs[0]` |
| T3 | **Switch the default.** `config.DefaultTLD` becomes `dotlane`; CLI help in `main.go` follows |
| T4 | **Remove one ending's route, per OS.** `unroute(tld)`: macOS removes `/etc/resolver/<tld>` when its content is Dotlane's; Linux rewrites the unit's `resolvectl domain` line and restarts it; Windows removes the NRPT rule matched by comment and namespace. Uninstall also removes routes for endings no longer in the file, found by that ownership check |
| T5 | **Add one ending's route, per OS.** `route(tld)` from the existing per-ending install steps, without running the whole install; one UAC prompt on Windows |
| T6 | **`dotlane tld` commands.** `ls`, `add`, `rm`, `use`: T1 check, T2 write, T5/T4 routing, the `Refresh` path to restart the daemon, and the `dotlane-check.<tld>` lookup to verify. Same step-by-step output as install |
| T7 | **`install --tld`.** Writes the chosen ending before installing; checked by T1. No interactive prompt |
| T8 | **Primary ending in output and API.** `add`/`up`/`ls` print the primary URL first; `/v1/status` gains `primary_tld`; `api.Site.URLs` ordered primary first |
| T9 | **Doctor.** Warn on a warned-tier ending; flag an orphaned Dotlane route with the fix (`dotlane tld rm <tld>` or `add`) |
| T10 | **Integration tests.** On macOS, Linux and Windows: add a second ending, resolve and curl it, `use` it, remove the first, check its route is gone, uninstall leaves nothing. |
| T11 | **Docs and copy.** `.dotlane` in README, `docs/`, npm README; `docs/cli.md` for `tld`; `security.md` with the tiers; changelog note |
| T12 | **Desktop roadmap.** "Domain endings: list, add, remove, choose the primary" under Next, waiting on T6 and T8 |

## Pull requests

These went in under the earlier `dev` branch flow ([`branching.md`](branching.md), History): each
branch came off `dev`, was squash-merged into it, and added its changelog line; those lines are
now the fragment [`changes/tld-management.md`](../changes/tld-management.md). Branches touching
the installer or DNS also ran the full tier on demand (`gh workflow run ci.yml --ref <branch>`).

| # | Branch | Tasks | CI | Needs |
| --- | --- | --- | --- | --- |
| 1 | `feat/tld-rules` | T1 | quick | — |
| 2 | `feat/tld-primary` | T2, T8 | quick | — |
| 3 | `feat/tld-routing` | T5, T4 (including the uninstall fix for dropped endings) | quick + manual full | — |
| 4 | `feat/tld-commands` | T6, T7, T9, T10 (commands) | quick + manual full | 1, 2, 3 |
| 5 | `feat/default-dotlane` | T3 with the default change and CLI help in `main.go`, T10 | quick + manual full | 2, 3 |
| 6 | `docs/dotlane-default` | T11 | none (docs only) | 4, 5 |

1, 2 and 3 can go in parallel. The uninstall fix in 3 is a bug in `v0.2.0`; if it should ship
before `v0.3.0`, it goes out as `v0.2.1` from a `release/v0.2` branch ([`branching.md`](branching.md),
Patches).

Outside this repository:

- **Website and Homebrew tap**: `.dotlane` examples, merged when `v0.3.0` is published, so the
  site never describes an unreleased default.
- **Desktop**: T12 in its `ROADMAP.md`.

## Release

A minor release, `v0.3.0`: the default ending changes. Tag **`v0.3.0-rc.1`** on `main` for
testers; a fix is a pull request into `main`, then `-rc.2`. Then the release pull request
(`scripts/changelog.sh release v0.3.0`) and the `v0.3.0` tag on its commit. Full steps in
[`releasing.md`](releasing.md).

## Open questions

- **Name-constrained CA** (core roadmap, Later): once the CA is limited to the configured
  endings, adding an ending means a new CA and a new trust prompt. `tld add` must then say so.
- **`.internal` on managed networks:** warn only when the network's DNS already answers it (a
  lookup before routing), not always.
