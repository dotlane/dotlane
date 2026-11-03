# WSL2

Windows Subsystem for Linux runs a Linux machine next to Windows, with its own network stack, DNS
and certificate store. Where Dotlane runs decides which browser trusts it, so pick by where the
browser is.

| Dev server | Browser | Run Dotlane | Status |
| --- | --- | --- | --- |
| In WSL | On Windows | **On Windows** | Supported, the recommended setup |
| In WSL | In WSL (WSLg) | In WSL, as on Linux | Supported where WSL runs systemd and systemd-resolved |
| In WSL | On Windows | In WSL | Not supported |

## Dev server in WSL, browser on Windows

Install Dotlane on Windows ([`setup.md`](setup.md)) and register the site with the port the dev
server uses in WSL:

```powershell
dotlane add myapp 5173
```

WSL forwards Windows' `localhost` to ports that Linux programs listen on, so Dotlane on Windows
reaches the dev server in WSL as if it ran on Windows. This holds in WSL's default NAT mode
(`localhostForwarding`, on by default) and in mirrored networking mode. Two things can get in the
way:

- **The dev server listens on IPv6 or IPv4 only.** Dotlane dials `localhost:<port>`. If the page
  says the dev server is not answering, start the dev server on `0.0.0.0` (for Vite:
  `vite --host`), or turn on mirrored networking (`networkingMode=mirrored` in `.wslconfig`).
- **`dotlane.toml` lives in the WSL file system.** Run `dotlane up` from Windows in the
  project's `\\wsl$\<distro>\…` path, or keep a copy of the `dotlane add` commands; the
  Windows binary cannot read a project through a Linux path.

## Everything in WSL

With a browser inside WSL (WSLg), Dotlane installs as on Linux: a systemd user service,
systemd-resolved for DNS, and the CA in the distribution's store and NSS. WSL needs two settings
it does not have by default:

```ini
# /etc/wsl.conf
[boot]
systemd=true

[network]
generateResolvConf=false
```

Then point `/etc/resolv.conf` at systemd-resolved's stub
(`sudo ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf`), run `wsl --shutdown`
from Windows, open WSL again, and run `dotlane install`. `dotlane doctor` says what is still
missing.

## Dotlane in WSL, browser on Windows: not supported

Windows would have to send `*.dotlane` lookups into WSL and trust a certificate authority that
lives there, and the address of the WSL machine changes on every start in NAT mode. Run Dotlane
on Windows instead; the dev server can stay in WSL.
