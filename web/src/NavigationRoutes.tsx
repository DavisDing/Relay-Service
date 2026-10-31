import type { ModelsSection } from './models-types';
import type { NetworkSection } from './network-types';
import type { IconName } from './ui';

export type NavigationPage = { key: string; label: string; description: string; icon: IconName; restricted?: boolean };
type WorkspacePage<T> = NavigationPage & { section: T };

export const modelPages: WorkspacePage<ModelsSection>[] = [
  { key: 'models', section: 'overview', label: '总览', description: '模型目录、部署与来源覆盖', icon: 'grid' },
  { key: 'models-models', section: 'models', label: '模型', description: '按实例检索模型与部署证据', icon: 'grid' },
  { key: 'models-channels', section: 'channels', label: '渠道', description: '核对上游渠道与明确部署映射', icon: 'server' },
  { key: 'models-traffic', section: 'traffic', label: '调用证据', description: '已有调用样本与来源时间窗', icon: 'history' },
  { key: 'models-status', section: 'status', label: '公开状态', description: '站方报告的组件与模型状态', icon: 'pulse' },
  { key: 'models-probes', section: 'probes', label: '模型探针', description: '可用性、能力与质量探针说明', icon: 'pulse' },
];
export const networkPages: WorkspacePage<NetworkSection>[] = [
  { key: 'network', section: 'overview', label: '概览', description: '核心流量、连接与采集概况', icon: 'pulse' },
  { key: 'network-usage', section: 'usage', label: '用量', description: '实际采样流量与连接归因', icon: 'history' },
  { key: 'network-subscriptions', section: 'subscriptions', label: '订阅', description: '额度、到期与所属节点', icon: 'server' },
  { key: 'network-nodes', section: 'nodes', label: '节点', description: '当前检测、延迟与历史样本', icon: 'grid' },
  { key: 'network-connections', section: 'connections', label: '连接', description: '活动连接、路由与速率 · 管理员', icon: 'pulse', restricted: true },
  { key: 'network-logs', section: 'logs', label: '日志', description: '核心日志与采集事件 · 管理员', icon: 'history', restricted: true },
];

export function workspaceDestination(page: string) {
  const model = modelPages.find(item => item.key === page);
  if (model) return { workspace: 'models' as const, section: model.section };
  const network = networkPages.find(item => item.key === page);
  if (network) return { workspace: 'network' as const, section: network.section };
}
export function modelPage(section: ModelsSection) { return modelPages.find(item => item.section === section)!; }
export function networkPage(section: NetworkSection) { return networkPages.find(item => item.section === section)!; }

// Stable, explicit routes. Only known application paths get the SPA shell.
const pagePaths: Record<string, string> = {
 home: '/', services: '/services', alerts: '/alerts', devices: '/devices', settings: '/settings', diagnostic: '/diagnostic',
 logs: '/logs', 'logs-services': '/logs/services', 'logs-models': '/logs/models', 'logs-network': '/logs/network',
 ...Object.fromEntries(modelPages.map(page => [page.key, page.section === 'overview' ? '/models' : '/models/' + page.section])),
 ...Object.fromEntries(networkPages.map(page => [page.key, page.section === 'overview' ? '/network' : '/network/' + page.section])),
};
export function pagePath(page: string) { return pagePaths[page] || '/'; }
export function pageFromPath(path: string) {
 const normalized = path === '/' ? '/' : path.replace(/\/$/, '');
 return Object.entries(pagePaths).find(([, value]) => value === normalized)?.[0] || 'home';
}
