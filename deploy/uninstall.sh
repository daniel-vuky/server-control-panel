#!/usr/bin/env bash
# Remove the server control panel installed by install.sh.
#
#   /opt/server-control-panel/uninstall.sh [--yes] [--purge]
#   curl -fsSL https://raw.githubusercontent.com/daniel-vuky/server-control-panel/main/deploy/uninstall.sh | bash -s -- --purge
#
# By default the containers, the panel images and the compose files go, while
# panel.env and data/ stay so a later install picks up where this one left
# off. --purge deletes the whole install directory as well. The docker:29-cli
# image is left alone: other things on the host may use it.
#
# Everything lives in functions and `main` is called on the last line, so a
# truncated download runs nothing. Tests source this file with SCP_LIB=1 to
# reach the functions without running main.

PANEL_IMAGE_LABEL=io.github.daniel-vuky.server-control-panel.image=panel
# The files install.sh refreshes on every run; panel.env and data/ are not
# among them.
COMPOSE_FILES=(compose.yaml compose.override.yaml .env uninstall.sh updater.sh)

die() {
  echo "uninstall.sh: $*" >&2
  exit 1
}

# confirm <question>: succeeds when the owner answers y or yes on the terminal.
# Fails when there is nothing to read an answer from, and when the answer is
# anything else. Opens SCP_TTY (default /dev/tty) once on fd 3.
confirm() {
  local reply=""
  printf '%s [y/N] ' "$1" >&2
  IFS= read -r -u 3 reply || true
  [[ -n "$reply" ]] || echo >&2
  [[ "${reply,,}" == y || "${reply,,}" == yes ]]
}

# remove_images: removes every image that carries the panel label. Tagged
# images go by reference: removing by ID fails with "referenced in multiple
# repositories" when latest and a sha- tag name the same image, which is
# normal after a rollback. Only untagged images go by ID. An image that cannot
# be removed (a container still uses it) is reported and skipped.
remove_images() {
  local ref id rows seen=" "
  rows=$(docker image ls --filter "label=$PANEL_IMAGE_LABEL" --format '{{.Repository}}:{{.Tag}} {{.ID}}') ||
    die "could not list the panel images"
  while read -r ref id; do
    [[ -n "$ref" ]] || continue
    if [[ "$ref" == "<none>:"* || "$ref" == *":<none>" ]]; then
      [[ -n "$id" && "$seen" != *" $id "* ]] || continue
      seen+="$id "
      ref=$id
    fi
    docker image rm "$ref" || echo "warning: could not remove the image $ref, leaving it" >&2
  done <<<"$rows"
}

# remove_dir <dir>: deletes the directory, with sudo only when the user is not
# root and cannot write to the parent.
remove_dir() {
  if [[ "$(id -u)" == 0 || -w "$(dirname "$1")" ]]; then
    rm -rf -- "$1"
  else
    echo "Removing $1 with sudo"
    sudo rm -rf -- "$1"
  fi
}

# keep_message <dir>: what stays behind after a default uninstall and how to
# remove it.
keep_message() {
  cat <<EOF
Kept $1/panel.env (your Cloudflare settings) and $1/data/ so a later install can reuse them.
To remove them as well: sudo rm -rf $1
EOF
}

main() {
  set -euo pipefail
  local yes=0 purge=0 arg installed=0 question

  for arg in "$@"; do
    case $arg in
      --yes) yes=1 ;;
      --purge) purge=1 ;;
      *) die "unknown option '$arg' (the options are --yes and --purge)" ;;
    esac
  done

  SCP_DIR=${SCP_DIR:-/opt/server-control-panel}
  [[ "$SCP_DIR" == /* ]] || die "SCP_DIR must be an absolute path"
  while [[ "$SCP_DIR" == */ ]]; do SCP_DIR=${SCP_DIR%/}; done
  # A purge deletes SCP_DIR whole: refuse the root and top-level directories.
  [[ "$SCP_DIR" == /*/* ]] || die "SCP_DIR '$SCP_DIR' is too close to the root to remove; it is normally /opt/server-control-panel"

  if [[ -f "$SCP_DIR/compose.yaml" ]]; then
    installed=1
  fi
  if [[ "$installed" == 0 ]]; then
    if [[ ! -e "$SCP_DIR" ]]; then
      echo "The server control panel is not installed ($SCP_DIR does not exist)."
      return 0
    fi
    if [[ "$purge" == 0 ]]; then
      echo "The server control panel is not installed (no compose.yaml in $SCP_DIR)."
      if [[ -e "$SCP_DIR/panel.env" || -e "$SCP_DIR/data" ]]; then
        keep_message "$SCP_DIR"
      fi
      return 0
    fi
  fi

  if [[ "$purge" == 1 ]]; then
    question="Remove the server control panel and delete $SCP_DIR, including panel.env and data/?"
  else
    question="Remove the server control panel? panel.env and data/ in $SCP_DIR are kept."
  fi
  if [[ "$yes" == 0 ]]; then
    if ! { exec 3<"${SCP_TTY:-/dev/tty}"; } 2>/dev/null; then
      die "no terminal to ask for confirmation: run it from a terminal, or pass --yes"
    fi
    if ! confirm "$question"; then
      echo "Cancelled; nothing was removed."
      return 0
    fi
  fi

  if [[ "$installed" == 1 ]]; then
    command -v docker >/dev/null 2>&1 || die "docker is not installed or not on PATH, cannot stop the panel"
    cd "$SCP_DIR"
    echo "Stopping the panel..."
    docker compose down --remove-orphans
    echo "Removing the panel images..."
    remove_images
    for arg in "${COMPOSE_FILES[@]}"; do
      rm -f -- "$SCP_DIR/$arg"
    done
    cd /
  fi

  if [[ "$purge" == 1 ]]; then
    remove_dir "$SCP_DIR"
    echo "The server control panel is removed, and $SCP_DIR is deleted."
  else
    echo "The server control panel is removed."
    keep_message "$SCP_DIR"
  fi
}

[[ "${SCP_LIB:-}" == 1 ]] || main "$@"
