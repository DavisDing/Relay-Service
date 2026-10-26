package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"relayservice/internal/storage"
	"strings"
	"testing"
	"time"
)

type testCryptor struct{ value string }

func (c testCryptor) Encrypt(s string) (string, error) { return "cipher:" + s, nil }
func (c testCryptor) Decrypt(s string) (string, error) {
	if s != "cipher:"+c.value {
		return "", ErrValidation
	}
	return c.value, nil
}

func TestSavePersistsCiphertextWithoutExposingIt(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	defer store.Close()
	engine := New(store, testCryptor{value: "monitor-token"})
	s, err := engine.Save(ctx, "admin", ServiceInput{Name: "local", Kind: KindHTTP, BaseURL: "http://127.0.0.1:4321", Enabled: true, AllowPrivate: true, Token: "monitor-token"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.CredentialConfigured {
		t.Fatal("credential state missing")
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "cipher:") || strings.Contains(string(b), "monitor-token") {
		t.Fatalf("service response leaked credential: %s", b)
	}
	var got serviceRecord
	if err := store.View(ctx, func(tx storage.Tx) error { return tx.Get(servicesBucket, s.ID, &got) }); err != nil {
		t.Fatal(err)
	}
	if got.Cipher != "cipher:monitor-token" {
		t.Fatalf("persisted ciphertext=%q", got.Cipher)
	}
}

func TestAdapterUsesFixedReadOnlyEndpointAndNoInference(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ kind, path string }{
		{KindLiteLLM, "/health/liveliness"}, {KindHermes, "/health"}, {KindWorkbuddy2API, "/health"},
		{KindOpenWebUI, "/health"}, {KindOllama, "/api/version"}, {KindMetaCubeXD, "/api/control/health"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			got := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = true
				if r.Method != http.MethodGet || r.URL.Path != tc.path || r.URL.RawQuery != "" {
					t.Errorf("unexpected request %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
				}
				if r.URL.Path == "/v1/chat/completions" || strings.Contains(r.URL.Path, "generate") {
					t.Error("inference endpoint was called")
				}
				if r.Header.Get("Authorization") != "Bearer safe-test-token" {
					t.Errorf("authorization header missing")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.kind == KindLiteLLM {
					_ = json.NewEncoder(w).Encode("I\u0027m alive!")
				} else if tc.kind == KindOllama {
					_, _ = w.Write([]byte(`{"version":"0.5.0"}`))
				} else {
					_, _ = w.Write([]byte(`{"status":"ok"}`))
				}
			}))
			defer srv.Close()
			u := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
			parsed, err := validatedURL(u)
			if err != nil {
				t.Fatal(err)
			}
			rec := serviceRecord{Service: Service{ID: "svc", Kind: tc.kind, BaseURL: parsed.String(), AllowPrivate: true, TimeoutSeconds: 2}, Cipher: "cipher:safe-test-token"}
			store := storage.NewMemory()
			defer store.Close()
			result := New(store, testCryptor{value: "safe-test-token"}).execute(ctx, rec)
			if !got || !result.success || result.status != StatusHealthy {
				t.Fatalf("observation=%+v requested=%v", result, got)
			}
		})
	}
}

func TestRedirectAndBadAuthAreNotReportedAsTargetDown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{{"redirect", http.StatusFound, StatusUnsupported}, {"unauthorized", http.StatusUnauthorized, StatusUnauthorized}, {"forbidden", http.StatusForbidden, StatusUnauthorized}, {"not_found", http.StatusNotFound, StatusUnsupported}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }))
			defer srv.Close()
			base := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
			rec := serviceRecord{Service: Service{ID: "svc", Kind: KindLiteLLM, BaseURL: base, AllowPrivate: true, TimeoutSeconds: 2}}
			store := storage.NewMemory()
			defer store.Close()
			got := New(store, nil).execute(context.Background(), rec)
			if got.status != tc.want || got.success {
				t.Fatalf("got %+v want status %s", got, tc.want)
			}
		})
	}
}

func TestUnsupportedAndUnauthorizedDoNotOpenFailureIncident(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	defer store.Close()
	engine := New(store, nil)
	s, err := engine.Save(ctx, "admin", ServiceInput{Name: "service", Kind: KindHTTP, BaseURL: "http://127.0.0.1:4321", Enabled: true, AllowPrivate: true, FailureThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{StatusUnauthorized, StatusUnsupported} {
		if _, err := engine.RequestCheck(ctx, s.ID, string(rune('a'+i)), s.Generation); err != nil {
			t.Fatal(err)
		}
		c, _, found, err := engine.claim(ctx)
		if err != nil || !found {
			t.Fatalf("claim found=%v err=%v", found, err)
		}
		c.Status = status
		c.Class = "test"
		c.Message = "restriction"
		c.CompletedAt = time.Now().UTC()
		if err := engine.finish(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	updated, err := engine.Get(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.OpenIncident || updated.FailureCount != 0 || !updated.OpenRestriction {
		t.Fatalf("restriction counted as outage: %+v", updated)
	}
	alerts, err := engine.Alerts(ctx, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range alerts {
		if a.Kind == "failure" {
			t.Fatalf("restriction emitted outage alert: %+v", a)
		}
	}
}
