// SVG geometry is allowed by the strict CSP; inline style attributes are not.
const states = ['healthy', 'failed', 'unknown', 'pending', 'paused'] as const;
export function StatusDistribution({ counts, className, label }: {
  counts: Partial<Record<typeof states[number], number>>; className: string; label?: string;
}) {
  const total = states.reduce((sum, key) => sum + Math.max(0, counts[key] || 0), 0);
  let offset = 0;
  return <svg className={`status-distribution ${className}`} viewBox={`0 0 ${total || 1} 1`} preserveAspectRatio="none" role={label ? 'img' : undefined} aria-label={label} aria-hidden={!label || undefined}>
    {states.map(key => {
      const width = Math.max(0, counts[key] || 0); const x = offset; offset += width;
      return width > 0 ? <rect key={key} className={key} x={x} y={0} width={width} height={1}><title>{({ healthy: '成功', failed: '异常', unknown: '待观测', pending: '检查中', paused: '暂停' })[key]} {width} 项</title></rect> : null;
    })}
  </svg>;
}
