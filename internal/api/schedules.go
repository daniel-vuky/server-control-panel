package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// recentContainers is how old the hub's container list may be for the
// schedule route's and the summary's "can this database be backed up at all"
// check.
const recentContainers = 10 * time.Second

// fieldDatabase is the schedule input's database field.
const fieldDatabase = "database"

// The words of the schedule field errors, by field.
var scheduleWords = map[string]string{
	backup.FieldFrequency: "Choose Off, Daily or Weekly.",
	backup.FieldTime:      "Enter a time as HH:MM, from 00:00 to 23:59.",
	backup.FieldWeekday:   "Choose a day of the week.",
	backup.FieldKeep:      "Enter a whole number from 1 to 365.",
}

const (
	msgUnknownDatabase  = "This database is not one of the project's databases."
	msgScheduleNotAllow = "This database cannot be backed up, so it cannot have a schedule."
	defaultsTarget      = "Backup defaults"
)

// backupSettings is GET /api/backup-settings: the defaults a newly found
// database starts from and the server zone with its offset now.
func (s *server) backupSettings(w http.ResponseWriter, r *http.Request) {
	f, err := s.Store.BackupDefaults(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BackupSettings{
		Defaults: fromStoreFields(f),
		TimeZone: TimeZone{Name: s.Zone.String(), Offset: s.Now().In(s.Zone).Format("-07:00")},
	})
}

// backupDefaults is POST /api/backup-settings/defaults: it replaces the
// defaults (never the existing schedules) and answers them. A field error is
// 422; a save that changes something is recorded as backup.defaults with the
// changed fields.
func (s *server) backupDefaults(w http.ResponseWriter, r *http.Request) {
	var req ScheduleFields
	typeErr, ok := readJSONNumbers(w, r, &req, backup.FieldWeekday, backup.FieldKeep)
	if !ok {
		return
	}
	f := toStoreFields(req)
	if fe := validScheduleFields(f, typeErr); fe != nil {
		writeJSON(w, http.StatusUnprocessableEntity, fe)
		return
	}
	// The write outlives the request, so a saved change keeps its activity row.
	ctx := context.WithoutCancel(r.Context())
	before, changed, err := s.Store.SetBackupDefaults(ctx, f, s.Now())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if changed {
		s.record(r, store.Activity{Action: store.ActBackupDefaults, Target: defaultsTarget}, store.ResultOK, scheduleChanges(before, f))
	}
	writeJSON(w, http.StatusOK, fromStoreFields(f))
}

// backupSchedule is POST /api/projects/{id}/backup-schedule: it replaces one
// database's schedule and answers the Project. 404 for a project that is not
// tracked; 422 on database for a key that is not one of its databases or a
// database that can never be backed up (not_supported), then on the first bad
// field. A save that changes something is recorded as backup.schedule and
// pushed to every open tab through Hub.Refresh.
func (s *server) backupSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	}
	var req BackupScheduleInput
	typeErr, ok := readJSONNumbers(w, r, &req, backup.FieldWeekday, backup.FieldKeep)
	if !ok {
		return
	}
	t, err := s.Store.TrackedProject(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	db, found := findDatabase(t, req.Database)
	if !found {
		writeFieldError(w, fieldDatabase, "unknown", msgUnknownDatabase)
		return
	}
	if !s.supported(r.Context(), t, db) {
		writeFieldError(w, fieldDatabase, project.ReasonNotSupported, msgScheduleNotAllow)
		return
	}
	f := store.ScheduleFields{Frequency: req.Frequency, Time: req.Time, Weekday: req.Weekday, Keep: req.Keep, Compress: req.Compress}
	if fe := validScheduleFields(f, typeErr); fe != nil {
		writeJSON(w, http.StatusUnprocessableEntity, fe)
		return
	}

	ctx := context.WithoutCancel(r.Context())
	before, changed, err := s.Store.SetSchedule(ctx, id, req.Database, f, s.Now())
	switch {
	case errors.Is(err, store.ErrNotFound): // untracked meanwhile
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if changed {
		old := before.ScheduleFields
		if before.UpdatedAt.IsZero() {
			old = store.ScheduleFields{} // no row before: every field reads "none"
		}
		s.record(r, store.Activity{Action: store.ActBackupSchedule, ProjectID: id, Target: t.DisplayName + " · " + db.Name},
			store.ResultOK, scheduleChanges(old, f))
		s.Hub.Refresh()
	}
	after, err := s.Store.TrackedProject(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toProjects(s.readViews(r.Context(), []store.Tracked{after}))[0])
}

// findDatabase is the detected database of t with key.
func findDatabase(t store.Tracked, key string) (project.Database, bool) {
	for _, d := range t.Databases {
		if project.DatabaseKey(d) == key {
			return d, true
		}
	}
	return project.Database{}, false
}

// supported is false only when d can never be backed up as it is
// (project.BackupReason's not_supported: SQLite, outside a container, an
// unresolved service), over the hub's recent containers, as the scheduler
// decides it. With Docker down the stored services still resolve it.
func (s *server) supported(ctx context.Context, t store.Tracked, d project.Database) bool {
	cs, up := s.Hub.RecentContainers(ctx, recentContainers)
	_, reason := project.BackupReason(d, t.ComposeProject, t.ServiceDetails, cs, up)
	return reason != project.ReasonNotSupported
}

// validScheduleFields checks the fields in form order. typeErr names a
// number field whose value did not decode as a whole number.
func validScheduleFields(f store.ScheduleFields, typeErr string) *FieldError {
	field, code := backup.ValidSchedule(f)
	order := []string{backup.FieldFrequency, backup.FieldTime, backup.FieldWeekday, backup.FieldKeep}
	if typeErr != "" && (field == "" || slices.Index(order, typeErr) < slices.Index(order, field)) {
		field, code = typeErr, backup.CodeInvalid
	}
	if field == "" {
		return nil
	}
	return &FieldError{Error: scheduleWords[field], Field: field, Code: code}
}

func toStoreFields(f ScheduleFields) store.ScheduleFields {
	return store.ScheduleFields{Frequency: f.Frequency, Time: f.Time, Weekday: f.Weekday, Keep: f.Keep, Compress: f.Compress}
}

func fromStoreFields(f store.ScheduleFields) ScheduleFields {
	return ScheduleFields{Frequency: f.Frequency, Time: f.Time, Weekday: f.Weekday, Keep: f.Keep, Compress: f.Compress}
}

// scheduleChanges is the activity reason of a schedule or defaults save: each
// changed field as `field: old → new`, joined with "; ", in form order. The
// weekday is its English name and compress reads on or off; a zero before
// (no row yet) reads "none". It is "" when nothing changed.
func scheduleChanges(before, after store.ScheduleFields) string {
	none := before == store.ScheduleFields{}
	var parts []string
	add := func(field, old, next string) {
		if none {
			old = "none"
		}
		if none || old != next {
			parts = append(parts, field+": "+old+" → "+next)
		}
	}
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	add(backup.FieldFrequency, before.Frequency, after.Frequency)
	add(backup.FieldTime, before.Time, after.Time)
	add(backup.FieldWeekday, weekdayName(before.Weekday), weekdayName(after.Weekday))
	add(backup.FieldKeep, strconv.Itoa(before.Keep), strconv.Itoa(after.Keep))
	add("compress", onOff(before.Compress), onOff(after.Compress))
	return strings.Join(parts, "; ")
}

// weekdayName is the English name of a valid weekday, else the number.
func weekdayName(d int) string {
	if d < 0 || d > 6 {
		return strconv.Itoa(d)
	}
	return time.Weekday(d).String()
}

// summaryNextRun is the earliest next run over the enabled schedules of the
// tracked, present projects whose database can be backed up at all, with the
// projects due at that same minute (display names, in project order); nil
// when nothing is scheduled.
func (s *server) summaryNextRun(ctx context.Context, now time.Time) (*SummaryNextRun, error) {
	scs, err := s.Store.EnabledSchedules(ctx)
	if err != nil || len(scs) == 0 {
		return nil, err
	}
	ts, err := s.Store.TrackedProjects(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]store.Tracked, len(ts))
	for _, t := range ts {
		byID[t.ID] = t
	}
	cs, up := s.Hub.RecentContainers(ctx, recentContainers)
	var earliest time.Time
	var names []string
	var seen map[int64]bool
	for _, sc := range scs {
		t, ok := byID[sc.ProjectID]
		if !ok {
			continue
		}
		d, ok := findDatabase(t, sc.DBKey)
		if !ok {
			continue
		}
		if _, reason := project.BackupReason(d, t.ComposeProject, t.ServiceDetails, cs, up); reason == project.ReasonNotSupported {
			continue
		}
		next, ok := s.nextRun(sc, now)
		if !ok {
			continue
		}
		next = next.Truncate(time.Minute)
		switch {
		case earliest.IsZero() || next.Before(earliest):
			earliest, names, seen = next, []string{t.DisplayName}, map[int64]bool{t.ID: true}
		case next.Equal(earliest) && !seen[t.ID]:
			names = append(names, t.DisplayName)
			seen[t.ID] = true
		}
	}
	if earliest.IsZero() {
		return nil, nil
	}
	return &SummaryNextRun{At: earliest.In(s.Zone).Format(time.RFC3339), Projects: names}, nil
}
