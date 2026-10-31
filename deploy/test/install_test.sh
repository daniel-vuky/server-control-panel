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
install -m 0755 "$here/fake-timedatectl" "$T/bin/timedatectl"
export PATH="$T/bin:$PATH"

# fresh_case: forget every fake-docker setting, pick a new install directory
# that does not exist yet, and set the answers in the environment.
fresh_case() {
  unset "${!FAKE_DOCKER_@}" "${!FAKE_TIMEDATECTL_@}" SCP_HEALTH_TIMEOUT PANEL_PUBLIC_URL GOOGLE_CLIENT_ID GOOGLE_CLIENT_SECRET TZ
  # The zone sources the install reads: timedatectl (faked, prints nothing
  # unless a case sets FAKE_TIMEDATECTL_OUT) and a private /etc.
  rm -rf "${T:?}/etc"
  mkdir -p "$T/etc"
  export SCP_ETC_DIR="$T/etc"
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

# --- secret key and optional Drive settings
# key_of <panel.env>: the PANEL_SECRET_KEY value(s), one per line.
key_of() { sed -n 's/^PANEL_SECRET_KEY=//p' "$1"; }
# count_key <panel.env>: how many PANEL_SECRET_KEY= lines the file has.
count_key() { grep -c '^PANEL_SECRET_KEY=' "$1" || true; }
hex64='^[0-9a-f]{64}$'

fresh_case
run_install --no-start
assert_eq 0 "$RC" "key: fresh install without a tty exits 0"
key=$(key_of "$SCP_DIR/panel.env")
assert_eq 1 "$(count_key "$SCP_DIR/panel.env")" "key: one PANEL_SECRET_KEY line"
assert_eq yes "$([[ "$key" =~ $hex64 ]] && echo yes || echo no)" "key: 64 lower-case hex characters"
assert_line "$SCP_DIR/panel.env" "PANEL_PUBLIC_URL=" "key: public URL empty without an answer"
assert_line "$SCP_DIR/panel.env" "GOOGLE_CLIENT_ID=" "key: client ID empty without an answer"
assert_line "$SCP_DIR/panel.env" "GOOGLE_CLIENT_SECRET=" "key: client secret empty without an answer"
assert_file_mode 600 "$SCP_DIR/panel.env"
assert_lacks "$OUT$ERR" "$key" "key: never printed on a fresh install"
fresh_case
run_install --no-start
assert_eq no "$([[ "$(key_of "$SCP_DIR/panel.env")" == "$key" ]] && echo yes || echo no)" "key: a second install gets a different key"

fresh_case
export PANEL_PUBLIC_URL=' HTTPS://Panel.Example.com/ ' GOOGLE_CLIENT_ID=' id-1.apps.googleusercontent.com ' GOOGLE_CLIENT_SECRET=GOCSPX-sec_ret
run_install --no-start
assert_eq 0 "$RC" "drive env: exit 0"
assert_line "$SCP_DIR/panel.env" "PANEL_PUBLIC_URL=https://panel.example.com" "drive env: URL normalised"
assert_line "$SCP_DIR/panel.env" "GOOGLE_CLIENT_ID=id-1.apps.googleusercontent.com" "drive env: client ID trimmed"
assert_line "$SCP_DIR/panel.env" "GOOGLE_CLIENT_SECRET=GOCSPX-sec_ret" "drive env: client secret written"
assert_lacks "$OUT$ERR" "GOCSPX-sec_ret" "drive env: the secret is never echoed"
for u in http://panel.example.com https://panel.example.com/app 'https://panel.example.com?x=1' \
  'https://panel.example.com#f' https://user@panel.example.com ftp://panel.example.com panel.example.com \
  https://panel.example.com:port 'https://pa nel.example.com'; do
  fresh_case
  export PANEL_PUBLIC_URL=$u
  run_install --no-start
  assert_nonzero "$RC" "invalid PANEL_PUBLIC_URL '$u': dies"
  assert_contains "$ERR" "PANEL_PUBLIC_URL" "invalid PANEL_PUBLIC_URL '$u': names the variable"
  assert_eq no "$([[ -e "$SCP_DIR/panel.env" ]] && echo yes || echo no)" "invalid PANEL_PUBLIC_URL '$u': no panel.env written"
done
for u in http://localhost:5173 http://127.0.0.1:8471/ https://panel.example.com:8443; do
  fresh_case
  export PANEL_PUBLIC_URL=$u
  run_install --no-start
  assert_eq 0 "$RC" "PANEL_PUBLIC_URL '$u': accepted"
  assert_line "$SCP_DIR/panel.env" "PANEL_PUBLIC_URL=${u%/}" "PANEL_PUBLIC_URL '$u': written without a trailing slash"
done
fresh_case
export GOOGLE_CLIENT_ID=only-an-id
run_install --no-start
assert_nonzero "$RC" "client ID without a secret: dies"
assert_contains "$ERR" "GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET" "client ID without a secret: says both or neither"
assert_eq no "$([[ -e "$SCP_DIR/panel.env" ]] && echo yes || echo no)" "client ID without a secret: no panel.env written"
fresh_case
export GOOGLE_CLIENT_ID='bad id'  GOOGLE_CLIENT_SECRET=x
run_install --no-start
assert_nonzero "$RC" "invalid GOOGLE_CLIENT_ID: dies"
assert_contains "$ERR" "GOOGLE_CLIENT_ID" "invalid GOOGLE_CLIENT_ID: names the variable"

# Interactive: the three optional prompts take values, and an empty line skips them.
fresh_case
unset CF_ACCESS_TEAM_DOMAIN CF_ACCESS_AUD WORKSPACE_ROOTS
printf '%s\n' myteam.cloudflareaccess.com abc123 "$T/ws1" http://nope.example.com panel.example.com/ https://panel.example.com/ cid-9 sec-9 >"$T/tty"
export SCP_TTY="$T/tty"
run_install --no-start
assert_eq 0 "$RC" "optional prompts: exit 0"
assert_line "$SCP_DIR/panel.env" "PANEL_PUBLIC_URL=https://panel.example.com" "optional prompts: URL asked again after bad answers"
assert_line "$SCP_DIR/panel.env" "GOOGLE_CLIENT_ID=cid-9" "optional prompts: client ID"
assert_line "$SCP_DIR/panel.env" "GOOGLE_CLIENT_SECRET=sec-9" "optional prompts: client secret"
assert_contains "$ERR" "panel.example.com/" "optional prompts: a bad URL is reported"
assert_lacks "$OUT$ERR" "sec-9" "optional prompts: the secret is never echoed"
fresh_case
unset CF_ACCESS_TEAM_DOMAIN CF_ACCESS_AUD WORKSPACE_ROOTS
printf '%s\n' myteam.cloudflareaccess.com abc123 "$T/ws1" '' '' '' >"$T/tty"
export SCP_TTY="$T/tty"
run_install --no-start
assert_eq 0 "$RC" "optional prompts: empty lines are accepted"
assert_line "$SCP_DIR/panel.env" "PANEL_PUBLIC_URL=" "optional prompts: empty URL"
assert_line "$SCP_DIR/panel.env" "GOOGLE_CLIENT_ID=" "optional prompts: empty client ID"

# Re-runs: a panel.env without a key gets exactly one line; one with a key is kept.
fresh_case
run_install --no-start
D=$SCP_DIR
grep -v '^PANEL_SECRET_KEY=' "$D/panel.env" >"$T/edit" && cat "$T/edit" >"$D/panel.env"
cp "$D/panel.env" "$T/panel.env.nokey"
unset CF_ACCESS_TEAM_DOMAIN CF_ACCESS_AUD WORKSPACE_ROOTS
run_install --no-start
assert_eq 0 "$RC" "append key: exit 0"
assert_eq 1 "$(count_key "$D/panel.env")" "append key: exactly one key line"
assert_eq yes "$([[ "$(key_of "$D/panel.env")" =~ $hex64 ]] && echo yes || echo no)" "append key: 64 hex"
assert_eq "$(wc -l <"$T/panel.env.nokey" | tr -d ' ')" "$(($(wc -l <"$D/panel.env") - 1))" "append key: one line added"
assert_eq same "$(same_file "$T/panel.env.nokey" <(head -n -1 "$D/panel.env"))" "append key: the rest of the file is untouched"
assert_file_mode 600 "$D/panel.env"
assert_eq 1000:1000 "$(stat -c %u:%g "$D/panel.env")" "append key: owner kept"
assert_contains "$OUT" "Added PANEL_SECRET_KEY" "append key: says so"
assert_lacks "$OUT$ERR" "$(key_of "$D/panel.env")" "append key: the key is never printed"
cp "$D/panel.env" "$T/panel.env.keyed"
run_install --no-start
assert_eq same "$(same_file "$T/panel.env.keyed" "$D/panel.env")" "keep key: a re-run with a key leaves panel.env byte for byte"
# A file whose last line has no newline: the key goes on its own line.
printf 'CF_ACCESS_TEAM_DOMAIN=myteam.cloudflareaccess.com\nCF_ACCESS_AUD=abc123\nWORKSPACE_ROOTS=%s' "$T/ws1" >"$D/panel.env"
run_install --no-start
assert_eq 0 "$RC" "append key, no final newline: exit 0"
assert_line "$D/panel.env" "WORKSPACE_ROOTS=$T/ws1" "append key, no final newline: the last line is intact"
assert_eq 1 "$(count_key "$D/panel.env")" "append key, no final newline: one key line"
# An empty PANEL_SECRET_KEY= counts as no key.
printf 'CF_ACCESS_TEAM_DOMAIN=myteam.cloudflareaccess.com\nCF_ACCESS_AUD=abc123\nWORKSPACE_ROOTS=%s\nPANEL_SECRET_KEY=\n' "$T/ws1" >"$D/panel.env"
run_install --no-start
assert_eq yes "$([[ "$(key_of "$D/panel.env" | tail -n 1)" =~ $hex64 ]] && echo yes || echo no)" "empty key: a real key is appended after it"

# --- server time zone (TZ)
# tz_of <panel.env>: the TZ value(s), one per line.
tz_of() { sed -n 's/^TZ=//p' "$1"; }
# count_tz <panel.env>: how many TZ= lines the file has.
count_tz() { grep -c '^TZ=' "$1" || true; }

# Detection order: timedatectl, then etc/timezone, then the etc/localtime link, then UTC.
fresh_case
run_install --no-start
assert_eq 0 "$RC" "tz none: exit 0"
assert_eq UTC "$(tz_of "$SCP_DIR/panel.env")" "tz none: nothing detected gives UTC"
assert_eq 1 "$(count_tz "$SCP_DIR/panel.env")" "tz none: one TZ line"
assert_file_mode 600 "$SCP_DIR/panel.env"

fresh_case
export FAKE_TIMEDATECTL_OUT=Asia/Ho_Chi_Minh
printf 'Europe/Paris\n' >"$T/etc/timezone"
ln -s /usr/share/zoneinfo/America/New_York "$T/etc/localtime"
run_install --no-start
assert_eq 0 "$RC" "tz timedatectl: exit 0"
assert_eq Asia/Ho_Chi_Minh "$(tz_of "$SCP_DIR/panel.env")" "tz timedatectl: wins over the files"

fresh_case
printf 'Europe/Paris\n' >"$T/etc/timezone"
ln -s /usr/share/zoneinfo/America/New_York "$T/etc/localtime"
run_install --no-start
assert_eq Europe/Paris "$(tz_of "$SCP_DIR/panel.env")" "tz etc/timezone: wins over the localtime link"

fresh_case
ln -s /usr/share/zoneinfo/America/New_York "$T/etc/localtime"
run_install --no-start
assert_eq America/New_York "$(tz_of "$SCP_DIR/panel.env")" "tz localtime link: the zoneinfo prefix is stripped"

fresh_case
ln -s ../usr/share/zoneinfo/posix/Europe/Berlin "$T/etc/localtime"
run_install --no-start
assert_eq posix/Europe/Berlin "$(tz_of "$SCP_DIR/panel.env")" "tz localtime link: a relative target is stripped too"

fresh_case
ln -s /somewhere/else "$T/etc/localtime"
run_install --no-start
assert_eq UTC "$(tz_of "$SCP_DIR/panel.env")" "tz localtime link: a target outside zoneinfo is UTC"

# A failing timedatectl or an unusable answer falls through to the next source.
fresh_case
export FAKE_TIMEDATECTL_OUT=n/a FAKE_TIMEDATECTL_RC=1
printf 'Europe/Paris\n' >"$T/etc/timezone"
run_install --no-start
assert_eq Europe/Paris "$(tz_of "$SCP_DIR/panel.env")" "tz timedatectl fails: falls back to etc/timezone"
fresh_case
export FAKE_TIMEDATECTL_OUT='bad zone;rm'
run_install --no-start
assert_eq UTC "$(tz_of "$SCP_DIR/panel.env")" "tz timedatectl unusable: UTC"

# TZ in the environment is the answer; a bad one dies and writes nothing.
fresh_case
export FAKE_TIMEDATECTL_OUT=Asia/Ho_Chi_Minh TZ=':Europe/Paris'
run_install --no-start
assert_eq 0 "$RC" "tz env: exit 0"
assert_eq Europe/Paris "$(tz_of "$SCP_DIR/panel.env")" "tz env: used, leading colon dropped"
fresh_case
export TZ='Mars Base'
run_install --no-start
assert_nonzero "$RC" "tz env invalid: dies"
assert_contains "$ERR" "TZ" "tz env invalid: names the variable"
assert_eq no "$([[ -e "$SCP_DIR/panel.env" ]] && echo yes || echo no)" "tz env invalid: no panel.env written"

# Interactive: the detected zone is the default, a typed one replaces it, a bad one is asked again.
fresh_case
unset CF_ACCESS_TEAM_DOMAIN CF_ACCESS_AUD WORKSPACE_ROOTS
export FAKE_TIMEDATECTL_OUT=Asia/Ho_Chi_Minh
printf '%s\n' myteam.cloudflareaccess.com abc123 "$T/ws1" '' '' '' '' >"$T/tty"
export SCP_TTY="$T/tty"
run_install --no-start
assert_eq 0 "$RC" "tz prompt default: exit 0"
assert_eq Asia/Ho_Chi_Minh "$(tz_of "$SCP_DIR/panel.env")" "tz prompt default: Enter takes the detected zone"
assert_contains "$ERR" "Asia/Ho_Chi_Minh" "tz prompt default: the prompt shows the detected zone"
fresh_case
unset CF_ACCESS_TEAM_DOMAIN CF_ACCESS_AUD WORKSPACE_ROOTS
export FAKE_TIMEDATECTL_OUT=Asia/Ho_Chi_Minh
printf '%s\n' myteam.cloudflareaccess.com abc123 "$T/ws1" '' '' '' 'not a zone' Europe/Paris >"$T/tty"
export SCP_TTY="$T/tty"
run_install --no-start
assert_eq 0 "$RC" "tz prompt typed: exit 0"
assert_eq Europe/Paris "$(tz_of "$SCP_DIR/panel.env")" "tz prompt typed: asked again after a bad answer"
assert_contains "$ERR" "not a zone" "tz prompt typed: a bad zone is reported"

# Re-runs: a panel.env without TZ gets exactly one line; one with TZ is kept.
fresh_case
export FAKE_TIMEDATECTL_OUT=Asia/Ho_Chi_Minh
run_install --no-start
D=$SCP_DIR
grep -v '^TZ=' "$D/panel.env" >"$T/edit" && cat "$T/edit" >"$D/panel.env"
cp "$D/panel.env" "$T/panel.env.notz"
unset CF_ACCESS_TEAM_DOMAIN CF_ACCESS_AUD WORKSPACE_ROOTS
export FAKE_TIMEDATECTL_OUT=Europe/Paris
run_install --no-start
assert_eq 0 "$RC" "append tz: exit 0"
assert_eq 1 "$(count_tz "$D/panel.env")" "append tz: exactly one TZ line"
assert_eq Europe/Paris "$(tz_of "$D/panel.env")" "append tz: the detected zone"
assert_eq "$(wc -l <"$T/panel.env.notz" | tr -d ' ')" "$(($(wc -l <"$D/panel.env") - 1))" "append tz: one line added"
assert_eq same "$(same_file "$T/panel.env.notz" <(head -n -1 "$D/panel.env"))" "append tz: the rest of the file is untouched"
assert_file_mode 600 "$D/panel.env"
assert_eq 1000:1000 "$(stat -c %u:%g "$D/panel.env")" "append tz: owner kept"
assert_contains "$OUT" "Added TZ to $D/panel.env" "append tz: says so"
cp "$D/panel.env" "$T/panel.env.tz"
export FAKE_TIMEDATECTL_OUT=Asia/Tokyo
run_install --no-start
assert_eq same "$(same_file "$T/panel.env.tz" "$D/panel.env")" "keep tz: a re-run with TZ leaves panel.env byte for byte"
assert_lacks "$OUT" "Added TZ" "keep tz: says nothing"
# A file whose last line has no newline: TZ goes on its own line.
printf 'CF_ACCESS_TEAM_DOMAIN=myteam.cloudflareaccess.com\nCF_ACCESS_AUD=abc123\nPANEL_SECRET_KEY=%s\nWORKSPACE_ROOTS=%s' \
  "$(printf '0%.0s' {1..64})" "$T/ws1" >"$D/panel.env"
run_install --no-start
assert_eq 0 "$RC" "append tz, no final newline: exit 0"
assert_line "$D/panel.env" "WORKSPACE_ROOTS=$T/ws1" "append tz, no final newline: the last line is intact"
assert_eq Asia/Tokyo "$(tz_of "$D/panel.env")" "append tz, no final newline: TZ on its own line"
# An owner's own TZ=, even an empty one, is theirs and stays.
printf 'CF_ACCESS_TEAM_DOMAIN=myteam.cloudflareaccess.com\nCF_ACCESS_AUD=abc123\nPANEL_SECRET_KEY=%s\nWORKSPACE_ROOTS=%s\nTZ=\n' \
  "$(printf '0%.0s' {1..64})" "$T/ws1" >"$D/panel.env"
cp "$D/panel.env" "$T/panel.env.emptytz"
run_install --no-start
assert_eq same "$(same_file "$T/panel.env.emptytz" "$D/panel.env")" "empty tz: an explicit empty TZ= is left alone"

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
assert_contains "$LOG" "compose ps -aq panel" "healthy: the panel container is looked up stopped or not"
assert_lacks "$LOG" "compose start updater" "healthy: up restarted the updater, the trap does not"
assert_contains "$OUT" "$SCP_DIR" "summary: names the install directory"
assert_contains "$OUT" "docs/deploy.md" "summary: links the deploy docs"

# --- start but unhealthy
fresh_case
export FAKE_DOCKER_COMPOSE_PS_OUT=c0ffee FAKE_DOCKER_INSPECT_OUT=starting SCP_HEALTH_TIMEOUT=2
run_install
assert_nonzero "$RC" "unhealthy: fails"
assert_contains "$LOG" "compose logs --tail 30 panel" "unhealthy: shows the panel logs"
assert_contains "$ERR" "the panel is not healthy" "unhealthy: says the panel is not healthy"

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
compose start updater" "$(grep -E '^compose (stop|start|pull|up)' <<<"$LOG")" "pull $word: the exit after the stop starts the updater again"
done

# --- the updater stop fails (nothing installed or running yet)
fresh_case
export FAKE_DOCKER_COMPOSE_PS_OUT=c0ffee FAKE_DOCKER_INSPECT_OUT=healthy FAKE_DOCKER_COMPOSE_STOP_RC=1
run_install
assert_eq 0 "$RC" "stop updater fails: install goes on"
assert_contains "$LOG" "compose up -d --remove-orphans" "stop updater fails: the panel is started"

# --- unhealthy after up: the updater is already running again
fresh_case
export FAKE_DOCKER_COMPOSE_PS_OUT=c0ffee FAKE_DOCKER_INSPECT_OUT=unhealthy
run_install
assert_nonzero "$RC" "unhealthy after up: fails"
assert_lacks "$LOG" "compose start updater" "unhealthy after up: no extra start"

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
