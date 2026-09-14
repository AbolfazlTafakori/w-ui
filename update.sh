#!/usr/bin/env bash
# W-UI update script.
#
# Replaces the panel binary and the management script with a release and
# restarts the service. It asks nothing: the port, the URL path, the
# certificate, the database and the administrator are whatever this install
# already has. Only when a panel has no certificate at all is one offered,
# because a panel on plain HTTP is a panel handing its password to the wire.
#
# Usage:
#   bash <(curl -fsSL https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/update.sh)
#   bash <(curl -fsSL .../update.sh) v1.2.0      a particular version
#   WUI_UPDATE_TAG=dev-latest bash <(curl -fsSL .../update.sh)
#                                               the rolling build of the latest commit
#
# The library of functions is the installer's own, so a download is verified
# the same way, a certificate is issued the same way, and the closing box is
# the same box.
set -eu

REPO="${WUI_REPO:-AbolfazlTafakori/w-ui}"
REF="${WUI_REF:-main}"
TAG="${1:-${WUI_UPDATE_TAG:-}}"
[[ -z "$TAG" || "$TAG" == v* || "$TAG" == dev-latest ]] || TAG="v$TAG"

[[ $EUID -eq 0 ]] || { printf '\n\033[0;31merror:\033[0m run as root (sudo bash %s)\n\n' "$0" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { printf '\n\033[0;31merror:\033[0m curl is required\n\n' >&2; exit 1; }

# Everything the installer knows, without the installer running: the same
# release resolution, checksum check, certificate issue and unit writer.
lib="$(mktemp)"
trap 'rm -f "$lib"' EXIT
curl -fsSL "https://raw.githubusercontent.com/${REPO}/${REF}/install.sh" -o "$lib" \
  || { printf '\n\033[0;31merror:\033[0m could not fetch the installer library from GitHub\n\n' >&2; exit 1; }
# shellcheck disable=SC1090
WUI_LIB_ONLY=1 source "$lib"

detect_os
printf '\n  %sW-UI update%s\n' "$B" "$N"
printf '  %s%s · kernel %s · %s%s\n' "$D" "$OS_NAME" "$KERNEL" "$ARCH" "$N"

[[ -f "$UNIT" || -x "$BIN_PATH" ]] || die "W-UI is not installed on this machine; run install.sh instead"

# ── which build ──────────────────────────────────────────────────────────────
step "Finding the release"
resolve_release() {
  local api json urls
  if [[ -n "$TAG" ]]; then
    api="https://api.github.com/repos/${REPO}/releases/tags/${TAG}"
  else
    api="https://api.github.com/repos/${REPO}/releases/latest"
  fi
  json="$(curl -fsSL --max-time 20 "$api" 2>/dev/null)" || return 1
  urls="$(printf '%s' "$json" | grep -o '"browser_download_url": *"[^"]*"' | sed 's/.*"browser_download_url": *"//; s/"$//')"
  RELEASE_TAG="$(printf '%s' "$json" | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*"tag_name": *"//; s/"$//')"
  ASSET_URL="$(grep -E "linux[-_]${ARCH}$" <<<"$urls" | head -1)"
  SIG_URL="$(grep -E "linux[-_]${ARCH}\.sig$" <<<"$urls" | head -1)"
  SUMS_URL="$(grep -E '/SHA256SUMS$' <<<"$urls" | head -1)"
  MENU_URL="$(grep -E '/w-ui\.sh$' <<<"$urls" | head -1)"
  [[ -n "$ASSET_URL" ]]
}
resolve_release || die "no release ${TAG:-(latest)} with a build for ${ARCH} was found"
current="$("$BIN_PATH" version 2>/dev/null || echo unknown)"
info "installed: $current"
info "available: $RELEASE_TAG"

# ── download and check ───────────────────────────────────────────────────────
step "Downloading W-UI $RELEASE_TAG"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp" "$lib"' EXIT
curl -fsSL --retry 4 --retry-all-errors --retry-delay 3 "$ASSET_URL" -o "$tmp/wui" || die "download failed"
verify_download "$tmp/wui" "${ASSET_URL##*/}"
# The signature, checked with the key baked into the panel already installed.
# A build that does not verify is not installed, whatever the checksum said.
if [[ -n "$SIG_URL" ]] && curl -fsSL --retry 4 --retry-all-errors --retry-delay 3 "$SIG_URL" -o "$tmp/wui.sig" 2>/dev/null; then
  # 0: signed by this project's key. 2: it is not, and it is not installed.
  # Anything else: the installed panel cannot check (no key baked in, or a
  # panel from before this command existed) -- said, and carried on with
  # the checksum alone.
  set +e
  out="$("$BIN_PATH" verify "$tmp/wui" "$tmp/wui.sig" 2>&1)"
  rc=$?
  set -e
  # A panel from before this command existed does not know "verify" and
  # tries to start instead; only an answer that begins "verify:" is one.
  if [[ $rc -eq 0 && "$out" == *"signature ok"* ]]; then
    ok "signature verified"
  elif [[ $rc -eq 2 && "$out" == *"verify:"* ]]; then
    die "signature check failed: the download is not a build this project signed"
  else
    warn "the installed panel cannot check signatures; the checksum is what was verified"
  fi
else
  warn "the release carries no signature for this build"
fi

# ── install ──────────────────────────────────────────────────────────────────
step "Installing"
install -m 0755 "$tmp/wui" "$BIN_PATH.new" && mv -f "$BIN_PATH.new" "$BIN_PATH"
ok "$BIN_PATH"
if [[ -n "${MENU_URL:-}" ]] && curl -fsSL --retry 4 --retry-all-errors --retry-delay 3 "$MENU_URL" -o "$tmp/w-ui.sh"; then
  sed 's/\r$//' "$tmp/w-ui.sh" > "$tmp/w-ui.clean" && install -m 0755 "$tmp/w-ui.clean" "$MENU_PATH"
  ok "$MENU_PATH"
else
  warn "could not refresh the w-ui command; the one installed stays"
fi
if have_systemd; then
  systemctl daemon-reload
  systemctl restart wui.service
  for _ in $(seq 1 20); do systemctl is-active --quiet wui.service && break; sleep 0.5; done
  systemctl is-active --quiet wui.service || die "the panel did not come back — see: journalctl -u wui -n 50 --no-pager"
  ok "wui.service restarted"
fi

# ── after the update ─────────────────────────────────────────────────────────
# What the panel answers on, read back; a missing URL path or certificate is
# put right, since neither is something a panel should be without.
step "Settings"
read_existing
# The panel's own environment -- the unit's lines, then db.env and wui.env --
# so what is read back is what the service runs with, not the defaults.
env_of() {
  (
    set -a
    WUI_DATA_DIR="$DATA_DIR"
    for kv in $(grep -oE 'WUI_[A-Z_]+=[^ ]+' "$UNIT" 2>/dev/null); do export "$kv"; done
    [[ -r "$CONF_DIR/db.env" ]] && . "$CONF_DIR/db.env"
    [[ -r "$CONF_DIR/wui.env" ]] && . "$CONF_DIR/wui.env"
    set +a
    "$BIN_PATH" "$@"
  )
}
env_of setting show 2>/dev/null | sed 's/^/    /' || true

base="$(env_of setting show 2>/dev/null | sed -n 's/^basePath: //p' | tr -d '/')"
[[ -n "$base" ]] || base="$BASE_PATH"
if (( ${#base} < 4 )); then
  BASE_PATH="$(gen_string 18)"
  warn "the URL path is missing or too short; a new one is set: /$BASE_PATH/"
  env_of setting set --base-path "/$BASE_PATH/" >/dev/null 2>&1 || true
  have_systemd && systemctl restart wui.service
else
  BASE_PATH="$base"
fi

if [[ -z "$TLS_CERT" || -z "$TLS_KEY" ]]; then
  printf '\n  %s═══════════════════════════════════════════%s\n' "$R" "$N"
  printf '  %s      ⚠ NO SSL CERTIFICATE DETECTED ⚠     %s\n' "$R" "$N"
  printf '  %s═══════════════════════════════════════════%s\n' "$R" "$N"
  printf '  %sFor security, a certificate is strongly recommended for every panel.%s\n' "$Y" "$N"
  printf "  %sLet's Encrypt supports both domains and IP addresses.%s\n\n" "$Y" "$N"
  open_tty
  if [[ "$INTERACTIVE" == 1 ]]; then
    ask_tls
    setup_tls
    if [[ -n "$TLS_CERT" && -n "$TLS_KEY" ]]; then
      write_unit
      have_systemd && systemctl restart wui.service
      # The subscription service on its own port gets the same certificate.
      env_of setting set --sub-cert "$TLS_CERT" --sub-key "$TLS_KEY" >/dev/null 2>&1 || true
    fi
  else
    warn "no terminal to ask on; get one later with: w-ui → 20"
  fi
else
  ok "certificate: $TLS_CERT"
fi

ip="$(public_ip 2>/dev/null || true)"
host="${ip:-<this server>}"
case "$TLS_MODE" in
  acme) [[ -n "${ACME_DOMAIN:-}" ]] && host="$ACME_DOMAIN" ;;
  files) [[ -n "${TLS_DOMAIN:-}" ]] && host="$TLS_DOMAIN" ;;
esac
if [[ -n "$TLS_CERT" ]]; then
  # The folder a certificate lives in is named for what it was issued to.
  n="$(basename "$(dirname "$TLS_CERT")")"
  [[ "$n" =~ ^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ || "$n" =~ ^[0-9.]+$ ]] && host="$n"
  scheme=https
else
  scheme=http
fi
printf '\n  %s═══════════════════════════════════════════%s\n' "$G" "$N"
printf '  %s     Panel Access Information              %s\n' "$G" "$N"
printf '  %s═══════════════════════════════════════════%s\n' "$G" "$N"
printf '  %sAccess URL:  %s://%s:%s/%s/%s\n' "$G" "$scheme" "$host" "$PANEL_PORT" "$BASE_PATH" "$N"
printf '  %s═══════════════════════════════════════════%s\n' "$G" "$N"

setup_fail2ban
printf '\n'
usage_box updating
