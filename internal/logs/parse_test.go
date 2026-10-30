package logs

import (
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

var shopContainers = []project.Container{
	{Project: "shop", Service: "web", State: "running", Name: "shop-web-1"},
	{Project: "shop", Service: "db", State: "running", Name: "shop-db-1"},
	{Project: "shop", Service: "b", State: "running", Name: "custom-b-name"},
	{Project: "other", Service: "web", State: "running", Name: "web-1"},
}

func TestParseLineStripsANSIAndPrefix(t *testing.T) {
	want := time.Date(2026, 10, 30, 10, 0, 0, 123456789, time.UTC)
	for _, raw := range []string{
		"web-1 | 2026-10-30T10:00:00.123456789Z hello",
		"web-1    | 2026-10-30T10:00:00.123456789Z hello",
		"\x1b[36mweb-1    |\x1b[0m 2026-10-30T10:00:00.123456789Z hello   ",
		"web-1  | 2026-10-30T10:00:00.123456789Z \x1b[1mhello\x1b[0m",
	} {
		l, ok := ParseLine(raw, "shop", shopContainers)
		if !ok {
			t.Fatalf("%q: not parsed", raw)
		}
		if l.Container != "shop-web-1" || l.Service != "web" || !l.Time.Equal(want) || l.Text != "hello" {
			t.Fatalf("%q: got %+v", raw, l)
		}
	}
}

func TestParseLineEmptyText(t *testing.T) {
	l, ok := ParseLine("db-1  | 2026-10-30T10:00:00Z ", "shop", shopContainers)
	if !ok || l.Container != "shop-db-1" || l.Service != "db" || l.Text != "" {
		t.Fatalf("got %+v, %v", l, ok)
	}
	if l.Time.Location() != time.UTC {
		t.Fatalf("time not UTC: %v", l.Time)
	}
}

func TestParseLineContainerName(t *testing.T) {
	l, ok := ParseLine("custom-b-name  | 2026-10-30T10:00:01.5Z started", "shop", shopContainers)
	if !ok || l.Container != "custom-b-name" || l.Service != "b" || l.Text != "started" {
		t.Fatalf("got %+v, %v", l, ok)
	}
}

func TestParseLineUnmappedPrefixKept(t *testing.T) {
	l, ok := ParseLine("worker-3 | 2026-10-30T10:00:01Z x", "shop", shopContainers)
	if !ok || l.Container != "worker-3" || l.Service != "" || l.Text != "x" {
		t.Fatalf("got %+v, %v", l, ok)
	}
}

// A container of another project whose name equals the prefix is not taken:
// the line came from this project's web-1.
func TestParseLineIgnoresOtherProjects(t *testing.T) {
	l, ok := ParseLine("web-1 | 2026-10-30T10:00:01Z x", "shop", shopContainers)
	if !ok || l.Container != "shop-web-1" {
		t.Fatalf("got %+v, %v", l, ok)
	}
}

func TestParseLineDropsStatusAndWarnings(t *testing.T) {
	for _, raw := range []string{
		"\x1b[Kweb-1 exited with code 137",
		`time="2026-10-30T10:00:00Z" level=warning msg="x"`,
		"",
		"web-1 | not a timestamp",
		"web-1 | 2026-13-45T99:00:00Z bad date",
		"| 2026-10-30T10:00:00Z no prefix",
		"Attaching to web-1, db-1",
	} {
		if l, ok := ParseLine(raw, "shop", shopContainers); ok {
			t.Fatalf("%q: parsed as %+v", raw, l)
		}
	}
}
