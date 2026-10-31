package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const interruptedBackupError = "The panel restarted during this backup"

// BackupStep is one step of a backup: step is dump, compress, upload or
// prune; state is waiting, running, done, failed or skipped.
type BackupStep struct {
	Step   string `json:"step"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// Backup is one backup run: a snapshot of the target database, its progress
// and its result. State is queued, running, ok, failed, refused or interrupted;
// FailedStep is empty unless a step failed.
type Backup struct {
	// QueuePosition is not stored: backup.Manager.Active() sets it.
	QueuePosition int `json:"-" db:"-"`

	ID, ProjectID                                                      int64
	ProjectName, DBType, DBName, DBLocationKind, DBLocation, DBService string
	DBKey, State, FailedStep                                           string
	Steps                                                              []BackupStep
	Error, Hint                                                        string
	RawBytes, SizeBytes                                                int64
	FileName, DriveFileID, DriveLink, StartedBy                        string
	CreatedAt                                                          time.Time
	StartedAt, FinishedAt                                              *time.Time
	DurationMS                                                         int64
	// PrunedAt is when retention removed the row's Drive file (nil: not
	// removed). Only MarkPruned writes it.
	PrunedAt *time.Time
}

const backupColumns = `id, project_id, project_name, db_type, db_name, db_location_kind, db_location, db_service, db_key,
	state, failed_step, steps, error, hint, raw_bytes, size_bytes, file_name, drive_file_id, drive_link,
	started_by, created_at, started_at, finished_at, duration_ms, pruned_at`

// InsertBackup stores a new backup and returns its id.
func (s *Store) InsertBackup(ctx context.Context, b Backup) (int64, error) {
	steps, err := marshalSteps(b.Steps)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO backups (project_id, project_name, db_type, db_name, db_location_kind, db_location, db_service, db_key,
		   state, failed_step, steps, error, hint, raw_bytes, size_bytes, file_name, drive_file_id, drive_link,
		   started_by, created_at, started_at, finished_at, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ProjectID, b.ProjectName, b.DBType, b.DBName, b.DBLocationKind, b.DBLocation, b.DBService, b.DBKey,
		b.State, nullString(b.FailedStep), steps, b.Error, b.Hint, b.RawBytes, b.SizeBytes, b.FileName, b.DriveFileID, b.DriveLink,
		b.StartedBy, formatTime(b.CreatedAt), nullTime(b.StartedAt), nullTime(b.FinishedAt), b.DurationMS)
	if err != nil {
		return 0, fmt.Errorf("store: insert backup: %w", err)
	}
	return res.LastInsertId()
}

// UpdateBackup writes every field of b except its identity (project and
// snapshot), by id; the database's service, which the worker resolves again,
// is written too. ErrNotFound for an unknown id.
func (s *Store) UpdateBackup(ctx context.Context, b Backup) error {
	steps, err := marshalSteps(b.Steps)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE backups SET db_service = ?, state = ?, failed_step = ?, steps = ?, error = ?, hint = ?, raw_bytes = ?, size_bytes = ?,
		   file_name = ?, drive_file_id = ?, drive_link = ?, started_at = ?, finished_at = ?, duration_ms = ? WHERE id = ?`,
		b.DBService, b.State, nullString(b.FailedStep), steps, b.Error, b.Hint, b.RawBytes, b.SizeBytes,
		b.FileName, b.DriveFileID, b.DriveLink, nullTime(b.StartedAt), nullTime(b.FinishedAt), b.DurationMS, b.ID)
	if err != nil {
		return fmt.Errorf("store: update backup: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: update backup: %w", err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetBackup returns one backup, or ErrNotFound.
func (s *Store) GetBackup(ctx context.Context, id int64) (Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+backupColumns+` FROM backups WHERE id = ?`, id)
	if err != nil {
		return Backup{}, fmt.Errorf("store: get backup: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Backup{}, fmt.Errorf("store: get backup: %w", err)
		}
		return Backup{}, ErrNotFound
	}
	return scanBackup(rows)
}

// ProjectBackups lists a project's backups, newest first. A positive before
// lists only the ids below it (the id of the last row of the previous page);
// more says whether older rows remain. limit must be positive.
func (s *Store) ProjectBackups(ctx context.Context, projectID, before int64, limit int) ([]Backup, bool, error) {
	if limit <= 0 {
		return []Backup{}, false, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+backupColumns+` FROM backups WHERE project_id = ? AND (? <= 0 OR id < ?) ORDER BY id DESC LIMIT ?`,
		projectID, before, before, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("store: list backups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("store: list backups: %w", err)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

// BackupFilter selects the rows of ListBackups. Zero values mean "any":
// ProjectID 0 is every project, Result "" is every state, a zero From or To
// is an open end. Result "ok" is state ok; "failed" is failed, refused and
// interrupted (never queued or running rows). From is inclusive and To
// exclusive, both UTC instants compared with created_at. A positive Before
// lists only ids below it; Limit must be positive.
type BackupFilter struct {
	ProjectID int64
	Result    string
	From, To  time.Time
	Before    int64
	Limit     int
}

// failedStates are the states the all-backups page counts as failed.
const failedStates = `'failed', 'refused', 'interrupted'`

// ListBackups lists backups across projects, newest first (by id), with
// the filter's bounds, and whether older rows remain.
func (s *Store) ListBackups(ctx context.Context, f BackupFilter) ([]Backup, bool, error) {
	if f.Limit <= 0 {
		return []Backup{}, false, nil
	}
	q := `SELECT ` + backupColumns + ` FROM backups WHERE 1 = 1`
	var args []any
	if f.ProjectID > 0 {
		q += ` AND project_id = ?`
		args = append(args, f.ProjectID)
	}
	switch f.Result {
	case "ok":
		q += ` AND state = 'ok'`
	case "failed":
		q += ` AND state IN (` + failedStates + `)`
	}
	if !f.From.IsZero() {
		q += ` AND created_at >= ?`
		args = append(args, formatTime(f.From))
	}
	if !f.To.IsZero() {
		q += ` AND created_at < ?`
		args = append(args, formatTime(f.To))
	}
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, fmt.Errorf("store: list all backups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("store: list all backups: %w", err)
	}
	more := len(out) > f.Limit
	if more {
		out = out[:f.Limit]
	}
	return out, more, nil
}

// DayCount is one day of the summary strip: Start is the day's first instant.
type DayCount struct {
	Start      time.Time
	OK, Failed int
}

// Summary is the all-backups page's numbers. The Drive totals are what the
// panel stored (rows with a file on Drive, not pruned), not a Drive query.
type Summary struct {
	DriveBytes                int64
	DriveFiles, DriveProjects int
	Days                      []DayCount
	OK7d, Failed7d            int
	RecentFailures            []Backup
}

// BackupSummary reads the summary. dayStarts are 8 instants: the starts of
// the 7 days (oldest first) and the start of the day after the last; day i
// covers [dayStarts[i], dayStarts[i+1]) of created_at. The newest two failed
// or refused backups created inside that whole window come back.
func (s *Store) BackupSummary(ctx context.Context, dayStarts []time.Time) (Summary, error) {
	if len(dayStarts) != 8 {
		return Summary{}, fmt.Errorf("store: backup summary needs 8 day instants, got %d", len(dayStarts))
	}
	sum := Summary{Days: make([]DayCount, 0, 7), RecentFailures: []Backup{}}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(size_bytes), 0), count(*), count(DISTINCT project_id) FROM backups
		 WHERE drive_file_id <> '' AND pruned_at IS NULL`).Scan(&sum.DriveBytes, &sum.DriveFiles, &sum.DriveProjects); err != nil {
		return Summary{}, fmt.Errorf("store: backup summary drive: %w", err)
	}
	for i := 0; i < 7; i++ {
		d := DayCount{Start: dayStarts[i]}
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(state = 'ok'), 0), COALESCE(SUM(state IN (`+failedStates+`)), 0) FROM backups
			 WHERE created_at >= ? AND created_at < ?`,
			formatTime(dayStarts[i]), formatTime(dayStarts[i+1])).Scan(&d.OK, &d.Failed); err != nil {
			return Summary{}, fmt.Errorf("store: backup summary day: %w", err)
		}
		sum.OK7d += d.OK
		sum.Failed7d += d.Failed
		sum.Days = append(sum.Days, d)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+backupColumns+` FROM backups WHERE state IN ('failed', 'refused') AND created_at >= ? AND created_at < ? ORDER BY id DESC LIMIT 2`,
		formatTime(dayStarts[0]), formatTime(dayStarts[7]))
	if err != nil {
		return Summary{}, fmt.Errorf("store: backup summary failures: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return Summary{}, err
		}
		sum.RecentFailures = append(sum.RecentFailures, b)
	}
	if err := rows.Err(); err != nil {
		return Summary{}, fmt.Errorf("store: backup summary failures: %w", err)
	}
	return sum, nil
}

// ActiveBackup returns the id of the queued or running backup of a project's
// database, if there is one.
func (s *Store) ActiveBackup(ctx context.Context, projectID int64, dbKey string) (int64, bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM backups WHERE project_id = ? AND db_key = ? AND state IN ('queued', 'running') ORDER BY id LIMIT 1`,
		projectID, dbKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("store: active backup: %w", err)
	}
	return id, true, nil
}

// RetentionCandidates lists the backups of one database whose file is on
// Drive (a Drive file id, not pruned), whatever their state, newest first:
// the copies retention counts (spec decision 9). Only rows the panel stored
// count.
func (s *Store) RetentionCandidates(ctx context.Context, projectID int64, dbKey string) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+backupColumns+` FROM backups
		 WHERE project_id = ? AND db_key = ? AND drive_file_id <> '' AND pruned_at IS NULL ORDER BY id DESC`,
		projectID, dbKey)
	if err != nil {
		return nil, fmt.Errorf("store: retention candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: retention candidates: %w", err)
	}
	return out, nil
}

// MarkPruned stamps a backup whose Drive file retention removed. The row
// stays (history is a record). ErrNotFound for an unknown id.
func (s *Store) MarkPruned(ctx context.Context, id int64, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE backups SET pruned_at = ? WHERE id = ?`, formatTime(at), id)
	if err != nil {
		return fmt.Errorf("store: mark pruned: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: mark pruned: %w", err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// StoredCopies counts a project's copies on Drive (rows with a Drive file id,
// not pruned) by db_key.
func (s *Store) StoredCopies(ctx context.Context, projectID int64) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT db_key, count(*) FROM backups
		 WHERE project_id = ? AND drive_file_id <> '' AND pruned_at IS NULL GROUP BY db_key`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: stored copies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, fmt.Errorf("store: stored copies: %w", err)
		}
		out[key] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: stored copies: %w", err)
	}
	return out, nil
}

// interruptActiveBackups ends backups a previous process left queued or
// running, next to interruptRunningJobs. They are not resumed. Their steps end
// as the manager's own interrupt leaves them: a running step failed, a waiting
// one skipped, so no step still reads as in progress.
func (s *Store) interruptActiveBackups(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: interrupt active backups: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	type active struct {
		id    int64
		steps string
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, steps FROM backups WHERE state IN ('queued', 'running')`)
	if err != nil {
		return fmt.Errorf("store: interrupt active backups: %w", err)
	}
	var found []active
	for rows.Next() {
		var a active
		if err := rows.Scan(&a.id, &a.steps); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: interrupt active backups: %w", err)
		}
		found = append(found, a)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("store: interrupt active backups: %w", err)
	}
	_ = rows.Close()
	now := formatTime(time.Now())
	for _, a := range found {
		var steps []BackupStep
		if err := json.Unmarshal([]byte(a.steps), &steps); err != nil {
			return fmt.Errorf("store: backup %d steps: %w", a.id, err)
		}
		for i := range steps {
			switch steps[i].State {
			case "running":
				steps[i].State = "failed"
			case "waiting":
				steps[i].State = "skipped"
			}
		}
		js, err := marshalSteps(steps)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE backups SET state = 'interrupted', error = ?, finished_at = ?, steps = ? WHERE id = ?`,
			interruptedBackupError, now, js, a.id); err != nil {
			return fmt.Errorf("store: interrupt active backups: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: interrupt active backups: %w", err)
	}
	return nil
}

func marshalSteps(steps []BackupStep) (string, error) {
	if steps == nil {
		steps = []BackupStep{}
	}
	b, err := json.Marshal(steps)
	if err != nil {
		return "", fmt.Errorf("store: backup steps: %w", err)
	}
	return string(b), nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func scanBackup(rows *sql.Rows) (Backup, error) {
	var (
		b                 Backup
		failed            sql.NullString
		steps, created    string
		started, finished sql.NullString
		pruned            sql.NullString
	)
	if err := rows.Scan(&b.ID, &b.ProjectID, &b.ProjectName, &b.DBType, &b.DBName, &b.DBLocationKind, &b.DBLocation, &b.DBService, &b.DBKey,
		&b.State, &failed, &steps, &b.Error, &b.Hint, &b.RawBytes, &b.SizeBytes, &b.FileName, &b.DriveFileID, &b.DriveLink,
		&b.StartedBy, &created, &started, &finished, &b.DurationMS, &pruned); err != nil {
		return Backup{}, fmt.Errorf("store: read backup: %w", err)
	}
	b.FailedStep = failed.String
	if err := json.Unmarshal([]byte(steps), &b.Steps); err != nil {
		return Backup{}, fmt.Errorf("store: backup %d steps: %w", b.ID, err)
	}
	if b.Steps == nil {
		b.Steps = []BackupStep{}
	}
	var err error
	if b.CreatedAt, err = parseTime(created); err != nil {
		return Backup{}, fmt.Errorf("store: backup %d created_at: %w", b.ID, err)
	}
	if b.StartedAt, err = parseNullTime(started); err != nil {
		return Backup{}, fmt.Errorf("store: backup %d started_at: %w", b.ID, err)
	}
	if b.FinishedAt, err = parseNullTime(finished); err != nil {
		return Backup{}, fmt.Errorf("store: backup %d finished_at: %w", b.ID, err)
	}
	if b.PrunedAt, err = parseNullTime(pruned); err != nil {
		return Backup{}, fmt.Errorf("store: backup %d pruned_at: %w", b.ID, err)
	}
	return b, nil
}

func parseNullTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid {
		return nil, nil
	}
	t, err := parseTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// LatestBackup is a tracked project's most recent ended backup: its state is
// ok, failed, refused or interrupted, never queued or running. At is when it
// finished, or when it was created for a row with no finish time.
type LatestBackup struct {
	ID, ProjectID         int64
	ProjectName, Database string
	State                 string
	At                    time.Time
}

// LatestBackups returns the newest ended backup of every tracked project that
// has one (a Not found project included), newest first by id, so the first row
// is the latest overall. The project name is its current display name. A
// project with no ended backup, and an ignored one, has no row.
func (s *Store) LatestBackups(ctx context.Context) ([]LatestBackup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT b.id, b.project_id, p.display_name, b.db_name, b.state, COALESCE(b.finished_at, b.created_at)
		 FROM backups b JOIN projects p ON p.id = b.project_id
		 WHERE p.state = 'tracked'
		   AND b.id = (SELECT MAX(id) FROM backups x WHERE x.project_id = b.project_id
		               AND x.state IN ('ok', 'failed', 'refused', 'interrupted'))
		 ORDER BY b.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: latest backups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []LatestBackup{}
	for rows.Next() {
		var (
			l  LatestBackup
			at string
		)
		if err := rows.Scan(&l.ID, &l.ProjectID, &l.ProjectName, &l.Database, &l.State, &at); err != nil {
			return nil, fmt.Errorf("store: read latest backup: %w", err)
		}
		if l.At, err = parseTime(at); err != nil {
			return nil, fmt.Errorf("store: latest backup %d time: %w", l.ID, err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: latest backups: %w", err)
	}
	return out, nil
}
