package project

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// maxErrorChars caps Inspect.Error: a docker error can carry a long message.
const maxErrorChars = 300

// Inspect is what docker.inspect tells about one container beyond docker ps.
type Inspect struct {
	RestartCount int
	StartedAt    time.Time // the zero time when the container never started
	FinishedAt   time.Time
	ExitCode     int
	OOMKilled    bool
	Error        string // State.Error, cut to 300 characters
	Health       string // healthy, unhealthy, starting; "" without a health check
	Pid          int    // State.Pid, the container's main process on the host; 0 when not running
	NetworkMode  string // HostConfig.NetworkMode: "host", "bridge", "default", a network name
}

// stateJSON is the part of `docker inspect` State the panel reads.
type stateJSON struct {
	StartedAt  time.Time `json:"StartedAt"`
	FinishedAt time.Time `json:"FinishedAt"`
	ExitCode   int       `json:"ExitCode"`
	Pid        int       `json:"Pid"`
	OOMKilled  bool      `json:"OOMKilled"`
	Error      string    `json:"Error"`
	Health     *struct {
		Status string `json:"Status"`
	} `json:"Health"`
}

// ParseInspect reads docker.inspect output: one line per container,
// "<id>\t<restart count>\t<network mode>\t<State JSON>". The result is keyed
// by container id. A line that does not parse is skipped. Tabs and newlines
// inside State.Error are JSON escapes, so they cannot split a line.
func ParseInspect(out []byte) map[string]Inspect {
	m := map[string]Inspect{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSuffix(line, "\r")
		f := strings.SplitN(line, "\t", 4)
		if len(f) != 4 || f[0] == "" {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil || n < 0 {
			continue
		}
		var st stateJSON
		if err := json.Unmarshal([]byte(f[3]), &st); err != nil {
			continue
		}
		in := Inspect{
			RestartCount: n,
			NetworkMode:  f[2],
			Pid:          st.Pid,
			StartedAt:    st.StartedAt,
			FinishedAt:   st.FinishedAt,
			ExitCode:     st.ExitCode,
			OOMKilled:    st.OOMKilled,
			Error:        cutChars(st.Error, maxErrorChars),
		}
		if st.Health != nil {
			in.Health = st.Health.Status
		}
		m[f[0]] = in
	}
	return m
}

// cutChars keeps at most n characters (runes) of s.
func cutChars(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Usage is one container's CPU and memory from docker.stats.
type Usage struct {
	CPUPercent float64 // Docker's percentage; above 100 on several cores
	MemBytes   int64   // the used part of MemUsage
}

var byteUnits = map[string]float64{
	"B":  1,
	"kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12,
	"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40,
}

// ParseStats reads docker.stats output: "<id>\t12.34%\t412MiB / 7.6GiB". The
// result is keyed by container id. A line that does not parse, including the
// "--" Docker prints when it has no value, is dropped.
func ParseStats(out []byte) map[string]Usage {
	m := map[string]Usage{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSuffix(line, "\r")
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[0] == "" {
			continue
		}
		cpu, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(f[1]), "%"), 64)
		if err != nil || math.IsNaN(cpu) || math.IsInf(cpu, 0) || cpu < 0 {
			continue
		}
		used, _, _ := strings.Cut(f[2], "/")
		mem, ok := parseBytes(used)
		if !ok {
			continue
		}
		m[f[0]] = Usage{CPUPercent: cpu, MemBytes: mem}
	}
	return m
}

// parseBytes reads "412MiB" or "1.5 GB" into bytes.
func parseBytes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return !unicode.IsDigit(r) && r != '.' })
	if i <= 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	mult, ok := byteUnits[strings.TrimSpace(s[i:])]
	if !ok {
		return 0, false
	}
	v := math.Round(n * mult)
	if v > math.MaxInt64 {
		return 0, false
	}
	return int64(v), true
}

// FormatPorts turns the docker ps Ports column into display strings: a
// published port as "3000→3000" (the IPv4 and IPv6 duplicates merged, "/tcp"
// dropped, "/udp" kept) and an exposed but unpublished one as "5432 (internal)".
// The result is never nil.
func FormatPorts(raw string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		spec, proto := p, "tcp"
		if i := strings.LastIndex(p, "/"); i >= 0 {
			spec, proto = p[:i], p[i+1:]
		}
		suffix := ""
		if proto != "tcp" {
			suffix = "/" + proto
		}
		var s string
		if host, container, ok := strings.Cut(spec, "->"); ok {
			if i := strings.LastIndex(host, ":"); i >= 0 {
				host = host[i+1:]
			}
			s = host + "→" + container + suffix
		} else {
			s = spec + suffix + " (internal)"
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ExitReason says why a container that is not running is not. The first match
// wins: out of memory, the engine's error text, restarting (with the last exit
// code), dead, exited (with the code), created and never started. A running or
// paused container has none. Without inspect data (hasInspect false) only the
// docker ps fields are used.
func ExitReason(c Container, in Inspect, hasInspect bool) string {
	if c.State == "running" || c.State == "paused" {
		return ""
	}
	if hasInspect {
		if in.OOMKilled {
			return "Out of memory"
		}
		if in.Error != "" {
			return in.Error
		}
	}
	code, hasCode := c.ExitCode, c.HasExitCode
	if hasInspect {
		code, hasCode = in.ExitCode, true
	}
	switch c.State {
	case "restarting":
		if hasCode {
			return "Restarting (exit " + strconv.Itoa(code) + ")"
		}
		return "Restarting"
	case "dead":
		return "Dead"
	case "exited":
		if hasCode {
			return "Exited (" + strconv.Itoa(code) + ")"
		}
		return "Exited"
	case "created":
		return "Created, never started"
	}
	return ""
}

// Component is one container of a project, or a service that has none.
type Component struct {
	Name         string
	Service      string
	State        string // docker's state word; "" for a service with no container
	Status       string // Running, Restarting, Exited, Dead, Paused, Created, Not created
	Image        string
	Ports        []string
	CPUPercent   *float64 // nil unless the container runs and the last read covered it
	MemBytes     *int64
	RestartCount int
	StartedAt    *time.Time // nil when the container never started or inspect is missing
	ExitReason   string
	Health       string
}

const statusNotCreated = "Not created"

// statusWord is the table word for a docker state.
func statusWord(state string) string {
	switch state {
	case "running":
		return "Running"
	case "restarting":
		return "Restarting"
	case "exited":
		return "Exited"
	case "dead":
		return "Dead"
	case "paused":
		return "Paused"
	case "created":
		return "Created"
	}
	if state == "" {
		return ""
	}
	return strings.ToUpper(state[:1]) + state[1:]
}

// Components lists the containers of composeProject (one-off containers and
// other projects' containers excluded) with their inspect and usage data, plus
// a Not created row for every expected service without a container. Rows are
// sorted by service, then name. in and use are keyed by container id and may be
// nil; CPU and RAM are set only for running containers that use covers.
func Components(cs []Container, composeProject string, expected []string, in map[string]Inspect, use map[string]Usage) []Component {
	comps := []Component{}
	has := map[string]bool{}
	for _, c := range cs {
		if c.Project != composeProject || c.OneOff {
			continue
		}
		has[c.Service] = true
		ins, hasIns := in[c.ID]
		comp := Component{
			Name:       c.Name,
			Service:    c.Service,
			State:      c.State,
			Status:     statusWord(c.State),
			Image:      c.Image,
			Ports:      FormatPorts(c.Ports),
			ExitReason: ExitReason(c, ins, hasIns),
		}
		if hasIns {
			comp.RestartCount = ins.RestartCount
			comp.Health = ins.Health
			if !ins.StartedAt.IsZero() {
				t := ins.StartedAt
				comp.StartedAt = &t
			}
		}
		if u, ok := use[c.ID]; ok && c.State == "running" {
			cpu, mem := u.CPUPercent, u.MemBytes
			comp.CPUPercent, comp.MemBytes = &cpu, &mem
		}
		comps = append(comps, comp)
	}
	for _, s := range expected {
		if !has[s] {
			has[s] = true
			comps = append(comps, Component{Name: s, Service: s, Status: statusNotCreated, Ports: []string{}})
		}
	}
	sort.SliceStable(comps, func(i, j int) bool {
		if comps[i].Service != comps[j].Service {
			return comps[i].Service < comps[j].Service
		}
		return comps[i].Name < comps[j].Name
	})
	return comps
}

// Totals sums CPU and RAM over the running components that have values (nil
// when none has) and returns the earliest StartedAt among the running ones
// (nil when none).
func Totals(comps []Component) (cpu *float64, mem *int64, upSince *time.Time) {
	for _, c := range comps {
		if c.Status != "Running" {
			continue
		}
		if c.CPUPercent != nil {
			if cpu == nil {
				cpu = new(float64)
			}
			*cpu += *c.CPUPercent
		}
		if c.MemBytes != nil {
			if mem == nil {
				mem = new(int64)
			}
			*mem += *c.MemBytes
		}
		if c.StartedAt != nil && (upSince == nil || c.StartedAt.Before(*upSince)) {
			t := *c.StartedAt
			upSince = &t
		}
	}
	return cpu, mem, upSince
}
