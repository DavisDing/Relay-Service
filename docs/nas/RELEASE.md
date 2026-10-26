# NAS CI、Packages 与 Release

本仓库已提供自动化代码；本地配置和离线检查不等于远端工作流成功，也不代表已发布。2026-10-26 核对远端时尚无 Release 或 Actions 运行记录；首次发布前需提交当前源码与工作流，并获得发布范围授权。当前应用为 M1 基础监控开发预览，数据库/NAS/六服务/客户端的验收边界见 [VALIDATION.md](VALIDATION.md)。

## 1. 触发和检查

- `ci.yml`：PR、main 推送与手动触发，复用 `verify.yml`；只读仓库，不推送镜像。
- `verify.yml`：锁文件构建 Web、Go 模块校验/竞态测试/vet、标签与打包离线测试、Compose 解析、amd64 镜像构建及无数据库容器检查、arm64 镜像构建。
- `release.yml`：推送已有 `vX.Y.Z` 或 `vX.Y.Z-rc.1` 等标签，或手动输入同样的已有标签。标签必须指向默认分支历史中的提交；非法标签和其他仓库在发布前拒绝。

Release 复用同一验证流程，通过后才授权发布 job 写入 Packages/Release。第三方 Actions 固定完整提交 SHA；Node 22.23.4、pnpm 11.25.0、Go 1.27.3 与依赖锁文件定义构建入口。镜像从根目录源码构建页面与服务端，写入版本/修订/提交时间 OCI 标签和 `relay version` JSON。构建阶段基础镜像固定版本标签，未固定上游基础镜像摘要，不能宣称位级可重现镜像；实际交付使用发布后获得的不可变镜像摘要。

不配置 PostgreSQL 测试服务或 `RELAY_TEST_DATABASE_URL`；遵守本轮跳过数据库实测的选择。容器检查只验证非 root、版本身份、CA/ELF、隔离运行及无监听服务时 healthcheck 失败，不验证应用启动、数据库持久化或 HTTP 页面。

## 2. 发布顺序与资产

发布 job 先拒绝覆盖已公开的同名 Release，确认标签提交，构建并推送 `ghcr.io/davisding/relay-service:vX.Y.Z`，包含 linux/amd64 与 linux/arm64。推送后按返回的 digest 拉取 amd64 再检查，随后使用同一 digest 打包部署资产；arm64 只完成构建，未运行验收。

不维护 `latest` 标签；部署和升级使用版本对应的 digest。含 `-alpha.1`/`-rc.1` 的标签标为 GitHub prerelease。初次开发预览可选择 `v0.1.0-alpha.1`，这只是版本建议，尚未创建该标签。

所有资产先上传到 draft，上传和摘要校验成功后才公开 Release。相同标签已有公开 Release 会拒绝覆盖；draft 只允许相同源码提交重试。镜像发布与 Release API 不能组成事务，后续失败可能留下已推送镜像或 draft；应检查失败 job 和标签身份后重试，不把残留 Packages 当完整发布。

| 资产 | 用途 |
|---|---|
| `relay-service-vX.Y.Z-nas.tar.gz` | 完整 NAS 部署目录，包含 Compose、env 模板、秘密生成器和 PostgreSQL 初始化脚本；不含镜像本体或源码 |
| `docker-compose.yaml` | 默认拉取镜像的 Compose；必须与完整包配套 |
| `relay-service-vX.Y.Z.env.example` | 本次 digest 和秘密占位，不能直接当实际 `.env` |
| `release-metadata.json` | 版本、Git 提交、镜像摘要、bundle 名称与 PostgreSQL 策略 |
| `SHA256SUMS` | 上述四个文件的 SHA-256；同源完整性检查，不等于独立发布者签名 |

下载后可用 `sha256sum -c SHA256SUMS`（Linux），或 `shasum -a 256 -c SHA256SUMS`（macOS）核对。完整解包后执行 `sh generate-env.sh .env` 生成独立随机秘密，发布摘要会保留。不要分享生成的 `.env`；具体配置和升级见 [DEPLOY.md](DEPLOY.md)。

## 3. 首次发布和后续版本

发布前确认仓库与公开内容、版本、渠道和依赖许可；需求中的 O-05 不包含在单纯文档维护授权内。先将当前实现和工作流提交到默认分支，确认该提交的 CI 通过，再给同一提交创建版本标签并推送。标签创建/推送会触发发布，不能用它做无意的流水线试运行。

以下仅为经授权后的示例，不表示已执行：

```sh
git tag v0.1.0-alpha.1 <已通过CI的完整提交SHA>
git push origin refs/tags/v0.1.0-alpha.1
```

需要仓库启用 Actions，内置 `GITHUB_TOKEN` 对发布 job 获得 `contents: write` 和 `packages: write`。不需要将长期个人令牌写入仓库。若同名 GHCR 包已有不同权限设置，需在该包设置中授予此仓库访问；首次发布后核对 Packages 可见性和 NAS 拉取权限。公开源码仓库不作为已验证匿名镜像拉取的证据。

Release 页面显示、全部五项资产可下载、metadata/Compose/template 指向相同 digest，以及 `docker buildx imagetools inspect 镜像@sha256:摘要` 的架构结果均应按实际发布提交核对。CI 成功仍不能替代 NAS Projects、PostgreSQL、真实服务与浏览器/设备验收。

## 4. 本地入口

```sh
python3 -m unittest discover -s .github/scripts -p 'test_*.py'
python3 -m unittest discover -s scripts -p 'test_*.py'
# 需要现成 Docker CLI/daemon；不启动数据库
a=unused # 不需要额外环境变量
sh scripts/container-smoke.sh --build
# 需要真实发布digest；输出目录必须不存在
sh scripts/package-release.sh vX.Y.Z ghcr.io/davisding/relay-service@sha256:<实际64位摘要> /tmp/relay-release-assets
```

脚本不安装/启动 Docker，缺少 CLI/daemon 返回 77。打包脚本只从白名单取模板/脚本，拒绝真实秘密、非法版本/镜像、符号链接输入和已有输出目录；离线测试覆盖生成器权限/拒绝覆盖/摘要保留、包结构、校验和与压缩包可重现性。测试中的合成摘要并不代表 registry 中存在镜像。
