# NAS Docker 开发预览部署

本轮交付 Go 服务端与已嵌入的 React 页面，以及新建 Relay 自有 PostgreSQL 的 Compose。属于 M1 基础监控切片；CI 已通过镜像构建及无数据库的 amd64 基础运行检查；真实数据库/权限/迁移、完整容器启动和 UGOS Projects 未验收。六服务的接口前提见 [COMPATIBILITY.md](../COMPATIBILITY.md)。调用 Key、模型路由写入、付费探针、账本余额、正文、原生客户端和镜像更新不在本轮代码范围。

## 1. 准备部署目录和配置

当前自行测试默认使用 `ghcr.io/davisding/relay-service:latest`（REQUIREMENT D-16）。从本仓库取完整 `deploy/nas/` 目录，保留 `docker-compose.yaml`、`.env.example`、`generate-env.sh` 和 `init-db/001-roles.sh`；直接拉取镜像无需安装 Go/Node。源码 Compose 和环境模板默认使用 `latest`，运行与迁移两项 Relay 服务共用 `RELAY_IMAGE`。`main` 通过 CI 后才更新该测试标签；稳定后才发布正式版。

版本发布流程另提供 `relay-service-vX.Y.Z-nas.tar.gz`，完整解压到同一文件夹后即可使用。版本包内环境模板固定该次构建的 `sha256` 摘要，不随测试渠道更新。已有版本包若希望跟随 `latest`，仅将现有 `.env` 的 `RELAY_IMAGE` 改为 `ghcr.io/davisding/relay-service:latest`，保留原秘密，然后按第 4 节升级；不要重新生成并替换已有数据库密码或加密主密钥。

GitHub Release 另附 `docker-compose.yaml`、版本化环境模板、`release-metadata.json` 和 `SHA256SUMS`。单独下载 YAML 仍缺少初始化脚本，应使用完整压缩包。已发布 [v0.1.0-alpha.1](https://github.com/DavisDing/Relay-Service/releases/tag/v0.1.0-alpha.1)，包含完整 NAS 包和 GHCR 双架构镜像；自动化配置见 [RELEASE.md](RELEASE.md)。

```sh
# 在 deploy/nas/ 或解压后的完整包目录
sh generate-env.sh .env
```

生成脚本需要 `openssl`，使用 32 byte 随机数生成六项秘密，文件权限 600，拒绝覆盖现有文件。不输出秘密内容。也可从 `.env.example` 复制并自行填写，所有数据库密码使用 URL 安全字符（推荐 64 位 hex），避免数据库 URL 插值歧义。

| 变量 | 用途 |
|---|---|
| `RELAY_IMAGE` | 自行测试默认 `ghcr.io/davisding/relay-service:latest`；版本包默认对应 digest，运行与迁移使用同一镜像 |
| `POSTGRES_SUPERUSER_PASSWORD` | 此独立 PostgreSQL 的初始超级用户口令 |
| `RELAY_DB_PASSWORD` | `relay_owner` 数据库所有者，常规 Relay 进程不使用 |
| `RELAY_MIGRATOR_PASSWORD` | 显式迁移使用；常规容器不注入 |
| `RELAY_APP_PASSWORD` | 普通进程业务表读写权限，无建表权限 |
| `RELAY_ENCRYPTION_KEY` | 64 位 hex AES-256 主密钥；备份必须单独保管，丢失后需重配监控凭据 |
| `RELAY_BOOTSTRAP_TOKEN` | 首次创建管理员的一次性部署令牌；已初始化后再次提交会拒绝 |
| `AI_NETWORK_NAME` | 用户既有 Docker 网络，默认 `ai-network`，部署前应已存在 |

默认仅发布 `127.0.0.1:8787`。通过既有反代访问时，设置 `RELAY_PUBLIC_URL=https://实际域名`、`RELAY_SECURE_COOKIES=true`；公开 origin 必须精确匹配浏览器使用的协议/主机/端口，不包含路径。用户自行配置 Lucky/TLS。反代若作为其他容器运行，可在 `AI_NETWORK_NAME` 网络访问 `http://relay-service:8787`。需要 NAS 局域网直连时由部署者调整 `RELAY_BIND_ADDRESS`，并设置实际 public URL。

Relay 不挂载 Docker socket、其他容器目录或数据卷。PostgreSQL 仅在独立后端网络，不对宿主发布端口。初始脚本只用于**全新命名卷**，不会修改既有 PostgreSQL/LiteLLM 库；它不会在已有卷上重建角色，修改 `.env` 也不等于修改已有数据库口令。

## 2. 拉取和启动

在完整部署目录执行：

```sh
docker compose --env-file .env -f docker-compose.yaml config --quiet
docker compose --env-file .env -f docker-compose.yaml pull
docker compose --env-file .env -f docker-compose.yaml up -d
```

首次顺序为 PostgreSQL 健康 → `relay-migrate` 一次性显式迁移成功 → `relay` 启动。schema 不匹配、库不可用、已有活动 Relay 或迁移失败会阻止启动。一个数据库只允许一个 Relay 进程持有调度锁；不构成自动 HA。

UGREEN Docker Projects 导入 `docker-compose.yaml` 时必须保留同目录 `.env` 和 `init-db/001-roles.sh`，并配置既有外部网络。v0.1.0-alpha.1 的 GHCR digest 已核实支持匿名 manifest 拉取；预发布版本号不会阻止 Docker 拉取。`latest` 的可用性取决于测试发布工作流是否成功，NAS 仍须具备到 GHCR 的网络访问。未来私有包需要部署者预先认证。GUI 的依赖条件和环境文件处理尚未实测；不以文件可导入代替实际验收。

需要从源代码构建时，在仓库 `deploy/nas/` 使用独立覆盖文件：

```sh
RELAY_IMAGE=relay-service:local sh generate-env.sh .env
docker compose --env-file .env -f docker-compose.yaml -f docker-compose.local-build.yaml config --quiet
docker compose --env-file .env -f docker-compose.yaml -f docker-compose.local-build.yaml build
docker compose --env-file .env -f docker-compose.yaml -f docker-compose.local-build.yaml up -d
```

`.env` 若已存在，生成器会拒绝覆盖；已有配置可将 `RELAY_IMAGE` 调整为 `relay-service:local` 后保留原秘密。Docker 构建上下文为仓库根目录：Node 阶段根据锁文件构建页面，Go 阶段嵌入该产物并交叉编译，最终为非 root 的 scratch 镜像及 CA。无需先在宿主构建 Web。本地命令默认版本为 dev；Release 注入版本、源码修订与提交时间，可用 `docker run --rm 镜像引用 version` 查看 JSON。

浏览器访问 `RELAY_PUBLIC_URL`，用部署令牌创建管理员（密码至少 12 字符），再登录。初始化后可清空部署环境中的 bootstrap 令牌并重建 Relay；数据库初始化状态会阻止再次创建管理员。会话有效期 12 小时；登录连续失败 8 次锁定 5 分钟。

## 3. 配置服务与验证

添加服务时使用 Relay 容器可以解析和访问的地址，例如同网络中的 `http://litellm:4000`。`127.0.0.1` 指 Relay 容器自身。检查 URL 只允许协议、主机、端口；原 WebUI 地址单独填写。私网目标必须显式授权，metadata/link-local/多播等仍拒绝；不跟随重定向、不使用环境 HTTP 代理。监控凭据加密保存，不回显给 Web 或只读设备。修改目标地址时必须重新输入或显式清除旧凭据，防止把已有秘密发送给新目标。

固定 GET 检查不会发推理或工具请求。成功仅证明对应端点/进程，不证明模型可用。工作副本版本与接口未知时显示未知/不支持；认证失败与检查权限受限单独记录，不归为服务宕机。首次未检查、过期数据和旧配置结果不会当当前健康。

- `/health/live`：进程 HTTP 存活；容器 healthcheck 使用此入口。
- `/health/ready`：自有数据库/schema 就绪；上游故障不影响此入口。
- `/api/v1/info`：公开最小身份、初始化状态和版本；其他接口需管理员 Cookie 或配对设备令牌。
- 浏览器写操作需要配置 origin 与双提交 CSRF；原生设备配对为一次性 5 分钟码，设备凭据 180 天到期，可即时撤销，只能读取与更新自身告警已读位置。

部署前后逐项验证：初始化、重登、服务检查/历史、模拟故障与恢复、暂停与静默、设备配对/撤销、重启历史仍在、凭据仍能解密、两角色权限与单活锁。当前这些数据库/容器/NAS 场景均为未执行。

## 4. 更新、备份与恢复边界

保留用户选择的 `postgres:latest`；18+ 官方镜像使用命名卷挂到 `/var/lib/postgresql`。实际镜像 digest/主版本需由部署者记录。主版本升级需要 PostgreSQL 正式迁移，不能把换镜像视为自动完成；本地 PostgreSQL 源码构建不证明该镜像兼容。

更新 Relay 必须先备份并停常规进程，再显式迁移。跟随测试渠道时，保留 `.env` 的 `RELAY_IMAGE=ghcr.io/davisding/relay-service:latest`；固定版本升级则改为新 Release 摘要，均保留原秘密。`docker compose pull` 只下载镜像，需要重建 Relay 才会使用新版本。以下命令在同一部署目录执行，Compose 自动读取 `.env`；两项 Relay 服务共用本机已拉取的镜像，后续步骤不再次拉取；不要在升级 Relay 时顺带更新 PostgreSQL：

```sh
docker compose stop relay
docker compose pull relay
docker compose up -d --pull never postgres
docker compose run --rm --pull never relay-migrate
docker compose up -d --pull never --no-deps --force-recreate relay
```

迁移失败应保留停止状态，查看脱敏日志、核对版本后处理；不要自动降级镜像或删卷。常规运行角色只能写业务表，迁移账本只读。启动与迁移共享同一数据库锁，未知/超前/不连续 migration history 会拒绝。

备份自有库与加密主密钥，秘密分开保管，不共享实际 `.env` 或数据库 dump。当前尚未实现需求中的灾备恢复授权代际轮换和受控恢复向导：恢复旧备份可能复活会话、配对/设备凭据与撤销前状态，禁止把直接恢复当安全的生产恢复流程。恢复时需隔离原实例并在开放访问前重新建立授权；现有版本缺少自动完成该流程的入口，需后续实现/验收。普通进程的单活锁无法阻止两个独立克隆数据库同时执行。

停止只使用 `docker compose stop` 或不带 `-v` 的 `down`；不要用 `down -v` 排错。

当前历史/告警/审计默认追加存储；管理员确认后可按策略分批清理历史与审计，预览由存储层时间条件计数，告警仍永久保留。尚无存储容量上限或 Retry-After 退避。没有公网认证入口保护和数据库/备份实测前，建议仅在受控开发环境试用。NAS 生产可用性、长期容量和全部首期 AC 均未通过验收。
