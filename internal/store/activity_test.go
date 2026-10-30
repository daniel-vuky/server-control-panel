package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func person(action string) Activity {
	return Activity{Actor: "dev@localhost", ActorKind: ActorPerson, Action: action, ProjectID: 7, Target: "Shop"}
}

func TestStartAndFinishActivity(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	id, err := s.StartActivity(ctx, person(ActProjectStop))
	if err != nil || id == 0 {
		t.Fatalf("StartActivity = %d, %v", id, err)
	}
	rows, err := s.RecentActivity(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("RecentActivity = %+v, %v", rows, err)
	}
	r := rows[0]
	if r.ID != id || r.Result != ResultRunning || r.FinishedAt != nil || r.StartedAt.IsZero() {
		t.Fatalf("running row = %+v", r)
	}
	if r.Actor != "dev@localhost" || r.ActorKind != ActorPerson || r.Action != ActProjectStop || r.ProjectID != 7 || r.Target != "Shop" {
		t.Fatalf("row fields = %+v", r)
	}

	if err := s.FinishActivity(ctx, id, ResultOK, "stopped"); err != nil {
		t.Fatal(err)
	}
	rows, err = s.RecentActivity(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("RecentActivity = %+v, %v", rows, err)
	}
	r = rows[0]
	if r.Result != ResultOK || r.Reason != "stopped" || r.FinishedAt == nil {
		t.Fatalf("finished row = %+v", r)
	}
}

func TestFinishActivityOnlyOnce(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	id, err := s.StartActivity(ctx, person(ActProjectStart))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishActivity(ctx, id, ResultOK, "first"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishActivity(ctx, id, ResultFailed, "second"); err != nil {
		t.Fatalf("second finish = %v, want nil", err)
	}
	if err := s.FinishActivity(ctx, 9999, ResultFailed, "nope"); err != nil {
		t.Fatalf("finish of an unknown id = %v, want nil", err)
	}
	rows, err := s.RecentActivity(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("RecentActivity = %+v, %v", rows, err)
	}
	if rows[0].Result != ResultOK || rows[0].Reason != "first" {
		t.Fatalf("row changed by second finish: %+v", rows[0])
	}
}

func TestRecordActivityRefusal(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a := Activity{Actor: "dev@localhost", ActorKind: ActorPerson, Action: ActScanStart, Target: "/srv/apps"}
	if err := s.RecordActivity(ctx, a, ResultRefused, "outside_roots"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.RecentActivity(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("RecentActivity = %+v, %v", rows, err)
	}
	r := rows[0]
	if r.Result != ResultRefused || r.Reason != "outside_roots" || r.FinishedAt == nil || r.ProjectID != 0 || r.Target != "/srv/apps" {
		t.Fatalf("refusal row = %+v", r)
	}
	if n := count(t, s.db, `SELECT count(*) FROM activity WHERE project_id IS NULL`); n != 1 {
		t.Fatalf("project_id stored non-NULL for 0: %d null rows", n)
	}
}

func TestRecentActivityNewestFirstAndLimit(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, act := range []string{ActProjectStart, ActProjectStop, ActProjectRestart} {
		if _, err := s.StartActivity(ctx, person(act)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.RecentActivity(ctx, 2)
	if err != nil || len(rows) != 2 {
		t.Fatalf("RecentActivity = %+v, %v", rows, err)
	}
	if rows[0].Action != ActProjectRestart || rows[1].Action != ActProjectStop {
		t.Fatalf("order = %s, %s", rows[0].Action, rows[1].Action)
	}
}

func TestActivityCleansTarget(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	dirty := strings.Repeat("é/a\x00b\x1bc\nd\te", 1000) // ~10 KB
	if len(dirty) < 10_000 {
		t.Fatalf("test input is %d bytes", len(dirty))
	}
	a := person(ActScanStart)
	a.Target = dirty
	id, err := s.StartActivity(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishActivity(ctx, id, ResultFailed, "bad\x00\x1b[31m reason\nline two\tend"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.RecentActivity(ctx, 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("RecentActivity = %+v, %v", rows, err)
	}
	got := rows[0].Target
	if len(got) > 256 || len(got) == 0 || !utf8.ValidString(got) {
		t.Fatalf("target is %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	for _, r := range got {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("control character %q left in target %q", r, got)
		}
	}
	reason := rows[0].Reason
	if strings.ContainsAny(reason, "\x00\x1b") || reason != "bad[31m reason\nline two\tend" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestCleanText(t *testing.T) {
	tests := []struct {
		name, in string
		max      int
		want     string
	}{
		{"plain", "abc", 10, "abc"},
		{"keeps newline and tab", "a\nb\tc", 10, "a\nb\tc"},
		{"drops controls", "a\x00b\x1bc\x7fd", 10, "abcd"},
		{"cuts at a rune boundary", "aéé", 4, "aé"},
		{"cut exact", "abcd", 4, "abcd"},
		{"invalid utf-8 dropped", "a\xffb", 10, "ab"},
	}
	for _, tt := range tests {
		if got := cleanText(tt.in, tt.max); got != tt.want {
			t.Errorf("%s: cleanText(%q, %d) = %q, want %q", tt.name, tt.in, tt.max, got, tt.want)
		}
	}
}

func TestActivityInterruptedOnReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	running, err := s.StartActivity(ctx, person(ActProjectRestart))
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.StartActivity(ctx, person(ActProjectStart))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishActivity(ctx, done, ResultOK, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rows, err := s.RecentActivity(ctx, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("RecentActivity = %+v, %v", rows, err)
	}
	for _, r := range rows {
		switch r.ID {
		case running:
			if r.Result != ResultInterrupted || r.FinishedAt == nil || r.Reason != "The panel restarted while this action was running." {
				t.Fatalf("running row after reopen = %+v", r)
			}
		case done:
			if r.Result != ResultOK {
				t.Fatalf("finished row changed: %+v", r)
			}
		}
	}
}
