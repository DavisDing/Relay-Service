import type { Alert, Service } from './types';
import { Button } from './ui';
import { severity, time } from './presentation';

type Props = {
  alerts: Alert[]; services: Service[]; ackCursor: number; busy: boolean; admin: boolean;
  skippedUnreadPage: boolean; canAck: boolean; hasPrevious: boolean; canNext: boolean;
  onAck: () => unknown; onSilence: (serviceID: string) => unknown;
  onPrevious: () => void; onNext: () => void;
};

export function AlertsPanel({ alerts, services, ackCursor, busy, admin, skippedUnreadPage,
  canAck, hasPrevious, canNext, onAck, onSilence, onPrevious, onNext }: Props) {
  return (
    <>
      <div className="section-head">
        <p>按时间顺序分页保留故障与恢复事件；徽标为当前故障或能力受限的服务数。</p>
        {alerts.length > 0 && (
          <Button className="secondary" disabled={busy || !canAck}
            title={skippedUnreadPage ? '请先返回前页，依次确认未读事件' : '仅确认本页已展示的连续未读事件'}
            onClick={onAck}>本页标记已读</Button>
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
                  <td><span className={'alert-severity ' + (alert.severity || 'unknown')}>{severity(alert.severity)}</span> {alert.message}
                    {alert.count > 0 && <small>第 {alert.count} 次失败触发</small>}
                    {alert.incident_id && <small>故障组 {alert.incident_id.slice(0, 8)}</small>}
                    {alert.check_id && <small>检查 {alert.check_id.slice(0, 8)}</small>}
                    <small>配置代际 {alert.generation}</small>
                    {alert.evidence_state === 'expired' && <small>检查明细已过保留期</small>}
                    {alert.evidence_state === 'unavailable' && <small>检查明细不可用，已保留事件摘要</small>}
                  </td><td>{time(alert.started_at)}</td>
                  <td>{alert.resolved_at ? '恢复事件' : '事件已记录'}
                    <small>{alert.cursor > ackCursor ? '未读' : '已读'}{alert.silenced ? ' · 静默至 ' + time(alert.silence_until) : ''}</small>
                  </td>
                  <td>{admin && (
                    <Button className="text" disabled={busy} onClick={() => onSilence(alert.service_id)}>静默 1 小时</Button>
                  )}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="buttons pagination">
        <Button className="secondary" disabled={busy || !hasPrevious} onClick={onPrevious}>上一页</Button>
        <Button className="secondary" disabled={busy || !canNext} onClick={onNext}>下一页</Button>
      </div>
    </>
  );
}
