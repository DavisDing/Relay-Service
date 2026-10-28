# Relay Service

NAS 服务端与 Web 运维控制台，采用 Go + PostgreSQL + React/TypeScript。当前为 M1 基础监控开发预览：管理员/只读设备、服务检查、历史、故障/恢复事件、静默与已读位置。

当前用于自行测试，Docker 默认使用 `ghcr.io/davisding/relay-service:latest`。`main` 通过 CI 和镜像基础检查后更新该标签；它代表最新测试构建，稳定后才发布正式版。

## Docker Compose（NAS）

默认使用 [docker-compose.yaml](deploy/nas/docker-compose.yaml)，在 NAS 的 Docker Projects 中导入即可；命令行等效操作如下，在该文件所在目录执行：

```sh
docker compose -f docker-compose.yaml up -d
docker compose -f docker-compose.yaml logs --tail=100 relay
```

默认映射 `8787:8787`，浏览器打开 `http://NAS实际地址:8787`。首次用户名为 `admin`，临时密码见 Relay 容器日志，登录后必须设置正式账号密码。数据库角色、表、内部密码和密钥由镜像自动初始化，不需要提前创建 `.env`、填写宿主 IP 或创建外部网络。正常重建复用原数据，首次改密成功后删除临时密码文件。

当前源码的应用镜像为 `ghcr.io/davisding/relay-service:latest`，数据库配套镜像为 `ghcr.io/davisding/relay-service-postgres:latest`，后者基于官方 `postgres:latest` 并加入 Relay 初始化脚本。**本地配置变更不表示镜像已发布；这套精简部署及凭据清理功能需匹配的两个新镜像发布后使用。** 历史发布版不证明包含这些改动，实际版本/digest 见对应发布结果。

### 常用修改项

默认参数不用写进 Compose；需要修改时，在已有服务下添加或修改相应项。以下是直接修改主文件的方式：

| 修改项 | 默认值 | 修改方式与作用 |
|---|---|---|
| Web/API 宿主端口 | `8787:8787` | 例如改成 `8877:8787`，浏览器访问8877；右侧容器端口保持8787 |
| 宿主绑定地址 | 映射至宿主所有接口 | 例如 `127.0.0.1:8787:8787` 只允许宿主本机访问；若只绑定某个 NAS 接口，可用 `NAS接口地址:8787:8787` |
| 应用镜像 | `ghcr.io/davisding/relay-service:latest` | 修改 `services.relay.image`；可改为发布版本或 digest |
| 数据库配套镜像 | `ghcr.io/davisding/relay-service-postgres:latest` | 修改 `services.postgres.image`；自动初始化方案依赖其内置脚本，直接换官方 postgres 镜像需要另行配置初始化 |
| 项目网络 | Compose 自动创建的项目网络 | 需要按容器服务名访问已有项目时，再加入已有网络，示例见下文 |
| 数据卷 | `relay-postgres`、`relay-secrets` | 分别保存数据库和密钥/连接配置；更改挂载位置或卷名需要先迁移对应数据，不能只改名称 |
| 重启策略 | `unless-stopped` | 在相应服务的 `restart` 修改 |

Relay 环境参数写在 **`services.relay.environment`**：

| 参数 | 默认行为 | 可设置内容 |
|---|---|---|
| `RELAY_LOG_LEVEL` | `info` | `debug`、`info`、`warn`、`error` |
| `RELAY_PUBLIC_URL` | 空；按请求 Host/协议校验来源 | 固定浏览器访问地址，例如 `https://relay.example.com`，不带业务路径、查询或账号；普通局域网访问无需设置 |
| `RELAY_SECURE_COOKIES` | 跟随 public URL 是否 HTTPS；URL为空时为 `false` | `true`/`false`；设置 HTTPS public URL 后自动启用。未固定 URL 的 HTTPS 反代也可显式设 `true`，反代须保留原始 Host；设置值必须与已配置 URL 的协议一致 |
| `RELAY_LISTEN_ADDR` | `:8787` | 容器内监听地址，例如 `:8888`；修改端口须同步修改 ports 右侧。通常仅改宿主端口即可 |

例如，把主文件中的 **relay 服务**改成以下内容；主文件的 postgres 服务、顶层卷声明和项目名继续保留：

```yaml
services:
  relay:
    image: ghcr.io/davisding/relay-service:latest
    restart: unless-stopped
    ports:
      - "8877:8787"
    environment:
      RELAY_LOG_LEVEL: info
      RELAY_PUBLIC_URL: https://relay.example.com
    volumes:
      - relay-secrets:/relay-secrets
    depends_on:
      postgres:
        condition: service_healthy
```

这是宿主端口8877和 HTTPS 反代的示例；直接 HTTP 访问时省略 `RELAY_PUBLIC_URL`，不需要照抄域名。以上是主文件修改片段，不要另加 `-f` 合并它来替换端口；Compose 合并多个文件时可能保留原端口映射。

### 配置覆盖、.env 和已有网络

应用非空环境值优先于部署文件；没有配置时使用文件/程序默认值。布尔开关请写 `"true"` 或 `"false"`；空字符串通常表示回退默认值，不能用空字符串清除已存密钥。

也可使用 [.env.example](deploy/nas/.env.example) 与 [设置示例](deploy/nas/docker-compose.settings.example.yaml)：复制模板为 `.env`，只改需要的值，然后显式合并设置文件：

```sh
docker compose -f docker-compose.yaml -f docker-compose.settings.example.yaml up -d
```

设置示例中的 `RELAY_IMAGE`、`RELAY_POSTGRES_IMAGE` 用于替换两个 image，其他变量通过 environment 传入相应服务。**主文件没有引用这些变量；只创建 `.env` 不会自动改变主文件中的镜像、端口或容器环境。** 修改参数后需重新运行相同的 Compose 启动命令以重建受影响容器，仅 `restart` 不会应用新的容器配置。

默认不依赖 `ai-network`。需要加入已有网络时，修改 [网络示例](deploy/nas/docker-compose.network.example.yaml) 的网络名称，再显式合并：

```sh
docker compose -f docker-compose.yaml -f docker-compose.network.example.yaml up -d
```

网络必须已经存在；示例只将 Relay 加入该网络，同时保留默认网络供 Relay 连接 PostgreSQL。在同一网络中可用 `http://litellm:4000` 等服务名配置监控目标，数据库连接使用 `postgres:5432`；浏览器仍使用 NAS 地址或反代域名。

### 高级参数：手工数据库配置或原秘密导入

正常首装不用填写以下参数。不要把数据库初始化参数当作已部署口令的在线修改入口：**持久化秘密创建后优先复用存储值，改环境变量不会自动修改现有数据库密码或完成加密密钥轮换。**

| 设置位置 | 参数 | 默认行为与约束 |
|---|---|---|
| `postgres.environment` | `POSTGRES_SUPERUSER_PASSWORD` | 首装自动生成；手工提供时至少32字符，使用字母、数字、下划线、短横线 |
| `postgres.environment` | `RELAY_DB_PASSWORD` | 数据库所有者 `relay_owner` 的密码，首装自动生成；不是应用运行账号密码，格式约束同上 |
| `postgres.environment` | `RELAY_MIGRATOR_PASSWORD` | 初始化账号 `relay_migrator` 的密码，首装自动生成；格式约束同上 |
| `postgres.environment` | `RELAY_APP_PASSWORD` | 业务账号 `relay_app` 的密码，首装自动生成；格式约束同上 |
| `postgres.environment` | `RELAY_ENCRYPTION_KEY` | 首装自动生成，须为64位十六进制字符串；原库导入须沿用原值 |
| `relay.environment` | `RELAY_DATABASE_URL` | 默认从部署文件读业务连接；手工指定须使用独立 Relay 库，不能使用 postgres/template 系统库 |
| `relay.environment` | `RELAY_MIGRATION_DATABASE_URL` | 默认从部署文件读初始化连接；显式配置时须与业务连接指向同一主机、端口、库和连接选项。纯手工省略仅适用于已建好结构的库；需要自动初始化时提供独立账号 |
| `relay.environment` | `RELAY_ENCRYPTION_KEY` | 默认读部署文件；显式提供须为64位十六进制字符串，必须与已有凭据加密所用密钥一致 |
| `relay.environment` | `RELAY_DEPLOYMENT_FILE` | 镜像默认 `/relay-secrets/current/deployment.env`；允许部分白名单键，最终有效配置仍须有业务连接和加密密钥；完全手工模式可显式设 `""` 禁用文件读取 |
| `relay.environment` | `RELAY_INITIAL_PASSWORD_FILE` | 镜像默认 `/relay-secrets/initial/password`；改密后删除，缺失允许。完全手工模式可显式设 `""` 禁用读取 |
| `relay.environment` | `RELAY_INITIAL_PASSWORD` | 默认从临时文件读取；手工提供时为32～1024字符的实例独立临时密码，只用于初始账号，不会重置已有账号。自行写进 Compose 的明文需自行移除 |
| `relay.environment` | `RELAY_BOOTSTRAP_TOKEN` | 默认空/关闭；保留给手工 bootstrap API 模式，至少32字符。普通 NAS 首装不需要 |

已有手工安装首次导入秘密卷时，在 postgres 服务中完整提供原超级用户密码、所有者密码、初始化密码、业务密码和加密密钥五项；不能重新生成替代值。设置示例已接入这五项。`POSTGRES_PASSWORD` 由包装脚本从持久化超级用户密码设置，普通部署使用表中的 `POSTGRES_SUPERUSER_PASSWORD`。

卷根目录和长期配置由 root 所有权保护，Relay 仅能写临时密码子目录；不要把秘密卷改成只读，否则改密后的文件删除会失败。更新和备份同时保留两个卷，不使用 `down -v` 排错；PostgreSQL 主版本升级需要专门迁移，详细步骤见 [NAS 部署指引](docs/nas/DEPLOY.md)。

### PostgreSQL 与 Relay 能否合成一个镜像？

**可以。** 可以基于 PostgreSQL 镜像加入已嵌入 Web 的 Relay 可执行文件，用一个入口管理两个进程，对外仍只发布8787，数据库只在容器内部监听。数据和密钥仍须放在持久化卷中。

| 项目 | 当前双容器方案 | 单镜像、单容器方案 |
|---|---|---|
| 部署入口 | 一个 Compose 项目，两个服务 | 一个服务，Compose 还能更短 |
| 更新应用 | 可以只重建 Relay | 通常需要重建整容器，数据库也随之重启 |
| 进程管理 | Docker 分别跟踪两个服务 | 入口需负责两个进程的启动顺序、异常退出、信号转发和优雅关闭 |
| 健康检查 | 应用与数据库分别检查 | 需要同时检查两个进程，避免数据库退出后容器仍显示运行 |
| 数据库维护 | 可以独立选择配套数据库镜像版本 | 镜像同时包含应用和数据库版本，需要兼容性及主版本升级策略 |

当前分开部署是为了分别管理应用和数据库的生命周期；不是技术上不能合并。单容器也不会省掉 PostgreSQL 本身的进程和数据管理需求。若提供一体版，需要补齐上述进程管理和持久化验证，不能只把可执行文件复制进去就算完成。本仓库目前未实现一体版，本段仅说明可行方案，默认部署继续使用两个服务。

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
