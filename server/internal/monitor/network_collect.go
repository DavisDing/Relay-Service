package monitor

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func (e *Engine) networkClient(ctx context.Context, w *networkWorker, control bool) (*http.Client, *url.URL, string, func(), error) {
	base := w.rec.Service.BaseURL
	cipher := w.rec.Cipher
	purpose := "monitor.service"
	if control {
		base = w.settings.ControlBaseURL
		cipher = w.controlCipher
		purpose = "network.control"
	}
	origin, err := validatedURL(base)
	if err != nil {
		return nil, nil, "", nil, ErrValidation
	}
	token := ""
	if cipher != "" {
		if e.crypt == nil {
			return nil, nil, "", nil, ErrValidation
		}
		if scoped, ok := e.crypt.(interface {
			DecryptFor(string, string, string) (string, error)
		}); ok {
			token, err = scoped.DecryptFor(purpose, w.rec.Service.ID, cipher)
		} else {
			token, err = e.crypt.Decrypt(cipher)
		}
		if err != nil || strings.ContainsAny(token, "\r\n") {
			return nil, nil, "", nil, ErrValidation
		}
	}
	if token != "" {
		w.mu.Lock()
		found := false
		for _, existing := range w.sensitive {
			if existing == token {
				found = true
			}
		}
		if !found {
			w.sensitive = append(w.sensitive, token)
		}
		w.mu.Unlock()
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return safeDial(ctx, network, address, w.rec.Service.AllowPrivate)
	}, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, ForceAttemptHTTP2: true}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, origin, token, transport.CloseIdleConnections, nil
}
func networkRequest(ctx context.Context, client *http.Client, origin *url.URL, token, path string) (*http.Response, error) {
	u := *origin
	u.Path = path
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrValidation
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Relay-Service/network-v1")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, &FetchError{StatusUnauthorized, "authentication_failed", "采集凭据或权限不足。"}
		}
		return nil, &FetchError{StatusUnsupported, "endpoint_unsupported", "当前安装未提供此采集接口，或接口暂不可用。"}
	}
	return resp, nil
}
func networkJSON(ctx context.Context, client *http.Client, origin *url.URL, token, path string, out any) error {
	resp, err := networkRequest(ctx, client, origin, token, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(body) > 1<<20 {
		return ErrValidation
	}
	return json.Unmarshal(body, out)
}
func (e *Engine) runNetworkStream(ctx context.Context, w *networkWorker, kind string) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		failureState := "disconnected"
		plannedRenewal := false
		client, origin, token, closeClient, err := e.networkClient(ctx, w, false)
		if err == nil {
			streamCtx, cancel := context.WithTimeout(ctx, 65*time.Second)
			resp, requestErr := networkRequest(streamCtx, client, origin, token, "/"+kind)
			if requestErr != nil {
				var typed *FetchError
				if errors.As(requestErr, &typed) && typed.Status == StatusUnauthorized {
					failureState = "unauthorized"
				}
			}
			if requestErr == nil {
				w.mu.Lock()
				if kind == "logs" {
					backoff = time.Second
					w.logConnectedAt = e.now()
					w.networkCollectorState(kind, "available", e.now())
				}
				w.mu.Unlock()
				reader := bufio.NewScanner(resp.Body)
				reader.Buffer(make([]byte, 4096), 64<<10)
				frames := 0
				for reader.Scan() {
					if ctx.Err() != nil {
						break
					}
					frames++
					now := e.now()
					if kind == "memory" && frames == 1 {
						continue
					}
					if err := w.applyStream(kind, reader.Bytes(), now); err != nil {
						break
					}
					backoff = time.Second
				}
				plannedRenewal = errors.Is(streamCtx.Err(), context.DeadlineExceeded)
				resp.Body.Close()
			}
			cancel()
			closeClient()
		}
		if ctx.Err() != nil {
			return
		}
		w.mu.Lock()
		if kind == "logs" && plannedRenewal {
			failureState = "renewing"
		}
		w.networkCollectorState(kind, failureState, e.now())
		if kind != "logs" {
			if kind == "traffic" {
				w.view.Overview.UploadRate = 0
				w.view.Overview.DownloadRate = 0
				w.lastCounters = nil
			}
		}
		w.mu.Unlock()
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
func (w *networkWorker) applyStream(kind string, body []byte, now time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch kind {
	case "traffic":
		var frame struct {
			Up        *int64 `json:"up"`
			Down      *int64 `json:"down"`
			UpTotal   *int64 `json:"upTotal"`
			DownTotal *int64 `json:"downTotal"`
		}
		if json.Unmarshal(body, &frame) != nil || frame.Up == nil || frame.Down == nil || *frame.Up < 0 || *frame.Down < 0 {
			return ErrValidation
		}
		w.view.Overview.UploadRate = *frame.Up
		w.view.Overview.DownloadRate = *frame.Down
		w.view.Coverage.SourceStates[kind] = "available"
		if frame.UpTotal != nil && frame.DownTotal != nil && *frame.UpTotal >= 0 && *frame.DownTotal >= 0 {
			w.view.Overview.UploadTotal = *frame.UpTotal
			w.view.Overview.DownloadTotal = *frame.DownTotal
			if prev := w.lastCounters; prev != nil && now.Sub(prev.at) <= 5*time.Second {
				if *frame.UpTotal >= prev.up && *frame.DownTotal >= prev.down {
					w.pendingUsage.UploadBytes = networkSafeAdd(w.pendingUsage.UploadBytes, *frame.UpTotal-prev.up)
					w.pendingUsage.DownloadBytes = networkSafeAdd(w.pendingUsage.DownloadBytes, *frame.DownTotal-prev.down)
					w.pendingUsage.Samples++
				} else {
					w.pendingUsage.CounterReset = true
					w.view.Overview.CounterReset = true
				}
			}
			w.lastCounters = &networkCounters{*frame.UpTotal, *frame.DownTotal, now}
		} else {
			w.lastCounters = nil
			w.view.Coverage.SourceStates[kind] = "counter_unavailable"
		}
	case "memory":
		var frame struct {
			Inuse *uint64 `json:"inuse"`
		}
		if json.Unmarshal(body, &frame) != nil || frame.Inuse == nil {
			return ErrValidation
		}
		w.view.Overview.MemoryBytes = *frame.Inuse
		w.view.Coverage.SourceStates[kind] = "available"
	case "logs":
		var frame struct {
			Type    string `json:"type"`
			Payload string `json:"payload"`
			Level   string `json:"level"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &frame) != nil {
			return ErrValidation
		}
		level := strings.ToLower(frame.Type)
		if level == "" {
			level = strings.ToLower(frame.Level)
		}
		switch level {
		case "debug", "info", "warning", "warn", "error", "silent":
		default:
			level = "info"
		}
		raw := frame.Payload
		if raw == "" {
			raw = frame.Message
		}
		class := "core"
		lower := strings.ToLower(raw)
		switch {
		case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
			class = "timeout"
		case strings.Contains(lower, "connection refused"):
			class = "connection_refused"
		case strings.Contains(lower, "dns"):
			class = "dns"
		case strings.Contains(lower, "error") || level == "error":
			class = "error"
		}
		w.appendNetworkLog(now, level, class, sanitizeNetworkLog(raw, w.sensitive))
		w.logState = "available"
		w.logDisconnectedAt = time.Time{}
	}
	if w.sourceUpdated == nil {
		w.sourceUpdated = map[string]time.Time{}
	}
	w.sourceUpdated[kind] = now
	return nil
}

// Callers hold w.mu; event text never includes upstream messages or addresses.
func (w *networkWorker) networkCollectorState(source, state string, now time.Time) {
	if source == "logs" {
		switch state {
		case "disconnected", "renewing":
			if !w.logConnectedAt.IsZero() && now.Sub(w.logConnectedAt) >= 15*time.Second {
				w.logDisconnectedAt = time.Time{}
			}
			w.logConnectedAt = time.Time{}
			if w.logDisconnectedAt.IsZero() {
				w.logDisconnectedAt = now
			}
			if now.Sub(w.logDisconnectedAt) < 15*time.Second {
				if state == "renewing" {
					state = "connecting"
				} else {
					state = "reconnecting"
				}
			} else {
				state = "disconnected"
			}
		case "unauthorized":
			w.logDisconnectedAt = time.Time{}
			w.logConnectedAt = time.Time{}
		}
	}
	previous := w.view.Coverage.SourceStates[source]
	if source == "logs" {
		previous = w.logState
		w.logState = state
	}
	w.view.Coverage.SourceStates[source] = state
	if previous == state || state == "reconnecting" || state == "connecting" {
		return
	}
	name := map[string]string{"logs": "日志流", "traffic": "流量流", "memory": "内存流", "storage": "历史存储"}[source]
	if name == "" {
		return
	}
	level, message := "warning", name+"采集中断，Relay将继续重试。"
	if state == "available" {
		level, message = "info", name+"采集连接已建立。"
	} else if state == "unauthorized" {
		message = name + "采集凭据或权限不足。"
	} else if source == "storage" {
		message = "历史存储失败，Relay将继续重试。"
	}
	w.appendNetworkLog(now, level, "collector", message)
}

func (w *networkWorker) appendNetworkLog(now time.Time, level, kind, message string) {
	w.logSequence++
	l := NetworkLog{ID: networkID(now.String(), networkSequence(w.logSequence)), At: now, Level: level, Kind: kind, Message: message}
	w.logs = append(w.logs, l)
	if len(w.logs) > 1000 {
		w.logs = w.logs[len(w.logs)-1000:]
		w.view.Coverage.Truncated = true
	}
	if w.settings.PersistLogs {
		if len(w.pendingLogs) < 1000 {
			w.pendingLogs = append(w.pendingLogs, l)
		} else {
			w.view.Coverage.Truncated = true
		}
	}
}
func networkSequence(n uint64) string { return sequenceKey(n) }

func (w *networkWorker) applyInventory(providerBody []byte, providerErr error, proxyBody []byte, proxyErr error, token string, now time.Time) {
	subs := []NetworkSubscription{}
	evidence := &NetworkSubscriptionSources{CoreState: "unavailable"}
	nodes := []NetworkNode{}
	// /proxies keys are unique within this core. Providers describe membership,
	// never a second identity or an alternative source of current health.
	byName := map[string]int{}
	groupNames := map[string]bool{}
	catalogIDs := []string{}
	if proxyErr == nil {
		var input struct {
			Proxies map[string]mihomoRawProxy `json:"proxies"`
		}
		if json.Unmarshal(proxyBody, &input) != nil || input.Proxies == nil {
			proxyErr = ErrValidation
		} else {
			for _, name := range mihomoSortedKeys(input.Proxies) {
				raw := input.Proxies[name]
				raw.Name = name
				if mihomoNonNode(raw) {
					groupNames[name] = true
				}
				if n, ok := networkProjectNode(raw, "", token, now); ok {
					if len(nodes) >= networkItemLimit {
						w.view.Coverage.Truncated = true
						break
					}
					byName[name] = len(nodes)
					nodes = append(nodes, n)
					catalogIDs = append(catalogIDs, name, raw.Type, raw.ID)
				}
			}
		}
	}
	if providerErr == nil {
		var input struct {
			Providers map[string]mihomoRawProvider `json:"providers"`
		}
		if json.Unmarshal(providerBody, &input) != nil || input.Providers == nil {
			providerErr = ErrValidation
		} else {
			evidence.ProviderEntries = len(input.Providers)
			evidence.CoreState = "available"
			for _, name := range mihomoSortedKeys(input.Providers) {
				if !networkLabel(name) || networkReflected(name, token) || w.networkSensitive(name) {
					continue
				}
				p := input.Providers[name]
				if !mihomoSubscriptionProvider(p) || p.VehicleType == "" && p.Info == nil && groupNames[name] {
					evidence.EmbeddedProviders++
					continue
				}
				id := networkID("provider", name)
				projected, err := mihomoProjectSubscription(name, p, now)
				if err != nil {
					continue
				}
				sub := NetworkSubscription{ID: id, Name: name, Source: "provider", Active: true, UploadBytes: projected.UploadBytes, DownloadBytes: projected.DownloadBytes, TotalBytes: projected.TotalBytes, ExpireSeconds: projected.ExpireSeconds, UsedBytes: projected.UsedBytes, RemainingBytes: projected.RemainingBytes, UpdatedAt: projected.UpdatedAt, UsageState: projected.UsageState, NodeCountKnown: true}
				seen := map[string]bool{}
				for _, raw := range p.Proxies {
					if _, ok := networkProjectNode(raw, "", token, now); !ok || seen[raw.Name] {
						continue
					}
					if len(seen) >= networkItemLimit {
						sub.NodeCountKnown = false
						w.view.Coverage.Truncated = true
						break
					}
					seen[raw.Name] = true
					sub.NodeCount++
					if i, ok := byName[raw.Name]; ok && strings.EqualFold(nodes[i].Type, raw.Type) {
						nodes[i].Subscriptions = append(nodes[i].Subscriptions, NetworkNodeSubscription{ID: id, Name: name})
					}
				}
				subs = append(subs, sub)
				if len(subs) >= 100 {
					w.view.Coverage.Truncated = true
					break
				}
			}
		}
	}
	w.view.Coverage.SourceStates["providers"] = "available"
	if providerErr != nil {
		w.view.Coverage.SourceStates["providers"] = "unavailable"
	}
	w.view.Coverage.SourceStates["nodes"] = "available"
	if proxyErr != nil {
		w.view.Coverage.SourceStates["nodes"] = "unavailable"
	}
	for i := range nodes {
		if len(nodes[i].Subscriptions) > 0 {
			// Keep the singular fields for older readers; all memberships are
			// available to filtering and current clients.
			nodes[i].SubscriptionID = nodes[i].Subscriptions[0].ID
			nodes[i].SubscriptionName = nodes[i].Subscriptions[0].Name
		}
	}
	evidence.CoreSubscriptions = len(subs)
	if len(subs) == 0 && evidence.EmbeddedProviders > 0 {
		evidence.CoreState = "not_exposed"
	}
	w.view.Coverage.SubscriptionSources = evidence
	w.coreSubs = subs
	w.coreNodes = nodes
	w.coreCatalogStamp = networkID(catalogIDs...)
	w.rebuildNetworkInventory(now)
}
func (w *networkWorker) rebuildNetworkInventory(now time.Time) {
	subs := append([]NetworkSubscription{}, w.coreSubs...)
	subs = append(subs, w.profileSubs...)
	subs = append(subs, w.settings.ImportedSubscriptions...)
	// Within the explicitly configured control/core pair, only a single active
	// profile with a complete current directory may supply profile membership.
	// Names remain case-sensitive; protocol spelling differs between YAML and
	// the core API. The core row/ID/history remains the canonical observation.
	activeProfile := ""
	activeNodeCount := 0
	activeCount := 0
	profileNames := map[string]string{}
	for _, sub := range w.profileSubs {
		profileNames[sub.ID] = sub.Name
		if sub.Source == "control" && sub.Active {
			activeCount++
			if sub.NodeCountKnown {
				activeProfile = sub.ID
				activeNodeCount = sub.NodeCount
			}
		}
	}
	if activeCount != 1 || w.view.Coverage.SourceStates["profiles"] != "available" || w.profilesFetched.IsZero() || now.Before(w.profilesFetched) || now.Sub(w.profilesFetched) > 5*time.Minute || w.coreCatalogStamp != w.profilesCoreStamp {
		activeProfile = ""
	}
	nodes := make([]NetworkNode, 0, len(w.coreNodes)+len(w.profileNodes))
	coreIndex := map[string]int{}
	for _, core := range w.coreNodes {
		core.Subscriptions = append([]NetworkNodeSubscription{}, core.Subscriptions...)
		coreIndex[networkNodeMatchKey(core.Name, core.Type)] = len(nodes)
		nodes = append(nodes, core)
	}
	// Require the entire active directory to match. A partial name overlap is
	// insufficient evidence that this is the profile currently used by the core.
	matched := 0
	for _, directory := range w.profileNodes {
		if activeProfile != "" && directory.SubscriptionID == activeProfile {
			if _, ok := coreIndex[networkNodeMatchKey(directory.Name, directory.Type)]; !ok {
				activeProfile = ""
				break
			}
			matched++
		}
	}
	if matched == 0 || matched != activeNodeCount {
		activeProfile = ""
	}
	if len(w.profileSubs) > 0 {
		w.view.Coverage.SourceStates["profile_membership"] = "unknown"
		if activeProfile != "" {
			w.view.Coverage.SourceStates["profile_membership"] = "available"
		}
	} else {
		delete(w.view.Coverage.SourceStates, "profile_membership")
	}
	for _, profileNode := range w.profileNodes {
		if activeProfile != "" && profileNode.SubscriptionID == activeProfile {
			if index, ok := coreIndex[networkNodeMatchKey(profileNode.Name, profileNode.Type)]; ok {
				if !networkNodeHasSubscription(nodes[index], profileNode.SubscriptionID) {
					nodes[index].Subscriptions = append(nodes[index].Subscriptions, NetworkNodeSubscription{ID: profileNode.SubscriptionID, Name: profileNames[profileNode.SubscriptionID]})
				}
				if nodes[index].SubscriptionID == "" {
					nodes[index].SubscriptionID = profileNode.SubscriptionID
					nodes[index].SubscriptionName = profileNames[profileNode.SubscriptionID]
				}
				continue
			}
		}
		nodes = append(nodes, profileNode)
	}

	for _, sub := range w.settings.ImportedSubscriptions {
		nodes = append(nodes, sub.Nodes...)
	}
	out := []NetworkNode{}
	for _, n := range nodes {
		if !w.networkSensitive(n.Name) && !w.networkSensitive(n.Type) {
			if len(out) >= networkItemLimit {
				w.view.Coverage.Truncated = true
				break
			}
			out = append(out, n)
		}
	}
	safeSubs := []NetworkSubscription{}
	for _, sub := range subs {
		if !w.networkSensitive(sub.Name) {
			safeSubs = append(safeSubs, sub)
		}
	}
	subs = safeSubs
	w.view.Subscriptions = subs
	w.updateSubscriptionSources()
	w.view.Nodes = out
	liveIDs := map[string]bool{}
	for _, n := range out {
		liveIDs[n.ID] = true
	}
	for id := range w.lastNodeSamples {
		if !liveIDs[id] {
			delete(w.lastNodeSamples, id)
			delete(w.pendingNodes, id)
		}
	}
	for _, n := range out {
		sample := NetworkNodeSample{At: now, State: n.State, DelayMS: n.DelayMS, CheckedAt: n.CheckedAt, Freshness: n.Freshness}
		prior, ok := w.lastNodeSamples[n.ID]
		changed := !ok || prior.State != sample.State || prior.Freshness != sample.Freshness || prior.CheckedAt == nil && sample.CheckedAt != nil || prior.CheckedAt != nil && sample.CheckedAt == nil || prior.CheckedAt != nil && sample.CheckedAt != nil && !prior.CheckedAt.Equal(*sample.CheckedAt)
		if changed {
			w.pendingNodes[n.ID] = sample
			w.lastNodeSamples[n.ID] = sample
		}
	}
	sort.Slice(w.view.Nodes, func(i, j int) bool { return w.view.Nodes[i].Name < w.view.Nodes[j].Name })
}

func networkNodeMatchKey(name, kind string) string {
	kind = strings.ToLower(strings.ReplaceAll(kind, "-", ""))
	switch kind {
	case "ss":
		kind = "shadowsocks"
	case "ssr":
		kind = "shadowsocksr"
	case "socks":
		kind = "socks5"
	}
	return name + "\x00" + kind
}

func networkNodeHasSubscription(node NetworkNode, id string) bool {
	if node.SubscriptionID == id {
		return true
	}
	for _, sub := range node.Subscriptions {
		if sub.ID == id {
			return true
		}
	}
	return false
}

func networkProjectNode(raw mihomoRawProxy, subscription, token string, now time.Time) (NetworkNode, bool) {
	if mihomoNonNode(raw) || !networkLabel(raw.Name) || !networkLabel(raw.Type) || networkReflected(raw.Name, token) || networkReflected(raw.Type, token) {
		return NetworkNode{}, false
	}
	n := mihomoProjectNode(raw, subscription, now)
	return NetworkNode{ID: networkID("core", n.Name), SubscriptionID: subscription, Name: n.Name, Type: n.Type, State: n.State, DelayMS: n.DelayMS, CheckedAt: n.CheckedAt, Freshness: n.Freshness, Loaded: true}, true
}

func (w *networkWorker) networkSensitive(value string) bool {
	for _, token := range w.sensitive {
		if networkReflected(value, token) {
			return true
		}
	}
	return false
}
