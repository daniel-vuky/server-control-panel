// Package metrics reads the server's own numbers: the host's CPU, memory,
// disk I/O and network from /proc (decision 9) and its disks from the host's
// mount table (decision 10), and samples them with the projects' usage into
// the store (the Sampler). The host readings are plain file reads or a statfs;
// Docker is read only through the runner and the hub: this package never
// starts a process itself (ADR 0005).
package metrics

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Host is where the host's files are mounted in the panel's container. The
// panel uses /host/proc, /sys, /host/root and its data directory.
type Host struct {
	Proc    string // the host's /proc
	Sys     string // /sys (its block devices are the host's)
	Root    string // the host's root, read-only; empty or missing means the fallback
	DataDir string // $PANEL_DATA_DIR, for the one-disk fallback

	// Statfs returns the used and total bytes of the filesystem holding path.
	// Nil means syscall.Statfs; tests set it.
	Statfs func(path string) (used, total uint64, err error)
}

// Available reports whether the host's /proc/stat can be read; without it there
// are no server metrics.
func (h Host) Available() bool {
	_, err := os.ReadFile(filepath.Join(h.Proc, "stat"))
	return err == nil
}

// CPUTimes are the aggregate jiffy counters of /proc/stat. Cores is the number
// of cpu<N> lines.
type CPUTimes struct {
	Busy, Total uint64
	Cores       int
}

// ParseStat reads the aggregate cpu line: total is its first 8 columns (user,
// nice, system, idle, iowait, irq, softirq, steal; guest and guest_nice are
// already inside user and nice) and busy is total minus idle and iowait. An
// older kernel with fewer columns counts the missing ones as 0.
func ParseStat(b []byte) (CPUTimes, error) {
	var t CPUTimes
	found := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		if f[0] != "cpu" {
			if _, err := strconv.Atoi(f[0][3:]); err == nil {
				t.Cores++
			}
			continue
		}
		if len(f) < 5 {
			return CPUTimes{}, errors.New("metrics: short cpu line in stat")
		}
		var col [8]uint64
		for i := 0; i < len(col) && i+1 < len(f); i++ {
			v, err := strconv.ParseUint(f[i+1], 10, 64)
			if err != nil {
				return CPUTimes{}, fmt.Errorf("metrics: bad cpu column in stat: %w", err)
			}
			col[i] = v
			t.Total += v
		}
		t.Busy = t.Total - col[3] - col[4]
		found = true
	}
	if !found {
		return CPUTimes{}, errors.New("metrics: no cpu line in stat")
	}
	return t, nil
}

// CPUPercent is the busy share between two reads, 0 to 100. It is false when
// the total did not advance (the same read twice, or a counter that went back).
func CPUPercent(prev, cur CPUTimes) (float64, bool) {
	if cur.Total <= prev.Total {
		return 0, false
	}
	var busy uint64
	if cur.Busy > prev.Busy {
		busy = cur.Busy - prev.Busy
	}
	p := float64(busy) / float64(cur.Total-prev.Total) * 100
	if p > 100 {
		p = 100
	}
	return p, true
}

// ParseMeminfo returns the memory in use (MemTotal minus MemAvailable) and the
// total, in bytes.
func ParseMeminfo(b []byte) (used, total int64, err error) {
	var avail int64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, haveTotal = kib(f)
		case "MemAvailable:":
			avail, haveAvail = kib(f)
		}
	}
	if !haveTotal || !haveAvail {
		return 0, 0, errors.New("metrics: meminfo has no MemTotal or MemAvailable")
	}
	if avail > total {
		avail = total
	}
	return total - avail, total, nil
}

// kib is the value of a "Name: 123 kB" line in bytes.
func kib(f []string) (int64, bool) {
	v, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v * 1024, true
}

var (
	// notWholeDisk are the devices that sit on top of others or hold no disk.
	notWholeDisk = []string{"loop", "ram", "zram", "dm-", "md"}
	// partitionName is how a partition is named when /sys/block cannot say.
	partitionName = regexp.MustCompile(`^(?:(?:sd|vd|xvd)[a-z]+[0-9]+|(?:nvme[0-9]+n[0-9]+|mmcblk[0-9]+)p[0-9]+)$`)
)

func excludedDisk(name string) bool {
	for _, p := range notWholeDisk {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// WholeDisk says which /proc/diskstats names are whole disks: the names in
// /sys/block minus loop, ram, zram, dm and md devices, so no byte is counted
// twice. When /sys/block cannot be read, names that end like a partition are
// dropped instead.
func (h Host) WholeDisk() func(string) bool {
	entries, err := os.ReadDir(filepath.Join(h.Sys, "block"))
	if err != nil {
		return func(name string) bool {
			return !excludedDisk(name) && !partitionName.MatchString(name)
		}
	}
	listed := make(map[string]bool, len(entries))
	for _, e := range entries {
		listed[e.Name()] = true
	}
	return func(name string) bool { return listed[name] && !excludedDisk(name) }
}

// ParseDiskstats sums the bytes read and written (sectors of 512) of the
// devices whole accepts; nil accepts every device.
func ParseDiskstats(b []byte, whole func(string) bool) (readBytes, writeBytes uint64) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || (whole != nil && !whole(f[2])) {
			continue
		}
		r, errR := strconv.ParseUint(f[5], 10, 64)
		w, errW := strconv.ParseUint(f[9], 10, 64)
		if errR != nil || errW != nil {
			continue
		}
		readBytes += r * 512
		writeBytes += w * 512
	}
	return readBytes, writeBytes
}

// SkipHostIface skips the interfaces whose traffic is also counted elsewhere:
// loopback, container veth ends, bridges and docker's own.
func SkipHostIface(name string) bool {
	return name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") ||
		strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "virbr")
}

// SkipLoopback skips only lo, for a container's own namespace.
func SkipLoopback(name string) bool { return name == "lo" }

// ParseNetDev sums the bytes received and sent over the interfaces skip does
// not drop (nil drops none).
func ParseNetDev(b []byte, skip func(string) bool) (in, out uint64) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if len(f) < 9 || (skip != nil && skip(name)) {
			continue
		}
		rx, errR := strconv.ParseUint(f[0], 10, 64)
		tx, errT := strconv.ParseUint(f[8], 10, 64)
		if errR != nil || errT != nil {
			continue
		}
		in += rx
		out += tx
	}
	return in, out
}

// Counters are the host's cumulative numbers at one moment; a rate is the
// difference of two reads.
type Counters struct {
	At                  time.Time
	CPU                 CPUTimes
	MemUsed, MemTotal   int64
	DiskRead, DiskWrite uint64
	NetIn, NetOut       uint64
	// DiskOK and NetOK are false when that file could not be read, so the
	// sampler stores a gap for just that series.
	DiskOK, NetOK bool
}

// Read takes one set of host counters. Network is pid 1's namespace, the
// host's, through /proc/1/net/dev (a container's own /proc/net would show only
// the panel's namespace). Only stat and meminfo are required: when diskstats or
// net/dev cannot be read the rest is still returned, with DiskOK or NetOK false.
func (h Host) Read(now time.Time) (Counters, error) {
	c := Counters{At: now}
	b, err := os.ReadFile(filepath.Join(h.Proc, "stat"))
	if err != nil {
		return Counters{}, fmt.Errorf("metrics: read stat: %w", err)
	}
	if c.CPU, err = ParseStat(b); err != nil {
		return Counters{}, err
	}
	if b, err = os.ReadFile(filepath.Join(h.Proc, "meminfo")); err != nil {
		return Counters{}, fmt.Errorf("metrics: read meminfo: %w", err)
	}
	if c.MemUsed, c.MemTotal, err = ParseMeminfo(b); err != nil {
		return Counters{}, err
	}
	if b, err = os.ReadFile(filepath.Join(h.Proc, "diskstats")); err == nil {
		c.DiskRead, c.DiskWrite = ParseDiskstats(b, h.WholeDisk())
		c.DiskOK = true
	}
	if b, err = os.ReadFile(filepath.Join(h.Proc, "1", "net", "dev")); err == nil {
		c.NetIn, c.NetOut = ParseNetDev(b, SkipHostIface)
		c.NetOK = true
	}
	return c, nil
}

// ContainerNet is the bytes in and out of a container's own namespace, read
// through any of its pids (loopback skipped).
func (h Host) ContainerNet(pid int) (in, out uint64, err error) {
	b, err := os.ReadFile(filepath.Join(h.Proc, strconv.Itoa(pid), "net", "dev"))
	if err != nil {
		return 0, 0, fmt.Errorf("metrics: read net/dev of pid %d: %w", pid, err)
	}
	in, out = ParseNetDev(b, SkipLoopback)
	return in, out, nil
}

// Running reports whether any process of the host has this command name (the
// kernel's comm, cut to 15 characters). It reads every pid's comm, so callers
// cache the answer.
func (h Host) Running(comm string) bool {
	entries, err := os.ReadDir(h.Proc)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(h.Proc, e.Name(), "comm"))
		if err == nil && strings.TrimSpace(string(b)) == comm {
			return true
		}
	}
	return false
}

// SelfUsage is the panel process's own use: CPU in clock ticks (user plus
// system, cumulative) and resident memory in bytes.
type SelfUsage struct {
	CPUTicks uint64
	RSS      int64
}

// ReadSelf reads the panel's own usage from its /proc (not the host's).
func ReadSelf(proc string) (SelfUsage, error) {
	var u SelfUsage
	b, err := os.ReadFile(filepath.Join(proc, "self", "stat")) //nolint:gosec // G304: the caller's /proc
	if err != nil {
		return u, fmt.Errorf("metrics: read self stat: %w", err)
	}
	// The command name is in parentheses and may hold spaces or parentheses, so
	// the fields start after the last ")": state is field 3, utime 14, stime 15.
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return u, errors.New("metrics: bad self stat")
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 13 {
		return u, errors.New("metrics: short self stat")
	}
	utime, errU := strconv.ParseUint(f[11], 10, 64)
	stime, errS := strconv.ParseUint(f[12], 10, 64)
	if errU != nil || errS != nil {
		return u, errors.New("metrics: bad cpu ticks in self stat")
	}
	u.CPUTicks = utime + stime

	b, err = os.ReadFile(filepath.Join(proc, "self", "status")) //nolint:gosec // G304: the caller's /proc
	if err != nil {
		return u, fmt.Errorf("metrics: read self status: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "VmRSS:" {
			rss, ok := kib(f)
			if !ok {
				return u, errors.New("metrics: bad VmRSS in self status")
			}
			u.RSS = rss
			return u, nil
		}
	}
	return u, errors.New("metrics: no VmRSS in self status")
}
