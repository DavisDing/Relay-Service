import { useEffect, useRef, useState } from 'react';
import { api, APIError, APIRequestSupersededError } from './api';
import { Button } from './ui';

type DatabaseStatus = { backend: 'sqlite' | 'postgresql' | 'unknown'; page_migration_available: boolean };
type Connection = { host: string; port: string; database: string; username: string; password: string; sslmode: string };
type Preflight = { ready: boolean; checks: { name: string; status: 'passed' | 'failed'; message: string }[] };
const initialConnection: Connection = {
  host: '', port: '5432', database: 'relay', username: 'relay_app', password: '', sslmode: 'verify-full',
};
const preparationSQL = String.raw`-- 在 psql 中由 PostgreSQL 管理员依次执行；不要放入 BEGIN 事务。
-- 仅用于首次创建；同名账号或数据库已存在时先核对，不要删除重建。
CREATE ROLE relay_app LOGIN;
\password relay_app
CREATE DATABASE relay OWNER relay_app;
\connect relay
GRANT USAGE, CREATE ON SCHEMA public TO relay_app;`;

export function DatabasePanel({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [status, setStatus] = useState<DatabaseStatus | null>(null);
  const [open, setOpen] = useState(false);
  const [connection, setConnection] = useState<Connection>(initialConnection);
  const [result, setResult] = useState<Preflight | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const alive = useRef(false);
  const checking = useRef(false);
  const unauthorized = useRef(onUnauthorized);
  unauthorized.current = onUnauthorized;

  function failed(reason: unknown) {
    if (!alive.current || reason instanceof APIRequestSupersededError) return;
    if (reason instanceof APIError && reason.status === 401) {
      unauthorized.current();
      return;
    }
    setError(reason instanceof Error ? reason.message : '数据库检查失败，请稍后重试。');
  }

  useEffect(() => {
    alive.current = true;
    let current = true;
    void api<DatabaseStatus>('/database').then(({ data }) => {
      if (current) setStatus(data);
    }).catch(reason => { if (current) failed(reason); });
    return () => { current = false; alive.current = false; };
  }, []);

  function change(field: keyof Connection, value: string) {
    setConnection(previous => ({ ...previous, [field]: value }));
    setResult(null);
    setError('');
  }

  async function check() {
    if (checking.current) return;
    checking.current = true;
    setBusy(true);
    setError('');
    setResult(null);
    const input = { ...connection, port: Number(connection.port) };
    // Passwords are sent once to Relay, never put in a URL, browser storage,
    // preparation SQL, success notice or connection-status response.
    setConnection(previous => ({ ...previous, password: '' }));
    try {
      const { data } = await api<Preflight>('/database/postgres/preflight', 'POST', input);
      if (alive.current) setResult(data);
    } catch (reason) {
      failed(reason);
    } finally {
      checking.current = false;
      if (alive.current) setBusy(false);
    }
  }

  const backend = status?.backend === 'sqlite' ? 'SQLite'
    : status?.backend === 'postgresql' ? 'PostgreSQL' : status ? '未知' : '读取中';
  return (
    <section aria-labelledby="database-title">
      <h2 id="database-title">数据库</h2>
      <p>当前使用：<strong>{backend}</strong></p>
      <p className="sub">SQLite 可独立使用，无需额外数据库服务。已有 PostgreSQL 时，可先检查接入条件。</p>
      {error && <p className="banner error" role="alert">{error}</p>}
      <Button type="button" className="secondary" disabled={busy} aria-expanded={open} aria-controls="postgres-connection"
        onClick={() => {
          setOpen(!open);
          setConnection(previous => ({ ...previous, password: '' }));
          setResult(null);
          setError('');
        }}>{open ? '收起连接检查' : '连接已有 PostgreSQL'}</Button>
      {open && <div id="postgres-connection">
        <p><strong>连接前准备：</strong>请在现有 PostgreSQL 中准备 Relay 专用数据库和普通账号，并授予 public schema 的访问与建表权限。</p>
        <p className="sub">已有实例不一定已有 Relay 的库和账号。当前页面会检查条件，不会自动创建它们；缺少时请由数据库管理员按下面的说明处理。不要使用 LiteLLM 的业务库或 PostgreSQL 管理员账号。</p>
        <details>
          <summary>尚未创建 Relay 数据库或账号？展开操作说明</summary>
          <p>在 PostgreSQL 容器终端或数据库管理工具中使用管理员连接。以下示例的库名和账号名需与下方表单保持一致；专用账号同时负责 Relay 读写与建表。</p>
          <pre>{preparationSQL}</pre>
          <p className="sub">\password 和 \connect 是 psql 命令。使用图形化数据库工具时，可分别通过其账号管理和数据库连接功能完成。账号或库已存在时请核对密码与权限，不要重复创建或清空数据。</p>
        </details>
        <form onSubmit={event => { event.preventDefault(); void check(); }}>
          <fieldset disabled={busy}>
            <div className="form-grid">
              <label>PostgreSQL 主机
                <input required maxLength={253} placeholder="例如 postgres" autoComplete="off" value={connection.host}
                  onChange={event => change('host', event.target.value)} />
              </label>
              <label>端口
                <input type="number" required min="1" max="65535" step="1" value={connection.port}
                  onChange={event => change('port', event.target.value)} />
              </label>
              <label>Relay 专用数据库
                <input required maxLength={63} autoComplete="off" value={connection.database}
                  onChange={event => change('database', event.target.value)} />
              </label>
              <label>数据库用户名
                <input required maxLength={63} autoComplete="off" value={connection.username}
                  onChange={event => change('username', event.target.value)} />
              </label>
              <label>数据库密码
                <input type="password" required maxLength={1024} autoComplete="new-password" value={connection.password}
                  onChange={event => change('password', event.target.value)} />
              </label>
              <label>TLS 模式
                <select value={connection.sslmode} onChange={event => change('sslmode', event.target.value)}>
                  <option value="verify-full">验证证书与主机名（推荐远程连接）</option>
                  <option value="require">加密连接，不验证服务器身份</option>
                  <option value="disable">关闭 TLS（仅限可信同机私有网络）</option>
                </select>
              </label>
            </div>
            <p className="sub">检查由 Relay 容器发起。同网络的 PostgreSQL 使用服务名和容器内端口；不需要 NAS IP。密码仅用于本次检查，提交后清空输入框。</p>
            <Button type="submit" className="secondary" loading={busy}>检查连接与权限</Button>
          </fieldset>
        </form>
        {result && <div role="status" aria-live="polite">
          <h3>{result.ready ? '前置检查通过' : '仍有条件需要处理'}</h3>
          <ul>{result.checks.map(item => <li key={item.name}>
            <strong>{item.status === 'passed' ? '通过' : '需处理'}：</strong>{item.message}
          </li>)}</ul>
        </div>}
        <p className="sub">此入口仅做只读检查，不会保存连接、迁移数据或切换数据库。页面内迁移尚未开放；现有 SQLite 数据仍需停机使用 migrate-sqlite 迁移，保留原数据卷与密钥。检查通过不代表迁移完成。</p>
      </div>}
    </section>
  );
}
