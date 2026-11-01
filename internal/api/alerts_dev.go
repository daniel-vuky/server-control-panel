//go:build dev

package api

import "net/http"

// registerAlertsDevRoutes adds the dev outbox: what the fake senders
// delivered (dev build only).
func registerAlertsDevRoutes(mux *http.ServeMux, s *server, protect func(http.Handler) http.Handler) {
	mux.Handle("GET /api/alerts/dev-outbox", protect(http.HandlerFunc(s.alertsDevOutbox)))
}

// alertsDevOutbox is GET /api/alerts/dev-outbox: the newest 50 messages the
// fake senders delivered, oldest first, each time in the server zone.
func (s *server) alertsDevOutbox(w http.ResponseWriter, _ *http.Request) {
	entries := s.Alerts.Outbox()
	out := make([]AlertOutboxEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, AlertOutboxEntry{At: s.zoneTime(e.At), Channel: e.Channel, Text: e.Text})
	}
	writeJSON(w, http.StatusOK, out)
}
