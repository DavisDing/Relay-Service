# 精简 Compose 本地验证（2026-10-28）

本次在 D-18 简化首装基础上，按用户补充要求将默认 Compose 缩为 24 行：仅两镜像、端口映射、两持久化卷、重启及数据库就绪依赖。使用 Compose 默认项目网络；不自动接入 ai-network，不要求 .env 或宿主 IP。

数据库包装入口与角色脚本搬入 deploy/nas/Dockerfile.postgres，基于官方 postgres:latest。Relay 镜像包含默认部署文件路径，两镜像均声明健康检查。默认参数由应用/镜像承担，需要时在 Compose 添加 environment；秘密首次生成后仍以持久化值为准，不自动轮换已有数据库口令和加密主密钥。设置与已有网络示例需主动合并。

源码构建覆盖文件同时构建两个镜像。CI/Release 已修改为同批构建/发布两个镜像并进行 amd64 包装检查，Release 部署资产绑定双 digest，metadata 格式版本更新为 2。移除旧 Compose 脚本生成器。此前 DEPLOY-SIMPLIFICATION-2026-10-28.md 记录的是旧内嵌方案，不代表本次结果。

本次实际验证：

- scripts 离线测试 25 项通过，包含秘密首次生成/重启复用、缺失或损坏秘密拒绝、旧完整秘密导入、打包双摘要和模板、禁止秘密混入、拒绝非法摘要/覆盖、压缩包可重现性。
- .github/scripts 标签/发布上下文测试 9 项通过。
- Go internal/config 测试通过，覆盖部署文件加载、显式配置优先、可选 public URL 与校验错误。
- 四个 Compose 文件与三个工作流通过 Ruby YAML 语法解析；shell 语法、工作流内 Python/shell 静态语法检查及 git diff --check 通过。语法解析不等同 docker compose config。
- Docker CLI/daemon 不可用，container-smoke.sh --build 返回 77；未执行镜像构建、docker compose config、配套镜像 smoke、数据库启动/权限/持久化、NAS 导入。
- 应用源代码/Web 未更改，本轮未重复完整 Go/Web 回归；上轮结果见此前记录。

本轮未提交、未推送、未发布镜像，也未触发远程 CI。新 Compose 依赖本次应用镜像及新增数据库配套镜像；对应镜像尚需实际构建发布，不能将本地工作流或文件存在当作 registry 可拉取证据。

同日后续 DS 审计修复改变了临时凭据存储/秘密卷写权限，最新行为及验证见 INITIAL-CREDENTIAL-AUDIT-2026-10-28.md；本记录保留此前精简步骤的验证范围。
