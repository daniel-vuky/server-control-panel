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
	for _, st := range []string{"ok", "failed"} {
		if b, _ := s.GetBackup(ctx, idsByState[st]); b.State != st || b.FinishedAt != nil || b.Error != "" {
			t.Fatalf("%s row touched: %+v", st, b)
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
