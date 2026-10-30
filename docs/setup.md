# Setup (macOS)

How to install Dotlane, check it, upgrade it and remove it. What each step does is explained in
[`how-it-works.md`](how-it-works.md). Moving from the Bash prototype instead? See
[Moving from the prototype](#moving-from-the-prototype).

## Requirements

- macOS, Apple Silicon or Intel.
- Go 1.27, to build the binary (packages come in Phase 2 of the [roadmap](roadmap.md)).
- Free ports 80 and 443. `dotlane install` checks this first, and names whatever holds them.

## 1. Build and put it on your PATH

```bash
git clone https://github.com/dotlane/dotlane && cd dotlane
go build -o bin/dotlane ./cmd/dotlane
mkdir -p ~/.local/bin && ln -sf "$PWD/bin/dotlane" ~/.local/bin/dotlane
```

If `dotlane` is not found afterwards, `~/.local/bin` is not on your PATH:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc   # then open a new terminal
```

`dotlane install` copies the binary to `~/Library/Application Support/dotlane/bin/` and the login
service runs that copy: macOS holds back background jobs started from Documents, Desktop or
Downloads until you approve them, and a new build would need a new approval.

## 2. Install

```bash
dotlane install
```

It sets up three things, and asks for your password for the last two, each explained before its
`sudo` command runs:

1. **Login service**: runs `dotlane serve` at login and restarts it if it stops.
2. **Certificate authority**: trusts Dotlane's local CA in the System keychain, so browsers and
   tools accept its certificates.
3. **DNS for `.traix`**: writes `/etc/resolver/traix`, which sends `*.traix` lookups, and only
   those, to Dotlane.

Steps already done are skipped, so it is safe to run again. If another tool is in the way, it
stops before changing anything and says which tool and how to move it.

## 3. First site

With a dev server on port 5173:

```bash
dotlane add myapp 5173
# ✓ https://myapp.traix → localhost:5173
```

Or, in a project, list its sites in a `dotlane.toml` and commit it:

```toml
[sites.myapp]
port = 5173
```

```bash
dotlane up      # dotlane down removes them
```

Type the full `https://myapp.traix` the first time: `.traix` is not a public ending, so a browser
treats a bare `myapp.traix` as a search. What the app itself may need (allowed hosts, origins,
cookies) is in [`app-integration.md`](app-integration.md).

## 4. Check

```bash
dotlane doctor          # every check, with the fix for anything wrong
dotlane doctor --fix    # repairs what dotlane install can
curl -sI https://myapp.traix | head -1
```

## Upgrading

Rebuild, then run `dotlane install`: it updates the service's copy of the binary and restarts the
service, and does the same after a change to the config file. `doctor` warns when either is
out of date.

```bash
git pull && go build -o bin/dotlane ./cmd/dotlane && dotlane install --refresh
```

`--refresh` does only that part of install, so it never asks for a password; plain
`dotlane install` also works and re-checks everything else.

## Using another domain ending

In `~/.config/dotlane/config.toml`:

```toml
tld = "test"                 # or several: tlds = ["traix", "test"]
```

Then run `dotlane install` again: it restarts the service to load the change and writes the
resolver file for the new ending (one password). Sites answer on every ending. `local` is
refused, since macOS uses it for Bonjour.

## Moving from the prototype

With the prototype (Homebrew Caddy, dnsmasq and the Bash `dotlane`) installed:

```bash
go build -o bin/dotlane ./cmd/dotlane
./bin/dotlane migrate
```

It keeps the sites and the already-trusted CA, so no password is needed. It stops Homebrew's Caddy
and the dnsmasq agent, installs Dotlane, and points `~/.local/bin/dotlane` at the binary. If the
install fails, the prototype is started again. It deletes nothing; the prototype's files it lists
at the end can go once you are happy. The prototype's own setup is kept in
[`prototype/setup.md`](prototype/setup.md).

## Uninstall

```bash
dotlane uninstall           # stops the service, removes the CA's trust and the resolver files
dotlane uninstall --purge   # also deletes the sites, the CA's keys and the log
```

Each step that needs your password is explained first. Resolver files with other content are left
alone (they belong to another tool). Then remove the binary and `~/.local/bin/dotlane`.
