package runner

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// dumpScripts are the scripts that run a dumper, record it and wait for it.
var dumpScripts = map[string]bool{"pgDump": true, "mysqlDump": true, "mongoDump": true}

// allScripts names every database script constant but the stop script.
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
		if !strings.Contains(l, `--defaults-extra-file="$f" `) || !strings.HasSuffix(strings.TrimSuffix(l, " ;;"), flags+" &") {
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
	if !strings.Contains(mongoDumpScript, `mongodump --archive --db "$1" --username "$u" --authenticationDatabase admin --config "$f" &`+"\n") {
		t.Fatal("the authenticated dump argv changed")
	}
	if !strings.Contains(mongoDumpScript, `mongodump --archive --db "$1" &`+"\n") {
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
		tmp := strings.Index(s, "f=$(mktemp)")
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
	if !strings.HasSuffix(pgDumpScript, `pg_dump --no-password --clean --if-exists -U "$u" -d "$d" &`+"\n"+wantDumpTail) {
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
		exitTrap, sigTrap := `trap '[ -z "${f:-}" ] || rm -f "$f"' EXIT`, "trap 'exit 1' HUP INT TERM PIPE"
		if dumpScripts[name] {
			exitTrap = `trap '[ -z "${f:-}" ] || rm -f "$f"; [ -z "${t:-}" ] || rm -f "$t"; [ -z "${o:-}" ] || rm -f /tmp/.scp-dump.pid' EXIT`
			sigTrap = `trap '[ -z "${k:-}" ] || { kill -s TERM "$k"; kill -s CONT "$k"; wait "$k"; } 2>/dev/null; exit 1' HUP INT TERM PIPE`
		}
		if strings.Count(s, "trap '") != 2 || !strings.Contains(s, exitTrap) || !strings.Contains(s, sigTrap) {
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
	scripts := map[string]string{"dumpStop": dumpStopScript}
	for name, s := range allScripts {
		scripts[name] = s
	}
	for name, s := range scripts {
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
	if [ -n "${FAKE_VERSION_SLOW:-}" ]; then
		echo "$$" >"$o/version.pid"
		i=0
		while [ "$i" -lt 600 ]; do
			sleep 0.1
			i=$((i + 1))
		done
	fi
	echo "${FAKE_DUMP_VERSION:-mysqldump  Ver 9.7.4 for Linux}"
fi
for a in "$@"; do
	if [ "$a" = 'select 1' ]; then
		exit "${FAKE_PROBE_EXIT:-0}"
	fi
done
if [ -n "${FAKE_SLOW:-}" ] && [ "${1:-}" != --version ]; then
	[ -z "${FAKE_IGNORE_TERM:-}" ] || trap '' TERM
	[ -z "${FAKE_TERM_DELAY:-}" ] || trap 'sleep 0.5; exit 143' TERM
	i=0
	while [ "$i" -lt 600 ]; do
		sleep 0.1
		i=$((i + 1))
	done
fi
echo "$n output"
exit "${FAKE_EXIT:-0}"
`

type scriptRun struct {
	out, tmp string
	code     int
	stdout   string
	stderr   string
}

func (r scriptRun) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.out, name)) //nolint:gosec // G304: a file the fake wrote in the test's temp dir
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// scriptDirs are a script run's fake clients (bin), what they record (out) and
// its TMPDIR (tmp).
type scriptDirs struct{ bin, out, tmp string }

// newScriptDirs makes the directories of one run, with fake clients for tools.
func newScriptDirs(t *testing.T, tools []string) scriptDirs {
	t.Helper()
	dir := t.TempDir()
	d := scriptDirs{filepath.Join(dir, "bin"), filepath.Join(dir, "out"), filepath.Join(dir, "tmp")}
	for _, p := range []string{d.bin, d.out, d.tmp} {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range tools {
		if err := os.WriteFile(filepath.Join(d.bin, tool), []byte(fakeTool), 0o700); err != nil { //nolint:gosec // G306: the fake must be executable
			t.Fatal(err)
		}
	}
	return d
}

// command is script under sh, the way compose exec runs it, with the fakes
// first on PATH and the environment env.
func (d scriptDirs) command(script string, env []string, args ...string) *exec.Cmd {
	cmd := exec.Command("sh", append([]string{"-c", script, "sh"}, args...)...) //nolint:gosec // G204: runs the package's own constant scripts against fakes
	cmd.Env = append([]string{"PATH=" + d.bin + ":/usr/bin:/bin", "FAKE_OUT=" + d.out, "TMPDIR=" + d.tmp}, env...)
	return cmd
}

// finish waits for cmd and checks that the run left no temp file behind, in
// TMPDIR or as the pid file.
func (d scriptDirs) finish(t *testing.T, cmd *exec.Cmd, stdout, stderr *strings.Builder) scriptRun {
	t.Helper()
	err := cmd.Wait()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadDir(d.tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
	return scriptRun{out: d.out, tmp: d.tmp, code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// start starts script and returns the command and its output buffers.
func (d scriptDirs) start(t *testing.T, script string, env []string, args ...string) (*exec.Cmd, *strings.Builder, *strings.Builder) {
	t.Helper()
	cmd := d.command(script, env, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd, &stdout, &stderr
}

// runScript runs script under sh with fake clients for tools on PATH, the
// environment env and the arguments args, the way compose exec runs it. A
// dump script must leave no pid file behind.
func runScript(t *testing.T, script string, tools []string, env []string, args ...string) scriptRun {
	t.Helper()
	d := newScriptDirs(t, tools)
	cmd, stdout, stderr := d.start(t, script, env, args...)
	r := d.finish(t, cmd, stdout, stderr)
	if _, err := os.Lstat(dumpPidFile); err == nil {
		t.Errorf("%s left behind", dumpPidFile)
	}
	return r
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

// dumpPidFile is where a running dump records its shell's and its dumper's
// pids. The scripts spell it out; it is never spliced into them.
const dumpPidFile = "/tmp/.scp-dump.pid"

// wantDumpTail ends every dump script: it waits for the dumper and exits with
// its status, forgetting the pid first so that a late signal is not forwarded
// to a reused one.
const wantDumpTail = "k=$!\nscp_claim\nwait \"$k\"\ne=$?\nk=\nexit \"$e\"\n"

// The dump scripts refuse while a previous dump lives, before any credential
// is read; run the dumper as a child they record and wait for; and touch no
// file but their own temp files and the pid file.
func TestDumpScriptsRecordTheirDumper(t *testing.T) {
	dumpers := map[string]int{"pgDump": 1, "mysqlDump": 3, "mongoDump": 2}
	for name, s := range allScripts {
		if !dumpScripts[name] {
			if strings.Contains(s, "scp-dump") || strings.Contains(s, " &\n") {
				t.Errorf("%s is a read; it must not record or background anything", name)
			}
			continue
		}
		refuse := strings.Index(s, "echo 'a previous dump is still running' >&2\n\texit 1\n")
		login := strings.Index(s, "${MONGO_INITDB_ROOT_USERNAME:-")
		for _, l := range []string{"${POSTGRES_USER:-", "${MYSQL_ROOT_PASSWORD:-"} {
			if i := strings.Index(s, l); i >= 0 {
				login = i
			}
		}
		if refuse < 0 || login < 0 || refuse > login {
			t.Errorf("%s does not refuse a second dump before it reads a credential", name)
		}
		if !strings.HasSuffix(s, wantDumpTail) {
			t.Errorf("%s does not end by waiting for its dumper", name)
		}
		if n := strings.Count(s, " &\n") + strings.Count(s, " & ;;\n"); n != dumpers[name] {
			t.Errorf("%s backgrounds %d commands, want %d (its dumper lines)", name, n, dumpers[name])
		}
		if n := strings.Count(s, "/tmp/.scp-dump."); n != strings.Count(s, dumpPidFile)+1 {
			t.Errorf("%s names a scp-dump path other than %s and its mktemp template", name, dumpPidFile)
		}
		for _, bad := range []string{"eval", "exec ", "kill -s KILL", "rm -rf"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
	}
	claim := "scp_claim() {\n" +
		"\tt=$(mktemp /tmp/.scp-dump.XXXXXX) || return 0\n" +
		"\tif printf '%s %s\\n' \"$$\" \"${k:-}\" >\"$t\" && mv -fT \"$t\" /tmp/.scp-dump.pid 2>/dev/null; then\n" +
		"\t\tt=\n\t\to=1\n" +
		"\telse\n\t\trm -f \"$t\"\n\t\tt=\n\tfi\n}\n"
	if !strings.Contains(dumpGuard, claim) {
		t.Fatal("the pid file is not written through a private mktemp file renamed into place with mv -T")
	}
	for _, l := range scriptLines(dumpGuard, "mv ") {
		if !strings.Contains(l, "mv -fT ") {
			t.Errorf("a rename without -T, which would move into a directory: %q", l)
		}
	}
}

// The stop script reads only the pid file, signals only a live dumper or dump
// shell, and takes no value.
func TestDumpStopScriptShape(t *testing.T) {
	s := dumpStopScript
	if !strings.HasPrefix(s, "umask 077\n") {
		t.Error("the stop script does not start with umask 077")
	}
	if nameUse.MatchString(strings.ReplaceAll(s, `i="$1"`, "")) {
		t.Error("the stop script reads an argument; it takes none")
	}
	for _, bad := range []string{"eval", "exec ", "rm -rf", "mktemp", "PASSWORD", "$(cat \"$"} {
		if strings.Contains(s, bad) {
			t.Errorf("the stop script contains %q", bad)
		}
	}
	for _, l := range scriptLines(s, "rm ") {
		if strings.TrimSpace(l) != "rm -f "+dumpPidFile {
			t.Errorf("the stop script removes something else: %q", l)
		}
	}
	for _, l := range scriptLines(s, "kill ") {
		if !regexp.MustCompile(`kill -s (TERM|CONT|KILL) "\$[sk]"|kill -s TERM "\$c"`).MatchString(l) {
			t.Errorf("unexpected kill: %q", l)
		}
	}
	// A child gets TERM only when it is all digits, not the stop's own shell,
	// readable in /proc and a child of the verified dump shell s.
	children := "\t\tcase $c in\n\t\t'' | *[!0-9]*) continue ;;\n\t\tesac\n" +
		"\t\t[ \"$c\" != \"$$\" ] || continue\n" +
		"\t\tp=$(cat 2>/dev/null \"/proc/$c/stat\") || continue\n" +
		"\t\tp=${p##*) }\n\t\tp=${p#* }\n" +
		"\t\t[ \"${p%% *}\" = \"$s\" ] || continue\n" +
		"\t\tkill -s TERM \"$c\" 2>/dev/null\n"
	if !strings.Contains(s, children) {
		t.Error("the TERM to the dump shell's children is not guarded")
	}
	if !strings.Contains(s, "\tif ! scp_wait 10 \"$s\"; then\n\t\tscp_children\n\t\tscp_wait 10 \"$s\" || kill -s KILL \"$s\" 2>/dev/null\n") {
		t.Error("the children are not ended before the dump shell is KILLed")
	}
	if !strings.Contains(dumpLive, "pg_dump | mysqldump | mariadb-dump | mongodump)") {
		t.Fatal("the liveness check does not name the dumpers")
	}
}

// requireProc skips a test that needs Linux's /proc, which the scripts read.
func requireProc(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/proc/self/comm"); err != nil {
		t.Skip("needs /proc")
	}
}

// cleanPidFile removes the pid file before and after the test.
func cleanPidFile(t *testing.T) {
	t.Helper()
	_ = os.Remove(dumpPidFile)
	t.Cleanup(func() { _ = os.Remove(dumpPidFile) })
}

// pidFile reads the pid file's two fields, "" when absent.
func pidFile(t *testing.T) (shell, dumper string) {
	t.Helper()
	b, err := os.ReadFile(dumpPidFile)
	if err != nil {
		return "", ""
	}
	f := strings.Fields(string(b))
	switch len(f) {
	case 1:
		return f[0], ""
	case 2:
		return f[0], f[1]
	}
	t.Fatalf("pid file %q", b)
	return "", ""
}

// comm is a process's name, "" when it is gone.
func comm(pid string) string {
	b, err := os.ReadFile("/proc/" + pid + "/comm") //nolint:gosec // G304: a /proc path of a pid the test started
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// alive reports whether pid runs and is not a zombie.
func procAlive(pid string) bool {
	b, err := os.ReadFile("/proc/" + pid + "/stat") //nolint:gosec // G304: as above
	return err == nil && !strings.Contains(string(b), ") Z ")
}

// waitDumper waits until the pid file names a running tool, and returns the
// shell's and the tool's pids.
func waitDumper(t *testing.T, tool string) (shell, dumper string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, k := pidFile(t)
		if k != "" && comm(k) == tool {
			return s, k
		}
		if time.Now().After(deadline) {
			t.Fatalf("no running %s in the pid file (%q %q)", tool, s, k)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runStop runs the stop script and returns its exit code and stderr.
func runStop(t *testing.T) (int, string) {
	t.Helper()
	d := newScriptDirs(t, nil)
	cmd, stdout, stderr := d.start(t, dumpStopScript, nil)
	r := d.finish(t, cmd, stdout, stderr)
	if r.stdout != "" {
		t.Errorf("stop stdout %q", r.stdout)
	}
	return r.code, r.stderr
}

func TestDumpScriptsStopAndRefuse(t *testing.T) {
	requireProc(t)
	cases := []struct {
		name, script, tool string
		tools, env         []string
	}{
		{"pg", pgDumpScript, "pg_dump", []string{"psql", "pg_dump"}, nil},
		{"mysql", mysqlDumpScript, "mysqldump", []string{"mysqldump"}, []string{"MYSQL_ROOT_PASSWORD=" + hostilePassword}},
		{"mariadb", mysqlDumpScript, "mariadb-dump", []string{"mariadb-dump"}, []string{"MARIADB_ROOT_PASSWORD=" + hostilePassword}},
		{"mongo", mongoDumpScript, "mongodump", []string{"mongodump"}, []string{"MONGO_INITDB_ROOT_USERNAME=root", "MONGO_INITDB_ROOT_PASSWORD=" + hostilePassword}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanPidFile(t)
			d := newScriptDirs(t, tc.tools)
			cmd, stdout, stderr := d.start(t, tc.script, append([]string{"FAKE_SLOW=1"}, tc.env...), "shop")
			s, k := waitDumper(t, tc.tool)
			if s != strconv.Itoa(cmd.Process.Pid) {
				t.Errorf("the pid file names shell %s, want %d", s, cmd.Process.Pid)
			}

			// A second dump is refused before it runs anything.
			second := runSecond(t, tc.script, tc.tools, tc.env)
			if second.code != 1 || second.stdout != "" || second.stderr != "a previous dump is still running\n" {
				t.Errorf("second dump: exit %d, stdout %q, stderr %q", second.code, second.stdout, second.stderr)
			}
			for _, tool := range tc.tools {
				if got := second.read(t, tool+".argv"); got != "" {
					t.Errorf("the refused dump ran %s: %q", tool, got)
				}
			}
			if s2, k2 := pidFile(t); s2 != s || k2 != k {
				t.Errorf("the refused dump changed the pid file to %q %q", s2, k2)
			}

			began := time.Now()
			if code, msg := runStop(t); code != 0 || msg != "" {
				t.Fatalf("stop: exit %d, stderr %q", code, msg)
			}
			if took := time.Since(began); took > 3*time.Second {
				t.Errorf("stop took %v for a dumper that obeys TERM", took)
			}
			r := d.finish(t, cmd, stdout, stderr)
			if r.code == 0 || r.stdout != "" {
				t.Errorf("stopped dump: exit %d, stdout %q", r.code, r.stdout)
			}
			if procAlive(k) || procAlive(s) {
				t.Error("the dumper or its shell still runs")
			}
			if _, err := os.Lstat(dumpPidFile); err == nil {
				t.Error("the pid file is left after the stop")
			}
		})
	}
}

// runSecond runs a dump while another holds the pid file; unlike runScript it
// expects that pid file to stay.
func runSecond(t *testing.T, script string, tools, env []string) scriptRun {
	t.Helper()
	d := newScriptDirs(t, tools)
	cmd, stdout, stderr := d.start(t, script, env, "shop")
	return d.finish(t, cmd, stdout, stderr)
}

// A dumper that ignores TERM is killed; the shell then exits and cleans up.
func TestDumpStopKillsAStubbornDumper(t *testing.T) {
	requireProc(t)
	if testing.Short() {
		t.Skip("waits out the TERM grace")
	}
	cleanPidFile(t)
	d := newScriptDirs(t, []string{"psql", "pg_dump"})
	cmd, stdout, stderr := d.start(t, pgDumpScript, []string{"FAKE_SLOW=1", "FAKE_IGNORE_TERM=1"}, "shop")
	_, k := waitDumper(t, "pg_dump")
	if code, msg := runStop(t); code != 0 || msg != "" {
		t.Fatalf("stop: exit %d, stderr %q", code, msg)
	}
	r := d.finish(t, cmd, stdout, stderr)
	if r.code != 128+int(syscall.SIGKILL) || procAlive(k) {
		t.Errorf("dump exit %d, dumper alive %v", r.code, procAlive(k))
	}
	if _, err := os.Lstat(dumpPidFile); err == nil {
		t.Error("the pid file is left")
	}
}

// A pid file whose pids are gone, or name processes that are not a dump, does
// not block a dump, and the stop signals none of them.
func TestDumpPidFileStaleOrReused(t *testing.T) {
	requireProc(t)
	other := exec.Command("sleep", "30")
	shell := exec.Command("sh", "-c", "sleep 30; :")
	for _, c := range []*exec.Cmd{other, shell} {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	}
	reused := strconv.Itoa(shell.Process.Pid) + " " + strconv.Itoa(other.Process.Pid) + "\n"
	for name, content := range map[string]string{"dead": "999999999 999999998\n", "reused": reused, "garbage": "-1 $(id)\n", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			cleanPidFile(t)
			if err := os.WriteFile(dumpPidFile, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if code, msg := runStop(t); code != 0 || msg != "" {
				t.Fatalf("stop: exit %d, stderr %q", code, msg)
			}
			if _, err := os.Lstat(dumpPidFile); err == nil {
				t.Error("the stop left a stale pid file")
			}
			if !procAlive(strconv.Itoa(other.Process.Pid)) || !procAlive(strconv.Itoa(shell.Process.Pid)) {
				t.Fatal("the stop signalled a process that is not a dump")
			}
			if err := os.WriteFile(dumpPidFile, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			r := runScript(t, pgDumpScript, []string{"psql", "pg_dump"}, nil, "shop")
			if r.code != 0 || r.stdout != "pg_dump output\n" || r.stderr != "" {
				t.Errorf("dump over a stale pid file: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
		})
	}
	if code, msg := runStop(t); code != 0 || msg != "" {
		t.Fatalf("stop without a pid file: exit %d, stderr %q", code, msg)
	}
}

// The pid file is replaced, never written through: a symlink planted at its
// path does not redirect the write, and is not followed by the stop. A link to
// a directory does not make the claim put a file into that directory: GNU
// mv -T replaces the link, BusyBox mv -T refuses it and the dump runs
// unrecorded.
func TestDumpPidFileSymlinkNotFollowed(t *testing.T) {
	requireProc(t)
	t.Run("file", func(t *testing.T) {
		cleanPidFile(t)
		target := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, dumpPidFile); err != nil {
			t.Fatal(err)
		}
		if code, _ := runStop(t); code != 0 {
			t.Fatalf("stop over a symlink: exit %d", code)
		}
		r := runScript(t, pgDumpScript, []string{"psql", "pg_dump"}, nil, "shop")
		if r.code != 0 || r.stdout != "pg_dump output\n" {
			t.Errorf("dump: exit %d, stdout %q", r.code, r.stdout)
		}
		if b, err := os.ReadFile(target); err != nil || string(b) != "keep\n" { //nolint:gosec // G304: the test's own file
			t.Errorf("the symlink's target = %q, %v; it was written through", b, err)
		}
		noClaimLeft(t)
	})
	t.Run("directory", func(t *testing.T) {
		cleanPidFile(t)
		target := t.TempDir()
		if err := os.Symlink(target, dumpPidFile); err != nil {
			t.Fatal(err)
		}
		if code, _ := runStop(t); code != 0 {
			t.Fatalf("stop over a symlink: exit %d", code)
		}
		d := newScriptDirs(t, []string{"psql", "pg_dump"})
		cmd, stdout, stderr := d.start(t, pgDumpScript, nil, "shop")
		r := d.finish(t, cmd, stdout, stderr)
		if r.code != 0 || r.stdout != "pg_dump output\n" || r.stderr != "" {
			t.Errorf("dump: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
		}
		if left, err := os.ReadDir(target); err != nil || len(left) != 0 {
			t.Errorf("the claim wrote into the linked directory: %v, %v", left, err)
		}
		// Replaced (and removed at exit), or refused and left as it was.
		if fi, err := os.Lstat(dumpPidFile); err == nil {
			if to, _ := os.Readlink(dumpPidFile); fi.Mode()&os.ModeSymlink == 0 || to != target {
				t.Errorf("the pid file path is now %v -> %q", fi.Mode(), to)
			}
		}
		noClaimLeft(t)
	})
}

// A claim that cannot be made (an mv without -T, or a destination it refuses)
// is abandoned: its temp file is removed, nothing is recorded, and the dump
// runs as usual, with nothing added to its stderr.
func TestDumpClaimFailureRunsUnrecorded(t *testing.T) {
	requireProc(t)
	cleanPidFile(t)
	d := newScriptDirs(t, []string{"psql", "pg_dump"})
	failMv := "#!/bin/sh\necho 'mv: unrecognized option: T' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(d.bin, "mv"), []byte(failMv), 0o700); err != nil { //nolint:gosec // G306: the fake must be executable
		t.Fatal(err)
	}
	cmd, stdout, stderr := d.start(t, pgDumpScript, nil, "shop")
	r := d.finish(t, cmd, stdout, stderr)
	if r.code != 0 || r.stdout != "pg_dump output\n" || r.stderr != "" {
		t.Errorf("dump: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if _, err := os.Lstat(dumpPidFile); err == nil {
		t.Error("a pid file was recorded though every claim failed")
	}
	noClaimLeft(t)
}

// noClaimLeft fails when a claim's temp file (/tmp/.scp-dump.XXXXXX) is left.
func noClaimLeft(t *testing.T) {
	t.Helper()
	left, err := filepath.Glob("/tmp/.scp-dump.*")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range left {
		if p != dumpPidFile {
			t.Errorf("claim temp file left: %s", p)
			_ = os.Remove(p)
		}
	}
}

// A dump shell held in a foreground command (here mysqldump --version, after
// the credentials file exists) cannot run its trap while that command runs.
// The stop ends that child too, so the shell exits by itself and removes its
// credentials file and the pid file, instead of being KILLed and leaving them.
func TestDumpStopEndsTheShellsForegroundChild(t *testing.T) {
	requireProc(t)
	if testing.Short() {
		t.Skip("waits out the TERM grace")
	}
	cleanPidFile(t)
	d := newScriptDirs(t, []string{"mysqldump"})
	cmd := d.command(mysqlDumpScript, []string{"FAKE_VERSION_SLOW=1", "MYSQL_ROOT_PASSWORD=" + hostilePassword}, "shop")
	// Files, not pipes: Wait returns when the shell exits, not when the last
	// holder of its stderr does.
	out, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	var child int
	deadline := time.Now().Add(5 * time.Second)
	for child == 0 {
		b, _ := os.ReadFile(filepath.Join(d.out, "version.pid")) //nolint:gosec // G304: a file the fake wrote in the test's temp dir
		child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		if time.Now().After(deadline) {
			t.Fatal("mysqldump --version did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if s, _ := pidFile(t); s != strconv.Itoa(cmd.Process.Pid) {
		t.Fatalf("the pid file names shell %q, want %d", s, cmd.Process.Pid)
	}
	if left, _ := os.ReadDir(d.tmp); len(left) != 1 {
		t.Fatalf("credentials files %v, want one", left)
	}

	if code, msg := runStop(t); code != 0 || msg != "" {
		t.Fatalf("stop: exit %d, stderr %q", code, msg)
	}
	var ee *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &ee) || ee.ExitCode() != 1 {
		t.Errorf("exit = %v, want 1 (the shell's own trap, not KILL)", err)
	}
	if left, _ := os.ReadDir(d.tmp); len(left) != 0 {
		t.Errorf("credentials file left: %v", left)
	}
	if _, err := os.Lstat(dumpPidFile); err == nil {
		t.Error("the pid file is left")
	}
	if procAlive(strconv.Itoa(child)) {
		t.Error("the shell's foreground child still runs")
	}
}

// A signal to the dump shell is forwarded to its dumper, the shell waits for
// (and reaps) it, and its exit removes the credentials file and the pid file.
func TestDumpScriptForwardsTerm(t *testing.T) {
	requireProc(t)
	cleanPidFile(t)
	d := newScriptDirs(t, []string{"mysqldump"})
	// The dumper takes a while to end on TERM; the shell must outwait it.
	cmd := d.command(mysqlDumpScript, []string{"FAKE_SLOW=1", "FAKE_TERM_DELAY=1", "MYSQL_ROOT_PASSWORD=" + hostilePassword}, "shop")
	// Files, not pipes: Wait then returns when the shell exits, not when the
	// last holder of its stdout does.
	out, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_, k := waitDumper(t, "mysqldump")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	var ee *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &ee) || ee.ExitCode() != 1 {
		t.Errorf("exit = %v, want 1", err)
	}
	if _, err := os.Stat("/proc/" + k); err == nil {
		t.Errorf("the dumper outlived its shell (%s)", comm(k))
	}
	if _, err := os.Lstat(dumpPidFile); err == nil {
		t.Error("the pid file is left")
	}
	if left, _ := os.ReadDir(d.tmp); len(left) != 0 {
		t.Errorf("credentials file left: %v", left)
	}
}

// The script's status is the dumper's, and its stdout only the dumper's.
func TestDumpScriptExitStatusIsTheDumpers(t *testing.T) {
	cleanPidFile(t)
	r := runScript(t, pgDumpScript, []string{"psql", "pg_dump"}, []string{"FAKE_EXIT=3"}, "shop")
	if r.code != 3 || r.stdout != "pg_dump output\n" {
		t.Fatalf("exit %d, stdout %q", r.code, r.stdout)
	}
}
