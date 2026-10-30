package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	id := containerID(proj, service)
	return strings.Join([]string{proj, service, state, status, "", "False", id, proj + "-" + service + "-1", "img", ""}, "\t") + "\n"
}

// containerID is a stable 64-hex id per (project, service), as Docker gives.
func containerID(proj, service string) string {
	sum := sha256.Sum256([]byte(proj + "/" + service))
	return hex.EncodeToString(sum[:])
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
	rec = do(h, http.MethodGet, "/api/projects")
	got = decode[[]Project](t, rec)
	if got[0].Status != "stopping" || len(got[0].Actions) != 0 {
		t.Errorf("alpha while stopping = %+v, want stopping with no actions", got[0])
	}
	if !strings.Contains(rec.Body.String(), `"actions":[]`) {
		t.Errorf("body = %s, want alpha's actions as [] while stopping", rec.Body.String())
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

func TestProjectsCarrySettingsAndContainers(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	cpu, ram := 80, 2048
	if _, _, err := d.Store.UpdateSettings(context.Background(), alpha, store.Settings{
		DisplayName: "Alpha", PublicDomain: "alpha.example.com", StopTemplate: "down", AlertCPUPercent: &cpu, AlertRAMMB: &ram,
	}); err != nil {
		t.Fatal(err)
	}
	web := strings.Join([]string{"alpha", "web", "running", "Up 2 hours", "", "False", containerID("alpha", "web"),
		"alpha-web-1", "nginx:1.27", "0.0.0.0:8080->80/tcp, [::]:8080->80/tcp"}, "\t") + "\n"
	dockerOf(d).containers = web + containerLine("alpha", "db", "exited", "Exited (1) 3 minutes ago") +
		containerLine("beta", "web", "running", "Up 2 hours")

	rec := do(h, http.MethodGet, "/api/projects")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	got := decode[[]Project](t, rec)
	if len(got) != 2 {
		t.Fatalf("projects = %+v", got)
	}
	a, b := got[0], got[1]
	if a.PublicDomain != "alpha.example.com" || a.StopTemplate != "down" || a.AlertCPUPercent == nil || *a.AlertCPUPercent != 80 ||
		a.AlertRAMMB == nil || *a.AlertRAMMB != 2048 || strings.Join(a.ComposeFiles, ",") != "compose.yaml" {
		t.Errorf("alpha settings = %+v", a)
	}
	// GET /api/projects runs no inspect or stats: no metrics, no uptime.
	if a.CPUPercent != nil || a.MemBytes != nil || a.UpSince != "" {
		t.Errorf("alpha metrics = %v %v %q, want none", a.CPUPercent, a.MemBytes, a.UpSince)
	}
	wantA := []Container{
		{Name: "alpha-db-1", Service: "db", State: "exited", Status: "Exited", Image: "img", Ports: []string{}, ExitReason: "Exited (1)"},
		{Name: "alpha-web-1", Service: "web", State: "running", Status: "Running", Image: "nginx:1.27", Ports: []string{"8080→80"}},
	}
	if len(a.Containers) != len(wantA) {
		t.Fatalf("alpha containers = %+v, want %+v", a.Containers, wantA)
	}
	for i, c := range a.Containers {
		w := wantA[i]
		if c.Name != w.Name || c.Service != w.Service || c.State != w.State || c.Status != w.Status || c.Image != w.Image ||
			strings.Join(c.Ports, ",") != strings.Join(w.Ports, ",") || c.ExitReason != w.ExitReason ||
			c.CPUPercent != nil || c.MemBytes != nil || c.RestartCount != 0 || c.StartedAt != "" || c.Health != "" {
			t.Errorf("alpha container %d = %+v, want %+v", i, c, w)
		}
	}
	if b.PublicDomain != "" || b.StopTemplate != "stop" || b.AlertCPUPercent != nil || b.AlertRAMMB != nil {
		t.Errorf("beta settings = %+v, want the defaults", b)
	}
	if len(b.Containers) != 2 || b.Containers[1].Service != "worker" || b.Containers[1].Status != "Not created" {
		t.Errorf("beta containers = %+v, want web and a Not created worker", b.Containers)
	}
	body := rec.Body.String()
	for _, want := range []string{`"alertCpuPercent":null`, `"cpuPercent":null`, `"memBytes":null`, `"upSince":""`, `"ports":[]`, `"startedAt":""`} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, want %s", body, want)
		}
	}

	dockerOf(d).set(func(f *fakeDocker) { f.containersErr = errDockerDown })
	rec = do(h, http.MethodGet, "/api/projects")
	if !strings.Contains(rec.Body.String(), `"containers":[]`) {
		t.Errorf("body with Docker down = %s, want containers as []", rec.Body.String())
	}
}
