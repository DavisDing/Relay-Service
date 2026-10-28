# NAS CI、Packages 与 Release

2026-10-26，已发布 [v0.1.0-alpha.1](https://github.com/DavisDing/Relay-Service/releases/tag/v0.1.0-alpha.1) 预发布版，对应提交 `b73f186eeabfb432cf545fab6c679486c8f2fe27`。该提交的 CI 及 [发布流程 #40326802919](https://github.com/DavisDing/Relay-Service/actions/runs/40326802919) 通过。五项公开资产、SHA-256、包内模板/Compose 与源码身份已独立下载核对；GHCR manifest 支持匿名访问并包含 amd64/arm64。当前应用为 M1 基础监控开发预览，数据库/NAS/六服务/客户端尚未完成验收；详见 [VALIDATION.md](VALIDATION.md)。

## 1. 触发和检查

- `ci.yml`：PR、main 推送与手动触发，复用 `verify.yml`。仅本仓库 `main` 推送或在 `main` 手动运行且验证成功后，发布测试镜像；PR、其他分支和 fork 不发包。
- `verify.yml`：锁文件构建 Web、Go 模块校验/竞态测试/vet、标签与打包离线测试、Compose 解析、amd64 镜像构建及无数据库容器检查、arm64 镜像构建和 ELF 架构静态检查（不运行 arm64）。应用两个架构均核对二进制的实际机器类型，防止镜像声明架构与程序不符。配套 PostgreSQL 镜像另做 amd64 构建、脚本/入口/身份检查与 postgres --version，以及 arm64 构建；不启动数据库。
- `release.yml`：推送已有 `vX.Y.Z` 或 `vX.Y.Z-rc.1` 等标签，或手动输入同样的已有标签。标签必须指向默认分支历史中的提交；非法标签和其他仓库在发布前拒绝。

Release 复用同一验证流程，通过后才授权发布 job 写入 Packages/Release。第三方 Actions 固定完整提交 SHA；Node 22.23.4、pnpm 11.25.0、Go 1.27.3 与依赖锁文件定义构建入口。镜像从根目录源码构建页面与服务端，写入版本/修订/提交时间 OCI 标签和 `relay version` JSON。构建阶段基础镜像固定版本标签，未固定上游基础镜像摘要，不能宣称位级可重现镜像；实际交付使用发布后获得的不可变镜像摘要。

不配置 PostgreSQL 测试服务或 `RELAY_TEST_DATABASE_URL`；遵守本轮跳过数据库实测的选择。容器检查只验证非 root、版本身份、CA/ELF、隔离运行及无监听服务时 healthcheck 失败，不验证应用启动、数据库持久化或 HTTP 页面。

## 2. 发布顺序与资产

发布 job 先拒绝覆盖已公开的同名 Release，确认标签提交，构建并推送 `ghcr.io/davisding/relay-service:vX.Y.Z`，包含 linux/amd64 与 linux/arm64。同批构建并发布基于 postgres:latest 的 `ghcr.io/davisding/relay-service-postgres` 同版本双架构镜像；推送后按两个 digest 拉取 amd64 再检查，随后使用两个 digest 打包部署资产；arm64 只完成构建，未运行验收。

按 REQUIREMENT D-16，当前自行测试默认使用 `ghcr.io/davisding/relay-service:latest`。CI 先以 `sha-<完整提交 SHA>` 发布双架构镜像，对已推送 digest 执行 amd64 无数据库容器检查，配套数据库镜像也以同一提交标签发布并通过独立包装检查后，两仓库的 `latest` 才分别指向已检查摘要；跨仓库标签更新不是原子事务，固定发布包则绑定匹配的双摘要。检查失败不更新 `latest`；是否成功发布以该次工作流和 registry 结果为准。测试发布只更新 Packages，不创建 GitHub Release。`latest` 是滚动测试入口，稳定后才发正式版。

版本 Release 仍使用独立版本标签和不可变 digest，Compose 默认值和包内模板保留对应 digest，便于复现。含 `-alpha.1`/`-rc.1` 的标签标为 GitHub prerelease。现有 `v0.1.0-alpha.1` 预发布及资产保留，不因默认测试入口改为 `latest` 而改名或覆盖。版本发布不会改变测试渠道 `latest` 的指向。

所有资产先上传到 draft，上传和摘要校验成功后才公开 Release。相同标签已有公开 Release 会拒绝覆盖；draft 只允许相同源码提交重试。镜像发布与 Release API 不能组成事务，后续失败可能留下已推送镜像或 draft；应检查失败 job 和标签身份后重试，不把残留 Packages 当完整发布。

| 资产 | 用途 |
|---|---|
| `relay-service-vX.Y.Z-nas.tar.gz` | 完整 NAS 部署目录，包含自包含两服务 Compose、可选 env 模板/手工生成器及设置/网络覆盖示例；不含镜像本体或应用源码 |
| `docker-compose.yaml` | 精简 Compose，初始化由镜像自动执行，固定该版本两镜像 digest；可单独导入 |
| `relay-service-vX.Y.Z.env.example` | 本次 digest 和秘密占位，不能直接当实际 `.env` |
| `release-metadata.json` | format_version 2：版本、Git 提交、两个镜像摘要、bundle 名称与 PostgreSQL 基础镜像策略 |
| `SHA256SUMS` | 上述四个文件的 SHA-256；同源完整性检查，不等于独立发布者签名 |

下载后可用 `sha256sum -c SHA256SUMS`（Linux），或 `shasum -a 256 -c SHA256SUMS`（macOS）核对。按 D-18，直接导入自包含 YAML，不需要生成 `.env`、填写宿主 IP 或运行 openssl；内部秘密自动保存到 relay-secrets 卷。首次临时密码见 Relay 部署日志，首次登录必须改密。手工生成器仅保留为可选工具；具体配置和升级见 [DEPLOY.md](DEPLOY.md)。

## 3. 首次发布和后续版本

发布前确认仓库与公开内容、版本、渠道和依赖许可；需求中的 O-05 不包含在单纯文档维护授权内。先将当前实现和工作流提交到默认分支，确认该提交的 CI 通过，再给同一提交创建版本标签并推送。标签创建/推送会触发发布，不能用它做无意的流水线试运行。

以下仅为后续版本操作格式；v0.1.0-alpha.1 已创建，不应重复创建或移动：

```sh
git tag vX.Y.Z-rc.1 <已通过CI的完整提交SHA>
git push origin refs/tags/vX.Y.Z-rc.1
```

需要仓库启用 Actions。内置 `GITHUB_TOKEN` 对测试镜像发布 job 获得 `contents: read`、`packages: write`，对版本 Release 发布 job 获得 `contents: write`、`packages: write`。不需要将长期个人令牌写入仓库。若同名 GHCR 包已有不同权限设置，需在该包设置中授予此仓库访问；首次发布后核对 Packages 可见性和 NAS 拉取权限。公开源码仓库不作为已验证匿名镜像拉取的证据。

Release 页面显示、全部五项资产可下载、metadata/Compose/template 指向相同的两个 digest，以及 `docker buildx imagetools inspect 镜像@sha256:摘要` 的架构结果均应按实际发布提交核对。CI 成功仍不能替代 NAS Projects、PostgreSQL、真实服务与浏览器/设备验收。

## 4. 本地入口

```sh
python3 -m unittest discover -s .github/scripts -p 'test_*.py'
python3 -m unittest discover -s scripts -p 'test_*.py'
# 需要现成 Docker CLI/daemon；不启动数据库
sh scripts/container-smoke.sh --build
# 需要真实发布digest；输出目录必须不存在
sh scripts/package-release.sh vX.Y.Z ghcr.io/davisding/relay-service@sha256:<实际64位摘要> ghcr.io/davisding/relay-service-postgres@sha256:<实际64位摘要> /tmp/relay-release-assets
```

脚本不安装/启动 Docker，缺少 CLI/daemon 返回 77。打包脚本只从白名单取模板/脚本，拒绝真实秘密、非法版本/镜像、符号链接输入和已有输出目录；离线测试覆盖生成器权限/拒绝覆盖/摘要保留、包结构、校验和与压缩包可重现性。测试中的合成摘要并不代表 registry 中存在镜像。

2026-10-28 配置变更需与支持 `RELAY_DEPLOYMENT_FILE` / 首次强制改密的新镜像配套。上述 v0.1.0-alpha.1 是历史发布证据，不代表这些新功能已进入其镜像或当日 latest。

当前精简部署须同批发布应用与 PostgreSQL 配套镜像；本地工作流修改不表示对应 GHCR 包或 latest 已发布。默认不要求外部网络，设置/网络示例需主动合并，详见 DEPLOY.md。
