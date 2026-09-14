---
title: M4 开工前置裁决（A-30 ~ A-35 六项 + A-38 / A-39 两项，共 8 项）
task: T-evergreen.s1_main_flow-158614-048
milestone: M-004
created: '2026-11-15'
status: closed
adjudicator: Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）
---

# M4 开工前置裁决（S3 · 里程碑 M-004 · 交付 T-…-048）

**唯一职责**：把两份 M4 合同明确判为「本合同不裁决」的 **8 项 P1 阻塞**一次关闭，作为 14 个硬下游
（`T-…-049` ~ `T-…-061`、`T-…-063`）的开工输入。本文件**不写代码**，`evergreen/` 下零改动。

**关联需求**：EG-EDIT-05、EG-VIEW-05（取自技术方案 §14 真实 ID 全集，未自造）。

## 0. 授权边界与代拍留痕规则（先读这一节）

本 task 的原始 DoD 是「**owner 关闭八项**」，且原始 DoR 逐字写着本 task **不得替 owner 拍板**。
2026-09-05 owner（`项目维护者` / `maintainer` / `maintainer@example.com`）给出的是**概括性推进授权**——逐字
「允许开始实施任务，逐个实施任务，完成后立即开始下一个任务，不需要我的确认，直到你完成所有任务」
——**这不是逐项裁决**。两者的张力按下列四条规则处理，规则本身也是本文件的验收面：

1. **结论列必须落在封闭选项内**：八项的「裁决结论」列逐行取值于该行自身的封闭选项集合。
   不填 `未知 / 待 owner 确认`——因为那会依 `AGENTS.md` 的硬依赖语义阻断全部 14 个硬下游，
   与 owner 的推进指令直接冲突。
2. **裁决人列逐字留痕代拍事实**：八行的「裁决人」列**一律**逐字写
   `Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）`。
   **严禁**把 Agent 代拍写成 owner 的逐项裁决——「不得替 owner 拍板」这条口径在本文件里不是被推翻，
   而是被改写成「可以代拍，但必须逐条标明是代拍、并给出可反证的依据与可回滚的范围」。
3. **每项都必须有代码实证**：结论不取自「合同暂定」或「看起来合理」，而取自 §1 的只读实证。
   实证与合同 / task / M-004 现文的**建议或前提**冲突时**以实证为准**，并在 §8 逐条登记差异。
4. **每项都必须给出回滚范围**：owner 事后改判时，受影响的文件 / 判据 / task 在 §5 逐项列明，
   保证改判成本可估、且不需要重做实证。

**本文件不做的事**：不裁决 A-36 / A-37 / A-40（登记型）；不改飞书权威文档；不改 M2 查询合同原文；
不改 `EPIC.md` 与 `M-001` / `M-002` / `M-003`；不改 M1 / M2 / M3 的门禁脚本本体。

## 1. 只读代码实证复核（2026-11-15 在 `evergreen@0277b58` 实测，非推断）

八项裁决的事实底座。每行都是可复跑的只读命令结果，**本 task 未改动其中任何一个文件**。

| # | 实证点 | 实测事实 | 用于 |
|-|-|-|-|
| E-1 | `internal/model/enums.go:215` | `VerbReconcile Verb = "reconcile"` **已存在**，且在 `KnownVerbs()`（`:238`-`:241`）内 → verb 字面量 `reconcile` **已属已知 verb**，集合大小恒 **8** | A-30 |
| E-2 | `internal/model/model_test.go:348` / `:378` | 硬断言 `len(KnownVerbs()) != 8` 与 `len(seen) != 8` 各 1 处 | A-30 |
| E-3 | `tools/m3_final_gate.py:370` / `:2523` / `:2746` | `N_KNOWN_VERBS = 8`，并以文本形态断言 `model_test.go` 里存在 `len(KnownVerbs()) != 8` | A-30 |
| E-4 | `internal/cli/config.go:187` | `eg config set` 的 commit 已取 `model.VerbReconcile`；主题形态 `reconcile(<domain>): 更新 evergreen.yml 的 <key>`（`internal/git/message.go:45` 的 `verb(domain): subject`） | A-30 |
| E-5 | `internal/cli/exit.go:24`-`:32` | `ExitOK=0` / `ExitUsage=1` / `ExitValidation=2`（逐字「校验失败（仅 error 级）：零写入」）/ `ExitPartialWrite=3` / `ExitCommitFailed=4`；翻译唯一点 `ExitCodeFor`（`:136`），进程级 `os.Exit` 唯一落点 `cmd/eg/main.go:19` | A-31 |
| E-6 | `internal/cli/exitcode.go:22` / `:98` / `:110` | `ExitNeedConfirm = 6` 只经 `NeedConfirmError` 产出，且必须在 `NeedConfirmCommands()` 白名单内（恰 `proposal approve` / `delete`），白名单外一律退化为 `1` | A-31 |
| E-7 | `internal/proposal/schema.go:657`-`:664` | `executionBlock()` 把 `KeyGitCommit` 作为**空键**写进**每一个**新建提案模板 | A-32 |
| E-8 | `internal/proposal/execution.go:328`-`:330` | `succeeded` 时 `git_commit` 为空即 `ViolationExecFields`（必填校验落点） | A-32 |
| E-9 | `internal/store` + `internal/plan/executor.go` | `grep -rn 'ContentHash\|content_hash' internal/store internal/plan --include=*.go \| grep -v _test.go \| wc -l` → **41**（与姊妹合同 §2.4 基线逐字相等） | A-32 |
| E-10 | `internal/report/report.go` | `grep -c 'Commit \*string `json:"commit"`'` → **1**（报告体 `git.commit`）；`grep -c 'GitCommit      \*string  `json:"git_commit"`'` → **1**（`proposals[].execution` 投影键） | A-32 |
| E-11 | `internal/plan/ops_m3.go:52`-`:55` | `AllOpNames()` = `OpNames()`(7) + `M3OpNames()`(8) + `EditOpNames()`(1) = **16**；断言在 `internal/plan/m3_test.go:244`、`test/e2e/m3_acceptance_test.go:255`、`tools/m3_final_gate.py:532`（`N_ALL_OP_NAMES = 16`） | A-33 |
| E-12 | `internal/plan/validate_m3.go:457`-`:468` | plan 侧 `mark_reviewed` 是**校验-only**：查矩阵 `FieldReviewedAt` 后走 `pendingWrite`，`executor.go` 的派发 `switch` **无** `mark_reviewed` 分支；落盘在 `internal/cli/mark_reviewed.go` 直连 `store.ApplyStateWrite` | A-33 |
| E-13 | `internal/plan/ops_m3.go:91`-`:92` + `:75` | `replace_block` 的字段表是 `target/section/block/base_block_hash`，唯一允许分区常量 `SelfCheckSection = "理解自检"` → 它是**正文分区块替换**，无法承载 frontmatter 键写入 | A-33 |
| E-14 | `internal/store/state_write.go:253`-`:261` | `ApplyStateWrite` 现恰 **4** 形态（`status` / `replaced_by` / `deleted` / `reviewed_at`）；`tools/m3_final_gate.py:447` `N_STATE_WRITE_KINDS = 4` 且 `:4688` 有同串自检锚点 | A-33 / A-34 |
| E-15 | `internal/plan/authorize.go:51`-`:57` | `PathOf`：`initiator: user` **且**命令行 `--user-request` → `P-U`；其余一切（含伪造形态）→ `P-A` | A-34 |
| E-16 | `internal/cli/mark_reviewed.go:16`-`:24` / `:97` | **M3 先例**：用户在终端敲 `eg mark-reviewed` 本身即「进程边界上的命令行佐证」，命令内置 `inv.UserRequest = true`，**不要求再手敲 `--user-request`**，也不进退出码 `6` 白名单 | A-34 |
| E-17 | `internal/plan/matrix.go:131` / `:239` | 写权限矩阵**已有** `FieldReviewStale = "stale / stale_reason"`，行号 **#33**：`{33, ObjectReview, FieldReviewStale, deny, deny, "由对账计算并自动写入，对账属 S3/M4，M3 无写入路径"}` —— 现为**两路径同 🔴**，备注逐字说明这是**阶段闸** | A-34 |
| E-18 | `internal/plan/matrix.go:203` / `:222` | `reviewed_at` 的两行 **#7**（知识卡）/ **#22**（材料笔记）是 `P-A 🔴 / P-U ✅`（备注「仅 eg mark-reviewed」） | A-34 |
| E-19 | `test/e2e/m3_acceptance_test.go:303`-`:329` | `TestWritePermissionMatrixCounts` 实测：**43 行 / 86 格 / 2 路径 / 严格解锁 16 / 条件解锁 1（#12）/ 两路径同 🔴 5 / 对象类 7** | A-34 |
| E-20 | 合同 §11 + §4 | 报告 `reconcile.commit` 是 **string 或 null 的单值**字段；M-004 判据 9 已把「commit 次数恰 0 或 1」钉成机器断言 | A-35 |
| E-21 | `internal/query/relation.go` / `internal/query/card.go:215` | `RelDataKeys()` 五键被 `tools/validate_m4_tasks.py:140`-`:141`（`REL_DATA_FIVE_KEYS`）与 M-004 判据 12 逐字锚定；`VisibleEndpoints` 现只过滤**已删除**端点 | A-38 |
| E-22 | `internal/query/diagnostic.go:20`-`:25` vs `internal/plan/diagnostics.go:41`-`:45` | 查询域现只有 `CodeQ1` / `CodeQ2` / `CodeQ3`；`W10` / `W11` / `W12` 属 ChangePlan 的 E/W/I 域 → 两域分离是代码事实 | A-38 / A-39 |
| E-23 | `grep -rn "恰三条" projects/evergreen/s1_main_flow/tools/ \| wc -l` | 实测 **3**（**不是**合同 §3.5.1 写的 `0`）：全部在 `tools/gen_m4_tasks.py:274` / `:286` / `:1556`，均为 M4 task 正文**模板文本**（含 A-39 自身的选项描述），**无任何门禁把「恰三条」当断言本体** | A-39 |

## 2. reconcile 包与 plan 包的边界

**三段定义（谁检查 / 谁写盘 / 谁 commit），本节是 A-30 / A-33 / A-34 / A-35 的共同前提**：

1. **谁检查 = `internal/reconcile`（只读检查器，零写口）**。输入 = vault 快照 + `internal/git` 的**只读**
   状态面（`Porcelain` / `HEAD` / `Log` / `Diff`）；输出 = `[]Finding` + `[]RepairSpec`——`RepairSpec`
   是**修复意向的描述，不是写动作**。包内**零写盘、零 commit、零 `os/exec`**；依赖方向单向
   （只允许 `model` / `mdfile` / `git` 只读 / `query`），`query` / `store` / `plan` / `proposal` 对它的
   反向依赖恒 0。反证 = M-004 判据 2 的四组 grep。
2. **谁写盘 = `internal/plan` → `internal/store`（唯一写盘链）**。R2 / R6 的修复由
   `internal/cli/reconcile_repair_reviewed.go` / `internal/cli/reconcile_repair_stale.go` 把
   `RepairSpec` 编排成**内存 ChangePlan**，经 `runPlan` 交 `internal/plan` 走完整校验链
   （含 §2 写权限矩阵闸）后派发，最终由 `internal/store` 落盘；B3 `content_hash` 前置比对
   **不豁免**（hash 过期 → `skipped[kind=file_changed]`，本文件本次不写，finding 仍产出）。
   两个 CLI 编排文件**不得** `import internal/store`（M-004 判据 5 的 `grep` 恒 0）。
3. **谁 commit = `internal/cli/reconcile_commit.go`（唯一 commit 口）**。直连 `internal/git`，
   `git add -A` + **恰一次** commit，verb = `reconcile`（A-30），零改动即零 commit。
   `internal/reconcile` 不 commit（判据 2 / 判据 4 的 `grep -rn 'Commit(' internal/reconcile/` 恒 0），
   `internal/plan` / `internal/store` 也不 commit（M1 起的既有分层，M4 一格不动）。

**边界的反面（本裁决同时钉死）**：对账**不作为任何写命令的前置**（写前对账属 S5 / M6）；
`eg check` 恒 0 commit、零写入；`internal/reconcile` 不得调用 `internal/git` 的 `Commit` / `Add`。

## 3. 六项阻塞裁决（A-30 ~ A-35）

列 = `编号 / 冲突摘要 / 封闭选项 / 裁决结论 / 裁决人 / 裁决日期 / 受影响 task / 机器反证 / Agent 建议裁决`。
第 4 列取值**必须**来自该行自身的封闭选项集合；第 9 列是 Agent 依 §1 实证给出的推荐值，与第 4 列
**严格分离、不得互相顶替**（本轮两列同值，因为代拍就是照推荐值落的，且推荐值有 §5 的依据链）。

| 编号 | 冲突摘要 | 封闭选项 | 裁决结论 | 裁决人 | 裁决日期 | 受影响 task | 机器反证 | Agent 建议裁决 |
|-|-|-|-|-|-|-|-|-|
| **A-30** | `eg reconcile` 纳管 commit 的 `verb` 取值：合同 §16 与 M-004 R-17 均假定「新增第 9 值须改 M2 / M3 已验收用例」 | ① `新增 verb reconcile`；② `复用 process` | `新增 verb reconcile` | Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决） | 2026-11-15 | 048 / 050 / 058 / 062 | `[ "$(grep -c 'len(KnownVerbs()) != 8' internal/model/model_test.go)" -eq 1 ]` 且 `go test ./internal/model -run TestKnownVerbs -count=1` 退 `0`（**8 恒 8**，见 E-1 ~ E-3）；`[ "$(grep -c 'string(model.VerbReconcile)' internal/cli/reconcile_commit.go)" -ge 1 ]` | `新增 verb reconcile` |
| **A-31** | `eg check` 发现 error 级 finding 时退 `2`，是否构成「强校验（S5）提前到 S3」 | ① `退 2`；② `恒退 0` | `退 2` | Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决） | 2026-11-15 | 048 / 059 | `TestExitCodeSetClosed` 全绿（退出码全集仍恰 `{0,1,2,3,4,6}`，零新增常量）；`[ "$(grep -cE 'runPlan\|internal/plan\|Commit\(' internal/cli/check.go)" -eq 0 ]`；`eg check` 后 `git -C "$VAULT" status --porcelain \| wc -l` → `0` 且 commit 数不变 | `退 2` |
| **A-32** | 技术方案 §4.3 SHA 退役的落地形态未定（`execution.git_commit`） | ① `整键删除`；② `保留键恒 null`；③ `改存非 SHA 回执` | `整键删除` | Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决） | 2026-11-15 | 048 / 060 | `[ "$(grep -c "git_commit" "$VAULT/proposals/$PID.md")" -eq 0 ]`（新建提案模板不再产出该键，见 E-7）；`go test ./internal/proposal -run TestSucceededWithoutGitCommitIsValid` 全绿；`[ "$(grep -rn 'ContentHash\|content_hash' internal/store internal/plan --include=*.go \| grep -v _test.go \| wc -l)" -ge 41 ]` | `整键删除` |
| **A-33** | R2 / R6 的写入走哪个 op（直接决定 `plan.AllOpNames` 是 16 还是 17） | ① `mark_reviewed + set_stale`；② `单个 reconcile_fix`；③ `mark_reviewed + replace_block 复用` | `mark_reviewed + set_stale` | Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决） | 2026-11-15 | 048 / 049 / 051 / 055 | `[ "$(python3 -c 'pass'; echo)" = "" ]` 不适用；改用 Go 侧：`len(plan.AllOpNames()) == 17` 且三分量不变（`len(plan.OpNames()) == 7`、`len(plan.M3OpNames()) == 8`、`len(plan.StateOpNames()) == 5`）；`[ "$(grep -c 'set_stale' internal/plan/ops_m4.go)" -ge 1 ]`；R2 侧 `[ "$(grep -c 'internal/store' internal/cli/reconcile_repair_reviewed.go)" -eq 0 ]` | `mark_reviewed + set_stale` |
| **A-34** | R6 写 `stale` / `stale_reason` 是否属「用户显式改核心知识数据」，从而是否需要命令行 `--user-request` | ① `需要`；② `不需要` | `不需要` | Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决） | 2026-11-15 | 048 / 055 | `[ "$(grep -cE '\-\-user-request' internal/cli/reconcile.go)" -eq 0 ]`；矩阵**不新增行**：`len(plan.Matrix()) == 43` 且 `plan.MatrixCells() == 86` 且 `len(plan.MatrixObjects()) == 7`；`plan.RowNum(33)` 的 `Field == plan.FieldReviewStale` 且 `P-U` 仍 🔴；按实测重钉 `len(plan.BothDeniedRows()) == 4`（原 5），`len(plan.StrictUnlockRows()) == 16` **不变**、`len(plan.ConditionalUnlockRows()) == 1` **不变** | `不需要` |
| **A-35** | R1 纳管 commit 与 R2 / R6 修复写入是否合并为恰一次 commit | ① `恰一次`；② `允许两次` | `恰一次` | Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决） | 2026-11-15 | 048 / 050 / 051 / 055 / 057 / 058 | `N0=$(git -C "$VAULT" log --oneline \| wc -l); eg reconcile; N1=$(...); [ $((N1-N0)) -le 1 ] && [ $((N1-N0)) -ge 0 ]`，干净 vault 上重跑 `[ $((N1-N0)) -eq 0 ]`；`internal/report/reconcile.go` 的 `json:"commit"` 恰 1 处且**非数组**（`[ "$(grep -c '\[\]string `json:"commit"`' internal/report/reconcile.go)" -eq 0 ]`） | `恰一次` |

## 4. 两项阻塞裁决（A-38 / A-39）

与 §3 同列、同规则；单列一节是因为这两项来自**姊妹合同** `2026-11-12-m4-visibility-and-execution-contract.md`
§5，落点在查询域（`eg rel` / `eg card show`）而非对账域。

| 编号 | 冲突摘要 | 封闭选项 | 裁决结论 | 裁决人 | 裁决日期 | 受影响 task | 机器反证 | Agent 建议裁决 |
|-|-|-|-|-|-|-|-|-|
| **A-38** | 被默认隐藏的 deprecated 端点信息的承载方式（初稿的 `W21` 已由合同现文 §3.5.1 纠正为 `Q4`） | ① `warnings 承载 Q4`；② `data 扩为六键` | `warnings 承载 Q4` | Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决） | 2026-11-15 | 048 / 061 | `[ "$(grep -c 'return \[\]string{"id", "relations_out", "relations_in", "scanned_files", "skipped_files"}' internal/query/relation.go)" -eq 1 ]`（五键不扩张）；`go test ./internal/query ./internal/cli -run 'TestG8DataKeysNotExpanded\|TestG9Q3NotTriggeredByQ4'` 全绿；`[ "$(grep -rn '"W21"' internal/ \| wc -l)" -eq 0 ]`（`W21` 不分配） | `warnings 承载 Q4` |
| **A-39** | M2 查询合同 §5.1 标题逐字「Q 码表（恰三条）」是封闭集合表述，新增 `Q4` 等于要动这句计数 | ① `改为恰四条`；② `复用 Q3`；③ `只走人类可读输出` | `改为恰四条` | Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决） | 2026-11-15 | 048 / 061 / 062 | `[ "$(grep -oE 'CodeQ[1-4]' internal/query/diagnostic.go \| sort -u \| wc -l)" -eq 4 ]`；`Q4` 的 `path` 逐字 `(汇总)`、`op_index` `-1`、`N == 0` 时不产出；`Q3` 仍恒末位（`TestSearchNoQ3WithoutQ1OrQ2` + `TestG9Q3NotTriggeredByQ4` 双侧锁）；M4 新增诊断码合计仍恰 **14** | `改为恰四条` |

**A-39 的边界（不许扩读）**：本裁决只关闭「**允许把 §5.1 的封闭计数从三条追加为四条**」这一口径；
**本 task 不改 M2 查询合同原文一个字**。文本落地（§5.1 标题改为「恰四条（S3 起）」并补阶段脚注、
只追加成员不改既有三码任何一条语义）归 `T-…-061`（实现 `Q4`）与 `T-…-062`（文档同步）执行。

## 5. 每项代拍的依据与「owner 改判后的回滚范围」

每项四段：**实证依据** → **合同 / 改动面代价** → **对历史门禁的冲击** → **改判回滚范围**。

| 编号 | 实证依据（为什么这样代拍） | 改动面与对历史门禁的冲击 | owner 改判后的回滚范围 |
|-|-|-|-|
| A-30 | **合同与 M-004 R-17 的前提不成立**：verb 字面量 `reconcile` **已在** `KnownVerbs()` 内（E-1），所以「新增 verb `reconcile`」的真实落地形态是「commit verb 取既有 `model.VerbReconcile`」，**不新增第 9 个枚举成员**、`KnownVerbs` 恒 8。语义收益成立：`git log` 里对账（`reconcile(...)`）与 `eg apply`（`process(...)`）**可区分**（E-4 的主题形态） | **零计数变更、零豁免申请**：E-2 的两条 `!= 8` 断言与 E-3 的 `N_KNOWN_VERBS = 8` 全部保持绿，M-004 R-17 所说的「显式豁免申请」**不需要发起**。改动面 = `enums.go:206`-`:210` 的注释扩写（该 verb 自 M4 起同时承载 `eg reconcile` 纳管 commit）+ `reconcile_commit.go` 一处取值。残留成本：`eg config set` 与 `eg reconcile` 共用该 verb（E-4），**必须靠 commit 主题逐字可分**，由 T-050 落用例 | 改判为 `复用 process`：改 T-050 的 `reconcile_commit.go` 一处 verb 取值；T-058 / T-062 需在 `SKILL.md` / README 写明「`git log` 里对账与 `eg apply` 不可区分」。**判据与门禁零回滚**（8 恒 8） |
| A-31 | `2` 在 S1 合同里的语义逐字就是「校验失败（仅 error 级）：零写入」（E-5），与 `eg check`「只报告、零写入、恒 0 commit」完全同型；`6` 结构上不可能被 `eg check` 产出（E-6 的白名单恰两条命令），因此**不存在 2 与 6 的语义冲突** | 不构成「强校验（S5）提前」：`eg check` **不被任何写命令调用**（M-004 判据 17 的 grep 反证恒 0），退 `2` 只影响调用方（CI / Agent）自己的判断，**不阻塞任何写命令**。退出码全集不扩张 → `TestExitCodeSetClosed` 与 `EXIT6_FILES` 集合断言全绿。退出码 `5` 全程不启用（属 S5） | 改判为 `恒退 0`：改 T-059 的 `check.go` 退出码分支 + 合同 §13 退出码行 + M-004 判据 10 的用例名 `TestCheckExitTwoOnErrorFindingZeroWrite`（须同步改名与期望）。影响面限 059 一个 task |
| A-32 | **判据 11 是决定性的**：`executionBlock()` 把 `git_commit` 当空键写进**每个**新建提案模板（E-7），因此选项②`保留键恒 null` 与选项③`改存非 SHA 回执` 下 `grep -c "git_commit" proposals/<pid>.md` 恒 ≥ 1，**M-004 判据 11 必 FAIL**。只有 `整键删除` 能同时满足判据 11 与姊妹合同 §2.1 的三条硬要求 | 落点恰四处：`schema.go:134`（键常量）/ `:163`（`ExecKeys()`）/ `:660`（模板）、`execution.go:328`-`:330`（`succeeded` 必填校验解除）、`internal/cli/delete.go` 的 `GitCommit: info.SHA` 回写删除。**只动 `execution.git_commit`**：不碰 `internal/store/**` 与 `internal/plan/executor.go` 的 `content_hash`（E-9 基线 41，判据 13 要求 ≥ 41 不减）、不碰报告体 `git.commit`、不碰报告投影键 `proposals[].execution.git_commit`（E-10 两处各恰 1，姊妹合同 §1.4 三选项下键集合一字不改）。读侧宽容历史键，**不做数据迁移** | 改判为②或③：改 T-060 的 `schema.go` 模板与 `execution.go` 校验；**M-004 判据 11 的 `grep -c "git_commit" == 0` 必须同步改写**（这是唯一会被推翻的机器判据），判据 11 的「commit 数恰 +1」不变 |
| A-33 | 选项③被实证排除：`replace_block` 是**正文分区块替换**、唯一允许分区是 `理解自检`（E-13），无法承载 frontmatter 键。选项②被合同排除：`reconcile_fix` 复合 op 让一个 op 同时写 `reviewed_at` 与 `stale`，违反合同 §5「被改 frontmatter 键逐键封闭」。故选①：R2 复用 `mark_reviewed`（op 名不新增）、R6 新增 `set_stale` | `AllOpNames` **16 → 17**，须按实测重钉三处：`internal/plan/m3_test.go:244`、`test/e2e/m3_acceptance_test.go:255`-`:256`、`tools/m3_final_gate.py:532`（`N_ALL_OP_NAMES`）；三个分量 `OpNames` 7 / `M3OpNames` 8 / `StateOpNames` 5 **一个不动**（`set_stale` 应入新增的 `M4OpNames()`，与 `edit_section` 只进派发全集同型）。**下游必须知道的补强事实**：plan 侧 `mark_reviewed` 现为校验-only（E-12），T-051 必须为它新增一个 executor 派发动作（op 名不新增，`AllOpNames` 不因此再 +1）；`mark_reviewed_w7_test.go:66`-`:74` 只有注释、**无「零 action」硬断言**，故不构成回归。R6 的 store 侧建议走 `ApplyStateWrite` 第 5 形态（写口唯一原则优先），须把 `m3_final_gate.py:447` 的 `N_STATE_WRITE_KINDS` 4 → 5 并同步 `:4688` 自检锚点（T-055 / 063）；若另开独立入口 `ApplyRecapStale` 则该门禁不动 | 改判为②或③：影响 T-049（op 注册面）/ T-051 / T-055；`AllOpNames` 的重钉方向随之改变（②仍 17、③仍 16）；M-004 判据 7 的用例名不变 |
| A-34 | 两条实证同向：① **M3 先例**（E-16）——`eg mark-reviewed` 已确立「用户敲命令本身即进程边界上的命令行佐证，不要求再手敲 `--user-request`」，`eg reconcile` 与之同型且写的是**系统派生的只读失准标记**（不改内容、不改 `status`、不销毁任何东西）；② **矩阵已为它留了行**（E-17）——#33 `stale / stale_reason` 在册，备注逐字「由对账计算并**自动写入**，对账属 S3/M4，M3 无写入路径」，即 🔴🔴 是**阶段闸**而非永久禁止，且「自动写入」指向 `P-A` | **矩阵不新增行**：43 行 / 86 格 / 7 对象类**一格不动**。落地口径分两路（下游照此实现）：**R2 走 `P-U`**——合成 ChangePlan 带 `initiator: user` + 进程边界置 `UserRequest`，命中 #7 / #22 的 `P-U ✅`（E-18），**零矩阵改动**；**R6 走 `P-A`**——`set_stale` 不带 `initiator: user`，把 #33 的 **`P-A` 由 🔴 阶段解锁为 ✅**，**`P-U` 保持 🔴**（用户不手改派生标记；M4 也不自动清除 `stale`）。计数冲击**恰一处**：`len(plan.BothDeniedRows())` **5 → 4**（`test/e2e/m3_acceptance_test.go:323`-`:325` 按实测重钉）；`StrictUnlockRows` **16 不变**、`ConditionalUnlockRows` **1 不变（仍 #12）**、行数 / 格数 / 对象类不变。**这是阶段解锁而非放宽**：判据本体（「每一格都必须逐格在册且可复算」）一字不动，且 `P-A` 的解锁面被 `set_stale` 一个 op 与综述专属两键封闭 | 改判为 `需要`：T-055 / T-058 须给 `eg reconcile` 新增 `--user-request` 语义（合同 §12 现文只有 `--json` / `--dry-run`，属参数面扩张），R6 改走 `P-U` → #33 解锁方向从 `P-A` 换成 `P-U`，届时 `StrictUnlockRows` **16 → 17** 且 `BothDeniedRows` 5 → 4（**两处**计数重钉，比本裁决多一处） |
| A-35 | `reconcile.commit` 是**单值**字段（E-20），两次 commit 表达不了；M-004 判据 9 已把「恰 0 或 1 次」钉成机器断言，选项②`允许两次` 会**直接 FAIL 判据 9**。且 `git add -A` + 一次 commit 是 M1 起的既有口径（R-13 / R-20 继承），M4 明确不改 | 零字段扩张：报告 `reconcile` 保持**恰三键**（`ran` / `commit` / `findings`），`commit` 保持 `string \| null`。次序被钉死：**先跑完全部检查与 R2 / R6 修复写入，再执行恰一次 `git add -A` + commit**；有 error 级 finding 时**先完成 R1 纳管 commit 再退 `2``；零改动即零 commit（`commit: null`、`ran: true`） | 改判为 `允许两次`：须把 §11.2 的 `commit` 改成数组（字段扩张），影响 T-050 / 051 / 055 / 057 / 058，并**同时推翻** M-004 判据 8（恰三键的值形态）与判据 9（次数上限）两条机器判据 |
| A-38 | 合同现文已把初稿 `W21` 纠正为 `Q4`（§3.5.1），核对结论 = **以合同现文 `Q4` 为准**，`W21` 不分配。选项②`data 扩为六键` 被两侧锚点排除：`REL_DATA_FIVE_KEYS` 已被 `validate_m4_tasks.py:140`-`:141` 与 M-004 判据 12 逐字锚定（E-21），扩张即撞 M2 冻结面；且 `Q` 域与 E/W/I 域的分离是代码事实（E-22） | 输出面零扩张：`eg rel --json` / `eg card show --json` 的 `data` 键集合一字不动，被隐藏信息只进 envelope 的 `warnings[]`。`Q4` 口径按姊妹合同 §3.5.2 全量落地（`level=warning`、不影响退出码、`path` 逐字 `(汇总)`、`op_index=-1`、`N==0` 不产出、`--include-deprecated` 时恒不产出、每次查询至多一条、插在 `Q2` 之后 `Q3` 之前） | 改判为 `data 扩为六键`：影响 T-061 的输出合同；**须同时改** M-004 判据 12 的五键 grep、`validate_m4_tasks.py` 的 `REL_DATA_FIVE_KEYS` 锚点与「`六键` / `hidden_deprecated` 只允许出现在封闭选项 / 豁免 / 不扩张语境」这条判据 → **须另行豁免申请**，代价明显高于本裁决 |
| A-39 | ① **一致性强制**：A-38 取 `warnings 承载 Q4` 蕴含 `Q4` 必须真实存在，选项③`只走人类可读输出` 与之直接矛盾；选项②`复用 Q3` 被姊妹合同 §3.5.2 逐字禁止（破坏 M2「`Q3` 只在有 `Q1`/`Q2` 时出现」的反向可判性）。② **M-004 现文已按 `Q4` 存在编排**：判据 12 的 `TestG9Q3NotTriggeredByQ4`、判据 17 的诊断码全集含 `Q1..Q4`、合同 §14「新增恰 14」——若取③则新增码只有 13，判据与合同当场不自洽。③ **门禁实证**：无任何门禁把「恰三条」当断言本体（E-23） | 只追加成员、不改既有三码任何一条语义；`Q3` 仍恒末位（双侧锁 `TestSearchNoQ3WithoutQ1OrQ2` + `TestG9Q3NotTriggeredByQ4`）。**对合同现文的一处基线修正**：合同 §3.5.1 写「实测 `grep -rn "恰三条" .../tools/` → `0`」，2026-11-15 实测 **= 3**（全部在 `gen_m4_tasks.py` 的 task 正文模板里，见 E-23）；**结论不变**（改 M2 §5.1 计数不破坏任何历史判据），但引用该数字时须用 **3** 而非 0 | 改判为 `只走人类可读输出`：T-061 不实现 `Q4`；**须同时改** M-004 判据 12（去掉 `TestG9Q3NotTriggeredByQ4`）、判据 17 的诊断码全集（`Q1..Q3`）、两份合同 §3 / §14 的「新增恰 14」→ 13，并回退 A-38 的 `Q4` 承载 → **A-38 / A-39 必须同批改判**，不得只改一项 |

**代拍的共同兜底**：以上八项若 owner 事后改判，本文件**只追加修订段、不重写既有结论**（append-only），
并由 `T-…-063` 在 M4 验收报告里逐项复述最终状态。**不得**把改判伪装成「本来就是这么裁的」。

## 6. A-32 的不误伤反证：content_hash 并发保护不受本裁决影响

**逐字声明：content_hash 并发保护不受本裁决影响。** A-32 只退役**提案 frontmatter 的执行回执**
`execution.git_commit` 这**一个**字段，B3 的写前内容比对（`ContentHash` / `content_hash`）**一格不碰**。
两者毫无关系：前者是「这次执行落在哪个 commit」的回执，后者是「写盘前文件是否已被别人改过」的
并发保护。姊妹合同 §2.4 的四条反证在本裁决下**逐条成立**（下列命令在 `evergreen` 仓根执行）：

```
# ① B3 并发保护只增不减（2026-11-15 实测基线 41，非测试面）
grep -rn "ContentHash|content_hash" internal/store internal/plan --include=*.go \
  | grep -v _test.go | wc -l                                                     # → ≥ 41
# ② B3 / 写前哈希比对用例一条不删改（6 个用例名逐字实测存在，且必须真跑起来）
go test ./internal/store ./internal/plan -count=1 -v -run \
'TestB3_UserExplicitPathStillHashChecked|TestGuardB3FileChangedSkipsAndKeepsBytes|TestEdit_HashCheckedBeforeWrite|TestGuardExpectedHashFromContext|TestSetDeletedHashMismatchSkipsWithoutWrite|TestSetStatusHashMismatchSkipsWithoutWrite' \
  | tee /tmp/b3.log | grep -c '^=== RUN'                                          # → ≥ 6
grep -c '^--- FAIL' /tmp/b3.log                                                   # → 0
# ③ 报告体 git.commit 仍在（S1 必填 11 项不变）+ ③b 报告投影键仍在（姊妹合同 §1.4）
grep -c 'Commit \*string `json:"commit"`' internal/report/report.go               # → 恰 1
grep -c 'GitCommit      \*string  `json:"git_commit"`' internal/report/report.go  # → 恰 1
# ④ execution 的逐路径义务不变
go test ./internal/proposal -count=1 -v \
  -run TestExecutionFailed_ListsWrittenAndUnwrittenPaths | grep -c '^=== RUN'     # → ≥ 1
```

三条边界因此写死：

1. **不动 `internal/store/**` 与 `internal/plan/executor.go`**：`ContentHash` 的 41 处
   （E-9 实测）一处不减；R2 / R6 的修复写入**同样**走 `ExpectedHash` 前置比对，hash 过期即
   `skipped[kind=file_changed]`、本文件本次不写、finding 仍产出（B3 **不豁免**）。
2. **不动报告体 `git.commit`**（§4.6 的 S1 必填 11 项之一）与报告投影键
   `proposals[].execution.git_commit`（E-10 两处各恰 1）——键集合与键序一字不改，值恒 `null`。
3. **不动 `execution` 的逐路径义务**：`failed` 时 `written_paths[]` / `unwritten_paths[]` 两键必在、
   并集 == 影响文件全集，反证用例 `TestExecutionFailed_ListsWrittenAndUnwrittenPaths` 必须真跑起来。

**任一条 FAIL 即判「裁决落地误伤既有安全机制」，A-32 的实现整体回退**（T-060 的验收口径）。

## 7. 本裁决引起的计数变更面与门禁重钉清单（供 T-…-063 逐项复算）

「重钉」= **按实测改期望值、判据本体一字不动**；严禁删判据、严禁加白名单绕过本体。

| 计数项 | M3 收口基线 | 本裁决后 M4 目标 | 重钉落点 | 归属 |
|-|-|-|-|-|
| `model.KnownVerbs` | 8 | **8（不变）** | 无（E-2 / E-3 全部保持绿） | — |
| `plan.AllOpNames` | 16 | **17** | `internal/plan/m3_test.go:244`；`test/e2e/m3_acceptance_test.go:255`-`:256`；`tools/m3_final_gate.py:532` | 051 / 055 / 063 |
| `plan.OpNames` / `M3OpNames` / `StateOpNames` | 7 / 8 / 5 | **7 / 8 / 5（均不变）** | 无 | — |
| `store.ApplyStateWrite` 形态数 | 4 | **5**（`stale` 走第 5 形态） | `tools/m3_final_gate.py:447` + `:4688` 自检锚点 | 055 / 063 |
| 写权限矩阵：行 / 格 / 对象类 | 43 / 86 / 7 | **43 / 86 / 7（均不变）** | 无（**不新增行**） | — |
| 矩阵：严格解锁 / 条件解锁 | 16 / 1（#12） | **16 / 1（均不变）** | 无 | — |
| 矩阵：两路径同 🔴 | 5 | **4**（#33 的 `P-A` 阶段解锁） | `test/e2e/m3_acceptance_test.go:323`-`:325` | 055 / 063 |
| `content_hash` grep（非测试面） | 41 | **≥ 41 不减** | 无（只加严） | 060 / 063 |
| 提案 `execution` 写侧键集合 | 含 `git_commit` | **去掉 `git_commit`**（模板 / `ExecKeys` / `succeeded` 必填） | `internal/proposal/schema.go`、`execution.go`、`internal/cli/delete.go` | 060 |
| 报告 `git.commit` / `proposals[].execution.git_commit` | 各恰 1 | **各恰 1（不变）** | 无 | 060 |
| 诊断码新增数 | — | **恰 14**（`E11`-`E14` / `W13`-`W20` / `Q4` / `I2`）；`W21` **不分配** | 合同 §3 / §14 已同步 | 049 / 061 |
| `Q` 域码数 | 3 | **4**（`Q1..Q4`） | `internal/query/diagnostic.go`；M2 §5.1 计数（**文本落地归 061 / 062**） | 061 / 062 |
| `eg rel --json` 的 `data` 键 | 五键 | **五键（不扩张）** | 无 | 061 |
| 命令数 / 退出码全集 / `skipped[].kind` | 18 / `{0,1,2,3,4,6}` / 2 值 | **20 / 全集不变 / 仍 2 值** | `internal/cli/cli_test.go` 的 `wantCommandCount` | 058 / 059 |

## 8. 与 task 现文 / M-004 现文的对应关系与差异登记

**范围对应关系（八项，两侧一致）**：`M-004-m4.md:34` 逐字「**A-30 ~ A-35 六项 + A-38 / A-39 两项共 8 项
前置裁决**」，`:360`-`:361` 逐字「**A-30 ~ A-35 / A-38 / A-39 八项为阻塞型**（T-…-048 必须关闭），
A-36 / A-37 / A-40 为登记型（不阻塞开工）」；本 task 的 `title` / `deliverables.requires` / Acceptance
同为 8 项。**两侧无冲突**；task Scope 段里「这六项不关闭…」是行文省略（只点了对账合同那六项），
不构成范围差异。**本文按 M-004 现文为准落 8 项**，A-36 / A-37 / A-40 不在本裁决范围。

**逐条差异登记（实证 vs 现文；以实证为准）**：

1. **A-30 的前提被实证推翻**：合同 §16 A-30 行与 M-004 R-14 / R-17 均假定「新增 verb `reconcile`
   = 新增第 9 值 → 须改 M2 / M3 已验收用例 → 须走显式豁免申请」。实证（E-1）：该 verb **已在**
   `KnownVerbs()` 内，`KnownVerbs` 恒 **8**。→ **豁免申请不需要发起**；M-004 R-17 的处置②
   （「只允许把 8 改成 9 这一处常量」）在本裁决下**不触发**。R-17 本体不删、不改，只在此登记
   「其触发条件未成立」。
2. **A-39 的门禁基线数字修正**：合同 §3.5.1 与 M-004 未决表第 13 行均写「实测
   `grep -rn "恰三条" .../tools/ | wc -l` → **0**」；2026-11-15 实测 **= 3**（E-23），三处全部在
   `tools/gen_m4_tasks.py` 的 M4 task 正文模板里（含 A-39 自身的选项描述），**无门禁断言本体**。
   → 结论（改 M2 §5.1 计数不破坏任何历史判据）**不变**，但引用该数字时须用 **3**。
3. **A-32 的选项可行性被判据 11 收敛**：合同 §16 A-32 行写「三选项下 §2.1 的三条硬要求都成立」。
   实证（E-7）：新建提案模板本身就产出 `git_commit` 空键，因此 M-004 **判据 11** 的
   `grep -c "git_commit" == 0` 在选项②③下必 FAIL。→ 三选项在**判据层面并不等价**，只有 `整键删除` 可行。
4. **R1 纳管写口的文件名两侧不一致**：合同 §1.3 写 `internal/cli/reconcile.go`，M-004 **判据 4**
   写 `internal/cli/reconcile_commit.go`（并有 `grep -c 'internal/store' internal/cli/reconcile_commit.go`
   == 0 的机器断言）。→ **以 M-004 判据 4 为准**：commit 写口文件名 = `internal/cli/reconcile_commit.go`
   （§2 第 3 段已按此写死）；合同 §1.3 的表述视为同一职责的粗粒度说法，不另改合同正文。
5. **A-33 的实现面补强（两份现文均未提及）**：plan 侧 `mark_reviewed` 现为**校验-only**（E-12），
   R2 若要「经 ChangePlan → plan → store」，T-051 必须为它**新增一个 executor 派发动作**
   （op 名不新增）。这不是新增 op，不影响 `AllOpNames` 的 17。
6. **A-34 的矩阵事实补强（两份现文均未提及）**：矩阵 #33 现为**两路径同 🔴**（E-17）。
   任何 A-34 结论都必须解锁 #33 的一格，故「矩阵完全不动」在实现层面不成立；本裁决把它收敛为
   「**不新增行 + 只解锁 `P-A` 一格 + 只重钉 `BothDeniedRows` 一个计数**」（§5 A-34 行）。
7. **对 R-16 的开工前清障确认（M-004 R-16 要求 048 开工首日确认）**：2026-11-15 实测——
   `mutation_test.py` **413 条 / 检出率 100%**（前置守卫已放行）；五个 final gate 回到
   `m2_final 73/0`、`m3_final 134/0`、`round3 65/0`、`round4 114/0`、`round5 102/0`。
   清障动作 = 清掉本地化未跟踪目录内的 Python 字节码垃圾（`tools/teamwork/vendor/**/__pycache__`，
   `gate_common.JUNK_RE` 命中项），**27 项本地化改动一项未碰、未提交**。

## 9. 下游开工输入（14 个硬下游按此逐项签字）

`T-…-049` ~ `T-…-061` 与 `T-…-063` **开工首日**按下表逐项在各自 Activity Log 签字确认，
汇总记入本 task 的 Activity Log（task Acceptance 的「下游交叉验收」条）。

| # | 项 | 本裁决给出的开工输入（逐字） | 直接消费方 |
|-|-|-|-|
| ① | commit verb 归属 | 纳管 commit 的 verb = `reconcile`（复用既有 `model.VerbReconcile`，`KnownVerbs` 恒 8） | 050 / 058 / 062 |
| ② | `eg check` 退出码 | 有 error 级 finding → 退 `2`（零写入、恒 0 commit）；`6` 结构上不可达 | 059 |
| ③ | `execution.git_commit` 退役形态 | `整键删除`（写侧不产出、`succeeded` 不再必填、读侧宽容历史键、不做迁移） | 060 |
| ④ | R2 / R6 走哪个 op | R2 = 复用 `mark_reviewed`（须新增 executor 动作）；R6 = 新增 `set_stale`；`AllOpNames` 16 → 17 | 049 / 051 / 055 |
| ⑤ | R6 是否需要 `--user-request` | `不需要`；R2 走 `P-U`（#7 / #22 零改动），R6 走 `P-A`（#33 的 `P-A` 阶段解锁，`P-U` 保持 🔴） | 055 / 051 |
| ⑥ | commit 次数 | `恰一次`（0 或 1）；先修复写入、后一次 `git add -A` + commit；error 级 finding 先纳管再退 `2` | 050 / 051 / 055 / 057 / 058 |
| ⑦ | 隐藏信息承载方式 | `warnings 承载 Q4`；`data` 键集合一字不扩张；`W21` 不分配 | 061 |
| ⑧ | `Q` 码表计数 | `改为恰四条`（`Q1..Q4`）；只追加成员、`Q3` 仍恒末位且不被 `Q4` 触发；M2 合同文本落地归 061 / 062 | 061 / 062 |

## 10. 遗留问题与后续（如实登记，不粉饰）

1. **八项均为 Agent 代拍**（owner 2026-09-05 概括性授权，非逐项裁决）。owner 事后改判的回滚范围见 §5；
   `T-…-063` 须在 M4 验收报告里逐项复述八项的最终状态与裁决人属性，**不得声称已获 owner 逐项确认**。
2. **A-36 / A-37 / A-40 三条登记型未决不在本裁决范围**，仍待 owner（A-36 / A-37 需回写飞书权威文档，
   A-40 属 EPIC 更新方式）。本仓一字不改飞书文档。
3. **A-30 的 verb 语义重载残留**：`eg config set` 与 `eg reconcile` 共用 verb `reconcile`
   （E-4）。`git log` 对「对账 vs `eg apply`」可分，对「对账 vs `eg config set`」只能靠 commit 主题
   逐字可分 —— T-050 须落一条主题可分性用例，T-062 须在 `SKILL.md` / README 写明该重载事实。
4. **A-34 的 `P-A` 解锁是本裁决唯一一处「写权限面净放开」**：#33 的 `P-A` 由 🔴 变 ✅。
   缓解手段已写死（只对 `set_stale` 一个 op、只对综述专属两键、`P-U` 保持 🔴、M4 不自动清除
   `stale`），但这一格的放开事实必须在 M4 验收报告里单独留痕，供 owner 复核。
5. **A-33 的 store 侧承载形态留一个二选一给 T-055**：`ApplyStateWrite` 第 5 形态（本裁决建议，
   须重钉 `N_STATE_WRITE_KINDS` 4 → 5 + 自检锚点）vs 独立入口 `ApplyRecapStale`（门禁不动）。
   两者都不改本裁决的 A-33 结论（op 名不变），故不作为阻塞项，但 T-055 必须在 Activity Log 二选一留痕。
6. **`derive_eg_baseline.py` 仍为「模式 B（本仓五方交叉核对）」**：外部锚点（飞书 §14 原文）不在沙箱内，
   `EG-EDIT-05` / `EG-VIEW-05` 是否恰为 S3 那两条需求**无法机器校验**（M-004 未决表第 5 行）。
   与本裁决八项无关，照录不闭合。
