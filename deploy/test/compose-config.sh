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
echo "compose config ok"
