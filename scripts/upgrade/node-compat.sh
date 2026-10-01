#!/usr/bin/env bash
# A managing panel and a node of different releases still work together:
# this build managing a node of OLD, and a panel of OLD managing this build.
#
# Usage:
#   scripts/upgrade/node-compat.sh            OLD is v2.5.2
#   scripts/upgrade/node-compat.sh v2.4.1
#
#   DIRECTIONS=both        this build managing OLD, and OLD managing this build
#   DIRECTIONS=old-panel   only OLD managing this build
#   DIRECTIONS=new-panel   only this build managing OLD
#
# Both run on this machine as two panels on two ports: the panel-to-node
# protocol is HTTP between two W-UI panels, and that is what is tested.
# Tunnels need a kernel and are not brought up; the node's database holding
# the tunnel and the customer the panel sent it is the check.
#
# On Linux a panel refuses to start beside another in the same network
# namespace -- the guard that keeps two panels off one server -- so there the
# node runs in a namespace of its own, reached over a veth pair. That takes
# sudo; the panels themselves still run as the calling user.
set -euo pipefail

OLD="${1:-v2.5.2}"
DIRECTIONS="${DIRECTIONS:-both}"
ROOT="$(git rev-parse --show-toplevel)"
EXE=""
[[ "$(go env GOOS)" == windows ]] && EXE=.exe
USER_=ci
PASS=ci-node-pass-1

work="$(mktemp -d)"
pids=()
NS=""
NODE_IP=127.0.0.1
# stop ends every panel started so far, so the next pair starts on its own.
stop() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; wait "$p" 2>/dev/null || true; done
  pids=()
  if [[ -n "$NS" ]]; then
    sudo ip netns pids "$NS" 2>/dev/null | xargs -r sudo kill 2>/dev/null || true
  fi
}
cleanup() {
  stop
  [[ -z "$NS" ]] || sudo ip netns del "$NS" 2>/dev/null || true
  git -C "$ROOT" worktree remove --force "$work/src" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

echo "building this build, $OLD and the tool"
(cd "$ROOT" && CGO_ENABLED=0 go build -ldflags "-X main.version=0.0.0-this" -o "$work/wui-new$EXE" ./cmd/wui)
(cd "$ROOT" && go build -o "$work/upgrade$EXE" ./tools/upgrade)
git -C "$ROOT" worktree add --detach "$work/src" "$OLD" >/dev/null 2>&1
(cd "$work/src" && CGO_ENABLED=0 go build -ldflags "-X main.version=${OLD#v}" -o "$work/wui-old$EXE" ./cmd/wui)

if [[ "$(uname -s)" == Linux ]]; then
  sudo -n true 2>/dev/null || { echo "on Linux the node needs a network namespace of its own, and that needs sudo" >&2; exit 1; }
  NS="wuinode$$"
  NODE_IP=10.98.0.2
  sudo ip netns add "$NS"
  sudo ip link add "wn$$a" type veth peer name "wn$$b"
  sudo ip link set "wn$$b" netns "$NS"
  sudo ip addr add 10.98.0.1/24 dev "wn$$a"
  sudo ip link set "wn$$a" up
  sudo ip -n "$NS" addr add "$NODE_IP/24" dev "wn$$b"
  sudo ip -n "$NS" link set "wn$$b" up
  sudo ip -n "$NS" link set lo up
fi

# run NAME BINARY HOST PORT [NETNS] starts a panel of its own, in NETNS when
# one is given.
run() {
  local name="$1" bin="$2" host="$3" port="$4" ns="${5:-}" data="$work/data-$1"
  mkdir -p "$data/backups"
  local env_=(WUI_DATA_DIR="$data" WUI_BACKUP_DIR="$data/backups" WUI_LISTEN="$host:$port" WUI_LOG_LEVEL=warn)
  env "${env_[@]}" "$bin" admin reset --username "$USER_" --password "$PASS" >/dev/null
  if [[ -n "$ns" ]]; then
    sudo ip netns exec "$ns" sudo -u "$(id -un)" env "${env_[@]}" "$bin" > "$work/$name.log" 2>&1 &
  else
    env "${env_[@]}" "$bin" > "$work/$name.log" 2>&1 &
  fi
  pids+=($!)
}

pair() {
  local label="$1" panel_bin="$2" node_bin="$3" base="$4"
  echo "== $label"
  run "panel-$base" "$panel_bin" 127.0.0.1 "$base"
  run "node-$base" "$node_bin" "$NODE_IP" $((base + 1)) "$NS"
  if ! "$work/upgrade$EXE" nodes -panel "http://127.0.0.1:$base/" -node "http://$NODE_IP:$((base + 1))/" \
      -user "$USER_" -pass "$PASS"; then
    echo "--- panel:"; grep -v "only available on Linux\|Linux-only\|port 53\|plain HTTP" "$work/panel-$base.log" | tail -25
    echo "--- node:"; grep -v "only available on Linux\|Linux-only\|port 53\|plain HTTP" "$work/node-$base.log" | tail -25
    return 1
  fi
  stop
}

if [[ "$DIRECTIONS" != old-panel ]]; then
  pair "this build managing a node of $OLD" "$work/wui-new$EXE" "$work/wui-old$EXE" 47300
fi
if [[ "$DIRECTIONS" != new-panel ]]; then
  pair "a panel of $OLD managing this build" "$work/wui-old$EXE" "$work/wui-new$EXE" 47310
fi
echo "node compatibility with $OLD ($DIRECTIONS): works"
