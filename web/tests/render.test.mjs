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
    return load(specifier.slice(2) + (specifier === './presentation' || specifier === './api' ? '.ts' : '.tsx'));
  }, module, module.exports);
  modules.set(name, module.exports);
  return module.exports;
}
const { ServicesOverview, serviceStatus, summarizeServices, needsAttention, filterServices } = load('ServicesOverview.tsx');
const { Icon, ThemeControl, Toast, Button } = load('ui.tsx');
const { AlertsPanel } = load('AlertsPanel.tsx');
const { RetentionPanel } = load('RetentionPanel.tsx');
const { Navigation } = load('Navigation.tsx');
const css = ['style.css', 'navigation.css'].map(file => readFileSync(new URL('../src/' + file, import.meta.url), 'utf8')).join('\n');
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
  for (const name of ['服务总览', '故障与告警', '访问设备', '系统设置', '诊断', '外观与账户', '退出登录']) assert.ok(html.includes(name));
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
