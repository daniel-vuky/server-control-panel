package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/logs"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The events of GET /api/projects/{id}/logs.
const (
	eventLines = "lines"
	eventEnd   = "end"
)

// The refusals of GET /api/projects/{id}/logs, sent as its end event.
const (
	endUnknownService = "unknown_service"
	endNotFound       = "not_found"
	endDockerDown     = "docker_down"
	endTooMany        = "too_many_log_streams"
)

const (
	msgLogsUnknownService = "This project has no such service."
	msgLogsNotFound       = "The project folder is missing, so logs can't be read."
	msgLogsDockerDown     = "Docker is not responding, so logs can't be read."
	msgLogsTooMany        = "Too many log views are open on the panel. Close one and retry."
	msgLogsFailed         = "The logs could not be read."
)

// projectLogs is GET /api/projects/{id}/logs?service=<name> (no service =
// every service): the project's container output as server-sent events. An
// unknown or untracked id gets a plain 404. Every other refusal answers 200
// and sends one end event at once, because a browser EventSource cannot read
// a non-200 answer. Otherwise the first lines event is the backlog, then one
// per batch of live lines, and the end event when the stream ends; the
// server then closes the response. When the client goes away the viewer
// leaves the shared stream, which stops the follow when it was the last one.
// Reading logs changes nothing, so it is not recorded.
func (s *server) projectLogs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	}
	t, err := s.Store.TrackedProject(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	service := r.URL.Query().Get("service")
	var refusal LogEnd
	switch {
	case service != "" && !slices.Contains(t.Services, service):
		refusal = LogEnd{Reason: endUnknownService, Message: msgLogsUnknownService}
	case t.MissingSince != nil:
		refusal = LogEnd{Reason: endNotFound, Message: msgLogsNotFound}
	}
	if refusal.Reason != "" {
		_ = startSSE(w).send(eventEnd, refusal)
		return
	}

	sub, err := s.Logs.Subscribe(r.Context(), t, service)
	switch {
	case err == nil:
	case errors.Is(err, logs.ErrDockerDown):
		_ = startSSE(w).send(eventEnd, LogEnd{Reason: endDockerDown, Message: msgLogsDockerDown})
		return
	case errors.Is(err, logs.ErrTooMany):
		_ = startSSE(w).send(eventEnd, LogEnd{Reason: endTooMany, Message: msgLogsTooMany})
		return
	default:
		s.log.Error("logs: subscribe", "project", id, "err", err)
		_ = startSSE(w).send(eventEnd, LogEnd{Reason: logs.ReasonError, Message: msgLogsFailed})
		return
	}
	defer sub.Cancel()

	sw := startSSE(w)
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
		case batch, ok := <-sub.Lines:
			if !ok {
				// The stream ended for this viewer: End holds the reason.
				if end, ok := <-sub.End; ok {
					_ = sw.send(eventEnd, LogEnd{Reason: end.Reason, Message: end.Message})
				}
				return
			}
			if sw.send(eventLines, LogLines{Lines: toLogLines(batch)}) != nil {
				return
			}
		}
	}
}

// toLogLines maps a batch to the API type. The slice is never nil.
func toLogLines(batch []logs.Line) []LogLine {
	out := make([]LogLine, 0, len(batch))
	for _, l := range batch {
		out = append(out, LogLine{
			Container: l.Container,
			Service:   l.Service,
			Time:      l.Time.UTC().Format(time.RFC3339Nano),
			Text:      l.Text,
		})
	}
	return out
}
