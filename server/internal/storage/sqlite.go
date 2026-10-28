package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// SQLite uses one connection and short transactions. It never copies the full
// database into memory, and its OS lock excludes another scheduler/maintenance.
type SQLite struct {
	db   *sql.DB
	lock *os.File
	path string
}

func OpenSQLite(ctx context.Context, path string) (*SQLite, error) {
	return openSQLite(ctx, path, true)
}

// OpenSQLiteSource never creates/upgrades a source and permits completed copies
// for verification/retry of interrupted transfers.
func OpenSQLiteSource(ctx context.Context, path string) (*SQLite, error) {
	return openSQLite(ctx, path, false)
}

func openSQLite(ctx context.Context, path string, create bool) (*SQLite, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("source SQLite database does not exist")
	}
	lock, err := LockFile(path + ".lock")
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*SQLite, error) { lock.Close(); return nil, err }
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fail(fmt.Errorf("SQLite path must be a regular file"))
		}
	} else if !os.IsNotExist(err) {
		return fail(err)
	}
	if create {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			f.Close()
		} else if !os.IsExist(err) {
			return fail(err)
		}
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "cache_size(-4096)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return fail(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &SQLite{db: db, lock: lock, path: path}
	if err := s.prepare(ctx, create); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) prepare(ctx context.Context, create bool) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == 0 && create {
		var tables int
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return err
		}
		if tables != 0 {
			return fmt.Errorf("untracked SQLite database; refusing initialization")
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, `CREATE TABLE relay_records(bucket TEXT NOT NULL CHECK(bucket<>''),record_key TEXT NOT NULL CHECK(record_key<>''),value TEXT NOT NULL CHECK(json_valid(value)),updated_at TEXT NOT NULL,retention_at INTEGER,PRIMARY KEY(bucket,record_key)) WITHOUT ROWID;
CREATE INDEX relay_records_retention_at_idx ON relay_records(bucket,retention_at) WHERE retention_at IS NOT NULL;
CREATE TABLE relay_sequences(sequence_name TEXT PRIMARY KEY,next_value INTEGER NOT NULL CHECK(next_value>0)) WITHOUT ROWID;
CREATE TABLE relay_local_metadata(name TEXT PRIMARY KEY,value TEXT NOT NULL) WITHOUT ROWID;
PRAGMA user_version=1;`)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	} else if version != 1 {
		return fmt.Errorf("unsupported SQLite schema version %d", version)
	}
	if err := s.Ping(ctx); err != nil {
		return err
	}
	var destination string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM relay_local_metadata WHERE name='migrated_to_postgres'").Scan(&destination)
	if err == nil && create {
		return fmt.Errorf("SQLite was migrated to PostgreSQL; configure RELAY_DATABASE_URL, or explicitly restore the pre-migration backup")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.db.ExecContext(ctx, "PRAGMA journal_mode=WAL")
	return err
}
func (s *SQLite) Ping(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "SELECT bucket,record_key,value,updated_at,retention_at FROM relay_records LIMIT 0; SELECT sequence_name,next_value FROM relay_sequences LIMIT 0; SELECT name,value FROM relay_local_metadata LIMIT 0")
	return err
}
func (s *SQLite) Close()                                            { s.db.Close(); s.lock.Close() }
func (s *SQLite) AcquireLeadership(context.Context) (Leader, error) { return sqliteLeader{}, nil }

type sqliteLeader struct{}

func (sqliteLeader) Watch(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
func (sqliteLeader) Close()                          {}
func (s *SQLite) DataVersion(ctx context.Context) (uint64, error) {
	var v uint64
	err := s.db.QueryRowContext(ctx, "SELECT next_value FROM relay_sequences WHERE sequence_name='relay.data_version'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}
func (s *SQLite) View(ctx context.Context, fn func(Tx) error) error {
	return s.transaction(ctx, fn, true)
}
func (s *SQLite) Update(ctx context.Context, fn func(Tx) error) error {
	return s.transaction(ctx, fn, false)
}
func (s *SQLite) transaction(ctx context.Context, fn func(Tx) error, readOnly bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := &sqliteTx{ctx: ctx, tx: tx, readOnly: readOnly}
	if err := fn(t); err != nil {
		return err
	}
	if t.changed {
		if _, err := t.Next("relay.data_version"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type sqliteTx struct {
	ctx               context.Context
	tx                *sql.Tx
	readOnly, changed bool
}

func (t *sqliteTx) Get(bucket, key string, target any) error {
	if err := validIdentity(bucket, key); err != nil {
		return err
	}
	var raw string
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM relay_records WHERE bucket=? AND record_key=?", bucket, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), target)
}
func retentionTime(bucket string, raw []byte) any {
	field := ""
	if bucket == "monitor.history" {
		field = "completed_at"
	} else if bucket == AuditBucket {
		field = "at"
	}
	if field == "" {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	var at time.Time
	if json.Unmarshal(fields[field], &at) != nil || at.IsZero() {
		return nil
	}
	return at.UnixMicro()
}
func (t *sqliteTx) Put(bucket, key string, value any) error {
	if t.readOnly {
		return ErrReadOnly
	}
	if err := validIdentity(bucket, key); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO relay_records(bucket,record_key,value,updated_at,retention_at) VALUES(?,?,?,?,?) ON CONFLICT(bucket,record_key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at,retention_at=excluded.retention_at`, bucket, key, string(raw), time.Now().UTC().Format(time.RFC3339Nano), retentionTime(bucket, raw))
	if err == nil {
		t.changed = true
	}
	return err
}
func (t *sqliteTx) Delete(bucket, key string) error {
	if t.readOnly {
		return ErrReadOnly
	}
	if err := validIdentity(bucket, key); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, "DELETE FROM relay_records WHERE bucket=? AND record_key=?", bucket, key)
	if err == nil {
		t.changed = true
	}
	return err
}
func (t *sqliteTx) Scan(bucket string, o Scan) ([]Record, error) {
	if err := validIdentity(bucket, "scan"); err != nil {
		return nil, err
	}
	limit := o.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := "SELECT record_key,value FROM relay_records WHERE bucket=?"
	args := []any{bucket}
	if o.Prefix != "" {
		query += " AND substr(record_key,1,length(?))=?"
		args = append(args, o.Prefix, o.Prefix)
	}
	direction, comparison := "ASC", ">"
	if o.Reverse {
		direction, comparison = "DESC", "<"
	}
	if o.After != "" {
		query += " AND record_key " + comparison + " ?"
		args = append(args, o.After)
	}
	query += " ORDER BY record_key " + direction + " LIMIT ?"
	args = append(args, limit)
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Record, 0, limit)
	for rows.Next() {
		var r Record
		var raw []byte
		if err := rows.Scan(&r.Key, &raw); err != nil {
			return nil, err
		}
		r.Value = json.RawMessage(raw)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (t *sqliteTx) Count(bucket string, o CountOptions) (int64, error) {
	if err := validIdentity(bucket, "count"); err != nil {
		return 0, err
	}
	if !validJSONField(o.TimeField) || (o.StatusField != "" && !validJSONField(o.StatusField)) || (len(o.ExcludeStatuses) > 0 && o.StatusField == "") || o.Before.IsZero() {
		return 0, fmt.Errorf("invalid count options")
	}
	query := "SELECT count(*) FROM relay_records WHERE bucket=?"
	args := []any{bucket}
	if o.Prefix != "" {
		query += " AND substr(record_key,1,length(?))=?"
		args = append(args, o.Prefix, o.Prefix)
	}
	if (bucket == "monitor.history" && o.TimeField == "completed_at") || (bucket == AuditBucket && o.TimeField == "at") {
		query += " AND retention_at IS NOT NULL AND retention_at < ?"
		args = append(args, o.Before.UnixMicro())
	} else {
		expr := "julianday(json_extract(value,'$." + o.TimeField + "'))"
		query += " AND " + expr + " > julianday('0001-01-01T00:00:00Z') AND " + expr + " < julianday(?)"
		args = append(args, o.Before.UTC().Format(time.RFC3339Nano))
	}
	for _, status := range o.ExcludeStatuses {
		query += " AND coalesce(json_extract(value,'$." + o.StatusField + "'),'') <> ?"
		args = append(args, status)
	}
	var n int64
	err := t.tx.QueryRowContext(t.ctx, query, args...).Scan(&n)
	return n, err
}
func (t *sqliteTx) Next(sequence string) (uint64, error) {
	if t.readOnly {
		return 0, ErrReadOnly
	}
	if err := validIdentity(sequence, "sequence"); err != nil {
		return 0, err
	}
	var v uint64
	err := t.tx.QueryRowContext(t.ctx, `INSERT INTO relay_sequences(sequence_name,next_value) VALUES(?,1) ON CONFLICT(sequence_name) DO UPDATE SET next_value=next_value+1 RETURNING next_value`, sequence).Scan(&v)
	if err == nil {
		t.changed = true
	}
	return v, err
}

// Backup creates a consistent, private snapshot; the caller also holds the
// instance lock. VACUUM INTO includes committed WAL data without copying files.
func (s *SQLite) Backup(ctx context.Context, path string) error {
	// VACUUM INTO accepts an empty file. Reserve it privately before SQLite
	// writes any data so a permissive parent directory cannot expose a backup.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("reserve new backup destination: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

var _ Database = (*SQLite)(nil)
