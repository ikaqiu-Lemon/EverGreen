# M4（S3 一致性与对账）验收报告

- 报告 ID：`2026-12-12-m4-acceptance-report`
- 里程碑：`M-004`（M4 一致性与对账）
- 责任 task：`T-evergreen.s1_main_flow-158614-063`（M4 验收收口，最终状态 `done`；
  本报告按最终提交与状态迁移后的事实复算）
- 目标日：`2026-12-12`（== `M-004.date` == `T-063.due`）
- 编制日：2026-09-07
- 判定统一在 `EvergreenDir` 根执行；`E=evergreen`，`T=teamwork`，
  `D=teamwork/projects/evergreen/s1_main_flow/docs/specs`。

> **三值口径（不许二值化）**：`PASS` = 全部机器断言通过；`FAIL` = 至少一条不通过；
> `NOT-VERIFIED` = 判据依赖沙箱外的外部事实（如 owner 真实裁决意图），本地无法证实亦不许伪证。
> **本报告不为让门禁通过而修改任何 M1–M3 历史结论**；发现的问题一律如实登记为 owner 回写项或 issue。

---

## 0. 总结论

- **M4 完成判据 17 条（严格原文口径）**：`PASS 15 / FAIL 2 / NOT-VERIFIED 0`（见 §2）。
  判据 14、15 的 FAIL 均来自 M2/M3 历史总控冻结事实与 M4 强制演进冲突，不是当前产品功能失败。
- **两项 owner 新裁决**（K-041-01 §4.3 不存 SHA、K-043-01 deprecated 默认隐藏）：**均已闭环**，机器反证在册（§3）。
- **P0 = 0；非阻塞 P1 = 2**：判据 14 的历史 e2e 冻结事实、判据 15 的历史 final gate 冻结事实。
  I2 合同⟷实现漂移与三处验证 grep 精度问题列为 P2；本轮未修改任何 M1–M3 历史脚本。
- 按当前开发减负策略，默认门禁只阻断 M4 产品面 P0 / 核心 P1；历史脚本仍全部执行并留痕，
  `M4_STRICT_HISTORY=1` 可恢复严格失败。全量结果见 §5。

**是否允许进入后续开发**：**允许**。M4 当前产品测试、lint、Go 全量、M4 专属 e2e 与规划门禁均通过；
判据 14/15 的严格失败作为延期补测债登记，不伪装成 PASS，也不阻断 T-063 按用户明确的减负策略关闭。

---

## 1. 审计方法与独立性声明

- 本次验收**不直接相信** milestone / task 的 `done` 标记，而是对附件最终状态**独立复算**：两仓 Git 状态、
  17 条完成判据的可执行判定、14 个 M4 e2e、M4 规划门禁（`validate_m4_tasks.py` / `m4_mutation_test.py` /
  `m4_final_gate.py`）、历史门禁不回归、版本与 dist 溯源。
- 越界反证按「术语只能出现在否定语境」原则复算，防止 S4/M5、S5/M6 能力被提前塞入 M4。
- 一切写操作只发生在各 e2e 自建的 `mktemp -d` 沙箱内；两仓工作区在验收脚本运行前后逐字相等。

---

## 2. M4 完成判据 17 条逐条判定

> 每条给出「判据来源（M-004 责任 task）→ 关键判定命令 → 实测 → 结论」。凡「`go test ./... -count=1` 全绿」
> 一句已覆盖该判据点名的 `-run` 子集（全量测试包含这些用例），逐条不再重复贴子集输出。

| # | 判据（摘要） | 关键判定 / 实测 | 结论 |
|-|-|-|-|
| 1 | T-048 八项前置裁决关闭（A-30~A-35 + A-38/A-39） | `2026-11-15-m4-prestart-adjudication.md` 在盘；`A-30~A-35` 恰 6 行、`A-38/A-39` 恰 2 行；含「不得替 owner 拍板」「content_hash 并发保护不受本裁决影响」字样（`m4_final_gate.py` C3/C4/C5 = OK） | **PASS** |
| 2 | `internal/reconcile` 包边界：零写盘/零 commit/零 `os/exec`/依赖单向 | `test -d internal/reconcile` = 0；`go test ./internal/reconcile` 绿；反向依赖 `grep -rn internal/reconcile internal/query internal/store internal/plan internal/proposal` = 0 | **PASS** |
| 3 | Finding 封闭四键 + `check` 十二值封闭枚举 + 诊断码双向单射 | `check` 十二值枚举实测 **12**；`E11–E14/W13–W20` 实测 **12**；`go test ./internal/reconcile` 绿（含 `TestCheckEnumExactlyTwelveValues` 等 ≥6 用例） | **PASS** |
| 4 | R1 Git 纳管：检出 `git_uncommitted`、恰一次 commit、干净树零 commit、`add -A` 未改窄 | `bash test/e2e/m4_r1_takeover.sh` 退 `0`（含 K-063-02 grep 精度修正后）；`grep -c internal/store internal/cli/reconcile_commit.go` = 0 | **PASS** |
| 5 | R2 `reviewed_at` 补齐：三条件同真、只写 `reviewed_at`、B3 不豁免、不裸写 store | `bash test/e2e/m4_r2_reviewed_backfill.sh` 退 `0`；`go test ./internal/reconcile ./internal/cli` 绿 | **PASS** |
| 6 | R4 重复 ID/悬空引用/孤儿：E11/E12/W17 + 孤儿三子型 + 与 Q1/Q2 零双计数 | `bash test/e2e/m4_r4_structure.sh` 退 `0`；孤儿三子型枚举 = 3；`grep -c VisibleEndpoints internal/reconcile/r4_structure.go` = 0 | **PASS** |
| 7 | R3/R5/R6/R7 按「逐项评估结论」落地（只报告、收窄形态） | 四个 e2e `m4_r3_relation.sh`/`m4_r5_domain_moved.sh`/`m4_r6_recap_stale.sh`/`m4_r7_support.sh` 逐个退 `0`；`stale_reason` 封闭三值；R7 零落盘标记（`grep -rn 材料支持不足 internal/reconcile internal/store` 非测试 = 0） | **PASS** |
| 8 | 报告 `reconcile` 恰三键 + 非对账路径键在值空 + `affected` 占位恒 `null` + `skipped[].kind` 恰 2 值 | `bash test/e2e/m4_report_reconcile.sh` 退 `0`；`json:"ran"/"commit"/"findings"` 各恰 1；`skipped[].kind` 实测 2 值 `{file_changed, user_block_unsafe}`；`go test ./internal/report ./internal/reconcile` 绿 | **PASS** |
| 9 | `eg reconcile` commit 恰 0/1、五键 envelope、退出码 0/1/2/3/4、拒 `--target`/`--domain`、命令 18→19 | `bash test/e2e/m4_cmd_reconcile.sh` 退 `0`（含 K-063-01 grep 精度修正后）；`go test ./internal/cli` 绿（含 `TestCommandCountNineteen` 等 ≥8 用例） | **PASS** |
| 10 | `eg check` 恒 0 commit、只跑 R3/R4、五 check 永不产出、命令 19→20 | `bash test/e2e/m4_cmd_check.sh` 退 `0`；`wantCommandCount = 20` 恰 1 处；`go test ./internal/cli` 绿 | **PASS** |
| 11 | K-041-01 关闭：`eg delete` 后零脏、commit 恰 +1、新提案不含 `git_commit` | `bash test/e2e/m4_k041_proposal_clean.sh` 退 `0`；`grep -cE 'GitCommit:\s*info\.SHA' internal/cli/delete.go` = 0；`go test ./internal/proposal ./internal/cli` 绿 | **PASS** |
| 12 | K-043-01 关闭：deprecated 默认隐藏、`--include-deprecated` 显式展示、`data` 键不扩张、G1–G10 全绿 | `bash test/e2e/m4_visibility_matrix.sh` 退 `0`；`grep -c Deleted internal/query/card.go` = 11（≥11）；`VisibleEndpoints` in `internal/query` = 6（≥5）；`go test ./internal/query ./internal/cli` 绿（含 G1–G10 + 逻辑删除正交 ≥15 用例） | **PASS** |
| 13 | B3 `content_hash` 未被误伤（只增不减 ≥41）+ 报告体 `git.commit`/投影键仍在 | `grep -rn 'ContentHash\|content_hash' internal/store internal/plan`（非测试）= **42**（≥41）；`go test ./internal/store ./internal/plan ./internal/proposal` 绿 | **PASS** |
| 14 | M1–M3 全量不回归：`make lint` + `go test ./...` 全绿；25 个 M1–M3 e2e 逐个退 0；e2e 磁盘 ≥39、`m4_*.sh` 恰 14 | lint、Go 全量、14 个 M4 e2e 与 `m1_real_article.sh` 全绿；磁盘 e2e=39、M4=14、M1–M3=25。**但** `m2_acceptance.sh` 退 1（旧可见性/版本），`m3_acceptance.sh` 退 1 且末行 `checks=94 failed=1`（旧子脚本、总数、版本、用法落点） | **FAIL**（历史冻结事实；延期，不改历史脚本） |
| 15 | M4 规划门禁全绿 + 历史门禁不删、独立可复跑 | **M4 侧全绿**：`validate_m4_tasks.py`=**68 checks,0 failed**、`m4_final_gate.py`=**35 checks,0 failed**、`m4_mutation_test.py`=0 存活变异；**历史门禁一个未删、独立可复跑**。**但**判据现文子句「历史 10 门禁逐个 0 failed」**不成立**：`m3_final_gate.py` 残留 **3 条** FAIL（H10k e2e 计数活常量滞后、H10x `VisibleEndpoints` 掺入 deprecated 维度、H10ad 版本 `0.4.0-m4`），系 M4 依判据 12/14/16 **合法推翻** M3 冻结事实所致（详见 §4-D） | **FAIL**（口径矛盾；M4 产品侧无缺陷，owner 回写，见 §4-D） |
| 16 | 版本号推进到 `0.4.0-m4` 且多处同真 + dist 溯源可复算 | `grep -c '0.4.0-m4' version.go Makefile README.md INSTALL.md`（去 `:0`）= 4；`! grep -qF '0.3.0-m3' version.go Makefile`；`bash test/e2e/m4_docs_commands.sh` 退 `0`；dist 四平台 + SHA256SUMS 校验（T-062 收口，产物 commit=402bc98） | **PASS** |
| 17 | 越界反证：S4/M5 与 S5/M6 能力一条未提前 | `.index` / `FTS5` / `sqlite` / `flock` / `txn` / `run.lock` / `os.Exit(5)` 均为 0；对账非写前置产品源码 grep = 0（K-063-03 排除测试文件并纳入合法 render/sample 辅助文件）；退出码全集仍恰 `{0,1,2,3,4,6}` | **PASS** |

**判据小结：15/17 PASS，2 FAIL（判据 14、15；历史冻结口径，已延期），0 NOT-VERIFIED。M4 当前产品面无阻断。**

---

## 3. 两项 owner 新裁决关闭结论（机器反证）

### 3.1 裁决①（K-041-01）：放宽合同 §4.3 —— 不持久化该处 SHA 字段

- **精确落点**：合同 §4.3 提出的 `execution.git_commit`（提案 frontmatter 的 SHA 投影字段）。
- **关闭动作**：整键退役（A-32），`eg delete` 成功后 `execution.succeeded` 与 `deleted_at` 同进**单次** delete commit，
  成功后工作区零脏；回执由报告体 `git.commit` + Git 历史承载；历史键只读兼容、不迁移。
- **机器反证（PASS）**：
  - `grep -cE 'GitCommit:\s*info\.SHA' internal/cli/delete.go` = **0**（不再写入 SHA）。
  - `bash test/e2e/m4_k041_proposal_clean.sh` 退 **0**（零脏、commit 恰 +1、新提案不含 `git_commit`）。
- **不误伤边界（关键，PASS）**：本裁决**只针对 §4.3 的 SHA**，**不得**削弱 B3 已有的 `content_hash` 并发保护：
  - `grep -rn 'ContentHash\|content_hash' internal/store internal/plan`（非测试）= **42**（≥41，只增不减）。
  - 报告体 `git.commit`（`Commit *string json:"commit"`）与 `proposals[].execution.git_commit` **投影键仍在**（读侧宽容既有键）。
- **落入 M4 规划的口径**：见 `2026-11-12-m4-visibility-and-execution-contract.md` 第一部分 §1/§2.3/§2.4/§2.5；
  M-004「明确不做」逐字写明「不削弱 B3 `content_hash`、不改报告体 §4.6 的 `git.commit`」。

### 3.2 裁决②（K-043-01）：deprecated 关系端点默认隐藏，显式筛选后才展示

- **落点**：`eg rel` / `eg card show` 的关系端点；deprecated 对端**默认不展示**，用户经**显式** `--include-deprecated` 后才展示（带 `[失效]`）。
- **关闭动作**：新增**唯一一个** flag `--include-deprecated`（纯读、不扩到写命令）；被隐藏信息改由 envelope `warnings[]` 的 **`Q4`**（查询域，非 `W21`）承载；
  `eg rel --json` / `eg card show --json` 的 `data` 键集合**一字不扩张**。
- **机器反证（PASS）**：
  - `bash test/e2e/m4_visibility_matrix.sh` 退 **0**（十组矩阵 G1–G10 全绿）。
  - `go test ./internal/query ./internal/cli` 绿：默认隐藏（G1）、`--include-deprecated` 带标记展示（G2）、
    正向/反向关系同策略（G3）、逻辑删除维度正交仍隐藏（G4）、`replaced_by` 链双向（G5）、
    flag 被 5 个写命令拒绝（G7）、`data` 键不扩张（G8）、`Q3` 不被 `Q4` 触发（G9）、逻辑删除维度不被替换（G10）。
  - `grep -c Deleted internal/query/card.go` = **11**（≥11，逻辑删除维度仍在，与 deprecated 可见性正交）。
- **落入 M4 规划的口径**：见姊妹合同第二部分 §3.2 四象限真值表、§3.4 `--include-deprecated` 参数合同、
  §3.5 不扩张 `data` 键 + `Q4`、§4.4 十组测试矩阵 G1–G10、§4.5 不误伤机器反证；登记项 A-37/A-38/A-39。

---

## 4. 发现登记（按严重度）

> **2 项非阻塞 P1**（§4-C/§4-D，历史冻结口径；按开发减负策略延期）+ **3 项 P2**（§4-A/B，均不阻塞）。

### 4-A. 合同⟷实现漂移：I2 诊断码「新增 14」vs 实现「13」（P2，owner 回写）

- **事实**：`2026-11-12-m4-reconcile-contract.md` §14（行 127/135/137/348/349）声称
  「M4 后诊断码全集 = `E1..E14 ∪ W1..W20 ∪ {I1,I2} ∪ Q1..Q4`，**新增恰 14 个**（E11–E14 四 + W13–W20 八 + `Q4` 一 + **I2 一**）」，
  其中 I2 定义为「本次对账**零 finding** 的信息条目」。
- **实测（evergreen 代码）**：`internal/` 非测试代码实际实现的新增诊断码为 **13** 个——`E11–E14`(4) + `W13–W20`(8) + `Q4`(1)；
  `internal/report/report.go` 只有 `CodeI1 = "I1"`，`AddInfo` 仍用 `CodeI1`，**`I2` 从未落地**
  （`grep -rn 'I2\|CodeI2' internal/ --include=*.go` 非测试 = 0）。
- **影响面判定**：I2 是「零 finding 的 info 条目」，**不对应任何 `check`**，因此**不落在**完成判据 3
  （只判 `check` 十二值枚举 + `E11–E14/W13–W20` 十二码双向单射）或任何其他 17 条判据的强断言上。
  故本漂移**不使任一完成判据 FAIL**，M4 仍视为达成。
- **处理**：本轮**不动产品逻辑**（不新增 I2 实现），如实登记为 **owner 回写项**：请 owner 裁决二选一——
  ①在 S3 之后补实现 I2（信息码，零 finding 正常态）；或②修订合同 §14 计数口径为「新增 13 个」并移除 I2 声明。
  `m4_final_gate.py` D2 强制本报告登记该漂移，防止静默洗白。

### 4-B. M4 e2e 门禁 grep 精度缺陷（P2，已修验证脚本，产品不变式成立）

> 与 T-062 已登记的 K-062-01/02/03 同类：验证脚本的 grep 口径不精确，**非产品缺陷**。本轮按 token/边界/
> `_test.go` 排除修正**验证脚本**，真实产品不变式经反证成立。**未改任何产品代码**。

- **K-063-01**（`test/e2e/m4_cmd_reconcile.sh` §8 越界反证）：
  - 缺陷：`grep -rn 'runReconcile' internal/cli/ --include='*.go'` 未排除 `_test.go`，以子串命中测试助手
    `runReconcileCLI`（`check_test.go:246/433`），误报「Wire 行之外被引用」。
  - 修正：新增 `--exclude='*_test.go'`（本反证语义为「**产品源码级** runReconcile 只现于 Wire 行」）。
  - 产品不变式（未放宽）：`Wire("reconcile", …)` 行恰 1 且只在 `root.go`；修正后 `bash m4_cmd_reconcile.sh` 退 `0`。
- **K-063-02**（`test/e2e/m4_r1_takeover.sh` §9 边界反证）：
  - 缺陷：`eg reconcile` 用法行「落点集合应恰 `{reconcile.go}`」漏计 T-059 `eg check` 的合法交叉引用帮助文案
    （`check.go`：「修复走 eg reconcile」「未判面…请跑 eg reconcile」）与注册表注释（`commands.go`）。
  - 修正：将 `check.go` / `commands.go` 的交叉引用一并豁免（与既有 `reconcile_render.go` 豁免同理，属边界声明非用法行）；
    写口守护由既有「集合恰等」逐字保留（写口 `reconcile_commit.go` 若出现非消歧用法行，集合立刻 ≠ 单元素判红）。
  - 产品不变式（未放宽）：`Name: "reconcile"` 注册形态全包恰 1 且只在 `reconcile.go`（`T058_REG_ALL=1 / OUT=0`）；
    修正后 `bash m4_r1_takeover.sh` 退 `0`。
- **K-063-03**（`test/e2e/m4_acceptance.sh` 判据 17）：旧 grep 扫入 `_test.go`，并漏列 `reconcile_render.go` / `reconcile_recap_sample.go` 两个合法命令族辅助文件；现按产品源码与完整命令族复算，结果为 0。

### 4-C. 判据 14：历史 e2e 冻结事实与 M4 当前行为冲突（**P1，延期补测**）

- `m1_acceptance.sh` 从不存在，且新增它会破坏同一判据的「M1–M3 恰 25 个」断言；M1 由 `m1_real_article.sh` 承载。
- `m2_acceptance.sh` 实测退 1：`m2_card_show.sh` 要求 deprecated 关系端点默认可见，`m2_docs_commands.sh` 仍指向旧版本决策；两者被 M4 判据 12/16 合法推进。
- `m3_acceptance.sh` 实测退 1，末行 `checks=94 failed=1`：除上述 M2 子脚本外，还冻结 M3 期 e2e 总数、版本 `0.3.0-m3` 与 `eg reconcile` 用法落点。
- 本轮不修改历史脚本。默认减负门禁仍逐个执行并把非 0 记为延期债；`M4_STRICT_HISTORY=1 bash test/e2e/m4_acceptance.sh` 保留原始严格复算并退非 0。

### 4-D. 判据 15 口径矛盾：`m3_final_gate.py` 冻结的 M3 事实被 M4 合法推翻（**P1，owner 回写**）

- **事实**：`m3_final_gate.py`（M3 收口门禁）持有对 M3 期事实的**双侧锁**。提交交付范围 + 清理
  `tools/teamwork/vendor/**/__pycache__` 后，其**工作区卫生**红灯（H6）消除，但仍残留 **3 条** FAIL：
  1. **H10k**（e2e 计数活常量滞后）：常量 `E2E_M4_COUNT=10 / E2E_TOTAL=35`（= 10+15+10），磁盘实测 `m4_*.sh`=**14**、
     总 e2e=**39**（10+15+14）。该常量按门禁自述「随每个 M4 task 收口**按实测重钉**，只改形态、本体一格不放宽」
     的既定模式演进，实际重钉到 T-056（M4 期 10 个）后**未随后续 M4 task 续钉**至最终 14。
  2. **H10x**（`VisibleEndpoints` 维度）：门禁锁「`VisibleEndpoints` 只认删除维度」，而 M4 **判据 12 / K-043-01**
     依 owner 裁决 4 **合法新增** deprecated 可见性（默认隐藏、`--include-deprecated` 显式展示），
     `VisibleEndpoints` 因此掺入 status/deprecated 维度。
  3. **H10ad**（版本字面量）：门禁锁「版本 `0.3.0-m3` 四处同真」，而 M4 **判据 16**（T-062 收口）依方案**合法推进**到
     `0.4.0-m4`，`INSTALL.md`/`Makefile`/`README.md` 不再逐字含 `0.3.0-m3`。
- **性质判定**：三条 FAIL **均非 M4 产品代码缺陷**，而是「冻结的 M3 门禁事实」与「M4 强制变更（判据 12/14/16）」
  之间的**内在矛盾**——判据 15 现文子句「历史 10 门禁逐个 0 failed 且 checks 不降」与判据 12/14/16 **不可同时成立**。
  这是**判据口径缺陷**，且 H10x/H10ad 属门禁自述「**不可回退双侧锁**、只加严、回退即 FAIL」，**无 M4 重钉先例**。
- **本轮处置（不越权、不伪证）**：严守执行约束「**不删除历史门禁、不为了让门禁通过而修改 M1–M3 历史结论**」——
  **本轮不修改 `m3_final_gate.py`**（既不重钉 H10k，也不放宽 H10x/H10ad）。如实把判据 15 记为 **FAIL** 并登记本项。
  M4 阶段使用**独立门禁** `m4_final_gate.py`（35 checks / 0 failed）作为本阶段权威判定，与历史门禁解耦。
- **本轮采用的减负策略**：不修改 `m3_final_gate.py`，接受其在 M4 后对被合法推翻的 M3 冻结事实报非 0；
  M4 阶段权威判定挂 `m4_final_gate.py`。历史脚本仍完整执行并保留严格模式，债务不被删除或伪装成 PASS。

---

## 5. 全量验证命令与结果

在 `EvergreenDir/evergreen` 执行（除注明）：

| 命令 | 结果 |
|-|-|
| `make lint` | **PASS**（exit=0；旁路留痕 `/tmp/aime/T063-full-lint.log`：`gofmt -l .` 无输出、`go vet ./...` 通过、写路径 guard 通过） |
| `go test ./... -count=1` | **PASS**（exit=0；旁路留痕 `/tmp/aime/T063-full-go.log`；16 个包全绿：cmd/eg、internal/cli、git、mdfile、model、plan、proposal、query、query/filter、reconcile、report、rules、store、version、skill、test/e2e） |
| `TestSkillNoStageTwoPromise`（K-062-01 修复点） | **PASS**（`--include-deprecated` 按 `\bdeprecate\b` 词边界匹配，不再误判为 M3 写命令；真正 `deprecate` 写命令阶段约束保留） |
| 14 个 `m4_*.sh` 逐个 | **全部退 0**（含 K-063-01/02 修正后的 `m4_cmd_reconcile.sh`、`m4_r1_takeover.sh`） |
| `ls test/e2e/*.sh \| wc -l` | **39**（≥39）；`m4_*.sh`=**14**；`m1\|m2\|m3_*.sh`=**25** |
| `cd $T && python3 …/validate_m4_tasks.py` | **68 checks, 0 failed**（最终状态快照为 048–063 全部 done，SELF_ASSERTS=37） |
| `cd $T && python3 …/m4_final_gate.py` | **35 checks, 0 failed**（本报告在盘后） |
| `cd $T && python3 …/m4_mutation_test.py` | 170/170 检出，0 存活变异，真实工作区零污染 |
| 历史规划门禁 `validate_m1/m2/m3_tasks.py` | **各 0 failed**（34 / 33 / 47 checks，一个未删、独立可复跑） |
| 历史 final gate `round3/round4/round5/m2_final_gate.py` | **提交交付范围 + 清理 `__pycache__` 后各 0 failed**（65 / 114 / 102 / 73 checks；四者原仅工作区卫生一项红灯） |
| 历史 final gate `m3_final_gate.py` | **135 checks / 3 failed**（H6 工作区卫生提交后消除；**残留 H10k/H10x/H10ad 3 条**为 M4 合法推翻 M3 冻结事实，详见 §4-D，判据 15 记 FAIL） |
| `cd $T && python3 …/mutation_test.py`（M3 变异，413 条） | 检出率 100%（**提交后复跑**，工作区卫生前置，见 §4-D/R-16） |
| `independent_schedule_check.py` / `derive_eg_baseline.py` | 22 checks / 0 failed；基线模式 B |
| 版本 / dist（T-062 收口） | `0.4.0-m4` 四处同真、`0.3.0-m3` 于 version.go/Makefile 零残留、dist 四平台 + SHA256SUMS 校验通过、产物 commit=402bc98 |

> `m4_acceptance.sh` 已完整跑完：默认减负模式执行所有历史脚本但只阻断当前 M4 产品面；严格模式通过 `M4_STRICT_HISTORY=1` 保留原始失败复算。

---

## 6. 延期债务与回写清单

1. **【P1】判据 14 历史 e2e 冻结事实**（§4-C）：后续统一补测时决定阶段化重钉或保留为历史快照。
2. **【P1】判据 15 历史 final gate 冻结事实**（§4-D）：本轮采用「M3 门禁只保证 M3 期事实，M4 权威判定挂 `m4_final_gate.py`」；后续如需跨里程碑全绿，再显式重钉。
3. **I2 诊断码**（§4-A）：补实现 I2，或修订合同 §14 计数为「新增 13」并移除 I2 声明。
4. **判据 14 文本**：把不存在的 `m1_acceptance.sh` 改为 `m1_real_article.sh`，消除与「恰 25」的冲突。
5. **飞书权威文档差异**：本轮不修改飞书文档；上述差异待后续统一回写。

---

## 7. 被推翻用例重钉清单

- 无（本轮未推翻任何 M1–M3 既有用例；仅修正 3 条 M4 e2e 的验证 grep 精度与 1 条 M3 期测试的 M4 阶段化兼容，
  均为验证侧、未改产品断言语义、未删任何既有用例）。
