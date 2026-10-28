package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"relayservice/internal/access"
	"relayservice/internal/config"
	"relayservice/internal/storage"
)

func TestLocalStartupCredentialsSurviveRestartAndCleanup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, SQLitePath: filepath.Join(dir, "relay.sqlite"), InitialPasswordFile: filepath.Join(dir, "initial-password")}
	if _, err := prepareLocalKey(&cfg); err != nil {
		t.Fatal(err)
	}
	key := append([]byte(nil), cfg.EncryptionKey...)
	s, err := storage.OpenSQLite(ctx, cfg.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := bindEncryptionKey(ctx, s, cfg.EncryptionKey, false); err != nil {
		t.Fatal(err)
	}
	svc, err := access.New(s, cfg.EncryptionKey, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareLocalInitial(ctx, &cfg, svc); err != nil {
		t.Fatal(err)
	}
	password := cfg.InitialPassword
	var logs bytes.Buffer
	if err := prepareInitialAdministrator(ctx, svc, &cfg, &logs); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(logs.Bytes(), []byte(password)) {
		t.Fatal("initial password missing from deployment log")
	}
	if err := prepareLocalInitial(ctx, &cfg, svc); err != nil {
		t.Fatal(err)
	}
	if cfg.InitialPassword != password {
		t.Fatal("restart changed initial password")
	}
	token, err := svc.Login(ctx, "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CompleteInitialPassword(ctx, token, "owner", "a long permanent test password"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.InitialPasswordFile); !os.IsNotExist(err) {
		t.Fatal("initial password survived completion")
	}
	cfg.InitialPassword = ""
	if err := prepareLocalInitial(ctx, &cfg, svc); err != nil {
		t.Fatal(err)
	}
	if cfg.InitialPassword != "" {
		t.Fatal("regenerated consumed password")
	}
	cfg.EncryptionKey = nil
	if _, err := prepareLocalKey(&cfg); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, cfg.EncryptionKey) {
		t.Fatal("key changed")
	}
	if err := os.Remove(filepath.Join(dir, "encryption.key")); err != nil {
		t.Fatal(err)
	}
	cfg.EncryptionKey = nil
	if _, err := prepareLocalKey(&cfg); err == nil {
		t.Fatal("replaced missing key for existing database")
	}
}
func TestPostgresPinPreventsSilentFallback(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir(), DatabaseURL: "postgres://app:password@postgres/relay"}
	if err := pinTarget(&cfg); err != nil {
		t.Fatal(err)
	}
	cfg.DatabaseURL = "postgresql://app:new-password@POSTGRES:5432/relay"
	if err := checkTargetPin(&cfg); err != nil {
		t.Fatal(err)
	}
	cfg.DatabaseURL = ""
	if err := checkTargetPin(&cfg); err == nil {
		t.Fatal("fell back to SQLite")
	}
	cfg.DatabaseURL = "postgres://app:password@other/relay"
	if err := checkTargetPin(&cfg); err == nil {
		t.Fatal("changed database silently")
	}
}
