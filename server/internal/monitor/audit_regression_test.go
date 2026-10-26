package monitor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"relayservice/internal/storage"
)

func seedAlertCursors(t *testing.T, e *Engine, cursors ...uint64) {
	t.Helper()
	if err := e.store.Update(context.Background(), func(tx storage.Tx) error {
		for _, cursor := range cursors {
			if err := tx.Put(alertsBucket, sequenceKey(cursor), Alert{Cursor: cursor, ID: fmt.Sprint(cursor), ServiceID: "service", Kind: "failure", StartedAt: e.now()}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAckRejectsUnreadSkipsAndMalformedBatches(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cursor   uint64
		observed []uint64
	}{
		{name: "cursor only jumps to last", cursor: 8},
		{name: "batch starts on later page", cursor: 8, observed: []uint64{5, 8}},
		{name: "batch omits an unread event", cursor: 8, observed: []uint64{2, 8}},
		{name: "duplicate", cursor: 8, observed: []uint64{2, 5, 5, 8}},
		{name: "descending", cursor: 8, observed: []uint64{5, 2, 8}},
		{name: "zero", cursor: 8, observed: []uint64{0, 2, 5, 8}},
		{name: "invented event", cursor: 8, observed: []uint64{2, 3, 5, 8}},
		{name: "missing target", cursor: 9, observed: []uint64{2, 5, 8, 9}},
		{name: "target differs from last", cursor: 8, observed: []uint64{2, 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := testEngine(t)
			seedAlertCursors(t, e, 2, 5, 8)
			ctx := context.Background()
			if err := e.Ack(ctx, "device", tc.cursor, tc.observed...); !errors.Is(err, ErrValidation) {
				t.Fatalf("invalid acknowledgment err=%v", err)
			}
			if got, err := e.AckCursor(ctx, "device"); err != nil || got != 0 {
				t.Fatalf("rejected acknowledgment mutated cursor=%d err=%v", got, err)
			}
		})
	}
}

func TestAckFollowsPersistedOrderAndKeepsEventsAndOtherPrincipals(t *testing.T) {
	e, _ := testEngine(t)
	seedAlertCursors(t, e, 2, 5, 8)
	ctx := context.Background()
	if err := e.Ack(ctx, "device-a", 2); err != nil {
		t.Fatalf("next persisted cursor after numeric gap: %v", err)
	}
	if err := e.Ack(ctx, "device-a", 8); !errors.Is(err, ErrValidation) {
		t.Fatalf("skipped unread event: %v", err)
	}
	if err := e.Ack(ctx, "device-a", 8, 2, 5, 8); err != nil {
		t.Fatalf("concurrent already-read prefix should succeed: %v", err)
	}
	if err := e.Ack(ctx, "device-a", 8, 2, 5, 8); err != nil {
		t.Fatalf("lost-response retry should be idempotent: %v", err)
	}
	if err := e.Ack(ctx, "device-a", 5, 2, 5); !errors.Is(err, ErrConflict) {
		t.Fatalf("cursor rollback err=%v", err)
	}
	if got, err := e.AckCursor(ctx, "device-a"); err != nil || got != 8 {
		t.Fatalf("device-a cursor=%d err=%v", got, err)
	}
	if got, err := e.AckCursor(ctx, "device-b"); err != nil || got != 0 {
		t.Fatalf("device-b cursor=%d err=%v", got, err)
	}
	events, err := e.Alerts(ctx, "", 100)
	if err != nil || len(events) != 3 {
		t.Fatalf("acknowledgment deleted alert history=%v err=%v", events, err)
	}
}

func TestAckBatchBoundAndConcurrentRequests(t *testing.T) {
	e, _ := testEngine(t)
	cursors := make([]uint64, 501)
	for i := range cursors {
		cursors[i] = uint64(i + 1)
	}
	seedAlertCursors(t, e, cursors...)
	ctx := context.Background()
	if err := e.Ack(ctx, "device", 501, cursors...); !errors.Is(err, ErrValidation) {
		t.Fatalf("oversize batch accepted: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.Ack(ctx, "device", 500, cursors[:500]...)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent same-page acknowledgment failed: %v", err)
		}
	}
	if got, err := e.AckCursor(ctx, "device"); err != nil || got != 500 {
		t.Fatalf("cursor=%d err=%v", got, err)
	}
	if err := e.Ack(ctx, "device", 501); err != nil {
		t.Fatalf("next page's event failed: %v", err)
	}
}

func TestAckRejectsInventedAlreadyReadPrefix(t *testing.T) {
	e, _ := testEngine(t)
	seedAlertCursors(t, e, 2, 5, 8)
	ctx := context.Background()
	if err := e.Ack(ctx, "device", 2); err != nil {
		t.Fatal(err)
	}
	if err := e.Ack(ctx, "device", 8, 1, 2, 5, 8); !errors.Is(err, ErrValidation) {
		t.Fatalf("invented prefix accepted: %v", err)
	}
	if got, _ := e.AckCursor(ctx, "device"); got != 2 {
		t.Fatalf("rejected batch advanced cursor to %d", got)
	}
}

func TestRestartDurationUsesRecordedStartAndPreservesHealth(t *testing.T) {
	for _, tc := range []struct {
		name        string
		startOffset time.Duration
		missing     bool
		wantMS      int64
	}{
		{name: "elapsed", startOffset: -2750 * time.Millisecond, wantMS: 2750},
		{name: "missing start", missing: true},
		{name: "clock moved backward", startOffset: time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s := testEngine(t)
			ctx := context.Background()
			complete(t, e, s, "last-good", StatusHealthy)
			q, err := e.RequestCheck(ctx, s.ID, "running", s.Generation)
			if err != nil {
				t.Fatal(err)
			}
			c, _, found, err := e.claim(ctx)
			if err != nil || !found {
				t.Fatalf("claim found=%v err=%v", found, err)
			}
			c.StartedAt = e.now().Add(tc.startOffset)
			if tc.missing {
				c.StartedAt = time.Time{}
			}
			if err := e.store.Update(ctx, func(tx storage.Tx) error {
				return tx.Put(queueBucket, c.Cursor, pending{Check: c})
			}); err != nil {
				t.Fatal(err)
			}
			if err := e.recoverInterrupted(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := e.Check(ctx, s.ID, q.ID)
			if err != nil || got.Status != StatusInterrupted || got.DurationMS != tc.wantMS || !got.CompletedAt.Equal(e.now()) {
				t.Fatalf("recovered check=%+v err=%v", got, err)
			}
			service, err := e.Get(ctx, s.ID)
			if err != nil || service.Status != StatusHealthy || service.OpenIncident || service.LastSuccess == nil || service.Current.ID == q.ID {
				t.Fatalf("interruption changed target health=%+v err=%v", service, err)
			}
			if err := e.recoverInterrupted(ctx); err != nil {
				t.Fatal(err)
			}
			history, err := e.History(ctx, s.ID, "", 100)
			if err != nil || len(history) != 2 {
				t.Fatalf("repeat recovery changed history=%+v err=%v", history, err)
			}
		})
	}
}

func TestFreshnessExactlyTwoConfiguredIntervals(t *testing.T) {
	last := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	if got := (Service{IntervalSeconds: 60}).Freshness(last); got != "never_observed" {
		t.Fatalf("missing observation freshness=%s", got)
	}
	for _, interval := range []int{10, 60, 86400} {
		s := Service{IntervalSeconds: interval, LastCheckAt: &last}
		boundary := last.Add(2 * time.Duration(interval) * time.Second)
		if got := s.Freshness(boundary); got != "fresh" {
			t.Errorf("interval=%d exact boundary=%s", interval, got)
		}
		if got := s.Freshness(boundary.Add(time.Nanosecond)); got != "stale" {
			t.Errorf("interval=%d after boundary=%s", interval, got)
		}
	}
}

func TestExpiredOperationRetriesIgnoreBackgroundCleanupPosition(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(fmt.Sprintf("disable=%v", disable), func(t *testing.T) {
			e, s := testEngine(t)
			ctx := context.Background()
			old := complete(t, e, s, "original", StatusHealthy)
			now := e.now()
			e.now = func() time.Time { return now.Add(48 * time.Hour) }
			operationIDs := map[string]string{operationKey(s.ID, "original"): "original"}
			if err := e.store.Update(ctx, func(tx storage.Tx) error {
				// More than a cleanup batch reference one observation, leaving
				// both swept and unswept handles after history is removed.
				for i := 0; i < retentionBatchSize+50; i++ {
					id := fmt.Sprintf("old-op-%03d", i)
					key := operationKey(s.ID, id)
					operationIDs[key] = id
					if err := tx.Put(operationBucket, key, old); err != nil {
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
			if _, err := e.Check(ctx, s.ID, old.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("old history should be expired: %v", err)
			}
			remaining := map[string]bool{}
			if err := e.store.View(ctx, func(tx storage.Tx) error {
				rows, err := tx.Scan(operationBucket, storage.Scan{Limit: 500})
				for _, row := range rows {
					remaining[row.Key] = true
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if len(remaining) != len(operationIDs)-retentionBatchSize {
				t.Fatalf("expected partially swept handles: %d", len(remaining))
			}
			if disable {
				approveRetention(t, e, 0, 0)
			}
			var unsweptID, sweptID string
			for key, id := range operationIDs {
				if remaining[key] {
					unsweptID = id
				} else {
					sweptID = id
				}
			}
			first, err := e.RequestCheck(ctx, s.ID, unsweptID, s.Generation)
			if err != nil || first.ID == old.ID || first.Status != StatusQueued {
				t.Fatalf("unswept expired handle should schedule new work: %+v %v", first, err)
			}
			second, err := e.RequestCheck(ctx, s.ID, sweptID, s.Generation)
			if err != nil || second.ID != first.ID {
				t.Fatalf("swept handle should reuse new in-flight work: %+v %v", second, err)
			}
			repeat, err := e.RequestCheck(ctx, s.ID, unsweptID, s.Generation)
			if err != nil || repeat.ID != first.ID {
				t.Fatalf("new retained operation lost idempotency: %+v %v", repeat, err)
			}
			if err := e.store.View(ctx, func(tx storage.Tx) error {
				rows, err := tx.Scan(queueBucket, storage.Scan{Limit: 500})
				if err != nil || len(rows) != 1 {
					return fmt.Errorf("duplicate queue entries=%d err=%v", len(rows), err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMissingOperationHistoryNeverDuplicatesPendingWork(t *testing.T) {
	e, s := testEngine(t)
	ctx := context.Background()
	queued, err := e.RequestCheck(ctx, s.ID, "pending", s.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Update(ctx, func(tx storage.Tx) error {
		return tx.Delete(historyBucket, historyKey(s.ID, queued.Cursor))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RequestCheck(ctx, s.ID, "pending", s.Generation); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing history with pending work should conflict: %v", err)
	}
	if err := e.store.View(ctx, func(tx storage.Tx) error {
		var original Check
		if err := tx.Get(operationBucket, operationKey(s.ID, "pending"), &original); err != nil || original.ID != queued.ID {
			return fmt.Errorf("original operation replaced: %+v %v", original, err)
		}
		rows, err := tx.Scan(queueBucket, storage.Scan{Limit: 500})
		if err != nil || len(rows) != 1 {
			return fmt.Errorf("pending work duplicated: %d %v", len(rows), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredOperationKeepsGenerationConflict(t *testing.T) {
	e, s := testEngine(t)
	ctx := context.Background()
	old := complete(t, e, s, "old-generation", StatusHealthy)
	if err := e.store.Update(ctx, func(tx storage.Tx) error {
		return tx.Delete(historyBucket, historyKey(s.ID, old.Cursor))
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := e.Save(ctx, "admin", ServiceInput{ID: s.ID, Generation: s.Generation, Name: s.Name, Kind: s.Kind, BaseURL: s.BaseURL, Enabled: true, AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RequestCheck(ctx, s.ID, "old-generation", updated.Generation); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired old-generation operation was reused: %v", err)
	}
}

func TestAlertEvidenceFollowsHistoryRetentionWithoutDeletingEvent(t *testing.T) {
	e, s := testEngine(t)
	ctx := context.Background()
	complete(t, e, s, "failure-1", StatusUnhealthy)
	complete(t, e, s, "failure-2", StatusUnhealthy)
	events, err := e.Alerts(ctx, "", 100)
	if err != nil || len(events) != 1 || events[0].EvidenceState != "available" {
		t.Fatalf("existing evidence=%+v err=%v", events, err)
	}
	old := events[0]
	now := e.now()
	e.now = func() time.Time { return now.Add(48 * time.Hour) }
	approveRetention(t, e, 1, 0)
	if err := e.PruneRetention(ctx); err != nil {
		t.Fatal(err)
	}
	events, err = e.Alerts(ctx, "", 100)
	if err != nil || len(events) != 1 || events[0].EvidenceState != "expired" || events[0].Message != old.Message || events[0].CheckID != old.CheckID {
		t.Fatalf("retained event must describe expired evidence=%+v err=%v", events, err)
	}
	approveRetention(t, e, 0, 0)
	events, err = e.Alerts(ctx, "", 100)
	if err != nil || len(events) != 1 || events[0].EvidenceState != "unavailable" {
		t.Fatalf("disabled policy must not invent expiration=%+v err=%v", events, err)
	}
}

func TestAlertMissingEvidenceRequiresNonzeroTimeBeforeCutoff(t *testing.T) {
	e, _ := testEngine(t)
	ctx := context.Background()
	approveRetention(t, e, 1, 0)
	cutoff := e.now().Add(-24 * time.Hour)
	starts := []time.Time{time.Time{}, cutoff, cutoff.Add(-time.Nanosecond), e.now()}
	if err := e.store.Update(ctx, func(tx storage.Tx) error {
		for i, start := range starts {
			cursor := uint64(i + 1)
			if err := tx.Put(alertsBucket, sequenceKey(cursor), Alert{Cursor: cursor, ServiceID: "s", CheckID: "missing", StartedAt: start}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	events, err := e.Alerts(ctx, "", 100)
	if err != nil || len(events) != len(starts) {
		t.Fatalf("alerts=%+v err=%v", events, err)
	}
	for i, want := range []string{"unavailable", "unavailable", "expired", "unavailable"} {
		if events[i].EvidenceState != want {
			t.Errorf("alert %d evidence=%s want %s", i, events[i].EvidenceState, want)
		}
	}
}

type alertReadFaultStore struct {
	storage.Store
	bucket string
	err    error
}

func (s alertReadFaultStore) View(ctx context.Context, fn func(storage.Tx) error) error {
	return s.Store.View(ctx, func(tx storage.Tx) error {
		return fn(alertReadFaultTx{Tx: tx, bucket: s.bucket, err: s.err})
	})
}

type alertReadFaultTx struct {
	storage.Tx
	bucket string
	err    error
}

func (tx alertReadFaultTx) Get(bucket, key string, value any) error {
	if bucket == tx.bucket {
		return tx.err
	}
	return tx.Tx.Get(bucket, key, value)
}

func TestAlertEvidenceStorageFailuresAreNotHidden(t *testing.T) {
	for _, bucket := range []string{retentionBucket, checkIndexBucket} {
		t.Run(bucket, func(t *testing.T) {
			e, _ := testEngine(t)
			seedAlertCursors(t, e, 1)
			injected := errors.New("injected read failure")
			e.store = alertReadFaultStore{Store: e.store, bucket: bucket, err: injected}
			if _, err := e.Alerts(context.Background(), "", 100); !errors.Is(err, injected) {
				t.Fatalf("evidence read failure hidden: %v", err)
			}
		})
	}
}
