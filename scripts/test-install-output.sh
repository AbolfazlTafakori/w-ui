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

sed -e '/^# ── run ─/,$d' -e '/^# ── arguments ─/,/^# ── platform ─/d' -e '/^set -euo pipefail$/d' "$SRC" >"$WORK/lib.sh"

pass=0; fail=0
yes_() { if [[ "$2" == *"$1"* ]]; then printf '  \e[32mok\e[0m   %s\n' "$3"; pass=$((pass+1));
         else printf '  \e[31mFAIL\e[0m %s\n       expected to find: %s\n' "$3" "$1"; fail=$((fail+1)); fi; }
no_()  { if [[ "$2" != *"$1"* ]]; then printf '  \e[32mok\e[0m   %s\n' "$3"; pass=$((pass+1));
         else printf '  \e[31mFAIL\e[0m %s\n       should not contain: %s\n' "$3" "$1"; fail=$((fail+1)); fi; }

unit_with() { # unit_with CERT KEY PORT
  (
    set +e
    # shellcheck disable=SC1091
    source "$WORK/lib.sh" >/dev/null 2>&1
    UNIT="$WORK/wui.service"
    TLS_CERT="$1"; TLS_KEY="$2"; PANEL_PORT="$3"
    have_systemd() { return 1; }
    write_unit >/dev/null 2>&1
    cat "$UNIT"
  )
}

echo
echo "── the unit carries the certificate the operator was given ────────────"
u=$(unit_with /etc/wui/certs/panel.crt /etc/wui/certs/panel.key 8443)
yes_ "Environment=WUI_TLS_CERT=/etc/wui/certs/panel.crt" "$u" "certificate path reaches the service"
yes_ "Environment=WUI_TLS_KEY=/etc/wui/certs/panel.key"  "$u" "key path reaches the service"
yes_ "Environment=WUI_LISTEN=0.0.0.0:8443"               "$u" "the chosen port reaches the service"

echo
echo "── and says nothing about TLS when there is none ──────────────────────"
u=$(unit_with "" "" 2096)
no_  "WUI_TLS_CERT" "$u" "no half-configured TLS in the unit"
no_  "WUI_TLS_KEY"  "$u" "no stray key variable"
yes_ "Environment=WUI_LISTEN=0.0.0.0:2096" "$u" "still listens"

echo
echo "── half a pair would stop the panel booting, so it is never written ───"
u=$(unit_with /etc/wui/certs/panel.crt "" 2096)
no_ "WUI_TLS_CERT" "$u" "a certificate with no key is dropped rather than written"

summary_of() { # summary_of MODE DOMAIN CERT PASS GENERATED
  (
    set +e
    # shellcheck disable=SC1091
    source "$WORK/lib.sh" >/dev/null 2>&1
    TLS_MODE="$1"; ACME_DOMAIN="$2"; TLS_CERT="$3"; TLS_KEY="$3"
    ADMIN_PASS="$4"; ADMIN_GENERATED="$5"
    ADMIN_USER=operator; PANEL_PORT=8443; DATA_DIR="$WORK"
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
s=$(summary_of acme panel.example.com /etc/wui/certs/panel.crt "Chosen-Pass-1" 0)
yes_ "https://panel.example.com:8443" "$s" "a domain certificate is advertised on the domain, not the IP"
no_  "https://203.0.113.9:8443"       "$s" "the IP, which the certificate is not valid for, is not offered"
yes_ "operator"                       "$s" "the chosen username"
yes_ "Chosen-Pass-1"                  "$s" "the chosen password"
no_  "shown once"                     "$s" "a password the operator chose is not called generated"

echo
echo "── a generated password is called what it is ──────────────────────────"
s=$(summary_of none "" "" "Rand0mGenerated" 1)
yes_ "http://203.0.113.9:8443" "$s" "no certificate means the IP over plain HTTP"
yes_ "generated and is shown once" "$s" "the operator is told to write it down"
yes_ "serves plain HTTP" "$s" "and told there is no certificate"

echo
echo "── an operator-supplied certificate is reported as theirs ─────────────"
s=$(summary_of files "" /srv/certs/live.pem "p" 0)
yes_ "https://203.0.113.9:8443" "$s" "https on the address"
yes_ "yours, at /srv/certs/live.pem" "$s" "and named as their own file"

echo
echo "── a terminal that closes mid-question stops instead of spinning ──────"
out=$(
  timeout 20 bash -c '
    set -uo pipefail
    source "'"$WORK"'/lib.sh" >/dev/null 2>&1
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

printf '\n  %d passed, %d failed\n\n' "$pass" "$fail"
[[ "$fail" == 0 ]]
