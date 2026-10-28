# NAS 部署简化验证记录（2026-10-28）

本次范围：D-18，普通 NAS 端口映射无需填写宿主 IP，内部秘密自动生成/持久化，实例临时密码首次登录强制更换。未推送提交、未发布镜像、未连接 NAS 或真实数据库。

## 实现

- deploy/nas/docker-compose.yaml：自包含两服务，内嵌 PostgreSQL 启动/角色脚本；默认端口 8787、既有 ai-network；新增 relay-secrets 命名卷，不需要 .env 或外置脚本。
- deploy/nas/start-postgres.sh / scripts/render-nas-compose.py：原子生成与复用秘密，已有数据库缺失秘密拒绝重新生成；原手工秘密可完整导入。内部目录 root:65532/710，仅 deployment.env 为 65532:65532/600，其余超级用户/所有者文件 root:root/600，Relay 只读挂载。未做真实容器 UID/卷权限验收。
- server/internal/config：允许省略 public URL；严格读取部署文件，不执行 shell；保留显式环境配置。
- server/internal/api：默认按请求 Host/协议验证 Origin，保持 CSRF；不信任 Forwarded 头。HTTPS 反代可固定 public URL。
- server/internal/access / web/src/main.tsx：临时 admin 仅能查看身份、设置正式账号密码或退出；完成时增加凭据版本，使全部临时会话失效。旧账号/会话新字段默认零值兼容，无 SQL schema 变更，缺失初始化标记也不覆盖已有账号。
- 发布包 Compose 默认固定发布摘要，保持单 YAML 导入；手工 .env 生成器保留为可选兼容入口。未新增项目依赖。业务页面和导航保持原布局，登录页新增强制设置表单。

## 通过

- Go 1.27.3：go test -race ./...、go vet ./...，Web 构建后 go test -race -tags webassets ./internal/webui ./internal/api ./cmd/relay、go vet -tags webassets ./...。
- 最后账号保护补充后，相关 access/config/api/cmd 包竞态回归通过；Linux amd64（webassets、CGO=0）构建通过，ELF 为 x86-64。
- web：现有 18 项测试通过，TypeScript 与 Vite 生产构建通过。
- scripts：25 项离线部署/打包/架构检查通过；.github/scripts：9 项标签/发布上下文检查通过。
- 真实启动脚本（以假 PostgreSQL 入口和 chown 记录器替代）：首次随机生成、私有权限、重启复用、完整原秘密导入、已有数据库丢失秘密拒绝、损坏文件拒绝、Compose 内嵌脚本实际 shell 执行和源文件一致性通过；不等同 PostgreSQL 或 Linux UID 实测。
- Ruby YAML 解析、提取后的内嵌 shell / 原始 shell 语法、render-nas-compose.py --check、git diff --check 通过。
- 浏览器：内存 API 夹具 + 实际嵌入页面，未配置 public URL；临时 admin 登录后显示强制设置页面，无管理导航，截图为本任务产物 first-login.png。未在浏览器提交新密码；提交/临时会话失效/新密码重登由真实 Go API 与事务回归覆盖。
- 夹具初次运行结束与 go test 默认 10 分钟期限冲突；已改为在测试期限前自动关停，3 秒期限的监听/自动清理重跑通过。普通回归默认跳过该人工浏览器夹具。

## 未执行

- Docker Compose CLI config：本机没有 Docker CLI。临时官方 Compose 下载完整性校验失败，重试传输过慢已停止，未执行未校验的二进制。已将无 .env 解析及脚本一致性加入 CI，但本次远端 CI 未执行。
- Docker 容器检查：scripts/container-smoke.sh --build 返回 77，明确 NOT_EXECUTED；未安装/启动 Docker daemon。
- PostgreSQL 真实角色权限/SQL 初始化/重启持久化、NAS UGOS Projects、真实网关检查、HTTPS 反代、完整浏览器改密后流程均未验收。
- 新镜像发布：未执行。新 Compose 需要支持 RELAY_DEPLOYMENT_FILE 与首次强制改密的同批镜像，不能把本地代码当成 GHCR latest 已更新。

## 使用

新镜像发布并核实后，导入本次 compose.yaml，查看 Relay 容器日志中的临时密码；浏览器打开 http://NAS地址:8787，按界面设置正式账号。保留数据库卷和秘密卷。源码先行验证可从 deploy/nas 使用 local-build 覆盖文件，见 DEPLOY.md。
