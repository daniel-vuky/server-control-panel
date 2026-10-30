package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// containerLine is one docker.containers line in runner.ContainerColumns order.
func containerLine(proj, service, state, status string) string {
	return strings.Join([]string{proj, service, state, status, "", "False"}, "\t") + "\n"
}

func services(names ...string) project.Details {
	d := project.Details{Services: []project.Service{}}
	for _, n := range names {
		d.Services = append(d.Services, project.Service{Name: n, Image: "nginx"})
	}
	return d
}

// addProjects stores a workspace with alpha (web, db; tracked, one database),
// beta (web, worker; tracked) and gamma (ignored).
func addProjects(t *testing.T, d Deps) {
	t.Helper()
	ws := filepath.Join(rootOf(d), "ws")
	_, _, err := d.Store.AddWorkspace(context.Background(),
		store.NewWorkspace{Name: "ws", Path: ws, Depth: 1, LastScanAt: time.Now()},
		[]store.NewProject{
			{
				Path: filepath.Join(ws, "alpha"), ComposeFiles: []string{"compose.yaml"}, ComposeProject: "alpha",
				DisplayName: "Alpha", Tracked: true, Details: services("web", "db"),
				Databases: []project.Database{{Type: "postgresql", Name: "alpha", LocationKind: "container", Location: "db", Sources: []string{"service db"}}},
			},
			{
				Path: filepath.Join(ws, "beta"), ComposeFiles: []string{"compose.yaml"}, ComposeProject: "beta",
				DisplayName: "Beta", Tracked: true, Self: true, Details: services("web", "worker"),
			},
			{
				Path: filepath.Join(ws, "gamma"), ComposeFiles: []string{"compose.yaml"}, ComposeProject: "gamma",
				DisplayName: "Gamma", Details: services("web"),
			},
		})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectsLiveStatus(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	dockerOf(d).containers = containerLine("alpha", "web", "running", "Up 2 hours") +
		containerLine("alpha", "db", "running", "Up 2 hours") +
		containerLine("beta", "web", "running", "Up 2 hours") +
		containerLine("beta", "worker", "exited", "Exited (0) 1 hour ago") +
		containerLine("gamma", "web", "running", "Up 2 hours")

	rec := do(h, http.MethodGet, "/api/projects")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	got := decode[[]Project](t, rec)
	if len(got) != 2 {
		t.Fatalf("projects = %+v, want alpha and beta", got)
	}
	alpha, beta := got[0], got[1]
	if alpha.DisplayName != "Alpha" || alpha.Status != "running" || alpha.Self || alpha.WorkspaceID <= 0 ||
		alpha.Path != filepath.Join(rootOf(d), "ws", "alpha") {
		t.Errorf("alpha = %+v", alpha)
	}
	wantDB := Database{Type: "postgresql", Name: "alpha", LocationKind: "container", Location: "db", Sources: []string{"service db"}}
	if len(alpha.Databases) != 1 || alpha.Databases[0].Type != wantDB.Type || alpha.Databases[0].Location != wantDB.Location ||
		len(alpha.Databases[0].Sources) != 1 || alpha.Databases[0].Sources[0] != "service db" {
		t.Errorf("alpha databases = %+v, want %+v", alpha.Databases, wantDB)
	}
	if beta.DisplayName != "Beta" || beta.Status != "partial" || !beta.Self {
		t.Errorf("beta = %+v", beta)
	}
	if !strings.Contains(rec.Body.String(), `"databases":[]`) {
		t.Errorf("body = %s, want beta's databases as []", rec.Body.String())
	}
}

// TestProjectsDockerDownOnlyUntrack: with Docker down every status is unknown and
// no lifecycle action is offered; only untrack, which needs no Docker.
func TestProjectsDockerDownOnlyUntrack(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	dockerOf(d).containersErr = errors.New("Cannot connect to the Docker daemon")

	rec := do(h, http.MethodGet, "/api/projects")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	got := decode[[]Project](t, rec)
	if len(got) != 2 {
		t.Fatalf("projects = %+v", got)
	}
	for _, p := range got {
		if p.Status != "unknown" {
			t.Errorf("%s status = %q, want unknown", p.DisplayName, p.Status)
		}
		if strings.Join(p.Actions, ",") != "untrack" {
			t.Errorf("%s actions = %v, want only untrack with Docker down", p.DisplayName, p.Actions)
		}
	}
}

func TestEmptyListsAreArrays(t *testing.T) {
	h := NewRouter(newTestDeps(t))
	for _, path := range []string{"/api/projects", "/api/workspaces"} {
		rec := do(h, http.MethodGet, path)
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Errorf("GET %s = %d %q, want 200 []", path, rec.Code, rec.Body.String())
		}
	}
}

func TestProjectsIncludeActionsAndInFlight(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	f := dockerOf(d)
	f.set(func(f *fakeDocker) {
		f.containers = alphaUp() + containerLine("beta", "web", "running", "Up 2 hours")
	})

	rec := do(h, http.MethodGet, "/api/projects")
	got := decode[[]Project](t, rec)
	if len(got) != 2 {
		t.Fatalf("projects = %+v", got)
	}
	if got[0].Status != "running" || strings.Join(got[0].Actions, ",") != "stop,restart,untrack" {
		t.Errorf("alpha = %+v, want running with stop, restart and untrack", got[0])
	}
	if got[1].Status != "partial" || strings.Join(got[1].Actions, ",") != "untrack" {
		t.Errorf("beta (self) = %+v, want partial with only untrack", got[1])
	}
	if !strings.Contains(rec.Body.String(), `"actions":["untrack"]`) {
		t.Errorf("body = %s, want beta's actions as [untrack]", rec.Body.String())
	}

	f.holdLifecycle(t)
	done := postAsync(h, actionURL(alpha, "stop"))
	waitLocked(t, d, alpha)
	got = decode[[]Project](t, do(h, http.MethodGet, "/api/projects"))
	if got[0].Status != "stopping" || len(got[0].Actions) != 0 {
		t.Errorf("alpha while stopping = %+v, want stopping with no actions", got[0])
	}
	f.releaseLifecycle()
	<-done

	f.set(func(f *fakeDocker) { f.containersErr = errors.New("Cannot connect to the Docker daemon") })
	got = decode[[]Project](t, do(h, http.MethodGet, "/api/projects"))
	for _, p := range got {
		if p.Status != "unknown" || strings.Join(p.Actions, ",") != "untrack" {
			t.Errorf("%s with Docker down = %+v, want unknown with only untrack", p.DisplayName, p)
		}
	}
}
