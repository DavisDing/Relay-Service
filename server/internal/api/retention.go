package api

import (
	"net/http"

	"relayservice/internal/monitor"
)

func (s *Server) retentionRoutes() {
	s.handle("GET /api/v1/retention", route{admin: true, fn: func(q request) (any, int, error) {
		policy, err := s.monitor.Retention(q.r.Context())
		return policy, http.StatusOK, err
	}})
	s.handle("POST /api/v1/retention/preview", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		var policy monitor.RetentionPolicy
		if err := decode(q.r, &policy); err != nil {
			return nil, 0, err
		}
		actor, _ := principal(q.r.Context())
		preview, err := s.monitor.PreviewRetention(q.r.Context(), actor.ID, policy)
		return preview, http.StatusOK, err
	}})
	s.handle("PUT /api/v1/retention", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		var input struct {
			PreviewID string `json:"preview_id"`
		}
		if err := decode(q.r, &input); err != nil {
			return nil, 0, err
		}
		actor, _ := principal(q.r.Context())
		policy, err := s.monitor.ConfirmRetention(q.r.Context(), actor.ID, input.PreviewID)
		return policy, http.StatusOK, err
	}})
}
