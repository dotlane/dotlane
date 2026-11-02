# Compatibility: the core on every OS

Where the `dotlane` binary stands on each operating system, item by item, so the work to run it
on Linux and Windows can be tracked. The plan behind it is in the [core roadmap](roadmap.md#later);
this page is the checklist. Update a row in the same pull request that changes it.

Last checked: 2026-11-02, with CI green on branch `feat/cross-platform`
([dotlane/dotlane#17](https://github.com/dotlane/dotlane/pull/17)).

## Summary

| OS | Where it stands |
| --- | --- |
| **macOS** (arm64, x64) | Shipped in `v0.1.0`. Signing and notarization are the open item. |
| **Linux** (amd64, arm64) | Installer, doctor, upgrade and uninstall done. End to end on CI on Ubuntu, Fedora and Arch (x64), and by hand on arm64 VMs of all three. Released as archives, deb, rpm, Arch packages and npm, from the next release. |
| **Windows** (amd64, arm64) | Installer, doctor, upgrade and uninstall done, end to end on CI's `windows-latest` in PowerShell 7 and 5.1, as an administrator. Not yet run on a desktop with a real UAC prompt, nor as a user without admin rights (I7, I10). Released as zip and npm, from the next release. |
| **WSL2** | Documented in [`wsl.md`](wsl.md): Dotlane on Windows with the dev server in WSL is the supported setup. |

Setup per OS: [macOS](setup.md), [Linux](setup-linux.md), [Windows](setup-windows.md).

## Status values

- **done**: works, and a test on that OS covers it.
- **partial**: works in part, or works but is not tested on that OS.
- **todo**: not started.
- **—**: does not apply.

## Matrix

### Build and release

| ID | Item | macOS | Linux | Windows | Notes |
| --- | --- | --- | --- | --- | --- |
| B1 | Cross-compiles with `CGO_ENABLED=0` | done | done | done | Also builds for FreeBSD (no installer there). |
| B2 | `go vet` and `govulncheck` on CI | done | done | done | `scripts/check.sh` runs both for all three OSes. |
| B3 | Unit tests run on CI | done | done | done | Linux in `test` (`go test -race ./...`), Windows in `test-windows` (without `-race`, which needs cgo). |
| B4 | GoReleaser archives | done | done | done | tar.gz on macOS and Linux, zip with `dotlane.exe` on Windows. Linux and Windows come from a second build (`dotlane-linux-windows`), so the Homebrew cask, which filters by build, stays macOS-only. |
| B5 | npm platform packages | done | done | done | `@dotlane/{linux,win32}-{x64,arm64}`; `npm-build.mjs` checks ELF and PE headers and requires all six binaries. Published from the next release; trusted publishing must be set up on the four new packages first. |
| B6 | Other package managers | partial | done | todo | macOS: Homebrew cask built, on hold. Linux: every pull request installs the snapshot's .deb on Ubuntu, .rpm on Fedora and Arch package on Arch with the distribution's package manager, runs the end-to-end test from `/usr/bin/dotlane`, and checks that removing the package leaves nothing (`linux-packages`). Windows: Scoop and winget need their own repositories. |
| B7 | Signed binaries | todo | — | todo | macOS: Developer ID and notarization (roadmap Now). Windows: Authenticode, or SmartScreen warns. |

### Daemon (shared code)

| ID | Item | macOS | Linux | Windows | Notes |
| --- | --- | --- | --- | --- | --- |
| D1 | DNS responder (`internal/dns`) | done | done | done | Listens on `127.0.0.1:53` on Windows, where NRPT takes no port (`config.DefaultDNSPort`). |
| D2 | Embedded Caddy and its CA (`internal/proxy`) | done | done | done | Ports below 1024 are bound wide only on macOS; loopback elsewhere (`proxy.ListenAddrs`). |
| D3 | Control API over a Unix socket | done | done | done | AF_UNIX everywhere. Limits per OS (`internal/daemon/socket_*.go`). On Windows the state folder's ACL (owner and SYSTEM only) protects the socket and the CA key (`internal/fsperm`). |
| D4 | State and config locations | done | done | done | Linux: `$XDG_CONFIG_HOME/dotlane/config.toml`, state in `$XDG_STATE_HOME/dotlane`. Windows: `%AppData%\dotlane\config.toml`, state in `%LocalAppData%\dotlane`. |
| D5 | Paths shown to the user | done | done | done | `~` shortening uses the OS separator. |
| D6 | Shutdown | done | done | done | Windows: the scheduled task ends the process; a stale socket is cleared on the next start. |
| D7 | Domain ending rules | done | done | done | `.local` is refused on every OS (multicast DNS). |

### Install, doctor, uninstall (`internal/install`)

| ID | Item | macOS | Linux | Windows | Notes |
| --- | --- | --- | --- | --- | --- |
| I1 | An `Installer` for the OS | done | done | done | `darwin.go`, `linux.go`, `windows.go`; shared helpers in `common.go`. |
| I2 | Login service | done | done | done | launchd agent; systemd user unit; Task Scheduler task (S4U, restart on failure). |
| I3 | Send `*.<tld>` to Dotlane's DNS | done | done | done | `/etc/resolver`; a systemd unit giving systemd-resolved a dummy link with only `~<tld>` (a `resolved.conf.d` server becomes a default route); NRPT rules. |
| I4 | Trust the CA: system store | done | done | done | System keychain; `update-ca-certificates` / `update-ca-trust`; the machine's Root store (`curl.exe` trusts it on CI, through Schannel). |
| I5 | Trust the CA: browsers with their own store | partial | done | partial | Linux adds it to `~/.pki/nssdb` and Firefox profiles with `certutil`. Firefox on macOS and Windows reads the system store when `security.enterprise_roots.enabled` is on; not checked. |
| I6 | Ports 80 and 443 without root | done | done | done | Linux: `/etc/sysctl.d/50-dotlane-<service>.conf`. Windows: allowed; doctor names IIS, `http.sys` and other holders. |
| I7 | Administrator steps, each explained | done | done | partial | sudo; sudo; one elevated PowerShell script (one UAC prompt, none when already elevated). CI's runner is already elevated, so only that path runs there; the UAC prompt, a standard user and another admin account are unit-tested only. |
| I8 | Conflict scan | done | done | done | `lsof`; `ss` or `/proc`; `Get-NetTCPConnection` / `Get-NetUDPEndpoint`. The tools it names are unit-tested on every OS. |
| I9 | `doctor` and `doctor --fix` | done | done | done | |
| I10 | Refresh and upgrade on the next command | done | done | partial | Windows: install, `install --refresh` and the next command each restart the daemon into a new build on CI, as an administrator. Without admin rights the restart relies on the task's permissions for the user; unit-tested only. If it needs admin rights, the automatic upgrade says to run `dotlane install --refresh`. |
| I11 | `uninstall [--purge]` leaves nothing | done | done | done | Windows: no task, NRPT rule, certificate or file is left (checked on CI). |
| I12 | `migrate` from the Bash prototype | done | — | — | The prototype was macOS-only. |

### Testing

| ID | Item | macOS | Linux | Windows | Notes |
| --- | --- | --- | --- | --- | --- |
| T1 | Installer tests with fakes | done | done | done | `darwin_test.go`, `linux_test.go`, `windows_test.go`. |
| T2 | End to end: install, HTTPS through the system's DNS and trust, upgrade, uninstall | done | done | done | `scripts/integration.sh`, `integration-linux.sh`, `integration-windows.ps1` (in PowerShell 7 and 5.1). The UAC path is I7. |
| T3 | Distributions | — | done | — | `integration-linux.sh` on CI: Ubuntu on the runner, Fedora and Arch in containers that boot systemd (`scripts/integration-linux-container.sh`); Arch pinned to an image digest and its day of the Arch Linux Archive. Also passed by hand on arm64 VMs (2026-11-02). |
| T4 | npm packages end to end | done | done | done | `scripts/npm-e2e.sh` on each OS's runner; on Windows under Git Bash. |

### WSL2

| ID | Item | Status | Notes |
| --- | --- | --- | --- |
| W1 | Dev server in WSL, browser on Windows | done | Dotlane on Windows reaches WSL through `localhost` forwarding. Documented; not tested on CI. |
| W2 | Dotlane inside WSL, browser in WSL | partial | The Linux installer, once WSL runs systemd and systemd-resolved. Documented; not tested. |
| W3 | Dotlane inside WSL, browser on Windows | — | Not supported; documented. |

## Decisions taken (2026-11-02)

- **Order:** shared groundwork, then Linux and Windows together.
- **Windows port 53:** required; no hosts-file fallback for now. `doctor` names what holds it.
- **Windows control channel:** AF_UNIX, protected by the state folder's ACL.
- **Linux state directory:** `$XDG_STATE_HOME/dotlane`; macOS unchanged.
- **Linux without systemd-resolved:** refused with the steps to enable it; NetworkManager-dnsmasq
  may come later.
- **Linux privileged ports:** the sysctl, since `setcap` would be lost on every package upgrade.

## Still open

- Windows code signing and macOS notarization (B7).
- Scoop and winget manifests (B6).
- A Windows desktop run with a real UAC prompt, and as a user without admin rights (I7, I10).
