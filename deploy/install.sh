#!/usr/bin/env bash
# Install the server control panel as a docker compose project.
#
#   curl -fsSL https://raw.githubusercontent.com/daniel-vuky/server-control-panel/main/deploy/install.sh | bash
#
# Everything lives in functions and `main` is called on the last line, so a
# truncated download runs nothing. Tests source this file with SCP_LIB=1 to
# reach the functions without running main.

# trim_space <string>: prints the string without leading or trailing whitespace.
trim_space() {
  local s=$1
  s=${s#"${s%%[![:space:]]*}"}
  s=${s%"${s##*[![:space:]]}"}
  printf '%s' "$s"
}

# normalize_team_domain <raw>: prints the Cloudflare Access team domain in the
# form the panel expects, or returns 1 with a message on stderr. The rules are
# those of NormalizeTeamDomain in internal/auth/access.go: trim, lower-case,
# strip one leading https:// and one trailing /, then require
# <letters, digits, hyphens>.cloudflareaccess.com.
normalize_team_domain() {
  local d
  d=$(trim_space "${1-}")
  d=${d,,}
  d=${d#https://}
  d=${d%/}
  if [[ ! "$d" =~ ^[a-z0-9-]+\.cloudflareaccess\.com$ ]]; then
    echo "team domain '${1-}' must look like myteam.cloudflareaccess.com" >&2
    return 1
  fi
  printf '%s\n' "$d"
}

# normalize_aud <raw>: prints the Cloudflare Access application AUD tag, or
# returns 1 with a message on stderr. Same rules as NormalizeAUD in
# internal/auth/access.go: trim, lower-case, non-empty, hex only.
normalize_aud() {
  local a
  a=$(trim_space "${1-}")
  a=${a,,}
  if [[ -z "$a" ]]; then
    echo "application AUD is empty" >&2
    return 1
  fi
  if [[ ! "$a" =~ ^[0-9a-f]+$ ]]; then
    echo "application AUD must be a hex string" >&2
    return 1
  fi
  printf '%s\n' "$a"
}

# normalize_public_url <raw>: prints the panel's public origin, or returns 1
# with a message on stderr. Same rules as parsePublicURL in
# internal/config/drive.go: trim, drop one trailing /, then https://<host>[:port]
# or http://localhost|127.0.0.1[:port], with no path, query or fragment. The
# scheme and host are lower-cased. An empty value is the caller's business.
normalize_public_url() {
  local u scheme rest host port
  u=$(trim_space "${1-}")
  u=${u%/}
  if [[ "$u" =~ ^([A-Za-z]+)://([^/?#@]+)$ ]]; then
    scheme=${BASH_REMATCH[1],,}
    rest=${BASH_REMATCH[2],,}
    host=${rest%%:*}
    port=""
    [[ "$rest" == *:* ]] && port=${rest#*:}
    if [[ "$host" =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ && ("$rest" != *:* || "$port" =~ ^[0-9]{1,5}$) ]]; then
      if [[ "$scheme" == https ]] ||
        [[ "$scheme" == http && ("$host" == localhost || "$host" == 127.0.0.1) ]]; then
        printf '%s://%s\n' "$scheme" "$rest"
        return 0
      fi
    fi
  fi
  echo "public URL '${1-}' must be https://<host>[:port] (or http://localhost[:port]) with no path" >&2
  return 1
}

# normalize_google_value <raw>: prints a Google OAuth client ID or secret
# trimmed, or returns 1. Only the characters Google uses are accepted, so the
# value is safe in an env file.
normalize_google_value() {
  local v
  v=$(trim_space "${1-}")
  if [[ ! "$v" =~ ^[A-Za-z0-9._~-]+$ ]]; then
    echo "a Google client ID or secret may only contain letters, digits and . _ ~ -" >&2
    return 1
  fi
  printf '%s\n' "$v"
}

# normalize_timezone <raw>: prints an IANA time zone name (Asia/Ho_Chi_Minh,
# UTC, Etc/UTC), or returns 1 with a message on stderr. One leading ":" (the
# POSIX form) is dropped. Only the characters of zone names are accepted, so
# the value is safe in an env file; the panel itself refuses a name it does not
# know at startup (internal/config).
normalize_timezone() {
  local z
  z=$(trim_space "${1-}")
  z=${z#:}
  if [[ ! "$z" =~ ^[A-Za-z0-9][A-Za-z0-9_+./-]*$ || "$z" == *..* ]]; then
    echo "time zone '${1-}' must be an IANA name such as Asia/Ho_Chi_Minh or UTC" >&2
    return 1
  fi
  printf '%s\n' "$z"
}

# detect_timezone: prints the host's time zone, trying `timedatectl show -p
# Timezone --value`, then $SCP_ETC_DIR/timezone, then the $SCP_ETC_DIR/localtime
# link (its target without the .../zoneinfo/ prefix), and falling back to UTC.
# An answer that is not a usable name is skipped. SCP_ETC_DIR (default /etc) is
# a test-only override.
detect_timezone() {
  local etc=${SCP_ETC_DIR:-/etc} z="" link=""
  if command -v timedatectl >/dev/null 2>&1; then
    z=$(timedatectl show -p Timezone --value 2>/dev/null) || z=""
    if z=$(normalize_timezone "$z" 2>/dev/null); then
      printf '%s\n' "$z"
      return 0
    fi
  fi
  if [[ -r "$etc/timezone" ]]; then
    IFS= read -r z <"$etc/timezone" || true
    if z=$(normalize_timezone "$z" 2>/dev/null); then
      printf '%s\n' "$z"
      return 0
    fi
  fi
  if [[ -L "$etc/localtime" ]]; then
    link=$(readlink "$etc/localtime") || link=""
    if [[ "$link" == */zoneinfo/* ]] && z=$(normalize_timezone "${link#*/zoneinfo/}" 2>/dev/null); then
      printf '%s\n' "$z"
      return 0
    fi
  fi
  printf '%s\n' UTC
}

# gen_secret_key: prints 32 random bytes as 64 hex characters.
gen_secret_key() {
  local k
  k=$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')
  [[ "$k" =~ ^[0-9a-f]{64}$ ]] || die "could not generate the panel secret key from /dev/urandom"
  printf '%s\n' "$k"
}

# root_refused <absolute normalised path>: succeeds when mounting the path
# would mask part of the panel container's own filesystem. /var is refused on
# its own (it would cover /var/run and /var/lib) while /var/www stays allowed.
root_refused() {
  local p=$1
  [[ "$p" == / || "$p" == /var ]] && return 0
  [[ "$p" =~ ^/(bin|sbin|lib|lib32|lib64|libx32|usr|etc|proc|sys|dev|run|tmp|root|data|host)(/|$) ]] && return 0
  [[ "$p" =~ ^/var/(run|lib)(/|$) ]] && return 0
  return 1
}

# validate_roots <comma list>: prints the cleaned, de-duplicated roots that
# exist, comma-separated. Spaces around items, empty items and repeated or
# trailing slashes are tolerated. A root that does not exist is skipped with a
# warning on stderr (it may have been removed since install); any other
# problem returns 1 with a message on stderr. Each root is checked in order:
# absolute, allowed characters, not refused, exists, is a directory.
validate_roots() {
  local item p seen="" items=()
  IFS=',' read -ra items <<<"${1-}"
  for item in ${items[@]+"${items[@]}"}; do
    item=$(trim_space "$item")
    [[ -n "$item" ]] || continue
    if [[ "$item" != /* ]]; then
      echo "workspace root '$item' must be an absolute path" >&2
      return 1
    fi
    if [[ ! "$item" =~ ^[A-Za-z0-9._/-]+$ ]]; then
      echo "workspace root '$item' may only contain letters, digits and . _ / -" >&2
      return 1
    fi
    # Collapse repeated slashes and drop trailing ones; "/" stays "/".
    p=$item
    while [[ "$p" == *//* ]]; do
      p=${p//\/\//\/}
    done
    if [[ "$p" != / ]]; then
      p=${p%/}
    fi
    # A . or .. component could be used to walk out of an allowed prefix.
    if [[ "$p" =~ (^|/)\.\.?(/|$) ]]; then
      echo "workspace root '$item' must not contain . or .. components" >&2
      return 1
    fi
    if root_refused "$p"; then
      echo "workspace root '$item' is not allowed: it would hide the container's own files" >&2
      return 1
    fi
    if [[ ! -e "$p" ]]; then
      echo "warning: workspace root '$p' does not exist, skipping it" >&2
    elif [[ ! -d "$p" ]]; then
      echo "workspace root '$p' is not a directory" >&2
      return 1
    elif [[ ",$seen," != *",$p,"* ]]; then
      seen="${seen:+$seen,}$p"
    fi
  done
  printf '%s\n' "$seen"
}

# default_roots: prints those of /home,/srv,/opt,/var/www that exist,
# comma-separated, in that order.
default_roots() {
  local d out=""
  for d in /home /srv /opt /var/www; do
    if [[ -d "$d" ]]; then
      out="${out:+$out,}$d"
    fi
  done
  printf '%s\n' "$out"
}

# --- install flow ---------------------------------------------------------
# The functions below use these globals, set by main: SCP_DIR, RUN_UID,
# RUN_GID, SCP_TMP (the temp file being written, removed on exit), TTY_OPEN.

die() {
  echo "install.sh: $*" >&2
  exit 1
}

# check_compose_version <version>: accepts docker compose 2.20 or newer (as
# `docker compose version --short` prints it, with or without a leading v).
check_compose_version() {
  local v major minor rest
  v=$(trim_space "$1")
  v=${v#v}
  major=${v%%.*}
  rest=${v#*.}
  minor=${rest%%.*}
  minor=${minor%%[!0-9]*}
  if [[ "$v" != *.* || ! "$major" =~ ^[0-9]+$ || ! "$minor" =~ ^[0-9]+$ ]]; then
    die "cannot read the docker compose version '$1'; docker compose 2.20 or newer is needed"
  fi
  if ((10#$major < 2 || (10#$major == 2 && 10#$minor < 20))); then
    die "docker compose $1 is too old; docker compose 2.20 or newer is needed"
  fi
}

# preflight <no_start>: Linux, docker, a recent compose plugin, curl when
# downloading, and (unless the panel is not started) a reachable daemon.
preflight() {
  local v
  [[ "$(uname -s)" == Linux ]] || die "the server control panel installs on Linux only"
  command -v docker >/dev/null 2>&1 || die "docker is not installed: https://docs.docker.com/engine/install/"
  if [[ -z "${SCP_SOURCE:-}" ]]; then
    command -v curl >/dev/null 2>&1 || die "curl is needed to download the compose files"
  fi
  if ! v=$(docker compose version --short 2>/dev/null); then
    die "the docker compose plugin is missing; install docker compose 2.20 or newer"
  fi
  check_compose_version "$v"
  if [[ "$1" == 0 ]] && ! docker info >/dev/null 2>&1; then
    die "cannot reach the docker daemon as $(id -un): add the user to the docker group (sudo usermod -aG docker $(id -un), then log in again) or run install.sh with sudo"
  fi
}

# resolve_user: sets RUN_UID and RUN_GID, the user the panel runs as.
resolve_user() {
  local normal="the panel must run as a normal user, not root: run install.sh as that user, or with sudo from that user's account"
  if [[ "$(id -u)" != 0 ]]; then
    RUN_UID=$(id -u)
    RUN_GID=$(id -g)
  elif [[ -n "${SUDO_UID:-}" ]]; then
    RUN_UID=$SUDO_UID
    RUN_GID=${SUDO_GID:-}
  else
    die "$normal"
  fi
  if [[ ! "$RUN_UID" =~ ^[0-9]+$ || ! "$RUN_GID" =~ ^[0-9]+$ ]]; then
    die "cannot tell which user to run the panel as (SUDO_UID='${SUDO_UID:-}', SUDO_GID='${SUDO_GID:-}')"
  fi
  if ((10#$RUN_UID == 0 || 10#$RUN_GID == 0)); then
    die "$normal"
  fi
}

# docker_gid <no_start>: prints the group that owns the docker socket.
docker_gid() {
  local sock=/var/run/docker.sock
  if [[ -S "$sock" ]]; then
    stat -c %g "$sock"
  elif [[ "${DOCKER_GID:-}" =~ ^[0-9]+$ ]]; then
    echo "note: $sock not found, using DOCKER_GID=$DOCKER_GID" >&2
    printf '%s\n' "$DOCKER_GID"
  elif [[ "$1" == 1 ]]; then
    echo "note: $sock not found, writing a placeholder docker group; re-run install.sh on the server to fix it" >&2
    printf '%s\n' docker-gid-unknown
  else
    die "cannot find the docker socket $sock"
  fi
}

# prepare_dir: creates SCP_DIR and data/ owned by RUN_UID:RUN_GID, mode 0750,
# or refuses an existing SCP_DIR owned by someone else.
prepare_dir() {
  local owner
  if [[ -e "$SCP_DIR" || -L "$SCP_DIR" ]]; then
    [[ -d "$SCP_DIR" && ! -L "$SCP_DIR" ]] || die "$SCP_DIR exists and is not a directory"
    owner=$(stat -c %u "$SCP_DIR")
    if [[ "$owner" != "$RUN_UID" ]]; then
      die "$SCP_DIR is owned by UID $owner, not UID $RUN_UID: re-run install.sh as the user with UID $owner, or uninstall first"
    fi
  elif [[ "$(id -u)" == 0 || -w "$(dirname "$SCP_DIR")" ]]; then
    install -d -o "$RUN_UID" -g "$RUN_GID" -m 0750 "$SCP_DIR"
  else
    echo "Creating $SCP_DIR with sudo"
    sudo install -d -o "$RUN_UID" -g "$RUN_GID" -m 0750 "$SCP_DIR"
  fi
  install -d -o "$RUN_UID" -g "$RUN_GID" -m 0750 "$SCP_DIR/data"
}

# new_tmp: creates the temp file SCP_TMP next to the files it will replace.
new_tmp() {
  SCP_TMP=$(mktemp "$SCP_DIR/.install.XXXXXX")
}

# commit_tmp <dest> <mode>: gives SCP_TMP its mode and owner and moves it into
# place, so a reader never sees a half-written file.
commit_tmp() {
  chmod "$2" "$SCP_TMP"
  if [[ "$(id -u)" == 0 ]]; then
    chown "$RUN_UID:$RUN_GID" "$SCP_TMP"
  fi
  mv -f "$SCP_TMP" "$1"
  SCP_TMP=""
}

# write_file <dest> <mode> <content>
write_file() {
  new_tmp
  printf '%s' "$3" >"$SCP_TMP"
  commit_tmp "$1" "$2"
}

# fetch_file <name> <mode>: copies deploy/<name> from SCP_SOURCE, or downloads
# it from the same ref as this script, into SCP_DIR.
fetch_file() {
  local url="https://raw.githubusercontent.com/daniel-vuky/server-control-panel/${SCP_REF:-main}/deploy/$1"
  new_tmp
  if [[ -n "${SCP_SOURCE:-}" ]]; then
    cp "$SCP_SOURCE/$1" "$SCP_TMP"
  elif ! curl -fsSL "$url" -o "$SCP_TMP"; then
    die "could not download $url"
  fi
  commit_tmp "$SCP_DIR/$1" "$2"
}

# open_tty <var>: opens the prompt input on fd 3 once, or fails naming the
# variable that would have answered the question.
open_tty() {
  [[ "${TTY_OPEN:-0}" == 1 ]] && return 0
  if ! { exec 3<"${SCP_TTY:-/dev/tty}"; } 2>/dev/null; then
    die "no terminal to ask for $1: set $1 in the environment and run install.sh again"
  fi
  TTY_OPEN=1
}

# answer <var> <prompt> <normaliser>: sets ANSWER to the normalised value of
# the environment variable <var> or, when it is empty, of a line read from the
# terminal, asking again until the normaliser accepts it. The normaliser
# explains a rejection on stderr; values are never echoed here.
answer() {
  local var=$1 prompt=$2 norm=$3 raw value
  if [[ -n "${!var:-}" ]]; then
    value=$("$norm" "${!var}") || die "$var is not valid"
    ANSWER=$value
    return 0
  fi
  open_tty "$var"
  while :; do
    printf '%s' "$prompt" >&2
    raw=""
    if ! IFS= read -r -u 3 raw && [[ -z "$raw" ]]; then
      echo >&2
      die "no answer for $var (end of input)"
    fi
    if value=$("$norm" "$raw"); then
      ANSWER=$value
      return 0
    fi
  done
}

# answer_optional <var> <prompt> <normaliser> [secret]: like answer, but the
# value is optional. ANSWER is the normalised value of the environment variable
# or of a line read from the terminal; it is empty when neither exists. With no
# terminal, an empty line or the end of the input is an empty answer; a bad
# answer is asked again. With "secret" the typed line is not shown.
answer_optional() {
  local var=$1 prompt=$2 norm=$3 mode=${4:-} raw value
  ANSWER=""
  if [[ -n "${!var:-}" ]]; then
    value=$("$norm" "${!var}") || die "$var is not valid"
    ANSWER=$value
    return 0
  fi
  if [[ "${TTY_OPEN:-0}" != 1 ]]; then
    if ! { exec 3<"${SCP_TTY:-/dev/tty}"; } 2>/dev/null; then
      return 0
    fi
    TTY_OPEN=1
  fi
  while :; do
    printf '%s' "$prompt" >&2
    raw=""
    if [[ "$mode" == secret ]]; then
      IFS= read -r -s -u 3 raw || true
      echo >&2
    else
      IFS= read -r -u 3 raw || true
    fi
    raw=$(trim_space "$raw")
    [[ -n "$raw" ]] || return 0
    if value=$("$norm" "$raw"); then
      ANSWER=$value
      return 0
    fi
  done
}

# normalize_roots_answer <raw>: an empty answer means the default roots; the
# result must keep at least one existing root.
normalize_roots_answer() {
  local raw out
  raw=$(trim_space "${1-}")
  [[ -n "$raw" ]] || raw=$(default_roots)
  out=$(validate_roots "$raw") || return 1
  if [[ -z "$out" ]]; then
    echo "no workspace root left: name at least one existing directory in WORKSPACE_ROOTS" >&2
    return 1
  fi
  printf '%s\n' "$out"
}

# choose_timezone: sets TZ_ANSWER to the server time zone: TZ from the
# environment, else a line typed at the prompt that offers the detected zone as
# the default (an empty line, or no terminal, takes it).
choose_timezone() {
  local def
  def=$(detect_timezone)
  answer_optional TZ "Server time zone for backup schedules [$def]: " normalize_timezone
  TZ_ANSWER=${ANSWER:-$def}
}

# write_panel_env: asks for (or reads from the environment) the Cloudflare
# values and workspace roots, then the optional public URL and Google values,
# generates the secret key and writes panel.env, mode 0600.
write_panel_env() {
  local team aud roots key url gid gsecret tz
  answer CF_ACCESS_TEAM_DOMAIN "Cloudflare Access team domain (myteam.cloudflareaccess.com): " normalize_team_domain
  team=$ANSWER
  answer CF_ACCESS_AUD "Cloudflare Access application AUD tag: " normalize_aud
  aud=$ANSWER
  answer WORKSPACE_ROOTS "Workspace roots, comma-separated [$(default_roots)]: " normalize_roots_answer
  roots=$ANSWER
  echo "Optional: Google Drive for backups (press Enter to skip; set them later in panel.env, see docs/google-drive.md)." >&2
  answer_optional PANEL_PUBLIC_URL "Panel public URL (https://panel.example.com), optional: " normalize_public_url
  url=$ANSWER
  answer_optional GOOGLE_CLIENT_ID "Google OAuth client ID, optional: " normalize_google_value
  gid=$ANSWER
  answer_optional GOOGLE_CLIENT_SECRET "Google OAuth client secret, optional: " normalize_google_value secret
  gsecret=$ANSWER
  if [[ -n "$gid" && -z "$gsecret" ]] || [[ -z "$gid" && -n "$gsecret" ]]; then
    die "GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET go together: give both or neither"
  fi
  choose_timezone
  tz=$TZ_ANSWER
  key=$(gen_secret_key)
  write_file "$SCP_DIR/panel.env" 0600 "# Settings for the server control panel, read by the panel container.
# install.sh writes this file once and never overwrites it: edit it here,
# then re-run install.sh (new workspace roots are mounted on the next run).
CF_ACCESS_TEAM_DOMAIN=$team
CF_ACCESS_AUD=$aud
WORKSPACE_ROOTS=$roots
# The address the panel listens on. The Cloudflare Tunnel route must point
# at the same address.
# PANEL_ADDR=127.0.0.1:8471
# Encrypts the Google Drive token. Keep it with the server's secrets: if it is
# lost, the only cost is connecting Google Drive again.
PANEL_SECRET_KEY=$key
# Google Drive backups (optional, see docs/google-drive.md). Leave empty to set
# them up later: PANEL_PUBLIC_URL is the panel's address, https://<your-domain>.
PANEL_PUBLIC_URL=$url
GOOGLE_CLIENT_ID=$gid
GOOGLE_CLIENT_SECRET=$gsecret
# The server time zone (an IANA name such as Asia/Ho_Chi_Minh). Backup schedules
# run and are shown in it. Change it here, then run docker compose up -d.
TZ=$tz
"
}

# ensure_secret_key: appends PANEL_SECRET_KEY to an existing panel.env that has
# none (or an empty one). Nothing else in the file changes.
ensure_secret_key() {
  local f="$SCP_DIR/panel.env" key
  [[ -n "$(env_file_value "$f" PANEL_SECRET_KEY)" ]] && return 0
  key=$(gen_secret_key)
  # A last line without a newline needs one first, or the key would join it.
  if [[ -s "$f" && -n "$(tail -c 1 "$f")" ]]; then
    printf '\n' >>"$f"
  fi
  printf 'PANEL_SECRET_KEY=%s\n' "$key" >>"$f"
  echo "Added PANEL_SECRET_KEY to $f"
}

# ensure_timezone: appends TZ to an existing panel.env that has no TZ= line.
# An owner's own TZ= line, even an empty one, stays. Nothing else in the file
# changes.
ensure_timezone() {
  local f="$SCP_DIR/panel.env"
  if grep -Eq '^[[:space:]]*TZ[[:space:]]*=' "$f"; then
    return 0
  fi
  choose_timezone
  # A last line without a newline needs one first, or TZ would join it.
  if [[ -s "$f" && -n "$(tail -c 1 "$f")" ]]; then
    printf '\n' >>"$f"
  fi
  printf 'TZ=%s\n' "$TZ_ANSWER" >>"$f"
  echo "Added TZ to $f"
}

# env_file_value <file> <key>: prints the last value of KEY=value in an env
# file, without surrounding spaces or one pair of matching quotes.
env_file_value() {
  local line value=""
  while IFS= read -r line || [[ -n "$line" ]]; do
    line=$(trim_space "$line")
    if [[ "$line" == "$2="* ]]; then
      value=${line#"$2="}
    fi
  done <"$1"
  value=$(trim_space "$value")
  if [[ ${#value} -ge 2 && ("$value" == \"*\" || "$value" == \'*\') ]]; then
    value=${value:1:${#value}-2}
  fi
  printf '%s\n' "$value"
}

# write_override <roots> <docker gid>: regenerates compose.override.yaml.
write_override() {
  local r items content
  IFS=',' read -ra items <<<"$1"
  content="# Generated by install.sh on every run: edits here are lost.
# To change the workspace mounts, edit WORKSPACE_ROOTS in panel.env and
# re-run install.sh.
services:
  panel:
    user: \"$RUN_UID:$RUN_GID\"
    group_add: [\"$2\"]
    volumes:
"
  for r in "${items[@]}"; do
    content+="      - $r:$r:ro
"
  done
  write_file "$SCP_DIR/compose.override.yaml" 0644 "$content"
}

show_logs() {
  echo "Last lines of the panel log (docker compose logs --tail 30 panel):" >&2
  docker compose logs --tail 30 panel >&2 || true
}

# start_updater: starts the updater again (nothing to start on a fresh
# install). Called from the EXIT trap while UPDATER_STOPPED=1.
start_updater() {
  (cd "$SCP_DIR" && docker compose start updater) >/dev/null 2>&1 || true
}

# on_exit: the EXIT trap. Removes a half-written temp file and, when install.sh
# ends between stopping the updater and the final `up` (a failure, Ctrl-C, a
# dropped SSH session), starts the updater again: with restart: unless-stopped
# a stopped updater would stay stopped, even across reboots.
on_exit() {
  local rc=$?
  if [[ -n "${SCP_TMP:-}" ]]; then
    rm -f -- "$SCP_TMP"
  fi
  if [[ "${UPDATER_STOPPED:-0}" == 1 ]]; then
    start_updater
  fi
  exit "$rc"
}

# start_panel: pulls the images and (re)creates the containers. Runs in SCP_DIR.
# The updater is stopped first so its rounds cannot pull or recreate the panel
# at the same time; `up` starts it again, and until `up` has run the EXIT trap
# does.
start_panel() {
  local out re='denied|unauthorized|manifest unknown'
  UPDATER_STOPPED=1
  docker compose stop updater >/dev/null 2>&1 || true
  echo "Pulling images..."
  if ! out=$(docker compose pull 2>&1); then
    printf '%s\n' "$out" >&2
    if [[ "$out" =~ $re ]]; then
      echo "hint: the panel image could not be pulled anonymously. If the GHCR package ghcr.io/daniel-vuky/server-control-panel is still private, make it public (see docs/deploy.md)." >&2
    fi
    die "docker compose pull failed; nothing was started"
  fi
  if [[ -n "$out" ]]; then
    printf '%s\n' "$out"
  fi
  echo "Starting the panel..."
  if ! docker compose up -d --remove-orphans; then
    show_logs
    die "docker compose up failed"
  fi
  UPDATER_STOPPED=0
}

# wait_healthy: waits for the panel container's health check to pass, or
# prints its logs and fails. Runs in SCP_DIR.
wait_healthy() {
  local cid status="" timeout=${SCP_HEALTH_TIMEOUT:-60} deadline
  # -a: a panel that exited between restarts is "not healthy", not missing.
  cid=$(docker compose ps -aq panel)
  cid=${cid%%$'\n'*}
  if [[ -z "$cid" ]]; then
    echo "install.sh: the panel container does not exist after docker compose up" >&2
    show_logs
    exit 1
  fi
  echo "Waiting up to ${timeout}s for the panel to become healthy..."
  deadline=$((SECONDS + timeout))
  while :; do
    status=$(docker inspect -f '{{.State.Health.Status}}' "$cid" 2>/dev/null) || status=""
    [[ "$status" == healthy ]] && return 0
    if [[ "$status" == unhealthy ]] || ((SECONDS >= deadline)); then
      break
    fi
    sleep 2
  done
  echo "install.sh: the panel is not healthy (status: ${status:-unknown})" >&2
  show_logs
  exit 1
}

main() {
  set -euo pipefail
  local no_start=0 arg gid roots
  for arg in "$@"; do
    case $arg in
      --no-start) no_start=1 ;;
      *) die "unknown option '$arg' (the only option is --no-start)" ;;
    esac
  done

  SCP_DIR=${SCP_DIR:-/opt/server-control-panel}
  [[ "$SCP_DIR" == /* ]] || die "SCP_DIR must be an absolute path"
  if [[ "$SCP_DIR" != /opt/server-control-panel && "$no_start" == 0 ]]; then
    echo "warning: the updater only works from /opt/server-control-panel, not $SCP_DIR" >&2
  fi
  if [[ -n "${SCP_SOURCE:-}" ]]; then
    SCP_SOURCE=$(cd "$SCP_SOURCE" && pwd) || die "SCP_SOURCE '$SCP_SOURCE' is not a directory"
  fi
  SCP_TMP=""
  TTY_OPEN=0
  UPDATER_STOPPED=0
  trap on_exit EXIT
  trap 'exit 130' INT TERM HUP

  # 1-3: preflight, who the panel runs as, the docker group.
  preflight "$no_start"
  resolve_user
  gid=$(docker_gid "$no_start")

  # 4: the install directory.
  prepare_dir

  # 5: panel.env, written once from the answers, never overwritten.
  if [[ ! -e "$SCP_DIR/panel.env" ]]; then
    write_panel_env
  else
    echo "Keeping $SCP_DIR/panel.env"
    ensure_secret_key
    ensure_timezone
  fi

  # 6: the workspace roots, as panel.env names them now.
  roots=$(validate_roots "$(env_file_value "$SCP_DIR/panel.env" WORKSPACE_ROOTS)") ||
    die "fix WORKSPACE_ROOTS in $SCP_DIR/panel.env and run install.sh again"
  [[ -n "$roots" ]] || die "no workspace root left; set WORKSPACE_ROOTS in $SCP_DIR/panel.env"

  # 7: the files that follow the repository, refreshed on every run.
  fetch_file compose.yaml 0644
  fetch_file updater.sh 0755
  fetch_file uninstall.sh 0755

  # 8: the generated override.
  write_override "$roots" "$gid"

  # 9: .env, created once; it holds the rollback pin.
  if [[ ! -e "$SCP_DIR/.env" ]]; then
    write_file "$SCP_DIR/.env" 0644 "# Compose settings for the server control panel, kept across re-runs of install.sh.
# PANEL_TAG: the panel image tag: latest follows main, sha-<7 chars> pins a build (rollback).
PANEL_TAG=latest
# UPDATE_INTERVAL: seconds between the updater's checks for a new panel image.
UPDATE_INTERVAL=300
"
  fi

  if [[ "$no_start" == 1 ]]; then
    echo "Wrote the server control panel to $SCP_DIR (not started: --no-start)."
    return 0
  fi

  # 10-11: start and wait for health.
  cd "$SCP_DIR"
  start_panel
  wait_healthy
  cat <<EOF

The server control panel is running.
  Install directory: $SCP_DIR (settings in panel.env and .env)
  Address:           https://panel.<your-domain>, once the Cloudflare Tunnel route exists
  Next steps:        https://github.com/daniel-vuky/server-control-panel/blob/main/docs/deploy.md
EOF
}

[[ "${SCP_LIB:-}" == 1 ]] || main "$@"
