package logs

import (
	"regexp"
	"strings"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
)

// Line is one line of container output.
type Line struct {
	Container string    // container name, such as shop-web-1; the raw prefix when unmapped
	Service   string    // compose service; "" when the prefix maps to no known container
	Time      time.Time // UTC, from the --timestamps column
	Text      string
}

var (
	// ansiRe matches the CSI escape sequences compose writes even with
	// --no-color (\x1b[K on status lines, colours from the container itself).
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	// lineRe is "<prefix> | <RFC 3339 timestamp> <text>". Compose pads the
	// prefix to the longest container name: "web-1  | …".
	lineRe = regexp.MustCompile(`^(\S+)\s+\| (\d{4}-\d\d-\d\dT[^ ]+Z) ?(.*)$`)
)

// ParseLine reads one line of compose logs --timestamps output. ANSI escapes
// are stripped first. A line that is not container output (a status line such
// as "web-1 exited with code 137", a compose warning, a bad timestamp) gives
// false. The prefix is mapped to a container of composeProject in cs by
// name == prefix || name == composeProject+"-"+prefix; an unmapped prefix is
// kept as the Container with no Service.
func ParseLine(raw, composeProject string, cs []project.Container) (Line, bool) {
	s := strings.TrimRight(ansiRe.ReplaceAllString(raw, ""), " ")
	m := lineRe.FindStringSubmatch(s)
	if m == nil {
		return Line{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, m[2])
	if err != nil {
		return Line{}, false
	}
	l := Line{Container: m[1], Time: ts.UTC(), Text: m[3]}
	full := composeProject + "-" + m[1]
	for _, c := range cs {
		if c.Project == composeProject && (c.Name == m[1] || c.Name == full) {
			l.Container, l.Service = c.Name, c.Service
			break
		}
	}
	return l, true
}
