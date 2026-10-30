package api

import (
	"net/http"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
)

// listProjects is GET /api/projects: the tracked projects, each with its
// effective status read live from one docker.containers call (or its
// in-flight status while a command runs on it) and the actions it allows.
// When Docker cannot be read, every other status is unknown, a project
// allows only untrack, and the list is still returned.
func (s *server) listProjects(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Store.TrackedProjects(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var containers []project.Container
	var cerr error
	if len(ts) > 0 {
		if containers, cerr = scan.ReadContainers(r.Context(), s.Docker); cerr != nil {
			s.log.Warn("projects: cannot read containers; every status is unknown", "err", cerr)
		}
	}
	writeJSON(w, http.StatusOK, toProjects(control.Views(ts, containers, cerr, cerr == nil, s.Control.Lock)))
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
		}
		if v.MissingSince != nil {
			p.MissingSince = v.MissingSince.UTC().Format(time.RFC3339)
		}
		out = append(out, p)
	}
	return out
}

// toLiveState maps a hub snapshot to the data of the state event.
func toLiveState(st control.State) LiveState {
	return LiveState{DockerUp: st.DockerUp, Projects: toProjects(st.Projects)}
}
