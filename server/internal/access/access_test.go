package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"relayservice/internal/storage"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestService(t *testing.T) (*Service, *storage.Memory) {
	t.Helper()
	store := storage.NewMemory()
	svc, err := New(store, []byte(strings.Repeat("k", 32)), strings.Repeat("b", 40), true)
	if err != nil {
		t.Fatal(err)
	}
	return svc, store
}
func TestBootstrapIsOneTimeAndStoresOnlyPasswordHash(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	if initialized, err := svc.Initialized(ctx); initialized || err != nil {
		t.Fatalf("new store initialized=%v err=%v", initialized, err)
	}
	if err := svc.Bootstrap(ctx, "wrong-token", "relay-admin", "correct horse battery"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token: %v", err)
	}
	if err := svc.Bootstrap(ctx, strings.Repeat("b", 40), "relay-admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if initialized, err := svc.Initialized(ctx); !initialized || err != nil {
		t.Fatalf("setup state initialized=%v err=%v", initialized, err)
	}
	if err := svc.Bootstrap(ctx, strings.Repeat("b", 40), "relay-admin", "correct horse battery"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second bootstrap: %v", err)
	}
	var admin administrator
	if err := store.View(ctx, func(tx storage.Tx) error { return tx.Get(adminBucket, "admin", &admin) }); err != nil {
		t.Fatal(err)
	}
	if admin.PasswordHash == "correct horse battery" || admin.Salt == "" || admin.PasswordHash == "" {
		t.Fatal("password is not stored as salted hash")
	}
	if _, err := svc.Login(ctx, "relay-admin", "incorrect battery"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password: %v", err)
	}
}
func TestConcurrentBootstrapCreatesExactlyOneAdministrator(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- svc.Bootstrap(ctx, strings.Repeat("b", 40), "relay-admin", "correct horse battery")
		}()
	}
	wg.Wait()
	close(results)
	var success, already int
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrAlreadyInitialized) {
			already++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || already != 7 {
		t.Fatalf("success=%d already=%d", success, already)
	}
	var records []storage.Record
	if err := store.View(ctx, func(tx storage.Tx) error {
		var err error
		records, err = tx.Scan(adminBucket, storage.Scan{Limit: 500})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("admin count %d", len(records))
	}
}

func TestConcurrentBootstrapAcrossServiceInstancesCreatesExactlyOneAdministrator(t *testing.T) {
	ctx := context.Background()
	one, store := newTestService(t)
	two, err := New(store, []byte(strings.Repeat("k", 32)), strings.Repeat("b", 40), true)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, svc := range []*Service{one, two} {
		go func(svc *Service) {
			<-start
			results <- svc.Bootstrap(ctx, strings.Repeat("b", 40), "relay-admin", "correct horse battery")
		}(svc)
	}
	close(start)
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, ErrAlreadyInitialized)) || (second == nil && errors.Is(first, ErrAlreadyInitialized))) {
		t.Fatalf("expected one successful bootstrap, got %v and %v", first, second)
	}
	var records []storage.Record
	if err := store.View(ctx, func(tx storage.Tx) error {
		var e error
		records, e = tx.Scan(adminBucket, storage.Scan{Limit: 500})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("administrator count=%d", len(records))
	}
}
func TestSessionTokenIsOneTimeAndRevocable(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	if err := svc.Bootstrap(ctx, strings.Repeat("b", 40), "admin-user", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	token, err := svc.Login(ctx, "admin-user", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.Authenticate(ctx, token)
	if err != nil || p.Role != "admin" {
		t.Fatalf("authenticate %#v %v", p, err)
	}
	var records []storage.Record
	if err := store.View(ctx, func(tx storage.Tx) error {
		var err error
		records, err = tx.Scan(sessionBucket, storage.Scan{Limit: 500})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustJSON(t, records)), token) {
		t.Fatal("raw session token persisted")
	}
	if err := svc.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("logged-out token accepted %v", err)
	}
}
func TestPairingIsSingleUseAndRevocationIsImmediate(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	if err := svc.Bootstrap(ctx, strings.Repeat("b", 40), "admin-user", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	pairing, err := svc.CreatePairing(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	device, err := svc.Pair(ctx, pairing.Code, "living room Mac")
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.Authenticate(ctx, device.Token)
	if err != nil || p.Role != "device" || p.ID != device.DeviceID {
		t.Fatalf("device auth %#v %v", p, err)
	}
	if _, err := svc.Pair(ctx, pairing.Code, "replay"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("code replay: %v", err)
	}
	if err := svc.Revoke(ctx, device.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, device.Token); !errors.Is(err, ErrExpired) {
		t.Fatalf("revoked token accepted: %v", err)
	}
	devices, err := svc.Devices(ctx)
	if err != nil || len(devices) != 1 || devices[0].RevokedAt == nil {
		t.Fatalf("devices %#v %v", devices, err)
	}
}

func TestPairingCodeIsSingleUseAcrossServiceInstances(t *testing.T) {
	ctx := context.Background()
	one, store := newTestService(t)
	two, err := New(store, []byte(strings.Repeat("k", 32)), strings.Repeat("b", 40), true)
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := one.CreatePairing(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, svc := range []*Service{one, two} {
		go func(svc *Service) { <-start; _, e := svc.Pair(ctx, pairing.Code, "device"); results <- e }(svc)
	}
	close(start)
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, ErrUnauthorized)) || (second == nil && errors.Is(first, ErrUnauthorized))) {
		t.Fatalf("expected one successful pairing, got %v and %v", first, second)
	}
	devices, err := one.Devices(ctx)
	if err != nil || len(devices) != 1 {
		t.Fatalf("paired devices=%d err=%v", len(devices), err)
	}
}
func TestPairingFailuresAreRateLimited(t *testing.T) {
	svc, _ := newTestService(t)
	for i := 0; i < pairingFailureLimit; i++ {
		if _, err := svc.Pair(context.Background(), "invalid", "device"); err == nil {
			t.Fatal("invalid code accepted")
		}
	}
	if _, err := svc.CreatePairing(context.Background(), "admin"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("not locked: %v", err)
	}
}

func TestPairingLockStorageFailureRollsBackAttempt(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	if err := store.Update(ctx, func(tx storage.Tx) error { return tx.Put(stateBucket, pairFailuresKey, pairingFailureLimit-1) }); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("write failed")
	svc.store = pairingResetFaultStore{Store: store, err: fault}
	if _, err := svc.Pair(ctx, "invalid", "device"); !errors.Is(err, fault) {
		t.Fatalf("write failure hidden: %v", err)
	}
	if err := store.View(ctx, func(tx storage.Tx) error {
		var failures int
		if err := tx.Get(stateBucket, pairFailuresKey, &failures); err != nil {
			return err
		}
		if failures != pairingFailureLimit-1 {
			t.Fatalf("failed transaction updated attempts: %d", failures)
		}
		var until time.Time
		if err := tx.Get(stateBucket, pairLockKey, &until); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("failed transaction left lock: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type pairingResetFaultStore struct {
	storage.Store
	err error
}

func (s pairingResetFaultStore) Update(ctx context.Context, fn func(storage.Tx) error) error {
	return s.Store.Update(ctx, func(tx storage.Tx) error { return fn(pairingResetFaultTx{Tx: tx, err: s.err}) })
}

type pairingResetFaultTx struct {
	storage.Tx
	err error
}

func (tx pairingResetFaultTx) Put(bucket, key string, value any) error {
	if bucket == stateBucket && key == pairFailuresKey {
		if n, ok := value.(int); ok && n == 0 {
			return tx.err
		}
	}
	return tx.Tx.Put(bucket, key, value)
}

func TestDeviceListAndRevokeCoverEveryStoragePage(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	if err := store.Update(ctx, func(tx storage.Tx) error {
		for i := 0; i < 501; i++ {
			key := fmt.Sprintf("digest-%04d", i)
			record := deviceRecord{Device: Device{ID: key, Name: "device", CreatedAt: time.Now()}, TokenDigest: key, ExpiresAt: time.Now().Add(time.Hour)}
			if err := tx.Put(deviceBucket, key, record); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	devices, err := svc.Devices(ctx)
	if err != nil || len(devices) != 501 {
		t.Fatalf("device list truncated: count=%d err=%v", len(devices), err)
	}
	if err := svc.Revoke(ctx, "digest-0500"); err != nil {
		t.Fatalf("cannot revoke device on second page: %v", err)
	}
	var record deviceRecord
	if err := store.View(ctx, func(tx storage.Tx) error { return tx.Get(deviceBucket, "digest-0500", &record) }); err != nil {
		t.Fatal(err)
	}
	if record.Device.RevokedAt == nil || record.TokenDigest != "" {
		t.Fatal("revocation was not persisted")
	}
}

func TestLoginFailuresLockAndCorrectPasswordResets(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	if err := svc.Bootstrap(ctx, strings.Repeat("b", 40), "admin-user", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := svc.Login(ctx, "admin-user", "wrong password"); err == nil {
			t.Fatal("wrong password accepted")
		}
	}
	if _, err := svc.Login(ctx, "admin-user", "correct horse battery"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("login was not locked: %v", err)
	}
	svc.now = func() time.Time { return time.Now().Add(loginLockDuration + time.Second) }
	if _, err := svc.Login(ctx, "admin-user", "correct horse battery"); err != nil {
		t.Fatalf("lock did not expire: %v", err)
	}
	if _, err := svc.Login(ctx, "admin-user", "wrong password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("failure counter not reset: %v", err)
	}
}

func TestLoginFailureLockSharedAcrossServiceInstances(t *testing.T) {
	ctx := context.Background()
	one, store := newTestService(t)
	if err := one.Bootstrap(ctx, strings.Repeat("b", 40), "admin-user", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	two, err := New(store, []byte(strings.Repeat("k", 32)), "", true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		current := one
		if i%2 != 0 {
			current = two
		}
		if _, err := current.Login(ctx, "admin-user", "wrong password"); err == nil {
			t.Fatal("wrong password accepted")
		}
	}
	for _, current := range []*Service{one, two} {
		if _, err := current.Login(ctx, "admin-user", "correct horse battery"); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("persisted login lock missing: %v", err)
		}
	}
}

func TestAuthenticationStorageFailureIsNotInvalidCredentials(t *testing.T) {
	svc, store := newTestService(t)
	fault := errors.New("database disconnected")
	svc.store = &failingReadStore{Store: store, failOn: 2, err: fault}
	if _, err := svc.Authenticate(context.Background(), "token"); !errors.Is(err, fault) {
		t.Fatalf("storage failure reported as credentials error: %v", err)
	}
	svc.store = &failingReadStore{Store: store, failOn: 2, err: fault}
	if _, err := svc.Login(context.Background(), "admin-user", "correct horse battery"); !errors.Is(err, fault) {
		t.Fatalf("login storage failure reported as credentials error: %v", err)
	}
	svc.store = &failingReadStore{Store: store, failOn: 1, err: fault}
	if _, err := svc.Initialized(context.Background()); !errors.Is(err, fault) {
		t.Fatalf("initialization storage failure hidden: %v", err)
	}
}

type failingReadStore struct {
	storage.Store
	failOn int
	calls  int
	err    error
}

func (s *failingReadStore) View(ctx context.Context, fn func(storage.Tx) error) error {
	s.calls++
	if s.calls == s.failOn {
		return s.err
	}
	return s.Store.View(ctx, fn)
}
func TestSecretCiphertextAuthenticatesAndUsesRandomNonce(t *testing.T) {
	svc, _ := newTestService(t)
	one, err := svc.Encrypt("upstream secret")
	if err != nil {
		t.Fatal(err)
	}
	two, err := svc.Encrypt("upstream secret")
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("nonce reused")
	}
	plain, err := svc.Decrypt(one)
	if err != nil || plain != "upstream secret" {
		t.Fatalf("decrypt %q %v", plain, err)
	}
	payload := []byte(one)
	payload[len(payload)-1] ^= 1
	if _, err := svc.Decrypt(string(payload)); err == nil {
		t.Fatal("tampered ciphertext authenticated")
	}
}

func TestSecretContextCannotBeMovedOrCollide(t *testing.T) {
	svc, _ := newTestService(t)
	ciphertext, err := svc.EncryptFor("service:credential", "one", "private-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"service", "credential:one"}, {"service:credential", "two"}, {"probe", "one"}} {
		if _, err := svc.DecryptFor(pair[0], pair[1], ciphertext); err == nil {
			t.Fatalf("ciphertext moved into %q/%q", pair[0], pair[1])
		}
	}
	plain, err := svc.DecryptFor("service:credential", "one", ciphertext)
	if err != nil || plain != "private-token" {
		t.Fatalf("original context rejected: %q %v", plain, err)
	}
}
func TestRelayIdentityIsStableAndDeviceListOmitsToken(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	a, err := svc.RelayID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.RelayID(ctx)
	if err != nil || a != b {
		t.Fatalf("identity %q %q %v", a, b, err)
	}
	if err := svc.Bootstrap(ctx, strings.Repeat("b", 40), "admin-user", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	pair, err := svc.CreatePairing(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	device, err := svc.Pair(ctx, pair.Code, "phone")
	if err != nil {
		t.Fatal(err)
	}
	devices, err := svc.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(devices)
	if strings.Contains(string(encoded), device.Token) {
		t.Fatal("device list exposed raw token")
	}
}
func TestPairingExpiry(t *testing.T) {
	svc, _ := newTestService(t)
	p, err := svc.CreatePairing(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return p.ExpiresAt }
	if _, err := svc.Pair(context.Background(), p.Code, "device"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired pairing accepted: %v", err)
	}
}

func TestSessionAndDeviceExpirationBoundaries(t *testing.T) {
	ctx := context.Background()
	t.Run("session", func(t *testing.T) {
		svc, _ := newTestService(t)
		now := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)
		svc.now = func() time.Time { return now }
		if err := svc.Bootstrap(ctx, strings.Repeat("b", 40), "admin-user", "correct horse battery"); err != nil {
			t.Fatal(err)
		}
		token, err := svc.Login(ctx, "admin-user", "correct horse battery")
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(sessionTTL - time.Nanosecond)
		if _, err := svc.Authenticate(ctx, token); err != nil {
			t.Fatalf("session expired early: %v", err)
		}
		now = now.Add(time.Nanosecond)
		if _, err := svc.Authenticate(ctx, token); !errors.Is(err, ErrExpired) {
			t.Fatalf("expired session accepted: %v", err)
		}
	})
	t.Run("device", func(t *testing.T) {
		svc, _ := newTestService(t)
		now := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)
		svc.now = func() time.Time { return now }
		pair, err := svc.CreatePairing(ctx, "admin")
		if err != nil {
			t.Fatal(err)
		}
		paired, err := svc.Pair(ctx, pair.Code, "device")
		if err != nil {
			t.Fatal(err)
		}
		if !paired.ExpiresAt.Equal(now.Add(deviceTTL)) {
			t.Fatalf("wrong expiration %v", paired.ExpiresAt)
		}
		devices, err := svc.Devices(ctx)
		if err != nil || len(devices) != 1 || !devices[0].ExpiresAt.Equal(paired.ExpiresAt) {
			t.Fatalf("expiration missing from device list: %#v %v", devices, err)
		}
		now = paired.ExpiresAt.Add(-time.Nanosecond)
		if _, err := svc.Authenticate(ctx, paired.Token); err != nil {
			t.Fatalf("device expired early: %v", err)
		}
		now = paired.ExpiresAt
		if _, err := svc.Authenticate(ctx, paired.Token); !errors.Is(err, ErrExpired) {
			t.Fatalf("expired device accepted: %v", err)
		}
	})
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
