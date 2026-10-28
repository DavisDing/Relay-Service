package access

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"relayservice/internal/storage"
)

// EnsureInitialAdministrator creates a restricted initial account only on an
// empty installation. Restarts never reset a completed administrator account.
// The caller may display the instance password in deployment logs only while
// the stored account still requires its first password change.
func (s *Service) EnsureInitialAdministrator(ctx context.Context, password string) (bool, error) {
	if len(password) < 32 || len(password) > 1024 {
		return false, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending bool
	err := s.store.Update(ctx, func(tx storage.Tx) error {
		var initialized bool
		if err := tx.Get(stateBucket, initializedKey, &initialized); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if initialized {
			var admin administrator
			if err := tx.Get(adminBucket, "admin", &admin); err != nil {
				return err
			}
			pending = admin.MustChangePassword
			if pending {
				salt, err := base64.RawStdEncoding.DecodeString(admin.Salt)
				if err != nil || len(salt) != 16 || base64.RawStdEncoding.EncodeToString(hashPassword(password, salt)) != admin.PasswordHash {
					return ErrInvalidInput
				}
			}
			return nil
		}
		var existing administrator
		if err := tx.Get(adminBucket, "admin", &existing); err == nil {
			// Missing/corrupt initialization state must never reset an account.
			return ErrAlreadyInitialized
		} else if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		salt := make([]byte, 16)
		if _, err := io.ReadFull(s.random, salt); err != nil {
			return err
		}
		admin := administrator{ID: "admin", Username: "admin", Salt: base64.RawStdEncoding.EncodeToString(salt), PasswordHash: base64.RawStdEncoding.EncodeToString(hashPassword(password, salt)), MustChangePassword: true}
		if err := tx.Put(adminBucket, admin.ID, admin); err != nil {
			return err
		}
		pending = true
		return tx.Put(stateBucket, initializedKey, true)
	})
	return pending, err
}

// CompleteInitialPassword upgrades a restricted session to a permanent account.
// Incrementing the credential version invalidates every initial session,
// including sessions obtained concurrently from another browser or process.
func (s *Service) CompleteInitialPassword(ctx context.Context, token, username, password string) error {
	username = strings.TrimSpace(username)
	if len(username) < 3 || len(username) > 64 || !validUsername(username) || len(password) < 12 || len(password) > 1024 {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Update(ctx, func(tx storage.Tx) error {
		var current session
		if err := tx.Get(sessionBucket, digestToken(token), &current); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return ErrUnauthorized
			}
			return err
		}
		var admin administrator
		if err := tx.Get(adminBucket, current.AdminID, &admin); err != nil {
			return err
		}
		if !s.now().Before(current.ExpiresAt) || current.CredentialVersion != admin.CredentialVersion {
			return ErrExpired
		}
		if !admin.MustChangePassword {
			return ErrAlreadyInitialized
		}
		oldSalt, err := base64.RawStdEncoding.DecodeString(admin.Salt)
		if err != nil {
			return err
		}
		oldHash := base64.RawStdEncoding.EncodeToString(hashPassword(password, oldSalt))
		if subtle.ConstantTimeCompare([]byte(oldHash), []byte(admin.PasswordHash)) == 1 {
			return ErrInvalidInput
		}
		salt := make([]byte, 16)
		if _, err := io.ReadFull(s.random, salt); err != nil {
			return err
		}
		admin.Username = username
		admin.Salt = base64.RawStdEncoding.EncodeToString(salt)
		admin.PasswordHash = base64.RawStdEncoding.EncodeToString(hashPassword(password, salt))
		admin.MustChangePassword = false
		admin.CredentialVersion++
		return tx.Put(adminBucket, admin.ID, admin)
	})
}
