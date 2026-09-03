#!/bin/sh
# Does the installer survive a third-party repository that publishes nothing
# for this release?
#
# This reproduces the failure an operator hit on Ubuntu 26.04: the AmneziaWG
# PPA had no Release file for `resolute`, so `apt-get update` returned non-zero,
# and with `set -e` the install ended at its first step having done nothing.
#
# Worse, the broken repository was one this installer had added on an earlier
# run -- so every later attempt failed the same way, and so did any other apt
# work on that machine.
#
# Run:  docker run --rm -v "$PWD/scripts:/work" -v "$PWD/install.sh:/install.sh" \
#         ubuntu:24.04 sh /work/verify-installer.sh
set -e

apt-get update -qq >/dev/null 2>&1 || true
apt-get install -y -qq curl ca-certificates >/dev/null 2>&1

say() { printf '\n=== %s ===\n' "$1"; }

# Plant exactly what the operator had: a PPA for a codename it does not serve.
cat >/etc/apt/sources.list.d/amnezia-ubuntu-ppa-noble.list <<'EOF'
deb https://ppa.launchpadcontent.net/amnezia/ppa/ubuntu resolute main
EOF

say "apt before the installer runs"
if apt-get update -qq >/dev/null 2>&1; then
  echo "  apt: OK (unexpected -- the planted repo should break it)"
else
  echo "  apt: BROKEN, which is the state the operator was in"
fi

# Pull in just the functions under test, without running the installer.
# Everything above the `── run ──` marker is definitions.
sed '/^# ── run ─/,$d' /install.sh >/tmp/lib.sh
# shellcheck disable=SC1091
. /tmp/lib.sh
detect_os

say "what the installer sees"
echo "  codename: ${OS_CODENAME:-<none>}"

say "does the PPA publish for this release?"
if ppa_publishes_for_this_release; then
  echo "  yes -- it would be added"
else
  echo "  no  -- it is skipped instead of being added"
fi

say "repairing"
repair_amnezia_ppa

say "apt afterwards"
if apt-get update -qq >/dev/null 2>&1; then
  echo "  apt: OK -- the installer can run"
else
  echo "  apt: STILL BROKEN"
  exit 1
fi

say "and the base step now completes"
install_base
