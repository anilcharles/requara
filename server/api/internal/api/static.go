package api

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// webUI serves the built React app from dir, so one Go program serves both
// the UI and the API from the same origin (the session cookie and the sign-in
// callback need that). Client-side routes (/apis/123, /settings, …) get
// index.html; a missing file under /assets/ is a real 404.
func webUI(dir string) http.HandlerFunc {
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		// Browser hardening for the app's pages.
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		p := path.Clean("/" + r.URL.Path)
		// The personal workspace's WebAssembly module is large; it ships
		// precompressed next to the original (requara-core.wasm.gz).
		if strings.HasPrefix(p, "/core/") && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			gz := filepath.Join(dir, filepath.FromSlash(p)) + ".gz"
			if fi, err := os.Stat(gz); err == nil && !fi.IsDir() {
				if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
					h.Set("Content-Type", ct)
				}
				h.Set("Content-Encoding", "gzip")
				h.Add("Vary", "Accept-Encoding")
				h.Set("Cache-Control", "no-cache")
				http.ServeFile(w, r, gz)
				return
			}
		}
		if p != "/" {
			fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p)))
			if err == nil && !fi.IsDir() {
				if strings.HasPrefix(p, "/assets/") {
					h.Set("Cache-Control", "public, max-age=31536000, immutable") // file names carry a content hash
				} else {
					h.Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(p, "/assets/") {
				http.NotFound(w, r)
				return
			}
		}
		h.Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	}
}
