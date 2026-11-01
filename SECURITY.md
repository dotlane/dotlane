# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: **Security → Report a vulnerability** on
this repository (private vulnerability reporting). Do not open a public issue.

Include what you found, how to reproduce it, and the output of `dotlane version` and
`dotlane doctor --json` if relevant. You will get an answer within a week; a fix and an advisory
follow as soon as a release can be made.

## Supported versions

Until 1.0, only the latest release receives security fixes.

## Where the CA key lives

`dotlane install` creates a certificate authority for this Mac and trusts its root in the System
keychain. Every browser and tool on the Mac then accepts any certificate that root signs, for
any name, so the root's private key is the secret that matters. Caddy, inside the daemon,
creates and renews these files under `~/Library/Application Support/dotlane/` (a folder of mode
0700), all owned by you and mode 0600:

| File | What it is |
| --- | --- |
| `caddy/pki/authorities/local/root.key` | The root's private key; valid 10 years |
| `caddy/pki/authorities/local/root.crt` | The root certificate, the one the keychain trusts (not secret) |
| `caddy/pki/authorities/local/intermediate.key` | The intermediate's key; renewed every 7 days |
| `caddy/certificates/local/<name>/` | Each site's certificate and key; renewed every 12 hours |

- **It never leaves the Mac.** Each Mac creates its own CA; nothing uploads or syncs it, and no
  CA ships with Dotlane. Don't copy it to another machine or share it with a team.
- **Any process running as you can read it**, as with your browser profile or SSH keys; other
  users on the Mac cannot. `dotlane doctor` checks the folder, the root key and the control
  socket for their modes and owner, and `dotlane doctor --fix` tightens the modes.
- **If you think it leaked:** `dotlane uninstall --purge` removes the root from the keychain and
  deletes the keys, and `dotlane install` then creates a new CA.
- With `state_dir` set in `~/.config/dotlane/config.toml`, the files are under that folder
  instead.

More on the trust model in [`docs/security.md`](docs/security.md).

## What is in scope

Dotlane installs a local certificate authority that the system trusts, routes a domain ending
to itself and runs a background service, so these matter most:

- anything that lets another user, another machine or a web page use the CA's key, obtain a
  certificate from it, or reach a site through Dotlane;
- privilege escalation through `dotlane install`, `uninstall`, `migrate` or the login service;
- the control socket (`dotlane.sock`, mode 0600) accepting another user;
- the daemon answering requests from other machines (it drops them by design).

How the CA, the listeners and the control socket are protected is described in
[`docs/how-it-works.md`](docs/how-it-works.md) and [`docs/security.md`](docs/security.md); how
the project's repositories, secrets and releases are protected, in
[`docs/security-guide.md`](docs/security-guide.md).
