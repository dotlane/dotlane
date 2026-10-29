# Troubleshooting and lessons learned

Each of these happened while building the prototype. Several are things a product must detect
and handle automatically.

## Start with

```bash
dotlane doctor
```

## Name does not resolve

- `dig +short x.traix @127.0.0.1 -p 10053` empty → dnsmasq is down:
  `launchctl kickstart -k gui/$(id -u)/dev.traix.dnsmasq`; log in `~/Library/Logs/dnsmasq-traix.log`.
- dnsmasq answers but `dscacheutil -q host -a name x.traix` does not → `/etc/resolver/traix` is
  missing or wrong (`scutil --dns` should list `domain : traix`, `port : 10053`).
- **Port already in use.** On the reference machine UDP 1053 was held by Yerd (`yerdd`, a
  Herd-like dev tool). dnsmasq moved to 10053. *Product: probe for a free port and write the same
  port into both the dnsmasq config and the resolver file.*

## Conflicts with other local-domain tools

- **Laravel Herd / Valet** install a resolver for `.test`, a root helper, and nginx on 80/443.
  Herd's uninstaller only removes `/etc/resolver/test` when its content is exactly
  `nameserver 127.0.0.1`; Herd itself had written a `port` line, so the file survived and pointed
  at a DNS server that no longer existed. The uninstaller also leaves a `PHP_INI_SCAN_DIR` export
  in `~/.zshrc`.
- Anything on 80/443 (nginx, Apache, another Caddy, Docker publishing 443) stops Caddy from
  serving. `lsof` without `sudo` does not show root-owned listeners; test with
  `nc -z 127.0.0.1 443` instead.
- *Product: an install-time conflict scan for resolver files, listeners on 80/443/the DNS port, and
  known tools, with a clear report.*

## Certificate errors

- **TLS handshake fails right after adding a domain** (`tlsv1 alert internal error`). Caddy issues
  a new site's certificate in the background after a reload; requests in the first ~0.5 s failed.
  `dotlane add` now waits until the site completes a TLS handshake before printing the URL.
- `dotlane doctor` says the CA is not trusted → run `caddy trust`.
- Firefox can use its own certificate store; if it rejects the certificate, set
  `security.enterprise_roots.enabled` to `true` in `about:config`. (Only Chrome and `curl` were
  verified on the reference machine.) Node.js ignores the keychain: use
  `NODE_EXTRA_CA_CERTS=/opt/homebrew/var/lib/caddy/pki/authorities/local/root.crt` for
  server-side calls to `.traix` URLs.

## The app answers, but…

- **"Blocked request. This host is not allowed."** — Vite's `server.allowedHosts` (see
  `app-integration.md`).
- **403 / CSRF / "Cross-origin request rejected"** — the backend's origin allowlist lacks
  `https://<name>.traix`, or it was added but the backend was not restarted.
- **Chrome searches instead of opening the site** — type the full `https://myapp.traix`
  once; afterwards it autocompletes.

## Homebrew details

- Homebrew's dnsmasq service is `require_root true`, hence the custom LaunchAgent.
- Running `brew formula <name>` silently switches Homebrew into developer mode; switch it back
  with `brew developer off`.
- Caddy's Homebrew service runs `caddy run --config /opt/homebrew/etc/Caddyfile`; with no file
  there it sits in an `error` state in `brew services list`.
