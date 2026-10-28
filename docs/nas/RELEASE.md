# NAS CI、Packages 与 Release

当前源码按 D-19 只构建发布 Relay 镜像，默认 SQLite。PostgreSQL 使用可选官方服务示例，不再构建或发布配套 PostgreSQL 镜像。工作流修改不代表远程 CI、GHCR 或 Release 已运行。

## 1. 触发与验证

- `ci.yml`：PR/main/手动触发，复用 `verify.yml`；仅本仓库 main 验证通过后发布测试镜像。
- `verify.yml`：Web 构建、Go 模块/竞态/静态检查、发布工具测试、Compose 解析，以及 amd64 容器基础检查和 arm64 构建/ELF检查。
- `release.yml`：版本标签或手动指定已有版本标签；标签须位于默认分支历史，验证通过才发布。

工作流保留既有检查，不因本次用户跳过本地测试而关闭。当前未配置 PostgreSQL 集成测试服务，数据库和迁移需由独立测试环境验证。容器基础检查不替代默认启动、浏览器、持久化或 NAS 验收；arm64 仅构建和静态检查。

第三方 Actions 固定提交 SHA，工具链和依赖见工作流及锁文件。Relay 镜像嵌入 Web 页面，并写入版本、修订与构建时间身份。构建基础镜像固定版本标签，未固定全部上游摘要，不宣称位级可重现。

## 2. 镜像与资产

main 测试渠道先推送 `sha-<完整提交>` 的双架构 Relay 镜像，amd64 基础检查通过后更新 `ghcr.io/davisding/relay-service:latest`。测试渠道不创建 GitHub Release。版本发布使用独立版本标签和不可变 digest，不改变测试渠道 latest。

发布先生成 draft，资产上传/校验成功才公开；拒绝覆盖已公开同名 Release。镜像推送和 Release API 不构成事务，失败后可能保留镜像或 draft，须核对提交和工作流后重试。

| 资产 | 内容 |
|---|---|
| `relay-service-vX.Y.Z-nas.tar.gz` | 单 Relay Compose、可选设置/网络/PostgreSQL连接文件、官方数据库服务示例、初始化脚本和 STORAGE.md；不含镜像或业务源码 |
| `docker-compose.yaml` | 默认 SQLite，固定该版本 Relay 镜像摘要，可独立导入 |
| `relay-service-vX.Y.Z.env.example` | 可选参数模板，只有占位秘密 |
| `release-metadata.json` | format_version 3：版本、源码提交、Relay摘要、bundle、默认sqlite及可选官方PostgreSQL镜像策略 |
| `SHA256SUMS` | 四项资产的SHA-256；不等同于独立发布者签名 |

可用 `sha256sum -c SHA256SUMS` 或 macOS `shasum -a 256 -c SHA256SUMS` 核对。默认导入 YAML 后即可运行，无需预生成 `.env` 或填写宿主 IP；首次密码见 Relay 部署日志。迁移/直接连接 PostgreSQL 时下载完整压缩包并阅读 STORAGE.md。

## 3. 发布操作边界

提交、推送、创建标签和发布须在对应用户授权范围内执行。常规顺序是同一提交通过 CI 后，再创建并推送版本标签：

```sh
git tag vX.Y.Z-rc.1 <已通过CI的完整提交SHA>
git push origin refs/tags/vX.Y.Z-rc.1
```

需要 Actions 和仓库 Packages/Release 权限；内置 GITHUB_TOKEN 按 job 最小权限配置，不在仓库存储长期个人令牌。确认 Packages 可见性和 NAS 拉取权限，不能用公开源码推断镜像可匿名拉取。

历史 `v0.1.0-alpha.1` 预发布保持原状，不覆盖或移动；它不能证明本次 SQLite 默认部署或迁移功能已发布。验证结果必须绑定具体提交/digest。

## 4. 本地入口

以下为交接入口，本次按用户要求跳过后续执行：

```sh
python3 -m unittest discover -s .github/scripts -p 'test_*.py'
python3 -m unittest discover -s scripts -p 'test_*.py'
sh scripts/container-smoke.sh --build
# 仅打包；使用实际发布摘要，输出目录必须不存在。
sh scripts/package-release.sh vX.Y.Z ghcr.io/davisding/relay-service@sha256:<实际64位摘要> /tmp/relay-release-assets
```

容器脚本需要现成 Docker CLI/daemon，缺失返回77。打包脚本只读取白名单文件、拒绝秘密和覆盖已有输出，当前调用只接受 TAG、Relay摘要、输出目录三个参数。
