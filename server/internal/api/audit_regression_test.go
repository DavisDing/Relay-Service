package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"relayservice/internal/access"
	"relayservice/internal/monitor"
	"relayservice/internal/storage"
)

func pairedToken(t *testing.T, f fixture, name string) access.DeviceToken {
	t.Helper()
	p, err := f.access.CreatePairing(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.access.Pair(context.Background(), p.Code, name)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestGlobalDeviceAlertsKeepIndependentContiguousReadPositions(t *testing.T) {
	f := newFixture(t)
	first := pairedToken(t, f, "first")
	second := pairedToken(t, f, "second")
	if err := f.store.Update(context.Background(), func(tx storage.Tx) error {
		for i := 1; i <= 3; i++ {
			a := monitor.Alert{Cursor: uint64(i), ID: fmt.Sprint(i), ServiceID: fmt.Sprintf("service-%d", i%2), Kind: "failure", Message: "Target connection failed", StartedAt: time.Now().UTC()}
			if err := tx.Put("monitor.alerts", fmt.Sprintf("%020d", i), a); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w := f.call("GET", "/api/v1/alerts?limit=2", "", nil, first.Token, "")
	var page struct {
		Data struct {
			Items []monitor.Alert `json:"items"`
			Ack   uint64          `json:"ack_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Data.Items) != 2 {
		t.Fatalf("global readonly alerts: %d %s %v", w.Code, w.Body.String(), err)
	}
	if page.Data.Items[0].ServiceID == page.Data.Items[1].ServiceID {
		t.Fatal("fixture did not exercise cross-service visibility")
	}
	for _, body := range []string{`{"cursor":3}`, `{"cursor":3,"cursors":[1,3]}`, `{"cursor":3,"cursors":[3]}`} {
		w := f.call("POST", "/api/v1/alerts/ack", body, nil, first.Token, "")
		if w.Code != 400 {
			t.Fatalf("jump accepted: %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	w = f.call("POST", "/api/v1/alerts/ack", `{"cursor":2,"cursors":[1,2]}`, nil, first.Token, "")
	if w.Code != 200 {
		t.Fatalf("visible continuous page ack: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		token access.DeviceToken
		want  uint64
	}{{first, 2}, {second, 0}} {
		cursor, err := f.monitor.AckCursor(context.Background(), tc.token.DeviceID)
		if err != nil || cursor != tc.want {
			t.Fatalf("principal cursor %d want %d: %v", cursor, tc.want, err)
		}
	}
	// Reading/acknowledging does not remove either service's original events.
	w = f.call("GET", "/api/v1/alerts?limit=100", "", nil, second.Token, "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Data.Items) != 3 || page.Data.Ack != 0 {
		t.Fatal("ack removed global events or changed another device", w.Body.String(), err)
	}
	if err := f.access.Revoke(context.Background(), first.DeviceID); err != nil {
		t.Fatal(err)
	}
	if w := f.call("GET", "/api/v1/alerts", "", nil, first.Token, ""); w.Code != 401 {
		t.Fatal("revoked device read alerts", w.Code)
	}
}

func TestRetentionAdministrationPreviewConfirmAndPermissions(t *testing.T) {
	f := newFixture(t)
	device := pairedToken(t, f, "readonly")
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/retention", ""},
		{"POST", "/api/v1/retention/preview", `{"history_days":30,"audit_days":0,"generation":0}`},
		{"PUT", "/api/v1/retention", `{"preview_id":"anything"}`},
	} {
		if w := f.call(tc.method, tc.path, tc.body, nil, device.Token, ""); w.Code != 403 {
			t.Fatalf("device retention access %s: %d", tc.path, w.Code)
		}
		if w := f.call(tc.method, tc.path, tc.body, nil, "", "http://relay.local"); w.Code != 401 {
			t.Fatalf("anonymous retention access %s: %d", tc.path, w.Code)
		}
	}
	w := f.call("GET", "/api/v1/retention", "", f.cookie, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"history_days":0`) {
		t.Fatal("default policy not indefinite", w.Code, w.Body.String())
	}
	w = f.call("POST", "/api/v1/retention/preview", `{"history_days":30,"audit_days":0,"generation":0}`, f.cookie, "", "https://other.local")
	if w.Code != 403 {
		t.Fatal("cross-origin policy preview accepted", w.Code)
	}
	w = f.call("POST", "/api/v1/retention/preview", `{"history_days":30,"audit_days":0,"generation":0}`, f.cookie, "", "http://relay.local")
	var preview struct {
		Data monitor.RetentionPreview `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != 200 || preview.Data.ID == "" {
		t.Fatalf("preview: %d %s %v", w.Code, w.Body.String(), err)
	}
	w = f.call("GET", "/api/v1/retention", "", f.cookie, "", "")
	if !strings.Contains(w.Body.String(), `"history_days":0`) {
		t.Fatal("preview changed policy", w.Body.String())
	}
	body := fmt.Sprintf(`{"preview_id":%q}`, preview.Data.ID)
	w = f.call("PUT", "/api/v1/retention", body, f.cookie, "", "http://relay.local")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"history_days":30`) {
		t.Fatal("confirm failed", w.Code, w.Body.String())
	}
	w = f.call("PUT", "/api/v1/retention", body, f.cookie, "", "http://relay.local")
	if w.Code != 409 {
		t.Fatal("replayed authorization accepted", w.Code, w.Body.String())
	}
}
