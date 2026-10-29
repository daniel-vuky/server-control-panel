package api

import (
	"net/http"
	"time"
)

// listWorkspaces is GET /api/workspaces.
func (s *server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	ws, err := s.Store.Workspaces(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]Workspace, 0, len(ws))
	for _, x := range ws {
		out = append(out, Workspace{
			ID:         x.ID,
			Name:       x.Name,
			Path:       x.Path,
			Depth:      x.Depth,
			LastScanAt: x.LastScanAt.UTC().Format(time.RFC3339),
			Tracked:    x.Tracked,
			Ignored:    x.Ignored,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
