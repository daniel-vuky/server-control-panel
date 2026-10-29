package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// mkfile writes a file under root, creating its folders.
func mkfile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, rel), 0o750); err != nil {
		t.Fatal(err)
	}
}

// tempRoot returns a temp dir with symlinks resolved, as the guard would store it.
func tempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// rel turns listed folders into "path|composeFile|override" lines relative to root.
func rel(t *testing.T, root string, folders []Folder) []string {
	t.Helper()
	trim := func(p string) string {
		if p == "" {
			return ""
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	out := make([]string, 0, len(folders))
	for _, f := range folders {
		out = append(out, trim(f.Path)+"|"+trim(f.ComposeFile)+"|"+trim(f.Override))
	}
	return out
}

func depthTree(t *testing.T) string {
	root := tempRoot(t)
	mkfile(t, root, "a/compose.yaml", "services: {}\n")
	mkfile(t, root, "a/sub/compose.yaml", "services: {}\n")
	mkfile(t, root, "b/c/docker-compose.yml", "services: {}\n")
	mkfile(t, root, "d/e/f/compose.yml", "services: {}\n")
	return root
}

func TestListDepthBoundaries(t *testing.T) {
	root := depthTree(t)
	cases := []struct {
		depth int
		want  []string
	}{
		{1, []string{"a|a/compose.yaml|", "b||", "d||"}},
		{2, []string{"a|a/compose.yaml|", "b||", "b/c|b/c/docker-compose.yml|", "d||", "d/e||"}},
		{3, []string{"a|a/compose.yaml|", "b||", "b/c|b/c/docker-compose.yml|", "d||", "d/e||", "d/e/f|d/e/f/compose.yml|"}},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint("depth ", c.depth), func(t *testing.T) {
			folders, warnings := List(root, c.depth)
			if len(warnings) != 0 {
				t.Fatalf("warnings %v", warnings)
			}
			if got := rel(t, root, folders); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("List depth %d\n got %q\nwant %q", c.depth, got, c.want)
			}
		})
	}
}

func TestListNoDescentIntoProject(t *testing.T) {
	root := depthTree(t)
	folders, _ := List(root, 3)
	for _, f := range folders {
		if strings.Contains(f.Path, filepath.Join("a", "sub")) {
			t.Fatalf("a/sub listed under the project a: %+v", f)
		}
	}
}

func TestListComposeFileOrder(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "p/docker-compose.yaml", "")
	mkfile(t, root, "p/docker-compose.yml", "")
	mkfile(t, root, "p/compose.yml", "")
	folders, _ := List(root, 1)
	if got := rel(t, root, folders); !reflect.DeepEqual(got, []string{"p|p/compose.yml|"}) {
		t.Fatalf("got %q, want compose.yml to win", got)
	}
}

func TestListSkipsHiddenAndNodeModules(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, ".git/compose.yaml", "")
	mkfile(t, root, "node_modules/compose.yaml", "")
	mkfile(t, root, "app/node_modules/x/compose.yaml", "")
	mkfile(t, root, "app/.cache/compose.yaml", "")
	folders, _ := List(root, 3)
	if got := rel(t, root, folders); !reflect.DeepEqual(got, []string{"app||"}) {
		t.Fatalf("got %q", got)
	}
}

func TestListSkipsSymlinkedFolder(t *testing.T) {
	root := tempRoot(t)
	outside := tempRoot(t)
	mkfile(t, outside, "compose.yaml", "")
	mkfile(t, root, "real/compose.yaml", "")
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	folders, _ := List(root, 3)
	if got := rel(t, root, folders); !reflect.DeepEqual(got, []string{"real|real/compose.yaml|"}) {
		t.Fatalf("got %q, want the symlinked folders left out", got)
	}
}

func TestListSymlinkedComposeFileIsPlainFolder(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "real/compose.yaml", "")
	mkdir(t, root, "s")
	if err := os.Symlink(filepath.Join(root, "real", "compose.yaml"), filepath.Join(root, "s", "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	folders, _ := List(root, 1)
	if got := rel(t, root, folders); !reflect.DeepEqual(got, []string{"real|real/compose.yaml|", "s||"}) {
		t.Fatalf("got %q", got)
	}
}

func TestListOverride(t *testing.T) {
	root := tempRoot(t)
	mkfile(t, root, "o/compose.yaml", "")
	mkfile(t, root, "o/docker-compose.override.yml", "")
	mkfile(t, root, "o/compose.override.yml", "")
	mkfile(t, root, "l/compose.yaml", "")
	if err := os.Symlink(filepath.Join(root, "o", "compose.override.yml"), filepath.Join(root, "l", "compose.override.yaml")); err != nil {
		t.Fatal(err)
	}
	folders, _ := List(root, 1)
	want := []string{"l|l/compose.yaml|", "o|o/compose.yaml|o/compose.override.yml"}
	if got := rel(t, root, folders); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestListOddFolderName(t *testing.T) {
	root := tempRoot(t)
	name := "my app; $(id) 'q' \"dq\" ünï"
	mkfile(t, root, filepath.Join(name, "compose.yaml"), "")
	folders, _ := List(root, 1)
	if len(folders) != 1 || folders[0].Path != filepath.Join(root, name) || folders[0].ComposeFile != filepath.Join(root, name, "compose.yaml") {
		t.Fatalf("got %+v", folders)
	}
}

func TestListUnreadableFolderCountedWithWarning(t *testing.T) {
	root := tempRoot(t)
	mkdir(t, root, "locked/inner")
	mkfile(t, root, "ok/compose.yaml", "")
	locked := filepath.Join(root, "locked")
	orig := readDir
	readDir = func(name string) ([]fs.DirEntry, error) {
		if name == locked {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
		}
		return orig(name)
	}
	t.Cleanup(func() { readDir = orig })

	folders, warnings := List(root, 3)
	if got := rel(t, root, folders); !reflect.DeepEqual(got, []string{"locked||", "ok|ok/compose.yaml|"}) {
		t.Fatalf("got %q", got)
	}
	if want := []string{"cannot read " + locked}; !reflect.DeepEqual(warnings, want) {
		t.Fatalf("warnings %q, want %q", warnings, want)
	}
}

// treeHash hashes every path, mode and file content under root, without
// following symlinks (their targets are hashed as text).
func treeHash(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := p + "|" + info.Mode().String()
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			line += "|->" + target
		case info.Mode().IsRegular():
			b, err := os.ReadFile(p) //nolint:gosec // G304: test helper reading its own temp tree
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			line += "|" + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func TestTreeUnchanged(t *testing.T) {
	root := depthTree(t)
	mkfile(t, root, "a/.env", "POSTGRES_PASSWORD=devpassword\n")
	mkfile(t, root, "o/compose.yaml", "")
	mkfile(t, root, "o/compose.override.yaml", "")
	mkfile(t, root, "my app; $(id)/compose.yaml", "")
	if err := os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	before := treeHash(t, root)

	List(root, 3)
	fc := newFakeCmd()
	fc.configs[filepath.Join(root, "a")] = fakeReply{stdout: configJSON("a", map[string]string{"db": "postgres:16"})}
	m, _ := newTestManager(t, fc)
	runScan(t, m, root, 3)

	if after := treeHash(t, root); after != before {
		t.Fatal("the scanned tree changed")
	}
}
