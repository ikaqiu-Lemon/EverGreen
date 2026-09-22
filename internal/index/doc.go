// [S4] index/ SQLite+FTS5 与 Storage v3 candidate sidecar：把权威 Markdown
// 的扫描结果落成**可重建的派生索引**。
//
// 允许依赖：model / mdfile（上限；本包当前实测**零本仓依赖** —— 输入是调用方喂进来的
// 中性快照 Snapshot，解析权威 Markdown 这件事不在本包内发生）。
// 施工索引 §13 三条依赖禁令的第一条逐字落地：**index 不得依赖 store 包** ——
// 索引只吃「已经读好的事实」，绝不自己碰权威文件的读写口。
//
// # 三条最高约束（转录 M5 索引架构合同 §1.1，本包一字不得放宽）
//
//	Markdown 是唯一权威来源，.index/ 恒为可重建派生
//	CGO_ENABLED=0 静态单二进制不得被破坏
//	索引不可用时必须降级为全量扫描而非报错退出
//
// 因此本包的形态被这三条钉死：
//
//   - 驱动只用**纯 Go** 的 modernc.org/sqlite（合同 A-41，版本钉 v1.45.0；
//     cgo 驱动永久禁入），`CGO_ENABLED=0 go build ./...` 必须恒退 0；
//   - 本包**零写权威 Markdown**：唯一允许写的路径是 vault 下的 `.index/` 目录，
//     而且整个目录随时可 `rm -rf` 后由 Build 复原（Rebuild 就是这条语义的命令化）；
//   - 本包**只报不改**索引健康度（Inspect 返回诊断码 W23 / W24），
//     「降级成全量扫描」这件事发生在读路径（T-…-067），不在本包内。
//
// # 本包的范围（T-…-065）与明确不做的事
//
// 做：固定 Schema（恰 6 张表 / index_meta 恰 6 键 / IndexSchemaVersion）、
// 全量确定性构建、四类损坏检测、无损重建；Storage v3 另以
// `.index/blocks/<n-id>.json` 投影候选，绝不扩张 SQLite schema。
//
// 不做（各有归属，早做即越界）：
//
//   - 增量更新与陈旧判定（`W22 index_stale`、`eg index sync`）→ T-…-066；
//   - 读路径接入与降级语义（`Q5`、各档结果等价）→ T-…-067；
//   - 排序 / 分页 / `replaced_by` 反向查询 / 性能采样 → T-…-068；
//   - 强原子事务、锁、事务日志、崩溃恢复、退出码 5 → M-006 / S5。
package index
