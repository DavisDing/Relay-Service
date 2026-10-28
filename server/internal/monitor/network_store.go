package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"relayservice/internal/storage"
)

func networkID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:16])
}
func networkDefaults(generation int64) NetworkSettings {
	return NetworkSettings{Enabled: true, LogRetentionHours: 24, LogLimitMiB: 20, Generation: generation, ImportedSubscriptions: []NetworkSubscription{}}
}
func networkRecord(tx storage.Tx, id string) (serviceRecord, error) {
	var rec serviceRecord
	err := tx.Get(servicesBucket, id, &rec)
	if err != nil {
		return rec, err
	}
	if rec.Service.Tombstone || rec.Service.Kind != KindMihomo {
		return rec, ErrNotFound
	}
	return rec, nil
}
func readNetworkSettings(tx storage.Tx, rec serviceRecord) (networkSettingsRecord, error) {
	var saved networkSettingsRecord
	err := tx.Get(networkSettingsBucket, rec.Service.ID, &saved)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return saved, err
	}
	if errors.Is(err, storage.ErrNotFound) || saved.Settings.Generation != rec.Service.Generation {
		return networkSettingsRecord{Settings: networkDefaults(rec.Service.Generation), HistoryGeneration: rec.Service.Generation}, nil
	}
	if saved.HistoryGeneration == 0 {
		saved.HistoryGeneration = rec.Service.Generation
	}
	return saved, err
}
func (e *Engine) NetworkSettings(ctx context.Context, id string) (NetworkSettings, error) {
	var out NetworkSettings
	err := e.store.View(ctx, func(tx storage.Tx) error {
		rec, err := networkRecord(tx, id)
		if err != nil {
			return err
		}
		r, err := readNetworkSettings(tx, rec)
		out = r.Settings
		out.HasControlToken = r.ControlCipher != ""
		return err
	})
	return out, err
}
func (e *Engine) UpdateNetworkSettings(ctx context.Context, id string, in NetworkSettings, actor string) (NetworkSettings, error) {
	var out NetworkSettings
	err := e.store.Update(ctx, func(tx storage.Tx) error {
		rec, err := networkRecord(tx, id)
		if err != nil {
			return err
		}
		saved, err := readNetworkSettings(tx, rec)
		if err != nil {
			return err
		}
		if in.Generation != rec.Service.Generation {
			return ErrConflict
		}
		if in.LogRetentionHours == 0 {
			in.LogRetentionHours = 24
		}
		if in.LogLimitMiB == 0 {
			in.LogLimitMiB = 20
		}
		if in.LogRetentionHours < 1 || in.LogRetentionHours > 168 || in.LogLimitMiB < 1 || in.LogLimitMiB > 100 || len(in.ControlToken) > 8192 || strings.ContainsAny(in.ControlToken, "\r\n") || in.ClearControlToken && in.ControlToken != "" {
			return ErrValidation
		}
		if in.ControlBaseURL != "" {
			u, err := validatedURL(in.ControlBaseURL)
			if err != nil || u.RawQuery != "" || u.Fragment != "" {
				return ErrValidation
			}
			in.ControlBaseURL = strings.TrimSuffix(u.String(), "/")
		}
		if saved.Settings.ControlBaseURL != in.ControlBaseURL {
			if saved.ControlCipher != "" && in.ControlToken == "" && !in.ClearControlToken {
				return ErrValidation
			}
			saved.ControlCipher = ""
			saved.HistoryGeneration = e.now().UnixNano()
		}
		if in.ClearControlToken {
			saved.ControlCipher = ""
		}
		if in.ControlToken != "" {
			if e.crypt == nil {
				return ErrValidation
			}
			if scoped, ok := e.crypt.(interface {
				EncryptFor(string, string, string) (string, error)
			}); ok {
				saved.ControlCipher, err = scoped.EncryptFor("network.control", id, in.ControlToken)
			} else {
				saved.ControlCipher, err = e.crypt.Encrypt(in.ControlToken)
			}
			if err != nil {
				return err
			}
		}
		in.ControlToken = ""
		in.ClearControlToken = false
		in.HasControlToken = saved.ControlCipher != ""
		in.ImportedSubscriptions = saved.Settings.ImportedSubscriptions
		saved.Settings = in
		saved.Revision++
		if err := tx.Put(networkSettingsBucket, id, saved); err != nil {
			return err
		}
		out = in
		return storage.AppendAudit(tx, storage.AuditRecord{ActorID: actor, TargetID: id, Action: "network.settings.update", At: e.now()})
	})
	return out, err
}
func (e *Engine) ImportNetworkSubscriptions(ctx context.Context, id string, in NetworkImport, actor string) (NetworkSettings, error) {
	var out NetworkSettings
	err := e.store.Update(ctx, func(tx storage.Tx) error {
		rec, err := networkRecord(tx, id)
		if err != nil {
			return err
		}
		if in.Generation != rec.Service.Generation {
			return ErrConflict
		}
		saved, err := readNetworkSettings(tx, rec)
		if err != nil {
			return err
		}
		if len(in.Subscriptions) > 100 {
			return ErrValidation
		}
		out = saved.Settings
		out.ImportedSubscriptions = []NetworkSubscription{}
		seen := map[string]bool{}
		total := 0
		for _, sub := range in.Subscriptions {
			if !networkLabel(sub.Name) || len(sub.Nodes) > networkItemLimit {
				return ErrValidation
			}
			if sub.ID == "" {
				sub.ID = networkID("import", sub.Name)
			}
			if len(sub.ID) > 128 || strings.ContainsAny(sub.ID, "/\\\x00") || seen[sub.ID] {
				return ErrValidation
			}
			seen[sub.ID] = true
			// Imported identifiers cannot collide with provider/control identities.
			sub.ID = "import-" + networkID(sub.ID)
			raw := mihomoRawSubscription{Upload: sub.UploadBytes, Download: sub.DownloadBytes, Total: sub.TotalBytes, Expire: sub.ExpireSeconds}
			projected, err := mihomoProjectSubscription(sub.Name, mihomoRawProvider{Info: &raw}, e.now())
			if err != nil {
				return ErrValidation
			}
			safe := NetworkSubscription{ID: sub.ID, Name: sub.Name, Source: "import", UploadBytes: projected.UploadBytes, DownloadBytes: projected.DownloadBytes, TotalBytes: projected.TotalBytes, ExpireSeconds: projected.ExpireSeconds, UsedBytes: projected.UsedBytes, RemainingBytes: projected.RemainingBytes, UsageState: projected.UsageState}
			now := e.now()
			safe.UpdatedAt = &now
			names := map[string]bool{}
			for _, node := range sub.Nodes {
				if !networkLabel(node.Name) || !networkLabel(node.Type) || names[node.Name] {
					return ErrValidation
				}
				names[node.Name] = true
				total++
				if total > networkItemLimit {
					return ErrValidation
				}
				safe.Nodes = append(safe.Nodes, NetworkNode{ID: networkID("import", sub.ID, node.Name), SubscriptionID: sub.ID, Name: node.Name, Type: node.Type, State: "unknown", Freshness: "missing", Loaded: false})
			}
			safe.NodeCount = len(safe.Nodes)
			safe.NodeCountKnown = true
			safe.NodeCountKnown = true
			out.ImportedSubscriptions = append(out.ImportedSubscriptions, safe)
		}
		saved.Settings = out
		saved.Revision++
		if err := tx.Put(networkSettingsBucket, id, saved); err != nil {
			return err
		}
		return storage.AppendAudit(tx, storage.AuditRecord{ActorID: actor, TargetID: id, Action: "network.subscriptions.import", At: e.now()})
	})
	return out, err
}

func networkLabel(value string) bool {
	return safeName(value) && !strings.Contains(value, "://") && !strings.ContainsAny(value, "\x00\r\n")
}
func networkCopyView(v NetworkView) NetworkView {
	b, _ := json.Marshal(v)
	var c NetworkView
	_ = json.Unmarshal(b, &c)
	return c
}
func emptyNetworkView(id string, generation int64) NetworkView {
	return NetworkView{ServiceID: id, Generation: generation, State: "waiting", Message: "等待后台采集。", Settings: networkDefaults(generation), Subscriptions: []NetworkSubscription{}, Nodes: []NetworkNode{}, Connections: []NetworkConnection{}, Logs: []NetworkLog{}, Usage: []NetworkUsage{}, NodeHistory: []NetworkNodeSample{}, Coverage: NetworkCoverage{SourceStates: map[string]string{}, Notes: []string{"节点结果来自已有检测；未加载的订阅节点没有当前检测证据。", "首次接入前的历史及采集中断均不补造。"}}}
}
func (e *Engine) NetworkView(ctx context.Context, id string, q NetworkQuery) (NetworkView, error) {
	var rec serviceRecord
	var saved networkSettingsRecord
	err := e.store.View(ctx, func(tx storage.Tx) error {
		var err error
		rec, err = networkRecord(tx, id)
		if err != nil {
			return err
		}
		saved, err = readNetworkSettings(tx, rec)
		return err
	})
	if err != nil {
		return NetworkView{}, err
	}
	if q.Section == "" {
		q.Section = "overview"
	}
	switch q.Section {
	case "overview", "usage", "subscriptions", "nodes", "connections", "logs":
	default:
		return NetworkView{}, ErrValidation
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	if q.Hours == 0 {
		q.Hours = 24
	}
	if q.Hours != 1 && q.Hours != 24 && q.Hours != 168 {
		return NetworkView{}, ErrValidation
	}
	if len(q.Q) > 256 || len(q.Node) > 128 || len(q.Subscription) > 128 {
		return NetworkView{}, ErrValidation
	}
	offset := 0
	if q.Cursor != "" {
		offset, err = strconv.Atoi(q.Cursor)
		if err != nil || offset < 0 || offset > 100000 {
			return NetworkView{}, ErrValidation
		}
	}
	out := emptyNetworkView(id, rec.Service.Generation)
	out.Settings = saved.Settings
	out.Settings.HasControlToken = saved.ControlCipher != ""
	var live *networkWorker
	e.networkMu.Lock()
	runtime := e.network
	e.networkMu.Unlock()
	if runtime != nil {
		runtime.mu.RLock()
		live = runtime.workers[id]
		runtime.mu.RUnlock()
	}
	if live != nil {
		live.mu.RLock()
		if live.rec.Service.Generation == rec.Service.Generation && live.revision == saved.Revision {
			out = networkCopyView(live.view)
			for _, source := range []string{"traffic", "memory"} {
				state := out.Coverage.SourceStates[source]
				if at, ok := live.sourceUpdated[source]; ok && e.now().Sub(at) > 5*time.Second && (state == "available" || state == "counter_unavailable") {
					out.Coverage.SourceStates[source] = "stale"
					if source == "traffic" {
						out.Overview.UploadRate = 0
						out.Overview.DownloadRate = 0
					}
				}
			}
			out.Settings = saved.Settings
			out.Settings.HasControlToken = saved.ControlCipher != ""
			if q.Section == "logs" {
				out.Logs = append([]NetworkLog{}, live.logs...)
				out.Coverage.SourceStates["logs"] = live.logState
			}
		}
		live.mu.RUnlock()
	}
	if live == nil && runtime != nil && rec.Service.Enabled && saved.Settings.Enabled {
		runtime.mu.RLock()
		atCapacity := len(runtime.workers) >= 32
		runtime.mu.RUnlock()
		if atCapacity {
			out.State = "capacity_limited"
			out.Message = "后台最多同时采集32个mihomo实例；此实例尚未采集。"
		}
	}
	if out.CollectedAt == nil {
		err = e.store.View(ctx, func(tx storage.Tx) error {
			var cached NetworkView
			getErr := tx.Get(networkSnapshotBucket, id, &cached)
			if getErr != nil && !errors.Is(getErr, storage.ErrNotFound) {
				return getErr
			}
			if getErr == nil && cached.Generation == rec.Service.Generation && cached.SettingsRevision == saved.Revision {
				out = cached
				out.Settings = saved.Settings
				out.State = "stale"
				out.Message = "后台尚未恢复；这是已保存的旧快照。"
				out.Connections = []NetworkConnection{}
				out.Logs = []NetworkLog{}
			}
			return nil
		})
	}
	if err != nil {
		return out, err
	}
	if !rec.Service.Enabled || !saved.Settings.Enabled {
		out.State = "paused"
		out.Message = "网络代理采集已暂停。"
		out.Connections = []NetworkConnection{}
		out.Logs = []NetworkLog{}
	}
	if out.CollectedAt != nil && e.now().Sub(*out.CollectedAt) > 30*time.Second && out.State == "available" {
		out.State = "stale"
		out.Message = "采集结果已过期。"
	}
	out.Overview.SubscriptionCount = len(out.Subscriptions)
	out.Overview.NodeCount = len(out.Nodes)
	out.Overview.HealthyNodes = 0
	out.Overview.UnhealthyNodes = 0
	out.Overview.UnknownNodes = 0
	for i := range out.Nodes {
		n := &out.Nodes[i]
		if n.CheckedAt == nil {
			n.State = "unknown"
			n.Freshness = "missing"
		} else if n.CheckedAt.After(e.now()) {
			n.State = "unknown"
			n.Freshness = "future"
		} else if e.now().Sub(*n.CheckedAt) > mihomoHistoryMaxAge {
			n.State = "unknown"
			n.Freshness = "stale"
		}
		if out.State != "available" {
			n.State = "unknown"
			n.Freshness = "stale"
		}
		for _, sub := range out.Subscriptions {
			if sub.ID == n.SubscriptionID {
				n.SubscriptionName = sub.Name
				break
			}
		}
		switch n.State {
		case "healthy":
			out.Overview.HealthyNodes++
		case "unhealthy":
			out.Overview.UnhealthyNodes++
		default:
			out.Overview.UnknownNodes++
		}
	}

	out.Usage = []NetworkUsage{}
	out.NodeHistory = []NetworkNodeSample{}
	if q.Section != "logs" {
		out.Logs = []NetworkLog{}
	}
	if q.Section != "connections" {
		out.Connections = []NetworkConnection{}
	}
	if q.Section == "usage" || q.Section == "nodes" && q.Node != "" || q.Section == "logs" && saved.Settings.PersistLogs {
		err = e.readNetworkHistory(ctx, id, saved.HistoryGeneration, q, &out)
		if err != nil {
			return out, err
		}
	}
	if q.Section == "usage" && len(out.Usage) > 180 {
		// Reserve one bucket for partial boundaries and round upward, keeping
		// the full requested window within the response budget.
		width := time.Duration((q.Hours*60+178)/179) * time.Minute
		grouped := []NetworkUsage{}
		for _, point := range out.Usage {
			at := point.At.Truncate(width)
			if len(grouped) == 0 || !grouped[len(grouped)-1].At.Equal(at) {
				point.At = at
				grouped = append(grouped, point)
			} else {
				last := &grouped[len(grouped)-1]
				last.UploadBytes = networkSafeAdd(last.UploadBytes, point.UploadBytes)
				last.DownloadBytes = networkSafeAdd(last.DownloadBytes, point.DownloadBytes)
				last.Samples += point.Samples
				last.CounterReset = last.CounterReset || point.CounterReset
			}
		}
		out.Usage = grouped
	}
	contains := func(value string) bool {
		return q.Q == "" || strings.Contains(strings.ToLower(value), strings.ToLower(q.Q))
	}
	switch q.Section {
	case "nodes":
		filtered := []NetworkNode{}
		for _, n := range out.Nodes {
			if contains(n.Name+" "+n.Type) && (q.Subscription == "" || n.SubscriptionID == q.Subscription) && (q.State == "" || n.State == q.State) {
				filtered = append(filtered, n)
			}
		}
		out.Nodes = filtered
		out.Total = len(filtered)
		out.Nodes = networkPage(filtered, offset, q.Limit)
	case "subscriptions":
		filtered := []NetworkSubscription{}
		for _, s := range out.Subscriptions {
			if contains(s.Name) {
				s.Nodes = nil
				filtered = append(filtered, s)
			}
		}
		out.Subscriptions = filtered
		out.Total = len(filtered)
		out.Subscriptions = networkPage(filtered, offset, q.Limit)
	case "connections":
		filtered := []NetworkConnection{}
		for _, c := range out.Connections {
			if contains(c.Host + " " + c.Source + " " + c.Destination + " " + c.Rule + " " + strings.Join(c.Chains, " ")) {
				filtered = append(filtered, c)
			}
		}
		out.Total = len(filtered)
		out.Connections = networkPage(filtered, offset, q.Limit)
	case "logs":
		filtered := []NetworkLog{}
		for i := len(out.Logs) - 1; i >= 0; i-- {
			l := out.Logs[i]
			if contains(l.Message+" "+l.Kind) && (q.State == "" || l.Level == q.State) {
				filtered = append(filtered, l)
			}
		}
		out.Total = len(filtered)
		out.Logs = networkPage(filtered, offset, q.Limit)
	case "usage":
		out.Total = len(out.Usage)
	}
	if q.Section != "nodes" {
		out.Nodes = []NetworkNode{}
	}
	if q.Section != "subscriptions" && q.Section != "nodes" {
		out.Subscriptions = []NetworkSubscription{}
	}
	for i := range out.Subscriptions {
		out.Subscriptions[i].Nodes = nil
	}
	if q.Section != "usage" && q.Section != "overview" && offset+q.Limit < out.Total {
		out.HasMore = true
		out.NextCursor = strconv.Itoa(offset + q.Limit)
	}
	out.Settings.ImportedSubscriptions = nil
	return out, nil
}
func networkPage[T any](items []T, offset, limit int) []T {
	if offset >= len(items) {
		return []T{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
func networkScope(id string, generation int64) string {
	return id + "/" + strconv.FormatInt(generation, 10) + "/"
}
func networkHourKey(id string, generation int64, at time.Time) string {
	return networkScope(id, generation) + at.UTC().Format("2006010215")
}
func networkPutExpiry(tx storage.Tx, bucket, key string, expires time.Time) error {
	expKey := expires.UTC().Format("20060102150405") + "/" + networkID(bucket, key)
	return tx.Put(networkExpiryBucket, expKey, networkExpiry{Bucket: bucket, Key: key})
}
func (e *Engine) readNetworkHistory(ctx context.Context, id string, generation int64, q NetworkQuery, out *NetworkView) error {
	return e.store.View(ctx, func(tx storage.Tx) error {
		now := e.now()
		hours := q.Hours

		start := now.Add(-time.Duration(hours) * time.Hour).Truncate(time.Hour)
		if q.Section == "usage" {
			for at := start; !at.After(now); at = at.Add(time.Hour) {
				var row networkHourUsage
				err := tx.Get(networkUsageBucket, networkHourKey(id, generation, at), &row)
				if err != nil && !errors.Is(err, storage.ErrNotFound) {
					return err
				}
				for _, u := range row.Items {
					if !u.At.Before(now.Add(-time.Duration(hours) * time.Hour)) {
						out.Usage = append(out.Usage, u)
					}
				}
			}
		}
		if q.Section == "usage" {
			if err := readNetworkBreakdown(tx, id, generation, now, hours, out); err != nil {
				return err
			}
		}
		if q.Section == "nodes" {
			for at := start; !at.After(now); at = at.Add(time.Hour) {
				var row networkHourNodes
				err := tx.Get(networkNodeBucket, networkHourKey(id, generation, at)+"/"+q.Node, &row)
				if err != nil && !errors.Is(err, storage.ErrNotFound) {
					return err
				}
				for _, n := range row.Items {
					if !n.At.Before(now.Add(-time.Duration(hours) * time.Hour)) {
						out.NodeHistory = append(out.NodeHistory, n)
					}
				}
			}
		}
		if q.Section == "logs" {
			liveLogs := append([]NetworkLog{}, out.Logs...)
			out.Logs = []NetworkLog{}
			prefix := networkScope(id, generation)
			rows, err := boundedNetworkScan(tx, networkLogsBucket, storage.Scan{Prefix: prefix, Reverse: true}, 1000)
			if err != nil {
				return err
			}
			cutoff := now.Add(-time.Duration(out.Settings.LogRetentionHours) * time.Hour)
			for i := len(rows) - 1; i >= 0; i-- {
				var l NetworkLog
				if json.Unmarshal(rows[i].Value, &l) == nil && !l.At.Before(cutoff) {
					out.Logs = append(out.Logs, l)
				}
			}
			seen := map[string]bool{}
			for _, l := range out.Logs {
				seen[l.ID] = true
			}
			for _, l := range liveLogs {
				if !seen[l.ID] && !l.At.Before(cutoff) {
					out.Logs = append(out.Logs, l)
				}
			}
			sort.Slice(out.Logs, func(i, j int) bool { return out.Logs[i].At.Before(out.Logs[j].At) })
			if len(rows) == 1000 {
				out.Coverage.Notes = append(out.Coverage.Notes, "日志搜索覆盖最近1000条保存记录；更早日志未载入。")
				out.Coverage.Truncated = true
			}
		}
		return nil
	})
}
func (e *Engine) pruneNetwork(ctx context.Context) error {
	for batch := 0; batch < 4; batch++ {
		removed := 0
		err := e.store.Update(ctx, func(tx storage.Tx) error {
			rows, err := tx.Scan(networkExpiryBucket, storage.Scan{Limit: 200})
			if err != nil {
				return err
			}
			cutoff := e.now().UTC().Format("20060102150405")
			for _, row := range rows {
				if len(row.Key) < 14 || row.Key[:14] > cutoff {
					break
				}
				var expiry networkExpiry
				if err := json.Unmarshal(row.Value, &expiry); err != nil {
					return err
				}
				if expiry.Prefix {
					children, err := tx.Scan(expiry.Bucket, storage.Scan{Prefix: expiry.Key, Limit: 200})
					if err != nil {
						return err
					}
					for _, child := range children {
						if err := tx.Delete(expiry.Bucket, child.Key); err != nil {
							return err
						}
					}
					if len(children) == 200 {
						removed += len(children)
						break
					}
				} else if err := tx.Delete(expiry.Bucket, expiry.Key); err != nil {
					return err
				}
				if err := tx.Delete(networkExpiryBucket, row.Key); err != nil {
					return err
				}
				removed++
			}
			return nil
		})
		if err != nil {
			return err
		}
		if removed < 200 {
			break
		}
	}
	return nil
}
func networkSafeAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
func networkLogKey(id string, generation int64, l NetworkLog) string {
	return networkScope(id, generation) + l.At.UTC().Format("2006010215") + "/" + fmt.Sprintf("%020d", l.At.UnixNano()) + "/" + l.ID
}

// rebindNetworkSettings participates in Save's transaction. Same-origin edits
// retain explicit collection/privacy preferences instead of silently re-enabling.
func rebindNetworkSettings(tx storage.Tx, old, new Service) error {
	if old.ID == "" {
		return nil
	}
	var saved networkSettingsRecord
	err := tx.Get(networkSettingsBucket, old.ID, &saved)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if saved.HistoryGeneration == 0 {
		saved.HistoryGeneration = old.Generation
	}
	saved.Settings.Generation = new.Generation
	saved.Revision++
	if old.Kind != new.Kind || old.BaseURL != new.BaseURL {
		saved.HistoryGeneration = new.Generation
		saved.Settings.Enabled = false
		saved.Settings.ControlBaseURL = ""
		saved.Settings.HasControlToken = false
		saved.ControlCipher = ""
		saved.Settings.ImportedSubscriptions = nil
	}
	return tx.Put(networkSettingsBucket, old.ID, saved)
}

func networkPutLogExpiry(tx storage.Tx, id string, generation int64, at time.Time, hours int) error {
	key := networkScope(id, generation) + at.UTC().Format("2006010215")
	return tx.Put(networkExpiryBucket, at.Truncate(time.Hour).Add(time.Duration(hours+1)*time.Hour).UTC().Format("20060102150405")+"/"+networkID(networkLogsBucket, key), networkExpiry{Bucket: networkLogsBucket, Key: key + "/", Prefix: true})
}

// Storage Scan deliberately caps each page at 500. Higher network limits must
// advance the exclusive cursor rather than silently reading only one page.
func boundedNetworkScan(tx storage.Tx, bucket string, scan storage.Scan, limit int) ([]storage.Record, error) {
	if limit < 1 || limit > 5000 {
		return nil, ErrValidation
	}
	out := make([]storage.Record, 0, limit)
	for len(out) < limit {
		scan.Limit = min(500, limit-len(out))
		rows, err := tx.Scan(bucket, scan)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(rows) < scan.Limit {
			break
		}
		scan.After = rows[len(rows)-1].Key
	}
	return out, nil
}
