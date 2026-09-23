# EverGreen Storage v3 开发计划

- 日期：2026-09-22
- 状态：执行中
- 产品接续基线：`ff9868652b685eae4502ad0b546e3912724117c0`
- Teamwork 接续基线：`72e607f4ef06d3366a09a1f788919eec6eab03e1`
- 产品集成分支：`feature/block_boundary_materialization/integration`
- 正式合同：`docs/specs/2026-09-22-storage-v3-contract-decisions.md`

## 1. 基线事实

- T-008 已完成并验收；EverGreen `feature/knowledge_opinion_split/integration`
  位于 `ff986865`，版本为 `0.7.0-m7`。
- 旧 T-009（真实 Vault 迁移）与 T-010（最终门禁/发布）均为 `created`，未启动。
- 真实 Vault 未迁移，`mark-reviewed` 继续流程冻结。
- 产品仓已经依赖 goldmark v1.8.6，`internal/mdfile/assets.go` 已用只读 AST。
- txn journal v1 已支持完整多文件 write-set、前像、intent 屏障、两阶段提交和恢复。
- `eg context` 仍保留 D-3 兼容字段 `candidates`；进入 `0.8.0-m8` 时必须移除。
- 2026-09-21 旧方案仍有调研价值，但“移动候选正文、不用 goldmark、journal v2”三项
  已被正式合同替代。

## 2. 执行顺序

| 批次 | Teamwork Task | 分支 |
| --- | --- | --- |
| C1 | `T-evergreen.block_boundary_materialization-158614-001` | Teamwork only |
| B1 | `T-evergreen.block_boundary_materialization-158614-002` | `feature/block_boundary_materialization/h3-candidate-drafts` |
| B2 | `T-evergreen.block_boundary_materialization-158614-003` | `feature/block_boundary_materialization/h3-materializer-core` |
| B3 | `T-evergreen.block_boundary_materialization-158614-004` | `feature/block_boundary_materialization/h3-txn-recovery` |
| B4 | `T-evergreen.block_boundary_materialization-158614-005` | `feature/block_boundary_materialization/h3-cli-e2e` |
| L2 | `T-evergreen.block_boundary_materialization-158614-006` | `feature/block_boundary_materialization/l2-fenced-div` |
| sidecar | `T-evergreen.block_boundary_materialization-158614-007` | `feature/block_boundary_materialization/candidate-sidecar` |
| migration | `T-evergreen.block_boundary_materialization-158614-008` | `feature/block_boundary_materialization/migration-dry-run` |
| final | `T-evergreen.block_boundary_materialization-158614-009` | `feature/block_boundary_materialization/final-gates-m8` |

### C1：D1-D6 合同冻结

交付：

- H3/L2 候选线格式与边界；
- 草稿保存/编辑和 draft coverage；
- Knowledge/Opinion 模板载荷映射；
- 持久 output 映射与跨日幂等；
- journal v1 多文件事务复用；
- materialize/export/query/sidecar/version/迁移合同。

出口：合同无悬置项；B1-B4、L2、sidecar、迁移和收口任务均能只读合同开工。

### H3 首期 B1：候选结构与草稿

范围：

- goldmark H3 + attribute parser；
- `eg:cd:1` candidate anchor 编解码；
- `write_note.candidate_drafts[]` / `candidate_coverage[]`；
- Note `提取结果` 的候选草稿 writer/reader；
- `eg edit` candidate 模式；
- draft/final coverage 严格分离。

聚焦验证：`internal/mdfile`、`internal/store`、`internal/plan`、`internal/cli` 直接相关单测，
对应 manifest 自检、`make lint`、`scripts/check-public.sh`。

### H3 首期 B2：模板映射与确定性物化内核

范围：

- Knowledge/Opinion H4 模板校验；
- payload span 字节复制；
- 首次 ID/路径生成；
- candidate output 映射写回；
- materialize plan/action/store 内核；
- Opinion `validation: pending`；
- 已物化候选漂移与目标冲突 fail closed。

聚焦验证：模板矩阵、字节等价、ID 冲突、同日/跨日幂等、Note 非目标区间不变。

### H3 首期 B3：journal v1 多文件闭环

范围：

- materialize accepted write-set 接入现有 atomic overlay；
- 目标文件与 Note 同一 journal v1；
- 复用 run.lock、Recover、Commit、Git 单提交和写后索引；
- 多候选同事务；
- 每个既有 failpoint 的物化恢复覆盖。

聚焦验证：`journal_version == 1`、2/N 文件 failpoint、恢复两态、B3 hash 冲突、
Git 失败后 Markdown 保持目标态。

### H3 首期 B4：CLI、查询、导出与真实 E2E

范围：

- `eg materialize`；
- `eg export --plain`；
- `eg context` / Note 查询接入 draft candidates；
- 命令名册与四份文档双向恰等；
- 版本推进 `0.8.0-m8` 并履行 Schema v2 D-3：删除 `context.candidates` 与弃用 I1；
- 一份完整 Note 同时物化至少一张 Knowledge 和一条 Opinion 的真实闭环。

聚焦验证：CLI 参数/退出码、plain export 内容守恒、查询集合、文档/帮助名册、
真实 Note→Knowledge+Opinion E2E。

H3 B1-B4 每批独立提交；每批只跑新增/修改行为与直接受影响接口，不提前跑完整七档。
B4 集成完成前必须确认 B1-B4 task inbox 无未处理项。

### L2：fenced div 兜底

在 H3 闭环稳定后实现 D1.2。只新增 Span provider，复用 B2-B4 的 candidate、模板、
物化、事务、查询和导出内核。验收 H3/L2 对同一载荷产出等价，嵌套、未闭合、代码围栏
内伪标记均 fail closed。

### Sidecar：可重建候选派生层

实现 `.index/blocks/`：

- 只投影 Markdown 中已有的 candidate key/kind/span/output/hash；
- `eg index build|rebuild|sync|status` 纳管；
- 删除 sidecar 后可重建并逐字等价；
- SQLite 六表与 `index_meta` 六键不变；
- 查询加速结果与 H3/L2 直接扫描逐项等价。

### Migration：工具与副本演练

一次性工具落 `scripts/migrate-storage-v3/`，不新增长期 CLI 命令：

1. dry-run 输出逐文件 diff、候选/输出映射和 coverage 变化；
2. fixture 演练；
3. 真实 Vault 的完整副本演练；
4. `eg check` / `eg reconcile` / `eg index rebuild` / plain export 对账；
5. 记录哈希、计数、错误与回滚方法。

旧 T-009/T-010 不直接启动；其尚未执行的迁移与最终门禁职责由本 Epic 新任务承接，
详见 `docs/specs/2026-09-22-migration-carryover.md`。

### Final：固定 SHA、聚合回归、真实 Vault 与收口

1. 合并全部计划代码并固定最终产品 SHA。
2. 以 `LC_ALL=C` 在同一 SHA 集中运行 core/full/race/perf/manifest/mutation/fuzz、
   `make lint`、`scripts/check-public.sh` 与脱敏检查。
3. 禁止 skip、xfail、allowlist、frozen-red、吞失败或弱化断言。
4. 只有全部聚合回归通过后，才对真实 Vault 执行一次迁移。
5. 真实迁移后重跑迁移验收、解除 `mark-reviewed` 冻结、完成版本/交付与 Epic 收口。

## 3. 分支与提交

- 从 `feature/knowledge_opinion_split/integration@ff986865` 创建
  `feature/block_boundary_materialization/integration`。
- 每个实现 Task 从 Storage v3 integration HEAD 切
  `feature/block_boundary_materialization/<task-short>`。
- task 完成后以普通 merge/fast-forward 集成；不改写既有历史。
- 每个聚焦批次独立提交；author/committer 固定为
  `Ikaqiu Lemon <ikaqiu.lemon@gmail.com>`。
- 不提交工作区绝对路径、凭据、内部环境身份或无关生成物。

## 4. 阶段记录模板

每个阶段在 Task Activity Log 记录：

```text
产品 SHA：
Teamwork SHA：
变更范围：
聚焦验证：
尚待最终聚合回归：
```

“聚焦通过”只表示本批直接受影响范围已通过，不得表述为完整 core/full 或七档全绿。

## 5. 停止条件

实现与正式合同出现真实接口冲突时：

1. 停止扩大实现；
2. 记录最小复现、源码位置与失败输出；
3. 先修订正式合同和受影响任务的 DoR/Acceptance；
4. 单独提交合同修订；
5. 再恢复代码实现。

普通实现取舍在合同范围内自主完成，不逐项等待确认。
