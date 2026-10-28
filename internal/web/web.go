// Package web serves the embedded single-page app.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist is the Vite build output. The "all:" prefix keeps dist/.gitkeep, so the
// directory always exists and the package compiles before the first web build.
//
//go:embed all:dist
var dist embed.FS

// Handler serves the embedded single-page app.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Unreachable: "dist" is embedded above, so the sub-tree always exists.
		panic("web: " + err.Error())
	}
	return newHandler(sub)
}

// newHandler serves files from fsys. Paths that name no file fall back to
// index.html so client-side routes survive a reload, except paths whose last
// segment has an extension: those are asset requests, and a missing asset must
// be a 404 rather than HTML.
func newHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")

		if name != "" && name != "index.html" {
			if info, err := fs.Stat(fsys, name); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
			if strings.Contains(path.Base(name), ".") {
				http.NotFound(w, r)
				return
			}
		}

		serveIndex(w, fsys)
	})
}

func serveIndex(w http.ResponseWriter, fsys fs.FS) {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.Error(w, "web app not built: run `make build`", http.StatusServiceUnavailable)
		return
	}
	// index.html names the hashed asset files, so it must always be revalidated.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}
