package api

import (
	"net/http"
	"strconv"

	"relayservice/internal/monitor"
)

// Network reads only Relay's shared collector cache. A browser refresh never
// forwards arbitrary paths, starts a health check or opens an upstream stream.
func (s *Server) networkRoutes() {
	s.handle("GET /api/v1/network/{service_id}", route{fn: func(q request) (any, int, error) {
		values := q.r.URL.Query()
		section := values.Get("section")
		if section == "" {
			section = "overview"
		}
		switch section {
		case "overview", "usage", "subscriptions", "nodes", "connections", "logs":
		default:
			return nil, 0, badRequest("invalid_request", "未知的网络代理页面。")
		}
		actor, _ := principal(q.r.Context())
		if actor.Role != "admin" && (section == "connections" || section == "logs") {
			return nil, 0, &HTTPError{Status: http.StatusForbidden, Code: "forbidden", Message: "连接明细与核心日志仅管理员可读。"}
		}
		limit, err := parseLimit(q.r)
		if err != nil {
			return nil, 0, err
		}
		if limit > 200 {
			limit = 200
		}
		hours := 24
		if raw := values.Get("hours"); raw != "" {
			hours, err = strconv.Atoi(raw)
			if err != nil || (hours != 1 && hours != 24 && hours != 168) {
				return nil, 0, badRequest("invalid_request", "时间范围应为1小时、24小时或7天。")
			}
		}
		if len(values.Get("q")) > 200 || len(values.Get("cursor")) > 2048 || len(values.Get("subscription")) > 256 || len(values.Get("state")) > 64 || len(values.Get("node_id")) > 256 {
			return nil, 0, badRequest("invalid_request", "筛选条件过长。")
		}
		nodeID := values.Get("node_id")
		if nodeID == "" {
			nodeID = values.Get("node")
		}
		if len(nodeID) > 256 {
			return nil, 0, badRequest("invalid_request", "节点标识过长。")
		}
		view, err := s.monitor.NetworkView(q.r.Context(), q.r.PathValue("service_id"), monitor.NetworkQuery{
			Section: section, Cursor: values.Get("cursor"), Limit: limit, Hours: hours,
			Q: values.Get("q"), Subscription: values.Get("subscription"), State: values.Get("state"), Node: nodeID,
		})
		if actor.Role != "admin" {
			// A device can read safe monitoring evidence, not upstream addresses,
			// credential-presence flags or writable collection configuration.
			view.Settings = monitor.NetworkSettings{Enabled: view.Settings.Enabled, Generation: view.Generation}
			view.Connections = nil
			view.Logs = nil
			view.UsageGroups = nil
		}
		return view, http.StatusOK, err
	}})
	s.handle("GET /api/v1/network/{service_id}/settings", route{admin: true, fn: func(q request) (any, int, error) {
		settings, err := s.monitor.NetworkSettings(q.r.Context(), q.r.PathValue("service_id"))
		return settings, http.StatusOK, err
	}})
	s.handle("PUT /api/v1/network/{service_id}/settings", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		var input monitor.NetworkSettings
		if err := decode(q.r, &input); err != nil {
			return nil, 0, err
		}
		actor, _ := principal(q.r.Context())
		settings, err := s.monitor.UpdateNetworkSettings(q.r.Context(), q.r.PathValue("service_id"), input, actor.ID)
		return settings, http.StatusOK, err
	}})
	s.handle("POST /api/v1/network/{service_id}/subscriptions/import", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		var input monitor.NetworkImport
		if err := decode(q.r, &input); err != nil {
			return nil, 0, err
		}
		actor, _ := principal(q.r.Context())
		settings, err := s.monitor.ImportNetworkSubscriptions(q.r.Context(), q.r.PathValue("service_id"), input, actor.ID)
		return settings, http.StatusOK, err
	}})
}
