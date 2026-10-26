# NAS 基础监控兼容矩阵

核实日期：2026-10-26。适用范围：当前 Go `server/internal/monitor` 的 M1 基础监控切片。实现和本地 fixture 已验证；未连接现有 NAS，未核实六服务的实际安装版本、镜像 digest、认证代理和网络配置。**AC-60 尚未完成验收**。

## 1. 固定检查入口

管理员只配置 HTTP(S) origin，检查方法固定为 GET，不接受任意路径、查询或请求体。成功只说明下表所述观察层级，所有服务的 `inference_state` 均为 `not_tested`，镜像更新状态均为 `update_status_unavailable`。

| 服务 / kind | 固定 GET 路径 | 当前响应判定与层级 | 资料 / 安装验证状态 | 许可结论 |
|---|---|---|---|---|
| LiteLLM / `litellm` | `/health/liveliness` | 2xx + JSON 字符串 `"I'm alive!"`；也兼容下述健康对象。只代表进程存活，不代表数据库、上游或推理就绪 | 官方源码 S1 已核对；NAS 安装版本、路由与实际响应 **未验证** | 未判定 |
| Hermes / `hermes` | `/health` | 官方文档示例 `{"status":"ok"}`；进程存活。不会调用 `/health/detailed`、聊天或工具接口 | 官方文档 S2 已核对；需要实际启用 API server，NAS 安装版本与响应 **未验证** | 未判定 |
| workbuddy2api / `workbuddy2api` | `/health` | 候选检查路径，按健康对象解析；没有据此证明用户所用分支具备该入口 | **NEEDS_CONFIRMATION**：实际上游仓库、分支/版本、无副作用端点与响应合同；本地 fixture 仅证明代码行为 | 未判定 |
| Open WebUI / `openwebui` | `/health` | 官方源码示例 `{"status":true}`；进程存活。未检查其数据库 `/health/db` 或 `/ready` | 官方源码 S3 已核对；NAS 安装版本与响应 **未验证** | 未判定 |
| Ollama / `ollama` | `/api/version` | 2xx + JSON 对象中非空 `version` 字符串（最多 128 字符）；只说明版本端点响应，不证明模型已加载或可推理 | 官方源码 S4 已核对；NAS 安装版本与响应 **未验证** | 未判定 |
| MetaCubeXD / `metacubexd` | `/api/control/health` | 适用于具有控制代理的 all-in-one server；只说明 UI/控制代理响应，不证明 Mihomo 内核运行。当前解析器要求健康 JSON 对象，不会仅凭 200 判成功 | S5 确认该端点与变体差异；纯静态 hosted-panel 容器不具备控制代理。实际变体及健康响应 schema **未验证** | 未判定 |

健康对象兼容字段为 `status`、`healthy`、`ok`：布尔 true，或字符串 `ok/healthy/alive/up/ready/success/running` 为进程级成功；布尔 false 或已识别失败词为失败；其余响应为 `unsupported`。该兼容解析不是任意版本的正式协议保证。workbuddy2api 与 MetaCubeXD 仍需安装版本证据才能纳入服务兼容验收。

补充 kind：`http` 对 origin `/` 做 GET，仅表示非 HTML 成功响应的连通；`tcp` 只建立 TCP 连接。纯静态面板可单独登记 TCP 连通，但不能把此证据升级成应用健康、代理内核健康或六服务完整监控验收。

## 2. 判定与安全边界

- 401/403 显示 `unauthorized`；404/405、重定向、HTML、未知 schema、超大响应和被阻止目标显示 `unsupported`。这些结果生成能力受限事件，不计为服务故障阈值。
- 网络错误、超时、非成功 HTTP 状态、已识别的健康失败会计入失败阈值；429 单独标记 `rate_limited`，当前尚无 `Retry-After` 退避。默认连续失败 3 次开故障事件、连续成功 2 次生成恢复事件。事件不可变，通过 `incident_id` 关联。
- 默认间隔 60 秒、超时 5 秒；允许间隔 10～86400 秒、超时 1～30 秒且必须小于间隔。两个全局 worker，每服务至多一个进行中检查；配置代际变化后的旧结果只进入历史。
- 超过两倍检查间隔显示 `freshness=stale`、当前状态未知，保留最近成功及其时间；暂停与移除停止新检查并保留历史。重启中断不归咎为目标故障。
- 私网、回环和共享地址需要显式 `allow_private`；元数据地址、link-local、组播和未指定地址仍被阻止。DNS 全部结果先校验并按已解析地址拨号；不使用环境 HTTP 代理，不跟随重定向。HTTP 响应最多 64 KiB，配置超时包括 DNS/连接/读取。
- 检查凭据只支持可选 Bearer token，经服务 ID 绑定的 AES-GCM 加密后持久化，不在服务 API 或诊断摘要回显。不读取 Cookie、订阅会话、Docker socket、其他容器卷或文件。

此模块不调用模型、Agent 工具、推理健康接口或容器管理接口；不采集账本、余额、正文、CPU/内存、容器日志或运行 digest。无法从基础检查推断这些能力。尚无自动历史保留清理、完整备份恢复验收、付费探针、通知投递或镜像更新比较。

## 3. 已核对的官方资料

以下均于 2026-10-26 只读核对，浮动 `main` 不代表 NAS 所安装版本，未据此固定安装版本或得出许可证合规结论。

| 编号 | 资料与本次用途 |
|---|---|
| S1 | [LiteLLM 官方健康端点源码](https://raw.githubusercontent.com/BerriAI/litellm/main/litellm/proxy/health_endpoints/_health_endpoints.py)：`/health/liveliness` 返回进程存活 JSON 字符串；与可能调用模型的 `/health` 区分 |
| S2 | [Hermes 官方 API server 文档](https://hermes-agent.nousresearch.com/docs/user-guide/features/api-server/#get-health)：`/health` 是廉价存活检查，返回 `status=ok`；详细就绪检查另有入口 |
| S3 | [Open WebUI 官方服务入口源码](https://raw.githubusercontent.com/open-webui/open-webui/main/backend/open_webui/main.py)：`/health` 返回 `status=true`，数据库及就绪检查为独立路由 |
| S4 | [Ollama 官方路由源码](https://raw.githubusercontent.com/ollama/ollama/main/server/routes.go)：GET `/api/version` 返回版本对象；生成、聊天和模型管理另有路由 |
| S5 | [MetaCubeXD 官方 README](https://raw.githubusercontent.com/MetaCubeX/metacubexd/main/README.md)：standalone hosted-panel 只托管 UI；all-in-one server 有 `/api/control/health`，其成功不保证内核运行 |

workbuddy2api 暂无可与用户实际部署对应的唯一上游证据，不能以名称相近的项目代替。各服务的许可证、所用发行物及再分发条款均为后续核对项；本项目仅新增 HTTP/TCP 检查代码，没有复制这些服务的业务实现。

## 4. 监控模块验证记录

2026-10-26，本机 Go 1.27.3：`cd server && go test -race ./internal/monitor && go vet ./internal/monitor` 通过。本地回环 HTTP fixture 测试需要执行环境允许监听临时端口；本记录不是容器或 NAS 运行证明。

| 场景 | 结果 | 证据与限制 |
|---|---|---|
| 六种 kind 的固定 GET 路径、凭据头与正常响应 | 通过 | `monitor_test.go` 合成 HTTP fixture；LiteLLM 覆盖官方 JSON 字符串，其他服务响应为合成数据 |
| 凭据单独持久化、API JSON 不回显 | 通过 | 内存 Store 序列化往返；真实 PostgreSQL/恢复链由集成验证另报 |
| 401/403、重定向/404 分类；受限结果不触发故障事件 | 通过 | 合成 HTTP 响应与状态机测试；不是上游认证接口实测 |
| 私网授权与元数据/IP 拒绝规则、origin 拒绝推理路径/秘密 | 通过 | `state_test.go` 规则测试；没有云环境或实际 DNS rebinding 攻击演练 |
| 故障/恢复阈值、稳定 incident 关联、过期状态与最近成功保留 | 通过 | 确定性内存状态机测试 |
| 手动检查幂等、历史游标、旧代际结果隔离、跨代际单服务并发限制 | 通过 | 内存事务与 claim/finish 测试；跨进程数据库单活由集成验证另报 |
| 静默不删除事件、到期解除、设备独立已读及游标单调 | 通过 | 内存状态机测试；无 Mac/Android 通知实际投递验收 |
| 重启时 running 检查转 interrupted，避免假故障 | 通过 | 持久任务恢复函数测试；进程杀断/数据库故障注入另由集成验证记录 |
| 真实六服务、NAS 网络/TLS/权限/版本与资源占用 | 未执行 | 当前没有真实 NAS 连接和安装版本证据 |
| 非法 schema/HTML/64 KiB 上限、DNS 混合结果、慢读/取消等全部网络故障组合 | 未完整覆盖 | 已有实现保护；不能把代码存在当作所有边界通过 |
| 自动保留清理、Retry-After、运行版本/digest 比较、付费推理与完整产品验收 | 未实现 / 未执行 | 不属于已完成 M1 模块结果；AC-60 及其他整体验收仍待对应证据 |
