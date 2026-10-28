package storage

import (
	"fmt"
	"os"
	"syscall"
)

// EnsurePrivateDir also secures an existing directory before secrets or locks
// are opened. Refuse a symlink at the configured path; chmod the opened inode.
func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create private data directory: %w", err)
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open private data directory: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if err := f.Chmod(0700); err != nil {
		return fmt.Errorf("secure data directory: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("verify data directory permissions: %w", err)
	}
	if info.Mode().Perm() != 0700 {
		return fmt.Errorf("data directory must have mode 0700")
	}
	return nil
}

// LockFile holds an OS lock, released on process exit even after a crash. Never
// unlink a lock file: another process may already have opened the same inode.
func LockFile(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open local database lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("local database is in use; stop Relay before maintenance")
	}
	return f, nil
}
