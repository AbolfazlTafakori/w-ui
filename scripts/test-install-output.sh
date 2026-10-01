#!/usr/bin/env bash
# What the installer writes and what it tells the operator afterwards.
#
# The unit file and the closing summary are the two things an operator actually
# depends on: one decides whether the panel starts with the certificate it was
# given, the other is the only place the address and the password are ever
# shown. Both are checked here against a temporary directory.
set -uo pipefail

SRC="${1:?usage: test-output.sh /path/to/install.sh}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

cp "$SRC" "$WORK/lib.sh"

pass=0; fail=0
yes_() { if [[ "$2" == *"$1"* ]]; then printf '  \e[32mok\e[0m   %s\n' "$3"; pass=$((pass+1));
         else printf '  \e[31mFAIL\e[0m %s\n       expected to find: %s\n' "$3" "$1"; fail=$((fail+1)); fi; }
no_()  { if [[ "$2" != *"$1"* ]]; then printf '  \e[32mok\e[0m   %s\n' "$3"; pass=$((pass+1));
         else printf '  \e[31mFAIL\e[0m %s\n       should not contain: %s\n' "$3" "$1"; fail=$((fail+1)); fi; }

unit_with() { # unit_with CERT KEY PORT [BASE]
  (
    set +e
    # shellcheck disable=SC1091
    WUI_LIB_ONLY=1 source "$WORK/lib.sh" >/dev/null 2>&1
    UNIT="$WORK/wui.service"
    TLS_CERT="$1"; TLS_KEY="$2"; PANEL_PORT="$3"; BASE_PATH="${4:-}"; LISTEN_ADDR="${5:-0.0.0.0}"
    have_systemd() { return 1; }
    write_unit >/dev/null 2>&1
    cat "$UNIT"
  )
}

echo
echo "── the unit carries the certificate the operator was given ────────────"
u=$(unit_with /etc/wui/certs/panel.crt /etc/wui/certs/panel.key 8443 hiddenPath9)
yes_ "Environment=WUI_TLS_CERT=/etc/wui/certs/panel.crt" "$u" "certificate path reaches the service"
yes_ "Environment=WUI_TLS_KEY=/etc/wui/certs/panel.key"  "$u" "key path reaches the service"
yes_ "Environment=WUI_LISTEN=0.0.0.0:8443"               "$u" "the chosen port reaches the service"
yes_ "Environment=WUI_BASE_PATH=hiddenPath9"             "$u" "the URL path reaches the service"

echo
echo "── and says nothing about TLS when there is none ──────────────────────"
u=$(unit_with "" "" 2096 "")
no_  "WUI_TLS_CERT" "$u" "no half-configured TLS in the unit"
no_  "WUI_TLS_KEY"  "$u" "no stray key variable"
yes_ "Environment=WUI_LISTEN=0.0.0.0:2096" "$u" "still listens"
no_  "WUI_BASE_PATH" "$u" "no path variable when the panel is at the root"

echo
echo "── half a pair would stop the panel booting, so it is never written ───"
u=$(unit_with /etc/wui/certs/panel.crt "" 2096)
no_ "WUI_TLS_CERT" "$u" "a certificate with no key is dropped rather than written"


summary_of() { # summary_of MODE DOMAIN CERT PASS GENERATED [BASE]
  (
    set +e
    # shellcheck disable=SC1091
    WUI_LIB_ONLY=1 source "$WORK/lib.sh" >/dev/null 2>&1
    TLS_MODE="$1"; ACME_DOMAIN="$2"; TLS_CERT="$3"; TLS_KEY="$3"
    ADMIN_PASS="$4"; ADMIN_GENERATED="$5"
    ADMIN_USER=operator; PANEL_PORT=8443; DATA_DIR="$WORK"; BASE_PATH="${6:-}"
    LISTEN_ADDR="${7:-0.0.0.0}"; ACME_METHOD=""
    public_ip() { printf '203.0.113.9'; }
    have_systemd() { return 0; }
    have() { return 1; }
    nft() { printf 'nftables v1.0.9\n'; }
    sysctl() { printf '1\n'; }
    summary 2>&1
  )
}

echo
echo "── the summary prints an address that actually works ──────────────────"
s=$(summary_of acme panel.example.com /etc/wui/certs/panel.crt "Chosen-Pass-1" 0 s3cretPath)
# The path is half the address. A summary that prints the host and port but
# not the prefix hands the operator a URL that answers 404.
yes_ "https://panel.example.com:8443/s3cretPath/" "$s" "the address includes the path the panel is actually at"
no_  "https://203.0.113.9:8443"       "$s" "the IP, which the certificate is not valid for, is not offered"
yes_ "operator"                       "$s" "the chosen username"
yes_ "Chosen-Pass-1"                  "$s" "the chosen password"
no_  "shown once"                     "$s" "a password the operator chose is not called generated"

echo
echo "── a generated password is called what it is ──────────────────────────"
s=$(summary_of none "" "" "Rand0mGenerated" 1)
yes_ "http://203.0.113.9:8443/" "$s" "no certificate means the IP over plain HTTP"
yes_ "none (the panel is at the root)" "$s" "and says so when there is no path to hide behind"
yes_ "generated and is shown once" "$s" "the operator is told to write it down"
yes_ "serves plain HTTP" "$s" "and told there is no certificate"

echo
echo "── an operator-supplied certificate is reported as theirs ─────────────"
s=$(summary_of files "" /srv/certs/live.pem "p" 0)
yes_ "https://203.0.113.9:8443" "$s" "https on the address"
yes_ "yours, at /srv/certs/live.pem" "$s" "and named as their own file"

echo
echo "── a certificate for the address itself: https, on the one port ───────"
s=$(ACME_IP=203.0.113.9 summary_of ip "" /etc/wui/certs/ip/fullchain.pem "p" 0 secretPath)
yes_ "https://203.0.113.9:8443/secretPath/" "$s" "https on the address, on the panel's own port"
yes_ "6 days, renews itself" "$s" "and the operator is told it is a six-day certificate that renews"

echo
echo "── plain HTTP on the loopback is said to be reachable only from here ───"
u=$(unit_with "" "" 39001 secretPath 127.0.0.1)
yes_ "Environment=WUI_LISTEN=127.0.0.1:39001" "$u" "the service binds the loopback, not every interface"
no_  "WUI_TLS_CERT" "$u" "and terminates no TLS of its own"
yes_ "ExecReload=/bin/kill -HUP" "$u" "reload restarts the tunnels without stopping the panel"
s=$(summary_of none "" "" "p" 0 secretPath 127.0.0.1)
yes_ "127.0.0.1 only" "$s" "the operator is told it listens on the loopback"

fw() {
  (
    set +e
    # shellcheck disable=SC1091
    WUI_LIB_ONLY=1 source "$WORK/lib.sh" >/dev/null 2>&1
    LISTEN_ADDR="$1"; PANEL_PORT=39001; ACME_METHOD="${2:-}"
    have() { return 1; }
    open_firewall 2>&1
  )
}
echo
echo "── the firewall is not asked to open a port nothing can reach ─────────"
yes_ "the panel listens on 127.0.0.1 only" "$(fw 127.0.0.1)" "nothing is opened for a loopback-only panel"
no_  "39001" "$(fw 127.0.0.1)" "and its port is never named as open"
yes_ "39001" "$(fw 0.0.0.0)" "a public panel still gets its port opened"

echo
echo "── a terminal that closes mid-question stops instead of spinning ──────"
out=$(
  timeout 20 bash -c '
    set -uo pipefail
    WUI_LIB_ONLY=1 source "'"$WORK"'/lib.sh" >/dev/null 2>&1
    : >"'"$WORK"'/empty"
    open_tty() { INTERACTIVE=1; exec 3<"'"$WORK"'/empty"; }
    have() { return 1; }
    port_taken() { return 1; }
    configure 2>&1 | tail -2
  '
)
rc=$?
if [[ $rc == 124 ]]; then
  printf '  \e[31mFAIL\e[0m an exhausted terminal hangs the installer\n'; fail=$((fail+1))
else
  yes_ "the terminal closed" "$out" "an exhausted terminal ends the install with a reason"
fi


echo
echo "── an update keeps what the install handed out ────────────────────────"
# update_run SETUP CALLS: a run of the installer's own functions over a
# temporary /etc/wui and unit, with the panel binary replaced by one that
# writes down what it is asked to do. SETUP is "update" (a unit is there, as
# every release's install leaves one) or "fresh"; RESULT is "keep" (an
# install-result.env from the first install is there) or "none".
update_run() { # update_run fresh|update keep|none
  (
    set +e
    # shellcheck disable=SC1091
    WUI_LIB_ONLY=1 source "$WORK/lib.sh" >/dev/null 2>&1
    # The installer's own set -eu came with it; what is checked here is what
    # each function leaves behind, missing files included.
    set +eu
    dir="$WORK/upd-$1-$2"; rm -rf "$dir"; mkdir -p "$dir/etc"
    UNIT="$dir/wui.service"; CONF_DIR="$dir/etc"; DATA_DIR="$dir/data"
    BIN_PATH="$dir/wui"
    printf '#!/usr/bin/env bash\necho "$*" >> "%s/calls"\necho wui_minted_token\n' "$dir" > "$BIN_PATH"
    chmod +x "$BIN_PATH"
    have_systemd() { return 0; }
    systemctl() { return 0; }
    # shellcheck disable=SC2034 # read by the installer's functions
    open_tty() { INTERACTIVE=0; }
    if [[ "$1" == update ]]; then
      # As v2.5.2 and v1.1.0 left it: the unit, and the file the install wrote.
      printf '[Service]\nEnvironment=WUI_LISTEN=0.0.0.0:2053\nEnvironment=WUI_DATA_DIR=%s\n' "$DATA_DIR" > "$UNIT"
    fi
    if [[ "$2" == keep ]]; then
      printf "WUI_USERNAME=admin\nWUI_PASSWORD='first-install-pass'\nWUI_API_TOKEN=wui_from_the_install\nWUI_SUB_PORT=2096\n" > "$CONF_DIR/install-result.env"
      chmod 600 "$CONF_DIR/install-result.env"
    fi
    before=$(cat "$CONF_DIR/install-result.env" 2>/dev/null)
    if [[ "$1" == update ]]; then configure >/dev/null 2>&1; fi
    issue_api_token
    # shellcheck disable=SC2034 # read by write_install_result
    { ADMIN_USER="admin"; PANEL_PORT=2053; SUB_PORT=2096; DB_DRIVER=sqlite; }
    note=$(write_install_result http 203.0.113.7:2053 2>&1)
    echo "is_update=$IS_UPDATE"
    echo "minted=$(cat "$dir/calls" 2>/dev/null | grep -c 'token issue')"
    echo "api_token=$API_TOKEN"
    echo "note=$note"
    if [[ -f "$CONF_DIR/install-result.env" ]]; then
      [[ "$(cat "$CONF_DIR/install-result.env")" == "$before" ]] && echo "file=unchanged" || echo "file=rewritten"
      echo "mode=$(stat -c %a "$CONF_DIR/install-result.env" 2>/dev/null)"
      cat "$CONF_DIR/install-result.env"
    else
      echo "file=absent"
    fi
  )
}

u=$(update_run update keep)
yes_ "is_update=1"   "$u" "a unit already here makes the run an update"
yes_ "minted=0"      "$u" "an update mints no API token"
yes_ "api_token="$'\n' "$u" "and holds none to print"
yes_ "file=unchanged" "$u" "an update leaves install-result.env as the install wrote it"
yes_ "first-install-pass" "$u" "the password in it is kept"
yes_ "wui_from_the_install" "$u" "the token in it is the install's"
yes_ "kept as it was" "$u" "and says it kept it"
if [[ "$(uname -s)" == Linux ]]; then
  yes_ "mode=600" "$u" "with its mode"
fi

u=$(update_run update none)
yes_ "file=absent" "$u" "an install older than the file does not get one made up"
yes_ "older than the file" "$u" "and is told why there is none"
yes_ "minted=0" "$u" "nor a token"

u=$(update_run fresh none)
yes_ "is_update=0" "$u" "no unit: a fresh install"
yes_ "minted=1" "$u" "a fresh install mints one token"
yes_ "api_token=wui_minted_token" "$u" "and holds it"
yes_ "WUI_API_TOKEN=wui_minted_token" "$u" "and writes it to install-result.env"
yes_ "WUI_USERNAME=admin" "$u" "with the administrator"
if [[ "$(uname -s)" == Linux ]]; then
  yes_ "mode=600" "$u" "mode 600"
fi

printf '\n  %d passed, %d failed\n\n' "$pass" "$fail"
[[ "$fail" == 0 ]]
