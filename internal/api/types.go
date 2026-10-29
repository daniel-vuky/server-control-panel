package api

// The request and response bodies of the scan, track, workspace and project
// endpoints. tygo turns them into web/src/api/types.gen.ts, so every field is
// a built-in type, a slice of one, or another type of this file. Slices are
// never null in a response.

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

// ScanBusy is the 409 body of POST /api/scans while another scan runs: the
// running scan, so the client can attach to it.
type ScanBusy struct {
	Error string `json:"error"`
	ID    int64  `json:"id"`
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

// ScanProgress is how far a scan is: folders examined out of the folders
// listed, and the compose file or folder being examined. It is also the data
// of the SSE progress event.
type ScanProgress struct {
	Examined int    `json:"examined"`
	Total    int    `json:"total"`
	Current  string `json:"current"`
}

// ScanJob is the body of GET /api/scans/{id}. State is running, done, failed
// or interrupted; Result is set when the state is done.
type ScanJob struct {
	ID       int64        `json:"id"`
	State    string       `json:"state"`
	Path     string       `json:"path"`
	Depth    int          `json:"depth"`
	Progress ScanProgress `json:"progress"`
	Error    string       `json:"error"`
	Result   *ScanResult  `json:"result,omitempty"`
}

// ScanResult is what a finished scan found. AlreadyTracked and Ignored count
// the projects left out because they are already stored.
type ScanResult struct {
	Rows           []ScanRow `json:"rows"`
	FoldersScanned int       `json:"foldersScanned"`
	AlreadyTracked int       `json:"alreadyTracked"`
	Ignored        int       `json:"ignored"`
	Warnings       []string  `json:"warnings"`
}

// ScanRow is one compose project a scan found. Status is running, partial,
// stopped, error or unknown. Warning may hold several lines.
type ScanRow struct {
	Path         string     `json:"path"`
	ComposeFiles []string   `json:"composeFiles"`
	DefaultName  string     `json:"defaultName"`
	Status       string     `json:"status"`
	Self         bool       `json:"self"`
	Services     int        `json:"services"`
	Databases    []Database `json:"databases"`
	Warning      string     `json:"warning"`
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

// TrackRequest is the body of POST /api/scans/{id}/track: every row of the
// scan, once, with its display name and whether to track it.
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

// TrackResponse is the body of a successful POST /api/scans/{id}/track.
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

// Project is one element of GET /api/projects: a tracked project with its
// live status (running, partial, stopped, error or unknown).
type Project struct {
	ID          int64      `json:"id"`
	DisplayName string     `json:"displayName"`
	Path        string     `json:"path"`
	WorkspaceID int64      `json:"workspaceId"`
	Self        bool       `json:"self"`
	Status      string     `json:"status"`
	Databases   []Database `json:"databases"`
}
