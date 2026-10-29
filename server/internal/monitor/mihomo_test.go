package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMihomoSubscriptionAndExistingQuality(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"providers":{"airport":{"updatedAt":"2026-10-28T10:00:00Z","subscriptionInfo":{"Upload":20,"Download":30,"Total":100,"Expire":1800000000},"proxies":[{"name":"HK","type":"Shadowsocks","alive":true,"history":[{"time":"2026-10-28T11:59:00Z","delay":52}],"server":"private.example","password":"do-not-store","uuid":"private-id","extra":{"https://test.example":{"alive":true}}}]}}}`)
	d, err := ParseMihomoProviders(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.NodeCount != 1 || d.HealthyNodes != 1 || d.Nodes[0].Provider != "airport" || *d.Nodes[0].DelayMS != 52 || d.Nodes[0].Freshness != "fresh" {
		t.Fatalf("unexpected quality: %+v", d)
	}
	sub := d.Subscriptions[0]
	if *sub.UsedBytes != 50 || *sub.RemainingBytes != 50 || sub.UsageState != "available" || sub.UpdatedAt == nil {
		t.Fatalf("unexpected subscription: %+v", sub)
	}
	projected, _ := json.Marshal(d)
	for _, secret := range []string{"private.example", "do-not-store", "private-id", "https://test.example", "password", "uuid", "server", "extra"} {
		if strings.Contains(string(projected), secret) {
			t.Fatalf("retained private field %s", secret)
		}
	}
}

func TestMihomoMissingStaleZeroAndFutureEvidence(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"providers":{"P":{"proxies":[{"name":"default-alive","type":"VMess","alive":true,"history":[]},{"name":"old","type":"VMess","alive":true,"history":[{"time":"2026-10-28T11:00:00Z","delay":10}]},{"name":"zero","type":"VMess","alive":true,"history":[{"time":"2026-10-28T11:59:00Z","delay":0}]},{"name":"failed","type":"VMess","alive":false,"history":[{"time":"2026-10-28T11:59:00Z","delay":0}]},{"name":"future","type":"VMess","alive":true,"history":[{"time":"2026-10-28T12:01:00Z","delay":10}]},{"name":"unordered","type":"VMess","history":[{"time":"2026-10-28T11:59:00Z","delay":10},{"time":"2026-10-28T10:01:00Z","delay":0}]},{"name":"group","type":"Selector","all":["failed"]},{"name":"DIRECT","type":"Direct"}]}}}`)
	d, err := ParseMihomoProviders(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.NodeCount != 6 || d.HealthyNodes != 1 || d.UnhealthyNodes != 2 || d.UnknownNodes != 3 || d.StaleNodes != 1 {
		t.Fatalf("wrong counts: %+v", d)
	}
	if d.Subscriptions[0].Info != nil || d.Subscriptions[0].UsageState != "unknown" {
		t.Fatal("missing metadata must stay unknown")
	}
	want := []string{"unknown", "unknown", "unhealthy", "unhealthy", "unknown", "healthy"}
	for i, n := range d.Nodes {
		if n.State != want[i] {
			t.Fatalf("node %s state %s", n.Name, n.State)
		}
	}
}

func TestMihomoSubscriptionQuotaBoundaries(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, tt := range []struct {
		info, want string
		invalid    bool
	}{
		{`{"Upload":60,"Download":40,"Total":100}`, "exhausted", false},
		{`{"Upload":60,"Download":60,"Total":100}`, "exhausted", false},
		{`{"Upload":0,"Download":0,"Total":0,"Expire":0}`, "unknown", false},
		{`{"Upload":1,"Download":1,"Total":100,"Expire":1000}`, "expired", false},
		{`{"Total":100}`, "unknown", false}, {`{"Upload":-1}`, "", true},
		{`{"Upload":9223372036854775807,"Download":1}`, "", true}, {`{"Expire":253402300800}`, "", true},
	} {
		d, err := ParseMihomoProviders([]byte(`{"providers":{"P":{"subscriptionInfo":`+tt.info+`}}}`), now)
		if tt.invalid {
			if err == nil {
				t.Errorf("accepted invalid %s", tt.info)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if d.Subscriptions[0].UsageState != tt.want {
			t.Errorf("%s: %s", tt.info, d.Subscriptions[0].UsageState)
		}
	}
}

func TestMihomoCollectorFixedRequestsAndDeduplication(t *testing.T) {
	var paths []string
	d, err := CollectMihomo(context.Background(), func(_ context.Context, path string) ([]byte, error) {
		paths = append(paths, path)
		if path == "/providers/proxies" {
			return []byte(`{"providers":{"A":{"proxies":[{"name":"same","type":"Vless"},{"name":"same","type":"Vless"}]},"B":{"proxies":[{"name":"same","type":"Vless"}]}}}`), nil
		}
		if path == "/proxies" {
			return []byte(`{"proxies":{"same":{"name":"same","type":"Vless"},"manual":{"name":"manual","type":"Socks5"},"group":{"type":"Selector","all":["manual"]}}}`), nil
		}
		t.Fatalf("unapproved path %s", path)
		return nil, nil
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "/providers/proxies,/proxies" || d.NodeCount != 2 || d.ProxySourceState != "available" {
		t.Fatalf("requests/details: %v %+v", paths, d)
	}
}

func TestMihomoSupplementaryFailureKeepsSubscription(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		d, err := CollectMihomo(context.Background(), func(_ context.Context, path string) ([]byte, error) {
			if path == "/providers/proxies" {
				return []byte(`{"providers":{"airport":{"subscriptionInfo":{"Expire":1}}}}`), nil
			}
			if invalid {
				return []byte(`{"proxies":{"bad":{"type":"VMess","name":"different"}}}`), nil
			}
			return nil, errors.New("Bearer secret raw upstream URL")
		}, time.Now())
		if err != nil || d.SubscriptionCount != 1 || !d.SubscriptionUnavailable {
			t.Fatalf("provider data lost: %+v %v", d, err)
		}
		b, _ := json.Marshal(d)
		if strings.Contains(string(b), "secret") {
			t.Fatal("error leaked")
		}
		if invalid && d.ProxySourceState != "invalid_response" {
			t.Fatal(d.ProxySourceState)
		}
		if !invalid && d.ProxySourceState != "unavailable" {
			t.Fatal(d.ProxySourceState)
		}
	}
}

func TestMihomoProjectionLimitAndInvalidResponse(t *testing.T) {
	var nodes []string
	for i := 0; i < 205; i++ {
		nodes = append(nodes, fmt.Sprintf(`{"name":"node-%d","type":"VMess"}`, i))
	}
	d, err := ParseMihomoProviders([]byte(`{"providers":{"P":{"proxies":[`+strings.Join(nodes, ",")+`]}}}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if d.NodeCount != 205 || len(d.Nodes) != 200 || !d.Truncated || d.UnknownNodes != 205 {
		t.Fatal("projection limit/statistics incorrect")
	}
	for _, body := range [][]byte{[]byte(`{}`), []byte(`{"providers":null}`), []byte(`{"providers":{"P":{"proxies":[{"name":"bad\nname","type":"VMess"}]}}}`), []byte(strings.Repeat(" ", mihomoResponseLimit+1))} {
		if _, err := ParseMihomoProviders(body, time.Now()); err == nil {
			t.Fatal("accepted invalid response")
		}
	}
}

func TestMihomoSubscriptionAlertBeyondProjection(t *testing.T) {
	providers := make(map[string]any)
	for i := 0; i < 201; i++ {
		providers[fmt.Sprintf("provider-%03d", i)] = map[string]any{}
	}
	providers["provider-200"] = map[string]any{"subscriptionInfo": map[string]int64{"Expire": 1}}
	body, _ := json.Marshal(map[string]any{"providers": providers})
	d, err := ParseMihomoProviders(body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !d.SubscriptionUnavailable || !d.Truncated || d.SubscriptionCount != 201 || len(d.Subscriptions) != 200 {
		t.Fatalf("lost unprojected alert: %+v", d)
	}
}
