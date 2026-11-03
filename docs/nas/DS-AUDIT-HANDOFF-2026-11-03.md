# DS 审计修复交接（2026-11-03）

基于本地d6c927e继续，修改未提交、推送、部署。覆盖用户首条5项与补充11项（两项性能问题原已列出）。用户安排跳过测试套件，以下新增用例只编译，Hermes负责运行。

## 修复对应

| 问题 | 实现 | 待验收重点 |
|---|---|---|
| 日志200/EOF每秒重连 | 响应头不重置，短连接即使返回一帧也保留1/2/4/8/16/30秒退避；连续连接15秒才恢复1秒 | 空200与单帧EOF各20秒请求数、无假连接日志 |
| not_exposed+空配置显示0 | 默认未知，核心或控制需有可识别证据；空控制清单不能证明没有机场 | 概览、订阅、检查详情一致未知 |
| 流量/内存计划换连写假告警 | 三类流统一识别65秒期限，connecting静默，续连不写恢复事件 | 观察跨3次计划换连，日志不新增collector告警 |
| profiles失败每2秒重试 | 网络失败10秒指数至5分钟；认证/凭据错误5分钟；成功重置，失败不改最后成功时间 | 60秒失败3次请求，核心变化不能绕过冷却；新配置立即生效 |
| 空白行断流/重放 | trim空白后跳过，Scanner.Err显式检查，EOF/取消/格式错误分开 | 同一连接多帧夹空行不重连、不重复入库 |
| 缺vehicleType误判及两处相反 | 单一分类函数；缺类型无额度需核心成功排除同名组，核心不可读不猜测 | 服务检查与工作区相同输入/核心状态给相同订阅数 |
| 旧快照来源矛盾 | 目录版本5刷新旧源状态为waiting，保留历史 | source_states与subscription_sources同口径 |
| 双路由清单漂移 | Web构建前检查前后端全部路径集合一致且无重复 | 任一侧加路径不补另一侧时构建失败 |
| 三处未知显示不同 | SubscriptionEvidence公共判断，未知统一文案；未采集另保留等待状态 | 首页/代理概览/检查详情/空订阅分页 |
| 超65535延迟炸目录 | 原始JSON局部解析，65535有效，溢出/负数/小数/字符串为单节点未知 | 保留其余节点、总数和订阅；最新无效不沿用旧绿色 |
| 空providers/全过滤available | 核心无识别项即not_exposed，解释来源缺口 | entries>0且识别0时显示未知，不以接口200当订阅可用 |
| CSP禁止inline style | 两处分布共用SVG几何属性；严格CSP保持 | 4成功1失败实际宽度4:1，颜色正常 |
| 前缀函数导致全桶扫描 | SQLite/PG Scan与Count改二进制/C排序索引范围，参数绑定，Unicode安全上界 | Unicode、最大码点、%/_/反斜线、逆序/游标；真实查询计划和大数据耗时 |
| 快照写/读回长期持锁 | 短锁摘取批次后释放；整个事务在锁外；失败批次独立重试、新采样继续有界接收；订阅不重复嵌套节点 | 阻塞/失败数据库时applyStream可写，重试无丢失/重复；真实磁盘锁等待 |
| 快照JSON复制长期持锁 | 类型化深复制，覆盖所有可变指针/切片/map | 独立副本与竞态、5000节点benchmark |
| services静默忽略limit | 保留完整数组合同，任何查询参数400 | abc/合法数字/cursor/重复参数均拒绝，无参数仍200 |

无新依赖、schema迁移、历史删除；旧快照下次采集刷新。既有测试调整原因：节点/额度测试夹具明确HTTP来源；缺类型另有对照测试；认证恢复遵守新冷却；未提交批次断言改查独立retry批次且保留条数/金额校验。

## 本轮验证边界

- Web生产构建和前后端路由集合检查通过；Go完整webassets构建、go vet通过；monitor/storage/API测试二进制编译通过。Web回归文件语法检查、差异空白检查通过。
- 本机真实生产页面+合成API、同线上严格CSP预览：4成功/1失败，首页宽度302.53/75.63，服务监控492.39/123.10，组概览338/84.5，均4:1；控制台warn/error为空。没有连接真实上游。
- 未执行Go/Web测试套件、benchmark、105万行性能复测、真实PG/NAS、13分钟流量采样、Docker/远程CI。用户报告的118ms→1ms和122ms/op不作为本轮实测结果。

## Hermes执行入口

```sh
# server/
go test -race -count=1 ./internal/monitor -run 'Network|Mihomo|Compatible|Airport|LogStream|ProfileMetadata'
go test -race -count=1 ./internal/storage -run 'SQLite|Prefix|ExactKey|RetentionMapping'
go test -race -count=1 ./internal/api -run 'ServicesReject|Origin|Auth|Network'
go test -run '^$' -bench BenchmarkNetworkCopyViewLarge -benchmem ./internal/monitor
go test -run '^$' -bench BenchmarkSQLitePrefixScanMillionRows -benchtime=100x -benchmem ./internal/storage
# 仅专用可销毁_test库，禁止生产库
RELAY_REQUIRE_POSTGRES_TESTS=1 go test -race -count=1 -v ./internal/storage -run 'PostgresPrefixRange|PostgresIntegration'
# web/
pnpm build
pnpm test
```

主要新增：ds_audit_20261103_test.go（短200/EOF、空白行、扫描错误、计划续连、冷却、缺类型一致性、延迟边界、慢写事务/失败恢复和深复制）、prefix_test.go（跨后端字面前缀/边界及SQLite查询计划）、service_query_test.go、Web SVG/未知口径回归。真实部署按实际镜像commit分别验收，不能以编译替代运行结果。
