package storage

import (
	"context"
	"strings"
	"testing"
)

func exerciseExactKeyScan(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	if err := store.Update(ctx, func(tx Tx) error {
		for _, key := range []string{"prefix/a", "prefix/b", "prefix/c", "other/a", "quote' OR 1=1 --"} {
			if err := tx.Put("scan.keys", key, key); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.View(ctx, func(tx Tx) error {
		for _, tc := range []struct {
			scan Scan
			want string
		}{
			{Scan{Keys: []string{"prefix/c", "prefix/a", "prefix/a", "missing"}}, "prefix/a,prefix/c"},
			{Scan{Keys: []string{}}, ""},
			{Scan{Keys: []string{"prefix/a", "prefix/c", "other/a"}, Prefix: "prefix/", After: "prefix/a", Reverse: false}, "prefix/c"},
			{Scan{Keys: []string{"prefix/a", "prefix/c"}, Reverse: true, Limit: 1}, "prefix/c"},
			{Scan{Keys: []string{"quote' OR 1=1 --"}}, "quote' OR 1=1 --"},
		} {
			rows, err := tx.Scan("scan.keys", tc.scan)
			if err != nil {
				t.Fatal(err)
			}
			keys := []string{}
			for _, row := range rows {
				keys = append(keys, row.Key)
			}
			if got := strings.Join(keys, ","); got != tc.want {
				t.Fatalf("scan %+v got %q want %q", tc.scan, got, tc.want)
			}
		}
		if _, err := tx.Scan("scan.keys", Scan{Keys: make([]string, 501)}); err == nil {
			t.Fatal("unbounded exact-key scan accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryExactKeyScan(t *testing.T) {
	store := NewMemory()
	t.Cleanup(store.Close)
	exerciseExactKeyScan(t, store)
}
