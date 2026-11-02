# Setup (Linux)

How to install Dotlane on Linux, check it, upgrade it and remove it. Sites, `dotlane.toml` and
`doctor` work as on macOS ([`setup.md`](setup.md#3-first-site)); this page covers what differs.
For WSL2, see [`wsl.md`](wsl.md).

## Requirements

- x86-64 or arm64.
- **systemd with systemd-resolved**, and `/etc/resolv.conf` pointing at its stub
  (`127.0.0.53`). Ubuntu and Fedora have this by default. On Arch, Debian and others, enable it
  first:

  ```bash
  sudo systemctl enable --now systemd-resolved
  sudo ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
  ```

  `dotlane install` checks this before changing anything, and says what is missing.
- Free ports 80 and 443 on `127.0.0.1`. `install` names whatever holds them.
- Optional: `certutil` (`libnss3-tools` on Debian and Ubuntu, `nss-tools` on Fedora, `nss` on
  Arch), so Firefox and Chromium trust Dotlane too. Without it, `install` says what to add.

Tested end to end on Ubuntu, Fedora and Arch, on every change (CI).

## 1. Get the `dotlane` command

**npm:** `npm install -g dotlane` (installs `@dotlane/linux-x64` or `@dotlane/linux-arm64`).

**A package** from the [release](https://github.com/dotlane/dotlane/releases), which puts
`dotlane` in `/usr/bin`:

```bash
sudo apt install ./dotlane_<version>_linux_amd64.deb        # Debian, Ubuntu
sudo dnf install ./dotlane_<version>_linux_amd64.rpm        # Fedora, RHEL
sudo pacman -U ./dotlane_<version>_linux_amd64.pkg.tar.zst  # Arch
```

Or the `.tar.gz`, with the binary anywhere on your PATH. **From source:** as on macOS
([`setup.md`](setup.md#1-get-the-dotlane-command)).

## 2. Install

```bash
dotlane install
```

It sets up four things, and asks for your password (sudo) for the last three, each explained
before it runs:

1. **Login service**: a systemd user unit, `~/.config/systemd/user/dev.dotlane.service`, which
   runs `dotlane serve` from a copy of the binary in `~/.local/share/dotlane/bin/`.
2. **Ports 80 and 443**: `/etc/sysctl.d/50-dotlane.conf` sets
   `net.ipv4.ip_unprivileged_port_start=80`, so the service can listen on them without root. It
   only binds them on `127.0.0.1` and `::1`.
3. **Certificate authority**: trusted in the system store (`update-ca-certificates` or
   `update-ca-trust`) and, with `certutil`, in `~/.pki/nssdb` and each Firefox profile.
4. **DNS for `.traix`**: a system unit, `/etc/systemd/system/dotlane-dns-dev.dotlane.service`,
   that gives systemd-resolved a small virtual network link carrying only `~traix`. Lookups for
   `*.traix` go to Dotlane, and every other lookup stays where it was.

Why a link and not a `resolved.conf.d` file: a DNS server set there becomes a default route, so
every lookup on the machine would be sent to Dotlane first.

## Logs and restarts

```bash
journalctl --user -u dev.dotlane          # the service's log
systemctl --user restart dev.dotlane      # restart it by hand
resolvectl domain                         # the dl-… link should list ~traix
```

## Upgrading

`npm install -g dotlane`, or a newer package. As on macOS, the next `dotlane` command that uses
the service updates its copy of the binary and restarts it, without a password; or run
`dotlane install --refresh`.

## Uninstalling

```bash
dotlane uninstall           # add --purge to also delete sites, the CA and its keys
```

Run it **before** removing the package: the deb, rpm and Arch packages have no removal scripts,
and the service would keep running. Uninstall removes the user unit, the DNS unit and its link,
the sysctl file (restoring the default of 1024), and the CA from the system store and NSS.
