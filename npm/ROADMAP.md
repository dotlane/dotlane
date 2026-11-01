# Roadmap: npm

`npm install -g dotlane` and `npx dotlane`. The packages are built here, in `npm/`, from each
release's binaries:

| Package | What it holds |
| --- | --- |
| `dotlane` | A small Node launcher ([`dotlane/bin/dotlane.js`](dotlane/bin/dotlane.js)) with the platform packages as `optionalDependencies` |
| `@dotlane/darwin-arm64`, `@dotlane/darwin-x64` | One binary each, with `os` and `cpu` set so npm installs only the matching one |

How they are built and published is in [`../docs/releasing.md`](../docs/releasing.md#npm).
Grouped like the [core roadmap](../docs/roadmap.md): Now, Next, Later and Done.

## Now

- Nothing in progress.

## Next

- **Check an upgrade.** `npm install -g dotlane` from `0.1.0` to the next release, then the next
  `dotlane` command restarts the service on the new binary. Covered by CI; needs a second release
  to check on a real Mac.
- **Check a project dependency.** `"devDependencies": {"dotlane": "…"}`, then `npx dotlane up`
  from the project's `dotlane.toml`.
- **Unsupported platforms at install time.** Today the launcher installs anywhere and says which
  platforms are supported when it runs. Decide whether to set `os` on `dotlane` too, so npm
  refuses the install on Linux and Windows until they are supported.

## Later

- **Signed binaries**, once the core signs and notarizes releases
  ([core roadmap](../docs/roadmap.md#now)). npm downloads are not quarantined, so nothing
  changes for users, but the binary carries the developer's identity.
- **Linux and Windows packages:** `@dotlane/linux-x64`, `@dotlane/linux-arm64`,
  `@dotlane/win32-x64` and `@dotlane/win32-arm64`, added to the launcher's table and the release
  workflow once the core runs on them ([core roadmap](../docs/roadmap.md#later)). Users run the
  same `npm install -g dotlane`.

## Done

- The launcher and the two macOS packages, with no install scripts, in lockstep versions;
  pre-releases go to `next`, releases to `latest` (2026-10-30).
- Published from the release workflow through trusted publishing, with provenance; `0.1.0` is
  `latest` (2026-11-01).
- Publishing access requires two-factor authentication and disallows tokens on all three
  packages (2026-11-01).
- The exit check on a new user account passed: install, a site over HTTPS, uninstall, then `npx`
  (2026-11-01).
