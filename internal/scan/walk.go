package scan

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// composeNames are the compose file names, in the order docker compose looks
// for them; the first one present wins.
var composeNames = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}

// overrideNames are the override file names, in the same order of preference.
var overrideNames = []string{"compose.override.yaml", "compose.override.yml", "docker-compose.override.yml", "docker-compose.override.yaml"}

// readDir is os.ReadDir; tests replace it to simulate an unreadable folder,
// since the dev image runs them as root.
var readDir = os.ReadDir

// Folder is one folder the walk listed. ComposeFile is the absolute path of its
// compose file, or "" for a plain folder; Override is the override file, if any.
type Folder struct {
	Path, ComposeFile, Override string
}

// List walks root to depth levels (the root itself is not listed) and returns
// every folder it examined, in walk order (entries sorted by name, each folder
// before its subfolders). It skips symlinks, hidden entries and node_modules,
// and does not descend into a folder that holds a compose file. A compose or
// override file counts only when it is a regular file. A folder that cannot
// be read is still listed, as a plain folder, with a "cannot read <path>"
// warning. List never follows a symlink and opens nothing outside root.
func List(root string, depth int) (folders []Folder, warnings []string) {
	entries, err := readDir(root)
	if err != nil {
		return nil, []string{"cannot read " + root}
	}
	var walk func(dir string, entries []fs.DirEntry, level int)
	walk = func(dir string, entries []fs.DirEntry, level int) {
		for _, e := range entries {
			if !walkable(e) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			children, err := readDir(path)
			if err != nil {
				folders = append(folders, Folder{Path: path})
				warnings = append(warnings, "cannot read "+path)
				continue
			}
			f := Folder{Path: path}
			if name := firstRegular(children, composeNames); name != "" {
				f.ComposeFile = filepath.Join(path, name)
				if o := firstRegular(children, overrideNames); o != "" {
					f.Override = filepath.Join(path, o)
				}
			}
			folders = append(folders, f)
			if f.ComposeFile == "" && level < depth {
				walk(path, children, level+1)
			}
		}
	}
	walk(root, entries, 1)
	return folders, warnings
}

// walkable says whether an entry is a folder the walk lists: a real directory
// (not a symlink to one), not hidden and not node_modules.
func walkable(e fs.DirEntry) bool {
	if e.Type()&fs.ModeSymlink != 0 || !e.IsDir() {
		return false
	}
	name := e.Name()
	return !strings.HasPrefix(name, ".") && name != "node_modules"
}

// firstRegular returns the first of names that is a regular file among entries.
func firstRegular(entries []fs.DirEntry, names []string) string {
	regular := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			regular[e.Name()] = true
		}
	}
	for _, n := range names {
		if regular[n] {
			return n
		}
	}
	return ""
}
