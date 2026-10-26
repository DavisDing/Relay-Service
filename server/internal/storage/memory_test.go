package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryTransactionsCommitAtomicallyAndRespectView(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()
	want := errors.New("rollback")
	if err := store.Update(ctx, func(tx Tx) error {
		if err := tx.Put("x", "one", map[string]int{"v": 1}); err != nil {
			return err
		}
		return want
	}); !errors.Is(err, want) {
		t.Fatalf("Update error=%v", err)
	}
	if err := store.View(ctx, func(tx Tx) error {
		var value map[string]int
		if err := tx.Get("x", "one", &value); !errors.Is(err, ErrNotFound) {
			t.Fatalf("transaction leaked: %v %v", value, err)
		}
		if err := tx.Put("x", "two", 1); !errors.Is(err, ErrReadOnly) {
			t.Fatalf("read-only Put=%v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, func(tx Tx) error {
		one, err := tx.Next("events")
		if err != nil {
			return err
		}
		two, err := tx.Next("events")
		if err != nil {
			return err
		}
		if one != 1 || two != 2 {
			t.Fatalf("sequences %d %d", one, two)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.View(ctx, func(tx Tx) error {
		n, err := tx.Next("events")
		if !errors.Is(err, ErrReadOnly) {
			t.Fatalf("read-only Next=%d err=%v", n, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryScanIsBoundedAndExclusive(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()
	if err := store.Update(ctx, func(tx Tx) error {
		for i := 0; i < 5; i++ {
			if err := tx.Put("b", string(rune('a'+i)), i); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.View(ctx, func(tx Tx) error {
		items, err := tx.Scan("b", Scan{After: "b", Limit: 2})
		if err != nil {
			return err
		}
		if len(items) != 2 || items[0].Key != "c" || items[1].Key != "d" {
			t.Fatalf("page %#v", items)
		}
		items, err = tx.Scan("b", Scan{After: "d", Reverse: true, Limit: 3})
		if err != nil {
			return err
		}
		if len(items) != 3 || items[0].Key != "c" || items[2].Key != "a" {
			t.Fatalf("reverse %#v", items)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryDataVersionTracksCommittedTransactions(t *testing.T) {
	exerciseDataVersion(t, NewMemory())
}

type versionedStore interface {
	Store
	DataVersion(context.Context) (uint64, error)
}

// Shared with the opt-in PostgreSQL suite: clients use this version to refresh
// cached data, so reads/failed transactions must not publish a new version.
func exerciseDataVersion(t *testing.T, store versionedStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	baseline, err := store.DataVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertVersion := func(want uint64) {
		t.Helper()
		got, err := store.DataVersion(ctx)
		if err != nil || got != want {
			t.Fatalf("DataVersion=%d, %v; want %d", got, err, want)
		}
	}
	if err := store.Update(ctx, func(Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.View(ctx, func(tx Tx) error {
		_, err := tx.Scan("version-test", Scan{})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertVersion(baseline)

	if err := store.Update(ctx, func(tx Tx) error {
		if err := tx.Put("version-test", "kept", 1); err != nil {
			return err
		}
		if err := tx.Put("version-test", "temporary", 2); err != nil {
			return err
		}
		if err := tx.Delete("version-test", "temporary"); err != nil {
			return err
		}
		_, err := tx.Next("version-test.sequence")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertVersion(baseline + 1)

	wantRollback := errors.New("reject transaction")
	if err := store.Update(ctx, func(tx Tx) error {
		if err := tx.Put("version-test", "kept", 99); err != nil {
			return err
		}
		if _, err := tx.Next("version-test.sequence"); err != nil {
			return err
		}
		return wantRollback
	}); !errors.Is(err, wantRollback) {
		t.Fatalf("rollback error=%v", err)
	}
	assertVersion(baseline + 1)
	if err := store.View(ctx, func(tx Tx) error {
		var value int
		if err := tx.Get("version-test", "kept", &value); err != nil {
			return err
		}
		if value != 1 {
			t.Fatalf("rolled-back record became visible: %d", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertVersion(baseline + 1)

	var next uint64
	if err := store.Update(ctx, func(tx Tx) error {
		var err error
		next, err = tx.Next("version-test.sequence")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if next != 2 {
		t.Fatalf("rolled-back sequence increment leaked: next=%d, want 2", next)
	}
	assertVersion(baseline + 2)
	if err := store.Update(ctx, func(tx Tx) error { return tx.Delete("version-test", "kept") }); err != nil {
		t.Fatal(err)
	}
	assertVersion(baseline + 3)
	if err := store.View(ctx, func(tx Tx) error {
		var value int
		if err := tx.Get("version-test", "kept", &value); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted record Get=%v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertVersion(baseline + 3)
}

func TestMemoryCancelledTransactionDoesNotPublishDataVersion(t *testing.T) {
	store := NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	err := store.Update(ctx, func(tx Tx) error {
		if err := tx.Put("cancelled", "record", 1); err != nil {
			return err
		}
		cancel()
		return nil
	})
	defer cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Update=%v", err)
	}
	version, err := store.DataVersion(context.Background())
	if err != nil || version != 0 {
		t.Fatalf("cancelled DataVersion=%d, %v", version, err)
	}
	if err := store.View(context.Background(), func(tx Tx) error {
		var value int
		if err := tx.Get("cancelled", "record", &value); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cancelled record Get=%v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
