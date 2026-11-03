### Added

- **`api` in `GET /v1/status`**: the version of the control API the daemon serves. Every command
  that uses the daemon checks it first, and stops with what to update when this `dotlane` and the
  service do not match: an older command left on the PATH (another Node version, a project's
  `npx`), or a service that was not refreshed. A command older than the service, but compatible,
  warns.

### Fixed

- **Commands started together after an upgrade restarting the service over each other**: they now
  take turns, and the ones that wait find it already updated.
