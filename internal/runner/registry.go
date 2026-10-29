package runner

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Stack is the kind of thing a command works on. v1 manages docker compose
// projects only, and reads the daemon's containers.
type Stack string

// The stacks the registry has commands for.
const (
	StackCompose Stack = "compose"
	StackDocker  Stack = "docker"
)

// Action names one whitelisted command.
type Action string

// The whitelisted actions.
const (
	ComposeConfig         Action = "compose.config"
	ComposeConfigOverride Action = "compose.config.override"
	DockerContainers      Action = "docker.containers"
)

// Values are the arguments of one call, keyed by placeholder name without
// braces: "dir" for {dir}, "file" for {file}, "override" for {override}.
type Values map[string]string

// Labels that docker compose and the panel put on containers.
const (
	labelProject = "com.docker.compose.project"
	labelService = "com.docker.compose.service"
	labelOneOff  = "com.docker.compose.oneoff"
	// labelImage marks the panel's own image.
	labelImage = "io.github.daniel-vuky.server-control-panel.image"
)

// ContainerColumns is the column order of DockerContainers output, one
// tab-separated line per container. Parsers refer to the columns by these names.
const ContainerColumns = "project,service,state,status,image,oneoff"

// containersFormat is the --format template of DockerContainers: one fixed
// argv element, built from the label names and never from input. .Labels is
// not used because docker ps joins it with commas and a label value can
// contain one.
var containersFormat = strings.Join([]string{
	label(labelProject),
	label(labelService),
	"{{.State}}",
	"{{.Status}}",
	label(labelImage),
	label(labelOneOff),
}, "\t")

func label(name string) string {
	return `{{.Label "` + name + `"}}`
}

// arg is one argv element: a fixed literal, or a placeholder that is replaced,
// whole, by one value.
type arg struct {
	literal string
	slot    string
}

func lit(elements ...string) []arg {
	args := make([]arg, len(elements))
	for i, e := range elements {
		args[i] = arg{literal: e}
	}
	return args
}

// pathArg is a placeholder whose value must be an absolute, clean path.
func pathArg(slot string) arg { return arg{slot: slot} }

func join(parts ...[]arg) []arg {
	var all []arg
	for _, p := range parts {
		all = append(all, p...)
	}
	return all
}

type key struct {
	stack  Stack
	action Action
}

// registry is the whole list of commands the panel can run, as argv templates
// for the docker binary. There is no way to add an entry at run time.
var registry = map[key][]arg{
	{StackCompose, ComposeConfig}: join(
		lit("compose", "--project-directory"), []arg{pathArg("dir")},
		lit("-f"), []arg{pathArg("file")},
		lit("config", "--format", "json", "--no-env-resolution"),
	),
	{StackCompose, ComposeConfigOverride}: join(
		lit("compose", "--project-directory"), []arg{pathArg("dir")},
		lit("-f"), []arg{pathArg("file")},
		lit("-f"), []arg{pathArg("override")},
		lit("config", "--format", "json", "--no-env-resolution"),
	),
	{StackDocker, DockerContainers}: lit(
		"ps", "--all", "--no-trunc",
		"--filter", "label="+labelProject,
		"--format", containersFormat,
	),
}

// argv checks the call against its template and returns the argument list,
// without the program. Every error is returned before anything runs, and
// every value ends up as one separate element, never as shell text.
func argv(s Stack, a Action, v Values) ([]string, error) {
	tpl, ok := registry[key{s, a}]
	if !ok {
		return nil, fmt.Errorf("%w: %s on %s", ErrUnknownCommand, a, s)
	}
	used := map[string]bool{}
	out := make([]string, len(tpl))
	for i, el := range tpl {
		if el.slot == "" {
			out[i] = el.literal
			continue
		}
		used[el.slot] = true
		val, ok := v[el.slot]
		if !ok {
			return nil, fmt.Errorf("%w: %s needs {%s}", ErrBadValue, a, el.slot)
		}
		if err := checkPath(el.slot, val); err != nil {
			return nil, err
		}
		out[i] = val
	}
	for name := range v {
		if !used[name] {
			return nil, fmt.Errorf("%w: %s does not take {%s}", ErrBadValue, a, name)
		}
	}
	return out, nil
}

// checkPath accepts only an absolute, filepath.Clean path without NUL bytes.
// Being absolute, it cannot start with "-" and be read as a flag.
func checkPath(slot, val string) error {
	switch {
	case val == "":
		return fmt.Errorf("%w: {%s} is empty", ErrBadValue, slot)
	case strings.ContainsRune(val, 0):
		return fmt.Errorf("%w: {%s} contains a NUL byte", ErrBadValue, slot)
	case !filepath.IsAbs(val):
		return fmt.Errorf("%w: {%s} is not an absolute path", ErrBadValue, slot)
	case filepath.Clean(val) != val:
		return fmt.Errorf("%w: {%s} is not a clean path", ErrBadValue, slot)
	}
	return nil
}
