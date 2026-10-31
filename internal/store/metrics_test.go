package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// metricsNow is 7 min 45 s into a 5-minute bucket that starts at 12:05 and a
// 30-minute bucket that starts at 12:00.
var metricsNow = time.Date(2026, 10, 31, 12, 7, 45, 0, time.UTC)

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

// rangeFor is the API's grid: n buckets of step, aligned to multiples of the
// step since the epoch, the last one containing now.
func rangeFor(now time.Time, step time.Duration, n int) Range {
	sec := int64(step / time.Second)
	to := time.Unix(now.Unix()/sec*sec, 0).UTC()
	return Range{From: to.Add(-time.Duration(n-1) * step), To: to, Step: step}
}

func cpuAt(ts time.Time, cpu float64) ServerSample {
	return ServerSample{TS: ts, CPU: f64(cpu)}
}

func mustInsert(t *testing.T, s *Store, srv *ServerSample, ps ...ProjectSample) {
	t.Helper()
	if err := s.InsertSamples(context.Background(), srv, ps); err != nil {
		t.Fatal(err)
	}
}

func mustRollup(t *testing.T, s *Store, now time.Time) {
	t.Helper()
	if err := s.Rollup(context.Background(), now); err != nil {
		t.Fatal(err)
	}
}

// rows lists a metrics table as "ts:project:cpu" strings, ordered, for
// comparing whole tables.
func rows(t *testing.T, s *Store, table string) []string {
	t.Helper()
	q, ok := map[string]string{
		"server_samples":  `SELECT ts, 0, cpu FROM server_samples ORDER BY ts`,
		"server_rollups":  `SELECT ts, 0, cpu FROM server_rollups ORDER BY ts`,
		"project_samples": `SELECT ts, project_id, cpu FROM project_samples ORDER BY ts, project_id`,
		"project_rollups": `SELECT ts, project_id, cpu FROM project_rollups ORDER BY ts, project_id`,
	}[table]
	if !ok {
		t.Fatalf("unknown table %s", table)
	}
	rs, err := s.db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var out []string
	for rs.Next() {
		var (
			ts, p int64
			cpu   sql.NullFloat64
		)
		if err := rs.Scan(&ts, &p, &cpu); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s:%d:%v", time.Unix(ts, 0).UTC().Format("Jan 2 15:04:05"), p, cpu.Float64))
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func clock(h, m, sec int) time.Time {
	return time.Date(2026, 10, 31, h, m, sec, 0, time.UTC)
}

func TestMigration0008SeedsRetention(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	days, err := s.MetricsRetention(ctx)
	if err != nil || days != 7 {
		t.Fatalf("MetricsRetention = %d, %v", days, err)
	}
	var updated string
	if err := s.db.QueryRow(`SELECT updated_at FROM metrics_settings WHERE id = 1`).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if _, err := parseTime(updated); err != nil {
		t.Fatalf("seeded updated_at %q: %v", updated, err)
	}
	for _, table := range []string{"server_samples", "server_rollups", "project_samples", "project_rollups"} {
		if n := count(t, s.db, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table); n != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
}

func TestInsertSamplesIgnoresDuplicateTS(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)

	srv := ServerSample{TS: clock(12, 0, 10), CPU: f64(10), MemUsed: i64(100), MemTotal: i64(1000), NetIn: f64(5)}
	mustInsert(t, s, &srv, ProjectSample{ProjectID: shopID, TS: clock(12, 0, 10), CPU: f64(1), Mem: i64(50)})
	// The same 30-second slot (a clock that went back, or an early tick) never overwrites.
	srv2 := cpuAt(clock(12, 0, 25), 99)
	mustInsert(t, s, &srv2, ProjectSample{ProjectID: shopID, TS: clock(12, 0, 25), CPU: f64(99)})

	if got, want := rows(t, s, "server_samples"), []string{"Oct 31 12:00:00:0:10"}; !slices.Equal(got, want) {
		t.Fatalf("server_samples = %v, want %v", got, want)
	}
	if got, want := rows(t, s, "project_samples"), []string{fmt.Sprintf("Oct 31 12:00:00:%d:1", shopID)}; !slices.Equal(got, want) {
		t.Fatalf("project_samples = %v, want %v", got, want)
	}
	var (
		mem, total  int64
		diskRead    sql.NullFloat64
		netIn       float64
		projectNet  sql.NullFloat64
		projectMemV int64
	)
	if err := s.db.QueryRow(`SELECT mem_used, mem_total, disk_read, net_in FROM server_samples`).Scan(&mem, &total, &diskRead, &netIn); err != nil {
		t.Fatal(err)
	}
	if mem != 100 || total != 1000 || diskRead.Valid || netIn != 5 {
		t.Fatalf("server row = %d %d %v %v", mem, total, diskRead, netIn)
	}
	if err := s.db.QueryRow(`SELECT mem, net_in FROM project_samples`).Scan(&projectMemV, &projectNet); err != nil {
		t.Fatal(err)
	}
	if projectMemV != 50 || projectNet.Valid {
		t.Fatalf("project row = %d %v", projectMemV, projectNet)
	}

	// No server sample (its reading failed): only the projects are stored.
	if err := s.InsertSamples(ctx, nil, []ProjectSample{{ProjectID: shopID, TS: clock(12, 0, 40), CPU: f64(2)}}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.db, `SELECT count(*) FROM server_samples`); n != 1 {
		t.Fatalf("server_samples = %d rows", n)
	}
	if n := count(t, s.db, `SELECT count(*) FROM project_samples`); n != 2 {
		t.Fatalf("project_samples = %d rows", n)
	}
}

// A project deleted, untracked to ignored or gone missing between the
// sampler's tracked-list read and its insert gets no row, and the rest of the
// tick (the server row, the other projects) is still stored: a re-tracked
// project starts a fresh history (D6).
func TestInsertSamplesOnlyForTrackedPresentProjects(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, blogID := seedApps(t, s)
	if _, err := s.db.Exec(`UPDATE projects SET state = 'ignored' WHERE id = ?`, blogID); err != nil {
		t.Fatal(err)
	}
	const deletedID = 9999
	srv := cpuAt(clock(12, 0, 10), 10)
	if err := s.InsertSamples(ctx, &srv, []ProjectSample{
		{ProjectID: deletedID, TS: clock(12, 0, 10), CPU: f64(1)},
		{ProjectID: blogID, TS: clock(12, 0, 10), CPU: f64(2)},
		{ProjectID: shopID, TS: clock(12, 0, 10), CPU: f64(3)},
	}); err != nil {
		t.Fatalf("a gone project must not fail the tick: %v", err)
	}
	if got, want := rows(t, s, "server_samples"), []string{"Oct 31 12:00:00:0:10"}; !slices.Equal(got, want) {
		t.Fatalf("server_samples = %v, want %v", got, want)
	}
	if got, want := rows(t, s, "project_samples"), []string{fmt.Sprintf("Oct 31 12:00:00:%d:3", shopID)}; !slices.Equal(got, want) {
		t.Fatalf("project_samples = %v, want %v", got, want)
	}

	// Gone missing meanwhile: no row either.
	if _, err := s.db.Exec(`UPDATE projects SET missing_since = '2026-10-31T12:00:00Z' WHERE id = ?`, shopID); err != nil {
		t.Fatal(err)
	}
	mustInsert(t, s, nil, ProjectSample{ProjectID: shopID, TS: clock(12, 0, 40), CPU: f64(4)})
	if n := count(t, s.db, `SELECT count(*) FROM project_samples`); n != 1 {
		t.Fatalf("project_samples = %d rows, want 1", n)
	}
}

func TestServerSeriesDenseWithGaps(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	r := rangeFor(metricsNow, 30*time.Second, 120)
	if r.Buckets() != 120 {
		t.Fatalf("Buckets = %d", r.Buckets())
	}
	first := ServerSample{TS: r.From.Add(5 * time.Second), CPU: f64(11), MemUsed: i64(1), MemTotal: i64(8)}
	mid := ServerSample{TS: r.From.Add(50 * 30 * time.Second), CPU: f64(22), DiskRead: f64(3), DiskWrite: f64(4)}
	last := ServerSample{TS: metricsNow, CPU: f64(33), NetIn: f64(7), NetOut: f64(8)}
	for _, x := range []ServerSample{first, mid, last} {
		mustInsert(t, s, &x)
	}
	// A sample before the range is not read.
	before := cpuAt(r.From.Add(-30*time.Second), 99)
	mustInsert(t, s, &before)

	got, err := s.ServerSeries(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 120 {
		t.Fatalf("%d points, want 120", len(got))
	}
	gaps := 0
	for i, p := range got {
		if want := r.From.Add(time.Duration(i) * 30 * time.Second); !p.TS.Equal(want) {
			t.Fatalf("point %d at %v, want %v", i, p.TS, want)
		}
		if p.CPU == nil && p.MemUsed == nil && p.MemTotal == nil && p.DiskRead == nil && p.DiskWrite == nil && p.NetIn == nil && p.NetOut == nil {
			gaps++
		}
	}
	if gaps != 117 {
		t.Fatalf("%d empty points, want 117", gaps)
	}
	if p := got[0]; *p.CPU != 11 || *p.MemUsed != 1 || *p.MemTotal != 8 || p.NetIn != nil {
		t.Fatalf("first = %+v", p)
	}
	if p := got[50]; *p.CPU != 22 || *p.DiskRead != 3 || *p.DiskWrite != 4 || p.MemUsed != nil {
		t.Fatalf("middle = %+v", p)
	}
	if p := got[119]; *p.CPU != 33 || *p.NetIn != 7 || *p.NetOut != 8 || p.DiskRead != nil {
		t.Fatalf("last = %+v", p)
	}
}

func TestSeriesRollupStepsAndOpenBucket(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)

	// 24 hours: 5-minute rollups, and the open bucket (12:05) from raw.
	day := rangeFor(metricsNow, 300*time.Second, 288)
	if !day.To.Equal(clock(12, 5, 0)) || day.Buckets() != 288 {
		t.Fatalf("24h range = %+v", day)
	}
	for _, x := range []ServerSample{
		{TS: clock(11, 55, 0), CPU: f64(10), MemUsed: i64(100)},
		{TS: clock(11, 57, 30), CPU: f64(20), MemUsed: i64(200)},
		{TS: clock(12, 1, 0), CPU: f64(30)},
		{TS: clock(12, 5, 0), CPU: f64(40)},
		{TS: clock(12, 7, 30), CPU: f64(60)},
	} {
		mustInsert(t, s, &x)
	}
	mustRollup(t, s, metricsNow)
	got, err := s.ServerSeries(ctx, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 288 {
		t.Fatalf("%d points", len(got))
	}
	if p := got[285]; !p.TS.Equal(clock(11, 55, 0)) || *p.CPU != 15 || *p.MemUsed != 150 {
		t.Fatalf("11:55 = %+v", p)
	}
	if p := got[286]; *p.CPU != 30 || p.MemUsed != nil {
		t.Fatalf("12:00 = %+v", p)
	}
	if p := got[287]; !p.TS.Equal(clock(12, 5, 0)) || *p.CPU != 50 {
		t.Fatalf("open bucket = %+v", p)
	}
	if got[284].CPU != nil {
		t.Fatalf("11:50 = %+v, want a gap", got[284])
	}

	// 7 days: 30-minute averages of the rollups, and the open bucket (12:00)
	// from raw.
	week := rangeFor(metricsNow, 1800*time.Second, 336)
	if !week.To.Equal(clock(12, 0, 0)) {
		t.Fatalf("7d range = %+v", week)
	}
	old := week.From.Add(100 * 1800 * time.Second) // three-odd days ago: rollups only
	for i := range 6 {
		mustExec(t, s.db, `INSERT INTO project_rollups (project_id, ts, cpu, mem) VALUES (?, ?, ?, ?)`,
			shopID, old.Unix()+int64(i)*300, float64(i+1), 1000+int64(i)*10)
	}
	mustExec(t, s.db, `INSERT INTO project_rollups (project_id, ts, net_in) VALUES (?, ?, ?)`,
		shopID, old.Unix()+1800, 8.0)
	mustInsert(t, s, nil,
		ProjectSample{ProjectID: shopID, TS: clock(12, 1, 0), CPU: f64(2)},
		ProjectSample{ProjectID: shopID, TS: clock(12, 6, 0), CPU: f64(4)})
	// 12:00 is rolled now, but the open 30-minute bucket still reads raw only.
	mustRollup(t, s, metricsNow)
	if n := count(t, s.db, `SELECT count(*) FROM project_rollups WHERE ts = ?`, clock(12, 0, 0).Unix()); n != 1 {
		t.Fatalf("12:00 project rollups = %d", n)
	}
	pts, err := s.ProjectSeries(ctx, shopID, week)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 336 {
		t.Fatalf("%d points", len(pts))
	}
	if p := pts[100]; !p.TS.Equal(old) || *p.CPU != 3.5 || *p.Mem != 1025 || p.NetIn != nil {
		t.Fatalf("old bucket = %+v", p)
	}
	if p := pts[101]; p.CPU != nil || *p.NetIn != 8 {
		t.Fatalf("next bucket = %+v", p)
	}
	if p := pts[335]; !p.TS.Equal(clock(12, 0, 0)) || *p.CPU != 3 {
		t.Fatalf("open bucket = %+v", p)
	}
	if pts[334].CPU != nil {
		t.Fatalf("11:30 = %+v, want a gap", pts[334])
	}
}

func TestSeriesReadsUnrolledBucketsFromRaw(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	// The 12:00 bucket is complete but not rolled up yet (the sampler rolls
	// after the boundary): it reads from raw, no gap.
	for _, x := range []ServerSample{cpuAt(clock(11, 56, 0), 10), cpuAt(clock(12, 1, 0), 20)} {
		mustInsert(t, s, &x)
	}
	mustRollup(t, s, clock(12, 0, 10))
	got, err := s.ServerSeries(ctx, rangeFor(metricsNow, 300*time.Second, 288))
	if err != nil {
		t.Fatal(err)
	}
	if p := got[285]; *p.CPU != 10 {
		t.Fatalf("11:55 = %+v", p)
	}
	if p := got[286]; *p.CPU != 20 {
		t.Fatalf("12:00 = %+v", p)
	}
}

func TestRollupIdempotentAndBoundary(t *testing.T) {
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	for _, ts := range []time.Time{clock(11, 55, 10), clock(11, 59, 50), clock(12, 0, 0), clock(12, 5, 30)} {
		x := cpuAt(ts, float64(ts.Minute()))
		mustInsert(t, s, &x, ProjectSample{ProjectID: shopID, TS: ts, CPU: f64(float64(ts.Minute()))})
	}
	mustRollup(t, s, metricsNow)
	srv, proj := rows(t, s, "server_rollups"), rows(t, s, "project_rollups")
	want := []string{"Oct 31 11:55:00:0:57", "Oct 31 12:00:00:0:0"}
	if !slices.Equal(srv, want) {
		t.Fatalf("server_rollups = %v, want %v (the open 12:05 bucket is never rolled)", srv, want)
	}
	if len(proj) != 2 {
		t.Fatalf("project_rollups = %v", proj)
	}
	mustRollup(t, s, metricsNow)
	if got := rows(t, s, "server_rollups"); !slices.Equal(got, srv) {
		t.Fatalf("second rollup changed server_rollups: %v", got)
	}
	if got := rows(t, s, "project_rollups"); !slices.Equal(got, proj) {
		t.Fatalf("second rollup changed project_rollups: %v", got)
	}
	// Once 12:05 has ended it is rolled.
	mustRollup(t, s, clock(12, 10, 0))
	if got := rows(t, s, "server_rollups"); len(got) != 3 || got[2] != "Oct 31 12:05:00:0:5" {
		t.Fatalf("after 12:10 server_rollups = %v", got)
	}
}

func TestRollupLateTrackedProject(t *testing.T) {
	s := open(t)
	_, a, b := seedApps(t, s)
	mustExec(t, s.db, `UPDATE projects SET state = 'tracked' WHERE id = ?`, b) // blog is seeded ignored
	mustInsert(t, s, nil,
		ProjectSample{ProjectID: a, TS: clock(11, 52, 0), CPU: f64(1)},
		ProjectSample{ProjectID: a, TS: clock(11, 57, 0), CPU: f64(2)})
	mustRollup(t, s, clock(12, 0, 10))
	// A raw row of a rolled bucket that changes afterwards is never re-rolled.
	mustExec(t, s.db, `UPDATE project_samples SET cpu = 9 WHERE project_id = ? AND ts = ?`, a, clock(11, 52, 0).Unix())

	// B starts sampling after A's buckets were rolled.
	mustInsert(t, s, nil,
		ProjectSample{ProjectID: a, TS: clock(12, 1, 0), CPU: f64(3)},
		ProjectSample{ProjectID: b, TS: clock(12, 1, 0), CPU: f64(4)},
		ProjectSample{ProjectID: b, TS: clock(12, 3, 0), CPU: f64(6)})
	mustRollup(t, s, clock(12, 5, 10))
	want := []string{
		fmt.Sprintf("Oct 31 11:50:00:%d:1", a),
		fmt.Sprintf("Oct 31 11:55:00:%d:2", a),
		fmt.Sprintf("Oct 31 12:00:00:%d:3", a),
		fmt.Sprintf("Oct 31 12:00:00:%d:5", b),
	}
	if got := rows(t, s, "project_rollups"); !slices.Equal(got, want) {
		t.Fatalf("project_rollups = %v, want %v", got, want)
	}
}

func TestRollupCatchesUpAfterRestart(t *testing.T) {
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	// Raw rows from before a restart, no rollups yet; one older than the
	// 25 hours raw rows are kept is past the catch-up.
	for _, ts := range []time.Time{
		metricsNow.Add(-40 * time.Minute), metricsNow.Add(-35 * time.Minute), metricsNow.Add(-20 * time.Minute),
		metricsNow.Add(-26 * time.Hour),
	} {
		x := cpuAt(ts, 1)
		mustInsert(t, s, &x, ProjectSample{ProjectID: shopID, TS: ts, CPU: f64(1)})
	}
	mustRollup(t, s, metricsNow)
	for _, table := range []string{"server_rollups", "project_rollups"} {
		got := rows(t, s, table)
		if len(got) != 3 || got[0][:15] != "Oct 31 11:25:00" || got[2][:15] != "Oct 31 11:45:00" {
			t.Fatalf("%s = %v", table, got)
		}
	}
}

func TestHistoryStart(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, blogID := seedApps(t, s)
	for _, id := range []int64{0, shopID} {
		if got, err := s.HistoryStart(ctx, id); err != nil || got != nil {
			t.Fatalf("empty HistoryStart(%d) = %v, %v", id, got, err)
		}
	}
	raw := cpuAt(metricsNow.Add(-time.Hour), 1)
	mustInsert(t, s, &raw, ProjectSample{ProjectID: shopID, TS: metricsNow.Add(-2 * time.Hour), CPU: f64(1)})
	rolled := clock(9, 0, 0).Add(-72 * time.Hour)
	mustExec(t, s.db, `INSERT INTO server_rollups (ts, cpu) VALUES (?, 1)`, rolled.Unix())

	if got, err := s.HistoryStart(ctx, 0); err != nil || got == nil || !got.Equal(rolled) {
		t.Fatalf("server HistoryStart = %v, %v, want %v", got, err, rolled)
	}
	want := time.Unix(metricsNow.Add(-2*time.Hour).Unix()/30*30, 0)
	if got, err := s.HistoryStart(ctx, shopID); err != nil || got == nil || !got.Equal(want) {
		t.Fatalf("project HistoryStart = %v, %v, want %v", got, err, want)
	}
	if got, err := s.HistoryStart(ctx, blogID); err != nil || got != nil {
		t.Fatalf("HistoryStart of a project with no rows = %v, %v", got, err)
	}
}

func TestPruneMetricsByBothAges(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	_, shopID, _ := seedApps(t, s)
	at := func(d time.Duration) int64 { return metricsNow.Add(-d).Unix() / 300 * 300 }
	for _, d := range []time.Duration{26 * time.Hour, 24 * time.Hour} {
		mustExec(t, s.db, `INSERT INTO server_samples (ts, cpu) VALUES (?, 1)`, at(d))
		mustExec(t, s.db, `INSERT INTO project_samples (project_id, ts, cpu) VALUES (?, ?, 1)`, shopID, at(d))
	}
	for _, days := range []int{11, 9, 8, 6} {
		d := time.Duration(days) * 24 * time.Hour
		mustExec(t, s.db, `INSERT INTO server_rollups (ts, cpu) VALUES (?, 1)`, at(d))
		mustExec(t, s.db, `INSERT INTO project_rollups (project_id, ts, cpu) VALUES (?, ?, 1)`, shopID, at(d))
	}
	check := func(when string, raw, rolled []time.Duration) {
		t.Helper()
		for _, table := range []string{"server_samples", "project_samples", "server_rollups", "project_rollups"} {
			want := raw
			if table == "server_rollups" || table == "project_rollups" {
				want = rolled
			}
			var exp []string
			for _, d := range want {
				p := "0"
				if strings.HasPrefix(table, "project") {
					p = fmt.Sprint(shopID)
				}
				exp = append(exp, time.Unix(at(d), 0).UTC().Format("Jan 2 15:04:05")+":"+p+":1")
			}
			if got := rows(t, s, table); !slices.Equal(got, exp) {
				t.Fatalf("%s: %s = %v, want %v", when, table, got, exp)
			}
		}
	}

	if err := s.PruneMetrics(ctx, metricsNow); err != nil {
		t.Fatal(err)
	}
	day := 24 * time.Hour
	check("keep 7 days", []time.Duration{24 * time.Hour}, []time.Duration{6 * day})

	// A longer retention keeps older rollups; those already deleted stay deleted.
	mustExec(t, s.db, `INSERT INTO server_rollups (ts, cpu) VALUES (?, 1)`, at(9*day))
	mustExec(t, s.db, `INSERT INTO project_rollups (project_id, ts, cpu) VALUES (?, ?, 1)`, shopID, at(9*day))
	if _, _, err := s.SetMetricsRetention(ctx, 10, metricsNow); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneMetrics(ctx, metricsNow); err != nil {
		t.Fatal(err)
	}
	check("keep 10 days", []time.Duration{24 * time.Hour}, []time.Duration{9 * day, 6 * day})
}

func TestSetMetricsRetentionNoChange(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	updatedAt := func() string {
		t.Helper()
		var v string
		if err := s.db.QueryRow(`SELECT updated_at FROM metrics_settings WHERE id = 1`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	seeded := updatedAt()

	before, changed, err := s.SetMetricsRetention(ctx, 7, schedT1)
	if err != nil || changed || before != 7 {
		t.Fatalf("unchanged SetMetricsRetention = %d, %v, %v", before, changed, err)
	}
	if got := updatedAt(); got != seeded {
		t.Fatalf("updated_at %q, want the seeded %q", got, seeded)
	}

	before, changed, err = s.SetMetricsRetention(ctx, 30, schedT2)
	if err != nil || !changed || before != 7 {
		t.Fatalf("changed SetMetricsRetention = %d, %v, %v", before, changed, err)
	}
	if got := updatedAt(); got != formatTime(schedT2) {
		t.Fatalf("updated_at = %q, want %q", got, formatTime(schedT2))
	}
	if days, err := s.MetricsRetention(ctx); err != nil || days != 30 {
		t.Fatalf("MetricsRetention = %d, %v", days, err)
	}

	// The table's CHECK is the backstop for the API's validation.
	for _, bad := range []int{6, 31} {
		if _, _, err := s.SetMetricsRetention(ctx, bad, schedT3); err == nil {
			t.Fatalf("SetMetricsRetention(%d) stored", bad)
		}
	}
	if days, err := s.MetricsRetention(ctx); err != nil || days != 30 {
		t.Fatalf("after refused sets MetricsRetention = %d, %v", days, err)
	}
}

// seedProjectMetrics gives a project one raw sample and one rollup, and the
// server one of each.
func seedProjectMetrics(t *testing.T, s *Store, projectID int64) {
	t.Helper()
	srv := cpuAt(clock(11, 1, 0), 1)
	mustInsert(t, s, &srv, ProjectSample{ProjectID: projectID, TS: clock(11, 1, 0), CPU: f64(1)})
	mustRollup(t, s, metricsNow)
	if n := count(t, s.db, `SELECT count(*) FROM project_rollups WHERE project_id = ?`, projectID); n != 1 {
		t.Fatalf("project_rollups = %d", n)
	}
}

func TestUntrackDeletesSamples(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	seedProjectMetrics(t, s, shopID)
	if _, err := s.UntrackProject(ctx, shopID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"project_samples", "project_rollups"} {
		if n := count(t, s.db, `SELECT count(*) FROM `+table); n != 0 {
			t.Fatalf("%d rows left in %s", n, table)
		}
	}
	for _, table := range []string{"server_samples", "server_rollups"} {
		if n := count(t, s.db, `SELECT count(*) FROM `+table); n != 1 {
			t.Fatalf("%s = %d rows, want the server's row kept", table, n)
		}
	}

	// A missing project is deleted, its rows by cascade.
	item := newItem(wsID, "/srv/apps/shop", true)
	if _, err := s.AddProjects(ctx, rescanJob(t, s), []AddItem{item}); err != nil {
		t.Fatal(err)
	}
	mustInsert(t, s, nil, ProjectSample{ProjectID: shopID, TS: clock(12, 1, 0), CPU: f64(1)})
	mustExec(t, s.db, `INSERT INTO project_rollups (project_id, ts, cpu) VALUES (?, ?, 1)`, shopID, clock(12, 0, 0).Unix())
	r := RescanApply{Workspaces: []WorkspaceApply{{ID: wsID, LastScanAt: rescanTime, Missing: []string{"/srv/apps/shop"}}}}
	if err := s.ApplyRescan(ctx, r, rescanTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UntrackProject(ctx, shopID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"project_samples", "project_rollups"} {
		if n := count(t, s.db, `SELECT count(*) FROM `+table); n != 0 {
			t.Fatalf("%d rows left in %s after removing a missing project", n, table)
		}
	}
}

func TestRemoveWorkspaceCascadesSamples(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)
	seedProjectMetrics(t, s, shopID)
	if _, err := s.RemoveWorkspace(ctx, wsID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"project_samples", "project_rollups"} {
		if n := count(t, s.db, `SELECT count(*) FROM `+table); n != 0 {
			t.Fatalf("%d rows left in %s", n, table)
		}
	}
	if n := count(t, s.db, `SELECT count(*) FROM server_samples`); n != 1 {
		t.Fatalf("server_samples = %d rows", n)
	}
}

func TestServerSamplesNewColumns(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	full := ServerSample{
		TS: clock(11, 55, 0), CPU: f64(10),
		Load1: f64(1.5), DiskUsed: i64(100), DiskTotal: i64(1000), Tunnel: f64(1),
	}
	second := ServerSample{
		TS: clock(11, 55, 30), CPU: f64(20),
		Load1: f64(2.5), DiskUsed: i64(201), DiskTotal: i64(1000), Tunnel: f64(0),
	}
	empty := ServerSample{TS: clock(11, 56, 0), CPU: f64(30)}
	mustInsert(t, s, &full)
	mustInsert(t, s, &second)
	mustInsert(t, s, &empty)
	mustInsert(t, s, &ServerSample{TS: clock(11, 20, 0), CPU: f64(5)})

	// 1 hour: raw 30-second points carry the values and the nils.
	hour, err := s.ServerSeries(ctx, rangeFor(metricsNow, 30*time.Second, 120))
	if err != nil {
		t.Fatal(err)
	}
	at := func(pts []ServerSample, ts time.Time) ServerSample {
		t.Helper()
		for _, p := range pts {
			if p.TS.Equal(ts) {
				return p
			}
		}
		t.Fatalf("no point at %v", ts)
		return ServerSample{}
	}
	if p := at(hour, full.TS); p.Load1 == nil || *p.Load1 != 1.5 || *p.DiskUsed != 100 || *p.DiskTotal != 1000 || *p.Tunnel != 1 {
		t.Fatalf("11:55:00 = %+v", p)
	}
	if p := at(hour, empty.TS); p.Load1 != nil || p.DiskUsed != nil || p.DiskTotal != nil || p.Tunnel != nil {
		t.Fatalf("11:56:00 = %+v, want nil new fields", p)
	}

	// Rollup, then 24 hours: the 11:55 bucket averages 1 and 0 to 0.5, the
	// disk bytes round to whole numbers, and the 11:55 values the empty tick
	// lacks do not drag them down (avg ignores NULL).
	mustRollup(t, s, metricsNow)
	day, err := s.ServerSeries(ctx, rangeFor(metricsNow, 300*time.Second, 288))
	if err != nil {
		t.Fatal(err)
	}
	p := at(day, clock(11, 55, 0))
	if p.Tunnel == nil || *p.Tunnel != 0.5 {
		t.Fatalf("tunnel = %v, want 0.5", p.Tunnel)
	}
	if p.Load1 == nil || *p.Load1 != 2 {
		t.Fatalf("load1 = %v, want 2", p.Load1)
	}
	if p.DiskUsed == nil || *p.DiskUsed != 151 || *p.DiskTotal != 1000 { // avg(100, 201) = 150.5 rounds to 151
		t.Fatalf("disk = %v / %v, want 151 / 1000", p.DiskUsed, p.DiskTotal)
	}
	// A bucket whose samples all lack the fields stays nil.
	if p := at(day, clock(11, 20, 0)); p.CPU == nil || p.Load1 != nil || p.DiskUsed != nil || p.DiskTotal != nil || p.Tunnel != nil {
		t.Fatalf("11:20 = %+v, want only cpu", p)
	}
}
