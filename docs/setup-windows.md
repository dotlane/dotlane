# Setup (Windows)

How to install Dotlane on Windows, check it, upgrade it and remove it. Sites, `dotlane.toml` and
`doctor` work as on macOS ([`setup.md`](setup.md#3-first-site)); this page covers what differs.
With a dev server in WSL2, see [`wsl.md`](wsl.md).

## Requirements

- Windows 10 (1803 or later) or 11, x64 or arm64; Windows Server 2022 or later.
- An account that can approve a UAC prompt (an administrator).
- **Port 53 free on `127.0.0.1`.** Windows sends a domain ending to a DNS server by address only,
  with no port, so Dotlane's DNS must listen on 53. `install` names whatever holds it (Internet
  Connection Sharing, Docker Desktop, another DNS proxy).
- Ports 80 and 443 free on `127.0.0.1`. IIS and other `http.sys` users can hold them; `install`
  says which.

## 1. Get the `dotlane` command

**npm:** `npm install -g dotlane` (installs `@dotlane/win32-x64` or `@dotlane/win32-arm64`).

**Zip:** `dotlane_<version>_windows_amd64.zip` (or `arm64`) from the
[release](https://github.com/dotlane/dotlane/releases); put `dotlane.exe` in a folder on your
PATH. The binaries are not signed yet, so SmartScreen may warn the first time.

## 2. Install

```powershell
dotlane install
```

It lists each step that needs administrator rights, with the exact command, then runs them all
in one elevated PowerShell: **one UAC prompt**.

1. **Login service**: a Task Scheduler task, `\Dotlane\dev.dotlane`, that runs `dotlane serve`
   at logon, as you, without a window, and restarts it if it stops. It runs a copy of the binary
   in `%LocalAppData%\Programs\dotlane\`.
2. **Certificate authority**: trusted in the machine's Trusted Root store
   (`Cert:\LocalMachine\Root`). Edge, Chrome and `curl.exe` use that store. Firefox uses it too
   when `security.enterprise_roots.enabled` is on (the default in recent versions); `doctor`
   reminds you when Firefox is installed.
3. **DNS for `.dotlane`**: a Name Resolution Policy Table rule that sends `*.dotlane`, and only
   that, to `127.0.0.1`.

Configuration lives in `%AppData%\dotlane\config.toml`; sites, the CA and the control socket in
`%LocalAppData%\dotlane`, readable by you and SYSTEM only.

## Logs and restarts

```powershell
Get-Content "$env:LOCALAPPDATA\dotlane\logs\dev.dotlane.log" -Tail 50
dotlane install --refresh                     # restart the service on the current binary
Get-DnsClientNrptRule | Where-Object Comment -like 'dotlane:*'
```

`curl.exe` checks certificate revocation by default, and a local CA publishes none. Use
`curl.exe --ssl-revoke-best-effort https://myapp.dotlane`; browsers are not affected.

## Upgrading

`npm install -g dotlane`, or replace `dotlane.exe`. The next `dotlane` command that uses the
service updates its copy and restarts it. If Windows refuses that without administrator rights,
the command says so: run `dotlane install --refresh`, which asks once.

## Uninstalling

```powershell
dotlane uninstall           # add --purge to also delete sites, the CA and its keys
```

One UAC prompt: it removes the task, the NRPT rules and the certificate, then the service's
copy of the binary.
