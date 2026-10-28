package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"math/big"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

type TransferSummary struct {
	Records   int64  `json:"records"`
	Sequences int64  `json:"sequences"`
	SHA256    string `json:"sha256"`
}
type transferRow struct {
	bucket, key string
	value       []byte
	updated     time.Time
}

// JSONB can change whitespace, object order and numeric notation. Hash semantic
// JSON without converting large integer counters or amounts to float64.
func hashJSON(h hash.Hash, raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return err
	}
	var visit func(any) error
	visit = func(v any) error {
		switch x := v.(type) {
		case nil:
			fmt.Fprint(h, "null;")
		case bool:
			fmt.Fprintf(h, "bool:%t;", x)
		case string:
			fmt.Fprintf(h, "string:%q;", x)
		case json.Number:
			r, ok := new(big.Rat).SetString(string(x))
			if !ok {
				return fmt.Errorf("invalid JSON number")
			}
			fmt.Fprintf(h, "number:%s;", r.RatString())
		case []any:
			fmt.Fprint(h, "[")
			for _, item := range x {
				if err := visit(item); err != nil {
					return err
				}
			}
			fmt.Fprint(h, "]")
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Fprint(h, "{")
			for _, k := range keys {
				fmt.Fprintf(h, "%q:", k)
				if err := visit(x[k]); err != nil {
					return err
				}
			}
			fmt.Fprint(h, "}")
		default:
			return fmt.Errorf("unsupported JSON value")
		}
		return nil
	}
	return visit(value)
}
func hashRecord(h hash.Hash, r transferRow) error {
	fmt.Fprintf(h, "record:%q:%q:%s:", r.bucket, r.key, r.updated.UTC().Format("2006-01-02T15:04:05.000000Z"))
	return hashJSON(h, r.value)
}
func sqliteSummary(ctx context.Context, tx *sql.Tx, copyRecord func(transferRow) error, copySequence func(string, int64) error) (TransferSummary, error) {
	var out TransferSummary
	h := sha256.New()
	rows, err := tx.QueryContext(ctx, "SELECT bucket,record_key,value,updated_at FROM relay_records ORDER BY bucket,record_key")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r transferRow
		var updated string
		if err := rows.Scan(&r.bucket, &r.key, &r.value, &updated); err != nil {
			rows.Close()
			return out, err
		}
		r.updated, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			rows.Close()
			return out, err
		}
		if err := hashRecord(h, r); err != nil {
			rows.Close()
			return out, err
		}
		if copyRecord != nil {
			if err := copyRecord(r); err != nil {
				rows.Close()
				return out, err
			}
		}
		out.Records++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT sequence_name,next_value FROM relay_sequences ORDER BY sequence_name")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var v int64
		if err := rows.Scan(&name, &v); err != nil {
			return out, err
		}
		fmt.Fprintf(h, "sequence:%q:%d;", name, v)
		if copySequence != nil {
			if err := copySequence(name, v); err != nil {
				return out, err
			}
		}
		out.Sequences++
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.SHA256 = hex.EncodeToString(h.Sum(nil))
	return out, nil
}
func postgresSummary(ctx context.Context, tx pgx.Tx) (TransferSummary, error) {
	var out TransferSummary
	h := sha256.New()
	rows, err := tx.Query(ctx, `SELECT bucket,record_key,value,updated_at FROM relay_records ORDER BY bucket COLLATE "C",record_key COLLATE "C"`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r transferRow
		if err := rows.Scan(&r.bucket, &r.key, &r.value, &r.updated); err != nil {
			rows.Close()
			return out, err
		}
		if err := hashRecord(h, r); err != nil {
			rows.Close()
			return out, err
		}
		out.Records++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, `SELECT sequence_name,next_value FROM relay_sequences ORDER BY sequence_name COLLATE "C"`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var v int64
		if err := rows.Scan(&name, &v); err != nil {
			return out, err
		}
		fmt.Fprintf(h, "sequence:%q:%d;", name, v)
		out.Sequences++
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.SHA256 = hex.EncodeToString(h.Sum(nil))
	return out, nil
}

// TransferSQLite is an offline copy, not a live replication mode. The source OS
// lock and target advisory lock exclude schedulers. All target business writes
// and the receipt commit together. A retry after an uncertain commit verifies
// the receipt and complete target before marking the source as retired.
func TransferSQLite(ctx context.Context, source *SQLite, target *Postgres) (TransferSummary, error) {
	var zero TransferSummary
	stx, err := source.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer stx.Rollback()
	tx, err := target.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtext('relayservice:leader'))").Scan(&locked); err != nil {
		return zero, err
	}
	if !locked {
		return zero, fmt.Errorf("target Relay is running; stop it before migration")
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('relayservice:writer'))"); err != nil {
		return zero, err
	}
	// Transactional infrastructure, separate from the application records being
	// verified. A nonempty target is never overwritten or merged.
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS relay_sqlite_transfer (id INTEGER PRIMARY KEY CHECK(id=1),receipt JSONB NOT NULL);
DO $$ BEGIN IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='relay_app') THEN GRANT SELECT ON relay_sqlite_transfer TO relay_app; END IF; END $$;`); err != nil {
		return zero, err
	}
	var receipt []byte
	err = tx.QueryRow(ctx, "SELECT receipt FROM relay_sqlite_transfer WHERE id=1").Scan(&receipt)
	if err != nil && err != pgx.ErrNoRows {
		return zero, err
	}
	if len(receipt) > 0 {
		want, err := sqliteSummary(ctx, stx, nil, nil)
		if err != nil {
			return zero, err
		}
		var previous TransferSummary
		if err := json.Unmarshal(receipt, &previous); err != nil {
			return zero, err
		}
		got, err := postgresSummary(ctx, tx)
		if err != nil {
			return zero, err
		}
		if previous != want || got != want {
			return zero, fmt.Errorf("target differs from the source/committed migration receipt; refusing overwrite")
		}
		if err := tx.Commit(ctx); err != nil {
			return zero, err
		}
		if err := stx.Commit(); err != nil {
			return zero, err
		}
		return want, source.markTransferred(ctx, want)
	}
	var n int64
	if err := tx.QueryRow(ctx, "SELECT (SELECT count(*) FROM relay_records)+(SELECT count(*) FROM relay_sequences)").Scan(&n); err != nil {
		return zero, err
	}
	if n != 0 {
		return zero, fmt.Errorf("target Relay database is not empty; refusing overwrite")
	}
	want, err := sqliteSummary(ctx, stx, func(r transferRow) error {
		_, err := tx.Exec(ctx, "INSERT INTO relay_records(bucket,record_key,value,updated_at) VALUES($1,$2,$3::jsonb,$4)", r.bucket, r.key, r.value, r.updated.Truncate(time.Microsecond))
		return err
	}, func(name string, v int64) error {
		_, err := tx.Exec(ctx, "INSERT INTO relay_sequences(sequence_name,next_value) VALUES($1,$2)", name, v)
		return err
	})
	if err != nil {
		return zero, err
	}
	got, err := postgresSummary(ctx, tx)
	if err != nil {
		return zero, err
	}
	if got != want {
		return zero, fmt.Errorf("migration verification failed; target transaction rolled back")
	}
	raw, _ := json.Marshal(want)
	if _, err := tx.Exec(ctx, "INSERT INTO relay_sqlite_transfer(id,receipt) VALUES(1,$1::jsonb)", raw); err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, fmt.Errorf("target commit outcome uncertain; rerun migration to verify its receipt: %w", err)
	}
	// Release source read snapshot before the source marker write.
	if err := stx.Commit(); err != nil {
		return zero, err
	}
	return want, source.markTransferred(ctx, want)
}
func (s *SQLite) markTransferred(ctx context.Context, summary TransferSummary) error {
	raw, _ := json.Marshal(summary)
	_, err := s.db.ExecContext(ctx, "INSERT INTO relay_local_metadata(name,value) VALUES('migrated_to_postgres',?) ON CONFLICT(name) DO UPDATE SET value=excluded.value", string(raw))
	return err
}

// CheckSQLiteCutover prevents accidental parallel histories. It can also finish
// retiring the source after a crash between the target commit and source marker.
func CheckSQLiteCutover(ctx context.Context, path string, target *Postgres) error {
	s, err := openSQLite(ctx, path, false)
	if err != nil {
		// A retired source must be inspected without the normal startup check.
		return err
	}
	defer s.Close()
	var marker string
	err = s.db.QueryRowContext(ctx, "SELECT value FROM relay_local_metadata WHERE name='migrated_to_postgres'").Scan(&marker)
	if err == nil {
		var local, receipt TransferSummary
		var raw []byte
		if json.Unmarshal([]byte(marker), &local) != nil {
			return fmt.Errorf("invalid SQLite migration marker")
		}
		if err := target.pool.QueryRow(ctx, "SELECT receipt FROM relay_sqlite_transfer WHERE id=1").Scan(&raw); err != nil {
			return fmt.Errorf("PostgreSQL has no matching SQLite migration receipt")
		}
		if json.Unmarshal(raw, &receipt) != nil || receipt != local {
			return fmt.Errorf("PostgreSQL does not match the retired SQLite source")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM relay_records").Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	stx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer stx.Rollback()
	want, err := sqliteSummary(ctx, stx, nil, nil)
	if err != nil {
		return err
	}
	var raw []byte
	err = target.pool.QueryRow(ctx, "SELECT receipt FROM relay_sqlite_transfer WHERE id=1").Scan(&raw)
	if err != nil {
		return fmt.Errorf("local SQLite has existing data; run migrate-sqlite before using PostgreSQL")
	}
	var receipt TransferSummary
	if json.Unmarshal(raw, &receipt) != nil || receipt != want {
		return fmt.Errorf("PostgreSQL migration receipt does not match local SQLite")
	}
	if err := stx.Commit(); err != nil {
		return err
	}
	return s.markTransferred(ctx, want)
}
