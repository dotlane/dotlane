# Setup (macOS)

Reproduces the reference install. Steps marked **(password)** need `sudo` or a keychain prompt;
everything else runs as the user. The steps use the default ending, `traix`; for another one, follow
them and then [Using another domain ending](#using-another-domain-ending).

## Prerequisites

- Homebrew.
- Nothing else answering `*.traix`, and ports 80/443 and UDP 10053 free. Tools such as Laravel
  Herd/Valet bind 80/443 and install their own resolver; remove or stop them first
  (see `troubleshooting.md`).

## 1. Install

```bash
brew install caddy dnsmasq
```

## 2. Wildcard DNS for `*.traix`

```bash
mkdir -p ~/.config/dnsmasq ~/Library/Logs
cp files/dnsmasq/traix.conf ~/.config/dnsmasq/traix.conf

# LaunchAgent: substitute $HOME, then load it
sed "s#\$HOME#$HOME#g" files/launchd/dev.traix.dnsmasq.plist > ~/Library/LaunchAgents/dev.traix.dnsmasq.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.traix.dnsmasq.plist

# (password) send only *.traix lookups to it
printf 'nameserver 127.0.0.1\nport 10053\n' | sudo tee /etc/resolver/traix
```

## 3. Caddy

```bash
mkdir -p ~/.config/caddy/sites
sed "s#\$HOME#$HOME#g" files/caddy/Caddyfile > /opt/homebrew/etc/Caddyfile
caddy validate --config /opt/homebrew/etc/Caddyfile --adapter caddyfile
brew services start caddy

# (password) trust Caddy's local CA in the System keychain, so browsers accept its certificates
caddy trust
```

Caddy only opens 80/443 once the first site exists.

## 4. The helper

```bash
mkdir -p ~/.local/bin
cp files/bin/dotlane ~/.local/bin/dotlane && chmod +x ~/.local/bin/dotlane
# if ~/.local/bin is not on PATH yet:
printf '\nexport PATH="$HOME/.local/bin:$PATH"\n' >> ~/.zshrc
```

## 5. Verify

```bash
dotlane doctor          # resolver, dnsmasq, Caddy, CA trust — expect "all good"
dotlane add demo 8080
python3 -m http.server 8080 &
curl -sS https://demo.traix/ -o /dev/null -w '%{http_code} tls-verify=%{ssl_verify_result}\n'
# 200 tls-verify=0   (0 = certificate verified, no -k needed)
kill %1; dotlane rm demo
```

Other useful checks:

```bash
dig +short anything.traix @127.0.0.1 -p 10053        # 127.0.0.1
dscacheutil -q host -a name anything.traix           # what macOS itself resolves
scutil --dns | grep -A3 'domain   : traix'           # resolver registered
curl -s localhost:2019/config/ | head -c 300         # Caddy's live config
```

## Using another domain ending

The ending is set in `~/.config/dotlane/config.toml` (see [`cli.md`](cli.md#configuration)). To
use, for example, `.internal`:

```bash
mkdir -p ~/.config/dotlane
printf 'tld = "internal"\n' > ~/.config/dotlane/config.toml

# answer *.internal too (the file can list several endings), then restart dnsmasq
printf 'address=/internal/127.0.0.1\naddress=/internal/::1\n' >> ~/.config/dnsmasq/traix.conf
launchctl kickstart -k gui/$(id -u)/dev.traix.dnsmasq

# (password) send *.internal lookups to it
printf 'nameserver 127.0.0.1\nport 10053\n' | sudo tee /etc/resolver/internal

dotlane doctor          # expect "domain ending .internal" and "all good"
```

Then re-add existing sites (`dotlane add <name> <port>`) to move them to the new ending, and update
any app that lists the old origin (see `app-integration.md`). To drop the old ending, remove its
`address=` lines and `sudo rm /etc/resolver/traix`.

Choosing an ending:

- **Safest:** `test`, `localhost` and `internal` are reserved and will never be public.
- **Never:** a public ending (`dev`, `app`, `com`, …; list at
  <https://data.iana.org/TLD/tlds-alpha-by-domain.txt>) — sites would shadow real websites — or
  `local`, which Bonjour uses.
- **Branded** (`traix`): works, with the small risk that the ending becomes public one day.

## Uninstall

```bash
brew services stop caddy
launchctl bootout gui/$(id -u)/dev.traix.dnsmasq
rm ~/Library/LaunchAgents/dev.traix.dnsmasq.plist ~/.config/dnsmasq/traix.conf
rm -r ~/.config/caddy/sites ~/.local/bin/dotlane
sudo rm /etc/resolver/traix                                    # (password)
caddy untrust                                                  # (password) remove the root CA
rm /opt/homebrew/etc/Caddyfile
brew uninstall caddy dnsmasq                                   # optional
```

Apps that listed a `.traix` origin (see `app-integration.md`) should drop it.

## Reference install log (2026-10-29)

Done on the MyApp developer's Mac, replacing Laravel Herd:

1. Quit Herd; ran its bundled uninstaller (`/Applications/Herd.app/Contents/Resources/uninstall`).
2. Removed `/etc/resolver/test` by hand — the uninstaller only removes it when the file is exactly
   `nameserver 127.0.0.1`, and Herd had written a `port 1053` line too.
3. Removed a leftover `PHP_INI_SCAN_DIR` export from `~/.zshrc` that the uninstaller misses.
4. Steps 1–5 above. Port 1053 was taken by Yerd, so dnsmasq moved to 10053.
