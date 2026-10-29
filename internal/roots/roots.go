// Package roots confines the folders the panel will scan to the directories
// the owner listed in WORKSPACE_ROOTS. Check is the one gate: a path that does
// not resolve to a readable folder inside a root is refused with a PathError the
// UI shows under the path field.
package roots

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Error codes of a PathError.
const (
	CodeNotAbsolute  = "not_absolute"
	CodeNotFound     = "not_found"
	CodeUnreadable   = "unreadable"
	CodeOutsideRoots = "outside_roots"
	CodeNotAFolder   = "not_a_folder"
)

// PathError is a refused path: a stable code and the message to show the owner.
type PathError struct {
	Code    string
	Message string
}

func (e *PathError) Error() string { return e.Message }

func notAbsolute() *PathError {
	return &PathError{CodeNotAbsolute, "Enter an absolute path, starting with /."}
}

func notFound() *PathError {
	return &PathError{CodeNotFound, "This folder does not exist."}
}

func unreadable() *PathError {
	return &PathError{CodeUnreadable, "The panel cannot read this folder (permission denied)."}
}

func notAFolder() *PathError {
	return &PathError{CodeNotAFolder, "This path is a file, not a folder."}
}

// Guard holds the resolved roots and checks paths against them.
type Guard struct {
	resolved []string // roots after EvalSymlinks, what containment compares against
	display  []string // roots as configured, what the outside_roots message shows

	// Filesystem hooks, replaced in tests.
	evalSymlinks func(string) (string, error)
	stat         func(string) (fs.FileInfo, error)
	readDir      func(string) ([]os.DirEntry, error)
}

// New resolves each root with EvalSymlinks once. A root that cannot be resolved
// (it does not exist, or is unreadable) is dropped with a warning. With no
// roots left, every path is refused.
func New(roots []string, log *slog.Logger) *Guard {
	if log == nil {
		log = slog.Default()
	}
	g := &Guard{
		evalSymlinks: filepath.EvalSymlinks,
		stat:         os.Stat,
		readDir:      os.ReadDir,
	}
	for _, r := range roots {
		resolved, err := g.evalSymlinks(r)
		if err != nil {
			log.Warn("workspace root dropped", "root", r, "error", err)
			continue
		}
		g.resolved = append(g.resolved, resolved)
		g.display = append(g.display, r)
	}
	return g
}

// Check validates path and returns the resolved folder to store and scan. It
// refuses, in this order: a path that is empty or not absolute; one that does
// not exist or cannot be read; one outside every root, which includes a ".."
// that climbs out and a symlink that leaves; one that is not a folder; and a
// folder that cannot be listed. The returned path has no symlinks, "..", "." or
// doubled or trailing slashes. On error it is empty and err is a *PathError.
func (g *Guard) Check(path string) (resolved string, err error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", notAbsolute()
	}
	resolved, err = g.evalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", classify(err)
	}
	if !g.Contains(resolved) {
		return "", &PathError{
			Code: CodeOutsideRoots,
			Message: "This folder is outside the allowed folders (" + strings.Join(g.display, ", ") +
				"). Add it to WORKSPACE_ROOTS and re-run install.sh.",
		}
	}
	info, err := g.stat(resolved)
	if err != nil {
		return "", classify(err)
	}
	if !info.IsDir() {
		return "", notAFolder()
	}
	if _, err := g.readDir(resolved); err != nil {
		return "", classify(err)
	}
	return resolved, nil
}

// Contains reports whether an already resolved path is a root or lies under one.
func (g *Guard) Contains(resolved string) bool {
	for _, root := range g.resolved {
		if resolved == root || strings.HasPrefix(resolved, strings.TrimSuffix(root, "/")+"/") {
			return true
		}
	}
	return false
}

// Display returns the kept roots as the owner configured them.
func (g *Guard) Display() []string {
	return append([]string{}, g.display...)
}

// classify maps a filesystem error to a PathError. A path that does not exist,
// or runs through a file as if it were a folder, is not_found; anything else
// the panel could not read, such as permission denied, is unreadable.
func classify(err error) *PathError {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return notFound()
	}
	return unreadable()
}
