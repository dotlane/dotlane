#!/usr/bin/env bash
# Installs GoReleaser and Syft from their prebuilt releases, for CI (Linux x86_64) and
# scripts/check.sh (macOS).
#
#   scripts/install-release-tools.sh <dir>     # binaries go to <dir>; on Actions, also on PATH
#
# Each archive must match the SHA-256 pinned below (taken from the release's own checksums file
# and checked on download: Linux 2026-10-30, macOS 2026-10-31), so a replaced or tampered asset
# fails. This
# replaced `go install` from source, which took over six minutes per run. To update a tool:
# change its version, download the new archive, and pin its checksum here.
set -euo pipefail

dir=${1:?usage: scripts/install-release-tools.sh <dir>}
mkdir -p "$dir"

GORELEASER_VERSION=2.19.0
SYFT_VERSION=1.55.0
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64)
    GORELEASER_ASSET=goreleaser_Linux_x86_64.tar.gz
    GORELEASER_SHA256=ebc3194ad1d1b1787614405285a0039665cd4c5bd3305215a1b201600f90630b
    SYFT_ASSET=syft_${SYFT_VERSION}_linux_amd64.tar.gz
    SYFT_SHA256=bb65249542db5fedeeb4d3f9b018b75e7264dbfbbb10486f044581a92f90490d ;;
  Darwin/arm64)
    GORELEASER_ASSET=goreleaser_Darwin_all.tar.gz
    GORELEASER_SHA256=21adceff6e676ef63497fc129983f8c454e5a46ec0125b905cba0f83e7879722
    SYFT_ASSET=syft_${SYFT_VERSION}_darwin_arm64.tar.gz
    SYFT_SHA256=1217c258bd5eb91ef3189cf22a82e02f9cac07e4ce934350f428e93aff674625 ;;
  Darwin/x86_64)
    GORELEASER_ASSET=goreleaser_Darwin_all.tar.gz
    GORELEASER_SHA256=21adceff6e676ef63497fc129983f8c454e5a46ec0125b905cba0f83e7879722
    SYFT_ASSET=syft_${SYFT_VERSION}_darwin_amd64.tar.gz
    SYFT_SHA256=e2e5831cbabaec9a36fe65c34755c1113982c842a685edce3ce0b54a0ded8003 ;;
  *) echo "install-release-tools: no pinned release for $(uname -s)/$(uname -m)" >&2; exit 1 ;;
esac

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

fetch "https://github.com/goreleaser/goreleaser/releases/download/v${GORELEASER_VERSION}/${GORELEASER_ASSET}" \
  "$GORELEASER_SHA256" goreleaser
fetch "https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/${SYFT_ASSET}" \
  "$SYFT_SHA256" syft

if [[ -n "${GITHUB_PATH:-}" ]]; then
  echo "$dir" >> "$GITHUB_PATH"
fi
