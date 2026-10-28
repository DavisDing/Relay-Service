# Relay 存储与迁移

适用当前源码。默认 SQLite；镜像需由包含此改动的提交构建。Docker/NAS、远程数据库和旧部署升级需分别验证。

## 1. 单独使用 SQLite

在部署文件所在目录执行 `docker compose -f docker-compose.yaml up -d`。只有一个常驻 Relay 进程；无额外数据库、迁移容器、同步进程或 PostgreSQL 探测。`relay-data` 卷挂到 `/data`，保存 `relay.sqlite`、`encryption.key`、首次临时密码以及锁/后端标记。不要把真实 env/密钥提交到 Git。

首次账号 `admin`，从 Relay 容器日志读取临时密码，首次登录强制修改。`encryption.key` 必须与数据一起保留。临时密码文件在改密事务成功后删除，旧容器日志/外部日志备份不会自动擦除。

SQLite 使用 WAL、单连接、4MiB 页面缓存目标和短事务，保留查询有索引；不承诺固定内存或吞吐。放在 NAS 本机文件系统，不放 SMB/NFS、OneDrive 等同步目录。只运行一个 Relay 实例，同文件的第二实例和维护命令会被 OS 锁拒绝。备份前停止 Relay，备份整个数据目录；不要在线只复制主 `.sqlite` 文件而漏掉 WAL。重建保留卷，禁止用 `down -v` 排错。

## 2. 首装直接连接 PostgreSQL

### 2.1 准备 Relay 的数据库与账号

可以使用已有官方 PostgreSQL，也可选用随包的 `postgres.compose.yaml`。脚本 `init-db/001-roles.sh` 只创建 `relay` 库、`relay_owner`、`relay_migrator` 和 `relay_app`，不改 LiteLLM 库。已有同名角色/库时停止并核对，不当成空库覆盖或重置口令。脚本不是版本升级脚本。

可选生成新的 PostgreSQL 密码模板（SQLite 默认不需要）：

```sh
sh generate-env.sh .env
```

如果已有 `.env`，手工补充 `POSTGRES_SUPERUSER_PASSWORD`、`RELAY_DB_PASSWORD`、`RELAY_MIGRATOR_PASSWORD`、`RELAY_APP_PASSWORD`；不要用新生成值覆盖原数据库秘密。密码建议至少32字符、URL安全随机值。模板中的 `RELAY_ENCRYPTION_KEY` 是高级导入项，连接示例不传给 Relay；默认仍由 Relay 生成并保存本地密钥。

使用附带官方镜像服务首次创建（沿用主项目名以便共享默认网络和原数据卷）：

```sh
docker compose -p relay-service -f docker-compose.yaml -f postgres.compose.yaml up -d postgres
```

空数据目录的官方 PostgreSQL 会运行挂载的初始化脚本。现有实例不会重跑该脚本；可用 `psql` 以管理员身份执行一次。若正在使用上面的服务示例且实例已有数据，执行：

```sh
docker compose -p relay-service -f docker-compose.yaml -f postgres.compose.yaml exec -T postgres sh /docker-entrypoint-initdb.d/001-relay.sh
```

连接任意已有 PostgreSQL：在有 `psql` 的环境中执行同一脚本，设置 `PGHOST`、`PGPORT`、`PGPASSWORD`、`POSTGRES_USER`（管理员）、`POSTGRES_DB=postgres` 和三个 Relay 密码环境变量。管理员凭据只用于这次操作，不放进 Relay 容器。初始化中途失败先检查已创建对象，保留数据，不能删除数据库重试。

### 2.2 配置并启动 Relay

在 `.env` 中添加两条连接，密码与上一节一致：

```dotenv
RELAY_DATABASE_URL=postgres://relay_app:URL编码后的业务密码@postgres:5432/relay?sslmode=disable
RELAY_MIGRATION_DATABASE_URL=postgres://relay_migrator:URL编码后的初始化密码@postgres:5432/relay?sslmode=disable
```

`sslmode=disable` 仅是同机私有 Docker 网络示例；远程数据库按部署要求使用证书和 TLS。两条连接必须指向同一主机/端口/库和相同选项。Relay 首次启动自动建表，业务账号没有建表/建库权限。PostgreSQL 的超级用户与所有者密码不传给 Relay。

附带官方服务一起运行：

```sh
docker compose -p relay-service -f docker-compose.yaml -f postgres.compose.yaml -f docker-compose.postgres.example.yaml up -d
```

已有 PostgreSQL（不创建新服务）：

```sh
docker compose -f docker-compose.yaml -f docker-compose.postgres.example.yaml up -d
```

两者需处于可互通的网络；跨 Compose 项目时使用已有网络示例，并填写实际服务名。无需填写宿主 IP，也无需映射5432供容器互通。`relay-data` 继续保留密钥和后端标记，即使业务数据已在 PostgreSQL，也不要删除这个卷。

后续 LiteLLM 使用单独的 `litellm` 库和账号，按其版本文档管理表结构；不把它的表放进 `relay` 库，不让 Relay 直接读它的私有表。共用实例的故障/重启会影响两个应用。

## 3. 从 SQLite 迁移到 PostgreSQL

这是显式停机迁移，不是自动探测、双写或故障切换。目标必须是 Relay 独立空库（表结构可以预先初始化），不能合并一个已经使用的 Relay 实例。先按第2节准备好 PostgreSQL 库、账号和两条连接。**保留相同的 `relay-data` 卷、项目名、数据路径和 `encryption.key`；不要先启动使用 PostgreSQL 的 Relay。**

```sh
# 停止原 SQLite Relay，释放源文件锁。
docker compose -f docker-compose.yaml stop relay
# 单次维护命令，不创建常驻服务；连接示例从 .env 读取目标。
docker compose -f docker-compose.yaml -f docker-compose.postgres.example.yaml run --rm --no-deps relay migrate-sqlite
```

若 PostgreSQL 由上面的官方服务示例启动，在迁移与后续启动命令中继续使用 `-p relay-service` 和相同的服务文件；已有网络也应继续合并相同网络文件，保证命令使用原卷及目标网络。

迁移命令会：

1. 获取源目录/文件锁；源或目标 Relay 正在运行则拒绝迁移。
2. 检查原密钥，并在 `/data/relay-before-postgres-时间.sqlite` 创建一致性私有备份；可用 `--backup /data/自定义文件.sqlite` 指定未存在的路径。
3. 初始化目标 schema，确认目标业务记录与序列为空，然后逐条复制所有 bucket，包括账号哈希、会话、设备、加密配置、历史、告警、审计和序列。不会批量把全库载入内存。
4. 在同一目标事务内校验记录数、序列数、语义 JSON 内容及更新时间的 SHA-256，核对成功才提交。失败回滚本次目标业务写入；已初始化的空表可能保留。
5. 保存事务内迁移回执，再标记源 SQLite 已迁移，并绑定本地 PostgreSQL 目标。源文件和备份保留。目标提交后若中断，重复命令会校验回执和完整目标，匹配才补完源标记；目标已产生新数据则拒绝覆盖。

开始复制前会写入 `/data/migration.pending`，在完成核对、源标记和目标绑定后删除。该标记存在时普通启动会停止，避免提交结果不确定时继续写旧 SQLite；修复错误后，对相同目标重跑原迁移命令。即使错误表示事务已回滚，也保留标记供明确恢复，不自动重开源库。不要直接删除标记绕过检查；若决定取消，先确认目标没有成功提交，并使用迁移前备份与原密钥恢复到独立数据卷。每次重试默认新建备份；手工指定的备份路径必须不存在，失败留下的备份也不自动覆盖。

成功输出记录数、序列数、SHA-256 与备份路径，然后使用 PostgreSQL 配置启动：

```sh
docker compose -f docker-compose.yaml -f docker-compose.postgres.example.yaml up -d relay
```

核对原账号登录、服务配置、历史范围和告警。迁移不重置账号或更换密钥。成功后保留原备份；正常运行只读写 PostgreSQL，不继续同步 SQLite。若首次目标启动前要退回，可停止所有实例，把迁移前备份与原密钥恢复到独立数据卷后启动 SQLite；目标开始写入后不能靠删配置回滚，必须另做数据核对/迁移，避免丢失新数据。

## 4. 失败与兼容边界

- PostgreSQL 连接一旦显式配置，连接失败就报错，不自动新建 SQLite。后端已绑定后遗漏连接变量也拒绝启动。
- 已有 SQLite 数据不能只添加连接串绕过迁移；默认路径保留源迁移标记。修改路径、换数据卷相当于指定另一个实例，需显式迁移/恢复原数据。
- 已有数据缺失密钥、密钥与数据库指纹不一致、SQLite 未知版本或 PostgreSQL schema 损坏均拒绝启动。不能自动重建密钥或删库。
- 历史 `relay-secrets` 部署可显式设置 `RELAY_DEPLOYMENT_FILE` 与 `RELAY_INITIAL_PASSWORD_FILE` 并挂载原秘密卷，继续使用原 PostgreSQL；首次导入本地密钥时沿用原值。不要把旧 PostgreSQL 卷当作 SQLite 数据卷。
- PostgreSQL 主版本升级仍按官方流程迁移；换 `latest` 镜像不等于完成数据升级。数据库备份与 Relay 密钥分别保留。
