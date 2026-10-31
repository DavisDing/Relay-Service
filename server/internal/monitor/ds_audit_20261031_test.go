package monitor

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCompatibleProvidersExposeUnknownSubscriptionsAndRetainControlMetadata(t *testing.T) {
	now := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	w := newNetworkTestWorker(now)
	body := []byte(`{"providers":{"default":{"vehicleType":"Compatible"},"自动选择":{"vehicleType":"Compatible"},"故障转移":{"vehicleType":"Compatible"},"机场视图":{"vehicleType":"Compatible"}}}`)
	w.applyInventory(body, nil, []byte(`{"proxies":{}}`), nil, "", now)
	evidence := w.view.Coverage.SubscriptionSources
	if evidence.CoreState != "not_exposed" || evidence.ProviderEntries != 4 || evidence.EmbeddedProviders != 4 || evidence.State != "unavailable" || !strings.Contains(evidence.Message, "不能解释为零订阅") {
		t.Fatalf("silent zero or fabricated subscriptions: %+v", evidence)
	}
	details, err := ParseMihomoProviders(body, now)
	if err != nil || details.SubscriptionState != "not_exposed" || details.SubscriptionCount != 0 {
		t.Fatalf("service check disagrees: %+v %v", details, err)
	}
	w.profileSubs = []NetworkSubscription{{ID: "profile-a", Name: "A", Source: "control"}, {ID: "profile-b", Name: "B", Source: "control"}}
	w.view.Coverage.SourceStates["profiles"] = "available"
	w.rebuildNetworkInventory(now)
	if w.view.Coverage.SubscriptionSources.State != "available" || w.view.Coverage.SubscriptionSources.ControlSubscriptions != 2 {
		t.Fatal("lost explicit control provenance")
	}
	w.view.Coverage.SourceStates["profiles"] = "unauthorized"
	w.rebuildNetworkInventory(now)
	if len(w.view.Subscriptions) != 2 || w.view.Coverage.SubscriptionSources.State != "unavailable" || !strings.Contains(w.view.Coverage.SubscriptionSources.Message, "保留上次") {
		t.Fatal("control failure erased metadata or pretended it was fresh")
	}
}

func TestCompatibleProviderWithExplicitQuotaIsNotDiscarded(t *testing.T) {
	body := []byte(`{"providers":{"airport":{"vehicleType":"Compatible","subscriptionInfo":{"upload":100,"download":200,"total":1000,"expire":0}}}}`)
	now := time.Now()
	d, err := ParseMihomoProviders(body, now)
	if err != nil || d.SubscriptionCount != 1 || d.Subscriptions[0].RemainingBytes == nil || *d.Subscriptions[0].RemainingBytes != 700 {
		t.Fatalf("quota evidence discarded: %+v %v", d, err)
	}
	w := newNetworkTestWorker(now)
	w.applyInventory(body, nil, []byte(`{"proxies":{}}`), nil, "", now)
	if len(w.view.Subscriptions) != 1 || w.view.Subscriptions[0].Source != "provider" || w.view.Coverage.SubscriptionSources.CoreSubscriptions != 1 {
		t.Fatal("workspace discarded core quota evidence or replaced its provenance")
	}
	zero := int64(0)
	if mihomoSubscriptionProvider(mihomoRawProvider{VehicleType: "Compatible", Info: &mihomoRawSubscription{Total: &zero, Expire: &zero}}) {
		t.Fatal("placeholder zero metadata fabricated an airport")
	}
}

func TestAirportInformationRowsNeverBecomeNodes(t *testing.T) {
	content := "proxies:\n"
	for i := 0; i < 77; i++ {
		content += fmt.Sprintf("- {name: 节点%d, type: hysteria2}\n", i)
	}
	content += "- {name: '剩余流量：986.32 GB', type: hysteria2}\n- {name: '套餐到期：长期有效', type: hysteria2}\n"
	nodes, err := parseNetworkProfileNodes(content, "sub", "")
	if err != nil || len(nodes) != 77 {
		t.Fatalf("information rows inflated catalog: %d %v", len(nodes), err)
	}
	for _, name := range []string{"剩余流量机场-日本01", "到期时间专线", "Traffic Remaining Tokyo", "套餐到期：日本节点"} {
		if mihomoInformationalName(name) {
			t.Fatalf("ordinary node removed: %s", name)
		}
	}
	for _, name := range []string{"📊 剩余流量：986.32 GB", "套餐到期: 2027-02-03", "Traffic Remaining: 10.5 GB"} {
		if !mihomoNonNode(mihomoRawProxy{Name: name, Type: "Hysteria2"}) {
			t.Fatalf("core/directory classification differs: %s", name)
		}
	}
}

func TestLogStreamRenewalAndTransientDisconnectAreNotSustainedFailure(t *testing.T) {
	now := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	w := newNetworkTestWorker(now)
	w.networkCollectorState("logs", "available", now)
	w.networkCollectorState("logs", "renewing", now.Add(time.Second))
	if w.logState != "connecting" {
		t.Fatalf("planned renewal raised outage: %s", w.logState)
	}
	w.networkCollectorState("logs", "available", now.Add(2*time.Second))
	w.networkCollectorState("logs", "disconnected", now.Add(3*time.Second))
	if w.logState != "reconnecting" {
		t.Fatal("transient failure raised sustained outage")
	}
	w.networkCollectorState("logs", "disconnected", now.Add(17*time.Second))
	if w.logState != "disconnected" {
		t.Fatal("sustained failure hidden")
	}
	w.networkCollectorState("logs", "unauthorized", now.Add(18*time.Second))
	if w.logState != "unauthorized" {
		t.Fatal("authentication failure delayed")
	}
	if err := w.applyStream("logs", []byte(`{"type":"info","payload":"connected"}`), now.Add(19*time.Second)); err != nil {
		t.Fatal(err)
	}
	if !w.logDisconnectedAt.IsZero() || w.logState != "available" {
		t.Fatal("valid frame failed to restore state")
	}
}

func TestProfileMetadataRestoreRequiresSameServiceAndRevision(t *testing.T) {
	now := time.Now()
	w := newNetworkTestWorker(now)
	w.rec.Service.ID = "s"
	w.rec.Service.Generation = 2
	w.revision = 3
	w.settings.ControlBaseURL = "https://control.example.test"
	snapshot := emptyNetworkView("s", 2)
	snapshot.SettingsRevision = 3
	snapshot.Subscriptions = []NetworkSubscription{{ID: "profile-a", Name: "A", Source: "control", NodeCountKnown: true}, {ID: "group", Name: "default", Source: "provider"}}
	wrong := snapshot
	wrong.SettingsRevision++
	w.restoreProfileSnapshot(wrong)
	if len(w.profileSubs) != 0 {
		t.Fatal("old revision leaked")
	}
	wrong = snapshot
	wrong.ServiceID = "other"
	w.restoreProfileSnapshot(wrong)
	if len(w.profileSubs) != 0 {
		t.Fatal("another service leaked")
	}
	w.restoreProfileSnapshot(snapshot)
	if len(w.profileSubs) != 1 || w.profileSubs[0].NodeCountKnown || len(w.view.Nodes) != 0 || w.view.Coverage.SubscriptionSources.State == "available" {
		t.Fatal("restored metadata invented fresh health or included groups")
	}
}
