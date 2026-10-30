package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const hostile = "/w/my app; rm -rf $(id) && x|y"

// newRunner returns a Runner that runs the fake docker script, with a fresh
// DataDir. knobs, when not empty, is written to $HOME/fake.env for the script.
func newRunner(t *testing.T, knobs string) *Runner {
	t.Helper()
	bin, err := filepath.Abs("testdata/fake-docker")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{Bin: bin, DataDir: t.TempDir(), Timeout: 10 * time.Second}
	if knobs != "" {
		home := filepath.Join(r.DataDir, "docker")
		if err := os.MkdirAll(home, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "fake.env"), []byte(knobs), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// lines returns the text after prefix on every line of out that has it.
func lines(out []byte, prefix string) []string {
	var got []string
	for _, l := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(l, prefix); ok {
			got = append(got, rest)
		}
	}
	return got
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestArgvFromTemplate(t *testing.T) {
	r := newRunner(t, "")
	file := hostile + "/compose.yaml"
	res, err := r.Run(context.Background(), StackCompose, ComposeConfig,
		Values{"dir": hostile, "file": file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"compose", "--project-directory", hostile, "-f", file,
		"config", "--format", "json", "--no-env-resolution"}
	if got := lines(res.Stdout, "ARG:"); !equal(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
}

func TestOverrideArgv(t *testing.T) {
	r := newRunner(t, "")
	res, err := r.Run(context.Background(), StackCompose, ComposeConfigOverride,
		Values{"dir": "/w/a", "file": "/w/a/compose.yaml", "override": "/w/a/compose.override.yaml"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"compose", "--project-directory", "/w/a",
		"-f", "/w/a/compose.yaml", "-f", "/w/a/compose.override.yaml",
		"config", "--format", "json", "--no-env-resolution"}
	if got := lines(res.Stdout, "ARG:"); !equal(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
}

func TestContainersFormatIsOneArg(t *testing.T) {
	r := newRunner(t, "")
	res, err := r.Run(context.Background(), StackDocker, DockerContainers, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	args := lines(res.Stdout, "ARG:")
	want := []string{"ps", "--all", "--no-trunc", "--filter", "label=com.docker.compose.project", "--format"}
	if len(args) != len(want)+1 || !equal(args[:len(want)], want) {
		t.Fatalf("argv %q", args)
	}
	format := args[len(want)]
	if n := strings.Count(format, "\t"); n != 5 {
		t.Fatalf("format has %d tabs, want 5: %q", n, format)
	}
	if n := len(strings.Split(ContainerColumns, ",")); n != 6 {
		t.Fatalf("ContainerColumns has %d columns", n)
	}
	if !strings.HasPrefix(format, `{{.Label "com.docker.compose.project"}}`+"\t") ||
		!strings.HasSuffix(format, `{{.Label "com.docker.compose.oneoff"}}`) ||
		!strings.Contains(format, `{{.Label "io.github.daniel-vuky.server-control-panel.image"}}`) {
		t.Fatalf("format = %q", format)
	}
}

func TestRefusesBeforeRunning(t *testing.T) {
	ok := Values{"dir": "/w/a", "file": "/w/a/compose.yaml"}
	with := func(k, v string) Values {
		c := Values{}
		for key, val := range ok {
			c[key] = val
		}
		c[k] = v
		return c
	}
	tests := []struct {
		name   string
		stack  Stack
		action Action
		values Values
		want   error
	}{
		{"unknown action", StackCompose, "compose.nope", ok, ErrUnknownCommand},
		{"unknown stack", "swarm", ComposeConfig, ok, ErrUnknownCommand},
		{"action under the wrong stack", StackDocker, ComposeConfig, ok, ErrUnknownCommand},
		{"missing file", StackCompose, ComposeConfig, Values{"dir": "/w/a"}, ErrBadValue},
		{"extra value", StackCompose, ComposeConfig, with("x", "/w/a"), ErrBadValue},
		{"extra value on a template without placeholders", StackDocker, DockerContainers, Values{"dir": "/w/a"}, ErrBadValue},
		{"relative path", StackCompose, ComposeConfig, with("dir", "w/a"), ErrBadValue},
		{"flag-looking value", StackCompose, ComposeConfig, with("dir", "--privileged"), ErrBadValue},
		{"path not clean", StackCompose, ComposeConfig, with("dir", "/w/../a"), ErrBadValue},
		{"trailing slash", StackCompose, ComposeConfig, with("dir", "/w/a/"), ErrBadValue},
		{"NUL byte", StackCompose, ComposeConfig, with("file", "/w/a/compose.yaml\x00--x"), ErrBadValue},
		{"empty value", StackCompose, ComposeConfig, with("dir", ""), ErrBadValue},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRunner(t, "")
			_, err := r.Run(context.Background(), tc.stack, tc.action, tc.values, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if _, statErr := os.Stat(filepath.Join(r.DataDir, "docker", "started")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("the fake docker was started (stat: %v)", statErr)
			}
		})
	}
}

func TestChildEnvIsMinimal(t *testing.T) {
	t.Setenv("CF_ACCESS_AUD", "abc")
	t.Setenv("DOCKER_HOST", "")
	r := newRunner(t, "")
	res, err := r.Run(context.Background(), StackDocker, DockerContainers, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	env := lines(res.Stdout, "ENV:")
	home := filepath.Join(r.DataDir, "docker")
	has := func(s string) bool {
		for _, e := range env {
			if e == s {
				return true
			}
		}
		return false
	}
	for _, e := range env {
		if strings.Contains(e, "CF_ACCESS") {
			t.Fatalf("panel environment leaked: %q", e)
		}
		if strings.HasPrefix(e, "DOCKER_HOST=") {
			t.Fatalf("DOCKER_HOST set although the panel has none: %q", e)
		}
	}
	if !has("HOME="+home) || !has("DOCKER_CONFIG="+home) {
		t.Fatalf("HOME and DOCKER_CONFIG must be %s, env = %q", home, env)
	}
	fi, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o, want 700", fi.Mode().Perm())
	}
}

func TestChildEnvKeepsDockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	r := newRunner(t, "")
	res, err := r.Run(context.Background(), StackDocker, DockerContainers, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range lines(res.Stdout, "ENV:") {
		if e == "DOCKER_HOST=unix:///var/run/docker.sock" {
			found = true
		}
	}
	if !found {
		t.Fatal("DOCKER_HOST was not passed on")
	}
}

func TestTimeout(t *testing.T) {
	r := newRunner(t, "FAKE_SLEEP=5\n")
	r.Timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := r.Run(context.Background(), StackDocker, DockerContainers, nil, nil)
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want *TimeoutError", err)
	}
	if te.After != 200*time.Millisecond {
		t.Fatalf("After = %v", te.After)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestCallerCancelIsNotATimeout(t *testing.T) {
	r := newRunner(t, "FAKE_SLEEP=5\n")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := r.Run(ctx, StackDocker, DockerContainers, nil, nil)
	var te *TimeoutError
	if err == nil || errors.As(err, &te) {
		t.Fatalf("err = %v, want the caller's context error", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestOutputCapped(t *testing.T) {
	r := newRunner(t, "FAKE_BYTES=5000000\n")
	res, err := r.Run(context.Background(), StackDocker, DockerContainers, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) != 4<<20 {
		t.Fatalf("len(Stdout) = %d, want %d", len(res.Stdout), 4<<20)
	}
	if !res.Truncated {
		t.Fatal("Truncated is false")
	}
}

func TestSmallOutputNotTruncated(t *testing.T) {
	r := newRunner(t, "")
	res, err := r.Run(context.Background(), StackDocker, DockerContainers, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Truncated || res.ExitCode != 0 || res.Duration <= 0 {
		t.Fatalf("result = truncated %v, exit %d, duration %v", res.Truncated, res.ExitCode, res.Duration)
	}
}

func TestStderrMasked(t *testing.T) {
	r := newRunner(t, "FAKE_STDERR='bad value hunter22 for KEY'\nFAKE_EXIT=1\n")
	res, err := r.Run(context.Background(), StackDocker, DockerContainers, nil, []string{"hunter22", "ab"})
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want *ExitError", err)
	}
	if ee.Code != 1 || ee.Stderr != "bad value •••• for KEY" {
		t.Fatalf("ExitError = %+v", ee)
	}
	if ee.Error() != "bad value •••• for KEY" {
		t.Fatalf("Error() = %q", ee.Error())
	}
	if strings.Contains(string(res.Stderr), "hunter22") || res.ExitCode != 1 {
		t.Fatalf("Result = exit %d, stderr %q", res.ExitCode, res.Stderr)
	}
}

func TestExitErrorWithoutStderr(t *testing.T) {
	r := newRunner(t, "FAKE_EXIT=3\n")
	_, err := r.Run(context.Background(), StackDocker, DockerContainers, nil, nil)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 || ee.Error() != "exit status 3" {
		t.Fatalf("err = %v", err)
	}
}

func TestStdoutNotMasked(t *testing.T) {
	r := newRunner(t, "")
	res, err := r.Run(context.Background(), StackCompose, ComposeConfig,
		Values{"dir": "/w/hunter22", "file": "/w/hunter22/compose.yaml"}, []string{"hunter22"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Stdout), "ARG:/w/hunter22\n") {
		t.Fatalf("stdout was masked: %q", res.Stdout)
	}
}

func TestMask(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		secrets []string
		want    string
	}{
		{"no secrets", "a b", nil, "a b"},
		{"every occurrence", "x pass1234 y pass1234", []string{"pass1234"}, "x •••• y ••••"},
		{"short secrets are ignored", "ab abc", []string{"ab", "abc", ""}, "ab abc"},
		{"four bytes counts", "abcd", []string{"abcd"}, "••••"},
		{"longer secret first", "pw=hunter22", []string{"hunter", "hunter22"}, "pw=••••"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Mask(tc.in, tc.secrets); got != tc.want {
				t.Fatalf("Mask = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMissingBinary(t *testing.T) {
	r := &Runner{Bin: filepath.Join(t.TempDir(), "nope"), DataDir: t.TempDir()}
	_, err := r.Run(context.Background(), StackDocker, DockerContainers, nil, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	var te *TimeoutError
	var ee *ExitError
	if errors.As(err, &te) || errors.As(err, &ee) {
		t.Fatalf("a start failure is neither a timeout nor an exit error: %v", err)
	}
}

func TestRunKillsProcessGroupOnTimeout(t *testing.T) {
	old := timeoutFor
	timeoutFor = func(Action, time.Duration) time.Duration { return 200 * time.Millisecond }
	t.Cleanup(func() { timeoutFor = old })

	r := newRunner(t, "FAKE_GRANDCHILD=1\n")
	_, err := r.Run(context.Background(), StackCompose, ComposeStop,
		Values{"dir": "/w/a", "file": "/w/a/compose.yaml"}, nil)
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want *TimeoutError", err)
	}
	if te.After != 200*time.Millisecond {
		t.Fatalf("After = %v", te.After)
	}

	raw, err := os.ReadFile(filepath.Join(r.DataDir, "docker", "grandchild"))
	if err != nil {
		t.Fatalf("grandchild pid not recorded: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
			t.Fatalf("grandchild %d is still alive after the timeout", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// alive reports whether the process pid still runs. A killed process nobody
// has reaped yet (a zombie, as when the test's PID 1 does not wait for
// orphans) counts as dead.
func alive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil || proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	// The state is the field after the ")" that closes the command name.
	if i := strings.LastIndexByte(string(stat), ')'); i >= 0 {
		if fields := strings.Fields(string(stat[i+1:])); len(fields) > 0 && fields[0] == "Z" {
			return false
		}
	}
	return true
}
