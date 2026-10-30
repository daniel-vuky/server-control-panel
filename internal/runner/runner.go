// Package runner is the only place the panel starts a process (ADR 0005,
// enforced by depguard). It runs the docker commands listed in registry.go,
// with every value as a separate argument and never through a shell.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultBin     = "docker"
	defaultTimeout = 15 * time.Second
	defaultPath    = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	// LifecycleTimeout is the deadline of a compose up, down, stop or restart.
	LifecycleTimeout = 60 * time.Second
	// PingTimeout is the deadline of docker.ping.
	PingTimeout = 5 * time.Second
	// outputCap is the most bytes kept of each of stdout and stderr.
	outputCap = 4 << 20
	// waitDelay bounds how long Run waits for the output pipes after a kill.
	waitDelay = 2 * time.Second
	// mask replaces every secret in stderr.
	mask = "••••"
	// maxLine is the most bytes of one log line Stream delivers; the rest of an
	// over-long line is dropped.
	maxLine = 16 << 10
	// minSecret is the shortest secret that is masked, so that masking never
	// shreds common short text.
	minSecret = 4
)

var (
	// ErrUnknownCommand means the stack and action are not in the registry.
	ErrUnknownCommand = errors.New("runner: unknown command")
	// ErrBadValue means a value is missing, extra, empty, or not an absolute clean path.
	ErrBadValue = errors.New("runner: bad value")
)

// timeoutFor is the deadline of action a: the lifecycle or ping value for
// those actions, else fallback (the Runner's Timeout, or 15 s). It is a
// variable only so that tests in this package can shorten it.
var timeoutFor = func(a Action, fallback time.Duration) time.Duration {
	switch a {
	case ComposeUp, ComposeUpOverride, ComposeStop, ComposeStopOverride,
		ComposeRestart, ComposeRestartOverride, ComposeDown, ComposeDownOverride:
		return LifecycleTimeout
	case DockerPing:
		return PingTimeout
	}
	return fallback
}

// TimeoutError means the command ran past its deadline and was killed.
type TimeoutError struct{ After time.Duration }

func (e *TimeoutError) Error() string {
	return "docker command timed out after " + e.After.String()
}

// ExitError means the command exited with a non-zero status.
type ExitError struct {
	Code   int
	Stderr string // masked, trailing newlines trimmed
}

func (e *ExitError) Error() string {
	if first, _, _ := strings.Cut(e.Stderr, "\n"); first != "" {
		return first
	}
	return "exit status " + strconv.Itoa(e.Code)
}

// Result is what a finished command left. Stdout is raw, because the caller
// parses it; Stderr is masked. Each is capped at 4 MiB and Truncated says that
// either lost bytes.
type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
	Duration       time.Duration
	Truncated      bool
}

// Runner runs registry commands with the docker binary.
type Runner struct {
	Bin     string        // the docker binary; "docker" when empty
	DataDir string        // PANEL_DATA_DIR; the child's HOME is DataDir/docker
	Timeout time.Duration // per command; 15 s when zero
}

// Run runs the command a and returns its result. v fills the template's
// placeholders; secrets are masked out of stderr. A non-zero exit gives both
// the Result and an *ExitError; a missed deadline gives a *TimeoutError.
func (r *Runner) Run(ctx context.Context, s Stack, a Action, v Values, secrets []string) (Result, error) {
	if isFollow(a) {
		return Result{}, fmt.Errorf("%w: %s only runs through Stream", ErrUnknownCommand, a)
	}
	bin, args, env, timeout, err := r.prepare(s, a, v)
	if err != nil {
		return Result{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := r.command(runCtx, bin, args, env)
	var stdout, stderr capWriter
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	start := time.Now()
	runErr := cmd.Run()
	res := Result{
		Stdout:    stdout.buf.Bytes(),
		Stderr:    []byte(Mask(stderr.buf.String(), secrets)),
		ExitCode:  cmd.ProcessState.ExitCode(),
		Duration:  time.Since(start),
		Truncated: stdout.truncated || stderr.truncated,
	}
	if runErr == nil {
		return res, nil
	}
	if ctx.Err() != nil {
		return res, fmt.Errorf("docker %s: %w", a, ctx.Err())
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return res, &TimeoutError{After: timeout}
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return res, &ExitError{Code: res.ExitCode, Stderr: strings.TrimRight(string(res.Stderr), "\n")}
	}
	return res, fmt.Errorf("docker %s: %s", a, Mask(runErr.Error(), secrets))
}

// prepare checks the call against the registry and returns what a command
// needs: the program, the argument list, the child's environment and the
// deadline for a. Both Run and Stream start from it.
func (r *Runner) prepare(s Stack, a Action, v Values) (bin string, args, env []string, timeout time.Duration, err error) {
	args, err = argv(s, a, v)
	if err != nil {
		return "", nil, nil, 0, err
	}
	env, err = r.childEnv()
	if err != nil {
		return "", nil, nil, 0, err
	}
	bin, timeout = r.Bin, r.Timeout
	if bin == "" {
		bin = defaultBin
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return bin, args, env, timeoutFor(a, timeout), nil
}

// command builds the process: the minimal environment, its own process group
// that ctx kills whole, and a bound on the wait for its output pipes.
func (r *Runner) command(ctx context.Context, bin string, args, env []string) *exec.Cmd {
	//nolint:gosec // G204: the program is configuration and args come from the registry, one element per value, with no shell.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	setProcessGroup(cmd)
	cmd.WaitDelay = waitDelay
	return cmd
}

// Stream runs the follow action a (compose logs --follow) and calls onLine for
// each line of stdout and stderr, masked of secrets, until the command ends or
// ctx is cancelled. onLine is never called concurrently. A line longer than
// 16 KiB is cut and the rest of it dropped. There is no timeout: cancelling ctx
// kills the whole process group. It returns nil on exit 0, an *ExitError on a
// non-zero exit and an error wrapping ctx.Err() on cancel. Any other action is
// ErrUnknownCommand; Run is the way to run those.
func (r *Runner) Stream(ctx context.Context, s Stack, a Action, v Values, secrets []string, onLine func(string)) error {
	if !isFollow(a) {
		return fmt.Errorf("%w: %s does not run through Stream", ErrUnknownCommand, a)
	}
	bin, args, env, _, err := r.prepare(s, a, v)
	if err != nil {
		return err
	}
	cmd := r.command(ctx, bin, args, env)

	var mu sync.Mutex
	deliver := func(line string, cut bool) {
		line = finishLine(line, cut, secrets)
		mu.Lock()
		defer mu.Unlock()
		onLine(line)
	}
	var stderrKept capWriter
	outLines := &lineWriter{emit: deliver}
	errLines := &lineWriter{emit: deliver}
	cmd.Stdout = outLines
	cmd.Stderr = io.MultiWriter(errLines, &stderrKept)

	runErr := cmd.Run()
	outLines.flush()
	errLines.flush()
	if runErr == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("docker %s: %w", a, ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		stderr := strings.TrimRight(Mask(stderrKept.buf.String(), secrets), "\n")
		return &ExitError{Code: cmd.ProcessState.ExitCode(), Stderr: stderr}
	}
	return fmt.Errorf("docker %s: %s", a, Mask(runErr.Error(), secrets))
}

// finishLine masks a line. A line that was cut at maxLine first loses what the
// cut may have broken: an incomplete last rune, then any tail that is a proper
// prefix of a secret, because Mask cannot see a secret the cut split in two.
func finishLine(line string, cut bool, secrets []string) string {
	if cut {
		line = trimCut(line, secrets)
	}
	return Mask(line, secrets)
}

func trimCut(line string, secrets []string) string {
	for i := len(line) - 1; i >= 0 && i >= len(line)-utf8.UTFMax; i-- {
		if utf8.RuneStart(line[i]) {
			if !utf8.FullRuneInString(line[i:]) {
				line = line[:i]
			}
			break
		}
	}
	for trimmed := true; trimmed; {
		trimmed = false
		for _, secret := range secrets {
			if len(secret) < minSecret {
				continue
			}
			for k := min(len(secret)-1, len(line)); k > 0; k-- {
				if strings.HasSuffix(line, secret[:k]) {
					line = line[:len(line)-k]
					trimmed = true
					break
				}
			}
		}
	}
	return line
}

// lineWriter splits what is written into lines and hands each to emit, with
// cut set when the line was longer than maxLine. It keeps at most maxLine
// bytes of a line and drops the rest of that line.
type lineWriter struct {
	emit    func(line string, cut bool)
	buf     []byte
	dropped bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		if room := maxLine - len(w.buf); len(chunk) > room {
			chunk = chunk[:room]
			w.dropped = true
		}
		w.buf = append(w.buf, chunk...)
		if i < 0 {
			break
		}
		w.emitLine()
		p = p[i+1:]
	}
	return n, nil
}

// flush emits the unfinished last line, if there is one.
func (w *lineWriter) flush() {
	if len(w.buf) > 0 || w.dropped {
		w.emitLine()
	}
}

// emitLine emits the line in progress, empty or not, and starts a new line.
func (w *lineWriter) emitLine() {
	w.emit(strings.TrimSuffix(string(w.buf), "\r"), w.dropped)
	w.buf, w.dropped = w.buf[:0], false
}

// childEnv is the whole environment of the child. It is never the panel's own,
// which holds the Cloudflare values. HOME and DOCKER_CONFIG point at a private
// directory because the panel's user has no passwd entry or writable home.
func (r *Runner) childEnv() ([]string, error) {
	if r.DataDir == "" {
		return nil, errors.New("runner: DataDir is not set")
	}
	home := filepath.Join(r.DataDir, "docker")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("runner: create %s: %w", home, err)
	}
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = defaultPath
	}
	env := []string{"PATH=" + pathEnv, "HOME=" + home, "DOCKER_CONFIG=" + home}
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		env = append(env, "DOCKER_HOST="+host)
	}
	return env, nil
}

// Mask replaces every occurrence of each secret in s with ••••, longest secret
// first. Secrets shorter than 4 bytes are ignored.
func Mask(s string, secrets []string) string {
	var usable []string
	for _, secret := range secrets {
		if len(secret) >= minSecret {
			usable = append(usable, secret)
		}
	}
	if len(usable) == 0 {
		return s
	}
	sort.SliceStable(usable, func(i, j int) bool { return len(usable[i]) > len(usable[j]) })
	pairs := make([]string, 0, 2*len(usable))
	for _, secret := range usable {
		pairs = append(pairs, secret, mask)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

// capWriter keeps the first outputCap bytes and drops the rest, but keeps
// accepting writes so that the child never blocks on a full pipe.
type capWriter struct {
	buf       bytes.Buffer
	truncated bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	n := len(p)
	if room := outputCap - w.buf.Len(); n > room {
		w.truncated = true
		p = p[:room]
	}
	w.buf.Write(p)
	return n, nil
}
