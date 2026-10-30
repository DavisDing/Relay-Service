import { Button, Icon } from './ui';
import { WorkspaceEmpty } from './Workspace';
import { serviceStatus, summarizeServices } from './ServicesOverview';
import { message, states, time } from './presentation';
import { networkIsCurrent, readingRetained, useCockpitData } from './CockpitData';
import type { CockpitSnapshot } from './CockpitData';
import type { Service } from './types';
import './cockpit.css';

type Props = {
  services: Service[]; servicesSynced: string; servicesError?: string; admin: boolean;
  refreshKey: number; onNavigate: (page: string, serviceID?: string) => void; onUnauthorized?: () => void;
};
export function HomeCockpit(props: Props) {
  const data = useCockpitData(props.services, props.admin, false, props.refreshKey, props.onUnauthorized);
  return <HomeCockpitView {...props} snapshot={data} now={data.now} />;
}

export function HomeCockpitView({ services, servicesSynced, servicesError, admin, onNavigate, snapshot, now = Date.now() }: Props & { snapshot: CockpitSnapshot; now?: number }) {
  const serviceAge = now - Date.parse(servicesSynced);
  const ready = !!servicesSynced && !servicesError && serviceAge >= 0 && serviceAge <= 90000;
  const counts = summarizeServices(services);
  const incidents = services.filter(item => item.open_incident || item.open_restriction);
  const models = snapshot.models.data;
  const modelCurrent = !!models && !readingRetained(snapshot.models, now);
  const instances = services.filter(item => item.kind === 'mihomo');
  const networkCurrent = instances.filter(item => networkIsCurrent(snapshot.networks[item.id] || {}, undefined, now));
  const networkValues = networkCurrent.map(item => snapshot.networks[item.id].data!);
  const subscriptions = networkValues.reduce((sum, item) => sum + item.overview.subscription_count, 0);
  const nodes = networkValues.reduce((sum, item) => sum + item.overview.node_count, 0);
  const observed = services.filter(item => item.current && item.last_check_at).sort((a, b) => Date.parse(b.last_check_at!) - Date.parse(a.last_check_at!)).slice(0, 5);
  const attention = services.filter(item => item.open_incident || item.open_restriction || ['unhealthy', 'unauthorized', 'unsupported', 'interrupted'].includes(serviceStatus(item))).slice(0, 4);
  const modelGaps = modelCurrent ? models!.sources.filter(item => item.enabled && (item.freshness !== 'fresh' || item.catalog_state === 'unauthorized' || item.logs_state === 'unauthorized')).length : 0;
  return <div className="data-workspace operations-cockpit">
    <section className="cockpit-hero" aria-labelledby="home-overview-title">
      <div><p className="cockpit-kicker"><Icon name="pulse" />RELAY / 全局概览</p><h2 id="home-overview-title">运行全貌，一览即知。</h2><p className="cockpit-intro">从服务基础检查，到模型证据与代理观测。<br />在一个地方找到需要关注的变化。</p><div className="cockpit-hero-actions"><Button className="secondary" onClick={() => onNavigate('services')}>查看服务监控<Icon name="next" /></Button><Button className="text" onClick={() => onNavigate('logs')}>浏览日志<Icon name="history" /></Button></div></div>
      <div className="cockpit-pulse"><span>启用服务 · 当前基础检查</span><strong>{ready ? counts.healthy : '—'}<small>/ {ready ? services.length - counts.paused : '—'}</small></strong><p>{!ready ? '正在等待有效同步' : !services.length ? '尚未接入服务' : counts.failed ? `${counts.failed} 项检查异常` : counts.unknown || counts.pending ? '仍有待观测或检查中的服务' : '已取得的基础检查正常'}</p><div className="cockpit-signal" aria-label="当前服务状态分布">{Object.entries(ready ? counts : { unknown: Math.max(services.length, 1) }).map(([key, value]) => value > 0 && <span className={`signal-${key}`} key={key} style={{ flex: value }} title={`${({ healthy: '成功', failed: '异常', unknown: '待观测', pending: '检查中', paused: '暂停' } as Record<string, string>)[key]} ${value} 项`} />)}</div><small>当前快照，不代表模型推理或历史可用率</small></div>
    </section>
    <div className="cockpit-domain-grid">
      <section className="cockpit-domain"><div className="cockpit-domain-heading"><span className="cockpit-domain-icon"><Icon name="server" /></span><h2>服务监控</h2><span className={`workspace-state ${ready && counts.failed ? 'unhealthy' : 'unknown'}`}>{ready ? `${services.length} 项接入` : '同步待确认'}</span></div><div className="cockpit-domain-number">{ready ? counts.healthy : '—'}<small>项基础检查成功</small></div><dl className="cockpit-mini-facts"><div><dt>检查异常</dt><dd>{ready ? counts.failed : '—'}</dd></div><div><dt>待观测 / 检查中</dt><dd>{ready ? counts.unknown + counts.pending : '—'}</dd></div><div><dt>已暂停</dt><dd>{ready ? counts.paused : '—'}</dd></div></dl><p className="cockpit-footnote">同步 {time(servicesSynced)}</p><Button className="text" onClick={() => onNavigate('services')}>进入服务监控<Icon name="next" /></Button></section>
      <section className="cockpit-domain"><div className="cockpit-domain-heading"><span className="cockpit-domain-icon"><Icon name="grid" /></span><h2>大模型</h2><span className="workspace-state unknown">{modelCurrent ? '只读证据' : models ? '保留数据' : '等待数据'}</span></div><div className="cockpit-domain-number">{models ? models.summary.models : '—'}<small>个已收集模型别名</small></div><dl className="cockpit-mini-facts"><div><dt>部署映射</dt><dd>{models?.summary.deployments ?? '—'}</dd></div><div><dt>接入来源</dt><dd>{models?.summary.services ?? '—'}</dd></div><div><dt>公开状态源</dt><dd>{models?.summary.public_status_services ?? '—'}</dd></div></dl><p className="cockpit-footnote">目录与历史调用分别展示 · 主动探针尚未实现</p>{snapshot.models.error && <p className="cockpit-source-error">{snapshot.models.error}</p>}<Button className="text" onClick={() => onNavigate('models')}>查看模型与渠道<Icon name="next" /></Button></section>
      <section className="cockpit-domain"><div className="cockpit-domain-heading"><span className="cockpit-domain-icon"><Icon name="pulse" /></span><h2>网络代理</h2><span className="workspace-state unknown">{networkCurrent.length} / {instances.length} 个实例有效</span></div><div className="cockpit-domain-number">{networkValues.length ? subscriptions : '—'}<small>项已读取订阅</small></div><dl className="cockpit-mini-facts"><div><dt>节点目录</dt><dd>{networkValues.length ? nodes : '—'}</dd></div><div><dt>检测成功</dt><dd>{networkValues.length ? networkValues.reduce((sum, item) => sum + item.overview.healthy_nodes, 0) : '—'}</dd></div><div><dt>检测失败</dt><dd>{networkValues.length ? networkValues.reduce((sum, item) => sum + item.overview.unhealthy_nodes, 0) : '—'}</dd></div></dl><p className="cockpit-footnote">仅汇总有效实例；目录节点可能没有检测记录</p><Button className="text" onClick={() => onNavigate('network')}>查看代理与订阅<Icon name="next" /></Button></section>
    </div>
    <div className="cockpit-detail-grid">
      <section className="workspace-panel"><div className="cockpit-panel-heading"><h2>需要关注</h2><Button className="text" onClick={() => onNavigate('alerts')}>查看告警<Icon name="next" /></Button></div><div className="cockpit-attention-list">
        {!!incidents.length && <p className="cockpit-attention-note"><Icon name="bell" />{incidents.length} 项服务有未恢复事件{!ready && ' · 最近数据'}</p>}
        {attention.map(item => <Button className="cockpit-observation" key={item.id} onClick={() => onNavigate('services')}><span className="cockpit-row-icon"><Icon name="bell" /></span><span><strong>{item.name}</strong><small>{!item.enabled ? '已暂停，事件仍未恢复' : message(item.current?.message) || states[serviceStatus(item)] || '需要核对状态'}</small></span><Icon name="next" /></Button>)}
        {modelGaps > 0 && <Button className="cockpit-observation" onClick={() => onNavigate('models')}><span className="cockpit-row-icon"><Icon name="grid" /></span><span><strong>{modelGaps} 个模型来源需要核对</strong><small>证据过期或采集权限不足</small></span><Icon name="next" /></Button>}
        {instances.filter(item => !networkIsCurrent(snapshot.networks[item.id] || {}, undefined, now)).map(item => <Button className="cockpit-observation" key={item.id} onClick={() => onNavigate('network', item.id)}><span className="cockpit-row-icon"><Icon name="pulse" /></span><span><strong>{item.name}</strong><small>{snapshot.networks[item.id]?.error || (!item.enabled ? '采集已暂停' : '等待有效代理观测')}</small></span><Icon name="next" /></Button>)}
        {!attention.length && !modelGaps && instances.length === networkCurrent.length && <WorkspaceEmpty>{ready ? '已取得的记录中暂无待处理异常。' : '完成首次同步后显示需要关注的事项。'}</WorkspaceEmpty>}
      </div></section>
      <section className="workspace-panel"><div className="cockpit-panel-heading"><h2>最近服务观测</h2><Button className="text" onClick={() => onNavigate('logs-services')}>检查日志<Icon name="next" /></Button></div><ol className="cockpit-observations">{observed.map(item => <li key={item.id}><span className={`workspace-state ${ready && serviceStatus(item) === 'healthy' ? 'healthy' : ready && serviceStatus(item) === 'unhealthy' ? 'unhealthy' : 'unknown'}`}>{states[serviceStatus(item)] || '待观测'}</span><div><strong>{item.name}</strong><small>{time(item.last_check_at)}</small></div><span>{item.current?.duration_ms !== undefined ? `${item.current.duration_ms} ms` : '—'}</span></li>)}</ol>{!observed.length && <WorkspaceEmpty>尚无服务检查记录。</WorkspaceEmpty>}</section>
    </div>
    <section className="cockpit-log-strip"><div><Icon name="history" /><span><strong>日志，按来源追踪。</strong><small>检查记录、调用证据与代理日志各有清晰的时间边界。</small></span></div><div className="buttons"><Button className="text" onClick={() => onNavigate('logs-services')}>服务日志<Icon name="next" /></Button><Button className="text" onClick={() => onNavigate('logs-models')}>大模型日志<Icon name="next" /></Button><Button className="text" onClick={() => onNavigate('logs-network')}>网络代理日志{!admin && ' · 管理员'}<Icon name="next" /></Button></div></section>
  </div>;
}
