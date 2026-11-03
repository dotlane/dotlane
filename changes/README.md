# Changelog fragments

One file per pull request a user would notice, named after its branch (`feat/port-retry` →
`port-retry.md`), holding only [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) sections:

```markdown
### Fixed

- **`dotlane up` retrying a busy port forever**: it now gives up after five tries and names the
  process holding the port.
```

Sections: `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`. Internal changes need
no fragment. Pull requests never edit `CHANGELOG.md` itself, so two of them never conflict over it;
a release collects the fragments into it (`scripts/changelog.sh`, and
[`docs/branching.md`](../docs/branching.md), Changelog).
