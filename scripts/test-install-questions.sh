#!/usr/bin/env bash
# Drives the installer's questions with scripted answers.
#
# Everything above the run marker is definitions, so it can be sourced without
# installing anything. The one thing replaced is where the questions read from:
# open_tty normally grabs /dev/tty, and here fd 3 is pointed at a file of
# answers instead, which is the only way to test a prompt without a terminal.
set -uo pipefail

SRC="${1:?usage: test-wizard.sh /path/to/install.sh}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Definitions only: the run section is cut, and so is argument parsing —
# which ends in a root check that would exit the moment this is sourced.
# set -e goes too, so a deliberately wrong answer under test does not kill
# the shell that is checking how it was handled.
sed -e '/^# ── run ─/,$d' -e '/^# ── arguments ─/,/^# ── platform ─/d' -e '/^set -euo pipefail$/d' "$SRC" >"$WORK/lib.sh"

pass=0; fail=0
check() { # check "what" "expected" "actual"
  if [[ "$2" == "$3" ]]; then
    printf '  \e[32mok\e[0m   %s\n' "$1"; pass=$((pass+1))
  else
    printf '  \e[31mFAIL\e[0m %s\n       want: %s\n       got:  %s\n' "$1" "$2" "$3"; fail=$((fail+1))
  fi
}
contains() {
  if [[ "$3" == *"$2"* ]]; then
    printf '  \e[32mok\e[0m   %s\n' "$1"; pass=$((pass+1))
  else
    printf '  \e[31mFAIL\e[0m %s\n       missing: %s\n       in: %s\n' "$1" "$2" "$3"; fail=$((fail+1))
  fi
}

# Run configure with a canned set of answers, then print the decisions.
run_wizard() { # run_wizard <<< answers
  local answers="$1"
  printf '%s\n' "$answers" >"$WORK/answers"
  (
    set +e
    # shellcheck disable=SC1091
    source "$WORK/lib.sh" >/dev/null 2>&1

    # The only substitution: answers come from a file, not a terminal.
    open_tty() { INTERACTIVE=1; exec 3<"$WORK/answers"; }
    # Nothing here is allowed to touch the machine.
    have() { case "$1" in ss|netstat) return 1 ;; *) command -v "$1" >/dev/null 2>&1 ;; esac; }
    port_taken() { [[ "$1" == "$BUSY_PORT" ]]; }
    port_owner() { printf 'nginx'; }
    die() { printf 'DIED: %s\n' "$*"; exit 9; }

    configure >/dev/null 2>&1
    printf 'PORT=%s\nUSER=%s\nPASS=%s\nMODE=%s\nDOMAIN=%s\nEMAIL=%s\nCERT=%s\nKEY=%s\nOVPN=%s\nAWG=%s\n' \
      "$PANEL_PORT" "$ADMIN_USER" "$ADMIN_PASS" "$TLS_MODE" "$ACME_DOMAIN" "$ACME_EMAIL" \
      "$TLS_CERT" "$TLS_KEY" "$WANT_OPENVPN" "$WANT_AMNEZIA"
  )
}

echo
echo "── the ordinary path: a domain and a chosen password ──────────────────"
BUSY_PORT=-1
out=$(run_wizard "8443
operator
Sup3rSecret!
Sup3rSecret!
1
panel.example.com
me@example.com
y
y
y")
check "port taken from the answer"      "PORT=8443"                  "$(grep '^PORT=' <<<"$out")"
check "username taken from the answer"  "USER=operator"              "$(grep '^USER=' <<<"$out")"
check "password kept"                   "PASS=Sup3rSecret!"          "$(grep '^PASS=' <<<"$out")"
check "certificate mode"                "MODE=acme"                  "$(grep '^MODE=' <<<"$out")"
check "domain"                          "DOMAIN=panel.example.com"   "$(grep '^DOMAIN=' <<<"$out")"
check "email"                           "EMAIL=me@example.com"       "$(grep '^EMAIL=' <<<"$out")"

echo
echo "── pressing enter through everything takes the defaults ───────────────"
out=$(run_wizard "











")
check "default port"     "PORT=2096"  "$(grep '^PORT=' <<<"$out")"
check "default username" "USER=admin" "$(grep '^USER=' <<<"$out")"
check "blank password stays blank, to be generated" "PASS=" "$(grep '^PASS=' <<<"$out")"
check "a blank domain lands on plain HTTP, not a dead end" "MODE=none" "$(grep '^MODE=' <<<"$out")"

echo
echo "── a port another project already serves is refused, not taken over ───"
BUSY_PORT=2096
out=$(run_wizard "2096
9000
admin

3
y
y
y")
check "moved off the busy port" "PORT=9000" "$(grep '^PORT=' <<<"$out")"
check "plain HTTP chosen"       "MODE=none" "$(grep '^MODE=' <<<"$out")"

echo
echo "── a mistyped password is asked again, not accepted ───────────────────"
BUSY_PORT=-1
out=$(run_wizard "2096
admin
firstpassword
secondpassword
matching-one
matching-one
3
y
y
y")
check "the confirmed password is the one kept" "PASS=matching-one" "$(grep '^PASS=' <<<"$out")"

echo
echo "── a password under 8 characters is refused ───────────────────────────"
out=$(run_wizard "2096
admin
short
longenough1
longenough1
3
y
y
y")
check "short password rejected, long one kept" "PASS=longenough1" "$(grep '^PASS=' <<<"$out")"

echo
echo "── junk answers are re-asked rather than accepted ─────────────────────"
out=$(run_wizard "notaport
70000
2096
admin

9
1
not-a-domain
panel.example.org

y
y
y")
check "invalid ports rejected"  "PORT=2096"                "$(grep '^PORT=' <<<"$out")"
check "invalid choice rejected" "MODE=acme"                "$(grep '^MODE=' <<<"$out")"
check "invalid domain rejected" "DOMAIN=panel.example.org" "$(grep '^DOMAIN=' <<<"$out")"

echo
echo "── declining the summary cancels without touching anything ────────────"
out=$(
  set +e
  # shellcheck disable=SC1091
  source "$WORK/lib.sh" >/dev/null 2>&1
  printf '2096\nadmin\n\n3\ny\ny\nn\n' >"$WORK/answers"
  open_tty() { INTERACTIVE=1; exec 3<"$WORK/answers"; }
  have() { return 1; }
  port_taken() { return 1; }
  die() { printf 'CANCELLED: %s\n' "$*"; exit 9; }
  configure 2>&1 | tail -1
)
contains "answering no cancels" "cancelled" "$out"

echo
echo "── no terminal: flags and defaults, and a clear refusal on a clash ────"
out=$(
  set +e
  # shellcheck disable=SC1091
  source "$WORK/lib.sh" >/dev/null 2>&1
  ASSUME_YES=1
  ACME_DOMAIN="auto.example.com"
  have() { return 1; }
  port_taken() { return 1; }
  die() { printf 'DIED: %s\n' "$*"; exit 9; }
  configure >/dev/null 2>&1
  printf 'MODE=%s USER=%s\n' "$TLS_MODE" "$ADMIN_USER"
)
check "a domain in the environment selects acme" "MODE=acme USER=admin" "$out"

out=$(
  set +e
  # shellcheck disable=SC1091
  source "$WORK/lib.sh" >/dev/null 2>&1
  ASSUME_YES=1
  TLS_MODE=none
  ACME_DOMAIN="auto.example.com"
  have() { return 1; }
  port_taken() { return 1; }
  die() { printf 'DIED: %s\n' "$*"; exit 9; }
  configure >/dev/null 2>&1
  printf 'MODE=%s\n' "$TLS_MODE"
)
check "--no-tls beats a domain in the environment" "MODE=none" "$out"

out=$(
  set +e
  # shellcheck disable=SC1091
  source "$WORK/lib.sh" >/dev/null 2>&1
  ASSUME_YES=1
  have() { return 1; }
  port_taken() { return 0; }
  port_owner() { printf 'caddy'; }
  die() { printf 'DIED: %s\n' "$*"; exit 9; }
  configure 2>&1 | tail -1
)
contains "a busy port with no terminal is a clear failure, naming the service" "already served by caddy" "$out"

echo
printf '\n  %d passed, %d failed\n\n' "$pass" "$fail"
[[ "$fail" == 0 ]]
