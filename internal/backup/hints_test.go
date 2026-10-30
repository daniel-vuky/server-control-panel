package backup

import (
	"strings"
	"testing"
)

func TestHintTable(t *testing.T) {
	cases := []struct {
		name, typ, stderr string
		sources           []string
		want              string
	}{
		{"pg password", "postgresql", `pg_dump: error: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" failed: FATAL:  password authentication failed for user "shop"`, nil,
			"Check POSTGRES_PASSWORD (and POSTGRES_USER) in the db service's environment."},
		{"pg role", "postgresql", `pg_dump: error: connection failed: FATAL:  role "admin" does not exist`, nil,
			"Check POSTGRES_PASSWORD (and POSTGRES_USER) in the db service's environment."},
		{"pg no password", "postgresql", `pg_dump: error: connection failed: fe_sendauth: no password supplied`, nil,
			"Check POSTGRES_PASSWORD (and POSTGRES_USER) in the db service's environment."},
		{"mysql 1045", "mysql", `mysqldump: Got error: 1045: Access denied for user 'root'@'localhost' (using password: YES) when trying to connect`, nil,
			"Check MYSQL_ROOT_PASSWORD or MYSQL_USER / MYSQL_PASSWORD in the db service's environment."},
		{"mysql 1227", "mysql", `mysqldump: Couldn't execute 'FLUSH TABLES': Access denied; you need (at least one of) the RELOAD or FLUSH_TABLES privilege(s) for this operation (1227)`, nil,
			"The MYSQL_USER account lacks a privilege the dump needs. Set MYSQL_ROOT_PASSWORD on the service, or grant the privilege named above."},
		{"mysql 1044", "mysql", `mysqldump: Got error: 1044: Access denied for user 'crm'@'%' to database 'crm' when selecting the database`, nil,
			"The MYSQL_USER account lacks a privilege the dump needs. Set MYSQL_ROOT_PASSWORD on the service, or grant the privilege named above."},
		{"mongo auth", "mongodb", `2026-10-30T10:00:00.000+0000	Failed: can't create session: connection(): auth error: sasl conversation error: unable to authenticate using mechanism "SCRAM-SHA-256": (AuthenticationFailed) Authentication failed.`, nil,
			"Check MONGO_INITDB_ROOT_USERNAME / MONGO_INITDB_ROOT_PASSWORD in the db service's environment."},
		{"mongo requires", "mongodb", `Failed: error counting crm.users: (Unauthorized) command count requires authentication`, nil,
			"Check MONGO_INITDB_ROOT_USERNAME / MONGO_INITDB_ROOT_PASSWORD in the db service's environment."},
		{"mysql unknown db", "mysql", `mysqldump: Got error: 1049: Unknown database 'shop' when selecting the database`, nil,
			"The database shop was not found in db. Rescan the workspace if it was renamed."},
		{"pg db missing", "postgresql", `pg_dump: error: connection failed: FATAL:  database "shop" does not exist`, nil,
			"The database shop was not found in db. Rescan the workspace if it was renamed."},
		{"tool missing busybox", "postgresql", `sh: pg_dump: not found`, nil,
			"pg_dump is not installed in the db container."},
		{"tool missing bash", "mysql", `sh: line 9: mysqldump: command not found`, nil,
			"mysqldump is not installed in the db container."},
		{"mariadb tool", "mysql", `sh: 1: mariadb-dump: not found`, nil,
			"mariadb-dump is not installed in the db container."},
		{"no shell", "mongodb", `OCI runtime exec failed: exec failed: unable to start container process: exec: "sh": executable file not found in $PATH: unknown`, nil,
			"sh is not installed in the db container."},
		{"case-insensitive", "postgresql", `FATAL: PASSWORD AUTHENTICATION FAILED for user "x"`, nil,
			"Check POSTGRES_PASSWORD (and POSTGRES_USER) in the db service's environment."},
		{"sources suffix", "postgresql", `FATAL:  password authentication failed for user "shop"`,
			[]string{".env DATABASE_URL", "service db", "service web DB_USER DB_PASSWORD"},
			"Check POSTGRES_PASSWORD (and POSTGRES_USER) in the db service's environment (detected from DATABASE_URL, DB_USER, DB_PASSWORD)."},
		{"service-only source adds nothing", "postgresql", `FATAL:  password authentication failed`, []string{"service db"},
			"Check POSTGRES_PASSWORD (and POSTGRES_USER) in the db service's environment."},
		{"another type's words do not match", "postgresql", `Access denied for user 'root'@'x' (using password: YES)`, nil, ""},
		{"unknown error", "postgresql", `pg_dump: error: query failed: server closed the connection unexpectedly`, nil, ""},
		{"a warning is not the error", "mysql", "mysqldump: [Warning] sh: mysqldump: not found\nmysqldump: Error 2013: Lost connection", nil, ""},
	}
	for _, tc := range cases {
		if got := Hint(tc.typ, "shop", "db", tc.stderr, tc.sources); got != tc.want {
			t.Errorf("%s: Hint = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHintNeverHoldsAValue(t *testing.T) {
	// A source string carries only names; anything that is not a plain variable
	// name is dropped, so a value can never reach the hint.
	got := Hint("postgresql", "shop", "db", "password authentication failed",
		[]string{".env DATABASE_URL=postgres://u:hunter22@db/shop", "service web s3cr3t!value"})
	if strings.Contains(got, "hunter22") || strings.Contains(got, "s3cr3t") || strings.Contains(got, "postgres://") {
		t.Fatalf("hint holds a value: %q", got)
	}
}

func TestErrorLineSkipsWarnings(t *testing.T) {
	cases := []struct{ stderr, want string }{
		{"", ""},
		{"\n\n", ""},
		{"pg_dump: error: query failed\n", "pg_dump: error: query failed"},
		{"first\nsecond\n\n", "second"},
		{"mysqldump: Got error: 1045: Access denied\nWarning: A partial dump from a server that has GTIDs will by default include the GTIDs\n", "mysqldump: Got error: 1045: Access denied"},
		{"real error\nmysqldump: [Warning] Using a password on the command line interface can be insecure.\n", "real error"},
		{"real error\nmariadb-dump: [Warning] something\n", "real error"},
		{"pg_dump: error: x\npg_dump: warning: y\n", "pg_dump: error: x"},
		// Only warnings: the last one is still better than nothing.
		{"Warning: only this\n", "Warning: only this"},
		{"  padded  \r\n", "padded"},
	}
	for _, tc := range cases {
		if got := ErrorLine(tc.stderr); got != tc.want {
			t.Errorf("ErrorLine(%q) = %q, want %q", tc.stderr, got, tc.want)
		}
	}
	long := strings.Repeat("é", 400)
	if got := ErrorLine(long); len(got) > maxErrorLine || !strings.HasPrefix(long, got) {
		t.Errorf("long line: %d bytes, want at most %d on a rune boundary", len(got), maxErrorLine)
	}
}
