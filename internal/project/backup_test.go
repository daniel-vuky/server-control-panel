package project

import (
	"strings"
	"testing"
)

func TestDatabaseKey(t *testing.T) {
	d := Database{Type: "postgresql", Name: "shop", Location: "shop-db"}
	if got := DatabaseKey(d); got != "postgresql:shop-db:shop" {
		t.Errorf("DatabaseKey = %q", got)
	}
}

func TestNameIsPlaceholder(t *testing.T) {
	d := Database{Type: "mysql", Name: "db", Location: "shop-db"}
	if !NameIsPlaceholder(d, "db") {
		t.Error("name equal to the service is a placeholder")
	}
	if NameIsPlaceholder(d, "mysql") {
		t.Error("a different service name is not a match")
	}
	// Compared with the resolved service, not the location.
	d.Name = "shop-db"
	if NameIsPlaceholder(d, "db") {
		t.Error("a name equal to the location only is not a placeholder")
	}
	// A name the service sets explicitly is never a placeholder, even when it
	// equals the service (MYSQL_DATABASE: db on service db).
	d = Database{Type: "mysql", Name: "db", NameExplicit: true, LocationKind: "container", Location: "db"}
	if NameIsPlaceholder(d, "db") {
		t.Error("an explicit name equal to the service is a placeholder")
	}
}

func ctr(svc, name, state string) Container {
	return Container{Project: "shop", Service: svc, Name: name, State: state}
}

func TestResolveServiceByContainerName(t *testing.T) {
	cs := []Container{ctr("web", "shop-web-1", "running"), ctr("db", "shop-db", "running")}
	if s, ok := ResolveService("shop-db", cs, "shop", nil); !ok || s != "db" {
		t.Errorf("via container list = %q, %v", s, ok)
	}
	// Containers of another compose project never match.
	other := []Container{{Project: "blog", Service: "db", Name: "shop-db", State: "running"}}
	if _, ok := ResolveService("shop-db", other, "shop", nil); ok {
		t.Error("a container of another project resolved")
	}
	// No container: the stored service's container_name.
	svcs := []Service{{Name: "web"}, {Name: "db", ContainerName: "shop-db"}}
	if s, ok := ResolveService("shop-db", nil, "shop", svcs); !ok || s != "db" {
		t.Errorf("via stored container_name = %q, %v", s, ok)
	}
}

func TestResolveServiceByServiceName(t *testing.T) {
	cs := []Container{ctr("db", "shop-db-1", "exited")}
	if s, ok := ResolveService("db", cs, "shop", nil); !ok || s != "db" {
		t.Errorf("via container service = %q, %v", s, ok)
	}
	svcs := []Service{{Name: "db"}}
	if s, ok := ResolveService("db", nil, "shop", svcs); !ok || s != "db" {
		t.Errorf("via stored service = %q, %v", s, ok)
	}
}

func TestResolveServiceUnknown(t *testing.T) {
	cs := []Container{ctr("web", "shop-web-1", "running")}
	svcs := []Service{{Name: "web"}}
	if s, ok := ResolveService("db", cs, "shop", svcs); ok || s != "" {
		t.Errorf("unknown = %q, %v", s, ok)
	}
	if _, ok := ResolveService("", cs, "shop", svcs); ok {
		t.Error("an empty location resolved")
	}
}

func TestBackupReason(t *testing.T) {
	running := []Container{ctr("db", "shop-db-1", "running")}
	stopped := []Container{ctr("db", "shop-db-1", "exited")}
	otherProject := []Container{{Project: "blog", Service: "db", Name: "blog-db-1", State: "running"}}
	svcs := []Service{{Name: "db"}}
	oneOff := []Container{{Project: "shop", Service: "db", Name: "shop-db-run-1", State: "running", OneOff: true}}

	pg := func(name string) Database {
		return Database{Type: "postgresql", Name: name, LocationKind: "container", Location: "db"}
	}
	my := func(name string) Database {
		return Database{Type: "mysql", Name: name, LocationKind: "container", Location: "db"}
	}
	mongo := func(name string) Database {
		return Database{Type: "mongodb", Name: name, LocationKind: "container", Location: "db"}
	}
	explicit := func(d Database) Database {
		d.NameExplicit = true
		return d
	}

	tests := []struct {
		name        string
		d           Database
		svcs        []Service
		cs          []Container
		dockerUp    bool
		wantService string
		wantReason  string
	}{
		{"postgres runs", pg("shop"), svcs, running, true, "db", ""},
		{"mysql runs", my("shop"), svcs, running, true, "db", ""},
		{"mongo runs", mongo("shop"), svcs, running, true, "db", ""},
		{"service found only in stored services", pg("shop"), svcs, nil, true, "db", "db_not_running"},
		{"service found only in the container list", pg("shop"), nil, running, true, "db", ""},

		{"sqlite", Database{Type: "sqlite", Name: "a.db", LocationKind: "file", Location: "a.db"}, svcs, running, true, "", "not_supported"},
		{"sqlite in a container is still refused", Database{Type: "sqlite", Name: "db", LocationKind: "container", Location: "db"}, svcs, running, true, "", "not_supported"},
		{"host", Database{Type: "postgresql", Name: "shop", LocationKind: "host"}, svcs, running, true, "", "not_supported"},
		{"external", Database{Type: "postgresql", Name: "shop", LocationKind: "external", Location: "rds.example.com"}, svcs, running, true, "", "not_supported"},
		{"unknown kind", Database{Type: "postgresql", Name: "shop", LocationKind: "unknown"}, svcs, running, true, "", "not_supported"},
		{"service does not resolve", Database{Type: "postgresql", Name: "shop", LocationKind: "container", Location: "gone"}, svcs, running, true, "", "not_supported"},
		{"not supported wins over docker down", Database{Type: "sqlite", Name: "a.db", LocationKind: "file", Location: "a.db"}, svcs, nil, false, "", "not_supported"},

		{"mysql placeholder name", my("db"), svcs, running, true, "db", "no_db_name"},
		{"mongo placeholder name", mongo("db"), svcs, running, true, "db", "no_db_name"},
		{"mysql explicit name equal to the service", explicit(my("db")), svcs, running, true, "db", ""},
		{"mongo explicit name equal to the service", explicit(mongo("db")), svcs, running, true, "db", ""},
		{"postgres placeholder name dumps the default", pg("db"), svcs, running, true, "db", ""},
		{"postgres placeholder name, stopped", pg("db"), svcs, stopped, true, "db", "db_not_running"},
		{"name with a leading dash", pg("-x"), svcs, running, true, "db", "no_db_name"},
		{"name with a space", my("a b"), svcs, running, true, "db", "no_db_name"},
		{"name with a quote", mongo("a'b"), svcs, running, true, "db", "no_db_name"},
		{"empty name", pg(""), svcs, running, true, "db", "no_db_name"},
		{"name too long", my(strings.Repeat("a", 65)), svcs, running, true, "db", "no_db_name"},
		{"name with a dollar and a dot", my("my.db$1"), svcs, running, true, "db", ""},
		{"no_db_name wins over not running", my("db"), svcs, stopped, true, "db", "no_db_name"},
		{"no_db_name wins over docker down", my("db"), svcs, nil, false, "db", "no_db_name"},

		{"stopped", pg("shop"), svcs, stopped, true, "db", "db_not_running"},
		{"docker down", pg("shop"), svcs, running, false, "db", "db_not_running"},
		{"container of another project", pg("shop"), svcs, otherProject, true, "db", "db_not_running"},
		{"only a one-off container runs", pg("shop"), svcs, oneOff, true, "db", "db_not_running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, reason := BackupReason(tt.d, "shop", tt.svcs, tt.cs, tt.dockerUp)
			if svc != tt.wantService || reason != tt.wantReason {
				t.Errorf("BackupReason = (%q, %q), want (%q, %q)", svc, reason, tt.wantService, tt.wantReason)
			}
		})
	}
}
