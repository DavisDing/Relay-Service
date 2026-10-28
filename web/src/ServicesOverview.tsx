import { CheckDetails } from './CheckDetails';
import { useState } from 'react';
import type { Alert, Service } from './types';
import { Button, Icon } from './ui';
import { message, evidence, kindLabels, states, time } from './presentation';

type Props = {
  services: Service[];
  alerts: Alert[];
  servicesSynced: string;
  alertsSynced: string;
  alertsCurrent: boolean;
  alertsComplete: boolean;
  admin: boolean;
  busy: boolean;
  onAdd: () => void;
  onAlerts: () => void;
  onHistory: (service: Service) => unknown;
  onEdit: (service: Service) => unknown;
  onToggle: (service: Service) => unknown;
  onCheck: (service: Service) => unknown;
  onNetwork?: (service: Service) => void;
};

export function serviceStatus(service: Service) {
  if (!service.enabled) return 'paused';
  const status = service.status || service.current?.status || 'unknown';
  if (status === 'queued' || status === 'running') return status;
  if (!service.last_check_at || service.freshness === 'stale' || service.freshness === 'never_observed') return 'unknown';
  return status;
}

const failedStates = new Set(['unhealthy', 'unauthorized', 'unsupported', 'interrupted']);
export function needsAttention(service: Service) {
  return service.open_incident || service.open_restriction || failedStates.has(serviceStatus(service)) || serviceStatus(service) === 'unknown';
}

// A distribution of current snapshots, not historical availability or an SLA.
export function summarizeServices(services: Service[]) {
  const counts = { healthy: 0, failed: 0, unknown: 0, pending: 0, paused: 0 };
  for (const service of services) {
    const status = serviceStatus(service);
    if (status === 'healthy') counts.healthy++;
    else if (status === 'paused') counts.paused++;
    else if (status === 'running' || status === 'queued') counts.pending++;
    else if (failedStates.has(status)) counts.failed++;
    else counts.unknown++;
  }
  return counts;
}

export function filterServices(services: Service[], group: string | null, filter: string, query: string) {
  return services.filter(service => {
    const status = serviceStatus(service);
    return (group === null || (service.group || '') === group)
      && (filter === 'all' || (filter === 'attention' && needsAttention(service))
        || (filter === 'pending' && (status === 'queued' || status === 'running'))
        || (filter === 'paused' && status === 'paused'))
      && `${service.name} ${service.group || ''} ${service.base_url} ${kindLabels[service.kind] || service.kind}`.toLowerCase().includes(query.trim().toLowerCase());
  });
}

const segments = [
  ['healthy', '成功'], ['failed', '异常'], ['unknown', '待观测'], ['pending', '检查中'], ['paused', '暂停'],
] as const;

function Distribution({ services }: { services: Service[] }) {
  const counts = summarizeServices(services);
  return <div className="health-distribution" aria-hidden="true">
    {segments.map(([key]) => counts[key] > 0 && <span key={key} className={'health-segment ' + key}
      style={{ flex: counts[key] }} />)}
  </div>;
}

function attentionReason(service: Service) {
  if (service.open_restriction) return '认证或访问受限事件尚未解除';
  if (service.open_incident) return service.enabled ? '故障事件尚未恢复' : '已暂停，故障事件仍未恢复';
  if (service.freshness === 'stale') return '采集已过期，需重新检查';
  if (!service.last_check_at) return '等待首次检查';
  return message(service.current?.message) || states[serviceStatus(service)] || '检查结果未知';
}

export function ServicesOverview(props: Props) {
  const { services, alerts, admin, busy, onAdd, onAlerts } = props;
  // null means all groups; an empty string is a real, ungrouped selection.
  const [group, setGroup] = useState<string | null>(null);
  const [filter, setFilter] = useState('all');
  const [query, setQuery] = useState('');
  const groups = [...new Set(services.map(service => service.group || ''))];
  const selected = group !== null && groups.includes(group) ? group : null;
  const counts = summarizeServices(services);
  const enabled = services.length - counts.paused;
  const attention = services.filter(needsAttention).sort((a, b) =>
    Number(b.open_incident || b.open_restriction || failedStates.has(serviceStatus(b))) -
    Number(a.open_incident || a.open_restriction || failedStates.has(serviceStatus(a))));
  const openEvents = services.filter(service => service.open_incident || service.open_restriction).length;
  const observed = services.filter(service => service.last_check_at).sort((a, b) =>
    Date.parse(b.last_check_at!) - Date.parse(a.last_check_at!)).slice(0, 3);
  const recent = props.alertsCurrent && props.alertsSynced
    ? alerts.reduce<Alert | undefined>((latest, alert) => !latest || alert.cursor > latest.cursor ? alert : latest, undefined)
    : undefined;
  const visible = filterServices(services, selected, filter, query);
  const synced = !!props.servicesSynced;
  const headline = !synced ? '等待服务数据' : !services.length ? '从接入第一个服务开始'
    : attention.length ? `${attention.length} 项服务需要关注` : !enabled ? '所有服务已暂停'
      : counts.pending ? '正在收集检查结果' : '基础检查状态平稳';

  return <div className="cockpit">
    <div className="cockpit-top">
      <section className="fleet-health" aria-labelledby="fleet-title">
        <div className="cockpit-eyebrow"><Icon name="pulse" />运行态势<span>基础服务检查</span></div>
        <h2 id="fleet-title">{headline}</h2>
        <div className="fleet-reading">
          <strong>{synced && enabled ? Math.round(counts.healthy / enabled * 100) : '—'}<small>{synced && enabled ? '%' : ''}</small></strong>
          <div><b>当前检查成功占比</b><p>{synced ? `${counts.healthy} / ${enabled} 个启用服务 · ${counts.paused} 个已暂停` : '同步完成后显示，不将未知视为成功'}</p></div>
        </div>
        <Distribution services={services} />
        <dl className="health-legend" aria-label="服务监控摘要">
          {segments.map(([key, label]) => <div key={key}><dt><i className={'health-dot ' + key} />{label}</dt><dd>{synced ? counts[key] : '—'}</dd></div>)}
        </dl>
        <div className="fleet-foot"><span>{synced ? `最近同步 ${time(props.servicesSynced)}` : '服务数据待同步'}</span><span>当前快照 · 非历史可用率</span></div>
      </section>

      <section className="group-health" aria-labelledby="groups-title">
        <div className="cockpit-section-head"><h2 id="groups-title">分组概况</h2><span>{groups.length} 个分组</span></div>
        <p className="cockpit-hint">按组查看成功覆盖与异常分布</p>
        <div className="group-list">
          {groups.map(value => {
            const members = services.filter(service => (service.group || '') === value);
            const healthy = summarizeServices(members).healthy;
            return <button type="button" className="group-row" key={value} aria-pressed={selected === value}
              onClick={() => setGroup(selected === value ? null : value)}>
              <span className="group-row-title"><b>{value || '未分组'}</b><span>{healthy} / {members.length} 成功</span></span>
              <Distribution services={members} />
            </button>;
          })}
          {!groups.length && <p className="cockpit-hint">{synced ? '接入服务后自动汇总分组' : '等待分组数据'}</p>}
        </div>
        <p className="capability">按各服务的检查证据汇总，版本更新证据未接入。</p>
      </section>
    </div>

    <div className="cockpit-streams">
      <section className="attention-panel" aria-labelledby="attention-title">
        <div className="cockpit-section-head"><h2 id="attention-title">需要关注 <span>{synced ? attention.length : '—'}</span></h2>
          <Button className="text" onClick={() => { setGroup(null); setFilter(filter === 'attention' ? 'all' : 'attention'); }}>
            {filter === 'attention' ? '显示全部' : '筛选明细'}<Icon name="next" />
          </Button></div>
        <div className="attention-list">
          {attention.slice(0, 2).map(service => <div className="attention-item" key={service.id}>
            <span className={'status ' + serviceStatus(service)}>{states[serviceStatus(service)] || serviceStatus(service)}</span>
            <div><b title={service.name}>{service.name}</b><p title={attentionReason(service)}>{attentionReason(service)}</p></div>
            <Button className="icon-button" aria-label={`查看 ${service.name} 检查历史`} title="检查历史" disabled={busy} onClick={() => props.onHistory(service)}><Icon name="history" /></Button>
          </div>)}
          {!attention.length && <div className="cockpit-quiet"><Icon name={synced ? 'check' : 'refresh'} /><div><b>{synced ? '暂无需关注的服务' : '等待服务同步'}</b><p>{synced ? '未恢复事件、检查失败和待观测服务将在这里集中显示。' : '同步前不判断整体健康状态。'}</p></div></div>}
        </div>
        <div className="attention-foot"><span>{synced ? `${openEvents} 个服务有未恢复事件${attention.length > 2 ? ' · 展示优先 2 项' : ''}` : '未恢复事件待同步'}</span><Button className="text" disabled={busy} onClick={onAlerts}>告警中心<Icon name="next" /></Button></div>
      </section>

      <section className="observations-panel" aria-labelledby="observations-title">
        <div className="cockpit-section-head"><h2 id="observations-title">最近检查</h2><span>各服务最近一次</span></div>
        <ol className="observation-list">
          {observed.map(service => <li key={service.id}>
            <i className={'health-dot ' + (service.current?.status === 'healthy' ? 'healthy' : failedStates.has(service.current?.status || '') ? 'failed' : 'unknown')} />
            <div><b title={service.name}>{service.name}</b><span>{states[service.current?.status || 'unknown'] || service.current?.status} · {time(service.last_check_at)}</span></div>
            <span>{duration(service)}</span>
          </li>)}
        </ol>
        {!observed.length && <p className="cockpit-hint">尚无检查记录，首次检查后显示。</p>}
        <p className="event-preview" title={message(recent?.message)}>{recent
          ? `${props.alertsComplete ? '最近事件' : '已加载事件'}：${services.find(service => service.id === recent.service_id)?.name || recent.service_id} · ${message(recent.message)}`
          : !props.alertsCurrent || !props.alertsSynced ? '告警摘要待同步，请查看告警中心。' : '暂无告警记录；待观测不代表健康。'}</p>
      </section>
    </div>

    <section className="service-inventory" aria-labelledby="inventory-title">
      <div className="inventory-heading"><div><h2 id="inventory-title">服务明细 <span>{visible.length} / {services.length}</span></h2><p>展开查看连接、配置与检查证据</p></div>
        {admin && <Button disabled={busy} onClick={onAdd}><Icon name="plus" />添加服务</Button>}
      </div>
      <div className="inventory-toolbar">
        <div className="inventory-filters" role="group" aria-label="服务状态筛选">
          {[['all', '全部'], ['attention', '需关注'], ['pending', '检查中'], ['paused', '已暂停']].map(([value, label]) =>
            <button type="button" key={value} aria-pressed={filter === value} onClick={() => setFilter(value)}>{label}</button>)}
        </div>
        <div className="inventory-search"><label className="service-search"><Icon name="search" /><input aria-label="搜索服务" placeholder="名称、类型或地址" value={query} onChange={event => setQuery(event.target.value)} /></label>
          <select aria-label="服务分组" value={selected === null ? 'all' : 'group:' + selected}
            onChange={event => setGroup(event.target.value === 'all' ? null : event.target.value.slice(6))}>
            <option value="all">全部分组（{services.length}）</option>
            {groups.map(value => <option key={value} value={'group:' + value}>{value || '未分组'}（{services.filter(service => (service.group || '') === value).length}）</option>)}
          </select></div>
      </div>
      {services.length === 0 ? <div className="empty"><h2>{synced ? '尚未配置服务' : '正在同步服务'}</h2><p>{synced ? '添加服务后，Relay 将保存检查记录和故障事件。' : '连接失败时请查看上方提示后重试。'}</p></div>
        : !visible.length ? <div className="empty"><h2>没有匹配的服务</h2><p>调整状态、分组或搜索条件。</p><Button className="secondary" onClick={() => { setGroup(null); setFilter('all'); setQuery(''); }}>重置筛选</Button></div>
          : <div className="service-list">
            <div className="service-columns" aria-hidden="true"><span>服务 / 分组</span><span>检查状态</span><span>最近检查</span><span>耗时</span><span /></div>
            {visible.map(service => <ServiceRow key={service.id} service={service} {...props} />)}
          </div>}
    </section>
  </div>;
}

function duration(service: Service) {
  return service.current?.duration_ms !== undefined ? service.current.duration_ms + ' ms' : '—';
}

function ServiceRow({ service, admin, busy, onHistory, onEdit, onToggle, onCheck, onNetwork }: Props & { service: Service }) {
  const status = serviceStatus(service);
  return <details className="service-row">
    <summary>
      <div className="service-identity"><span className="service-symbol"><Icon name="server" /></span><div><b title={service.name}>{service.name}</b><span>{kindLabels[service.kind] || service.kind} · {service.group || '未分组'}</span></div></div>
      <div className="service-state"><span className={'status ' + status}>{states[status] || status}</span>{service.enabled && service.freshness === 'stale' && <small>采集已过期</small>}{(service.open_incident || service.open_restriction) && <small>有未恢复事件</small>}</div>
      <span className="service-check-time">{time(service.last_check_at)}</span>
      <span className="service-duration">{duration(service)}</span>
      <span className="service-expand"><Icon name="next" /></span>
    </summary>
    <div className="service-expanded">
      <p className="service-endpoint"><span>检查地址</span>{service.base_url}</p>
      {service.enabled && status === 'unknown' && <p className="warning">{service.freshness === 'stale' ? '采集已过期' : '等待首次检查'}，暂不判断当前健康</p>}
      <dl className="service-facts">
        <div><dt>证据层级</dt><dd>{evidence(service.evidence_level)}</dd></div>
        <div><dt>最近检查</dt><dd>{time(service.last_check_at)}</dd></div>
        <div><dt>检查耗时</dt><dd>{duration(service)}</dd></div>
        <div><dt>最近成功</dt><dd>{time(service.last_success_at)}</dd></div>
        <div><dt>检查节奏</dt><dd>{service.interval_seconds ? `每 ${service.interval_seconds}s · ${service.timeout_seconds}s 超时` : '未提供'}</dd></div>
        <div><dt>检查凭据</dt><dd>{service.credential_configured === undefined ? '未提供' : service.credential_configured ? '已配置' : '未配置'}</dd></div>
        <div><dt>连续失败</dt><dd>{service.failure_count === undefined ? '未提供' : `${service.failure_count} / ${service.failure_threshold}`}</dd></div>
        <div><dt>恢复计数</dt><dd>{service.recovery_count === undefined ? '未提供' : `${service.recovery_count} / ${service.recovery_threshold}`}</dd></div>
      </dl>
      {service.current?.message && <p className="check-detail">{message(service.current.message)}</p>}
      {service.kind === 'mihomo' && onNetwork ? <p className="muted">订阅、节点、流量、连接与日志请在网络代理中查看。</p> : <CheckDetails check={service.current} />}
      <div className="buttons">
        {service.kind === 'mihomo' && onNetwork && <Button className="secondary" disabled={busy} onClick={() => onNetwork(service)}>查看网络代理<Icon name="next" /></Button>}
        <Button className="secondary" disabled={busy} onClick={() => onHistory(service)}><Icon name="history" />历史</Button>
        {service.ui_url && <a className="button secondary" href={service.ui_url} target="_blank" rel="noreferrer noopener">WebUI ↗</a>}
        {admin && <>
          <Button className="secondary" disabled={busy || !service.enabled} onClick={() => onCheck(service)}>检查</Button>
          <Button className="text" disabled={busy} onClick={() => onEdit(service)}>编辑</Button>
          <Button className="text" disabled={busy} onClick={() => onToggle(service)}>{service.enabled ? '暂停' : '恢复'}</Button>
        </>}
      </div>
    </div>
  </details>;
}
