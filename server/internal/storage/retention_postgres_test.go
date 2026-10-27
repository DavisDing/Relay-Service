package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Opt-in only, using the existing disposable database guard. The schema is
// initialized from empty; no legacy data or migration history is required.
func TestPostgresRetentionInitializationIntegration(t *testing.T) {
	store, _ := postgresIntegrationStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.Ping(ctx); !errors.Is(err, ErrSchemaNotReady) {
		t.Fatalf("empty schema readiness=%v; want ErrSchemaNotReady", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	exerciseRetentionCount(t, store)
	if _, err := store.pool.Exec(ctx, "ALTER TABLE relay_records DISABLE TRIGGER relay_records_retention_at"); err != nil {
		t.Fatal(err)
	}
	if err := store.Ping(ctx); err == nil || errors.Is(err, ErrSchemaNotReady) {
		t.Fatalf("disabled trigger readiness=%v; want a non-initializable error", err)
	}
	if _, err := store.pool.Exec(ctx, "ALTER TABLE relay_records ENABLE TRIGGER relay_records_retention_at; DROP INDEX relay_records_retention_at_idx"); err != nil {
		t.Fatal(err)
	}
	if err := store.Ping(ctx); err == nil || errors.Is(err, ErrSchemaNotReady) {
		t.Fatalf("missing index readiness=%v; want a non-initializable error", err)
	}
	// A complete ledger with a missing required column is damage, not an empty
	// installation. Startup must refuse it rather than rerun completed SQL.
	if _, err := store.pool.Exec(ctx, "ALTER TABLE relay_records DROP COLUMN retention_at"); err != nil {
		t.Fatal(err)
	}
	if err := store.Ping(ctx); err == nil || errors.Is(err, ErrSchemaNotReady) {
		t.Fatalf("damaged schema readiness=%v; want a non-initializable error", err)
	}
}

func TestPostgresUntrackedSchemaInitializationIntegration(t *testing.T) {
	first, _ := postgresIntegrationStores(t)
	for _, ledger := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty_ledger=%t", ledger), func(t *testing.T) {
			store := first
			if ledger {
				store, _ = postgresIntegrationStores(t)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := store.pool.Exec(ctx, "CREATE TABLE relay_records(value TEXT NOT NULL); INSERT INTO relay_records(value) VALUES('keep existing data')"); err != nil {
				t.Fatal(err)
			}
			if ledger {
				if _, err := store.pool.Exec(ctx, "CREATE TABLE relay_schema_migrations(name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Migrate(ctx); err == nil {
				t.Fatal("initialization accepted business tables without tracked migrations")
			}
			var value string
			if err := store.pool.QueryRow(ctx, "SELECT value FROM relay_records").Scan(&value); err != nil || value != "keep existing data" {
				t.Fatalf("untracked data changed: %q, %v", value, err)
			}
			var hasLedger bool
			if err := store.pool.QueryRow(ctx, "SELECT to_regclass('relay_schema_migrations') IS NOT NULL").Scan(&hasLedger); err != nil || hasLedger != ledger {
				t.Fatalf("rejected initialization changed the ledger: exists=%t, %v", hasLedger, err)
			}
		})
	}
}

func TestMemoryRetentionCountContract(t *testing.T) {
	store := NewMemory()
	defer store.Close()
	exerciseRetentionCount(t, store)
}

func exerciseRetentionCount(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	cutoff := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	for _, bucket := range []string{"monitor.history", AuditBucket} {
		field := "completed_at"
		options := CountOptions{Prefix: "contract/", Before: cutoff, TimeField: field, StatusField: "status", ExcludeStatuses: []string{"queued", "running"}}
		if bucket == AuditBucket {
			field = "at"
			options = CountOptions{Prefix: "contract/", Before: cutoff, TimeField: field}
		}
		t.Run(bucket, func(t *testing.T) {
			fixtures := map[string]map[string]any{
				"contract/old":      {field: cutoff.Add(-time.Hour), "status": "healthy"},
				"else/old":          {field: cutoff.Add(-time.Hour)},
				"contract/boundary": {field: cutoff},
				"contract/future":   {field: cutoff.Add(time.Hour)},
				"contract/zero":     {field: time.Time{}},
				"contract/missing":  {},
				"contract/null":     {field: nil},
			}
			if bucket == "monitor.history" {
				fixtures["contract/queued"] = map[string]any{field: cutoff.Add(-time.Hour), "status": "queued"}
				fixtures["contract/running"] = map[string]any{field: cutoff.Add(-time.Hour), "status": "running"}
			}
			if err := store.Update(ctx, func(tx Tx) error {
				for key, value := range fixtures {
					if err := tx.Put(bucket, key, value); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			assertCount := func(want int64) {
				t.Helper()
				if err := store.View(ctx, func(tx Tx) error {
					got, err := tx.Count(bucket, options)
					if err == nil && got != want {
						return fmt.Errorf("count=%d; want %d", got, want)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			assertCount(1)
			for _, value := range []any{cutoff.Add(time.Hour), time.Time{}, nil} {
				if err := store.Update(ctx, func(tx Tx) error {
					return tx.Put(bucket, "contract/old", map[string]any{field: value, "status": "healthy"})
				}); err != nil {
					t.Fatal(err)
				}
				assertCount(0)
			}
			// PostgreSQL rounds timestamps to microseconds; use a full-second
			// offset here to verify RFC3339 zone semantics without rounding noise.
			value := json.RawMessage(fmt.Sprintf(`{%q:%q}`, field, cutoff.Add(-time.Hour).In(time.FixedZone("UTC+8", 8*60*60)).Format(time.RFC3339)))
			if err := store.Update(ctx, func(tx Tx) error { return tx.Put(bucket, "contract/old", value) }); err != nil {
				t.Fatal(err)
			}
			assertCount(1)
		})
	}
}
