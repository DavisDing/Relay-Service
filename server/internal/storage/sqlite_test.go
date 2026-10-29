package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLiteStorageContracts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.sqlite")
	ctx := context.Background()
	s, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	exerciseDataVersion(t, s)
	exerciseRetentionCount(t, s)
	exerciseExactKeyScan(t, s)
	if err := s.Update(ctx, func(tx Tx) error {
		return tx.Put("persistent", "账号", map[string]any{"number": 9007199254740993, "secret": "ciphertext"})
	}); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenSQLite(ctx, path); err == nil {
		other.Close()
		t.Fatal("another process accepted the same database")
	}
	backup := filepath.Join(filepath.Dir(path), "backup.sqlite")
	if err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(ctx, backup); err == nil {
		t.Fatal("overwrote backup")
	}
	s.Close()
	s, err = OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.View(ctx, func(tx Tx) error {
		var v map[string]any
		if err := tx.Get("persistent", "账号", &v); err != nil {
			return err
		}
		if v["secret"] != "ciphertext" {
			return fmt.Errorf("lost persisted record")
		}
		if err := tx.Put("bad", "write", true); !errors.Is(err, ErrReadOnly) {
			return fmt.Errorf("read-only write accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	copy, err := OpenSQLite(ctx, backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if a, _ := s.DataVersion(ctx); a == 0 {
		t.Fatal("lost versions")
	} else if b, _ := copy.DataVersion(ctx); a != b {
		t.Fatal("backup differs")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatalf("database mode=%v", info.Mode())
	}
}
func TestSQLiteRollbackConcurrencyAndScan(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Update(ctx, func(tx Tx) error { _, err := tx.Next("counter"); return err }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	before, _ := s.DataVersion(ctx)
	sentinel := errors.New("rollback")
	if err := s.Update(ctx, func(tx Tx) error { tx.Put("scan", "a", 1); tx.Next("counter"); return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if after, _ := s.DataVersion(ctx); after != before {
		t.Fatal("rollback advanced version")
	}
	if err := s.Update(ctx, func(tx Tx) error {
		for _, key := range []string{"a%/1", "a%/2", "a_/1", "中", "Z"} {
			if err := tx.Put("scan", key, true); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.View(ctx, func(tx Tx) error {
		rows, err := tx.Scan("scan", Scan{Prefix: "a%/", Reverse: true, After: "a%/2"})
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].Key != "a%/1" {
			return fmt.Errorf("prefix scan=%v", rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Update(canceled, func(tx Tx) error { return tx.Put("bad", "x", true) }); err == nil {
		t.Fatal("canceled transaction committed")
	}
}
func TestSQLiteUnknownSchemaAndRetiredCopy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.markTransferred(ctx, TransferSummary{SHA256: "receipt"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err := OpenSQLite(ctx, path); err == nil {
		s.Close()
		t.Fatal("retired source ran")
	}
	s, err = OpenSQLiteSource(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec("PRAGMA user_version=99")
	s.Close()
	if s, err := OpenSQLite(ctx, path); err == nil {
		s.Close()
		t.Fatal("unknown schema accepted")
	}
}
func TestTransferDigestPreservesLargeNumbers(t *testing.T) {
	digest := func(raw string) string {
		h := sha256.New()
		if err := hashJSON(h, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%x", h.Sum(nil))
	}
	if digest(`{"a":9007199254740993,"b":1.00}`) != digest(`{ "b": 1e0, "a":9007199254740993 }`) {
		t.Fatal("JSONB normalization changed digest")
	}
	if digest(`9007199254740993`) == digest(`9007199254740992`) {
		t.Fatal("lost large integer precision")
	}
}
func TestSQLiteTransferIntegration(t *testing.T) {
	target, _ := postgresIntegrationStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := target.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "source.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Update(ctx, func(tx Tx) error {
		if err := tx.Put("access.admin", "admin", map[string]any{"hash": "hashed", "n": int64(9007199254740993)}); err != nil {
			return err
		}
		return tx.Put("monitor.history", "event", map[string]any{"completed_at": time.Now().UTC(), "cipher": "encrypted"})
	}); err != nil {
		t.Fatal(err)
	}
	leader, err := target.AcquireLeadership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TransferSQLite(ctx, s, target); err == nil {
		t.Fatal("copied while target running")
	}
	leader.Close()
	want, err := TransferSQLite(ctx, s, target)
	if err != nil {
		t.Fatal(err)
	}
	if want.Records != 2 {
		t.Fatal(want)
	}
	if got, err := TransferSQLite(ctx, s, target); err != nil || got != want {
		t.Fatalf("retry=%+v %v", got, err)
	}
	if err := target.Update(ctx, func(tx Tx) error { return tx.Put("new", "record", true) }); err != nil {
		t.Fatal(err)
	}
	if _, err := TransferSQLite(ctx, s, target); err == nil {
		t.Fatal("overwrote changed target")
	}
}

func TestSQLiteBackupFailureRemovesOnlyItsOwnDestination(t *testing.T) {
	s, err := OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "source"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "backup")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Backup(ctx, path); err == nil {
		t.Fatal("canceled backup succeeded")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("incomplete backup remains: %v", err)
	}
	if err := os.WriteFile(path, []byte("existing backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(context.Background(), path); err == nil {
		t.Fatal("accepted existing destination")
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "existing backup" {
		t.Fatal("removed or changed existing backup", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(context.Background(), link); err == nil {
		t.Fatal("accepted symlink backup")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("removed existing symlink", err)
	}
}

func TestSQLiteTransferFailureIntegration(t *testing.T) {
	target, _ := postgresIntegrationStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := target.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	source, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "source"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.Update(ctx, func(tx Tx) error {
		if err := tx.Put("data", "a", 1); err != nil {
			return err
		}
		return tx.Put("data", "b", 2)
	}); err != nil {
		t.Fatal(err)
	}
	assertNotRetired := func() {
		t.Helper()
		var value string
		if err := source.db.QueryRowContext(ctx, "SELECT value FROM relay_local_metadata WHERE name='migrated_to_postgres'").Scan(&value); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("failed transfer retired source: %q, %v", value, err)
		}
	}
	if err := target.Update(ctx, func(tx Tx) error { return tx.Put("existing", "keep", "untouched") }); err != nil {
		t.Fatal(err)
	}
	if _, err := TransferSQLite(ctx, source, target); err == nil {
		t.Fatal("accepted nonempty target")
	}
	var existing string
	if err := target.View(ctx, func(tx Tx) error { return tx.Get("existing", "keep", &existing) }); err != nil || existing != "untouched" {
		t.Fatal("changed existing target", err)
	}
	assertNotRetired()
	if _, err := target.pool.Exec(ctx, `DELETE FROM relay_records; DELETE FROM relay_sequences;
CREATE FUNCTION fail_copy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected copy failure'; END $$;
CREATE TRIGGER fail_copy BEFORE INSERT ON relay_records FOR EACH ROW WHEN (NEW.record_key='b') EXECUTE FUNCTION fail_copy();`); err != nil {
		t.Fatal(err)
	}
	if _, err := TransferSQLite(ctx, source, target); err == nil {
		t.Fatal("copy failure committed")
	}
	var count int64
	if err := target.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM relay_records)+(SELECT count(*) FROM relay_sequences)").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial target writes remain: %d, %v", count, err)
	}
	var receiptExists bool
	if err := target.pool.QueryRow(ctx, "SELECT to_regclass('relay_sqlite_transfer') IS NOT NULL").Scan(&receiptExists); err != nil || receiptExists {
		t.Fatalf("failed transfer retained receipt infrastructure: %t, %v", receiptExists, err)
	}
	assertNotRetired()
	if _, err := target.pool.Exec(ctx, "DROP TRIGGER fail_copy ON relay_records; DROP FUNCTION fail_copy()"); err != nil {
		t.Fatal(err)
	}
	// Force a failure after the target transaction commits its receipt.
	if _, err := source.db.ExecContext(ctx, `CREATE TRIGGER fail_retire BEFORE INSERT ON relay_local_metadata WHEN NEW.name='migrated_to_postgres' BEGIN SELECT RAISE(ABORT,'injected retirement failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := TransferSQLite(ctx, source, target); err == nil {
		t.Fatal("source retirement failure was ignored")
	}
	assertNotRetired()
	var raw []byte
	if err := target.pool.QueryRow(ctx, "SELECT receipt FROM relay_sqlite_transfer WHERE id=1").Scan(&raw); err != nil {
		t.Fatal("committed transfer lost receipt", err)
	}
	if _, err := source.db.ExecContext(ctx, "DROP TRIGGER fail_retire"); err != nil {
		t.Fatal(err)
	}
	got, err := TransferSQLite(ctx, source, target)
	if err != nil || got.Records != 2 || got.Sequences != 1 {
		t.Fatalf("could not recover committed receipt: %+v, %v", got, err)
	}
	if err := CheckSQLiteCutover(ctx, source.path, target); err == nil {
		t.Fatal("cutover accepted source still locked by maintenance")
	}
}
