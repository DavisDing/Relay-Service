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
	if deadline, ok := t.Deadline(); ok && time.Until(deadline)-time.Second < lifetime {
		lifetime = time.Until(deadline) - time.Second
	}
	timer := time.AfterFunc(lifetime, func() { _ = srv.Close() })
	defer timer.Stop()
	t.Log("Synthetic initial-login fixture listening on", address)
	if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
