# SQLite / PostgreSQL 测试交接（2026-10-28）

用户要求跳过后续测试，交由其他模型执行。当前为未提交的源码改动，没有推送、发布或操作 NAS。请先读取 AGENTS、AI_CONTEXT、REQUIREMENT D-19、DESIGN 存储更新和 STORAGE.md，保留当前工作区改动。

## 本次实现

默认单容器 SQLite；显式 PostgreSQL 首装；停机 SQLite→PostgreSQL迁移。新增 modernc.org/sqlite v1.61.0（纯Go）；默认 Compose 不启动 PostgreSQL。SQLite WAL/单连接/4MiB页面缓存目标、短事务、文件锁；迁移流式复制全部业务 bucket 和序列，保留原密钥，备份/摘要/事务回执/源退休/目标绑定防止覆盖或误回退。

重点文件：server/internal/storage/{sqlite,filelock,transfer}.go；server/cmd/relay/{main,local,transfer}.go；internal/config/config.go；deploy/nas/ 下 Compose/Dockerfile/初始化脚本；scripts/package-release.sh；三份工作流；README、STORAGE、DEPLOY、RELEASE。

## 已有结果与证据边界

此前阶段已运行并通过：Go 全包竞态测试、go vet、配置/存储/启动相关测试，scripts 的26项 Python 测试和 .github/scripts 的9项测试。首次沙箱执行 HTTP fixture 因监听权限失败，扩大权限后通过。以上仅是当时工作区的结果，不能当作最终修改的完整回归。

之后新增/调整了 migration.pending 启动守卫、目标时间戳微秒截断、迁移回执快速检查，以及私有备份文件预留；这些最终修改尚未重新测试。用户要求跳过后，只整理代码格式、完善迁移错误提示和文档，没有再运行测试、构建或部署。

真实 PostgreSQL/权限/迁移联调、SQLite服务端完整登录流程、Docker Compose/容器运行、Linux双架构最终构建及NAS/负载验收尚未执行。没有固定内存或性能结论。

## 建议测试顺序

1. 三条部署路径：默认无环境变量首装SQLite；直接连接空PostgreSQL；已有SQLite停机后迁移。登录改密、重启、服务配置/历史/告警/设备及已加密凭据在迁移后保持可用，不能只比较表行数。
2. SQLite 事务回滚/数据版本/序列并发、保留策略计数与分页、同库双实例及维护锁。核对数据目录、备份、密钥和临时密码权限；改密后临时文件删除，丢失密钥或错误指纹不得自动接管。
3. 用真实 PostgreSQL 和脚本创建的低权限账号测试：业务账号禁止DDL，迁移账号可初始化/升级，业务账号可读迁移回执；重复初始化与中途失败不覆盖已有库/口令。LiteLLM分库不受影响。
4. 迁移拒绝非空目标/正在运行的目标；提交前故障全部回滚；提交回执后源标记前中断可以重试；migration.pending 阻止旧源重新启动；重复迁移、目标变更/新增数据、连接变量遗漏、连接失败不能误切空库。校验大整数JSON、排序、时间精度、序列和原密钥。
5. 备份路径预先存在或为符号链接时拒绝覆盖；VACUUM INTO 私有空文件、磁盘满/取消/权限不足后的状态明确，部分备份不会覆盖或误判成功。恢复时隔离源/目标，并保留原密钥与授权风险边界。
6. 最终嵌入Web构建、CGO_ENABLED=0 Linux amd64/arm64、默认镜像非root数据卷可写、Compose默认/设置/网络/官方PG组合、格式3部署包和发布工作流。不要发布或访问真实NAS来代替本地验证。

从仓库入口核实后可执行：

```sh
cd server
go test -race ./...
go vet ./...
go mod verify
# 专用数据库名必须以 _test 结尾；不要使用真实库。
RELAY_TEST_DATABASE_URL='postgres://测试账号:测试密码@127.0.0.1:测试端口/relay_test?sslmode=disable' go test -race ./internal/storage -run 'Postgres|SQLiteTransfer'
```

完整前端/嵌入/发布工具命令见 AI_CONTEXT。PostgreSQL迁移测试默认跳过，必须显式提供专用测试数据库；不把默认测试通过当作迁移联调通过。不要因当前用户跳过本地测试而删除或禁用CI检查。

## 本机临时工具（可选，不是项目依赖）

Go位于 `/private/tmp/relay-nas-tools-20261028/go/bin`，模块/构建缓存为 `/private/tmp/relay-nas-go-modules`、`/private/tmp/relay-nas-go-cache`。临时PostgreSQL18.3二进制位于 `/private/tmp/relay-sqlite-postgres-check/pg/bin`；initdb在沙箱内创建共享内存失败，未启动测试数据库。若继续使用，需要相应的本机监听/共享内存权限；不要改系统服务。工具可能被系统清理。Docker当前不可用。
