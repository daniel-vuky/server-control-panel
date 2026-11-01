package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The fields of AddDatabaseRequest, and the check's.
const (
	fieldDBType      = "type"
	fieldDBService   = "service"
	fieldDBName      = "name"
	fieldUserVar     = "userVar"
	fieldPasswordVar = "passwordVar"
	fieldCheck       = "check"
)

const (
	codeCheckFailed = "check_failed"

	msgDBType         = "Choose PostgreSQL, MySQL / MariaDB or MongoDB."
	msgDBService      = "Choose one of the project's services."
	msgDBNameRequired = "Enter the database name."
	msgDBName         = "Use letters, digits and _ . $ -, starting with a letter, digit or _, 64 characters at most."
	msgEnvName        = "Use letters, digits and _, not starting with a digit, 64 characters at most."
	msgEnvReserved    = "Use another variable name."
	msgUserVarToo     = "Enter the user variable too."
	msgPasswordVarToo = "Enter the password variable too."
	msgNoManual       = "This database was not added by hand to this project."
)

// dbTypeWords are the types a database can be added as, with the words the
// activity uses.
var dbTypeWords = map[string]string{
	"postgresql": "PostgreSQL",
	"mysql":      "MySQL / MariaDB",
	"mongodb":    "MongoDB",
}

// scriptVars are the names the database scripts use for their own variables
// (runner/dbscripts.go). A custom variable of the same name would read the
// script's value, not the container's, so it is refused.
var scriptVars = []string{"u", "pw", "f", "d", "c", "r", "a", "pf", "s", "k", "t", "o", "q", "n", "e", "i", "p"}

// addDatabase is POST /api/projects/{id}/databases: it checks a database
// inside its container and, when the check passes, stores it as added by hand
// and answers 201 with the Project. 404 for a project that is not tracked; 422
// on the first bad field (type, service, name, userVar, passwordVar), then on
// check (check_failed) when the check ran and failed; 409 BackupRefused for a
// missing project (not_found), a service that does not run (db_not_running)
// or one that is gone (not_supported), and 503 BackupRefused with Docker down
// (docker_down). Nothing is stored or recorded on any failure; a database
// added is recorded as database.add and pushed through Hub.Refresh.
func (s *server) addDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	}
	var req AddDatabaseRequest
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
	if fe := validAddDatabase(req, t.Services); fe != nil {
		writeJSON(w, http.StatusUnprocessableEntity, fe)
		return
	}
	db := project.Database{
		Type: req.Type, Name: req.Name, NameExplicit: true, LocationKind: "container", Location: req.Service,
		Sources: []string{store.ManualSource}, Manual: true, UserVar: req.UserVar, PasswordVar: req.PasswordVar,
	}
	if err := s.Backups.CheckDatabase(r.Context(), t, db); err != nil {
		s.checkFailed(w, r, err)
		return
	}

	// The write outlives the request, so a stored database keeps its row.
	ctx := context.WithoutCancel(r.Context())
	_, err = s.Store.AddManualDatabase(ctx, id, store.ManualDatabase{
		Type: req.Type, Name: req.Name, Service: req.Service, UserVar: req.UserVar, PasswordVar: req.PasswordVar,
		CreatedBy: actor(r),
	})
	switch {
	case errors.Is(err, store.ErrNotFound): // untracked meanwhile
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.record(r, store.Activity{Action: store.ActDatabaseAdd, ProjectID: id, Target: t.DisplayName + " · " + db.Name},
		store.ResultOK, describeDatabase(db))
	s.Hub.Refresh()
	s.answerProject(ctx, w, r, id, http.StatusCreated)
}

// checkFailed answers a CheckDatabase that did not pass: a refusal as
// BackupRefused (503 for docker_down, else 409), a failed check as 422 on
// check.
func (s *server) checkFailed(w http.ResponseWriter, r *http.Request, err error) {
	var (
		refusal *backup.Refusal
		failed  *backup.CheckError
	)
	switch {
	case errors.As(err, &refusal):
		status := http.StatusConflict
		if refusal.Code == backup.CodeDockerDown {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, BackupRefused{Error: refusal.Code, Message: refusal.Words, Service: refusal.Service})
	case errors.As(err, &failed):
		writeFieldError(w, fieldCheck, codeCheckFailed, failed.Words)
	case r.Context().Err() != nil && errors.Is(err, r.Context().Err()):
		s.log.Info("databases: the client went away during the check", "path", r.URL.Path)
	default:
		s.internalError(w, r, err)
	}
}

// removeDatabase is POST /api/projects/{id}/databases/{dbId}/remove: it
// deletes a hand-added database and its schedule (never a backup) and answers
// 200 with the Project. 404 for a project that is not tracked or a {dbId}
// that is not one of its hand-added databases; 409 BackupRefused busy while
// a backup of it is queued or running. A removal is recorded as
// database.remove and pushed through Hub.Refresh.
func (s *server) removeDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	dbID, err := strconv.ParseInt(r.PathValue("dbId"), 10, 64)
	if !ok || err != nil || dbID <= 0 {
		writeError(w, http.StatusNotFound, msgNoManual)
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
	i := slices.IndexFunc(t.Databases, func(d project.Database) bool { return d.Manual && d.ManualID == dbID })
	if i < 0 {
		writeError(w, http.StatusNotFound, msgNoManual)
		return
	}
	db := t.Databases[i]
	if s.Backups.Busy(id, project.DatabaseKey(db)) {
		writeJSON(w, http.StatusConflict, BackupRefused{
			Error: codeBusy, Message: "A backup of " + db.Name + " is queued or running. Remove it once the backup ends.",
		})
		return
	}
	ctx := context.WithoutCancel(r.Context())
	switch err := s.Store.RemoveManualDatabase(ctx, id, dbID); {
	case errors.Is(err, store.ErrNotFound): // removed meanwhile
		writeError(w, http.StatusNotFound, msgNoManual)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.record(r, store.Activity{Action: store.ActDatabaseRemove, ProjectID: id, Target: t.DisplayName + " · " + db.Name},
		store.ResultOK, describeDatabase(db))
	s.Hub.Refresh()
	s.answerProject(ctx, w, r, id, http.StatusOK)
}

// answerProject re-reads project id and answers it with status; 404 when it
// was untracked meanwhile.
func (s *server) answerProject(ctx context.Context, w http.ResponseWriter, r *http.Request, id int64, status int) {
	t, err := s.Store.TrackedProject(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, status, s.toProjects(s.readViews(ctx, []store.Tracked{t}))[0])
}

// validAddDatabase checks an AddDatabaseRequest in form order against the
// project's services: the type, the service, the name, then the variable
// names (both or neither, each a shell name that is not one of the scripts'
// own).
func validAddDatabase(req AddDatabaseRequest, services []string) *FieldError {
	fe := func(field, code, msg string) *FieldError { return &FieldError{Error: msg, Field: field, Code: code} }
	switch {
	case dbTypeWords[req.Type] == "":
		return fe(fieldDBType, "invalid", msgDBType)
	case req.Service == "" || !slices.Contains(services, req.Service):
		return fe(fieldDBService, "unknown", msgDBService)
	case req.Name == "":
		return fe(fieldDBName, "required", msgDBNameRequired)
	case !runner.ValidDBName(req.Name):
		return fe(fieldDBName, "invalid", msgDBName)
	}
	for _, v := range []struct{ field, name string }{{fieldUserVar, req.UserVar}, {fieldPasswordVar, req.PasswordVar}} {
		switch {
		case !runner.ValidEnvName(v.name):
			return fe(v.field, "invalid", msgEnvName)
		case slices.Contains(scriptVars, v.name):
			return fe(v.field, "reserved", msgEnvReserved)
		}
	}
	switch {
	case req.UserVar != "" && req.PasswordVar == "":
		return fe(fieldPasswordVar, "required", msgPasswordVarToo)
	case req.UserVar == "" && req.PasswordVar != "":
		return fe(fieldUserVar, "required", msgUserVarToo)
	}
	return nil
}

// describeDatabase is the activity reason of a hand-added database: its type,
// its service and where its credentials are read from. Names only.
func describeDatabase(d project.Database) string {
	creds := "standard variables"
	if d.UserVar != "" {
		creds = "user " + d.UserVar + ", password " + d.PasswordVar
	}
	return dbTypeWords[d.Type] + " in " + d.Location + " · " + creds
}
