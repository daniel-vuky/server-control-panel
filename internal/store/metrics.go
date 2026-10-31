package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Metrics tiers (migration 0008). Raw samples are stored at sampleSeconds
// slots and kept rawKeep; rollups are the averages of rollupSeconds buckets,
// kept for the retention setting. Every ts is unix seconds.
const (
	sampleSeconds = 30
	rollupSeconds = 300
	rawKeep       = 25 * time.Hour
)

// ServerSample is one reading of the whole server: CPU % of the machine, RAM
// bytes used and total, and disk and network rates in bytes per second. A nil
// field is a value that could not be read (a gap). In a series TS is the
// bucket start and each field the bucket's average.
type ServerSample struct {
	TS        time.Time
	CPU       *float64
	MemUsed   *int64
	MemTotal  *int64
	DiskRead  *float64
	DiskWrite *float64
	NetIn     *float64
	NetOut    *float64
}

// ProjectSample is one reading of a tracked project: CPU in Docker's % (100 =
// one core) and RAM bytes summed over its containers, and its network rates
// in bytes per second. Nil fields are gaps.
type ProjectSample struct {
	ProjectID int64
	TS        time.Time
	CPU       *float64
	Mem       *int64
	NetIn     *float64
	NetOut    *float64
}

// Range is the bucket grid of a series: From is the first bucket's start, To
// the last one's, Step the bucket width (a whole number of seconds).
type Range struct {
	From, To time.Time
	Step     time.Duration
}

// Buckets is the number of buckets in the range.
func (r Range) Buckets() int {
	if r.Step <= 0 || r.To.Before(r.From) {
		return 0
	}
	return int(r.To.Sub(r.From)/r.Step) + 1
}

// The value columns of each tier and their averages. Memory is whole bytes,
// so its average is rounded back to an integer; the average of an all-null
// group is null (a gap).
const (
	serverCols  = `cpu, mem_used, mem_total, disk_read, disk_write, net_in, net_out`
	serverAvgs  = `avg(cpu), CAST(round(avg(mem_used)) AS INTEGER), CAST(round(avg(mem_total)) AS INTEGER), avg(disk_read), avg(disk_write), avg(net_in), avg(net_out)`
	projectCols = `cpu, mem, net_in, net_out`
	projectAvgs = `avg(cpu), CAST(round(avg(mem)) AS INTEGER), avg(net_in), avg(net_out)`
)

// The rollup statements (args: the fallback watermark, the open bucket's
// start). The project one groups by project and bucket across every project.
const (
	rollupServerSQL = `INSERT OR IGNORE INTO server_rollups (ts, ` + serverCols + `)
		SELECT ts / 300 * 300 AS bucket, ` + serverAvgs + ` FROM server_samples
		WHERE ts >= coalesce((SELECT max(ts) + 300 FROM server_rollups), ?) AND ts < ?
		GROUP BY bucket`
	rollupProjectSQL = `INSERT OR IGNORE INTO project_rollups (project_id, ts, ` + projectCols + `)
		SELECT project_id, ts / 300 * 300 AS bucket, ` + projectAvgs + ` FROM project_samples
		WHERE ts >= coalesce((SELECT max(ts) + 300 FROM project_rollups), ?) AND ts < ?
		GROUP BY project_id, bucket`
)

// The series statements average 5-minute (or 30-second) units per bucket: the
// rollups in [from, w) and the raw samples in [w, end) averaged per unit. Args:
// from, w, unit, unit, w, end, from, step; the project ones take the project id
// before each from and w pair.
const (
	serverSeriesSQL = `WITH units (ts, ` + serverCols + `) AS (
			SELECT ts, ` + serverCols + ` FROM server_rollups WHERE ts >= ? AND ts < ?
			UNION ALL
			SELECT ts / ? * ?, ` + serverAvgs + ` FROM server_samples WHERE ts >= ? AND ts < ? GROUP BY 1
		)
		SELECT (ts - ?) / ?, ` + serverAvgs + ` FROM units GROUP BY 1`
	projectSeriesSQL = `WITH units (ts, ` + projectCols + `) AS (
			SELECT ts, ` + projectCols + ` FROM project_rollups WHERE project_id = ? AND ts >= ? AND ts < ?
			UNION ALL
			SELECT ts / ? * ?, ` + projectAvgs + ` FROM project_samples WHERE project_id = ? AND ts >= ? AND ts < ? GROUP BY 1
		)
		SELECT (ts - ?) / ?, ` + projectAvgs + ` FROM units GROUP BY 1`
)

// metricsTier is one series' statements. mem marks the value columns that are
// whole bytes, in column order.
type metricsTier struct {
	series, watermark string
	mem               []bool
	project           bool
}

var (
	serverTier = metricsTier{
		series:    serverSeriesSQL,
		watermark: `SELECT max(ts) + 300 FROM server_rollups`,
		mem:       []bool{false, true, true, false, false, false, false},
	}
	projectTier = metricsTier{
		series:    projectSeriesSQL,
		watermark: `SELECT max(ts) + 300 FROM project_rollups`,
		mem:       []bool{false, true, false, false},
		project:   true,
	}
)

// InsertSamples stores one tick: the server sample (nil when it could not be
// read) and the project samples, in one transaction. Each ts is truncated to
// its 30-second slot; a slot already stored is kept (INSERT OR IGNORE), so a
// clock that went back never overwrites.
func (s *Store) InsertSamples(ctx context.Context, srv *ServerSample, ps []ProjectSample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: insert samples: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if srv != nil {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO server_samples (ts, cpu, mem_used, mem_total, disk_read, disk_write, net_in, net_out)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			slot(srv.TS), srv.CPU, srv.MemUsed, srv.MemTotal, srv.DiskRead, srv.DiskWrite, srv.NetIn, srv.NetOut); err != nil {
			return fmt.Errorf("store: insert samples: %w", err)
		}
	}
	for _, p := range ps {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO project_samples (project_id, ts, cpu, mem, net_in, net_out) VALUES (?, ?, ?, ?, ?, ?)`,
			p.ProjectID, slot(p.TS), p.CPU, p.Mem, p.NetIn, p.NetOut); err != nil {
			return fmt.Errorf("store: insert samples: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: insert samples: %w", err)
	}
	return nil
}

// slot is t's 30-second slot in unix seconds.
func slot(t time.Time) int64 { return t.Unix() / sampleSeconds * sampleSeconds }

// Rollup averages the completed 5-minute buckets that are not rolled up yet,
// for the server and every project, and leaves the bucket containing now
// open. The watermark is per table and global: the newest rollup bucket of
// that table + 300 (never per project, so the project statement rolls every
// project's buckets at once), or now − 25 h with no rollup, so a restart
// catches up from the raw rows still kept. It is idempotent.
func (s *Store) Rollup(ctx context.Context, now time.Time) error {
	cur := now.Unix() / rollupSeconds * rollupSeconds
	fallback := now.Add(-rawKeep).Unix() / rollupSeconds * rollupSeconds
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: roll up metrics: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{rollupServerSQL, rollupProjectSQL} {
		if _, err := tx.ExecContext(ctx, q, fallback, cur); err != nil {
			return fmt.Errorf("store: roll up metrics: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: roll up metrics: %w", err)
	}
	return nil
}

// ServerSeries reads the server's series over r: exactly r.Buckets() points,
// TS the bucket start, nil fields where no sample (see series).
func (s *Store) ServerSeries(ctx context.Context, r Range) ([]ServerSample, error) {
	out := make([]ServerSample, r.Buckets())
	for i := range out {
		out[i].TS = bucketStart(r, i)
	}
	err := s.series(ctx, serverTier, 0, r, func(i int, v []any) {
		out[i].CPU, out[i].MemUsed, out[i].MemTotal = floatOf(v[0]), intOf(v[1]), intOf(v[2])
		out[i].DiskRead, out[i].DiskWrite = floatOf(v[3]), floatOf(v[4])
		out[i].NetIn, out[i].NetOut = floatOf(v[5]), floatOf(v[6])
	})
	if err != nil {
		return nil, fmt.Errorf("store: read server series: %w", err)
	}
	return out, nil
}

// ProjectSeries reads one project's series over r, like ServerSeries.
func (s *Store) ProjectSeries(ctx context.Context, projectID int64, r Range) ([]ProjectSample, error) {
	out := make([]ProjectSample, r.Buckets())
	for i := range out {
		out[i].ProjectID, out[i].TS = projectID, bucketStart(r, i)
	}
	err := s.series(ctx, projectTier, projectID, r, func(i int, v []any) {
		out[i].CPU, out[i].Mem = floatOf(v[0]), intOf(v[1])
		out[i].NetIn, out[i].NetOut = floatOf(v[2]), floatOf(v[3])
	})
	if err != nil {
		return nil, fmt.Errorf("store: read project series: %w", err)
	}
	return out, nil
}

func bucketStart(r Range, i int) time.Time {
	return r.From.Add(time.Duration(i) * r.Step).UTC()
}

// series averages a tier per bucket of r and calls set for each bucket that
// has a row, with the values in column order (*sql.NullInt64 where t.mem,
// else *sql.NullFloat64). A step under 5 minutes reads raw samples. A longer
// step averages 5-minute units: the rollups before the table's watermark and,
// from there on, raw samples averaged per 5 minutes, so a completed bucket not
// rolled up yet is no gap and the last bucket (the one containing now) always
// comes from raw. The two parts never overlap, so a Rollup that commits
// between the watermark read and the series read changes nothing.
func (s *Store) series(ctx context.Context, t metricsTier, projectID int64, r Range, set func(i int, v []any)) error {
	n := r.Buckets()
	if n == 0 || r.Step%time.Second != 0 {
		return errors.New("bad range")
	}
	step := int64(r.Step / time.Second)
	from, to := r.From.Unix(), r.To.Unix()
	unit, w := int64(sampleSeconds), from // raw only: no rollup is read
	if step >= rollupSeconds {
		unit, w = rollupSeconds, to
		var wm sql.NullInt64
		if err := s.db.QueryRowContext(ctx, t.watermark).Scan(&wm); err != nil {
			return err
		}
		if !wm.Valid {
			w = from
		} else if wm.Int64 < to {
			w = max(wm.Int64, from)
		}
	}
	scoped := func(a ...any) []any {
		if t.project {
			return append([]any{projectID}, a...)
		}
		return a
	}
	args := scoped(from, w)
	args = append(args, unit, unit)
	args = append(args, scoped(w, to+step)...)
	args = append(args, from, step)
	rows, err := s.db.QueryContext(ctx, t.series, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var i int
		v := make([]any, len(t.mem))
		dest := []any{&i}
		for k, mem := range t.mem {
			if mem {
				v[k] = &sql.NullInt64{}
			} else {
				v[k] = &sql.NullFloat64{}
			}
			dest = append(dest, v[k])
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		if i >= 0 && i < n {
			set(i, v)
		}
	}
	return rows.Err()
}

func floatOf(v any) *float64 {
	n := v.(*sql.NullFloat64)
	if !n.Valid {
		return nil
	}
	return &n.Float64
}

func intOf(v any) *int64 {
	n := v.(*sql.NullInt64)
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

// HistoryStart is the oldest kept timestamp of a project (0 = the server)
// across raw samples and rollups, or nil with none.
func (s *Store) HistoryStart(ctx context.Context, projectID int64) (*time.Time, error) {
	q := `SELECT min(m) FROM (SELECT min(ts) AS m FROM server_samples UNION ALL SELECT min(ts) FROM server_rollups)`
	var args []any
	if projectID != 0 {
		q = `SELECT min(m) FROM (SELECT min(ts) AS m FROM project_samples WHERE project_id = ?
			UNION ALL SELECT min(ts) FROM project_rollups WHERE project_id = ?)`
		args = []any{projectID, projectID}
	}
	var oldest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&oldest); err != nil {
		return nil, fmt.Errorf("store: read history start: %w", err)
	}
	if !oldest.Valid {
		return nil, nil
	}
	at := time.Unix(oldest.Int64, 0).UTC()
	return &at, nil
}

// PruneMetrics deletes the raw samples older than 25 hours and the rollups
// older than the retention setting.
func (s *Store) PruneMetrics(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: prune metrics: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	days, err := readRetention(ctx, tx)
	if err != nil {
		return fmt.Errorf("store: prune metrics: %w", err)
	}
	rawCut := now.Add(-rawKeep).Unix()
	rollupCut := now.Add(-time.Duration(days) * 24 * time.Hour).Unix()
	for _, d := range []struct {
		q   string
		cut int64
	}{
		{`DELETE FROM server_samples WHERE ts < ?`, rawCut},
		{`DELETE FROM project_samples WHERE ts < ?`, rawCut},
		{`DELETE FROM server_rollups WHERE ts < ?`, rollupCut},
		{`DELETE FROM project_rollups WHERE ts < ?`, rollupCut},
	} {
		if _, err := tx.ExecContext(ctx, d.q, d.cut); err != nil {
			return fmt.Errorf("store: prune metrics: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: prune metrics: %w", err)
	}
	return nil
}

// MetricsRetention returns how many days rollups are kept.
func (s *Store) MetricsRetention(ctx context.Context) (int, error) {
	days, err := readRetention(ctx, s.db)
	if err != nil {
		return 0, fmt.Errorf("store: read metrics retention: %w", err)
	}
	return days, nil
}

func readRetention(ctx context.Context, q rowQueryer) (int, error) {
	var days int
	err := q.QueryRowContext(ctx, `SELECT retention_days FROM metrics_settings WHERE id = 1`).Scan(&days)
	return days, err
}

// SetMetricsRetention stores how many days rollups are kept and returns the
// value it had. When nothing changed it writes nothing (updated_at included)
// and changed is false. The range (7 to 30) is the API's to check; the
// table's CHECK refuses anything else as an error.
func (s *Store) SetMetricsRetention(ctx context.Context, days int, now time.Time) (before int, changed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("store: set metrics retention: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	before, err = readRetention(ctx, tx)
	if err != nil {
		return 0, false, fmt.Errorf("store: set metrics retention: %w", err)
	}
	if before == days {
		return before, false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE metrics_settings SET retention_days = ?, updated_at = ? WHERE id = 1`,
		days, formatTime(now)); err != nil {
		return 0, false, fmt.Errorf("store: set metrics retention: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("store: set metrics retention: %w", err)
	}
	return before, true, nil
}
