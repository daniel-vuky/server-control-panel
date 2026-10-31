package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

func sampleBackup(projectID int64, key string) Backup {
	return Backup{
		ProjectID: projectID, ProjectName: "Shop",
		DBType: "postgresql", DBName: "shop", DBLocationKind: "container", DBLocation: "db", DBService: "db",
		DBKey: key, State: "queued", StartedBy: "dev@localhost", CreatedAt: driveTime,
		Steps: []BackupStep{{Step: "dump", State: "waiting"}, {Step: "compress", State: "waiting"}},
	}
}

func TestBackupInsertUpdateList(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	b := sampleBackup(1, "postgresql:db:shop")
	id, err := s.InsertBackup(ctx, b)
	if err != nil || id == 0 {
		t.Fatalf("InsertBackup = %d, %v", id, err)
	}
	b.ID = id
	got, err := s.GetBackup(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, b) {
		t.Fatalf("GetBackup = %+v\nwant %+v", got, b)
	}

	// A fresh row with no steps reads back as an empty slice, not nil.
	bare := sampleBackup(1, "k")
	bare.Steps = nil
	bid, err := s.InsertBackup(ctx, bare)
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GetBackup(ctx, bid); g.Steps == nil || len(g.Steps) != 0 {
		t.Fatalf("Steps = %#v, want empty non-nil", g.Steps)
	}

	started, finished := driveTime.Add(time.Second), driveTime.Add(40*time.Second)
	b.State, b.FailedStep = "failed", "upload"
	b.DBService = "postgres" // the worker's resolved service is stored too
	b.Steps = []BackupStep{{Step: "dump", State: "done", Detail: "pg_dump · 18s"}, {Step: "upload", State: "failed", Detail: "Google Drive is full."}}
	b.Error, b.Hint = "Upload: Google Drive is full.", "h"
	b.RawBytes, b.SizeBytes = 212, 38
	b.FileName, b.DriveFileID, b.DriveLink = "shop.sql.gz", "fid", "https://drive/x"
	b.StartedAt, b.FinishedAt, b.DurationMS = &started, &finished, 39000
	if err := s.UpdateBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetBackup(ctx, id)
	if !reflect.DeepEqual(got, b) {
		t.Fatalf("after update = %+v\nwant %+v", got, b)
	}
	if err := s.UpdateBackup(ctx, Backup{ID: 999, State: "ok"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateBackup unknown = %v, want ErrNotFound", err)
	}
	if _, err := s.GetBackup(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBackup unknown = %v, want ErrNotFound", err)
	}

	// List: newest first, paged, with `more`, scoped to the project.
	var ids []int64
	for i := 0; i < 5; i++ {
		x := sampleBackup(2, "k")
		x.State = "ok"
		xid, err := s.InsertBackup(ctx, x)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, xid)
	}
	page, more, err := s.ProjectBackups(ctx, 2, 0, 2)
	if err != nil || !more || len(page) != 2 || page[0].ID != ids[4] || page[1].ID != ids[3] {
		t.Fatalf("page 1 = %v more=%v err=%v", idsOf(page), more, err)
	}
	page, more, err = s.ProjectBackups(ctx, 2, page[1].ID, 2)
	if err != nil || !more || idsOf(page)[0] != ids[2] || idsOf(page)[1] != ids[1] {
		t.Fatalf("page 2 = %v more=%v err=%v", idsOf(page), more, err)
	}
	page, more, err = s.ProjectBackups(ctx, 2, page[1].ID, 2)
	if err != nil || more || len(page) != 1 || page[0].ID != ids[0] {
		t.Fatalf("page 3 = %v more=%v err=%v", idsOf(page), more, err)
	}
	page, more, err = s.ProjectBackups(ctx, 2, 0, 5)
	if err != nil || more || len(page) != 5 {
		t.Fatalf("exact fit = %v more=%v err=%v", idsOf(page), more, err)
	}
	page, more, err = s.ProjectBackups(ctx, 77, 0, 5)
	if err != nil || more || page == nil || len(page) != 0 {
		t.Fatalf("empty = %#v more=%v err=%v", page, more, err)
	}
}

func idsOf(bs []Backup) []int64 {
	out := make([]int64, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

func TestActiveBackup(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, ok, err := s.ActiveBackup(ctx, 1, "k"); ok || err != nil {
		t.Fatalf("none = %v, %v", ok, err)
	}
	b := sampleBackup(1, "k")
	id, _ := s.InsertBackup(ctx, b)
	if got, ok, err := s.ActiveBackup(ctx, 1, "k"); !ok || err != nil || got != id {
		t.Fatalf("queued = %d %v %v", got, ok, err)
	}
	if _, ok, _ := s.ActiveBackup(ctx, 1, "other"); ok {
		t.Fatal("another db_key matched")
	}
	if _, ok, _ := s.ActiveBackup(ctx, 2, "k"); ok {
		t.Fatal("another project matched")
	}
	b.ID, b.State = id, "running"
	if err := s.UpdateBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := s.ActiveBackup(ctx, 1, "k"); !ok || got != id {
		t.Fatal("running not active")
	}
	for _, st := range []string{"ok", "failed", "refused", "interrupted"} {
		b.State = st
		if err := s.UpdateBackup(ctx, b); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := s.ActiveBackup(ctx, 1, "k"); ok {
			t.Fatalf("%s counted as active", st)
		}
	}
}

func TestOpenInterruptsActiveBackups(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var idsByState = map[string]int64{}
	for _, st := range []string{"queued", "running", "ok", "failed"} {
		b := sampleBackup(1, "k"+st)
		b.State = st
		if st == "running" {
			b.Steps = []BackupStep{{Step: "dump", State: "done", Detail: "pg_dump · 2s"}, {Step: "compress", State: "running", Detail: "gzip"},
				{Step: "upload", State: "waiting"}, {Step: "prune", State: "waiting"}}
		}
		id, err := s.InsertBackup(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		idsByState[st] = id
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, st := range []string{"queued", "running"} {
		b, err := s.GetBackup(ctx, idsByState[st])
		if err != nil {
			t.Fatal(err)
		}
		if b.State != "interrupted" || b.Error != "The panel restarted during this backup" || b.FinishedAt == nil {
			t.Fatalf("%s row after reopen = %+v", st, b)
		}
	}
	// No step is left running or waiting: a running step failed, a waiting one
	// skipped, the others kept with their detail.
	running, _ := s.GetBackup(ctx, idsByState["running"])
	wantRunning := []BackupStep{{Step: "dump", State: "done", Detail: "pg_dump · 2s"}, {Step: "compress", State: "failed", Detail: "gzip"},
		{Step: "upload", State: "skipped"}, {Step: "prune", State: "skipped"}}
	if !reflect.DeepEqual(running.Steps, wantRunning) {
		t.Errorf("running row steps = %+v\nwant %+v", running.Steps, wantRunning)
	}
	queued, _ := s.GetBackup(ctx, idsByState["queued"])
	wantQueued := []BackupStep{{Step: "dump", State: "skipped"}, {Step: "compress", State: "skipped"}}
	if !reflect.DeepEqual(queued.Steps, wantQueued) {
		t.Errorf("queued row steps = %+v\nwant %+v", queued.Steps, wantQueued)
	}
	for _, st := range []string{"ok", "failed"} {
		b, _ := s.GetBackup(ctx, idsByState[st])
		if b.State != st || b.FinishedAt != nil || b.Error != "" {
			t.Fatalf("%s row touched: %+v", st, b)
		}
		// A final row's steps are never rewritten, even a stray waiting one.
		if !reflect.DeepEqual(b.Steps, sampleBackup(1, "").Steps) {
			t.Errorf("%s row steps rewritten: %+v", st, b.Steps)
		}
	}
}

func TestBackupStateCheck(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b := sampleBackup(1, "k")
	b.State = "bogus"
	if _, err := s.InsertBackup(ctx, b); err == nil {
		t.Fatal("InsertBackup accepted state bogus")
	}
	b.State, b.FailedStep = "failed", "bogus"
	if _, err := s.InsertBackup(ctx, b); err == nil {
		t.Fatal("InsertBackup accepted failed_step bogus")
	}
	for _, st := range []string{"queued", "running", "ok", "failed", "refused", "interrupted"} {
		b.State, b.FailedStep = st, ""
		if _, err := s.InsertBackup(ctx, b); err != nil {
			t.Fatalf("state %s: %v", st, err)
		}
	}
	for _, step := range []string{"dump", "compress", "upload", "prune"} {
		b.State, b.FailedStep = "failed", step
		if _, err := s.InsertBackup(ctx, b); err != nil {
			t.Fatalf("failed_step %s: %v", step, err)
		}
	}
	if n := count(t, s.db, `SELECT count(*) FROM backups WHERE failed_step IS NULL`); n != 6 {
		t.Fatalf("%d rows with NULL failed_step, want 6", n)
	}
}

func TestTrackedServiceDetails(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, _, err := s.AddWorkspace(ctx, NewWorkspace{Name: "apps", Path: "/srv/apps", Depth: 2, LastScanAt: scanTime}, sampleProjects()); err != nil {
		t.Fatal(err)
	}
	ts, err := s.TrackedProjects(ctx)
	if err != nil || len(ts) != 1 {
		t.Fatalf("TrackedProjects = %+v, %v", ts, err)
	}
	want := []project.Service{
		{Name: "web", Image: "nginx", Ports: []string{"8080:80/tcp"}, Volumes: []project.Volume{{Type: "bind", Source: "/srv/apps/shop/html", Target: "/usr/share/nginx/html"}}},
		{Name: "db", Image: "postgres:16"},
	}
	if len(ts[0].ServiceDetails) != 2 || ts[0].ServiceDetails[0].Name != "web" || ts[0].ServiceDetails[0].Image != "nginx" ||
		ts[0].ServiceDetails[1].Image != "postgres:16" || ts[0].ServiceDetails[0].Volumes[0].Target != want[0].Volumes[0].Target {
		t.Fatalf("ServiceDetails = %+v", ts[0].ServiceDetails)
	}
	if !reflect.DeepEqual(ts[0].Services, []string{"web", "db"}) {
		t.Fatalf("Services = %v", ts[0].Services)
	}

	// A project whose config could not be read has [] , never nil.
	if _, err := s.db.Exec(`UPDATE projects SET details = '{}'`); err != nil {
		t.Fatal(err)
	}
	ts, _ = s.TrackedProjects(ctx)
	if ts[0].ServiceDetails == nil || len(ts[0].ServiceDetails) != 0 {
		t.Fatalf("ServiceDetails = %#v, want empty non-nil", ts[0].ServiceDetails)
	}
}

// storedRow inserts a backup of (projectID, key) in state with a Drive file
// when fileID is not empty, and returns its id.
func storedRow(t *testing.T, s *Store, projectID int64, key, state, fileID string) int64 {
	t.Helper()
	b := sampleBackup(projectID, key)
	b.State, b.DriveFileID = state, fileID
	id, err := s.InsertBackup(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRetentionCandidates(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	const key = "postgresql:db:shop"
	a := storedRow(t, s, 1, key, "ok", "f1")
	storedRow(t, s, 1, key, "failed", "")             // no file: never a candidate
	storedRow(t, s, 1, key, "refused", "")            // no file
	c := storedRow(t, s, 1, key, "interrupted", "f3") // a file stored before the end: counted
	d := storedRow(t, s, 1, key, "ok", "f4")
	pruned := storedRow(t, s, 1, key, "ok", "f5")
	storedRow(t, s, 1, "mysql:db:crm", "ok", "f6") // another database
	storedRow(t, s, 2, key, "ok", "f7")            // another project
	running := storedRow(t, s, 1, key, "running", "f8")
	if err := s.MarkPruned(ctx, pruned, driveTime); err != nil {
		t.Fatal(err)
	}

	got, err := s.RetentionCandidates(ctx, 1, key)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{running, d, c, a}; !reflect.DeepEqual(idsOf(got), want) {
		t.Fatalf("candidates = %v, want %v (newest first)", idsOf(got), want)
	}
	if got[0].DriveFileID != "f8" || got[0].State != "running" {
		t.Errorf("first candidate = %+v", got[0])
	}
	if none, err := s.RetentionCandidates(ctx, 3, key); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("none = %#v, %v", none, err)
	}
}

func TestMarkPruned(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	id := storedRow(t, s, 1, "k", "ok", "f1")
	if b, _ := s.GetBackup(ctx, id); b.PrunedAt != nil {
		t.Fatalf("PrunedAt = %v before the mark", b.PrunedAt)
	}
	at := driveTime.Add(time.Hour)
	if err := s.MarkPruned(ctx, id, at); err != nil {
		t.Fatal(err)
	}
	b, err := s.GetBackup(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if b.PrunedAt == nil || !b.PrunedAt.Equal(at) {
		t.Fatalf("PrunedAt = %v, want %v", b.PrunedAt, at)
	}
	// The worker's own update never clears a stamp.
	b.Error = "x"
	if err := s.UpdateBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	if b2, _ := s.GetBackup(ctx, id); b2.PrunedAt == nil || !b2.PrunedAt.Equal(at) {
		t.Fatalf("PrunedAt after UpdateBackup = %v", b2.PrunedAt)
	}
	// The list reads it too.
	page, _, err := s.ProjectBackups(ctx, 1, 0, 10)
	if err != nil || len(page) != 1 || page[0].PrunedAt == nil {
		t.Fatalf("ProjectBackups = %+v, %v", page, err)
	}
	if err := s.MarkPruned(ctx, 999, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkPruned unknown = %v, want ErrNotFound", err)
	}
}

func TestStoredCopies(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	const pg, my = "postgresql:db:shop", "mysql:db:crm"
	storedRow(t, s, 1, pg, "ok", "f1")
	storedRow(t, s, 1, pg, "interrupted", "f2")
	storedRow(t, s, 1, pg, "failed", "")
	p := storedRow(t, s, 1, pg, "ok", "f3")
	storedRow(t, s, 1, my, "ok", "f4")
	storedRow(t, s, 2, pg, "ok", "f5")
	if err := s.MarkPruned(ctx, p, driveTime); err != nil {
		t.Fatal(err)
	}
	got, err := s.StoredCopies(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{pg: 2, my: 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("StoredCopies = %v, want %v", got, want)
	}
	if none, err := s.StoredCopies(ctx, 9); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("none = %#v, %v", none, err)
	}
}

// rowAt inserts a backup created at at, in state, for (projectID, key), with a
// Drive file when fileID is not empty, and returns its id.
func rowAt(t *testing.T, s *Store, projectID int64, key, state, fileID string, size int64, at time.Time) int64 {
	t.Helper()
	b := sampleBackup(projectID, key)
	b.State, b.DriveFileID, b.SizeBytes, b.CreatedAt = state, fileID, size, at
	id, err := s.InsertBackup(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestListBackupsFilters(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	day := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }

	a1 := rowAt(t, s, 1, "k", "ok", "f1", 1, day(1, 10))
	a2 := rowAt(t, s, 1, "k", "failed", "", 0, day(2, 10))
	a3 := rowAt(t, s, 1, "k", "refused", "", 0, day(3, 10))
	a4 := rowAt(t, s, 1, "k", "interrupted", "", 0, day(4, 10))
	b1 := rowAt(t, s, 2, "k", "ok", "f2", 1, day(5, 10))
	b2 := rowAt(t, s, 2, "k", "queued", "", 0, day(6, 10))
	b3 := rowAt(t, s, 2, "k", "running", "", 0, day(7, 10))

	list := func(f BackupFilter) ([]int64, bool) {
		t.Helper()
		got, more, err := s.ListBackups(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return idsOf(got), more
	}
	eq := func(name string, got, want []int64, more, wantMore bool) {
		t.Helper()
		if !reflect.DeepEqual(got, want) || more != wantMore {
			t.Errorf("%s = %v more=%v, want %v more=%v", name, got, more, want, wantMore)
		}
	}

	got, more := list(BackupFilter{Limit: 50})
	eq("all", got, []int64{b3, b2, b1, a4, a3, a2, a1}, more, false)

	got, more = list(BackupFilter{ProjectID: 1, Limit: 50})
	eq("project", got, []int64{a4, a3, a2, a1}, more, false)

	got, more = list(BackupFilter{Result: "ok", Limit: 50})
	eq("ok", got, []int64{b1, a1}, more, false)

	got, more = list(BackupFilter{Result: "failed", Limit: 50})
	eq("failed has no queued or running", got, []int64{a4, a3, a2}, more, false)

	got, more = list(BackupFilter{Result: "failed", ProjectID: 2, Limit: 50})
	eq("failed in project 2", got, []int64{}, more, false)

	// From is inclusive, To exclusive.
	got, more = list(BackupFilter{From: day(2, 10), To: day(4, 10), Limit: 50})
	eq("from/to", got, []int64{a3, a2}, more, false)
	got, more = list(BackupFilter{From: day(5, 10), Limit: 50})
	eq("from only", got, []int64{b3, b2, b1}, more, false)
	got, more = list(BackupFilter{To: day(2, 10), Limit: 50})
	eq("to only", got, []int64{a1}, more, false)

	// A bound in another zone is compared as the same UTC instant.
	plus7 := time.FixedZone("UTC+7", 7*3600)
	got, _ = list(BackupFilter{From: time.Date(2026, 10, 2, 17, 0, 0, 0, plus7), To: time.Date(2026, 10, 4, 17, 0, 0, 0, plus7), Limit: 50})
	if !reflect.DeepEqual(got, []int64{a3, a2}) {
		t.Errorf("zone bounds = %v, want %v", got, []int64{a3, a2})
	}

	// Paging: limit+1 gives more; the cursor continues below the last id.
	got, more = list(BackupFilter{Limit: 3})
	eq("page 1", got, []int64{b3, b2, b1}, more, true)
	got, more = list(BackupFilter{Limit: 3, Before: b1})
	eq("page 2", got, []int64{a4, a3, a2}, more, true)
	got, more = list(BackupFilter{Limit: 3, Before: a2})
	eq("page 3", got, []int64{a1}, more, false)
	got, more = list(BackupFilter{Limit: 4, Before: a2, Result: "ok"})
	eq("cursor with filter", got, []int64{a1}, more, false)

	got, more = list(BackupFilter{})
	eq("no limit", got, []int64{}, more, false)
}

func TestBackupSummary(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	day := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.UTC) }
	// Seven day starts (Oct 25..31) and tomorrow's start.
	var starts []time.Time
	for d := 25; d <= 32; d++ {
		starts = append(starts, time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC))
	}

	// Outside the window: before the first day start and at/after tomorrow.
	rowAt(t, s, 1, "k", "failed", "", 0, day(24, 23, 59))
	rowAt(t, s, 1, "k", "ok", "", 0, starts[7])

	// Day 0 (Oct 25): one ok on Drive (project 1), one pruned ok (excluded from drive).
	rowAt(t, s, 1, "k", "ok", "f1", 100, day(25, 0, 0))
	pruned := rowAt(t, s, 1, "k", "ok", "f2", 1000, day(25, 12, 0))
	if err := s.MarkPruned(ctx, pruned, day(26, 1, 0)); err != nil {
		t.Fatal(err)
	}
	// Day 3 (Oct 28): an ok on Drive in project 2, a failed, a refused and an interrupted.
	rowAt(t, s, 2, "k", "ok", "f3", 50, day(28, 23, 59))
	rowAt(t, s, 2, "k", "failed", "", 0, day(28, 3, 0))
	f2 := rowAt(t, s, 2, "k", "refused", "", 0, day(28, 4, 0))
	rowAt(t, s, 2, "k", "interrupted", "", 0, day(28, 5, 0))
	// Day 6 (Oct 31): queued and running count in neither bucket.
	rowAt(t, s, 2, "k", "queued", "", 0, day(31, 8, 0))
	rowAt(t, s, 2, "k", "running", "", 0, day(31, 9, 0))
	// A failed row with a Drive file id still counts as a copy on Drive.
	f4 := rowAt(t, s, 3, "k", "failed", "f4", 7, day(30, 1, 0))
	// Failures outside the window but newer by id than the in-window ones: a
	// refused one before the first day and a failed one after tomorrow's start.
	rowAt(t, s, 3, "k", "refused", "", 0, day(24, 12, 0))
	rowAt(t, s, 3, "k", "failed", "", 0, starts[7].Add(time.Hour))

	got, err := s.BackupSummary(ctx, starts)
	if err != nil {
		t.Fatal(err)
	}
	if got.DriveBytes != 157 || got.DriveFiles != 3 || got.DriveProjects != 3 {
		t.Errorf("drive = %d bytes %d files %d projects, want 157, 3, 3", got.DriveBytes, got.DriveFiles, got.DriveProjects)
	}
	if len(got.Days) != 7 {
		t.Fatalf("Days = %d, want 7", len(got.Days))
	}
	want := []DayCount{
		{OK: 2}, {}, {}, {OK: 1, Failed: 3}, {}, {Failed: 1}, {},
	}
	for i, w := range want {
		d := got.Days[i]
		if !d.Start.Equal(starts[i]) || d.OK != w.OK || d.Failed != w.Failed {
			t.Errorf("Days[%d] = %+v, want start %v ok %d failed %d", i, d, starts[i], w.OK, w.Failed)
		}
	}
	if got.OK7d != 3 || got.Failed7d != 4 {
		t.Errorf("7d = %d ok %d failed, want 3 and 4", got.OK7d, got.Failed7d)
	}
	// Recent failures: failed or refused only, inside the 7-day window, newest
	// first, at most 2. The interrupted one (day 28) and the newer-by-id rows
	// outside the window are left out.
	var rf []int64
	for _, b := range got.RecentFailures {
		rf = append(rf, b.ID)
	}
	if !reflect.DeepEqual(rf, []int64{f4, f2}) {
		t.Errorf("RecentFailures = %v, want [%d %d] (in-window failed/refused, newest first)", rf, f4, f2)
	}

	if _, err := s.BackupSummary(ctx, starts[:7]); err == nil {
		t.Error("BackupSummary with 7 instants succeeded, want an error")
	}

	empty, err := open(t).BackupSummary(ctx, starts)
	if err != nil {
		t.Fatal(err)
	}
	if empty.DriveBytes != 0 || empty.OK7d != 0 || empty.RecentFailures == nil || len(empty.RecentFailures) != 0 || len(empty.Days) != 7 {
		t.Errorf("empty summary = %+v", empty)
	}
}
