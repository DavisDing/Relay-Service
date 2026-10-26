import React, { useCallback, useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';
import { Button, Icon, Modal, ThemeControl, Toast } from './ui';
import type { IconName } from './ui';
import { api, APIError, APIRequestSupersededError, invalidateAPIRequests } from './api';
import { RetentionPanel } from './RetentionPanel';

type Principal = { id: string; role: string };
type Check = {
  id: string;
  cursor: string;
  status: string;
  class?: string;
  message?: string;
  completed_at?: string;
  started_at?: string;
  duration_ms?: number;
};
type Service = {
  id: string;
  name: string;
  kind: string;
  base_url: string;
  ui_url?: string;
  group?: string;
  enabled: boolean;
  interval_seconds: number;
  timeout_seconds: number;
  failure_threshold: number;
  recovery_threshold: number;
  allow_private: boolean;
  generation: number;
  credential_configured?: boolean;
  status?: string;
  freshness?: string;
  evidence_level?: string;
  inference_state?: string;
  update_state?: string;
  current?: Check;
  last_success_at?: string;
  last_check_at?: string;
  open_incident: boolean;
  open_restriction: boolean;
};
type Alert = {
  cursor: number;
  id: string;
  service_id: string;
  kind: string;
  severity: string;
  message: string;
  started_at: string;
  resolved_at?: string;
  count: number;
  silenced: boolean;
  silence_until?: string;
  evidence_state?: string;
};
type Device = {
  id: string;
  name: string;
  created_at: string;
  expires_at: string;
  revoked_at?: string;
  issued_by?: string;
};
type AlertPage = {
  items: Alert[];
  ack_cursor: number;
  next_cursor: string;
  has_more: boolean;
};
type Form = {
  id: string;
  name: string;
  kind: string;
  base_url: string;
  ui_url: string;
  group: string;
  enabled: boolean;
  interval_seconds: number;
  timeout_seconds: number;
  failure_threshold: number;
  recovery_threshold: number;
  allow_private: boolean;
  generation: number;
  token: string;
  clear_token: boolean;
};
type Confirmation = {
  title: string;
  description: string;
  label: string;
  run: (ensureCurrent: () => void) => Promise<void>;
};

const defaults: Form = {
  id: '',
  name: '',
  kind: 'litellm',
  base_url: '',
  ui_url: '',
  group: '',
  enabled: true,
  interval_seconds: 60,
  timeout_seconds: 5,
  failure_threshold: 3,
  recovery_threshold: 2,
  allow_private: false,
  generation: 0,
  token: '',
  clear_token: false,
};
const kinds = ['litellm', 'hermes', 'workbuddy2api', 'openwebui', 'ollama', 'metacubexd', 'http', 'tcp'];
const kindLabels: Record<string, string> = {
  litellm: 'LiteLLM',
  hermes: 'Hermes',
  workbuddy2api: 'workbuddy2api',
  openwebui: 'Open WebUI',
  ollama: 'Ollama',
  metacubexd: 'MetaCubeXD',
  http: 'HTTP 连通',
  tcp: 'TCP 连通',
};
const states: Record<string, string> = {
  healthy: '检查成功',
  unhealthy: '检查失败',
  unauthorized: '认证失效',
  unsupported: '接口不支持',
  unknown: '未观测',
  queued: '检查排队',
  running: '检查中',
  interrupted: '已中断',
  paused: '已暂停',
};
const pageTitles: Record<string, string> = {
  services: '服务总览',
  alerts: '故障与告警',
  devices: '访问设备',
  diagnostic: '诊断摘要',
};
const time = (value?: string) => value && !value.startsWith('0001')
  ? new Date(value).toLocaleString('zh-CN')
  : '尚无记录';

function toInput(service: Service): Form {
  return {
    id: service.id,
    name: service.name,
    kind: service.kind,
    base_url: service.base_url,
    ui_url: service.ui_url || '',
    group: service.group || '',
    enabled: service.enabled,
    interval_seconds: service.interval_seconds,
    timeout_seconds: service.timeout_seconds,
    failure_threshold: service.failure_threshold,
    recovery_threshold: service.recovery_threshold,
    allow_private: service.allow_private,
    generation: service.generation,
    token: '',
    clear_token: false,
  };
}

function operationID(): string {
  const random = globalThis.crypto;
  if (typeof random?.randomUUID === 'function') return random.randomUUID();
  if (typeof random?.getRandomValues === 'function') {
    return Array.from(random.getRandomValues(new Uint8Array(16)), value =>
      value.toString(16).padStart(2, '0')).join('');
  }
  throw new Error('当前浏览器无法生成安全操作标识，请使用支持安全随机数的浏览器后重试。');
}


function App() {
  const [me, setMe] = useState<Principal | null>(null);
  const [checking, setChecking] = useState(true);
  const [authMode, setAuthMode] = useState<'login' | 'bootstrap'>('login');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState('services');
  const [alertPage, setAlertPage] = useState('');
  const [loadedAlertPage, setLoadedAlertPage] = useState('');
  const [alertPages, setAlertPages] = useState<string[]>([]);
  const [alertNext, setAlertNext] = useState('');
  const [alertsMore, setAlertsMore] = useState(false);
  const [ackCursor, setAckCursor] = useState(0);
  const [services, setServices] = useState<Service[]>([]);
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [servicesSynced, setServicesSynced] = useState('');
  const [alertsSynced, setAlertsSynced] = useState('');
  const [refreshErrors, setRefreshErrors] = useState<{ services?: string; alerts?: string }>({});
  const [form, setForm] = useState<Form | null>(null);
  const [history, setHistory] = useState<{
    service: Service; items: Check[]; hasMore: boolean;
  } | null>(null);
  const [pairing, setPairing] = useState<{ code: string; expires_at: string } | null>(null);
  const [diagnostic, setDiagnostic] = useState<unknown>(null);
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null);
  const sessionVersion = useRef(0);
  const refreshVersion = useRef(0);
  const busyRef = useRef(false);
  const actionVersion = useRef(0);

  const clearSession = useCallback(() => {
    invalidateAPIRequests();
    sessionVersion.current++;
    actionVersion.current++;
    busyRef.current = false;
    setBusy(false);
    refreshVersion.current++;
    setMe(null);
    setServices([]);
    setAlerts([]);
    setDevices([]);
    setDiagnostic(null);
    setPairing(null);
    setForm(null);
    setHistory(null);
    setConfirmation(null);
    setServicesSynced('');
    setAlertsSynced('');
    setRefreshErrors({});
    setNotice('');
    setAlertPage('');
    setLoadedAlertPage('');
    setAlertPages([]);
    setAlertNext('');
    setAlertsMore(false);
    setAckCursor(0);
    setTab('services');
  }, []);

  const refresh = useCallback(async () => {
    const session = sessionVersion.current;
    const request = ++refreshVersion.current;
    const current = () => session === sessionVersion.current && request === refreshVersion.current;
    function failed(resource: 'services' | 'alerts', reason: unknown) {
      if (!current()) return;
      if (reason instanceof APIError && reason.status === 401) {
        clearSession();
        setError('登录已失效，请重新登录。');
        return;
      }
      setRefreshErrors(previous => ({ ...previous, [resource]: (reason as Error).message }));
    }
    // Each request commits independently; a failed or delayed sibling retains only its own cache.
    await Promise.allSettled([
      api<Service[]>('/services').then(response => {
        if (!current()) return;
        setServices(response.data || []);
        setServicesSynced(response.generated_at);
        setRefreshErrors(previous => ({ ...previous, services: undefined }));
      }).catch(reason => failed('services', reason)),
      api<AlertPage>('/alerts?limit=100&cursor=' + encodeURIComponent(alertPage)).then(response => {
        if (!current()) return;
        setAlerts(response.data.items || []);
        setAckCursor(response.data.ack_cursor || 0);
        setAlertNext(response.data.next_cursor || '');
        setAlertsMore(response.data.has_more);
        setLoadedAlertPage(alertPage);
        setAlertsSynced(response.generated_at);
        setRefreshErrors(previous => ({ ...previous, alerts: undefined }));
      }).catch(reason => failed('alerts', reason)),
    ]);
  }, [alertPage, clearSession]);

  useEffect(() => {
    let active = true;
    Promise.allSettled([api<Principal>('/auth/me'), api<{ initialized: boolean }>('/info')])
      .then(([identity, info]) => {
        if (!active) return;
        if (identity.status === 'fulfilled') setMe(identity.value.data as Principal);
        if (info.status === 'fulfilled' && !(info.value.data as { initialized: boolean }).initialized) {
          setAuthMode('bootstrap');
        }
        if (info.status === 'rejected') setError('无法读取 Relay 状态，请检查连接后刷新页面。');
      }).finally(() => { if (active) setChecking(false); });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    if (!me) return;
    void refresh();
    const timer = setInterval(() => void refresh(), 60000);
    return () => {
      clearInterval(timer);
      refreshVersion.current++;
    };
  }, [me, refresh]);

  useEffect(() => {
    if (tab !== 'devices' || !me) return;
    const session = sessionVersion.current;
    let active = true;
    api<Device[]>('/devices').then(response => {
      if (active && session === sessionVersion.current) setDevices(response.data || []);
    }).catch(reason => {
      if (!active || session !== sessionVersion.current) return;
      if (reason instanceof APIError && reason.status === 401) clearSession();
      setError((reason as Error).message);
    });
    return () => { active = false; };
  }, [tab, me, clearSession]);

  async function action(fn: (ensureCurrent: () => void) => Promise<void>, refreshAfter = true): Promise<boolean> {
    if (busyRef.current) return false;
    const session = sessionVersion.current;
    const operation = ++actionVersion.current;
    const current = () => session === sessionVersion.current && operation === actionVersion.current;
    function ensureCurrent() {
      if (!current()) throw new APIRequestSupersededError();
    }
    busyRef.current = true;
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await fn(ensureCurrent);
      if (!current()) return false;
      if (me && refreshAfter) await refresh();
      return current();
    } catch (reason) {
      if (!current() || reason instanceof APIRequestSupersededError) return false;
      if (reason instanceof APIError && reason.status === 401) clearSession();
      setError((reason as Error).message);
      return false;
    } finally {
      // An earlier action cannot unlock a new login's in-flight action.
      if (operation === actionVersion.current) {
        busyRef.current = false;
        setBusy(false);
      }
    }
  }

  async function auth(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    await action(async ensureCurrent => {
      const credentials = { username: String(data.get('username')), password: String(data.get('password')) };
      if (authMode === 'bootstrap') {
        await api('/auth/bootstrap', 'POST', { ...credentials, token: String(data.get('token')) });
        ensureCurrent();
        setAuthMode('login');
        setNotice('管理员已初始化，请登录。');
        return;
      }
      await api('/auth/login', 'POST', credentials);
      ensureCurrent();
      const identity = await api<Principal>('/auth/me');
      ensureCurrent();
      invalidateAPIRequests();
      sessionVersion.current++;
      setMe(identity.data);
    });
  }

  async function save(event: React.FormEvent) {
    event.preventDefault();
    if (!form) return;
    const succeeded = await action(async ensureCurrent => {
      await api('/services' + (form.id ? '/' + form.id : ''), form.id ? 'PUT' : 'POST', form);
      ensureCurrent();
      setNotice('服务接入已保存。');
    });
    if (succeeded) setForm(null);
  }

  async function toggle(service: Service) {
    await action(async ensureCurrent => {
      await api('/services/' + service.id, 'PUT', { ...toInput(service), enabled: !service.enabled });
      ensureCurrent();
      setNotice(service.enabled ? '已暂停新检查，历史保留。' : '已恢复检查计划。');
    });
  }

  async function showHistory(service: Service) {
    await action(async ensureCurrent => {
      const response = await api<{ items: Check[] }>('/services/' + service.id + '/checks?limit=100');
      ensureCurrent();
      setHistory({ service, items: response.data.items || [], hasMore: response.data.items?.length === 100 });
    });
  }

  const dismissNotice = useCallback(() => setNotice(''), []);
  const unreadCursors = alerts.filter(alert => alert.cursor > ackCursor).map(alert => alert.cursor);
  const skippedUnreadPage = Number(loadedAlertPage || 0) > ackCursor;
  const canAck = unreadCursors.length > 0 && !skippedUnreadPage && loadedAlertPage === alertPage;
  const incidentCount = services.filter(service => service.open_incident || service.open_restriction).length;
  const navigation: { key: string; label: string; icon: IconName }[] = [
    { key: 'services', label: '服务总览', icon: 'grid' },
    { key: 'alerts', label: '故障与告警', icon: 'bell' },
    ...(me?.role === 'admin' ? [
      { key: 'devices', label: '访问设备', icon: 'devices' as IconName },
      { key: 'diagnostic', label: '诊断摘要', icon: 'pulse' as IconName },
    ] : []),
  ];

  if (checking) return (
    <main className="login" aria-busy="true">
      <div className="login-mark"><Icon name="pulse" /></div>
      <h1>Relay</h1>
      <p>正在连接运维控制台…</p>
    </main>
  );

  if (!me) return (
    <main className="login">
      <div className="login-head">
        <div className="login-mark"><Icon name="pulse" /></div>
        <ThemeControl />
      </div>
      <span className="eyebrow">NAS OPERATIONS</span>
      <h1>Relay</h1>
      <p className="sub">服务状态，一处掌握</p>
      <form onSubmit={auth}>
        <h2>{authMode === 'bootstrap' ? '初始化管理员' : '管理员登录'}</h2>
        {authMode === 'bootstrap' && (
          <label>部署初始化令牌<input name="token" type="password" autoComplete="off" required /></label>
        )}
        <label>用户名<input name="username" autoComplete="username" minLength={3} required /></label>
        <label>
          密码
          <input name="password" type="password" minLength={12} required
            autoComplete={authMode === 'bootstrap' ? 'new-password' : 'current-password'} />
        </label>
        {error && <p role="alert" className="error">{error}</p>}
        <Button type="submit" loading={busy}>{authMode === 'bootstrap' ? '创建管理员' : '登录'}</Button>
        <Button className="text" type="button" disabled={busy} onClick={() => {
          setAuthMode(authMode === 'login' ? 'bootstrap' : 'login');
          setError('');
        }}>
          {authMode === 'login' ? '首次使用？初始化管理员' : '返回登录'}
        </Button>
      </form>
      <small>凭据只在服务端使用。公网访问请使用可信 HTTPS 入口。</small>
      <Toast message={notice} onDismiss={dismissNotice} />
    </main>
  );

  return (
    <div className="shell">
      <aside>
        <div className="brand">
          <span className="brand-mark"><Icon name="pulse" /></span>
          <strong>Relay</strong><span>NAS 运维控制台</span>
        </div>
        <nav aria-label="主要导航">
          {navigation.map(item => (
            <Button className={tab === item.key ? 'active' : ''} key={item.key}
              aria-current={tab === item.key ? 'page' : undefined} disabled={busy}
              onClick={() => { setTab(item.key); setError(''); }}>
              <span><Icon name={item.icon} />{item.label}</span>
              {item.key === 'alerts' && incidentCount > 0 && <b>{incidentCount}</b>}
            </Button>
          ))}
        </nav>
        <div className="sidebar-foot">
          <ThemeControl />
          <span>{me.role === 'admin' ? '管理员' : '只读设备'}</span>
          <Button className="text" disabled={busy} onClick={() => action(async ensureCurrent => {
            if (me.role === 'admin') await api('/auth/logout', 'POST', {});
            ensureCurrent();
            clearSession();
          }, false)}><Icon name="logout" />退出登录</Button>
        </div>
      </aside>
      <main>
        <header>
          <div className="page-title">
            <span className="eyebrow">OPERATIONS</span>
            <h1>{pageTitles[tab]}</h1>
            <p className="sub">最近同步 {time(tab === 'alerts' ? alertsSynced : servicesSynced)} · 每 60 秒刷新</p>
          </div>
          <Button className="secondary" disabled={busy} onClick={() => refresh()} aria-label="刷新视图">
            <Icon name="refresh" />刷新视图
          </Button>
        </header>
        {error && <div className="banner error" role="alert"><Icon name="bell" />{error}</div>}
        {Object.entries(refreshErrors).filter(([, message]) => message).map(([resource, message]) => (
          <div className="banner error" role="alert" key={resource}>
            <Icon name="bell" />{resource === 'services' ? '服务列表' : '告警列表'}刷新失败：{message} · 已保留此列表最近数据
          </div>
        ))}

        {tab === 'services' && (
          <>
            <div className="section-head">
              <p>基础检查只读取服务状态，不调用模型或工具。</p>
              {me.role === 'admin' && (
                <Button disabled={busy} onClick={() => { setError(''); setForm({ ...defaults }); }}>
                  <Icon name="plus" />添加服务
                </Button>
              )}
            </div>
            {services.length === 0 ? (
              <div className="empty">
                <h2>尚未配置服务</h2><p>添加 LiteLLM 等服务后，检查记录和故障事件会保存在 Relay。</p>
              </div>
            ) : (
              <div className="grid">
                {services.map(service => {
                  const stale = service.freshness === 'stale' || service.freshness === 'missing' || !service.last_check_at;
                  const status = service.enabled ? service.status || service.current?.status || 'unknown' : 'paused';
                  return (
                    <article className="card" key={service.id}>
                      <div className="card-top">
                        <span className="type">{kindLabels[service.kind]}</span>
                        <span className={'status ' + status}>{states[status] || status}</span>
                      </div>
                      <h2>{service.name}</h2>
                      <p className="muted">{service.group || '未分组'}{service.base_url ? ' · ' + service.base_url : ''}</p>
                      <dl>
                        <div><dt>最近检查</dt><dd>{time(service.last_check_at)}</dd></div>
                        <div><dt>最近成功</dt><dd>{time(service.last_success_at)}</dd></div>
                        <div><dt>检查耗时</dt><dd>{service.current?.duration_ms !== undefined ? service.current.duration_ms + ' ms' : '未知'}</dd></div>
                      </dl>
                      {stale && service.enabled && (
                        <p className="warning">{service.last_check_at ? '采集已过期' : '等待首次检查'}，暂不判断当前健康</p>
                      )}
                      {service.current?.message && <p className="check-message">{service.current.message}</p>}
                      <p className="capability">推理能力未测试 · 版本更新证据未接入</p>
                      <div className="buttons">
                        <Button className="secondary" disabled={busy} onClick={() => showHistory(service)}>历史</Button>
                        {service.ui_url && (
                          <a className="button secondary" href={service.ui_url} target="_blank" rel="noreferrer noopener">
                            打开 WebUI ↗
                          </a>
                        )}
                        {me.role === 'admin' && (
                          <>
                            <Button className="secondary" disabled={busy || !service.enabled} onClick={() => action(async ensureCurrent => {
                              await api('/services/' + service.id + '/checks', 'POST', {
                                generation: service.generation, operation_id: operationID(),
                              });
                              ensureCurrent();
                              setNotice('无推理检查已排队。');
                            })}>检查</Button>
                            <Button className="text" disabled={busy} onClick={() => { setError(''); setForm(toInput(service)); }}>编辑</Button>
                            <Button className="text" disabled={busy} onClick={() => toggle(service)}>{service.enabled ? '暂停' : '恢复'}</Button>
                          </>
                        )}
                      </div>
                    </article>
                  );
                })}
              </div>
            )}
          </>
        )}

        {tab === 'alerts' && (
          <>
            <div className="section-head">
              <p>按时间顺序分页保留故障与恢复事件；徽标为当前故障或能力受限的服务数。</p>
              {alerts.length > 0 && (
                <Button className="secondary" disabled={busy || !canAck}
                  title={skippedUnreadPage ? '请先返回前页，依次确认未读事件' : '仅确认本页已展示的连续未读事件'}
                  onClick={() => action(async ensureCurrent => {
                    await api('/alerts/ack', 'POST', { cursor: unreadCursors.at(-1), cursors: unreadCursors });
                    ensureCurrent();
                    setNotice('本页事件已标记已读，历史记录仍保留。');
                  })}>本页标记已读</Button>
              )}
            </div>
            {skippedUnreadPage && <p className="warning">前页仍有未读事件，请先返回前页确认，避免跳过告警。</p>}
            {alerts.length === 0 ? (
              <div className="empty"><h2>暂无告警记录</h2><p>没有记录不代表尚未检查的服务健康。</p></div>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead><tr><th>服务 / 事件</th><th>说明</th><th>时间</th><th>状态</th><th></th></tr></thead>
                  <tbody>
                    {alerts.map(alert => (
                      <tr key={alert.id}>
                        <td>{services.find(service => service.id === alert.service_id)?.name || alert.service_id}<small>{alert.kind}</small></td>
                        <td>{alert.message}
                          {alert.evidence_state === 'expired' && <small>检查明细已过保留期</small>}
                          {alert.evidence_state === 'unavailable' && <small>检查明细不可用，已保留事件摘要</small>}
                        </td><td>{time(alert.started_at)}</td>
                        <td>{alert.resolved_at ? '恢复事件' : '事件已记录'}
                          <small>{alert.cursor > ackCursor ? '未读' : '已读'}{alert.silenced ? ' · 静默至 ' + time(alert.silence_until) : ''}</small>
                        </td>
                        <td>{me.role === 'admin' && (
                          <Button className="text" disabled={busy} onClick={() => action(async ensureCurrent => {
                            await api('/services/' + alert.service_id + '/silence', 'POST', {
                              until: new Date(Date.now() + 3600000).toISOString(), reason: '管理员临时维护',
                            });
                            ensureCurrent();
                            setNotice('此服务已静默 1 小时，继续检查并保存事件。');
                          })}>静默 1 小时</Button>
                        )}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <div className="buttons pagination">
              <Button className="secondary" disabled={busy || alertPages.length === 0} onClick={() => {
                const pages = [...alertPages];
                setAlertPage(pages.pop() || '');
                setAlertPages(pages);
              }}>上一页</Button>
              <Button className="secondary" disabled={busy || !alertsMore || loadedAlertPage !== alertPage} onClick={() => {
                setAlertPages([...alertPages, alertPage]);
                setAlertPage(alertNext);
              }}>下一页</Button>
            </div>
          </>
        )}

        {tab === 'devices' && (
          <>
            <div className="section-head">
              <p>每个设备独立配对，可撤销访问。</p>
              <Button disabled={busy} onClick={() => action(async ensureCurrent => {
                const response = await api<{ code: string; expires_at: string }>('/devices/pairings', 'POST', {});
                ensureCurrent();
                setPairing(response.data);
              })}>创建配对码</Button>
            </div>
            {pairing && (
              <div className="pairing"><span>一次性配对码</span><strong>{pairing.code}</strong>
                <p>有效至 {time(pairing.expires_at)}。只向可信设备提供，不会显示上游调用凭据。</p>
              </div>
            )}
            {devices.length === 0 ? <div className="empty"><h2>尚无配对设备</h2></div> : (
              <div className="table-wrap">
                <table>
                  <thead><tr><th>设备</th><th>配对时间 / 有效至</th><th>状态</th><th></th></tr></thead>
                  <tbody>{devices.map(device => (
                    <tr key={device.id}>
                      <td>{device.name}<small>授权：{device.issued_by === 'legacy_unknown' || !device.issued_by ? '未知（旧设备）' : device.issued_by}</small></td>
                      <td>{time(device.created_at)}<small>有效至 {time(device.expires_at)}</small></td>
                      <td>{device.revoked_at ? '已撤销' : Date.parse(device.expires_at) <= Date.now() ? '已过期' : '可访问'}</td>
                      <td>
                        <Button className="danger text" disabled={!!device.revoked_at || busy} onClick={() => {
                          setError('');
                          setConfirmation({
                            title: '撤销设备访问',
                            description: '撤销“' + device.name + '”后将拒绝新的读取请求。此设备需要重新配对才能访问。',
                            label: '撤销设备',
                            run: async ensureCurrent => {
                              await api('/devices/' + device.id, 'DELETE');
                              ensureCurrent();
                              const response = await api<Device[]>('/devices');
                              ensureCurrent();
                              setDevices(response.data);
                              setNotice('设备访问已撤销。');
                            },
                          });
                        }}>撤销</Button>
                      </td>
                    </tr>
                  ))}</tbody>
                </table>
              </div>
            )}
          </>
        )}

        {tab === 'diagnostic' && (
          <section className="panel">
            <h2>脱敏诊断预览</h2><p>仅包含 Relay 状态摘要；不会自动上传。</p>
            <Button className="secondary" disabled={busy} onClick={() => action(async ensureCurrent => {
              const response = await api('/diagnostic');
              ensureCurrent();
              setDiagnostic(response.data);
            })}>生成摘要</Button>
            {diagnostic !== null && (
              <>
                <pre>{JSON.stringify(diagnostic, null, 2)}</pre>
                <Button className="secondary" onClick={async () => {
                  const session = sessionVersion.current;
                  if (!navigator.clipboard) { setError('无法访问剪贴板，请手动复制。'); return; }
                  try {
                    await navigator.clipboard.writeText(JSON.stringify(diagnostic, null, 2));
                    if (session === sessionVersion.current) setNotice('已复制摘要。');
                  } catch {
                    if (session === sessionVersion.current) setError('无法访问剪贴板，请手动复制。');
                  }
                }}>复制摘要</Button>
              </>
            )}
            <RetentionPanel onNotice={setNotice} onUnauthorized={clearSession} />
          </section>
        )}
        <Toast message={notice} onDismiss={dismissNotice} />
        <footer>Relay 基础监控 · 数据来源与检查层级独立显示 · 容器操作请在 UGOS 中进行</footer>
      </main>

      {form && (
        <Modal titleId="form-title" busy={busy} onClose={() => { if (!busy) setForm(null); }}>
          <section className="dialog">
            <div className="section-head">
              <h2 id="form-title">{form.id ? '编辑服务' : '添加服务'}</h2>
              <Button className="text" disabled={busy} onClick={() => setForm(null)}>关闭</Button>
            </div>
            <form onSubmit={save}>
              <fieldset disabled={busy}>
                <div className="form-grid">
                  <label>显示名称<input value={form.name} required autoFocus data-autofocus onChange={event => setForm({ ...form, name: event.target.value })} /></label>
                  <label>服务类型
                    <select value={form.kind} onChange={event => setForm({ ...form, kind: event.target.value })}>
                      {kinds.map(kind => <option key={kind} value={kind}>{kindLabels[kind]}</option>)}
                    </select>
                  </label>
                  <label>服务地址<input type="url" placeholder="http://litellm:4000" value={form.base_url} required
                    onChange={event => setForm({ ...form, base_url: event.target.value })} /></label>
                  <label>原 WebUI 地址（可选）<input type="url" value={form.ui_url}
                    onChange={event => setForm({ ...form, ui_url: event.target.value })} /></label>
                  <label>展示分组<input value={form.group} onChange={event => setForm({ ...form, group: event.target.value })} /></label>
                  <label>检查间隔（秒）<input type="number" min={10} max={86400} value={form.interval_seconds}
                    onChange={event => setForm({ ...form, interval_seconds: Number(event.target.value) })} /></label>
                  <label>超时（秒）<input type="number" min={1} max={30} value={form.timeout_seconds}
                    onChange={event => setForm({ ...form, timeout_seconds: Number(event.target.value) })} /></label>
                  <label>连续失败告警次数<input type="number" min={1} max={20} value={form.failure_threshold}
                    onChange={event => setForm({ ...form, failure_threshold: Number(event.target.value) })} /></label>
                  <label>连续成功恢复次数<input type="number" min={1} max={20} value={form.recovery_threshold}
                    onChange={event => setForm({ ...form, recovery_threshold: Number(event.target.value) })} /></label>
                  <label>只读检查凭据（输入后不回显）
                    <input type="password" autoComplete="off" value={form.token} disabled={form.clear_token}
                      placeholder={form.id ? '留空保留原凭据' : '按服务接口需要填写'}
                      onChange={event => setForm({ ...form, token: event.target.value })} />
                  </label>
                </div>
                {form.id && (
                  <label className="checkbox">
                    <input type="checkbox" checked={form.clear_token} onChange={event => setForm({
                      ...form, clear_token: event.target.checked, token: event.target.checked ? '' : form.token,
                    })} />删除已保存的检查凭据
                  </label>
                )}
                <label className="checkbox"><input type="checkbox" checked={form.allow_private}
                  onChange={event => setForm({ ...form, allow_private: event.target.checked })} />授权检查此指定私网目标</label>
                <label className="checkbox"><input type="checkbox" checked={form.enabled}
                  onChange={event => setForm({ ...form, enabled: event.target.checked })} />启用定时检查</label>
                <p className="muted">检查使用固定无推理端点。服务地址只包含协议、主机和端口。</p>
                {error && !confirmation && <p className="error" role="alert">{error}</p>}
                <div className="buttons">
                  <Button type="submit" loading={busy}>保存接入</Button>
                  {form.id && (
                    <Button className="danger secondary" type="button" onClick={() => {
                      const service = form;
                      setError('');
                      setConfirmation({
                        title: '移除服务接入',
                        description: '移除“' + service.name + '”并停止新检查？检查历史和告警仍会保留。',
                        label: '移除接入',
                        run: async ensureCurrent => {
                          await api('/services/' + service.id + '?generation=' + service.generation, 'DELETE');
                          ensureCurrent();
                          setForm(null);
                          setNotice('服务接入已移除，历史仍保留。');
                        },
                      });
                    }}><Icon name="trash" />移除接入</Button>
                  )}
                </div>
              </fieldset>
            </form>
          </section>
        </Modal>
      )}

      {history && (
        <Modal titleId="history-title" busy={busy} onClose={() => { if (!busy) setHistory(null); }}>
          <section className="dialog wide">
            <div className="section-head">
              <h2 id="history-title">{history.service.name} · 检查历史</h2>
              <Button className="text" disabled={busy} onClick={() => setHistory(null)}>关闭</Button>
            </div>
            {error && <p className="error" role="alert">{error}</p>}
            {history.items.length === 0 ? <p>尚无检查记录。</p> : (
              <>
                <div className="table-wrap">
                  <table>
                    <thead><tr><th>完成时间</th><th>结果</th><th>耗时</th><th>原因</th></tr></thead>
                    <tbody>{history.items.map(check => (
                      <tr key={check.id}>
                        <td>{time(check.completed_at)}</td><td><span className={'status ' + check.status}>{states[check.status] || check.status}</span></td>
                        <td>{check.duration_ms ?? '未知'} ms</td><td>{check.class || '—'}<small>{check.message}</small></td>
                      </tr>
                    ))}</tbody>
                  </table>
                </div>
                {history.hasMore && (
                  <Button className="secondary" disabled={busy} onClick={() => action(async ensureCurrent => {
                    const cursor = encodeURIComponent(history.items.at(-1)!.cursor);
                    const response = await api<{ items: Check[] }>('/services/' + history.service.id + '/checks?limit=100&cursor=' + cursor);
                    ensureCurrent();
                    setHistory({ ...history, items: [...history.items, ...response.data.items], hasMore: response.data.items.length === 100 });
                  })}>继续读取历史</Button>
                )}
              </>
            )}
          </section>
        </Modal>
      )}

      {confirmation && (
        <Modal titleId="confirmation-title" busy={busy} compact onClose={() => { if (!busy) setConfirmation(null); }}>
          <section className="dialog">
            <h2 id="confirmation-title">{confirmation.title}</h2>
            <p>{confirmation.description}</p>
            {error && <p className="error" role="alert">{error}</p>}
            <div className="buttons">
              <Button className="secondary" disabled={busy} autoFocus data-autofocus onClick={() => setConfirmation(null)}>取消</Button>
              <Button className="danger secondary" loading={busy} onClick={async () => {
                const succeeded = await action(confirmation.run);
                if (succeeded) setConfirmation(null);
              }}>{confirmation.label}</Button>
            </div>
          </section>
        </Modal>
      )}
    </div>
  );
}

createRoot(document.getElementById('root')!).render(<React.StrictMode><App /></React.StrictMode>);
