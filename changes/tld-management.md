### Added

- **Warnings about risky domain endings.** Every command warns when `config.toml` sets an ending
  that is a public top-level domain (`.dev`, `.com`, from an embedded copy of IANA's list), a
  two-letter ending, a special-use name (`.localhost`, `.invalid`, `.onion`, `.alt`, `.arpa`)
  or one routers often answer (`.lan`, `.home`, `.corp`). The ending keeps working.
- **`dotlane tld ls | add | rm | use`**: list the domain endings, add one, remove one and its
  route, or choose the primary. Each change is saved in `config.toml` and, when Dotlane is
  installed, applied at once: the DNS routes follow and the service restarts. Endings that would
  hide real websites are refused; ones a router may already answer need `--force`.
- **`dotlane install --tld <ending>`** makes an ending the primary before installing.
- **`doctor`** warns about a risky ending and about a route left for an ending no longer
  configured.
- **`primary_tld` in `GET /v1/status`**: the first of the configured endings, whose URL comes
  first in each site's `urls`.

### Changed

- **The default domain ending is now `.dotlane`**: `https://myapp.dotlane`.

### Fixed

- **`uninstall` on macOS leaving `/etc/resolver` files behind** for endings removed from
  `config.toml` after installing: it now removes every resolver file of Dotlane's, configured or
  not. Linux and Windows already removed all of theirs.

