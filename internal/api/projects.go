package api

import (
	"context"
	"net/http"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// listProjects is GET /api/projects: the tracked projects, each with its
// effective status read live from one docker.containers call (or its
// in-flight status while a command runs on it) and the actions it allows.
// When Docker cannot be read, every other status is unknown, a project
// allows only untrack, and the list is still returned. It runs no inspect or
// stats, so metrics, uptime, restart counts and health are left empty.
func (s *server) listProjects(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Store.TrackedProjects(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toProjects(s.readViews(r.Context(), ts)))
}

// readViews gives ts their views from one docker.containers read of its own
// (not the hub's cache), with no inspect or stats data, and the Drive status
// from the Drive service's cache.
func (s *server) readViews(ctx context.Context, ts []store.Tracked) []control.ProjectView {
	var containers []project.Container
	var cerr error
	if len(ts) > 0 {
		if containers, cerr = scan.ReadContainers(ctx, s.Docker); cerr != nil {
			s.log.Warn("projects: cannot read containers; every status is unknown", "err", cerr)
		}
	}
	return control.Views(ts, containers, cerr, cerr == nil, s.Control.Lock, nil, nil, s.Drive.Status())
}

// toProjects maps project views to the API type, shared by GET /api/projects
// and the state event. Each schedule's next run is computed here, with the
// server zone and the clock (the hub has neither). Slices are never nil.
func (s *server) toProjects(vs []control.ProjectView) []Project {
	now := s.Now()
	out := make([]Project, 0, len(vs))
	for _, v := range vs {
		actions := make([]string, 0, len(v.Actions))
		for _, a := range v.Actions {
			actions = append(actions, string(a))
		}
		p := Project{
			ID:          v.ID,
			DisplayName: v.DisplayName,
			Path:        v.Path,
			WorkspaceID: v.WorkspaceID,
			Self:        v.Self,
			Status:      string(v.Status),
			Actions:     actions,
			Databases:   s.toProjectDatabases(v.DatabaseViews, now),

			MissingSince:    formatTime(v.MissingSince),
			PublicDomain:    v.PublicDomain,
			StopTemplate:    v.StopTemplate,
			AlertCPUPercent: v.AlertCPUPercent,
			AlertRAMMB:      v.AlertRAMMB,
			ComposeFiles:    nonNil(v.ComposeFiles),
			CPUPercent:      v.CPUPercent,
			MemBytes:        v.MemBytes,
			UpSince:         formatTime(v.UpSince),
			Containers:      toContainers(v.Components),
			DriveFolder:     backup.ProjectFolder(v.DisplayName),
		}
		out = append(out, p)
	}
	return out
}

// toProjectDatabases maps a project's databases with their backup rules and
// schedules. The slice is never nil.
func (s *server) toProjectDatabases(ds []control.DatabaseView, now time.Time) []Database {
	out := make([]Database, 0, len(ds))
	for _, d := range ds {
		db := toDatabase(d.Database)
		db.Key = d.Key
		db.Backup = &BackupRule{Allowed: d.Backup.Allowed, Reason: d.Backup.Reason, Service: d.Backup.Service}
		if d.Schedule != nil {
			db.Schedule = s.toSchedule(*d.Schedule, now)
		}
		out = append(out, db)
	}
	return out
}

// toSchedule maps a stored schedule with its next run: the first occurrence
// after max(now, updated_at) in the server zone (so a time already passed
// when it was saved is not shown), null when off.
func (s *server) toSchedule(sc store.Schedule, now time.Time) *DatabaseSchedule {
	out := &DatabaseSchedule{
		Frequency: sc.Frequency, Time: sc.Time, Weekday: sc.Weekday, Keep: sc.Keep, Compress: sc.Compress,
	}
	if next, ok := s.nextRun(sc, now); ok {
		v := next.In(s.Zone).Format(time.RFC3339)
		out.NextRunAt = &v
	}
	return out
}

// nextRun is the schedule's next run after max(now, updated_at), ok false
// when it is off.
func (s *server) nextRun(sc store.Schedule, now time.Time) (time.Time, bool) {
	after := now
	if sc.UpdatedAt.After(after) {
		after = sc.UpdatedAt
	}
	return backup.NextRun(sc.ScheduleFields, after, s.Zone)
}

// toContainers maps a view's components to the API type. Slices are never
// nil.
func toContainers(cs []project.Component) []Container {
	out := make([]Container, 0, len(cs))
	for _, c := range cs {
		out = append(out, Container{
			Name:         c.Name,
			Service:      c.Service,
			State:        c.State,
			Status:       c.Status,
			Image:        c.Image,
			Ports:        nonNil(c.Ports),
			CPUPercent:   c.CPUPercent,
			MemBytes:     c.MemBytes,
			RestartCount: c.RestartCount,
			StartedAt:    formatTime(c.StartedAt),
			ExitReason:   c.ExitReason,
			Health:       c.Health,
		})
	}
	return out
}

// formatTime is t in UTC RFC 3339, "" when nil.
func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// toLiveState maps a hub snapshot to the data of the state event.
func (s *server) toLiveState(st control.State) LiveState {
	return LiveState{
		DockerUp: st.DockerUp,
		Projects: s.toProjects(st.Projects),
		Backups:  toBackups(st.Backups),
		Batch:    toBatch(st.Batch),
		Drive:    toDriveStatus(st.Drive),
	}
}

// toBatch maps the latest Back up all, nil before the first.
func toBatch(b *backup.Batch) *BackupBatch {
	if b == nil {
		return nil
	}
	var finished *string
	if b.FinishedAt != nil {
		v := formatTime(b.FinishedAt)
		finished = &v
	}
	return &BackupBatch{
		ID: b.ID, StartedBy: b.StartedBy, StartedAt: formatTime(&b.StartedAt),
		Total: b.Total, Done: b.Done, OK: b.OK, Failed: b.Failed, Skipped: b.Skipped, FinishedAt: finished,
	}
}
