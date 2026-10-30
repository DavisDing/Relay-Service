import { useEffect, useRef, useState } from 'react';
import { api, APIError, APIRequestSupersededError } from './api';
import { checkClass, message, states, time } from './presentation';
import { Icon } from './ui';
import { WorkspaceEmpty, WorkspacePagination, WorkspaceSection } from './Workspace';
import type { Check, Service } from './types';

type Props = { services: Service[]; refreshKey: number; onUnauthorized?: () => void };
export function ServiceLogs({ services, refreshKey, onUnauthorized }: Props) {
  const [selected, setSelected] = useState('');
  const service = services.find(item => item.id === selected) || services[0];
  const [cursor, setCursor] = useState(''); const [previous, setPrevious] = useState<string[]>([]);
  const [query, setQuery] = useState(''); const [status, setStatus] = useState('');
  const [stored, setStored] = useState<{ scope: string; items: Check[]; at: string } | null>(null);
  const [error, setError] = useState(''); const [loading, setLoading] = useState(true);
  const scope = `${service?.id}:${service?.generation}:${cursor}`;
  const view = stored?.scope === scope ? stored : null;
  const unauthorized = useRef(onUnauthorized); unauthorized.current = onUnauthorized;
  useEffect(() => { setCursor(''); setPrevious([]); setQuery(''); setStatus(''); }, [service?.id, service?.generation]);
  useEffect(() => {
    if (!service) { setLoading(false); return; }
    let active = true; let timer: ReturnType<typeof setTimeout>;
    async function load() {
      setLoading(true);
      try {
        const result = await api<{ items: Check[] }>(`/services/${encodeURIComponent(service!.id)}/checks?limit=50&cursor=${encodeURIComponent(cursor)}`);
        if (active) { setStored({ scope, items: result.data.items || [], at: result.generated_at }); setError(''); }
      } catch (reason) {
        if (!active || reason instanceof APIRequestSupersededError) return;
        if (reason instanceof APIError && reason.status === 401) unauthorized.current?.();
        else setError(reason instanceof Error ? reason.message : '服务检查日志暂不可读。');
      } finally {
        if (active) { setLoading(false); if (!cursor) timer = setTimeout(load, 60000); }
      }
    }
    setError(''); void load();
    return () => { active = false; clearTimeout(timer); };
  }, [scope, refreshKey]);
  if (!service) return <WorkspaceEmpty>尚未接入服务，添加服务后会在这里显示已保存的检查记录。</WorkspaceEmpty>;
  const items = view?.items || [];
  const visible = filterServiceLogs(items, query, status);
  return <div className="data-workspace">
    <section className="workspace-navigation"><div className="workspace-instance-bar"><label>检查来源<select value={service.id} onChange={event => setSelected(event.target.value)}>{services.map(item => <option value={item.id} key={item.id}>{item.name}{!item.enabled ? ' · 已暂停' : ''}</option>)}</select></label><div className="workspace-instance-meta"><span>Relay 已保存的基础检查记录</span><small>读取 {time(view?.at)} · 第一页每 60 秒同步</small></div></div></section>
    <WorkspaceSection title="服务日志" description="按服务追溯检查结果、响应耗时与脱敏错误；不读取容器运行日志。" />
    {error && <p className="banner error" role="alert">{error}{view && ' 当前保留上次读取的记录，请核对时间。'}</p>}
    <section className="workspace-results" aria-label="服务检查日志"><div className="workspace-toolbar"><label className="workspace-search"><Icon name="search" /><input type="search" maxLength={120} aria-label="搜索本页检查日志" placeholder="搜索本页错误或检查类别" value={query} onChange={event => setQuery(event.target.value)} /></label><select aria-label="筛选本页检查结果" value={status} onChange={event => setStatus(event.target.value)}><option value="">本页全部结果</option>{Object.entries(states).map(([key, name]) => <option key={key} value={key}>{name}</option>)}</select></div>
      <p className="sub">每页最多 50 条，搜索和筛选仅作用于当前页。旧配置的历史保留并显示对应版本。</p>
      {loading && !view ? <p className="workspace-loading" role="status">正在读取检查记录…</p> : <ServiceLogTable items={visible} />}
      {view && !visible.length && <WorkspaceEmpty>{items.length ? '本页没有匹配记录，可调整筛选或翻页。' : '这一页尚无保存的检查记录。'}</WorkspaceEmpty>}
      <WorkspacePagination label="服务日志分页" countLabel={`本页 ${items.length} 条`} total={items.length} page={previous.length + 1} loading={loading} hasPrevious={previous.length > 0} hasNext={items.length === 50 && !!items[items.length - 1]?.cursor} onPrevious={() => { const stack = [...previous]; setCursor(stack.pop() || ''); setPrevious(stack); }} onNext={() => { setPrevious(value => [...value, cursor]); setCursor(items[items.length - 1].cursor); }} />
    </section>
  </div>;
}

export function filterServiceLogs(items: Check[], query: string, status: string) {
  const search = query.trim().toLowerCase();
  return items.filter(item => (!status || item.status === status) && `${message(item.message)} ${checkClass(item.class)} ${item.id}`.toLowerCase().includes(search));
}

export function ServiceLogTable({ items }: { items: Check[] }) {
  return <div className="workspace-table-scroll" role="region" tabIndex={0} aria-label="可横向滚动的服务日志"><table className="workspace-table"><thead><tr><th className="workspace-time">检查完成</th><th>结果 / 类别</th><th>记录</th><th className="workspace-number">耗时</th><th>配置版本</th></tr></thead><tbody>{items.map(item => <tr key={item.id}><td className="workspace-time">{time(item.completed_at)}</td><td><span className={`workspace-state ${item.status === 'healthy' ? 'healthy' : item.status === 'unhealthy' ? 'unhealthy' : 'unknown'}`}>{states[item.status] || '未知结果'}</span><small>{checkClass(item.class)}</small></td><td>{message(item.message) || '无附加信息'}<small>记录 {item.id}</small></td><td className="workspace-number">{item.duration_ms ?? '—'}{item.duration_ms !== undefined && ' ms'}</td><td>{item.generation ?? '未知'}</td></tr>)}</tbody></table></div>;
}
