package drive

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// mockGoogle is an in-memory stand-in for Google's OAuth token endpoint and the
// parts of the Drive v3 API the client uses.
type mockGoogle struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	email    string
	codes    map[string]string // code → redirect_uri it was issued for
	refresh  map[string]bool   // valid refresh tokens
	access   map[string]string // valid access token → its refresh token
	files    map[string]*mockFile
	sessions map[string]*mockUpload
	// expiresIn is the access token lifetime the token endpoint answers.
	expiresIn int
	// refreshCount counts refresh_token grants.
	refreshCount int
	// putHook, when set, may answer a chunk PUT itself (return true). It gets the
	// upload, the PUT count so far (1-based) and the chunk body.
	putHook func(u *mockUpload, n int, body []byte, w http.ResponseWriter) bool
	// earlyPut, when set, may answer a chunk PUT before its body is read.
	earlyPut func(w http.ResponseWriter) bool
	puts     int
	queries  int
	// lastAuthQuery is the query of the last /drive/v3/about call.
	lastAboutQuery string
}

type mockFile struct {
	id, name, mimeType, parent string
	trashed                    bool
	content                    []byte
}

type mockUpload struct {
	name, parent string
	size         int64
	data         []byte
}

const folderMime = "application/vnd.google-apps.folder"

func newMockGoogle(t *testing.T) *mockGoogle {
	t.Helper()
	m := &mockGoogle{
		t: t, email: "owner@example.com", expiresIn: 3600,
		codes: map[string]string{}, refresh: map[string]bool{}, access: map[string]string{},
		files: map[string]*mockFile{}, sessions: map[string]*mockUpload{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", m.token)
	mux.HandleFunc("POST /revoke", m.revoke)
	mux.HandleFunc("GET /drive/v3/about", m.auth(m.about))
	mux.HandleFunc("GET /drive/v3/files", m.auth(m.list))
	mux.HandleFunc("POST /drive/v3/files", m.auth(m.create))
	mux.HandleFunc("GET /drive/v3/files/{id}", m.auth(m.get))
	mux.HandleFunc("DELETE /drive/v3/files/{id}", m.auth(m.delete))
	mux.HandleFunc("POST /upload/drive/v3/files", m.auth(m.initUpload))
	mux.HandleFunc("PUT /upload/session/{sid}", m.auth(m.put))
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

// client is a Google client pointed at the mock.
func (m *mockGoogle) client() *Google {
	return newGoogle("client-id", "client-secret", googleEndpoints{
		Auth:   m.srv.URL + "/auth",
		Token:  m.srv.URL + "/token",
		Revoke: m.srv.URL + "/revoke",
		API:    m.srv.URL,
	}, m.srv.Client())
}

// issueCode returns an authorization code valid for redirect.
func (m *mockGoogle) issueCode(redirect string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	code := "code-" + randomHex(8)
	m.codes[code] = redirect
	return code
}

// expireAccess drops every access token, as if they had expired at Google.
func (m *mockGoogle) expireAccess() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.access = map[string]string{}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, reason string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": status, "message": reason, "errors": []map[string]string{{"reason": reason}},
	}})
}

func (m *mockGoogle) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.Form.Get("client_id") != "client-id" || r.Form.Get("client_secret") != "client-secret" {
		writeJSON(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	var refresh string
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		redirect, ok := m.codes[r.Form.Get("code")]
		if !ok || redirect != r.Form.Get("redirect_uri") {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		delete(m.codes, r.Form.Get("code"))
		refresh = "refresh-" + randomHex(16)
		m.refresh[refresh] = true
	case "refresh_token":
		refresh = r.Form.Get("refresh_token")
		if !m.refresh[refresh] {
			// Google never echoes the token; the mock does, so a test can prove the
			// client never passes the body on.
			writeJSON(w, 400, map[string]string{"error": "invalid_grant", "error_description": "bad " + refresh})
			return
		}
		m.refreshCount++
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	access := "access-" + randomHex(16)
	m.access[access] = refresh
	body := map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": m.expiresIn}
	if r.Form.Get("grant_type") == "authorization_code" {
		body["refresh_token"] = refresh
	}
	writeJSON(w, 200, body)
}

func (m *mockGoogle) revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(400)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tok := r.Form.Get("token")
	if !m.refresh[tok] {
		writeJSON(w, 400, map[string]string{"error": "invalid_token"})
		return
	}
	delete(m.refresh, tok)
	for a, rt := range m.access {
		if rt == tok {
			delete(m.access, a)
		}
	}
	w.WriteHeader(200)
}

func (m *mockGoogle) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		m.mu.Lock()
		_, valid := m.access[tok]
		m.mu.Unlock()
		if !ok || !valid {
			apiError(w, 401, "authError")
			return
		}
		h(w, r)
	}
}

func (m *mockGoogle) about(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.lastAboutQuery = r.URL.RawQuery
	email := m.email
	m.mu.Unlock()
	writeJSON(w, 200, map[string]any{"user": map[string]string{"emailAddress": email}})
}

var clauseRE = regexp.MustCompile(`^(?:mimeType='(.*)'|trashed=false|name='(.*)'|'(.*)' in parents)$`)

func unq(s string) string {
	return strings.NewReplacer(`\'`, `'`, `\\`, `\`).Replace(s)
}

func (m *mockGoogle) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var mime, name, parent string
	var hasName, hasParent, notTrashed bool
	for _, c := range strings.Split(q, " and ") {
		sub := clauseRE.FindStringSubmatch(c)
		if sub == nil {
			m.t.Errorf("mock: unexpected q clause %q", c)
			apiError(w, 400, "invalid")
			return
		}
		switch {
		case strings.HasPrefix(c, "mimeType="):
			mime = unq(sub[1])
		case c == "trashed=false":
			notTrashed = true
		case strings.HasPrefix(c, "name="):
			name, hasName = unq(sub[2]), true
		default:
			parent, hasParent = unq(sub[3]), true
		}
	}
	m.mu.Lock()
	var out []map[string]string
	for _, f := range m.files {
		if (mime != "" && f.mimeType != mime) || (notTrashed && f.trashed) ||
			(hasName && f.name != name) || (hasParent && f.parent != parent) {
			continue
		}
		out = append(out, map[string]string{"id": f.id, "name": f.name})
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i]["name"] < out[j]["name"] })
	// One item per page, to exercise paging.
	start := 0
	if pt := r.URL.Query().Get("pageToken"); pt != "" {
		start, _ = strconv.Atoi(pt)
	}
	page := map[string]any{"files": []map[string]string{}}
	if start < len(out) {
		page["files"] = out[start : start+1]
		if start+1 < len(out) {
			page["nextPageToken"] = strconv.Itoa(start + 1)
		}
	}
	writeJSON(w, 200, page)
}

func (m *mockGoogle) create(w http.ResponseWriter, r *http.Request) {
	var meta struct {
		Name     string   `json:"name"`
		MimeType string   `json:"mimeType"`
		Parents  []string `json:"parents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
		apiError(w, 400, "parseError")
		return
	}
	parent := "root"
	if len(meta.Parents) > 0 {
		parent = meta.Parents[0]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if parent != "root" && m.files[parent] == nil {
		apiError(w, 404, "notFound")
		return
	}
	f := &mockFile{id: "id" + randomHex(6), name: meta.Name, mimeType: meta.MimeType, parent: parent}
	m.files[f.id] = f
	writeJSON(w, 200, map[string]string{"id": f.id, "name": f.name})
}

func (m *mockGoogle) get(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	f := m.files[r.PathValue("id")]
	m.mu.Unlock()
	if f == nil {
		apiError(w, 404, "notFound")
		return
	}
	if r.URL.Query().Get("alt") == "media" {
		_, _ = w.Write(f.content)
		return
	}
	writeJSON(w, 200, map[string]any{"id": f.id, "name": f.name, "mimeType": f.mimeType, "trashed": f.trashed})
}

func (m *mockGoogle) delete(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := r.PathValue("id")
	if m.files[id] == nil {
		apiError(w, 404, "notFound")
		return
	}
	var drop func(id string)
	drop = func(id string) {
		delete(m.files, id)
		for cid, c := range m.files {
			if c.parent == id {
				drop(cid)
			}
		}
	}
	drop(id)
	w.WriteHeader(204)
}

func (m *mockGoogle) initUpload(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("uploadType") != "resumable" {
		apiError(w, 400, "badRequest")
		return
	}
	var meta struct {
		Name    string   `json:"name"`
		Parents []string `json:"parents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil || len(meta.Parents) != 1 {
		apiError(w, 400, "parseError")
		return
	}
	size, err := strconv.ParseInt(r.Header.Get("X-Upload-Content-Length"), 10, 64)
	if err != nil {
		apiError(w, 400, "badContentLength")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.files[meta.Parents[0]] == nil {
		apiError(w, 404, "notFound")
		return
	}
	sid := randomHex(8)
	m.sessions[sid] = &mockUpload{name: meta.Name, parent: meta.Parents[0], size: size}
	w.Header().Set("Location", m.srv.URL+"/upload/session/"+sid+"?upload_id="+sid)
	w.WriteHeader(200)
}

var rangeRE = regexp.MustCompile(`^bytes (?:(\d+)-(\d+)|\*)/(\d+)$`)

func (m *mockGoogle) put(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	u := m.sessions[r.PathValue("sid")]
	m.mu.Unlock()
	if u == nil {
		apiError(w, 404, "notFound")
		return
	}
	cr := rangeRE.FindStringSubmatch(r.Header.Get("Content-Range"))
	if cr == nil {
		m.t.Errorf("mock: bad Content-Range %q", r.Header.Get("Content-Range"))
		apiError(w, 400, "badContentRange")
		return
	}
	if cr[1] == "" { // a status query: bytes */total
		m.mu.Lock()
		m.queries++
		m.mu.Unlock()
		m.answerUpload(w, u)
		return
	}
	m.mu.Lock()
	early := m.earlyPut
	m.mu.Unlock()
	if early != nil && early(w) {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	m.mu.Lock()
	m.puts++
	n := m.puts
	hook := m.putHook
	m.mu.Unlock()
	if hook != nil && hook(u, n, body, w) {
		return
	}
	start, _ := strconv.ParseInt(cr[1], 10, 64)
	end, _ := strconv.ParseInt(cr[2], 10, 64)
	if start != int64(len(u.data)) || end-start+1 != int64(len(body)) {
		m.t.Errorf("mock: chunk %d-%d with %d bytes, have %d", start, end, len(body), len(u.data))
		apiError(w, 400, "badRange")
		return
	}
	m.mu.Lock()
	u.data = append(u.data, body...)
	m.mu.Unlock()
	m.answerUpload(w, u)
}

// answerUpload answers 308 with the received range, or creates the file once
// every byte is in.
func (m *mockGoogle) answerUpload(w http.ResponseWriter, u *mockUpload) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if int64(len(u.data)) < u.size {
		if len(u.data) > 0 {
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(u.data)-1))
		}
		w.WriteHeader(308)
		return
	}
	f := &mockFile{id: "id" + randomHex(6), name: u.name, parent: u.parent, mimeType: "application/octet-stream", content: u.data}
	m.files[f.id] = f
	writeJSON(w, 200, map[string]string{"id": f.id, "webViewLink": "https://drive.google.com/file/d/" + f.id + "/view"})
}

// addFolder puts a folder into the mock directly.
func (m *mockGoogle) addFolder(name, parent string, trashed bool) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := "id" + randomHex(6)
	m.files[id] = &mockFile{id: id, name: name, mimeType: folderMime, parent: parent, trashed: trashed}
	return id
}
