package ui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec
}

func TestHandler(t *testing.T) {
	h := Handler(fstest.MapFS{
		"app/index.html":      {Data: []byte("<!doctype html><title>Tapyr</title>")},
		"app/assets/index.js": {Data: []byte("console.log(1)")},
		"app/favicon.svg":     {Data: []byte("<svg/>")},
		"README.md":           {Data: []byte("not served")},
	})
	for _, tc := range []struct {
		path, cache, body string
		status            int
	}{
		{"/ui/", "no-cache", "<title>Tapyr</title>", 200},
		{"/ui/index.html", "no-cache", "", 301}, // to /ui/
		{"/ui/assets/index.js", "public, max-age=31536000, immutable", "console.log(1)", 200},
		{"/ui/favicon.svg", "", "<svg/>", 200},
		{"/ui/nope.js", "", "", 404},
		{"/ui/../README.md", "", "", 404},
	} {
		rec := get(t, h, tc.path)
		if rec.Code != tc.status || rec.Header().Get("Cache-Control") != tc.cache || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%s: %d %q %q", tc.path, rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
		}
		if rec.Header().Get("Content-Security-Policy") != CSP {
			t.Errorf("%s: no CSP", tc.path)
		}
	}
	// Without a build, a page explains how to build the UI.
	rec := get(t, Handler(fstest.MapFS{"README.md": {Data: []byte("x")}}), "/ui/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "make ui") || rec.Header().Get("Content-Security-Policy") != CSP {
		t.Errorf("not built: %d %s", rec.Code, rec.Body.String())
	}
}

// TestNoExternalReferences checks the built UI, when present, for
// URLs other than the known non-network ones (XML namespaces, links to the
// documentation in React and React Flow error messages).
func TestNoExternalReferences(t *testing.T) {
	dir := filepath.Join("..", "..", "ui", "dist", "app")
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Skip("the UI is not built (make ui)")
	}
	allowed := regexp.MustCompile(`^https?://(www\.w3\.org/|react\.dev/|reactjs\.org/|reactflow\.dev|\$\{e\}flow\.dev/|xyflow\.com|github\.com/xyflow)`)
	url := regexp.MustCompile(`https?://[^\s"'()<>\\` + "`" + `]+`)
	err := fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".woff2") {
			return err
		}
		data, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			return err
		}
		for _, u := range url.FindAllString(string(data), -1) {
			if allowed.MatchString(u) {
				continue
			}
			t.Errorf("%s references %s", p, u)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
