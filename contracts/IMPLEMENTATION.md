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
{"error":{"code":"conflict","message":"Resource state changed; refresh and try again"}}
```

`data_version` 是自有库业务写事务提交后的单调版本；不因纯GET/空事务递增。响应读取和版本查询尚不共用同一数据库快照，不能将其作为跨接口一致快照、ETag或“无需再取数据”的缓存保证；客户端应按接口正常刷新。`schema_version` 与 `api_version` 是合同版本，非数据版本。时间均使用RFC3339 UTC，游标与ID不依赖本地时区。

| 方法与路径 | 访问 | 用途/请求 |
|---|---|---|
| GET `/api/v1/info` | 公开 | 最小Relay ID、版本、capabilities、initialized |
| POST `/api/v1/auth/bootstrap` | 公开，配置origin | `{token,username,password}`，仅未初始化库可成功；密码至少12字符 |
| POST `/api/v1/auth/login` | 公开，配置origin | `{username,password}`；只在Cookie设置会话，响应不含令牌 |
| POST `/api/v1/auth/logout` | 管理员 | 撤销当前会话并清Cookie |
| GET `/api/v1/auth/me` | 管理员/设备 | 当前principal `{id,role}` |
| POST `/api/v1/devices/pairings` | 管理员 | 生成5分钟单次配对码 |
| POST `/api/v1/devices/pair` | 公开 | `{code,name}`；首次返回设备令牌和expires_at，应由客户端安全保存 |
| GET `/api/v1/devices` | 管理员 | 设备列表，不含token，包含授权人issued_by、创建/到期/撤销时间；旧授权来源为legacy_unknown |
| DELETE `/api/v1/devices/{id}` | 管理员 | 撤销设备，新请求立即拒绝 |
| GET `/api/v1/services` | 管理员/设备 | 服务与当前观测；设备投影省略base_url/凭据和可写配置 |
| POST `/api/v1/services` | 管理员 | 新增ServiceInput |
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

服务输入字段：`id? name kind base_url ui_url? group? enabled interval_seconds timeout_seconds failure_threshold recovery_threshold allow_private token? clear_token? generation`。新建generation=0，更新必须使用读到的当前值。kind为 `litellm/hermes/workbuddy2api/openwebui/ollama/metacubexd/http/tcp`。基础地址只能是HTTP(S) origin，TCP kind也从该origin解析主机/端口；没有可配置的HTTP方法/路径/请求体。参数范围和证据见COMPATIBILITY。`token`为空保留已有秘密，`clear_token=true`显式清除，不允许同时提供新秘密；秘密从不响应回显；目标地址改变时需重新提供或显式清除原凭据。

Cookie写操作校验已配置origin与双提交 `relay_csrf` Cookie/`X-Relay-CSRF` header。bootstrap/login在未有会话时仅校验origin；Cookie会话采用HttpOnly、SameSite=Strict和匹配部署HTTPS的Secure标志。纯Bearer设备请求不依赖浏览器origin，但不得写管理员资源；设备仅允许读取及更新自己的已读位置。服务端不信任客户端forwarded host/proto来推导允许origin。

JSON请求最大64KiB，未知字段/多JSON/非对象拒绝。常见错误为400输入、401无效或过期凭据、403权限/CSRF/origin、404不存在、409generation/暂停冲突、413过大、415媒体类型、429认证尝试限制、503请求截止、500内部错误。登录连续8次失败锁5分钟；配对失败也有限制。错误文本不输出数据库URL/上游响应/秘密。

认证限流的429响应带`Retry-After: 300`；这不代表已实现上游HTTP限流后的调度退避。配对码为8随机字节的Base32无填充编码（13字符，64 bit），兼容仍有效的旧码。设备名拒绝非法UTF-8、控制字符及双向控制符；签发、成功配对、撤销与脱敏审计在同一事务提交，审计不包含码、令牌或秘密。

## 观测与事件语义

每服务当前结果、最近成功和最近检查时间分开。严格超过两倍检查间隔为stale，当前状态unknown；恰好两倍仍为fresh。例如间隔86400秒时超过48小时才过期；此规则已经在DESIGN 3.4定义，不等于失败检测SLA。缺失、暂停、认证失败、不支持和中断各自保留原因。所有服务推理状态为not_tested，镜像更新状态不可得；基础成功不能代替模型/数据库/容器健康。

检查先持久化queued，再由两个全局worker领取running，网络IO不持有数据库事务锁；每服务至多一个进行中，旧generation结果只入历史。关机/重启的running转interrupted，不生成目标故障。手动操作号幂等不等于外部HTTP exactly-once，不承诺断链后无重复GET。

中断记录的duration_ms为StartedAt到中断完成/恢复时刻的非负墙钟耗时，重启恢复会包含停机时间；缺失开始时间或时钟回退时0表示未测得，不能解读为目标零延迟。

故障/恢复与能力受限事件不可变，incident_id关联；故障事件本身不会因后续恢复改写resolved_at。当前活动故障依据service.open_incident/open_restriction，不能按历史事件缺少resolved_at统计。静默只影响通知资格，检测与历史继续；当前尚无真实客户端通知投递。

按REQUIREMENT D-01，已配对设备默认读取全局脱敏服务与告警；不做租户/服务级授权。事件已读按认证principal独立保存，既不删除原始事件也不修改其他设备；连续游标校验证明提交的序列有效，不证明人实际阅读。未授权、过期或撤销设备不能读取；设备不能修改监控配置、静默、其他设备或保留策略。

检查历史和审计默认永久保留，30天检查历史仍是设计建议，不自动作为已确认默认。诊断页提供策略读取、影响预览、显式确认。启用后启动时及每30秒分别扫描最多200条历史、操作句柄和审计；进度与删除同事务提交、重启可恢复，渐进清理不保证到期立刻删除。只清理已完成且无待执行队列引用的历史及关联索引；操作幂等窗口随历史保留结束。当前/最近成功快照、排队/执行中检查和告警始终保留。告警的evidence_state区分available/expired/unavailable；清理明细后仍有摘要，关闭策略后无法证明原过期原因则标unavailable。审计保留期限同样必须预览确认，没有新增已确认默认；数据库容量/磁盘风险告警尚未实现。

## 存储与部署

`storage.Store`提供View（只读快照）、Update（原子短事务）、Ping、Close，内存Store仅用于测试。bucket归对应模块所有；扫描使用排他字典游标，默认100/最大500，reverse取小于After的键。历史与序号使用可排序键，分页遍历不依赖全量载入。

PostgreSQL初版为relay_records JSONB记录与relay_sequences普通计数表；Update共享短advisory writer锁实现跨进程读改写序列化，禁止在事务中做外部网络调用。App不运行DDL，migration命令使用独立角色与显式SQL，拒绝未知或不连续schema；迁移与普通进程共享数据库leader互斥。只保护同一数据库，不解决独立克隆库双活。

源码Compose默认使用GHCR latest自行测试渠道，版本发布包锁定digest；发布/容器证据见nas/VALIDATION.md，不以本次本地测试代替新提交的远端CI。真实库/SQL授权/迁移、UGOS、备份恢复和客户端验收未执行。容量限制/磁盘风险告警、上游Retry-After退避、账本/余额/目录、付费探针、模型/路由管理、正文、镜像更新、灾备授权代际和原生客户端仍为后续需求。
