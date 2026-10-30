package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// nextState reads events up to the next state event, skipping pings.
func nextState(t *testing.T, r *sseReader) LiveState {
	t.Helper()
	for {
		ev, ok := r.next()
		if !ok {
			t.Fatal("stream ended before a state event")
		}
		if ev.kind == ":" {
			continue
		}
		if ev.kind != "state" {
			t.Fatalf("event = %+v, want state", ev)
		}
		var s LiveState
		if err := json.Unmarshal([]byte(ev.data), &s); err != nil {
			t.Fatalf("state %q: %v", ev.data, err)
		}
		return s
	}
}

// stateUntil reads state events until cond holds for one, and returns it.
func stateUntil(t *testing.T, r *sseReader, what string, cond func(LiveState) bool) LiveState {
	t.Helper()
	for range 20 {
		if s := nextState(t, r); cond(s) {
			return s
		}
	}
	t.Fatalf("no state with %s", what)
	return LiveState{}
}

func TestEventsFirstStateOnConnect(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	dockerOf(d).set(func(f *fakeDocker) {
		f.containers = alphaUp() + containerLine("beta", "web", "running", "Up 2 hours")
	})
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()

	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	s := nextState(t, r)
	if !s.DockerUp || len(s.Projects) != 2 {
		t.Fatalf("state = %+v", s)
	}
	alpha := s.Projects[0]
	if alpha.DisplayName != "Alpha" || alpha.Status != "running" || len(alpha.Actions) != 2 ||
		len(alpha.Databases) != 1 || alpha.Databases[0].Type != "postgresql" {
		t.Errorf("alpha = %+v", alpha)
	}
	if beta := s.Projects[1]; beta.Status != "partial" || !beta.Self || beta.Actions == nil || len(beta.Actions) != 0 {
		t.Errorf("beta = %+v", beta)
	}
}

func TestEventsEmptyStateIsArrays(t *testing.T) {
	d := newTestDeps(t)
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()

	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	ev, ok := r.next()
	if !ok || ev.kind != "state" || ev.data != `{"dockerUp":true,"projects":[]}` {
		t.Errorf("first event = %+v", ev)
	}
}

func TestEventsPushOnLock(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	h := NewRouter(d)
	f := dockerOf(d)
	stopped := ""
	f.set(func(f *fakeDocker) { f.containers = alphaUp(); f.after = &stopped })
	srv := httptest.NewServer(h)
	defer srv.Close()

	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	if s := nextState(t, r); s.Projects[0].Status != "running" {
		t.Fatalf("first state = %+v", s)
	}

	f.holdLifecycle(t)
	done := postAsync(h, actionURL(alpha, "stop"))
	start := time.Now()
	stateUntil(t, r, "alpha stopping", func(s LiveState) bool { return s.Projects[0].Status == "stopping" })
	if time.Since(start) > 4*time.Second {
		t.Errorf("the lock took %v to arrive, want it without waiting for a tick", time.Since(start))
	}

	// The release pushes the re-read status at once, even when the re-read
	// found no container at all, and no state in between shows alpha running.
	f.releaseLifecycle()
	start = time.Now()
	s := nextState(t, r)
	if time.Since(start) > 4*time.Second {
		t.Errorf("the release took %v to arrive, want it without waiting for a tick", time.Since(start))
	}
	if s.Projects[0].Status != "stopped" || len(s.Projects[0].Actions) != 1 || s.Projects[0].Actions[0] != "start" {
		t.Errorf("alpha after stop = %+v, want stopped with start", s.Projects[0])
	}
	if rec := <-done; rec.Code != http.StatusOK {
		t.Errorf("stop = %d %s", rec.Code, rec.Body.String())
	}
}

func TestEventsHeartbeat(t *testing.T) {
	old := sseHeartbeat
	sseHeartbeat = 50 * time.Millisecond
	t.Cleanup(func() { sseHeartbeat = old })

	d := newTestDeps(t)
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()

	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	if ev, _ := r.next(); ev.kind != "state" {
		t.Fatalf("first event = %+v", ev)
	}
	for range 2 {
		if ev, _ := r.next(); ev != (sseEvent{kind: ":", data: "ping"}) {
			t.Fatalf("event = %+v, want a ping comment", ev)
		}
	}
}

func TestDockerCheck(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	h := NewRouter(d)
	f := dockerOf(d)
	f.set(func(f *fakeDocker) { f.containers = alphaUp() })
	srv := httptest.NewServer(h)
	defer srv.Close()

	rec := doJSON(h, http.MethodPost, "/api/docker/check", "")
	if rec.Code != http.StatusOK || decode[DockerCheck](t, rec) != (DockerCheck{DockerUp: true}) {
		t.Fatalf("check = %d %s, want dockerUp true", rec.Code, rec.Body.String())
	}

	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	if s := nextState(t, r); !s.DockerUp {
		t.Fatalf("first state = %+v", s)
	}

	f.set(func(f *fakeDocker) { f.pingErr = errors.New("Cannot connect to the Docker daemon") })
	rec = doJSON(h, http.MethodPost, "/api/docker/check", "")
	if rec.Code != http.StatusOK || decode[DockerCheck](t, rec) != (DockerCheck{DockerUp: false}) {
		t.Fatalf("check = %d %s, want dockerUp false", rec.Code, rec.Body.String())
	}
	s := stateUntil(t, r, "Docker down", func(s LiveState) bool { return !s.DockerUp })
	for _, p := range s.Projects {
		if p.Status != "unknown" || len(p.Actions) != 0 {
			t.Errorf("%s with Docker down = %+v", p.DisplayName, p)
		}
	}

	f.set(func(f *fakeDocker) { f.pingErr = nil })
	if rec := doJSON(h, http.MethodPost, "/api/docker/check", ""); decode[DockerCheck](t, rec) != (DockerCheck{DockerUp: true}) {
		t.Errorf("check after recovery = %s", rec.Body.String())
	}
	stateUntil(t, r, "Docker up", func(s LiveState) bool { return s.DockerUp })
}
