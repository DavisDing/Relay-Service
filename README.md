# Relay Service

NAS 服务端与 Web 运维控制台，采用 Go + PostgreSQL + React/TypeScript。当前为 M1 基础监控开发预览：管理员/只读设备、服务检查、历史、故障/恢复事件、静默与已读位置。

当前用于自行测试，Docker 默认使用 `ghcr.io/davisding/relay-service:latest`。`main` 通过 CI 和镜像基础检查后更新该标签；它代表最新测试构建，稳定后才发布正式版。

```sh
docker pull ghcr.io/davisding/relay-service:latest
```

拉取镜像后，按 [NAS 部署指引](docs/nas/DEPLOY.md) 配置数据库、秘密与启动初始化；仅执行 `docker pull` 不会更新已运行的容器。

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

启动需要真实 PostgreSQL、独立角色与环境配置，按 NAS 部署文档启动时检查并自动初始化；测试默认使用内存Store/本机HTTP fixture。数据库集成测试需专用 `_test` 数据库和显式 `RELAY_TEST_DATABASE_URL`，默认跳过。

已发布 [v0.1.0-alpha.1](https://github.com/DavisDing/Relay-Service/releases/tag/v0.1.0-alpha.1) 开发预览和 GHCR 双架构镜像，提供带摘要的完整 NAS 部署包。CI、Compose 解析和 amd64 容器基础检查通过；arm64 只构建。真实数据库、UGOS、浏览器与六服务安装版本尚未验收。workbuddy2api 安全检查合同与 MetaCubeXD 变体需核对，完整首期能力见需求文档，不能将本版视为全部完成。
