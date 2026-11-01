#!/usr/bin/env bash
# The checks of CI's `test` job, plus `goreleaser check`, to run before pushing:
#
#   scripts/check.sh
#
# gofmt, build, go vet for Linux and macOS, go test -race, govulncheck for both, the Bash
# scripts' syntax, the npm tests and the GoReleaser config. CI's `test` job runs this script, so
# the two cannot drift apart. It changes nothing on the system: govulncheck and GoReleaser (the
# same checksum-pinned release CI uses, scripts/install-release-tools.sh) go to a cache folder,
# and nothing is installed or started. The macOS-only tests that need a Mac (`install-tests`),
# the integration tests and the snapshot build stay in CI.
set -euo pipefail
cd "$(dirname "$0")/.."

GOVULNCHECK_VERSION=v1.9.0
cache=${XDG_CACHE_HOME:-$HOME/.cache}/dotlane-check
# Keyed on the pinned versions, so changing a pin installs the new one.
tools=$cache/tools-$(cat scripts/install-release-tools.sh <(echo "$GOVULNCHECK_VERSION") | cksum | cut -d' ' -f1)

failed=()
step() { local name=$1; shift; printf '\n== %s\n' "$name"; "$@" || failed+=("$name"); }

gofmt_clean() { local out; out=$(gofmt -l .); [[ -z $out ]] || { echo "not gofmt'ed:"; echo "$out"; return 1; }; }
vet() { GOOS=$1 go vet ./...; }
vulncheck() { GOOS=$1 "$tools/govulncheck" ./...; }
scripts_parse() { local f; for f in files/bin/dotlane scripts/*.sh; do bash -n "$f" || return 1; done; }

if [[ ! -x $tools/govulncheck || ! -x $tools/goreleaser ]]; then
  printf '== tools (once, into %s)\n' "$tools"
  rm -rf "$cache" && mkdir -p "$tools"
  GOBIN=$tools go install "golang.org/x/vuln/cmd/govulncheck@$GOVULNCHECK_VERSION"
  scripts/install-release-tools.sh "$tools" >/dev/null
fi

step "gofmt" gofmt_clean
step "go build" go build ./...
step "go vet (Linux)" vet linux
step "go vet (macOS)" vet darwin
step "go test -race" go test -race ./...
step "govulncheck (Linux)" vulncheck linux
step "govulncheck (macOS)" vulncheck darwin
step "Bash scripts" scripts_parse
step "npm tests" node --test 'npm/test/*.test.*'
step "goreleaser check" "$tools/goreleaser" check

if (( ${#failed[@]} )); then
  printf '\nFAILED:\n'; printf '  %s\n' "${failed[@]}"
  exit 1
fi
printf '\nall checks passed\n'
