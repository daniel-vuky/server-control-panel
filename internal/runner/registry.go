package runner

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Stack is the kind of thing a command works on. v1 manages docker compose
// projects only, and reads the daemon's containers.
type Stack string

// The stacks the registry has commands for.
const (
	StackCompose Stack = "compose"
	StackDocker  Stack = "docker"
)

// Action names one whitelisted command.
type Action string

// The whitelisted actions.
const (
	ComposeConfig         Action = "compose.config"
	ComposeConfigOverride Action = "compose.config.override"
	DockerContainers      Action = "docker.containers"

	ComposeUp              Action = "compose.up"
	ComposeUpOverride      Action = "compose.up.override"
	ComposeStop            Action = "compose.stop"
	ComposeStopOverride    Action = "compose.stop.override"
	ComposeRestart         Action = "compose.restart"
	ComposeRestartOverride Action = "compose.restart.override"
	DockerPing             Action = "docker.ping"

	DockerInspect       Action = "docker.inspect"
	DockerStats         Action = "docker.stats"
	ComposeDown         Action = "compose.down"
	ComposeDownOverride Action = "compose.down.override"

	// The compose.logs actions that follow run only through Stream.
	ComposeLogs                Action = "compose.logs"
	ComposeLogsOverride        Action = "compose.logs.override"
	ComposeLogsService         Action = "compose.logs.service"
	ComposeLogsServiceOverride Action = "compose.logs.service.override"
	// The compose.logs.tail actions read a backlog and end; they run through Run.
	ComposeLogsTail                Action = "compose.logs.tail"
	ComposeLogsTailOverride        Action = "compose.logs.tail.override"
	ComposeLogsTailService         Action = "compose.logs.tail.service"
	ComposeLogsTailServiceOverride Action = "compose.logs.tail.service.override"

	// The database actions run a fixed script inside the database's container
	// (dbscripts.go, ADR 0011). The dumps run only through RunTo; the sizes are
	// read commands and run through Run. The Default variants take no {dbname}
	// and use the image's default database.
	DBPgDump                Action = "db.pg.dump"
	DBPgDumpOverride        Action = "db.pg.dump.override"
	DBPgDumpDefault         Action = "db.pg.dump.default"
	DBPgDumpDefaultOverride Action = "db.pg.dump.default.override"
	DBPgSize                Action = "db.pg.size"
	DBPgSizeOverride        Action = "db.pg.size.override"
	DBPgSizeDefault         Action = "db.pg.size.default"
	DBPgSizeDefaultOverride Action = "db.pg.size.default.override"
	DBMySQLDump             Action = "db.mysql.dump"
	DBMySQLDumpOverride     Action = "db.mysql.dump.override"
	DBMySQLSize             Action = "db.mysql.size"
	DBMySQLSizeOverride     Action = "db.mysql.size.override"
	DBMongoDump             Action = "db.mongo.dump"
	DBMongoDumpOverride     Action = "db.mongo.dump.override"
	DBMongoSize             Action = "db.mongo.size"
	DBMongoSizeOverride     Action = "db.mongo.size.override"
)

// Values are the arguments of one call, keyed by placeholder name without
// braces: "dir" for {dir}, "file" for {file}, "override" for {override},
// "service" for {service}, "since" for {since}, "dbname" for {dbname}. "ids"
// holds container ids joined by single spaces; argv expands it into one element
// per id.
type Values map[string]string

// Labels that docker compose and the panel put on containers.
const (
	labelProject = "com.docker.compose.project"
	labelService = "com.docker.compose.service"
	labelOneOff  = "com.docker.compose.oneoff"
	// labelImage marks the panel's own image.
	labelImage = "io.github.daniel-vuky.server-control-panel.image"
)

// ContainerColumns is the column order of DockerContainers output, one
// tab-separated line per container. Parsers refer to the columns by these names.
const ContainerColumns = "project,service,state,status,image,oneoff,id,name,imageref,ports"

// containersFormat is the --format template of DockerContainers: one fixed
// argv element, built from the label names and never from input. .Labels is
// not used because docker ps joins it with commas and a label value can
// contain one.
var containersFormat = strings.Join([]string{
	label(labelProject),
	label(labelService),
	"{{.State}}",
	"{{.Status}}",
	label(labelImage),
	label(labelOneOff),
	"{{.ID}}",
	"{{.Names}}",
	"{{.Image}}",
	"{{.Ports}}",
}, "\t")

func label(name string) string {
	return `{{.Label "` + name + `"}}`
}

// slotKind says how a placeholder's value is checked.
type slotKind int

const (
	kindPath   slotKind = iota // an absolute, clean path
	kindIDs                    // 1 to maxIDs full container ids, space-joined, one element each
	kindName                   // a service name
	kindSince                  // an RFC 3339 UTC timestamp
	kindDBName                 // a database name (ValidDBName)
)

// ValidDBName reports whether name may reach a database dump script as its
// database name: a whole argument, never a flag.
func ValidDBName(name string) bool { return dbNamePattern.MatchString(name) }

// maxIDs is the most container ids one call may carry.
const maxIDs = 1000

var (
	idPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	namePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	dbNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.$-]{0,63}$`)
	// sincePattern is RFC 3339 in UTC with an optional fraction, which docker
	// compose logs --since accepts.
	sincePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z$`)
)

// arg is one argv element: a fixed literal, or a placeholder that is replaced,
// whole, by one value.
type arg struct {
	literal string
	slot    string
	kind    slotKind
}

func lit(elements ...string) []arg {
	args := make([]arg, len(elements))
	for i, e := range elements {
		args[i] = arg{literal: e}
	}
	return args
}

// pathArg is a placeholder whose value must be an absolute, clean path.
func pathArg(slot string) arg { return arg{slot: slot} }

// idsArg is a placeholder that becomes one argv element per container id.
func idsArg() arg { return arg{slot: "ids", kind: kindIDs} }

// nameArg is a placeholder whose value must be a service name.
func nameArg(slot string) arg { return arg{slot: slot, kind: kindName} }

// sinceArg is a placeholder whose value must be a UTC RFC 3339 timestamp.
func sinceArg(slot string) arg { return arg{slot: slot, kind: kindSince} }

// dbNameArg is the {dbname} placeholder, a database name that ValidDBName accepts.
func dbNameArg() arg { return arg{slot: "dbname", kind: kindDBName} }

func join(parts ...[]arg) []arg {
	var all []arg
	for _, p := range parts {
		all = append(all, p...)
	}
	return all
}

type key struct {
	stack  Stack
	action Action
}

// registry is the whole list of commands the panel can run, as argv templates
// for the docker binary. There is no way to add an entry at run time.
var registry = map[key][]arg{
	{StackCompose, ComposeConfig}: join(
		lit("compose", "--project-directory"), []arg{pathArg("dir")},
		lit("-f"), []arg{pathArg("file")},
		lit("config", "--format", "json", "--no-env-resolution"),
	),
	{StackCompose, ComposeConfigOverride}: join(
		lit("compose", "--project-directory"), []arg{pathArg("dir")},
		lit("-f"), []arg{pathArg("file")},
		lit("-f"), []arg{pathArg("override")},
		lit("config", "--format", "json", "--no-env-resolution"),
	),
	{StackCompose, ComposeUp}:              join(composeBase, lit("up", "-d")),
	{StackCompose, ComposeUpOverride}:      join(composeBaseOverride, lit("up", "-d")),
	{StackCompose, ComposeStop}:            join(composeBase, lit("stop")),
	{StackCompose, ComposeStopOverride}:    join(composeBaseOverride, lit("stop")),
	{StackCompose, ComposeRestart}:         join(composeBase, lit("restart")),
	{StackCompose, ComposeRestartOverride}: join(composeBaseOverride, lit("restart")),
	{StackDocker, DockerPing}:              lit("version", "--format", "{{.Server.Version}}"),
	{StackDocker, DockerContainers}: lit(
		"ps", "--all", "--no-trunc",
		"--filter", "label="+labelProject,
		"--format", containersFormat,
	),
	{StackDocker, DockerInspect}: join(
		lit("inspect", "--format", inspectFormat), []arg{idsArg()},
	),
	{StackDocker, DockerStats}: lit(
		"stats", "--no-stream", "--no-trunc",
		"--format", statsFormat,
	),
	{StackCompose, ComposeDown}:         join(composeBase, lit("down")),
	{StackCompose, ComposeDownOverride}: join(composeBaseOverride, lit("down")),

	{StackCompose, ComposeLogs}:                join(composeBase, followLogs),
	{StackCompose, ComposeLogsOverride}:        join(composeBaseOverride, followLogs),
	{StackCompose, ComposeLogsService}:         join(composeBase, followLogs, []arg{nameArg("service")}),
	{StackCompose, ComposeLogsServiceOverride}: join(composeBaseOverride, followLogs, []arg{nameArg("service")}),

	{StackCompose, ComposeLogsTail}:                join(composeBase, tailLogs),
	{StackCompose, ComposeLogsTailOverride}:        join(composeBaseOverride, tailLogs),
	{StackCompose, ComposeLogsTailService}:         join(composeBase, tailLogs, []arg{nameArg("service")}),
	{StackCompose, ComposeLogsTailServiceOverride}: join(composeBaseOverride, tailLogs, []arg{nameArg("service")}),

	{StackCompose, DBPgDump}:                dbScript(composeBase, pgDumpScript, true),
	{StackCompose, DBPgDumpOverride}:        dbScript(composeBaseOverride, pgDumpScript, true),
	{StackCompose, DBPgDumpDefault}:         dbScript(composeBase, pgDumpScript, false),
	{StackCompose, DBPgDumpDefaultOverride}: dbScript(composeBaseOverride, pgDumpScript, false),
	{StackCompose, DBPgSize}:                dbScript(composeBase, pgSizeScript, true),
	{StackCompose, DBPgSizeOverride}:        dbScript(composeBaseOverride, pgSizeScript, true),
	{StackCompose, DBPgSizeDefault}:         dbScript(composeBase, pgSizeScript, false),
	{StackCompose, DBPgSizeDefaultOverride}: dbScript(composeBaseOverride, pgSizeScript, false),
	{StackCompose, DBMySQLDump}:             dbScript(composeBase, mysqlDumpScript, true),
	{StackCompose, DBMySQLDumpOverride}:     dbScript(composeBaseOverride, mysqlDumpScript, true),
	{StackCompose, DBMySQLSize}:             dbScript(composeBase, mysqlSizeScript, true),
	{StackCompose, DBMySQLSizeOverride}:     dbScript(composeBaseOverride, mysqlSizeScript, true),
	{StackCompose, DBMongoDump}:             dbScript(composeBase, mongoDumpScript, true),
	{StackCompose, DBMongoDumpOverride}:     dbScript(composeBaseOverride, mongoDumpScript, true),
	{StackCompose, DBMongoSize}:             dbScript(composeBase, mongoSizeScript, true),
	{StackCompose, DBMongoSizeOverride}:     dbScript(composeBaseOverride, mongoSizeScript, true),
}

// dbScript is a database action: exec -T {service} sh -c <script> sh, then
// {dbname} when named. The script is one fixed argv element; "sh" fills $0, so
// the name is "$1" inside it. -T keeps the dump's bytes free of a TTY.
func dbScript(base []arg, script string, named bool) []arg {
	a := join(base, lit("exec", "-T"), []arg{nameArg("service")}, lit("sh", "-c", script, "sh"))
	if named {
		a = append(a, dbNameArg())
	}
	return a
}

// isDump says whether a is a database dump, which only RunTo runs.
func isDump(a Action) bool {
	switch a {
	case DBPgDump, DBPgDumpOverride, DBPgDumpDefault, DBPgDumpDefaultOverride,
		DBMySQLDump, DBMySQLDumpOverride, DBMongoDump, DBMongoDumpOverride:
		return true
	}
	return false
}

// inspectFormat is the --format template of DockerInspect. docker inspect runs
// templates on the JSON map, so the id key is .Id; docker ps and docker stats
// use {{.ID}}.
const inspectFormat = "{{.Id}}\t{{.RestartCount}}\t{{json .State}}"

// statsFormat is the --format template of DockerStats.
const statsFormat = "{{.ID}}\t{{.CPUPerc}}\t{{.MemUsage}}"

// The log tails of compose logs. followLogs never ends by itself and is run
// only by Stream; tailLogs reads the last lines and exits.
var (
	followLogs = join(lit("logs", "--no-color", "--timestamps", "--follow", "--since"), []arg{sinceArg("since")})
	tailLogs   = lit("logs", "--no-color", "--timestamps", "--tail", "500")
)

// isFollow says whether a is a log action that follows, which only Stream runs.
func isFollow(a Action) bool {
	switch a {
	case ComposeLogs, ComposeLogsOverride, ComposeLogsService, ComposeLogsServiceOverride:
		return true
	}
	return false
}

// composeBase is the start of every compose command on a project folder and its
// compose file, and composeBaseOverride adds the override file.
var (
	composeBase = join(
		lit("compose", "--project-directory"), []arg{pathArg("dir")},
		lit("-f"), []arg{pathArg("file")},
	)
	composeBaseOverride = join(composeBase, lit("-f"), []arg{pathArg("override")})
)

// argv checks the call against its template and returns the argument list,
// without the program. Every error is returned before anything runs, and
// every value ends up as one separate element, never as shell text.
func argv(s Stack, a Action, v Values) ([]string, error) {
	tpl, ok := registry[key{s, a}]
	if !ok {
		return nil, fmt.Errorf("%w: %s on %s", ErrUnknownCommand, a, s)
	}
	used := map[string]bool{}
	out := make([]string, 0, len(tpl))
	for _, el := range tpl {
		if el.slot == "" {
			out = append(out, el.literal)
			continue
		}
		used[el.slot] = true
		val, ok := v[el.slot]
		if !ok {
			return nil, fmt.Errorf("%w: %s needs {%s}", ErrBadValue, a, el.slot)
		}
		switch el.kind {
		case kindIDs:
			ids, err := checkIDs(el.slot, val)
			if err != nil {
				return nil, err
			}
			out = append(out, ids...)
			continue
		case kindName:
			if err := checkName(el.slot, val); err != nil {
				return nil, err
			}
		case kindSince:
			if err := checkSince(el.slot, val); err != nil {
				return nil, err
			}
		case kindDBName:
			if !ValidDBName(val) {
				return nil, fmt.Errorf("%w: {%s} is not a valid database name", ErrBadValue, el.slot)
			}
		default:
			if err := checkPath(el.slot, val); err != nil {
				return nil, err
			}
		}
		out = append(out, val)
	}
	for name := range v {
		if !used[name] {
			return nil, fmt.Errorf("%w: %s does not take {%s}", ErrBadValue, a, name)
		}
	}
	return out, nil
}

// checkPath accepts only an absolute, filepath.Clean path without NUL bytes.
// Being absolute, it cannot start with "-" and be read as a flag.
func checkPath(slot, val string) error {
	switch {
	case val == "":
		return fmt.Errorf("%w: {%s} is empty", ErrBadValue, slot)
	case strings.ContainsRune(val, 0):
		return fmt.Errorf("%w: {%s} contains a NUL byte", ErrBadValue, slot)
	case !filepath.IsAbs(val):
		return fmt.Errorf("%w: {%s} is not an absolute path", ErrBadValue, slot)
	case filepath.Clean(val) != val:
		return fmt.Errorf("%w: {%s} is not a clean path", ErrBadValue, slot)
	}
	return nil
}

// checkIDs splits the space-joined ids and accepts 1 to 1,000 full container
// ids. An id is 64 lowercase hex digits, so it cannot start with "-".
func checkIDs(slot, val string) ([]string, error) {
	ids := strings.Split(val, " ")
	if len(ids) > maxIDs {
		return nil, fmt.Errorf("%w: {%s} has %d ids, at most %d", ErrBadValue, slot, len(ids), maxIDs)
	}
	for _, id := range ids {
		if !idPattern.MatchString(id) {
			return nil, fmt.Errorf("%w: {%s} has an id that is not 64 lowercase hex digits", ErrBadValue, slot)
		}
	}
	return ids, nil
}

// checkName accepts a compose service name: it starts with a letter or digit,
// so it cannot be read as a flag.
func checkName(slot, val string) error {
	if !namePattern.MatchString(val) {
		return fmt.Errorf("%w: {%s} is not a valid name", ErrBadValue, slot)
	}
	return nil
}

// checkSince accepts an RFC 3339 UTC timestamp with an optional fraction.
func checkSince(slot, val string) error {
	if !sincePattern.MatchString(val) {
		return fmt.Errorf("%w: {%s} is not an RFC 3339 UTC timestamp", ErrBadValue, slot)
	}
	if _, err := time.Parse(time.RFC3339Nano, val); err != nil {
		return fmt.Errorf("%w: {%s} is not a valid timestamp", ErrBadValue, slot)
	}
	return nil
}
