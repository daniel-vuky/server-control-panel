package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrateFreshAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "panel.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open on a missing directory: %v", err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM schema_migrations`); n != 4 {
		t.Fatalf("schema_migrations has %d rows, want 4", n)
	}
	var applied string
	if err := s.db.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version = 1`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, applied); err != nil {
		t.Fatalf("applied_at %q is not RFC 3339: %v", applied, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	if n := count(t, s.db, `SELECT count(*) FROM schema_migrations`); n != 4 {
		t.Fatalf("after reopen schema_migrations has %d rows, want 4", n)
	}
	for _, table := range []string{"workspaces", "projects", "databases", "jobs", "activity"} {
		if n := count(t, s.db, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table); n != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
}

func TestForeignKeysOnEveryConnection(t *testing.T) {
	s := open(t)
	s.db.SetMaxOpenConns(4)
	ctx := context.Background()

	var wg sync.WaitGroup
	opened := make(chan struct{}, 4)
	release := make(chan struct{})
	results := make([]int, 4)
	errs := make([]error, 4)
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := s.db.Conn(ctx)
			opened <- struct{}{}
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = conn.Close() }()
			// Hold the connection until all four are open so each one is distinct.
			<-release
			errs[i] = conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&results[i])
		}()
	}
	for range 4 {
		<-opened
	}
	close(release)
	wg.Wait()
	for i := range 4 {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if results[i] != 1 {
			t.Fatalf("connection %d has foreign_keys = %d", i, results[i])
		}
	}
}

func TestRunningJobsInterruptedOnOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	running, err := s.CreateScanJob(ctx, "/srv/apps", 2, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.CreateScanJob(ctx, "/srv/other", 1, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishJob(ctx, done, "done", json.RawMessage(`{"total":1}`), json.RawMessage(`{"rows":[]}`), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	j, err := s.Job(ctx, running)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != "interrupted" || j.FinishedAt == nil || j.Error == "" {
		t.Fatalf("running job after reopen = %+v", j)
	}
	if j.Kind != "scan" || j.Target != "/srv/apps" || j.Depth != 2 || j.StartedBy != "dev@localhost" {
		t.Fatalf("job fields = %+v", j)
	}
	j, err = s.Job(ctx, done)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != "done" || string(j.Result) != `{"rows":[]}` || string(j.Progress) != `{"total":1}` || j.FinishedAt == nil {
		t.Fatalf("done job changed: %+v", j)
	}
}

func TestJobLifecycle(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	id, err := s.CreateScanJob(ctx, "/srv/apps", 3, "a@b.c")
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.Job(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != "running" || j.FinishedAt != nil || j.Progress != nil || j.Result != nil || j.Error != "" || j.StartedAt.IsZero() || j.Depth != 3 {
		t.Fatalf("new job = %+v", j)
	}
	if err := s.FinishJob(ctx, id, "failed", nil, nil, "boom"); err != nil {
		t.Fatal(err)
	}
	j, _ = s.Job(ctx, id)
	if j.State != "failed" || j.Error != "boom" || j.FinishedAt == nil {
		t.Fatalf("finished job = %+v", j)
	}
	if _, err := s.Job(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Job(999) err = %v", err)
	}
	if err := s.FinishJob(ctx, 999, "done", nil, nil, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FinishJob(999) err = %v", err)
	}
}

var scanTime = time.Date(2026, 10, 29, 12, 30, 0, 0, time.UTC)

func sampleProjects() []NewProject {
	return []NewProject{
		{
			Path:           "/srv/apps/shop",
			ComposeFiles:   []string{"compose.yaml", "compose.override.yaml"},
			ComposeProject: "shop",
			DisplayName:    "Shop",
			Tracked:        true,
			Self:           true,
			Details: project.Details{Services: []project.Service{
				{Name: "web", Image: "nginx", Ports: []string{"8080:80/tcp"}, Volumes: []project.Volume{{Type: "bind", Source: "/srv/apps/shop/html", Target: "/usr/share/nginx/html"}}},
				{Name: "db", Image: "postgres:16"},
			}},
			Databases: []project.Database{
				{Type: "postgresql", Name: "shop", LocationKind: "container", Location: "db", Sources: []string{"service db", "DATABASE_URL"}},
			},
		},
		{
			Path:           "/srv/apps/blog",
			ComposeFiles:   []string{"compose.yml"},
			ComposeProject: "blog",
			DisplayName:    "Blog",
			Tracked:        false,
			Databases: []project.Database{
				{Type: "mysql", Name: "blog", LocationKind: "host", Location: "localhost", Sources: []string{"MYSQL_HOST"}},
			},
		},
	}
}

func TestAddWorkspaceStoresTrackedAndIgnored(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	id, added, err := s.AddWorkspace(ctx, NewWorkspace{Name: "apps", Path: "/srv/apps", Depth: 2, LastScanAt: scanTime}, sampleProjects())
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || id == 0 {
		t.Fatalf("id=%d added=%d, want a workspace id and added 1", id, added)
	}
	if n := count(t, s.db, `SELECT count(*) FROM projects`); n != 2 {
		t.Fatalf("%d projects stored", n)
	}
	if n := count(t, s.db, `SELECT count(*) FROM databases`); n != 1 {
		t.Fatalf("%d databases stored, want 1 (tracked project only)", n)
	}

	ws, err := s.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 {
		t.Fatalf("Workspaces = %+v", ws)
	}
	w := ws[0]
	if w.ID != id || w.Name != "apps" || w.Path != "/srv/apps" || w.Depth != 2 || w.Tracked != 1 || w.Ignored != 1 || !w.LastScanAt.Equal(scanTime) {
		t.Fatalf("workspace = %+v", w)
	}
	byPath, err := s.WorkspaceByPath(ctx, "/srv/apps")
	if err != nil {
		t.Fatal(err)
	}
	if byPath != w {
		t.Fatalf("WorkspaceByPath = %+v, want %+v", byPath, w)
	}
	if _, err := s.WorkspaceByPath(ctx, "/nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("WorkspaceByPath(/nope) err = %v", err)
	}

	tr, err := s.TrackedProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr) != 1 {
		t.Fatalf("TrackedProjects = %+v", tr)
	}
	p := tr[0]
	if p.WorkspaceID != id || p.DisplayName != "Shop" || p.Path != "/srv/apps/shop" || p.ComposeProject != "shop" || !p.Self {
		t.Fatalf("tracked = %+v", p)
	}
	if len(p.Services) != 2 || p.Services[0] != "web" || p.Services[1] != "db" {
		t.Fatalf("services = %v", p.Services)
	}
	if len(p.Databases) != 1 || p.Databases[0].Type != "postgresql" || p.Databases[0].Location != "db" ||
		len(p.Databases[0].Sources) != 2 || p.Databases[0].Sources[1] != "DATABASE_URL" {
		t.Fatalf("databases = %+v", p.Databases)
	}

	var files, details string
	if err := s.db.QueryRow(`SELECT compose_files, details FROM projects WHERE path = '/srv/apps/shop'`).Scan(&files, &details); err != nil {
		t.Fatal(err)
	}
	if files != `["compose.yaml","compose.override.yaml"]` {
		t.Fatalf("compose_files = %s", files)
	}
	var d project.Details
	if err := json.Unmarshal([]byte(details), &d); err != nil || len(d.Services) != 2 || d.Services[0].Ports[0] != "8080:80/tcp" {
		t.Fatalf("details = %s (%v)", details, err)
	}
	// The db service has no ports or volumes: they are stored as [], not null.
	if strings.Contains(details, "null") || !strings.Contains(details, `"name":"db","image":"postgres:16","ports":[],"volumes":[]`) {
		t.Fatalf("details has null arrays: %s", details)
	}
}

func TestEmptyDetailsStoredWithoutNulls(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	ps := []NewProject{{Path: "/srv/apps/broken", ComposeProject: "broken", DisplayName: "broken", Tracked: true}}
	if _, _, err := s.AddWorkspace(ctx, NewWorkspace{Name: "apps", Path: "/srv/apps", Depth: 1, LastScanAt: scanTime}, ps); err != nil {
		t.Fatal(err)
	}
	var details, files string
	if err := s.db.QueryRow(`SELECT details, compose_files FROM projects`).Scan(&details, &files); err != nil {
		t.Fatal(err)
	}
	if details != `{"services":[]}` || files != `[]` {
		t.Fatalf("details = %s, compose_files = %s", details, files)
	}
	tr, err := s.TrackedProjects(ctx)
	if err != nil || len(tr) != 1 || tr[0].Services == nil || len(tr[0].Services) != 0 || tr[0].Databases == nil {
		t.Fatalf("tracked = %+v, %v", tr, err)
	}
}

func TestTrackedProjectsDuringConcurrentAdds(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	const adds = 25
	done := make(chan error, 1)
	go func() {
		for i := range adds {
			path := fmt.Sprintf("/srv/w%d", i)
			ps := []NewProject{{
				Path: path + "/app", ComposeProject: fmt.Sprintf("app%d", i), DisplayName: "app", Tracked: true,
				Databases: []project.Database{{Type: "mysql", Name: "app", LocationKind: "container", Location: "db"}},
			}}
			if _, _, err := s.AddWorkspace(ctx, NewWorkspace{Name: "w", Path: path, Depth: 1, LastScanAt: scanTime}, ps); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for finished := false; !finished; {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			finished = true
		default:
		}
		tr, err := s.TrackedProjects(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range tr {
			if len(p.Databases) != 1 {
				t.Fatalf("project %s has %d databases, want 1 (torn read)", p.Path, len(p.Databases))
			}
		}
	}
	if tr, err := s.TrackedProjects(ctx); err != nil || len(tr) != adds {
		t.Fatalf("after all adds: %d projects, %v", len(tr), err)
	}
}

func TestAddWorkspaceTwiceIsAlreadyAdded(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	w := NewWorkspace{Name: "apps", Path: "/srv/apps", Depth: 1, LastScanAt: scanTime}
	if _, _, err := s.AddWorkspace(ctx, w, sampleProjects()); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.AddWorkspace(ctx, w, nil)
	if !errors.Is(err, ErrAlreadyAdded) {
		t.Fatalf("second AddWorkspace err = %v, want ErrAlreadyAdded", err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM workspaces`); n != 1 {
		t.Fatalf("%d workspaces", n)
	}
}

func TestAddWorkspaceRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	// Two projects with the same path break the unique constraint after the workspace row went in.
	ps := []NewProject{
		{Path: "/srv/apps/a", ComposeFiles: []string{"compose.yaml"}, ComposeProject: "a", DisplayName: "A", Tracked: true},
		{Path: "/srv/apps/a", ComposeFiles: []string{"compose.yaml"}, ComposeProject: "a", DisplayName: "A", Tracked: true},
	}
	_, _, err := s.AddWorkspace(ctx, NewWorkspace{Name: "apps", Path: "/srv/apps", Depth: 1, LastScanAt: scanTime}, ps)
	if err == nil || errors.Is(err, ErrAlreadyAdded) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM workspaces`) + count(t, s.db, `SELECT count(*) FROM projects`); n != 0 {
		t.Fatalf("%d rows left after a failed add", n)
	}
}

func TestAddWorkspaceRejectsBadDepth(t *testing.T) {
	s := open(t)
	for _, depth := range []int{0, 4} {
		if _, _, err := s.AddWorkspace(context.Background(), NewWorkspace{Name: "x", Path: "/x", Depth: depth, LastScanAt: scanTime}, nil); err == nil {
			t.Fatalf("depth %d accepted", depth)
		}
	}
}

func TestKnownProjects(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	known, err := s.KnownProjects(ctx)
	if err != nil || len(known) != 0 {
		t.Fatalf("empty store: %v, %v", known, err)
	}
	if _, _, err := s.AddWorkspace(ctx, NewWorkspace{Name: "apps", Path: "/srv/apps", Depth: 1, LastScanAt: scanTime}, sampleProjects()); err != nil {
		t.Fatal(err)
	}
	known, err = s.KnownProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Known{
		"/srv/apps/shop": {State: "tracked", ComposeProject: "shop"},
		"/srv/apps/blog": {State: "ignored", ComposeProject: "blog"},
	}
	if len(known) != len(want) {
		t.Fatalf("known = %+v", known)
	}
	for k, v := range want {
		if known[k] != v {
			t.Fatalf("known[%s] = %+v, want %+v", k, known[k], v)
		}
	}
}

func TestDeleteWorkspaceCascades(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	id, _, err := s.AddWorkspace(ctx, NewWorkspace{Name: "apps", Path: "/srv/apps", Depth: 1, LastScanAt: scanTime}, sampleProjects())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM workspaces WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"projects", "databases"} {
		if n := count(t, s.db, `SELECT count(*) FROM `+table); n != 0 {
			t.Fatalf("%d rows left in %s", n, table)
		}
	}
}

func TestTrackedProjectLookup(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, _, err := s.AddWorkspace(ctx, NewWorkspace{Name: "apps", Path: "/srv/apps", Depth: 2, LastScanAt: scanTime}, sampleProjects()); err != nil {
		t.Fatal(err)
	}
	var shopID, blogID int64
	if err := s.db.QueryRow(`SELECT id FROM projects WHERE path = '/srv/apps/shop'`).Scan(&shopID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM projects WHERE path = '/srv/apps/blog'`).Scan(&blogID); err != nil {
		t.Fatal(err)
	}

	got, err := s.TrackedProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != shopID || got.Path != "/srv/apps/shop" || got.DisplayName != "Shop" || got.ComposeProject != "shop" || !got.Self {
		t.Fatalf("TrackedProject = %+v", got)
	}
	if strings.Join(got.ComposeFiles, ",") != "compose.yaml,compose.override.yaml" {
		t.Fatalf("ComposeFiles = %v", got.ComposeFiles)
	}
	if strings.Join(got.Services, ",") != "web,db" || len(got.Databases) != 1 || got.Databases[0].Name != "shop" {
		t.Fatalf("services/databases = %v / %+v", got.Services, got.Databases)
	}

	all, err := s.TrackedProjects(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("TrackedProjects = %+v, %v", all, err)
	}
	if strings.Join(all[0].ComposeFiles, ",") != "compose.yaml,compose.override.yaml" {
		t.Fatalf("TrackedProjects ComposeFiles = %v", all[0].ComposeFiles)
	}

	if _, err := s.TrackedProject(ctx, blogID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ignored project: err = %v, want ErrNotFound", err)
	}
	if _, err := s.TrackedProject(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: err = %v, want ErrNotFound", err)
	}
}
