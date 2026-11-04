# `dotlane` reference

Every command also has `dotlane <command> --help`. How the pieces work is in
[`how-it-works.md`](how-it-works.md); the Bash prototype's reference is in
[`prototype/cli.md`](prototype/cli.md).

## Sites

| Command | What it does |
| --- | --- |
| `dotlane add <name> <port>` | Serves `https://<name>.<tld>` from `localhost:<port>` and waits until its certificate is served (at most ~5 s), so the printed URL works on the first click. Running it again with a new port updates the site. A name a project owns is refused. |
| `dotlane add <name> --php <folder>` | Serves a folder of PHP files (see [PHP sites](#php-sites)). `--php-version 8.4` picks the PHP version (default `latest`, the newest installed), `--frankenphp` runs it with FrankenPHP instead of PHP-FPM, and `--worker <script>` keeps a script running in FrankenPHP's worker mode. |
| `dotlane rm <name>` | Removes a site, whoever registered it. |
| `dotlane ls [--json]` | Lists sites: name, URL per ending, the port it forwards to (or a PHP site's folder and PHP), and the project that owns it. `--json` prints the daemon's data for scripts. |
| `dotlane up` | Serves the sites in the nearest `dotlane.toml` (this directory or a parent) and removes the project's sites the file no longer lists. |
| `dotlane down` | Removes every site this project registered. |

Names: lowercase letters, digits and dashes, not starting or ending with a dash, up to 63
characters. Ports: 1–65535.

### `dotlane.toml`

Committed in the project's root. Unknown keys are rejected.

```toml
# https://myapp.dotlane → the Vite dev server
[sites.myapp]
port = 5173

[sites.myapp-api]
port = 8000
```

A PHP site has `root` and `php` instead of a port:

```toml
# https://shop.dotlane → the PHP files in public/, with PHP 8.4 through PHP-FPM
[sites.shop]
root = "public"        # relative to the project; the project's own folder when left out
php = "8.4"            # major.minor, or "latest" for the newest installed

# https://shop-octane.dotlane → FrankenPHP, keeping Laravel Octane's worker running
[sites.shop-octane]
root = "public"
php = "latest"
runtime = "frankenphp" # "fpm" is the default, except on Windows
worker = "frankenphp-worker.php"   # relative to root; FrankenPHP only
```

Each site records the project directory that registered it. `up` refuses a name another project
already uses and changes nothing; it takes over a site added by hand with `dotlane add`.

## PHP sites

Most PHP apps are a folder of files, so Dotlane runs PHP for them, with one of two runtimes:

- **PHP-FPM** (the default) uses the PHP versions already installed, with their own `php.ini`
  and extensions: Homebrew (`brew install php@8.4`), mise or asdf, and the system's packages
  (`php8.4-fpm` on Debian and Ubuntu, `php-fpm` on Fedora and Arch). Each PHP version gets one
  pool, shared by its sites, so sites can use different versions side by side. A site asking
  for `8.4` gets the newest 8.4.x found.
- **FrankenPHP** (`runtime = "frankenphp"`, or `dotlane add --frankenphp`) runs each site in
  its own FrankenPHP process, with worker mode as an option (`worker`). Each FrankenPHP build
  brings one PHP version and a fixed set of extensions, so `php` must match it or be
  `"latest"`. It is the only runtime on Windows, which has no PHP-FPM.

| Command | What it does |
| --- | --- |
| `dotlane frankenphp install` | Downloads the FrankenPHP release this Dotlane pins (190 MB on macOS) from its GitHub release into Dotlane's state folder, and keeps it only if its SHA-256 matches. A `frankenphp` already on the `PATH` is used too; the newest found wins. There is no build for Intel Macs. |

Dotlane starts a runtime when the first site needs it, stops it when the last one goes, and
starts it again, waiting longer each time (up to 30 s), when it exits. `add` and `up` refuse a
PHP site whose folder, worker script or PHP version is missing, and list the versions found with
the command that installs the one asked for. Requests go as Caddy's `php_fastcgi` would send
them: a file is served as it is, a `.php` file runs, a folder with an `index.php` gets its
trailing slash, and anything else goes to `index.php` (Laravel's and Symfony's front
controller). Hidden files such as `.env` and `.git` are never served. PHP sees `HTTPS=on` and the
HTTPS port, so apps build `https://` URLs.

## Domain endings

| Command | What it does |
| --- | --- |
| `dotlane tld ls [--json]` | Lists the endings, which is the primary, and whether each is routed to Dotlane; also an ending Dotlane still routes that `config.toml` no longer lists. |
| `dotlane tld add <ending> [--force]` | Adds an ending: saves it in `config.toml`, routes it (password, or one administrator prompt on Windows) and restarts the service, then checks a lookup. Every site answers on it at once. |
| `dotlane tld rm <ending>` | Removes an ending and its route; the sites stay, on the other endings. The last ending cannot be removed. Also removes a route left for an ending the file no longer lists. |
| `dotlane tld use <ending> [--force]` | Makes an ending the primary, adding it first if needed. The others keep working until removed. |

Endings that would hide real websites (any public top-level domain, such as `.com` or `.dev`, and
any two-letter one) or that resolvers treat specially (`.local`, `.localhost`, `.invalid`,
`.onion`, `.alt`, `.arpa`) are refused. Ones a router or company network may already answer
(`.lan`, `.home`, `.corp`, `.mail`, `.localdomain`, `.intranet`, `.private`) need `--force`.
`.dotlane`, `.test` and `.internal` can never collide. When Dotlane is not installed, the
commands only save the file; `dotlane install` routes it.

## Setup and checks

| Command | What it does |
| --- | --- |
| `dotlane install` | Login service, CA trust (password), resolver file per ending (password). Checks for conflicts first and changes nothing if another tool is in the way. Restarts the service when it is stuck, or the binary or config changed after it started. Safe to re-run. |
| `dotlane install --tld <ending>` | Makes the ending the primary first, like `dotlane tld use`, then installs. Endings that need `--force` go through `dotlane tld use` instead. |
| `dotlane install --refresh` | Only updates an installed service to this binary (its copy, the plist) and restarts it if it runs an older build or config. Never asks for a password; when Dotlane is not installed it says so and exits 0. What package managers run after an upgrade. |
| `dotlane uninstall [--purge]` | Reverses install. `--purge` also deletes the sites, the CA's keys and the log. |
| `dotlane doctor [--fix \| --json]` | Checks the config, the service, the daemon, ports 80/443 and the DNS port, private files, the CA's trust, each resolver file and a real lookup, endings that need `--force` or would be refused, routes left for endings no longer configured, and each PHP runtime in use and PHP site that cannot be served. Names other tools in the way, with their fix. `--fix` runs install for what it repairs; `--json` prints the checks. |
| `dotlane migrate` | Moves a Bash prototype install to Dotlane, keeping its sites and trusted CA (see [`setup.md`](setup.md#moving-from-the-prototype)). |
| `dotlane serve` | Runs the service in the foreground; the login service runs this. |
| `dotlane version` | Prints the version (also `--version`). |

## Configuration

`~/.config/dotlane/config.toml` is optional; every setting has a default. Another file can be given
with `--config <file>` or `$DOTLANE_CONFIG`, and must then exist. Unknown keys are rejected.

```toml
tld = "dotlane"          # the default; or several: tlds = ["dotlane", "test"]
dns_port = 10053         # install picks a free one if the default is taken, and saves it here
http_port = 80
https_port = 443
state_dir = "…"          # default ~/Library/Application Support/dotlane
service_name = "dev.dotlane"
```

With several endings, every site answers on each; the first is the primary, whose URL `add`,
`up` and `ls` print first, and which `GET /v1/status` reports as `primary_tld`.

`dotlane tld` edits the `tld`/`tlds` line in place, keeping the rest of the file. An ending set
by hand that `dotlane tld add` would refuse or ask `--force` for still works; every command
and `doctor` warn about it.

A relative `state_dir` is relative to the config file. Ports below 1024 are bound on all
interfaces on macOS (which allows a normal user nothing else; Linux and Windows bind them on loopback) and drop requests from other machines; other
ports stay on loopback.

## Pages from Dotlane

- A name under the ending with no site gets a "No site at …" page (404) with the command to add one.
- A site whose dev server is not answering gets a page naming the port (502).
- A PHP site whose runtime is not answering gets a page naming it and its log (502); one that
  cannot be served (its PHP version or folder is gone) gets a page saying why, with the fix (503).
- `http://` redirects (308) to the same URL on HTTPS.

## Output and exit codes

Output is coloured on a terminal only. `NO_COLOR=1` turns colour off and always wins;
`CLICOLOR_FORCE=1` forces it (for `less -R`).

A failure prints an `ERROR` block on stderr, with what to do next, and exits 1. That includes
`doctor` finding a problem, so scripts can rely on its exit code. A command line that is wrong
(missing argument, unknown flag) adds `try --help for usage`.
