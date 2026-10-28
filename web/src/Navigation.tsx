import { useEffect, useRef, useState } from 'react';
import type { KeyboardEvent, MouseEvent, PointerEvent } from 'react';
import { Button, Icon, Modal, ThemeControl } from './ui';
import type { IconName } from './ui';

type Page = { key: string; label: string; description: string; icon: IconName };
const monitoring: Page[] = [
  { key: 'services', label: '服务总览', description: '运行态势、分组与检查明细', icon: 'grid' },
  { key: 'alerts', label: '故障与告警', description: '故障、恢复与访问受限事件', icon: 'bell' },
];
const management: Page[] = [
  { key: 'devices', label: '访问设备', description: '配对设备与撤销访问', icon: 'devices' },
  { key: 'settings', label: '系统设置', description: '数据保留与清理策略', icon: 'system' },
];
const diagnostic: Page = { key: 'diagnostic', label: '诊断摘要', description: '生成与导出脱敏诊断', icon: 'pulse' };
const network: Page = { key: 'network', label: '网络代理', description: '流量、订阅、节点、连接与日志', icon: 'server' };

type Menu = 'monitoring' | 'management' | 'account';
type Props = {
  active: string;
  admin: boolean;
  busy: boolean;
  incidentCount: number;
  onNavigate: (page: string) => void;
  onLogout: () => unknown;
};

export function Navigation({ active, admin, busy, incidentCount, onNavigate, onLogout }: Props) {
  const [menu, setMenu] = useState<Menu | null>(null);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [scrolled, setScrolled] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement | null>(null);
  const opening = useRef<'hover' | 'press'>('press');
  const focusMenu = useRef<Menu | null>(null);

  useEffect(() => {
    if (!menu || focusMenu.current !== menu) return;
    const frame = requestAnimationFrame(() => {
      root.current?.querySelector<HTMLButtonElement>(`#nav-${menu} button:not(:disabled)`)?.focus();
      focusMenu.current = null;
    });
    return () => cancelAnimationFrame(frame);
  }, [menu]);

  useEffect(() => {
    const media = matchMedia('(max-width: 760px)');
    const resize = () => { setMenu(null); if (!media.matches) setMobileOpen(false); };
    const scroll = () => setScrolled(window.scrollY > 8);
    scroll();
    media.addEventListener('change', resize);
    window.addEventListener('scroll', scroll, { passive: true });
    return () => {
      media.removeEventListener('change', resize);
      window.removeEventListener('scroll', scroll);
    };
  }, []);

  function close(restoreFocus = false) {
    focusMenu.current = null;
    setMenu(null);
    if (restoreFocus) trigger.current?.focus();
  }
  function navigate(page: string) {
    close();
    setMobileOpen(false);
    onNavigate(page);
  }
  function open(value: Menu, button: HTMLButtonElement, keyboard = false) {
    trigger.current = button;
    if (menu === value && opening.current === 'press' && !keyboard) { close(); return; }
    opening.current = 'press';
    if (keyboard) focusMenu.current = value;
    setMenu(value);
    if (keyboard && menu === value) root.current?.querySelector<HTMLButtonElement>(`#nav-${value} button:not(:disabled)`)?.focus();
  }
  function hover(value: Menu, button: HTMLButtonElement, pointerType: string) {
    if (pointerType !== 'mouse' || busy || !matchMedia('(hover: hover) and (pointer: fine)').matches) return;
    // Moving back over a clicked menu must not turn its pinned state into a hover.
    if (!menu || opening.current !== 'press') opening.current = 'hover';
    trigger.current = button;
    setMenu(value);
  }
  function triggerProps(value: Menu) {
    return {
      'aria-expanded': menu === value,
      'aria-controls': 'nav-' + value,
      onClick: (event: MouseEvent<HTMLButtonElement>) => open(value, event.currentTarget),
      onPointerEnter: (event: PointerEvent<HTMLButtonElement>) => hover(value, event.currentTarget, event.pointerType),
      onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => {
        if (event.key === 'ArrowDown') { event.preventDefault(); open(value, event.currentTarget, true); }
      },
    };
  }
  function pageButton(page: Page, mobile = false) {
    return <Button key={page.key} className={mobile ? 'mobile-nav-link' : 'flyout-link'}
      disabled={busy} aria-current={active === page.key ? 'page' : undefined} onClick={() => navigate(page.key)}>
      <span>{page.label}{page.key === 'alerts' && incidentCount > 0 && <b className="nav-count" aria-label={`${incidentCount} 个服务有未恢复事件`}>{incidentCount}</b>}</span>
      <small>{page.description}</small>
    </Button>;
  }
  function panel(value: Menu, title: string, pages: Page[]) {
    return <section key={value} id={'nav-' + value} className="nav-flyout" data-open={menu === value}
      aria-label={title} aria-hidden={menu !== value} inert={menu !== value}>
      <div className="flyout-clip"><div className="flyout-inner">
        <div className="flyout-primary"><p className="nav-caption">{title}</p>{pages.map(page => pageButton(page))}</div>
        <div className="flyout-context"><p className="nav-caption">{value === 'monitoring' ? '监控范围' : '管理范围'}</p>
          <h2>{value === 'monitoring' ? '每一次观测，都有依据。' : '连接、授权与数据策略。'}</h2>
          <p>{value === 'monitoring' ? '集中查看服务的基础检查、数据新鲜度与未恢复事件。' : '管理 Relay 的设备访问与保留策略，检查凭据保留在服务端。'}</p>
          {value === 'monitoring' && <span className="nav-context-note">无推理基础检查 · 独立证据层级</span>}
        </div>
      </div></div>
    </section>;
  }

  return <>
    <a className="skip-link" href="#main-content">跳到主要内容</a>
    <div ref={root} className="global-nav" data-open={menu !== null} data-scrolled={scrolled}
      onPointerLeave={() => {
        const focusedPanel = root.current?.querySelector('.nav-flyout[data-open="true"]')?.contains(document.activeElement);
        if (opening.current === 'hover' && !focusedPanel) close();
      }}
      onBlur={event => {
        const next = event.relatedTarget as HTMLElement | null;
        // Let the scrim click restore focus before its closed state makes it inert.
        if (next && !event.currentTarget.contains(next) && !next.classList.contains('nav-scrim')) close();
      }}
      onKeyDown={event => { if (event.key === 'Escape' && menu) { event.preventDefault(); event.stopPropagation(); close(true); } }}>
      <div className="global-nav-bar">
        <Button className="nav-brand" aria-label="Relay 服务总览" disabled={busy} onClick={() => navigate('services')}><Icon name="pulse" /><span>Relay</span></Button>
        <nav className="global-nav-links" aria-label="主要导航">
          <Button className="nav-trigger" data-active={active === 'services' || active === 'alerts'} disabled={busy} {...triggerProps('monitoring')}>监控{incidentCount > 0 && <i className="nav-alert-dot" aria-label={`${incidentCount} 个服务有未恢复事件`} />}</Button>
          <Button className="nav-trigger" data-active={active === 'network'} aria-current={active === 'network' ? 'page' : undefined} disabled={busy}
            onPointerEnter={() => close()} onClick={() => navigate('network')}>网络代理</Button>
          {admin && <>
            <Button className="nav-trigger" data-active={active === 'devices' || active === 'settings'} disabled={busy} {...triggerProps('management')}>管理</Button>
            <Button className="nav-trigger" data-active={active === 'diagnostic'} aria-current={active === 'diagnostic' ? 'page' : undefined} disabled={busy}
              onPointerEnter={() => close()} onClick={() => navigate('diagnostic')}>诊断</Button>
          </>}
        </nav>
        <div className="global-nav-tools">
          <span className="nav-role">{admin ? '管理员' : '只读设备'}</span>
          <Button className="icon-button nav-account" aria-label="外观与账户" {...triggerProps('account')}><Icon name="person" /></Button>
          <Button className="icon-button nav-mobile-toggle" aria-label="打开导航菜单" aria-expanded={mobileOpen} aria-controls="mobile-navigation" disabled={busy}
            onClick={() => { close(); setMobileOpen(true); }}><Icon name="menu" /></Button>
        </div>
      </div>
      {panel('monitoring', '浏览监控', monitoring)}
      {admin && panel('management', '管理 Relay', management)}
      <section id="nav-account" className="nav-flyout" data-open={menu === 'account'} aria-label="外观与账户" aria-hidden={menu !== 'account'} inert={menu !== 'account'}>
        <div className="flyout-clip"><div className="flyout-inner account-inner">
          <div><p className="nav-caption">界面外观</p><h2>适合此刻的明暗。</h2><ThemeControl /></div>
          <div className="account-session"><p className="nav-caption">当前访问</p><h2>{admin ? '管理员' : '只读设备'}</h2><p>{admin ? '服务接入与运维管理' : '服务状态与告警只读访问'}</p><Button className="text" disabled={busy} onClick={onLogout}><Icon name="logout" />退出登录</Button></div>
        </div></div>
      </section>
    </div>
    <button type="button" className="nav-scrim" data-open={menu !== null} aria-label="关闭展开的导航" tabIndex={-1} aria-hidden={menu === null} inert={menu === null} onClick={() => close(true)} />
    {mobileOpen && <Modal titleId="mobile-navigation-title" className="navigation-dialog" onClose={() => setMobileOpen(false)}>
      <div id="mobile-navigation" className="mobile-navigation">
        <div className="mobile-nav-head"><span>Relay</span><Button className="icon-button" aria-label="关闭导航菜单" autoFocus data-autofocus onClick={() => setMobileOpen(false)}><Icon name="close" /></Button></div>
        <h2 id="mobile-navigation-title" className="nav-caption">浏览控制台</h2>
        <nav aria-label="移动端导航">{[...monitoring, network].map(page => pageButton(page, true))}{admin && [...management, diagnostic].map(page => pageButton(page, true))}</nav>
        <div className="mobile-nav-foot"><span>{admin ? '管理员' : '只读设备'}</span><Button className="text" onClick={() => {
          setMobileOpen(false);
          opening.current = 'press';
          trigger.current = root.current?.querySelector<HTMLButtonElement>('.nav-account') || null;
          focusMenu.current = 'account';
          setMenu('account');
        }}>外观与账户<Icon name="next" /></Button></div>
      </div>
    </Modal>}
  </>;
}
