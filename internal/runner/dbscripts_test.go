package runner

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// allScripts names every database script constant.
var allScripts = map[string]string{
	"pgDump":    pgDumpScript,
	"pgSize":    pgSizeScript,
	"mysqlDump": mysqlDumpScript,
	"mysqlSize": mysqlSizeScript,
	"mongoDump": mongoDumpScript,
	"mongoSize": mongoSizeScript,
}

// scriptLines returns the lines of script that contain sub.
func scriptLines(script, sub string) []string {
	var got []string
	for _, l := range strings.Split(script, "\n") {
		if strings.Contains(l, sub) {
			got = append(got, l)
		}
	}
	return got
}

func TestMySQLDumpScriptNoPasswordOnArgvAndFlags(t *testing.T) {
	for name, s := range allScripts {
		for _, bad := range []string{`-p"$`, "-p$", "--password", "MYSQL_PWD", "PGPASSWORD"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
		if exportsSecret.MatchString(s) {
			t.Errorf("%s exports a credential variable", name)
		}
	}
	const flags = `--single-transaction --routines --triggers --no-tablespaces --databases "$1"`
	var dumps int
	for _, l := range strings.Split(mysqlDumpScript, "\n") {
		isMaria := strings.Contains(l, "mariadb-dump --")
		isMySQL := strings.Contains(l, "mysqldump --defaults")
		if !isMaria && !isMySQL {
			continue
		}
		dumps++
		if !strings.Contains(l, `--defaults-extra-file="$f" `) || !strings.HasSuffix(strings.TrimSuffix(l, " ;;"), flags) {
			t.Errorf("dump line %q lacks the defaults file or the flags", l)
		}
		if isMaria && strings.Contains(l, "--set-gtid-purged") {
			t.Errorf("mariadb-dump gets --set-gtid-purged: %q", l)
		}
	}
	if dumps != 3 {
		t.Fatalf("found %d dump lines, want 3 (mariadb-dump, MariaDB's mysqldump, MySQL's mysqldump)", dumps)
	}
	gtid := scriptLines(mysqlDumpScript, "--set-gtid-purged=OFF")
	if len(gtid) != 1 || !strings.Contains(gtid[0], "mysqldump --defaults-extra-file") {
		t.Fatalf("--set-gtid-purged=OFF lines = %q, want one mysqldump line", gtid)
	}
	if !strings.Contains(mysqlDumpScript, "command -v mariadb-dump") || !strings.Contains(mysqlDumpScript, "*MariaDB*)") {
		t.Fatal("the dump does not choose mariadb-dump first and skip the GTID flag for MariaDB's mysqldump")
	}
	if !strings.Contains(mysqlSizeScript, `case "$1" in`) || !strings.Contains(mysqlSizeScript, `'' | [!A-Za-z0-9_]* | *[!A-Za-z0-9_.\$-]*)`) {
		t.Fatal("the size script does not check the name again with case")
	}
	if !strings.Contains(mysqlSizeScript, `table_schema = '$1'"`) {
		t.Fatal("the size query does not compare table_schema with the checked name")
	}
	for _, s := range []string{mysqlDumpScript, mysqlSizeScript} {
		if n := len(scriptLines(s, "printf 'password=")); n != 1 {
			t.Fatalf("password is written %d times, want once, to the defaults file", n)
		}
	}
}

func TestMongoScriptsPasswordOnlyInConfigOrEnv(t *testing.T) {
	cfg := scriptLines(mongoDumpScript, "password:")
	if len(cfg) != 1 || !strings.HasSuffix(cfg[0], `>"$f" || exit 1`) {
		t.Fatalf("config lines = %q, want one write of password: to the temp file", cfg)
	}
	if strings.Contains(mongoDumpScript, "username:") || strings.Contains(mongoDumpScript, "-p ") {
		t.Fatal("the dump puts more than the password in the config, or a password flag on argv")
	}
	if !strings.Contains(mongoDumpScript, `mongodump --archive --db "$1" --username "$u" --authenticationDatabase admin --config "$f"`) {
		t.Fatal("the authenticated dump argv changed")
	}
	if !strings.Contains(mongoDumpScript, `mongodump --archive --db "$1"`+"\n") {
		t.Fatal("the dump without a root user changed")
	}
	run := scriptLines(mongoSizeScript, "mongosh")
	if len(run) != 1 || !strings.HasPrefix(strings.TrimSpace(run[0]), `U=$u P=$pw N="$1" mongosh --quiet --norc --nodb --eval '`) {
		t.Fatalf("mongosh lines = %q, want U/P/N set for that one process", run)
	}
	js := mongoJS
	for _, want := range []string{"process.env.U", "process.env.P", "getSiblingDB(process.env.N)", "dataSize"} {
		if !strings.Contains(js, want) {
			t.Errorf("JS lacks %s", want)
		}
	}
	if strings.ContainsAny(js, "$'`") {
		t.Fatal("the JS holds a shell character; nothing may be spliced into it")
	}
	if !strings.Contains(mongoSizeScript, "'"+js+"'") {
		t.Fatal("the JS is not passed as one single-quoted argument")
	}
}

func TestPgScriptsUseSocketFirst(t *testing.T) {
	for name, s := range map[string]string{"pgDump": pgDumpScript, "pgSize": pgSizeScript} {
		probe := strings.Index(s, "if ! psql --no-password -X -q -U \"$u\" -d \"$d\" -tAc 'select 1'")
		tmp := strings.Index(s, "mktemp")
		if probe < 0 || tmp < probe {
			t.Errorf("%s does not try the socket without a password before writing a PGPASSFILE", name)
		}
		for _, bad := range []string{" -h ", "--host", "PGHOST"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s names a host (%q); it must use the local socket", name, bad)
			}
		}
		if !strings.Contains(s, `sed 's/[\\:]/\\&/g'`) {
			t.Errorf("%s does not escape : and \\ in the PGPASSFILE", name)
		}
		if !strings.Contains(s, `d="${1:-}"`) || !strings.Contains(s, "d=${d:-$u}") {
			t.Errorf("%s does not fall back to the image's default database", name)
		}
	}
	if !strings.HasSuffix(pgDumpScript, `pg_dump --no-password --clean --if-exists -U "$u" -d "$d"`+"\n") {
		t.Fatal("pg_dump line changed")
	}
	if !strings.HasSuffix(pgSizeScript, `-tAc 'select pg_database_size(current_database())'`+"\n") {
		t.Fatal("size query changed")
	}
}

// exportsSecret matches an export of the variables that hold credentials.
var exportsSecret = regexp.MustCompile(`(?m)\bexport\b.*\b(pw|u|P|U)\b`)

// nameUse finds $1 in any form: $1, ${1...} and ${#1}.
var nameUse = regexp.MustCompile(`\$(1|\{#?1)`)

func TestScriptsQuoteNameAndCleanUp(t *testing.T) {
	for name, s := range allScripts {
		if !strings.HasPrefix(s, "umask 077\n") {
			t.Errorf("%s does not start with umask 077", name)
		}
		if !strings.Contains(s, `trap '[ -z "${f:-}" ] || rm -f "$f"' EXIT`) || !strings.Contains(s, "trap 'exit 1' HUP INT TERM PIPE") {
			t.Errorf("%s does not remove its temp file on every exit", name)
		}
		if strings.Contains(s, "mktemp") && !strings.Contains(s, "f=$(mktemp) || exit 1") {
			t.Errorf("%s uses mktemp without checking it", name)
		}
		if strings.Contains(s, "exec ") {
			t.Errorf("%s uses exec, which skips the cleanup trap", name)
		}
		for _, l := range strings.Split(s, "\n") {
			for _, loc := range nameUse.FindAllStringIndex(l, -1) {
				if strings.Count(l[:loc[0]], `"`)%2 != 1 {
					t.Errorf("%s: unquoted name in %q", name, l)
				}
			}
		}
	}
}

func TestScriptsReadFileTwins(t *testing.T) {
	vars := map[string][]string{
		"pgDump":    {"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"},
		"pgSize":    {"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"},
		"mysqlDump": {"MYSQL_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD", "MYSQL_USER", "MARIADB_USER", "MYSQL_PASSWORD", "MARIADB_PASSWORD"},
		"mysqlSize": {"MYSQL_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD", "MYSQL_USER", "MARIADB_USER", "MYSQL_PASSWORD", "MARIADB_PASSWORD"},
		"mongoDump": {"MONGO_INITDB_ROOT_USERNAME", "MONGO_INITDB_ROOT_PASSWORD"},
		"mongoSize": {"MONGO_INITDB_ROOT_USERNAME", "MONGO_INITDB_ROOT_PASSWORD"},
	}
	for name, vs := range vars {
		for _, v := range vs {
			want := "${" + v + `:-$(cat "${` + v + `_FILE:-/dev/null}" 2>/dev/null)}`
			if !strings.Contains(allScripts[name], want) {
				t.Errorf("%s does not read %s or its _FILE twin", name, v)
			}
		}
	}
}

func TestScriptsParse(t *testing.T) {
	for name, s := range allScripts {
		out, err := exec.Command("sh", "-n", "-c", s, "sh", "shop").CombinedOutput() //nolint:gosec // G204: parses the package's own constant scripts
		if err != nil {
			t.Errorf("%s does not parse: %v\n%s", name, err, out)
		}
	}
}

// fakeTool stands in for every database client the scripts call. It appends
// its argv, its environment and the contents of the credentials file it was
// given to files named after itself in $FAKE_OUT.
const fakeTool = `#!/bin/sh
n=$(basename "$0")
o=$FAKE_OUT
{ echo '--call'; printf '%s\n' "$@"; } >>"$o/$n.argv"
env >>"$o/$n.env"
prev=
for a in "$@"; do
	case $a in
	--defaults-extra-file=*) cat "${a#--defaults-extra-file=}" >>"$o/$n.file" ;;
	esac
	if [ "$prev" = --config ]; then
		cat "$a" >>"$o/$n.file"
	fi
	prev=$a
done
if [ -n "${PGPASSFILE:-}" ]; then
	cat "$PGPASSFILE" >>"$o/$n.file"
fi
if [ "$n" = mysqldump ] && [ "${1:-}" = --version ]; then
	echo "${FAKE_DUMP_VERSION:-mysqldump  Ver 9.7.4 for Linux}"
fi
for a in "$@"; do
	if [ "$a" = 'select 1' ]; then
		exit "${FAKE_PROBE_EXIT:-0}"
	fi
done
echo "$n output"
`

type scriptRun struct {
	out, tmp string
	code     int
	stdout   string
}

func (r scriptRun) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.out, name)) //nolint:gosec // G304: a file the fake wrote in the test's temp dir
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// runScript runs script under sh with fake clients for tools on PATH, the
// environment env and the arguments args, the way compose exec runs it.
func runScript(t *testing.T, script string, tools []string, env []string, args ...string) scriptRun {
	t.Helper()
	dir := t.TempDir()
	bin, out, tmp := filepath.Join(dir, "bin"), filepath.Join(dir, "out"), filepath.Join(dir, "tmp")
	for _, d := range []string{bin, out, tmp} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range tools {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(fakeTool), 0o700); err != nil { //nolint:gosec // G306: the fake must be executable
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", append([]string{"-c", script, "sh"}, args...)...) //nolint:gosec // G204: runs the package's own constant scripts against fakes
	cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "FAKE_OUT=" + out, "TMPDIR=" + tmp}, env...)
	stdout, err := cmd.Output()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
	return scriptRun{out: out, tmp: tmp, code: code, stdout: string(stdout)}
}

// secretFile writes value to a file for a *_FILE variable.
func secretFile(t *testing.T, value string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// hostilePassword holds every character the credentials files must escape.
const hostilePassword = `p:a\ss"w'o#r d;$x` //nolint:gosec // G101: a fake password, the test subject

func noSecretOnArgv(t *testing.T, r scriptRun, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if strings.Contains(r.read(t, tool+".argv"), "ss\"w") {
			t.Errorf("%s got the password on argv", tool)
		}
	}
}

func TestPgScriptsBehaviour(t *testing.T) {
	tools := []string{"psql", "pg_dump"}
	t.Run("socket", func(t *testing.T) {
		r := runScript(t, pgDumpScript, tools, []string{"POSTGRES_PASSWORD=" + hostilePassword}, "shop")
		if r.code != 0 || r.stdout != "pg_dump output\n" {
			t.Fatalf("exit %d, stdout %q", r.code, r.stdout)
		}
		if f := r.read(t, "pg_dump.file"); f != "" || strings.Contains(r.read(t, "pg_dump.env"), "PGPASSFILE") {
			t.Fatalf("a PGPASSFILE was written although the socket needs no password: %q", f)
		}
		if got := r.read(t, "pg_dump.argv"); got != "--call\n--no-password\n--clean\n--if-exists\n-U\npostgres\n-d\nshop\n" {
			t.Fatalf("pg_dump argv %q", got)
		}
	})
	t.Run("password from file, default database", func(t *testing.T) {
		env := []string{
			"POSTGRES_USER_FILE=" + secretFile(t, "app"),
			"POSTGRES_PASSWORD_FILE=" + secretFile(t, hostilePassword),
			"FAKE_PROBE_EXIT=2",
		}
		r := runScript(t, pgDumpScript, tools, env)
		if r.code != 0 {
			t.Fatalf("exit %d", r.code)
		}
		if got, want := r.read(t, "pg_dump.file"), `*:*:*:*:p\:a\\ss"w'o#r d;$x`+"\n"; got != want {
			t.Fatalf("PGPASSFILE = %q, want %q", got, want)
		}
		if got := r.read(t, "pg_dump.argv"); got != "--call\n--no-password\n--clean\n--if-exists\n-U\napp\n-d\napp\n" {
			t.Fatalf("pg_dump argv %q", got)
		}
		noSecretOnArgv(t, r, tools...)
		if strings.Contains(r.read(t, "pg_dump.env"), "ss\"w") {
			t.Fatal("the password reached pg_dump's environment")
		}
	})
	t.Run("default database from POSTGRES_DB", func(t *testing.T) {
		r := runScript(t, pgSizeScript, tools, []string{"POSTGRES_DB=main"})
		if got := r.read(t, "psql.argv"); !strings.HasSuffix(got, "-d\nmain\n-tAc\nselect pg_database_size(current_database())\n") {
			t.Fatalf("psql argv %q", got)
		}
	})
}

func TestMySQLScriptsBehaviour(t *testing.T) {
	t.Run("root from file, MySQL", func(t *testing.T) {
		tools := []string{"mysqldump"}
		r := runScript(t, mysqlDumpScript, tools, []string{"MYSQL_ROOT_PASSWORD_FILE=" + secretFile(t, hostilePassword), "MYSQL_USER=app"}, "shop")
		if r.code != 0 {
			t.Fatalf("exit %d", r.code)
		}
		want := "[client]\nuser=\"root\"\npassword=\"p:a\\\\ss\\\"w'o#r d;$x\"\n"
		if got := r.read(t, "mysqldump.file"); got != want {
			t.Fatalf("defaults file = %q, want %q", got, want)
		}
		argv := r.read(t, "mysqldump.argv")
		if !strings.Contains(argv, "\n--set-gtid-purged=OFF\n--single-transaction\n--routines\n--triggers\n--no-tablespaces\n--databases\nshop\n") {
			t.Fatalf("mysqldump argv %q", argv)
		}
		noSecretOnArgv(t, r, tools...)
		if strings.Contains(r.read(t, "mysqldump.env"), "ss\"w") {
			t.Fatal("the password reached mysqldump's environment")
		}
	})
	t.Run("user, MariaDB", func(t *testing.T) {
		tools := []string{"mariadb-dump", "mysqldump"}
		r := runScript(t, mysqlDumpScript, tools, []string{"MARIADB_USER=app", "MARIADB_PASSWORD=" + hostilePassword}, "shop")
		if got := r.read(t, "mariadb-dump.file"); !strings.HasPrefix(got, "[client]\nuser=\"app\"\npassword=") {
			t.Fatalf("defaults file = %q", got)
		}
		if strings.Contains(r.read(t, "mariadb-dump.argv"), "gtid") || r.read(t, "mysqldump.argv") != "" {
			t.Fatal("MariaDB got the GTID flag, or mysqldump ran instead of mariadb-dump")
		}
	})
	t.Run("MariaDB's mysqldump", func(t *testing.T) {
		r := runScript(t, mysqlDumpScript, []string{"mysqldump"}, []string{"FAKE_DUMP_VERSION=mysqldump from 10.4.0-MariaDB"}, "shop")
		if argv := r.read(t, "mysqldump.argv"); strings.Contains(argv, "gtid") || !strings.Contains(argv, "--databases\nshop\n") {
			t.Fatalf("mysqldump argv %q", argv)
		}
		if got := r.read(t, "mysqldump.file"); got != "[client]\nuser=\"root\"\n" {
			t.Fatalf("defaults file without credentials = %q", got)
		}
	})
	t.Run("size", func(t *testing.T) {
		r := runScript(t, mysqlSizeScript, []string{"mysql"}, []string{"MYSQL_ROOT_PASSWORD=" + hostilePassword}, "a.b-c$d")
		want := "--call\n--defaults-extra-file=" // then the temp path
		if argv := r.read(t, "mysql.argv"); !strings.HasPrefix(argv, want) ||
			!strings.HasSuffix(argv, "\n-N\n-B\n-e\nselect coalesce(sum(data_length+index_length),0) from information_schema.tables where table_schema = 'a.b-c$d'\n") {
			t.Fatalf("mysql argv %q", argv)
		}
	})
	t.Run("size refuses a bad name", func(t *testing.T) {
		for _, name := range []string{"", "-x", ".x", "a'b", "a b", "a\\b", strings.Repeat("a", 65)} {
			r := runScript(t, mysqlSizeScript, []string{"mysql", "mariadb"}, nil, name)
			if r.code != 2 || r.read(t, "mysql.argv") != "" || r.read(t, "mariadb.argv") != "" {
				t.Errorf("%q: exit %d, a client ran", name, r.code)
			}
		}
	})
}

func TestMongoScriptsBehaviour(t *testing.T) {
	t.Run("root from file", func(t *testing.T) {
		tools := []string{"mongodump"}
		env := []string{"MONGO_INITDB_ROOT_USERNAME=root", "MONGO_INITDB_ROOT_PASSWORD_FILE=" + secretFile(t, hostilePassword)}
		r := runScript(t, mongoDumpScript, tools, env, "shop")
		if r.code != 0 {
			t.Fatalf("exit %d", r.code)
		}
		if got, want := r.read(t, "mongodump.file"), `password: 'p:a\ss"w''o#r d;$x'`+"\n"; got != want {
			t.Fatalf("config = %q, want %q", got, want)
		}
		argv := r.read(t, "mongodump.argv")
		if !strings.HasPrefix(argv, "--call\n--archive\n--db\nshop\n--username\nroot\n--authenticationDatabase\nadmin\n--config\n") {
			t.Fatalf("mongodump argv %q", argv)
		}
		noSecretOnArgv(t, r, tools...)
	})
	t.Run("no root user", func(t *testing.T) {
		r := runScript(t, mongoDumpScript, []string{"mongodump"}, nil, "shop")
		if got := r.read(t, "mongodump.argv"); got != "--call\n--archive\n--db\nshop\n" {
			t.Fatalf("mongodump argv %q", got)
		}
	})
	t.Run("size", func(t *testing.T) {
		env := []string{"MONGO_INITDB_ROOT_USERNAME_FILE=" + secretFile(t, "root"), "MONGO_INITDB_ROOT_PASSWORD=" + hostilePassword}
		r := runScript(t, mongoSizeScript, []string{"mongosh"}, env, "shop")
		got := r.read(t, "mongosh.env")
		for _, want := range []string{"\nU=root\n", "\nP=" + hostilePassword + "\n", "\nN=shop\n"} {
			if !strings.Contains("\n"+got, want) {
				t.Errorf("mongosh env lacks %q", want)
			}
		}
		if argv := r.read(t, "mongosh.argv"); argv != "--call\n--quiet\n--norc\n--nodb\n--eval\n"+mongoJS+"\n" {
			t.Fatalf("mongosh argv %q", argv)
		}
	})
}
