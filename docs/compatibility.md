# Compatibility: the core on every OS

Where the `dotlane` binary stands on each operating system, item by item, so the work to run it
on Linux and Windows can be tracked. The plan behind it is in the [core roadmap](roadmap.md#later);
this page is the checklist. Update a row in the same pull request that changes it.

Last checked: 2026-11-02, against `main` at `a851a7b`.

## Summary

| OS | Where it stands |
| --- | --- |
| **macOS** (arm64, x64) | Shipped in `v0.1.0`. Installer, doctor, upgrade and uninstall done and tested end to end on CI. Signing and notarization are the open item. |
| **Linux** (amd64, arm64) | Builds and vets. The shared packages (DNS, proxy, API, sites, config, project, ui) pass `go test -race` on CI's Ubuntu runner, but nothing has run end to end. No installer: `dotlane install` says Linux is not supported. Not released. |
| **Windows** (amd64, arm64) | Builds and vets (checked locally, not on CI). No test has ever run on Windows. No installer. Not released. |
| **WSL2** | Not started. Same binary as Linux; needs the Windows side (browser, DNS, trust) documented or automated. |

## Status values

- **done**: works, and a test on that OS covers it.
- **partial**: works in part, or works but is not tested on that OS.
- **todo**: not started.
- **—**: does not apply.

## Matrix

### Build and release

| ID | Item | macOS | Linux | Windows | Notes |
| --- | --- | --- | --- | --- | --- |
| B1 | Cross-compiles with `CGO_ENABLED=0` | done | done | done | Also builds for FreeBSD. Checked by hand on 2026-11-02 for amd64 and arm64. |
| B2 | `go vet` and `govulncheck` on CI | done | done | todo | `scripts/check.sh` runs them for `linux` and `darwin` only. |
| B3 | Unit tests run on CI | done | partial | todo | Linux runs every package except `internal/install` (darwin-only files). Windows never runs them. |
| B4 | GoReleaser archives | done | todo | todo | `.goreleaser.yaml` has `goos: [darwin]`. Windows wants `.zip` and `dotlane.exe`. |
| B5 | npm platform packages | done | todo | todo | `scripts/npm-build.mjs` only knows `darwin-*` and checks Mach-O headers; it needs ELF and PE checks, and the shim `dotlane.exe`. |
| B6 | Other package managers | partial | todo | todo | macOS: Homebrew cask built, on hold. Linux: deb/rpm (GoReleaser nfpm), maybe Homebrew on Linux. Windows: Scoop, winget. |
| B7 | Signed binaries | todo | — | todo | macOS: Developer ID and notarization (roadmap Now). Windows: Authenticode, or SmartScreen warns. |

### Daemon (shared code)

| ID | Item | macOS | Linux | Windows | Notes |
| --- | --- | --- | --- | --- | --- |
| D1 | DNS responder (`internal/dns`) | done | partial | partial | Portable Go. Windows' NRPT takes no port, so on Windows it must listen on `127.0.0.1:53` (see I3). |
| D2 | Embedded Caddy and its CA (`internal/proxy`) | done | partial | partial | Portable. `ListenAddrs` binds 80/443 on every interface because of a macOS rule; Linux and Windows can stay on loopback. `thisMacOnly` guards the wide listener. |
| D3 | Control API over a Unix socket (`internal/api`, `internal/daemon`) | done | partial | partial | Windows has AF_UNIX since 10 (1803), but `os.Chmod(socket, 0o600)` does nothing there: the socket needs an ACL, or a named pipe instead. `maxSocketPath` (103) and its error message are macOS's; Linux allows 107. |
| D4 | State and config locations (`internal/config`) | done | partial | partial | State is `os.UserConfigDir()/dotlane`. On Linux that is `~/.config/dotlane`, the same folder as `config.toml`: keys, sites and socket would sit with the config. `$XDG_STATE_HOME` (`~/.local/state/dotlane`) fits better. Windows: `%AppData%\dotlane`, config at `~\.config\dotlane\config.toml`, which is unusual there. |
| D5 | Paths shown to the user | done | done | todo | `sites.tilde` and `ui.Printer` test `home + "/"`, so `~` shortening never matches on Windows. |
| D6 | Shutdown signals | done | done | partial | `SIGTERM` compiles on Windows but a Windows service stops through the service control manager (`golang.org/x/sys/windows/svc`). |
| D7 | Domain ending rules | done | done | done | `.local` is refused (Bonjour on macOS, Avahi on Linux). The message names macOS only. |

### Install, doctor, uninstall (`internal/install`)

| ID | Item | macOS | Linux | Windows | Notes |
| --- | --- | --- | --- | --- | --- |
| I1 | An `Installer` for the OS | done | todo | todo | `install/darwin.go`; `install/other.go` returns "not supported" on everything else. |
| I2 | Login service | done | todo | todo | macOS: launchd user agent. Linux: systemd user unit, plus a fallback without systemd (or a clear "unsupported"). Windows: a service or a scheduled task at logon. |
| I3 | Send `*.<tld>` to Dotlane's DNS | done | todo | todo | macOS: `/etc/resolver/<tld>` with a port. Linux: systemd-resolved drop-in (`DNS=127.0.0.1:<port>`, `Domains=~<tld>`), NetworkManager-dnsmasq fallback. Windows: NRPT rule, which needs port 53. |
| I4 | Trust the CA: system store | done | todo | todo | macOS: System keychain. Linux: `update-ca-certificates` (Debian, Ubuntu, Arch) or `update-ca-trust` (Fedora); `smallstep/truststore` already handles both and Windows. Windows: `Root` store. |
| I5 | Trust the CA: browsers with their own store | partial | todo | todo | Firefox (NSS) on every OS, Chromium's NSS database on Linux. Not checked on macOS either. |
| I6 | Ports 80 and 443 without root | done | todo | todo | macOS allows it on a wide bind. Linux: `setcap cap_net_bind_service` on the binary (lost on every upgrade) or `net.ipv4.ip_unprivileged_port_start`. Windows: allowed, but IIS or an `http.sys` reservation may hold them. |
| I7 | Administrator steps, each explained | done | todo | todo | `Runner.Sudo` runs `/usr/bin/sudo`. Linux: sudo, or pkexec without a terminal. Windows: UAC elevation of a second process. |
| I8 | Conflict scan (who holds the ports) | done | todo | todo | Runs `/usr/sbin/lsof`, `/bin/launchctl` and `/bin/ps`: macOS paths and tools. Linux: `ss -lntup` or `/proc/net`, systemd units, and systemd-resolved's stub on `127.0.0.53:53`. Windows: `Get-NetTCPConnection`, `netsh http show urlacl`, Docker Desktop, IIS, Internet Connection Sharing on port 53. |
| I9 | `doctor` and `doctor --fix` | done | todo | todo | The file-permission check reads `syscall.Stat_t`; on Windows that means ACLs. |
| I10 | `install --refresh` and upgrade on the next command | done | todo | todo | Depends on I2. |
| I11 | `uninstall [--purge]` leaves nothing | done | todo | todo | Reverses I2 to I6. |
| I12 | `migrate` from the Bash prototype | done | — | — | The prototype was macOS-only. |

### Testing on CI

| ID | Item | macOS | Linux | Windows | Notes |
| --- | --- | --- | --- | --- | --- |
| T1 | Installer tests with fakes | done | todo | todo | `install/darwin_test.go` on `macos-15-intel`. |
| T2 | End to end: install, a site over HTTPS through the system's DNS and trust, upgrade, uninstall | done | todo | todo | `scripts/integration.sh` on `macos-15-intel`. Linux: `ubuntu-latest` has systemd-resolved and passwordless sudo. Windows: `windows-latest` has admin rights. |
| T3 | Distributions | — | todo | — | Ubuntu, Fedora and Arch, per the roadmap. Containers lack a full systemd; VMs or self-hosted runners for Fedora and Arch. |
| T4 | npm packages end to end | done | todo | todo | `scripts/npm-e2e.sh`. |

### WSL2

| ID | Item | Status | Notes |
| --- | --- | --- | --- |
| W1 | Dev server in WSL, browser on Windows | todo | WSL forwards `localhost` to Windows, so Dotlane on Windows can proxy to a port in WSL. Document it. |
| W2 | Dotlane inside WSL, browser in WSL | todo | The Linux build; depends on the Linux installer. |
| W3 | Dotlane inside WSL, browser on Windows | todo | Windows' DNS and trust store would need to point into WSL. Probably documented as unsupported. |

## Open decisions for cross-platform

These change what the rows above mean; the roadmap's own open decisions are in
[`roadmap.md`](roadmap.md#open-decisions).

- **Order:** Linux first (closest to macOS: sudo, Unix sockets, a resolver that takes a port), or
  a Windows prototype first, since it is the riskiest part (roadmap Risks)?
- **Windows port 53:** require it, or fall back to the hosts file (no wildcard, one entry per
  site) on machines where it is taken or blocked? Already open in the roadmap.
- **Windows control channel:** AF_UNIX socket with an ACL, or a named pipe? The desktop app and
  the documented control API depend on it.
- **Linux state directory:** move to `$XDG_STATE_HOME`, keeping macOS where it is?
- **Linux without systemd-resolved:** support NetworkManager-dnsmasq and plain `resolv.conf`, or
  only report it in `doctor`?
- **Linux privileged ports:** `setcap` on the binary (must be reapplied after every upgrade, and
  npm or Homebrew upgrades cannot), or the sysctl (changes the whole machine)?
