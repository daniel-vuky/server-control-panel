package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

func TestMigration0004Defaults(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"0001_init.sql", "0002_activity.sql", "0003_workspace_management.sql"} {
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations VALUES (?, ?)`, i+1, "2026-10-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO workspaces (name, path, depth, created_at, last_scan_at) VALUES ('apps', '/srv/apps', 2, '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects (workspace_id, path, compose_files, compose_project, display_name, state, self, details, created_at)
		VALUES (1, '/srv/apps/shop', '["compose.yaml"]', 'shop', 'Shop', 'tracked', 0, '{}', '2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open on a v3 database: %v", err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.TrackedProject(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.StopTemplate != "stop" || got.PublicDomain != "" || got.AlertCPUPercent != nil || got.AlertRAMMB != nil {
		t.Fatalf("defaults = %+v", got)
	}
	var domain sql.NullString
	if err := s.db.QueryRow(`SELECT public_domain FROM projects WHERE id = 1`).Scan(&domain); err != nil || domain.Valid {
		t.Fatalf("public_domain = %v, %v; want NULL", domain, err)
	}
}

func TestUpdateSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)

	next := Settings{DisplayName: "My Shop", PublicDomain: "shop.example.com", StopTemplate: "down", AlertCPUPercent: intp(85), AlertRAMMB: intp(512)}
	before, after, err := s.UpdateSettings(ctx, shopID, next)
	if err != nil {
		t.Fatal(err)
	}
	if before.DisplayName != "Shop" || before.StopTemplate != "stop" || before.PublicDomain != "" || before.AlertCPUPercent != nil || before.AlertRAMMB != nil {
		t.Fatalf("before = %+v", before)
	}
	if after.DisplayName != "My Shop" || after.PublicDomain != "shop.example.com" || after.StopTemplate != "down" ||
		after.AlertCPUPercent == nil || *after.AlertCPUPercent != 85 || after.AlertRAMMB == nil || *after.AlertRAMMB != 512 {
		t.Fatalf("after = %+v", after)
	}
	if strings.Join(after.Services, ",") != "web,db" || len(after.Databases) != 1 {
		t.Fatalf("after lost its config: %+v", after)
	}
	got, err := s.TrackedProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "My Shop" || got.PublicDomain != "shop.example.com" || got.StopTemplate != "down" ||
		got.AlertCPUPercent == nil || *got.AlertCPUPercent != 85 || got.AlertRAMMB == nil || *got.AlertRAMMB != 512 {
		t.Fatalf("stored = %+v", got)
	}

	// Clearing: an empty domain and nil thresholds go back to NULL.
	_, after, err = s.UpdateSettings(ctx, shopID, Settings{DisplayName: "My Shop", StopTemplate: "stop"})
	if err != nil {
		t.Fatal(err)
	}
	if after.PublicDomain != "" || after.AlertCPUPercent != nil || after.AlertRAMMB != nil || after.StopTemplate != "stop" {
		t.Fatalf("cleared = %+v", after)
	}
	var domain sql.NullString
	var cpu, ram sql.NullInt64
	if err := s.db.QueryRow(`SELECT public_domain, alert_cpu_percent, alert_ram_mb FROM projects WHERE id = ?`, shopID).Scan(&domain, &cpu, &ram); err != nil {
		t.Fatal(err)
	}
	if domain.Valid || cpu.Valid || ram.Valid {
		t.Fatalf("columns = %v %v %v, want NULLs", domain, cpu, ram)
	}
}

func TestUpdateSettingsIgnoredIsNotFound(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, _, blogID := seedApps(t, s)
	next := Settings{DisplayName: "X", StopTemplate: "stop"}
	for _, id := range []int64{blogID, 999, 0, -1} {
		if _, _, err := s.UpdateSettings(ctx, id, next); !errors.Is(err, ErrNotFound) {
			t.Fatalf("id %d: err = %v, want ErrNotFound", id, err)
		}
	}
	var name string
	if err := s.db.QueryRow(`SELECT display_name FROM projects WHERE id = ?`, blogID).Scan(&name); err != nil || name != "Blog" {
		t.Fatalf("ignored row changed: %q, %v", name, err)
	}
}

func TestRescanKeepsSettings(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, blogID := seedApps(t, s)
	want := Settings{DisplayName: "My Shop", PublicDomain: "shop.example.com", StopTemplate: "down", AlertCPUPercent: intp(70), AlertRAMMB: intp(256)}
	if _, _, err := s.UpdateSettings(ctx, shopID, want); err != nil {
		t.Fatal(err)
	}
	// Blog is ignored with its own settings columns set, then re-tracked.
	if _, err := s.db.Exec(`UPDATE projects SET public_domain = 'blog.example.com', stop_template = 'down', alert_cpu_percent = 60, alert_ram_mb = 128 WHERE id = ?`, blogID); err != nil {
		t.Fatal(err)
	}

	r := refreshOf("/srv/apps/shop", "api")
	apply := RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: laterTime, Refresh: []ProjectRefresh{r}, Found: []string{"/srv/apps/shop"}}}}
	if err := s.ApplyRescan(ctx, apply, laterTime); err != nil {
		t.Fatal(err)
	}
	got, err := s.TrackedProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != want.DisplayName || got.PublicDomain != want.PublicDomain || got.StopTemplate != want.StopTemplate ||
		got.AlertCPUPercent == nil || *got.AlertCPUPercent != 70 || got.AlertRAMMB == nil || *got.AlertRAMMB != 256 {
		t.Fatalf("after rescan: %+v", got)
	}

	if n, err := s.AddProjects(ctx, rescanJob(t, s), []AddItem{newItem(wsID, "/srv/apps/blog", true)}); err != nil || n != 1 {
		t.Fatalf("AddProjects = %d, %v", n, err)
	}
	got, err = s.TrackedProject(ctx, blogID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicDomain != "blog.example.com" || got.StopTemplate != "down" ||
		got.AlertCPUPercent == nil || *got.AlertCPUPercent != 60 || got.AlertRAMMB == nil || *got.AlertRAMMB != 128 {
		t.Fatalf("after re-track: %+v", got)
	}
}

func TestStopTemplateCheck(t *testing.T) {
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	if _, err := s.db.Exec(`UPDATE projects SET stop_template = 'halt' WHERE id = ?`, shopID); err == nil {
		t.Fatal("raw update to 'halt' succeeded, want a CHECK failure")
	}
	if _, err := s.db.Exec(`INSERT INTO projects (workspace_id, path, compose_files, compose_project, display_name, state, self, details, created_at, stop_template)
		VALUES (1, '/srv/apps/halt', '[]', 'halt', 'Halt', 'tracked', 0, '{}', '2026-10-01T00:00:00Z', 'halt')`); err == nil {
		t.Fatal("raw insert of 'halt' succeeded, want a CHECK failure")
	}
}
