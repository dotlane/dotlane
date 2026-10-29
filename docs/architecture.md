# Architecture

## Goal

Give any local dev server a stable, trusted HTTPS hostname on a private domain ending, without
changing how the dev server is started, without root-owned long-running processes, and with
near-zero cost per new hostname.

## Components

![Dotlane: tools, libraries and request flow](diagrams/dotlane-components.svg)

Colours: teal is Dotlane's own (the `dotlane` CLI and its site files), violet the open-source
dependencies (Caddy, dnsmasq, Homebrew), blue macOS built-ins (resolver, Keychain, launchd), grey
the user's apps and tools. Solid numbered arrows are a request; dashed arrows are setup and
management.

| Component | Role | Runs as | Listens on |
| --- | --- | --- | --- |
| macOS resolver (`/etc/resolver/traix`) | Routes only `*.traix` lookups to our DNS server | system | — |
| dnsmasq 2.93 | Answers every `*.traix` name with `127.0.0.1` / `::1` | user (LaunchAgent) | `127.0.0.1:10053` UDP+TCP |
| Caddy 2.11.7 | TLS termination with its own local CA; reverse proxy per site | user (Homebrew service) | `:80`, `:443`, admin API `localhost:2019` |
| `dotlane` | Writes/removes one site file per domain, reloads Caddy | user, on demand | — |
| Dev server (e.g. Vite) | The app, untouched | user | e.g. `localhost:5173` |

## Request flow

1. A browser or tool asks for `myapp.traix`.
2. macOS sees `/etc/resolver/traix` (`nameserver 127.0.0.1`, `port 10053`) and sends **only** this
   lookup to dnsmasq. All other DNS is unaffected.
3. dnsmasq answers from one wildcard rule, `address=/traix/127.0.0.1` (and `::1`). There is no
   per-site DNS: any `<anything>.traix` resolves the moment it is asked for.
4. The client connects to `127.0.0.1:443` (or `[::1]:443`). Caddy picks the site by SNI/Host.
5. Caddy presents a certificate for `myapp.traix`, issued on demand by its local CA, which the
   system trusts (`caddy trust`).
6. Caddy reverse-proxies to `localhost:5173`. WebSockets (Vite's live reload) pass through without
   configuration. `http://` requests get a 308 redirect to `https://`.

## Design decisions

**Resolver file + wildcard DNS, not `/etc/hosts`.** `/etc/hosts` needs root for every new name and
has no wildcards. A per-domain-ending resolver file is written once (one `sudo`), after which new
names cost nothing. It also works for every tool on the machine — `curl`, Node, Python — unlike
`*.localhost`, which browsers resolve themselves but many CLI tools do not.

**dnsmasq on an unprivileged port as a user LaunchAgent.** macOS resolver files accept a `port`,
so dnsmasq does not need port 53 and therefore does not need root. Homebrew's own dnsmasq service
declares `require_root true`, so we ship our own LaunchAgent (`dev.traix.dnsmasq`) pointing at a
dedicated config. The config has `no-resolv` and `no-hosts`: it answers `.traix` and nothing else,
and cannot leak or alter other DNS.

Port **10053**, not the common 1053: on the reference machine 1053 was already taken by another
dev tool (Yerd). A product must detect this (see `troubleshooting.md`).

**Caddy as the proxy and CA.** Caddy issues certificates from its own CA automatically
(`local_certs`), renews them (site certificates live 12 hours, the intermediate 7 days, the root
10 years), proxies WebSockets and HTTP/2 by default, and reloads config through its admin API
without dropping connections. On macOS since 10.14, an unprivileged process may bind ports 80/443
on all interfaces, so Caddy runs as the user.

**One file per site, imported by glob.** The main Caddyfile is static:

```caddy
{
	local_certs
}

import /Users/<you>/.config/caddy/sites/*.caddy
```

Each domain is a separate `<name>.caddy` file. Adding or removing a domain is a file write/delete
plus `caddy reload`, so there is no shared file to parse and edit, and sites can be listed by
reading the directory.

**The dev server is never wrapped.** Unlike tools that run the dev command themselves to inject a
port, this only needs to know the port the app already uses. Any language, framework or process
manager works, including servers started by an IDE or a container.

## Files and state

| What | Where | Owner |
| --- | --- | --- |
| Resolver | `/etc/resolver/traix` | root (written once) |
| dnsmasq config | `~/.config/dnsmasq/traix.conf` | user |
| dnsmasq LaunchAgent | `~/Library/LaunchAgents/dev.traix.dnsmasq.plist` | user |
| dnsmasq log | `~/Library/Logs/dnsmasq-traix.log` | user |
| Caddy main config | `/opt/homebrew/etc/Caddyfile` | user |
| Caddy site files | `~/.config/caddy/sites/*.caddy` | user |
| Caddy LaunchAgent | `~/Library/LaunchAgents/sh.brew.caddy.plist` (Homebrew) | user |
| Caddy CA + certificates | `/opt/homebrew/var/lib/caddy/pki/`, `.../certificates/` | user, keys mode 600 |
| Caddy log | `/opt/homebrew/var/log/caddy.log` | user |
| Trusted root | System keychain, "Caddy Local Authority - 2026 ECC Root" | root (via `caddy trust`) |

## Scope limits of the prototype

- macOS only (resolver files, LaunchAgents, Homebrew paths).
- One domain ending (`.traix`) hard-coded in three places: dnsmasq config, resolver file name,
  `dotlane`.
- This Mac only: phones and other machines cannot resolve `.traix` (no LAN DNS or mDNS).
- Targets are always `localhost:<port>`; no remote upstreams, path routing or per-site headers.
