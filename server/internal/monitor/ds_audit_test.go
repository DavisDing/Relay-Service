package monitor

import (
	"context"
	"fmt"
	"testing"
)

func TestRecoveryCountTracksOnlyAnOpenIncident(t *testing.T) {
	e, s := testEngine(t)
	ctx := context.Background()
	sequence := []struct {
		status   string
		count    int
		incident bool
	}{
		{StatusHealthy, 0, false}, {StatusHealthy, 0, false},
		{StatusUnhealthy, 0, false}, {StatusUnhealthy, 0, true},
		{StatusHealthy, 1, true}, {StatusUnhealthy, 0, true},
		{StatusHealthy, 1, true}, {StatusHealthy, 0, false},
		{StatusHealthy, 0, false}, {StatusHealthy, 0, false},
		{StatusUnhealthy, 0, false}, {StatusUnhealthy, 0, true},
		{StatusHealthy, 1, true}, {StatusHealthy, 0, false},
	}
	for i, step := range sequence {
		complete(t, e, s, fmt.Sprint(i), step.status)
		got, err := e.Get(ctx, s.ID)
		if err != nil || got.RecoveryCount != step.count || got.OpenIncident != step.incident {
			t.Fatalf("step %d recovery=%d incident=%v want %+v err=%v", i, got.RecoveryCount, got.OpenIncident, step, err)
		}
	}
	events, err := e.Alerts(ctx, "", 100)
	if err != nil || len(events) != 4 {
		t.Fatalf("expected two failure/recovery pairs: %+v %v", events, err)
	}
}

func TestAlertsPageHasMoreUsesLookaheadAtStorageLimit(t *testing.T) {
	for _, count := range []int{0, 1, 2, 500, 501} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			e, _ := testEngine(t)
			cursors := make([]uint64, count)
			for i := range cursors {
				cursors[i] = uint64((i + 1) * 2)
			}
			seedAlertCursors(t, e, cursors...)
			for _, limit := range []int{1, 500} {
				items, more, err := e.AlertsPage(context.Background(), "", limit)
				if err != nil || len(items) != min(count, limit) || more != (count > limit) {
					t.Fatalf("count=%d limit=%d items=%d more=%v err=%v", count, limit, len(items), more, err)
				}
			}
		})
	}
}
