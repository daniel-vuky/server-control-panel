package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// workspaceID returns the id of the stored workspace at path.
func workspaceID(t *testing.T, d Deps, path string) int64 {
	t.Helper()
	w, err := d.Store.WorkspaceByPath(context.Background(), path)
	if err != nil {
		t.Fatalf("workspace %s: %v", path, err)
	}
	return w.ID
}

func removeURL(id int64) string { return "/api/workspaces/" + strconv.FormatInt(id, 10) + "/remove" }

func untrackURL(id int64) string { return actionURL(id, "untrack") }

// rescanSetup stores the workspace ws from a first scan, with a tracked and b
// ignored, then adds the new compose folders c and e.
func rescanSetup(t *testing.T) (d Deps, h http.Handler, ws string) {
	t.Helper()
	d = newTestDeps(t)
	h = NewRouter(d)
	ws = filepath.Join(rootOf(d), "ws")
	mkProject(t, filepath.Join(ws, "a"), "compose.yaml")
	mkProject(t, filepath.Join(ws, "b"), "compose.yaml")
	id, _ := scanDone(t, h, ws, 1)
	rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), TrackRequest{Projects: []TrackItem{
		{Path: filepath.Join(ws, "a"), DisplayName: "a", Track: true},
		{Path: filepath.Join(ws, "b"), DisplayName: "b", Track: false},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("track = %d %s", rec.Code, rec.Body.String())
	}
	mkProject(t, filepath.Join(ws, "c"), "compose.yaml")
	mkProject(t, filepath.Join(ws, "e"), "compose.yaml")
	return d, h, ws
}

// startRescan posts a rescan and returns its id.
func startRescan(t *testing.T, h http.Handler, req RescanRequest) int64 {
	t.Helper()
	rec := doJSON(h, http.MethodPost, "/api/rescans", req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /api/rescans = %d %s, want 202", rec.Code, rec.Body.String())
	}
	return decode[ScanStarted](t, rec).ID
}

// rescanDone starts a rescan and waits for it to finish as done.
func rescanDone(t *testing.T, h http.Handler, req RescanRequest) (int64, ScanJob) {
	t.Helper()
	id := startRescan(t, h, req)
	job, body := waitScan(t, h, id)
	if job.State != "done" || job.Result == nil {
		t.Fatalf("rescan ended %q: %s", job.State, body)
	}
	return id, job
}

// activityOf returns the stored activity rows of one action, newest first.
func activityOf(t *testing.T, d Deps, action string) []store.ActivityRow {
	t.Helper()
	var out []store.ActivityRow
	for _, r := range activity(t, d) {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

// trackedNames returns the display names of the tracked projects, by path.
func trackedNames(t *testing.T, d Deps) map[string]string {
	t.Helper()
	ts, err := d.Store.TrackedProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, p := range ts {
		out[p.Path] = p.DisplayName
	}
	return out
}

func TestRescanAll(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	one, two := filepath.Join(rootOf(d), "one"), filepath.Join(rootOf(d), "two")
	mkProject(t, filepath.Join(one, "x"), "compose.yaml")
	mkProject(t, filepath.Join(two, "y"), "compose.yaml")
	for _, ws := range []string{one, two} {
		id, job := scanDone(t, h, ws, 1)
		if rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), trackAll(job)); rec.Code != http.StatusOK {
			t.Fatalf("track %s = %d %s", ws, rec.Code, rec.Body.String())
		}
	}
	id1, id2 := workspaceID(t, d, one), workspaceID(t, d, two)
	x2 := filepath.Join(one, "x2")
	mkProject(t, x2, "compose.yaml")

	_, job := rescanDone(t, h, RescanRequest{})
	if !job.Rescan || !slices.Equal(job.WorkspaceIDs, []int64{id1, id2}) || job.Path != "all workspaces" || job.Depth != 0 {
		t.Errorf("job = %+v, want a rescan of %d and %d", job, id1, id2)
	}
	if job.Progress.Workspaces != 2 {
		t.Errorf("progress = %+v, want 2 workspaces", job.Progress)
	}
	res := job.Result
	if !slices.Equal(res.Workspaces, []string{one, two}) || res.New != 1 || res.Refreshed != 2 || res.NotFound != 0 {
		t.Errorf("result = %+v", res)
	}
	if len(res.Rows) != 1 || res.Rows[0].Path != x2 || !res.Rows[0].New || res.Rows[0].Ignored || res.Rows[0].WorkspaceID != id1 {
		t.Errorf("rows = %+v, want x2 new under %d", res.Rows, id1)
	}
	rows := activityOf(t, d, store.ActWorkspaceRescan)
	if len(rows) != 1 || rows[0].Target != "all workspaces" || rows[0].Actor != "test@example.com" || rows[0].Result != store.ResultOK {
		t.Errorf("activity = %+v, want one ok rescan of all workspaces", rows)
	}
}

func TestRescanOne(t *testing.T) {
	d, h, ws := rescanSetup(t)
	other := filepath.Join(rootOf(d), "other")
	mkProject(t, filepath.Join(other, "z"), "compose.yaml")
	oid, ojob := scanDone(t, h, other, 1)
	if rec := doJSON(h, http.MethodPost, scanURL(oid, "/track"), trackAll(ojob)); rec.Code != http.StatusOK {
		t.Fatalf("track = %d %s", rec.Code, rec.Body.String())
	}
	wsID := workspaceID(t, d, ws)

	_, job := rescanDone(t, h, RescanRequest{WorkspaceID: wsID})
	if !job.Rescan || !slices.Equal(job.WorkspaceIDs, []int64{wsID}) || job.Path != ws {
		t.Errorf("job = %+v, want a rescan of %s", job, ws)
	}
	if job.Progress.Workspace != 1 || job.Progress.Workspaces != 1 {
		t.Errorf("progress = %+v, want workspace 1 of 1", job.Progress)
	}
	res := job.Result
	if !slices.Equal(res.Workspaces, []string{ws}) || res.New != 2 || res.IgnoredFound != 1 || res.Refreshed != 1 {
		t.Errorf("result = %+v", res)
	}
	got := map[string]ScanRow{}
	for _, r := range res.Rows {
		got[filepath.Base(r.Path)] = r
	}
	if b := got["b"]; !b.Ignored || b.New || b.WorkspaceID != wsID {
		t.Errorf("b = %+v, want ignored", b)
	}
	for _, n := range []string{"c", "e"} {
		if r := got[n]; !r.New || r.Ignored || r.WorkspaceID != wsID {
			t.Errorf("%s = %+v, want new", n, r)
		}
	}
	if len(got) != 3 {
		t.Errorf("rows = %+v, want b, c and e", res.Rows)
	}
	if rows := activityOf(t, d, store.ActWorkspaceRescan); len(rows) != 1 || rows[0].Target != ws {
		t.Errorf("activity = %+v, want one rescan of %s", rows, ws)
	}
}

func TestRescanUnknown404(t *testing.T) {
	d, h, _ := rescanSetup(t)
	for _, id := range []int64{9999, -1} {
		rec := doJSON(h, http.MethodPost, "/api/rescans", RescanRequest{WorkspaceID: id})
		if rec.Code != http.StatusNotFound || decode[map[string]string](t, rec)["error"] != "This workspace does not exist." {
			t.Errorf("rescan of %d = %d %s, want 404", id, rec.Code, rec.Body.String())
		}
	}
	if rows := activityOf(t, d, store.ActWorkspaceRescan); len(rows) != 0 {
		t.Errorf("activity = %+v, want no rescan", rows)
	}
}

func TestRescanNoWorkspace409(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	rec := doJSON(h, http.MethodPost, "/api/rescans", RescanRequest{})
	if rec.Code != http.StatusConflict || strings.TrimSpace(rec.Body.String()) != `{"error":"There is no workspace to rescan."}` {
		t.Errorf("rescan = %d %s, want 409", rec.Code, rec.Body.String())
	}
	rows := activityOf(t, d, store.ActWorkspaceRescan)
	if len(rows) != 1 || rows[0].Result != store.ResultRefused || rows[0].Target != "all workspaces" {
		t.Errorf("activity = %+v, want one refused rescan", rows)
	}
}

func TestRescanBusy409CarriesRescan(t *testing.T) {
	d, h, ws := rescanSetup(t)
	other := filepath.Join(rootOf(d), "other")
	mkDir(t, other)
	dockerOf(d).hold(t)

	id := startRescan(t, h, RescanRequest{})
	for _, tc := range []struct {
		path string
		body any
	}{
		{"/api/rescans", RescanRequest{}},
		{"/api/scans", ScanRequest{Path: other, Depth: 1}},
	} {
		rec := doJSON(h, http.MethodPost, tc.path, tc.body)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s = %d %s, want 409", tc.path, rec.Code, rec.Body.String())
		}
		got := decode[ScanBusy](t, rec)
		if got.ID != id || got.Path != ws || got.Depth != 0 || !got.Rescan || got.Error != "A scan is already running." {
			t.Errorf("%s body = %+v, want the running rescan %d", tc.path, got, id)
		}
	}
	running := decode[ScanJob](t, do(h, http.MethodGet, scanURL(id, "")))
	if running.State != "running" || !running.Rescan || running.Path != ws {
		t.Errorf("running job = %+v", running)
	}
	rows := activityOf(t, d, store.ActWorkspaceRescan)
	if len(rows) != 2 || rows[0].Result != store.ResultRefused || rows[0].Reason != "busy" {
		t.Errorf("activity = %+v, want the refused rescan", rows)
	}

	dockerOf(d).release()
	if job, body := waitScan(t, h, id); job.State != "done" {
		t.Errorf("rescan ended %q: %s", job.State, body)
	}
}

func TestTrackRescanOnlyChangedRows(t *testing.T) {
	d, h, ws := rescanSetup(t)
	id, _ := rescanDone(t, h, RescanRequest{})

	c := filepath.Join(ws, "c")
	rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), TrackRequest{Projects: []TrackItem{
		{Path: c, DisplayName: " Cee ", Track: true},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("track = %d %s", rec.Code, rec.Body.String())
	}
	if got := decode[TrackResponse](t, rec); got != (TrackResponse{WorkspaceID: 0, Added: 1}) {
		t.Errorf("body = %+v, want 1 added", got)
	}
	names := trackedNames(t, d)
	if len(names) != 2 || names[filepath.Join(ws, "a")] != "a" || names[c] != "Cee" {
		t.Errorf("tracked = %v, want a and Cee", names)
	}
	known, err := d.Store.KnownProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"b", "e"} {
		if k, ok := known[filepath.Join(ws, n)]; !ok || k.State != "ignored" {
			t.Errorf("%s = %+v, want stored as ignored", n, k)
		}
	}
	wsList := decode[[]Workspace](t, do(h, http.MethodGet, "/api/workspaces"))
	if len(wsList) != 1 || wsList[0].Tracked != 2 || wsList[0].Ignored != 2 {
		t.Errorf("workspaces = %+v, want 2 tracked and 2 ignored", wsList)
	}
	rows := activityOf(t, d, store.ActScanTrack)
	// The only workspace is the rescan's target.
	if len(rows) != 2 || rows[0].Result != store.ResultOK || rows[0].Target != ws {
		t.Errorf("activity = %+v, want an ok track of the rescan", rows)
	}
}

func TestTrackRescanRetrackIgnored(t *testing.T) {
	d, h, ws := rescanSetup(t)
	id, _ := rescanDone(t, h, RescanRequest{WorkspaceID: workspaceID(t, d, ws)})

	b := filepath.Join(ws, "b")
	rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), TrackRequest{Projects: []TrackItem{
		{Path: b, DisplayName: "Bee", Track: true},
	}})
	if rec.Code != http.StatusOK || decode[TrackResponse](t, rec).Added != 1 {
		t.Fatalf("track = %d %s, want 1 added", rec.Code, rec.Body.String())
	}
	names := trackedNames(t, d)
	if len(names) != 2 || names[b] != "Bee" {
		t.Errorf("tracked = %v, want a and Bee", names)
	}
	known, err := d.Store.KnownProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"c", "e"} {
		if k := known[filepath.Join(ws, n)]; k.State != "ignored" {
			t.Errorf("%s = %+v, want stored as ignored", n, k)
		}
	}
}

func TestTrackRescanSecond409(t *testing.T) {
	d, h, ws := rescanSetup(t)
	id, _ := rescanDone(t, h, RescanRequest{})
	body := TrackRequest{Projects: []TrackItem{{Path: filepath.Join(ws, "c"), DisplayName: "c", Track: true}}}

	if rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), body); rec.Code != http.StatusOK {
		t.Fatalf("first track = %d %s", rec.Code, rec.Body.String())
	}
	rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), body)
	if rec.Code != http.StatusConflict || strings.TrimSpace(rec.Body.String()) != `{"error":"These projects were already added."}` {
		t.Errorf("second track = %d %s, want 409", rec.Code, rec.Body.String())
	}
	rows := activityOf(t, d, store.ActScanTrack)
	if len(rows) == 0 || rows[0].Result != store.ResultRefused || rows[0].Reason != "These projects were already added." {
		t.Errorf("activity = %+v, want a refused second track", rows)
	}
}

func TestTrackRescanMismatch422(t *testing.T) {
	d, h, ws := rescanSetup(t)
	id, _ := rescanDone(t, h, RescanRequest{})
	a, c := filepath.Join(ws, "a"), filepath.Join(ws, "c")

	for name, items := range map[string][]TrackItem{
		"a tracked path":  {{Path: a, DisplayName: "a", Track: true}},
		"an unknown path": {{Path: filepath.Join(ws, "zz"), DisplayName: "zz", Track: true}},
		"track false":     {{Path: c, DisplayName: "c", Track: false}},
		"twice":           {{Path: c, DisplayName: "c", Track: true}, {Path: c, DisplayName: "c", Track: true}},
	} {
		rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), TrackRequest{Projects: items})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %s, want 422", name, rec.Code, rec.Body.String())
			continue
		}
		if fe := decode[FieldError](t, rec); fe.Code != "projects_mismatch" || fe.Field != "projects" || fe.Error == "" {
			t.Errorf("%s body = %+v", name, fe)
		}
	}
	for _, n := range []string{"", "   ", strings.Repeat("x", 81)} {
		rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), TrackRequest{Projects: []TrackItem{{Path: c, DisplayName: n, Track: true}}})
		if rec.Code != http.StatusUnprocessableEntity || decode[FieldError](t, rec).Code != "invalid_name" {
			t.Errorf("name %q = %d %s, want 422 invalid_name", n, rec.Code, rec.Body.String())
		}
	}
	if names := trackedNames(t, d); len(names) != 1 {
		t.Errorf("tracked = %v, want only a", names)
	}
	// Nothing was marked: a valid track still goes through.
	rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), TrackRequest{Projects: []TrackItem{{Path: c, DisplayName: "c", Track: true}}})
	if rec.Code != http.StatusOK {
		t.Errorf("valid track = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoveWorkspaceOK(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	ws := filepath.Join(rootOf(d), "ws")
	id := workspaceID(t, d, ws)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	h := NewRouter(d)
	srv := httptest.NewServer(h)
	defer srv.Close()

	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	if s := nextState(t, r); len(s.Projects) != 2 {
		t.Fatalf("first state = %+v", s)
	}

	rec := doJSON(h, http.MethodPost, removeURL(id), "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"removed":2}` {
		t.Fatalf("remove = %d %s, want 200 {removed:2}", rec.Code, rec.Body.String())
	}
	start := time.Now()
	stateUntil(t, r, "no project", func(s LiveState) bool { return len(s.Projects) == 0 })
	if time.Since(start) > 4*time.Second {
		t.Errorf("the removal took %v to reach the hub, want it without waiting for a tick", time.Since(start))
	}
	if got := strings.TrimSpace(do(h, http.MethodGet, "/api/workspaces").Body.String()); got != "[]" {
		t.Errorf("workspaces = %s, want []", got)
	}
	rows := activityOf(t, d, store.ActWorkspaceRemove)
	if len(rows) != 1 || rows[0].Result != store.ResultOK || rows[0].Target != ws || rows[0].Actor != "test@example.com" {
		t.Errorf("activity = %+v, want one ok removal of %s", rows, ws)
	}
}

func TestRemoveWorkspace404(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	for _, id := range []string{"9999", "0", "-1", "abc"} {
		rec := doJSON(h, http.MethodPost, "/api/workspaces/"+id+"/remove", "")
		if rec.Code != http.StatusNotFound || decode[map[string]string](t, rec)["error"] != "This workspace does not exist." {
			t.Errorf("remove %s = %d %s, want 404", id, rec.Code, rec.Body.String())
		}
	}
	if names := trackedNames(t, d); len(names) != 2 {
		t.Errorf("tracked = %v, want alpha and beta", names)
	}
}

func TestRemoveWorkspaceWhileScanning409(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	ws := filepath.Join(rootOf(d), "ws")
	id := workspaceID(t, d, ws)
	other := filepath.Join(rootOf(d), "other")
	mkDir(t, other)
	dockerOf(d).hold(t)
	scanID := startScan(t, h, other, 1)

	rec := doJSON(h, http.MethodPost, removeURL(id), "")
	if rec.Code != http.StatusConflict || strings.TrimSpace(rec.Body.String()) != `{"error":"A scan is running; try again when it ends."}` {
		t.Errorf("remove = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if names := trackedNames(t, d); len(names) != 2 {
		t.Errorf("tracked = %v, want alpha and beta kept", names)
	}
	rows := activityOf(t, d, store.ActWorkspaceRemove)
	if len(rows) != 1 || rows[0].Result != store.ResultRefused || rows[0].Target != ws ||
		rows[0].Reason != "A scan is running; try again when it ends." {
		t.Errorf("activity = %+v, want one refused removal", rows)
	}
	dockerOf(d).release()
	waitScan(t, h, scanID)
}

func TestRemoveWorkspaceWhileLocked409(t *testing.T) {
	t.Run("command", func(t *testing.T) {
		d := newTestDeps(t)
		h := NewRouter(d)
		addProjects(t, d)
		ws := filepath.Join(rootOf(d), "ws")
		id := workspaceID(t, d, ws)
		alpha, _ := trackedIDs(t, d)
		f := dockerOf(d)
		f.set(func(f *fakeDocker) { f.containers = alphaUp() })
		f.holdLifecycle(t)
		done := postAsync(h, actionURL(alpha, "stop"))
		waitLocked(t, d, alpha)

		rec := doJSON(h, http.MethodPost, removeURL(id), "")
		want := "Alpha is " + project.Word(project.StatusStopping) + "; wait for it to finish."
		if rec.Code != http.StatusConflict || decode[map[string]string](t, rec)["error"] != want {
			t.Errorf("remove = %d %s, want 409 %q", rec.Code, rec.Body.String(), want)
		}
		f.releaseLifecycle()
		<-done
		if names := trackedNames(t, d); len(names) != 2 {
			t.Errorf("tracked = %v, want alpha and beta kept", names)
		}
		rows := activityOf(t, d, store.ActWorkspaceRemove)
		if len(rows) != 1 || rows[0].Result != store.ResultRefused || rows[0].Reason != want {
			t.Errorf("activity = %+v, want one refused removal", rows)
		}
	})
	t.Run("held", func(t *testing.T) {
		d := newTestDeps(t)
		h := NewRouter(d)
		addProjects(t, d)
		id := workspaceID(t, d, filepath.Join(rootOf(d), "ws"))
		alpha, _ := trackedIDs(t, d)
		held, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
		go func() {
			defer close(finished)
			_ = d.Control.WithLocks([]int64{alpha}, func() error {
				close(held)
				<-release
				return errors.New("let go")
			})
		}()
		<-held

		rec := doJSON(h, http.MethodPost, removeURL(id), "")
		if rec.Code != http.StatusConflict || decode[map[string]string](t, rec)["error"] != "Alpha is being removed from the panel." {
			t.Errorf("remove = %d %s, want 409", rec.Code, rec.Body.String())
		}
		close(release)
		<-finished
	})
}

func TestUntrackOK(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	alpha, beta := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	h := NewRouter(d)
	srv := httptest.NewServer(h)
	defer srv.Close()

	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	if s := nextState(t, r); len(s.Projects) != 2 {
		t.Fatalf("first state = %+v", s)
	}

	rec := doJSON(h, http.MethodPost, untrackURL(alpha), "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"removed":true}` {
		t.Fatalf("untrack = %d %s, want 200 {removed:true}", rec.Code, rec.Body.String())
	}
	// The hold for the write may be published first, with alpha offering
	// nothing; no state shows alpha with its actions again, and the state
	// without it comes without waiting for a tick.
	start := time.Now()
	s := stateUntil(t, r, "alpha gone", func(s LiveState) bool {
		for _, p := range s.Projects {
			if p.ID == alpha {
				if len(p.Actions) != 0 {
					t.Errorf("alpha after the untrack = %+v, want no actions", p)
				}
				return false
			}
		}
		return true
	})
	if time.Since(start) > 4*time.Second {
		t.Errorf("the untrack took %v to reach the hub, want it without waiting for a tick", time.Since(start))
	}
	if len(s.Projects) != 1 || s.Projects[0].ID != beta {
		t.Errorf("state = %+v, want only beta", s)
	}
	known, err := d.Store.KnownProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if k := known[filepath.Join(rootOf(d), "ws", "alpha")]; k.State != "ignored" {
		t.Errorf("alpha = %+v, want ignored", k)
	}
	rows := activityOf(t, d, store.ActProjectUntrack)
	if len(rows) != 1 || rows[0].Result != store.ResultOK || rows[0].ProjectID != alpha || rows[0].Target != "Alpha" {
		t.Errorf("activity = %+v, want one ok untrack of Alpha", rows)
	}
}

// markMissing marks alpha of addProjects as Not found, as a rescan would.
func markMissing(t *testing.T, d Deps) {
	t.Helper()
	ws := filepath.Join(rootOf(d), "ws")
	err := d.Store.ApplyRescan(context.Background(), store.RescanApply{Workspaces: []store.WorkspaceApply{{
		ID: workspaceID(t, d, ws), LastScanAt: time.Now(), Missing: []string{filepath.Join(ws, "alpha")},
	}}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

func TestUntrackMissingRemoves(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	markMissing(t, d)

	rec := doJSON(h, http.MethodPost, untrackURL(alpha), "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"removed":true}` {
		t.Fatalf("untrack = %d %s", rec.Code, rec.Body.String())
	}
	known, err := d.Store.KnownProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := known[filepath.Join(rootOf(d), "ws", "alpha")]; ok {
		t.Error("alpha is still stored, want it removed")
	}
	rows := activityOf(t, d, store.ActProjectRemove)
	if len(rows) != 1 || rows[0].Result != store.ResultOK || rows[0].ProjectID != alpha {
		t.Errorf("activity = %+v, want one ok project.remove", rows)
	}
}

func TestUntrack404(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, beta := trackedIDs(t, d)
	gamma := beta + 1 // ignored: stored right after beta

	for _, id := range []string{"9999", strconv.FormatInt(gamma, 10), "abc", "0", "-1"} {
		rec := doJSON(h, http.MethodPost, "/api/projects/"+id+"/untrack", "")
		if rec.Code != http.StatusNotFound || decode[map[string]string](t, rec)["error"] != "This project is not tracked." {
			t.Errorf("untrack %s = %d %s, want 404", id, rec.Code, rec.Body.String())
		}
	}
	if rec := doJSON(h, http.MethodPost, untrackURL(alpha), ""); rec.Code != http.StatusOK {
		t.Fatalf("untrack = %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(h, http.MethodPost, untrackURL(alpha), ""); rec.Code != http.StatusNotFound {
		t.Errorf("second untrack = %d %s, want 404", rec.Code, rec.Body.String())
	}
	// remove stays unknown as an action.
	if rec := doJSON(h, http.MethodPost, actionURL(beta, "remove"), ""); rec.Code != http.StatusNotFound {
		t.Errorf("remove action = %d %s, want 404", rec.Code, rec.Body.String())
	}
}

func TestUntrackLocked409(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	f := dockerOf(d)
	f.set(func(f *fakeDocker) { f.containers = alphaUp() })
	f.holdLifecycle(t)
	done := postAsync(h, actionURL(alpha, "stop"))
	waitLocked(t, d, alpha)

	rec := doJSON(h, http.MethodPost, untrackURL(alpha), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("untrack = %d %s, want 409", rec.Code, rec.Body.String())
	}
	got := decode[ActionRefused](t, rec)
	want := "Alpha is " + project.Word(project.StatusStopping) + "; wait for it to finish."
	if got.Code != "locked" || got.Status != "stopping" || got.Error != want {
		t.Errorf("body = %+v, want locked while stopping", got)
	}
	f.releaseLifecycle()
	<-done
	if names := trackedNames(t, d); len(names) != 2 {
		t.Errorf("tracked = %v, want alpha kept", names)
	}
	rows := activityOf(t, d, store.ActProjectUntrack)
	if len(rows) != 1 || rows[0].Result != store.ResultRefused || rows[0].Reason != want {
		t.Errorf("activity = %+v, want one refused untrack", rows)
	}
}

func TestUntrackActorFromVerifier(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)

	req := httptest.NewRequest(http.MethodPost, untrackURL(alpha), nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Email", "evil@x")
	req.Header.Set("Cf-Access-Authenticated-User-Email", "evil@x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if rows := activityOf(t, d, store.ActProjectUntrack); len(rows) != 1 || rows[0].Actor != "test@example.com" {
		t.Errorf("activity = %+v, want the verifier's email", rows)
	}
}

func TestNewPostsNeedJSON415(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	id := workspaceID(t, d, filepath.Join(rootOf(d), "ws"))
	const want = `{"error":"Send the request as application/json."}`

	for _, path := range []string{"/api/rescans", removeURL(id), untrackURL(alpha)} {
		for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnsupportedMediaType || strings.TrimSpace(rec.Body.String()) != want {
				t.Errorf("POST %s with %q = %d %s, want 415", path, ct, rec.Code, rec.Body.String())
			}
		}
	}
	if names := trackedNames(t, d); len(names) != 2 {
		t.Errorf("tracked = %v, want nothing changed", names)
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("activity = %+v, want none", rows)
	}
	if n := dockerOf(d).callCount(); n != 0 {
		t.Errorf("the fake ran %d commands, want none", n)
	}
}

func TestProjectsMissingSinceAndActions(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	markMissing(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })

	rec := do(h, http.MethodGet, "/api/projects")
	got := decode[[]Project](t, rec)
	if len(got) != 2 {
		t.Fatalf("projects = %+v", got)
	}
	alpha, beta := got[0], got[1]
	if alpha.Status != "notfound" || !slices.Equal(alpha.Actions, []string{"remove"}) {
		t.Errorf("alpha = %+v, want notfound with only remove", alpha)
	}
	if ts, err := time.Parse(time.RFC3339, alpha.MissingSince); err != nil || time.Since(ts) > time.Minute {
		t.Errorf("alpha missingSince = %q, want a recent RFC 3339 time", alpha.MissingSince)
	}
	if beta.MissingSince != "" || !slices.Equal(beta.Actions, []string{"untrack"}) {
		t.Errorf("beta = %+v, want present with only untrack", beta)
	}
	if !strings.Contains(rec.Body.String(), `"missingSince":""`) {
		t.Errorf("body = %s, want beta's missingSince as \"\"", rec.Body.String())
	}
}
