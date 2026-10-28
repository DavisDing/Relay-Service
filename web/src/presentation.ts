export const kinds = ['litellm', 'hermes', 'workbuddy2api', 'openwebui', 'ollama', 'metacubexd', 'mihomo', 'openai', 'statuspage', 'http', 'tcp'];
export const kindLabels: Record<string, string> = {
  litellm: 'LiteLLM',
  hermes: 'Hermes',
  workbuddy2api: 'workbuddy2api',
  openwebui: 'Open WebUI',
  ollama: 'Ollama',
  metacubexd: 'MetaCubeXD',
  mihomo: 'mihomo 订阅与节点质量',
  openai: '中转站 / OpenAI 模型目录',
  statuspage: '公开状态页 / 站方探针',
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
export const evidence = (value?: string) => value === 'model_catalog' ? '模型目录可读' : value === 'subscription_metadata' ? '订阅元数据' : value === 'provider_reported' ? '服务商报告' : value === 'process' ? '进程可达' : value === 'connectivity_only' ? '仅端口连通' : '未知';
export const severity = (value?: string) => value === 'warning' ? '警告' : value === 'info' ? '信息' : '未分级';


const legacyMessages: Record<string, string> = {
  'TCP 连接成功；应用就绪状态和推理能力未测试。': 'TCP 连接成功；尚未检查应用就绪状态。',
  'HTTP 地址响应成功；应用就绪状态和推理能力未测试。': 'HTTP 地址响应成功；尚未检查应用就绪状态。',
  '进程存活接口响应成功；网关就绪状态和推理能力未测试。': '进程存活接口响应成功；尚未检查网关就绪状态。',
  '版本接口响应成功；模型和推理能力未测试。': '版本接口响应成功；仅确认服务版本信息可读取。',
  '进程健康检查成功；推理能力未测试。': '进程健康检查成功。',
  "已读取服务商公开状态或站方探针；未执行本地模型推理。": "已读取服务商公开状态或站方探针，详见各组件状态。",
  "已读取公开状态页；状态由服务商报告，未执行模型推理测试。": "已读取公开状态页；状态由服务商报告。",
  "Connection origin is invalid": "服务地址无效，请仅填写协议、主机和端口。",
  "TCP connection succeeded; application readiness and inference are untested": "TCP 连接成功；尚未检查应用就绪状态。",
  "Monitoring credential is unavailable": "检查凭据不可用，请重新配置。",
  "Monitoring credential could not be decrypted": "检查凭据无法解密，请重新配置。",
  "Monitoring credential is invalid": "检查凭据格式无效，请重新配置。",
  "Monitoring credential was rejected": "检查凭据被拒绝，请核对凭据和权限。",
  "Health endpoint redirected; target and authentication need verification": "检查接口发生重定向，请核对地址和认证方式。",
  "Health endpoint is not supported by this installation": "当前服务未提供此检查接口，请核对服务类型与版本。",
  "Health endpoint rate limited the check": "检查被服务限流，请稍后重试。",
  "Health endpoint returned an unsuccessful status": "检查接口返回错误状态，请检查上游服务。",
  "Health response was incomplete": "检查响应不完整，请重试。",
  "Health response exceeded the size limit": "检查响应超过大小限制。",
  "Health endpoint returned HTML rather than a health response": "检查接口返回了网页，请核对服务类型和 API 地址。",
  "HTTP origin responded successfully; application readiness and inference are untested": "HTTP 地址响应成功；尚未检查应用就绪状态。",
  "Liveliness endpoint responded; gateway readiness and inference are untested": "进程存活接口响应成功；尚未检查网关就绪状态。",
  "Health endpoint did not return a supported JSON object": "检查接口未返回支持的 JSON 对象。",
  "Version endpoint did not return the expected schema": "版本接口返回的数据格式不符合预期。",
  "Version endpoint responded; models and inference are untested": "版本接口响应成功；仅确认服务版本信息可读取。",
  "Process health endpoint reported success; inference is untested": "进程健康检查成功。",
  "Health endpoint reported failure": "服务健康接口报告失败。",
  "Health response semantics require installation-specific verification": "无法判断当前安装的检查响应，请核对接口合同。",
  "Health check timed out or was cancelled": "检查超时或已取消，请检查网络和超时设置。",
  "Destination requires private-network consent or is a blocked metadata address": "目标需要私网授权，或属于禁止访问的元数据地址。",
  "Health check timed out": "检查超时，请检查网络和超时设置。",
  "Health endpoint could not be reached": "无法连接检查接口，请检查地址、DNS 和网络。",
  "Check interrupted by Relay restart": "Relay 重启导致检查中断。",
  "Configuration changed before execution": "执行前配置已变更，此次检查已中断。",
  "Check interrupted by Relay shutdown": "Relay 关闭导致检查中断。"
};
export const message = (value?: string) => value ? legacyMessages[value] || value : "";

export const checkClass = (value?: string) => ({
 configuration_invalid: '地址配置无效', connectivity_only: '基础连通', credential_unavailable: '凭据不可用',
 credential_invalid: '凭据格式无效', authentication_failed: '凭据被拒绝', redirect_rejected: '接口重定向',
 endpoint_unsupported: '接口不支持', rate_limited: '触发限流', http_error: '接口错误',
 response_incomplete: '响应不完整', response_too_large: '响应过大', unexpected_html: '返回了网页',
 invalid_response: '响应格式无效', process: '进程健康', health_failed: '健康检查失败',
 unverified_response: '响应含义未确认', timeout: '检查超时', target_blocked: '目标访问受限',
 network_error: '网络连接失败', process_interrupted: '检查中断', configuration_changed: '配置已变更',
 subscription_unavailable: '订阅不可用', subscription_metadata: '订阅信息', model_catalog: '模型目录', provider_reported: '服务商报告',
} as Record<string, string>)[value || ''] || '未分类';
