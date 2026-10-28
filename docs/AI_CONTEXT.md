# 项目长期上下文

> 只记录当前仓库核实的事实、协作边界和特殊约束；产品目标见 REQUIREMENT.md，待实施方案见 DESIGN.md。
> 已新增 NAS Go/React 基础监控代码和部署资产；浏览器已做本机合成夹具验证，数据库、Docker、NAS 与真实服务联调仍未验收。外部服务状态、附件摘要和旧 Mac 工程历史记录不等于当前 NAS 的实测结果。

## 1. 项目概况

- 工作目录：`/Users/dinghao/Downloads/Relay-Service`；本项目目标承载 NAS 服务端/Web、macOS 27 与 Android 客户端，见 REQUIREMENT D-11/D-13；正式品牌名仍可后续调整。
- 当前形态：Go 模块化单体 + 嵌入式 React/TypeScript 管理页面的 M1 开发预览。已实现访问控制、服务配置、固定 GET/TCP 基础检查、调度、历史、故障/恢复事件、静默、设备已读和 PostgreSQL 存储代码。需求/设计基线仍为 v1.4-draft；本切片不是完整首期交付或六服务 AC-60 验收。
- 当前审计修复：已读按实际事件顺序连续确认（批量最多500），配对签发/兑换/撤销与脱敏审计同事务，设备记录授权来源；历史/审计保留默认不删除，仅管理员预览确认后周期分批清理，预览通过存储层时间计数，告警和当前快照保留。前端补全主题/图标/状态样式、接入原生dialog封装与Toast，服务卡片和告警展示真实阈值/证据字段，两个轮询接口独立更新；基础总览已改为驾驶舱：运行态势、状态分布、分组概况、需要关注、最近检查和可搜索/筛选/展开的服务明细；配置/证据按需展开，保留策略独立置于系统设置；实际验收范围仍须查本次记录。
- Web（2026-10-28）补充修复：移动窄屏页头刷新按钮仅显示图标，宽高及最小/最大尺寸均为44px、圆形且不收缩，桌面保留文字与无障碍名称；日出日落定位说明/错误放入独立原生dialog，不在首页/导航内联展开；新增复用脉冲标识的SVG/PNG/ICO favicon及Apple触摸图标。总览去掉全局推理未测试提示，基础健康消息仅描述对应服务；仅实际采集到模型目录、部署或调用证据的服务返回inference_state=not_tested，其余not_applicable。新修改的构建/交互验收交Hermes。
- 网络代理（D-20，2026-10-28）已新增原生六页签：概览、用量、订阅、节点、连接、日志；调度器管理独立共享后台采集，不依赖网页打开。核心流量/内存/日志使用有界HTTP流，连接轮询，provider与MetaCubeXD控制层分别投影；控制层地址/加密令牌独立配置，不转发核心secret。配置YAML仅在内存解析name/type，不保存原文、不拉取provider引用URL；同名配置节点不继承核心健康。节点/流量及管理员连接分组保存7天，日志持久默认关闭、开启默认24小时/20MiB且有条数限制。设备不可读取连接、日志和客户端/规则/代理链分组。复用现有Store，无SQL结构迁移；新增直接依赖yaml.v3 v3.0.1，已有go.sum锁定。用户要求跳过测试，本轮未运行测试、构建或真实NAS联调；具体边界及验收清单见nas/NETWORK-PROXY-HERMES-HANDOFF-2026-10-28.md。大模型工作区仍未授权实施。
- Web（2026-10-28）补充按日出日落外观：新浏览器默认自动，显式浏览器定位后按本地坐标计算，每30秒及重新聚焦时校准；位置仅保存在当前浏览器，已有手动/系统偏好保留。定位失败/尚未授权时提示并暂时跟随系统，NAS通过普通HTTP IP访问时浏览器通常要求HTTPS才能定位。服务编辑器支持保存前连通测试，沿用既有安全边界，不保存临时结果；API/检查原因中文化并兼容展示已知历史英文信息。历史弹窗外层加宽、前三列居中不换行，手机通过表格横向滚动；表单原生select与输入框尺寸一致。
- 新增 `mihomo`（订阅流量/到期）、`openai`（只读模型目录）、`statuspage`（服务商报告组件状态）固定GET类型，采集结果随检查快照/历史保存和展示，配额耗尽/到期及公开组件故障复用既有阈值事件。三类响应最多1MiB，投影最多200项；缺失/未知内容不冒充成功的模型可用性。目录与公开状态页可分别添加；新增Pipio `/status`公开数据、自定义JSON/显式HTML状态页、mihomo剩余额度/已有节点质量；LiteLLM模型部署目录和最近24小时有限日志样本按开关采集，分别记录权限、未知和覆盖。逐模型主动推理仍未实现；样本不是完整请求账本或实时健康。兼容范围与真实服务验收边界见 COMPATIBILITY.md。
- Web 导航按 2026-10-28 用户要求参考 Apple 官网的方向，改为轻薄吸顶顶部栏、监控/管理展开面板、诊断入口及外观/账户面板；手机复用原生 dialog 封装提供全屏菜单。仅对现有页面分组，保留角色权限；页面切换使用短入场动画，轮询不更改页面动画的 key，减少动态/透明度与高对比偏好有对应规则。实现与本机验证范围见 `nas/UI-NAVIGATION-2026-10-28.md`，不代表完整 FR-W02 或实机验收。
- 产品目标（非已实现事实）：UGOS PRO Docker 服务端、统一运维 Web、M 系列/Apple Silicon macOS 27 站外分发客户端、Android 17 / Xiaomi 15 只读客户端；用户自配反代，项目交付 Web/API 端口。按 REQUIREMENT D-14，现有六服务运维监控必须具备，调用 Key 管理不列必需交付；详细范围由 REQUIREMENT 管理，不在此重复维护需求。
- 同仓已有 `server/`、`web/`、`deploy/nas/`、`contracts/`；`mac/`、`android/` 尚未创建。`DavisDing/Relay` 仅作为原生 UI/适配器参考，不是首期旧数据迁移来源；本项目首装从空数据初始化。

## 2. 技术与环境

- 已确认的技术方向：NAS Relay 服务端采用 Go（REQUIREMENT D-15，2026-10-26）。代码采用标准库 net/http、context、embed，SQLite/modernc 与 PostgreSQL/pgx、Argon2id、AES-GCM；固定端点检查不发推理请求，监控凭据不回显。
- 锁定依赖：`server/go.mod`/`go.sum`（Go 1.27，pgx/v5 5.11.0、modernc.org/sqlite 1.61.0、x/crypto 0.58.0）；`web/package.json`/`pnpm-lock.yaml`（React 19.2.0、TypeScript 5.9.3、Vite 7.3.7）。Docker builder Node 22.23.4/Go 1.27.3、pnpm 11.25.0、CGO=0；根上下文的多阶段构建先生成页面再嵌入 Go，最终 scratch/默认 root（USER 0:0），直接以 `/relay` 作为 PID 1，包含 CA；本地生成页面在 `internal/webui/dist`。完整构建需 `-tags webassets`，Docker 已固定启用；纯 Go 默认构建不嵌入页面、UI 返回 503，可在无 Node/dist 的新检出运行后端检查。工具链需安装后按下面入口验证，不假设主机 PATH 已具备。
- NAS 存储按 D-19（2026-10-28）切换为默认 SQLite、可选 PostgreSQL：默认 Compose 只有 Relay 服务（固定容器名 relay-service）、8787端口和 relay-data:/data；SQLite 为纯 Go modernc.org/sqlite v1.61.0，无 CGO/额外常驻数据库。单连接/WAL/短事务，OS 文件锁限制同库单实例；默认不探测 PostgreSQL。不配置 URL 使用 SQLite，配置 URL 后连接失败报错；后端绑定及源迁移标记防止切换到空库或误用旧库。
- 数据目录保存 SQLite、encryption.key、initial-password、锁/后端标记；启动与 SQLite 迁移入口将已有数据目录收紧为0700，拒绝目录末端符号链接，无法收紧则停止。Relay 自身生成并持久化秘密，临时密码仅部署日志显示，首次登录强制正式改密，提交后立即删除临时文件；清理失败每30秒后台重试，成功或进程退出后结束，重启仍检查残留。已有数据缺失密钥/指纹不符拒绝启动，不自动重建。旧 deployment.env 支持显式路径兼容。
- 三种用法：单独 SQLite；SQLite 后显式停机 migrate-sqlite 到 PostgreSQL；首装直连 PostgreSQL。迁移维护命令先一致性备份、锁源及目标、拒绝非空业务目标、逐条复制全部记录和序列、事务内核对数量及语义 JSON 摘要，提交回执后标记源退休；同一数据卷/密钥保留，失败/不确定提交按回执重试，不自动回退/双写。SQL 权限、真库/NAS 和镜像发布分别验证。
- 系统设置新增管理员“连接已有 PostgreSQL”前置检查入口，内嵌专用账号/库的 psql 操作说明；GET /api/v1/database 返回当前后端，POST /api/v1/database/postgres/preflight 仅在明确提交时临时连接，最多一个检查、8秒超时，读取认证、普通账号、public USAGE/CREATE/默认schema及目标表/序列/视图状态。检查不创建对象、不保存密码、不切库；页面内迁移尚未开放，迁移仍使用停机维护命令。沿用管理员、首次改密和CSRF限制，不对SQLite默认运行增加探测任务或连接池。
- 可选 PostgreSQL 使用官方 postgres:latest，postgres.compose.yaml 是独立服务示例；init-db/001-roles.sh 供空实例首次初始化或管理员执行一次，只创建 relay 独立库及 relay_app 普通账号，授予 public USAGE/CREATE，由同一账号读写和自动建表。LiteLLM 可共用实例但分库分账号。旧自定义 PostgreSQL 镜像脚本仅兼容历史资产，不再是默认部署或发布依赖。发布包格式3只固定 Relay 摘要并包含官方数据库示例及迁移指引。
- 用户已补充 UGOS PRO 公开最新版本、Docker 部署、LiteLLM 最新版本/默认启动和权限/DB 未启用、M 系列 Mac/非 App Store 分发；此前 Compose/模型映射已经提供，Lucky 由用户自行配置。作为用户报告记录，不虚构版本号或在线验证结果；不再索要 NAS 工作目录/备份目标/Lucky 配置，见 REQUIREMENT 2.3/2.4。
- 不存储真实密码、Key、管理令牌、账户个人信息或真实 NAS 数据。需要证据时使用脱敏配置、固定版本与验证记录。
- 按 D-17，当前没有存量数据，按空库首次安装交付；现按 D-19 默认单 Relay/SQLite，可选 PostgreSQL，不定义常驻迁移服务。PostgreSQL 仅需 `RELAY_DATABASE_URL`，同一专用账号具有 public USAGE/CREATE 并负责业务读写、建表/升级及 SQLite 停机迁移；已移除第二迁移URL及独立迁移账号配置，不保留兼容分支；不自动改变已有对象所有者。启动先检查 schema ledger、必要表列及保留策略触发器/索引，仅空库或有待执行版本时初始化，完成并关闭初始化池后经运行连接复验。结构完整跳过；未知版本、损坏/无账本旧结构或权限错误拒绝启动。已有库升级还需对既有对象具备相应修改权限，schema CREATE 不转移所有权。Compose 直接 `up -d`，不需要手动建表文件。当前开发期首装基线为001/002，retention_at 时间列及普通索引由触发器维护，JSON为业务事实来源；后续有数据后的升级须遵守兼容与备份要求。真实数据库/容器运行及镜像发布分别核实。

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

模块按实际职责组织为 access、monitor、storage、config、api、webui；没有原生客户端入口。持久化存储复用 bucket/key 记录接口；SQLite 使用 JSON 文本，PostgreSQL 使用 JSONB 记录与普通计数表，业务跨记录一致性在短事务写锁内实现，不代表设计中全部关系模型/SQL 约束已落地。

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
| Go 单元/竞态/静态检查 | `server/`：`go test -race ./...`、`go vet ./...` | 默认纯后端检查无需 Web 构建；部分 HTTP fixture 需允许本机临时监听，不连接真实数据库。完整嵌入检查在 Web 构建后执行 `go test -race -tags webassets ./internal/webui ./internal/api ./cmd/relay`、`go vet -tags webassets ./...` |
| Linux 构建 | `server/`：`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags webassets -trimpath -o /tmp/relay-linux-amd64 ./cmd/relay` | 交叉编译不是 Linux/Docker 运行验收 |
| Web 安装/构建 | `web/`：`pnpm install --frozen-lockfile`、`pnpm build` | 输出嵌入 server/internal/webui/dist；安装脚本精确允许 esbuild 版本 |
| Web 会话竞态回归 | `web/`：`pnpm test` | 用现有 TypeScript/React/Node 测试真实 API 会话隔离、组件静态渲染与 CSS 类名合同，不代替浏览器布局和交互验收 |
| 显式迁移/启动 | `server/`：`go run ./cmd/relay migrate`、`go run -tags webassets ./cmd/relay` | 普通启动复用主连接配置自动建表；migrate 保留作维护入口，需真实配置 |
| Compose 入口 | `deploy/nas/`：`docker compose -f docker-compose.yaml config --quiet`，部署见 nas/DEPLOY.md | 默认单 Relay/SQLite，可选 PostgreSQL 时启动按需建表；本地 Docker、数据库与 UGOS 尚未验证 |
| 发布工具检查 | 根目录：`python3 -m unittest discover -s scripts -p 'test_*.py'`、`python3 -m unittest discover -s .github/scripts -p 'test_*.py'` | 离线包/标签合同，不触发发布 |
| 容器基础检查 | 根目录：`sh scripts/container-smoke.sh --build` | 需现成 Docker CLI/daemon；不启动 PostgreSQL；不可用返回 77 |
| ARM64 二进制静态检查 | 根目录：`python3 scripts/check-oci-architecture.py <OCI导出tar> arm64` | CI 导出镜像后核对 config 与实际 ELF 架构；不运行 ARM64 程序，不代表设备验收 |
| PostgreSQL/迁移测试 | `server/`：设置 `RELAY_TEST_DATABASE_URL` 与 `RELAY_REQUIRE_POSTGRES_TESTS=1` 后 `go test -race -count=1 -v ./internal/storage -run 'Postgres\|SQLiteTransfer\|RetentionMapping'` | 必须是专用 `_test` 数据库；本地未配置时默认跳过，强制模式缺配置直接失败。CI/Release 共用验证流程已配置独立 PostgreSQL 服务及必跑任务；修改工作流不代表远程执行通过 |

已核实的核心限制：workbuddy2api 实际健康合同 NEEDS_CONFIRMATION；MetaCubeXD 纯静态与 all-in-one 能力不同；尚无容量/磁盘风险告警、上游Retry-After调度退避、通知投递、账本/余额、正文、付费探针、镜像更新、灾备授权轮换或客户端。认证限流429已有Retry-After:300，与上游退避不同；历史/审计已有显式确认后的保留清理，不自动删除永久告警。不可把历史成功缓存、网页可打开或fixture正常响应当成实时健康/全部验收。

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
