package monitor

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"relayservice/internal/storage"
)

func networkAvailability(samples []NetworkNodeSample, now time.Time) NetworkAvailability {
	a := NetworkAvailability{StartAt: now.Add(-24 * time.Hour), EndAt: now, Intervals: make([]string, 96)}
	for i := range a.Intervals {
		a.Intervals[i] = "unknown"
	}
	seen := map[int64]bool{}
	for _, sample := range samples {
		if sample.CheckedAt == nil || sample.At.After(now) || sample.CheckedAt.Before(a.StartAt) || sample.CheckedAt.After(now) || sample.CheckedAt.After(sample.At) || sample.Freshness != "fresh" || (sample.State != "healthy" && sample.State != "unhealthy") {
			continue
		}
		key := sample.CheckedAt.UnixNano()
		if seen[key] {
			continue
		}
		seen[key] = true
		index := min(95, int(sample.CheckedAt.Sub(a.StartAt)/(15*time.Minute)))
		if sample.State == "healthy" {
			a.Success++
			if a.Intervals[index] == "unknown" {
				a.Intervals[index] = "healthy"
			}
		} else {
			a.Failure++
			a.Intervals[index] = "unhealthy"
		}
	}
	for _, state := range a.Intervals {
		if state != "unknown" {
			a.CoveredIntervals++
		}
	}
	if a.Success+a.Failure > 0 {
		rate := float64(a.Success) / float64(a.Success+a.Failure)
		a.SuccessRate = &rate
	}
	return a
}

// Only the visible page is enriched. Exact-key batches avoid one PostgreSQL
// round trip for every node/hour and never load an entire instance's history.
func (e *Engine) readNetworkAvailability(ctx context.Context, id string, generation int64, nodes []NetworkNode) error {
	if len(nodes) == 0 {
		return nil
	}
	now := e.now()
	keys := make([]string, 0, len(nodes)*25)
	samples := map[string][]NetworkNodeSample{}
	for _, n := range nodes {
		for at := now.Add(-24 * time.Hour).Truncate(time.Hour); !at.After(now); at = at.Add(time.Hour) {
			keys = append(keys, networkHourKey(id, generation, at)+"/"+n.ID)
		}
	}
	if err := e.store.View(ctx, func(tx storage.Tx) error {
		for start := 0; start < len(keys); start += 500 {
			rows, err := tx.Scan(networkNodeBucket, storage.Scan{Keys: keys[start:min(start+500, len(keys))], Limit: 500})
			if err != nil {
				return err
			}
			for _, row := range rows {
				var hour networkHourNodes
				if err := json.Unmarshal(row.Value, &hour); err != nil {
					return err
				}
				nodeID := row.Key[strings.LastIndex(row.Key, "/")+1:]
				samples[nodeID] = append(samples[nodeID], hour.Items...)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for i := range nodes {
		n := &nodes[i]
		// Include the current observation before the next history flush.
		s := append(samples[n.ID], NetworkNodeSample{At: now, State: n.State, Freshness: n.Freshness, CheckedAt: n.CheckedAt})
		a := networkAvailability(s, now)
		n.Availability24H = &a
	}
	return nil
}
