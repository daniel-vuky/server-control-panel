package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// handAdded is a hand-added database as the API builds it before the check:
// on service, with custom variables when user and pass are set.
func handAdded(typ, name, service, user, pass string) project.Database {
	return project.Database{
		Type: typ, Name: name, NameExplicit: true, LocationKind: "container", Location: service,
		Sources: []string{store.ManualSource}, Manual: true, UserVar: user, PasswordVar: pass,
	}
}

// addManual stores a hand-added database of shop and returns shop re-read
// with it, and the stored row.
func (e *env) addManual(t *testing.T, m store.ManualDatabase) (store.Tracked, project.Database) {
	t.Helper()
	ctx := context.Background()
	id, err := e.st.AddManualDatabase(ctx, e.shop.ID, m)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := e.st.TrackedProject(ctx, e.shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range tr.Databases {
		if d.Manual && d.ManualID == id {
			return tr, d
		}
	}
	t.Fatalf("manual database %d not in %+v", id, tr.Databases)
	return store.Tracked{}, project.Database{}
}

// The check refuses before it runs anything: a missing project, a service
// that is gone, Docker down and a service that does not run, each with the
// service named once it is resolved.
func TestCheckDatabaseRefusals(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	ctx := context.Background()

	missing := e.shop
	now := time.Now()
	missing.MissingSince = &now
	r := refusalOf(t, e.m.CheckDatabase(ctx, missing, handAdded("postgresql", "shop", "db", "", "")))
	if r.Code != CodeNotFound || r.Words != wordsNotFound {
		t.Errorf("missing project: %+v", r)
	}

	r = refusalOf(t, e.m.CheckDatabase(ctx, e.shop, handAdded("postgresql", "shop", "gone", "", "")))
	if r.Code != CodeNotSupported || r.Words != "The service gone is not in the project any more." {
		t.Errorf("service gone: %+v", r)
	}

	r = refusalOf(t, e.m.CheckDatabase(ctx, e.shop, handAdded("postgresql", "st", "stopped", "", "")))
	if r.Code != CodeDBNotRunning || r.Words != "Start stopped first" || r.Service != "stopped" {
		t.Errorf("not running: %+v", r)
	}

	e.hub.mu.Lock()
	e.hub.up = false
	e.hub.mu.Unlock()
	r = refusalOf(t, e.m.CheckDatabase(ctx, e.shop, handAdded("mysql", "crm", "crm-mysql", "", "")))
	if r.Code != CodeDockerDown || r.Words != wordsDockerDown || r.Service != "mysql" {
		t.Errorf("docker down: %+v", r)
	}

	if calls := e.r.callsOf(func(runner.Action) bool { return true }); len(calls) != 0 {
		t.Errorf("a refused check ran %+v", calls)
	}
}

// The check runs the type's action in the resolved service with the name and
// the variable names, masked by the project's secrets; it passes on exit 0.
func TestCheckDatabaseRunsTheTypesAction(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	ctx := context.Background()
	cases := []struct {
		db   project.Database
		want runner.Action
	}{
		{handAdded("postgresql", "shop", "db", "", ""), runner.DBPgCheck},
		{handAdded("mysql", "crm", "crm-mysql", "APP_USER", "APP_PASSWORD"), runner.DBMySQLCheck},
		{handAdded("mongodb", "notes", "mongo", "", ""), runner.DBMongoCheck},
	}
	for _, tc := range cases {
		if err := e.m.CheckDatabase(ctx, e.shop, tc.db); err != nil {
			t.Fatalf("%s: %v", tc.want, err)
		}
	}
	calls := e.r.callsOf(isCheckAction)
	if len(calls) != len(cases) {
		t.Fatalf("checks = %+v", calls)
	}
	for i, tc := range cases {
		c := calls[i]
		service, _ := project.ResolveService(tc.db.Location, nil, "shop", e.shop.ServiceDetails)
		if c.a != tc.want || c.v["service"] != service || c.v["dbname"] != tc.db.Name ||
			c.v["uservar"] != tc.db.UserVar || c.v["passvar"] != tc.db.PasswordVar || c.v["dir"] != e.shop.Path {
			t.Errorf("%s: call = %+v", tc.want, c)
		}
		if !strings.Contains(strings.Join(c.secrets, " "), devPass) {
			t.Errorf("%s: secrets %v miss the .env value", tc.want, c.secrets)
		}
	}

	// The values fill the real runner's template: argv refuses a missing slot.
	r := &runner.Runner{Bin: "/bin/true", DataDir: t.TempDir()}
	for _, c := range calls {
		if _, err := r.Run(ctx, runner.StackCompose, c.a, c.v, nil); err != nil {
			t.Errorf("%s: %v", c.a, err)
		}
	}
}

// A failed check is a *CheckError: the last stderr line, masked, then the
// hint, naming the custom password variable when one was given.
func TestCheckDatabaseFailureWords(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	ctx := context.Background()
	stderr := "mysql: [Warning] something\nERROR 1045 (28000): Access denied for user 'app'@'172.18.0.1' (using password: YES) " + devPass
	e.r.check = func(call) (runner.Result, error) {
		return runner.Result{Stderr: []byte(stderr), ExitCode: 1}, &runner.ExitError{Code: 1, Stderr: stderr}
	}

	err := e.m.CheckDatabase(ctx, e.shop, handAdded("mysql", "crm", "crm-mysql", "APP_USER", "APP_PASSWORD"))
	var ce *CheckError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a *CheckError", err)
	}
	if !strings.Contains(ce.Words, "Access denied for user 'app'") || !strings.Contains(ce.Words, "Check APP_PASSWORD in the container's environment.") {
		t.Errorf("words = %q", ce.Words)
	}
	assertNoSecret(t, "check words", ce.Words, ce.Error())

	// The standard variables keep the image's words.
	err = e.m.CheckDatabase(ctx, e.shop, handAdded("mysql", "crm", "crm-mysql", "", ""))
	if !errors.As(err, &ce) || !strings.Contains(ce.Words, "Check MYSQL_ROOT_PASSWORD or MYSQL_USER / MYSQL_PASSWORD in the mysql service's environment.") {
		t.Errorf("standard words = %v", err)
	}

	// The script's own words stand alone.
	missing := "Database nope was not found, or the user has no access to it."
	e.r.check = func(call) (runner.Result, error) {
		return runner.Result{Stderr: []byte(missing + "\n"), ExitCode: 1}, &runner.ExitError{Code: 1, Stderr: missing}
	}
	err = e.m.CheckDatabase(ctx, e.shop, handAdded("postgresql", "nope", "db", "", ""))
	if !errors.As(err, &ce) || ce.Words != missing {
		t.Errorf("missing database: %v", err)
	}

	// No stderr: the exit status.
	e.r.check = func(call) (runner.Result, error) {
		return runner.Result{ExitCode: 3}, &runner.ExitError{Code: 3}
	}
	err = e.m.CheckDatabase(ctx, e.shop, handAdded("postgresql", "shop", "db", "", ""))
	if !errors.As(err, &ce) || ce.Words != "The check exited with status 3." {
		t.Errorf("no stderr: %v", err)
	}

	// A timeout is worded too, never the command.
	e.r.check = func(call) (runner.Result, error) {
		return runner.Result{}, &runner.TimeoutError{After: 15 * time.Second}
	}
	err = e.m.CheckDatabase(ctx, e.shop, handAdded("postgresql", "shop", "db", "", ""))
	if !errors.As(err, &ce) || ce.Words != "The database did not answer within 15 seconds." {
		t.Errorf("timeout: %v", err)
	}
}

// A hand-added database's backup passes its variable names to the named
// actions; a detected one passes empty names.
func TestPlanPassesVarNames(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	ctx := context.Background()
	tr, db := e.addManual(t, store.ManualDatabase{Type: "mysql", Name: "crm", Service: "mysql", UserVar: "APP_USER", PasswordVar: "APP_PASSWORD"})

	p, r := e.m.plan(ctx, tr, db)
	if r != nil {
		t.Fatalf("refused %+v", r)
	}
	if p.dump != runner.DBMySQLDump || p.values["uservar"] != "APP_USER" || p.values["passvar"] != "APP_PASSWORD" || p.values["dbname"] != "crm" || p.service != "mysql" {
		t.Errorf("manual plan = %+v", p)
	}
	run := &runner.Runner{Bin: "/bin/true", DataDir: t.TempDir()}
	if _, err := run.Run(ctx, runner.StackCompose, p.size, p.values, nil); err != nil {
		t.Errorf("size: %v", err)
	}
	if _, err := run.RunTo(ctx, runner.StackCompose, p.dump, p.values, nil, io.Discard); err != nil {
		t.Errorf("dump: %v", err)
	}

	detected, _ := findDatabase(tr, keyMySQL)
	p, r = e.m.plan(ctx, tr, detected)
	if r != nil {
		t.Fatalf("refused %+v", r)
	}
	if v, ok := p.values["uservar"]; !ok || v != "" {
		t.Errorf("detected uservar = %q, %v", v, ok)
	}
	if v, ok := p.values["passvar"]; !ok || v != "" {
		t.Errorf("detected passvar = %q, %v", v, ok)
	}
}

// A hand-added database whose service left the project cannot be backed up,
// and says which service is gone.
func TestPlanManualServiceGone(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	tr, db := e.addManual(t, store.ManualDatabase{Type: "postgresql", Name: "old", Service: "legacy"})
	_, r := e.m.plan(context.Background(), tr, db)
	if r == nil || r.Code != CodeNotSupported || r.Words != "The service legacy is not in the project any more." {
		t.Errorf("refusal = %+v", r)
	}
}

// Busy says whether a backup of the database is queued or running.
func TestBusy(t *testing.T) {
	e := newEnv(t)
	tr, db := e.addManual(t, store.ManualDatabase{Type: "postgresql", Name: "shop", Service: "db"})
	key := project.DatabaseKey(db)
	if e.m.Busy(tr.ID, key) {
		t.Fatal("busy before any backup")
	}
	release := make(chan struct{})
	e.r.mu.Lock()
	e.r.dump = func(ctx context.Context, c call, w io.Writer) ([]byte, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		_, err := w.Write(defaultDump(c))
		return nil, err
	}
	e.r.mu.Unlock()
	id, err := e.m.Enqueue(context.Background(), tr, key, Person(actor))
	if err != nil {
		t.Fatal(err)
	}
	if !e.m.Busy(tr.ID, key) {
		t.Error("not busy while its backup runs")
	}
	if e.m.Busy(tr.ID, keyPG) || e.m.Busy(tr.ID+1, key) {
		t.Error("another database or project is busy")
	}
	close(release)
	if b := e.waitDone(t, id); b.State != StateOK {
		t.Fatalf("backup = %+v", b)
	}
	if e.m.Busy(tr.ID, key) {
		t.Error("busy after the backup ended")
	}
}

// A hand-added database's failed dump names its custom password variable.
func TestDumpHintNamesCustomVariable(t *testing.T) {
	e := newEnv(t)
	tr, db := e.addManual(t, store.ManualDatabase{Type: "mysql", Name: "crm", Service: "mysql", UserVar: "APP_USER", PasswordVar: "APP_PASSWORD"})
	stderr := "mysqldump: Got error: 1045: Access denied for user 'app'@'localhost' (using password: YES) when trying to connect"
	e.r.mu.Lock()
	e.r.dump = func(context.Context, call, io.Writer) ([]byte, error) {
		return []byte(stderr), &runner.ExitError{Code: 2, Stderr: stderr}
	}
	e.r.mu.Unlock()
	id, err := e.m.Enqueue(context.Background(), tr, project.DatabaseKey(db), Person(actor))
	if err != nil {
		t.Fatal(err)
	}
	b := e.waitDone(t, id)
	if b.State != StateFailed || b.Hint != "Check APP_PASSWORD in the container's environment." {
		t.Errorf("row = %+v", b)
	}
}

// A hand-added database's user and password variables are masked whatever
// their names: their .env values, the literals compose writes for them and
// their env_file values, each of 4 characters or more, reach every command's
// secrets even though DB_CRED and DB_LOGIN do not look like secrets. A
// detected database's commands do not get them.
func TestPlanMasksManualVariables(t *testing.T) {
	e := newEnv(t, envOpt{noStart: true})
	ctx := context.Background()
	env := "DB_PASSWORD=" + devPass + "\nDB_CRED=dotenvcred1\nDB_LOGIN=abc\n"
	if err := os.WriteFile(filepath.Join(e.shop.Path, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.shop.Path, "db.env"), []byte("DB_CRED=filecred22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.r.mu.Lock()
	e.r.config = `{"name":"shop","services":{"db":{"environment":{"DB_CRED":"composecred3","DB_LOGIN":"reportuser","OTHER":"plainvalue"},` +
		`"env_file":[{"path":"` + filepath.Join(e.shop.Path, "db.env") + `"}]}}}`
	e.r.mu.Unlock()

	if err := e.m.CheckDatabase(ctx, e.shop, handAdded("postgresql", "shop", "db", "DB_LOGIN", "DB_CRED")); err != nil {
		t.Fatal(err)
	}
	calls := e.r.callsOf(isCheckAction)
	if len(calls) != 1 {
		t.Fatalf("checks = %+v", calls)
	}
	for _, s := range []string{devPass, "dotenvcred1", "filecred22", "composecred3", "reportuser"} {
		if !slices.Contains(calls[0].secrets, s) {
			t.Errorf("secrets = %v, want %s", calls[0].secrets, s)
		}
	}
	for _, s := range []string{"abc", "plainvalue"} {
		if slices.Contains(calls[0].secrets, s) {
			t.Errorf("secrets = %v carry %s", calls[0].secrets, s)
		}
	}

	detected, ok := findDatabase(e.shop, keyPG)
	if !ok {
		t.Fatal("no detected PostgreSQL database")
	}
	p, r := e.m.plan(ctx, e.shop, detected)
	if r != nil {
		t.Fatalf("refused %+v", r)
	}
	for _, s := range []string{"dotenvcred1", "composecred3", "reportuser"} {
		if slices.Contains(p.secrets, s) {
			t.Errorf("detected secrets = %v carry %s", p.secrets, s)
		}
	}
}
