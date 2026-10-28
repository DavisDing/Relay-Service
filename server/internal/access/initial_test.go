package access

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"relayservice/internal/storage"
)

func TestInitialPasswordCannotBeReusedAndRevokesAllInitialSessions(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	defer store.Close()
	initial := strings.Repeat("a", 64)
	if pending, err := svc.EnsureInitialAdministrator(ctx, initial); !pending || err != nil {
		t.Fatalf("initial account pending=%v err=%v", pending, err)
	}
	first, err := svc.Login(ctx, "admin", initial)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Login(ctx, "admin", initial)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := svc.Authenticate(ctx, first); err != nil || !p.MustChangePassword {
		t.Fatalf("initial principal=%+v err=%v", p, err)
	}
	if err := svc.CompleteInitialPassword(ctx, first, "admin", initial); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("accepted unchanged initial password: %v", err)
	}
	if err := svc.CompleteInitialPassword(ctx, first, "owner", "permanent password for tests"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first, second} {
		if _, err := svc.Authenticate(ctx, token); !errors.Is(err, ErrExpired) {
			t.Fatalf("old session survived: %v", err)
		}
	}
	if _, err := svc.Login(ctx, "admin", initial); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("initial credential survived: %v", err)
	}
	if pending, err := svc.EnsureInitialAdministrator(ctx, strings.Repeat("b", 64)); pending || err != nil {
		t.Fatalf("restart reset account: pending=%v err=%v", pending, err)
	}
	token, err := svc.Login(ctx, "owner", "permanent password for tests")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := svc.Authenticate(ctx, token); err != nil || p.MustChangePassword {
		t.Fatalf("permanent principal=%+v err=%v", p, err)
	}
}

func TestInitialAccountPreservesLegacyAdministrator(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	defer store.Close()
	if err := svc.Bootstrap(ctx, strings.Repeat("b", 40), "existing", "existing administrator password"); err != nil {
		t.Fatal(err)
	}
	token, err := svc.Login(ctx, "existing", "existing administrator password")
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := svc.EnsureInitialAdministrator(ctx, strings.Repeat("a", 64)); pending || err != nil {
		t.Fatalf("legacy account modified: %v %v", pending, err)
	}
	if p, err := svc.Authenticate(ctx, token); err != nil || p.MustChangePassword {
		t.Fatalf("legacy session modified: %v %v", p, err)
	}
}

func TestMissingInitializedFlagNeverOverwritesAnAdministrator(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	defer store.Close()
	if err := svc.Bootstrap(ctx, strings.Repeat("b", 40), "existing", "existing administrator password"); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, func(tx storage.Tx) error { return tx.Delete(stateBucket, initializedKey) }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnsureInitialAdministrator(ctx, strings.Repeat("a", 64)); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("corrupt state reset administrator: %v", err)
	}
	if _, err := svc.Login(ctx, "existing", "existing administrator password"); err != nil {
		t.Fatalf("original administrator lost: %v", err)
	}
}

func TestConcurrentInitialPasswordCompletionHasOneWinner(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()
	defer store.Close()
	first, _ := New(store, make([]byte, 32), "", false)
	second, _ := New(store, make([]byte, 32), "", false)
	initial := strings.Repeat("c", 64)
	if _, err := first.EnsureInitialAdministrator(ctx, initial); err != nil {
		t.Fatal(err)
	}
	token, err := first.Login(ctx, "admin", initial)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, svc := range []*Service{first, second} {
		wg.Add(1)
		go func(svc *Service) {
			defer wg.Done()
			results <- svc.CompleteInitialPassword(ctx, token, "owner", "concurrent permanent password")
		}(svc)
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrExpired) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d", winners)
	}
}

func TestInitialRestartErrorsPreserveCredential(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	defer store.Close()
	initial := strings.Repeat("e", 64)
	if _, err := svc.EnsureInitialAdministrator(ctx, initial); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		password string
		want     error
	}{{"", ErrInitialPasswordMissing}, {strings.Repeat("f", 64), ErrInitialPasswordMismatch}} {
		if _, err := svc.EnsureInitialAdministrator(ctx, test.password); !errors.Is(err, test.want) || strings.Contains(err.Error(), initial) {
			t.Fatalf("restart error: %v", err)
		}
	}
	if _, err := svc.Login(ctx, "admin", initial); err != nil {
		t.Fatal("original credential overwritten:", err)
	}
}
