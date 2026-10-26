package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestCookieWritesRequireCSRF(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		name, header, cookie string
		want                 int
	}{
		{"missing", "", "", 403},
		{"header-only", "token", "", 403},
		{"cookie-only", "", "token", 403},
		{"mismatch", "first", "other", 403},
		{"valid", "token", "token", 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/devices/pairings", strings.NewReader(`{}`))
			r.AddCookie(f.cookie)
			r.Header.Set("Origin", "http://relay.local")
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "relay_csrf", Value: tc.cookie})
			}
			if tc.header != "" {
				r.Header.Set("X-Relay-CSRF", tc.header)
			}
			w := httptest.NewRecorder()
			f.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestEnvelopeUsesCommittedStorageDataVersion(t *testing.T) {
	f := newFixture(t)
	read := func() uint64 {
		w := f.call("GET", "/api/v1/info", "", nil, "", "")
		var value struct {
			DataVersion string `json:"data_version"`
		}
		if w.Code != 200 {
			t.Fatalf("info status %d %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		n, err := strconv.ParseUint(value.DataVersion, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := read()
	if repeated := read(); repeated != before {
		t.Fatalf("read-only request changed version: %d -> %d", before, repeated)
	}
	if _, err := f.access.CreatePairing(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	if after := read(); after <= before {
		t.Fatalf("mutation did not advance version: %d -> %d", before, after)
	}
}

func TestOriginMustBeAnOrigin(t *testing.T) {
	f := newFixture(t)
	for _, origin := range []string{"null", "http://relay.local/", "http://relay.local/path", "http://relay.local?query", "http://relay.local#fragment", "http://user@relay.local", "http://relay.local.evil.example"} {
		w := f.call("POST", "/api/v1/devices/pairings", `{}`, f.cookie, "", origin)
		if w.Code != 403 {
			t.Errorf("accepted origin %q with status %d", origin, w.Code)
		}
	}
}

func TestOriginAcceptsEquivalentDefaultPorts(t *testing.T) {
	for _, pair := range [][2]string{{"http://relay.local", "http://relay.local:80"}, {"https://relay.local", "https://RELAY.local:443"}, {"http://[::1]", "http://[::1]:80"}} {
		a, ok := parseOrigin(pair[0])
		if !ok {
			t.Fatal(pair[0])
		}
		b, ok := parseOrigin(pair[1])
		if !ok || a != b {
			t.Fatalf("origins differ: %q %q", pair[0], pair[1])
		}
	}
}

func TestBearerLogoutRevokesPresentedSession(t *testing.T) {
	f := newFixture(t)
	w := f.call("POST", "/api/v1/auth/logout", `{}`, nil, f.cookie.Value, "")
	if w.Code != 200 {
		t.Fatalf("logout status %d %s", w.Code, w.Body.String())
	}
	w = f.call("GET", "/api/v1/auth/me", "", nil, f.cookie.Value, "")
	if w.Code != 401 {
		t.Fatalf("session survived logout: %d", w.Code)
	}
}

func TestLoginSetsSeparateCSRFAndSessionCookies(t *testing.T) {
	f := newFixture(t)
	w := f.call("POST", "/api/v1/auth/login", `{"username":"admin","password":"this is a test password"}`, nil, "", "http://relay.local")
	if w.Code != 200 {
		t.Fatalf("login status %d %s", w.Code, w.Body.String())
	}
	var session, csrf *http.Cookie
	for _, c := range w.Result().Cookies() {
		switch c.Name {
		case "relay_session":
			session = c
		case "relay_csrf":
			csrf = c
		}
	}
	if session == nil || csrf == nil || !session.HttpOnly || csrf.HttpOnly || len(csrf.Value) != 64 || session.SameSite != http.SameSiteStrictMode || csrf.SameSite != http.SameSiteStrictMode {
		t.Fatal("invalid session/CSRF cookie policy")
	}
	if session.Value == csrf.Value || strings.Contains(w.Body.String(), csrf.Value) || strings.Contains(w.Body.String(), session.Value) {
		t.Fatal("credentials returned in JSON or not separate")
	}
	if _, err := f.access.Authenticate(context.Background(), csrf.Value); err == nil {
		t.Fatal("CSRF value authenticated as session")
	}
}
