# Relay · LiteLLM 数据库接入与升级操作指引

- 日期：2026-10-09。
- 状态：文档方案 / PROPOSED 实施；D-02 已确认可以给 LiteLLM 配置数据库并要求说明步骤。没有在 NAS 执行任何命令。
- 范围：既有 LiteLLM 增加 PostgreSQL，不重建 Open WebUI/Ollama/Hermes/workbuddy2api，不自动修改客户端调用 Key 或开启付费探针。
- 输入：用户已经提供的 Docker Compose，NAS 为 UGOS PRO/Docker，LiteLLM 默认最新版本且当前无数据库。示例服务名/环境引用沿用实际 Compose；不要求用户提供工作目录、备份位置或 Lucky 配置后才能继续设计。真正部署仍需明确操作范围/授权。

## 1. 准备与安全边界

> 说明：本节提到的持久化目录、Compose 工作目录和备份目标，是部署实施时必须落地的责任边界，不是要求用户现在把 NAS 路径交给设计阶段。Docker 容器可重建而数据不能随容器消失，因此实施时必须在已有 Compose 中明确卷/目录、权限、备份与恢复方式；项目不读取或管理 Lucky 配置。

1. 保存现有 Compose、LiteLLM `config.yaml`、受保护环境文件和镜像 digest；秘密与普通配置分开备份，不能贴到聊天/日志。
2. 明确维护窗口、允许重启的服务及回滚版本；数据库连接配置修改可能需要重建 LiteLLM 容器，不能保证零中断。
3. 拟交付 Compose 默认使用 Docker 命名卷，不要求指定数据库宿主机目录；若已有 Compose 使用目录挂载，则保留其选择，不擅自搬迁。路径和备份目标由部署者在 NAS 现场决定并记录，不需要发送给设计方；必须保证重启/升级不丢数据，并能按指引恢复，不覆盖已有数据目录。
4. 推荐 PostgreSQL 17 为验证基线，不表示必须是最新系列。实施固定具体补丁版本及 digest，不使用浮动 `postgres:latest`。如果复用已有兼容 PostgreSQL，跳过建容器，仍需独立库/角色。
5. 共用一个 PostgreSQL 实例时创建 `litellm` 和 `relay` 两库、两业务账号；Relay 不读写 LiteLLM 私有表。数据库共享故障域会影响两个应用，恢复演练需覆盖。

### 1.1 为什么保留持久化与备份说明

| 项目 | 实际用途 | 当前需要用户提供吗？ | 拟交付处理 |
|---|---|---|---|
| 数据库持久化 | 容器删除/重建后保留账本、任务和配置；不能只写容器临时层 | 不需要 NAS 路径 | 默认命名卷；已有目录挂载按原 Compose 保留，升级沿用同一数据卷 |
| Relay 工作目录 | 区分镜像内程序目录与宿主机放 Compose/受保护配置的位置 | 不需要专门创建或提交业务工作目录 | 程序随镜像交付；配置按已有 Compose 组织，业务事实保存在 relay 库 |
| 备份目标 | 发生数据库损坏、误操作或升级失败时恢复数据 | 设计阶段不需要位置 | 管理员部署时选择并记录目标、保留周期与恢复步骤；同盘副本不能覆盖整机/磁盘故障 |

Docker 命名卷的内容独立于容器生命周期；卷保留不等于已有数据库备份。依据见第 8 节 Docker 官方卷说明。

## 2. 增加 PostgreSQL（拟推荐的命名卷合并片段）

以下只展示新增服务的合并片段，不是用户原 Compose 的完整重建，也未在 UGOS PRO 执行。片段仅针对 PostgreSQL 17 的目录布局；不要同时覆盖/重新声明用户已有的 `ai-network`。已有网络名需核对 Compose 实际创建的名称；跨 Compose 复用时才将网络声明为 `external: true`。

```yaml
services:
  postgres:
    image: ${POSTGRES_IMAGE:?设置已验证的17系列标签与digest}
    restart: unless-stopped
    environment:
      POSTGRES_USER: postgres
      POSTGRES_DB: postgres
      POSTGRES_PASSWORD_FILE: /run/secrets/postgres_init_password
    secrets:
      - postgres_init_password
    volumes:
      - relay_postgres_data:/var/lib/postgresql/data
    networks:
      - ai-network
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres -d postgres"]
      interval: 10s
      timeout: 5s
      retries: 5
    # 不配置 ports，不默认开放宿主机5432。

secrets:
  postgres_init_password:
    file: ${POSTGRES_INIT_PASSWORD_FILE:?设置NAS本地受保护密码文件}

volumes:
  relay_postgres_data:
    name: ${POSTGRES_VOLUME_NAME:-relay-postgres-data}
```

- 管理员在 NAS 本地创建强随机初始化密码文件，仅允许必要账号读取，不提交 Git。Compose 文件挂载并不是加密保险库；宿主机管理者可能读取秘密。
- 确认卷名没有与现存不相关数据库冲突，记录后续升级沿用的卷名；若改用目录挂载，另外核对路径和镜像用户权限。再只启动此服务，例如在实际 Compose 工作目录执行 `docker compose up -d postgres`。命令是执行指引，不代表当前工具已经运行。
- 命名卷由 Docker 管理，不需要把它的宿主机目录提供给项目；后续不能随意更换卷名或用 `down -v` 清卷。若需要目录挂载，在部署时改为 `${POSTGRES_DATA_DIR}:/var/lib/postgresql/data` 并检查权限，不同时使用两种挂载覆盖同一目录。
- `pg_isready` 仅证明数据库服务接受连接，不证明业务账号权限/表迁移正确。
- 官方镜像初始化环境和 `/docker-entrypoint-initdb.d` 只作用于空数据目录。已有目录修改初始化密码变量不会自动改库内密码；失败后禁止删除数据卷“重来”，应检查/修复对应初始化阶段。

## 3. 创建独立业务数据库和角色

进入数据库本地交互终端（示例服务名 `postgres`，须与实际 Compose 一致）：

```sh
docker compose exec postgres psql -U postgres -d postgres
```

检查库/角色是否已有；下列为**首次建库**示例，不是无限重跑脚本。若已有则核对所有者和权限，不重新创建或覆盖。

```sql
CREATE ROLE litellm_app LOGIN;
\password litellm_app
CREATE DATABASE litellm OWNER litellm_app;
REVOKE CONNECT ON DATABASE litellm FROM PUBLIC;
GRANT CONNECT ON DATABASE litellm TO litellm_app;

CREATE ROLE relay_app LOGIN;
\password relay_app
CREATE DATABASE relay OWNER relay_app;
REVOKE CONNECT ON DATABASE relay FROM PUBLIC;
GRANT CONNECT ON DATABASE relay TO relay_app;
```

`\password` 是 psql 交互输入，避免把业务密码写成 SQL 示例或命令行参数。两个业务角色不是超级用户；数据库所有者只负责自己的库。后续按实际迁移权限验证 schema 创建/读取，不无条件给 `SUPERUSER`。生产可进一步分开迁移角色与运行角色，最终权限由实际版本测试确认。

## 4. 配置 LiteLLM 数据库与加密依赖

在 NAS 本地受保护的环境文件中设置（下列不是可用真实值）：

```dotenv
DATABASE_URL=postgresql://litellm_app:<URL编码后的数据库密码>@postgres:5432/litellm
LITELLM_MASTER_KEY=sk-<管理Key，不对客户端分发>
LITELLM_SALT_KEY=sk-<新生成并安全保存的固定加密依赖>
STORE_MODEL_IN_DB=True
```

- `postgres` 必须替换为 LiteLLM 容器网络可解析的实际数据库服务名/地址；不要在容器里使用宿主机的 `localhost` 代指 PostgreSQL 容器。
- 密码包含保留字符时必须正确 URL 编码；不要把连接串打印到普通日志。
- 若原 Compose 使用 `env_file`，确认四个变量被传入 LiteLLM；如果另有 `environment` 同名项，其覆盖顺序必须核对。不能假定放进目录里的 `.env` 自动成为所有容器环境。
- Master Key 在此阶段优先保留现有值，避免 Open WebUI/Hermes 仍用管理 Key 时无意断流；后续创建独立调用 Key，再在明确授权范围内切换客户端。
- Salt Key 在任何数据库模型秘密落库前配置；已存在模型密文则先核对当时加密依赖。随意更改 Salt Key 会使旧凭据无法解密，不能把换 Key 当普通重启。密钥、数据库与固定版本都属于恢复依赖。
- `STORE_MODEL_IN_DB` 开启数据库模型写入，不会自动把已有 YAML 模型导入数据库或使其可通过 UI 编辑。

## 5. 应用数据库配置并核对

1. 验证目标 LiteLLM 固定镜像包含本版本所需数据库迁移能力，按该版本文档安排 schema 初始化；首期单实例不盲目复制云多实例的 `DISABLE_SCHEMA_UPDATE=true` 而遗漏独立迁移步骤。
2. 在维护窗口应用环境变化，只针对 LiteLLM 重建/启动；示例 `docker compose up -d <实际LiteLLM服务名>`。不要执行整个项目的 `down -v`。
3. 用数据库自身查询及脱敏诊断确认：库连接成功、schema 初始化完成、版本可得、目录保留、管理权限正常。日志只检查错误类别，避免拷贝环境/连接串/完整响应。
4. 优先固定版本的无推理进程/就绪检查；不要用可能触发全模型推理的 `/health` 验证“免费连通”。
5. 在授权的非生产测试范围创建/限制/撤销一个测试虚拟 Key，核对持久化及允许模型/预算/到期行为；创建 Key 是写操作，数据库就绪并不自动授权。
6. 经单独付费授权发一个限定目标/输出/预算的请求，验证真实请求日志与消费来源；不能仅凭成功创建 Key 就声称采集已完整。
7. 未完成接口验证时保留明确原因，不用零值替代未知消费；正文仍默认关闭，D-04 确认后再配置正文接入。

## 6. 文件模型到数据库模型的独立切换

数据库 Key 与日志先接通，不必立即一次性改动历史 80 条别名。已启用数据库后，YAML 模型仍由文件控制，API/UI 不能假装已编辑它们。

1. 按实际模型/部署稳定映射列出需要日常可写管理的对象，记录参数、渠道/账户、价格和来源；解析 YAML 锚点/环境引用，但不在普通导出落地秘密。
2. 在隔离验证环境确认同别名、多部署、fallback/路由设置和数据库新增接口支持。
3. 计划维护窗口，以备份和明确切换顺序将目标转到数据库来源；同一对象不能长期在 YAML 与 DB 重复声明产生双路由。可先迁少量对象，但保留首期完整管理目标。
4. 对目录/实际部署/允许 Key/路由读回核对；无法热更新的字段说明重启步骤。不因新增模型接口成功就宣称所有 fallback 字段均可热改。
5. 回滚按文件/数据库切换备份和目标对象清单执行，不删除整库。

## 7. 备份、升级与恢复

- 两库分别备份，记录一致性时间点、应用/schema 版本；数据库导出、秘密保护和正文保留过滤是不同责任。文本默认导出不带正文或秘密不等于数据库完整备份能完全不含它们。
- 数据库完整灾备涉及敏感数据时必须受控加密/授权；正文恢复后按原到期时间拒绝读取。备份/秘密保存选择落实后才能宣称“可恢复”。
- D-10 的 24 小时数据丢失/60 分钟恢复是已接受目标，不是已测能力；管理员备份须有实际执行节奏，不能拿指引代替成功记录。
- 正常本项目升级保留服务身份/设备授权，schema/API 与 Mac 缓存/设置各自迁移；失败进入维护/只读。灾备恢复/换 NAS 才按 FR-O03 使旧会话/配对码/设备凭据失效。
- 不重放未知付费任务；只恢复 Relay 库不能覆盖现存 LiteLLM Key/路由；若恢复 LiteLLM 库，需要核对撤销 Key 复活与消费状态。

## 8. 依据与验证边界

2026-10-09 只读核对官方资料：[LiteLLM 虚拟 Key](https://docs.litellm.ai/docs/proxy/virtual_keys)、[数据库模型](https://docs.litellm.ai/docs/proxy/model_management)、[生产部署](https://docs.litellm.ai/docs/proxy/deploy)、[健康检查](https://docs.litellm.ai/docs/proxy/health)、[PostgreSQL 官方镜像](https://hub.docker.com/_/postgres)。本轮另核对 [Docker 命名卷与生命周期](https://docs.docker.com/engine/storage/volumes/)，用于说明容器/卷的责任边界。在线文档不能直接等同于 NAS 的固定安装版本；本文没有执行 Compose、SQL、读取真实秘密或付费调用。
