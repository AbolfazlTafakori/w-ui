#!/usr/bin/env bash
#
# W-UI installer.
#
# Brings a bare server to the point where the panel can actually serve tunnels:
# WireGuard, AmneziaWG, OpenVPN, nftables, kernel forwarding, a systemd unit and
# the panel itself. Safe to re-run — it upgrades in place rather than starting
# over, and every step checks whether it is already done.
#
#   curl -fsSL https://raw.githubusercontent.com/abolfazl/w-ui/main/install.sh | bash
#
# Flags:
#   --local <path>     install a binary you already built instead of downloading
#   --from-source      build from the repository in the current directory
#   --no-amnezia       skip AmneziaWG (it builds a kernel module and needs headers)
#   --no-openvpn       skip OpenVPN if you only sell WireGuard
#   --port <n>         panel port (default 2096)
#   --uninstall        remove the service, binary and unit; keeps the database
#   --purge            remove everything including the database
#
set -euo pipefail

# Whether this host runs systemd. Checked once: a container and a few
# distributions do not, and the installer has to finish on them rather than
# aborting at the first systemctl call with a message about a bus.
have_systemd() {
  [[ -d /run/systemd/system ]] && command -v systemctl >/dev/null 2>&1
}

BIN_PATH=/usr/local/bin/wui
MENU_PATH=/usr/local/bin/w-ui
MENU_URL="${WUI_MENU_URL:-https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/w-ui.sh}"
DATA_DIR=/var/lib/wui
CONF_DIR=/etc/wui
UNIT=/etc/systemd/system/wui.service
SERVICE_USER=wui
RELEASE_URL="${WUI_RELEASE_URL:-}"

PANEL_PORT=2096
WANT_AMNEZIA=1
WANT_OPENVPN=1
LOCAL_BIN=""
FROM_SOURCE=0
ACTION=install

# ── output ───────────────────────────────────────────────────────────────────
if [[ -t 1 ]]; then
  R=$'\e[31m'; G=$'\e[32m'; Y=$'\e[33m'; B=$'\e[1m'; D=$'\e[2m'; N=$'\e[0m'
else
  R=""; G=""; Y=""; B=""; D=""; N=""
fi

step() { printf '\n%s==>%s %s%s%s\n' "$B" "$N" "$B" "$*" "$N"; }
info() { printf '    %s\n' "$*"; }
ok()   { printf '    %s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf '    %s!%s %s\n' "$Y" "$N" "$*"; }
die()  { printf '\n%serror:%s %s\n\n' "$R" "$N" "$*" >&2; exit 1; }

# ── arguments ────────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --local)       LOCAL_BIN="${2:?--local needs a path}"; shift 2 ;;
    --from-source) FROM_SOURCE=1; shift ;;
    --no-amnezia)  WANT_AMNEZIA=0; shift ;;
    --no-openvpn)  WANT_OPENVPN=0; shift ;;
    --port)        PANEL_PORT="${2:?--port needs a number}"; shift 2 ;;
    --uninstall)   ACTION=uninstall; shift ;;
    --purge)       ACTION=purge; shift ;;
    -h|--help)     awk '/^# Flags:/,/^set -/' "$0" | sed 's/^# \{0,1\}//; /^set -/d'; exit 0 ;;
    *)             die "unknown option: $1" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "run as root (sudo bash $0)"

# ── platform ─────────────────────────────────────────────────────────────────
detect_os() {
  [[ -r /etc/os-release ]] || die "cannot read /etc/os-release; unsupported system"
  # shellcheck disable=SC1091
  . /etc/os-release
  OS_ID="${ID:-unknown}"
  OS_LIKE="${ID_LIKE:-}"
  OS_NAME="${PRETTY_NAME:-$OS_ID}"
  OS_VERSION="${VERSION_ID:-}"

  case "$OS_ID" in
    ubuntu|debian) FAMILY=debian ;;
    *)
      case "$OS_LIKE" in
        *debian*)          FAMILY=debian ;;
        *rhel*|*fedora*)   FAMILY=rhel ;;
        *)                 die "unsupported distribution: $OS_NAME" ;;
      esac ;;
  esac

  ARCH_RAW="$(uname -m)"
  case "$ARCH_RAW" in
    x86_64|amd64)  ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "unsupported architecture: $ARCH_RAW" ;;
  esac
  KERNEL="$(uname -r)"
}

# ── uninstall ────────────────────────────────────────────────────────────────
do_uninstall() {
  step "Removing W-UI"
  if have_systemd && systemctl list-unit-files 2>/dev/null | grep -q '^wui\.service'; then
    systemctl disable --now wui.service >/dev/null 2>&1 || true
    ok "service stopped and disabled"
  fi
  rm -f "$UNIT"
  have_systemd && systemctl daemon-reload || true
  rm -f "$BIN_PATH" "$MENU_PATH"
  ok "binary and unit removed"

  if [[ "$ACTION" == purge ]]; then
    rm -rf "$DATA_DIR" "$CONF_DIR"
    id -u "$SERVICE_USER" >/dev/null 2>&1 && userdel "$SERVICE_USER" 2>/dev/null || true
    warn "database and configuration deleted"
  else
    info "database kept at $DATA_DIR — use --purge to delete it too"
  fi

  # The VPN packages are deliberately left alone: they may predate this panel,
  # and removing WireGuard would take down tunnels the operator still needs.
  info "wireguard / openvpn / nftables were left installed"
  printf '\n%sW-UI removed.%s\n\n' "$G" "$N"
  exit 0
}

# ── packages ─────────────────────────────────────────────────────────────────
pkg_refresh() {
  case "$FAMILY" in
    debian) DEBIAN_FRONTEND=noninteractive apt-get update -qq ;;
    rhel)   : ;;  # dnf refreshes per transaction
  esac
}

pkg_install() {
  case "$FAMILY" in
    debian) DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" >/dev/null ;;
    rhel)   dnf install -y -q "$@" >/dev/null ;;
  esac
}

have() { command -v "$1" >/dev/null 2>&1; }

install_base() {
  step "Installing base packages"
  pkg_refresh

  local base
  case "$FAMILY" in
    debian) base=(curl ca-certificates gnupg tar iproute2 nftables iptables qrencode) ;;
    rhel)   base=(curl ca-certificates gnupg2 tar iproute nftables iptables qrencode) ;;
  esac

  pkg_install "${base[@]}" || die "could not install base packages"
  ok "base tools installed"
}

install_wireguard() {
  step "Installing WireGuard"
  case "$FAMILY" in
    debian) pkg_install wireguard wireguard-tools ;;
    rhel)   pkg_install wireguard-tools ;;
  esac

  have wg || die "wireguard-tools did not install; the panel cannot serve WireGuard without it"
  ok "wg $(wg --version 2>/dev/null | awk '{print $2}')"

  # The module is built into the kernel from 5.6 onward. Loading it now surfaces
  # a container or a stripped kernel here, rather than at the first customer.
  if modprobe wireguard 2>/dev/null; then
    ok "wireguard kernel module loaded"
  else
    warn "could not load the wireguard module (kernel $KERNEL)"
    warn "on a VPS this usually means the provider ships a kernel without it"
  fi
}

install_amnezia() {
  step "Installing AmneziaWG (anti-DPI)"

  # Building a kernel module needs headers matching the running kernel. Missing
  # them is the usual reason DKMS fails, so it is checked before the attempt.
  case "$FAMILY" in
    debian)
      if ! pkg_install "linux-headers-$KERNEL"; then
        warn "no headers for kernel $KERNEL; skipping AmneziaWG"
        warn "standard WireGuard still works — install headers and re-run to add it"
        AMNEZIA_OK=0
        return 0
      fi
      pkg_install software-properties-common
      if ! add-apt-repository -y ppa:amnezia/ppa >/dev/null 2>&1; then
        warn "could not add ppa:amnezia/ppa; skipping AmneziaWG"
        AMNEZIA_OK=0
        return 0
      fi
      pkg_refresh
      if ! pkg_install amneziawg; then
        warn "amneziawg failed to build against kernel $KERNEL; skipping"
        AMNEZIA_OK=0
        return 0
      fi
      ;;
    rhel)
      pkg_install "kernel-devel-$KERNEL" dnf-plugins-core || true
      dnf copr enable -y amneziavpn/amneziawg >/dev/null 2>&1 || {
        warn "could not enable the AmneziaWG COPR; skipping"
        AMNEZIA_OK=0
        return 0
      }
      pkg_install amneziawg-dkms amneziawg-tools || {
        warn "amneziawg failed to build; skipping"
        AMNEZIA_OK=0
        return 0
      }
      ;;
  esac

  if have awg; then
    AMNEZIA_OK=1
    ok "awg $(awg --version 2>/dev/null | awk '{print $2}')"
    if [[ -r /sys/module/amneziawg/version ]]; then
      ok "kernel module $(cat /sys/module/amneziawg/version)"
    else
      warn "awg installed but the module is not loaded yet; a reboot may be needed"
    fi
  else
    AMNEZIA_OK=0
    warn "awg not found after install; AmneziaWG unavailable"
  fi
}

install_openvpn() {
  step "Installing OpenVPN"
  pkg_install openvpn easy-rsa || {
    warn "openvpn failed to install; the panel will only offer WireGuard"
    OPENVPN_OK=0
    return 0
  }

  # OpenVPN needs a tun device to exist before it will start. It is present on
  # almost every host and absent on almost every minimal container, and the
  # failure without it is a server that exits at boot with a message about a
  # file rather than about networking.
  modprobe tun 2>/dev/null || true
  if [[ -c /dev/net/tun ]]; then
    ok "/dev/net/tun present"
  else
    warn "/dev/net/tun is missing; OpenVPN interfaces will not start"
    warn "  load it with: modprobe tun   (and add 'tun' to /etc/modules-load.d/)"
  fi
  echo tun > /etc/modules-load.d/wui-tun.conf 2>/dev/null || true

  if have openvpn; then
    OPENVPN_OK=1
    ok "$(openvpn --version 2>/dev/null | head -1 | cut -d' ' -f1-2)"
  else
    OPENVPN_OK=0
    warn "openvpn not on PATH after install"
  fi
}

enable_forwarding() {
  step "Enabling kernel forwarding"

  # Written to a file rather than only applied, or the setting is lost on the
  # first reboot and every tunnel stops routing with no obvious cause.
  cat >/etc/sysctl.d/99-wui.conf <<'SYSCTL'
# Installed by W-UI. Without forwarding, packets arriving on a tunnel are
# never routed out to the internet and customers connect but reach nothing.
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1
SYSCTL

  sysctl --system >/dev/null 2>&1 || sysctl -p /etc/sysctl.d/99-wui.conf >/dev/null
  local v4 v6
  v4=$(sysctl -n net.ipv4.ip_forward 2>/dev/null || echo 0)
  v6=$(sysctl -n net.ipv6.conf.all.forwarding 2>/dev/null || echo 0)
  [[ "$v4" == 1 ]] && ok "IPv4 forwarding on" || warn "IPv4 forwarding is still off"
  [[ "$v6" == 1 ]] && ok "IPv6 forwarding on" || warn "IPv6 forwarding is still off"
}

check_nftables() {
  step "Checking the enforcement engine"
  have nft || die "nft missing; quota enforcement cannot run without nftables"
  ok "nft $(nft --version 2>/dev/null | awk '{print $2}')"

  # Proving the ruleset is writable now beats discovering it when the first
  # customer's limit silently fails to apply.
  if nft list tables >/dev/null 2>&1; then
    ok "kernel ruleset is readable"
  else
    warn "cannot read the nftables ruleset; the panel needs CAP_NET_ADMIN"
  fi

  # Everything the panel promises about data limits rests on this one module.
  # Without it the kernel cannot stop a transfer at a byte boundary, so limits
  # fall back to polling and a customer on a fast link overshoots their quota by
  # whatever they can pull between two collections. That is a silent failure —
  # the panel still shows a limit, it just does not hold — so it is worth being
  # noisy about at install time rather than at the end of the month.
  modprobe nft_quota 2>/dev/null || true
  if nft add table inet wui_probe 2>/dev/null &&
     nft add quota inet wui_probe q '{ over 1000 bytes }' 2>/dev/null; then
    ok "kernel supports quota objects; data limits are enforced exactly"
  else
    warn "this kernel has no nft_quota support"
    warn "  data limits will be enforced by polling instead, which means a"
    warn "  customer can overshoot their quota before the panel notices."
    warn "  Hetzner's stock Ubuntu and Debian kernels do support it; custom,"
    warn "  minimal or container kernels often do not."
  fi
  nft delete table inet wui_probe 2>/dev/null || true

  # Speed limits need a classful scheduler. Without one tc accepts the command
  # and the kernel quietly ignores it, so the panel would show a limit that no
  # packet ever meets.
  modprobe sch_htb 2>/dev/null || true
  if ip link add wui-htbprobe type dummy 2>/dev/null; then
    if tc qdisc replace dev wui-htbprobe root handle 1: htb default ffff 2>/dev/null; then
      ok "kernel supports HTB; speed limits are enforced"
    else
      warn "this kernel has no HTB scheduler"
      warn "  per-customer speed limits will be recorded but never applied."
    fi
    ip link del wui-htbprobe 2>/dev/null || true
  fi
}

# ── the panel ────────────────────────────────────────────────────────────────
install_binary() {
  step "Installing the panel"

  if [[ -n "$LOCAL_BIN" ]]; then
    [[ -f "$LOCAL_BIN" ]] || die "no such file: $LOCAL_BIN"
    install -m 0755 "$LOCAL_BIN" "$BIN_PATH"
    ok "installed from $LOCAL_BIN"

  elif [[ "$FROM_SOURCE" == 1 ]]; then
    have go || die "--from-source needs Go on PATH"
    [[ -f go.mod ]] || die "--from-source must run from the repository root"
    info "building (this takes a minute)…"
    CGO_ENABLED=0 go build -ldflags "-s -w" -o "$BIN_PATH" ./cmd/wui \
      || die "build failed"
    chmod 0755 "$BIN_PATH"
    ok "built from source"

  elif [[ -n "$RELEASE_URL" ]]; then
    info "downloading $RELEASE_URL"
    curl -fsSL "$RELEASE_URL" -o /tmp/wui.new || die "download failed"
    install -m 0755 /tmp/wui.new "$BIN_PATH"
    rm -f /tmp/wui.new
    ok "installed from release"

  else
    die "no binary source. Use --local <path>, --from-source, or set WUI_RELEASE_URL"
  fi

  [[ -x "$BIN_PATH" ]] || die "installed binary is not executable"
  ok "$BIN_PATH"
}

install_menu() {
  step "Installing the w-ui command"

  # Prefer the copy beside this installer. Someone running --from-source or
  # --local wants the script from their checkout, not whatever is on the branch.
  local src=""
  if [[ -f "$(dirname "$0")/w-ui.sh" ]]; then
    src="$(dirname "$0")/w-ui.sh"
  elif [[ -f ./w-ui.sh ]]; then
    src=./w-ui.sh
  fi

  if [[ -n "$src" ]]; then
    install -m 0755 "$src" "$MENU_PATH"
    ok "installed from $src"
  elif curl -fsSL "$MENU_URL" -o /tmp/w-ui.sh; then
    install -m 0755 /tmp/w-ui.sh "$MENU_PATH"
    rm -f /tmp/w-ui.sh
    ok "downloaded"
  else
    warn "could not install the w-ui command; the panel still works"
    return 0
  fi

  # A CRLF here makes the shebang line unparseable, and the error then names
  # the interpreter rather than the file - a confusing first impression on a
  # repository that has been edited on Windows.
  local clean
  clean=$(mktemp)
  if tr -d '\015' < "$MENU_PATH" > "$clean"; then
    install -m 0755 "$clean" "$MENU_PATH"
  fi
  rm -f "$clean"

  ok "$MENU_PATH - run 'w-ui' to manage the panel"
}

create_user() {
  step "Creating the service account"
  if id -u "$SERVICE_USER" >/dev/null 2>&1; then
    ok "user $SERVICE_USER already exists"
  else
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
    ok "user $SERVICE_USER created"
  fi

  install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$DATA_DIR" "$CONF_DIR"
  ok "$DATA_DIR (0750, owned by $SERVICE_USER)"
}

write_unit() {
  step "Installing the systemd unit"

  cat >"$UNIT" <<UNITFILE
[Unit]
Description=W-UI — WireGuard and OpenVPN panel
Documentation=https://github.com/abolfazl/w-ui
After=network-online.target nftables.service
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER

# The panel programs nftables and WireGuard through netlink, which needs
# CAP_NET_ADMIN. Granting exactly that instead of running as root keeps a
# compromised panel from owning the whole machine.
AmbientCapabilities=CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_ADMIN
NoNewPrivileges=true

Environment=WUI_LISTEN=0.0.0.0:$PANEL_PORT
Environment=WUI_DATA_DIR=$DATA_DIR
Environment=WUI_DB_SOURCE=$DATA_DIR/wui.db
EnvironmentFile=-$CONF_DIR/wui.env

ExecStart=$BIN_PATH
WorkingDirectory=$DATA_DIR

Restart=always
RestartSec=3

# The panel needs the network and its own data directory, nothing else.
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=$DATA_DIR
ProtectKernelTunables=false
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK

[Install]
WantedBy=multi-user.target
UNITFILE

  if have_systemd; then
    systemctl daemon-reload
    ok "$UNIT"
  else
    ok "$UNIT (written, not loaded)"
    warn "this host is not running systemd, so the service was not registered"
    warn "  start the panel yourself with:"
    warn "    WUI_LISTEN=0.0.0.0:$PANEL_PORT WUI_DATA_DIR=$DATA_DIR $BIN_PATH"
  fi
}

open_firewall() {
  step "Opening the panel port"
  if have ufw && ufw status 2>/dev/null | grep -q "Status: active"; then
    ufw allow "$PANEL_PORT/tcp" >/dev/null 2>&1 && ok "ufw: $PANEL_PORT/tcp allowed"
  elif have firewall-cmd && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --add-port="$PANEL_PORT/tcp" >/dev/null 2>&1
    firewall-cmd --reload >/dev/null 2>&1
    ok "firewalld: $PANEL_PORT/tcp allowed"
  else
    info "no active host firewall detected"
    info "if your provider has one, allow TCP $PANEL_PORT and your tunnel's UDP port"
  fi
}

start_service() {
  if ! have_systemd; then
    # Nothing to start it with. Said plainly rather than failing: everything
    # else installed correctly and the panel runs perfectly well by hand.
    step "Starting W-UI"
    warn "skipped: no systemd on this host"
    return 0
  fi

  step "Starting W-UI"
  local fresh=0
  [[ -f "$DATA_DIR/wui.db" ]] || fresh=1

  systemctl enable wui.service >/dev/null 2>&1
  systemctl restart wui.service

  for _ in $(seq 1 20); do
    if systemctl is-active --quiet wui.service; then break; fi
    sleep 0.5
  done

  systemctl is-active --quiet wui.service \
    || die "service failed to start — see: journalctl -u wui -n 50 --no-pager"
  ok "wui.service is running"

  if [[ "$fresh" == 1 ]]; then
    # The generated password is printed once, to the journal, and never stored
    # in the clear. Fishing it out here saves the operator the search.
    sleep 1
    FIRST_RUN_PW=$(journalctl -u wui --since "-2 min" --no-pager 2>/dev/null \
      | grep -A1 'password' | grep -oE '[A-Za-z0-9_-]{16}' | head -1 || true)
  fi
}

summary() {
  local ip
  ip=$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null \
    || hostname -I 2>/dev/null | awk '{print $1}' || echo "your-server-ip")

  printf '\n%s────────────────────────────────────────────────────────────%s\n' "$D" "$N"
  printf '  %sW-UI is installed%s\n\n' "$B" "$N"
  printf '  Panel      http://%s:%s\n' "$ip" "$PANEL_PORT"

  if [[ -n "${FIRST_RUN_PW:-}" ]]; then
    printf '  Username   admin\n'
    printf '  Password   %s%s%s\n' "$B" "$FIRST_RUN_PW" "$N"
    printf '\n  %sThis password is shown once. Change it after signing in.%s\n' "$Y" "$N"
  elif ! have_systemd; then
    # Nothing started the panel, so no account exists yet. Claiming an existing
    # one here would send the operator looking for something that is not there.
    printf '  %sThe panel has not been started yet, so no account exists.%s\n' "$Y" "$N"
    printf '  It prints a generated password to its log the first time it runs.\n'
  elif [[ -f "$DATA_DIR/wui.db" ]]; then
    printf '  Sign in with your existing admin account.\n'
  else
    printf '  %sThe first-run password could not be read from the log.%s\n' "$Y" "$N"
    printf '  Find it with: journalctl -u wui | grep -A6 "First run"\n'
  fi

  printf '\n  %sInstalled%s\n' "$B" "$N"
  printf '    WireGuard    %s\n' "$(have wg && wg --version 2>/dev/null | awk '{print $2}' || echo 'missing')"
  printf '    AmneziaWG    %s\n' "$([[ "${AMNEZIA_OK:-0}" == 1 ]] && awg --version 2>/dev/null | awk '{print $2}' || echo 'not installed')"
  printf '    OpenVPN      %s\n' "$([[ "${OPENVPN_OK:-0}" == 1 ]] && openvpn --version 2>/dev/null | head -1 | awk '{print $2}' || echo 'not installed')"
  printf '    nftables     %s\n' "$(nft --version 2>/dev/null | awk '{print $2}')"
  printf '    forwarding   %s\n' "$([[ "$(sysctl -n net.ipv4.ip_forward)" == 1 ]] && echo on || echo off)"

  printf '\n  %sCommands%s\n' "$B" "$N"
  if have_systemd; then
    printf '    w-ui                        the management menu\n'
    printf '    systemctl status wui        service state\n'
    printf '    journalctl -u wui -f        live log\n'
    printf '    systemctl restart wui       restart\n'
  else
    # Only what works here. Four systemctl lines on a host with no systemd are
    # four commands that fail.
    printf '    %s   start it in the foreground\n' "$BIN_PATH"
    printf '    w-ui settings               what it is configured with\n'
    printf '    w-ui admin                  reset the administrator\n'
  fi
  printf '    bash %s --uninstall   remove (keeps the database)\n' "$0"

  printf '\n  %sNext%s open the panel, add an interface, then add clients.\n' "$B" "$N"
  printf '%s────────────────────────────────────────────────────────────%s\n\n' "$D" "$N"
}

# ── run ──────────────────────────────────────────────────────────────────────
detect_os
[[ "$ACTION" == install ]] || do_uninstall

printf '\n  %sW-UI installer%s\n' "$B" "$N"
printf '  %s%s · kernel %s · %s%s\n' "$D" "$OS_NAME" "$KERNEL" "$ARCH" "$N"

install_base
install_wireguard
if [[ "$WANT_AMNEZIA" == 1 ]]; then install_amnezia; else info "AmneziaWG skipped (--no-amnezia)"; fi
if [[ "$WANT_OPENVPN" == 1 ]]; then install_openvpn; else info "OpenVPN skipped (--no-openvpn)"; fi
enable_forwarding
check_nftables
install_binary
install_menu
create_user
write_unit
open_firewall
start_service
summary
