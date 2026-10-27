# NAS M1 开发预览：交付与验证记录

日期：2026-10-26。范围是 Go/React 基础监控切片与发布自动化。已公开预发布版 `v0.1.0-alpha.1`，源码提交 `b73f186eeabfb432cf545fab6c679486c8f2fe27`；[main CI #40326470704](https://github.com/DavisDing/Relay-Service/actions/runs/40326470704) 与 [发布流程 #40326802919](https://github.com/DavisDing/Relay-Service/actions/runs/40326802919) 均成功。需求基线 v1.4-draft 保持不变。工作区后续 UI/文档修改不属于此发布提交，发布不能替代数据库/NAS/完整产品验收。

## 1. 实际交付

| 模块 | 已编码行为 | 对应需求部分 |
|---|---|---|
| `server/internal/access` | 单管理员初始化、Argon2id、12小时会话、连续失败锁定、5分钟单次配对、180天只读设备/即时撤销、AES-GCM服务绑定秘密 | WP-1，AC-13/19/21/31/53 的访问基础，非整项验收 |
| `server/internal/monitor` | 六服务kind/HTTP/TCP、固定无推理端点、SSRF/私网授权、两个worker、周期与手动幂等检查、代际与中断隔离、分页历史、故障/恢复/受限事件、静默、设备独立已读 | WP-2，AC-25/32/38/46/60 的基础切片；端点证据和未验证服务见 COMPATIBILITY |
| `server/internal/api` / `web/` | v1 API、初始化登录、服务增改暂停移除/阈值/凭据清除、历史/告警分页、配对设备、脱敏诊断、Cookie/Origin/CSRF与只读投影 | WP-3，AC-19/53/56/57/60 的基础页面/API |
| `server/internal/storage` | PostgreSQL JSONB记录/计数表、原子短事务、data_version、显式迁移、schema匹配与数据库单活锁；内存Store仅用于测试 | WP-1/8；真实持久化与SQL权限/锁未实测 |
| `.github/` / `scripts/` | PR/main CI、tag Release、GHCR 双架构镜像、digest部署包/校验和、发布前验证 | 上述提交 CI 与预发布流程已通过；完整业务验收仍未完成 |
| `cmd/relay` / `deploy/nas` | 启动/退出、live/ready、独立迁移/版本命令、Dockerfile/拉取镜像Compose/源码构建覆盖文件、角色初始化、随机秘密生成模板 | 部署资产；v0.1.0-alpha.1 已发布；UGOS导入未实测 |

首期完整的账本、目录/余额、模型/路由写入、正文、付费探针/质量、通知投递、版本/digest更新、原生Mac/Android、灾备授权轮换均尚未实现。本版已编码管理员确认后的历史/审计分批保留清理；容量限制和上游 Retry-After 退避仍未实现，不建议作为已验收生产服务。

## 2. 本轮实际检查

| 检查 | 结果 | 实际范围 |
|---|---|---|
| `cd server && env -u RELAY_TEST_DATABASE_URL go test -race ./...` | 通过 | access/api/config/monitor/storage/webui；本地HTTP fixture和内存Store，数据库集成测试显式跳过 |
| `cd server && go vet ./...` | 通过 | 全部Go包静态检查 |
| `cd server && go mod verify` | 通过 | 锁定模块校验；不是依赖零漏洞保证 |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false ... ./cmd/relay`（另以 arm64 构建） | 通过 | 两个架构分别构建静态 ELF；本地未运行 Linux 产物 |
| `cd web && pnpm install --frozen-lockfile && pnpm build` | 通过 | TypeScript检查与Vite编译，产物嵌入Go；非浏览器行为验收 |
| `pnpm audit --prod` / 完整依赖审计 | 通过，0项已知命中 | 本轮锁文件/当时registry结果；不保证未来安全性 |
| 编译页面与JS/CSS静态资源HTTP测试 | 通过 | 嵌入文件存在、Content/Cache策略、API/非法方法边界 |
| 发布打包离线测试 | 通过，10项 | 合法/非法版本与摘要、输出拒覆盖、秘密/白名单、包结构、校验和、可重现、环境权限与摘要保留 |
| 发布标签离线测试 | 通过 | Git fixture 验证标签与默认分支祖先、注解预发布标签及非法输入；非远端tag验证 |
| `relay version` 与链接元数据 | 通过 | 本地命令无需数据库，默认/注入 version/revision/created JSON 可读 |
| Shell语法、随机env生成/权限/拒绝覆盖 | 通过 | `sh -n`，临时路径验证600权限/64hex秘密/已有文件不改 |
| Compose YAML解析与关键引用检查 | 通过 | Ruby YAML，核对latest、卷路径、角色环境和migration无profile；不是`docker compose config` |
| `git diff --check` | 通过 | 文本空白检查 |
| PostgreSQL库、DDL/授权、两pool并发、数据库锁、持久化/重启 | **未执行** | 用户明确选择跳过数据库实测；临时PG仅编译/启动后已停止，未创建业务角色/库、未迁移或运行应用 |
| Docker 镜像构建、`docker compose config`、amd64 容器基础检查 | 远端通过，本地未执行 | 发布提交 CI #40326470704 的对应步骤均成功；amd64 运行仅为版本/结构/无服务健康检查，arm64 只构建 |
| UGOS Projects、真实六服务/网络/TLS | **未执行** | 没有真实 NAS 验收证据 |
| 浏览器E2E | **未执行** | 浏览器连接临时内存fixture origin被自动审批拒绝（未获连接器origin授权）；已清理fixture进程/源码 |
| GitHub Actions | 上述提交 CI 通过 | Web/Go/19项发布脚本测试、Compose、amd64容器检查、arm64构建；其他提交须单独核对 |
| GHCR push、Release 资产上传/公开 | 通过 | v0.1.0-alpha.1 为公开 prerelease；发布后 digest 的 amd64 无数据库检查、五项资产上传、draft 状态与远端标签重核成功 |
| 发布资产独立下载/匿名 registry 核对 | 通过 | 五项资产与 GitHub digest/SHA256SUMS 一致；metadata/包内 env 指向同一镜像；匿名 manifest 含 linux/amd64、linux/arm64 |
| 实际付费调用、NAS配置写入、旧应用改造 | **未执行** | 不在本轮范围 |

## 3. 工具与依赖

- Go工具链 1.27.3 darwin/arm64，本轮临时放在 `/private/tmp`；项目声明Go 1.27.0。没有修改系统Go环境。
- 直接Go依赖：pgx/v5 5.11.0（PostgreSQL驱动/连接池，标准库不提供PG驱动）、x/crypto 0.58.0（Argon2id）。其余间接依赖见go.mod/go.sum。
- React/ReactDOM 19.2.0、TypeScript5.9.3、Vite7.3.7；pnpm lock精确锁定，esbuild0.28.2构建脚本按版本允许。Vite从7.1.12更新以解决本轮审计命中。
- 原设计中独立domain关系表/复杂迁移库在初版暂以JSONB记录+普通计数表+内嵌顺序SQL替代，减少新依赖；迁移/恢复能力仍需真实库验证，后续不能直接改已发布迁移。

## 4. 下次验证入口与阻塞

基础监控必须继续验证 workbuddy2api 的实际fork/version与安全端点、MetaCubeXD部署变体，以及六服务安装响应，才能判断AC-60。单纯TCP连通/原WebUI链接不能算六服务完整监控。

数据库验证已提供显式可选测试：仅在专用名以`_test`结尾的空测试数据库设置`RELAY_TEST_DATABASE_URL`后运行`go test ./internal/storage -run Postgres -v`。测试使用随机schema，验证迁移幂等、回滚/版本、跨pool序号、leader和未知schema；默认跳过。运行角色/初始化脚本授权链仍需部署专项测试。

发布流程和资产说明见 RELEASE.md。本地容器检查缺少 Docker CLI 时实际返回 77；远端已执行范围以上述 CI 提交为准。Docker/NAS完整验收按DEPLOY执行并保存实际镜像digest、PG主版本、脚本/SQL结果、重启持久化、故障恢复/配对撤销、内存/磁盘/CPU与TLS记录。受控恢复授权代际轮换未实现前，不把旧库恢复直接当作安全生产流程。后续改动须继续区分代码、fixture、真实库、容器、NAS和客户端结果。

## 5. v0.1.0-alpha.1 发布身份

- Release：<https://github.com/DavisDing/Relay-Service/releases/tag/v0.1.0-alpha.1>
- Git commit：`b73f186eeabfb432cf545fab6c679486c8f2fe27`
- 镜像：`ghcr.io/davisding/relay-service@sha256:28a97269ef0f756113484fcf654dc508c7b7c9c2069daaca72aa5d5b5a28a102`
- 包：`relay-service-v0.1.0-alpha.1-nas.tar.gz`，SHA-256 `1ef537bdcf810afa436b7697404874287851402c2ce0ecc7886112c69bc0c5ff`
- 远端 amd64 仅验证版本身份/非 root/结构/CA 和无监听时健康检查失败；arm64 仅构建。PostgreSQL 实测按用户选择跳过，没有 NAS、浏览器、完整启动或六服务业务验收。
