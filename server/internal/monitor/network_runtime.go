package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"relayservice/internal/storage"
)

func (e *Engine) RunNetwork(ctx context.Context) error {
	e.networkMu.Lock()
	if e.network != nil {
		e.networkMu.Unlock()
		return ErrConflict
	}
	runtime := &networkRuntime{workers: map[string]*networkWorker{}}
	e.network = runtime
	e.networkMu.Unlock()
	var wg sync.WaitGroup
	defer func() {
		runtime.mu.Lock()
		for _, w := range runtime.workers {
			w.cancel()
		}
		runtime.mu.Unlock()
		wg.Wait()
		e.networkMu.Lock()
		e.network = nil
		e.networkMu.Unlock()
	}()
	syncWorkers := func() error {
		type config struct {
			rec      serviceRecord
			settings networkSettingsRecord
		}
		configs := map[string]config{}
		err := e.store.View(ctx, func(tx storage.Tx) error {
			after := ""
			for {
				rows, err := tx.Scan(servicesBucket, storage.Scan{After: after, Limit: 200})
				if err != nil {
					return err
				}
				for _, row := range rows {
					after = row.Key
					var rec serviceRecord
					if err := json.Unmarshal(row.Value, &rec); err != nil {
						return err
					}
					if rec.Service.Tombstone || rec.Service.Kind != KindMihomo || !rec.Service.Enabled {
						continue
					}
					saved, err := readNetworkSettings(tx, rec)
					if err != nil {
						return err
					}
					if saved.Settings.Enabled {
						configs[rec.Service.ID] = config{rec, saved}
					}
				}
				if len(rows) < 200 {
					return nil
				}
			}
		})
		if err != nil {
			return err
		}
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		for id, w := range runtime.workers {
			cfg, ok := configs[id]
			if !ok || cfg.rec.Service.Generation != w.rec.Service.Generation || cfg.settings.Revision != w.revision {
				w.cancel()
				delete(runtime.workers, id)
			}
		}
		ids := make([]string, 0, len(configs))
		for id := range configs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if runtime.workers[id] != nil {
				continue
			}
			if len(runtime.workers) >= 32 {
				continue
			}
			cfg := configs[id]
			workerCtx, cancel := context.WithCancel(ctx)
			w := &networkWorker{historyGeneration: cfg.settings.HistoryGeneration, rec: cfg.rec, settings: cfg.settings.Settings, controlCipher: cfg.settings.ControlCipher, revision: cfg.settings.Revision, cancel: cancel, view: emptyNetworkView(id, cfg.rec.Service.Generation), logs: []NetworkLog{}, logState: "connecting", previous: map[string]NetworkConnection{}, lastNodeSamples: map[string]NetworkNodeSample{}, pendingNodes: map[string]NetworkNodeSample{}, pendingLogs: []NetworkLog{}}
			runtime.workers[id] = w
			wg.Add(1)
			go func() { defer wg.Done(); e.runNetworkWorker(workerCtx, w) }()
		}
		return nil
	}
	if err := syncWorkers(); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	prune := time.NewTicker(5 * time.Second)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := syncWorkers(); err != nil {
				return err
			}
		case <-prune.C:
			if err := e.pruneNetwork(ctx); err != nil {
				return err
			}
		}
	}
}
func (e *Engine) runNetworkWorker(ctx context.Context, w *networkWorker) {
	// Register both independent credentials before any upstream response can
	// reach a projection, including a reflection of the other source's token.
	for _, control := range []bool{false, true} {
		if control && w.settings.ControlBaseURL == "" {
			continue
		}
		_, _, _, closeClient, err := e.networkClient(ctx, w, control)
		if err != nil {
			w.mu.Lock()
			w.view.State = "unavailable"
			w.view.Message = "采集凭据无效，请重新保存连接设置。"
			w.mu.Unlock()
			return
		}
		closeClient()
	}
	var wg sync.WaitGroup
	for _, kind := range []string{"traffic", "memory", "logs"} {
		kind := kind
		wg.Add(1)
		go func() { defer wg.Done(); e.runNetworkStream(ctx, w, kind) }()
	}
	defer wg.Wait()
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = e.flushNetwork(flushCtx, w, true)
		cancel()
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastMeta := time.Time{}
	for {
		if ctx.Err() != nil {
			return
		}
		now := e.now()
		meta := lastMeta.IsZero() || now.Sub(lastMeta) >= time.Minute
		if meta {
			lastMeta = now
		}
		e.pollNetwork(ctx, w, meta)
		if err := e.flushNetwork(ctx, w, false); err != nil {
			w.mu.Lock()
			w.networkCollectorState("storage", "unavailable", e.now())
			w.view.Message = "历史保存失败；采集仍在继续。"
			w.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (e *Engine) pollNetwork(ctx context.Context, w *networkWorker, meta bool) {
	timeout := time.Duration(w.rec.Service.TimeoutSeconds) * time.Second
	if timeout < time.Second {
		timeout = 5 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fetch, token, closeClient, err := e.collector(requestCtx, w.rec)
	if err != nil {
		w.mu.Lock()
		w.view.State = "unavailable"
		w.view.Message = networkError(err)
		w.view.Coverage.SourceStates["connections"] = "unavailable"
		w.mu.Unlock()
		return
	}
	defer closeClient()
	body, err := fetch(requestCtx, "/connections")
	now := e.now()
	w.mu.Lock()
	if err != nil {
		w.view.Coverage.SourceStates["connections"] = "unavailable"
		w.view.Connections = []NetworkConnection{}
		w.view.State = "unavailable"
		w.view.Message = networkError(err)
	} else if err := w.applyConnections(body, *token, now); err != nil {
		w.view.State = "unavailable"
		w.view.Message = "连接数据格式不支持。"
		w.view.Coverage.SourceStates["connections"] = "invalid_response"
		w.view.Connections = []NetworkConnection{}
	} else {
		w.view.State = "available"
		w.view.Message = "后台正在持续采集，节点结果来自核心已有检测。"
		w.view.Coverage.SourceStates["connections"] = "available"
		w.view.CollectedAt = &now
		w.view.Coverage.LastSampleAt = &now
		if w.view.Coverage.StartedAt == nil {
			w.view.Coverage.StartedAt = &now
		}
	}
	w.mu.Unlock()
	if !meta {
		return
	}
	body, err = fetch(requestCtx, "/version")
	w.mu.Lock()
	if err == nil {
		var v struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(body, &v) == nil && networkLabel(v.Version) && !w.networkSensitive(v.Version) {
			w.view.Overview.CoreVersion = v.Version
			w.view.Coverage.SourceStates["version"] = "available"
		} else {
			w.view.Coverage.SourceStates["version"] = "invalid_response"
		}
	} else {
		w.view.Coverage.SourceStates["version"] = "unavailable"
	}
	w.mu.Unlock()
	// Inventory requests get their own bounded deadline; a failed live connection
	// response must not consume every metadata source's entire timeout budget.
	metaCtx, metaCancel := context.WithTimeout(ctx, timeout)
	defer metaCancel()
	mf, mt, mc, err := e.collector(metaCtx, w.rec)
	if err != nil {
		return
	}
	defer mc()
	providers, providerErr := mf(metaCtx, "/providers/proxies")
	proxies, proxyErr := mf(metaCtx, "/proxies")
	w.mu.Lock()
	w.applyInventory(providers, providerErr, proxies, proxyErr, *mt, e.now())
	w.mu.Unlock()
	e.pollNetworkProfiles(ctx, w)
}
func networkError(err error) string {
	var f *FetchError
	if errors.As(err, &f) {
		return f.Message
	}
	return "网络代理数据暂不可用。"
}
func (e *Engine) flushNetwork(ctx context.Context, w *networkWorker, force bool) error {
	w.mu.Lock()
	now := e.now()
	minute := now.Truncate(time.Minute)
	if w.lastMinute.IsZero() {
		w.lastMinute = minute
	}
	if !force && minute.Equal(w.lastMinute) {
		w.mu.Unlock()
		return nil
	}
	usage := w.pendingUsage
	usage.At = w.lastMinute
	nodes := w.pendingNodes
	logs := append([]NetworkLog{}, w.pendingLogs...)
	snapshot := networkCopyView(w.view)
	snapshot.SettingsRevision = w.revision
	snapshot.Connections = []NetworkConnection{}
	snapshot.Logs = []NetworkLog{}
	snapshot.Settings = NetworkSettings{}
	snapshot.Settings.ImportedSubscriptions = nil
	err := e.store.Update(ctx, func(tx storage.Tx) error {
		rec, err := networkRecord(tx, w.rec.Service.ID)
		if err != nil {
			return err
		}
		if rec.Service.Generation != w.rec.Service.Generation {
			return ErrConflict
		}
		settings, err := readNetworkSettings(tx, rec)
		if err != nil {
			return err
		}
		if settings.Revision != w.revision {
			return ErrConflict
		}
		if err := tx.Put(networkSettingsBucket, rec.Service.ID, settings); err != nil {
			return err
		}
		if err := tx.Put(networkSnapshotBucket, rec.Service.ID, snapshot); err != nil {
			return err
		}
		if usage.Samples > 0 {
			key := networkHourKey(rec.Service.ID, w.historyGeneration, usage.At)
			var hour networkHourUsage
			err := tx.Get(networkUsageBucket, key, &hour)
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			found := false
			for i := range hour.Items {
				if hour.Items[i].At.Equal(usage.At) {
					hour.Items[i].UploadBytes = networkSafeAdd(hour.Items[i].UploadBytes, usage.UploadBytes)
					hour.Items[i].DownloadBytes = networkSafeAdd(hour.Items[i].DownloadBytes, usage.DownloadBytes)
					hour.Items[i].Samples += usage.Samples
					hour.Items[i].CounterReset = hour.Items[i].CounterReset || usage.CounterReset
					found = true
					break
				}
			}
			if !found {
				hour.Items = append(hour.Items, usage)
			}
			if len(hour.Items) > 60 {
				hour.Items = hour.Items[len(hour.Items)-60:]
			}
			if err := tx.Put(networkUsageBucket, key, hour); err != nil {
				return err
			}
			if err := networkPutExpiry(tx, networkUsageBucket, key, networkHourExpires(usage.At)); err != nil {
				return err
			}
		}
		for id, n := range nodes {
			key := networkHourKey(rec.Service.ID, w.historyGeneration, n.At) + "/" + id
			var hour networkHourNodes
			err := tx.Get(networkNodeBucket, key, &hour)
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			if len(hour.Items) == 0 || hour.Items[len(hour.Items)-1].CheckedAt == nil || n.CheckedAt == nil || !hour.Items[len(hour.Items)-1].CheckedAt.Equal(*n.CheckedAt) {
				hour.Items = append(hour.Items, n)
			}
			if len(hour.Items) > 60 {
				hour.Items = hour.Items[len(hour.Items)-60:]
			}
			if err := tx.Put(networkNodeBucket, key, hour); err != nil {
				return err
			}
			if err := networkPutExpiry(tx, networkNodeBucket, key, networkHourExpires(n.At)); err != nil {
				return err
			}
		}
		if settings.Settings.PersistLogs {
			for _, l := range logs {
				key := networkLogKey(rec.Service.ID, w.historyGeneration, l)
				if err := tx.Put(networkLogsBucket, key, l); err != nil {
					return err
				}
				if err := networkPutLogExpiry(tx, rec.Service.ID, w.historyGeneration, l.At, settings.Settings.LogRetentionHours); err != nil {
					return err
				}
			}
			// A bounded number of records and an explicit byte ceiling enforce the
			// configured cap even when a busy upstream emits many short events.
			prefix := networkScope(rec.Service.ID, w.historyGeneration)
			rows, err := boundedNetworkScan(tx, networkLogsBucket, storage.Scan{Prefix: prefix, Reverse: true}, 5000)
			if err != nil {
				return err
			}
			bytes := 0
			for _, row := range rows {
				bytes += len(row.Value)
				if bytes > settings.Settings.LogLimitMiB*(1<<20) {
					if err := tx.Delete(networkLogsBucket, row.Key); err != nil {
						return err
					}
				}
			}
			if len(rows) == 5000 {
				oldest := rows[len(rows)-1].Key
				for batch := 0; batch < 5; batch++ {
					extra, err := tx.Scan(networkLogsBucket, storage.Scan{Prefix: prefix, Limit: 200})
					if err != nil {
						return err
					}
					n := 0
					for _, row := range extra {
						if row.Key >= oldest {
							break
						}
						if err := tx.Delete(networkLogsBucket, row.Key); err != nil {
							return err
						}
						n++
					}
					if n < 200 {
						break
					}
				}
			}
		}
		return w.flushNetworkBreakdown(tx, rec.Service.ID, w.historyGeneration)
	})
	if err == nil {
		w.lastMinute = minute
		w.pendingUsage = NetworkUsage{}
		w.pendingNodes = map[string]NetworkNodeSample{}
		w.pendingLogs = nil
		w.pendingBreakdown = nil
		if w.view.Coverage.SourceStates["storage"] == "unavailable" {
			w.networkCollectorState("storage", "available", now)
		}
	}
	w.mu.Unlock()
	return err
}
func (w *networkWorker) applyConnections(body []byte, token string, now time.Time) error {
	var input struct {
		Connections []struct {
			ID       string `json:"id"`
			Metadata struct {
				Network       string `json:"network"`
				SourceIP      string `json:"sourceIP"`
				DestinationIP string `json:"destinationIP"`
				Host          string `json:"host"`
				Process       string `json:"process"`
			} `json:"metadata"`
			Upload   int64     `json:"upload"`
			Download int64     `json:"download"`
			Start    time.Time `json:"start"`
			Chains   []string  `json:"chains"`
			Rule     string    `json:"rule"`
		} `json:"connections"`
	}
	if json.Unmarshal(body, &input) != nil || input.Connections == nil {
		return ErrValidation
	}
	out := []NetworkConnection{}
	previous := map[string]NetworkConnection{}
	seen := map[string]bool{}
	w.view.Overview.ConnectionCount = len(input.Connections)
	if len(input.Connections) > networkItemLimit {
		w.view.Coverage.Truncated = true
	}
	for i, c := range input.Connections {
		if i >= networkItemLimit {
			break
		}
		if c.ID == "" || len(c.ID) > 256 || c.Upload < 0 || c.Download < 0 {
			continue
		}
		if seen[c.ID] {
			w.view.Coverage.Truncated = true
			continue
		}
		seen[c.ID] = true
		safe := NetworkConnection{ID: networkID(c.ID), Network: networkText(c.Metadata.Network, 32, token), Source: networkText(c.Metadata.SourceIP, 128, token), Destination: networkText(c.Metadata.DestinationIP, 128, token), Host: networkText(c.Metadata.Host, 256, token), Rule: networkText(c.Rule, 256, token), Process: networkText(c.Metadata.Process, 128, token), UploadBytes: c.Upload, DownloadBytes: c.Download, Chains: []string{}}
		for _, chain := range c.Chains {
			if len(safe.Chains) >= 32 {
				break
			}
			if value := networkText(chain, 256, token); value != "" {
				safe.Chains = append(safe.Chains, value)
			}
		}
		if !c.Start.IsZero() && c.Start.Before(now.Add(time.Minute)) {
			safe.StartedAt = &c.Start
		}
		if encoded, err := json.Marshal(safe); err != nil || w.networkSensitive(string(encoded)) {
			continue
		}
		if prior, ok := w.previous[safe.ID]; ok && w.view.CollectedAt != nil {
			seconds := now.Sub(*w.view.CollectedAt).Seconds()
			if seconds > 0 && seconds <= 10 && c.Upload >= prior.UploadBytes && c.Download >= prior.DownloadBytes {
				safe.UploadRate = int64(float64(c.Upload-prior.UploadBytes) / seconds)
				safe.DownloadRate = int64(float64(c.Download-prior.DownloadBytes) / seconds)
				if seconds <= 5 {
					w.recordNetworkBreakdown(safe, prior, now)
				}
			}
		}
		out = append(out, safe)
		previous[safe.ID] = safe
	}
	w.previous = previous
	w.view.Connections = out
	return nil
}
func networkText(s string, max int, token string) string {
	if len(s) > max || networkReflected(s, token) {
		return ""
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return ""
		}
	}
	return s
}
func networkReflected(value, token string) bool { return token != "" && strings.Contains(value, token) }
