package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

var driveTime = time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)

func TestMigration0005(t *testing.T) {
	s := open(t)
	for _, table := range []string{"drive", "drive_oauth_states", "backups"} {
		if n := count(t, s.db, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table); n != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
	for _, idx := range []string{"backups_project_idx", "backups_state_idx"} {
		if n := count(t, s.db, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?`, idx); n != 1 {
			t.Fatalf("index %s missing", idx)
		}
	}
	// The drive table holds one row only.
	ins := `INSERT INTO drive (id, refresh_token_enc, email, state, connected_by, connected_at) VALUES (?, 'v1:x', 'a@b.c', 'connected', 'me', '2026-10-30T10:00:00Z')`
	if _, err := s.db.Exec(ins, 2); err == nil {
		t.Fatal("drive accepted id 2")
	}
	if _, err := s.db.Exec(ins, 1); err != nil {
		t.Fatalf("drive id 1: %v", err)
	}
}

func TestDriveRowRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, ok, err := s.Drive(ctx); err != nil || ok {
		t.Fatalf("empty Drive = ok %v, %v", ok, err)
	}
	row := DriveRow{RefreshTokenEnc: "v1:abc", Email: "me@example.com", State: "connected", ConnectedBy: "dev@localhost", ConnectedAt: driveTime}
	if err := s.SaveDrive(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Drive(ctx)
	if err != nil || !ok {
		t.Fatalf("Drive = %v, %v", ok, err)
	}
	if got != row {
		t.Fatalf("Drive = %+v, want %+v", got, row)
	}
	if got.FolderID != "" || got.FolderName != "" {
		t.Fatalf("folder = %q %q, want empty", got.FolderID, got.FolderName)
	}
	if n := count(t, s.db, `SELECT count(*) FROM drive WHERE folder_id IS NULL AND folder_name IS NULL`); n != 1 {
		t.Fatal("empty folder is not stored NULL")
	}

	if err := s.SetDriveFolder(ctx, "f1", "Backups"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDriveState(ctx, "expired"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Drive(ctx)
	if got.FolderID != "f1" || got.FolderName != "Backups" || got.State != "expired" || got.RefreshTokenEnc != "v1:abc" {
		t.Fatalf("after updates: %+v", got)
	}

	// Save is an upsert: it replaces the one row.
	row.RefreshTokenEnc, row.Email, row.FolderID, row.FolderName = "v1:def", "other@example.com", "f2", "More"
	if err := s.SaveDrive(ctx, row); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Drive(ctx)
	if got != row || count(t, s.db, `SELECT count(*) FROM drive`) != 1 {
		t.Fatalf("after upsert: %+v", got)
	}

	if err := s.SetDriveState(ctx, "bogus"); err == nil {
		t.Fatal("SetDriveState accepted bogus")
	}
	if err := s.DeleteDrive(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Drive(ctx); ok {
		t.Fatal("Drive still present after delete")
	}
	if err := s.DeleteDrive(ctx); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if err := s.SetDriveFolder(ctx, "f", "n"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetDriveFolder without a row = %v, want ErrNotFound", err)
	}
	if err := s.SetDriveState(ctx, "expired"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetDriveState without a row = %v, want ErrNotFound", err)
	}
}

func TestOAuthStateSingleUseAndExpiry(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.PutOAuthState(ctx, "s1", "a@b.c", "/projects/1", "http://x/cb", driveTime); err != nil {
		t.Fatal(err)
	}
	actor, ret, redir, ok, err := s.TakeOAuthState(ctx, "s1", driveTime.Add(time.Minute))
	if err != nil || !ok || actor != "a@b.c" || ret != "/projects/1" || redir != "http://x/cb" {
		t.Fatalf("Take = %q %q %q %v %v", actor, ret, redir, ok, err)
	}
	if _, _, _, ok, _ := s.TakeOAuthState(ctx, "s1", driveTime.Add(time.Minute)); ok {
		t.Fatal("state used twice")
	}
	if _, _, _, ok, err := s.TakeOAuthState(ctx, "nope", driveTime); ok || err != nil {
		t.Fatalf("unknown state = %v, %v", ok, err)
	}

	// Expired: older than 10 minutes. Exactly 10 minutes still counts.
	for _, id := range []string{"old", "edge", "stale"} {
		if err := s.PutOAuthState(ctx, id, "a", "/", "r", driveTime); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, ok, _ := s.TakeOAuthState(ctx, "edge", driveTime.Add(10*time.Minute)); !ok {
		t.Fatal("a 10 minute old state was refused")
	}
	if _, _, _, ok, _ := s.TakeOAuthState(ctx, "old", driveTime.Add(10*time.Minute+time.Second)); ok {
		t.Fatal("an expired state was accepted")
	}
	// Expired rows are deleted on use, including ones nobody asked for.
	if n := count(t, s.db, `SELECT count(*) FROM drive_oauth_states`); n != 0 {
		t.Fatalf("%d states left, want 0 (stale deleted on use)", n)
	}
}

// Started connects that never come back do not pile up: a new state deletes
// the expired ones and keeps the live ones.
func TestPutOAuthStatePrunesExpired(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, id := range []string{"a", "b"} {
		if err := s.PutOAuthState(ctx, id, "a", "/", "r", driveTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutOAuthState(ctx, "live", "a", "/", "r", driveTime.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutOAuthState(ctx, "new", "a", "/", "r", driveTime.Add(10*time.Minute+time.Second)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM drive_oauth_states`); n != 2 {
		t.Fatalf("%d states left, want 2 (the expired ones deleted)", n)
	}
	if _, _, _, ok, _ := s.TakeOAuthState(ctx, "live", driveTime.Add(10*time.Minute+time.Second)); !ok {
		t.Fatal("a live state was deleted")
	}
}
