package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/roots"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// doJSON sends body as JSON (a string is sent as it is).
func doJSON(h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var b []byte
	switch v := body.(type) {
	case string:
		b = []byte(v)
	default:
		var err error
		if b, err = json.Marshal(v); err != nil {
			panic(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	return v
}

// mkProject creates dir with a compose file named file.
func mkProject(t *testing.T, dir, file string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
}

// startScan posts a scan and returns its id.
func startScan(t *testing.T, h http.Handler, path string, depth int) int64 {
	t.Helper()
	rec := doJSON(h, http.MethodPost, "/api/scans", ScanRequest{Path: path, Depth: depth})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /api/scans = %d %s, want 202", rec.Code, rec.Body.String())
	}
	got := decode[ScanStarted](t, rec)
	if got.ID <= 0 {
		t.Fatalf("scan id = %d", got.ID)
	}
	return got.ID
}

func scanURL(id int64, suffix string) string {
	return "/api/scans/" + strconv.FormatInt(id, 10) + suffix
}

// waitScan polls GET /api/scans/{id} until the job is no longer running and
// returns the job and the raw body.
func waitScan(t *testing.T, h http.Handler, id int64) (ScanJob, string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := do(h, http.MethodGet, scanURL(id, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET scan = %d %s", rec.Code, rec.Body.String())
		}
		job := decode[ScanJob](t, rec)
		if job.State != "running" {
			return job, rec.Body.String()
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan %d still running", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// scanDone starts a scan and waits for it to finish as done.
func scanDone(t *testing.T, h http.Handler, path string, depth int) (int64, ScanJob) {
	t.Helper()
	id := startScan(t, h, path, depth)
	job, body := waitScan(t, h, id)
	if job.State != "done" || job.Result == nil {
		t.Fatalf("scan ended %q: %s", job.State, body)
	}
	return id, job
}

// waitTotal polls a running scan until it has listed total folders, so the
// next progress it publishes comes after the docker gate opens.
func waitTotal(t *testing.T, h http.Handler, id int64, total int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for decode[ScanJob](t, do(h, http.MethodGet, scanURL(id, ""))).Progress.Total != total {
		if time.Now().After(deadline) {
			t.Fatalf("scan %d never listed %d folders", id, total)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// trackAll tracks every row of a scan under its default name.
func trackAll(job ScanJob) TrackRequest {
	req := TrackRequest{Projects: []TrackItem{}}
	for _, r := range job.Result.Rows {
		req.Projects = append(req.Projects, TrackItem{Path: r.Path, DisplayName: r.DefaultName, Track: true})
	}
	return req
}

func TestStartScanFieldErrors(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	root := rootOf(d)
	mkProject(t, filepath.Join(root, "ws", "app"), "compose.yaml")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	tests := []struct {
		name, path, code, msg string
	}{
		{"empty", "", roots.CodeNotAbsolute, "Enter an absolute path, starting with /."},
		{"blank", "   ", roots.CodeNotAbsolute, "Enter an absolute path, starting with /."},
		{"relative", "ws/app", roots.CodeNotAbsolute, "Enter an absolute path, starting with /."},
		{"missing", filepath.Join(root, "missing"), roots.CodeNotFound, "This folder does not exist."},
		{"outside", outside, roots.CodeOutsideRoots, "This folder is outside the allowed folders (" + root + "). Add it to WORKSPACE_ROOTS and re-run install.sh."},
		{"climbs out", root + "/ws/../..", roots.CodeOutsideRoots, "This folder is outside the allowed folders (" + root + "). Add it to WORKSPACE_ROOTS and re-run install.sh."},
		{"file", filepath.Join(root, "file.txt"), roots.CodeNotAFolder, "This path is a file, not a folder."},
	}
	if os.Geteuid() != 0 {
		locked := filepath.Join(root, "locked")
		mkDir(t, locked)
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o750) }) //nolint:gosec // a folder needs its execute bit back so the temp dir can be removed
		tests = append(tests, struct{ name, path, code, msg string }{
			"unreadable", locked, roots.CodeUnreadable, "The panel cannot read this folder (permission denied).",
		})
	} else {
		t.Log("running as root: the unreadable case is covered by the roots package tests")
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(h, http.MethodPost, "/api/scans", ScanRequest{Path: tc.path, Depth: 1})
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d %s, want 422", rec.Code, rec.Body.String())
			}
			got := decode[FieldError](t, rec)
			want := FieldError{Error: tc.msg, Field: "path", Code: tc.code}
			if got != want {
				t.Errorf("body = %+v, want %+v", got, want)
			}
		})
	}

	for _, depth := range []int{0, 4, -1} {
		t.Run("depth "+strconv.Itoa(depth), func(t *testing.T) {
			rec := doJSON(h, http.MethodPost, "/api/scans", ScanRequest{Path: filepath.Join(root, "ws"), Depth: depth})
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d %s, want 422", rec.Code, rec.Body.String())
			}
			got := decode[FieldError](t, rec)
			if got.Field != "depth" || got.Error == "" || got.Code == "" {
				t.Errorf("body = %+v, want a depth field error", got)
			}
		})
	}

	t.Run("bad JSON", func(t *testing.T) {
		rec := doJSON(h, http.MethodPost, "/api/scans", `{"path":`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestStartScanAlreadyAdded(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	ws := filepath.Join(rootOf(d), "ws")
	mkProject(t, filepath.Join(ws, "app"), "compose.yaml")
	if _, _, err := d.Store.AddWorkspace(context.Background(),
		store.NewWorkspace{Name: "ws", Path: ws, Depth: 1, LastScanAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{ws, ws + "/", "  " + ws + "  ", rootOf(d) + "//ws/./"} {
		rec := doJSON(h, http.MethodPost, "/api/scans", ScanRequest{Path: path, Depth: 1})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%q: status = %d %s, want 422", path, rec.Code, rec.Body.String())
		}
		got := decode[FieldError](t, rec)
		want := FieldError{Error: "This folder is already a workspace. Rescan it from Settings.", Field: "path", Code: "already_added"}
		if got != want {
			t.Errorf("%q: body = %+v, want %+v", path, got, want)
		}
	}
}

func TestStartScanBusy(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	ws := filepath.Join(rootOf(d), "ws")
	mkProject(t, filepath.Join(ws, "app"), "compose.yaml")
	dockerOf(d).hold(t)

	id := startScan(t, h, ws+"/", 2)
	rec := doJSON(h, http.MethodPost, "/api/scans", ScanRequest{Path: rootOf(d), Depth: 1})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second scan = %d %s, want 409", rec.Code, rec.Body.String())
	}
	got := decode[ScanBusy](t, rec)
	if got.ID != id || got.Path != ws || got.Depth != 2 || got.Error == "" {
		t.Errorf("body = %+v, want the running scan %d of %s at depth 2", got, id, ws)
	}

	running := decode[ScanJob](t, do(h, http.MethodGet, scanURL(id, "")))
	if running.State != "running" || running.Result != nil || running.Path != ws || running.Depth != 2 {
		t.Errorf("running job = %+v", running)
	}

	dockerOf(d).release()
	if job, body := waitScan(t, h, id); job.State != "done" {
		t.Errorf("scan ended %q: %s", job.State, body)
	}
}

func TestGetScanNotFound(t *testing.T) {
	h := NewRouter(newTestDeps(t))
	for _, path := range []string{"/api/scans/999", "/api/scans/abc", "/api/scans/999/events"} {
		if rec := do(h, http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
	if rec := doJSON(h, http.MethodPost, "/api/scans/999/track", TrackRequest{}); rec.Code != http.StatusNotFound {
		t.Errorf("POST track = %d, want 404", rec.Code)
	}
}

func TestScanFlowToTrack(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	ws := filepath.Join(rootOf(d), "ws")
	shop := filepath.Join(ws, "shop")
	blog := filepath.Join(ws, "blog")
	mkProject(t, shop, "compose.yaml")
	mkProject(t, blog, "docker-compose.yml")
	mkDir(t, filepath.Join(ws, "notes"))

	id := startScan(t, h, "  "+ws+"  ", 2)
	job, body := waitScan(t, h, id)
	if job.State != "done" || job.Result == nil {
		t.Fatalf("scan ended %q: %s", job.State, body)
	}
	if job.ID != id || job.Path != ws || job.Depth != 2 || job.Error != "" {
		t.Errorf("job = %+v", job)
	}
	if job.Progress.Examined != job.Progress.Total || job.Progress.Total != 3 {
		t.Errorf("progress = %+v, want 3/3", job.Progress)
	}
	res := job.Result
	if len(res.Rows) != 2 || res.FoldersScanned != 3 || res.AlreadyTracked != 0 || res.Ignored != 0 {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{`"databases":[]`, `"warnings":[]`, `"composeFiles":["docker-compose.yml"]`, `"status":"stopped"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body has no %s: %s", want, body)
		}
	}
	var blogRow ScanRow
	for _, r := range res.Rows {
		if r.Path == blog {
			blogRow = r
		}
	}
	if blogRow.DefaultName != "blog" || blogRow.Services != 1 || blogRow.Self || blogRow.Warning != "" {
		t.Errorf("blog row = %+v", blogRow)
	}

	req := TrackRequest{Projects: []TrackItem{
		{Path: shop, DisplayName: "  Shop  ", Track: true},
		{Path: blog, DisplayName: "blog", Track: false},
	}}
	rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), req)
	if rec.Code != http.StatusOK {
		t.Fatalf("track = %d %s, want 200", rec.Code, rec.Body.String())
	}
	tracked := decode[TrackResponse](t, rec)
	if tracked.Added != 1 || tracked.WorkspaceID <= 0 {
		t.Errorf("track = %+v, want added 1", tracked)
	}

	rec = do(h, http.MethodGet, "/api/workspaces")
	if rec.Code != http.StatusOK {
		t.Fatalf("workspaces = %d", rec.Code)
	}
	wss := decode[[]Workspace](t, rec)
	if len(wss) != 1 {
		t.Fatalf("workspaces = %+v", wss)
	}
	w := wss[0]
	if w.ID != tracked.WorkspaceID || w.Name != "ws" || w.Path != ws || w.Depth != 2 || w.Tracked != 1 || w.Ignored != 1 {
		t.Errorf("workspace = %+v", w)
	}
	last, err := time.Parse(time.RFC3339, w.LastScanAt)
	if err != nil || !strings.HasSuffix(w.LastScanAt, "Z") || time.Since(last) > time.Minute {
		t.Errorf("lastScanAt = %q (%v), want a recent UTC RFC 3339 time", w.LastScanAt, err)
	}

	projects := decode[[]Project](t, do(h, http.MethodGet, "/api/projects"))
	if len(projects) != 1 || projects[0].DisplayName != "Shop" || projects[0].Path != shop || projects[0].WorkspaceID != w.ID {
		t.Errorf("projects = %+v", projects)
	}

	rec = doJSON(h, http.MethodPost, scanURL(id, "/track"), req)
	if rec.Code != http.StatusConflict {
		t.Errorf("second track = %d %s, want 409", rec.Code, rec.Body.String())
	}
}

func TestTrackRejectsBadNames(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	ws := filepath.Join(rootOf(d), "ws")
	a, b := filepath.Join(ws, "a"), filepath.Join(ws, "b")
	mkProject(t, a, "compose.yaml")
	mkProject(t, b, "compose.yaml")
	id, _ := scanDone(t, h, ws, 1)

	item := func(path, name string) TrackItem { return TrackItem{Path: path, DisplayName: name, Track: true} }
	tests := []struct {
		name  string
		items []TrackItem
	}{
		{"spaces only", []TrackItem{item(a, "   "), item(b, "b")}},
		{"empty", []TrackItem{item(a, ""), item(b, "b")}},
		{"81 characters", []TrackItem{item(a, strings.Repeat("x", 81)), item(b, "b")}},
		{"ignored row with a blank name", []TrackItem{item(a, "a"), {Path: b, DisplayName: " ", Track: false}}},
		{"missing row", []TrackItem{item(a, "a")}},
		{"duplicate row", []TrackItem{item(a, "a"), item(a, "a2"), item(b, "b")}},
		{"unknown row", []TrackItem{item(a, "a"), item(b, "b"), item(filepath.Join(ws, "c"), "c")}},
		{"no rows", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), TrackRequest{Projects: tc.items})
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d %s, want 422", rec.Code, rec.Body.String())
			}
			if got := decode[FieldError](t, rec); got.Field != "projects" || got.Error == "" || got.Code == "" {
				t.Errorf("body = %+v, want a projects field error", got)
			}
		})
	}
	if wss, err := d.Store.Workspaces(context.Background()); err != nil || len(wss) != 0 {
		t.Fatalf("a refused track stored %v (%v)", wss, err)
	}

	// 80 characters, counted as characters rather than bytes, is accepted.
	long := strings.Repeat("é", 80)
	rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), TrackRequest{Projects: []TrackItem{item(a, " "+long+" "), item(b, "b")}})
	if rec.Code != http.StatusOK {
		t.Fatalf("track with an 80-character name = %d %s", rec.Code, rec.Body.String())
	}
	projects := decode[[]Project](t, do(h, http.MethodGet, "/api/projects"))
	if len(projects) != 2 || projects[0].DisplayName != long {
		t.Errorf("projects = %+v", projects)
	}
}

func TestTrackRefusesUnfinishedScan(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	ws := filepath.Join(rootOf(d), "ws")
	mkProject(t, filepath.Join(ws, "a"), "compose.yaml")
	dockerOf(d).hold(t)

	id := startScan(t, h, ws, 1)
	rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), TrackRequest{})
	if rec.Code != http.StatusConflict {
		t.Errorf("track of a running scan = %d %s, want 409", rec.Code, rec.Body.String())
	}
	dockerOf(d).release()
	waitScan(t, h, id)
}

func TestRescanOverlappingLeavesIgnoredOut(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	root := rootOf(d)
	ws := filepath.Join(root, "ws")
	a, b, c := filepath.Join(ws, "a"), filepath.Join(ws, "b"), filepath.Join(root, "other", "c")
	mkProject(t, a, "compose.yaml")
	mkProject(t, b, "compose.yaml")
	mkProject(t, c, "compose.yaml")

	id, _ := scanDone(t, h, ws, 1)
	rec := doJSON(h, http.MethodPost, scanURL(id, "/track"), TrackRequest{Projects: []TrackItem{
		{Path: a, DisplayName: "a", Track: true},
		{Path: b, DisplayName: "b", Track: false},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("track = %d %s", rec.Code, rec.Body.String())
	}

	_, job := scanDone(t, h, root, 2)
	res := job.Result
	if len(res.Rows) != 1 || res.Rows[0].Path != c {
		t.Errorf("rows = %+v, want only %s", res.Rows, c)
	}
	if res.Ignored != 1 || res.AlreadyTracked != 1 {
		t.Errorf("ignored = %d, alreadyTracked = %d, want 1 and 1", res.Ignored, res.AlreadyTracked)
	}
}

func TestTrackRefusesProjectsAddedMeanwhile(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	root := rootOf(d)
	ws := filepath.Join(root, "ws")
	a := filepath.Join(ws, "a")
	mkProject(t, a, "compose.yaml")

	wide, wideJob := scanDone(t, h, root, 2)
	narrow, narrowJob := scanDone(t, h, ws, 1)
	if rec := doJSON(h, http.MethodPost, scanURL(narrow, "/track"), trackAll(narrowJob)); rec.Code != http.StatusOK {
		t.Fatalf("track = %d %s", rec.Code, rec.Body.String())
	}
	rec := doJSON(h, http.MethodPost, scanURL(wide, "/track"), trackAll(wideJob))
	if rec.Code != http.StatusConflict {
		t.Errorf("track of a stale scan = %d %s, want 409", rec.Code, rec.Body.String())
	}
}

func TestStaleRunningJobIsReportedFailed(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	// A job the store says is running but the manager is not running: what a
	// failed FinishJob leaves behind.
	id, err := d.Store.CreateScanJob(context.Background(), rootOf(d), 1, "test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	job := decode[ScanJob](t, do(h, http.MethodGet, scanURL(id, "")))
	if job.State != "failed" || job.Error != "The scan stopped unexpectedly." || job.Result != nil {
		t.Errorf("job = %+v, want failed", job)
	}

	srv := httptest.NewServer(h)
	defer srv.Close()
	events := readSSE(t, srv.URL+scanURL(id, "/events"))
	if len(events) != 2 || events[0].kind != "progress" || events[1].kind != "end" || events[1].data != `{"state":"failed"}` {
		t.Errorf("events = %+v", events)
	}
}

// sseEvent is one parsed server-sent event; a comment has kind ":".
type sseEvent struct{ kind, data string }

type sseReader struct {
	t *testing.T
	r *bufio.Reader
}

// next reads one event or comment. ok is false at EOF.
func (s *sseReader) next() (ev sseEvent, ok bool) {
	s.t.Helper()
	for {
		line, err := s.r.ReadString('\n')
		if errors.Is(err, io.EOF) && line == "" {
			if ev.kind != "" {
				s.t.Fatalf("stream ended inside event %+v", ev)
			}
			return sseEvent{}, false
		}
		if err != nil {
			s.t.Fatalf("read stream: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			if ev.kind == "" {
				s.t.Fatal("blank line outside an event")
			}
			return ev, true
		case strings.HasPrefix(line, ": "):
			ev = sseEvent{kind: ":", data: strings.TrimPrefix(line, ": ")}
		case strings.HasPrefix(line, "event: "):
			ev.kind = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.data = strings.TrimPrefix(line, "data: ")
		default:
			s.t.Fatalf("unexpected line %q", line)
		}
	}
}

// openSSE connects to url and checks the stream headers.
func openSSE(t *testing.T, url string) (*sseReader, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", url, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	return &sseReader{t: t, r: bufio.NewReader(resp.Body)}, func() { _ = resp.Body.Close(); cancel() }
}

// readSSE reads a whole stream up to EOF.
func readSSE(t *testing.T, url string) []sseEvent {
	t.Helper()
	r, closeFn := openSSE(t, url)
	defer closeFn()
	var events []sseEvent
	for {
		ev, ok := r.next()
		if !ok {
			return events
		}
		events = append(events, ev)
	}
}

func TestSSEStream(t *testing.T) {
	d := newTestDeps(t)
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()
	ws := filepath.Join(rootOf(d), "ws")
	mkProject(t, filepath.Join(ws, "a"), "compose.yaml")
	mkProject(t, filepath.Join(ws, "b"), "compose.yaml")
	dockerOf(d).hold(t)

	id := startScan(t, NewRouter(d), ws, 1)
	waitTotal(t, NewRouter(d), id, 2)
	r, closeFn := openSSE(t, srv.URL+scanURL(id, "/events"))
	defer closeFn()

	first, ok := r.next()
	if !ok || first.kind != "progress" {
		t.Fatalf("first event = %+v, want progress", first)
	}
	var p ScanProgress
	if err := json.Unmarshal([]byte(first.data), &p); err != nil || p.Total != 2 {
		t.Errorf("snapshot %q = %+v (%v), want total 2", first.data, p, err)
	}

	dockerOf(d).release()
	var last ScanProgress
	for {
		ev, ok := r.next()
		if !ok {
			t.Fatal("stream ended before the end event")
		}
		if ev.kind == "end" {
			if ev.data != `{"state":"done"}` {
				t.Errorf("end data = %q", ev.data)
			}
			break
		}
		if ev.kind != "progress" {
			t.Fatalf("unexpected event %+v", ev)
		}
		if err := json.Unmarshal([]byte(ev.data), &last); err != nil {
			t.Fatal(err)
		}
	}
	if last.Examined != 2 || last.Total != 2 {
		t.Errorf("last progress = %+v, want 2/2", last)
	}
	if ev, ok := r.next(); ok {
		t.Errorf("event after end: %+v", ev)
	}
}

func TestSSELateSubscriber(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	srv := httptest.NewServer(h)
	defer srv.Close()
	ws := filepath.Join(rootOf(d), "ws")
	mkProject(t, filepath.Join(ws, "a"), "compose.yaml")
	id, _ := scanDone(t, h, ws, 1)

	events := readSSE(t, srv.URL+scanURL(id, "/events"))
	if len(events) != 2 || events[0].kind != "progress" || events[1].kind != "end" {
		t.Fatalf("events = %+v, want progress then end", events)
	}
	if events[0].data != `{"examined":1,"total":1,"current":""}` {
		t.Errorf("snapshot = %q", events[0].data)
	}
	if events[1].data != `{"state":"done"}` {
		t.Errorf("end = %q", events[1].data)
	}
}

func TestSSEHeartbeat(t *testing.T) {
	old := sseHeartbeat
	sseHeartbeat = 50 * time.Millisecond
	t.Cleanup(func() { sseHeartbeat = old })

	d := newTestDeps(t)
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()
	ws := filepath.Join(rootOf(d), "ws")
	mkProject(t, filepath.Join(ws, "a"), "compose.yaml")
	dockerOf(d).hold(t)

	id := startScan(t, NewRouter(d), ws, 1)
	waitTotal(t, NewRouter(d), id, 1)
	r, closeFn := openSSE(t, srv.URL+scanURL(id, "/events"))
	defer closeFn()
	if ev, _ := r.next(); ev.kind != "progress" {
		t.Fatalf("first event = %+v", ev)
	}
	for range 2 {
		if ev, _ := r.next(); ev != (sseEvent{kind: ":", data: "ping"}) {
			t.Fatalf("event = %+v, want a ping comment", ev)
		}
	}
	dockerOf(d).release()
	for {
		ev, ok := r.next()
		if !ok {
			t.Fatal("stream ended before the end event")
		}
		if ev.kind == "end" {
			break
		}
	}
}

func TestNoSecretInResponses(t *testing.T) {
	const secret = "devpassword"
	d := newTestDeps(t)
	h := NewRouter(d)
	srv := httptest.NewServer(h)
	defer srv.Close()
	ws := filepath.Join(rootOf(d), "ws")
	shop := filepath.Join(ws, "shop")
	mkProject(t, shop, "compose.yaml")
	env := "POSTGRES_PASSWORD=" + secret + "\nDATABASE_URL=postgres://shop:" + secret + "@db:5432/shop\n"
	if err := os.WriteFile(filepath.Join(shop, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	dockerOf(d).setConfig(shop, configJSON("shop", map[string]service{
		"db":  {Image: "postgres:16", Environment: map[string]string{"POSTGRES_DB": "shop", "POSTGRES_PASSWORD": secret}},
		"web": {Image: "nginx", Environment: map[string]string{"DATABASE_URL": "postgres://shop:" + secret + "@db:5432/shop"}},
	}))

	id, job := scanDone(t, h, ws, 1)
	if len(job.Result.Rows) != 1 || len(job.Result.Rows[0].Databases) == 0 {
		t.Fatalf("rows = %+v, want shop with its database", job.Result.Rows)
	}
	db := job.Result.Rows[0].Databases[0]
	if db.Type != "postgresql" || db.Name != "shop" || db.LocationKind != "container" || db.Location != "db" {
		t.Errorf("database = %+v", db)
	}

	bodies := map[string]string{}
	bodies["scan"] = do(h, http.MethodGet, scanURL(id, "")).Body.String()
	events := readSSE(t, srv.URL+scanURL(id, "/events"))
	bodies["events"] = ""
	for _, ev := range events {
		bodies["events"] += ev.data
	}
	bodies["track"] = doJSON(h, http.MethodPost, scanURL(id, "/track"), trackAll(job)).Body.String()
	bodies["workspaces"] = do(h, http.MethodGet, "/api/workspaces").Body.String()
	bodies["projects"] = do(h, http.MethodGet, "/api/projects").Body.String()
	for name, body := range bodies {
		if strings.Contains(body, secret) {
			t.Errorf("%s response contains the secret: %s", name, body)
		}
	}
	if !strings.Contains(bodies["projects"], `"type":"postgresql"`) {
		t.Errorf("projects = %s, want the database", bodies["projects"])
	}
}
