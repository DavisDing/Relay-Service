package storage

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const applicationName = "relayservice"

type Postgres struct{ pool *pgxpool.Pool }

func OpenPostgres(ctx context.Context, databaseURL string, maxConns int32) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid database connection configuration")
	}
	if maxConns < 1 {
		maxConns = 8
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	store := &Postgres{pool: pool}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (p *Postgres) Ping(ctx context.Context) error {
	rows, err := p.pool.Query(ctx, "SELECT name FROM relay_schema_migrations ORDER BY name")
	if err != nil {
		return err
	}
	defer rows.Close()
	var applied []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		applied = append(applied, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	var expected []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			expected = append(expected, entry.Name())
		}
	}
	if strings.Join(applied, "\n") != strings.Join(expected, "\n") {
		return fmt.Errorf("database schema is incompatible; run the matching migration image before starting")
	}
	return nil
}
func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) DataVersion(ctx context.Context) (uint64, error) {
	var version uint64
	err := p.pool.QueryRow(ctx, "SELECT next_value FROM relay_sequences WHERE sequence_name='relay.data_version'").Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return version, err
}
func (p *Postgres) View(ctx context.Context, fn func(Tx) error) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin database view: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(&postgresTx{ctx: ctx, tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Update(ctx context.Context, fn func(Tx) error) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin database update: %w", err)
	}
	defer tx.Rollback(ctx)
	// Single short writer lock makes read/modify/write operations atomic across
	// processes. Never perform external HTTP calls inside this transaction.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('relayservice:writer'))"); err != nil {
		return err
	}
	transaction := &postgresTx{ctx: ctx, tx: tx}
	if err := fn(transaction); err != nil {
		return err
	}
	if transaction.changed {
		if _, err := transaction.Next("relay.data_version"); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit database update: %w", err)
	}
	return nil
}

// Migrate must be invoked explicitly with a deployment-side owner role.
// The regular application role never migrates tables on process startup.
func (p *Postgres) Migrate(ctx context.Context) (resultErr error) {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	// Session locks must never survive a connection's return to the pool, even
	// when cancellation leaves the outcome of a lock request uncertain.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, cleanupErr := conn.Exec(cleanupCtx, "SELECT pg_advisory_unlock_all()")
		cancel()
		if cleanupErr != nil {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = conn.Conn().Close(closeCtx)
			closeCancel()
			if resultErr == nil {
				resultErr = fmt.Errorf("release migration locks: %w", cleanupErr)
			}
		}
		conn.Release()
	}()
	var leader bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtext('relayservice:leader'))").Scan(&leader); err != nil {
		return fmt.Errorf("lock application for migrations: %w", err)
	}
	if !leader {
		return fmt.Errorf("cannot migrate while another Relay process owns this database; stop Relay before migrating")
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext('relayservice:migrations'))"); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS relay_schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("initialize migration ledger: %w", err)
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var expected []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			expected = append(expected, entry.Name())
		}
	}
	rows, err := conn.Query(ctx, "SELECT name FROM relay_schema_migrations ORDER BY name")
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	var applied []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("read migration history entry: %w", err)
		}
		applied = append(applied, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	// Applied migrations must be an exact prefix of this image's history. This
	// rejects newer/unknown migrations and gaps before executing any SQL file.
	for index, name := range applied {
		if index >= len(expected) || name != expected[index] {
			return fmt.Errorf("database migration history is incompatible with this image; use the matching migration image or restore a matching backup")
		}
	}
	for _, name := range expected[len(applied):] {
		contents, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err = tx.Exec(ctx, string(contents)); err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO relay_schema_migrations (name) VALUES ($1)", name)
		}
		if err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

type databaseTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type postgresTx struct {
	ctx     context.Context
	tx      databaseTx
	changed bool
}

func (t *postgresTx) Get(bucket, key string, target any) error {
	if err := validIdentity(bucket, key); err != nil {
		return err
	}
	var data []byte
	if err := t.tx.QueryRow(t.ctx, "SELECT value FROM relay_records WHERE bucket=$1 AND record_key=$2", bucket, key).Scan(&data); err != nil {
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("read %s record: %w", bucket, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s record: %w", bucket, err)
	}
	return nil
}

func (t *postgresTx) Put(bucket, key string, value any) error {
	if err := validIdentity(bucket, key); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s record: %w", bucket, err)
	}
	if _, err := t.tx.Exec(t.ctx, `INSERT INTO relay_records(bucket, record_key, value) VALUES ($1,$2,$3::jsonb) ON CONFLICT(bucket,record_key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, bucket, key, data); err != nil {
		return fmt.Errorf("write %s record: %w", bucket, err)
	}
	t.changed = true
	return nil
}

func (t *postgresTx) Delete(bucket, key string) error {
	if err := validIdentity(bucket, key); err != nil {
		return err
	}
	if _, err := t.tx.Exec(t.ctx, "DELETE FROM relay_records WHERE bucket=$1 AND record_key=$2", bucket, key); err != nil {
		return fmt.Errorf("delete %s record: %w", bucket, err)
	}
	t.changed = true
	return nil
}

func (t *postgresTx) Scan(bucket string, options Scan) ([]Record, error) {
	if err := validIdentity(bucket, "scan"); err != nil {
		return nil, err
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	comparison, direction := ">", "ASC"
	if options.Reverse {
		comparison, direction = "<", "DESC"
	}
	query := "SELECT record_key, value FROM relay_records WHERE bucket=$1"
	args := []any{bucket}
	n := 2
	if options.Prefix != "" {
		query += fmt.Sprintf(" AND left(record_key, length($%d))=$%d", n, n)
		args = append(args, options.Prefix)
		n++
	}
	if options.After != "" {
		query += fmt.Sprintf(" AND record_key COLLATE \"C\" %s $%d", comparison, n)
		args = append(args, options.After)
		n++
	}
	query += " ORDER BY record_key COLLATE \"C\" " + direction + fmt.Sprintf(" LIMIT $%d", n)
	args = append(args, limit)
	rows, err := t.tx.Query(t.ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("scan %s records: %w", bucket, err)
	}
	defer rows.Close()
	out := make([]Record, 0, limit)
	for rows.Next() {
		var item Record
		if err := rows.Scan(&item.Key, &item.Value); err != nil {
			return nil, fmt.Errorf("read %s scan result: %w", bucket, err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s records: %w", bucket, err)
	}
	return out, nil
}

func (t *postgresTx) Next(sequence string) (uint64, error) {
	if err := validIdentity(sequence, "sequence"); err != nil {
		return 0, err
	}
	var next int64
	if err := t.tx.QueryRow(t.ctx, `INSERT INTO relay_sequences(sequence_name,next_value) VALUES ($1,1) ON CONFLICT(sequence_name) DO UPDATE SET next_value=relay_sequences.next_value+1 RETURNING next_value`, sequence).Scan(&next); err != nil {
		return 0, fmt.Errorf("increment sequence: %w", err)
	}
	t.changed = true
	return uint64(next), nil
}

func validIdentity(bucket, key string) error {
	if bucket == "" || key == "" || strings.ContainsRune(bucket, 0) || strings.ContainsRune(key, 0) {
		return fmt.Errorf("invalid storage bucket or key")
	}
	return nil
}
