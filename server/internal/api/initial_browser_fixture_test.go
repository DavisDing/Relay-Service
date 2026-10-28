package api

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"relayservice/internal/access"
	"relayservice/internal/monitor"
	"relayservice/internal/storage"
)

// Opt-in first-login browser acceptance using synthetic data only. The fixture
// has no database, scheduler, external targets or production credentials.
func TestInitialLoginBrowserFixture(t *testing.T) {
	address := os.Getenv("RELAY_INITIAL_UI_FIXTURE_ADDR")
	if address == "" {
		t.Skip("opt-in first-login browser fixture")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("fixture must use IPv4 loopback")
	}
	if deadline, ok := t.Deadline(); ok && time.Until(deadline) <= time.Second {
		t.Skip("insufficient time for browser fixture initialization")
	}
	store := storage.NewMemory()
	defer store.Close()
	a, err := access.New(store, make([]byte, 32), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInitialAdministrator(context.Background(), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: New(store, a, monitor.New(store, a), Options{Version: "synthetic-first-login"}), ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { _ = srv.Close() })
	lifetime := 8 * time.Minute
	if deadline, ok := t.Deadline(); ok {
		var usable bool
		lifetime, usable = initialFixtureLifetime(time.Until(deadline))
		if !usable {
			t.Skip("insufficient time remaining for the opt-in browser fixture")
		}
	}
	timer := time.AfterFunc(lifetime, func() { _ = srv.Close() })
	defer timer.Stop()
	t.Log("Synthetic initial-login fixture listening on", address)
	if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}

// Leave proportional shutdown time even for a subsecond test deadline.
func initialFixtureLifetime(remaining time.Duration) (time.Duration, bool) {
	if remaining <= 0 {
		return 0, false
	}
	margin := remaining / 4
	if margin > time.Second {
		margin = time.Second
	}
	lifetime := remaining - margin
	if lifetime > 8*time.Minute {
		lifetime = 8 * time.Minute
	}
	return lifetime, lifetime > 0
}

func TestInitialFixtureLifetime(t *testing.T) {
	for _, remaining := range []time.Duration{-time.Second, 0, time.Nanosecond, 200 * time.Millisecond, time.Second, 10 * time.Minute} {
		lifetime, ok := initialFixtureLifetime(remaining)
		if remaining <= 0 {
			if ok || lifetime != 0 {
				t.Fatalf("expired deadline accepted: %v", remaining)
			}
		} else if !ok || lifetime <= 0 || lifetime > remaining || lifetime > 8*time.Minute {
			t.Fatalf("remaining=%v lifetime=%v ok=%v", remaining, lifetime, ok)
		}
	}
}
