package monitor

import (
	"context"
	"sync"
	"time"
)

const networkSettingsBucket = "network.settings"
const networkSnapshotBucket = "network.snapshot"
const networkUsageBucket = "network.usage"
const networkNodeBucket = "network.nodes"
const networkLogsBucket = "network.logs"
const networkExpiryBucket = "network.expiry"
const networkItemLimit = 5000
const networkNodeIdentityVersion = 3

type NetworkQuery struct {
	Section, Cursor, Q, Subscription, State, Node string
	Limit, Hours                                  int
}
type NetworkSettings struct {
	Enabled               bool                  `json:"enabled"`
	PersistLogs           bool                  `json:"persist_logs"`
	LogRetentionHours     int                   `json:"log_retention_hours"`
	LogLimitMiB           int                   `json:"log_limit_mib"`
	Generation            int64                 `json:"generation"`
	ControlBaseURL        string                `json:"control_base_url"`
	ControlToken          string                `json:"control_token,omitempty"`
	ClearControlToken     bool                  `json:"clear_control_token,omitempty"`
	HasControlToken       bool                  `json:"has_control_token"`
	ImportedSubscriptions []NetworkSubscription `json:"imported_subscriptions,omitempty"`
}
type NetworkImport struct {
	Generation    int64                 `json:"generation"`
	Subscriptions []NetworkSubscription `json:"subscriptions"`
}
type NetworkSubscription struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Source         string        `json:"source"`
	Active         bool          `json:"active"`
	UploadBytes    *int64        `json:"upload_bytes,omitempty"`
	DownloadBytes  *int64        `json:"download_bytes,omitempty"`
	UsedBytes      *int64        `json:"used_bytes,omitempty"`
	RemainingBytes *int64        `json:"remaining_bytes,omitempty"`
	TotalBytes     *int64        `json:"total_bytes,omitempty"`
	ExpireSeconds  *int64        `json:"expire_seconds,omitempty"`
	UpdatedAt      *time.Time    `json:"updated_at,omitempty"`
	NodeCount      int           `json:"node_count"`
	NodeCountKnown bool          `json:"node_count_known"`
	UsageState     string        `json:"usage_state"`
	Nodes          []NetworkNode `json:"nodes,omitempty"`
}
type NetworkNode struct {
	ID               string                    `json:"id"`
	SubscriptionID   string                    `json:"subscription_id,omitempty"`
	SubscriptionName string                    `json:"subscription_name,omitempty"`
	Subscriptions    []NetworkNodeSubscription `json:"subscriptions,omitempty"`
	Name             string                    `json:"name"`
	Type             string                    `json:"type"`
	State            string                    `json:"state"`
	DelayMS          *uint16                   `json:"delay_ms,omitempty"`
	CheckedAt        *time.Time                `json:"checked_at,omitempty"`
	Freshness        string                    `json:"freshness"`
	Loaded           bool                      `json:"loaded"`
	Availability24H  *NetworkAvailability      `json:"availability_24h,omitempty"`
}

// Availability is a ratio of distinct valid source checks, not continuous
// uptime. Empty intervals remain unknown, and never enter the denominator.
type NetworkAvailability struct {
	StartAt          time.Time `json:"start_at"`
	EndAt            time.Time `json:"end_at"`
	Success          int       `json:"success"`
	Failure          int       `json:"failure"`
	SuccessRate      *float64  `json:"success_rate,omitempty"`
	CoveredIntervals int       `json:"covered_intervals"`
	Intervals        []string  `json:"intervals"`
}
type NetworkNodeSubscription struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type NetworkConnection struct {
	ID            string     `json:"id"`
	Network       string     `json:"network"`
	Source        string     `json:"source,omitempty"`
	Destination   string     `json:"destination,omitempty"`
	Host          string     `json:"host,omitempty"`
	Rule          string     `json:"rule,omitempty"`
	Chains        []string   `json:"chains"`
	UploadBytes   int64      `json:"upload_bytes"`
	DownloadBytes int64      `json:"download_bytes"`
	UploadRate    int64      `json:"upload_rate"`
	DownloadRate  int64      `json:"download_rate"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	Process       string     `json:"process,omitempty"`
}
type NetworkLog struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
	NodeID  string    `json:"node_id,omitempty"`
}
type NetworkUsage struct {
	At            time.Time `json:"at"`
	UploadBytes   int64     `json:"upload_bytes"`
	DownloadBytes int64     `json:"download_bytes"`
	Samples       int       `json:"samples"`
	CounterReset  bool      `json:"counter_reset"`
}

// NetworkUsageGroup contains sampled connection deltas, never a second copy of
// the core traffic counter. Client/chain labels are administrator-only data.
type NetworkUsageGroup struct {
	Dimension     string `json:"dimension"`
	Label         string `json:"label"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	Samples       int    `json:"samples"`
}
type NetworkNodeSample struct {
	At        time.Time  `json:"at"`
	State     string     `json:"state"`
	DelayMS   *uint16    `json:"delay_ms,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Freshness string     `json:"freshness"`
}
type NetworkOverview struct {
	SubscriptionCount int    `json:"subscription_count"`
	NodeCount         int    `json:"node_count"`
	HealthyNodes      int    `json:"healthy_nodes"`
	UnhealthyNodes    int    `json:"unhealthy_nodes"`
	UnknownNodes      int    `json:"unknown_nodes"`
	UploadRate        int64  `json:"upload_rate"`
	DownloadRate      int64  `json:"download_rate"`
	UploadTotal       int64  `json:"upload_total"`
	DownloadTotal     int64  `json:"download_total"`
	MemoryBytes       uint64 `json:"memory_bytes"`
	ConnectionCount   int    `json:"connection_count"`
	CoreVersion       string `json:"core_version"`
	ActiveConfig      string `json:"active_config,omitempty"`
	CounterReset      bool   `json:"counter_reset"`
}
type NetworkCoverage struct {
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	LastSampleAt *time.Time        `json:"last_sample_at,omitempty"`
	SourceStates map[string]string `json:"source_states"`
	Truncated    bool              `json:"truncated"`
	Notes        []string          `json:"notes"`
}
type NetworkView struct {
	NodeIdentityVersion int                   `json:"node_identity_version"`
	SettingsRevision    int64                 `json:"settings_revision"`
	ServiceID           string                `json:"service_id"`
	Generation          int64                 `json:"generation"`
	CollectedAt         *time.Time            `json:"collected_at,omitempty"`
	State               string                `json:"state"`
	Message             string                `json:"message"`
	Overview            NetworkOverview       `json:"overview"`
	Settings            NetworkSettings       `json:"settings"`
	Subscriptions       []NetworkSubscription `json:"subscriptions"`
	Nodes               []NetworkNode         `json:"nodes"`
	Connections         []NetworkConnection   `json:"connections"`
	Logs                []NetworkLog          `json:"logs"`
	Usage               []NetworkUsage        `json:"usage"`
	UsageGroups         []NetworkUsageGroup   `json:"usage_groups"`
	UsageGroupStart     *time.Time            `json:"usage_group_start,omitempty"`
	UsageGroupEnd       *time.Time            `json:"usage_group_end,omitempty"`
	NodeHistory         []NetworkNodeSample   `json:"node_history"`
	Total               int                   `json:"total"`
	NextCursor          string                `json:"next_cursor,omitempty"`
	HasMore             bool                  `json:"has_more"`
	Coverage            NetworkCoverage       `json:"coverage"`
}
type networkSettingsRecord struct {
	HistoryGeneration int64           `json:"history_generation"`
	Settings          NetworkSettings `json:"settings"`
	ControlCipher     string          `json:"control_cipher,omitempty"`
	Revision          int64           `json:"revision"`
}
type networkRuntime struct {
	mu      sync.RWMutex
	workers map[string]*networkWorker
}
type networkWorker struct {
	historyGeneration int64
	sensitive         []string
	sourceUpdated     map[string]time.Time
	mu                sync.RWMutex
	pendingBreakdown  map[string]map[string]NetworkUsageGroup
	rec               serviceRecord
	settings          NetworkSettings
	controlCipher     string
	revision          int64
	cancel            context.CancelFunc
	view              NetworkView
	logs              []NetworkLog
	logState          string
	logSequence       uint64
	previous          map[string]NetworkConnection
	lastCounters      *networkCounters
	lastMinute        time.Time
	pendingUsage      NetworkUsage
	lastNodeSamples   map[string]NetworkNodeSample
	pendingNodes      map[string]NetworkNodeSample
	pendingLogs       []NetworkLog
	coreSubs          []NetworkSubscription
	coreNodes         []NetworkNode
	profileSubs       []NetworkSubscription
	profileNodes      []NetworkNode
	profilesFetched   time.Time
	profileCursor     int
}
type networkCounters struct {
	up, down int64
	at       time.Time
}
type networkHourUsage struct {
	Items []NetworkUsage `json:"items"`
}
type networkHourNodes struct {
	Items []NetworkNodeSample `json:"items"`
}
type networkExpiry struct {
	Bucket, Key string
	Prefix      bool
}
