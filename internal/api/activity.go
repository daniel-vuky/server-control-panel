package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The kinds of GET /api/activity and of each entry.
const (
	kindAction = "action"
	kindBackup = "backup"
	kindAlert  = "alert"
)

const (
	msgActivityKind    = "kind must be action, backup or alert."
	msgActivityBefore  = "before must be an activity id."
	msgActivityProject = "project must be a project id."
)

// activityKind is the kind an action is listed under: alert for alert.* (not
// alerts.*, the settings changes, which are a person's actions), backup for
// backup.run and backup.all, else action.
func activityKind(action string) string {
	switch {
	case strings.HasPrefix(action, "alert."):
		return kindAlert
	case action == store.ActBackupRun || action == store.ActBackupAll:
		return kindBackup
	}
	return kindAction
}

// listActivity is GET /api/activity?kind=&project=&before=&limit=: a page of
// the activity record, newest first by id, 50 by default and 100 at most.
// kind is action, backup or alert; project and before are ids. A bad
// parameter is 400 QueryError. It reads only the store.
func (s *server) listActivity(w http.ResponseWriter, r *http.Request) {
	f, qe := activityFilter(r)
	if qe != nil {
		writeJSON(w, http.StatusBadRequest, qe)
		return
	}
	rows, more, err := s.Store.ListActivity(r.Context(), f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	page := ActivityPage{Entries: make([]ActivityEntry, 0, len(rows)), More: more}
	for _, e := range rows {
		page.Entries = append(page.Entries, s.activityEntry(e))
	}
	writeJSON(w, http.StatusOK, page)
}

// activityFilter reads the query of GET /api/activity. An empty parameter
// is the same as none.
func activityFilter(r *http.Request) (store.ActivityFilter, *QueryError) {
	q := r.URL.Query()
	f := store.ActivityFilter{Limit: historyLimit}
	switch v := q.Get("kind"); v {
	case "", kindAction, kindBackup, kindAlert:
		f.Kind = v
	default:
		return f, &QueryError{Error: msgActivityKind, Field: "kind"}
	}
	if v := q.Get("project"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return f, &QueryError{Error: msgActivityProject, Field: "project"}
		}
		f.ProjectID = n
	}
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return f, &QueryError{Error: msgActivityBefore, Field: "before"}
		}
		f.Before = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxHistoryLimit {
			return f, &QueryError{Error: msgHistoryLimit, Field: "limit"}
		}
		f.Limit = n
	}
	return f, nil
}

// activityEntry maps a stored record: times in the server zone, a null
// project id for none and an empty channel list rather than null.
func (s *server) activityEntry(e store.ActivityEntry) ActivityEntry {
	out := ActivityEntry{
		ID: e.ID, At: s.zoneTime(e.StartedAt),
		Actor: e.Actor, ActorKind: e.ActorKind, Action: e.Action, Kind: activityKind(e.Action),
		ProjectTracked: e.ProjectTracked,
		Target:         e.Target, Result: e.Result, Reason: e.Reason,
		Channels: append([]string{}, e.Channels...),
	}
	if e.FinishedAt != nil {
		v := s.zoneTime(*e.FinishedAt)
		out.FinishedAt = &v
	}
	if e.ProjectID != 0 {
		id := e.ProjectID
		out.ProjectID = &id
	}
	return out
}
