#!/usr/bin/env bash
# End-to-end test on a real Linux machine: install → add → HTTPS through the system's DNS and trust →
# up/down → doctor → uninstall --purge, then check nothing is left. It changes the system
# (a systemd user unit, a DNS routing unit and its network link, /etc/sysctl.d, the system trust
# store), so it runs on GitHub's throwaway ubuntu runners, where sudo needs no password, and in
# throwaway machines (OrbStack). Needs systemd-resolved as the system's resolver, curl, python3 and
# iproute2; libnss3-tools (certutil) adds the browsers' trust to the test. Not for a development
# machine: it uses the default ports and service name, and would collide with a real install.
set -euo pipefail

TLD=dltest
SERVICE=dev.dotlane.ci
WORK=$(mktemp -d)
CONFIG=$WORK/config.toml
# DOTLANE_BIN tests an installed binary (a deb, rpm or Arch package's /usr/bin/dotlane) instead of a
# build from this checkout; the steps that rebuild it to test upgrades are then skipped.
BIN=${DOTLANE_BIN:-$PWD/bin/dotlane}
DL() { timeout 120 "$BIN" --config "$CONFIG" "$@"; }
step() { printf '\n== %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# systemctl --user needs the user's manager and its bus. A CI job or an su shell has neither in its
# environment, and the manager itself only runs while the user is logged in or lingers.
sudo loginctl enable-linger "$(id -un)"
export XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}
export DBUS_SESSION_BUS_ADDRESS=${DBUS_SESSION_BUS_ADDRESS:-unix:path=$XDG_RUNTIME_DIR/bus}
for _ in $(seq 1 50); do
  systemctl --user show-environment >/dev/null 2>&1 && break
  sleep 0.2
done
systemctl --user show-environment >/dev/null || fail "no systemd user manager (XDG_RUNTIME_DIR=$XDG_RUNTIME_DIR)"

UNIT=$SERVICE.service
SERVICE_BIN=${XDG_DATA_HOME:-$HOME/.local/share}/dotlane/bin/$SERVICE
UNIT_FILE=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/$UNIT
DNS_UNIT=dotlane-dns-$SERVICE.service
NICK="Dotlane Local Authority ($SERVICE)"

cat > "$CONFIG" <<TOML
tld = "$TLD"
service_name = "$SERVICE"
state_dir = "$WORK/state"
TOML

[[ -n ${DOTLANE_BIN:-} ]] || go build -ldflags "-X main.version=ci-build-1" -o "$BIN" ./cmd/dotlane
echo hello > "$WORK/hello.txt"
python3 -m http.server 18080 --bind 127.0.0.1 --directory "$WORK" >"$WORK/upstream.log" 2>&1 &
UPSTREAM=$!
# The test's dev server must answer before Dotlane is asked to proxy to it: Python can take
# seconds to start on a cold runner, and a 502 then would be the test's fault, not Dotlane's.
for _ in $(seq 1 60); do
  curl -fsS --max-time 1 http://127.0.0.1:18080/hello.txt >/dev/null 2>&1 && break
  sleep 0.5
done
curl -fsS --max-time 2 http://127.0.0.1:18080/hello.txt >/dev/null || { cat "$WORK/upstream.log"; fail "the test's dev server on :18080 did not start"; }

# Why a request through Dotlane failed: a certificate problem, or a proxy answer (502) and what
# is behind it.
why() { # <url>
  local code
  code=$(curl -sS -o "$WORK/why.html" -w '%{http_code}' --max-time 10 "$1" 2>"$WORK/why.err") || true
  echo "--- $1: HTTP ${code:-none}"
  cat "$WORK/why.err"
  [[ -s "$WORK/why.html" ]] && sed -e '/<style/,/<\/style>/d' -e 's/<[^>]*>//g' "$WORK/why.html" | grep -v '^[[:space:]]*$' | head -8
  echo "--- listening on :18080:"; ss -lntp 'sport = :18080' || true
  echo "--- direct: $(curl -sS -o /dev/null -w '%{http_code}' --max-time 2 http://127.0.0.1:18080/hello.txt 2>&1)"
  echo "--- resolver:"; resolvectl status | head -30 || true
}
cleanup() {
  kill "$UPSTREAM" 2>/dev/null || true
  [[ -n "${DONE:-}" ]] || {
    echo "--- daemon log"; journalctl --user -u "$UNIT" --no-pager -n 50 2>/dev/null || true
    dns_diagnostics
  }
}
# What the system's resolver and network manager think of Dotlane's link, for a failure that
# only shows on a machine we cannot log in to (a CI runner).
dns_diagnostics() {
  local l=${link:-}
  [[ -n "$l" ]] || l=$(ip -o link show 2>/dev/null | sed -n 's/^[0-9]*: \(dl-[0-9a-f]*\)[:@].*/\1/p' | head -1)
  echo "--- systemd: $(systemctl --version | head -1); resolved $(systemctl is-active systemd-resolved 2>&1); networkd $(systemctl is-active systemd-networkd 2>&1); NetworkManager $(systemctl is-active NetworkManager 2>&1)"
  echo "--- resolvectl status ${l:-(all links)}"; resolvectl status ${l:+"$l"} 2>&1 | head -60 || true
  echo "--- resolvectl default-route ${l}"; [[ -z "$l" ]] || resolvectl default-route "$l" 2>&1 || true
  echo "--- resolvectl domain"; resolvectl domain 2>&1 || true
  echo "--- resolvectl dns"; resolvectl dns 2>&1 || true
  if command -v networkctl >/dev/null; then
    echo "--- networkctl list"; networkctl list --no-pager 2>&1 || true
    [[ -z "$l" ]] || { echo "--- networkctl status $l"; networkctl status "$l" --no-pager 2>&1 | head -30 || true; }
  fi
  echo "--- ip -d link show ${l}"; [[ -z "$l" ]] || ip -d link show "$l" 2>&1 || true
  echo "--- DNS unit"; systemctl status "$DNS_UNIT" --no-pager 2>&1 | head -20 || true
  echo "--- DNS unit journal"; journalctl -u "$DNS_UNIT" --no-pager -n 40 2>&1 || true
  echo "--- resolved and networkd journal for the link"
  journalctl -u systemd-resolved -u systemd-networkd --no-pager -n 400 2>&1 | grep -i -e "${l:-dl-}" -e "default" | tail -30 || true
}
trap cleanup EXIT

# The lowest unprivileged port before install: 1024 on a stock kernel, 0 in a Docker container.
# Install lowers it only when it is above 80, and uninstall must leave it as it found it.
PORT_START=$(cat /proc/sys/net/ipv4/ip_unprivileged_port_start)

step "install"
start=$(date +%s)
DL install

step "first site"
DL add web 18080
# No --cacert: the system's trust store must vouch for the certificate, and the system's resolver
# (through systemd-resolved) must find the name.
body=$(curl -fsS --max-time 10 "https://web.$TLD/hello.txt") || { why "https://web.$TLD/hello.txt"; fail "https://web.$TLD/hello.txt was not served"; }
[[ "$body" == hello ]] || fail "unexpected body: $body"
elapsed=$(( $(date +%s) - start ))
echo "install → first site: ${elapsed}s"
(( elapsed < 60 )) || fail "install → first site took ${elapsed}s (budget 60s)"

step "other lookups are left alone"
# The DNS setting must route *.$TLD only: it sits on a link of its own that is not a default route.
link=$(resolvectl domain | sed -n "s/^Link [0-9]* (\(.*\)): .*~$TLD.*/\1/p")
[[ -n "$link" ]] || { resolvectl domain; fail "no link routes ~$TLD"; }
# `resolvectl default-route` prints the setting the same way on every systemd version; `status`
# does not (255 shows it as "-DefaultRoute" among the protocols, 259 as "Default Route: no").
[[ "$(resolvectl default-route "$link")" == *": no" ]] || fail "the $link link is a default route"
resolvectl domain | grep -v "(${link}):" | grep -q "~$TLD" && fail "~$TLD is routed on another link too"
resolvectl query example.com >/dev/null || fail "resolvectl query example.com stopped working"
getent hosts example.com >/dev/null || fail "getent hosts example.com stopped working"
if command -v dig >/dev/null; then
  [[ -n "$(dig +short +time=3 example.com | head -1)" ]] || fail "dig example.com returned nothing"
fi
echo "example.com resolves; $TLD is routed on $link only"

step "browsers' trust"
if command -v certutil >/dev/null; then
  certutil -L -d "sql:$HOME/.pki/nssdb" -n "$NICK" >/dev/null || fail "the CA is not in ~/.pki/nssdb"
  echo "CA is in ~/.pki/nssdb"
else
  echo "certutil not installed; skipped"
fi

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
# On Linux the proxy listens on loopback only, so another address of this machine gets no answer.
lan=$(ip -4 -o addr show scope global | awk '{print $4}' | cut -d/ -f1 | head -1 || true)
if [[ -n "$lan" ]]; then
  if curl -sS -o /dev/null --max-time 5 --resolve "web.$TLD:443:$lan" "https://web.$TLD/" 2>/dev/null; then
    fail "a request to $lan was answered"
  fi
  echo "request to $lan got no answer"
else
  echo "no global address; skipped"
fi

step "project file"
mkdir -p "$WORK/project/sub"
printf '[sites.proj]\nport = 18080\n' > "$WORK/project/dotlane.toml"
(cd "$WORK/project/sub" && DL up)
curl -fsS "https://proj.$TLD/hello.txt" >/dev/null || fail "dotlane up did not serve proj"
(cd "$WORK/project" && DL down)
DL ls --json | grep -q '"proj"' && fail "dotlane down left proj"

if [[ -n ${DOTLANE_BIN:-} ]]; then
  step "upgrades: skipped, $BIN is an installed package"
else
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
  [[ "$(sha256sum < "$BIN")" == "$(sha256sum < "$SERVICE_BIN")" ]] || fail "the service's copy is not the new build"
  body=$(curl -fsS --max-time 10 "https://web.$TLD/hello.txt") || { why "https://web.$TLD/hello.txt"; fail "web.$TLD not served after the reinstall"; }
  [[ "$body" == hello ]] || fail "unexpected body after the reinstall: $body"
  DL ls --json | grep -q '"web"' || fail "site web lost in the reinstall"
  out=$(DL install 2>&1) || { echo "$out"; fail "second install failed"; }
  echo "$out" | grep -q "restarted" && { echo "$out"; fail "an unchanged binary restarted the daemon"; }
  grep -qE '\$ sudo' <<<"$out" && { echo "$out"; fail "an unchanged install ran sudo"; }
  echo "restarted into the new build (pid $pid_before → $pid_after); an unchanged install left it alone"

  step "install --refresh after an upgrade"
  # What a user runs after an upgrade (and what the next add, rm, ls, up or down does on its own
  # for a newer release): no password, just the new build.
  before=$(daemon)
  go build -ldflags "-X main.version=ci-build-3" -o "$BIN" ./cmd/dotlane
  out=$(DL install --refresh 2>&1) || { echo "$out"; fail "install --refresh failed"; }
  echo "$out"
  grep -qE 'needs your password|\$ sudo' <<<"$out" && fail "install --refresh asked for a password"
  grep -q "restarted (to load the rebuilt binary)" <<<"$out" || fail "install --refresh did not restart the daemon"
  after=$(daemon)
  [[ "$after" == *"ci-build-3"* ]] || fail "the daemon does not run the refreshed build: $after"
  [[ "$(sed -E 's/.*pid ([0-9]+).*/\1/' <<<"$before")" != "$(sed -E 's/.*pid ([0-9]+).*/\1/' <<<"$after")" ]] || fail "daemon pid unchanged after refresh"
  curl -fsS --max-time 10 "https://web.$TLD/hello.txt" >/dev/null || fail "web.$TLD not served after the refresh"
fi

step "domain endings"
# A second ending answers at once, can become the primary, and the first can go: the DNS unit's
# link routes the new list, and the sites stay, on the new ending.
TLD2=dltest2
DL tld add "$TLD2"
resolvectl domain "$link" | grep -q "~$TLD2" || { resolvectl domain; fail "~$TLD2 not routed on $link after tld add"; }
body=$(curl -fsS --max-time 10 "https://web.$TLD2/hello.txt") || { why "https://web.$TLD2/hello.txt"; fail "web.$TLD2 not served after tld add"; }
[[ "$body" == hello ]] || fail "unexpected body on .$TLD2: $body"
DL tld use "$TLD2"
DL ls --json | python3 -c 'import json,sys; u=next(s["urls"] for s in json.load(sys.stdin) if s["name"]=="web"); sys.exit(0 if u[0].startswith("https://web.'"$TLD2"'") else "primary URL is " + u[0])'
DL tld rm "$TLD"
resolvectl domain | grep -q "~$TLD\b" && { resolvectl domain; fail "~$TLD still routed after tld rm"; }
[[ "$(resolvectl default-route "$link")" == *": no" ]] || fail "the $link link became a default route"
curl -fsS --max-time 10 "https://web.$TLD2/hello.txt" >/dev/null || fail "web.$TLD2 not served after removing .$TLD"
# Back to the first ending, so the steps below test what they always did.
DL tld use "$TLD"
DL tld rm "$TLD2"
resolvectl domain | grep -q "~$TLD2" && fail "~$TLD2 still routed after tld rm"
curl -fsS --max-time 10 "https://web.$TLD/hello.txt" >/dev/null || fail "web.$TLD not served after switching back"
resolvectl query example.com >/dev/null || fail "resolvectl query example.com broke"
echo "added, served, made primary, removed, and back"

step "doctor"
DL doctor

step "uninstall --purge"
out=$(DL uninstall --purge 2>&1) || { echo "$out"; fail "uninstall failed"; }
echo "$out"
[[ ! -e "$UNIT_FILE" ]] || fail "login service left behind"
[[ ! -e "$SERVICE_BIN" ]] || fail "the service's binary left behind"
[[ ! -e "$WORK/state" ]] || fail "state left behind"
systemctl --user is-enabled "$UNIT" >/dev/null 2>&1 && fail "service still enabled"
systemctl --user is-active "$UNIT" >/dev/null 2>&1 && fail "service still running"
[[ ! -e /etc/systemd/system/$DNS_UNIT ]] || fail "the DNS unit left behind"
ip link show "$link" >/dev/null 2>&1 && fail "the $link link left behind"
resolvectl domain | grep -q "~$TLD" && fail "~$TLD still routed"
[[ ! -e /etc/sysctl.d/50-dotlane-$SERVICE.conf ]] || fail "/etc/sysctl.d/50-dotlane-$SERVICE.conf left behind"
[[ "$(cat /proc/sys/net/ipv4/ip_unprivileged_port_start)" == "$PORT_START" ]] || fail "ip_unprivileged_port_start was not restored to $PORT_START"
ls /usr/local/share/ca-certificates/dotlane-$SERVICE.crt /etc/ca-certificates/trust-source/anchors/dotlane-$SERVICE.crt \
   /etc/pki/ca-trust/source/anchors/dotlane-$SERVICE.crt >/dev/null 2>&1 && fail "the CA file is left in the system's store"
if command -v certutil >/dev/null; then
  certutil -L -d "sql:$HOME/.pki/nssdb" -n "$NICK" >/dev/null 2>&1 && fail "CA still in ~/.pki/nssdb"
fi
curl -sS -o /dev/null --max-time 5 "https://web.$TLD/" 2>/dev/null && fail "web.$TLD is still served"
resolvectl query example.com >/dev/null || fail "resolvectl query example.com broke after uninstall"

step "install --refresh with nothing installed"
out=$(DL install --refresh 2>&1) || { echo "$out"; fail "install --refresh failed when not installed"; }
grep -q "nothing to refresh" <<<"$out" || { echo "$out"; fail "install --refresh did not say there was nothing to do"; }
[[ ! -e "$UNIT_FILE" ]] || fail "install --refresh installed a service"
[[ ! -e "$SERVICE_BIN" ]] || fail "install --refresh copied the binary"
echo "no-op, as expected"

DONE=1
step "passed (install → first site: ${elapsed}s)"
