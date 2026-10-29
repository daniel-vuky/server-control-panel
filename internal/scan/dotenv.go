package scan

import (
	"net/url"
	"sort"
	"strings"
)

// minSecretLen is the shortest value masked, so masking never shreds common
// text.
const minSecretLen = 4

var secretKeyWords = []string{
	"PASS", "PWD", "SECRET", "TOKEN", "KEY", "CREDENTIAL", "AUTH", "PRIVATE", "URL", "URI", "DSN",
}

// ParseDotenv parses KEY=VALUE lines: an optional `export ` prefix, single or
// double quotes, and `#` comments (a whole line, or after whitespace in an
// unquoted value). Lines that are not a pair are skipped. Nothing is
// interpolated.
func ParseDotenv(b []byte) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			continue
		}
		env[key] = parseDotenvValue(strings.TrimSpace(val))
	}
	return env
}

func parseDotenvValue(v string) string {
	if v == "" {
		return ""
	}
	switch q := v[0]; q {
	case '\'', '"':
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			inner := v[1 : 1+end]
			if q == '"' {
				inner = strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(inner)
			}
			return inner
		}
		// An unclosed quote is kept as written.
		return v
	}
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return strings.TrimSpace(v[:i])
		}
	}
	return v
}

// SecretValues returns the values to mask for a call: every value whose key
// contains a secret key word (case-insensitive), plus the user and password of
// any URL-shaped value. Values shorter than 4 characters are dropped and the
// result has no duplicates.
func SecretValues(env map[string]string) []string {
	set := map[string]struct{}{}
	add := func(v string) {
		if len(v) >= minSecretLen {
			set[v] = struct{}{}
		}
	}
	for k, v := range env {
		if isSecretKey(k) {
			add(v)
		}
		if u, err := url.Parse(v); err == nil && u.User != nil {
			add(u.User.Username())
			if pw, ok := u.User.Password(); ok {
				add(pw)
			}
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func isSecretKey(k string) bool {
	k = strings.ToUpper(k)
	for _, w := range secretKeyWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}
