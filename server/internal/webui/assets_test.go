//go:build webassets

package webui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestCompiledApplicationAndAssets(t *testing.T) {
	h := Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="root"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("compiled application is unavailable: %d %s", w.Code, w.Body.String())
	}
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"<>]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(assets) < 2 {
		t.Fatal("missing compiled script/styles; run web build")
	}
	for _, asset := range assets {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, asset[1], nil))
		if w.Code != 200 || w.Body.Len() == 0 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
			t.Fatalf("asset %s unavailable: %d", asset[1], w.Code)
		}
	}
}

func TestWebHandlerDoesNotServeAPIOrWrites(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/api/v1/missing", 404}, {"GET", "/health/missing", 404}, {"GET", "/missing.js", 404}, {"POST", "/", 405}, {"HEAD", "/", 200}} {
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.want {
			t.Errorf("%s %s: got %d", tc.method, tc.path, w.Code)
		}
	}
}

func TestDeepLinksServeShellAndUnknownRoutesStay404(t *testing.T) {
	for _, path := range []string{"/models", "/models/channels", "/network/subscriptions", "/network/nodes/", "/logs", "/logs/models", "/settings"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := httptest.NewRecorder()
			Handler().ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || method == "GET" && !strings.Contains(w.Body.String(), `id="root"`) {
				t.Fatalf("deep link %s %s: %d %s", method, path, w.Code, w.Body.String())
			}
		}
	}
	for _, path := range []string{"/models/missing", "/logs.js", "/assets/missing.js", "/api/v1/missing", "/health/missing"} {
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("SPA swallowed missing resource: %s %d", path, w.Code)
		}
	}
}
