package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"relayservice/internal/monitor"
	"strings"
	"testing"
)

func TestServicePreviewRequiresAdminAndWriteGuardsAndDoesNotSave(t *testing.T) {
	f := newFixture(t)
	body := `{"name":"preview","kind":"openai","base_url":"http://127.0.0.1:1","allow_private":false}`
	device := pairedToken(t, f, "read-only-preview")
	for _, tc := range []struct {
		cookie        bool
		token, origin string
		want          int
	}{
		{false, "", "http://relay.local", 401},
		{true, "", "http://attacker.local", 403},
		{false, device.Token, "http://relay.local", 403},
		{true, "", "http://relay.local", 200},
	} {
		cookie := f.cookie
		if !tc.cookie {
			cookie = nil
		}
		w := f.call(http.MethodPost, "/api/v1/services/test", body, cookie, tc.token, tc.origin)
		if w.Code != tc.want {
			t.Fatalf("preview guard status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
		}
		if w.Code == 200 && !strings.Contains(w.Body.String(), `"class":"target_blocked"`) {
			t.Fatalf("private target permitted: %s", w.Body.String())
		}
	}
	services, err := f.monitor.Services(context.Background())
	if err != nil || len(services) != 0 {
		t.Fatalf("preview saved service: %+v %v", services, err)
	}
}

func TestServicePreviewUsesRealScopedCryptorForNewAndSavedCredentials(t *testing.T) {
	f := newFixture(t)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer synthetic-preview-token" {
			t.Error("preview credential or endpoint missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer upstream.Close()
	input := monitor.ServiceInput{Name: "scope", Kind: monitor.KindOpenAI, BaseURL: upstream.URL, AllowPrivate: true, Token: "synthetic-preview-token"}
	test := func() {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		w := f.call(http.MethodPost, "/api/v1/services/test", string(body), f.cookie, "", "http://relay.local")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"success":true`) || strings.Contains(w.Body.String(), "synthetic-preview-token") {
			t.Fatalf("scoped preview: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	test()
	saved, err := f.monitor.Save(context.Background(), "admin", input)
	if err != nil {
		t.Fatal(err)
	}
	input.ID, input.Generation, input.Token = saved.ID, saved.Generation, ""
	test()
	if calls != 2 {
		t.Fatalf("upstream calls=%d", calls)
	}
}
