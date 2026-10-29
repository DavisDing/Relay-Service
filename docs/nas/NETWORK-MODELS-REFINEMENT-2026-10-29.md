# 网络代理与大模型工作区修订交接

日期：2026-10-29。范围：网络小时数据保留、节点24小时可用性与独立历史、订阅识别、两工作区界面一致性。基于本地`ec4de21`继续修改，未提交、推送或部署。按用户安排不执行测试套件，新增回归用例交Hermes运行。

## 实现与验收对应

1. **保留策略**：当前基线已有`network.expiry`与5秒分批清理，并非完全没有落库清理。本次让`PruneRetention`先处理网络清理，再处理普通历史/审计策略；即使普通策略永久保留、没有活跃实例也执行。新增`network_retention.go`按每桶每轮最多200条续扫`network.usage`、`network.nodes`、`network.usage_groups`，覆盖无索引的旧记录、已删服务与旧代际。游标在`network.retention`，和删除同事务；启动及调度器每30秒推进，重启续扫，扫描完循环。保留7天，以整个小时桶最晚可能样本到期为准，最多宽限不足1小时；日志按既有配置和独立索引处理，不被普通历史开关关闭。无SQL schema迁移。
2. **近24小时可用性**：节点当前页新增`availability_24h`，有效、新鲜、时间可信的源检测按`checked_at`去重；成功率分母仅成功+失败。96段图每格15分钟，有失败优先失败、无有效样本保持未知。界面明确这是检测样本成功率，不是连续在线时长或SLA。历史独立弹窗提供24小时/7天及每页20条，分别保留源检测与Relay观测时间。
3. **订阅识别**：服务检查与工作区都排除`vehicleType=Compatible/Inline`。工作区对类型缺失但与核心非节点组同名的provider兜底排除；HTTP/File真实订阅不因叫default、故障转移或自动选择被误删。节点仍由`/proxies`唯一名称建立身份。快照版本3要求刷新旧目录，保留已有历史至到期。部署后需等下一轮目录采集，不能用旧快照立即确认最终计数。
4. **界面收敛**：沿用apple-design技能与现有Relay主题token，两页共享`WorkspaceSection`、`WorkspacePagination`、范围导航、工具栏、指标条和列表表面；页签不再各自拼装分页。顶级领域为服务监控、大模型、网络代理。内容实色，导航轻透，按压/键盘焦点和减少动态/透明度、高对比规则沿用共享基础。手机节点显示带字段标签的卡片，可用性直接可见；模型列表保持折叠部署细节。保留六页签与权限限制，不新增探针执行能力。

为避免节点页读取历史变成每节点每小时一次数据库往返，Store增加可选`Scan.Keys`（最多500个精确key），兼容原Scan，支持Memory/SQLite/PostgreSQL参数绑定。节点当前页和单节点历史均按批读取，不扫描整个实例历史。无新增依赖。

## 本机已做与未做

- Web TypeScript与生产构建通过；Go构建（含`webassets`）、`go vet`通过；`git diff --check`通过。
- monitor、storage包测试二进制编译通过；Web回归文件语法检查通过。**未执行Go/Web测试套件**，编译不表示用例通过。
- 用实际React组件、临时合成API响应进行浏览器预览：桌面1280px节点列表与历史、24小时切换7天、关闭弹窗后焦点回到历史入口；手机390px节点卡片和深色模型列表；模型第二页与共享分页布局。390px模型页document宽度和scrollWidth均为390，无整页横向溢出。合成数据用于布局与交互，不证明真实API分页或健康结果。
- 临时预览源文件已移除，Vite进程已停止，浏览器viewport恢复。
- PostgreSQL真实数据、NAS、mihomo/LiteLLM认证联调、发布均未执行。本次没有读取真实凭据、向上游写配置或发推理请求。原主动模型探针仍未实现。

## Hermes执行入口

使用项目锁定的Go/Node/pnpm工具链。以下仅为交接命令，本轮没有执行测试。

```sh
# server/
go test -race -count=1 ./internal/monitor -run 'Network|Mihomo|Retention'
go test -race -count=1 ./internal/storage -run 'MemoryExactKeyScan|SQLiteStorageContracts'
# 配置独立、可销毁且以_test结尾的RELAY_TEST_DATABASE_URL，严禁生产库
RELAY_REQUIRE_POSTGRES_TESTS=1 go test -race -count=1 -v ./internal/storage -run '^TestPostgresIntegration$'
# web/
pnpm test
```

重点检查：

- `TestUnifiedRetentionSweepsUnindexedNetworkHoursAcrossRestart`：各桶201条旧记录第一轮删200条，SQLite重启后删余下1条；新鲜边界与当前快照存活；没有服务与普通保留策略也清理。已有`network_runtime_test.go`验证索引、日志限额和期限变化。
- 4个真实HTTP provider各含相同25节点仍只计25节点、22健康；Compatible/Inline不变成订阅；缺失vehicleType的核心组被排除；真实同名HTTP订阅保留。
- 重复checked_at不重复计数；过期、未来、缺失源时间不计成功；没有历史不返回100%；历史代际隔离。
- Memory/SQLite/PostgreSQL精确key批量Scan的空数组、重复/缺失key、前缀/游标、逆序/limit、引号字面值与500个上限。
- 真机桌面/手机、深浅主题、键盘焦点、放大文字与辅助偏好；分页首尾禁用、错误重试、切实例/代际隔离。大模型与网络代理均应保持相同控件和列表节奏。
- NAS停用/删除全部mihomo服务后，旧小时桶依旧渐进回收；真实PG下验证记录数下降。数据库文件/磁盘空间是否立即缩小由数据库自身回收机制决定，不能仅凭文件大小判断清理失败。

主要代码：`server/internal/monitor/network_retention.go`、`network_availability.go`、`mihomo.go`、`retention.go`、`network_store.go`；`server/internal/storage/*`的可选精确key扫描；`web/src/Workspace.tsx`、`workspace.css`、`NetworkProxy.tsx`、`ModelsWorkspace.tsx`及各自布局样式。
