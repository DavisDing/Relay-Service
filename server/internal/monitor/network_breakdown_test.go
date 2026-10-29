package monitor

import (
	"fmt"
	"testing"
	"time"
)

func TestNetworkBreakdownOverflowPreservesTotals(t *testing.T) {
	items := map[string]NetworkUsageGroup{}
	for i := 0; i < 120; i++ {
		networkAddGroup(items, NetworkUsageGroup{Dimension: "client", Label: fmt.Sprint(i), UploadBytes: 1, DownloadBytes: 2, Samples: 1})
	}
	if len(items) != 101 {
		t.Fatalf("unbounded client cardinality: %d", len(items))
	}
	var up, down int64
	for _, item := range items {
		up += item.UploadBytes
		down += item.DownloadBytes
	}
	if up != 120 || down != 240 || items[networkGroupKey("client", networkOtherLabel)].Samples != 20 {
		t.Fatal("overflow discarded or duplicated deltas")
	}
}

func TestNetworkBreakdownDoesNotFillStreamGapsOrCounterResets(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	w := newNetworkTestWorker(now)
	prior := NetworkConnection{ID: "a", UploadBytes: 10, DownloadBytes: 20}
	current := NetworkConnection{ID: "a", UploadBytes: 15, DownloadBytes: 30, Rule: "Match", Source: "192.168.1.2", Chains: []string{"台湾1"}}
	w.recordNetworkBreakdown(current, prior, now.Add(6*time.Second))
	w.recordNetworkBreakdown(prior, current, now.Add(time.Second))
	if len(w.pendingBreakdown) != 0 {
		t.Fatal("gap or reset generated connection usage")
	}
	w.recordNetworkBreakdown(current, prior, now.Add(time.Second))
	groups := w.pendingBreakdown[now.Format("2006010215")]
	if len(groups) != 3 {
		t.Fatalf("missing independent dimensions: %+v", groups)
	}
	for _, group := range groups {
		if group.UploadBytes != 5 || group.DownloadBytes != 10 || group.Samples != 1 {
			t.Fatalf("wrong delta: %+v", group)
		}
	}
}
