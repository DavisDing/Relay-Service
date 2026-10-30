package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type networkProfileMeta struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	Type             string                 `json:"type"`
	UpdatedAt        int64                  `json:"updatedAt"`
	SubscriptionInfo *mihomoRawSubscription `json:"subscriptionInfo"`
	Active           bool                   `json:"active"`
}

func (e *Engine) pollNetworkProfiles(ctx context.Context, w *networkWorker) {
	if w.settings.ControlBaseURL == "" {
		w.mu.Lock()
		w.view.Coverage.SourceStates["profiles"] = "not_configured"
		w.view.Coverage.Notes = networkNote(w.view.Coverage.Notes, "配置文件订阅需要独立的MetaCubeXD控制API地址/CONTROL_TOKEN；核心secret不用于控制层。")
		w.mu.Unlock()
		return
	}
	client, origin, token, closeClient, err := e.networkClient(ctx, w, true)
	if err != nil {
		w.mu.Lock()
		w.view.Coverage.SourceStates["profiles"] = "credential_invalid"
		w.profileCollectionNote("配置文件订阅采集：", "控制服务地址或已保存的凭据无法读取；请核对地址并重新保存独立的控制令牌。")
		w.mu.Unlock()
		return
	}
	defer closeClient()
	w.mu.RLock()
	refresh := w.profilesFetched.IsZero() || e.now().Sub(w.profilesFetched) >= 5*time.Minute
	w.mu.RUnlock()
	if refresh {
		timeout, cancel := context.WithTimeout(ctx, 8*time.Second)
		var metas []networkProfileMeta
		err = networkJSON(timeout, client, origin, token, "/api/control/profiles", &metas)
		cancel()
		if err != nil {
			w.mu.Lock()
			state, note := networkProfileFailure(err, token)
			w.view.Coverage.SourceStates["profiles"] = state
			w.view.Coverage.Notes = networkNote(w.view.Coverage.Notes, "配置文件目录与核心节点缺少可验证身份映射，分别展示；未将同名节点健康借给订阅。")
			w.rebuildNetworkInventory(e.now())
			w.profileCollectionNote("配置文件订阅采集：", note)
			w.mu.Unlock()
			return
		}
		subs := []NetworkSubscription{}
		seen := map[string]bool{}
		for _, meta := range metas {
			if meta.Type != "local" && meta.Type != "remote" {
				continue
			}
			if !networkProfileID(meta.ID) || !networkLabel(meta.Name) || networkReflected(meta.Name, token) || networkReflected(meta.ID, token) {
				continue
			}
			if seen[meta.ID] {
				continue
			}
			seen[meta.ID] = true
			w.mu.RLock()
			reflected := w.networkSensitive(meta.ID) || w.networkSensitive(meta.Name)
			w.mu.RUnlock()
			if reflected {
				continue
			}
			projected, err := mihomoProjectSubscription(meta.Name, mihomoRawProvider{Info: meta.SubscriptionInfo}, e.now())
			if err != nil {
				continue
			}
			var updated *time.Time
			if meta.UpdatedAt > 0 && meta.UpdatedAt < 253402300799000 {
				v := time.UnixMilli(meta.UpdatedAt).UTC()
				updated = &v
			}
			sub := NetworkSubscription{ID: "profile-" + meta.ID, Name: meta.Name, Source: "control", Active: meta.Active, UploadBytes: projected.UploadBytes, DownloadBytes: projected.DownloadBytes, TotalBytes: projected.TotalBytes, ExpireSeconds: projected.ExpireSeconds, UsedBytes: projected.UsedBytes, RemainingBytes: projected.RemainingBytes, UpdatedAt: updated, UsageState: projected.UsageState}
			subs = append(subs, sub)
			if len(subs) >= 100 {
				break
			}
		}
		w.mu.Lock()
		previousSubs := map[string]NetworkSubscription{}
		for _, previous := range w.profileSubs {
			previousSubs[previous.ID] = previous
		}
		allowed := map[string]bool{}
		for i, sub := range subs {
			if prior, ok := previousSubs[sub.ID]; ok && sub.UpdatedAt != nil && prior.UpdatedAt != nil && sub.UpdatedAt.Equal(*prior.UpdatedAt) {
				allowed[sub.ID] = true
				subs[i].NodeCount = prior.NodeCount
				subs[i].NodeCountKnown = prior.NodeCountKnown
			}
		}
		keptNodes := []NetworkNode{}
		for _, node := range w.profileNodes {
			if allowed[node.SubscriptionID] {
				keptNodes = append(keptNodes, node)
			}
		}
		w.profileSubs = subs
		w.view.Overview.ActiveConfig = ""
		for _, sub := range subs {
			if sub.Active {
				w.view.Overview.ActiveConfig = sub.Name
			}
		}
		w.profileNodes = keptNodes
		// Keep unfinished detail requests ahead of a new sweep, independently of
		// list refreshes or upstream reordering. Large catalogs cannot delay new
		// subscription discovery or starve the later profiles of detail reads.
		queue := []string{}
		queued := map[string]bool{}
		current := map[string]bool{}
		for _, sub := range subs {
			current[sub.ID] = true
		}
		for _, id := range w.profileQueue[w.profileCursor:] {
			if current[id] {
				queue = append(queue, id)
				queued[id] = true
			}
		}
		for _, sub := range subs {
			if !queued[sub.ID] {
				queue = append(queue, sub.ID)
			}
		}
		w.profileQueue = queue
		w.profileCursor = 0
		w.profilesFetched = e.now()
		w.view.Coverage.SourceStates["profiles"] = "available"
		w.profileCollectionNote("配置文件订阅采集：", "")
		w.view.Coverage.SourceStates["profile_nodes"] = "collecting"
		w.profileCollectionNote("配置文件节点采集：", "")
		if len(subs) == 0 {
			w.view.Coverage.SourceStates["profile_nodes"] = "available"
		}
		if len(metas) > 100 {
			w.view.Coverage.Truncated = true
			w.view.Coverage.Notes = networkNote(w.view.Coverage.Notes, "配置文件清单超过100项，本轮未完整覆盖。")
		}
		w.view.Coverage.Notes = networkNote(w.view.Coverage.Notes, "配置文件目录与核心节点缺少可验证身份映射，分别展示；未将同名节点健康借给订阅。")
		w.rebuildNetworkInventory(e.now())
		w.mu.Unlock()
	}
	// Continue from the saved cursor next minute: never silently restrict all
	// subscriptions to the first handful of profiles on a large installation.
	for i := 0; i < 4; i++ {
		w.mu.RLock()
		cursor := w.profileCursor
		if cursor >= len(w.profileQueue) {
			w.mu.RUnlock()
			break
		}
		subIndex := 0
		for w.profileSubs[subIndex].ID != w.profileQueue[cursor] {
			subIndex++
		}
		sub := w.profileSubs[subIndex]
		w.mu.RUnlock()
		rawID := strings.TrimPrefix(sub.ID, "profile-")
		timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
		resp, err := networkRequest(timeout, client, origin, token, "/api/control/profiles/"+url.PathEscape(rawID))
		nodes := []NetworkNode{}
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
			resp.Body.Close()
			if readErr != nil || len(body) > 1<<20 {
				err = ErrValidation
			} else {
				var detail struct {
					Meta    networkProfileMeta `json:"meta"`
					Content string             `json:"content"`
				}
				if json.Unmarshal(body, &detail) != nil || detail.Meta.ID != rawID {
					err = ErrValidation
				} else {
					nodes, err = parseNetworkProfileNodes(detail.Content, sub.ID, token)
				}
			}
		}
		cancel()
		w.mu.Lock()
		if err == nil {
			kept := []NetworkNode{}
			for _, n := range w.profileNodes {
				if n.SubscriptionID != sub.ID {
					kept = append(kept, n)
				}
			}
			w.profileNodes = kept
			complete := true
			if len(w.profileNodes)+len(nodes) > networkItemLimit {
				complete = false
				nodes = nodes[:max(0, networkItemLimit-len(w.profileNodes))]
				w.view.Coverage.Truncated = true
				w.view.Coverage.SourceStates["profile_nodes"] = "partial"
			}
			w.profileNodes = append(w.profileNodes, nodes...)
			w.profileSubs[subIndex].NodeCount = len(nodes)
			w.profileSubs[subIndex].NodeCountKnown = complete
		} else {
			state, note := networkProfileFailure(err, token)
			if state != "unauthorized" {
				state = "partial"
			}
			w.view.Coverage.SourceStates["profile_nodes"] = state
			w.profileCollectionNote("配置文件节点采集：", note)
		}
		w.profileCursor++
		if w.profileCursor == len(w.profileQueue) && w.view.Coverage.SourceStates["profile_nodes"] == "collecting" {
			w.view.Coverage.SourceStates["profile_nodes"] = "available"
		}
		w.view.Coverage.Notes = networkNote(w.view.Coverage.Notes, "配置文件目录与核心节点缺少可验证身份映射，分别展示；未将同名节点健康借给订阅。")
		w.rebuildNetworkInventory(e.now())
		w.mu.Unlock()
	}
}

func networkProfileFailure(err error, token string) (string, string) {
	var failure *FetchError
	if errors.As(err, &failure) && failure.Status == StatusUnauthorized {
		if token == "" {
			return "unauthorized", "控制接口要求认证；请在后台采集设置中填写独立的 CONTROL_TOKEN 并保存，核心 secret 不能替代。"
		}
		return "unauthorized", "控制接口拒绝了已配置的令牌或权限；请核对独立的 CONTROL_TOKEN 及反向代理认证。"
	}
	if errors.Is(err, ErrValidation) {
		return "invalid_response", "响应超出限制或含不支持的配置结构；节点目录只读取顶层 proxies，provider 引用不会下载。"
	}
	return "unavailable", "控制接口暂不可读或响应格式不支持；请核对控制服务地址、接口支持情况与网络。"
}

// Replace the current diagnostic for a source so recovered authentication
// failures do not remain visible alongside successful collection.
func (w *networkWorker) profileCollectionNote(prefix, note string) {
	kept := w.view.Coverage.Notes[:0]
	for _, existing := range w.view.Coverage.Notes {
		if !strings.HasPrefix(existing, prefix) {
			kept = append(kept, existing)
		}
	}
	w.view.Coverage.Notes = kept
	if note != "" {
		w.view.Coverage.Notes = networkNote(kept, prefix+note)
	}
}

func networkProfileID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func networkNote(notes []string, note string) []string {
	for _, v := range notes {
		if v == note {
			return notes
		}
	}
	if len(notes) < 32 {
		return append(notes, note)
	}
	return notes
}
func parseNetworkProfileNodes(content, subscription, token string) ([]NetworkNode, error) {
	out := []NetworkNode{}
	if len(content) > 1<<20 {
		return out, ErrValidation
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return out, ErrValidation
	}
	count := 0
	var validate func(*yaml.Node, int) bool
	validate = func(n *yaml.Node, depth int) bool {
		count++
		if count > 50000 || depth > 32 || n.Kind == yaml.AliasNode || n.Tag == "!!merge" {
			return false
		}
		for _, child := range n.Content {
			if !validate(child, depth+1) {
				return false
			}
		}
		return true
	}
	if !validate(&root, 0) || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return out, ErrValidation
	}
	mapping := root.Content[0]
	var proxies *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "proxy-providers" {
			return out, ErrValidation
		}
		if mapping.Content[i].Value == "proxies" {
			if proxies != nil {
				return out, ErrValidation
			}
			proxies = mapping.Content[i+1]
		}
	}
	if proxies == nil {
		return out, nil
	}
	if proxies.Kind != yaml.SequenceNode {
		return out, ErrValidation
	}
	seen := map[string]bool{}
	for _, entry := range proxies.Content {
		if entry.Kind != yaml.MappingNode {
			return out, ErrValidation
		}
		name, kind := "", ""
		for i := 0; i+1 < len(entry.Content); i += 2 {
			key, value := entry.Content[i], entry.Content[i+1]
			if key.Kind != yaml.ScalarNode {
				return out, ErrValidation
			}
			if key.Value == "name" {
				if name != "" || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
					return out, ErrValidation
				}
				name = value.Value
			}
			if key.Value == "type" {
				if kind != "" || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
					return out, ErrValidation
				}
				kind = value.Value
			}
		}
		if !networkLabel(name) || !networkLabel(kind) || networkReflected(name, token) || networkReflected(kind, token) || seen[name] {
			return out, ErrValidation
		}
		if mihomoNonNode(mihomoRawProxy{Name: name, Type: kind}) {
			continue
		}
		seen[name] = true
		out = append(out, NetworkNode{ID: networkID("profile", subscription, name), SubscriptionID: subscription, Name: name, Type: kind, State: "unknown", Freshness: "missing", Loaded: false})
		if len(out) > networkItemLimit {
			return nil, ErrValidation
		}
	}
	return out, nil
}
