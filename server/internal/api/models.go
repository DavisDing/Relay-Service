package api

import (
	"net/http"
	"strconv"

	"relayservice/internal/monitor"
)

// This endpoint reads persisted monitoring evidence; refreshing the view does
// not contact a gateway or run inference.
func (s *Server) modelsRoutes() {
	s.handle("GET /api/v1/models", route{fn: func(q request) (any, int, error) {
		values := q.r.URL.Query()
		section := values.Get("section")
		if section == "" {
			section = "overview"
		}
		switch section {
		case "overview", "models", "channels", "traffic", "status", "probes":
		default:
			return nil, 0, badRequest("invalid_request", "未知的大模型页面。")
		}
		limit := 50
		if raw := values.Get("limit"); raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 200 {
				return nil, 0, badRequest("invalid_limit", "读取数量应在 1 到 200 之间。")
			}
		}
		if len(values.Get("service")) > 128 || len(values.Get("q")) > 200 || len(values.Get("state")) > 64 || len(values.Get("cursor")) > 2048 {
			return nil, 0, badRequest("invalid_request", "筛选条件过长。")
		}
		view, err := s.monitor.ModelsWorkspace(q.r.Context(), monitor.ModelsWorkspaceQuery{
			Section: section, Service: values.Get("service"), Q: values.Get("q"),
			State: values.Get("state"), Cursor: values.Get("cursor"), Limit: limit,
		})
		return view, http.StatusOK, err
	}})
}
