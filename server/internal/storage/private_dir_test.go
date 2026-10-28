package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "keep")
	if err := os.WriteFile(secret, []byte("keep data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("private directory mode: %v, %v", info, err)
	}
	if raw, err := os.ReadFile(secret); err != nil || string(raw) != "keep data" {
		t.Fatal("existing data changed", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(link); err == nil {
		t.Fatal("accepted symlink directory")
	}
	if err := EnsurePrivateDir(secret); err == nil {
		t.Fatal("accepted regular file as directory")
	}
}
