import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import { test } from 'node:test';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import ts from 'typescript';

const require = createRequire(import.meta.url);
const modules = new Map();
function load(name) {
  if (modules.has(name)) return modules.get(name);
  const filename = new URL(`../src/${name}`, import.meta.url);
  const code = ts.transpileModule(readFileSync(filename, 'utf8'), {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX },
  }).outputText;
  const module = { exports: {} };
  new Function('require', 'module', 'exports', code)(specifier => {
    if (!specifier.startsWith('.')) return require(specifier);
    if (specifier.endsWith('.css')) return {};
    return load(specifier.slice(2) + (['./presentation', './api', './solar'].includes(specifier) ? '.ts' : '.tsx'));
  }, module, module.exports);
  modules.set(name, module.exports);
  return module.exports;
}
const { ServicesOverview, serviceStatus, summarizeServices, needsAttention, filterServices } = load('ServicesOverview.tsx');
const { Icon, ThemeControl, Toast, Button } = load('ui.tsx');
const { AlertsPanel } = load('AlertsPanel.tsx');
const { RetentionPanel } = load('RetentionPanel.tsx');
const { Navigation } = load('Navigation.tsx');
const { modelPages, networkPages, workspaceDestination, modelPage, networkPage } = load('NavigationRoutes.tsx');
const { ModelsWorkspace } = load('ModelsWorkspace.tsx');
const { NetworkProxy } = load('NetworkProxy.tsx');
const { HomeCockpitView } = load('HomeCockpit.tsx');
const { LogsWorkspace, LogsOverviewView } = load('LogsWorkspace.tsx');
const { filterServiceLogs, ServiceLogTable } = load('ServiceLogs.tsx');
const { readCockpit, networkIsCurrent } = load('CockpitData.tsx');
const { APIError: CockpitAPIError } = load('api.ts');
// Inspect styles in the same directory as the components scanned below. A
// workspace can own a stylesheet without adding it to this test's file list.
const css = readdirSync(new URL('../src/', import.meta.url)).filter(file => file.endsWith('.css')).sort()
  .map(file => readFileSync(new URL('../src/' + file, import.meta.url), 'utf8')).join('\n');
const noop = () => {};
const base = {
  id: 'a', name: 'LiteLLM', kind: 'litellm', group: '核心', base_url: 'http://example.invalid',
  enabled: true, interval_seconds: 86400, timeout_seconds: 30, failure_threshold: 3,
  recovery_threshold: 2, allow_private: false, generation: 1, status: 'healthy',
  last_check_at: '2026-10-27T12:00:00Z', freshness: 'fresh', evidence_level: 'process',
  current: { status: 'healthy', duration_ms: 123, message: 'safe <script> rendered as text' },
};
const fixtureServices = [
  base,
  { ...base, id: 'b', name: 'Hermes', status: 'unhealthy', current: { status: 'unhealthy', duration_ms: 800, message: 'upstream unavailable' }, open_incident: true },
  { ...base, id: 'c', name: 'Stale', freshness: 'stale', status: 'healthy', last_check_at: '2026-10-20T12:00:00Z' },
  { ...base, id: 'd', name: 'Paused with open event', enabled: false, status: 'unhealthy', open_incident: true },
  { ...base, id: 'e', name: 'Pending', status: 'running', last_check_at: undefined },
  { ...base, id: 'f', name: 'Other group', group: '其他', current: { status: 'healthy', duration_ms: 23 } },
];
function render(overrides = {}) {
  return renderToStaticMarkup(React.createElement(ServicesOverview, {
    services: [base], alerts: [], servicesSynced: '2026-10-27T12:00:00Z', alertsSynced: '',
    alertsCurrent: true, alertsComplete: true, admin: true, busy: false,
    onAdd: noop, onAlerts: noop, onHistory: noop, onEdit: noop, onToggle: noop, onCheck: noop,
    ...overrides,
  }));
}

test('cockpit summarizes snapshot coverage, alerting groups, observations and compact service details', () => {
  const summary = summarizeServices(fixtureServices);
  assert.deepEqual(summary, { healthy: 2, failed: 1, unknown: 1, pending: 1, paused: 1 });
  const html = render({ services: fixtureServices });
  assert.match(html, /运行态势/);
  assert.match(html, /当前检查成功占比/);
  assert.match(html, /当前快照 · 非历史可用率/);
  assert.match(html, /需要关注 <span>3/);
  assert.match(html, /Paused with open event/);
  assert.match(html, /最近检查/);
  assert.match(html, /服务明细 <span>6 \/ 6/);
  assert.match(html, /核心/);
  assert.match(html, /检查凭据/);
  assert.match(html, /每 86400s · 30s 超时/);
  assert.match(html, /&lt;script&gt;/);
});

test('stale, never-observed and paused services do not count healthy; paused unresolved events stay visible', () => {
  for (const changes of [{ freshness: 'stale' }, { last_check_at: undefined }]) {
    const candidate = { ...base, ...changes };
    assert.notEqual(serviceStatus(candidate), 'healthy');
    assert.equal(summarizeServices([candidate]).healthy, 0);
    assert.equal(needsAttention(candidate), true);
  }
  const paused = { ...base, enabled: false, open_incident: true };
  assert.equal(serviceStatus(paused), 'paused');
  assert.equal(needsAttention(paused), true);
  assert.equal(summarizeServices([paused]).paused, 1);
  assert.match(render({ servicesSynced: '', services: [] }), /等待服务数据/);
  assert.match(render({ servicesSynced: 'now', services: [] }), /从接入第一个服务开始/);
});

test('queued and running remain in-progress before a first observation', () => {
  for (const status of ['queued', 'running']) {
    assert.equal(serviceStatus({ ...base, last_check_at: undefined, status }), status);
  }
});

test('service filters combine attention, group, pending, paused and case-insensitive search', () => {
  const ids = (group, status, query = '') => filterServices(fixtureServices, group, status, query).map(service => service.id);
  assert.deepEqual(ids(null, 'attention'), ['b', 'c', 'd']);
  assert.deepEqual(ids('其他', 'attention'), []);
  assert.deepEqual(ids(null, 'pending'), ['e']);
  assert.deepEqual(ids(null, 'paused'), ['d']);
  assert.deepEqual(ids('核心', 'attention', '  HERMES  '), ['b']);
  assert.deepEqual(ids('其他', 'all'), ['f']);
  assert.deepEqual(filterServices([{ ...base, group: '' }], '', 'all', ''), [{ ...base, group: '' }]);
});

test('device rendering hides administrative service actions', () => {
  const html = render({ admin: false });
  assert.doesNotMatch(html, /<button[^>]*>(?:编辑|暂停|检查)<\/button>/);
  assert.doesNotMatch(html, /添加服务/);
  assert.match(html, /历史/);
  assert.match(html, /检查历史|历史/);
});

test('partial alert pages are not labeled as the newest global event', () => {
  const alerts = [{ cursor: 3, service_id: 'a', message: 'older' }, { cursor: 9, service_id: 'a', message: 'newest' }];
  assert.match(render({ alerts, alertsSynced: 'now' }), /最近事件：LiteLLM · newest/);
  assert.doesNotMatch(render({ alerts, alertsSynced: 'now', alertsCurrent: false }), /最近事件：/);
  assert.match(render({ alerts, alertsSynced: 'now', alertsComplete: false }), /已加载事件：/);
  assert.doesNotMatch(render({ alerts, alertsSynced: 'now', alertsComplete: false }), /最近事件：/);
});

test('rendered UI classes have CSS rules and cockpit layout remains responsive', () => {
  const statuses = ['healthy', 'unhealthy', 'unauthorized', 'unknown', 'queued', 'running', 'interrupted', 'unsupported', 'paused'];
  const html = statuses.map(status => render({ services: [{ ...base, status, enabled: status !== 'paused' }] })).join('')
    + renderToStaticMarkup(React.createElement(ThemeControl))
    + renderToStaticMarkup(React.createElement(Toast, { message: 'saved', onDismiss: noop }))
    + renderToStaticMarkup(React.createElement(Button, { loading: true }, 'check'))
    + renderToStaticMarkup(React.createElement(Navigation, { active: 'services', admin: true, busy: false, incidentCount: 2, onNavigate: noop, onLogout: noop }))
    + renderToStaticMarkup(React.createElement(RetentionPanel, { onNotice: noop, onUnauthorized: noop }));
  const classes = new Set([...html.matchAll(/class="([^"]+)"/g)].flatMap(match => match[1].split(/\s+/)));
  for (const file of readdirSync(new URL('../src/', import.meta.url)).filter(file => file.endsWith('.tsx'))) {
    const source = readFileSync(new URL('../src/' + file, import.meta.url), 'utf8');
    for (const match of source.matchAll(/className="([^"]+)"/g)) for (const name of match[1].split(/\s+/)) classes.add(name);
  }
  for (const name of classes) assert.match(css, new RegExp(`\\.${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?=[\\s,.#:{\\[]|$)`), `missing CSS: ${name}`);
  for (const status of statuses) assert.ok(css.includes(`.status.${status}`));
  const svg = renderToStaticMarkup(React.createElement(Icon, { name: 'pulse' }));
  assert.match(svg, /width="22" height="22"/);
  assert.match(css, /:root\[data-theme="dark"\]/);
  assert.match(css, /\.icon-button\s*\{[^}]*min-width:\s*44px/s);
  assert.match(css, /\.cockpit-top\s*\{[^}]*grid-template-columns/s);
  assert.match(css, /@media \(max-width:760px\)\s*\{[^}]*\.cockpit-top,\.cockpit-streams\s*\{\s*grid-template-columns:\s*1fr/s);
});

test('navigation exposes real workspaces, hides admin pages for devices and makes closed panels inert', () => {
  const nav = admin => renderToStaticMarkup(React.createElement(Navigation, {
    active: 'alerts', admin, busy: false, incidentCount: 2, onNavigate: noop, onLogout: noop,
  }));
  const html = nav(true);
  for (const name of ['服务监控', '故障与告警', '访问设备', '系统设置', '诊断', '外观与账户', '退出登录']) assert.ok(html.includes(name));
  assert.match(html, /aria-current="page"/);
  assert.match(html, /2 个服务有未恢复事件/);
  assert.match(html, /id="nav-monitoring"[^>]*aria-hidden="true"[^>]*inert=""/);
  assert.match(html, /aria-controls="nav-management"/);
  assert.match(html, /aria-controls="mobile-navigation"/);
  assert.match(html, /href="#main-content"/);
  const device = nav(false);
  for (const name of ['访问设备', '系统设置', '诊断摘要', 'nav-management']) assert.ok(!device.includes(name));
  assert.match(device, /只读设备/);
  assert.match(device, /故障与告警/);
});

test('navigation motion is bounded, respects preferences and mobile reuses the modal focus guard', () => {
  assert.match(css, /@media \(prefers-reduced-motion:reduce\)/);
  assert.match(css, /\.workspace-content,\.navigation-dialog\s*\{\s*animation:\s*none/);
  assert.match(css, /@media \(prefers-reduced-transparency:reduce\)/);
  assert.match(css, /@media \(prefers-contrast:more\)/);
  const source = readFileSync(new URL('../src/Navigation.tsx', import.meta.url), 'utf8');
  assert.match(source, /<Modal titleId="mobile-navigation-title"/);
  assert.match(source, /event.key === 'Escape'/);
  assert.match(source, /event.key === 'ArrowDown'/);
  assert.match(source, /relatedTarget/);
  const main = readFileSync(new URL('../src/main.tsx', import.meta.url), 'utf8');
  assert.match(main, /key=\{tab\} className="workspace-content"/);
});

test('all workspace sections share top-menu destinations and mark their current secondary page', () => {
  assert.equal(modelPages.length, 6);
  assert.equal(networkPages.length, 6);
  for (const [workspace, pages, lookup] of [['models', modelPages, modelPage], ['network', networkPages, networkPage]]) {
    for (const page of pages) {
      assert.deepEqual(workspaceDestination(page.key), { workspace, section: page.section });
      assert.equal(lookup(page.section).key, page.key);
      const html = renderToStaticMarkup(React.createElement(Navigation, { active: page.key, admin: true, busy: false, incidentCount: 0, onNavigate: noop, onLogout: noop }));
      assert.match(html, new RegExp(`aria-controls="nav-${workspace}"`));
      assert.ok(html.includes(`aria-current="page"><span>${page.label}</span>`));
    }
  }
  assert.equal(workspaceDestination('logs-models'), undefined);
  assert.equal(workspaceDestination('network-unknown'), undefined);
});

test('workspace bodies omit secondary navigation while retaining source filters and restricted states', () => {
  const model = renderToStaticMarkup(React.createElement(ModelsWorkspace, { services: [], admin: false, section: 'channels' }));
  assert.match(model, /模型 \/ 公开状态来源/);
  assert.match(model, /核对模型部署、上游提供方与渠道证据/);
  const network = renderToStaticMarkup(React.createElement(NetworkProxy, { services: [{ ...base, kind: 'mihomo' }], admin: false, section: 'connections' }));
  assert.match(network, /仅管理员可查看/);
  const logs = renderToStaticMarkup(React.createElement(LogsWorkspace, { page: 'logs-services', services: [], servicesSynced: '', admin: false, refreshKey: 0, onNavigate: noop }));
  for (const html of [model, network, logs]) {
    assert.doesNotMatch(html, /workspace-tabs/);
    assert.doesNotMatch(html, /aria-label="(?:大模型视图|网络代理视图|日志视图)"/);
  }
});

test('alert panel preserves evidence, device read access and sequential ack guards', () => {
  const html = renderToStaticMarkup(React.createElement(AlertsPanel, {
    services: [base], alerts: [{ id: 'alert', cursor: 2, service_id: 'a', kind: 'failure',
      message: 'unavailable', started_at: '2026-10-27T12:00:00Z', count: 3, generation: 1,
      evidence_state: 'expired', severity: 'warning' }],
    ackCursor: 0, busy: false, admin: false, skippedUnreadPage: true, canAck: false,
    hasPrevious: true, canNext: false, onAck: noop, onSilence: noop, onPrevious: noop, onNext: noop,
  }));
  assert.match(html, /检查明细已过保留期/);
  assert.match(html, /未读/);
  assert.match(html, /disabled=""[^>]*>本页标记已读/);
  assert.doesNotMatch(html, /静默 1 小时/);
});

test('subscription and model details expose unknown evidence instead of inventing health or quotas', () => {
  const { CheckDetails } = load('CheckDetails.tsx');
  const subscription = renderToStaticMarkup(React.createElement(CheckDetails, { check: { completed_at: '2026-10-28T00:00:00Z', details: {
    source: '/providers/proxies', count: 2, subscriptions: [{name:'no-info'}, {name:'zero-fields', info:{upload:0,download:0,total:0,expire:0}}],
  } } }));
  assert.match(subscription, /上游未提供流量和到期信息/);
  assert.match(subscription, /到期 未提供/);
  assert.doesNotMatch(subscription, /1970|无限|健康/);
  const models = renderToStaticMarkup(React.createElement(CheckDetails, { check: { details: {
    source: '/v1/models', count: 201, truncated: true, models: [{ id: '<script>model</script>', status:'not_tested'}],
  } } }));
  assert.match(models, /推理未测试/);
  assert.match(models, /失败率未知/);
  assert.match(models, /仅展示前 200 项/);
  assert.match(models, /&lt;script&gt;/);
});

test('public status component labels remain distinct from actual inference evidence', () => {
  const { CheckDetails } = load('CheckDetails.tsx');
  const html = renderToStaticMarkup(React.createElement(CheckDetails, {check:{details:{
    source:'/api/v2/summary.json',count:1,components:[{name:'Model API',status:'major_outage'}],
  }}}));
  assert.match(html,/严重故障/);
  assert.match(html,/服务商公布的状态/);
  assert.match(html,/当前账号或网络下的实际可用性还需独立证据/);
});

test('Pipio reports its own evidence and fractional uptime without inventing source time', () => {
  const { CheckDetails } = load('CheckDetails.tsx');
  const html=renderToStaticMarkup(React.createElement(CheckDetails,{check:{details:{source:'/api/uptime/status',count:1,public_status:{count:1,components:[{name:'example',status:'unhealthy',uptime:0.995,heartbeats:['healthy','unhealthy']} ]}}}}));
  assert.match(html,/99.50%/);
  assert.match(html,/上游未提供逐条时间/);
  assert.match(html,/未提供，不能据采集时间证明探针新鲜度/);
  assert.doesNotMatch(html,/过去24小时|实时可用|推理通过/);
});

test('gateway samples expose unknown and cache results independently of model catalog', () => {
  const { CheckDetails }=load('CheckDetails.tsx');
  const html=renderToStaticMarkup(React.createElement(CheckDetails,{check:{details:{source:'gateway',gateway:{catalog_state:'available',deployments_state:'unauthorized',logs_state:'partial',model_count:1,deployment_count:0,models:[{id:'model-a'}],rows:4,total:500,sampled:true,traffic:[{model:'model-a',success:1,failure:1,unknown:1,cache_hits:1,last_at:'2026-10-28T01:00:00Z'}]}}}}));
  assert.match(html,/权限不足/);assert.match(html,/有限样本/);assert.match(html,/50.0%/);assert.match(html,/未知 1 · 缓存 1/);assert.match(html,/目录可见不代表推理可用/);
});

test('mihomo quota and existing node history preserve stale evidence labels', () => {
  const { CheckDetails }=load('CheckDetails.tsx');
  const html=renderToStaticMarkup(React.createElement(CheckDetails,{check:{details:{source:'/providers/proxies',mihomo:{subscription_count:1,subscriptions:[{name:'airport',usage_state:'available',used_bytes:1024,remaining_bytes:2048}],node_count:1,healthy_nodes:0,unhealthy_nodes:0,unknown_nodes:1,stale_nodes:1,proxy_source_state:'available',nodes:[{name:'old-node',type:'ss',state:'unknown',delay_ms:35,freshness:'stale'}]}}}}));
  assert.match(html,/剩余 2.0 KiB/);assert.match(html,/记录过期/);assert.match(html,/未主动测速/);assert.match(html,/old-node/);
});

const { WorkspacePagination } = load('Workspace.tsx');
const { NodeAvailability, SubscriptionCard, NodeSubscription } = load('NetworkProxy.tsx');
test('node membership shows merged profile names and labels unmatched core evidence explicitly', () => {
  const membership = (node, subscriptions = []) => renderToStaticMarkup(React.createElement(NodeSubscription, { node, subscriptions }));
  assert.equal(membership({ subscriptions: [{ id: 'provider', name: 'provider' }, { id: 'profile', name: 'start' }] }), 'provider、start');
  assert.equal(membership({ loaded: true }), '核心节点 · 归属待确认');
  assert.equal(membership({ subscription_id: 'profile' }, [{ id: 'profile', name: '1' }]), '1');
});
test('subscription expiry distinguishes unlimited duration from missing data and dated plans', () => {
  const card = expire => renderToStaticMarkup(React.createElement(SubscriptionCard, {
    item: { id: 'profile', name: 'plan', source: 'control', usage_state: 'available', expire_seconds: expire }, onNodes: noop,
  }));
  assert.match(card(0), /<dt>到期<\/dt><dd>不限时<\/dd>/);
  assert.match(card(undefined), /到期信息未知/);
  assert.doesNotMatch(card(undefined), /不限时/);
  const dated = card(Date.parse('2031-12-04T00:00:00Z') / 1000);
  assert.match(dated, /2031/);
  assert.doesNotMatch(dated, /不限时|到期信息未知|未提供/);
});
test('workspace pagination shares secondary controls and explicit disabled boundaries', () => {
  const html = renderToStaticMarkup(React.createElement(WorkspacePagination, {
    total: 25, page: 1, hasPrevious: false, hasNext: false, onPrevious: noop, onNext: noop,
  }));
  assert.match(html, /共 25 条 · 第 1 页/);
  assert.equal((html.match(/class="secondary"/g) || []).length, 2);
  assert.equal((html.match(/disabled=""/g) || []).length, 2);
});
test('node availability presents distinct sample ratio and leaves missing history unknown', () => {
  const node = { id: 'node', name: 'HK', type: 'AnyTLS', state: 'healthy', loaded: true, freshness: 'fresh' };
  const missing = renderToStaticMarkup(React.createElement(NodeAvailability, { node }));
  assert.match(missing, /暂无有效检测/);
  assert.doesNotMatch(missing, /100.0%/);
  const html = renderToStaticMarkup(React.createElement(NodeAvailability, { node: { ...node, availability_24h: {
    start_at: '2026-10-28T10:00:00Z', end_at: '2026-10-29T10:00:00Z', success: 3, failure: 1,
    success_rate: .75, covered_intervals: 2, intervals: Array.from({ length: 96 }, (_, i) => i === 0 ? 'healthy' : i === 1 ? 'unhealthy' : 'unknown'),
  } } }));
  assert.match(html, /75.0%/); assert.match(html, /4 次检测/); assert.match(html, /2\/96 时段有样本/);
  assert.match(html, /3 次成功，1 次失败/);
});

test('home cockpit links all three domains and keeps missing or stale evidence out of live totals', () => {
  const now = Date.parse('2026-10-30T08:00:00Z');
  const props = { services: [{ ...base, kind: 'mihomo' }], servicesSynced: new Date(now).toISOString(), admin: true, refreshKey: 0, onNavigate: noop, now };
  const empty = renderToStaticMarkup(React.createElement(HomeCockpitView, { ...props, snapshot: { models: {}, networks: {} } }));
  for (const label of ['服务监控', '大模型', '网络代理', '需要关注', '最近服务观测', '服务日志', '大模型日志', '网络代理日志']) assert.ok(empty.includes(label));
  assert.match(empty, /等待数据/);
  assert.match(empty, /0 \/ 1 个实例有效/);
  const stale = renderToStaticMarkup(React.createElement(HomeCockpitView, { ...props, servicesError: 'offline', snapshot: { models: {}, networks: { a: { data: { state: 'available', generation: 1, collected_at: new Date(now).toISOString(), overview: { subscription_count: 999, node_count: 999 }, coverage: { source_states: {} } }, receivedAt: now, error: 'offline' } } } }));
  assert.doesNotMatch(stale, /999/);
  assert.match(stale, /同步待确认/);
  assert.doesNotMatch(stale, /class="signal-healthy"/);
});

test('log cockpit exposes source windows, preserves network privilege and describes model aggregates', () => {
  const props = { page: 'logs', services: [], servicesSynced: '', admin: false, refreshKey: 0, onNavigate: noop, snapshot: { models: {}, networks: {} } };
  const html = renderToStaticMarkup(React.createElement(LogsOverviewView, props));
  assert.match(html, /不是逐请求明细或完整 24 小时统计/);
  assert.match(html, /代理日志采集状态和保存配置仅管理员可查看/);
  const restricted = renderToStaticMarkup(React.createElement(LogsWorkspace, { ...props, page: 'logs-network' }));
  assert.match(restricted, /仅管理员可查看/);
  assert.doesNotMatch(restricted, /原控制台|日志等级/);
  const nav = renderToStaticMarkup(React.createElement(Navigation, { active: 'logs-models', admin: true, busy: false, incidentCount: 0, onNavigate: noop, onLogout: noop }));
  assert.match(nav, /aria-controls="nav-logs"/);
  assert.match(nav, /id="nav-logs"[^>]*inert=""/);
  assert.match(nav, /aria-current="page"[^>]*><span>大模型日志/);
  assert.match(nav, />首页<\/button>/);
  const now = Date.parse('2026-10-30T08:00:00Z');
  const partial = renderToStaticMarkup(React.createElement(LogsOverviewView, { ...props, admin: true, now,
    services: [{ ...base, id: 'ok', kind: 'mihomo' }, { ...base, id: 'failed', kind: 'mihomo' }],
    snapshot: { models: {}, networks: {
      ok: { receivedAt: now, data: { settings: { persist_logs: true, log_retention_hours: 24, log_limit_mib: 20 }, coverage: { source_states: {} } } },
      failed: { error: 'offline' },
    } },
  }));
  assert.match(partial, /已确认 1 \/ 2 个实例的保存配置/);
  assert.match(partial, /读取失败/);
});

test('service logs filter projected messages and render literal error text with historical version', () => {
  const items = [{ id: 'one', status: 'healthy', class: 'process', message: 'ok', generation: 2 }, { id: 'two', status: 'unhealthy', class: 'timeout', message: '<script>literal</script>', generation: 1 }];
  assert.deepEqual(filterServiceLogs(items, '检查超时', 'unhealthy').map(item => item.id), ['two']);
  assert.equal(filterServiceLogs(items, 'ok', 'unhealthy').length, 0);
  const html = renderToStaticMarkup(React.createElement(ServiceLogTable, { items }));
  assert.match(html, /&lt;script&gt;literal/);
  assert.doesNotMatch(html, /<script>/);
  assert.match(html, /配置版本/);
});

test('cockpit reads use bounded cache GETs and never request network logs for the home summary', async () => {
  const services = Array.from({ length: 11 }, (_, i) => ({ ...base, id: String(i), kind: 'mihomo' }));
  let running = 0; let max = 0; const paths = [];
  const read = async path => {
    paths.push(path);
    if (path.startsWith('/models')) return { data: { sources: [] } };
    running++; max = Math.max(max, running);
    await new Promise(resolve => setTimeout(resolve, 1)); running--;
    return { data: { generation: 1 } };
  };
  const result = await readCockpit(services, false, read);
  assert.equal(Object.keys(result.networks).length, 11);
  assert.equal(max, 4);
  assert.ok(paths.every(path => path.startsWith('/models?section=overview') || path.includes('section=overview')));
  assert.ok(paths.every(path => !path.includes('checks') && !path.includes('section=logs')));
});

test('cockpit partial failures retain only their own cache and reject old configuration generations', async () => {
  const services = [{ ...base, kind: 'mihomo' }];
  const prior = { models: { data: { sources: [] }, receivedAt: 10 }, networks: { a: { data: { generation: 1, state: 'available' }, receivedAt: 10 } } };
  const read = async path => { if (path.startsWith('/models')) return { data: { sources: [] } }; throw new Error('fixture offline'); };
  const value = await readCockpit(services, false, read, prior);
  assert.equal(value.networks.a.receivedAt, 10);
  assert.match(value.networks.a.error, /offline/);
  assert.equal(value.models.error, undefined);
  assert.equal(networkIsCurrent(value.networks.a), false);
  const changed = await readCockpit(services, false, async path => ({ data: path.startsWith('/models') ? { sources: [{ id: 'a', generation: 9 }] } : { generation: 9 } }));
  assert.equal(changed.models.data, undefined);
  assert.equal(changed.networks.a.data, undefined);
  await assert.rejects(readCockpit(services, true, async () => { throw new CockpitAPIError('expired', 401); }), reason => reason.status === 401);
});

test('every navigation child has a stable deep link and round trips', () => {
 const { modelPages, networkPages, pagePath, pageFromPath } = load('NavigationRoutes.tsx');
 const keys = ['home', 'services', 'alerts', 'devices', 'settings', 'diagnostic', 'logs', 'logs-services', 'logs-models', 'logs-network', ...modelPages.map(x => x.key), ...networkPages.map(x => x.key)];
 const paths = keys.map(pagePath);
 assert.equal(new Set(paths).size, keys.length);
 for (const key of keys) { assert.equal(pageFromPath(pagePath(key)), key); assert.equal(pageFromPath(pagePath(key) + (key === 'home' ? '' : '/')), key); }
 assert.equal(pagePath('logs-models'), '/logs/models');
 assert.equal(pageFromPath('/network/nodes'), 'network-nodes');
});
