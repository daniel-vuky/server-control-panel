package project

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseInspect(t *testing.T) {
	idA, idB, idC := id64(0xa), id64(0xb), id64(0xc)
	longErr := strings.Repeat("é", 400)
	out := strings.Join([]string{
		// no Health key at all
		idA + "\t0\t" + `{"Status":"running","StartedAt":"2026-10-01T08:00:00.123456789Z","FinishedAt":"0001-01-01T00:00:00Z","ExitCode":0,"OOMKilled":false,"Error":""}`,
		// Health.Status, a restart count, an OOM kill
		idB + "\t7\t" + `{"Status":"exited","StartedAt":"2026-10-01T08:00:00Z","FinishedAt":"2026-10-02T09:30:00Z","ExitCode":137,"OOMKilled":true,"Error":"","Health":{"Status":"unhealthy","FailingStreak":3}}`,
		// a tab and a newline inside Error are JSON-escaped, so the line stays one line
		idC + "\t1\t" + `{"Status":"created","StartedAt":"0001-01-01T00:00:00Z","FinishedAt":"0001-01-01T00:00:00Z","ExitCode":127,"Error":"oci runtime:\tno such file\nsecond line"}`,
		"total garbage",
		id64(0xd) + "\tnot-a-number\t" + `{"Status":"running"}`,
		id64(0xe) + "\t0\t{not json",
		"\t0\t{}", // no id
		id64(0xf) + "\t2\t" + `{"Error":"` + longErr + `"}`,
		"",
	}, "\n") + "\n"

	got := ParseInspect([]byte(out))
	if len(got) != 4 {
		t.Fatalf("parsed %d lines, want 4: %+v", len(got), got)
	}
	a := got[idA]
	if a.RestartCount != 0 || a.Health != "" || a.ExitCode != 0 || a.OOMKilled || a.Error != "" {
		t.Fatalf("a = %+v", a)
	}
	if want := time.Date(2026, 10, 1, 8, 0, 0, 123456789, time.UTC); !a.StartedAt.Equal(want) {
		t.Fatalf("a.StartedAt = %v", a.StartedAt)
	}
	if !a.FinishedAt.IsZero() {
		t.Fatalf("a.FinishedAt = %v, want the zero time", a.FinishedAt)
	}
	b := got[idB]
	if b.RestartCount != 7 || b.Health != "unhealthy" || b.ExitCode != 137 || !b.OOMKilled {
		t.Fatalf("b = %+v", b)
	}
	if want := time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC); !b.FinishedAt.Equal(want) {
		t.Fatalf("b.FinishedAt = %v", b.FinishedAt)
	}
	c := got[idC]
	if c.Error != "oci runtime:\tno such file\nsecond line" || c.ExitCode != 127 || c.RestartCount != 1 {
		t.Fatalf("c = %+v", c)
	}
	if e := got[id64(0xf)].Error; len([]rune(e)) != 300 {
		t.Fatalf("Error has %d characters, want 300", len([]rune(e)))
	}
}

func TestParseInspectEmpty(t *testing.T) {
	if got := ParseInspect(nil); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestParseStatsUnits(t *testing.T) {
	tests := []struct {
		mem  string
		want int64
	}{
		{"0B / 0B", 0},
		{"512B / 1GiB", 512},
		{"1kB / 1GiB", 1000},
		{"1KB / 1GiB", 1000},
		{"1MB / 1GiB", 1_000_000},
		{"1GB / 1TB", 1_000_000_000},
		{"1KiB / 1GiB", 1 << 10},
		{"412MiB / 7.6GiB", 412 << 20},
		{"1GiB / 7.6GiB", 1 << 30},
		{"1.5MiB / 7.6GiB", 3 << 19},
		{"2.5kB / 7.6GiB", 2500},
		{"1TiB / 2TiB", 1 << 40},
	}
	for _, tt := range tests {
		t.Run(tt.mem, func(t *testing.T) {
			id := id64(1)
			got := ParseStats([]byte(id + "\t12.34%\t" + tt.mem + "\n"))
			u, ok := got[id]
			if !ok {
				t.Fatalf("line dropped: %+v", got)
			}
			if u.MemBytes != tt.want {
				t.Fatalf("MemBytes = %d, want %d", u.MemBytes, tt.want)
			}
			if u.CPUPercent != 12.34 {
				t.Fatalf("CPUPercent = %v", u.CPUPercent)
			}
		})
	}
}

func TestParseStatsDropsBadLines(t *testing.T) {
	out := strings.Join([]string{
		id64(1) + "\t0.00%\t10MiB / 1GiB",
		id64(2) + "\t--\t--",
		id64(3) + "\t--\t10MiB / 1GiB",
		id64(4) + "\t1.0%\t-- / --",
		id64(5) + "\t1.0%\t10XiB / 1GiB",
		id64(6) + "\tabc%\t10MiB / 1GiB",
		id64(7) + "\t350.5%\t1GiB / 2GiB", // above 100 % on several cores
		"garbage",
		"\t1%\t1MiB / 2MiB", // no id
		"",
	}, "\n")
	got := ParseStats([]byte(out))
	if len(got) != 2 {
		t.Fatalf("kept %d lines, want 2: %+v", len(got), got)
	}
	if u := got[id64(1)]; u.CPUPercent != 0 || u.MemBytes != 10<<20 {
		t.Fatalf("first = %+v", u)
	}
	if u := got[id64(7)]; u.CPUPercent != 350.5 || u.MemBytes != 1<<30 {
		t.Fatalf("seventh = %+v", u)
	}
}

func TestFormatPorts(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", []string{}},
		{"published, ipv4 and ipv6 merged", "0.0.0.0:3000->3000/tcp, :::3000->3000/tcp", []string{"3000→3000"}},
		{"internal only", "5432/tcp", []string{"5432 (internal)"}},
		{"udp keeps its protocol", "0.0.0.0:53->53/udp, :::53->53/udp", []string{"53→53/udp"}},
		{"internal udp", "5353/udp", []string{"5353/udp (internal)"}},
		{"different host and container port", "127.0.0.1:8080->80/tcp", []string{"8080→80"}},
		{"published and internal, order kept", "3000/tcp, 0.0.0.0:80->80/tcp, :::80->80/tcp", []string{"3000 (internal)", "80→80"}},
		{"a range", "0.0.0.0:8000-8002->8000-8002/tcp", []string{"8000-8002→8000-8002"}},
		{"bracketed ipv6", "[::]:9000->9000/tcp", []string{"9000→9000"}},
		{"tcp and udp of one port are two entries", "0.0.0.0:53->53/tcp, 0.0.0.0:53->53/udp", []string{"53→53", "53→53/udp"}},
		{"spaces only", "   ", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatPorts(tt.raw)
			if got == nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FormatPorts(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestExitReasonOrder(t *testing.T) {
	mk := func(state string, code int, has bool) Container {
		return Container{State: state, ExitCode: code, HasExitCode: has}
	}
	tests := []struct {
		name string
		c    Container
		in   Inspect
		has  bool
		want string
	}{
		{"running is empty", mk("running", 0, false), Inspect{}, true, ""},
		{"running ignores a stale OOM flag", mk("running", 0, false), Inspect{OOMKilled: true}, true, ""},
		{"paused is empty", mk("paused", 0, false), Inspect{}, true, ""},
		{"oom beats everything", mk("exited", 137, true), Inspect{OOMKilled: true, Error: "boom", ExitCode: 137}, true, "Out of memory"},
		{"oom on a restarting container", mk("restarting", 0, false), Inspect{OOMKilled: true}, true, "Out of memory"},
		{"error text beats state", mk("exited", 1, true), Inspect{Error: "no such file", ExitCode: 1}, true, "no such file"},
		{"error on a created container", mk("created", 0, false), Inspect{Error: "port is already allocated"}, true, "port is already allocated"},
		{"restarting with the inspect exit code", mk("restarting", 0, false), Inspect{ExitCode: 2}, true, "Restarting (exit 2)"},
		{"restarting without inspect uses the ps code", mk("restarting", 3, true), Inspect{}, false, "Restarting (exit 3)"},
		{"restarting with no code at all", mk("restarting", 0, false), Inspect{}, false, "Restarting"},
		{"dead", mk("dead", 0, false), Inspect{}, true, "Dead"},
		{"exited with inspect", mk("exited", 0, false), Inspect{ExitCode: 1}, true, "Exited (1)"},
		{"exited without inspect uses the ps code", mk("exited", 137, true), Inspect{}, false, "Exited (137)"},
		{"exited zero", mk("exited", 0, true), Inspect{}, false, "Exited (0)"},
		{"exited with no code at all", mk("exited", 0, false), Inspect{}, false, "Exited"},
		{"created", mk("created", 0, false), Inspect{}, true, "Created, never started"},
		{"oom flag without inspect is not seen", mk("exited", 1, true), Inspect{OOMKilled: true, Error: "x"}, false, "Exited (1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitReason(tt.c, tt.in, tt.has); got != tt.want {
				t.Fatalf("ExitReason = %q, want %q", got, tt.want)
			}
		})
	}
}

func cont(service, name, state string, n int) Container {
	return Container{Project: "shop", Service: service, Name: name, State: state, ID: id64(n), Image: service + ":1"}
}

func TestComponentsNotCreatedAndSort(t *testing.T) {
	oneOff := cont("migrate", "shop-migrate-run-1", "exited", 90)
	oneOff.OneOff = true
	other := cont("web", "other-web-1", "running", 91)
	other.Project = "other"
	web2 := cont("web", "shop-web-2", "running", 3)
	web1 := cont("web", "shop-web-1", "running", 2)
	web1.Ports = "0.0.0.0:80->80/tcp, :::80->80/tcp"
	db := cont("db", "shop-db-1", "exited", 1)
	db.ExitCode, db.HasExitCode = 1, true

	in := map[string]Inspect{
		id64(1): {ExitCode: 1, RestartCount: 4, StartedAt: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), Error: "bad config"},
		id64(2): {StartedAt: time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC), Health: "healthy"},
	}
	got := Components([]Container{web2, oneOff, other, db, web1}, "shop", []string{"web", "db", "cache"}, in, nil)

	var names []string
	for _, c := range got {
		names = append(names, c.Service+"/"+c.Name+"/"+c.Status)
	}
	want := []string{"cache/cache/Not created", "db/shop-db-1/Exited", "web/shop-web-1/Running", "web/shop-web-2/Running"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("components = %q, want %q", names, want)
	}

	nc := got[0]
	if nc.State != "" || nc.Image != "" || nc.Ports == nil || len(nc.Ports) != 0 ||
		nc.CPUPercent != nil || nc.MemBytes != nil || nc.StartedAt != nil || nc.RestartCount != 0 || nc.ExitReason != "" {
		t.Fatalf("not created row = %+v", nc)
	}
	d := got[1]
	if d.ExitReason != "bad config" || d.RestartCount != 4 || d.Image != "db:1" || d.State != "exited" {
		t.Fatalf("db = %+v", d)
	}
	if d.StartedAt == nil || !d.StartedAt.Equal(in[id64(1)].StartedAt) {
		t.Fatalf("db.StartedAt = %v", d.StartedAt)
	}
	w1 := got[2]
	if !reflect.DeepEqual(w1.Ports, []string{"80→80"}) || w1.Health != "healthy" || w1.ExitReason != "" {
		t.Fatalf("web1 = %+v", w1)
	}
	if w2 := got[3]; w2.Ports == nil || w2.StartedAt != nil || w2.Health != "" {
		t.Fatalf("web2 (no inspect) = %+v", w2)
	}
}

func TestComponentsStatusWords(t *testing.T) {
	tests := map[string]string{
		"running": "Running", "restarting": "Restarting", "exited": "Exited",
		"dead": "Dead", "paused": "Paused", "created": "Created",
	}
	for state, word := range tests {
		got := Components([]Container{cont("web", "n", state, 1)}, "shop", []string{"web"}, nil, nil)
		if len(got) != 1 || got[0].Status != word || got[0].State != state {
			t.Fatalf("%s: %+v", state, got)
		}
	}
}

func TestComponentsNoMetricsIsNil(t *testing.T) {
	run := cont("web", "w", "running", 1)
	stopped := cont("db", "d", "exited", 2)
	covered := cont("cache", "c", "running", 3)
	use := map[string]Usage{
		id64(3): {CPUPercent: 1.5, MemBytes: 100},
		id64(2): {CPUPercent: 9, MemBytes: 9}, // a stopped container never shows metrics
	}
	got := Components([]Container{run, stopped, covered}, "shop", []string{"web", "db", "cache"}, nil, use)
	by := map[string]Component{}
	for _, c := range got {
		by[c.Service] = c
	}
	if w := by["web"]; w.CPUPercent != nil || w.MemBytes != nil {
		t.Fatalf("running, not covered by the read: %+v", w)
	}
	if d := by["db"]; d.CPUPercent != nil || d.MemBytes != nil {
		t.Fatalf("stopped: %+v", d)
	}
	c := by["cache"]
	if c.CPUPercent == nil || *c.CPUPercent != 1.5 || c.MemBytes == nil || *c.MemBytes != 100 {
		t.Fatalf("covered: %+v", c)
	}
}

func TestComponentsZeroStartedAtIsNil(t *testing.T) {
	in := map[string]Inspect{id64(1): {}}
	got := Components([]Container{cont("web", "w", "created", 1)}, "shop", []string{"web"}, in, nil)
	if got[0].StartedAt != nil {
		t.Fatalf("StartedAt = %v, want nil for the zero time", got[0].StartedAt)
	}
}

func TestTotalsUpSinceEarliestRunning(t *testing.T) {
	early := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	late := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	earlier := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	f := func(v float64) *float64 { return &v }
	m := func(v int64) *int64 { return &v }
	comps := []Component{
		{Status: "Running", CPUPercent: f(1.5), MemBytes: m(100), StartedAt: &late},
		{Status: "Running", CPUPercent: f(2.0), StartedAt: &early},
		{Status: "Running", MemBytes: m(50)}, // no start time
		{Status: "Exited", CPUPercent: f(99), MemBytes: m(99), StartedAt: &earlier},
		{Status: "Not created"},
	}
	cpu, mem, up := Totals(comps)
	if cpu == nil || *cpu != 3.5 {
		t.Fatalf("cpu = %v", cpu)
	}
	if mem == nil || *mem != 150 {
		t.Fatalf("mem = %v", mem)
	}
	if up == nil || !up.Equal(early) {
		t.Fatalf("upSince = %v, want %v (the earliest running start, not the exited one)", up, early)
	}
}

func TestTotalsNothingRunning(t *testing.T) {
	cpu, mem, up := Totals([]Component{{Status: "Exited"}, {Status: "Not created"}})
	if cpu != nil || mem != nil || up != nil {
		t.Fatalf("got %v %v %v, want all nil", cpu, mem, up)
	}
	cpu, mem, up = Totals(nil)
	if cpu != nil || mem != nil || up != nil {
		t.Fatalf("got %v %v %v, want all nil", cpu, mem, up)
	}
	// Running but with no values: still null, never 0.
	cpu, mem, up = Totals([]Component{{Status: "Running"}})
	if cpu != nil || mem != nil || up != nil {
		t.Fatalf("got %v %v %v, want all nil", cpu, mem, up)
	}
}
