package project

// Action is a lifecycle command the panel can run on a project.
type Action string

// The lifecycle actions.
const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
)

// Allowed lists the actions a project may take, in the order start, stop,
// restart. The panel's own project (self) and an unreachable Docker (!dockerUp)
// allow none. The result is never nil, so it encodes as [] in JSON.
func Allowed(s Status, self, dockerUp bool) []Action {
	if self || !dockerUp {
		return []Action{}
	}
	switch s {
	case StatusRunning:
		return []Action{ActionStop, ActionRestart}
	case StatusPartial:
		return []Action{ActionStart, ActionStop, ActionRestart}
	case StatusStopped:
		return []Action{ActionStart}
	case StatusError:
		return []Action{ActionStart, ActionRestart}
	}
	return []Action{}
}

// InFlight is the status a project shows while action a runs.
func InFlight(a Action) Status {
	switch a {
	case ActionStart:
		return StatusStarting
	case ActionStop:
		return StatusStopping
	case ActionRestart:
		return StatusRestarting
	}
	return StatusUnknown
}

// Word is the status as a person reads it, used in server messages.
func Word(s Status) string {
	switch s {
	case StatusRunning:
		return "Running"
	case StatusPartial:
		return "Partial"
	case StatusStopped:
		return "Stopped"
	case StatusError:
		return "Error"
	case StatusStarting:
		return "Starting…"
	case StatusStopping:
		return "Stopping…"
	case StatusRestarting:
		return "Restarting…"
	case StatusNotFound:
		return "Not found"
	}
	return "Unknown"
}
