package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"
)

func TestRetentionMappingMatchesMigration(t *testing.T) {
	raw, err := migrationFiles.ReadFile("migrations/002_retention_count_indexes.sql")
	if err != nil {
		t.Fatal(err)
	}
	branches := regexp.MustCompile(`(?:IF|ELSIF) NEW.bucket = '([^']+)' THEN\s+NEW.retention_at := NULLIF\(NEW.value->>'([^']+)'`).FindAllStringSubmatch(string(raw), -1)
	got := make(map[string]string)
	for _, branch := range branches {
		got[branch[1]] = branch[2]
	}
	if !reflect.DeepEqual(got, retentionFields) {
		t.Fatalf("PostgreSQL migration retention mapping=%v, Go mapping=%v; update both contracts through an explicit migration", got, retentionFields)
	}
}

func TestRetentionMappingIntegration(t *testing.T) {
	pg, _ := postgresIntegrationStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := pg.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sq, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sq.Close()
	cutoff := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	mapping := make(map[string]string)
	for bucket, field := range retentionFields {
		mapping[bucket] = field
	}
	mapping["custom"] = "at"
	for bucket, field := range mapping {
		t.Run(bucket, func(t *testing.T) {
			for _, value := range []any{cutoff.Add(-time.Microsecond), cutoff, cutoff.Add(time.Microsecond), cutoff.Add(-time.Hour).In(time.FixedZone("UTC+8", 8*3600)), nil, ""} {
				record := map[string]any{field: value}
				raw, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				// Exercise the PostgreSQL trigger directly, independently of Put.
				if _, err := pg.pool.Exec(ctx, `INSERT INTO relay_records(bucket,record_key,value) VALUES($1,'mapping',$2::jsonb) ON CONFLICT(bucket,record_key) DO UPDATE SET value=excluded.value`, bucket, raw); err != nil {
					t.Fatal(err)
				}
				if err := sq.Update(ctx, func(tx Tx) error { return tx.Put(bucket, "mapping", record) }); err != nil {
					t.Fatal(err)
				}
				var postgresTime *time.Time
				if err := pg.pool.QueryRow(ctx, "SELECT retention_at FROM relay_records WHERE bucket=$1 AND record_key='mapping'", bucket).Scan(&postgresTime); err != nil {
					t.Fatal(err)
				}
				var sqliteTime *int64
				if err := sq.db.QueryRowContext(ctx, "SELECT retention_at FROM relay_records WHERE bucket=? AND record_key='mapping'", bucket).Scan(&sqliteTime); err != nil {
					t.Fatal(err)
				}
				if (postgresTime == nil) != (sqliteTime == nil) || (postgresTime != nil && sqliteTime != nil && postgresTime.UnixMicro() != *sqliteTime) {
					t.Fatalf("retention differs for %s: PostgreSQL=%v SQLite=%v", raw, postgresTime, sqliteTime)
				}
				var counts []int64
				for _, store := range []Store{pg, sq} {
					if err := store.View(ctx, func(tx Tx) error {
						n, err := tx.Count(bucket, CountOptions{TimeField: field, Before: cutoff})
						counts = append(counts, n)
						return err
					}); err != nil {
						t.Fatal(err)
					}
				}
				if counts[0] != counts[1] {
					t.Fatalf("Count differs for %s: %v", raw, counts)
				}
			}
		})
	}
}
