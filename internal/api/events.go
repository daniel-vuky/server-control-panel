package api

import (
	"net/http"
	"time"
)

// eventState is the name of the live state event.
const eventState = "state"

// events is GET /api/events: the live state as server-sent events (ADR 0003).
// It sends a state event on connect and then each time the state changes,
// plus a "ping" event every sseHeartbeat, until the request ends.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	// Start the stream first: Subscribe may run a fresh tick, which can take
	// a while when Docker hangs.
	sw := startSSE(w)
	if sw.rc.Flush() != nil {
		return
	}
	current, updates, cancel := s.Hub.Subscribe()
	defer cancel()

	if sw.send(eventState, toLiveState(current)) != nil {
		return
	}
	ping := time.NewTicker(sseHeartbeat)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if sw.ping() != nil {
				return
			}
		case st := <-updates:
			if sw.send(eventState, toLiveState(st)) != nil {
				return
			}
		}
	}
}

// dockerCheck is POST /api/docker/check: it checks Docker at once (or waits
// for the check in progress), broadcasts the state when it changed, and
// answers whether Docker is up.
func (s *server) dockerCheck(w http.ResponseWriter, r *http.Request) {
	st := s.Hub.Check(r.Context())
	writeJSON(w, http.StatusOK, DockerCheck{DockerUp: st.DockerUp})
}
