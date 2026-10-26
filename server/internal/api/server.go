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
	store   storage.Store
	access  *access.Service
	monitor *monitor.Engine
	options Options
	mux     *http.ServeMux
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
	public bool
	admin  bool
	write  bool
	fn     func(request) (any, int, error)
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
	Cursor uint64 `json:"cursor"`
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
	s := &Server{store: store, access: accessService, monitor: monitorEngine, options: options, mux: http.NewServeMux()}
	s.routes()
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "API route was not found")
	})
	s.mux.Handle("/", webui.Handler())
	return s.security(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "live"})
	})
	s.mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.Ping(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "not_ready", "Relay database is unavailable")
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
		http.SetCookie(q.w, &http.Cookie{Name: "relay_session", Value: token, Path: "/", HttpOnly: true, Secure: q.s.options.SecureCookies, SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(12 * time.Hour)})
		http.SetCookie(q.w, &http.Cookie{Name: "relay_csrf", Value: hex.EncodeToString(csrf), Path: "/", HttpOnly: false, Secure: q.s.options.SecureCookies, SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(12 * time.Hour)})
		return map[string]any{"authenticated": true}, http.StatusOK, nil
	}})
	s.handle("POST /api/v1/auth/logout", route{admin: true, write: true, fn: func(q request) (any, int, error) {
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
	s.handle("GET /api/v1/auth/me", route{fn: func(q request) (any, int, error) { p, _ := principal(q.r.Context()); return p, http.StatusOK, nil }})
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
			return nil, 0, badRequest("invalid_generation", "generation is required")
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
		if err := q.s.monitor.Ack(q.r.Context(), actor.ID, p.Cursor); err != nil {
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
}

func (s *Server) handle(pattern string, rt route) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if rt.write {
			sessionCookie, cookieErr := r.Cookie("relay_session")
			cookieAuth := cookieErr == nil && sessionCookie.Value != ""
			if !s.sameOrigin(r) && (!bearerToken(r) || cookieAuth) {
				writeError(w, http.StatusForbidden, "origin_rejected", "Write request origin is not allowed")
				return
			}
			if cookieAuth && !validCSRF(r) {
				writeError(w, http.StatusForbidden, "csrf_rejected", "Cookie-authenticated writes require a valid CSRF token")
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
				writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
			} else {
				s.fail(w, err)
			}
			return
		}
		if rt.admin && p.Role != "admin" {
			writeError(w, http.StatusForbidden, "forbidden", "Administrator access required")
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
		if r.TLS != nil {
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
	status, code, message := http.StatusInternalServerError, "internal_error", "Request could not be completed"
	var apiErr *HTTPError
	if errors.As(err, &apiErr) {
		status, code, message = apiErr.Status, apiErr.Code, apiErr.Message
	}
	switch {
	case errors.Is(err, access.ErrUnauthorized), errors.Is(err, access.ErrExpired):
		status, code, message = http.StatusUnauthorized, "unauthorized", "Credentials are invalid or expired"
	case errors.Is(err, access.ErrInvalidInput), errors.Is(err, monitor.ErrValidation):
		status, code, message = http.StatusBadRequest, "invalid_request", "Input values are invalid"
	case errors.Is(err, access.ErrAlreadyInitialized), errors.Is(err, monitor.ErrConflict):
		status, code, message = http.StatusConflict, "conflict", "Resource state changed; refresh and try again"
	case errors.Is(err, access.ErrBootstrapDisabled):
		status, code, message = http.StatusForbidden, "bootstrap_disabled", "Administrator initialization is disabled"
	case errors.Is(err, access.ErrRateLimited):
		status, code, message = http.StatusTooManyRequests, "rate_limited", "Too many attempts; try again later"
		w.Header().Set("Retry-After", "300")
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = http.StatusServiceUnavailable, "request_timeout", "Request timed out; retry later"
	case errors.Is(err, monitor.ErrPaused):
		status, code, message = http.StatusConflict, "paused", "Monitoring is paused"
	}
	if errors.Is(err, storage.ErrNotFound) {
		status, code, message = http.StatusNotFound, "not_found", "Requested record was not found"
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
			return &HTTPError{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "Use application/json"}
		}
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			return &HTTPError{Status: http.StatusRequestEntityTooLarge, Code: "body_too_large", Message: "Request body exceeds 64 KiB"}
		}
		return badRequest("invalid_request", "Request body is invalid")
	}
	if value := strings.TrimSpace(string(body)); value == "" || value[0] != '{' {
		return badRequest("invalid_request", "Request body must be a JSON object")
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return badRequest("invalid_request", "Request body is invalid")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return badRequest("invalid_request", "Request body must contain one JSON value")
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
		return 0, badRequest("invalid_limit", "limit must be between 1 and 500")
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
