package scan

import (
	"os"
	"reflect"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name) //nolint:gosec // fixed test fixture names
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig(readFixture(t, "config-basic.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "shop" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if len(cfg.Services) != 2 || cfg.Services[0].Name != "db" || cfg.Services[1].Name != "web" {
		t.Fatalf("services not sorted by name: %+v", cfg.Services)
	}
	db, web := cfg.Services[0], cfg.Services[1]

	if db.Image != "postgres:16" || db.ContainerName != "" {
		t.Errorf("db = %+v", db)
	}
	if want := []project.Volume{{Type: "volume", Source: "dbdata", Target: "/var/lib/postgresql/data"}}; !reflect.DeepEqual(db.Volumes, want) {
		t.Errorf("db volumes = %+v", db.Volumes)
	}

	if web.Image != "nginx:1.27" || web.ContainerName != "shop-web" {
		t.Errorf("web = %+v", web)
	}
	if want := []string{"8080:80/tcp", "53/udp"}; !reflect.DeepEqual(web.Ports, want) {
		t.Errorf("web ports = %v, want %v", web.Ports, want)
	}
	if want := []project.Volume{{Type: "bind", Source: "/srv/shop/html", Target: "/usr/share/nginx/html"}}; !reflect.DeepEqual(web.Volumes, want) {
		t.Errorf("web volumes = %+v", web.Volumes)
	}
	wantEnv := map[string]string{ //nolint:gosec // fake credential in a fixture
		"DATABASE_URL": "postgres://shop:devpassword@db/shop",
		"LOG_LEVEL":    "info",
		"UNSET_VAR":    "",
	}
	if !reflect.DeepEqual(web.Environment, wantEnv) {
		t.Errorf("web env = %v", web.Environment)
	}
}

func TestDetails(t *testing.T) {
	cfg, err := ParseConfig(readFixture(t, "config-basic.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Details()
	if len(d.Services) != 2 || d.Services[0].Name != "db" || d.Services[1].Name != "web" {
		t.Fatalf("details = %+v", d)
	}
	if d.Services[1].Image != "nginx:1.27" || !reflect.DeepEqual(d.Services[1].Ports, []string{"8080:80/tcp", "53/udp"}) {
		t.Errorf("web = %+v", d.Services[1])
	}
	// Details never carries environment values; the type has no field for it.
	if d.Services[0].Ports == nil || d.Services[0].Volumes == nil {
		t.Errorf("nil slices would marshal as null: %+v", d.Services[0])
	}
}

func TestParseConfigTruncated(t *testing.T) {
	b := readFixture(t, "config-basic.json")
	if _, err := ParseConfig(b[:100]); err == nil {
		t.Fatal("want an error for truncated input")
	}
}

func TestParseConfigLeadingNoise(t *testing.T) {
	b := append([]byte("WARN[0000] the \"X\" variable is not set\n"), readFixture(t, "config-basic.json")...)
	if _, err := ParseConfig(b); err == nil {
		t.Fatal("want an error for a line before the JSON")
	}
}

func TestParseConfigEmpty(t *testing.T) {
	if _, err := ParseConfig(nil); err == nil {
		t.Fatal("want an error for empty input")
	}
}

func TestNormalizeProjectName(t *testing.T) {
	cases := map[string]string{
		"My App.v2": "myappv2",
		"__x":       "x",
		"-a_b-c":    "a_b-c",
		"Shop":      "shop",
		"é app":     "app",
		"":          "",
	}
	for in, want := range cases {
		if got := NormalizeProjectName(in); got != want {
			t.Errorf("NormalizeProjectName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeDetailsContainerName(t *testing.T) {
	cfg, err := ParseConfig(readFixture(t, "config-basic.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Details()
	if d.Services[0].Name != "db" || d.Services[0].ContainerName != "" {
		t.Errorf("db = %+v, want no container name", d.Services[0])
	}
	if d.Services[1].Name != "web" || d.Services[1].ContainerName != "shop-web" {
		t.Errorf("web = %+v, want container name shop-web", d.Services[1])
	}
}
