package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"relayservice/internal/monitor"
)

func TestNetworkWorkspaceAuthenticationAndDeviceIsolation(t *testing.T) {
	f := newFixture(t)
	s, err := f.monitor.Save(context.Background(), "admin", monitor.ServiceInput{Name: "mihomo", Kind: monitor.KindMihomo, BaseURL: "http://127.0.0.1:9090", Enabled: true, AllowPrivate: true, Token: "network-secret-not-returned"})
	if err != nil {
		t.Fatal(err)
	}
	device := pairedToken(t, f, "network reader")
	base := "/api/v1/network/" + s.ID
	for _, section := range []string{"overview", "usage", "subscriptions", "nodes", "connections", "logs"} {
		path := base + "?section=" + section
		if w := f.call("GET", path, "", nil, "", ""); w.Code != 401 {
			t.Fatalf("anonymous %s status=%d", section, w.Code)
		}
		w := f.call("GET", path, "", nil, device.Token, "")
		want := 200
		if section == "connections" || section == "logs" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("device %s status=%d body=%s", section, w.Code, w.Body.String())
		}
		for _, hidden := range []string{"network-secret-not-returned", "127.0.0.1", "control_base_url\":\"http"} {
			if strings.Contains(w.Body.String(), hidden) {
				t.Fatalf("device leaked %s", hidden)
			}
		}
	}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", base + "/settings", ""},
		{"PUT", base + "/settings", fmt.Sprintf(`{"generation":%d}`, s.Generation)},
		{"POST", base + "/subscriptions/import", fmt.Sprintf(`{"generation":%d,"subscriptions":[]}`, s.Generation)},
	} {
		if w := f.call(tc.method, tc.path, tc.body, nil, device.Token, ""); w.Code != 403 {
			t.Fatalf("device accessed management: %s %d", tc.path, w.Code)
		}
	}
	for _, query := range []string{"section=bad", "hours=2", "cursor=-1", "limit=0"} {
		if w := f.call("GET", base+"?"+query, "", f.cookie, "", ""); w.Code != 400 {
			t.Fatalf("invalid query accepted: %s %d %s", query, w.Code, w.Body.String())
		}
	}
}
