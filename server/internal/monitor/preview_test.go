package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relayservice/internal/storage"
)

func TestPreviewDoesNotPersistAndBindsExistingCredentialToOrigin(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	defer store.Close()
	engine := New(store, testCryptor{value: "preview-secret"})
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/models" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer preview-secret" {
			t.Error("unexpected preview request")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"model-a","owned_by":"PRIVATE-OWNER"}],"key":"preview-secret"}`))
	}))
	defer upstream.Close()
	input := ServiceInput{Name: "preview", Kind: KindOpenAI, BaseURL: upstream.URL, AllowPrivate: true, Token: "preview-secret"}
	before, _ := store.DataVersion(ctx)
	check, err := engine.Preview(ctx, input)
	if err != nil || !check.Success || len(check.Details.Models) != 1 {
		t.Fatalf("preview=%+v err=%v", check, err)
	}
	after, _ := store.DataVersion(ctx)
	if before != after {
		t.Fatalf("preview persisted data: %d -> %d", before, after)
	}
	b, _ := json.Marshal(check)
	if strings.Contains(string(b), "preview-secret") || strings.Contains(string(b), "PRIVATE-OWNER") {
		t.Fatal("preview leaked upstream data")
	}
	saved, err := engine.Save(ctx, "admin", input)
	if err != nil {
		t.Fatal(err)
	}
	input.ID, input.Generation, input.Token = saved.ID, saved.Generation, ""
	if result, err := engine.Preview(ctx, input); err != nil || !result.Success {
		t.Fatalf("reuse saved credential: %+v %v", result, err)
	}
	input.BaseURL = "http://127.0.0.1:1"
	if _, err := engine.Preview(ctx, input); !errors.Is(err, ErrValidation) {
		t.Fatalf("forwarded credential to changed origin: %v", err)
	}
	input.BaseURL, input.Generation = upstream.URL, saved.Generation+1
	if _, err := engine.Preview(ctx, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale edit allowed: %v", err)
	}
	input.Generation, input.AllowPrivate = saved.Generation, false
	if result, err := engine.Preview(ctx, input); err != nil || result.Class != "target_blocked" {
		t.Fatalf("SSRF guard bypass: %+v %v", result, err)
	}
	if calls != 2 {
		t.Fatalf("unexpected upstream calls: %d", calls)
	}
	history, _ := engine.History(ctx, saved.ID, "", 100)
	if len(history) != 0 {
		t.Fatal("preview wrote history")
	}
}

func TestDetailedAdaptersProjectOnlySupportedFieldsAndNeverInvokeModels(t *testing.T) {
	for _, tc := range []struct{ kind, path, body, class string }{
		{KindMihomo, "/providers/proxies", `{"providers":{"subscription":{"subscriptionInfo":{"Upload":12,"Download":34,"Total":1000,"Expire":4102444800},"url":"https://PRIVATE@host/sub","proxies":[{"name":"node-a","type":"ss","password":"PRIVATE"}]},"no-info":{}}}`, "subscription_metadata"},
		{KindOpenAI, "/v1/models", `{"data":[{"id":"model-a","owned_by":"PRIVATE"}]}`, "model_catalog"},
		{KindStatuspage, "/api/v2/summary.json", `{"components":[{"name":"Model API","status":"operational","description":"PRIVATE"}]}`, "provider_reported"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.kind == KindMihomo && r.URL.Path == "/proxies" {
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(`{"proxies":{}}`))
					return
				}
				if r.Method != "GET" || r.URL.RequestURI() != tc.path {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			engine := New(storage.NewMemory(), nil)
			result := engine.execute(context.Background(), serviceRecord{Service: Service{Kind: tc.kind, BaseURL: srv.URL, AllowPrivate: true, TimeoutSeconds: 2}})
			if !result.success || result.class != tc.class || result.details == nil {
				t.Fatalf("result=%+v", result)
			}
			b, _ := json.Marshal(result.details)
			if strings.Contains(string(b), "PRIVATE") {
				t.Fatal("unapproved upstream fields persisted")
			}
		})
	}
}

func TestDetailFailuresUnknownsAndTruncation(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct{ kind, body, status string }{
		{KindMihomo, `{"providers":{"a":{"subscriptionInfo":{"Expire":1700000000}}}}`, StatusUnhealthy},
		{KindMihomo, `{"providers":{"a":{"subscriptionInfo":{"Total":10,"Upload":4,"Download":6}}}}`, StatusUnhealthy},
		{KindMihomo, `{"providers":{"a":{}}}`, StatusHealthy},
		{KindMihomo, `{"providers":{"a":{"subscriptionInfo":{"Total":-1}}}}`, StatusUnsupported},
		{KindOpenAI, `{"data":[{"id":"x"},{"id":"x"}]}`, StatusUnsupported},
		{KindOpenAI, `{"status":"ok"}`, StatusUnsupported},
		{KindStatuspage, `{"components":[{"name":"API","status":"major_outage"}]}`, StatusUnhealthy},
		{KindStatuspage, `{"components":[{"name":"API","status":"new-unknown-state"}]}`, StatusUnsupported},
		{KindStatuspage, `{"components":[]}`, StatusUnsupported},
	} {
		result := detailObservation(tc.kind, []byte(tc.body), now)
		if result.status != tc.status {
			t.Fatalf("kind=%s input=%s result=%+v", tc.kind, tc.body, result)
		}
	}
	var models []map[string]string
	for i := 0; i < 201; i++ {
		models = append(models, map[string]string{"id": strings.Repeat("a", i+1)})
	}
	b, _ := json.Marshal(map[string]any{"data": models})
	result := detailObservation(KindOpenAI, b, now)
	if !result.success || !result.details.Truncated || len(result.details.Models) != 200 || result.details.Count != 201 {
		t.Fatalf("truncation=%+v", result)
	}
}

func TestReflectedCredentialIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":[{"id":"reflected-secret"}]}`)) }))
	defer srv.Close()
	engine := New(storage.NewMemory(), testCryptor{value: "reflected-secret"})
	result, err := engine.Preview(context.Background(), ServiceInput{Name: "reflection", Kind: KindOpenAI, BaseURL: srv.URL, AllowPrivate: true, Token: "reflected-secret"})
	if err != nil || result.Status != StatusUnsupported || result.Details != nil || strings.Contains(result.Message, "reflected-secret") {
		t.Fatalf("credential reflected: %+v %v", result, err)
	}
}

func TestDetailedObservationsPersistInHistoryAndDriveIncidents(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	defer store.Close()
	e := New(store, nil)
	s, err := e.Save(ctx, "admin", ServiceInput{Name: "status", Kind: KindStatuspage, BaseURL: "https://example.invalid", Enabled: true, FailureThreshold: 1, RecoveryThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RequestCheck(ctx, s.ID, "details-check", s.Generation); err != nil {
		t.Fatal(err)
	}
	c, _, found, err := e.claim(ctx)
	if err != nil || !found {
		t.Fatalf("claim=%v %v", found, err)
	}
	result := detailObservation(KindStatuspage, []byte(`{"components":[{"name":"Model A","status":"major_outage"}]}`), e.now())
	c.Status, c.Success, c.Message, c.Class, c.Details = result.status, result.success, result.message, result.class, result.details
	c.CompletedAt = e.now()
	if err := e.finish(ctx, c); err != nil {
		t.Fatal(err)
	}
	current, err := e.Get(ctx, s.ID)
	if err != nil || !current.OpenIncident || current.Current.Details.Components[0].Name != "Model A" {
		t.Fatalf("current=%+v %v", current, err)
	}
	history, err := e.History(ctx, s.ID, "", 10)
	if err != nil || len(history) != 1 || history[0].Details.Components[0].Status != "major_outage" {
		t.Fatalf("history=%+v %v", history, err)
	}
	b, _ := json.Marshal(Check{DurationMS: 0})
	if !strings.Contains(string(b), `"duration_ms":0`) {
		t.Fatal("measured zero duration omitted")
	}
}
