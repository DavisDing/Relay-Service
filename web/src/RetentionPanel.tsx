import { useEffect, useRef, useState } from 'react';
import { api, APIError, APIRequestSupersededError } from './api';
import { Button, Modal } from './ui';

type Policy = {
  history_days: number;
  audit_days: number;
  generation: number;
};
type Preview = {
  id: string;
  expires_at: string;
  policy: Policy;
  history_before?: string;
  audit_before?: string;
  history_count: number;
  audit_count: number;
};

function period(days: number) {
  return days === 0 ? '永久保留' : `${days} 天`;
}
function cutoff(value?: string) {
  return value ? new Date(value).toLocaleString('zh-CN') : '不清理';
}

export function RetentionPanel({ onNotice, onUnauthorized }: {
  onNotice: (message: string) => void;
  onUnauthorized: () => void;
}) {
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [historyDays, setHistoryDays] = useState('0');
  const [auditDays, setAuditDays] = useState('0');
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const unauthorized = useRef(onUnauthorized);
  unauthorized.current = onUnauthorized;

  function failed(reason: unknown) {
    if (reason instanceof APIRequestSupersededError) return;
    if (reason instanceof APIError && reason.status === 401) {
      unauthorized.current();
      return;
    }
    setError(reason instanceof Error ? reason.message : '数据保留设置读取失败');
  }

  async function reloadPolicy() {
    setBusy(true);
    setError('');
    try {
      const { data } = await api<Policy>('/retention');
      setPolicy(data);
      setHistoryDays(String(data.history_days));
      setAuditDays(String(data.audit_days));
      setPreview(null);
    } catch (reason) {
      failed(reason);
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    let active = true;
    void api<Policy>('/retention').then(({ data }) => {
      if (!active) return;
      setPolicy(data);
      setHistoryDays(String(data.history_days));
      setAuditDays(String(data.audit_days));
    }).catch(reason => { if (active) failed(reason); });
    return () => { active = false; };
  }, []);

  async function makePreview() {
    if (!policy) return;
    setBusy(true);
    setError('');
    try {
      const { data } = await api<Preview>('/retention/preview', 'POST', {
        history_days: Number(historyDays),
        audit_days: Number(auditDays),
        generation: policy.generation,
      });
      setPreview(data);
    } catch (reason) {
      failed(reason);
    } finally {
      setBusy(false);
    }
  }

  async function confirmPolicy() {
    if (!preview) return;
    setBusy(true);
    setError('');
    try {
      const { data } = await api<Policy>('/retention', 'PUT', { preview_id: preview.id });
      setPolicy(data);
      setHistoryDays(String(data.history_days));
      setAuditDays(String(data.audit_days));
      setPreview(null);
      onNotice('保留策略已保存。后台将按策略分批处理，告警继续永久保留。');
    } catch (reason) {
      failed(reason);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-labelledby="retention-title">
      <h2 id="retention-title">数据保留</h2>
      <p className="sub">
        当前检查明细：{policy ? period(policy.history_days) : '读取中'}；
        审计：{policy ? period(policy.audit_days) : '读取中'}。告警永久保留。
      </p>
      <p className="sub">0 表示永久保留。检查明细建议 30 天；修改前先预览影响，确认后才启用自动清理。</p>
      {error && !preview && <p className="banner error" role="alert">{error}</p>}
      <Button type="button" className="text" disabled={busy} onClick={reloadPolicy}>重新读取当前策略</Button>
      <form onSubmit={event => { event.preventDefault(); void makePreview(); }}>
        <fieldset disabled={busy || policy === null}>
          <div className="form-grid">
            <label>
              检查明细保留天数
              <input type="number" min="0" max="3650" step="1" required value={historyDays}
                onChange={event => setHistoryDays(event.target.value)} />
            </label>
            <label>
              审计保留天数
              <input type="number" min="0" max="3650" step="1" required value={auditDays}
                onChange={event => setAuditDays(event.target.value)} />
            </label>
          </div>
          <Button type="submit" className="secondary" loading={busy}>预览保留策略</Button>
        </fieldset>
      </form>
      {preview && (
        <Modal titleId="retention-confirm-title" compact busy={busy} onClose={() => setPreview(null)}>
          <div className="dialog">
            <h2 id="retention-confirm-title">确认数据保留策略</h2>
            <p>检查明细：{period(preview.policy.history_days)}；审计：{period(preview.policy.audit_days)}。</p>
            <p>截至预览时，检查明细 {preview.history_count} 条，早于 {cutoff(preview.history_before)}；
              审计 {preview.audit_count} 条，早于 {cutoff(preview.audit_before)}。</p>
            <p>确认后后台每 30 秒分批清理到期记录，后续新增数据也适用。清理不可恢复；
              预览计数会随采集变化。告警摘要、当前状态和在途检查继续保留，过期明细无法再展开。</p>
            <p className="sub">预览有效至 {new Date(preview.expires_at).toLocaleString('zh-CN')}。</p>
            {error && <p className="banner error" role="alert">{error}</p>}
            <div className="buttons">
              <Button type="button" className="secondary" disabled={busy} autoFocus onClick={() => setPreview(null)}>取消</Button>
              {error && <Button type="button" className="secondary" disabled={busy} onClick={reloadPolicy}>重读策略后重新预览</Button>}
              <Button type="button" loading={busy} onClick={confirmPolicy}>确认并保存</Button>
            </div>
          </div>
        </Modal>
      )}
    </section>
  );
}
