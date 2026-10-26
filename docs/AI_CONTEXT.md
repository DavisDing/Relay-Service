# 项目长期上下文

> 只记录当前仓库核实的事实、协作边界和特殊约束；产品目标见 REQUIREMENT.md，待实施方案见 DESIGN.md。
> 已新增 NAS Go/React 基础监控代码和部署资产；数据库、Docker、浏览器及 NAS 仍未验收。外部服务状态、附件摘要和旧 Mac 工程历史记录不等于当前 NAS 的实测结果。

## 1. 项目概况

- 工作目录：`/Users/dinghao/Downloads/Relay-Service`；本项目目标承载 NAS 服务端/Web、macOS 27 与 Android 客户端，见 REQUIREMENT D-11/D-13；正式品牌名仍可后续调整。
- 当前形态：Go 模块化单体 + 嵌入式 React/TypeScript 管理页面的 M1 开发预览。已实现访问控制、服务配置、固定 GET/TCP 基础检查、调度、历史、故障/恢复事件、静默、设备已读和 PostgreSQL 存储代码。需求/设计基线仍为 v1.4-draft；本切片不是完整首期交付或六服务 AC-60 验收。
- 产品目标（非已实现事实）：UGOS PRO Docker 服务端、统一运维 Web、M 系列/Apple Silicon macOS 27 站外分发客户端、Android 17 / Xiaomi 15 只读客户端；用户自配反代，项目交付 Web/API 端口。按 REQUIREMENT D-14，现有六服务运维监控必须具备，调用 Key 管理不列必需交付；详细范围由 REQUIREMENT 管理，不在此重复维护需求。
- 同仓已有 `server/`、`web/`、`deploy/nas/`、`contracts/`；`mac/`、`android/` 尚未创建。`DavisDing/Relay` 仅作为原生 UI/适配器参考，不是首期旧数据迁移来源；本项目首装从空数据初始化。

## 2. 技术与环境

- 已确认的技术方向：NAS Relay 服务端采用 Go（REQUIREMENT D-15，2026-10-26）。代码采用标准库 net/http、context、embed，PostgreSQL/pgx、Argon2id、AES-GCM；固定端点检查不发推理请求，监控凭据不回显。
- 锁定依赖：`server/go.mod`/`go.sum`（Go 1.27，pgx/v5 5.11.0、x/crypto 0.58.0）；`web/package.json`/`pnpm-lock.yaml`（React 19.2.0、TypeScript 5.9.3、Vite 7.3.7）。Docker builder Node 22.23.4/Go 1.27.3、pnpm 11.25.0、CGO=0；根上下文的多阶段构建先生成页面再嵌入 Go，最终 scratch/非 root，包含 CA；本地生成页面在 `internal/webui/dist`。工具链需安装后按下面入口验证，不假设主机 PATH 已具备。
- 环境配置：`deploy/nas/` 提供 Dockerfile、Compose、`.env.example`、秘密生成脚本和全新独立库初始化脚本；不包含实际 `.env`/秘密。保留用户选择的 PostgreSQL latest，18+ 卷挂载 `/var/lib/postgresql`。按 REQUIREMENT D-16，当前自行测试的源码 Compose/模板默认 `ghcr.io/davisding/relay-service:latest`；CI 配置为本仓库 main 验证成功后发布双架构提交镜像，按 digest 完成 amd64 基础检查后更新 latest，PR/fork 不发包。稳定后才发布正式版；版本 Release 包继续绑定实际构建摘要，另有源码构建覆盖文件。已发布 v0.1.0-alpha.1 预发布及带摘要 NAS 包；测试渠道本次是否已更新须查对应工作流/registry，不以配置存在代替发布证据。发布与验证证据见 nas/VALIDATION.md；未通过数据库/UGOS 完整验收。历史 NAS 配置附件仍未归档。
- 用户已补充 UGOS PRO 公开最新版本、Docker 部署、LiteLLM 最新版本/默认启动和权限/DB 未启用、M 系列 Mac/非 App Store 分发；此前 Compose/模型映射已经提供，Lucky 由用户自行配置。作为用户报告记录，不虚构版本号或在线验证结果；不再索要 NAS 工作目录/备份目标/Lucky 配置，见 REQUIREMENT 2.3/2.4。
- 不存储真实密码、Key、管理令牌、账户个人信息或真实 NAS 数据。需要证据时使用脱敏配置、固定版本与验证记录。

## 3. 代码与文档导航

| 路径 | 当前职责 |
|---|---|
| `AGENTS.md` | 协作入口、文档读取顺序、保护已有修改和阶段边界 |
| `docs/README.md` | 文档导航、配套版本、需求/设计/操作手册阅读顺序 |
| `docs/REQUIREMENT.md` | 已确认需求、提案、首期范围、FR/AC、D/T/E/O 决策登记 |
| `docs/DESIGN.md` | 新项目同仓架构/升级兼容方案；记录约束与设计责任，不假装存在已落地架构 |
| `docs/AI_CONTEXT.md` | 当前仓库事实、长期边界、已核实检查入口 |
| `docs/DEPLOYMENT.md` | 已创建的 LiteLLM 数据库配置指引/示例；尚未部署/实测 |
| `docs/NAS_SETUP.md` | 按用户既有 Compose 整理的 PostgreSQL/LiteLLM 用量日志操作入口；正文关闭，尚未执行 |
| `server/` | Go API、访问控制、监控调度、记录存储、显式 SQL 迁移与测试 |
| `web/` | React/TypeScript 管理页面、锁文件与构建配置 |
| `contracts/IMPLEMENTATION.md` | 本轮实现边界、接口/持久化合同 |
| `docs/COMPATIBILITY.md` | 固定端点、证据层级、版本限制及验证范围 |
| `deploy/nas/`、`docs/nas/DEPLOY.md` | NAS 镜像部署/源码构建、启动/迁移与恢复限制 |
| `.github/`、`scripts/`、`docs/nas/RELEASE.md` | CI、GHCR/Release 工作流、摘要部署包、离线打包测试与无数据库容器检查 |
| `.gitignore` | 忽略实际 env/秘密、node_modules 和临时构建产物；保留环境模板 |

模块按实际职责组织为 access、monitor、storage、config、api、webui；没有原生客户端入口。PostgreSQL 初版使用 bucket/key JSONB 记录与普通计数表，业务跨记录一致性在短事务写锁内实现，不代表设计中全部关系模型/SQL 约束已落地。

## 4. 运行与验证

以下命令仅为从本仓库实际核实的查看/文档检查入口，工作目录均为项目根。命令存在不代表业务验收通过；单次结果记录在会话，不作为长期状态。

| 用途 | 命令 | 前提及核实依据 |
|---|---|---|
| 查看已有工作 | `git status --short`、`git diff --stat` | 当前目录为 Git 工作区；修改前必须查看 |
| 核对具体修改 | `git diff -- docs/AI_CONTEXT.md docs/REQUIREMENT.md docs/DESIGN.md` | 三个实际文档路径；注意 diff 可能包含用户此前修改 |
| 文档差异空白检查 | `git diff --check` | Git 已可用；仅检查差异空白，不检查需求正确性 |
| 查看实际文件 | `git ls-files` | 用跟踪文件核对源码/配置是否确实存在；未跟踪文件另看 status |

新增实际入口（工具链须先可用）：

| 用途 | 工作目录与命令 | 边界 |
|---|---|---|
| Go 单元/竞态/静态检查 | `server/`：`go test -race ./...`、`go vet ./...` | 部分 HTTP fixture 需允许本机临时监听；默认不连接真实数据库 |
| Linux 构建 | `server/`：`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/relay-linux-amd64 ./cmd/relay` | 交叉编译不是 Linux/Docker 运行验收 |
| Web 安装/构建 | `web/`：`pnpm install --frozen-lockfile`、`pnpm build` | 输出嵌入 server/internal/webui/dist；安装脚本精确允许 esbuild 版本 |
| 显式迁移/启动 | `server/`：`go run ./cmd/relay migrate`、`go run ./cmd/relay` | 分别使用迁移/应用角色，需真实配置；本轮未执行 |
| Compose 入口 | `deploy/nas/`：`docker compose --env-file .env -f docker-compose.yaml config --quiet`，部署见 nas/DEPLOY.md | 远端 CI 已解析两种 Compose；本地 Docker 与 UGOS 尚未验证 |
| 发布工具检查 | 根目录：`python3 -m unittest discover -s scripts -p 'test_*.py'`、`python3 -m unittest discover -s .github/scripts -p 'test_*.py'` | 离线包/标签合同，不触发发布 |
| 容器基础检查 | 根目录：`sh scripts/container-smoke.sh --build` | 需现成 Docker CLI/daemon；不启动 PostgreSQL；不可用返回 77 |
| 可选数据库测试 | `server/`：设置 `RELAY_TEST_DATABASE_URL` 后 `go test ./internal/storage -run Postgres` | 必须是专用 `_test` 数据库；默认跳过，用户本轮选择不执行 |

已核实的核心限制：workbuddy2api 实际健康合同 NEEDS_CONFIRMATION；MetaCubeXD 纯静态与 all-in-one 能力不同；尚无自动保留清理/Retry-After、通知投递、账本/余额、正文、付费探针、镜像更新、灾备授权轮换或客户端。不可把历史成功缓存、网页可打开或fixture正常响应当成实时健康/全部验收。

## 5. 项目特殊约束

- 按会话明确的阶段与范围工作；文档审查本身不授权自行进入业务实现、NAS 联调或旧工程改造。
- 需求、建议、设计与实现现状必须区分。REQUIREMENT 中 CONFIRMED 不表示已实现；新增提案无明确用户答复不能自动成为 CONFIRMED。
- 需求决策、技术设计、环境事实、操作授权分别登记 D/T/E/O。环境暂不支持时不能擅自减掉已确认首期功能，兼容性降级不自动等于验收通过。
- NAS 首次部署/版本升级、线上路由修改、真实付费调用、旧本地/云数据清理、新增依赖与发布分别明确范围和授权；默认探针预算不是开发期付费测试授权。
- 本轮新增基础运维 Web；尚未形成独立确认的视觉稿/保护清单，后续优先保留现有功能与状态。Mac 已有 UI 的保护范围按旧工程自己的文档核实；本仓库的概述不是修改旧 UI 的授权。
- 用户已有未提交改动应保留，只做本次范围内补充，不回退或覆盖与本任务无关内容。

## 6. 更新约定

- 事实证据：当前仓库文件、Git 跟踪清单和配置读取。NAS/供应商/旧工程实测需另有版本、权限、步骤和结果记录；外部历史信息的出处与日期保留在 REQUIREMENT。
- 已落地实现、实际架构、已核实入口、长期兼容限制发生变化时更新本文件。未批准技术选择和未落地功能留在 DESIGN/REQUIREMENT。
- 决策确认后更新 REQUIREMENT 中对应 D 项及受影响 FR/AC，DESIGN 引用同一编号；不要在三份文档各维护一套互相矛盾的需求状态。
- 临时进度、当前 diff、单次测试结果与待办完成情况留在会话或交接记录，不记录为长期项目事实。
