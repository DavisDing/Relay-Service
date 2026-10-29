import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { api, APIError, APIRequestSupersededError } from './api';
import { kindLabels, time } from './presentation';
import { Button, Icon } from './ui';
import { WorkspaceTabs, WorkspaceMetric as Metric, WorkspaceEmpty as Empty, WorkspaceSection, WorkspacePagination } from './Workspace';
import type { Service } from './types';
import type { ModelsChannel, ModelsModel, ModelsPublicStatus, ModelsSample, ModelsSection, ModelsSource, ModelsView } from './models-types';
import './models.css';

const sections: { key: ModelsSection; label: string }[] = [
  { key: 'overview', label: '总览' }, { key: 'models', label: '模型' }, { key: 'channels', label: '渠道' },
  { key: 'traffic', label: '调用证据' }, { key: 'status', label: '公开状态' }, { key: 'probes', label: '模型探针' },
];
const sectionDescriptions: Record<ModelsSection, string> = {
  overview: '按模型整理目录、部署与调用证据，服务进程状态在服务监控查看。',
  models: '按实例检索模型，展开查看部署映射和调用样本。',
  channels: '核对模型部署、上游提供方与渠道证据。',
  traffic: '从已采集样本查看模型调用结果；保留样本覆盖边界。',
  status: '查看站方公开组件与模型状态。',
  probes: '查看模型可用性、能力与质量探针的实施状态。',
};
const sourceStates: Record<string, string> = {
  available: '已读取', disabled: '未启用', unavailable: '暂不可读', invalid: '格式不支持',
  invalid_response: '格式不支持', partial: '部分样本', unauthorized: '权限不足', unsupported: '当前安装不支持',
  unknown: '未知', missing: '尚无数据', pending: '等待采集', stale: '已过期', paused: '已暂停', fresh: '采集有效', never_observed: '尚未观测', not_collected: '尚未采集',
  healthy: '检查正常', unhealthy: '检查异常', degraded: '受限', not_applicable: '不适用',
};
const inferenceStates: Record<string, string> = {
  not_tested: '推理未测试', no_samples: '无调用样本', historical_success: '窗口含成功样本',
  historical_failure: '窗口含失败样本', historical_mixed: '窗口含成功与失败', unknown: '证据不足',
  cache_only: '仅缓存样本', stale: '样本已过期',
};
const componentStates: Record<string, string> = {
  operational: '正常', degraded_performance: '性能下降', partial_outage: '部分故障', major_outage: '严重故障',
  under_maintenance: '维护中', healthy: '正常', unhealthy: '异常', unknown: '未知',
};
function sourceLabel(state?: string) { return state ? sourceStates[state] || '状态待确认' : '尚无数据'; }
function sampleLabel(state?: string) { return state ? inferenceStates[state] || '证据不足' : '证据不足'; }
function tone(state: string, freshness?: string) {
  if (freshness && freshness !== 'fresh') return 'unknown';
  if (['healthy', 'operational', 'historical_success'].includes(state)) return 'success';
  if (['unhealthy', 'historical_failure', 'major_outage', 'partial_outage'].includes(state)) return 'failure';
  if (['historical_mixed', 'degraded_performance', 'unauthorized', 'partial'].includes(state)) return 'warning';
  return 'unknown';
}
function Badge({ state, children, freshness }: { state: string; children: ReactNode; freshness?: string }) {
  return <span className={`workspace-state ${tone(state, freshness)}`}>{children}</span>;
}
function sampleCount(item: ModelsSample) { return item.success + item.failure + item.unknown + item.cache_hits; }
function failureRate(item: ModelsSample) { const known = item.success + item.failure; return known ? `${(item.failure / known * 100).toFixed(1)}%` : '无已知结果'; }

function SampleSummary({ item, readable, freshness }: { item: ModelsSample; readable: boolean; freshness: string }) {
  if (!readable) return <p className="sub">调用证据不可读或尚未启用；空值不能解释为零调用。</p>;
  if (!sampleCount(item)) return <p className="sub">此轮窗口没有该模型的可用调用样本，当前推理状态未知。</p>;
  return <><dl className="workspace-facts"><div><dt>成功样本</dt><dd>{item.success}</dd></div><div><dt>失败样本</dt><dd>{item.failure}</dd></div><div><dt>结果未知</dt><dd>{item.unknown}</dd></div><div><dt>缓存命中</dt><dd>{item.cache_hits}</dd></div><div><dt>已知样本失败率</dt><dd>{failureRate(item)}</dd></div><div><dt>最近样本</dt><dd>{time(item.last_at)}</dd></div></dl><p className="sub">{freshness !== 'fresh' ? '本轮证据未处于新鲜状态，仅保留历史样本。' : ''}成功样本不证明当前推理可用；最近样本时间可能来自缓存或未知结果，不等于最近成功时间。缓存命中独立计数，不当作上游推理成功。</p></>;
}
function SourceCoverage({ source }: { source: ModelsSource }) {
  const issueNames: Record<string, string> = {
    source_unavailable: '来源暂不可读', invalid_response: '响应格式不支持', authentication_failed: '来源拒绝认证或授权',
    endpoint_unsupported: '当前安装不支持该接口', timeout: '请求超时', network_error: '连接失败',
  };
  return <div><p className="sub">快照状态：{sourceLabel(source.freshness)} · {source.enabled ? '检查已启用' : '检查已暂停'}</p><dl className="workspace-facts"><div><dt>目录</dt><dd>{sourceLabel(source.catalog_state)}</dd></div><div><dt>部署</dt><dd>{sourceLabel(source.deployments_state)}</dd></div><div><dt>日志</dt><dd>{sourceLabel(source.logs_state)}</dd></div><div><dt>采集时间</dt><dd>{time(source.collected_at)}</dd></div><div><dt>有效日志样本</dt><dd>{['available', 'partial'].includes(source.logs_state) ? `${source.rows} 条 / 源报告 ${source.total} 条` : '不可判断'}</dd></div><div><dt>跳过记录</dt><dd>{['available', 'partial'].includes(source.logs_state) ? `${source.skipped} 条` : '—'}</dd></div></dl>{source.window_start && <p className="sub">源窗口 {time(source.window_start)} 至 {time(source.window_end)} · 最近样本 {time(source.latest_at)}</p>}{(source.sampled || source.truncated) && <p className="models-warning">此来源仅保留有限清单或日志样本，不能代表完整调用量或完整失败率。</p>}{source.issues?.map((issue, index) => <p className="sub" key={index}>{issue.source} · {issueNames[issue.class] || '来源读取受限'}</p>)}</div>;
}
function Deployments({ items }: { items: ModelsModel['deployments'] }) {
  if (!items.length) return <p className="sub">没有可核对的部署映射；不能从公开别名猜测上游模型或渠道。</p>;
  return <div className="workspace-table-scroll" tabIndex={0} aria-label="可横向滚动的数据表" role="region"><table className="workspace-table"><thead><tr><th>部署标识</th><th>上游模型</th><th>协议 / 提供方</th><th>端点分组</th></tr></thead><tbody>{items.map((item, index) => <tr key={`${item.id || item.model}-${index}`}><td>{item.id || '源未提供'}</td><td>{item.upstream_model || '源未提供'}</td><td>{item.provider || '源未提供'}</td><td>{item.channel || '无法归属'}</td></tr>)}</tbody></table></div>;
}
function ModelEntry({ item, source }: { item: ModelsModel; source?: ModelsSource }) {
  const readable = !!source && ['available', 'partial'].includes(source.logs_state);
  return <details className="workspace-surface models-entry"><summary><div className="models-entry-name"><strong>{item.name}</strong><small>{source?.name || '实例待确认'}</small></div><div><small>目录</small><span>{item.catalog_visible ? '采集时凭据可见' : '由部署 / 样本发现'}</span></div><div><small>部署映射</small><span>{item.deployments?.length || 0} 项</span></div><div><small>历史调用</small><Badge state={item.inference_state} freshness={item.freshness}>{sampleLabel(item.inference_state)}</Badge></div><Icon name="next" /></summary><div className="models-entry-body"><p className="sub">实例与公开别名共同标识本行；同名模型跨实例分别保留。采集 {time(item.collected_at)} · {sourceLabel(item.catalog_state)}。</p><div className="models-evidence-levels"><span>目录 · {item.catalog_visible ? '可见' : '未见'}</span><span>可用性探针 · 未运行</span><span>能力测试 · 未测试</span><span>质量评测 · 未评测</span></div><h3>部署与渠道</h3><Deployments items={item.deployments || []} /><h3>本轮调用聚合样本</h3><SampleSummary item={item.traffic} readable={readable} freshness={item.freshness} />{source && <details className="models-inline-details"><summary>此实例的权限与采集覆盖</summary><SourceCoverage source={source} /></details>}</div></details>;
}
function PublicStatusEntry({ item }: { item: ModelsPublicStatus }) {
  const [page, setPage] = useState(0);
  const components = item.components || []; const start = Math.min(page * 20, Math.max(0, Math.ceil(components.length / 20) - 1) * 20);
  const outdated = item.freshness !== 'fresh';
  return <details className="workspace-surface models-entry models-public-entry"><summary><div className="models-entry-name"><strong>{item.name}</strong><small>站方公开状态 · {item.source || '来源待确认'}</small></div><div><small>已投影组件</small><span>{components.length} / {item.count} 项</span></div><div><small>来源状态</small><Badge state={item.state} freshness={item.freshness}>{sourceLabel(item.state)}{outdated ? ` · ${sourceLabel(item.freshness)}` : ''}</Badge></div><Icon name="next" /></summary><div className="models-entry-body"><p className="sub">仅表示站方报告，不证明当前账号或网络的实际可用性。未建立可信映射时，组件不会自动关联到同名渠道或模型。</p><dl className="workspace-facts"><div><dt>Relay采集</dt><dd>{time(item.collected_at)}</dd></div><div><dt>来源观测</dt><dd>{time(item.observed_at)}</dd></div><div><dt>上游更新时间</dt><dd>{item.updated_at ? time(item.updated_at) : '未提供，新鲜度不可证明'}</dd></div></dl>{item.truncated && <p className="models-warning">来源清单超过采集上限，以下组件不代表完整站点。</p>}<div className="workspace-table-scroll" tabIndex={0} aria-label="可横向滚动的数据表" role="region"><table className="workspace-table"><thead><tr><th>组件</th><th>站方状态</th><th className="workspace-number">站方可用率</th><th className="workspace-time">上游时间</th><th>站方历史心跳</th></tr></thead><tbody>{components.slice(start, start + 20).map((component, index) => <tr key={`${component.name}-${index}`}><td><strong>{component.name}</strong><small>{[component.category, component.group].filter(Boolean).join(' · ') || '未提供分类'}</small></td><td><Badge state={component.status} freshness={item.freshness}>{componentStates[component.status] || '未知'}</Badge>{outdated && <small>历史报告</small>}</td><td className="workspace-number">{component.uptime !== undefined && Number.isFinite(component.uptime) ? `${(component.uptime * 100).toFixed(2)}%` : '未提供'}</td><td className="workspace-time">{component.updated_at ? time(component.updated_at) : '源未提供'}</td><td>{component.heartbeats?.length ? <div className="models-heartbeats" aria-label={`站方提供${component.heartbeats.length}条历史心跳，无逐条时间`}>{component.heartbeats.map((state, beat) => <span className={tone(state, item.freshness)} key={beat} title={componentStates[state] || '未知'} />)}</div> : '未提供'}</td></tr>)}</tbody></table></div>{!components.length && <Empty>当前来源没有可展示的组件证据；请核对权限、解析配置与来源状态。</Empty>}{components.some(component => component.heartbeats?.length) && <p className="sub">心跳按源顺序展示；源未提供逐条时间，不能换算为Relay的24小时在线率。</p>}{components.length > 20 && <WorkspacePagination label="公开组件分页" total={components.length} page={Math.floor(start / 20) + 1} hasPrevious={start > 0} hasNext={start + 20 < components.length} onPrevious={() => setPage(Math.max(0, Math.floor(start / 20) - 1))} onNext={() => setPage(Math.floor(start / 20) + 1)} />}</div></details>;
}

type Props = {
  services: Service[]; admin: boolean; initialServiceID?: string; refreshKey?: number;
  onUnauthorized?: () => void; onEditService?: (service: Service) => void;
};
export function ModelsWorkspace({ services, admin, initialServiceID = '', refreshKey = 0, onUnauthorized, onEditService }: Props) {
  const eligible = services.filter(service => ['litellm', 'workbuddy2api', 'openai', 'ollama', 'openwebui', 'statuspage'].includes(service.kind) || !!service.collect_models || !!service.collect_logs || !!service.current?.details?.gateway || !!service.current?.details?.public_status);
  const [serviceID, setServiceID] = useState(initialServiceID);
  const [section, setSection] = useState<ModelsSection>('overview');
  const [query, setQuery] = useState(''); const [search, setSearch] = useState(''); const [state, setState] = useState('');
  const [cursor, setCursor] = useState(''); const [previous, setPrevious] = useState<string[]>([]);
  const [stored, setStored] = useState<{ scope: string; data: ModelsView; receivedAt: number } | null>(null);
  const [error, setError] = useState(''); const [loading, setLoading] = useState(false);
  const [now, setNow] = useState(Date.now);
  const sourceSignature = eligible.map(service => `${service.id}:${service.generation}:${service.enabled}`).join(',');
  const scope = `${serviceID}:${section}:${search}:${state}:${cursor}:${admin}:${sourceSignature}`;
  const snapshot = stored?.scope === scope ? stored.data : null;
  const retained = !!snapshot && (!!error || now - (stored?.receivedAt || 0) > 60000);
  // A failed Relay refresh cannot keep a cached source's freshness green indefinitely.
  const view = snapshot && retained ? {
    ...snapshot,
    sources: snapshot.sources.map(item => ({ ...item, freshness: item.freshness === 'fresh' ? 'stale' : item.freshness })),
    models: snapshot.models.map(item => ({ ...item, freshness: 'stale', inference_state: 'stale' })),
    channels: snapshot.channels.map(item => ({ ...item, freshness: 'stale', inference_state: 'stale' })),
    traffic: snapshot.traffic.map(item => ({ ...item, freshness: 'stale', inference_state: 'stale' })),
    public_status: snapshot.public_status.map(item => ({ ...item, freshness: 'stale', state: 'stale' })),
  } : snapshot;
  const version = useRef(0); const unauthorized = useRef(onUnauthorized); unauthorized.current = onUnauthorized;
  const source = view?.sources.find(item => item.id === serviceID);
  const sourceMap = new Map((view?.sources || []).map(item => [item.id, item]));
  const isList = !['overview', 'probes'].includes(section);
  useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 30000); return () => clearInterval(timer); }, []);
  useEffect(() => { setServiceID(initialServiceID); setCursor(''); setPrevious([]); }, [initialServiceID]);
  useEffect(() => { const timer = setTimeout(() => setSearch(query.trim()), 250); return () => clearTimeout(timer); }, [query]);
  useEffect(() => {
    if (serviceID && !eligible.some(item => item.id === serviceID)) { setServiceID(''); setCursor(''); setPrevious([]); }
  }, [serviceID, sourceSignature]);
  useEffect(() => {
    let active = true; let timer: ReturnType<typeof setTimeout>; let inFlight = false;
    const request = ++version.current;
    async function load() {
      if (!active || inFlight) return;
      inFlight = true; setLoading(true);
      const params = new URLSearchParams({ section, limit: '25' });
      if (serviceID) params.set('service', serviceID);
      if (search) params.set('q', search);
      if (state) params.set('state', state);
      if (cursor) params.set('cursor', cursor);
      try {
        const { data } = await api<ModelsView>(`/models?${params}`);
        if (!active || request !== version.current) return;
        if (data.sources.some(item => { const current = services.find(service => service.id === item.id); return !current || current.generation !== item.generation; })) {
          setStored(null); setError('服务配置版本正在同步，请刷新视图后重试。'); return;
        }
        setStored({ scope, data, receivedAt: Date.now() }); setError('');
      } catch (reason) {
        if (!active || request !== version.current || reason instanceof APIRequestSupersededError) return;
        if (reason instanceof APIError && reason.status === 401) unauthorized.current?.();
        else setError(reason instanceof Error ? reason.message : '模型工作区暂不可读，请稍后重试。');
      } finally {
        inFlight = false;
        if (active && request === version.current) { setLoading(false); timer = setTimeout(load, 15000); }
      }
    }
    setError(''); void load();
    return () => { active = false; clearTimeout(timer); };
  }, [scope, refreshKey]);
  function resetPage() { setCursor(''); setPrevious([]); }
  function chooseSection(next: ModelsSection) { setSection(next); setQuery(''); setSearch(''); setState(''); resetPage(); }
  function editSource(id: string) { const service = services.find(item => item.id === id); if (admin && service) onEditService?.(service); }
  function chooseService(id: string) { setServiceID(id); resetPage(); }
  const summary = view?.summary;
  return <div className="data-workspace">
    <div className="workspace-navigation"><section className="workspace-instance-bar"><label>模型 / 公开状态来源<select value={serviceID} onChange={event => chooseService(event.target.value)}><option value="">全部实例与状态来源</option>{eligible.map(item => <option key={item.id} value={item.id}>{item.name}{!item.enabled ? ' · 已暂停' : ''}</option>)}</select></label><div className="workspace-instance-meta"><span>目录、历史调用、站方状态分别保留</span><small>视图整理 {time(view?.collected_at)} · 不触发推理</small></div>{serviceID && admin && onEditService && <Button className="secondary" onClick={() => editSource(serviceID)}><Icon name="edit" />接入设置</Button>}</section>
    <WorkspaceTabs label="大模型视图" items={sections} value={section} onChange={chooseSection} /></div>
    <WorkspaceSection title={sections.find(item => item.key === section)!.label} description={sectionDescriptions[section]} />
    {error && <p className="banner error" role="alert">{error}{view ? ' 当前保留最近一次成功读取的数据，请核对采集时间。' : ''}</p>}
    {retained && !error && <p className="banner notice" role="status">当前视图超过一分钟未成功同步，保留记录已标为过期；恢复连接后重新判断。</p>}
    {!view && !error && <p className="workspace-loading" role="status">正在读取模型工作区…</p>}
    <section className={isList ? "workspace-results" : "workspace-results-unframed"} aria-label={sections.find(item => item.key === section)!.label}>
    {isList && <div className="workspace-toolbar"><label className="workspace-search"><Icon name="search" /><input type="search" maxLength={120} value={query} onChange={event => { setQuery(event.target.value); resetPage(); }} placeholder={section === 'status' ? '搜索公开状态来源或组件' : '搜索模型、部署或渠道'} aria-label="搜索模型证据" /></label><select aria-label="模型证据筛选" value={state} onChange={event => { setState(event.target.value); resetPage(); }}><option value="">全部证据状态</option>{section === 'status' ? <><option value="healthy">检查正常</option><option value="unhealthy">站方异常</option><option value="unknown">证据未知</option><option value="stale">已过期</option><option value="paused">已暂停</option><option value="unauthorized">权限不足</option></> : Object.entries(inferenceStates).map(([key, name]) => <option key={key} value={key}>{name}</option>)}</select></div>}
    {view && section === 'overview' && <>
      <div className="workspace-metrics"><Metric title="接入来源" value={summary?.services} note="实例与公开状态来源" /><Metric title="已收集模型别名" value={summary?.models} note="当前投影，未知来源不计入" /><Metric title="已收集部署" value={summary?.deployments} note="当前投影，未知来源不计入" /><Metric title="公开状态来源" value={summary?.public_status_services} note="站方报告，非本地推理" /></div>
      <section className="workspace-panel"><h2>模型证据分层</h2><div className="models-evidence-levels"><span>目录 · 账号可见清单</span><span>调用 · 有限历史样本</span><span>公开状态 · 站方报告</span><span>主动探针 · 尚未实现</span></div><p className="sub">统计仅覆盖已取得的目录和部署，零项不代表源中没有模型。网关进程可达、目录可见和历史成功，分别回答不同的问题。当前未执行逐模型可用性、能力或质量测试；未知与过期不会显示为测试通过。</p></section>
      <section className="workspace-panel"><h2>来源与采集覆盖</h2><div className="models-source-list">{view.sources.map(item => <details className="models-source" key={item.id}><summary><div><strong>{item.name}</strong><small>{kindLabels[item.kind] || '服务来源'} · {item.enabled ? sourceLabel(item.freshness) : '已暂停'}</small></div><div className="models-source-states"><span>目录 {sourceLabel(item.catalog_state)}</span><span>部署 {sourceLabel(item.deployments_state)}</span><span>日志 {sourceLabel(item.logs_state)}</span></div><Icon name="next" /></summary><SourceCoverage source={item} /><div className="buttons"><Button className="text" onClick={() => { chooseService(item.id); chooseSection(item.kind === 'statuspage' ? 'status' : 'models'); }}>查看此来源<Icon name="next" /></Button>{admin && onEditService && <Button className="text" onClick={() => editSource(item.id)}><Icon name="edit" />接入设置</Button>}</div></details>)}</div>{!view.sources.length && <Empty>尚无模型或公开状态来源。可在服务监控添加 LiteLLM、workbuddy2api、模型目录或公开状态页。</Empty>}</section>
    </>}
    {view && section === 'models' && <><p className="sub">每行是一个实例中的公开别名。展开查看部署与调用样本；别名不一定对应唯一的底层模型。</p><div className="models-list">{view.models.map(item => <ModelEntry key={item.id} item={item} source={sourceMap.get(item.service_id)} />)}</div>{!view.models.length && <Empty>当前筛选下没有可展示模型。目录未启用、权限不足或清单为空需分别核对，下方保留来源覆盖。</Empty>}</>}
    {view && section === 'channels' && <><p className="sub">每行表示一个具体部署映射。端点分组是匿名标识，不代表已核实的商业渠道名称；没有部署标识的样本不强行归属到某个渠道。</p><div className="workspace-table-scroll" tabIndex={0} aria-label="可横向滚动的数据表" role="region"><table className="workspace-table"><thead><tr><th>实例 / 模型别名</th><th>部署 / 上游模型</th><th>提供方 / 端点分组</th><th>历史调用证据</th><th className="workspace-time">最近样本</th></tr></thead><tbody>{view.channels.map(item => <ChannelRow key={item.id} item={item} source={sourceMap.get(item.service_id)} />)}</tbody></table></div>{!view.channels.length && <Empty>当前筛选没有部署映射；目录可读不保证部署配置可读。</Empty>}</>}
    {view && section === 'traffic' && <><section className="workspace-panel"><h2>本轮窗口聚合证据</h2><p className="sub">此处按模型、部署与渠道展示已采集日志的聚合组，不是逐请求日志或完整账本。刷新读取新的当前快照，不把重叠窗口相加；有限样本不能构成完整24小时失败率。</p></section><div className="workspace-table-scroll" tabIndex={0} aria-label="可横向滚动的数据表" role="region"><table className="workspace-table"><thead><tr><th>实例 / 模型</th><th>部署 / 渠道</th><th>样本结果</th><th className="workspace-number">成功 / 失败</th><th className="workspace-number">未知 / 缓存</th><th className="workspace-number">已知样本失败率</th><th className="workspace-time">来源窗口 / 最近样本</th></tr></thead><tbody>{view.traffic.map(item => <tr key={item.id}><td><strong>{item.model}</strong><small>{sourceMap.get(item.service_id)?.name || '实例待确认'}</small></td><td>{item.deployment_id || '部署未归属'}<small>{item.channel || '渠道未归属'}</small></td><td><Badge state={item.inference_state} freshness={item.freshness}>{sampleLabel(item.inference_state)}</Badge>{item.sampled && <small>有限样本</small>}</td><td className="workspace-number">{item.success} / {item.failure}</td><td className="workspace-number">{item.unknown} / {item.cache_hits}</td><td className="workspace-number">{failureRate(item)}</td><td className="workspace-time">{time(item.window_start)}<small>至 {time(item.window_end)}</small><small>最近 {time(item.last_at)}</small></td></tr>)}</tbody></table></div>{!view.traffic.length && <Empty>当前筛选没有可用调用聚合组。权限不足、未启用、无样本均不能解释为零调用或全部成功。</Empty>}</>}
    {view && section === 'status' && <><p className="sub">公开组件、模型或站方探针按来源分别展示；源时间缺失时明确标记，和Relay的主动测试分开。</p><div className="models-list">{view.public_status.map(item => <PublicStatusEntry key={item.service_id} item={item} />)}</div>{!view.public_status.length && <Empty>当前筛选没有公开状态来源。可在服务监控添加 Pipio、标准状态页或显式配置的 JSON / HTML 状态页。</Empty>}</>}
    {view && section === 'probes' && <section className="workspace-panel models-probes"><div className="models-probe-icon"><Icon name="pulse" /></div><h2>主动模型探针尚未实现</h2><p>{view.probes.message || '当前页面整理已有只读证据，不会发起付费推理。'}</p><div className="models-probe-grid"><article><h3>可用性</h3><p>尚无独立的模型调用执行记录。目录、网关存活和站方探针不能替代当前账号的可用性测试。</p><span className="workspace-state unknown">未运行</span></article><article><h3>能力</h3><p>流式、工具、结构化输出、视觉及上下文等能力尚未逐项测试。</p><span className="workspace-state unknown">未测试</span></article><article><h3>质量</h3><p>尚无版本化用例、同部署基线与重复评测证据，无法判断质量变化。</p><span className="workspace-state unknown">未评测</span></article></div><p className="sub">预算预留、持久调度、并发保护、暂停与费用对账闭环尚未实现，页面没有可执行的探针开关。后续启用需独立调用凭据与明确预算；本页不会发送推理请求。</p></section>}
    {view && isList && <WorkspacePagination total={view.total} page={previous.length + 1} loading={loading} hasPrevious={previous.length > 0} hasNext={!!view.has_more && !!view.next_cursor} onPrevious={() => { const stack = [...previous]; setCursor(stack.pop() || ''); setPrevious(stack); }} onNext={() => { setPrevious(items => [...items, cursor]); setCursor(view.next_cursor || ''); }} />}
    </section>
    {view && section !== 'overview' && section !== 'probes' && <details className="workspace-panel workspace-coverage"><summary>权限、时间窗与采集覆盖</summary><p className="sub">只显示当前检查快照。断流、来源截断和权限受限均不补造调用，也不将上次绿色结果当作当前测试。</p>{(source ? [source] : view.sources).map(item => <div className="models-coverage-source" key={item.id}><h3>{item.name}</h3><SourceCoverage source={item} /></div>)}</details>}
    {view && (view.coverage.truncated || !!view.coverage.notes?.length) && <aside className="models-coverage-notes">{view.coverage.truncated && <p className="models-warning">一个或多个来源超过采集上限；分页覆盖已有投影，不会补齐上游未采集的数据。</p>}{view.coverage.notes?.map((note, index) => <p className="sub" key={index}>{note}</p>)}</aside>}
  </div>;
}
function ChannelRow({ item, source }: { item: ModelsChannel; source?: ModelsSource }) { return <tr><td><strong>{item.model}</strong><small>{source?.name || '实例待确认'}</small></td><td>{item.deployment_id || '源未提供部署ID'}<small>{item.upstream_model || '上游模型未提供'}</small></td><td>{item.provider || '提供方未提供'}<small>{item.channel || '端点未归属'}</small></td><td><Badge state={item.inference_state} freshness={item.freshness}>{sampleLabel(item.inference_state)}</Badge>{source && ['available', 'partial'].includes(source.logs_state) && sampleCount(item.traffic) > 0 && <small>成功 {item.traffic.success} / 失败 {item.traffic.failure} / 未知 {item.traffic.unknown} / 缓存 {item.traffic.cache_hits}</small>}</td><td className="workspace-time">{time(item.traffic.last_at)}<small>采集 {time(item.collected_at)}</small></td></tr>; }
