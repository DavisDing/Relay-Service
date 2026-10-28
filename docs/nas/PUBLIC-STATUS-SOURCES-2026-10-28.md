# 公开状态页采集与来源核实（2026-10-28）

本记录对应公开状态页解析器，真实服务验收与夹具测试分开记录。公开状态是供应商报告的证据，不是 Relay 使用当前账号执行成功的推理结果，也不证明该账号或每个渠道可调用所有列出的模型。

## Pipio 公开来源

本机通过未认证、只读 HTTPS GET 核实：

- `https://pipio.io/status` 返回 React 页面壳；HTML 本身不含各模型状态。
- 页面公开脚本 `https://pipio.io/app-assets/assets/status-page-DOG0lVYi.js` 声明 GET `/api/uptime/status`，缓存30秒、轮询60秒。脚本仅作为文本检查，没有执行脚本。
- `https://pipio.io/api/uptime/status` 返回 JSON：`{success:true,data:[{categoryName,monitors:[{name,status,group,uptime,heartbeats}]}]}`。实际访问成功取得模型、核心API、区域三个类别的16项公开状态；此数量和实时值会变化，未作为产品常量。
- 页面公开脚本 `https://pipio.io/app-assets/assets/status-DvTAOp5_.js` 明确映射：`0=outage`、`1=operational`、`2=pending`、`3=maintenance`、其他=`unknown`。Relay 对应 `unhealthy / healthy / unknown / unhealthy / unknown`，pending 不冒充健康。
- 实际 JSON 没有源更新时间。页面显示的 `dataUpdatedAt` 是本次前端取数时间。Relay 分开保存本机 `observed_at`，不伪造 `updated_at`。
- `https://pipio.io/analytics` 当前路由属于认证页面，未登录检查只取得页面壳；没有用私有analytics、账户余额或日志替代公开状态源，没有输入 Key 或发送推理。

配置 `format=pipio`、`resource_path=/api/uptime/status`。模型名称/类别/分组、当前状态、0～1的可用率和最后24个心跳经白名单投影；不保存链接、用户字段、任意错误文本或原始响应。可用率窗口和心跳间隔未由公开响应声明，UI不得推定为“过去24小时”。

本轮网络环境下 Web fetch 未返回可读正文，使用获准的 curl HTTPS GET 核实页面和上述公开数据，未对远端写入。该结果仅证明检查当时公开端点和结构可用，不是全天可用率或NAS联调证明。

## 通用 JSON 与 HTML

- `statuspage` 支持组件列表及已知 Atlassian Statuspage 状态；只统计非group组件。未知状态保留unknown，不当成功。可读取明确RFC3339的源更新时间。
- `json` 支持点分对象路径（如`report.services`、`service.label`、`state.code`）和明确健康/故障值映射，`items_path=$`可选择根数组；不执行JSONPath表达式或脚本，拒绝重复键和超过64层的嵌套。默认识别Statuspage组件结构和Pipio公开结构，不凭HTTP 200或`success:true`判断模型健康。
- `html` 必须配置标签、匹配属性、组件名称属性及健康/故障值；匹配属性值可进一步限定。状态取独立属性，或匹配元素的完整可见文本。没有整页关键词、子串或正则“猜健康”。脚本、样式、模板、导航、页头页尾、hidden/aria-hidden及常见内联隐藏元素不贡献状态。
- HTML扫描器不增加依赖、执行脚本、抓取图片或二次资源；不会把上游HTML放进UI。为避免浏览器容错产生歧义，错误嵌套、未闭合标签、重复属性和嵌套候选组件直接拒绝。只支持结构明确、显式闭合的静态HTML；依赖外部CSS决定状态、JS动态渲染、省略结束标签的HTML、验证码/登录页不能通用推断，需要提供JSON源或专门适配。缺匹配组件为unknown，不报告全健康。
- 响应上限1MiB、输出最多200项；全量扫描后判定整体状态，截断末尾的故障不会遗漏。HTML深度上限256，每个文本状态上限4096字节。来源仅保存相对路径，不含URL、query或fragment；请求仍须经过主检查器既有SSRF、凭据、超时和体积限制。

## 验证范围

`public_status_test.go`使用合成数据验证真实Pipio结构及数字状态映射、自定义JSON路径、多组件和源时间、HTML属性与完整文本映射、脚本/注释/导航/隐藏内容排除、未知/错误结构、1MiB限制、200项截断后故障汇总、字段白名单与无原文错误回显。合成用例不代表真实服务推理、私有日志、NAS或浏览器的完整联调验收。

解析器独立检查入口（`server/`）：`go test ./internal/monitor -run '^(TestPipioPublicStatus.*|TestPublicStatus(Standard|HTML|Limits|Config|JSON).*)$' -count=1 -v`。10组测试通过；更宽的`PublicStatus|Pipio`筛选也会匹配主任务新增的HTTP集成夹具，需本机回环监听权限。最终集成结果由主任务统一验证。
