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
# Both run on this machine without root, as two panels on two ports: the
# panel-to-node protocol is HTTP between two W-UI panels, and that is what is
# tested. Tunnels need a kernel and are not brought up; the node's database
# holding the tunnel and the customer the panel sent it is the check.
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
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; wait "$p" 2>/dev/null || true; done
  git -C "$ROOT" worktree remove --force "$work/src" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

echo "building this build, $OLD and the tool"
(cd "$ROOT" && CGO_ENABLED=0 go build -ldflags "-X main.version=0.0.0-this" -o "$work/wui-new$EXE" ./cmd/wui)
(cd "$ROOT" && go build -o "$work/upgrade$EXE" ./tools/upgrade)
git -C "$ROOT" worktree add --detach "$work/src" "$OLD" >/dev/null 2>&1
(cd "$work/src" && CGO_ENABLED=0 go build -ldflags "-X main.version=${OLD#v}" -o "$work/wui-old$EXE" ./cmd/wui)

# run NAME BINARY PORT starts a panel of its own.
run() {
  local name="$1" bin="$2" port="$3" data="$work/data-$1"
  mkdir -p "$data/backups"
  local env_=(WUI_DATA_DIR="$data" WUI_BACKUP_DIR="$data/backups" WUI_LISTEN="127.0.0.1:$port" WUI_LOG_LEVEL=warn)
  env "${env_[@]}" "$bin" admin reset --username "$USER_" --password "$PASS" >/dev/null
  env "${env_[@]}" "$bin" > "$work/$name.log" 2>&1 &
  pids+=($!)
}

pair() {
  local label="$1" panel_bin="$2" node_bin="$3" base="$4"
  echo "== $label"
  run "panel-$base" "$panel_bin" "$base"
  run "node-$base" "$node_bin" $((base + 1))
  if ! "$work/upgrade$EXE" nodes -panel "http://127.0.0.1:$base/" -node "http://127.0.0.1:$((base + 1))/" \
      -user "$USER_" -pass "$PASS"; then
    echo "--- panel:"; grep -v "only available on Linux\|Linux-only\|port 53\|plain HTTP" "$work/panel-$base.log" | tail -25
    echo "--- node:"; grep -v "only available on Linux\|Linux-only\|port 53\|plain HTTP" "$work/node-$base.log" | tail -25
    return 1
  fi
}

if [[ "$DIRECTIONS" != old-panel ]]; then
  pair "this build managing a node of $OLD" "$work/wui-new$EXE" "$work/wui-old$EXE" 47300
fi
if [[ "$DIRECTIONS" != new-panel ]]; then
  pair "a panel of $OLD managing this build" "$work/wui-old$EXE" "$work/wui-new$EXE" 47310
fi
echo "node compatibility with $OLD ($DIRECTIONS): works"
