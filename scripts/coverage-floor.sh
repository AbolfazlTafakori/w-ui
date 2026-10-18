#!/usr/bin/env bash
# The packages that keep customers apart, sign people in, keep the backups
# and bill usage do not lose their tests quietly.
#
# Each has a floor a few points under what its tests cover today. A change
# that drops below it fails here; raising a floor after adding tests is
# welcome, lowering one is a decision for review.
set -uo pipefail

declare -A floor=(
	[./internal/radiusd]=85     # who an IKEv2 or OpenConnect sign-in lets in
	[./internal/scope]=80       # a reseller sees their own customers only
	[./internal/keyfile]=60     # the session key and the backup password
	[./internal/backup]=70      # sealed archives and restores
	[./internal/database]=50    # migrations and the portable dump
	[./internal/backend/agentkit]=50
	[./internal/listfield]=95
)

fail=0
for pkg in "${!floor[@]}"; do
	out="$(go test -count=1 -cover "$pkg" 2>&1)"
	pct="$(grep -o 'coverage: [0-9.]*%' <<<"$out" | grep -o '[0-9.]*' | head -1)"
	if [[ -z "$pct" ]]; then
		printf 'FAIL %s: no coverage reported\n%s\n' "$pkg" "$out"
		fail=1
		continue
	fi
	if awk -v p="$pct" -v f="${floor[$pkg]}" 'BEGIN { exit !(p < f) }'; then
		printf 'FAIL %s: %s%% covered, the floor is %s%%\n' "$pkg" "$pct" "${floor[$pkg]}"
		fail=1
	else
		printf 'ok   %s: %s%% (floor %s%%)\n' "$pkg" "$pct" "${floor[$pkg]}"
	fi
done
exit "$fail"
