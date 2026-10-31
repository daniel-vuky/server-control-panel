package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/config"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(loc *time.Location, y int, mo time.Month, d, hh, mm, ss int) time.Time {
	return time.Date(y, mo, d, hh, mm, ss, 0, loc)
}

func daily(hhmm string) store.ScheduleFields {
	return store.ScheduleFields{Frequency: store.FrequencyDaily, Time: hhmm, Keep: 14, Compress: true}
}

func weekly(hhmm string, wd time.Weekday) store.ScheduleFields {
	return store.ScheduleFields{Frequency: store.FrequencyWeekly, Time: hhmm, Weekday: int(wd), Keep: 14, Compress: true}
}

func TestLastOccurrenceAndNextRun(t *testing.T) {
	hcm := mustZone(t, "Asia/Ho_Chi_Minh")
	// 2026-11-04 is a Wednesday.
	now := at(hcm, 2026, 11, 4, 10, 0, 0)
	cases := []struct {
		name       string
		f          store.ScheduleFields
		last, next time.Time
	}{
		{"daily, earlier today", daily("02:00"), at(hcm, 2026, 11, 4, 2, 0, 0), at(hcm, 2026, 11, 5, 2, 0, 0)},
		{"daily, later today", daily("23:59"), at(hcm, 2026, 11, 3, 23, 59, 0), at(hcm, 2026, 11, 4, 23, 59, 0)},
		{"daily, midnight", daily("00:00"), at(hcm, 2026, 11, 4, 0, 0, 0), at(hcm, 2026, 11, 5, 0, 0, 0)},
		{"daily, exactly now", daily("10:00"), at(hcm, 2026, 11, 4, 10, 0, 0), at(hcm, 2026, 11, 5, 10, 0, 0)},
	}
	for wd := time.Sunday; wd <= time.Saturday; wd++ {
		// The last one on or before Wednesday 10:00, the next one strictly after.
		back := (int(time.Wednesday) - int(wd) + 7) % 7
		fwd := (int(wd) - int(time.Wednesday) + 7) % 7
		last := at(hcm, 2026, 11, 4-back, 2, 0, 0)
		next := at(hcm, 2026, 11, 4+fwd, 2, 0, 0)
		if fwd == 0 {
			next = at(hcm, 2026, 11, 11, 2, 0, 0)
		}
		cases = append(cases, struct {
			name       string
			f          store.ScheduleFields
			last, next time.Time
		}{"weekly " + wd.String(), weekly("02:00", wd), last, next})
	}
	cases = append(cases, struct {
		name       string
		f          store.ScheduleFields
		last, next time.Time
	}{"weekly Wednesday, later today", weekly("23:59", time.Wednesday), at(hcm, 2026, 10, 28, 23, 59, 0), at(hcm, 2026, 11, 4, 23, 59, 0)})

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The instant is what matters, whatever zone now is given in.
			last, ok := LastOccurrence(c.f, now.UTC(), hcm)
			if !ok || !last.Equal(c.last) {
				t.Errorf("LastOccurrence = %v, %v; want %v", last, ok, c.last)
			}
			if last.Location() != hcm {
				t.Errorf("LastOccurrence zone = %v, want %v", last.Location(), hcm)
			}
			next, ok := NextRun(c.f, now.UTC(), hcm)
			if !ok || !next.Equal(c.next) {
				t.Errorf("NextRun = %v, %v; want %v", next, ok, c.next)
			}
		})
	}

	// The same wall time is a different instant in another zone.
	utcLast, _ := LastOccurrence(daily("02:00"), now, time.UTC)
	if want := time.Date(2026, 11, 4, 2, 0, 0, 0, time.UTC); !utcLast.Equal(want) {
		t.Errorf("UTC LastOccurrence = %v, want %v", utcLast, want)
	}

	for _, f := range []store.ScheduleFields{
		{Frequency: store.FrequencyOff, Time: "02:00", Keep: 14},
		{Frequency: "hourly", Time: "02:00", Keep: 14},
		{Frequency: store.FrequencyDaily, Time: "2:00", Keep: 14},
		{Frequency: store.FrequencyWeekly, Time: "02:00", Weekday: 7, Keep: 14},
	} {
		if got, ok := LastOccurrence(f, now, hcm); ok {
			t.Errorf("LastOccurrence(%+v) = %v, want none", f, got)
		}
		if got, ok := NextRun(f, now, hcm); ok {
			t.Errorf("NextRun(%+v) = %v, want none", f, got)
		}
	}
}

// simulateRuns ticks every 30 s from from to to and counts the runs the due
// rule allows, keeping last_run_at as the scheduler does.
func simulateRuns(f store.ScheduleFields, saved, from, to time.Time, loc *time.Location) []time.Time {
	sc := store.Schedule{ScheduleFields: f, UpdatedAt: saved}
	var runs []time.Time
	for now := from; !now.After(to); now = now.Add(tickEvery) {
		if due, ok := dueAt(sc, now, loc); ok {
			d := due
			sc.LastRunAt = &d
			runs = append(runs, due)
		}
	}
	return runs
}

func TestOccurrenceDST(t *testing.T) {
	ny := mustZone(t, "America/New_York")

	// 2026-03-08: 02:00 EST jumps to 03:00 EDT, so 02:30 does not exist and
	// normalises forward to 03:30 EDT (07:30 UTC).
	gap := daily("02:30")
	want := time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC)
	if got, ok := NextRun(gap, at(ny, 2026, 3, 8, 0, 0, 0), ny); !ok || !got.Equal(want) {
		t.Errorf("gap NextRun = %v, want %v", got, want)
	}
	if got, ok := LastOccurrence(gap, at(ny, 2026, 3, 8, 5, 0, 0), ny); !ok || !got.Equal(want) {
		t.Errorf("gap LastOccurrence = %v, want %v", got, want)
	}
	runs := simulateRuns(gap, at(ny, 2026, 3, 1, 12, 0, 0), at(ny, 2026, 3, 7, 23, 0, 0), at(ny, 2026, 3, 9, 1, 0, 0), ny)
	if len(runs) != 1 || !runs[0].Equal(want) {
		t.Errorf("gap runs = %v, want one at %v", runs, want)
	}

	// 2026-11-01: 02:00 EDT falls back to 01:00 EST, so 01:30 happens twice.
	// Whatever offset time.Date picks, the day has exactly one run.
	overlap := daily("01:30")
	from := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC) // 23:00 EDT on Oct 31
	to := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)   // 04:00 EST
	runs = simulateRuns(overlap, at(ny, 2026, 10, 20, 12, 0, 0), from, to, ny)
	if len(runs) != 1 {
		t.Fatalf("overlap runs = %v, want exactly one", runs)
	}
	if h, m, _ := runs[0].In(ny).Clock(); h != 1 || m != 30 {
		t.Errorf("overlap run at %v, want 01:30 local", runs[0].In(ny))
	}
	// Every tick in the second 01:30 hour sees the same due instant and
	// does not run it again.
	next, ok := NextRun(overlap, runs[0], ny)
	if !ok || next.Sub(runs[0]) < 23*time.Hour {
		t.Errorf("overlap NextRun after the run = %v, want the next day", next)
	}
}

func TestValidSchedule(t *testing.T) {
	ok := []store.ScheduleFields{
		daily("00:00"), daily("23:59"), daily("02:00"), weekly("12:30", time.Saturday), weekly("12:30", time.Sunday),
		{Frequency: store.FrequencyOff, Time: "02:00", Weekday: 0, Keep: 14, Compress: true},
		{Frequency: store.FrequencyDaily, Time: "02:00", Keep: 1},
		{Frequency: store.FrequencyDaily, Time: "02:00", Keep: 365},
	}
	for _, f := range ok {
		if field, code := ValidSchedule(f); field != "" || code != "" {
			t.Errorf("ValidSchedule(%+v) = %q %q, want valid", f, field, code)
		}
	}
	bad := []struct {
		f     store.ScheduleFields
		field string
	}{
		{store.ScheduleFields{Frequency: "hourly", Time: "02:00", Keep: 14}, "frequency"},
		{store.ScheduleFields{Frequency: "", Time: "02:00", Keep: 14}, "frequency"},
		{store.ScheduleFields{Frequency: "Daily", Time: "02:00", Keep: 14}, "frequency"},
		{daily("24:00"), "time"},
		{daily("7:5"), "time"},
		{daily("07:60"), "time"},
		{daily(""), "time"},
		{daily("02:00 "), "time"},
		{daily("０２:00"), "time"},
		{store.ScheduleFields{Frequency: store.FrequencyWeekly, Time: "02:00", Weekday: 7, Keep: 14}, "weekday"},
		{store.ScheduleFields{Frequency: store.FrequencyDaily, Time: "02:00", Weekday: -1, Keep: 14}, "weekday"},
		{store.ScheduleFields{Frequency: store.FrequencyDaily, Time: "02:00", Keep: 0}, "keep"},
		{store.ScheduleFields{Frequency: store.FrequencyDaily, Time: "02:00", Keep: 366}, "keep"},
	}
	for _, c := range bad {
		if field, code := ValidSchedule(c.f); field != c.field || code != "invalid" {
			t.Errorf("ValidSchedule(%+v) = %q %q, want %q invalid", c.f, field, code, c.field)
		}
	}
}

// fakeClock is the scheduler's clock in the tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

type schedEnv struct {
	*env
	s     *Scheduler
	clock *fakeClock
	loc   *time.Location
	logs  *lockedBuffer
}

// newSchedEnv is the backup env with every schedule of "shop" off and a
// scheduler in Asia/Ho_Chi_Minh on a fake clock. m replaces the env's manager
// when set.
func newSchedEnv(t *testing.T, opts ...envOpt) *schedEnv {
	t.Helper()
	e := newEnv(t, opts...)
	ctx := context.Background()
	for _, d := range e.shop.Databases {
		off := store.ScheduleFields{Frequency: store.FrequencyOff, Time: "02:00", Keep: 14, Compress: true}
		if _, _, err := e.st.SetSchedule(ctx, e.shop.ID, project.DatabaseKey(d), off, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	se := &schedEnv{env: e, clock: &fakeClock{}, loc: mustZone(t, "Asia/Ho_Chi_Minh"), logs: &lockedBuffer{}}
	log := slog.New(slog.NewTextHandler(se.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	se.s = NewScheduler(e.st, e.m, se.loc, se.clock.now, log)
	return se
}

// set stores key's schedule as saved at saved.
func (se *schedEnv) set(t *testing.T, key string, f store.ScheduleFields, saved time.Time) {
	t.Helper()
	if _, _, err := se.st.SetSchedule(context.Background(), se.shop.ID, key, f, saved); err != nil {
		t.Fatal(err)
	}
}

// tickAt runs one tick at now.
func (se *schedEnv) tickAt(now time.Time) {
	se.clock.set(now)
	se.s.tick(context.Background())
}

// scheduled lists the rows the schedule started, oldest first.
func (se *schedEnv) scheduled(t *testing.T) []store.Backup {
	t.Helper()
	rows, _, err := se.st.ProjectBackups(context.Background(), se.shop.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Backup
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].StartedBy == ScheduleActor.Name {
			out = append(out, rows[i])
		}
	}
	return out
}

func (se *schedEnv) allRows(t *testing.T) int {
	t.Helper()
	rows, _, err := se.st.ProjectBackups(context.Background(), se.shop.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func (se *schedEnv) lastRun(t *testing.T, key string) *time.Time {
	t.Helper()
	tr, err := se.st.TrackedProject(context.Background(), se.shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tr.Schedules[key].LastRunAt
}

func TestSchedulerRunsDueDaily(t *testing.T) {
	se := newSchedEnv(t)
	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))

	se.tickAt(at(se.loc, 2026, 11, 4, 1, 59, 50))
	if n := len(se.scheduled(t)); n != 0 {
		t.Fatalf("before the due time: %d rows", n)
	}

	se.tickAt(at(se.loc, 2026, 11, 4, 2, 0, 10))
	rows := se.scheduled(t)
	if len(rows) != 1 || rows[0].DBKey != keyPG {
		t.Fatalf("rows = %+v, want one scheduled backup of %s", rows, keyPG)
	}
	b := se.waitDone(t, rows[0].ID)
	if b.State != StateOK || b.StartedBy != "schedule" {
		t.Errorf("row = %s by %q (%s), want ok by schedule", b.State, b.StartedBy, b.Error)
	}
	due := at(se.loc, 2026, 11, 4, 2, 0, 0)
	if lr := se.lastRun(t, keyPG); lr == nil || !lr.Equal(due) {
		t.Errorf("last_run_at = %v, want %v", lr, due)
	}
	var found bool
	for _, a := range se.activity(t) {
		if a.Actor == "schedule" && a.ActorKind == store.ActorSchedule && a.Result == "ok" {
			found = true
		}
	}
	if !found {
		t.Errorf("no backup.run activity by schedule: %+v", se.activity(t))
	}

	// Later ticks the same day do nothing; the next day runs again.
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 0, 40))
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 45, 0))
	if n := len(se.scheduled(t)); n != 1 {
		t.Fatalf("after later ticks: %d rows, want 1", n)
	}
	se.tickAt(at(se.loc, 2026, 11, 5, 2, 0, 5))
	if rows := se.scheduled(t); len(rows) != 2 {
		t.Fatalf("next day: %d rows, want 2", len(rows))
	} else {
		se.waitDone(t, rows[1].ID)
	}
}

func TestSchedulerRunsDueWeekly(t *testing.T) {
	se := newSchedEnv(t)
	se.set(t, keyMySQL, weekly("03:15", time.Thursday), at(se.loc, 2026, 11, 1, 10, 0, 0))

	// Wednesday 2026-11-04 and Friday 2026-11-06: not the day.
	se.tickAt(at(se.loc, 2026, 11, 4, 3, 15, 0))
	if n := len(se.scheduled(t)); n != 0 {
		t.Fatalf("Wednesday: %d rows", n)
	}
	se.tickAt(at(se.loc, 2026, 11, 5, 3, 15, 20))
	rows := se.scheduled(t)
	if len(rows) != 1 || rows[0].DBKey != keyMySQL {
		t.Fatalf("Thursday rows = %+v", rows)
	}
	se.waitDone(t, rows[0].ID)
	se.tickAt(at(se.loc, 2026, 11, 6, 3, 15, 20))
	if n := len(se.scheduled(t)); n != 1 {
		t.Fatalf("Friday: %d rows, want 1", n)
	}
	se.tickAt(at(se.loc, 2026, 11, 12, 3, 16, 0))
	if n := len(se.scheduled(t)); n != 2 {
		t.Fatalf("the next Thursday: %d rows, want 2", n)
	}
}

// Review focus 1: saving a 02:00 schedule at 02:05 runs nothing now; the next
// run is tomorrow 02:00.
func TestSchedulerSavingAfterTimeDoesNotFire(t *testing.T) {
	se := newSchedEnv(t)
	saved := at(se.loc, 2026, 11, 4, 2, 5, 0)
	se.set(t, keyPG, daily("02:00"), saved)

	se.tickAt(saved.Add(10 * time.Second))
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 59, 0))
	if n := len(se.scheduled(t)); n != 0 {
		t.Fatalf("%d rows after saving past the time, want 0", n)
	}
	want := at(se.loc, 2026, 11, 5, 2, 0, 0)
	if next, ok := NextRun(daily("02:00"), saved, se.loc); !ok || !next.Equal(want) {
		t.Errorf("NextRun = %v, want %v", next, want)
	}
	se.tickAt(want.Add(5 * time.Second))
	if rows := se.scheduled(t); len(rows) != 1 {
		t.Fatalf("tomorrow: %d rows, want 1", len(rows))
	} else {
		se.waitDone(t, rows[0].ID)
	}
}

// Review focus 2: an update restarts the panel around a 02:00 schedule; the
// backup runs once after the restart, never twice.
func TestSchedulerRestartWithinHourRunsOnce(t *testing.T) {
	se := newSchedEnv(t)
	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))

	// The panel was down at 02:00 and is back at 02:31: the run happens now.
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 31, 0))
	rows := se.scheduled(t)
	if len(rows) != 1 {
		t.Fatalf("after the restart: %d rows, want 1", len(rows))
	}
	se.waitDone(t, rows[0].ID)

	// Restarted again (a second scheduler, a new manager over the same store),
	// with two ticks close together: nothing more.
	m2 := New(se.st, se.r, se.drv, se.hub.containers, se.dataDir, time.Now, discardLog())
	m2.freeSpace = se.m.freeSpace
	m2.Start(context.Background())
	t.Cleanup(func() {
		m2.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m2.Wait(ctx)
	})
	s2 := NewScheduler(se.st, m2, se.loc, se.clock.now, discardLog())
	se.clock.set(at(se.loc, 2026, 11, 4, 2, 32, 0))
	s2.tick(context.Background())
	s2.tick(context.Background())
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 32, 30))
	if n := len(se.scheduled(t)); n != 1 {
		t.Fatalf("after a second restart: %d rows, want 1", n)
	}
}

func TestSchedulerSkipsOlderThanHour(t *testing.T) {
	se := newSchedEnv(t)
	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))
	se.set(t, keyMySQL, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))

	// Down for more than an hour: skipped, no catch-up.
	se.tickAt(at(se.loc, 2026, 11, 4, 3, 0, 1))
	if n := len(se.scheduled(t)); n != 0 {
		t.Fatalf("an hour and a second late: %d rows", n)
	}
	if lr := se.lastRun(t, keyPG); lr != nil {
		t.Errorf("last_run_at = %v for a skipped run, want nil", lr)
	}
	// Exactly 60 minutes late still runs.
	se.set(t, keyPG, daily("02:01"), at(se.loc, 2026, 11, 3, 11, 0, 0))
	se.tickAt(at(se.loc, 2026, 11, 4, 3, 1, 0))
	rows := se.scheduled(t)
	if len(rows) != 1 || rows[0].DBKey != keyPG {
		t.Fatalf("exactly an hour late: rows = %+v", rows)
	}
	se.waitDone(t, rows[0].ID)
	// The next occurrence runs as usual.
	se.tickAt(at(se.loc, 2026, 11, 5, 2, 0, 0))
	if n := len(se.scheduled(t)); n != 2 {
		t.Fatalf("next day: %d rows, want 2", n)
	}
}

// Review focus 2: last_run_at is stored before Enqueue, so an Enqueue that
// fails (or a crash right after) never runs the same due time again.
func TestSchedulerMarksBeforeEnqueue(t *testing.T) {
	se := newSchedEnv(t)
	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))
	due := at(se.loc, 2026, 11, 4, 2, 0, 0)

	var calls int
	se.s.enqueue = func(_ context.Context, _ store.Tracked, key string, by Actor) (int64, error) {
		calls++
		if lr := se.lastRun(t, key); lr == nil || !lr.Equal(due) {
			t.Errorf("at Enqueue last_run_at = %v, want %v already stored", lr, due)
		}
		if by != ScheduleActor {
			t.Errorf("actor = %+v", by)
		}
		return 0, errors.New("the panel stopped")
	}
	se.tickAt(due.Add(10 * time.Second))
	se.tickAt(due.Add(40 * time.Second))
	se.tickAt(due.Add(30 * time.Minute))
	if calls != 1 {
		t.Fatalf("Enqueue called %d times, want 1 (not retried)", calls)
	}
	if n := se.allRows(t); n != 0 {
		t.Errorf("%d rows", n)
	}

	// A closed manager is the same: marked, warned, not retried.
	se.set(t, keyMySQL, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))
	se.s.enqueue = nil
	se.m.Close()
	se.tickAt(due.Add(24*time.Hour + time.Second))
	if lr := se.lastRun(t, keyMySQL); lr == nil || !lr.Equal(due.Add(24*time.Hour)) {
		t.Errorf("mysql last_run_at = %v", lr)
	}
	if !strings.Contains(se.logs.String(), "level=WARN") || !strings.Contains(se.logs.String(), ErrClosed.Error()) {
		t.Errorf("logs = %s, want a warning with ErrClosed", se.logs.String())
	}
}

func TestSchedulerAlreadyRunningSkippedNoRow(t *testing.T) {
	se := newSchedEnv(t)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	se.r.mu.Lock()
	se.r.dump = func(ctx context.Context, c call, w io.Writer) ([]byte, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		_, err := w.Write(defaultDump(c))
		return nil, err
	}
	se.r.mu.Unlock()
	manual := se.enqueue(t, keyPG)
	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))

	se.tickAt(at(se.loc, 2026, 11, 4, 2, 0, 5))
	if n := se.allRows(t); n != 1 {
		t.Fatalf("%d rows, want only the manual one", n)
	}
	logs := se.logs.String()
	if !strings.Contains(logs, "level=INFO") || !strings.Contains(logs, CodeAlreadyRunning) || strings.Contains(logs, "level=WARN") {
		t.Errorf("logs = %s, want one Info about already_running", logs)
	}
	close(release)
	if b := se.waitDone(t, manual); b.State != StateOK {
		t.Errorf("manual backup = %s", b.State)
	}
	// It counted as this occurrence's run: nothing later the same day.
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 10, 0))
	if n := se.allRows(t); n != 1 {
		t.Fatalf("%d rows after the manual one ended, want 1", n)
	}
}

func TestSchedulerDriveNotConfiguredNoRow(t *testing.T) {
	se := newSchedEnv(t)
	drv, err := drive.NewService(config.Config{SecretKey: bytes.Repeat([]byte{7}, 32)}, se.st,
		drive.NewFake(t.TempDir(), "http://localhost:5173"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if drv.Status().State != drive.StateNotConfigured {
		t.Fatalf("state = %s", drv.Status().State)
	}
	m := New(se.st, se.r, drv, se.hub.containers, se.dataDir, time.Now, discardLog())
	m.freeSpace = se.m.freeSpace
	t.Cleanup(m.Close)
	se.s = NewScheduler(se.st, m, se.loc, se.clock.now, slog.New(slog.NewTextHandler(se.logs, nil)))
	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))
	se.set(t, keyMySQL, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))

	se.tickAt(at(se.loc, 2026, 11, 4, 1, 0, 0)) // nothing due: no log
	if logs := se.logs.String(); logs != "" {
		t.Errorf("a tick with nothing due logged %s", logs)
	}
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 0, 5))
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 0, 35))
	if n := se.allRows(t); n != 0 {
		t.Fatalf("%d rows while Drive is not set up", n)
	}
	// The due time is considered once: marked, one Info line, and the next
	// tick has nothing due.
	if lines := strings.Count(se.logs.String(), "level=INFO"); lines != 1 {
		t.Errorf("logs = %s, want one Info line for the due time", se.logs.String())
	}
	due := at(se.loc, 2026, 11, 4, 2, 0, 0)
	for _, k := range []string{keyPG, keyMySQL} {
		if lr := se.lastRun(t, k); lr == nil || !lr.Equal(due) {
			t.Errorf("%s last_run_at = %v, want %v", k, lr, due)
		}
	}
	if se.r.callsOf(func(runner.Action) bool { return true }) != nil {
		t.Errorf("runner called while Drive is not set up")
	}
}

func TestSchedulerDriveDisconnectedRefusedRowBySchedule(t *testing.T) {
	se := newSchedEnv(t, envOpt{noConnect: true})
	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))

	se.tickAt(at(se.loc, 2026, 11, 4, 2, 0, 5))
	rows := se.scheduled(t)
	if len(rows) != 1 {
		t.Fatalf("%d rows, want one refused row", len(rows))
	}
	b := rows[0]
	if b.State != StateRefused || b.StartedBy != "schedule" || b.Error != wordsNotConnected {
		t.Errorf("row = %s by %q: %q", b.State, b.StartedBy, b.Error)
	}
	acts := se.activity(t)
	if len(acts) != 1 || acts[0].Actor != "schedule" || acts[0].ActorKind != store.ActorSchedule || acts[0].Result != "refused" {
		t.Errorf("activity = %+v", acts)
	}
	if strings.Contains(se.logs.String(), "level=WARN") {
		t.Errorf("a refusal logged a warning: %s", se.logs.String())
	}
	// Not retried within the hour.
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 0, 35))
	if n := len(se.scheduled(t)); n != 1 {
		t.Errorf("%d rows after the next tick", n)
	}
}

func TestSchedulerNotSupportedSkippedNoRow(t *testing.T) {
	se := newSchedEnv(t)
	saved := at(se.loc, 2026, 11, 3, 10, 0, 0)
	for _, k := range []string{keySQLite, keyHost, keyGhost} {
		se.set(t, k, daily("02:00"), saved)
	}
	tr, err := se.st.TrackedProject(context.Background(), se.shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range tr.Databases {
		k := project.DatabaseKey(d)
		want := k != keySQLite && k != keyHost && k != keyGhost
		if got := se.m.supported(tr, d); got != want {
			t.Errorf("supported(%s) = %v, want %v", k, got, want)
		}
	}

	se.tickAt(at(se.loc, 2026, 11, 4, 2, 0, 5))
	if n := se.allRows(t); n != 0 {
		t.Fatalf("%d rows for unsupported databases", n)
	}
	for _, k := range []string{keySQLite, keyHost, keyGhost} {
		if lr := se.lastRun(t, k); lr == nil || !lr.Equal(at(se.loc, 2026, 11, 4, 2, 0, 0)) {
			t.Errorf("%s last_run_at = %v, want the due time", k, lr)
		}
	}
	if calls := se.r.callsOf(func(runner.Action) bool { return true }); calls != nil {
		t.Errorf("runner calls = %+v", calls)
	}
	if acts := se.activity(t); len(acts) != 0 {
		t.Errorf("activity = %+v", acts)
	}
	if strings.Contains(se.logs.String(), "level=WARN") || strings.Contains(se.logs.String(), "level=INFO") {
		t.Errorf("logs = %s, want silence", se.logs.String())
	}

	// Docker down: a service resolved from the stored services is still
	// supported, so the run goes on and is refused with a row.
	se.hub.mu.Lock()
	se.hub.up, se.hub.cs = false, nil
	se.hub.mu.Unlock()
	se.set(t, keyPG, daily("02:00"), saved)
	se.tickAt(at(se.loc, 2026, 11, 5, 2, 0, 5))
	rows := se.scheduled(t)
	if len(rows) != 1 || rows[0].DBKey != keyPG || rows[0].State != StateRefused {
		t.Fatalf("Docker down rows = %+v", rows)
	}
}

func TestSchedulerIgnoresOffAndUntracked(t *testing.T) {
	se := newSchedEnv(t)
	se.set(t, keyMySQL, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))
	se.set(t, keyMySQL, store.ScheduleFields{Frequency: store.FrequencyOff, Time: "02:00", Keep: 14, Compress: true},
		at(se.loc, 2026, 11, 3, 11, 0, 0))
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 0, 5))
	if n := se.allRows(t); n != 0 {
		t.Fatalf("off: %d rows", n)
	}

	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))
	if _, err := se.st.UntrackProject(context.Background(), se.shop.ID); err != nil {
		t.Fatal(err)
	}
	se.tickAt(at(se.loc, 2026, 11, 5, 2, 0, 5))
	if n := se.allRows(t); n != 0 {
		t.Fatalf("untracked: %d rows", n)
	}
	if strings.Contains(se.logs.String(), "level=WARN") {
		t.Errorf("logs = %s", se.logs.String())
	}
}

func TestSchedulerEnqueueErrorLogged(t *testing.T) {
	se := newSchedEnv(t)
	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))
	se.set(t, keyMySQL, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))

	var deadlines []time.Duration
	se.s.enqueue = func(ctx context.Context, _ store.Tracked, key string, _ Actor) (int64, error) {
		d, ok := ctx.Deadline()
		if !ok {
			t.Errorf("Enqueue ctx has no deadline")
		}
		deadlines = append(deadlines, time.Until(d))
		if key == keyPG {
			return 0, context.DeadlineExceeded
		}
		return 0, fmt.Errorf("store: insert backup: %w", errors.New("disk I/O error"))
	}
	se.tickAt(at(se.loc, 2026, 11, 4, 2, 0, 5))
	if len(deadlines) != 2 {
		t.Fatalf("Enqueue called %d times, want 2", len(deadlines))
	}
	for _, d := range deadlines {
		if d <= 0 || d > enqueueWait {
			t.Errorf("deadline in %v, want within %v", d, enqueueWait)
		}
	}
	logs := se.logs.String()
	if strings.Count(logs, "level=WARN") != 2 || !strings.Contains(logs, "context deadline exceeded") ||
		!strings.Contains(logs, "disk I/O error") || !strings.Contains(logs, keyPG) || !strings.Contains(logs, keyMySQL) {
		t.Errorf("logs = %s", logs)
	}
}

func TestSchedulerStartTicksAtOnceAndClose(t *testing.T) {
	se := newSchedEnv(t)
	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))
	se.clock.set(at(se.loc, 2026, 11, 4, 2, 0, 5))

	se.s.Start(context.Background())
	waitFor(t, "the first tick", func() bool { return len(se.scheduled(t)) == 1 })
	done := make(chan struct{})
	go func() {
		se.s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	se.s.Close() // twice is fine
	se.s.Start(context.Background())
	se.waitDone(t, se.scheduled(t)[0].ID)

	// Close before Start never ticks.
	s2 := NewScheduler(se.st, se.m, se.loc, se.clock.now, discardLog())
	s2.Close()
	s2.Start(context.Background())
}

// A scheduled Enqueue cut by the scheduler's own shutdown is an Info line,
// not a warning; the run stays marked.
func TestSchedulerEnqueueCutByCloseLogsInfo(t *testing.T) {
	se := newSchedEnv(t)
	se.set(t, keyPG, daily("02:00"), at(se.loc, 2026, 11, 3, 10, 0, 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	se.s.enqueue = func(ectx context.Context, _ store.Tracked, _ string, _ Actor) (int64, error) {
		cancel() // Close while Enqueue runs
		<-ectx.Done()
		return 0, ectx.Err()
	}
	se.clock.set(at(se.loc, 2026, 11, 4, 2, 0, 5))
	se.s.tick(ctx)
	logs := se.logs.String()
	if strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "level=INFO") || !strings.Contains(logs, "context canceled") {
		t.Errorf("logs = %s, want an Info line and no warning", logs)
	}
	if lr := se.lastRun(t, keyPG); lr == nil {
		t.Error("last_run_at not stored")
	}
}
