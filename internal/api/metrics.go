package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/metrics"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

const (
	msgNoHostProc    = "Server metrics are not available: the host's /proc is not mounted."
	msgBadRange      = "Choose a period: 1h, 24h or 7d."
	msgRetention     = "Enter a whole number of days from 7 to 30."
	panelRowName     = "Server Control Panel"
	retentionTarget  = "Metrics history"
	fieldRange       = "range"
	fieldRetention   = "retentionDays"
	minRetentionDays = 7
	maxRetentionDays = 30
)

// pruneTimeout bounds the prune after a retention save, which outlives the
// request.
const pruneTimeout = 30 * time.Second

// metricRange is one period of the charts: its bucket width and count.
type metricRange struct {
	step time.Duration
	n    int
}

// metricRanges are the periods a series can be read over (1h of raw 30 s
// samples, 24h of 5-minute rollups, 7d of 30-minute averages).
var metricRanges = map[string]metricRange{
	"1h":  {30 * time.Second, 120},
	"24h": {5 * time.Minute, 288},
	"7d":  {30 * time.Minute, 336},
}

// seriesRange reads the range query parameter and returns its key and its
// bucket grid at now: buckets aligned to multiples of the step since the
// epoch, the last one the bucket containing now. A bad range is answered 400.
func (s *server) seriesRange(w http.ResponseWriter, r *http.Request) (string, store.Range, bool) {
	key := r.URL.Query().Get("range")
	mr, ok := metricRanges[key]
	if !ok {
		writeJSON(w, http.StatusBadRequest, QueryError{Error: msgBadRange, Field: fieldRange})
		return "", store.Range{}, false
	}
	step := int64(mr.step / time.Second)
	to := s.Now().Unix() / step * step
	from := to - int64(mr.n-1)*step
	return key, store.Range{From: time.Unix(from, 0), To: time.Unix(to, 0), Step: mr.step}, true
}

// zoneTime is t as RFC 3339 in the server zone.
func (s *server) zoneTime(t time.Time) string { return t.In(s.Zone).Format(time.RFC3339) }

// timeZone is the server zone with its offset now.
func (s *server) timeZone() TimeZone {
	return TimeZone{Name: s.Zone.String(), Offset: s.Now().In(s.Zone).Format("-07:00")}
}

// latestTick is the Monitor's latest tick when it is at most 90 s old by the
// router's clock.
func (s *server) latestTick() (metrics.Tick, bool) {
	tk, ok := s.Metrics.Latest()
	if !ok || s.Now().Sub(tk.At) > metrics.LatestMaxAge {
		return metrics.Tick{}, false
	}
	return tk, true
}

// historyStart is the oldest kept sample of a project (0 = the server) in the
// server zone, nil with none.
func (s *server) historyStart(ctx context.Context, projectID int64) (*string, error) {
	at, err := s.Store.HistoryStart(ctx, projectID)
	if err != nil || at == nil {
		return nil, err
	}
	v := s.zoneTime(*at)
	return &v, nil
}

// serverInfo is GET /api/server: the System info and System services cards,
// the thresholds, the sampling interval, the retention and the server zone.
func (s *server) serverInfo(w http.ResponseWriter, r *http.Request) {
	sys := s.Metrics.System(r.Context())
	days, err := s.Store.MetricsRetention(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	th := s.Metrics.Thresholds()
	info := ServerInfo{
		Available: sys.Available,
		Hostname:  sys.Hostname, OS: sys.OS, Kernel: sys.Kernel, Cores: sys.Cores, RAMBytes: sys.RAMBytes,
		Docker:        DockerService{State: string(sys.Docker), Version: sys.DockerVersion, ContainersRunning: sys.ContainersRunning},
		Cloudflared:   TunnelService{State: string(sys.Tunnel.State), Hostnames: sys.Tunnel.Hostnames},
		Disks:         make([]DiskInfo, 0, len(sys.Disks)),
		DisksPartial:  sys.DisksFallback,
		Thresholds:    Thresholds{CPUPercent: th.CPUPercent, RAMPercent: th.RAMPercent, DiskPercent: th.DiskPercent},
		SampleSeconds: int(metrics.Interval / time.Second),
		RetentionDays: days,
		TimeZone:      s.timeZone(),
	}
	if !sys.Available {
		info.Reason = msgNoHostProc
	}
	for _, d := range sys.Disks {
		info.Disks = append(info.Disks, DiskInfo{Mount: d.Mount, UsedBytes: d.Used, TotalBytes: d.Total})
	}
	writeJSON(w, http.StatusOK, info)
}

// serverMetrics is GET /api/server/metrics?range=: the server's series over
// the period, one point per bucket (null values in a gap), the oldest kept
// sample and the newest sample when it is at most 90 s old.
func (s *server) serverMetrics(w http.ResponseWriter, r *http.Request) {
	key, rg, ok := s.seriesRange(w, r)
	if !ok {
		return
	}
	tk, fresh := s.latestTick()
	samples, err := s.Store.ServerSeries(r.Context(), rg)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	hs, err := s.historyStart(r.Context(), 0)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := ServerSeries{
		Range: key, StepSeconds: int(rg.Step / time.Second), From: s.zoneTime(rg.From), To: s.zoneTime(rg.To),
		HistoryStart: hs, Points: make([]ServerPoint, 0, len(samples)),
	}
	for _, p := range samples {
		out.Points = append(out.Points, ServerPoint{
			T: s.zoneTime(p.TS), CPU: p.CPU, MemUsed: p.MemUsed, MemTotal: p.MemTotal,
			DiskRead: p.DiskRead, DiskWrite: p.DiskWrite, NetIn: p.NetIn, NetOut: p.NetOut,
		})
	}
	if fresh && tk.Server != nil {
		v := tk.Server
		out.Latest = &ServerLatest{
			At: s.zoneTime(tk.At), CPU: v.CPU, MemUsed: v.MemUsed, MemTotal: v.MemTotal,
			DiskRead: v.DiskRead, DiskWrite: v.DiskWrite, NetIn: v.NetIn, NetOut: v.NetOut,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// projectMetrics is GET /api/projects/{id}/metrics?range=: one tracked
// project's series, like serverMetrics; 404 for a project that is not
// tracked. Its latest sample is the latest tick's while the project runs.
func (s *server) projectMetrics(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	}
	key, rg, ok := s.seriesRange(w, r)
	if !ok {
		return
	}
	switch _, err := s.Store.TrackedProject(r.Context(), id); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	tk, fresh := s.latestTick()
	samples, err := s.Store.ProjectSeries(r.Context(), id, rg)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	hs, err := s.historyStart(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := ProjectSeries{
		Range: key, StepSeconds: int(rg.Step / time.Second), From: s.zoneTime(rg.From), To: s.zoneTime(rg.To),
		HistoryStart: hs, Points: make([]ProjectPoint, 0, len(samples)),
	}
	for _, p := range samples {
		out.Points = append(out.Points, ProjectPoint{T: s.zoneTime(p.TS), CPU: p.CPU, Mem: p.Mem, NetIn: p.NetIn, NetOut: p.NetOut})
	}
	if p, ok := tk.Projects[id]; fresh && ok && p.Running {
		out.Latest = &ProjectLatest{At: s.zoneTime(tk.At), CPU: p.CPU, Mem: p.Mem, NetIn: p.NetIn, NetOut: p.NetOut}
	}
	writeJSON(w, http.StatusOK, out)
}

// serverUsage is GET /api/server/usage, the Usage by app table: a row per
// tracked, present project, plus the panel's own row when its project is not
// tracked, with the live values of the latest tick (at most 90 s old) and
// the hourly disk usage; the totals are the latest server sample's and the
// used space of the listed disks. The status comes from the hub's cached
// read (null without a recent one); the handler never reads Docker.
func (s *server) serverUsage(w http.ResponseWriter, r *http.Request) {
	tk, fresh := s.latestTick()
	disk := s.Metrics.Disk()
	disks, _ := s.Metrics.Disks()
	ts, err := s.Store.TrackedProjects(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	statuses := s.cachedStatuses(ts)

	out := UsageReport{Rows: []UsageRow{}}
	if fresh {
		at := s.zoneTime(tk.At)
		out.At = &at
		if v := tk.Server; v != nil {
			out.Totals.CPUPercent, out.Totals.MemBytes = v.CPU, v.MemUsed
			out.Totals.NetBps = sum(v.NetIn, v.NetOut)
		}
	}
	if len(disks) > 0 {
		var used uint64
		for _, d := range disks {
			used += d.Used
		}
		out.Totals.DiskUsedBytes = &used
	}

	cores := max(1, tk.Cores)
	selfTracked := false
	for _, t := range ts {
		if t.MissingSince != nil {
			continue
		}
		self := t.Self || (tk.SelfProject != "" && t.ComposeProject == tk.SelfProject)
		selfTracked = selfTracked || self
		row := UsageRow{ProjectID: &t.ID, Name: t.DisplayName, Self: self}
		if st, ok := statuses[t.ID]; ok {
			row.Status = &st
		}
		if p, ok := tk.Projects[t.ID]; fresh && ok {
			row.CPUPercent, row.MemBytes, row.NetBps = usageValues(p, cores)
		}
		if u, ok := disk[t.ID]; ok {
			bytes, at := u.Volumes+u.Folder, s.zoneTime(u.At)
			row.DiskBytes, row.DiskPartial, row.DiskAt = &bytes, u.Partial, &at
		}
		out.Rows = append(out.Rows, row)
	}
	if !selfTracked {
		row := UsageRow{Name: panelRowName, Self: true}
		if fresh && tk.Self != nil {
			row.CPUPercent, row.MemBytes, row.NetBps = usageValues(*tk.Self, cores)
		}
		out.Rows = append(out.Rows, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// cachedStatuses are the projects' statuses as the project list computes
// them, from the hub's container list when it is at most as old as the latest
// tick may be; empty without one. It never reads Docker.
func (s *server) cachedStatuses(ts []store.Tracked) map[int64]string {
	cs, up, ok := s.Hub.CachedContainers(metrics.LatestMaxAge)
	if !ok {
		return nil
	}
	out := make(map[int64]string, len(ts))
	for _, v := range control.Views(ts, cs, nil, up, s.Control.Lock, nil, nil, drive.Status{}) {
		out[v.ID] = string(v.Status)
	}
	return out
}

// usageValues are a project's CPU as a share of the server (Docker's % over
// the core count), its RAM and its network in + out.
func usageValues(p metrics.ProjectNow, cores int) (cpu *float64, mem *int64, net *float64) {
	if p.CPU != nil {
		v := *p.CPU / float64(cores)
		cpu = &v
	}
	return cpu, p.Mem, sum(p.NetIn, p.NetOut)
}

// sum is a + b, nil when either is unknown.
func sum(a, b *float64) *float64 {
	if a == nil || b == nil {
		return nil
	}
	v := *a + *b
	return &v
}

// metricsSettings is GET /api/metrics-settings.
func (s *server) metricsSettings(w http.ResponseWriter, r *http.Request) {
	days, err := s.Store.MetricsRetention(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, MetricsSettings{RetentionDays: days})
}

// saveMetricsSettings is POST /api/metrics-settings {retentionDays}: a whole
// number from 7 to 30, else 422 on retentionDays (code range). A save that
// changes the value is recorded as metrics.retention and prunes at once, so
// a lowered retention deletes the older history now (bounded by
// pruneTimeout); a failed prune is logged (the hourly one retries) and does
// not fail the save.
func (s *server) saveMetricsSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RetentionDays json.RawMessage `json:"retentionDays"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	var days int
	if req.RetentionDays == nil || json.Unmarshal(req.RetentionDays, &days) != nil ||
		days < minRetentionDays || days > maxRetentionDays {
		writeFieldError(w, fieldRetention, "range", msgRetention)
		return
	}
	// The write outlives the request, so a saved change keeps its activity row.
	ctx := context.WithoutCancel(r.Context())
	before, changed, err := s.Store.SetMetricsRetention(ctx, days, s.Now())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if changed {
		s.record(r, store.Activity{Action: store.ActMetricsRetention, Target: retentionTarget}, store.ResultOK,
			fmt.Sprintf("retention: %d → %d days", before, days))
		pctx, cancel := context.WithTimeout(ctx, pruneTimeout)
		err := s.Metrics.Prune(pctx)
		cancel()
		if err != nil {
			s.log.Warn("metrics: cannot prune after a retention save; the hourly prune retries", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, MetricsSettings{RetentionDays: days})
}
