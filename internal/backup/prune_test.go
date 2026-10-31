package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// pruneHooks watch and script the fake Drive's deletes. del, when set, runs
// before the fake's own Delete: an error answers instead of it.
type pruneHooks struct {
	n       atomic.Int64
	mu      sync.Mutex
	del     func(ctx context.Context, n int, fileID string) error
	deletes []string
}

func (h *pruneHooks) setDelete(fn func(ctx context.Context, n int, fileID string) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.del, h.deletes = fn, nil
}

func (h *pruneHooks) deleted() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.deletes...)
}

// pruneClient gives every upload a unique name (the fake replaces a file of
// the same name, and two backups in the same minute share one) and passes
// Delete through the hooks.
type pruneClient struct {
	drive.Client
	h *pruneHooks
}

func (c pruneClient) Session(refresh string) drive.Session {
	return pruneSession{c.Client.Session(refresh), c.h}
}

type pruneSession struct {
	drive.Session
	h *pruneHooks
}

func (s pruneSession) Upload(ctx context.Context, folderID, name string, size int64, r io.Reader, progress func(int64)) (string, string, error) {
	return s.Session.Upload(ctx, folderID, fmt.Sprintf("%d-%s", s.h.n.Add(1), name), size, r, progress)
}

func (s pruneSession) Delete(ctx context.Context, fileID string) error {
	s.h.mu.Lock()
	s.h.deletes = append(s.h.deletes, fileID)
	n, del := len(s.h.deletes), s.h.del
	s.h.mu.Unlock()
	if del != nil {
		if err := del(ctx, n, fileID); err != nil {
			return err
		}
	}
	return s.Session.Delete(ctx, fileID)
}

func newPruneEnv(t *testing.T) (*env, *pruneHooks) {
	t.Helper()
	h := &pruneHooks{}
	e := newEnv(t, envOpt{client: func(c drive.Client) drive.Client { return pruneClient{c, h} }})
	return e, h
}

// seed stores earlier copies of database key of project projectID, oldest
// first, one per state, each with a file on Drive (in the project's
// sub-folder), and returns their rows.
func (e *env) seed(t *testing.T, projectID int64, key string, states ...string) []store.Backup {
	t.Helper()
	ctx := context.Background()
	sess, folder, err := e.drv.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := sess.FindOrCreateFolder(ctx, "shop", folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Backup
	for i, st := range states {
		name := fmt.Sprintf("old-%d.sql.gz", i)
		fid, _, err := sess.Upload(ctx, sub.ID, name, 3, strings.NewReader("old"), nil)
		if err != nil {
			t.Fatal(err)
		}
		b := store.Backup{
			ProjectID: projectID, ProjectName: "Shop", DBType: "postgresql", DBName: "x", DBLocationKind: "container",
			DBLocation: "db", DBKey: key, State: st, FileName: name, DriveFileID: fid, StartedBy: actor,
			CreatedAt: time.Now().Add(-time.Hour), Steps: newSteps(stepDone),
		}
		if b.ID, err = e.st.InsertBackup(ctx, b); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

// onDrive reports whether the fake still holds fileID.
func (e *env) onDrive(t *testing.T, fileID string) bool {
	t.Helper()
	ctx := context.Background()
	sess, _, err := e.drv.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := sess.Download(ctx, fileID)
	if err == nil {
		_ = rc.Close()
		return true
	}
	if drive.CodeOf(err) != drive.CodeFolderMissing {
		t.Fatalf("Download(%s): %v", fileID, err)
	}
	return false
}

func (e *env) pruned(t *testing.T, id int64) bool {
	t.Helper()
	b, err := e.st.GetBackup(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return b.PrunedAt != nil
}

// wantCopies checks which seeded rows are removed (on Drive no more and
// stamped) and which are kept.
func (e *env) wantCopies(t *testing.T, rows []store.Backup, removed ...bool) {
	t.Helper()
	if len(removed) != len(rows) {
		t.Fatalf("%d rows, %d expectations", len(rows), len(removed))
	}
	for i, gone := range removed {
		b := rows[i]
		if e.onDrive(t, b.DriveFileID) == gone || e.pruned(t, b.ID) != gone {
			t.Errorf("seed %d (backup %d): on Drive %v, pruned %v; want removed %v", i, b.ID, e.onDrive(t, b.DriveFileID), e.pruned(t, b.ID), gone)
		}
	}
}

func TestPruneRemovesBeyondKeep(t *testing.T) {
	e, h := newPruneEnv(t)
	e.setSchedule(t, keyPG, 3, true)
	old := e.seed(t, e.shop.ID, keyPG, "ok", "ok", "ok", "ok", "ok")
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "ok" || b.FailedStep != "" {
		t.Fatalf("row = %+v", b)
	}
	if b.Steps[iPrune].State != "done" || b.Steps[iPrune].Detail != "Removed 3 old copies · keeping 3" {
		t.Errorf("prune = %+v", b.Steps[iPrune])
	}
	e.wantCopies(t, old, true, true, true, false, false)
	// Oldest first.
	if got, want := h.deleted(), []string{old[0].DriveFileID, old[1].DriveFileID, old[2].DriveFileID}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("deletes = %v, want %v", got, want)
	}
	if !e.onDrive(t, b.DriveFileID) || b.PrunedAt != nil {
		t.Error("the new copy was removed")
	}
	acts := e.activity(t)
	if len(acts) != 1 || acts[0].Result != store.ResultOK {
		t.Errorf("activity = %+v", acts)
	}
}

func TestPruneOneCopyWord(t *testing.T) {
	e, _ := newPruneEnv(t)
	e.setSchedule(t, keyPG, 1, true)
	old := e.seed(t, e.shop.ID, keyPG, "ok")
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.Steps[iPrune].Detail != "Removed 1 old copy · keeping 1" {
		t.Errorf("prune = %+v", b.Steps[iPrune])
	}
	e.wantCopies(t, old, true)
}

func TestPruneNothingToRemove(t *testing.T) {
	e, h := newPruneEnv(t)
	old := e.seed(t, e.shop.ID, keyPG, "ok")
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "ok" || b.Steps[iPrune].State != "done" || b.Steps[iPrune].Detail != "Nothing to remove · 2 of 14 kept" {
		t.Fatalf("row = %q, prune = %+v", b.State, b.Steps[iPrune])
	}
	e.wantCopies(t, old, false)
	if len(h.deleted()) != 0 {
		t.Errorf("deletes = %v", h.deleted())
	}
}

func TestPruneFolderMissingCountsAsRemoved(t *testing.T) {
	e, _ := newPruneEnv(t)
	e.setSchedule(t, keyPG, 1, true)
	old := e.seed(t, e.shop.ID, keyPG, "ok", "ok", "ok")
	// The middle copy was removed from Drive by hand: Drive answers
	// folder_missing, which counts as removed.
	ctx := context.Background()
	sess, _, err := e.drv.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Delete(ctx, old[1].DriveFileID); err != nil {
		t.Fatal(err)
	}
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "ok" || b.Steps[iPrune].State != "done" || b.Steps[iPrune].Detail != "Removed 3 old copies · keeping 1" {
		t.Fatalf("row = %q, prune = %+v", b.State, b.Steps[iPrune])
	}
	e.wantCopies(t, old, true, true, true)
	if strings.Contains(b.Steps[iPrune].Detail, "missing") {
		t.Errorf("the not-found words were shown: %q", b.Steps[iPrune].Detail)
	}
}

func TestPruneDriveErrorKeepsBackupOK(t *testing.T) {
	e, h := newPruneEnv(t)
	e.setSchedule(t, keyPG, 2, true)
	old := e.seed(t, e.shop.ID, keyPG, "ok", "ok", "ok", "ok")
	h.setDelete(func(_ context.Context, n int, _ string) error {
		if n == 2 {
			return &drive.Error{Code: drive.CodeQuota, Err: errors.New("raw google body")}
		}
		return nil
	})
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "ok" || b.FailedStep != "" || b.Error != "" {
		t.Fatalf("row = %q %q %q", b.State, b.FailedStep, b.Error)
	}
	want := "Could not remove 2 old copies: Google Drive is full. It is retried after the next backup."
	if b.Steps[iPrune].State != "failed" || b.Steps[iPrune].Detail != want {
		t.Errorf("prune = %+v, want failed %q", b.Steps[iPrune], want)
	}
	// The first was removed; the delete that failed stopped the rest.
	e.wantCopies(t, old, true, false, false, false)
	if len(h.deleted()) != 2 {
		t.Errorf("deletes = %v", h.deleted())
	}
	acts := e.activity(t)
	if len(acts) != 1 || acts[0].Result != store.ResultOK {
		t.Errorf("activity = %+v", acts)
	}

	// The next success picks up the leftovers: new2, new1 kept.
	h.setDelete(nil)
	b2 := e.waitDone(t, e.enqueue(t, keyPG))
	if b2.State != "ok" || b2.Steps[iPrune].State != "done" || b2.Steps[iPrune].Detail != "Removed 3 old copies · keeping 2" {
		t.Fatalf("next = %q, prune = %+v", b2.State, b2.Steps[iPrune])
	}
	e.wantCopies(t, old, true, true, true, true)
	if !e.onDrive(t, b.DriveFileID) || !e.onDrive(t, b2.DriveFileID) {
		t.Error("a kept copy was removed")
	}
}

func TestPruneTokenExpiredWords(t *testing.T) {
	e, h := newPruneEnv(t)
	e.setSchedule(t, keyPG, 1, true)
	old := e.seed(t, e.shop.ID, keyPG, "ok")
	h.setDelete(func(context.Context, int, string) error {
		return &drive.Error{Code: drive.CodeTokenExpired}
	})
	b := e.waitDone(t, e.enqueue(t, keyPG))
	want := "Could not remove 1 old copy: " + drive.Words(drive.CodeTokenExpired) + " It is retried after the next backup."
	if b.State != "ok" || b.Steps[iPrune].State != "failed" || b.Steps[iPrune].Detail != want {
		t.Fatalf("row = %q, prune = %+v", b.State, b.Steps[iPrune])
	}
	// Drive is expired now, so the fake is not read: the row is not stamped.
	if e.pruned(t, old[0].ID) || len(h.deleted()) != 1 {
		t.Errorf("pruned %v, deletes %v", e.pruned(t, old[0].ID), h.deleted())
	}
	if st := e.drv.Status().State; st != drive.StateExpired {
		t.Errorf("drive state = %s, want expired", st)
	}
}

func TestPruneFailedBackupRemovesNothing(t *testing.T) {
	e, h := newPruneEnv(t)
	e.setSchedule(t, keyPG, 1, true)
	old := e.seed(t, e.shop.ID, keyPG, "ok", "ok")
	e.r.mu.Lock()
	e.r.dump = func(context.Context, call, io.Writer) ([]byte, error) {
		return []byte("pg_dump: error: boom\n"), &runner.ExitError{Code: 1}
	}
	e.r.mu.Unlock()
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "failed" || stepStates(b) != "dump=failed compress=skipped upload=skipped prune=skipped" || b.Steps[iPrune].Detail != "temp file deleted" {
		t.Fatalf("row = %q %s %q", b.State, stepStates(b), b.Steps[iPrune].Detail)
	}
	e.wantCopies(t, old, false, false)
	if len(h.deleted()) != 0 {
		t.Errorf("deletes = %v", h.deleted())
	}
}

func TestPruneLoweredKeepOnNextSuccess(t *testing.T) {
	e, h := newPruneEnv(t)
	old := e.seed(t, e.shop.ID, keyPG, "ok", "ok", "ok", "ok", "ok")
	first := e.waitDone(t, e.enqueue(t, keyPG))
	if first.Steps[iPrune].Detail != "Nothing to remove · 6 of 14 kept" {
		t.Fatalf("prune = %+v", first.Steps[iPrune])
	}
	// Lowering the number removes nothing by itself...
	e.setSchedule(t, keyPG, 3, true)
	e.wantCopies(t, old, false, false, false, false, false)
	// ...nor does a failed backup...
	e.r.mu.Lock()
	e.r.dump = func(context.Context, call, io.Writer) ([]byte, error) {
		return nil, &runner.ExitError{Code: 1}
	}
	e.r.mu.Unlock()
	if fb := e.waitDone(t, e.enqueue(t, keyPG)); fb.State != "failed" {
		t.Fatalf("failed row = %q", fb.State)
	}
	e.wantCopies(t, old, false, false, false, false, false)
	if len(h.deleted()) != 0 {
		t.Errorf("deletes = %v", h.deleted())
	}
	// ...the next success removes everything past 3: new, first, old[4] kept.
	e.r.mu.Lock()
	e.r.dump = nil
	e.r.mu.Unlock()
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "ok" || b.Steps[iPrune].Detail != "Removed 4 old copies · keeping 3" {
		t.Fatalf("row = %q, prune = %+v", b.State, b.Steps[iPrune])
	}
	e.wantCopies(t, old, true, true, true, true, false)
	if !e.onDrive(t, first.DriveFileID) {
		t.Error("the first backup's copy was removed")
	}
}

func TestPruneCountsInterruptedRowWithFile(t *testing.T) {
	e, _ := newPruneEnv(t)
	e.setSchedule(t, keyPG, 2, true)
	// An interrupted row stored between the upload's persist and the end holds
	// a real file: it counts, and is kept as the newer one.
	old := e.seed(t, e.shop.ID, keyPG, "ok", "interrupted")
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.Steps[iPrune].Detail != "Removed 1 old copy · keeping 2" {
		t.Fatalf("prune = %+v", b.Steps[iPrune])
	}
	e.wantCopies(t, old, true, false)

	// Keep 1: the interrupted row's file goes too.
	e.setSchedule(t, keyPG, 1, true)
	b2 := e.waitDone(t, e.enqueue(t, keyPG))
	if b2.Steps[iPrune].Detail != "Removed 2 old copies · keeping 1" {
		t.Fatalf("prune = %+v", b2.Steps[iPrune])
	}
	e.wantCopies(t, old, true, true)
}

func TestPruneIgnoresOtherDatabasesAndProjects(t *testing.T) {
	e, h := newPruneEnv(t)
	e.setSchedule(t, keyPG, 1, true)
	other := e.seed(t, e.shop.ID, keyMySQL, "ok", "ok")
	foreign := e.seed(t, e.shop.ID+100, keyPG, "ok", "ok")
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.Steps[iPrune].Detail != "Nothing to remove · 1 of 1 kept" {
		t.Fatalf("prune = %+v", b.Steps[iPrune])
	}
	e.wantCopies(t, other, false, false)
	e.wantCopies(t, foreign, false, false)
	if len(h.deleted()) != 0 {
		t.Errorf("deletes = %v", h.deleted())
	}
}

// Close during Prune: the backup stays ok. The delete under way finishes (it
// runs on a context Close does not end), then Prune stops and leaves the rest
// for the next backup.
func TestCloseDuringPruneKeepsBackupOK(t *testing.T) {
	e, h := newPruneEnv(t)
	e.setSchedule(t, keyPG, 1, true)
	old := e.seed(t, e.shop.ID, keyPG, "ok", "ok", "ok")
	started := make(chan struct{})
	var ctxErr atomic.Value
	h.setDelete(func(ctx context.Context, n int, _ string) error {
		if n == 1 {
			close(started)
		}
		time.Sleep(200 * time.Millisecond)
		if err := ctx.Err(); err != nil {
			ctxErr.Store(err)
		}
		return nil
	})
	id := e.enqueue(t, keyPG)
	<-started
	e.m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err, _ := ctxErr.Load().(error); err != nil {
		t.Errorf("the delete's context ended: %v", err)
	}
	b, err := e.st.GetBackup(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != "ok" || b.FailedStep != "" || b.Error != "" || b.FinishedAt == nil {
		t.Fatalf("row = %q %q %q", b.State, b.FailedStep, b.Error)
	}
	want := "Could not remove 2 old copies: The panel restarted during this backup. It is retried after the next backup."
	if b.Steps[iPrune].State != "failed" || b.Steps[iPrune].Detail != want {
		t.Errorf("prune = %+v, want failed %q", b.Steps[iPrune], want)
	}
	e.wantCopies(t, old, true, false, false)
	if len(h.deleted()) != 1 {
		t.Errorf("deletes = %v", h.deleted())
	}
	acts := e.activity(t)
	if len(acts) != 1 || acts[0].Result != store.ResultOK {
		t.Errorf("activity = %+v", acts)
	}
}

// A panic in Prune (here in a Drive delete) fails only step 4: the copy is on
// Drive, so the backup stays ok and its activity finishes ok.
func TestPrunePanicKeepsBackupOK(t *testing.T) {
	e, h := newPruneEnv(t)
	var logs lockedBuffer
	e.m.log = slog.New(slog.NewTextHandler(&logs, nil))
	e.setSchedule(t, keyPG, 1, true)
	e.seed(t, e.shop.ID, keyPG, "ok")
	h.setDelete(func(context.Context, int, string) error {
		panic("boom " + devPass)
	})
	b := e.waitDone(t, e.enqueue(t, keyPG))
	if b.State != "ok" || b.FailedStep != "" || b.Error != "" {
		t.Fatalf("row = %q %q %q", b.State, b.FailedStep, b.Error)
	}
	want := "Could not remove old copies: internal error. It is retried after the next backup."
	if b.Steps[iPrune].State != "failed" || b.Steps[iPrune].Detail != want {
		t.Errorf("prune = %+v, want failed %q", b.Steps[iPrune], want)
	}
	if b.DriveFileID == "" || !e.onDrive(t, b.DriveFileID) {
		t.Error("the new copy is not on Drive")
	}
	acts := e.activity(t)
	if len(acts) != 1 || acts[0].Result != store.ResultOK {
		t.Errorf("activity = %+v", acts)
	}
	out := logs.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "prune panicked") {
		t.Errorf("log = %q", out)
	}
	assertNoSecret(t, "log", out)

	// The worker goes on: the next backup removes the leftover.
	h.setDelete(nil)
	b2 := e.waitDone(t, e.enqueue(t, keyPG))
	if b2.State != "ok" || b2.Steps[iPrune].State != "done" {
		t.Fatalf("next = %q, prune = %+v", b2.State, b2.Steps[iPrune])
	}
}
