#!/usr/bin/env bash
# End to end: install.sh, the updater and uninstall.sh against a real Docker
# daemon (the spec's "End to end before the PR", issue #75).
#
#   make deploy-e2e            (or: bash deploy/test/e2e.sh)
#
# Manual, not in CI: it needs privileged containers and network access. It
# runs on the host and drives everything through docker. The deploy scripts
# only ever run inside a throwaway docker:29-dind container ("dind") with a
# registry:2 container sharing its network, so inside dind the registry is
# localhost:5000. The host builds the panel image twice, pushes it to that
# registry through dind's published port, and dind's daemon pulls it from
# there exactly as it would from GHCR, so install.sh and the updater run
# unchanged.
#
# Everything it creates on the host is named scp-e2e-<pid>-* and is removed on
# exit, also on failure or Ctrl-C: the two containers with their anonymous
# volumes, the built image tags, and the dind and registry images when this
# run pulled them. Only the host's docker build cache stays.
#
# Knob: SCP_E2E_PORT, the host port the registry is published on (default
# 5000; macOS AirPlay Receiver often holds 5000).
#
# Not named *_test.sh, so deploy/test/run.sh (make deploy-test) skips it.

# The check_<n> functions are called indirectly, by run_check.
# shellcheck disable=SC2317
set -uo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
PORT=${SCP_E2E_PORT:-5000}
ID=scp-e2e-$$
DIND=$ID-dind
REGISTRY=$ID-registry
BUILD_REPO=$ID-panel
DIND_IMAGE=docker:29-dind
REGISTRY_IMAGE=registry:2
HOST_REF=localhost:$PORT/panel:latest
REF=localhost:5000/panel:latest
SHA_REF=localhost:5000/panel:sha-test
DIR=/opt/server-control-panel
LABEL=io.github.daniel-vuky.server-control-panel.image=panel
TEAM=scp-e2e.cloudflareaccess.com
AUD=0123456789abcdef0123456789abcdef
ROOT1=/home
ROOT2=/srv/scp-e2e
# The answers are only read when panel.env does not exist yet.
INSTALL="SCP_SOURCE=/src/deploy SCP_TTY=/dev/null CF_ACCESS_TEAM_DOMAIN=$TEAM CF_ACCESS_AUD=$AUD WORKSPACE_ROOTS=$ROOT1 bash /src/deploy/install.sh"
# The .env written before the first install (and again before the re-install
# in check 7); install.sh keeps it.
ENV_TEXT='# Written by deploy/test/e2e.sh.\nPANEL_IMAGE=localhost:5000/panel\nPANEL_TAG=latest\nUPDATE_INTERVAL=5\n'
REVISION=$(git -C "$REPO" rev-parse HEAD 2>/dev/null || echo unknown)

TITLES=(
  ""
  "install as u1000: healthy, /api/health answers, runs as 1000:1000 with the socket group, roots read-only"
  "re-run after adding a root to panel.env: root mounted, panel.env and .env unchanged"
  "bad AUD in panel.env: install.sh fails and prints the panel log; restored, re-run is healthy"
  "install as root without sudo is refused"
  "updater: unchanged image leaves the panel alone; a new image under the same tag runs within 30s, old panel image pruned, alpine kept"
  "updater: a panel stopped by the owner stays stopped for two rounds, no update, no prune; started again it is healthy"
  "uninstall.sh (installed copy) with latest and sha-test on one image keeps panel.env and data/; --purge deletes the directory"
)

TMPD=$(mktemp -d "${TMPDIR:-/tmp}/scp-e2e.XXXXXX")
BUILT=()
HOST_REF_CREATED=0
PULLED_IMAGES=()
RESULTS=()
FAILED=0
OUT=""
REASON=""

cleanup() {
  local img
  echo
  echo "=== cleanup"
  docker rm -f -v "$REGISTRY" "$DIND" >/dev/null 2>&1 || true
  if [[ "$HOST_REF_CREATED" == 1 ]]; then
    docker image rm "$HOST_REF" >/dev/null 2>&1 || true
  fi
  for img in ${BUILT[@]+"${BUILT[@]}"} ${PULLED_IMAGES[@]+"${PULLED_IMAGES[@]}"}; do
    docker image rm "$img" >/dev/null 2>&1 || true
  done
  rm -rf -- "$TMPD"
  echo "removed $DIND, $REGISTRY and the images this run created"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

# --- helpers ----------------------------------------------------------------

indent() { sed 's/^/    | /'; }

show() {
  if [[ -n "$OUT" ]]; then
    printf '%s\n' "$OUT" | indent
  fi
}

# as_user <command>: runs the command in dind as u1000 through su, which keeps
# the supplementary docker group (docker exec -u 1000 would drop it). Leaves
# the combined output in OUT and returns the command's status.
as_user() {
  local rc
  OUT=$(docker exec "$DIND" su u1000 -c "cd / && $1" 2>&1)
  rc=$?
  show
  return "$rc"
}

# as_root <command>: the same, as root in dind.
as_root() {
  local rc
  OUT=$(docker exec "$DIND" sh -c "$1" 2>&1)
  rc=$?
  show
  return "$rc"
}

# q <cmd...>: a quiet query in dind; stdout only.
q() { docker exec "$DIND" "$@" 2>/dev/null; }

# need <what> <cmd...>: runs one step of a check and prints ok/not ok. On
# failure it records <what> as the reason and returns 1.
need() {
  local what=$1
  shift
  if "$@"; then
    echo "    ok: $what"
    return 0
  fi
  echo "    not ok: $what"
  REASON=$what
  return 1
}

fails() { ! "$@"; }

eq() {
  if [[ "$1" == "$2" ]]; then
    return 0
  fi
  echo "      expected [$1], got [$2]"
  return 1
}

out_has() {
  if [[ "$OUT" == *"$1"* ]]; then
    return 0
  fi
  echo "      output does not contain [$1]"
  return 1
}

out_lacks() {
  if [[ "$OUT" != *"$1"* ]]; then
    return 0
  fi
  echo "      output contains [$1]"
  return 1
}

panel_cid() {
  q docker ps -q --filter label=com.docker.compose.project=server-control-panel \
    --filter label=com.docker.compose.service=panel | head -n 1
}

project_containers() {
  q docker ps -aq --filter label=com.docker.compose.project=server-control-panel
}

inspect() { q docker inspect -f "$2" "$1"; }

image_id() { q docker image inspect -f '{{.Id}}' "$1"; }

has_image() { image_id "$1" >/dev/null; }

labelled_images() { q docker image ls -q --filter "label=$LABEL"; }

healthy() {
  local cid
  cid=$(panel_cid)
  [[ -n "$cid" ]] || {
    echo "      no running panel container"
    return 1
  }
  eq healthy "$(inspect "$cid" '{{.State.Health.Status}}')"
}

# health_answers <version>: GET /api/health inside dind answers ok with that
# version.
health_answers() {
  local body
  body=$(q wget -qO- http://127.0.0.1:8471/api/health) || {
    echo "      no answer on 127.0.0.1:8471"
    return 1
  }
  echo "      $body"
  [[ "$body" == *'"status":"ok"'* && "$body" == *"\"version\":\"$1\""* ]]
}

# wait_for <seconds> <cmd...>: retries the command every second.
wait_for() {
  local deadline=$((SECONDS + $1))
  shift
  until "$@" >/dev/null 2>&1; do
    if ((SECONDS >= deadline)); then
      "$@"
      return 1
    fi
    sleep 1
  done
}

# roots_read_only <root...>: every mount of the panel except the docker socket
# and /data is read-only, and each given root is mounted exactly once.
roots_read_only() {
  local m dest rw r rc=0
  m=$(inspect "$(panel_cid)" '{{range .Mounts}}{{.Destination}} {{.RW}}{{"\n"}}{{end}}')
  printf '%s\n' "$m" | sed '/^$/d; s/^/      mount: /'
  while read -r dest rw; do
    [[ -n "$dest" ]] || continue
    case $dest in
      /var/run/docker.sock | /data) continue ;;
    esac
    if [[ "$rw" != false ]]; then
      echo "      $dest is writable"
      rc=1
    fi
  done <<<"$m"
  for r in "$@"; do
    if [[ "$(grep -c "^$r " <<<"$m")" != 1 ]]; then
      echo "      $r is not mounted exactly once"
      rc=1
    fi
  done
  return "$rc"
}

file_state() { q sh -c "sha256sum $DIR/panel.env $DIR/.env && stat -c '%n %u:%g %a' $DIR/panel.env $DIR/.env"; }

# build_push <tag> <VERSION>: builds the panel image on the host and pushes it
# to the registry in dind as panel:latest.
build_push() {
  local tag=$BUILD_REPO:$1 log=$TMPD/build-$1.log
  echo "    building $tag (VERSION=$2)"
  BUILT+=("$tag")
  if ! docker build --build-arg VERSION="$2" --build-arg REVISION="$REVISION" -t "$tag" "$REPO" >"$log" 2>&1; then
    tail -n 40 "$log" | indent
    return 1
  fi
  HOST_REF_CREATED=1
  docker tag "$tag" "$HOST_REF" || return 1
  echo "    pushing $HOST_REF"
  if ! docker push --quiet "$HOST_REF" 2>&1 | indent; then
    return 1
  fi
  docker image rm "$HOST_REF" >/dev/null
}

record() {
  echo "$1"
  RESULTS+=("$1")
}

# run_check <n>: runs check_<n> and records PASS or FAIL with the reason.
run_check() {
  local n=$1
  echo
  echo "=== check $n: ${TITLES[$n]}"
  REASON=""
  if "check_$n"; then
    record "PASS  $n  ${TITLES[$n]}"
  else
    FAILED=1
    record "FAIL  $n  ${TITLES[$n]}: ${REASON:-see above}"
  fi
}

# skip_all <from> <reason>: records checks <from>..7 as SKIP.
skip_all() {
  local n
  for ((n = $1; n <= 7; n++)); do
    record "SKIP  $n  ${TITLES[$n]}: $2"
  done
}

summary() {
  local line
  echo
  echo "=== summary"
  for line in ${RESULTS[@]+"${RESULTS[@]}"}; do
    echo "$line"
  done
}

# setup_failed <reason>: nothing could be verified; every check is a SKIP and
# the run fails.
setup_failed() {
  echo "setup failed: $1" >&2
  skip_all 1 "setup failed: $1"
  summary
  exit 1
}

# --- setup ------------------------------------------------------------------

pull_if_missing() {
  if ! docker image inspect "$1" >/dev/null 2>&1; then
    docker pull -q "$1" >/dev/null || return 1
    PULLED_IMAGES+=("$1")
  fi
}

setup() {
  local gid grp

  echo "=== setup ($ID)"
  docker info >/dev/null 2>&1 || setup_failed "cannot reach the host docker daemon"
  if (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then
    setup_failed "host port 127.0.0.1:$PORT is taken (macOS AirPlay Receiver often holds 5000): free it or set SCP_E2E_PORT"
  fi
  if docker image inspect "$HOST_REF" >/dev/null 2>&1; then
    setup_failed "$HOST_REF already exists on the host and this run would replace it: remove it or set SCP_E2E_PORT"
  fi
  pull_if_missing "$DIND_IMAGE" || setup_failed "cannot pull $DIND_IMAGE"
  pull_if_missing "$REGISTRY_IMAGE" || setup_failed "cannot pull $REGISTRY_IMAGE"

  echo "    starting $DIND and $REGISTRY"
  docker run -d --privileged --name "$DIND" -p "127.0.0.1:$PORT:5000" \
    -v "$REPO:/src:ro" "$DIND_IMAGE" >/dev/null ||
    setup_failed "cannot start a privileged $DIND_IMAGE container"
  docker run -d --name "$REGISTRY" --network "container:$DIND" "$REGISTRY_IMAGE" >/dev/null ||
    setup_failed "cannot start $REGISTRY_IMAGE in dind's network"
  echo "    waiting for the daemon in dind"
  if ! wait_for 90 docker exec "$DIND" docker info; then
    docker logs --tail 30 "$DIND" 2>&1 | indent
    setup_failed "the docker daemon in dind did not come up within 90s"
  fi
  wait_for 30 docker exec "$DIND" wget -qO- http://localhost:5000/v2/ ||
    setup_failed "the registry did not answer on localhost:5000 inside dind"

  echo "    preparing dind: packages, u1000, sudo, $DIR and .env"
  as_root "apk add --no-cache bash curl coreutils sudo >/dev/null" ||
    setup_failed "apk add failed in dind"
  gid=$(q stat -c %g /var/run/docker.sock)
  [[ "$gid" =~ ^[0-9]+$ && "$gid" != 0 ]] ||
    setup_failed "the docker socket in dind has group '$gid', not a dedicated group"
  as_root "addgroup -g 1000 u1000 && adduser -D -u 1000 -G u1000 u1000" ||
    setup_failed "cannot create u1000"
  grp=$(q getent group "$gid" | cut -d: -f1)
  if [[ -z "$grp" ]]; then
    grp=scp-docker
    as_root "addgroup -g $gid $grp" || setup_failed "cannot create a group with GID $gid"
  fi
  as_root "addgroup u1000 $grp" || setup_failed "cannot add u1000 to $grp"
  as_root "echo 'u1000 ALL=(ALL) NOPASSWD: ALL' >/etc/sudoers.d/u1000 && chmod 0440 /etc/sudoers.d/u1000" ||
    setup_failed "cannot give u1000 sudo"
  as_root "install -d -o 1000 -g 1000 -m 0750 $DIR && printf '$ENV_TEXT' >$DIR/.env && chown 1000:1000 $DIR/.env && mkdir -p $ROOT2" ||
    setup_failed "cannot prepare $DIR"
  as_user "id && sudo -n true && docker info --format 'daemon {{.ServerVersion}}, storage {{.Driver}}'" ||
    setup_failed "u1000 cannot reach the daemon or sudo"
  echo "    socket GID $gid (group $grp)"
  SOCK_GID=$gid

  echo "    pulling alpine in dind (the unrelated image the prune must keep)"
  as_root "docker pull -q alpine" || setup_failed "cannot pull alpine in dind"

  build_push v1 e2e-1 || setup_failed "building or pushing the first image failed"
}

# --- checks -----------------------------------------------------------------

check_1() {
  local cid
  need "install.sh as u1000 exits 0" as_user "$INSTALL" || return 1
  need "the panel is healthy" healthy || return 1
  need "GET /api/health answers inside dind (version e2e-1)" health_answers e2e-1 || return 1
  cid=$(panel_cid)
  need "the panel runs as 1000:1000" eq 1000:1000 "$(inspect "$cid" '{{.Config.User}}')" || return 1
  need "group_add is the socket GID $SOCK_GID" eq "[\"$SOCK_GID\"]" "$(inspect "$cid" '{{json .HostConfig.GroupAdd}}')" || return 1
  need "every root mount is read-only ($ROOT1 mounted once)" roots_read_only "$ROOT1" || return 1
  need "the panel drops every capability" eq '["ALL"]' "$(inspect "$cid" '{{json .HostConfig.CapDrop}}')" || return 1
  need "the panel has no-new-privileges" eq '["no-new-privileges:true"]' "$(inspect "$cid" '{{json .HostConfig.SecurityOpt}}')" || return 1
  need "the updater runs" eq running "$(inspect server-control-panel-updater-1 '{{.State.Status}}')" || return 1
  need "the updater mounts $DIR read-only" eq false \
    "$(inspect server-control-panel-updater-1 "{{range .Mounts}}{{if eq .Destination \"$DIR\"}}{{.RW}}{{end}}{{end}}")" || return 1
}

check_2() {
  local before after
  need "add $ROOT2 to WORKSPACE_ROOTS in panel.env (edited in place as u1000)" \
    as_user "sed 's|^WORKSPACE_ROOTS=.*|WORKSPACE_ROOTS=$ROOT1,$ROOT2|' $DIR/panel.env >/tmp/scp-e2e-pe && cat /tmp/scp-e2e-pe >$DIR/panel.env" || return 1
  before=$(file_state)
  need "re-run install.sh exits 0" as_user "$INSTALL" || return 1
  need "install.sh keeps panel.env" out_has "Keeping $DIR/panel.env" || return 1
  after=$(file_state)
  printf '%s\n' "$after" | sed 's/^/      /'
  need "panel.env and .env unchanged (content, owner, mode)" eq "$before" "$after" || return 1
  need "the panel is healthy" healthy || return 1
  need "$ROOT2 is mounted read-only, $ROOT1 still, no duplicates" roots_read_only "$ROOT1" "$ROOT2" || return 1
}

check_3() {
  local good restored
  good=$(q sha256sum "$DIR/panel.env")
  need "set a bad AUD in panel.env (edited in place as u1000)" \
    as_user "cp $DIR/panel.env /tmp/scp-e2e-panel.env && sed 's/^CF_ACCESS_AUD=.*/CF_ACCESS_AUD=not-a-hex-aud/' /tmp/scp-e2e-panel.env >$DIR/panel.env" || return 1
  need "install.sh exits non-zero" fails as_user "SCP_HEALTH_TIMEOUT=30 $INSTALL" || return 1
  need "it prints the panel's log" out_has "Last lines of the panel log" || return 1
  need "the log shows the panel refusing the AUD" out_has "CF_ACCESS_AUD: application AUD must be a hex string" || return 1
  need "restore panel.env" as_user "cat /tmp/scp-e2e-panel.env >$DIR/panel.env" || return 1
  restored=$(q sha256sum "$DIR/panel.env")
  need "panel.env is back to the good content" eq "$good" "$restored" || return 1
  need "re-run install.sh exits 0" as_user "$INSTALL" || return 1
  need "the panel is healthy" healthy || return 1
  need "GET /api/health answers" health_answers e2e-1 || return 1
}

check_4() {
  local before after
  before=$(file_state)
  need "install.sh as root (no SUDO_UID) exits non-zero" fails as_root "cd / && $INSTALL" || return 1
  need "it says the panel must run as a normal user" out_has "the panel must run as a normal user, not root" || return 1
  after=$(file_state)
  need "panel.env and .env untouched" eq "$before" "$after" || return 1
  # Check 3's last install recreated the panel; give it time to pass its
  # health check.
  need "the panel is healthy" wait_for 60 healthy || return 1
}

updater_log_count() {
  q docker logs server-control-panel-updater-1 2>&1 | grep -c "$1"
}

# up_to_date_rounds <count before>: at least two more "up to date" rounds.
up_to_date_rounds() { (($(updater_log_count 'panel is up to date') >= $1 + 2)); }

check_5() {
  local old cur want start elapsed=-1 cid quiet updates
  cid=$(panel_cid)
  quiet=$(updater_log_count 'panel is up to date')
  updates=$(updater_log_count 'updating panel')
  need "with an unchanged image the updater logs 'panel is up to date' in two more rounds" \
    wait_for 30 up_to_date_rounds "$quiet" || return 1
  need "those rounds did not update the panel" eq "$updates" "$(updater_log_count 'updating panel')" || return 1
  need "the panel container is the same ($cid)" eq "$cid" "$(panel_cid)" || return 1
  old=$(inspect "$cid" '{{.Image}}')
  echo "      running image before: $old"
  need "alpine is present before the update" has_image alpine || return 1
  need "build VERSION=e2e-2 and push it as panel:latest" build_push v2 e2e-2 || return 1
  start=$SECONDS
  while ((SECONDS - start <= 30)); do
    cur=$(inspect "$(panel_cid)" '{{.Image}}')
    want=$(image_id "$REF")
    if [[ -n "$cur" && "$cur" == "$want" && "$cur" != "$old" ]] && ! has_image "$old"; then
      elapsed=$((SECONDS - start))
      break
    fi
    sleep 1
  done
  echo "      running image after:  ${cur:-none} (panel:latest in dind: ${want:-none})"
  need "within 30s the panel runs the new image and the old one is gone (took ${elapsed}s)" test "$elapsed" -ge 0 || return 1
  need "the panel answers with version e2e-2" wait_for 60 health_answers e2e-2 || return 1
  need "the panel is healthy" wait_for 60 healthy || return 1
  need "alpine is still present" has_image alpine || return 1
  q docker logs --tail 8 server-control-panel-updater-1 | indent
}

# stopped_rounds <count before>: at least two more "panel is stopped" rounds.
stopped_rounds() { (($(updater_log_count 'panel is stopped, leaving it') >= $1 + 2)); }

# panel_any_cid: the panel container, running or not.
panel_any_cid() {
  q docker ps -aq --filter label=com.docker.compose.project=server-control-panel \
    --filter label=com.docker.compose.service=panel | head -n 1
}

check_6() {
  local cid stopped updates prunes
  cid=$(panel_cid)
  stopped=$(updater_log_count 'panel is stopped, leaving it')
  updates=$(updater_log_count 'updating panel')
  prunes=$(updater_log_count 'pruning old panel images')
  need "docker compose stop panel as u1000" as_user "cd $DIR && docker compose stop panel" || return 1
  need "the updater logs 'panel is stopped, leaving it' in two rounds" \
    wait_for 30 stopped_rounds "$stopped" || return 1
  need "the panel container is still stopped ($cid)" eq "$cid exited" \
    "$(panel_any_cid) $(inspect "$cid" '{{.State.Status}}')" || return 1
  need "no panel container is running" eq "" "$(panel_cid)" || return 1
  need "those rounds did not update the panel" eq "$updates" "$(updater_log_count 'updating panel')" || return 1
  need "those rounds did not prune" eq "$prunes" "$(updater_log_count 'pruning old panel images')" || return 1
  q docker logs --tail 4 server-control-panel-updater-1 | indent
  need "docker compose start panel as u1000" as_user "cd $DIR && docker compose start panel" || return 1
  need "the panel is healthy" wait_for 60 healthy || return 1
  need "it is the same container" eq "$cid" "$(panel_cid)" || return 1
}

check_7() {
  local id_latest id_sha
  need "tag the running image as sha-test" q docker tag "$REF" "$SHA_REF" || return 1
  id_latest=$(image_id "$REF")
  id_sha=$(image_id "$SHA_REF")
  echo "      latest:   $id_latest"
  echo "      sha-test: $id_sha"
  need "latest and sha-test share one image ID" eq "$id_latest" "$id_sha" || return 1

  need "the installed uninstall.sh --yes as u1000 exits 0" as_user "$DIR/uninstall.sh --yes" || return 1
  need "sha-test was removed by reference" out_has "Untagged: $SHA_REF" || return 1
  need "latest was removed by reference" out_has "Untagged: $REF" || return 1
  need "no image removal was skipped" out_lacks "could not remove" || return 1
  need "no panel image is left" eq "" "$(labelled_images)" || return 1
  need "no project container is left" eq "" "$(project_containers)" || return 1
  need "panel.env and data/ remain" q test -f "$DIR/panel.env" -a -d "$DIR/data" || return 1
  need "compose.yaml, the override, .env, uninstall.sh and updater.sh are gone" \
    q sh -c "! ls $DIR/compose.yaml $DIR/compose.override.yaml $DIR/.env $DIR/uninstall.sh $DIR/updater.sh 2>/dev/null" || return 1

  # The default uninstall removed the installed uninstall.sh and .env, so
  # reinstall (reusing panel.env) to run the installed copy again for --purge.
  need "rewrite .env as u1000 and reinstall" as_user "printf '$ENV_TEXT' >$DIR/.env && $INSTALL" || return 1
  need "the reinstall reuses panel.env" out_has "Keeping $DIR/panel.env" || return 1
  need "the panel is healthy" healthy || return 1
  need "tag the running image as sha-test again" q docker tag "$REF" "$SHA_REF" || return 1
  need "the installed uninstall.sh --purge --yes as u1000 exits 0" as_user "$DIR/uninstall.sh --purge --yes" || return 1
  need "it deletes $DIR with sudo (/opt is root-owned)" out_has "Removing $DIR with sudo" || return 1
  need "$DIR is gone" fails q test -e "$DIR" || return 1
  need "no panel image is left" eq "" "$(labelled_images)" || return 1
  need "no project container is left" eq "" "$(project_containers)" || return 1
}

# --- main -------------------------------------------------------------------

setup
run_check 1
if [[ "$FAILED" == 1 ]]; then
  skip_all 2 "check 1 (install) failed"
else
  for n in 2 3 4 5 6 7; do
    run_check "$n"
  done
fi
summary
exit "$FAILED"
