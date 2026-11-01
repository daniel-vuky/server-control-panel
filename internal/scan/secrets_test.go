package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The three secrets of the masks fixture, one per source, and one outside
// the project folder that must never be read.
const (
	secretDotenv  = "envsecret1"  //nolint:gosec // fake credential, the test subject
	secretLiteral = "literal22"   //nolint:gosec // fake credential, the test subject
	secretEnvFile = "filesecret3" //nolint:gosec // fake credential, the test subject
	secretOutside = "outsidesecret4"
)

// syncLog is a log sink safe to read while a logger writes to it.
type syncLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newTestLog() (*slog.Logger, *syncLog) {
	buf := &syncLog{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// masksFixture is a project folder with a secret in .env, one in compose
// `environment:` (in the config JSON it returns) and one in an env_file,
// plus an env_file symlink that points outside the folder. It returns the
// folder and the config JSON as docker compose config prints it.
func masksFixture(t *testing.T) (dir, cfg string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(base, "shop")
	outside := filepath.Join(base, "elsewhere")
	for _, d := range []string{dir, outside} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, ".env"), "A_PASSWORD="+secretDotenv+"\n")
	writeFile(t, filepath.Join(dir, "db.env"), "TOKEN="+secretEnvFile+"\nLOG_LEVEL=info\n")
	writeFile(t, filepath.Join(outside, "other.env"), "SECRET_KEY="+secretOutside+"\n")
	if err := os.Symlink(filepath.Join(outside, "other.env"), filepath.Join(dir, "link.env")); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{
		"name": "shop",
		"services": map[string]any{
			"db": map[string]any{
				"image":       "postgres:16",
				"environment": map[string]any{"DB_PW": secretLiteral, "LOG_LEVEL": "info", "POSTGRES_PASSWORD": "${A_PASSWORD}"},
				"env_file": []map[string]any{
					{"path": filepath.Join(dir, "db.env"), "required": true},
					{"path": filepath.Join(dir, "link.env"), "required": true},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir, string(b)
}

func TestProjectSecretsSources(t *testing.T) {
	dir, raw := masksFixture(t)
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	log, buf := newTestLog()
	got := ProjectSecrets(cfg, dir, log)
	for _, want := range []string{secretDotenv, secretLiteral, secretEnvFile} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	for _, not := range []string{secretOutside, "info", "${A_PASSWORD}"} {
		if slices.Contains(got, not) {
			t.Errorf("unexpected %q in %v", not, got)
		}
	}
	if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Errorf("masks %v are not sorted and unique", got)
	}
	logged := buf.String()
	if !strings.Contains(logged, "level=INFO") || !strings.Contains(logged, filepath.Join(dir, "link.env")) {
		t.Errorf("log = %q, want an Info with the skipped path", logged)
	}
	if strings.Contains(logged, secretOutside) || strings.Contains(logged, "other.env") {
		t.Errorf("log = %q names the outside file or its value", logged)
	}
}

func TestProjectSecretsEnvFileEscapes(t *testing.T) {
	dir, _ := masksFixture(t)
	outside := filepath.Join(filepath.Dir(dir), "elsewhere", "other.env")
	cases := map[string]string{
		"absolute outside": outside,
		"dot-dot":          filepath.Join(dir, "..", "elsewhere", "other.env"),
		"missing":          filepath.Join(dir, "nope.env"),
		"the folder":       dir,
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := ComposeConfig{Name: "shop", Services: []ComposeService{{Name: "db", EnvFiles: []string{path}}}}
			got := ProjectSecrets(cfg, dir, slog.New(slog.DiscardHandler))
			if slices.Contains(got, secretOutside) || !slices.Equal(got, []string{secretDotenv}) {
				t.Fatalf("masks = %v, want only the .env one", got)
			}
		})
	}
}

func TestProjectSecretsDefaultsAndURLs(t *testing.T) {
	dir, _ := masksFixture(t)
	cfg := ComposeConfig{Name: "shop", Services: []ComposeService{{Name: "app", Environment: map[string]string{ //nolint:gosec // fake credentials, the test subject
		"DB_PASSWORD":  "${DB_PASSWORD:-fallbackpw}",
		"DATABASE_URL": "postgres://appuser:urlpass99@db/app",
		"PORT":         "8080",
	}}}}
	got := ProjectSecrets(cfg, dir, slog.New(slog.DiscardHandler))
	for _, want := range []string{"fallbackpw", "appuser", "urlpass99", secretDotenv} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if slices.Contains(got, "8080") {
		t.Errorf("unexpected 8080 in %v", got)
	}
}

func TestIsSecretKeyPW(t *testing.T) {
	for _, k := range []string{"DB_PW", "PW_ROOT", "pw", "MY_PW_2", "DB_PASSWORD", "API_KEY"} {
		if !isSecretKey(k) {
			t.Errorf("isSecretKey(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"UPWARD", "PWNED", "LOG_LEVEL", "PORT", "SPW_X"} {
		if isSecretKey(k) {
			t.Errorf("isSecretKey(%q) = true, want false", k)
		}
	}
}

func TestShortValuesNotMasked(t *testing.T) {
	dir, _ := masksFixture(t)
	writeFile(t, filepath.Join(dir, "short.env"), "TOKEN=xyz\n")
	cfg := ComposeConfig{Name: "shop", Services: []ComposeService{{
		Name:        "db",
		Environment: map[string]string{"DB_PW": "abc", "API_KEY": ""},
		EnvFiles:    []string{filepath.Join(dir, "short.env")},
	}}}
	got := ProjectSecrets(cfg, dir, slog.New(slog.DiscardHandler))
	if !slices.Equal(got, []string{secretDotenv}) {
		t.Fatalf("masks = %v, want only %q", got, secretDotenv)
	}
}

func TestParseConfigEnvFiles(t *testing.T) {
	raw := `{"name":"x","services":{
		"a":{"env_file":[{"path":"/srv/x/a.env","required":true},{"path":"/srv/x/b.env","required":false}]},
		"b":{"env_file":["/srv/x/c.env"]},
		"c":{"env_file":"/srv/x/d.env"},
		"d":{}
	}}`
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"a": {"/srv/x/a.env", "/srv/x/b.env"},
		"b": {"/srv/x/c.env"},
		"c": {"/srv/x/d.env"},
		"d": nil,
	}
	for _, s := range cfg.Services {
		if !slices.Equal(s.EnvFiles, want[s.Name]) {
			t.Errorf("%s env files = %v, want %v", s.Name, s.EnvFiles, want[s.Name])
		}
	}
}

// trackedAt is a tracked project in dir with the given compose files.
func trackedAt(dir string, files ...string) store.Tracked {
	return store.Tracked{ID: 7, Path: dir, ComposeProject: "shop", ComposeFiles: files}
}

func TestReadProjectSecrets(t *testing.T) {
	dir, raw := masksFixture(t)
	for _, files := range [][]string{{"compose.yaml"}, {"compose.yaml", "compose.override.yaml"}} {
		f := newFakeCmd()
		f.configs[dir] = fakeReply{stdout: raw}
		tr := trackedAt(dir, files...)
		got := ReadProjectSecrets(context.Background(), f, tr, slog.New(slog.DiscardHandler))
		for _, want := range []string{secretDotenv, secretLiteral, secretEnvFile} {
			if !slices.Contains(got, want) {
				t.Errorf("%v: missing %q in %v", files, want, got)
			}
		}
		action := runner.ComposeConfig
		if len(files) > 1 {
			action = runner.ComposeConfigOverride
		}
		calls := f.callsOf(action)
		values, _, _ := tr.ComposeValues()
		if len(f.calls) != 1 || len(calls) != 1 || calls[0].Stack != runner.StackCompose ||
			len(calls[0].Values) != len(values) || calls[0].Values["file"] != values["file"] || calls[0].Values["override"] != values["override"] {
			t.Fatalf("%v: calls = %+v, want one %s with %v", files, f.calls, action, values)
		}
		// The config read itself is masked by what is known before it: .env.
		if !slices.Equal(calls[0].Secrets, []string{secretDotenv}) {
			t.Errorf("%v: config read secrets = %v, want the .env masks", files, calls[0].Secrets)
		}
	}
}

func TestReadProjectSecretsFallback(t *testing.T) {
	dir, _ := masksFixture(t)
	cases := map[string]struct {
		reply fakeReply
		code  string
	}{
		"exit":      {fakeReply{err: &runner.ExitError{Code: 1, Stderr: "invalid value " + secretLiteral + " in " + secretEnvFile}}, "code=exit"},
		"timeout":   {fakeReply{err: &runner.TimeoutError{}}, "code=timeout"},
		"other":     {fakeReply{err: errors.New("start docker: " + secretLiteral)}, "code=run"},
		"truncated": {fakeReply{stdout: `{"name":"shop"`, truncated: true}, "code=truncated"},
		"not json":  {fakeReply{stdout: "garbage " + secretLiteral}, "code=unreadable"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeCmd()
			f.configs[dir] = tc.reply
			log, buf := newTestLog()
			got := ReadProjectSecrets(context.Background(), f, trackedAt(dir, "compose.yaml"), log)
			if !slices.Equal(got, []string{secretDotenv}) {
				t.Fatalf("masks = %v, want the .env masks", got)
			}
			logged := buf.String()
			if !strings.Contains(logged, "level=WARN") || !strings.Contains(logged, tc.code) || !strings.Contains(logged, "project=7") {
				t.Fatalf("log = %q, want a Warn with %s", logged, tc.code)
			}
			for _, leak := range []string{secretLiteral, secretEnvFile, secretDotenv, "invalid value", "start docker", "garbage"} {
				if strings.Contains(logged, leak) {
					t.Fatalf("log = %q carries %q", logged, leak)
				}
			}
		})
	}
}

func TestReadProjectSecretsCancelledIsQuiet(t *testing.T) {
	dir, _ := masksFixture(t)
	f := newFakeCmd()
	f.configs[dir] = fakeReply{err: context.Canceled}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	log, buf := newTestLog()
	if got := ReadProjectSecrets(ctx, f, trackedAt(dir, "compose.yaml"), log); !slices.Equal(got, []string{secretDotenv}) {
		t.Fatalf("masks = %v, want the .env masks", got)
	}
	if buf.String() != "" {
		t.Fatalf("log = %q, want nothing for a cancelled read", buf.String())
	}
}

func TestReadProjectSecretsNoComposeFile(t *testing.T) {
	dir, _ := masksFixture(t)
	f := newFakeCmd()
	got := ReadProjectSecrets(context.Background(), f, trackedAt(dir), slog.New(slog.DiscardHandler))
	if !slices.Equal(got, []string{secretDotenv}) || len(f.calls) != 0 {
		t.Fatalf("masks = %v, calls = %+v; want the .env masks and no command", got, f.calls)
	}
}
