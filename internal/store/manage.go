package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

var (
	// ErrWorkspaceGone is returned when a workspace was removed meanwhile.
	ErrWorkspaceGone = errors.New("store: workspace removed")
	// ErrAddedMeanwhile is returned when a project path is already tracked.
	ErrAddedMeanwhile = errors.New("store: project already tracked")
	// ErrAlreadyTracked is returned when a rescan job's projects were added already.
	ErrAlreadyTracked = errors.New("store: rescan already tracked")
)

// WorkspaceByID returns one workspace, or ErrNotFound.
func (s *Store) WorkspaceByID(ctx context.Context, id int64) (Workspace, error) {
	w, err := scanWorkspace(s.db.QueryRowContext(ctx, workspaceSelect+` WHERE w.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("store: read workspace: %w", err)
	}
	return w, nil
}

// RemoveWorkspace deletes a workspace; its projects and databases go with it
// (ON DELETE CASCADE) and activity rows stay. It returns the workspace as it
// was, or ErrNotFound.
func (s *Store) RemoveWorkspace(ctx context.Context, id int64) (Workspace, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, fmt.Errorf("store: remove workspace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	w, err := scanWorkspace(tx.QueryRowContext(ctx, workspaceSelect+` WHERE w.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("store: remove workspace: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, id); err != nil {
		return Workspace{}, fmt.Errorf("store: remove workspace: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Workspace{}, fmt.Errorf("store: remove workspace: %w", err)
	}
	return w, nil
}

// UntrackProject stops tracking a project and returns it as it was. A normal
// tracked project becomes ignored, its databases dropped; a project that a
// rescan could not find (missing_since set) is deleted, so a gone folder does
// not linger as an ignored row. An unknown or ignored id gives ErrNotFound.
func (s *Store) UntrackProject(ctx context.Context, id int64) (Tracked, error) {
	if id <= 0 { // 0 would list every tracked project in trackedIn
		return Tracked{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Tracked{}, fmt.Errorf("store: untrack project: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ts, err := trackedIn(ctx, tx, id)
	if err != nil {
		return Tracked{}, err
	}
	if len(ts) == 0 {
		return Tracked{}, ErrNotFound
	}
	t := ts[0]
	if t.MissingSince != nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id) // databases cascade
	} else {
		if _, err = tx.ExecContext(ctx, `DELETE FROM databases WHERE project_id = ?`, id); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE projects SET state = 'ignored', missing_since = NULL WHERE id = ?`, id)
		}
	}
	if err != nil {
		return Tracked{}, fmt.Errorf("store: untrack project: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Tracked{}, fmt.Errorf("store: untrack project: %w", err)
	}
	return t, nil
}

// RescanApply is what a finished rescan writes back, per workspace.
type RescanApply struct {
	Workspaces []WorkspaceApply
}

// WorkspaceApply is the rescan outcome of one workspace.
type WorkspaceApply struct {
	ID         int64
	LastScanAt time.Time
	Refresh    []ProjectRefresh
	Found      []string // tracked paths seen again: missing_since is cleared
	Missing    []string // tracked paths not seen: missing_since is set when NULL
}

// ProjectRefresh updates a tracked project found again. A nil Config or Self
// keeps what is stored.
type ProjectRefresh struct {
	Path   string
	Config *ConfigRefresh
	Self   *bool
}

// ConfigRefresh is the compose-derived part of a project.
type ConfigRefresh struct {
	ComposeFiles   []string
	ComposeProject string
	Details        project.Details
	Databases      []project.Database
}

// ApplyRescan writes a rescan in one transaction. A workspace that no longer
// exists is skipped, and only rows still state = 'tracked' are touched (an
// untrack during the scan wins). display_name is never changed.
func (s *Store) ApplyRescan(ctx context.Context, r RescanApply, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: apply rescan: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, w := range r.Workspaces {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workspaces WHERE id = ?`, w.ID).Scan(&exists); err != nil {
			return fmt.Errorf("store: apply rescan: %w", err)
		}
		if exists == 0 {
			continue
		}
		for _, p := range w.Refresh {
			if err := refreshProject(ctx, tx, p); err != nil {
				return err
			}
		}
		// Found is matched by path in any workspace: a project seen by any
		// scanned workspace is present. Missing stays scoped to the reporter.
		for _, path := range w.Found {
			if _, err := tx.ExecContext(ctx,
				`UPDATE projects SET missing_since = NULL WHERE path = ? AND state = 'tracked'`, path); err != nil {
				return fmt.Errorf("store: apply rescan: %w", err)
			}
		}
		for _, path := range w.Missing {
			if _, err := tx.ExecContext(ctx,
				`UPDATE projects SET missing_since = ? WHERE workspace_id = ? AND path = ? AND state = 'tracked' AND missing_since IS NULL`,
				formatTime(now), w.ID, path); err != nil {
				return fmt.Errorf("store: apply rescan: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET last_scan_at = ? WHERE id = ?`, formatTime(w.LastScanAt), w.ID); err != nil {
			return fmt.Errorf("store: apply rescan: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: apply rescan: %w", err)
	}
	return nil
}

func refreshProject(ctx context.Context, tx *sql.Tx, p ProjectRefresh) error {
	var id int64
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE path = ? AND state = 'tracked'`, p.Path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // untracked or removed meanwhile
	}
	if err != nil {
		return fmt.Errorf("store: apply rescan: %w", err)
	}
	// A refreshed project was seen, so it is present.
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET missing_since = NULL WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: apply rescan: %w", err)
	}
	if p.Self != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET self = ? WHERE id = ?`, *p.Self, id); err != nil {
			return fmt.Errorf("store: apply rescan: %w", err)
		}
	}
	c := p.Config
	if c == nil {
		return nil
	}
	files, details, err := encodeConfig(c.ComposeFiles, c.Details)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET compose_files = ?, compose_project = ?, details = ? WHERE id = ?`,
		files, c.ComposeProject, details, id); err != nil {
		return fmt.Errorf("store: apply rescan: %w", err)
	}
	return replaceDatabases(ctx, tx, id, c.Databases)
}

func encodeConfig(files []string, d project.Details) (filesJSON, detailsJSON string, err error) {
	f, err := json.Marshal(nonNil(files))
	if err != nil {
		return "", "", err
	}
	dj, err := json.Marshal(normalizeDetails(d))
	if err != nil {
		return "", "", err
	}
	return string(f), string(dj), nil
}

// insertProject stores a new project of workspaceID with a NULL missing_since,
// and its databases when it is tracked. It returns the project id.
func insertProject(ctx context.Context, tx *sql.Tx, workspaceID int64, p NewProject, now string) (int64, error) {
	files, details, err := encodeConfig(p.ComposeFiles, p.Details)
	if err != nil {
		return 0, err
	}
	state := "ignored"
	if p.Tracked {
		state = "tracked"
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO projects (workspace_id, path, compose_files, compose_project, display_name, state, self, details, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		workspaceID, p.Path, files, p.ComposeProject, p.DisplayName, state, p.Self, details, now)
	if err != nil {
		return 0, fmt.Errorf("store: add project %s: %w", p.Path, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if p.Tracked {
		if err := replaceDatabases(ctx, tx, id, p.Databases); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func replaceDatabases(ctx context.Context, tx *sql.Tx, projectID int64, dbs []project.Database) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM databases WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("store: replace databases: %w", err)
	}
	for _, d := range dbs {
		sources, err := json.Marshal(nonNil(d.Sources))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO databases (project_id, type, name, location_kind, location, sources) VALUES (?, ?, ?, ?, ?, ?)`,
			projectID, d.Type, d.Name, d.LocationKind, d.Location, string(sources)); err != nil {
			return fmt.Errorf("store: replace databases: %w", err)
		}
	}
	return nil
}

// AddItem is a project a rescan found, for an existing workspace.
type AddItem struct {
	WorkspaceID int64
	NewProject
}

// AddProjects stores the projects a rescan found, in one transaction, and
// marks the job as tracked ("tracked":true in its params) in that same
// transaction so a second track is refused with ErrAlreadyTracked. An ignored
// path is updated in place (re-tracked or kept ignored); a path stored as
// tracked gives ErrAddedMeanwhile and a removed workspace ErrWorkspaceGone. It
// returns the number of tracked projects added. Any error leaves nothing
// written, the marker included.
func (s *Store) AddProjects(ctx context.Context, jobID int64, items []AddItem) (added int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: add projects: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var params string
	if err := tx.QueryRowContext(ctx, `SELECT params FROM jobs WHERE id = ?`, jobID).Scan(&params); errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, fmt.Errorf("store: add projects: %w", err)
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(params), &fields); err != nil {
		return 0, fmt.Errorf("store: job %d params: %w", jobID, err)
	}
	if string(fields["tracked"]) == "true" {
		return 0, ErrAlreadyTracked
	}
	fields["tracked"] = json.RawMessage("true")
	marked, err := json.Marshal(fields)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET params = ? WHERE id = ?`, string(marked), jobID); err != nil {
		return 0, fmt.Errorf("store: add projects: %w", err)
	}

	now := formatTime(time.Now())
	for _, it := range items {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workspaces WHERE id = ?`, it.WorkspaceID).Scan(&exists); err != nil {
			return 0, fmt.Errorf("store: add projects: %w", err)
		}
		if exists == 0 {
			return 0, ErrWorkspaceGone
		}
		var (
			id       int64
			oldState string
		)
		err := tx.QueryRowContext(ctx, `SELECT id, state FROM projects WHERE path = ?`, it.Path).Scan(&id, &oldState)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := insertProject(ctx, tx, it.WorkspaceID, it.NewProject, now); err != nil {
				return 0, err
			}
		case err != nil:
			return 0, fmt.Errorf("store: add projects: %w", err)
		case oldState == "tracked":
			return 0, ErrAddedMeanwhile
		default:
			files, details, err := encodeConfig(it.ComposeFiles, it.Details)
			if err != nil {
				return 0, err
			}
			state := "ignored"
			if it.Tracked {
				state = "tracked"
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE projects SET compose_files = ?, compose_project = ?, display_name = ?, state = ?, self = ?, details = ?, missing_since = NULL WHERE id = ?`,
				files, it.ComposeProject, it.DisplayName, state, it.Self, details, id); err != nil {
				return 0, fmt.Errorf("store: add project %s: %w", it.Path, err)
			}
			var dbs []project.Database
			if it.Tracked {
				dbs = it.Databases
			}
			if err := replaceDatabases(ctx, tx, id, dbs); err != nil {
				return 0, err
			}
		}
		if it.Tracked {
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: add projects: %w", err)
	}
	return added, nil
}
