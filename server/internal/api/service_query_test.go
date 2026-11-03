package api

import (
	"net/http"
	"testing"
)

func TestServicesRejectIgnoredQueryParameters(t *testing.T) {
	f := newFixture(t)
	for _, query := range []string{"limit=abc", "limit=1", "cursor=1", "q=x", "limit=1&limit=2"} {
		w := f.call("GET", "/api/v1/services?"+query, "", f.cookie, "", "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("ignored %s: %d %s", query, w.Code, w.Body)
		}
	}
	w := f.call("GET", "/api/v1/services", "", f.cookie, "", "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body)
	}
}
