package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"relayservice/internal/config"
	"relayservice/internal/storage"
)

func postgresTarget(raw string) string {
	u, _ := url.Parse(raw)
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	sum := sha256.Sum256([]byte(strings.ToLower(u.Hostname()) + ":" + port + u.Path + "?" + u.Query().Encode()))
	return hex.EncodeToString(sum[:])
}
func checkTargetPin(cfg *config.Config) error {
	raw, err := privateRead(filepath.Join(cfg.DataDir, "postgres-target"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("this data volume uses PostgreSQL; restore RELAY_DATABASE_URL, refusing SQLite fallback")
	}
	if string(raw) != postgresTarget(cfg.DatabaseURL) {
		return fmt.Errorf("PostgreSQL target differs from this data volume; use the original database or a separately restored data volume")
	}
	return nil
}
func pinTarget(cfg *config.Config) error {
	path := filepath.Join(cfg.DataDir, "postgres-target")
	if err := checkTargetPin(cfg); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writePrivate(path, []byte(postgresTarget(cfg.DatabaseURL)))
}
func guardSQLiteCutover(ctx context.Context, cfg *config.Config, db storage.Database) error {
	if err := checkTargetPin(cfg); err != nil {
		return err
	}
	if _, err := os.Stat(cfg.SQLitePath); err == nil {
		if err := storage.CheckSQLiteCutover(ctx, cfg.SQLitePath, db.(*storage.Postgres)); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func migrateSQLite(args []string) error {
	flags := flag.NewFlagSet("migrate-sqlite", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	backup := flags.String("backup", "", "new SQLite backup path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return fmt.Errorf("usage: relay migrate-sqlite [--backup PATH]; configure PostgreSQL URLs in the container environment")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" || cfg.MigrationDatabaseURL == "" {
		return fmt.Errorf("migrate-sqlite requires RELAY_DATABASE_URL and RELAY_MIGRATION_DATABASE_URL")
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return err
	}
	lock, err := storage.LockFile(filepath.Join(cfg.DataDir, "instance.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := checkTargetPin(&cfg); err != nil {
		return err
	}
	if _, err := prepareLocalKey(&cfg); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	source, err := storage.OpenSQLiteSource(ctx, cfg.SQLitePath)
	if err != nil {
		return err
	}
	defer source.Close()
	if err := bindEncryptionKey(ctx, source, cfg.EncryptionKey, false); err != nil {
		return err
	}
	if *backup == "" {
		*backup = filepath.Join(cfg.DataDir, "relay-before-postgres-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".sqlite")
	}
	if err := source.Backup(ctx, *backup); err != nil {
		return fmt.Errorf("create source backup: %w", err)
	}
	pending := filepath.Join(cfg.DataDir, "migration.pending")
	raw, pendingErr := privateRead(pending)
	if pendingErr == nil {
		if string(raw) != postgresTarget(cfg.DatabaseURL) {
			return fmt.Errorf("pending migration targets another PostgreSQL database")
		}
	} else if !os.IsNotExist(pendingErr) {
		return pendingErr
	}
	target, err := storage.OpenPostgres(ctx, cfg.MigrationDatabaseURL, 2)
	if err != nil {
		return fmt.Errorf("connect migration target: %w", err)
	}
	defer target.Close()
	if err := target.Migrate(ctx); err != nil {
		return err
	}
	if err := target.Ping(ctx); err != nil {
		return err
	}
	// Connection/schema failures cannot copy business data. Persist the guard
	// immediately before transfer, where a lost commit response needs a retry.
	if os.IsNotExist(pendingErr) {
		if err := writePrivate(pending, []byte(postgresTarget(cfg.DatabaseURL))); err != nil {
			return err
		}
	}
	summary, err := storage.TransferSQLite(ctx, source, target)
	if err != nil {
		return fmt.Errorf("migration is pending; keep Relay stopped and rerun migrate-sqlite with the same target after resolving the error: %w", err)
	}
	if err := pinTarget(&cfg); err != nil {
		return fmt.Errorf("data copied but local target pin failed; rerun migration before starting: %w", err)
	}
	if err := os.Remove(pending); err != nil {
		return fmt.Errorf("data copied; could not clear migration.pending, rerun the command")
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		storage.TransferSummary
		Backup string `json:"backup"`
		Status string `json:"status"`
	}{summary, *backup, "verified; start Relay with the same PostgreSQL URLs and data volume"})
}
