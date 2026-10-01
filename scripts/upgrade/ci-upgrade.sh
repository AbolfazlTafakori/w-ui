#!/usr/bin/env bash
# Install an earlier release on this machine, put customers on it, connect one
# of them over a real WireGuard tunnel, update to this build -- the way named
# -- and check that nothing broke: the customers and their links, the
# settings, /etc/wui, the systemd units, the nftables names. The tunnel is
# pinged all through the update, and how long the customer was cut off is
# reported.
#
# Usage, as root, from the repository:
#   scripts/upgrade/ci-upgrade.sh installer   install.sh --local over the install
#   scripts/upgrade/ci-upgrade.sh panel       the panel's update button: the panel
#                                             fetches and checks the release, the
#                                             root helper (wui-update.path ->
#                                             wui apply-update) installs it
#   scripts/upgrade/ci-upgrade.sh menu        `w-ui update`, which runs update.sh
#
#   OLD=v2.5.2   the release to start from
#
# "installer" starts from the real release, downloaded from GitHub. "panel" and
# "menu" start from the same release built from its tag with a test signing
# key, and update to this build signed with it, served by a stand-in for
# GitHub (tools/upgrade fakegithub) that /etc/hosts points the GitHub names at:
# the update code under test is the code that ships, and nothing is signed
# with the project's own key.
#
# Ubuntu, with systemd, nftables and the WireGuard module: a CI runner, never a
# server anyone uses. It rewrites /etc/hosts and trusts a test authority.
set -euo pipefail

METHOD="${1:?installer, panel or menu}"
OLD="${OLD:-v2.5.2}"
REPO="AbolfazlTafakori/w-ui"
ROOT="$(git rev-parse --show-toplevel)"
W="${WORK:-/tmp/wui-upgrade}"
PASS="ci-upgrade-pass-1"
NEW_VERSION="2.99.0" # newer than any release, so the panel offers it
[[ $EUID -eq 0 ]] || { echo "run as root" >&2; exit 1; }
rm -rf "$W"; mkdir -p "$W/rel"

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
fail() { printf '\n\033[31mFAIL: %s\033[0m\n' "$*" >&2; journalctl -u wui -n 60 --no-pager >&2 || true; exit 1; }

say "tools and a test signing key"
(cd "$ROOT" && go build -o "$W/upgrade" ./tools/upgrade && go build -o "$W/wui-host" ./cmd/wui)
keys="$("$W/wui-host" keygen)"
PUB="$(sed -n 's/.*update\.PublicKey=\([^"]*\)".*/\1/p' <<<"$keys")"
WUI_SIGNING_KEY="$(sed -n 's/^ *WUI_SIGNING_KEY=//p' <<<"$keys")"
export WUI_SIGNING_KEY
[[ -n "$PUB" && -n "$WUI_SIGNING_KEY" ]] || fail "keygen printed no key pair"

say "$OLD, installed with its own installer"
if [[ "$METHOD" == installer ]]; then
  curl -fsSL -o "$W/wui-old" "https://github.com/$REPO/releases/download/$OLD/wui-linux-amd64"
else
  git -C "$ROOT" worktree add --detach "$W/old-src" "$OLD" >/dev/null 2>&1
  (cd "$W/old-src" && CGO_ENABLED=0 go build -trimpath \
    -ldflags "-X main.version=${OLD#v} -X github.com/abolfazl/w-ui/internal/update.PublicKey=$PUB" \
    -o "$W/wui-old" ./cmd/wui)
  git -C "$ROOT" worktree remove --force "$W/old-src"
fi
chmod +x "$W/wui-old"
git -C "$ROOT" show "$OLD:install.sh" > "$W/install-old.sh"
WUI_MENU_URL="https://raw.githubusercontent.com/$REPO/$OLD/w-ui.sh" \
  bash "$W/install-old.sh" --local "$W/wui-old" -y --no-tls --no-amnezia --db sqlite \
  --username ci --password "$PASS" --sub-port 2096 > "$W/install-old.log" 2>&1 \
  || { tail -40 "$W/install-old.log"; fail "$OLD did not install"; }
# shellcheck disable=SC1091
. /etc/wui/install-result.env
URL="http://127.0.0.1:${WUI_PANEL_PORT}/${WUI_WEB_BASE_PATH:+$WUI_WEB_BASE_PATH/}"
echo "panel at $URL"

say "a customer's side of the tunnel: a network namespace on a veth"
ip netns add wuicust
ip link add wuiveth0 type veth peer name wuiveth1
ip link set wuiveth1 netns wuicust
ip addr add 10.99.0.1/24 dev wuiveth0 && ip link set wuiveth0 up
ip -n wuicust addr add 10.99.0.2/24 dev wuiveth1 && ip -n wuicust link set wuiveth1 up && ip -n wuicust link set lo up

say "customers on $OLD"
"$W/upgrade" seed -url "$URL" -user ci -pass "$PASS" -version "$OLD" -out "$W/fixture" \
  -sqlite /var/lib/wui/wui.db -listen "127.0.0.1:$WUI_PANEL_PORT" -endpoint 10.99.0.1 \
  || fail "seeding $OLD"

# The customer's own configuration, as their link served it, in the namespace.
conf="$W/fixture/sub/wg-unlimited.conf"
addr="$(sed -n 's/^Address *= *//p' "$conf" | head -1)"
ip -n wuicust link add wgc type wireguard
ip netns exec wuicust wg setconf wgc <(wg-quick strip "$conf")
ip -n wuicust addr add "$addr" dev wgc
ip -n wuicust link set wgc up
ip -n wuicust route add 10.71.0.0/24 dev wgc
# WireGuard sends its first handshake with the first packet and tries again
# only five seconds later, so a first try lost while the veth comes up would
# fail a short ping. The tunnel gets half a minute to answer.
tunnel_up=""
for _ in $(seq 15); do
  if ip netns exec wuicust ping -c 1 -W 2 10.71.0.1 >/dev/null 2>&1; then tunnel_up=1; break; fi
done
if [[ -z "$tunnel_up" ]]; then
  ip netns exec wuicust wg show; wg show
  ip -br addr; ip rule; ip route show table all | grep -v "^local\|^broadcast" | head -40
  nft list ruleset | head -150
  fail "the customer's tunnel never carried a ping on $OLD"
fi
echo "the customer's tunnel answers on $OLD"

"$W/upgrade" snapshot -out "$W/before.json"

say "this build, signed with the test key"
(cd "$ROOT" && CGO_ENABLED=0 go build -trimpath \
  -ldflags "-X main.version=$NEW_VERSION -X github.com/abolfazl/w-ui/internal/update.PublicKey=$PUB" \
  -o "$W/rel/wui-linux-amd64" ./cmd/wui)
"$W/wui-host" sign "$W/rel/wui-linux-amd64" >/dev/null
cp "$ROOT/install.sh" "$ROOT/w-ui.sh" "$ROOT/update.sh" "$W/rel/"
(cd "$W/rel" && sha256sum -- * > SHA256SUMS)

if [[ "$METHOD" != installer ]]; then
  say "GitHub, played by tools/upgrade fakegithub"
  "$W/upgrade" fakegithub -addr :443 -release "$W/rel" -tag "v$NEW_VERSION" -raw "$ROOT" -ca "$W/ca.pem" \
    > "$W/fakegithub.log" 2>&1 &
  for _ in $(seq 1 20); do [[ -s "$W/ca.pem" ]] && break; sleep 0.5; done
  cp "$W/ca.pem" /usr/local/share/ca-certificates/wui-upgrade-test.crt
  update-ca-certificates >/dev/null
  cp /etc/hosts "$W/hosts.orig"
  echo "127.0.0.1 api.github.com github.com raw.githubusercontent.com objects.githubusercontent.com codeload.github.com" >> /etc/hosts
fi

say "the customer pings through the update"
ip netns exec wuicust ping -D -i 0.2 -W 1 10.71.0.1 > "$W/ping.log" 2>&1 &
ping_pid=$!
sleep 3
t0="$(date +%s.%N)"
case "$METHOD" in
  installer)
    bash "$ROOT/install.sh" --local "$W/rel/wui-linux-amd64" -y > "$W/install-new.log" 2>&1 \
      || { tail -40 "$W/install-new.log"; fail "install.sh over $OLD"; } ;;
  panel)
    "$W/upgrade" selfupdate -url "$URL" -user ci -pass "$PASS" -want "$NEW_VERSION" \
      || { journalctl -u wui-update -n 40 --no-pager; fail "the panel's update button"; } ;;
  menu)
    echo y | w-ui update > "$W/menu.log" 2>&1 || { tail -40 "$W/menu.log"; fail "w-ui update"; } ;;
  *) fail "unknown method $METHOD" ;;
esac
t1="$(date +%s.%N)"
"$W/upgrade" verify -url "$URL" -fixture "$W/fixture" >/dev/null 2>&1 || true # waits for the panel
sleep 5
kill "$ping_pid" 2>/dev/null || true
wait "$ping_pid" 2>/dev/null || true

# The longest the customer went without an answer, from a moment before the
# update began to the end of the log.
gap="$(awk -v t0="$t0" '
  /bytes from/ { ts = substr($1, 2, length($1) - 2) + 0
                 if (ts >= t0 - 1) { if (prev && ts - prev > max) max = ts - prev; prev = ts; n++ }
                 last = ts }
  END { printf "%.1f %d %.3f", max, n, last }' "$W/ping.log")"
read -r longest replies last_reply <<<"$gap"
took="$(awk -v a="$t0" -v b="$t1" 'BEGIN { printf "%.1f", b - a }')"
echo "the update took ${took}s; the customer's longest wait for a reply: ${longest}s ($replies replies)"
echo "customer_disconnected_seconds=$longest" >> "${GITHUB_OUTPUT:-/dev/null}"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  printf '| %s | %s → %s | %ss | %ss |\n' "$METHOD" "$OLD" "$NEW_VERSION" "$took" "$longest" >> "$GITHUB_STEP_SUMMARY"
fi
awk -v a="$last_reply" -v b="$t1" 'BEGIN { exit !(a > b) }' \
  || fail "the customer's tunnel stopped answering after the update"

say "after the update"
got="$(/usr/local/bin/wui version 2>/dev/null || true)"
[[ "$got" == *"$NEW_VERSION"* ]] || fail "the installed binary says $got, not $NEW_VERSION"
"$W/upgrade" verify -url "$URL" -fixture "$W/fixture" || fail "customers, links or tunnels changed"
"$W/upgrade" snapshot -out "$W/after.json"
"$W/upgrade" compare -before "$W/before.json" -after "$W/after.json" || fail "the update changed what it must leave alone"
ip netns exec wuicust wg show wgc latest-handshakes
systemctl is-active --quiet wui-update.path || fail "wui-update.path is not active after the update"

if [[ -f "$W/hosts.orig" ]]; then cp "$W/hosts.orig" /etc/hosts; fi
echo "upgrade from $OLD by $METHOD: everything as it was"
