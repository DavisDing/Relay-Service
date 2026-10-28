> 2026-10-28 D-19 更新：默认存储已改为 SQLite，PostgreSQL 可选。下文较早 PostgreSQL-only/双镜像部署描述由 [存储与迁移](../docs/nas/STORAGE.md) 覆盖。业务 Store/Tx、API及数据所有权不变；SQLite与PG行为、迁移与NAS验收分别记录。

# NAS M1 实现合同

日期：2026-10-26。模块 `relayservice`，Go代码在 `server/`，管理页面在 `web/`，编译后嵌入 `server/internal/webui/dist`。本合同描述当前基础监控切片；不代表完整首期、真实数据库或六服务兼容验收。证据见 `docs/nas/VALIDATION.md`、`docs/COMPATIBILITY.md`。

## HTTP/API

健康入口：`GET /health/live` 仅进程；`GET /health/ready` 检查Relay自有数据库与schema，不调用上游。

成功响应：

```json
{"api_version":"v1","schema_version":1,"relay_id":"opaque-id","data_version":"123","generated_at":"2026-10-26T00:00:00Z","data":{}}
```

错误响应：

```json
{"error":{"code":"conflict","message":"配置已变更，请刷新后重试。"}}
```

`data_version` 是自有库业务写事务提交后的单调版本；不因纯GET/空事务递增。响应读取和版本查询尚不共用同一数据库快照，不能将其作为跨接口一致快照、ETag或“无需再取数据”的缓存保证；客户端应按接口正常刷新。`schema_version` 与 `api_version` 是合同版本，非数据版本。时间均使用RFC3339 UTC，游标与ID不依赖本地时区。

| 方法与路径 | 访问 | 用途/请求 |
|---|---|---|
| GET `/api/v1/info` | 公开 | 最小Relay ID、版本、capabilities、initialized |
| POST `/api/v1/auth/bootstrap` | 公开，配置origin | `{token,username,password}`，仅未初始化库可成功；密码至少12字符 |
| POST `/api/v1/auth/password` | 受限管理员会话，Origin/CSRF | `{username,password}`，仅首次强制改密；成功使全部临时会话失效，重新登录 |
| POST `/api/v1/auth/login` | 公开，配置origin | `{username,password}`；只在Cookie设置会话，响应不含令牌 |
| POST `/api/v1/auth/logout` | 管理员 | 撤销当前会话并清Cookie |
| GET `/api/v1/auth/me` | 管理员/设备 | 当前principal `{id,role}` |
| POST `/api/v1/devices/pairings` | 管理员 | 生成5分钟单次配对码 |
| POST `/api/v1/devices/pair` | 公开 | `{code,name}`；首次返回设备令牌和expires_at，应由客户端安全保存 |
| GET `/api/v1/devices` | 管理员 | 设备列表，不含token，包含授权人issued_by、创建/到期/撤销时间；旧授权来源为legacy_unknown |
| DELETE `/api/v1/devices/{id}` | 管理员 | 撤销设备，新请求立即拒绝 |
| GET `/api/v1/services` | 管理员/设备 | 服务与当前观测；设备投影省略base_url/凭据和可写配置 |
| POST `/api/v1/services` | 管理员 | 新增ServiceInput |
| POST `/api/v1/services/test` | 管理员，Origin/CSRF | ServiceInput；同步临时检查，最多10秒/一个并发；不保存服务/凭据/历史/事件，编辑要求当前generation和凭据origin绑定 |
| GET `/api/v1/services/{id}` | 管理员/设备 | 服务详情 |
| PUT `/api/v1/services/{id}` | 管理员 | 完整ServiceInput，要求当前generation，成功递增 |
| DELETE `/api/v1/services/{id}?generation=N` | 管理员 | 软移除接入，历史保留 |
| POST `/api/v1/services/{id}/checks` | 管理员 | `{operation_id,generation}`，202返回已持久排队的check；历史保留窗口内同操作号重试返回同记录，记录到期后可作为新操作排队 |
| GET `/api/v1/services/{id}/checks` | 管理员/设备 | `limit=1..500&cursor=...`，历史从新到旧，响应`{items}` |
| GET `/api/v1/services/{id}/checks/{check_id}` | 管理员/设备 | 查询单个持久记录，不发起新检查 |
| GET `/api/v1/alerts` | 管理员/设备 | 从cursor之后按升序读取，返回`items/ack_cursor/next_cursor/has_more` |
| POST `/api/v1/alerts/ack` | 管理员/设备 | `{cursor,cursors?}`；仅当前principal自己的单调已读游标；批量必须列出从下一未读事件开始连续展示的真实游标，最多500条。仅cursor只确认下一条或幂等重试，不能跳页确认 |
| POST `/api/v1/services/{id}/silence` | 管理员 | `{until,reason}`；限制期限且不停止检查/删除事件 |
| GET `/api/v1/diagnostic` | 管理员 | 脱敏摘要，不自动上传，不包含上游秘密 |
| GET `/api/v1/retention` | 管理员 | 当前`history_days/audit_days/generation`；0为永久保留 |
| POST `/api/v1/retention/preview` | 管理员 | `{history_days,audit_days,generation}`；0..3650天，返回计数、截止时间、五分钟有效的单次预览ID；不删除数据 |
| PUT `/api/v1/retention` | 管理员 | `{preview_id}`；核对代际/有效期，保存预览中的策略并审计，随后周期分批清理 |

服务输入字段：`id? name kind base_url ui_url? group? enabled interval_seconds timeout_seconds failure_threshold recovery_threshold allow_private token? clear_token? generation public_status? collect_models? collect_logs?`。新建generation=0，更新必须使用读到的当前值。kind为 `litellm/hermes/workbuddy2api/openwebui/ollama/metacubexd/mihomo/openai/statuspage/http/tcp`。基础地址只能是HTTP(S) origin，TCP kind也从该origin解析主机/端口；不允许配置HTTP方法/请求体；公开状态页可在独立配置中指定受限相对GET路径。参数范围和证据见COMPATIBILITY。`token`为空保留已有秘密，`clear_token=true`显式清除，不允许同时提供新秘密；秘密从不响应回显；目标地址改变时需重新提供或显式清除原凭据。

Cookie写操作校验origin（默认匹配请求Host/协议，或显式固定origin）与双提交 `relay_csrf` Cookie/`X-Relay-CSRF` header。bootstrap/login在未有会话时仅校验origin；Cookie会话采用HttpOnly、SameSite=Strict和匹配部署HTTPS的Secure标志。纯Bearer设备请求不依赖浏览器origin，但不得写管理员资源；设备仅允许读取及更新自己的已读位置。服务端不信任客户端forwarded host/proto来推导允许origin。

JSON请求最大64KiB，未知字段/多JSON/非对象拒绝。常见错误为400输入、401无效或过期凭据、403权限/CSRF/origin、404不存在、409generation/暂停冲突、413过大、415媒体类型、429认证尝试限制、503请求截止、500内部错误。登录连续8次失败锁5分钟；配对失败也有限制。错误文本不输出数据库URL/上游响应/秘密。API与监控原因使用中文，客户端按稳定code做本地化和安全兜底，不把任意上游/网络原文回显给用户。

认证限流的429响应带`Retry-After: 300`；这不代表已实现上游HTTP限流后的调度退避。配对码为8随机字节的Base32无填充编码（13字符，64 bit），兼容仍有效的旧码。设备名拒绝非法UTF-8、控制字符及双向控制符；签发、成功配对、撤销与脱敏审计在同一事务提交，审计不包含码、令牌或秘密。

## 观测与事件语义

每服务当前结果、最近成功和最近检查时间分开。严格超过两倍检查间隔为stale，当前状态unknown；恰好两倍仍为fresh。例如间隔86400秒时超过48小时才过期；此规则已经在DESIGN 3.4定义，不等于失败检测SLA。缺失、暂停、认证失败、不支持和中断各自保留原因。仅当前结果含实际模型目录、部署或模型调用证据时推理状态为not_tested，其余为not_applicable，镜像更新状态不可得；基础成功不能代替模型/数据库/容器健康。

检查先持久化queued，再由两个全局worker领取running，网络IO不持有数据库事务锁；每服务至多一个进行中，旧generation结果只入历史。关机/重启的running转interrupted，不生成目标故障。手动操作号幂等不等于外部HTTP exactly-once，不承诺断链后无重复GET。

检查可附带 `details={source,count,truncated,subscriptions?,models?,components?}`：mihomo 为 `/providers/proxies` 的名称和 Upload/Download/Total/Expire 投影（统一小写JSON字段，缺少info/零总额/零到期不推断无限或健康）；openai 为 `/v1/models` 的模型ID和 `not_tested`；statuspage 为 `/api/v2/summary.json` 的非分组组件名称及已知状态。响应最大1MiB，持久快照最多200项，超过标记truncated。字段为可选新增，不需要SQL结构迁移；旧记录仍可读取，不改写原历史消息，Web映射已知旧英文消息。只有实际获取到模型证据的服务标记not_tested；非模型服务或尚无模型证据时为not_applicable。正常检查不足1ms时duration_ms明确返回0，预览也不省略。

中断记录的duration_ms为StartedAt到中断完成/恢复时刻的非负墙钟耗时，重启恢复会包含停机时间；缺失开始时间或时钟回退时0表示未测得，不能解读为目标零延迟。

故障/恢复与能力受限事件不可变，incident_id关联；故障事件本身不会因后续恢复改写resolved_at。当前活动故障依据service.open_incident/open_restriction，不能按历史事件缺少resolved_at统计。静默只影响通知资格，检测与历史继续；当前尚无真实客户端通知投递。

按REQUIREMENT D-01，已配对设备默认读取全局脱敏服务与告警；不做租户/服务级授权。事件已读按认证principal独立保存，既不删除原始事件也不修改其他设备；连续游标校验证明提交的序列有效，不证明人实际阅读。未授权、过期或撤销设备不能读取；设备不能修改监控配置、静默、其他设备或保留策略。

检查历史和审计默认永久保留，30天检查历史仍是设计建议，不自动作为已确认默认。诊断页提供策略读取、影响预览、显式确认。启用后每30秒分批扫描最多200条历史、操作句柄和审计；启动只恢复调度，不阻塞在清理扫描上；进度与删除同事务提交、重启可恢复，渐进清理不保证到期立刻删除。预览使用存储层时间条件计数，不逐页反序列化整桶。只清理已完成且无待执行队列引用的历史及关联索引；操作幂等窗口随历史保留结束。当前/最近成功快照、排队/执行中检查和告警始终保留。告警的evidence_state区分available/expired/unavailable；清理明细后仍有摘要，关闭策略后无法证明原过期原因则标unavailable。审计保留期限同样必须预览确认，没有新增已确认默认；数据库容量/磁盘风险告警尚未实现。

## 存储与部署

`storage.Store`提供View（只读快照）、Update（原子短事务）、Ping、Close，内存Store仅用于测试。bucket归对应模块所有；扫描使用排他字典游标，默认100/最大500，reverse取小于After的键。历史与序号使用可排序键，分页遍历不依赖全量载入。

PostgreSQL初版为relay_records JSONB记录与relay_sequences普通计数表；Update共享短advisory writer锁实现跨进程读改写序列化，禁止在事务中做外部网络调用。历史/审计预览使用由JSON派生的retention_at列及普通时间索引，数据库触发器保证INSERT/UPDATE时同步。当前无存量数据，首装基线001/002顺序建表，002直接采用普通时间列索引；不包含旧版002/003兼容、跳过或回填。默认仅RELAY_DATABASE_URL，同一具有public USAGE/CREATE的专用账号读写并初始化/升级；新建表及函数归该账号所有。已移除第二迁移URL及独立迁移账号配置，不保留兼容分支；不修改原对象所有者。启动检查ledger、必要表列和保留策略触发器/索引，缺失或有待执行版本时初始化，关闭初始化池并经运行连接复验后才启动HTTP/调度。完整结构跳过；未知/不连续历史、无账本但已有业务表、账本完整而对象损坏或权限错误均拒绝启动。migrate命令保留作维护入口，SQLite停机迁移也默认复用主连接；初始化与普通进程共享数据库leader互斥。只保护同一数据库，不解决独立克隆库双活。

源码Compose默认使用GHCR latest自行测试渠道，版本发布包锁定digest；发布/容器证据见nas/VALIDATION.md，不以本次本地测试代替新提交的远端CI。真实库/SQL授权/迁移、UGOS、备份恢复和客户端验收未执行。容量限制/磁盘风险告警、上游Retry-After退避、完整请求账本/余额/未接入来源目录整合、付费探针、模型/路由管理、正文、镜像更新、灾备授权代际和原生客户端仍为后续需求。

## 源码检查与完整 Web 构建

`server/` 默认 Go 构建不依赖前端产物，Web 请求返回 503 并给出构建提示；可直接运行后端测试/vet。完整 Web 构建必须先运行 `web/` 的 `pnpm build`，再使用 `-tags webassets`；Dockerfile 与 CI 的嵌入验证均明确选择此标签。发布路径缺少真实 `dist` 时编译失败，已有真实 HTML/哈希资源/缓存策略验证保留在该标签下，不能用纯后端测试替代。

Web 总览驾驶舱的状态分布与计数来自已同步服务，过期/未观测不计为成功；需要关注区包含未恢复事件、失败和待观测服务。分组概况与筛选仅改变视图，不改变检查计划。告警仅有当前加载页时不宣称覆盖全部最新事件；全量 FR-W02 八工作区、模型/额度、日志、版本/恢复及容量来源仍未完成。

默认 NAS 部署按 D-18 不要求 public URL；Origin 与请求 Host/协议一致，固定反代 URL 可选，不信任代理头。自动长期部署文件包含单一数据库连接和加密密钥，临时密码另存；数据库先生成并持久化，Relay 只读加载长期配置；临时密码转为可删除的独立文件，旧文件由包装脚本迁移。首次 admin 会话返回 `must_change_password:true`，只放行 me/password/logout，业务 API 返回 403；改密后凭据版本失配的旧会话返回 401。新增 JSON 字段默认零值兼容旧账号，不变更 SQL schema。

默认 NAS Compose 仅保留镜像、端口、卷与就绪依赖；PostgreSQL 配套镜像内置初始化脚本，Relay 镜像内置配置路径/健康检查。默认 Compose 网络由项目创建，既有网络和环境覆盖示例仅按需合并。版本部署包绑定两个镜像摘要。

2026-10-28 审计修复：首次改密事务提交后删除临时密码文件，清理失败告警/重启重试，不回滚已提交账号；启动后清空 cfg.InitialPassword 与进程环境值，无法保证 GC 旧副本物理擦除。待改密密码失配/缺失仅告警、保留原账号和受限会话规则，不再阻止 HTTP 启动。部署文件允许白名单键子集，由最终配置验证必需项。未认证登录页不再给出部署凭据获取路径。


### 只读监控扩展（2026-10-28）

服务输入/响应可选增加 `public_status`、`collect_models`、`collect_logs`（默认false，旧配置行为保留）。`public_status`仅statuspage支持，字段包括format/resource_path/items_path/name_field/status_field/healthy_values/unhealthy_values和html_item_tag/html_item_attribute/html_item_value/html_name_attribute/html_status_attribute。只允许同源相对路径，不允许查询、fragment或目录跳转。`format=pipio`无路径时默认`/api/uptime/status`；普通默认`/api/v2/summary.json`。公开状态配置拒绝新凭据，读取时忽略历史残留凭据。规则变化增加代际并清除当前状态/计数，不继承旧目标绿色；在途旧代际沿用历史隔离。

`collect_models`仅LiteLLM/workbuddy2api可用；`collect_logs`仅LiteLLM可用。预览复用同一配置，最多10秒、零持久化。采集失败与进程状态独立，`details.gateway`保存各来源状态、模型/部署清单和聚合调用样本，不把它们写成`inference_state=healthy`。`details.mihomo`保存订阅已用/剩余和已有节点质量；`details.public_status`保存服务商组件/站方探针报告、源时间可得性和心跳。新增JSON字段不需要SQL结构迁移，旧明细兼容显示，保留政策沿用检查历史；各类清单最多200项且完整响应内尾项异常仍汇总。

调用样本窗口最近24小时，最多两页×100，使用确切model_id关联渠道部署；状态空/未知单列，缓存不证明上游成功，重复/丢弃/满页边界/部分失败标为不完整。统计不能跨重叠快照相加。Pipio可用率窗口和心跳时间未提供，不标成24小时。mihomo节点历史15分钟内且可判断才计近期正常/异常，空alive默认值不证明在线；0延迟按当前官方实现失败哨兵处理。所有投影统一检查当前凭据反射，错误只返回稳定中文分类，不保存上游原始错误体。

### 网络代理工作区（D-20，2026-10-28）

`GET /api/v1/network/{service_id}?section=overview|usage|subscriptions|nodes|connections|logs`读取共享后台快照，不直接代理任意上游请求。只支持有效mihomo服务；分页默认50、最大200，数字cursor为局部偏移。筛选为q/subscription/state，节点历史用node_id；hours仅1/24/168。用量完整窗口按时间聚合至最多180点，不用分页丢弃后半段。节点页默认展示24小时样本，按源checked_at去重/落位；采集时间at不冒充检测时间。

管理员可GET/PUT同路径`/settings`，POST`/subscriptions/import`；写入沿用首次改密、CSRF、origin、generation和同事务脱敏审计。控制地址仅origin，控制token独立加密且不回显；换址有旧token时必须新输或显式清除。元数据导入最多100订阅/总5000节点，整个请求沿用64KiB限制；网页文件上限60KiB以留出包装空间，拒绝原始配置/链接/凭据字段，导入不创建检测证据。

设备只可读取概览、用量、订阅与节点；连接、日志直接403。设备响应设置仅enabled/generation，移除控制地址、凭据存在标志、导入设置和usage_groups。连接分组含客户端、规则、完整代理链，仅管理员查看；每个连接ID连续两次有效快照间隔不超过5秒且计数递增才计增量，首见/断流/短连接不补算。各维度独立描述同一采样，不能相加或与核心traffic累加。按小时保存最多每类100个标签加其他；窗口内先合并已保留标签再显示每类最多20项及其他。API给出usage_group_start/end，分组覆盖起点向整小时取整，与核心精确窗口可相差不足1小时。

后台由Engine.Run生命周期启动，同实例共享三路HTTP流和轮询，关闭浏览器仍采集；暂停/移除/换代取消旧worker，旧版本不能提交新快照。常规页面轮询5秒，目录/历史15秒；后端流每秒、连接约2秒、节点约60秒、控制profiles整轮完成后约5分钟更新，详情每轮最多4项分批推进。上游每响应1MiB、每NDJSON帧64KiB；总节点5000，来源限制与截断写入coverage，不能据截断目录宣称完整。

新增network.settings/snapshot/usage/usage_groups/nodes/logs/expiry记录复用通用Store；没有SQL schema迁移。流量/节点/连接分组7天分桶保存和渐进过期，未接入之前不补造。日志默认仅有界实时缓冲，显式开启持久后默认24小时/20MiB，上限配置1～168小时/1～100MiB，同时有记录条数限制；关闭持久不删除已保存记录，到期回收。仅保存日志等级/接收时间/安全分类摘要，不保存原始访问正文；无完整连接账本、无原Web浏览器历史自动迁移。源字段缺失、未加载目录、无可信控制层与核心绑定、过期、权限不足、断流均分开呈现。

代码实现不等于验收通过。用户本轮要求跳过测试，测试、构建、NAS认证联调和部署均未执行，交接见docs/nas/NETWORK-PROXY-HERMES-HANDOFF-2026-10-28.md。


### 大模型只读工作区（D-21，2026-10-29）

新增已认证的`GET /api/v1/models`，query为`section=overview|models|channels|traffic|status|probes`、`service`（可选实例ID）、`q`、`state`、`cursor`和`limit`（默认50，1～200）。返回sources、summary、coverage、当前页models/channels/traffic/public_status、probes及total/next_cursor/has_more；总览计数以已保留投影为准，上游报告目录总数另在source中显示，截断不当作不存在。当前游标为快照内位置，下一轮目录变化可能改变位置，不保证跨轮历史一致性。

API只读既有Store中的当前同代检查，拒用旧代或未来完成时间；刷新不触发采集/推理。模型ID包含实例+别名，渠道部署身份包含实例+部署ID/别名/上游模型/协议/匿名端点；调用关联需部署唯一且别名匹配，无法关联保留未知。相同实例别名聚合现有单窗口样本，不累加多轮检查；缓存不计上游成功，未知不计已知结果分母。历史成功/失败/混合样本不表示当前可用或最近一次请求结果。设备服务摘要增加只读generation以校验作用域；设备与管理员均可读安全投影，无上游URL、凭据、正文或原始日志；采集配置仍仅管理员通过原服务编辑器修改。

Web新增同级“大模型”入口及六页签；服务总览仅保留摘要和跳转，历史弹窗保留原检查证据。目录、渠道、调用和公开状态分别呈现来源/权限/时效；公开组件不自动映射到同名模型。模型探针页明确未实现，无假执行按钮。无新依赖、bucket或数据库结构迁移；完整增量请求账本、费用/延迟/错误明细和主动可用性/能力/质量探针仍未实现。本轮测试、构建及NAS联调按用户要求交Hermes，交接见docs/nas/MODELS-WORKSPACE-HERMES-HANDOFF-2026-10-29.md。
