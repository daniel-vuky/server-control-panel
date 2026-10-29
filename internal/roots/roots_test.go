package roots

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tree is the temp layout the tests share:
//
//	base/root/ws/        a folder inside the root
//	base/root/file       a file inside the root
//	base/root/link       a symlink to ../other
//	base/other/x         a folder outside the root
//	base/root2/x         inside a sibling whose name starts with "root"
type tree struct {
	base, root, ws, file, link, other, root2 string
}

func newTree(t *testing.T) tree {
	t.Helper()
	// The test tmp dir may itself sit behind a symlink (macOS /var), so work
	// from its resolved spelling.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tr := tree{
		base:  base,
		root:  filepath.Join(base, "root"),
		ws:    filepath.Join(base, "root", "ws"),
		file:  filepath.Join(base, "root", "file"),
		link:  filepath.Join(base, "root", "link"),
		other: filepath.Join(base, "other"),
		root2: filepath.Join(base, "root2"),
	}
	for _, d := range []string{tr.ws, filepath.Join(tr.other, "x"), filepath.Join(tr.root2, "x")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(tr.file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../other", tr.link); err != nil {
		t.Fatal(err)
	}
	return tr
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)) }

// wantCode fails the test unless err is a *PathError with the given code.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	_ = pathErr(t, err, code)
}

// pathErr is wantCode that also returns the error for further checks.
func pathErr(t *testing.T, err error, code string) *PathError {
	t.Helper()
	var pe *PathError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v (%T), want a *PathError with code %q", err, err, code)
	}
	if pe.Code != code {
		t.Fatalf("code = %q (%s), want %q", pe.Code, pe.Message, code)
	}
	return pe
}

func TestCheckErrors(t *testing.T) {
	tr := newTree(t)
	g := New([]string{tr.root}, quiet())
	tests := []struct {
		name    string
		path    string
		code    string
		message string
	}{
		{"empty", "", CodeNotAbsolute, "Enter an absolute path, starting with /."},
		{"relative", "root/ws", CodeNotAbsolute, "Enter an absolute path, starting with /."},
		{"not found", filepath.Join(tr.root, "missing"), CodeNotFound, "This folder does not exist."},
		{"below a file", filepath.Join(tr.file, "x"), CodeNotFound, "This folder does not exist."},
		{"outside", tr.other, CodeOutsideRoots,
			"This folder is outside the allowed folders (" + tr.root + "). Add it to WORKSPACE_ROOTS and re-run install.sh."},
		{"a file", tr.file, CodeNotAFolder, "This path is a file, not a folder."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := g.Check(tt.path)
			pe := pathErr(t, err, tt.code)
			if pe.Message != tt.message {
				t.Errorf("message = %q, want %q", pe.Message, tt.message)
			}
			if pe.Error() != tt.message {
				t.Errorf("Error() = %q, want the message", pe.Error())
			}
			if resolved != "" {
				t.Errorf("resolved = %q, want empty on error", resolved)
			}
		})
	}
}

func TestOutsideMessageListsEveryRoot(t *testing.T) {
	tr := newTree(t)
	g := New([]string{tr.root, tr.root2}, quiet())
	_, err := g.Check(tr.other)
	pe := pathErr(t, err, CodeOutsideRoots)
	want := "(" + tr.root + ", " + tr.root2 + ")"
	if !strings.Contains(pe.Message, want) {
		t.Errorf("message = %q, want it to contain %q", pe.Message, want)
	}
}

func TestCheckConfinesPaths(t *testing.T) {
	tr := newTree(t)
	g := New([]string{tr.root}, quiet())
	tests := []struct {
		name string
		path string
	}{
		{"dot-dot climbs out", filepath.Join(tr.ws, "..", "..", "other")},
		{"symlink leaves the root", tr.link},
		{"below a symlink that leaves the root", filepath.Join(tr.link, "x")},
		{"sibling sharing the root's name prefix", filepath.Join(tr.root2, "x")},
		{"the parent of the root", tr.base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := g.Check(tt.path)
			wantCode(t, err, CodeOutsideRoots)
		})
	}
}

func TestCheckAcceptsEverySpellingOfAnAllowedFolder(t *testing.T) {
	tr := newTree(t)
	g := New([]string{tr.root}, quiet())
	tests := []struct {
		name string
		path string
		want string
	}{
		{"the root itself", tr.root, tr.root},
		{"the root with a trailing slash", tr.root + "/", tr.root},
		{"plain", tr.ws, tr.ws},
		{"trailing slash", tr.ws + "/", tr.ws},
		{"double slash", tr.root + "//ws", tr.ws},
		{"dot segment", tr.root + "/./ws", tr.ws},
		{"dot-dot that stays inside", tr.root + "/ws/../ws", tr.ws},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.Check(tt.path)
			if err != nil {
				t.Fatalf("Check(%q) error = %v", tt.path, err)
			}
			if got != tt.want {
				t.Errorf("Check(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestCheckResolvesSymlinkInsideRoot(t *testing.T) {
	tr := newTree(t)
	inside := filepath.Join(tr.root, "alias")
	if err := os.Symlink("ws", inside); err != nil {
		t.Fatal(err)
	}
	g := New([]string{tr.root}, quiet())
	got, err := g.Check(inside)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got != tr.ws {
		t.Errorf("Check() = %q, want the resolved %q", got, tr.ws)
	}
}

func TestRootGivenThroughSymlink(t *testing.T) {
	tr := newTree(t)
	linkRoot := filepath.Join(tr.base, "linkroot")
	if err := os.Symlink(tr.root, linkRoot); err != nil {
		t.Fatal(err)
	}
	g := New([]string{linkRoot}, quiet())

	for _, path := range []string{filepath.Join(linkRoot, "ws"), tr.ws} {
		got, err := g.Check(path)
		if err != nil {
			t.Fatalf("Check(%q) error = %v", path, err)
		}
		if got != tr.ws {
			t.Errorf("Check(%q) = %q, want %q", path, got, tr.ws)
		}
	}
	if got := g.Display(); !slices.Equal(got, []string{linkRoot}) {
		t.Errorf("Display() = %v, want the configured spelling %q", got, linkRoot)
	}
}

func TestContains(t *testing.T) {
	tr := newTree(t)
	g := New([]string{tr.root}, quiet())
	tests := []struct {
		path string
		want bool
	}{
		{tr.root, true},
		{tr.ws, true},
		{tr.root2, false},
		{tr.root + "x", false},
		{tr.base, false},
	}
	for _, tt := range tests {
		if got := g.Contains(tt.path); got != tt.want {
			t.Errorf("Contains(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestFilesystemRootContainsEverything(t *testing.T) {
	tr := newTree(t)
	g := New([]string{"/"}, quiet())
	got, err := g.Check(tr.ws)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got != tr.ws {
		t.Errorf("Check() = %q, want %q", got, tr.ws)
	}
}

func TestPermissionClassified(t *testing.T) {
	tr := newTree(t)
	g := New([]string{tr.root}, quiet())
	g.stat = func(string) (fs.FileInfo, error) { return nil, fs.ErrPermission }
	_, err := g.Check(tr.ws)
	pe := pathErr(t, err, CodeUnreadable)
	if want := "The panel cannot read this folder (permission denied)."; pe.Message != want {
		t.Errorf("message = %q, want %q", pe.Message, want)
	}
}

func TestPermissionOnEvalSymlinksClassified(t *testing.T) {
	tr := newTree(t)
	g := New([]string{tr.root}, quiet())
	g.evalSymlinks = func(string) (string, error) { return "", fs.ErrPermission }
	_, err := g.Check(tr.ws)
	wantCode(t, err, CodeUnreadable)
}

func TestReadDirFailureIsUnreadable(t *testing.T) {
	tr := newTree(t)
	g := New([]string{tr.root}, quiet())
	g.readDir = func(string) ([]os.DirEntry, error) { return nil, fs.ErrPermission }
	_, err := g.Check(tr.ws)
	wantCode(t, err, CodeUnreadable)
}

func TestRealPermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	tr := newTree(t)
	locked := filepath.Join(tr.root, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) }) //nolint:gosec // a folder needs its execute bit back so the temp dir can be removed
	g := New([]string{tr.root}, quiet())
	_, err := g.Check(locked)
	wantCode(t, err, CodeUnreadable)
}

func TestMissingRootDropped(t *testing.T) {
	tr := newTree(t)
	missing := filepath.Join(tr.base, "missing")
	var logs bytes.Buffer
	g := New([]string{missing, tr.root}, slog.New(slog.NewTextHandler(&logs, nil)))

	if got := g.Display(); !slices.Equal(got, []string{tr.root}) {
		t.Errorf("Display() = %v, want only the kept root %q", got, tr.root)
	}
	if _, err := g.Check(tr.ws); err != nil {
		t.Errorf("Check() under the kept root error = %v", err)
	}
	out := logs.String()
	for _, want := range []string{"level=WARN", "workspace root dropped", "root=" + missing} {
		if !strings.Contains(out, want) {
			t.Errorf("log = %q, want it to contain %q", out, want)
		}
	}
}

func TestEmptyRootsRefuseEverything(t *testing.T) {
	tr := newTree(t)
	for name, g := range map[string]*Guard{
		"no roots":          New(nil, quiet()),
		"only a bad root":   New([]string{filepath.Join(tr.base, "missing")}, quiet()),
		"empty but non-nil": New([]string{}, quiet()),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := g.Check(tr.ws)
			pe := pathErr(t, err, CodeOutsideRoots)
			if !strings.Contains(pe.Message, "()") {
				t.Errorf("message = %q, want it to contain ()", pe.Message)
			}
			if g.Contains(tr.ws) {
				t.Error("Contains() = true with no roots")
			}
			if got := g.Display(); len(got) != 0 {
				t.Errorf("Display() = %v, want none", got)
			}
		})
	}
}
