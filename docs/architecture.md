# Architecture

The design decisions every Dotlane product builds on. How the core works today, in detail, is in
[`how-it-works.md`](how-it-works.md); how a release reaches users, in
[`distribution.md`](distribution.md). The Bash prototype that came first is described in
[`prototype/architecture.md`](prototype/architecture.md).

## One Go binary

Dotlane is **one Go program** that embeds its parts instead of installing and orchestrating
them. Decided 2026-10-29, when it replaced a Bash script driving Homebrew's Caddy and dnsmasq:

| The prototype | In the binary | Why it works |
| --- | --- | --- |
| Caddy (Homebrew service) | Caddy as a Go library, configured through its JSON API | Caddy is built to be embedded and extended (Apache-2.0) |
| dnsmasq (LaunchAgent) | A small DNS responder on `miekg/dns` | The job is one wildcard rule; `miekg/dns` is the standard Go DNS library |
| `caddy trust` | The trust-store code Caddy uses (`smallstep/truststore`) | Handles the macOS Keychain, the Windows store, Linux stores and NSS (Firefox) |
| The Bash `dotlane` | A Go CLI talking to the daemon over a local socket | One code path for the CLI, the desktop app and editor integrations |

Results: one file to install per OS, no Homebrew dependency, no dnsmasq, cross-compilation to
macOS, Linux and Windows on amd64 and arm64, and a local API the desktop app can use.

**Alternative considered:** Rust (smaller binaries, pairs with Tauri). Rejected for the core
because Caddy (TLS automation, the local CA, HTTP/3, WebSockets, FastCGI for PHP) would have to
be rebuilt or run as a separate process. Go keeps Caddy in the same process.

## The service is the product

The daemon is the product; the CLI, `dotlane.toml` and the desktop app are equal clients of it,
through its control API. A client adds no capability of its own, so anything the desktop app can
do, the CLI can do too.

```text
  desktop app ──┐
  editors     ──┼──►  dotlane daemon  ──►  DNS · HTTPS · certificate authority
  dotlane CLI ──┘
```

Every distribution channel (GitHub Releases, npm, Homebrew, later Linux and Windows packages)
ships the same binary, built once per OS and CPU from one tag.

## Configuration

The same two files on every OS:

```toml
# ~/.config/dotlane/config.toml  — the machine (Windows: %APPDATA%\dotlane\config.toml)
tld = "traix"                # or several: tlds = ["traix", "test"]

# <project>/dotlane.toml       — committed, shared by the team
[sites.myapp]
port = 5173

[sites.api]
port = 8000
```

`dotlane up` in a project registers its sites; `dotlane down` removes them. The CLI and the
desktop app change both files through the daemon. The settings that exist today are in
[`cli.md`](cli.md#configuration); planned ones (`php`, `root`, `command`, `bind = "lan"`) are in
the [core roadmap](roadmap.md).

## The dev server is never wrapped

Dotlane only needs the port an app already listens on. Any language, framework or process
manager works, including servers started by an IDE or a container. Running the dev command
(a `command` per site) is planned as an option, never the default.

## What differs per OS

Only macOS is built today; the rest is researched (2026-10-29) and planned in the
[core roadmap](roadmap.md). The code keeps OS-specific parts in their own files, so a new OS
adds files instead of rewriting them.

| Concern | macOS | Linux | Windows |
| --- | --- | --- | --- |
| Route `*.tld` to Dotlane's DNS | `/etc/resolver/<tld>` with a port (done) | systemd-resolved drop-in: `DNS=127.0.0.1:10053`, `Domains=~<tld>` (ports supported); NetworkManager/dnsmasq fallback | NRPT rule (`Add-DnsClientNrptRule -Namespace .<tld>`); takes addresses, not ports, so **the DNS responder must listen on 127.0.0.1:53** |
| Trust the local CA | System keychain | System store, plus NSS for Firefox and Chromium | Windows certificate store |
| Ports 80/443 as a user | Allowed | Needs `CAP_NET_BIND_SERVICE` on the binary, or a lower `ip_unprivileged_port_start` | Generally allowed; may clash with IIS or `http.sys` reservations |
| Run at login | launchd user agent | systemd user unit | Windows service or scheduled task |
| One-time admin steps | Resolver file, CA trust | resolved drop-in, capability, CA trust | NRPT rule, CA trust, service install |
| Special cases | — | Distributions without systemd-resolved | WSL2: the Linux side resolves through Windows; browser on Windows, server in WSL |
