import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { afterEach, test } from 'node:test';
import ts from 'typescript';

// Execute the real API module using the project's locked TypeScript compiler.
// No browser, database, network or additional test dependency is required.
const source = readFileSync(new URL('../src/api.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const originalFetch = globalThis.fetch;
const originalDocument = globalThis.document;
let moduleID = 0;
async function freshAPI() {
  globalThis.document = { cookie: 'relay_csrf=fixture-token' };
  const encoded = Buffer.from(compiled).toString('base64');
  return import(`data:text/javascript;base64,${encoded}#test-${++moduleID}`);
}
function deferred() {
  let resolve, reject;
  const promise = new Promise((onResolve, onReject) => { resolve = onResolve; reject = onReject; });
  return { promise, resolve, reject };
}
afterEach(() => {
  globalThis.fetch = originalFetch;
  if (originalDocument === undefined) delete globalThis.document;
  else globalThis.document = originalDocument;
});

test('current responses retain CSRF and same-origin credentials', async () => {
  const { api } = await freshAPI();
  globalThis.fetch = async (_url, options) => {
    assert.equal(options.headers['X-Relay-CSRF'], 'fixture-token');
    assert.equal(options.credentials, 'same-origin');
    return { ok: true, status: 200, json: async () => ({ data: { ok: true } }) };
  };
  assert.deepEqual((await api('/fixture', 'POST', {})).data, { ok: true });
});

test('current 401 remains an authentication error', async () => {
  const { api, APIError } = await freshAPI();
  globalThis.fetch = async () => ({
    ok: false, status: 401, json: async () => ({ error: { message: 'expired' } }),
  });
  await assert.rejects(api('/fixture'), error => error instanceof APIError && error.status === 401);
});

test('invalidation aborts transport and suppresses a late success even if abort is ignored', async () => {
  const { api, invalidateAPIRequests, APIRequestSupersededError } = await freshAPI();
  const pending = deferred();
  let signal;
  globalThis.fetch = (_url, options) => { signal = options.signal; return pending.promise; };
  const result = api('/fixture');
  invalidateAPIRequests();
  assert.equal(signal.aborted, true);
  pending.resolve({ ok: true, status: 200, json: async () => ({ data: 'old-session' }) });
  await assert.rejects(result, error => error instanceof APIRequestSupersededError && error.message === '');
});

test('a late 401 from an old session cannot expire the new session', async () => {
  const { api, invalidateAPIRequests, APIRequestSupersededError, APIError } = await freshAPI();
  const pending = deferred();
  globalThis.fetch = () => pending.promise;
  const old = api('/fixture');
  invalidateAPIRequests();
  pending.resolve({ ok: false, status: 401, json: async () => ({ error: { message: 'old expired' } }) });
  await assert.rejects(old, error => error instanceof APIRequestSupersededError && !(error instanceof APIError));
});

test('invalidation during response parsing suppresses the old body', async () => {
  const { api, invalidateAPIRequests, APIRequestSupersededError } = await freshAPI();
  const body = deferred();
  const parsing = deferred();
  globalThis.fetch = async () => ({
    ok: true, status: 200, json: () => { parsing.resolve(); return body.promise; },
  });
  const old = api('/fixture');
  await parsing.promise;
  invalidateAPIRequests();
  body.resolve({ data: 'old-body' });
  await assert.rejects(old, APIRequestSupersededError);
});

test('invalidation during parsing suppresses a stale malformed-body error', async () => {
  const { api, invalidateAPIRequests, APIRequestSupersededError } = await freshAPI();
  const body = deferred();
  const parsing = deferred();
  globalThis.fetch = async () => ({
    ok: true, status: 200, json: () => { parsing.resolve(); return body.promise; },
  });
  const old = api('/fixture');
  await parsing.promise;
  invalidateAPIRequests();
  body.reject(new Error('old malformed JSON'));
  await assert.rejects(old, APIRequestSupersededError);
});

test('current network failures remain visible', async () => {
  const { api } = await freshAPI();
  globalThis.fetch = async () => { throw new Error('network-offline'); };
  await assert.rejects(api('/fixture'), error => error.status === 0 && /无法连接 Relay/.test(error.message) && !error.message.includes('network-offline'));
});

test('a new generation can immediately read the new login identity', async () => {
  const { api, invalidateAPIRequests } = await freshAPI();
  invalidateAPIRequests();
  globalThis.fetch = async () => ({
    ok: true, status: 200, json: async () => ({ data: { id: 'admin', role: 'admin' } }),
  });
  assert.equal((await api('/auth/me')).data.id, 'admin');
});

test('API failures use Chinese stable codes and never expose arbitrary server errors', async () => {
  const { api } = await freshAPI();
  for (const [code, status, expected] of [['invalid_request', 400, '输入无效'], ['internal_error', 500, '操作未能完成'], ['new_error', 502, '操作未能完成']]) {
    globalThis.fetch = async () => ({ ok: false, status, json: async () => ({ error: { code, message: 'postgres://SECRET@host failed' } }) });
    await assert.rejects(api('/fixture'), error => error.message.includes(expected) && !error.message.includes('SECRET'));
  }
  globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => null });
  await assert.rejects(api('/fixture'), /无效响应/);
});

test('origin rejection names only the configured safe login origin', async () => {
 const { api, trustedLoginURL } = await freshAPI();
 globalThis.fetch = async () => ({ ok: false, status: 403, json: async () => ({error: { code: 'origin_rejected', login_url: 'https://relay.example.test:16666', message: '<unsafe upstream>' }}) });
 await assert.rejects(api('/auth/login', 'POST', {}), /https:\/\/relay.example.test:16666/);
 for (const value of ['javascript:alert(1)', 'https://user:secret@example.test', 'https://example.test/redirect', 'https://example.test?secret=x', null]) assert.equal(trustedLoginURL(value), '');
 assert.equal(trustedLoginURL('https://relay.example.test:16666'), 'https://relay.example.test:16666');
});
