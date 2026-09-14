---
id: 2026-11-11-m3-acceptance-report
title: M3 端到端验收报告（唯一结论文档）
task: T-evergreen.s1_main_flow-158614-047
author: 项目维护者
date: '2026-11-11'
status: final
---

# M3 端到端验收报告

> 判定统一在 `EvergreenDir` 根执行；`E=evergreen`，`D=teamwork/projects/evergreen/s1_main_flow/docs/specs`。
> 全部数字为本轮**实测**，命令与输出摘录逐条给出。被测代码：evergreen@`55d9535`（工作区 porcelain 0，版本 `0.3.0-m3`）。

## §0 结论

**M3 完成判据 17 条，结论：达成 17 条、未达成 0 条、未知 0 条 → M3 达成。**

结论的唯一可复算入口：`cd evergreen && bash test/e2e/m3_acceptance.sh`，末行 `checks=83 failed=0`，
倒数第二行 `M3 结论：17 条判据全部 PASS（离线可复算部分），M3 达成`。

**同时如实披露两条未决 known issues（M3 交付随行，未被本报告宣布解决）**：

- **K-041-01** `execution.succeeded.git_commit` 自指哈希张力 —— 成功 `eg delete` 后必然留 1 条脏变更；两条出路待 owner 裁决（§F.1）。
- **K-043-01** 合同 §5.1 第 2 行第 3 列（deprecated 端点默认不展示）与 M2 §2.2 / §3 冲突 —— 当前只落地「端点已删除 → 退出展示」，失效端点保持 M2 口径，待 owner 裁决（§F.2）。

前提声明（不得被读成更强的结论）：

1. 判据 17 的 `go test ./... -count=1` 全绿以「**仓库与 teamwork 同根共存**」为前提（`I-…-003` 仍 `open`：独立分发包内 `make test` 因读不到 `../teamwork/**` 退 `2`，`make build` / `make lint` 通过）。本报告**不**声称「独立分发包也全绿」。
2. 验收门禁 8（真实 Agent 验证）的执行路径只需 `make build` + `eg apply`，**不需要** `make test`，因此不被 `I-…-003` 卡死。
3. 门禁 8 的证据是本轮**新跑出的 M3 期真实 Agent 会话**（留痕 ID `3c23b9d9-b367-47a0-b65b-828562b60223`），**不是** M2 那一次会话（`56cc0392-ffa1-41ee-9b89-21d7ae5c0199`，后者只用于复述 R-11 的解除依据），也**不是**任何离线回放脚本；该会话的性质限制在 §E.0 逐条声明。

## §A 全量实测数字

| 项 | 判定命令 | 实测 |
|-|-|-|
| lint | `cd $E && make lint` | 退 `0`（`gofmt -l .` 无输出 / `go vet ./...` 通过 / 「写路径无 yaml.Marshal」guard 通过） |
| 单测 | `cd $E && go test ./... -count=1` | 退 `0`；14 个包 `ok`（`cmd/eg` `internal/cli` `internal/git` `internal/mdfile` `internal/model` `internal/plan` `internal/proposal` `internal/query` `internal/query/filter` `internal/report` `internal/rules` `internal/store` `internal/version` `test/e2e`），`skill` 无测试文件 |
| e2e 脚本数 | `ls $E/test/e2e/*.sh \| wc -l` | **25** = M1 + M2 恰 **10** + M3 期恰 **15**（本 task 新增第 15 个 `m3_acceptance.sh`；T-046 收口时的 24 是**当日真事实**，此处按实测重钉，计数只增不减） |
| e2e 全绿 | `for f in $E/test/e2e/*.sh; do bash "$f"; done` | **25/25 退 `0`**（含总控脚本自身；先前 24 个脚本的 24/24 全绿在本轮复跑中同样成立） |
| 总控脚本 | `cd $E && bash test/e2e/m3_acceptance.sh` | 末行 `checks=83 failed=0`；判据 1–17 全 `PASS` |
| 版本号 | `eg --version` / `make print-version` / `version.go` / README / INSTALL | **四处同真 `0.3.0-m3`**（`eg 0.3.0-m3` == `make print-version`） |
| 顶层命令数 | `internal/cli` 注册表 | **18**（`wantCommandCount = 18` 与 e2e 侧 `len(cmds) != 18` 双侧锁）；`eg proposal` 恰 **5** 子命令；`eg rel` 有 `add` / `remove` |
| op / verb / 过滤器 | `plan.AllOpNames()` / `model.KnownVerbs()` / `ls internal/query/filter/*.go` | **16** / **8** / 源文件 **2**（`unreviewed.go` `visibility.go`） |
| 诊断码 | `plan.AllCodes()` | **23** = `E1..E10 ∪ W1..W12 ∪ {I1}`，越界编号 0 命中；`E7`–`E10` / `W9`–`W12` 各 ≥ 1 个用例覆盖 |
| 退出码 | `internal/cli/exit.go` + `exitcode.go` | 全集恰 **{0,1,2,3,4,6}**；`5` **全程不启用**（`ExitCode5Enabled()` 恒 false、进程级 5 号退出 0 命中）；`6` 白名单恰 `{delete, proposal approve}`；判定顺序 参数错 `1` → 校验失败 `2` → 缺确认 `6` |
| 写权限矩阵 | `plan.Matrix()` 等 | 行 **43** / 格 **86** / 路径 **2** / 严格「P-A 🔴 → P-U ✅」**16** / 条件解锁 **1**（#12）/ 两路径同 🔴 **5** / 对象类 **7** |
| teamwork 门禁 | `tools/` 下 10 个脚本 | `validate_m1` **31** / `validate_m2` **31** / `validate_m3` **47** / `round3` **65** / `round4` **114** / `round5` **102** / `m2_final` **73** / `m3_final` **134** / `independent_schedule` **20** / `derive_eg_baseline` **8**，**全部 0 failed** |
| 变异测试 | `tools/mutation_test.py` | **413 条**，检出率 **100%**（还原后工作区逐字复原，porcelain 回到基线 27） |
| 越界门禁 | 见 §D | `.index/` / SQLite / FTS5 / `flock` / `txn/` / `run.lock` / 强原子写 / 崩溃恢复 / `eg reconcile` **均不存在** |

## §B 17 条完成判据逐条复核

结论列取值只有两种（加粗的「达成」/「未达成」），共 17 行（本表即「17 条判据逐条成文」的载体）。

| # | 判据（摘要） | 判定命令（逐字取自 `M-003-m3.md` 判据表） | 实测输出摘录 | 结论 |
|-|-|-|-|-|
| 1 | A-23 / A-24 前置裁决已关闭 | `test -f $D/2026-10-13-m3-prestart-adjudication.md && git -C teamwork ls-files --error-unmatch …`；`grep -c '^\| \*\*A-2[34]\*\* \|'` == 2 | 文档在盘且被跟踪；A-23 结论逐字 `CLI 直写例外`、A-24 结论逐字 `物理移除`，裁决人 `项目维护者`、裁决日期在 §7 留痕 | **达成** |
| 2 | 提案 `status` 四态封闭、无第五值 | `go test ./internal/proposal -run TestStatus_ExactlyFourValues -count=1` | `--- PASS: TestStatus_ExactlyFourValues`（`draft` / `applied` → `E8` + 退 `2`） | **达成** |
| 3 | 合法边恰 5 / 非法边恰 12 / 终态不可再迁 | `go test ./internal/proposal -run 'TestStateMachine_LegalTransitions\|TestStateMachine_IllegalTransitions\|TestTerminalStates' -count=1` | 三个用例全 `PASS`；非法边为表驱动 12 行，逐行 `E9` + 退 `2` + 目标文件字节不变 | **达成** |
| 4 | `superseded` 两个触发全通过、缺 `superseded_by` → `E7` | `go test ./internal/proposal -run 'TestSuperseded_TriggerPartialAccept\|TestSuperseded_TriggerImpactChanged\|TestSupersededBy_Required' -count=1` | 三个用例全 `PASS` | **达成** |
| 5 | `eg proposal approve` 执行前重算影响面 | `grep -n "recomputeImpact\|RecomputeImpact" internal/cli/proposal.go \| wc -l` → ≥ 1；`go test ./internal/cli -run TestApprove_RecomputesImpactBeforeExecute -count=1` | grep 实测 **4**；用例 `PASS`（重算函数调用点在写盘函数之前） | **达成** |
| 6 | `status` × `execution` 4 × 3 可达矩阵生效，`approved ≠ 已执行` | `go test ./internal/proposal -run 'TestOrthogonality_ReachabilityMatrix\|TestApprovedDoesNotImplySucceeded' -count=1` | 两个用例 `PASS`（表驱动 12 行：6 ✅ / 6 拒绝） | **达成** |
| 7 | `execution=failed` 时报告逐路径列出已写 / 未写 | `go test ./internal/proposal -run TestExecutionFailed_ListsWrittenAndUnwrittenPaths -count=1` | `PASS`：两键均存在、并集 == 影响文件全集、`unwritten_paths` ⊇ 报告 `skipped[].target` 中属本提案者 | **达成** |
| 8 | 用户显式命令可改核心内容，**Agent 自动路径改不了** | `go test ./internal/plan -run TestE6_AgentAppendCoreKnowledgeRejected -count=1` **与** `go test ./internal/cli -run TestEdit_UserExplicitCoreKnowledgeAccepted -count=1` | 两条同时 `PASS`；真实会话侧同真：S14 `eg edit --section 知识内容 … --user-request` 退 `0` 且正文逐字生效，S15 `--section 用户补充` 退 `2`（`E6`）零写入 | **达成** |
| 9 | 授权判据 = `initiator: user` **＋** 命令行 `--user-request`（反伪造 N-1） | `go test ./internal/cli -run TestUserRequestFlagRequired -count=1` | `PASS`：plan 内写 `initiator: user` 但命令行无 `--user-request` → 退 `2` | **达成** |
| 10 | 写口唯一（矩阵 #3#4#5#6 总闸）+ rules 包无可变引用 | 判据裸词 grep（见右） | 裸词 grep 实测 **40** 行（命中 op 名常量 / `ActionKind` 常量 / 矩阵备注），按**调用形态** `grep -rnE '\b(SetStatus\|SetReplacedBy\|SetDeleted)\('` 复算：非测试调用点**全部**落在 `internal/store/state_write.go`，白名单外 **0**；`grep -rn "\*model\.Card" internal/rules/` → **0** | **达成** |
| 11 | 退出码 `6` 启用且恰限两条命令，语义为「权威 Markdown 完全不变」 | `go test ./internal/cli -run TestExitCode6_OnlyAfterValidationPasses -count=1`；`grep -rn "os.Exit(5)" internal/ \| wc -l` → 0 | 用例 `PASS`；`5` 落点 **0**；本 task 新增 `TestExitCodeSetClosed` `PASS`（全集 {0,1,2,3,4,6} + 白名单恰两条 + 顺序 1→2→6）；真实会话 S09 实测退 `6` 且**全库 9 个 `.md` 的 sha256 逐行相等、顶端 commit 不变、porcelain 0** | **达成** |
| 12 | 逻辑删除全时序：删除后无自动状态变化 / 关系不被物理删除 / 恢复不校验 support / `undelete` 不动 `status` | `go test ./internal/cli -run 'TestDelete_NoAutoStatusChange\|TestRestore_NoSupportPrecondition\|TestUndelete_StatusUntouched' -count=1` + `go test ./internal/store -run TestLogicalDelete_RelationsPreserved -count=1` | 四个用例全 `PASS`；真实会话 S20 / S23 同真：`delete` 后 `status` 仍 `deprecated`、`undelete` 只清两键、关系条目数前后相等（1 → 1） | **达成** |
| 13 | `reviewed_at` 只由 `eg mark-reviewed` 写入，ADR-20 文件级隔离成立 | `grep -rln "filter/unreviewed" internal/query/rank internal/query/relations internal/rules/converge internal/query/review \| wc -l` → 0；`go test ./internal/cli -run 'TestMarkReviewed\|TestUnreviewed' -count=1` | grep **0**；两个用例 `PASS`；真实会话 S24–S26：`unreviewed` 只读、`mark-reviewed` 后目标退出清单 | **达成** |
| 14 | `[已删除]` / `[未过目]` 两标记输出，双标记顺序固定 `[失效][已删除]`；`[材料支持不足]` 不出现 | `go test ./internal/cli -run TestMarkers_DeletedAndUnreviewed -count=1`；`grep -rn "材料支持不足\|insufficient_support" internal/ \| grep -v _test.go \| wc -l` → 0 | 用例 `PASS`（含顺序断言）；grep **0**；真实会话 S22 `--include-deleted` 命中带 `[已删除]` | **达成** |
| 15 | 8 个新增 op 全部可执行，诊断码闭合，`skipped[].kind` 不新增 | `grep -rhoE '"(E\|W\|I)[0-9]+"' internal/ \| sort -u` ⊆ 闭合集合；`go test ./internal/plan -run 'TestW7_IsErrorInM3\|TestM3Ops' -count=1` | 码集合恰 **23** 值且闭合、越界 0；两个用例 `PASS`；本 task 新增 `TestM3OpsAllExecutable`（8 个 op 逐个在派发全集内、字段表非空、各有 e2e 落点）与 `TestDiagnosticCodesCovered`（`E7`–`E10` / `W9`–`W12` 各 ≥ 1 覆盖、`kind` 非空取值恰两值、`block_conflict` / `block_hash_changed` 0 命中）均 `PASS` | **达成** |
| 16 | M3 接管 M2 的 `rel remove` 占位 | `grep -n "M3/S2 未实现" internal/cli/rel.go \| wc -l` → 0；一次 `eg rel remove` 后 commit 恰 +1 且 `git log -1 --pretty=%s` 以 `relate(` 开头 | 占位 **0**；`m3_rel_remove.sh` 退 `0`；真实会话 S12 实测：匹配条目**物理移除**（1 → 0）、**无墓碑字样**、`.md` 文件数 9 → 9（不删文件） | **达成** |
| 17 | B1–B4 与 M1 / M2 全量用例不回归；不引入 M4–M6；验收报告成文 | `make lint && go test ./... -count=1` 全绿；e2e 逐个退 `0`；版本四处同真；越界 grep 全 0；报告在盘且被 Git 跟踪 | lint / 单测全绿；**25/25** e2e 退 `0`；版本 `0.3.0-m3` 四处同真、`0.2.0-m2` 在 M2 历史证据白名单外零残留；越界门禁见 §D 全 0；本报告在盘且 `git -C teamwork ls-files --error-unmatch …` 退 `0` | **达成** |

**判据覆盖自查**：§16.1 M3 原文三句判据 → 第 2/3/4/5 条、第 7 条、第 8 条；两份合同硬约束 → 第 6/9/10/12/13/14/15 条；`rel remove` 占位接管 → 第 16 条；退出码 `6` → 第 11 条；前置裁决 → 第 1 条；不回归与不越界 → 第 17 条。

## §C M3 交付范围（T-032 ~ T-047 逐条一行结论）

| Task | 交付 | 状态 | 一行结论（实测事实） |
|-|-|-|-|
| 032 | M3 开工前置裁决（A-23 / A-24） | `done` | owner `项目维护者` 裁决 **A-23 = `CLI 直写例外`**（+5 条附加约束）、**A-24 = `物理移除`**（+5 条附加约束），9 条硬依赖边一条未摘 |
| 033 | `proposal` 包与提案落盘形态 | `done` | `vault/proposals/p-<yyyymmdd>-<3d>.md`、frontmatter 键集合含 `execution.written_paths[]` / `unwritten_paths[]`、正文恰 7 个 H2、提案目录被排除出知识扫描面 |
| 034 | 四态状态机与 E8 / E9 | `done` | 四态封闭、合法边恰 5 / 非法边恰 12（表驱动）、`rejected` / `superseded` 终态、`approved` 判非终态 |
| 035 | `superseded` 两触发 + approve 先重算影响面 | `done` | S-①部分接受 / S-②前提变化两触发全通过，缺 `decision.superseded_by` → `E7`；重算发生在写盘之前 |
| 036 | `execution` 三态与失败逐路径报告 | `done` | 4 × 3 可达矩阵（6 ✅ / 5 🔴 / 1 ⚠️）生效；`failed` 时两键并集 == 影响文件全集 |
| 037 | 8 个新增 op 与 E7–E10 / W9–W12 | `done` | op 派发全集 16、W7 自 S2 起升 error（窄口径恰 5 个状态类 op）、W3 起真正判定、码集合闭合 23 值 |
| 038 | 授权判据与 43 行写权限矩阵 | `done` | `initiator: user` **＋** 命令行 `--user-request` 双条件；43 行 / 16 严格解锁 / 1 条件解锁（#12）/ 5 两路径同 🔴 逐行可反证 |
| 039 | `eg deprecate` / `restore` / `replaced-by` | `done` | 只动 `status` 与 `replaced_by`；写口唯一；`replaced-by` 单向（被指向卡字节不变），E10 / W12 到位 |
| 040 | `eg proposal new\|list\|show\|approve\|reject` 与退出码 `6` | `done` | 恰 5 子命令；`new` 允许 Agent（可提不可执）、`approve` 只允许 P-U；缺 `--confirm` 退 `6` 零写入 |
| 041 | `eg delete` / `eg undelete` 与十一步时序 | `done` | 三件前置（approved 提案 + `--confirm` + `--user-request`）齐备才写；只写两键、不改 `status`、不删文件、不级联删关系；**遗留 K-041-01（未决）** |
| 042 | `reviewed_at` / `mark-reviewed` / `unreviewed` + ADR-20 | `done` | `reviewed_at` 唯一写入路径成立；`unreviewed` 只读无副作用；过滤器跨包引用 0（文件级隔离） |
| 043 | `[已删除]` / `[未过目]` 标记与双标记顺序 | `done` | 顺序固定 `[失效][已删除]`；`[材料支持不足]` 零输出；**遗留 K-043-01（未决）** |
| 044 | `eg rel remove` + `remove_relation` 接管占位 | `done` | 按 A-24 物理移除全部匹配条目、不留墓碑、不删文件；未命中 → W10 幂等零 commit；占位字样全仓 0 |
| 045 | `eg edit`（A-13） | `done` | 可编辑分区白名单恰 3（知识内容 / 解释与依据 / 条件与边界）；`用户补充` 恒退 `2`（E6）；缺 `--user-request` 落 P-A → E6；三维不牵连；B3 不豁免 |
| 046 | 文档 / SKILL / README / INSTALL / 版本号收口 | `done` | 版本 `0.2.0-m2` → **`0.3.0-m3`** 四处同真；README 覆盖 18 命令全集；INSTALL 含退出码表（`6` 在册、`5` 标未启用）与已知限制 R-12 / R-13 / K-041-01 / K-043-01；文档命令实跑 56/56 |
| 047 | M3 验收（本 task） | `done` | 本报告 + `test/e2e/m3_acceptance.sh`（`checks=83 failed=0`）+ `test/e2e/m3_acceptance_test.go`（4 条闭合性断言 + 2 条验收自守）+ M3 期真实 Agent 会话留痕入库；e2e 24 → **25** |

## §D 越界门禁（不引入 M4–M6 能力）

| 门禁 | 判定命令 | 实测 |
|-|-|-|
| `eg reconcile` 零注册 | `grep -rn "reconcile" internal/cli/` | 裸词 **14** 行，**全部**是「`config set` 的 commit verb 说明」（`commands.go` 2 行，逐字含「≠ S3 的 eg reconcile 命令」）与测试里的反证串（12 行）；命令注册与用法行 **0**——`eg reconcile` 命令**不存在** |
| S5 符号 | `grep -rnE "flock\|txn/\|run\.lock\|FTS5\|sqlite" internal/ cmd/` | 裸词 **1** 行，是 `internal/query/relation.go` 的**阶段说明注释**（判据原文允许「仅命中标注 S3–S5 阶段的注释」）；非注释命中 **0** |
| 退出码 5 | 进程级 5 号退出落点 | **0**；`cli.ExitCode5Enabled()` 恒 `false` |
| 物理删除 | `grep -rn "os.Remove" internal/ --include=*.go \| grep -v _test.go` | 恰 **2**，集合恰 `{internal/cli/ymlwrite.go, internal/store/write.go}`（均为 tmp 清理，不删任何权威 Markdown） |
| 破坏性回滚 | `grep -rnE "checkout --\|reset --hard\|RemoveAll" internal/ \| grep -v _test.go` | **0** |
| 索引 | `test -d $E/vault/.index` | 不存在（`OK`）；`.index/` / SQLite / FTS5 / 增量索引全无 |
| 性能门槛 | `grep -rn "P95\|万卡" internal/` | **0** |
| 占位字样 | `grep -rF "M3/S2 未实现" internal/ README.md INSTALL.md skill/SKILL.md` | **0** |
| 三条禁止措辞 | `跳过 hash 比对` / `自动回滚到执行前` / `Agent 自动路径亦可改` | 三条**逐条 0 命中** |
| `[材料支持不足]` | `grep -rn "材料支持不足\|insufficient_support" internal/ \| grep -v _test.go` | **0**（属 S3） |
| 强原子 / 崩溃恢复 | `txn/` / `run.lock` / 写前复核 / 恢复流程 | 均不存在（属 S5 / M6） |

## §E M3 期真实 Agent 会话（验收门禁 8）

原始留痕入库路径：`evergreen/test/e2e/testdata/ppe/m3-raw-session/`（14 个原件，随 T-047 一并被 Git 跟踪）。
**留痕 ID `3c23b9d9-b367-47a0-b65b-828562b60223`**（本会话自建标识，`≠` M2 的 `56cc0392-ffa1-41ee-9b89-21d7ae5c0199`）。

### §E.0 证据性质与限制（如实声明）

1. 这是**一次真实执行**：每步都真实调用 `eg 0.3.0-m3` 二进制、真实退出码、真实落盘、真实 Git commit；
   `m3-ops-plan.json` 是本会话**自主撰写**的 ChangePlan（含 3 个 M3 新增 op），经 `eg apply` 真实执行并产生 commit。
2. 它**不是** Agent Harness E2E 产品线上的会话：沙箱内无 PPE 会话服务，会话 ID 是本会话自建标识而非平台侧会话 ID。
   因此「PPE 平台侧会话」这一层外部事实**未验证**；可验证的是「真实 Agent 通过 CLI 端到端跑通 M3 全主流程」。
   本报告**不**把它包装成 PPE 平台会话，也**不**因此放宽判据 8 的任何一条断言。
3. 目录内**不含**任何离线回放脚本，全仓 `m3_*replay*.sh` 命中 **0**（由 `TestM3RawSessionTraceInPlace` 锁死）。
4. 门禁 8 的执行路径只需 `make build` + `eg apply`，**不需要** `make test`，故不受 `I-…-003` 影响。

### §E.1 逐步记录（28 步；退出码 100% 符合预期，无意外）

| 步 | 命令 | 退出码 | 关键断言 |
|-|-|-|-|
| S01 | `eg init --domain ai-infra` | 0 | 骨架 + `SKILL.md` + Git 仓建立 |
| S02 | `eg config set domains ai-infra` | 0 | 一次 `reconcile` commit |
| S03 | `eg capture --url … --body-file … --json` | 0 | 材料 `s-20261109-retrieval-augmented-…` 落盘并登记收件区 |
| S04 | `eg context --source … --json` | 0 | 取得 `base[unprocessed.md] = sha256:52d5c7d8…` |
| S05 | `eg apply --plan plan-ingest.json --json` | 0 | 1 篇材料笔记 + 4 张卡 + 1 条未决问题，一次 `process` commit |
| S06 | `eg proposal new --type logical_delete --target KC` | 0 | 生成 `p-20260904-001`；**未带** `--user-request` 也成立 → Agent「可提不可执」 |
| S07 | `eg proposal list --json` | 0 | 只读，零 commit |
| S08 | `eg proposal show p-20260904-001 --json` | 0 | 只读，零 commit |
| **S09** | `eg proposal approve … --user-request --json`（**不带** `--confirm`） | **6** | **权威 Markdown 完全不变**：全库 9 个 `.md` 的 sha256 清单 before/after **diff 为空**（逐行相等）；顶端 commit 不变；`porcelain` 0 |
| S10 | `eg proposal approve … --confirm --user-request --json` | 0 | `status: 'approved'`，`execution.status: not_started`（approved ≠ 已执行） |
| S11 | `eg rel add KA supports KB --reason …` | 0 | 一次 `relate` commit |
| S12 | `eg rel remove KA supports KB --reason …` | 0 | 匹配条目**物理移除**（1 → 0）、**无墓碑字样**、`.md` 文件数 9 → 9（**不删文件**） |
| S13 | `eg rel add KA supports KB --reason …` | 0 | 重建一条关系，留给 S27 的 `remove_relation` op |
| S14 | `eg edit --target KA --section 知识内容 --content … --user-request` | 0 | 新正文逐字生效；`status` / `deleted_at` / `reviewed_at` **三维逐字不变** |
| S15 | `eg edit --target KA --section 用户补充 --content … --user-request` | **2** | 诊断 `E6`，**恒拒**、零写入（全库 sha256 清单不变） |
| S16 | `eg deprecate --target KD --reason …` | 0 | `status: deprecated`，删除 / 过目两维为空 |
| S17 | `eg restore --target KD --reason …` | 0 | `status: active`，另两维仍为空（**只动 `status`**） |
| S18 | `eg deprecate --target KC --reason …` | 0 | 为 `replaced-by` 准备失效卡 |
| S19 | `eg replaced-by --target KC --to KA --reason …` | 0 | KC 写 `replaced_by{target,reason}`；**被指向卡 KA 字节不变**（单向） |
| S20 | `eg delete --target KC --reason … --proposal p-20260904-001 --confirm --user-request` | 0 | 三件齐备才写；只写 `deleted_at` + `deleted_reason`，`status` 仍 `deprecated`；commits 13 → 14；**留下恰 1 条脏变更**（K-041-01，见 §F.1） |
| S21 | `eg search 召回 --json` | 0 | 已删除的 KC **命中 0 次**（默认视图排除） |
| S22 | `eg search 召回 --include-deleted --json` | 0 | KC 带回（命中 2 次），带 `[已删除]` 标记 |
| S23 | `eg undelete --target KC --reason …` | 0 | 两个删除键清空；`status` 仍 `deprecated`（**只动删除维度**）；KA 关系条目数不变（**关系不被级联删**） |
| S24 | `eg unreviewed --json` | 0 | KB 在未过目清单内（只读） |
| S25 | `eg mark-reviewed --target KB --json` | 0 | **只写 `reviewed_at` 单键** |
| S26 | `eg unreviewed --json` | 0 | KB 退出未过目清单 |
| **S27** | `eg apply --plan m3-ops-plan.json --json --user-request` | 0 | **门禁 8 本体**：自撰 plan 含 3 个 M3 新增 op（`deprecate` / `remove_relation` / `mark_reviewed`）真实执行 → commits 16 → 17；KD → `deprecated`，KA 关系条目物理移除 |
| S28 | `eg report --last --json` | 0 | 信封顶层键 `['data','exit_code','ok','status','warnings']`，`ok=true` / `exit_code=0` / `status=completed`；`data` 键 `['convergence','domain','report']`；阶段字段 `report.reconcile.ran=false`、`report.support_check=[]`（S3 不提前） |

**汇总**：28 步全部按预期退出（`0` × 26、`6` × 1、`2` × 1），`unexpected.txt` 为空，会话结束时 vault 17 个 commit、`porcelain` 0。

### §E.2 退 `6` 的 sha256 对比（判据 11 的语义证据）

```
sha256sum $(git ls-files '*.md' | sort) > sha256-before-exit6.txt   # 退 6 之前
eg proposal approve p-20260904-001 --user-request --json ; echo $?  # → 6
sha256sum $(git ls-files '*.md' | sort) > sha256-after-exit6.txt    # 退 6 之后
diff sha256-before-exit6.txt sha256-after-exit6.txt                 # → 无输出（退 0）
```

实测：两份清单各 **9** 行，`diff` **无输出**（逐行完全相等），顶端 commit 未变，`git status --porcelain` 为空。
即「退 `6` = 校验已过、仅缺确认、**权威 Markdown 完全不变**」成立。

## §F 未决事项如实披露

### §F.1 K-041-01（`execution.succeeded.git_commit` 自指哈希张力）—— **未决**

- **现象（本会话 S20 实测）**：成功 `eg delete` 之后 `git status --porcelain` 恰 **1 行**：` M proposals/p-20260904-001.md`。
- **diff 范围**：**只落在 `execution` 六键块内**（`+5 / −4` 行）——`status` `not_started → 'succeeded'`、
  `attempted_at` 空 → `'2026-09-04T07:12:47+08:00'`、`git_commit` 空 → `'51c15093…'`、`written_paths` `[]` → 1 条路径；
  `reason` / `unwritten_paths` 保持原值。**六键之外一字未动**。
- **成因**：`execution.git_commit` 只能在 commit **之后**取得，写回它必然产生第二次变更（自指哈希）。
- **影响面**：`eg delete` 成功路径**必然**留 1 条脏变更；上层若断言「命令后工作区必为 clean」会失败；
  提案文件的 `execution` 与实际 commit 之间存在一个「已执行但未落 SHA」的瞬时窗口。
- **两条待裁决出路**：① 放宽提案合同 §4.3，`execution` **不存** SHA（改存 commit 主题 / 序号）；
  ② 接受 `+2 commits`（第二次 commit 专门写回 `execution`）。**owner 未裁决前 M3 不做任何绕过**，本报告不宣布其解决。

### §F.2 K-043-01（合同 §5.1 第 2 行第 3 列 与 M2 §2.2 / §3 冲突）—— **未决**

- **冲突点**：M3 合同 §5.1 表第 2 行第 3 列要求「**deprecated（失效）端点默认不展示**」；
  M2 的 §2.2 / §3 口径是「**失效端点仍展示并带 `[失效]` 标记**」。两者对同一场景给出相反的默认视图。
- **M3 当前落地**：**只**实现「端点**已删除** → 退出展示」；**失效**端点保持 **M2 口径**（继续展示 + `[失效]` 标记），
  双标记顺序固定 `[失效][已删除]`。即 M3 **未**按 §5.1 那一格收窄默认视图。
- **影响面**：关系视图的默认可见性以 M2 口径为准；若 owner 最终采纳 §5.1 那一格，需改动关系展示过滤器并同步 M2 侧断言（属**收窄**，会打破现有 M2 用例）。
- **待裁决**：① 修正 §5.1 第 2 行第 3 列，与 M2 对齐（保持现状）；② 维持 §5.1，S2/S3 内改造关系视图并同步重写 M2 断言。

### §F.3 其他仍 `open` 的既有 issue（复述，不在本报告宣布解决）

| ID | 状态 | 与 M3 判据的关系 |
|-|-|-|
| `I-…-002` `apply --dry-run` 报告计数与 `planned[]` 不同源 | `open`（M2 遗留） | 17 条判据无一以 dry-run 计数为入口；M3 的报告体（判据 7 / 12）**未**复制该缺陷；修复本体仍在 M2 侧 |
| `I-…-003` 独立分发包内 `make test` 读不到 `../teamwork/**` 退 `2` | `open` | 判据 17 的全绿以「同根共存」为前提（见 §0 前提 1）；门禁 8 不受影响（见 §E.0 第 4 条） |

## §G A-13 ~ A-29 与 R-7 ~ R-13 逐条复述

### §G.1 文档待修正登记项（A-13 ~ A-24 来自两份 M3 合同；A-25 ~ A-29 来自本里程碑）

| ID | 一行内容 | M3 收口时的状态 |
|-|-|-|
| **A-13** | 飞书 §7.1 把 `eg edit` 标 S2，§16.1 M3 交付列未点名 | **已按 M3 执行并落地**（T-045：`eg edit` 为「用户显式可改核心内容」唯一命令载体）；权威文档侧仍待 owner 补脚注 |
| **A-14** | §10.3 引用的校验编号 V9 / V10 在授权合同内无定义源 | **已按语义落地**（`initiator=user` + `delete` 必引 `approved` 提案），编号本身仍待权威侧补定义 |
| **A-15** | §4.5.1 W7「状态类 op」范围未列举 | **按窄口径落地**（恰 5 个状态类 op），W7 自 S2 起为 error（T-037）；权威侧待补显式列举 |
| **A-16** | §5.5 `reviewed_at` 的「用户直接编辑 → 对账补齐」属 S3 | **M3 只落 `eg mark-reviewed` 单一写入路径**，对账补齐留 S3；登记不变 |
| **A-17** | §7.2 退出码表行序 `…4 6 5`，与本仓「S1 恰五条」冲突 | **已收口**：M3 启用 `6`、`5` 全程不启用；INSTALL 退出码表已同步（T-046）；权威侧行序仍待修正 |
| **A-18** | §16.1 M3 交付列与 §7.1 命令表的归属差异（含归属未知项） | **M3 不实现、也不声称其不属 M3**（如自检问题块相关面），保持登记 |
| **A-19** | §14.4 把 EG-CHK-06 标 S1，§4.2/§4.5/§16.1 标 S2 | **按 S2 执行**（`replace_block` 归 M3 / T-037），追溯上暂挂并标注阶段列存疑；待 owner 修正 §14.4 |
| **A-20** | §4.4 提案 yaml 同时存在顶层 `status` 与执行状态字段 | **已按「两维正交」落地**（`status` 四态 × `execution` 三态，4 × 3 可达矩阵，T-034 / T-036） |
| **A-21** | §10.1 表要求 `execution` 含 `failed` + `reason` + 已写/未写路径 | **已落地**（`written_paths[]` / `unwritten_paths[]` 两键，判据 7 实测通过） |
| **A-22** | §10 状态图存在 `rejected --> [*]` / `superseded --> [*]` 两条终止边 | **已落地**（两态为终态，终态再迁 → `E9` + 退 `2`，判据 3 实测通过） |
| **A-23** | 提案写入通道：ChangePlan 唯一通道 vs 提案由 CLI 直写 | **已由 owner 裁决关闭 = `CLI 直写例外` + 5 条附加约束**（T-032）；M3 全程按裁决施工，只许更严 |
| **A-24** | `remove_relation` 落盘语义：物理移除 vs 标记删除 | **已由 owner 裁决关闭 = `物理移除` + 5 条附加约束**（T-032）；T-044 实测物理移除、不留墓碑、不删文件、不级联 |
| **A-25** | 本仓 `EPIC.md` `target: 2026-10-09` 早于 `M-003.date = 2026-11-11` | **本轮不改 `EPIC.md`**（改它等于放宽 M2 三值相等硬断言）；M3 收口日以本里程碑 `date` 为准；待 owner 二选一 |
| **A-26** | 飞书 §14 S2 集合恰 18 条，其中 EG-VIEW-02 / EG-VIEW-07 已由 M2 提前落地 | M3 承接 **16** 条；各 task 关联需求均为这 18 个真实 ID 的子集，**未自造 ID**；待权威侧补阶段脚注 |
| **A-27** | EG-CHK-06 阶段列 S1 vs 三处 S2 | 同 A-19，按 S2 执行；追溯暂挂 |
| **A-28** | §4.6 `support_check[]` 整列标 S3，与 §5.2 / §10.2 的删除报告清单冲突 | **路径分治已落地**：**只有 `eg delete` 路径**产出 `support_check[].recommendation`（本会话 `delete-output.json` 可验），**其余路径仍为空数组**；`affected` / `reconcile` 派生字段仍属 S3 |
| **A-29** | 本仓 CLI 合同 §5 的 `code` 枚举写成 `E1`–`E6` / `W1`–`W8` / `I1`（S1 结论） | **本仓侧已关闭**：T-046 在 §5 追加阶段脚注（只追加不改写），判据 15 的集合闭合断言为真（恰 23 值、越界 0） |

### §G.2 风险与限制（R-7 ~ R-13）

| ID | 复述 | M3 收口结论 |
|-|-|-|
| **R-7** | 原「A-23 未关闭」 | **已解除**：owner 裁决 `CLI 直写例外` + 5 条约束；033/034/035/036/040/041/047 逐条核对通过；9 条对 032 的硬依赖边**一条未摘** |
| **R-8** | 原「A-24 未关闭」 | **已解除**：owner 裁决 `物理移除` + 5 条约束；037 / 044 按口径 1–4、041 / 043 按口径 5 施工；`Relation` 仍恰三字段、B1 / B3 未放宽 |
| **R-9** | 提案多文件写入**非强原子**，逻辑删除涉多文件时可能部分写入 | **接受为 M3 既定形态**，未引入强原子（属 S5 / M6）；代价由 `execution=failed` 的逐路径报告兜底（判据 7 实测通过） |
| **R-10** | W7 自 S2 起由 warning 升 error，Agent 生成的状态类 op 从可执行变退 `2` | **已一次改到位**（T-037，含 `TestW7_IsErrorInM3`）；`SKILL.md` 已同步给 Agent 的规程（T-046），避免继续生成会被拒的 plan |
| **R-11** | 真实 Agent 会话强度要求（继承 M-002 R-5） | **已解除**（M2 侧 2026-09-02 订正）：解除依据 = M2 真实会话 `56cc0392-ffa1-41ee-9b89-21d7ae5c0199`，原始留痕在 `evergreen/test/e2e/testdata/ppe/raw-session/`（另见 `docs/specs/2026-10-09-m2-acceptance-report.md` §0 / §C 与 `2026-10-06-m2-ppe-agent-e2e-evidence.md`）。**M3 未退回更弱证据**：本轮出具 M3 期真实 Agent 会话 `3c23b9d9-…`（自撰含 3 个 M3 op 的 plan → `eg apply` 真实执行 → 留痕入库 `test/e2e/testdata/ppe/m3-raw-session/`），**未**使用任何 replay 脚本、**未**以 M2 那次会话冒充 M3。**但**「Agent Harness E2E 平台侧会话」这一层外部事实**未验证**（§E.0 第 2 条），此限制随 M3 交付如实保留 |
| **R-12** | macOS 产物仅交叉编译、未真机验证（继承 R-4） | **保持已知限制**：INSTALL / release 说明继续登记，不承诺已验证 |
| **R-13** | `git add -A` + 非强原子 + 整文件冲突跳过（继承 R-6） | **保持不变**：M3 新增写入路径同样沿用 `-A` 与整文件跳过（`kind=file_changed`），**未**偷改为选择性 add / 强原子 / 块级合并 |

## §H M3 未做的范围边界（S2 之后的遗留项）

以下能力 **M3 明确不做**，且已由 §D 越界门禁逐条反证「代码中不存在」：

1. **索引与检索加速**：`.index/`、SQLite、FTS5、增量索引、`P95` 性能门槛 —— 属 S3 / S4。
2. **并发与强原子**：`flock`、`run.lock`、`txn/`、多文件强原子写、写前复核、崩溃恢复 —— 属 S5 / M6（R-9 已登记接受）。
3. **对账命令 `eg reconcile`**：命令零注册；`reviewed_at` 的「用户直接编辑后对账补齐」（A-16）留 S3。
4. **`support_check[]` 全量计算**：仅 `eg delete` 路径产 `recommendation`（A-28 路径分治），`affected` / `reconcile` 派生字段与 `[材料支持不足]` 标记均属 S3（后者全仓 0 命中）。
5. **退出码 `5`**：定义在册但**全程不启用**（属 S5）。
6. **块级合并 / 选择性 `git add`**：B3 仍为整文件跳过（R-13）。
7. **真机 macOS 验证**：仅交叉编译（R-12）。
8. **归属未知项**：A-18 点名的归属未定能力，M3 不实现也不声称其不属 M3。

## §I 一键复算入口

```bash
cd EvergreenDir/evergreen
make lint                         # 退 0
go test ./... -count=1            # 退 0（前提：与 teamwork 同根共存，见 §0 前提 1）
for f in test/e2e/*.sh; do bash "$f" >/dev/null 2>&1 && echo "ok $f" || echo "FAIL $f"; done   # 25/25 ok
bash test/e2e/m3_acceptance.sh    # 末行 checks=83 failed=0，判据 1–17 全 PASS
go build -o /tmp/eg ./cmd/eg && /tmp/eg --version   # eg 0.3.0-m3

cd ../teamwork                    # 10 个门禁 + 变异测试
for s in validate_m1_tasks validate_m2_tasks validate_m3_tasks round3_final_gate round4_final_gate \
         round5_final_gate m2_final_gate m3_final_gate independent_schedule_check derive_eg_baseline; do
  python3 projects/evergreen/s1_main_flow/tools/$s.py; done      # 全部 0 failed
python3 projects/evergreen/s1_main_flow/tools/mutation_test.py   # 413 条，检出率 100%
```

---

**签署**：项目维护者（T-047 验收负责人），2026-11-11。
**报告立场**：M3 达成 17/17；同时随交付如实披露 **K-041-01**、**K-043-01** 两条未决 known issues 与 **R-9 / R-11（PPE 平台层未验证）/ R-12 / R-13** 四条已知限制。
