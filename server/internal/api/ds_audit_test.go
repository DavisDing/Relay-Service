package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"relayservice/internal/monitor"
	"relayservice/internal/storage"
)

func TestServiceNamesAndGroupsRejectControlCharactersBeforePersistence(t *testing.T) {
	f := newFixture(t)
	for _, field := range []string{"name", "group"} {
		for _, value := range []string{"a\x00b", "a\nb", "a\tb", "a\u007fb", "a\u0085b"} {
			in := monitor.ServiceInput{Name: "service", Kind: monitor.KindHTTP, BaseURL: "https://example.com", Enabled: true}
			if field == "name" {
				in.Name = value
			} else {
				in.Group = value
			}
			body, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			w := f.call("POST", "/api/v1/services", string(body), f.cookie, "", "http://relay.local")
			if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_request") {
				t.Fatalf("invalid %s %q returned %d %s", field, value, w.Code, w.Body.String())
			}
		}
	}
	services, err := f.monitor.Services(context.Background())
	if err != nil || len(services) != 0 {
		t.Fatalf("invalid service persisted: %+v %v", services, err)
	}
	s, err := f.monitor.Save(context.Background(), "admin", monitor.ServiceInput{Name: "valid", Kind: monitor.KindHTTP, BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"id":%q,"generation":%d,"name":"a\u0000b","group":"g\u0000x","kind":"http","base_url":"https://example.com"}`, s.ID, s.Generation)
	w := f.call("PUT", "/api/v1/services/"+s.ID, body, f.cookie, "", "http://relay.local")
	if w.Code != 400 {
		t.Fatalf("invalid update returned %d %s", w.Code, w.Body.String())
	}
	got, err := f.monitor.Get(context.Background(), s.ID)
	if err != nil || got.Name != "valid" || got.Generation != s.Generation {
		t.Fatal("invalid update changed saved service", got, err)
	}
}

func TestAlertsHTTPHasMoreAndCursorAtExactPageBoundary(t *testing.T) {
	f := newFixture(t)
	if err := f.store.Update(context.Background(), func(tx storage.Tx) error {
		return tx.Put("monitor.alerts", fmt.Sprintf("%020d", 2), monitor.Alert{ID: "2", Cursor: 2})
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cursor string
		count  int
		next   string
	}{{"", 1, "2"}, {"2", 0, "2"}} {
		w := f.call("GET", "/api/v1/alerts?limit=1&cursor="+tc.cursor, "", f.cookie, "", "")
		var page struct {
			Data struct {
				Items   []monitor.Alert `json:"items"`
				HasMore bool            `json:"has_more"`
				Next    string          `json:"next_cursor"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || page.Data.HasMore || len(page.Data.Items) != tc.count || page.Data.Next != tc.next {
			t.Fatalf("incorrect final page: %d %s", w.Code, w.Body.String())
		}
	}
}
