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

// newHandler serves files from fsys. Asset requests are paths under /assets/
// and single top-level segments with a dot (such as /favicon.png): a missing
// asset is a 404 rather than HTML. Every other path that names no file falls
// back to index.html so client-side routes survive a reload, including routes
// whose id has a dot (such as /projects/example.com).
func newHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean("/" + r.URL.Path)
		name := strings.TrimPrefix(clean, "/")

		if name != "" && name != "index.html" {
			if info, err := fs.Stat(fsys, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(clean, assetsPrefix) {
					// Vite hashes asset file names, so they never change.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			if isAssetPath(clean) {
				http.NotFound(w, r)
				return
			}
		}

		serveIndex(w, fsys)
	})
}

const assetsPrefix = "/assets/"

// isAssetPath reports whether the cleaned path is an asset request: under
// /assets/, or a single top-level segment containing a dot.
func isAssetPath(clean string) bool {
	if strings.HasPrefix(clean, assetsPrefix) {
		return true
	}
	segment := strings.TrimPrefix(clean, "/")
	return !strings.Contains(segment, "/") && strings.Contains(segment, ".")
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
