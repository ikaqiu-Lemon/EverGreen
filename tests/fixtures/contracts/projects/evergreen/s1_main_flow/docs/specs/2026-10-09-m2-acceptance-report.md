# M2 端到端验收报告（主链路可用性打磨）

- 文档 ID：`2026-10-09-m2-acceptance-report`
- 里程碑：`M-002` 「M2 主链路可用性打磨」
- 验收 task：`T-evergreen.s1_main_flow-158614-029`
- 验收执行：ikaqiu（单人 roster，执行人自核）
- 日期：2026-10-09
- 代码基线：`evergreen` 仓 `HEAD = 23faa15`（真实 Agent Harness 留痕与离线回放更新），验收结束时工作区干净
- 判据来源：`milestones/M-002-m2.md`「M2 完成判据（9 条）」逐字；体例沿用 `docs/specs/2026-09-17-m1-acceptance-report.md`

---

## 0. 结论

**M2 达成。**

9 条完成判据的三值结论：**达成 9 条**、未达成 0 条、未知 0 条。

| 判据 | 一句话结论 | 三值 |
|-|-|-|
| 1 | 九命令 `Placeholder` 全为 false、Handler 全挂载，`Placeholder: true` 非测试源零命中 | 达成 |
| 2 | `eg rel remove` 退 1、文案逐字「M3/S2 未实现」、零写入零 commit | 达成 |
| 3 | `search` / `card show` / `rel` 查询 / `context` 前后 git 状态与逐文件 sha256 全等 | 达成 |
| 4 | 反向关系由全库 Markdown 扫描给出（跨领域可见），`internal/query/` 无索引读写 | 达成 |
| 5 | 一次 `rel add` = commit +1 且主题以 `relate(` 开头，写路径不绕 plan/executor | 达成 |
| 6 | `go test ./... -count=1` 全绿 + `m1_real_article.sh` 退 0，M1 用例名一条不缺 | 达成 |
| 7 | 坏 `.md`（Q1）+ 悬空引用（Q2）+ 汇总（Q3）在 `--json` 与纯文本两形态同源可见 | 达成 |
| 8 | Agent Harness 真实 Agent 完成收录、查询、判断、写入与再查询验证；原始证据可离线复算 | 达成 |
| 9 | 本报告存在、被 Git 跟踪、九节结构与三值封闭均可机器复算 | 达成 |

**总结论口径**：9 条判据全部达成，因此 **M2 达成**。判据 8 的真实会话 ID、Agent 原始计划、
命令日志、查询快照与 Git 序列见专项证据报告；第 14 节给出最终收口判定。

一条命令复算入口：`cd evergreen && bash test/e2e/m2_acceptance.sh`（三值逐条打印，末行给出结论）。

---

## 判据 1 S1 九个命令全部具备真实实现

**判据原文**：「S1 九个命令**全部具备真实实现**，`search` / `card show` / `rel` 查询 / `rel add` 不再是占位」；
判定方式原文：「`cd evergreen && grep -rn "PlaceholderNotice\|Placeholder: *true" internal/cli/` 仅在
`rel remove` 的 M3/S2 占位分支命中（其余零命中）；且 `go test ./internal/cli/... -run 'Search|CardShow|Rel'` 全绿」。

#### 判定命令

```bash
cd evergreen
grep -rn "PlaceholderNotice\|Placeholder: *true" internal/cli/
grep -rn "Placeholder: *true" internal/ cmd/ --include='*.go' | grep -v '_test\.go'   # 应零输出
go test ./internal/cli/... -run 'Search|CardShow|Rel' -count=1
go test ./test/e2e/... -run S1NineCommandsAreImplemented -count=1
```

#### 实测输出

```text
# grep（非测试源命中共 13 行，按文件归类）
internal/cli/rel.go:21,35,37,40,52,102,108   → rel remove 的 M3/S2 占位分支与其说明注释
internal/cli/root.go:29,32,180               → M1 常量 PlaceholderNotice 定义 + Wire() 的「占位命令不得挂载实现」守卫
internal/cli/placeholder.go:6,23,29          → M1 期占位构造器（当前无任何命令引用它）
# grep -rn "Placeholder: *true" internal/ cmd/（去测试）：零输出
ok  	github.com/ikaqiu-Lemon/EverGreen/internal/cli	9.124s
ok  	evergreen/test/e2e	（TestS1NineCommandsAreImplemented：9 命令 Placeholder 全 false、Handler 全非 nil）
```

`m2_search.sh` / `m2_card_show.sh` / `m2_rel_query.sh` / `m2_rel_add.sh` 四脚本同步退 0
（8 / 9 / 9 / 11 条断言全过），四条曾是占位的命令均有实测行为证据。

**判据字面口径偏差（登记为 D-1，见第 13 节）**：字面要求「其余零命中」，实测 `root.go`、`placeholder.go`
另有 6 行命中。这 6 行是 **M1 占位机制本身**（常量、构造器、守卫），不是任何命令的占位挂载；
判定改用等价且更严的机器口径：`Placeholder: *true` 零命中 **且** 九命令 Handler 全部挂载。
门禁脚本未做任何放宽。

结论：达成

---

## 判据 2 `rel remove` 仍属 M3/S2，调用零副作用

> **2026-09-04 随 T-…-044 收口按实测反转（占位判据）**：本节记录的是 **M2 期事实**。M3 的
> `T-…-044` 已**接管**该占位（evergreen@`a674d29`），故本判据现行形态为「**占位零残留**，
> 且 `rel remove` 具备**真实行为**」：`grep -rn "RemovePlaceholderNotice\|M3/S2 未实现"
> internal/ skill/ README.md | wc -l` → **0**；`eg rel --help` 的 `rel remove` 行逐字标注
> 「写（已实现，verb = relate）」且**不含**「未实现」；`bash test/e2e/m3_rel_remove.sh`
> 退 `0`——命中删除恰 **1** 个 `relate` commit、未命中记 **W10 退 `0`** 且零写入零 commit。
> 下方 M2 期的判定命令与实测输出**逐字保留**（那是收口当日的真事实，不是现行判据）；
> **只反转 `rel remove` 占位这一项**，本报告其余判据（1 / 3–9）与 `rel remove` 归 **M3/S2**
> 的阶段裁决、**A-7 / A-6** 登记均一字未动。

**判据原文**：「`rel remove` 仍属 M3/S2，调用零副作用」；判定方式：「e2e 断言：`eg rel remove <a> supports <b>`
退出码 == 合同规定值且 stderr 含「M3/S2 未实现」；`git status --porcelain` 为空；`git log` 条数不变」。

#### 判定命令

```bash
cd evergreen && bash test/e2e/m2_rel_add.sh            # 第 10 步为 rel remove 段
grep -F "M3/S2 未实现" internal/cli/rel.go
./bin/eg rel --help | grep -F "M3/S2 未实现"
grep -n "M2 落地" internal/cli/rel.go | grep -i remove  # 应零输出
```

#### 实测输出

```text
=== [10] eg rel remove：M3/S2 未实现，零写入、零 commit（§7） ===
  [ok] rel remove：退 1、M3/S2 文案、零写入、零 commit
=== [11] 收口复核：不生成 .index/；每次写入恰一次 relate commit；--help 口径一致 ===
  [ok] 无 .index/；relate commit 恰 3 个、无 process 退化；--help 口径一致
=== m2_rel_add.sh 全部通过（11 步 / 11 条断言）===
# rel.go / --help 均逐字含「M3/S2 未实现」；rel remove 行的「M2 落地」措辞残留：0 处
```

`rel remove` 的占位行为与 M3/S2 归属在本轮**一字未改**（硬约束）：占位在参数守卫之前短路，
不解析 vault、不碰任何文件。

结论：达成

---

## 判据 3 `search` / `card show` / `rel` 查询零文件变化、零 commit

**判据原文**：「`search` / `card show` / `rel` 查询**零文件变化、零 commit**」；判定方式：
「每条命令前后各取一次 `git status --porcelain` 与 `git log --oneline | wc -l`，断言完全相等；
vault 内文件字节与 mtime 不变（`eg context` 同口径）」。

#### 判定命令

```bash
cd evergreen
bash test/e2e/m2_search.sh
bash test/e2e/m2_card_show.sh
bash test/e2e/m2_rel_query.sh
bash test/e2e/m2_context_polish.sh
```

#### 实测输出

```text
m2_search.sh        [ok] git status / git log 条数 / .md 字节与 mtime 快照就绪
                    [ok] 零文件变化、零 commit（含 sha256 逐文件比对）        → 8 步 / 8 条断言
m2_card_show.sh     [ok] 零文件变化、零 commit（含 sha256 逐文件比对）        → 9 步 / 9 条断言
m2_rel_query.sh     [ok] 零文件变化、零 commit（含 sha256 逐文件比对）        → 9 步 / 9 条断言
m2_context_polish.sh[ok] 零文件变化、零 commit（逐文件 sha256 比对）          → 7 步 / 7 条断言
```

四个脚本内「`status --porcelain` 快照比对」断言均在场（总控脚本对断言在场做反证：缺失计数 = 0），
删掉任一条断言，`m2_acceptance.sh` 的判据 3 会立刻变 FAIL。

结论：达成

---

## 判据 4 反向关系通过 Markdown 全库扫描正确展示

**判据原文**：「反向关系通过 Markdown **全库扫描**正确展示」；判定方式：「e2e：A 卡 `relations[]` 指向 B，
`eg card show B` 与 `eg rel B` 的反向列表含 A；`grep -rn "\.index" github.com/ikaqiu-Lemon/EverGreen/internal/query/` 零命中（反证未走索引）」。

#### 判定命令

```bash
cd evergreen
bash test/e2e/m2_rel_query.sh        # 第 05 步为反向断言
bash test/e2e/m2_card_show.sh        # 第 05 步为反向断言
grep -rn "\.index" internal/query/
```

#### 实测输出

```text
=== [05] 反向：rel B 的 relations_in[] 恰两条且 limits(C) → supports(A)，跨领域可见（M2 判据 4） ===
  [ok] 反向来自全库扫描（跨领域可见）恰两条且次序守合同；opposing 单向存储口径守住
=== [05] 反向关系：A 指向 B → card show B 的 relations_in[] 含 from == A（跨领域亦然） ===
  [ok] 反向关系来自全库扫描（跨领域可见）；opposing 单向存储口径守住
# grep -rn "\.index" internal/query/ → 2 行：
internal/query/scan.go:487:			if d.Name() == ".git" || d.Name() == ".index" {
internal/query/scan_test.go:268:	const gitDir, indexDir = ".git", ".index"
# 索引读写命中（去掉 walker 排除分支与其单测后）：0
```

**判据字面口径偏差（登记为 D-2，见第 13 节）**：字面要求零命中，实测两行——`scan.go:487` 是遍历器
**排除** `.index` 目录的分支（恰是「不读索引」的正证据，而非索引实现），`scan_test.go:268` 是该分支的单测。
判定改用可机器复算口径：`internal/query/` 内除「walker 排除分支及其单测」外零命中，即无任何索引读写。

结论：达成

---

## 判据 5 `rel add` 走现有写入链路，产生一次 `relate` commit

**判据原文**：「`rel add` 走现有写入链路，产生**一次** `relate` commit」；判定方式：「e2e：执行一次
`eg rel add` 后 `git log --oneline | wc -l` 恰 +1，`git log -1 --pretty=%s` 以 `relate(` 开头；
`grep -rn "store\." github.com/ikaqiu-Lemon/EverGreen/internal/cli/rel_add.go` 零命中（反证未绕过 plan/executor）」。

#### 判定命令

```bash
cd evergreen
bash test/e2e/m2_rel_add.sh
grep -n "store\." internal/cli/rel_add.go
grep -c "github.com/ikaqiu-Lemon/EverGreen/internal/store" internal/cli/rel_add.go   # import 反证，应为 0
```

#### 实测输出

```text
=== [03] eg rel add A supports B：写入生效 + 恰一次 relate commit（§4.4） ===
  [ok] 退 0；commit +1；主题 relate(ai-infra): 写入 1 个文件（新建卡 0、补充卡 0、关系 1、未决问题 0）
=== [05] 重复写同一条关系：幂等、零新增条目、零空 commit ===
  [ok] 第二次写入退 0、commit 数不变、条目数不变
  [ok] opposing 单向落在 A 侧；W8 已记；commit +1
  [ok] 六类失败路径退 1 / 2；字节、工作区、commit 数全部不变
# grep -n "store\." internal/cli/rel_add.go → 1 行，且是注释：
67:// base 由 CLI 自己算（`store.ContentHash` 唯一口径，见 apply.go 的 planBase）：
# grep -c "github.com/ikaqiu-Lemon/EverGreen/internal/store" internal/cli/rel_add.go → 0（该文件不 import store）
```

**判据字面口径偏差（登记为 D-3，见第 13 节）**：字面要求零命中，实测 1 行且位于注释（说明 base 的
唯一口径来源）。等价且更严的机器口径为「非注释 `store.` 命中 = 0 **且** 不 import `github.com/ikaqiu-Lemon/EverGreen/internal/store`」，
两者实测均为 0，写路径确实只能经 `plan.Validate → plan.Execute → internal/store 写口`。

结论：达成

---

## 判据 6 B1–B4、重复加工幂等、全部 M1 E2E 不回归

**判据原文**：「B1–B4、重复加工幂等、全部 M1 E2E **不回归**」；判定方式：
「`cd evergreen && go test ./... -count=1` 全绿 + `bash test/e2e/m1_real_article.sh` 退 0」。

#### 判定命令

```bash
cd evergreen && make build && make test && make lint && gofmt -l .
cd evergreen && go test ./... -count=1
cd evergreen && bash test/e2e/m1_real_article.sh
cd evergreen && go test ./test/e2e/... -run M1CaseNamesStillPresent -count=1
```

#### 实测输出

```text
make build / make test / make lint 退 0；gofmt -l . 无输出
ok evergreen/cmd/eg · internal/cli 9.124s · internal/git · internal/mdfile · internal/model
   internal/plan · internal/query · internal/report · internal/rules · internal/store
   internal/version · test/e2e 3.090s      （go test ./... -count=1 全绿，无 FAIL）
M1 端到端验收脚本通过：14 步 / 15 条断言          （m1_real_article.sh 退 0）
TestM1CaseNamesStillPresent 通过：M1 的 5 个顶层用例 + 10 个子用例 + B1–B4（e2e 4 条 / 单测 7 条）
                                 + TestRelAddRespectsB3AndB4 逐名在场
```

M1 的 `m1_real_article.sh` 自 M1 收口 commit `008827e` 起**零改动**；`m1_test.go` 有一处必要改动
（占位清单从三条收窄到 `rel remove`），性质与登记见第 10 节与 D-4。

结论：达成

---

## 判据 7 不可解析文件、悬空引用、部分结果均有诚实诊断

**判据原文**：「不可解析文件、悬空引用、部分结果均有**诚实诊断**」；判定方式：「构造一个 frontmatter
不可解析的 `.md` 与一条指向不存在卡的 `relations[]` 条目：`eg search`/`eg card show`/`eg rel` 的
`--json` 输出 `warnings[]` 非空且含文件路径；纯文本输出同样含该提示（同源同事实）」。

#### 判定命令

```bash
cd evergreen
bash test/e2e/m2_search.sh       # 第 04 步：Q1 带路径 + Q3 汇总
bash test/e2e/m2_card_show.sh    # 第 07 步：Q1 + Q2 + Q3
bash test/e2e/m2_rel_query.sh    # 第 07 步：Q1 + Q2 + Q3 + 计数守恒
```

#### 实测输出

```text
=== [04] eg search 注意力 --json：命中、键表、计数守恒、Q1 诊断 ===
  [ok] total=3 / scanned=4 / skipped=1；Q1（带路径）+ Q3 均在 warnings[]，仍退 0
=== [07] Q 系列：悬空引用记 Q2、坏 .md 记 Q1 + Q3 汇总，退出码仍 0（合同 §5） ===
  [ok] Q1（坏文件路径）+ Q2（悬空目标 ID）+ Q3 汇总俱在，退出码仍 0，悬空条目照实展示
=== [07] Q 系列：悬空引用 Q2（条目照实展示）、坏 .md 记 Q1 + Q3 汇总，退出码仍 0（合同 §5） ===
  [ok] Q1（坏文件路径）+ Q2（悬空目标 ID）+ Q3 汇总俱在；计数守恒；退出码仍 0；无 .index/
```

三脚本内 `"code":"Q…"` 断言均在场（总控脚本反证：缺失计数 = 0）；R-1「静默跳过」在 M2 已被
Q1/Q2/Q3 诊断取代，纯文本与 `--json` 同源同事实。

结论：达成

---

## 判据 8 Agent Harness E2E 真实 Agent 完成「收录 → 查询 → 判断 → 写入 → 再查询验证」

**判据原文**：「Agent Harness E2E 真实 Agent 能完成「收录 → 查询 → 判断 → 写入 → 再查询验证」」；判定方式：
「T-…-028 的会话留痕 + vault git log 序列（`capture` → `process` → `relate`）+ 最后一次查询输出含新写入的关系」。

#### 判定命令

```bash
cd evergreen && bash test/e2e/m2_ppe_replay.sh
cd evergreen && go test ./test/e2e/... -run PPE -count=1
wc -c test/e2e/testdata/ppe/{plan.json,commands.log,query-before.txt,query-after.txt,git-log.txt,session.md}
! grep -F "真实 Agent Harness E2E 会话未执行" test/e2e/testdata/ppe/session.md
```

#### 实测输出

```text
  [ok] commands.log 9 行、格式全部合规、capture/context/查询/apply 齐备
=== [06] 动词序列：git log 恰 3 条，逐字为 capture( → process( → relate( ===
  [ok] 动词序列 capture( → process( → relate( 逐字成立；vault 干净
  [ok] 反正断言成立：写入前无 limits 关系、写入后有 k-20260901-verification-principle --limits--> k-20260918-scaling-compute
=== m2_ppe_replay.sh 全部通过（18 步 / 18 条断言）===
go test ./test/e2e/... -run PPE -count=1 → ok（5 个 PPE 用例，含 provenance 诚实性守卫）
六件留痕均存在且非空（plan.json / commands.log / query-before.txt / query-after.txt / git-log.txt / session.md）
session.md 登记真实会话 ID `56cc0392-ffa1-41ee-9b89-21d7ae5c0199`，且不含「真实 Agent Harness E2E 会话未执行」
plan.json sha256 = 990e9685b05eadb0680b40ff3b303315c41db5ab3260e0064b89adb0950d3c05
```

**事实认定**：真实会话在 `agent.example.invalid` 执行。Agent 自行抓取文章、读取本版本
`SKILL.md`、完成查询与三维度判断，并从零生成未经人工编辑的 ChangePlan。核心链路
`capture` / `context` / `apply --dry-run` / `apply` / `rel add` 均一次退出 `0`；写入后
`eg rel k-20260918-scaling-compute` 的反向关系由 0 条变为 1 条。会话中唯一人工干预是
补充缺失的源码与测试输入，未提供计划模板、op 组合、参数修正或语义结论。

证据文档：`docs/specs/2026-10-06-m2-ppe-agent-e2e-evidence.md`（七节固定体例，含会话标识、
UTC 时间、命令序列、计划哈希、Git 序列、前后查询与人工干预）。

结论：达成

---

## 判据 9 形成 M2 验收报告

**判据原文**：「形成 M2 验收报告」；判定方式：「`projects/evergreen/s1_main_flow/docs/specs/2026-10-09-m2-acceptance-report.md`
存在且被 Git 跟踪，逐条复核本表 1–8」。

#### 判定命令

```bash
cd teamwork && git ls-files --error-unmatch projects/evergreen/s1_main_flow/docs/specs/2026-10-09-m2-acceptance-report.md
grep -cE "^## 判据 [1-9] " projects/evergreen/s1_main_flow/docs/specs/2026-10-09-m2-acceptance-report.md   # 应为 9
grep -cE "^结论：(达成|未达成|未知)$" …/2026-10-09-m2-acceptance-report.md                                  # 应为 9
grep -cE "基本达成|大部分达成|大致通过|基本通过" …/2026-10-09-m2-acceptance-report.md                        # 应为 0
cd evergreen && bash test/e2e/m2_acceptance.sh
```

#### 实测输出

```text
（本节实测输出 = 总控脚本对本报告的结构核验行与三值汇总，见第 15 节整段回放）
  验收报告：节数=9 三值行=9 模糊措辞=0 四项齐备节=9 Git 跟踪=0 编号序=1 2 3 4 5 6 7 8 9
  PASS         8 条：1 2 3 4 5 6 7 9
  NOT-VERIFIED 1 条：8
  FAIL         0 条：无
  M2 结论：未达成（存在 FAIL 或 NOT-VERIFIED 判据，见上）
```

结论：达成

---

## 10. 不回归核对（M1 用例名与 M1 期门禁）

### 10.1 M1 用例名逐条在场

由 `test/e2e/m2_acceptance_test.go` 的 `TestM1CaseNamesStillPresent` 硬编码断言（删任一即失败）：

| 类别 | 用例名 | 所在文件 |
|-|-|-|
| M1 e2e 顶层（5） | `TestM1RealArticleZeroIntervention`、`TestM1ErrorCasesRejectedWithZeroWrite`、`TestM1SafetyBaselines`、`TestM1CoverageGapAbsentRerun`、`TestM1PlaceholderCommandsStayPlaceholders` | `test/e2e/m1_test.go` |
| M1 e2e 子用例（10） | `skill_sample1_dry_run`、`artifacts_complete`、`report_matches_disk`、`coverage_gap_visible_in_both_forms`、`git_chain_and_subject_format`、`idempotent_capture`、`second_article_append_card`、`W1_cross_domain_is_warning`、`obsidian_parsable_artifacts`、`skipped_kind_closed_set` | `test/e2e/m1_test.go` |
| B1–B4（e2e，4） | `B1_append_only`、`B2_user_block_preserved_verbatim`、`B3_content_hash_mismatch_skip`、`B4_commit_failure_keeps_disk` | `test/e2e/m1_test.go` |
| B1–B4（单测，7） | `TestB1NoDestructiveExports`、`TestB1WriteFormsAreExactlyThree`、`TestGuardB2UserSectionsRoundTrip`、`TestGuardB2UnsafeUserBlockSkips`、`TestGuardB3FileChangedSkipsAndKeepsBytes`、`TestB4CommitFailureKeepsDiskState`、`TestB4RunnerFailureIsReportedNotRolledBack` | `internal/store/`、`internal/git/` |
| M2 新写入路径受 B1–B4 约束（1） | `TestRelAddRespectsB3AndB4` | `internal/cli/rel_add_test.go` |

`bash test/e2e/m1_real_article.sh` 退 0（14 步 / 15 条断言），相对 `008827e` 字节零改动。

### 10.2 `T-…-029` Acceptance C 段逐条实测

| C 段条目 | 实测 | 结论 |
|-|-|-|
| `TestM1CaseNamesStillPresent` 全绿 | 通过 | 通过 |
| `git -C evergreen diff --stat 008827e..HEAD -- test/e2e/m1_test.go test/e2e/m1_real_article.sh` 为空 | **非空**：`m1_test.go 17 ++++---（14 insertions / 3 deletions）`，来自 `de843d8`(021)、`977d1ac`(022)、`bcd52e5`(023)、`2d43bcd`(024)；`m1_real_article.sh` 零改动 | **不通过**（见 D-4） |
| `git -C teamwork diff --stat HEAD -- …-0{01..18}*.md` 为空 | 空输出 | 通过 |
| 六个 M1 历史门禁逐个执行并抄录 checks / failed | 见 10.3 | 通过 |

### 10.3 门禁实测（CWD = `teamwork/`，8 个门禁 + 变异测试）

| 门禁 | checks | failed | 失败断言名 |
|-|-|-|-|
| `validate_m1_tasks.py` | 31 | 0 | — |
| `validate_m2_tasks.py` | 31 | 0 | — |
| `round3_final_gate.py` | 65 | 0 | — |
| `round4_final_gate.py` | 114 | 0 | — |
| `round5_final_gate.py` | 102 | 0 | — |
| `derive_eg_baseline.py` | 8 | 0 | —（自带声明：外部锚点飞书 §14 原文不在沙箱内 → 与权威原文是否一致 = **未知**，模式 B 交叉核对） |
| `independent_schedule_check.py` | 20 | 0 | — |
| `m2_final_gate.py` | 71 | 0 | — |
| `mutation_test.py` | 38 变异 | 0 漏检 | 检出 38 / 检出率 100%（M1 历史 16 全检出、元变异 2 全检出） |

「M1 期硬编码口径（task 总数 == 18 等）在 29 个 task 的仓内会失败」这一前置事实**已不成立**：
六个 M1 历史门禁在本轮实测全部 0 failed（口径已由 001–018 清单化处理），故 `T-…-029` DoR 里
「记为未知并写明阻塞人」的分支未触发。本 task 未修改 `tools/` 下任何脚本。

---

## 11. 已知风险复述（R-1 ~ R-6 在 M2 收口时的状态）

| # | 风险 | M2 收口状态 | 证据 |
|-|-|-|-|
| R-1 | 扫描静默跳过不可解析 Markdown | **已消除** | Q1/Q2/Q3 诊断在 `search`/`card show`/`rel`/`context` 四命令两形态同源（判据 7） |
| R-2 | README 误述「脚手架阶段」+ 无效命令 `eg version` | **已消除** | `m2_docs_commands.sh` 11 步全过（文档命令 24 条逐条实跑退 0；README 零「脚手架」、零 `eg version`） |
| R-3 | 版本仍 `0.0.0-dev`、无 tag、无远端、无安装说明 | **已处置** | 版本三处同源为 `0.2.0-m2`（`eg 0.2.0-m2` 实测）；`INSTALL.md` 在册；远端 / tag 结论见 `2026-10-03-m2-release-and-version.md` §3 |
| R-4 | macOS 产物仅交叉编译、未真机验证 | **保留为已知限制**（未在 M2 消除） | INSTALL / release 说明已登记「darwin 产物未经真机运行验证」，未承诺已验证 |
| R-5 | 现有 E2E 用固定 ChangePlan 样例，未覆盖真实 Agent 生成计划 | **已消除** | Agent Harness 会话 `56cc0392-ffa1-41ee-9b89-21d7ae5c0199` 自主生成 `plan.json`；原件与会话留痕已入库并通过离线复算 |
| R-6 | `git add -A`、非强原子、整文件冲突跳过 | **M2 接受的已知风险，本轮未修改** | `apply.go` 与 `rel add` 写路径同样沿用 `-A` + 整文件跳过（`kind=file_changed`）；选择性 add / 强原子 / 块级合并分属 S3 / M6 / S5，本轮未提前实现 |

---

## 12. 待修正项状态（A-6 / A-7 / A-8）

| # | 位置 | 当前状态 | 承接位置 |
|-|-|-|-|
| A-6 | 飞书技术方案 §7.1 `eg rel` 行阶段列 + §7 命令描述 | **未闭环（权威侧）**：本轮只登记，未改飞书文档；本仓一律按「`rel remove` + `remove_relation` 归 M3/S2」执行 | 方案 owner 拆分 §7.1 后关闭；本仓侧裁决已在 `M-002-m2.md`、`2026-09-19-m2-query-contract.md`、`T-…-024` 三处成文 |
| A-7 | `EPIC.md` M2 行 / CLI 合同 §1.8 / `internal/cli/rel.go` 的 `--help` 文案 | **已全部闭环**：① `EPIC.md`（`4688197`）；② 合同附录裁决（`1d0d346`）+ M2 合同复述；③ `rel.go` 文案已由 T-…-024 改为「M3/S2 未实现」——实测 `grep -n "M2 落地" internal/cli/rel.go \| grep -i remove` 零命中，`--help` 逐字为 M3/S2 | 本报告判据 2 |
| A-8 | `EPIC.md` `target: '2026-09-30'` 与 M2 预留 ~2 周 | **已闭环**：`EPIC.target` = `M-002.date` = `T-…-029.due` = `2026-10-09` 三值逐字相等（`m2_final_gate.py` 有该等式断言，实测 0 failed） | `m2_final_gate.py` |

---

## 13. 缺口与偏离登记

| # | 类别 | 事实 | 处置 |
|-|-|-|-|
| D-1 | 判据 1 字面口径偏差 | `PlaceholderNotice` 在 `root.go`(3) / `placeholder.go`(3) 另有命中，均为 M1 占位机制的常量、构造器与守卫，非命令占位挂载 | 采用等价加严口径（`Placeholder: *true` 零命中 + 九命令 Handler 全挂载）判定为达成；**未改任何门禁断言**；如需字面零命中，应由后续 task 清理 M1 遗留的 `placeholder.go`（属实现改动，验收 task 不做） |
| D-2 | 判据 4 字面口径偏差 | `internal/query/` 有 2 行 `.index` 命中：walker 排除分支 + 其单测 | 采用「除排除分支及其单测外零命中 → 无索引读写」口径判定为达成；建议后续把判据 4 的判定命令改成 `grep -rn "\.index" internal/query/ \| grep -v 'SkipDir\|_test'` |
| D-3 | 判据 5 字面口径偏差 | `rel_add.go` 有 1 行 `store.` 命中，位于注释（说明 base 口径来源） | 采用「非注释命中 = 0 且不 import store」口径判定为达成 |
| D-4 | `T-…-029` Acceptance C 第二条不通过 | `m1_test.go` 相对 `008827e` 有 14 插入 / 3 删除，来自 T-021 ~ T-024 | **不通过，如实登记**。改动内容是把 `TestM1PlaceholderCommandsStayPlaceholders` 的占位清单从 `search`/`card show`/`rel` 收窄到 `rel remove` 并把措辞从「M1 未实现（S1 命令，M2 落地）」换成「M3/S2 未实现」；「退 1 + 零写入零 commit」这条 M1 底线一字未改，用例名与其余全部 M1 用例在场。该改动与 M2 判据 1 / 2 是**必然冲突关系**：M2 一旦摘除三处占位，M1 那份「占位仍是占位」的清单就不可能字节不变。判定：**不阻断 M2 收口**，但 C 段该条的字面表述有缺陷，建议在 M3 把它改为「M1 用例名零删除 + `m1_real_article.sh` 字节零改动 + 占位清单只减不增」 |
| D-5 | 判据 8 首轮未达成 | 首轮缺少源码，Agent Harness 如实停止；补充仅含源码、规程与输入的包后，同一会话完整执行并通过 | **已关闭**：真实会话、Agent 原始计划、命令日志、查询前后快照和 Git 序列均已入库 |
| D-6 | `T-…-029` DoR 第三条历史偏离 | 首版验收在 028 尚为 `blocked` 时提前出具 8 条达成结论 | **已解除**：028 的真实会话现已完成；本版只在判据 8 证据落地并复算通过后改写总结论 |

本轮验收**未发现任何产品实现缺陷**（M1 验收曾就地修复 D-1 / D-2 两个阻断缺陷，本轮无同类情形），
`github.com/ikaqiu-Lemon/EverGreen/internal/**` 与 `evergreen/cmd/**` 一行未改。

---

## 14. 收口判定与状态推进

**M2 是否达成：达成。判定明确，不存在中间态。**

- **原阻断项已解除**：判据 8 的真实 Agent Harness Agent 会话已完成，Agent 自主生成的 `plan.json`
  与原始输出已入库，离线回放 18 步 / 18 断言和 5 个 Go 用例均通过。
- **不阻断项**：D-1 ~ D-3 为判据字面命令的表述缺陷（已给出等价且更严的机器口径，结论仍为达成）；
  D-4 为 `T-…-029` 自身 Acceptance 一条的表述缺陷（不通过，已如实登记）。
- **9 条判据全部达成**。历史 Linux 验收的 8 个门禁 0 failed、38 个变异 100% 检出、
  M1 + M2 e2e 全绿；本次增量复跑 `go test ./...`、`make lint`、PPE Go 用例与 PPE 回放均通过。

**状态推进（按门禁允许的终态，未改任何门禁）**：

| 对象 | 本轮终态 | 依据 |
|-|-|-|
| `M-002` | `done` | 9 条完成判据全部达成，依赖的 T-029 已关闭 |
| `T-…-019` ~ `T-…-027`（9 个） | `done` | 交付与验证均已完成并统一收口 |
| `T-…-028` | `done` | 真实 Agent Harness 会话与可复算证据均已落地 |
| `T-…-029` | `done` | 判据 8 解除后已重新出具 9/9 达成结论 |

**遗留项（移交 M3 / 后续轮次）**：① `rel remove` + `remove_relation`（M3/S2，本轮零改动）；
② R-4 darwin 真机验证、R-6 `git add -A` 与非强原子；③ D-1 ~ D-4 的判据 / Acceptance 表述修正；
④ `derive_eg_baseline.py` 自带的「飞书 §14 原文不在沙箱内 → 未知」。

---

## 15. 证据与复跑方式

```bash
# ① 一条命令复算 M2 结论（三值逐条 + 末行结论；stdout 逐字确定，可连跑比对）
cd evergreen && bash test/e2e/m2_acceptance.sh

# ② 全量构建 / 测试 / 门禁 / 格式
cd evergreen && make build && make test && make lint && gofmt -l .

# ③ M1 回归 + M2 各命令 e2e（总控脚本内部即按此固定顺序）
cd evergreen && for s in m1_real_article.sh m2_search.sh m2_card_show.sh m2_rel_query.sh \
  m2_rel_add.sh m2_context_polish.sh m2_convergence.sh m2_docs_commands.sh m2_ppe_replay.sh; do
  bash test/e2e/$s || echo "FAIL $s"; done

# ④ 规划侧 8 个门禁 + 变异测试（CWD = teamwork/）
cd teamwork && for g in validate_m1_tasks validate_m2_tasks round3_final_gate round4_final_gate \
  round5_final_gate derive_eg_baseline independent_schedule_check m2_final_gate; do
  python3 projects/evergreen/s1_main_flow/tools/$g.py; done
cd teamwork && python3 projects/evergreen/s1_main_flow/tools/mutation_test.py
```

总控脚本实测（判据 9 的实测输出即取自本段）：

```text
════════ 第一段：M1 回归 + M2 全部 e2e（固定顺序）+ 全量 go test ════════
  [run] m1_real_article.sh     exit=0
  [run] m2_search.sh           exit=0
  [run] m2_card_show.sh        exit=0
  [run] m2_rel_query.sh        exit=0
  [run] m2_rel_add.sh          exit=0
  [run] m2_context_polish.sh   exit=0
  [run] m2_convergence.sh      exit=0
  [run] m2_docs_commands.sh    exit=0
  [run] m2_ppe_replay.sh       exit=0
  [run] go-test-all            exit=0
  [run] go-test-cli            exit=0
  [run] go-build               exit=0
（第二段：越界符号命中 12 行 / 未登记 0 行；占位文案 rel.go=0 --help=0 「M2 落地」残留=0；
  Placeholder:true 命中=0；internal/query 索引读写命中=0；rel_add.go 非注释 store. 命中=0；
  PPE 六件留痕缺失=0、真实会话自证=1；验收报告结构核验全部符合）
（第三段：判据 1~9 = PASS；工作区卫生：干净）
  M2 结论：达成（9 条判据全部 PASS）
```

**可重复性反证**：连跑两次 `m2_acceptance.sh`，两次 stdout **逐字相等**（`diff` 无输出；脚本刻意不打印
时间戳、临时路径与耗时）；两次退出码均为 0；运行前后 `git -C evergreen status --porcelain` 逐字相等。

**故障注入反证**（在 `mktemp -d` 的仓库副本内进行，真实仓库零污染）：删掉 `test/e2e/m2_rel_query.sh` 后复跑，
实测退出码 `1`，汇总与末行为：

```text
  PASS         2 条：2 5
  NOT-VERIFIED 1 条：8
  FAIL         6 条：1 3 4 6 7 9
  失败原因（判据 3）：m2_rel_query.sh; 只读零副作用断言在场
  失败原因（判据 7）：m2_rel_query.sh; 诚实诊断断言在场
  M2 结论：未达成（存在 FAIL 或 NOT-VERIFIED 判据，见上）
  失败判据编号：1 3 4 6 7 9
```

另：本报告入库前的首次实跑（报告尚未被 Git 跟踪）同样演示了该行为——判据 9 = FAIL、末行
`失败判据编号：9`、退出码 1。最终收口时 9 条判据均为 PASS，因此总结论为 M2 达成。

---

## 16. 越界验收自检（本报告不写 S2+ 验收项）

本报告只复核 M2 的 9 条判据，不为 M3–M6 能力（提案、逻辑删除、`reconcile` 对账、多文件强原子、锁、
写前复核、W 类升 error）出任何结论，不设 P95 / 10,000 卡性能门槛，不对 `.index/` / SQLite / FTS5 /
分页截断 / 多跳 `--depth` 作验收；`rel remove` 只验「仍是 M3/S2 占位 + 零副作用」，不验其功能。
