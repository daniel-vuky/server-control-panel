package drive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FakeEmail is the fake Drive's account.
const FakeEmail = "dev@fake-drive.local"

// fake is a Drive over a local folder, for dev builds (wired by the dev-tagged
// NewClient) and tests. It is untagged so tests use it in both tag sets; a
// release binary never references it, so the linker drops it.
//
// Files live at <dir>/<folderID>/<name>. index.json keeps the folders, files,
// pending consent states and the SHA-256 of each valid refresh token (never a
// token itself), so it survives a dev restart.
type fake struct {
	dir, publicURL string

	mu  sync.Mutex
	idx *fakeIndex
}

type fakeIndex struct {
	Tokens  map[string]bool       `json:"tokens"`
	Pending map[string]string     `json:"pending"`
	Folders map[string]fakeFolder `json:"folders"`
	Files   map[string]fakeFile   `json:"files"`
}

type fakeFolder struct {
	Name   string `json:"name"`
	Parent string `json:"parent"`
}

type fakeFile struct {
	Folder string `json:"folder"`
	Name   string `json:"name"`
}

// NewFake returns the fake Drive over dir. Its consent page is the panel's own
// <publicURL>/api/drive/fake-consent (a dev route).
func NewFake(dir, publicURL string) Client {
	return &fake{dir: dir, publicURL: publicURL}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}

func tokenHash(refresh string) string {
	sum := sha256.Sum256([]byte(refresh))
	return hex.EncodeToString(sum[:])
}

func (f *fake) indexPath() string { return filepath.Join(f.dir, "index.json") }

// loadLocked reads the index once.
func (f *fake) loadLocked() error {
	if f.idx != nil {
		return nil
	}
	idx := &fakeIndex{}
	b, err := os.ReadFile(f.indexPath())
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("fake drive: %w", err)
	default:
		if err := json.Unmarshal(b, idx); err != nil {
			return fmt.Errorf("fake drive: index: %w", err)
		}
	}
	if idx.Tokens == nil {
		idx.Tokens = map[string]bool{}
	}
	if idx.Pending == nil {
		idx.Pending = map[string]string{}
	}
	if idx.Folders == nil {
		idx.Folders = map[string]fakeFolder{}
	}
	if idx.Files == nil {
		idx.Files = map[string]fakeFile{}
	}
	f.idx = idx
	return nil
}

func (f *fake) saveLocked() error {
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return fmt.Errorf("fake drive: %w", err)
	}
	b, err := json.MarshalIndent(f.idx, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("fake drive: %w", err)
	}
	if err := os.Rename(tmp, f.indexPath()); err != nil {
		return fmt.Errorf("fake drive: %w", err)
	}
	return nil
}

func (f *fake) AuthURL(state, redirect string) string {
	f.mu.Lock()
	if f.loadLocked() == nil {
		f.idx.Pending[state] = redirect
		_ = f.saveLocked() // a failure shows at Exchange
	}
	f.mu.Unlock()
	return f.publicURL + "/api/drive/fake-consent?" + url.Values{"state": {state}, "redirect": {redirect}}.Encode()
}

// Exchange accepts only fake-<state> for a state AuthURL issued with the same
// redirect, once.
func (f *fake) Exchange(_ context.Context, code, redirect string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.loadLocked(); err != nil {
		return "", err
	}
	state, ok := strings.CutPrefix(code, "fake-")
	if want, issued := f.idx.Pending[state]; !ok || !issued || want != redirect {
		return "", errCode(CodeTokenExpired, "the fake code was not issued")
	}
	delete(f.idx.Pending, state)
	refresh := "fake-refresh-" + randomHex(24)
	f.idx.Tokens[tokenHash(refresh)] = true
	if err := f.saveLocked(); err != nil {
		return "", err
	}
	return refresh, nil
}

func (f *fake) Session(refresh string) Session {
	return &fakeSession{f: f, hash: tokenHash(refresh)}
}

type fakeSession struct {
	f    *fake
	hash string
}

// lock takes the fake's lock and checks the token; the caller unlocks on nil.
func (s *fakeSession) lock() error {
	s.f.mu.Lock()
	if err := s.f.loadLocked(); err != nil {
		s.f.mu.Unlock()
		return err
	}
	if !s.f.idx.Tokens[s.hash] {
		s.f.mu.Unlock()
		return errCode(CodeTokenExpired, "the fake token was revoked")
	}
	return nil
}

func (s *fakeSession) unlock() { s.f.mu.Unlock() }

func (s *fakeSession) Account(context.Context) (string, error) {
	if err := s.lock(); err != nil {
		return "", err
	}
	s.unlock()
	return FakeEmail, nil
}

func (s *fakeSession) foldersLocked(parent string) []Folder {
	out := []Folder{}
	for id, fo := range s.f.idx.Folders {
		if fo.Parent == parent {
			out = append(out, Folder{ID: id, Name: fo.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *fakeSession) ListFolders(context.Context) ([]Folder, error) {
	if err := s.lock(); err != nil {
		return nil, err
	}
	defer s.unlock()
	return s.foldersLocked(""), nil
}

func (s *fakeSession) missingLocked(id string) error {
	if _, ok := s.f.idx.Folders[id]; !ok {
		return errCode(CodeFolderMissing, "no such fake folder")
	}
	return nil
}

func (s *fakeSession) createLocked(name, parent string) (Folder, error) {
	id := "f" + randomHex(8)
	if err := os.MkdirAll(filepath.Join(s.f.dir, id), 0o700); err != nil {
		return Folder{}, fmt.Errorf("fake drive: %w", err)
	}
	s.f.idx.Folders[id] = fakeFolder{Name: name, Parent: parent}
	if err := s.f.saveLocked(); err != nil {
		return Folder{}, err
	}
	return Folder{ID: id, Name: name}, nil
}

func (s *fakeSession) CreateFolder(_ context.Context, name, parentID string) (Folder, error) {
	if err := s.lock(); err != nil {
		return Folder{}, err
	}
	defer s.unlock()
	if parentID != "" {
		if err := s.missingLocked(parentID); err != nil {
			return Folder{}, err
		}
	}
	return s.createLocked(name, parentID)
}

func (s *fakeSession) FindOrCreateFolder(_ context.Context, name, parentID string) (Folder, error) {
	if err := s.lock(); err != nil {
		return Folder{}, err
	}
	defer s.unlock()
	if parentID != "" {
		if err := s.missingLocked(parentID); err != nil {
			return Folder{}, err
		}
	}
	for _, fo := range s.foldersLocked(parentID) {
		if fo.Name == name {
			return fo, nil
		}
	}
	return s.createLocked(name, parentID)
}

// plainName refuses a name that is not one path element.
func plainName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return fmt.Errorf("fake drive: %q is not a plain file name", name)
	}
	return nil
}

type progressWriter struct {
	w        io.Writer
	n        int64
	progress func(int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.n += int64(n)
	if n > 0 && p.progress != nil {
		p.progress(p.n)
	}
	return n, err
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Upload copies into a temporary file without the lock, then moves it in place.
func (s *fakeSession) Upload(ctx context.Context, folderID, name string, size int64, r io.Reader, progress func(int64)) (string, string, error) {
	if err := plainName(name); err != nil {
		return "", "", err
	}
	if err := s.lock(); err != nil {
		return "", "", err
	}
	err := s.missingLocked(folderID)
	s.unlock()
	if err != nil {
		return "", "", err
	}
	folderDir := filepath.Join(s.f.dir, folderID)
	tmp, err := os.CreateTemp(folderDir, ".upload-*")
	if err != nil {
		return "", "", fmt.Errorf("fake drive: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(&progressWriter{w: tmp, progress: progress}, io.LimitReader(ctxReader{ctx, r}, size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", fmt.Errorf("fake drive: upload: %w", err)
	}
	if n != size {
		return "", "", fmt.Errorf("fake drive: upload: got %d bytes, want %d", n, size)
	}

	if err := s.lock(); err != nil {
		return "", "", err
	}
	defer s.unlock()
	if err := s.missingLocked(folderID); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(folderDir, name)); err != nil {
		return "", "", fmt.Errorf("fake drive: %w", err)
	}
	for id, fi := range s.f.idx.Files {
		if fi.Folder == folderID && fi.Name == name {
			delete(s.f.idx.Files, id) // the same name replaces the file
		}
	}
	id := "x" + randomHex(8)
	s.f.idx.Files[id] = fakeFile{Folder: folderID, Name: name}
	if err := s.f.saveLocked(); err != nil {
		return "", "", err
	}
	return id, s.f.publicURL + "/settings#fake-drive-" + id, nil
}

func (s *fakeSession) Download(_ context.Context, fileID string) (io.ReadCloser, error) {
	if err := s.lock(); err != nil {
		return nil, err
	}
	defer s.unlock()
	fi, ok := s.f.idx.Files[fileID]
	if !ok {
		return nil, errCode(CodeFolderMissing, "no such fake file")
	}
	rc, err := os.Open(filepath.Join(s.f.dir, fi.Folder, fi.Name))
	if err != nil {
		return nil, errCode(CodeFolderMissing, "the fake file is gone from disk")
	}
	return rc, nil
}

func (s *fakeSession) Delete(_ context.Context, fileID string) error {
	if err := s.lock(); err != nil {
		return err
	}
	defer s.unlock()
	if fi, ok := s.f.idx.Files[fileID]; ok {
		delete(s.f.idx.Files, fileID)
		_ = os.Remove(filepath.Join(s.f.dir, fi.Folder, fi.Name))
		return s.f.saveLocked()
	}
	if _, ok := s.f.idx.Folders[fileID]; !ok {
		return errCode(CodeFolderMissing, "no such fake file or folder")
	}
	s.dropFolderLocked(fileID)
	return s.f.saveLocked()
}

// dropFolderLocked removes a folder, its files and its sub-folders.
func (s *fakeSession) dropFolderLocked(id string) {
	for cid, fo := range s.f.idx.Folders {
		if fo.Parent == id {
			s.dropFolderLocked(cid)
		}
	}
	for fid, fi := range s.f.idx.Files {
		if fi.Folder == id {
			delete(s.f.idx.Files, fid)
		}
	}
	delete(s.f.idx.Folders, id)
	_ = os.RemoveAll(filepath.Join(s.f.dir, id))
}

func (s *fakeSession) Revoke(context.Context) error {
	if err := s.lock(); err != nil {
		return err
	}
	defer s.unlock()
	delete(s.f.idx.Tokens, s.hash)
	return s.f.saveLocked()
}
