package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AlertSettings are the server-wide alert thresholds (percent, 1 to 100), how
// many minutes a resource must stay over one (1 to 60), and whether the tunnel
// was ever seen connected (the tunnel alert and the domain pause need it).
type AlertSettings struct {
	CPUPercent, RAMPercent, DiskPercent, SustainedMinutes int
	TunnelSeenConnected                                   bool
}

// AlertSettings returns the stored settings; a fresh database has 90/90/85/5.
func (s *Store) AlertSettings(ctx context.Context) (AlertSettings, error) {
	a, err := readAlertSettings(ctx, s.db)
	if err != nil {
		return AlertSettings{}, fmt.Errorf("store: read alert settings: %w", err)
	}
	return a, nil
}

func readAlertSettings(ctx context.Context, q rowQueryer) (AlertSettings, error) {
	var a AlertSettings
	err := q.QueryRowContext(ctx,
		`SELECT cpu_percent, ram_percent, disk_percent, sustained_minutes, tunnel_seen_connected FROM alert_settings WHERE id = 1`).
		Scan(&a.CPUPercent, &a.RAMPercent, &a.DiskPercent, &a.SustainedMinutes, &a.TunnelSeenConnected)
	return a, err
}

// SetAlertThresholds replaces the four thresholds and returns the settings as
// they were. TunnelSeenConnected in a is ignored (SetTunnelSeenConnected owns
// it). When nothing changed it writes nothing and changed is false. The values
// are validated by the API; the table's CHECKs are the backstop.
func (s *Store) SetAlertThresholds(ctx context.Context, a AlertSettings) (before AlertSettings, changed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AlertSettings{}, false, fmt.Errorf("store: set alert thresholds: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	before, err = readAlertSettings(ctx, tx)
	if err != nil {
		return AlertSettings{}, false, fmt.Errorf("store: set alert thresholds: %w", err)
	}
	a.TunnelSeenConnected = before.TunnelSeenConnected
	if before == a {
		return before, false, nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE alert_settings SET cpu_percent = ?, ram_percent = ?, disk_percent = ?, sustained_minutes = ? WHERE id = 1`,
		a.CPUPercent, a.RAMPercent, a.DiskPercent, a.SustainedMinutes); err != nil {
		return AlertSettings{}, false, fmt.Errorf("store: set alert thresholds: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AlertSettings{}, false, fmt.Errorf("store: set alert thresholds: %w", err)
	}
	return before, true, nil
}

// SetTunnelSeenConnected records whether the tunnel has been seen connected.
func (s *Store) SetTunnelSeenConnected(ctx context.Context, seen bool) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE alert_settings SET tunnel_seen_connected = ? WHERE id = 1`, seen); err != nil {
		return fmt.Errorf("store: set tunnel seen: %w", err)
	}
	return nil
}

// Alert channel kinds.
const (
	ChannelTelegram = "telegram"
	ChannelEmail    = "email"
)

// AlertChannel is one channel's stored configuration. SealedToken is the
// Telegram bot token sealed with secret.Seal (never the token itself); ChatID
// is Telegram's; Recipients are email's.
type AlertChannel struct {
	Kind        string
	Enabled     bool
	SealedToken string
	ChatID      string
	Recipients  []string
	UpdatedAt   time.Time
}

func scanAlertChannel(r rowScanner) (AlertChannel, error) {
	var (
		c          AlertChannel
		recipients string
		updated    string
	)
	if err := r.Scan(&c.Kind, &c.Enabled, &c.SealedToken, &c.ChatID, &recipients, &updated); err != nil {
		return AlertChannel{}, err
	}
	if recipients != "" {
		c.Recipients = strings.Split(recipients, "\n")
	}
	t, err := parseTime(updated)
	if err != nil {
		return AlertChannel{}, fmt.Errorf("alert channel updated_at: %w", err)
	}
	c.UpdatedAt = t
	return c, nil
}

const alertChannelColumns = `kind, enabled, sealed_token, chat_id, recipients, updated_at`

// AlertChannels returns the stored channels by kind; a kind never saved is
// absent.
func (s *Store) AlertChannels(ctx context.Context) (map[string]AlertChannel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+alertChannelColumns+` FROM alert_channels`)
	if err != nil {
		return nil, fmt.Errorf("store: list alert channels: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]AlertChannel{}
	for rows.Next() {
		c, err := scanAlertChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list alert channels: %w", err)
		}
		out[c.Kind] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list alert channels: %w", err)
	}
	return out, nil
}

// SetAlertChannel stores a channel (insert or replace) and returns it as it
// was; with no row yet, existed is false and before carries only Kind. When no
// field changed it writes nothing, so UpdatedAt stays, and changed is false.
// UpdatedAt in c is ignored.
func (s *Store) SetAlertChannel(ctx context.Context, c AlertChannel) (before AlertChannel, existed, changed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AlertChannel{}, false, false, fmt.Errorf("store: set alert channel: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	before, err = scanAlertChannel(tx.QueryRowContext(ctx, `SELECT `+alertChannelColumns+` FROM alert_channels WHERE kind = ?`, c.Kind))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		before = AlertChannel{Kind: c.Kind}
	case err != nil:
		return AlertChannel{}, false, false, fmt.Errorf("store: set alert channel: %w", err)
	default:
		existed = true
		if before.Enabled == c.Enabled && before.SealedToken == c.SealedToken && before.ChatID == c.ChatID &&
			strings.Join(before.Recipients, "\n") == strings.Join(c.Recipients, "\n") {
			return before, true, false, nil
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO alert_channels (kind, enabled, sealed_token, chat_id, recipients, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (kind) DO UPDATE SET enabled = excluded.enabled, sealed_token = excluded.sealed_token,
		   chat_id = excluded.chat_id, recipients = excluded.recipients, updated_at = excluded.updated_at`,
		c.Kind, c.Enabled, c.SealedToken, c.ChatID, strings.Join(c.Recipients, "\n"), formatTime(time.Now())); err != nil {
		return AlertChannel{}, false, false, fmt.Errorf("store: set alert channel: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AlertChannel{}, false, false, fmt.Errorf("store: set alert channel: %w", err)
	}
	return before, existed, true, nil
}

// Incident is a problem an alert was raised for and that has not cleared. Key
// is the alert key ("server:cpu", "project:<id>:error"); ProjectID is 0 for a
// server-wide one, and the incident goes with its project row. ActivityID is
// the alert's activity row.
type Incident struct {
	Key        string
	ProjectID  int64
	OpenedAt   time.Time
	ActivityID int64
}

// OpenIncidents lists every open incident, oldest first.
func (s *Store) OpenIncidents(ctx context.Context) ([]Incident, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, project_id, opened_at, activity_id FROM alert_incidents ORDER BY opened_at, key`)
	if err != nil {
		return nil, fmt.Errorf("store: list incidents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Incident{}
	for rows.Next() {
		var (
			in       Incident
			project  sql.NullInt64
			opened   string
			activity sql.NullInt64
		)
		if err := rows.Scan(&in.Key, &project, &opened, &activity); err != nil {
			return nil, fmt.Errorf("store: list incidents: %w", err)
		}
		in.ProjectID, in.ActivityID = project.Int64, activity.Int64
		if in.OpenedAt, err = parseTime(opened); err != nil {
			return nil, fmt.Errorf("store: incident %q opened_at: %w", in.Key, err)
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// OpenIncident stores an incident. An existing key is kept as it is (its
// OpenedAt and ActivityID stay).
func (s *Store) OpenIncident(ctx context.Context, in Incident) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO alert_incidents (key, project_id, opened_at, activity_id) VALUES (?, ?, ?, ?)`,
		in.Key, nullID(in.ProjectID), formatTime(in.OpenedAt), nullID(in.ActivityID)); err != nil {
		return fmt.Errorf("store: open incident: %w", err)
	}
	return nil
}

// CloseIncident removes an incident; an unknown key is not an error.
func (s *Store) CloseIncident(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alert_incidents WHERE key = ?`, key); err != nil {
		return fmt.Errorf("store: close incident: %w", err)
	}
	return nil
}
