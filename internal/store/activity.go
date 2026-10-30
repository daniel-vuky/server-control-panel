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
)

// What was done.
const (
	ActProjectStart    = "project.start"
	ActProjectStop     = "project.stop"
	ActProjectRestart  = "project.restart"
	ActScanStart       = "scan.start"
	ActScanTrack       = "scan.track"
	ActWorkspaceRescan = "workspace.rescan"
	ActWorkspaceRemove = "workspace.remove"
	ActProjectUntrack  = "project.untrack"
	ActProjectRemove   = "project.remove"
	ActProjectSettings = "project.settings"
)

// How it ended.
const (
	ResultRunning     = "running"
	ResultOK          = "ok"
	ResultFailed      = "failed"
	ResultTimedOut    = "timed_out"
	ResultRefused     = "refused"
	ResultInterrupted = "interrupted"
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
	_, err := s.db.ExecContext(ctx,
		`UPDATE activity SET finished_at = ?, result = ?, reason = ? WHERE id = ? AND result = 'running'`,
		formatTime(time.Now()), result, cleanText(reason, maxReason), id)
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

// RecentActivity returns up to limit records, newest first.
func (s *Store) RecentActivity(ctx context.Context, limit int) ([]ActivityRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, started_at, finished_at, actor, actor_kind, action, project_id, target, result, reason
		 FROM activity ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list activity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []ActivityRow{}
	for rows.Next() {
		var (
			r        ActivityRow
			started  string
			finished sql.NullString
			project  sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &started, &finished, &r.Actor, &r.ActorKind, &r.Action, &project, &r.Target, &r.Result, &r.Reason); err != nil {
			return nil, fmt.Errorf("store: list activity: %w", err)
		}
		if r.StartedAt, err = parseTime(started); err != nil {
			return nil, fmt.Errorf("store: activity %d started_at: %w", r.ID, err)
		}
		if finished.Valid {
			t, err := parseTime(finished.String)
			if err != nil {
				return nil, fmt.Errorf("store: activity %d finished_at: %w", r.ID, err)
			}
			r.FinishedAt = &t
		}
		r.ProjectID = project.Int64
		out = append(out, r)
	}
	return out, rows.Err()
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
