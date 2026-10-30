# Phase 1 plan — Go core on macOS

The first phase of [`roadmap.md`](roadmap.md), broken into milestones that each end with something
runnable. Goal: replace the Bash prototype with one Go binary that behaves the same and is easier to
install and repair. macOS only; the code is laid out so Linux and Windows (Phase 4) add files rather
than rewrite them.

Estimate: 4–6 weeks, one developer. Versions checked 2026-10-29.

## Decisions taken for this phase

| Topic | Choice | Why |
| --- | --- | --- |
| Language / toolchain | Go 1.27 | Already installed; Caddy is Go |
| HTTPS + local CA | Caddy v2.11 embedded as a library, configured with JSON through `caddy.Load` | Same engine as the prototype, no separate process; its admin endpoint is disabled |
| DNS | `github.com/miekg/dns` v1.1 | Stable and standard. Its successor (`codeberg.org/miekg/dns`) is still pre-1.0 |
| Trusting the CA | `github.com/smallstep/truststore` v0.13 | What Caddy itself uses; covers Keychain now, and Linux/Windows/NSS later |
| CLI | `github.com/spf13/cobra` v1.10 | Already a Caddy dependency; gives help and shell completions |
| Config | `github.com/pelletier/go-toml/v2` | Reads the `config.toml` the Bash script already uses, and `dotlane.toml` |
| CLI ↔ daemon | JSON over HTTP on a Unix socket in the state directory, mode 0600 | No TCP port to protect; the same API serves the desktop app later |
| Files | Config `~/.config/dotlane/`, state and CA `~/Library/Application Support/dotlane/`, logs `~/Library/Logs/dotlane/` | macOS conventions; config already lives there |

## Code layout

```text
cmd/dotlane/            main: CLI commands, also `dotlane serve` (the daemon)
internal/config/        config.toml + dotlane.toml parsing and validation
internal/sites/         site registry (state file), name/port rules
internal/dns/           *.<tld> responder on 127.0.0.1:<port>, A + AAAA, NXDOMAIN otherwise
internal/proxy/         builds Caddy's JSON config from the registry, reloads it in-process
internal/api/           Unix-socket server (daemon) and client (CLI)
internal/doctor/        checks and fixes, shared by `doctor` and `install`
internal/platform/      OS specifics behind one interface:
    darwin/             /etc/resolver, launchd agent, Keychain, port checks
```

## Milestones

### M1 — The daemon serves sites (≈ 1 week) — done 2026-10-29

Shipped as planned, plus: `tlds` (several endings) already honoured by the DNS responder and the
proxy, failed reloads roll the registry back, and a 2-second shutdown grace period so open
websockets (Vite's live reload) cannot hold a stop or restart. Verified with MyApp's running
Vite and FastAPI behind `https://myapp.traix:9443` and `https://api.traix:9443`, and by an
end-to-end test (`internal/daemon`). Known, by design: with a non-443 `https_port`, Caddy's HTTP
redirect omits the port; an unknown name gets a TLS alert rather than a page (friendlier answer in
M3).


- `dotlane serve`: starts the DNS responder and embedded Caddy, reads the site registry.
- Caddy config generated from the registry: `local_certs`, one route per site, `reverse_proxy`, CA
  storage in the state directory.
- `add`, `rm`, `ls` (also `--json`) through the socket; changes apply without a restart.
- **Done when** the daemon, run by hand on spare ports (DNS 10054, HTTPS 9443), serves
  `https://myapp.traix:9443` next to the prototype, in tests and with `curl`.

### M2 — Install and uninstall (≈ 1 week) — done 2026-10-30

Shipped as planned, with `--purge` instead of `--keep-ca`: uninstall always removes trust (keeping
a trusted CA after uninstalling is the riskier default) and keeps sites and the CA's keys unless
`--purge` is given. Install refuses, before changing anything, when another tool's file holds
`/etc/resolver/<tld>`; resolver files cannot carry a comment, so "ours" means "exactly the content
Dotlane writes". `service_name` lets the dev instance (`.dldev`, ports 10054/9443) install next to
a real one. Output is grouped per step with colour (`internal/ui`; `NO_COLOR` and
`CLICOLOR_FORCE` honoured).

Verified on this Mac with the dev instance: `curl` and Chrome reached `https://api.dldev:9443`
with no flags and no certificate warning; a second install asked for no password; launchd
restarted the daemon within a second after `kill -9`; uninstall `--purge` left no launchd job,
keychain entry, trust setting, resolver file or state, and the prototype kept working. Not yet
measured: the "under a minute on a clean VM" criterion.


- `dotlane install`: writes the resolver file(s), trusts the CA, installs and starts the launchd
  agent. Each admin step prints one sentence on what and why before asking for the password.
- `dotlane uninstall`: the reverse, leaving nothing behind (`--keep-ca` to keep trust).
- The daemon runs at login and restarts if it crashes; logs go to `~/Library/Logs/dotlane/`.
- **Done when** install → first working site takes under a minute on a clean macOS VM, and uninstall
  leaves `scutil --dns`, the Keychain and `launchctl list` as before.

### M3 — Conflict scan and `doctor` (≈ 1 week) — done 2026-10-30

Shipped: `dotlane doctor [--fix] [--json]` and the same checks as a pre-install gate. Ports are
traced to their process with `lsof`, `ps` and `launchctl list`, and named with a fix (the
prototype's Caddy and dnsmasq, Yerd, Herd, Valet, Docker, else the process and pid); ports held by
another user's process are caught by a bind test. All listeners on a port are reported, because
Caddy binds with `SO_REUSEPORT`: a second server can share 443 and split its traffic instead of
failing. A foreign `/etc/resolver/<tld>` is traced to whatever answers its port, or reported as
left behind. A taken default DNS port moves to the next free one (saved in `config.toml`, only
when nothing else blocks the install); an explicit one is a conflict. Install refuses before
changing anything; `--fix` runs install for what it repairs and leaves other tools alone. The
daemon is recognised by its launchd label even when it is not answering, and a loaded but stuck
service is restarted with `launchctl kickstart`.

Verified on this Mac: doctor names the prototype's Caddy on 80/443 and its dnsmasq on 10053 with
the right fixes; the reference-log case (Herd's leftover resolver, Yerd on 1053) and the others
are covered by tests. `doctor --fix` verified on the dev instance: with the resolver file missing
it ran install, which wrote only that file, and the second pass came back all green;
`uninstall --purge` then left nothing behind. Still open: a friendlier answer than a TLS alert
for unknown names (moved to M6).


- Before installing, and in `doctor`: other resolver files for the same ending; anything on 80, 443
  or the DNS port (with the owning process); known tools (Herd, Valet, Yerd, Docker on 443, the
  prototype's Caddy and dnsmasq).
- Picks a free DNS port automatically if the default is taken.
- `doctor --fix` repairs what it can (daemon stopped, resolver missing, CA not trusted) and
  explains the rest. `--json` output for scripts and the future app.
- **Done when** each conflict from the reference install log (Herd's resolver, Yerd on 1053) is
  detected with a correct fix message.

### M4 — Project files: `dotlane.toml`, `up` / `down` (≈ 1 week) — done 2026-10-30

Shipped: `dotlane up` / `down`, run from the project's directory or below it. Each site records
the project directory (symlinks resolved) that registered it; `ls` shows it. `up` checks every
name first and changes nothing if another project owns one, adopts a site added by hand, skips
the reload for unchanged sites, and removes the project's sites the file no longer lists. The
daemon enforces the same ownership rule, so `dotlane add` cannot take a project's name either
(HTTP 409). Several endings (`tlds = [...]`) already worked since M1/M2. The Bash prototype got
a minimal `up` / `down` (no ownership), so MyApp could switch now, before migration (M5).
Verified against the dev daemon with two scratch projects (adopt, conflict, re-run, stale site,
down) and in MyApp with the prototype.

Planned:

- `dotlane.toml` in a project (`[sites.<name>] port = …`); `dotlane up` registers its sites,
  `dotlane down` removes them. Sites remember which project registered them.
- Several endings (`tlds = [...]`) in `config.toml`; one resolver file each.
- Name collisions between projects are reported, not silently overwritten.
- **Done when** MyApp's `mise run domain` is replaced by a committed `dotlane.toml` and
  `dotlane up`.

### M5 — Migration from the prototype (≈ 3 days) — built 2026-10-30, to run on this Mac

Shipped: `dotlane migrate`. It reads the prototype without changing anything first (a site file
for an ending Dotlane is not set up for stops it), copies Homebrew Caddy's root certificate and
key into Dotlane's state before the daemon first starts, imports the `# Managed by dotlane` site
files (others are named and left out), stops Homebrew's Caddy and boots out and disables the
dnsmasq agent, then runs `dotlane install`. The resolver file matches as it is (port 10053) and
the CA is already trusted, so no step needs a password. If the install fails, Dotlane's service
is removed and the prototype restarted, CA trust untouched. On success it checks each site with
a TLS handshake against the system's trusted roots, points `~/.local/bin/dotlane` (the
prototype script) at the binary, and lists the prototype's files for the user to delete.
Re-running is safe. Verified: the embedded Caddy accepts the prototype's root (it issues its own
intermediate under it) and curl trusts the result with no `--cacert`; the flow is covered by
tests with a fake launchd/Homebrew, including the rollback.

Planned:

- `dotlane migrate`: imports `~/.config/caddy/sites/*.caddy` (the `# Managed by dotlane` line),
  reuses the Homebrew Caddy service's root CA from `/opt/homebrew/var/lib/caddy/pki/authorities/local/`
  (the trusted one; `~/Library/Application Support/Caddy` holds a different, untrusted CA from
  running `caddy` as a plain command) so nothing needs trusting again, stops the Homebrew Caddy service and the dnsmasq agent, and says
  which prototype files can be deleted.
- **Done when** this Mac moves from the prototype to the binary with no new password prompt and
  `https://myapp.traix` keeps working.

### M6 — Hardening and release candidate (≈ 1 week)

- Tests: unit tests per package; an integration test on GitHub's macOS runners (they allow `sudo`)
  covering install → add → HTTPS request → uninstall.
- Proxy bound to loopback by default; socket and state permissions checked by `doctor`.
- Error messages reviewed end to end; `--help` for every command; `docs/` updated from Bash to Go.
- Licence chosen and added (open decision in the roadmap).
- **Done when** the roadmap's Phase 1 exit criteria pass: MyApp runs on the binary end to end,
  and install → first site takes under a minute on a clean Mac.

## Command surface at the end of Phase 1

```text
dotlane install | uninstall [--purge]
dotlane add <name> <port> | rm <name> | ls [--json]
dotlane up | down                 # from ./dotlane.toml
dotlane doctor [--fix] [--json]
dotlane migrate                   # from the Bash prototype
dotlane serve                     # the daemon; launchd runs it
dotlane version
```

`add`, `rm`, `ls` and `doctor` keep the prototype's names and output, so the MyApp task and the
docs keep working during the switch.

## Out of scope for Phase 1

PHP, static sites and managed processes (Phase 3); packages and signing (Phase 2); Linux and Windows
(Phase 4); the desktop app (Phase 5).

## Before starting

- Repository home and Go module path (for example `github.com/traix/dotlane`). The roadmap's
  Homebrew tap and npm scope follow from it.
- Licence: Apache-2.0 or MIT. Needed before the code is public, not before it is written.
