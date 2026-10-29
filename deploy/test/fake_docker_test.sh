#!/usr/bin/env bash
# Self-test for deploy/test/fake-docker, which the install, updater and
# uninstall tests rely on.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=deploy/test/lib.sh
source "$here/lib.sh"

TMPD=$(mktemp -d -p /srv) || exit 1
# shellcheck disable=SC2317  # called by lib.sh at exit
test_cleanup() { rm -rf "$TMPD"; }
mkdir "$TMPD/bin"
# Installed the way the real tests do, so the checkout's mode bits do not matter.
install -m 0755 "$here/fake-docker" "$TMPD/bin/docker"
export PATH="$TMPD/bin:$PATH"
export FAKE_DOCKER_LOG="$TMPD/log"

assert_eq 2.40.0 "$(docker compose version)" "compose version prints 2.40.0 by default"
assert_eq "compose version" "$(cat "$FAKE_DOCKER_LOG")" "the call is logged as one line of arguments"

: >"$FAKE_DOCKER_LOG"
docker compose pull --quiet
assert_eq 0 "$?" "default rc is 0"
assert_eq "" "$(docker compose pull)" "default output is empty"
assert_eq "compose pull --quiet
compose pull" "$(cat "$FAKE_DOCKER_LOG")" "each invocation appends a line"

out=$(FAKE_DOCKER_COMPOSE_PULL_RC=3 FAKE_DOCKER_COMPOSE_PULL_OUT=denied docker compose pull)
rc=$?
assert_eq 3 "$rc" "COMPOSE_PULL_RC sets the exit code"
assert_eq denied "$out" "COMPOSE_PULL_OUT sets stdout"

assert_eq 2.99.0 "$(FAKE_DOCKER_COMPOSE_VERSION_OUT=2.99.0 docker compose version)" "COMPOSE_VERSION_OUT overrides the default"
assert_eq "" "$(FAKE_DOCKER_COMPOSE_VERSION_OUT="" docker compose version)" "an empty COMPOSE_VERSION_OUT stays empty"

assert_eq healthy "$(FAKE_DOCKER_INSPECT_OUT=healthy docker inspect --format x cid)" "INSPECT keys on the first argument alone"
FAKE_DOCKER_INFO_RC=1 docker info
assert_eq 1 "$?" "INFO_RC sets the exit code"
assert_eq abc "$(FAKE_DOCKER_IMAGE_LS_OUT=abc docker image ls)" "image subcommands add the second argument to the key"
FAKE_DOCKER_IMAGE_RM_RC=1 docker image rm x
assert_eq 1 "$?" "IMAGE_RM_RC is separate from IMAGE_LS"
assert_eq 0 "$(FAKE_DOCKER_COMPOSE_UP_RC=5 docker compose pull >/dev/null; echo $?)" "other subcommands are unaffected"

unset FAKE_DOCKER_LOG
assert_eq 2.40.0 "$(docker compose version)" "works without FAKE_DOCKER_LOG"

summary
