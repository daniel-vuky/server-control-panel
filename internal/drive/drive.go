// Package drive is the panel's Google Drive connection: the Client seam with
// its Google and fake implementations, and the Service that runs the OAuth
// flow, seals the refresh token and caches the connection state in memory.
//
// No token ever leaves this package: errors are reduced to the codes below
// before they are returned, and Status carries no token.
package drive

import (
	"context"
	"errors"
	"io"
)

// Folder is a Drive folder the panel created.
type Folder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// The error codes the UI words (spec §2).
const (
	CodeTokenExpired  = "token_expired"
	CodeNoAccess      = "no_access"
	CodeFolderMissing = "folder_missing"
	CodeQuota         = "quota"
	CodeUnreachable   = "unreachable"
)

// TestingNote is added to the token_expired words: a Google Cloud app left in
// Testing issues refresh tokens that expire after 7 days.
const TestingNote = "If the Google Cloud app is still in Testing, publish it: tokens issued in Testing expire after 7 days."

// Error is a Drive failure reduced to a code. Err is the panel's own short
// description; it never holds a token or a response body.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return "drive: " + e.Code
	}
	return "drive: " + e.Code + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func errCode(code, msg string) *Error { return &Error{Code: code, Err: errors.New(msg)} }

// CodeOf returns the code of a *Error in err's chain, or "".
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Words gives the words the UI shows for a code (spec §2), or "" for another
// code.
func Words(code string) string {
	switch code {
	case CodeTokenExpired:
		return "The Google Drive token has expired. Reconnect Google Drive. " + TestingNote
	case CodeNoAccess:
		return "The panel no longer has access to this folder."
	case CodeFolderMissing:
		return "The destination folder is missing or in the trash. Choose or create a folder."
	case CodeQuota:
		return "Google Drive is full."
	case CodeUnreachable:
		return "Google Drive is unreachable."
	}
	return ""
}

// Client is the Drive seam: Google, or the fake in dev builds and tests.
type Client interface {
	// AuthURL is the consent page for state; Google sends the browser back to
	// redirect.
	AuthURL(state, redirect string) string
	// Exchange trades an authorization code for a refresh token. redirect must be
	// the one AuthURL was given.
	Exchange(ctx context.Context, code, redirect string) (refresh string, err error)
	// Session binds the Drive calls to a refresh token. It does no I/O; the
	// access token is fetched on first use.
	Session(refresh string) Session
}

// Session is the Drive API, bound to one refresh token. Every error is a *Error,
// or the caller's context error.
type Session interface {
	// Account is the connected account's email.
	Account(ctx context.Context) (string, error)
	// ListFolders lists the folders the panel created in My Drive root, not
	// trashed, by name.
	ListFolders(ctx context.Context) ([]Folder, error)
	// CreateFolder creates a folder under parentID ("" = My Drive root).
	CreateFolder(ctx context.Context, name, parentID string) (Folder, error)
	// FindOrCreateFolder returns the panel's folder called name under parentID
	// ("" = root), creating it when there is none. A missing or trashed parent is
	// folder_missing.
	FindOrCreateFolder(ctx context.Context, name, parentID string) (Folder, error)
	// Upload stores size bytes from r as name in folderID, calling progress
	// (when not nil) with the bytes sent so far. progress is called serially,
	// from the goroutine that called Upload, never after Upload returns. A
	// missing or trashed folder is folder_missing.
	Upload(ctx context.Context, folderID, name string, size int64, r io.Reader, progress func(int64)) (fileID, link string, err error)
	// Download streams a file's content.
	Download(ctx context.Context, fileID string) (io.ReadCloser, error)
	// Delete removes a file or folder for good.
	Delete(ctx context.Context, fileID string) error
	// Revoke invalidates the refresh token.
	Revoke(ctx context.Context) error
}
