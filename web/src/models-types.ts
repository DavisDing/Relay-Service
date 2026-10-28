import type { GatewayDetails } from './types';

export type ModelsSection = 'overview' | 'models' | 'channels' | 'traffic' | 'status' | 'probes';
export type ModelsSample = { success: number; failure: number; unknown: number; cache_hits: number; last_at?: string };
export type ModelsSource = {
  id: string; name: string; kind: string; enabled: boolean; generation: number;
  status: string; freshness: string; collected_at?: string;
  catalog_state: string; deployments_state: string; logs_state: string;
  model_count: number; deployment_count: number;
  window_start?: string; window_end?: string; latest_at?: string;
  rows: number; total: number; skipped: number; sampled: boolean; truncated: boolean;
  issues?: { source: string; class: string }[];
};
export type ModelsModel = {
  id: string; service_id: string; name: string; catalog_visible: boolean; catalog_state: string;
  inference_state: string; deployments: NonNullable<GatewayDetails['deployments']>;
  traffic: ModelsSample; freshness: string; collected_at?: string;
};
export type ModelsChannel = {
  id: string; service_id: string; model: string; deployment_id?: string; upstream_model?: string;
  provider?: string; channel?: string; inference_state: string; traffic: ModelsSample;
  freshness: string; collected_at?: string;
};
export type ModelsTraffic = ModelsSample & {
  id: string; service_id: string; model: string; deployment_id?: string; channel?: string;
  inference_state: string; window_start: string; window_end: string; sampled: boolean; freshness: string;
};
export type ModelsPublicStatus = {
  service_id: string; name: string; source: string; state: string; freshness: string;
  collected_at?: string; observed_at?: string; updated_at?: string; count: number; truncated: boolean;
  unknown: boolean; unhealthy: boolean;
  components: {
    name: string; status: string; source_status?: string; category?: string; group?: string;
    uptime?: number; heartbeats?: string[]; updated_at?: string;
  }[];
};
export type ModelsView = {
  collected_at: string; sources: ModelsSource[];
  summary: { services: number; models: number; deployments: number; channels: number; traffic_groups: number; public_status_services: number };
  models: ModelsModel[]; channels: ModelsChannel[]; traffic: ModelsTraffic[]; public_status: ModelsPublicStatus[];
  probes: { state: string; message: string }; coverage: { truncated: boolean; notes: string[] };
  total: number; next_cursor?: string; has_more: boolean;
};
