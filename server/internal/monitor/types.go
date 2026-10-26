// Package monitor records and runs low-impact checks against configured services.
package monitor

import "time"

const (
	KindLiteLLM       = "litellm"
	KindHermes        = "hermes"
	KindWorkbuddy2API = "workbuddy2api"
	KindOpenWebUI     = "openwebui"
	KindOllama        = "ollama"
	KindMetaCubeXD    = "metacubexd"
	KindHTTP          = "http"
	KindTCP           = "tcp"

	StatusUnknown      = "unknown"
	StatusHealthy      = "healthy"
	StatusUnhealthy    = "unhealthy"
	StatusUnauthorized = "unauthorized"
	StatusUnsupported  = "unsupported"
	StatusQueued       = "queued"
	StatusRunning      = "running"
	StatusInterrupted  = "interrupted"
)

type ServiceInput struct {
	ID                string `json:"id,omitempty"`
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	BaseURL           string `json:"base_url"`
	UIURL             string `json:"ui_url,omitempty"`
	Group             string `json:"group,omitempty"`
	Enabled           bool   `json:"enabled"`
	IntervalSeconds   int    `json:"interval_seconds"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
	FailureThreshold  int    `json:"failure_threshold"`
	RecoveryThreshold int    `json:"recovery_threshold"`
	AllowPrivate      bool   `json:"allow_private"`
	Token             string `json:"token,omitempty"`
	ClearToken        bool   `json:"clear_token,omitempty"`
	Generation        int64  `json:"generation"`
}

type Service struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	Kind                 string     `json:"kind"`
	BaseURL              string     `json:"base_url"`
	UIURL                string     `json:"ui_url,omitempty"`
	Group                string     `json:"group,omitempty"`
	Enabled              bool       `json:"enabled"`
	IntervalSeconds      int        `json:"interval_seconds"`
	TimeoutSeconds       int        `json:"timeout_seconds"`
	FailureThreshold     int        `json:"failure_threshold"`
	RecoveryThreshold    int        `json:"recovery_threshold"`
	AllowPrivate         bool       `json:"allow_private"`
	Generation           int64      `json:"generation"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	Tombstone            bool       `json:"tombstone"`
	TokenCiphertext      string     `json:"-"`
	CredentialConfigured bool       `json:"credential_configured"`
	Current              *Check     `json:"current,omitempty"`
	LastSuccess          *Check     `json:"last_success,omitempty"`
	LastCheckAt          *time.Time `json:"last_check_at,omitempty"`
	LastSuccessAt        *time.Time `json:"last_success_at,omitempty"`
	FailureCount         int        `json:"failure_count"`
	RecoveryCount        int        `json:"recovery_count"`
	OpenIncident         bool       `json:"open_incident"`
	OpenRestriction      bool       `json:"open_restriction"`
	Status               string     `json:"status"`
	FreshnessState       string     `json:"freshness"`
	EvidenceLevel        string     `json:"evidence_level"`
	InferenceState       string     `json:"inference_state"`
	UpdateState          string     `json:"update_state"`
}

type Check struct {
	ID             string    `json:"id"`
	Cursor         string    `json:"cursor"`
	ServiceID      string    `json:"service_id"`
	Kind           string    `json:"kind"`
	Generation     int64     `json:"generation"`
	AdapterVersion string    `json:"adapter_version"`
	Status         string    `json:"status"`
	QueuedAt       time.Time `json:"queued_at"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	DurationMS     int64     `json:"duration_ms,omitempty"`
	Class          string    `json:"class,omitempty"`
	Message        string    `json:"message,omitempty"`
	Success        bool      `json:"success"`
}

type Alert struct {
	Cursor       uint64     `json:"cursor"`
	ID           string     `json:"id"`
	ServiceID    string     `json:"service_id"`
	CheckID      string     `json:"check_id"`
	Generation   int64      `json:"generation"`
	IncidentID   string     `json:"incident_id"`
	Kind         string     `json:"kind"`
	Severity     string     `json:"severity"`
	Message      string     `json:"message"`
	StartedAt    time.Time  `json:"started_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	Count        int        `json:"count"`
	Silenced     bool       `json:"silenced"`
	SilenceUntil *time.Time `json:"silence_until,omitempty"`
}

type Cryptor interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}

type state struct {
	Current          *Check     `json:"current,omitempty"`
	LastSuccess      *Check     `json:"last_success,omitempty"`
	LastCheckAt      *time.Time `json:"last_check_at,omitempty"`
	LastSuccessAt    *time.Time `json:"last_success_at,omitempty"`
	FailureCount     int        `json:"failure_count"`
	RecoveryCount    int        `json:"recovery_count"`
	OpenIncident     bool       `json:"open_incident"`
	IncidentID       string     `json:"incident_id,omitempty"`
	OpenRestriction  bool       `json:"open_restriction"`
	RestrictionCount int        `json:"restriction_count"`
	RestrictionID    string     `json:"restriction_id,omitempty"`
}

type pending struct {
	Check Check `json:"check"`
}

type silence struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
}
