package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/oauth2"
)

// driveFileScope is the only scope the panel asks for: the files it creates.
const driveFileScope = "https://www.googleapis.com/auth/drive.file"

const folderMimeType = "application/vnd.google-apps.folder"

// Limits, as variables so tests can lower them.
var (
	// uploadChunk is the resumable chunk size (a multiple of 256 KiB).
	uploadChunk = 8 << 20
	// uploadStall fails an upload that makes no progress for this long.
	uploadStall = 2 * time.Minute
	// maxResumes is how many times one chunk is resumed after a transient error.
	maxResumes = 3
	// resumeBackoff is the wait before the first resume; it grows linearly.
	resumeBackoff = time.Second
	// callTimeout bounds one metadata or token call.
	callTimeout = 60 * time.Second
)

var (
	errStall       = errors.New("upload stalled")
	errCallTimeout = errors.New("request timed out")
	errSessionGone = errors.New("upload session expired")
	// errFinal marks an unreachable answer that a retry cannot fix (a 4xx that
	// maps to no other code).
	errFinal = errors.New("not retryable")
)

type googleEndpoints struct {
	Auth, Token, Revoke, API string
}

var defaultEndpoints = googleEndpoints{ //nolint:gosec // G101: public endpoint URLs, not credentials
	Auth:   "https://accounts.google.com/o/oauth2/auth",
	Token:  "https://oauth2.googleapis.com/token",
	Revoke: "https://oauth2.googleapis.com/revoke",
	API:    "https://www.googleapis.com",
}

// Google is the Client for Google Drive: golang.org/x/oauth2 for the tokens and
// plain REST calls to the Drive v3 API.
type Google struct {
	conf oauth2.Config
	ep   googleEndpoints
	hc   *http.Client
}

// NewGoogle returns the Google Drive client for an OAuth "Web application"
// client.
func NewGoogle(clientID, clientSecret string) *Google {
	return newGoogle(clientID, clientSecret, defaultEndpoints, &http.Client{})
}

func newGoogle(clientID, clientSecret string, ep googleEndpoints, hc *http.Client) *Google {
	return &Google{
		conf: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     oauth2.Endpoint{AuthURL: ep.Auth, TokenURL: ep.Token, AuthStyle: oauth2.AuthStyleInParams},
			Scopes:       []string{driveFileScope},
		},
		ep: ep,
		hc: hc,
	}
}

func (g *Google) oauthCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, g.hc)
}

// AuthURL asks for offline access with a fresh consent, so Google always
// returns a refresh token.
func (g *Google) AuthURL(state, redirect string) string {
	c := g.conf
	c.RedirectURL = redirect
	return c.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// Exchange trades the code for the refresh token.
func (g *Google) Exchange(ctx context.Context, code, redirect string) (string, error) {
	c := g.conf
	c.RedirectURL = redirect
	cctx, cancel := context.WithTimeoutCause(ctx, callTimeout, errCallTimeout)
	defer cancel()
	tok, err := c.Exchange(g.oauthCtx(cctx), code)
	if err != nil {
		return "", tokenErr(ctx, cctx, err)
	}
	if tok.RefreshToken == "" {
		return "", errCode(CodeNoAccess, "google returned no refresh token")
	}
	return tok.RefreshToken, nil
}

// Session binds the Drive calls to refresh. The access token is kept in memory
// and refreshed when it runs out.
func (g *Google) Session(refresh string) Session {
	return &googleSession{g: g, refresh: refresh}
}

// tokenErr reduces a token endpoint failure to a code. The response body is
// never kept.
func tokenErr(parent, cctx context.Context, err error) error {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return netErr(parent, cctx, err)
	}
	status := 0
	if re.Response != nil {
		status = re.Response.StatusCode
	}
	switch {
	case re.ErrorCode == "invalid_grant":
		return errCode(CodeTokenExpired, "google refused the token (invalid_grant)")
	case status >= 500 || status == http.StatusTooManyRequests:
		return errCode(CodeUnreachable, fmt.Sprintf("token endpoint: HTTP %d", status))
	default:
		return errCode(CodeNoAccess, fmt.Sprintf("token endpoint: HTTP %d %s", status, cleanWord(re.ErrorCode)))
	}
}

// netErr reduces a transport failure: the caller's own cancellation passes
// through, a stall or timeout of ours and a network error are unreachable. The
// request URL is dropped.
func netErr(parent, cctx context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if cctx.Err() != nil {
		return &Error{Code: CodeUnreachable, Err: context.Cause(cctx)}
	}
	// Only the transport's own message is kept (a dial or TLS failure); any other
	// text, which might quote an answer, is dropped.
	var ue *url.Error
	if errors.As(err, &ue) {
		return &Error{Code: CodeUnreachable, Err: fmt.Errorf("network: %w", ue.Err)}
	}
	return errCode(CodeUnreachable, "network or protocol failure")
}

// cleanWord keeps a short identifier from a Google answer (an error code or
// reason) and drops anything else.
func cleanWord(s string) string {
	if len(s) > 64 {
		return ""
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '.' {
			return ""
		}
	}
	return s
}

// statusErr maps a non-2xx Drive answer to a code and closes nothing; the
// caller closes the body.
func statusErr(resp *http.Response) *Error {
	var body struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
	reason := ""
	if len(body.Error.Errors) > 0 {
		reason = cleanWord(body.Error.Errors[0].Reason)
	}
	msg := "HTTP " + strconv.Itoa(resp.StatusCode)
	if reason != "" {
		msg += " " + reason
	}
	code := resp.StatusCode
	switch {
	case reason == "storageQuotaExceeded":
		return errCode(CodeQuota, msg)
	case code == http.StatusTooManyRequests || reason == "rateLimitExceeded" || reason == "userRateLimitExceeded":
		return errCode(CodeUnreachable, msg)
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return errCode(CodeNoAccess, msg)
	case code == http.StatusNotFound:
		return errCode(CodeFolderMissing, msg)
	case code >= 400 && code < 500:
		return &Error{Code: CodeUnreachable, Err: fmt.Errorf("%s (%w)", msg, errFinal)}
	default:
		return errCode(CodeUnreachable, msg)
	}
}

func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

type googleSession struct {
	g       *Google
	refresh string

	mu  sync.Mutex
	tok *oauth2.Token
}

// accessToken returns a valid access token, refreshing it with cctx when it has
// run out.
func (s *googleSession) accessToken(ctx, cctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok.Valid() {
		return s.tok.AccessToken, nil
	}
	t, err := s.g.conf.TokenSource(s.g.oauthCtx(cctx), &oauth2.Token{RefreshToken: s.refresh}).Token()
	if err != nil {
		return "", tokenErr(ctx, cctx, err)
	}
	s.tok = t
	return t.AccessToken, nil
}

func (s *googleSession) dropToken(access string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok != nil && s.tok.AccessToken == access {
		s.tok = nil
	}
}

// send sends the request build makes, with the access token. A 401 drops the
// token and tries once more with a fresh one, so a revoked refresh token shows
// as token_expired.
func (s *googleSession) send(ctx, cctx context.Context, build func() (*http.Request, error)) (*http.Response, error) {
	for try := 0; ; try++ {
		access, err := s.accessToken(ctx, cctx)
		if err != nil {
			return nil, err
		}
		req, err := build()
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+access)
		resp, err := s.g.hc.Do(req)
		if err != nil {
			return nil, netErr(ctx, cctx, err)
		}
		if resp.StatusCode == http.StatusUnauthorized && try == 0 {
			drainClose(resp)
			s.dropToken(access)
			continue
		}
		return resp, nil
	}
}

// call runs one JSON metadata call within callTimeout and decodes the answer
// into out (when not nil).
func (s *googleSession) call(ctx context.Context, method, u string, body []byte, out any) error {
	cctx, cancel := context.WithTimeoutCause(ctx, callTimeout, errCallTimeout)
	defer cancel()
	resp, err := s.send(ctx, cctx, func() (*http.Request, error) {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(cctx, method, u, rd)
		if err == nil && body != nil {
			req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		}
		return req, err
	})
	if err != nil {
		return err
	}
	defer drainClose(resp)
	if resp.StatusCode/100 != 2 {
		return statusErr(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
		if ctx.Err() != nil || cctx.Err() != nil {
			return netErr(ctx, cctx, err)
		}
		return errCode(CodeUnreachable, "unreadable answer from Drive")
	}
	return nil
}

func (s *googleSession) files() string { return s.g.ep.API + "/drive/v3/files" }

// qString quotes a value for a Drive search query.
func qString(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}

func (s *googleSession) Account(ctx context.Context) (string, error) {
	var about struct {
		User struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"user"`
	}
	if err := s.call(ctx, http.MethodGet, s.g.ep.API+"/drive/v3/about?fields=user(emailAddress)", nil, &about); err != nil {
		return "", err
	}
	if about.User.EmailAddress == "" {
		return "", errCode(CodeNoAccess, "drive gave no account email")
	}
	return about.User.EmailAddress, nil
}

// maxPages bounds a folder listing (100 folders a page).
const maxPages = 50

func (s *googleSession) search(ctx context.Context, q string, pageSize int, all bool) ([]Folder, error) {
	var out []Folder
	pageToken := ""
	for range maxPages {
		v := url.Values{
			"q":        {q},
			"fields":   {"nextPageToken,files(id,name)"},
			"pageSize": {strconv.Itoa(pageSize)},
			"orderBy":  {"name"},
			"spaces":   {"drive"},
		}
		if pageToken != "" {
			v.Set("pageToken", pageToken)
		}
		var page struct {
			NextPageToken string   `json:"nextPageToken"`
			Files         []Folder `json:"files"`
		}
		if err := s.call(ctx, http.MethodGet, s.files()+"?"+v.Encode(), nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Files...)
		if !all || page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return out, nil
}

func (s *googleSession) ListFolders(ctx context.Context) ([]Folder, error) {
	return s.search(ctx, "mimeType='"+folderMimeType+"' and trashed=false and 'root' in parents", 100, true)
}

// checkFolder answers folder_missing for a folder that is gone, trashed or not
// a folder.
func (s *googleSession) checkFolder(ctx context.Context, id string) error {
	var f struct {
		MimeType string `json:"mimeType"`
		Trashed  bool   `json:"trashed"`
	}
	if err := s.call(ctx, http.MethodGet, s.files()+"/"+url.PathEscape(id)+"?fields=id,mimeType,trashed", nil, &f); err != nil {
		return err
	}
	if f.Trashed || f.MimeType != folderMimeType {
		return errCode(CodeFolderMissing, "the folder is in the trash or is not a folder")
	}
	return nil
}

func (s *googleSession) CreateFolder(ctx context.Context, name, parentID string) (Folder, error) {
	if parentID != "" {
		if err := s.checkFolder(ctx, parentID); err != nil {
			return Folder{}, err
		}
	}
	return s.createFolder(ctx, name, parentID)
}

func (s *googleSession) createFolder(ctx context.Context, name, parentID string) (Folder, error) {
	meta := map[string]any{"name": name, "mimeType": folderMimeType}
	if parentID != "" {
		meta["parents"] = []string{parentID}
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return Folder{}, err
	}
	var f Folder
	if err := s.call(ctx, http.MethodPost, s.files()+"?fields=id,name", body, &f); err != nil {
		return Folder{}, err
	}
	return f, nil
}

func (s *googleSession) FindOrCreateFolder(ctx context.Context, name, parentID string) (Folder, error) {
	parent := "root"
	if parentID != "" {
		if err := s.checkFolder(ctx, parentID); err != nil {
			return Folder{}, err
		}
		parent = parentID
	}
	found, err := s.search(ctx, "mimeType='"+folderMimeType+"' and trashed=false and name="+qString(name)+
		" and "+qString(parent)+" in parents", 1, false)
	if err != nil {
		return Folder{}, err
	}
	if len(found) > 0 {
		return found[0], nil
	}
	return s.createFolder(ctx, name, parentID)
}

func (s *googleSession) Download(ctx context.Context, fileID string) (io.ReadCloser, error) {
	resp, err := s.send(ctx, ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, s.files()+"/"+url.PathEscape(fileID)+"?alt=media", nil)
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer drainClose(resp)
		return nil, statusErr(resp)
	}
	return resp.Body, nil
}

func (s *googleSession) Delete(ctx context.Context, fileID string) error {
	return s.call(ctx, http.MethodDelete, s.files()+"/"+url.PathEscape(fileID), nil, nil)
}

func (s *googleSession) Revoke(ctx context.Context) error {
	cctx, cancel := context.WithTimeoutCause(ctx, callTimeout, errCallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, s.g.ep.Revoke,
		strings.NewReader(url.Values{"token": {s.refresh}}.Encode()))
	if err != nil {
		return errCode(CodeUnreachable, "cannot build the revoke request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.g.hc.Do(req)
	if err != nil {
		return netErr(ctx, cctx, err)
	}
	defer drainClose(resp)
	s.mu.Lock()
	s.tok = nil
	s.mu.Unlock()
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusBadRequest:
		return errCode(CodeTokenExpired, "the token was already invalid")
	default:
		return errCode(CodeUnreachable, "revoke: HTTP "+strconv.Itoa(resp.StatusCode))
	}
}

// watchdog cancels an upload that has made no progress for uploadStall.
type watchdog struct {
	last atomic.Int64
	done chan struct{}
}

func startWatchdog(cancel context.CancelCauseFunc) *watchdog {
	w := &watchdog{done: make(chan struct{})}
	w.touch()
	stall := uploadStall
	go func() {
		t := time.NewTicker(max(stall/8, time.Millisecond))
		defer t.Stop()
		for {
			select {
			case <-w.done:
				return
			case <-t.C:
				if time.Since(time.Unix(0, w.last.Load())) > stall {
					cancel(errStall)
					return
				}
			}
		}
	}()
	return w
}

func (w *watchdog) touch() { w.last.Store(time.Now().UnixNano()) }
func (w *watchdog) stop()  { close(w.done) }

// activityReader is a chunk body: each read is progress for the watchdog. The
// transport reads it on its own goroutine, possibly after the answer is back,
// so it touches only the watchdog's atomic clock and never reports progress.
type activityReader struct {
	r io.Reader
	w *watchdog
}

func (a *activityReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		a.w.touch()
	}
	return n, err
}

// uploadState is Drive's answer to a chunk or a status query.
type uploadState struct {
	done     bool
	received int64
	id, link string
}

// Upload uses Drive's resumable protocol: one session, then uploadChunk-sized
// PUTs. A chunk that fails with a 5xx or a network error is resumed from what
// Drive reports it received, up to maxResumes times. progress is called only
// from this goroutine, with the bytes Drive has confirmed.
func (s *googleSession) Upload(ctx context.Context, folderID, name string, size int64, r io.Reader, progress func(int64)) (string, string, error) {
	if size < 0 {
		return "", "", errors.New("drive: negative upload size")
	}
	if err := s.checkFolder(ctx, folderID); err != nil {
		return "", "", err
	}
	uctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	w := startWatchdog(cancel)
	defer w.stop()

	loc, err := s.startUpload(ctx, uctx, w, folderID, name, size)
	if err != nil {
		return "", "", err
	}
	var reported int64
	report := func(n int64) {
		if progress != nil && n > reported {
			reported = n
			progress(n)
		}
	}
	buf := make([]byte, min(int64(uploadChunk), size))
	var offset int64
	for {
		n := min(int64(len(buf)), size-offset)
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return "", "", fmt.Errorf("drive: read the upload data: %w", err)
		}
		w.touch()
		chunkStart, pos := offset, offset
		attempts := 0
		for pos < chunkStart+n || n == 0 {
			from := pos
			st, err := s.putChunk(ctx, uctx, w, loc, buf[from-chunkStart:n], from, size)
			if err == nil && !st.done && n > 0 && st.received <= from {
				// Drive took nothing: a failure, so the loop cannot spin.
				err = errCode(CodeUnreachable, "upload made no progress")
			}
			if err != nil {
				if st, err = s.resume(ctx, uctx, w, loc, size, err, &attempts); err != nil {
					return "", "", err
				}
			}
			if st.done {
				report(size)
				return st.id, st.link, nil
			}
			if st.received < chunkStart || st.received > chunkStart+n {
				return "", "", errCode(CodeUnreachable, "upload out of step")
			}
			pos = st.received
			report(pos)
			if n == 0 {
				return "", "", errCode(CodeUnreachable, "upload did not finish")
			}
		}
		offset = chunkStart + n
		report(offset)
		if offset >= size {
			return "", "", errCode(CodeUnreachable, "upload did not finish")
		}
	}
}

// retryable says whether a resume may follow err: a transient Drive failure,
// while neither the caller nor the watchdog has given up.
func retryable(ctx, uctx context.Context, err error) bool {
	return ctx.Err() == nil && uctx.Err() == nil && CodeOf(err) == CodeUnreachable &&
		!errors.Is(err, errSessionGone) && !errors.Is(err, errFinal)
}

// resume waits, then asks Drive what it received, counting each failure
// against maxResumes.
func (s *googleSession) resume(ctx, uctx context.Context, w *watchdog, loc string, size int64, err error, attempts *int) (uploadState, error) {
	for {
		if !retryable(ctx, uctx, err) {
			return uploadState{}, err
		}
		*attempts++
		if *attempts > maxResumes {
			return uploadState{}, err
		}
		t := time.NewTimer(resumeBackoff * time.Duration(*attempts))
		select {
		case <-t.C:
		case <-uctx.Done():
			t.Stop()
			return uploadState{}, netErr(ctx, uctx, uctx.Err())
		}
		w.touch()
		var st uploadState
		if st, err = s.queryUpload(ctx, uctx, w, loc, size); err == nil {
			return st, nil
		}
	}
}

func (s *googleSession) startUpload(ctx, uctx context.Context, w *watchdog, folderID, name string, size int64) (string, error) {
	meta, err := json.Marshal(map[string]any{"name": name, "parents": []string{folderID}})
	if err != nil {
		return "", err
	}
	for attempt := 0; ; attempt++ {
		resp, err := s.send(ctx, uctx, func() (*http.Request, error) {
			req, err := http.NewRequestWithContext(uctx, http.MethodPost,
				s.g.ep.API+"/upload/drive/v3/files?uploadType=resumable&fields=id,webViewLink", bytes.NewReader(meta))
			if err == nil {
				req.Header.Set("Content-Type", "application/json; charset=UTF-8")
				req.Header.Set("X-Upload-Content-Type", "application/octet-stream")
				req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(size, 10))
			}
			return req, err
		})
		w.touch()
		if err == nil {
			loc := resp.Header.Get("Location")
			if resp.StatusCode/100 != 2 {
				err = statusErr(resp)
			} else if !s.sameOrigin(loc) {
				err = errCode(CodeUnreachable, "unexpected upload location")
			}
			drainClose(resp)
			if err == nil {
				return loc, nil
			}
		}
		if !retryable(ctx, uctx, err) || attempt >= maxResumes {
			return "", err
		}
		t := time.NewTimer(resumeBackoff * time.Duration(attempt+1))
		select {
		case <-t.C:
		case <-uctx.Done():
			t.Stop()
			return "", netErr(ctx, uctx, uctx.Err())
		}
		w.touch()
	}
}

// sameOrigin keeps the access token on the Drive API's own origin.
func (s *googleSession) sameOrigin(loc string) bool {
	u, err := url.Parse(loc)
	api, err2 := url.Parse(s.g.ep.API)
	return err == nil && err2 == nil && u.Scheme == api.Scheme && u.Host == api.Host
}

func (s *googleSession) putChunk(ctx, uctx context.Context, w *watchdog, loc string, data []byte, pos, size int64) (uploadState, error) {
	contentRange := "bytes */0"
	if size > 0 {
		contentRange = fmt.Sprintf("bytes %d-%d/%d", pos, pos+int64(len(data))-1, size)
	}
	resp, err := s.send(ctx, uctx, func() (*http.Request, error) {
		var body io.Reader = http.NoBody
		if len(data) > 0 {
			body = &activityReader{r: bytes.NewReader(data), w: w}
		}
		req, err := http.NewRequestWithContext(uctx, http.MethodPut, loc, body)
		if err == nil {
			req.ContentLength = int64(len(data))
			req.Header.Set("Content-Range", contentRange)
		}
		return req, err
	})
	if err != nil {
		return uploadState{}, err
	}
	return s.uploadAnswer(ctx, uctx, w, resp)
}

func (s *googleSession) queryUpload(ctx, uctx context.Context, w *watchdog, loc string, size int64) (uploadState, error) {
	resp, err := s.send(ctx, uctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(uctx, http.MethodPut, loc, http.NoBody)
		if err == nil {
			req.Header.Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		}
		return req, err
	})
	if err != nil {
		return uploadState{}, err
	}
	return s.uploadAnswer(ctx, uctx, w, resp)
}

// uploadAnswer reads a chunk or status answer: 200/201 with the file, or 308
// with the received range.
func (s *googleSession) uploadAnswer(ctx, uctx context.Context, w *watchdog, resp *http.Response) (uploadState, error) {
	w.touch()
	defer drainClose(resp)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var f struct {
			ID          string `json:"id"`
			WebViewLink string `json:"webViewLink"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&f); err != nil || f.ID == "" {
			if ctx.Err() != nil || uctx.Err() != nil {
				return uploadState{}, netErr(ctx, uctx, err)
			}
			return uploadState{}, errCode(CodeUnreachable, "unreadable answer from Drive")
		}
		return uploadState{done: true, id: f.ID, link: f.WebViewLink}, nil
	case http.StatusPermanentRedirect:
		st := uploadState{}
		if rg := resp.Header.Get("Range"); rg != "" {
			_, last, ok := strings.Cut(strings.TrimPrefix(rg, "bytes="), "-")
			n, err := strconv.ParseInt(last, 10, 64)
			if !ok || err != nil || !strings.HasPrefix(rg, "bytes=0-") {
				return uploadState{}, errCode(CodeUnreachable, "bad upload range")
			}
			st.received = n + 1
		}
		return st, nil
	case http.StatusNotFound, http.StatusGone:
		return uploadState{}, &Error{Code: CodeUnreachable, Err: errSessionGone}
	default:
		return uploadState{}, statusErr(resp)
	}
}
