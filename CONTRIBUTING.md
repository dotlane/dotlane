# Contributing

Thanks for helping. Bugs and ideas go in [issues](https://github.com/dotlane/dotlane/issues/new/choose);
a bug report asks for `dotlane doctor --json`, which answers most first questions. Security
problems go through [`SECURITY.md`](SECURITY.md), never a public issue.

## Before a pull request

For anything bigger than a fix, open an issue first, so the design can be agreed before the
code. The [roadmap](docs/roadmap.md) says what is planned and in which phase.

## Working on the code

Go 1.27 on macOS. The [README's development section](README.md#development-go-version) runs a
dev instance on spare ports with its own state, next to a real install without touching it.
[`docs/how-it-works.md`](docs/how-it-works.md) explains the daemon, certificates and install.

Before pushing:

```bash
scripts/check.sh
```

It runs what CI's `test` job runs: gofmt, build, `go vet` and `govulncheck` for Linux and macOS,
`go test -race`, the npm tests, `goreleaser check`, and whether `NOTICE` lists every embedded
module. After adding or removing a dependency, run `scripts/notice.sh` and commit the new
`NOTICE`.

**Don't run `scripts/integration.sh` or `scripts/brew-e2e.sh` on your Mac.** They install
Dotlane system-wide with sudo, under the same service name as a real install, so they run only on
CI's throwaway macOS runners. Pull requests run them there.

## What a pull request needs

- **Tests** for the change. macOS-only code has its own tests in `internal/install`; the
  end-to-end paths are covered by the CI jobs above.
- **Docs** in the same pull request when behaviour changes: [`docs/cli.md`](docs/cli.md) for
  commands and flags, [`docs/setup.md`](docs/setup.md) for install steps.
- **A line in [`CHANGELOG.md`](CHANGELOG.md)** under `[Unreleased]` for anything a user would
  notice (Added, Changed, Fixed, Security). Internal changes need none.
- **Green CI:** `test`, `install-tests`, `integration`, `release-snapshot` and `homebrew`.
  Pull requests are squash-merged, so the title becomes the commit message: say what changes,
  in the imperative ("Retry fresh ports when Start is asked for port 0").

## Style

Code reads like the code around it: standard Go, `gofmt`, small packages under `internal/` (the
table in the README says what each does). User-facing output goes through `internal/ui`, which
handles colour, alignment and `NO_COLOR`. Messages say what happened and what to do next; an
error names the thing in the way and the command that fixes it.

Docs are plain English with short sentences, written for someone setting Dotlane up for the
first time. Examples use `myapp` and the `.traix` ending.

## Licence

Dotlane is Apache-2.0. By contributing, you agree your contribution is licensed under the same
terms (section 5 of the [`LICENSE`](LICENSE)); no separate agreement is needed.
