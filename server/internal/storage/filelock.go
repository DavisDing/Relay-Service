package storage

import (
	"fmt"
	"os"
	"syscall"
)

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
