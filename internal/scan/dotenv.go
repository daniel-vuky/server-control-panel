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

// dquoteUnescaper undoes the escapes of a double-quoted value.
var dquoteUnescaper = strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`)

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
	switch v[0] {
	case '\'':
		if end := strings.IndexByte(v[1:], '\''); end >= 0 {
			return v[1 : 1+end]
		}
		// An unclosed quote is kept as written.
		return v
	case '"':
		// The closing quote is the first one not escaped by a backslash, so
		// PASSWORD="ab\"cd" is ab"cd, not ab\.
		for i := 1; i < len(v); i++ {
			switch v[i] {
			case '\\':
				i++ // skip the escaped character
			case '"':
				return dquoteUnescaper.Replace(v[1:i])
			}
		}
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
// any URL-shaped value, both decoded and exactly as written (p%40ss and p@ss).
// Values shorter than 4 characters are dropped and the result has no
// duplicates.
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
		for _, info := range rawUserinfo(v) {
			user, pass, _ := strings.Cut(info, ":")
			add(user)
			add(pass)
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// rawUserinfo returns the userinfo of a URL-shaped value exactly as written,
// undecoded: the text between the first // and the last @ of the authority
// (which ends at the first /, ? or #). A password may hold an unencoded / ? or
// #, so the text up to the last @ of the whole value is returned too when that
// @ lies further on. Masking more than needed is harmless; missing a secret is
// not.
func rawUserinfo(v string) []string {
	i := strings.Index(v, "//")
	if i < 0 {
		return nil
	}
	rest := v[i+2:]
	end := len(rest)
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		end = j
	}
	var out []string
	strict := strings.LastIndex(rest[:end], "@")
	if strict >= 0 {
		out = append(out, rest[:strict])
	}
	if loose := strings.LastIndex(rest, "@"); loose > strict {
		out = append(out, rest[:loose])
	}
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
