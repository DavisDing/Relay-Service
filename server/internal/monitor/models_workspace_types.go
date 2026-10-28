package monitor

import "time"

// ModelsWorkspace is a projection of the latest current-generation checks.
// It never accumulates overlapping gateway log windows or initiates inference.
type ModelsWorkspaceQuery struct {
	Section, Service, Q, State, Cursor string
	Limit                              int
}

type ModelsWorkspaceView struct {
	CollectedAt  time.Time                `json:"collected_at"`
	Sources      []ModelsWorkspaceSource  `json:"sources"`
	Summary      ModelsWorkspaceSummary   `json:"summary"`
	Models       []ModelsWorkspaceModel   `json:"models"`
	Channels     []ModelsWorkspaceChannel `json:"channels"`
	Traffic      []ModelsWorkspaceTraffic `json:"traffic"`
	PublicStatus []ModelsWorkspaceStatus  `json:"public_status"`
	Probes       ModelsWorkspaceProbes    `json:"probes"`
	Coverage     ModelsWorkspaceCoverage  `json:"coverage"`
	Total        int                      `json:"total"`
	NextCursor   string                   `json:"next_cursor"`
	HasMore      bool                     `json:"has_more"`
}
type ModelsWorkspaceSummary struct {
	Services             int `json:"services"`
	Models               int `json:"models"`
	Deployments          int `json:"deployments"`
	Channels             int `json:"channels"`
	TrafficGroups        int `json:"traffic_groups"`
	PublicStatusServices int `json:"public_status_services"`
}
type ModelsWorkspaceSource struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Kind             string         `json:"kind"`
	Enabled          bool           `json:"enabled"`
	Generation       int64          `json:"generation"`
	Status           string         `json:"status"`
	Freshness        string         `json:"freshness"`
	CollectedAt      *time.Time     `json:"collected_at,omitempty"`
	CatalogState     string         `json:"catalog_state"`
	DeploymentsState string         `json:"deployments_state"`
	LogsState        string         `json:"logs_state"`
	ModelCount       int            `json:"model_count"`
	DeploymentCount  int            `json:"deployment_count"`
	WindowStart      *time.Time     `json:"window_start,omitempty"`
	WindowEnd        *time.Time     `json:"window_end,omitempty"`
	LatestAt         *time.Time     `json:"latest_at,omitempty"`
	Rows             int            `json:"rows"`
	Total            int            `json:"total"`
	Skipped          int            `json:"skipped"`
	Sampled          bool           `json:"sampled"`
	Truncated        bool           `json:"truncated"`
	Issues           []GatewayIssue `json:"issues"`
}
type ModelsWorkspaceCounts struct {
	Success   int        `json:"success"`
	Failure   int        `json:"failure"`
	Unknown   int        `json:"unknown"`
	CacheHits int        `json:"cache_hits"`
	LastAt    *time.Time `json:"last_at,omitempty"`
}
type ModelsWorkspaceModel struct {
	ID             string                `json:"id"`
	ServiceID      string                `json:"service_id"`
	Name           string                `json:"name"`
	CatalogVisible bool                  `json:"catalog_visible"`
	CatalogState   string                `json:"catalog_state"`
	InferenceState string                `json:"inference_state"`
	Deployments    []GatewayDeployment   `json:"deployments"`
	Traffic        ModelsWorkspaceCounts `json:"traffic"`
	Freshness      string                `json:"freshness"`
	CollectedAt    *time.Time            `json:"collected_at,omitempty"`
}
type ModelsWorkspaceChannel struct {
	ID             string                `json:"id"`
	ServiceID      string                `json:"service_id"`
	Model          string                `json:"model"`
	DeploymentID   string                `json:"deployment_id,omitempty"`
	UpstreamModel  string                `json:"upstream_model,omitempty"`
	Provider       string                `json:"provider,omitempty"`
	Channel        string                `json:"channel,omitempty"`
	InferenceState string                `json:"inference_state"`
	Traffic        ModelsWorkspaceCounts `json:"traffic"`
	Freshness      string                `json:"freshness"`
	CollectedAt    *time.Time            `json:"collected_at,omitempty"`
}
type ModelsWorkspaceTraffic struct {
	ID             string    `json:"id"`
	ServiceID      string    `json:"service_id"`
	Model          string    `json:"model"`
	DeploymentID   string    `json:"deployment_id,omitempty"`
	Channel        string    `json:"channel,omitempty"`
	Success        int       `json:"success"`
	Failure        int       `json:"failure"`
	Unknown        int       `json:"unknown"`
	CacheHits      int       `json:"cache_hits"`
	LastAt         time.Time `json:"last_at"`
	InferenceState string    `json:"inference_state"`
	WindowStart    time.Time `json:"window_start"`
	WindowEnd      time.Time `json:"window_end"`
	Sampled        bool      `json:"sampled"`
	Freshness      string    `json:"freshness"`
}
type ModelsWorkspaceStatus struct {
	ServiceID   string                  `json:"service_id"`
	Name        string                  `json:"name"`
	Source      string                  `json:"source"`
	State       string                  `json:"state"`
	Freshness   string                  `json:"freshness"`
	CollectedAt *time.Time              `json:"collected_at,omitempty"`
	ObservedAt  *time.Time              `json:"observed_at,omitempty"`
	UpdatedAt   *time.Time              `json:"updated_at,omitempty"`
	Count       int                     `json:"count"`
	Truncated   bool                    `json:"truncated"`
	Components  []PublicStatusComponent `json:"components"`
	Unknown     bool                    `json:"unknown"`
	Unhealthy   bool                    `json:"unhealthy"`
}
type ModelsWorkspaceProbes struct {
	State   string `json:"state"`
	Message string `json:"message"`
}
type ModelsWorkspaceCoverage struct {
	Truncated bool     `json:"truncated"`
	Notes     []string `json:"notes"`
}
