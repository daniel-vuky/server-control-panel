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

func TestMigration0010KeepsActivity(t *testing.T) {
	ctx := context.Background()
	db, path := openAt(t, 9)
	for _, row := range []struct {
		id      int
		action  string
		project any
		result  string
	}{
		{5, "project.start", 7, "ok"},
		{6, "backup.run", nil, "failed"},
		{9, "scan.start", nil, "refused"},
	} {
		if _, err := db.Exec(`INSERT INTO activity (id, started_at, finished_at, actor, actor_kind, action, project_id, target, result, reason)
			VALUES (?, '2026-10-01T10:00:00Z', '2026-10-01T10:00:05Z', 'a@b.c', 'person', ?, ?, 'Shop', ?, 'why')`,
			row.id, row.action, row.project, row.result); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open with 0010 pending: %v", err)
	}
	defer func() { _ = s.Close() }()
	rows, err := s.RecentActivity(ctx, 10)
	if err != nil || len(rows) != 3 {
		t.Fatalf("RecentActivity = %+v, %v", rows, err)
	}
	for i, want := range []struct {
		id     int64
		action string
		result string
		proj   int64
	}{{9, "scan.start", "refused", 0}, {6, "backup.run", "failed", 0}, {5, "project.start", "ok", 7}} {
		r := rows[i]
		if r.ID != want.id || r.Action != want.action || r.Result != want.result || r.ProjectID != want.proj ||
			r.Actor != "a@b.c" || r.ActorKind != ActorPerson || r.Target != "Shop" || r.Reason != "why" ||
			r.FinishedAt == nil || r.StartedAt.IsZero() || r.Channels != nil {
			t.Fatalf("row %d = %+v, want %+v", i, r, want)
		}
	}

	sys := Activity{Actor: "system", ActorKind: ActorSystem, Action: ActAlertDomain, Target: "shop.example.com"}
	if err := s.RecordActivity(ctx, sys, ResultSent, "ok"); err != nil {
		t.Fatalf("system sent row: %v", err)
	}
	robot := sys
	robot.ActorKind = "robot"
	if err := s.RecordActivity(ctx, robot, ResultSent, ""); err == nil {
		t.Fatal("actor_kind robot was accepted")
	}
	if n := count(t, s.db, `SELECT count(*) FROM pragma_index_list('activity') WHERE name IN ('activity_started_at', 'activity_project')`); n != 2 {
		t.Fatalf("activity indexes = %d, want 2", n)
	}
}

// seedActivity stores finished rows and returns their ids in order.
func seedActivity(t *testing.T, s *Store, rows []Activity) []int64 {
	t.Helper()
	ctx := context.Background()
	ids := make([]int64, len(rows))
	for i, a := range rows {
		if a.Actor == "" {
			a.Actor, a.ActorKind = "dev@localhost", ActorPerson
		}
		if err := s.RecordActivity(ctx, a, ResultOK, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT max(id) FROM activity`).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func actionsOf(es []ActivityEntry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Action)
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListActivityFilters(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	ids := seedActivity(t, s, []Activity{
		{Action: ActProjectStart, ProjectID: 1, Target: "One"},
		{Action: ActBackupRun, ProjectID: 2, Target: "Two"},
		{Action: ActBackupAll, Target: "All"},
		{Action: ActAlertDomain, ProjectID: 1, Target: "One", Actor: "system", ActorKind: ActorSystem},
		{Action: ActScanStart, Target: "/srv"},
		{Action: ActAlertsChannel, Target: "Telegram"},
		{Action: ActAlertsThresholds, Target: "Thresholds"},
	})
	for _, c := range []struct {
		name string
		f    ActivityFilter
		want []string
	}{
		{"all", ActivityFilter{Limit: 50}, []string{ActAlertsThresholds, ActAlertsChannel, ActScanStart, ActAlertDomain, ActBackupAll, ActBackupRun, ActProjectStart}},
		// alerts.channel and alerts.thresholds are actions, not alerts.
		{"action", ActivityFilter{Kind: "action", Limit: 50}, []string{ActAlertsThresholds, ActAlertsChannel, ActScanStart, ActProjectStart}},
		{"backup", ActivityFilter{Kind: "backup", Limit: 50}, []string{ActBackupAll, ActBackupRun}},
		{"alert", ActivityFilter{Kind: "alert", Limit: 50}, []string{ActAlertDomain}},
		{"project", ActivityFilter{ProjectID: 1, Limit: 50}, []string{ActAlertDomain, ActProjectStart}},
		{"project and kind", ActivityFilter{ProjectID: 1, Kind: "alert", Limit: 50}, []string{ActAlertDomain}},
	} {
		got, more, err := s.ListActivity(ctx, c.f)
		if err != nil || more || !sameStrings(actionsOf(got), c.want) {
			t.Errorf("%s: got %v more=%v err=%v, want %v", c.name, actionsOf(got), more, err, c.want)
		}
	}

	page, more, err := s.ListActivity(ctx, ActivityFilter{Limit: 2})
	if err != nil || !more || !sameStrings(actionsOf(page), []string{ActAlertsThresholds, ActAlertsChannel}) {
		t.Fatalf("page 1 = %v more=%v err=%v", actionsOf(page), more, err)
	}
	if page[0].ID != ids[6] || page[1].ID != ids[5] {
		t.Fatalf("page 1 ids = %d, %d", page[0].ID, page[1].ID)
	}
	next, more, err := s.ListActivity(ctx, ActivityFilter{Limit: 2, Before: page[1].ID})
	if err != nil || !more || !sameStrings(actionsOf(next), []string{ActScanStart, ActAlertDomain}) {
		t.Fatalf("page 2 = %v more=%v err=%v", actionsOf(next), more, err)
	}
	last, more, err := s.ListActivity(ctx, ActivityFilter{Limit: 3, Before: next[1].ID})
	if err != nil || more || !sameStrings(actionsOf(last), []string{ActBackupAll, ActBackupRun, ActProjectStart}) {
		t.Fatalf("page 3 = %v more=%v err=%v", actionsOf(last), more, err)
	}
}

// The page size is clamped to 1 to 100, whatever the caller passes.
func TestListActivityClampsLimit(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	rows := make([]Activity, 102)
	for i := range rows {
		rows[i] = Activity{Action: ActScanStart, Target: "/srv"}
	}
	seedActivity(t, s, rows)
	for _, c := range []struct {
		limit, want int
	}{{0, 1}, {-5, 1}, {1000, 100}, {100, 100}} {
		got, more, err := s.ListActivity(ctx, ActivityFilter{Limit: c.limit})
		if err != nil || len(got) != c.want || !more {
			t.Errorf("Limit %d: %d rows more=%v err=%v, want %d and more", c.limit, len(got), more, err, c.want)
		}
	}
}

func TestListActivityProjectTracked(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, blogID := seedApps(t, s)
	// seedApps tracks shop and stores blog as ignored.
	seedActivity(t, s, []Activity{
		{Action: ActProjectStart, ProjectID: shopID, Target: "Shop"},
		{Action: ActProjectStop, ProjectID: blogID, Target: "Blog"},
		{Action: ActProjectRestart, ProjectID: 4242, Target: "Gone"},
		{Action: ActScanStart, Target: "/srv"},
	})
	tracked := func() map[string]bool {
		es, _, err := s.ListActivity(ctx, ActivityFilter{Limit: 50})
		if err != nil || len(es) != 4 {
			t.Fatalf("ListActivity = %+v, %v", es, err)
		}
		m := map[string]bool{}
		for _, e := range es {
			m[e.Target] = e.ProjectTracked
		}
		return m
	}
	m := tracked()
	if !m["Shop"] || m["Blog"] || m["Gone"] || m["/srv"] {
		t.Fatalf("ProjectTracked = %v", m)
	}
	if _, err := s.RemoveWorkspace(ctx, wsID); err != nil {
		t.Fatal(err)
	}
	if m := tracked(); m["Shop"] || m["Blog"] || m["Gone"] || m["/srv"] {
		t.Fatalf("ProjectTracked after removal = %v", m)
	}
}

func TestFinishActivityWithChannels(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	id, err := s.StartActivity(ctx, Activity{Actor: "system", ActorKind: ActorSystem, Action: ActAlertTunnel, Target: "Tunnel"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishActivityWith(ctx, id, ResultSent, "Tunnel disconnected", []string{"telegram", "email"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.RecentActivity(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("RecentActivity = %+v, %v", rows, err)
	}
	if r := rows[0]; r.Result != ResultSent || r.Reason != "Tunnel disconnected" || r.FinishedAt == nil || !sameStrings(r.Channels, []string{"telegram", "email"}) {
		t.Fatalf("finished row = %+v", r)
	}
	if err := s.FinishActivityWith(ctx, id, ResultNotSent, "later", []string{"email"}); err != nil {
		t.Fatalf("second finish = %v, want nil", err)
	}
	rows, _ = s.RecentActivity(ctx, 10)
	if r := rows[0]; r.Result != ResultSent || r.Reason != "Tunnel disconnected" || !sameStrings(r.Channels, []string{"telegram", "email"}) {
		t.Fatalf("row changed by the second finish: %+v", r)
	}
	// The plain finish leaves channels empty.
	plain, _ := s.StartActivity(ctx, person(ActProjectStop))
	if err := s.FinishActivity(ctx, plain, ResultOK, ""); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.RecentActivity(ctx, 10)
	if rows[0].ID != plain || rows[0].Channels != nil {
		t.Fatalf("plain finish row = %+v", rows[0])
	}
}
