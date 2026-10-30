#!/usr/bin/env bash
# Renders the compose project with `install.sh --no-start` into a temp
# directory and checks it with the real `docker compose config` (the compose
# plugin only; no daemon needed). Run it through `make deploy-test`, never on
# the host.
set -euo pipefail

if [[ "$(uname -s)" != Linux ]]; then
  echo "compose-config.sh needs Linux; run it with 'make deploy-test'" >&2
  exit 2
fi

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
mkdir -p /srv
T=$(mktemp -d -p /srv)
trap 'rm -rf "$T"' EXIT
mkdir "$T/ws"

echo "== compose config check"
SCP_DIR="$T/scp" SCP_SOURCE="$here/.." SCP_TTY=/nonexistent \
  SUDO_UID=1000 SUDO_GID=1000 DOCKER_GID=998 \
  CF_ACCESS_TEAM_DOMAIN=myteam.cloudflareaccess.com CF_ACCESS_AUD=abc123 WORKSPACE_ROOTS="$T/ws" \
  bash "$here/../install.sh" --no-start

cd "$T/scp"
docker compose config --quiet
services=$(docker compose config --services | sort | tr '\n' ' ')
if [[ "$services" != "panel updater " ]]; then
  echo "unexpected services: $services" >&2
  exit 1
fi
rendered=$(docker compose config)
for want in 'name: server-control-panel' 'user: 1000:1000' "source: $T/ws" 'read_only: true' 'image: ghcr.io/daniel-vuky/server-control-panel:latest' 'UPDATE_INTERVAL: "300"'; do
  if [[ "$rendered" != *"$want"* ]]; then
    printf 'rendered config lacks [%s]:\n%s\n' "$want" "$rendered" >&2
    exit 1
  fi
done
updater_init=$(printf '%s\n' "$rendered" | awk '/^  updater:/ {u=1; next} /^  [a-z]/ || /^[a-z]/ {u=0} u && /^    init: true$/ {print "yes"}')
if [[ "$updater_init" != yes ]]; then
  printf 'updater service lacks init: true:\n%s\n' "$rendered" >&2
  exit 1
fi
# service_block <name>: the rendered lines of one service.
service_block() {
  printf '%s\n' "$rendered" | awk -v svc="  $1:" '$0 == svc {u=1; next} /^  [a-z]/ || /^[a-z]/ {u=0} u'
}
panel=$(service_block panel)
updater=$(service_block updater)
# list_has <service lines> <key> <item>: the service's <key> list holds <item>.
list_has() {
  awk -v key="    $2:" -v item="      - $3" '
    $0 == key {in_list = 1; next}
    in_list && /^      - / {if ($0 == item) found = 1; next}
    {in_list = 0}
    END {exit !found}' <<<"$1"
}
# The panel runs under an init that reaps killed command trees.
if ! grep -qx '    init: true' <<<"$panel"; then
  printf 'panel service lacks init: true:\n%s\n' "$panel" >&2
  exit 1
fi
# The panel drops every capability and cannot gain privileges.
if ! list_has "$panel" cap_drop ALL; then
  printf 'panel service lacks cap_drop: [ALL]:\n%s\n' "$panel" >&2
  exit 1
fi
if grep -qE '^    cap_add:' <<<"$panel"; then
  printf 'panel service adds capabilities:\n%s\n' "$panel" >&2
  exit 1
fi
if ! list_has "$panel" security_opt no-new-privileges:true; then
  printf 'panel service lacks security_opt no-new-privileges:true:\n%s\n' "$panel" >&2
  exit 1
fi
# The updater mounts the project directory read-only.
updater_ro=$(awk '
  function flush() { if (t == "/opt/server-control-panel" && ro) found = 1; t = ""; ro = 0 }
  /^      - / { flush() }
  /^        target: / { t = $2 }
  /^        read_only: true$/ { ro = 1 }
  END { flush(); if (found) print "yes" }' <<<"$updater")
if [[ "$updater_ro" != yes ]]; then
  printf 'updater service does not mount /opt/server-control-panel read-only:\n%s\n' "$updater" >&2
  exit 1
fi
echo "compose config ok"
