package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"relayservice/internal/storage"
)

func (e *Engine) ModelsWorkspace(ctx context.Context, q ModelsWorkspaceQuery) (ModelsWorkspaceView, error) {
	now := e.now()
	out := ModelsWorkspaceView{CollectedAt: now, Sources: []ModelsWorkspaceSource{}, Models: []ModelsWorkspaceModel{}, Channels: []ModelsWorkspaceChannel{}, Traffic: []ModelsWorkspaceTraffic{}, PublicStatus: []ModelsWorkspaceStatus{}, Probes: ModelsWorkspaceProbes{State: "not_implemented", Message: "主动模型探针尚未实现；本页不会发送推理请求。"}, Coverage: ModelsWorkspaceCoverage{Notes: []string{"调用证据仅采用每个实例当前检查快照，不累加重叠的24小时日志窗口；不是完整请求账本。", "目录可见、部署已配置和窗口内历史调用样本均不证明当前推理可用；分组最近时间不能说明每种结果发生的时间。", "渠道来自明确部署映射；无法唯一关联的日志保留未关联，不按模型名称猜测渠道。", "公开状态页独立展示，不按组件名称自动关联模型。"}}}
	if q.Section == "" {
		q.Section = "overview"
	}
	switch q.Section {
	case "overview", "models", "channels", "traffic", "status", "probes":
	default:
		return out, ErrValidation
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 200 || len(q.Q) > 256 || len(q.Service) > 128 || len(q.State) > 64 {
		return out, ErrValidation
	}
	offset := 0
	if q.Cursor != "" {
		var err error
		offset, err = strconv.Atoi(q.Cursor)
		if err != nil || offset < 0 || offset > 100000 {
			return out, ErrValidation
		}
	}
	q.Q = strings.ToLower(strings.TrimSpace(q.Q))
	err := e.store.View(ctx, func(tx storage.Tx) error {
		after, scanned := "", 0
		for {
			rows, err := tx.Scan(servicesBucket, storage.Scan{After: after, Limit: 200})
			if err != nil {
				return err
			}
			for _, row := range rows {
				after = row.Key
				scanned++
				if scanned > 10000 {
					out.Coverage.Truncated = true
					return nil
				}
				var rec serviceRecord
				if err := json.Unmarshal(row.Value, &rec); err != nil {
					return err
				}
				s := rec.Service
				if s.Tombstone || q.Service != "" && s.ID != q.Service || !modelsWorkspaceEligible(s) {
					continue
				}
				if len(out.Sources) >= 200 {
					out.Coverage.Truncated = true
					return nil
				}
				s, err = e.serviceWithState(tx, rec)
				if err != nil {
					return err
				}
				e.projectModelsWorkspace(s, &out, now)
			}
			if len(rows) < 200 {
				return nil
			}
		}
	})
	if err != nil {
		return out, err
	}
	if out.Coverage.Truncated {
		out.Coverage.Notes = append(out.Coverage.Notes, "展示存在采集或工作区容量截断；未展示条目不代表不存在。")
	}
	sort.Slice(out.Sources, func(i, j int) bool {
		return out.Sources[i].Name+out.Sources[i].ID < out.Sources[j].Name+out.Sources[j].ID
	})
	sort.Slice(out.Models, func(i, j int) bool {
		return out.Models[i].ServiceID+"\x00"+out.Models[i].Name < out.Models[j].ServiceID+"\x00"+out.Models[j].Name
	})
	sort.Slice(out.Channels, func(i, j int) bool {
		return out.Channels[i].ServiceID+"\x00"+out.Channels[i].Model+"\x00"+out.Channels[i].ID < out.Channels[j].ServiceID+"\x00"+out.Channels[j].Model+"\x00"+out.Channels[j].ID
	})
	sort.Slice(out.Traffic, func(i, j int) bool {
		if !out.Traffic[i].LastAt.Equal(out.Traffic[j].LastAt) {
			return out.Traffic[i].LastAt.After(out.Traffic[j].LastAt)
		}
		return out.Traffic[i].ID < out.Traffic[j].ID
	})
	sort.Slice(out.PublicStatus, func(i, j int) bool {
		return out.PublicStatus[i].Name+out.PublicStatus[i].ServiceID < out.PublicStatus[j].Name+out.PublicStatus[j].ServiceID
	})
	out.Summary.Services = len(out.Sources)
	out.Summary.Models = len(out.Models)
	knownChannels := map[string]bool{}
	for _, channel := range out.Channels {
		if channel.Channel != "" {
			knownChannels[channel.ServiceID+"\x00"+channel.Channel] = true
		}
	}
	out.Summary.Channels = len(knownChannels)
	out.Summary.Deployments = len(out.Channels)
	out.Summary.TrafficGroups = len(out.Traffic)
	out.Summary.PublicStatusServices = len(out.PublicStatus)
	models, channels, traffic, status := out.Models, out.Channels, out.Traffic, out.PublicStatus
	out.Models = []ModelsWorkspaceModel{}
	out.Channels = []ModelsWorkspaceChannel{}
	out.Traffic = []ModelsWorkspaceTraffic{}
	out.PublicStatus = []ModelsWorkspaceStatus{}
	match := func(state string, labels ...string) bool {
		if q.State != "" && q.State != state {
			return false
		}
		return q.Q == "" || strings.Contains(strings.ToLower(strings.Join(labels, " ")), q.Q)
	}
	sourceNames := map[string]string{}
	for _, s := range out.Sources {
		sourceNames[s.ID] = s.Name
	}
	switch q.Section {
	case "models":
		for _, v := range models {
			if match(v.InferenceState, v.Name, sourceNames[v.ServiceID]) {
				out.Models = append(out.Models, v)
			}
		}
		out.Total = len(out.Models)
		start, end := modelsWorkspacePage(offset, q.Limit, out.Total)
		out.Models = out.Models[start:end]
	case "channels":
		for _, v := range channels {
			if match(v.InferenceState, v.Model, v.UpstreamModel, v.Provider, v.Channel, v.DeploymentID, sourceNames[v.ServiceID]) {
				out.Channels = append(out.Channels, v)
			}
		}
		out.Total = len(out.Channels)
		start, end := modelsWorkspacePage(offset, q.Limit, out.Total)
		out.Channels = out.Channels[start:end]
	case "traffic":
		for _, v := range traffic {
			if match(v.InferenceState, v.Model, v.Channel, v.DeploymentID, sourceNames[v.ServiceID]) {
				out.Traffic = append(out.Traffic, v)
			}
		}
		out.Total = len(out.Traffic)
		start, end := modelsWorkspacePage(offset, q.Limit, out.Total)
		out.Traffic = out.Traffic[start:end]
	case "status":
		for _, v := range status {
			labels := []string{v.Name}
			for _, c := range v.Components {
				labels = append(labels, c.Name, c.Category)
			}
			if match(v.State, labels...) {
				out.PublicStatus = append(out.PublicStatus, v)
			}
		}
		out.Total = len(out.PublicStatus)
		start, end := modelsWorkspacePage(offset, q.Limit, out.Total)
		out.PublicStatus = out.PublicStatus[start:end]
	}
	if offset+q.Limit < out.Total {
		out.HasMore = true
		out.NextCursor = strconv.Itoa(offset + q.Limit)
	}
	return out, nil
}

func modelsWorkspaceEligible(s Service) bool {
	return s.Kind == KindStatuspage || s.Kind == KindLiteLLM || s.Kind == KindWorkbuddy2API || s.Kind == KindOpenAI || s.Kind == KindOllama || s.Kind == KindOpenWebUI || s.CollectModels || s.CollectLogs
}
func modelsWorkspacePage(offset, limit, total int) (int, int) {
	if offset > total {
		offset = total
	}
	return offset, min(offset+limit, total)
}
func modelsWorkspaceID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func (e *Engine) projectModelsWorkspace(s Service, out *ModelsWorkspaceView, now time.Time) {
	state := func(enabled bool) string {
		if enabled {
			return "not_collected"
		}
		return "disabled"
	}
	source := ModelsWorkspaceSource{ID: s.ID, Name: s.Name, Kind: s.Kind, Enabled: s.Enabled, Generation: s.Generation, Status: s.Status, Freshness: s.FreshnessState, CatalogState: state(s.CollectModels || s.Kind == KindOpenAI), DeploymentsState: state(s.CollectModels), LogsState: state(s.CollectLogs), Issues: []GatewayIssue{}}
	c := s.Current
	if c == nil || c.Generation != s.Generation || c.CompletedAt.IsZero() || c.CompletedAt.After(now) {
		c = nil
		source.Freshness = "never_observed"
		source.Status = StatusUnknown
	}
	if !s.Enabled {
		source.Status = "paused"
		source.Freshness = "paused"
	}
	if c != nil {
		at := c.CompletedAt
		source.CollectedAt = &at
		if s.Enabled && now.Sub(at) > 2*time.Duration(s.IntervalSeconds)*time.Second {
			source.Freshness = "stale"
			source.Status = StatusUnknown
		}
	}
	var details *Details
	if c != nil {
		details = c.Details
	}
	if details == nil {
		if c != nil {
			failureState := "unavailable"
			if c.Status == StatusUnauthorized {
				failureState = "unauthorized"
			}
			if c.Status == StatusUnsupported {
				failureState = "unsupported"
			}
			if source.CatalogState != "disabled" {
				source.CatalogState = failureState
			}
			if source.DeploymentsState != "disabled" {
				source.DeploymentsState = failureState
			}
			if source.LogsState != "disabled" {
				source.LogsState = failureState
			}
		}
		out.Sources = append(out.Sources, source)
		if s.Kind == KindStatuspage {
			status := source.Status
			if source.Freshness == "stale" {
				status = "stale"
			}
			out.PublicStatus = append(out.PublicStatus, ModelsWorkspaceStatus{ServiceID: s.ID, Name: s.Name, State: status, Freshness: source.Freshness, CollectedAt: source.CollectedAt, Unknown: true, Components: []PublicStatusComponent{}})
		}
		return
	}
	if s.Kind == KindStatuspage {
		projectWorkspaceStatus(s, source, details, out)
	}
	g := details.Gateway
	if g == nil {
		if len(details.Models) > 0 || s.Kind == KindOpenAI && c.Success {
			source.CatalogState = "available"
		}
		source.ModelCount = len(details.Models)
		if s.Kind == KindOpenAI {
			source.ModelCount = details.Count
		}
		source.Truncated = details.Truncated
		for _, m := range details.Models {
			if !gatewayLabel(m.ID) {
				continue
			}
			out.Models = append(out.Models, ModelsWorkspaceModel{ID: modelsWorkspaceID(s.ID, m.ID), ServiceID: s.ID, Name: m.ID, CatalogVisible: true, CatalogState: source.CatalogState, InferenceState: workspaceInference(ModelsWorkspaceCounts{}, source.LogsState, source.Freshness, now), Deployments: []GatewayDeployment{}, Freshness: source.Freshness, CollectedAt: source.CollectedAt})
		}
		out.Coverage.Truncated = out.Coverage.Truncated || source.Truncated
		out.Sources = append(out.Sources, source)
		return
	}
	source.CatalogState = g.CatalogState
	source.DeploymentsState = g.DeploymentsState
	source.LogsState = g.LogsState
	source.ModelCount = g.ModelCount
	source.DeploymentCount = g.DeploymentCount
	source.Rows = g.Rows
	source.Total = g.Total
	source.Skipped = g.Skipped
	source.Sampled = g.Sampled
	source.Truncated = g.Truncated
	source.Issues = append(source.Issues, g.Issues...)
	source.LatestAt = g.LatestAt
	if !g.WindowStart.IsZero() {
		at := g.WindowStart
		source.WindowStart = &at
	}
	if !g.WindowEnd.IsZero() {
		at := g.WindowEnd
		source.WindowEnd = &at
	}
	models := map[string]*ModelsWorkspaceModel{}
	model := func(name string) *ModelsWorkspaceModel {
		if !gatewayLabel(name) {
			return nil
		}
		m := models[name]
		if m == nil {
			m = &ModelsWorkspaceModel{ID: modelsWorkspaceID(s.ID, name), ServiceID: s.ID, Name: name, CatalogState: source.CatalogState, Deployments: []GatewayDeployment{}, Freshness: source.Freshness, CollectedAt: source.CollectedAt}
			models[name] = m
		}
		return m
	}
	for _, v := range g.Models {
		if m := model(v.ID); m != nil {
			m.CatalogVisible = true
		}
	}
	channels := []ModelsWorkspaceChannel{}
	deploymentMatches := map[string][]int{}
	seen := map[string]bool{}
	for _, d := range g.Deployments {
		m := model(d.Model)
		if m == nil {
			continue
		}
		id := modelsWorkspaceID(s.ID, d.ID, d.Model, d.UpstreamModel, d.Provider, d.Channel)
		if seen[id] {
			continue
		}
		seen[id] = true
		m.Deployments = append(m.Deployments, d)
		channels = append(channels, ModelsWorkspaceChannel{ID: id, ServiceID: s.ID, Model: d.Model, DeploymentID: d.ID, UpstreamModel: d.UpstreamModel, Provider: d.Provider, Channel: d.Channel, Freshness: source.Freshness, CollectedAt: source.CollectedAt})
		if d.ID != "" {
			deploymentMatches[d.ID] = append(deploymentMatches[d.ID], len(channels)-1)
		}
	}
	for _, t := range g.Traffic {
		m := model(t.Model)
		if m == nil || t.Success < 0 || t.Failure < 0 || t.Unknown < 0 || t.CacheHits < 0 || t.LastAt.After(now) || t.LastAt.IsZero() {
			continue
		}
		counts := ModelsWorkspaceCounts{Success: t.Success, Failure: t.Failure, Unknown: t.Unknown, CacheHits: t.CacheHits, LastAt: &t.LastAt}
		addWorkspaceCounts(&m.Traffic, counts)
		channel := ""
		matches := deploymentMatches[t.DeploymentID]
		if len(matches) == 1 {
			ch := &channels[matches[0]]
			if ch.Model == t.Model && (t.Channel == "" || t.Channel == ch.Channel) {
				addWorkspaceCounts(&ch.Traffic, counts)
				channel = ch.Channel
			}
		}
		out.Traffic = append(out.Traffic, ModelsWorkspaceTraffic{ID: modelsWorkspaceID(s.ID, t.Model, t.DeploymentID), ServiceID: s.ID, Model: t.Model, DeploymentID: t.DeploymentID, Channel: channel, Success: t.Success, Failure: t.Failure, Unknown: t.Unknown, CacheHits: t.CacheHits, LastAt: t.LastAt, InferenceState: workspaceInference(counts, g.LogsState, source.Freshness, now), WindowStart: g.WindowStart, WindowEnd: g.WindowEnd, Sampled: g.Sampled || g.LogsState == "partial", Freshness: source.Freshness})
	}
	for _, m := range models {
		m.InferenceState = workspaceInference(m.Traffic, source.LogsState, source.Freshness, now)
		out.Models = append(out.Models, *m)
	}
	for i := range channels {
		channels[i].InferenceState = workspaceInference(channels[i].Traffic, source.LogsState, source.Freshness, now)
	}
	out.Channels = append(out.Channels, channels...)
	out.Coverage.Truncated = out.Coverage.Truncated || g.Truncated
	out.Sources = append(out.Sources, source)
}

func workspaceInference(c ModelsWorkspaceCounts, logsState, freshness string, now time.Time) string {
	if freshness == "stale" || freshness == "paused" {
		return "stale"
	}
	if c.LastAt != nil && c.LastAt.After(now) {
		return "stale"
	}
	if c.Success > 0 && c.Failure > 0 {
		return "historical_mixed"
	}
	if c.Failure > 0 {
		return "historical_failure"
	}
	if c.Success > 0 {
		return "historical_success"
	}
	if c.Unknown > 0 {
		return "unknown"
	}
	if c.CacheHits > 0 {
		return "cache_only"
	}
	if logsState == "available" || logsState == "partial" {
		return "no_samples"
	}
	return "not_tested"
}
func addWorkspaceCounts(dst *ModelsWorkspaceCounts, src ModelsWorkspaceCounts) {
	dst.Success += src.Success
	dst.Failure += src.Failure
	dst.Unknown += src.Unknown
	dst.CacheHits += src.CacheHits
	if src.LastAt != nil && (dst.LastAt == nil || src.LastAt.After(*dst.LastAt)) {
		at := *src.LastAt
		dst.LastAt = &at
	}
}
func projectWorkspaceStatus(s Service, source ModelsWorkspaceSource, d *Details, out *ModelsWorkspaceView) {
	p := d.PublicStatus
	v := ModelsWorkspaceStatus{ServiceID: s.ID, Name: s.Name, State: source.Status, Freshness: source.Freshness, CollectedAt: source.CollectedAt, Components: []PublicStatusComponent{}, Unknown: true}
	if p != nil {
		v.Source = p.Source
		v.Count = p.Count
		v.Truncated = p.Truncated
		v.Components = append(v.Components, p.Components...)
		v.Unknown = p.Unknown
		v.Unhealthy = p.Unhealthy
		v.UpdatedAt = p.UpdatedAt
		if !p.ObservedAt.IsZero() {
			at := p.ObservedAt
			v.ObservedAt = &at
		}
		if source.Freshness == "fresh" {
			v.State = "healthy"
			if p.Unhealthy {
				v.State = "unhealthy"
			} else if p.Unknown {
				v.State = "unknown"
			}
		}
	} else {
		v.Count = d.Count
		v.Truncated = d.Truncated
		for _, c := range d.Components {
			v.Components = append(v.Components, PublicStatusComponent{Name: c.Name, Status: c.Status})
		}
	}
	if source.Freshness == "stale" {
		v.State = "stale"
	}
	if !s.Enabled {
		v.State = "paused"
	}
	out.Coverage.Truncated = out.Coverage.Truncated || v.Truncated
	out.PublicStatus = append(out.PublicStatus, v)
}
