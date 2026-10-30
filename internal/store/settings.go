package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Settings is the editable part of a tracked project; UpdateSettings replaces
// all of it. An empty PublicDomain and a nil threshold are stored as NULL.
type Settings struct {
	DisplayName     string
	PublicDomain    string
	StopTemplate    string // stop | down; the CHECK on the column enforces it
	AlertCPUPercent *int
	AlertRAMMB      *int
}

// UpdateSettings replaces a tracked project's settings in one transaction and
// returns the project as it was and as it is now. An unknown or ignored id gives
// ErrNotFound. The values are validated by the API; the store relies on the
// CHECK for stop_template.
func (s *Store) UpdateSettings(ctx context.Context, id int64, next Settings) (before, after Tracked, err error) {
	if id <= 0 { // 0 would list every tracked project in trackedIn
		return Tracked{}, Tracked{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Tracked{}, Tracked{}, fmt.Errorf("store: update settings: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ts, err := trackedIn(ctx, tx, id)
	if err != nil {
		return Tracked{}, Tracked{}, err
	}
	if len(ts) == 0 {
		return Tracked{}, Tracked{}, ErrNotFound
	}
	var domain sql.NullString
	if next.PublicDomain != "" {
		domain = sql.NullString{String: next.PublicDomain, Valid: true}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET display_name = ?, public_domain = ?, stop_template = ?, alert_cpu_percent = ?, alert_ram_mb = ?
		 WHERE id = ? AND state = 'tracked'`,
		next.DisplayName, domain, next.StopTemplate, intOrNull(next.AlertCPUPercent), intOrNull(next.AlertRAMMB), id); err != nil {
		return Tracked{}, Tracked{}, fmt.Errorf("store: update settings: %w", err)
	}
	ts2, err := trackedIn(ctx, tx, id)
	if err != nil {
		return Tracked{}, Tracked{}, err
	}
	if len(ts2) == 0 {
		return Tracked{}, Tracked{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Tracked{}, Tracked{}, fmt.Errorf("store: update settings: %w", err)
	}
	return ts[0], ts2[0], nil
}

// intOrNull stores a nil threshold as NULL.
func intOrNull(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
