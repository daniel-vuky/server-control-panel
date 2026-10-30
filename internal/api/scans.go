package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/roots"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// maxDisplayName is the longest display name, in characters, after trimming.
const maxDisplayName = 80

// Messages the owner sees.
const (
	msgAlreadyAdded   = "This folder is already a workspace. Rescan it from Settings."
	msgBadDepth       = "Choose a depth of 1, 2 or 3."
	msgScanBusy       = "A scan is already running."
	msgScanNotFound   = "This scan does not exist."
	msgStopped        = "The scan stopped unexpectedly."
	msgNotDone        = "Projects can be added only from a scan that finished."
	msgRowsMismatch   = "Every project of the scan must be listed exactly once."
	msgAddedMeanwhile = "Some of these projects were added from another scan in the meantime. Scan this folder again."
)

// Codes and reasons of refused scans, as the field error and the activity
// record give them.
const (
	codeInvalidDepth = "invalid_depth"
	codeAlreadyAdded = "already_added"
	reasonBusy       = "busy"
	msgInternal      = "internal error" // the reason of a failed track
)

// Job states as the API reports them.
const (
	stateRunning = "running"
	stateDone    = "done"
	stateFailed  = "failed"
)

// startScan is POST /api/scans. A refusal (a path error, a bad depth, a
// folder already added, or another scan running) is recorded as a refused
// scan.start; the scan manager records a scan it starts.
func (s *server) startScan(w http.ResponseWriter, r *http.Request) {
	var req ScanRequest
	if !readJSON(w, r, &req) {
		return
	}
	refuse := func(target, reason string) {
		s.record(r, store.Activity{Action: store.ActScanStart, Target: target}, store.ResultRefused, reason)
	}
	resolved, err := s.Guard.Check(strings.TrimSpace(req.Path))
	if err != nil {
		var pe *roots.PathError
		if errors.As(err, &pe) {
			refuse(req.Path, pe.Code) // the raw path: the store cleans and cuts it
			writeFieldError(w, "path", pe.Code, pe.Message)
			return
		}
		s.internalError(w, r, err)
		return
	}
	if req.Depth < 1 || req.Depth > 3 {
		refuse(resolved, codeInvalidDepth)
		writeFieldError(w, "depth", codeInvalidDepth, msgBadDepth)
		return
	}
	switch _, err := s.Store.WorkspaceByPath(r.Context(), resolved); {
	case err == nil:
		refuse(resolved, codeAlreadyAdded)
		writeFieldError(w, "path", codeAlreadyAdded, msgAlreadyAdded)
		return
	case !errors.Is(err, store.ErrNotFound):
		s.internalError(w, r, err)
		return
	}

	id, err := s.Scans.Start(r.Context(), resolved, req.Depth, actor(r))
	if err != nil {
		var busy *scan.BusyError
		if errors.As(err, &busy) {
			refuse(resolved, reasonBusy)
			writeJSON(w, http.StatusConflict, toBusy(busy))
			return
		}
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, ScanStarted{ID: id})
}

// getScan is GET /api/scans/{id}.
func (s *server) getScan(w http.ResponseWriter, r *http.Request) {
	job, ok := s.loadJob(w, r)
	if !ok {
		return
	}
	var progress ScanProgress
	live := false
	if job.State == stateRunning {
		var p scan.Progress
		if p, live = s.Scans.Progress(job.ID); live {
			progress = toProgress(p)
		} else {
			var err error
			if job, err = s.settle(r.Context(), job.ID); err != nil {
				s.internalError(w, r, err)
				return
			}
		}
	}
	if !live {
		progress = storedProgress(job)
	}
	view := ScanJob{
		ID:           job.ID,
		State:        job.State,
		Path:         job.Target,
		Depth:        job.Depth,
		Rescan:       len(job.Rescan) > 0,
		WorkspaceIDs: nonNil(job.Rescan),
		Progress:     progress,
		Error:        job.Error,
	}
	if job.State == stateDone {
		res, err := jobResult(job)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		view.Result = toResult(res)
	}
	writeJSON(w, http.StatusOK, view)
}

// trackScan is POST /api/scans/{id}/track. For a first-time scan it stores
// the scanned folder as a workspace and every row of the result as a tracked
// or ignored project, in one transaction; a rescan job goes to trackRescan.
// Every answer after the job is found is recorded as a scan.track: ok,
// refused (409 or 422, with the message) or failed (500).
func (s *server) trackScan(w http.ResponseWriter, r *http.Request) {
	job, ok := s.loadJob(w, r)
	if !ok {
		return
	}
	rec := store.Activity{Action: store.ActScanTrack, Target: job.Target}
	refuse := func(status int, msg string) {
		s.record(r, rec, store.ResultRefused, msg)
		writeError(w, status, msg)
	}
	fail := func(err error) {
		s.record(r, rec, store.ResultFailed, msgInternal)
		s.internalError(w, r, err)
	}

	if job.State != stateDone {
		refuse(http.StatusConflict, msgNotDone)
		return
	}
	var req TrackRequest
	if !readJSON(w, r, &req) {
		return
	}
	res, err := jobResult(job)
	if err != nil {
		fail(err)
		return
	}
	if len(job.Rescan) > 0 {
		s.trackRescan(w, r, job, res, req.Projects)
		return
	}
	projects, fe := newProjects(res.Rows, req.Projects)
	if fe != nil {
		s.record(r, rec, store.ResultRefused, fe.Error)
		writeJSON(w, http.StatusUnprocessableEntity, fe)
		return
	}

	ctx := r.Context()
	switch _, err := s.Store.WorkspaceByPath(ctx, job.Target); {
	case err == nil:
		refuse(http.StatusConflict, msgAlreadyAdded)
		return
	case !errors.Is(err, store.ErrNotFound):
		fail(err)
		return
	}
	// A project of this result may have been stored by another scan's track
	// since this scan ran (an overlapping workspace). Refuse rather than fail
	// on the unique path.
	known, err := s.Store.KnownProjects(ctx)
	if err != nil {
		fail(err)
		return
	}
	for _, p := range projects {
		if _, ok := known[p.Path]; ok {
			refuse(http.StatusConflict, msgAddedMeanwhile)
			return
		}
	}

	last := job.StartedAt
	if job.FinishedAt != nil {
		last = *job.FinishedAt
	}
	wsID, added, err := s.Store.AddWorkspace(ctx, store.NewWorkspace{
		Name:       filepath.Base(job.Target),
		Path:       job.Target,
		Depth:      job.Depth,
		LastScanAt: last,
	}, projects)
	if errors.Is(err, store.ErrAlreadyAdded) {
		refuse(http.StatusConflict, msgAlreadyAdded)
		return
	}
	if err != nil {
		fail(err)
		return
	}
	s.record(r, rec, store.ResultOK, "")
	s.Hub.Refresh()
	s.log.Info("workspace added", "workspace", wsID, "path", job.Target, "tracked", added, "projects", len(projects))
	writeJSON(w, http.StatusOK, TrackResponse{WorkspaceID: wsID, Added: added})
}

// newProjects checks a track request against the scan's rows: every row once,
// nothing else, and every display name 1 to 80 characters after trimming. It
// returns the projects to store, in the result's order.
func newProjects(rows []scan.Row, items []TrackItem) ([]store.NewProject, *FieldError) {
	mismatch := &FieldError{Error: msgRowsMismatch, Field: "projects", Code: "projects_mismatch"}
	byPath := make(map[string]TrackItem, len(items))
	for _, it := range items {
		if _, dup := byPath[it.Path]; dup {
			return nil, mismatch
		}
		byPath[it.Path] = it
	}
	if len(byPath) != len(rows) {
		return nil, mismatch
	}
	ps := make([]store.NewProject, 0, len(rows))
	for _, row := range rows {
		it, ok := byPath[row.Path]
		if !ok {
			return nil, mismatch
		}
		name, fe := displayName(row.Path, it.DisplayName)
		if fe != nil {
			return nil, fe
		}
		ps = append(ps, store.NewProject{
			Path:           row.Path,
			ComposeFiles:   row.ComposeFiles,
			ComposeProject: row.ComposeProject,
			DisplayName:    name,
			Tracked:        it.Track,
			Self:           row.Self,
			Details:        row.Details,
			Databases:      row.Databases,
		})
	}
	return ps, nil
}

// displayName trims a display name and checks it is 1 to 80 characters.
func displayName(path, raw string) (string, *FieldError) {
	name := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxDisplayName {
		return "", &FieldError{
			Error: fmt.Sprintf("The name of %s must be 1 to %d characters.", path, maxDisplayName),
			Field: "projects",
			Code:  "invalid_name",
		}
	}
	return name, nil
}

// toBusy is the 409 body for a running scan or rescan.
func toBusy(b *scan.BusyError) ScanBusy {
	return ScanBusy{Error: msgScanBusy, ID: b.ID, Path: b.Path, Depth: b.Depth, Rescan: b.Rescan}
}

// loadJob reads the scan job named by the {id} path value. It writes a 404
// for an id that is malformed, unknown or not a scan, and returns false.
func (s *server) loadJob(w http.ResponseWriter, r *http.Request) (store.Job, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, msgScanNotFound)
		return store.Job{}, false
	}
	job, err := s.Store.Job(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && job.Kind != "scan") {
		writeError(w, http.StatusNotFound, msgScanNotFound)
		return store.Job{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return store.Job{}, false
	}
	return job, true
}

// settle re-reads a job that the store said was running but the manager no
// longer runs. The manager stores the final state before it lets go of a job,
// so the re-read normally finds it finished. A job still stored as running is
// one whose final write failed: it is reported as failed.
func (s *server) settle(ctx context.Context, id int64) (store.Job, error) {
	job, err := s.Store.Job(ctx, id)
	if err != nil {
		return store.Job{}, err
	}
	if job.State == stateRunning {
		job.State, job.Error = stateFailed, msgStopped
	}
	return job, nil
}

// storedProgress is the progress saved with a finished job; zero when none.
func storedProgress(job store.Job) ScanProgress {
	var p scan.Progress
	if len(job.Progress) > 0 {
		_ = json.Unmarshal(job.Progress, &p) // a bad value shows as no progress
	}
	return toProgress(p)
}

func jobResult(job store.Job) (scan.Result, error) {
	var res scan.Result
	if err := json.Unmarshal(job.Result, &res); err != nil {
		return scan.Result{}, fmt.Errorf("scan %d result: %w", job.ID, err)
	}
	return res, nil
}

func toProgress(p scan.Progress) ScanProgress {
	return ScanProgress{
		Examined:   p.Examined,
		Total:      p.Total,
		Current:    p.Current,
		Workspace:  p.Workspace,
		Workspaces: p.Workspaces,
	}
}

func toResult(r scan.Result) *ScanResult {
	rows := make([]ScanRow, 0, len(r.Rows))
	for _, row := range r.Rows {
		rows = append(rows, ScanRow{
			Path:         row.Path,
			ComposeFiles: nonNil(row.ComposeFiles),
			DefaultName:  row.DefaultName,
			Status:       string(row.Status),
			Self:         row.Self,
			Services:     row.Services,
			Databases:    toDatabases(row.Databases),
			Warning:      row.Warning,
			WorkspaceID:  row.WorkspaceID,
			New:          row.New,
			Ignored:      row.Ignored,
		})
	}
	return &ScanResult{
		Rows:           rows,
		FoldersScanned: r.FoldersScanned,
		AlreadyTracked: r.AlreadyTracked,
		Ignored:        r.Ignored,
		Warnings:       nonNil(r.Warnings),
		New:            r.New,
		IgnoredFound:   r.IgnoredFound,
		Refreshed:      r.Refreshed,
		NotFound:       r.NotFound,
		NotFoundPaths:  nonNil(r.NotFoundPaths),
		Workspaces:     nonNil(r.Workspaces),
	}
}

func toDatabases(ds []project.Database) []Database {
	out := make([]Database, 0, len(ds))
	for _, d := range ds {
		out = append(out, Database{
			Type:         d.Type,
			Name:         d.Name,
			LocationKind: d.LocationKind,
			Location:     d.Location,
			Sources:      nonNil(d.Sources),
		})
	}
	return out
}

// nonNil returns s, or an empty slice for nil, so it encodes as [] not null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
