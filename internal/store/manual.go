package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrDatabaseExists is a hand-added database the project already has: the
// same type, service and name.
var ErrDatabaseExists = errors.New("store: database already added")

// ManualSource is the one entry in Sources of a database added by hand.
const ManualSource = "added by hand"

// ManualDatabase is a database the owner added by hand to a project's compose
// service. UserVar and PasswordVar are the names of the environment variables
// that hold the credentials inside the container, never values; both empty
// means the image's standard variables. The store does not validate the
// fields beyond the table's CHECKs; the API does.
type ManualDatabase struct {
	Type, Name, Service, UserVar, PasswordVar, CreatedBy string
}

// manualKey is the backup key of a hand-added database (project.DatabaseKey).
func manualKey(id int64) string { return fmt.Sprintf("manual:%d", id) }

// AddManualDatabase stores a hand-added database of a tracked project and its
// schedule row, from the current backup defaults, in one transaction, and
// returns the new id. A project that is unknown or not tracked gives
// ErrNotFound, one that already has a hand-added database of the same type,
// service and name ErrDatabaseExists. A rescan never touches the row.
func (s *Store) AddManualDatabase(ctx context.Context, projectID int64, m ManualDatabase) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: add manual database: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var tracked int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE id = ? AND state = 'tracked'`, projectID).Scan(&tracked); err != nil {
		return 0, fmt.Errorf("store: add manual database: %w", err)
	}
	if tracked == 0 {
		return 0, ErrNotFound
	}
	now := formatTime(time.Now())
	res, err := tx.ExecContext(ctx,
		`INSERT INTO manual_databases (project_id, type, name, service, user_var, password_var, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID, m.Type, m.Name, m.Service, m.UserVar, m.PasswordVar, m.CreatedBy, now)
	if isUnique(err, "manual_databases.project_id") {
		return 0, ErrDatabaseExists
	}
	if err != nil {
		return 0, fmt.Errorf("store: add manual database: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: add manual database: %w", err)
	}
	// insertDefaultSchedules reads only `databases`, so the row is written here.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO backup_schedules (project_id, db_key, frequency, run_time, weekday, keep, compress, updated_at)
		 SELECT ?, ?, b.frequency, b.run_time, b.weekday, b.keep, b.compress, ? FROM backup_defaults b WHERE b.id = 1`,
		projectID, manualKey(id), now); err != nil {
		return 0, fmt.Errorf("store: add manual database: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: add manual database: %w", err)
	}
	return id, nil
}

// RemoveManualDatabase deletes a hand-added database and its schedule row. Its
// backups are not touched. An unknown id, or one that belongs to another
// project, gives ErrNotFound.
func (s *Store) RemoveManualDatabase(ctx context.Context, projectID, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: remove manual database: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM manual_databases WHERE id = ? AND project_id = ?`, id, projectID)
	if err != nil {
		return fmt.Errorf("store: remove manual database: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: remove manual database: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM backup_schedules WHERE project_id = ? AND db_key = ?`, projectID, manualKey(id)); err != nil {
		return fmt.Errorf("store: remove manual database: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: remove manual database: %w", err)
	}
	return nil
}
