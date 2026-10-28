package api

import (
	"net/http"

	"relayservice/internal/storage"
)

func (s *Server) databaseRoutes() {
	s.handle("GET /api/v1/database", route{admin: true, fn: func(q request) (any, int, error) {
		backend := "unknown"
		switch q.s.store.(type) {
		case *storage.SQLite:
			backend = "sqlite"
		case *storage.Postgres:
			backend = "postgresql"
		}
		return map[string]any{"backend": backend, "page_migration_available": false}, http.StatusOK, nil
	}})
	s.handle("POST /api/v1/database/postgres/preflight", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		var input storage.PostgresConnection
		if err := decode(q.r, &input); err != nil {
			return nil, 0, err
		}
		// One explicit check at a time; no scheduled probes or idle pools.
		select {
		case q.s.databaseCheck <- struct{}{}:
			defer func() { <-q.s.databaseCheck }()
		default:
			return nil, 0, &HTTPError{Status: http.StatusConflict, Code: "database_check_busy", Message: "已有数据库检查正在进行，请稍后重试。"}
		}
		return storage.PreflightPostgres(q.r.Context(), input), http.StatusOK, nil
	}})
}
