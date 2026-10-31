package api

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"

	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The Drive words (spec §2 and §6).
const (
	msgDriveNotConfigured = "Google Drive is not set up on this server."
	msgDriveNotConnected  = "Connect Google Drive first"
	msgDriveBusy          = "A connection test is already running."
	msgLinkExpired        = "This link has expired. Go back to Settings and connect again."
	msgCallbackFailed     = "The panel could not complete this request. Go back to Settings and connect again."
	msgBadFolderName      = "A folder name is 1 to 100 characters, with no /."
	msgConnectedMeanwhile = "Google Drive was connected again meanwhile; nothing was disconnected."
)

// The codes of a DriveError that are not Drive's own.
const (
	codeNotConfigured = drive.StateNotConfigured
	codeNotConnected  = drive.StateNotConnected
	codeBusy          = "busy"
)

// driveTarget is the activity target when no account is known.
const driveTarget = "Google Drive"

// toDriveStatus maps the Drive service's status to the API type.
func toDriveStatus(st drive.Status) DriveStatus {
	out := DriveStatus{State: st.State, Email: st.Email, Reason: st.Reason}
	if st.Folder != nil {
		out.Folder = &DriveFolder{ID: st.Folder.ID, Name: st.Folder.Name}
	}
	return out
}

// driveCode reduces a Drive service error to a code and its words: the
// connection's own state (not_configured, not_connected) or a Drive code. ok
// is false for any other error.
func driveCode(err error) (code, words string, ok bool) {
	switch {
	case errors.Is(err, drive.ErrNotConfigured):
		return codeNotConfigured, msgDriveNotConfigured, true
	case errors.Is(err, drive.ErrNotConnected):
		return codeNotConnected, msgDriveNotConnected, true
	}
	if code := drive.CodeOf(err); code != "" {
		return code, drive.Words(code), true
	}
	return "", "", false
}

// driveFailed answers a Drive service error: 409 for the connection's own
// state (not configured, not connected) or a running test, 502 with the Drive
// code when Drive refused, nothing when the client went away, else 500.
func (s *server) driveFailed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, drive.ErrBusy) {
		writeJSON(w, http.StatusConflict, DriveError{Error: codeBusy, Message: msgDriveBusy})
		return
	}
	if code, words, ok := driveCode(err); ok {
		status := http.StatusBadGateway
		if code == codeNotConfigured || code == codeNotConnected {
			status = http.StatusConflict
		}
		writeJSON(w, status, DriveError{Error: code, Message: words})
		return
	}
	if r.Context().Err() != nil && errors.Is(err, r.Context().Err()) {
		s.log.Info("drive: the client went away", "path", r.URL.Path)
		return
	}
	s.internalError(w, r, err)
}

// driveStatus is GET /api/drive: the cached connection, never a token.
func (s *server) driveStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, toDriveStatus(s.Drive.Status()))
}

// driveConnect is POST /api/drive/connect: it stores a single-use state for
// the actor and the sanitised returnTo, and answers the consent URL.
func (s *server) driveConnect(w http.ResponseWriter, r *http.Request) {
	var req DriveConnectRequest
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.Drive.StartConnect(r.Context(), actor(r), req.ReturnTo)
	if err != nil {
		s.driveFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, DriveConnectResponse{URL: u})
}

// driveCallback is GET /api/drive/callback, where Google sends the browser
// back. An unknown, used, expired or foreign state gets a 400 page; otherwise
// it redirects to the returnTo stored with the state, with drive=connected,
// declined or error added. A connect, or a failed one, is recorded as
// drive.connect; a declined one is not.
func (s *server) driveCallback(w http.ResponseWriter, r *http.Request) {
	// The query holds the authorization code: never cache this answer, and
	// keep its URL out of any Referer.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	q := r.URL.Query()
	// The exchange and the store write outlive a client that goes away.
	ctx := context.WithoutCancel(r.Context())
	to, err := s.Drive.FinishConnect(ctx, actor(r), q.Get("state"), q.Get("code"), q.Get("error"))
	switch {
	case errors.Is(err, drive.ErrBadState):
		writePage(w, http.StatusBadRequest, "Link expired", msgLinkExpired)
		return
	case err != nil:
		s.log.Error("drive: finish connect", "err", err)
		writePage(w, http.StatusInternalServerError, "Google Drive was not connected", msgCallbackFailed)
		return
	}
	s.recordConnect(r, to)
	w.Header().Set("Location", to)
	w.WriteHeader(http.StatusFound)
}

// recordConnect records the outcome of a callback from the drive= parameter
// FinishConnect added to its redirect.
func (s *server) recordConnect(r *http.Request, to string) {
	u, err := url.Parse(to)
	if err != nil {
		return
	}
	q := u.Query()
	switch q.Get("drive") {
	case "connected":
		target := s.Drive.Status().Email
		if target == "" {
			target = driveTarget
		}
		s.record(r, store.Activity{Action: store.ActDriveConnect, Target: target}, store.ResultOK, "")
	case "error":
		words := drive.ConnectWords(q.Get("code"))
		if q.Get("code") == codeNotConfigured {
			words = msgDriveNotConfigured
		}
		s.record(r, store.Activity{Action: store.ActDriveConnect, Target: driveTarget}, store.ResultFailed, words)
	}
}

// driveFolders is GET /api/drive/folders: the folders the panel created in My
// Drive root, by name.
func (s *server) driveFolders(w http.ResponseWriter, r *http.Request) {
	fs, err := s.Drive.Folders(r.Context())
	if err != nil {
		s.driveFailed(w, r, err)
		return
	}
	out := make([]DriveFolder, 0, len(fs))
	for _, f := range fs {
		out = append(out, DriveFolder{ID: f.ID, Name: f.Name})
	}
	writeJSON(w, http.StatusOK, out)
}

// driveFolder is POST /api/drive/folder: {id} selects one of the panel's
// folders (422 on id for another), {name} creates one in My Drive root (or
// finds the panel's folder of that name) and selects it (422 on name for a bad
// name). It answers the new status and records drive.folder.
func (s *server) driveFolder(w http.ResponseWriter, r *http.Request) {
	var req DriveFolderRequest
	if !readJSON(w, r, &req) {
		return
	}
	before := s.Drive.Status()
	var f drive.Folder
	var err error
	if req.ID != "" {
		f, err = s.Drive.SelectFolder(r.Context(), req.ID)
	} else {
		f, err = s.Drive.CreateFolder(r.Context(), req.Name)
	}
	switch {
	case errors.Is(err, drive.ErrBadName):
		writeFieldError(w, "name", "invalid", msgBadFolderName)
		return
	case req.ID != "" && drive.CodeOf(err) == drive.CodeFolderMissing:
		writeFieldError(w, "id", drive.CodeFolderMissing, drive.Words(drive.CodeFolderMissing))
		return
	case err != nil:
		s.driveFailed(w, r, err)
		return
	}
	old := "none"
	if before.Folder != nil {
		old = strconv.Quote(before.Folder.Name)
	}
	s.record(r, store.Activity{Action: store.ActDriveFolder, Target: f.Name}, store.ResultOK,
		"folder: "+old+" → "+strconv.Quote(f.Name))
	writeJSON(w, http.StatusOK, toDriveStatus(s.Drive.Status()))
}

// driveTest is POST /api/drive/test: it uploads a 1 KB file to the folder and
// deletes it. A failure is {ok:false, code, message}; a test already running
// is 409 busy.
func (s *server) driveTest(w http.ResponseWriter, r *http.Request) {
	err := s.Drive.Test(r.Context())
	if err == nil {
		writeJSON(w, http.StatusOK, DriveTestResult{OK: true})
		return
	}
	if code, words, ok := driveCode(err); ok {
		writeJSON(w, http.StatusOK, DriveTestResult{Code: code, Message: words})
		return
	}
	s.driveFailed(w, r, err)
}

// driveDisconnect is POST /api/drive/disconnect: it revokes the token (best
// effort), forgets the connection, records drive.disconnect and answers the
// new status. A connect that landed meanwhile is kept: 409 busy, recorded as
// refused.
func (s *server) driveDisconnect(w http.ResponseWriter, r *http.Request) {
	before := s.Drive.Status()
	target := before.Email
	if target == "" {
		target = driveTarget
	}
	err := s.Drive.Disconnect(context.WithoutCancel(r.Context()))
	if errors.Is(err, drive.ErrConnectedMeanwhile) {
		s.record(r, store.Activity{Action: store.ActDriveDisconnect, Target: target}, store.ResultRefused, msgConnectedMeanwhile)
		writeJSON(w, http.StatusConflict, DriveError{Error: codeBusy, Message: msgConnectedMeanwhile})
		return
	}
	if err != nil {
		s.driveFailed(w, r, err)
		return
	}
	s.record(r, store.Activity{Action: store.ActDriveDisconnect, Target: target}, store.ResultOK, "")
	writeJSON(w, http.StatusOK, toDriveStatus(s.Drive.Status()))
}

// pageTemplate is the minimal HTML page of a browser navigation that cannot
// end in the app (the Drive callback).
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title></head>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 16px">
<h1 style="font-size:1.25rem">{{.Title}}</h1>
<p>{{.Message}}</p>
<p><a href="/settings">Back to Settings</a></p>
</body></html>
`))

// writePage writes the minimal HTML page with status.
func writePage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = pageTemplate.Execute(w, struct{ Title, Message string }{title, message})
}
