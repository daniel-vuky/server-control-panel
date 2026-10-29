//nolint:gosec // fake credentials in URL fixtures
package scan

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

func svc(name, image string, env map[string]string) ComposeService {
	return ComposeService{Name: name, Image: image, Environment: env}
}

func cfgOf(services ...ComposeService) ComposeConfig {
	return ComposeConfig{Name: "p", Services: services}
}

func db(t, name, kind, loc string, sources ...string) project.Database {
	return project.Database{Type: t, Name: name, LocationKind: kind, Location: loc, Sources: sources}
}

func TestDetectDatabases(t *testing.T) {
	tests := []struct {
		name   string
		cfg    ComposeConfig
		dotenv map[string]string
		want   []project.Database
	}{
		{"postgres image", cfgOf(svc("db", "postgres:16", nil)),
			nil, []project.Database{db("postgresql", "db", "container", "db", "service db")}},
		{"postgresql image", cfgOf(svc("db", "postgresql", nil)),
			nil, []project.Database{db("postgresql", "db", "container", "db", "service db")}},
		{"mysql image", cfgOf(svc("db", "mysql:8", nil)),
			nil, []project.Database{db("mysql", "db", "container", "db", "service db")}},
		{"mysql-server image", cfgOf(svc("db", "mysql-server", nil)),
			nil, []project.Database{db("mysql", "db", "container", "db", "service db")}},
		{"mariadb image", cfgOf(svc("db", "mariadb:11", nil)),
			nil, []project.Database{db("mysql", "db", "container", "db", "service db")}},
		{"mongo image", cfgOf(svc("db", "mongo:7", nil)),
			nil, []project.Database{db("mongodb", "db", "container", "db", "service db")}},
		{"mongodb image", cfgOf(svc("db", "mongodb", nil)),
			nil, []project.Database{db("mongodb", "db", "container", "db", "service db")}},
		{"bitnami postgresql", cfgOf(svc("db", "bitnami/postgresql:16", nil)),
			nil, []project.Database{db("postgresql", "db", "container", "db", "service db")}},
		{"registry with port and digest", cfgOf(svc("db", "registry.local:5000/library/mongo@sha256:0123456789abcdef", nil)),
			nil, []project.Database{db("mongodb", "db", "container", "db", "service db")}},
		{"docker.io digest", cfgOf(svc("db", "docker.io/library/mongo@sha256:0123456789abcdef", nil)),
			nil, []project.Database{db("mongodb", "db", "container", "db", "service db")}},
		{"mongo-express matches nothing", cfgOf(svc("ui", "mongo-express:1", nil)),
			nil, []project.Database{}},
		{"postgres-exporter matches nothing", cfgOf(svc("exp", "prometheuscommunity/postgres-exporter", nil)),
			nil, []project.Database{}},
		{"no image", cfgOf(svc("web", "", nil)), nil, []project.Database{}},

		{"POSTGRES_DB names the database", cfgOf(svc("db", "postgres:16", map[string]string{"POSTGRES_DB": "shop", "POSTGRES_PASSWORD": "x"})),
			nil, []project.Database{db("postgresql", "shop", "container", "db", "service db", "service db POSTGRES_DB")}},
		{"MYSQL_DATABASE names the database", cfgOf(svc("db", "mysql:8", map[string]string{"MYSQL_DATABASE": "shop"})),
			nil, []project.Database{db("mysql", "shop", "container", "db", "service db", "service db MYSQL_DATABASE")}},
		{"MARIADB_DATABASE names the database", cfgOf(svc("db", "mariadb:11", map[string]string{"MARIADB_DATABASE": "shop"})),
			nil, []project.Database{db("mysql", "shop", "container", "db")}},
		{"MONGO_INITDB_DATABASE names the database", cfgOf(svc("db", "mongo:7", map[string]string{"MONGO_INITDB_DATABASE": "shop"})),
			nil, []project.Database{db("mongodb", "shop", "container", "db")}},
		{"container_name is the location", cfgOf(ComposeService{Name: "db", Image: "postgres:16", ContainerName: "shop-db"}),
			nil, []project.Database{db("postgresql", "db", "container", "shop-db", "service db")}},

		{"DATABASE_URL postgres", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://u:p@db.example.com:5432/shop"},
			[]project.Database{db("postgresql", "shop", "external", "db.example.com", ".env DATABASE_URL")}},
		{"DATABASE_URL postgresql", cfgOf(),
			map[string]string{"DATABASE_URL": "postgresql://u:p@db.example.com/shop?sslmode=require"},
			[]project.Database{db("postgresql", "shop", "external", "db.example.com", ".env DATABASE_URL")}},
		{"DATABASE_URL mysql", cfgOf(),
			map[string]string{"DATABASE_URL": "mysql://u:p@db.example.com/shop"},
			[]project.Database{db("mysql", "shop", "external", "db.example.com", ".env DATABASE_URL")}},
		{"DATABASE_URL mariadb", cfgOf(),
			map[string]string{"DATABASE_URL": "mariadb://u:p@db.example.com/shop"},
			[]project.Database{db("mysql", "shop", "external", "db.example.com", ".env DATABASE_URL")}},
		{"DATABASE_URL mongodb", cfgOf(),
			map[string]string{"DATABASE_URL": "mongodb://u:p@db.example.com:27017/shop"},
			[]project.Database{db("mongodb", "shop", "external", "db.example.com", ".env DATABASE_URL")}},
		{"MONGO_URI mongodb+srv", cfgOf(),
			map[string]string{"MONGO_URI": "mongodb+srv://u:p@cluster0.mongodb.net/shop?retryWrites=true"},
			[]project.Database{db("mongodb", "shop", "external", "cluster0.mongodb.net", ".env MONGO_URI")}},
		{"file URL is sqlite file", cfgOf(),
			map[string]string{"DATABASE_URL": "file:./data/notes.db"},
			[]project.Database{db("sqlite", "./data/notes.db", "file", "./data/notes.db", ".env DATABASE_URL")}},
		{"sqlite URL is sqlite file", cfgOf(),
			map[string]string{"DATABASE_URL": "sqlite:///data/app.db"},
			[]project.Database{db("sqlite", "data/app.db", "file", "data/app.db", ".env DATABASE_URL")}},
		{"sqlite absolute URL", cfgOf(),
			map[string]string{"DATABASE_URL": "sqlite:////var/lib/app.db?mode=rw"},
			[]project.Database{db("sqlite", "/var/lib/app.db", "file", "/var/lib/app.db", ".env DATABASE_URL")}},
		{"unknown scheme ignored", cfgOf(),
			map[string]string{"DATABASE_URL": "redis://cache:6379/0"}, []project.Database{}},
		{"not a URL ignored", cfgOf(),
			map[string]string{"DATABASE_URL": "nonsense"}, []project.Database{}},
		{"URL from a service environment", cfgOf(svc("web", "app", map[string]string{"DATABASE_URL": "postgres://u:p@rds.example.com/shop"})),
			nil, []project.Database{db("postgresql", "shop", "external", "rds.example.com", "service web DATABASE_URL")}},

		{"host that is a service merges with both sources",
			cfgOf(svc("db", "postgres:16", nil)),
			map[string]string{"DATABASE_URL": "postgres://u:p@db:5432/shop"},
			[]project.Database{db("postgresql", "db", "container", "db", ".env DATABASE_URL", "service db")}},
		{"host that is a container_name merges",
			cfgOf(ComposeService{Name: "db", Image: "postgres:16", ContainerName: "shop-db"}),
			map[string]string{"DATABASE_URL": "postgres://u:p@shop-db/shop"},
			[]project.Database{db("postgresql", "db", "container", "shop-db", ".env DATABASE_URL", "service db")}},
		{"host of another type does not merge",
			cfgOf(svc("db", "mysql:8", nil)),
			map[string]string{"DATABASE_URL": "postgres://u:p@db/shop"},
			[]project.Database{
				db("mysql", "db", "container", "db", "service db"),
				db("postgresql", "shop", "container", "db", ".env DATABASE_URL"),
			}},
		{"URL with no host merges into the service of its type",
			cfgOf(svc("db", "postgres:16", nil)),
			map[string]string{"DATABASE_URL": "postgres:///shop"},
			[]project.Database{db("postgresql", "db", "container", "db", ".env DATABASE_URL", "service db")}},
		{"URL with no host and no service is unknown",
			cfgOf(),
			map[string]string{"DATABASE_URL": "postgres:///shop"},
			[]project.Database{db("postgresql", "shop", "unknown", "", ".env DATABASE_URL")}},
		{"localhost is host", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://u:p@localhost:5432/shop"},
			[]project.Database{db("postgresql", "shop", "host", "", ".env DATABASE_URL")}},
		{"127.0.0.1 is host", cfgOf(),
			map[string]string{"DATABASE_URL": "mysql://u:p@127.0.0.1/shop"},
			[]project.Database{db("mysql", "shop", "host", "", ".env DATABASE_URL")}},
		{"host.docker.internal is host", cfgOf(),
			map[string]string{"DATABASE_URL": "mongodb://host.docker.internal/shop"},
			[]project.Database{db("mongodb", "shop", "host", "", ".env DATABASE_URL")}},
		{"rds.example.com is external", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://u:p@rds.example.com/shop"},
			[]project.Database{db("postgresql", "shop", "external", "rds.example.com", ".env DATABASE_URL")}},
		{"URL with an unescaped slash-free odd password", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://u:p%zz@rds.example.com/shop"},
			[]project.Database{db("postgresql", "shop", "external", "rds.example.com", ".env DATABASE_URL")}},

		{"slash in the password is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://shopuser:pa/ss@db.example.com/shop"}, []project.Database{}},
		{"question mark in the password is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://shopuser:pa?ss@db.example.com/shop"}, []project.Database{}},
		{"hash in the password is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://shopuser:pa#ss@db.example.com:5432/shop"}, []project.Database{}},
		{"at sign in the password is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://shopuser:pa@ss@db.example.com/shop"}, []project.Database{}},
		{"slash and at in the password of a mongodb replica set is refused", cfgOf(),
			map[string]string{"MONGO_URI": "mongodb://shopuser:p/a@s@h1.example.com:27017,h2.example.com:27017/shop?replicaSet=rs0"},
			[]project.Database{}},
		{"mongodb replica set without odd characters", cfgOf(),
			map[string]string{"MONGO_URI": "mongodb://u:p@h1.example.com:27017,h2.example.com:27017/shop?replicaSet=rs0"},
			[]project.Database{db("mongodb", "shop", "external", "h1.example.com", ".env MONGO_URI")}},
		{"password in the query with an at sign is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://db.example.com/shop?user=shopuser&password=p@ss"},
			[]project.Database{}},
		{"password in the query with a slash and an at sign is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://db.example.com/shop?password=a/b@c"},
			[]project.Database{}},
		{"unresolved port with a query password is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://db.example.com:${DB_PORT}/shop?password=p@ss"},
			[]project.Database{}},
		{"two at signs with a query at sign is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://shopuser:p@ss/wd@db.example.com/shop?x=a@b&c=1"},
			[]project.Database{}},
		{"numeric password prefix with a slash is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://user:123/abc@host/db?x=a@b=c"},
			[]project.Database{}},
		{"benign at sign in the query is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://db.example.com/shop?email=a@b"},
			[]project.Database{}},
		{"password in the query without an at sign is fine", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://db.example.com/shop?user=shopuser&password=pass"},
			[]project.Database{db("postgresql", "shop", "external", "db.example.com", ".env DATABASE_URL")}},
		{"sqlite URL with a user and password is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "sqlite://shopuser:pass@host/x.db"}, []project.Database{}},
		{"sqlite URL with a slash in the password is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "sqlite://shopuser:pa/ss@host/x.db"}, []project.Database{}},
		{"file URL with localhost", cfgOf(),
			map[string]string{"DATABASE_URL": "file://localhost/var/app.db"},
			[]project.Database{db("sqlite", "/var/app.db", "file", "/var/app.db", ".env DATABASE_URL")}},
		{"unparseable authority is refused, not misread", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://shopuser:pa/ss"}, []project.Database{}},
		{"bad port is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://db.example.com:pa/shop"}, []project.Database{}},
		{"IPv6 host", cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://u:p@[::1]:5432/shop"},
			[]project.Database{db("postgresql", "shop", "external", "::1", ".env DATABASE_URL")}},

		{"POSTGRES_* with no host and a postgres service merges",
			cfgOf(svc("db", "postgres:16", nil)),
			map[string]string{"POSTGRES_DB": "shop", "POSTGRES_USER": "u", "POSTGRES_PASSWORD": "p"},
			[]project.Database{db("postgresql", "db", "container", "db", ".env POSTGRES_DB", "service db")}},
		{"POSTGRES_* with a host that is a service merges",
			cfgOf(svc("db", "postgres:16", nil)),
			map[string]string{"POSTGRES_DB": "shop", "POSTGRES_HOST": "db"},
			[]project.Database{db("postgresql", "db", "container", "db", ".env POSTGRES_DB POSTGRES_HOST", "service db")}},
		{"POSTGRES_* with an external host",
			cfgOf(),
			map[string]string{"POSTGRES_DB": "shop", "POSTGRES_HOST": "rds.example.com"},
			[]project.Database{db("postgresql", "shop", "external", "rds.example.com", ".env POSTGRES_DB POSTGRES_HOST")}},
		{"MYSQL_* with no host and no service is unknown",
			cfgOf(),
			map[string]string{"MYSQL_DATABASE": "shop", "MYSQL_USER": "u", "MYSQL_PASSWORD": "p"},
			[]project.Database{db("mysql", "shop", "unknown", "", ".env MYSQL_DATABASE")}},
		{"MYSQL_HOST localhost is host",
			cfgOf(),
			map[string]string{"MYSQL_DATABASE": "shop", "MYSQL_HOST": "localhost"},
			[]project.Database{db("mysql", "shop", "host", "", ".env MYSQL_DATABASE MYSQL_HOST")}},
		{"POSTGRES_HOST with a port matches the service",
			cfgOf(svc("db", "postgres:16", nil)),
			map[string]string{"POSTGRES_DB": "shop", "POSTGRES_HOST": "db:5432"},
			[]project.Database{db("postgresql", "db", "container", "db", ".env POSTGRES_DB POSTGRES_HOST", "service db")}},
		{"POSTGRES_HOST external with a port keeps the host only",
			cfgOf(),
			map[string]string{"POSTGRES_DB": "shop", "POSTGRES_HOST": "rds.example.com:5432"},
			[]project.Database{db("postgresql", "shop", "external", "rds.example.com", ".env POSTGRES_DB POSTGRES_HOST")}},
		{"MYSQL_HOST with a user and password is unknown, without its value",
			cfgOf(),
			map[string]string{"MYSQL_DATABASE": "shop", "MYSQL_HOST": "user:pass@rds.example.com"},
			[]project.Database{db("mysql", "shop", "unknown", "", ".env MYSQL_DATABASE MYSQL_HOST")}},
		{"MYSQL_HOST with a user is unknown",
			cfgOf(),
			map[string]string{"MYSQL_DATABASE": "shop", "MYSQL_HOST": "user@rds.example.com"},
			[]project.Database{db("mysql", "shop", "unknown", "", ".env MYSQL_DATABASE MYSQL_HOST")}},
		{"POSTGRES_HOST holding a URL is unknown",
			cfgOf(),
			map[string]string{"POSTGRES_DB": "shop", "POSTGRES_HOST": "postgres://u:p@rds.example.com/shop"},
			[]project.Database{db("postgresql", "shop", "unknown", "", ".env POSTGRES_DB POSTGRES_HOST")}},
		{"unclean POSTGRES_HOST does not merge into the postgres service",
			cfgOf(svc("db", "postgres:16", nil)),
			map[string]string{"POSTGRES_DB": "shop", "POSTGRES_HOST": "u:p@db"},
			[]project.Database{
				db("postgresql", "shop", "unknown", "", ".env POSTGRES_DB POSTGRES_HOST"),
				db("postgresql", "db", "container", "db", "service db"),
			}},
		{"sqlite opaque URL with a user and password is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "sqlite:user:pass@host/x.db"}, []project.Database{}},
		{"file opaque URL with a user and password is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "file:user:pass@h/x"}, []project.Database{}},
		{"file URL with a question mark in the password is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "file:user:p?ss@h/x"}, []project.Database{}},
		{"sqlite URL wrapping another URL is refused", cfgOf(),
			map[string]string{"DATABASE_URL": "sqlite:postgres://h/x.db"}, []project.Database{}},

		{"a password variable alone is not a database",
			cfgOf(),
			map[string]string{"POSTGRES_PASSWORD": "p", "MYSQL_ROOT_PASSWORD": "p"},
			[]project.Database{}},

		{"same database from .env and a service is merged",
			cfgOf(svc("web", "app", map[string]string{"DATABASE_URL": "postgres://u:p@rds.example.com/shop"})),
			map[string]string{"DATABASE_URL": "postgres://u:p@rds.example.com/shop"},
			[]project.Database{db("postgresql", "shop", "external", "rds.example.com", ".env DATABASE_URL", "service web DATABASE_URL")}},
		{"two distinct databases are two records",
			cfgOf(svc("db", "postgres:16", nil), svc("cache", "mongo:7", nil)),
			map[string]string{"DATABASE_URL": "mysql://u:p@rds.example.com/orders"},
			[]project.Database{
				db("mongodb", "cache", "container", "cache", "service cache"),
				db("mysql", "orders", "external", "rds.example.com", ".env DATABASE_URL"),
				db("postgresql", "db", "container", "db", "service db"),
			}},
		{"result is sorted by type, location, name",
			cfgOf(),
			map[string]string{"DATABASE_URL": "postgres://u:p@b.example.com/z", "MONGO_URI": "mongodb://a.example.com/a"},
			[]project.Database{
				db("mongodb", "a", "external", "a.example.com", ".env MONGO_URI"),
				db("postgresql", "z", "external", "b.example.com", ".env DATABASE_URL"),
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectDatabases(tc.cfg, tc.dotenv)
			if got == nil {
				t.Fatal("result is nil, want a non-nil slice")
			}
			for _, d := range got {
				if d.Sources == nil {
					t.Fatalf("%+v has nil Sources", d)
				}
			}
			// Expectations that omit sources of a variable the case is not about
			// are checked on type, name and location only.
			if len(got) != len(tc.want) {
				t.Fatalf("got %d databases, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				w := tc.want[i]
				g := got[i]
				if g.Type != w.Type || g.Name != w.Name || g.LocationKind != w.LocationKind || g.Location != w.Location {
					t.Errorf("database %d = %+v, want %+v", i, g, w)
				}
				if len(w.Sources) > 0 && !reflect.DeepEqual(g.Sources, w.Sources) {
					t.Errorf("database %d sources = %v, want %v", i, g.Sources, w.Sources)
				}
			}
		})
	}
}

func TestNoSecretInResult(t *testing.T) {
	cfg := cfgOf(
		svc("db", "postgres:16", map[string]string{"POSTGRES_DB": "shop", "POSTGRES_USER": "shop", "POSTGRES_PASSWORD": "devpassword"}),
		svc("web", "app", map[string]string{"DATABASE_URL": "postgres://shop:devpassword@db:5432/shop"}),
	)
	dotenv := map[string]string{
		"DATABASE_URL":      "postgres://shop:devpassword@db:5432/shop",
		"MONGO_URI":         "mongodb://shop:devpassword@mongo.example.com/shop",
		"POSTGRES_PASSWORD": "devpassword",
		"MYSQL_DATABASE":    "shop",
		"MYSQL_PASSWORD":    "devpassword",
	}
	for name, url := range map[string]string{
		"slash":    "postgres://shopuser:pa/ssfrag@db2.example.com/orders",
		"question": "postgres://shopuser:qu?erypart@db8.example.com/shop",
		"hash":     "postgres://shopuser:ha#shfrag@db3.example.com/hashdb",
		"at":       "postgres://shopuser:at@fragat@db4.example.com/atdb",
		"slash2":   "postgres://shopuser:sl/ashfrag@db5.example.com/apidb",
		"bad":      "postgres://shopuser:ba/dfrag",
		"qpw":      "postgres://db6.example.com/qdb?user=shopuser&password=p@ssqtail",
		"qpw2":     "postgres://db7.example.com/qdb2?password=a/b@btail",
		"port":     "postgres://db9.example.com:${DB_PORT}/portdb?password=p@sstail9",
		"twoat":    "postgres://shopuser:p@ss/wd@db10.example.com/twoatdb?x=a@b&c=1",
		"numeric":  "postgres://shopuser:123/abcnum@host11/numdb?x=a@b=c",
		"mongo":    "mongodb://shopuser:p/a@mongofrag@h12.example.com:27017,h13.example.com:27017/mdb",
		"lite":     "sqlite://shopuser:litepass@host/x.db",
		"lite2":    "sqlite://shopuser:lite/pass@host/x.db",
		"lite3":    "sqlite:shopuser:opaquelite@host/x.db",
		"lite4":    "file:shopuser:filepass@h/x",
		"lite5":    "file:shopuser:fi?lequery@h/x",
	} {
		key := "DATABASE_URL"
		if name == "mongo" {
			key = "MONGO_URI"
		}
		cfg.Services = append(cfg.Services, svc(name, "app", map[string]string{key: url}))
	}
	for name, env := range map[string]map[string]string{
		"myhost":  {"MYSQL_DATABASE": "hostdb", "MYSQL_HOST": "shopuser:hostpass@rds.example.com"},
		"pghost":  {"POSTGRES_DB": "pgdb", "POSTGRES_HOST": "shopuser@pg.example.com:5432"},
		"urlhost": {"POSTGRES_DB": "urldb", "POSTGRES_HOST": "postgres://shopuser:urlhostpass@pg2.example.com/urldb"},
	} {
		cfg.Services = append(cfg.Services, svc(name, "app", env))
	}
	got := DetectDatabases(cfg, dotenv)
	if len(got) == 0 {
		t.Fatal("expected databases")
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"devpassword", "shop:", "shopuser", "ssfrag", "erypart", "shfrag", "fragat", "ashfrag", "pa/", "qu?", "ha#", "ssqtail", "btail", "litepass", "lite/pass", "sstail9", "abcnum", "mongofrag", "wd@", "ss/", "123", "p@ss", "opaquelite", "filepass", "fi?", "hostpass", "urlhostpass", "@"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("result contains %q: %s", secret, b)
		}
	}
}

func TestDetectDatabasesDoesNotChangeItsInput(t *testing.T) {
	cfg := cfgOf(svc("web", "app", nil), svc("db", "postgres:16", nil))
	DetectDatabases(cfg, nil)
	if cfg.Services[0].Name != "web" {
		t.Error("services were reordered")
	}
}
