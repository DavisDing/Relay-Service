# UGOS PRO · PostgreSQL 与 LiteLLM 用量日志操作手册

更新日期：2026-10-13。依据用户当天贴出的 Compose 和公开官方文档整理；尚未在 NAS 执行。

本次操作结果：现有 LiteLLM 接入 PostgreSQL，保存经过该网关的逐请求用量记录；输入和回答正文关闭。沿用共享 `.env`、现有模型配置、Master Key、网络和端口。PostgreSQL 使用 `postgres:latest`；LiteLLM 保留当前 `ghcr.io/berriai/litellm:main-latest`。

只新增 PostgreSQL 容器，无须安装 Redis、Langfuse、pgAdmin、宿主机 PostgreSQL 或 Python。

## 1. 准备原文件和密码

使用 NAS 上原有 Docker 项目；后面的命令均在原 `compose.yaml` 所在目录执行，沿用原项目名。如果原项目通过 `-p` 或 `-f` 指定名称/文件，下面每条 Compose 命令也使用相同参数。不要新建一个不同名称的项目来替代原项目。

先保存以下文件的副本，标记为接入数据库前的版本：

- 原 `compose.yaml`。
- 原共享 `.env`。
- `/volume4/docker/ai-platform/litellm/config.yaml`。

记录当前 LiteLLM 的镜像 ID，供撤回时定位原镜像：

```sh
docker inspect litellm --format '{{.Image}}'
```

生成三份相互独立的随机值，建议用密码管理器生成并保存：

| 内容 | 使用位置 |
|---|---|
| PostgreSQL 管理员密码 | `secrets/postgres-init.txt` |
| LiteLLM 业务数据库密码 | 建库时的 `\password` 与 `.env` 中 `DATABASE_URL` |
| LiteLLM Salt Key | `.env` 中 `LITELLM_SALT_KEY`，以 `sk-` 开头 |

业务密码建议采用足够长的随机字母/数字或十六进制值，例如 64 位随机十六进制字符，避免连接串编码问题。若用特殊字符，需要只对连接串中的密码部分做 URL 编码，不编码整条连接串。

在原 Compose 同级创建 `secrets` 文件夹，再创建 `secrets/postgres-init.txt`。文件只放管理员密码，一行，不加引号，不写变量名。限制密码文件和 `.env` 的读取权限；真实值只保存在 NAS 和受保护的密码备份中。

## 2. 修改 Compose

### 2.1 在原 `services` 下加入 PostgreSQL

`postgres` 与 `ollama`、`litellm` 等服务同级；不要添加第二个 `services:`。

```yaml
  postgres:
    image: postgres:latest
    restart: unless-stopped
    environment:
      POSTGRES_USER: postgres
      POSTGRES_DB: postgres
      POSTGRES_PASSWORD_FILE: /run/secrets/postgres_init_password
      TZ: Asia/Shanghai
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
```

不添加 `ports`；LiteLLM 在 Docker 内通过 `postgres:5432` 连接。当前 PostgreSQL 18+ 镜像的数据卷根目录是 `/var/lib/postgresql`；这里用于首次新建数据库，已有 17 或更早数据不能直接套用。不要手动指定旧版 `PGDATA`。

### 2.2 用下面内容替换原 `litellm` 服务块

新增的配置是 PostgreSQL 健康依赖和正文环境开关；原 `env_file` 继续读取共享 `.env`。如果原服务已启用代理，保留原代理值。

```yaml
  litellm:
    image: ghcr.io/berriai/litellm:main-latest
    container_name: litellm
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "4000:4000"
    volumes:
      - /volume4/docker/ai-platform/litellm/config.yaml:/app/config.yaml:ro
    command:
      - "--config"
      - "/app/config.yaml"
      - "--port"
      - "4000"
    env_file:
      - .env
    environment:
      - LITELLM_MASTER_KEY=${LITELLM_MASTER_KEY}
      - LITELLM_DROP_PARAMS=True
      - STORE_PROMPTS_IN_SPEND_LOGS=False
      - TZ=Asia/Shanghai
      # 原来已使用的 HTTP_PROXY / HTTPS_PROXY 在这里继续保留。
    networks:
      - ai-network
```

`depends_on` 在 Compose 管理启动时等待 PostgreSQL 健康，但不负责创建业务库，也不是运行期间的数据库故障恢复机制。首次操作仍按第 3～4 节单独启动和建库。

### 2.3 在文件末尾加入顶层配置

与原 `networks:`、`services:` 同级，没有前导缩进。已有顶层 `secrets` 或 `volumes` 时只合并条目。

```yaml
secrets:
  postgres_init_password:
    file: ./secrets/postgres-init.txt

volumes:
  relay_postgres_data: {}
```

原 `ai-network`、Ollama、Open WebUI、Hermes、workbuddy2api 配置继续使用。顶部 `version: '3.8'` 可暂时保留；现代 Compose 的 obsolete 提示不代表配置失败。

## 3. 首次启动 PostgreSQL

此时暂不重建 LiteLLM；先让数据库就绪。

```sh
docker compose config --quiet
docker compose pull postgres
docker compose up -d --no-deps postgres
docker compose ps postgres
docker compose logs --tail=50 postgres
docker compose exec postgres psql -U postgres -d postgres -c 'SHOW server_version;'
```

`config --quiet` 成功通常不输出内容；查看容器达到 healthy，版本查询成功后继续。首次初始化需要一点时间，尚在 starting 时再检查，不马上判定失败。`pg_isready` 只证明服务接受连接，业务密码和 LiteLLM 表结构还要验证。

这里的 `pull postgres` 是首次空库安装步骤。以后已有数据时更新镜像，按第 9 节处理。

若 Secret 文件报错：确认文件存在、路径相对原 Compose 正确、NAS Docker 能读取。若 UGOS 项目编辑界面把 YAML 复制到另一目录，应将 `file` 改为该密码文件在 NAS 的真实绝对路径；不需要把路径或密码发回聊天。不要通过放开全部目录权限或删卷解决错误。

## 4. 创建 LiteLLM 数据库与业务账号

进入数据库交互终端：

```sh
docker compose exec postgres psql -U postgres -d postgres
```

逐条执行，下列仅用于首次创建：

```sql
CREATE ROLE litellm_app LOGIN;
\password litellm_app
CREATE DATABASE litellm OWNER litellm_app;
REVOKE CONNECT ON DATABASE litellm FROM PUBLIC;
GRANT CONNECT ON DATABASE litellm TO litellm_app;
\q
```

`\password` 提示时输入准备好的业务密码两次；不要输入管理员密码。库/角色已存在时先核对，不删除或重复创建。当前不需要创建 Relay 数据库。

再检查业务密码和建表权限：

```sh
docker compose exec postgres psql -h postgres -U litellm_app -d litellm -W
```

通过服务名连接容器网络地址，检查密码认证；不要使用本地 socket 或可能设为 trust 的回环地址作为密码验证。按提示输入业务密码，执行：

```sql
SELECT current_user, current_database();
BEGIN;
CREATE TABLE public.relay_setup_check (id integer);
ROLLBACK;
\q
```

应返回 `litellm_app` 与 `litellm`，建表成功后回滚，不留测试表。这不等于 LiteLLM 的 schema 已初始化。

## 5. 修改共享 `.env` 与 LiteLLM 配置

### 5.1 在原 `.env` 追加以下内容

尖括号内容全部替换为自己的值，不保留尖括号。保留原文件其他变量和原 `LITELLM_MASTER_KEY`。

```dotenv
DATABASE_URL=postgresql://litellm_app:<业务密码>@postgres:5432/litellm
LITELLM_SALT_KEY=sk-<独立随机值并固定保存>
STORE_PROMPTS_IN_SPEND_LOGS=False
```

如果 `.env` 已有同名变量，修改原值，不再追加重复项。密码必须与第 4 节设置的一致，数据库地址使用 `postgres`，不使用 `localhost`。

无需把 `DATABASE_URL` 和 Salt Key 再写进 `litellm.environment`：原 `env_file: .env` 已经注入它们。LiteLLM 和 Hermes 在重建时都会收到共享文件中的变量；Open WebUI 按原 `environment` 接收变量。保留这一共享方式，不需要另建 `litellm-db.env`，也不需要给全部服务新增 `env_file`。

Salt Key 用于数据库供应商凭据加密，设置后固定保存。当前不加入 `STORE_MODEL_IN_DB=True`，模型仍来自原 YAML。也不要新增 `DISABLE_SCHEMA_UPDATE=true`，以免独立迁移尚未执行时跳过初始化。

### 5.2 修改 NAS 上实际的 LiteLLM 配置文件

文件为 `/volume4/docker/ai-platform/litellm/config.yaml`。保留原模型列表、锚点、Master Key 设置和其他参数；只把以下字段合并到已有同名块。不要出现两个 `general_settings` 或两个 `litellm_settings`。

```yaml
general_settings:
  disable_spend_logs: false
  disable_spend_updates: false
  store_prompts_in_spend_logs: false

litellm_settings:
  turn_off_message_logging: true
```

这些配置保存逐请求用量和消费更新，关闭 SpendLogs 的输入/回答正文和回调的消息/回答内容。YAML 与环境变量必须都没有启用正文，因为任意来源设为 True 都可能开启存储。自定义 metadata、错误信息和其他应用自己的聊天历史仍需分别检查。

## 6. 应用 LiteLLM 配置

数据库健康、业务账号验证成功后，执行：

```sh
docker compose config --quiet
docker compose up -d --no-deps --force-recreate --pull never litellm
docker compose ps litellm
docker compose logs --tail=100 litellm
```

容器重建会短暂停止网关，选择当前没有请求的时间操作。不要用 `docker compose restart litellm` 代替：重启已有容器不会重新注入修改后的环境文件。

`--pull never` 防止本次下载新 LiteLLM 镜像，但使用的是本地该标签指向的镜像。若本机曾另外拉取过新 `main-latest`，应先确认它与第 1 节记录的运行镜像相同；需要保持原镜像时临时把 `image` 设为已记录的原镜像 ID。

首次启动可能创建或迁移表。若出现 Prisma、迁移或缺表错误，先确认当前 LiteLLM 镜像和启动迁移配置；官方单体镜像包含数据库工具链，不需要在 NAS 宿主机安装 Python/Prisma。不要把角色改成超级用户或删卷重来。

## 7. 验证数据库与新用量记录

浏览器访问（将 `<NAS地址>` 替换为自己的地址）：

```text
http://<NAS地址>:4000/health/liveliness
http://<NAS地址>:4000/health/readiness
```

进程端点应成功；readiness 需要确认数据库已连接，不能只凭 HTTP 200 判断。当前官方响应示例为：

```json
{"status":"healthy","db":"connected"}
```

安装镜像返回字段不同时以实际版本为准；显示 `Not connected` 不能当成数据库接入成功。

不要为了验证数据库调用 `/health` 或点击 `Run All Checks`，这两种模型健康检查可能对模型发起付费请求。

随后用 Open WebUI 或原客户端进行一次本来就需要的正常、非敏感请求，确保选的是走 LiteLLM 的云端模型；直连 Ollama 的请求不会因此进入 LiteLLM 日志。进入现有 LiteLLM 管理页面的 Logs/Usage，确认新记录。

也可在本机管理端或受保护的 API 客户端使用管理权限查询 `/spend/logs/v2`，具体字段以该实例 `/docs` 为准；不要将 Master Key 填进浏览器 URL 或共享命令。

完成判定：

- PostgreSQL 健康，业务账号可连接。
- LiteLLM 数据库就绪、原客户端请求正常。
- 新请求在 Logs/Usage 可见；异步批量写入时等待短暂延迟后刷新。
- 模型、时间、Token 等可得字段与请求一致。
- Spend Log 不含输入/回答正文。

自定义别名/中转模型可能缺少正确价格，费用为 0 不证明免费。接通日志不会补回此前未保存的历史请求，数据库记录也不等于供应商余额已经接入。

## 8. 出错处理与撤回

| 现象 | 优先处理 |
|---|---|
| Secret 不存在/不可读 | 检查 NAS 文件路径、项目目录与 Docker 读取权限 |
| PostgreSQL 仍是 starting | 等待初始化完成，再看日志；失败时保留数据卷 |
| 数据库认证失败 | 核对业务账号密码、连接串编码；使用第 4 节交互登录复查 |
| 不能解析 `postgres` | 确认数据库与 LiteLLM 都在原 `ai-network` |
| readiness 显示未配置数据库 | 核对 `env_file` 指向原 `.env`、同名环境覆盖以及是否重建容器 |
| Prisma/schema/缺表错误 | 检查实际镜像、初始化迁移流程和业务权限 |
| 正常请求成功但无日志 | 确认流量走该 LiteLLM、日志开关、写入错误和批量延迟；查实际请求时间窗口 |

本次首次接入导致原调用失败时，用第 1 节副本恢复 Compose、共享 `.env` 和 LiteLLM 配置，使用原镜像只重建 LiteLLM。PostgreSQL 数据卷保留。这是首次接入的撤回步骤；以后虚拟 Key 或模型已依赖数据库时，不能把删除连接串当成通用回滚。

禁止执行 `docker compose down -v` 或删除数据库卷来解决错误。备份的配置和数据库导出分别保存，密码、Salt Key 与原镜像一起保留；反馈问题时只提供脱敏错误类别。

## 9. 后续使用 latest 更新

继续使用 `postgres:latest`，安装时记录 `SHOW server_version` 和实际镜像；保留镜像不会自动完成数据库主版本迁移。未来更新先备份数据库、账号和秘密，核对旧/新主版本；同主版本按维护流程重建，跨主版本先执行 `pg_upgrade` 或导出/恢复，不把旧数据直接挂到新版本启动。保持同一个 Compose 项目与数据卷。

LiteLLM 更新也应与首次数据库接入分开，检查 schema 迁移、日志和原客户端请求。后续 Relay 库、模型管理迁库、虚拟 Key 切换以及正文范围按需求与设计的各自步骤推进。

## 10. 官方核对与实际验证范围

2026-10-13 查阅：

- [PostgreSQL 官方镜像与 PGDATA](https://hub.docker.com/_/postgres)。
- [PostgreSQL 主版本升级](https://www.postgresql.org/docs/current/upgrading.html)。
- [Docker Compose 环境文件](https://docs.docker.com/compose/how-tos/environment-variables/set-environment-variables/)。
- [Docker Compose 启动依赖](https://docs.docker.com/compose/how-tos/startup-order/)。
- [LiteLLM 配置与正文环境开关](https://docs.litellm.ai/docs/proxy/config_settings)。
- [LiteLLM 数据库、Salt Key 和镜像工具链](https://docs.litellm.ai/docs/proxy/deploy)。
- [LiteLLM 无推理健康检查](https://docs.litellm.ai/docs/proxy/health)。
- [LiteLLM 用量日志](https://docs.litellm.ai/docs/proxy/cost_tracking)。

本手册中的 YAML 示例可以做静态语法核对；NAS Secret 支持、数据库初始化、schema、日志、费用和恢复仍需在执行后逐项验证。没有连接 NAS、读取真实秘密或发起付费调用。
