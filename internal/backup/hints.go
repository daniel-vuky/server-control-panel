package backup

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/daniel-vuky/server-control-panel/internal/scan"
)

// maxErrorLine caps the error line a failed dump stores.
const maxErrorLine = 500

// isWarning reports whether a stderr line is only a warning: "Warning: …",
// "mysqldump: [Warning] …" (mysqldump prints a GTID warning on every
// successful partial dump) or "pg_dump: warning: …".
func isWarning(line string) bool {
	l := strings.ToLower(line)
	return strings.HasPrefix(l, "warning:") || strings.Contains(l, ": [warning]") || strings.Contains(l, ": warning:")
}

// meaningful returns stderr's non-empty lines that are not warnings, trimmed.
func meaningful(stderr string) []string {
	var out []string
	for _, line := range strings.Split(stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" && !isWarning(line) {
			out = append(out, line)
		}
	}
	return out
}

// ErrorLine is the last meaningful line of a dump's stderr: the last non-empty
// line that is not a warning, else the last non-empty line, cut to 500 bytes.
func ErrorLine(stderr string) string {
	line := ""
	if lines := meaningful(stderr); len(lines) > 0 {
		line = lines[len(lines)-1]
	} else {
		for _, l := range strings.Split(stderr, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				line = l
			}
		}
	}
	return cut(line, maxErrorLine)
}

// cut shortens s to at most n bytes on a rune boundary.
func cut(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// pgRoleMissing is PostgreSQL's `role "…" does not exist`.
var pgRoleMissing = regexp.MustCompile(`role ".*" does not exist`)

// The tools a "not found" line may name, longest first so that mariadb-dump is
// not read as mariadb.
var tools = []string{"mariadb-dump", "mysqldump", "pg_dump", "mongodump"}

// MySQL-type families, from the service's image.
const (
	familyMySQL   = "mysql"
	familyMariaDB = "mariadb"
)

// mysqlFamily says which family a MySQL-type service's image is: mariadb,
// mysql, or "" when the image does not tell (a custom or unknown image).
func mysqlFamily(image string) string {
	switch scan.ImageBase(image) {
	case "mariadb":
		return familyMariaDB
	case "mysql", "mysql-server":
		return familyMySQL
	}
	return ""
}

// dumpTool is the dump tool of a database type: mariadb-dump for a mariadb
// image (the script prefers it there), else mysqldump for MySQL.
func dumpTool(dbType, image string) string {
	switch dbType {
	case "postgresql":
		return "pg_dump"
	case "mysql":
		if mysqlFamily(image) == familyMariaDB {
			return "mariadb-dump"
		}
		return "mysqldump"
	case "mongodb":
		return "mongodump"
	}
	return dbType
}

// mysqlVar names a MySQL image variable for the service's family: MYSQL_<v>,
// MARIADB_<v>, or both when the image does not tell.
func mysqlVar(family, v string) string {
	switch family {
	case familyMariaDB:
		return "MARIADB_" + v
	case familyMySQL:
		return "MYSQL_" + v
	}
	return "MYSQL_" + v + " (or MARIADB_" + v + ")"
}

// Hint gives the fix for a failed dump from its masked stderr (spec §4),
// matched case-insensitively on the lines that are not warnings, or "" when
// nothing matches. image is the service's image: it picks the MySQL or MariaDB
// words. When the database was detected from variables (its sources), the hint
// names them: "(detected from DATABASE_URL)". It never holds a value: only the
// database and service names and variable names.
func Hint(dbType, image, dbName, service, stderr string, sources []string) string {
	lines := meaningful(stderr)
	text := strings.ToLower(strings.Join(lines, "\n"))
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(text, s) {
				return true
			}
		}
		return false
	}
	// onOneLine reports whether a single line holds every one of subs.
	onOneLine := func(subs ...string) bool {
		for _, line := range lines {
			l, all := strings.ToLower(line), true
			for _, s := range subs {
				all = all && strings.Contains(l, s)
			}
			if all {
				return true
			}
		}
		return false
	}
	family := mysqlFamily(image)
	hint := ""
	switch {
	case dbType == "postgresql" && (has("password authentication failed", "no password supplied") || pgRoleMissing.MatchString(text)):
		hint = "Check POSTGRES_PASSWORD (and POSTGRES_USER) in the " + service + " service's environment."
	case dbType == "mysql" && has("access denied for user") && has("(using password"):
		vars := "MYSQL_ROOT_PASSWORD or MYSQL_USER / MYSQL_PASSWORD"
		switch family {
		case familyMariaDB:
			vars = "MARIADB_ROOT_PASSWORD or MARIADB_USER / MARIADB_PASSWORD"
		case "":
			vars = "MYSQL_ROOT_PASSWORD (or MARIADB_ROOT_PASSWORD) or MYSQL_USER / MYSQL_PASSWORD (or MARIADB_USER / MARIADB_PASSWORD)"
		}
		hint = "Check " + vars + " in the " + service + " service's environment."
	case dbType == "mysql" && (has("access denied; you need") || (has("access denied for user") && has(" to database "))):
		hint = "The " + mysqlVar(family, "USER") + " account lacks a privilege the dump needs. Set " + mysqlVar(family, "ROOT_PASSWORD") + " on the service, or grant the privilege named above."
	case dbType == "mongodb" && has("authentication failed", "requires authentication"):
		hint = "Check MONGO_INITDB_ROOT_USERNAME / MONGO_INITDB_ROOT_PASSWORD in the " + service + " service's environment."
	case has("unknown database") || onOneLine("database", "does not exist"):
		hint = "The database " + dbName + " was not found in " + service + ". Rescan the workspace if it was renamed."
	default:
		if tool := missingTool(lines, dbType, image); tool != "" {
			hint = tool + " is not installed in the " + service + " container."
		}
	}
	if hint == "" {
		return ""
	}
	if vars := sourceVariables(sources); len(vars) > 0 {
		hint = strings.TrimSuffix(hint, ".") + " (detected from " + strings.Join(vars, ", ") + ")."
	}
	return hint
}

// missingTool returns the program a "not found" line says is missing: a dump
// tool the line names, "sh" when the container has no shell, the image's dump
// tool for another program the exec could not find, else "".
func missingTool(lines []string, dbType, image string) string {
	for _, line := range lines {
		l := strings.ToLower(line)
		if strings.Contains(l, "executable file not found") {
			if strings.Contains(l, `"sh"`) {
				return "sh"
			}
			return dumpTool(dbType, image)
		}
		if !strings.Contains(l, "not found") {
			continue
		}
		for _, t := range tools {
			if strings.Contains(l, t) {
				return t
			}
		}
	}
	return ""
}

// variableName is a plain environment variable name.
var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// sourceVariables lists the variable names in a database's sources
// (".env DATABASE_URL", "service web DB_USER DB_PASSWORD"), in order and
// without repeats. The bare "service db" source names none. Anything that is
// not a plain variable name is dropped, so a value can never pass.
func sourceVariables(sources []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range sources {
		f := strings.Fields(s)
		var names []string
		switch {
		case len(f) > 1 && f[0] == ".env":
			names = f[1:]
		case len(f) > 2 && f[0] == "service":
			names = f[2:]
		}
		for _, n := range names {
			if variableName.MatchString(n) && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}
