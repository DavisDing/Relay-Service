# Relay Service

NAS 服务端与 Web 运维控制台，采用 Go + SQLite/PostgreSQL + React/TypeScript。当前为 M1 基础监控开发预览：管理员/只读设备、服务检查、历史、故障/恢复事件、静默与已读位置。

当前用于自行测试，Docker 默认使用 `ghcr.io/davisding/relay-service:latest`。`main` 通过 CI 和镜像基础检查后更新该标签；它代表最新测试构建，稳定后才发布正式版。

## Docker Compose（NAS）

默认使用 **SQLite**，只运行一个 Relay 容器。导入 [docker-compose.yaml](deploy/nas/docker-compose.yaml)，或在 `deploy/nas/` 执行：

```sh
docker compose -f docker-compose.yaml up -d
docker compose -f docker-compose.yaml logs --tail=100 relay
```

打开 `http://NAS实际地址:8787`。首次用户名为 `admin`，实例独立临时密码见 Relay 日志；首次登录必须设置正式账号密码。密钥、SQLite 数据和临时凭据由 Relay 自动初始化到 `relay-data` 卷，改密成功后删除临时文件。不需要 PostgreSQL、`.env`、宿主 IP 或外部网络。无数据库服务、同步进程或双写任务；单实例 SQLite 使用单连接、短事务和索引查询。

### 三种存储用法

| 使用方式 | 部署操作 |
|---|---|
| 单独使用 Relay | 默认 Compose 即可，SQLite 保存在 `/data/relay.sqlite` |
| 先 SQLite，后 PostgreSQL | 停止 Relay → 准备独立空库 → 执行 `relay migrate-sqlite` → 保留原数据卷并使用 PostgreSQL 连接启动 |
| 首装直接 PostgreSQL | 先创建 Relay 专用库/账号，再配置业务和建表连接；Relay 启动时自动建表 |

完整可复制步骤见 [存储、PostgreSQL 初始化与 SQLite 迁移](docs/nas/STORAGE.md)。提供 [官方 PostgreSQL 服务示例](deploy/nas/postgres.compose.yaml)、[数据库/账号初始化脚本](deploy/nas/init-db/001-roles.sh)和[已有 PostgreSQL 连接示例](deploy/nas/docker-compose.postgres.example.yaml)。后续 LiteLLM 可共用 PostgreSQL 实例，另建 `litellm` 库和账号；Relay 也可以一直保留 SQLite。

**不配置 PostgreSQL 连接就默认 SQLite；配置后连接失败会报错，不会降级到空 SQLite。已有 SQLite 数据必须显式迁移，不能只加连接串就切库。迁移后删除连接配置也不会自动回到旧数据。** 迁移先生成一致性备份，目标逐条复制并核对数量、内容摘要和序列；不覆盖非空目标，不删除源文件，保留同一加密密钥。

### 可修改参数

默认值不用写进 Compose，需要调整时添加到 `services.relay.environment`：

| 参数 | 默认值/行为 | 修改用途 |
|---|---|---|
| `RELAY_DATA_DIR` | 镜像内 `/data`；直接运行二进制为 `./data` | 数据、密钥和后端标记目录；必须持久化，PostgreSQL 模式也保留 |
| `RELAY_SQLITE_PATH` | 数据目录下 `relay.sqlite` | 自定义 SQLite 文件路径；使用 NAS 本机磁盘，不放 SMB/NFS/同步盘；更改前迁移原文件与 WAL 数据 |
| `RELAY_DATABASE_URL` | 空，使用 SQLite | 例如 `postgres://relay_app:密码@postgres:5432/relay?sslmode=disable`；密码特殊字符需 URL 编码，远程连接按部署配置 TLS |
| `RELAY_MIGRATION_DATABASE_URL` | 空 | PostgreSQL 建表/升级连接；与业务连接同主机/端口/库/选项，使用独立迁移账号；已完成结构且无升级时可省略 |
| `RELAY_LOG_LEVEL` | `info` | `debug`、`info`、`warn`、`error` |
| `RELAY_PUBLIC_URL` | 空，允许按请求 Host/协议验证来源 | 固定反代 origin，例如 `https://relay.example.com`，不含路径/查询/账号；局域网直连无需设置 |
| `RELAY_SECURE_COOKIES` | 随 public URL 是否 HTTPS，未固定 URL 默认 `false` | `"true"` / `"false"`；固定 URL 时必须与协议一致 |
| `RELAY_LISTEN_ADDR` | `:8787` | 容器内监听地址；修改端口时同步修改 ports 右侧 |
| `RELAY_ENCRYPTION_KEY` | 首装随机生成并保存为 `/data/encryption.key` | 仅导入原实例密钥时提供64位十六进制；不能通过改变量轮换，遗失后不能重建替代 |
| `RELAY_INITIAL_PASSWORD` | 首装生成并保存，正式改密后删除临时文件 | 可指定32～1024字符临时密码；不会重置已有账号，自行填入 Compose 的明文需自行移除 |
| `RELAY_BOOTSTRAP_TOKEN` | 空 | 高级手工 bootstrap API 模式，至少32字符；普通首装不用 |
| `RELAY_DEPLOYMENT_FILE` | 空 | 兼容旧部署文件，非空时必须存在；显式环境值覆盖文件值 |
| `RELAY_INITIAL_PASSWORD_FILE` | 数据目录下 `initial-password` | 临时密码文件；兼容旧部署时可指定原路径，正式改密后删除 |

常用 Compose 修改项：

| 项目 | 默认 | 示例 |
|---|---|---|
| 宿主端口 | `8787:8787` | 主文件直接改为 `8877:8787`；只改左侧不用调整监听参数 |
| 绑定接口 | 宿主所有接口 | `127.0.0.1:8787:8787` 仅宿主可访问 |
| 镜像 | `ghcr.io/davisding/relay-service:latest` | 可选择已发布版本或 digest |
| 持久化 | `relay-data:/data` | 可改 NAS 本地目录，确保 UID/GID 65532 可写；已有数据需先搬迁，不能只换卷名 |
| 网络 | Compose 默认网络 | 有需要时显式合并 [已有网络示例](deploy/nas/docker-compose.network.example.yaml)，再改成你的网络名 |

例如只修改主文件中对应字段：

```yaml
services:
  relay:
    image: ghcr.io/davisding/relay-service:latest
    restart: unless-stopped
    ports:
      - "8877:8787"
    environment:
      RELAY_PUBLIC_URL: https://relay.example.com
      RELAY_LOG_LEVEL: info
    volumes:
      - relay-data:/data
volumes:
  relay-data:
```

以上 HTTPS 域名仅是反代示例，HTTP 直连时省略。主文件端口请直接修改，多文件合并可能保留旧端口。

也可以复制 [.env.example](deploy/nas/.env.example) 为 `.env`，显式合并 [设置文件](deploy/nas/docker-compose.settings.example.yaml)。只有 Compose 引用的变量才生效，主文件本身未引用 `.env`。修改后重新 `up -d` 应用配置，单纯 `restart` 不会更新环境。

```sh
docker compose -f docker-compose.yaml -f docker-compose.settings.example.yaml up -d
```

目前源码已切换 SQLite 默认部署；本地改动不代表新镜像已发布。旧 PostgreSQL 配套镜像仅为历史部署兼容资产，不再是默认依赖。PostgreSQL 与 Relay 技术上可合成一个容器，但当前 SQLite 默认方案已实现单容器使用，无需捆绑数据库进程；可选 PostgreSQL 保持官方独立服务。

- [项目现状与验证入口](docs/AI_CONTEXT.md)
- [需求与设计](docs/README.md)
- [NAS 镜像/Compose 部署](docs/nas/DEPLOY.md)
- [CI、Packages 与 Release 流程](docs/nas/RELEASE.md)
- [本轮验证与未完成项](docs/nas/VALIDATION.md)
- [六服务端点兼容矩阵](docs/COMPATIBILITY.md)
- [当前 API 与持久化合同](contracts/IMPLEMENTATION.md)

```sh
cd web
pnpm install --frozen-lockfile
pnpm build
cd ../server
go test -race ./...
go vet ./...
go test -race -tags webassets ./internal/webui ./internal/api ./cmd/relay
go vet -tags webassets ./...
```

从纯源码检出可直接在 `server/` 执行 `go test -race ./...` 和 `go vet ./...`，无需前端产物。默认是后端开发构建，Web 请求明确返回 503；完整运行使用 `pnpm build` 后 `go run -tags webassets ./cmd/relay`。Docker/发布构建固定带 `webassets`，缺少真实页面资源时编译失败。

测试默认使用内存Store/本机HTTP fixture。数据库集成测试需专用 `_test` 数据库和显式 `RELAY_TEST_DATABASE_URL`，默认跳过。

已发布 [v0.1.0-alpha.1](https://github.com/DavisDing/Relay-Service/releases/tag/v0.1.0-alpha.1) 开发预览和 GHCR 双架构镜像，提供带摘要的完整 NAS 部署包。CI、Compose 解析和 amd64 容器基础检查通过；arm64 只构建。真实数据库、UGOS、浏览器与六服务安装版本尚未验收。workbuddy2api 安全检查合同与 MetaCubeXD 变体需核对，完整首期能力见需求文档，不能将本版视为全部完成。
