# How Dotlane works

What the Go version of Dotlane does and how it does it, as of 2026-10-30 (Phase 1, M1–M6). The Bash
prototype it replaces is described in [`prototype/architecture.md`](prototype/architecture.md).

## What it does

Dotlane gives every local dev server a real, trusted HTTPS address on a private domain ending:
`https://myapp.traix` instead of `http://localhost:5173`. A site is a name and a port. Dotlane
answers DNS for the name, presents a certificate for it that the Mac trusts, and forwards the
traffic to `localhost:<port>`. The dev server is not wrapped or changed, so any language or
framework works, started however you already start it.

Setup happens once per machine (`dotlane install`, two password prompts). After that, a new site
needs no password and takes about a second:

```bash
dotlane add myapp 5173      # by hand
dotlane up                      # or every site in the project's dotlane.toml
```

## The pieces

One binary, `dotlane`, is both the CLI and the background service (`dotlane serve`). The service
runs as the user, as a launchd login agent, and holds four things:

| Part | What it does | Listens on |
| --- | --- | --- |
| DNS responder (`internal/dns`, miekg/dns) | Answers `A 127.0.0.1` and `AAAA ::1` for every name under the configured endings, with TTL 0. Anything else is refused | `127.0.0.1:10053` UDP and TCP |
| Caddy, embedded (`internal/proxy`) | TLS with a local certificate authority, then one reverse-proxy route per site. HTTP only redirects to HTTPS | `:443` and `:80` (see [Listening](#listening-on-443-and-80)) |
| Control API (`internal/api`) | JSON over HTTP on a Unix socket, mode 0600, so only this user can use it | `<state>/dotlane.sock` |
| Site registry (`internal/sites`) | Each site's name, port and owning project, in `sites.json` | — |

Caddy runs inside the process as a library (`caddy.Load`), with its admin endpoint disabled. The
daemon rebuilds Caddy's JSON config from the registry on each change, so nothing else can
reconfigure it and port 2019 stays free. Only the Caddy modules Dotlane uses are compiled in.

The CLI, `dotlane.toml` and a future menu-bar app are all clients of the control API:

| Call | Used by |
| --- | --- |
| `GET /v1/status` | `doctor`, `install` (is the daemon up, which PID, version) |
| `GET /v1/sites` | `ls`, `up`, `down`, `migrate` |
| `PUT /v1/sites/{name}` `{port, project}` | `add`, `up`, `migrate` |
| `DELETE /v1/sites/{name}` | `rm`, `down`, `up` (sites dropped from the file) |

## A request, end to end

```text
browser / curl / any tool
   │ myapp.traix ?
   ▼
macOS resolver ── /etc/resolver/traix ──▶ Dotlane DNS 127.0.0.1:10053  →  127.0.0.1, ::1
   │
   ▼
https://myapp.traix  ──▶  Dotlane :443  (Caddy: certificate from the local CA, picks the site
   │                                          by name, drops requests from other machines)
   ▼ reverse proxy
localhost:5173  (the dev server, unchanged)
```

1. macOS sends only `*.traix` lookups to Dotlane's DNS, because of `/etc/resolver/traix`
   (`nameserver 127.0.0.1`, `port 10053`). All other DNS is unaffected. There is no per-site DNS:
   any name under the ending resolves to this Mac.
2. The connection reaches Caddy on 443. Caddy presents a certificate for the name, issued by its
   local CA, which macOS trusts.
3. Caddy forwards to `localhost:<port>`. WebSockets (Vite's live reload) and HTTP/2 work without
   configuration. `http://` gets a 308 redirect to the same URL on HTTPS.

Two cases get a page from Dotlane instead of a browser error:

- **A name with no site** (`typo.traix`) gets "No site at typo.traix" (404), with the command to
  add one. Its certificate is issued at the first handshake: an in-process Caddy permission module
  allows on-demand certificates only for single-label names under the configured endings.
- **A site whose dev server is not running** gets a page naming the port (502).

The pages show the TLS server name, which the permission module has checked, never the `Host`
header, so a client cannot inject markup into them.

## Listening on 443 and 80

macOS lets a normal user listen on ports below 1024 only on all interfaces (`*:443`). It refuses
the same port on `127.0.0.1` or `::1` alone (`EACCES`), which was found when the first real
`dotlane migrate` was refused. So, without a root helper:

- Ports below 1024 (80 and 443 by default) are bound on all interfaces. Every server's first
  route drops any request that does not come from this Mac (`127.0.0.0/8`, `::1`), with no answer.
- Ports from 1024 up (the dev instance's 9443 and 9480, and the DNS port) stay on loopback.

Another device on the network can still open a connection to 443 and, with the right name,
complete the TLS handshake (it sees the certificate). It never reaches a site. Verified
2026-10-30: requests to this Mac's LAN address got no response, while loopback worked. The
alternatives were a root LaunchDaemon handing over loopback sockets, or a pf redirect. Both need a
password at install and more moving parts, so they were left for later. A `bind = "lan"` setting
to let phones in on purpose is on the [roadmap](roadmap.md).

## Certificates

Caddy's local CA has a root (10 years), an intermediate (7 days) and short-lived site certificates
(12 hours), all renewed automatically. A site's certificate is issued when the site is added; `add`
and `up` wait (up to ~5 s) until it is served, so the printed URL works on the first click.

`dotlane install` trusts the root in the System keychain (one password). Caddy never touches the
keychain itself (`install_trust: false`). The root key is the sensitive part: a process holding it
could impersonate any site to this Mac. It lives in the state directory with mode 0600; see
[`security.md`](security.md).

`dotlane migrate` reuses the prototype's root instead of creating one: it copies the root
certificate and key from Homebrew Caddy's storage before the daemon first starts. Caddy then
issues its own intermediate under that root, which macOS already trusts.

## Installing

`dotlane install` sets up three things, each checked first, so re-running only does what is
missing:

| Step | What it writes | Password |
| --- | --- | --- |
| Login service | Copies the binary to `~/Library/Application Support/dotlane/bin/dev.dotlane`, and writes `~/Library/LaunchAgents/dev.dotlane.plist` to run that copy's `serve` at login and restart it if it exits | no |
| Certificate authority | Trusts `<state>/caddy/pki/authorities/local/root.crt` in the System keychain | yes, once |
| DNS routing | `/etc/resolver/<tld>` for each ending | yes, once per ending |

Before changing anything, it runs the same checks as `doctor`. If another tool holds 80, 443 or
the DNS port, it names the tool (the prototype's Caddy or dnsmasq, Yerd, Herd, Valet, Docker, or
the process and PID) with its fix, and stops. The one exception is a taken default DNS port: install
picks the next free one and saves it to `config.toml`. Each password step is explained and its
exact `sudo` command shown before it runs. A loaded but unresponsive service is restarted.

Re-running install also restarts the service when it is stuck, or when the binary or
`config.toml` changed after the daemon started (the daemon reports its start time), and tightens
the private files' modes if they drifted.

`dotlane uninstall` reverses it: it stops and removes the service, removes the CA from the
keychain, and removes resolver files whose content is exactly Dotlane's (resolver files cannot
carry a marker). Sites and the CA's keys are kept unless `--purge` is given.

## Sites and projects

- `dotlane add <name> <port>`, `rm <name>` and `ls` manage sites by hand.
- A project lists its sites in a committed `dotlane.toml`:

  ```toml
  [sites.myapp]
  port = 5173
  ```

  `dotlane up`, run anywhere in the project, serves them and removes the project's sites the file
  no longer lists; `dotlane down` removes them all.
- Each site records the project directory that registered it (symlinks resolved), shown by `ls`.
  A name another project already uses is refused, and nothing changes; `dotlane add` cannot take a
  project's name either (HTTP 409 from the daemon). A site added by hand is taken over by the
  first project that lists it.

## Checking: `dotlane doctor`

`doctor` checks the config, the login service, the daemon (and whether it runs an older binary or
config), the three ports, the private files (the state directory 0700, the socket and the CA's
root key 0600, all owned by this user), the CA's trust, each resolver file and a real lookup
through the macOS resolver. For each problem it gives the fix, and
it names whatever is in the way. It reports every process on a port, not just the first: Caddy uses
`SO_REUSEPORT`, so two servers can share 443 and split its traffic without either failing.
`lsof` only sees this user's processes, so a bind test catches ports held by other users.
`doctor --fix` runs `install` for what it can repair; other tools are left alone. `--json` is for
scripts.

## Moving from the prototype: `dotlane migrate`

1. Reads the prototype without changing anything: the site files written by its `dotlane add`
   (others are named and left out), Homebrew Caddy's CA, and whether Caddy and the dnsmasq agent
   are loaded. A site on an ending Dotlane is not set up for stops it here.
2. Copies the prototype's trusted root CA into Dotlane's state, and imports the sites.
3. Stops Homebrew's Caddy (`brew services stop caddy`) and boots out and disables the dnsmasq
   agent, so it does not return at login.
4. Runs `install`. With the CA already trusted and the resolver file already pointing at port
   10053, no step needs a password.
5. If the install fails, it removes Dotlane's service, starts the prototype again and leaves the
   CA's trust alone. On success, it checks each site with a TLS handshake against the system's
   trusted roots, points `~/.local/bin/dotlane` (the prototype script) at the binary, warns if that
   folder is not on `PATH`, and lists the prototype's files for you to delete. It deletes nothing.

## Configuration

`~/.config/dotlane/config.toml` (or `--config`, or `$DOTLANE_CONFIG`) is optional. Unknown keys
are rejected, so typos surface:

```toml
tld = "traix"            # or several: tlds = ["traix", "test"]; "local" is refused
dns_port = 10053
http_port = 80
https_port = 443
state_dir = "…"          # default ~/Library/Application Support/dotlane
service_name = "dev.dotlane"
```

The dev instance in this repo (`dev/config.toml`) uses `.dldev`, ports 10054, 9480 and 9443, and
`dev/state`, so it runs next to a real install.

## Files and state

| What | Where |
| --- | --- |
| Binary | wherever it was built; the login service runs a copy in `~/Library/Application Support/dotlane/bin/`, since macOS holds back background jobs started from Documents, Desktop or Downloads |
| Config | `~/.config/dotlane/config.toml` (optional) |
| Sites | `~/Library/Application Support/dotlane/sites.json` |
| CA, certificates | `~/Library/Application Support/dotlane/caddy/` (keys mode 0600) |
| Control socket | `~/Library/Application Support/dotlane/dotlane.sock` (mode 0600) |
| Login service | `~/Library/LaunchAgents/dev.dotlane.plist` |
| Log | `~/Library/Logs/dotlane/dev.dotlane.log` |
| DNS routing | `/etc/resolver/<tld>` (root-owned) |
| Trusted root | System keychain |

## Commands

| Command | What it does |
| --- | --- |
| `install` | Login service, CA trust, DNS routing; checks for conflicts first |
| `uninstall [--purge]` | Reverses install; `--purge` also deletes sites, CA keys and the log |
| `doctor [--fix] [--json]` | Checks everything, names what is in the way, repairs what install can |
| `migrate` | From the Bash prototype to Dotlane, keeping sites and the trusted CA |
| `add <name> <port>` | Serve `https://<name>.<tld>` from `localhost:<port>` |
| `rm <name>` | Remove a site |
| `ls [--json]` | List sites, with the project that owns each |
| `up` / `down` | Serve / remove the sites in the project's `dotlane.toml` |
| `serve` | Run the service in the foreground (what the login service runs) |
| `version` | Print the version |

Output is coloured on a terminal only. `NO_COLOR=1` turns colour off and always wins;
`CLICOLOR_FORCE=1` forces it.

## Limits today

- macOS only. Linux and Windows are on the [roadmap](roadmap.md#later).
- This Mac only: phones and other machines cannot resolve the ending, and are dropped if they
  connect.
- No PHP without a server on a port (planned: [roadmap](roadmap.md#next)).
