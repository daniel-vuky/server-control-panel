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

main() {
  set -euo pipefail
  echo "install.sh: not implemented" >&2
  exit 1
}

[[ "${SCP_LIB:-}" == 1 ]] || main "$@"
