package monitor

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"relayservice/internal/storage"
)

func retentionFixture(t *testing.T) (*Engine, *storage.Memory, time.Time) {
	t.Helper()
	store := storage.NewMemory()
	e := New(store, nil)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	return e, store, now
}

func approveRetention(t *testing.T, e *Engine, days, auditDays int) RetentionPolicy {
	t.Helper()
	ctx := context.Background()
	old, err := e.Retention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := e.PreviewRetention(ctx, "admin", RetentionPolicy{HistoryDays: days, AuditDays: auditDays, Generation: old.Generation})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := e.ConfirmRetention(ctx, "admin", preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func seedRetentionCheck(tx storage.Tx, c Check) error {
	if err := tx.Put(historyBucket, historyKey(c.ServiceID, c.Cursor), c); err != nil {
		return err
	}
	return tx.Put(checkIndexBucket, c.ServiceID+"/"+c.ID, c.Cursor)
}

func TestRetentionRequiresFreshSingleUsePreview(t *testing.T) {
	e, store, now := retentionFixture(t)
	ctx := context.Background()
	c := Check{ID: "old", ServiceID: "s", Cursor: sequenceKey(1), Status: StatusHealthy, CompletedAt: now.Add(-31 * 24 * time.Hour)}
	if err := store.Update(ctx, func(tx storage.Tx) error { return seedRetentionCheck(tx, c) }); err != nil {
		t.Fatal(err)
	}
	if err := e.PruneRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Check(ctx, "s", "old"); err != nil {
		t.Fatal("default policy removed history", err)
	}
	if _, err := e.ConfirmRetention(ctx, "admin", "invented"); !errors.Is(err, ErrConflict) {
		t.Fatalf("confirmation without preview: %v", err)
	}
	for _, p := range []RetentionPolicy{{HistoryDays: -1}, {AuditDays: 3651}, {HistoryDays: 30, Generation: 3}} {
		if _, err := e.PreviewRetention(ctx, "admin", p); err == nil {
			t.Fatalf("invalid/stale policy accepted: %+v", p)
		}
	}
	preview, err := e.PreviewRetention(ctx, "admin", RetentionPolicy{HistoryDays: 30})
	if err != nil || preview.HistoryCount != 1 || preview.AuditBefore != nil {
		t.Fatalf("preview %+v: %v", preview, err)
	}
	if err := e.PruneRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Check(ctx, "s", "old"); err != nil {
		t.Fatal("preview removed history", err)
	}
	if _, err := e.ConfirmRetention(ctx, "device", preview.ID); !errors.Is(err, ErrValidation) {
		t.Fatal("device confirmation accepted", err)
	}
	e.now = func() time.Time { return now.Add(5 * time.Minute) }
	if _, err := e.ConfirmRetention(ctx, "admin", preview.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("expired preview accepted", err)
	}
	e.now = func() time.Time { return now }
	// A newer preview supersedes the older authorization.
	next, err := e.PreviewRetention(ctx, "admin", RetentionPolicy{HistoryDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ConfirmRetention(ctx, "admin", preview.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("superseded preview accepted", err)
	}
	policy, err := e.ConfirmRetention(ctx, "admin", next.ID)
	if err != nil || policy.Generation != 1 || policy.UpdatedBy != "admin" {
		t.Fatalf("policy %+v: %v", policy, err)
	}
	if _, err := e.ConfirmRetention(ctx, "admin", next.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("preview replay accepted", err)
	}
}

func TestRetentionPreservesAlertsSnapshotsBoundaryAndPending(t *testing.T) {
	e, store, now := retentionFixture(t)
	ctx := context.Background()
	cutoff := now.Add(-30 * 24 * time.Hour)
	checks := []Check{
		{ID: "expired", Cursor: sequenceKey(1), ServiceID: "s", Status: StatusHealthy, QueuedAt: cutoff.Add(-time.Hour), CompletedAt: cutoff.Add(-time.Second)},
		{ID: "boundary", Cursor: sequenceKey(2), ServiceID: "s", Status: StatusUnhealthy, CompletedAt: cutoff},
		{ID: "queued", Cursor: sequenceKey(3), ServiceID: "s", Status: StatusQueued, QueuedAt: cutoff.Add(-time.Hour)},
		{ID: "running", Cursor: sequenceKey(4), ServiceID: "s", Status: StatusRunning, StartedAt: cutoff.Add(-time.Hour)},
		{ID: "still-pending", Cursor: sequenceKey(5), ServiceID: "s", Status: StatusInterrupted, CompletedAt: cutoff.Add(-time.Hour)},
	}
	err := store.Update(ctx, func(tx storage.Tx) error {
		for _, c := range checks {
			if err := seedRetentionCheck(tx, c); err != nil {
				return err
			}
		}
		if err := tx.Put(queueBucket, checks[4].Cursor, pending{Check: checks[4]}); err != nil {
			return err
		}
		if err := tx.Put(operationBucket, operationKey("s", "old-op"), checks[0]); err != nil {
			return err
		}
		if err := tx.Put(stateBucket, "s", state{Current: &checks[0], LastSuccess: &checks[0]}); err != nil {
			return err
		}
		if err := tx.Put(alertsBucket, sequenceKey(1), Alert{Cursor: 1, ServiceID: "s", CheckID: "expired", StartedAt: cutoff.Add(-time.Hour)}); err != nil {
			return err
		}
		return storage.AppendAudit(tx, storage.AuditRecord{ActorID: "admin", TargetID: "s", Action: "old", At: cutoff.Add(-time.Hour)})
	})
	if err != nil {
		t.Fatal(err)
	}
	approveRetention(t, e, 30, 30)
	if err := e.PruneRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Check(ctx, "s", "expired"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired history/index retained", err)
	}
	for _, c := range checks[1:] {
		if _, err := e.Check(ctx, "s", c.ID); err != nil {
			t.Fatalf("protected %s removed: %v", c.ID, err)
		}
	}
	if err := store.View(ctx, func(tx storage.Tx) error {
		var st state
		if err := tx.Get(stateBucket, "s", &st); err != nil || st.Current == nil || st.LastSuccess == nil {
			return fmt.Errorf("snapshot lost: %v", err)
		}
		var operation Check
		if err := tx.Get(operationBucket, operationKey("s", "old-op"), &operation); !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("dangling operation retained: %v", err)
		}
		var a storage.AuditRecord
		if err := tx.Get(auditBucket, sequenceKey(1), &a); !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("expired audit retained: %v", err)
		}
		rows, err := tx.Scan(auditBucket, storage.Scan{})
		if err != nil || len(rows) != 1 {
			return fmt.Errorf("policy confirmation audit lost: %d %v", len(rows), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	alerts, err := e.Alerts(ctx, "", 100)
	if err != nil || len(alerts) != 1 || alerts[0].CheckID != "expired" {
		t.Fatal("permanent alert removed", alerts, err)
	}
}

func TestRetentionBatchResumeAndDisable(t *testing.T) {
	e, store, now := retentionFixture(t)
	ctx := context.Background()
	count := retentionBatchSize*2 + 1
	if err := store.Update(ctx, func(tx storage.Tx) error {
		for i := 1; i <= count; i++ {
			c := Check{ID: fmt.Sprint(i), Cursor: sequenceKey(uint64(i)), ServiceID: "s", Status: StatusHealthy, CompletedAt: now.Add(-48 * time.Hour)}
			if err := seedRetentionCheck(tx, c); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	approveRetention(t, e, 1, 0)
	if err := e.PruneRetention(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := e.History(ctx, "s", "", 500)
	if err != nil || len(items) != count-retentionBatchSize {
		t.Fatalf("batch not bounded: %d %v", len(items), err)
	}
	approveRetention(t, e, 0, 0)
	if err := e.PruneRetention(ctx); err != nil {
		t.Fatal(err)
	}
	items, _ = e.History(ctx, "s", "", 500)
	if len(items) != count-retentionBatchSize {
		t.Fatal("disabled retention removed records")
	}
	approveRetention(t, e, 1, 0)
	// Resume progress is durable across Engine instances.
	for i := 0; i < 2; i++ {
		restarted := New(store, nil)
		restarted.now = e.now
		if err := restarted.PruneRetention(ctx); err != nil {
			t.Fatal(err)
		}
	}
	items, err = e.History(ctx, "s", "", 500)
	if err != nil || len(items) != 0 {
		t.Fatal("resumed sweep incomplete", len(items), err)
	}
}

type retentionFaultStore struct {
	storage.Store
	fail bool
}

type retentionFaultTx struct {
	storage.Tx
}

func (s *retentionFaultStore) Update(ctx context.Context, fn func(storage.Tx) error) error {
	return s.Store.Update(ctx, func(tx storage.Tx) error {
		if s.fail {
			return fn(retentionFaultTx{tx})
		}
		return fn(tx)
	})
}

func (tx retentionFaultTx) Delete(bucket, key string) error {
	if bucket == checkIndexBucket {
		return errors.New("injected index deletion failure")
	}
	return tx.Tx.Delete(bucket, key)
}

func TestRetentionDeletionFailureRollsBackHistoryAndProgress(t *testing.T) {
	e, store, now := retentionFixture(t)
	ctx := context.Background()
	c := Check{ID: "old", Cursor: sequenceKey(1), ServiceID: "s", Status: StatusHealthy, CompletedAt: now.Add(-48 * time.Hour)}
	if err := store.Update(ctx, func(tx storage.Tx) error { return seedRetentionCheck(tx, c) }); err != nil {
		t.Fatal(err)
	}
	approveRetention(t, e, 1, 0)
	fault := &retentionFaultStore{Store: store, fail: true}
	e.store = fault
	if err := e.PruneRetention(ctx); err == nil {
		t.Fatal("injected failure lost")
	}
	if _, err := e.Check(ctx, "s", "old"); err != nil {
		t.Fatal("partial deletion committed", err)
	}
	fault.fail = false
	if err := e.PruneRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Check(ctx, "s", "old"); !errors.Is(err, ErrNotFound) {
		t.Fatal("retry did not clean history", err)
	}
}
