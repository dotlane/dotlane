### Added

- **`api` in `GET /v1/status`**: the version of the control API the daemon serves. Every command
  that uses the daemon checks it first, and stops with what to update when this `dotlane` and the
  service do not match: a service that was not refreshed, or an older command left on the PATH
  (another Node version, a project's `npx`). The command gets the update command for how it was
  installed (npm, npx, pnpm, yarn, bun or bunx, globally, in a project or a workspace, Volta,
  Homebrew, `go install` or a clone), in a form any shell runs, Windows PowerShell 5.1 included.
  A command older than the service, but compatible, warns.

### Fixed

- **Commands started together after an upgrade restarting the service over each other**: they now
  take turns, and the ones that wait find it already updated. `install`, `doctor --fix` and
  `uninstall` take their turn too, so an automatic update no longer restarts the service in the
  middle of one, or puts back a service `uninstall` is removing.
