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

// Workspace is a stored workspace with the number of its tracked and ignored projects.
type Workspace struct {
	ID         int64
	Name, Path string
	Depth      int
	LastScanAt time.Time
	Tracked    int
	Ignored    int
}

const workspaceSelect = `SELECT w.id, w.name, w.path, w.depth, w.last_scan_at,
	(SELECT count(*) FROM projects p WHERE p.workspace_id = w.id AND p.state = 'tracked'),
	(SELECT count(*) FROM projects p WHERE p.workspace_id = w.id AND p.state = 'ignored')
	FROM workspaces w`

type rowScanner interface{ Scan(dest ...any) error }

func scanWorkspace(r rowScanner) (Workspace, error) {
	var (
		w    Workspace
		last string
	)
	if err := r.Scan(&w.ID, &w.Name, &w.Path, &w.Depth, &last, &w.Tracked, &w.Ignored); err != nil {
		return Workspace{}, err
	}
	t, err := parseTime(last)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace %d last_scan_at: %w", w.ID, err)
	}
	w.LastScanAt = t
	return w, nil
}

// WorkspaceByPath returns the workspace stored at path, or ErrNotFound.
func (s *Store) WorkspaceByPath(ctx context.Context, path string) (Workspace, error) {
	w, err := scanWorkspace(s.db.QueryRowContext(ctx, workspaceSelect+` WHERE w.path = ?`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("store: read workspace: %w", err)
	}
	return w, nil
}

// Workspaces lists every workspace, oldest first.
func (s *Store) Workspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, workspaceSelect+` ORDER BY w.id`)
	if err != nil {
		return nil, fmt.Errorf("store: list workspaces: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ws := []Workspace{}
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list workspaces: %w", err)
		}
		ws = append(ws, w)
	}
	return ws, rows.Err()
}

// NewWorkspace is a workspace to store.
type NewWorkspace struct {
	Name, Path string
	Depth      int
	LastScanAt time.Time
}

// NewProject is a project found by a scan, to be stored as tracked or ignored.
type NewProject struct {
	Path           string
	ComposeFiles   []string
	ComposeProject string
	DisplayName    string
	Tracked, Self  bool
	Details        project.Details
	Databases      []project.Database // stored only when Tracked
}

// AddWorkspace stores a workspace and its projects in one transaction. It
// returns the workspace id and the number of tracked projects added, and
// ErrAlreadyAdded when a workspace with that path exists.
func (s *Store) AddWorkspace(ctx context.Context, w NewWorkspace, ps []NewProject) (workspaceID int64, added int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("store: add workspace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := formatTime(time.Now())
	res, err := tx.ExecContext(ctx,
		`INSERT INTO workspaces (name, path, depth, created_at, last_scan_at) VALUES (?, ?, ?, ?, ?)`,
		w.Name, w.Path, w.Depth, now, formatTime(w.LastScanAt))
	if err != nil {
		if isUnique(err, "workspaces.path") {
			return 0, 0, ErrAlreadyAdded
		}
		return 0, 0, fmt.Errorf("store: add workspace: %w", err)
	}
	workspaceID, err = res.LastInsertId()
	if err != nil {
		return 0, 0, fmt.Errorf("store: add workspace: %w", err)
	}

	for _, p := range ps {
		files, err := json.Marshal(nonNil(p.ComposeFiles))
		if err != nil {
			return 0, 0, err
		}
		details, err := json.Marshal(p.Details)
		if err != nil {
			return 0, 0, err
		}
		state := "ignored"
		if p.Tracked {
			state = "tracked"
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO projects (workspace_id, path, compose_files, compose_project, display_name, state, self, details, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			workspaceID, p.Path, string(files), p.ComposeProject, p.DisplayName, state, p.Self, string(details), now)
		if err != nil {
			return 0, 0, fmt.Errorf("store: add project %s: %w", p.Path, err)
		}
		if !p.Tracked {
			continue
		}
		added++
		projectID, err := res.LastInsertId()
		if err != nil {
			return 0, 0, err
		}
		for _, d := range p.Databases {
			sources, err := json.Marshal(nonNil(d.Sources))
			if err != nil {
				return 0, 0, err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO databases (project_id, type, name, location_kind, location, sources) VALUES (?, ?, ?, ?, ?, ?)`,
				projectID, d.Type, d.Name, d.LocationKind, d.Location, string(sources)); err != nil {
				return 0, 0, fmt.Errorf("store: add database of %s: %w", p.Path, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("store: add workspace: %w", err)
	}
	return workspaceID, added, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
