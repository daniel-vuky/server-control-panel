package project

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/daniel-vuky/server-control-panel/internal/runner"
)

// Status is the state of a project as Docker alone can tell it.
type Status string

// The five statuses.
const (
	StatusRunning Status = "running"
	StatusPartial Status = "partial"
	StatusStopped Status = "stopped"
	StatusError   Status = "error"
	StatusUnknown Status = "unknown"
)

// Container is one line of the docker.containers output.
type Container struct {
	Project     string // compose project name
	Service     string
	State       string // running, exited, restarting, paused, dead, created
	ExitCode    int    // from the Status column; valid when HasExitCode
	HasExitCode bool
	PanelImage  bool // carries the panel image label: the panel's own container
	OneOff      bool // left behind by docker compose run
}

var exitedRe = regexp.MustCompile(`^Exited \((\d+)\)`)

// Column positions, derived from runner.ContainerColumns so the two cannot drift.
var (
	nColumns   = len(strings.Split(runner.ContainerColumns, ","))
	colProject = column("project")
	colService = column("service")
	colState   = column("state")
	colStatus  = column("status")
	colImage   = column("image")
	colOneOff  = column("oneoff")
)

func column(name string) int {
	for i, c := range strings.Split(runner.ContainerColumns, ",") {
		if c == name {
			return i
		}
	}
	panic("project: runner.ContainerColumns has no column " + name)
}

// ParseContainers reads docker.containers output: one tab-separated line per
// container, in runner.ContainerColumns order. Lines with the wrong number of
// columns, and lines without a compose project (containers that compose did
// not create), are skipped.
func ParseContainers(out []byte) []Container {
	var cs []Container
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != nColumns || f[colProject] == "" {
			continue
		}
		c := Container{
			Project:    f[colProject],
			Service:    f[colService],
			State:      f[colState],
			PanelImage: f[colImage] == "panel",
			OneOff:     f[colOneOff] == "True",
		}
		if m := exitedRe.FindStringSubmatch(f[colStatus]); m != nil {
			if code, err := strconv.Atoi(m[1]); err == nil {
				c.ExitCode, c.HasExitCode = code, true
			}
		}
		cs = append(cs, c)
	}
	return cs
}

// StatusOf maps the containers of one project to a Status. expected is the
// project's service names from the compose config; an empty list means the
// config could not be read, so the status is Unknown. One-off containers and
// other projects' containers are ignored. First match wins:
//
//  1. no containers: Stopped
//  2. any restarting or dead, or exited with a code other than 0, 137 or 143: Error
//  3. every expected service has a running container: Running
//  4. at least one running: Partial
//  5. otherwise: Stopped
//
// A paused container counts as not running. 137 and 143 are what docker stop
// leaves behind.
func StatusOf(cs []Container, project string, expected []string) Status {
	if len(expected) == 0 {
		return StatusUnknown
	}
	runningServices := map[string]bool{}
	seen, anyRunning := false, false
	for _, c := range cs {
		if c.Project != project || c.OneOff {
			continue
		}
		seen = true
		switch {
		case c.State == "restarting" || c.State == "dead":
			return StatusError
		case c.State == "exited" && c.HasExitCode && c.ExitCode != 0 && c.ExitCode != 137 && c.ExitCode != 143:
			return StatusError
		case c.State == "running":
			runningServices[c.Service] = true
			anyRunning = true
		}
	}
	if !seen {
		return StatusStopped
	}
	all := true
	for _, s := range expected {
		if !runningServices[s] {
			all = false
			break
		}
	}
	switch {
	case all:
		return StatusRunning
	case anyRunning:
		return StatusPartial
	}
	return StatusStopped
}

// SelfProject returns the compose project of the panel's own container, or ""
// when there is none (in dev and in tests).
func SelfProject(cs []Container) string {
	for _, c := range cs {
		if c.PanelImage {
			return c.Project
		}
	}
	return ""
}
