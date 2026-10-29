# Product notes

Inputs for turning the prototype into **Dotlane**. The plan built from them is in [`roadmap.md`](roadmap.md).

Name chosen 2026-10-29. At that date `dotlane` was free on npm and Homebrew, and `dotlane.dev` had
no DNS records (a quick check, not a trademark search).

## The problem

Local development runs on `localhost:<port>`. That breaks down as soon as a project has several
services or a team:

- Ports are meaningless, collide between projects, and change.
- `http://localhost` differs from production: no `Secure` cookies, no secure-context APIs, different
  CORS and cookie-domain behaviour, OAuth redirect URIs that cannot match.
- Every existing fix asks for something per site: a hosts-file edit with `sudo`, a certificate to
  generate and trust, or wrapping the dev command in another tool.

## What the prototype does differently

1. **A whole private domain ending, answered once.** One resolver file and one wildcard DNS rule;
   afterwards every `*.<tld>` name works immediately, in every tool on the machine (browsers,
   `curl`, Node, Python), not only in browsers.
2. **No root daemon, ever.** Root is needed twice at install (resolver file, trusting the CA) and
   never again. DNS runs as the user on a high port; the proxy runs as the user on 80/443.
3. **Does not wrap the dev server.** It needs a name and a port, nothing else: any language,
   framework, IDE run configuration or container works, unchanged.
4. **Branded domain endings.** `myapp.traix` instead of `myapp.localhost` or `.test`: a
   company's own namespace for its local stack, the same names on every developer's machine.
5. **Built on boring, proven parts:** macOS resolver files, dnsmasq, Caddy (≈78k stars). The
   product is orchestration, not a new proxy or DNS server.

## Landscape (checked 2026-10-29)

| Tool | Approach | Notes |
| --- | --- | --- |
| **portless** (Vercel Labs, ★12.8k, very active) | `*.localhost`, wraps the dev command (`portless run next dev`), assigns ports and injects `--port` flags, own proxy + CA, auto-elevates with sudo for 443 | Closest competitor and well funded. Framework-aware; pre-1.0. `.localhost` is resolved by browsers, not by every CLI tool. Positions for "humans and agents". |
| **localias** (★1.5k, active) | Aliases `https://x.test → localhost:3000`, Caddy inside, edits `/etc/hosts` per alias, mDNS `.local` for phones, shared team config file | Same core idea as ours; per-alias hosts edits instead of wildcard DNS. macOS, Linux, WSL2. |
| **Laravel Valet / Herd** | dnsmasq + nginx for `.test`, PHP-centric, root helper | Herd is a commercial app. Ownership of `.test` and 80/443 conflicts with others. |
| **puma-dev** (★1.8k, quiet since 2024) | `.test` for Rack apps, auto HTTPS | Ruby-specific. |
| **hotel** (★10k, inactive since 2023) | Process manager + local domains | Popular and now dormant: demand exists. |
| **mkcert** (★60k) | Certificates only | Solves the TLS piece; no DNS or proxy. |

Takeaway: demand is proven (hotel, mkcert, portless traction). portless sets the bar for developer
experience; the open space is framework-agnostic, no-wrapping, wildcard-DNS, team-branded domains
with a strong security story.

## Gaps between prototype and product

**Install and lifecycle**
- One-command install and uninstall (Homebrew tap and/or single binary), including the two password
  prompts with clear explanations.
- Conflict detection: resolver files for the same ending, listeners on 80/443 and the DNS port,
  known tools (Herd, Valet, Yerd, Docker on 443), and automatic port choice for DNS.
- Self-healing `doctor --fix`; upgrades that keep the CA and sites.

**Core features**
- Configurable domain ending(s); several at once.
- Project config file in the repo (e.g. a small `domains.toml`) so `cd project && <tool> up`
  registers its names; shared across a team.
- Upstream targets beyond `localhost:<port>`: containers, other hosts, path routing, headers.
- Port discovery or tight integration with process managers (mise, concurrently, Docker Compose).
- Status of each site (upstream up/down) in `ls`.

**Reach**
- Linux (systemd-resolved per-domain routing or NetworkManager dnsmasq) and Windows/WSL (NRPT).
- Phones and other devices on the LAN (mDNS, a LAN DNS answer, or a tunnel) — mobile developers
  hit this immediately.

**Security**
- Name-constrained root CA limited to the chosen endings.
- Bind the proxy to loopback by default; authenticated admin API (Unix socket).
- Clear documentation of what is trusted and how to revoke it.

**Product surface**
- Menu-bar app or TUI showing sites, status and logs.
- Team features: shared domain ending and site list per organisation, onboarding in one step.
- AI-agent friendliness: stable, discoverable URLs and a machine-readable `ls --json`.

## Open questions

- Who is it for first: individual developers, or teams that want a shared branded namespace?
- Wrap the dev command (portless-style convenience) or stay non-wrapping (universality)?
- Open source, open core, or a paid team product?
- Keep Caddy and dnsmasq as dependencies, or embed them (Caddy is a Go library; a Go binary could
  embed both the proxy and a tiny DNS responder)?
