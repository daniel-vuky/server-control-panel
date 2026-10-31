package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// walkMaxEntries and walkDeadline bound one project folder's walk
	// (decision 17); a walk that hits either is partial.
	walkMaxEntries = 500000
	walkDeadline   = 60 * time.Second
	// diskFirst is the wait before the first disk usage read after a start,
	// diskEvery the wait between two reads (docker system df walks every
	// volume and layer, which is real disk I/O on the server).
	diskFirst = 2 * time.Minute
	diskEvery = 60 * time.Minute
)

// DiskUsage is a project's disk use: its named volumes and its folder, in
// bytes. Partial means the value is a lower bound ("≥"): part of the folder
// could not be read, a walk cap was hit, or the volumes were never read. At is
// when it was read.
type DiskUsage struct {
	Volumes, Folder int64
	Partial         bool
	At              time.Time
}

// composeProjectLabel names a volume's compose project. Compose project names
// cannot hold a comma, so the "k=v,k=v" labels string splits safely on it.
const composeProjectLabel = "com.docker.compose.project="

// sizeUnits are the decimal units docker prints sizes in (go-units HumanSize).
var sizeUnits = map[string]float64{
	"B": 1, "kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "PB": 1e15,
}

// ParseVolumes reads `docker system df -v --format {{json .Volumes}}`: one
// JSON array of volumes whose Labels is a "k=v,k=v" string and Size a decimal
// size such as "232.7MB". It returns each compose project's summed volume
// bytes; a volume with no compose project label (anonymous, or created by
// hand) or a size it cannot read (N/A) is left out. It returns nil when the
// output is not that array.
func ParseVolumes(b []byte) map[string]int64 {
	var vs []struct {
		Labels string
		Size   string
	}
	if err := json.Unmarshal(b, &vs); err != nil {
		return nil
	}
	out := map[string]int64{}
	for _, v := range vs {
		var proj string
		for _, l := range strings.Split(v.Labels, ",") {
			if p, ok := strings.CutPrefix(l, composeProjectLabel); ok {
				proj = p
			}
		}
		n, ok := parseSize(v.Size)
		if proj == "" || !ok {
			continue
		}
		out[proj] += n
	}
	return out
}

// parseSize reads a docker size: a number and a decimal unit.
func parseSize(s string) (int64, bool) {
	i := strings.IndexFunc(s, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if i <= 0 {
		return 0, false
	}
	unit, ok := sizeUnits[s[i:]]
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return int64(math.Round(n * unit)), true
}

// WalkFolder sums the space allocated to the files under root (blocks of 512
// bytes, as du counts; a hard-linked file once), without following symlinks or
// leaving root's filesystem. It stops after maxEntries entries, at the
// deadline or when ctx ends. partial is true when it stopped early or skipped
// anything it could not read, including root itself (0 bytes then).
func WalkFolder(ctx context.Context, root string, maxEntries int, deadline time.Duration) (bytes int64, partial bool) {
	bytes, partial, _ = walkFolder(ctx, root, maxEntries, deadline, os.ReadDir)
	return bytes, partial
}

// walkFolder is WalkFolder with the folder reader as a seam (root reads every
// folder, so a test makes one unreadable here). err is set only when root
// itself cannot be read.
func walkFolder(ctx context.Context, root string, maxEntries int, deadline time.Duration,
	readDir func(string) ([]os.DirEntry, error)) (bytes int64, partial bool, err error) {
	fi, err := os.Lstat(root)
	if err != nil {
		return 0, true, fmt.Errorf("metrics: read %s: %w", root, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, true, errors.New("metrics: no stat for " + root)
	}
	dev := st.Dev
	bytes = st.Blocks * 512
	if !fi.IsDir() {
		return bytes, false, nil
	}
	stop := time.Now().Add(deadline)
	seen := map[uint64]bool{} // inodes of hard-linked files already counted
	entries := 1
	dirs := []string{root}
	for len(dirs) > 0 {
		dir := dirs[len(dirs)-1]
		dirs = dirs[:len(dirs)-1]
		des, err := readDir(dir) // on an error, the entries read before it
		if err != nil {
			partial = true
		}
		for _, de := range des {
			entries++
			if entries > maxEntries || ctx.Err() != nil || !time.Now().Before(stop) {
				return bytes, true, nil
			}
			info, err := de.Info() // an lstat: a symlink is the link itself
			if err != nil {
				partial = true
				continue
			}
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok || st.Dev != dev {
				continue // another filesystem mounted inside: not counted, not entered
			}
			if !info.IsDir() && st.Nlink > 1 {
				if seen[st.Ino] {
					continue
				}
				seen[st.Ino] = true
			}
			bytes += st.Blocks * 512
			if info.IsDir() {
				dirs = append(dirs, filepath.Join(dir, de.Name()))
			}
		}
	}
	return bytes, partial, nil
}
