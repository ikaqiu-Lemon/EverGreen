# M3 期真实 Coding Agent 会话留痕（T-evergreen.s1_main_flow-158614-047 / M-003 验收门禁 8）

- **会话留痕 ID**：`3c23b9d9-b367-47a0-b65b-828562b60223`
  （本次会话自建的唯一留痕标识，用于与 M2 会话区分；**≠** M2 的 `56cc0392-ffa1-41ee-9b89-21d7ae5c0199`）
- **会话时间**：2026-09-03T23:12:47Z ~ 23:12:49Z（沙箱 UTC 时钟；vault 内时间戳按本机 +08:00 落盘）
- **执行者**：Agent Harness 环境内的 Coding Agent（本次 T-047 的执行体），角色 = 「读 `skill/SKILL.md` 规程后自主决定命令序列与 ChangePlan 内容」
- **被测二进制**：`eg 0.3.0-m3`（`go build -o /tmp/eg ./cmd/eg`，evergreen@`55d9535`）
- **vault**：全新 `mktemp` 临时库（`/tmp/m3sess/run/vault`），会话结束时 17 个 commit、`git status --porcelain` 为 0

## 证据性质与限制（如实声明，不得被读成更强的结论）

1. 本留痕是**一次真实执行**：每条命令都真实调用了 `eg` 二进制、真实退出码、真实落盘与真实 Git commit；
   `m3-ops-plan.json` 是本会话**自主撰写**的 ChangePlan（含 3 个 M3 新增 op），经 `eg apply` 真实执行并产生 commit。
2. 本留痕**不是** Agent Harness E2E 产品线上的会话：沙箱内无 PPE 会话服务，会话 ID 是**本会话自建标识**而非平台侧会话 ID。
   因此「Agent Harness E2E 环境内会话」这一层的外部事实**未验证**；可验证的是「真实 Agent 通过 CLI 端到端跑通 M3 全主流程」。
3. 本目录**不含**任何离线回放脚本；`session-command-sequence.sh` 是本会话逐条敲下的命令序列的忠实记录器
   （驱动 + 断言 + 留痕导出），它**产生**了本目录里的原件，不是用来「重放已知结果」的替代物。
4. 门禁 8 的执行路径只需 `make build` + `eg apply`，**不需要** `make test`（因此不受 I-…-003 影响）。

## 逐步记录（28 步，退出码全部符合预期，无意外）

| 步 | 命令 | 退出码 | 关键断言 |
|-|-|-|-|
| S01 | `eg init --domain ai-infra` | 0 | 骨架 + `SKILL.md` + Git 仓建立 |
| S02 | `eg config set domains ai-infra` | 0 | `reconcile` commit |
| S03 | `eg capture --url … --body-file … --json` | 0 | `s-20261109-retrieval-augmented-generation-for-knowledge-int` 落盘并登记收件区 |
| S04 | `eg context --source … --json` | 0 | 取得 `base[unprocessed.md] = sha256:52d5c7d8…` |
| S05 | `eg apply --plan plan-ingest.json --json` | 0 | 1 篇材料笔记 + 4 张知识卡 + 1 条未决问题，一次 `process` commit |
| S06 | `eg proposal new --type logical_delete --target k-…-retriever-recall-bound` | 0 | 提案 `p-20260904-001`（**未带** `--user-request`，Agent「可提不可执」成立） |
| S07 | `eg proposal list --json` | 0 | 只读，零 commit |
| S08 | `eg proposal show p-20260904-001 --json` | 0 | 只读，零 commit |
| S09 | `eg proposal approve p-20260904-001 --user-request --json`（**不带** `--confirm`） | **6** | 全库 9 个 `.md` 的 sha256 清单**逐行相等**（`sha256-before-exit6.txt` vs `sha256-after-exit6.txt`，diff 为空）；顶端 commit 不变；`git status --porcelain` 为 0 |
| S10 | `eg proposal approve p-20260904-001 --confirm --user-request --json` | 0 | 提案 frontmatter `status: 'approved'`，`execution.status: not_started` |
| S11 | `eg rel add KA supports KB --reason …` | 0 | 一次 `relate` commit |
| S12 | `eg rel remove KA supports KB --reason …` | 0 | KA 的 `relations[]` 匹配条目**物理移除**（匹配行 1 → 0），**无墓碑字样**，`.md` 文件数 9 → 9（不删文件） |
| S13 | `eg rel add KA supports KB --reason …` | 0 | 重建一条关系，留给 S27 的 `remove_relation` op |
| S14 | `eg edit --target KA --section 知识内容 --content … --user-request` | 0 | 新正文逐字生效；`status` / `deleted_at` / `reviewed_at` **三维逐字不变** |
| S15 | `eg edit --target KA --section 用户补充 --content 任何内容 --user-request` | **2** | 诊断 `E6`，零写入（全库 sha256 清单不变） |
| S16 | `eg deprecate --target KD --reason …` | 0 | `status: deprecated`，删除/过目两维为空 |
| S17 | `eg restore --target KD --reason …` | 0 | `status: active`，删除/过目两维仍为空（只动 `status`） |
| S18 | `eg deprecate --target KC --reason …` | 0 | 为 `replaced-by` 准备失效卡 |
| S19 | `eg replaced-by --target KC --to KA --reason …` | 0 | KC 写入 `replaced_by{target,reason}`；**被指向卡 KA 字节不变**（单向） |
| S20 | `eg delete --target KC --reason … --proposal p-20260904-001 --confirm --user-request` | 0 | `deleted_at` + `deleted_reason` 写入、`status` 仍 `deprecated`（不被 delete 改动）；commits 13 → 14；**留下恰 1 条脏变更 ` M proposals/p-20260904-001.md`**（K-041-01，见下） |
| S21 | `eg search 召回 --json` | 0 | 已删除的 KC **命中 0 次**（默认视图排除） |
| S22 | `eg search 召回 --include-deleted --json` | 0 | KC 被带回（命中 2 次），带 `[已删除]` 标记 |
| S23 | `eg undelete --target KC --reason …` | 0 | 两个删除键被清空；`status` 仍 `deprecated`（只动删除维度）；KA 关系条目数不变（关系不被级联删除） |
| S24 | `eg unreviewed --json` | 0 | KB 在未过目清单内 |
| S25 | `eg mark-reviewed --target KB --json` | 0 | 只写 `reviewed_at` 单键 |
| S26 | `eg unreviewed --json` | 0 | KB 退出未过目清单（before 命中 → after 0） |
| S27 | `eg apply --plan m3-ops-plan.json --json --user-request` | 0 | **门禁 8 本体**：自撰 plan 含 3 个 M3 新增 op（`deprecate` / `remove_relation` / `mark_reviewed`），真实执行 → commits 16 → 17；KD → `deprecated`，KA 关系条目物理移除 |
| S28 | `eg report --last --json` | 0 | 信封顶层键 `['data','exit_code','ok','status','warnings']`，`ok=true` / `exit_code=0` / `status=completed`；`data` 键 `['convergence','domain','report']`，阶段字段 `report.reconcile.ran=false`、`report.support_check=[]`（S3 不提前） |

**汇总**：28 步，**退出码 100% 符合预期**（0 × 25、6 × 1、2 × 1、另 1 步为 0 的只读），`unexpected.txt` 为空，无意外。

## K-041-01 在本会话中的实际表现（如实披露，未解决）

`eg delete` 成功后（S20）：

- `git status --porcelain` 恰 **1 行**：` M proposals/p-20260904-001.md`（见 `k-041-01-porcelain.txt`）；
- 该脏变更的 diff **只落在 `execution` 六键块内**（`k-041-01-diff.txt`）：`status` `not_started → 'succeeded'`、
  `attempted_at` 空 → `'2026-09-04T07:12:47+08:00'`、`git_commit` 空 → `'51c15093…'`、`written_paths` `[]` → 1 条路径；
  另两键 `reason` / `unwritten_paths` 保持原值不变。**六键之外一字未动**，`+5 / −4` 行。
- 成因即 K-041-01 的自指哈希张力：`execution.git_commit` 只能在 commit 之后取得，因此写回它必然产生第二次变更。
- 该条 **仍未决**，两条出路（放宽 §4.3 不存 SHA / 接受 +2 commits）待 owner 裁决；本会话**不做**任何绕过。

## 目录清单

| 文件 | 内容 |
|-|-|
| `commands.log` | 28 步命令 + 退出码 + 关键断言的原始日志 |
| `session-command-sequence.sh` | 本会话逐条命令的忠实记录器（产生本目录全部原件） |
| `plan-ingest.json` | 会话自撰的加工 ChangePlan（材料笔记 + 4 张卡） |
| `m3-ops-plan.json` | **门禁 8 的 plan 原件**：含 `deprecate` / `remove_relation` / `mark_reviewed` 三个 M3 新增 op |
| `apply-m3-ops-output.json` | 该 plan 的 `eg apply` 报告原件（真实 commit） |
| `approve-noconfirm-exit6-output.json` | 退 6 那一步的报告原件 |
| `delete-output.json` | `eg delete` 成功报告原件（含 `support_check[].recommendation`） |
| `report-last-output.json` | `eg report --last --json` 信封原件 |
| `sha256-before-exit6.txt` / `sha256-after-exit6.txt` | 退 6 前后全库 `.md` 的 sha256 清单（逐行相等） |
| `k-041-01-porcelain.txt` / `k-041-01-diff.txt` | K-041-01 的脏变更与 diff 原件 |
| `git-log.txt` | 会话结束时 vault 的 17 条 commit |
