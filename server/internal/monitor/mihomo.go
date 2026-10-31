package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

const mihomoDetailLimit = 200
const mihomoResponseLimit = 1 << 20
const mihomoHistoryMaxAge = 15 * time.Minute

// MihomoDetails contains only public labels, quota metadata and existing test
// evidence. Reading these endpoints never asks mihomo to perform a healthcheck.
type MihomoDetails struct {
	Subscriptions           []MihomoSubscription `json:"subscriptions"`
	Nodes                   []MihomoNode         `json:"nodes"`
	ProviderCount           int                  `json:"provider_count"`
	EmbeddedProviderCount   int                  `json:"embedded_provider_count"`
	SubscriptionState       string               `json:"subscription_state"`
	SubscriptionCount       int                  `json:"subscription_count"`
	NodeCount               int                  `json:"node_count"`
	HealthyNodes            int                  `json:"healthy_nodes"`
	UnhealthyNodes          int                  `json:"unhealthy_nodes"`
	UnknownNodes            int                  `json:"unknown_nodes"`
	StaleNodes              int                  `json:"stale_nodes"`
	Truncated               bool                 `json:"truncated"`
	ProxySourceState        string               `json:"proxy_source_state"`
	SubscriptionUnavailable bool                 `json:"subscription_unavailable"`
}

type MihomoSubscription struct {
	UploadBytes    *int64            `json:"upload_bytes,omitempty"`
	DownloadBytes  *int64            `json:"download_bytes,omitempty"`
	TotalBytes     *int64            `json:"total_bytes,omitempty"`
	ExpireSeconds  *int64            `json:"expire_seconds,omitempty"`
	Name           string            `json:"name"`
	Info           *SubscriptionInfo `json:"info,omitempty"`
	UsedBytes      *int64            `json:"used_bytes,omitempty"`
	RemainingBytes *int64            `json:"remaining_bytes,omitempty"`
	UsageState     string            `json:"usage_state"`
	UpdatedAt      *time.Time        `json:"updated_at,omitempty"`
}

type MihomoNode struct {
	Provider  string     `json:"provider,omitempty"`
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	State     string     `json:"state"`
	Alive     *bool      `json:"alive,omitempty"`
	DelayMS   *uint16    `json:"delay_ms,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Freshness string     `json:"freshness"`
}

type mihomoRawProxy struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Provider string          `json:"provider-name"`
	Alive    *bool           `json:"alive"`
	All      json.RawMessage `json:"all"`
	History  []struct {
		Time  time.Time `json:"time"`
		Delay *uint16   `json:"delay"`
	} `json:"history"`
}

type mihomoRawSubscription struct {
	Upload   *int64 `json:"upload"`
	Download *int64 `json:"download"`
	Total    *int64 `json:"total"`
	Expire   *int64 `json:"expire"`
}

type mihomoRawProvider struct {
	VehicleType string                 `json:"vehicleType"`
	Info        *mihomoRawSubscription `json:"subscriptionInfo"`
	UpdatedAt   *time.Time             `json:"updatedAt"`
	Proxies     []mihomoRawProxy       `json:"proxies"`
}

// Explicit quota metadata can identify a subscription even when a compatible
// implementation labels every provider Compatible. Group memberships alone
// cannot identify an airport or reconstruct its quota.
func mihomoSubscriptionProvider(p mihomoRawProvider) bool {
	if p.Info != nil && (p.Info.Total != nil && *p.Info.Total > 0 || p.Info.Expire != nil && *p.Info.Expire > 0) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(p.VehicleType)) {
	case "compatible", "inline":
		return false
	default:
		return true // Older providers omit vehicleType; keep their existing projection.
	}
}

// Airports sometimes insert quota/expiry labels as connectable proxy rows.
// Require both a known label and a structured value; ordinary node names that
// merely mention traffic or expiry must survive.
var mihomoInformationalRow = regexp.MustCompile(`(?i)^(?:剩余流量|可用流量|套餐流量|本月剩余|traffic remaining|remaining traffic)\s*[:：]\s*[0-9]+(?:\.[0-9]+)?\s*(?:[kmgtp]i?b|[kmgtp])(?:\s*.*)?$|^(?:套餐到期|到期时间|订阅到期|expire|expiry|expiration)\s*[:：]\s*(?:长期有效|永久|不限期|无期限|never|unlimited|[0-9]{4}[-/.年][0-9]{1,2}[-/.月][0-9]{1,2}日?)(?:\s*.*)?$`)

func mihomoInformationalName(name string) bool {
	// Trim decorative prefixes without matching arbitrary leading words.
	name = strings.TrimLeftFunc(name, func(r rune) bool {
		return r == ' ' || r > 0xffff || r == '⏳' || r == '⌛' || r == '♾' || r == '\ufe0f'
	})
	return mihomoInformationalRow.MatchString(strings.TrimSpace(name))
}

func mihomoInvalid() error {
	return errors.New("mihomo response does not match the supported contract")
}

// CollectMihomo performs at most two fixed, read-only requests. Failure of the
// supplementary /proxies endpoint does not discard provider/subscription data.
func CollectMihomo(ctx context.Context, fetch func(context.Context, string) ([]byte, error), now time.Time) (*MihomoDetails, error) {
	body, err := fetch(ctx, "/providers/proxies")
	if err != nil {
		return nil, err
	}
	d, _, _, err := parseMihomoProviders(body, now)
	if err != nil {
		return nil, err
	}
	// Provider entries describe memberships and may repeat the same physical
	// node. Only /proxies can supply aggregate core node counts and health.
	d.Nodes = []MihomoNode{}
	d.NodeCount, d.HealthyNodes, d.UnhealthyNodes, d.UnknownNodes, d.StaleNodes = 0, 0, 0, 0, 0
	d.Truncated = d.SubscriptionCount > mihomoDetailLimit
	body, err = fetch(ctx, "/proxies")
	if err != nil {
		d.ProxySourceState = "unavailable"
		return d, nil
	}
	if len(body) > mihomoResponseLimit {
		d.ProxySourceState = "invalid_response"
		return d, nil
	}
	var response struct {
		Proxies map[string]mihomoRawProxy `json:"proxies"`
	}
	if json.Unmarshal(body, &response) != nil || response.Proxies == nil {
		d.ProxySourceState = "invalid_response"
		return d, nil
	}
	// Validate into a copy first, so a malformed supplemental item cannot leave
	// partially applied statistics presented as a fully available response.
	extra := &MihomoDetails{Nodes: []MihomoNode{}}
	for _, name := range mihomoSortedKeys(response.Proxies) {
		p := response.Proxies[name]
		if p.Name == "" {
			p.Name = name
		}
		if p.Name != name || !safeName(p.Name) || !safeName(p.Type) || (p.Provider != "" && !safeName(p.Provider)) {
			d.ProxySourceState = "invalid_response"
			return d, nil
		}
		if mihomoNonNode(p) {
			continue
		}
		addMihomoNode(extra, mihomoProjectNode(p, p.Provider, now))
	}
	for _, n := range extra.Nodes {
		if len(d.Nodes) < mihomoDetailLimit {
			d.Nodes = append(d.Nodes, n)
		}
	}
	d.NodeCount += extra.NodeCount
	d.HealthyNodes += extra.HealthyNodes
	d.UnhealthyNodes += extra.UnhealthyNodes
	d.UnknownNodes += extra.UnknownNodes
	d.StaleNodes += extra.StaleNodes
	d.Truncated = d.Truncated || d.NodeCount > mihomoDetailLimit
	d.ProxySourceState = "available"
	return d, nil
}

// ParseMihomoProviders supports callers which already fetched the mandatory
// provider response. ProxySourceState makes the missing supplemental source clear.
func ParseMihomoProviders(body []byte, now time.Time) (*MihomoDetails, error) {
	d, _, _, err := parseMihomoProviders(body, now)
	return d, err
}

func mihomoSortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func parseMihomoProviders(body []byte, now time.Time) (*MihomoDetails, map[string]bool, map[string]bool, error) {
	if len(body) > mihomoResponseLimit {
		return nil, nil, nil, mihomoInvalid()
	}
	var response struct {
		Providers map[string]mihomoRawProvider `json:"providers"`
	}
	if json.Unmarshal(body, &response) != nil || response.Providers == nil {
		return nil, nil, nil, mihomoInvalid()
	}
	d := &MihomoDetails{ProviderCount: len(response.Providers), SubscriptionState: "available", Subscriptions: []MihomoSubscription{}, Nodes: []MihomoNode{}, ProxySourceState: "not_collected"}
	seen, names := map[string]bool{}, map[string]bool{}
	for _, name := range mihomoSortedKeys(response.Providers) {
		if !safeName(name) {
			return nil, nil, nil, mihomoInvalid()
		}
		provider := response.Providers[name]
		if !mihomoSubscriptionProvider(provider) {
			d.EmbeddedProviderCount++
			continue
		}
		d.SubscriptionCount++
		sub, err := mihomoProjectSubscription(name, provider, now)
		if err != nil {
			return nil, nil, nil, err
		}
		if sub.UsageState == "expired" || sub.UsageState == "exhausted" {
			d.SubscriptionUnavailable = true
		}
		if len(d.Subscriptions) < mihomoDetailLimit {
			d.Subscriptions = append(d.Subscriptions, sub)
		}
		for _, p := range provider.Proxies {
			if !safeName(p.Name) || !safeName(p.Type) {
				return nil, nil, nil, mihomoInvalid()
			}
			if mihomoNonNode(p) {
				continue
			}
			key := name + "\x00" + p.Name
			if seen[key] {
				continue
			}
			seen[key], names[p.Name] = true, true
			addMihomoNode(d, mihomoProjectNode(p, name, now))
		}
	}
	if d.SubscriptionCount == 0 && d.EmbeddedProviderCount > 0 {
		d.SubscriptionState = "not_exposed"
	}
	d.Truncated = d.SubscriptionCount > mihomoDetailLimit || d.NodeCount > mihomoDetailLimit
	return d, seen, names, nil
}

func mihomoProjectSubscription(name string, p mihomoRawProvider, now time.Time) (MihomoSubscription, error) {
	sub := MihomoSubscription{Name: name, UsageState: "unknown", UpdatedAt: p.UpdatedAt}
	if sub.UpdatedAt != nil && sub.UpdatedAt.IsZero() {
		sub.UpdatedAt = nil
	}
	if p.Info == nil {
		return sub, nil
	}
	i := p.Info
	for _, v := range []*int64{i.Upload, i.Download, i.Total, i.Expire} {
		if v != nil && *v < 0 {
			return sub, mihomoInvalid()
		}
	}
	if i.Expire != nil && *i.Expire > 253402300799 {
		return sub, mihomoInvalid()
	}
	value := func(v *int64) int64 {
		if v == nil {
			return 0
		}
		return *v
	}
	sub.UploadBytes, sub.DownloadBytes, sub.TotalBytes, sub.ExpireSeconds = i.Upload, i.Download, i.Total, i.Expire
	sub.Info = &SubscriptionInfo{Upload: value(i.Upload), Download: value(i.Download), Total: value(i.Total), Expire: value(i.Expire)}
	if i.Upload != nil && i.Download != nil {
		if *i.Upload > math.MaxInt64-*i.Download {
			return sub, mihomoInvalid()
		}
		used := *i.Upload + *i.Download
		sub.UsedBytes = &used
		if i.Total != nil && *i.Total > 0 {
			remaining := *i.Total - used
			if remaining < 0 {
				remaining = 0
			}
			sub.RemainingBytes = &remaining
			sub.UsageState = "available"
			if remaining == 0 {
				sub.UsageState = "exhausted"
			}
		}
	}
	if i.Expire != nil && *i.Expire > 0 && *i.Expire <= now.Unix() {
		sub.UsageState = "expired"
	}
	return sub, nil
}

func mihomoNonNode(p mihomoRawProxy) bool {
	if mihomoInformationalName(p.Name) {
		return true
	}
	if len(p.All) > 0 && string(p.All) != "null" {
		return true
	}
	switch strings.ToLower(strings.ReplaceAll(p.Type, "-", "")) {
	case "direct", "reject", "rejectdrop", "pass", "dns", "compatible", "selector", "urltest", "fallback", "loadbalance", "relay":
		return true
	}
	return false
}

func mihomoProjectNode(p mihomoRawProxy, provider string, now time.Time) MihomoNode {
	n := MihomoNode{Provider: provider, Name: p.Name, Type: p.Type, Alive: p.Alive, State: "unknown", Freshness: "missing"}
	for _, h := range p.History {
		if h.Time.IsZero() || h.Delay == nil {
			continue
		}
		if n.CheckedAt == nil || h.Time.After(*n.CheckedAt) {
			t, delay := h.Time, *h.Delay
			n.CheckedAt, n.DelayMS = &t, &delay
		}
	}
	if n.CheckedAt == nil {
		return n
	}
	if n.CheckedAt.After(now) {
		n.Freshness = "future"
		return n
	}
	if now.Sub(*n.CheckedAt) > mihomoHistoryMaxAge {
		n.Freshness = "stale"
		return n
	}
	n.Freshness = "fresh"
	if *n.DelayMS == 0 || (n.Alive != nil && !*n.Alive) {
		n.State = "unhealthy"
	} else if *n.DelayMS > 0 {
		n.State = "healthy"
	}
	return n
}

func addMihomoNode(d *MihomoDetails, n MihomoNode) {
	d.NodeCount++
	switch n.State {
	case "healthy":
		d.HealthyNodes++
	case "unhealthy":
		d.UnhealthyNodes++
	default:
		d.UnknownNodes++
	}
	if n.Freshness == "stale" {
		d.StaleNodes++
	}
	if len(d.Nodes) < mihomoDetailLimit {
		d.Nodes = append(d.Nodes, n)
	}
}
