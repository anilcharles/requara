package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebUI(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o644)
	os.WriteFile(filepath.Join(dir, "assets", "app-1a2b.js"), []byte("console.log(1)"), 0o644)
	os.WriteFile(filepath.Join(dir, "favicon.svg"), []byte("<svg/>"), 0o644)
	h := webUI(dir)

	get := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, path, nil))
		return rec
	}
	cases := []struct {
		path, want, cache string
		code              int
	}{
		{"/", "app", "no-cache", 200},
		{"/apis/123", "app", "no-cache", 200},                // client-side route
		{"/assets/app-1a2b.js", "console", "immutable", 200}, // hashed asset
		{"/favicon.svg", "<svg/>", "no-cache", 200},
		{"/assets/missing.js", "", "", 404},                // never index.html for a missing asset
		{"/../../etc/passwd", "invalid URL path", "", 400}, // refused, never escapes dir
	}
	for _, c := range cases {
		rec := get("GET", c.path)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) || !strings.Contains(rec.Header().Get("Cache-Control"), c.cache) {
			t.Errorf("%s: %d %q cache=%q", c.path, rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
		}
		if c.code == 200 && rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: missing security headers", c.path)
		}
	}
	if rec := get("POST", "/apis"); rec.Code != http.StatusNotFound {
		t.Errorf("POST should not serve the app, got %d", rec.Code)
	}
}
