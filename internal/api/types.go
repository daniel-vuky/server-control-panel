package api

// The request and response bodies of the scan, rescan, track, workspace,
// project, action, untrack, event and Docker check endpoints. tygo turns them into
// web/src/api/types.gen.ts, so every field is a built-in type, a slice of one,
// or another type of this file. Slices are never null in a response.

// FieldError is a 422 body: the message to show under the named form field
// and a stable code.
type FieldError struct {
	Error string `json:"error"`
	Field string `json:"field"`
	Code  string `json:"code"`
}

// ScanRequest is the body of POST /api/scans.
type ScanRequest struct {
	Path  string `json:"path"`
	Depth int    `json:"depth"` // 1 to 3
}

// ScanStarted is the 202 body of POST /api/scans.
type ScanStarted struct {
	ID int64 `json:"id"`
}

// ScanBusy is the 409 body of POST /api/scans and POST /api/rescans while
// another scan runs: the running scan, so the client can attach to it. For a
// running rescan, Rescan is true, Path is its target (a workspace path or
// "all workspaces") and Depth is 0.
type ScanBusy struct {
	Error  string `json:"error"`
	ID     int64  `json:"id"`
	Path   string `json:"path"`
	Depth  int    `json:"depth"`
	Rescan bool   `json:"rescan"`
}

// RescanRequest is the body of POST /api/rescans: the workspace to rescan, or
// 0 for every workspace.
type RescanRequest struct {
	WorkspaceID int64 `json:"workspaceId"`
}

// ScanProgress is how far a scan is: folders examined out of the folders
// listed, and the compose file or folder being examined. For a rescan,
// Workspace is the 1-based index of the workspace being scanned and
// Workspaces their count; both are 0 for a first-time scan. It is also the
// data of the SSE progress event.
type ScanProgress struct {
	Examined   int    `json:"examined"`
	Total      int    `json:"total"`
	Current    string `json:"current"`
	Workspace  int    `json:"workspace"`
	Workspaces int    `json:"workspaces"`
}

// ScanJob is the body of GET /api/scans/{id}. State is running, done, failed
// or interrupted; Result is set when the state is done. Rescan is true for a
// rescan of stored workspaces: WorkspaceIDs then lists them in scan order (to
// retry the same scope), Path is the single workspace's path or "all
// workspaces", and Depth is 0. WorkspaceIDs is empty for a first-time scan.
type ScanJob struct {
	ID           int64        `json:"id"`
	State        string       `json:"state"`
	Path         string       `json:"path"`
	Depth        int          `json:"depth"`
	Rescan       bool         `json:"rescan"`
	WorkspaceIDs []int64      `json:"workspaceIds"`
	Progress     ScanProgress `json:"progress"`
	Error        string       `json:"error"`
	Result       *ScanResult  `json:"result,omitempty"`
}

// ScanResult is what a finished scan found. For a first-time scan,
// AlreadyTracked and Ignored count the projects left out because they are
// already stored. For a rescan, Rows holds the new and the ignored projects
// found; New and IgnoredFound count them, Refreshed counts the tracked
// projects found again, NotFound every tracked project not found (marked Not
// found), NotFoundPaths the paths newly marked by this rescan, and Workspaces
// the workspace paths scanned. A skipped workspace is named in Warnings.
type ScanResult struct {
	Rows           []ScanRow `json:"rows"`
	FoldersScanned int       `json:"foldersScanned"`
	AlreadyTracked int       `json:"alreadyTracked"`
	Ignored        int       `json:"ignored"`
	Warnings       []string  `json:"warnings"`
	New            int       `json:"new"`
	IgnoredFound   int       `json:"ignoredFound"`
	Refreshed      int       `json:"refreshed"`
	NotFound       int       `json:"notFound"`
	NotFoundPaths  []string  `json:"notFoundPaths"`
	Workspaces     []string  `json:"workspaces"`
}

// ScanRow is one compose project a scan found. Status is running, partial,
// stopped, error or unknown. Warning may hold several lines. On a rescan,
// WorkspaceID is the workspace the row belongs to, and the row is either New
// (a path never stored) or Ignored (a path stored as ignored); on a first-time
// scan all three are zero.
type ScanRow struct {
	Path         string     `json:"path"`
	ComposeFiles []string   `json:"composeFiles"`
	DefaultName  string     `json:"defaultName"`
	Status       string     `json:"status"`
	Self         bool       `json:"self"`
	Services     int        `json:"services"`
	Databases    []Database `json:"databases"`
	Warning      string     `json:"warning"`
	WorkspaceID  int64      `json:"workspaceId"`
	New          bool       `json:"new"`
	Ignored      bool       `json:"ignored"`
}

// Database is a database a project uses. It holds names only, never values:
// Sources lists the variables or services it was found in.
type Database struct {
	Type         string   `json:"type"`         // postgresql | mysql | mongodb | sqlite
	Name         string   `json:"name"`         // database name, or the service name
	LocationKind string   `json:"locationKind"` // container | host | external | file | unknown
	Location     string   `json:"location"`     // service or container name, host name or file path
	Sources      []string `json:"sources"`
}

// TrackRequest is the body of POST /api/scans/{id}/track. For a first-time
// scan it lists every row of the scan, once, with its display name and
// whether to track it. For a rescan it lists only the rows to track (a New or
// an Ignored row, Track true), once each; the New rows it leaves out are
// stored as ignored, and the Ignored rows it leaves out stay ignored.
type TrackRequest struct {
	Projects []TrackItem `json:"projects"`
}

// TrackItem is one row of a TrackRequest. DisplayName is trimmed and must be
// 1 to 80 characters.
type TrackItem struct {
	Path        string `json:"path"`
	DisplayName string `json:"displayName"`
	Track       bool   `json:"track"`
}

// TrackResponse is the body of a successful POST /api/scans/{id}/track: the
// workspace stored by a first-time scan (0 for a rescan) and the number of
// projects now tracked by it.
type TrackResponse struct {
	WorkspaceID int64 `json:"workspaceId"`
	Added       int   `json:"added"`
}

// Workspace is one element of GET /api/workspaces. LastScanAt is UTC RFC 3339.
type Workspace struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Depth      int    `json:"depth"`
	LastScanAt string `json:"lastScanAt"`
	Tracked    int    `json:"tracked"`
	Ignored    int    `json:"ignored"`
}

// Project is one element of GET /api/projects and of LiveState: a tracked
// project with its effective status (running, partial, stopped, error,
// unknown, notfound when a rescan could not find its folder, or starting,
// stopping or restarting while a command runs on it) and the actions it
// allows now: start, stop, restart, untrack, or only remove when Not found;
// [] while a command runs on it or it is held for a removal. untrack needs no
// Docker, so it is offered even when Docker could not be read. MissingSince is
// when a rescan first missed its folder (UTC RFC 3339), "" when it is present.
type Project struct {
	ID           int64      `json:"id"`
	DisplayName  string     `json:"displayName"`
	Path         string     `json:"path"`
	WorkspaceID  int64      `json:"workspaceId"`
	Self         bool       `json:"self"`
	Status       string     `json:"status"`
	Actions      []string   `json:"actions"`
	Databases    []Database `json:"databases"`
	MissingSince string     `json:"missingSince"`
}

// ActionResult is the 200 body of POST /api/projects/{id}/{action}: how the
// command ended (ok, failed or timed_out), the project's status re-read after
// it, the actions it allows now as Project.Actions gives them (with Docker
// unreadable that is only untrack), and the masked reason when it did not
// succeed (empty on ok).
type ActionResult struct {
	Outcome string   `json:"outcome"`
	Status  string   `json:"status"`
	Actions []string `json:"actions"`
	Message string   `json:"message"`
}

// ActionRefused is the 409 or 503 body of POST /api/projects/{id}/{action}
// when the command was not run, and the 409 body of POST
// /api/projects/{id}/untrack (locked) when nothing changed. Code is locked,
// not_allowed or docker_down; Status is the project's status at the time (its
// in-flight status when locked, unknown when Docker is down).
type ActionRefused struct {
	Error  string `json:"error"`
	Code   string `json:"code"`
	Status string `json:"status"`
}

// UntrackResponse is the 200 body of POST /api/projects/{id}/untrack: the
// project left the tracked list (it is ignored now, or deleted when it was
// Not found).
type UntrackResponse struct {
	Removed bool `json:"removed"`
}

// RemoveResponse is the 200 body of POST /api/workspaces/{id}/remove: how
// many tracked projects went with the workspace.
type RemoveResponse struct {
	Removed int `json:"removed"`
}

// LiveState is the data of the "state" event of GET /api/events: whether
// Docker answers, and every tracked project as GET /api/projects returns it.
type LiveState struct {
	DockerUp bool      `json:"dockerUp"`
	Projects []Project `json:"projects"`
}

// DockerCheck is the body of POST /api/docker/check.
type DockerCheck struct {
	DockerUp bool `json:"dockerUp"`
}
