# 网络代理数据来源核对

日期：2026-10-28。用途：为已确认的 Relay 网络代理工作区提供接口与边界依据。只读研究，不代表用户 NAS 接口已认证联调，也不代表新增代码已测试。本文不保存真实订阅 URL、secret、配置原文、节点连接参数或真实日志正文。

## 固定版本

- MetaCubeXD `v1.273.1` Git tag 指向 `8bbc8f58fef71148a94fb5c0ff808f79b057337d`，与用户面板显示的 UI 版本相同。
- mihomo `v1.19.33` Git tag 指向 `3f27ef39f8c96bbfbc8b0b9bb10f5c9ef60149d8`，与用户面板显示的核心版本相同。
- 以上为官方标签与源码合同核对。未认证访问用户管理接口；版本文字相同也不证明镜像未修改、反代路径/鉴权已配置。
- MetaCubeXD 根 LICENSE 为 MIT / Copyright (c) 2023 MetaCubeX。本实现复用 HTTP 合同，不 fork UI 或整套代理服务；若后续复制实质代码应保留对应许可声明。

## 两层数据源

### mihomo 核心

[server.go](https://github.com/MetaCubeX/mihomo/blob/3f27ef39f8c96bbfbc8b0b9bb10f5c9ef60149d8/hub/route/server.go) 注册固定核心入口：

| GET 路径 | 合同与实现注意 |
|---|---|
| `/version` | `meta` / `version`；仅核心版本与进程响应证据 |
| `/traffic` | HTTP 每秒一帧 NDJSON，也支持 WebSocket；`up` / `down` 为当前速率，`upTotal` / `downTotal` 为累计值。不能 `ReadAll` 等待流结束；累计回退应视为新计数区段 |
| `/memory` | HTTP 每秒一帧 NDJSON，也支持 WebSocket；`inuse` / `oslimit`。该版本源码刻意将每个新连接的第一帧 `inuse` 设为 0；只读首帧会一直错误显示 0，应读后续帧或保持流 |
| `/logs` | 新增日志订阅，HTTP NDJSON / WebSocket；普通格式 `type` / `payload`，`format=structured` 为 `time` / `level` / `message` / `fields`。`time` 只有 HH:MM:SS，不含日期；Relay须记录接收时间并标明源时间不完整，不猜绝对日期 |
| `/connections` | 普通 HTTP 为一次当前连接 JSON 快照；WebSocket 为周期快照，见下方源码。不是已结束连接的完整日志 API |
| `/proxies` | 当前内核已加载代理与组及已有 history；不会因为读接口而测试节点 |
| `/providers/proxies` | 内核配置的 proxy-provider 元数据、可得额度与节点。空 provider 清单不表示上层没有 remote 配置文件订阅 |

[connections.go](https://github.com/MetaCubeX/mihomo/blob/3f27ef39f8c96bbfbc8b0b9bb10f5c9ef60149d8/hub/route/connections.go) 的普通 GET 调用 `statistic.DefaultManager.Snapshot()` 并直接 JSON 返回。DELETE 路由会关闭连接，本轮不调用。核心使用 Bearer secret；浏览器 WS 可用 query token，但 Relay 后端应使用 Authorization 头，不把凭据放到 URL。

### MetaCubeXD 配置管理层

[useControlApi.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/composables/useControlApi.ts) 明确：all-in-one 默认 control base 为 **UI 所在 origin + `/api/control`**，桌面模式可由 preload 指定。控制 Token 来自单独的 `CONTROL_TOKEN` / preload 配置，与核心 secret 不是同一合同。用户核心/API 域与 UI 域不同，不能自动把核心 secret 发给 UI origin。

[agent http.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/agent/src/http.ts) 定义：

| GET 路径 | 返回 | Relay 用途与秘密边界 |
|---|---|---|
| `/api/control/info` | agent 信息与 features | 核对 `profiles` / `visual-config-editor` 等能力；不把该公开信息当 profiles 已授权 |
| `/api/control/profiles` | `ProfileMeta[]`，附派生 `active` 标记 | 自动列出多订阅元数据；只能投影安全字段，原列表内含可能带凭据的 `url` |
| `/api/control/profiles/{id}` | `{meta, content}`，content 为 YAML | 可读取未激活配置的已有内容，但含节点秘密，只能后端内存受限解析安全节点元数据，不能存原文或转给前端 |
| `/api/control/profiles/{id}/editor` | editableYaml、composedYaml、composition、诊断等 | 仍是完整配置，**不是安全节点 JSON**；不能当脱敏接口，单纯节点目录无需调用 |
| `/api/control/config/section?key=...` | 当前激活 profile 的指定 YAML 解析段 | 只针对 active，不能据此枚举未激活订阅；proxies 段也可能含秘密 |

不调用 create/update/delete/import/refresh/activate/validate/editor-preview 等写或可能触发下载/重启的路径；不读取 `kernel/status` 来获取核心凭据，其返回内有 `secret`。

`ProfileMeta` 字段见 [types.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/agent/src/types.ts) 与 [UI control.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/types/control.ts)：

```text
id, name, type(local|remote|merge|script), updatedAt(Unix毫秒), active(可选)
subscriptionInfo: {upload, download, total, expire}  // 字节；expire为Unix秒
updateInterval: 分钟
```

`url`、`userAgent`、完整 YAML、原始错误与诊断不进入 Relay API。`merge` / `script` 是覆盖层/脚本，不作为独立机场订阅统计。[profiles.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/agent/src/profiles.ts) 从机场 `subscription-userinfo` 响应头生成额度，缺字段可能被上游默认成 0，因此 Relay 无法恢复“原始字段是否缺失”；total=0不能当额度用尽，expire=0不能伪造一个到期时间。

未激活 profile 可以有本地节点清单，但不一定进入当前核心。节点目录可读不等于有实时延迟。若配置里仅引用 provider URL，读取 YAML 不能直接得到所有远端节点；不得借机自动访问第三方订阅 URL。受限解析不能支持的 YAML/配置形态必须显示不支持或未覆盖，不能静默当零节点。

## 原 Web 的历史存放位置

- [useDataUsage.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/composables/useDataUsage.ts) 从 `db.query(start,end)` 统计用量；[utils/db.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/utils/db.ts) 是浏览器 IndexedDB `metacubexd_db` / `data_usage_logs`。
- [stores/connections.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/stores/connections.ts) 根据同一 connection ID 的累计上传/下载增量，按分钟和来源IP/目标/出口/进程等缓冲写入 IndexedDB。这个用量不是机场账单，也不是核心可回读的持久请求账本。
- [stores/global.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/stores/global.ts) 的概览图表为 Pinia 内存长度受限数组；[stores/logs.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/stores/logs.ts) 的日志也是内存有限行缓冲。
- [useWebSocket.ts](https://github.com/MetaCubeX/metacubexd/blob/8bbc8f58fef71148a94fb5c0ff808f79b057337d/packages/ui/composables/useWebSocket.ts) 建立 connections/traffic/memory/logs 四路核心实时数据流。

因此直接读取核心 API 不会自动继承用户浏览器此前“用量”数据，也不能回溯之前24小时的所有日志/节点检测。Relay 应独立持续采集和保存，标明接入时间、断流与缺口。若需导入原 Web 历史，必须另做明确、受限的导出格式及去重；不读取整个浏览器存储作为常规采集。

## 接入与验收建议

1. 连接项保留核心 origin/secret；另设明确的控制 origin 与独立加密 token。控制 origin 应由用户确认，不能静默从 UI 链接获取并向它发送核心秘密。
2. 优先 GET profiles 自动同步所有元数据；每个detail仅读取固定ID路径，限制响应、数量、并发、解析成本；投影后立即丢弃配置原文。
3. 对 YAML 节点清单采用可靠解析并只取 name/type 等白名单字段。若仅支持受限形态，必须显式标明源解析范围；同名节点、组、DIRECT、provider引用分别处理，不能靠名称猜跨订阅归属。
4. 所有未激活订阅持续检测需要额外的核心加载/provider配置或独立检测环境。单纯读取现有接口不能凭空获得未执行测试的延迟；本轮不修改用户现有 mihomo 配置。
5. Hermes 验证固定版HTTP流（特别是memory第二帧）、两套认证、provider空但profiles非空、多订阅inactive节点、计数器重置、上限截断、secret不落日志、连接短采样缺口及断流恢复。本轮未执行测试、构建或真实NAS认证接口验证。
