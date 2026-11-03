package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"relayservice/internal/storage"
)

func TestNetworkStreamBlankLinesAndReadFailures(t *testing.T) {
	now := time.Now()
	w := newNetworkTestWorker(now)
	w.settings.PersistLogs = true
	input := " \n\r\n" + `{"type":"info","payload":"first"}` + "\n \t\n" + `{"type":"info","payload":"second"}` + "\n"
	n, err := w.readNetworkStream(context.Background(), "logs", strings.NewReader(input), func() time.Time { return now })
	if n != 2 || !errors.Is(err, io.EOF) || len(w.logs) != 2 || len(w.pendingLogs) != 2 {
		t.Fatalf("blank line disconnected/replayed stream: %d %v logs=%d", n, err, len(w.logs))
	}
	failure := errors.New("read failed")
	_, err = w.readNetworkStream(context.Background(), "logs", failingNetworkReader{failure}, time.Now)
	if !errors.Is(err, failure) {
		t.Fatal("scanner error lost", err)
	}
	_, err = w.readNetworkStream(context.Background(), "logs", strings.NewReader(strings.Repeat("x", 65<<10)), time.Now)
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatal("oversize frame treated as EOF")
	}
	_, err = w.readNetworkStream(context.Background(), "logs", strings.NewReader("{}\ninvalid\n"), time.Now)
	if !errors.Is(err, ErrValidation) {
		t.Fatal("invalid record not distinguished", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = w.readNetworkStream(ctx, "logs", strings.NewReader(""), time.Now); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
}

type failingNetworkReader struct{ err error }

func (r failingNetworkReader) Read([]byte) (int, error) { return 0, r.err }

func TestNetworkStreamRetryGrowthAndQuietRenewals(t *testing.T) {
	var backoff time.Duration
	for _, want := range []time.Duration{1, 2, 4, 8, 16, 30, 30} {
		backoff = networkStreamBackoff(backoff, false)
		if backoff != want*time.Second {
			t.Fatalf("backoff=%s want %ds", backoff, want)
		}
	}
	if networkStreamBackoff(backoff, true) != time.Second {
		t.Fatal("stable stream did not reset retry")
	}
	now := time.Now()
	w := newNetworkTestWorker(now)
	w.settings.PersistLogs = true
	for i := 0; i < 20; i++ {
		for _, source := range []string{"logs", "traffic", "memory"} {
			w.networkCollectorState(source, "available", now)
			w.networkCollectorState(source, "renewing", now)
		}
	}
	if len(w.logs) != 0 || len(w.pendingLogs) != 0 {
		t.Fatal("planned renewal generated collector logs", w.logs)
	}
	w.networkCollectorState("traffic", "disconnected", now)
	if len(w.logs) != 1 || w.logs[0].Level != "warning" {
		t.Fatal("real failure hidden")
	}
	w.networkCollectorState("traffic", "available", now)
	if len(w.logs) != 2 || w.logs[1].Level != "info" {
		t.Fatal("real recovery hidden")
	}
}

func TestNetworkSubscriptionUnknownClassificationAgrees(t *testing.T) {
	now := time.Now()
	for _, body := range []string{`{"providers":{}}`, `{"providers":{"default":{},"自动选择":{"proxies":[]}}}`, `{"providers":{"default":{"vehicleType":"Compatible"}}}`} {
		d, err := ParseMihomoProviders([]byte(body), now)
		if err != nil {
			t.Fatal(err)
		}
		if d.SubscriptionCount != 0 || d.SubscriptionState != "not_exposed" {
			t.Fatal("unproven airport", d)
		}
		for _, proxyFailure := range []bool{false, true} {
			w := newNetworkTestWorker(now)
			var proxyErr error
			if proxyFailure {
				proxyErr = errors.New("offline")
			}
			w.view.Coverage.SourceStates["profiles"] = "available"
			w.applyInventory([]byte(body), nil, []byte(`{"proxies":{"default":{"type":"Selector","all":[]},"自动选择":{"type":"URLTest","all":[]}}}`), proxyErr, "", now)
			e := w.view.Coverage.SubscriptionSources
			if len(w.view.Subscriptions) != 0 || e.State != "unavailable" || e.CoreState != d.SubscriptionState || !strings.Contains(e.Message, "不能解释为零订阅") {
				t.Fatalf("classification split: %+v", e)
			}
		}
	}
}

func TestNetworkDelayOverflowIsLocalUnknownEvidence(t *testing.T) {
	now := time.Now()
	provider := []byte(`{"providers":{"airport":{"vehicleType":"HTTP"}}}`)
	for _, delay := range []string{"65536", "-1", "1.5", `"slow"`, "999999999999999999999999"} {
		core := []byte(fmt.Sprintf(`{"proxies":{"bad":{"type":"AnyTLS","history":[{"time":%q,"delay":%s}]},"good":{"type":"AnyTLS","history":[{"time":%q,"delay":65535}]}}}`, now.Format(time.RFC3339Nano), delay, now.Format(time.RFC3339Nano)))
		w := newNetworkTestWorker(now)
		w.applyInventory(provider, nil, core, nil, "", now)
		if len(w.coreNodes) != 2 || w.view.Coverage.SourceStates["nodes"] != "available" {
			t.Fatal("single delay cleared directory", delay)
		}
		for _, n := range w.coreNodes {
			if n.Name == "bad" && (n.State != "unknown" || n.DelayMS != nil) {
				t.Fatal("overflow became health", n)
			}
			if n.Name == "good" && (n.State != "healthy" || n.DelayMS == nil || *n.DelayMS != 65535) {
				t.Fatal("valid boundary lost", n)
			}
		}
	}
}

func TestNetworkProfileListFailureBackoffAndRecovery(t *testing.T) {
	e, w, f := newProfileControlTest(t)
	now := time.Now()
	e.now = func() time.Time { return now }
	f.status = 503
	for i := 0; i < 30; i++ {
		e.pollNetworkProfiles(context.Background(), w)
		now = now.Add(2 * time.Second)
	}
	f.mu.Lock()
	reads := f.listReads
	f.status = 0
	f.metas = []networkProfileMeta{}
	f.mu.Unlock()
	if reads != 3 || w.profilesFetched != (time.Time{}) {
		t.Fatalf("unbounded retry / false success time: %d", reads)
	}
	now = w.profilesRetryAt
	e.pollNetworkProfiles(context.Background(), w)
	if w.profilesBackoff != 0 || !w.profilesRetryAt.IsZero() || !w.profilesFetched.Equal(now) {
		t.Fatal("successful read did not reset cooldown")
	}
}

func TestNetworkCopyViewDetachesMutableFields(t *testing.T) {
	now := time.Now()
	delay := uint16(2)
	quota := int64(5)
	rate := .5
	node := NetworkNode{DelayMS: &delay, CheckedAt: &now, Subscriptions: []NetworkNodeSubscription{{Name: "A"}}, Availability24H: &NetworkAvailability{SuccessRate: &rate, Intervals: []string{"healthy"}}}
	sub := NetworkSubscription{UploadBytes: &quota, DownloadBytes: &quota, TotalBytes: &quota, ExpireSeconds: &quota, UsedBytes: &quota, RemainingBytes: &quota, UpdatedAt: &now, Nodes: []NetworkNode{node}}
	v := emptyNetworkView("s", 1)
	v.CollectedAt = &now
	v.Nodes = []NetworkNode{node}
	v.Subscriptions = []NetworkSubscription{sub}
	v.Settings.ImportedSubscriptions = []NetworkSubscription{sub}
	v.Connections = []NetworkConnection{{Chains: []string{"A"}, StartedAt: &now}}
	v.NodeHistory = []NetworkNodeSample{{DelayMS: &delay, CheckedAt: &now}}
	v.Coverage.SourceStates["logs"] = "available"
	v.Coverage.StartedAt = &now
	v.Coverage.LastSampleAt = &now
	v.UsageGroupStart = &now
	v.UsageGroupEnd = &now
	c := networkCopyView(v)
	if !reflect.DeepEqual(v, c) {
		t.Fatal("copy changed values")
	}
	*c.Nodes[0].DelayMS = 9
	c.Nodes[0].Subscriptions[0].Name = "B"
	*c.Nodes[0].Availability24H.SuccessRate = 1
	c.Nodes[0].Availability24H.Intervals[0] = "failure"
	*c.Subscriptions[0].TotalBytes = 99
	c.Subscriptions[0].Nodes[0].Type = "B"
	*c.Settings.ImportedSubscriptions[0].UploadBytes = 99
	c.Connections[0].Chains[0] = "B"
	*c.NodeHistory[0].DelayMS = 99
	c.Coverage.SourceStates["logs"] = "B"
	c.Coverage.SubscriptionSources.State = "B"
	c.Coverage.Notes[0] = "B"
	*c.CollectedAt = now.Add(time.Hour)
	if *v.Nodes[0].DelayMS != 2 || v.Nodes[0].Subscriptions[0].Name != "A" || *v.Nodes[0].Availability24H.SuccessRate != .5 || v.Nodes[0].Availability24H.Intervals[0] != "healthy" || *v.Subscriptions[0].TotalBytes != 5 || *v.Settings.ImportedSubscriptions[0].UploadBytes != 5 || v.Connections[0].Chains[0] != "A" || *v.NodeHistory[0].DelayMS != 2 || v.Coverage.SourceStates["logs"] != "available" || v.Coverage.SubscriptionSources.State == "B" || v.Coverage.Notes[0] == "B" || !v.CollectedAt.Equal(now) {
		t.Fatal("copy shares mutable state")
	}
}

// Inject slow/failing persistence and assert the collector can acquire w.mu
// while the transaction is blocked. Retrying must neither drop nor double usage.
type blockedNetworkStore struct {
	storage.Store
	entered, release chan struct{}
	fail             bool
}

func (s *blockedNetworkStore) Update(ctx context.Context, fn func(storage.Tx) error) error {
	if s.entered != nil {
		close(s.entered)
		<-s.release
		s.entered = nil
	}
	if s.fail {
		return errors.New("storage down")
	}
	return s.Store.Update(ctx, fn)
}
func TestNetworkFlushReleasesCollectorLockAndRetriesBatch(t *testing.T) {
	base := storage.NewMemory()
	t.Cleanup(base.Close)
	now := time.Now()
	e, w := networkEngineFixture(t, base, now)
	blocked := &blockedNetworkStore{Store: base, entered: make(chan struct{}), release: make(chan struct{}), fail: true}
	e.store = blocked
	done := make(chan error, 1)
	go func() { done <- e.flushNetwork(context.Background(), w, true) }()
	<-blocked.entered
	acquired := make(chan struct{})
	go func() {
		w.mu.Lock()
		w.pendingUsage = NetworkUsage{UploadBytes: 7, Samples: 1}
		w.mu.Unlock()
		close(acquired)
	}()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		close(blocked.release)
		t.Fatal("database transaction held collector lock")
	}
	close(blocked.release)
	if <-done == nil {
		t.Fatal("expected storage failure")
	}
	blocked.fail = false
	if err := e.flushNetwork(context.Background(), w, true); err != nil {
		t.Fatal(err)
	}
	if err := base.View(context.Background(), func(tx storage.Tx) error {
		var hour networkHourUsage
		if err := tx.Get(networkUsageBucket, networkHourKey(w.rec.Service.ID, w.historyGeneration, now), &hour); err != nil {
			return err
		}
		var up int64
		var samples int
		for _, p := range hour.Items {
			up += p.UploadBytes
			samples += p.Samples
		}
		if up != 19 || samples != 2 {
			return fmt.Errorf("pending data lost/duplicated: %d %d", up, samples)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkNetworkCopyViewLarge(b *testing.B) {
	v := emptyNetworkView("s", 1)
	for i := 0; i < 5000; i++ {
		v.Nodes = append(v.Nodes, NetworkNode{ID: fmt.Sprint(i), Name: strings.Repeat("节点", 20), Subscriptions: []NetworkNodeSubscription{{ID: "a", Name: "A"}}})
	}
	raw, _ := json.Marshal(v)
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c := networkCopyView(v)
		if len(c.Nodes) != 5000 {
			b.Fatal("lost nodes")
		}
	}
}

func TestNetworkStreamHeadersAndShortReplayDoNotResetBackoff(t *testing.T) {
	for _, frame := range []string{"", `{"type":"info","payload":"replayed"}` + "\n"} {
		t.Run(fmt.Sprint(len(frame)), func(t *testing.T) {
			var requests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				rw.WriteHeader(200)
				fmt.Fprint(rw, frame)
			}))
			defer upstream.Close()
			store := storage.NewMemory()
			defer store.Close()
			e := New(store, nil)
			w := newNetworkTestWorker(time.Now())
			w.rec.Service.BaseURL = upstream.URL
			w.rec.Service.AllowPrivate = true
			ctx, cancel := context.WithTimeout(context.Background(), 4500*time.Millisecond)
			defer cancel()
			e.runNetworkStream(ctx, w, "logs")
			if n := requests.Load(); n < 2 || n > 3 {
				t.Fatalf("short 200 responses reset retry policy: %d requests in 4.5s", n)
			}
			if frame == "" && len(w.logs) != 0 {
				t.Fatal("headers/EOF generated fake connection events", w.logs)
			}
		})
	}
}

func TestMihomoLegacyClassificationUsesSameReadableCore(t *testing.T) {
	now := time.Now()
	providers := []byte(`{"providers":{"default":{"proxies":[]},"airport":{"proxies":[]}}}`)
	core := []byte(`{"proxies":{"default":{"type":"Selector","all":[]}}}`)
	for _, offline := range []bool{false, true} {
		var proxyErr error
		if offline {
			proxyErr = errors.New("offline")
		}
		d, err := CollectMihomo(context.Background(), func(ctx context.Context, path string) ([]byte, error) {
			if path == "/providers/proxies" {
				return providers, nil
			}
			return core, proxyErr
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		w := newNetworkTestWorker(now)
		w.applyInventory(providers, nil, core, proxyErr, "", now)
		if d.SubscriptionCount != len(w.coreSubs) || d.SubscriptionState != w.view.Coverage.SubscriptionSources.CoreState {
			t.Fatal("service/workspace classification differs", d, w.coreSubs)
		}
		if offline && d.SubscriptionCount != 0 {
			t.Fatal("missing core guessed airport")
		}
		if !offline && (d.SubscriptionCount != 1 || d.Subscriptions[0].Name != "airport") {
			t.Fatal("readable group classification wrong", d)
		}
	}
}
