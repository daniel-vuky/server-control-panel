package scan

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

var (
	// varReference is a value that only names a variable: ${NAME} or $NAME.
	// It is a name, not a value; the variable's value comes from .env.
	varReference = regexp.MustCompile(`^\$(\{[A-Za-z_][A-Za-z0-9_]*\}|[A-Za-z_][A-Za-z0-9_]*)$`)
	// varDefault is the default written in a ${NAME:-default} or
	// ${NAME-default} reference, a value written in the file.
	varDefault = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*:?-([^}]*)\}`)
)

// ProjectSecrets returns the values to mask for a project: the secrets of
// its .env, the secret-looking values of every service's `environment:` in
// cfg, and the secrets of each service's env_file whose real path (symlinks
// resolved) is inside dir, read up to 1 MiB; URL user and password parts
// count, as SecretValues says. An env_file outside dir, or one that cannot be
// resolved, is skipped with an Info log of its path only. The result is sorted
// and has no duplicates; values under 4 characters are left out. The values
// of the variables in names (a hand-added database's user and password
// variables) are masked from the same three sources whatever their names
// look like.
func ProjectSecrets(cfg ComposeConfig, dir string, log *slog.Logger, names ...string) []string {
	if log == nil {
		log = slog.Default()
	}
	set := map[string]struct{}{}
	add := func(vs []string) {
		for _, v := range vs {
			set[v] = struct{}{}
		}
	}
	dotenv := ReadDotenv(filepath.Join(dir, ".env"))
	add(SecretValues(dotenv))
	add(namedValues(dotenv, names))
	realDir, dirErr := filepath.EvalSymlinks(dir)
	for _, s := range cfg.Services {
		for k, v := range s.Environment {
			for _, lit := range literals(v) {
				add(SecretValues(map[string]string{k: lit}))
				add(namedValues(map[string]string{k: lit}, names))
			}
		}
		for _, p := range s.EnvFiles {
			path, ok := envFileInside(p, dir, realDir, dirErr)
			if !ok {
				log.Info("scan: an env_file that is not a file inside the project folder is not read for masks", "path", p)
				continue
			}
			file := ReadDotenv(path)
			add(SecretValues(file))
			add(namedValues(file, names))
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// namedValues are the values in env of the variables in names, whatever the
// names look like, each of minSecretLen characters or more.
func namedValues(env map[string]string, names []string) []string {
	var out []string
	for _, n := range names {
		if v, ok := env[n]; ok && n != "" && len(v) >= minSecretLen {
			out = append(out, v)
		}
	}
	return out
}

// literals are the values of one `environment:` entry worth masking: the
// value as written (and with $$ unescaped), and the defaults of the variable
// references in it. A value that only names a variable gives nothing.
func literals(v string) []string {
	if v == "" || varReference.MatchString(v) {
		return nil
	}
	out := []string{v}
	if strings.Contains(v, "$$") {
		out = append(out, strings.ReplaceAll(v, "$$", "$"))
	}
	for _, m := range varDefault.FindAllStringSubmatch(v, -1) {
		out = append(out, m[1])
	}
	return out
}

// envFileInside resolves an env_file path (relative to dir when compose did
// not print it absolute) and returns it when it lies inside the project
// folder realDir, the folder itself excluded.
func envFileInside(p, dir, realDir string, dirErr error) (string, bool) {
	if dirErr != nil {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(realDir, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return resolved, true
}

// ReadProjectSecrets reads a tracked project's compose config (the
// compose.config action, masked by its .env) and returns ProjectSecrets of
// it. When the read or the parse fails it returns the .env masks and logs a
// Warn with the project and a code (exit, timeout, run, truncated or
// unreadable), never the command's output, which can quote the compose file.
// A read cut short by ctx is not logged. names are passed to ProjectSecrets
// (and masked from the .env when the read fails).
func ReadProjectSecrets(ctx context.Context, cmd Commander, t store.Tracked, log *slog.Logger, names ...string) []string {
	if log == nil {
		log = slog.Default()
	}
	env := ReadDotenv(filepath.Join(t.Path, ".env"))
	dotenv := SecretValues(env)
	values, override, ok := t.ComposeValues()
	if !ok {
		return withNamed(dotenv, env, names)
	}
	action := runner.ComposeConfig
	if override {
		action = runner.ComposeConfigOverride
	}
	res, err := cmd.Run(ctx, runner.StackCompose, action, values, dotenv)
	var cfg ComposeConfig
	code := ""
	switch {
	case err != nil:
		code = errorCode(err)
	case res.Truncated:
		code = "truncated"
	default:
		if cfg, err = ParseConfig(res.Stdout); err != nil {
			code = "unreadable"
		}
	}
	if code != "" {
		if ctx.Err() == nil {
			log.Warn("scan: cannot read the compose config for masks; masking the .env values only",
				"project", t.ID, "code", code)
		}
		return withNamed(dotenv, env, names)
	}
	return ProjectSecrets(cfg, t.Path, log, names...)
}

// withNamed is secrets plus the values in env of the variables in names,
// sorted with no duplicates (the .env masks of a failed config read).
func withNamed(secrets []string, env map[string]string, names []string) []string {
	extra := namedValues(env, names)
	if len(extra) == 0 {
		return secrets
	}
	out := append(slices.Clone(secrets), extra...)
	slices.Sort(out)
	return slices.Compact(out)
}

// errorCode names a runner error without its text.
func errorCode(err error) string {
	var exitErr *runner.ExitError
	var timeoutErr *runner.TimeoutError
	switch {
	case errors.As(err, &exitErr):
		return "exit"
	case errors.As(err, &timeoutErr):
		return "timeout"
	}
	return "run"
}
