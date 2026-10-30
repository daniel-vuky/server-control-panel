package api

import (
	"context"
	"net/http"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// listProjects is GET /api/projects: the tracked projects, each with its
// effective status read live from one docker.containers call (or its
// in-flight status while a command runs on it) and the actions it allows.
// When Docker cannot be read, every other status is unknown, a project
// allows only untrack, and the list is still returned. It runs no inspect or
// stats, so metrics, uptime, restart counts and health are left empty.
func (s *server) listProjects(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Store.TrackedProjects(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjects(s.readViews(r.Context(), ts)))
}

// readViews gives ts their views from one docker.containers read of its own
// (not the hub's cache), with no inspect or stats data. The Drive status is a
// placeholder until the API holds the Drive service.
func (s *server) readViews(ctx context.Context, ts []store.Tracked) []control.ProjectView {
	var containers []project.Container
	var cerr error
	if len(ts) > 0 {
		if containers, cerr = scan.ReadContainers(ctx, s.Docker); cerr != nil {
			s.log.Warn("projects: cannot read containers; every status is unknown", "err", cerr)
		}
	}
	return control.Views(ts, containers, cerr, cerr == nil, s.Control.Lock, nil, nil, drive.Status{State: drive.StateNotConnected})
}

// toProjects maps project views to the API type, shared by GET /api/projects
// and the state event. Slices are never nil.
func toProjects(vs []control.ProjectView) []Project {
	out := make([]Project, 0, len(vs))
	for _, v := range vs {
		actions := make([]string, 0, len(v.Actions))
		for _, a := range v.Actions {
			actions = append(actions, string(a))
		}
		p := Project{
			ID:          v.ID,
			DisplayName: v.DisplayName,
			Path:        v.Path,
			WorkspaceID: v.WorkspaceID,
			Self:        v.Self,
			Status:      string(v.Status),
			Actions:     actions,
			Databases:   toDatabases(v.Databases),

			MissingSince:    formatTime(v.MissingSince),
			PublicDomain:    v.PublicDomain,
			StopTemplate:    v.StopTemplate,
			AlertCPUPercent: v.AlertCPUPercent,
			AlertRAMMB:      v.AlertRAMMB,
			ComposeFiles:    nonNil(v.ComposeFiles),
			CPUPercent:      v.CPUPercent,
			MemBytes:        v.MemBytes,
			UpSince:         formatTime(v.UpSince),
			Containers:      toContainers(v.Components),
		}
		out = append(out, p)
	}
	return out
}

// toContainers maps a view's components to the API type. Slices are never
// nil.
func toContainers(cs []project.Component) []Container {
	out := make([]Container, 0, len(cs))
	for _, c := range cs {
		out = append(out, Container{
			Name:         c.Name,
			Service:      c.Service,
			State:        c.State,
			Status:       c.Status,
			Image:        c.Image,
			Ports:        nonNil(c.Ports),
			CPUPercent:   c.CPUPercent,
			MemBytes:     c.MemBytes,
			RestartCount: c.RestartCount,
			StartedAt:    formatTime(c.StartedAt),
			ExitReason:   c.ExitReason,
			Health:       c.Health,
		})
	}
	return out
}

// formatTime is t in UTC RFC 3339, "" when nil.
func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// toLiveState maps a hub snapshot to the data of the state event.
func toLiveState(st control.State) LiveState {
	return LiveState{DockerUp: st.DockerUp, Projects: toProjects(st.Projects)}
}
