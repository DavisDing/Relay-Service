// Package api exposes the same-origin NAS administration and read-only device API.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"relayservice/internal/access"
	"relayservice/internal/monitor"
	"relayservice/internal/storage"
	"relayservice/internal/webui"
)

type Options struct {
	Version       string
	SecureCookies bool
	AllowedOrigin string
	DataVersion   func() string
}

type Server struct {
	store         storage.Store
	access        *access.Service
	monitor       *monitor.Engine
	options       Options
	mux           *http.ServeMux
	databaseCheck chan struct{}
	serviceTest   chan struct{}
}

type envelope struct {
	APIVersion    string    `json:"api_version"`
	SchemaVersion int       `json:"schema_version"`
	RelayID       string    `json:"relay_id"`
	DataVersion   string    `json:"data_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Data          any       `json:"data"`
}
type errorEnvelope struct {
	Error apiError `json:"error"`
}
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type request struct {
	r *http.Request
	w http.ResponseWriter
	s *Server
}
type route struct {
	public  bool
	admin   bool
	write   bool
	initial bool
	fn      func(request) (any, int, error)
}
type bootstrapPayload struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}
type loginPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type pairPayload struct {
	Code string `json:"code"`
	Name string `json:"name"`
}
type checkPayload struct {
	OperationID string `json:"operation_id"`
	Generation  int64  `json:"generation"`
}
type ackPayload struct {
	Cursor  uint64   `json:"cursor"`
	Cursors []uint64 `json:"cursors,omitempty"`
}
type silencePayload struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
}

// The device projection deliberately omits target connection details and all
// writable monitoring configuration. The API never serializes a secret.
type deviceService struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Kind            string         `json:"kind"`
	UIURL           string         `json:"ui_url,omitempty"`
	Group           string         `json:"group,omitempty"`
	Enabled         bool           `json:"enabled"`
	Current         *monitor.Check `json:"current,omitempty"`
	LastSuccess     *monitor.Check `json:"last_success,omitempty"`
	LastCheckAt     *time.Time     `json:"last_check_at,omitempty"`
	LastSuccessAt   *time.Time     `json:"last_success_at,omitempty"`
	OpenIncident    bool           `json:"open_incident"`
	OpenRestriction bool           `json:"open_restriction"`
	Status          string         `json:"status"`
	Freshness       string         `json:"freshness"`
	EvidenceLevel   string         `json:"evidence_level"`
	InferenceState  string         `json:"inference_state"`
	UpdateState     string         `json:"update_state"`
}

func projectService(s monitor.Service) deviceService {
	return deviceService{ID: s.ID, Name: s.Name, Kind: s.Kind, UIURL: s.UIURL, Group: s.Group, Enabled: s.Enabled, Current: s.Current, LastSuccess: s.LastSuccess, LastCheckAt: s.LastCheckAt, LastSuccessAt: s.LastSuccessAt, OpenIncident: s.OpenIncident, OpenRestriction: s.OpenRestriction, Status: s.Status, Freshness: s.FreshnessState, EvidenceLevel: s.EvidenceLevel, InferenceState: s.InferenceState, UpdateState: s.UpdateState}
}

func New(store storage.Store, accessService *access.Service, monitorEngine *monitor.Engine, options Options) http.Handler {
	s := &Server{store: store, access: accessService, monitor: monitorEngine, options: options, mux: http.NewServeMux(), databaseCheck: make(chan struct{}, 1), serviceTest: make(chan struct{}, 1)}
	s.routes()
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "未找到 API 接口。")
	})
	s.mux.Handle("/", webui.Handler())
	return s.security(s.mux)
}

func (s *Server) routes() {
	s.databaseRoutes()
	s.mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "live"})
	})
	s.mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.Ping(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "not_ready", "Relay 数据库暂不可用。")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
	})
	s.mux.HandleFunc("GET /api/v1/info", func(w http.ResponseWriter, r *http.Request) {
		id, err := s.access.RelayID(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		initialized, err := s.access.Initialized(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		s.success(w, r, id, map[string]any{"capabilities": []string{"service-monitoring", "read-only-devices"}, "version": s.options.Version, "initialized": initialized})
	})
	s.handle("POST /api/v1/auth/bootstrap", route{public: true, write: true, fn: func(q request) (any, int, error) {
		var p bootstrapPayload
		if err := decode(q.r, &p); err != nil {
			return nil, 0, err
		}
		if err := q.s.access.Bootstrap(q.r.Context(), p.Token, p.Username, p.Password); err != nil {
			return nil, 0, err
		}
		return map[string]any{"initialized": true}, http.StatusCreated, nil
	}})
	s.handle("POST /api/v1/auth/login", route{public: true, write: true, fn: func(q request) (any, int, error) {
		var p loginPayload
		if err := decode(q.r, &p); err != nil {
			return nil, 0, err
		}
		token, err := q.s.access.Login(q.r.Context(), p.Username, p.Password)
		if err != nil {
			return nil, 0, err
		}
		csrf := make([]byte, 32)
		if _, err := rand.Read(csrf); err != nil {
			return nil, 0, err
		}
		secure := q.s.options.SecureCookies || q.r.TLS != nil
		http.SetCookie(q.w, &http.Cookie{Name: "relay_session", Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(12 * time.Hour)})
		http.SetCookie(q.w, &http.Cookie{Name: "relay_csrf", Value: hex.EncodeToString(csrf), Path: "/", HttpOnly: false, Secure: secure, SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(12 * time.Hour)})
		return map[string]any{"authenticated": true}, http.StatusOK, nil
	}})
	s.handle("POST /api/v1/auth/password", route{admin: true, write: true, initial: true, fn: func(q request) (any, int, error) {
		var p loginPayload
		if err := decode(q.r, &p); err != nil {
			return nil, 0, err
		}
		if err := q.s.access.CompleteInitialPassword(q.r.Context(), requestCredential(q.r), p.Username, p.Password); err != nil {
			return nil, 0, err
		}
		return map[string]any{"password_changed": true, "login_required": true}, http.StatusOK, nil
	}})
	s.handle("POST /api/v1/auth/logout", route{admin: true, write: true, initial: true, fn: func(q request) (any, int, error) {
		token := ""
		cookie, _ := q.r.Cookie("relay_session")
		if bearerToken(q.r) {
			token = strings.TrimSpace(strings.TrimPrefix(q.r.Header.Get("Authorization"), "Bearer "))
		} else if cookie != nil {
			token = cookie.Value
		}
		if token != "" {
			if err := q.s.access.Logout(q.r.Context(), token); err != nil {
				return nil, 0, err
			}
		}
		http.SetCookie(q.w, &http.Cookie{Name: "relay_session", Value: "", Path: "/", HttpOnly: true, Secure: q.s.options.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		http.SetCookie(q.w, &http.Cookie{Name: "relay_csrf", Value: "", Path: "/", HttpOnly: false, Secure: q.s.options.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		return map[string]any{"authenticated": false}, http.StatusOK, nil
	}})
	s.handle("GET /api/v1/auth/me", route{initial: true, fn: func(q request) (any, int, error) { p, _ := principal(q.r.Context()); return p, http.StatusOK, nil }})
	s.handle("POST /api/v1/devices/pair", route{public: true, write: false, fn: func(q request) (any, int, error) {
		var p pairPayload
		if err := decode(q.r, &p); err != nil {
			return nil, 0, err
		}
		device, err := q.s.access.Pair(q.r.Context(), p.Code, p.Name)
		if err != nil {
			return nil, 0, err
		}
		return device, http.StatusCreated, nil
	}})
	s.handle("POST /api/v1/devices/pairings", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		actor, _ := principal(q.r.Context())
		pair, err := q.s.access.CreatePairing(q.r.Context(), actor.ID)
		if err != nil {
			return nil, 0, err
		}
		return pair, http.StatusCreated, nil
	}})
	s.handle("GET /api/v1/devices", route{admin: true, fn: func(q request) (any, int, error) {
		devices, err := q.s.access.Devices(q.r.Context())
		return devices, http.StatusOK, err
	}})
	s.handle("DELETE /api/v1/devices/{device_id}", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		if err := q.s.access.Revoke(q.r.Context(), q.r.PathValue("device_id")); err != nil {
			return nil, 0, err
		}
		return map[string]any{"revoked": true}, http.StatusOK, nil
	}})
	s.handle("GET /api/v1/services", route{fn: func(q request) (any, int, error) {
		items, err := q.s.monitor.Services(q.r.Context())
		actor, _ := principal(q.r.Context())
		if actor.Role == "device" {
			out := make([]deviceService, 0, len(items))
			for _, item := range items {
				out = append(out, projectService(item))
			}
			return out, http.StatusOK, err
		}
		return items, http.StatusOK, err
	}})
	s.handle("POST /api/v1/services/test", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		var input monitor.ServiceInput
		if err := decode(q.r, &input); err != nil {
			return nil, 0, err
		}
		select {
		case q.s.serviceTest <- struct{}{}:
			defer func() { <-q.s.serviceTest }()
		default:
			return nil, 0, &HTTPError{Status: http.StatusConflict, Code: "service_test_busy", Message: "已有连通测试正在进行，请稍后重试。"}
		}
		result, err := q.s.monitor.Preview(q.r.Context(), input)
		return result, http.StatusOK, err
	}})
	s.handle("POST /api/v1/services", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		var p monitor.ServiceInput
		if err := decode(q.r, &p); err != nil {
			return nil, 0, err
		}
		actor, _ := principal(q.r.Context())
		item, err := q.s.monitor.Save(q.r.Context(), actor.ID, p)
		return item, http.StatusCreated, err
	}})
	s.handle("GET /api/v1/services/{service_id}", route{fn: func(q request) (any, int, error) {
		item, err := q.s.monitor.Get(q.r.Context(), q.r.PathValue("service_id"))
		actor, _ := principal(q.r.Context())
		if actor.Role == "device" {
			return projectService(item), http.StatusOK, err
		}
		return item, http.StatusOK, err
	}})
	s.handle("PUT /api/v1/services/{service_id}", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		var p monitor.ServiceInput
		if err := decode(q.r, &p); err != nil {
			return nil, 0, err
		}
		p.ID = q.r.PathValue("service_id")
		actor, _ := principal(q.r.Context())
		item, err := q.s.monitor.Save(q.r.Context(), actor.ID, p)
		return item, http.StatusOK, err
	}})
	s.handle("DELETE /api/v1/services/{service_id}", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		generation, err := strconv.ParseInt(q.r.URL.Query().Get("generation"), 10, 64)
		if err != nil {
			return nil, 0, badRequest("invalid_generation", "缺少配置版本，请刷新后重试。")
		}
		actor, _ := principal(q.r.Context())
		if err = q.s.monitor.Remove(q.r.Context(), actor.ID, q.r.PathValue("service_id"), generation); err != nil {
			return nil, 0, err
		}
		return map[string]any{"removed": true}, http.StatusOK, nil
	}})
	s.handle("POST /api/v1/services/{service_id}/checks", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		var p checkPayload
		if err := decode(q.r, &p); err != nil {
			return nil, 0, err
		}
		item, err := q.s.monitor.RequestCheck(q.r.Context(), q.r.PathValue("service_id"), p.OperationID, p.Generation)
		return item, http.StatusAccepted, err
	}})
	s.handle("GET /api/v1/services/{service_id}/checks", route{fn: func(q request) (any, int, error) {
		limit, err := parseLimit(q.r)
		if err != nil {
			return nil, 0, err
		}
		items, err := q.s.monitor.History(q.r.Context(), q.r.PathValue("service_id"), q.r.URL.Query().Get("cursor"), limit)
		return map[string]any{"items": items}, http.StatusOK, err
	}})
	s.handle("GET /api/v1/services/{service_id}/checks/{check_id}", route{fn: func(q request) (any, int, error) {
		item, err := q.s.monitor.Check(q.r.Context(), q.r.PathValue("service_id"), q.r.PathValue("check_id"))
		return item, http.StatusOK, err
	}})
	s.handle("GET /api/v1/alerts", route{fn: func(q request) (any, int, error) {
		limit, err := parseLimit(q.r)
		if err != nil {
			return nil, 0, err
		}
		items, err := q.s.monitor.Alerts(q.r.Context(), q.r.URL.Query().Get("cursor"), limit)
		if err != nil {
			return nil, 0, err
		}
		actor, _ := principal(q.r.Context())
		ack, err := q.s.monitor.AckCursor(q.r.Context(), actor.ID)
		next := q.r.URL.Query().Get("cursor")
		if len(items) > 0 {
			next = strconv.FormatUint(items[len(items)-1].Cursor, 10)
		}
		return map[string]any{"items": items, "ack_cursor": ack, "next_cursor": next, "has_more": len(items) == limit}, http.StatusOK, err
	}})
	s.handle("POST /api/v1/alerts/ack", route{write: true, fn: func(q request) (any, int, error) {
		var p ackPayload
		if err := decode(q.r, &p); err != nil {
			return nil, 0, err
		}
		actor, _ := principal(q.r.Context())
		if err := q.s.monitor.Ack(q.r.Context(), actor.ID, p.Cursor, p.Cursors...); err != nil {
			return nil, 0, err
		}
		return map[string]any{"acknowledged": true}, http.StatusOK, nil
	}})
	s.handle("POST /api/v1/services/{service_id}/silence", route{admin: true, write: true, fn: func(q request) (any, int, error) {
		var p silencePayload
		if err := decode(q.r, &p); err != nil {
			return nil, 0, err
		}
		actor, _ := principal(q.r.Context())
		if err := q.s.monitor.Silence(q.r.Context(), actor.ID, q.r.PathValue("service_id"), p.Until, p.Reason); err != nil {
			return nil, 0, err
		}
		return map[string]any{"silenced_until": p.Until}, http.StatusOK, nil
	}})
	s.handle("GET /api/v1/diagnostic", route{admin: true, fn: func(q request) (any, int, error) {
		out, err := q.s.monitor.Diagnostic(q.r.Context())
		return out, http.StatusOK, err
	}})
	s.retentionRoutes()
}

func (s *Server) handle(pattern string, rt route) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if rt.write {
			sessionCookie, cookieErr := r.Cookie("relay_session")
			cookieAuth := cookieErr == nil && sessionCookie.Value != ""
			if !s.sameOrigin(r) && (!bearerToken(r) || cookieAuth) {
				writeError(w, http.StatusForbidden, "origin_rejected", "访问来源不受信任，请核对 Relay 地址配置。")
				return
			}
			if cookieAuth && !validCSRF(r) {
				writeError(w, http.StatusForbidden, "csrf_rejected", "安全校验失败，请刷新页面后重试。")
				return
			}
		}
		var p access.Principal
		var err error
		credential := ""
		if bearerToken(r) {
			credential = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		} else if c, e := r.Cookie("relay_session"); e == nil {
			credential = c.Value
		}
		if credential != "" {
			p, err = s.access.Authenticate(r.Context(), credential)
		} else {
			err = errors.New("unauthorized")
		}
		if err != nil && !rt.public {
			if credential == "" || errors.Is(err, access.ErrUnauthorized) || errors.Is(err, access.ErrExpired) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录。")
			} else {
				s.fail(w, err)
			}
			return
		}
		if rt.admin && p.Role != "admin" {
			writeError(w, http.StatusForbidden, "forbidden", "需要管理员权限。")
			return
		}
		if p.MustChangePassword && !rt.initial && !rt.public {
			writeError(w, http.StatusForbidden, "password_change_required", "请先设置正式密码。")
			return
		}
		ctx := context.WithValue(r.Context(), principalKey{}, p)
		data, status, err := rt.fn(request{r: r.WithContext(ctx), w: w, s: s})
		if err != nil {
			s.fail(w, err)
			return
		}
		id, err := s.access.RelayID(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		s.successStatus(w, r, id, data, status)
	})
}

func requestCredential(r *http.Request) string {
	if bearerToken(r) {
		return strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	if cookie, err := r.Cookie("relay_session"); err == nil {
		return cookie.Value
	}
	return ""
}

type principalKey struct{}

func principal(ctx context.Context) (access.Principal, bool) {
	v, ok := ctx.Value(principalKey{}).(access.Principal)
	return v, ok
}
func (s *Server) sameOrigin(r *http.Request) bool {
	origin, ok := parseOrigin(r.Header.Get("Origin"))
	if !ok {
		return false
	}
	expected := s.options.AllowedOrigin
	if expected == "" {
		expected = r.Host
		scheme := "http"
		if r.TLS != nil || s.options.SecureCookies {
			scheme = "https"
		}
		expected = scheme + "://" + expected
	}
	expectedOrigin, valid := parseOrigin(expected)
	return valid && origin.scheme == expectedOrigin.scheme && strings.EqualFold(origin.host, expectedOrigin.host)
}

type origin struct{ scheme, host string }

func parseOrigin(raw string) (origin, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return origin{}, false
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return origin{scheme: strings.ToLower(u.Scheme), host: strings.ToLower(u.Hostname()) + ":" + port}, true
}

func validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie("relay_csrf")
	if err != nil || cookie.Value == "" {
		return false
	}
	header := r.Header.Get("X-Relay-CSRF")
	if header == "" || len(header) != len(cookie.Value) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) == 1
}
func (s *Server) success(w http.ResponseWriter, r *http.Request, relayID string, data any) {
	s.successStatus(w, r, relayID, data, http.StatusOK)
}
func (s *Server) successStatus(w http.ResponseWriter, r *http.Request, relayID string, data any, status int) {
	version := "unavailable"
	if s.options.DataVersion != nil {
		version = s.options.DataVersion()
	} else if versioned, ok := s.store.(interface {
		DataVersion(context.Context) (uint64, error)
	}); ok {
		value, err := versioned.DataVersion(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		version = strconv.FormatUint(value, 10)
	}
	writeJSON(w, status, envelope{APIVersion: "v1", SchemaVersion: 1, RelayID: relayID, DataVersion: version, GeneratedAt: time.Now().UTC(), Data: data})
}
func (s *Server) fail(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "internal_error", "操作未能完成，请稍后重试或查看服务日志。"
	var apiErr *HTTPError
	if errors.As(err, &apiErr) {
		status, code, message = apiErr.Status, apiErr.Code, apiErr.Message
	}
	switch {
	case errors.Is(err, access.ErrUnauthorized), errors.Is(err, access.ErrExpired):
		status, code, message = http.StatusUnauthorized, "unauthorized", "账号、密码无效或登录已过期。"
	case errors.Is(err, access.ErrInvalidInput), errors.Is(err, monitor.ErrValidation):
		status, code, message = http.StatusBadRequest, "invalid_request", "输入无效，请检查名称、服务地址、检查间隔和凭据。"
	case errors.Is(err, access.ErrAlreadyInitialized), errors.Is(err, monitor.ErrConflict):
		status, code, message = http.StatusConflict, "conflict", "配置已变更，请刷新后重试。"
	case errors.Is(err, access.ErrBootstrapDisabled):
		status, code, message = http.StatusForbidden, "bootstrap_disabled", "管理员初始化入口已关闭。"
	case errors.Is(err, access.ErrRateLimited):
		status, code, message = http.StatusTooManyRequests, "rate_limited", "尝试次数过多，请稍后重试。"
		w.Header().Set("Retry-After", "300")
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = http.StatusServiceUnavailable, "request_timeout", "请求超时，请稍后重试。"
	case errors.Is(err, monitor.ErrPaused):
		status, code, message = http.StatusConflict, "paused", "服务已暂停，请先恢复检查。"
	}
	if errors.Is(err, storage.ErrNotFound) {
		status, code, message = http.StatusNotFound, "not_found", "未找到对应记录。"
	}
	writeError(w, status, code, message)
}

type HTTPError struct {
	Status  int
	Code    string
	Message string
}

func (e *HTTPError) Error() string { return e.Code + ": " + e.Message }
func badRequest(code, message string) error {
	return &HTTPError{Status: http.StatusBadRequest, Code: code, Message: message}
}
func decode(r *http.Request, dst any) error {
	if value := r.Header.Get("Content-Type"); value != "" {
		mediaType, _, err := mime.ParseMediaType(value)
		if err != nil || mediaType != "application/json" {
			return &HTTPError{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "请使用 JSON 格式提交。"}
		}
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			return &HTTPError{Status: http.StatusRequestEntityTooLarge, Code: "body_too_large", Message: "请求内容不能超过 64 KiB。"}
		}
		return badRequest("invalid_request", "请求内容无效。")
	}
	if value := strings.TrimSpace(string(body)); value == "" || value[0] != '{' {
		return badRequest("invalid_request", "请求内容必须是 JSON 对象。")
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return badRequest("invalid_request", "请求内容无效。")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return badRequest("invalid_request", "请求内容只能包含一个 JSON 对象。")
	}
	return nil
}
func parseLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 100, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 500 {
		return 0, badRequest("invalid_limit", "读取数量应在 1 到 500 之间。")
	}
	return n, nil
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func bearerToken(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) != ""
}
func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/health/ready" {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}
