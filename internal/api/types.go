package api

// The request and response bodies of the scan, rescan, track, workspace,
// project, action, untrack, settings, logs, event, Docker check, Drive,
// backup and schedule endpoints. tygo turns them into
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
// Sources lists the variables or services it was found in. Key identifies it
// within its project (type:location:name). Backup says whether a backup may
// start now and Schedule is its backup schedule; both are null on a scan row
// and set on a tracked project's row (Schedule stays null for a database with
// no schedule row).
type Database struct {
	Type         string            `json:"type"`         // postgresql | mysql | mongodb | sqlite
	Name         string            `json:"name"`         // database name, or the service name
	LocationKind string            `json:"locationKind"` // container | host | external | file | unknown
	Location     string            `json:"location"`     // service or container name, host name or file path
	Sources      []string          `json:"sources"`
	Key          string            `json:"key"`
	Backup       *BackupRule       `json:"backup" tstype:"BackupRule | null,required"`
	Schedule     *DatabaseSchedule `json:"schedule" tstype:"DatabaseSchedule | null,required"`
}

// DatabaseSchedule is a database's backup schedule. Frequency is off, daily or
// weekly; Time is "HH:MM" in the server zone; Weekday is 0 (Sunday) to 6 and
// only matters for weekly; Keep is how many copies stay on Drive (1 to 365);
// Compress gzips the dump. NextRunAt is the next scheduled run, RFC 3339 with
// the server zone's offset, null when the schedule is off or the database can
// never be backed up (not_supported), since the scheduler skips it.
type DatabaseSchedule struct {
	Frequency string  `json:"frequency"`
	Time      string  `json:"time"`
	Weekday   int     `json:"weekday"`
	Keep      int     `json:"keep"`
	Compress  bool    `json:"compress"`
	NextRunAt *string `json:"nextRunAt" tstype:"string | null,required"`
}

// ScheduleFields are a schedule's settings, as DatabaseSchedule describes
// them: the body of POST /api/backup-settings/defaults, its 200 answer and
// BackupSettings.Defaults.
type ScheduleFields struct {
	Frequency string `json:"frequency"`
	Time      string `json:"time"`
	Weekday   int    `json:"weekday"`
	Keep      int    `json:"keep"`
	Compress  bool   `json:"compress"`
}

// BackupScheduleInput is the body of POST /api/projects/{id}/backup-schedule:
// the database (Database.Key) and its new schedule, replaced as a whole.
type BackupScheduleInput struct {
	Database  string `json:"database"`
	Frequency string `json:"frequency"`
	Time      string `json:"time"`
	Weekday   int    `json:"weekday"`
	Keep      int    `json:"keep"`
	Compress  bool   `json:"compress"`
}

// TimeZone is the server's zone: its IANA name (UTC when TZ is unset) and its
// offset now, such as "+07:00".
type TimeZone struct {
	Name   string `json:"name"`
	Offset string `json:"offset"`
}

// BackupSettings is the body of GET /api/backup-settings: the settings a
// newly found database starts from, and the zone schedule times are read in.
type BackupSettings struct {
	Defaults ScheduleFields `json:"defaults"`
	TimeZone TimeZone       `json:"timeZone"`
}

// BackupRule says whether a database can be backed up now. Allowed is true
// only when Reason is "". Reason is not_found (the project's folder is
// missing), not_supported, no_db_name, docker_down, db_not_running,
// drive_not_connected, drive_expired or folder_missing. Service is the compose
// service the database resolved to ("" when none did), for words such as
// "Start <service> first".
type BackupRule struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
	Service string `json:"service"`
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

// Project is one element of GET /api/projects, of LiveState and the 200 body
// of POST /api/projects/{id}/settings: a tracked project with its effective
// status (running, partial, stopped, error, unknown, notfound when a rescan
// could not find its folder, or starting, stopping or restarting while a
// command runs on it) and the actions it allows now: start, stop, restart,
// untrack, or only remove when Not found; [] while a command runs on it or it
// is held for a removal. untrack needs no Docker, so it is offered even when
// Docker could not be read. MissingSince is when a rescan first missed its
// folder (UTC RFC 3339), "" when it is present.
//
// The settings: PublicDomain is "" when unset, StopTemplate is stop or down,
// and a null alert threshold means the server default. ComposeFiles are the
// base names of the compose files, the main one first.
//
// The live part: CPUPercent and MemBytes sum the running containers that
// have values (null when none has), UpSince is the start of the
// longest-running running container (UTC RFC 3339, "" unless Running or
// Partial), and Containers lists the project's containers plus a Not created
// row per service without one, sorted by service then name ([] when Docker
// could not be read). GET /api/projects reads only docker ps, so there
// metrics are null, UpSince is "" and each container has no restart count,
// start time or health; the live state fills them in.
//
// DriveFolder is the project's sub-folder on Drive, under the destination
// folder, that its backups go to (the slugged display name).
type Project struct {
	ID              int64       `json:"id"`
	DisplayName     string      `json:"displayName"`
	Path            string      `json:"path"`
	WorkspaceID     int64       `json:"workspaceId"`
	Self            bool        `json:"self"`
	Status          string      `json:"status"`
	Actions         []string    `json:"actions"`
	Databases       []Database  `json:"databases"`
	MissingSince    string      `json:"missingSince"`
	PublicDomain    string      `json:"publicDomain"`
	StopTemplate    string      `json:"stopTemplate"`
	AlertCPUPercent *int        `json:"alertCpuPercent" tstype:"number | null,required"`
	AlertRAMMB      *int        `json:"alertRamMb" tstype:"number | null,required"`
	ComposeFiles    []string    `json:"composeFiles"`
	CPUPercent      *float64    `json:"cpuPercent" tstype:"number | null,required"`
	MemBytes        *int64      `json:"memBytes" tstype:"number | null,required"`
	UpSince         string      `json:"upSince"`
	Containers      []Container `json:"containers"`
	DriveFolder     string      `json:"driveFolder"`
}

// Container is one row of Project.Containers. Status is the word for the
// table: Running, Restarting, Exited, Dead, Paused, Created or Not created
// (a service with no container, whose State is ""). Ports are ready to show,
// such as "3000→3000" or "5432 (internal)". CPUPercent (Docker's figure, which
// may pass 100 on several cores) and MemBytes are null unless the container
// runs and the last read covered it. StartedAt is UTC RFC 3339, "" when it
// never started or is not known. ExitReason says why a container that is not
// running is not ("" while it runs); Health is the healthcheck's status, ""
// without one.
type Container struct {
	Name         string   `json:"name"`
	Service      string   `json:"service"`
	State        string   `json:"state"`
	Status       string   `json:"status"`
	Image        string   `json:"image"`
	Ports        []string `json:"ports"`
	CPUPercent   *float64 `json:"cpuPercent" tstype:"number | null,required"`
	MemBytes     *int64   `json:"memBytes" tstype:"number | null,required"`
	RestartCount int      `json:"restartCount"`
	StartedAt    string   `json:"startedAt"`
	ExitReason   string   `json:"exitReason"`
	Health       string   `json:"health"`
}

// SettingsRequest is the body of POST /api/projects/{id}/settings. It
// replaces all five settings. DisplayName is trimmed, 1 to 80 characters with
// no control character; PublicDomain is trimmed and lowercased, "" to clear
// it, else a host name of two or more labels; StopTemplate is stop or down
// (always stop for the panel's own project); AlertCPUPercent is null or 1 to
// 100 and AlertRAMMB null or 1 to 1048576.
type SettingsRequest struct {
	DisplayName     string `json:"displayName"`
	PublicDomain    string `json:"publicDomain"`
	StopTemplate    string `json:"stopTemplate"`
	AlertCPUPercent *int   `json:"alertCpuPercent" tstype:"number | null,required"`
	AlertRAMMB      *int   `json:"alertRamMb" tstype:"number | null,required"`
}

// LogLine is one line of container output. Container is the container's name
// and Service its compose service ("" when the line's prefix matched no known
// container); Time is when Docker received it (UTC RFC 3339 with fractional
// seconds). Text is masked with the project's .env values.
type LogLine struct {
	Container string `json:"container"`
	Service   string `json:"service"`
	Time      string `json:"time"`
	Text      string `json:"text"`
}

// LogLines is the data of the "lines" event of GET /api/projects/{id}/logs.
// The first one is the backlog (the newest lines Docker keeps, possibly
// none), each next one the lines that arrived since.
type LogLines struct {
	Lines []LogLine `json:"lines"`
}

// LogEnd is the data of the final "end" event of GET
// /api/projects/{id}/logs, after which the server closes the stream. Reason
// is exited (nothing runs, or the containers stopped), slow (the viewer fell
// behind), error (Message says why, masked), or a refusal sent before
// anything ran: unknown_service, not_found, docker_down or
// too_many_log_streams. Message is "" for exited and slow.
type LogEnd struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
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
// Docker answers, every tracked project as GET /api/projects returns it, the
// active backups (running first, then the queue in order), the latest Back up
// all (null before the first since the panel started) and the Drive
// connection as GET /api/drive returns it.
type LiveState struct {
	DockerUp bool         `json:"dockerUp"`
	Projects []Project    `json:"projects"`
	Backups  []Backup     `json:"backups"`
	Batch    *BackupBatch `json:"batch" tstype:"BackupBatch | null,required"`
	Drive    DriveStatus  `json:"drive"`
}

// DockerCheck is the body of POST /api/docker/check.
type DockerCheck struct {
	DockerUp bool `json:"dockerUp"`
}

// DriveFolder is a Google Drive folder the panel created.
type DriveFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DriveStatus is the body of GET /api/drive and LiveState.Drive. State is
// not_configured, not_connected, connected or expired. Email is the connected
// account ("" when none), Folder the destination folder (null until chosen)
// and Reason why Drive is not configured ("" otherwise). It never carries a
// token.
type DriveStatus struct {
	State  string       `json:"state"`
	Email  string       `json:"email"`
	Folder *DriveFolder `json:"folder" tstype:"DriveFolder | null,required"`
	Reason string       `json:"reason"`
}

// DriveConnectRequest is the body of POST /api/drive/connect: the same-site
// path to come back to; anything else becomes /settings.
type DriveConnectRequest struct {
	ReturnTo string `json:"returnTo"`
}

// DriveConnectResponse is the 200 body of POST /api/drive/connect: the
// consent page the browser navigates to.
type DriveConnectResponse struct {
	URL string `json:"url"`
}

// DriveFolderRequest is the body of POST /api/drive/folder: ID selects one of
// the panel's folders; without an ID, Name (trimmed, 1 to 100 characters, no
// /) creates a folder in My Drive root, or selects the panel's folder of that
// name, and selects it.
type DriveFolderRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// DriveTestResult is the 200 body of POST /api/drive/test: OK, or the Drive
// code (token_expired, no_access, folder_missing, quota, unreachable,
// not_configured or not_connected) and its words.
type DriveTestResult struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// DriveError is the body of a Drive route or a download that Drive (or the
// connection) refused: Error is the code (not_configured, not_connected,
// busy, or a Drive code such as token_expired), Message its words.
type DriveError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// BackupRequest is the body of POST /api/projects/{id}/backups: the key of
// the database to back up (Database.Key).
type BackupRequest struct {
	Database string `json:"database"`
}

// BackupStarted is the 202 body of POST /api/projects/{id}/backups and POST
// /api/backups/{id}/retry: the queued backup.
type BackupStarted struct {
	ID int64 `json:"id"`
}

// BackupRefused is the 409 body of a backup that was not queued. Error is the
// code: not_found, not_supported, no_db_name, drive_not_connected,
// drive_expired, folder_missing, already_running, docker_down,
// db_not_running, disk_short or not_retryable; Message its words. ID is the
// backup already running (only for already_running), NeedBytes and FreeBytes
// are set for disk_short, and Service is the database's compose service once
// it is resolved.
type BackupRefused struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	ID        int64  `json:"id,omitempty"`
	NeedBytes int64  `json:"needBytes,omitempty"`
	FreeBytes int64  `json:"freeBytes,omitempty"`
	Service   string `json:"service,omitempty"`
}

// BackupDatabase is the snapshot of the database a backup was taken of.
type BackupDatabase struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	LocationKind string `json:"locationKind"`
	Location     string `json:"location"`
	Key          string `json:"key"`
}

// BackupStep is one step of a backup: Step is dump, compress, upload or
// prune; State is waiting, running, done, failed or skipped; Detail is the
// line shown under it.
type BackupStep struct {
	Step   string `json:"step"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// Backup is one backup run: GET /api/backups/{id}, an element of
// BackupHistory and of LiveState.Backups. State is queued, running, ok,
// failed, refused or interrupted; FailedStep is the step that failed ("" when
// none). Error and Hint are masked. The times are UTC RFC 3339 ("" when not
// reached). QueuePosition is 0 when running or finished, and N ≥ 1 while
// waiting. StartedBy is the email of the person who started it, or
// "schedule". PrunedAt is when retention removed its file from Drive (UTC RFC
// 3339), null while the file is kept.
type Backup struct {
	ID            int64          `json:"id"`
	ProjectID     int64          `json:"projectId"`
	ProjectName   string         `json:"projectName"`
	Database      BackupDatabase `json:"database"`
	State         string         `json:"state"`
	FailedStep    string         `json:"failedStep"`
	Steps         []BackupStep   `json:"steps"`
	Error         string         `json:"error"`
	Hint          string         `json:"hint"`
	RawBytes      int64          `json:"rawBytes"`
	SizeBytes     int64          `json:"sizeBytes"`
	FileName      string         `json:"fileName"`
	DriveLink     string         `json:"driveLink"`
	StartedBy     string         `json:"startedBy"`
	CreatedAt     string         `json:"createdAt"`
	StartedAt     string         `json:"startedAt"`
	FinishedAt    string         `json:"finishedAt"`
	DurationMS    int64          `json:"durationMs"`
	QueuePosition int            `json:"queuePosition"`
	PrunedAt      *string        `json:"prunedAt" tstype:"string | null,required"`
}

// BackupHistory is the body of GET /api/projects/{id}/backups: a page of the
// project's backups, newest first, whether older ones remain, and the number
// of copies on Drive per database key (only keys with at least one).
type BackupHistory struct {
	Backups []Backup       `json:"backups"`
	More    bool           `json:"more"`
	Stored  map[string]int `json:"stored"`
}

// BackupList is the body of GET /api/backups: a page of every project's
// backups, newest first, and whether older ones remain.
type BackupList struct {
	Backups []Backup `json:"backups"`
	More    bool     `json:"more"`
}

// QueryError is the 400 body of GET /api/backups for a bad query parameter:
// the message and the parameter's name.
type QueryError struct {
	Error string `json:"error"`
	Field string `json:"field"`
}

// BackupBatch is LiveState.Batch, the latest Back up all. Total is the
// databases it queued; Done counts those that ended, as OK, Failed (failed,
// refused, interrupted or not queued) or Skipped (gone by the time it was
// queued). Skipped also counts the databases that cannot be backed up, which
// are not in Total. StartedBy is the email of who started it; the times are
// UTC RFC 3339, FinishedAt null while it runs.
type BackupBatch struct {
	ID         int64   `json:"id"`
	StartedBy  string  `json:"startedBy"`
	StartedAt  string  `json:"startedAt"`
	Total      int     `json:"total"`
	Done       int     `json:"done"`
	OK         int     `json:"ok"`
	Failed     int     `json:"failed"`
	Skipped    int     `json:"skipped"`
	FinishedAt *string `json:"finishedAt" tstype:"string | null,required"`
}

// BackupAllResult is the 202 body of POST /api/backups/all: the batch, the
// databases it queues and the ones skipped as not supported.
type BackupAllResult struct {
	ID      int64 `json:"id"`
	Total   int   `json:"total"`
	Skipped int   `json:"skipped"`
}

// BackupSummary is the body of GET /api/backups/summary. The Drive figures
// are what the panel stored (every backup row with a Drive file that
// retention has not removed, whatever its state), not a Drive query. Days
// are the last 7 days of the server zone, oldest first, today last; OK7d and
// Failed7d sum them (failed counts failed, refused and
// interrupted). RecentFailures are the newest two failed or refused backups.
// NextRun is the earliest scheduled run and the projects due at that minute,
// null when nothing is scheduled.
type BackupSummary struct {
	DriveBytes     int64            `json:"driveBytes"`
	DriveFiles     int              `json:"driveFiles"`
	DriveProjects  int              `json:"driveProjects"`
	Days           []SummaryDay     `json:"days"`
	OK7d           int              `json:"ok7d"`
	Failed7d       int              `json:"failed7d"`
	RecentFailures []SummaryFailure `json:"recentFailures"`
	NextRun        *SummaryNextRun  `json:"nextRun" tstype:"SummaryNextRun | null,required"`
}

// SummaryDay is one day of BackupSummary.Days: Date is YYYY-MM-DD in the
// server zone.
type SummaryDay struct {
	Date   string `json:"date"`
	OK     int    `json:"ok"`
	Failed int    `json:"failed"`
}

// SummaryFailure is one of BackupSummary.RecentFailures: At is when the
// backup was created (UTC RFC 3339).
type SummaryFailure struct {
	ProjectID   int64  `json:"projectId"`
	ProjectName string `json:"projectName"`
	DBName      string `json:"dbName"`
	At          string `json:"at"`
}

// SummaryNextRun is BackupSummary.NextRun: At is RFC 3339 with the server
// zone's offset; Projects are the display names due at that minute.
type SummaryNextRun struct {
	At       string   `json:"at"`
	Projects []string `json:"projects"`
}

// ServerInfo is the body of GET /api/server: the System info and System
// services cards and what the Server page's charts need. Available is false,
// with Reason the words to show, when the host's /proc is not mounted (no
// server metrics). Hostname, OS, Kernel and Docker.Version are "" before
// Docker has answered once. DisksPartial means the one fallback row ("/
// (panel data)"): re-running install.sh lists every disk. SampleSeconds is the
// sampling interval and RetentionDays how long history is kept. Load is the
// host's load averages and BootTime when it booted (RFC 3339 in the server
// zone), each null when /proc cannot be read.
type ServerInfo struct {
	Available     bool          `json:"available"`
	Reason        string        `json:"reason,omitempty"`
	Hostname      string        `json:"hostname"`
	OS            string        `json:"os"`
	Kernel        string        `json:"kernel"`
	Cores         int           `json:"cores"`
	RAMBytes      int64         `json:"ramBytes"`
	Docker        DockerService `json:"docker"`
	Cloudflared   TunnelService `json:"cloudflared"`
	Disks         []DiskInfo    `json:"disks"`
	DisksPartial  bool          `json:"disksPartial"`
	Thresholds    Thresholds    `json:"thresholds"`
	SampleSeconds int           `json:"sampleSeconds"`
	RetentionDays int           `json:"retentionDays"`
	TimeZone      TimeZone      `json:"timeZone"`
	Load          *LoadAverage  `json:"load" tstype:"LoadAverage | null,required"`
	BootTime      *string       `json:"bootTime" tstype:"string | null,required"`
}

// LoadAverage is ServerInfo.Load: the 1, 5 and 15 minute load averages.
type LoadAverage struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

// DockerService is ServerInfo.Docker: State is running, not_responding or
// not_installed (no Docker socket); ContainersRunning is from the last good
// docker info.
type DockerService struct {
	State             string `json:"state"`
	Version           string `json:"version"`
	ContainersRunning int    `json:"containersRunning"`
}

// TunnelService is ServerInfo.Cloudflared: State is connected, disconnected,
// not_running or unknown (running, its metrics server not reachable);
// Hostnames is null when its configuration could not be read.
type TunnelService struct {
	State     string `json:"state"`
	Hostnames *int   `json:"hostnames" tstype:"number | null,required"`
}

// DiskInfo is one disk of the server, in bytes.
type DiskInfo struct {
	Mount      string `json:"mount"`
	UsedBytes  uint64 `json:"usedBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}

// Thresholds are the server's alert lines in percent.
type Thresholds struct {
	CPUPercent  int `json:"cpuPercent"`
	RAMPercent  int `json:"ramPercent"`
	DiskPercent int `json:"diskPercent"`
}

// ServerPoint is one bucket of a ServerSeries: T is the bucket start; CPU is %
// of the whole machine, memory and disk space in bytes, the rates in bytes per
// second, Load1 the 1-minute load average and Tunnel the share of the bucket
// cloudflared was connected (1 connected, 0 not); a value is null where
// nothing was sampled (a gap).
type ServerPoint struct {
	T         string   `json:"t"`
	CPU       *float64 `json:"cpu" tstype:"number | null,required"`
	MemUsed   *int64   `json:"memUsed" tstype:"number | null,required"`
	MemTotal  *int64   `json:"memTotal" tstype:"number | null,required"`
	DiskRead  *float64 `json:"diskRead" tstype:"number | null,required"`
	DiskWrite *float64 `json:"diskWrite" tstype:"number | null,required"`
	NetIn     *float64 `json:"netIn" tstype:"number | null,required"`
	NetOut    *float64 `json:"netOut" tstype:"number | null,required"`
	Load1     *float64 `json:"load1" tstype:"number | null,required"`
	DiskUsed  *int64   `json:"diskUsed" tstype:"number | null,required"`
	DiskTotal *int64   `json:"diskTotal" tstype:"number | null,required"`
	Tunnel    *float64 `json:"tunnel" tstype:"number | null,required"`
}

// ServerLatest is ServerSeries.Latest, the newest sample (at most 90 s old):
// At is when it was taken, the values as in ServerPoint.
type ServerLatest struct {
	At        string   `json:"at"`
	CPU       *float64 `json:"cpu" tstype:"number | null,required"`
	MemUsed   *int64   `json:"memUsed" tstype:"number | null,required"`
	MemTotal  *int64   `json:"memTotal" tstype:"number | null,required"`
	DiskRead  *float64 `json:"diskRead" tstype:"number | null,required"`
	DiskWrite *float64 `json:"diskWrite" tstype:"number | null,required"`
	NetIn     *float64 `json:"netIn" tstype:"number | null,required"`
	NetOut    *float64 `json:"netOut" tstype:"number | null,required"`
	Load1     *float64 `json:"load1" tstype:"number | null,required"`
	DiskUsed  *int64   `json:"diskUsed" tstype:"number | null,required"`
	DiskTotal *int64   `json:"diskTotal" tstype:"number | null,required"`
	Tunnel    *float64 `json:"tunnel" tstype:"number | null,required"`
}

// ServerSeries is the body of GET /api/server/metrics: one point per bucket
// of the range (1h: 120 of 30 s, 24h: 288 of 5 min, 7d: 336 of 30 min), the
// last bucket the one containing now. From and To are the first and last
// bucket starts. HistoryStart is the oldest kept sample (null with none);
// Latest is null when there is no sample of the last 90 s. Times are RFC 3339
// in the server zone.
type ServerSeries struct {
	Range        string        `json:"range"`
	StepSeconds  int           `json:"stepSeconds"`
	From         string        `json:"from"`
	To           string        `json:"to"`
	HistoryStart *string       `json:"historyStart" tstype:"string | null,required"`
	Latest       *ServerLatest `json:"latest" tstype:"ServerLatest | null,required"`
	Points       []ServerPoint `json:"points"`
}

// ProjectPoint is one bucket of a ProjectSeries: CPU in Docker's % (100 = one
// core), memory in bytes, the network rates in bytes per second; null in a
// gap (the project was not running, Docker was down or nothing was sampled).
type ProjectPoint struct {
	T      string   `json:"t"`
	CPU    *float64 `json:"cpu" tstype:"number | null,required"`
	Mem    *int64   `json:"mem" tstype:"number | null,required"`
	NetIn  *float64 `json:"netIn" tstype:"number | null,required"`
	NetOut *float64 `json:"netOut" tstype:"number | null,required"`
}

// ProjectLatest is ProjectSeries.Latest, the project's newest sample (at
// most 90 s old, while it runs).
type ProjectLatest struct {
	At     string   `json:"at"`
	CPU    *float64 `json:"cpu" tstype:"number | null,required"`
	Mem    *int64   `json:"mem" tstype:"number | null,required"`
	NetIn  *float64 `json:"netIn" tstype:"number | null,required"`
	NetOut *float64 `json:"netOut" tstype:"number | null,required"`
}

// ProjectSeries is the body of GET /api/projects/{id}/metrics, shaped like
// ServerSeries.
type ProjectSeries struct {
	Range        string         `json:"range"`
	StepSeconds  int            `json:"stepSeconds"`
	From         string         `json:"from"`
	To           string         `json:"to"`
	HistoryStart *string        `json:"historyStart" tstype:"string | null,required"`
	Latest       *ProjectLatest `json:"latest" tstype:"ProjectLatest | null,required"`
	Points       []ProjectPoint `json:"points"`
}

// UsageRow is one row of the Usage by app table: a tracked, present project,
// or the panel itself when its project is not tracked (ProjectID null, Name
// "Server Control Panel"). Self marks the panel's row. Status is the
// project's status from the hub's recent read, null when there is none (and
// for the untracked panel row). CPUPercent is a share of the whole server
// (Docker's % divided by the core count); NetBps is in + out. A value is null
// when unknown (Docker down, no recent sample, no usable network delta) and 0
// for a project with nothing running; the panel's own row has no NetBps in
// production, since the panel runs with host networking (by design, D7a).
// DiskBytes is its named volumes plus its folder, read hourly (null before
// the first read); DiskPartial means it is a lower bound; DiskAt is when it
// was read.
type UsageRow struct {
	ProjectID   *int64   `json:"projectId" tstype:"number | null,required"`
	Name        string   `json:"name"`
	Status      *string  `json:"status" tstype:"string | null,required"`
	Self        bool     `json:"self"`
	CPUPercent  *float64 `json:"cpuPercent" tstype:"number | null,required"`
	MemBytes    *int64   `json:"memBytes" tstype:"number | null,required"`
	NetBps      *float64 `json:"netBps" tstype:"number | null,required"`
	DiskBytes   *int64   `json:"diskBytes" tstype:"number | null,required"`
	DiskPartial bool     `json:"diskPartial"`
	DiskAt      *string  `json:"diskAt" tstype:"string | null,required"`
}

// UsageTotals are the server's totals the share bars divide by: CPU % used,
// RAM used and network in + out from the latest server sample, and the used
// space of the listed disks. Null when unknown.
type UsageTotals struct {
	CPUPercent    *float64 `json:"cpuPercent" tstype:"number | null,required"`
	MemBytes      *int64   `json:"memBytes" tstype:"number | null,required"`
	DiskUsedBytes *uint64  `json:"diskUsedBytes" tstype:"number | null,required"`
	NetBps        *float64 `json:"netBps" tstype:"number | null,required"`
}

// UsageReport is the body of GET /api/server/usage: At is the latest sample
// the values come from (null with none in the last 90 s), RFC 3339 in the
// server zone.
type UsageReport struct {
	At     *string     `json:"at" tstype:"string | null,required"`
	Totals UsageTotals `json:"totals"`
	Rows   []UsageRow  `json:"rows"`
}

// MetricsSettings is the body of GET and POST /api/metrics-settings: how many
// days of history are kept (7 to 30).
type MetricsSettings struct {
	RetentionDays int `json:"retentionDays"`
}

// LatestBackups is the body of GET /api/backups/latest: for each tracked
// project its newest ended backup (ok, failed, refused or interrupted; never a
// queued or running one), newest first, and Latest, the first of them (null
// when no project has one).
type LatestBackups struct {
	Latest   *LatestBackup  `json:"latest" tstype:"LatestBackup | null,required"`
	Projects []LatestBackup `json:"projects"`
}

// LatestBackup is one project's newest ended backup: Database is the name the
// backup was taken of, At when it finished (UTC RFC 3339; when it was created
// for a row with no finish time).
type LatestBackup struct {
	ID          int64  `json:"id"`
	ProjectID   int64  `json:"projectId"`
	ProjectName string `json:"projectName"`
	Database    string `json:"database"`
	State       string `json:"state"`
	At          string `json:"at"`
}
