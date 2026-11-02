#!/usr/bin/env bash
# Runs scripts/integration-linux.sh on another distribution, inside a throwaway container that
# boots systemd. GitHub only hosts Ubuntu runners, and the test needs what a plain container lacks:
# systemd-resolved as the resolver, a user manager (systemctl --user), a network link it can create
# and its own sysctls. A privileged container with systemd as PID 1, in its own cgroup and network
# namespaces, has all of them; the runner itself is not changed.
#
#   scripts/integration-linux-container.sh fedora:44
#   ARCH_SNAPSHOT=2026/10/25 scripts/integration-linux-container.sh archlinux@sha256:<digest>
#
# ARCH_SNAPSHOT installs Arch's packages from that day's Arch Linux Archive instead of the live
# mirrors, so a pinned image also gets pinned packages: the job changes only when the pin does.
#
#   PACKAGE=dist/dotlane_<version>_linux_amd64.deb scripts/integration-linux-container.sh ubuntu:24.04
#
# PACKAGE tests a release package instead of a build from this checkout: the container installs the
# .deb, .rpm or .pkg.tar.zst with its own package manager, the test runs /usr/bin/dotlane (and skips
# the steps that rebuild it), then the package is removed and must take /usr/bin/dotlane with it.
#
# Ubuntu and Debian (apt), Fedora (dnf) and Arch (pacman) are supported. The repository is mounted at
# the same path and the test runs as a user with this machine's UID, so its build lands in bin/ as
# usual; Go comes from this machine (go env GOROOT), read-only. From a Mac, point LINUX_GOROOT at an
# unpacked Linux Go for the container's CPU.
set -euo pipefail

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

IMAGE=${1:?usage: $0 <image>}
NAME=dotlane-e2e-$$
TAG=dotlane-e2e:$$
REPO=$PWD
GOROOT_HOST=${LINUX_GOROOT:-$(go env GOROOT)}
GOMODCACHE_HOST=$(go env GOMODCACHE)
mkdir -p "$GOMODCACHE_HOST"
PACKAGE=${PACKAGE:-}
if [[ -n $PACKAGE ]]; then
  [[ -f $PACKAGE ]] || fail "no package at $PACKAGE"
  PACKAGE=$(cd "$(dirname "$PACKAGE")" && pwd)/$(basename "$PACKAGE")
fi

step() { printf '\n== %s\n' "$*"; }
ctr() { docker exec "$NAME" "$@"; }

cleanup() {
  local status=$?
  if (( status != 0 )) && docker inspect "$NAME" >/dev/null 2>&1; then
    echo "--- the container's journal"
    ctr journalctl -b --no-pager 2>/dev/null | tail -n 80 || true
  fi
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker rmi -f "$TAG" >/dev/null 2>&1 || true
  rm -f "${build_log:-}"
}
trap cleanup EXIT

# The DNS unit adds a dummy link. The kernel loads that driver on first use, but not always on behalf
# of a container, so load it here when we can (CI's sudo needs no password).
sudo -n modprobe dummy 2>/dev/null || true

step "image: $IMAGE"
# systemd, the tools the test uses, and a user with sudo and this machine's UID. Arch is rolling,
# so -Syu brings the image to the packages of the repositories it installs from (the archive's day,
# with ARCH_SNAPSHOT); pacman's download sandbox is off, since it needs seccomp filters a container
# build may refuse ("error restricting syscalls via seccomp"), and the image is thrown away after.
build_log=$(mktemp)
docker build --progress=plain -t "$TAG" --build-arg BASE="$IMAGE" --build-arg UID="$(id -u)" \
  --build-arg ARCH_SNAPSHOT="${ARCH_SNAPSHOT:-}" - <<'DOCKERFILE' >"$build_log" 2>&1 || { cat "$build_log"; fail "the image did not build"; }
ARG BASE
FROM ${BASE}
ARG UID
ARG ARCH_SNAPSHOT
RUN set -eux; \
    if command -v apt-get >/dev/null; then \
      export DEBIAN_FRONTEND=noninteractive; \
      apt-get update; \
      apt-get install -y --no-install-recommends systemd systemd-sysv systemd-resolved dbus-user-session libpam-systemd \
        sudo curl ca-certificates python3 iproute2 libnss3-tools procps findutils; \
      rm -rf /var/lib/apt/lists/*; \
    elif command -v dnf >/dev/null; then \
      dnf -y install systemd systemd-resolved sudo curl python3 iproute nss-tools procps-ng findutils which; \
      dnf clean all; \
    elif command -v pacman >/dev/null; then \
      if [ -n "$ARCH_SNAPSHOT" ]; then \
        echo "Server = https://archive.archlinux.org/repos/$ARCH_SNAPSHOT/\$repo/os/\$arch" > /etc/pacman.d/mirrorlist; \
      fi; \
      sed -i '/^\[options\]/a DisableSandbox' /etc/pacman.conf; \
      pacman -Syu --noconfirm --needed systemd sudo curl python iproute2 nss procps-ng findutils which; \
      pacman -Scc --noconfirm; \
    else \
      echo "unsupported distribution" >&2; exit 1; \
    fi; \
    if getent passwd "$UID" >/dev/null; then userdel -r "$(getent passwd "$UID" | cut -d: -f1)"; fi; \
    useradd -m -u "$UID" -s /bin/bash runner; \
    echo 'runner ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/runner; \
    systemctl enable systemd-resolved
STOPSIGNAL SIGRTMIN+3
CMD ["/usr/lib/systemd/systemd"]
DOCKERFILE

step "boot systemd"
# Docker opens every port to normal users in a container (ip_unprivileged_port_start=0); a stock
# kernel starts at 1024, which is what install's sysctl step is for, so start from that.
docker run -d --name "$NAME" --privileged --cgroupns=private \
  --sysctl net.ipv4.ip_unprivileged_port_start=1024 \
  --tmpfs /run --tmpfs /run/lock --tmpfs /tmp:exec \
  -v "$REPO:$REPO" -v "$GOROOT_HOST:/usr/local/go:ro" -v "$GOMODCACHE_HOST:/home/runner/go/pkg/mod" \
  ${PACKAGE:+-v "$PACKAGE:/pkg/$(basename "$PACKAGE"):ro"} \
  "$TAG" >/dev/null
# is-system-running --wait needs systemd's bus, which is not up the moment the container starts.
state=
for _ in $(seq 1 120); do
  state=$(ctr systemctl is-system-running 2>/dev/null || true)
  [[ $state == running || $state == degraded ]] && break
  sleep 0.5
done
echo "system: $state"
[[ $state == running || $state == degraded ]] || fail "systemd did not finish booting ($state)"
[[ $state == running ]] || ctr systemctl --failed --no-pager || true
ctr sh -c '. /etc/os-release && echo "$PRETTY_NAME"; systemctl --version | head -1'

step "systemd-resolved as the resolver"
# Docker bind-mounts its own resolv.conf. Keep its upstream servers for resolved (falling back to
# public ones when Docker hands out a loopback address), then point programs at resolved's stub,
# as a desktop Ubuntu, or a Fedora or Arch with resolved enabled, does (docs/setup-linux.md, Requirements).
ctr sh -c '
  set -e
  upstream=$(awk "/^nameserver/ && \$2 !~ /^127\\./ {print \$2}" /etc/resolv.conf | tr "\n" " ")
  [ -n "$upstream" ] || upstream="1.1.1.1 8.8.8.8"
  umount /etc/resolv.conf
  mkdir -p /etc/systemd/resolved.conf.d
  printf "[Resolve]\nDNS=%s\n" "$upstream" > /etc/systemd/resolved.conf.d/upstream.conf
  ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
  systemctl restart systemd-resolved
  resolvectl dns
  getent hosts example.com >/dev/null'

env=()
if [[ -n $PACKAGE ]]; then
  pkg=/pkg/$(basename "$PACKAGE")
  step "install $(basename "$PACKAGE")"
  case $pkg in
    *.deb) ctr sh -c "DEBIAN_FRONTEND=noninteractive apt-get install -y $pkg" ;;
    *.rpm) ctr dnf install -y "$pkg" ;;
    *.pkg.tar.zst) ctr pacman -U --noconfirm "$pkg" ;;
    *) fail "not a .deb, .rpm or .pkg.tar.zst: $pkg" ;;
  esac
  # By path and by owner, not `command -v`: on Fedora /usr/sbin is the same folder as /usr/bin and
  # comes first on root's PATH.
  ctr test -x /usr/bin/dotlane || fail "the package did not put dotlane in /usr/bin"
  case $pkg in
    *.deb) ctr dpkg -S /usr/bin/dotlane ;;
    *.rpm) ctr rpm -qf /usr/bin/dotlane ;;
    *.pkg.tar.zst) ctr pacman -Qo /usr/bin/dotlane ;;
  esac
  ctr /usr/bin/dotlane version
  env=(-e DOTLANE_BIN=/usr/bin/dotlane)
fi

step "scripts/integration-linux.sh"
docker exec -u runner -w "$REPO" \
  -e HOME=/home/runner -e PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin \
  -e GOCACHE=/tmp/go-build -e GOFLAGS=-buildvcs=false -e GOTOOLCHAIN=local ${env[@]+"${env[@]}"} \
  "$NAME" scripts/integration-linux.sh

if [[ -n $PACKAGE ]]; then
  step "remove the package"
  # After dotlane uninstall --purge (the test's last step), removing the package must leave no
  # dotlane behind: the packages have no scripts, so this is the whole of what they own.
  case $pkg in
    *.deb) ctr sh -c 'DEBIAN_FRONTEND=noninteractive apt-get remove -y dotlane' ;;
    *.rpm) ctr dnf remove -y dotlane ;;
    *.pkg.tar.zst) ctr pacman -R --noconfirm dotlane ;;
  esac
  ctr test ! -e /usr/bin/dotlane || fail "/usr/bin/dotlane is still there after removing the package"
  echo "removed; /usr/bin/dotlane is gone"
fi
