package project

import (
	"strings"
	"testing"
)

func c(state string) Container { return Container{Project: "shop", Service: "web", State: state} }

func exited(service string, code int) Container {
	return Container{Project: "shop", Service: service, State: "exited", ExitCode: code, HasExitCode: true}
}

func running(service string) Container {
	return Container{Project: "shop", Service: service, State: "running"}
}

func TestStatusOf(t *testing.T) {
	oneOff := exited("migrate", 1)
	oneOff.OneOff = true
	other := exited("web", 1)
	other.Project = "other"
	otherRunning := running("web")
	otherRunning.Project = "other"
	restarting := c("restarting")
	dead := c("dead")
	paused := c("paused")

	tests := []struct {
		name     string
		cs       []Container
		expected []string
		want     Status
	}{
		{"no containers", nil, []string{"web"}, StatusStopped},
		{"restarting", []Container{restarting}, []string{"web"}, StatusError},
		{"dead", []Container{dead}, []string{"web"}, StatusError},
		{"exited 1", []Container{exited("web", 1)}, []string{"web"}, StatusError},
		{"exited 137", []Container{exited("web", 137)}, []string{"web"}, StatusStopped},
		{"exited 143", []Container{exited("web", 143)}, []string{"web"}, StatusStopped},
		{"exited 0", []Container{exited("web", 0)}, []string{"web"}, StatusStopped},
		{"exited without a code is not an error", []Container{c("exited")}, []string{"web"}, StatusStopped},
		{"both services running", []Container{running("web"), running("db")}, []string{"web", "db"}, StatusRunning},
		{"one of two running", []Container{running("web")}, []string{"web", "db"}, StatusPartial},
		{"one running one stopped cleanly", []Container{running("web"), exited("db", 0)}, []string{"web", "db"}, StatusPartial},
		{"error wins over running", []Container{running("web"), exited("db", 1)}, []string{"web", "db"}, StatusError},
		{"paused only", []Container{paused}, []string{"web"}, StatusStopped},
		{"paused next to running is partial", []Container{running("db"), paused}, []string{"web", "db"}, StatusPartial},
		{"created only", []Container{c("created")}, []string{"web"}, StatusStopped},
		{"one-off exited 1 next to all running", []Container{running("web"), running("db"), oneOff}, []string{"web", "db"}, StatusRunning},
		{"one-off only", []Container{oneOff}, []string{"web"}, StatusStopped},
		{"another project is ignored", []Container{other, otherRunning}, []string{"web"}, StatusStopped},
		{"another project does not make this partial", []Container{running("web"), other}, []string{"web", "db"}, StatusPartial},
		{"empty expected with containers running", []Container{running("web")}, nil, StatusUnknown},
		{"empty expected without containers", nil, []string{}, StatusUnknown},
		{"an unexpected extra service does not count", []Container{running("worker")}, []string{"web"}, StatusPartial},
		{"two replicas, one running", []Container{running("web"), c("exited")}, []string{"web"}, StatusRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StatusOf(tt.cs, "shop", tt.expected); got != tt.want {
				t.Fatalf("StatusOf = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusValues(t *testing.T) {
	for s, want := range map[Status]string{
		StatusRunning: "running", StatusPartial: "partial", StatusStopped: "stopped",
		StatusError: "error", StatusUnknown: "unknown",
		StatusStarting: "starting", StatusStopping: "stopping",
		StatusRestarting: "restarting", StatusNotFound: "notfound",
	} {
		if string(s) != want {
			t.Fatalf("%q != %q", s, want)
		}
	}
}

func TestParseContainers(t *testing.T) {
	out := strings.Join([]string{
		"shop\tweb\trunning\tUp 3 hours (healthy)\t\tFalse",
		"shop\tdb\texited\tExited (137) 2 hours ago\t\t",
		"shop\tmigrate\texited\tExited (1) 5 minutes ago\t\tTrue",
		"this line is malformed",
		"",
		"monitor\tpanel\trunning\tUp 1 day\tpanel\tFalse",
		"a\tb\tc\td\te\tf\tg",               // too many columns
		"\tweb\trunning\tUp 1 day\t\tFalse", // no project: not a compose container
	}, "\n") + "\n"

	got := ParseContainers([]byte(out))
	if len(got) != 4 {
		t.Fatalf("parsed %d containers, want 4: %+v", len(got), got)
	}
	if want := (Container{Project: "shop", Service: "web", State: "running"}); got[0] != want {
		t.Fatalf("got[0] = %+v, want %+v", got[0], want)
	}
	if want := (Container{Project: "shop", Service: "db", State: "exited", ExitCode: 137, HasExitCode: true}); got[1] != want {
		t.Fatalf("got[1] = %+v, want %+v", got[1], want)
	}
	if !got[2].OneOff || got[2].ExitCode != 1 || !got[2].HasExitCode {
		t.Fatalf("got[2] = %+v", got[2])
	}
	if !got[3].PanelImage || got[3].OneOff || got[3].Project != "monitor" {
		t.Fatalf("got[3] = %+v", got[3])
	}
}

func TestParseContainersCRLFAndEmpty(t *testing.T) {
	if got := ParseContainers(nil); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	got := ParseContainers([]byte("shop\tweb\trunning\tUp 1 minute\t\tFalse\r\n"))
	if len(got) != 1 || got[0].OneOff {
		t.Fatalf("got %+v", got)
	}
}

func TestSelfProject(t *testing.T) {
	panel := Container{Project: "monitor", Service: "panel", State: "running", PanelImage: true}
	if got := SelfProject([]Container{running("web"), panel}); got != "monitor" {
		t.Fatalf("SelfProject = %q", got)
	}
	if got := SelfProject([]Container{running("web")}); got != "" {
		t.Fatalf("SelfProject = %q, want empty", got)
	}
	if got := SelfProject(nil); got != "" {
		t.Fatalf("SelfProject = %q, want empty", got)
	}
}

func TestAnyActive(t *testing.T) {
	oneOffRunning := running("migrate")
	oneOffRunning.OneOff = true
	otherRunning := running("web")
	otherRunning.Project = "other"

	tests := []struct {
		name string
		cs   []Container
		want bool
	}{
		{"running", []Container{running("web")}, true},
		{"restarting", []Container{c("restarting")}, true},
		{"exited 1 only", []Container{exited("web", 1)}, false},
		{"exited 0 plus a one-off running", []Container{exited("web", 0), oneOffRunning}, false},
		{"another project running", []Container{otherRunning}, false},
		{"no containers", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AnyActive(tt.cs, "shop"); got != tt.want {
				t.Fatalf("AnyActive = %v, want %v", got, tt.want)
			}
		})
	}
}
