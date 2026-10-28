package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// GatewayFetch must apply the monitor's same-origin, authentication, SSRF,
// timeout and response-size protections to each GET. It must not follow links
// returned by the remote API. This collector never requests a completion.
type GatewayFetch func(context.Context, string) ([]byte, error)

type GatewayOptions struct {
	Models bool
	Logs   bool
}

// GatewayDetails is a bounded snapshot, not an incrementally accumulated ledger.
// Each source has an independent state so a disabled DB or denied management
// permission cannot turn a readable process endpoint into a model failure.
type GatewayDetails struct {
	CatalogState     string              `json:"catalog_state"`
	DeploymentsState string              `json:"deployments_state"`
	LogsState        string              `json:"logs_state"`
	Models           []GatewayModel      `json:"models,omitempty"`
	Deployments      []GatewayDeployment `json:"deployments,omitempty"`
	Traffic          []GatewayTraffic    `json:"traffic,omitempty"`
	Issues           []GatewayIssue      `json:"issues,omitempty"`
	ModelCount       int                 `json:"model_count"`
	DeploymentCount  int                 `json:"deployment_count"`
	WindowStart      time.Time           `json:"window_start"`
	WindowEnd        time.Time           `json:"window_end"`
	LatestAt         *time.Time          `json:"latest_at,omitempty"`
	Rows             int                 `json:"rows"`
	Total            int                 `json:"total"`
	Skipped          int                 `json:"skipped"`
	Sampled          bool                `json:"sampled"`
	Truncated        bool                `json:"truncated"`
}

type GatewayModel struct {
	ID string `json:"id"`
}

type GatewayDeployment struct {
	ID            string `json:"id,omitempty"`
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Channel       string `json:"channel,omitempty"`
}

type GatewayTraffic struct {
	Model        string    `json:"model"`
	DeploymentID string    `json:"deployment_id,omitempty"`
	Channel      string    `json:"channel,omitempty"`
	Success      int       `json:"success"`
	Failure      int       `json:"failure"`
	Unknown      int       `json:"unknown"`
	CacheHits    int       `json:"cache_hits"`
	LastAt       time.Time `json:"last_at"`
}

type GatewayIssue struct {
	Source string `json:"source"`
	Class  string `json:"class"`
}

const gatewayLimit = 1 << 20

var errGatewayResponse = errors.New("unsupported gateway response")

func gatewayLabel(s string) bool {
	return safeName(s) && !strings.ContainsAny(s, "?#") && !strings.Contains(s, "://")
}

func gatewayChannel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	// Never expose upstream URLs, credentials, query strings or paths. This is
	// an opaque endpoint group, not a verified commercial provider identity.
	origin := strings.ToLower(u.Scheme + "://" + u.Host)
	h := sha256.Sum256([]byte(origin))
	return "endpoint-" + hex.EncodeToString(h[:6])
}

func gatewayBody(fetch GatewayFetch, ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, err := fetch(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(b) > gatewayLimit {
		return nil, errGatewayResponse
	}
	return b, nil
}

// CollectGateway reads at most four endpoints/pages: a model catalog, deployment
// configuration and two 100-row log pages. The caller opts into each category.
// No process, catalog or log result proves current per-model inference health.
func CollectGateway(ctx context.Context, kind string, options GatewayOptions, fetch GatewayFetch, now time.Time) *GatewayDetails {
	now = now.UTC().Truncate(time.Second)
	d := &GatewayDetails{CatalogState: "disabled", DeploymentsState: "disabled", LogsState: "disabled", WindowStart: now.Add(-24 * time.Hour), WindowEnd: now}
	issue := func(source string, err error) string {
		class := "source_unavailable"
		state := "unavailable"
		if errors.Is(err, errGatewayResponse) {
			class, state = "invalid_response", "invalid"
		}
		var fetchErr *FetchError
		if errors.As(err, &fetchErr) {
			// These are stable monitor classifications, never the raw upstream
			// response/error text (which may contain URLs or credentials).
			switch fetchErr.Status {
			case StatusUnauthorized:
				state = "unauthorized"
			case StatusUnsupported:
				state = "unsupported"
			}
			switch fetchErr.Class {
			case "authentication_failed", "credential_unavailable", "credential_invalid", "endpoint_unsupported", "redirect_rejected", "configuration_invalid", "response_too_large", "response_incomplete", "rate_limited", "http_error", "timeout", "network_error", "target_blocked":
				class = fetchErr.Class
			}
		}
		d.Issues = append(d.Issues, GatewayIssue{Source: source, Class: class})
		return state
	}
	if options.Models {
		b, err := gatewayBody(fetch, ctx, "/v1/models")
		if err == nil {
			d.Models, d.ModelCount, err = parseGatewayCatalog(b)
		}
		if err != nil {
			d.CatalogState = issue("/v1/models", err)
		} else {
			d.CatalogState = "available"
			d.Truncated = d.ModelCount > len(d.Models)
		}
		if kind == KindLiteLLM {
			b, err = gatewayBody(fetch, ctx, "/model/info")
			if err == nil {
				d.Deployments, d.DeploymentCount, err = parseGatewayDeployments(b)
			}
			if err != nil {
				d.DeploymentsState = issue("/model/info", err)
			} else {
				d.DeploymentsState = "available"
				d.Truncated = d.Truncated || d.DeploymentCount > len(d.Deployments)
			}
		} else {
			d.DeploymentsState = "unsupported"
		}
	}
	if !options.Logs {
		return d
	}
	if kind != KindLiteLLM {
		d.LogsState = "unsupported"
		return d
	}
	collectGatewayLogs(ctx, fetch, d, issue)
	return d
}

func parseGatewayCatalog(b []byte) ([]GatewayModel, int, error) {
	var input struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &input) != nil || input.Data == nil {
		return nil, 0, errGatewayResponse
	}
	seen := make(map[string]bool)
	models := make([]GatewayModel, 0)
	for _, m := range input.Data {
		if !gatewayLabel(m.ID) || seen[m.ID] {
			return nil, 0, errGatewayResponse
		}
		seen[m.ID] = true
		if len(models) < 200 {
			models = append(models, GatewayModel{ID: m.ID})
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, len(input.Data), nil
}

func parseGatewayDeployments(b []byte) ([]GatewayDeployment, int, error) {
	var input struct {
		Data []struct {
			Name   string `json:"model_name"`
			Params struct {
				Model    string `json:"model"`
				APIBase  string `json:"api_base"`
				Provider string `json:"custom_llm_provider"`
			} `json:"litellm_params"`
			Info struct {
				ID string `json:"id"`
			} `json:"model_info"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &input) != nil || input.Data == nil {
		return nil, 0, errGatewayResponse
	}
	deployments := make([]GatewayDeployment, 0)
	seen := make(map[string]bool)
	for _, m := range input.Data {
		if !gatewayLabel(m.Name) || (m.Info.ID != "" && !gatewayLabel(m.Info.ID)) || (m.Params.Model != "" && !gatewayLabel(m.Params.Model)) {
			return nil, 0, errGatewayResponse
		}
		// A missing stable deployment ID must not collapse same-named routes
		// from different upstreams into a single channel.
		key := m.Info.ID + "\x00" + m.Name + "\x00" + m.Params.Model + "\x00" + gatewayChannel(m.Params.APIBase)
		if seen[key] {
			continue
		}
		seen[key] = true
		provider := m.Params.Provider
		if provider == "" {
			provider, _, _ = strings.Cut(m.Params.Model, "/")
			if !strings.Contains(m.Params.Model, "/") {
				provider = ""
			}
		}
		if provider != "" && !gatewayLabel(provider) {
			return nil, 0, errGatewayResponse
		}
		if len(deployments) < 200 {
			deployments = append(deployments, GatewayDeployment{ID: m.Info.ID, Model: m.Name, UpstreamModel: m.Params.Model, Provider: provider, Channel: gatewayChannel(m.Params.APIBase)})
		}
	}
	sort.Slice(deployments, func(i, j int) bool {
		if deployments[i].Model == deployments[j].Model {
			return deployments[i].ID < deployments[j].ID
		}
		return deployments[i].Model < deployments[j].Model
	})
	return deployments, len(seen), nil
}

type gatewayLogRow struct {
	ID       string          `json:"request_id"`
	Model    string          `json:"model"`
	Group    string          `json:"model_group"`
	ModelID  string          `json:"model_id"`
	Status   *string         `json:"status"`
	Start    string          `json:"startTime"`
	End      string          `json:"endTime"`
	CacheHit json.RawMessage `json:"cache_hit"`
}

type gatewayLogPage struct {
	Data        []gatewayLogRow `json:"data"`
	Total       *int            `json:"total"`
	Page        int             `json:"page"`
	PageSize    int             `json:"page_size"`
	TotalPages  int             `json:"total_pages"`
	TotalCapped bool            `json:"total_is_capped"`
}

func gatewayTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func collectGatewayLogs(ctx context.Context, fetch GatewayFetch, d *GatewayDetails, issue func(string, error) string) {
	const pageSize = 100
	seen := make(map[string]bool)
	groups := make(map[string]*GatewayTraffic)
	channels := make(map[string]string)
	for _, deployment := range d.Deployments {
		if deployment.ID != "" {
			if prior, found := channels[deployment.ID]; found && prior != deployment.Channel {
				channels[deployment.ID] = ""
			} else if !found {
				channels[deployment.ID] = deployment.Channel
			}
		}
	}
	d.LogsState = "available"
	for page := 1; page <= 2; page++ {
		q := url.Values{"start_date": {d.WindowStart.Format("2006-01-02 15:04:05")}, "end_date": {d.WindowEnd.Format("2006-01-02 15:04:05")}, "page": {fmt.Sprint(page)}, "page_size": {fmt.Sprint(pageSize)}, "sort_by": {"startTime"}, "sort_order": {"desc"}, "exclude_internal_health_checks": {"true"}}
		b, err := gatewayBody(fetch, ctx, "/spend/logs/v2?"+q.Encode())
		var data gatewayLogPage
		if err == nil && (json.Unmarshal(b, &data) != nil || data.Data == nil || data.Total == nil || *data.Total < 0 || data.Page != page || data.PageSize != pageSize || data.TotalPages < 0 || len(data.Data) > pageSize) {
			err = errGatewayResponse
		}
		if err != nil {
			d.LogsState = issue("/spend/logs/v2", err)
			if page > 1 {
				d.LogsState, d.Sampled = "partial", true
			}
			break
		}
		if *data.Total > d.Total {
			d.Total = *data.Total
		}
		d.Sampled = d.Sampled || data.TotalCapped
		for _, row := range data.Data {
			// Source request IDs are used only for in-memory deduplication and
			// are never returned, persisted or exposed in errors.
			if row.ID == "" || len(row.ID) > 512 {
				d.Skipped++
				continue
			}
			if seen[row.ID] {
				d.Sampled = true // pagination moved; do not claim exact coverage
				continue
			}
			seen[row.ID] = true
			model := row.Group
			if model == "" {
				model = row.Model
			}
			start, validStart := gatewayTime(row.Start)
			end, validEnd := gatewayTime(row.End)
			if !gatewayLabel(model) || (row.ModelID != "" && !gatewayLabel(row.ModelID)) || !validStart || !validEnd || start.Before(d.WindowStart) || start.After(d.WindowEnd) || end.Before(start) || end.After(d.WindowEnd) {
				d.Skipped++
				continue
			}
			key := model + "\x00" + row.ModelID
			g := groups[key]
			if g == nil {
				g = &GatewayTraffic{Model: model, DeploymentID: row.ModelID, Channel: channels[row.ModelID], LastAt: end}
				groups[key] = g
			}
			cached := string(row.CacheHit) == "true" || string(row.CacheHit) == `"True"` || string(row.CacheHit) == `"true"`
			switch {
			case cached:
				g.CacheHits++
			case row.Status != nil && *row.Status == "success":
				g.Success++
			case row.Status != nil && *row.Status == "failure":
				g.Failure++
			default:
				g.Unknown++
			}
			if end.After(g.LastAt) {
				g.LastAt = end
			}
			if d.LatestAt == nil || end.After(*d.LatestAt) {
				t := end
				d.LatestAt = &t
			}
			d.Rows++
		}
		if len(data.Data) < pageSize {
			if d.Rows+d.Skipped < d.Total {
				d.Sampled = true
			}
			break
		}
		if page == 2 {
			d.Sampled = true // a full boundary page cannot prove exhaustion
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		d.Traffic = append(d.Traffic, *groups[key])
	}
	if d.Skipped > 0 {
		d.Sampled = true
	}
}
