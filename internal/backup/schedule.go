package backup

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

const (
	// tickEvery is the pause between the end of one scheduler tick and the
	// start of the next.
	tickEvery = 30 * time.Second
	// lateLimit is how late a due run may still start: a panel down longer
	// than this at a due time skips that run (no catch-up).
	lateLimit = time.Hour
	// enqueueWait bounds one scheduled Enqueue (its container and size reads).
	enqueueWait = 60 * time.Second
	// minKeep and maxKeep bound the number of copies to keep.
	minKeep, maxKeep = 1, 365
)

// The schedule field codes ValidSchedule answers.
const (
	FieldFrequency = "frequency"
	FieldTime      = "time"
	FieldWeekday   = "weekday"
	FieldKeep      = "keep"
	CodeInvalid    = "invalid"
)

// ValidSchedule checks a schedule's fields: frequency off, daily or weekly;
// time "HH:MM" (00:00 to 23:59, two digits each); weekday 0 (Sunday) to 6,
// checked for every frequency; keep 1 to 365. It answers "", "" when valid,
// else the first bad field and the code "invalid".
func ValidSchedule(f store.ScheduleFields) (field, code string) {
	switch f.Frequency {
	case store.FrequencyOff, store.FrequencyDaily, store.FrequencyWeekly:
	default:
		return FieldFrequency, CodeInvalid
	}
	if _, _, ok := parseClock(f.Time); !ok {
		return FieldTime, CodeInvalid
	}
	if f.Weekday < 0 || f.Weekday > 6 {
		return FieldWeekday, CodeInvalid
	}
	if f.Keep < minKeep || f.Keep > maxKeep {
		return FieldKeep, CodeInvalid
	}
	return "", ""
}

// parseClock reads "HH:MM" (^([01][0-9]|2[0-3]):[0-5][0-9]$).
func parseClock(s string) (hh, mm int, ok bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	digit := func(b byte) (int, bool) { return int(b - '0'), b >= '0' && b <= '9' }
	h1, ok1 := digit(s[0])
	h2, ok2 := digit(s[1])
	m1, ok3 := digit(s[3])
	m2, ok4 := digit(s[4])
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return 0, 0, false
	}
	hh, mm = h1*10+h2, m1*10+m2
	if hh > 23 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

// occursOn returns the schedule's occurrence on the calendar day (y, m, d) of
// loc, ok false when the schedule does not run that day. The time is built
// with time.Date. In a repeated hour time.Date picks one of the two instants
// (the scheduler runs it once either way, since last_run_at stores the
// instant). A wall time a DST gap skips is normalised forward by the gap's
// length (02:30 on a 02:00 → 03:00 day runs at 03:30): time.Date leaves the
// direction unspecified and Go's resolves it backward, so that case is moved.
func occursOn(f store.ScheduleFields, hh, mm, y int, mo time.Month, d int, loc *time.Location) (time.Time, bool) {
	day := time.Date(y, mo, d, 0, 0, 0, 0, time.UTC) // calendar arithmetic only
	if f.Frequency == store.FrequencyWeekly && int(day.Weekday()) != f.Weekday {
		return time.Time{}, false
	}
	c := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc)
	want := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, time.UTC)
	wy, wmo, wd := c.In(loc).Date()
	wh, wmi, _ := c.In(loc).Clock()
	got := time.Date(wy, wmo, wd, wh, wmi, 0, 0, time.UTC)
	if skew := want.Sub(got); skew > 0 {
		c = c.Add(skew) // resolved before the gap: move it past the gap
	}
	return c, true
}

// clockOf is the schedule's time when it runs at all: frequency daily or
// weekly and a valid time and weekday.
func clockOf(f store.ScheduleFields) (hh, mm int, ok bool) {
	if f.Frequency != store.FrequencyDaily && f.Frequency != store.FrequencyWeekly {
		return 0, 0, false
	}
	if f.Weekday < 0 || f.Weekday > 6 {
		return 0, 0, false
	}
	return parseClock(f.Time)
}

// LastOccurrence is the schedule's most recent occurrence at or before now, in
// loc. ok is false when the schedule is off (or its fields are not valid).
func LastOccurrence(f store.ScheduleFields, now time.Time, loc *time.Location) (time.Time, bool) {
	hh, mm, ok := clockOf(f)
	if !ok {
		return time.Time{}, false
	}
	y, mo, d := now.In(loc).Date()
	// An occurrence is never earlier than its own day's wall time (a gap moves
	// it forward), so no later day can hold one at or before now: start today
	// and go back. 15 days cover a weekly schedule and any DST shift.
	for i := 0; i <= 15; i++ {
		if c, ok := occursOn(f, hh, mm, y, mo, d-i, loc); ok && !c.After(now) {
			return c, true
		}
	}
	return time.Time{}, false
}

// NextRun is the schedule's first occurrence strictly after after, in loc. ok
// is false when the schedule is off (or its fields are not valid). The UI's
// next run is NextRun(f, max(now, updated_at)): saving a schedule never runs a
// time already passed.
func NextRun(f store.ScheduleFields, after time.Time, loc *time.Location) (time.Time, bool) {
	hh, mm, ok := clockOf(f)
	if !ok {
		return time.Time{}, false
	}
	y, mo, d := after.In(loc).Date()
	for i := -1; i <= 15; i++ {
		if c, ok := occursOn(f, hh, mm, y, mo, d+i, loc); ok && c.After(after) {
			return c, true
		}
	}
	return time.Time{}, false
}

// dueAt is the due rule (decision 4): the schedule's last occurrence at or
// before now runs when it is later than both its last run and its last change,
// and at most an hour old.
func dueAt(sc store.Schedule, now time.Time, loc *time.Location) (time.Time, bool) {
	due, ok := LastOccurrence(sc.ScheduleFields, now, loc)
	if !ok || !due.After(sc.UpdatedAt) || now.Sub(due) > lateLimit {
		return time.Time{}, false
	}
	if sc.LastRunAt != nil && !due.After(*sc.LastRunAt) {
		return time.Time{}, false
	}
	return due, true
}

// supported is false only when the database can never be backed up as it is
// (not_supported: SQLite, outside a container, an unresolved service), by
// project.BackupReason over the hub's cached containers. The scheduler and
// Back up all skip such a database without a row.
func (m *Manager) supported(t store.Tracked, d project.Database) bool {
	cs, up := m.cs(context.Background())
	return supportedWith(t, d, cs, up)
}

func supportedWith(t store.Tracked, d project.Database, cs []project.Container, up bool) bool {
	_, reason := project.BackupReason(d, t.ComposeProject, t.ServiceDetails, cs, up)
	return reason != project.ReasonNotSupported
}

// Scheduler runs the databases' scheduled backups: every 30 s (from the end
// of one tick to the start of the next, so ticks never overlap) it lists the
// enabled schedules and queues each due one through Manager.Enqueue with the
// schedule actor. last_run_at is stored before Enqueue, so a due time is
// never run twice, even across a crash or a restart; a failed Enqueue is
// logged and not retried. A due time skipped because Drive is not set up or
// the database is not supported is marked too, so it is considered once.
type Scheduler struct {
	st  *store.Store
	m   *Manager
	loc *time.Location
	now func() time.Time
	log *slog.Logger

	// enqueue is m.Enqueue; tests replace it.
	enqueue func(ctx context.Context, t store.Tracked, dbKey string, by Actor) (int64, error)

	mu      sync.Mutex
	started bool
	closed  bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewScheduler returns a Scheduler over st and m that reads schedule times in
// loc. now is time.Now and log slog.Default when nil.
func NewScheduler(st *store.Store, m *Manager, loc *time.Location, now func() time.Time, log *slog.Logger) *Scheduler {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &Scheduler{st: st, m: m, loc: loc, now: now, log: log}
}

// Start runs the first tick at once, then one 30 s after each tick ends,
// until ctx ends or Close. A second Start, or a Start after Close, does
// nothing.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.started, s.done = true, make(chan struct{})
	go s.loop(ctx, s.done)
}

// Close stops the scheduler and waits for a running tick to end. It is called
// before Manager.Close, so no scheduled backup is queued after that.
func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (s *Scheduler) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.tick(ctx)
		timer.Reset(tickEvery)
	}
}

// candidate is one schedule due now.
type candidate struct {
	sc  store.Schedule
	due time.Time
}

// tick queues the schedules due now.
func (s *Scheduler) tick(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			s.log.Error("backup: the scheduler tick panicked", "panic", p)
		}
	}()
	scheds, err := s.st.EnabledSchedules(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("backup: cannot read the backup schedules", "err", err)
		}
		return
	}
	now := s.now()
	var due []candidate
	for _, sc := range scheds {
		if d, ok := dueAt(sc, now, s.loc); ok {
			due = append(due, candidate{sc: sc, due: d})
		}
	}
	if len(due) == 0 {
		return
	}
	// Not set up on this server: nothing the owner can fix from the panel, so
	// no refused rows. Each due time is still marked as considered, so it is
	// logged once (one line for the tick), not on every tick for an hour.
	if s.m.d.Status().State == drive.StateNotConfigured {
		for _, c := range due {
			if !s.mark(ctx, c) && ctx.Err() != nil {
				return
			}
		}
		s.log.Info("backup: scheduled backups skipped: Google Drive is not set up on this server", "due", len(due))
		return
	}
	cs, up := s.m.cs(ctx)
	projects := map[int64]*store.Tracked{}
	for _, c := range due {
		if ctx.Err() != nil {
			return
		}
		s.run(ctx, c, projects, cs, up)
	}
}

// run queues one due schedule. projects caches the tick's project reads (nil
// for a project no longer tracked).
func (s *Scheduler) run(ctx context.Context, c candidate, projects map[int64]*store.Tracked, cs []project.Container, up bool) {
	pid, key := c.sc.ProjectID, c.sc.DBKey
	t, seen := projects[pid]
	if !seen {
		tr, err := s.st.TrackedProject(ctx, pid)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			s.log.Warn("backup: cannot read the project of a scheduled backup", "project", pid, "db", key, "err", err)
			return
		default:
			t = &tr
		}
		projects[pid] = t
	}
	if t == nil || t.MissingSince != nil {
		return // untracked or missing since the list was read
	}
	db, ok := findDatabase(*t, key)
	if !ok {
		return // not detected any more since the list was read
	}
	if !supportedWith(*t, db, cs, up) {
		// Never backed up as it is: no row, but the due time is considered.
		s.mark(ctx, c)
		return
	}
	if !s.mark(ctx, c) {
		return
	}
	ectx, cancel := context.WithTimeout(ctx, enqueueWait)
	defer cancel()
	enqueue := s.enqueue
	if enqueue == nil {
		enqueue = s.m.Enqueue
	}
	id, err := enqueue(ectx, *t, key, ScheduleActor)
	var r *Refusal
	switch {
	case err == nil:
		s.log.Info("backup: scheduled backup queued", "project", pid, "db", key, "due", c.due, "id", id)
	case errors.As(err, &r) && r.Code == CodeAlreadyRunning:
		s.log.Info("backup: scheduled backup skipped: a backup of this database is already queued or running",
			"project", pid, "db", key, "code", r.Code, "id", r.ID)
	case errors.As(err, &r):
		s.log.Info("backup: scheduled backup refused", "project", pid, "db", key, "code", r.Code, "id", r.ID)
	case ctx.Err() != nil:
		// The scheduler is closing (the panel shuts down): not a fault.
		s.log.Info("backup: scheduled backup not queued: the panel is stopping",
			"project", pid, "db", key, "due", c.due, "err", err)
	default:
		s.log.Warn("backup: cannot queue a scheduled backup; it runs at the next occurrence",
			"project", pid, "db", key, "due", c.due, "err", err)
	}
}

// mark stores c's due time as the schedule's last run, before anything is
// queued, so that due time is never considered again. It is false when
// nothing was stored: the schedule row is gone (the project was untracked or
// removed meanwhile), or the write failed (logged at Warn).
func (s *Scheduler) mark(ctx context.Context, c candidate) bool {
	err := s.st.MarkScheduleRun(ctx, c.sc.ProjectID, c.sc.DBKey, c.due)
	if err == nil {
		return true
	}
	if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
		s.log.Warn("backup: cannot store a scheduled run; not queued", "project", c.sc.ProjectID, "db", c.sc.DBKey, "err", err)
	}
	return false
}
