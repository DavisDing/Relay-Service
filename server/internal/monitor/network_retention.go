package monitor

import (
	"context"
	"errors"
	"strings"
	"time"

	"relayservice/internal/storage"
)

const networkHistoryHours = 168
const networkRetentionBucket = "network.retention"

func networkHourExpires(at time.Time) time.Time {
	// Expire the whole hour only after its newest possible observation is 7d old.
	return at.UTC().Truncate(time.Hour).Add((networkHistoryHours + 1) * time.Hour)
}

// The expiry index is the fast path. This resumable sweep also covers old
// unindexed hours, including deleted services and previous generations. It
// reads at most 200 keys per bucket and never depends on an active worker.
func (e *Engine) pruneUnindexedNetworkHours(ctx context.Context) error {
	return e.store.Update(ctx, func(tx storage.Tx) error {
		for _, bucket := range []string{networkUsageBucket, networkNodeBucket, networkBreakdownBucket} {
			progressKey := "hour-sweep/" + bucket
			var after string
			if err := tx.Get(networkRetentionBucket, progressKey, &after); err != nil && !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			previous := after
			rows, err := tx.Scan(bucket, storage.Scan{After: after, Limit: retentionBatchSize})
			if err != nil {
				return err
			}
			if len(rows) == 0 && after == "" {
				continue
			}
			for _, row := range rows {
				after = row.Key
				parts := strings.Split(row.Key, "/")
				if len(parts) < 3 {
					continue
				}
				at, err := time.Parse("2006010215", parts[2])
				if err != nil || e.now().Before(networkHourExpires(at)) {
					continue
				}
				if err := tx.Delete(bucket, row.Key); err != nil {
					return err
				}
			}
			if len(rows) < retentionBatchSize {
				after = ""
			}
			if after != previous {
				if err := tx.Put(networkRetentionBucket, progressKey, after); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
