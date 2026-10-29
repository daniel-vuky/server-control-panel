package api

import (
	"net/http"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
)

// listProjects is GET /api/projects: the tracked projects, each with its
// status read live from one docker.containers call. When Docker cannot be
// read, every status is unknown and the list is still returned.
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
	out := make([]Project, 0, len(ts))
	for _, t := range ts {
		status := project.StatusUnknown
		if cerr == nil {
			status = project.StatusOf(containers, t.ComposeProject, t.Services)
		}
		out = append(out, Project{
			ID:          t.ID,
			DisplayName: t.DisplayName,
			Path:        t.Path,
			WorkspaceID: t.WorkspaceID,
			Self:        t.Self,
			Status:      string(status),
			Databases:   toDatabases(t.Databases),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
