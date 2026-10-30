package runner

import (
	"errors"
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
