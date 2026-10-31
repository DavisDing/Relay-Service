# DS 审计修复交接（2026-10-31）

范围：订阅来源、展示型伪节点、登录入口指引、页面路由、日志流抖动。本地工作区修改，未提交、推送或部署。按用户安排跳过测试套件，由 Hermes 执行；新增用例只编译，不声称通过。

## 修复与边界

1. **订阅来源**：修正 `mihomoSubscriptionProvider`，Compatible/Inline 有明确正数 total/expire 证据时保留；只有组/内联视图时不伪造机场。服务检查返回 provider_count/embedded_provider_count/subscription_state；工作区 `coverage.subscription_sources` 分开返回核心与控制层来源、数量及可读性。核心只有4组、控制不可读时明确“数量与额度未知”，不静默解释为0。控制失败保留旧配置元数据并提示非当前证据；重启只恢复同服务/代际/设置修订下的 control 元数据，不恢复节点健康或配置绑定。既有活跃worker同步不反复读取快照。
   - 上游只提供组而未提供机场元数据时，Relay无法仅靠核心重建真实机场及额度；仍需控制API或手工元数据。不能把这次修复声称为已从该实例核心读取到2个机场，也不能声称已满足未附在当前报告中的旧三条验收判据。
2. **伪节点**：核心与YAML配置目录共用非节点判定，已知流量/到期标签必须同时具备结构化数值/单位或有效期文案才排除；保留普通含流量/到期字样的节点名。快照版本升到4以要求刷新旧目录；不改写/删除历史。77真实节点+2展示行是回归夹具，真实实例当前数量尚未复核。
3. **登录入口**：固定HTTPS `RELAY_PUBLIC_URL`继续是唯一可信浏览器origin；不开放第二HTTP入口或降低Cookie保护。公开info和origin_rejected响应增加login_url。错误入口页面显示可点击固定地址并禁用登录；前端拒绝脚本URL、userinfo、路径和查询。README明确默认HTTP只适用于未固定PUBLIC_URL的部署。提示自然换行，按钮保留全宽。
4. **路由**：所有当前菜单页与子页都有明确路径，导航pushState，popstate还原页，刷新保持位置；登录失效后保留当前路径。实例选择/筛选不写URL。后端只为已知页面GET/HEAD返回no-store SPA入口；未知API/健康/资源继续404，写方法405。无新增依赖。反向代理仍须将这些页面路径转发Relay，不能自行拦成404。
5. **日志流**：65秒计划换连显示connecting，短暂失败显示reconnecting；失败持续至少15秒后在后续失败重试显示持续断流。认证失败立即提示，取消不报断流。有效帧清除连续失败起点，持续连接至少15秒也视为恢复；响应头本身不清除失败起点，避免快速EOF掩盖故障。

## 已验证与未执行

- 通过：Web TypeScript与Vite生产构建、Go完整嵌入构建、go vet（含webassets）；monitor/API/webui测试二进制编译；Web测试文件语法检查；git diff --check。
- 本机合成API+真实嵌入页面预览：直达 `/network/subscriptions` 显示4组、控制失败与订阅未知提示；菜单跳 `/models/models`，刷新保留模型页，后退返回订阅页，前进回模型页；错误入口展示固定地址且用户名/密码/提交均禁用。截图检查发现提示横向挤压，已修复并重建后复看。合成响应不证明真实NAS接口行为。
- 未执行：Go/Web测试套件、真实NAS/mihomo/PostgreSQL联调、线上日志长时采样、Docker构建和远程CI/发布。无真实密钥读取或上游写入，无付费模型调用。
- 本轮无新增依赖、SQL结构迁移或历史删除。临时预览源与服务已清理。

## Hermes 执行入口

使用项目锁定工具链。以下是交接命令，本轮不执行：

```sh
# server/
go test -race -count=1 ./internal/monitor -run 'Mihomo|Network|CompatibleProvider|AirportInformation|LogStreamRenewal|ProfileMetadataRestore'
go test -race -count=1 ./internal/api -run 'FixedLoginEntry|Origin|Auth'
# 先在web/执行pnpm build，再在server/执行：
go test -race -count=1 -tags webassets ./internal/webui
# web/
pnpm test
```

新增用例：`server/internal/monitor/ds_audit_20261031_test.go`、`server/internal/api/login_entry_test.go`、`server/internal/webui/assets_test.go`与`web/tests/{api,render}.test.mjs`增量。

真实部署重点：记录镜像commit；核对4个Compatible各自是否含subscriptionInfo，以及是否只是/proxies组投影；停用控制接口/错误令牌时应明确未知并保留旧清单，重启不恢复旧健康；恢复后来源回到当前证据。伪节点下一轮采集后不计目录，真节点不误删。固定HTTPS下LAN入口给正确地址且不发会话Cookie；取消PUBLIC_URL仅在专用验证实例验证默认HTTP。逐页刷新/前进/后退、设备权限、未知路径404与反代转发分别验证。日志持续观察跨至少3次65秒计划续连、短暂掉线、持续掉线和401，不能把单帧/响应头成功当长时验收。
