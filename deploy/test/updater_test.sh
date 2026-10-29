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

echo "== success: pull, up, prune in order"
fresh_case
run_once
assert_eq 0 "$RC" "a successful round exits 0"
assert_eq "compose pull --quiet panel
compose up -d panel
$PRUNE" "$LOG" "docker calls are pull, up, prune, in that order"
assert_eq 4 "$(grep -cE '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z ' "$T/out")" "each step and the round end log a UTC-timestamped line"

echo "== pull fails: nothing after it"
fresh_case
export FAKE_DOCKER_COMPOSE_PULL_RC=1
run_once
assert_eq 1 "$RC" "a failed pull makes the round exit 1"
assert_eq "compose pull --quiet panel" "$LOG" "only the pull ran"
assert_contains "$OUT" "pull failed" "the failure is logged"

echo "== up fails: no prune"
fresh_case
export FAKE_DOCKER_COMPOSE_UP_RC=1
run_once
assert_eq 1 "$RC" "a failed up makes the round exit 1"
assert_eq "compose pull --quiet panel
compose up -d panel" "$LOG" "pull and up ran, prune did not"
assert_contains "$OUT" "up failed" "the failure is logged"

echo "== prune fails"
fresh_case
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
