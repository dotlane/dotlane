#!/usr/bin/env bash
# End-to-end test on a real Mac: install → add → HTTPS through the system's DNS and trust →
# up/down → doctor → uninstall --purge, then check nothing is left. It changes the system
# (login service, System keychain, /etc/resolver), so it runs on GitHub's throwaway macOS runners,
# where sudo needs no password. Not for a development Mac: it uses the default ports and service
# name, and would collide with a real install.
set -euo pipefail

TLD=dltest
SERVICE=dev.dotlane.ci
WORK=$(mktemp -d)
CONFIG=$WORK/config.toml
BIN=$PWD/bin/dotlane
DL() { "$BIN" --config "$CONFIG" "$@"; }
step() { printf '\n== %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

cat > "$CONFIG" <<TOML
tld = "$TLD"
service_name = "$SERVICE"
state_dir = "$WORK/state"
TOML

go build -ldflags "-X main.version=ci-build-1" -o "$BIN" ./cmd/dotlane
python3 -m http.server 18080 --bind 127.0.0.1 --directory "$WORK" >/dev/null 2>&1 &
UPSTREAM=$!
echo hello > "$WORK/hello.txt"
cleanup() {
  kill "$UPSTREAM" 2>/dev/null || true
  [[ -n "${DONE:-}" ]] || { echo "--- daemon log"; cat "$HOME/Library/Logs/dotlane/$SERVICE.log" 2>/dev/null || true; }
}
trap cleanup EXIT

step "install"
start=$(date +%s)
DL install

step "first site"
DL add web 18080
body=$(curl -fsS --max-time 10 "https://web.$TLD/hello.txt") || fail "https://web.$TLD not served with a trusted certificate"
[[ "$body" == hello ]] || fail "unexpected body: $body"
elapsed=$(( $(date +%s) - start ))
echo "install → first site: ${elapsed}s"
(( elapsed < 60 )) || fail "install → first site took ${elapsed}s (budget 60s)"

step "HTTP redirect, unknown name, dev server down"
loc=$(curl -sS -o /dev/null -w '%{redirect_url}' "http://web.$TLD/x")
[[ "$loc" == "https://web.$TLD/x" ]] || fail "redirect went to $loc"
code=$(curl -sS -o /dev/null -w '%{http_code}' "https://nosuch.$TLD/")
[[ "$code" == 404 ]] || fail "unknown name answered $code, want the no-site page"
DL add down 18081
code=$(curl -sS -o /dev/null -w '%{http_code}' "https://down.$TLD/")
[[ "$code" == 502 ]] || fail "stopped dev server answered $code"
DL rm down

step "other machines are dropped"
lan=$(ipconfig getifaddr en0 || true)
if [[ -n "$lan" ]]; then
  if curl -sS -o /dev/null --max-time 5 --resolve "web.$TLD:443:$lan" "https://web.$TLD/" 2>/dev/null; then
    fail "a request to $lan was answered"
  fi
  echo "request to $lan got no answer"
else
  echo "no en0 address; skipped"
fi

step "project file"
mkdir -p "$WORK/project/sub"
printf '[sites.proj]\nport = 18080\n' > "$WORK/project/dotlane.toml"
(cd "$WORK/project/sub" && DL up)
curl -fsS "https://proj.$TLD/hello.txt" >/dev/null || fail "dotlane up did not serve proj"
(cd "$WORK/project" && DL down)
DL ls --json | grep -q '"proj"' && fail "dotlane down left proj"

step "reinstall over the running daemon"
# What an upgrade does: rebuild, then install again while the daemon runs. Install must copy the
# new build to the service's folder, restart the daemon into it, and keep every site.
daemon() { DL doctor --json | python3 -c 'import json,sys; print(next(c["detail"] for c in json.load(sys.stdin) if c["name"] == "Daemon"))'; }
before=$(daemon)
echo "before: $before"
go build -ldflags "-X main.version=ci-build-2" -o "$BIN" ./cmd/dotlane
out=$(DL install 2>&1) || { echo "$out"; fail "reinstall failed"; }
echo "$out" | grep -q "restarted (to load the rebuilt binary)" || { echo "$out"; fail "install did not restart the daemon"; }
after=$(daemon)
echo "after:  $after"
[[ "$after" == *"ci-build-2"* ]] || fail "the daemon does not run the new build"
pid_before=$(sed -E 's/.*pid ([0-9]+).*/\1/' <<<"$before"); pid_after=$(sed -E 's/.*pid ([0-9]+).*/\1/' <<<"$after")
[[ "$pid_before" != "$pid_after" ]] || fail "daemon pid unchanged ($pid_after)"
cmp -s "$BIN" "$HOME/Library/Application Support/dotlane/bin/$SERVICE" || fail "the service's copy is not the new build"
body=$(curl -fsS --max-time 10 "https://web.$TLD/hello.txt") || fail "web.$TLD not served after the reinstall"
[[ "$body" == hello ]] || fail "unexpected body after the reinstall: $body"
DL ls --json | grep -q '"web"' || fail "site web lost in the reinstall"
out=$(DL install 2>&1) || { echo "$out"; fail "second install failed"; }
echo "$out" | grep -q "restarted" && { echo "$out"; fail "an unchanged binary restarted the daemon"; }
echo "restarted into the new build (pid $pid_before → $pid_after); an unchanged install left it alone"

step "doctor"
DL doctor

step "uninstall --purge"
DL uninstall --purge
[[ ! -e /etc/resolver/$TLD ]] || fail "/etc/resolver/$TLD left behind"
[[ ! -e "$HOME/Library/LaunchAgents/$SERVICE.plist" ]] || fail "login service left behind"
[[ ! -e "$HOME/Library/Application Support/dotlane/bin/$SERVICE" ]] || fail "the service's binary left behind"
[[ ! -e "$WORK/state" ]] || fail "state left behind"
launchctl print "gui/$(id -u)/$SERVICE" >/dev/null 2>&1 && fail "service still loaded"
security find-certificate -a -c "Dotlane Local Authority" /Library/Keychains/System.keychain >/dev/null 2>&1 \
  && fail "CA still in the System keychain"

DONE=1
step "passed (install → first site: ${elapsed}s)"
