# Plan: managing domain endings

Status: proposed (2026-11-02). Goal: choose, add, remove and switch domain endings (TLDs) from
the CLI, refuse endings that cannot work or would break the machine, and make `.dotlane` the
default instead of `.traix`.

## What exists today

- `config.toml` already takes `tld = "x"` or `tlds = ["x", "y"]`; the default is `traix`
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
is unassigned today but can be assigned at any time, and ICANN will not delegate a two-letter
generic. `.dotlane` is the brand, is not delegated (to be re-checked against IANA's list when
implemented) and is long enough that a collision is unlikely. The development instance keeps
`dldev`.

Existing installs that never set `tld` were relying on the `traix` default. They must keep
`.traix`, so install/refresh pins it (see T3) before the default changes under them.

### Switching means choosing the primary ending

The list stays ordered and its first entry is the **primary** ending: the one `add`, `ls` and
`up` print, and the one a desktop app shows. Switching moves an ending to the front; the others
keep answering, so old links keep working until they are removed. One per-site ending
(`myapp.dotlane` but `api.test`) is out of scope: every site answers on every ending, as now.

### Blocklist plus warnings, no allowlist

An allowlist would forbid the private endings people already use (company names, `.traix`).
Instead, three tiers:

| Tier | Endings | Why |
| --- | --- | --- |
| **Refused** | `local` | Multicast DNS (RFC 6762): macOS's mDNSResponder and Avahi take it before `/etc/resolver` or systemd-resolved routing |
| | `localhost` | RFC 6761: browsers, systemd-resolved and macOS answer it themselves with loopback; Dotlane's DNS is never asked, and a cert check still works, so routing it only confuses `doctor` |
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

Sizes: S under half a day, M about a day, L two or more.

| # | Task | Size | Needs |
| --- | --- | --- | --- |
| T1 | **Ending rules.** New `internal/tld` package: `Check(tld) (warning string, err error)` with the three tiers; embedded IANA snapshot (`tlds-alpha-by-domain.txt`) and a `go generate` script that refreshes it; table tests for each tier. `config.validate` calls it, so a hand-edited file is checked too (warnings only printed, not fatal, for files) | M | — |
| T2 | **Config writer.** `config.SetTLDs(path, []string)`: rewrites the `tld`/`tlds` line in place, keeping comments and other keys (go-toml does not keep comments, so line-based like `SetDNSPort`); creates the file when missing; always writes the `tlds = [...]` form. `Config.Primary()` returns `TLDs[0]` | S | — |
| T3 | **Pin `.traix` for existing installs, then switch the default to `.dotlane`.** In install/refresh/upgrade: when the config sets no ending and the OS has Dotlane's route for `traix` (resolver file with Dotlane's content, systemd unit, NRPT rule with Dotlane's comment), write `tlds = ["traix"]` with a comment saying why. Then change the default in `config.go` | M | T2 |
| T4 | **Remove one ending's route, per OS.** Split the per-ending part of uninstall into `unroute(tld)`: macOS removes `/etc/resolver/<tld>` when its content is Dotlane's; Linux rewrites the unit's `resolvectl domain` line and restarts it; Windows removes the NRPT rule matched by comment and namespace. Uninstall then also removes routes for endings no longer in the file, found by that ownership check (`/etc/resolver/*` content, NRPT comment) | L | — |
| T5 | **Add one ending's route, per OS.** Expose the existing per-ending install steps (`installResolver`, the unit rewrite, `addRuleBody`) as `route(tld)` without running the whole install; Windows batches into one UAC prompt as install does | M | — |
| T6 | **`dotlane tld` commands.** `ls`, `add`, `rm`, `use` in `cmd/dotlane`: T1 check, T2 write, T5/T4 routing, then the `Refresh` path to restart the daemon and the existing lookup check (`dotlane-check.<tld>`) to verify. Same step-by-step output and `Ask` explanations as install. `--json` for `ls` | M | T1, T2, T4, T5 |
| T7 | **`install --tld`.** Writes the chosen ending before installing; refuses/warns via T1. No interactive prompt (install must stay scriptable); the default is `.dotlane` | S | T1, T2, T3 |
| T8 | **Primary ending in output and API.** `add`/`up`/`ls` print the primary URL first; `/v1/status` gains `primary_tld`; `api.Site.URLs` ordered primary first | S | T2 |
| T9 | **Doctor.** Warn on an ending in the warned tier; flag an orphaned Dotlane route (routed but not in the config) with the fix `dotlane tld rm <tld>` or `dotlane tld add <tld>` | S | T1, T4 |
| T10 | **Integration tests.** In `integration.sh`, `integration-linux.sh`, `integration-windows.ps1`: add a second ending, resolve and curl it, `use` it, remove the first, check its route is gone, uninstall leaves nothing. Upgrade test: an install with no `tld` keeps `.traix` | M | T6, T3 |
| T11 | **Docs and copy.** Replace `.traix` with `.dotlane` in README, `docs/`, CLI help (`main.go`), npm README, website `index.html`, Homebrew tap; `docs/cli.md` for `tld`; `security.md` "Domain ending choice" with the tiers; CHANGELOG entry noting that existing installs keep `.traix` | M | T6 |
| T12 | **Desktop roadmap.** Add "Domain endings: list, add, remove, choose the primary" to the desktop app's Next, waiting on T6 and T8 | S | T8 |

Order: T1, T2, T4 and T5 can be done in parallel; then T3, T6, T7, T8; then T9, T10, T11, T12.
T3 must ship in the same release as the default change, never after it.

## Open questions

- **Name-constrained CA** (core roadmap, Later): once the CA is limited to the configured
  endings, adding an ending means a new CA and a new trust prompt. `tld add` must then say so.
- **`.internal` on managed networks:** worth suggesting, but some companies already use it
  internally; the warning should fire only when the network's DNS answers it (a lookup before
  routing), not always.
