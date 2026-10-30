package scan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/roots"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// seedTime is the last_scan_at every seeded workspace starts with.
var seedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// tracked is a tracked project to seed, with one service named "old".
func trackedProject(path, name string) store.NewProject {
	return store.NewProject{
		Path: path, ComposeFiles: []string{"compose.yaml"}, ComposeProject: NormalizeProjectName(filepath.Base(path)),
		DisplayName: name, Tracked: true,
		Details: project.Details{Services: []project.Service{{Name: "old", Image: "busybox"}}},
	}
}

// seedWorkspace stores a workspace at root with the given projects and returns it.
func seedWorkspace(t *testing.T, st *store.Store, root string, ps ...store.NewProject) store.Workspace {
	t.Helper()
	ctx := context.Background()
	id, _, err := st.AddWorkspace(ctx, store.NewWorkspace{Name: filepath.Base(root), Path: root, Depth: 1, LastScanAt: seedTime}, ps)
	if err != nil {
		t.Fatal(err)
	}
	w, err := st.WorkspaceByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// runRescan rescans ws, waits for it and returns the stored job and result.
func runRescan(t *testing.T, m *Manager, ws ...store.Workspace) (store.Job, Result) {
	t.Helper()
	id, err := m.Rescan(context.Background(), ws, "dev@localhost")
	if err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	m.wait()
	return storedJob(t, m, id)
}

func storedJob(t *testing.T, m *Manager, id int64) (store.Job, Result) {
	t.Helper()
	job, err := m.st.Job(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var res Result
	if job.State == "done" {
		if err := json.Unmarshal(job.Result, &res); err != nil {
			t.Fatalf("result JSON: %v", err)
		}
	}
	return job, res
}

// trackedByPath returns the stored tracked projects keyed by path.
func trackedByPath(t *testing.T, st *store.Store) map[string]store.Tracked {
	t.Helper()
	ts, err := st.TrackedProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.Tracked{}
	for _, p := range ts {
		out[p.Path] = p
	}
	return out
}

func workspaceNow(t *testing.T, st *store.Store, id int64) store.Workspace {
	t.Helper()
	w, err := st.WorkspaceByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func hasWarning(res Result, want string) bool {
	return slices.Contains(res.Warnings, want)
}

func TestRescanTwoWorkspaces(t *testing.T) {
	rootA, rootB := tempRoot(t), tempRoot(t)
	mkfile(t, rootA, "shop/compose.yaml", "")
	mkfile(t, rootA, "fresh/compose.yaml", "")
	mkfile(t, rootA, "old/compose.yaml", "")
	mkdir(t, rootA, "plain")
	mkfile(t, rootB, "blog/compose.yaml", "")
	mkfile(t, rootB, "extra/compose.yaml", "")
	shop, gone, old := filepath.Join(rootA, "shop"), filepath.Join(rootA, "gone"), filepath.Join(rootA, "old")
	fresh, blog, extra := filepath.Join(rootA, "fresh"), filepath.Join(rootB, "blog"), filepath.Join(rootB, "extra")

	fc := newFakeCmd()
	fc.gate = make(chan struct{})
	fc.containers = containerLine("fresh", "app", "running", "Up", "")
	m, st := newTestManager(t, fc)
	m.interval = 0
	ignored := trackedProject(old, "Old")
	ignored.Tracked = false
	wa := seedWorkspace(t, st, rootA, trackedProject(shop, "My Shop"), trackedProject(gone, "Gone"), ignored)
	wb := seedWorkspace(t, st, rootB, trackedProject(blog, "Blog"))

	id, err := m.Rescan(context.Background(), []store.Workspace{wa, wb}, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	_, events, cancel, live := m.Subscribe(id)
	defer cancel()
	if !live {
		t.Fatal("a running rescan is not live")
	}
	close(fc.gate)
	evs := collect(t, events)
	m.wait()
	seen := map[[2]int]bool{}
	for _, ev := range evs {
		if ev.Kind == EventProgress {
			seen[[2]int{ev.Progress.Workspace, ev.Progress.Workspaces}] = true
		}
	}
	if !seen[[2]int{1, 2}] || !seen[[2]int{2, 2}] {
		t.Fatalf("progress workspace indices %v, want 1/2 and 2/2", seen)
	}

	job, res := storedJob(t, m, id)
	if job.State != "done" || job.Target != "all workspaces" || job.Depth != 0 || !reflect.DeepEqual(job.Rescan, []int64{wa.ID, wb.ID}) {
		t.Fatalf("job %+v", job)
	}
	var final Progress
	if err := json.Unmarshal(job.Progress, &final); err != nil {
		t.Fatal(err)
	}
	if final.Workspace != 2 || final.Workspaces != 2 {
		t.Fatalf("final progress %+v", final)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("rows %+v, want fresh, old and extra", res.Rows)
	}
	if r := rowByPath(t, res, fresh); !r.New || r.Ignored || r.WorkspaceID != wa.ID || r.Status != project.StatusRunning {
		t.Fatalf("fresh row %+v", r)
	}
	if r := rowByPath(t, res, old); r.New || !r.Ignored || r.WorkspaceID != wa.ID || r.Warning != "" {
		t.Fatalf("old row %+v", r)
	}
	if r := rowByPath(t, res, extra); !r.New || r.WorkspaceID != wb.ID {
		t.Fatalf("extra row %+v", r)
	}
	if res.New != 2 || res.IgnoredFound != 1 || res.Refreshed != 2 || res.NotFound != 1 ||
		!reflect.DeepEqual(res.NotFoundPaths, []string{gone}) || !reflect.DeepEqual(res.Workspaces, []string{rootA, rootB}) {
		t.Fatalf("result counts %+v", res)
	}
	if res.AlreadyTracked != 0 || res.Ignored != 0 {
		t.Fatalf("first-scan counts set on a rescan: %+v", res)
	}

	ts := trackedByPath(t, st)
	if ts[gone].MissingSince == nil {
		t.Fatal("gone is not marked missing")
	}
	for _, p := range []string{shop, blog} {
		if ts[p].MissingSince != nil || !reflect.DeepEqual(ts[p].Services, []string{"app"}) {
			t.Fatalf("%s not refreshed: %+v", p, ts[p])
		}
	}
	if _, ok := ts[fresh]; ok {
		t.Fatal("a new row was stored before the owner chose")
	}
	for _, w := range []store.Workspace{wa, wb} {
		if !workspaceNow(t, st, w.ID).LastScanAt.After(seedTime) {
			t.Fatalf("workspace %s last_scan_at not updated", w.Path)
		}
	}
}

func TestRescanKeepsName(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "shop/compose.yaml", "")
	shop := filepath.Join(root, "shop")
	fc := newFakeCmd()
	fc.configs[shop] = fakeReply{stdout: configJSON("shop2", map[string]string{"web": "nginx", "db": "postgres:16"})}
	m, st := newTestManager(t, fc)
	w := seedWorkspace(t, st, root, trackedProject(shop, "My Shop"))

	job, res := runRescan(t, m, w)
	if job.State != "done" || job.Target != root || !reflect.DeepEqual(job.Rescan, []int64{w.ID}) {
		t.Fatalf("job %+v", job)
	}
	if res.Refreshed != 1 || len(res.Rows) != 0 {
		t.Fatalf("result %+v", res)
	}
	p := trackedByPath(t, st)[shop]
	if p.DisplayName != "My Shop" || p.ComposeProject != "shop2" || !reflect.DeepEqual(p.Services, []string{"db", "web"}) ||
		len(p.Databases) != 1 {
		t.Fatalf("refreshed project %+v", p)
	}
}

func TestRescanFolderReturnClearsMissing(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "keep/compose.yaml", "")
	app := filepath.Join(root, "app")
	m, st := newTestManager(t, newFakeCmd())
	w := seedWorkspace(t, st, root, trackedProject(app, "App"), trackedProject(filepath.Join(root, "keep"), "Keep"))

	_, res := runRescan(t, m, w)
	if res.NotFound != 1 || trackedByPath(t, st)[app].MissingSince == nil {
		t.Fatalf("app not missing after the first rescan: %+v", res)
	}
	mkfile(t, root, "app/compose.yaml", "")
	_, res = runRescan(t, m, w)
	if res.NotFound != 0 || len(res.NotFoundPaths) != 0 || res.Refreshed != 2 {
		t.Fatalf("second rescan %+v", res)
	}
	if trackedByPath(t, st)[app].MissingSince != nil {
		t.Fatal("the returning folder is still missing")
	}
}

// A project already missing stays counted as not found, but is not newly marked.
func TestRescanStillMissingCountedNotNew(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "keep/compose.yaml", "")
	app := filepath.Join(root, "app")
	m, st := newTestManager(t, newFakeCmd())
	w := seedWorkspace(t, st, root, trackedProject(app, "App"), trackedProject(filepath.Join(root, "keep"), "Keep"))
	runRescan(t, m, w)
	first := trackedByPath(t, st)[app].MissingSince
	_, res := runRescan(t, m, w)
	if res.NotFound != 1 || len(res.NotFoundPaths) != 0 {
		t.Fatalf("second rescan %+v, want 1 not found and none newly marked", res)
	}
	if again := trackedByPath(t, st)[app].MissingSince; again == nil || !again.Equal(*first) {
		t.Fatalf("missing_since %v, want kept at %v", again, first)
	}
}

func TestRescanEmptyRootMarksNothing(t *testing.T) {
	root := tempRoot(t)
	mkdir(t, root, "lost+found")
	a := filepath.Join(root, "a")
	m, st := newTestManager(t, newFakeCmd())
	w := seedWorkspace(t, st, root, trackedProject(a, "A"))

	job, res := runRescan(t, m, w)
	if job.State != "done" {
		t.Fatalf("job %+v", job)
	}
	want := "Skipped " + root + ": it holds no compose project now; is it mounted?"
	if !hasWarning(res, want) {
		t.Fatalf("warnings %q, want %q", res.Warnings, want)
	}
	if res.NotFound != 0 || !reflect.DeepEqual(res.Workspaces, []string{root}) {
		t.Fatalf("result %+v", res)
	}
	if trackedByPath(t, st)[a].MissingSince != nil {
		t.Fatal("a project was marked missing under an empty root")
	}
	if !workspaceNow(t, st, w.ID).LastScanAt.Equal(seedTime) {
		t.Fatal("last_scan_at updated for a skipped workspace")
	}
}

// An empty root with no tracked project is not a missing mount: it is scanned.
func TestRescanEmptyRootWithoutTrackedIsScanned(t *testing.T) {
	root := tempRoot(t)
	m, st := newTestManager(t, newFakeCmd())
	w := seedWorkspace(t, st, root)
	_, res := runRescan(t, m, w)
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %q", res.Warnings)
	}
	if !workspaceNow(t, st, w.ID).LastScanAt.After(seedTime) {
		t.Fatal("last_scan_at not updated")
	}
}

func TestRescanMissingRootMarksNothing(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "a/compose.yaml", "")
	a := filepath.Join(root, "a")
	m, st := newTestManager(t, newFakeCmd())
	w := seedWorkspace(t, st, root, trackedProject(a, "A"))
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	job, res := runRescan(t, m, w)
	if job.State != "done" {
		t.Fatalf("job %+v", job)
	}
	want := "Skipped " + root + ": the folder is missing or cannot be read."
	if !hasWarning(res, want) {
		t.Fatalf("warnings %q, want %q", res.Warnings, want)
	}
	if res.NotFound != 0 || res.Refreshed != 0 || !reflect.DeepEqual(res.Workspaces, []string{root}) {
		t.Fatalf("result %+v", res)
	}
	if trackedByPath(t, st)[a].MissingSince != nil {
		t.Fatal("a project was marked missing under a missing root")
	}
	if !workspaceNow(t, st, w.ID).LastScanAt.Equal(seedTime) {
		t.Fatal("last_scan_at updated for a skipped workspace")
	}
}

// fakeGuard answers Check from a map; a path not in it resolves to itself.
type fakeGuard map[string]struct {
	resolved string
	err      error
}

func (g fakeGuard) Check(path string) (string, error) {
	if r, ok := g[path]; ok {
		return r.resolved, r.err
	}
	return path, nil
}

func TestRescanGuardFailureWarns(t *testing.T) {
	outside, swapped, ok := tempRoot(t), tempRoot(t), tempRoot(t)
	for _, r := range []string{outside, swapped, ok} {
		mkfile(t, r, "new/compose.yaml", "")
		mkfile(t, r, "keep/compose.yaml", "")
	}
	m, st := newTestManager(t, newFakeCmd())
	m.SetGuard(fakeGuard{
		outside: {err: &roots.PathError{Code: roots.CodeOutsideRoots, Message: "This folder is outside the allowed folders (/srv)."}},
		swapped: {resolved: "/elsewhere"},
	})
	var ws []store.Workspace
	for _, r := range []string{outside, swapped, ok} {
		ws = append(ws, seedWorkspace(t, st, r,
			trackedProject(filepath.Join(r, "gone"), "Gone"), trackedProject(filepath.Join(r, "keep"), "Keep")))
	}

	job, res := runRescan(t, m, ws...)
	if job.State != "done" {
		t.Fatalf("job %+v", job)
	}
	for _, want := range []string{
		"Skipped " + outside + ": This folder is outside the allowed folders (/srv).",
		"Skipped " + swapped + ": it now resolves to /elsewhere.",
	} {
		if !hasWarning(res, want) {
			t.Fatalf("warnings %q, want %q", res.Warnings, want)
		}
	}
	if !reflect.DeepEqual(res.Workspaces, []string{outside, swapped, ok}) {
		t.Fatalf("workspaces %q", res.Workspaces)
	}
	if len(res.Rows) != 1 || res.Rows[0].Path != filepath.Join(ok, "new") {
		t.Fatalf("rows %+v, want only the allowed workspace's new folder", res.Rows)
	}
	if res.NotFound != 1 || res.Refreshed != 1 {
		t.Fatalf("result %+v", res)
	}
	ts := trackedByPath(t, st)
	for _, r := range []string{outside, swapped} {
		if ts[filepath.Join(r, "gone")].MissingSince != nil {
			t.Fatalf("%s: a project was marked missing for a skipped workspace", r)
		}
	}
	if ts[filepath.Join(ok, "gone")].MissingSince == nil {
		t.Fatal("the allowed workspace's gone project is not missing")
	}
	if !workspaceNow(t, st, ws[0].ID).LastScanAt.Equal(seedTime) || !workspaceNow(t, st, ws[1].ID).LastScanAt.Equal(seedTime) {
		t.Fatal("last_scan_at updated for a skipped workspace")
	}
}

// A single-workspace rescan refused by the guard still runs and reports it.
func TestRescanGuardSingleWorkspaceStillRuns(t *testing.T) {
	root := tempRoot(t)
	m, st := newTestManager(t, newFakeCmd())
	m.SetGuard(fakeGuard{root: {err: &roots.PathError{Code: roots.CodeNotFound, Message: "This folder does not exist."}}})
	w := seedWorkspace(t, st, root)
	job, res := runRescan(t, m, w)
	if job.State != "done" || !hasWarning(res, "Skipped "+root+": This folder does not exist.") {
		t.Fatalf("job %+v result %+v", job, res)
	}
}

func TestRescanKeepsSelfWhenDockerDown(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "panel/compose.yaml", "")
	panel := filepath.Join(root, "panel")
	fc := newFakeCmd()
	fc.configs[panel] = fakeReply{stdout: configJSON("scp", map[string]string{"panel": "ghcr.io/x/panel"})}
	fc.containersErr = errors.New("cannot connect to the Docker daemon")
	m, st := newTestManager(t, fc)
	p := trackedProject(panel, "Panel")
	p.ComposeProject, p.Self = "scp", true
	w := seedWorkspace(t, st, root, p)

	runRescan(t, m, w)
	got := trackedByPath(t, st)[panel]
	if !got.Self {
		t.Fatal("the panel's own project lost self while Docker was down")
	}
	if !reflect.DeepEqual(got.Services, []string{"panel"}) {
		t.Fatalf("config not refreshed: %+v", got)
	}

	// With Docker up and no panel container, self is read and cleared.
	fc.containersErr = nil
	fc.containers = containerLine("scp", "panel", "running", "Up", "")
	runRescan(t, m, w)
	if trackedByPath(t, st)[panel].Self {
		t.Fatal("self not updated from a successful container read")
	}
}

func TestRescanKeepsDetailsWhenConfigFails(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "panel/compose.yaml", "")
	panel := filepath.Join(root, "panel")
	fc := newFakeCmd()
	fc.configs[panel] = fakeReply{err: errors.New("yaml: line 3: mapping values are not allowed\nmore")}
	fc.containers = containerLine("scp", "panel", "running", "Up", "panel")
	m, st := newTestManager(t, fc)
	p := trackedProject(panel, "Panel")
	p.ComposeProject, p.Self = "scp", true
	p.Databases = []project.Database{{Type: "postgresql", Name: "db", LocationKind: "container", Location: "db", Sources: []string{"service db"}}}
	w := seedWorkspace(t, st, root, p)

	_, res := runRescan(t, m, w)
	want := "Could not refresh " + panel + ": yaml: line 3: mapping values are not allowed"
	if !hasWarning(res, want) {
		t.Fatalf("warnings %q, want %q", res.Warnings, want)
	}
	got := trackedByPath(t, st)[panel]
	if !reflect.DeepEqual(got.Services, []string{"old"}) || got.ComposeProject != "scp" || len(got.Databases) != 1 {
		t.Fatalf("stored config lost: %+v", got)
	}
	if !got.Self {
		t.Fatal("self compared against a guessed name and lost")
	}
	if got.MissingSince != nil || res.Refreshed != 1 {
		t.Fatalf("a present project with a failing config: %+v, %+v", got, res)
	}
}

func TestRescanNestedRootsOneRow(t *testing.T) {
	outer := tempRoot(t)
	inner := filepath.Join(outer, "team")
	mkfile(t, inner, "app/compose.yaml", "")
	app := filepath.Join(inner, "app")
	m, st := newTestManager(t, newFakeCmd())
	wo, err := func() (store.Workspace, error) {
		id, _, err := st.AddWorkspace(context.Background(),
			store.NewWorkspace{Name: "outer", Path: outer, Depth: 2, LastScanAt: seedTime}, nil)
		if err != nil {
			return store.Workspace{}, err
		}
		return st.WorkspaceByID(context.Background(), id)
	}()
	if err != nil {
		t.Fatal(err)
	}
	wi := seedWorkspace(t, st, inner)

	_, res := runRescan(t, m, wo, wi)
	if len(res.Rows) != 1 || res.New != 1 {
		t.Fatalf("rows %+v, want app once", res.Rows)
	}
	if r := rowByPath(t, res, app); r.WorkspaceID != wi.ID {
		t.Fatalf("app row owned by %d, want the inner workspace %d", r.WorkspaceID, wi.ID)
	}
	if n := len(fakeOf(m).callsOf(runner.ComposeConfig)); n != 1 {
		t.Fatalf("app examined %d times", n)
	}

	// When the inner workspace is skipped, the outer one owns the row.
	m.SetGuard(fakeGuard{inner: {err: errors.New("gone")}})
	_, res = runRescan(t, m, wo, wi)
	if len(res.Rows) != 1 || res.Rows[0].WorkspaceID != wo.ID {
		t.Fatalf("rows %+v, want app under the outer workspace", res.Rows)
	}
}

// fakeOf returns the manager's fake commander.
func fakeOf(m *Manager) *fakeCmd { return m.cmd.(*fakeCmd) }

func TestRescanBusySharedWithStart(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "a/compose.yaml", "")
	cmd := newFakeCmd()
	cmd.gate = make(chan struct{})
	m, st := newTestManager(t, cmd)
	w := seedWorkspace(t, st, root)

	id, err := m.Rescan(context.Background(), []store.Workspace{w}, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Start(context.Background(), root, 1, "dev@localhost")
	var busy *BusyError
	if !errors.As(err, &busy) || *busy != (BusyError{ID: id, Path: root, Rescan: true}) {
		close(cmd.gate)
		t.Fatalf("Start during a rescan: %v", err)
	}
	if _, err := m.Rescan(context.Background(), []store.Workspace{w}, "dev@localhost"); !errors.As(err, &busy) || busy.ID != id {
		close(cmd.gate)
		t.Fatalf("Rescan during a rescan: %v", err)
	}
	close(cmd.gate)
	m.wait()

	cmd.gate = make(chan struct{})
	sid, err := m.Start(context.Background(), root, 1, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Rescan(context.Background(), []store.Workspace{w}, "dev@localhost"); !errors.As(err, &busy) ||
		*busy != (BusyError{ID: sid, Path: root, Depth: 1}) {
		close(cmd.gate)
		t.Fatalf("Rescan during a scan: %v", err)
	}
	close(cmd.gate)
}

func TestRescanNeedsWorkspaces(t *testing.T) {
	m, _ := newTestManager(t, newFakeCmd())
	if _, err := m.Rescan(context.Background(), nil, "dev@localhost"); err == nil {
		t.Fatal("Rescan of no workspace accepted")
	}
}

func TestWhileIdleBlocksStart(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "a/compose.yaml", "")
	cmd := newFakeCmd()
	m, st := newTestManager(t, cmd)
	w := seedWorkspace(t, st, root)

	// While fn runs, no job can start.
	inside, release := make(chan struct{}), make(chan struct{})
	idleDone := make(chan error, 1)
	go func() {
		idleDone <- m.WhileIdle(func() error {
			close(inside)
			<-release
			return errors.New("fn result")
		})
	}()
	<-inside
	started := make(chan error, 1)
	go func() {
		_, err := m.Rescan(context.Background(), []store.Workspace{w}, "dev@localhost")
		started <- err
	}()
	select {
	case err := <-started:
		t.Fatalf("Rescan returned while WhileIdle ran fn: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-idleDone; err == nil || err.Error() != "fn result" {
		t.Fatalf("WhileIdle = %v, want fn's error", err)
	}
	if err := <-started; err != nil {
		t.Fatalf("Rescan after WhileIdle: %v", err)
	}
	m.wait()

	// While a job runs, WhileIdle refuses and does not call fn.
	cmd.gate = make(chan struct{})
	id, err := m.Start(context.Background(), root, 1, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = m.WhileIdle(func() error { called = true; return nil })
	close(cmd.gate)
	var busy *BusyError
	if !errors.As(err, &busy) || busy.ID != id || called {
		t.Fatalf("WhileIdle during a scan = %v, called %v", err, called)
	}
	m.wait()
	if err := m.WhileIdle(func() error { return nil }); err != nil {
		t.Fatalf("WhileIdle when idle: %v", err)
	}
}

func TestRescanActivity(t *testing.T) {
	rootA, rootB := tempRoot(t), tempRoot(t)
	m, st := newTestManager(t, newFakeCmd())
	wa, wb := seedWorkspace(t, st, rootA), seedWorkspace(t, st, rootB)
	runRescan(t, m, wa)
	runRescan(t, m, wa, wb)
	rows, err := st.RecentActivity(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("activity rows %+v", rows)
	}
	for i, target := range []string{"all workspaces", rootA} { // newest first
		r := rows[i]
		if r.Action != store.ActWorkspaceRescan || r.Result != store.ResultOK || r.Actor != "dev@localhost" ||
			r.ActorKind != store.ActorPerson || r.Target != target || r.FinishedAt == nil {
			t.Fatalf("activity row %d %+v", i, r)
		}
	}
}

func TestRescanFailureRecordsActivity(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "p/compose.yaml", "")
	cmd := newFakeCmd()
	cmd.panicOn = runner.ComposeConfig
	m, st := newTestManager(t, cmd)
	w := seedWorkspace(t, st, root)
	var changes atomic.Int32
	m.SetOnChange(func() { changes.Add(1) })
	job, _ := runRescan(t, m, w)
	if job.State != "failed" {
		t.Fatalf("job %+v", job)
	}
	rows, err := st.RecentActivity(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Action != store.ActWorkspaceRescan || rows[0].Result != store.ResultFailed {
		t.Fatalf("activity %+v", rows)
	}
	if changes.Load() != 0 {
		t.Fatal("onChange called for a failed rescan")
	}
}

func TestRescanOnChangeCalledAfterApply(t *testing.T) {
	root := tempRoot(t)
	a := filepath.Join(root, "a")
	mkfile(t, root, "b/compose.yaml", "")
	m, st := newTestManager(t, newFakeCmd())
	w := seedWorkspace(t, st, root, trackedProject(a, "A"))
	var (
		calls       atomic.Int32
		sawMissing  atomic.Bool
		jobFinished atomic.Bool
	)
	var jobID atomic.Int64
	m.SetOnChange(func() {
		calls.Add(1)
		ts, err := st.TrackedProjects(context.Background())
		sawMissing.Store(err == nil && len(ts) == 1 && ts[0].MissingSince != nil)
		if j, err := st.Job(context.Background(), jobID.Load()); err == nil && j.State != "running" {
			jobFinished.Store(true)
		}
	})
	id, err := m.Rescan(context.Background(), []store.Workspace{w}, "dev@localhost")
	if err != nil {
		t.Fatal(err)
	}
	jobID.Store(id)
	m.wait()
	if calls.Load() != 1 || !sawMissing.Load() {
		t.Fatalf("onChange calls %d, saw the apply %v", calls.Load(), sawMissing.Load())
	}
	if jobFinished.Load() {
		t.Fatal("onChange ran after FinishJob; the apply must land before the job is done")
	}
}

func TestRescanResultRoundTripsJSON(t *testing.T) {
	res := Result{
		Rows:          []Row{{Path: "/a", WorkspaceID: 3, New: true, Ignored: false}},
		New:           1,
		IgnoredFound:  2,
		Refreshed:     3,
		NotFound:      4,
		NotFoundPaths: []string{"/x"},
		Workspaces:    []string{"/a"},
		Warnings:      []string{},
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"workspaceId":3`, `"new":true`, `"ignored":false`, `"ignoredFound":2`, `"refreshed":3`, `"notFound":4`, `"notFoundPaths":["/x"]`, `"workspaces":["/a"]`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("%s lacks %s", b, key)
		}
	}
	b, err = json.Marshal(Progress{Workspace: 1, Workspaces: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"workspace":1`) || !strings.Contains(string(b), `"workspaces":2`) {
		t.Fatalf("progress %s", b)
	}
}
