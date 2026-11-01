package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

func databasesURL(id int64) string { return actionURL(id, "databases") }

func removeDatabaseURL(id, dbID int64) string {
	return databasesURL(id) + "/" + strconv.FormatInt(dbID, 10) + "/remove"
}

// databasesSetup is alpha and beta (addProjects) with alpha running. It
// returns alpha's id.
func databasesSetup(t *testing.T) (Deps, http.Handler, int64) {
	t.Helper()
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	return d, h, alpha
}

// manualOf is p's hand-added databases.
func manualOf(p Project) []Database {
	var out []Database
	for _, db := range p.Databases {
		if db.Manual {
			out = append(out, db)
		}
	}
	return out
}

// storedManual is the hand-added databases stored for project id.
func storedManual(t *testing.T, d Deps, id int64) int {
	t.Helper()
	tr, err := d.Store.TrackedProject(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, db := range tr.Databases {
		if db.Manual {
			n++
		}
	}
	return n
}

func checksOf(d Deps) []call {
	f := dockerOf(d)
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.checks...)
}

func TestAddDatabase(t *testing.T) {
	d, h, alpha := databasesSetup(t)

	rec := doJSON(h, http.MethodPost, databasesURL(alpha), AddDatabaseRequest{Type: "postgresql", Service: "db", Name: "reports"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", rec.Code, rec.Body.String())
	}
	p := decode[Project](t, rec)
	if p.ID != alpha || !slices.Equal(p.ServiceNames, []string{"web", "db"}) {
		t.Errorf("project = %d, services %v", p.ID, p.ServiceNames)
	}
	m := manualOf(p)
	if len(m) != 1 {
		t.Fatalf("manual databases = %+v", p.Databases)
	}
	db := m[0]
	if db.ManualID <= 0 || db.Key != "manual:"+strconv.FormatInt(db.ManualID, 10) || db.Type != "postgresql" || db.Name != "reports" ||
		db.LocationKind != "container" || db.Location != "db" || !slices.Equal(db.Sources, []string{store.ManualSource}) ||
		db.UserVar != "" || db.PasswordVar != "" || db.Schedule == nil || db.Backup == nil {
		t.Errorf("database = %+v", db)
	}
	if !slices.ContainsFunc(p.Databases, func(x Database) bool { return x.Key == alphaKey && !x.Manual && x.ManualID == 0 }) {
		t.Errorf("the detected database is gone: %+v", p.Databases)
	}
	checks := checksOf(d)
	if len(checks) != 1 || checks[0].a != runner.DBPgCheck || checks[0].v["service"] != "db" || checks[0].v["dbname"] != "reports" ||
		checks[0].v["uservar"] != "" || checks[0].v["passvar"] != "" {
		t.Errorf("checks = %+v", checks)
	}
	rows := activityOf(t, d, store.ActDatabaseAdd)
	if len(rows) != 1 || rows[0].Result != store.ResultOK || rows[0].Actor != "test@example.com" || rows[0].ProjectID != alpha ||
		rows[0].Target != "Alpha · reports" || rows[0].Reason != "PostgreSQL in db · standard variables" {
		t.Errorf("activity = %+v", rows)
	}

	// Custom variables: stored, passed to the check and named in the activity.
	rec = doJSON(h, http.MethodPost, databasesURL(alpha),
		AddDatabaseRequest{Type: "mysql", Service: "db", Name: "crm", UserVar: "APP_USER", PasswordVar: "APP_PASSWORD"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add custom = %d %s", rec.Code, rec.Body.String())
	}
	m = manualOf(decode[Project](t, rec))
	if len(m) != 2 || m[1].UserVar != "APP_USER" || m[1].PasswordVar != "APP_PASSWORD" || m[1].Type != "mysql" {
		t.Errorf("manual databases = %+v", m)
	}
	checks = checksOf(d)
	if len(checks) != 2 || checks[1].a != runner.DBMySQLCheck || checks[1].v["uservar"] != "APP_USER" || checks[1].v["passvar"] != "APP_PASSWORD" {
		t.Errorf("checks = %+v", checks)
	}
	rows = activityOf(t, d, store.ActDatabaseAdd)
	if len(rows) != 2 || rows[0].Target != "Alpha · crm" || rows[0].Reason != "MySQL / MariaDB in db · user APP_USER, password APP_PASSWORD" {
		t.Errorf("activity = %+v", rows)
	}

	// The list carries them too.
	ps := decode[[]Project](t, do(h, http.MethodGet, "/api/projects"))
	for _, lp := range ps {
		if lp.ID == alpha && len(manualOf(lp)) != 2 {
			t.Errorf("listed alpha = %+v", lp.Databases)
		}
	}
}

// A database the project already has by hand (same type, service and name)
// is refused 422 on name, code exists, before the check runs; one added
// while the check ran is refused the same way by the store. Nothing more is
// stored or recorded.
func TestAddDatabaseDuplicate(t *testing.T) {
	d, h, alpha := databasesSetup(t)
	req := AddDatabaseRequest{Type: "postgresql", Service: "db", Name: "reports"}
	if rec := doJSON(h, http.MethodPost, databasesURL(alpha), req); rec.Code != http.StatusCreated {
		t.Fatalf("first add = %d %s", rec.Code, rec.Body.String())
	}
	wantExists := func(rec *httptest.ResponseRecorder) {
		t.Helper()
		fe := decode[FieldError](t, rec)
		if rec.Code != http.StatusUnprocessableEntity || fe.Field != "name" || fe.Code != "exists" || fe.Error != "This database is already added." {
			t.Fatalf("duplicate = %d %+v", rec.Code, fe)
		}
	}

	again := req
	again.UserVar, again.PasswordVar = "APP_USER", "APP_PASSWORD"
	wantExists(doJSON(h, http.MethodPost, databasesURL(alpha), again))
	if n := len(checksOf(d)); n != 1 {
		t.Errorf("checks = %d, want only the first add's", n)
	}

	// Added by someone else while the check ran.
	raced := AddDatabaseRequest{Type: "mysql", Service: "db", Name: "crm"}
	dockerOf(d).set(func(f *fakeDocker) {
		f.onCheck = func() {
			if _, err := d.Store.AddManualDatabase(context.Background(), alpha, store.ManualDatabase{
				Type: raced.Type, Service: raced.Service, Name: raced.Name, CreatedBy: "other@example.com",
			}); err != nil {
				t.Errorf("add meanwhile: %v", err)
			}
		}
	})
	wantExists(doJSON(h, http.MethodPost, databasesURL(alpha), raced))
	if n := storedManual(t, d, alpha); n != 2 {
		t.Errorf("stored = %d, want 2", n)
	}
	if rows := activityOf(t, d, store.ActDatabaseAdd); len(rows) != 1 {
		t.Errorf("activity = %+v, want the first add only", rows)
	}
}

func TestAddDatabaseValidation(t *testing.T) {
	d, h, alpha := databasesSetup(t)
	ok := AddDatabaseRequest{Type: "postgresql", Service: "db", Name: "reports"}
	with := func(fn func(r *AddDatabaseRequest)) AddDatabaseRequest {
		r := ok
		fn(&r)
		return r
	}
	cases := []struct {
		name  string
		req   AddDatabaseRequest
		field string
		words string
	}{
		{"unknown type", with(func(r *AddDatabaseRequest) { r.Type = "sqlite" }), "type", ""},
		{"empty type", with(func(r *AddDatabaseRequest) { r.Type = "" }), "type", ""},
		{"service not in the project", with(func(r *AddDatabaseRequest) { r.Service = "worker" }), "service", ""},
		{"empty service", with(func(r *AddDatabaseRequest) { r.Service = "" }), "service", ""},
		{"bad name", with(func(r *AddDatabaseRequest) { r.Name = "bad name" }), "name", ""},
		{"flag name", with(func(r *AddDatabaseRequest) { r.Name = "-x" }), "name", ""},
		{"empty name", with(func(r *AddDatabaseRequest) { r.Name = "" }), "name", ""},
		{"user without password", with(func(r *AddDatabaseRequest) { r.UserVar = "APP_USER" }), "passwordVar", ""},
		{"password without user", with(func(r *AddDatabaseRequest) { r.PasswordVar = "APP_PASSWORD" }), "userVar", ""},
		{"bad user variable", with(func(r *AddDatabaseRequest) { r.UserVar, r.PasswordVar = "A-B", "P" }), "userVar", ""},
		{"shell user variable", with(func(r *AddDatabaseRequest) { r.UserVar, r.PasswordVar = "X;rm", "P" }), "userVar", ""},
		{"digit user variable", with(func(r *AddDatabaseRequest) { r.UserVar, r.PasswordVar = "1X", "P" }), "userVar", ""},
		{"bad password variable", with(func(r *AddDatabaseRequest) { r.UserVar, r.PasswordVar = "U", "$(id)" }), "passwordVar", ""},
		{"spaced password variable", with(func(r *AddDatabaseRequest) { r.UserVar, r.PasswordVar = "U", "A B" }), "passwordVar", ""},
		{"script user variable", with(func(r *AddDatabaseRequest) { r.UserVar, r.PasswordVar = "pw", "P" }), "userVar", "Use another variable name."},
		{"script password variable", with(func(r *AddDatabaseRequest) { r.UserVar, r.PasswordVar = "U", "f" }), "passwordVar", "Use another variable name."},
	}
	for _, tc := range cases {
		rec := doJSON(h, http.MethodPost, databasesURL(alpha), tc.req)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d %s", tc.name, rec.Code, rec.Body.String())
			continue
		}
		fe := decode[FieldError](t, rec)
		if fe.Field != tc.field || fe.Error == "" || (tc.words != "" && fe.Error != tc.words) {
			t.Errorf("%s: field error = %+v, want field %s", tc.name, fe, tc.field)
		}
	}
	if n := storedManual(t, d, alpha); n != 0 {
		t.Errorf("stored %d databases", n)
	}
	if c := checksOf(d); len(c) != 0 {
		t.Errorf("checks ran: %+v", c)
	}
	if rows := activityOf(t, d, store.ActDatabaseAdd); len(rows) != 0 {
		t.Errorf("activity = %+v", rows)
	}

	// An unknown project is a 404.
	if rec := doJSON(h, http.MethodPost, databasesURL(999), ok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d", rec.Code)
	}
}

func TestAddDatabaseCheckFails(t *testing.T) {
	d, h, alpha := databasesSetup(t)
	missing := "Database reports was not found, or the user has no access to it."
	dockerOf(d).set(func(f *fakeDocker) { f.checkErr = &runner.ExitError{Code: 1, Stderr: missing} })

	rec := doJSON(h, http.MethodPost, databasesURL(alpha), AddDatabaseRequest{Type: "postgresql", Service: "db", Name: "reports"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("add = %d %s", rec.Code, rec.Body.String())
	}
	if fe := decode[FieldError](t, rec); fe != (FieldError{Error: missing, Field: "check", Code: "check_failed"}) {
		t.Errorf("field error = %+v", fe)
	}

	// A failed login names the custom password variable.
	dockerOf(d).set(func(f *fakeDocker) {
		f.checkErr = &runner.ExitError{Code: 1, Stderr: "ERROR 1045 (28000): Access denied for user 'app'@'localhost' (using password: YES)"}
	})
	rec = doJSON(h, http.MethodPost, databasesURL(alpha),
		AddDatabaseRequest{Type: "mysql", Service: "db", Name: "crm", UserVar: "APP_USER", PasswordVar: "APP_PASSWORD"})
	fe := decode[FieldError](t, rec)
	if rec.Code != http.StatusUnprocessableEntity || fe.Field != "check" || !strings.Contains(fe.Error, "Check APP_PASSWORD in the container's environment.") {
		t.Errorf("custom = %d %+v", rec.Code, fe)
	}

	if n := storedManual(t, d, alpha); n != 0 {
		t.Errorf("stored %d databases", n)
	}
	if rows := activityOf(t, d, store.ActDatabaseAdd); len(rows) != 0 {
		t.Errorf("activity = %+v", rows)
	}
}

func TestAddDatabaseRefused(t *testing.T) {
	req := AddDatabaseRequest{Type: "postgresql", Service: "db", Name: "reports"}
	cases := []struct {
		name   string
		setup  func(t *testing.T, d Deps)
		status int
		want   BackupRefused
	}{
		{"not running", func(_ *testing.T, d Deps) {
			dockerOf(d).set(func(f *fakeDocker) {
				f.containers = containerLine("alpha", "web", "running", "Up 1 hour") + containerLine("alpha", "db", "exited", "Exited (0)")
			})
		}, http.StatusConflict, BackupRefused{Error: "db_not_running", Message: "Start db first", Service: "db"}},
		{"docker down", func(_ *testing.T, d Deps) {
			dockerOf(d).set(func(f *fakeDocker) { f.containersErr = errors.New("docker is down") })
		}, http.StatusServiceUnavailable, BackupRefused{Error: "docker_down", Message: "Docker is not responding.", Service: "db"}},
		{"folder missing", func(t *testing.T, d Deps) { markMissing(t, d) },
			http.StatusConflict, BackupRefused{Error: "not_found", Message: "This project is no longer tracked, or its folder is missing."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, h, alpha := databasesSetup(t)
			tc.setup(t, d)
			rec := doJSON(h, http.MethodPost, databasesURL(alpha), req)
			if rec.Code != tc.status {
				t.Fatalf("add = %d %s", rec.Code, rec.Body.String())
			}
			if got := decode[BackupRefused](t, rec); got != tc.want {
				t.Errorf("body = %+v, want %+v", got, tc.want)
			}
			if n := storedManual(t, d, alpha); n != 0 {
				t.Errorf("stored %d databases", n)
			}
			if c := checksOf(d); len(c) != 0 {
				t.Errorf("checks ran: %+v", c)
			}
			if rows := activityOf(t, d, store.ActDatabaseAdd); len(rows) != 0 {
				t.Errorf("activity = %+v", rows)
			}
		})
	}
}

func TestRemoveDatabase(t *testing.T) {
	d, h, alpha := databasesSetup(t)
	rec := doJSON(h, http.MethodPost, databasesURL(alpha), AddDatabaseRequest{Type: "postgresql", Service: "db", Name: "reports"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", rec.Code, rec.Body.String())
	}
	db := manualOf(decode[Project](t, rec))[0]

	// A detected database has no hand-added id; an unknown id and another
	// project's are 404 too.
	_, beta := trackedIDs(t, d)
	for _, path := range []string{removeDatabaseURL(alpha, 0), removeDatabaseURL(alpha, db.ManualID+1), removeDatabaseURL(beta, db.ManualID),
		removeDatabaseURL(999, db.ManualID), databasesURL(alpha) + "/x/remove"} {
		if rec := doJSON(h, http.MethodPost, path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s", path, rec.Code, rec.Body.String())
		}
	}

	// Busy while a backup of it runs.
	connectDrive(t, h, true)
	dockerOf(d).holdDumps(t)
	bid := startBackup(t, h, alpha, db.Key)
	rec = doJSON(h, http.MethodPost, removeDatabaseURL(alpha, db.ManualID), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("busy remove = %d %s", rec.Code, rec.Body.String())
	}
	if got := decode[BackupRefused](t, rec); got.Error != "busy" || got.Message == "" {
		t.Errorf("busy body = %+v", got)
	}
	if storedManual(t, d, alpha) != 1 {
		t.Error("a busy database was removed")
	}
	dockerOf(d).releaseDumps()
	if b := waitBackup(t, h, bid); b.State != backup.StateOK {
		t.Fatalf("backup = %+v", b)
	}

	rec = doJSON(h, http.MethodPost, removeDatabaseURL(alpha, db.ManualID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body.String())
	}
	p := decode[Project](t, rec)
	if len(manualOf(p)) != 0 || !slices.ContainsFunc(p.Databases, func(x Database) bool { return x.Key == alphaKey }) {
		t.Errorf("databases = %+v", p.Databases)
	}
	if storedManual(t, d, alpha) != 0 {
		t.Error("the database is still stored")
	}
	// Its backups stay.
	if b, err := d.Store.GetBackup(context.Background(), bid); err != nil || b.DBKey != db.Key {
		t.Errorf("backup after remove = %+v, %v", b, err)
	}
	rows := activityOf(t, d, store.ActDatabaseRemove)
	if len(rows) != 1 || rows[0].Result != store.ResultOK || rows[0].Actor != "test@example.com" || rows[0].ProjectID != alpha ||
		rows[0].Target != "Alpha · reports" || rows[0].Reason != "PostgreSQL in db · standard variables" {
		t.Errorf("activity = %+v", rows)
	}
	if rec := doJSON(h, http.MethodPost, removeDatabaseURL(alpha, db.ManualID), ""); rec.Code != http.StatusNotFound {
		t.Errorf("second remove = %d", rec.Code)
	}
}

func TestDatabasesNeedJSON(t *testing.T) {
	d, h, alpha := databasesSetup(t)
	for _, path := range []string{databasesURL(alpha), removeDatabaseURL(alpha, 1)} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"type":"postgresql","service":"db","name":"reports"}`))
		req.Header.Set("Content-Type", "text/plain")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s = %d, want 415", path, rec.Code)
		}
	}
	if storedManual(t, d, alpha) != 0 || len(checksOf(d)) != 0 {
		t.Error("a request without JSON ran")
	}
}
