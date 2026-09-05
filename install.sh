#!/usr/bin/env bash
#
# W-UI installer.
#
# Brings a bare server to the point where the panel can actually serve tunnels:
# WireGuard, AmneziaWG, OpenVPN, nftables, kernel forwarding, a systemd unit and
# the panel itself. Safe to re-run — it upgrades in place rather than starting
# over, and every step checks whether it is already done.
#
#   curl -fsSL https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh | sudo bash
#
# It asks before it acts: the port, who signs in, and whether to fetch a
# certificate. Answer nothing and it uses the defaults below. Every answer can
# also be given as a flag, which is what --yes needs to run unattended.
#
# Flags:
#   --local <path>     install a binary you already built instead of downloading
#   --from-source      build from the repository in the current directory
#   --no-amnezia       skip AmneziaWG (it builds a kernel module and needs headers)
#   --no-openvpn       skip OpenVPN if you only sell WireGuard
#   --port <n>         panel port (default 2096)
#   --username <name>  administrator name (default admin)
#   --password <pass>  administrator password (default: generated)
#   --domain <name>    get a Let's Encrypt certificate for this domain
#   --email <addr>     where the certificate authority sends expiry notices
#   --tls-cert <path>  use a certificate you already have
#   --tls-key <path>   its private key
#   --no-tls           serve plain HTTP (only sane behind a proxy or tunnel)
#   --local-only       bind to 127.0.0.1, for use behind a proxy or a tunnel
#   -y, --yes          ask nothing; use flags, environment and defaults
#   --uninstall        remove the service, binary and unit; keeps the database
#   --purge            remove everything including the database
#
# errexit and nounset, deliberately without pipefail.
#
# Nearly every pipeline here ends in `head`, `grep -q` or `tail`, which is
# how those tools are meant to be used: they close the pipe as soon as they
# have what they need, and the command upstream dies of SIGPIPE. Under
# pipefail that is reported as a failure of the whole pipeline -- exit 141 --
# and errexit then ends the install without printing anything at all.
#
# That is not a hypothetical. It killed a real install at the first random
# password: `tr -dc … </dev/urandom | head -c 16` exits 141 every single
# time, and the whole run stopped there with an empty log.
#
# Worse, it can give the wrong answer rather than no answer:
# `certbot plugins | grep -q nginx` returns 141 when grep matches and finds
# what it wanted, so the check reports the plugin missing when it is there.
#
# Nothing here depends on catching a failure in the middle of a pipeline;
# every pipeline's meaning is its last command.
set -eu

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
# Where to fetch from when nothing else was asked for. Overridable, because a
# fork or a mirror is a normal thing to install from.
REPO="${WUI_REPO:-AbolfazlTafakori/w-ui}"
BRANCH="${WUI_BRANCH:-main}"

# Only a starting point for the prompt. Nothing is installed on it unless the
# operator asks for it by name: the default is a random free port.
PANEL_PORT=2096
WANT_AMNEZIA=1
WANT_OPENVPN=1
LOCAL_BIN=""
FROM_SOURCE=0
ACTION=install

# What the questions fill in. Pre-seeded from the environment so a cloud
# image can set them once and never see a prompt.
ADMIN_USER="${WUI_ADMIN_USER:-}"
ADMIN_PASS="${WUI_ADMIN_PASSWORD:-}"
ADMIN_GENERATED=0
TLS_MODE=""
TLS_CERT="${WUI_TLS_CERT:-}"
TLS_KEY="${WUI_TLS_KEY:-}"
ACME_DOMAIN="${WUI_DOMAIN:-}"
ACME_EMAIL="${WUI_ACME_EMAIL:-}"
ACME_METHOD=""
# Which address the panel binds to. 127.0.0.1 when something else is
# terminating TLS in front of it, in which case the panel must not be
# reachable from outside at all.
LISTEN_ADDR="${WUI_LISTEN_ADDR:-0.0.0.0}"
# The URL path the panel answers on, one segment, empty for the root.
BASE_PATH="${WUI_BASE_PATH:-}"
# Set when an existing install already decided these, so re-running does not
# move a panel that people have bookmarked.
PORT_KNOWN=0
BASE_KNOWN=0
ASSUME_YES=0
INTERACTIVE=0

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
# WUI_LIB_ONLY lets the test suites source this file for its functions without
# installing anything. It is what makes them able to test what actually ships:
# they used to cut the definitions out with sed, and sed on one machine split a
# long comment line in half, so every test after it ran against a file that was
# not this one.
#
# Checked here rather than further down because a sourced file inherits the
# caller's positional parameters -- inside a function, that is the function's
# own arguments, which this loop would then try to parse as install flags.
LIB_ONLY="${WUI_LIB_ONLY:-0}"

while [[ "$LIB_ONLY" != 1 && $# -gt 0 ]]; do
  case "$1" in
    --local)       LOCAL_BIN="${2:?--local needs a path}"; shift 2 ;;
    --from-source) FROM_SOURCE=1; shift ;;
    --no-amnezia)  WANT_AMNEZIA=0; shift ;;
    --no-openvpn)  WANT_OPENVPN=0; shift ;;
    --port)        PANEL_PORT="${2:?--port needs a number}"; PORT_KNOWN=1; shift 2 ;;
    --username)    ADMIN_USER="${2:?--username needs a name}"; shift 2 ;;
    --password)    ADMIN_PASS="${2:?--password needs a value}"; shift 2 ;;
    --domain)      ACME_DOMAIN="${2:?--domain needs a name}"; shift 2 ;;
    --email)       ACME_EMAIL="${2:?--email needs an address}"; shift 2 ;;
    --tls-cert)    TLS_CERT="${2:?--tls-cert needs a path}"; shift 2 ;;
    --tls-key)     TLS_KEY="${2:?--tls-key needs a path}"; shift 2 ;;
    --path)        BASE_PATH="${2:?--path needs a value}"; BASE_KNOWN=1; shift 2 ;;
    --no-path)     BASE_PATH=""; BASE_KNOWN=1; shift ;;
    --local-only)  LISTEN_ADDR=127.0.0.1; shift ;;
    --no-tls)      TLS_MODE=none; shift ;;
    -y|--yes)      ASSUME_YES=1; shift ;;
    --uninstall)   ACTION=uninstall; shift ;;
    --purge)       ACTION=purge; shift ;;
    -h|--help)     awk '/^# Flags:/,/^set -/' "$0" | sed 's/^# \{0,1\}//; /^set -/d'; exit 0 ;;
    *)             die "unknown option: $1" ;;
  esac
done

[[ "$LIB_ONLY" == 1 || $EUID -eq 0 ]] || die "run as root (sudo bash $0)"

# ── platform ─────────────────────────────────────────────────────────────────
detect_os() {
  [[ -r /etc/os-release ]] || die "cannot read /etc/os-release; unsupported system"
  # shellcheck disable=SC1091
  . /etc/os-release
  OS_ID="${ID:-unknown}"
  OS_LIKE="${ID_LIKE:-}"
  OS_NAME="${PRETTY_NAME:-$OS_ID}"
  OS_VERSION="${VERSION_ID:-}"
  # The archive codename, which is what a third-party repository publishes
  # under. A release too new for a PPA is the usual reason apt breaks after
  # adding one.
  OS_CODENAME="${VERSION_CODENAME:-${UBUNTU_CODENAME:-}}"

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
# Third-party repositories break. When one does, everything this installer
# actually needs is still in the distribution's own archive, so a failed
# refresh is reported and stepped over rather than being fatal.
#
# It is fatal only in the sense that `set -e` would make it so: `apt-get update`
# returns non-zero if any single source failed, including one this installer
# added itself on an earlier run. That is how an install could end at the first
# step with nothing done.
pkg_refresh() {
  case "$FAMILY" in
    debian)
      if ! DEBIAN_FRONTEND=noninteractive apt-get update -qq 2>/tmp/wui-apt.err; then
        # Name the repositories that failed, so the operator can see it is not
        # the distribution archive that is unreachable.
        local broken
        broken="$(grep -oE "https?://[^ ]+" /tmp/wui-apt.err 2>/dev/null | sort -u | head -3)"
        warn "some package sources could not be refreshed"
        [[ -n "$broken" ]] && printf '%s\n' "$broken" | sed 's/^/      /'
        warn "continuing with what the distribution archive has"
      fi
      rm -f /tmp/wui-apt.err
      ;;
    rhel)   : ;;  # dnf refreshes per transaction
  esac
}

# Remove an AmneziaWG PPA that this installer added and that does not publish
# for this release.
#
# Left in place it breaks every `apt-get update` on the machine from then on,
# including this installer's own -- which is exactly how a second run ends at
# the first step. Only the file this installer writes is touched.
repair_amnezia_ppa() {
  [[ "$FAMILY" == debian ]] || return 0
  local f found=0
  for f in /etc/apt/sources.list.d/amnezia-ubuntu-ppa-*.list \
           /etc/apt/sources.list.d/amnezia-ubuntu-ppa-*.sources; do
    [[ -e "$f" ]] || continue
    if ! ppa_publishes_for_this_release; then
      rm -f "$f"
      found=1
    fi
  done
  if [[ "$found" == 1 ]]; then
    warn "removed the AmneziaWG repository: it publishes nothing for ${OS_CODENAME:-this release}"
    warn "that repository was breaking apt; standard WireGuard is unaffected"
    DEBIAN_FRONTEND=noninteractive apt-get update -qq >/dev/null 2>&1 || true
  fi
}

# Does the AmneziaWG PPA actually have packages for this release?
#
# `add-apt-repository` succeeds whether or not it does -- it only writes a file
# -- so asking first is the difference between skipping a feature and leaving
# the machine with an apt that no longer works.
ppa_publishes_for_this_release() {
  [[ -n "${OS_CODENAME:-}" ]] || return 1
  # Without curl there is no way to ask, and "cannot ask" must not be read as
  # "no" -- that would have the repair below remove a repository that is
  # working perfectly well. This runs before curl is installed.
  command -v curl >/dev/null 2>&1 || return 0
  curl -fsSI --max-time 10 \
    "https://ppa.launchpadcontent.net/amnezia/ppa/ubuntu/dists/${OS_CODENAME}/Release" \
    >/dev/null 2>&1
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
  # Before anything else: if an earlier run left a repository behind that this
  # release has no packages in, apt is broken and nothing below would work.
  repair_amnezia_ppa
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
      # Asked before it is added. A PPA that publishes nothing for this
      # release still installs cleanly as a sources file and then breaks every
      # apt operation afterwards, including this installer's own on the next
      # run. Ubuntu releases routinely ship before their PPAs catch up.
      if ! ppa_publishes_for_this_release; then
        warn "AmneziaWG has no packages for ${OS_CODENAME:-this release} yet; skipping"
        warn "standard WireGuard is unaffected — re-run later to add it"
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
# The download for this machine's architecture in the newest release, if the
# project has published one.
#
# Asked over the API rather than guessed at a URL, so a release whose assets
# are named differently still resolves, and a repository with no releases at
# all answers cleanly instead of 404-ing on a made-up path.
release_asset_url() {
  local api json
  api="https://api.github.com/repos/${REPO}/releases/latest"
  json="$(curl -fsSL --max-time 20 "$api" 2>/dev/null)" || return 1
  [[ -n "$json" ]] || return 1

  ASSET_URL="$(printf '%s' "$json" \
    | grep -o '"browser_download_url": *"[^"]*"' \
    | sed 's/.*"browser_download_url": *"//; s/"$//' \
    | grep -E "linux[-_]${ARCH}" \
    | head -1)"
  [[ -n "$ASSET_URL" ]]
}

# Build the panel from the project's own source, without needing the repository
# to have been cloned first.
#
# `--from-source` used to require a checkout -- it looked for go.mod in the
# working directory -- which the one-line install can never have. It fetches a
# tarball now, and installs Go if the machine has none, because "install Go and
# come back" is not an answer an installer should be giving.
build_from_tarball() {
  local work
  work="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$work'" RETURN

  if ! have go; then
    info "installing Go to build with"
    pkg_install golang-go || pkg_install golang || die "could not install Go; use --local <path> with a prebuilt binary"
  fi
  have go || die "Go is still not on PATH after installing it"

  info "fetching source"
  curl -fsSL --max-time 120 \
    "https://codeload.github.com/${REPO}/tar.gz/refs/heads/${BRANCH}" \
    -o "$work/src.tgz" || die "could not fetch the source"
  tar -xzf "$work/src.tgz" -C "$work" || die "could not unpack the source"

  local root
  root="$(find "$work" -maxdepth 1 -type d -name 'w-ui-*' | head -1)"
  [[ -n "$root" && -f "$root/go.mod" ]] || die "the source archive does not look like this project"

  info "building (this takes a minute)…"
  ( cd "$root" && CGO_ENABLED=0 GOFLAGS=-mod=mod go build -ldflags "-s -w" -o "$BIN_PATH" ./cmd/wui ) \
    || die "build failed"
  chmod 0755 "$BIN_PATH"
  ok "built from source"
}

# ── asking ───────────────────────────────────────────────────────────────────
# An installer that asks nothing has already decided everything, and some of
# those decisions are wrong for every operator: the port collides with what is
# already on the box, the credentials are a random string nobody chose, and the
# sign-in form that carries an administrator's password runs over plain HTTP.
#
# Everything is asked once, up front, before a single package is fetched — so
# the operator answers for a minute and can then walk away, rather than being
# called back to a prompt after the long part has finished.

# A terminal to talk to, whatever stdin happens to be.
#
# `curl … | bash` gives the shell the script itself on stdin, so a plain `read`
# there swallows the next line of the script instead of the operator's answer.
# /dev/tty is the terminal this process is attached to no matter what stdin was
# redirected to, which is what lets the documented one-liner ask anything at
# all. When there is no terminal — cloud-init, CI, a pipe from a file — every
# question falls back to its flag, its environment variable, or its default.
open_tty() {
  INTERACTIVE=0
  [[ "$ASSUME_YES" == 1 ]] && return 0
  if [[ -e /dev/tty ]] && exec 3<>/dev/tty 2>/dev/null; then
    INTERACTIVE=1
  fi
  return 0
}

# The terminal went away mid-question — Ctrl-D, a closed session, a pipe that
# ran out. Every question that validates its answer sits in a loop, and a loop
# that keeps re-asking a terminal which will never answer again spins forever
# on a machine nobody is watching. It stops here instead.
no_more_input() {
  printf %s "$N" >&3 2>/dev/null || true
  die "no answer — the terminal closed before the questions were finished"
}

# Prompt text, written to the terminal rather than to stdout.
#
# Two reasons it is not a plain printf. An install whose output is being
# captured to a file should still ask its questions on the screen, which is
# what fd 3 is for; and a write that fails must not end the install. Under
# errexit an unchecked `printf … >&3` onto a descriptor that is not writable
# aborts the whole run with nothing but "Bad file descriptor" to show for it.
tty_out() { printf "$@" >&3 2>/dev/null || true; }

# ask VAR "question" "default"
ask() {
  local __var="$1" __q="$2" __def="$3" __ans=""
  if [[ "$INTERACTIVE" == 1 ]]; then
    if [[ -n "$__def" ]]; then
      tty_out '    %s [%s%s%s]: ' "$__q" "$B" "$__def" "$N"
    else
      tty_out '    %s: ' "$__q"
    fi
    IFS= read -r __ans <&3 || no_more_input
  fi
  # Trimmed, because a pasted answer usually arrives with a space attached.
  __ans="${__ans#"${__ans%%[![:space:]]*}"}"
  __ans="${__ans%"${__ans##*[![:space:]]}"}"
  [[ -n "$__ans" ]] || __ans="$__def"
  printf -v "$__var" '%s' "$__ans"
}

# ask_secret VAR "question" — typed blind, confirmed, never echoed.
ask_secret() {
  local __var="$1" __q="$2" a="" b=""
  if [[ "$INTERACTIVE" != 1 ]]; then printf -v "$__var" '%s' ""; return 0; fi
  while true; do
    tty_out '    %s: ' "$__q"
    IFS= read -rs a <&3 || no_more_input
    tty_out '\n'
    if [[ -z "$a" ]]; then printf -v "$__var" '%s' ""; return 0; fi
    if (( ${#a} < 8 )); then
      tty_out '    %s!%s at least 8 characters\n' "$Y" "$N"
      continue
    fi
    tty_out '    confirm it: '
    IFS= read -rs b <&3 || no_more_input
    tty_out '\n'
    if [[ "$a" == "$b" ]]; then printf -v "$__var" '%s' "$a"; return 0; fi
    tty_out '    %s!%s they do not match — try again\n' "$Y" "$N"
  done
}

# ask_yn "question" default(y|n) — true when the answer is yes.
ask_yn() {
  local q="$1" def="${2:-n}" ans="" hint="y/N"
  if [[ "$INTERACTIVE" != 1 ]]; then [[ "$def" == y ]]; return; fi
  [[ "$def" == y ]] && hint="Y/n"
  while true; do
    tty_out '    %s [%s]: ' "$q" "$hint"
    IFS= read -r ans <&3 || no_more_input
    ans="${ans:-$def}"
    case "${ans,,}" in
      y|yes) return 0 ;;
      n|no)  return 1 ;;
      *) printf '    %s!%s answer y or n\n' "$Y" "$N" >&3 ;;
    esac
  done
}

# Is something already listening on this TCP port?
#
# The panel must not land on a port another project is serving from. This can
# run before iproute2 is guaranteed to be present, so: ss, then netstat, then
# bash's own TCP connect — whichever the machine has.
port_taken() {
  local p="$1"
  if have ss; then
    ss -ltnH "sport = :$p" 2>/dev/null | grep -q . && return 0
    return 1
  fi
  if have netstat; then
    netstat -ltn 2>/dev/null | grep -qE "[:.]${p}[[:space:]]" && return 0
    return 1
  fi
  # No tool for it: ask the port itself. An answer means somebody is home.
  if (exec 4<>"/dev/tcp/127.0.0.1/$p") 2>/dev/null; then
    exec 4>&-
    return 0
  fi
  return 1
}

# Who is on that port, in words, so the operator recognises their own service
# rather than being told only that the port is unavailable.
port_owner() {
  local p="$1" who=""
  if have ss; then
    who=$(ss -ltnpH "sport = :$p" 2>/dev/null | sed -n 's/.*users:((\"\([^\"]*\)\".*/\1/p' | head -1)
  fi
  if [[ -n "$who" ]]; then printf '%s' "$who"; else printf 'another service'; fi
}

# What this machine is already running, so an upgrade does not move the panel.
#
# A re-run that handed out a fresh random port and a fresh random path would
# leave every bookmark, every firewall rule and every note the operator wrote
# down pointing at nothing.
read_existing() {
  [[ -f "$UNIT" ]] || return 0
  local v
  v=$(sed -n 's/^Environment=WUI_LISTEN=.*:\([0-9]\{1,5\}\)$/\1/p' "$UNIT" | head -1)
  if [[ -n "$v" && "$PORT_KNOWN" == 0 ]]; then PANEL_PORT="$v"; PORT_KNOWN=1; fi
  v=$(sed -n 's|^Environment=WUI_BASE_PATH=/\{0,1\}\(.*\)|\1|p' "$UNIT" | head -1)
  v="${v%/}"
  if [[ -n "$v" && "$BASE_KNOWN" == 0 ]]; then BASE_PATH="$v"; BASE_KNOWN=1; fi
}

# Say plainly what a re-run does, because the wrong idea about it is the one
# that causes real damage.
#
# A panel on this machine is upgraded in place: same port, same path, same data.
# The installer cannot produce a second panel and should not — two on one
# machine overwrite each other's firewall rules with neither noticing, and the
# panel refuses to start rather than let that happen.
#
# The mistake worth heading off is "I will add a node to this server". A node is
# another panel; it belongs on another machine.
announce_existing() {
  [[ -f "$UNIT" ]] || return 0

  local state="installed"
  if have_systemd && systemctl is-active --quiet wui.service 2>/dev/null; then
    state="installed and running"
  fi

  tty_out '\n%s%sA panel is already %s on this machine.%s\n' "$B" "$Y" "$state" "$N"
  tty_out '%sThis will upgrade it, keeping its port, its path and its data.%s\n' "$D" "$N"
  tty_out '%sIt cannot add a second panel: two on one machine overwrite each%s\n' "$D" "$N"
  tty_out "%sother's firewall rules, so the second one refuses to start. A node%s\n" "$D" "$N"
  tty_out '%sis another panel and belongs on another machine.%s\n\n' "$D" "$N"
}

configure() {
  open_tty
  read_existing
  announce_existing

  if [[ "$INTERACTIVE" != 1 ]]; then
    # Said, not silently assumed. An operator who piped this from a file and
    # expected questions should learn that here, not at the summary.
    step "Configuration"
    info "no terminal to ask on — using flags, environment and defaults"
    configure_defaults
    return 0
  fi

  step "Setup"
  info "a few questions, then the install runs on its own"
  tty_out '\n'

  # ── the port ──────────────────────────────────────────────────────────────
  #
  # Random unless the operator wants otherwise. A panel on a port that is
  # written down in a README is a panel that gets found by anyone sweeping the
  # address space for one, and what they find is a form to guess passwords at.
  local p
  if [[ "$PORT_KNOWN" == 1 ]]; then
    info "keeping the port this install already uses: $PANEL_PORT"
  elif ask_yn "Choose the panel port yourself? (otherwise a random one is used)" n; then
    while true; do
      ask p "Panel port" "$PANEL_PORT"
      if [[ ! "$p" =~ ^[0-9]+$ ]] || (( p < 1 || p > 65535 )); then
        warn "a port is a number from 1 to 65535"
        continue
      fi
      if port_taken "$p"; then
        # Never offered as something to take over. Whatever is listening there
        # belongs to somebody, and this installer's job is to fit around it.
        warn "port $p is already served by $(port_owner "$p") — pick another"
        continue
      fi
      PANEL_PORT="$p"
      break
    done
  else
    PANEL_PORT="$(random_free_port)"
    info "panel port: $PANEL_PORT"
  fi

  # ── the path it answers on ────────────────────────────────────────────────
  #
  # The other half of not being found. With a random prefix there is nothing at
  # the root of this address at all: every path outside it answers 404,
  # including the sign-in page. It is not a replacement for the password — it
  # is what stops the password being the only thing in the way.
  if [[ "$BASE_KNOWN" == 1 ]]; then
    info "keeping the path this install already uses: /$BASE_PATH/"
  elif ask_yn "Choose the panel's URL path yourself? (otherwise a random one is used)" n; then
    while true; do
      ask BASE_PATH "URL path (one segment, blank for none)" ""
      # Surrounding slashes are how people write a path and are simply
      # dropped. A slash in the middle is a different path from the one
      # typed, so it is refused rather than quietly flattened -- an
      # operator who is handed a path they did not type will write down
      # the one they did.
      BASE_PATH="${BASE_PATH#/}"
      BASE_PATH="${BASE_PATH%/}"
      if [[ -z "$BASE_PATH" ]]; then
        warn "no path — the panel answers at the root, where anyone can find it"
        break
      fi
      if [[ "$BASE_PATH" == api ]]; then
        warn "\"api\" is where the API already answers; pick another"
        continue
      fi
      if [[ ! "$BASE_PATH" =~ ^[A-Za-z0-9._~-]+$ ]]; then
        warn "one segment of letters, digits and - _ . ~ — no slashes or spaces"
        continue
      fi
      break
    done
  else
    BASE_PATH="$(gen_string 18)"
    info "panel path: /$BASE_PATH/"
  fi

  # ── who signs in ──────────────────────────────────────────────────────────
  #
  # Generated unless asked otherwise, name included. "admin" is half of every
  # credential-stuffing attempt ever made against a panel, and a name nobody
  # can guess costs nothing to keep in the same place as the password.
  if ask_yn "Choose the administrator name and password yourself? (otherwise both are generated)" n; then
    ask ADMIN_USER "Administrator username" "${ADMIN_USER:-admin}"
    ask_secret ADMIN_PASS "Administrator password (blank to generate one)"
    [[ -n "$ADMIN_PASS" ]] || info "a password will be generated and shown at the end"
  else
    [[ -n "$ADMIN_USER" ]] || ADMIN_USER="$(gen_string 10)"
    info "administrator: $ADMIN_USER"
    info "the password is generated and shown once, at the end"
  fi

  # ── how it is reached ─────────────────────────────────────────────────────────────────────────────────────────────────────────
  tty_out '\n'
  info "How should the panel be reached?"
  info "  1) a domain name, with a free certificate from Let's Encrypt"
  info "  2) certificate files you already have"
  info "  3) plain HTTP — no certificate"
  tty_out '\n'
  warn "option 3 sends your password across the network in the clear"

  local choice
  while true; do
    ask choice "Choose" "1"
    case "$choice" in
      1|2|3) break ;;
      *) warn "answer 1, 2 or 3" ;;
    esac
  done

  case "$choice" in
    1)
      TLS_MODE=acme
      # Blank is a way out, not a mistake to be corrected. An operator who
      # picked this option and then realised the DNS is not ready yet should
      # be able to finish the install, not be held at a question whose only
      # valid answer they do not have.
      while true; do
        ask ACME_DOMAIN "Domain pointing at this server (blank: no certificate)" "${ACME_DOMAIN:-}"
        if [[ -z "$ACME_DOMAIN" ]]; then
          warn "no domain — the panel will serve plain HTTP"
          TLS_MODE=none
          break
        elif [[ ! "$ACME_DOMAIN" =~ ^[A-Za-z0-9._-]+\.[A-Za-z]{2,}$ ]]; then
          warn "that does not look like a domain name"
        else
          break
        fi
      done
      [[ "$TLS_MODE" == acme ]] && ask ACME_EMAIL "Email for expiry notices (optional)" "${ACME_EMAIL:-}"
      ;;
    2)
      TLS_MODE=files
      while true; do
        ask TLS_CERT "Path to the certificate (fullchain .crt or .pem)" "${TLS_CERT:-}"
        [[ -s "$TLS_CERT" ]] && break
        warn "no readable file at that path"
      done
      while true; do
        ask TLS_KEY "Path to the private key" "${TLS_KEY:-}"
        [[ -s "$TLS_KEY" ]] && break
        warn "no readable file at that path"
      done
      ;;
    3)
      TLS_MODE=none
      ;;
  esac

  # Asked wherever the panel ends up on plain HTTP, not only when that was
  # picked outright -- choosing a domain and then leaving it blank lands in
  # the same place and deserves the same question.
  #
  # A panel on a public address over plain HTTP puts an administrator's
  # password on the wire. Bound to the loopback it is reachable only through
  # an SSH tunnel or a proxy on this machine, which is the one way serving
  # plain HTTP is defensible at all.
  if [[ "$TLS_MODE" == none && "$LISTEN_ADDR" != 127.0.0.1 ]]; then
    tty_out '\n'
    if ask_yn "Bind it to 127.0.0.1 only? (reachable via an SSH tunnel or a local proxy)" n; then
      LISTEN_ADDR=127.0.0.1
    fi
  fi

  # ── what is installed alongside ───────────────────────────────────────────
  tty_out '\n'
  if ask_yn "Install OpenVPN as well as WireGuard?" y; then WANT_OPENVPN=1; else WANT_OPENVPN=0; fi
  if ask_yn "Install AmneziaWG (obfuscated WireGuard)?" y; then WANT_AMNEZIA=1; else WANT_AMNEZIA=0; fi

  # ── read it back ──────────────────────────────────────────────────────────
  tty_out '\n'
  info "Panel port     $PANEL_PORT"
  info "Administrator  $ADMIN_USER"
  local shown_path="/"
  [[ -n "$BASE_PATH" ]] && shown_path="/$BASE_PATH/"
  case "$TLS_MODE" in
    acme)  info "Address        https://$ACME_DOMAIN:$PANEL_PORT$shown_path  (certificate from Let's Encrypt)" ;;
    files) info "Address        https://<your host>:$PANEL_PORT$shown_path  (your own certificate)" ;;
    none)  info "Address        http://$([[ "$LISTEN_ADDR" == 127.0.0.1 ]] && echo 127.0.0.1 || echo '<this server>'):$PANEL_PORT$shown_path  (no certificate)" ;;
  esac
  local extras="WireGuard"
  [[ "$WANT_AMNEZIA" == 1 ]] && extras="AmneziaWG, $extras"
  [[ "$WANT_OPENVPN" == 1 ]] && extras="OpenVPN, $extras"
  info "Installing     $extras"
  tty_out '\n'

  ask_yn "Start the install with these?" y || die "cancelled — nothing was changed"
}

# The same decisions, made without a terminal.
configure_defaults() {
  # The same decisions the questions would have reached, made without a
  # terminal: unguessable unless something was passed in.
  ADMIN_USER="${ADMIN_USER:-$(gen_string 10)}"
  [[ "$BASE_KNOWN" == 1 ]] || BASE_PATH="$(gen_string 18)"
  if [[ "$PORT_KNOWN" == 0 ]]; then
    PANEL_PORT="$(random_free_port)"
  elif port_taken "$PANEL_PORT"; then
    die "port $PANEL_PORT is already served by $(port_owner "$PANEL_PORT"); pass --port with a free one"
  fi
  if [[ -n "$TLS_MODE" ]]; then
    : # --no-tls, or a mode already chosen
  elif [[ -n "$ACME_DOMAIN" ]]; then
    TLS_MODE=acme
  elif [[ -n "$TLS_CERT" && -n "$TLS_KEY" ]]; then
    TLS_MODE=files
  else
    TLS_MODE=none
  fi
}

# ── certificates ─────────────────────────────────────────────────────────────
CERT_DIR="$CONF_DIR/certs"

# Run a command as the panel's own account.
#
# The panel reads its database and its private key as this user, so anything
# this script creates on its behalf is created by it too — otherwise the first
# thing the service does on boot is fail to open a root-owned file.
as_service_user() {
  if have runuser; then
    runuser -u "$SERVICE_USER" -- "$@"
  elif have sudo; then
    sudo -u "$SERVICE_USER" -- "$@"
  else
    # su takes a command string, so the arguments have to be quoted back up.
    local q="" a
    for a in "$@"; do q+="$(printf '%q ' "$a")"; done
    su -s /bin/sh -c "$q" "$SERVICE_USER"
  fi
}

# n random alphanumeric characters. Alphanumeric so the result survives being
# copied out of a terminal, typed into a phone, or pasted through a chat app
# that helpfully reformats punctuation.
gen_string() {
  local n="${1:-16}" out=""
  # Read a fixed number of bytes rather than letting head close the pipe.
  # /dev/urandom is a file, so head reading from it kills nothing, and the
  # length is checked instead of assumed: a password that came out short
  # because a tool was interrupted is a security bug, not a cosmetic one.
  while (( ${#out} < n )); do
    out+="$(head -c 256 /dev/urandom 2>/dev/null | base64 2>/dev/null | LC_ALL=C tr -dc 'A-Za-z0-9')"
    [[ -n "$out" ]] || die "cannot read randomness from /dev/urandom"
  done
  printf '%s' "${out:0:n}"
}

# A high port nothing else is listening on.
#
# Random rather than fixed: a well-known port is the first thing a scanner
# tries. Checked for a listener before it is offered, because a collision here
# would take down whatever was already there.
random_free_port() {
  local p tries=0
  while (( tries++ < 60 )); do
    if have shuf; then p=$(shuf -i 1024-62000 -n 1); else p=$(( (RANDOM << 15 | RANDOM) % 60977 + 1024 )); fi
    port_taken "$p" || { printf '%s' "$p"; return 0; }
  done
  # Sixty collisions in a row is not a busy machine, it is a broken check.
  # Falling back to the prompt's own default beats returning nothing.
  printf '%s' "$PANEL_PORT"
}

gen_password() {
  # 16 characters from a real random source, alphanumeric so it survives being
  # copied out of a terminal, typed into a phone, or pasted through a chat app
  # that helpfully reformats punctuation.
  gen_string 16
}

# This server's address as the internet sees it, for checking that a domain
# actually points here before asking a certificate authority to confirm it.
public_ip() {
  curl -fsS --max-time 8 https://api.ipify.org 2>/dev/null \
    || curl -fsS --max-time 8 https://ifconfig.me/ip 2>/dev/null \
    || hostname -I 2>/dev/null | awk '{print $1}'
}

# Does the domain resolve to this machine?
#
# Not fatal when it does not: a domain behind a proxy, or one whose DNS has not
# propagated yet, both look like this. But it is by far the most common reason
# an ACME challenge fails, and finding out before the certificate authority
# says no saves an operator a confusing five minutes.
domain_points_here() {
  local d="$1" mine resolved=""
  mine="$(public_ip)"
  [[ -n "$mine" ]] || return 0
  if have getent; then
    resolved="$(getent ahostsv4 "$d" 2>/dev/null | awk '{print $1}' | sort -u)"
  elif have dig; then
    resolved="$(dig +short A "$d" 2>/dev/null)"
  else
    return 0
  fi
  [[ -n "$resolved" ]] || return 1
  grep -qx "$mine" <<<"$resolved"
}

setup_tls() {
  case "$TLS_MODE" in
    none)
      step "Certificate"
      warn "none — the panel serves plain HTTP"
      warn "put it behind a reverse proxy, or reach it over an SSH tunnel"
      return 0
      ;;
    files)
      step "Certificate"
      use_existing_cert
      return 0
      ;;
    acme)
      step "Certificate for $ACME_DOMAIN"
      issue_certificate || {
        # A failed certificate must not be a failed install. Everything else
        # works; the panel comes up on HTTP and the operator can re-run the
        # certificate step from the menu once the cause is fixed.
        warn "continuing without a certificate — the panel will serve plain HTTP"
        warn "fix the cause, then re-run this installer and choose a domain again"
        TLS_MODE=none
        TLS_CERT=""; TLS_KEY=""
      }
      return 0
      ;;
  esac
}

# A certificate the operator already has.
#
# Left where it is rather than copied, so whatever renews it keeps working. The
# only thing checked is that the panel's own account can read it, because a key
# under /etc/letsencrypt/live is root-only by default and the service does not
# run as root.
use_existing_cert() {
  local ok_cert=1 ok_key=1
  as_service_user test -r "$TLS_CERT" 2>/dev/null || ok_cert=0
  as_service_user test -r "$TLS_KEY" 2>/dev/null || ok_key=0

  if [[ "$ok_cert" == 1 && "$ok_key" == 1 ]]; then
    ok "using $TLS_CERT"
    ok "the service account can read both files"
    return 0
  fi

  warn "$SERVICE_USER cannot read the certificate, so the panel could not start with it"
  install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$CERT_DIR"
  install -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0644 "$TLS_CERT" "$CERT_DIR/panel.crt"
  install -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0600 "$TLS_KEY" "$CERT_DIR/panel.key"
  TLS_CERT="$CERT_DIR/panel.crt"
  TLS_KEY="$CERT_DIR/panel.key"
  ok "copied into $CERT_DIR"
  warn "this is a copy: when the certificate is renewed, copy it again and"
  warn "  restart the panel, or grant $SERVICE_USER read access to the original"
}

# A free certificate from Let's Encrypt, over the HTTP-01 challenge.
#
# The challenge needs port 80 for a few seconds. If another project is already
# serving on 80 this does not stop it — it asks for that server's document root
# and drops the challenge file there instead, which is the one way to get a
# certificate without interrupting a site that is already running.
issue_certificate() {
  if ! domain_points_here "$ACME_DOMAIN"; then
    warn "$ACME_DOMAIN does not resolve to this server's address"
    warn "  the certificate authority has to reach it here to issue anything"
    if [[ "$INTERACTIVE" == 1 ]]; then
      ask_yn "Try anyway?" n || return 1
    else
      warn "trying anyway"
    fi
  fi

  # A machine that already has a web server in front is not a machine to take
  # port 80 from. Going through the proxy is better in every direction: no
  # outage for the sites already on it, one certificate manager instead of two,
  # and a panel that is not exposed on a public port at all.
  if [[ "$(proxy_on_80 2>/dev/null)" == nginx ]]; then
    info "nginx is already serving port 80 on this machine"
    info "the panel will sit behind it rather than taking the port"
    issue_via_nginx && return 0
    warn "could not put the panel behind nginx"
    return 1
  fi

  local method="standalone" webroot=""
  if port_taken 80; then
    local who; who="$(port_owner 80)"
    warn "port 80 is already served by $who"
    info "that service is left running — the challenge can go through it instead"
    if [[ "$INTERACTIVE" == 1 ]]; then
      info "give the document root it serves for $ACME_DOMAIN, or leave blank to give up"
      ask webroot "Document root" ""
      [[ -n "$webroot" && -d "$webroot" ]] || {
        warn "no usable document root — skipping the certificate"
        return 1
      }
      method="webroot"
    else
      warn "no terminal to ask for a document root — skipping the certificate"
      return 1
    fi
  fi

  # Only the standalone challenge needs it, and only now that we know that is
  # the route being taken.
  [[ "$method" == standalone ]] && { pkg_install socat || warn "socat is missing; the standalone challenge may not work"; }

  # A fixed home rather than $HOME. Under `sudo bash` the environment often
  # still carries the calling user's home, so acme.sh would install itself
  # into their directory while its renewal cron runs as root — and a later
  # run of this installer would not find it there.
  local acme_home
  acme_home="$(getent passwd root 2>/dev/null | cut -d: -f6)"
  acme_home="${acme_home:-/root}/.acme.sh"
  local acme="$acme_home/acme.sh"

  if [[ ! -x "$acme" ]]; then
    info "installing acme.sh"
    curl -fsSL --max-time 60 https://get.acme.sh -o /tmp/get-acme.sh || {
      warn "could not download acme.sh"; return 1; }
    ( HOME="${acme_home%/.acme.sh}" sh /tmp/get-acme.sh --home "$acme_home" ${ACME_EMAIL:+--accountemail "$ACME_EMAIL"} >/dev/null 2>&1 )
    rm -f /tmp/get-acme.sh
  fi
  [[ -x "$acme" ]] || { warn "acme.sh is not installed"; return 1; }

  # Let's Encrypt by name. acme.sh defaults to a different authority, and an
  # operator who was told "Let's Encrypt" should get Let's Encrypt.
  "$acme" --set-default-ca --server letsencrypt >/dev/null 2>&1

  info "asking Let's Encrypt for a certificate (this takes a moment)"
  local out
  if [[ "$method" == webroot ]]; then
    out=$("$acme" --issue -d "$ACME_DOMAIN" --webroot "$webroot" --keylength ec-256 2>&1) || true
  else
    out=$("$acme" --issue -d "$ACME_DOMAIN" --standalone --keylength ec-256 2>&1) || true
  fi

  if ! "$acme" --list 2>/dev/null | grep -q "^$ACME_DOMAIN"; then
    warn "the certificate was not issued"
    # The operator needs the authority's own words, not a summary of them.
    printf '%s\n' "$out" | tail -12 | sed 's/^/      /'
    return 1
  fi

  install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$CERT_DIR"
  "$acme" --install-cert -d "$ACME_DOMAIN" --ecc \
    --fullchain-file "$CERT_DIR/panel.crt" \
    --key-file "$CERT_DIR/panel.key" \
    --reloadcmd "chown $SERVICE_USER:$SERVICE_USER $CERT_DIR/panel.crt $CERT_DIR/panel.key; chmod 640 $CERT_DIR/panel.key; systemctl restart wui 2>/dev/null || true" \
    >/dev/null 2>&1 || { warn "the certificate was issued but could not be installed"; return 1; }

  chown "$SERVICE_USER:$SERVICE_USER" "$CERT_DIR/panel.crt" "$CERT_DIR/panel.key"
  chmod 640 "$CERT_DIR/panel.key"
  chmod 644 "$CERT_DIR/panel.crt"

  TLS_CERT="$CERT_DIR/panel.crt"
  TLS_KEY="$CERT_DIR/panel.key"
  ACME_METHOD="$method"

  ok "issued and installed"
  ok "renewal is automatic; the panel restarts itself when it happens"
  if [[ "$method" == standalone ]]; then
    # Renewal binds port 80 again in sixty days. An operator who closes it, or
    # who later puts a web server there, gets an expired certificate and no
    # warning — so it is said once, now, while it can still be written down.
    warn "renewal needs port 80 free again in ~60 days"
  fi
  return 0
}

# ── sitting behind a proxy that is already here ──────────────────────────────
# A server that already runs nginx on port 80 and 443 is the common case, not
# the exception: it has other sites on it, a certbot that renews them, and an
# operator who will not thank anyone for a panel that took port 80 away to run
# its own ACME challenge.
#
# So when nginx is found holding port 80, the panel does not compete with it. It
# binds to localhost, a new server block is added for the panel's domain, and
# the certificate is obtained with the certbot that is already managing every
# other certificate on the machine. Nothing that was already configured is read,
# rewritten or reloaded out from under itself -- one new file, checked before it
# is allowed to take effect.

# Which reverse proxy, if any, owns port 80.
proxy_on_80() {
  port_taken 80 || return 1
  case "$(port_owner 80)" in
    nginx) printf 'nginx'; return 0 ;;
    apache2|httpd) printf 'apache'; return 0 ;;
    caddy) printf 'caddy'; return 0 ;;
    *) return 1 ;;
  esac
}

# The panel's own nginx site. Its own file, so removing the panel is removing
# one file and nothing of anybody else's is involved.
NGINX_SITE=""

# Put the panel behind the nginx that is already running.
#
# Returns non-zero without having changed anything that matters if it cannot
# finish, so the caller can fall back to serving plain HTTP rather than leaving
# a half-configured web server behind.
issue_via_nginx() {
  local avail="/etc/nginx/sites-available" enabled="/etc/nginx/sites-enabled"
  if [[ ! -d "$avail" || ! -d "$enabled" ]]; then
    # A distribution that does not use the sites-available layout. conf.d is
    # the other convention and is read by every nginx build.
    avail="/etc/nginx/conf.d"; enabled=""
    [[ -d "$avail" ]] || { warn "cannot find nginx's configuration directory"; return 1; }
  fi

  have certbot || pkg_install certbot python3-certbot-nginx || {
    warn "could not install certbot"; return 1; }
  certbot plugins --non-interactive 2>/dev/null | grep -q '^\* nginx' || {
    warn "certbot has no nginx plugin here; install python3-certbot-nginx"; return 1; }

  local site="$avail/wui-panel"
  [[ "$avail" == */conf.d ]] && site="$avail/wui-panel.conf"

  if [[ -e "$site" ]]; then
    # A previous run's file. Ours to replace; anybody else's would not be
    # called this.
    info "replacing the panel's own nginx site"
  fi

  # Plain HTTP only, on purpose. certbot adds the TLS half itself, the same way
  # it did for every other site here, so renewal keeps working through the same
  # mechanism instead of a second one nobody remembers.
  cat >"$site" <<NGINXSITE
# W-UI panel. Written by the W-UI installer; safe to delete with the panel.
server {
    listen 80;
    listen [::]:80;
    server_name $ACME_DOMAIN;

    # The panel is the only thing on this name, so everything goes through.
    location / {
        proxy_pass         http://127.0.0.1:$PANEL_PORT;
        proxy_http_version 1.1;
        proxy_set_header   Host              \$host;
        proxy_set_header   X-Real-IP         \$remote_addr;
        proxy_set_header   X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto \$scheme;

        # The overview polls, and a configuration download can be slow on a
        # busy node. Neither should be cut off by the proxy.
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;

        # Buffering off: the panel streams its own responses and an operator
        # watching a live figure should see it move.
        proxy_buffering off;
    }

    # QR codes and configuration files.
    client_max_body_size 16m;
}
NGINXSITE

  if [[ -n "$enabled" ]]; then
    ln -sfn "$site" "$enabled/$(basename "$site")"
  fi

  # Checked before it is allowed anywhere near a running web server. A syntax
  # error here would take every other site on this machine down with it, and
  # this installer's whole promise is that it does not do that.
  if ! nginx -t >/tmp/wui-nginx.err 2>&1; then
    warn "the panel's nginx site did not pass nginx -t; removing it"
    sed 's/^/      /' /tmp/wui-nginx.err | head -6
    rm -f "$site"
    [[ -n "$enabled" ]] && rm -f "$enabled/$(basename "$site")"
    nginx -t >/dev/null 2>&1 || warn "nginx was already failing its own config test before this"
    return 1
  fi
  rm -f /tmp/wui-nginx.err

  systemctl reload nginx 2>/dev/null || systemctl restart nginx 2>/dev/null || {
    warn "nginx would not reload"; return 1; }
  NGINX_SITE="$site"
  ok "nginx now serves $ACME_DOMAIN on port 80"

  # certbot is pointed at this one name, so it edits this one server block.
  info "asking certbot for a certificate (this takes a moment)"
  local out
  out=$(certbot --nginx -d "$ACME_DOMAIN" --non-interactive --agree-tos --redirect \
        ${ACME_EMAIL:+-m "$ACME_EMAIL"} ${ACME_EMAIL:+--no-eff-email} 2>&1) || true

  if [[ ! -s "/etc/letsencrypt/live/$ACME_DOMAIN/fullchain.pem" ]]; then
    warn "certbot did not issue a certificate"
    printf '%s\n' "$out" | tail -12 | sed 's/^/      /'
    warn "the panel is still reachable over plain HTTP at http://$ACME_DOMAIN/"
    # The site stays: it works, it just has no TLS yet. Removing it would
    # leave the operator with nothing at all.
    TLS_MODE=proxy_plain
    LISTEN_ADDR=127.0.0.1
    return 0
  fi

  ok "certificate issued; nginx serves https://$ACME_DOMAIN"
  ok "renewal is certbot's, alongside every other certificate on this server"

  # The panel itself speaks plain HTTP to nginx over the loopback and is not
  # reachable from outside at all. Its own TLS support is for servers that have
  # no proxy in front of them.
  TLS_MODE=proxy
  LISTEN_ADDR=127.0.0.1
  TLS_CERT=""; TLS_KEY=""
  return 0
}

# ── the administrator ────────────────────────────────────────────────────────
# Created before the panel first starts, so the account is the one the operator
# chose. The panel generates a random administrator only when it finds none,
# which after this it never does.
apply_admin() {
  step "Administrator account"

  # This installer is safe to re-run, and that has to include the account
  # somebody is already signing in with. Nothing was asked for here, so
  # nothing is changed: generating a fresh password over a working one
  # would lock an operator out of their own panel on an upgrade.
  if [[ -f "$DATA_DIR/wui.db" && -z "$ADMIN_PASS" ]]; then
    ok "existing account left as it is"
    info "to change it:  wui admin reset --username NAME"
    return 0
  fi

  ADMIN_GENERATED=0
  if [[ -z "$ADMIN_PASS" ]]; then
    ADMIN_PASS="$(gen_password)"
    ADMIN_GENERATED=1
  fi

  # Piped, never passed as an argument: an argument is visible in ps to every
  # user on the machine for as long as the command runs.
  if printf '%s' "$ADMIN_PASS" | as_service_user env \
      WUI_DATA_DIR="$DATA_DIR" WUI_DB_SOURCE="$DATA_DIR/wui.db" \
      "$BIN_PATH" admin reset --username "$ADMIN_USER" --password-stdin --quiet; then
    ok "$ADMIN_USER"
  else
    warn "could not create the administrator here"
    warn "  the panel will generate one on first start; find it with:"
    warn "    journalctl -u wui | grep -A6 'First run'"
    ADMIN_PASS=""
  fi
}

install_binary() {
  step "Installing the panel"

  if [[ -n "$LOCAL_BIN" ]]; then
    [[ -f "$LOCAL_BIN" ]] || die "no such file: $LOCAL_BIN"
    install -m 0755 "$LOCAL_BIN" "$BIN_PATH"
    ok "installed from $LOCAL_BIN"

  elif [[ "$FROM_SOURCE" == 1 ]]; then
    if [[ -f go.mod ]]; then
      # Run from a checkout: build what is in front of us, which is what
      # somebody testing a change expects.
      have go || die "--from-source needs Go on PATH"
      info "building (this takes a minute)…"
      CGO_ENABLED=0 go build -ldflags "-s -w" -o "$BIN_PATH" ./cmd/wui \
        || die "build failed"
      chmod 0755 "$BIN_PATH"
      ok "built from source"
    else
      build_from_tarball
    fi

  elif [[ -n "$RELEASE_URL" ]]; then
    info "downloading $RELEASE_URL"
    curl -fsSL "$RELEASE_URL" -o /tmp/wui.new || die "download failed"
    install -m 0755 /tmp/wui.new "$BIN_PATH"
    rm -f /tmp/wui.new
    ok "installed from release"

  else
    # Nothing was asked for, which is what `curl … | bash` looks like. Work it
    # out rather than refusing: a published build if there is one, and the
    # source if there is not.
    #
    # This used to be a `die`, which meant the one-line install printed in the
    # README could not succeed on any machine -- it reached the last step and
    # stopped there having installed WireGuard, OpenVPN and nothing else.
    if release_asset_url; then
      info "downloading $ASSET_URL"
      curl -fsSL "$ASSET_URL" -o /tmp/wui.new || die "download failed"
      install -m 0755 /tmp/wui.new "$BIN_PATH"
      rm -f /tmp/wui.new
      ok "installed from the latest release"
    else
      info "no published build for $ARCH; building from source instead"
      build_from_tarball
    fi
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

  # Two lines or none. An empty WUI_TLS_CERT is not the same as an unset one:
  # the panel refuses to start when only half a pair is present, which is the
  # right answer for a typo and the wrong one for a plain-HTTP install.
  local TLS_ENV="" BASE_ENV=""
  [[ -n "$BASE_PATH" ]] && BASE_ENV="Environment=WUI_BASE_PATH=$BASE_PATH"$'\n'
  if [[ -n "$TLS_CERT" && -n "$TLS_KEY" ]]; then
    TLS_ENV="Environment=WUI_TLS_CERT=$TLS_CERT"$'\n'"Environment=WUI_TLS_KEY=$TLS_KEY"
  fi

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

Environment=WUI_LISTEN=$LISTEN_ADDR:$PANEL_PORT
Environment=WUI_DATA_DIR=$DATA_DIR
Environment=WUI_DB_SOURCE=$DATA_DIR/wui.db
$BASE_ENV$TLS_ENV
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
    warn "    WUI_LISTEN=$LISTEN_ADDR:$PANEL_PORT WUI_DATA_DIR=$DATA_DIR $BIN_PATH"
  fi
}

open_firewall() {
  step "Opening the panel port"

  # A certificate issued through the standalone challenge is renewed the same
  # way, in about sixty days, on a machine nobody is watching. If port 80 is
  # closed by then the renewal fails silently and the panel serves an expired
  # certificate — so the port that made this work stays open.
  # Nothing is opened for a panel that only listens on the loopback: the
  # port is unreachable from outside by design, and a firewall rule for it
  # would be a lie in the operator's own rule list.
  local ports=()
  [[ "$LISTEN_ADDR" == 127.0.0.1 ]] || ports+=("$PANEL_PORT/tcp")
  [[ "$ACME_METHOD" == standalone ]] && ports+=("80/tcp")
  local pr
  if [[ ${#ports[@]} -eq 0 ]]; then
    ok "nothing to open: the panel listens on 127.0.0.1 only"
    return 0
  fi

  if have ufw && ufw status 2>/dev/null | grep -q "Status: active"; then
    for pr in "${ports[@]}"; do
      ufw allow "$pr" >/dev/null 2>&1 && ok "ufw: $pr allowed"
    done
  elif have firewall-cmd && firewall-cmd --state >/dev/null 2>&1; then
    for pr in "${ports[@]}"; do
      firewall-cmd --permanent --add-port="$pr" >/dev/null 2>&1 && ok "firewalld: $pr allowed"
    done
    firewall-cmd --reload >/dev/null 2>&1
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

  if [[ "$fresh" == 1 && -z "$ADMIN_PASS" ]]; then
    # Only reached when creating the account up front did not work. The panel
    # prints its generated password once, to the journal, and stores it
    # nowhere in the clear; fishing it out here saves the operator the search.
    sleep 1
    FIRST_RUN_PW=$(journalctl -u wui --since "-2 min" --no-pager 2>/dev/null \
      | grep -A1 'password' | grep -oE '[A-Za-z0-9_-]{16}' | head -1 || true)
  fi
}

summary() {
  local ip host scheme
  ip=$(public_ip 2>/dev/null || echo "your-server-ip")
  [[ -n "$ip" ]] || ip="your-server-ip"

  # The address that will actually work. A certificate issued for a domain is
  # not valid for the address, so printing the IP there would hand the operator
  # a URL their browser refuses.
  scheme=http; host="$ip"; local port=":$PANEL_PORT"
  if [[ -n "$TLS_CERT" && -n "$TLS_KEY" ]]; then scheme=https; fi
  [[ "$TLS_MODE" == acme && -n "$ACME_DOMAIN" ]] && host="$ACME_DOMAIN"

  # Behind a proxy the panel's own port is not the address anybody uses: nginx
  # answers on 443 for the domain and reaches the panel over the loopback.
  # Printing the panel's port here would hand the operator a URL that is
  # firewalled off from the internet.
  case "$TLS_MODE" in
    proxy)       scheme=https; host="$ACME_DOMAIN"; port="" ;;
    proxy_plain) scheme=http;  host="$ACME_DOMAIN"; port="" ;;
  esac

  printf '\n%s────────────────────────────────────────────────────────────%s\n' "$D" "$N"
  printf '  %sW-UI is installed%s\n\n' "$B" "$N"
  local shown_path="/"
  [[ -n "$BASE_PATH" ]] && shown_path="/$BASE_PATH/"
  printf '  Panel      %s://%s%s%s\n' "$scheme" "$host" "$port" "$shown_path"

  if [[ -n "$ADMIN_PASS" ]]; then
    printf '  Username   %s\n' "$ADMIN_USER"
    printf '  Password   %s%s%s\n' "$B" "$ADMIN_PASS" "$N"
    if [[ "$ADMIN_GENERATED" == 1 ]]; then
      printf '\n  %sThis password was generated and is shown once. Write it down.%s\n' "$Y" "$N"
    fi
  elif [[ -n "${FIRST_RUN_PW:-}" ]]; then
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
  printf '    listening    %s\n' "$([[ "$LISTEN_ADDR" == 127.0.0.1 ]] && echo '127.0.0.1 only — not reachable from outside this server' || echo "$LISTEN_ADDR:$PANEL_PORT")"
  printf '    URL path     %s\n' "$([[ -n "$BASE_PATH" ]] && echo "$shown_path — nothing else on this address answers" || echo 'none (the panel is at the root)')"
  case "$TLS_MODE" in
    acme)  printf "    certificate  Let%ss Encrypt, renews itself\n" "'" ;;
    files) printf '    certificate  yours, at %s\n' "$TLS_CERT" ;;
    proxy) printf '    certificate  certbot, through the nginx already on this server\n' ;;
    proxy_plain) printf '    certificate  %snone yet — nginx serves the panel over plain HTTP%s\n' "$Y" "$N" ;;
    *)     printf '    certificate  %snone — this panel serves plain HTTP%s\n' "$Y" "$N" ;;
  esac

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
# Everything above is definitions. A test suite that only wants those stops
# here, with the file it sourced being byte for byte the file that installs.
if [[ "$LIB_ONLY" == 1 ]]; then return 0 2>/dev/null || exit 0; fi

detect_os
[[ "$ACTION" == install ]] || do_uninstall

printf '\n  %sW-UI installer%s\n' "$B" "$N"
printf '  %s%s · kernel %s · %s%s\n' "$D" "$OS_NAME" "$KERNEL" "$ARCH" "$N"

# Asked first, so the rest runs unattended.
configure

install_base
install_wireguard
if [[ "$WANT_AMNEZIA" == 1 ]]; then install_amnezia; else info "AmneziaWG skipped (--no-amnezia)"; fi
if [[ "$WANT_OPENVPN" == 1 ]]; then install_openvpn; else info "OpenVPN skipped (--no-openvpn)"; fi
enable_forwarding
check_nftables
install_binary
install_menu
create_user
setup_tls
write_unit
apply_admin
open_firewall
start_service
summary
