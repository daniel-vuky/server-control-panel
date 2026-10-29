#!/usr/bin/env bash
# Tests for the install flow of deploy/install.sh (main), run as root against
# fake-docker. Each case installs into a fresh, not yet existing directory, as
# the user 1000:1000 that sudo would name.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=deploy/test/lib.sh
source "$here/lib.sh"

INSTALL="$here/../install.sh"
# Roots under /tmp are refused, so everything lives under /srv (run.sh creates it).
T=$(mktemp -d -p /srv) || exit 1
# shellcheck disable=SC2317  # called by lib.sh at exit
test_cleanup() { rm -rf "$T"; }
mkdir -p "$T/bin" "$T/ws1" "$T/ws2" "$T/ws3"
install -m 0755 "$here/fake-docker" "$T/bin/docker"
export PATH="$T/bin:$PATH"

# fresh_case: forget every fake-docker setting, pick a new install directory
# that does not exist yet, and set the answers in the environment.
fresh_case() {
  unset "${!FAKE_DOCKER_@}" SCP_HEALTH_TIMEOUT
  export FAKE_DOCKER_LOG="$T/docker.log"
  SCP_DIR="$(mktemp -d -p "$T")/scp"
  export SCP_DIR SCP_SOURCE="$here/.." SUDO_UID=1000 SUDO_GID=1000 DOCKER_GID=998 SCP_TTY=/nonexistent
  export CF_ACCESS_TEAM_DOMAIN='HTTPS://MyTeam.cloudflareaccess.com/' CF_ACCESS_AUD=abc123 WORKSPACE_ROOTS="$T/ws1,$T/ws2"
}

# run_install [args...]: runs install.sh; sets RC, OUT, ERR and LOG (the
# docker calls of this run only).
run_install() {
  : >"$FAKE_DOCKER_LOG"
  bash "$INSTALL" "$@" >"$T/out" 2>"$T/err"
  RC=$?
  OUT=$(<"$T/out")
  ERR=$(<"$T/err")
  LOG=$(<"$FAKE_DOCKER_LOG")
}

# assert_line <file> <line> <name>: the file has exactly this line.
assert_line() {
  local found=no
  if grep -qxF -- "$2" "$1" 2>/dev/null; then found=yes; fi
  assert_eq yes "$found" "$3"
}

# assert_nonzero <rc> <name>
assert_nonzero() {
  local failed=no
  if [[ "$1" != 0 ]]; then failed=yes; fi
  assert_eq yes "$failed" "$2"
}

# assert_lacks <haystack> <needle> <name>
assert_lacks() {
  local present=no
  if [[ "$1" == *"$2"* ]]; then present=yes; fi
  assert_eq no "$present" "$3"
}

# same_file <a> <b>: prints "same" when the files are byte-identical.
same_file() {
  if cmp -s "$1" "$2"; then echo same; else echo different; fi
}

# yaml_of <override>: the override without its comment lines.
yaml_of() { sed '/^#/d' "$1"; }

# --- fresh install --no-start
fresh_case
run_install --no-start
D=$SCP_DIR
assert_eq 0 "$RC" "fresh: exit 0"
entries=$(cd "$D" && shopt -s dotglob && printf '%s\n' * | LC_ALL=C sort | tr '\n' ' ')
assert_eq ".env compose.override.yaml compose.yaml data panel.env uninstall.sh updater.sh " "$entries" \
  "fresh: installed layout, no leftover temp files"
assert_file_mode 750 "$D"
assert_file_mode 750 "$D/data"
assert_file_mode 600 "$D/panel.env"
assert_file_mode 755 "$D/updater.sh"
assert_file_mode 755 "$D/uninstall.sh"
for f in . panel.env .env compose.yaml compose.override.yaml uninstall.sh updater.sh data; do
  assert_eq 1000:1000 "$(stat -c %u:%g "$D/$f" 2>/dev/null)" "fresh: $f owned by 1000:1000"
done
assert_line "$D/panel.env" "CF_ACCESS_TEAM_DOMAIN=myteam.cloudflareaccess.com" "fresh: team domain normalised"
assert_line "$D/panel.env" "CF_ACCESS_AUD=abc123" "fresh: AUD written"
assert_line "$D/panel.env" "WORKSPACE_ROOTS=$T/ws1,$T/ws2" "fresh: roots written"
assert_line "$D/panel.env" "# PANEL_ADDR=127.0.0.1:8471" "fresh: commented PANEL_ADDR"
assert_line "$D/.env" "PANEL_TAG=latest" "fresh: .env PANEL_TAG"
assert_line "$D/.env" "UPDATE_INTERVAL=300" "fresh: .env UPDATE_INTERVAL"
assert_eq same "$(same_file "$here/../compose.yaml" "$D/compose.yaml")" "fresh: compose.yaml copied"
assert_eq same "$(same_file "$here/../updater.sh" "$D/updater.sh")" "fresh: updater.sh copied"
assert_eq same "$(same_file "$here/../uninstall.sh" "$D/uninstall.sh")" "fresh: uninstall.sh copied"
first=$(head -n 1 "$D/compose.override.yaml")
assert_eq "# Generated" "${first:0:11}" "fresh: override starts with the generated comment"
assert_eq "services:
  panel:
    user: \"1000:1000\"
    group_add: [\"998\"]
    volumes:
      - $T/ws1:$T/ws1:ro
      - $T/ws2:$T/ws2:ro" "$(yaml_of "$D/compose.override.yaml")" "fresh: override content"
assert_eq "" "$(grep -E '^compose (pull|up)' <<<"$LOG")" "fresh: --no-start neither pulls nor starts"

# --- re-run keeps panel.env and .env
# Edit both the way an owner would; cat > keeps owner and mode.
sed "s|^WORKSPACE_ROOTS=.*|&,$T/ws3|" "$D/panel.env" >"$T/edit" && cat "$T/edit" >"$D/panel.env"
sed 's|^PANEL_TAG=.*|PANEL_TAG=sha-abc1234|' "$D/.env" >"$T/edit" && cat "$T/edit" >"$D/.env"
cp "$D/panel.env" "$T/panel.env.edited"
cp "$D/.env" "$T/env.edited"
unset CF_ACCESS_TEAM_DOMAIN CF_ACCESS_AUD WORKSPACE_ROOTS
run_install --no-start
assert_eq 0 "$RC" "re-run: exit 0 without answers or a tty"
assert_eq same "$(same_file "$T/panel.env.edited" "$D/panel.env")" "re-run: panel.env untouched"
assert_eq same "$(same_file "$T/env.edited" "$D/.env")" "re-run: .env untouched"
assert_file_mode 600 "$D/panel.env"
assert_line "$D/compose.override.yaml" "      - $T/ws3:$T/ws3:ro" "re-run: the new root is mounted"
cp "$D/compose.override.yaml" "$T/override.second"
run_install --no-start
assert_eq 0 "$RC" "re-run twice: exit 0"
assert_eq same "$(same_file "$T/override.second" "$D/compose.override.yaml")" "re-run twice: override unchanged"
assert_eq 1 "$(grep -c -- "- $T/ws1:" "$D/compose.override.yaml")" "re-run twice: no duplicate mounts"

# --- every root gone on re-run
sed "s|^WORKSPACE_ROOTS=.*|WORKSPACE_ROOTS=$T/gone|" "$D/panel.env" >"$T/edit" && cat "$T/edit" >"$D/panel.env"
run_install --no-start
assert_nonzero "$RC" "no root left: fails"
assert_contains "$ERR" "no workspace root left" "no root left: says so"
assert_eq same "$(same_file "$T/override.second" "$D/compose.override.yaml")" "no root left: override not rewritten"

# --- no tty and missing answer
fresh_case
unset CF_ACCESS_AUD
run_install --no-start
assert_nonzero "$RC" "no tty: fails"
assert_contains "$ERR" "CF_ACCESS_AUD" "no tty: names the missing variable"
assert_eq no "$([[ -e "$SCP_DIR/panel.env" ]] && echo yes || echo no)" "no tty: no panel.env written"

# --- non-interactive roots that all are missing
fresh_case
export WORKSPACE_ROOTS="$T/gone"
run_install --no-start
assert_nonzero "$RC" "env roots all missing: fails"
assert_contains "$ERR" "no workspace root left" "env roots all missing: says so"
assert_eq no "$([[ -e "$SCP_DIR/panel.env" ]] && echo yes || echo no)" "env roots all missing: no panel.env written"

# --- prompt retries
fresh_case
unset CF_ACCESS_TEAM_DOMAIN CF_ACCESS_AUD WORKSPACE_ROOTS
printf '%s\n' bad.com myteam.cloudflareaccess.com zz abc123 '' >"$T/tty"
export SCP_TTY="$T/tty"
run_install --no-start
defaults=""
for d in /home /srv /opt /var/www; do
  if [[ -d "$d" ]]; then defaults="${defaults:+$defaults,}$d"; fi
done
assert_eq 0 "$RC" "prompts: exit 0 after retries"
assert_line "$SCP_DIR/panel.env" "CF_ACCESS_TEAM_DOMAIN=myteam.cloudflareaccess.com" "prompts: team domain"
assert_line "$SCP_DIR/panel.env" "CF_ACCESS_AUD=abc123" "prompts: AUD"
assert_line "$SCP_DIR/panel.env" "WORKSPACE_ROOTS=$defaults" "prompts: empty answer = default roots"
assert_contains "$ERR" "bad.com" "prompts: a bad team domain is reported"
assert_lacks "$OUT$ERR" "abc123" "prompts: the AUD is never echoed"

# --- prompt input ends early
fresh_case
unset CF_ACCESS_TEAM_DOMAIN CF_ACCESS_AUD WORKSPACE_ROOTS
printf '%s\n' bad.com >"$T/tty"
export SCP_TTY="$T/tty"
run_install --no-start
assert_nonzero "$RC" "prompts: end of input fails instead of looping"
assert_eq no "$([[ -e "$SCP_DIR/panel.env" ]] && echo yes || echo no)" "prompts: end of input writes no panel.env"

# --- AUD never echoed (non-interactive)
fresh_case
export CF_ACCESS_AUD=0123456789ABCDEF0042
run_install --no-start
assert_eq 0 "$RC" "aud: exit 0"
assert_line "$SCP_DIR/panel.env" "CF_ACCESS_AUD=0123456789abcdef0042" "aud: normalised"
assert_lacks "$OUT$ERR" "0123456789" "aud: not in the output"

# --- invalid answer in the environment
fresh_case
export CF_ACCESS_TEAM_DOMAIN=evil.com
run_install --no-start
assert_nonzero "$RC" "env: invalid team domain fails"
assert_contains "$ERR" "CF_ACCESS_TEAM_DOMAIN" "env: names the variable"

# --- compose version
for v in 5.6.1 v2.20.0 2.20.0-desktop.1; do
  fresh_case
  export FAKE_DOCKER_COMPOSE_VERSION_OUT=$v
  run_install --no-start
  assert_eq 0 "$RC" "compose $v accepted"
done
for v in 2.19.9 v1.29.2 garbage; do
  fresh_case
  export FAKE_DOCKER_COMPOSE_VERSION_OUT=$v
  run_install --no-start
  assert_nonzero "$RC" "compose $v refused"
  assert_contains "$ERR" "2.20" "compose $v: message names the minimum"
done
fresh_case
export FAKE_DOCKER_COMPOSE_VERSION_RC=1
run_install --no-start
assert_nonzero "$RC" "compose plugin missing: refused"

# --- bare root refused
fresh_case
unset SUDO_UID SUDO_GID
run_install --no-start
assert_nonzero "$RC" "bare root: refused"
assert_contains "$ERR" "normal user" "bare root: says to run as a normal user"
assert_eq no "$([[ -e "$SCP_DIR" ]] && echo yes || echo no)" "bare root: nothing created"
fresh_case
export SUDO_UID=0 SUDO_GID=0
run_install --no-start
assert_nonzero "$RC" "SUDO_UID=0: refused"
assert_contains "$ERR" "normal user" "SUDO_UID=0: says to run as a normal user"

# --- different owner refused
fresh_case
run_install --no-start
export SUDO_UID=1001 SUDO_GID=1001
run_install --no-start
assert_nonzero "$RC" "other owner: refused"
assert_contains "$ERR" "UID 1000" "other owner: names the owner"

# --- pre-created directory with .env but no panel.env (the e2e harness)
fresh_case
install -d -o 1000 -g 1000 -m 0750 "$SCP_DIR"
printf 'PANEL_IMAGE=localhost:5000/scp\nPANEL_TAG=e2e\n' >"$SCP_DIR/.env"
cp "$SCP_DIR/.env" "$T/env.precreated"
run_install --no-start
assert_eq 0 "$RC" "pre-created: exit 0"
assert_eq same "$(same_file "$T/env.precreated" "$SCP_DIR/.env")" "pre-created: .env kept"
assert_line "$SCP_DIR/panel.env" "CF_ACCESS_AUD=abc123" "pre-created: panel.env written from the answers"

# --- start and healthy
fresh_case
export FAKE_DOCKER_COMPOSE_PS_OUT=c0ffee FAKE_DOCKER_INSPECT_OUT=healthy
run_install
assert_eq 0 "$RC" "healthy: exit 0"
assert_contains "$LOG" "info" "healthy: the daemon is checked"
assert_eq "compose stop updater
compose pull
compose up -d --remove-orphans" "$(grep -E '^compose (stop|start|pull|up)' <<<"$LOG")" "healthy: stop the updater, pull, then up"
assert_contains "$LOG" "inspect -f {{.State.Health.Status}} c0ffee" "healthy: health read from the panel container"
assert_contains "$OUT" "$SCP_DIR" "summary: names the install directory"
assert_contains "$OUT" "docs/deploy.md" "summary: links the deploy docs"

# --- start but unhealthy
fresh_case
export FAKE_DOCKER_COMPOSE_PS_OUT=c0ffee FAKE_DOCKER_INSPECT_OUT=starting SCP_HEALTH_TIMEOUT=2
run_install
assert_nonzero "$RC" "unhealthy: fails"
assert_contains "$LOG" "compose logs --tail 30 panel" "unhealthy: shows the panel logs"

# --- daemon unreachable
fresh_case
export FAKE_DOCKER_INFO_RC=1
run_install
assert_nonzero "$RC" "no daemon: fails"
assert_contains "$ERR" "docker group" "no daemon: suggests the docker group"

# --- pull denied
for word in denied unauthorized; do
  fresh_case
  export FAKE_DOCKER_COMPOSE_PULL_RC=1 FAKE_DOCKER_COMPOSE_PULL_OUT="Error response from daemon: $word"
  run_install
  assert_nonzero "$RC" "pull $word: fails"
  assert_contains "$ERR" "public" "pull $word: GHCR hint"
  assert_contains "$ERR" "$word" "pull $word: pull output shown"
  assert_lacks "$LOG" "compose up" "pull $word: nothing started"
  assert_eq "compose stop updater
compose pull
compose start updater" "$(grep -E '^compose (stop|start|pull|up)' <<<"$LOG")" "pull $word: the updater is stopped first and started again"
done

# --- the updater stop fails (nothing installed or running yet)
fresh_case
export FAKE_DOCKER_COMPOSE_PS_OUT=c0ffee FAKE_DOCKER_INSPECT_OUT=healthy FAKE_DOCKER_COMPOSE_STOP_RC=1
run_install
assert_eq 0 "$RC" "stop updater fails: install goes on"
assert_contains "$LOG" "compose up -d --remove-orphans" "stop updater fails: the panel is started"

# --- up fails
fresh_case
export FAKE_DOCKER_COMPOSE_UP_RC=1
run_install
assert_nonzero "$RC" "up fails: fails"
assert_eq "compose stop updater
compose pull
compose up -d --remove-orphans
compose start updater" "$(grep -E '^compose (stop|start|pull|up)' <<<"$LOG")" "up fails: the updater is started again"

# --- panel container missing
fresh_case
export FAKE_DOCKER_INSPECT_OUT=healthy
run_install
assert_nonzero "$RC" "no container: fails"
assert_contains "$LOG" "compose logs --tail 30 panel" "no container: shows the logs"
assert_lacks "$LOG" "inspect" "no container: no docker inspect ''"

summary
