// Package webui serves the compiled same-origin administration application.
package webui

import (
	"io/fs"
	"net/http"
	"strings"
)

func Handler() http.Handler {
	var files http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "Web UI unavailable: build web assets, then rebuild with -tags webassets", http.StatusServiceUnavailable)
	})
	if assets != nil {
		root, err := fs.Sub(assets, "dist")
		if err != nil {
			panic(err)
		}
		files = http.FileServer(http.FS(root))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/health/") {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") {
			w.Header().Set("Cache-Control", "no-store")
		} else if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		if applicationPath(r.URL.Path) {
			w.Header().Set("Cache-Control", "no-store")
			clone := r.Clone(r.Context())
			u := *r.URL
			u.Path = "/"
			u.RawPath = ""
			clone.URL = &u
			files.ServeHTTP(w, clone)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func applicationPath(path string) bool {
	switch strings.TrimSuffix(path, "/") {
	case "", "/services", "/alerts", "/devices", "/settings", "/diagnostic", "/models", "/models/models", "/models/channels", "/models/traffic", "/models/status", "/models/probes", "/network", "/network/usage", "/network/subscriptions", "/network/nodes", "/network/connections", "/network/logs", "/logs", "/logs/services", "/logs/models", "/logs/network":
		return true
	}
	return false
}
