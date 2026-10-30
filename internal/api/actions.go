package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

const msgNotTracked = "This project is not tracked."

// projectAction is POST /api/projects/{id}/{action}: it runs start, stop or
// restart on a tracked project and waits for the outcome. Any other action is
// 404 before the controller is called. A refusal is 409, or 503 when Docker
// is down. When the client goes away first, the command still finishes in
// the background and nothing is written.
func (s *server) projectAction(w http.ResponseWriter, r *http.Request) {
	a := project.Action(r.PathValue("action"))
	switch a {
	case project.ActionStart, project.ActionStop, project.ActionRestart:
	default:
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	}
	res, err := s.Control.Do(r.Context(), id, a, actor(r))
	var refusal *control.Refusal
	switch {
	case err == nil:
		actions := make([]string, 0, len(res.Actions))
		for _, act := range res.Actions {
			actions = append(actions, string(act))
		}
		writeJSON(w, http.StatusOK, ActionResult{Outcome: string(res.Outcome), Status: string(res.Status), Actions: actions, Message: res.Message})
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNotTracked)
	case errors.As(err, &refusal):
		status := http.StatusConflict
		if refusal.Code == control.CodeDockerDown {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, ActionRefused{Error: refusal.Message, Code: refusal.Code, Status: string(refusal.Status)})
	case r.Context().Err() != nil && errors.Is(err, r.Context().Err()):
		// The client went away; the command, if it started, still finishes.
		s.log.Info("project action: the client went away", "project", id, "action", a)
	default:
		s.internalError(w, r, err)
	}
}
