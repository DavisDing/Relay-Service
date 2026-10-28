# NAS Docker 开发预览部署

当前源码按 D-19 默认使用 SQLite，只运行 Relay 一个容器。PostgreSQL 为可选外部服务，三种存储路径、初始化与迁移命令见 [STORAGE.md](STORAGE.md)。当前为 M1 基础监控开发预览；源码修改不表示 GHCR `latest` 已发布对应镜像，Docker/UGOS 与真实数据库验收需分别完成。

## 1. 默认部署

将 `deploy/nas/docker-compose.yaml` 导入 NAS Docker Projects，或在该目录执行：

```sh
docker compose -f docker-compose.yaml up -d
docker compose -f docker-compose.yaml logs --tail=100 relay
```

默认只有镜像、重启策略、8787端口和 `relay-data:/data` 卷。Compose 创建普通项目网络；无需 `.env`、宿主 IP、外部网络或数据库容器。浏览器打开 `http://NAS实际地址:8787`。端口冲突时直接把主文件的 ports 改成 `8877:8787` 等，不必更改容器监听端口。

Relay 自动初始化 SQLite、加密密钥和临时管理员密码。首次用户名为 `admin`，密码从 Relay 容器日志读取。首次登录必须设置至少12字符的正式密码并重新登录；改密前不能使用业务 API。成功后删除临时密码文件，旧日志里的密码已失效。重建或升级必须保留数据卷。

源码构建使用：

```sh
docker compose -f docker-compose.yaml -f docker-compose.local-build.yaml build
docker compose -f docker-compose.yaml -f docker-compose.local-build.yaml up -d --pull never
```

此覆盖文件只构建 Relay。默认部署不使用历史 PostgreSQL 配套镜像、包装脚本或 `relay-secrets` 卷。

## 2. 可选设置与网络

参数名称、默认值与用途见根 [README 的 Docker Compose 部分](../../README.md#可修改参数)。需要修改时才在 `services.relay.environment` 中添加，例如：

```yaml
services:
  relay:
    environment:
      RELAY_PUBLIC_URL: https://relay.example.com
      RELAY_LOG_LEVEL: debug
```

普通 HTTP 直连省略以上域名。固定 HTTPS origin 会启用 Secure Cookie；不固定 URL 时按请求 Host/协议校验来源，不信任任意代理头。Lucky/反代与 Relay 同网络时可使用 `http://relay:8787` 作为上游。前端使用相对 API 地址。

也可复制 `.env.example` 为 `.env` 后主动合并设置示例；只有 Compose 引用的变量才会注入：

```sh
docker compose -f docker-compose.yaml -f docker-compose.settings.example.yaml up -d
```

加入已有网络时先修改网络示例中的名称，再合并：

```sh
docker compose -f docker-compose.yaml -f docker-compose.network.example.yaml up -d
```

默认仍使用普通 Compose 网络。配置监控服务时填写 Relay 容器能解析和访问的地址，例如同网络下的 `http://litellm:4000`；`127.0.0.1` 指 Relay 自己。私网检查需显式授权，凭据加密保存，不回显。

## 3. 可选 PostgreSQL

- 首装直连：执行 `init-db/001-roles.sh` 创建独立 Relay 库与账号，合并 `docker-compose.postgres.example.yaml` 提供业务/建表连接。Relay 自动初始化表结构。
- 先 SQLite 后 PostgreSQL：停止 Relay，使用同一卷和密钥执行 `relay migrate-sqlite`，完成备份与摘要核对后再按 PostgreSQL 配置启动。
- 需要新 PostgreSQL 实例时，使用独立官方服务示例 `postgres.compose.yaml`。LiteLLM 可共享实例，另建库和账号。

完整命令和失败恢复边界以 [STORAGE.md](STORAGE.md) 为准。显式 PostgreSQL 连接失败不回退 SQLite，已存在的 SQLite 数据不能只添加连接串就切换。两个后端都必须保留 `/data` 中的原加密密钥。

## 4. 更新、备份与验收

SQLite 备份时停止 Relay，复制整个数据目录，包含数据库、可能存在的 WAL 和密钥；避免在线只复制主文件。使用 NAS 本地文件系统，不把 SQLite 放到 SMB/NFS/同步盘。PostgreSQL 分别备份业务库和 Relay 数据卷；其主版本升级单独安排。

更新 Relay 时保留卷和后端配置，只更新 Relay 镜像；多文件部署继续使用原来的完整文件列表：

```sh
docker compose stop relay
docker compose pull relay
docker compose up -d --pull never --no-deps --force-recreate relay
```

查看 Relay 日志和 `/health/ready`。`/health/live` 仅验证进程 HTTP 存活；上游服务故障不改变自有数据库就绪状态。初始化失败会退出，应先查明原因，不能通过删卷解决。停止可用 `stop` 或不带 `-v` 的 `down`。

部署验收应覆盖首次改密、重登、服务配置/检查/历史、故障恢复、设备配对撤销、重启持久化和凭据解密；PostgreSQL 另核对权限与单实例锁、迁移前后数据一致性。当前未执行的项目见 [测试交接记录](STORAGE-TEST-HANDOFF-2026-10-28.md)。真实 NAS、六服务和客户端验收不由本地编译或夹具替代。

现有版本未完成灾备授权代际轮换/恢复向导；恢复旧备份可能恢复旧会话和撤销前状态，需隔离原实例并重新核对授权。完整首期能力与限制见 REQUIREMENT、COMPATIBILITY 和 VALIDATION。
