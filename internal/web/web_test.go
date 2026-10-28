package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<html>spa</html>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestServesExistingAsset(t *testing.T) {
	rec := get(newHandler(testFS()), "/assets/app.js")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "console.log('app')" {
		t.Errorf("body = %q, want the asset", got)
	}
}

func TestFallsBackToIndexForRoutes(t *testing.T) {
	h := newHandler(testFS())

	for _, path := range []string{"/", "/backups", "/projects/shop-api"} {
		t.Run(path, func(t *testing.T) {
			rec := get(h, path)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Body.String(); got != "<html>spa</html>" {
				t.Errorf("body = %q, want index.html", got)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
				t.Errorf("Cache-Control = %q, want no-cache", cc)
			}
		})
	}
}

func TestMissingAssetIs404(t *testing.T) {
	h := newHandler(testFS())

	for _, path := range []string{"/assets/missing.js", "/favicon.png"} {
		t.Run(path, func(t *testing.T) {
			if rec := get(h, path); rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
	}
}

func TestNoIndexIs503(t *testing.T) {
	rec := get(newHandler(fstest.MapFS{}), "/")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "web app not built") {
		t.Errorf("body = %q, want it to mention \"web app not built\"", rec.Body.String())
	}
}
