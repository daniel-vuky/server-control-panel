#!/usr/bin/env bash
# Tests for deploy/updater.sh, run with `sh` (busybox in the real image) against
# fake-docker. UPDATE_INTERVAL=0 and SCP_UPDATER_ONCE=1 make it run one round.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=deploy/test/lib.sh
source "$here/lib.sh"

UPDATER="$here/../updater.sh"
T=$(mktemp -d -p /srv) || exit 1
# shellcheck disable=SC2317  # called by lib.sh at exit
test_cleanup() { rm -rf "$T"; }
mkdir -p "$T/bin"
install -m 0755 "$here/fake-docker" "$T/bin/docker"
export PATH="$T/bin:$PATH"

PRUNE='image prune -a -f --filter label=io.github.daniel-vuky.server-control-panel.image=panel'

# fresh_case: forget every fake-docker setting and empty the docker log.
fresh_case() {
  unset "${!FAKE_DOCKER_@}" SCP_UPDATER_ONCE
  export FAKE_DOCKER_LOG="$T/docker.log"
  : >"$FAKE_DOCKER_LOG"
  export UPDATE_INTERVAL=0
}

# run_once: one round; sets RC, OUT and LOG.
run_once() {
  SCP_UPDATER_ONCE=1 sh "$UPDATER" >"$T/out" 2>&1
  RC=$?
  OUT=$(<"$T/out")
  LOG=$(<"$FAKE_DOCKER_LOG")
}

PULL='compose pull --quiet panel'
LOOKUP='compose config --images panel
image inspect -f {{.Id}} localhost:5000/panel:latest
compose ps -q panel'
CHECK="$LOOKUP
inspect -f {{.Image}} c0ffee"

# panel_running <running image id>: stages a running panel container c0ffee on
# that image, with the pulled image sha256:new under localhost:5000/panel:latest.
panel_running() {
  export FAKE_DOCKER_COMPOSE_CONFIG_OUT=localhost:5000/panel:latest
  export FAKE_DOCKER_IMAGE_INSPECT_OUT=sha256:new
  export FAKE_DOCKER_COMPOSE_PS_OUT=c0ffee
  export FAKE_DOCKER_INSPECT_OUT=$1
}

stamped_lines() { grep -cE '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z ' "$T/out"; }

echo "== up to date: no up, no prune"
fresh_case
panel_running sha256:new
run_once
assert_eq 0 "$RC" "an up-to-date round exits 0"
assert_eq "$PULL
$CHECK" "$LOG" "docker calls are the pull and the image lookups only"
assert_contains "$OUT" "panel is up to date" "it logs that the panel is up to date"
assert_eq 2 "$(stamped_lines)" "the pull and the result log UTC-timestamped lines"

echo "== new image: pull, compare, up, prune in order"
fresh_case
panel_running sha256:old
run_once
assert_eq 0 "$RC" "a successful update round exits 0"
assert_eq "$PULL
$CHECK
compose up -d panel
$PRUNE" "$LOG" "docker calls are pull, lookups, up, prune, in that order"
assert_eq 4 "$(stamped_lines)" "each step and the round end log a UTC-timestamped line"

echo "== no running panel container: up, prune"
fresh_case
panel_running sha256:new
export FAKE_DOCKER_COMPOSE_PS_OUT=""
run_once
assert_eq 0 "$RC" "the round exits 0"
assert_eq "$PULL
$LOOKUP
compose up -d panel
$PRUNE" "$LOG" "no docker inspect of an empty ID; up and prune run"

echo "== the image lookup fails: treated as a new image"
fresh_case
panel_running sha256:new
export FAKE_DOCKER_IMAGE_INSPECT_RC=1 FAKE_DOCKER_IMAGE_INSPECT_OUT=""
run_once
assert_eq 0 "$RC" "the round exits 0"
assert_contains "$LOG" "compose up -d panel" "up runs"
assert_contains "$LOG" "$PRUNE" "prune runs"

echo "== pull fails: nothing after it"
fresh_case
panel_running sha256:old
export FAKE_DOCKER_COMPOSE_PULL_RC=1
run_once
assert_eq 1 "$RC" "a failed pull makes the round exit 1"
assert_eq "$PULL" "$LOG" "only the pull ran"
assert_contains "$OUT" "pull failed" "the failure is logged"

echo "== up fails: no prune"
fresh_case
panel_running sha256:old
export FAKE_DOCKER_COMPOSE_UP_RC=1
run_once
assert_eq 1 "$RC" "a failed up makes the round exit 1"
assert_eq "$PULL
$CHECK
compose up -d panel" "$LOG" "pull, lookups and up ran, prune did not"
assert_contains "$OUT" "up failed" "the failure is logged"

echo "== prune fails"
fresh_case
panel_running sha256:old
export FAKE_DOCKER_IMAGE_PRUNE_RC=1
run_once
assert_eq 1 "$RC" "a failed prune makes the round exit 1"
assert_contains "$OUT" "prune failed" "the failure is logged"

echo "== the loop survives a failed round"
fresh_case
export FAKE_DOCKER_COMPOSE_PULL_RC=1 UPDATE_INTERVAL=0.2
sh "$UPDATER" >"$T/out" 2>&1 &
pid=$!
sleep 1
alive=no
if kill -0 "$pid" 2>/dev/null; then alive=yes; fi
kill "$pid" 2>/dev/null
wait "$pid" 2>/dev/null
pulls=$(grep -c '^compose pull --quiet panel$' "$FAKE_DOCKER_LOG")
assert_eq yes "$alive" "the updater is still running after failed rounds"
assert_eq yes "$([[ $pulls -ge 2 ]] && echo yes || echo no)" "at least two pull rounds ran (saw $pulls)"
assert_eq 0 "$(grep -c '^compose up' "$FAKE_DOCKER_LOG")" "no up after a failed pull"

echo "== a malformed UPDATE_INTERVAL does not hammer the registry"
fresh_case
export UPDATE_INTERVAL=bogus
sh "$UPDATER" >"$T/out" 2>&1 &
pid=$!
sleep 0.5
alive=no
if kill -0 "$pid" 2>/dev/null; then alive=yes; fi
pkill -P "$pid" 2>/dev/null
kill "$pid" 2>/dev/null
wait "$pid" 2>/dev/null
assert_eq yes "$alive" "the updater keeps waiting"
assert_eq 0 "$(grep -c "" "$FAKE_DOCKER_LOG")" "no docker call while the interval is malformed"
assert_contains "$(<"$T/out")" "invalid UPDATE_INTERVAL 'bogus'" "the bad value is logged once"
assert_eq 1 "$(grep -c "invalid UPDATE_INTERVAL" "$T/out")" "only one line about it"

summary
