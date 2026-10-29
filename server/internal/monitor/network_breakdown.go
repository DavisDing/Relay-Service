package monitor

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"relayservice/internal/storage"
)

const networkBreakdownBucket = "network.usage_groups"
const networkOtherLabel = "其他（合并超出上限的分组）"

type networkHourBreakdown struct {
	Items map[string]NetworkUsageGroup `json:"items"`
}

func networkGroupKey(dimension, label string) string {
	return dimension + "/" + networkID(label)
}

// Each hour has a bounded number of labels in each dimension. Overflow is
// still counted, but cannot create unbounded client/host cardinality on disk.
func networkAddGroup(items map[string]NetworkUsageGroup, group NetworkUsageGroup) {
	if group.Label == "" {
		group.Label = "来源未提供"
	}
	key := networkGroupKey(group.Dimension, group.Label)
	if _, exists := items[key]; !exists && group.Label != networkOtherLabel {
		count := 0
		for _, item := range items {
			if item.Dimension == group.Dimension && item.Label != networkOtherLabel {
				count++
			}
		}
		if count >= 100 {
			group.Label = networkOtherLabel
			key = networkGroupKey(group.Dimension, group.Label)
		}
	}
	current := items[key]
	current.Dimension = group.Dimension
	current.Label = group.Label
	current.UploadBytes = networkSafeAdd(current.UploadBytes, group.UploadBytes)
	current.DownloadBytes = networkSafeAdd(current.DownloadBytes, group.DownloadBytes)
	current.Samples += group.Samples
	items[key] = current
}

// Called under the worker lock after validating monotonic connection counters.
func (w *networkWorker) recordNetworkBreakdown(current, prior NetworkConnection, at time.Time) {
	if w.view.CollectedAt == nil || at.Sub(*w.view.CollectedAt) <= 0 || at.Sub(*w.view.CollectedAt) > 5*time.Second || current.UploadBytes < prior.UploadBytes || current.DownloadBytes < prior.DownloadBytes {
		return
	}
	if encoded, err := json.Marshal(current); err != nil || w.networkSensitive(string(encoded)) {
		return
	}
	up := current.UploadBytes - prior.UploadBytes
	down := current.DownloadBytes - prior.DownloadBytes
	if up == 0 && down == 0 {
		return
	}
	if w.pendingBreakdown == nil {
		w.pendingBreakdown = map[string]map[string]NetworkUsageGroup{}
	}
	hour := at.UTC().Truncate(time.Hour).Format("2006010215")
	if w.pendingBreakdown[hour] == nil {
		// A storage outage must not turn the pending queue into a memory leak.
		if len(w.pendingBreakdown) >= 2 {
			oldest := hour
			for key := range w.pendingBreakdown {
				if key < oldest {
					oldest = key
				}
			}
			delete(w.pendingBreakdown, oldest)
			w.view.Coverage.Truncated = true
			w.view.Coverage.Notes = networkNote(w.view.Coverage.Notes, "存储中断导致部分连接分组增量未保存。")
		}
		w.pendingBreakdown[hour] = map[string]NetworkUsageGroup{}
	}
	chain := strings.Join(current.Chains, " → ")
	if len(chain) > 1024 {
		chain = networkOtherLabel
	}
	for dimension, label := range map[string]string{"client": current.Source, "rule": current.Rule, "chain": chain} {
		networkAddGroup(w.pendingBreakdown[hour], NetworkUsageGroup{Dimension: dimension, Label: label, UploadBytes: up, DownloadBytes: down, Samples: 1})
	}
}

// Pending data is cleared by the caller only after the whole transaction commits.
func (w *networkWorker) flushNetworkBreakdown(tx storage.Tx, id string, generation int64) error {
	for hour, pending := range w.pendingBreakdown {
		at, err := time.Parse("2006010215", hour)
		if err != nil {
			return err
		}
		key := networkHourKey(id, generation, at)
		var stored networkHourBreakdown
		err = tx.Get(networkBreakdownBucket, key, &stored)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if stored.Items == nil {
			stored.Items = map[string]NetworkUsageGroup{}
		}
		// Stable admission order makes overflow deterministic across retries.
		keys := make([]string, 0, len(pending))
		for key := range pending {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			networkAddGroup(stored.Items, pending[key])
		}
		if err := tx.Put(networkBreakdownBucket, key, stored); err != nil {
			return err
		}
		if err := networkPutExpiry(tx, networkBreakdownBucket, key, networkHourExpires(at)); err != nil {
			return err
		}
	}
	return nil
}

func readNetworkBreakdown(tx storage.Tx, id string, generation int64, now time.Time, hours int, out *NetworkView) error {
	groups := map[string]NetworkUsageGroup{}
	start := now.Add(-time.Duration(hours) * time.Hour).Truncate(time.Hour)
	out.UsageGroupStart = &start
	out.UsageGroupEnd = &now
	for at := start; !at.After(now); at = at.Add(time.Hour) {
		var row networkHourBreakdown
		err := tx.Get(networkBreakdownBucket, networkHourKey(id, generation, at), &row)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		keys := make([]string, 0, len(row.Items))
		for key := range row.Items {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		// There are at most 169 hourly rows × 303 groups. Merge all retained
		// labels before ranking so an earlier hour cannot crowd out later ones.
		for _, key := range keys {
			group := row.Items[key]
			mergedKey := networkGroupKey(group.Dimension, group.Label)
			merged := groups[mergedKey]
			merged.Dimension = group.Dimension
			merged.Label = group.Label
			merged.UploadBytes = networkSafeAdd(merged.UploadBytes, group.UploadBytes)
			merged.DownloadBytes = networkSafeAdd(merged.DownloadBytes, group.DownloadBytes)
			merged.Samples += group.Samples
			groups[mergedKey] = merged
		}
	}
	out.UsageGroups = []NetworkUsageGroup{}
	for _, dimension := range []string{"client", "rule", "chain"} {
		items := []NetworkUsageGroup{}
		for _, group := range groups {
			if group.Dimension == dimension {
				items = append(items, group)
			}
		}
		sort.Slice(items, func(i, j int) bool {
			a := networkSafeAdd(items[i].UploadBytes, items[i].DownloadBytes)
			b := networkSafeAdd(items[j].UploadBytes, items[j].DownloadBytes)
			if a == b {
				return items[i].Label < items[j].Label
			}
			return a > b
		})
		other := NetworkUsageGroup{Dimension: dimension, Label: networkOtherLabel}
		count := 0
		for _, group := range items {
			if count < 20 && group.Label != networkOtherLabel {
				out.UsageGroups = append(out.UsageGroups, group)
				count++
			} else {
				other.UploadBytes = networkSafeAdd(other.UploadBytes, group.UploadBytes)
				other.DownloadBytes = networkSafeAdd(other.DownloadBytes, group.DownloadBytes)
				other.Samples += group.Samples
			}
		}
		if other.Samples > 0 {
			out.UsageGroups = append(out.UsageGroups, other)
		}
	}
	out.Coverage.Notes = networkNote(out.Coverage.Notes, "连接分组从所选起点所在整小时开始统计，可能多含不足一小时；每小时每类保留100个标签，超出合并其他。展示保留分组中用量最多20项，其余合并其他；不含首见、断流和未捕获短连接，不与核心总流量相加。")
	return nil
}
