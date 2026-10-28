package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"relayservice/internal/access"
	"relayservice/internal/storage"
)

func TestAutomaticOriginUsesRequestHostAndIgnoresForwardedHeaders(t *testing.T) {
	store := storage.NewMemory()
	defer store.Close()
	a, _ := access.New(store, make([]byte, 32), "", false)
	for _, tc := range []struct {
		host, origin, forwarded string
		secure                  bool
		want                    int
	}{
		{"192.168.1.20:8787", "http://192.168.1.20:8787", "", false, 401},
		{"nas.local:8888", "http://nas.local:8888", "", false, 401},
		{"[fd00::20]:8787", "http://[fd00::20]:8787", "", false, 401},
		{"relay.example.test", "https://relay.example.test", "", true, 401},
		{"nas.local:8787", "https://attacker.test", "attacker.test", false, 403},
		{"nas.local:8787", "http://nas.local:9999", "", false, 403},
		{"nas.local:8787", "", "", false, 403},
		{"nas.local:8787", "null", "", false, 403},
		{"nas.local:8787", "https://nas.local:8787", "", false, 403},
	} {
		h := New(store, a, nil, Options{SecureCookies: tc.secure})
		r := httptest.NewRequest("POST", "http://"+tc.host+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong password for test"}`))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Forwarded-Host", tc.forwarded)
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("host=%q origin=%q status=%d want=%d body=%s", tc.host, tc.origin, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestTemporaryLoginIsRestrictedUntilPermanentPasswordIsSet(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	defer store.Close()
	a, _ := access.New(store, make([]byte, 32), "", false)
	initial := strings.Repeat("a", 64)
	if _, err := a.EnsureInitialAdministrator(ctx, initial); err != nil {
		t.Fatal(err)
	}
	h := New(store, a, nil, Options{})
	call := func(method, path, body string, cookies []*http.Cookie, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://nas.local:8787"+path, strings.NewReader(body))
		r.Header.Set("Origin", "http://nas.local:8787")
		for _, c := range cookies {
			r.AddCookie(c)
			if csrf && c.Name == "relay_csrf" {
				r.Header.Set("X-Relay-CSRF", c.Value)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := call("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+initial+`"}`, nil, false)
	if login.Code != 200 {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	me := call("GET", "/api/v1/auth/me", "", cookies, false)
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"must_change_password":true`) {
		t.Fatalf("me: %d %s", me.Code, me.Body.String())
	}
	for _, path := range []string{"/services", "/alerts", "/devices", "/diagnostic"} {
		if w := call("GET", "/api/v1"+path, "", cookies, false); w.Code != 403 {
			t.Fatalf("temporary session accessed %s: %d", path, w.Code)
		}
	}
	body := `{"username":"owner","password":"new permanent password"}`
	if w := call("POST", "/api/v1/auth/password", body, cookies, false); w.Code != 403 {
		t.Fatalf("password change bypassed CSRF: %d", w.Code)
	}
	if w := call("POST", "/api/v1/auth/password", body, cookies, true); w.Code != 200 {
		t.Fatalf("password change: %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "/api/v1/auth/me", "", cookies, false); w.Code != 401 {
		t.Fatalf("temporary session survived: %d", w.Code)
	}
	if w := call("POST", "/api/v1/auth/login", body, nil, false); w.Code != 200 {
		t.Fatalf("permanent login: %d", w.Code)
	}
}

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
