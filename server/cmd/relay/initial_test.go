package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"relayservice/internal/access"
	"relayservice/internal/config"
	"relayservice/internal/storage"
	"strings"
	"testing"
)

func TestInitialCredentialLifecycle(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	defer store.Close()
	svc, _ := access.New(store, make([]byte, 32), "", false)
	initial := strings.Repeat("a", 64)
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{InitialPassword: initial, InitialPasswordFile: path}
	var log bytes.Buffer
	if err := prepareInitialAdministrator(ctx, svc, &cfg, &log); err != nil {
		t.Fatal(err)
	}
	if cfg.InitialPassword != "" || !strings.Contains(log.String(), initial) {
		t.Fatal("credential display/reference lifecycle incorrect")
	}
	token, err := svc.Login(ctx, "admin", initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CompleteInitialPassword(ctx, token, "owner", initial); err == nil {
		t.Fatal("accepted unchanged credential")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("failed setup consumed credential")
	}
	if err := svc.CompleteInitialPassword(ctx, token, "owner", "permanent password for testing"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("credential file remains: %v", err)
	}
	// Recreate residue to simulate a crash between the DB commit and file deletion.
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	restarted, _ := access.New(store, make([]byte, 32), "", false)
	cfg.InitialPassword = initial
	log.Reset()
	if err := prepareInitialAdministrator(ctx, restarted, &cfg, &log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("restart did not retry cleanup")
	}
	if log.Len() != 0 || cfg.InitialPassword != "" {
		t.Fatal("completed account retained/displayed expired password")
	}
	if _, err := restarted.Login(ctx, "owner", "permanent password for testing"); err != nil {
		t.Fatal(err)
	}
}

func TestPendingRestartPreservesOriginalCredential(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	defer store.Close()
	svc, _ := access.New(store, make([]byte, 32), "", false)
	initial := strings.Repeat("b", 64)
	if _, err := svc.EnsureInitialAdministrator(ctx, initial); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{initial, "", strings.Repeat("c", 64)} {
		restarted, _ := access.New(store, make([]byte, 32), "", false)
		cfg := config.Config{InitialPassword: password}
		var log bytes.Buffer
		if err := prepareInitialAdministrator(ctx, restarted, &cfg, &log); err != nil {
			t.Fatal(err)
		}
		if cfg.InitialPassword != "" {
			t.Fatal("config retained initial password")
		}
		if password != initial && log.Len() != 0 {
			t.Fatal("printed an invalid credential")
		}
		if _, err := restarted.Login(ctx, "admin", initial); err != nil {
			t.Fatal("original credential changed:", err)
		}
	}
}

func TestCleanupFailureDoesNotUndoCommittedPasswordChange(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	defer store.Close()
	svc, _ := access.New(store, make([]byte, 32), "", false)
	initial := strings.Repeat("d", 64)
	// A directory simulates a credential path that cannot be safely deleted.
	path := filepath.Join(t.TempDir(), "password")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{InitialPassword: initial, InitialPasswordFile: path}
	var log bytes.Buffer
	if err := prepareInitialAdministrator(ctx, svc, &cfg, &log); err != nil {
		t.Fatal(err)
	}
	token, err := svc.Login(ctx, "admin", initial)
	if err != nil {
		t.Fatal(err)
	}
	permanent := "permanent password after cleanup failure"
	if err := svc.CompleteInitialPassword(ctx, token, "owner", permanent); err != nil {
		t.Fatal("cleanup failure changed DB result:", err)
	}
	if _, err := svc.Login(ctx, "owner", permanent); err != nil {
		t.Fatal("permanent account lost:", err)
	}
	if _, err := svc.Login(ctx, "admin", initial); err == nil {
		t.Fatal("old initial credential survived")
	}
}
