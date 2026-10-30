package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This suite never connects by default. Opt in only with a disposable database
// ending in _test and a role allowed to create/drop schemas and run migrations.
// Every run owns a random schema; it never drops a database or existing schema.
func TestPostgresIntegration(t *testing.T) {
	first, second := postgresIntegrationStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	t.Run("migrations are idempotent and preserve records", func(t *testing.T) {
		if err := first.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := first.Update(ctx, func(tx Tx) error { return tx.Put("migration-test", "kept", 7) }); err != nil {
			t.Fatal(err)
		}
		version, err := first.DataVersion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ledgerBefore int
		if err := first.pool.QueryRow(ctx, "SELECT count(*) FROM relay_schema_migrations").Scan(&ledgerBefore); err != nil {
			t.Fatal(err)
		}
		if err := second.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		var ledgerAfter int
		if err := second.pool.QueryRow(ctx, "SELECT count(*) FROM relay_schema_migrations").Scan(&ledgerAfter); err != nil {
			t.Fatal(err)
		}
		if ledgerBefore == 0 || ledgerAfter != ledgerBefore {
			t.Fatalf("migration ledger count before=%d after=%d", ledgerBefore, ledgerAfter)
		}
		if err := second.View(ctx, func(tx Tx) error {
			var value int
			if err := tx.Get("migration-test", "kept", &value); err != nil {
				return err
			}
			if value != 7 {
				return fmt.Errorf("migration changed existing record to %d", value)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got, err := second.DataVersion(ctx); err != nil || got != version {
			t.Fatalf("migration changed DataVersion: got=%d, %v; want %d", got, err, version)
		}
		if err := second.Ping(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if t.Failed() {
		return
	}
	t.Run("rollback and committed data versions", func(t *testing.T) {
		exerciseDataVersion(t, first)
	})
	t.Run("Next is atomic across independent pools", func(t *testing.T) {
		const count = 24
		type result struct {
			number uint64
			err    error
		}
		results := make(chan result, count)
		var workers sync.WaitGroup
		for index := 0; index < count; index++ {
			store := first
			if index%2 == 1 {
				store = second
			}
			workers.Add(1)
			go func(store *Postgres) {
				defer workers.Done()
				var number uint64
				err := store.Update(ctx, func(tx Tx) error {
					var err error
					number, err = tx.Next("integration.concurrent")
					return err
				})
				results <- result{number: number, err: err}
			}(store)
		}
		workers.Wait()
		close(results)
		seen := make(map[uint64]bool, count)
		for result := range results {
			if result.err != nil {
				t.Errorf("Next transaction: %v", result.err)
				continue
			}
			if result.number < 1 || result.number > count || seen[result.number] {
				t.Errorf("unexpected or duplicate sequence value %d", result.number)
			}
			seen[result.number] = true
		}
		if len(seen) != count {
			t.Fatalf("committed distinct values=%d, want %d", len(seen), count)
		}
	})
	t.Run("leader excludes another process and migrations", func(t *testing.T) {
		leader, err := first.AcquireLeadership(ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer leader.Close()
			other, err := second.AcquireLeadership(ctx)
			if err == nil {
				other.Close()
				t.Error("second pool acquired an already held leader lock")
			}
			if err := second.Migrate(ctx); err == nil {
				t.Error("migration ran while an application held the leader lock")
			}
		}()
		replacement, err := second.AcquireLeadership(ctx)
		if err != nil {
			t.Fatalf("leader lock was not released: %v", err)
		}
		replacement.Close()
		if err := first.Migrate(ctx); err != nil {
			t.Fatalf("migration locks were not released after rejection: %v", err)
		}
	})
	t.Run("unknown schema history refuses startup and migration", func(t *testing.T) {
		const unknown = "999_unrecognized_future.sql"
		if _, err := first.pool.Exec(ctx, "INSERT INTO relay_schema_migrations(name) VALUES($1)", unknown); err != nil {
			t.Fatal(err)
		}
		if err := second.Ping(ctx); err == nil {
			t.Error("startup accepted an unknown migration")
		}
		if err := second.Migrate(ctx); err == nil {
			t.Error("migration accepted an unknown history")
		}
		if _, err := first.pool.Exec(ctx, "DELETE FROM relay_schema_migrations WHERE name=$1", unknown); err != nil {
			t.Fatal(err)
		}
		if err := second.Ping(ctx); err != nil {
			t.Fatalf("known history did not recover: %v", err)
		}
	})
}

func TestPostgresExactKeyScansIntegration(t *testing.T) {
	store, _ := postgresIntegrationStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	exerciseExactKeyScan(t, store)
}

func postgresIntegrationStores(t *testing.T) (*Postgres, *Postgres) {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("RELAY_TEST_DATABASE_URL"))
	if databaseURL == "" {
		if os.Getenv("RELAY_REQUIRE_POSTGRES_TESTS") == "1" {
			t.Fatal("RELAY_TEST_DATABASE_URL is required for mandatory PostgreSQL integration tests")
		}
		t.Skip("NOT_EXECUTED: set RELAY_TEST_DATABASE_URL to opt into a disposable PostgreSQL database ending in _test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("invalid RELAY_TEST_DATABASE_URL; refusing PostgreSQL integration test")
	}
	if !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatal("refusing PostgreSQL integration test: database name must end in _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, config.ConnConfig.Copy())
	if err != nil {
		t.Fatal("connect to disposable PostgreSQL test database failed")
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	schema := "relay_integration_" + hex.EncodeToString(random[:])
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	// Registered first, so both pools close before the schema is removed.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("remove integration schema %s: %v", schema, err)
		}
		_ = admin.Close(cleanupCtx)
	})
	newStore := func() *Postgres {
		t.Helper()
		poolConfig := config.Copy()
		poolConfig.MaxConns = 4
		poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
		pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			t.Fatal("create PostgreSQL integration pool failed")
		}
		store := &Postgres{pool: pool}
		t.Cleanup(store.Close)
		if err := pool.Ping(ctx); err != nil {
			t.Fatal("ping disposable PostgreSQL integration pool failed")
		}
		return store
	}
	return newStore(), newStore()
}
