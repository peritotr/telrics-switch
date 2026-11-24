// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// WithPanel serves the built web panel from dir next to the API: API paths
// go to h, existing files are served as they are, and any other path gets
// index.html so that the panel's own routes survive a reload.
func WithPanel(h http.Handler, dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/healthz" {
			h.ServeHTTP(w, r)
			return
		}
		name := filepath.Join(dir, filepath.FromSlash(filepath.Clean("/"+r.URL.Path)))
		if st, err := os.Stat(name); err != nil || st.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
}
