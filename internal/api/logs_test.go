package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/runner"
)

var errDockerDown = errors.New("cannot connect to the Docker daemon")

func logsURL(id int64, query string) string {
	u := "/api/projects/" + strconv.FormatInt(id, 10) + "/logs"
	if query != "" {
		u += "?" + query
	}
	return u
}

// ranLogs says whether the fake was asked for any compose.logs command.
func ranLogs(f *fakeDocker) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.ContainsFunc(f.calls, func(a runner.Action) bool {
		return isLogsTail(a) || a == runner.ComposeLogs || a == runner.ComposeLogsService ||
			a == runner.ComposeLogsOverride || a == runner.ComposeLogsServiceOverride
	})
}

// nextLogEvent reads the next event, skipping pings.
func nextLogEvent(t *testing.T, r *sseReader) sseEvent {
	t.Helper()
	for {
		ev, ok := r.next()
		if !ok {
			t.Fatal("stream ended early")
		}
		if ev.kind != "ping" {
			return ev
		}
	}
}

func decodeLines(t *testing.T, ev sseEvent) LogLines {
	t.Helper()
	if ev.kind != "lines" {
		t.Fatalf("event = %+v, want lines", ev)
	}
	var l LogLines
	if err := json.Unmarshal([]byte(ev.data), &l); err != nil {
		t.Fatalf("lines %q: %v", ev.data, err)
	}
	return l
}

// waitStream waits for the fake's next follow to start.
func waitStream(t *testing.T, f *fakeDocker) runner.Values {
	t.Helper()
	select {
	case v := <-f.streamStarted:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("no follow started")
	}
	return nil
}

// wantOnlyEnd checks that the stream at url answers 200 as SSE with exactly
// one end event with reason and message.
func wantOnlyEnd(t *testing.T, srvURL, path, reason, message string) {
	t.Helper()
	events := readSSE(t, srvURL+path)
	want := LogEnd{Reason: reason, Message: message}
	if len(events) != 1 || events[0].kind != "end" {
		t.Fatalf("events = %+v, want one end", events)
	}
	var got LogEnd
	if err := json.Unmarshal([]byte(events[0].data), &got); err != nil || got != want {
		t.Errorf("end = %s, want %+v", events[0].data, want)
	}
}

func TestLogs404(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	_, beta := trackedIDs(t, d)
	gamma := beta + 1 // stored right after beta, as ignored

	for _, id := range []string{"999", "0", "abc", strconv.FormatInt(gamma, 10)} {
		rec := do(h, http.MethodGet, "/api/projects/"+id+"/logs")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s, want 404", id, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s Content-Type = %q, want a plain JSON 404", id, ct)
		}
	}
	if ranLogs(dockerOf(d)) {
		t.Error("a logs command ran")
	}
}

func TestLogsUnknownServiceEnds(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()

	for _, q := range []string{"service=nope", "service=-web", "service=worker"} {
		wantOnlyEnd(t, srv.URL, logsURL(alpha, q), "unknown_service", "This project has no such service.")
	}
	if ranLogs(dockerOf(d)) {
		t.Error("a logs command ran")
	}
}

func TestLogsNotFoundEnds(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	markMissing(t, d)
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()

	wantOnlyEnd(t, srv.URL, logsURL(alpha, ""), "not_found", "The project folder is missing, so logs can't be read.")
	if ranLogs(dockerOf(d)) {
		t.Error("a logs command ran")
	}
}

func TestLogsDockerDownEnds(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containersErr = errDockerDown })
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()

	wantOnlyEnd(t, srv.URL, logsURL(alpha, "service=web"), "docker_down", "Docker is not responding, so logs can't be read.")
	if ranLogs(dockerOf(d)) {
		t.Error("a logs command ran")
	}
}

func TestLogsTooMany(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	alpha, beta := trackedIDs(t, d)
	f := dockerOf(d)
	f.set(func(f *fakeDocker) { f.containers = alphaUp() + containerLine("beta", "web", "running", "Up 2 hours") })
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()

	// Three follows: every service of alpha, then each of its two services.
	for _, q := range []string{"", "service=web", "service=db"} {
		r, closeFn := openSSE(t, srv.URL+logsURL(alpha, q))
		defer closeFn()
		if l := decodeLines(t, nextLogEvent(t, r)); len(l.Lines) != 0 {
			t.Errorf("%q backlog = %+v, want empty", q, l)
		}
		waitStream(t, f)
	}

	wantOnlyEnd(t, srv.URL, logsURL(beta, ""), "too_many_log_streams", "Too many log views are open on the panel. Close one and retry.")

	// A viewer of a running stream always gets in.
	r, closeFn := openSSE(t, srv.URL+logsURL(alpha, "service=web"))
	defer closeFn()
	decodeLines(t, nextLogEvent(t, r))
}

func TestLogsStreamsLinesThenEnd(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	f := dockerOf(d)
	f.set(func(f *fakeDocker) {
		f.containers = alphaUp()
		f.backlog = "web-1  | 2026-10-30T09:00:00.000000000Z first\n"
	})
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()

	r, closeFn := openSSE(t, srv.URL+logsURL(alpha, "service=web"))
	defer closeFn()
	ev := nextLogEvent(t, r)
	if want := `{"lines":[{"container":"alpha-web-1","service":"web","time":"2026-10-30T09:00:00Z","text":"first"}]}`; ev.data != want {
		t.Errorf("backlog = %s, want %s", ev.data, want)
	}
	if v := waitStream(t, f); v["service"] != "web" || v["since"] == "" {
		t.Errorf("follow values = %+v, want service web and a since", v)
	}

	f.follow <- "web-1  | 2026-10-30T09:00:01.5Z second"
	close(f.follow) // the follow exits on its own: every container stopped
	ev = nextLogEvent(t, r)
	if want := `{"lines":[{"container":"alpha-web-1","service":"web","time":"2026-10-30T09:00:01.5Z","text":"second"}]}`; ev.data != want {
		t.Errorf("live = %s, want %s", ev.data, want)
	}
	ev = nextLogEvent(t, r)
	if ev.kind != "end" || ev.data != `{"reason":"exited","message":""}` {
		t.Errorf("end = %+v, want exited", ev)
	}
	if ev, ok := r.next(); ok {
		t.Errorf("event after end = %+v, want the stream closed", ev)
	}
}

func TestLogsCancelOnDisconnect(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	f := dockerOf(d)
	f.set(func(f *fakeDocker) { f.containers = alphaUp() })
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()

	r, closeFn := openSSE(t, srv.URL+logsURL(alpha, ""))
	decodeLines(t, nextLogEvent(t, r))
	waitStream(t, f)

	closeFn()
	select {
	case <-f.streamCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the follow was not cancelled when the only viewer left")
	}
}
