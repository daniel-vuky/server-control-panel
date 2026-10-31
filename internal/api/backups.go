package api

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The history page sizes.
const (
	historyLimit    = 50
	maxHistoryLimit = 100
)

const (
	msgNoBackup       = "This backup does not exist."
	msgShuttingDown   = "The panel is shutting down."
	msgNotDownload    = "Only a backup that succeeded can be downloaded."
	msgDownloadFailed = "The panel could not complete this request. Try again."
	msgHistoryLimit   = "limit must be a number from 1 to 100."
	msgHistoryBefore  = "before must be a backup id."
)

// pathID is the positive {id} of r's path.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// toBackup maps a stored or active backup to the API type.
func toBackup(b store.Backup) Backup {
	steps := make([]BackupStep, 0, len(b.Steps))
	for _, st := range b.Steps {
		steps = append(steps, BackupStep{Step: st.Step, State: st.State, Detail: st.Detail})
	}
	return Backup{
		ID:          b.ID,
		ProjectID:   b.ProjectID,
		ProjectName: b.ProjectName,
		Database: BackupDatabase{
			Type: b.DBType, Name: b.DBName, LocationKind: b.DBLocationKind, Location: b.DBLocation, Key: b.DBKey,
		},
		State:         b.State,
		FailedStep:    b.FailedStep,
		Steps:         steps,
		Error:         b.Error,
		Hint:          b.Hint,
		RawBytes:      b.RawBytes,
		SizeBytes:     b.SizeBytes,
		FileName:      b.FileName,
		DriveLink:     b.DriveLink,
		StartedBy:     b.StartedBy,
		CreatedAt:     formatTime(&b.CreatedAt),
		StartedAt:     formatTime(b.StartedAt),
		FinishedAt:    formatTime(b.FinishedAt),
		DurationMS:    b.DurationMS,
		QueuePosition: b.QueuePosition,
	}
}

// toBackups maps backups to the API type. The slice is never nil.
func toBackups(bs []store.Backup) []Backup {
	out := make([]Backup, 0, len(bs))
	for _, b := range bs {
		out = append(out, toBackup(b))
	}
	return out
}

// activeByID is the manager's active backups by id: their in-memory progress
// is newer than the stored row.
func (s *server) activeByID() map[int64]store.Backup {
	active := s.Backups.Active()
	out := make(map[int64]store.Backup, len(active))
	for _, b := range active {
		out[b.ID] = b
	}
	return out
}

// enqueueBackup is POST /api/projects/{id}/backups: it queues a backup of one
// database of a tracked project and answers 202 {id}, or the refusal (spec
// §4): 404 for an unknown project, 422 for an unknown database, 409 for the
// rest.
func (s *server) enqueueBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	}
	var req BackupRequest
	if !readJSON(w, r, &req) {
		return
	}
	t, err := s.Store.TrackedProject(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	bid, err := s.Backups.Enqueue(r.Context(), t, req.Database, actor(r))
	s.backupQueued(w, r, bid, err)
}

// retryBackup is POST /api/backups/{id}/retry: a new backup of the same
// database, for a failed, refused or interrupted one (else 409
// not_retryable), answered like enqueueBackup.
func (s *server) retryBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, msgNoBackup)
		return
	}
	bid, err := s.Backups.Retry(r.Context(), id, actor(r))
	var refusal *backup.Refusal
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNoBackup)
		return
	case errors.As(err, &refusal) && refusal.Code == backup.CodeNotFound && !s.stillTracked(r, id):
		// A new backup of a project no longer tracked is a 404 too.
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	}
	s.backupQueued(w, r, bid, err)
}

// stillTracked says whether the project of backup id is still tracked. A
// read error counts as tracked, which keeps the manager's refusal.
func (s *server) stillTracked(r *http.Request, id int64) bool {
	b, err := s.Store.GetBackup(r.Context(), id)
	if err != nil {
		return true
	}
	_, err = s.Store.TrackedProject(r.Context(), b.ProjectID)
	return !errors.Is(err, store.ErrNotFound)
}

// backupQueued answers an Enqueue or a Retry: 202 {id}; 422 on database for
// an unknown database; 409 BackupRefused for another refusal, with the id
// only for already_running; 503 once the manager is closed.
func (s *server) backupQueued(w http.ResponseWriter, r *http.Request, id int64, err error) {
	var refusal *backup.Refusal
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, BackupStarted{ID: id})
	case errors.As(err, &refusal):
		if refusal.Code == backup.CodeUnknownDatabase {
			writeFieldError(w, "database", "unknown", refusal.Words)
			return
		}
		body := BackupRefused{
			Error: refusal.Code, Message: refusal.Words,
			NeedBytes: refusal.NeedBytes, FreeBytes: refusal.FreeBytes, Service: refusal.Service,
		}
		// The UI opens the progress of this id; a refused row's id is only
		// for the history.
		if refusal.Code == backup.CodeAlreadyRunning {
			body.ID = refusal.ID
		}
		writeJSON(w, http.StatusConflict, body)
	case errors.Is(err, backup.ErrClosed):
		writeError(w, http.StatusServiceUnavailable, msgShuttingDown)
	case r.Context().Err() != nil && errors.Is(err, r.Context().Err()):
		s.log.Info("backup: the client went away before the checks ended", "path", r.URL.Path)
	default:
		s.internalError(w, r, err)
	}
}

// projectBackups is GET /api/projects/{id}/backups?limit=50&before=<id>: a
// page of the project's backups, newest first, every state. It does not need
// the project to be tracked: the history outlives it.
func (s *server) projectBackups(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	}
	q := r.URL.Query()
	limit := historyLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxHistoryLimit {
			writeError(w, http.StatusBadRequest, msgHistoryLimit)
			return
		}
		limit = n
	}
	var before int64
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, msgHistoryBefore)
			return
		}
		before = n
	}
	bs, more, err := s.Store.ProjectBackups(r.Context(), id, before, limit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	active := s.activeByID()
	for i, b := range bs {
		if a, ok := active[b.ID]; ok {
			bs[i] = a
		}
	}
	writeJSON(w, http.StatusOK, BackupHistory{Backups: toBackups(bs), More: more})
}

// getBackup is GET /api/backups/{id}: an active backup from the manager (with
// its queue position), otherwise the stored row.
func (s *server) getBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, msgNoBackup)
		return
	}
	if b, ok := s.activeByID()[id]; ok {
		writeJSON(w, http.StatusOK, toBackup(b))
		return
	}
	b, err := s.Store.GetBackup(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNoBackup)
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, toBackup(b))
	}
}

// downloadBackup is GET /api/backups/{id}/download: it streams a succeeded
// backup's file from Drive. The browser opens it as a navigation, so every
// failure before the first byte is a short HTML page with a link back (404 for
// an unknown id, 409 when the backup did not succeed or Drive is not
// connected, 502 with the Drive words when Drive refused), never a body the
// browser would save as the file. A failure after the first byte cuts the
// connection so the download is never mistaken for complete. It is not
// recorded in activity.
func (s *server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		downloadPage(w, http.StatusNotFound, msgNoBackup, 0)
		return
	}
	b, err := s.Store.GetBackup(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		downloadPage(w, http.StatusNotFound, msgNoBackup, 0)
		return
	case err != nil:
		s.downloadFailed(w, r, err, 0)
		return
	}
	if b.State != backup.StateOK || b.DriveFileID == "" {
		downloadPage(w, http.StatusConflict, msgNotDownload, b.ProjectID)
		return
	}
	rc, err := s.Drive.Download(r.Context(), b.DriveFileID)
	if err != nil {
		s.downloadFailed(w, r, err, b.ProjectID)
		return
	}
	defer func() { _ = rc.Close() }()
	// Read the first byte before the headers, so an early failure can still be
	// answered.
	br := bufio.NewReaderSize(rc, 64<<10)
	if _, err := br.Peek(1); err != nil && !errors.Is(err, io.EOF) {
		s.downloadFailed(w, r, err, b.ProjectID)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/gzip")
	h.Set("Content-Disposition", `attachment; filename="`+safeFileName(b.FileName)+`"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	// Send the headers now: the browser shows the download at once, and a
	// cut below reads as a failed download.
	_ = http.NewResponseController(w).Flush()
	if _, err := io.Copy(w, br); err != nil {
		if r.Context().Err() != nil {
			// The browser cancelled the download: nothing went wrong here.
			s.log.Debug("backup: the download was cancelled", "backup", id)
		} else {
			s.log.Warn("backup: the download stopped", "backup", id, "code", drive.CodeOf(err))
		}
		// Abort the response: a clean end would look like a complete file.
		panic(http.ErrAbortHandler)
	}
}

// downloadFailed answers a download failure before the first byte as a page:
// 409 for Drive's own state (not configured, not connected), 502 with the Drive
// words when Drive refused, nothing when the client went away, else 500.
func (s *server) downloadFailed(w http.ResponseWriter, r *http.Request, err error, projectID int64) {
	if code, words, ok := driveCode(err); ok {
		status := http.StatusBadGateway
		if code == codeNotConfigured || code == codeNotConnected {
			status = http.StatusConflict
		}
		downloadPage(w, status, words, projectID)
		return
	}
	if r.Context().Err() != nil && errors.Is(err, r.Context().Err()) {
		s.log.Debug("backup: the download was cancelled", "path", r.URL.Path)
		return
	}
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	downloadPage(w, http.StatusInternalServerError, msgDownloadFailed, projectID)
}

// downloadPage is the page of a failed download, with a link back to the
// project's Database tab (or to Backups when the project is not known).
func downloadPage(w http.ResponseWriter, status int, message string, projectID int64) {
	back, text := "/backups", "Back to Backups"
	if projectID > 0 {
		back, text = "/projects/"+strconv.FormatInt(projectID, 10)+"/database", "Back to the project"
	}
	writePageBack(w, status, "The backup could not be downloaded", message, back, text)
}

// safeFileName keeps a file name safe inside a quoted header value: printable
// ASCII only, without " or \.
func safeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if name == "" {
		return "backup.gz"
	}
	return name
}
