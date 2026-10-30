package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// trackedIDs returns the ids of alpha and beta stored by addProjects.
func trackedIDs(t *testing.T, d Deps) (alpha, beta int64) {
	t.Helper()
	ts, err := d.Store.TrackedProjects(context.Background())
	if err != nil || len(ts) != 2 {
		t.Fatalf("tracked = %+v, %v", ts, err)
	}
	return ts[0].ID, ts[1].ID
}

func actionURL(id int64, action string) string {
	return "/api/projects/" + strconv.FormatInt(id, 10) + "/" + action
}

// alphaUp is the docker.containers output of alpha with both services running.
func alphaUp() string {
	return containerLine("alpha", "web", "running", "Up 2 hours") + containerLine("alpha", "db", "running", "Up 2 hours")
}

// activity returns the stored activity rows, newest first.
func activity(t *testing.T, d Deps) []store.ActivityRow {
	t.Helper()
	rows, err := d.Store.RecentActivity(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// waitFor polls cond until it holds, or fails the test after 10 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitLocked waits until a command runs on project id.
func waitLocked(t *testing.T, d Deps, id int64) {
	t.Helper()
	waitFor(t, "the lock", func() bool { _, ok := d.Control.InFlight(id); return ok })
}

// postAsync sends an action in the background; the returned channel gets the
// response once the handler returns.
func postAsync(h http.Handler, path string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- doJSON(h, http.MethodPost, path, "") }()
	return done
}

func TestActionStartOK(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	up := alphaUp()
	dockerOf(d).set(func(f *fakeDocker) { f.after = &up })

	rec := doJSON(h, http.MethodPost, actionURL(alpha, "start"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := decode[ActionResult](t, rec); got != (ActionResult{Outcome: "ok", Status: "running", Message: ""}) {
		t.Errorf("body = %+v", got)
	}
	if !strings.Contains(rec.Body.String(), `"message":""`) {
		t.Errorf("body = %s, want an empty message", rec.Body.String())
	}

	f := dockerOf(d)
	f.mu.Lock()
	runs := f.lifecycleRuns
	f.mu.Unlock()
	wantDir := filepath.Join(rootOf(d), "ws", "alpha")
	if len(runs) != 1 || runs[0]["dir"] != wantDir {
		t.Errorf("lifecycle runs = %+v, want one in %s", runs, wantDir)
	}

	rows := activity(t, d)
	if len(rows) != 1 {
		t.Fatalf("activity = %+v", rows)
	}
	r := rows[0]
	if r.Action != store.ActProjectStart || r.Result != store.ResultOK || r.Actor != "test@example.com" ||
		r.ActorKind != store.ActorPerson || r.ProjectID != alpha || r.Target != "Alpha" {
		t.Errorf("activity row = %+v", r)
	}
	if _, locked := d.Control.InFlight(alpha); locked {
		t.Error("alpha is still locked")
	}
}

func TestActionUnknownAction404(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)

	for _, action := range []string{"delete", "kill", "Start", "up", "start.override", "%20start"} {
		rec := doJSON(h, http.MethodPost, actionURL(alpha, action), "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s, want 404", action, rec.Code, rec.Body.String())
		}
	}
	if rec := doJSON(h, http.MethodPost, actionURL(alpha, "start")+"/x", ""); rec.Code != http.StatusNotFound {
		t.Errorf("start/x = %d, want 404", rec.Code)
	}
	if n := dockerOf(d).callCount(); n != 0 {
		t.Errorf("the fake ran %d commands, want none", n)
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("activity = %+v, want none", rows)
	}
}

func TestActionUnknownProject404(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	_, beta := trackedIDs(t, d)
	gamma := beta + 1 // ignored: stored right after beta

	for _, id := range []string{"9999", strconv.FormatInt(gamma, 10), "abc", "0", "-1", "1.5"} {
		rec := doJSON(h, http.MethodPost, "/api/projects/"+id+"/start", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s, want 404", id, rec.Code, rec.Body.String())
			continue
		}
		if got := decode[map[string]string](t, rec); got["error"] != "This project is not tracked." {
			t.Errorf("%s body = %v", id, got)
		}
	}
	if n := dockerOf(d).callCount(); n != 0 {
		t.Errorf("the fake ran %d commands, want none", n)
	}
}

func TestActionLocked409(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	up := alphaUp()
	f := dockerOf(d)
	f.set(func(f *fakeDocker) { f.after = &up })
	f.holdLifecycle(t)

	first := postAsync(h, actionURL(alpha, "start"))
	waitLocked(t, d, alpha)

	rec := doJSON(h, http.MethodPost, actionURL(alpha, "stop"), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("second = %d %s, want 409", rec.Code, rec.Body.String())
	}
	got := decode[ActionRefused](t, rec)
	if got.Code != "locked" || got.Status != "starting" || got.Error != "Alpha is Starting…; wait for it to finish." {
		t.Errorf("body = %+v", got)
	}

	f.releaseLifecycle()
	if r := <-first; r.Code != http.StatusOK || decode[ActionResult](t, r).Outcome != "ok" {
		t.Errorf("first = %d %s", r.Code, r.Body.String())
	}
	rows := activity(t, d)
	if len(rows) != 2 {
		t.Fatalf("activity = %+v", rows)
	}
	var refused int
	for _, r := range rows {
		if r.Result == store.ResultRefused && r.Action == store.ActProjectStop && r.Reason == got.Error {
			refused++
		}
	}
	if refused != 1 {
		t.Errorf("activity = %+v, want one refused stop", rows)
	}
}

func TestActionNotAllowed409(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, beta := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) {
		f.containers = alphaUp() + containerLine("beta", "web", "running", "Up 2 hours")
	})

	rec := doJSON(h, http.MethodPost, actionURL(alpha, "start"), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("start of a running project = %d %s, want 409", rec.Code, rec.Body.String())
	}
	got := decode[ActionRefused](t, rec)
	if got.Code != "not_allowed" || got.Status != "running" || got.Error != "Start is not allowed while Alpha is Running." {
		t.Errorf("body = %+v", got)
	}

	rec = doJSON(h, http.MethodPost, actionURL(beta, "stop"), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("stop of the panel = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if got := decode[ActionRefused](t, rec); got.Code != "not_allowed" || !strings.Contains(got.Error, "Monitor only") {
		t.Errorf("self body = %+v", got)
	}

	dockerOf(d).mu.Lock()
	runs := len(dockerOf(d).lifecycleRuns)
	dockerOf(d).mu.Unlock()
	if runs != 0 {
		t.Errorf("%d lifecycle commands ran, want none", runs)
	}
	if rows := activity(t, d); len(rows) != 2 || rows[0].Result != store.ResultRefused || rows[1].Result != store.ResultRefused {
		t.Errorf("activity = %+v, want two refusals", rows)
	}
}

func TestActionDockerDown503(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) {
		f.containersErr = &runner.ExitError{Code: 1, Stderr: "Cannot connect to the Docker daemon"}
	})

	rec := doJSON(h, http.MethodPost, actionURL(alpha, "start"), "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d %s, want 503", rec.Code, rec.Body.String())
	}
	got := decode[ActionRefused](t, rec)
	if got.Code != "docker_down" || got.Status != "unknown" || got.Error == "" {
		t.Errorf("body = %+v", got)
	}
	if rows := activity(t, d); len(rows) != 1 || rows[0].Result != store.ResultRefused {
		t.Errorf("activity = %+v, want one refusal", rows)
	}
}

func TestActionFailureMasksSecret(t *testing.T) {
	const secret = "devpassword"
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dir := filepath.Join(rootOf(d), "ws", "alpha")
	mkDir(t, dir)
	env := "POSTGRES_PASSWORD=" + secret + "\nDATABASE_URL=postgres://alpha:" + secret + "@db:5432/alpha\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	dockerOf(d).set(func(f *fakeDocker) {
		f.lifecycleErr = &runner.ExitError{Code: 1, Stderr: "Error: password authentication failed: " + secret}
	})

	rec := doJSON(h, http.MethodPost, actionURL(alpha, "start"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}
	got := decode[ActionResult](t, rec)
	if got.Outcome != "failed" || !strings.Contains(got.Message, "••••") {
		t.Errorf("body = %+v, want failed with a masked message", got)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Errorf("response contains the secret: %s", rec.Body.String())
	}
	rows := activity(t, d)
	if len(rows) != 1 || rows[0].Result != store.ResultFailed {
		t.Fatalf("activity = %+v", rows)
	}
	if strings.Contains(rows[0].Reason, secret) || !strings.Contains(rows[0].Reason, "••••") {
		t.Errorf("activity reason = %q", rows[0].Reason)
	}
}

func TestActionSurvivesClientDisconnect(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	up := alphaUp()
	f := dockerOf(d)
	f.set(func(f *fakeDocker) { f.after = &up })
	f.holdLifecycle(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, actionURL(alpha, "start"), nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	returned := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(returned)
	}()
	waitLocked(t, d, alpha)
	cancel()
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler did not return when the client went away")
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing written", rec.Body.String())
	}
	if _, locked := d.Control.InFlight(alpha); !locked {
		t.Error("the lock was released before the command finished")
	}

	f.releaseLifecycle()
	waitFor(t, "the lock release", func() bool { _, ok := d.Control.InFlight(alpha); return !ok })
	waitFor(t, "the finished activity row", func() bool {
		rows := activity(t, d)
		return len(rows) == 1 && rows[0].Result == store.ResultOK && rows[0].FinishedAt != nil
	})
}

func TestActionEmailFromVerifierNotHeader(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	up := alphaUp()
	dockerOf(d).set(func(f *fakeDocker) { f.after = &up })

	req := httptest.NewRequest(http.MethodPost, actionURL(alpha, "start"), nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Email", "evil@x")
	req.Header.Set("Cf-Access-Authenticated-User-Email", "evil@x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if rows := activity(t, d); len(rows) != 1 || rows[0].Actor != "test@example.com" {
		t.Errorf("activity = %+v, want the verifier's email", rows)
	}
}

func TestPostWithoutJSON415(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	const want = `{"error":"Send the request as application/json."}`

	paths := []string{
		"/api/scans",
		"/api/scans/1/track",
		actionURL(alpha, "start"),
		actionURL(alpha, "stop"),
		actionURL(alpha, "restart"),
		"/api/docker/check",
	}
	for _, path := range paths {
		for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/jsonx", "json"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"path":"/x","depth":1}`))
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
	if n := dockerOf(d).callCount(); n != 0 {
		t.Errorf("the fake ran %d commands, want none", n)
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("activity = %+v, want none", rows)
	}

	for _, ct := range []string{"application/json; charset=utf-8", "Application/JSON"} {
		req := httptest.NewRequest(http.MethodPost, "/api/docker/check", nil)
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("docker check with %q = %d %s, want 200", ct, rec.Code, rec.Body.String())
		}
	}
}
