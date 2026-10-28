// Package access implements the Relay administrator and read-only device identities.
package access

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"relayservice/internal/storage"
)

var (
	ErrUnauthorized       = errors.New("unauthorized")
	ErrInvalidInput       = errors.New("invalid input")
	ErrAlreadyInitialized = errors.New("administrator already initialized")
	ErrRateLimited        = errors.New("too many failed authentication attempts")
	ErrExpired            = errors.New("credential expired or revoked")
	ErrBootstrapDisabled  = errors.New("bootstrap is disabled")
)

const (
	adminBucket         = "access.admin"
	sessionBucket       = "access.sessions"
	deviceBucket        = "access.devices"
	pairingBucket       = "access.pairings"
	stateBucket         = "access.state"
	relayIdentityKey    = "relay_id"
	initializedKey      = "initialized"
	pairFailuresKey     = "pairing.failures"
	pairLockKey         = "pairing.locked_until"
	loginFailuresKey    = "login.failures"
	loginLockKey        = "login.locked_until"
	sessionTTL          = 12 * time.Hour
	pairingTTL          = 5 * time.Minute
	deviceTTL           = 180 * 24 * time.Hour
	pairingFailureLimit = 10
	pairingLockDuration = 5 * time.Minute
	loginLockDuration   = 5 * time.Minute
	legacyIssuer        = "legacy_unknown"
)

type Service struct {
	store          storage.Store
	key            []byte
	bootstrapToken []byte
	secureCookies  bool
	now            func() time.Time
	random         io.Reader
	mu             sync.Mutex
}

// New creates an access service. The independent AES key must be 32 bytes. A blank
// deployment bootstrap token disables first-run administrator creation.
func New(store storage.Store, encryptionKey []byte, bootstrapToken string, secureCookies bool) (*Service, error) {
	if store == nil || len(encryptionKey) != 32 {
		return nil, fmt.Errorf("access requires a store and a 32-byte encryption key: %w", ErrInvalidInput)
	}
	if bootstrapToken != "" && len(bootstrapToken) < 32 {
		return nil, fmt.Errorf("bootstrap token must be at least 32 bytes: %w", ErrInvalidInput)
	}
	return &Service{store: store, key: append([]byte(nil), encryptionKey...), bootstrapToken: []byte(bootstrapToken), secureCookies: secureCookies, now: time.Now, random: rand.Reader}, nil
}

func (s *Service) SecureCookies() bool { return s.secureCookies }

// Initialized reports only whether the one-time administrator setup completed.
// It never returns administrator details or the deployment bootstrap token.
func (s *Service) Initialized(ctx context.Context) (bool, error) {
	var initialized bool
	err := s.store.View(ctx, func(tx storage.Tx) error {
		return tx.Get(stateBucket, initializedKey, &initialized)
	})
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	return initialized, err
}

func (s *Service) Bootstrap(ctx context.Context, token, username, password string) error {
	if len(s.bootstrapToken) == 0 {
		return ErrBootstrapDisabled
	}
	if len(token) != len(s.bootstrapToken) || subtle.ConstantTimeCompare([]byte(token), s.bootstrapToken) != 1 {
		return ErrUnauthorized
	}
	username = strings.TrimSpace(username)
	if len(username) < 3 || len(username) > 64 || !validUsername(username) || len(password) < 12 || len(password) > 1024 {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var initialized bool
	if err := s.store.View(ctx, func(tx storage.Tx) error { return tx.Get(stateBucket, initializedKey, &initialized) }); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if initialized {
		return ErrAlreadyInitialized
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(s.random, salt); err != nil {
		return err
	}
	hash := hashPassword(password, salt)
	admin := administrator{ID: "admin", Username: username, Salt: base64.RawStdEncoding.EncodeToString(salt), PasswordHash: base64.RawStdEncoding.EncodeToString(hash)}
	return s.store.Update(ctx, func(tx storage.Tx) error {
		var initialized bool
		if err := tx.Get(stateBucket, initializedKey, &initialized); err == nil && initialized {
			return ErrAlreadyInitialized
		} else if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if err := tx.Put(adminBucket, admin.ID, admin); err != nil {
			return err
		}
		return tx.Put(stateBucket, initializedKey, true)
	})
}

func validUsername(value string) bool {
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-' || ch == '@') {
			return false
		}
	}
	return true
}

type administrator struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	Salt               string `json:"salt"`
	PasswordHash       string `json:"password_hash"`
	MustChangePassword bool   `json:"must_change_password,omitempty"`
	CredentialVersion  uint64 `json:"credential_version,omitempty"`
}
type session struct {
	ID                string    `json:"id"`
	AdminID           string    `json:"admin_id"`
	ExpiresAt         time.Time `json:"expires_at"`
	CredentialVersion uint64    `json:"credential_version,omitempty"`
}
type deviceRecord struct {
	Device      Device    `json:"device"`
	TokenDigest string    `json:"token_digest"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type Device struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	IssuedBy  string     `json:"issued_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}
type Principal struct {
	ID                 string `json:"id"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password,omitempty"`
}

type Pairing struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}
type pairing struct {
	Digest    string    `json:"digest"`
	ExpiresAt time.Time `json:"expires_at"`
	Attempts  int       `json:"attempts"`
	IssuedBy  string    `json:"issued_by,omitempty"`
}
type DeviceToken struct {
	Token     string    `json:"token"`
	DeviceID  string    `json:"device_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if len(username) > 64 || len(password) > 1024 {
		return "", ErrUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	locked, err := s.loginLocked(ctx)
	if err != nil {
		return "", err
	}
	if locked {
		return "", ErrRateLimited
	}
	var admin administrator
	if err := s.store.View(ctx, func(tx storage.Tx) error { return tx.Get(adminBucket, "admin", &admin) }); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "", s.failedLogin(ctx)
		}
		return "", err
	}
	salt, err := base64.RawStdEncoding.DecodeString(admin.Salt)
	if err != nil || len(salt) != 16 {
		return "", errors.New("stored administrator credential is invalid")
	}
	expected, err := base64.RawStdEncoding.DecodeString(admin.PasswordHash)
	if err != nil || len(expected) != 32 {
		return "", errors.New("stored administrator credential is invalid")
	}
	actual := hashPassword(password, salt)
	if subtle.ConstantTimeCompare([]byte(username), []byte(admin.Username)) != 1 || subtle.ConstantTimeCompare(actual, expected) != 1 {
		return "", s.failedLogin(ctx)
	}
	if err := s.store.Update(ctx, func(tx storage.Tx) error {
		if err := tx.Put(stateBucket, loginFailuresKey, 0); err != nil {
			return err
		}
		return tx.Put(stateBucket, loginLockKey, time.Time{})
	}); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return "", err
	}
	token, digest, err := s.newToken()
	if err != nil {
		return "", err
	}
	item := session{ID: admin.ID, AdminID: admin.ID, ExpiresAt: s.now().Add(sessionTTL), CredentialVersion: admin.CredentialVersion}
	if err := s.store.Update(ctx, func(tx storage.Tx) error {
		var current administrator
		if err := tx.Get(adminBucket, admin.ID, &current); err != nil {
			return err
		}
		if current.CredentialVersion != admin.CredentialVersion {
			return ErrUnauthorized
		}
		return tx.Put(sessionBucket, digest, item)
	}); err != nil {
		return "", err
	}
	return token, nil
}

func hashPassword(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 2, 32*1024, 2, 32)
}

func (s *Service) loginLocked(ctx context.Context) (bool, error) {
	var until time.Time
	err := s.store.View(ctx, func(tx storage.Tx) error { return tx.Get(stateBucket, loginLockKey, &until) })
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !s.now().Before(until) {
		return false, nil
	}
	return true, nil
}

func (s *Service) failedLogin(ctx context.Context) error {
	var errLocked = ErrUnauthorized
	err := s.store.Update(ctx, func(tx storage.Tx) error {
		var failures int
		if err := tx.Get(stateBucket, loginFailuresKey, &failures); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		failures++
		if err := tx.Put(stateBucket, loginFailuresKey, failures); err != nil {
			return err
		}
		if failures >= 8 {
			if err := tx.Put(stateBucket, loginLockKey, s.now().Add(loginLockDuration)); err != nil {
				return err
			}
			if err := tx.Put(stateBucket, loginFailuresKey, 0); err != nil {
				return err
			}
			errLocked = ErrRateLimited
		}
		return nil
	})
	if err != nil {
		return err
	}
	return errLocked
}

func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrUnauthorized
	}
	digest := digestToken(token)
	var adminSession session
	var admin administrator
	err := s.store.View(ctx, func(tx storage.Tx) error {
		if err := tx.Get(sessionBucket, digest, &adminSession); err != nil {
			return err
		}
		return tx.Get(adminBucket, adminSession.AdminID, &admin)
	})
	if err == nil {
		if !s.now().Before(adminSession.ExpiresAt) || adminSession.CredentialVersion != admin.CredentialVersion {
			_ = s.store.Update(ctx, func(tx storage.Tx) error { return tx.Delete(sessionBucket, digest) })
			return Principal{}, ErrExpired
		}
		return Principal{ID: adminSession.AdminID, Role: "admin", MustChangePassword: admin.MustChangePassword}, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return Principal{}, err
	}
	var device deviceRecord
	err = s.store.View(ctx, func(tx storage.Tx) error { return tx.Get(deviceBucket, digest, &device) })
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Principal{}, ErrUnauthorized
		}
		return Principal{}, err
	}
	if device.TokenDigest != digest || device.Device.RevokedAt != nil || !s.now().Before(device.ExpiresAt) {
		return Principal{}, ErrExpired
	}
	return Principal{ID: device.Device.ID, Role: "device"}, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.Update(ctx, func(tx storage.Tx) error {
		var existing session
		err := tx.Get(sessionBucket, digestToken(token), &existing)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return tx.Delete(sessionBucket, digestToken(token))
	})
}

func (s *Service) CreatePairing(ctx context.Context, actorID string) (Pairing, error) {
	if actorID != "admin" {
		return Pairing{}, ErrUnauthorized
	}
	codeBytes := make([]byte, 8)
	if _, err := io.ReadFull(s.random, codeBytes); err != nil {
		return Pairing{}, err
	}
	// Base32 has a single letter case. Unlike uppercasing base64url, this
	// encoding preserves every bit from the random source.
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(codeBytes)
	expires := s.now().Add(pairingTTL)
	item := pairing{Digest: digestToken(code), ExpiresAt: expires, IssuedBy: actorID}
	err := s.store.Update(ctx, func(tx storage.Tx) error {
		var lock time.Time
		if err := tx.Get(stateBucket, pairLockKey, &lock); err == nil && s.now().Before(lock) {
			return ErrRateLimited
		} else if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if err := tx.Put(pairingBucket, item.Digest, item); err != nil {
			return err
		}
		return storage.AppendAudit(tx, storage.AuditRecord{ActorID: actorID, Action: "device.pairing_issued", At: s.now()})
	})
	if err != nil {
		return Pairing{}, err
	}
	return Pairing{Code: code, ExpiresAt: expires}, nil
}

func (s *Service) Pair(ctx context.Context, code, name string) (DeviceToken, error) {
	code = strings.TrimSpace(code)
	if !validDeviceName(name) {
		return DeviceToken{}, ErrInvalidInput
	}
	name = strings.TrimSpace(name)
	if code == "" || len(code) > 64 || len(name) < 1 || len(name) > 80 {
		return DeviceToken{}, ErrInvalidInput
	}
	// New codes are 13-character base32. Legacy codes were 11-character
	// uppercase base64url and must keep their original lookup behavior.
	if len(code) == 13 {
		code = strings.ToUpper(code)
	}
	token, tokenDigest, err := s.newToken()
	if err != nil {
		return DeviceToken{}, err
	}
	idBytes := make([]byte, 16)
	if _, err := io.ReadFull(s.random, idBytes); err != nil {
		return DeviceToken{}, err
	}
	deviceID := hex.EncodeToString(idBytes)
	var resultErr error
	expiresAt := s.now().Add(deviceTTL)
	err = s.store.Update(ctx, func(tx storage.Tx) error {
		var lock time.Time
		if e := tx.Get(stateBucket, pairLockKey, &lock); e == nil && s.now().Before(lock) {
			resultErr = ErrRateLimited
			return nil
		} else if e != nil && !errors.Is(e, storage.ErrNotFound) {
			return e
		}
		codeDigest := digestToken(code)
		var item pairing
		e := tx.Get(pairingBucket, codeDigest, &item)
		if e != nil && !errors.Is(e, storage.ErrNotFound) {
			return e
		}
		if e != nil || !s.now().Before(item.ExpiresAt) || item.Digest != codeDigest {
			var failures int
			if e := tx.Get(stateBucket, pairFailuresKey, &failures); e != nil && !errors.Is(e, storage.ErrNotFound) {
				return e
			}
			failures++
			if err := tx.Put(stateBucket, pairFailuresKey, failures); err != nil {
				return err
			}
			if failures >= pairingFailureLimit {
				if err := tx.Put(stateBucket, pairLockKey, s.now().Add(pairingLockDuration)); err != nil {
					return err
				}
				if err := tx.Put(stateBucket, pairFailuresKey, 0); err != nil {
					return err
				}
			}
			resultErr = ErrUnauthorized
			return nil
		}
		if err := tx.Delete(pairingBucket, codeDigest); err != nil {
			return err
		}
		if err := tx.Put(stateBucket, pairFailuresKey, 0); err != nil {
			return err
		}
		issuer := item.IssuedBy
		if issuer == "" {
			issuer = legacyIssuer
		}
		record := deviceRecord{Device: Device{ID: deviceID, Name: name, IssuedBy: issuer, CreatedAt: s.now(), ExpiresAt: expiresAt}, TokenDigest: tokenDigest, ExpiresAt: expiresAt}
		if err := tx.Put(deviceBucket, tokenDigest, record); err != nil {
			return err
		}
		return storage.AppendAudit(tx, storage.AuditRecord{ActorID: issuer, TargetID: deviceID, Action: "device.paired", At: s.now()})
	})
	if err != nil {
		return DeviceToken{}, err
	}
	if resultErr != nil {
		return DeviceToken{}, resultErr
	}
	return DeviceToken{Token: token, DeviceID: deviceID, ExpiresAt: expiresAt}, nil
}

func validDeviceName(name string) bool {
	if !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		// Reject control characters and Unicode bidi controls, which can
		// conceal or visually reorder identities in administration screens.
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || r == '\u061c' ||
			r == '\u200e' || r == '\u200f' || (r >= '\u202a' && r <= '\u202e') ||
			(r >= '\u2066' && r <= '\u2069') {
			return false
		}
	}
	return true
}

func (s *Service) Devices(ctx context.Context) ([]Device, error) {
	devices := make([]Device, 0)
	if err := s.store.View(ctx, func(tx storage.Tx) error {
		after := ""
		for {
			records, err := tx.Scan(deviceBucket, storage.Scan{After: after, Limit: 500})
			if err != nil {
				return err
			}
			for _, item := range records {
				var record deviceRecord
				if err := json.Unmarshal(item.Value, &record); err != nil {
					return err
				}
				record.Device.ExpiresAt = record.ExpiresAt
				if record.Device.IssuedBy == "" {
					record.Device.IssuedBy = legacyIssuer
				}
				devices = append(devices, record.Device)
			}
			if len(records) < 500 {
				return nil
			}
			after = records[len(records)-1].Key
		}
	}); err != nil {
		return nil, err
	}
	return devices, nil
}

func (s *Service) Revoke(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidInput
	}
	return s.store.Update(ctx, func(tx storage.Tx) error {
		after := ""
		for {
			records, err := tx.Scan(deviceBucket, storage.Scan{After: after, Limit: 500})
			if err != nil {
				return err
			}
			for _, item := range records {
				var record deviceRecord
				if err := json.Unmarshal(item.Value, &record); err != nil {
					return err
				}
				if record.Device.ID == id {
					if record.Device.RevokedAt != nil {
						return nil
					}
					now := s.now()
					record.Device.RevokedAt = &now
					record.TokenDigest = ""
					if err := tx.Put(deviceBucket, item.Key, record); err != nil {
						return err
					}
					// Revoke is exposed only by the single-administrator route.
					return storage.AppendAudit(tx, storage.AuditRecord{ActorID: "admin", TargetID: id, Action: "device.revoked", At: now})
				}
			}
			if len(records) < 500 {
				return storage.ErrNotFound
			}
			after = records[len(records)-1].Key
		}
	})
}

func (s *Service) RelayID(ctx context.Context) (string, error) {
	var id string
	err := s.store.Update(ctx, func(tx storage.Tx) error {
		if err := tx.Get(stateBucket, relayIdentityKey, &id); err == nil {
			return nil
		} else if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		raw := make([]byte, 16)
		if _, err := io.ReadFull(s.random, raw); err != nil {
			return err
		}
		id = hex.EncodeToString(raw)
		return tx.Put(stateBucket, relayIdentityKey, id)
	})
	return id, err
}

// Encrypt stores secrets with AES-256-GCM and authenticated versioned base64url.
func (s *Service) Encrypt(plaintext string) (string, error) {
	return s.EncryptFor("generic", "generic", plaintext)
}

// EncryptFor authenticates the secret's storage purpose and stable owner ID. Callers
// cannot copy ciphertext to another service credential slot or reinterpret its use.
func (s *Service) EncryptFor(purpose, owner, plaintext string) (string, error) {
	if !validSecretContext(purpose, owner) {
		return "", ErrInvalidInput
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return "", err
	}
	payload := aead.Seal(nonce, nonce, []byte(plaintext), secretAAD(purpose, owner))
	return "v1." + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (s *Service) Decrypt(value string) (string, error) {
	return s.DecryptFor("generic", "generic", value)
}

func (s *Service) DecryptFor(purpose, owner, value string) (string, error) {
	if !validSecretContext(purpose, owner) {
		return "", ErrInvalidInput
	}
	if !strings.HasPrefix(value, "v1.") {
		return "", ErrInvalidInput
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(value, "v1."))
	if err != nil {
		return "", ErrInvalidInput
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) < aead.NonceSize()+aead.Overhead() {
		return "", ErrInvalidInput
	}
	nonce, ciphertext := payload[:aead.NonceSize()], payload[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, secretAAD(purpose, owner))
	if err != nil {
		return "", ErrUnauthorized
	}
	return string(plaintext), nil
}

func validSecretContext(purpose, owner string) bool {
	return len(purpose) > 0 && len(purpose) <= 80 && len(owner) > 0 && len(owner) <= 128 && !strings.ContainsRune(purpose, 0) && !strings.ContainsRune(owner, 0)
}
func secretAAD(purpose, owner string) []byte {
	// Length-prefix both fields so caller-selected delimiters cannot make two
	// distinct purpose/owner pairs authenticate as the same associated data.
	encoded := make([]byte, 0, 8+len(purpose)+len(owner))
	encoded = append(encoded, byte(len(purpose)>>8), byte(len(purpose)))
	encoded = append(encoded, purpose...)
	encoded = append(encoded, byte(len(owner)>>8), byte(len(owner)))
	encoded = append(encoded, owner...)
	return append([]byte("relayservice:secret:v1:"), encoded...)
}

func (s *Service) newToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(s.random, raw); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, digestToken(token), nil
}
func digestToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
