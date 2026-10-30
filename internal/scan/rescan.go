package scan

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// allWorkspaces is the target of a rescan of more than one workspace.
const allWorkspaces = "all workspaces"

// Rescan creates a rescan job over stored workspaces, scanned in the given
// order, and runs it in the background, detached from ctx. It shares the
// one-job-at-a-time rule with Start: it returns a *BusyError while any job
// runs. Every workspace is checked by the job itself, so one that fails the
// guard still appears in the result, with a warning.
func (m *Manager) Rescan(ctx context.Context, ws []store.Workspace, by string) (int64, error) {
	if len(ws) == 0 {
		return 0, errors.New("scan: rescan of no workspace")
	}
	target := allWorkspaces
	if len(ws) == 1 {
		target = ws[0].Path
	}
	ids := make([]int64, len(ws))
	for i, w := range ws {
		ids[i] = w.ID
	}

	m.startMu.Lock()
	defer m.startMu.Unlock()
	if busy := m.busyLocked(); busy != nil {
		return 0, busy
	}
	id, err := m.st.CreateRescanJob(ctx, target, ids, by)
	if err != nil {
		return 0, err
	}
	m.launch(ctx, &job{id: id, path: target, rescan: true, workspaces: slices.Clone(ws)}, by, store.ActWorkspaceRescan)
	return id, nil
}

// listed is one workspace of a rescan after its guard check and walk. A
// skipped workspace owns no row and gets no apply.
type listed struct {
	w       store.Workspace
	folders []Folder
	skipped bool
}

// rescanJob scans every workspace of j, applies the refreshes and Not found
// marks, and returns the rows that wait for the owner's choice: new paths and
// paths stored as ignored.
func (m *Manager) rescanJob(ctx context.Context, j *job) (Result, error) {
	n := len(j.workspaces)
	known, err := m.st.KnownProjects(ctx)
	if err != nil {
		return Result{}, err
	}
	tracked, err := m.st.TrackedProjects(ctx)
	if err != nil {
		return Result{}, err
	}
	own := map[int64][]store.Tracked{} // workspace id -> its tracked projects
	for _, t := range tracked {
		own[t.WorkspaceID] = append(own[t.WorkspaceID], t)
	}
	dk := m.readDocker(ctx, j)
	res := newResult()

	ls := make([]listed, n)
	for i, w := range j.workspaces {
		res.Workspaces = append(res.Workspaces, w.Path)
		ls[i].w = w
		folders, warnings, skip := m.listWorkspace(w, len(own[w.ID]) > 0)
		if skip != "" {
			ls[i].skipped = true
			res.Warnings = append(res.Warnings, "Skipped "+w.Path+": "+skip)
			continue
		}
		ls[i].folders = folders
		res.Warnings = append(res.Warnings, warnings...)
	}

	// Nested roots: a compose folder belongs to the deepest scanned workspace
	// that found it (the longest root), so it is examined and offered once.
	owner := map[string]int{}
	scanned := map[string]bool{}
	for i, l := range ls {
		for _, f := range l.folders {
			scanned[f.Path] = true
			if f.ComposeFile == "" {
				continue
			}
			if o, ok := owner[f.Path]; !ok || len(l.w.Path) > len(ls[o].w.Path) {
				owner[f.Path] = i
			}
		}
	}
	res.FoldersScanned = len(scanned)

	var applies []store.WorkspaceApply
	for i, l := range ls {
		total := len(l.folders)
		m.setProgress(j, Progress{Total: total, Current: l.w.Path, Workspace: i + 1, Workspaces: n})
		if l.skipped {
			continue
		}
		a := store.WorkspaceApply{ID: l.w.ID}
		for k, f := range l.folders {
			current := f.Path
			if f.ComposeFile != "" {
				current = f.ComposeFile
			}
			m.setProgress(j, Progress{Examined: k, Total: total, Current: current, Workspace: i + 1, Workspaces: n})
			if f.ComposeFile == "" || owner[f.Path] != i {
				continue
			}
			m.rescanFolder(ctx, f, l.w.ID, known[f.Path], dk, &a, &res)
		}
		// A workspace marks only its own tracked projects, and only those no
		// scanned workspace found.
		for _, t := range own[l.w.ID] {
			if _, found := owner[t.Path]; found {
				continue
			}
			a.Missing = append(a.Missing, t.Path)
			res.NotFound++
			if t.MissingSince == nil {
				res.NotFoundPaths = append(res.NotFoundPaths, t.Path)
			}
		}
		m.setProgress(j, Progress{Examined: total, Total: total, Workspace: i + 1, Workspaces: n})
		applies = append(applies, a)
	}
	warnDuplicates(res.Rows, known)

	if len(applies) > 0 {
		now := time.Now()
		for i := range applies {
			applies[i].LastScanAt = now
		}
		if err := m.st.ApplyRescan(ctx, store.RescanApply{Workspaces: applies}, now); err != nil {
			return Result{}, err
		}
		if m.onChange != nil {
			m.onChange()
		}
	}
	return res, nil
}

// listWorkspace checks a workspace with the guard and lists it. A non-empty
// skip is why the workspace is left alone: it no longer passes the guard or
// resolves elsewhere, its root cannot be read, or it lists no compose folder
// while it has tracked projects (an unmounted mount point reads as an empty,
// readable folder).
func (m *Manager) listWorkspace(w store.Workspace, hasTracked bool) (folders []Folder, warnings []string, skip string) {
	if m.guard != nil {
		resolved, err := m.guard.Check(w.Path)
		if err != nil {
			return nil, nil, err.Error()
		}
		if resolved != w.Path {
			return nil, nil, "it now resolves to " + resolved + "."
		}
	}
	folders, warnings, err := list(w.Path, w.Depth)
	if err != nil {
		return nil, nil, "the folder is missing or cannot be read."
	}
	if hasTracked && !slices.ContainsFunc(folders, func(f Folder) bool { return f.ComposeFile != "" }) {
		return nil, nil, "it holds no compose project now; is it mounted?"
	}
	return folders, warnings, ""
}

// rescanFolder examines one compose folder a workspace owns. A tracked path
// becomes a refresh in a, holding only what was actually read; a new or
// ignored path becomes a row in res.
func (m *Manager) rescanFolder(ctx context.Context, f Folder, workspaceID int64, k store.Known, dk docker, a *store.WorkspaceApply, res *Result) {
	row, configRead := m.examine(ctx, f)
	if k.State == "tracked" {
		r := store.ProjectRefresh{Path: f.Path}
		if configRead {
			r.Config = &store.ConfigRefresh{
				ComposeFiles:   row.ComposeFiles,
				ComposeProject: row.ComposeProject,
				Details:        row.Details,
				Databases:      row.Databases,
			}
			// Self compares compose project names, so it needs the real
			// name from the config as well as the containers.
			if dk.err == nil {
				self := dk.self != "" && row.ComposeProject == dk.self
				r.Self = &self
			}
		} else {
			res.Warnings = append(res.Warnings, "Could not refresh "+f.Path+": "+row.Warning)
		}
		a.Refresh = append(a.Refresh, r)
		a.Found = append(a.Found, f.Path)
		res.Refreshed++
		return
	}
	dk.fill(&row, configRead)
	row.WorkspaceID = workspaceID
	if k.State == "ignored" {
		row.Ignored = true
		res.IgnoredFound++
	} else {
		row.New = true
		res.New++
	}
	res.Rows = append(res.Rows, row)
}
