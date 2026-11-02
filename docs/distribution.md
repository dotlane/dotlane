# Distribution

How Dotlane gets from this repository to a user's machine. There is one product, the `dotlane`
Go binary, built from this repository. Every channel ships that same binary; the desktop apps,
later, sit on top of it. How to cut a release is in [`releasing.md`](releasing.md).

## From a tag to a user

```text
                    dotlane/dotlane  (Go source, this repository)
                                   │
                            git tag v<version>
                                   │
                    Release workflow (CI, GoReleaser)
                                   │
          builds ONE binary per OS and CPU, from the same commit
       today: macOS arm64 and Intel   ·   later: also Linux and Windows
                                   │
                                   ▼
          ┌────────────────────────────────────────────────┐
          │  GitHub Release (a draft, then published)      │
          │  the source of truth for every channel         │
          │                                                │
          │  dotlane_<version>_darwin_arm64.tar.gz         │
          │  dotlane_<version>_darwin_amd64.tar.gz         │
          │  checksums.txt, one SBOM per archive           │
          │  dotlane.rb               the Homebrew cask    │
          │  dotlane-*.tgz (three)    the npm packages     │
          └───────┬────────────────────────────┬───────────┘
                  │                            │
    publishing the draft runs               npm publish (by hand until the
    tap.yml, which commits the cask         repository is public; then the
                  │                         release workflow, with trusted
                  │                         publishing)
                  ▼                            ▼
       dotlane/homebrew-tap               npmjs.com
       Casks/dotlane.rb, pointing         dotlane                 a small Node launcher
       at the release's archives          @dotlane/darwin-arm64   the binary
                  │                       @dotlane/darwin-x64     the binary
                  ▼                            ▼
   brew install dotlane/tap/dotlane     npm install -g dotlane, or npx dotlane
                  │                            │
                  └─────────────┬──────────────┘
                                ▼
                the same `dotlane` binary on the user's Mac
            `dotlane install` then runs it as a background service
```

- **One build, many wrappers.** The archives, the cask and the npm packages all carry the binary
  GoReleaser built for that tag; nothing is rebuilt per channel.
- **The GitHub Release is the source of truth.** The cask downloads the release's archives and
  checks their SHA-256; the npm platform packages contain the same binary.
- **Pre-releases** (`v0.1.0-rc.3`) stay drafts and go to npm under the `next` tag; the tap only
  changes when a release (not a pre-release) is published.
- **Upgrades:** `brew upgrade` or `npm install -g dotlane` replaces the binary; the next `add`,
  `rm`, `ls`, `up` or `down` restarts the service on it ([`setup.md`](setup.md#upgrading)).

## More operating systems, and desktop apps

"An app for each OS" covers two different things:

```text
  1. Linux and Windows       core roadmap (roadmap.md), this repository
     The same Go binary, compiled for more operating systems by the same release workflow,
     shipped through the same channels (GitHub Release, npm; later perhaps apt, winget or
     scoop). No new repository.

  2. Desktop apps            repository dotlane/desktop, with its own roadmap
     A graphical app (a menu-bar app, macOS first), not another build of the binary. It is a
     client of the daemon, like the CLI, through the daemon's local API, and adds no capability
     of its own. It bundles or installs the dotlane binary.

         desktop app ──┐
                       ├──►  dotlane daemon (the binary)  ──►  DNS · HTTPS · certificate authority
         dotlane CLI ──┘
```

The website (`dotlane/website`) holds the landing page and is hosted separately; it is not a
distribution channel.

Each product plans its own features: the [core](roadmap.md), [npm](../npm/ROADMAP.md),
[Homebrew](homebrew.md), and the website and desktop apps in their repositories.

## Repositories

| Repository | Holds | Public |
| --- | --- | --- |
| `dotlane/dotlane` | The Go source, the release workflow, the npm and Homebrew packaging, the docs | yes, since 2026-11-01 |
| `dotlane/homebrew-tap` | `Casks/dotlane.rb`, written by `tap.yml` (empty while Homebrew is on hold) | yes, since 2026-11-01 |
| `dotlane/desktop` | The desktop apps, with their own roadmap | later |
| `dotlane/website` | The landing page | no; the site itself is |
