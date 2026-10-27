//go:build !webassets

package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSourceOnlyBuildReportsMissingUI(t *testing.T) {
	for _, path := range []string{"/", "/assets/missing.js"} {
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "-tags webassets") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("missing UI should be explicit: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestSourceOnlyBuildPreservesRouteAndMethodGuards(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/v1/missing", 404}, {"GET", "/health/missing", 404}, {"POST", "/", 405}, {"HEAD", "/", 503},
	} {
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.want {
			t.Errorf("%s %s: got %d; want %d", tc.method, tc.path, w.Code, tc.want)
		}
	}
}
