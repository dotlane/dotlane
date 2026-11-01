#!/usr/bin/env bash
# End-to-end test of the Homebrew cask on a real Mac, as a user would run it:
#
#   brew install dotlane/tap/dotlane → dotlane install → dotlane add → HTTPS
#   → brew upgrade → the next dotlane command restarts the service on the new version
#   → dotlane uninstall --purge → brew uninstall --zap → nothing left.
#
# The cask and archives come from a GoReleaser snapshot build (the release-snapshot job's dist/);
# a local tap serves them from disk, so the URLs are rewritten to file:// and the checksums stay
# GoReleaser's. The upgrade is a second build of this commit with a higher version. Like
# scripts/integration.sh it changes the system with sudo, so it runs only on CI's throwaway Macs.
#
#   scripts/brew-e2e.sh <dist>
set -euo pipefail

DIST=$(cd "$1" && pwd)
cd "$(dirname "$0")/.."
WORK=$(mktemp -d)
TAP=dotlane-e2e/tap
TLD=traix
SERVICE=dev.dotlane
export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_ENV_HINTS=1
step() { printf '\n== %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
# Two minutes per dotlane call, as in integration.sh: a prompt nobody can answer fails, named.
DL() { perl -e 'alarm 120; exec @ARGV or die "exec: $!"' dotlane "$@"; }
# The version alone: release builds add " (<commit>, <date>)".
installed() { dotlane version | cut -d' ' -f1; }
daemon() { DL doctor --json | python3 -c 'import json,sys; print(next(c["detail"] for c in json.load(sys.stdin) if c["name"] == "Daemon"))'; }

case "$(uname -m)" in
  arm64) arch=arm64 block=on_arm ;;
  x86_64) arch=amd64 block=on_intel ;;
esac
cask=$DIST/homebrew/Casks/dotlane.rb
[[ -f $cask ]] || fail "no cask at $cask (goreleaser release --snapshot writes it)"
V1=$(sed -nE 's/^  version "(.*)"/\1/p' "$cask")

# localcask copies a cask into the local tap with its URLs pointing at $DIST.
localcask() { # <cask>
  sed -E "s|url \"https://github.com/dotlane/dotlane/releases/download/[^/]+/|url \"file://$DIST/|" "$1" \
    > "$(brew --repository "$TAP")/Casks/dotlane.rb"
}

echo hello > "$WORK/hello.txt"
python3 -m http.server 18080 --bind 127.0.0.1 --directory "$WORK" >"$WORK/upstream.log" 2>&1 &
UPSTREAM=$!
for _ in $(seq 1 60); do curl -fsS --max-time 1 http://127.0.0.1:18080/hello.txt >/dev/null 2>&1 && break; sleep 0.5; done
cleanup() {
  kill "$UPSTREAM" 2>/dev/null || true
  [[ -n "${DONE:-}" ]] || { echo "--- daemon log"; cat "$HOME/Library/Logs/dotlane/$SERVICE.log" 2>/dev/null || true; }
}
trap cleanup EXIT

step "brew install $TAP/dotlane ($V1)"
brew tap-new --no-git "$TAP" >/dev/null
mkdir -p "$(brew --repository "$TAP")/Casks"
localcask "$cask"
out=$(brew install --cask "$TAP/dotlane" 2>&1) || { echo "$out"; fail "brew install failed"; }
echo "$out"
grep -q "dotlane install" <<<"$out" || fail "the caveats were not shown"
[[ "$(command -v dotlane)" == "$(brew --prefix)/bin/dotlane" ]] || fail "dotlane on the PATH is $(command -v dotlane)"
[[ "$(installed)" == "$V1" ]] || fail "dotlane version printed $(dotlane version), want $V1"
xattr -p com.apple.quarantine "$(brew --prefix)/bin/dotlane" >/dev/null 2>&1 && fail "the binary is still quarantined"

step "dotlane install, then a site"
DL install
DL add web 18080
body=$(curl -fsS --max-time 10 "https://web.$TLD/hello.txt") || fail "https://web.$TLD was not served"
[[ "$body" == hello ]] || fail "unexpected body: $body"
before=$(daemon)
echo "daemon: $before"
[[ "$before" == *"$V1"* ]] || fail "the daemon does not run $V1"

step "brew upgrade"
V2=99.0.0-e2e
mkdir "$WORK/v2"
go build -trimpath -ldflags "-s -w -X main.version=$V2" -o "$WORK/v2/dotlane" ./cmd/dotlane
cp LICENSE NOTICE README.md "$WORK/v2/"
tar -C "$WORK/v2" -czf "$DIST/dotlane_${V2}_darwin_${arch}.tar.gz" dotlane LICENSE NOTICE README.md
sum=$(shasum -a 256 "$DIST/dotlane_${V2}_darwin_${arch}.tar.gz" | cut -d' ' -f1)
# The new version, and this architecture's checksum (the line after its on_arm/on_intel).
awk -v v="$V2" -v b="$block" -v s="$sum" '
  /^  version "/ { print "  version \"" v "\""; next }
  $1 == b { inblock = 1 }
  inblock && $1 == "sha256" { sub(/"[0-9a-f]+"/, "\"" s "\""); inblock = 0 }
  { print }' "$cask" > "$WORK/dotlane-v2.rb"
localcask "$WORK/dotlane-v2.rb"
brew upgrade --cask "$TAP/dotlane"
[[ "$(installed)" == "$V2" ]] || fail "after brew upgrade, dotlane version printed $(dotlane version)"
[[ "$(daemon)" == *"$V1"* ]] || fail "the daemon changed before any dotlane command ran"

step "the next command restarts the service on $V2"
out=$(DL ls 2>&1) || { echo "$out"; fail "dotlane ls failed"; }
echo "$out"
grep -q "Updating the Dotlane service" <<<"$out" || fail "dotlane ls did not update the service"
grep -qE 'needs your password|\$ sudo' <<<"$out" && fail "the update asked for a password"
after=$(daemon)
echo "daemon: $after"
[[ "$after" == *"$V2"* ]] || fail "the daemon does not run $V2"
body=$(curl -fsS --max-time 10 "https://web.$TLD/hello.txt") || fail "https://web.$TLD not served after the upgrade"
out=$(DL ls --json 2>&1)
grep -q "Updating" <<<"$out" && fail "the service was updated twice"
python3 -c 'import json,sys; json.loads(sys.argv[1])' "$out" || fail "ls --json is not JSON: $out"

step "dotlane uninstall --purge, then brew uninstall --zap"
DL uninstall --purge
brew uninstall --zap --cask "$TAP/dotlane"
[[ ! -e "$(brew --prefix)/bin/dotlane" ]] || fail "$(brew --prefix)/bin/dotlane left behind"
[[ ! -e "$(brew --prefix)/Caskroom/dotlane" ]] || fail "the Caskroom folder left behind"
for p in "Library/Application Support/dotlane" "Library/Logs/dotlane" ".config/dotlane" "Library/LaunchAgents/$SERVICE.plist"; do
  [[ ! -e "$HOME/$p" ]] || fail "~/$p left behind"
done
[[ ! -e /etc/resolver/$TLD ]] || fail "/etc/resolver/$TLD left behind"
launchctl print "gui/$(id -u)/$SERVICE" >/dev/null 2>&1 && fail "service still loaded"
security find-certificate -a -c "Dotlane Local Authority" -Z /Library/Keychains/System.keychain 2>/dev/null | grep -q "SHA-1" \
  && fail "CA still in the System keychain"
brew untap "$TAP" >/dev/null

DONE=1
step "passed"
