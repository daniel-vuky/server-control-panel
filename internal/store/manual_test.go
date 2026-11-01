package store

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

func addManual(t *testing.T, s *Store, projectID int64, m ManualDatabase) int64 {
	t.Helper()
	id, err := s.AddManualDatabase(context.Background(), projectID, m)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

var appManual = ManualDatabase{Type: "mysql", Name: "app", Service: "db", UserVar: "APP_USER", PasswordVar: "APP_PASSWORD", CreatedBy: "dev@localhost"}

func TestAddManualDatabase(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	if _, _, err := s.SetBackupDefaults(ctx, ScheduleFields{Frequency: FrequencyWeekly, Time: "03:30", Weekday: 2, Keep: 7, Compress: false}, schedT1); err != nil {
		t.Fatal(err)
	}
	id := addManual(t, s, shopID, appManual)

	tp, err := s.TrackedProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.Databases) != 2 {
		t.Fatalf("databases = %+v, want the detected one then the manual one", tp.Databases)
	}
	if tp.Databases[0].Manual {
		t.Fatalf("first database should be the detected one: %+v", tp.Databases[0])
	}
	want := project.Database{
		Type: "mysql", Name: "app", NameExplicit: true, LocationKind: "container", Location: "db",
		Sources: []string{"added by hand"}, Manual: true, ManualID: id, UserVar: "APP_USER", PasswordVar: "APP_PASSWORD",
	}
	got := tp.Databases[1]
	if got.Type != want.Type || got.Name != want.Name || got.NameExplicit != want.NameExplicit || got.LocationKind != want.LocationKind ||
		got.Location != want.Location || !slices.Equal(got.Sources, want.Sources) || !got.Manual || got.ManualID != id ||
		got.UserVar != want.UserVar || got.PasswordVar != want.PasswordVar {
		t.Fatalf("manual database = %+v, want %+v", got, want)
	}
	key := project.DatabaseKey(got)
	if key != "manual:"+itoa(id) {
		t.Fatalf("key = %q", key)
	}
	sc, ok := tp.Schedules[key]
	if !ok {
		t.Fatalf("no schedule for %q in %+v", key, tp.Schedules)
	}
	if sc.Frequency != FrequencyWeekly || sc.Time != "03:30" || sc.Weekday != 2 || sc.Keep != 7 || sc.Compress {
		t.Fatalf("schedule = %+v, want the current defaults", sc)
	}
	// Standard variables: both names empty.
	id2 := addManual(t, s, shopID, ManualDatabase{Type: "postgresql", Name: "x", Service: "db", CreatedBy: "dev@localhost"})
	tp, _ = s.TrackedProject(ctx, shopID)
	if d := tp.Databases[2]; d.ManualID != id2 || d.UserVar != "" || d.PasswordVar != "" {
		t.Fatalf("standard-variables row = %+v", d)
	}
}

func TestAddManualDatabaseUnknownProject(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, _, blogID := seedApps(t, s)
	for _, id := range []int64{0, 999, blogID} { // unknown, and ignored
		if _, err := s.AddManualDatabase(ctx, id, appManual); !errors.Is(err, ErrNotFound) {
			t.Fatalf("project %d: err = %v, want ErrNotFound", id, err)
		}
	}
	if n := count(t, s.db, `SELECT count(*) FROM manual_databases`); n != 0 {
		t.Fatalf("%d rows stored", n)
	}
}

// The same database (type, service and name) cannot be added twice to a
// project: ErrDatabaseExists, nothing stored. Another type, service, name or
// project is a different database, and a removed row can be added again.
func TestAddManualDatabaseDuplicate(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	id := addManual(t, s, shopID, appManual)

	again := appManual
	again.UserVar, again.PasswordVar, again.CreatedBy = "", "", "other@localhost"
	if _, err := s.AddManualDatabase(ctx, shopID, again); !errors.Is(err, ErrDatabaseExists) {
		t.Fatalf("duplicate: err = %v, want ErrDatabaseExists", err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM manual_databases`); n != 1 {
		t.Fatalf("%d rows stored, want 1", n)
	}
	if n := count(t, s.db, `SELECT count(*) FROM backup_schedules WHERE db_key LIKE 'manual:%'`); n != 1 {
		t.Fatalf("%d manual schedules, want 1", n)
	}

	other := appManual
	other.Type = "postgresql"
	addManual(t, s, shopID, other)
	other = appManual
	other.Name = "app2"
	addManual(t, s, shopID, other)

	if err := s.RemoveManualDatabase(ctx, shopID, id); err != nil {
		t.Fatal(err)
	}
	addManual(t, s, shopID, appManual)
}

func TestManualSurvivesRescan(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	id := addManual(t, s, shopID, appManual)
	key := "manual:" + itoa(id)
	before := mustSchedule(t, s, shopID, key)

	for _, dbs := range [][]project.Database{
		sampleProjects()[0].Databases,
		nil, // the database is gone from compose
	} {
		r := RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: rescanTime, Refresh: []ProjectRefresh{{
			Path:   "/srv/apps/shop",
			Config: &ConfigRefresh{ComposeFiles: []string{"compose.yaml"}, ComposeProject: "shop", Databases: dbs},
		}}}}}
		if err := s.ApplyRescan(ctx, r, laterTime); err != nil {
			t.Fatal(err)
		}
		tp, err := s.TrackedProject(ctx, shopID)
		if err != nil {
			t.Fatal(err)
		}
		last := tp.Databases[len(tp.Databases)-1]
		if !last.Manual || last.ManualID != id || last.UserVar != "APP_USER" {
			t.Fatalf("after rescan databases = %+v", tp.Databases)
		}
		if sc := mustSchedule(t, s, shopID, key); sc != before {
			t.Fatalf("schedule changed by rescan: %+v, was %+v", sc, before)
		}
	}
}

func TestEnabledSchedulesIncludesManual(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	id := addManual(t, s, shopID, appManual) // the defaults are daily
	key := "manual:" + itoa(id)

	keys := func() []string {
		t.Helper()
		got, err := s.EnabledSchedules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, sc := range got {
			out = append(out, sc.DBKey)
		}
		return out
	}
	if got := keys(); !slices.Contains(got, key) || !slices.Contains(got, shopKey) {
		t.Fatalf("enabled = %v, want %s and %s", got, key, shopKey)
	}
	off := ScheduleFields{Frequency: FrequencyOff, Time: "02:00", Keep: 14, Compress: true}
	if _, _, err := s.SetSchedule(ctx, shopID, key, off, schedT1); err != nil {
		t.Fatal(err)
	}
	if got := keys(); slices.Contains(got, key) {
		t.Fatalf("an off manual schedule is listed: %v", got)
	}
	on := off
	on.Frequency = FrequencyDaily
	if _, _, err := s.SetSchedule(ctx, shopID, key, on, schedT2); err != nil {
		t.Fatal(err)
	}
	if got := keys(); !slices.Contains(got, key) {
		t.Fatalf("enabled = %v, want %s", got, key)
	}
	// An orphan manual key (the row was removed behind the schedule) is not listed.
	mustExec(t, s.db, `DELETE FROM manual_databases WHERE id = ?`, id)
	if got := keys(); slices.Contains(got, key) {
		t.Fatalf("a removed manual database's schedule is listed: %v", got)
	}
}

func TestRemoveManualDatabase(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	id := addManual(t, s, shopID, appManual)
	other := addManual(t, s, shopID, ManualDatabase{Type: "mongodb", Name: "m", Service: "mongo", CreatedBy: "dev@localhost"})
	key := "manual:" + itoa(id)

	if err := s.RemoveManualDatabase(ctx, shopID+1, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another project's id: err = %v, want ErrNotFound", err)
	}
	if err := s.RemoveManualDatabase(ctx, shopID, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := readSchedule(t, s, shopID, key); ok {
		t.Fatal("schedule row left")
	}
	if _, ok := readSchedule(t, s, shopID, "manual:"+itoa(other)); !ok {
		t.Fatal("the other row's schedule was removed")
	}
	tp, _ := s.TrackedProject(ctx, shopID)
	if len(tp.Databases) != 2 || tp.Databases[1].ManualID != other {
		t.Fatalf("databases = %+v", tp.Databases)
	}
	if err := s.RemoveManualDatabase(ctx, shopID, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: err = %v, want ErrNotFound", err)
	}
	if err := s.RemoveManualDatabase(ctx, shopID, 4242); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrNotFound", err)
	}
	// Ids are not reused.
	again := addManual(t, s, shopID, appManual)
	if again == id || again == other {
		t.Fatalf("id %d reused", again)
	}
}

func TestUntrackDeletesManual(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	id := addManual(t, s, shopID, appManual)
	if _, err := s.UntrackProject(ctx, shopID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM manual_databases WHERE project_id = ?`, shopID); n != 0 {
		t.Fatalf("%d manual rows left after untrack", n)
	}
	if _, ok := readSchedule(t, s, shopID, "manual:"+itoa(id)); ok {
		t.Fatal("manual schedule left after untrack")
	}
}

func TestManualCascade(t *testing.T) {
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	addManual(t, s, shopID, appManual)
	mustExec(t, s.db, `DELETE FROM projects WHERE id = ?`, shopID)
	if n := count(t, s.db, `SELECT count(*) FROM manual_databases`); n != 0 {
		t.Fatalf("%d manual rows left after the project was deleted", n)
	}
	if n := count(t, s.db, `SELECT count(*) FROM backup_schedules`); n != 0 {
		t.Fatalf("%d schedules left after the project was deleted", n)
	}
}

func TestManualKeyNeverCollides(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s) // detected postgresql:db:shop
	id := addManual(t, s, shopID, ManualDatabase{Type: "postgresql", Name: "shop", Service: "db", CreatedBy: "dev@localhost"})
	tp, err := s.TrackedProject(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.Databases) != 2 {
		t.Fatalf("databases = %+v", tp.Databases)
	}
	k0, k1 := project.DatabaseKey(tp.Databases[0]), project.DatabaseKey(tp.Databases[1])
	if k0 != shopKey || k1 != "manual:"+itoa(id) || k0 == k1 {
		t.Fatalf("keys = %q, %q", k0, k1)
	}
	for _, k := range []string{k0, k1} {
		if _, ok := tp.Schedules[k]; !ok {
			t.Fatalf("no schedule for %q", k)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
