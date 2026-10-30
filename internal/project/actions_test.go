package project

import (
	"reflect"
	"testing"
)

func TestAllowed(t *testing.T) {
	statuses := []Status{
		StatusRunning, StatusPartial, StatusStopped, StatusError, StatusUnknown,
		StatusStarting, StatusStopping, StatusRestarting, StatusNotFound,
	}
	base := map[Status][]Action{
		StatusRunning: {ActionStop, ActionRestart},
		StatusPartial: {ActionStart, ActionStop, ActionRestart},
		StatusStopped: {ActionStart},
		StatusError:   {ActionStart, ActionRestart},
	}
	for _, s := range statuses {
		for _, self := range []bool{false, true} {
			for _, up := range []bool{false, true} {
				want := []Action{}
				switch s {
				case StatusNotFound:
					want = []Action{ActionRemove}
				case StatusStarting, StatusStopping, StatusRestarting:
					want = []Action{}
				default:
					if !self && up {
						want = append(want, base[s]...)
					}
					want = append(want, ActionUntrack)
				}
				got := Allowed(s, self, up)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("Allowed(%q, self=%v, dockerUp=%v) = %v, want %v", s, self, up, got, want)
				}
			}
		}
	}
}

func TestAllowedUntrackWithDockerDownAndSelf(t *testing.T) {
	want := []Action{ActionUntrack}
	if got := Allowed(StatusRunning, true, true); !reflect.DeepEqual(got, want) {
		t.Errorf("self: got %v, want %v", got, want)
	}
	if got := Allowed(StatusUnknown, false, false); !reflect.DeepEqual(got, want) {
		t.Errorf("docker down: got %v, want %v", got, want)
	}
	if got := Allowed(StatusNotFound, true, false); !reflect.DeepEqual(got, []Action{ActionRemove}) {
		t.Errorf("notfound: got %v, want [remove]", got)
	}
	if ActionUntrack != "untrack" || ActionRemove != "remove" {
		t.Errorf("action values: %q %q", ActionUntrack, ActionRemove)
	}
}

func TestAllowedNeverNil(t *testing.T) {
	for _, s := range []Status{StatusUnknown, StatusStarting, StatusNotFound, "bogus"} {
		if Allowed(s, false, true) == nil {
			t.Errorf("Allowed(%q) is nil", s)
		}
	}
	if Allowed(StatusRunning, true, true) == nil || Allowed(StatusRunning, false, false) == nil {
		t.Error("Allowed with self or docker down is nil")
	}
}

func TestInFlightAndWord(t *testing.T) {
	for a, want := range map[Action]Status{
		ActionStart: StatusStarting, ActionStop: StatusStopping, ActionRestart: StatusRestarting,
	} {
		if got := InFlight(a); got != want {
			t.Errorf("InFlight(%q) = %q, want %q", a, got, want)
		}
	}
	for s, want := range map[Status]string{
		StatusRunning: "Running", StatusPartial: "Partial", StatusStopped: "Stopped",
		StatusError: "Error", StatusStarting: "Starting…", StatusStopping: "Stopping…",
		StatusRestarting: "Restarting…", StatusNotFound: "Not found", StatusUnknown: "Unknown",
	} {
		if got := Word(s); got != want {
			t.Errorf("Word(%q) = %q, want %q", s, got, want)
		}
	}
	if got := Word("bogus"); got != "Unknown" {
		t.Errorf("Word(bogus) = %q, want Unknown", got)
	}
}
