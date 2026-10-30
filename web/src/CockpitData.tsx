import { useEffect, useRef, useState } from 'react';
import { api, APIError, APIRequestSupersededError } from './api';
import type { Service } from './types';
import type { ModelsView } from './models-types';
import type { NetworkView } from './network-types';

export type Reading<T> = { data?: T; error?: string; receivedAt?: number };
export type CockpitSnapshot = { models: Reading<ModelsView>; networks: Record<string, Reading<NetworkView>> };

// Independent sources keep their last timestamped result; at most four network
// cache reads run together. These endpoints never trigger upstream checks.
export async function readCockpit(
  services: Service[], networkLogs: boolean,
  read = api, prior: CockpitSnapshot = { models: {}, networks: {} },
): Promise<CockpitSnapshot> {
  const result: CockpitSnapshot = { models: { ...prior.models }, networks: {} };
  const instances = services.filter(item => item.kind === 'mihomo');
  let cursor = 0;
  let stopped = false;
  const capture = async <T,>(path: string, previous: Reading<T>): Promise<Reading<T>> => {
    try { return { data: (await read<T>(path)).data, receivedAt: Date.now() }; }
    catch (reason) {
      if (reason instanceof APIRequestSupersededError || reason instanceof APIError && reason.status === 401) { stopped = true; throw reason; }
      return { ...previous, error: reason instanceof Error ? reason.message : '读取失败，请稍后重试。' };
    }
  };
  await Promise.all([
    capture<ModelsView>('/models?section=overview', prior.models).then(value => {
      result.models = value.data?.sources.some(item => !services.some(service => service.id === item.id && service.generation === item.generation))
        ? { error: '服务配置版本正在同步。' } : value;
    }),
    ...Array.from({ length: Math.min(4, instances.length) }, async () => {
      while (!stopped && cursor < instances.length) {
        const service = instances[cursor++];
        const value = await capture<NetworkView>(`/network/${encodeURIComponent(service.id)}?section=${networkLogs ? 'logs' : 'overview'}&limit=1`, prior.networks[service.id] || {});
        result.networks[service.id] = value.data && value.data.generation !== service.generation
          ? { error: '服务配置版本正在同步。' } : value;
      }
    }),
  ]);
  return result;
}

export function readingRetained<T>(reading: Reading<T>, now = Date.now()) {
  return !!reading.error || !reading.receivedAt || now - reading.receivedAt > 60000;
}

export function networkIsCurrent(reading: Reading<NetworkView>, source?: string, now = Date.now()) {
  const data = reading.data;
  if (!data || readingRetained(reading, now) || data.state !== 'available' || !data.collected_at) return false;
  const age = now - Date.parse(data.collected_at);
  return age >= 0 && age <= 30000 && (!source || ['available', 'healthy'].includes(data.coverage.source_states[source]));
}

export function useCockpitData(services: Service[], admin: boolean, logs: boolean, refreshKey: number, onUnauthorized?: () => void) {
  const [stored, setStored] = useState<{ scope: string; value: CockpitSnapshot } | null>(null);
  const [now, setNow] = useState(Date.now);
  const scope = `${admin}:${logs}:${services.map(item => `${item.id}:${item.generation}:${item.enabled}`).join(',')}`;
  const unauthorized = useRef(onUnauthorized); unauthorized.current = onUnauthorized;
  const current = stored?.scope === scope ? stored.value : { models: {}, networks: {} };
  const cache = useRef(current); cache.current = current;
  useEffect(() => {
    let active = true; let timer: ReturnType<typeof setTimeout>;
    async function load() {
      try {
        const value = await readCockpit(services, logs && admin, api, cache.current);
        if (active) { cache.current = value; setStored({ scope, value }); setNow(Date.now()); }
      } catch (reason) {
        if (active && reason instanceof APIError && reason.status === 401) unauthorized.current?.();
      } finally {
        if (active) timer = setTimeout(() => { if (document.visibilityState === 'visible') void load(); else timer = setTimeout(load, 15000); }, 15000);
      }
    }
    void load();
    const clock = setInterval(() => setNow(Date.now()), 10000);
    return () => { active = false; clearTimeout(timer); clearInterval(clock); };
  }, [scope, refreshKey]);
  // A service-list refresh can render between clock ticks. Compare its new
  // timestamp against the render time instead of the previous tick.
  return { ...current, now: Math.max(now, Date.now()) };
}
