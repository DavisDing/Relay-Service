import { useId } from 'react';
import type { ReactNode } from 'react';
import { Button, Icon } from './ui';
import './workspace.css';

export function WorkspaceTabs<T extends string>({ label, items, value, onChange }: {
  label: string;
  items: readonly { key: T; label: string; note?: string }[];
  value: T;
  onChange: (value: T) => void;
}) {
  return <nav className="workspace-tabs" aria-label={label}>
    {items.map(item => <Button type="button" className="text" key={item.key}
      aria-current={value === item.key ? 'page' : undefined} onClick={() => onChange(item.key)}>
      {item.label}{item.note && <small>{item.note}</small>}
    </Button>)}
  </nav>;
}

export function WorkspaceMetric({ title, value, note }: {
  title: string; value?: string | number; note: string;
}) {
  return <article className="workspace-metric"><p>{title}</p><strong>{value ?? '—'}</strong><small>{note}</small></article>;
}

export function WorkspaceEmpty({ children }: { children: ReactNode }) {
  return <div className="workspace-empty"><Icon name="pulse" /><p>{children}</p></div>;
}

export function WorkspaceSection({ title, description, children }: { title: string; description: string; children?: ReactNode }) {
  return <div className="workspace-section-heading"><div><h2>{title}</h2><p>{description}</p></div>{children}</div>;
}

export function WorkspacePagination({ total, page, loading = false, hasPrevious, hasNext, onPrevious, onNext, label = '列表分页' }: {
  total: number; page: number; loading?: boolean; hasPrevious: boolean; hasNext: boolean;
  onPrevious: () => void; onNext: () => void; label?: string;
}) {
  const statusID = useId();
  return <nav className="workspace-pagination" aria-label={label} aria-describedby={statusID}>
    <span id={statusID} role="status">共 {total} 条 · 第 {page} 页{loading ? ' · 同步中' : ''}</span>
    <div className="buttons pagination"><Button type="button" className="secondary" disabled={!hasPrevious || loading} onClick={onPrevious}><Icon name="back" />上一页</Button><Button type="button" className="secondary" disabled={!hasNext || loading} onClick={onNext}>下一页<Icon name="next" /></Button></div>
  </nav>;
}
