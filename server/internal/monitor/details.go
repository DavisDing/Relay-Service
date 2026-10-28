package monitor

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"
)

// These projections intentionally omit provider URLs, proxy connection
// secrets, raw responses, ownership fields and credentials. Node labels and
// existing quality metadata are projected separately. Details share retention.
type Details struct {
	PublicStatus  *PublicStatus   `json:"public_status,omitempty"`
	Mihomo        *MihomoDetails  `json:"mihomo,omitempty"`
	Gateway       *GatewayDetails `json:"gateway,omitempty"`
	Source        string          `json:"source"`
	Subscriptions []Subscription  `json:"subscriptions,omitempty"`
	Models        []Model         `json:"models,omitempty"`
	Components    []Component     `json:"components,omitempty"`
	Count         int             `json:"count"`
	Truncated     bool            `json:"truncated"`
}
type Subscription struct {
	Name string            `json:"name"`
	Info *SubscriptionInfo `json:"info,omitempty"`
}
type SubscriptionInfo struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"`
}
type Model struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type Component struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func safeName(s string) bool {
	if strings.TrimSpace(s) == "" || len(s) > 256 {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func detailObservation(kind string, b []byte, now time.Time) observation {
	invalid := func() observation {
		return failure(StatusUnsupported, "invalid_response", "监控接口未返回支持的数据结构。")
	}
	d := &Details{}
	result := positive("model_catalog", "已读取模型目录；各模型推理可用性和失败率尚未验证。")
	switch kind {
	case KindMihomo:
		var input struct {
			Providers map[string]struct {
				Info *SubscriptionInfo `json:"subscriptionInfo"`
			} `json:"providers"`
		}
		if json.Unmarshal(b, &input) != nil || input.Providers == nil {
			return invalid()
		}
		d.Source = "/providers/proxies"
		names := make([]string, 0, len(input.Providers))
		for name := range input.Providers {
			names = append(names, name)
		}
		sort.Strings(names)
		d.Count = len(names)
		result = positive("subscription_metadata", "已读取 mihomo 订阅信息；未提供流量或到期字段的订阅显示为未知，未测试节点连通性。")
		for i, name := range names {
			if !safeName(name) {
				return invalid()
			}
			info := input.Providers[name].Info
			if info != nil && (info.Upload < 0 || info.Download < 0 || info.Total < 0 || info.Expire < 0 || info.Expire > 253402300799) {
				return invalid()
			}
			if info != nil && ((info.Expire > 0 && info.Expire <= now.Unix()) || (info.Total > 0 && (info.Upload >= info.Total || info.Download >= info.Total-info.Upload))) {
				result = failure(StatusUnhealthy, "subscription_unavailable", "订阅已到期或流量已用尽，请检查订阅信息。")
			}
			if i < 200 {
				d.Subscriptions = append(d.Subscriptions, Subscription{Name: name, Info: info})
			}
		}
	case KindOpenAI:
		var input struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(b, &input) != nil || input.Data == nil {
			return invalid()
		}
		d.Source = "/v1/models"
		d.Count = len(input.Data)
		seen := make(map[string]bool)
		for _, model := range input.Data {
			if !safeName(model.ID) || seen[model.ID] {
				return invalid()
			}
			seen[model.ID] = true
			if len(d.Models) < 200 {
				d.Models = append(d.Models, Model{ID: model.ID, Status: "not_tested"})
			}
		}
	case KindStatuspage:
		var input struct {
			Components []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Group  bool   `json:"group"`
			} `json:"components"`
		}
		if json.Unmarshal(b, &input) != nil || input.Components == nil {
			return invalid()
		}
		d.Source = "/api/v2/summary.json"
		result = positive("provider_reported", "已读取公开状态页；状态由服务商报告，未执行模型推理测试。")
		for _, component := range input.Components {
			if component.Group {
				continue
			}
			if !safeName(component.Name) {
				return invalid()
			}
			switch component.Status {
			case "operational":
			case "degraded_performance", "partial_outage", "major_outage", "under_maintenance":
				result = failure(StatusUnhealthy, "provider_reported", "服务商状态页报告降级、故障或维护；详见组件状态。")
			default:
				return invalid()
			}
			d.Count++
			if len(d.Components) < 200 {
				d.Components = append(d.Components, Component{Name: component.Name, Status: component.Status})
			}
		}
		if d.Count == 0 {
			result = failure(StatusUnsupported, "unverified_response", "状态页未提供可判断的服务组件。")
		}
	default:
		return invalid()
	}
	d.Truncated = d.Count > 200
	result.details = d
	return result
}

func reflectedCredential(d *Details, credential string) bool {
	if d == nil || credential == "" {
		return false
	}
	b, err := json.Marshal(d)
	if err != nil {
		return true
	}
	var value any
	if json.Unmarshal(b, &value) != nil {
		return true
	}
	var contains func(any) bool
	contains = func(v any) bool {
		switch x := v.(type) {
		case string:
			return strings.Contains(x, credential)
		case []any:
			for _, item := range x {
				if contains(item) {
					return true
				}
			}
		case map[string]any:
			for key, item := range x {
				if strings.Contains(key, credential) || contains(item) {
					return true
				}
			}
		}
		return false
	}
	return contains(value)
}
