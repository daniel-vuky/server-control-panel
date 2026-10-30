package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// Messages of the workspace management endpoints.
const (
	msgWorkspaceNotFound = "This workspace does not exist."
	msgNoWorkspace       = "There is no workspace to rescan."
	msgRemoveWhileScan   = "A scan is running; try again when it ends."
	msgRescanTracked     = "These projects were already added."
	msgWorkspaceGone     = "This workspace was removed meanwhile."
	msgRescanMismatch    = "List only the new or ignored projects of this rescan to add, each once."
	msgHeldForRemoval    = " is being removed from the panel."
)

// targetAllWorkspaces is the activity target of a rescan of every workspace,
// as scan.Manager stores it.
const targetAllWorkspaces = "all workspaces"

// startRescan is POST /api/rescans: a rescan of one workspace, or of every
// workspace when workspaceId is 0. The scan manager checks each workspace with
// the guard itself and records the rescan it starts; a refusal (no workspace,
// or another scan running) is recorded here as a refused workspace.rescan.
func (s *server) startRescan(w http.ResponseWriter, r *http.Request) {
	var req RescanRequest
	if !readJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	target := targetAllWorkspaces
	var ws []store.Workspace
	if req.WorkspaceID != 0 {
		one, ok := s.loadWorkspace(w, r, req.WorkspaceID)
		if !ok {
			return
		}
		ws, target = []store.Workspace{one}, one.Path
	} else {
		var err error
		if ws, err = s.Store.Workspaces(ctx); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	refuse := func(reason string) {
		s.record(r, store.Activity{Action: store.ActWorkspaceRescan, Target: target}, store.ResultRefused, reason)
	}
	if len(ws) == 0 {
		refuse(msgNoWorkspace)
		writeError(w, http.StatusConflict, msgNoWorkspace)
		return
	}

	id, err := s.Scans.Rescan(ctx, ws, actor(r))
	var busy *scan.BusyError
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, ScanStarted{ID: id})
	case errors.As(err, &busy):
		refuse(reasonBusy)
		writeJSON(w, http.StatusConflict, toBusy(busy))
	default:
		s.internalError(w, r, err)
	}
}

// removeWorkspace is POST /api/workspaces/{id}/remove: it deletes the
// workspace with its projects. The delete runs while no scan runs (so none
// can start on it meanwhile) and while the locks of its tracked projects are
// held (so no command starts on them meanwhile). Every answer after the
// workspace is found is recorded as a workspace.remove.
func (s *server) removeWorkspace(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		id = 0 // loadWorkspace answers 404
	}
	wsp, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}
	// The write outlives the request: a client that goes away does not leave
	// it half known.
	ctx := context.WithoutCancel(r.Context())
	rec := store.Activity{Action: store.ActWorkspaceRemove, Target: wsp.Path}
	refuse := func(status int, msg string) {
		s.record(r, rec, store.ResultRefused, msg)
		writeError(w, status, msg)
	}

	var (
		removed store.Workspace
		names   map[int64]string // the workspace's tracked projects by id
	)
	err = s.Scans.WhileIdle(func() error {
		ts, err := s.Store.TrackedProjects(ctx)
		if err != nil {
			return err
		}
		names = map[int64]string{}
		var ids []int64
		for _, t := range ts {
			if t.WorkspaceID == id {
				ids = append(ids, t.ID)
				names[t.ID] = t.DisplayName
			}
		}
		return s.Control.WithLocks(ids, func() error {
			var err error
			removed, err = s.Store.RemoveWorkspace(ctx, id)
			return err
		})
	})
	var (
		busy    *scan.BusyError
		refusal *control.Refusal
	)
	switch {
	case err == nil:
	case errors.As(err, &busy):
		refuse(http.StatusConflict, msgRemoveWhileScan)
		return
	case errors.As(err, &refusal):
		refuse(http.StatusConflict, lockedMessage(refusal, names))
		return
	case errors.Is(err, store.ErrNotFound): // removed by another request meanwhile
		writeError(w, http.StatusNotFound, msgWorkspaceNotFound)
		return
	default:
		s.record(r, rec, store.ResultFailed, msgInternal)
		s.internalError(w, r, err)
		return
	}
	s.record(r, rec, store.ResultOK, "")
	s.Hub.Refresh() // the lock release did not publish; see Controller.WithLocks
	s.log.Info("workspace removed", "workspace", id, "path", removed.Path, "tracked", removed.Tracked)
	writeJSON(w, http.StatusOK, RemoveResponse{Removed: removed.Tracked})
}

// lockedMessage names the project a WithLocks refusal is about, from the
// workspace's projects; it falls back to the refusal's own message.
func lockedMessage(r *control.Refusal, names map[int64]string) string {
	name, ok := names[r.ProjectID]
	switch {
	case !ok:
		return r.Message
	case r.Held:
		return name + msgHeldForRemoval
	default:
		return name + " is " + project.Word(r.Status) + "; wait for it to finish."
	}
}

// untrackProject is POST /api/projects/{id}/untrack: a present project
// becomes ignored, and one a rescan could not find is removed from the list.
// The controller records it, refusals included.
func (s *server) untrackProject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	}
	// The write outlives the request, as for removeWorkspace.
	gone, err := s.Control.Untrack(context.WithoutCancel(r.Context()), id, actor(r))
	var refusal *control.Refusal
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case errors.As(err, &refusal):
		writeJSON(w, http.StatusConflict, ActionRefused{Error: refusal.Message, Code: refusal.Code, Status: string(refusal.Status)})
		return
	default:
		s.internalError(w, r, err)
		return
	}
	s.Hub.Refresh() // the lock release did not publish; see Controller.WithLocks
	s.log.Info("project untracked", "project", id, "path", gone.Path, "missing", gone.MissingSince != nil)
	writeJSON(w, http.StatusOK, UntrackResponse{Removed: true})
}

// trackRescan is POST /api/scans/{id}/track for a rescan job. The request
// lists only the rows to track; the New rows it leaves out are stored as
// ignored, so the next rescan does not offer them as New again. It is
// recorded as a scan.track like a first-time track.
func (s *server) trackRescan(w http.ResponseWriter, r *http.Request, job store.Job, res scan.Result, items []TrackItem) {
	rec := store.Activity{Action: store.ActScanTrack, Target: job.Target}
	refuse := func(status int, msg string) {
		s.record(r, rec, store.ResultRefused, msg)
		writeError(w, status, msg)
	}

	adds, fe := rescanItems(res.Rows, items)
	if fe != nil {
		s.record(r, rec, store.ResultRefused, fe.Error)
		writeJSON(w, http.StatusUnprocessableEntity, fe)
		return
	}
	added, err := s.Store.AddProjects(r.Context(), job.ID, adds)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrAlreadyTracked):
		refuse(http.StatusConflict, msgRescanTracked)
		return
	case errors.Is(err, store.ErrAddedMeanwhile):
		refuse(http.StatusConflict, msgAddedMeanwhile)
		return
	case errors.Is(err, store.ErrWorkspaceGone):
		refuse(http.StatusConflict, msgWorkspaceGone)
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgScanNotFound)
		return
	default:
		s.record(r, rec, store.ResultFailed, msgInternal)
		s.internalError(w, r, err)
		return
	}
	s.record(r, rec, store.ResultOK, "")
	s.Hub.Refresh()
	s.log.Info("rescan projects added", "job", job.ID, "target", job.Target, "tracked", added, "stored", len(adds))
	writeJSON(w, http.StatusOK, TrackResponse{WorkspaceID: 0, Added: added})
}

// rescanItems checks a rescan's track request against its rows: each item a
// New or Ignored row of the result, once, with track true and a display name
// of 1 to 80 characters after trimming. It returns what to store, in the
// result's order: the listed rows tracked under their names, and the New rows
// not listed as ignored under their default names. Ignored rows not listed
// are left as they are.
func rescanItems(rows []scan.Row, items []TrackItem) ([]store.AddItem, *FieldError) {
	mismatch := &FieldError{Error: msgRescanMismatch, Field: "projects", Code: "projects_mismatch"}
	offered := make(map[string]bool, len(rows))
	for _, row := range rows {
		offered[row.Path] = row.New || row.Ignored
	}
	byPath := make(map[string]TrackItem, len(items))
	for _, it := range items {
		if _, dup := byPath[it.Path]; dup || !it.Track || !offered[it.Path] {
			return nil, mismatch
		}
		byPath[it.Path] = it
	}

	out := make([]store.AddItem, 0, len(rows))
	for _, row := range rows {
		p := store.NewProject{
			Path:           row.Path,
			ComposeFiles:   row.ComposeFiles,
			ComposeProject: row.ComposeProject,
			Self:           row.Self,
			Details:        row.Details,
			Databases:      row.Databases,
		}
		if it, listed := byPath[row.Path]; listed {
			name, fe := displayName(row.Path, it.DisplayName)
			if fe != nil {
				return nil, fe
			}
			p.DisplayName, p.Tracked = name, true
		} else if row.New {
			p.DisplayName = row.DefaultName
		} else {
			continue
		}
		out = append(out, store.AddItem{WorkspaceID: row.WorkspaceID, NewProject: p})
	}
	return out, nil
}

// loadWorkspace reads workspace id. It writes a 404 for an id that is not
// positive or unknown, and returns false.
func (s *server) loadWorkspace(w http.ResponseWriter, r *http.Request, id int64) (store.Workspace, bool) {
	if id <= 0 {
		writeError(w, http.StatusNotFound, msgWorkspaceNotFound)
		return store.Workspace{}, false
	}
	x, err := s.Store.WorkspaceByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, msgWorkspaceNotFound)
		return store.Workspace{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return store.Workspace{}, false
	}
	return x, true
}
