#!/bin/sh
# The update loop run by the `updater` service in compose.yaml (busybox sh in
# docker:29-cli, so POSIX only). Every UPDATE_INTERVAL seconds (default 300,
# sleep first) it pulls the panel image and compares it with the image the
# running panel uses. Only when they differ does it recreate the panel and then
# remove panel images no container uses: on the containerd image store an
# image prune breaks every pull that overlaps it, so it must not run every
# round. A failed step is logged and ends the round without running the later
# steps; the loop goes on. SCP_UPDATER_ONCE=1 runs one round and exits with its
# status (tests).

IMAGE_LABEL='io.github.daniel-vuky.server-control-panel.image=panel'

log() {
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

# panel_is_current: succeeds when the running panel container uses the image
# the pull left under the panel's image reference. Both IDs are sha256:<hex>
# on the classic and the containerd image stores. No running panel container,
# or any lookup failing, counts as not current.
panel_is_current() {
  ref=$(docker compose config --images panel) || return 1
  want=$(docker image inspect -f '{{.Id}}' "$ref") || return 1
  cid=$(docker compose ps -q panel) || return 1
  [ -n "$cid" ] || return 1
  have=$(docker inspect -f '{{.Image}}' "$cid") || return 1
  [ -n "$want" ] && [ "$want" = "$have" ]
}

# round: returns 0 when every step succeeded.
round() {
  log 'pulling panel image'
  if ! docker compose pull --quiet panel; then
    log 'pull failed, will retry next round'
    return 1
  fi
  if panel_is_current; then
    log 'panel is up to date'
    return 0
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
