package drive

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daniel-vuky/server-control-panel/internal/config"
	"github.com/daniel-vuky/server-control-panel/internal/secret"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The connection states Status reports.
const (
	StateNotConfigured = "not_configured"
	StateNotConnected  = "not_connected"
	StateConnected     = "connected"
	StateExpired       = "expired"
)

// Status is the Drive connection as the UI sees it. It never carries a token.
// Reason says why Drive is not configured; it is empty otherwise.
type Status struct {
	State  string  `json:"state"`
	Email  string  `json:"email"`
	Folder *Folder `json:"folder"`
	Reason string  `json:"reason"`
}

var (
	// ErrBadState is an unknown, used, expired or foreign OAuth state.
	ErrBadState = errors.New("drive: this link has expired")
	// ErrNotConfigured is returned while Drive is not set up on this server.
	ErrNotConfigured = errors.New("drive: Google Drive is not set up on this server")
	// ErrNotConnected is returned when no account is connected.
	ErrNotConnected = errors.New("drive: Google Drive is not connected")
	// ErrBadName is a folder name that is empty, longer than 100 characters or
	// has a /.
	ErrBadName = errors.New("drive: a folder name is 1 to 100 characters, with no /")
	// ErrBusy is returned while a connection test runs.
	ErrBusy = errors.New("drive: a connection test is already running")
)

const (
	callbackPath    = "/api/drive/callback"
	defaultReturnTo = "/settings"
	maxFolderName   = 100
	maxReturnTo     = 2048
)

// Service is the panel's one Drive connection. It runs the OAuth flow, keeps the
// refresh token sealed in the store and caches the status in memory.
//
// Locking: mu guards the cache and is never held while calling out (store,
// network, callbacks), so the hub may read Status under its own lock. writeMu
// serialises the store writes with the cache swaps that follow them; the hub
// never takes it. OnChange callbacks run after both are released.
type Service struct {
	key       []byte
	publicURL string
	reason    string
	st        *store.Store
	client    Client
	now       func() time.Time

	writeMu sync.Mutex
	testing atomic.Bool

	mu       sync.RWMutex
	status   Status
	sess     *serviceSession // cached while connected; nil until first use
	gen      uint64          // bumped whenever the token changes or is dropped
	onChange []func()
}

// NewService loads the connection from the store. With cfg.DriveReason() != ""
// it is not_configured and never touches the store. A stored token the key
// cannot open marks the connection expired.
func NewService(cfg config.Config, st *store.Store, c Client, now func() time.Time) (*Service, error) {
	s := &Service{
		key: cfg.SecretKey, publicURL: cfg.PublicURL, reason: cfg.DriveReason(),
		st: st, client: c, now: now,
	}
	if s.reason != "" {
		s.status = Status{State: StateNotConfigured, Reason: s.reason}
		return s, nil
	}
	ctx := context.Background()
	row, ok, err := st.Drive(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		s.status = Status{State: StateNotConnected}
		return s, nil
	}
	if row.State == StateConnected {
		if _, err := secret.Open(s.key, row.RefreshTokenEnc); err != nil {
			if err := st.SetDriveState(ctx, StateExpired); err != nil {
				return nil, err
			}
			row.State = StateExpired
		}
	}
	s.status = statusFromRow(row)
	return s, nil
}

func statusFromRow(row store.DriveRow) Status {
	st := Status{State: row.State, Email: row.Email}
	if st.State != StateConnected {
		st.State = StateExpired
	}
	if row.FolderID != "" {
		st.Folder = &Folder{ID: row.FolderID, Name: row.FolderName}
	}
	return st
}

// Status is the cached connection; it never reads the store.
func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.status
	if st.Folder != nil {
		f := *st.Folder
		st.Folder = &f
	}
	return st
}

// OnChange registers fn, called after every status change, outside the
// Service's locks.
func (s *Service) OnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = append(s.onChange, fn)
}

func (s *Service) fire() {
	s.mu.RLock()
	fns := append([]func(){}, s.onChange...)
	s.mu.RUnlock()
	for _, fn := range fns {
		fn()
	}
}

// CallbackURL is the redirect URI registered at Google:
// <PANEL_PUBLIC_URL>/api/drive/callback.
func (s *Service) CallbackURL() string { return s.publicURL + callbackPath }

// SanitizeReturnTo keeps a same-site path: exactly one leading /, no //, no \,
// no control characters and no scheme. Anything else is /settings.
func SanitizeReturnTo(p string) string {
	if len(p) > maxReturnTo || !strings.HasPrefix(p, "/") || strings.Contains(p, "//") ||
		strings.Contains(p, `\`) || !utf8.ValidString(p) {
		return defaultReturnTo
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return defaultReturnTo
		}
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return defaultReturnTo
	}
	return p
}

// withDrive adds drive=<result> (and code=<code>) to a sanitised returnTo,
// replacing any left from an earlier return.
func withDrive(returnTo, result, code string) string {
	u, err := url.Parse(returnTo)
	if err != nil {
		u = &url.URL{Path: defaultReturnTo}
	}
	if q := u.Query(); q.Has("drive") || q.Has("code") {
		q.Del("drive")
		q.Del("code")
		u.RawQuery = q.Encode()
	}
	extra := "drive=" + url.QueryEscape(result)
	if code != "" {
		extra += "&code=" + url.QueryEscape(code)
	}
	if u.RawQuery == "" {
		u.RawQuery = extra
	} else {
		u.RawQuery += "&" + extra
	}
	return u.String()
}

// StartConnect stores a fresh single-use state for actor and returns the
// consent URL. returnTo is sanitised.
func (s *Service) StartConnect(ctx context.Context, actor, returnTo string) (string, error) {
	if s.reason != "" {
		return "", ErrNotConfigured
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	redirect := s.CallbackURL()
	if err := s.st.PutOAuthState(ctx, state, actor, SanitizeReturnTo(returnTo), redirect, s.now()); err != nil {
		return "", err
	}
	return s.client.AuthURL(state, redirect), nil
}

// FinishConnect handles Google's redirect. An unknown, used, expired or foreign
// state is ErrBadState. Otherwise it answers the returnTo stored with the state,
// with drive=declined (access_denied or no code), drive=error&code=<code> (the
// exchange or the account read failed), or drive=connected. A connect stores the
// sealed token, keeps the folder when the same account reconnects and clears it
// for another. Other errors are the panel's own (the store). The caller records
// the activity.
func (s *Service) FinishConnect(ctx context.Context, actor, state, code, errParam string) (string, error) {
	if state == "" {
		return "", ErrBadState
	}
	owner, returnTo, redirect, ok, err := s.st.TakeOAuthState(ctx, state, s.now())
	if err != nil {
		return "", err
	}
	if !ok || owner != actor {
		return "", ErrBadState
	}
	switch {
	case errParam == "access_denied" || (errParam == "" && code == ""):
		return withDrive(returnTo, "declined", ""), nil
	case errParam != "":
		return withDrive(returnTo, "error", CodeNoAccess), nil
	case s.reason != "":
		return withDrive(returnTo, "error", StateNotConfigured), nil
	}
	refresh, err := s.client.Exchange(ctx, code, redirect)
	if err != nil {
		return connectFailed(ctx, returnTo, err)
	}
	inner := s.client.Session(refresh)
	email, err := inner.Account(ctx)
	if err != nil {
		return connectFailed(ctx, returnTo, err)
	}
	sealed, err := secret.Seal(s.key, refresh)
	if err != nil {
		return "", err
	}

	s.writeMu.Lock()
	old, had, err := s.st.Drive(ctx)
	if err != nil {
		s.writeMu.Unlock()
		return "", err
	}
	row := store.DriveRow{
		RefreshTokenEnc: sealed, Email: email, State: StateConnected,
		ConnectedBy: actor, ConnectedAt: s.now(),
	}
	if had && strings.EqualFold(old.Email, email) {
		row.FolderID, row.FolderName = old.FolderID, old.FolderName
	}
	if err := s.st.SaveDrive(ctx, row); err != nil {
		s.writeMu.Unlock()
		return "", err
	}
	s.mu.Lock()
	s.gen++
	s.sess = &serviceSession{s: s, inner: inner, gen: s.gen}
	s.status = statusFromRow(row)
	s.mu.Unlock()
	s.writeMu.Unlock()
	s.fire()
	return withDrive(returnTo, "connected", ""), nil
}

// connectFailed turns a Drive failure into drive=error&code=<code>; the caller's
// cancellation stays an error.
func connectFailed(ctx context.Context, returnTo string, err error) (string, error) {
	code := CodeOf(err)
	if code == "" {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		code = CodeUnreachable
	}
	return withDrive(returnTo, "error", code), nil
}

// session returns the cached session (opening the sealed token on first use)
// and the destination folder. needFolder makes a missing folder folder_missing.
func (s *Service) session(ctx context.Context, needFolder bool) (*serviceSession, Folder, error) {
	s.mu.RLock()
	st, cached, gen := s.status, s.sess, s.gen
	s.mu.RUnlock()
	switch st.State {
	case StateNotConfigured:
		return nil, Folder{}, ErrNotConfigured
	case StateNotConnected:
		return nil, Folder{}, ErrNotConnected
	case StateExpired:
		return nil, Folder{}, errCode(CodeTokenExpired, "reconnect Google Drive")
	}
	var folder Folder
	if st.Folder != nil {
		folder = *st.Folder
	} else if needFolder {
		return nil, Folder{}, errCode(CodeFolderMissing, "no destination folder is chosen")
	}
	if cached != nil {
		return cached, folder, nil
	}
	row, ok, err := s.st.Drive(ctx)
	if err != nil {
		return nil, Folder{}, err
	}
	if !ok {
		return nil, Folder{}, ErrNotConnected
	}
	refresh, err := secret.Open(s.key, row.RefreshTokenEnc)
	if err != nil {
		s.markExpired(ctx, gen)
		return nil, Folder{}, errCode(CodeTokenExpired, "the stored token cannot be opened")
	}
	sess := &serviceSession{s: s, inner: s.client.Session(refresh), gen: gen}
	s.mu.Lock()
	if s.gen == gen {
		if s.sess == nil {
			s.sess = sess
		} else {
			sess = s.sess
		}
	}
	s.mu.Unlock()
	return sess, folder, nil
}

// Session is the connected session and the destination folder, for the backup
// manager. The access token is refreshed lazily; a token_expired from any call
// marks the connection expired (store and cache) and calls OnChange.
func (s *Service) Session(ctx context.Context) (Session, Folder, error) {
	sess, folder, err := s.session(ctx, true)
	if err != nil {
		return nil, Folder{}, err
	}
	return sess, folder, nil
}

// Download streams a file the panel uploaded. It needs a connection but not a
// destination folder. A token_expired marks the connection expired.
func (s *Service) Download(ctx context.Context, fileID string) (io.ReadCloser, error) {
	sess, _, err := s.session(ctx, false)
	if err != nil {
		return nil, err
	}
	return sess.Download(ctx, fileID)
}

// markExpired records token_expired for the token of generation gen; a newer
// connection is left alone.
func (s *Service) markExpired(ctx context.Context, gen uint64) {
	s.writeMu.Lock()
	s.mu.RLock()
	current := s.gen == gen && s.status.State == StateConnected
	s.mu.RUnlock()
	if !current {
		s.writeMu.Unlock()
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	if err := s.st.SetDriveState(wctx, StateExpired); err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Default().Warn("drive: cannot store the expired state", "error", err)
	}
	cancel()
	s.mu.Lock()
	s.gen++
	s.sess = nil
	s.status.State = StateExpired
	s.mu.Unlock()
	s.writeMu.Unlock()
	s.fire()
}

// Folders lists the folders the panel created in My Drive root.
func (s *Service) Folders(ctx context.Context) ([]Folder, error) {
	sess, _, err := s.session(ctx, false)
	if err != nil {
		return nil, err
	}
	return sess.ListFolders(ctx)
}

// SelectFolder makes one of the panel's folders the destination. Another id is
// folder_missing.
func (s *Service) SelectFolder(ctx context.Context, id string) (Folder, error) {
	sess, _, err := s.session(ctx, false)
	if err != nil {
		return Folder{}, err
	}
	list, err := sess.ListFolders(ctx)
	if err != nil {
		return Folder{}, err
	}
	for _, f := range list {
		if f.ID == id {
			return f, s.setFolder(ctx, f)
		}
	}
	return Folder{}, errCode(CodeFolderMissing, "not one of the panel's folders")
}

// CreateFolder creates (or finds) a folder called name in My Drive root and
// selects it. The name is trimmed and must be 1 to 100 characters with no /.
func (s *Service) CreateFolder(ctx context.Context, name string) (Folder, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxFolderName || strings.Contains(name, "/") || !utf8.ValidString(name) {
		return Folder{}, ErrBadName
	}
	sess, _, err := s.session(ctx, false)
	if err != nil {
		return Folder{}, err
	}
	f, err := sess.FindOrCreateFolder(ctx, name, "")
	if err != nil {
		return Folder{}, err
	}
	return f, s.setFolder(ctx, f)
}

func (s *Service) setFolder(ctx context.Context, f Folder) error {
	s.writeMu.Lock()
	if err := s.st.SetDriveFolder(ctx, f.ID, f.Name); err != nil {
		s.writeMu.Unlock()
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotConnected
		}
		return err
	}
	s.mu.Lock()
	s.status.Folder = &Folder{ID: f.ID, Name: f.Name}
	s.mu.Unlock()
	s.writeMu.Unlock()
	s.fire()
	return nil
}

// testFile is the 1 KB of text Test uploads.
var testFile = bytes.Repeat([]byte("Server Control Panel connection test. This file is deleted right away.\n"), 15)[:1024]

// Test uploads scp-test-<timestamp>.txt to the folder and deletes it. One runs
// at a time; another gets ErrBusy.
func (s *Service) Test(ctx context.Context) error {
	if !s.testing.CompareAndSwap(false, true) {
		return ErrBusy
	}
	defer s.testing.Store(false)
	sess, folder, err := s.session(ctx, true)
	if err != nil {
		return err
	}
	name := "scp-test-" + s.now().UTC().Format("20060102-150405") + ".txt"
	id, _, err := sess.Upload(ctx, folder.ID, name, int64(len(testFile)), bytes.NewReader(testFile), nil)
	if err != nil {
		return err
	}
	return sess.Delete(ctx, id)
}

// Disconnect revokes the token at Google (best effort; a failure is logged with
// its code only) and deletes the connection. The caller records the activity.
func (s *Service) Disconnect(ctx context.Context) error {
	if s.reason != "" {
		return ErrNotConfigured
	}
	row, ok, err := s.st.Drive(ctx)
	if err != nil {
		return err
	}
	if ok {
		if refresh, err := secret.Open(s.key, row.RefreshTokenEnc); err == nil {
			if err := s.client.Session(refresh).Revoke(ctx); err != nil {
				slog.Default().Warn("drive: revoking the token failed", "code", CodeOf(err))
			}
		}
	}
	s.writeMu.Lock()
	if err := s.st.DeleteDrive(ctx); err != nil {
		s.writeMu.Unlock()
		return err
	}
	s.mu.Lock()
	changed := s.status.State != StateNotConnected
	s.gen++
	s.sess = nil
	s.status = Status{State: StateNotConnected}
	s.mu.Unlock()
	s.writeMu.Unlock()
	if changed {
		s.fire()
	}
	return nil
}

// serviceSession passes calls to the client's session and marks the connection
// expired on token_expired.
type serviceSession struct {
	s     *Service
	inner Session
	gen   uint64
}

func (w *serviceSession) check(ctx context.Context, err error) error {
	if CodeOf(err) == CodeTokenExpired {
		w.s.markExpired(ctx, w.gen)
	}
	return err
}

func (w *serviceSession) Account(ctx context.Context) (string, error) {
	email, err := w.inner.Account(ctx)
	return email, w.check(ctx, err)
}

func (w *serviceSession) ListFolders(ctx context.Context) ([]Folder, error) {
	f, err := w.inner.ListFolders(ctx)
	return f, w.check(ctx, err)
}

func (w *serviceSession) CreateFolder(ctx context.Context, name, parentID string) (Folder, error) {
	f, err := w.inner.CreateFolder(ctx, name, parentID)
	return f, w.check(ctx, err)
}

func (w *serviceSession) FindOrCreateFolder(ctx context.Context, name, parentID string) (Folder, error) {
	f, err := w.inner.FindOrCreateFolder(ctx, name, parentID)
	return f, w.check(ctx, err)
}

func (w *serviceSession) Upload(ctx context.Context, folderID, name string, size int64, r io.Reader, progress func(int64)) (string, string, error) {
	id, link, err := w.inner.Upload(ctx, folderID, name, size, r, progress)
	return id, link, w.check(ctx, err)
}

func (w *serviceSession) Download(ctx context.Context, fileID string) (io.ReadCloser, error) {
	rc, err := w.inner.Download(ctx, fileID)
	return rc, w.check(ctx, err)
}

func (w *serviceSession) Delete(ctx context.Context, fileID string) error {
	return w.check(ctx, w.inner.Delete(ctx, fileID))
}

func (w *serviceSession) Revoke(ctx context.Context) error {
	return w.check(ctx, w.inner.Revoke(ctx))
}
