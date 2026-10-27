package storage

import (
	"context"
	"testing"
	"time"
)

func TestMemoryCountUsesTimestampAndExcludesStatuses(t *testing.T) {
	store := NewMemory()
	defer store.Close()
	cutoff := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	if err := store.Update(context.Background(), func(tx Tx) error {
		if err := tx.Put("history", "old", map[string]any{"completed_at": cutoff.Add(-time.Hour), "status": "healthy"}); err != nil {
			return err
		}
		if err := tx.Put("history", "queued", map[string]any{"queued_at": cutoff.Add(-time.Hour), "status": "queued"}); err != nil {
			return err
		}
		if err := tx.Put("history", "new", map[string]any{"completed_at": cutoff.Add(time.Hour), "status": "healthy"}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var got int64
	if err := store.View(context.Background(), func(tx Tx) error {
		var err error
		got, err = tx.Count("history", CountOptions{Before: cutoff, TimeField: "completed_at", StatusField: "status", ExcludeStatuses: []string{"queued", "running"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("count=%d, want 1", got)
	}
}
