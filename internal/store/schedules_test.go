package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

var (
	schedT1 = time.Date(2026, 10, 31, 1, 0, 0, 0, time.UTC)
	schedT2 = schedT1.Add(time.Hour)
	schedT3 = schedT2.Add(time.Hour)
)

const shopKey = "postgresql:db:shop" // project.DatabaseKey of sampleProjects' Shop database

// storeAt0006 builds a database at migration 0006, runs fixture on it, closes
// it and returns its path, so Open applies 0007 to existing rows.
func storeAt0006(t *testing.T, fixture func(db *sql.DB)) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db}
	for i, name := range []string{
		"0001_init.sql", "0002_activity.sql", "0003_workspace_management.sql",
		"0004_project_settings.sql", "0005_backups.sql", "0006_database_name_explicit.sql",
	} {
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := old.applyMigration(ctx, i+1, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	fixture(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// readSchedule reads one schedule row straight from the table.
func readSchedule(t *testing.T, s *Store, projectID int64, key string) (Schedule, bool) {
	t.Helper()
	var (
		sc       Schedule
		lastRun  sql.NullString
		updated  string
		compress int
	)
	err := s.db.QueryRow(`SELECT project_id, db_key, frequency, run_time, weekday, keep, compress, last_run_at, updated_at
		FROM backup_schedules WHERE project_id = ? AND db_key = ?`, projectID, key).
		Scan(&sc.ProjectID, &sc.DBKey, &sc.Frequency, &sc.Time, &sc.Weekday, &sc.Keep, &compress, &lastRun, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	sc.Compress = compress != 0
	if lastRun.Valid {
		v, err := parseTime(lastRun.String)
		if err != nil {
			t.Fatalf("last_run_at %q: %v", lastRun.String, err)
		}
		sc.LastRunAt = &v
	}
	v, err := parseTime(updated)
	if err != nil {
		t.Fatalf("updated_at %q: %v", updated, err)
	}
	sc.UpdatedAt = v
	return sc, true
}

func mustSchedule(t *testing.T, s *Store, projectID int64, key string) Schedule {
	t.Helper()
	sc, ok := readSchedule(t, s, projectID, key)
	if !ok {
		t.Fatalf("no schedule for project %d %q", projectID, key)
	}
	return sc
}

func projectIDByPath(t *testing.T, s *Store, path string) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM projects WHERE path = ?`, path).Scan(&id); err != nil {
		t.Fatalf("project %s: %v", path, err)
	}
	return id
}

func TestMigrationSeedsOffSchedules(t *testing.T) {
	ctx := context.Background()
	const now = "2026-10-01T00:00:00Z"
	path := storeAt0006(t, func(db *sql.DB) {
		mustExec(t, db, `INSERT INTO workspaces (id, name, path, depth, created_at, last_scan_at) VALUES (1, 'apps', '/srv/apps', 2, ?, ?)`, now, now)
		mustExec(t, db, `INSERT INTO projects (id, workspace_id, path, compose_files, compose_project, display_name, state, self, details, created_at)
			VALUES (1, 1, '/srv/apps/ledger', '["compose.yaml"]', 'ledger', 'Ledger', 'tracked', 0, '{}', ?)`, now)
		mustExec(t, db, `INSERT INTO projects (id, workspace_id, path, compose_files, compose_project, display_name, state, self, details, created_at)
			VALUES (2, 1, '/srv/apps/blog', '["compose.yaml"]', 'blog', 'Blog', 'ignored', 0, '{}', ?)`, now)
		for _, q := range []string{
			`INSERT INTO databases (project_id, type, name, name_explicit, location_kind, location, sources) VALUES (1, 'postgresql', 'ledger', 1, 'container', 'pg', '[]')`,
			`INSERT INTO databases (project_id, type, name, name_explicit, location_kind, location, sources) VALUES (1, 'mysql', 'books', 1, 'container', 'maria', '[]')`,
			// Two detected databases can share a key: the seed keeps one row.
			`INSERT INTO databases (project_id, type, name, name_explicit, location_kind, location, sources) VALUES (1, 'mysql', 'books', 0, 'container', 'maria', '["DB_NAME"]')`,
			`INSERT INTO databases (project_id, type, name, name_explicit, location_kind, location, sources) VALUES (2, 'mysql', 'blog', 1, 'host', 'localhost', '[]')`,
		} {
			mustExec(t, db, q)
		}
	})

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open on a v6 database: %v", err)
	}
	defer func() { _ = s.Close() }()

	if n := count(t, s.db, `SELECT count(*) FROM schema_migrations`); n != 8 {
		t.Fatalf("schema_migrations has %d rows, want 8", n)
	}
	got, err := s.TrackedProject(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM backup_schedules WHERE project_id = 1`); n != 2 {
		t.Fatalf("ledger has %d schedule rows, want 2", n)
	}
	for _, d := range got.Databases {
		key := project.DatabaseKey(d)
		sc := mustSchedule(t, s, 1, key)
		if sc.Frequency != FrequencyOff || sc.Time != "02:00" || sc.Weekday != 0 || sc.Keep != 14 || !sc.Compress || sc.LastRunAt != nil || sc.UpdatedAt.IsZero() {
			t.Fatalf("seeded schedule %q = %+v", key, sc)
		}
	}
	if n := count(t, s.db, `SELECT count(*) FROM backup_schedules WHERE project_id = 2`); n != 0 {
		t.Fatalf("an ignored project has %d schedule rows", n)
	}

	def, err := s.BackupDefaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if def != (ScheduleFields{Frequency: FrequencyDaily, Time: "02:00", Weekday: 0, Keep: 14, Compress: true}) {
		t.Fatalf("defaults = %+v", def)
	}
	if n := count(t, s.db, `SELECT count(*) FROM pragma_table_info('backups') WHERE name = 'pruned_at'`); n != 1 {
		t.Fatal("backups.pruned_at is missing")
	}
	if n := count(t, s.db, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'backups_db_idx'`); n != 1 {
		t.Fatal("index backups_db_idx is missing")
	}
}

// The seed's db_key is project.DatabaseKey for a database whose name came
// from a variable or a URL (name_explicit), so the scheduler and the API find
// the seeded row.
func TestMigrationSeedKeyMatchesExplicitName(t *testing.T) {
	ctx := context.Background()
	const now = "2026-10-01T00:00:00Z"
	path := storeAt0006(t, func(db *sql.DB) {
		mustExec(t, db, `INSERT INTO workspaces (id, name, path, depth, created_at, last_scan_at) VALUES (1, 'apps', '/srv/apps', 2, ?, ?)`, now, now)
		mustExec(t, db, `INSERT INTO projects (id, workspace_id, path, compose_files, compose_project, display_name, state, self, details, created_at)
			VALUES (1, 1, '/srv/apps/shop', '["compose.yaml"]', 'shop', 'Shop', 'tracked', 0, '{}', ?)`, now)
		for _, q := range []string{
			// A name from a URL path, and one from a variable on a service named
			// like the placeholder.
			`INSERT INTO databases (project_id, type, name, name_explicit, location_kind, location, sources) VALUES (1, 'postgresql', 'orders', 1, 'container', 'pg', '["DATABASE_URL"]')`,
			`INSERT INTO databases (project_id, type, name, name_explicit, location_kind, location, sources) VALUES (1, 'mysql', 'db', 1, 'container', 'db', '["MYSQL_DATABASE"]')`,
		} {
			mustExec(t, db, q)
		}
	})
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open on a v6 database: %v", err)
	}
	defer func() { _ = s.Close() }()

	got, err := s.TrackedProject(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Databases) != 2 {
		t.Fatalf("databases = %+v", got.Databases)
	}
	want := map[string]bool{}
	for _, d := range got.Databases {
		if !d.NameExplicit {
			t.Fatalf("database %+v is not name_explicit", d)
		}
		want[project.DatabaseKey(d)] = true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT db_key FROM backup_schedules WHERE project_id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	seeded := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		seeded[k] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(seeded) != fmt.Sprint(want) {
		t.Fatalf("seeded keys = %v, want %v", seeded, want)
	}
	if !want["postgresql:pg:orders"] || !want["mysql:db:db"] {
		t.Fatalf("keys = %v", want)
	}
}

func TestDefaultsApplyToNewDatabasesOnly(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	d1 := ScheduleFields{Frequency: FrequencyWeekly, Time: "03:30", Weekday: 3, Keep: 7, Compress: false}
	if _, changed, err := s.SetBackupDefaults(ctx, d1, schedT1); err != nil || !changed {
		t.Fatalf("SetBackupDefaults = %v, %v", changed, err)
	}

	// First track.
	wsID, shopID, blogID := seedApps(t, s)
	if sc := mustSchedule(t, s, shopID, shopKey); sc.ScheduleFields != d1 || sc.LastRunAt != nil {
		t.Fatalf("first track: %+v", sc)
	}
	if n := count(t, s.db, `SELECT count(*) FROM backup_schedules WHERE project_id = ?`, blogID); n != 0 {
		t.Fatalf("ignored blog has %d rows", n)
	}

	// An AddProjects add, and an ignored add that gets nothing.
	if _, err := s.AddProjects(ctx, rescanJob(t, s), []AddItem{newItem(wsID, "/srv/apps/new", true), newItem(wsID, "/srv/apps/other", false)}); err != nil {
		t.Fatal(err)
	}
	newID := projectIDByPath(t, s, "/srv/apps/new")
	newKey := "sqlite:/srv/apps/new/x.db:x"
	if sc := mustSchedule(t, s, newID, newKey); sc.ScheduleFields != d1 {
		t.Fatalf("added: %+v", sc)
	}
	if n := count(t, s.db, `SELECT count(*) FROM backup_schedules WHERE project_id = ?`, projectIDByPath(t, s, "/srv/apps/other")); n != 0 {
		t.Fatalf("ignored add has %d rows", n)
	}

	// A re-track of the ignored blog.
	if _, err := s.AddProjects(ctx, rescanJob(t, s), []AddItem{newItem(wsID, "/srv/apps/blog", true)}); err != nil {
		t.Fatal(err)
	}
	if sc := mustSchedule(t, s, blogID, newKey); sc.ScheduleFields != d1 {
		t.Fatalf("re-tracked: %+v", sc)
	}

	// A rescan that finds a new database gets the defaults of that moment;
	// the existing row is left as it is.
	custom := ScheduleFields{Frequency: FrequencyDaily, Time: "05:00", Weekday: 0, Keep: 30, Compress: true}
	if _, _, err := s.SetSchedule(ctx, shopID, shopKey, custom, schedT2); err != nil {
		t.Fatal(err)
	}
	d2 := ScheduleFields{Frequency: FrequencyDaily, Time: "01:15", Weekday: 6, Keep: 2, Compress: true}
	if _, _, err := s.SetBackupDefaults(ctx, d2, schedT3); err != nil {
		t.Fatal(err)
	}
	shopDB := sampleProjects()[0].Databases[0]
	mongo := project.Database{Type: "mongodb", Name: "m", LocationKind: "container", Location: "mongo", Sources: []string{"service mongo"}}
	r := RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: rescanTime, Refresh: []ProjectRefresh{{
		Path:   "/srv/apps/shop",
		Config: &ConfigRefresh{ComposeFiles: []string{"compose.yaml"}, ComposeProject: "shop", Databases: []project.Database{shopDB, mongo}},
	}}}}}
	if err := s.ApplyRescan(ctx, r, rescanTime); err != nil {
		t.Fatal(err)
	}
	if sc := mustSchedule(t, s, shopID, "mongodb:mongo:m"); sc.ScheduleFields != d2 || !sc.UpdatedAt.Equal(rescanTime) {
		t.Fatalf("rescan found: %+v", sc)
	}
	if sc := mustSchedule(t, s, shopID, shopKey); sc.ScheduleFields != custom || !sc.UpdatedAt.Equal(schedT2) {
		t.Fatalf("existing row after rescan: %+v", sc)
	}
	// Existing rows elsewhere are untouched by the defaults change.
	if sc := mustSchedule(t, s, newID, newKey); sc.ScheduleFields != d1 {
		t.Fatalf("other project's row changed: %+v", sc)
	}
}

func TestSchedulesSurviveRescan(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	f := ScheduleFields{Frequency: FrequencyWeekly, Time: "04:00", Weekday: 2, Keep: 5, Compress: false}
	if _, _, err := s.SetSchedule(ctx, shopID, shopKey, f, schedT1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkScheduleRun(ctx, shopID, shopKey, schedT2); err != nil {
		t.Fatal(err)
	}
	refresh := func(dbs ...project.Database) {
		t.Helper()
		r := RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: rescanTime, Refresh: []ProjectRefresh{{
			Path:   "/srv/apps/shop",
			Config: &ConfigRefresh{ComposeFiles: []string{"compose.yaml"}, ComposeProject: "shop", Databases: dbs},
		}}}}}
		if err := s.ApplyRescan(ctx, r, laterTime); err != nil {
			t.Fatal(err)
		}
	}
	check := func(when string) {
		t.Helper()
		sc := mustSchedule(t, s, shopID, shopKey)
		if sc.ScheduleFields != f || sc.LastRunAt == nil || !sc.LastRunAt.Equal(schedT2) || !sc.UpdatedAt.Equal(schedT1) {
			t.Fatalf("%s: %+v", when, sc)
		}
	}
	shopDB := sampleProjects()[0].Databases[0]
	refresh(shopDB)
	check("same databases")
	refresh() // the database is gone from compose: the row stays
	check("database gone")
	refresh(shopDB)
	check("database back")
}

func TestUntrackDeletesSchedules(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	f := ScheduleFields{Frequency: FrequencyWeekly, Time: "04:00", Weekday: 2, Keep: 5, Compress: false}
	if _, _, err := s.SetSchedule(ctx, shopID, shopKey, f, schedT1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UntrackProject(ctx, shopID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM backup_schedules WHERE project_id = ?`, shopID); n != 0 {
		t.Fatalf("untracked project has %d rows", n)
	}

	// Re-tracking starts from the defaults.
	item := newItem(wsID, "/srv/apps/shop", true)
	item.Databases = sampleProjects()[0].Databases
	if _, err := s.AddProjects(ctx, rescanJob(t, s), []AddItem{item}); err != nil {
		t.Fatal(err)
	}
	def, err := s.BackupDefaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sc := mustSchedule(t, s, shopID, shopKey); sc.ScheduleFields != def {
		t.Fatalf("re-tracked: %+v, want the defaults %+v", sc, def)
	}

	// A missing project is deleted, its schedules by cascade.
	r := RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: rescanTime, Missing: []string{"/srv/apps/shop"}}}}
	if err := s.ApplyRescan(ctx, r, rescanTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UntrackProject(ctx, shopID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM backup_schedules`); n != 0 {
		t.Fatalf("%d schedule rows left after removing a missing project", n)
	}
}

func TestRemoveWorkspaceCascadesSchedules(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	if _, ok := readSchedule(t, s, shopID, shopKey); !ok {
		t.Fatal("no schedule row before the removal")
	}
	if _, err := s.RemoveWorkspace(ctx, wsID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM backup_schedules`); n != 0 {
		t.Fatalf("%d schedule rows left after removing the workspace", n)
	}
}

func TestSetScheduleNoChangeKeepsUpdatedAt(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	f := ScheduleFields{Frequency: FrequencyDaily, Time: "02:00", Weekday: 1, Keep: 9, Compress: true}
	if _, changed, err := s.SetSchedule(ctx, shopID, shopKey, f, schedT1); err != nil || !changed {
		t.Fatalf("first SetSchedule = %v, %v", changed, err)
	}
	before, changed, err := s.SetSchedule(ctx, shopID, shopKey, f, schedT2)
	if err != nil || changed {
		t.Fatalf("unchanged SetSchedule = %v, %v", changed, err)
	}
	if before.ScheduleFields != f || before.ProjectID != shopID || before.DBKey != shopKey || !before.UpdatedAt.Equal(schedT1) {
		t.Fatalf("before = %+v", before)
	}
	if sc := mustSchedule(t, s, shopID, shopKey); !sc.UpdatedAt.Equal(schedT1) {
		t.Fatalf("updated_at bumped to %v with no change", sc.UpdatedAt)
	}

	g := f
	g.Keep = 10
	before, changed, err = s.SetSchedule(ctx, shopID, shopKey, g, schedT3)
	if err != nil || !changed || before.ScheduleFields != f {
		t.Fatalf("changed SetSchedule = %+v, %v, %v", before, changed, err)
	}
	if sc := mustSchedule(t, s, shopID, shopKey); sc.ScheduleFields != g || !sc.UpdatedAt.Equal(schedT3) {
		t.Fatalf("after change: %+v", sc)
	}
}

func TestSetScheduleKeepsLastRunAt(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, blogID := seedApps(t, s)
	if err := s.MarkScheduleRun(ctx, shopID, shopKey, schedT1); err != nil {
		t.Fatal(err)
	}
	f := ScheduleFields{Frequency: FrequencyWeekly, Time: "23:59", Weekday: 6, Keep: 365, Compress: false}
	if _, changed, err := s.SetSchedule(ctx, shopID, shopKey, f, schedT2); err != nil || !changed {
		t.Fatalf("SetSchedule = %v, %v", changed, err)
	}
	sc := mustSchedule(t, s, shopID, shopKey)
	if sc.ScheduleFields != f || sc.LastRunAt == nil || !sc.LastRunAt.Equal(schedT1) || !sc.UpdatedAt.Equal(schedT2) {
		t.Fatalf("after SetSchedule: %+v", sc)
	}

	// An upsert on a key with no row creates it.
	before, changed, err := s.SetSchedule(ctx, shopID, "mysql:maria:books", f, schedT3)
	if err != nil || !changed || before.ProjectID != shopID || before.DBKey != "mysql:maria:books" || before.LastRunAt != nil {
		t.Fatalf("new key: %+v, %v, %v", before, changed, err)
	}
	if sc := mustSchedule(t, s, shopID, "mysql:maria:books"); sc.ScheduleFields != f || sc.LastRunAt != nil || !sc.UpdatedAt.Equal(schedT3) {
		t.Fatalf("new row: %+v", sc)
	}

	for _, id := range []int64{blogID, 999, 0} {
		if _, _, err := s.SetSchedule(ctx, id, shopKey, f, schedT3); !errors.Is(err, ErrNotFound) {
			t.Fatalf("SetSchedule(%d) = %v, want ErrNotFound", id, err)
		}
	}
	if err := s.MarkScheduleRun(ctx, shopID, "mysql:nope:x", schedT3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkScheduleRun on no row = %v, want ErrNotFound", err)
	}
}

func TestEnabledSchedulesSkipsOffMissingIgnoredAndOrphans(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, blogID := seedApps(t, s) // shop's row is daily from the defaults
	on := ScheduleFields{Frequency: FrequencyDaily, Time: "02:00", Weekday: 0, Keep: 14, Compress: true}

	// A second detected database with the same key is listed once.
	mustExec(t, s.db, `INSERT INTO databases (project_id, type, name, name_explicit, location_kind, location, sources)
		VALUES (?, 'postgresql', 'shop', 0, 'container', 'db', '[]')`, shopID)
	// An orphan: a schedule whose database is no longer detected.
	if _, _, err := s.SetSchedule(ctx, shopID, "postgresql:db:old", on, schedT1); err != nil {
		t.Fatal(err)
	}
	// An ignored project's row (never written by the panel, but never listed).
	mustExec(t, s.db, `INSERT INTO databases (project_id, type, name, name_explicit, location_kind, location, sources)
		VALUES (?, 'mysql', 'blog', 1, 'container', 'db', '[]')`, blogID)
	mustExec(t, s.db, `INSERT INTO backup_schedules (project_id, db_key, frequency, run_time, weekday, keep, compress, updated_at)
		VALUES (?, 'mysql:db:blog', 'daily', '02:00', 0, 14, 1, ?)`, blogID, formatTime(schedT1))

	// An off schedule.
	if _, err := s.AddProjects(ctx, rescanJob(t, s), []AddItem{newItem(wsID, "/srv/apps/off", true), newItem(wsID, "/srv/apps/gone", true)}); err != nil {
		t.Fatal(err)
	}
	offID := projectIDByPath(t, s, "/srv/apps/off")
	off := on
	off.Frequency = FrequencyOff
	if _, _, err := s.SetSchedule(ctx, offID, "sqlite:/srv/apps/new/x.db:x", off, schedT1); err != nil {
		t.Fatal(err)
	}
	// A missing project.
	r := RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: rescanTime, Missing: []string{"/srv/apps/gone"}}}}
	if err := s.ApplyRescan(ctx, r, rescanTime); err != nil {
		t.Fatal(err)
	}
	goneID := projectIDByPath(t, s, "/srv/apps/gone")
	if _, ok := readSchedule(t, s, goneID, "sqlite:/srv/apps/new/x.db:x"); !ok {
		t.Fatal("the missing project has no schedule row to skip")
	}

	got, err := s.EnabledSchedules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ProjectID != shopID || got[0].DBKey != shopKey || got[0].Frequency != FrequencyDaily || got[0].UpdatedAt.IsZero() {
		t.Fatalf("EnabledSchedules = %+v", got)
	}

	// Detected again, the orphan is listed again.
	mustExec(t, s.db, `INSERT INTO databases (project_id, type, name, name_explicit, location_kind, location, sources)
		VALUES (?, 'postgresql', 'old', 1, 'container', 'db', '[]')`, shopID)
	got, err = s.EnabledSchedules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, sc := range got {
		keys = append(keys, sc.DBKey)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"postgresql:db:old", shopKey}) {
		t.Fatalf("keys = %v", keys)
	}
}

func TestTrackedCarriesSchedules(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	f := ScheduleFields{Frequency: FrequencyWeekly, Time: "06:45", Weekday: 5, Keep: 3, Compress: false}
	if _, _, err := s.SetSchedule(ctx, shopID, shopKey, f, schedT1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkScheduleRun(ctx, shopID, shopKey, schedT2); err != nil {
		t.Fatal(err)
	}
	item := newItem(wsID, "/srv/apps/nodb", true)
	item.Databases = nil
	if _, err := s.AddProjects(ctx, rescanJob(t, s), []AddItem{item}); err != nil {
		t.Fatal(err)
	}
	noDB := projectIDByPath(t, s, "/srv/apps/nodb")

	one, err := s.TrackedProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.TrackedProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range append([]Tracked{one}, all...) {
		if tr.Schedules == nil {
			t.Fatalf("project %d has a nil Schedules map", tr.ID)
		}
		switch tr.ID {
		case shopID:
			sc, ok := tr.Schedules[project.DatabaseKey(tr.Databases[0])]
			if !ok || len(tr.Schedules) != 1 || sc.ScheduleFields != f || sc.ProjectID != shopID || sc.DBKey != shopKey ||
				sc.LastRunAt == nil || !sc.LastRunAt.Equal(schedT2) || !sc.UpdatedAt.Equal(schedT1) {
				t.Fatalf("shop schedules = %+v", tr.Schedules)
			}
		case noDB:
			if len(tr.Schedules) != 0 {
				t.Fatalf("project with no database has schedules %+v", tr.Schedules)
			}
		}
	}
}

func TestSetBackupDefaultsNoChange(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	def, err := s.BackupDefaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := ScheduleFields{Frequency: FrequencyDaily, Time: "02:00", Weekday: 0, Keep: 14, Compress: true}
	if def != want {
		t.Fatalf("defaults = %+v", def)
	}
	updatedAt := func() string {
		t.Helper()
		var v string
		if err := s.db.QueryRow(`SELECT updated_at FROM backup_defaults WHERE id = 1`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	seeded := updatedAt()
	if _, err := parseTime(seeded); err != nil {
		t.Fatalf("seeded updated_at %q: %v", seeded, err)
	}

	before, changed, err := s.SetBackupDefaults(ctx, want, schedT1)
	if err != nil || changed || before != want {
		t.Fatalf("unchanged SetBackupDefaults = %+v, %v, %v", before, changed, err)
	}
	if got := updatedAt(); got != seeded {
		t.Fatalf("updated_at %q, want the seeded %q", got, seeded)
	}

	next := ScheduleFields{Frequency: FrequencyOff, Time: "02:00", Weekday: 0, Keep: 14, Compress: true}
	before, changed, err = s.SetBackupDefaults(ctx, next, schedT2)
	if err != nil || !changed || before != want {
		t.Fatalf("changed SetBackupDefaults = %+v, %v, %v", before, changed, err)
	}
	if got := updatedAt(); got != formatTime(schedT2) {
		t.Fatalf("updated_at = %q, want %q", got, formatTime(schedT2))
	}
	if def, err := s.BackupDefaults(ctx); err != nil || def != next {
		t.Fatalf("defaults after set = %+v, %v", def, err)
	}
}
