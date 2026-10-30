#!/usr/bin/env bash
# Installs GoReleaser and Syft from their prebuilt Linux x86_64 releases, for CI.
#
#   scripts/install-release-tools.sh <dir>     # binaries go to <dir>; on Actions, also on PATH
#
# Each archive must match the SHA-256 pinned below (taken from the release's own checksums file
# and checked on download, 2026-10-30), so a replaced or tampered asset fails the job. This
# replaced `go install` from source, which took over six minutes per run. To update a tool:
# change its version, download the new archive, and pin its checksum here.
set -euo pipefail

dir=${1:?usage: scripts/install-release-tools.sh <dir>}
mkdir -p "$dir"

GORELEASER_VERSION=2.19.0
GORELEASER_SHA256=ebc3194ad1d1b1787614405285a0039665cd4c5bd3305215a1b201600f90630b
SYFT_VERSION=1.55.0
SYFT_SHA256=bb65249542db5fedeeb4d3f9b018b75e7264dbfbbb10486f044581a92f90490d

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi; }

fetch() { # <url> <sha256> <binary>
  local url=$1 want=$2 bin=$3 tmp
  tmp=$(mktemp -d)
  curl -fsSL --retry 3 -o "$tmp/archive.tar.gz" "$url"
  local got
  got=$(sha256 "$tmp/archive.tar.gz" | cut -d' ' -f1)
  if [[ "$got" != "$want" ]]; then
    echo "install-release-tools: checksum mismatch for $url" >&2
    echo "  want $want" >&2
    echo "  got  $got" >&2
    exit 1
  fi
  tar -xzf "$tmp/archive.tar.gz" -C "$tmp" "$bin"
  install -m 0755 "$tmp/$bin" "$dir/$bin"
  rm -rf "$tmp"
  echo "installed $bin from $url (sha256 ok)"
}

fetch "https://github.com/goreleaser/goreleaser/releases/download/v${GORELEASER_VERSION}/goreleaser_Linux_x86_64.tar.gz" \
  "$GORELEASER_SHA256" goreleaser
fetch "https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/syft_${SYFT_VERSION}_linux_amd64.tar.gz" \
  "$SYFT_SHA256" syft

if [[ -n "${GITHUB_PATH:-}" ]]; then
  echo "$dir" >> "$GITHUB_PATH"
fi
