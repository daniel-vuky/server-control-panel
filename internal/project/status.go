package project

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/daniel-vuky/server-control-panel/internal/runner"
)

// Status is the state of a project as Docker alone can tell it.
type Status string

// The nine statuses. StatusOf only ever returns the first five; Starting,
// Stopping and Restarting are set while a lifecycle command runs, and NotFound
// marks a tracked project whose folder is gone.
const (
	StatusRunning    Status = "running"
	StatusPartial    Status = "partial"
	StatusStopped    Status = "stopped"
	StatusError      Status = "error"
	StatusUnknown    Status = "unknown"
	StatusStarting   Status = "starting"
	StatusStopping   Status = "stopping"
	StatusRestarting Status = "restarting"
	StatusNotFound   Status = "notfound"
)

// Container is one line of the docker.containers output.
type Container struct {
	Project     string // compose project name
	Service     string
	State       string // running, exited, restarting, paused, dead, created
	ExitCode    int    // from the Status column; valid when HasExitCode
	HasExitCode bool
	PanelImage  bool   // carries the panel image label: the panel's own container
	OneOff      bool   // left behind by docker compose run
	ID          string // full container id
	Name        string // container name, such as shop-web-1
	Image       string // image reference the container was created from
	Ports       string // raw docker ps Ports column; see FormatPorts
}

// exitedRe reads the exit code out of "Exited (1) 5 minutes ago" and, for a
// restarting container, "Restarting (1) 4 seconds ago".
var exitedRe = regexp.MustCompile(`^(?:Exited|Restarting) \((\d+)\)`)

// Column positions, derived from runner.ContainerColumns so the two cannot drift.
var (
	nColumns   = len(strings.Split(runner.ContainerColumns, ","))
	colProject = column("project")
	colService = column("service")
	colState   = column("state")
	colStatus  = column("status")
	colImage   = column("image")
	colOneOff  = column("oneoff")
	colID      = column("id")
	colName    = column("name")
	colImgRef  = column("imageref")
	colPorts   = column("ports")
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
			ID:         f[colID],
			Name:       f[colName],
			Image:      f[colImgRef],
			Ports:      f[colPorts],
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

// AnyActive reports whether a container of composeProject is running or
// restarting. One-off containers and other projects' containers are ignored.
func AnyActive(cs []Container, composeProject string) bool {
	for _, c := range cs {
		if c.Project != composeProject || c.OneOff {
			continue
		}
		if c.State == "running" || c.State == "restarting" {
			return true
		}
	}
	return false
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
