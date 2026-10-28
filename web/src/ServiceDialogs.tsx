import { useEffect, useState } from 'react';
import { CheckDetails } from './CheckDetails';
import type { FormEvent } from 'react';
import type { Form, Service, Check } from './types';
import { message, checkClass, kinds, kindLabels, states, time } from './presentation';
import { Button, Icon, Modal } from './ui';

export type History = { service: Service; items: Check[]; hasMore: boolean };

export function ServiceEditor({ form, setForm, busy, error, save, onRemove, confirming, testConnection }: {
  testConnection: (form: Form) => Promise<Check | null>;
  form: Form; setForm: (form: Form | null) => void; busy: boolean; error: string; onRemove: () => void; confirming: boolean; save: (event: FormEvent) => Promise<void>;
}) {
  const [preview, setPreview] = useState<Check | null>(null);
  const [testing, setTesting] = useState(false);
  useEffect(() => { setPreview(null); }, [form]);
  async function test() {
    setPreview(null);
    setTesting(true);
    try { setPreview(await testConnection(form)); } finally { setTesting(false); }
  }
  return (
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
                <select value={form.kind} onChange={event => setForm({ ...form, kind: event.target.value, public_status: undefined, collect_models: false, collect_logs: false, ...(event.target.value === 'statuspage' ? { token: '', clear_token: !!form.id } : {}) })}>
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
                <input type="password" autoComplete="off" value={form.token} disabled={form.clear_token || form.kind === 'statuspage'}
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
            <p className="muted">检查使用只读接口，不发推理请求。服务地址只包含协议、主机和端口；公开状态页的路径在下方配置。</p>
            {form.kind === 'mihomo' && <p className="muted">填写 mihomo 控制 API 地址及 secret。读取订阅流量、剩余额度、到期及节点已有延迟/在线历史；不主动测速或刷新订阅。</p>}
            {(form.kind === 'litellm' || form.kind === 'workbuddy2api') && <>
              <label className="checkbox"><input type="checkbox" checked={!!form.collect_models} onChange={event => setForm({ ...form, collect_models: event.target.checked })} />采集模型目录{form.kind === 'litellm' ? '与渠道部署清单' : ''}</label>
              {form.kind === 'litellm' && <label className="checkbox"><input type="checkbox" checked={!!form.collect_logs} onChange={event => setForm({ ...form, collect_logs: event.target.checked })} />采集最近24小时调用日志元数据与模型成功/失败样本</label>}
              <p className="muted">仅发只读请求。目录可见不等于模型可用；LiteLLM 日志需要其数据库日志已启用及相应读取权限，仅展示有限页样本，不采集提示词或回答。wb2api 未核实独立日志接口，可从 LiteLLM 下该渠道的调用样本观察。</p>
            </>}
            {form.kind === 'openai' && <p className="muted">读取 /v1/models，支持提供此标准接口的中转站；填 API Key。只能确认目录可读，不能证明逐模型推理成功或计算失败率。</p>}
            {form.kind === 'statuspage' && <>
              <div className="form-grid">
                <label>状态页格式<select value={form.public_status?.format || 'auto'} onChange={event => {
                  const format = event.target.value;
                  setForm({ ...form, token: '', clear_token: !!form.id, public_status: { format, resource_path: format === 'pipio' ? '/api/uptime/status' : format === 'html' ? '/status' : '/api/v2/summary.json' } });
                }}>
                  <option value="auto">自动识别已支持的 JSON</option><option value="pipio">Pipio 站方模型探针</option>
                  <option value="statuspage">Statuspage 标准</option><option value="json">自定义 JSON 字段</option><option value="html">普通 HTML 显式匹配</option>
                </select></label>
                <label>公开状态接口路径<input value={form.public_status?.resource_path || '/api/v2/summary.json'} placeholder="/api/uptime/status" onChange={event => setForm({ ...form, public_status: { ...form.public_status, resource_path: event.target.value } })} /></label>
                {form.public_status?.format === 'json' && <>
                  {([['items_path', '列表路径（点分字段）'], ['name_field', '名称字段'], ['status_field', '状态字段']] as const).map(([key, label]) => <label key={key}>{label}<input value={form.public_status?.[key] || ''} onChange={event => setForm({ ...form, public_status: { ...form.public_status, [key]: event.target.value } })} /></label>)}
                  {([['healthy_values', '正常值（逗号分隔）'], ['unhealthy_values', '异常值（逗号分隔）']] as const).map(([key, label]) => <label key={key}>{label}<input value={form.public_status?.[key]?.join(',') || ''} onChange={event => setForm({ ...form, public_status: { ...form.public_status, [key]: event.target.value.split(',') } })} /></label>)}
                </>}
                {form.public_status?.format === 'html' && <>
                  {([['html_item_tag', '组件标签（例如 div）'], ['html_item_attribute', '组件匹配属性（例如 class）'], ['html_item_value', '组件匹配值'], ['html_name_attribute', '名称属性（例如 data-name）'], ['html_status_attribute', '状态属性（例如 data-status）']] as const).map(([key, label]) => <label key={key}>{label}<input value={form.public_status?.[key] || ''} onChange={event => setForm({ ...form, public_status: { ...form.public_status, [key]: event.target.value } })} /></label>)}
                  {([['healthy_values', '正常状态精确文本（逗号分隔）'], ['unhealthy_values', '异常状态精确文本（逗号分隔）']] as const).map(([key, label]) => <label key={key}>{label}<input value={form.public_status?.[key]?.join(',') || ''} onChange={event => setForm({ ...form, public_status: { ...form.public_status, [key]: event.target.value.split(',') } })} /></label>)}
                </>}
              </div>
              <p className="muted">Pipio 填 https://pipio.io 并选择站方模型探针，将读取 /status 页面使用的公开数据。其他页面可配置 JSON 字段或 HTML 组件匹配；只返回 JavaScript 空壳且无公开数据的页面不能判断。公开状态页不发送检查凭据。</p>
            </>}
            {preview && <div className="connection-result" role="status">
              <strong>{states[preview.status] || '状态未知'} · {preview.duration_ms ?? '未知'} ms</strong>
              <p>{message(preview.message)}</p>
              <CheckDetails check={preview} />
              <p className="muted">这是本次表单的临时检查，尚未保存服务或检查历史。</p>
            </div>}
            {error && !confirming && <p className="error" role="alert">{error}</p>}
            <div className="buttons">
              <Button type="button" className="secondary" disabled={busy || !form.base_url || !form.kind} loading={testing} onClick={test}>连通测试</Button>
              <Button type="submit" loading={busy && !testing}>保存接入</Button>
              {form.id && (
                <Button className="danger secondary" type="button" disabled={busy} onClick={onRemove}><Icon name="trash" />移除接入</Button>
              )}
            </div>
          </fieldset>
        </form>
      </section>
    </Modal>
  );
}

export function HistoryDialog({ history, setHistory, busy, error, onMore }: {
  history: History; setHistory: (history: History | null) => void; busy: boolean; error: string; onMore: () => Promise<unknown>;
}) {
  return (
    <Modal className="history-modal" titleId="history-title" busy={busy} onClose={() => { if (!busy) setHistory(null); }}>
      <section className="dialog wide">
        <div className="section-head">
          <h2 id="history-title">{history.service.name} · 检查历史</h2>
          <Button className="text" disabled={busy} onClick={() => setHistory(null)}>关闭</Button>
        </div>
        {error && <p className="error" role="alert">{error}</p>}
        {history.items.length === 0 ? <p>尚无检查记录。</p> : (
          <>
            <div className="table-wrap">
              <table className="history-table">
                <thead><tr><th>完成时间</th><th>结果</th><th>耗时</th><th>原因</th></tr></thead>
                <tbody>{history.items.map(check => (
                  <tr key={check.id}>
                    <td>{time(check.completed_at)}</td><td><span className={'status ' + check.status}>{states[check.status] || check.status}</span></td>
                    <td>{check.duration_ms ?? '未知'} ms</td><td>{checkClass(check.class)}<small>{message(check.message)}</small><CheckDetails check={check} /></td>
                  </tr>
                ))}</tbody>
              </table>
            </div>
            {history.hasMore && (
              <Button className="secondary" disabled={busy} onClick={onMore}>继续读取历史</Button>
            )}
          </>
        )}
      </section>
    </Modal>
  );
}
