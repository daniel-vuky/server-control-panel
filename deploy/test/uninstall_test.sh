#!/usr/bin/env bash
# Tests for deploy/uninstall.sh, run as root against fake-docker. Each case
# installs first with `install.sh --no-start` into a fresh directory, then
# uninstalls it.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=deploy/test/lib.sh
source "$here/lib.sh"

INSTALL="$here/../install.sh"
UNINSTALL="$here/../uninstall.sh"
T=$(mktemp -d -p /srv) || exit 1
# shellcheck disable=SC2317  # called by lib.sh at exit
test_cleanup() { rm -rf "$T"; }
mkdir -p "$T/bin" "$T/ws1"
install -m 0755 "$here/fake-docker" "$T/bin/docker"
export PATH="$T/bin:$PATH"

REPO=ghcr.io/daniel-vuky/server-control-panel
IMAGE_ROWS="$REPO:latest 1111aaaa2222
$REPO:sha-abc1234 3333bbbb4444
<none>:<none> 5555cccc6666"

# fresh_install: a new install directory with a full --no-start install in it.
fresh_install() {
  unset "${!FAKE_DOCKER_@}"
  export FAKE_DOCKER_LOG="$T/docker.log"
  export FAKE_DOCKER_IMAGE_LS_OUT="$IMAGE_ROWS"
  SCP_DIR="$(mktemp -d -p "$T")/scp"
  export SCP_DIR SCP_SOURCE="$here/.." SUDO_UID=1000 SUDO_GID=1000 DOCKER_GID=998 SCP_TTY=/nonexistent
  CF_ACCESS_TEAM_DOMAIN=myteam.cloudflareaccess.com CF_ACCESS_AUD=abc123 WORKSPACE_ROOTS="$T/ws1" \
    bash "$INSTALL" --no-start >/dev/null 2>&1 || echo "setup: install.sh failed" >&2
}

# run_uninstall [args...]: sets RC, OUT (stdout and stderr) and LOG (the docker
# calls of this run only).
run_uninstall() {
  : >"$FAKE_DOCKER_LOG"
  bash "$UNINSTALL" "$@" >"$T/out" 2>&1
  RC=$?
  OUT=$(<"$T/out")
  LOG=$(<"$FAKE_DOCKER_LOG")
}

# present <path>: yes or no.
present() {
  if [[ -e "$1" ]]; then echo yes; else echo no; fi
}

# assert_log_has <line> <name>: the docker log has exactly this line.
assert_log_has() {
  local found=no
  if grep -qxF -- "$1" <<<"$LOG"; then found=yes; fi
  assert_eq yes "$found" "$2"
}

# assert_log_lacks <text> <name>: no docker call contains the text.
assert_log_lacks() {
  local found=no
  if [[ "$LOG" == *"$1"* ]]; then found=yes; fi
  assert_eq no "$found" "$2"
}

assert_nonzero() {
  local failed=no
  if [[ "$1" != 0 ]]; then failed=yes; fi
  assert_eq yes "$failed" "$2"
}

# assert_installed <name>: the compose files are all still there.
assert_installed() {
  local f missing=""
  for f in compose.yaml compose.override.yaml .env uninstall.sh updater.sh panel.env data; do
    [[ -e "$SCP_DIR/$f" ]] || missing+="$f "
  done
  assert_eq "" "$missing" "$1"
}

# --- default uninstall with --yes
fresh_install
run_uninstall --yes
D=$SCP_DIR
assert_eq 0 "$RC" "default: exit 0"
assert_log_has "compose down --remove-orphans" "default: compose down --remove-orphans"
assert_log_has "image ls --filter label=io.github.daniel-vuky.server-control-panel.image=panel --format {{.Repository}}:{{.Tag}} {{.ID}}" \
  "default: lists images by the panel label"
assert_log_has "image rm $REPO:latest" "default: removes latest by reference"
assert_log_has "image rm $REPO:sha-abc1234" "default: removes the sha tag by reference"
assert_log_has "image rm 5555cccc6666" "default: removes the untagged image by ID"
assert_log_lacks "image rm 1111aaaa2222" "default: no bare ID for latest"
assert_log_lacks "image rm 3333bbbb4444" "default: no bare ID for the sha tag"
assert_log_lacks "image rm <none>" "default: never removes <none>:<none> by name"
assert_log_lacks "docker:29-cli" "default: leaves docker:29-cli alone"
assert_eq "compose down --remove-orphans" "$(grep -m1 '^compose' <<<"$LOG")" "default: first compose call is down"
assert_eq "compose" "$(head -n 1 <<<"$LOG" | cut -d' ' -f1)" "default: containers are stopped before images are removed"
for f in compose.yaml compose.override.yaml .env uninstall.sh updater.sh; do
  assert_eq no "$(present "$D/$f")" "default: $f removed"
done
assert_eq yes "$(present "$D/panel.env")" "default: panel.env kept"
assert_eq yes "$(present "$D/data")" "default: data/ kept"
assert_contains "$OUT" "panel.env" "default: output names panel.env"
assert_contains "$OUT" "data/" "default: output names data/"
assert_contains "$OUT" "rm -rf $D" "default: output says how to remove the rest"

# --- after a default uninstall the panel counts as not installed
run_uninstall
assert_eq 0 "$RC" "after default: exit 0 without --yes or a tty"
assert_contains "$OUT" "not installed" "after default: says not installed"
assert_contains "$OUT" "rm -rf $D" "after default: still says how to remove panel.env and data/"
assert_eq "" "$LOG" "after default: no docker call"
assert_eq yes "$(present "$D/panel.env")" "after default: panel.env kept"

# --- --purge after that still removes the directory
run_uninstall --purge --yes
assert_eq 0 "$RC" "purge after default: exit 0"
assert_eq no "$(present "$D")" "purge after default: directory removed"
assert_eq "" "$LOG" "purge after default: no docker call"

# --- --purge --yes on a full install
fresh_install
run_uninstall --purge --yes
assert_eq 0 "$RC" "purge: exit 0"
assert_eq no "$(present "$SCP_DIR")" "purge: install directory gone"
assert_log_has "compose down --remove-orphans" "purge: compose down --remove-orphans"
assert_log_has "image rm $REPO:latest" "purge: removes images too"

# --- nothing installed: directory missing
fresh_install
rm -rf "$SCP_DIR"
run_uninstall --yes
assert_eq 0 "$RC" "missing dir: exit 0"
assert_contains "$OUT" "not installed" "missing dir: says not installed"
assert_eq "" "$LOG" "missing dir: no docker call"
run_uninstall --purge --yes
assert_eq 0 "$RC" "missing dir, purge: exit 0"
assert_contains "$OUT" "not installed" "missing dir, purge: says not installed"
assert_eq "" "$LOG" "missing dir, purge: no docker call"

# --- no tty, no --yes: refuses and removes nothing
fresh_install
run_uninstall
assert_nonzero "$RC" "no tty: non-zero"
assert_contains "$OUT" "--yes" "no tty: hints at --yes"
assert_eq "" "$LOG" "no tty: no docker call"
assert_installed "no tty: nothing removed"
run_uninstall --purge
assert_nonzero "$RC" "no tty, purge: non-zero"
assert_eq "" "$LOG" "no tty, purge: no docker call"
assert_installed "no tty, purge: nothing removed"

# --- answering n: exit 0, nothing removed
fresh_install
echo n >"$T/answer"
SCP_TTY="$T/answer" run_uninstall
assert_eq 0 "$RC" "answer n: exit 0"
assert_eq "" "$LOG" "answer n: no docker call"
assert_installed "answer n: nothing removed"
: >"$T/answer"
SCP_TTY="$T/answer" run_uninstall
assert_eq 0 "$RC" "empty answer: exit 0"
assert_eq "" "$LOG" "empty answer: no docker call"
assert_installed "empty answer: nothing removed"
echo maybe >"$T/answer"
SCP_TTY="$T/answer" run_uninstall --purge
assert_eq 0 "$RC" "answer maybe, purge: exit 0"
assert_installed "answer maybe, purge: nothing removed"

# --- answering y, and the confirmation text
fresh_install
echo y >"$T/answer"
SCP_TTY="$T/answer" run_uninstall
assert_eq 0 "$RC" "answer y: exit 0"
assert_contains "$OUT" "Remove the server control panel?" "answer y: asked the question"
assert_log_has "compose down --remove-orphans" "answer y: removes the panel"
assert_eq no "$(present "$SCP_DIR/compose.yaml")" "answer y: compose.yaml removed"
assert_eq yes "$(present "$SCP_DIR/panel.env")" "answer y: panel.env kept"
fresh_install
echo Y >"$T/answer"
SCP_TTY="$T/answer" run_uninstall --purge
assert_eq 0 "$RC" "purge answer Y: exit 0"
assert_contains "$OUT" "panel.env and data/" "purge confirmation names panel.env and data/"
assert_eq no "$(present "$SCP_DIR")" "purge answer Y: directory removed"

# --- an image that cannot be removed does not stop the uninstall
fresh_install
FAKE_DOCKER_IMAGE_RM_RC=1 run_uninstall --yes
assert_eq 0 "$RC" "image rm fails: exit 0"
assert_contains "$OUT" "warning" "image rm fails: warns"
assert_eq no "$(present "$SCP_DIR/compose.yaml")" "image rm fails: files still removed"

# --- compose down failing keeps the files so the uninstall can be retried
fresh_install
FAKE_DOCKER_COMPOSE_DOWN_RC=1 run_uninstall --yes
assert_nonzero "$RC" "down fails: non-zero"
assert_installed "down fails: nothing removed"
assert_log_lacks "image rm" "down fails: no image removed"

# --- the same untagged image listed twice is removed once
fresh_install
export FAKE_DOCKER_IMAGE_LS_OUT="<none>:<none> 5555cccc6666
$REPO:<none> 5555cccc6666"
run_uninstall --yes
assert_eq 1 "$(grep -cxF 'image rm 5555cccc6666' <<<"$LOG")" "duplicate untagged: removed once by ID"

# --- no panel images at all
fresh_install
export FAKE_DOCKER_IMAGE_LS_OUT=""
run_uninstall --yes
assert_eq 0 "$RC" "no images: exit 0"
assert_log_lacks "image rm" "no images: nothing to remove"
assert_eq no "$(present "$SCP_DIR/compose.yaml")" "no images: files removed"

# --- bad options and unsafe SCP_DIR
fresh_install
run_uninstall --frobnicate
assert_nonzero "$RC" "unknown option: non-zero"
assert_installed "unknown option: nothing removed"
SCP_DIR=/opt run_uninstall --purge --yes
assert_nonzero "$RC" "SCP_DIR=/opt: refused"
SCP_DIR=relative/dir run_uninstall --yes
assert_nonzero "$RC" "relative SCP_DIR: refused"
assert_eq "" "$LOG" "unsafe SCP_DIR: no docker call"

summary
