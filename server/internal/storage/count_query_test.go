package storage

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresCountUsesIndexedTimeOnlyForMatchingRecords(t *testing.T) {
	cutoff := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		bucket, field, expression string
	}{
		{"monitor.history", "completed_at", "retention_at"},
		{AuditBucket, "at", "retention_at"},
		{"monitor.history", "at", "(NULLIF(value->>'at',''))::timestamptz"},
		{AuditBucket, "completed_at", "(NULLIF(value->>'completed_at',''))::timestamptz"},
		{"custom", "at", "(NULLIF(value->>'at',''))::timestamptz"},
	} {
		t.Run(test.bucket+"/"+test.field, func(t *testing.T) {
			db := &countQueryCapture{}
			tx := &postgresTx{ctx: context.Background(), tx: db}
			got, err := tx.Count(test.bucket, CountOptions{Prefix: "svc/", Before: cutoff, TimeField: test.field, StatusField: "status", ExcludeStatuses: []string{"queued", "running"}})
			if err != nil || got != 7 {
				t.Fatalf("Count=%d, %v", got, err)
			}
			for _, condition := range []string{
				test.expression + " IS NOT NULL",
				test.expression + " <> TIMESTAMPTZ '0001-01-01 00:00:00+00'",
				test.expression + " < $3",
				"left(record_key, length($2))=$2",
				"COALESCE(value->>'status','') <> $4",
				"COALESCE(value->>'status','') <> $5",
			} {
				if !strings.Contains(db.query, condition) {
					t.Errorf("missing condition %q in %s", condition, db.query)
				}
			}
			if want := []any{test.bucket, "svc/", cutoff, "queued", "running"}; !reflect.DeepEqual(db.args, want) {
				t.Fatalf("query arguments=%v; want %v", db.args, want)
			}
		})
	}
}

type countQueryCapture struct {
	query string
	args  []any
}

func (c *countQueryCapture) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	c.query, c.args = query, args
	return countRow{}
}
func (*countQueryCapture) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("Count must not mutate records")
}
func (*countQueryCapture) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("Count must not fetch all records")
}

type countRow struct{}

func (countRow) Scan(dest ...any) error {
	if len(dest) != 1 {
		return fmt.Errorf("unexpected count result shape")
	}
	value, ok := dest[0].(*int64)
	if !ok {
		return fmt.Errorf("unexpected count result type")
	}
	*value = 7
	return nil
}
