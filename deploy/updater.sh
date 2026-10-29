#!/bin/sh
# The update loop run by the `updater` service in compose.yaml (busybox sh in
# docker:29-cli, so POSIX only). Every UPDATE_INTERVAL seconds (default 300,
# sleep first) it pulls the panel image, recreates the panel when the image
# changed, and then removes panel images no container uses. A failed step is
# logged and ends the round without running the later steps; the loop goes on.
# SCP_UPDATER_ONCE=1 runs one round and exits with its status (tests).

IMAGE_LABEL='io.github.daniel-vuky.server-control-panel.image=panel'

log() {
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

# round: returns 0 when every step succeeded.
round() {
  log 'pulling panel image'
  if ! docker compose pull --quiet panel; then
    log 'pull failed, will retry next round'
    return 1
  fi
  log 'updating panel'
  if ! docker compose up -d panel; then
    log 'up failed, skipping image prune'
    return 1
  fi
  log 'pruning old panel images'
  if ! docker image prune -a -f --filter "label=$IMAGE_LABEL"; then
    log 'prune failed'
    return 1
  fi
  log 'update round done'
  return 0
}

while :; do
  if ! sleep "${UPDATE_INTERVAL:-300}"; then
    # A malformed value (for example 5m) would make sleep fail at once and the
    # loop would hammer the registry; wait the default instead.
    log "invalid UPDATE_INTERVAL '${UPDATE_INTERVAL}', waiting 300 seconds"
    sleep 300
  fi
  round
  rc=$?
  if [ "${SCP_UPDATER_ONCE:-}" = 1 ]; then
    exit "$rc"
  fi
done
