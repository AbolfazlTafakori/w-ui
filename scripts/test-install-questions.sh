#!/usr/bin/env bash
# Drives the installer's questions with scripted answers.
#
# Everything above the run marker is definitions, so it can be sourced without
# installing anything. The one thing replaced is where the questions read from:
# open_tty normally grabs /dev/tty, and here fd 3 is pointed at a file of
# answers instead, which is the only way to test a prompt without a terminal.
#
# The claim most of this file exists to check: an operator who presses enter
# through every question ends up with an install nobody can find — a random
# port, a random URL path and a random administrator name. That is the whole
# security posture of a default install, and it is one wrong default away from
# being gone.
set -uo pipefail

SRC="${1:?usage: test-install-questions.sh /path/to/install.sh}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Definitions only: the run section is cut, and so is argument parsing — which
# ends in a root check that would exit the moment this is sourced. set -e goes
# too, so a deliberately wrong answer under test does not kill the shell that
# is checking how it was handled.
sed -e '/^# ── run ─/,$d' -e '/^# ── arguments ─/,/^# ── platform ─/d' -e '/^set -euo pipefail$/d' "$SRC" >"$WORK/lib.sh"

pass=0; fail=0
ok_()   { printf '  \e[32mok\e[0m   %s\n' "$1"; pass=$((pass+1)); }
bad_()  { printf '  \e[31mFAIL\e[0m %s\n       %s\n' "$1" "$2"; fail=$((fail+1)); }
check() { if [[ "$2" == "$3" ]]; then ok_ "$1"; else bad_ "$1" "want: $2   got: $3"; fi; }
truth() { if [[ "$2" == 1 ]]; then ok_ "$1"; else bad_ "$1" "$3"; fi; }

field() { grep "^$1=" <<<"$2" | cut -d= -f2-; }

# Run configure with a canned set of answers, then print the decisions.
run_wizard() {
  printf '%s\n' "$1" >"$WORK/answers"
  (
    set +e
    # shellcheck disable=SC1091
    source "$WORK/lib.sh" >/dev/null 2>&1
    eval "${2:-:}"

    # The only substitution: answers come from a file, not a terminal.
    open_tty() { INTERACTIVE=1; exec 3<"$WORK/answers"; }
    # Nothing here is allowed to touch the machine.
    have() { case "$1" in ss|netstat|shuf) return 1 ;; *) command -v "$1" >/dev/null 2>&1 ;; esac; }
    port_taken() { [[ "$1" == "${BUSY_PORT:--1}" ]]; }
    port_owner() { printf 'nginx'; }
    die() { printf 'DIED: %s\n' "$*"; exit 9; }

    configure >/dev/null 2>&1
    printf 'PORT=%s\nUSER=%s\nPASS=%s\nBASE=%s\nMODE=%s\nDOMAIN=%s\nEMAIL=%s\nCERT=%s\nOVPN=%s\nAWG=%s\n' \
      "$PANEL_PORT" "$ADMIN_USER" "$ADMIN_PASS" "$BASE_PATH" "$TLS_MODE" "$ACME_DOMAIN" \
      "$ACME_EMAIL" "$TLS_CERT" "$WANT_OPENVPN" "$WANT_AMNEZIA"
  )
}

# Eight blank lines: port? path? credentials? certificate? domain? openvpn?
# amneziawg? confirm?
ALL_DEFAULT=$'\n\n\n\n\n\n\n\n'

echo
echo "── pressing enter through everything leaves nothing guessable ─────────"
out=$(run_wizard "$ALL_DEFAULT")
port=$(field PORT "$out"); base=$(field BASE "$out"); user=$(field USER "$out")

truth "the port is not the one written in the README" \
      "$([[ "$port" != 2096 ]] && echo 1)" "got $port"
truth "the port is a real high port" \
      "$([[ "$port" =~ ^[0-9]+$ ]] && (( port >= 1024 && port <= 62000 )) && echo 1)" "got $port"
truth "the panel is not at the root" \
      "$([[ -n "$base" ]] && echo 1)" "no URL path was generated"
truth "the URL path is long enough not to be walked into" \
      "$([[ ${#base} -ge 16 ]] && echo 1)" "path was ${#base} characters: $base"
truth "the administrator is not called admin" \
      "$([[ "$user" != admin && -n "$user" ]] && echo 1)" "got $user"
truth "the administrator name is generated, not typed" \
      "$([[ ${#user} -ge 8 ]] && echo 1)" "name was ${#user} characters: $user"
check "no password is chosen here; one is generated at install time" "" "$(field PASS "$out")"
check "a blank domain lands on plain HTTP, not a dead end" "none" "$(field MODE "$out")"

echo
echo "── and the next install is not the same install ───────────────────────"
out2=$(run_wizard "$ALL_DEFAULT")
truth "a second run picks a different port"      "$([[ "$(field PORT "$out2")" != "$port" ]] && echo 1)" "both were $port"
truth "a second run picks a different URL path"  "$([[ "$(field BASE "$out2")" != "$base" ]] && echo 1)" "both were $base"
truth "a second run picks a different name"      "$([[ "$(field USER "$out2")" != "$user" ]] && echo 1)" "both were $user"

echo
echo "── an operator who wants to choose, can ───────────────────────────────"
out=$(run_wizard "y
8443
y
panel
y
operator
Sup3rSecret!
Sup3rSecret!
1
panel.example.com
me@example.com
y
y
y")
check "the port they asked for"     "8443"              "$(field PORT "$out")"
check "the path they asked for"     "panel"             "$(field BASE "$out")"
check "the name they asked for"     "operator"          "$(field USER "$out")"
check "the password they asked for" "Sup3rSecret!"      "$(field PASS "$out")"
check "a certificate for the domain" "acme"             "$(field MODE "$out")"
check "the domain"                  "panel.example.com" "$(field DOMAIN "$out")"

echo
echo "── a port another project already serves is refused, not taken over ───"
out=$(run_wizard "y
2096
9000
n
n
3
y
y
y" 'BUSY_PORT=2096')
check "moved off the busy port" "9000" "$(field PORT "$out")"

echo
echo "── deliberately putting the panel at the root is allowed, and warned ──"
out=$(run_wizard "n
y

n
3
y
y
y")
check "a blank path means the root" "" "$(field BASE "$out")"

echo
echo "── junk answers are re-asked rather than accepted ─────────────────────"
out=$(run_wizard "y
notaport
70000
2096
y
has/slash
also space
good-path
n
9
1
not-a-domain
panel.example.org

y
y
y")
check "invalid ports rejected"  "2096"              "$(field PORT "$out")"
check "invalid paths rejected"  "good-path"         "$(field BASE "$out")"
check "invalid choice rejected" "acme"              "$(field MODE "$out")"
check "invalid domain rejected" "panel.example.org" "$(field DOMAIN "$out")"

echo
echo "── a mistyped password is asked again, not accepted ───────────────────"
out=$(run_wizard "n
n
y
admin
firstpassword
secondpassword
matching-one
matching-one
3
y
y
y")
check "the confirmed password is the one kept" "matching-one" "$(field PASS "$out")"

echo
echo "── a password under 8 characters is refused ───────────────────────────"
out=$(run_wizard "n
n
y
admin
short
longenough1
longenough1
3
y
y
y")
check "short password rejected, long one kept" "longenough1" "$(field PASS "$out")"

echo
echo "── re-running does not move a panel people have bookmarked ────────────"
cat >"$WORK/wui.service" <<'UNIT'
[Service]
Environment=WUI_LISTEN=0.0.0.0:41234
Environment=WUI_DATA_DIR=/var/lib/wui
Environment=WUI_BASE_PATH=aBcDeFgHiJkLmNoPqR
UNIT
out=$(run_wizard "$ALL_DEFAULT" "UNIT='$WORK/wui.service'")
check "the port the panel already listens on is kept" "41234"               "$(field PORT "$out")"
check "the path it is already reached at is kept"     "aBcDeFgHiJkLmNoPqR"  "$(field BASE "$out")"

echo
echo "── declining the summary cancels without touching anything ────────────"
out=$(
  set +e
  # shellcheck disable=SC1091
  source "$WORK/lib.sh" >/dev/null 2>&1
  printf 'n\nn\nn\n3\ny\ny\nn\n' >"$WORK/answers"
  open_tty() { INTERACTIVE=1; exec 3<"$WORK/answers"; }
  have() { return 1; }
  port_taken() { return 1; }
  die() { printf 'CANCELLED: %s\n' "$*"; exit 9; }
  configure 2>&1 | tail -1
)
truth "answering no cancels" "$([[ "$out" == *cancelled* ]] && echo 1)" "got: $out"

echo
echo "── no terminal: the same unguessable defaults, not weaker ones ────────"
out=$(
  set +e
  # shellcheck disable=SC1091
  source "$WORK/lib.sh" >/dev/null 2>&1
  ASSUME_YES=1
  have() { return 1; }
  port_taken() { return 1; }
  die() { printf 'DIED: %s\n' "$*"; exit 9; }
  configure >/dev/null 2>&1
  printf 'PORT=%s\nUSER=%s\nBASE=%s\nMODE=%s\n' "$PANEL_PORT" "$ADMIN_USER" "$BASE_PATH" "$TLS_MODE"
)
truth "a random port even with nobody to ask"  "$([[ "$(field PORT "$out")" != 2096 ]] && echo 1)"       "$(field PORT "$out")"
nb=$(field BASE "$out")
truth "a random path even with nobody to ask"  "$([[ ${#nb} -ge 16 ]] && echo 1)" "$nb"
truth "a random name even with nobody to ask"  "$([[ "$(field USER "$out")" != admin ]] && echo 1)"      "$(field USER "$out")"

out=$(
  set +e
  # shellcheck disable=SC1091
  source "$WORK/lib.sh" >/dev/null 2>&1
  ASSUME_YES=1; BASE_KNOWN=1; BASE_PATH=""; PORT_KNOWN=1; PANEL_PORT=2096
  ACME_DOMAIN="auto.example.com"
  have() { return 1; }
  port_taken() { return 1; }
  die() { printf 'DIED: %s\n' "$*"; exit 9; }
  configure >/dev/null 2>&1
  printf 'PORT=%s\nBASE=%s\nMODE=%s\n' "$PANEL_PORT" "$BASE_PATH" "$TLS_MODE"
)
check "--no-path really means the root"      ""     "$(field BASE "$out")"
check "--port really means that port"        "2096" "$(field PORT "$out")"
check "a domain in the environment selects acme" "acme" "$(field MODE "$out")"

out=$(
  set +e
  # shellcheck disable=SC1091
  source "$WORK/lib.sh" >/dev/null 2>&1
  ASSUME_YES=1; PORT_KNOWN=1; PANEL_PORT=2096
  have() { return 1; }
  port_taken() { return 0; }
  port_owner() { printf 'caddy'; }
  die() { printf 'DIED: %s\n' "$*"; exit 9; }
  configure 2>&1 | tail -1
)
truth "a pinned port that is busy fails clearly, naming the service" \
      "$([[ "$out" == *"already served by caddy"* ]] && echo 1)" "got: $out"

printf '\n  %d passed, %d failed\n\n' "$pass" "$fail"
[[ "$fail" == 0 ]]
