package scan

import (
	"sort"
	"strings"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

// Database types and location kinds, as the API reports them.
const (
	dbPostgres = "postgresql"
	dbMySQL    = "mysql"
	dbMongo    = "mongodb"
	dbSQLite   = "sqlite"

	locContainer = "container"
	locHost      = "host"
	locExternal  = "external"
	locFile      = "file"
	locUnknown   = "unknown"
)

// imageTypes maps the last path segment of an image, without tag or digest,
// to a database type. "mongo-express" and "postgres-exporter" are not here.
var imageTypes = map[string]string{
	"postgres":     dbPostgres,
	"postgresql":   dbPostgres,
	"mysql":        dbMySQL,
	"mysql-server": dbMySQL,
	"mariadb":      dbMySQL,
	"mongo":        dbMongo,
	"mongodb":      dbMongo,
}

// serviceNameVars are the variables that name the database a database
// container creates, in order of preference, per type.
var serviceNameVars = map[string][]string{
	dbPostgres: {"POSTGRES_DB"},
	dbMySQL:    {"MYSQL_DATABASE", "MARIADB_DATABASE"},
	dbMongo:    {"MONGO_INITDB_DATABASE"},
}

// urlVars are the variables that hold a connection URL.
var urlVars = []string{"DATABASE_URL", "MONGO_URI"}

// urlSchemes maps a URL scheme to a database type; other schemes are ignored.
var urlSchemes = map[string]string{
	"postgres":    dbPostgres,
	"postgresql":  dbPostgres,
	"mysql":       dbMySQL,
	"mariadb":     dbMySQL,
	"mongodb":     dbMongo,
	"mongodb+srv": dbMongo,
	"sqlite":      dbSQLite,
	"file":        dbSQLite,
}

// prefixGroup is a family of variables, such as POSTGRES_*, that describes
// one database. Only the name and host variables are read, and only their
// names are kept.
type prefixGroup struct {
	dbType  string
	nameVar string
	hostVar string
}

var prefixGroups = []prefixGroup{
	{dbType: dbPostgres, nameVar: "POSTGRES_DB", hostVar: "POSTGRES_HOST"},
	{dbType: dbMySQL, nameVar: "MYSQL_DATABASE", hostVar: "MYSQL_HOST"},
}

// finding is a database found in one variable source before merging. hasHost
// is false when the source gives no host (a group with no host variable, or
// a URL without one).
type finding struct {
	dbType  string
	name    string
	kind    string
	loc     string
	host    string // raw host, for matching a compose service
	hasHost bool
	source  string
}

// DetectDatabases finds the databases a project uses from its compose config
// and its parsed .env. It keeps only the type, name, location and the names of
// the variables or services it read; no value, user or password is returned.
// The result is sorted by (Type, Location, Name) and never nil.
func DetectDatabases(cfg ComposeConfig, dotenv map[string]string) []project.Database {
	services := append([]ComposeService(nil), cfg.Services...)
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

	// Compose database services come first; they are the merge targets.
	var records, svcRecords []*project.Database
	svcRecord := map[string]*project.Database{} // service name -> its record
	explicit := map[*project.Database]bool{}    // the service names its database in its own environment
	for _, s := range services {
		t, ok := imageTypes[imageBase(s.Image)]
		if !ok {
			continue
		}
		rec := &project.Database{
			Type:         t,
			Name:         s.Name,
			LocationKind: locContainer,
			Location:     serviceLocation(s),
			Sources:      []string{"service " + s.Name},
		}
		for _, v := range serviceNameVars[t] {
			if n := strings.TrimSpace(s.Environment[v]); n != "" {
				rec.Name = n
				explicit[rec] = true
				break
			}
		}
		records = append(records, rec)
		svcRecords = append(svcRecords, rec)
		svcRecord[s.Name] = rec
	}

	// Host names a finding can use to point at a service.
	hostService := map[string]string{} // lower-case host -> service name
	for _, s := range services {
		hostService[strings.ToLower(s.Name)] = s.Name
	}
	for _, s := range services {
		if s.ContainerName != "" {
			hostService[strings.ToLower(s.ContainerName)] = s.Name
		}
	}

	var findings []finding
	collect := func(env map[string]string, origin string) {
		findings = append(findings, envFindings(env, origin, hostService, services)...)
	}
	collect(dotenv, ".env")
	for _, s := range services {
		collect(s.Environment, "service "+s.Name)
	}

	for _, f := range findings {
		if rec := mergeTarget(f, svcRecords, svcRecord, hostService); rec != nil {
			rec.Sources = append(rec.Sources, f.source)
			// A name from a URL or a variable group replaces only the
			// service-name placeholder, never an explicit *_DB / *_DATABASE.
			if !explicit[rec] && f.name != "" {
				rec.Name = f.name
				explicit[rec] = true
			}
			continue
		}
		var same *project.Database
		for _, rec := range records {
			if rec.Type == f.dbType && rec.Location == f.loc && rec.Name == f.name {
				same = rec
				break
			}
		}
		if same != nil {
			same.Sources = append(same.Sources, f.source)
			continue
		}
		records = append(records, &project.Database{
			Type:         f.dbType,
			Name:         f.name,
			LocationKind: f.kind,
			Location:     f.loc,
			Sources:      []string{f.source},
		})
	}

	out := make([]project.Database, 0, len(records))
	for _, rec := range records {
		rec.Sources = sortedUnique(rec.Sources)
		out = append(out, *rec)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Location != b.Location {
			return a.Location < b.Location
		}
		return a.Name < b.Name
	})
	return out
}

// mergeTarget returns the compose database record a finding belongs to: one of
// the same type that the finding points at, or, when the finding has no host,
// the first one of that type. It returns nil when there is none.
func mergeTarget(f finding, svcRecords []*project.Database, svcRecord map[string]*project.Database, hostService map[string]string) *project.Database {
	if f.hasHost {
		svc, ok := hostService[strings.ToLower(f.host)]
		if !ok {
			return nil
		}
		if rec := svcRecord[svc]; rec != nil && rec.Type == f.dbType {
			return rec
		}
		return nil
	}
	for _, rec := range svcRecords {
		if rec.Type == f.dbType {
			return rec
		}
	}
	return nil
}

// envFindings reads the variables of one source (.env or a service's
// environment) and returns the databases they describe.
func envFindings(env map[string]string, origin string, hostService map[string]string, services []ComposeService) []finding {
	var out []finding
	for _, key := range urlVars {
		raw, ok := env[key]
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		t, name, host, hasHost, isFile, ok := parseDatabaseURL(raw)
		if !ok {
			continue
		}
		f := finding{dbType: t, name: name, source: origin + " " + key}
		switch {
		case isFile:
			f.kind, f.loc = locFile, name
		case !hasHost:
			f.kind = locUnknown
		default:
			f.host, f.hasHost = host, true
			f.kind, f.loc = classifyHost(host, hostService, services)
		}
		out = append(out, f)
	}
	for _, g := range prefixGroups {
		var names []string
		name, hasName := env[g.nameVar]
		host, hasHost := env[g.hostVar]
		hasHost = hasHost && strings.TrimSpace(host) != ""
		if !hasName && !hasHost {
			continue
		}
		if hasName {
			names = append(names, g.nameVar)
		}
		if hasHost {
			names = append(names, g.hostVar)
		}
		f := finding{
			dbType: g.dbType,
			name:   strings.TrimSpace(name),
			source: origin + " " + strings.Join(names, " "),
			kind:   locUnknown,
		}
		if hasHost {
			// The value is used only when it is a clean host[:port]; anything
			// else (user:pass@host, a URL) may hold a credential, so the finding
			// keeps no location and matches no service.
			f.hasHost = true
			if h, clean := parseHostPort(strings.TrimSpace(host)); clean && h != "" {
				f.host = h
				f.kind, f.loc = classifyHost(h, hostService, services)
			}
		}
		out = append(out, f)
	}
	return out
}

// classifyHost applies the host rule: a compose service is a container, a
// local address is the host, anything else is external.
func classifyHost(host string, hostService map[string]string, services []ComposeService) (kind, loc string) {
	if svc, ok := hostService[strings.ToLower(host)]; ok {
		for _, s := range services {
			if s.Name == svc {
				return locContainer, serviceLocation(s)
			}
		}
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "host.docker.internal":
		return locHost, ""
	}
	return locExternal, host
}

func serviceLocation(s ComposeService) string {
	if s.ContainerName != "" {
		return s.ContainerName
	}
	return s.Name
}

// imageBase returns the last path segment of an image reference, without tag
// or digest, in lower case: "docker.io/library/mongo@sha256:..." gives "mongo".
func imageBase(image string) string {
	if i := strings.LastIndex(image, "/"); i >= 0 {
		image = image[i+1:]
	}
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.Index(image, ":"); i >= 0 {
		image = image[:i]
	}
	return strings.ToLower(strings.TrimSpace(image))
}

// parseDatabaseURL reads the scheme, host and path of a connection URL by
// hand, so a password with characters that url.Parse rejects cannot make it
// fail. It returns nothing that holds a user or password. isFile is true for
// SQLite; then name is the file path and host is unused.
func parseDatabaseURL(raw string) (dbType, name, host string, hasHost, isFile, ok bool) {
	raw = strings.TrimSpace(raw)
	colon := strings.Index(raw, ":")
	if colon <= 0 {
		return "", "", "", false, false, false
	}
	scheme := strings.ToLower(raw[:colon])
	dbType, known := urlSchemes[scheme]
	if !known {
		return "", "", "", false, false, false
	}
	rest := raw[colon+1:]
	if dbType == dbSQLite {
		path, ok := sqlitePath(scheme, rest)
		if !ok {
			return "", "", "", false, false, false
		}
		return dbType, path, "", false, true, true
	}
	if !strings.HasPrefix(rest, "//") {
		return "", "", "", false, false, false
	}
	rest = rest[2:]
	host, path, ok := splitURLRest(rest)
	if !ok {
		return "", "", "", false, false, false
	}
	return dbType, path, host, host != "", false, true
}

// splitURLRest reads what follows "scheme://" into a host and a database path,
// by one strict rule that refuses anything ambiguous. A credential may hold an
// unencoded / ? # or @, and libpq also takes `password` as a query parameter,
// so no guess is safe; missing a database is acceptable, leaking a user or
// password fragment is not.
//
//   - The authority is the text up to the first /, ? or #.
//   - With no @ in the whole text there is no userinfo.
//   - With exactly one @, and it inside the authority, the userinfo is
//     everything up to it and is dropped unread.
//   - Any other @ (a second one, or one after the authority) refuses the URL.
//   - The rest of the authority must be a clean host[:port] (each host, for a
//     replica set); the path is what follows the authority's /, up to ? or #.
func splitURLRest(rest string) (host, path string, ok bool) {
	end := len(rest)
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		end = i
	}
	authority := rest[:end]
	switch strings.Count(rest, "@") {
	case 0:
	case 1:
		at := strings.Index(rest, "@")
		if at >= end {
			return "", "", false
		}
		authority = authority[at+1:]
	default:
		return "", "", false
	}
	if end < len(rest) && rest[end] == '/' {
		path = rest[end+1:]
		if j := strings.IndexAny(path, "?#"); j >= 0 {
			path = path[:j]
		}
	}
	for i, h := range strings.Split(authority, ",") { // replica set: first host names it
		hostName, clean := parseHostPort(h)
		if !clean {
			return "", "", false
		}
		if i == 0 {
			host = hostName
		}
	}
	return host, path, true
}

// parseHostPort splits host[:port] or [ipv6][:port]. It reports false unless
// the host holds only name characters and the port, when present, is digits.
func parseHostPort(authority string) (host string, ok bool) {
	port := ""
	switch {
	case strings.HasPrefix(authority, "["):
		end := strings.Index(authority, "]")
		if end < 0 {
			return "", false
		}
		host = authority[1:end]
		tail := authority[end+1:]
		if tail != "" {
			if !strings.HasPrefix(tail, ":") {
				return "", false
			}
			port = tail[1:]
		}
		for _, r := range host {
			if r != ':' && r != '.' && r != '%' && !isHexOrLetterDigit(r) {
				return "", false
			}
		}
	default:
		host = authority
		if i := strings.LastIndex(authority, ":"); i >= 0 {
			host, port = authority[:i], authority[i+1:]
		}
		for _, r := range host {
			if r != '.' && r != '-' && r != '_' && r != '%' && !isHexOrLetterDigit(r) {
				return "", false
			}
		}
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return host, true
}

func isHexOrLetterDigit(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// sqlitePath returns the file path of a sqlite: or file: URL: "file:./a.db"
// gives "./a.db", "sqlite:///data/a.db" gives "data/a.db" and
// "sqlite:////var/a.db" gives "/var/a.db". In the // form the authority must
// be empty or "localhost"; anything else could be a user and password
// ("sqlite://user:pass@host/x.db"), so the URL is refused. Any @ or :// in
// any form ("sqlite:user:pass@host/x.db", "file:u:p?w@h/x") refuses it too,
// since the path would otherwise carry the credential.
func sqlitePath(scheme, rest string) (string, bool) {
	if strings.Contains(rest, "@") || strings.Contains(rest, "://") {
		return "", false
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	if strings.HasPrefix(rest, "//") {
		rest = rest[2:]
		authority := rest
		if i := strings.Index(rest, "/"); i >= 0 {
			authority = rest[:i]
		}
		if authority != "" && !strings.EqualFold(authority, "localhost") {
			return "", false
		}
		rest = rest[len(authority):]
		if scheme == "sqlite" {
			rest = strings.TrimPrefix(rest, "/")
		}
	}
	return rest, true
}

func sortedUnique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
