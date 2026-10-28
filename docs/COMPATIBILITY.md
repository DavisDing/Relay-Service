# NAS 基础监控兼容矩阵

核实日期：2026-10-28。适用范围：当前 Go `server/internal/monitor` 的 M1 基础监控切片。实现和本地 fixture 已验证；未连接现有 NAS，未核实六服务的实际安装版本、镜像 digest、认证代理和网络配置。**AC-60 尚未完成验收**。

## 1. 固定检查入口

管理员只配置 HTTP(S) origin，检查方法固定为 GET；基础检查不接受任意路径，公开状态页可配置同源相对路径，不接受查询或请求体。成功只说明下表所述观察层级，只有当前采集实际含模型目录、部署或模型调用证据的服务，其 `inference_state` 为 `not_tested`；其余为 `not_applicable`，镜像更新状态均为 `update_status_unavailable`。

| 服务 / kind | 固定 GET 路径 | 当前响应判定与层级 | 资料 / 安装验证状态 | 许可结论 |
|---|---|---|---|---|
| LiteLLM / `litellm` | `/health/liveliness` | 2xx + JSON 字符串 `"I'm alive!"`；也兼容下述健康对象。只代表进程存活，不代表数据库、上游或推理就绪 | 官方源码 S1 已核对；NAS 安装版本、路由与实际响应 **未验证** | 未判定 |
| Hermes / `hermes` | `/health` | 官方文档示例 `{"status":"ok"}`；进程存活。不会调用 `/health/detailed`、聊天或工具接口 | 官方文档 S2 已核对；需要实际启用 API server，NAS 安装版本与响应 **未验证** | 未判定 |
| workbuddy2api / `workbuddy2api` | `/health` | 候选检查路径，按健康对象解析；没有据此证明用户所用分支具备该入口 | **NEEDS_CONFIRMATION**：实际上游仓库、分支/版本、无副作用端点与响应合同；本地 fixture 仅证明代码行为 | 未判定 |
| Open WebUI / `openwebui` | `/health` | 官方源码示例 `{"status":true}`；进程存活。未检查其数据库 `/health/db` 或 `/ready` | 官方源码 S3 已核对；NAS 安装版本与响应 **未验证** | 未判定 |
| Ollama / `ollama` | `/api/version` | 2xx + JSON 对象中非空 `version` 字符串（最多 128 字符）；只说明版本端点响应，不证明模型已加载或可推理 | 官方源码 S4 已核对；NAS 安装版本与响应 **未验证** | 未判定 |
| MetaCubeXD / `metacubexd` | `/api/control/health` | 适用于具有控制代理的 all-in-one server；只说明 UI/控制代理响应，不证明 Mihomo 内核运行。当前解析器要求健康 JSON 对象，不会仅凭 200 判成功 | S5 确认该端点与变体差异；纯静态 hosted-panel 容器不具备控制代理。实际变体及健康响应 schema **未验证** | 未判定 |

新增只读采集类型（2026-10-28）：

| 服务 / kind | 固定 GET 路径 | 当前响应判定与层级 | 实际安装验收 |
|---|---|---|---|
| mihomo 订阅 / `mihomo` | `/providers/proxies` | 读取 provider 的 `subscriptionInfo.Upload/Download/Total/Expire`；字段缺失显示未知，Total/Expire 为0显示未提供。到期或已知配额耗尽计入现有故障阈值；补读 `/proxies` 中已有节点延迟/在线记录，超过15分钟或缺失显示未知；不刷新订阅、不主动测速 | 合成响应通过；真实 mihomo 版本/权限/订阅头信息未验收 |
| 中转站 / OpenAI 模型目录 / `openai` | `/v1/models` | 读取 `data[].id`；证据为模型目录可读，每个模型推理状态 `not_tested`。空目录明确显示，不推断模型可用、配额或失败率 | 提供兼容接口的服务可接入；真实中转站尚未指定，未验收 |
| 公开状态页 / `statuspage` | `/api/v2/summary.json` | 读取非分组组件名称及 `operational/degraded_performance/partial_outage/major_outage/under_maintenance`；非正常状态进入故障阈值，未知状态或无组件显示不支持。是服务商报告，不能替代账号下实际推理证据 | 已核对公开 Statuspage JSON 样例；补充 Pipio `/api/uptime/status`、自定义JSON字段和显式HTML规则；动态JS空壳/歧义结构不自动判断 |

同一中转站可分别登记模型目录和公开状态页，二者独立展示。LiteLLM可显式启用部署目录及最多200条最近24小时日志的成功/失败/未知/缓存样本；无日志或权限不伪造零失败。真实推理还涉及单独的授权与预算，当前未实现付费探针，不把目录读取当作模型推理成功。MetaCubeXD 与 mihomo 是两个不同接入目标。

健康对象兼容字段为 `status`、`healthy`、`ok`：布尔 true，或字符串 `ok/healthy/alive/up/ready/success/running` 为进程级成功；布尔 false 或已识别失败词为失败；其余响应为 `unsupported`。该兼容解析不是任意版本的正式协议保证。workbuddy2api 与 MetaCubeXD 仍需安装版本证据才能纳入服务兼容验收。

补充 kind：`http` 对 origin `/` 做 GET，仅表示非 HTML 成功响应的连通；`tcp` 只建立 TCP 连接。纯静态面板可单独登记 TCP 连通，但不能把此证据升级成应用健康、代理内核健康或六服务完整监控验收。

## 2. 判定与安全边界

- 401/403 显示 `unauthorized`；404/405、重定向、HTML、未知 schema、超大响应和被阻止目标显示 `unsupported`。这些结果生成能力受限事件，不计为服务故障阈值。
- 网络错误、超时、非成功 HTTP 状态、已识别的健康失败会计入失败阈值；429 单独标记 `rate_limited`，当前尚无 `Retry-After` 退避。默认连续失败 3 次开故障事件、连续成功 2 次生成恢复事件。事件不可变，通过 `incident_id` 关联。
- 默认间隔 60 秒、超时 5 秒；允许间隔 10～86400 秒、超时 1～30 秒且必须小于间隔。两个全局 worker，每服务至多一个进行中检查；配置代际变化后的旧结果只进入历史。
- 超过两倍检查间隔显示 `freshness=stale`、当前状态未知，保留最近成功及其时间；暂停与移除停止新检查并保留历史。重启中断不归咎为目标故障。
- 私网、回环和共享地址需要显式 `allow_private`；元数据地址、link-local、组播和未指定地址仍被阻止。DNS 全部结果先校验并按已解析地址拨号；不使用环境 HTTP 代理，不跟随重定向。基础健康 HTTP 响应最多 64 KiB；新增订阅/目录/状态页最多 1 MiB，每次快照最多200项，超过明确标注截断。配置超时包括 DNS/连接/读取。
- 检查凭据只支持可选 Bearer token，经服务 ID 绑定的 AES-GCM 加密后持久化，不在服务 API 或诊断摘要回显。不读取 Cookie、订阅会话、Docker socket、其他容器卷或文件。

此模块不调用模型、Agent 工具、推理健康接口或容器管理接口；日志采样不等于完整账本，不采集账户余额、正文、CPU/内存、容器日志或运行 digest。无法从基础检查推断这些能力。已实现管理员确认后的历史/审计分批保留清理；预览使用存储层时间计数，后台按批次渐进执行。尚无完整备份恢复验收、付费探针、通知投递或镜像更新比较。

保存前连通测试：管理员 `POST /api/v1/services/test` 复用同一检查器、CSRF/首次改密和网络限制；一次只允许一个预览，最多10秒，不持久化配置、凭据或历史，也不触发事件。编辑时可以复用原凭据；变更 origin 后必须重输或清除原凭据，过期配置版本拒绝预览。定时检查仍由既有两个 worker 执行。

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
| 管理员确认后的自动保留清理 | 已编码，未真实库/NAS验收 | 预览使用存储层时间计数，后台按批次渐进执行；真实 PostgreSQL、Docker、NAS 仍未执行 |
| 容量保护、Retry-After、运行版本/digest 比较、付费推理与完整产品验收 | 未实现 / 未执行 | 不属于已完成 M1 模块结果；AC-60 及其他整体验收仍待对应证据 |

2026-10-28 补充资料：mihomo 官方 `Meta` 分支 `adapter/provider/provider.go`、`subscription_info.go` 与 `hub/route/provider.go` 已只读核对，`subscriptionInfo` 来源于服务商订阅响应头，不保证每个订阅都有；公开 `https://www.githubstatus.com/api/v2/summary.json` 仅用于核对 Statuspage 标准结构，不作为用户中转站的可用性证据。浮动分支和公开样例不替代 NAS 安装验收。Web 日出日落使用 NOAA fractional-year 太阳位置近似，按浏览器定位在本地计算，不增加定位或天文服务请求。


2026-10-28 本轮接口扩展：`statuspage` 配置 `public_status`，格式 `auto/statuspage/pipio/json/html`；Pipio 站方探针与本地主动推理分开。JSON采用字段路径和值映射，HTML采用显式组件标签/属性/名称/状态和值映射，不执行脚本，不全页搜索“正常”来判定。服务地址仍为origin，单独相对路径禁止跨源、查询凭据和跳转；公开页永不发送保存的Bearer凭据。

LiteLLM可分别启用 `collect_models`、`collect_logs`；wb2api仅允许 `collect_models`，独立日志合同尚未核实，不猜端点。渠道清单只代表网关配置的部署，`api_base`以匿名端点分组摘要显示，无法证明服务商商业身份或全部可售模型。日志为已有调用样本，可能包含外部探针/重试，不当作完整自然请求账本；不采集正文、账户或密钥，不调用 LiteLLM `/health` 推理检查。共享采集器每响应1MiB、整轮最多5MiB/8请求，并共用配置超时、私网授权、DNS/IP校验和禁止重定向。具体合同和真实来源分别见 `nas/PUBLIC-STATUS-SOURCES-2026-10-28.md`、`nas/GATEWAY-SOURCES-2026-10-28.md`、`nas/MIHOMO-SOURCES-2026-10-28.md`。

### 网络代理独立采集（D-20，2026-10-28）

新增工作区基于官方MetaCubeXD v1.273.1（8bbc8f58fef71148a94fb5c0ff808f79b057337d）和mihomo v1.19.33（3f27ef39f8c96bbfbc8b0b9bb10f5c9ef60149d8）HTTP合同。来源核对见nas/NETWORK-PROXY-SOURCES-2026-10-28.md；不代表用户安装的认证接口已经联调。

核心GET固定使用/version、/traffic、/memory、/connections、/proxies、/providers/proxies、/logs；memory忽略每连接首个占位帧，NDJSON持续读取而非ReadAll。MetaCubeXD控制层另配origin及CONTROL_TOKEN，只读/api/control/profiles及固定ID详情。provider为空不代表没有配置订阅；独立控制层未配置/无权限时显示覆盖缺口。控制令牌不从核心secret复用，禁止重定向，沿用私网授权/DNS-IP校验；不读取浏览器秘密、Docker或其他容器文件。

配置详情含完整YAML，后端只内存提取顶层proxies名称和协议，拒绝过深/超量/alias/merge形态；provider引用不会下载，完整目录未知需标partial，不能假报0节点。同名同协议不能证明控制层与核心同一安装/配置，配置目录不继承核心健康；只有核心provider归属可直接用于该provider的节点证据。要使所有未激活订阅持续有新延迟需另行授权加载provider或隔离检测，本轮没有执行。

原Web用量为浏览器IndexedDB、概览/日志为内存缓冲，Relay自行积累历史。原始日志正文不保存/回传，仅提供级别、接收时间和分类摘要；连接为当前采样，管理员用量分组为连续快照差分，均不是完整访问账本。节点24小时生命条按实际源检测时间落位，采集中断/缺失保留未知。新代码仅静态核对并格式化；按用户要求测试、构建及真实NAS联调交Hermes，均未执行。


### 大模型工作区（2026-10-29）

工作区复用既有LiteLLM目录/部署与可选有限日志窗口、OpenAI兼容目录、workbuddy2api目录、服务商公开状态，不增加上游接口或推理请求。wb2api独立调用日志/渠道合同未适配，不能继承LiteLLM的日志权限和覆盖。目录/日志来源缺失、鉴权失败、禁用和采集过期分别展示；公开状态仅为站方报告，不证明当前凭据实际推理成功。服务内别名与部署可展开，跨实例不自动归并。

日志按当前快照展示最多既有200条聚合样本，页面翻页不会扩展上游日志覆盖；不累加重叠窗口，不宣称完整24小时失败率或当前实时健康。后续完整账本需持久增量采集与request_id去重；主动模型探针、能力和质量评测尚未实现。新增代码仅静态审查/格式检查，测试、构建、浏览器与真实NAS验收未执行，待Hermes核实，见nas/MODELS-WORKSPACE-HERMES-HANDOFF-2026-10-29.md。
