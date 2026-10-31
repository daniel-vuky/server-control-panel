package metrics

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseMountinfo(t *testing.T) {
	ms := ParseMountinfo(readFile(t, "1/mountinfo"))
	byPoint := map[string]Mount{}
	for _, m := range ms {
		byPoint[m.Point] = m
	}
	if len(ms) != 18 {
		t.Fatalf("got %d mounts, want 18", len(ms))
	}
	for point, want := range map[string]Mount{
		"/":                        {Device: "/dev/sda1", Point: "/", FSType: "ext4"},
		"/var/lib/docker":          {Device: "/dev/sdb1", Point: "/var/lib/docker", FSType: "ext4"},
		"/dev/shm":                 {Device: "tmpfs", Point: "/dev/shm", FSType: "tmpfs"},
		"/boot/efi":                {Device: "/dev/sda15", Point: "/boot/efi", FSType: "vfat"},
		"/mnt/backup disk":         {Device: "/dev/sdd1", Point: "/mnt/backup disk", FSType: "xfs"},
		"/proc/sys/fs/binfmt_misc": {Device: "systemd-1", Point: "/proc/sys/fs/binfmt_misc", FSType: "autofs"},
	} {
		if byPoint[point] != want {
			t.Errorf("%s = %+v, want %+v", point, byPoint[point], want)
		}
	}
	if m := byPoint["/var/lib/docker/overlay2/6c4d0e1f2a3b/merged"]; m.FSType != "overlay" || m.Device != "overlay" {
		t.Errorf("overlay row = %+v", m)
	}
	// Rows without the separator or with too few fields are skipped.
	if got := ParseMountinfo([]byte("garbage\n1 2 3 - ext4 /dev/x\n\n")); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestUnescapeMount(t *testing.T) {
	for in, want := range map[string]string{
		`/plain`:         "/plain",
		`/a\040b`:        "/a b",
		`/tab\011x`:      "/tab\tx",
		`/back\134slash`: `/back\slash`,
		`/short\04`:      `/short\04`,
		`/bad\0zz`:       `/bad\0zz`,
		`/end\`:          `/end\`,
	} {
		if got := unescapeMount(in); got != want {
			t.Errorf("unescapeMount(%q) = %q, want %q", in, got, want)
		}
	}
}

// statfsStub answers by path relative to root, as the panel sees /host/root.
func statfsStub(root string, rows map[string]Disk, fail map[string]error) func(string) (uint64, uint64, error) {
	return func(p string) (uint64, uint64, error) {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return 0, 0, err
		}
		point := "/" + rel
		if rel == "." {
			point = "/"
		}
		if e, ok := fail[point]; ok {
			return 0, 0, e
		}
		d, ok := rows[point]
		if !ok {
			return 0, 0, errors.New("unexpected statfs path " + p)
		}
		return d.Used, d.Total, nil
	}
}

func TestDisksFilterDedupeAndVarLibDocker(t *testing.T) {
	root := t.TempDir()
	h := testHost()
	h.Root = root
	h.Statfs = statfsStub(root, map[string]Disk{
		"/":                {Used: 10, Total: 100},
		"/var/lib/docker":  {Used: 400, Total: 500},
		"/mnt/backup disk": {Used: 7, Total: 70},
	}, map[string]error{
		"/home/private": &fs.PathError{Op: "statfs", Path: "/home/private", Err: os.ErrPermission},
	})
	got, fallback := h.Disks()
	if fallback {
		t.Fatal("Root exists, so not the fallback")
	}
	// The ext4 at /var/lib/docker is kept; overlay, tmpfs, vfat at /boot/efi and
	// squashfs under /snap are dropped; the binds of / dedupe to /; the 0700
	// /home/private (EACCES) is left out.
	want := []Disk{
		{Mount: "/", Used: 10, Total: 100},
		{Mount: "/mnt/backup disk", Used: 7, Total: 70},
		{Mount: "/var/lib/docker", Used: 400, Total: 500},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Disks = %+v, want %+v", got, want)
	}
}

func TestDisksDropSnapAndEFIByPath(t *testing.T) {
	root := t.TempDir()
	proc := t.TempDir()
	mi := "1 0 8:1 / / rw - ext4 /dev/sda1 rw\n" +
		"2 1 7:0 / /snap/core22/1612 ro - ext4 /dev/loop0 ro\n" +
		"3 1 8:15 / /boot/efi rw - ext4 /dev/sda15 rw\n" +
		"4 1 8:16 / /snapshots rw - ext4 /dev/sdb1 rw\n"
	if err := os.MkdirAll(filepath.Join(proc, "1"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "1", "mountinfo"), []byte(mi), 0o600); err != nil {
		t.Fatal(err)
	}
	h := Host{Proc: proc, Root: root, Statfs: statfsStub(root, map[string]Disk{
		"/": {Used: 1, Total: 2}, "/snapshots": {Used: 3, Total: 4},
	}, nil)}
	got, _ := h.Disks()
	want := []Disk{{Mount: "/", Used: 1, Total: 2}, {Mount: "/snapshots", Used: 3, Total: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Disks = %+v, want %+v", got, want)
	}
}

func TestDisksFallbackWithoutRoot(t *testing.T) {
	data := t.TempDir()
	var asked []string
	stub := func(p string) (uint64, uint64, error) {
		asked = append(asked, p)
		return 30, 90, nil
	}
	for name, root := range map[string]string{
		"empty":   "",
		"missing": filepath.Join(t.TempDir(), "host-root"),
	} {
		asked = nil
		h := Host{Proc: "testdata/proc", Root: root, DataDir: data, Statfs: stub}
		got, fallback := h.Disks()
		if !fallback {
			t.Errorf("%s: want fallback", name)
		}
		if want := []Disk{{Mount: "/ (panel data)", Used: 30, Total: 90}}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: Disks = %+v, want %+v", name, got, want)
		}
		if !reflect.DeepEqual(asked, []string{data}) {
			t.Errorf("%s: statfs asked %v, want the data dir", name, asked)
		}
	}
	// The fallback row is also what a Root without a readable mount table gives.
	h := Host{Proc: filepath.Join(t.TempDir(), "nope"), Root: t.TempDir(), DataDir: data, Statfs: stub}
	if got, fb := h.Disks(); !fb || len(got) != 1 {
		t.Fatalf("no mountinfo: %+v %v", got, fb)
	}
	// A data dir that cannot be read leaves the list empty.
	h = Host{Root: "", DataDir: data, Statfs: func(string) (uint64, uint64, error) { return 0, 0, errors.New("boom") }}
	if got, fb := h.Disks(); !fb || len(got) != 0 {
		t.Fatalf("statfs error: %+v %v", got, fb)
	}
}

func TestStatfsDefault(t *testing.T) {
	used, total, err := statfs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 || used > total {
		t.Fatalf("used %d total %d", used, total)
	}
	if _, _, err := statfs(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing path should be an error")
	}
	// A Host with a nil Statfs uses it.
	h := Host{Root: "", DataDir: t.TempDir()}
	if got, fb := h.Disks(); !fb || len(got) != 1 || got[0].Total == 0 {
		t.Fatalf("default statfs fallback: %+v %v", got, fb)
	}
}
