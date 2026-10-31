package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
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
	ComposeFiles    []string // base names: the main file, then the override
	Services        []string
	// ServiceDetails is the services with their image, ports and volumes, from
	// the same config as Services; [] when the config could not be read.
	ServiceDetails []project.Service
	Databases      []project.Database
	// MissingSince is set when a rescan could not find the folder; nil when
	// the project is present.
	MissingSince *time.Time
	// PublicDomain is empty when unset. StopTemplate is "stop" or "down". A nil
	// alert threshold means the server default.
	PublicDomain    string
	StopTemplate    string
	AlertCPUPercent *int
	AlertRAMMB      *int
}

// ComposeValues are the runner values that name the project's compose files:
// dir, file and, when the project has an override file, override. ok is false
// when the project has no compose file.
func (t Tracked) ComposeValues() (v runner.Values, override, ok bool) {
	if len(t.ComposeFiles) == 0 {
		return nil, false, false
	}
	v = runner.Values{"dir": t.Path, "file": filepath.Join(t.Path, t.ComposeFiles[0])}
	if len(t.ComposeFiles) > 1 {
		v["override"] = filepath.Join(t.Path, t.ComposeFiles[1])
		override = true
	}
	return v, override, true
}

// TrackedProjects lists the tracked projects, oldest first.
func (s *Store) TrackedProjects(ctx context.Context) ([]Tracked, error) {
	return s.tracked(ctx, 0)
}

// TrackedProject returns one tracked project, or ErrNotFound when the id is
// unknown or the project is ignored.
func (s *Store) TrackedProject(ctx context.Context, id int64) (Tracked, error) {
	if id <= 0 { // 0 means "all" to tracked
		return Tracked{}, ErrNotFound
	}
	ts, err := s.tracked(ctx, id)
	if err != nil {
		return Tracked{}, err
	}
	if len(ts) == 0 {
		return Tracked{}, ErrNotFound
	}
	return ts[0], nil
}

// tracked lists the tracked projects with their databases; a non-zero only
// limits it to that project id.
func (s *Store) tracked(ctx context.Context, only int64) ([]Tracked, error) {
	// One transaction, so both reads see the same snapshot: a concurrent
	// AddWorkspace cannot show up in the second query but not the first.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("store: list tracked projects: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return trackedIn(ctx, tx, only)
}

// queryer is what a transaction offers for reads.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// trackedIn is tracked on a transaction the caller owns.
func trackedIn(ctx context.Context, tx queryer, only int64) ([]Tracked, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, workspace_id, display_name, path, compose_project, self, compose_files, details, missing_since,
		        public_domain, stop_template, alert_cpu_percent, alert_ram_mb FROM projects
		 WHERE state = 'tracked' AND (? = 0 OR id = ?) ORDER BY id`, only, only)
	if err != nil {
		return nil, fmt.Errorf("store: list tracked projects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ts := []Tracked{}
	byID := map[int64]int{}
	for rows.Next() {
		var (
			t       Tracked
			files   string
			details string
			missing sql.NullString
			domain  sql.NullString
			cpu     sql.NullInt64
			ram     sql.NullInt64
		)
		if err := rows.Scan(&t.ID, &t.WorkspaceID, &t.DisplayName, &t.Path, &t.ComposeProject, &t.Self, &files, &details, &missing,
			&domain, &t.StopTemplate, &cpu, &ram); err != nil {
			return nil, fmt.Errorf("store: list tracked projects: %w", err)
		}
		t.PublicDomain = domain.String
		if cpu.Valid {
			v := int(cpu.Int64)
			t.AlertCPUPercent = &v
		}
		if ram.Valid {
			v := int(ram.Int64)
			t.AlertRAMMB = &v
		}
		if missing.Valid {
			m, err := parseTime(missing.String)
			if err != nil {
				return nil, fmt.Errorf("store: project %d missing_since: %w", t.ID, err)
			}
			t.MissingSince = &m
		}
		if err := json.Unmarshal([]byte(files), &t.ComposeFiles); err != nil {
			return nil, fmt.Errorf("store: project %d compose files: %w", t.ID, err)
		}
		if t.ComposeFiles == nil {
			t.ComposeFiles = []string{}
		}
		var d project.Details
		if err := json.Unmarshal([]byte(details), &d); err != nil {
			return nil, fmt.Errorf("store: project %d details: %w", t.ID, err)
		}
		t.ServiceDetails = d.Services
		if t.ServiceDetails == nil {
			t.ServiceDetails = []project.Service{}
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

	drows, err := tx.QueryContext(ctx,
		`SELECT d.project_id, d.type, d.name, d.name_explicit, d.location_kind, d.location, d.sources
		 FROM databases d JOIN projects p ON p.id = d.project_id WHERE p.state = 'tracked' AND (? = 0 OR p.id = ?) ORDER BY d.id`, only, only)
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
		if err := drows.Scan(&pid, &d.Type, &d.Name, &d.NameExplicit, &d.LocationKind, &d.Location, &sources); err != nil {
			return nil, fmt.Errorf("store: list databases: %w", err)
		}
		if err := json.Unmarshal([]byte(sources), &d.Sources); err != nil {
			return nil, fmt.Errorf("store: database sources: %w", err)
		}
		if d.Sources == nil {
			d.Sources = []string{}
		}
		i, ok := byID[pid]
		if !ok {
			continue
		}
		ts[i].Databases = append(ts[i].Databases, d)
	}
	return ts, drows.Err()
}
