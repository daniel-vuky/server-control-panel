//go:build !dev

package api

import "net/http"

// registerDevRoutes adds nothing in a release build: there is no fake Drive.
func registerDevRoutes(*http.ServeMux, *server, func(http.Handler) http.Handler) {}
