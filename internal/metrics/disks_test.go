package metrics

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// writeMountinfo writes a /proc/1/mountinfo under a new temp /proc and
// returns that /proc.
func writeMountinfo(t *testing.T, mi string) string {
	t.Helper()
	proc := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proc, "1"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "1", "mountinfo"), []byte(mi), 0o600); err != nil {
		t.Fatal(err)
	}
	return proc
}

func TestParseMountinfo(t *testing.T) {
	ms := ParseMountinfo(readFile(t, "1/mountinfo"))
	byPoint := map[string]Mount{}
	for _, m := range ms {
		byPoint[m.Point] = m
	}
	if len(ms) != 19 {
		t.Fatalf("got %d mounts, want 19", len(ms))
	}
	for point, want := range map[string]Mount{
		"/":                        {Device: "/dev/sda1", Point: "/", FSType: "ext4", DevID: "8:1"},
		"/var/lib/docker":          {Device: "/dev/sdb1", Point: "/var/lib/docker", FSType: "ext4", DevID: "8:17"},
		"/dev/shm":                 {Device: "tmpfs", Point: "/dev/shm", FSType: "tmpfs", DevID: "0:27"},
		"/boot/efi":                {Device: "/dev/sda15", Point: "/boot/efi", FSType: "vfat", DevID: "8:15"},
		"/mnt/backup disk":         {Device: "/dev/sdd1", Point: "/mnt/backup disk", FSType: "xfs", DevID: "8:49"},
		"/proc/sys/fs/binfmt_misc": {Device: "systemd-1", Point: "/proc/sys/fs/binfmt_misc", FSType: "autofs", DevID: "0:26"},
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

// statfsStub answers by path relative to root, as the panel sees /host/root,
// and records every path it is asked, so a test can pin which mounts Disks
// looked at. A path it has no row for fails the test. devs is the major:minor
// each path reads as (Host.Dev); a path above root is the container's own
// filesystem, outsideDev.
type statfsStub struct {
	t     *testing.T
	root  string
	rows  map[string]Disk
	fail  map[string]error
	devs  map[string]string
	asked []string
}

const outsideDev = "0:99"

func (s *statfsStub) point(p string) (string, error) {
	rel, err := filepath.Rel(s.root, p)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "/", nil
	}
	return "/" + rel, nil
}

func (s *statfsStub) dev(p string) (string, error) {
	point, err := s.point(p)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(point, "/..") {
		return outsideDev, nil
	}
	d, ok := s.devs[point]
	if !ok {
		return "", errors.New("no dev for " + p)
	}
	return d, nil
}

func (s *statfsStub) statfs(p string) (uint64, uint64, error) {
	s.asked = append(s.asked, p)
	point, err := s.point(p)
	if err != nil {
		return 0, 0, err
	}
	if e, ok := s.fail[point]; ok {
		return 0, 0, e
	}
	d, ok := s.rows[point]
	if !ok {
		s.t.Errorf("unexpected statfs path %s", p)
		return 0, 0, errors.New("unexpected statfs path " + p)
	}
	return d.Used, d.Total, nil
}

// wantAsked pins the exact set of paths statfs was asked, in any order.
func (s *statfsStub) wantAsked(points ...string) {
	s.t.Helper()
	var want []string
	for _, p := range points {
		want = append(want, filepath.Join(s.root, p))
	}
	got := append([]string(nil), s.asked...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		s.t.Errorf("statfs asked %v, want %v", got, want)
	}
}

func TestDisksFilterDedupeAndVarLibDocker(t *testing.T) {
	root := t.TempDir()
	h := testHost()
	h.Root = root
	stub := &statfsStub{t: t, root: root, rows: map[string]Disk{
		"/":                {Used: 10, Total: 100},
		"/var/lib/docker":  {Used: 400, Total: 500},
		"/mnt/backup disk": {Used: 7, Total: 70},
	}, fail: map[string]error{
		"/home/private": &fs.PathError{Op: "statfs", Path: "/home/private", Err: os.ErrPermission},
	}, devs: map[string]string{"/": "8:1", "/var/lib/docker": "8:17", "/mnt/backup disk": "8:49"}}
	h.Statfs, h.Dev = stub.statfs, stub.dev
	got, fallback := h.Disks()
	if fallback {
		t.Fatal("Root exists, so not the fallback")
	}
	// Exactly one statfs per surviving device: sda1 (reached as /, /mnt/shared,
	// /mnt/rootbind and, under another source name, /mnt/by-uuid) is read once
	// at /; overlay, tmpfs, vfat, squashfs and the rest are never read.
	stub.wantAsked("/", "/var/lib/docker", "/mnt/backup disk", "/home/private")
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
	stub := &statfsStub{t: t, root: root, rows: map[string]Disk{
		"/": {Used: 1, Total: 2}, "/snapshots": {Used: 3, Total: 4},
	}, devs: map[string]string{"/": "8:1", "/snapshots": "8:16"}}
	h := Host{Proc: proc, Root: root, Statfs: stub.statfs, Dev: stub.dev}
	got, _ := h.Disks()
	stub.wantAsked("/", "/snapshots")
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

func TestDisksDedupeByMajorMinor(t *testing.T) {
	root := t.TempDir()
	proc := t.TempDir()
	// One filesystem (8:1) under two source names (the shorter mount point wins), and two filesystems that
	// share a source name but not a major:minor.
	mi := "1 0 8:1 / /data rw - ext4 /dev/sda1 rw\n" +
		"2 1 8:1 / /srv rw - ext4 /dev/disk/by-uuid/aaaa rw\n" +
		"3 1 8:16 / /a rw - ext4 none rw\n" +
		"4 1 8:32 / /b rw - ext4 none rw\n"
	if err := os.MkdirAll(filepath.Join(proc, "1"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "1", "mountinfo"), []byte(mi), 0o600); err != nil {
		t.Fatal(err)
	}
	stub := &statfsStub{t: t, root: root, rows: map[string]Disk{
		"/srv": {Used: 1, Total: 2}, "/a": {Used: 3, Total: 4}, "/b": {Used: 5, Total: 6},
	}, devs: map[string]string{"/srv": "8:1", "/a": "8:16", "/b": "8:32"}}
	got, _ := Host{Proc: proc, Root: root, Statfs: stub.statfs, Dev: stub.dev}.Disks()
	want := []Disk{{Mount: "/a", Used: 3, Total: 4}, {Mount: "/b", Used: 5, Total: 6}, {Mount: "/srv", Used: 1, Total: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Disks = %+v, want %+v", got, want)
	}
	stub.wantAsked("/srv", "/a", "/b")
}

// A disk mounted after the panel started is in the host's mount table but not
// under the /host/root bind (frozen at container start), so its path there is
// a plain directory of the parent filesystem: statfs would answer the
// parent's numbers. Its device differs from the mount table's, and it reads as
// the same device as its parent directory, so it is left out until a restart.
func TestDisksDropAMountNotUnderTheRootBind(t *testing.T) {
	root := t.TempDir()
	proc := writeMountinfo(t, "1 0 8:1 / / rw - ext4 /dev/sda1 rw\n"+
		"2 1 8:16 / /data rw - ext4 /dev/sdb1 rw\n"+
		"3 1 8:32 / /srv/late rw - xfs /dev/sdc1 rw\n")
	stub := &statfsStub{t: t, root: root, rows: map[string]Disk{
		"/": {Used: 10, Total: 100}, "/data": {Used: 10, Total: 100}, "/srv/late": {Used: 10, Total: 100},
	}, devs: map[string]string{"/": "8:1", "/data": "8:1", "/srv": "8:1", "/srv/late": "8:1"}}
	got, fallback := Host{Proc: proc, Root: root, Statfs: stub.statfs, Dev: stub.dev}.Disks()
	if fallback {
		t.Fatal("not the fallback")
	}
	if want := []Disk{{Mount: "/", Used: 10, Total: 100}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Disks = %+v, want only /", got)
	}
}

// btrfs (and bcachefs) give every subvolume its own anonymous device, so stat
// does not match the mount table's major:minor even for a mount that is
// there. A path whose device differs from its parent directory's is a mount
// seen through the bind, and is kept; a late mount reads as its parent's
// device and is still left out. A device that cannot be read is left out.
func TestDisksKeepASubvolumeMountWithAnotherDevice(t *testing.T) {
	root := t.TempDir()
	proc := writeMountinfo(t, "1 0 0:31 /@ / rw - btrfs /dev/sda2 rw\n"+
		"2 1 0:40 / /srv rw - btrfs /dev/sdb1 rw\n"+
		"3 1 0:50 / /late rw - btrfs /dev/sdc1 rw\n"+
		"4 1 8:48 / /nodev rw - ext4 /dev/sdd1 rw\n")
	stub := &statfsStub{t: t, root: root, rows: map[string]Disk{
		"/": {Used: 1, Total: 2}, "/srv": {Used: 3, Total: 4}, "/late": {Used: 1, Total: 2}, "/nodev": {Used: 5, Total: 6},
	}, devs: map[string]string{"/": "0:33", "/srv": "0:41", "/late": "0:33"}}
	got, _ := Host{Proc: proc, Root: root, Statfs: stub.statfs, Dev: stub.dev}.Disks()
	want := []Disk{{Mount: "/", Used: 1, Total: 2}, {Mount: "/srv", Used: 3, Total: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Disks = %+v, want %+v", got, want)
	}
}

func TestDevName(t *testing.T) {
	for dev, want := range map[uint64]string{
		0x801:    "8:1",
		0x10301:  "259:1",
		0x10082c: "8:300",
		0x2b:     "0:43",
	} {
		if got := devName(dev); got != want {
			t.Errorf("devName(%#x) = %s, want %s", dev, got, want)
		}
	}
	got, err := lstatDev(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if major, minor, ok := strings.Cut(got, ":"); !ok || major == "" || minor == "" {
		t.Fatalf("lstatDev = %q", got)
	}
	if _, err := lstatDev(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing path should be an error")
	}
}
