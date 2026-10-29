package monitor

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"relayservice/internal/storage"
)

func TestNetworkLogExpiryPrefixResumesAndProtectsFutureData(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	t.Cleanup(store.Close)
	e := New(store, nil)
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	if err := store.Update(ctx, func(tx storage.Tx) error {
		for i := 0; i < 1001; i++ {
			l := NetworkLog{ID: fmt.Sprint(i), At: now.Add(-25 * time.Hour)}
			if err := tx.Put(networkLogsBucket, networkLogKey("a", 1, l), l); err != nil {
				return err
			}
		}
		if err := networkPutLogExpiry(tx, "a", 1, now.Add(-25*time.Hour), 24); err != nil {
			return err
		}
		l := NetworkLog{ID: "future", At: now}
		if err := tx.Put(networkLogsBucket, networkLogKey("a", 1, l), l); err != nil {
			return err
		}
		return networkPutLogExpiry(tx, "a", 1, now, 24)
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.pruneNetwork(ctx); err != nil {
		t.Fatal(err)
	}
	assertNetworkRows(t, store, networkLogsBucket, 202) // 4 batches of 200
	// A fresh engine resumes the prefix marker left by the prior batch.
	e = New(store, nil)
	e.now = func() time.Time { return now }
	if err := e.pruneNetwork(ctx); err != nil {
		t.Fatal(err)
	}
	assertNetworkRows(t, store, networkLogsBucket, 1)
	assertNetworkRows(t, store, networkExpiryBucket, 1)
}

func TestNetworkShorterLogRetentionExpiresExistingRows(t *testing.T) {
	store := storage.NewMemory()
	t.Cleanup(store.Close)
	now := time.Date(2026, 10, 29, 10, 15, 0, 0, time.UTC)
	e, w := networkEngineFixture(t, store, now.Add(-4*time.Hour))
	if err := e.flushNetwork(context.Background(), w, true); err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return now }
	settings := w.settings
	settings.LogRetentionHours, settings.Enabled = 1, false
	if _, err := e.UpdateNetworkSettings(context.Background(), w.rec.Service.ID, settings, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := e.pruneNetwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertNetworkRows(t, store, networkLogsBucket, 0)
	assertNetworkRows(t, store, networkUsageBucket, 1)
}

func TestNetworkViewMembershipPaginationAndOldSnapshotInvalidation(t *testing.T) {
	store := storage.NewMemory()
	t.Cleanup(store.Close)
	now := time.Date(2026, 10, 29, 10, 15, 0, 0, time.UTC)
	e, w := networkEngineFixture(t, store, now)
	w.view.Nodes = []NetworkNode{
		{ID: "one", Name: "one", Type: "AnyTLS", State: "healthy", Loaded: true, CheckedAt: &now, Subscriptions: []NetworkNodeSubscription{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}},
		{ID: "two", Name: "two", Type: "AnyTLS", State: "healthy", Loaded: true, CheckedAt: &now, Subscriptions: []NetworkNodeSubscription{{ID: "b", Name: "B"}}},
	}
	w.view.Subscriptions = []NetworkSubscription{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	e.network = &networkRuntime{workers: map[string]*networkWorker{w.rec.Service.ID: w}}
	q := NetworkQuery{Section: "nodes", Subscription: "b", Limit: 1}
	v, err := e.NetworkView(context.Background(), w.rec.Service.ID, q)
	if err != nil || v.Total != 2 || len(v.Nodes) != 1 || !v.HasMore || v.Overview.NodeCount != 2 || v.Overview.HealthyNodes != 2 {
		t.Fatalf("membership pagination/overview: %+v %v", v, err)
	}
	q.Cursor = v.NextCursor
	v, err = e.NetworkView(context.Background(), w.rec.Service.ID, q)
	if err != nil || v.HasMore || len(v.Nodes) != 1 || v.Nodes[0].ID != "two" {
		t.Fatalf("last page: %+v %v", v, err)
	}
	if err := e.flushNetwork(context.Background(), w, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func(tx storage.Tx) error {
		var snapshot NetworkView
		if err := tx.Get(networkSnapshotBucket, w.rec.Service.ID, &snapshot); err != nil {
			return err
		}
		snapshot.NodeIdentityVersion = 0
		return tx.Put(networkSnapshotBucket, w.rec.Service.ID, snapshot)
	}); err != nil {
		t.Fatal(err)
	}
	e.network = nil
	v, err = e.NetworkView(context.Background(), w.rec.Service.ID, NetworkQuery{Section: "nodes"})
	if err != nil || len(v.Nodes) != 0 || v.Overview.NodeCount != 0 || v.State != "stale" {
		t.Fatalf("legacy duplicate snapshot surfaced: %+v %v", v, err)
	}
}

func TestNetworkGenerationConflictAndImportDoesNotTrustHealth(t *testing.T) {
	store := storage.NewMemory()
	t.Cleanup(store.Close)
	now := time.Date(2026, 10, 29, 10, 15, 0, 0, time.UTC)
	e, w := networkEngineFixture(t, store, now)
	settings := w.settings
	settings.Generation++
	if _, err := e.UpdateNetworkSettings(context.Background(), w.rec.Service.ID, settings, "admin"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation accepted: %v", err)
	}
	input := NetworkImport{Generation: w.rec.Service.Generation, Subscriptions: []NetworkSubscription{{Name: "import", Nodes: []NetworkNode{{Name: "same", Type: "AnyTLS", State: "healthy", Loaded: true, CheckedAt: &now}}}}}
	saved, err := e.ImportNetworkSubscriptions(context.Background(), w.rec.Service.ID, input, "admin")
	if err != nil {
		t.Fatal(err)
	}
	node := saved.ImportedSubscriptions[0].Nodes[0]
	if node.Loaded || node.State != "unknown" || node.CheckedAt != nil {
		t.Fatalf("import forged live evidence: %+v", node)
	}
	if err := e.flushNetwork(context.Background(), w, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("old worker wrote after settings revision change: %v", err)
	}
	assertNetworkRows(t, store, networkSnapshotBucket, 0)
}
