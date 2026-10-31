package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Mount is one row of a mount table.
type Mount struct {
	Device, Point, FSType string
}

// ParseMountinfo reads /proc/<pid>/mountinfo: the mount point (with its octal
// escapes decoded), then, after the " - " separator, the filesystem type and
// the device. A row it cannot read is skipped.
func ParseMountinfo(b []byte) []Mount {
	var ms []Mount
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		left, right, ok := strings.Cut(sc.Text(), " - ")
		if !ok {
			continue
		}
		l := strings.Fields(left)
		r := strings.Fields(right)
		if len(l) < 5 || len(r) < 2 {
			continue
		}
		ms = append(ms, Mount{Device: unescapeMount(r[1]), Point: unescapeMount(l[4]), FSType: r[0]})
	}
	return ms
}

// unescapeMount decodes the \040-style octal escapes of a mountinfo field.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				sb.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// Disk is one filesystem of the server.
type Disk struct {
	Mount       string
	Used, Total uint64
}

// diskFS are the real block filesystems a disk row is made of.
var diskFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true,
	"btrfs": true, "zfs": true, "f2fs": true, "bcachefs": true,
}

// panelDataLabel names the one fallback row.
const panelDataLabel = "/ (panel data)"

// Disks lists the server's disks from the host's mount table (decision 10):
// real block filesystems only, mount points under /snap and /boot/efi dropped,
// one row per device at its shortest mount point, used and total read with
// statfs under Root. A mount the panel cannot reach is left out. Without a Root
// (an install whose compose file predates the mount) it returns the one
// filesystem holding DataDir and fallback true.
func (h Host) Disks() (disks []Disk, fallback bool) {
	var mounts []Mount
	if fi, err := os.Stat(h.Root); err == nil && fi.IsDir() {
		if b, err := os.ReadFile(filepath.Join(h.Proc, "1", "mountinfo")); err == nil {
			mounts = ParseMountinfo(b)
		}
	}
	if mounts == nil {
		return h.panelDataDisk(), true
	}

	best := make(map[string]string) // device -> shortest mount point
	for _, m := range mounts {
		if !diskFS[m.FSType] || underAny(m.Point, "/snap", "/boot/efi") {
			continue
		}
		if cur, ok := best[m.Device]; !ok || len(m.Point) < len(cur) || (len(m.Point) == len(cur) && m.Point < cur) {
			best[m.Device] = m.Point
		}
	}
	statfs := h.statfs()
	for _, point := range best {
		used, total, err := statfs(filepath.Join(h.Root, point))
		if err != nil {
			continue
		}
		disks = append(disks, Disk{Mount: point, Used: used, Total: total})
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Mount < disks[j].Mount })
	return disks, false
}

func (h Host) panelDataDisk() []Disk {
	used, total, err := h.statfs()(h.DataDir)
	if err != nil {
		return nil
	}
	return []Disk{{Mount: panelDataLabel, Used: used, Total: total}}
}

func (h Host) statfs() func(string) (uint64, uint64, error) {
	if h.Statfs != nil {
		return h.Statfs
	}
	return statfs
}

// underAny reports whether p is one of the roots or below one.
func underAny(p string, roots ...string) bool {
	p = path.Clean(p)
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// statfs is the used and total bytes of the filesystem holding p. Blocks count
// in fragments (Frsize), which is not always Bsize; a zero Frsize falls back to
// Bsize.
func statfs(p string) (used, total uint64, err error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(p, &s); err != nil {
		return 0, 0, fmt.Errorf("metrics: statfs %s: %w", p, err)
	}
	unit := uint64(s.Frsize) //nolint:gosec // G115: statfs sizes are non-negative
	if unit == 0 {
		unit = uint64(s.Bsize) //nolint:gosec // G115: statfs sizes are non-negative
	}
	total = s.Blocks * unit
	if s.Blocks > s.Bfree {
		used = (s.Blocks - s.Bfree) * unit
	}
	return used, total, nil
}
