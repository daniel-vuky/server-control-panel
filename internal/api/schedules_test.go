package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// testNow is the fake clock of the schedule tests: Tuesday 2030-01-15 10:00
// UTC (17:00 in Asia/Ho_Chi_Minh). It is later than any real updated_at, so
// a next run is computed from it.
var testNow = time.Date(2030, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// scheduleSetup is alpha and beta (addProjects) with alpha running, a router
// whose zone is zone and whose clock reads testNow. It returns alpha's id.
func scheduleSetup(t *testing.T, zone *time.Location) (Deps, http.Handler, int64) {
	t.Helper()
	d := newTestDeps(t)
	d.Zone, d.Now = zone, fixedClock(testNow)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	return d, h, alpha
}

func scheduleURL(id int64) string {
	return "/api/projects/" + strconv.FormatInt(id, 10) + "/backup-schedule"
}

// saveSchedule posts a schedule and answers the project.
func saveSchedule(t *testing.T, h http.Handler, projectID int64, in BackupScheduleInput) Project {
	t.Helper()
	rec := doJSON(h, http.MethodPost, scheduleURL(projectID), in)
	if rec.Code != http.StatusOK {
		t.Fatalf("save schedule = %d %s", rec.Code, rec.Body.String())
	}
	return decode[Project](t, rec)
}

// databaseOf is the database with key in p.
func databaseOf(t *testing.T, p Project, key string) Database {
	t.Helper()
	for _, db := range p.Databases {
		if db.Key == key {
			return db
		}
	}
	t.Fatalf("project %s has no database %s", p.DisplayName, key)
	return Database{}
}

func ptr[T any](v T) *T { return &v }

func TestBackupSettingsGetAndDefaults(t *testing.T) {
	d, h, _ := scheduleSetup(t, mustZone(t, "Asia/Ho_Chi_Minh"))

	rec := do(h, http.MethodGet, "/api/backup-settings")
	want := BackupSettings{
		Defaults: ScheduleFields{Frequency: "daily", Time: "02:00", Weekday: 0, Keep: 14, Compress: true},
		TimeZone: TimeZone{Name: "Asia/Ho_Chi_Minh", Offset: "+07:00"},
	}
	if got := decode[BackupSettings](t, rec); rec.Code != http.StatusOK || got != want {
		t.Fatalf("settings = %d %s, want %+v", rec.Code, rec.Body.String(), want)
	}

	const path = "/api/backup-settings/defaults"
	for _, tc := range []struct {
		name, body, field string
	}{
		{"frequency", `{"frequency":"hourly","time":"02:00","weekday":0,"keep":14,"compress":true}`, "frequency"},
		{"time format", `{"frequency":"daily","time":"2:00","weekday":0,"keep":14,"compress":true}`, "time"},
		{"time range", `{"frequency":"daily","time":"24:00","weekday":0,"keep":14,"compress":true}`, "time"},
		{"weekday high", `{"frequency":"weekly","time":"02:00","weekday":7,"keep":14,"compress":true}`, "weekday"},
		{"weekday low", `{"frequency":"weekly","time":"02:00","weekday":-1,"keep":14,"compress":true}`, "weekday"},
		{"keep zero", `{"frequency":"daily","time":"02:00","weekday":0,"keep":0,"compress":true}`, "keep"},
		{"keep high", `{"frequency":"daily","time":"02:00","weekday":0,"keep":366,"compress":true}`, "keep"},
		{"keep fraction", `{"frequency":"daily","time":"02:00","weekday":0,"keep":1.5,"compress":true}`, "keep"},
	} {
		rec := doJSON(h, http.MethodPost, path, tc.body)
		if fe := decode[FieldError](t, rec); rec.Code != http.StatusUnprocessableEntity || fe.Field != tc.field || fe.Error == "" || fe.Code == "" {
			t.Errorf("%s = %d %s, want 422 on %s", tc.name, rec.Code, rec.Body.String(), tc.field)
		}
	}
	if rec := do(h, http.MethodPost, path); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("without JSON = %d, want 415", rec.Code)
	}

	// No change: 200 and no activity row.
	rec = doJSON(h, http.MethodPost, path, want.Defaults)
	if got := decode[ScheduleFields](t, rec); rec.Code != http.StatusOK || got != want.Defaults {
		t.Fatalf("same defaults = %d %s", rec.Code, rec.Body.String())
	}
	if rows := activityOf(t, d, store.ActBackupDefaults); len(rows) != 0 {
		t.Errorf("activity after no change = %+v", rows)
	}

	next := want.Defaults
	next.Keep = 7
	rec = doJSON(h, http.MethodPost, path, next)
	if got := decode[ScheduleFields](t, rec); rec.Code != http.StatusOK || got != next {
		t.Fatalf("new defaults = %d %s", rec.Code, rec.Body.String())
	}
	rows := activityOf(t, d, store.ActBackupDefaults)
	if len(rows) != 1 || rows[0].Result != store.ResultOK || rows[0].Actor != "test@example.com" ||
		rows[0].ActorKind != store.ActorPerson || rows[0].Reason != "keep: 14 → 7" {
		t.Errorf("activity = %+v", rows)
	}
	if got := decode[BackupSettings](t, do(h, http.MethodGet, "/api/backup-settings")); got.Defaults != next {
		t.Errorf("defaults after save = %+v", got.Defaults)
	}
}

func TestBackupScheduleRoute(t *testing.T) {
	d, h, alpha := scheduleSetup(t, time.UTC)
	ledger := addLedger(t, d)
	srv := httptest.NewServer(h)
	defer srv.Close()
	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	if s := nextState(t, r); len(s.Projects) == 0 {
		t.Fatalf("first state = %+v", s)
	}

	in := BackupScheduleInput{Database: alphaKey, Frequency: "weekly", Time: "03:30", Weekday: 1, Keep: 7, Compress: false}
	p := saveSchedule(t, h, alpha, in)
	// Tuesday 2030-01-15 10:00 UTC: the next Monday 03:30.
	want := DatabaseSchedule{Frequency: "weekly", Time: "03:30", Weekday: 1, Keep: 7, Compress: false, NextRunAt: ptr("2030-01-21T03:30:00Z")}
	if db := databaseOf(t, p, alphaKey); p.ID != alpha || db.Schedule == nil || !sameSchedule(*db.Schedule, want) {
		t.Fatalf("project = %+v, schedule = %+v", p, db.Schedule)
	}
	rows := activityOf(t, d, store.ActBackupSchedule)
	if len(rows) != 1 || rows[0].ProjectID != alpha || rows[0].Target != "Alpha · alpha" || rows[0].Actor != "test@example.com" {
		t.Fatalf("activity = %+v", rows)
	}
	for _, part := range []string{"frequency: daily → weekly", "time: 02:00 → 03:30", "weekday: Sunday → Monday", "keep: 14 → 7", "compress: on → off"} {
		if !strings.Contains(rows[0].Reason, part) {
			t.Errorf("reason %q, want %q", rows[0].Reason, part)
		}
	}
	// Hub.Refresh: the open tab sees it without waiting for a tick.
	start := time.Now()
	stateUntil(t, r, "the new schedule", func(s LiveState) bool {
		for _, p := range s.Projects {
			for _, db := range p.Databases {
				if db.Key == alphaKey && db.Schedule != nil && db.Schedule.Frequency == "weekly" {
					return true
				}
			}
		}
		return false
	})
	if time.Since(start) > 4*time.Second {
		t.Errorf("the change took %v to arrive", time.Since(start))
	}

	// The same again: 200, no new row.
	saveSchedule(t, h, alpha, in)
	if rows := activityOf(t, d, store.ActBackupSchedule); len(rows) != 1 {
		t.Errorf("activity after no change = %+v", rows)
	}
	// Off: no next run.
	in.Frequency = "off"
	if db := databaseOf(t, saveSchedule(t, h, alpha, in), alphaKey); db.Schedule == nil || db.Schedule.NextRunAt != nil {
		t.Errorf("off schedule = %+v", db.Schedule)
	}

	for _, tc := range []struct {
		name  string
		id    int64
		in    BackupScheduleInput
		field string
	}{
		{"unknown database", alpha, BackupScheduleInput{Database: "postgresql:db:nope", Frequency: "daily", Time: "02:00", Keep: 14}, "database"},
		{"no database", alpha, BackupScheduleInput{Frequency: "daily", Time: "02:00", Keep: 14}, "database"},
		{"not supported", ledger, BackupScheduleInput{Database: "sqlite:/data/app.db:app.db", Frequency: "daily", Time: "02:00", Keep: 14}, "database"},
		{"frequency", alpha, BackupScheduleInput{Database: alphaKey, Frequency: "monthly", Time: "02:00", Keep: 14}, "frequency"},
		{"time", alpha, BackupScheduleInput{Database: alphaKey, Frequency: "daily", Time: "02:60", Keep: 14}, "time"},
		{"weekday", alpha, BackupScheduleInput{Database: alphaKey, Frequency: "weekly", Time: "02:00", Weekday: 9, Keep: 14}, "weekday"},
		{"keep", alpha, BackupScheduleInput{Database: alphaKey, Frequency: "daily", Time: "02:00", Keep: 400}, "keep"},
	} {
		rec := doJSON(h, http.MethodPost, scheduleURL(tc.id), tc.in)
		if fe := decode[FieldError](t, rec); rec.Code != http.StatusUnprocessableEntity || fe.Field != tc.field || fe.Error == "" {
			t.Errorf("%s = %d %s, want 422 on %s", tc.name, rec.Code, rec.Body.String(), tc.field)
		}
	}
	valid := BackupScheduleInput{Database: alphaKey, Frequency: "daily", Time: "02:00", Keep: 14, Compress: true}
	for _, path := range []string{scheduleURL(999), "/api/projects/abc/backup-schedule"} {
		if rec := doJSON(h, http.MethodPost, path, valid); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s, want 404", path, rec.Code, rec.Body.String())
		}
	}
	if rec := do(h, http.MethodPost, scheduleURL(alpha)); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("without JSON = %d, want 415", rec.Code)
	}
	if rows := activityOf(t, d, store.ActBackupSchedule); len(rows) != 2 {
		t.Errorf("activity = %+v, want only the two changes", rows)
	}
}

// sameSchedule compares two schedules, next run included.
func sameSchedule(a, b DatabaseSchedule) bool {
	if (a.NextRunAt == nil) != (b.NextRunAt == nil) || (a.NextRunAt != nil && *a.NextRunAt != *b.NextRunAt) {
		return false
	}
	a.NextRunAt, b.NextRunAt = nil, nil
	return a == b
}

func TestNextRunAtUsesServerZone(t *testing.T) {
	_, h, alpha := scheduleSetup(t, mustZone(t, "Asia/Ho_Chi_Minh"))
	in := BackupScheduleInput{Database: alphaKey, Frequency: "daily", Time: "02:00", Weekday: 0, Keep: 14, Compress: true}
	in.Time = "01:30" // a change, so updated_at is the fake clock
	db := databaseOf(t, saveSchedule(t, h, alpha, in), alphaKey)
	// 10:00 UTC is 17:00 in Ho Chi Minh City: the next 01:30 there is tomorrow.
	if db.Schedule == nil || db.Schedule.NextRunAt == nil || *db.Schedule.NextRunAt != "2030-01-16T01:30:00+07:00" {
		t.Fatalf("schedule = %+v", db.Schedule)
	}
	got := decode[[]Project](t, do(h, http.MethodGet, "/api/projects"))
	if db := databaseOf(t, got[0], alphaKey); db.Schedule == nil || db.Schedule.NextRunAt == nil || *db.Schedule.NextRunAt != "2030-01-16T01:30:00+07:00" {
		t.Errorf("GET /api/projects schedule = %+v", db.Schedule)
	}
	// A scan row has no schedule.
	if rows := toDatabases([]project.Database{{Type: "postgresql", Name: "x", LocationKind: "container", Location: "db"}}); rows[0].Schedule != nil {
		t.Errorf("scan row = %+v", rows[0])
	}
}

func TestBackupsAllRoute(t *testing.T) {
	d, h, _ := backupSetup(t)
	addLedger(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() + ledgerUp() })

	if rec := do(h, http.MethodPost, "/api/backups/all"); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("without JSON = %d, want 415", rec.Code)
	}

	dockerOf(d).holdDumps(t)
	rec := doJSON(h, http.MethodPost, "/api/backups/all", "")
	got := decode[BackupAllResult](t, rec)
	// alpha's and ledger's PostgreSQL and ledger's MariaDB; the SQLite file is skipped.
	if rec.Code != http.StatusAccepted || got.ID == 0 || got.Total != 3 || got.Skipped != 1 {
		t.Fatalf("back up all = %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(h, http.MethodPost, "/api/backups/all", "")
	if refused := decode[BackupRefused](t, rec); rec.Code != http.StatusConflict || refused.Error != "batch_running" || refused.Message == "" {
		t.Errorf("second back up all = %d %s", rec.Code, rec.Body.String())
	}
	dockerOf(d).releaseDumps()
	waitFor(t, "the batch to finish", func() bool {
		b := d.Backups.CurrentBatch()
		return b != nil && b.FinishedAt != nil
	})

	// The live state carries the batch.
	srv := httptest.NewServer(h)
	defer srv.Close()
	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	s := nextState(t, r)
	if b := s.Batch; b == nil || b.ID != got.ID || b.Total != 3 || b.Done != 3 || b.Skipped != 1 ||
		b.OK+b.Failed != 3 || b.StartedBy != "test@example.com" || b.StartedAt == "" || b.FinishedAt == nil {
		t.Errorf("live batch = %+v", s.Batch)
	}

	doJSON(h, http.MethodPost, "/api/drive/disconnect", "")
	rec = doJSON(h, http.MethodPost, "/api/backups/all", "")
	if refused := decode[BackupRefused](t, rec); rec.Code != http.StatusConflict || refused.Error != "drive_not_connected" || refused.Message == "" {
		t.Errorf("without Drive = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBackupsAllNothingToBackUp(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	connectDrive(t, h, true)
	rec := doJSON(h, http.MethodPost, "/api/backups/all", "")
	if got := decode[BackupAllResult](t, rec); rec.Code != http.StatusAccepted || got.Total != 0 || got.Skipped != 0 {
		t.Errorf("empty back up all = %d %s", rec.Code, rec.Body.String())
	}
}

// insertAt stores a finished backup of alpha's database created at.
func insertAt(t *testing.T, d Deps, projectID int64, state string, at time.Time, mod func(*store.Backup)) int64 {
	t.Helper()
	b := store.Backup{
		ProjectID: projectID, ProjectName: "Alpha", DBType: "postgresql", DBName: "alpha", DBLocationKind: "container",
		DBLocation: "db", DBKey: alphaKey, State: state, StartedBy: "someone@example.com", CreatedAt: at, FinishedAt: &at,
	}
	if mod != nil {
		mod(&b)
	}
	id, err := d.Store.InsertBackup(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func ids(bs []Backup) []int64 {
	out := []int64{}
	for _, b := range bs {
		out = append(out, b.ID)
	}
	return out
}

func TestListBackupsRoute(t *testing.T) {
	d := newTestDeps(t)
	d.Zone, d.Now = mustZone(t, "Asia/Ho_Chi_Minh"), fixedClock(testNow)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, beta := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	connectDrive(t, h, true)

	// Ho Chi Minh City is UTC+7: 17:00 UTC is local midnight.
	r1 := insertAt(t, d, alpha, "ok", time.Date(2030, 1, 14, 16, 59, 0, 0, time.UTC), nil)       // 14th 23:59 local
	r2 := insertAt(t, d, alpha, "failed", time.Date(2030, 1, 14, 17, 0, 0, 0, time.UTC), nil)    // 15th 00:00 local
	r3 := insertAt(t, d, alpha, "refused", time.Date(2030, 1, 15, 16, 59, 59, 0, time.UTC), nil) // 15th 23:59:59
	r4 := insertAt(t, d, beta, "ok", time.Date(2030, 1, 15, 17, 0, 0, 0, time.UTC), nil)         // 16th 00:00 local

	list := func(q string) BackupList {
		t.Helper()
		rec := do(h, http.MethodGet, "/api/backups"+q)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", q, rec.Code, rec.Body.String())
		}
		return decode[BackupList](t, rec)
	}
	for _, tc := range []struct {
		q    string
		want []int64
		more bool
	}{
		{"", []int64{r4, r3, r2, r1}, false},
		{"?from=2030-01-15&to=2030-01-15", []int64{r3, r2}, false},
		{"?from=2030-01-15", []int64{r4, r3, r2}, false},
		{"?to=2030-01-14", []int64{r1}, false},
		{"?result=ok", []int64{r4, r1}, false},
		{"?result=failed", []int64{r3, r2}, false},
		{"?project=" + strconv.FormatInt(beta, 10), []int64{r4}, false},
		{"?limit=1", []int64{r4}, true},
		{"?limit=1&before=" + strconv.FormatInt(r4, 10), []int64{r3}, true},
		{"?limit=2&before=" + strconv.FormatInt(r2, 10), []int64{r1}, false},
	} {
		got := list(tc.q)
		if !slices.Equal(ids(got.Backups), tc.want) || got.More != tc.more {
			t.Errorf("%q = %v more %v, want %v more %v", tc.q, ids(got.Backups), got.More, tc.want, tc.more)
		}
	}

	// A running backup comes from the manager, only without a result filter.
	dockerOf(d).holdDumps(t)
	running := startBackup(t, h, alpha, alphaKey)
	waitFor(t, "the backup to run", func() bool {
		for _, b := range d.Backups.Active() {
			if b.ID == running && b.State == "running" {
				return true
			}
		}
		return false
	})
	if got := list(""); len(got.Backups) == 0 || got.Backups[0].ID != running || got.Backups[0].State != "running" || len(got.Backups[0].Steps) != 4 {
		t.Errorf("all = %+v, want the running backup first", got.Backups)
	}
	if got := list("?result=ok"); slices.Contains(ids(got.Backups), running) {
		t.Errorf("result=ok lists the running backup: %v", ids(got.Backups))
	}
	if got := list("?result=failed"); slices.Contains(ids(got.Backups), running) {
		t.Errorf("result=failed lists the running backup: %v", ids(got.Backups))
	}
	dockerOf(d).releaseDumps()
	waitBackup(t, h, running)

	for _, tc := range []struct{ q, field string }{
		{"?result=x", "result"},
		{"?from=2030-13-01", "from"},
		{"?from=x", "from"},
		{"?to=2030-1-5", "to"},
		{"?from=2030-01-16&to=2030-01-15", "to"},
		{"?project=x", "project"},
		{"?project=0", "project"},
		{"?before=x", "before"},
		{"?before=-1", "before"},
		{"?limit=0", "limit"},
		{"?limit=101", "limit"},
	} {
		rec := do(h, http.MethodGet, "/api/backups"+tc.q)
		if qe := decode[QueryError](t, rec); rec.Code != http.StatusBadRequest || qe.Field != tc.field || qe.Error == "" {
			t.Errorf("%s = %d %s, want 400 on %s", tc.q, rec.Code, rec.Body.String(), tc.field)
		}
	}
	if rec := do(h, http.MethodGet, "/api/backups?project=12345"); strings.TrimSpace(rec.Body.String()) != `{"backups":[],"more":false}` {
		t.Errorf("empty = %s", rec.Body.String())
	}
}

func TestBackupSummaryRoute(t *testing.T) {
	d, h, alpha := scheduleSetup(t, mustZone(t, "Asia/Ho_Chi_Minh"))
	ledger := addLedger(t, d)
	const booksKey, mariaKey = "postgresql:db:books", "mysql:maria:maria"

	onDrive := func(id string, size int64) func(*store.Backup) {
		return func(b *store.Backup) { b.DriveFileID, b.SizeBytes, b.FileName = id, size, id+".sql.gz" }
	}
	// Today is 2030-01-15 in Ho Chi Minh City (17:00 local).
	insertAt(t, d, alpha, "ok", time.Date(2030, 1, 15, 1, 0, 0, 0, time.UTC), onDrive("f1", 100))
	insertAt(t, d, alpha, "ok", time.Date(2030, 1, 8, 16, 59, 0, 0, time.UTC), onDrive("f2", 50)) // the 8th local: outside the 7 days
	pruned := insertAt(t, d, alpha, "ok", time.Date(2030, 1, 10, 1, 0, 0, 0, time.UTC), onDrive("f3", 1000))
	if err := d.Store.MarkPruned(context.Background(), pruned, testNow); err != nil {
		t.Fatal(err)
	}
	failedAt := time.Date(2030, 1, 14, 16, 30, 0, 0, time.UTC) // the 14th 23:30 local
	insertAt(t, d, alpha, "failed", failedAt, nil)
	insertAt(t, d, alpha, "running", time.Date(2030, 1, 15, 2, 0, 0, 0, time.UTC), nil) // counts in neither

	// Alpha and ledger's books at 02:00, maria off; ledger's SQLite keeps the
	// daily default but can never run, so it is not a next run.
	saveSchedule(t, h, alpha, BackupScheduleInput{Database: alphaKey, Frequency: "daily", Time: "02:00", Keep: 14, Compress: false})
	saveSchedule(t, h, ledger, BackupScheduleInput{Database: booksKey, Frequency: "daily", Time: "02:00", Keep: 14, Compress: false})
	saveSchedule(t, h, ledger, BackupScheduleInput{Database: mariaKey, Frequency: "off", Time: "02:00", Keep: 14, Compress: true})

	rec := do(h, http.MethodGet, "/api/backups/summary")
	if rec.Code != http.StatusOK {
		t.Fatalf("summary = %d %s", rec.Code, rec.Body.String())
	}
	got := decode[BackupSummary](t, rec)
	if got.DriveBytes != 150 || got.DriveFiles != 2 || got.DriveProjects != 1 {
		t.Errorf("drive = %d bytes, %d files, %d projects", got.DriveBytes, got.DriveFiles, got.DriveProjects)
	}
	wantDays := []SummaryDay{
		{Date: "2030-01-09"}, {Date: "2030-01-10", OK: 1}, {Date: "2030-01-11"}, {Date: "2030-01-12"},
		{Date: "2030-01-13"}, {Date: "2030-01-14", Failed: 1}, {Date: "2030-01-15", OK: 1},
	}
	if !slices.Equal(got.Days, wantDays) || got.OK7d != 2 || got.Failed7d != 1 {
		t.Errorf("days = %+v ok %d failed %d, want %+v", got.Days, got.OK7d, got.Failed7d, wantDays)
	}
	wantFail := SummaryFailure{ProjectID: alpha, ProjectName: "Alpha", DBName: "alpha", At: "2030-01-14T16:30:00Z"}
	if len(got.RecentFailures) != 1 || got.RecentFailures[0] != wantFail {
		t.Errorf("recent failures = %+v", got.RecentFailures)
	}
	if got.NextRun == nil || got.NextRun.At != "2030-01-16T02:00:00+07:00" || !slices.Equal(got.NextRun.Projects, []string{"Alpha", "Ledger"}) {
		t.Errorf("next run = %+v", got.NextRun)
	}
	// The project tab agrees: the SQLite file keeps its daily row but has no
	// next run, since the scheduler never runs it.
	for _, p := range decode[[]Project](t, do(h, http.MethodGet, "/api/projects")) {
		if p.ID != ledger {
			continue
		}
		if db := databaseOf(t, p, "sqlite:/data/app.db:app.db"); db.Schedule == nil || db.Schedule.Frequency != "daily" || db.Schedule.NextRunAt != nil {
			t.Errorf("sqlite schedule = %+v, want daily with no next run", db.Schedule)
		}
		if db := databaseOf(t, p, booksKey); db.Schedule == nil || db.Schedule.NextRunAt == nil || *db.Schedule.NextRunAt != got.NextRun.At {
			t.Errorf("books schedule = %+v, want the summary's next run", db.Schedule)
		}
	}

	// An earlier time on one project wins alone.
	saveSchedule(t, h, ledger, BackupScheduleInput{Database: booksKey, Frequency: "daily", Time: "01:00", Keep: 14, Compress: false})
	got = decode[BackupSummary](t, do(h, http.MethodGet, "/api/backups/summary"))
	if got.NextRun == nil || got.NextRun.At != "2030-01-16T01:00:00+07:00" || !slices.Equal(got.NextRun.Projects, []string{"Ledger"}) {
		t.Errorf("next run = %+v", got.NextRun)
	}

	// Every schedule off: no next run, and the arrays are never null.
	saveSchedule(t, h, alpha, BackupScheduleInput{Database: alphaKey, Frequency: "off", Time: "02:00", Keep: 14})
	saveSchedule(t, h, ledger, BackupScheduleInput{Database: booksKey, Frequency: "off", Time: "02:00", Keep: 14})
	rec = do(h, http.MethodGet, "/api/backups/summary")
	if raw := decodeRaw(t, rec.Body.Bytes()); raw["nextRun"] != nil {
		t.Errorf("nextRun = %v, want null", raw["nextRun"])
	}
}

func TestBackupSummaryEmptyArrays(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	rec := do(h, http.MethodGet, "/api/backups/summary")
	raw := decodeRaw(t, rec.Body.Bytes())
	if rec.Code != http.StatusOK || raw["recentFailures"] == nil || raw["days"] == nil || raw["nextRun"] != nil {
		t.Errorf("empty summary = %d %s", rec.Code, rec.Body.String())
	}
}

func TestLiteralRoutesWin(t *testing.T) {
	_, h, alpha := scheduleSetup(t, time.UTC)

	// Without Drive, Back up all is refused by its own handler (not a 404 or 405).
	rec := doJSON(h, http.MethodPost, "/api/backups/all", "")
	if refused := decode[BackupRefused](t, rec); rec.Code != http.StatusConflict || refused.Error != "drive_not_connected" {
		t.Errorf("POST /api/backups/all = %d %s", rec.Code, rec.Body.String())
	}
	rec = do(h, http.MethodGet, "/api/backups/summary")
	if raw := decodeRaw(t, rec.Body.Bytes()); rec.Code != http.StatusOK || raw["days"] == nil {
		t.Errorf("GET /api/backups/summary = %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(h, http.MethodPost, scheduleURL(alpha),
		BackupScheduleInput{Database: alphaKey, Frequency: "daily", Time: "02:00", Keep: 14, Compress: true})
	if p := decode[Project](t, rec); rec.Code != http.StatusOK || p.ID != alpha {
		t.Errorf("POST backup-schedule = %d %s", rec.Code, rec.Body.String())
	}
	// GET /api/backups/{id} still answers ids.
	if rec := do(h, http.MethodGet, "/api/backups/999"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/backups/999 = %d", rec.Code)
	}
}
