package api

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	msgPruned         = "This copy was removed from Drive by retention."
	msgListProject    = "project must be a project id."
	msgListResult     = "result must be ok or failed."
	msgListDate       = "Dates are YYYY-MM-DD."
	msgListRange      = "to must not be before from."
)

// dateLayout is the from and to query dates and the summary's day dates.
const dateLayout = "2006-01-02"

// stepSkipped is a step's state when it did not run (BackupStep.State).
const stepSkipped = "skipped"

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
		PrunedAt:      optTime(b.PrunedAt),
	}
}

// optTime is t in UTC RFC 3339, nil when nil.
func optTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := formatTime(t)
	return &v
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
	bid, err := s.Backups.Enqueue(r.Context(), t, req.Database, backup.Person(actor(r)))
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
	bid, err := s.Backups.Retry(r.Context(), id, backup.Person(actor(r)))
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
	limit, before, qe := pageParams(r)
	if qe != nil {
		writeJSON(w, http.StatusBadRequest, qe)
		return
	}
	bs, more, err := s.Store.ProjectBackups(r.Context(), id, before, limit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	stored, err := s.Store.StoredCopies(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BackupHistory{Backups: toBackups(s.withActive(bs)), More: more, Stored: stored})
}

// withActive replaces the rows the manager still runs or queues with their
// in-memory progress, which is newer than the stored row.
func (s *server) withActive(bs []store.Backup) []store.Backup {
	active := s.activeByID()
	for i, b := range bs {
		if a, ok := active[b.ID]; ok {
			bs[i] = a
		}
	}
	return bs
}

// pageParams reads the limit (1 to 100, 50 by default) and before (a backup
// id, 0 when absent) query parameters of a backup page, or the 400 body.
func pageParams(r *http.Request) (limit int, before int64, qe *QueryError) {
	q := r.URL.Query()
	limit = historyLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxHistoryLimit {
			return 0, 0, &QueryError{Error: msgHistoryLimit, Field: "limit"}
		}
		limit = n
	}
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return 0, 0, &QueryError{Error: msgHistoryBefore, Field: "before"}
		}
		before = n
	}
	return limit, before, nil
}

// listBackups is GET /api/backups?project=&result=&from=&to=&before=&limit=:
// a page of every project's backups, newest first (spec decision 21). result
// is ok or failed (failed, refused and interrupted); from and to are
// inclusive YYYY-MM-DD dates of the server zone, turned into UTC instants
// here. The queued and running rows come from the manager. A bad parameter is
// 400 QueryError.
func (s *server) listBackups(w http.ResponseWriter, r *http.Request) {
	f, qe := s.backupFilter(r)
	if qe != nil {
		writeJSON(w, http.StatusBadRequest, qe)
		return
	}
	bs, more, err := s.Store.ListBackups(r.Context(), f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BackupList{Backups: toBackups(s.withActive(bs)), More: more})
}

// backupFilter reads the query of GET /api/backups.
func (s *server) backupFilter(r *http.Request) (store.BackupFilter, *QueryError) {
	var f store.BackupFilter
	var qe *QueryError
	if f.Limit, f.Before, qe = pageParams(r); qe != nil {
		return f, qe
	}
	q := r.URL.Query()
	if v := q.Get("project"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return f, &QueryError{Error: msgListProject, Field: "project"}
		}
		f.ProjectID = n
	}
	switch v := q.Get("result"); v {
	case "", "ok", "failed":
		f.Result = v
	default:
		return f, &QueryError{Error: msgListResult, Field: "result"}
	}
	from, ok := s.parseDay(q.Get("from"))
	if !ok {
		return f, &QueryError{Error: msgListDate, Field: "from"}
	}
	to, ok := s.parseDay(q.Get("to"))
	if !ok {
		return f, &QueryError{Error: msgListDate, Field: "to"}
	}
	if !from.IsZero() {
		f.From = from.UTC()
	}
	if !to.IsZero() {
		if !from.IsZero() && to.Before(from) {
			return f, &QueryError{Error: msgListRange, Field: "to"}
		}
		// Inclusive: up to the start of the next day.
		f.To = s.dayStart(to, 1).UTC()
	}
	return f, nil
}

// parseDay reads a YYYY-MM-DD date as the start of that day in the server
// zone; "" is the zero time, ok false for anything else.
func (s *server) parseDay(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, true
	}
	t, err := time.ParseInLocation(dateLayout, v, s.Zone)
	if err != nil {
		return time.Time{}, false
	}
	return s.dayStart(t, 0), true
}

// dayStart is the start of the day days after t's day, in the server zone.
func (s *server) dayStart(t time.Time, days int) time.Time {
	y, m, d := t.In(s.Zone).Date()
	return time.Date(y, m, d+days, 0, 0, 0, 0, s.Zone)
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
// an unknown id, 410 when retention removed the file, 409 when the backup did
// not succeed or Drive is not connected, 502 with the Drive words when Drive
// refused), never a body the
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
	if b.PrunedAt != nil {
		downloadPage(w, http.StatusGone, msgPruned, b.ProjectID)
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
	name := safeFileName(b.FileName, compressed(b))
	h := w.Header()
	h.Set("Content-Type", contentTypeOf(name))
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
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
// ASCII only, without " or \. A row with no name gets backup.gz, or
// backup.sql when it was not compressed.
func safeFileName(name string, compressed bool) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	switch {
	case name != "":
		return name
	case compressed:
		return "backup.gz"
	default:
		return "backup.sql"
	}
}

// compressed says whether a backup's file is gzipped: its Compress step was
// not skipped ("Compression off").
func compressed(b store.Backup) bool {
	for _, st := range b.Steps {
		if st.Step == backup.StepCompress {
			return st.State != stepSkipped
		}
	}
	return true
}

// contentTypeOf is the download's type: gzip for a .gz name, else raw bytes.
func contentTypeOf(name string) string {
	if strings.HasSuffix(name, ".gz") {
		return "application/gzip"
	}
	return "application/octet-stream"
}

// backupAll is POST /api/backups/all: Back up all (spec decisions 17-20). It
// answers 202 {id, total, skipped} at once and the manager queues the
// databases one at a time; 409 BackupRefused when Drive is not connected,
// expired or has no folder, or batch_running while one runs; 503 once the
// manager is closed. With nothing to back up the batch starts and ends at
// once (total 0).
func (s *server) backupAll(w http.ResponseWriter, r *http.Request) {
	// The batch and its activity outlive the request.
	ctx := context.WithoutCancel(r.Context())
	ts, err := s.Store.TrackedProjects(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	b, err := s.Backups.StartAll(ctx, backup.Person(actor(r)), ts)
	var refusal *backup.Refusal
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, BackupAllResult{ID: b.ID, Total: b.Total, Skipped: b.Skipped})
	case errors.As(err, &refusal):
		writeJSON(w, http.StatusConflict, BackupRefused{Error: refusal.Code, Message: refusal.Words})
	case errors.Is(err, backup.ErrClosed):
		writeError(w, http.StatusServiceUnavailable, msgShuttingDown)
	default:
		s.internalError(w, r, err)
	}
}

// backupSummary is GET /api/backups/summary (spec decision 22): what the
// panel stored on Drive, the last 7 days of the server zone (today last), the
// newest failures and the next scheduled run.
func (s *server) backupSummary(w http.ResponseWriter, r *http.Request) {
	now := s.Now()
	starts := make([]time.Time, 0, 8)
	for i := -6; i <= 1; i++ {
		starts = append(starts, s.dayStart(now, i).UTC())
	}
	sum, err := s.Store.BackupSummary(r.Context(), starts)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	next, err := s.summaryNextRun(r.Context(), now)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := BackupSummary{
		DriveBytes: sum.DriveBytes, DriveFiles: sum.DriveFiles, DriveProjects: sum.DriveProjects,
		Days: make([]SummaryDay, 0, len(sum.Days)), OK7d: sum.OK7d, Failed7d: sum.Failed7d,
		RecentFailures: make([]SummaryFailure, 0, len(sum.RecentFailures)), NextRun: next,
	}
	for _, d := range sum.Days {
		out.Days = append(out.Days, SummaryDay{Date: d.Start.In(s.Zone).Format(dateLayout), OK: d.OK, Failed: d.Failed})
	}
	for _, b := range sum.RecentFailures {
		out.RecentFailures = append(out.RecentFailures, SummaryFailure{
			ProjectID: b.ProjectID, ProjectName: b.ProjectName, DBName: b.DBName, At: formatTime(&b.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
