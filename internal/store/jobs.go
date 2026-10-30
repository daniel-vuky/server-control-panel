package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Job is a background job. Progress and Result are nil until set.
type Job struct {
	ID     int64
	Kind   string // scan
	Target string // the path being scanned, or "all workspaces" for a rescan of several
	Depth  int    // 0 for a rescan
	// Rescan holds the workspace ids of a rescan job, in scan order; nil for
	// a first-time scan.
	Rescan     []int64
	State      string // running | done | failed | interrupted
	Error      string
	StartedBy  string
	Progress   json.RawMessage
	Result     json.RawMessage
	StartedAt  time.Time
	FinishedAt *time.Time
}

// CreateScanJob stores a new running scan job and returns its id.
func (s *Store) CreateScanJob(ctx context.Context, target string, depth int, by string) (int64, error) {
	params, err := json.Marshal(struct {
		Depth int `json:"depth"`
	}{depth})
	if err != nil {
		return 0, err
	}
	return s.insertScanJob(ctx, target, string(params), by)
}

// CreateRescanJob stores a new running rescan of the given workspaces and
// returns its id. A rescan is a scan job (the jobs.kind CHECK allows only
// 'scan') whose params are {"depth":0,"rescan":[<workspace ids>]}.
func (s *Store) CreateRescanJob(ctx context.Context, target string, workspaceIDs []int64, by string) (int64, error) {
	params, err := json.Marshal(struct {
		Depth  int     `json:"depth"`
		Rescan []int64 `json:"rescan"`
	}{0, nonNil(workspaceIDs)})
	if err != nil {
		return 0, err
	}
	return s.insertScanJob(ctx, target, string(params), by)
}

func (s *Store) insertScanJob(ctx context.Context, target, params, by string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (kind, target, params, state, started_by, started_at) VALUES ('scan', ?, ?, 'running', ?, ?)`,
		target, params, by, formatTime(time.Now()))
	if err != nil {
		return 0, fmt.Errorf("store: create job: %w", err)
	}
	return res.LastInsertId()
}

// FinishJob sets the final state of a job (done, failed or interrupted) with
// its last progress, its result and an error message. A nil progress or result,
// or an empty message, is stored as NULL.
func (s *Store) FinishJob(ctx context.Context, id int64, state string, progress, result json.RawMessage, errMsg string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = ?, progress = ?, result = ?, error = ?, finished_at = ? WHERE id = ?`,
		state, nullBytes(progress), nullBytes(result), nullString(errMsg), formatTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("store: finish job: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: finish job: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Job returns one job, or ErrNotFound.
func (s *Store) Job(ctx context.Context, id int64) (Job, error) {
	var (
		j                        Job
		params                   string
		progress, result, errMsg sql.NullString
		started                  string
		finished                 sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, kind, target, params, state, progress, result, error, started_by, started_at, finished_at FROM jobs WHERE id = ?`, id).
		Scan(&j.ID, &j.Kind, &j.Target, &params, &j.State, &progress, &result, &errMsg, &j.StartedBy, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("store: read job: %w", err)
	}
	var p struct {
		Depth  int     `json:"depth"`
		Rescan []int64 `json:"rescan"`
	}
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return Job{}, fmt.Errorf("store: job %d params: %w", id, err)
	}
	j.Depth, j.Rescan = p.Depth, p.Rescan
	if progress.Valid {
		j.Progress = json.RawMessage(progress.String)
	}
	if result.Valid {
		j.Result = json.RawMessage(result.String)
	}
	j.Error = errMsg.String
	if j.StartedAt, err = parseTime(started); err != nil {
		return Job{}, fmt.Errorf("store: job %d started_at: %w", id, err)
	}
	if finished.Valid {
		t, err := parseTime(finished.String)
		if err != nil {
			return Job{}, fmt.Errorf("store: job %d finished_at: %w", id, err)
		}
		j.FinishedAt = &t
	}
	return j, nil
}

// interruptRunningJobs marks jobs a previous process left running as
// interrupted (ADR 0007).
func (s *Store) interruptRunningJobs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = 'interrupted', error = 'The panel restarted while this job was running.', finished_at = ? WHERE state = 'running'`,
		formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("store: interrupt running jobs: %w", err)
	}
	return nil
}

func nullBytes(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
