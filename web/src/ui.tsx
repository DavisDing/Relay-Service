import { useEffect, useRef, useState } from 'react';
import type { ButtonHTMLAttributes, MouseEvent, ReactNode } from 'react';

const paths = {
  grid: <><rect x="3" y="3" width="7" height="7" rx="2"/><rect x="14" y="3" width="7" height="7" rx="2"/><rect x="3" y="14" width="7" height="7" rx="2"/><rect x="14" y="14" width="7" height="7" rx="2"/></>,
  bell: <><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4"/></>,
  devices: <><rect x="3" y="3" width="13" height="13" rx="2"/><path d="M6 21h8m-4-5v5"/><rect x="16" y="9" width="5" height="12" rx="1.5"/></>,
  pulse: <><path d="M2 12h5l3-8 4 16 3-8h5"/></>,
  refresh: <><path d="M20 7v5h-5M4 17v-5h5"/><path d="M6 6a8 8 0 0 1 14 6M18 18a8 8 0 0 1-14-6"/></>,
  plus: <path d="M12 5v14M5 12h14"/>,
  close: <path d="m6 6 12 12M6 18 18 6"/>,
  sun: <><circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5"/></>,
  moon: <path d="M21 13a9 9 0 0 1-10-10A9 9 0 1 0 21 13Z"/>,
  system: <><rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8m-4-4v4"/></>,
  history: <><path d="M3 4v5h5M3 9a9 9 0 1 1 0 7M12 7v5l3 2"/></>,
  external: <><path d="M14 3h7v7m0-7L10 14M10 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-5"/></>,
  edit: <><path d="m15 4 5 5M4 16 16 4a3.5 3.5 0 0 1 5 5L9 21H3Z"/></>,
  pause: <><path d="M8 5v14M16 5v14"/></>,
  play: <path d="m8 4 12 8-12 8Z"/>,
  check: <path d="m5 12 4 4L19 6"/>,
  logout: <><path d="M9 4H4v16h5m5-13 5 5-5 5m-7-5h12"/></>,
  server: <><rect x="3" y="3" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="7" rx="2"/><path d="M7 6.5h.01M7 17.5h.01M15 6.5h3m-3 11h3"/></>,
  search: <><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/></>,
  back: <path d="m14 5-7 7 7 7"/>,
  next: <path d="m10 5 7 7-7 7"/>,
  trash: <><path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7m4-7v7"/></>,
} satisfies Record<string, ReactNode>;
export type IconName = keyof typeof paths;
export function Icon({ name }: { name: IconName }) {
  return <svg className="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[name]}</svg>;
}

type ButtonProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'onClick'> & {
  loading?: boolean;
  onClick?: (event: MouseEvent<HTMLButtonElement>) => unknown;
};
export function Button({ loading = false, disabled, children, onClick, ...props }: ButtonProps) {
  const [pending, setPending] = useState(false);
  const waiting = loading || pending;
  async function click(event: MouseEvent<HTMLButtonElement>) {
    const result = onClick?.(event);
    if (result && typeof (result as Promise<unknown>).then === 'function') {
      setPending(true);
      try { await result; } finally { setPending(false); }
    }
  }
  return <button {...props} disabled={disabled || waiting} aria-busy={waiting || undefined} onClick={click}>
    {waiting ? <><span className="spinner" aria-hidden="true"/>处理中…</> : children}
  </button>;
}

type Theme = 'system' | 'light' | 'dark';
const themeOptions: { value: Theme; label: string; icon: IconName }[] = [
  { value: 'light', label: '浅色', icon: 'sun' },
  { value: 'dark', label: '深色', icon: 'moon' },
  { value: 'system', label: '跟随系统', icon: 'system' },
];
export function ThemeControl() {
  const [theme, setTheme] = useState<Theme>(() => {
    try { const saved = localStorage.getItem('relay-theme'); return saved === 'light' || saved === 'dark' ? saved : 'system'; }
    catch { return 'system'; }
  });
  useEffect(() => {
    const query = matchMedia('(prefers-color-scheme: dark)');
    function apply() {
      const resolved = theme === 'system' ? (query.matches ? 'dark' : 'light') : theme;
      document.documentElement.dataset.theme = resolved;
      document.querySelector('meta[name="theme-color"]')?.setAttribute('content', resolved === 'dark' ? '#000000' : '#f5f5f7');
    }
    apply();
    query.addEventListener('change', apply);
    try { localStorage.setItem('relay-theme', theme); } catch { /* Appearance remains available without storage. */ }
    return () => query.removeEventListener('change', apply);
  }, [theme]);
  return <div className="theme-control" role="group" aria-label="界面外观">{themeOptions.map(option =>
    <Button type="button" className="icon-button" key={option.value} aria-label={option.label} title={option.label} aria-pressed={theme === option.value} onClick={() => setTheme(option.value)}><Icon name={option.icon}/></Button>
  )}</div>;
}

export function Modal({ titleId, onClose, busy = false, compact = false, children }: {
  titleId: string; onClose: () => void; busy?: boolean; compact?: boolean; children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current!;
    const previousFocus = document.activeElement as HTMLElement | null;
    const overflow = document.body.style.overflow;
    dialog.showModal();
    document.body.style.overflow = 'hidden';
    return () => { dialog.close(); document.body.style.overflow = overflow; previousFocus?.focus(); };
  }, []);
  return <dialog ref={ref} className={'modal' + (compact ? ' compact' : '')} aria-labelledby={titleId} onCancel={event => { event.preventDefault(); if (!busy) onClose(); }}>
    {children}
  </dialog>;
}

export function Toast({ message, onDismiss }: { message: string; onDismiss: () => void }) {
  useEffect(() => { if (!message) return; const timer = setTimeout(onDismiss, 4500); return () => clearTimeout(timer); }, [message, onDismiss]);
  return <div className="toast-region" role="status" aria-live="polite" aria-atomic="true">{message && <div className="toast" key={message}><Icon name="check"/><span>{message}</span><Button className="icon-button" type="button" aria-label="关闭提示" onClick={onDismiss}><Icon name="close"/></Button></div>}</div>;
}
