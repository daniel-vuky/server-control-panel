package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/scan"
)

// sseHeartbeat is how often an open event stream sends a ": ping" comment, so
// proxies keep an idle stream open. Tests shorten it.
var sseHeartbeat = 25 * time.Second

// sseWriter writes server-sent events to one response, flushing each.
type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// startSSE sends the 200 and the event-stream headers.
func startSSE(w http.ResponseWriter) *sseWriter {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	return &sseWriter{w: w, rc: http.NewResponseController(w)}
}

// send writes one event with data encoded as JSON.
func (s *sseWriter) send(event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return err
	}
	return s.rc.Flush()
}

// ping writes the heartbeat comment.
func (s *sseWriter) ping() error {
	if _, err := fmt.Fprint(s.w, ": ping\n\n"); err != nil {
		return err
	}
	return s.rc.Flush()
}

// sseEnd is the data of the final "end" event.
type sseEnd struct {
	State string `json:"state"`
}

// scanEvents is GET /api/scans/{id}/events: the scan's progress as
// server-sent events. It sends the current progress, then each further
// progress event, then one "end" event with the final state, and returns. A
// job that is no longer running gets its stored progress and "end" at once.
func (s *server) scanEvents(w http.ResponseWriter, r *http.Request) {
	job, ok := s.loadJob(w, r)
	if !ok {
		return
	}
	snapshot, events, cancel, live := s.Scans.Subscribe(job.ID)
	defer cancel()
	if !live && job.State == stateRunning {
		var err error
		if job, err = s.settle(r.Context(), job.ID); err != nil {
			s.internalError(w, r, err)
			return
		}
	}

	sw := startSSE(w)
	if !live {
		if sw.send(scan.EventProgress, storedProgress(job)) == nil {
			_ = sw.send(scan.EventEnd, sseEnd{State: job.State})
		}
		return
	}
	if sw.send(scan.EventProgress, toProgress(snapshot)) != nil {
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
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Kind == scan.EventEnd {
				_ = sw.send(scan.EventEnd, sseEnd{State: ev.State})
				return
			}
			if sw.send(scan.EventProgress, toProgress(ev.Progress)) != nil {
				return
			}
		}
	}
}
