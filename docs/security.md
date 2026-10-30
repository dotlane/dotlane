# Security model

Written for the Bash prototype. The Go version keeps the same CA model with its keys in
`~/Library/Application Support/dotlane/caddy/` (mode 0600); its daemon has no admin API (a Unix
socket, mode 0600, instead) and drops requests from other machines on 80/443. See
[`how-it-works.md`](how-it-works.md#listening-on-443-and-80).

## The certificate authority is the sensitive part

`caddy trust` adds Caddy's root certificate to the **System keychain**. From then on, this Mac's
browsers and tools accept any certificate signed by that CA, for **any** hostname — not only
`.traix`. Whoever holds the CA's private key could impersonate any website to this machine.

| Item | Location | Protection |
| --- | --- | --- |
| Root key / certificate | `/opt/homebrew/var/lib/caddy/pki/authorities/local/root.{key,crt}` | user-owned, mode 600 |
| Intermediate key / certificate | `.../intermediate.{key,crt}` | user-owned, mode 600 |
| Site keys / certificates | `/opt/homebrew/var/lib/caddy/certificates/local/` | user-owned |

Lifetimes: root 10 years, intermediate 7 days, site certificates 12 hours — all generated and
renewed by Caddy, so a leaked site key is short-lived. The root is the long-lived secret.

Consequences:

- Any process running as this user can read the root key. That is the same trust level as the
  user's browser profile and SSH keys, but it should be stated plainly to users.
- Never copy the CA to another machine or share it with a team; each machine gets its own.
- `caddy untrust` removes the root from the keychain; deleting `pki/` makes Caddy create a new CA.
- A product should consider **name constraints** on its root (limiting it to the chosen domain
  ending), which would cap the damage of a leaked key. The root Caddy generated here carries no
  name constraints (check with `openssl x509 -in root.crt -noout -text`).
  (mkcert has the same property and documents it as a warning.)

## Privileges

| Step | Needs root | Why |
| --- | --- | --- |
| `/etc/resolver/traix` | once | system directory |
| `caddy trust` / `untrust` | once | System keychain |
| dnsmasq | never | runs as a user LaunchAgent on port 10053 |
| Caddy, ports 80/443 | never | macOS allows unprivileged binding of low ports on all interfaces |
| `dotlane add/rm` | never | user files + Caddy admin API |

No long-running process runs as root, and nothing is installed into `/etc/sudoers.d`.

## Network exposure

- dnsmasq listens on `127.0.0.1` only (`listen-address`, `bind-interfaces`), answers only `.traix`
  (`no-resolv`, `no-hosts`, single `address=` rule), and forwards nothing.
- Caddy listens on all interfaces for 80/443 (its default). Another device on the LAN that sends
  `Host: myapp.traix` to this Mac's IP would reach the dev server — the same exposure as a dev
  server bound to `0.0.0.0`. A product should bind Caddy to loopback unless LAN access is wanted
  (`default_bind 127.0.0.1 ::1`).
- Caddy's admin API listens on `localhost:2019` without authentication (Caddy's default). Any local
  process can rewrite the proxy config. Acceptable for a single-user dev machine; a product may
  prefer a Unix socket with file permissions.

## Domain ending choice

`.traix` is not a public top-level domain today (checked against IANA's list, 2026-10-29). If one
with that name were ever delegated, this machine would shadow it. `.test` is reserved for this use
by RFC 2606 and cannot collide; a branded ending trades that guarantee for identity.
