# DS审计修复与Hermes交接（2026-10-29）

基线 `3f1521d`，本次修复为其上的本地未提交修改。没有部署、修改真实mihomo配置或发送模型推理。按用户安排，本轮编写回归测试但不执行，由Hermes执行后记录结果。

## 核实与处理

| 审计项 | 代码核实 | 修复或澄清 |
| --- | --- | --- |
| P0节点虚增 | 成立，provider+name产生多个ID；旧服务检查也按provider累计 | 核心 `/proxies` map key唯一身份，provider只补充多归属；界面显示全部归属，筛选任一命中，健康/总数各计一次。服务检查同步使用核心统计 |
| P0日志失去正文 | 成立，固定分类文案丢失诊断 | 保留目标、节点、规则、DNS及错误。过滤查询/fragment、URL认证、Cookie、认证头、凭据字段及已知令牌，单条2048字符；脱敏发生在入缓冲和落库之前 |
| 服务名称/组NUL | 成立，输入校验未拒绝控制字符 | 名称/组拒绝控制字符，创建及修改返回400，拒绝前不写入Store |
| alerts has_more | 成立，满页即true | 同事务扫描当前页后前瞻1条，包含存储500条上限和游标有间隔场景 |
| recovery_count | 成立，无故障也累加 | 仅未恢复故障中累加，失败清零，达到阈值发恢复事件并清零；旧健康状态的累积值立即对外投影为0 |
| 新代码无测试 | 网络/模型工作区缺直接回归覆盖，原有适配器测试不能替代 | 新增21个测试函数，覆盖下列场景；尚未执行，不声称覆盖率达标或测试通过 |
| 网络桶永不清理 | 当前代码不支持此结论 | `Run`调用`RunNetwork`，后者每5秒调用`pruneNetwork`；usage/nodes/usage_groups/logs已有同事务到期索引。修复缩短日志保留期不提前已有索引的边界，补落库/重启/批处理用例 |

## 数据兼容与边界

新增 `nodes[].subscriptions=[{id,name}]`，单值归属字段保留给旧读者。新节点ID与provider无关，仍处于服务/历史代际隔离下。旧节点快照的身份版本不足时不发布旧重复节点，等待采集；原始旧历史不删除、不猜测合并，按既有7天策略回收。新身份时间线从修复后首次采集开始。

配置文件和导入目录没有可靠核心映射时仍是独立未知条目；不以同名继承核心健康。核心 `/proxies` 失败时不使用provider替代健康，订阅额度仍可读取。跨实例同名模型保留两项，符合用户澄清。

网络保留与需管理员确认的检查/审计保留策略独立。网络记录按整小时到期索引清理：7天窗口的物理删除最多延后一小时，日志也以小时桶处理，读取按配置时间过滤；每次清理限批，积压由后续周期续扫。没有宣称真实NAS最大负载下已验证清理吞吐。

日志仍仅管理员可读，持久化默认关闭，已有内存/磁盘条数和字节上限保留。日志中不建立猜测的结构化NodeID关联，不提供完整请求账本。此前已被压成固定文案的历史无法还原；新采集才能获得有效详情。

## 新增回归场景

- `network_collect_test.go`：4个provider各含25节点及重复成员，核心25/22健康/3失败；多归属筛选与历史ID稳定；目录不借健康；核心/provider失败独立；流量回退/缺计数不补算；日志环和pending上限。
- `network_log_test.go`：TCP/DNS原文证据，URL查询、数组查询、IPv6 userinfo、Cookie/Bearer/Basic/JSON字段/已知令牌/编码令牌、控制字符与截断。
- `network_runtime_test.go`：SQLite写入usage/nodes/logs/usage_groups与索引，关闭重开后清理日志及7天数据；索引写失败回滚、pending保留、重试不双计。
- `network_store_test.go`：超过单批日志前缀清理与重启续扫，未来记录保留；暂停时缩短已有日志期限；多归属分页、旧身份快照隔离；代际冲突、导入不信任健康、旧worker禁止提交。
- `network_profiles_test.go`：目录只投影名称协议，不保存server/password；拒绝alias、引用provider、重复键/节点及不支持结构。
- `network_breakdown_test.go`：高基数合并不丢总量；断流/回退不伪造差分。
- `models_workspace_test.go`：跨实例124项逐页完整且不合并，权限不足不冒充推理可用；拒绝旧代际/未来证据/非法查询。
- `ds_audit_test.go`（monitor/api）：恢复阈值和反复故障、500/501告警边界、HTTP精确满页、控制字符创建/修改返回400且不写入。
- `api/network_test.go`：六个section认证、普通设备日志/连接/管理403、筛选参数校验和秘密投影。
- 原 `mihomo_test.go` 的provider重复场景期望由3改为2：权威核心仅same/manual两节点，A/B归属不再额外累计。这是修正语义对应的期望变化。

## 本轮验证

- 已通过：`pnpm build`（TypeScript和Vite）；`go build -tags webassets ./...`；monitor/api测试二进制仅编译；`go vet ./internal/monitor ./internal/api`；`git diff --check`。
- 未执行：所有测试用例、race、PostgreSQL集成、浏览器实测、真实NAS与mihomo联调。仅编译不能证明用例行为通过。
- 无新依赖、SQL schema变更、发布、部署或真实数据删除。

Hermes执行入口（从最终diff记录实际提交/版本身份）：

```sh
cd server
go test -race -count=1 -v ./internal/monitor ./internal/api
go test -race -count=1 ./...
go test -race -tags webassets ./internal/webui ./internal/api ./cmd/relay
go vet -tags webassets ./...
cd ../web
pnpm test
```

真实验收：同一时刻对照mihomo `/proxies` 非组节点数量、健康证据时间和Relay六页签；确认25/22是当时源数据才写入结论。切换四种归属筛选不增加节点身份，翻页不重复，历史不因归属变化新开；TCP/DNS错误可排障且无秘密。复查登录态/设备权限、390px节点多归属换行与日志长文本。测试数据单独使用；不要修改真实代理/订阅/路由或发送付费探针。
