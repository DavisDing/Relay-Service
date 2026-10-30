import { Button, Icon } from './ui';
import { WorkspaceEmpty, WorkspaceMetric } from './Workspace';
import { ServiceLogs } from './ServiceLogs';
import { ModelsWorkspace } from './ModelsWorkspace';
import { NetworkProxy } from './NetworkProxy';
import { readingRetained, useCockpitData } from './CockpitData';
import { time } from './presentation';
import type { CockpitSnapshot } from './CockpitData';
import type { Service } from './types';
import './cockpit.css';

type Props = {
  page: string; services: Service[]; servicesSynced: string; servicesError?: string; admin: boolean; refreshKey: number;
  onNavigate: (page: string) => void; onUnauthorized?: () => void;
};
export function LogsWorkspace(props: Props) {
  return <div className="data-workspace">
    {props.page === 'logs' && <LogsOverview {...props} />}
    {props.page === 'logs-services' && <ServiceLogs services={props.services} refreshKey={props.refreshKey} onUnauthorized={props.onUnauthorized} />}
    {props.page === 'logs-models' && <ModelsWorkspace logsOnly services={props.services} admin={props.admin} refreshKey={props.refreshKey} onUnauthorized={props.onUnauthorized} />}
    {props.page === 'logs-network' && (props.admin ? <NetworkProxy logsOnly services={props.services} admin refreshKey={props.refreshKey} onUnauthorized={props.onUnauthorized} /> : <WorkspaceEmpty>网络代理核心日志与连接详情仅管理员可查看。你仍可查看服务日志和已有大模型调用证据。</WorkspaceEmpty>)}
  </div>;
}
function LogsOverview(props: Props) {
  const snapshot = useCockpitData(props.services, props.admin, true, props.refreshKey, props.onUnauthorized);
  return <LogsOverviewView {...props} snapshot={snapshot} now={snapshot.now} />;
}

export function LogsOverviewView({ services, servicesSynced, servicesError, admin, onNavigate, snapshot, now = Date.now() }: Props & { snapshot: CockpitSnapshot; now?: number }) {
  const sources = snapshot.models.data?.sources || [];
  const modelsRetained = readingRetained(snapshot.models, now);
  const readable = sources.filter(item => item.enabled && item.freshness === 'fresh' && ['available', 'partial'].includes(item.logs_state));
  const instances = services.filter(item => item.kind === 'mihomo');
  const networkReading = instances.map(item => snapshot.networks[item.id]).filter(item => item?.data && !readingRetained(item, now));
  const persisted = networkReading.filter(item => item.data?.settings.persist_logs).length;
  const checked = services.filter(item => item.last_check_at);
  const serviceAge = now - Date.parse(servicesSynced);
  const serviceReady = !!servicesSynced && !servicesError && serviceAge >= 0 && serviceAge <= 90000;
  return <div className="data-workspace operations-cockpit">
    <section className="cockpit-hero"><div><p className="cockpit-kicker"><Icon name="history" />RELAY / 日志中心</p><h2>从变化，找到线索。</h2><p className="cockpit-intro">检查结果、模型调用与网络事件，按来源有序呈现。<br />先看采集覆盖，再进入记录。</p></div><div className="cockpit-log-principle"><span>三类记录 · 各自保留时间范围</span><strong>来源清晰。<br />缺口可见。</strong><p>此处读取 Relay 已有记录，不会开启正文采集或触发检查。</p></div></section>
    <div className="workspace-metrics"><WorkspaceMetric title="已有检查观测的服务" value={serviceReady ? checked.length : undefined} note={`共 ${services.length} 项接入 · 非日志总条数`} /><WorkspaceMetric title="调用日志可读来源" value={snapshot.models.data && !modelsRetained ? readable.length : undefined} note="有限窗口；不是完整请求账本" /><WorkspaceMetric title="代理日志持久保存" value={admin && networkReading.length ? `${persisted} / ${networkReading.length}` : undefined} note={admin ? `已确认 ${networkReading.length} / ${instances.length} 个实例的保存配置` : '管理员权限可查看'} /><WorkspaceMetric title="日志分类" value="3" note="服务 · 大模型 · 网络代理" /></div>
    <div className="cockpit-domain-grid">
      <section className="cockpit-domain"><div className="cockpit-domain-heading"><span className="cockpit-domain-icon"><Icon name="server" /></span><h2>服务日志</h2></div><p className="cockpit-domain-description">查看每次服务检查的结果、耗时和错误，追溯已保存的配置版本。</p><dl className="cockpit-mini-facts"><div><dt>记录来源</dt><dd>基础检查</dd></div><div><dt>最近同步</dt><dd>{time(servicesSynced)}</dd></div></dl><Button className="text" onClick={() => onNavigate('logs-services')}>浏览服务日志<Icon name="next" /></Button></section>
      <section className="cockpit-domain"><div className="cockpit-domain-heading"><span className="cockpit-domain-icon"><Icon name="grid" /></span><h2>大模型日志</h2></div><p className="cockpit-domain-description">按实例、模型与渠道查看调用样本聚合，核对成功、失败与缓存证据。</p><dl className="cockpit-mini-facts"><div><dt>记录形态</dt><dd>调用聚合</dd></div><div><dt>覆盖范围</dt><dd>来源窗口</dd></div></dl><p className="cockpit-footnote">不是逐请求明细或完整 24 小时统计。</p><Button className="text" onClick={() => onNavigate('logs-models')}>浏览大模型日志<Icon name="next" /></Button></section>
      <section className="cockpit-domain"><div className="cockpit-domain-heading"><span className="cockpit-domain-icon"><Icon name="pulse" /></span><h2>网络代理日志</h2></div><p className="cockpit-domain-description">检索脱敏核心日志与采集事件，查看等级、发生时间和读取状态。</p><dl className="cockpit-mini-facts"><div><dt>实时缓冲</dt><dd>短期保留</dd></div><div><dt>持久保存</dt><dd>按实例配置</dd></div></dl><Button className="text" onClick={() => onNavigate('logs-network')}>{admin ? '浏览网络代理日志' : '查看访问说明'}<Icon name="next" /></Button></section>
    </div>
    <div className="cockpit-detail-grid"><section className="workspace-panel"><h2>模型日志采集覆盖</h2>{snapshot.models.error && <p className="cockpit-source-error">{snapshot.models.error} · 保留记录已标为过期</p>}<ul className="cockpit-coverage-list">{sources.map(item => <li key={item.id}><div><strong>{item.name}</strong><small>{time(item.window_start)} — {time(item.window_end)}</small></div><span className="workspace-state unknown">{modelsRetained ? '读取待恢复' : !item.enabled ? '已暂停' : item.freshness !== 'fresh' ? '证据过期 / 未观测' : ({ available: '已读取', partial: '部分样本', unauthorized: '权限不足', disabled: '未启用', unsupported: '暂不支持', unavailable: '暂不可读', missing: '尚无数据' } as Record<string, string>)[item.logs_state] || '尚无调用证据'}</span></li>)}</ul>{!sources.length && <WorkspaceEmpty>{snapshot.models.error || '尚无模型日志来源数据。'}</WorkspaceEmpty>}</section>
    <section className="workspace-panel"><h2>代理日志采集覆盖</h2>{!admin ? <WorkspaceEmpty>代理日志采集状态和保存配置仅管理员可查看。</WorkspaceEmpty> : <><ul className="cockpit-coverage-list">{instances.map(item => { const reading = snapshot.networks[item.id]; const view = reading?.data; return <li key={item.id}><div><strong>{item.name}</strong><small>{view ? view.settings.persist_logs ? `持久保存 ${view.settings.log_retention_hours} 小时 · ${view.settings.log_limit_mib} MiB 上限` : '仅实时短缓冲 · 未开启持久保存' : '等待采集状态'}<br />采集 {time(view?.collected_at)}</small></div><span className="workspace-state unknown">{reading?.error ? '读取失败' : !item.enabled ? '已暂停' : !view ? '等待数据' : readingRetained(reading!, now) || view.state === 'stale' ? '状态过期' : ({ available: '已连接', connected: '已连接', unauthorized: '认证受限', disconnected: '已断流', connecting: '连接中' } as Record<string, string>)[view.coverage.source_states.logs] || '等待日志'}</span></li>; })}</ul>{!instances.length && <WorkspaceEmpty>尚未接入网络代理日志来源。</WorkspaceEmpty>}</>}</section></div>
  </div>;
}
