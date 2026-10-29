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
