# 网关目录、部署与调用证据采集

本次补充的是只读、有界的监控快照。它不发模型推理请求，不启用 LiteLLM 内部健康调用，不改模型、路由、账户或日志配置。管理页面可分别启用模型读取和日志读取；服务进程状态与这两类权限独立。

## 实际读取

| 来源 | 接口 | 保存的投影 | 不能据此判断 |
|---|---|---|---|
| OpenAI 兼容目录，包括提供该合同的 wb2api | `GET /v1/models` | 模型 ID，最多200项及总数 | 当前 Key 的实际推理成功、上下文/工具能力、上游实时健康 |
| LiteLLM 配置部署 | `GET /model/info` | 公开别名、部署 ID、上游模型名、协议标签、端点分组的不可逆短摘要 | 商业渠道身份、同名模型真实身份、供应商可售全量清单 |
| LiteLLM 已有调用日志 | `GET /spend/logs/v2` | 最近24小时按模型别名和确切部署 ID 聚合的成功/失败/未知/缓存次数、最新结果时间 | 当前实时可用性、完整用户逻辑请求数、重试去重后的调用次数、完整账单、绕过网关的直连流量 |

目录读取最多2个请求；日志读取最多2页、每页100条。每个响应至多1MiB。调用日志请求固定按 `startTime` 降序，传入UTC的起止时间，并设置 `exclude_internal_health_checks=true`。不信任上游 `total_pages` 来宣称遍历完整：到达满页边界、计数截断、重复请求ID、跳过无效记录或第二页读取失败均标记采样/不完整。第二页失败保留第一页有效投影并显示 `partial`，不伪造零失败。

`request_id` 仅在本次采集内存中去重，不下发或持久化。每次定时检查重新生成窗口快照，历史保存的是快照，不能把多个重叠窗口相加。当前没有持久化增量请求账本或跨重启游标，不能据此声称完成 DESIGN 10.3 的完整统计账本。

明确 `status=success/failure` 才计对应结果；旧数据的空状态、其他状态计未知。缓存命中单列，不证明上游发生调用。日志缺少有效请求 ID、模型名、开始/结束时间、时间在窗口外或结束时间晚于本次窗口时跳过，并显示不完整。LiteLLM 的公开模型目录、权限过滤、通配符/别名和部署列表不一定一一对应；缺少 `model_id` 时只显示未归属调用，不根据相似模型名猜渠道。

端点分组以 `api_base` 的 origin 摘要区分，不暴露URL、路径、查询参数、用户名或密码。相同 origin 下不同路径的部署仍由稳定部署 ID 区分。协议标签（如 `openai`）不是 Pipio/wb2api 等商业身份；上游不提供可验证映射时显示未识别，不把兼容协议自动当渠道名称。当前部署列表截断时，一部分历史调用可能没有对应端点分组。

## 隐私与权限

仅投影上述字段；不保存原始响应、API Key/哈希、用户/团队/组织信息、请求提示词、回答正文、metadata、日志错误正文或请求URL。日志接口本身可能返回这些字段，Relay 在接收后立即丢弃，服务端与Web不暴露它们。读取凭据由既有监控凭据机制处理，不进入投影。

`catalog_state`、`deployments_state`、`logs_state` 分别记录 `disabled/available/unauthorized/unsupported/unavailable/invalid/partial`。未启用数据库、无日志权限、旧版本不提供接口都不能显示为“零请求且健康”，也不让基础进程监控失败。明文上游异常信息不进入 `issues`，只保存来源固定路径与稳定错误分类。

## 接口证据和验证边界

2026-10-28读取并核对 LiteLLM 官方仓库提交 `295d79fc7d9f935165e07c66e150a8579d511d4c`：

- `litellm/proxy/proxy_server.py` 中 `model_info_v1` 同时注册 `/model/info` 与 `/v1/model/info`，返回 `data` 下的 `model_name/litellm_params/model_info`。源码SHA-256：`c9e8a0f0d7dbc77dfea3c6c58ceff5517a4cc27dc1943a639785d437bffabfee`。
- `litellm/proxy/spend_tracking/spend_management_endpoints.py` 中 `ui_view_spend_logs` 注册 `/spend/logs/v2`，提供起止时间、分页、排序及排除内部健康请求选项，响应为 `data/total/page/page_size/total_pages/total_is_capped`。源码SHA-256：`56f6f3fec181993a98ad1eaf821e9b50daefb3973c07278fb461d1c35581ecfc`。
- 官方源码：<https://github.com/BerriAI/litellm/tree/295d79fc7d9f935165e07c66e150a8579d511d4c>。该提交证明本次实现所依据的字段合同，不代表用户NAS当前镜像已运行该提交。

仓库现有文档只有 `workbuddy2api:latest` 名称、端口7863、42条 LiteLLM 别名和环境引用，没有可与实际安装唯一对应的仓库/发行身份。此次只接入标准目录读取；没有将名称相似的公开分支假定为用户部署，也没有猜测独立日志/额度端点。wb2api 经 LiteLLM 的调用可按LiteLLM确切部署ID查看；绕过LiteLLM的调用不可见，独立日志/额度合同仍为 NEEDS_CONFIRMATION。

`gateway_test.go` 验证目录与权限独立、同名模型不同部署不合并、未知状态/缓存不当成功、分页上限与去重、部分失败、坏响应/超限、窗口边界、投影脱敏与截断。合成响应验证不等于真实 LiteLLM/wb2api、NAS 或真实收费推理联调。此次未执行真实模型调用。
