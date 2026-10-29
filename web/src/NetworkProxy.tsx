import { useEffect, useId, useRef, useState } from 'react';
import { api, APIError, APIRequestSupersededError } from './api';
import { Button, Icon, Modal } from './ui';
import { WorkspaceTabs, WorkspaceMetric as Metric, WorkspaceEmpty as Empty, WorkspaceSection, WorkspacePagination } from './Workspace';
import { time } from './presentation';
import type { Service } from './types';
import type { NetworkCoverage, NetworkNode, NetworkNodeSample, NetworkSection, NetworkSettings, NetworkSubscription, NetworkUsage, NetworkView } from './network-types';
import './network.css';

const sections: { key: NetworkSection; label: string }[] = [
  { key: 'overview', label: '概览' }, { key: 'usage', label: '用量' },
  { key: 'subscriptions', label: '订阅' }, { key: 'nodes', label: '节点' },
  { key: 'connections', label: '连接' }, { key: 'logs', label: '日志' },
];
const sectionDescriptions: Record<NetworkSection, string> = {
  overview: '查看代理流量、活动连接与节点检测概况。', usage: '按实际采样观察流量变化与连接归因。',
  subscriptions: '查看外部订阅的额度、到期与所属节点；代理组不计入订阅。',
  nodes: '查看节点当前检测与近 24 小时可用性，历史记录独立展开。',
  connections: '查看当前活动连接、路由与采样速率。', logs: '按等级检索核心日志与采集事件。',
};
const stateNames: Record<string, string> = {
  healthy: '检测成功', unhealthy: '检测失败', available: '已采集', fresh: '有效', stale: '已过期',
  unknown: '待观测', missing: '无检测样本', future: '源时间异常', paused: '已暂停',
  disabled: '已关闭', unavailable: '暂不可读', unauthorized: '认证受限', unsupported: '暂不支持',
  pending: '等待采集', collecting: '采集中', error: '采集异常', capacity_limited: '采集实例达到上限',
  active: '当前激活', inactive: '未激活', exhausted: '额度耗尽', expired: '已到期',
  not_configured: '未配置', not_applicable: '不适用', import: '手动元数据', provider: '内核提供者',
  connected: '已连接', connecting: '连接中', disconnected: '已断流', waiting: '等待数据', partial: '部分可读', counter_unavailable: '计数器暂不可读',
  authentication_failed: '凭据被拒绝', credential_unavailable: '凭据不可用', credential_invalid: '凭据格式无效', target_blocked: '目标访问受限', endpoint_unsupported: '接口不支持', invalid_response: '响应格式不支持', timeout: '请求超时', network_error: '连接失败', http_error: '接口返回错误', response_too_large: '响应超过上限', redirect_rejected: '重定向被拒绝', rate_limited: '上游限流', stream_closed: '实时流已断开',
};
const sourceNames: Record<string, string> = {
  traffic: '实时流量', memory: '内存', connections: '连接', logs: '核心日志',
  proxies: '节点', nodes: '节点目录', providers: '代理提供者', subscriptions: '订阅', version: '内核版本',
  profiles: '配置订阅', profile: '配置订阅', profile_nodes: '配置订阅节点目录', control: '配置订阅管理', storage: '历史存储',
};
function label(value?: string) { return value ? stateNames[value] || '状态待确认' : '尚无数据'; }
function bytes(value?: number) {
  if (value === undefined || !Number.isFinite(value) || value < 0) return '—';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let size = value; let unit = 0;
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit++; }
  return `${size.toLocaleString('zh-CN', { maximumFractionDigits: unit ? 1 : 0 })} ${units[unit]}`;
}
function elapsed(value?: string, sampledAt?: string) {
  if (!value) return '尚无记录';
  const seconds = Math.floor(((sampledAt ? Date.parse(sampledAt) : Date.now()) - Date.parse(value)) / 1000);
  if (!Number.isFinite(seconds) || seconds < 0) return '源时间待确认';
  return seconds >= 3600 ? `${Math.floor(seconds / 3600)} 小时 ${Math.floor(seconds % 3600 / 60)} 分` : seconds >= 60 ? `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒` : `${seconds} 秒`;
}
function rate(value?: number) { return value === undefined ? '—' : `${bytes(value)}/s`; }
function nodeTone(state?: string, freshness?: string) {
  if (freshness && freshness !== 'fresh') return 'unknown';
  return ['healthy', 'success', 'available', 'up'].includes(state || '') ? 'healthy'
    : ['unhealthy', 'failure', 'failed', 'down'].includes(state || '') ? 'unhealthy' : 'unknown';
}
function NodeState({ node }: { node: NetworkNode }) {
  const tone = node.loaded ? nodeTone(node.state, node.freshness) : 'unknown';
  return <span className={`workspace-state ${tone}`}>{!node.loaded ? '目录未关联核心' : node.freshness === 'stale' ? '样本已过期' : tone === 'healthy' ? '检测成功' : tone === 'unhealthy' ? '检测失败' : '待观测'}</span>;
}

export function NodeAvailability({ node }: { node: NetworkNode }) {
  const value = node.availability_24h;
  const count = value ? value.success + value.failure : 0;
  const rate = value?.success_rate;
  const text = rate === undefined ? '暂无有效检测' : `${(rate * 100).toFixed(1)}%`;
  return <div className="network-availability">
    <strong>{text}</strong><small>{count ? `${count} 次检测 · ${value!.covered_intervals}/96 时段有样本` : '首次接入前与采集缺口保持未知'}</small>
    <div className="network-availability-bars" role="img" aria-label={`近24小时有效检测成功率 ${text}；${value?.success || 0} 次成功，${value?.failure || 0} 次失败；灰色时段无有效样本。`}>
      {(value?.intervals || Array.from({ length: 96 }, () => 'unknown')).map((state, index) => <span key={index} className={state} title={`${value ? new Date(Date.parse(value.start_at) + index * 900000).toLocaleString('zh-CN') : '未采集'} · ${state === 'healthy' ? '有效检测成功' : state === 'unhealthy' ? '有检测失败' : '无有效样本'}`} />)}
    </div>
  </div>;
}

function NodeHistory({ serviceID, generation, node, onClose, onUnauthorized }: { serviceID: string; generation: number; node: NetworkNode; onClose: () => void; onUnauthorized?: () => void }) {
  const titleID = useId();
  const [hours, setHours] = useState(24); const [page, setPage] = useState(0);
  const [samples, setSamples] = useState<NetworkNodeSample[]>([]); const [loading, setLoading] = useState(true); const [error, setError] = useState('');
  const unauthorized = useRef(onUnauthorized); unauthorized.current = onUnauthorized;
  useEffect(() => {
    let active = true;
    setLoading(true); setError(''); setSamples([]); setPage(0);
    api<NetworkView>(`/network/${encodeURIComponent(serviceID)}?section=nodes&node_id=${encodeURIComponent(node.id)}&hours=${hours}&limit=1`).then(({ data }) => {
      if (active && data.generation === generation) setSamples(data.node_history || []);
    }).catch(reason => {
      if (!active || reason instanceof APIRequestSupersededError) return;
      if (reason instanceof APIError && reason.status === 401) unauthorized.current?.();
      else setError(reason instanceof Error ? reason.message : '节点历史读取失败。');
    }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [serviceID, generation, node.id, hours]);
  const history = [...samples].reverse();
  return <Modal titleId={titleID} onClose={onClose}><section className="dialog data-workspace network-history-dialog">
    <div className="section-head"><div><h2 id={titleID}>{node.name} · 检测历史</h2><p className="sub">保存实际观测记录；相同源检测可能被多次观察，不能当作多次检测。</p></div><Button className="icon-button" aria-label="关闭节点历史" onClick={onClose}><Icon name="close" /></Button></div>
    <label className="network-history-range">历史范围<select value={hours} onChange={event => setHours(Number(event.target.value))}><option value={24}>近 24 小时</option><option value={168}>近 7 天</option></select></label>
    {error && <p className="banner error" role="alert">{error}</p>}
    {loading ? <p className="workspace-loading" role="status">正在读取检测历史…</p> : !error && <>
      {hours === 24 && <NodeTimeline samples={samples} />}
      <div className="workspace-table-scroll" tabIndex={0} role="region" aria-label="节点检测历史，可横向滚动"><table className="workspace-table"><thead><tr><th className="workspace-time">源检测时间</th><th className="workspace-time">Relay 观测时间</th><th>检测结果</th><th className="workspace-number">延迟</th></tr></thead><tbody>{history.slice(page * 20, page * 20 + 20).map((sample, index) => <tr key={`${sample.at}-${index}`}><td className="workspace-time">{time(sample.checked_at)}</td><td className="workspace-time">{time(sample.at)}</td><td><span className={`workspace-state ${nodeTone(sample.state, sample.freshness)}`}>{label(sample.freshness === 'fresh' ? sample.state : sample.freshness)}</span></td><td className="workspace-number">{nodeTone(sample.state, sample.freshness) === 'healthy' && sample.delay_ms !== undefined ? `${sample.delay_ms} ms` : '—'}</td></tr>)}</tbody></table></div>
      {!history.length && <Empty>所选时段尚无保存的节点观测记录。</Empty>}
      {!!history.length && <WorkspacePagination label="节点历史分页" total={history.length} page={page + 1} hasPrevious={page > 0} hasNext={(page + 1) * 20 < history.length} onPrevious={() => setPage(value => value - 1)} onNext={() => setPage(value => value + 1)} />}
    </>}
  </section></Modal>;
}

function Coverage({ coverage }: { coverage: NetworkCoverage }) {
  return <details className="workspace-panel workspace-coverage network-coverage">
    <summary>采集覆盖与来源 <span>最近样本 {time(coverage.last_sample_at)}</span></summary>
    <p className="sub">从 {time(coverage.started_at)} 开始积累。首次接入前和采集中断期间没有历史；源数据过期不会继续显示成功。</p>
    <dl className="network-sources">{Object.entries(coverage.source_states || {}).map(([source, state]) => <div key={source}><dt>{sourceNames[source] || '附加来源'}</dt><dd>{label(state)}</dd></div>)}</dl>
    {coverage.truncated && <p className="network-warning">上游数据超过本次采集上限，当前视图未覆盖全部记录。</p>}
    {coverage.notes?.map((note, index) => <p className="sub" key={index}>{note}</p>)}
  </details>;
}

function UsageChart({ items }: { items: NetworkUsage[] }) {
  const id = useId();
  if (!items.length) return <Empty>还没有流量历史。后台采集后会逐步显示，打开页面不会补造过去的数据。</Empty>;
  const points = [...items].filter(item => Number.isFinite(Date.parse(item.at))).sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  if (!points.length) return <Empty>源时间不可用，暂时无法绘制流量。</Empty>;
  const max = Math.max(1, ...points.flatMap(point => [point.upload_bytes, point.download_bytes]));
  const start = Date.parse(points[0].at); const end = Date.parse(points.at(-1)!.at);
  const x = (at: string) => 16 + (Date.parse(at) - start) / Math.max(60000, end - start) * 968;
  const y = (value: number) => 172 - Math.max(0, value) / max * 152;
  // A column represents only its returned aggregate. Never connect observations across a gap.
  const columnWidth = Math.max(1, Math.min(8, 350 / points.length));
  const upload = points.reduce((sum, item) => sum + item.upload_bytes, 0);
  const download = points.reduce((sum, item) => sum + item.download_bytes, 0);
  return <div className="network-chart">
    <div className="network-chart-legend"><span><i className="network-up" />上传 {bytes(upload)}</span><span><i className="network-down" />下载 {bytes(download)}</span><small>每组柱为实际采集时段增量</small></div>
    <svg viewBox="0 0 1000 196" role="img" aria-labelledby={id}>
      <title id={id}>上传 {bytes(upload)}，下载 {bytes(download)}。每组柱仅代表该时段的实际采样；未采集时段没有柱。</title>
      {[20, 96, 172].map(line => <line key={line} x1="16" y1={line} x2="984" y2={line} className="network-chart-grid" />)}
      {points.map((point, index) => <g key={`${point.at}-${index}`}><title>{time(point.at)} · 上传 {bytes(point.upload_bytes)} · 下载 {bytes(point.download_bytes)} · {point.samples} 次源采样</title><rect x={x(point.at) - columnWidth} y={y(point.upload_bytes)} width={columnWidth} height={Math.max(1, 172 - y(point.upload_bytes))} className="network-dot-up" /><rect x={x(point.at)} y={y(point.download_bytes)} width={columnWidth} height={Math.max(1, 172 - y(point.download_bytes))} className="network-dot-down" /></g>)}
    </svg>
    <div className="network-chart-axis"><span>{time(points[0].at)}</span><span>{time(points.at(-1)!.at)}</span></div>
    <p className="sub">当前已加载 {points.length} 个时段；较长时间范围由后端汇总，柱内仍可能存在采样缺口。{points.some(item => item.counter_reset) ? '包含内核计数器重置，已分段计算。' : ''}采样缺口内的流量归属可能不完整；本机观测不替代机场账单。</p>
  </div>;
}

function NodeTimeline({ samples }: { samples: NetworkNodeSample[] }) {
  const end = Date.now();
  const start = end - 24 * 3600000;
  const bins: { success: number; failure: number; unknown: number; delay: number[]; checks: Set<string> }[] = Array.from({ length: 96 }, () => ({ success: 0, failure: 0, unknown: 0, delay: [], checks: new Set<string>() }));
  const seen = new Set<string>();
  for (const sample of samples) {
    const collected = Date.parse(sample.at); const sourceAt = sample.checked_at ? Date.parse(sample.checked_at) : NaN;
    if (!Number.isFinite(collected) || !Number.isFinite(sourceAt) || sourceAt < start || sourceAt > end || sourceAt > collected) continue;
    const identity = String(sourceAt);
    // Source time locates the test; reading the same result later does not move or repeat it.
    if (seen.has(identity)) continue;
    seen.add(identity);
    const index = Math.min(95, Math.floor((sourceAt - start) / 900000));
    const bin = bins[index];
    const tone = nodeTone(sample.state, sample.freshness);
    bin.checks.add(identity);
    if (tone === 'healthy') { bin.success++; if (sample.delay_ms !== undefined) bin.delay.push(sample.delay_ms); }
    else if (tone === 'unhealthy') bin.failure++;
    else bin.unknown++;
  }
  const success = bins.reduce((sum, bin) => sum + bin.success, 0); const failure = bins.reduce((sum, bin) => sum + bin.failure, 0);
  return <div className="network-timeline-wrap">
    <div className="network-timeline" role="img" aria-label={`近24小时，检测成功${success}次，失败${failure}次；灰色表示无新鲜检测样本。`}>
      {bins.map((bin, index) => {
        const status = bin.failure ? 'unhealthy' : bin.success ? 'healthy' : 'unknown';
        return <span key={index} className={status} title={`${new Date(start + index * 900000).toLocaleString('zh-CN')} · 成功 ${bin.success} / 失败 ${bin.failure}${!bin.success && !bin.failure ? ' · 无有效新检测' : ''}`} />;
      })}
    </div>
    <div className="network-chart-axis"><span>24 小时前</span><span>现在</span></div>
    <p className="sub"><span className="network-key healthy" />成功 <span className="network-key unhealthy" />失败 <span className="network-key unknown" />未知/缺口 · 有效检测 {success + failure} 次{success + failure ? ` · 样本成功率 ${(success / (success + failure) * 100).toFixed(1)}%` : ''}</p>
    <DelayChart samples={samples} />
  </div>;
}

function DelayChart({ samples }: { samples: NetworkNodeSample[] }) {
  const seen = new Set<string>();
  const points = samples.filter(sample => {
    if (nodeTone(sample.state, sample.freshness) !== 'healthy' || sample.delay_ms === undefined || !sample.checked_at || seen.has(sample.checked_at) || (Date.parse(sample.checked_at) > Date.now() || Date.parse(sample.checked_at) < Date.now() - 86400000) || Date.parse(sample.checked_at) > Date.parse(sample.at)) return false;
    seen.add(sample.checked_at); return Number.isFinite(Date.parse(sample.checked_at));
  }).sort((a, b) => Date.parse(a.checked_at!) - Date.parse(b.checked_at!));
  if (!points.length) return <p className="sub">尚无有效延迟曲线，未执行主动测速。</p>;
  const max = Math.max(1, ...points.map(point => point.delay_ms!));
  const start = Date.parse(points[0].checked_at!); const end = Date.parse(points.at(-1)!.checked_at!);
  const xy = (point: NetworkNodeSample) => [12 + (Date.parse(point.checked_at!) - start) / Math.max(60000, end - start) * 976, 98 - point.delay_ms! / max * 80];
  return <div className="network-delay-chart"><svg viewBox="0 0 1000 112" role="img" aria-label={`已采集${points.length}个有效延迟样本，最大${max}毫秒。`}>
    <line x1="12" y1="98" x2="988" y2="98" className="network-chart-grid" />
    {points.map((point, index) => { const [x, y] = xy(point); return <circle key={index} cx={x} cy={y} r="3" className="network-dot-up"><title>{time(point.checked_at)} · {point.delay_ms} ms</title></circle>; })}
  </svg><p className="sub">有效源检测延迟 · 最大 {max} ms。每点只代表对应检测目标和时刻。</p></div>;
}

function SubscriptionCard({ item, onNodes }: { item: NetworkSubscription; onNodes: () => void }) {
  const percentage = item.total_bytes && item.used_bytes !== undefined ? Math.min(100, Math.max(0, item.used_bytes / item.total_bytes * 100)) : undefined;
  return <article className="workspace-panel network-subscription">
    <div className="network-row-head"><div><h3>{item.name}</h3><p className="sub">{item.source === 'provider' ? '内核代理提供者' : item.source === 'control' ? '配置管理服务' : '导入的订阅元数据'} · {item.source === 'control' ? item.active ? '管理层当前激活' : '管理层未激活' : item.active ? '源标记激活' : '源未标记激活'}</p></div><span className={`workspace-state ${['expired', 'exhausted'].includes(item.usage_state) ? 'unhealthy' : 'unknown'}`}>{label(item.usage_state)}</span></div>
    <strong className="network-quota">{bytes(item.used_bytes)} <small>/ {bytes(item.total_bytes)}</small></strong>
    {percentage !== undefined ? <progress max={100} value={percentage} aria-label={`${item.name}额度使用${percentage.toFixed(1)}%`} /> : <p className="sub">额度字段不完整，暂不计算使用率。</p>}
    <dl className="workspace-facts network-mini-grid"><div><dt>剩余</dt><dd>{bytes(item.remaining_bytes)}</dd></div><div><dt>到期</dt><dd>{item.expire_seconds && item.expire_seconds > 0 ? time(new Date(item.expire_seconds * 1000).toISOString()) : '未提供'}</dd></div><div><dt>节点</dt><dd>{item.node_count_known ? `${item.node_count} 项` : '目录待采集'}</dd></div><div><dt>更新时间</dt><dd>{time(item.updated_at)}</dd></div></dl>
    <Button type="button" className="text" onClick={onNodes}>查看所属节点<Icon name="next" /></Button>
  </article>;
}

type Props = { services: Service[]; admin: boolean; initialServiceID?: string; refreshKey?: number; onUnauthorized?: () => void; onAdd?: () => void };
export function NetworkProxy({ services, admin, initialServiceID = '', refreshKey = 0, onUnauthorized, onAdd }: Props) {
  const instances = services.filter(service => service.kind === 'mihomo');
  const [selectedID, setSelectedID] = useState(initialServiceID);
  const service = instances.find(item => item.id === selectedID) || instances[0];
  const serviceID = service?.id || '';
  const [section, setSection] = useState<NetworkSection>('overview');
  const [storedView, setView] = useState<NetworkView | null>(null);
  const view = storedView?.service_id === serviceID && storedView.generation === service?.generation ? storedView : null;
  const [query, setQuery] = useState(''); const [search, setSearch] = useState('');
  const [subscription, setSubscription] = useState(''); const [subscriptionName, setSubscriptionName] = useState('');
  const [filter, setFilter] = useState(''); const [hours, setHours] = useState(24);
  const [usageDimension, setUsageDimension] = useState<'client' | 'rule' | 'chain'>('client');
  const [cursor, setCursor] = useState(''); const [previous, setPrevious] = useState<string[]>([]);
  const [node, setNode] = useState<NetworkNode | null>(null);
  const [error, setError] = useState(''); const [loading, setLoading] = useState(false); const [revision, setRevision] = useState(0);
  const [settings, setSettings] = useState<NetworkSettings | null>(null); const [controlToken, setControlToken] = useState(''); const [saving, setSaving] = useState(false); const [notice, setNotice] = useState('');
  const [imports, setImports] = useState<NetworkSubscription[] | null>(null); const [importError, setImportError] = useState(''); const [importing, setImporting] = useState(false);
  const importRead = useRef(0); const mounted = useRef(false);
  const scope = useRef(0); const currentInstance = useRef(''); const unauthorized = useRef(onUnauthorized); unauthorized.current = onUnauthorized;
  const restricted = !admin && (section === 'connections' || section === 'logs');
  const selectedScope = `${serviceID}:${service?.generation || 0}:${admin}`;
  currentInstance.current = selectedScope;

  useEffect(() => { mounted.current = true; return () => { mounted.current = false; importRead.current++; }; }, []);
  useEffect(() => { if (initialServiceID) setSelectedID(initialServiceID); }, [initialServiceID]);
  useEffect(() => { const timer = setTimeout(() => setSearch(query.trim()), 250); return () => clearTimeout(timer); }, [query]);
  useEffect(() => {
    importRead.current++; setView(null); setSettings(null); setError(''); setNotice(''); setCursor(''); setPrevious([]); setQuery(''); setSearch('');
    setSubscription(''); setSubscriptionName(''); setFilter(''); setNode(null); setImports(null); setImportError(''); setSaving(false); setImporting(false); setControlToken('');
  }, [selectedScope]);
  useEffect(() => {
    if (!serviceID || restricted) { setLoading(false); return; }
    let active = true; let timer: ReturnType<typeof setTimeout>; let inFlight = false;
    const version = ++scope.current;
    async function load() {
      if (!active || inFlight) return;
      inFlight = true; setLoading(true);
      const params = new URLSearchParams({ section, limit: section === 'usage' ? '200' : '25', hours: String(hours) });
      if (cursor) params.set('cursor', cursor);
      if (search) params.set('q', search);
      if (subscription && section === 'nodes') params.set('subscription', subscription);
      if (filter) params.set('state', filter);
      try {
        const { data } = await api<NetworkView>(`/network/${encodeURIComponent(serviceID)}?${params}`);
        if (!active || scope.current !== version) return;
        setView(data); setError('');
        setSettings(current => current || data.settings);
      } catch (reason) {
        if (!active || scope.current !== version || reason instanceof APIRequestSupersededError) return;
        if (reason instanceof APIError && reason.status === 401) unauthorized.current?.();
        else setError(reason instanceof Error ? reason.message : '网络代理信息读取失败，请稍后重试。');
      } finally {
        inFlight = false;
        if (active && scope.current === version) {
          setLoading(false);
          timer = setTimeout(() => { if (document.visibilityState === 'visible') void load(); else timer = setTimeout(load, 5000); }, ['overview', 'connections', 'logs'].includes(section) ? 5000 : 15000);
        }
      }
    }
    // Clear data at a view boundary; a failed poll within that boundary retains its timestamped last result.
    setView(null); void load();
    return () => { active = false; clearTimeout(timer); };
  }, [selectedScope, section, cursor, search, subscription, filter, hours, revision, refreshKey, restricted]);

  function resetPage() { setCursor(''); setPrevious([]); }
  function chooseSection(next: NetworkSection) { setSection(next); resetPage(); setQuery(''); setSearch(''); setFilter(''); setNode(null); }
  function showNodes(item: NetworkSubscription) { setSubscription(item.id); setSubscriptionName(item.name); chooseSection('nodes'); }
  async function saveSettings() {
    if (!serviceID || !settings) return;
    const id = serviceID; const version = currentInstance.current; setSaving(true); setNotice(''); setError('');
    try {
      const { data } = await api<NetworkSettings>(`/network/${encodeURIComponent(id)}/settings`, 'PUT', {
        enabled: settings.enabled, persist_logs: settings.persist_logs,
        log_retention_hours: settings.log_retention_hours, log_limit_mib: settings.log_limit_mib, generation: settings.generation,
        control_base_url: settings.control_base_url || '', control_token: controlToken, clear_control_token: settings.clear_control_token || false,
      });
      if (!mounted.current || currentInstance.current !== version) return;
      setSettings(data); setControlToken(''); setNotice('采集策略已保存。'); setRevision(value => value + 1);
    } catch (reason) {
      if (!mounted.current || currentInstance.current !== version || reason instanceof APIRequestSupersededError) return;
      if (reason instanceof APIError && reason.status === 401) unauthorized.current?.();
      else setError(reason instanceof Error ? reason.message : '采集策略保存失败。');
    } finally { if (mounted.current && currentInstance.current === version) setSaving(false); }
  }
  async function readImport(file?: File) {
    const version = currentInstance.current; const read = ++importRead.current;
    setImports(null); setImportError('');
    if (!file) return;
    if (file.size > 60 * 1024) { setImportError('文件超过 60 KiB，请仅导入订阅元数据（最多 100 个订阅、总计 5000 个节点）。'); return; }
    try {
      const raw = await file.text();
      if (!mounted.current || currentInstance.current !== version || importRead.current !== read) return;
      const parsed: unknown = JSON.parse(raw);
      const list = typeof parsed === 'object' && parsed !== null && 'subscriptions' in parsed ? (parsed as { subscriptions: unknown }).subscriptions : undefined;
      if (!Array.isArray(list) || !list.length || list.length > 100) throw new Error('请提供 subscriptions 数组，包含 1～100 个订阅元数据。');
      const allowed = new Set(['id', 'name', 'source', 'active', 'upload_bytes', 'download_bytes', 'used_bytes', 'remaining_bytes', 'total_bytes', 'expire_seconds', 'updated_at', 'node_count', 'node_count_known', 'usage_state', 'nodes']);
      const nodeAllowed = new Set(['id', 'subscription_id', 'subscription_name', 'name', 'type', 'state', 'loaded', 'freshness']);
      let nodeTotal = 0;
      const clean = list.map((item: unknown) => {
        if (!item || typeof item !== 'object' || Array.isArray(item)) throw new Error('订阅项目必须是元数据对象。');
        const row = item as Record<string, unknown>;
        if (Object.keys(row).some(key => !allowed.has(key))) throw new Error('文件包含非元数据字段。请移除订阅链接、完整配置和凭据后再导入。');
        if (typeof row.name !== 'string' || !row.name.trim() || row.name.length > 120) throw new Error('每个订阅需要 1～120 字名称。');
        if (row.nodes !== undefined && (!Array.isArray(row.nodes) || row.nodes.length > 5000)) throw new Error('节点目录必须为数组，总节点数最多 5000 项。');
        if (Array.isArray(row.nodes)) nodeTotal += row.nodes.length;
        if (nodeTotal > 5000) throw new Error('本次导入的总节点数不能超过 5000 项。');
        if (Array.isArray(row.nodes)) for (const value of row.nodes) {
          if (!value || typeof value !== 'object' || Object.keys(value).some(key => !nodeAllowed.has(key))) throw new Error('节点目录仅接收名称、协议与身份等元数据，不接收连接参数或检测结果。');
        }
        return { ...row, source: 'import' } as NetworkSubscription;
      });
      setImports(clean);
    } catch (reason) { if (mounted.current && currentInstance.current === version && importRead.current === read) setImportError(reason instanceof SyntaxError ? '文件不是有效的 JSON 元数据，请检查格式。' : reason instanceof Error ? reason.message : '元数据文件无法读取。'); }
  }
  async function importSubscriptions() {
    if (!imports || !serviceID) return;
    const version = currentInstance.current; setImporting(true); setImportError('');
    try {
      await api(`/network/${encodeURIComponent(serviceID)}/subscriptions/import`, 'POST', { subscriptions: imports, generation: settings?.generation || view?.settings.generation });
      if (!mounted.current || currentInstance.current !== version) return;
      setSettings(null); setImports(null); setNotice('订阅元数据已导入；节点在线状态仍由实际检测证据决定。'); setRevision(value => value + 1);
    } catch (reason) {
      if (!mounted.current || currentInstance.current !== version || reason instanceof APIRequestSupersededError) return;
      if (reason instanceof APIError && reason.status === 401) unauthorized.current?.();
      else setImportError(reason instanceof Error ? reason.message : '订阅元数据导入失败。');
    } finally { if (mounted.current && currentInstance.current === version) setImporting(false); }
  }

  if (!instances.length) return <section className="data-workspace"><Empty>尚未接入 mihomo。请在服务监控添加“mihomo 订阅与节点质量”，填写控制 API 地址及检查凭据。</Empty>{admin && onAdd && <Button className="secondary" onClick={onAdd}><Icon name="plus" />接入 mihomo</Button>}</section>;
  const overview = view?.overview;
  const available = (source: string) => !!view && !['stale', 'paused'].includes(view.state) && ['available', 'healthy'].includes(view.coverage.source_states?.[source] || '');
  const subscriptions = view?.subscriptions || []; const nodes = view?.nodes || []; const usage = view?.usage || [];
  const isList = ['subscriptions', 'nodes', 'connections', 'logs', 'usage'].includes(section);
  return <div className="data-workspace">
    <div className="workspace-navigation"><section className="workspace-instance-bar">
      <label>mihomo 实例<select value={serviceID} onChange={event => setSelectedID(event.target.value)}>{instances.map(item => <option key={item.id} value={item.id}>{item.name}{!item.enabled ? ' · 已暂停' : ''}</option>)}</select></label>
      <div className="workspace-instance-meta"><span className={`workspace-state ${['healthy', 'available'].includes(view?.state || '') ? 'healthy' : 'unknown'}`}>{view ? label(view.state) : '等待采集数据'}</span><small>最近采集 {time(view?.collected_at)}</small></div>
      {service.ui_url && <a className="button secondary network-console-link" href={service.ui_url} target="_blank" rel="noopener noreferrer">原控制台<Icon name="external" /></a>}
    </section>
    <WorkspaceTabs label="网络代理视图" items={sections.map(item => ({ ...item, note: !admin && ['connections', 'logs'].includes(item.key) ? '管理员' : undefined }))} value={section} onChange={chooseSection} /></div>
    <WorkspaceSection title={sections.find(item => item.key === section)!.label} description={sectionDescriptions[section]} />
    {error && <p className="banner error" role="alert">{error}{view ? ' 当前显示最近一次成功读取的数据，请核对采集时间。' : ''}</p>}
    {notice && <p className="network-notice" role="status">{notice}</p>}
    {restricted ? <Empty>连接目的地与核心日志仅管理员可查看。你仍可查看概览、用量、订阅和节点的脱敏信息。</Empty> : <>
      {view?.message && view.state !== 'available' && <p className="network-context" role="status">{view.message}</p>}
      {section === 'overview' && <>
        <div className="workspace-metrics" aria-label="当前网络指标">
          <Metric title="上传速率" value={available('traffic') ? rate(overview?.upload_rate) : '—'} note="核心实时观测" />
          <Metric title="下载速率" value={available('traffic') ? rate(overview?.download_rate) : '—'} note="核心实时观测" />
          <Metric title="活动连接" value={available('connections') ? String(overview?.connection_count ?? '—') : '—'} note="当前采样时刻" />
          <Metric title="内存使用" value={available('memory') ? bytes(overview?.memory_bytes) : '—'} note="mihomo 核心" />
        </div>
        <section className="workspace-panel"><h2>订阅与节点</h2><dl className="workspace-facts network-mini-grid network-inventory-counts"><div><dt>订阅</dt><dd>{overview?.subscription_count ?? '—'} 项</dd></div><div><dt>节点目录</dt><dd>{overview?.node_count ?? '—'} 项</dd></div><div><dt>检测成功</dt><dd>{overview?.healthy_nodes ?? '—'} 项</dd></div><div><dt>检测失败</dt><dd>{overview?.unhealthy_nodes ?? '—'} 项</dd></div><div><dt>待观测 / 未关联</dt><dd>{overview?.unknown_nodes ?? '—'} 项</dd></div></dl><p className="sub">管理层激活仅表示配置选择；无法核实与核心的归属时，目录节点保持未知，不借用同名节点的检测结果。</p></section>
        <section className="workspace-panel"><h2>核心与采集</h2><dl className="workspace-facts network-mini-grid"><div><dt>内核版本</dt><dd>{overview?.core_version || '尚未取得'}</dd></div><div><dt>当前配置</dt><dd>{overview?.active_config || '源未提供'}</dd></div><div><dt>上传累计</dt><dd>{available('traffic') ? bytes(overview?.upload_total) : '—'}</dd></div><div><dt>下载累计</dt><dd>{available('traffic') ? bytes(overview?.download_total) : '—'}</dd></div></dl><p className="sub">后台采集独立于此页面运行；这里读取已有状态，不会切换代理、刷新订阅或主动测速。</p></section>
        {admin && view && settings && <details className="workspace-panel"><summary>后台采集与日志保存</summary><form onSubmit={event => { event.preventDefault(); void saveSettings(); }}><fieldset disabled={saving}><h3>多订阅配置管理服务</h3><p className="sub">自动读取原面板的配置文件订阅。控制服务地址填写原面板所在的协议、主机和端口；控制令牌与 mihomo 核心 secret 是两种凭据。仅由 Relay 后端使用，保存后不回显。</p><div className="form-grid"><label>控制服务地址<input type="url" maxLength={2048} placeholder="https://面板主机:端口" value={settings.control_base_url || ''} autoComplete="off" onChange={event => setSettings({ ...settings, control_base_url: event.target.value })} /></label><label>控制服务令牌<input type="password" maxLength={8192} autoComplete="new-password" value={controlToken} placeholder={settings.has_control_token ? '已配置；留空保留原值' : '按控制服务设置填写；无鉴权时可留空'} onChange={event => { setControlToken(event.target.value); setSettings({ ...settings, clear_control_token: false }); }} /></label></div>{settings.has_control_token && <label className="network-toggle"><input type="checkbox" checked={settings.clear_control_token || false} onChange={event => { setSettings({ ...settings, clear_control_token: event.target.checked }); if (event.target.checked) setControlToken(''); }} />清除已保存的控制服务令牌</label>}<h3>采集与保留</h3><label className="network-toggle"><input type="checkbox" checked={settings.enabled} onChange={event => setSettings({ ...settings, enabled: event.target.checked })} />启用网络详情后台采集</label><label className="network-toggle"><input type="checkbox" checked={settings.persist_logs} onChange={event => setSettings({ ...settings, persist_logs: event.target.checked })} />持久保存脱敏日志</label><p className="sub">默认日志只保留实时短缓冲。节点可用性展示近 24 小时，节点历史与流量汇总保留 7 天；开启持久日志后按以下时间与容量上限保留。</p><div className="form-grid"><label>日志保留小时<input type="number" min="1" max="168" required value={settings.log_retention_hours} onChange={event => setSettings({ ...settings, log_retention_hours: Number(event.target.value) })} /></label><label>每实例日志上限（MiB）<input type="number" min="1" max="100" required value={settings.log_limit_mib} onChange={event => setSettings({ ...settings, log_limit_mib: Number(event.target.value) })} /></label></div><div className="buttons"><Button className="secondary" type="submit" loading={saving}>保存采集策略</Button><Button type="button" className="text" disabled={saving} onClick={() => { setSettings(null); setControlToken(''); setRevision(value => value + 1); }}>重读当前设置</Button></div></fieldset></form></details>}
      </>}
      <section className={isList && section !== 'usage' ? "workspace-results" : "workspace-results-unframed"} aria-label={sections.find(item => item.key === section)!.label}>
      {isList && <div className="workspace-toolbar">{section !== 'usage' && <label className="workspace-search"><Icon name="search" /><input type="search" maxLength={120} placeholder="搜索当前来源记录" value={query} onChange={event => { setQuery(event.target.value); resetPage(); }} aria-label="搜索网络记录" /></label>}{section === 'nodes' && <><select aria-label="节点状态" value={filter} onChange={event => { setFilter(event.target.value); resetPage(); }}><option value="">全部状态</option><option value="healthy">检测成功</option><option value="unhealthy">检测失败</option><option value="unknown">未知</option></select>{subscription && <Button className="text" onClick={() => { setSubscription(''); setSubscriptionName(''); resetPage(); }}>清除订阅筛选：{subscriptionName || '已选订阅'}<Icon name="close" /></Button>}</>}{section === 'logs' && <select aria-label="日志等级" value={filter} onChange={event => { setFilter(event.target.value); resetPage(); }}><option value="">全部等级</option><option value="error">错误</option><option value="warning">警告</option><option value="info">信息</option><option value="debug">调试</option></select>}{section === 'usage' && <select aria-label="流量时间范围" value={hours} onChange={event => { setHours(Number(event.target.value)); resetPage(); }}><option value={1}>最近 1 小时</option><option value={24}>最近 24 小时</option><option value={168}>最近 7 天</option></select>}</div>}
      {!view && !error && <p role="status" className="workspace-loading">正在读取网络代理信息…</p>}
      {view && section === 'usage' && <section className="workspace-panel"><h2>观测流量</h2><UsageChart items={usage} /><dl className="workspace-facts network-mini-grid"><div><dt>采集时段</dt><dd>{usage.length} 个</dd></div><div><dt>源观测样本</dt><dd>{usage.reduce((sum, item) => sum + item.samples, 0)} 次</dd></div><div><dt>计数器重置</dt><dd>{usage.filter(item => item.counter_reset).length} 个时段</dd></div><div><dt>覆盖开始</dt><dd>{time(view.coverage.started_at)}</dd></div></dl><p className="sub">此处为核心全局流量增量；节点、订阅无法可靠关联时不分摊，采集缺口不补算。</p></section>}
      {view && section === 'usage' && admin && <section className="workspace-panel"><div className="network-row-head"><h2>连接采样归因</h2><select aria-label="流量归因维度" value={usageDimension} onChange={event => setUsageDimension(event.target.value as 'client' | 'rule' | 'chain')}><option value="client">按客户端</option><option value="rule">按规则</option><option value="chain">按代理链</option></select></div><p className="sub">仅统计同一连接在相邻有效快照间的流量差；首见、断流和已结束的短连接可能缺失。各维度分别描述相同的采样流量，不能彼此相加，也不能与核心全局流量相加。分组统计从所选起点所在整小时开始，可能多含不足一小时。每小时每类保留 100 个标签，超出合并其他；展示保留分组中用量最多 20 项。</p><p className="sub">分组覆盖：{time(view.usage_group_start)} 至 {time(view.usage_group_end)}</p><div className="workspace-table-scroll" tabIndex={0} aria-label="可横向滚动的数据表" role="region"><table className="workspace-table"><thead><tr><th>{({ client: '客户端', rule: '规则', chain: '代理链' })[usageDimension]}</th><th className="workspace-number">上传</th><th className="workspace-number">下载</th><th className="workspace-number">有效差分样本</th></tr></thead><tbody>{(view.usage_groups || []).filter(item => item.dimension === usageDimension).map((item, index) => <tr key={`${item.label}-${index}`}><td>{item.label || '源未提供'}</td><td className="workspace-number">{bytes(item.upload_bytes)}</td><td className="workspace-number">{bytes(item.download_bytes)}</td><td className="workspace-number">{item.samples}</td></tr>)}</tbody></table></div>{!(view.usage_groups || []).some(item => item.dimension === usageDimension) && <Empty>当前时段尚无可归因连接差分；只有全局流量时不推测具体归属。</Empty>}</section>}
      {view && section === 'subscriptions' && <><div className="network-subscriptions">{subscriptions.map(item => <SubscriptionCard key={item.id} item={item} onNodes={() => showNodes(item)} />)}</div>{!subscriptions.length && <Empty>当前来源未提供可显示的订阅。代理提供者为空不代表没有机场订阅；配置文件中的订阅可能由面板另行管理。</Empty>}{admin && <details className="workspace-panel"><summary>导入配置文件订阅元数据</summary><p className="sub">优先在概览配置控制服务，自动读取全部配置订阅。管理层暂不可读时，可导入名称、额度和节点目录。导入不刷新机场、不加载配置，也不会让未检测节点变为在线。</p><p className="sub">最多 100 个订阅、总计 5000 个节点，文件小于 60 KiB。仅接收下列 JSON 元数据结构。请勿选择完整代理配置或含订阅链接、密码、UUID 的面板备份。</p><pre>{'{"subscriptions":[{"id":"my-subscription","name":"订阅名称","active":false,"node_count":0,"usage_state":"unknown"}]}'}</pre><label>选择元数据 JSON<input type="file" accept="application/json,.json" disabled={importing} onChange={event => { void readImport(event.target.files?.[0]); event.currentTarget.value = ''; }} /></label>{importError && <p className="banner error" role="alert">{importError}</p>}{imports && <div><p>待导入 {imports.length} 个订阅：{imports.map(item => item.name).join('、')}</p><Button className="secondary" loading={importing} onClick={importSubscriptions}>确认导入元数据</Button></div>}</details>}</>}
      {view && section === 'nodes' && <><div className="workspace-table-scroll" tabIndex={0} aria-label="可横向滚动的数据表" role="region"><table className="workspace-table network-nodes-table"><thead><tr><th>节点</th><th>订阅归属</th><th>当前证据</th><th className="workspace-number">延迟</th><th className="workspace-time">检测时间</th><th className="network-availability-heading">近 24 小时可用性</th><th>历史</th></tr></thead><tbody>{nodes.map(item => <tr key={item.id}><td data-label="节点"><strong>{item.name}</strong><small>{item.type || '协议未知'} · {item.loaded ? '核心观测节点' : '目录未关联核心'}</small></td><td data-label="订阅归属">{item.subscriptions?.length ? item.subscriptions.map(entry => entry.name).join('、') : item.subscription_name || subscriptions.find(entry => entry.id === item.subscription_id)?.name || (item.subscription_id ? '已关联订阅' : '未归属订阅')}</td><td data-label="当前检测"><NodeState node={item} /></td><td data-label="延迟" className="workspace-number">{item.loaded && nodeTone(item.state, item.freshness) === 'healthy' && item.delay_ms !== undefined ? `${item.delay_ms} ms` : '—'}</td><td data-label="检测时间" className="workspace-time">{time(item.checked_at)}</td><td data-label="近 24 小时可用性"><NodeAvailability node={item} /></td><td data-label="历史"><Button className="text" aria-haspopup="dialog" onClick={() => setNode(item)}><Icon name="history" />历史</Button></td></tr>)}</tbody></table></div>{!nodes.length && <Empty>当前筛选下没有节点记录。未激活订阅可能尚无可读节点目录，详情见采集覆盖。</Empty>}<p className="sub">可用性 = 有效检测成功次数 / 有效检测总次数；状态条每格 15 分钟，灰色表示没有有效样本。采样成功率不等于连续在线时长或 SLA。</p></>}
      {view && section === 'connections' && <><p className="sub">仅当前采样中的活动连接。短连接可能在采样间隔内结束，不是完整访问审计；页面不提供断开连接操作。</p><div className="workspace-table-scroll" tabIndex={0} aria-label="可横向滚动的数据表" role="region"><table className="workspace-table"><thead><tr><th>目标与来源</th><th>规则 / 代理链</th><th className="workspace-number">上传 / 下载</th><th className="workspace-number">当前速率</th><th className="workspace-time">持续时间 / 开始时间</th></tr></thead><tbody>{view.connections?.map(item => <tr key={item.id}><td><strong>{item.host || item.destination || '目的地未提供'}</strong><small>{item.source || '来源未提供'} · {item.network || '协议未知'}</small>{item.process && <small>{item.process}</small>}</td><td>{item.rule || '规则未提供'}<small>{item.chains?.length ? item.chains.join(' → ') : '代理链未提供'}</small></td><td className="workspace-number">↑ {bytes(item.upload_bytes)}<br />↓ {bytes(item.download_bytes)}</td><td className="workspace-number">↑ {rate(item.upload_rate)}<br />↓ {rate(item.download_rate)}</td><td className="workspace-time">{elapsed(item.started_at, view.collected_at)}<small>{time(item.started_at)}</small></td></tr>)}</tbody></table></div>{!view.connections?.length && <Empty>{available('connections') ? '当前采样没有活动连接。' : '连接信息尚未取得，请查看采集来源状态。'}</Empty>}</>}
      {view && section === 'logs' && <><p className="sub">核心日志与采集事件按实际覆盖展示。{view.settings.persist_logs ? `脱敏日志持久保留 ${view.settings.log_retention_hours} 小时，容量上限 ${view.settings.log_limit_mib} MiB。` : '当前未开启日志持久保存；实时缓冲不保证重启后保留。'}</p><ol className="workspace-surface network-logs">{view.logs?.map(item => <li key={item.id}><div><span className={`network-log-level ${['error', 'warning'].includes(item.level) ? 'warning' : ''}`}>{({ error: '错误', warning: '警告', info: '信息', debug: '调试', silent: '静默' } as Record<string, string>)[item.level] || '未分类'}</span><time dateTime={item.at}>{time(item.at)}</time><small>{item.kind === 'collector' ? 'Relay 采集事件' : '核心日志'}</small></div><p>{item.message}</p></li>)}</ol>{!view.logs?.length && <Empty>当前筛选没有日志。请核对日志流状态和覆盖时间，空列表不代表没有发生错误。</Empty>}</>}
      {view && isList && section !== 'usage' && <WorkspacePagination total={view.total} page={previous.length + 1} loading={loading} hasPrevious={previous.length > 0} hasNext={!!view.has_more && !!view.next_cursor} onPrevious={() => { const stack = [...previous]; setCursor(stack.pop() || ''); setPrevious(stack); }} onNext={() => { setPrevious(items => [...items, cursor]); setCursor(view.next_cursor || ''); }} />}
      </section>
      {view && <Coverage coverage={view.coverage} />}
    </>}
    {node && <NodeHistory serviceID={serviceID} generation={service.generation} node={node} onClose={() => setNode(null)} onUnauthorized={onUnauthorized} />}
  </div>;
}
