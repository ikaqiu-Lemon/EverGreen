# T-008 接续基线回执

- 日期：2026-09-22
- 状态：已核验
- 上游任务：`T-evergreen.knowledge_opinion_split-158614-008`

## 1. 仓库回执

| 仓库 | 分支 | 接续 SHA | 核验结果 |
| --- | --- | --- | --- |
| EverGreen | `feature/knowledge_opinion_split/integration` | `ff9868652b685eae4502ad0b546e3912724117c0` | 与远端尖端一致，工作树 clean |
| Teamwork | `main` | `72e607f4ef06d3366a09a1f788919eec6eab03e1` | 与 `upstream/main` 一致，工作树 clean |

提交身份已核对为 `Ikaqiu Lemon <ikaqiu.lemon@gmail.com>`。

## 2. 已接收能力

- 版本 `0.7.0-m7`。
- Knowledge/Opinion 分型已贯通 model、store、plan、txn、reconcile、index、query、CLI。
- Knowledge 使用 `k-*` / `knowledge/`；Opinion 使用 `o-*` / `opinions/`，新建恒为
  `validation: pending`。
- `write_note.blocks[]`、七类 annotation、`source_ref`、`omissions[]` 和
  `extraction_coverage[]` 已实现。
- goldmark v1.8.6 已在 `internal/mdfile` 用于结构资产只读解析。
- txn journal v1 已实现完整多文件 accepted write-set、intent 发布屏障、前像、两阶段提交、
  failpoint 和两遍恢复。
- 派生索引为 schema v2、恰 6 表；Knowledge/Opinion 共享表并由 kind 分型。
- CLI 顶层命令恰 23 条，四份文档与 `--help` 的现态门禁已同步。

## 3. T-008 已有验证证据

上游任务 Activity Log 记录：

- `LC_ALL=C` full：115/115 suites，2617 cases，FAIL=0，NOT_RUN=0；
- `LC_ALL=C` core：50/50 suites，1771 cases，FAIL=0，NOT_RUN=0；
- manifest 两次：6/6 suites、33 cases、零漂移；
- `make lint`、`scripts/check-public.sh`、contract-live-drift、docs CLI 门禁通过。

本回执只接收上述历史证据，不把它冒充 Storage v3 修改后的验证结果。Storage v3 每批重新跑
聚焦验证，最终固定 SHA 后再集中运行完整聚合回归。

## 4. 明确未接收为完成的事项

- 旧 T-009/T-010 未开始。
- 真实 Vault 未迁移。
- `mark-reviewed` 未解冻。
- `v0.7.0-m7` 未打 tag、未发布，旧 Epic 未 shipped。
- Storage v3 候选、materialize、plain export、L2、sidecar 和迁移工具均未实现。
- `eg context` 仍有 0.7.x 的 `candidates` 兼容字段与无条件 I1；进入 `0.8.0-m8` 时必须删除。

## 5. Storage v3 开工结论

Storage v3 以 `ff986865` 为唯一产品代码起点，不从 `main` 或更早的
`9a0d1d6` 重新分叉。现有 goldmark 与 journal v1 都是应复用的已验收基座；
任何要求新引 Markdown parser 或 journal v2 的方案都必须先提供现有能力不足的可复现证据。
