#!/usr/bin/env bash
# End-to-end test of the npm packages on a Mac: builds the binary for this Mac and the other
# architecture, packs the three packages (scripts/npm-build.mjs), installs them into a scratch
# project from the tarballs, and runs `npx dotlane`. Nothing is installed system-wide and
# `dotlane install` is not run. Used by CI's macOS job; safe on a development Mac.
#
#   scripts/npm-e2e.sh
set -euo pipefail

cd "$(dirname "$0")/.."
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*"; }

case "$(uname -m)" in
  arm64) here=darwin-arm64 other=darwin-x64 ;;
  x86_64) here=darwin-x64 other=darwin-arm64 ;;
  *) fail "not a Mac architecture: $(uname -m)" ;;
esac
goarch() { [[ $1 == darwin-arm64 ]] && echo arm64 || echo amd64; }
VERSION=0.0.0-e2e.$(git rev-parse --short HEAD)

step "build and pack $VERSION"
for p in darwin-arm64 darwin-x64; do
  GOOS=darwin GOARCH=$(goarch $p) CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$WORK/bin-$p" ./cmd/dotlane
done
node scripts/npm-build.mjs --version "$VERSION" --darwin-arm64 "$WORK/bin-darwin-arm64" --darwin-x64 "$WORK/bin-darwin-x64" --out "$WORK/pkgs"
launcher=$WORK/pkgs/dotlane-$VERSION.tgz
platform=$WORK/pkgs/dotlane-$here-$VERSION.tgz

step "install from the tarballs, with this Mac's platform package ($here)"
mkdir "$WORK/app" && cd "$WORK/app"
npm init -y >/dev/null
# --omit=optional: the launcher's optional dependencies point at the registry, where this version
# does not exist; the platform package comes from its tarball instead.
npm install --no-audit --no-fund --omit=optional "$launcher" "$platform" >/dev/null
[[ -x node_modules/@dotlane/$here/bin/dotlane ]] || fail "the $here binary was not installed"
[[ ! -e node_modules/@dotlane/$other ]] || fail "the $other package was installed on a $here Mac"

step "npx dotlane"
out=$(npx --no-install dotlane version)
echo "$out"
[[ "$out" == "$VERSION" ]] || fail "dotlane version printed '$out', want $VERSION"
npx --no-install dotlane --help >/dev/null || fail "dotlane --help failed"
set +e
npx --no-install dotlane rm 2>/dev/null; code=$?
set -e
[[ $code == 1 ]] || fail "a usage error exited $code, want 1"
echo "version, help and exit codes pass through"

step "without the platform package"
cd "$WORK" && mkdir bare && cd bare
npm init -y >/dev/null
npm install --no-audit --no-fund --omit=optional "$launcher" >/dev/null
set +e
msg=$(npx --no-install dotlane version 2>&1); code=$?
set -e
echo "$msg"
[[ $code == 1 && "$msg" == *"npm install @dotlane/$here"* ]] || fail "missing platform package: exit $code, '$msg'"

step "passed"
