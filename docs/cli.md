# `dotlane` reference

Every command also has `dotlane <command> --help`. How the pieces work is in
[`how-it-works.md`](how-it-works.md); the Bash prototype's reference is in
[`prototype/cli.md`](prototype/cli.md).

## Sites

| Command | What it does |
| --- | --- |
| `dotlane add <name> <port>` | Serves `https://<name>.<tld>` from `localhost:<port>` and waits until its certificate is served (at most ~5 s), so the printed URL works on the first click. Running it again with a new port updates the site. A name a project owns is refused. |
| `dotlane rm <name>` | Removes a site, whoever registered it. |
| `dotlane ls [--json]` | Lists sites: name, URL per ending, the port it forwards to, and the project that owns it. `--json` prints the daemon's data for scripts. |
| `dotlane up` | Serves the sites in the nearest `dotlane.toml` (this directory or a parent) and removes the project's sites the file no longer lists. |
| `dotlane down` | Removes every site this project registered. |

Names: lowercase letters, digits and dashes, not starting or ending with a dash, up to 63
characters. Ports: 1–65535.

### `dotlane.toml`

Committed in the project's root. Unknown keys are rejected.

```toml
# https://myapp.traix → the Vite dev server
[sites.myapp]
port = 5173

[sites.myapp-api]
port = 8000
```

Each site records the project directory that registered it. `up` refuses a name another project
already uses and changes nothing; it takes over a site added by hand with `dotlane add`.

## Setup and checks

| Command | What it does |
| --- | --- |
| `dotlane install` | Login service, CA trust (password), resolver file per ending (password). Checks for conflicts first and changes nothing if another tool is in the way. Restarts the service when it is stuck, or the binary or config changed after it started. Safe to re-run. |
| `dotlane install --refresh` | Only updates an installed service to this binary (its copy, the plist) and restarts it if it runs an older build or config. Never asks for a password; when Dotlane is not installed it says so and exits 0. What package managers run after an upgrade. |
| `dotlane uninstall [--purge]` | Reverses install. `--purge` also deletes the sites, the CA's keys and the log. |
| `dotlane doctor [--fix \| --json]` | Checks the config, the service, the daemon, ports 80/443 and the DNS port, private files, the CA's trust, each resolver file and a real lookup. Names other tools in the way, with their fix. `--fix` runs install for what it repairs; `--json` prints the checks. |
| `dotlane migrate` | Moves a Bash prototype install to Dotlane, keeping its sites and trusted CA (see [`setup.md`](setup.md#moving-from-the-prototype)). |
| `dotlane serve` | Runs the service in the foreground; the login service runs this. |
| `dotlane version` | Prints the version (also `--version`). |

## Configuration

`~/.config/dotlane/config.toml` is optional; every setting has a default. Another file can be given
with `--config <file>` or `$DOTLANE_CONFIG`, and must then exist. Unknown keys are rejected.

```toml
tld = "traix"            # or several: tlds = ["traix", "test"]; "local" is refused
dns_port = 10053         # install picks a free one if the default is taken, and saves it here
http_port = 80
https_port = 443
state_dir = "…"          # default ~/Library/Application Support/dotlane
service_name = "dev.dotlane"
```

A relative `state_dir` is relative to the config file. Ports below 1024 are bound on all
interfaces (macOS allows a normal user nothing else) and drop requests from other machines; other
ports stay on loopback.

## Pages from Dotlane

- A name under the ending with no site gets a "No site at …" page (404) with the command to add one.
- A site whose dev server is not answering gets a page naming the port (502).
- `http://` redirects (308) to the same URL on HTTPS.

## Output and exit codes

Output is coloured on a terminal only. `NO_COLOR=1` turns colour off and always wins;
`CLICOLOR_FORCE=1` forces it (for `less -R`).

A failure prints an `ERROR` block on stderr, with what to do next, and exits 1. That includes
`doctor` finding a problem, so scripts can rely on its exit code. A command line that is wrong
(missing argument, unknown flag) adds `try --help for usage`.
