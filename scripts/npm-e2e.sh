#!/usr/bin/env bash
# End-to-end test of the npm packages on the machine it runs on (macOS, Linux, or Windows under
# Git Bash, which GitHub's Windows runners provide): builds the binary for every platform, packs
# the packages (scripts/npm-build.mjs, which needs all six), installs the launcher and this
# machine's platform package into a scratch project from the tarballs, and runs `npx dotlane`.
# Nothing is installed system-wide and `dotlane install` is not run. Used by CI's macOS, Linux and
# Windows jobs; safe on a development machine.
#
#   scripts/npm-e2e.sh
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$PWD
WORK=$(mktemp -d)
# Git Bash paths (/tmp/...) mean nothing to node, npm and go, which are Windows programs there;
# C:/... works for them and for bash.
if command -v cygpath >/dev/null; then WORK=$(cygpath -m "$WORK"); fi
# Leave the scratch project first: Windows cannot delete a directory that is some process's cwd.
# A folder that cannot be deleted must not fail a test that passed.
trap 'cd "$ROOT"; rm -rf "$WORK" || true' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*"; }

# This machine's package key, as npm and the launcher name it: ${process.platform}-${process.arch}.
case "$(go env GOOS)" in
  darwin) os=darwin ;;
  linux) os=linux ;;
  windows) os=win32 ;;
  *) fail "not a supported OS: $(go env GOOS)" ;;
esac
case "$(go env GOARCH)" in
  arm64) cpu=arm64 ;;
  amd64) cpu=x64 ;;
  *) fail "not a supported architecture: $(go env GOARCH)" ;;
esac
here=$os-$cpu
exe=dotlane
[[ $os == win32 ]] && exe=dotlane.exe
# A package key → Go's GOOS and GOARCH.
goos() { case $1 in win32-*) echo windows ;; *) echo "${1%%-*}" ;; esac; }
goarch() { [[ $1 == *-arm64 ]] && echo arm64 || echo amd64; }
VERSION=0.0.0-e2e.$(git rev-parse --short HEAD)

step "build and pack $VERSION (this machine: $here)"
platforms="darwin-arm64 darwin-x64 linux-arm64 linux-x64 win32-arm64 win32-x64"
flags=()
for p in $platforms; do
  GOOS=$(goos $p) GOARCH=$(goarch $p) CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$WORK/bin-$p" ./cmd/dotlane
  flags+=("--$p" "$WORK/bin-$p")
done
node scripts/npm-build.mjs --version "$VERSION" "${flags[@]}" --out "$WORK/pkgs"
launcher=$WORK/pkgs/dotlane-$VERSION.tgz
platform=$WORK/pkgs/dotlane-$here-$VERSION.tgz

step "install from the tarballs, with this machine's platform package ($here)"
mkdir "$WORK/app" && cd "$WORK/app"
npm init -y >/dev/null
# --omit=optional: the launcher's optional dependencies point at the registry, where this version
# does not exist; the platform package comes from its tarball instead.
npm install --no-audit --no-fund --omit=optional "$launcher" "$platform" >/dev/null
[[ -f node_modules/@dotlane/$here/bin/$exe ]] || fail "the $here binary (bin/$exe) was not installed"
[[ $os == win32 || -x node_modules/@dotlane/$here/bin/$exe ]] || fail "the $here binary is not executable"
installed=$(ls node_modules/@dotlane)
[[ "$installed" == "$here" ]] || fail "installed @dotlane packages: '$installed', want only $here"

step "npx dotlane"
# tr: a Windows .cmd shim may add carriage returns around the output.
out=$(npx --no-install dotlane version | tr -d '\r')
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
