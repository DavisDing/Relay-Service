export type NetworkSection = 'overview' | 'usage' | 'subscriptions' | 'nodes' | 'connections' | 'logs';
export type NetworkNode = {
  id: string; subscription_id?: string; subscription_name?: string; subscriptions?: { id: string; name: string }[]; name: string; type: string; state: string;
  delay_ms?: number; checked_at?: string; freshness: string; loaded: boolean;
};
export type NetworkSubscription = {
  id: string; name: string; source: 'provider' | 'import' | 'control'; active: boolean;
  upload_bytes?: number; download_bytes?: number; used_bytes?: number; remaining_bytes?: number;
  total_bytes?: number; expire_seconds?: number; updated_at?: string; node_count: number; node_count_known: boolean;
  usage_state: string; nodes?: NetworkNode[];
};
export type NetworkSettings = {
  enabled: boolean; persist_logs: boolean; log_retention_hours: number; log_limit_mib: number;
  generation: number; control_base_url: string; has_control_token: boolean; control_token?: string; clear_control_token?: boolean;
  imported_subscriptions?: NetworkSubscription[];
};
export type NetworkConnection = {
  id: string; network: string; source?: string; destination?: string; host?: string;
  rule?: string; chains: string[]; upload_bytes: number; download_bytes: number;
  upload_rate: number; download_rate: number; started_at?: string; process?: string;
};
export type NetworkLog = { id: string; at: string; level: string; kind: string; message: string; node_id?: string };
export type NetworkUsage = { at: string; upload_bytes: number; download_bytes: number; samples: number; counter_reset: boolean };
export type NetworkUsageGroup = { dimension: 'client' | 'rule' | 'chain'; label: string; upload_bytes: number; download_bytes: number; samples: number };
export type NetworkNodeSample = { at: string; state: string; delay_ms?: number; checked_at?: string; freshness: string };
export type NetworkCoverage = {
  started_at?: string; last_sample_at?: string; source_states: Record<string, string>;
  truncated: boolean; notes: string[];
};
export type NetworkView = {
  service_id: string; generation: number; settings_revision: number; collected_at?: string; state: string; message: string;
  overview: {
    subscription_count: number; node_count: number; healthy_nodes: number; unhealthy_nodes: number; unknown_nodes: number;
    upload_rate?: number; download_rate?: number; upload_total?: number; download_total?: number;
    memory_bytes?: number; connection_count?: number; core_version?: string; active_config?: string; counter_reset: boolean;
  };
  settings: NetworkSettings; subscriptions: NetworkSubscription[]; nodes: NetworkNode[];
  connections: NetworkConnection[]; logs: NetworkLog[]; usage: NetworkUsage[]; usage_groups: NetworkUsageGroup[];
  usage_group_start?: string; usage_group_end?: string;
  node_history: NetworkNodeSample[]; total: number; next_cursor?: string; has_more: boolean; coverage: NetworkCoverage;
};
