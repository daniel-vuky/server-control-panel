package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Schedule frequencies.
const (
	FrequencyOff    = "off"
	FrequencyDaily  = "daily"
	FrequencyWeekly = "weekly"
)

// ScheduleFields are the settings of a schedule, and of the defaults new
// databases start from. Time is "HH:MM" in the server zone; Weekday is Go's
// time.Weekday (0 = Sunday) and only matters for weekly; Keep is how many
// copies retention keeps on Drive. The store does not validate them beyond
// the table's CHECKs; the API does.
type ScheduleFields struct {
	Frequency string
	Time      string
	Weekday   int
	Keep      int
	Compress  bool
}

// Schedule is one database's backup schedule. DBKey is project.DatabaseKey of
// the database. LastRunAt is the due time of the last scheduled run (nil when
// none ran); UpdatedAt is when the fields last changed.
type Schedule struct {
	ScheduleFields
	ProjectID int64
	DBKey     string
	LastRunAt *time.Time
	UpdatedAt time.Time
}

const scheduleColumns = `s.project_id, s.db_key, s.frequency, s.run_time, s.weekday, s.keep, s.compress, s.last_run_at, s.updated_at`

func scanSchedule(r rowScanner) (Schedule, error) {
	var (
		sc      Schedule
		lastRun sql.NullString
		updated string
	)
	if err := r.Scan(&sc.ProjectID, &sc.DBKey, &sc.Frequency, &sc.Time, &sc.Weekday, &sc.Keep, &sc.Compress, &lastRun, &updated); err != nil {
		return Schedule{}, err
	}
	if lastRun.Valid {
		t, err := parseTime(lastRun.String)
		if err != nil {
			return Schedule{}, fmt.Errorf("store: schedule last_run_at: %w", err)
		}
		sc.LastRunAt = &t
	}
	t, err := parseTime(updated)
	if err != nil {
		return Schedule{}, fmt.Errorf("store: schedule updated_at: %w", err)
	}
	sc.UpdatedAt = t
	return sc, nil
}

// BackupDefaults returns the settings a newly stored database starts from.
func (s *Store) BackupDefaults(ctx context.Context) (ScheduleFields, error) {
	f, err := readDefaults(ctx, s.db)
	if err != nil {
		return ScheduleFields{}, fmt.Errorf("store: read backup defaults: %w", err)
	}
	return f, nil
}

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func readDefaults(ctx context.Context, q rowQueryer) (ScheduleFields, error) {
	var f ScheduleFields
	err := q.QueryRowContext(ctx, `SELECT frequency, run_time, weekday, keep, compress FROM backup_defaults WHERE id = 1`).
		Scan(&f.Frequency, &f.Time, &f.Weekday, &f.Keep, &f.Compress)
	return f, err
}

// SetBackupDefaults replaces the defaults and returns them as they were.
// Existing schedules are never touched. When nothing changed it writes
// nothing (updated_at included) and changed is false.
func (s *Store) SetBackupDefaults(ctx context.Context, f ScheduleFields, now time.Time) (before ScheduleFields, changed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScheduleFields{}, false, fmt.Errorf("store: set backup defaults: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	before, err = readDefaults(ctx, tx)
	if err != nil {
		return ScheduleFields{}, false, fmt.Errorf("store: set backup defaults: %w", err)
	}
	if before == f {
		return before, false, nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE backup_defaults SET frequency = ?, run_time = ?, weekday = ?, keep = ?, compress = ?, updated_at = ? WHERE id = 1`,
		f.Frequency, f.Time, f.Weekday, f.Keep, f.Compress, formatTime(now)); err != nil {
		return ScheduleFields{}, false, fmt.Errorf("store: set backup defaults: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ScheduleFields{}, false, fmt.Errorf("store: set backup defaults: %w", err)
	}
	return before, true, nil
}

// SetSchedule stores a database's schedule (insert or replace) and returns it
// as it was; with no row yet, before carries only ProjectID and DBKey.
// last_run_at is kept. When no field changed it writes nothing, so updated_at
// is not bumped (a bump would suppress a run due within the hour), and changed
// is false. The key is not checked against the detected databases. A project
// that is unknown or not tracked gives ErrNotFound.
func (s *Store) SetSchedule(ctx context.Context, projectID int64, dbKey string, f ScheduleFields, now time.Time) (before Schedule, changed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Schedule{}, false, fmt.Errorf("store: set schedule: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var tracked int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE id = ? AND state = 'tracked'`, projectID).Scan(&tracked); err != nil {
		return Schedule{}, false, fmt.Errorf("store: set schedule: %w", err)
	}
	if tracked == 0 {
		return Schedule{}, false, ErrNotFound
	}
	before, err = scanSchedule(tx.QueryRowContext(ctx,
		`SELECT `+scheduleColumns+` FROM backup_schedules s WHERE s.project_id = ? AND s.db_key = ?`, projectID, dbKey))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		before = Schedule{ProjectID: projectID, DBKey: dbKey}
	case err != nil:
		return Schedule{}, false, fmt.Errorf("store: set schedule: %w", err)
	case before.ScheduleFields == f:
		return before, false, nil
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO backup_schedules (project_id, db_key, frequency, run_time, weekday, keep, compress, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (project_id, db_key) DO UPDATE SET
		   frequency = excluded.frequency, run_time = excluded.run_time, weekday = excluded.weekday,
		   keep = excluded.keep, compress = excluded.compress, updated_at = excluded.updated_at`,
		projectID, dbKey, f.Frequency, f.Time, f.Weekday, f.Keep, f.Compress, formatTime(now)); err != nil {
		return Schedule{}, false, fmt.Errorf("store: set schedule: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Schedule{}, false, fmt.Errorf("store: set schedule: %w", err)
	}
	return before, true, nil
}

// EnabledSchedules lists the schedules that may run: frequency not off, the
// project tracked and present (no missing_since), and the key matching a
// database detected now (an orphan row, whose database was renamed or
// removed, is left out until it is detected again). Ordered by project, then
// key.
func (s *Store) EnabledSchedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduleColumns+`
		FROM backup_schedules s JOIN projects p ON p.id = s.project_id
		WHERE s.frequency <> 'off' AND p.state = 'tracked' AND p.missing_since IS NULL
		  AND EXISTS (SELECT 1 FROM databases d
		              WHERE d.project_id = s.project_id AND d.type || ':' || d.location || ':' || d.name = s.db_key)
		ORDER BY s.project_id, s.db_key`)
	if err != nil {
		return nil, fmt.Errorf("store: list enabled schedules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list enabled schedules: %w", err)
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list enabled schedules: %w", err)
	}
	return out, nil
}

// MarkScheduleRun stores due as the schedule's last run. It gives ErrNotFound
// when the schedule row does not exist (the project was untracked or removed).
func (s *Store) MarkScheduleRun(ctx context.Context, projectID int64, dbKey string, due time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE backup_schedules SET last_run_at = ? WHERE project_id = ? AND db_key = ?`,
		formatTime(due), projectID, dbKey)
	if err != nil {
		return fmt.Errorf("store: mark schedule run: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: mark schedule run: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// insertDefaultSchedules gives every stored database of a tracked project
// that has no schedule yet one from the current defaults. Existing rows are
// left as they are (INSERT OR IGNORE), so a rescan never resets a schedule. It
// runs in the caller's transaction, right after the databases are written.
func insertDefaultSchedules(ctx context.Context, tx *sql.Tx, projectID int64, now string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO backup_schedules (project_id, db_key, frequency, run_time, weekday, keep, compress, updated_at)
		 SELECT d.project_id, d.type || ':' || d.location || ':' || d.name, b.frequency, b.run_time, b.weekday, b.keep, b.compress, ?
		 FROM databases d JOIN projects p ON p.id = d.project_id CROSS JOIN backup_defaults b
		 WHERE d.project_id = ? AND p.state = 'tracked' AND b.id = 1`, now, projectID); err != nil {
		return fmt.Errorf("store: insert default schedules: %w", err)
	}
	return nil
}

// projectSchedules reads every schedule row of the tracked projects (or of
// only, when non-zero), including rows whose database is no longer detected.
func projectSchedules(ctx context.Context, tx queryer, only int64) ([]Schedule, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+scheduleColumns+`
		FROM backup_schedules s JOIN projects p ON p.id = s.project_id
		WHERE p.state = 'tracked' AND (? = 0 OR p.id = ?)`, only, only)
	if err != nil {
		return nil, fmt.Errorf("store: list schedules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list schedules: %w", err)
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list schedules: %w", err)
	}
	return out, nil
}
