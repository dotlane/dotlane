# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: **Security → Report a vulnerability** on
this repository (private vulnerability reporting). Do not open a public issue.

Include what you found, how to reproduce it, and the output of `dotlane version` and
`dotlane doctor --json` if relevant. You will get an answer within a week; a fix and an advisory
follow as soon as a release can be made.

## Supported versions

Until 1.0, only the latest release receives security fixes.

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
