# Relay 项目文档

更新日期：2026-10-26。需求与设计统一为 **v1.4-draft**；按用户澄清新增 D-14，并按 D-15 确认 NAS 服务端采用 Go，其他确认日期和历史依据各自保留，文档更新不代表开发完成或实机验收。

## 阅读入口

| 文档 | 用途 | 当前状态 |
|---|---|---|
| [AI_CONTEXT.md](AI_CONTEXT.md) | 开始任务先读：仓库现状、协作边界、检查入口 | 已有 Go/React M1 基础监控代码，数据库/Docker/NAS 未验收 |
| [REQUIREMENT.md](REQUIREMENT.md) | 做什么：范围、36 个 FR、60 个 AC（3 个历史场景当前不适用）、D/T/E/O 登记 | v1.4-draft；D-03/D-04 待确认，验收为草案 |
| [DESIGN.md](DESIGN.md) | 怎么做：架构、合同/数据、权限、客户端、工作包 | v1.4-draft；Go 语言已确认，其他技术细则 PROPOSED，依赖/接口和环境尚待验证 |
| [NAS_SETUP.md](NAS_SETUP.md) | 当前 NAS 首次接数据库/用量日志的操作手册 | 保留 2026-10-13 基线；未在 NAS 执行，正文关闭 |
| [nas/DEPLOY.md](nas/DEPLOY.md) | Relay 镜像部署、源码构建与角色迁移入口 | 开发预览，数据库/Docker/UGOS 未执行 |
| [nas/RELEASE.md](nas/RELEASE.md) | CI、GHCR Packages、Release 资产与镜像摘要 | 自动化已配置；特定提交 CI 通过，版本发布未执行 |
| [COMPATIBILITY.md](COMPATIBILITY.md) | 六服务固定检查端点、证据与能力限制 | fixture 已测；安装版本与 AC-60 未验收 |
| [nas/VALIDATION.md](nas/VALIDATION.md) | 本轮代码交付、依赖、实际验证和缺口 | 不等于完整首期验收 |
| [DEPLOYMENT.md](DEPLOYMENT.md) | 数据库接入原理、通用方案、后续模型迁库/升级恢复 | 保留 2026-10-13 基线；不是本项目完整部署资产或实测结果 |

需求/设计是本次配套更新的版本；两份部署文档保留原日期，执行时须核对实际版本，不因导航更新而视为已复验。`COMPATIBILITY.md` 已补本轮适配矩阵；`PROBE_POLICY.md` 仍为后续工作包产出。

## 按目的阅读

- **确认范围**：REQUIREMENT 0.4 总览 → 第 4～10 节 FR → 第 11 节首期分期 → 第 12 节验收 → 第 13 节决策/闸门。
- **开展设计或实现**：DESIGN 0.2 导航 → 2.3 架构 → 5.2 运维 Web → 10.6～10.10 技术/合同/客户端 → 第 12 节工作包。
- **在现有 NAS 接通用量日志**：NAS_SETUP 顺序操作；DEPLOYMENT 用于查阅原理与后续迁库。首次接日志保留现有文件模型，不自动启用数据库模型管理、正文或付费探针。

## 本版边界

NAS/Web、Mac、Android 同仓规划；NAS Docker 中的 Relay 服务端语言已确认为 Go（D-15），已有锁定依赖与基础监控实现，实际数据库/容器/服务版本仍待验证。必须提供 LiteLLM、Hermes、workbuddy2api、Open WebUI、Ollama、MetaCubeXD 及 Relay 自身的运维监控；调用 Key 的创建、分发、轮换、撤销及预算编辑不列必需交付（D-14）。监控使用管理员配置的既有凭据，无需先启用调用 Key 管理或迁移 LiteLLM 模型。

六服务基础监控须有可验证的无推理检查、历史、故障/恢复和告警；缺安全检查路径是阻塞，未知态/链接本身不算完成。目录、日志、额度、应用版本和镜像更新等增强信息按实际接口与权限提供，缺失时标明原因。基础监控可独立于调用 Key 管理、LiteLLM 数据库和付费探针运行。Go 服务端与 React/TypeScript Web、Mac/Android 原生客户端协作，技术细节与验收见设计文档。

统一运维 Web 可管理 Relay 自身配置；LiteLLM 模型/路由保留原独立管理范围，其他外部服务只读接入。Ollama 允许无推理状态/目录/驻留总览，暂缓统计、模型管理和主动探针。镜像更新仅在安全只读证据充分时判断，无运行 digest 则显示未知并提供人工核对。数据库/用量日志手册用于补齐相应数据能力，不是其他服务基本监控的前置条件。

workbuddy2api 基础运维监控已确认；D-03 只决定独立额度/直连统计及非文本主动测试范围；D-04 决定正文内容/来源/接入与失败策略。技术版本、Android 分发/后台方案和真实环境验证由设计/实施补齐，不要求重新选择已确认产品方向。

线上写入、付费调用、基础设施变更和发布按 REQUIREMENT O 项明确范围。本轮已新增代码与离线验证入口；单元/fixture结果见 nas/VALIDATION，数据库实测按用户选择跳过，Docker/NAS/浏览器未验收。完整首期需求保持不变，不把 M1 切片算作全部交付。
