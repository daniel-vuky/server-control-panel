package backup

import (
	"testing"
	"time"
)

func TestFileName(t *testing.T) {
	at := time.Date(2026, 10, 30, 23, 45, 9, 0, time.UTC)
	cases := []struct {
		project, db, typ string
		loc              *time.Location
		want             string
	}{
		{"shop", "shop", "postgresql", time.UTC, "shop_shop_20261030-2345.sql.gz"},
		{"My Shop", "crm_db", "mysql", time.UTC, "my-shop_crm_db_20261030-2345.sql.gz"},
		{"Notes", "notes", "mongodb", time.UTC, "notes_notes_20261030-2345.archive.gz"},
		// Every run of other characters is one dash; dashes and dots at the ends go.
		{"  Café / Bar!! ", "a b$c", "postgresql", time.UTC, "caf-bar_a-b-c_20261030-2345.sql.gz"},
		{"..hidden", "../x", "mysql", time.UTC, "hidden_x_20261030-2345.sql.gz"},
		{"v1.2_app-x", "db.main", "postgresql", time.UTC, "v1.2_app-x_db.main_20261030-2345.sql.gz"},
		// Nothing left after slugging falls back to a word.
		{"!!!", "", "postgresql", time.UTC, "project_db_20261030-2345.sql.gz"},
		// The time is in the panel's zone: 23:45 UTC is 06:45 the next day at +07.
		{"shop", "shop", "postgresql", time.FixedZone("ICT", 7*3600), "shop_shop_20261031-0645.sql.gz"},
	}
	for _, tc := range cases {
		if got := fileName(tc.project, tc.db, tc.typ, at, tc.loc); got != tc.want {
			t.Errorf("fileName(%q, %q, %q, %s) = %q, want %q", tc.project, tc.db, tc.typ, tc.loc, got, tc.want)
		}
	}
	if got, want := FileName("shop", "shop", "postgresql", at), fileName("shop", "shop", "postgresql", at, time.Local); got != want {
		t.Errorf("FileName = %q, want the time.Local name %q", got, want)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Shop":           "shop",
		"a__b..c--d":     "a__b..c--d",
		"Ünïcödé name":   "n-c-d-name",
		"tab\tnew\nline": "tab-new-line",
		"":               "",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int64]string{
		0:                 "0 B",
		512:               "512 B",
		1024:              "1.0 KB",
		38 << 20:          "38 MB",
		212 << 20:         "212 MB",
		4*(1<<30) + 1<<28: "4.2 GB",
		500 << 20:         "500 MB",
		1<<40 + 1<<39:     "1.5 TB",
	} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
	if got := formatProgress(23*(1<<20)+(6<<20)/10, 38<<20); got != "23.6 / 38 MB · 62%" {
		t.Errorf("formatProgress = %q", got)
	}
	if got := formatCompress(212<<20, 38<<20); got != "gzip · 212 → 38 MB" {
		t.Errorf("formatCompress same unit = %q", got)
	}
	if got := formatCompress(2*(1<<30)+(1<<27), 300<<20); got != "gzip · 2.1 GB → 300 MB" {
		t.Errorf("formatCompress = %q", got)
	}
	for d, want := range map[time.Duration]string{
		0:                                     "0s",
		18*time.Second + 400*time.Millisecond: "18s",
		3*time.Minute + 12*time.Second:        "3m 12s",
		time.Hour + 3*time.Minute + 5*time.Second: "1h 3m",
	} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
