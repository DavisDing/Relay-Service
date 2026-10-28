# NAS Docker 开发预览部署

本轮交付 Go 服务端与已嵌入的 React 页面，以及新建 Relay 自有 PostgreSQL 的 Compose。属于 M1 基础监控切片；CI 已通过镜像构建及无数据库的 amd64 基础运行检查；真实数据库/权限/迁移、完整容器启动和 UGOS Projects 未验收。六服务的接口前提见 [COMPATIBILITY.md](../COMPATIBILITY.md)。调用 Key、模型路由写入、付费探针、账本余额、正文、原生客户端和镜像更新不在本轮代码范围。

## 1. NAS 首次部署（无需填写 IP 或预生成秘密）

按 2026-10-28 会话确认的简化流程，使用本次源码构建并发布的镜像与 Compose 配套部署。源码更新不表示 GHCR `latest` 已包含改动；旧镜像不支持 `RELAY_DEPLOYMENT_FILE`，不能把新 YAML 直接配旧镜像使用。自行测试默认渠道仍为 `ghcr.io/davisding/relay-service:latest`（D-16），实际提交/digest 需查看发布结果。

直接将 `deploy/nas/docker-compose.yaml` 导入 Docker Projects。默认只声明两个镜像、端口、卷与就绪依赖；数据库启动/角色脚本和健康检查在镜像内部执行，无需额外脚本、`.env`、宿主 IP、openssl 或手动建表。Compose 自动创建普通项目网络，不要求预先存在 ai-network。数据库配套镜像 `ghcr.io/davisding/relay-service-postgres:latest` 基于官方 `postgres:latest`，只增加 Relay 初始化文件；需要与本次应用镜像同批发布。

默认映射 `0.0.0.0:8787:8787`。浏览器打开 `http://NAS实际地址:8787`；地址只在浏览器输入，不写入 YAML。端口冲突时直接将 ports 改为例如 `8877:8787`。不指定 `RELAY_PUBLIC_URL` 时，服务端按请求的 Host 和协议校验 Origin，保留写请求来源和 Cookie CSRF 校验；前端使用相对 API 地址。不读取或信任 `X-Forwarded-Host` / `X-Forwarded-Proto`。

终端等效命令（在 YAML 所在目录）：

```sh
docker compose -f docker-compose.yaml config --quiet
docker compose -f docker-compose.yaml pull
docker compose -f docker-compose.yaml up -d --pull never
docker compose -f docker-compose.yaml logs --tail=100 relay
```

首次启动由 PostgreSQL 包装脚本从系统随机源生成各自独立的数据库口令、加密主密钥和临时管理员密码，完整生成后原子发布到 `relay-secrets` 命名卷。该卷顶层可遍历，内部目录归 root:65532、权限 710，秘密文件默认 root:root/600；长期 deployment.env 归 root:65532/640，Relay 可读而不可改；临时密码单独存放在 initial/password（目录和文件归 UID/GID 65532，权限 700/600）。卷挂载可写仅为删除临时文件，根目录和 current 目录均由 root 持有，Relay 无法改写长期配置或读取超级用户/所有者原始口令文件。数据库超级用户/所有者口令不加载到 Relay 配置中，Relay 从长期配置读取业务和初始化连接、加密密钥，另读可选的临时密码文件。旧四键配置由数据库包装脚本在启动时迁移，移除其中的临时字段和原始临时口令副本，不改变其他持久化秘密。容器内持有初始化连接这一现有边界不变。

## 2. 首次登录与反代

查看 NAS 的 **Relay 容器日志**，找到 `Relay first login` 的用户名 `admin` 和实例独立临时密码。用它登录后，只允许设置正式用户名/密码或退出，不能访问监控、设备、诊断等业务 API。设置至少 12 字符的新密码（不能继续使用临时密码）后，所有临时会话和临时密码失效，必须用正式账号重新登录。正式密码仅保存 Argon2id 哈希；数据库提交成功后删除 initial/password，清理失败输出脱敏告警并在已完成账号的下次启动重试，不能撤回已成功改密的数据库事务。启动准备后清空运行配置中的初始密码引用与进程环境值，但 Go 字符串/GC 不保证物理内存立即擦除。完成后重启不重置账号、不再输出临时密码。日志中历史临时密码不会自动擦除，但已失效。

普通 HTTP 局域网访问无需其他配置。Lucky/HTTPS 反代推荐设置唯一 `RELAY_PUBLIC_URL=https://实际域名`，Secure Cookie 自动启用，无需另外填 `RELAY_SECURE_COOKIES`。该地址是实际浏览器 origin，不含路径；反代与 Relay 在同一网络时，上游可使用 `http://relay:8787`；可选网络示例同时提供 relay-service 别名。如果反代保留原始 Host，也可仅设置 `RELAY_SECURE_COOKIES=true` 以 HTTPS scheme 校验；不支持凭任意代理头自动推断可信协议。反代/TLS 由部署者配置。

`relay-postgres` 保存数据库，`relay-secrets` 保存解密和连接所需秘密，两者均需保留并分别保护备份。重启/重建复用已存秘密；已有数据库但缺少密钥卷会拒绝生成替代秘密。长期配置文件缺失、损坏或与秘密不一致会停止启动，不自动换密码、删库或修复未知结构。待改密账号的临时密码配置缺失或与数据库哈希不匹配时，应用输出明确告警并继续运行，不显示错误密码、不重置原账号；原临时密码仍可登录，部署者应从原部署日志/备份找回。手工维护若改坏长期连接或密钥，仍应拒绝启动。

已有旧版手工 `.env` 安装可继续使用原业务密码、迁移密码、所有者密码、超级用户密码和加密密钥，首次包装启动将这五项原值导入新秘密卷；必须完整提供，不要重新运行生成器。已有管理员及旧会话 JSON 字段默认兼容（新字段为 0/false），不会强制重置。部署脚本只接受 URL 安全的随机口令（至少 32 字符）；若原配置不符合此条件，保留旧 Compose 与显式环境模式运行，不能随意改环境值替换真实数据库口令。旧 `RELAY_BOOTSTRAP_TOKEN` / bootstrap API 保留供显式环境部署使用，默认 NAS 流程不需要。

空库角色创建、schema 检查/按需 SQL 初始化沿用 D-17；完整 schema 不重复执行，不重置管理员或服务数据。未知/不连续版本、无账本旧结构、损坏对象或权限错误拒绝启动。一个数据库只允许一个 Relay 调度实例。当前开发期 001/002 面向空库，不含历史 schema 回填。

源码构建仍使用：

```sh
docker compose -f docker-compose.yaml -f docker-compose.local-build.yaml build
docker compose -f docker-compose.yaml -f docker-compose.local-build.yaml up -d --pull never
```

无需运行 `generate-env.sh`；该脚本仅保留为手工秘密的可选工具。源码构建覆盖文件同时构建应用和数据库两个镜像。版本包 Compose 固定两镜像的发布摘要；源码 Compose 默认 latest。`start-postgres.sh` 和 `init-db/001-roles.sh` 是数据库镜像源码，修改后重建镜像，不再生成 Compose 脚本块。

默认参数放在应用和镜像里；需要更改时在相应服务添加 `environment:`，例如：

```yaml
services:
  relay:
    environment:
      RELAY_LOG_LEVEL: debug
      RELAY_PUBLIC_URL: https://relay.example.com
```

这是添加到主文件已有 relay 服务的片段，不是完整部署文件。显式环境值优先，未配置时使用默认值；业务/迁移连接和密钥可由环境显式覆盖持久化部署文件。部署文件只接收四个白名单键，可仅包含其中部分；合并后仍按实际必需项验证数据库连接和加密密钥，临时密码及迁移连接可缺省。已初始化的数据库秘密优先复用存储值，不因修改环境而自动轮换数据库口令或加密密钥。镜像默认读取 `/relay-secrets/current/deployment.env`；完全手工部署可显式设 `RELAY_DEPLOYMENT_FILE: ""`，再提供完整环境配置；完全手工模式也可将 `RELAY_INITIAL_PASSWORD_FILE` 置空，避免读取镜像默认临时文件。

也可复制 `.env.example` 到 `.env` 并主动合并设置示例：

```sh
docker compose -f docker-compose.yaml -f docker-compose.settings.example.yaml up -d
```

主文件不自动读取设置示例；`.env` 只用于 Compose 插值，不会自行注入全部容器变量。既有手工秘密导入使用此设置示例。需要加入已有网络时，修改 `docker-compose.network.example.yaml` 的网络名称后主动合并：

```sh
docker compose -f docker-compose.yaml -f docker-compose.network.example.yaml up -d
```

默认网络仍保留给 PostgreSQL，只有 Relay 加入外部网络。也可以直接在主文件按自己的部署环境修改网络。

## 3. 配置服务与验证

添加服务时使用 Relay 容器可以解析和访问的地址，例如同网络中的 `http://litellm:4000`。`127.0.0.1` 指 Relay 容器自身。检查 URL 只允许协议、主机、端口；原 WebUI 地址单独填写。私网目标必须显式授权，metadata/link-local/多播等仍拒绝；不跟随重定向、不使用环境 HTTP 代理。监控凭据加密保存，不回显给 Web 或只读设备。修改目标地址时必须重新输入或显式清除旧凭据，防止把已有秘密发送给新目标。

固定 GET 检查不会发推理或工具请求。成功仅证明对应端点/进程，不证明模型可用。工作副本版本与接口未知时显示未知/不支持；认证失败与检查权限受限单独记录，不归为服务宕机。首次未检查、过期数据和旧配置结果不会当当前健康。

- `/health/live`：进程 HTTP 存活；容器 healthcheck 使用此入口。
- `/health/ready`：自有数据库/schema 就绪；上游故障不影响此入口。
- `/api/v1/info`：公开最小身份、初始化状态和版本；其他接口需管理员 Cookie 或配对设备令牌。
- 浏览器写操作需要配置 origin 与双提交 CSRF；原生设备配对为一次性 5 分钟码，设备凭据 180 天到期，可即时撤销，只能读取与更新自身告警已读位置。

部署前后逐项验证：初始化、重登、服务检查/历史、模拟故障与恢复、暂停与静默、设备配对/撤销、重启历史仍在、凭据仍能解密、两角色权限与单活锁。当前这些数据库/容器/NAS 场景均为未执行。

## 4. 更新、备份与恢复边界

数据库配套镜像构建基于用户选择的 `postgres:latest`；18+ 官方镜像使用命名卷挂到 `/var/lib/postgresql`。实际镜像 digest/主版本需由部署者记录。主版本升级需要 PostgreSQL 正式迁移，不能把换镜像视为自动完成；本地 PostgreSQL 源码构建不证明该镜像兼容。

以下更新步骤适用于本次首装基线部署后的未来兼容升级，不提供历史开发 schema 的修复/回填。更新 Relay 必须先备份并停止常规进程；新进程启动时检查并执行待应用的版本化 SQL。跟随测试渠道时，主 Compose 的应用 image 保持 latest；固定版本升级则改为新 Release 摘要，均保留原秘密和 relay-secrets 卷。`docker compose pull` 只下载镜像，需要重建 Relay 才会使用新版本。以下命令在同一部署目录执行，Compose 自动读取 `.env`；初始化与正常运行使用本机已拉取的同一镜像，后续步骤不再次拉取；不要在升级 Relay 时顺带更新 PostgreSQL：

```sh
docker compose stop relay
docker compose pull relay
docker compose up -d --pull never postgres
docker compose up -d --pull never --no-deps --force-recreate relay
```

用 `docker compose logs --tail=100 relay` 与 `/health/ready` 核对新进程就绪；命令返回不代表初始化成功。初始化失败时进程会退出，默认重启策略可能继续重试，须查看脱敏日志、核对版本并修复原因；不要自动降级镜像或删卷。常规运行角色只能写业务表，迁移账本只读。启动与迁移共享同一数据库锁，未知/超前/不连续 migration history 会拒绝。

备份自有库与 relay-secrets 命名卷（包含加密主密钥），秘密分开保管，不共享实际 `.env` 或数据库 dump。当前尚未实现需求中的灾备恢复授权代际轮换和受控恢复向导：恢复旧备份可能复活会话、配对/设备凭据与撤销前状态，禁止把直接恢复当安全的生产恢复流程。恢复时需隔离原实例并在开放访问前重新建立授权；现有版本缺少自动完成该流程的入口，需后续实现/验收。普通进程的单活锁无法阻止两个独立克隆数据库同时执行。

停止只使用 `docker compose stop` 或不带 `-v` 的 `down`；不要用 `down -v` 排错。

当前历史/告警/审计默认追加存储；管理员确认后可按策略分批清理历史与审计，预览由存储层时间条件计数，告警仍永久保留。尚无存储容量上限或 Retry-After 退避。没有公网认证入口保护和数据库/备份实测前，建议仅在受控开发环境试用。NAS 生产可用性、长期容量和全部首期 AC 均未通过验收。
