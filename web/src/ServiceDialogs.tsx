import type { FormEvent } from 'react';
import type { Form, Service, Check } from './types';
import { kinds, kindLabels, states, time } from './presentation';
import { Button, Icon, Modal } from './ui';

export type History = { service: Service; items: Check[]; hasMore: boolean };

export function ServiceEditor({ form, setForm, busy, error, save, onRemove, confirming }: {
  form: Form; setForm: (form: Form | null) => void; busy: boolean; error: string; onRemove: () => void; confirming: boolean; save: (event: FormEvent) => Promise<void>;
}) {
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
            {error && !confirming && <p className="error" role="alert">{error}</p>}
            <div className="buttons">
              <Button type="submit" loading={busy}>保存接入</Button>
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
              <Button className="secondary" disabled={busy} onClick={onMore}>继续读取历史</Button>
            )}
          </>
        )}
      </section>
    </Modal>
  );
}
