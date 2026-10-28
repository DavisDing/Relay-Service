package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relayservice/internal/storage"
)

func TestPublicStatusPreviewConfiguredFormatsAndNoCredentialForwarding(t *testing.T) {
	for _, tc := range []struct {
		cfg  PublicStatusConfig
		body string
		want string
	}{
		{PublicStatusConfig{Format: "pipio"}, `{"success":true,"data":[{"categoryName":"模型","monitors":[{"name":"model-a","status":1,"uptime":0.99,"heartbeats":[1,0,1]}]}]}`, StatusHealthy},
		{PublicStatusConfig{Format: "json", ResourcePath: "/availability", ItemsPath: "data.services", NameField: "label", StatusField: "state", HealthyValues: []string{"ready"}, UnhealthyValues: []string{"failed"}}, `{"data":{"services":[{"label":"model-a","state":"failed"}]}}`, StatusUnhealthy},
		{PublicStatusConfig{Format: "html", ResourcePath: "/status", HTMLItemTag: "div", HTMLItemAttribute: "class", HTMLItemValue: "component", HTMLNameAttribute: "data-name", HTMLStatusAttribute: "data-state", HealthyValues: []string{"up"}, UnhealthyValues: []string{"down"}}, `<html><body><nav>up</nav><div class="component" data-name="api" data-state="up">API</div></body></html>`, StatusHealthy},
	} {
		t.Run(tc.cfg.Format, func(t *testing.T) {
			paths := []string{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.Method != "GET" || r.Header.Get("Authorization") != "" {
					t.Error("public status received credentials or mutation")
				}
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			store := storage.NewMemory()
			defer store.Close()
			engine := New(store, testCryptor{value: "private-secret"})
			input := ServiceInput{Name: "status", Kind: KindStatuspage, BaseURL: srv.URL, AllowPrivate: true, PublicStatus: &tc.cfg}
			before, _ := store.DataVersion(context.Background())
			result, err := engine.Preview(context.Background(), input)
			if err != nil || result.Status != tc.want || result.Details.PublicStatus.Count != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			after, _ := store.DataVersion(context.Background())
			if after != before {
				t.Fatal("preview persisted")
			}
			expected := tc.cfg.ResourcePath
			if expected == "" {
				expected = "/api/uptime/status"
			}
			if len(paths) != 1 || paths[0] != expected {
				t.Fatalf("paths %v", paths)
			}
			// Even a historical record with a credential must never send it to a public page.
			result2 := engine.execute(context.Background(), serviceRecord{Service: Service{ID: "old", Kind: KindStatuspage, BaseURL: srv.URL, AllowPrivate: true, TimeoutSeconds: 2, PublicStatus: &tc.cfg}, Cipher: "legacy-cipher"})
			if result2.status != tc.want {
				t.Fatalf("legacy %+v", result2)
			}
		})
	}
}

func TestCollectorSafeOriginLimitsAndNestedCredentialReflection(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{}`)) }))
	defer srv.Close()
	store := storage.NewMemory()
	defer store.Close()
	engine := New(store, nil)
	fetch, _, closeClient, err := engine.collector(context.Background(), serviceRecord{Service: Service{BaseURL: srv.URL, AllowPrivate: true, TimeoutSeconds: 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	for _, path := range []string{"https://other.invalid/x", "//other.invalid/x"} {
		if _, err := fetch(context.Background(), path); err == nil {
			t.Fatal("accepted remote path")
		}
	}
	for i := 0; i < 8; i++ {
		if _, err := fetch(context.Background(), "/status"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fetch(context.Background(), "/status"); err == nil || calls != 8 {
		t.Fatalf("request cap failed %d %v", calls, err)
	}
	if !reflectedCredential(&Details{PublicStatus: &PublicStatus{Components: []PublicStatusComponent{{Name: "secret-model"}}}}, "secret") {
		t.Fatal("nested credential escaped")
	}
	// Private consent applies to every collector request.
	denied, _, done, _ := engine.collector(context.Background(), serviceRecord{Service: Service{BaseURL: srv.URL, TimeoutSeconds: 2}})
	defer done()
	_, err = denied(context.Background(), "/status")
	var f *FetchError
	if !errors.As(err, &f) || f.Class != "target_blocked" || calls != 8 {
		t.Fatalf("private target accepted: %v", err)
	}
}

func TestStatusConfigPersistenceAndGenerationReset(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	defer store.Close()
	engine := New(store, nil)
	input := ServiceInput{Name: "public", Kind: KindStatuspage, BaseURL: "https://example.invalid", Enabled: true, PublicStatus: &PublicStatusConfig{Format: "pipio"}}
	saved, err := engine.Save(ctx, "admin", input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.PublicStatus.Format != "pipio" {
		t.Fatal("configuration lost")
	}
	if err := store.Update(ctx, func(tx storage.Tx) error {
		return tx.Put(stateBucket, saved.ID, state{Current: &Check{Status: StatusHealthy}})
	}); err != nil {
		t.Fatal(err)
	}
	input.ID, input.Generation = saved.ID, saved.Generation
	input.PublicStatus = &PublicStatusConfig{Format: "json", ResourcePath: "/other"}
	updated, err := engine.Save(ctx, "admin", input)
	if err != nil || updated.Current != nil || updated.Generation != saved.Generation+1 {
		t.Fatalf("state reused %+v %v", updated, err)
	}
	for _, bad := range []ServiceInput{
		{Name: "x", Kind: KindStatuspage, BaseURL: "https://example.invalid", Token: "secret"},
		{Name: "x", Kind: KindHTTP, BaseURL: "https://example.invalid", PublicStatus: &PublicStatusConfig{}},
		{Name: "x", Kind: KindWorkbuddy2API, BaseURL: "https://example.invalid", CollectLogs: true},
	} {
		if _, err := engine.Save(ctx, "admin", bad); !errors.Is(err, ErrValidation) {
			t.Fatal("unsupported capability accepted")
		}
	}
	b, _ := json.Marshal(updated)
	if strings.Contains(string(b), "cipher") {
		t.Fatal("credential output")
	}
}
