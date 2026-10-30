# `dotlane` reference (Bash prototype)

Kept for reference. The Go version's commands are in [`../cli.md`](../cli.md).

A Bash script (`files/bin/dotlane`, installed at `~/.local/bin/dotlane`). It manages files in
`~/.config/caddy/sites/` and reloads Caddy through its admin API.

| Command | Effect |
| --- | --- |
| `dotlane add <name> <port>` | Writes `<name>.caddy` (`https://<name>.<tld>` → `localhost:<port>`), reloads Caddy, waits until the site's certificate is served (typically 0.15–0.5 s, at most ~5 s), prints the URL. Re-running with a new port updates it. |
| `dotlane rm <name>` | Deletes the site file, reloads Caddy. |
| `dotlane ls` | Lists sites as `https://<name>.<tld> -> localhost:<port>`. |
| `dotlane up` | Runs `add` for each site in the nearest `dotlane.toml` (current directory or a parent). |
| `dotlane down` | Runs `rm` for each of them that exists. |
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

The format is TOML: one `key = value` per line, `#` comments. The Go version reads the same file
and needs quotes around text values (`tld = "test"`); the script tolerates them missing. It is
parsed, never sourced. An ending must be lowercase
letters, digits and dashes, and cannot be `local` (macOS reserves it for Bonjour).

Changing `tld` only changes what the script writes and checks. The ending also needs its own
resolver file and DNS rule (see [`setup.md`](setup.md#using-another-domain-ending)), and sites added
earlier keep their old address until re-added.

## Using it from a project

A project lists its sites in a `dotlane.toml` at its root, committed with the code:

```toml
# https://myapp.traix → the Vite dev server
[sites.myapp]
port = 5173

[sites.myapp-api]
port = 8000
```

`dotlane up`, run anywhere inside the project, adds them; `dotlane down` removes them. The script
reads only `[sites.<name>]` headers and `port = <n>` lines. The Go version reads the file as TOML
and rejects unknown keys, records which project registered each site, refuses a name another
project already uses (instead of silently pointing it elsewhere), and on `up` also removes the
project's sites the file no longer lists. A site added by hand with `dotlane add` is taken over
by the first project that lists it.

## Exit codes and errors

Any failure prints `dotlane: <reason>` to stderr and exits 1. A rejected `caddy reload`
(Caddy stopped, or an invalid hand-edited site file) leaves the new file in place: fix it or
`dotlane rm` it, then reload.
