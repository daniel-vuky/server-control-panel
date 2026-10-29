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
			f.host, f.hasHost = strings.TrimSpace(host), true
			f.kind, f.loc = classifyHost(f.host, hostService, services)
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
		return dbType, sqlitePath(scheme, rest), "", false, true, true
	}
	if !strings.HasPrefix(rest, "//") {
		return "", "", "", false, false, false
	}
	rest = rest[2:]
	// Cut the query and fragment first; a password never holds a raw ? or #.
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	authority, path := rest, ""
	if i := strings.Index(rest, "/"); i >= 0 {
		authority, path = rest[:i], rest[i+1:]
	}
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		authority = authority[i+1:]
	}
	if i := strings.Index(authority, ","); i >= 0 { // replica set: first host
		authority = authority[:i]
	}
	host = hostOnly(authority)
	return dbType, path, host, host != "", false, true
}

// hostOnly strips the port from host or host:port, and the brackets from an
// IPv6 literal.
func hostOnly(authority string) string {
	if strings.HasPrefix(authority, "[") {
		if i := strings.Index(authority, "]"); i > 0 {
			return authority[1:i]
		}
	}
	if i := strings.LastIndex(authority, ":"); i >= 0 {
		return authority[:i]
	}
	return authority
}

// sqlitePath returns the file path of a sqlite: or file: URL: "file:./a.db"
// gives "./a.db", "sqlite:///data/a.db" gives "data/a.db" and
// "sqlite:////var/a.db" gives "/var/a.db".
func sqlitePath(scheme, rest string) string {
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	if strings.HasPrefix(rest, "//") {
		rest = rest[2:]
		if scheme == "sqlite" {
			rest = strings.TrimPrefix(rest, "/")
		}
	}
	return rest
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
