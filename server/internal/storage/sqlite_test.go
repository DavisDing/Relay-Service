package storage

import (
	"context"
	"crypto/sha256"
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
