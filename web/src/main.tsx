import React, { useCallback, useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';
import './navigation.css';
import { Navigation } from './Navigation';
import { Button, Icon, Modal, ThemeControl, Toast } from './ui';
import { api, APIError, APIRequestSupersededError, invalidateAPIRequests } from './api';
import { RetentionPanel } from './RetentionPanel';
import type { Principal, Check, Service, Alert, Device, AlertPage, Form, Confirmation } from './types';
import { time } from './presentation';
import { AlertsPanel } from './AlertsPanel';
import { ServicesOverview } from './ServicesOverview';
import { ServiceEditor, HistoryDialog } from './ServiceDialogs';

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
const pageTitles: Record<string, string> = {
  services: '服务总览', alerts: '故障与告警', devices: '访问设备',
  diagnostic: '诊断摘要', settings: '系统设置',
};

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
      <Navigation active={tab} admin={me.role === 'admin'} busy={busy} incidentCount={incidentCount}
        onNavigate={page => {
          setTab(page);
          setError('');
          window.scrollTo(0, 0);
          requestAnimationFrame(() => document.getElementById('workspace-title')?.focus({ preventScroll: true }));
        }}
        onLogout={() => action(async ensureCurrent => {
          if (me.role === 'admin') await api('/auth/logout', 'POST', {});
          ensureCurrent();
          clearSession();
        }, false)} />
      <main id="main-content" tabIndex={-1} className={tab === 'services' ? 'overview-page' : undefined}>
        <div key={tab} className="workspace-content">
        <header>
          <div className="page-title">
            <span className="eyebrow">OPERATIONS</span>
            <h1 id="workspace-title" tabIndex={-1}>{pageTitles[tab]}</h1>
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

        {tab === 'services' && <ServicesOverview services={services} alerts={alerts}
          servicesSynced={servicesSynced} alertsSynced={alertsSynced}
          alertsCurrent={loadedAlertPage === '' && !refreshErrors.alerts} alertsComplete={!alertsMore}
          admin={me.role === 'admin'} busy={busy}
          onAdd={() => { setError(''); setForm({ ...defaults }); }}
          onAlerts={() => { setAlertPage(''); setAlertPages([]); setTab('alerts'); }}
          onHistory={showHistory} onEdit={service => { setError(''); setForm(toInput(service)); }}
          onToggle={toggle} onCheck={service => action(async ensureCurrent => {
            await api('/services/' + service.id + '/checks', 'POST', {
              generation: service.generation, operation_id: operationID(),
            });
            ensureCurrent();
            setNotice('无推理检查已排队。');
          })} />}

        {tab === 'alerts' && <AlertsPanel alerts={alerts} services={services} ackCursor={ackCursor}
          busy={busy} admin={me.role === 'admin'} skippedUnreadPage={skippedUnreadPage} canAck={canAck}
          hasPrevious={alertPages.length > 0} canNext={alertsMore && loadedAlertPage === alertPage}
          onAck={() => action(async ensureCurrent => {
            await api('/alerts/ack', 'POST', { cursor: unreadCursors.at(-1), cursors: unreadCursors });
            ensureCurrent();
            setNotice('本页事件已标记已读，历史记录仍保留。');
          })}
          onSilence={serviceID => action(async ensureCurrent => {
            await api('/services/' + serviceID + '/silence', 'POST', {
              until: new Date(Date.now() + 3600000).toISOString(), reason: '管理员临时维护',
            });
            ensureCurrent();
            setNotice('此服务已静默 1 小时，继续检查并保存事件。');
          })}
          onPrevious={() => {
            const pages = [...alertPages];
            setAlertPage(pages.pop() || '');
            setAlertPages(pages);
          }}
          onNext={() => { setAlertPages([...alertPages, alertPage]); setAlertPage(alertNext); }} />}

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
          </section>
        )}
        {tab === 'settings' && me.role === 'admin' && (
          <section className="panel" aria-label="数据保留设置">
            <RetentionPanel onNotice={setNotice} onUnauthorized={clearSession} />
          </section>
        )}
        </div>
        <Toast message={notice} onDismiss={dismissNotice} />
        <footer>Relay 基础监控 · 数据来源与检查层级独立显示 · 容器操作请在 UGOS 中进行</footer>
      </main>

      {form && <ServiceEditor form={form} setForm={setForm} busy={busy} error={error} save={save} confirming={!!confirmation}
        onRemove={() => {
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
        }} />}
      {history && <HistoryDialog history={history} setHistory={setHistory} busy={busy} error={error} onMore={() => action(async ensureCurrent => {
        const cursor = encodeURIComponent(history.items.at(-1)!.cursor);
        const response = await api<{ items: Check[] }>('/services/' + history.service.id + '/checks?limit=100&cursor=' + cursor);
        ensureCurrent();
        setHistory({ ...history, items: [...history.items, ...response.data.items], hasMore: response.data.items.length === 100 });
      })} />}

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
