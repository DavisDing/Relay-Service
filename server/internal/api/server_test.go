package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relayservice/internal/access"
	"relayservice/internal/monitor"
	"relayservice/internal/storage"
)

func TestHealthRoutes(t *testing.T) {
	store := storage.NewMemory()
	defer store.Close()
	a, err := access.New(store, make([]byte, 32), "", false)
	if err != nil {
		t.Fatal(err)
	}
	h := New(store, a, nil, Options{})
	for _, tc := range []struct{ path, want string }{{"/health/live", "live"}, {"/health/ready", "ready"}} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			var got map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got["status"] != tc.want {
				t.Fatalf("status value %q", got["status"])
			}
		})
	}
}

type fixture struct {
	http.Handler
	store   storage.Store
	access  *access.Service
	monitor *monitor.Engine
	cookie  *http.Cookie
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	store := storage.NewMemory()
	t.Cleanup(store.Close)
	a, err := access.New(store, make([]byte, 32), "bootstrap-token-with-enough-entropy-123456", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Bootstrap(context.Background(), "bootstrap-token-with-enough-entropy-123456", "admin", "this is a test password"); err != nil {
		t.Fatal(err)
	}
	token, err := a.Login(context.Background(), "admin", "this is a test password")
	if err != nil {
		t.Fatal(err)
	}
	m := monitor.New(store, a)
	return fixture{Handler: New(store, a, m, Options{AllowedOrigin: "http://relay.local"}), store: store, access: a, monitor: m, cookie: &http.Cookie{Name: "relay_session", Value: token}}
}

func (f fixture) call(method, path, body string, cookie *http.Cookie, token, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if cookie != nil {
		r.AddCookie(&http.Cookie{Name: "relay_csrf", Value: "test-csrf-value"})
		r.Header.Set("X-Relay-CSRF", "test-csrf-value")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	return w
}

func TestInfoReportsInitializationWithoutConfiguration(t *testing.T) {
	store := storage.NewMemory()
	defer store.Close()
	a, _ := access.New(store, make([]byte, 32), "bootstrap-token-with-enough-entropy-123456", false)
	h := New(store, a, nil, Options{Version: "test-version"})
	for _, initialized := range []bool{false, true} {
		if initialized {
			if err := a.Bootstrap(context.Background(), "bootstrap-token-with-enough-entropy-123456", "admin", "this is a test password"); err != nil {
				t.Fatal(err)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/info", nil))
		var got struct {
			Data struct {
				Initialized bool   `json:"initialized"`
				Version     string `json:"version"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || got.Data.Initialized != initialized || got.Data.Version != "test-version" {
			t.Fatalf("info status %d: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "bootstrap-token") || strings.Contains(w.Body.String(), "username") {
			t.Fatal("public info exposed configuration")
		}
	}
}

func TestDevicePermissionsProjectionAndRevocation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s, err := f.monitor.Save(ctx, "admin", monitor.ServiceInput{Name: "gateway", Kind: "http", BaseURL: "http://127.0.0.1:4000", UIURL: "http://nas.local:4000", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, FailureThreshold: 3, RecoveryThreshold: 2, AllowPrivate: true, Token: "upstream-secret-123"})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := f.access.CreatePairing(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	w := f.call("POST", "/api/v1/devices/pair", `{"code":"`+pair.Code+`","name":"test device"}`, nil, "", "")
	if w.Code != 201 {
		t.Fatalf("pair status %d %s", w.Code, w.Body.String())
	}
	var paired struct {
		Data access.DeviceToken `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &paired); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/services", "/api/v1/services/" + s.ID} {
		w = f.call("GET", path, "", nil, paired.Data.Token, "")
		if w.Code != 200 {
			t.Fatalf("read status %d %s", w.Code, w.Body.String())
		}
		for _, hidden := range []string{"base_url", "allow_private", "failure_threshold", "credential_configured", "upstream-secret-123", "127.0.0.1"} {
			if strings.Contains(w.Body.String(), hidden) {
				t.Fatalf("device response contains %q: %s", hidden, w.Body.String())
			}
		}
	}
	for _, tc := range []struct{ method, path string }{{"POST", "/api/v1/services"}, {"PUT", "/api/v1/services/" + s.ID}, {"DELETE", "/api/v1/services/" + s.ID}, {"POST", "/api/v1/services/" + s.ID + "/checks"}, {"POST", "/api/v1/services/" + s.ID + "/silence"}, {"POST", "/api/v1/devices/pairings"}, {"GET", "/api/v1/devices"}, {"DELETE", "/api/v1/devices/any"}, {"GET", "/api/v1/diagnostic"}, {"POST", "/api/v1/auth/logout"}} {
		w = f.call(tc.method, tc.path, `{}`, nil, paired.Data.Token, "")
		if w.Code != 403 {
			t.Errorf("device %s %s status %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	w = f.call("POST", "/api/v1/alerts/ack", `{"cursor":0}`, nil, paired.Data.Token, "")
	if w.Code != 200 {
		t.Fatalf("own ack status %d %s", w.Code, w.Body.String())
	}
	w = f.call("POST", "/api/v1/alerts/ack", `{"cursor":0,"device_id":"another"}`, nil, paired.Data.Token, "")
	if w.Code != 400 {
		t.Fatalf("foreign ack status %d %s", w.Code, w.Body.String())
	}
	if err := f.access.Revoke(ctx, paired.Data.DeviceID); err != nil {
		t.Fatal(err)
	}
	w = f.call("GET", "/api/v1/services", "", nil, paired.Data.Token, "")
	if w.Code != 401 {
		t.Fatalf("revoked device status %d", w.Code)
	}
}

func TestCookieCannotBypassOriginUsingInvalidBearer(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		origin, bearer string
		status         int
	}{{"", "", 403}, {"https://evil.example", "", 403}, {"", "invalid", 403}, {"https://evil.example", "invalid", 403}} {
		w := f.call("POST", "/api/v1/devices/pairings", `{}`, f.cookie, tc.bearer, tc.origin)
		if w.Code != tc.status {
			t.Errorf("origin %q bearer %q: status %d %s", tc.origin, tc.bearer, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "http://relay.local/api/v1/devices/pairings", strings.NewReader(`{}`))
	r.AddCookie(f.cookie)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("X-Forwarded-Host", "evil.example")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("forwarded host spoof status %d", w.Code)
	}
	w = f.call("POST", "/api/v1/devices/pairings", `{}`, f.cookie, "", "http://relay.local")
	if w.Code != 201 {
		t.Fatalf("same origin status %d %s", w.Code, w.Body.String())
	}
}

func TestJSONLimitsAndValidation(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		name, body, content string
		status              int
	}{{"null", "null", "application/json", 400}, {"array", "[]", "application/json", 400}, {"unknown", `{"username":"admin","password":"valid password long","extra":true}`, "application/json", 400}, {"multiple", `{} {}`, "application/json", 400}, {"too_large", `{"password":"` + strings.Repeat("x", 65536) + `"}`, "application/json", 413}, {"media", `{}`, "text/plain", 415}} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(tc.body))
			r.Header.Set("Origin", "http://relay.local")
			r.Header.Set("Content-Type", tc.content)
			w := httptest.NewRecorder()
			f.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, limit := range []string{"0", "-1", "501", "wat"} {
		w := f.call("GET", "/api/v1/alerts?limit="+limit, "", f.cookie, "", "")
		if w.Code != 400 {
			t.Errorf("limit %s status %d", limit, w.Code)
		}
	}
}

type failedStore struct{ storage.Store }

func (s failedStore) View(context.Context, func(storage.Tx) error) error {
	return errors.New("postgres://admin:SECRET@db/internal")
}
func (s failedStore) Update(context.Context, func(storage.Tx) error) error {
	return errors.New("postgres://admin:SECRET@db/internal")
}
func TestStorageFailureDoesNotMasqueradeAsAuthentication(t *testing.T) {
	store := failedStore{storage.NewMemory()}
	defer store.Close()
	a, _ := access.New(store, make([]byte, 32), "", false)
	h := New(store, a, nil, Options{})
	r := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	r.Header.Set("Authorization", "Bearer any-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "postgres") {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestWriteRequiresAllowedSameOrigin(t *testing.T) {
	store := storage.NewMemory()
	defer store.Close()
	a, _ := access.New(store, make([]byte, 32), "", false)
	h := New(store, a, nil, Options{AllowedOrigin: "http://relay.local"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestLoginDoesNotReturnSessionInJSON(t *testing.T) {
	store := storage.NewMemory()
	defer store.Close()
	a, _ := access.New(store, make([]byte, 32), "bootstrap-token-with-enough-entropy-123456", false)
	h := New(store, a, nil, Options{AllowedOrigin: "http://relay.local"})
	body := `{"token":"bootstrap-token-with-enough-entropy-123456","username":"admin","password":"this is a test password"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/bootstrap", strings.NewReader(body))
	r.Header.Set("Origin", "http://relay.local")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("bootstrap status=%d body=%s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"this is a test password"}`))
	r.Header.Set("Origin", "http://relay.local")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing session cookie")
	}
	if strings.Contains(w.Body.String(), cookies[0].Value) {
		t.Fatal("session leaked in response")
	}
}
