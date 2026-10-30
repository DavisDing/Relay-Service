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
		providers[name] = map[string]any{"vehicleType": "HTTP", "proxies": append(append([]any{}, nodes...), nodes[0])}
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

func TestNetworkInventoryAssociatesOnlyActiveProfileWithCoreEvidence(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	w := newNetworkTestWorker(now)
	active := NetworkSubscription{ID: "profile-start", Name: "start", Source: "control", Active: true, NodeCount: 1, NodeCountKnown: true}
	inactive := NetworkSubscription{ID: "profile-1", Name: "1", Source: "control", Active: false}
	w.profileSubs = []NetworkSubscription{active, inactive}
	w.profilesFetched = now
	w.view.Coverage.SourceStates["profiles"] = "available"
	w.coreNodes = []NetworkNode{{ID: "core-node", Name: "same", Type: "AnyTLS", State: "healthy", Freshness: "fresh", Loaded: true, CheckedAt: &now, DelayMS: ptrUint16(42)}}
	w.profileNodes = []NetworkNode{
		{ID: "active-directory", SubscriptionID: active.ID, SubscriptionName: active.Name, Name: "same", Type: "anytls", State: "unknown", Freshness: "missing", Loaded: false},
		{ID: "inactive-directory", SubscriptionID: inactive.ID, SubscriptionName: inactive.Name, Name: "same", Type: "AnyTLS", State: "unknown", Freshness: "missing", Loaded: false},
	}
	w.rebuildNetworkInventory(now)
	if len(w.view.Nodes) != 2 {
		t.Fatalf("expected one loaded node and one inactive directory node: %+v", w.view.Nodes)
	}
	var loaded, inactiveNode *NetworkNode
	for i := range w.view.Nodes {
		node := &w.view.Nodes[i]
		if node.Loaded {
			loaded = node
		} else {
			inactiveNode = node
		}
	}
	if loaded == nil || loaded.DelayMS == nil || *loaded.DelayMS != 42 || !networkNodeHasSubscription(*loaded, active.ID) || networkNodeHasSubscription(*loaded, inactive.ID) {
		t.Fatalf("active profile did not inherit only core evidence: %+v", loaded)
	}
	if inactiveNode == nil || inactiveNode.SubscriptionID != inactive.ID || inactiveNode.Loaded || inactiveNode.DelayMS != nil {
		t.Fatalf("inactive profile incorrectly inherited core evidence: %+v", inactiveNode)
	}
	if w.coreNodes[0].SubscriptionID != "" || len(w.coreNodes[0].Subscriptions) != 0 || w.profileNodes[0].Loaded || w.profileNodes[0].DelayMS != nil {
		t.Fatal("derived membership mutated source inventory")
	}
	w.pendingNodes = map[string]NetworkNodeSample{}
	w.rebuildNetworkInventory(now)
	if len(w.pendingNodes) != 0 {
		t.Fatal("repeated membership merge duplicated history")
	}
	// A profile switch moves the membership, retaining the core observation's
	// stable identity. The old profile returns to a separate unknown directory.
	w.profileSubs[0].Active = false
	w.profileSubs[1].Active = true
	w.profileSubs[1].NodeCountKnown = true
	w.profileSubs[1].NodeCount = 1
	w.rebuildNetworkInventory(now)
	for _, node := range w.view.Nodes {
		if node.Loaded && (node.ID != "core-node" || !networkNodeHasSubscription(node, inactive.ID) || networkNodeHasSubscription(node, active.ID)) {
			t.Fatalf("membership survived profile switch: %+v", node)
		}
		if !node.Loaded && (node.SubscriptionID != active.ID || node.DelayMS != nil || node.CheckedAt != nil) {
			t.Fatalf("previous active profile kept detection evidence: %+v", node)
		}
	}
}

func ptrUint16(value uint16) *uint16 { return &value }

func TestNetworkProfileMembershipRejectsPartialAmbiguousAndStaleEvidence(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		change func(*networkWorker)
	}{
		{"inactive", func(w *networkWorker) { w.profileSubs[0].Active = false }},
		{"partial-detail", func(w *networkWorker) { w.profileSubs[0].NodeCountKnown = false }},
		{"catalog-auth-failed", func(w *networkWorker) { w.view.Coverage.SourceStates["profiles"] = "unauthorized" }},
		{"catalog-future", func(w *networkWorker) { w.profilesFetched = now.Add(time.Minute) }},
		{"inconsistent-directory-count", func(w *networkWorker) { w.profileSubs[0].NodeCount = 2 }},
		{"catalog-stale", func(w *networkWorker) { w.profilesFetched = now.Add(-6 * time.Minute) }},
		{"core-reloaded", func(w *networkWorker) { w.coreCatalogStamp = "new-core" }},
		{"different-protocol", func(w *networkWorker) { w.profileNodes[0].Type = "trojan" }},
		{"case-sensitive-name", func(w *networkWorker) { w.profileNodes[0].Name = "SAME" }},
		{"partial-overlap", func(w *networkWorker) {
			w.profileNodes = append(w.profileNodes, NetworkNode{ID: "extra", SubscriptionID: "profile-start", Name: "not-loaded", Type: "ss"})
		}},
		{"multiple-active", func(w *networkWorker) {
			w.profileSubs = append(w.profileSubs, NetworkSubscription{ID: "profile-other", Source: "control", Active: true, NodeCount: 1, NodeCountKnown: true})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newNetworkTestWorker(now)
			w.profileSubs = []NetworkSubscription{{ID: "profile-start", Name: "start", Source: "control", Active: true, NodeCount: 1, NodeCountKnown: true}}
			w.profileNodes = []NetworkNode{{ID: "directory", Name: "same", Type: "ss", SubscriptionID: "profile-start", State: "unknown", Freshness: "missing"}}
			w.coreNodes = []NetworkNode{{ID: "core", Name: "same", Type: "Shadowsocks", Loaded: true, State: "healthy", Freshness: "fresh", CheckedAt: &now, DelayMS: ptrUint16(9)}}
			w.profilesFetched = now
			w.view.Coverage.SourceStates["profiles"] = "available"
			w.rebuildNetworkInventory(now)
			if len(w.view.Nodes) != 1 || !networkNodeHasSubscription(w.view.Nodes[0], "profile-start") {
				t.Fatal("complete active profile did not link protocol alias")
			}
			tc.change(w)
			w.rebuildNetworkInventory(now)
			if len(w.view.Nodes) < 2 || w.view.Coverage.SourceStates["profile_membership"] != "unknown" {
				t.Fatalf("unverified profile linked: %+v", w.view.Nodes)
			}
			for _, node := range w.view.Nodes {
				if node.Loaded && networkNodeHasSubscription(node, "profile-start") || !node.Loaded && node.DelayMS != nil {
					t.Fatalf("unverified directory inherited core detection: %+v", node)
				}
			}
		})
	}
}

func TestNetworkProfileMembershipRetainsProvidersAndPublicCounts(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	store := storage.NewMemory()
	t.Cleanup(store.Close)
	e, w := networkEngineFixture(t, store, now)
	e.network = &networkRuntime{workers: map[string]*networkWorker{w.rec.Service.ID: w}}
	w.profilesFetched = now
	w.view.Coverage.SourceStates["profiles"] = "available"
	w.profileSubs = []NetworkSubscription{{ID: "profile-start", Name: "start", Source: "control", Active: true, NodeCount: 1, NodeCountKnown: true}}
	w.profileNodes = []NetworkNode{{ID: "directory", SubscriptionID: "profile-start", Name: "same", Type: "ss", State: "unknown", Freshness: "missing"}}
	w.coreNodes = []NetworkNode{{ID: "core", Name: "same", Type: "Shadowsocks", State: "healthy", Freshness: "fresh", Loaded: true, CheckedAt: &now, DelayMS: ptrUint16(9), SubscriptionID: "provider", Subscriptions: []NetworkNodeSubscription{{ID: "provider", Name: "provider"}}}}
	w.rebuildNetworkInventory(now)
	for _, filter := range []string{"", "profile-start", "provider"} {
		v, err := e.NetworkView(context.Background(), w.rec.Service.ID, NetworkQuery{Section: "nodes", Subscription: filter})
		if err != nil || v.Total != 1 || v.Overview.NodeCount != 1 || v.Overview.HealthyNodes != 1 || len(v.Nodes) != 1 || v.Nodes[0].ID != "core" || v.Nodes[0].DelayMS == nil {
			t.Fatalf("filter %q duplicated or lost core observation: %+v %v", filter, v, err)
		}
		if len(v.Nodes[0].Subscriptions) != 2 || v.Nodes[0].Subscriptions[1].Name != "start" {
			t.Fatalf("lost provider or profile membership: %+v", v.Nodes[0])
		}
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
