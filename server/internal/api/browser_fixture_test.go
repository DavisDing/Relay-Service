package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"relayservice/internal/monitor"
	"relayservice/internal/storage"
)

// TestBrowserFixture is an opt-in, loopback-only UI fixture backed entirely by
// memory. It never opens PostgreSQL or probes a real service. See audit notes.
func TestBrowserFixture(t *testing.T) {
	address := os.Getenv("RELAY_UI_FIXTURE_ADDR")
	if address == "" {
		t.Skip("opt-in browser fixture")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("browser fixture must bind IPv4 loopback")
	}
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	statuses := []string{"healthy", "unhealthy", "unauthorized", "unsupported", "unknown", "queued", "running", "interrupted", "paused"}
	for i, status := range statuses {
		s, err := f.monitor.Save(ctx, "admin", monitor.ServiceInput{Name: "审计 " + status, Kind: "http", BaseURL: "http://127.0.0.1:9", Enabled: status != "paused", IntervalSeconds: 86400, TimeoutSeconds: 5})
		if err != nil {
			t.Fatal(err)
		}
		c := monitor.Check{ID: fmt.Sprint(i + 1), ServiceID: s.ID, Cursor: fmt.Sprintf("%020d", i+1), Generation: s.Generation, Status: status, CompletedAt: now, StartedAt: now.Add(-80 * time.Millisecond), DurationMS: 80, Message: "Fixture observation"}
		if err := f.store.Update(ctx, func(tx storage.Tx) error {
			if err := tx.Put("monitor.state", s.ID, map[string]any{"current": c, "last_check_at": now}); err != nil {
				return err
			}
			if err := tx.Put("monitor.history", s.ID+"/"+c.Cursor, c); err != nil {
				return err
			}
			if err := tx.Put("monitor.check_index", s.ID+"/"+c.ID, c.Cursor); err != nil {
				return err
			}
			return tx.Put("monitor.alerts", fmt.Sprintf("%020d", i+1), monitor.Alert{ID: fmt.Sprint(i + 1), Cursor: uint64(i + 1), ServiceID: s.ID, CheckID: c.ID, Kind: "failure", Message: "审计事件摘要", StartedAt: now})
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A few extra events exercise paging and prevent a later page from silently
	// acknowledging the first 100 unread observations.
	if err := f.store.Update(ctx, func(tx storage.Tx) error {
		for i := 10; i <= 105; i++ {
			if err := tx.Put("monitor.alerts", fmt.Sprintf("%020d", i), monitor.Alert{ID: fmt.Sprint(i), Cursor: uint64(i), ServiceID: "fixture", Kind: "failure", Message: "分页审计事件", StartedAt: now}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var servicesStatus, alertsStatus atomic.Int32
	handler := New(f.store, f.access, f.monitor, Options{AllowedOrigin: "http://" + address, Version: "browser-fixture"})
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/faults" && r.Method == "POST" {
			switch r.URL.Query().Get("target") {
			case "services":
				servicesStatus.Store(503)
			case "alerts":
				alertsStatus.Store(503)
			case "unauthorized":
				servicesStatus.Store(401)
			default:
				servicesStatus.Store(0)
				alertsStatus.Store(0)
			}
			w.WriteHeader(204)
			return
		}
		var status int32
		if r.URL.Path == "/api/v1/services" && r.Method == "GET" {
			status = servicesStatus.Load()
		}
		if r.URL.Path == "/api/v1/alerts" {
			status = alertsStatus.Load()
		}
		if status != 0 {
			writeError(w, int(status), "fixture_failure", "审计模拟接口失败")
			return
		}
		handler.ServeHTTP(w, r)
	})
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: wrapped, ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { _ = srv.Close() })
	t.Logf("Browser fixture: http://%s ; test login admin / this is a test password", address)
	timer := time.AfterFunc(20*time.Minute, func() { _ = srv.Close() })
	defer timer.Stop()
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
