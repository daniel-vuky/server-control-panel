package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Who did it.
const (
	ActorPerson   = "person"
	ActorSchedule = "schedule"
	ActorSystem   = "system"
)

// What was done.
const (
	ActProjectStart     = "project.start"
	ActProjectStop      = "project.stop"
	ActProjectRestart   = "project.restart"
	ActScanStart        = "scan.start"
	ActScanTrack        = "scan.track"
	ActWorkspaceRescan  = "workspace.rescan"
	ActWorkspaceRemove  = "workspace.remove"
	ActProjectUntrack   = "project.untrack"
	ActProjectRemove    = "project.remove"
	ActProjectSettings  = "project.settings"
	ActBackupRun        = "backup.run"
	ActDriveConnect     = "drive.connect"
	ActDriveFolder      = "drive.folder"
	ActDriveDisconnect  = "drive.disconnect"
	ActBackupSchedule   = "backup.schedule"
	ActBackupDefaults   = "backup.defaults"
	ActBackupAll        = "backup.all"
	ActMetricsRetention = "metrics.retention"

	// Alert settings are actions of a person; the alert.* ones are the system's.
	ActAlertsChannel    = "alerts.channel"
	ActAlertsThresholds = "alerts.thresholds"
	ActAlertProject     = "alert.project"
	ActAlertDomain      = "alert.domain"
	ActAlertBackup      = "alert.backup"
	ActAlertResource    = "alert.resource"
	ActAlertTunnel      = "alert.tunnel"
	ActAlertRecovery    = "alert.recovery"
)

// How it ended.
const (
	ResultRunning     = "running"
	ResultOK          = "ok"
	ResultFailed      = "failed"
	ResultTimedOut    = "timed_out"
	ResultRefused     = "refused"
	ResultInterrupted = "interrupted"
	ResultSent        = "sent"     // an alert reached at least one channel
	ResultNotSent     = "not_sent" // an alert reached none
)

const (
	maxTarget = 256
	maxReason = 1024

	interruptedActivityReason = "The panel restarted while this action was running."
)

// Activity is who did what to what. ProjectID 0 means none and is stored NULL.
type Activity struct {
	Actor, ActorKind, Action string
	ProjectID                int64
	Target                   string
}

// ActivityRow is a stored activity record.
type ActivityRow struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt *time.Time
	Activity
	Result, Reason string
	Channels       []string // the channels an alert went to; nil for other rows
}

// ActivityFilter selects rows for ListActivity. Kind is "" (everything),
// "action" (not a backup or an alert), "backup" (backup.run, backup.all) or
// "alert" (alert.*; alerts.channel and alerts.thresholds are actions).
// ProjectID 0 means any project, Before 0 means from the newest, and Limit is
// the page size.
type ActivityFilter struct {
	Kind      string
	ProjectID int64
	Before    int64
	Limit     int
}

// ActivityEntry is a listed row with whether its project is still tracked, so
// its name can link to a page that exists.
type ActivityEntry struct {
	ActivityRow
	ProjectTracked bool
}

// The activity record is append-only: the only writes are an insert, and the
// one running -> final update in FinishActivity. There is no delete.

// StartActivity stores a running record and returns its id.
func (s *Store) StartActivity(ctx context.Context, a Activity) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO activity (started_at, actor, actor_kind, action, project_id, target, result) VALUES (?, ?, ?, ?, ?, ?, 'running')`,
		formatTime(time.Now()), a.Actor, a.ActorKind, a.Action, nullID(a.ProjectID), cleanTarget(a.Target))
	if err != nil {
		return 0, fmt.Errorf("store: start activity: %w", err)
	}
	return res.LastInsertId()
}

// FinishActivity sets the result and reason of a running record, once. A record
// that is not running (finished already, or unknown) is left as it is and the
// call returns nil.
func (s *Store) FinishActivity(ctx context.Context, id int64, result, reason string) error {
	return s.FinishActivityWith(ctx, id, result, reason, nil)
}

// FinishActivityWith is FinishActivity that also stores the channels an alert
// went to.
func (s *Store) FinishActivityWith(ctx context.Context, id int64, result, reason string, channels []string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE activity SET finished_at = ?, result = ?, reason = ?, channels = ? WHERE id = ? AND result = 'running'`,
		formatTime(time.Now()), result, cleanText(reason, maxReason), strings.Join(channels, ","), id)
	if err != nil {
		return fmt.Errorf("store: finish activity: %w", err)
	}
	return nil
}

// RecordActivity stores one finished record, for actions that were refused and
// so never ran.
func (s *Store) RecordActivity(ctx context.Context, a Activity, result, reason string) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO activity (started_at, finished_at, actor, actor_kind, action, project_id, target, result, reason) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, a.Actor, a.ActorKind, a.Action, nullID(a.ProjectID), cleanTarget(a.Target), result, cleanText(reason, maxReason))
	if err != nil {
		return fmt.Errorf("store: record activity: %w", err)
	}
	return nil
}

const activityColumns = `a.id, a.started_at, a.finished_at, a.actor, a.actor_kind, a.action, a.project_id, a.target, a.result, a.reason, a.channels`

// scanActivity reads activityColumns, then any extra columns into extra.
func scanActivity(r rowScanner, extra ...any) (ActivityRow, error) {
	var (
		row      ActivityRow
		started  string
		finished sql.NullString
		project  sql.NullInt64
		channels string
	)
	if err := r.Scan(append([]any{&row.ID, &started, &finished, &row.Actor, &row.ActorKind, &row.Action, &project, &row.Target, &row.Result, &row.Reason, &channels}, extra...)...); err != nil {
		return ActivityRow{}, fmt.Errorf("store: list activity: %w", err)
	}
	var err error
	if row.StartedAt, err = parseTime(started); err != nil {
		return ActivityRow{}, fmt.Errorf("store: activity %d started_at: %w", row.ID, err)
	}
	if finished.Valid {
		t, err := parseTime(finished.String)
		if err != nil {
			return ActivityRow{}, fmt.Errorf("store: activity %d finished_at: %w", row.ID, err)
		}
		row.FinishedAt = &t
	}
	row.ProjectID = project.Int64
	if channels != "" {
		row.Channels = strings.Split(channels, ",")
	}
	return row, nil
}

// RecentActivity returns up to limit records, newest first.
func (s *Store) RecentActivity(ctx context.Context, limit int) ([]ActivityRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+activityColumns+` FROM activity a ORDER BY a.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list activity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []ActivityRow{}
	for rows.Next() {
		r, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListActivity returns one page of records, newest first by id, and whether
// older ones match. An unknown Kind matches everything.
func (s *Store) ListActivity(ctx context.Context, f ActivityFilter) ([]ActivityEntry, bool, error) {
	kind := f.Kind
	if kind != "action" && kind != "backup" && kind != "alert" {
		kind = ""
	}
	// substr, not LIKE 'alert%': alerts.channel and alerts.thresholds are
	// actions, not alerts.
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+activityColumns+`, p.id IS NOT NULL
		 FROM activity a LEFT JOIN projects p ON p.id = a.project_id AND p.state = 'tracked'
		 WHERE (? = '' OR
		        (? = 'action' AND a.action NOT IN ('backup.run', 'backup.all') AND substr(a.action, 1, 6) <> 'alert.') OR
		        (? = 'backup' AND a.action IN ('backup.run', 'backup.all')) OR
		        (? = 'alert' AND substr(a.action, 1, 6) = 'alert.'))
		   AND (? = 0 OR a.project_id = ?)
		   AND (? = 0 OR a.id < ?)
		 ORDER BY a.id DESC LIMIT ?`,
		kind, kind, kind, kind, f.ProjectID, f.ProjectID, f.Before, f.Before, f.Limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("store: list activity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []ActivityEntry{}
	for rows.Next() {
		var tracked bool
		r, err := scanActivity(rows, &tracked)
		if err != nil {
			return nil, false, err
		}
		out = append(out, ActivityEntry{ActivityRow: r, ProjectTracked: tracked})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("store: list activity: %w", err)
	}
	more := len(out) > f.Limit
	if more {
		out = out[:f.Limit]
	}
	return out, more, nil
}

// interruptRunningActivity marks records a previous process left running as
// interrupted, next to interruptRunningJobs.
func (s *Store) interruptRunningActivity(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE activity SET result = 'interrupted', reason = ?, finished_at = ? WHERE result = 'running'`,
		interruptedActivityReason, formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("store: interrupt running activity: %w", err)
	}
	return nil
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// cleanTarget cleans a target, which may be a raw request path: no control
// character at all (newline and tab included), at most 256 bytes.
func cleanTarget(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return -1
		}
		return r
	}, s)
	return cleanText(s, maxTarget)
}

// cleanText drops invalid UTF-8, NUL and every control character except
// newline and tab, then cuts the text to max bytes at a rune boundary.
func cleanText(s string, limit int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
