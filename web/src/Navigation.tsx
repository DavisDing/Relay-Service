import { useEffect, useRef, useState } from 'react';
import type { KeyboardEvent, MouseEvent, PointerEvent } from 'react';
import { Button, Icon, Modal, ThemeControl } from './ui';
import { modelPages, networkPages } from './NavigationRoutes';
import type { NavigationPage } from './NavigationRoutes';

type Page = NavigationPage;
const monitoring: Page[] = [
  { key: 'services', label: '服务监控', description: '服务运行态势、分组与检查明细', icon: 'grid' },
  { key: 'alerts', label: '故障与告警', description: '故障、恢复与访问受限事件', icon: 'bell' },
];
const management: Page[] = [
  { key: 'devices', label: '访问设备', description: '配对设备与撤销访问', icon: 'devices' },
  { key: 'settings', label: '系统设置', description: '数据保留与清理策略', icon: 'system' },
];
const diagnostic: Page = { key: 'diagnostic', label: '诊断摘要', description: '生成与导出脱敏诊断', icon: 'pulse' };
const home: Page = { key: 'home', label: '首页', description: '服务、模型与网络代理的全局概览', icon: 'pulse' };
const logs: Page[] = [
  { key: 'logs', label: '日志总览', description: '按来源查看记录与采集覆盖', icon: 'history' },
  { key: 'logs-services', label: '服务日志', description: '已保存的服务检查与错误记录', icon: 'server' },
  { key: 'logs-models', label: '大模型日志', description: '模型调用聚合与样本时间窗', icon: 'grid' },
  { key: 'logs-network', label: '网络代理日志', description: '核心日志与采集事件 · 管理员', icon: 'pulse' },
];

type Menu = 'monitoring' | 'models' | 'network' | 'management' | 'logs' | 'account';
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
        <div className="flyout-context"><p className="nav-caption">{value === 'logs' ? '记录与来源' : value === 'models' ? '模型证据' : value === 'network' ? '代理观测' : value === 'monitoring' ? '监控范围' : '管理范围'}</p>
          <h2>{value === 'logs' ? '每一条记录，都有出处。' : value === 'models' ? '目录、调用与状态，分别核对。' : value === 'network' ? '流量、订阅与节点，一处查看。' : value === 'monitoring' ? '每一次观测，都有依据。' : '连接、授权与数据策略。'}</h2>
          <p>{value === 'logs' ? '查看服务检查、模型调用证据与代理日志，保留各自的来源、权限和时间范围。' : value === 'models' ? '按实例查看目录、渠道和已有调用证据；公开状态与主动探针保持独立。' : value === 'network' ? '查看采集到的流量、订阅、节点与连接；未知和检测缺口分别呈现。' : value === 'monitoring' ? '集中查看服务的基础检查、数据新鲜度与未恢复事件。' : '管理 Relay 的设备访问与保留策略，检查凭据保留在服务端。'}</p>
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
        <Button className="nav-brand" aria-label="Relay 首页" disabled={busy} onClick={() => navigate('home')}><Icon name="pulse" /><span>Relay</span></Button>
        <nav className="global-nav-links" aria-label="主要导航">
          <Button className="nav-trigger" data-active={active === 'home'} aria-current={active === 'home' ? 'page' : undefined} disabled={busy}
            onPointerEnter={() => close()} onClick={() => navigate('home')}>首页</Button>
          <Button className="nav-trigger" data-active={active === 'services' || active === 'alerts'} disabled={busy} {...triggerProps('monitoring')}>服务监控{incidentCount > 0 && <i className="nav-alert-dot" aria-label={`${incidentCount} 个服务有未恢复事件`} />}</Button>
          <Button className="nav-trigger" data-active={modelPages.some(page => page.key === active)} disabled={busy} {...triggerProps('models')}>大模型</Button>
          <Button className="nav-trigger" data-active={networkPages.some(page => page.key === active)} disabled={busy} {...triggerProps('network')}>网络代理</Button>
          <Button className="nav-trigger" data-active={active === 'logs' || active.startsWith('logs-')} disabled={busy} {...triggerProps('logs')}>日志</Button>
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
      {panel('models', '浏览大模型', modelPages)}
      {panel('network', '浏览网络代理', networkPages)}
      {panel('logs', '浏览日志', logs)}
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
        <nav aria-label="移动端导航">{pageButton(home, true)}<p className="mobile-nav-group">服务监控</p>{monitoring.map(page => pageButton(page, true))}<p className="mobile-nav-group">大模型</p>{modelPages.map(page => pageButton(page, true))}<p className="mobile-nav-group">网络代理</p>{networkPages.map(page => pageButton(page, true))}<p className="mobile-nav-group">日志</p>{logs.map(page => pageButton(page, true))}{admin && <><p className="mobile-nav-group">管理</p>{[...management, diagnostic].map(page => pageButton(page, true))}</>}</nav>
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
