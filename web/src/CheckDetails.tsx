import type { Check, GatewayDetails } from './types';
import { time } from './presentation';

const bytes = (value: number) => value < 1024 ? `${value} B` : value < 1048576
  ? `${(value / 1024).toFixed(1)} KiB` : value < 1073741824
    ? `${(value / 1048576).toFixed(1)} MiB` : `${(value / 1073741824).toFixed(2)} GiB`;
const componentStates: Record<string, string> = {
  operational: '正常', degraded_performance: '性能下降', partial_outage: '部分故障',
  major_outage: '严重故障', under_maintenance: '维护中', healthy:'正常', unhealthy:'异常',unknown:'未知',
};
export function CheckDetails({ check }: { check?: Check }) {
  const details = check?.details;
  if (!details) return null;
  return <div className="monitor-details">
    <p className="muted">采集时间：{time(check?.completed_at)} · 来源：{details.source}</p>
    {details.source === '/providers/proxies' && !details.mihomo && <>
      <strong>订阅信息（{details.count}）</strong>
      <ul>{details.subscriptions?.map(subscription => <li key={subscription.name}>
        <b>{subscription.name}</b>：{subscription.info
          ? <>上传 {bytes(subscription.info.upload)} · 下载 {bytes(subscription.info.download)} · 总额 {subscription.info.total > 0 ? bytes(subscription.info.total) : '未提供'} · 到期 {subscription.info.expire > 0 ? time(new Date(subscription.info.expire * 1000).toISOString()) : '未提供'}</>
          : '上游未提供流量和到期信息'}
      </li>)}</ul>
      {!details.count && <p>未配置代理提供者，或此实例没有可读取的订阅。</p>}
    </>}
    {details.source === '/v1/models' && <>
      <strong>模型目录（{details.count}）</strong>
      <p>目录可读；逐模型推理状态、失败率未知。可另添加服务商公开状态页。</p>
      <ul>{details.models?.map(model => <li key={model.id}>{model.id} · 推理未测试</li>)}</ul>
      {!details.count && <p>此凭据下没有可见模型。</p>}
    </>}
    {details.source === '/api/v2/summary.json' && <>
      <strong>服务商报告的组件状态（{details.count}）</strong>
      <ul>{details.components?.map((component, index) => <li key={index}>{component.name} · {componentStates[component.status] || '未知'}</li>)}</ul>
      <p>仅表示服务商公布的状态，不代表当前账号或网络下的实际推理结果。</p>
    </>}
    {details.public_status && <>
      <strong>服务商公开状态 / 站方探针（{details.public_status.count}）</strong>
      <p className="muted">上游更新时间：{details.public_status.updated_at ? time(details.public_status.updated_at) : '未提供，不能据采集时间证明探针新鲜度'}</p>
      <ul>{details.public_status.components.map((item,index) => <li key={index}>
        <b>{item.name}</b> · {componentStates[item.status] || '未知'}
        {item.category && <> · {item.category}</>}{item.group && <> · {item.group}</>}
        {item.uptime != null && <> · 站方可用率 {(item.uptime * 100).toFixed(2)}%</>}
        {item.updated_at && <> · 上游时间 {time(item.updated_at)}</>}
        {!!item.heartbeats?.length && <small>站方最近 {item.heartbeats.length} 条心跳：{item.heartbeats.map(state => componentStates[state] || '未知').join(' / ')}（上游未提供逐条时间）</small>}
      </li>)}</ul>
      <p>仅表示服务商公布的状态，不代表当前账号或网络下的实际推理结果。</p>
    </>}
    {details.mihomo && <>
      <strong>机场订阅额度（{details.mihomo.subscription_count}）</strong>
      <ul>{details.mihomo.subscriptions.map(sub => <li key={sub.name}><b>{sub.name}</b> · {({unknown:'额度未知',available:'有剩余额度',exhausted:'额度已耗尽',expired:'已到期'} as Record<string,string>)[sub.usage_state] || '未知'}
        {sub.info && <> · 上传 {sub.upload_bytes != null ? bytes(sub.upload_bytes) : '未提供'} · 下载 {sub.download_bytes != null ? bytes(sub.download_bytes) : '未提供'} · 总额 {sub.total_bytes != null && sub.total_bytes > 0 ? bytes(sub.total_bytes) : '未提供'} · 到期 {sub.expire_seconds != null && sub.expire_seconds > 0 ? time(new Date(sub.expire_seconds*1000).toISOString()) : '未提供'}</>}
        {sub.used_bytes != null && <> · 已用 {bytes(sub.used_bytes)}</>}{sub.remaining_bytes != null && <> · 剩余 {bytes(sub.remaining_bytes)}</>}
        {sub.updated_at && <small>订阅更新时间 {time(sub.updated_at)}</small>}
      </li>)}</ul>
      <strong>节点已有质量记录（{details.mihomo.node_count}）</strong>
      <p>近期正常 {details.mihomo.healthy_nodes} · 异常 {details.mihomo.unhealthy_nodes} · 未知 {details.mihomo.unknown_nodes}，其中过期 {details.mihomo.stale_nodes}。超过15分钟的记录显示过期；未主动测速。</p>
      {details.mihomo.proxy_source_state !== 'available' && <p>补充节点源未完整读取，以下仅含可取得的提供者节点。</p>}
      <ul>{details.mihomo.nodes.map((node,index) => <li key={index}><b>{node.name}</b>{node.provider && <> · {node.provider}</>} · {node.type} · {componentStates[node.state] || '未知'}
        {node.delay_ms != null && <> · 记录延迟 {node.delay_ms} ms</>} · {({fresh:'近期记录',stale:'记录过期',future:'时间异常',missing:'无检测记录'} as Record<string,string>)[node.freshness] || '未知'}
        {node.checked_at && <small>{time(node.checked_at)}</small>}
      </li>)}</ul>
      {details.mihomo.truncated && <p>订阅或节点超过200项，各展示前200项。</p>}
    </>}
    {details.gateway && <GatewayEvidence details={details.gateway} />}
    {details.truncated && !details.mihomo && <p>共 {details.count} 项，本次仅展示前 200 项。</p>}
  </div>;
}

const sourceStates: Record<string,string> = { available:'已读取',disabled:'未启用',unavailable:'暂不可读取',invalid:'格式不支持',partial:'部分样本',unauthorized:'权限不足',unsupported:'当前安装不支持' };
function GatewayEvidence({ details:d }: { details: GatewayDetails }) {
  return <>
    <strong>网关模型、部署与调用证据</strong>
    <p>目录：{sourceStates[d.catalog_state] || '未知'} · 渠道部署：{sourceStates[d.deployments_state] || '未知'} · 日志：{sourceStates[d.logs_state] || '未知'}</p>
    <p>模型目录 {d.model_count} 项；目录可见不代表推理可用。</p>
    <ul>{d.models?.map(model => <li key={model.id}>{model.id} · 推理未测试</li>)}</ul>
    <strong>各渠道模型部署（{d.deployment_count}）</strong>
    <ul>{d.deployments?.map((item,index) => <li key={index}><b>{item.model}</b>{item.upstream_model && <> → {item.upstream_model}</>}{item.id && <> · 部署 {item.id}</>}{item.provider && <> · 协议 {item.provider}</>}{item.channel && <> · 渠道分组 {item.channel}</>}</li>)}</ul>
    <p>渠道分组为端点的匿名标识；相同别名的不同部署分别展示。未提供部署映射时无法断定具体渠道。</p>
    <strong>已有调用样本</strong>
    <p>{time(d.window_start)} 至 {time(d.window_end)} · 有效样本 {d.rows} 条 / 上游报告 {d.total} 条{d.skipped > 0 && <> · 已跳过 {d.skipped} 条无效或窗口外记录</>} · 最近样本 {time(d.latest_at)}</p>
    {d.sampled && <p>分页或来源不完整，以下为有限样本，不能当作完整24小时失败率。</p>}
    <ul>{d.traffic?.map((item,index) => {
      const attempts=item.success+item.failure;
      return <li key={index}><b>{item.model}</b>{item.deployment_id && <> · 部署 {item.deployment_id}</>}{item.channel && <> · {item.channel}</>}
        {' '}· 成功 {item.success} · 失败 {item.failure} · 未知 {item.unknown} · 缓存 {item.cache_hits}
        {' '}· 已知结果样本失败率 {attempts ? `${(100*item.failure/attempts).toFixed(1)}%` : '未知'}<small>最近样本 {time(item.last_at)}</small></li>;
    })}</ul>
    {!d.traffic?.length && <p>没有可用于判断模型可用性的已有调用样本。</p>}
    {d.issues?.map((item,index) => <p key={index}>{item.source}：{({ source_unavailable:'来源暂不可读取',invalid_response:'响应格式不支持',authentication_failed:'读取权限不足',endpoint_unsupported:'当前安装不支持该接口'} as Record<string,string>)[item.class] || '来源读取受限'}</p>)}
    {d.truncated && <p>目录、部署或日志超过采集上限，显示有限清单。</p>}
    <p>调用样本是历史证据，不表示当前实时可用；缓存命中不计入上游成功。这里没有发起主动推理。</p>
  </>;
}
