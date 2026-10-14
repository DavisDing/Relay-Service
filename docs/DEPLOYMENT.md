# Relay · LiteLLM 数据库接入与升级操作指引

> 首次按当前 Compose 操作请使用 [2026-10-13 最新操作手册](NAS_SETUP.md)。本文保留设计依据、通用配置和后续模型管理/恢复说明；新手册沿用共享 `.env`，按一个顺序执行。

- 更新日期：2026-10-13（根据用户贴出的 Compose 补充最小修改位置；沿用共享 `.env`）。
- 状态：文档方案 / PROPOSED 实施；D-02 已确认可以给 LiteLLM 配置数据库并要求说明步骤。没有在 NAS 执行任何命令。
- 范围：既有 LiteLLM 增加 PostgreSQL，不重建 Open WebUI/Ollama/Hermes/workbuddy2api，不自动修改客户端调用 Key 或开启付费探针。
- 输入：用户已经提供的 Docker Compose，NAS 为 UGOS PRO/Docker，LiteLLM 默认最新版本且当前无数据库。示例服务名/环境引用沿用实际 Compose；不要求用户提供工作目录、备份位置或 Lucky 配置后才能继续设计。真正部署仍需明确操作范围/授权。

## 0. 第一阶段：只接通数据库和请求用量日志（2026-10-11）

### 0.1 当前要做什么、安装什么

第一步是“已有 LiteLLM → PostgreSQL → 记录请求元数据/Token/费用”，不是开启全部对话正文。这里只完成公开接口/设置核对与部署操作方案，未连接 NAS、安装容器或完成 WP-0 实机验证。

| 项目 | 本阶段处理 |
|---|---|
| PostgreSQL | 新增一个官方镜像容器，按第 2 节合并到已有 Compose；`postgres:latest` 为用户已选择的镜像策略，安装/更新时记录实际主版本和摘要 |
| 已有 LiteLLM | 复用当前服务、端口、模型列表、命令和 Master Key；加入 DB 环境与请求日志设置；核对镜像带有所需 Prisma/schema 初始化能力 |
| Redis、Langfuse、Prometheus、pgAdmin、宿主机 Python/PostgreSQL | 本阶段不需要；不照搬云多副本部署方案 |
| Relay/Mac | 尚无运行实现；本步骤不需要安装未发布的 Relay 或改 Mac |
| 模型配置迁库/调用 Key 切换 | 另一步处理；不要为了日志一次性改动已有 80 条映射、WebUI/Hermes 调用 Key |

日志分三类：**请求用量/费用**是现在要保存的；**Docker 运行日志**是诊断，不作为账本；**输入/回答正文**保持关闭，D-04 未确认。关闭 SpendLogs 正文不意味着所有错误、元数据或既有第三方回调都已完成脱敏，仍要核对并避免客户端把秘密/正文塞进自定义 metadata。

### 0.2 按顺序操作（在 NAS 上执行，不在 Mac 项目目录执行）

1. 备份已有 Compose、模型 `config.yaml` 和受保护环境配置，记录正在运行的 LiteLLM 镜像摘要。当前目录没有原 Compose 附件，以下是合并指引，不是重建原文件；不要求重新提交秘密或路径。
2. 按第 2 节新增 `postgres`、密码文件和命名卷到同一个 Compose，保留已有 `ai-network` 定义。使用 `image: postgres:latest`，不要求为补丁/主版本更新修改 Compose 的镜像字段；首次部署记录实际版本/digest，后续更新遵循 1.2，不把 latest 视为数据自动升级。管理员初始化密码与业务数据库密码分开生成；初始化文件在 NAS 本地保存，不发送到聊天。
3. 只启动 PostgreSQL：`docker compose up -d postgres`；确认健康后按第 3 节创建 `litellm_app`/`litellm`。`relay_app`/`relay` 可留到本项目服务实施时创建，不是日志开始工作的前提。
4. 向当前 LiteLLM 容器**实际收到的环境**传入下列变量。NAS 的受保护 `.env` 供 Compose 替换，不一定自动注入容器；原服务用了 `env_file` 则核对传入，没有则在原 `environment` 里追加映射。不要重复创建第二个同名 LiteLLM 服务或替换原服务其他环境。

```dotenv
DATABASE_URL=postgresql://litellm_app:<URL编码后的业务密码>@postgres:5432/litellm
# 已有 LITELLM_MASTER_KEY 保留原值，不重新生成。
LITELLM_SALT_KEY=sk-<独立随机值，设置后固定保存>
STORE_PROMPTS_IN_SPEND_LOGS=False
```

如需显式传入，**合并**到原 LiteLLM 服务的 `environment`（服务名以原 Compose 为准）：

```yaml
environment:
  DATABASE_URL: ${DATABASE_URL:?设置LiteLLM业务数据库连接}
  LITELLM_SALT_KEY: ${LITELLM_SALT_KEY:?设置并保存固定加密依赖}
  STORE_PROMPTS_IN_SPEND_LOGS: "False"
  # 原有 LITELLM_MASTER_KEY、URL/Key 等 environment/env_file 保留。
```

5. 修改 NAS 上已挂载的 LiteLLM `config.yaml` 原文件，保留 `model_list`、锚点、`drop_params` 等已有设置；以下字段**合并**到已有同名块，不建立两个 `general_settings` 或 `litellm_settings`。该文件在容器内是只读挂载，应修改挂载源文件而不是容器内 `/app/config.yaml`。

```yaml
general_settings:
  disable_spend_logs: false           # 保存逐请求用量/费用记录
  disable_spend_updates: false        # 保持 Key/User/Team 消费更新
  store_prompts_in_spend_logs: false  # 不启用输入/回答正文存储

litellm_settings:
  turn_off_message_logging: true      # 抑制日志回调中的消息/回答内容
  # 原有 drop_params 等配置继续保留。
```

`STORE_MODEL_IN_DB=True` 是数据库模型管理选项，**不是用量日志开关**；本阶段不必开启，不迁模型。若此前已启用则保留并核对，不因为本指引重复开关。后续需要模型管理时按第 4/6 节推进；Salt Key 提前固定是为了后续凭据加密，不是正文加密或日志开关。

6. 验证 Compose 语法且不输出展开的秘密：`docker compose config --quiet`。待数据库角色就绪、镜像迁移能力核对后，在维护窗口只重建原 LiteLLM 服务：`docker compose up -d --no-deps --force-recreate <实际LiteLLM服务名>`；不要重新拉最新 LiteLLM 顺带升级，不操作其他服务，不执行 `down -v`。若已设置 `DISABLE_SCHEMA_UPDATE=true` 而未执行独立初始化/迁移，先按固定版本完成迁移，不用关掉校验来掩盖缺表。
7. 按 0.3 检查数据库与日志。失败先恢复原配置/同一镜像，保留新增数据库卷，不删卷重来。接通数据库之后未来若已使用虚拟 Key，不能把移除 `DATABASE_URL` 当作无损通用回滚。

### 0.2.1 可直接执行的首次配置步骤（管理员自行操作）

以下命令在 NAS 上、**原 Compose 所在目录**执行；保持原项目名与原 `-f` 参数。UGOS Docker 界面中的项目编辑/容器终端可完成相同操作，但当前未实测其导入、Secret 和 `.env` 处理；不能假定上传一份 YAML 就会上传旁边的文件。

1. 先保存现有 Compose、LiteLLM 配置、环境文件与当前镜像信息。在原 Compose 所在目录创建 `secrets` 文件夹，其中创建 `postgres-init.txt`：仅写一行强随机管理员密码，不加引号、不写 `POSTGRES_PASSWORD=`。限制该文件和环境文件的访问权限。可用密码管理器生成；建议管理员密码、`litellm_app` 密码、Salt Key 三者独立，不发到聊天。业务密码使用 32 位以上随机字母/数字或十六进制可避开 URI 保留字符，但不要使用可猜测的重复字符串。
2. 合并第 2 节 PostgreSQL 片段。只在**原有** `services`、`secrets`、`volumes` 块追加对应内容，不创建重复顶层块；`postgres` 与 LiteLLM 都接入原 `ai-network`。第 2 节的 Secret 文件默认相对原 Compose；NAS 界面若不保留该目录，需要在现场改成其真实可读路径，无须把路径或密码交给设计方。
3. 暂时只校验/启动 PostgreSQL，暂不填写 LiteLLM 的 `${DATABASE_URL:?…}` 必填映射；否则空变量会导致 Compose 校验提前失败：

```sh
docker compose config --quiet
docker compose pull postgres
docker compose up -d postgres
docker compose ps postgres
docker compose logs --tail=50 postgres
docker compose exec postgres psql -U postgres -d postgres -c 'SHOW server_version;'
```

只查看本地日志，不复制含秘密的错误载荷。这里的 `pull postgres` 用于首次空库安装；已有数据的日常更新不得未经第 1.2 节核对直接重跑。`config --quiet` 不输出展开后的连接串，但错误内容仍需脱敏。

4. 按第 3 节创建 `litellm_app` 和 `litellm`。在 `\password litellm_app` 提示时输入独立业务密码两次；SQL 完成后输入 `\q` 退出。再验证业务账号的 TCP 密码连接和建表权限，密码交互输入，不放到命令参数中：

```sh
docker compose exec postgres psql -h 127.0.0.1 -U litellm_app -d litellm -W
```

```sql
SELECT current_user, current_database();
BEGIN;
CREATE TABLE public.relay_setup_check (id integer);
ROLLBACK;
\q
```

建表验证是管理员明确执行的临时写入，事务回滚不留测试表；它不等于 LiteLLM schema 迁移已成功。

5. 保留原 Master Key，选择与原服务一致的环境注入方式：使用 Compose 读取的 `.env` 时加入 0.2 的变量，并追加 0.2 的 `environment` 映射；使用服务 `env_file` 时直接在该文件加入变量并保留原 `env_file`，不要再追加依赖 `${DATABASE_URL}` 的必填插值映射（服务 `env_file` 不是 Compose 插值来源）。核对 `environment` 没有同名旧值覆盖 `env_file`，并显式使正文环境开关为 False。若原 `environment` 是列表形式（`- NAME=value`），追加相同格式，不混合列表/字典。检查没有其他配置把 `STORE_PROMPTS_IN_SPEND_LOGS` 设为 True：官方说明 YAML 或环境**任意一个开启都会保存正文**，所以只把 YAML 设为 false 不够。不要新增/开启 `STORE_MODEL_IN_DB`，也不要新增 `DISABLE_SCHEMA_UPDATE=true`。
6. 合并 0.2 的 `config.yaml` 字段，然后只重建 LiteLLM。示例假设实际服务名为 `litellm`；如果原服务名称不同，仅替换最后的服务名：

```sh
docker compose config --quiet
docker compose up -d --no-deps --force-recreate --pull never litellm
docker compose ps litellm
docker compose logs --tail=100 litellm
```

这里不拉取新的 LiteLLM 镜像，避免把数据库接入与网关升级混在一起；保留当前已运行的 latest 系列镜像和原启动参数。首次连接可能需要初始化 schema；若有 Prisma/迁移/权限错误，先检查，不以 `SUPERUSER` 或删卷代替修复。
7. 按 0.3 验证：先检查无推理健康端点和 `db` 状态，随后用一次用户原本会进行的正常请求确认 Spend Log。不要点击 Run All Checks 或调用 `/health` 来验证数据库。先不创建新的调用 Key、不迁模型、不重建其他容器。

**中断处理**：如果连接失败影响现有请求，先用维护前保存的配置恢复 LiteLLM 新增环境/配置并只重建 LiteLLM，保留原镜像、Master Key 和 PostgreSQL 卷；这是本次新增空库接入的撤回步骤，不是后续有数据的数据库升级回滚流程。禁止 `docker compose down -v`，禁止靠删卷重装掩盖错误。

### 0.2.2 对应用户当前 Compose 的具体修改（2026-10-13）

用户当前配置：LiteLLM 使用 `ghcr.io/berriai/litellm:main-latest`；LiteLLM 和 Hermes 都通过 `env_file: [.env]` 读取同一个文件，Open WebUI 只接收其 `environment` 显式列出的变量。保留这一组织方式，不要求另建 `litellm-db.env`。Docker 支持多个服务共用环境文件；建议拆分凭据属于可选的最小权限改进，不是数据库接入的技术前提。共享连接串不表示其他应用会自动使用数据库，程序仍须明确实现数据库接入。

**Compose 修改 1：在已有 `services` 内加入 PostgreSQL。** 直接使用第 2 节的 `postgres` 服务，网络继续使用原有 `ai-network`；保持 `postgres:latest` 和数据卷挂载 `/var/lib/postgresql`。

**Compose 修改 2：在文件末尾加入顶层 Secret 和命名卷。** 若已有同名顶层块，只合并内容：

```yaml
secrets:
  postgres_init_password:
    file: ./secrets/postgres-init.txt

volumes:
  relay_postgres_data: {}
```

**Compose 修改 3：在原 `litellm` 服务内添加启动依赖。** 与 `image`、`ports`、`env_file` 同级：

```yaml
    depends_on:
      postgres:
        condition: service_healthy
```

该依赖使 Compose 启动 LiteLLM 前等待数据库健康；它不能代替创建业务账号和数据库，也不负责持续自动重启依赖者。首次仍先启动 PostgreSQL并按第 3 节建库，再重建 LiteLLM。

**Compose 修改 4：原 `litellm.environment` 列表追加正文关闭项。** 原 `LITELLM_MASTER_KEY`、`LITELLM_DROP_PARAMS`、时区、可选代理设置全部保留：

```yaml
      - STORE_PROMPTS_IN_SPEND_LOGS=False
```

`DATABASE_URL` 和 `LITELLM_SALT_KEY` 通过原有 `env_file` 注入，无需再追加 `${DATABASE_URL}` 或另建同名服务。镜像保持用户当前 `main-latest`，不为了接数据库改成另一个移动标签。

**共享 `.env` 追加：**

```dotenv
DATABASE_URL=postgresql://litellm_app:<URL编码后的业务密码>@postgres:5432/litellm
LITELLM_SALT_KEY=sk-<独立随机值，固定保存>
STORE_PROMPTS_IN_SPEND_LOGS=False
```

Hermes 在下次容器重建时也会收到这些变量，修改文件不会即时更新已运行容器。是否被其他程序使用取决于其自身实现，不能把共享环境文件当作跨应用数据库合同；后续 Relay 使用自己的 `relay` 库和账号，不复用 LiteLLM 私有库。当前不用给其他服务新增 `env_file`，也不用为接数据库重建 Hermes/Open WebUI。

**配置文件修改：** 在 NAS 的 `/volume4/docker/ai-platform/litellm/config.yaml` 合并 0.2 的日志配置，不删除原模型、锚点或其他设置。创建 Secret 文件后按 0.2.1 的顺序启动数据库、交互建库、重建 LiteLLM、检查 readiness 与新日志。

`version: '3.8'` 在现代 Compose 中已属于旧声明；可移除以消除提示，但与本次数据库接入无关，不需要借此重写现有网络/服务。

本节依据用户 2026-10-13 粘贴的配置及 [Docker 环境文件](https://docs.docker.com/compose/how-tos/environment-variables/set-environment-variables/)、[启动顺序](https://docs.docker.com/compose/how-tos/startup-order/)与第 8 节官方配置资料；尚未在 NAS 执行。

### 0.3 怎么判断这一步完成

| 检查 | 通过条件 | 不应据此宣称什么 |
|---|---|---|
| PostgreSQL | 容器健康，业务角色能连接 `litellm` 库 | `pg_isready` 不证明 LiteLLM 表/schema 已就绪 |
| LiteLLM 无推理就绪 | 访问 `http://<NAS地址>:4000/health/readiness`；安装版本响应指明 DB 已连接 | 仅 HTTP 200 不够，未配置 DB 时也可能就绪；不用 `/health` 或 Run All Checks，否则可能逐模型收费 |
| 逐请求记录 | 用自己本来就会进行的一次正常、非敏感请求，或明确选择的小额测试；随后在管理 UI 的 Logs/Usage 或安装版本 `/spend/logs/v2` 查询到新请求 | 不自动发付费测试，空库在尚无请求时为空是正常；内存/批量写入可能使可见记录稍有延迟 |
| 字段/费用 | 模型、时间、可得的 Token/状态等与请求一致，费用与已验证价格来源对照 | 自定义别名/中转渠道可能无正确价格；零费用不证明免费，不能仅靠日志开启就认定价格和质量已验证 |
| 隐私与范围 | 测试记录不含输入/回答正文，既有回调/metadata/错误脱敏单独核对 | 本指引不授权完整正文采集、不覆盖未经过 LiteLLM 的直连请求，也不能补回此前未存的历史日志 |

若数据库连接失败，优先检查共享网络中的服务名 `postgres`、角色密码/URL 编码、容器实际环境；若已连库而无记录，检查两项 disable 开关、schema/权限、写入错误、请求是否确实走这个实例和认证路径。不要粘贴完整环境/日志载荷，只反馈脱敏错误类别。

### 0.4 源日志保留（可选，先核对安装版本）

按当前公开官方指引，SpendLogs 保留/清理无企业许可要求，单实例无需为了清理新增 Redis；旧安装版本行为仍须核对。若此数据库为本次空库且安装版本支持，可在同一个 `general_settings` 内追加：

```yaml
  maximum_spend_logs_retention_period: "180d"
  maximum_spend_logs_retention_interval: "1d"
```

这会清理 LiteLLM **源日志**，不是 Relay 已设计的 180 天元数据/2 年聚合能力；Relay 采集尚未实现前不能宣称已生成长期聚合。若后来在非空库缩短保留期，先预览影响/备份，另行确认删除；如版本不支持暂不加字段并记录缺口，不能自动删除整库。首次日志接通可先不加此项，不承诺默认已有清理。

## 1. 准备与安全边界

> 说明：本节提到的持久化目录、Compose 工作目录和备份目标，是部署实施时必须落地的责任边界，不是要求用户现在把 NAS 路径交给设计阶段。Docker 容器可重建而数据不能随容器消失，因此实施时必须在已有 Compose 中明确卷/目录、权限、备份与恢复方式；项目不读取或管理 Lucky 配置。

1. 保存现有 Compose、LiteLLM `config.yaml`、受保护环境文件和镜像 digest；秘密与普通配置分开备份，不能贴到聊天/日志。
2. 明确维护窗口、允许重启的服务及回滚版本；数据库连接配置修改可能需要重建 LiteLLM 容器，不能保证零中断。
3. 拟交付 Compose 默认使用 Docker 命名卷，不要求指定数据库宿主机目录；若已有 Compose 使用目录挂载，则保留其选择，不擅自搬迁。路径和备份目标由部署者在 NAS 现场决定并记录，不需要发送给设计方；必须保证重启/升级不丢数据，并能按指引恢复，不覆盖已有数据目录。
4. 2026-10-12 用户已选择 `postgres:latest`，替代此前 17 系列/固定版本建议；无需修改镜像字段追踪更新。每次安装/更新仍记录解析到的主版本与 digest，以核对兼容和保留恢复依据；不自动拉取重建数据库。若复用已有兼容 PostgreSQL，跳过建容器，仍需独立库/角色。
5. 共用一个 PostgreSQL 实例时创建 `litellm` 和 `relay` 两库、两业务账号；Relay 不读写 LiteLLM 私有表。数据库共享故障域会影响两个应用，恢复演练需覆盖。

### 1.1 为什么保留持久化与备份说明

| 项目 | 实际用途 | 当前需要用户提供吗？ | 拟交付处理 |
|---|---|---|---|
| 数据库持久化 | 容器删除/重建后保留账本、任务和配置；不能只写容器临时层 | 不需要 NAS 路径 | 默认命名卷；已有目录挂载按原 Compose 保留，升级沿用同一数据卷 |
| Relay 工作目录 | 区分镜像内程序目录与宿主机放 Compose/受保护配置的位置 | 不需要专门创建或提交业务工作目录 | 程序随镜像交付；配置按已有 Compose 组织，业务事实保存在 relay 库 |
| 备份目标 | 发生数据库损坏、误操作或升级失败时恢复数据 | 设计阶段不需要位置 | 管理员部署时选择并记录目标、保留周期与恢复步骤；同盘副本不能覆盖整机/磁盘故障 |

Docker 命名卷的内容独立于容器生命周期；卷保留不等于已有数据库备份。依据见第 8 节 Docker 官方卷说明。

### 1.2 latest 策略与数据库更新边界（用户选择，2026-10-12）

- Compose 统一使用 `image: postgres:latest`，不固定 digest 到镜像字段；安装/更新报告保存真实版本和摘要，作为兼容核对和恢复的证据，不是要求用户维护一张版本表。
- `latest` 省去手改镜像版本号，**不代表运行中的容器会自动升级**；拉取只下载镜像，生效要另行重建。用户的标签选择不授权后台自动更新，不加 Watchtower 等无人值守数据库更新组件。
- 初装：只在确认全新数据卷时按第 2 节启动。当前没有 NAS 初始化记录；是否已用 17 初始化需要实际操作前检查，不把本项目空数据需求推断成 NAS 库一定为空。
- 后续更新：先核对旧实例与目标镜像主版本、备份数据库/秘密并保留旧镜像 → 同主版本按维护窗口重建/读回验证；主版本变化则停止普通重建流程，先演练 `pg_upgrade` 或导出/恢复，并核对 LiteLLM 迁移/查询/权限。不把新主版本直接挂旧数据目录，也不另建空库冒充升级完成。
- 官方 18 及以上镜像用 `/var/lib/postgresql` 卷根与版本化 `PGDATA`；17 及以下默认使用 `/var/lib/postgresql/data`。采用卷根不会自动转换旧数据格式，跨主版本仍需迁移；已有卷保留、禁止删卷或覆盖文件来解决启动失败。
- 回滚使用保留的旧镜像与匹配数据库/秘密依赖；`latest` 不能定位旧版本。需要回滚时允许临时引用已记录的旧标签/digest，不保证“Compose 永远任何字段都不用改”。前提是升级失败可保留数据，而不是标签永远不动。

依据：PostgreSQL 官方镜像、数据库升级和 Docker Compose pull 文档，见 8.2。这里只修正文档策略，未执行拉取/重建/数据库升级。

## 2. 增加 PostgreSQL（拟推荐的命名卷合并片段）

以下只展示新增服务的合并片段，不是用户原 Compose 的完整重建，也未在 UGOS PRO 执行。片段用于全新数据库，按当前官方 18 及以上镜像的目录布局挂载；已有 17 或更早数据卷不能直接套用，也不保证未来 latest 的布局永远不变；不要同时覆盖/重新声明用户已有的 `ai-network`。已有网络名需核对 Compose 实际创建的名称；跨 Compose 复用时才将网络声明为 `external: true`。

```yaml
services:
  postgres:
    image: postgres:latest
    restart: unless-stopped
    environment:
      POSTGRES_USER: postgres
      POSTGRES_DB: postgres
      POSTGRES_PASSWORD_FILE: /run/secrets/postgres_init_password
    secrets:
      - postgres_init_password
    volumes:
      - relay_postgres_data:/var/lib/postgresql
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
    file: ./secrets/postgres-init.txt

volumes:
  relay_postgres_data: {}
```

- 管理员在 NAS 本地创建强随机初始化密码文件，仅允许必要账号读取，不提交 Git。Compose 文件挂载并不是加密保险库；宿主机管理者可能读取秘密。
- 保持原 Compose 项目名；未显式设置 `name` 的命名卷默认带项目名前缀，后续升级沿用同一项目/卷，不另建同名导入项目导致出现空库。确认卷没有与现存不相关数据库冲突；若改用目录挂载，另外核对路径和镜像用户权限。再只启动此服务，例如在实际 Compose 工作目录执行 `docker compose up -d postgres`。命令是执行指引，不代表当前工具已经运行。
- 命名卷由 Docker 管理，不需要把它的宿主机目录提供给项目；后续不能随意更换卷名或用 `down -v` 清卷。若需要目录挂载，在部署时改为 `${POSTGRES_DATA_DIR}:/var/lib/postgresql` 并检查权限，不同时使用两种挂载覆盖同一目录。
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
\q
```

**当前停在这里，不必创建 Relay 数据库。** 本项目服务实施时再进入 psql，执行下面的独立步骤；不是本次日志接通的前提：

```sql
CREATE ROLE relay_app LOGIN;
\password relay_app
CREATE DATABASE relay OWNER relay_app;
REVOKE CONNECT ON DATABASE relay FROM PUBLIC;
GRANT CONNECT ON DATABASE relay TO relay_app;
\q
```

`\password` 是 psql 交互输入，避免把业务密码写成 SQL 示例或命令行参数。两个业务角色不是超级用户；数据库所有者只负责自己的库。后续按实际迁移权限验证 schema 创建/读取，不无条件给 `SUPERUSER`。生产可进一步分开迁移角色与运行角色，最终权限由实际版本测试确认。

## 4. 数据库与加密依赖（模型管理选项另行启用）

首次只接日志按 0.2/0.2.1；本次实际配置如下，所有值在 NAS 本地受保护环境中配置（下列不是可用真实值）：

```dotenv
DATABASE_URL=postgresql://litellm_app:<URL编码后的数据库密码>@postgres:5432/litellm
LITELLM_MASTER_KEY=<保留原有值，不重新生成>
LITELLM_SALT_KEY=sk-<新生成并安全保存的固定加密依赖>
STORE_PROMPTS_IN_SPEND_LOGS=False
```

`STORE_MODEL_IN_DB=True` 留到第 6 节数据库模型管理切换时另行开启；不是日志必要开关，本次不要加入。

- `postgres` 必须替换为 LiteLLM 容器网络可解析的实际数据库服务名/地址；不要在容器里使用宿主机的 `localhost` 代指 PostgreSQL 容器。
- 密码包含保留字符时必须正确 URL 编码；不要把连接串打印到普通日志。
- 若原 Compose 使用 `env_file`，确认本阶段所需变量被传入 LiteLLM；如果另有 `environment` 同名项，其覆盖顺序必须核对。不能假定放进目录里的 `.env` 自动成为所有容器环境。
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

### 8.1 本轮数据库/日志核对（2026-10-11）

只读核对 [配置字段](https://docs.litellm.ai/docs/proxy/config_settings)、[用量日志/分页](https://docs.litellm.ai/docs/proxy/cost_tracking)、[日志保留](https://docs.litellm.ai/docs/proxy/spend_logs_deletion)、[生产数据库/镜像与 Salt Key](https://docs.litellm.ai/docs/proxy/deploy)、[无推理就绪检查](https://docs.litellm.ai/docs/proxy/health)和 [PostgreSQL 官方镜像](https://hub.docker.com/_/postgres)。已确认公开文档中区分请求日志、消费更新、正文及数据库模型开关；不是 NAS 已部署证据，也不是完整 WP-0 完成记录。文档可能领先于用户实际 digest，执行前保留版本和脱敏验证结果。

### 8.2 latest、正文环境开关与操作核对（2026-10-12）

核对 [PostgreSQL 官方镜像/PGDATA](https://hub.docker.com/_/postgres)、[数据库主版本升级](https://www.postgresql.org/docs/current/upgrading.html)、[Compose pull](https://docs.docker.com/reference/cli/docker/compose/pull/)。按用户选择使用 latest；卷挂载示例适用于当前 18+ 布局，并非对未来镜像或 NAS 已有数据状态的实测保证。

本轮再次核对 PostgreSQL 官方镜像当前 tags（`latest` 对应 18.6，后续可能变化）、[Compose Secret 文件注入](https://docs.docker.com/compose/how-tos/use-secrets/)、[LiteLLM 环境配置](https://docs.litellm.ai/docs/proxy/config_settings)中 `STORE_PROMPTS_IN_SPEND_LOGS` 的开启语义和 [Salt Key/镜像工具链](https://docs.litellm.ai/docs/proxy/deploy)。仅完成公开资料与文档核对，UGOS 项目导入、数据库初始化和 LiteLLM 实际日志均未执行。
