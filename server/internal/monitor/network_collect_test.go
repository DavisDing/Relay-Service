package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"relayservice/internal/storage"
)

func newNetworkTestWorker(now time.Time) *networkWorker {
	w := &networkWorker{view: emptyNetworkView("test", 1), settings: networkDefaults(1), lastNodeSamples: map[string]NetworkNodeSample{}, pendingNodes: map[string]NetworkNodeSample{}}
	w.view.State = "available"
	w.view.CollectedAt = &now
	return w
}

func networkFixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNetworkInventoryCanonicalNodesAcrossFourProviders(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	w := newNetworkTestWorker(now)
	proxies := map[string]any{"DIRECT": map[string]any{"type": "Direct"}, "自动选择": map[string]any{"type": "URLTest", "all": []string{"节点00"}}}
	nodes := []any{}
	for i := 0; i < 25; i++ {
		delay := 341
		if i >= 22 {
			delay = 0
		}
		name := fmt.Sprintf("节点%02d", i)
		n := map[string]any{"name": name, "type": "AnyTLS", "history": []any{map[string]any{"time": now, "delay": delay}}}
		proxies[name] = n
		nodes = append(nodes, n)
	}
	providers := map[string]any{}
	for _, name := range []string{"故障转移", "自动选择", "default", "星网-Starnetwork"} {
		providers[name] = map[string]any{"proxies": append(append([]any{}, nodes...), nodes[0])}
	}
	providerBody := networkFixtureJSON(t, map[string]any{"providers": providers})
	proxyBody := networkFixtureJSON(t, map[string]any{"proxies": proxies})
	w.applyInventory(providerBody, nil, proxyBody, nil, "", now)
	if len(w.view.Nodes) != 25 || len(w.pendingNodes) != 25 || len(w.view.Subscriptions) != 4 {
		t.Fatalf("duplicated identities: nodes=%d history=%d subscriptions=%d", len(w.view.Nodes), len(w.pendingNodes), len(w.view.Subscriptions))
	}
	healthy := 0
	ids := map[string]bool{}
	for _, n := range w.view.Nodes {
		if ids[n.ID] || len(n.Subscriptions) != 4 {
			t.Fatalf("bad node membership: %+v", n)
		}
		ids[n.ID] = true
		if n.State == "healthy" {
			healthy++
		}
		for _, sub := range w.view.Subscriptions {
			if sub.NodeCount != 25 || !networkNodeInSubscription(n, sub.ID) {
				t.Fatalf("lost provider membership: %+v %+v", sub, n)
			}
		}
	}
	if healthy != 22 {
		t.Fatalf("healthy=%d want 22", healthy)
	}
	store := storage.NewMemory()
	t.Cleanup(store.Close)
	e := New(store, nil)
	e.now = func() time.Time { return now }
	s, err := e.Save(context.Background(), "admin", ServiceInput{Name: "mihomo", Kind: KindMihomo, BaseURL: "http://127.0.0.1:9090", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w.rec = serviceRecord{Service: s}
	w.view.ServiceID = s.ID
	e.network = &networkRuntime{workers: map[string]*networkWorker{s.ID: w}}
	v, err := e.NetworkView(context.Background(), s.ID, NetworkQuery{Section: "nodes"})
	if err != nil || v.Total != 25 || v.Overview.NodeCount != 25 || v.Overview.HealthyNodes != 22 || v.Overview.UnhealthyNodes != 3 {
		t.Fatalf("public counts still inflated: %+v total=%d err=%v", v.Overview, v.Total, err)
	}
	// Membership removal/reordering does not change physical identity or create
	// another history stream for the same current core observation.
	delete(providers, "default")
	w.pendingNodes = map[string]NetworkNodeSample{}
	w.applyInventory(networkFixtureJSON(t, map[string]any{"providers": providers}), nil, proxyBody, nil, "", now)
	if len(w.pendingNodes) != 0 {
		t.Fatal("membership-only change created history samples")
	}
	for _, n := range w.view.Nodes {
		if !ids[n.ID] || len(n.Subscriptions) != 3 {
			t.Fatalf("unstable identity: %+v", n)
		}
	}
}

func TestNetworkInventoryUsesCoreEvidenceAndDoesNotBorrowDirectoryHealth(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	w := newNetworkTestWorker(now)
	provider := []byte(`{"providers":{"airport":{"proxies":[{"name":"same","type":"AnyTLS","history":[{"time":"2026-10-29T10:00:00Z","delay":5}]},{"name":"not-loaded","type":"AnyTLS"}]}}}`)
	core := []byte(`{"proxies":{"same":{"name":"ignored-payload-name","type":"AnyTLS","history":[{"time":"2026-10-29T10:00:00Z","delay":0}]}}}`)
	w.profileNodes = []NetworkNode{{ID: "profile", Name: "same", Type: "AnyTLS", State: "unknown", Freshness: "missing"}}
	w.applyInventory(provider, nil, core, nil, "", now)
	if len(w.coreNodes) != 1 || w.coreNodes[0].Name != "same" || w.coreNodes[0].State != "unhealthy" || w.profileNodes[0].State != "unknown" {
		t.Fatalf("borrowed provider/directory evidence: %+v", w.view.Nodes)
	}
	w.applyInventory(provider, nil, nil, errors.New("offline"), "", now)
	if len(w.coreNodes) != 0 || len(w.coreSubs) != 1 || w.view.Coverage.SourceStates["nodes"] != "unavailable" {
		t.Fatal("failed canonical source borrowed provider health")
	}
	w.applyInventory([]byte(`{}`), nil, core, nil, "", now)
	if len(w.coreNodes) != 1 || len(w.coreNodes[0].Subscriptions) != 0 || w.view.Coverage.SourceStates["providers"] != "unavailable" {
		t.Fatal("independent core evidence lost when providers fail")
	}
}

func TestNetworkStreamCountersResetAndLogBounds(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	w := newNetworkTestWorker(now)
	for i, frame := range []string{`{"up":1,"down":2,"upTotal":100,"downTotal":200}`, `{"up":1,"down":2,"upTotal":110,"downTotal":220}`, `{"up":1,"down":2,"upTotal":1,"downTotal":2}`, `{"up":1,"down":2}`, `{"up":1,"down":2,"upTotal":500,"downTotal":600}`} {
		if err := w.applyStream("traffic", []byte(frame), now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if w.pendingUsage.UploadBytes != 10 || w.pendingUsage.DownloadBytes != 20 || w.pendingUsage.Samples != 1 || !w.pendingUsage.CounterReset {
		t.Fatalf("counter reset or missing total manufactured usage: %+v", w.pendingUsage)
	}
	for _, frame := range []string{`{}`, `{"up":-1,"down":2}`, `broken`} {
		if err := w.applyStream("traffic", []byte(frame), now); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid traffic accepted: %s", frame)
		}
	}
	w.settings.PersistLogs = true
	w.sensitive = []string{"core-secret"}
	for i := 0; i < 1005; i++ {
		body := networkFixtureJSON(t, map[string]any{"type": "info", "payload": "[TCP] --> pipio.io:443 using 星网[台湾1] token=core-secret"})
		if err := w.applyStream("logs", body, now); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.logs) != 1000 || len(w.pendingLogs) != 1000 || !w.view.Coverage.Truncated || w.logs[0].ID == w.logs[1].ID {
		t.Fatal("unbounded logs or duplicate identity for same timestamp")
	}
	for _, log := range append(w.logs, w.pendingLogs...) {
		if strings.Contains(log.Message, "core-secret") || !strings.Contains(log.Message, "pipio.io:443") || !strings.Contains(log.Message, "台湾1") {
			t.Fatalf("unsafe or useless log: %+v", log)
		}
	}
}
