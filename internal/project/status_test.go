package project

import (
	"fmt"
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

// id64 is a 64-hex container id, distinct per n.
func id64(n int) string { return fmt.Sprintf("%064x", n) }

// psLine is one docker.containers line in runner.ContainerColumns order.
func psLine(project, service, state, status, panel, oneOff, id, name, imageRef, ports string) string {
	return strings.Join([]string{project, service, state, status, panel, oneOff, id, name, imageRef, ports}, "\t")
}

func TestParseContainers(t *testing.T) {
	out := strings.Join([]string{
		psLine("shop", "web", "running", "Up 3 hours (healthy)", "", "False", id64(1), "shop-web-1", "nginx:1", "0.0.0.0:80->80/tcp"),
		psLine("shop", "db", "exited", "Exited (137) 2 hours ago", "", "", id64(2), "shop-db-1", "postgres:16", ""),
		psLine("shop", "migrate", "exited", "Exited (1) 5 minutes ago", "", "True", id64(3), "shop-migrate-1", "app", ""),
		"this line is malformed",
		"",
		psLine("monitor", "panel", "running", "Up 1 day", "panel", "False", id64(4), "monitor-panel-1", "scp-panel", ""),
		psLine("a", "b", "c", "d", "e", "f", "g", "h", "i", "j") + "\tk",                 // too many columns
		psLine("", "web", "running", "Up 1 day", "", "False", id64(5), "x", "nginx", ""), // no project: not a compose container
		"shop\tweb\trunning\tUp 3 hours\t\tFalse",                                        // the old 6 columns
	}, "\n") + "\n"

	got := ParseContainers([]byte(out))
	if len(got) != 4 {
		t.Fatalf("parsed %d containers, want 4: %+v", len(got), got)
	}
	want0 := Container{Project: "shop", Service: "web", State: "running", ID: id64(1), Name: "shop-web-1", Image: "nginx:1", Ports: "0.0.0.0:80->80/tcp"}
	if got[0] != want0 {
		t.Fatalf("got[0] = %+v, want %+v", got[0], want0)
	}
	want1 := Container{Project: "shop", Service: "db", State: "exited", ExitCode: 137, HasExitCode: true, ID: id64(2), Name: "shop-db-1", Image: "postgres:16"}
	if got[1] != want1 {
		t.Fatalf("got[1] = %+v, want %+v", got[1], want1)
	}
	if !got[2].OneOff || got[2].ExitCode != 1 || !got[2].HasExitCode {
		t.Fatalf("got[2] = %+v", got[2])
	}
	if !got[3].PanelImage || got[3].OneOff || got[3].Project != "monitor" {
		t.Fatalf("got[3] = %+v", got[3])
	}
}

func TestParseContainersNewColumns(t *testing.T) {
	line := psLine("shop", "web", "restarting", "Restarting (3) 4 seconds ago", "", "False", id64(9), "shop-web-1", "ghcr.io/x/web:2", "3000/tcp, 0.0.0.0:80->80/tcp")
	got := ParseContainers([]byte(line + "\n"))
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	c := got[0]
	if c.ID != id64(9) || c.Name != "shop-web-1" || c.Image != "ghcr.io/x/web:2" || c.Ports != "3000/tcp, 0.0.0.0:80->80/tcp" {
		t.Fatalf("new columns: %+v", c)
	}
	if c.PanelImage {
		t.Fatalf("the imageref column must not set PanelImage: %+v", c)
	}
	if !c.HasExitCode || c.ExitCode != 3 {
		t.Fatalf("a restarting container carries the last exit code from its Status: %+v", c)
	}
}

func TestParseContainersCRLFAndEmpty(t *testing.T) {
	if got := ParseContainers(nil); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	got := ParseContainers([]byte(psLine("shop", "web", "running", "Up 1 minute", "", "False", id64(1), "n", "i", "") + "\r\n"))
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
