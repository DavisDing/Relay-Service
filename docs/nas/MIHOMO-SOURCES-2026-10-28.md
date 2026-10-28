# mihomo 订阅与节点质量采集合同

2026-10-28：本轮扩展 `mihomo` 监控类型，读取用户明确配置的控制 API origin 和已保存 secret。现有 `mihomo` 类型的只读监控扩展为最多两次固定 GET，沿用统一 DNS/IP 检查、禁止重定向、超时及凭据加密边界。没有新增依赖，不扫描其他地址。

## 上游合同与证据

核对的上游是 MetaCubeX/mihomo 的 `Meta` 分支源码，以下链接是会变动的源码，不能代替用户安装版本验收。

- [adapter/provider/provider.go](https://github.com/MetaCubeX/mihomo/blob/Meta/adapter/provider/provider.go)：provider JSON 含 `proxies`、`updatedAt`、可选 `subscriptionInfo`。`testUrl`、`expectedStatus` 等字段不保存。
- [adapter/provider/subscription_info.go](https://github.com/MetaCubeX/mihomo/blob/Meta/adapter/provider/subscription_info.go)：订阅头 `subscription-userinfo` 转为 `Upload`、`Download`、`Total`、`Expire`；不同机场不一定提供这些数据。
- [hub/route/provider.go](https://github.com/MetaCubeX/mihomo/blob/Meta/hub/route/provider.go)：`GET /providers/proxies` 仅返回已有 provider 状态。`PUT` 刷新和 `/healthcheck` 有副作用，不调用。
- [hub/route/proxies.go](https://github.com/MetaCubeX/mihomo/blob/Meta/hub/route/proxies.go)：`GET /proxies` 仅返回已有代理状态，补充不属于 provider 的手动节点。`/delay` 会发起测试，不调用。
- [adapter/adapter.go](https://github.com/MetaCubeX/mihomo/blob/Meta/adapter/adapter.go)：序列化 `name`、`alive`、`history`；`alive` 初始化为 true，因此 `alive:true` 与空 history 不能证明节点在线。URLTest 错误留下 delay=0；当前源码将低于1ms的成功钳制为1ms，0为失败哨兵。

## 本地投影与判断

- 每个响应最多1MiB；每类投影最多200条。`node_count`、状态统计和订阅故障判断覆盖完整已接收响应，`truncated` 明示显示被截断。
- 订阅保存名称、上游更新时间、上传、下载、总额度、到期、已用及剩余字节。上传或下载缺失时不计算已用；总额度缺失或为0时不计算剩余，不把0解释为已用尽。上传与下载之和溢出、负数及不合法到期拒绝解析。
- `usage_state`：`unknown`（无法完整计算额度）、`available`（已用小于明确总额）、`exhausted`（明确用尽）、`expired`（明确到期，优先于额度）。`available`仅指额度，不表示订阅网址可访问或节点可用。
- 节点只保存 provider、显示名、协议类型、上游 alive、最新有效历史的延迟与时间、显示新鲜度及证据状态。不保存原始响应、server、IP、UUID、密码、订阅URL、测试URL、额外测试URL map、流量连接或配置。
- 同一个provider内按节点名称去重，同名不同provider保留。`/proxies`补充provider外节点；分组、DIRECT、REJECT等不作为机场节点计数。
- 最新有效历史15分钟以内为 `fresh`；超过为 `stale`；未来时间为 `future`；无有效时间/延迟为 `missing`。15分钟是本产品显示策略，不是mihomo SLA或延迟历史采样频率。
- 只有 fresh 历史参与节点可用计数：`alive:false` 或 `delay=0` 为失败；`delay>0`且alive不为false为已观测成功。过期/未来/缺失记录为未知，不保留旧成功作为当前在线。
- 节点结果仅对应上游最近一次测试目标与条件，不能推断视频解锁、带宽、丢包、所有目的地可达或当前业务调用质量。未执行主动测速。
- `/providers/proxies`为必要来源，失败使本次检查失败；辅助 `/proxies`失败/结构不支持时保留订阅和provider节点证据，`proxy_source_state`明确 `unavailable` / `invalid_response`，不复制原始错误。正常补充为 `available`。

## 验证边界

新增针对真实字段形态的合成合同测试：额度/到期/缺失/溢出、默认alive与空历史、零延迟、失败、过期及未来历史、最新时间选择、分组排除、同名去重、200条截断、两次固定路径、辅助失败保留证据、秘密字段不进入投影。最终执行结果见本次会话/集成验收记录。

没有连接用户真实 mihomo、机场订阅或NAS，也未执行主动测速；真实安装版本合同与自然产生的历史数据仍需环境联调，不能把上述解析测试当作已验收用户节点质量。


整合补充：订阅投影的 `upload_bytes/download_bytes/total_bytes/expire_seconds` 为可空字段，区分上游缺失与真实0；旧 `info` 仅为既有快照兼容保留。UI以新增可空字段显示新记录，缺失时标未提供。此收尾在用户要求停止测试后完成，最终回归交Hermes，见MONITOR-EXTENSIONS-HERMES-HANDOFF-2026-10-28.md。
