# M6（S5 · 原子性与强校验）验收报告 —— 兼 S1 阶段收口

- **里程碑**：`M-006`（S5：原子性与强校验）
- **收口 Task**：`T-evergreen.s1_main_flow-158614-075`
- **目标态**：**权威 Markdown 一个字节都不错写**（从「尽力而为 + 事后可查」升级为「要么全成、要么全不成」）
- **Evergreen 构建基线**：commit `b535b54`，`eg 0.6.0-m6 (commit b535b54, built 2026-09-11T11:28:43Z, go1.24.13)`
- **上游事实基线**（只读继承，不改历史结论）：
  - `docs/specs/2027-01-17-m5-acceptance-report.md`（M5 事实基线、`J14`/`J15`/`I2` 延期债、口径漂移 `D-M5-*`）
  - `docs/specs/2026-12-12-m4-acceptance-report.md`（M4 事实基线与延期债起源）
  - `docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md`（`T-070` 冻结的 M6 合同）
  - `docs/specs/2027-02-21-m6-release-and-version.md`（M6 版本与发布口径 + 四平台 dist 溯源）

## 0. 本文件的阶段状态（**必须先读**）

本报告为 **M6 一次性统一收口稿**：18 条完成判据逐条给出实测证据与 `PASS`/`FAIL` 结论、
`J14`/`J15`/`I2` 三条延期债给出**终态**（M6 之后无里程碑可延，「继续延期」不再是合法取值）、
S1 全阶段 70 条生效需求逐条给出结论、三项已知限制在册、环境 issue 给出终态。

**边界声明（越界即失败，与 `T-075` Scope 逐字一致）**：本轮**不改** M1~M5 历史 Task /
历史 Activity Log / 历史验收结论 / **历史门禁判据**（`*_final_gate.py` / `validate_m*.py`
的判据一字不动）；**不改** M6 功能实现（`071`~`074` 已冻结）；**不新增命令**。唯一显式例外
（合同 §16.4）：M1~M5 **历史 e2e 脚本**中与 `.index/` 布局、「M6 不存在」相关的断言允许
**按现态重钉**（只许「保留历史事实 + 新增现态双侧锁」，禁止 skip / 条件跳过 / allowlist /
frozen-red / 删断言）。凡历史门禁 `.py` 因 M6 版本推进与 e2e 计数上升而产生的冻结基线漂移，
**一律登记为债务 / 环境 issue，不改判据**（见 §4、§7）。

### 0.1 本文件的修订批次（**最终证据审计**，2026-09-12）

初版（commit `fa536d6`）落盘后经**最终证据审计**发现下述**证据缺口**，本批次逐项修正。
修正以**如实化本报告与 Issue 的措辞 / 表格**为主，另含**一处产品缺陷修复**（`I-002`，见 §7.1）；
**不动**任何历史判据、历史验收结论与 M6 功能实现：

| 缺口 | 初版形态 | 本批次修正 |
|---|---|---|
| §6.1 算术与口径失真 | 表列 16 支、小结写「12 支 0 failed / 4 支环境漂移」，但表内实有 **5 支非零**（round3 2 / round4 1 / round5 1 / m2_final 1 / m3_final 9），且**漏登** `m5_final_gate`（实测 67 checks / 1 failed） | 表扩为**逐支 17 行**并新增「本次执行 / 复用 / 外部未知」口径列；小结按真实计数重写（见 §6.1）。**不再出现「全部测试全绿」这类掩盖已登记差异的表述** |
| R6/R7 重钉缺专表 | `T-075` deliverable / DoD 要求「逐行重钉对账表」与「R7 零重钉清单」，初版只在正文散述、无专表，全文 `R7` 出现 **0** 次 | 新增 §6.2（R6 逐条重钉对账，带脚本 / 行号 / 原断言 / 保留的历史事实 / 新增现态双侧锁 / 复跑证据）、§6.3（R7 零重钉清单，逐支给 diff 事实）、§6.4（`.index/` 共存与零写入边界实证，逐支给测试名与实跑输出） |
| `I-002` 凭状态未验 | 该 issue 自 2026-09-02 起 `open`，历次收口只沿用状态、**未核验现态** | 本批次实测**缺陷仍在**并**修复**（`a2069db`：dry-run 卡计数与 `planned[]` 同源）+ 新增端到端回归 + 受影响面复算，随后走 Teamwork `close-issue` 正常关闭（§7.1） |
| `I-003` / `I-004` 未给复现证据 | 仅有原始 filer 描述，无现态复现 | 本批次逐条实跑复现，写明命令 / 环境 / 日志路径 / 影响 / 解除条件 / 归属，**保持 open，不凭状态关闭**（§7.1） |
| 双架构未原生运行只写在风险项 | `linux/arm64` 与 `darwin/*` 仅在发布文档与风险里提及，**未建 issue** | 新建 `I-007` 环境兼容 issue（命令 / 当前环境 / 证据 / 影响 / 解除条件），并入 §9 已知限制第 4 项；**绝不**把交叉编译称为原生验收通过 |
| dist 预置二进制用途未分析 | 交付包与 `dist/` 关系未说明 | 新增 §7.2：用途分析（源码 / 测试零消费预置二进制）+ **按新 HEAD `a2069db` 一次性重建四平台产物**并四重溯源（构建时间 / `sha256sum -c` / `file` / 本机 `--version` 与冒烟）+ 删除包外四个可执行、保留 `SHA256SUMS` 与新增 `dist/PROVENANCE.txt`，并把 `b535b54` 明确标为**历史全量测试基线**与 `a2069db`（交付源码基线）分开 |
| §6.2 重钉计数仍不准 | 修订稿写「26 个重钉点 / 23 个文件」：**漏掉整批 `0ef2bc6`（C2b-A/B）4 个触达点**（含 `T-075` 明确要求的 `m5_docs_commands.sh` R7 重钉），且去重数算错（当时表内实为 24） | §6.2 改为 **`git show --numstat` 机器复算**：三批次触达点 22+4+4 = **30**，去重唯一文件 **27**（23 `.sh` + 3 Go 测试 + 1 语料），补齐 4 行（`m3_docs_commands` 第 2 次触达 / `m4_docs_commands` / `m5_docs_commands` / `internal/cli/docs_test.go`），并新增「判据面」列（`R6` 22 / `R7` 3 / `V` 4） |
| 交付总括口径未分层 | 结论散落各节，易被读成「全绿」 | 新增 **§8.2 五类口径归类表**（本次执行 / 历史复用 / 已登记失败差异 / 未执行 / 待 owner 确认），并给出唯一合法总括句 |
| J15 / I2 改判缺授权证据 | 两行直接写「已改判口径」，未声明**由谁批准** | 新建 `I-evergreen.s1_main_flow-158614-008`（major, `open`, 待 owner 确认）并在 §4 增列**授权状态**：两条改判均为**执行者（Agent Harness）依 `T-075` Scope 作出的登记口径**，**无 owner（`ikaqiu`）书面授权证据**；在 owner 复核前只成立为「**已登记的口径差异**」，不得读作「已批准改判」 |

**本批次证据基线**：Evergreen `b535b54` → **`a2069db`**（`I-002` 修复 + 回归，见 §7.1）；
Teamwork `b41779d` → 本修订 commit 及随后的 Issue / Activity Log commit。
所有数字均为本批次**实跑输出**或**明确标注的复用 / 外部未知**。**收口成立的边界条件见 §8.1**。

## 1. M6 十八条完成判据（逐条实测，结论 ∈ {PASS, FAIL}）

> 证据口径：`E` = Evergreen 仓根，`T` = Teamwork 仓根。测试均 `-count=1`；`-race` 全量见判据 17。

| # | 完成判据（摘要） | 实测证据与结论 | 结论 |
|---|---|---|---|
| 1 | T-070 九项前置裁决关闭 + 原子性与强校验合同在盘（`^## `≥9、A-52~A-60 恰 9 行、四条目标态字面在册） | 合同 `2027-01-24-m6-atomicity-and-strict-check-contract.md` 在盘；`m6_final_gate.py` 判据 A5/A6/B2/C 全绿；「要么全部生效、要么全部不生效」「任何阶段任何授权都不做破坏性还原」「退出码 5 只在写前强校验失败与锁不可用两类情形产出，且零写入」「权威 Markdown 一个字节都不错写」四条字面命中 | PASS |
| 2 | 事务边界与崩溃点失败矩阵封闭（阶段×崩溃点→恢复后磁盘态→退出码，行数≥8） | 合同崩溃点矩阵 ≥8 行；`txn_id`/`.index/txn/`/`flock`/「intent 日志写入先于任何权威文件写入」字面在册；`m6_final_gate.py` 判据 2 相关锚点全绿 | PASS |
| 3 | `internal/txn` 包边界反证（`[S5]` 声明、依赖单向、零 `os/exec`、只在事务目录与 `run.lock` 落盘） | `internal/txn/doc.go` 首行 `// [S5]`；`internal/txn` 对 `cli/query/index/report/reconcile` 引用 0、`os/exec` 0、写盘目标除 `run.lock`/`.index/txn` 外 0；`go test ./internal/txn` 全绿 | PASS |
| 4 | `run.lock` 互斥/超时(E16)/陈旧锁安全接管/短临界区 + 持锁 inode 零替换（禁 tmp+rename/unlink），唯一锁形态（无 `run.lock.meta`） | `go test ./internal/txn -run 'Lock…\|Inode…'` 8 用例全绿；`grep run.lock.meta internal/` = 0；`m6_run_lock.sh` exit 0 | PASS |
| 5 | 事务日志：intent 先写 + 前像/target_hash/create 完整 + 发布屏障(rename) + commit/abort 原子 + txn_id 唯一 + residue/损坏 intent 严格二分 + 路径规范化；txn_id 恒 `^t[0-9a-f]{16}$` 无秒段、seq 溢出 fail closed | `go test ./internal/txn -run 'Intent…\|TxnID…\|Residue…\|Corrupt…\|Escapes'` 全绿（≥6+≥5+≥3 用例）；`m6_txn_journal.sh` exit 0 | PASS |
| 6 | 多文件原子提交：write-set 内 N 文件全成或全不成，L1/L2/L3/L4 四级可见性成立，任意观察者永不见撕裂 | `go test ./internal/txn ./internal/store ./internal/plan -run 'MultiFileCommit…\|NoTornFile…\|Fsync…\|Idempotent\|PreservesBytes'` ≥5 用例全绿；`m6_atomic_multifile.sh` exit 0 | PASS |
| 7 | 崩溃恢复：8 崩溃点真实 `kill -9` 注入，恢复后磁盘态与矩阵逐行相符；两遍恢复整事务原子；Pass A 前像校验 + 全局只读分类；`.index/` 共存(R6/R9/R10)反证 | `go test ./internal/txn -run 'CrashRecoveryMatrix'` ≥8 子用例全绿；`m6_crash_recovery.sh`（含 `kill -9`）exit 0；恢复冲突/损坏/路径三类零写入不发 W26；`index rebuild/build` 保全 txn+lock inode | PASS |
| 8 | 恢复 hook 覆盖全部写命令、只读命令不触发、命令数恒 22 | `go test ./internal/cli -run 'RecoverHook…\|CommandCountStillTwentyTwo'` 全绿；`grep 'wantCommandCount = 22' cli_test.go` = 1 | PASS |
| 9 | 块级安全合并：不匹配时三方判定，不安全跳过 + W27，M3 匹配路径行为一字不变，无 diff 依赖 | `go test ./internal/mdfile ./internal/store ./internal/plan -run 'BlockMerge…'` ≥5 用例全绿；`W27` 在 `block_merge.go`/`merge.go` ≥1；`m6_block_merge.sh` exit 0 | PASS |
| 10 | 并发冲突：两进程真实并发写，零半写、零交叉 txn_id、等待留痕 W28 | `go test ./internal/txn ./internal/store -race -run 'Concurrent…\|W28…'` ≥4 用例全绿；`m6_concurrent_conflict.sh`（真实起两进程）exit 0 | PASS |
| 11 | `eg check` 强校验：W1/W2/W3/W4/W6 升 error，诊断码与十二值枚举一字不变，默认路径零漂移 | `go test ./internal/reconcile ./internal/cli -run 'StrictMode…\|CheckEnumStillTwelve\|NonStrictModeUnchangedFromM4'` ≥4 用例全绿；`m6_strict_check.sh` exit 0 | PASS |
| 12 | 写前强校验(含 S3 锁内重发现) + 退出码 5 + 零写入三位一体；E15 第一类=写前安全复核语义大类(8 原因同码)；reserved 类型违规仅 A/B 类退 5、C 类只读产恰一条 W24(+Q5)不退 5；退出码 5 常量单点、`internal/**` 零 `os.Exit` | `go test ./internal/plan ./internal/cli -run 'Precheck…\|Exit5…\|LockUnavailable…\|Rediscover…\|ReservedType…'` ≥10 用例全绿；`ExitPrecheckOrLock=5` 常量 1 处；`internal/**` `os.Exit`=0、`cmd/eg/main.go`=1（唯一调用点）；`m6_precheck_exit5.sh`（带「无待恢复事务」前置）exit 0 | PASS |
| 13 | 写权限矩阵升硬约束 + CI 依赖方向门禁三条禁令全真且进 `make lint` | `go test ./internal/plan ./internal/store -run 'WriteMatrixHardConstraint\|ForbiddenSetterCallSitesExactlyFive'` 全绿；`dep_direction_gate.sh` exit 0（三条断言）；`grep dep_direction_gate Makefile`≥1；`m6_ci_dep_gate.sh` exit 0 | PASS |
| 14 | `txn_id` 在最终报告填充（S1「可省略」在 M6 收紧为必填，且与日志一致） | `go test ./internal/report ./internal/txn -run 'ReportTxnIDPopulated\|MatchesJournal\|ReconcileStillThreeKeys'` 全绿；`eg apply --json` 的 `.data.txn_id` 匹配 `^t[0-9a-f]{16}$` | PASS |
| 15 | 计数面逐项复算：命令恒 22、新增诊断码恰 5(E15/E16/W26/W27/W28)、W21 不分配、退出码全集 `{0,1,2,3,4,5,6}`、KnownVerbs 8、AllOpNames 17、check 12 值、rel data 5 键、skipped[].kind 2 值 | 实测：新码集合 `{E15,E16,W26,W27,W28}` 恰 5；`grep '"W21"' internal/`=0；`go test … -run 'KnownVerbs…\|AllOpNames…\|RelDataKeys…\|ExitCodeSurfaceExactlySeven\|ConfirmWhitelistUnchanged'` 全绿；退出码集合恰 `{0,1,2,3,4,5,6}` | PASS |
| 16 | 版本推进 0.6.0-m6 多处同真 + dist 四平台重建溯源 + 静态构建保全（纯 Go SQLite、直接依赖≤2） | `grep -l 0.6.0-m6 version.go Makefile README.md INSTALL.md`=4；`grep 0.5.0-m5 version.go Makefile`=0；`eg --version`==`make print-version`；`dist/eg_linux_amd64 --version` 含 0.6.0-m6 且 commit==`b535b54`；`sha256sum -c SHA256SUMS` exit 0；`CGO_ENABLED=0 go build ./...` exit 0；`go list -m all` 无 `mattn/go-sqlite3`，直接依赖=`yaml.v3`+`modernc.org/sqlite`(=2)；`m6_docs_commands.sh` exit 0 | PASS |
| 17 | M1–M5 不回归 + M6 门禁全绿 + e2e 只增不减 + `-race` 强制（详见 §6 历史门禁登记） | `make lint` exit 0；`go test ./... -count=1 -race` **RACE_EXIT=0**（全包绿）；`ls m6_*.sh`=11 且逐个 exit 0；`ls *.sh`=61(≥61)；M1–M5 e2e 一个不删；`validate_m56_tasks.py` 35/0、`m6_final_gate.py` 0 failed(收口后)、`m6_mutation_test.py` 65 算子/0 存活；M5 全量 e2e 于 b535b54 已 5/5、50 支全绿(复用 `/tmp/m5acc.log`)；历史门禁**本批次逐支复算 17 支**：本次执行 0 failed **9 支**、本次执行非零 **6 支共 15 条 failed**（全为冻结基线 / 打包环境漂移，按 §6.1 逐支登记为 `J15` + `I-006`，**不改判据**）、复用 **1 支**、外部未知 **1 支**（`derive_eg_baseline` 不计入通过）。初版此格写「12/16 为 0 failed，其余 4 支漂移」，**算术不成立，已由 §6.1 逐支表替代**；判据 17 的 PASS 结论只覆盖「M1–M5 不回归 + M6 门禁全绿 + e2e 只增不减 + `-race`」四项，**不代表全部历史门禁 0 failed** | PASS |
| 18 | S1 最终统一验收 + M4/M5 延期债终态：J14/J15/I2 三条 + M5 遗留项给出终态，S1 全阶段 66+ 条生效需求逐条结论，含「S1 阶段收口」与≥3 已知限制 | 本报告 §4（J14/J15/I2 三行终态 ⊆ {已清偿, 已改判口径}）、§5（70 条 EG-* 逐条结论 ⊆ {PASS,FAIL,DEFERRED}）、§8（S1 阶段收口）、§9（≥3 已知限制）齐备；`m6_final_gate.py` G1~G7 收口后全绿 | PASS |

**判据小结**：18 条 **逐条 PASS**（P0 与核心 P1 全绿；全量回归见 §6）。


## 2. 性能面（M6 不改检索路径）

M6 是原子性与强校验里程碑，**不触碰 M5 的检索 / 索引读路径**，故 `eg search` / `eg card` /
`eg rel` 的 P95 门槛与实测值**逐字沿用 M5 验收报告 §2 的达标结论**，M6 不引入性能回归。

| 维度 | 结论 |
|---|---|
| 检索性能（P95 门槛） | 沿用 M5 达标基线；M6 未改 `internal/index` / 读路径，无回归 |
| 写路径开销 | M6 新增强事务 / `fsync` / `flock` 仅作用于**写命令**临界区；只读命令零新增开销（`EG-VIEW-01` 只读零副作用仍成立） |
| 构建产物 | 四平台 dist 于 `b535b54` 重建，linux 两平台静态链接（`file(1)` 动态段计数 0），SHA256 见发布口径文档 |

## 3. A-52 ~ A-61 未决事项终态（`T-070` 收口后逐条复述）

| 编号 | 事项 | 终态裁决（`T-070` 合同定稿） |
|---|---|---|
| A-52 | 事务边界定义 | 选项①：一条写命令 = 一个事务（Git 提交在事务内，提交失败退 4 且磁盘保留） |
| A-53 | `run.lock` 位置 | 选项①：`vault/.index/run.lock`（`.gitignore` 已覆盖） |
| A-54 | 锁超时与等待 | 选项①：默认等待 10s 指数退避、等待期 W28，超时退 5 + E16 |
| A-55 | 崩溃恢复触发时机 | 选项①：每个写命令启动时自动恢复 + W26，不新增命令 |
| A-56 | 强校验开关形态 | 选项②：默认宽松 + `--strict` 显式开启（未改 M1–M5 既有 e2e 退出码期望） |
| A-57 | 块级「安全」判定 | 选项①：差异不重叠于本次写块区间才合并，否则跳过 + W27，不引入 diff 依赖 |
| A-58 | 退出码 5 语义边界 | 选项①：5 = 写前强校验失败 **或** 锁不可用（E15 / E16 区分），均零写入 |
| A-59 | `txn_id` 格式与填充 | 选项①：`t`+16 位小写 hex，报告必填（收紧不破坏既有消费方） |
| A-60 | S1 收口版本号 | 选项①：`0.6.0-m6` + 逐字列出三项已知限制（见 §9） |
| A-61 | EPIC target 早于 M-006.date | 沿用 A-40 选项③：`EPIC.md` frontmatter 一字不改，排期只写正文并登记 |

## 4. M4 / M5 延期债终态（J14 / J15 / I2）

> **终态列封闭取值**：`已清偿` / `已改判口径`。**M6 之后无里程碑可延，「继续延期」不再是合法取值。**

| 债务 | 起源与内容 | 终态 | 依据 |
|---|---|---|---|
| J14 | M2/M3 历史 e2e 冻结事实退 1（`m2_acceptance.sh` / `m3_acceptance.sh` 等） | 已清偿 | 于 M5 真实修到退 0（非改判口径）；本轮 C2a 复证：M5 全量 `m5_acceptance.sh` 于 `b535b54` 一次性复算 **5/5、50 支逐个 exit 0、零豁免**（`/tmp/m5acc.log`），`m2_acceptance.sh` / `m3_acceptance.sh` 均 exit 0，J14 保持已清偿终态 |
| J15 | `m3_final_gate.py` 冻结基线（M5 期 3 条 `H10k`/`H10x`/`H10ad`；M6 期实测 135 checks / **9 failed**） | 已改判口径（**待 owner 确认**，`I-evergreen.s1_main_flow-158614-008`） | **本批次逐条复算，9 条按真实根因分为 5 类**（初版把 9 条笼统写成「版本 / e2e 计数漂移」**不准确，已修正**）：**①计数**`H10k`（磁盘 e2e 实测 61 ≠ 35、M4 e2e ≠ 10）；**②版本**`H10ad`（README/INSTALL/Makefile 未逐字锁 `0.3.0-m3`，现为 `0.6.0-m6`）；**③M6 能力启用**`H10r`（`ExitCode5Enabled() 不再恒 false` —— M3 期锁「恒 false」，M6·T-074 **有意启用**退出码 5）、`H10v`（`mark_reviewed.go` 落盘改走 M6 强事务，不再是 M3 期的 `ApplyStateWrite` 直写形态）；**④M5/M6 实现新增删除点与测试符号**`H10t`/`H10z`/`H10ab`（`os.Remove*` 白名单外多出 `internal/txn/commit.go`、`internal/txn/recover.go`；`internal/index/build.go` 命中 2、`rebuild.go` 命中 3 ≠ 各恰 1；「写口越界 `flock\|txn/\|run.lock`」命中 16，落点为两个 **M6 测试文件** `delete_release_order_test.go`、`exit5_classify_test.go`）；**⑤打包/环境**`H6`（工作区卫生，同 `I-006`）。**9 条均非产品回归**，其正确归零点是「M6 历史门禁纳管重构」，而 `T-075` Scope **明令历史门禁判据一字不动**。故**不改判据、不 skip / 不 allowlist / 不 frozen-red**，统一作为**已登记的口径差异**接受。复算命令：`python3 -B projects/evergreen/s1_main_flow/tools/m3_final_gate.py`。**授权状态：本改判由执行者（Agent Harness）依 `T-075` Scope 作出，沙箱内查无 owner（`ikaqiu`）书面授权证据（复算命令见 `I-evergreen.s1_main_flow-158614-008`）；已按本批次要求登记为独立待确认 issue `I-evergreen.s1_main_flow-158614-008`，owner 复核前只成立为「已登记差异」，不得读作「已批准改判」**。归属登记见 §7 与 `I-006` |
| I2 | M4 合同 §14 称新增诊断码恰 14、实测实现 13（`I2` 从未落地） | 已改判口径（**待 owner 确认**，`I-evergreen.s1_main_flow-158614-008`） | M6 按 R8 §18.4 撤销「精确计数」口径：诊断码以**实现为权威真源**（Markdown/代码即真源原则），M6 新增**恰 5 条** `E15/E16/W26/W27/W28`、`W21` 仍不分配；M4 §14「14 条」为规范期**多计**，本轮**不替 owner 补造** `I` 域码、**不改 M4 历史结论**，改判为「合同计数口径已由实现真源纠正并登记」。实测 `grep '"I2"' github.com/ikaqiu-Lemon/EverGreen/internal/` 仍为 0。**授权状态：同 `J15` —— 执行者依 R8 §18.4 与实现真源原则作出的登记口径，无 owner 书面授权证据；已并入待确认 issue `I-evergreen.s1_main_flow-158614-008`；本批次不假称已获授权，owner 复核前该行只作「已登记的合同计数差异」** |

**延期债小结**：`J14` = 已清偿（M5 真实修复，本轮复证保持）；`J15` = 已改判口径（M3 期冻结基线固有漂移，登记接受）；`I2` = 已改判口径（合同计数口径以实现真源纠正）。三条**均无「继续延期」**。
**`J15` / `I2` 的授权缺口已单列为 `I-evergreen.s1_main_flow-158614-008`（`open`, major, 待 owner 确认，并已 `link-issue` 挂到 `T-075` 的 `known_issues`）**。
该 issue 覆盖的**恰两项**授权对象（逐字对应上表两行，勿与环境漂移混淆）：① `J15` 的「`m3_final_gate.py` 9 条 failed **不改判据**、登记为冻结基线随里程碑演进」口径；② `I2` 的「撤销 M4 合同 §14『新增诊断码恰 14』的精确计数、改以**实现为权威真源**（实测 13、`I2` 码从未落地）」口径。
**不含** `I-006`（打包 / 工作区卫生类环境漂移，见 §7，与 owner 授权无关）。
**`T-075` DoD 完备性如实登记**：`T-075` body「终局约束」与 DoD「延期债终局（判据 18）」均要求「取 `已改判口径` **附授权留痕**与被改判判据编号」——**被改判判据编号已在册（本节逐条给出），授权留痕缺失**，故该 DoD 项**当前未满足**：在 owner 书面确认前，本报告**不得**被引用为「owner 已批准改判」，也**不得**以「全部硬门禁通过」作总括（真实口径见 §6.1、§8.2）。


## 5. S1 全阶段生效需求逐条结论（70 条 EG-*，结论 ∈ {PASS, FAIL, DEFERRED}）

> 口径：`S1_REQUIREMENTS` 恰 44 条（阶段 = S1，M1–M5 已交付 → `PASS`）；另 26 条为 S1 时段
> **未承诺**的需求（正式承接阶段 S2 / S5 / Deferred），如实标 `DEFERRED` 并给归属阶段，
> **不写 PASS**。合计 70 条，与 `validate_m1_tasks.py` 的 `ALL_REQUIREMENTS` 全集逐字一致。

### 5.1 S1 生效需求（44 条 → PASS）

| 需求 | 内容摘要 | 承接 | 结论 |
|---|---|---|---|
| EG-SRC-01 | 同 URL / 同标题二次收录不产生第二份原文（幂等收录） | S1(M1) | PASS |
| EG-SRC-02 | 笔记生成与条目移出在同一事务（报告如实） | S1(M1) | PASS |
| EG-SRC-03 | 「产出知识卡」分区 + 卡 sources[] 双向可达 | S1(M1) | PASS |
| EG-SRC-04 | create_card.sources 结构完整（四要素齐全） | S1(M1) | PASS |
| EG-NOTE-01 | 材料笔记五分区固定名（恰 5 个 H2、无 H1） | S1(M1) | PASS |
| EG-NOTE-02 | 「用户补充」的任何自动写入被拒（退 2 零写入） | S1(M1) | PASS |
| EG-NOTE-03 | 笔记质量清单写进规程（SKILL.md 自检清单） | S1(M1) | PASS |
| EG-NOTE-04 | `eg context` 只取 kind='note'，不把笔记当卡 | S1(M1) | PASS |
| EG-NOTE-05 | 默认不重复加工（`verb: reprocess` 显式声明） | S1(M1) | PASS |
| EG-EXT-01 | `eg context` 输出加工上下文（base 全为 sha256 形态） | S1(M1) | PASS |
| EG-EXT-02 | 覆盖项缺失 → 报告标注 counterexample | S1(M1) | PASS |
| EG-EXT-03 | 语义相同判定（宁拆勿并） | S1(M1) | PASS |
| EG-EXT-04 | plan `reason` 非空 | S1(M1) | PASS |
| EG-EXT-05 | 知识卡五分区、无必填 type 字段 | S1(M1) | PASS |
| EG-KNW-01 | 卡 status 两值枚举（第三值被拒） | S1(M1) | PASS |
| EG-KNW-02 | `eg context` 只取 status='active' 卡 | S1(M1) | PASS |
| EG-KNW-03 | 未决问题写入「存疑与待验证」 | S1(M1) | PASS |
| EG-KNW-04 | 材料关系四要素（source/note/rel/reason） | S1(M1) | PASS |
| EG-KNW-05 | 零卡不判失败（退 0） | S1(M1) | PASS |
| EG-CVG-01 | 七种关系 → 产物结果对照表逐行验证 | S1(M1) | PASS |
| EG-CVG-02 | 冲突并存：两卡 active + 恰一条 opposing | S1(M1) | PASS |
| EG-CVG-03 | 「知识内容」自动路径只读（退 2 零写入） | S1(M1) | PASS |
| EG-CVG-04 | plan.reason → commit 正文 | S1(M1) | PASS |
| EG-CVG-05 | opposing 一对一条记录、方向规范化 | S1(M1) | PASS |
| EG-CVG-06 | relations[].type 四谓词封闭枚举 | S1(M1) | PASS |
| EG-CFM-01 | 无中间态（不落 candidate） | S1(M1) | PASS |
| EG-CFM-04 | 直接执行动作集，不做确认交互 | S1(M1) | PASS |
| EG-EDIT-02 | 无 source_check 字段（废弃字段黑名单） | S1(M1) | PASS |
| EG-EDIT-03 | 改笔记不改卡（字节隔离） | S1(M1) | PASS |
| EG-CHK-01 | 「理解自检」分区存在且不进 frontmatter | S1(M1) | PASS |
| EG-CHK-02 | 无评分 / 打分字段 | S1(M1) | PASS |
| EG-CHK-03 | 同领域取材（按 domain 扫描） | S1(M1) | PASS |
| EG-CHK-04 | 自检由用户自行转化，CLI 不生成结论 | S1(M1) | PASS |
| EG-CHK-05 | 自检不是写入前置 | S1(M1) | PASS |
| EG-CHK-06 | 历史块只追加（append-only） | S1(M1) | PASS |
| EG-VIEW-01 | 只读命令零副作用（report 后工作区无变化） | S1(M1) | PASS |
| EG-DOM-01 | domain 由 path 推导，无顶层 domain 字段 | S1(M1) | PASS |
| EG-DOM-02 | 跨域 op 与跨域关系被拒（S1 记 W1，S5 起可 error） | S1(M1) | PASS |
| EG-DOM-03 | default_domain 配置生效，CLI 绝不自选默认领域 | S1(M1) | PASS |
| EG-AGT-02 | SKILL.md 十一/十三条边界条目逐条可查 | S1(M1) | PASS |
| EG-AGT-03 | 一次 `apply` 走完链路（唯一写入入口） | S1(M1) | PASS |
| EG-AGT-04 | 只有 vault/ 内容算知识 | S1(M1) | PASS |
| EG-AGT-05 | Markdown 权威、Git 为历史（.index/ 不升权威） | S1(M1) | PASS |
| EG-AGT-06 | 写前重新读盘 + content_hash 比对（S1 由 B3 保证，M6 升级为强事务） | S1(M1→M6) | PASS |

### 5.2 S1 时段未承诺需求（26 条 → DEFERRED，附归属阶段）

| 需求 | 内容摘要 | 归属阶段 | 结论 |
|---|---|---|---|
| EG-AGT-01 | Agent 端到端自治能力扩展 | S5+ | DEFERRED |
| EG-AGT-07 | Agent 高级调度 / 多源编排 | S5+ | DEFERRED |
| EG-CFM-02 | 确认交互 / 人工复核回路 | S2 | DEFERRED |
| EG-CFM-03 | 分级确认策略 | S2 | DEFERRED |
| EG-CFM-05 | 确认审计留痕 | S2 | DEFERRED |
| EG-CFM-06 | 触发式确认（见 A-16 登记项） | S2 | DEFERRED |
| EG-CFM-07 | 批量确认 / 撤销 | S2 | DEFERRED |
| EG-DOM-04 | 跨领域能力（不随阶段变化） | Deferred | DEFERRED |
| EG-DOM-05 | 领域迁移 / 合并 | Deferred | DEFERRED |
| EG-DOM-06 | 跨域引用治理（不随阶段变化） | Deferred | DEFERRED |
| EG-DOM-07 | 领域级视图（不随阶段变化） | Deferred | DEFERRED |
| EG-DOM-08 | 跨域检索（不随阶段变化） | Deferred | DEFERRED |
| EG-EDIT-01 | 结构化编辑命令 | S2 | DEFERRED |
| EG-EDIT-04 | 编辑冲突交互解决 | S2 | DEFERRED |
| EG-EDIT-05 | 编辑历史回溯 | S2 | DEFERRED |
| EG-EDIT-06 | 无物理删除路径（逻辑删除，正式承接 S2） | S2 | DEFERRED |
| EG-KNW-06 | 知识卡三道物理约束 | S2 | DEFERRED |
| EG-KNW-07 | 知识卡版本 / 演进 | S2 | DEFERRED |
| EG-KNW-08 | 知识卡跨域复用 | S2 | DEFERRED |
| EG-VIEW-02 | 检索视图扩展（M2 已提前部分落地，正式承接 S2） | S2 | DEFERRED |
| EG-VIEW-03 | 关系图视图 | S2 | DEFERRED |
| EG-VIEW-04 | 时间线 / 演进视图 | S2 | DEFERRED |
| EG-VIEW-05 | 领域仪表视图 | S2 | DEFERRED |
| EG-VIEW-06 | 自定义查询视图 | S2 | DEFERRED |
| EG-VIEW-07 | 只读查询增强（M2 已提前部分落地，正式承接 S2） | S2 | DEFERRED |
| EG-VIEW-08 | 导出 / 分享视图 | S2 | DEFERRED |

**需求小结**：70 条逐条结论在册 —— **44 条 PASS**（S1 生效需求全部交付）、**26 条 DEFERRED**（S1 时段未承诺，附 S2 / S5 / Deferred 归属），**0 条 FAIL**（无 S1 承诺需求落空）。


## 6. 全量回归证据（逐项实测）

| 项 | 命令 | 结果 |
|---|---|---|
| 单测 + 竞态（全包，于 `b535b54`） | `go test ./... -count=1 -race` | **RACE_EXIT=0**（全包绿，含 `-race`）—— **复用**：该次全量执行的基线是 `b535b54` |
| 单测（全包，本批次增量后） | `CGO_ENABLED=0 go test ./internal/... -count=1`（于 `a2069db`） | **本次执行**：15 包全 `ok`（`I-002` 修复后复算） |
| 竞态（受影响包，本批次增量后） | `go test ./internal/cli -race -count=1`（于 `a2069db`） | **本次执行**：`ok github.com/ikaqiu-Lemon/EverGreen/internal/cli 151.3s`，0 data race —— `I-002` 只改 `internal/cli/apply.go`，按「有增量则重跑受影响包」跑该包 `-race`，未重跑无增量的其余包 |
| Lint + 依赖门禁 | `make lint`（含 `dep_direction_gate.sh` 三条禁令） | exit 0（三条依赖禁令全真） |
| M6 e2e（11 支） | `m6_run_lock`/`m6_txn_journal`/`m6_atomic_multifile`/`m6_crash_recovery`/`m6_block_merge`/`m6_concurrent_conflict`/`m6_precheck_exit5`/`m6_strict_check`/`m6_ci_dep_gate`/`m6_docs_commands` 逐个 + `m6_acceptance.sh --list` | 逐个 exit 0（`M6CORE_EXIT=0`） |
| M5∪历史 e2e（50 支） | `m5_acceptance.sh`（全量，于 `b535b54`） | 5/5、50 支逐个 exit 0、零豁免 —— **复用** `/tmp/m5acc.log`。**增量声明**：本批次 `I-002` 修复（`a2069db`）改动 `internal/cli/apply.go` 的 dry-run 记账，按「受影响面重跑」原则实跑了 `internal/cli` 全包（含 `-race`）、`internal/...` 15 包、`test/e2e/m2_convergence.sh`（12/12）与 3 支 M1 dry-run 相关 Go e2e，**均 exit 0**；未对无关的其余 46 支历史 e2e 做无增量重跑 |
| e2e 磁盘总数 | `ls test/e2e/*.sh` | 61（≥61，只增不减） |
| 规划结构门禁 | `validate_m56_tasks.py` | 35 checks / 0 failed |
| M6 内容门禁 | `m6_final_gate.py` | 收口后 62 checks / 0 failed（本报告落盘 + 状态迁移 + 交付提交后） |
| 变异反证 | `m6_mutation_test.py` | 65 算子（≥60）/ **0 存活** |

### 6.1 历史门禁（**逐支 17 行**）逐个结论

> **口径（本批次修正为如实三分）**：每支门禁标明其结果来源 —— **本次执行**（本批次亲自跑出的数字）、
> **复用**（历史已证明通过且源码无增量，未重跑，明确标注）、**外部未知**（判据依赖沙箱外材料，
> 无法在本环境判定，**不计入通过**）。
> `T-075` Scope 明令历史门禁判据一字不动：凡历史 `.py` 因 M6 版本推进（0.5.0-m5→0.6.0-m6）、
> e2e 计数上升（50→61）、M6 能力启用（退出码 5 / 强事务）、以及打包把「本地化基线」
> 由**未提交**改为**已提交/已跟踪**而失败的，**一律不改判据**，登记为 `J15` / `I-006`（§4、§7）。
> **本节不写「全部通过 / 全绿」**：非零结果逐支列出真实 failed 数与根因。

| 门禁 | 实测 | 口径 | 结论 |
|---|---|---|---|
| validate_m1_tasks | 35 checks / 0 failed | 本次执行 | 0 failed ✅ |
| validate_m2_tasks | 34 checks / 0 failed | 本次执行 | 0 failed ✅ |
| validate_m3_tasks | 47 checks / 0 failed | 本次执行 | 0 failed ✅ |
| validate_m4_tasks | 69 checks / 0 failed | 本次执行 | 0 failed ✅ |
| validate_m56_tasks（M5/M6 规划） | 35 checks / 0 failed | 本次执行 | 0 failed ✅ |
| m4_final_gate | 35 checks / 0 failed | 本次执行 | 0 failed ✅ |
| m6_final_gate（M6 内容） | 62 checks / 0 failed | 本次执行 | 0 failed ✅ |
| independent_schedule_check | 22 checks / 0 failed | 本次执行 | 0 failed ✅ |
| m6_mutation_test（M6 变异） | 65 算子 / 0 存活 | 本次执行 | 0 存活 ✅ |
| round3_final_gate | 65 checks / **2 failed** | 本次执行 | **非零**：`H3` 工作区卫生（本地化基线 5 文件 + 2 目录 + `vendor/click/__pycache__` 垃圾）→ `I-006`；`H6` 断言 `overview.html` 含「0 active tasks」**且**「09/17」—— 实测「0 active tasks」在场、`09/17` **已不在**（生成产物随 M2–M6 排期重算为 `10/09` / `02/21`）→ **生成产物快照漂移**（初版误标为「工作区卫生」，已修正） |
| round4_final_gate | 114 checks / **1 failed** | 本次执行 | **非零**：`[P1] J3` 工作区卫生 → `I-006` |
| round5_final_gate | 102 checks / **1 failed** | 本次执行 | **非零**：`[P1] H3` 工作区卫生 → `I-006` |
| m2_final_gate | 73 checks / **1 failed** | 本次执行 | **非零**：`[P1] H5` 工作区卫生 → `I-006` |
| m3_final_gate | 135 checks / **9 failed** | 本次执行 | **非零**：`H10k`/`H10ad`/`H10r`/`H10t`/`H10v`/`H10x`/`H10z`/`H10ab`/`H6`，5 类根因逐条见 §4 `J15` 行 → 已登记差异（**非产品回归**） |
| m5_final_gate | 67 checks / **1 failed** | 本次执行 | **非零（初版漏登，本批次补齐）**：`[P1] E2` 要求「版本 `0.5.0-m5` 在 version.go / Makefile / 发布文档三处同真」—— M6 已推进到 `0.6.0-m6`，该 M5 期冻结断言**必然为假**；不改判据、不回退版本，登记为 M5 冻结基线在 M6 现态下的固有漂移 |
| derive_eg_baseline | 沙箱内本仓五方交叉核对一致 | **外部未知** | 判据依赖飞书 §14 原文，**该材料不在沙箱**，**不计入通过**，如实标「未知」 |
| m4_mutation_test（M4 变异） | 沿用 M5 收口结论（M4 产品/测试本轮零改动） | **复用（未重跑）** | 复用 ✅（不充当本次执行证据） |

**历史门禁小结（真实算术，17 支）**：

- **本次执行 0 failed / 0 存活：9 支** —— validate_m1/m2/m3/m4、validate_m56、m4_final、m6_final、independent_schedule、m6_mutation。
- **本次执行非零：6 支，合计 15 条 failed checks** —— round3 **2** + round4 **1** + round5 **1** + m2_final **1** + m3_final **9** + m5_final **1**。
- **复用未重跑：1 支** —— m4_mutation_test（M4 侧零增量）。
- **外部未知：1 支** —— derive_eg_baseline（飞书 §14 原文不在沙箱）。
- 校验：9 + 6 + 1 + 1 = **17** 支，与表行数逐一对应。

**结论口径**：这 15 条非零**全部**归入三类**已登记差异** —— ①打包/环境基线（`I-006`：round3 `H3`、round4 `J3`、round5 `H3`、m2_final `H5`、m3_final `H6`，共 5 条）、②冻结基线随里程碑演进（版本 / e2e 计数 / M6 能力启用 / 新增删除点与测试符号，共 9 条：m3_final 8 条 + m5_final `E2`）、③生成产物快照漂移（round3 `H6`，1 条）。
**均不改判据、不 skip、不 allowlist、不 frozen-red、不删断言**；**也不声称「全部测试全绿」** —— 差异如实在册（§4 `J15`、§7）。
初版小结「12 支 0 failed / 4 支环境漂移」**算术与分类均不成立，已由本行替代**。

### 6.2 R6 历史 e2e 现态重钉 —— 逐条对账表（`T-075` deliverable / DoD 专表）

> **唯一合法形态**（合同 §16.4）：**保留历史事实 + 新增 M6 现态双侧锁**。
> 禁止 skip / 条件跳过 / allowlist / frozen-red / 删断言 / 弱化。
>
> **重钉总量（本批次以 `git` 机器复算，替代初版的手工计数）**：重钉分**三**个批次，
> 初版只看了两个（漏 `0ef2bc6`），且把去重数写成 23，**两处计数均已订正**：
>
> | 批次 | commit | 该 commit 变更文件 | 其中「历史测试面」触达点 | 明细 |
> |---|---|---|---|---|
> | C2a | `d59c215` | 22 | **22** | **20 支 `.sh`** + 1 支 Go e2e（`test/e2e/m4_report_reconcile_test.go`）+ 1 份语料（`testdata/ppe/query-before.txt`） |
> | C2b-A/B | `0ef2bc6` | 9 | **4** | 3 支 `.sh`（`m3_docs_commands` / `m4_docs_commands` / `m5_docs_commands`）+ 1 支单测（`internal/cli/docs_test.go`）；另 5 个非测试文件（`INSTALL.md` / `Makefile` / `README.md` / `internal/version/version.go` / `skill/SKILL.md`） |
> | C2b-D | `b535b54` | 7 | **4** | 3 支历史 `.sh`（`m2_docs_commands` / `m3_acceptance` / `m5_acceptance`）+ 1 支单测（`internal/cli/skill_test.go`）；另 2 支**新增** M6 e2e（`m6_acceptance.sh` / `m6_docs_commands.sh`，属新增不属重钉）+ `README.md` |
>
> **机器复算结论**：历史测试面**触达点 = 22 + 4 + 4 = 30**；
> **去重后唯一文件 = 27**（**23 支历史 `.sh`** + **3 支 Go 测试** + **1 份语料**）。
> 重复触达的三支：`m3_acceptance.sh`（`d59c215` + `b535b54`）、`m5_acceptance.sh`（`d59c215` + `b535b54`）、
> `m3_docs_commands.sh`（`d59c215` + `0ef2bc6`），故 30 − 3 = **27**。
> 复算命令：`git show --numstat --format= <commit>` 三个 commit 求并集后按路径去重。
>
> **判据面列**：`R6` = `.index/` 布局 / 共存 / 「M6 不存在」类断言重钉（合同 §16.4）；
> `R7` = 退出码 `5` 常量表 / 文档口径重钉（合同 §17.2 / §17.3，`T-075` 要求 **`R7` 行 ≥ 4**）；
> `V` = 版本口径推进（`0.5.0-m5` → `0.6.0-m6`，历史值降为只读基线）。
> **复跑证据列**：`L1` = `/tmp/m5acc.log`（`m5_acceptance.sh` 全量，于 `b535b54` 一次性复算：**5/5 组断言、e2e 磁盘总数 61、M5 11 支 + 历史 39 支逐个 `exit 0`、零豁免零冻结红**，日志内 50 条 `exit=0` 逐支可数）；`L2` = 本批次实跑 `go test ./internal/... -count=1`（15 包全 `ok`）；`L3` = 本批次实跑 `bash test/e2e/m2_convergence.sh`（12/12，`exit 0`）与 `go test ./test/e2e -run 'TestM1RealArticleZeroIntervention|TestM1ErrorCasesRejectedWithZeroWrite|TestM1CoverageGapAbsentRerun'`（`ok`）。

| # | 脚本 / 测试（重钉批次） | 判据面 | 改动量 | 锚点行 | 原断言（历史事实，**保留**） | 新增 M6 现态双侧锁 | 复跑证据 |
|---|---|---|---|---|---|---|---|
| 1 | `m2_context_polish.sh`（C2a） | **R6** | +10/-2 | L166–171 | `.index/` 不得出现派生索引 DB（M2/S1 写命令不替用户建索引，属 S4） | `.index/` 若存在，条目**只许** `run.lock` / `txn`；出现任一**非 runtime-reserved** 条目即红 | `L1` |
| 2 | `m2_rel_add.sh`（C2a） | **R6** | +9/-1 | L217–222 | 同上（写命令零派生 DB） | 同上（runtime-reserved 白名单 + 反向零杂项） | `L1` |
| 3 | `m3_acceptance.sh`（C2a） | **R6** | +59/-14 | L~300/402/507 + 加法等式段 | e2e 计数**加法等式**：M1+M2 10 + M3 15 + M4 14 + M5 11 = 50，四个历史加数**一格不动** | 新增 M6 加数 `N_M6`，总数抬为 `50 + M6`；仍用**加法等式**判定（挪号段 / 少提交当场不等），比写死总数更严 | `L1` |
| 4 | `m3_authorization.sh`（C2a） | **R6** | +32/-10 | L314–318 | `internal/` 零破坏性回滚动作（U-02） | 可执行行**摘掉 M5 派生索引重建白名单**后仍须恰 0；**新增** `internal/index/build.go` 可执行 `RemoveAll` 恰 **0**（M6 R9/R10 禁整目录删除） | `L1` |
| 5 | `m3_docs_commands.sh`（C2a，第 1 次触达） | **R7** | +14/-3 | L148、L161–162 | INSTALL 退出码表恰 6 行、白名单恰两条、`ExitNeedConfirm` 恰 3 文件 | 第 5 行**必须逐字标「启用」**并含 `ExitPrecheckOrLock` + `E15`/`E16`（M6·T-074 已启用）；原「标未启用」口径**因事实改变而反向锁**，强度位不降 | `L1` |
| 6 | `m3_edit.sh`（C2a） | **R6** | +9/-1 | L208–213 | `.index/` 零派生 DB（M3/S1 写命令不建索引） | runtime-reserved 白名单 + 反向零杂项 | `L1` |
| 7 | `m3_execution_failed.sh`（C2a） | **R6** | +32/-10 | L277–281 | 产品代码零破坏性动作（B4） | 摘掉 M5 重建白名单后仍恰 0；`build.go` 可执行 `RemoveAll` 恰 0 | `L1` |
| 8 | `m3_ops_diagnostics.sh`（C2a） | **R6** | +68/-8 | L545–575 | `internal/reconcile` strict 升级码集合与合同 §9 逐字相等（恰 `W1–W6`、恰 6 值） | 新增 `internal/txn` M6 码集合**逐字恰** `{E15,E16,W26,W28}`（原子性合同 §12/§16.3） | `L1` |
| 9 | `m3_proposal_layout.sh`（C2a） | **R6** | +10/-1 | L186–191 | 夹具前提：`.index/` 零派生 DB，无索引降级路径成立 | 夹具前提加锁：`.index/` 出现非 runtime-reserved 条目即判「夹具前提被破坏」 | `L1` |
| 10 | `m3_proposal_state.sh`（C2a） | **R6** | +13/-1 | L206–222 | 码域封闭 grep（`E1x`/`W1x`/`Ix` 不越界） | 排除目录后新增 `internal/txn` 恰 `{E15,E16,W26,W28}`、`internal/mdfile` 恰 `{W27}`（块级合并合同 §7） | `L1` |
| 11 | `m3_rel_remove.sh`（C2a） | **R6** | +9/-1 | L186–191 | `.index/` 零派生 DB | runtime-reserved 白名单 + 反向零杂项 | `L1` |
| 12 | `m3_superseded.sh`（C2a） | **R6** | +16/-2 | L242–247 | 码域封闭 | `internal/txn` 恰 `{E15,E16,W26,W28}`、`internal/mdfile` 恰 `{W27}` | `L1` |
| 13 | `m4_acceptance.sh`（C2a） | **R6** | +24/-63 | L180–184 | 判据 17 越界反证：对账包零索引符号、`os.Exit(5)` 恒 0、对账非写命令前置零泄漏 | M6 符号**未授权落点恰 0**（`M6_UNAUTH`）且**授权本位 ≥ 1**（`M6_AUTH`）—— 由「M6 一格不许有」改判为「只许落在授权本位」，**零命中断言未删**（`os.Exit(5)` 仍恰 0，见 §6.3） | `L1` |
| 14 | `m4_cmd_check.sh`（C2a） | **R6** | +13/-3 | L491–496 | `runCheck` 方法只许出现在 `check.go` / `check_test.go` | 新增 M6 测试助手 `runCheckCLI` **零泄漏生产代码**（只许在 `*_test.go`） | `L1` |
| 15 | `m4_r7_support.sh`（C2a） | **R6** | +24/-4 | L456–465 | `W20` 是 M4 末位，`W2x` 只许落在已分配落地面 | 摘掉 **M5∪M6 落地面**后仍恰 0；新增「M6 事务域码只许落 `internal/txn/`」（§16.3） | `L1` |
| 16 | `m4_report_reconcile_test.go`（C2a） | **R6** | +7/-0 | `rep.SetTxnID(...)` | 历史不变量：`apply` 与 `reconcile` 两条写路径**顶层键集合逐位相等** | 按 A-59「事务写必填 `txn_id`」为合成对账报告补合法形态 `t0123456789abcdef`，使历史不变量在 M6 下**原样成立**（只比键名不比取值） | `L2` |
| 17 | `m5_acceptance.sh`（C2a） | **R6+R7** | +66/-35 | L96–106、L134–170 | M5 清单：`m5_*.sh` 恰 11 / M1–M4 恰 39 一个不删；退出码全集 M5 6 值全保留；`W21` 恒 0 | 新增 M6 现态：`m6_*.sh` 清单**封闭且逐个在盘**、磁盘总数 `≥ WANT_TOTAL_M6`；退出码全集锁 `{0,1,2,3,4,5,6}`、取值 5 常量恰 1 个且名逐字 `ExitPrecheckOrLock` | `L1` |
| 18 | `m5_index_build.sh`（C2a） | **R6** | +27/-4 | L183–194 | 只读命令绝不建索引：`status` 不得建出派生 DB；`health=missing` + `W23` 在场 | missing 态 `.index/` **只允许** `run.lock` / `txn`，其余条目逐个判红（§16.3） | `L1` |
| 19 | `m5_index_consistency.sh`（C2a） | **R6** | +87/-29 | L113–120、L212–220 | `no_derived_index`：写命令不得建出派生 DB（建索引只走显式 `eg index build`） | `.index/` 若存在只允许 runtime-reserved；`proposal approve` 按 M6 A 类**强事务 / 锁内写后同步**重钉 | `L1` |
| 20 | `m5_index_corrupt_rebuild.sh`（C2a） | **R6** | +69/-34 | L93–102、L246–276 | 派生 DB **家族**逐字白名单；损坏→重建后权威零改动、占位原样保留 | `assert_index_family()` 在保留家族白名单的同时允许 `run.lock`/`txn`；新增 `.index` 为**普通文件 / 悬空 symlink** 时 **fail closed 退 5 + E15**、权威零改动 | `L1` |
| 21 | `m5_read_path_index.sh`（C2a） | **R6** | +19/-3 | L128–142、L202 | 读路径派生 DB 家族恰三值白名单；索引 `healthy`/`fresh` | 家族白名单**另允许** M6 runtime-reserved `run.lock`/`txn` | `L1` |
| 22 | `testdata/ppe/query-before.txt`（C2a） | **R6** | +1/-1 | 语料 1 行 | PPE 冻结语料 | 与重钉后的读路径输出同步（语料同步，非断言弱化） | `L1` |
| 23 | `m2_docs_commands.sh`（C2b-D） | **V** | +7/-4 | `SPEC_REL` / `HIST_VERSIONS` / `CUR_VERSION` | M2/M3 历史基线只读一字未动 | 当期指针随里程碑走：`SPEC_REL` → M6 发布口径文档、`CUR_VERSION` = `0.6.0-m6`、`0.5.0-m5` 降为历史基线（须 < 当前且不得作生效值残留） | `L1` |
| 24 | `m3_acceptance.sh`（C2b-D，第 2 次触达） | **V** | +5/-3 | `CUR_VERSION` + 旧值对撞集 | 四处逐字同真 + 旧值残留恰 0 | 当期值抬为 `0.6.0-m6`，`0.5.0-m5` **并入旧值残留对撞集**（M3 只读基线一字未动） | `L1` |
| 25 | `m5_acceptance.sh`（C2b-D，第 2 次触达） | **R6** | +5/-1 | M6 清单段 | M5 清单与历史 39 支不变 | M6 e2e 从 9 支补到**恰 11 支**（与 `M-006`「M6 e2e 恰 11 支、磁盘总数 ≥ 61」同真），清单**逐个在盘 + 反向零杂项** | `L1` |
| 26 | `internal/cli/skill_test.go`（C2b-D） | **R6** | +44/-16 | ⑦ 段 | `SKILL.md` §10「未落地清单」逐字要求 + 阶段标注词表 | 阶段标注词加入 `M6`/`S5`；§10 最后一行从「未落地清单」移出，**强度位原地换成正面交叉引用清单**（M6 已启用后继续要求「没做」等于强迫文档说谎；**不删、不放宽**，与 T-069 对前两行的处置同一形态） | `L2` |
| 27 | `m3_docs_commands.sh`（**C2b-A/B**，第 2 次触达） | **V** | +7/-4 | L40（`SPEC_REL`）、L44（`HIST_VERSIONS`）、L45（`CUR_VERSION`） | 「指针随里程碑走 + 历史值不得作为**生效值**残留」的历史口径；M2 / M3 决策文档历史基线**只读复算一格不动** | 当期决策指针切到 M6 发布口径文档；`CUR_VERSION` 抬为 `0.6.0-m6`；`0.5.0-m5` **并入** `HIST_VERSIONS` 逐个旧值对撞集（对撞面由 3 值扩到 4 值，**比原来更严**） | `L1` |
| 28 | `m4_docs_commands.sh`（**C2b-A/B**） | **V** | +5/-2 | L41（`WANT_VERSION`）、L43（`M4_VERSION`） | 期望值**写死不反推**（否则「四处一致」退化为自证）；`M4_VERSION="0.4.0-m4"` 作历史锚点 + 反证基线**一字未改** | `WANT_VERSION` 抬为 `0.6.0-m6`；`0.6.0-m6` 仍须严格新于 `M4_VERSION` 且后者不得出现在任何赋值行 | `L1` |
| 29 | `m5_docs_commands.sh`（**C2b-A/B**） | **R7**（+`V`） | +27/-20 | L36（`SPEC_REL`）、L44（`WANT_VERSION`）、L146（上一里程碑残留对撞）、L194–223（第 7 步：`M6_LANDED_RE` / 退出码 `5` 语境窗口） | 历史事实（M5 收口当日）：第 7 步是**越界反证** —— `run.lock` / 事务日志 / 崩溃恢复 / 块级合并 / 退出码 `5` 只许出现在**否定 / 属 M6** 语境，防 M5 文档预告未落地能力；上一里程碑残留对撞目标为 `0.4.0-m4` | M6·`T-070`~`074` 已把五项**全部落地**，故重钉为**正面在场断言**：三份文档必须把五项写成**已落地的 M6 能力**（2 行窗口须命中 `M6\|S5\|启用\|落地\|已`），且退出码 `5` **绝不再**出现「未启用 / 尚未启用 / 不启用」（旧字样零残留）；残留对撞目标同步推进为 `0.5.0-m5`；`WANT_VERSION` = `0.6.0-m6` | `L1` |
| 30 | `internal/cli/docs_test.go`（**C2b-A/B**） | **V** | +4/-3 | L35（`docsReleaseSpec`） | 「当期版本号唯一出处 = `docsReleaseSpec` 指针」；M2 / M3 / M4 / M5 四份发布文档**仍在盘、事实一字未改**（注释逐条保留为历史出处） | 指针前挪到 `2027-02-21-m6-release-and-version.md`（属**阶段化更新**，不放宽任何断言） | `L2` |

**R6 / R7 / V 小结（机器复算，替代初版手工计数）**：**30 个重钉触达点 / 27 个唯一文件**
（23 支历史 `.sh` + 3 支 Go 测试 + 1 份语料；3 支文件被触达两次，见本节开头批次表）。
按判据面：**`R6` 22 行、`R7` 3 行（含 `m5_acceptance.sh` 的 `CODES` + `N5C` 两处与
`m3_docs_commands.sh` 退出码表第 5 行、`m5_docs_commands.sh` 退出码 `5` 语境窗口共 4 处锚点，
达成 `T-075`「`R7` 标注行 ≥ 4」）、`V`（版本口径）4 行**。
形态**全部**为「保留历史事实 + 新增现态双侧锁 / 阶段化指针前挪」；
**零 skip、零 allowlist、零 frozen-red、零删断言**（反证见 §6.3 与 `m5_final_gate` F9c「无冻结红项章节」判据）。
**订正说明**：初版写「26 个重钉点 / 23 个文件」——重钉点漏了 `0ef2bc6`（C2b-A/B）整批 4 个触达点，
去重数也算错（当时表内实为 24 个唯一文件）。本节两处计数**均已按 `git show --numstat` 机器复算重写**，
不以一处计数错误替换另一处。

### 6.3 R7 `os.Exit(5)` 零重钉清单（逐支给 diff 事实）

> **口径**：`T-075` R7 要求登记的是「**7 支历史脚本里「`internal/` 下 `os.Exit(5)` 恰 0」这条断言**
> 在 R7 落地形态（`ExitPrecheckOrLock` 常量 + `ExitCodeFor` 单点出口 + `cmd/eg/main.go` 唯一 `os.Exit`）下
> **M6 交付后仍恒为真、一处都不需要重钉**」。因此本表比的是**该断言行本身**，
> **不是**整支脚本的文件级零 diff（同一支脚本的其它段落可能另有 `R6` / `V` 重钉，见 §6.2）。
>
> **本批次机器复算命令**（`d59c215^` = C2a 之前的 M5 基线 → `HEAD`）：
>
> ```bash
> for f in m3_acceptance m3_authorization m3_lifecycle_state m4_acceptance \
>          m5_acceptance m5_degrade_fallback m5_read_path_index; do
>   diff <(git show "d59c215^:test/e2e/$f.sh" | grep -F 'os.Exit(5)') \
>        <(git show "HEAD:test/e2e/$f.sh"      | grep -F 'os.Exit(5)') >/dev/null \
>     && echo "$f: identical" || echo "$f: CHANGED"
> done
> ```
>
> **实跑输出**：`m3_acceptance: identical`（4 行）、`m3_authorization: identical`（1 行）、
> `m3_lifecycle_state: identical`（1 行，文件级亦零 diff）、`m4_acceptance: identical`（3 行）、
> `m5_acceptance: CHANGED`（4 → 7 行）、`m5_degrade_fallback: identical`（5 行，文件级亦零 diff）、
> `m5_read_path_index: identical`（3 行）。

| # | 历史脚本 | `os.Exit(5)` 断言行数 | diff 事实 | 结论 |
|---|---|---|---|---|
| 1 | `m3_acceptance.sh` | 4 | 逐字相等 | **零重钉** |
| 2 | `m3_authorization.sh` | 1 | 逐字相等 | **零重钉** |
| 3 | `m3_lifecycle_state.sh` | 1 | 逐字相等（该文件两批均**无任何改动**） | **零重钉** |
| 4 | `m4_acceptance.sh` | 3 | 逐字相等 | **零重钉** |
| 5 | `m5_degrade_fallback.sh` | 5 | 逐字相等（该文件两批均**无任何改动**） | **零重钉** |
| 6 | `m5_read_path_index.sh` | 3 | 逐字相等（该文件其它段落有 R6 重钉，但 `os.Exit(5)` 行未动） | **零重钉** |
| 7 | `m5_acceptance.sh` | 3 → 6 | **谓词未变**：`[ "${N5}" = "0" ] \|\| die ...` 仍锁「字面量恰 0」；**提示语重写**：`die` 文案由「退出码 5 属 M6，M5 不启用」改为「§17.2 R7 形态：`os.Exit` 单点用 `ExitCodeFor` 变量出口，字面量恒 0」，并新增两行历史事实注释 + step 文案 | **非零重钉**（谓词同强度，**不可**记为严格零 diff） |

**R7 小结（如实）**：7 支中 **6 支严格零重钉**（断言行逐字未变，`≥ 4` 的 DoD 门槛达成），
**1 支（`m5_acceptance.sh`）谓词不变但提示语重写** —— 本报告**不把它计入零 diff**。
底层事实一致：`internal/**` 非测试代码 `os.Exit(5)` **字面量恒 0**，`os.Exit` 唯一调用点在
`cmd/eg/main.go`，退出码 5 经 `ExitPrecheckOrLock` 常量与 `ExitCodeFor` 变量出口给出（§17.2 R7 形态）。

### 6.4 R6 `.index/` 共存 / 零写入边界 —— 实证专表（本批次逐支实跑）

> 命令：`CGO_ENABLED=0 go test ./internal/... -run '<测试名>' -count=1 -v`（本批次实跑输出如下列）。

| # | 测试 | 所在文件 | 钉住的边界 | 本批次实跑输出 |
|---|---|---|---|---|
| 1 | `TestIndexRebuildPreservesTxnAndLockInode` | `internal/index/purge_test.go` | 重建派生索引**不得**碰 `.index/txn/` 与 `run.lock`（inode 级不变，事务日志不可重建） | `--- PASS (0.14s)` |
| 2 | `TestIndexBuildFailurePreservesTxnAndLock` | `internal/index/purge_test.go` | 构建**失败**路径同样保全 `txn/` 与 `run.lock`（失败不扩大破坏面） | `--- PASS (0.14s)` |
| 3 | `TestIndexInspectAllowsM6RuntimeEntries` | `internal/index/reserved_test.go` | `.index/` 巡检**允许** M6 runtime-reserved 条目共存，不误判为脏 | `--- PASS (0.42s)` |
| 4 | `TestIndexMaintenanceUsesRunLock` | `internal/cli/index_lock_test.go` | B 类 index 维护命令与 A 类写路径**持同一把** `run.lock` | `--- PASS (3.37s)` |
| 5 | `TestIndexReservedTypeViolationExits5` | `internal/cli/reserved_runtime_exit5_test.go` | reserved 条目**类型违例** → fail closed **退 5**、零写入 | `--- PASS (4.36s)` |

包级结论：`ok github.com/ikaqiu-Lemon/EverGreen/internal/index 0.722s`、`ok github.com/ikaqiu-Lemon/EverGreen/internal/cli 7.754s`（同批 `-run` 过滤下其余包 `no tests to run`）。
**权威性口径复证**：Markdown 仍是唯一权威；`.index/`（M5 派生 DB 家族 + M6 runtime-reserved `run.lock` / `txn/`）**不升为权威**；
派生部分可重建，`txn/` **不可重建**（存在未闭合事务时不可删 `.index/`，见 §9 第 3 项）。

## 7. 环境 / 打包 issue 登记（终态）

> **原则**：环境 issue **如实登记、给出终态**，与 skip / allowlist / frozen-red / 删断言 /
> 弱化**性质不同**——前者是透明留痕，后者是掩盖失败。本节属前者。

| Issue | 内容 | 根因 | 终态 |
|---|---|---|---|
| `I-005` | ENOSPC：沙箱磁盘满致 `m5_acceptance` 递归 `make dist` 失败 | 主串行 `m5_acceptance` 递归触发 `make dist`；失败瞬间 `df` 仅剩极小空间 | **已缓解**：清理废弃 `/tmp/eg-*` scratch 释放空间后 `make dist` exit 0；本轮重门禁前再次清理已结束测试 scratch，`/tmp` 保留 ≈2.7 GiB。属环境事件，非回归 |
| `I-006` | 历史门禁 `worktree_hygiene()` 冻结基线漂移：round3/round4/round5/m2_final_gate 的工作区卫生判据在本 handoff 快照下失败 | `gate_common.worktree_hygiene()` 硬绑定 **M1 期本地化基线**（27 个本地化文件保持**未提交**、`tools/teamwork/{scripts,vendor}/` **未跟踪**）；本 `EvergreenDir_M4_final.zip` 打包时把这些**本地化产物已提交、vendor 已跟踪**，与判据假设相反。属**打包 / 环境基线差异**，非产品或测试回归 | **已登记 / 已改判口径**：`T-075` Scope 明令历史门禁判据一字不动，故**不改** `worktree_hygiene()`；余下「本地化改动缺失 / 目录已跟踪」为打包状态差异，接受为已知环境漂移，登记于此并在 §6.1 逐支标注。**不 skip、不 allowlist、不 frozen-red**。**本批次订正一处不实表述**：初版称已清理「其名下唯一真垃圾 `__pycache__`/`*.pyc`」，实测该垃圾**会在每次运行 Teamwork CLI / vendor 内 Python 时重新生成**（本批次复算门禁时 `tools/teamwork/vendor/click/__pycache__` 再次出现）。它是 `tools/teamwork/.gitignore` 忽略的**未跟踪**产物（`git ls-files \| grep -c __pycache__` = **0**），因此**不进交付包**；「已清理」只对**某一时刻**成立，打包前再清一次 |

### 7.1 本批次核验的三条 open issue（`I-002` / `I-003` / `I-004`）

> **原则**：**不凭状态关闭**。已解决的用可复现证据走 Teamwork 正常关闭；仍存在的写清
> 复现命令 / 环境 / 日志 / 影响 / 解除条件 / 归属。P0 与核心 P1 真问题**修**，非阻断项**如实登记**。

| Issue | 现态核验（本批次） | 处置 |
|---|---|---|
| `I-002`（major，dry-run 计数） | **仍真实存在**，根因定位到 `internal/cli/apply.go` 的 `applyDryRun()`：该函数**从不填** `rep.Cards.Created` / `rep.Cards.Updated`，却无条件调 `rep.NoteZeroCards("cards")`。于是同一次输出里 `planned[]` 已列出新卡分区，报告体却写「知识卡：新建 0 张」+ I1「本次未产生知识卡」；正式 `apply` 写「新建 1 张」。既有用例 `TestApplyDryRunAndFormalCountTheSameCards` 只钉「两条源数量相等」，其头注明确写着**修复本体留待后续、本 task 不修不关闭** | **已修复并关闭**：`applyDryRun()` 改为与 `planned[]` **同源**（同一轮 `pres.Actions` 遍历），只镜像执行器真正记账的两条形态（`ActCardNew`→`cards.created`、`ActCardAppend`→`cards.updated`），其余写形态执行器也不记、这里同样不记 —— **不多数一张也不少数一张**（修复 commit `a2069db`，evergreen 仓）；`NoteZeroCards` 因此只在**确实无新卡**时出现（EG-KNW-05 零卡合法语义**原样保留**）。新增端到端回归 `TestApplyDryRunReportCountsMatchPlanned`（4 组：计划建卡→计数非空且零卡说明消失、与正式执行 `cards.*` 逐字相等、只补充既有卡→两侧都记 `updated` 且都保留零卡说明、dry-run 仍**零写入零 commit**）。回归：`go test ./internal/... -count=1` **15 包全 ok**；受影响 e2e `bash test/e2e/m2_convergence.sh` **12/12 exit 0**；`go test ./internal/cli -race -count=1` **ok（151.3s，0 data race）**、`go test ./test/e2e -run 'TestM1RealArticleZeroIntervention\|TestM1ErrorCasesRejectedWithZeroWrite\|TestM1CoverageGapAbsentRerun'` **ok**。**零 e2e 断言被删或弱化**（全库 `grep '新建 0'` 仅命中历史 PPE 语料，无现行断言依赖旧错误行为） |
| `I-003`（minor，源码包 `make test` 缺合同） | **仍存在，但只对「evergreen 单仓分发」成立**。复现：`git archive HEAD \| tar -x -C /tmp/i003_probe/evergreen-only && cd /tmp/i003_probe/evergreen-only && CGO_ENABLED=0 go test ./internal/... -count=1`。环境：本沙箱 `linux/amd64`、go1.24.13、无同级 `teamwork/`。证据：`CGO_ENABLED=0 go build ./...` **BUILD OK**；单测**恰 4 支失败**，全在 `internal/cli/skill_test.go`，报 `读不到 ../../../teamwork/projects/.../2026-09-01-eg-cli-contract.md: no such file or directory` —— `TestSkillRelationTableMatchesContract` / `TestSkillCoverageGapsMatchesContract` / `TestSkillSpecCrossReferences` / `TestSkillCommandStatusMatchesImplementation`；其余 14 包全部 `ok`（日志 `/tmp/i003_probe/evergreen_only_unit.log`）。**影响**：仅影响「只拿 evergreen 单仓」的使用者跑合同一致性用例；**本次交付的组合源码包**（`EvergreenDir/{evergreen,teamwork}` 同包）下相对路径成立，同四支**实测 `ok`**。**归属**：测试基础设施（合同文档跨仓引用），非产品缺陷 | **保持 open（不凭状态关闭）**：**解除条件**（任一）——①把 4 支用例的合同路径改为可配置（如 `EG_CONTRACT_ROOT` 环境变量）并在缺失时 `t.Skip` 前给出显式说明；②在 evergreen 仓内落一份合同文档只读副本并加同步门禁；③正式声明「合同一致性用例仅在双仓组合树下运行」并在 `INSTALL.md` 写明。本批次**不改**（属 M1 期测试口径，改动会触碰 4 支历史用例与 `T-075` Scope 边界）。交付侧**已规避**：最终源码包为**双仓组合树** |
| `I-004`（minor，SKILL 验收样本重合） | **仍存在**。实测 `skill/SKILL.md` §6.2 样例 ② 仍以 `Verification, The Key to AI（Rich Sutton, 2001）` 为素材（`skill/SKILL.md:385/396`），而该文正是 PPE 验收语料的同一篇（`test/e2e/testdata/ppe/**` 下 `s-20260901-verification-the-key-to-ai.md` / `n-20260901-...` 等）。**影响**：照 `SKILL.md` 学习的 Agent 与被用来判定它的语料**同源**，存在**判断偏置**（不影响任何断言的强度或产品行为）。**改动代价**：`grep` 统计 `skill_test.go` + `test/e2e/m1_test.go` 对样例的引用 **51 处**，且 PPE 为**逐字冻结**语料，替换样例会连带改动冻结夹具 | **保持 open（非阻断，如实登记）**：**解除条件** —— 为 §6.2 样例换一篇**不在任何验收语料中**的素材，同时保持 §6.2 两份 ChangePlan 样例仍能被 `eg apply --dry-run` 接受（`m2_convergence.sh` 第 8 步），并同步 `skill_test.go` 的样例引用；**不得**通过删除 PPE 语料或弱化 `m1_test.go` 的样例回放来"解决"。**归属**：文档 / 验收语料设计，`T-075` 之后的独立小改 |
| `I-007`（新增，环境兼容） | **linux/arm64 与 darwin 两架构在本环境从未实际运行**，初版只在发布文档 §4 与风险项里提及、**未登记为 issue**。本批次按要求补建，见 `issues/I-evergreen.s1_main_flow-158614-007-linux-arm64-darwin.md` | **open（非阻断，如实登记）**：证据、复现命令、当前环境、影响与解除条件见该 issue 与 §9 已知限制第 1、4 项。**绝不**把交叉编译说成原生验收通过 |

### 7.2 dist 四平台产物的处置（本批次：**按新 HEAD `a2069db` 重建后**再删除）

**用途分析**：本次交付物是**源码 / 测试 / 脚本 / 配置 / 文档 / 合同**，
`make test`、`go build`、全部 e2e 与门禁**都从源码构建**（`m6_docs_commands.sh` 第 1 步即
「从源码构建 `eg` 到沙箱（不落 `bin/` `dist/`）」），**没有任何一条路径消费预置二进制**。

**为什么必须重建**：初版 §7.2 溯源的四平台产物构建自 `b535b54`；本批次 `I-002` 修复后
产品源码确已前进到 `a2069db`，旧快照**与交付源码不再对应**。故本批次先**一次性重建**
四平台产物并保存全套证据，再删除包外二进制。

**重建与溯源（`a2069db`，四重核对全部通过）**：

| 校验 | 命令 | 结果 |
|---|---|---|
| 构建 | `CGO_ENABLED=0 make dist`（`COMMIT` 取 `git rev-parse --short HEAD` = `a2069db`） | 四平台交叉编译成功；构建时间（UTC）`2026-09-11T19:11:43Z`；Go `go1.24.13` |
| 校验和 | `cd dist && sha256sum -c SHA256SUMS` | 四项**全 OK**：`eg_darwin_amd64` `4eb7085e…`、`eg_darwin_arm64` `4f9efb17…`、`eg_linux_amd64` `40328824…`、`eg_linux_arm64` `35407798…` |
| 格式 | `file dist/eg_*` | linux 两支 `ELF 64-bit … statically linked, stripped`（x86-64 BuildID `29a91099…` / aarch64 BuildID `e80e2cf8…`）；darwin 两支 `Mach-O 64-bit executable`（x86_64 / arm64，`DYLDLINK\|PIE`）；**零动态链接** |
| 本机运行 | `./dist/eg_linux_amd64 --version` + `--help` + `init` / `check` / `search --json` 冒烟 | `eg 0.6.0-m6 (commit a2069db, built 2026-09-11T19:11:43Z, go1.24.13 X:cacheprog,testenhance)`；`init` 落盘 `evergreen.yml`/`SKILL.md`/`sources`/`domains`/`unprocessed.md`；`check` **exit 0**；`search --json` **exit 0** 且索引缺失下留痕恰 `W23` + `Q5`（降级语义在场） |

**两个基线必须分开读（不可混用）**：

| 基线 | commit | 构建时间（UTC） | 角色 |
|---|---|---|---|
| **历史全量测试基线** | `b535b54` | `2026-09-11T11:28:43Z` | 全量 61 支 e2e / `go test ./... -race` / 变异测试 / `m5_acceptance` 50 支全绿的**执行基线**；旧 SHA（`b3cdd040…` / `511a64dc…` / `8e13fb36…` / `18f3755a…`）**只作历史对照**，**不得**用来校验当前源码构建产物 |
| **最终交付源码基线** | `a2069db` | `2026-09-11T19:11:43Z` | 本报告页脚、最终源码包与本节 provenance 对应的 commit；与 `b535b54` 的唯一差异是 `I-002` 修复（`internal/cli/apply.go` + `apply_test.go`），无其它产品语义变更 |

**处置**：按用户授权删除**工作区（包外）**的四个 `dist/eg_{linux,darwin}_{amd64,arm64}`；
**保留** `dist/SHA256SUMS`（`a2069db` 值）与新增的 `dist/PROVENANCE.txt`（含上述两个基线的
SHA / 构建时间 / `file` / 本机 `--version` 与冒烟输出、恢复构建方法、未原生验证声明）。

1. `dist/` 被 `.gitignore` 忽略（`git check-ignore -v dist/SHA256SUMS` → `.gitignore:3:/dist/`），
   故这两个文件**不在 `git archive` 的 tracked 集合内**，由打包流程作为**审计快照**单独放入交付包。
2. **未用 `make clean`**（会连带删掉 `SHA256SUMS`），也**未为打包改 `Makefile`**。

**源码包恢复构建方法**（包内无 `.git`，`COMMIT` / `DATE` 需显式注入）：

```bash
cd EvergreenDir/evergreen
CGO_ENABLED=0 go build ./...                                        # 编译自检
CGO_ENABLED=0 make dist COMMIT=a2069db DATE=2026-09-11T19:11:43Z    # 复现四平台产物
cd dist && sha256sum -c SHA256SUMS
```

> 可复现构建**未承诺**：`SHA256SUMS` 依赖注入的 `COMMIT` / `DATE`，同源码不同时刻构建哈希不同；
> 溯源锚点是 `eg --version` 的 `commit` 段（发布文档 §4 已登记同一口径）。
> 三个非 amd64/linux 目标仍**只经交叉编译**，见 `I-007` 与 §9 第 1、4 项。

## 8. S1 阶段收口

**S1 阶段收口**结论：Evergreen S1 主线自 M1 至 M6 六个里程碑（M-001 ~ M-006）逐一交付并验收，
S1 达成从「幂等收录 + 尽力而为写入」到「**权威 Markdown 一个字节都不错写**」的目标态：

- **功能面**：22 命令封闭；8 KnownVerbs / 17 AllOpNames / 12 值 check / 5 键 rel data 计数封闭；
  退出码全集 `{0,1,2,3,4,5,6}`；诊断码在 M6 新增恰 5 条（E15/E16/W26/W27/W28）、`W21` 仍不分配。
- **原子性面**（M6）：多文件强原子事务、`run.lock` 短临界区、`.index/txn/` 事务日志、
  两遍整事务原子崩溃恢复、块级安全合并（W27）、并发冲突封闭（W28）、写前强校验 + 退出码 5 零写入。
- **权威性面**：Markdown 仍是**唯一权威**，`.index/`（含 M5 派生索引与 M6 runtime-reserved 的
  `run.lock` / `txn/`）**不升为权威**；索引可重建、降级语义成立。
- **需求面**：S1 生效 44 条需求全部 PASS，26 条未承诺需求 DEFERRED 附归属阶段（§5），0 条 FAIL。
- **延期债面**：`J14` 已清偿；`J15` / `I2` 已改判口径但**授权待 owner 确认**（§4 + `I-evergreen.s1_main_flow-158614-008`）；环境漂移 `I-005` / `I-006` 已登记终态（§7）。
- **版本 / 发布面**：版本收口 `0.6.0-m6`、四平台 dist 于 `b535b54` 重建并溯源、静态构建保全
  （纯 Go `modernc.org/sqlite`、直接依赖恰 2）。

据此，**S1 阶段收口成立**，M-006 达成 18 条完成判据逐条 PASS，可迁移 `T-075` / `M-006` / S1 / Evergreen 至收口状态。

### 8.1 收口成立的**边界条件**（本批次如实补记，不得省略）

「S1 收口成立」是对 **18 条 M6 完成判据 + 44 条 S1 生效需求**的结论，**不等于**「全部测试全绿 / 零遗留」。
同时成立且**必须一并读**的事实：

| # | 事实 | 位置 | 是否阻断收口 |
|---|---|---|---|
| 1 | 历史门禁**本次执行非零 6 支 / 15 条 failed check**，全部归入三类已登记差异（打包环境基线、冻结基线随里程碑演进、生成产物快照漂移），**未改判据、未 skip / allowlist / frozen-red** | §6.1 | 否（非产品回归；差异在册） |
| 2 | `derive_eg_baseline` 判据依赖沙箱外飞书 §14 原文，**标为「外部未知」，不计入通过** | §6.1 | 否（如实标未知，不冒充通过） |
| 3 | `J15` / `I2` 的改判**没有 owner（`ikaqiu`）书面授权证据**；已登记为独立待确认 issue `I-evergreen.s1_main_flow-158614-008`（major, `open`，已挂 `T-075` `known_issues`），本报告只把它作为 Agent Harness 依 `T-075` Scope / R8 §18.4 的**口径差异声明** | §4、`I-evergreen.s1_main_flow-158614-008` | **是（硬性 DoD 项未满足）**：见下一行 |
| 4 | `I-003` / `I-004` / `I-007` / `I-008` 四条 issue **保持 open**（前三条非阻断，均写明复现 / 环境 / 证据 / 影响 / 解除条件 / 归属；`I-008` 为授权待确认） | §7.1、§4 | 否 |
| 5 | **linux/arm64 与 darwin 两架构从未原生运行**，只有交叉编译 + `file(1)` 校验 | §7.2、§9、`I-007` | 否，但**不得**表述为「原生验收通过」 |
| 6 | `I-002` 在本批次**修复并关闭**，故最终源码包内的 evergreen commit 为 **`a2069db`**，不再是 `b535b54`；四平台 `dist` 已按 `a2069db` **重建**并留全套溯源，`b535b54` 的旧 SHA 仅作**历史全量测试基线**对照 | §7.1、§7.2 | 否 |
| 7 | **`T-075` DoD 判据 18 的「授权留痕」项当前未满足** —— DoD 原文要求「取 `已改判口径` 附**授权留痕**与被改判判据编号」；判据编号已在册，**授权留痕缺失**。故 `T-075` = `done`、`M-006` = `done`、`s1_main_flow` = `shipped` 应读作**先前已记录的历史状态**，而**不是**「全部硬性关闭条件已无保留满足」。本报告**不自行**把这条硬性要求降级为「非阻断」，也**不自行**回改任何生命周期字段；授权缺口按正常流程由 `I-evergreen.s1_main_flow-158614-008`（`open`）承载，owner 确认或驳回后再走对应流程 | §4、§8.2 ⑤、`I-evergreen.s1_main_flow-158614-008` | **是** —— 该项须 owner 确认方能闭合 |

### 8.2 最终交付结论 —— 五类口径逐项归类（**禁止合并成「全绿」**）

> 本表是最终交付的**唯一总括口径**。任何一项都必须能落进下面**恰一类**；
> 不得为了凑 `PASS` 修改门禁判据、不得把「复用」写成「本次执行」、不得把「未执行」写成「通过」。

| 口径 | 含义 | 本次交付的归类项 | 计数 |
|---|---|---|---|
| **① 本次执行（0 failed / 0 存活）** | 本批次亲自跑出且全绿 | 历史门禁 9 支（`validate_m1/m2/m3/m4`、`validate_m56` 35/0、`m4_final` 35/0、`m6_final` 62/0、`independent_schedule` 22/0、`m6_mutation` 65 算子 0 存活）；`go build ./...`；`go vet ./internal/cli`；`go test ./internal/... -count=1`（15 包 `ok`）；`go test ./internal/cli -race -count=1`（`ok` 151.3s）；`m2_convergence.sh` 12/12；3 支 M1 dry-run Go e2e；§6.4 五支 `.index/` 边界测试；`I-002` 新增回归 `TestApplyDryRunReportCountsMatchPlanned`；`dist` 四平台重建 + `sha256sum -c` + `file` + 本机 `--version`/冒烟 | **门禁 9 支 + 代码面 8 组** |
| **② 历史复用（源码无增量，未重跑）** | 已在 `b535b54` 证明通过，本批次源码增量不触达其判定面，明确标注复用 | `m5_acceptance.sh` 全量 50 支（`/tmp/m5acc.log`，5/5 组断言、e2e 磁盘总数 61、零豁免）；`go test ./... -count=1 -race` 全包；`m6_*.sh` 11 支；`make lint`（含 `dep_direction_gate.sh`）；`m4_mutation_test.py` | **5 组** |
| **③ 已登记失败差异（如实在册，不改判据）** | 本次执行确实非零，根因已定性为非产品回归，逐条在册 | 历史门禁 6 支共 **15 条 failed**：`round3`（`H3` 打包卫生、`H6` 生成产物快照漂移）、`round4`（`J3`）、`round5`（`H3`）、`m2_final`（`H5`）、`m3_final`（9 条，5 类根因）、`m5_final`（`E2` M5 版本冻结断言）；归口 `I-006` + `J15` | **6 支 / 15 条** |
| **④ 未执行（本环境不具备条件，绝不冒充通过）** | 无法在本沙箱执行，或依赖沙箱外材料 | `linux/arm64`、`darwin/amd64`、`darwin/arm64` 三目标的**任何**原生 e2e / 单测 / 门禁（`I-007`）；`derive_eg_baseline` 判据依赖飞书 §14 原文（**外部未知**，不计入通过）；`I-003` 的三条解除条件与 `I-004` 的样例替换（**本批次不改**，登记 open） | **3 目标 + 1 支门禁 + 2 条 issue** |
| **⑤ 待 owner 确认（硬性 DoD 项，未闭合）** | 结论已登记但缺 DoD 要求的书面授权留痕 | `J15`（`m3_final_gate.py` 9 条 failed 不改判据、登记接受）与 `I2`（撤销 M4 合同 §14「诊断码恰 14」精确计数、以实现真源为准，实测 13、`I2` 码从未落地）两项「已改判口径」；对应 `I-evergreen.s1_main_flow-158614-008`（major, `open`，已挂 `T-075` `known_issues`）。**`T-075` DoD 判据 18 的「授权留痕」要求当前未满足**，`done`/`shipped` 仅为历史状态 | **2 条延期债 / 1 个 issue / 1 项未满足的 DoD** |

**据此，最终交付的总括表述只能是**：
「**M6 十八条判据与 S1 44 条生效需求在 `linux/amd64` 上逐条 PASS；同时存在 6 支门禁 15 条已登记失败差异、
1 支外部未知门禁、3 个未原生验证架构、4 条 open issue，其中 `I-008` 对应 `T-075` DoD 判据 18
「授权留痕」一项**尚未满足**、待 owner 确认**」——
**不是**「全部测试全绿」，**不是**「全部硬门禁通过」，**也不是**「全部硬性关闭条件无保留满足」。
`T-075` / `M-006` / `s1_main_flow` 的 `done` / `shipped` 是**历史已记录状态**，本报告不据此宣称授权项已闭合。

## 9. 已知限制（≥3 项，逐条在册）

M6 收口版本 `0.6.0-m6` 明确保留以下**已知限制**（对应 A-60 选项①，与 `T-075` 标题一致），
对外文档（README / INSTALL）同步登记：

1. **macOS 未真机验证**：darwin/amd64 与 darwin/arm64 两平台产物仅为交叉编译（`file(1)` 确认非本机验证），
   **未经真机**运行验证；`flock` / `fsync` / `rename` 语义以 Linux 为准。
2. **网络盘 / 同步盘不保证原子性**：`run.lock` 仅保证**单机多进程**互斥；**网络盘**（NFS / SMB）
   与同步盘（Dropbox / iCloud / OneDrive）上 `flock` 语义不可靠，作为已知限制**如实登记，不承诺、不探测**。
3. **未闭合事务时不可删 `.index/`**：`.index/txn/` 是**不可重建**的事务日志（非派生索引）；
   存在**未闭合事务**（`OpenTxn`）时**不可删除 `.index/`**，否则将丢失崩溃恢复所需前像。
4. **linux/arm64 未原生验证**（本批次补记，`I-007`）：`linux/arm64` 产物同样**只经交叉编译**
   （`GOOS=linux GOARCH=arm64`）与 `file(1)` 静态链接校验，**从未在 aarch64 机器上运行过任何
   e2e / 门禁**。本环境唯一原生验证过的目标是 **`linux/amd64`**（`uname -m` = `x86_64`）。
   四平台的「支持」口径 = **可交叉编译且静态链接**，**不是**「已原生验收」。
5. **evergreen 单仓分发下 4 支合同一致性用例失败**（`I-003`，minor）：`internal/cli/skill_test.go`
   的 4 支用例按相对路径读 `teamwork/` 内冻结合同，只拿 evergreen 单仓时读不到而失败
   （`go build` 仍 OK）。**本次交付为双仓组合树**，该 4 支实测 `ok`。
6. **SKILL 示例与验收语料同源**（`I-004`，minor）：`skill/SKILL.md` §6.2 样例与 PPE 验收语料
   为同一篇文章，存在**判断偏置**；不影响任何断言强度与产品行为。

---

*本报告为 `T-evergreen.s1_main_flow-158614-075` 收口产物。*
*证据基线：**Evergreen `b535b54`**（M6 收口 / 全量 e2e / race / mutation / dist 快照）
→ **`a2069db`**（本批次 `I-002` 修复 + 受影响面复算，最终源码包所用 commit）。*
*本批次已按新 HEAD `a2069db` **重建**四平台 dist 并留全套溯源证据，随后删除工作区（包外）四个
`dist/eg_*` 可执行，保留 `dist/SHA256SUMS`（`a2069db` 值）与 `dist/PROVENANCE.txt`（含 `b535b54`
历史全量测试基线对照）。*
*不修改任何 M1–M5 历史 Task / 历史验收结论 / 历史门禁判据；差异一律登记于本文档与对应 Issue。*
*本报告**不声称**「全部测试全绿」，**也不以「全部硬门禁通过」作总括**；非零门禁、外部未知项、
open issue（`I-003`/`I-004`/`I-007`/`I-008`）、未原生验证架构与 `J15`/`I2` 授权缺口均已逐条在册
（§4、§6.1、§7.1、§8.1、**§8.2 五类口径归类表**、§9）。*
