# PostgreSQL 索引修复与启动自动初始化

2026-10-27，用户确认当前没有存量数据需要迁移。本次按全新安装修复源码与部署包；尚未提交/发布本次修复镜像，未执行真实 PostgreSQL、Docker 或 NAS 验收。

## 原因与最终实现

原 `002_retention_count_indexes.sql` 在表达式索引中使用 text → timestamptz 转换。该转换使用 STABLE 输入函数，而 PostgreSQL 索引表达式要求 IMMUTABLE，会阻止全新库完成建表。

当前仍处于开发预览且没有存量数据，直接修正首装基线 002，与原 001 顺序执行：

- 增加普通 `retention_at TIMESTAMPTZ` 列；触发器在 INSERT/UPDATE 时从历史的 `completed_at` 或审计的 `at` 派生，其他桶置 NULL。业务 JSON 仍为事实来源，普通应用无需新增 DDL 权限。
- 建立 `(bucket, retention_at)` 普通索引，避免在索引表达式中做文本时间转换。
- Count 对相应桶和时间字段使用该列，继续排除零值时间、queued/running，并保留 Prefix；其他桶/字段保留通用计数语义。
- 移除前轮新增的 003、旧版本跳过逻辑、旧索引清理及数据回填；未操作任何实际数据库或卷。

用户进一步确认初始化由程序自动完成。默认 Compose 仅保留 Relay/PostgreSQL，直接 `up -d`：PostgreSQL 空卷脚本创建独立库与角色；Relay 启动先以业务账号检查结构版本和必要对象，只有缺失/待执行版本才打开独立初始化连接，执行 SQL 后关闭连接池，再用业务账号复验，最后启动 HTTP/调度。

完整结构直接启动，不打开初始化连接。默认 Compose 注入独立 `RELAY_MIGRATION_DATABASE_URL`，要求与业务连接匹配主机、端口、库及连接选项；业务账号仍无 DDL 权限。容器环境会持有初始化凭据，关闭连接池不等于移除环境中的秘密。原手动初始化覆盖文件与对应打包步骤已移除，`relay migrate` 保留作维护入口并在结束前验证结构。

## 初始化与未来升级边界

当前首装基线为 001/002。建表命令仅接受严格连续的账本前缀，拒绝未知、超前或缺口；启动要求与镜像完整匹配。每个 SQL 文件与对应账本写入在同一事务提交；重复运行已完成的建表命令不会再次执行 SQL。数据库单活锁继续保护建表与普通进程互斥。初始化错误、未知/不连续版本或权限错误阻止启动；已有业务表却没有账本/账本记录时拒绝初始化；完整账本下缺失必要表列、保留策略触发器/索引或禁用触发器时报告结构损坏，不尝试重建数据。

本次不提供历史开发 schema 的升级/回填路径，不适用于已有旧结构或旧账本的数据库。未来本基线部署并产生数据后，按 D-08 新增兼容迁移、升级前备份，不能继续重写已部署 SQL 或通过删卷升级。

## 验证边界

- Count 查询生成测试覆盖时间列选择、通用字段、零值/状态条件与参数顺序。
- Memory 与 opt-in PostgreSQL 共用计数合同：历史/审计、Prefix、时间边界、零值、缺失/null、queued/running、更新后同步、时区语义。
- 启动单元测试覆盖：结构完整时跳过初始化连接、缺失/待执行版本、未配置初始化凭据、连接失败、SQL 失败时关闭连接池、业务账号复验失败、取消；断言先关闭初始化池，再复验业务连接。配置测试覆盖连接目标不一致及错误消息不含凭据；版本测试覆盖空库、连续前缀、完整、缺口/重复/乱序/超前。
- PostgreSQL 集成入口覆盖空库、结构完整后的计数、禁用触发器/缺索引/缺列，以及无账本或空账本但已有业务表时拒绝初始化且保留数据；原有套件保留幂等、数据保留、未知版本拒绝和单活验证。按用户此前“跳过数据库实测”的选择，本次不配置真实库。
- 本轮通过：`go test -race ./...`、`go vet ./...`、`go mod verify`；19 项打包/工具测试及 9 项发布元数据测试；三份 YAML 解析、两服务自动初始化结构检查、shell 语法及 `git diff --check`。
- 未执行：PostgreSQL 集成测试（运行时明确 SKIP）、Docker Compose 实际解析/容器运行、UGOS、执行计划与修复镜像发布。查询生成或 Memory 测试不代替这些验收。

修复发布后的启动步骤见 [DEPLOY.md](DEPLOY.md)。验证工具链和缓存仅放入本轮独立 `/private/tmp/relay-auto-init-*` 目录，完成后清理；没有增加项目依赖。

官方依据：前轮已只读核对 PostgreSQL `CREATE INDEX` 的 immutable 约束及 PostgreSQL 18 `pg_proc.dat` 中 `timestamptz_in` 的 `provolatile = 's'` 定义；资料核对不代替数据库执行。

## 启动流程参考

- PostgreSQL 官方镜像入口只在数据目录为空时创建库、角色并执行 `/docker-entrypoint-initdb.d`；不能只靠该目录处理后续应用 schema 检查。
- Grafana 的 SQL store 启动时调用版本化 migration 并使用 migration lock。本项目复用这一启动顺序思想，保留自身 SQL、权限分离和账本规则，没有引入其代码或依赖。
- 参考源码：`docker-library/postgres/docker-entrypoint.sh` 的 `DATABASE_ALREADY_EXISTS` / `docker_process_init_files`；`grafana/grafana/pkg/services/sqlstore/sqlstore.go` 的 `ProvideService` / `migrate`。官方源码阅读不构成数据库运行验收。
