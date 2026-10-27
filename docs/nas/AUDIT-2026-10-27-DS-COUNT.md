# 2026-10-27 DS Count 提交审计复核

复核对象：`d5ffc52`（历史/审计预览计数与分批清理）。真实 PostgreSQL、Docker、NAS 仍未联调。

| 项目 | 判断 | 处理 |
|---|---|---|
| N10 Count 与 `expiredCheck` 零值时间语义不一致 | 真实防御性缺陷 | PostgreSQL Count 使用 `NULLIF` 与显式 `0001-01-01` 排除条件；补 Memory 历史/审计零值回归测试 |
| N11 Count 错误消息包含桶名 | 对外 API 不成立，API 将内部错误映射为通用 500；内部风格仍可收敛 | 改为 `count records`，不暴露 bucket 名称 |
| N12 AuditRecord 零值时间 | 与 N10 同源的防御性缺陷 | 由 Count 的统一零值排除覆盖；不添加无效的 `omitempty` 到 `time.Time` |
| N13 卡片纵向膨胀 | 真实低优先级 UI 问题 | 宽屏启用三列服务卡片和四列字段网格，减少纵向占用；保留窄屏布局 |
| N14 startupCleanup channel | 当前缓冲容量为 1，完成后发送不会阻塞泄漏 | 不修改 |
| N15 Count Prefix 未测试 | 真实覆盖缺口 | 增加 Prefix 计数测试 |

验证边界：未执行真实 PostgreSQL `EXPLAIN` 或迁移；SQL 表达式与现有索引的 `NULLIF` 形式保持一致，最终执行计划仍需专用测试库确认。


后续部署核验发现：002 的 JSON 文本转 timestamptz 表达式使用 STABLE 输入转换，PostgreSQL 不允许将其用于索引，因此“形式一致”不足以证明索引可创建。按用户确认的“当前无存量数据”，直接修正开发期首装基线 002：普通 retention_at 时间列、由 JSON 派生该列的触发器及普通索引；Count 同步改用该列。新安装顺序执行 001/002，不包含旧版 002/003 兼容或数据回填；缺口/未知版本仍拒绝。真实数据库测试按用户此前选择仍未执行。
