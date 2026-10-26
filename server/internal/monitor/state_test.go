package monitor

import (
	"context"
	"errors"
	"net/netip"
	"relayservice/internal/storage"
	"testing"
	"time"
)

func TestPrivateConsentNeverAllowsMetadata(t *testing.T) {
	for _, ip := range []string{"0.0.0.0", "::", "169.254.169.254", "169.254.170.2", "100.100.100.200", "fd00:ec2::254", "fe80::1", "224.0.0.1", "ff02::1", "::ffff:169.254.169.254"} {
		for _, consent := range []bool{false, true} {
			if permittedIP(netip.MustParseAddr(ip), consent) {
				t.Errorf("permitted %s consent=%v", ip, consent)
			}
		}
	}
	for _, ip := range []string{"127.0.0.1", "::1", "10.0.0.1", "192.168.1.1", "172.16.0.1", "fd00::1", "100.64.0.2"} {
		if permittedIP(netip.MustParseAddr(ip), false) {
			t.Errorf("private accepted without consent: %s", ip)
		}
		if !permittedIP(netip.MustParseAddr(ip), true) {
			t.Errorf("private rejected with consent: %s", ip)
		}
	}
	if !permittedIP(netip.MustParseAddr("1.1.1.1"), false) {
		t.Error("public IP rejected")
	}
}

func TestConnectionOriginCannotContainInferencePathOrSecret(t *testing.T) {
	for _, raw := range []string{"http://a/v1/chat/completions", "http://user:pass@host", "http://host?token=x", "http://host#frag", "file:///etc/passwd", "http://host:0", "http://host:65536", "http://host:abc"} {
		if _, err := validatedURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func testEngine(t *testing.T) (*Engine, Service) {
	t.Helper()
	store := storage.NewMemory()
	t.Cleanup(store.Close)
	e := New(store, nil)
	e.now = func() time.Time { return time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC) }
	s, err := e.Save(context.Background(), "admin", ServiceInput{Name: "test", Kind: KindHTTP, BaseURL: "http://127.0.0.1:4321", AllowPrivate: true, Enabled: true, FailureThreshold: 2, RecoveryThreshold: 2})
	if err != nil {
		t.Fatal(err)
	}
	return e, s
}

func complete(t *testing.T, e *Engine, s Service, op, status string) Check {
	t.Helper()
	ctx := context.Background()
	queued, err := e.RequestCheck(ctx, s.ID, op, s.Generation)
	if err != nil {
		t.Fatal(err)
	}
	c, _, found, err := e.claim(ctx)
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	if c.ID != queued.ID {
		t.Fatal("wrong claim")
	}
	c.Status = status
	c.Success = status == StatusHealthy
	c.Class = "test"
	c.Message = "test"
	c.CompletedAt = e.now()
	if err := e.finish(ctx, c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFailureRecoveryThresholdsAndFreshness(t *testing.T) {
	e, s := testEngine(t)
	ctx := context.Background()
	complete(t, e, s, "1", StatusUnhealthy)
	got, _ := e.Get(ctx, s.ID)
	if got.OpenIncident {
		t.Fatal("incident before threshold")
	}
	complete(t, e, s, "2", StatusUnhealthy)
	got, _ = e.Get(ctx, s.ID)
	if !got.OpenIncident {
		t.Fatal("missing incident")
	}
	complete(t, e, s, "3", StatusHealthy)
	got, _ = e.Get(ctx, s.ID)
	if !got.OpenIncident {
		t.Fatal("recovered before threshold")
	}
	complete(t, e, s, "4", StatusHealthy)
	got, _ = e.Get(ctx, s.ID)
	if got.OpenIncident {
		t.Fatal("incident not recovered")
	}
	events, err := e.Alerts(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != "failure" || events[1].Kind != "recovery" || events[0].IncidentID != events[1].IncidentID {
		t.Fatalf("bad events %+v", events)
	}
	old := e.now()
	e.now = func() time.Time { return old.Add(3 * time.Minute) }
	got, err = e.Get(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUnknown || got.FreshnessState != "stale" || got.LastSuccess == nil {
		t.Fatalf("stale status %+v", got)
	}
}

func TestIdempotencyGenerationAndHistoryCursor(t *testing.T) {
	e, s := testEngine(t)
	ctx := context.Background()
	first := complete(t, e, s, "same", StatusHealthy)
	repeat, err := e.RequestCheck(ctx, s.ID, "same", s.Generation)
	if err != nil || repeat.ID != first.ID || repeat.Status != StatusHealthy {
		t.Fatalf("idempotent result %+v err=%v", repeat, err)
	}
	second := complete(t, e, s, "next", StatusHealthy)
	page, err := e.History(ctx, s.ID, "", 1)
	if err != nil || len(page) != 1 || page[0].ID != second.ID {
		t.Fatalf("page1 %+v err=%v", page, err)
	}
	page, err = e.History(ctx, s.ID, page[0].Cursor, 1)
	if err != nil || len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("page2 %+v err=%v", page, err)
	}
	queued, err := e.RequestCheck(ctx, s.ID, "old-generation", s.Generation)
	if err != nil {
		t.Fatal(err)
	}
	c, _, _, err := e.claim(ctx)
	if err != nil || c.ID != queued.ID {
		t.Fatal(err)
	}
	s, err = e.Save(ctx, "admin", ServiceInput{ID: s.ID, Generation: s.Generation, Name: s.Name, Kind: s.Kind, BaseURL: s.BaseURL, Enabled: true, AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RequestCheck(ctx, s.ID, "new-generation", s.Generation); !errors.Is(err, ErrConflict) {
		t.Fatalf("parallel generation check accepted: %v", err)
	}
	c.Status = StatusHealthy
	c.Success = true
	c.CompletedAt = e.now()
	if err := e.finish(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Get(ctx, s.ID)
	if got.Current != nil && got.Current.ID == c.ID {
		t.Fatal("old generation changed current state")
	}
}

func TestSilenceDoesNotEraseEventsOrAdvanceOtherDevice(t *testing.T) {
	e, s := testEngine(t)
	ctx := context.Background()
	complete(t, e, s, "1", StatusUnhealthy)
	complete(t, e, s, "2", StatusUnhealthy)
	events, err := e.Alerts(ctx, "", 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	if err := e.Silence(ctx, "admin", s.ID, e.now().Add(time.Hour), "maintenance"); err != nil {
		t.Fatal(err)
	}
	events, err = e.Alerts(ctx, "", 10)
	if err != nil || !events[0].Silenced {
		t.Fatalf("silenced events=%+v err=%v", events, err)
	}
	if err := e.Ack(ctx, "device-a", events[0].Cursor); err != nil {
		t.Fatal(err)
	}
	if cursor, err := e.AckCursor(ctx, "device-b"); err != nil || cursor != 0 {
		t.Fatalf("other device cursor=%d err=%v", cursor, err)
	}
	if err := e.Ack(ctx, "device-a", 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("ack rollback accepted %v", err)
	}
	old := e.now()
	e.now = func() time.Time { return old.Add(2 * time.Hour) }
	events, err = e.Alerts(ctx, "", 10)
	if err != nil || len(events) != 1 || events[0].Silenced {
		t.Fatalf("expired silence events=%+v err=%v", events, err)
	}
}

func TestRestartMarksRunningCheckInterrupted(t *testing.T) {
	e, s := testEngine(t)
	ctx := context.Background()
	q, err := e.RequestCheck(ctx, s.ID, "start", s.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := e.claim(ctx); err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	if err := e.recoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := e.Check(ctx, s.ID, q.ID)
	if err != nil || c.Status != StatusInterrupted {
		t.Fatalf("recovered check=%+v err=%v", c, err)
	}
	got, err := e.Get(ctx, s.ID)
	if err != nil || got.Current != nil || got.OpenIncident {
		t.Fatalf("interruption counted as target failure: %+v err=%v", got, err)
	}
}
