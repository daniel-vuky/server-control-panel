package project

// Action is a command the panel can run on a project: a lifecycle command
// (start, stop, restart) or a change to the panel's own list (untrack, remove).
type Action string

// The actions.
const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	// ActionUntrack stops tracking a project; its files and containers stay.
	ActionUntrack Action = "untrack"
	// ActionRemove drops a Not found project from the list.
	ActionRemove Action = "remove"
)

// Allowed lists the actions a project may take, in the order start, stop,
// restart, untrack. A Not found project allows only remove, and a project that
// is starting, stopping or restarting allows none. For every other status the
// lifecycle actions are empty when the project is the panel's own (self) or
// Docker is unreachable (!dockerUp), and untrack is always allowed last, since
// it touches only the panel's list. The result is never nil, so it encodes as
// [] in JSON.
func Allowed(s Status, self, dockerUp bool) []Action {
	switch s {
	case StatusNotFound:
		return []Action{ActionRemove}
	case StatusStarting, StatusStopping, StatusRestarting:
		return []Action{}
	}
	acts := []Action{}
	if !self && dockerUp {
		switch s {
		case StatusRunning:
			acts = append(acts, ActionStop, ActionRestart)
		case StatusPartial:
			acts = append(acts, ActionStart, ActionStop, ActionRestart)
		case StatusStopped:
			acts = append(acts, ActionStart)
		case StatusError:
			acts = append(acts, ActionStart, ActionRestart)
		}
	}
	return append(acts, ActionUntrack)
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
