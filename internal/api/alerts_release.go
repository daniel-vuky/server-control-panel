//go:build !dev

package api

import "net/http"

// registerAlertsDevRoutes adds nothing in a release build: there is no dev
// outbox.
func registerAlertsDevRoutes(*http.ServeMux, *server, func(http.Handler) http.Handler) {}
