<!--
Thanks for contributing. The title becomes the squash-merge commit message: say what changes,
in the imperative ("Retry fresh ports when Start is asked for port 0").
Anything bigger than a fix should have an agreed issue first; see CONTRIBUTING.md.
Security problems go through SECURITY.md, never a public pull request.
-->

## What and why

<!-- What this changes, and the problem it solves. One or two sentences. -->

Closes #

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Behaviour change a user would notice (commands, flags, output, config)
- [ ] Refactor or internal change
- [ ] Docs only

## Platforms affected

- [ ] macOS
- [ ] Linux
- [ ] Windows / WSL2
- [ ] npm package
- [ ] Homebrew

## How it was tested

<!--
Commands you ran and what you saw. For CLI output changes, paste before and after.
Don't run scripts/integration.sh or scripts/brew-e2e.sh on your Mac: CI runs them.
-->

## Where to look closely

<!-- The risky or non-obvious parts: daemon, DNS, certificates, install steps, permissions. -->

## Checklist

- [ ] `scripts/check.sh` passes
- [ ] Tests added or updated (macOS-only code: `internal/install`)
- [ ] Docs updated if behaviour changed (`docs/cli.md`, `docs/setup*.md`)
- [ ] `CHANGELOG.md` has a line under `[Unreleased]` if a user would notice
- [ ] `NOTICE` regenerated with `scripts/notice.sh` if dependencies changed
- [ ] User-facing output goes through `internal/ui`
