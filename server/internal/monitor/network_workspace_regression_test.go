package monitor

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"relayservice/internal/storage"
)

func TestNetworkSubscriptionsExcludeCompatibleGroupsWithoutNameBlacklist(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	w := newNetworkTestWorker(now)
	providers := []byte(`{"providers":{"default":{"vehicleType":"Compatible","proxies":[{"name":"HK","type":"AnyTLS"}]},"故障转移":{"vehicleType":"Compatible"},"自动选择":{"vehicleType":"Compatible"},"内置节点":{"vehicleType":"Inline"},"星网":{"vehicleType":"HTTP","proxies":[{"name":"HK","type":"AnyTLS"}]},"同名真实订阅":{"vehicleType":"File"}}}`)
	core := []byte(`{"proxies":{"HK":{"type":"AnyTLS"}}}`)
	w.applyInventory(providers, nil, core, nil, "", now)
	if len(w.view.Subscriptions) != 2 || len(w.view.Nodes) != 1 || len(w.view.Nodes[0].Subscriptions) != 1 || w.view.Nodes[0].Subscriptions[0].Name != "星网" {
		t.Fatalf("group leaked into subscriptions: %+v", w.view)
	}
	details, err := ParseMihomoProviders(providers, now)
	if err != nil || details.SubscriptionCount != 2 {
		t.Fatalf("service check disagrees with workspace: %+v %v", details, err)
	}
	for _, name := range []string{"default", "故障转移", "自动选择"} {
		body := []byte(fmt.Sprintf(`{"providers":{%q:{"vehicleType":"HTTP"}}}`, name))
		w.applyInventory(body, nil, core, nil, "", now)
		if len(w.view.Subscriptions) != 1 || w.view.Subscriptions[0].Name != name {
			t.Fatalf("real subscription was blacklisted by name: %s", name)
		}
	}
}

func TestNetworkSubscriptionsFallbackExcludesCoreGroupsWithoutVehicleType(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	w := newNetworkTestWorker(now)
	providers := []byte(`{"providers":{"自动选择":{"proxies":[{"name":"HK","type":"AnyTLS"}]},"机场":{"proxies":[{"name":"HK","type":"AnyTLS"}]}}}`)
	core := []byte(`{"proxies":{"自动选择":{"type":"URLTest","all":["HK"]},"HK":{"type":"AnyTLS"}}}`)
	w.applyInventory(providers, nil, core, nil, "", now)
	if len(w.view.Subscriptions) != 1 || w.view.Subscriptions[0].Name != "机场" || len(w.view.Nodes) != 1 || len(w.view.Nodes[0].Subscriptions) != 1 {
		t.Fatalf("core group leaked into subscription fallback: %+v", w.view)
	}
}

func TestNetworkAvailabilityCountsDistinctFreshSourceChecksAndKeepsGaps(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	sample := func(minutes int, state, freshness string) NetworkNodeSample {
		at := now.Add(-time.Duration(minutes) * time.Minute)
		return NetworkNodeSample{At: at, CheckedAt: &at, State: state, Freshness: freshness}
	}
	items := []NetworkNodeSample{sample(60, "healthy", "fresh"), sample(30, "unhealthy", "fresh"), sample(20, "healthy", "stale"), sample(1450, "healthy", "fresh"), sample(-1, "healthy", "fresh"), {At: now, State: "healthy", Freshness: "fresh"}}
	duplicate := items[0]
	duplicate.At = now
	items = append(items, duplicate)
	a := networkAvailability(items, now)
	if a.Success != 1 || a.Failure != 1 || a.SuccessRate == nil || *a.SuccessRate != .5 || a.CoveredIntervals != 2 || len(a.Intervals) != 96 {
		t.Fatalf("invalid availability: %+v", a)
	}
	if empty := networkAvailability(nil, now); empty.SuccessRate != nil || empty.CoveredIntervals != 0 {
		t.Fatalf("missing history became success: %+v", empty)
	}
}

func TestUnifiedRetentionSweepsUnindexedNetworkHoursAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.sqlite")
	store, err := storage.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	old := now.Add(-170 * time.Hour)
	if err := store.Update(ctx, func(tx storage.Tx) error {
		for _, bucket := range []string{networkUsageBucket, networkNodeBucket, networkBreakdownBucket} {
			for i := 0; i < retentionBatchSize+1; i++ {
				key := networkHourKey(fmt.Sprintf("deleted-%03d", i), 1, old)
				if bucket == networkNodeBucket {
					key += "/node"
				}
				if err := tx.Put(bucket, key, map[string]any{"items": []any{}}); err != nil {
					return err
				}
			}
			key := networkHourKey("fresh", 2, now.Add(-networkHistoryHours*time.Hour))
			if bucket == networkNodeBucket {
				key += "/node"
			}
			if err := tx.Put(bucket, key, map[string]any{"items": []any{}}); err != nil {
				return err
			}
		}
		return tx.Put(networkSnapshotBucket, "current", emptyNetworkView("current", 1))
	}); err != nil {
		t.Fatal(err)
	}
	e := New(store, nil)
	e.now = func() time.Time { return now }
	// No history/audit policy or enabled service exists: network policy still runs.
	if err := e.PruneRetention(ctx); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{networkUsageBucket, networkNodeBucket, networkBreakdownBucket} {
		assertNetworkRows(t, store, bucket, 2)
	}
	store.Close()
	store, err = storage.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	e = New(store, nil)
	e.now = func() time.Time { return now }
	if err := e.PruneRetention(ctx); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{networkUsageBucket, networkNodeBucket, networkBreakdownBucket, networkSnapshotBucket} {
		assertNetworkRows(t, store, bucket, 1)
	}
}

func TestNetworkAvailabilityEnrichesOnlyRequestedPageFromSavedHistory(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := storage.NewMemory()
	t.Cleanup(store.Close)
	e := New(store, nil)
	e.now = func() time.Time { return now }
	checked := now.Add(-time.Hour)
	if err := store.Update(context.Background(), func(tx storage.Tx) error {
		return tx.Put(networkNodeBucket, networkHourKey("instance", 2, checked)+"/node", networkHourNodes{Items: []NetworkNodeSample{{At: checked, CheckedAt: &checked, State: "healthy", Freshness: "fresh"}}})
	}); err != nil {
		t.Fatal(err)
	}
	nodes := []NetworkNode{{ID: "node", State: "unknown"}, {ID: "empty", State: "unknown"}}
	if err := e.readNetworkAvailability(context.Background(), "instance", 2, nodes); err != nil {
		t.Fatal(err)
	}
	if nodes[0].Availability24H.Success != 1 || nodes[1].Availability24H.SuccessRate != nil {
		t.Fatalf("lost persisted history: %+v", nodes)
	}
	if err := e.readNetworkAvailability(context.Background(), "instance", 3, nodes); err != nil {
		t.Fatal(err)
	}
	if nodes[0].Availability24H.SuccessRate != nil {
		t.Fatal("history leaked across generations")
	}
}
