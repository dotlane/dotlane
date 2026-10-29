# `dotlane` reference

A Bash script (`files/bin/dotlane`, installed at `~/.local/bin/dotlane`). It manages files in
`~/.config/caddy/sites/` and reloads Caddy through its admin API.

| Command | Effect |
| --- | --- |
| `dotlane add <name> <port>` | Writes `<name>.caddy` (`https://<name>.<tld>` → `localhost:<port>`), reloads Caddy, waits until the site's certificate is served (typically 0.15–0.5 s, at most ~5 s), prints the URL. Re-running with a new port updates it. |
| `dotlane rm <name>` | Deletes the site file, reloads Caddy. |
| `dotlane ls` | Lists sites as `https://<name>.<tld> -> localhost:<port>`. |
| `dotlane doctor` | Shows the domain ending in use, then checks the resolver file, dnsmasq's answer, Caddy's admin API, and that the CA is in the System keychain. Exits non-zero on any failure, with the fix for each. |

Names: lowercase letters, digits and dashes, not starting or ending with a dash. Ports: 1–65535.

Generated site file:

```caddy
# Managed by dotlane: https://myapp.traix -> localhost:5173
myapp.traix {
	reverse_proxy localhost:5173
}
```

The first line is metadata: `ls` reads it, so hand-edited files keep listing correctly as long
as it stays.

## Configuration

Optional file `~/.config/dotlane/config.toml` (another path with `DOTLANE_CONFIG=<file>`). Without
it, the defaults apply:

```toml
tld = "traix"      # domain ending: sites become https://<name>.<tld>
dns_port = 10053   # where dnsmasq answers; doctor queries it
```

The format is the TOML subset of one `key = value` per line (quotes optional, `#` comments), the
same file the planned Go version reads. It is parsed, never sourced. An ending must be lowercase
letters, digits and dashes, and cannot be `local` (macOS reserves it for Bonjour).

Changing `tld` only changes what the script writes and checks. The ending also needs its own
resolver file and DNS rule (see [`setup.md`](setup.md#using-another-domain-ending)), and sites added
earlier keep their old address until re-added.

## Using it from a project

MyApp wraps it in a mise task so the project owns its name and port:

```toml
[tasks.domain]
description = "Serve the web app at https://myapp.traix"
run = '''
command -v dotlane >/dev/null || { echo "dotlane is not installed" >&2; exit 1; }
dotlane add myapp 5173
grep -q 'https://myapp.traix' backend/.env 2>/dev/null \
  || echo "Add https://myapp.traix to ALLOWED_ORIGINS in backend/.env, or sign-in will be refused."
'''
```

## Exit codes and errors

Any failure prints `dotlane: <reason>` to stderr and exits 1. A rejected `caddy reload`
(Caddy stopped, or an invalid hand-edited site file) leaves the new file in place: fix it or
`dotlane rm` it, then reload.
