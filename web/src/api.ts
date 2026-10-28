export type Envelope<T> = { data: T; generated_at: string; relay_id: string };

export class APIError extends Error {
  constructor(message: string, public status: number) { super(message); }
}

// A superseded response belongs to an earlier login and must never update the current UI.
export class APIRequestSupersededError extends Error {
  constructor() { super(''); this.name = 'APIRequestSupersededError'; }
}

let requestGeneration = 0;
const pendingRequests = new Set<AbortController>();

export function invalidateAPIRequests() {
  requestGeneration++;
  for (const request of pendingRequests) request.abort();
  pendingRequests.clear();
}

function cookieValue(name: string): string {
  const item = document.cookie.split(';').map(value => value.trim())
    .find(value => value.startsWith(name + '='));
  return item ? decodeURIComponent(item.slice(name.length + 1)) : '';
}

export async function api<T>(path: string, method = 'GET', body?: unknown): Promise<Envelope<T>> {
  const generation = requestGeneration;
  const controller = new AbortController();
  function assertCurrent() {
    if (generation !== requestGeneration) throw new APIRequestSupersededError();
  }
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) {
    const csrf = cookieValue('relay_csrf');
    if (csrf) headers['X-Relay-CSRF'] = csrf;
  }
  pendingRequests.add(controller);
  try {
    let response: Response;
    try {
      response = await fetch('/api/v1' + path, {
        method,
        credentials: 'same-origin',
        headers,
        signal: controller.signal,
        body: body !== undefined ? JSON.stringify(body) : undefined,
      });
    } catch (reason) {
      assertCurrent();
      throw new APIError('无法连接 Relay，请检查网络后重试。', 0);
    }
    assertCurrent();
    let json;
    try { json = await response.json(); }
    catch {
      assertCurrent();
      throw new APIError('Relay 返回了无效响应，请检查连接。', response.status);
    }
    assertCurrent();
    if (!json || typeof json !== 'object' || Array.isArray(json)) throw new APIError('Relay 返回了无效响应，请检查连接。', response.status);
    if (!response.ok) throw new APIError(errorMessage(json.error?.code, response.status), response.status);
    return json;
  } finally {
    pendingRequests.delete(controller);
  }
}

export function errorMessage(code: string, status: number): string {
  const messages: Record<string, string> = {
    unauthorized: '账号、密码无效或登录已过期，请重新登录。',
    forbidden: '需要管理员权限才能执行此操作。',
    password_change_required: '请先设置正式密码。',
    invalid_request: '输入无效，请检查名称、地址、检查间隔及凭据；更换地址时需重新输入或删除原凭据。',
    conflict: '配置已发生变化，请刷新后重试。',
    bootstrap_disabled: '管理员初始化入口已关闭。',
    rate_limited: '尝试次数过多，请稍后重试。',
    request_timeout: '请求超时，请稍后重试。',
    paused: '服务已暂停，请先恢复检查。',
    not_found: '未找到对应记录，请刷新页面。',
    origin_rejected: '访问来源不受信任，请通过配置的 Relay 地址访问。',
    csrf_rejected: '安全校验失败，请刷新页面后重试。',
    not_ready: '数据库暂不可用，请稍后重试。',
    internal_error: '操作未能完成，请稍后重试或查看服务日志。',
    unsupported_media_type: '请求格式不支持，请使用 JSON。',
    body_too_large: '请求内容过大，请减少输入内容。',
    invalid_generation: '缺少配置版本，请刷新后重试。',
    invalid_limit: '读取数量应在 1 到 500 之间。',
    database_check_busy: '已有数据库检查正在进行，请稍后重试。',
    service_test_busy: '已有连通测试正在进行，请稍后重试。',
  };
  return messages[code] || (status === 401 ? messages.unauthorized : status === 403 ? messages.forbidden
    : status === 429 ? messages.rate_limited : status >= 500 ? messages.internal_error : '操作失败，请检查输入后重试。');
}
