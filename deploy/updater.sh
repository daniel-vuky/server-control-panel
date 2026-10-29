#!/bin/sh
# The update loop run by the `updater` service in compose.yaml (busybox sh in
# docker:29-cli, so POSIX only). Every UPDATE_INTERVAL seconds (default 300,
# sleep first) it pulls the panel image and compares it with the image the
# panel container uses. A panel the owner stopped is left stopped. When the
# images differ, or no panel container exists, it recreates the panel and then
# removes panel images no container uses: on the containerd image store an
# image prune breaks every pull that overlaps it, so it must not run every
# round. A failed step is logged and ends the round without running the later
# steps; the loop goes on. SCP_UPDATER_ONCE=1 runs one round and exits with its
# status (tests).

IMAGE_LABEL='io.github.daniel-vuky.server-control-panel.image=panel'

log() {
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

# panel_state: compares the image the pull left under the panel's image
# reference with the image the panel container uses. Both IDs are
# sha256:<hex> on the classic and the containerd image stores. Returns 0 when
# they are the same, 1 when they differ or no panel container exists, 3 when
# the panel container exists but is not running (the owner stopped it; leave
# it alone), and 2 when a lookup failed or printed nothing: then nothing is
# known, and the round must not update or prune on a guess.
panel_state() {
  ref=$(docker compose config --images panel) || return 2
  want=$(docker image inspect -f '{{.Id}}' "$ref") || return 2
  # -a: a stopped container counts too, so a panel the owner stopped is not
  # mistaken for a missing one and restarted.
  cid=$(docker compose ps -aq panel) || return 2
  [ -n "$cid" ] || return 1
  info=$(docker inspect -f '{{.State.Running}} {{.Image}}' "$cid") || return 2
  running=${info%% *}
  have=${info#* }
  if [ -z "$want" ] || [ -z "$running" ] || [ -z "$have" ] || [ "$have" = "$info" ]; then
    return 2
  fi
  [ "$running" = true ] || return 3
  [ "$want" = "$have" ] && return 0
  return 1
}

# round: returns 0 when every step succeeded.
round() {
  log 'pulling panel image'
  if ! docker compose pull --quiet panel; then
    log 'pull failed, will retry next round'
    return 1
  fi
  panel_state
  state=$?
  if [ "$state" = 2 ]; then
    log 'cannot read the panel image, will retry next round'
    return 1
  fi
  if [ "$state" = 0 ]; then
    log 'panel is up to date'
    return 0
  fi
  if [ "$state" = 3 ]; then
    log 'panel is stopped, leaving it'
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
    # A malformed value (for example soon) would make sleep fail at once and the
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
