package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
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
	if n := strings.Count(format, "\t"); n != 9 {
		t.Fatalf("format has %d tabs, want 9: %q", n, format)
	}
	if n := len(strings.Split(ContainerColumns, ",")); n != 10 {
		t.Fatalf("ContainerColumns has %d columns", n)
	}
	if !strings.HasPrefix(format, `{{.Label "com.docker.compose.project"}}`+"\t") ||
		!strings.HasSuffix(format, "\t{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Ports}}") ||
		!strings.Contains(format, `{{.Label "com.docker.compose.oneoff"}}`+"\t{{.ID}}") ||
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

var followVals = Values{"dir": "/w/a", "file": "/w/a/compose.yaml", "since": "2026-10-30T10:00:00Z"}

// collect returns an onLine that appends to a slice, and a getter. It is
// not guarded, so a concurrent call shows up as a race under -race.
func collect() (func(string), func() []string) {
	var got []string
	return func(l string) { got = append(got, l) }, func() []string { return got }
}

func TestStreamDeliversMaskedLines(t *testing.T) {
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_STDOUT='a secret b'\nFAKE_STDERR='warn secret'\n")
	onLine, got := collect()
	err := r.Stream(context.Background(), StackCompose, ComposeLogs, followVals, []string{"secret"}, onLine)
	if err != nil {
		t.Fatal(err)
	}
	lines := got()
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	have := map[string]bool{lines[0]: true, lines[1]: true}
	if !have["a •••• b"] || !have["warn ••••"] {
		t.Fatalf("lines = %q", lines)
	}
}

func TestStreamArgv(t *testing.T) {
	r := newRunner(t, "")
	onLine, got := collect()
	if err := r.Stream(context.Background(), StackCompose, ComposeLogs, followVals, nil, onLine); err != nil {
		t.Fatal(err)
	}
	want := []string{"compose", "--project-directory", "/w/a", "-f", "/w/a/compose.yaml",
		"logs", "--no-color", "--timestamps", "--follow", "--since", "2026-10-30T10:00:00Z"}
	var args []string
	for _, l := range got() {
		if rest, ok := strings.CutPrefix(l, "ARG:"); ok {
			args = append(args, rest)
		}
	}
	if !equal(args, want) {
		t.Fatalf("argv\n got %q\nwant %q", args, want)
	}
}

func TestStreamCutsLongLine(t *testing.T) {
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_LONGLINE=20480\n")
	onLine, got := collect()
	if err := r.Stream(context.Background(), StackCompose, ComposeLogs, followVals, nil, onLine); err != nil {
		t.Fatal(err)
	}
	lines := got()
	if len(lines) != 2 || len(lines[0]) != 16<<10 || lines[0] != strings.Repeat("x", 16<<10) || lines[1] != "after" {
		var sizes []int
		for _, l := range lines {
			sizes = append(sizes, len(l))
		}
		t.Fatalf("line sizes = %v, last = %q", sizes, lines[len(lines)-1])
	}
}

func TestStreamExitError(t *testing.T) {
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_STDERR='no such service secret'\nFAKE_EXIT=1\n")
	onLine, _ := collect()
	err := r.Stream(context.Background(), StackCompose, ComposeLogs, followVals, []string{"secret"}, onLine)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 1 || ee.Stderr != "no such service ••••" {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamKillsGroupOnCancel(t *testing.T) {
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_GRANDCHILD=1\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	onLine, _ := collect()
	done := make(chan error, 1)
	go func() {
		done <- r.Stream(ctx, StackCompose, ComposeLogs, followVals, nil, onLine)
	}()

	pidFile := filepath.Join(r.DataDir, "docker", "grandchild")
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for pid == 0 {
		if raw, err := os.ReadFile(pidFile); err == nil { //nolint:gosec // G304: a path under the test's temp dir.
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
		if pid == 0 && time.Now().After(deadline) {
			t.Fatal("grandchild pid not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stream did not return after cancel")
	}
	deadline = time.Now().Add(3 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
			t.Fatalf("grandchild %d is still alive after cancel", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStreamRefusesReadAction(t *testing.T) {
	r := newRunner(t, "")
	onLine, got := collect()
	for _, tc := range []struct {
		stack  Stack
		action Action
		values Values
	}{
		{StackCompose, ComposeLogsTail, Values{"dir": "/w/a", "file": "/w/a/compose.yaml"}},
		{StackCompose, ComposeUp, Values{"dir": "/w/a", "file": "/w/a/compose.yaml"}},
		{StackDocker, DockerContainers, nil},
	} {
		err := r.Stream(context.Background(), tc.stack, tc.action, tc.values, nil, onLine)
		if !errors.Is(err, ErrUnknownCommand) {
			t.Errorf("%s: err = %v, want ErrUnknownCommand", tc.action, err)
		}
	}
	if len(got()) != 0 {
		t.Fatalf("lines = %q", got())
	}
	if _, err := os.Stat(filepath.Join(r.DataDir, "docker", "started")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the fake docker was started (stat: %v)", err)
	}
}

func TestRunRefusesFollowAction(t *testing.T) {
	for _, a := range []Action{ComposeLogs, ComposeLogsOverride, ComposeLogsService, ComposeLogsServiceOverride} {
		r := newRunner(t, "")
		_, err := r.Run(context.Background(), StackCompose, a, followVals, nil)
		if !errors.Is(err, ErrUnknownCommand) {
			t.Errorf("%s: err = %v, want ErrUnknownCommand", a, err)
		}
		if _, err := os.Stat(filepath.Join(r.DataDir, "docker", "started")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: the fake docker was started (stat: %v)", a, err)
		}
	}
}

func TestDownUsesLifecycleTimeout(t *testing.T) {
	var asked time.Duration
	old := timeoutFor
	timeoutFor = func(a Action, fallback time.Duration) time.Duration {
		d := old(a, fallback)
		asked = d
		return 200 * time.Millisecond
	}
	t.Cleanup(func() { timeoutFor = old })

	r := newRunner(t, "FAKE_SLEEP=5\n")
	_, err := r.Run(context.Background(), StackCompose, ComposeDown,
		Values{"dir": "/w/a", "file": "/w/a/compose.yaml"}, nil)
	var te *TimeoutError
	if !errors.As(err, &te) || te.After != 200*time.Millisecond {
		t.Fatalf("err = %v, want *TimeoutError", err)
	}
	if asked != LifecycleTimeout {
		t.Fatalf("timeoutFor gave %v for compose.down, want %v", asked, LifecycleTimeout)
	}
}

func TestRunTailAndInspectRun(t *testing.T) {
	r := newRunner(t, "")
	res, err := r.Run(context.Background(), StackDocker, DockerInspect, Values{"ids": id1 + " " + id2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	args := lines(res.Stdout, "ARG:")
	if len(args) != 5 || args[3] != id1 || args[4] != id2 {
		t.Fatalf("argv = %q", args)
	}
}

type emitted struct {
	line string
	cut  bool
}

// writeLines feeds chunks to a lineWriter, flushes it, and returns what it
// emitted both raw and as Stream delivers it (finishLine with secrets).
func writeLines(secrets []string, chunks ...string) (raw []emitted, final []string) {
	w := &lineWriter{emit: func(line string, cut bool) {
		raw = append(raw, emitted{line, cut})
		final = append(final, finishLine(line, cut, secrets))
	}}
	for _, c := range chunks {
		_, _ = w.Write([]byte(c))
	}
	w.flush()
	return raw, final
}

func TestLineWriterCutNeverLeaksSecretPrefix(t *testing.T) {
	const secret = "hunter22xyz"
	line := strings.Repeat("x", maxLine-5) + secret + " tail"
	_, final := writeLines([]string{secret}, line+"\nnext\n")
	want := strings.Repeat("x", maxLine-5)
	if len(final) != 2 || final[0] != want || final[1] != "next" {
		t.Fatalf("final = %d lines, first has %d bytes, ends %q", len(final), len(final[0]), final[0][len(final[0])-6:])
	}
	// A secret that ends exactly at the cut is masked whole.
	_, final = writeLines([]string{secret}, strings.Repeat("x", maxLine-len(secret))+secret+"more\n")
	if want := strings.Repeat("x", maxLine-len(secret)) + "••••"; final[0] != want {
		t.Fatalf("final = %q", final[0][len(final[0])-12:])
	}
	// A prefix is also trimmed when the secret was split in several pieces.
	_, final = writeLines([]string{"ab-secret-1", "secret-2"}, strings.Repeat("x", maxLine-2)+"se"+"cret-2 and more\n")
	if final[0] != strings.Repeat("x", maxLine-2) {
		t.Fatalf("final tail = %q", final[0][len(final[0])-4:])
	}
}

func TestLineWriterCutNeverSplitsARune(t *testing.T) {
	for name, tc := range map[string]struct{ rune, want string }{
		"2-byte rune": {"é", strings.Repeat("x", maxLine-1)},
		"3-byte rune": {"€", strings.Repeat("x", maxLine-1)},
		"4-byte rune": {"😀", strings.Repeat("x", maxLine-1)},
	} {
		_, final := writeLines(nil, strings.Repeat("x", maxLine-1)+tc.rune+"tail\n")
		if !utf8.ValidString(final[0]) || final[0] != tc.want {
			t.Errorf("%s: len %d, valid %v", name, len(final[0]), utf8.ValidString(final[0]))
		}
	}
	// A rune that ends exactly at the cut is kept.
	_, final := writeLines(nil, strings.Repeat("x", maxLine-2)+"é"+"tail\n")
	if final[0] != strings.Repeat("x", maxLine-2)+"é" {
		t.Errorf("complete rune at the cut was trimmed: len %d", len(final[0]))
	}
}

func TestLineWriterByteAtATime(t *testing.T) {
	in := strings.Repeat("y", maxLine+100) + "\nok\n"
	chunks := make([]string, len(in))
	for i := range in {
		chunks[i] = in[i : i+1]
	}
	raw, _ := writeLines(nil, chunks...)
	if len(raw) != 2 || raw[0] != (emitted{strings.Repeat("y", maxLine), true}) || raw[1] != (emitted{"ok", false}) {
		t.Fatalf("raw = %d lines, first %d bytes cut=%v", len(raw), len(raw[0].line), raw[0].cut)
	}
	// A line of exactly maxLine bytes is not cut.
	raw, _ = writeLines(nil, strings.Repeat("y", maxLine)+"\n")
	if len(raw) != 1 || raw[0].cut || len(raw[0].line) != maxLine {
		t.Fatalf("exact line: %d lines, cut=%v", len(raw), raw[0].cut)
	}
}

func TestLineWriterLineEnds(t *testing.T) {
	for name, tc := range map[string]struct {
		chunks []string
		want   []string
	}{
		"CRLF":                  {[]string{"a\r\n"}, []string{"a"}},
		"CRLF split":            {[]string{"a\r", "\n"}, []string{"a"}},
		"blank lines":           {[]string{"\n\n"}, []string{"", ""}},
		"trailing text":         {[]string{"a\nb"}, []string{"a", "b"}},
		"trailing text chunked": {[]string{"a\nb", "c"}, []string{"a", "bc"}},
		"nothing":               {nil, nil},
		"ends with newline":     {[]string{"a\n"}, []string{"a"}},
	} {
		raw, _ := writeLines(nil, tc.chunks...)
		var got []string
		for _, e := range raw {
			got = append(got, e.line)
			if e.cut {
				t.Errorf("%s: unexpected cut", name)
			}
		}
		if !equal(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// dumpValues fills every slot of DBPgDump.
var dumpValues = Values{"dir": "/w/a", "file": "/w/a/compose.yaml", "service": "db", "dbname": "shop"}

// setDumpLimits lowers the stall and ceiling of RunTo for one test.
func setDumpLimits(t *testing.T, stall, ceiling time.Duration) {
	t.Helper()
	oldStall, oldCeiling := dumpStall, dumpCeiling
	dumpStall, dumpCeiling = stall, ceiling
	t.Cleanup(func() { dumpStall, dumpCeiling = oldStall, oldCeiling })
}

// countWriter counts the bytes written to it and checks that they are all x.
type countWriter struct {
	n     int
	other bool
}

func (w *countWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b != 'x' {
			w.other = true
		}
	}
	w.n += len(p)
	return len(p), nil
}

func TestRunToArgv(t *testing.T) {
	r := newRunner(t, "")
	var out strings.Builder
	if _, err := r.RunTo(context.Background(), StackCompose, DBPgDump, dumpValues, nil, &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"compose", "--project-directory", "/w/a", "-f", "/w/a/compose.yaml",
		"exec", "-T", "db", "sh", "-c", pgDumpScript, "sh", "shop"}
	// The script spans lines; join the ARG: lines back at the script.
	got := strings.Split(strings.SplitN(out.String(), "\nENV:", 2)[0], "\nARG:")
	got[0] = strings.TrimPrefix(got[0], "ARG:")
	if !equal(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
}

func TestRunToStreamsMoreThan4MiB(t *testing.T) {
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_BYTES=5000000\n")
	var w countWriter
	stderr, err := r.RunTo(context.Background(), StackCompose, DBPgDump, dumpValues, nil, &w)
	if err != nil {
		t.Fatal(err)
	}
	if w.n != 5000000 || w.other || len(stderr) != 0 {
		t.Fatalf("wrote %d bytes (other bytes %v), stderr %q", w.n, w.other, stderr)
	}
}

func TestRunToMasksStderr(t *testing.T) {
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_STDERR='mysqldump: Got error: hunter22 denied'\nFAKE_EXIT=2\n")
	var out strings.Builder
	stderr, err := r.RunTo(context.Background(), StackCompose, DBMySQLDump, dumpValues, []string{"hunter22"}, &out)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 2 || ee.Stderr != "mysqldump: Got error: •••• denied" {
		t.Fatalf("err = %#v", err)
	}
	if string(stderr) != "mysqldump: Got error: •••• denied\n" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRunToStderrCapped(t *testing.T) {
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_STDERR=\"$(head -c 5000000 /dev/zero | tr '\\0' e)\"\n")
	stderr, err := r.RunTo(context.Background(), StackCompose, DBPgDump, dumpValues, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(stderr) != 4<<20 {
		t.Fatalf("len(stderr) = %d, want %d", len(stderr), 4<<20)
	}
}

func TestRunToStall(t *testing.T) {
	setDumpLimits(t, 300*time.Millisecond, time.Hour)
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_STDOUT=first\nFAKE_GRANDCHILD=1\n")
	start := time.Now()
	var out strings.Builder
	_, err := r.RunTo(context.Background(), StackCompose, DBPgDump, dumpValues, nil, &out)
	var se *StallError
	if !errors.As(err, &se) || se.After != 300*time.Millisecond {
		t.Fatalf("err = %v, want *StallError after 300ms", err)
	}
	if se.Error() != "docker command wrote nothing for 300ms" {
		t.Fatalf("Error() = %q", se.Error())
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v", d)
	}
	if out.String() != "first\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	waitGone(t, r)
}

func TestRunToWritingKeepsItAlive(t *testing.T) {
	setDumpLimits(t, 400*time.Millisecond, time.Hour)
	// One line every 100 ms for a second: far longer than the stall, never silent for it.
	r := newRunner(t, "FAKE_QUIET=1\nfor i in 1 2 3 4 5 6 7 8 9 10; do echo x; sleep 0.1; done\n")
	var out strings.Builder
	if _, err := r.RunTo(context.Background(), StackCompose, DBPgDump, dumpValues, nil, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "x\n") != 10 {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunToCeiling(t *testing.T) {
	setDumpLimits(t, time.Hour, 300*time.Millisecond)
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_GRANDCHILD=1\n")
	start := time.Now()
	_, err := r.RunTo(context.Background(), StackCompose, DBMongoDump, dumpValues, nil, io.Discard)
	var te *TimeoutError
	if !errors.As(err, &te) || te.After != 300*time.Millisecond {
		t.Fatalf("err = %v, want *TimeoutError after 300ms", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v", d)
	}
	waitGone(t, r)
}

func TestRunToCallerCancel(t *testing.T) {
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_SLEEP=5\n")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := r.RunTo(ctx, StackCompose, DBPgDump, dumpValues, nil, io.Discard)
	var te *TimeoutError
	var se *StallError
	if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &te) || errors.As(err, &se) {
		t.Fatalf("err = %v, want the caller's context error", err)
	}
}

// errWriter fails every write, like a full disk.
type errWriter struct{}

var errDiskFull = errors.New("no space left on device")

func (errWriter) Write([]byte) (int, error) { return 0, errDiskFull }

func TestRunToWriterErrorStopsTheDump(t *testing.T) {
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_STDOUT=data\nFAKE_SLEEP=5\n")
	start := time.Now()
	_, err := r.RunTo(context.Background(), StackCompose, DBPgDump, dumpValues, nil, errWriter{})
	if !errors.Is(err, errDiskFull) {
		t.Fatalf("err = %v, want the write error", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v; the dump was not stopped", d)
	}
}

func TestRunToRefusesNonDump(t *testing.T) {
	r := newRunner(t, "")
	plain := Values{"dir": "/w/a", "file": "/w/a/compose.yaml"}
	for _, tc := range []struct {
		a Action
		v Values
	}{
		{DBPgSize, dumpValues},
		{DBMySQLSize, dumpValues},
		{DBMongoSize, dumpValues},
		{ComposeUp, plain},
		{ComposeLogs, with(plain, "since", "2026-10-30T10:00:00Z")},
		{DockerContainers, nil},
	} {
		_, err := r.RunTo(context.Background(), StackCompose, tc.a, tc.v, nil, io.Discard)
		if !errors.Is(err, ErrUnknownCommand) {
			t.Errorf("%s: err = %v, want ErrUnknownCommand", tc.a, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r.DataDir, "docker", "started")); !os.IsNotExist(err) {
		t.Fatal("a refused command ran")
	}
}

func TestRunRefusesDump(t *testing.T) {
	r := newRunner(t, "")
	for _, a := range []Action{DBPgDump, DBPgDumpOverride, DBPgDumpDefault, DBPgDumpDefaultOverride,
		DBMySQLDump, DBMySQLDumpOverride, DBMongoDump, DBMongoDumpOverride} {
		if _, err := r.Run(context.Background(), StackCompose, a, dumpValues, nil); !errors.Is(err, ErrUnknownCommand) {
			t.Errorf("Run %s: err = %v, want ErrUnknownCommand", a, err)
		}
		if err := r.Stream(context.Background(), StackCompose, a, dumpValues, nil, func(string) {}); !errors.Is(err, ErrUnknownCommand) {
			t.Errorf("Stream %s: err = %v, want ErrUnknownCommand", a, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r.DataDir, "docker", "started")); !os.IsNotExist(err) {
		t.Fatal("a refused command ran")
	}
}

func TestRunSizeAction(t *testing.T) {
	r := newRunner(t, "FAKE_QUIET=1\nFAKE_STDOUT=7706291\n")
	v := Values{"dir": "/w/a", "file": "/w/a/compose.yaml", "service": "db"}
	res, err := r.Run(context.Background(), StackCompose, DBPgSizeDefault, v, nil)
	if err != nil || string(res.Stdout) != "7706291\n" {
		t.Fatalf("Run = %q, %v", res.Stdout, err)
	}
}

// waitGone fails unless the fake's grandchild, when it started one, is dead.
func waitGone(t *testing.T, r *Runner) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.DataDir, "docker", "grandchild"))
	if err != nil {
		return
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
			t.Fatalf("grandchild %d is still alive", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
