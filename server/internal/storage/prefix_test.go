package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func exercisePrefixRange(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	keys := []string{"a%/1", "a%/2", "a_/1", "a/1", "机场/1", "机场/2", "机埻/1", "z" + string(utf8.MaxRune) + "/1", string(utf8.MaxRune), string(utf8.MaxRune) + "/1", "a\\/1"}
	if err := store.Update(ctx, func(tx Tx) error {
		for _, key := range keys {
			if err := tx.Put("prefix.test", key, map[string]any{"at": now.Add(-time.Hour)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"a%/", "a_/", "机场/", "z" + string(utf8.MaxRune), string(utf8.MaxRune), "a\\/", "missing/"} {
		if err := store.View(ctx, func(tx Tx) error {
			rows, err := tx.Scan("prefix.test", Scan{Prefix: prefix, Limit: 500})
			if err != nil {
				return err
			}
			want := 0
			for _, key := range keys {
				if strings.HasPrefix(key, prefix) {
					want++
				}
			}
			if len(rows) != want {
				return fmt.Errorf("prefix %q got %d want %d", prefix, len(rows), want)
			}
			for _, row := range rows {
				if !strings.HasPrefix(row.Key, prefix) {
					return fmt.Errorf("wildcard interpreted: %q", row.Key)
				}
			}
			n, err := tx.Count("prefix.test", CountOptions{Prefix: prefix, TimeField: "at", Before: now})
			if err != nil {
				return err
			}
			if n != int64(want) {
				return fmt.Errorf("count mismatch %q: %d", prefix, n)
			}
			if len(rows) > 1 {
				back, err := tx.Scan("prefix.test", Scan{Prefix: prefix, Reverse: true, After: rows[len(rows)-1].Key, Limit: 1})
				if err != nil {
					return err
				}
				if len(back) != 1 || back[0].Key != rows[len(rows)-2].Key {
					return fmt.Errorf("reverse cursor mismatch")
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestSQLitePrefixRangeContractsAndPlan(t *testing.T) {
	s, err := OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "relay.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	exercisePrefixRange(t, s)
	query, args := sqliteRecordPrefix("EXPLAIN QUERY PLAN SELECT record_key,value FROM relay_records WHERE bucket=?", []any{"prefix.test"}, "机场/")
	rows, err := s.db.QueryContext(context.Background(), query+" ORDER BY record_key LIMIT 50", args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "record_key>?") || !strings.Contains(plan, "record_key<?") {
		t.Fatalf("prefix not constrained by key index: %s", plan)
	}
}
func TestPostgresPrefixRangeContracts(t *testing.T) {
	s, _ := postgresIntegrationStores(t)
	exercisePrefixRange(t, s)
}
func TestRecordPrefixEnd(t *testing.T) {
	for _, prefix := range []string{"a%/", "机场/", "z" + string(utf8.MaxRune), "a\uD7FF"} {
		end, ok := recordPrefixEnd(prefix)
		if !ok || !utf8.ValidString(end) || end <= prefix {
			t.Fatal(prefix, end, ok)
		}
	}
	if _, ok := recordPrefixEnd(string(utf8.MaxRune)); ok {
		t.Fatal("max scalar has no upper bound")
	}
	q, args := sqliteRecordPrefix("q", []any{"bucket"}, "a%/")
	if strings.Contains(q, "substr") || !reflect.DeepEqual(args, []any{"bucket", "a%/", "a%0"}) {
		t.Fatal(q, args)
	}
}

// Explicit benchmark only: the fixture puts the selected service near the
// end of a million-row bucket, exercising the real Scan path and index order.
func BenchmarkSQLitePrefixScanMillionRows(b *testing.B) {
	s, err := OpenSQLite(context.Background(), filepath.Join(b.TempDir(), "relay.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	_, err = s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1050000) INSERT INTO relay_records(bucket,record_key,value,updated_at) SELECT 'scan.bench',printf('%08d/check',x),'{}','2026-11-03T00:00:00Z' FROM n`)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.View(context.Background(), func(tx Tx) error {
			rows, err := tx.Scan("scan.bench", Scan{Prefix: "01050000/", Limit: 50})
			if err == nil && len(rows) != 1 {
				return fmt.Errorf("lost target row: %d", len(rows))
			}
			return err
		}); err != nil {
			b.Fatal(err)
		}
	}
}
