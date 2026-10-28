# 监控扩展实现与 Hermes 测试交接

日期：2026-10-28。工作目录 `/Users/dinghao/Downloads/Relay-Service`。本轮实现保留工作区已有修改，未提交、部署或发布。

用户已要求停止后续测试并由 Hermes 承接。临时浏览器标签与内存夹具进程已关闭。停止夹具时出现的 interrupt/FAIL 是主动停止常驻夹具，不是产品回归失败；本记录不据此声明验收通过。不要连接真实收费模型或改变NAS日志配置来完成下述合成测试。

## 本轮完成

| 接入 | 配置入口 | 具体能力 |
|---|---|---|
| Pipio | 添加服务 → 公开状态页 / 站方探针；地址 `https://pipio.io`；格式 Pipio；路径 `/api/uptime/status`（自动填入） | 读取 `/status` 页面使用的站方模型/API/区域探针状态、可用率、最近24条心跳；不是本地发起推理。上游没有探针时间戳或可用率窗口，界面明确提示 |
| 其他公开 JSON | 公开状态页 → 自定义 JSON 字段 | 同源相对路径、列表字段路径、名称/状态字段、正常/异常值映射；根数组使用 `$`；未知值不算正常 |
| 静态 HTML 状态页 | 公开状态页 → 普通 HTML 显式匹配 | 组件标签、匹配属性和值、名称属性、状态属性或完整可见文本、正常/异常值映射；排除脚本/导航/隐藏元素，不执行页面代码 |
| LiteLLM | LiteLLM 类型下分别勾选模型/部署清单、日志元数据 | `/v1/models`、`/model/info`、最近24小时 `/spend/logs/v2` 最多2页×100条。模型别名、部署、上游模型、匿名端点渠道、明确成功/失败/未知/缓存计数、时间与采样限制 |
| wb2api | workbuddy2api 类型下勾选模型目录；或标准中转站目录类型 | 标准 `/v1/models` 目录。经LiteLLM的调用可从LiteLLM日志观察对应部署；独立日志/额度合同未核实，不猜端点 |
| mihomo | mihomo 订阅与节点质量，填写控制API的origin与secret | `/providers/proxies`、补充 `/proxies`；机场已用/剩余/总额/到期、节点已有延迟/在线证据/记录时间，超过15分钟显示过期；不主动测速、不更新订阅 |

原Web布局、管理员权限、保存前预览、当前快照/历史、阈值告警及设备只读投影继续复用。新增服务配置和检查详情均为可选JSON字段，无SQL结构迁移、无新增依赖。公开状态页永不发送检查Bearer凭据。外部返回不保存原始正文、API Key、用户/团队、提示词、回答、节点连接秘密或原始错误。

## 边界与未完成能力

- 主动推理探针执行引擎尚未实现。本轮接入的是站方公开探针、已有调用样本及目录，`inference_state`继续`not_tested`。后续付费探针必须落实既有全局预算、持久预留、串行许可、暂停、未知费用核对等合同，不能用一次简单补全请求冒充全部能力。
- 日志是窗口快照，可能包含外部探针、重试或工具子调用；不是完整自然请求账本，不得把重叠历史快照相加。没有日志或权限时不能推导零失败。
- 渠道分组是api_base origin的匿名摘要，不是商业服务商身份。目录只是该凭据/网关配置可见的模型，不代表供应商全部在售模型或实际可调用。
- HTML支持显式结构的静态页面；JavaScript空壳、CSS才表达状态、歧义/不闭合标记不自动支持。动态站点需找到公开JSON接口再配置。不是“任意网址自动理解”。
- wb2api具体发行来源仍无唯一证据，独立日志/余额接口为NEEDS_CONFIRMATION。原`/health`合同也未在实际安装验证；可单独使用标准目录能力。
- 未连接用户NAS、真实mihomo/LiteLLM/wb2api，未进行收费推理、主动测速、部署或发布。

## 已有验证和停止点

停止测试前：`go test -race -tags webassets ./...` 七包通过；Web26项测试通过，TypeScript/Vite构建通过；各子模块聚焦测试通过。实际浏览器仅完成Pipio合成预览：异常组件导致整体失败，显示99%/90%及来源时间缺失提示；网关和mihomo的本轮浏览器详情尚未验证。

**这些结果不覆盖最后收尾修改。** 用户要求停测之后又修改了：

1. mihomo投影增加可空的`upload_bytes/download_bytes/total_bytes/expire_seconds`，Web按缺失显示未提供，保留旧info兼容字段。
2. 缺少稳定部署ID的同名不同上游/渠道清单避免误合并。
3. 公开状态解析配置无效时返回明确中文配置原因；详情样式和跳过日志计数文字。
4. 文档合同与本交接说明。

最终源码需要Hermes重新构建及回归，不应把先前构建当最终产物。`server/internal/webui/dist`为本地忽略的构建目录，最后改动后未重建；请先构建Web再做嵌入式UI验收。

## Hermes 建议验收清单

1. 先阅读仓库AGENTS、AI_CONTEXT、COMPATIBILITY和三份来源证据文档；保护已有未提交修改。
2. Web构建、26项既有测试、Go全包竞态、webassets和vet；HTTP夹具需要允许本机临时监听。测试环境命令以AI_CONTEXT为准。
3. `collector_http_test.go`验证Pipio/自定义JSON/HTML的表单预览、零持久化、公开页无Authorization、私网策略、请求上限、规则代际变更、嵌套凭据反射。
4. `public_status_test.go`验证真实Pipio结构、故障与待定状态、0~1可用率、未知时间、脚本/导航/隐藏文字、不完整/重复JSON键、200项外异常仍聚合。
5. `gateway_test.go`检查权限和进程状态分离、无日志/未知状态/缓存、不完整分页、重复请求、窗口边界、去敏投影；补充缺部署ID但同名不同渠道的最终收尾回归。
6. `mihomo_test.go`检查额度已用/剩余、部分缺字段不伪造0、过期/耗尽、缺历史alive默认值、0延迟失败哨兵、15分钟过期、同名不同provider、辅助源失败和截断外故障。
7. 可启动本机内存夹具：`RELAY_UI_FIXTURE_ADDR=127.0.0.1:55447 go test -tags webassets ./internal/api -run '^TestBrowserFixture$' -v -timeout 22m`。合成登录 `admin / this is a test password`。测试地址就是该回环origin，勾指定私网授权；预览支持Pipio、mihomo、LiteLLM目录/部署/日志。不需要真实秘密。
8. 浏览器检查桌面与390px表单、可配置规则、预览失效与保存/编辑、详情空/未知/部分/异常状态；确认新增字段保存后重新打开仍在，设备仍不获得管理配置。结束关闭自己创建的进程/标签。

真实NAS联调必须单独记录安装版本、来源权限、时间和结果，不把合成夹具当真实服务验收。

## 主要文件

- `server/internal/monitor/{public_status,gateway,mihomo,collector_http}.go`：解析、只读采集、网络限制与整合。
- `server/internal/monitor/{types,engine,preview,details}.go`：配置、持久化、预览和检查投影。
- `web/src/{ServiceDialogs,CheckDetails}.tsx`、`types.ts`、`main.tsx`、`presentation.ts`：配置入口、详情与字段往返。
- `docs/nas/PUBLIC-STATUS-SOURCES-2026-10-28.md`、`GATEWAY-SOURCES-2026-10-28.md`、`MIHOMO-SOURCES-2026-10-28.md`：实际接口证据及边界。
