package backup

import (
	"regexp"
	"strings"
	"unicode/utf8"
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

// defaultTool is the dump tool of a database type.
func defaultTool(dbType string) string {
	switch dbType {
	case "postgresql":
		return "pg_dump"
	case "mysql":
		return "mysqldump"
	case "mongodb":
		return "mongodump"
	}
	return dbType
}

// Hint gives the fix for a failed dump from its masked stderr (spec §4),
// matched case-insensitively on the lines that are not warnings, or "" when
// nothing matches. When the database was detected from variables (its
// sources), the hint names them: "(detected from DATABASE_URL)". It never holds
// a value: only the database and service names and variable names.
func Hint(dbType, dbName, service, stderr string, sources []string) string {
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
	hint := ""
	switch {
	case dbType == "postgresql" && (has("password authentication failed", "no password supplied") || pgRoleMissing.MatchString(text)):
		hint = "Check POSTGRES_PASSWORD (and POSTGRES_USER) in the " + service + " service's environment."
	case dbType == "mysql" && has("access denied for user") && has("(using password"):
		hint = "Check MYSQL_ROOT_PASSWORD or MYSQL_USER / MYSQL_PASSWORD in the " + service + " service's environment."
	case dbType == "mysql" && (has("access denied; you need") || (has("access denied for user") && has(" to database "))):
		hint = "The MYSQL_USER account lacks a privilege the dump needs. Set MYSQL_ROOT_PASSWORD on the service, or grant the privilege named above."
	case dbType == "mongodb" && has("authentication failed", "requires authentication"):
		hint = "Check MONGO_INITDB_ROOT_USERNAME / MONGO_INITDB_ROOT_PASSWORD in the " + service + " service's environment."
	case has("unknown database") || (has("database") && has("does not exist")):
		hint = "The database " + dbName + " was not found in " + service + ". Rescan the workspace if it was renamed."
	default:
		if tool := missingTool(lines, dbType); tool != "" {
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
// tool the line names, "sh" when the container has no shell, else "".
func missingTool(lines []string, dbType string) string {
	for _, line := range lines {
		l := strings.ToLower(line)
		if strings.Contains(l, "executable file not found") {
			if strings.Contains(l, `"sh"`) {
				return "sh"
			}
			return defaultTool(dbType)
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
