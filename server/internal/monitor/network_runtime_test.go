package monitor

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"relayservice/internal/storage"
)

func networkEngineFixture(t *testing.T, store storage.Store, now time.Time) (*Engine, *networkWorker) {
	t.Helper()
	e := New(store, nil)
	e.now = func() time.Time { return now }
	s, err := e.Save(context.Background(), "admin", ServiceInput{Name: "mihomo", Kind: KindMihomo, BaseURL: "http://127.0.0.1:9090", Enabled: true, AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	w := newNetworkTestWorker(now)
	w.rec = serviceRecord{Service: s}
	w.historyGeneration = s.Generation
	w.settings = networkDefaults(s.Generation)
	w.settings.PersistLogs = true
	w.view = emptyNetworkView(s.ID, s.Generation)
	w.view.State, w.view.CollectedAt = "available", &now
	w.lastMinute = now.Truncate(time.Minute)
	w.pendingUsage = NetworkUsage{UploadBytes: 12, DownloadBytes: 34, Samples: 1}
	w.pendingNodes["node"] = NetworkNodeSample{At: now, State: "healthy", Freshness: "fresh", CheckedAt: &now}
	w.appendNetworkLog(now, "info", "core", "[TCP] --> pipio.io:443 using 台湾1")
	w.pendingBreakdown = map[string]map[string]NetworkUsageGroup{now.UTC().Format("2006010215"): {"rule": {Dimension: "rule", Label: "Match", UploadBytes: 12, DownloadBytes: 34, Samples: 1}}}
	// PersistLogs is an explicit administrator choice, never inferred by flush.
	if _, err := e.UpdateNetworkSettings(context.Background(), s.ID, w.settings, "admin"); err != nil {
		t.Fatal(err)
	}
	w.revision = 1
	return e, w
}

func assertNetworkRows(t *testing.T, store storage.Store, bucket string, want int) {
	t.Helper()
	if err := store.View(context.Background(), func(tx storage.Tx) error {
		rows, err := boundedNetworkScan(tx, bucket, storage.Scan{}, 5000)
		if err != nil {
			return err
		}
		if len(rows) != want {
			t.Errorf("bucket %s has %d rows want %d", bucket, len(rows), want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkFlushExpirySurvivesSQLiteRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 10, 15, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "relay.sqlite")
	store, err := storage.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	e, w := networkEngineFixture(t, store, now)
	if err := e.flushNetwork(ctx, w, true); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{networkUsageBucket, networkNodeBucket, networkLogsBucket, networkBreakdownBucket, networkSnapshotBucket} {
		assertNetworkRows(t, store, bucket, 1)
	}
	assertNetworkRows(t, store, networkExpiryBucket, 4)
	if len(w.pendingNodes) != 0 || len(w.pendingLogs) != 0 || w.pendingUsage.Samples != 0 || len(w.pendingBreakdown) != 0 {
		t.Fatal("committed pending data not cleared")
	}
	store.Close()
	reopened, err := storage.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	store = reopened
	e = New(store, nil)
	e.now = func() time.Time { return now.Add(26 * time.Hour) }
	if err := e.pruneNetwork(ctx); err != nil {
		t.Fatal(err)
	}
	assertNetworkRows(t, store, networkLogsBucket, 0)
	assertNetworkRows(t, store, networkUsageBucket, 1)
	e.now = func() time.Time { return now.Add(170 * time.Hour) }
	if err := e.pruneNetwork(ctx); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{networkUsageBucket, networkNodeBucket, networkLogsBucket, networkBreakdownBucket, networkExpiryBucket} {
		assertNetworkRows(t, store, bucket, 0)
	}
	assertNetworkRows(t, store, networkSnapshotBucket, 1)
	assertNetworkRows(t, store, servicesBucket, 1)
}

type networkWriteFaultStore struct {
	storage.Store
	fail bool
}
type networkWriteFaultTx struct{ storage.Tx }

func (s *networkWriteFaultStore) Update(ctx context.Context, fn func(storage.Tx) error) error {
	return s.Store.Update(ctx, func(tx storage.Tx) error {
		if s.fail {
			return fn(networkWriteFaultTx{tx})
		}
		return fn(tx)
	})
}
func (tx networkWriteFaultTx) Put(bucket, key string, value any) error {
	if bucket == networkExpiryBucket {
		return errors.New("injected expiry write failure")
	}
	return tx.Tx.Put(bucket, key, value)
}

func TestNetworkFlushRollsBackDataAndKeepsPendingOnExpiryFailure(t *testing.T) {
	store := &networkWriteFaultStore{Store: storage.NewMemory()}
	t.Cleanup(store.Close)
	now := time.Date(2026, 10, 29, 10, 15, 0, 0, time.UTC)
	e, w := networkEngineFixture(t, store, now)
	store.fail = true
	if err := e.flushNetwork(context.Background(), w, true); err == nil {
		t.Fatal("injected expiry failure ignored")
	}
	assertNetworkRows(t, store, networkUsageBucket, 0)
	assertNetworkRows(t, store, networkSnapshotBucket, 0)
	if w.pendingUsage.Samples != 1 || len(w.pendingNodes) != 1 || len(w.pendingLogs) != 1 || len(w.pendingBreakdown) != 1 {
		t.Fatal("uncommitted pending evidence discarded")
	}
	store.fail = false
	if err := e.flushNetwork(context.Background(), w, true); err != nil {
		t.Fatal(err)
	}
	// Retrying an already cleared batch must not count its usage twice.
	if err := e.flushNetwork(context.Background(), w, true); err != nil {
		t.Fatal(err)
	}
	if err := store.View(context.Background(), func(tx storage.Tx) error {
		var hour networkHourUsage
		if err := tx.Get(networkUsageBucket, networkHourKey(w.rec.Service.ID, w.historyGeneration, now), &hour); err != nil {
			return err
		}
		if len(hour.Items) != 1 || hour.Items[0].UploadBytes != 12 || hour.Items[0].Samples != 1 {
			t.Fatalf("retry duplicated usage: %+v", hour)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
