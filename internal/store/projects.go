package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

// Known is what the store remembers of a project path.
type Known struct {
	State          string // tracked | ignored
	ComposeProject string
}

// KnownProjects returns every stored project, tracked or ignored, keyed by its path.
func (s *Store) KnownProjects(ctx context.Context) (map[string]Known, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, state, compose_project FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("store: list known projects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	known := map[string]Known{}
	for rows.Next() {
		var path string
		var k Known
		if err := rows.Scan(&path, &k.State, &k.ComposeProject); err != nil {
			return nil, fmt.Errorf("store: list known projects: %w", err)
		}
		known[path] = k
	}
	return known, rows.Err()
}

// Tracked is a tracked project with the service names from its config and its
// databases.
type Tracked struct {
	ID, WorkspaceID int64
	DisplayName     string
	Path            string
	ComposeProject  string
	Self            bool
	Services        []string
	Databases       []project.Database
}

// TrackedProjects lists the tracked projects, oldest first.
func (s *Store) TrackedProjects(ctx context.Context) ([]Tracked, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, workspace_id, display_name, path, compose_project, self, details FROM projects WHERE state = 'tracked' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list tracked projects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ts := []Tracked{}
	byID := map[int64]int{}
	for rows.Next() {
		var (
			t       Tracked
			details string
		)
		if err := rows.Scan(&t.ID, &t.WorkspaceID, &t.DisplayName, &t.Path, &t.ComposeProject, &t.Self, &details); err != nil {
			return nil, fmt.Errorf("store: list tracked projects: %w", err)
		}
		var d project.Details
		if err := json.Unmarshal([]byte(details), &d); err != nil {
			return nil, fmt.Errorf("store: project %d details: %w", t.ID, err)
		}
		t.Services = make([]string, 0, len(d.Services))
		for _, sv := range d.Services {
			t.Services = append(t.Services, sv.Name)
		}
		t.Databases = []project.Database{}
		byID[t.ID] = len(ts)
		ts = append(ts, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()

	drows, err := s.db.QueryContext(ctx,
		`SELECT d.project_id, d.type, d.name, d.location_kind, d.location, d.sources
		 FROM databases d JOIN projects p ON p.id = d.project_id WHERE p.state = 'tracked' ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("store: list databases: %w", err)
	}
	defer func() { _ = drows.Close() }()
	for drows.Next() {
		var (
			pid     int64
			d       project.Database
			sources string
		)
		if err := drows.Scan(&pid, &d.Type, &d.Name, &d.LocationKind, &d.Location, &sources); err != nil {
			return nil, fmt.Errorf("store: list databases: %w", err)
		}
		if err := json.Unmarshal([]byte(sources), &d.Sources); err != nil {
			return nil, fmt.Errorf("store: database sources: %w", err)
		}
		if d.Sources == nil {
			d.Sources = []string{}
		}
		i := byID[pid]
		ts[i].Databases = append(ts[i].Databases, d)
	}
	return ts, drows.Err()
}
