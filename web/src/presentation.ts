export const kinds = ['litellm', 'hermes', 'workbuddy2api', 'openwebui', 'ollama', 'metacubexd', 'http', 'tcp'];
export const kindLabels: Record<string, string> = {
  litellm: 'LiteLLM',
  hermes: 'Hermes',
  workbuddy2api: 'workbuddy2api',
  openwebui: 'Open WebUI',
  ollama: 'Ollama',
  metacubexd: 'MetaCubeXD',
  http: 'HTTP 连通',
  tcp: 'TCP 连通',
};
export const states: Record<string, string> = {
  healthy: '检查成功',
  unhealthy: '检查失败',
  unauthorized: '认证失效',
  unsupported: '接口不支持',
  unknown: '未观测',
  queued: '检查排队',
  running: '检查中',
  interrupted: '已中断',
  paused: '已暂停',
};
export const time = (value?: string) => value && !value.startsWith('0001')
  ? new Date(value).toLocaleString('zh-CN')
  : '尚无记录';
export const evidence = (value?: string) => value === 'process' ? '进程可达' : value === 'connectivity_only' ? '仅端口连通' : '未知';
export const severity = (value?: string) => value === 'warning' ? '警告' : value === 'info' ? '信息' : '未分级';

