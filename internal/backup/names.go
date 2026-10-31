package backup

import (
	"fmt"
	"strings"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// FileName is the Drive file name of a backup taken at at:
// <project>_<db>_<YYYYMMDD-HHmm>.sql.gz, or .archive.gz for MongoDB; without
// the .gz when compression is off. The time is in the panel's zone
// (time.Local, from TZ); the parts are slugged.
func FileName(projectName, dbName, dbType string, compressed bool, at time.Time) string {
	return fileName(projectName, dbName, dbType, compressed, at, time.Local)
}

func fileName(projectName, dbName, dbType string, compressed bool, at time.Time, loc *time.Location) string {
	ext := ".sql"
	if dbType == "mongodb" {
		ext = ".archive"
	}
	if compressed {
		ext += ".gz"
	}
	return slugOr(projectName, "project") + "_" + slugOr(dbName, "db") + "_" + at.In(loc).Format("20060102-1504") + ext
}

// ProjectFolder is the project's sub-folder on Drive, under the destination
// folder: its slugged display name. The API sends it (Project.driveFolder), so
// the UI never re-derives it.
func ProjectFolder(displayName string) string { return slugOr(displayName, "project") }

// slug lowercases s and turns every run of characters other than a-z, 0-9, ".",
// "_" and "-" into one "-". Dashes and dots are trimmed from both ends, so a
// slug is never hidden (".x") nor a path element like "..".
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-.")
}

func slugOr(s, fallback string) string {
	if v := slug(s); v != "" {
		return v
	}
	return fallback
}

var byteUnits = []string{"KB", "MB", "GB", "TB", "PB"}

// unitOf returns the unit n is shown in and its size in bytes; size 1 is bytes.
func unitOf(n int64) (string, float64) {
	if n < 1024 {
		return "B", 1
	}
	size := float64(1024)
	for i, u := range byteUnits {
		if float64(n) < size*1024 || i == len(byteUnits)-1 {
			return u, size
		}
		size *= 1024
	}
	return "B", 1 // unreachable
}

// number shows v with one decimal below 10, else none: 4.2, 38, 212.
func number(v float64) string {
	if v < 10 {
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.0f", v)
}

// formatBytes is a size as the UI shows it: 512 B, 4.2 GB, 38 MB (1024-based).
func formatBytes(n int64) string {
	unit, size := unitOf(n)
	if size == 1 {
		return fmt.Sprintf("%d B", n)
	}
	return number(float64(n)/size) + " " + unit
}

// formatProgress is the upload detail: "23.6 / 38 MB · 62%".
func formatProgress(sent, total int64) string {
	pct := int64(100)
	if total > 0 {
		pct = sent * 100 / total
	}
	unit, size := unitOf(total)
	if size == 1 {
		return fmt.Sprintf("%d / %d B · %d%%", sent, total, pct)
	}
	return fmt.Sprintf("%.1f / %s %s · %d%%", float64(sent)/size, number(float64(total)/size), unit, pct)
}

// formatCompress is the Compress detail: "gzip · 212 → 38 MB", or both units
// when they differ: "gzip · 2.1 GB → 300 MB".
func formatCompress(raw, compressed int64) string {
	ru, rs := unitOf(raw)
	cu, cs := unitOf(compressed)
	if ru == cu && rs != 1 {
		return "gzip · " + number(float64(raw)/rs) + " → " + number(float64(compressed)/cs) + " " + cu
	}
	return "gzip · " + formatBytes(raw) + " → " + formatBytes(compressed)
}

// formatDuration is an elapsed time, whole units only: 18s, 3m 12s, 1h 3m.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %dm", s/3600, s%3600/60)
}

// formatSpan is a limit in words: "5 minutes", "6 hours", "1 minute".
func formatSpan(d time.Duration) string {
	plural := func(n int64, word string) string {
		if n == 1 {
			return "1 " + word
		}
		return fmt.Sprintf("%d %ss", n, word)
	}
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return plural(int64(d/time.Hour), "hour")
	case d >= time.Minute && d%time.Minute == 0:
		return plural(int64(d/time.Minute), "minute")
	}
	return d.String()
}

// Compressed says whether a backup's file is gzipped: false only when its
// Compress step was skipped as "Compression off" (the database's compress
// setting was off when it was queued).
func Compressed(b store.Backup) bool {
	for _, st := range b.Steps {
		if st.Step == StepCompress {
			return st.State != stepSkipped || st.Detail != wordsCompressionOff
		}
	}
	return true
}
