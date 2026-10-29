#!/usr/bin/env bash
# Tests for the value validation functions in deploy/install.sh.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=deploy/test/lib.sh
source "$here/lib.sh"
export SCP_LIB=1
# shellcheck source=deploy/install.sh
source "$here/../install.sh"

# Roots under /tmp are refused, so the fixtures live under /srv (run.sh creates it).
T=$(mktemp -d -p /srv) || exit 1
# shellcheck disable=SC2317  # called by lib.sh at exit
test_cleanup() { rm -rf "$T"; }
mkdir -p "$T/a" "$T/b" "$T/with space" "$T/a:b"
: >"$T/file"

# Cloudflare team domain
assert_eq myteam.cloudflareaccess.com "$(normalize_team_domain ' https://MyTeam.cloudflareaccess.com/ ')" "team: trims, lowercases, strips scheme and slash"
assert_eq my-team2.cloudflareaccess.com "$(normalize_team_domain my-team2.cloudflareaccess.com)" "team: bare host with digits and hyphen"
assert_fails "team: other host"     normalize_team_domain evil.com
assert_fails "team: path"           normalize_team_domain myteam.cloudflareaccess.com/x
assert_fails "team: empty"          normalize_team_domain ''
assert_fails "team: http scheme"    normalize_team_domain http://myteam.cloudflareaccess.com
assert_fails "team: suffix lookalike" normalize_team_domain myteam.cloudflareaccess.com.evil.com
assert_fails "team: subdomain"      normalize_team_domain a.b.cloudflareaccess.com
assert_fails "team: two slashes"    normalize_team_domain myteam.cloudflareaccess.com//
assert_fails "team: trailing newline" normalize_team_domain $'myteam.cloudflareaccess.com\nx'
err=$(normalize_team_domain evil.com 2>&1 >/dev/null)
assert_contains "$err" "myteam.cloudflareaccess.com" "team: error names the expected shape"

# Cloudflare AUD
assert_eq abcdef01 "$(normalize_aud ' ABCdef01 ')" "aud: trims and lowercases"
assert_fails "aud: not hex"         normalize_aud xyz
assert_fails "aud: empty"           normalize_aud ''
assert_fails "aud: blank"           normalize_aud '   '
assert_fails "aud: inner space"     normalize_aud 'abc def'

# Workspace roots
assert_eq "$T/a,$T/b" "$(validate_roots "$T/a,$T/b,$T/a")" "roots: dedup"
assert_eq "$T/a,$T/b" "$(validate_roots " $T/a/ , $T/b ,")" "roots: trims spaces, trailing slash and empty items"
assert_eq "$T/a" "$(validate_roots "$T/a,$T/missing" 2>/dev/null)" "roots: missing skipped"
warn=$(validate_roots "$T/a,$T/missing" 2>&1 >/dev/null)
assert_contains "$warn" "$T/missing" "roots: missing root is warned about"
assert_eq "$T/a" "$(validate_roots "$T/a//" 2>/dev/null)" "roots: repeated trailing slashes"
assert_eq "$T/a" "$(validate_roots "$T//a" 2>/dev/null)" "roots: repeated inner slashes"
# shellcheck disable=SC2016  # '$x' is meant literally: a root with a $ in it
for bad in / // /usr /usr/bin /etc /etc/ssh /var /var/ /var/lib/docker /var/run /var/run/x /proc /sys /dev /run /bin /sbin /lib /lib64 /lib32/x /tmp /tmp/x /root /root/x /data /data/x /host /host/x relative "$T/with space" "$T/a:b" "$T/file" "$T/a/../../etc" /srv/../etc "$T/./a" '/srv/$x' '/srv/a"b'; do
  assert_fails "roots: refuses [$bad]" validate_roots "$bad"
done
assert_eq "" "$(validate_roots "")" "roots: an empty list yields an empty result"
# an invalid root fails even when valid ones are listed alongside
assert_fails "roots: one bad root among good ones" validate_roots "$T/a,/usr,$T/b"
# lookalikes of refused names are fine
mkdir -p /srv/library-scp-test /srv/dataset-scp-test
assert_eq /srv/library-scp-test "$(validate_roots /srv/library-scp-test)" "roots: /library... is not /lib*"
assert_eq /srv/dataset-scp-test "$(validate_roots /srv/dataset-scp-test)" "roots: /srv/dataset... is not /data"
rmdir /srv/library-scp-test /srv/dataset-scp-test
mkdir -p /var/www
assert_eq /var/www "$(validate_roots /var/www)" "roots: /var/www allowed"
assert_eq /var/www "$(validate_roots /var/www/)" "roots: /var/www/ allowed"

# default_roots: the existing ones of /home,/srv,/opt,/var/www, in that order
want=""
for d in /home /srv /opt /var/www; do
  [[ -d "$d" ]] && want="${want:+$want,}$d"
done
got=$(default_roots)
assert_eq "$want" "$got" "default_roots: the existing standard roots"
assert_contains ",$got," ",/srv," "default_roots: includes /srv"
assert_contains ",$got," ",/var/www," "default_roots: includes /var/www"
assert_eq "$got" "$(validate_roots "$got")" "default_roots: output passes validate_roots"

summary
