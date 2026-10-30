package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

var (
	rescanTime = time.Date(2026, 10, 30, 9, 15, 0, 0, time.UTC)
	laterTime  = rescanTime.Add(time.Hour)
)

// seedApps stores /srv/apps with Shop (tracked, with a database) and Blog
// (ignored, with no stored databases), and returns the ids.
func seedApps(t *testing.T, s *Store) (wsID, shopID, blogID int64) {
	t.Helper()
	ctx := context.Background()
	wsID, _, err := s.AddWorkspace(ctx, NewWorkspace{Name: "apps", Path: "/srv/apps", Depth: 2, LastScanAt: scanTime}, sampleProjects())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM projects WHERE path = '/srv/apps/shop'`).Scan(&shopID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM projects WHERE path = '/srv/apps/blog'`).Scan(&blogID); err != nil {
		t.Fatal(err)
	}
	return wsID, shopID, blogID
}

func TestMigration0003OnPopulatedDB(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for v, name := range map[int]string{1: "0001_init.sql", 2: "0002_activity.sql"} {
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations VALUES (?, ?)`, v, formatTime(time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	now := formatTime(scanTime)
	for _, q := range []string{
		`INSERT INTO workspaces (id, name, path, depth, created_at, last_scan_at) VALUES (1, 'apps', '/srv/apps', 2, '` + now + `', '` + now + `')`,
		`INSERT INTO projects (id, workspace_id, path, compose_files, compose_project, display_name, state, self, details, created_at)
		 VALUES (1, 1, '/srv/apps/shop', '["compose.yaml"]', 'shop', 'Shop', 'tracked', 0, '{"services":[{"name":"web","image":"nginx","ports":[],"volumes":[]}]}', '` + now + `')`,
		`INSERT INTO activity (started_at, actor, actor_kind, action, target, result) VALUES ('` + now + `', 'a@b.c', 'person', 'scan.track', '/srv/apps', 'ok')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen with 0003 pending: %v", err)
	}
	defer func() { _ = s.Close() }()
	if n := count(t, s.db, `SELECT count(*) FROM schema_migrations`); n != 3 {
		t.Fatalf("schema_migrations has %d rows, want 3", n)
	}
	ts, err := s.TrackedProjects(ctx)
	if err != nil || len(ts) != 1 || ts[0].DisplayName != "Shop" || ts[0].MissingSince != nil {
		t.Fatalf("TrackedProjects = %+v, %v", ts, err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM activity`); n != 1 {
		t.Fatalf("activity rows = %d, want 1", n)
	}
	if n := count(t, s.db, `SELECT count(*) FROM pragma_table_info('projects') WHERE name = 'missing_since'`); n != 1 {
		t.Fatal("projects.missing_since is missing")
	}
}

func TestWorkspaceByID(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, _, _ := seedApps(t, s)
	w, err := s.WorkspaceByID(ctx, wsID)
	if err != nil || w.Path != "/srv/apps" || w.Tracked != 1 || w.Ignored != 1 {
		t.Fatalf("WorkspaceByID = %+v, %v", w, err)
	}
	if _, err := s.WorkspaceByID(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestRemoveWorkspaceCascadesKeepsActivity(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	if _, err := s.StartActivity(ctx, Activity{Actor: "a@b.c", ActorKind: ActorPerson, Action: ActScanTrack, ProjectID: shopID, Target: "/srv/apps"}); err != nil {
		t.Fatal(err)
	}
	w, err := s.RemoveWorkspace(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if w.Path != "/srv/apps" || w.Tracked != 1 || w.Ignored != 1 {
		t.Fatalf("returned workspace = %+v", w)
	}
	for _, table := range []string{"workspaces", "projects", "databases"} {
		if n := count(t, s.db, `SELECT count(*) FROM `+table); n != 0 {
			t.Fatalf("%d rows left in %s", n, table)
		}
	}
	if n := count(t, s.db, `SELECT count(*) FROM activity`); n != 1 {
		t.Fatalf("activity rows = %d, want 1", n)
	}
	if _, err := s.RemoveWorkspace(ctx, wsID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
}

func TestUntrackIgnoresTracked(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	got, err := s.UntrackProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != shopID || got.DisplayName != "Shop" || got.Path != "/srv/apps/shop" || len(got.Databases) != 1 {
		t.Fatalf("returned project = %+v", got)
	}
	var state string
	var missing sql.NullString
	if err := s.db.QueryRow(`SELECT state, missing_since FROM projects WHERE id = ?`, shopID).Scan(&state, &missing); err != nil {
		t.Fatal(err)
	}
	if state != "ignored" || missing.Valid {
		t.Fatalf("state = %q, missing = %v", state, missing)
	}
	if n := count(t, s.db, `SELECT count(*) FROM databases WHERE project_id = ?`, shopID); n != 0 {
		t.Fatalf("%d databases left", n)
	}
}

func TestUntrackDeletesMissing(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	if err := s.ApplyRescan(ctx, RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: rescanTime, Missing: []string{"/srv/apps/shop"}}}}, rescanTime); err != nil {
		t.Fatal(err)
	}
	got, err := s.UntrackProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MissingSince == nil || got.DisplayName != "Shop" {
		t.Fatalf("returned project = %+v", got)
	}
	if n := count(t, s.db, `SELECT count(*) FROM projects WHERE id = ?`, shopID); n != 0 {
		t.Fatal("missing project not deleted")
	}
	if n := count(t, s.db, `SELECT count(*) FROM databases`); n != 0 {
		t.Fatalf("%d databases left", n)
	}
}

func TestUntrackUnknownOrIgnored(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, blogID := seedApps(t, s)
	if _, err := s.UntrackProject(ctx, blogID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ignored: %v", err)
	}
	if _, err := s.UntrackProject(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.UntrackProject(ctx, shopID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UntrackProject(ctx, shopID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("already untracked: %v", err)
	}
}

func TestTrackedHasMissingSince(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	ts, err := s.TrackedProjects(ctx)
	if err != nil || len(ts) != 1 || ts[0].MissingSince != nil {
		t.Fatalf("before: %+v, %v", ts, err)
	}
	if err := s.ApplyRescan(ctx, RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: rescanTime, Missing: []string{"/srv/apps/shop"}}}}, rescanTime); err != nil {
		t.Fatal(err)
	}
	got, err := s.TrackedProject(ctx, shopID)
	if err != nil || got.MissingSince == nil || !got.MissingSince.Equal(rescanTime) {
		t.Fatalf("after: %+v, %v", got, err)
	}
	ts, err = s.TrackedProjects(ctx)
	if err != nil || len(ts) != 1 || ts[0].MissingSince == nil || !ts[0].MissingSince.Equal(rescanTime) {
		t.Fatalf("list after: %+v, %v", ts, err)
	}
}

func refreshOf(path string, services ...string) ProjectRefresh {
	var sv []project.Service
	for _, n := range services {
		sv = append(sv, project.Service{Name: n, Image: n + ":2"})
	}
	return ProjectRefresh{Path: path, Config: &ConfigRefresh{
		ComposeFiles:   []string{"compose.yml"},
		ComposeProject: "renamed",
		Details:        project.Details{Services: sv},
		Databases:      []project.Database{{Type: "mongodb", Name: "m", LocationKind: "container", Location: "mongo", Sources: []string{"service mongo"}}},
	}}
}

func TestApplyRescanRefreshKeepsName(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	if _, err := s.db.Exec(`UPDATE projects SET display_name = 'My Shop' WHERE id = ?`, shopID); err != nil {
		t.Fatal(err)
	}
	selfTrue := false
	r := refreshOf("/srv/apps/shop", "api", "cache")
	r.Self = &selfTrue
	apply := RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: laterTime, Refresh: []ProjectRefresh{r}, Found: []string{"/srv/apps/shop"}}}}
	if err := s.ApplyRescan(ctx, apply, laterTime); err != nil {
		t.Fatal(err)
	}
	got, err := s.TrackedProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "My Shop" || got.ComposeProject != "renamed" || got.Self || strings.Join(got.ComposeFiles, ",") != "compose.yml" {
		t.Fatalf("project = %+v", got)
	}
	if strings.Join(got.Services, ",") != "api,cache" || len(got.Databases) != 1 || got.Databases[0].Type != "mongodb" {
		t.Fatalf("services/databases = %v / %+v", got.Services, got.Databases)
	}
	w, err := s.WorkspaceByID(ctx, wsID)
	if err != nil || !w.LastScanAt.Equal(laterTime) {
		t.Fatalf("last scan = %v, %v", w.LastScanAt, err)
	}
}

func TestApplyRescanNilConfigKeeps(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	selfFalse := false
	apply := RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: laterTime, Refresh: []ProjectRefresh{{Path: "/srv/apps/shop", Self: &selfFalse}}, Found: []string{"/srv/apps/shop"}}}}
	if err := s.ApplyRescan(ctx, apply, laterTime); err != nil {
		t.Fatal(err)
	}
	got, err := s.TrackedProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ComposeProject != "shop" || strings.Join(got.ComposeFiles, ",") != "compose.yaml,compose.override.yaml" ||
		strings.Join(got.Services, ",") != "web,db" || len(got.Databases) != 1 || got.Databases[0].Name != "shop" {
		t.Fatalf("config changed: %+v", got)
	}
	if got.Self {
		t.Fatal("self not updated")
	}
}

func TestApplyRescanNilSelfKeeps(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s) // Shop is self
	apply := RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: laterTime, Refresh: []ProjectRefresh{refreshOf("/srv/apps/shop", "api")}}}}
	if err := s.ApplyRescan(ctx, apply, laterTime); err != nil {
		t.Fatal(err)
	}
	got, err := s.TrackedProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Self || got.ComposeProject != "renamed" {
		t.Fatalf("project = %+v", got)
	}
}

func TestApplyRescanMissingSetOnce(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	missing := func(at time.Time) {
		t.Helper()
		if err := s.ApplyRescan(ctx, RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: at, Missing: []string{"/srv/apps/shop"}}}}, at); err != nil {
			t.Fatal(err)
		}
	}
	missing(rescanTime)
	missing(laterTime)
	got, err := s.TrackedProject(ctx, shopID)
	if err != nil || got.MissingSince == nil || !got.MissingSince.Equal(rescanTime) {
		t.Fatalf("missing_since = %v, %v; want the first time %v", got.MissingSince, err, rescanTime)
	}
}

func TestApplyRescanFoundClears(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	if err := s.ApplyRescan(ctx, RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: rescanTime, Missing: []string{"/srv/apps/shop"}}}}, rescanTime); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyRescan(ctx, RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: laterTime, Found: []string{"/srv/apps/shop"}}}}, laterTime); err != nil {
		t.Fatal(err)
	}
	got, err := s.TrackedProject(ctx, shopID)
	if err != nil || got.MissingSince != nil {
		t.Fatalf("missing_since = %v, %v", got.MissingSince, err)
	}
}

func TestApplyRescanSkipsGoneWorkspace(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	apply := RescanApply{Workspaces: []WorkspaceApply{
		{ID: 999, LastScanAt: laterTime, Refresh: []ProjectRefresh{refreshOf("/srv/apps/shop", "x")}, Missing: []string{"/srv/apps/shop"}},
		{ID: wsID, LastScanAt: laterTime},
	}}
	if err := s.ApplyRescan(ctx, apply, laterTime); err != nil {
		t.Fatal(err)
	}
	got, err := s.TrackedProject(ctx, shopID)
	if err != nil || got.MissingSince != nil || got.ComposeProject != "shop" {
		t.Fatalf("project touched through a gone workspace: %+v, %v", got, err)
	}
	w, err := s.WorkspaceByID(ctx, wsID)
	if err != nil || !w.LastScanAt.Equal(laterTime) {
		t.Fatalf("the live workspace was not applied: %v, %v", w.LastScanAt, err)
	}
}

func TestApplyRescanOnlyTrackedRows(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, _, blogID := seedApps(t, s) // Blog is ignored
	apply := RescanApply{Workspaces: []WorkspaceApply{{
		ID: wsID, LastScanAt: laterTime,
		Refresh: []ProjectRefresh{refreshOf("/srv/apps/blog", "api")},
		Found:   []string{"/srv/apps/blog"},
		Missing: []string{"/srv/apps/blog"},
	}}}
	if err := s.ApplyRescan(ctx, apply, laterTime); err != nil {
		t.Fatal(err)
	}
	var proj string
	var missing sql.NullString
	if err := s.db.QueryRow(`SELECT compose_project, missing_since FROM projects WHERE id = ?`, blogID).Scan(&proj, &missing); err != nil {
		t.Fatal(err)
	}
	if proj != "blog" || missing.Valid {
		t.Fatalf("ignored row updated: %q, %v", proj, missing)
	}
	if n := count(t, s.db, `SELECT count(*) FROM databases WHERE project_id = ?`, blogID); n != 0 {
		t.Fatalf("%d databases written for an ignored project", n)
	}
}

func newItem(wsID int64, path string, tracked bool) AddItem {
	return AddItem{WorkspaceID: wsID, NewProject: NewProject{
		Path: path, ComposeFiles: []string{"compose.yaml"}, ComposeProject: "np", DisplayName: "NP", Tracked: tracked,
		Details:   project.Details{Services: []project.Service{{Name: "web", Image: "nginx"}}},
		Databases: []project.Database{{Type: "sqlite", Name: "x", LocationKind: "file", Location: "/srv/apps/new/x.db", Sources: []string{"SQLITE_PATH"}}},
	}}
}

func rescanJob(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.CreateScanJob(context.Background(), "all workspaces", 0, "a@b.c")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAddProjectsInsert(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, _, _ := seedApps(t, s)
	job := rescanJob(t, s)
	n, err := s.AddProjects(ctx, job, []AddItem{newItem(wsID, "/srv/apps/new", true), newItem(wsID, "/srv/apps/other", false)})
	if err != nil || n != 1 {
		t.Fatalf("AddProjects = %d, %v", n, err)
	}
	var state string
	var missing sql.NullString
	if err := s.db.QueryRow(`SELECT state, missing_since FROM projects WHERE path = '/srv/apps/new'`).Scan(&state, &missing); err != nil || state != "tracked" || missing.Valid {
		t.Fatalf("new project: %q, %v, %v", state, missing, err)
	}
	if err := s.db.QueryRow(`SELECT state FROM projects WHERE path = '/srv/apps/other'`).Scan(&state); err != nil || state != "ignored" {
		t.Fatalf("other project: %q, %v", state, err)
	}
	if c := count(t, s.db, `SELECT count(*) FROM databases d JOIN projects p ON p.id = d.project_id WHERE p.path = '/srv/apps/new'`); c != 1 {
		t.Fatalf("databases of new = %d", c)
	}
	if c := count(t, s.db, `SELECT count(*) FROM databases d JOIN projects p ON p.id = d.project_id WHERE p.path = '/srv/apps/other'`); c != 0 {
		t.Fatalf("databases of an ignored project = %d", c)
	}
}

func TestAddProjectsRetrackIgnored(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, _, blogID := seedApps(t, s)
	job := rescanJob(t, s)
	n, err := s.AddProjects(ctx, job, []AddItem{newItem(wsID, "/srv/apps/blog", true)})
	if err != nil || n != 1 {
		t.Fatalf("AddProjects = %d, %v", n, err)
	}
	if c := count(t, s.db, `SELECT count(*) FROM projects WHERE path = '/srv/apps/blog'`); c != 1 {
		t.Fatalf("%d rows for blog", c)
	}
	got, err := s.TrackedProject(ctx, blogID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "NP" || got.ComposeProject != "np" || strings.Join(got.Services, ",") != "web" ||
		len(got.Databases) != 1 || got.Databases[0].Type != "sqlite" || got.MissingSince != nil {
		t.Fatalf("retracked = %+v", got)
	}
}

func TestAddProjectsWorkspaceGone(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, _, _ := seedApps(t, s)
	job := rescanJob(t, s)
	_, err := s.AddProjects(ctx, job, []AddItem{newItem(wsID, "/srv/apps/new", true), newItem(999, "/srv/gone/x", true)})
	if !errors.Is(err, ErrWorkspaceGone) {
		t.Fatalf("err = %v, want ErrWorkspaceGone", err)
	}
	if c := count(t, s.db, `SELECT count(*) FROM projects WHERE path = '/srv/apps/new'`); c != 0 {
		t.Fatal("first item was kept after the failure")
	}
	assertNoMarker(t, s, job)
}

func TestAddProjectsAddedMeanwhile(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, _, _ := seedApps(t, s)
	job := rescanJob(t, s)
	_, err := s.AddProjects(ctx, job, []AddItem{newItem(wsID, "/srv/apps/new", true), newItem(wsID, "/srv/apps/shop", true)})
	if !errors.Is(err, ErrAddedMeanwhile) {
		t.Fatalf("err = %v, want ErrAddedMeanwhile", err)
	}
	if c := count(t, s.db, `SELECT count(*) FROM projects WHERE path = '/srv/apps/new'`); c != 0 {
		t.Fatal("first item was kept after the failure")
	}
	assertNoMarker(t, s, job)
}

func assertNoMarker(t *testing.T, s *Store, job int64) {
	t.Helper()
	var params string
	if err := s.db.QueryRow(`SELECT params FROM jobs WHERE id = ?`, job).Scan(&params); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(params, "tracked") {
		t.Fatalf("a failed add left a marker: %s", params)
	}
}

func TestAddProjectsSecondTrackRefused(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, _, _ := seedApps(t, s)
	job := rescanJob(t, s)
	if _, err := s.AddProjects(ctx, job, []AddItem{newItem(wsID, "/srv/apps/new", true)}); err != nil {
		t.Fatal(err)
	}
	var params string
	if err := s.db.QueryRow(`SELECT params FROM jobs WHERE id = ?`, job).Scan(&params); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(params, `"tracked":true`) || !strings.Contains(params, `"depth":0`) {
		t.Fatalf("params = %s, want the marker next to depth", params)
	}
	_, err := s.AddProjects(ctx, job, []AddItem{newItem(wsID, "/srv/apps/again", true)})
	if !errors.Is(err, ErrAlreadyTracked) {
		t.Fatalf("second track: %v, want ErrAlreadyTracked", err)
	}
	if c := count(t, s.db, `SELECT count(*) FROM projects WHERE path = '/srv/apps/again'`); c != 0 {
		t.Fatal("second track inserted a row")
	}
	if _, err := s.AddProjects(ctx, 9999, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown job: %v", err)
	}
}
