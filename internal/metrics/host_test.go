package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testHost reads the captured /proc and /sys under testdata.
func testHost() Host {
	return Host{Proc: "testdata/proc", Sys: "testdata/sys"}
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "proc", name)) //nolint:gosec // G304: test fixture names
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseStat(t *testing.T) {
	got, err := ParseStat(readFile(t, "stat"))
	if err != nil {
		t.Fatal(err)
	}
	// Total is the first 8 columns (guest 1500 and guest_nice 120 are left
	// out); busy is total minus idle (98211400) and iowait (53210).
	want := CPUTimes{Busy: 1685880, Total: 99950490, Cores: 4}
	if got != want {
		t.Fatalf("ParseStat = %+v, want %+v", got, want)
	}
}

func TestParseStatTolerance(t *testing.T) {
	// An old kernel has only 7 columns; a missing one counts as 0.
	got, err := ParseStat([]byte("cpu  100 0 50 800 20 5 5\ncpu0 100 0 50 800 20 5 5\nbtime 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (CPUTimes{Busy: 160, Total: 980, Cores: 1}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for name, in := range map[string]string{
		"empty":     "",
		"no cpu":    "intr 1 2 3\nctxt 4\n",
		"short":     "cpu 1 2 3\n",
		"not a num": "cpu 1 2 x 4 5\n",
	} {
		if _, err := ParseStat([]byte(in)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestCPUPercent(t *testing.T) {
	prev := CPUTimes{Busy: 100, Total: 1000, Cores: 4}
	if p, ok := CPUPercent(prev, CPUTimes{Busy: 150, Total: 1200, Cores: 4}); !ok || p != 25 {
		t.Fatalf("got %v %v, want 25 true", p, ok)
	}
	if _, ok := CPUPercent(prev, prev); ok {
		t.Fatal("the same read twice has no percent")
	}
	if _, ok := CPUPercent(prev, CPUTimes{Busy: 90, Total: 900}); ok {
		t.Fatal("a counter that went back has no percent")
	}
	// iowait can go backwards on some kernels, which makes busy shrink.
	if p, ok := CPUPercent(prev, CPUTimes{Busy: 90, Total: 1100}); !ok || p != 0 {
		t.Fatalf("shrinking busy: got %v %v, want 0 true", p, ok)
	}
	if p, ok := CPUPercent(prev, CPUTimes{Busy: 5000, Total: 1100}); !ok || p != 100 {
		t.Fatalf("busy past total: got %v %v, want 100 true", p, ok)
	}
}

func TestParseMeminfo(t *testing.T) {
	used, total, err := ParseMeminfo(readFile(t, "meminfo"))
	if err != nil {
		t.Fatal(err)
	}
	if total != 8024196*1024 || used != (8024196-5173184)*1024 {
		t.Fatalf("used %d total %d", used, total)
	}
	if _, _, err := ParseMeminfo([]byte("MemTotal: 100 kB\nMemFree: 50 kB\n")); err == nil {
		t.Fatal("no MemAvailable should be an error")
	}
	if _, _, err := ParseMeminfo(nil); err == nil {
		t.Fatal("empty should be an error")
	}
}

func TestParseDiskstatsWholeDisksOnly(t *testing.T) {
	b := readFile(t, "diskstats")
	// sda, sdb, nvme0n1 and mmcblk0 count; their partitions, dm-0, loop*, md0,
	// zram0 and ram0 do not.
	wantRead := uint64(4426090+529168+80000+14000) * 512
	wantWrite := uint64(5860616+20480+40000+6000) * 512

	// With /sys/block listing the disks.
	r, w, n := ParseDiskstats(b, testHost().WholeDisk())
	if r != wantRead || w != wantWrite || n != 4 {
		t.Fatalf("with /sys/block: read %d write %d, want %d %d", r, w, wantRead, wantWrite)
	}
	// Without /sys/block, partition names are dropped instead.
	noSys := Host{Proc: "testdata/proc", Sys: filepath.Join(t.TempDir(), "missing")}
	r, w, n = ParseDiskstats(b, noSys.WholeDisk())
	if r != wantRead || w != wantWrite || n != 4 {
		t.Fatalf("without /sys/block: read %d write %d, want %d %d", r, w, wantRead, wantWrite)
	}
	// /sys/block is the authority when it is there: a disk it does not list is out.
	sys := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sys, "block", "sda"), 0o750); err != nil {
		t.Fatal(err)
	}
	r, w, n = ParseDiskstats(b, Host{Sys: sys}.WholeDisk())
	if r != 4426090*512 || w != 5860616*512 || n != 1 {
		t.Fatalf("only sda listed: read %d write %d", r, w)
	}
	// nil counts every row.
	if r, _, _ := ParseDiskstats(b, nil); r <= wantRead {
		t.Fatalf("nil filter read %d, want more than %d", r, wantRead)
	}
}

func TestWholeDiskNames(t *testing.T) {
	noSys := Host{Sys: filepath.Join(t.TempDir(), "missing")}.WholeDisk()
	for name, want := range map[string]bool{
		"sda": true, "sda1": false, "sdaa": true, "sdaa12": false,
		"vda": true, "vda1": false, "xvda": true, "xvda2": false,
		"nvme0n1": true, "nvme0n1p2": false, "mmcblk0": true, "mmcblk0p1": false,
		"loop0": false, "ram0": false, "zram0": false, "dm-0": false, "md127": false,
	} {
		if got := noSys(name); got != want {
			t.Errorf("fallback %s = %v, want %v", name, got, want)
		}
	}
}

func TestParseNetDevSkips(t *testing.T) {
	b := readFile(t, "1/net/dev")
	// eth0 and ens5 only: lo, docker0, br-*, veth* and virbr0 are left out.
	in, out, n := ParseNetDev(b, SkipHostIface)
	if in != 1500000 || out != 600000 || n != 2 {
		t.Fatalf("host: in %d out %d, want 1500000 600000", in, out)
	}
	// Only lo is skipped for a container.
	in, out, _ = ParseNetDev(b, SkipLoopback)
	if in != 1000000+500000+7000000+3000000+6000000+1200000+100000 ||
		out != 400000+200000+8000000+2500000+6500000+1100000+50000 {
		t.Fatalf("loopback skipped: in %d out %d", in, out)
	}
	if in, out, _ := ParseNetDev(b, nil); in != 9000000+18800000 || out != 9000000+18750000 {
		t.Fatalf("nothing skipped: in %d out %d", in, out)
	}
	// No space after the colon, a short row and junk are tolerated.
	in, out, n = ParseNetDev([]byte("hdr\nhdr\neth0:5 0 0 0 0 0 0 0 7 0 0 0 0 0 0 0\neth1: 1 2\nnonsense\n"), nil)
	if in != 5 || out != 7 || n != 1 {
		t.Fatalf("tolerance: in %d out %d", in, out)
	}
}

func TestSkipFilters(t *testing.T) {
	for name, want := range map[string]bool{
		"lo": true, "veth12ab": true, "docker0": true, "docker_gwbridge": true,
		"br-3f2a9c1d4e5b": true, "virbr0": true, "eth0": false, "ens5": false, "wlan0": false, "bridge0": false,
	} {
		if got := SkipHostIface(name); got != want {
			t.Errorf("SkipHostIface(%s) = %v, want %v", name, got, want)
		}
	}
	if !SkipLoopback("lo") || SkipLoopback("eth0") || SkipLoopback("veth1") {
		t.Fatal("SkipLoopback skips only lo")
	}
}

func TestHostRead(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	c, err := testHost().Read(now)
	if err != nil {
		t.Fatal(err)
	}
	want := Counters{
		At:       now,
		CPU:      CPUTimes{Busy: 1685880, Total: 99950490, Cores: 4},
		MemUsed:  (8024196 - 5173184) * 1024,
		MemTotal: 8024196 * 1024,
		DiskRead: uint64(4426090+529168+80000+14000) * 512, DiskWrite: uint64(5860616+20480+40000+6000) * 512,
		NetIn: 1500000, NetOut: 600000, DiskOK: true, NetOK: true,
	}
	if c != want {
		t.Fatalf("Read = %+v, want %+v", c, want)
	}
}

// procWithout copies the testdata /proc to a temp dir without the named files.
func procWithout(t *testing.T, drop ...string) Host {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"stat", "meminfo", "diskstats", "1/net/dev"} {
		skip := false
		for _, d := range drop {
			skip = skip || d == name
		}
		if skip {
			continue
		}
		dst := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, readFile(t, name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Host{Proc: dir, Sys: "testdata/sys"}
}

func TestHostReadWithoutNetDev(t *testing.T) {
	c, err := procWithout(t, "1/net/dev").Read(time.Now())
	if err != nil {
		t.Fatalf("a missing net/dev must not fail the reading: %v", err)
	}
	if c.NetOK || c.NetIn != 0 || c.NetOut != 0 {
		t.Fatalf("net: %+v", c)
	}
	if !c.DiskOK || c.DiskRead == 0 || c.CPU.Total == 0 || c.MemTotal == 0 {
		t.Fatalf("the rest should still be read: %+v", c)
	}
}

func TestHostReadWithoutDiskstats(t *testing.T) {
	c, err := procWithout(t, "diskstats").Read(time.Now())
	if err != nil {
		t.Fatalf("a missing diskstats must not fail the reading: %v", err)
	}
	if c.DiskOK || c.DiskRead != 0 || c.DiskWrite != 0 {
		t.Fatalf("disk: %+v", c)
	}
	if !c.NetOK || c.NetIn == 0 || c.CPU.Total == 0 || c.MemTotal == 0 {
		t.Fatalf("the rest should still be read: %+v", c)
	}
}

// A diskstats or net/dev that reads fine but has no row the filters count
// (only partitions and loop devices, only lo and docker0) is no reading: a
// gap, not a zero.
func TestHostReadWithNothingCounted(t *testing.T) {
	h := procWithout(t)
	if err := os.WriteFile(filepath.Join(h.Proc, "diskstats"),
		[]byte("   8       1 sda1 100 0 4000 0 50 0 2000 0 0 0 0\n   7       0 loop0 1 0 8 0 0 0 0 0 0 0 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Proc, "1", "net", "dev"),
		[]byte("hdr\nhdr\n    lo: 5 0 0 0 0 0 0 0 5 0 0 0 0 0 0 0\ndocker0: 9 0 0 0 0 0 0 0 9 0 0 0 0 0 0 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := h.Read(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if c.DiskOK || c.NetOK {
		t.Fatalf("nothing counted should be no reading: %+v", c)
	}
	if c.CPU.Total == 0 || c.MemTotal == 0 {
		t.Fatalf("the rest should still be read: %+v", c)
	}
}

func TestHostReadNeedsStatAndMeminfo(t *testing.T) {
	for _, name := range []string{"stat", "meminfo"} {
		if _, err := procWithout(t, name).Read(time.Now()); err == nil {
			t.Errorf("without %s: want an error", name)
		}
	}
}

func TestContainerNet(t *testing.T) {
	in, out, err := testHost().ContainerNet(4242)
	if err != nil {
		t.Fatal(err)
	}
	if in != 9876543 || out != 1234567 {
		t.Fatalf("in %d out %d, want eth0 only", in, out)
	}
	if _, _, err := testHost().ContainerNet(99999); err == nil {
		t.Fatal("an unknown pid should be an error")
	}
	// A namespace with only lo (network_mode: none) has no usable reading.
	h := procWithout(t)
	if err := os.MkdirAll(filepath.Join(h.Proc, "77", "net"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Proc, "77", "net", "dev"),
		[]byte("hdr\nhdr\n    lo: 5 0 0 0 0 0 0 0 5 0 0 0 0 0 0 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.ContainerNet(77); err == nil {
		t.Fatal("only lo should be an error")
	}
}

func TestRunning(t *testing.T) {
	h := testHost()
	if !h.Running("cloudflared") || !h.Running("systemd") {
		t.Fatal("cloudflared and systemd are in testdata")
	}
	if h.Running("nginx") || h.Running("cloud") {
		t.Fatal("only an exact comm matches")
	}
	if (Host{Proc: "testdata/nope"}).Running("systemd") {
		t.Fatal("an unreadable /proc runs nothing")
	}
}

func TestReadSelf(t *testing.T) {
	u, err := ReadSelf("testdata/proc")
	if err != nil {
		t.Fatal(err)
	}
	if u.CPUTicks != 1234+567 || u.RSS != 16384*1024 {
		t.Fatalf("got %+v", u)
	}
	if _, err := ReadSelf("testdata/nope"); err == nil {
		t.Fatal("missing /proc should be an error")
	}
}

func TestReadSelfTrickyCommName(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "self"), 0o750); err != nil {
		t.Fatal(err)
	}
	// The command name holds spaces and a parenthesis.
	stat := "7 (pa) nel (x) S 1 7 7 0 -1 4194560 8123 0 12 0 100 20 0 0 20 0 11 0 214230\n"
	if err := os.WriteFile(filepath.Join(dir, "self", "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "self", "status"), []byte("Name:\tx\nVmRSS:\t   2048 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u, err := ReadSelf(dir)
	if err != nil {
		t.Fatal(err)
	}
	if u.CPUTicks != 120 || u.RSS != 2048*1024 {
		t.Fatalf("got %+v", u)
	}
	// No VmRSS is an error.
	if err := os.WriteFile(filepath.Join(dir, "self", "status"), []byte("Name:\tx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSelf(dir); err == nil {
		t.Fatal("no VmRSS should be an error")
	}
}

func TestHostUnavailable(t *testing.T) {
	h := Host{Proc: filepath.Join(t.TempDir(), "missing"), Sys: "testdata/sys"}
	if h.Available() {
		t.Fatal("no /proc/stat means unavailable")
	}
	if _, err := h.Read(time.Now()); err == nil {
		t.Fatal("Read should fail")
	}
	if !testHost().Available() {
		t.Fatal("testdata has a /proc/stat")
	}
}
