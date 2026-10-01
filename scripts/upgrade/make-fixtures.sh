#!/usr/bin/env bash
# Makes the upgrade fixtures: for each release named, the panel that release
# built, run on an empty data directory, given the fixture set through its own
# API, and written down -- its database, a backup it took, each customer's
# configuration as their link served it, and a manifest of what it said.
#
# Usage:
#   scripts/upgrade/make-fixtures.sh                    every release in VERSIONS
#   scripts/upgrade/make-fixtures.sh v2.5.2 v2.6.0      only these
#
#   OUT=dir                   where to write (default internal/upgradecheck/testdata)
#   WUI_FIXTURE_PG_DSN=dsn    seed onto this PostgreSQL database instead, and
#                             write pg.sql (pg_dump) in place of wui.db. The
#                             database is emptied first: give it one of its own.
#
# Each release is built from its tag in a throwaway git worktree, so the
# working tree is not touched. Runs on Linux and in Git Bash on Windows: the
# panel answers its API without root, which is all the fixture needs.
set -euo pipefail

VERSIONS_DEFAULT=(v1.0.0 v1.1.0 v2.0.0 v2.3.6 v2.4.1 v2.5.2 v2.6.0)
ROOT="$(git rev-parse --show-toplevel)"
OUT="${OUT:-$ROOT/internal/upgradecheck/testdata}"
PG_DSN="${WUI_FIXTURE_PG_DSN:-}"
ADMIN_USER=fixture
ADMIN_PASS=fixture-password-1
EXE=""
[[ "$(go env GOOS)" == windows ]] && EXE=.exe

work="$(mktemp -d)"
panel_pid=""
cleanup() {
  if [[ -n "$panel_pid" ]]; then kill "$panel_pid" 2>/dev/null || true; wait "$panel_pid" 2>/dev/null || true; fi
  for wt in "$work"/src-*; do [[ -d "$wt" ]] && git -C "$ROOT" worktree remove --force "$wt" >/dev/null 2>&1 || true; done
  rm -rf "$work"
}
trap cleanup EXIT

echo "building the tool"
(cd "$ROOT" && go build -o "$work/upgrade$EXE" ./tools/upgrade)

versions=("$@")
[[ ${#versions[@]} -gt 0 ]] || versions=("${VERSIONS_DEFAULT[@]}")

i=0
for v in "${versions[@]}"; do
  i=$((i + 1))
  echo "== $v"
  src="$work/src-$v"
  git -C "$ROOT" worktree add --detach "$src" "$v" >/dev/null 2>&1
  (cd "$src" && CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=${v#v}" -o "$work/wui-$v$EXE" ./cmd/wui)
  git -C "$ROOT" worktree remove --force "$src" >/dev/null 2>&1

  data="$work/data-$v"
  mkdir -p "$data/backups"
  # A fixed address per release: the test runs the new panel on the same
  # one, so the links this panel hands out are the links tested later.
  port=$((47100 + i * 10))
  dest="$OUT/$v"
  if [[ -n "$PG_DSN" ]]; then
    dest="$OUT/pg-$v"
    psql "$PG_DSN" -q -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;' >/dev/null
  fi
  rm -rf "$dest"
  mkdir -p "$dest"

  env_panel=(WUI_DATA_DIR="$data" WUI_BACKUP_DIR="$data/backups" WUI_LISTEN="127.0.0.1:$port" WUI_LOG_LEVEL=warn)
  if [[ -n "$PG_DSN" ]]; then
    env_panel+=(WUI_DB_DRIVER=postgres WUI_DB_SOURCE="$PG_DSN")
  fi
  env "${env_panel[@]}" "$work/wui-$v$EXE" admin reset --username "$ADMIN_USER" --password "$ADMIN_PASS" >/dev/null
  start_panel() {
    env "${env_panel[@]}" "$work/wui-$v$EXE" >> "$work/panel-$v.log" 2>&1 &
    panel_pid=$!
  }
  stop_panel() {
    kill "$panel_pid" 2>/dev/null || true
    wait "$panel_pid" 2>/dev/null || true
    panel_pid=""
  }
  # Its own port for the subscription service, as the installer gives it,
  # from v1.1.0 -- the release that brought one. v1.0.0 served links on the
  # panel's port, and is left that way.
  sub_port=$((port + 1))
  [[ "$v" == v1.0.* ]] && sub_port=0
  # The subscription service first, then a restart: a release that opens its
  # own port only when it starts has it open for the links seeded next.
  start_panel
  "$work/upgrade$EXE" seed -subscription-only -url "http://127.0.0.1:$port/" \
    -user "$ADMIN_USER" -pass "$ADMIN_PASS" -sub-port $sub_port
  stop_panel
  start_panel

  db_flag=(-sqlite "$data/wui.db")
  [[ -n "$PG_DSN" ]] && db_flag=(-postgres "$PG_DSN")
  if ! "$work/upgrade$EXE" seed -url "http://127.0.0.1:$port/" -user "$ADMIN_USER" -pass "$ADMIN_PASS" \
      -version "$v" -out "$dest" -listen "127.0.0.1:$port" -sub-port $sub_port "${db_flag[@]}"; then
    echo "--- the $v panel said:"; tail -40 "$work/panel-$v.log"
    exit 1
  fi
  stop_panel

  if [[ -n "$PG_DSN" ]]; then
    # Plain INSERTs, no owners or grants: loaded into any database by any role.
    # psql's own meta-commands, and settings a newer pg_dump writes that an
    # older server refuses, are left out: the test loads this as plain SQL.
    pg_dump --no-owner --no-privileges --inserts "$PG_DSN" \
      | grep -v -e '^\\' -e '^SET transaction_timeout' > "$dest/pg.sql"
  fi
done
echo "fixtures written to $OUT"
