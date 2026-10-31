package project

import "github.com/daniel-vuky/server-control-panel/internal/runner"

// The reasons BackupReason gives for refusing a backup. The empty string means
// the database can be backed up now. The Drive reasons are layered on top by
// the callers.
const (
	ReasonNotSupported = "not_supported"
	ReasonNoDBName     = "no_db_name"
	ReasonDBNotRunning = "db_not_running"
)

// DatabaseKey identifies a detected database across scans: type, location and
// name. The location is not the resolved service, so the key stays stable.
func DatabaseKey(d Database) string {
	return d.Type + ":" + d.Location + ":" + d.Name
}

// ResolveService returns the compose service a container database's location
// belongs to. A container of the compose project whose name or service equals
// the location wins; else a stored service whose name or container_name equals
// it (rows stored before container_name was read have none, and rely on the
// container list). The bool is false when neither matches.
func ResolveService(location string, cs []Container, composeProject string, services []Service) (string, bool) {
	if location == "" {
		return "", false
	}
	for _, c := range cs {
		if c.Project != composeProject || c.OneOff || c.Service == "" {
			continue
		}
		if c.Name == location || c.Service == location {
			return c.Service, true
		}
	}
	for _, s := range services {
		if s.Name == location || (s.ContainerName != "" && s.ContainerName == location) {
			return s.Name, true
		}
	}
	return "", false
}

// NameIsPlaceholder reports whether the database's name is only the service
// name detection falls back to, so the real database name is not known. A name
// a variable set (MYSQL_DATABASE: db on service db) is never a placeholder;
// rows stored before NameExplicit was kept read as placeholders until the next
// rescan.
func NameIsPlaceholder(d Database, service string) bool {
	return !d.NameExplicit && d.Name == service
}

// BackupReason is the one rule for whether a database can be backed up now,
// used by the project views and by the backup manager. It returns the resolved
// compose service and the reason, which is "" when the backup may run. Order:
// not_supported, then no_db_name, then db_not_running.
//
// A PostgreSQL database with a placeholder name is allowed: it dumps the image
// default database. MySQL and MongoDB need the name.
func BackupReason(d Database, composeProject string, services []Service, cs []Container, dockerUp bool) (service, reason string) {
	switch d.Type {
	case "postgresql", "mysql", "mongodb":
	default:
		return "", ReasonNotSupported
	}
	if d.LocationKind != "container" {
		return "", ReasonNotSupported
	}
	service, ok := ResolveService(d.Location, cs, composeProject, services)
	if !ok {
		return "", ReasonNotSupported
	}
	if NameIsPlaceholder(d, service) {
		if d.Type != "postgresql" {
			return service, ReasonNoDBName
		}
	} else if !runner.ValidDBName(d.Name) {
		return service, ReasonNoDBName
	}
	if !dockerUp {
		return service, ReasonDBNotRunning
	}
	for _, c := range cs {
		if c.Project == composeProject && c.Service == service && !c.OneOff && c.State == "running" {
			return service, ""
		}
	}
	return service, ReasonDBNotRunning
}
