package runner

// The database scripts. Each runs as `docker compose exec -T {service} sh -c
// <script> sh [{dbname}]`, so inside the database's own container, as the
// service's user, with the container's environment (ADR 0011). They are fixed
// constants: no value is ever spliced into them, and the database name, when
// there is one, arrives only as "$1", checked by the {dbname} slot.
//
// Rules every script keeps:
//   - credentials are read from the variable or its Docker-secrets twin,
//     ${VAR:-$(cat "${VAR_FILE:-/dev/null}" 2>/dev/null)}, as the official
//     images' entrypoints do;
//   - a password is never on a command line and never exported: it goes into a
//     mktemp file (umask 077) that the EXIT trap removes, and printf, a shell
//     builtin in busybox ash, dash and bash, writes it without a new process;
//   - the shell does not exec the client, so the trap runs; a signal becomes
//     an exit, which runs it too;
//   - unset clears the script's variable names first, so a variable of the same
//     name in the container's environment cannot export them.
//
// Verified on postgres:17-alpine (busybox sh), mysql:9 (bash), mariadb:11 and
// mongo:7 (dash), with a non-root MySQL and MariaDB user, a MongoDB root user,
// *_FILE secrets and the password p:a\ss"w'o#r d;$x.

// scriptStart begins every script: private temp files, and their removal on
// every way out. f is the temp file, once there is one.
const scriptStart = `umask 077
unset f u d pw c
trap '[ -z "${f:-}" ] || rm -f "$f"' EXIT
trap 'exit 1' HUP INT TERM PIPE
`

// pgLogin sets u (POSTGRES_USER, else postgres) and d ("$1", else the image's
// default database, POSTGRES_DB, else the user). The official image trusts
// local socket connections, so it first tries the socket with no password.
// Only when that fails and POSTGRES_PASSWORD is set does it write a
// PGPASSFILE, with : and \ escaped as .pgpass needs, and export its path (not
// the password).
const pgLogin = `u=${POSTGRES_USER:-$(cat "${POSTGRES_USER_FILE:-/dev/null}" 2>/dev/null)}
u=${u:-postgres}
d="${1:-}"
if [ -z "$d" ]; then
	d=${POSTGRES_DB:-$(cat "${POSTGRES_DB_FILE:-/dev/null}" 2>/dev/null)}
	d=${d:-$u}
fi
if ! psql --no-password -X -q -U "$u" -d "$d" -tAc 'select 1' >/dev/null 2>&1; then
	pw=${POSTGRES_PASSWORD:-$(cat "${POSTGRES_PASSWORD_FILE:-/dev/null}" 2>/dev/null)}
	if [ -n "$pw" ]; then
		f=$(mktemp) || exit 1
		printf '*:*:*:*:%s\n' "$(printf '%s' "$pw" | sed 's/[\\:]/\\&/g')" >"$f" || exit 1
		unset pw
		PGPASSFILE=$f
		export PGPASSFILE
	fi
fi
`

// pgDumpScript writes a plain SQL dump of the database to stdout. It serves
// db.pg.dump ("$1" is the name) and db.pg.dump.default (no "$1": the image's
// default database).
const pgDumpScript = scriptStart + pgLogin +
	`pg_dump --no-password --clean --if-exists -U "$u" -d "$d"
`

// pgSizeScript prints the size of the same database in bytes. It serves
// db.pg.size and db.pg.size.default.
const pgSizeScript = scriptStart + pgLogin +
	`psql --no-password -X -q -U "$u" -d "$d" -tAc 'select pg_database_size(current_database())'
`

// mysqlLogin writes the client's defaults file to f: root with
// MYSQL_ROOT_PASSWORD or MARIADB_ROOT_PASSWORD when one is set, else
// MYSQL_USER or MARIADB_USER with its password, else root with no password
// (MariaDB's unix_socket login, or an allowed empty password). Values are
// double-quoted with \ and " escaped, the option-file syntax of both MySQL and
// MariaDB. MYSQL_PWD is not used: MySQL 9.7 deprecates it.
const mysqlLogin = `pw=${MYSQL_ROOT_PASSWORD:-$(cat "${MYSQL_ROOT_PASSWORD_FILE:-/dev/null}" 2>/dev/null)}
[ -n "$pw" ] || pw=${MARIADB_ROOT_PASSWORD:-$(cat "${MARIADB_ROOT_PASSWORD_FILE:-/dev/null}" 2>/dev/null)}
if [ -n "$pw" ]; then
	u=root
else
	u=${MYSQL_USER:-$(cat "${MYSQL_USER_FILE:-/dev/null}" 2>/dev/null)}
	[ -n "$u" ] || u=${MARIADB_USER:-$(cat "${MARIADB_USER_FILE:-/dev/null}" 2>/dev/null)}
	pw=${MYSQL_PASSWORD:-$(cat "${MYSQL_PASSWORD_FILE:-/dev/null}" 2>/dev/null)}
	[ -n "$pw" ] || pw=${MARIADB_PASSWORD:-$(cat "${MARIADB_PASSWORD_FILE:-/dev/null}" 2>/dev/null)}
	if [ -z "$u" ]; then
		u=root
		pw=
	fi
fi
f=$(mktemp) || exit 1
{
	printf '[client]\n'
	printf 'user="%s"\n' "$(printf '%s' "$u" | sed 's/[\\"]/\\&/g')"
	[ -z "$pw" ] || printf 'password="%s"\n' "$(printf '%s' "$pw" | sed 's/[\\"]/\\&/g')"
} >"$f" || exit 1
unset pw
`

// mysqlDumpScript writes an SQL dump of database "$1" to stdout, with
// mariadb-dump when the image has it, else mysqldump. --no-tablespaces and
// --set-gtid-purged=OFF let a non-root MySQL user dump (they avoid the PROCESS
// and RELOAD privileges); MariaDB rejects --set-gtid-purged, so an older
// MariaDB whose dump tool is still called mysqldump does not get it.
const mysqlDumpScript = scriptStart + mysqlLogin +
	`if command -v mariadb-dump >/dev/null 2>&1; then
	mariadb-dump --defaults-extra-file="$f" --single-transaction --routines --triggers --no-tablespaces --databases "$1"
else
	case $(mysqldump --version 2>/dev/null) in
	*MariaDB*) mysqldump --defaults-extra-file="$f" --single-transaction --routines --triggers --no-tablespaces --databases "$1" ;;
	*) mysqldump --defaults-extra-file="$f" --set-gtid-purged=OFF --single-transaction --routines --triggers --no-tablespaces --databases "$1" ;;
	esac
fi
`

// mysqlSizeScript prints the data and index bytes of database "$1". The name
// has to go into the SQL, so the script checks it again with the {dbname}
// slot's rule before anything else: no character of that set can end the
// quoted literal.
const mysqlSizeScript = scriptStart +
	`case "$1" in
'' | [!A-Za-z0-9_]* | *[!A-Za-z0-9_.\$-]*)
	echo 'invalid database name' >&2
	exit 2
	;;
esac
if [ "${#1}" -gt 64 ]; then
	echo 'invalid database name' >&2
	exit 2
fi
` + mysqlLogin +
	`c=mysql
if command -v mariadb >/dev/null 2>&1; then
	c=mariadb
fi
"$c" --defaults-extra-file="$f" -N -B -e "select coalesce(sum(data_length+index_length),0) from information_schema.tables where table_schema = '$1'"
`

// mongoDumpScript writes a mongodump archive of database "$1" to stdout. With
// a root user (MONGO_INITDB_ROOT_USERNAME) it logs in against admin; the
// password goes into a --config file, which the database tools accept with
// password: alone, as a single-quoted YAML string (' doubled). The user name
// is not a secret and stays on argv.
const mongoDumpScript = scriptStart +
	`u=${MONGO_INITDB_ROOT_USERNAME:-$(cat "${MONGO_INITDB_ROOT_USERNAME_FILE:-/dev/null}" 2>/dev/null)}
if [ -z "$u" ]; then
	mongodump --archive --db "$1"
else
	pw=${MONGO_INITDB_ROOT_PASSWORD:-$(cat "${MONGO_INITDB_ROOT_PASSWORD_FILE:-/dev/null}" 2>/dev/null)}
	f=$(mktemp) || exit 1
	printf "password: '%s'\n" "$(printf '%s' "$pw" | sed "s/'/''/g")" >"$f" || exit 1
	unset pw
	mongodump --archive --db "$1" --username "$u" --authenticationDatabase admin --config "$f"
fi
`

// mongoJS is the fixed program of the MongoDB size read. It takes the user,
// password and database name from its own environment, never from its text,
// and prints the database's dataSize in bytes.
const mongoJS = `const url = "mongodb://127.0.0.1:27017/admin"; ` +
	`const c = process.env.U ? connect(url, process.env.U, process.env.P) : connect(url); ` +
	`print(c.getSiblingDB(process.env.N).stats().dataSize);`

// mongoSizeScript runs mongoJS. mongosh has no config file, so U, P and N are
// set for that one mongosh process only. --norc skips any .mongoshrc.js.
const mongoSizeScript = scriptStart +
	`u=${MONGO_INITDB_ROOT_USERNAME:-$(cat "${MONGO_INITDB_ROOT_USERNAME_FILE:-/dev/null}" 2>/dev/null)}
pw=${MONGO_INITDB_ROOT_PASSWORD:-$(cat "${MONGO_INITDB_ROOT_PASSWORD_FILE:-/dev/null}" 2>/dev/null)}
U=$u P=$pw N="$1" mongosh --quiet --norc --nodb --eval '` + mongoJS + `'
`
