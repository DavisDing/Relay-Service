package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"relayservice/internal/access"
	"relayservice/internal/config"
	"relayservice/internal/storage"
)

func privateRead(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, fmt.Errorf("invalid local secret file")
	}
	return os.ReadFile(path)
}
func randomSecret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func writePrivate(path string, value []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".relay-pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(value); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Link(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// The caller holds the data-directory lock, shared by startup and migration.
// Existing local keys cannot be silently changed by an environment edit.
func prepareLocalKey(cfg *config.Config) (bool, error) {
	path := filepath.Join(cfg.DataDir, "encryption.key")
	raw, err := privateRead(path)
	if err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != 32 {
			return false, fmt.Errorf("invalid persisted encryption key; restore the data volume")
		}
		if len(cfg.EncryptionKey) > 0 && hex.EncodeToString(cfg.EncryptionKey) != hex.EncodeToString(key) {
			return false, fmt.Errorf("configured encryption key differs from persisted key")
		}
		cfg.EncryptionKey = key
		return false, nil
	}
	if !os.IsNotExist(err) {
		return false, err
	}
	generated := len(cfg.EncryptionKey) == 0
	if generated {
		// SQLite data without its key must never be adopted with a new key.
		if info, err := os.Stat(cfg.SQLitePath); err == nil && info.Size() > 0 {
			return false, fmt.Errorf("SQLite data exists without encryption.key; restore the original key")
		} else if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		cfg.EncryptionKey, _ = hex.DecodeString(randomSecret())
	}
	if err := writePrivate(path, []byte(hex.EncodeToString(cfg.EncryptionKey))); err != nil {
		return false, err
	}
	return generated, nil
}

func bindEncryptionKey(ctx context.Context, db storage.Store, key []byte, allowLegacy bool) error {
	digest := sha256.Sum256(key)
	fingerprint := hex.EncodeToString(digest[:])
	return db.Update(ctx, func(tx storage.Tx) error {
		var stored string
		err := tx.Get("system.storage", "encryption_key_sha256", &stored)
		if err == nil {
			if stored != fingerprint {
				return fmt.Errorf("database encryption key does not match; restore the original encryption.key")
			}
			return nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if !allowLegacy {
			var initialized bool
			err := tx.Get("access.state", "initialized", &initialized)
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			if initialized {
				return fmt.Errorf("existing database requires its original encryption key")
			}
		}
		return tx.Put("system.storage", "encryption_key_sha256", fingerprint)
	})
}

func prepareLocalInitial(ctx context.Context, cfg *config.Config, svc *access.Service) error {
	if cfg.InitialPassword != "" || cfg.BootstrapToken != "" {
		return nil
	}
	if cfg.InitialPasswordFile == "" {
		cfg.InitialPasswordFile = filepath.Join(cfg.DataDir, "initial-password")
	}
	raw, err := privateRead(cfg.InitialPasswordFile)
	if err == nil {
		cfg.InitialPassword = string(raw)
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	ready, err := svc.Initialized(ctx)
	if err != nil {
		return err
	}
	if ready {
		return nil
	}
	password := randomSecret()
	if err := writePrivate(cfg.InitialPasswordFile, []byte(password)); err != nil {
		return err
	}
	cfg.InitialPassword = password
	return nil
}
