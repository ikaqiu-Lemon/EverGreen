# M5（S4 · 检索与性能）验收报告

- **里程碑**：`M-005`（S4 · 检索与性能）｜**收口 task**：`T-evergreen.s1_main_flow-158614-069`
- **收口日**：2027-01-17（== `M-005.date` == `T-…-069.due`）
- **版本**：`0.5.0-m5`｜**命令数**：22｜**e2e 磁盘总数**：50（M1–M4 的 39 + M5 新增 11）
- **上游依据**：`milestones/M-005-m5.md`（18 条完成判据 / 11 条未决 / R-21 ~ R-27）、
  `docs/specs/2026-12-19-m5-index-architecture-contract.md`（M5 唯一施工合同）、
  `docs/specs/2027-01-17-m5-release-and-version.md`（版本与发布口径）、
  `docs/specs/2026-12-12-m4-acceptance-report.md`（M4 事实基线与延期债）

## 0. 本文件的阶段状态（**必须先读**）

本报告采用**两阶段落盘**：

| 阶段 | 内容 | 状态 |
|-|-|-|
| **阶段 A**（本次） | 骨架落盘：18 条判据的判定命令逐条在册、bench 双列表、A-41 ~ A-51 终态、J14 / J15 / I2 三行终态表、口径漂移登记表（`D-M5-*`）、M1–M4 历史 e2e 逐条清偿留痕表、`T069-DEBT-*` / `T069-FLAKE-*` 终态。**结论列一律为 `NOT-VERIFIED`** | 已落盘 |
| **阶段 B**（2026-09-09 收口） | 实测回填：50 条 e2e 全量真跑（`EXIT=0`）、`make lint && go test ./... -count=1` 全绿、11 个历史门禁逐个 0 failed 且 checks 不降、`m3_final_gate.py` 恰 135 / 3、`m4_mutation_test.py` 171 算子 100% 检出、`validate_m56_tasks.py` 0 failed、`m5_mutation_test.py` 0 存活、`make dist` 四平台重建 + 静态反证，18 行结论已改为 `PASS`，`R3` 转绿 | 已回填 |

**阶段 A 纠正轮（owner 否决后重做，本轮）**：阶段 A 的第一版曾把「M1–M4 的 39 个既有 e2e 全绿」
改判为**冻结红项登记 + 失败签名钉死**（`m5_acceptance.sh` 的 `FROZEN_RED` / `FROZEN_SIG` / `FROZEN_WHY`，
并在 §5 / §6 各留一处登记）。该口径被 owner 判定为**放宽历史判据**并整体否决：
「把红项定义为通过」不是清偿。本轮已**彻底删除**该机制（脚本侧、门禁侧、变异算子侧、本报告侧四处同删），
改为**逐条修脚本判据直到真实 exit 0**：删掉名单后复跑又暴露 3 条名单外红项（`m4_r7_support` / `m4_visibility_matrix` / `m4_acceptance`），一并清偿；39 个历史 e2e 现逐条实跑退 0（共修 12 条，留痕见 §6），
`m3_final_gate.py` 由 135 checks / 9 failed 收敛到 **135 checks / 3 failed**（仅剩 J15 原三项，见 §5）。
本报告不保留任何「冻结红」「失败签名豁免」口径，也不复用被撤销的漂移编号。

**为什么允许 `NOT-VERIFIED` 存在**：`T-…-069` 的 Acceptance 要求结论行「恰 18 条且每条 ∈ {`PASS`, `FAIL`}」。
阶段 A 尚未跑全量证据，若此刻写 `PASS` 就是伪造。因此 `m5_final_gate.py` 设了**一条专门的 P1 判据**
（`R3`：`NOT-VERIFIED` 计数必须为 `0`），它在阶段 A **必红且必须必红**——红灯即「本报告尚未收口」的机器信号，
不允许用「暂时跳过」「阶段 A 豁免」之类手段消掉。收口以该判据转绿为准。

**阶段 B 已收口（2026-09-09）**：全部证据项实跑取证后，18 行结论由 `NOT-VERIFIED` 改为 `PASS`（无一条 `FAIL`），`R3` 随之转绿。本报告至此不再有 `NOT-VERIFIED` 行；证据逐项见 §1 现况列与 §8「收口留痕」。

## 1. 18 条完成判据

判定命令逐字取自 `milestones/M-005-m5.md` 完成判据表（本报告**不改写、不弱化**任何一条）。
`$E` = `evergreen` 仓根，`$T` = `teamwork` 仓根，`$D` = `projects/evergreen/s1_main_flow/docs/specs`。

| # | 判据 | 判定命令（摘要，全文见 M-005） | 责任 task | 阶段 A 现况（只记事实，非结论） | 结论 |
|-|-|-|-|-|-|
| 1 | A-41 ~ A-48 八项前置裁决已关闭 | `test -f $D/2026-12-19-m5-index-architecture-contract.md`；`^## ` ≥ 8；`A-4[1-8]` 登记行恰 8；三处关键口径逐字命中 | 064 | 合同在盘，§10 登记表 8 行，八项均「已关闭」 | PASS |
| 2 | 驱动选型先实证后定稿 | `grep -qF 'PRAGMA compile_options'`；`ENABLE_FTS5` 实测输出在册；逐字引用 `U-7` | 064 | 合同 §2.3 / §2.5 在册（54 条 compile_options，`fts5` 命中 23 次） | PASS |
| 3 | `internal/index` 包边界反证（依赖单向 / 零写 Markdown / 零 `os/exec`） | `head -1 internal/index/doc.go` 含 `[S4]`；`go test ./internal/index -run 'TestNoStoreDep\|TestNoExec'` | 065 | 包已落地，`doc.go` 首行在册 | PASS |
| 4 | Schema 封闭 + 版本常量 + 全量构建可复算 | `go test ./internal/index -run 'TestSchemaVersionConstant\|TestSchemaClosed\|TestBuildDeterministic'` | 065 | T-065 独立验收已过 | PASS |
| 5 | 四类损坏可检出 + `rebuild` 与全量构建等价 | `go test ./internal/index ./internal/cli -run 'TestCorrupt\|TestRebuild'`；`m5_index_corrupt_rebuild.sh` | 065 | 同上 | PASS |
| 6 | 增量索引只碰受影响文件 + 与全量重建等价 | `go test ./internal/index ./internal/plan -run 'TestIncremental'`；`m5_index_incremental.sh` | 066 | T-066 独立验收已过 | PASS |
| 7 | 水位线 + `W22` 检出 + `eg index sync` 收敛 | `go test ./internal/index ./internal/cli -run 'TestW22\|TestSync'`；`m5_index_consistency.sh` | 066 | 同上 | PASS |
| 8 | 索引可丢弃：`rm -rf .index` 后 `.data` 逐字相等 + 各多一条 `W23` | `m5_degrade_fallback.sh` | 067 | T-067 独立验收已过 | PASS |
| 9 | 三条读路径接索引且后端可切换、选择逻辑单点 | `go test ./internal/query -run 'TestBackendSelect'`；`m5_read_path_index.sh` | 067 | 同上 | PASS |
| 10 | `data` 键集合不因索引扩张（rel 恰五键 / `skipped[].kind` 恰 2 值） | `go test ./internal/cli -run 'TestRelDataFiveKeys\|TestSkippedKind'` | 067 | 同上 | PASS |
| 11 | 关系四级排序键固定 + 同键稳定序 | `go test ./internal/query ./internal/rules -run 'TestRelationSortFour'` | 068 | T-068 独立验收已过 | PASS |
| 12 | 分页参数封闭 + 截断 `W25` + 翻页无重无漏 | `go test ./internal/query ./internal/cli -run 'TestPageParamsClosedSet\|TestPaginate'`；`m5_sort_page.sh` | 068 | 同上（全局 limit，非 `2*limit`） | PASS |
| 13 | `replaced_by` 正反双向一致 + 与逻辑删除 / deprecated 正交 | `go test ./internal/query -run 'TestReplacedByReverse'`；`m5_replaced_by_reverse.sh` | 068 | 同上 | PASS |
| 14 | 10,000 卡语料 + `eg bench` + 五条门槛达标 | `go run ./test/perf/corpus_gen.go -cards 10000 …`；`eg bench --json` 五键各 ≤ 合同门槛；`m5_bench_p95.sh` | 068 | 实测值见 §2，五项均 < 门槛 | PASS |
| 15 | 计数变更面逐项复算（命令 20→22 / 新增诊断码恰 5 / `W21` 仍不分配） | `eg --help` 顶层恰 22；`W22\|W23\|W24\|W25\|Q5` 各在册；`grep -c '"W21"'` == 0 | 065–068 | `m5_docs_commands.sh` 已覆盖并退 0 | PASS |
| 16 | 版本 `0.5.0-m5` 多处同真 + dist 四平台重建 + 静态构建未破坏 | `grep -c '0.5.0-m5' internal/version/version.go` ≥ 1；`grep -c '0.4.0-m4' internal/version/version.go Makefile` == 0；`CGO_ENABLED=0` 四平台 | 069 | 版本四处已推进（`version.go` / `Makefile` / `SKILL.md` / README 逐字 `0.5.0-m5`），旧版本 `0.4.0-m4` 零残留；**dist 四平台已重建**（`make dist` rc=0，`dist/eg_*` 恰 4 个 + `SHA256SUMS`）；`file(1)` 反证 linux 两平台 `statically linked`、全量产物 `dynamically linked` 命中 0；本机产物 `--version` = `eg 0.5.0-m5 (commit db64ce7…)`，commit 段 == 当期 HEAD | PASS |
| 17 | M1–M4 不回归 + M5 门禁全绿 + e2e 只增不减 | `make lint && go test ./... -count=1`；`ls test/e2e/m5_*.sh` == 11；`ls test/e2e/*.sh` ≥ 50；11 个历史门禁脚本逐个 0 failed 且 checks 不降 | 069 | `make lint` 退 0、`go test ./... -count=1` **整包全绿**；m5_*.sh 已 11、总数已 50；11 个历史门禁脚本实测 **0 failed 且 checks 均不降**（逐项与 §8 第 4 项及 `audit/T069_HANDOFF.json` 的 `verification.history_gates_11` 逐字同序：`validate_m1_tasks` 35 / `validate_m2_tasks` 34 / `validate_m3_tasks` 47 / `validate_m4_tasks` 69 / `round3_final_gate` 65 / `round4_final_gate` 114 / `round5_final_gate` 102 / `m2_final_gate` 73 / `m4_final_gate` 35 / `independent_schedule_check` 22 / `derive_eg_baseline` 9 —— 恰 **11** 项计数，前提是先清 `__pycache__`，见 `D-M5-07`）；M1–M4 的 39 个历史 e2e **逐条实跑 exit=0**（曾红的 12 条已逐条修判据清偿，留痕见 §6，**零豁免 / 零冻结红**）；`m3_final_gate.py` 实测 **135 checks / 3 failed**，且失败集合逐字 == J15 原三项 `{H10k, H10x, H10ad}`（见 `D-M5-06`）；阶段 B 全量复跑 `m5_acceptance.sh` **`EXIT=0`**（50 个脚本逐个真跑全绿）；`m4_mutation_test.py` 171 算子 100% 检出 / 100% 命中登记判据（沙箱基线 68→69 的阶段化重钉见 §8 第 6 项，按 owner 口径**不占漂移编号**） | PASS |
| 18 | M4 延期债 J14 / J15 / I2 逐条给出终态 | 本报告 `^\|\s*(J14\|J15\|I2)\s*\|` 恰 3 行，终态列取值封闭 | 069 | 三行已在册（§4） | PASS |

**结论行计数**：18（`PASS` 18 / `FAIL` 0 / `NOT-VERIFIED` 0）。（阶段 B 实测回填；任何一条若未达成本应写 `FAIL`，本轮无 `FAIL`。）

## 2. `eg bench` 实测值与门槛值（双列）

数值源 = `M-005-m5.md`「A-46 数值回填」段 + 合同 §7.6 + `test/e2e/m5_bench_p95.sh` 的 `THRESH_*`，三处逐字同真。
门槛 = `ceil(实测 × 1.5 / 10) × 10`。

| 键 | 实测值（ms） | 门槛值（ms） | 复跑佐证（ms） | 是否达标 |
|-|-|-|-|-|
| `search_p95_ms` | `3198` | `4800` | `3254` | 达标 |
| `card_show_p95_ms` | `2408` | `3620` | `2450` | 达标 |
| `rel_p95_ms` | `2428` | `3650` | `2469` | 达标 |
| `index_build_ms` | `6035` | `9060` | `6076` | 达标 |
| `index_incremental_ms` | `9958` | `14940` | `9854` | 达标 |

采样口径（A-46 选项 ①，由 064 冻结）：语料 10,000 卡 / 预热 3 轮丢弃 + 采 50 轮 /
P95 = 第 48 小值不插值 / 端到端进程墙钟 / 固定 20 查询集。

## 3. A-41 ~ A-51 十一条未决事项终态（逐条复述，不得声称已解决而无留痕）

| 编号 | 终态 | 结论摘要 | 留痕 |
|-|-|-|-|
| A-41 | 已关闭 | 选 ①：`modernc.org/sqlite v1.45.0`（纯 Go）；禁 `mattn/go-sqlite3` | 合同 §2.4 / §10 |
| A-42 | 已关闭 | 选 ② `trigram` 主路 + ① `unicode61`+bigram 同表补路（恒开） | 合同 §2.6 / §10 |
| A-43 | 已关闭 | 选 ①：`.index/eg.db` 单文件、水位线入库内表、`.gitignore` 整目录忽略 | 合同 §3.1 / §10 |
| A-44 | 已关闭 | 选 ①：`(head, files_hash)` 水位线；`mtime` / `size` 仅快路径过滤 | 合同 §5.1 / §10 |
| A-45 | 已关闭 | 选 ① + ②：`Q5 index_degraded` + `W22` / `W23` / `W24` / `W25`；`Q` 计数「恰四条」→「恰五条（S4 起）」，只追加成员 | 合同 §6.2 / §6.3 / §10 |
| A-46 | 已关闭（口径 + 数值） | 口径由 064 冻结、数值由 068 实测回填，见 §2 | 合同 §7.3 / §7.6 |
| A-47 | 已关闭 | 选 ①：`--limit`（默认 50，`0` == 不限量）/ `--offset`（超界返回空且退 `0`） | 合同 §8.2 / §10 |
| A-48 | 已关闭 | 选 ①：`eg index build\|rebuild\|status\|sync` + `eg bench`，命令数 20 → 22 | 合同 §8.1 / §10 |
| A-49 | 登记，未关闭（沿用 A-40 选项 ③） | `EPIC.md` frontmatter 一字不改，M5 / M6 排期只写正文 | 合同 §12.1 |
| A-50 | 登记，**未知 / 待 owner 确认** | S4 那一条需求的 ID 与原文在沙箱内不可得；不自造 ID，暂沿用 `EG-VIEW-05` | 合同 §12.1 |
| A-51 | 登记，未关闭（本轮采用选项 ②） | 以本仓合同为 S4 唯一施工依据，ADR-08 / ADR-09 只作历史出处 | 合同 §12.1 |

## 4. M4 延期债终态（J14 / J15 / I2）

终态列**封闭取值**：`已清偿` / `已改判口径` / `继续延期（给出新到期里程碑）`。

| ID | 债务 | 终态 | 依据 / 可复跑命令 |
|-|-|-|-|
| J14 | `m2_acceptance.sh` / `m3_acceptance.sh` 历史 e2e 冻结事实退 1 | 已清偿 | 不是「改判口径」而是**真实修到退 0**：`m2_card_show.sh`（`b3bbb00`）、`m2_docs_commands.sh` / `m3_docs_commands.sh`（`9a90d8d`）、`m3_ops_diagnostics.sh` / `m3_proposal_layout.sh`（`a9a4eef`、`b878420`）、`m3_execution_failed.sh` / `m3_authorization.sh`（`13cd331`）、两个总控自身（`f86ab54`）逐条按**当期事实正面断言**重钉；39 个历史 e2e 现逐条 exit=0（§6）。复跑：`bash test/e2e/m2_acceptance.sh; echo $?` == 0、`bash test/e2e/m3_acceptance.sh; echo $?` == 0；阶段 B 由 `m5_acceptance.sh` 全量总控**一次性复算**：50 个脚本逐个真跑、逐条打印 `exit=0`，总控 `EXIT=0` |
| J15 | `m3_final_gate.py` 三条（H10k / H10x / H10ad） | 继续延期（新到期里程碑：M6 / `T-…-075`） | 阶段 A 首测为 135 checks / 9 failed；本轮已把新增六条（H10p / H10r / H10t / H10v / H10z / H10ab）按**阶段化双侧锁**清偿（teamwork `b13209d`），实测收敛为 **135 checks / 3 failed**，失败集合逐字 == J15 原三条，登记基线「恰 3 failed，不多不少」**已恢复成立**（详见 `D-M5-06`）。三条的根因是 M3 期把「e2e 总数 35」「T-043 取舍原文」「版本 `0.3.0-m3`」做成历史双侧锁，正确归零点是 M6 的历史门禁纳管重构；复跑命令：`python3 -B projects/evergreen/s1_main_flow/tools/m3_final_gate.py` |
| I2 | M4 合同 §14 称新增诊断码恰 14，实测实现 13（`I2` 从未落地） | 继续延期（新到期里程碑：M6 / `T-…-075`） | M5 未新增 `I` 域码，本轮**不替 owner 拍板**补 `I2` 或改合同计数；`grep -c '"I2"' github.com/ikaqiu-Lemon/EverGreen/internal/` 仍为 `0` |

J14 已在本轮**真实清偿**（39 个历史 e2e 逐条退 0，不靠豁免）；J15 / I2 仍为「继续延期」，
到期点**全部钉到 `T-…-075`**；按 `M-006` 判据 18，M6 之后「继续延期」不再是合法取值。

## 5. 口径漂移登记表（`D-M5-*`）——只登记，不洗白

**编号口径**：本表在册编号恰为 `D-M5-01 / 02 / 03 / 05 / 06 / 07` 六条，**04 号位刻意空缺且永不复用**，**且本集合已被 owner 锁定为冻结集合——不扩张、不新增编号**（阶段 B 期间出现的工具链基线演进只在 §8 阶段 B 证据与对应 task 的 Activity Log 留痕，一律**不占新的漂移编号**）。空缺原因：该号位原本登记的是「39 个历史 e2e 全绿改判为冻结红项 + 失败签名钉死」，该改判已被 owner 否决并整体删除（见 §0 纠正轮），因此它是**作废**而不是被重编号——其余五条一律**不下移补位**，正文与 §1 / §4 的引用也一律按上列编号。本报告全文不得再出现 04 号漂移编号，`m5_final_gate.py` 的 F8 对此做精确集合校验：在册 ID 集合逐字相等 + 该编号全文零命中，两侧同时成立才算绿。

| ID | 漂移事实 | 两侧口径 | 本报告处置 |
|-|-|-|-|
| `D-M5-01` | M1–M4 每个 task 的 Scope 首段都有「**所属阶段** / **技术方案章节** / **关联需求**」三要素行，M5 / M6 规划批次 12 个 task **实测 0 命中** | 体例侧要求三要素；本批次以 `design_doc` 键 + Scope bullet 承载同类信息 | 不回填（回填要动 5 个已 `done` 的 task 正文）。`validate_m56_tasks.py` 的 `E2b` 把「三要素 0 命中」**钉为期望值**并强制本行在册；技术方案章节改由 `B8`（`design_doc` 逐条等号）判定，强度不降 |
| `D-M5-02` | `audit/M56_PLANNING_HANDOFF.json` 登记关键路径为 `064→065→066→067→068→069→070→071→072→074→075`（11 跳），但 `066 → 067` 在 frontmatter 里**只是软边**（`067.depends_on = [064, 065]`） | handoff 口径含软边；依赖图口径只认硬边 | 关键路径以**硬边最长链**为准：`064→065→067→068→069→070→071→072→074→075`（9 跳），由 `validate_m56_tasks.py` 的 `C5` 逐字复算；handoff 文件**不追改**，差异以本行为唯一登记 |
| `D-M5-03` | `M-005-m5.md`「规划期结论」称「本轮**只落盘规划结构门禁** `tools/validate_m56_tasks.py` 并跑到 0 failed」，但该文件在 M5 施工期（064 ~ 068 收口后）**磁盘上并不存在**（`audit/T064_HANDOFF.json` 同期记为「尚未存在，属 T-…-069 交付物」） | 里程碑正文称已落盘；实际由 `T-…-069` 落盘 | 由 `T-…-069` 落盘并跑到 0 failed（本轮已完成）。**不追改 M-005 的该句原文**（里程碑正文属规划期留痕），差异以本行为唯一登记 |
| `D-M5-05` | `T-…-068` 期的 `8a891f2` 把 `index.FreshnessStale` 绑定到 `model.FMKeyStale`，理由仅为「二者同为字符串 `stale`」 | 同字符串 ≠ 同语义：`model.FMKeyStale` 是 frontmatter 键名，`index.FreshnessStale` 是索引新鲜度令牌 | 已由 corrective commit `7879ca8` 解耦（`8a891f2` **不重写**）：`internal/index/consistency.go` 恢复独立常量；`internal/cli/index.go` 继续用 `string(index.FreshnessStale)` 作 index 域数据键；`TestStaleKeyLiteralsAppearOnce` 重钉为**两个具名语义所有者双侧精确计数**（`model/frontmatter.go: FMKeyStale`、`index/consistency.go: FreshnessStale`），不放宽为任意多处 |
| `D-M5-06` | `m3_final_gate.py` 的登记基线是「恰 135 checks / **3** failed（H10k / H10x / H10ad = J15）」，阶段 A 首测 **9 failed**：新增 H10p / H10r / H10t / H10v / H10z / H10ab 六条 | M3 期把 T-039 ~ T-045 的实现事实做成了「双侧锁」（磁盘计数 == task 正文逐字），M5 新增 `internal/index` 包、`eg index` / `eg bench` 两条命令、11 个 e2e 与版本推进后，这些计数**全部合法地变了** | **已收敛：9 → 3**。处置口径是「不删断言、不降 checks、不改 M3 历史 task 正文」，改为把六条重钉成**阶段化双侧锁**（历史结论保留为 M3 期事实，新增当期事实的正面精确断言）：commit `b13209d`，checks 仍恰 135，实测 **3 failed**，失败集合逐字 == `{H10k, H10x, H10ad}`。本报告 §1 判据 17 与 §4 J15 一律写 **135 / 3**，**不得再写 9 failed**；剩余三条随 J15 延期到 `T-…-075` 归零 |
| `D-M5-07` | 六个历史门禁（`validate_m4_tasks` / `round3` / `round4` / `round5` / `m2_final_gate` / `m3_final_gate`）的「工作区卫生」判据会因 `__pycache__` 目录判红，而该目录由**运行门禁本身**产生（子进程未继承 `-B`） | 判据要求 epic 目录与本地化目录无临时产物；跑门禁却会造出临时产物 | 不改判据。收口口径固定为：**每次复跑前先 `find . -name __pycache__ -type d -prune -exec rm -rf {} +`**，且 `m5_final_gate.py` / `m5_mutation_test.py` / `validate_m56_tasks.py` 三者均设 `sys.dont_write_bytecode = True`、变异体临时文件跑完必删并反证无残留 |

## 6. M1–M4 历史 e2e 逐条清偿留痕（曾红 12 条 → 逐条实跑 `exit=0`）

阶段 A 第一版把其中 9 条登记为「冻结红项」并用失败签名钉死；owner 判定该口径等于**放宽历史判据**，
已整体删除。删除后按「39 条逐个真跑」复跑，**又暴露出 3 条第一版名单里根本没有的红项**
（`m4_r7_support.sh` / `m4_visibility_matrix.sh` / `m4_acceptance.sh`）——这恰好反证了
「先列名单、再对名单豁免」这一形态天然会漏：名单是人写的，只有逐个真跑才是事实。
故本节共 **12 行**，每行给出「为什么红 → 怎么修 → 修后实跑退出码」。

**清偿三条硬约束**（逐条自查，违反任一即视为洗白）：① 不删断言、不 `skip`、不吞退出码、
不加失败签名白名单；② 事实变了就**重钉判据形态**（历史结论保留为当期事实 + 新增当期事实的
正面精确断言），不降低合同强度；③ 不改产品代码去迁就测试，也不改 M1–M4 的历史验收结论。

| 脚本 | 曾红原因 | 清偿方式（判据怎么重钉） | 修后实跑 |
|-|-|-|-|
| `m2_card_show.sh` | `relations_out` 次序与 M2 合同 §3.3 不符：M4 让 `deprecated` 对端默认隐藏，改变了对端集合 | 把「次序」判据拆成**可见性阶段化 + 次序封闭**两侧：默认视图断言 M4 可见性裁决（`deprecated` 对端隐藏且计数精确），`--all` 视图再断言 M5 四级排序键逐字次序 | `b3bbb00`，exit=0 |
| `m2_docs_commands.sh` | `Makefile(0.5.0-m5)` 与版本决策文档(`0.3.0-m3`)「不同源」——决策文档指针冻结在 M3 | 版本同源判据改为「**当期版本真源**（`version.go` / `Makefile` / `SKILL.md` / README）四处逐字相等 + 历史决策文档按其自身里程碑版本自洽」，双侧都精确，不放宽为「包含即可」 | `9a90d8d`，exit=0 |
| `m3_docs_commands.sh` | 同上（M3 版本决策文档指针） | 同上，并补 INSTALL / SKILL 示例命令**可实跑**反证 | `9a90d8d`，exit=0 |
| `m3_ops_diagnostics.sh` | 诊断码封闭集合断言与 M3 合同不一致：M5 新增 `W22`–`W25` / `Q5` 撞 M3 期封闭集合 | 诊断码判据按**四域分治**重钉（`E` / `W` / `I` / `Q` 各自封闭 + 各域计数精确 + `W21` 仍不分配），M3 期成员一个不少、M5 新成员一个不多 | `a9a4eef`，exit=0 |
| `m3_proposal_layout.sh` | 「提案不得产生 `Q` 类诊断噪声」：M5 无索引降级留痕 `W23` + `Q5` 进了输出 | 重钉为**降级留痕精确断言**：无索引路径 `warnings` 段恰 `W23` → `Q5` 两条、且段内不泄漏提案 ID / 正文；封闭检查按文件头「无外部依赖」约束只用 bash/coreutils（不引入 `python3` / `jq`） | `a9a4eef` + `b878420`，exit=0 |
| `m3_execution_failed.sh` | 破坏性动作白名单外命中：`T-…-068` 新增的 `internal/index/scratch.go` 第三处 `os.RemoveAll` 不在 M3 白名单 | 不加通配白名单，而是把 scratch 的**受控 cleanup** 具名纳管进 B4 / U-02 判据（限定文件 + 限定前缀 + 限定调用点计数），越界仍判红 | `13cd331`，exit=0 |
| `m3_authorization.sh` | 同上 | 同上 | `13cd331`，exit=0 |
| `m2_acceptance.sh` | 复合：聚合上列 M2 项；且判据 4 原用「`internal/query/` 不读索引」的静态反证，M5 接索引后该反证必红 | 判据 4 重钉为 `.index` 落点集合封闭 + **三条读路径实跑等价性**（无索引 / 索引在位 / 删索引降级的 `rel data` 逐字节相等、反向关系恰 1 条、降级留痕恰 `W23`→`Q5`）；等价性复算只用 bash/coreutils | `f86ab54`，exit=0 |
| `m3_acceptance.sh` | 复合：聚合上列 M3 项；且判据 17 的 e2e 总数锁写成旧草稿 `46`（M4 记 10），与磁盘实测不符 | e2e 计数改为**加数式双侧锁**：总 `50 = M1+M2 10 + M3 15 + M4 14（10 + 4）+ M5 11`，M4 拆成 `T-059` 历史冻结 10 + `T-060`–`063` 新增 4；并把 `check.go` 的 `CheckReportFieldNotice` reconcile 命中具名归类为「报告三键占位提示」（要求常量行含 `ran=false` 恰 1） | `f86ab54`，exit=0 |
| `m4_r7_support.sh` | 步 11 的 `W2[1-9]` 越位判据：`T-…-068` 让 `W25`（分页截断）落到 `internal/query/page.go`，而该判据当时只摘掉 `internal/index/` 一个 M5 落地面，于是把**合法交付**读成越位 | M5 落地面扩为 `internal/index/ ∪ internal/query/page.go`，其余仍按 M4 结论「`W20` 是末位」原样复算；并补两格更严的正面断言：`W25` 唯一落点必须是 `page.go`、且**非注释面恰 1 处**（唯一常量声明），`W21` / `W26`–`W29` 仍全库恒 0 | `db64ce7`，exit=0 |
| `m4_visibility_matrix.sh` | G2 / G5 用 `warn_codes == ""` 表达「无诊断噪声」；`T-…-067` 之后读路径接索引，而该脚本语料**不建索引**，每条读命令都会如实带 `W23` + `Q5` 降级留痕 | 拆成两侧且都精确：业务面 `warn_codes_biz()` **只扣掉** `W23` / `Q5` 后必须逐字为空（夹带任何其它码即红）；索引面 `assert_degraded()` 要求降级留痕本身逐字恰 `W23,Q5`（成员、条数、次序全钉住）——降级被**正面断言**，不是被忽略 | `db64ce7`，exit=0 |
| `m4_acceptance.sh` | 三处复合：① 判据 14 聚合上面两条子脚本的红；② 判据 17 的越界反证整行 grep `flock|txn/|run.lock`，命中 M5 index 实现里两处**否定语境注释**（「不引入 `run.lock` / 事务日志（属 M6）」）；③ 判据 14 把 `m3_acceptance` 末行硬钉 `checks=83`，而清偿后合法长到 106（旧口径下该格在默认模式只打印不入账 = 延期债） | 判据 17 按**代码面 / 注释面分域 + 具名封闭**重钉：代码面（非注释行）恒 0；注释面命中集合逐字 == `internal/cli/index.go=1 internal/cli/index_sync.go=1`（总数恰 2，任意新增命中即红）；语义面不再逐行找否定词（`index.go` 的否定语义跨相邻两行，逐行判词会把正常换行误判成预告注释），改为两个具名文件的**精确相邻文本断言**：`index.go` 命中行与其上一行拼接后须逐字含「不引入任何 M6 能力（`run.lock` / 事务日志 / 退出码 `5`）。」，`index_sync.go` 命中行须逐字含「不引入 `run.lock` / 事务日志（属 M6）。」。判据 14 的末行锁按实测重钉为 `checks=106 failed=0`，并加「不得低于 M4 收口值 83」的单调锁，同时**取消**「非严格模式不入账」的豁免 | `db64ce7`，exit=0 |

**总控口径**：`m5_acceptance.sh` 现对 39 个历史脚本**逐个实跑**并逐条打印 `exit=`，
任一非零即记账并让总控**非零退出**（无豁免、无签名比对、无「名单内允许红」）。
`m2_acceptance.sh` / `m3_acceptance.sh` 两个总控自身也必须 `exit 0`（J14 的清偿判据）。
**教训登记**：第一版「9 条名单」漏了 3 条，红项由 9 变 12 后又全部清偿到 0；`m5_final_gate.py` 的 `F9` 因此不再只数条数，而是对本节 12 个脚本名做**逐字集合封闭**校验（漏登记、改名、拿别的脚本顶替，三种作弊都判红）。

## 7. `T-…-068` 移交债务终态

| ID | 终态 | 清偿留痕 |
|-|-|-|
| `T069-DEBT-1` | 已清偿 | commit `23b1415`：按 M5 合同 §6.2 重钉 `internal/query/search_test.go` 的 `W23` + `Q5` 期望与 PPE 留痕；**未放宽** `Q5` 语义 |
| `T069-DEBT-2` | 已清偿 | commit `0718c71` + `70f55ff`：`skill/SKILL.md` / README / INSTALL 收口到 M5 终态（22 命令 / `index` 四子命令 / `bench` / 分页 / 降级留痕），内嵌副本逐字相等；`m5_docs_commands.sh` 双向恰等门禁退 0 |
| `T069-FLAKE-1` | 已清偿 | commit `2e5f7a9`：把 `--strict` 只读判据从 Git racy-clean 启发式改成确定性四条；**未 skip、未重试、未改 A-44 产品新鲜度语义** |

## 8. 阶段 B 收口留痕（逐项实测，2026-09-09）

判定口径：每项都记「rc / 汇总数字」，任一项不符即阻断（不存在「记录完就宣告成功」）。

| # | 项 | 实测结果 |
|-|-|-|
| 1 | `make lint` | rc=0 |
| 2 | `go test ./... -count=1`（整包） | rc=0，`FAIL` 包数 0 |
| 3 | `bash test/e2e/m5_acceptance.sh`（全量 50 条真跑） | `EXIT=0`；11 个 M5 + 39 个历史脚本逐个 `exit=0`（零豁免 / 零冻结红）；工作区 `git status --porcelain` 前后一致 |
| 4 | 11 个历史门禁逐个 0 failed 且 checks 不降 | `validate_m1_tasks` 35（≥34）/ `validate_m2_tasks` 34（≥33）/ `validate_m3_tasks` 47（≥47）/ `validate_m4_tasks` 69（≥68）/ `round3` 65 / `round4` 114 / `round5` 102 / `m2_final_gate` 73 / `m4_final_gate` 35 / `independent_schedule_check` 22 / `derive_eg_baseline` 9 —— 逐个 rc=0、failed=0（复跑前先清 `__pycache__`，见 `D-M5-07`） |
| 5 | `m3_final_gate.py`（另行判定，不计入上项 11 个） | 恰 **135 checks / 3 failed**，失败集合逐字 == `{H10ad, H10k, H10x}`（J15，延期到 `T-…-075`） |
| 6 | `m4_mutation_test.py`（另行判定） | rc=0，**171 算子 / 检出率 100% / 命中登记判据 100%**、未被检出 = 无、真实文件逐字未变。首跑曾整支拒跑（rc=1）：该脚本把 `validate_m4_tasks.py` 的沙箱基线硬钉成「恰 68 checks / 0 failed」，而 M5/M6 规划 commit `5539e62` 给 V4 新增了「M5/M6 manifest 三重反证」这一条**只加严**的判据，当期实测 **69 / 0**；另有 `Z2-2` 锚点因编号作用域 001–063 → 001–075 而失配。处置与 `D-M5-06` 同口径且**不新增漂移编号**（§5 集合已冻结为六条）：commit `c1faf18` 把基线重钉为三侧同锁（`BASELINE_CHECKS_M4 = 68` 保留 M4 历史事实 + 单调锁、`BASELINE_CHECKS = 69` 当期等号侧、`BASELINE_NEW_CHECK` 要求 68 → 69 的增量**具名**且在 V4 里恰 1 处）；commit `f1f52e7` 把 `Z2-2` 越界面重定向到真界外号 076，并**新增** `Z2-3` 补「在册新号 064 被双占」这一新攻击面，算子 170 → 171（只增不减、不删算子、不放宽判据、不改 V4） |
| 7 | `validate_m56_tasks.py` | 35 checks / 0 failed |
| 8 | `m5_final_gate.py` | 阶段 B 回填前 67 checks / 1 failed（唯一红 = `R3`，非 `R3` 红 0）；18 行结论回填后实测 **67 checks / 0 failed**（`R3` 转绿，P1 0 / P2 0） |
| 9 | `m5_mutation_test.py` | **89 算子 / 0 存活**（算子数 ≥ 60）；`GATE` 基线随 `R3` 转绿由 `1` 同步改为 `0`——基线记「实测事实」而非「允许的红」，否则「严格大于基线」的判定会白吃一格容差 |
| 10 | `make dist` 四平台重建 + 静态反证（判据 16 剩余项） | rc=0，`dist/eg_*` 恰 4 个 + `SHA256SUMS`；`file(1)`：linux 两平台 `statically linked`、`dynamically linked` 命中 0；`--version` = `eg 0.5.0-m5 (commit db64ce7…)` == 当期 HEAD |
| 11 | §1 的 18 行结论回填 | 由 `NOT-VERIFIED` 全部改为 `PASS`（0 `FAIL`），`R3` 转绿 |

**阶段 B 纠正轮（owner 阻断后回滚，2026-09-09）**：本节第 6 项的 `m4_mutation_test.py` 基线 68 → 69 一度被登记为**新增的第七个漂移编号（08 号位）**，§5 在册集合因此扩到七条。owner 判定该扩张违反已锁定口径：§5 的漂移编号集合精确冻结为 `{01, 02, 03, 05, 06, 07}`（04 作废、其余不扩张），扩表直接导致 `m5_final_gate.py` 的 `F8` 集合逐字判据转红。本轮已**整体回滚**：删除该 08 号位登记行及全文所有引用（08 号位与 04 号位同为**不在册编号**，本报告不再出现该编号字面）、`m5_final_gate.py` 的 `DRIFT_IDS` / `DRIFT_ANCHORS` 恢复六条、`m5_mutation_test.py` 恢复对应六条口径的算子（89 个，未新增 08 号位相关算子）；68 → 69 的根因与处置**只留痕在本节第 6 项与 `T-…-069` 的 Activity Log**，不占漂移编号。回滚后实测：`m5_final_gate.py` 67 checks / 0 failed、`m5_mutation_test.py` 89 算子 / 0 存活、`validate_m56_tasks.py` 35 checks / 0 failed。

**未随本里程碑归零的项（如实登记，不洗白）**：`J15` 三条（`H10k` / `H10x` / `H10ad`）与 `I2`
按 §4 继续延期到 `T-…-075`；`A-49` / `A-50` / `A-51` 三条未决按 §3 沿用登记态。
