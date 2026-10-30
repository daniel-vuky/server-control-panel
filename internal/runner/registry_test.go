package runner

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestArgvLifecycle(t *testing.T) {
	plain := Values{"dir": "/w/shop", "file": "/w/shop/compose.yaml"}
	over := Values{"dir": "/w/shop", "file": "/w/shop/compose.yaml", "override": "/w/shop/compose.override.yaml"}
	base := []string{"compose", "--project-directory", "/w/shop", "-f", "/w/shop/compose.yaml"}
	withOver := append(append([]string{}, base...), "-f", "/w/shop/compose.override.yaml")
	tail := func(head []string, rest ...string) []string {
		return append(append([]string{}, head...), rest...)
	}
	tests := []struct {
		stack  Stack
		action Action
		values Values
		want   []string
	}{
		{StackCompose, ComposeUp, plain, tail(base, "up", "-d")},
		{StackCompose, ComposeUpOverride, over, tail(withOver, "up", "-d")},
		{StackCompose, ComposeStop, plain, tail(base, "stop")},
		{StackCompose, ComposeStopOverride, over, tail(withOver, "stop")},
		{StackCompose, ComposeRestart, plain, tail(base, "restart")},
		{StackCompose, ComposeRestartOverride, over, tail(withOver, "restart")},
		{StackDocker, DockerPing, nil, []string{"version", "--format", "{{.Server.Version}}"}},
	}
	for _, tt := range tests {
		got, err := argv(tt.stack, tt.action, tt.values)
		if err != nil {
			t.Fatalf("%s: %v", tt.action, err)
		}
		if !equal(got, tt.want) {
			t.Errorf("%s argv\n got %q\nwant %q", tt.action, got, tt.want)
		}
	}
	if string(ComposeUp) != "compose.up" || string(ComposeStopOverride) != "compose.stop.override" ||
		string(ComposeRestartOverride) != "compose.restart.override" || string(DockerPing) != "docker.ping" {
		t.Fatal("action names changed")
	}
}

func TestArgvLifecycleRefusesRelative(t *testing.T) {
	for _, a := range []Action{ComposeUp, ComposeStop, ComposeRestart} {
		_, err := argv(StackCompose, a, Values{"dir": "w/shop", "file": "/w/shop/compose.yaml"})
		if !errors.Is(err, ErrBadValue) {
			t.Errorf("%s relative dir: err = %v, want ErrBadValue", a, err)
		}
	}
	for _, a := range []Action{ComposeUpOverride, ComposeStopOverride, ComposeRestartOverride} {
		_, err := argv(StackCompose, a, Values{"dir": "/w/shop", "file": "/w/shop/compose.yaml", "override": "o.yaml"})
		if !errors.Is(err, ErrBadValue) {
			t.Errorf("%s relative override: err = %v, want ErrBadValue", a, err)
		}
	}
}

func TestArgvLifecycleRefusesExtra(t *testing.T) {
	v := Values{"dir": "/w/shop", "file": "/w/shop/compose.yaml", "override": "/w/shop/compose.override.yaml"}
	for _, a := range []Action{ComposeUp, ComposeStop, ComposeRestart} {
		if _, err := argv(StackCompose, a, v); !errors.Is(err, ErrBadValue) {
			t.Errorf("%s with override: err = %v, want ErrBadValue", a, err)
		}
	}
	if _, err := argv(StackDocker, DockerPing, Values{"dir": "/w"}); !errors.Is(err, ErrBadValue) {
		t.Errorf("docker.ping with a value: err = %v, want ErrBadValue", err)
	}
}

func TestTimeoutFor(t *testing.T) {
	const fallback = 15 * time.Second
	tests := []struct {
		action Action
		want   time.Duration
	}{
		{ComposeUp, 60 * time.Second},
		{ComposeStop, 60 * time.Second},
		{ComposeRestartOverride, 60 * time.Second},
		{DockerPing, 5 * time.Second},
		{DockerContainers, fallback},
		{ComposeConfig, fallback},
	}
	for _, tt := range tests {
		if got := timeoutFor(tt.action, fallback); got != tt.want {
			t.Errorf("timeoutFor(%s) = %v, want %v", tt.action, got, tt.want)
		}
	}
	if LifecycleTimeout != 60*time.Second || PingTimeout != 5*time.Second {
		t.Fatal("timeout constants changed")
	}
}

const (
	id1 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	id2 = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

var (
	plainVals = Values{"dir": "/w/shop", "file": "/w/shop/compose.yaml"}
	overVals  = Values{"dir": "/w/shop", "file": "/w/shop/compose.yaml", "override": "/w/shop/compose.override.yaml"}
	baseArgs  = []string{"compose", "--project-directory", "/w/shop", "-f", "/w/shop/compose.yaml"}
	overArgs  = append(append([]string{}, baseArgs...), "-f", "/w/shop/compose.override.yaml")
)

func with(v Values, kv ...string) Values {
	c := Values{}
	for k, val := range v {
		c[k] = val
	}
	for len(kv) >= 2 {
		c[kv[0]] = kv[1]
		kv = kv[2:]
	}
	return c
}

func cat(head []string, rest ...string) []string {
	return append(append([]string{}, head...), rest...)
}

func TestArgvInspect(t *testing.T) {
	got, err := argv(StackDocker, DockerInspect, Values{"ids": id1 + " " + id2})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"inspect", "--format", "{{.Id}}\t{{.RestartCount}}\t{{json .State}}", id1, id2}
	if !equal(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
}

func TestArgvInspectRejects(t *testing.T) {
	many := func(n int) string {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = id1
		}
		return strings.Join(ids, " ")
	}
	if _, err := argv(StackDocker, DockerInspect, Values{"ids": many(1000)}); err != nil {
		t.Fatalf("1,000 ids: %v", err)
	}
	tests := map[string]string{
		"no ids":         "",
		"1,001 ids":      many(1001),
		"uppercase hex":  strings.ToUpper(id1),
		"63 chars":       id1[:63],
		"65 chars":       id1 + "a",
		"a flag":         "-x",
		"a flag last":    id1 + " -x",
		"double space":   id1 + "  " + id2,
		"trailing space": id1 + " ",
		"newline":        id1 + "\n" + id2,
	}
	for name, ids := range tests {
		if _, err := argv(StackDocker, DockerInspect, Values{"ids": ids}); !errors.Is(err, ErrBadValue) {
			t.Errorf("%s: err = %v, want ErrBadValue", name, err)
		}
	}
	if _, err := argv(StackDocker, DockerInspect, nil); !errors.Is(err, ErrBadValue) {
		t.Errorf("missing ids: err = %v, want ErrBadValue", err)
	}
}

func TestArgvStats(t *testing.T) {
	got, err := argv(StackDocker, DockerStats, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"stats", "--no-stream", "--no-trunc", "--format", "{{.ID}}\t{{.CPUPerc}}\t{{.MemUsage}}"}
	if !equal(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
}

func TestArgvDown(t *testing.T) {
	for _, tt := range []struct {
		action Action
		values Values
		want   []string
	}{
		{ComposeDown, plainVals, cat(baseArgs, "down")},
		{ComposeDownOverride, overVals, cat(overArgs, "down")},
	} {
		got, err := argv(StackCompose, tt.action, tt.values)
		if err != nil {
			t.Fatalf("%s: %v", tt.action, err)
		}
		if !equal(got, tt.want) {
			t.Errorf("%s argv\n got %q\nwant %q", tt.action, got, tt.want)
		}
	}
	// Down never removes volumes or images.
	for k, tpl := range registry {
		for _, el := range tpl {
			if el.literal == "-v" || el.literal == "--volumes" || strings.HasPrefix(el.literal, "--rmi") {
				t.Errorf("%s has %q", k.action, el.literal)
			}
		}
	}
}

func TestArgvLogsFollow(t *testing.T) {
	const since = "2026-10-30T10:00:00Z"
	follow := []string{"logs", "--no-color", "--timestamps", "--follow", "--since", since}
	for _, tt := range []struct {
		action Action
		values Values
		want   []string
	}{
		{ComposeLogs, with(plainVals, "since", since), cat(baseArgs, follow...)},
		{ComposeLogsOverride, with(overVals, "since", since), cat(overArgs, follow...)},
		{ComposeLogsService, with(plainVals, "since", since, "service", "web"), cat(baseArgs, append(follow, "web")...)},
		{ComposeLogsServiceOverride, with(overVals, "since", since, "service", "web"), cat(overArgs, append(follow, "web")...)},
	} {
		got, err := argv(StackCompose, tt.action, tt.values)
		if err != nil {
			t.Fatalf("%s: %v", tt.action, err)
		}
		if !equal(got, tt.want) {
			t.Errorf("%s argv\n got %q\nwant %q", tt.action, got, tt.want)
		}
	}
}

func TestArgvLogsTail(t *testing.T) {
	tail := []string{"logs", "--no-color", "--timestamps", "--tail", "500"}
	for _, tt := range []struct {
		action Action
		values Values
		want   []string
	}{
		{ComposeLogsTail, plainVals, cat(baseArgs, tail...)},
		{ComposeLogsTailOverride, overVals, cat(overArgs, tail...)},
		{ComposeLogsTailService, with(plainVals, "service", "web"), cat(baseArgs, append(tail, "web")...)},
		{ComposeLogsTailServiceOverride, with(overVals, "service", "db-1.x_y"), cat(overArgs, append(tail, "db-1.x_y")...)},
	} {
		got, err := argv(StackCompose, tt.action, tt.values)
		if err != nil {
			t.Fatalf("%s: %v", tt.action, err)
		}
		if !equal(got, tt.want) {
			t.Errorf("%s argv\n got %q\nwant %q", tt.action, got, tt.want)
		}
	}
}

func TestServiceSlotRejects(t *testing.T) {
	for name, svc := range map[string]string{
		"flag":       "-f",
		"space":      "a b",
		"empty":      "",
		"64 chars":   strings.Repeat("a", 64),
		"dot dot":    "../x",
		"leading _":  "_a",
		"leading .":  ".a",
		"slash":      "a/b",
		"NUL":        "a\x00b",
		"unicode":    "é",
		"newline":    "a\nb",
		"equals":     "a=b",
		"leading --": "--follow",
	} {
		if _, err := argv(StackCompose, ComposeLogsTailService, with(plainVals, "service", svc)); !errors.Is(err, ErrBadValue) {
			t.Errorf("%s: err = %v, want ErrBadValue", name, err)
		}
	}
	if _, err := argv(StackCompose, ComposeLogsTailService, with(plainVals, "service", strings.Repeat("a", 63))); err != nil {
		t.Errorf("63 chars: %v", err)
	}
	if _, err := argv(StackCompose, ComposeLogsTailService, plainVals); !errors.Is(err, ErrBadValue) {
		t.Errorf("missing service: err = %v, want ErrBadValue", err)
	}
}

func TestSinceSlotRejects(t *testing.T) {
	for name, since := range map[string]string{
		"offset":     "2026-10-30T10:00:00+07:00",
		"yesterday":  "yesterday",
		"negative":   "-1",
		"empty":      "",
		"no zone":    "2026-10-30T10:00:00",
		"lower z":    "2026-10-30T10:00:00z",
		"bad month":  "2026-13-30T10:00:00Z",
		"date only":  "2026-10-30",
		"flag":       "--follow",
		"trailing":   "2026-10-30T10:00:00Z ",
		"fraction x": "2026-10-30T10:00:00.Z",
	} {
		if _, err := argv(StackCompose, ComposeLogs, with(plainVals, "since", since)); !errors.Is(err, ErrBadValue) {
			t.Errorf("%s: err = %v, want ErrBadValue", name, err)
		}
	}
	for _, since := range []string{"2026-10-30T10:00:00Z", "2026-10-30T10:00:00.123456789Z", "2026-10-30T10:00:00.5Z"} {
		if _, err := argv(StackCompose, ComposeLogs, with(plainVals, "since", since)); err != nil {
			t.Errorf("%s: %v", since, err)
		}
	}
	if _, err := argv(StackCompose, ComposeLogs, plainVals); !errors.Is(err, ErrBadValue) {
		t.Errorf("missing since: err = %v, want ErrBadValue", err)
	}
}

func TestTimeoutForDown(t *testing.T) {
	for _, a := range []Action{ComposeDown, ComposeDownOverride} {
		if got := timeoutFor(a, 15*time.Second); got != LifecycleTimeout {
			t.Errorf("timeoutFor(%s) = %v", a, got)
		}
	}
	for _, a := range []Action{DockerInspect, DockerStats, ComposeLogsTail, ComposeLogsTailServiceOverride} {
		if got := timeoutFor(a, 15*time.Second); got != 15*time.Second {
			t.Errorf("timeoutFor(%s) = %v, want the read timeout", a, got)
		}
	}
}
