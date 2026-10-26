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
      throw reason;
    }
    assertCurrent();
    let json;
    try { json = await response.json(); }
    catch {
      assertCurrent();
      throw new APIError('Relay 返回了无效响应，请检查连接。', response.status);
    }
    assertCurrent();
    if (!response.ok) throw new APIError(json.error?.message || '操作失败，请重试', response.status);
    return json;
  } finally {
    pendingRequests.delete(controller);
  }
}
