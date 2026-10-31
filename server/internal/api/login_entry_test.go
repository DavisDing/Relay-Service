package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFixedLoginEntryIsDiscoverableWithoutRelaxingOrigin(t *testing.T) {
	f := newFixture(t)
	h := New(f.store, f.access, f.monitor, Options{AllowedOrigin: "https://relay.example.test:16666", SecureCookies: true})
	info := httptest.NewRecorder()
	h.ServeHTTP(info, httptest.NewRequest("GET", "/api/v1/info", nil))
	var entry struct {
		Data struct {
			LoginURL string `json:"login_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(info.Body.Bytes(), &entry); err != nil || info.Code != 200 || entry.Data.LoginURL != "https://relay.example.test:16666" {
		t.Fatalf("missing entry guidance: %d %s", info.Code, info.Body.String())
	}
	for _, origin := range []string{"http://relay:8787", "https://attacker.example.test"} {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"not-submitted"}`))
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"origin_rejected"`) || !strings.Contains(w.Body.String(), `"login_url":"https://relay.example.test:16666"`) || len(w.Result().Cookies()) > 0 {
			t.Fatalf("origin restriction bypassed or guidance absent: %s", w.Body.String())
		}
	}
}
