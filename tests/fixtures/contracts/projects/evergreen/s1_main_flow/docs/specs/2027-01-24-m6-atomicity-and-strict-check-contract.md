# M6 原子性与强校验合同（事务、锁、崩溃恢复、并发冲突、写前强校验与退出码 5）

- **文件**：`projects/evergreen/s1_main_flow/docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md`
- **产出 task**：`T-evergreen.s1_main_flow-158614-070`（M6 唯一 Contract 型 task，`due = 2027-01-24`）
- **里程碑**：`milestones/M-006-m6.md`（M6 原子性与强校验，收口日 `2027-02-21`，S1 主线收口）
- **上游依赖**：`T-evergreen.s1_main_flow-158614-069`（M5 收口，全 Epic 唯一一条跨里程碑硬依赖边）
- **下游消费**：`T-…-071` / `T-…-072` / `T-…-073` / `T-…-074` 硬依赖本合同开工；`T-…-075` 软依赖并在 S1 收口报告中复述
- **设计依据**：`docs/specs/2026-08-31-evergreen-s1-tech-design.md` §9（四条安全底线）、§13（包边界与三条依赖禁令）、§4.6（`txn_id` 逐字「可省略」）、§16.1（S5 行「强原子事务、锁与写前复核、崩溃恢复、error 面扩大」）、§7「退出码」行（`4` 的逐字语义）
- **本合同的地位**：M6 的**唯一施工依据**。本文件是文档，**零产品代码**——`internal/**` 在 `T-…-070` 内一个字节都不改。

M6 的目标态逐字写作「**权威 Markdown 一个字节都不错写**」：把 M1 ~ M5 的「宁可少写、不可错写 + 事后可查」升级为
「**要么全部生效、要么全部不生效**」。M6 只把既有需求从「可用」推到「严格」，**不开任何新需求面**。

---

## 0. 冻结口径与阅读顺序

1. **本合同的九个实做小节**（§2 ~ §10）与 `M-006-m6.md`「M6 的实做范围恰为下列九项」**一一对应，无遗漏、无重叠**。
2. **九项裁决**集中在 §1 一张表，**恰 9 行**（`A-52` ~ `A-60`），每行给出**唯一裁决**；全表无 `未知 / 待 owner 确认`，
   因此 `071` ~ `074` **无一条被阻断**。
3. `A-56`（强校验开关形态）与 `A-60`（S1 收口版本号）是 `M-006` 标注的**两处 owner 授权点**：
   本合同两项**已获 owner 口径并按该口径定稿**——`A-56 = ②`（默认宽松 + `--strict`）、`A-60 = ①`（`0.6.0-m6`）。
   两行是**裁决**，不是「预判生效、待授权可改判」。
4. 本合同**不改**任何历史事实：M1 ~ M5 的历史 Task、历史 Activity Log、历史验收结论、历史门禁判据一字不动；
   与 `M-006` 表格的差异（若有）在 §11 以实测为准逐行登记。
5. **术语**：`vault` = 被操作的知识库根；`前像` = 本次事务开始时被写文件的原始字节与 `content_hash`；
   `临界区` = 持锁区间；`权威文件` = `vault/` 下的 Markdown（唯一权威）；
   `accepted write-set` = `S3` preflight 定盘后本次事务真正要写的文件集合（= 原子域）；`skipped 集合` = 同阶段定盘的跳过集合。
6. **修订记录（R1 ~ R7：`R1` ~ `R5` 为 `T-…-070` integration 期 reviewer 五轮阻断后追加；`R6` / `R7` 为 `T-…-070` 收口（`done`）之后的两轮 **post-close correction**——`R6` 收口 `.index/` 共存 P0 缺口（定稿见 §16），`R7` 收口退出码 `5` 的落地形态与 `os.Exit` 单点 P0 错误表述（定稿见 §17）；本文件始终是同一份唯一合同）**：
   **R1（第一轮）**收口五处语义矛盾，逐一给出唯一答案——① 跨文件可见性改为 §5.1 **四级模型**（不再声称非协作观察者获得跨文件瞬时一致）；
   ② **Markdown 事务提交点 = `commit` 标记**，Git 提交是持锁下的**后提交步骤**（§1 A-52 行 / §2）；
   ③ **`W26` 只在真实回滚的 `P2` / `P3` / `P4` 产出**（§4 / §6）；④ `txn_id` 改为**持锁后按 `.index/txn/seq` 持久计数器 + 日志最大序号**分配（§1 A-59 行 / §4）；
   ⑤ **B3 跳过与 `W27` 跳过统一在 `S3` preflight 定盘**，原子性只覆盖 accepted write-set（§5.2）。
   与之配套的 M6 规划正文（`M-006-m6.md` 判据 6 与范围第 4 项、`T-…-072`、`T-…-075`）已按 owner 授权同步纠正，
   改动面严格限于 `projects/evergreen/**`，**M1 ~ M5 历史一字未动**。
   **R2（第二轮）**收口五处：① `L3` 改为**恢复屏障语义**——半应用态**可能持续到下一次写命令**、恢复屏障后回到前像、
   新写事务不建立在半应用态上；判据用例名 `TestNoPartialFileEverVisible` → **`TestNoTornFileEverVisible`**（规划与验收命令同改）；
   ② 失败 / 恢复的**唯一顺序**改为「保持未闭合 → 幂等回滚全部前像并 `fsync` → 最后原子写 `abort` 并 `fsync` txn 目录」，
   标记落盘含 `fsync(tmp)` 与目录 `fsync`（先写 `abort` 再回滚是崩溃不安全的，**严禁**）；
   ③ 陈旧锁：**互斥唯一真源 = 内核 `flock`**，正文三项（`pid` / `acquired_at` / `argv`）纯诊断可直接覆盖、初次不含 `txn_id`，
   `flock` 忙时绝不凭 `pid` / `host` 强抢（消除 hostname 变化 / 目录复制后的永久阻塞）；
   ④ `S0` 锁外只做参数解析与候选发现，**`S3` 必须在锁内重读全部权威输入并重算 / 最终化 ChangePlan、`content_hash`、
   accepted write-set**（只验 hash 不足以排除跨文件混合快照）；
   ⑤ `txn_id` 的唯一 / 单调**保证域限定为「当前 `.index/txn` 生命周期内」**，并无条件保留 `071` 的「同进程唯一且单调」。
   R2 同步纠正 `M-006-m6.md`、`T-…-071`、`T-…-072`、`T-…-074`、`T-…-075` 的对应规划正文，范围仍限 `projects/evergreen/**`。
   **R3（第三轮）**收口四处：① **P0 锁 inode 不可替换**——`flock` 加在 inode 上，持锁期 `run.lock` **禁止** `rename` / `unlink` / 重建，
   正文改为同一把已加锁 fd 的 `Ftruncate` + `Pwrite` + `Fsync`（允许非原子、读侧容错）——R3 当时还并列给出「`run.lock` 恒空 + 独立
   `run.lock.meta`」分离形态作为备选，**该备选已在 R5 被撤销**（见下 R5 ①，现唯一形态即同 fd 原地写）；新增反证 `TestLockInodeNeverReplacedWhileHeld` / `TestInodeReplacementCannotYieldSecondLock`（§3）；
   ② **P0 恢复不得盲目还原前像**——`intent.files[]` 增记 `target_hash` / `target_size` / `create`，恢复逐文件三分支
   `B-R1` / `B-R2` / `B-R3`，`B-R3`（post-crash 外部编辑冲突）零写入、保持未闭合、阻断新写、退 `5` + `E15`、不发 `W26`；
   新建文件回滚改为**原子移入 `quarantine/`** 而非删除；新增 `TestRecoverRefusesPostCrashUserEdit`（§4.1）；
   ③ **P1 `S3` 必须在锁内重新执行候选发现**，`S0` 结果只作提示、不得限定 `S3` 输入（§2 / §9）；
   ④ **P1 pre-intent residue 语义**——无 `intent.json` 的孤儿目录可安全移除 / 就地闭合、**不发 `W26`**、不得当作可解析事务（§4.2）。
   R3 **零新增诊断码**（仍恰 5 条）、**零新增退出码**（仍恰 `{0,1,2,3,4,5,6}`），A-58 两类边界一字不改；
   同步纠正 `M-006-m6.md` 与 `T-…-071` / `T-…-072` / `T-…-074` / `T-…-075`，范围仍限 `projects/evergreen/**`。
   **R4（第四轮）**收口四处 P0：① **恢复改为两遍**（§4.1）——`Pass A` 全量只读判定、`Pass B` 才写盘，任一 `B-R3` ⇒ 整事务零写入并退 `5` + `E15`，
   **严禁**边判定边回滚；② **intent 发布屏障**（§4）——`pre/` 全量 `fsync` → 两级目录 `fsync` → `intent.json.tmp` → `fsync(tmp)` → `rename` → `fsync(txn 目录)`，
   屏障完成后才允许首个权威 `rename`，由此「无 `intent.json` ⇒ 零权威写入」**可证明**；③ **损坏 intent fail closed**（§4.2）——
   `stat` 成功但不可解析 / `journal_version` 不支持 ⇒ 原样保留、权威零写入、退 `5` + `E15`，**不降级为 residue**；
   ④ **intent 路径合同级约束**（§4.3）——规范化相对路径，拒绝绝对路径 / `..` / 未规范化串 / symlink 逃逸，违规在 `Pass A` 即 fail closed。
   **R5（第五轮，最终复核）**收口四处：① **A-53 锁正文形态定为唯一**（§3）——同一把已加锁 fd 的 `Ftruncate` + `Pwrite` + `Fsync`，
   **撤销** `run.lock.meta` 分离备选与「二选一」表述；② **A-59 公式补入进程游标**（§4）——
   `seq = max(持久 seq, 现存目录最大序号, process-local lastSeq) + 1`，`txn_id = "t" + seq 的 **16 位**零填充小写十六进制`
   （去掉旧的「8 位 seq + 8 位秒」拼接，避免 `seq` 超过 `0xFFFFFFFF` 后破坏正则），`seq` 超 `uint64` 上限 ⇒ **fail closed** 退 `5` + `E15`；
   ③ **`Pass A` 增补前像可用性校验并升级为全局只读分类**（§4.1）——`pre_bytes_ref` 必须存在、是**普通文件**且其内容哈希 `== pre_hash`，
   缺失 / 损坏即 fail closed；**同一次扫描内的全部事务**（含多个 `OpenTxn` 与并存的 `CorruptTxn`）必须**先全部分类完毕**，
   任何一处冲突 / 损坏 / 违规都在**任何回滚写之前**整体阻断；④ **界定「退出码 5 零写入」的精确含义**（§9）——
   它保证「**本次请求事务零权威写入**」；若 `S2` 已成功完成前一事务的恢复而 `S3` 随后失败，磁盘相对命令启动时**可能已变**，
   但那是**已由 `W26` 记录**的恢复结果，**不是**本次事务的写入。R5 仍**零新增诊断码 / 零新增退出码**，五句逐字锚点一字不改。
   **R6（第六轮，`T-…-070` 收口后 post-close correction，全部 P0）**：对 M5 已交付索引层做**实码核对**
   （`evergreen` HEAD `db64ce7`，2026-09-09）后发现 M6 与索引层在**同一个 `.index/` 目录**上有四点共存冲突，且原 `071` ~ `075` 规划**无任务明确承接**，故本轮闭合——
   ① `internal/index/rebuild.go:25` 的 `Rebuild` 首句即 `os.RemoveAll(dir)`、`internal/index/build.go:152` 的失败清理同样 `os.RemoveAll(dir)`：
   M6 把 `run.lock` 与**不可重建**的 `.index/txn/` 放进同一目录后，一次 `eg index rebuild` 或一次失败的 `build` 会连带删除未闭合事务日志、
   前像 `pre/`、隔离区 `quarantine/` 与 `seq`（**销毁恢复证据**），并**替换掉正被 `flock` 持有的 `run.lock` inode**（直撞 A-53「持锁期 inode 零替换」，可致双写）；
   ② `internal/index/schema.go:42` 的 `AllowedFiles()` 把 `.index/` 允许项钉死为 `eg.db` / `eg.db-wal` / `eg.db-shm` **恰 3 项**，
   `internal/index/corrupt.go:200` 的 `unexpectedFiles()` 会把 `run.lock` / `txn/` 判成 `unexpected_file`（`corrupt.go:130`）⇒ 索引恒 `corrupt`、
   读路径恒降级、`EnsureBuilt` 每次都走 `Rebuild`，与 ① 组成「**每次写命令都毁一次恢复证据**」的放大回路；
   ③ index 维护路径（`eg index build` / `rebuild` / `sync`）当前**不拿任何锁**，可在写事务临界区之外删改 `.index/`；
   ④ M1 ~ M5 的历史 e2e 中有脚本逐字断言「`.index/` 恰 3 项」「M6 能力不存在」，M6 交付后这些断言必然与现态冲突。
   R6 的定稿口径集中在 **§16**（命令三类别与锁覆盖面 / `.index/` 共存契约与 `RemoveAll` 禁令 / M6 runtime reserved entries 与类型 fail closed /
   历史门禁阶段化与现态重钉）。R6 **零新增诊断码**（仍恰 5 条，复用 `W24` / `E15` / `E16` / `W26`）、**零新增退出码**（仍恰 `{0,1,2,3,4,5,6}`）、
   **零新增命令**（仍恰 22）、**零新增 e2e**（M6 仍恰 11 支、磁盘总数仍 ≥ 61），五句逐字锚点一字不改。
   承接归属：**`072` 独家承接** `internal/index/{build,rebuild,corrupt,schema}.go` 与 `internal/cli/{index,index_sync}.go` 的兼容改造；
   `071` 只提供锁 API 且**不碰** `internal/index`；`074` 承接 reserved entries 类型错误的 `E15` 出口与 index 维护锁忙的 `E16` 出口；
   `075` 承接历史脚本现态重钉与 61 支全跑。R6 同步纠正 `M-006-m6.md` 与 `T-…-071` ~ `T-…-075`，范围仍限 `projects/evergreen/**`、
   `internal/**` 零改动，且**不回退 `T-…-070` 的 `done` 生命周期**（post-close correction 以追加提交 + Activity Log 留痕）。
   **R7（第七轮，`T-…-070` 收口后 post-close correction 之二，P0）**：继续实码核对**退出码 5 的落地形态**后发现 R1 ~ R6 反复写下的
   「`os.Exit(5)` 由 `internal/cli/exitcode.go` **单点**承载」是**错误表述**，与本仓架构和历史门禁**双向冲突**——
   ① `internal/cli/exit.go:5` ~ `:7` 逐字规定「业务层只返回带类型的错误，退出码只在本文件翻译一次」「各命令实现禁止调用 `os.Exit`（唯一允许处是 `cmd/eg/main.go`）」；
   ② `cmd/eg/main.go:6` 逐字自证「本文件是全仓唯一允许调用 `os.Exit` 的地方」，函数体恰一行；
   ③ `internal/cli/exitcode.go:24` ~ `:25` 逐字写明退出码 5「刻意不定义常量」，`:62` 的 `ExitCode5Enabled()` 恒 `false`——启用动作本应是「定义常量 + 翻转该函数」；
   ④ 6 处历史门禁（`m3_acceptance.sh` / `m3_authorization.sh` / `m3_lifecycle_state.sh` / `m4_acceptance.sh` / `m5_acceptance.sh` / `m5_degrade_fallback.sh` / `m5_read_path_index.sh`）
   断言 `internal/` 下 `os.Exit(5)` **恰 0**，若按旧表述实现将当场转红且**无法**按「保留历史事实」重钉（历史事实与现态互斥）；
   ⑤ `m5_acceptance.sh:117` ~ `:120` 逐字说明「只 grep os.Exit 会得到空集，让判据形同虚设，故改为**常量表** + 空集反证双侧」——权威落点本来就在常量表。
   R7 的定稿口径集中在 **§17**（常量 `ExitPrecheckOrLock = 5` + 两类类型化错误 + `ExitCodeFor` 恰两条分支 + `cmd/eg/main.go` 唯一 `os.Exit`；
   验证口径由 `grep os.Exit(5)` 全量替换为 V-R7-1 ~ V-R7-4；`071` ~ `073` 只返回可被 `074` 映射的 typed / sentinel 错误与 `E15` / `E16`，
   其 targeted e2e **不得**提前断言真实进程退 `5`；`ExitCode5Enabled()` 的 6 处消费点由 `074` 重钉，shell 常量表断言由 `075` 重钉）。
   R7 **零新增诊断码**（仍恰 5 条）、**零新增退出码**（仍恰 `{0,1,2,3,4,5,6}`）、**零新增命令**、**零新增 e2e 文件**，A-58 的选项与结论一字不改；
   同步纠正 `M-006-m6.md` 判据 12 与 `T-…-071` ~ `T-…-075`，范围仍限 `projects/evergreen/**`、`internal/**` 零改动，
   且**不回退 `T-…-070` 的 `done` 生命周期**。

---

## 1. A-52 ~ A-60 九项裁决（恰 9 行，全部唯一裁决）

行格式固定：`| **A-5N** | 问题 | 封闭选项 | 裁决 | 改动面 | 机器反证形态 | 受影响 task |`。

| 编号 | 问题 | 封闭选项 | 裁决 | 改动面 | 机器反证形态 | 受影响 task |
|-|-|-|-|-|-|-|
| **A-52** | 事务边界：一次「事务」是一条命令、一个 ChangePlan、还是一次 Git 提交 | ① 一条写命令 = 一个事务（Git 提交在事务内）；② 一个 ChangePlan = 一个事务；③ 一次 Git 提交 = 一个事务 | **①**：一条写命令 = 一个事务。**Markdown 事务的提交点 = `commit` 标记**，一经落盘事务即已生效；**Git 提交是持锁下的后提交步骤**（在同一临界区内、`commit` 标记**之后**执行），**不是** Markdown 原子事务提交点的组成部分；Git 提交失败退 `4`、磁盘保留当前状态且**不回滚 Markdown**——这就是选项①「Git 提交在事务内」在本合同中的**唯一读法**（「在事务内」= 在同一命令、同一把锁的临界区内被执行，**不**等于「Git 参与决定 Markdown 是否生效」）。② 会让 `eg reconcile --repair` 一条命令出现多事务、跨事务半成品持久留在盘上；③ 把原子性押在 Git 上，与 `4` 的既有语义直接冲突 | `internal/txn/commit.go` 的事务生命周期起止；`internal/plan/executor.go` 的调用点 | `TestMultiFileCommitAllOrNothing` / `TestCommitIsIdempotent`；`e2e m6_atomic_multifile.sh` 退 `0` | 070 / 071 / 072 / 074 |
| **A-53** | `run.lock` 位置：放 `.index/` 与「`.index/` 可删」冲突，放 vault 根会被 Git 跟踪 | ① `vault/.index/run.lock`；② `vault/.eg/run.lock`（需扩 F1 冻结布局）；③ 系统临时目录 + vault 路径哈希 | **①**：锁文件恒为 `vault/.index/run.lock`，已被 `.gitignore` 覆盖，**不新增顶层目录、不动 F1 冻结布局**；`.index/` 不存在时由锁层**按需创建目录**（创建 `.index/` 不等于创建索引）；**互斥真源只认内核 `flock`**，锁正文（`pid` / `acquired_at` / `argv` 三项）纯诊断；**被加锁的 inode 在持锁期零替换**——正文写法**恰一种、无备选**：同一把已加锁 fd 做 `Ftruncate` + `Pwrite` + `Fsync` **原地写**（允许非原子，读侧容错），**严禁** `tmp` + `rename` 或 `unlink`（否则另一进程 `open` 到新 inode 可再拿一把 `flock` ⇒ 双写），**亦不允许**「`run.lock` 恒空 + `run.lock.meta`」等任何旁路形态（R5 撤销该备选，见 §3）；`flock` 忙时**绝不**凭 `pid` / `host` 强抢（§3）；**同目录共存契约见 §16**——任何 M6 路径**禁止** `os.RemoveAll(.index)`，`run.lock` 与 `txn/` 是 M6 runtime reserved entries，index 维护路径必须先拿同一把锁；②③ 分别撞 F1 与跨用户互斥 | `internal/txn/lock.go` 的路径常量与正文写法（`Ftruncate` + `Pwrite` + `Fsync`）；`eg init` 的 `.gitignore` 模板不变 | `grep -rnE 'os\.WriteFile\|os\.Create' internal/txn/` 除 `run.lock` 与 `.index/txn` 外恰 0 处；`grep -rnE 'os\.Rename\(.*run\.lock\|os\.Remove\(.*run\.lock' internal/txn/` 恰 **0** 处；`grep -rn 'run\.lock\.meta' internal/` 恰 **0** 处；`TestLockMutualExclusion` / `TestLockInodeNeverReplacedWhileHeld` / `TestInodeReplacementCannotYieldSecondLock` | 070 / 071 |
| **A-54** | 锁超时值与等待策略：等多久算超时、超时退 `5` 还是无限等 | ① 默认等待 **10s**（指数退避 + `W28` 留痕），超时退 `5` + `E16`；② 不等待，占用即退 `5`；③ 无限等待 | **①**：默认等待 **10s**，退避序列 `50ms → 100ms → 200ms → 400ms → 800ms → 1s（此后恒 1s）`，每次重试留痕 `W28`；超时退 `5` 并携 `E16`；上限可由 `EG_LOCK_TIMEOUT_MS` 覆盖（**不新增命令、不新增全局 flag**）；③ 在 CI 会挂死 | `internal/txn/lock.go` 的 `AcquireWithTimeout`；`W28` / `E16` 产出点 | `TestLockTimeoutReturnsE16` / `TestW28EmittedOnLockWait`；`e2e m6_run_lock.sh` 退 `0` | 070 / 071 / 073 |
| **A-55** | 崩溃恢复触发时机：自动恢复会在用户不知情时改磁盘 | ① 每个写命令启动时自动恢复 + `W26`；② 只有显式命令能恢复（需新增命令）；③ 自动恢复但需 `--yes` | **①**：恢复是**写命令的启动 hook**（`internal/cli/recover_hook.go`），在解析参数之后、拿锁之后、任何写入之前执行；只读命令**永不触发**（**「写命令」的类别边界见 §16.1**：权威 Markdown 写命令与 index 维护命令 `index build` / `rebuild` / `sync` 都必须先过恢复屏障 `S2`，`index status` / `bench` 等只读命令不拿锁、不触发恢复；**只读命令即便在 `.index/` 出现 runtime reserved entry 类型违规时也绝不退 `5`**，按 §18.1 的 C 类分层走 `W24` / `Q5`）；**恢复被定死为两遍**——Pass A 全量只读判定（路径合法性 + `current_hash` 分类，零写入），仅当全量无 `B-R3` 且路径全合法才进 Pass B 幂等回滚并最后写 `abort`（**严禁边判定边回滚**）；`intent.json` 不可解析 / `journal_version` 不支持 ⇒ **fail closed**（退 `5` + `E15`、原样保留）；无 `intent.json` 的 pre-intent residue 可静默清理；恢复事实由 `W26` 承载并进报告 `warnings[]`；② 撞「命令数恒 22」；③ 撞退出码 `6` 白名单恰两条 | `internal/txn/recover.go` + `internal/cli/recover_hook.go` | `TestRecoverHookOnEveryWriteCommand` / `TestRecoverHookNotOnReadOnlyCommand` / `TestCommandCountStillTwentyTwo` / `TestRecoverIsAllOrNothingAcrossFiles` / `TestRecoverConflictAtLastFileRollsBackNothing` / `TestCorruptIntentFailsClosed` / `TestRecoverRejectsEscapingPaths` | 070 / 072 / 074 |
| **A-56** | 强校验开关形态：默认强校验会打爆 M1 ~ M5 的 50 个既有 e2e | ① 默认强校验 + `--no-strict`；② 默认宽松 + `--strict` 显式开启；③ 无开关一律强校验 | **②**（**owner 口径已定，本行为裁决而非预判**）：默认路径**零漂移**，强校验仅在显式 `--strict` 下生效；`W1/W2/W3/W4/W6` 在 `--strict` 下**升 severity 为 error、码不变**；M1 ~ M5 的 50 个既有 e2e **一条不改**即继续全绿 | `internal/reconcile/strict.go`；各写命令的 `--strict` 布尔位；**不改**任何既有默认退出码 | `TestStrictModeW1W2W3W4W6BecomeError` / `TestStrictModeCodesUnchanged` / `TestNonStrictModeUnchangedFromM4`；`e2e m6_strict_check.sh` 退 `0` | 070 / 074 / 075 |
| **A-57** | 块级三方合并的「安全」判定标准 | ① 「前像 → 当前值」的差异**不重叠于**本次要写的块区间即合并，否则跳过 + `W27`；② 逐行三方合并（引入 diff 依赖）；③ 一律不合并，保持 M3 的「不匹配即跳过」 | **①**：判定粒度**只在块**——以 §7 的四条判据（分区可定位、目标块区间与当期差异块集合**字节区间不相交**、目标块非「用户补充」、当期文件仍是合法 frontmatter）**全真**才算安全；任一不真即跳过并留痕 `W27`；② 撞「直接依赖 ≤ 2」，③ 会让本里程碑第 6 项范围为空 | `internal/mdfile/block_merge.go` + `internal/store/merge.go` | `TestBlockMergeSafeCase` / `TestBlockMergeUnsafeSkipsWithW27` / `TestBlockMergeBlockGranularityOnly` / `TestBlockMergeM3BehaviourPreservedWhenHashMatches` | 070 / 073 |
| **A-58** | 退出码 `5` 的语义边界：「写前复核失败」与「锁不可用」是否共用 `5` | ① `5` = 写前强校验失败 **或** 锁不可用（`E15` / `E16` 区分）；② `5` 只给锁；③ `5` 只给复核 | **①**：**退出码 5 只在写前强校验失败与锁不可用两类情形产出，且零写入**；`E15 precheck_failed` 与 `E16 lock_timeout` 区分原因；与 `2` 的分界是**触发时机**——`2` 是 ChangePlan 自身校验（error 级 E1 ~ E6）失败，`5` 是**写前复核**（含 `--strict` 升级面）或**锁不可用**；**落地形态见 §17（R7 定稿）**：`internal/cli/exitcode.go` 定义常量 `ExitPrecheckOrLock = 5` + 两类类型化错误，`internal/cli/exit.go` 的 `ExitCodeFor` **单点映射**为 `5`，`cmd/eg/main.go` 仍是**全仓唯一** `os.Exit` 调用点，`internal/**` **零** `os.Exit`；**`E15` 的原因清单口径与 reserved entry 类型违规的路径分层见 §18（R8 定稿）**：`E15` = 写前安全复核这一**语义大类**（当前枚举 **8 个原因**，精确数字不作判据），reserved 类型违规**只在 A / B 类写路径**映射为 `5`，**C 类只读路径永不退 `5`**（走 `W24` / `Q5`） | `internal/cli/exitcode.go`（新增 `ExitPrecheckOrLock = 5`；`ExitCode5Enabled` 由恒 `false` 改为恒 `true`）；`internal/cli/exit.go`（`ExitCodeFor` 增恰两条分支）；`internal/plan/precheck.go` | `TestPrecheckFailureExits5` / `TestExit5MeansZeroWrite` / `TestExit5NeverOnReadOnlyCommands` / `TestLockUnavailableExits5`；机器验证四条（§17.2）：常量 `ExitPrecheckOrLock` **值恰 5**、两类类型化错误经 `ExitCodeFor` **各映射为 5**、`grep -rn 'os\.Exit(' internal/ | wc -l` 为 **0**、`grep -c 'os\.Exit(' cmd/eg/main.go` 为 **1** | 070 / 073 / 074 |
| **A-59** | `txn_id` 格式与报告填充口径：§4.6 逐字「可省略」，M6 改必填是否算改历史合同 | ① `t` + 16 位小写十六进制，报告必填；② 沿用 Git 短 hash；③ 继续可省略，只在事务日志里可见 | **①**：`txn_id` 恒匹配 `^t[0-9a-f]{16}$`，**分配算法定死为「持锁后按日志序号 + 进程游标分配」**（R5 定稿）：取 `seq = max(.index/txn/seq 持久计数器值, .index/txn/ 下现存目录名解出的最大序号, **process-local lastSeq**) + 1`，`txn_id = "t" + seq 的 **16 位**零填充小写十六进制`（**不再**拼接 Unix 秒——旧「8 位 seq + 8 位秒」在 `seq > 0xFFFFFFFF` 后会溢出并破坏该正则）；`seq` 超 `uint64` 上限时**不回绕不截断**，直接 fail closed：零权威写入、退 `5` + `E15`；事务目录以 `O_EXCL` 创建，撞名则 `seq + 1` 重试，成功后原子更新 `seq` 文件与 `process-local lastSeq`。**保证域被显式限定为「当前 `.index/txn` 生命周期内」**：在该生命周期内**同一 vault 唯一 + 按字典序单调递增（跨进程亦成立，因分配只发生在已持有 `run.lock` 的临界区内）**；由于 `.index/` 在**无未闭合事务时可被合法删除**（M5 的「派生物可重建」口径不变），删除会使 `seq` 与现存目录一起消失、序号从头开始，故**不声称跨生命周期的永久唯一或永久不减**——该限制在 §4 与 `075` 的对外文档在册。同时**无条件保证 `071` 要求的「同进程内唯一且单调」**——该保证由公式中的 `process-local lastSeq` 项**直接给出**（磁盘 `seq` / 目录被清理或改小均不影响），不是旁注；最终报告**必填**。「可省略 → 必填」是**收紧**，既有键名与类型不变，**不破坏任何既有消费方**，故不算改历史合同；② 在提交失败时无值，③ 无法让报告与日志对账 | `internal/txn/id.go`（含 `.index/txn/seq` 持久计数器）；`internal/report` 的 `txn_id` 填充 | `TestTxnIDUniqueMonotonic` / `TestReportTxnIDPopulated` / `TestReportTxnIDMatchesJournal`；`jq -e '.data.txn_id \| test("^t[0-9a-f]{16}$")'` | 070 / 072 / 075 |
| **A-60** | S1 收口版本号：`0.6.0-m6` 还是 `1.0.0` | ① `0.6.0-m6` + 逐字列出三项已知限制；② `1.0.0-rc1`；③ `1.0.0` | **①**（**owner 口径已定，本行为裁决而非预判**）：S1 收口版本号取 **`0.6.0-m6`**，**不宣称 1.0**；README / INSTALL / `SKILL.md` 逐字列出三项已知限制（macOS 产物仅交叉编译未真机验证；网络盘 / 同步盘不保证原子性；存在未闭合事务时不可删 `.index/`） | `internal/version/version.go` / `Makefile` / `skill/SKILL.md` / README / INSTALL 五处同真（由 `075` 落地） | `grep -c '0.6.0-m6' internal/version/version.go` ≥ 1 且 `grep -c '0.5.0-m5'` 于版本真源 == 0；dist `--version` 的 `commit` == `git rev-parse --short HEAD` | 075 |

**裁决可判性自证**：上表恰 **9** 行；每行「裁决」列取值均落在该行「封闭选项」之内；全表**零** `未知 / 待 owner 确认`；
因此 `071` / `072` / `073` / `074` 的 DoR 阻断条件**全部不成立**（逐项对照见 §14）。

---

## 2. 事务边界与临界区（范围①的落地口径，A-52）

- **一条写命令 = 一个事务**。写命令恰为「会改动 `vault/` 下权威文件或 Git 历史」的那些命令；只读命令**不开事务、不拿锁、不触发恢复**。
- 事务的**阶段序列**固定如下，`P1` ~ `P9` 的崩溃点编号与 §6 矩阵逐行对应：
  1. **S0 准备（锁外，能力被严格限定为「参数解析 + 候选提示」）**：**只允许**做「参数解析 / 用法校验」与一次**提示性的候选发现**
     （遍历路径、按条件粗筛，用于进度显示与工作量预估）。**锁外读到的任何东西都只是提示**——既可能落在另一个写者 `S5` 的
     `rename` 序列中间（**混合快照**），也可能**在 S0 之后**被另一写者**新增或删除匹配文件**（集合本身漂移，不只是内容漂移）。
     因此 `S0` **不得**最终化 ChangePlan、**不得**把锁外算出的 `content_hash` 用作 B3 基准、**不得**据锁外读定盘 accepted write-set，
     更**不得**用 `S0` 的候选集合去**限定** `S3` 的输入范围。锁外算出的 hash 与候选集**一律**在 `S3` 被丢弃重算；
  2. **S1 取锁**（`flock` on `vault/.index/run.lock`）——临界区开始；
  3. **S2 恢复屏障 / 恢复 hook**（扫描 `.index/txn/`，回滚未闭合事务，留痕 `W26`）——**先于任何重读**，保证后续重读不会读到半应用态；
  4. **S3 重新候选发现 + 重读 + 最终化 + 写前强校验 + preflight 定盘**（临界区内，`--strict` 时含升级面；失败退 `5` + `E15`，零写入）。
     本阶段**必须**按以下次序执行，缺一步即违约：
     ⓪ **在锁内重新执行一次完整的候选发现**（按同一命令语义重新遍历并筛选，得到 `S3` 自己的候选集合）——
     `S0` 的候选集合**只能**用于提示与对账（差异可进人类可读输出），**绝不**作为 `S3` 的输入边界；
     理由：另一写者可能在 `S0` 之后**新增**匹配文件（漏做）或**删除** / 改名匹配文件（对不存在的路径做计划）；
     ① **重新读取全部权威输入**（`S3` 候选集合内每个 Markdown 的全文与 frontmatter，以及本次判定需要的关系 / 状态输入），
     **不得**复用 `S0` 的任何读结果与缓存；② 基于重读结果**重算 `content_hash`（B3 基准）与块 / 分区 `Span`**；
     ③ **重算并最终化 ChangePlan**（含 op 集合与每个 op 的目标字节）；④ 执行写前强校验（含 `--strict` 升级面）；
     ⑤ **定盘**两个在本次事务内不再变化的集合：**accepted write-set**（本次事务的原子域）与 **skipped 集合**（B3 前像不匹配、块级合并不安全等，见 §5）。
     **理由（`L2` 的成立条件）**：只校验「hash 未变」不足以排除混合快照——`S0` 期间被观察的多个文件可能分别来自另一写者
     `rename` 序列的前后两侧，逐文件 hash 校验会全部通过而计划本身建立在不一致的跨文件视图上；
     而只重读「`S0` 候选集」连**集合层面的漂移**都发现不了（新增文件根本不在集合里，逐文件校验永远不会触发）。
     **只有「持锁 + 恢复屏障之后**重新发现 + 重读 + 重算**」才能让本次事务的输入快照（含集合与内容两层）落在一个串行点上**，
     `L2` 与 accepted write-set 的原子性才成立。
  5. **S4 写 intent 日志**（`.index/txn/<txn_id>/intent.json`，含 accepted write-set 的全部路径与**前像** `content_hash`）；
  6. **S5 落盘**（对 accepted write-set 逐文件 `tmp` + `fsync` + 原子 `rename` + 目录 `fsync`）；
  7. **S6 写 `commit` 标记**（原子 `rename`）——**Markdown 事务的提交点，且是唯一提交点**；
  8. **S7 Git 提交**（**持锁下的后提交步骤**，失败退 `4`，磁盘保留当前状态）；
  9. **S8 释放锁 → S9 清理日志**（临界区在 S8 结束）。
- **临界区覆盖「重新候选发现 + 重读 + 校验 + 落盘 + 提交」**（S1 ~ S8），**不覆盖**参数解析、`S0` 的提示性候选发现与报告渲染；
  `S0` 与报告输出在锁外，保证锁持有时间与**候选文件数**成正比而非与命令生命周期成正比。
  代价是 `S3` 的**重新发现与重读必然发生在锁内**：这是 `L2` 不可省的成立条件（见上），
  **不得**以「缩短临界区」为由把它们挪到锁外，也**不得**用 `S0` 的结果替代。
  短临界区的达成手段是**缩小候选范围（更精确的路径 / 条件参数）**与**不在锁内做网络 / 渲染 / 交互**，**不是**省掉重新发现与重读。
- **提交点唯一且只属于 Markdown**：`commit` 标记是否存在，是「本事务的 Markdown 写入已生效 / 未生效」的**唯一判据**。
  标记之前崩溃一律回滚到前像；标记之后崩溃一律保持已提交态。
- **Git 提交不是提交点的组成部分**：`S7` 在 `S6` 之后执行，因此 Git 成功与否**不改变** Markdown 的生效结论。
  `S7` 失败**不回滚** `S5` 的落盘结果——**退出码 `4` 的逐字语义「磁盘保留当前状态」一字不改**，未提交变更清单与原因进报告。
  换言之：**Markdown 事务提交点 = `commit` 标记；Git 提交是持锁下的后提交步骤，失败退 `4` 且不回滚 Markdown。**
  A-52 选项①中「Git 提交在事务内」在本合同中只读作「Git 提交发生在同一命令、同一把锁的临界区内」，
  **不**读作「Git 参与决定 Markdown 是否生效」。**任何阶段任何授权都不做破坏性还原。**

---

## 3. `run.lock` 与短临界区锁形态（范围②，A-53 / A-54）

- **路径**：`vault/.index/run.lock`（唯一）。`.index/` 不存在时由锁层创建目录；`.gitignore` 已覆盖 `.index/`，锁文件**永不进 Git**。
- **机制**：`flock`（`syscall.Flock` + `LOCK_EX|LOCK_NB` 轮询），单机多进程互斥；**不使用 pid 文件比较作为互斥手段**（仅作展示）。
- **锁 inode 不可替换（P0 硬约束，先于一切正文写法）**：`flock` 是**加在 inode 上**的，不是加在路径上的。
  因此 **在持锁期间，`vault/.index/run.lock` 的 inode 绝对不得被 `rename` 覆盖、不得被 `unlink`、不得被重建**。
  **反例即事故**：若持锁者用 `tmp` + `rename` 覆盖锁文件正文，路径会指向**新 inode**，旧 inode 上的 `flock` 只剩持锁者自己持有；
  另一进程随后 `open(run.lock)` 得到**新 inode** 并能**立即获得 `flock` 成功** ⇒ **两个写者同时进入临界区（双写）**。
  由此定死三条禁令：① **禁止**对 `run.lock` 使用 `tmp` + `rename`；② **禁止**在持锁期 `unlink` / `os.Remove` 锁文件
  （含「清理陈旧锁」「重置锁」等任何理由）；③ **禁止**先 `Release` 再重建锁文件来写正文。
  锁文件由 `OpenFile(run.lock, O_CREATE|O_RDWR, 0644)` 获得**长驻 fd**，`flock` 加在该 fd 上，直到 `S8` 释放。
- **持有者信息（写法被 inode 约束反向定死）**：恰三项 —— `pid`、`acquired_at`（RFC3339，UTC）、`argv`（命令行），
  与 `T-…-071` 原合同「pid + 时间戳 + 命令行」逐字一致；**仅供人读与诊断，不参与互斥判定**。
  写入方式**只能**是**通过已加锁的同一个 fd 原地写**：`Ftruncate(fd, 0)` → `Pwrite(fd, body, 0)` → `Fsync(fd)`（**不换 inode**）。
  **允许非原子**：正文是纯诊断，读者可能读到被截断或半写的 JSON，因此**读侧必须容错**（解析失败按「正文不可用」处理，
  绝不因此阻塞或改变互斥判定）。**初次写入不含 `txn_id`**（`txn_id` 在持锁之后才分配，见 §4，写锁正文时它还不存在）；
  `txn_id` 分配完成后**可以**用**同一把 fd 的 `Ftruncate` + `Pwrite` + `Fsync` 原地补写**一次（可选、纯诊断，
  失败只记人类可读输出，**不影响**互斥、不产诊断码、不改退出码）。**不写 `host`**——见下条陈旧锁判据。
  **本形态是唯一形态（R5 定死，`071` 无选择权）**：`run.lock` **既是** `flock` 载体**又是**诊断正文载体，正文只能由**同一把已加锁 fd 原地写**。
  曾作为备选提出的「`run.lock` 恒空 + 独立 `run.lock.meta`」形态**在本合同被撤销**，理由是它给实现留下两条路径、
  让「被加锁 inode 零替换」这条 P0 硬约束的静态反证（对 `run.lock` 的 `os.Rename` / `os.Remove` 恰 0 处）出现分叉；
  `071` **不得**引入 `run.lock.meta` 或任何其他锁元数据旁路文件（静态反证：`grep -rn 'run\.lock\.meta' internal/ | wc -l` 恰 **0**）。
- **等待与超时**：默认 **10s**；退避序列 `50ms → 100ms → 200ms → 400ms → 800ms → 1s（此后恒 1s）`；每次重试留痕 `W28 lock_wait_retry`；
  超时退 `5` 且携 `E16 lock_timeout`，**零写入**。`EG_LOCK_TIMEOUT_MS` 可覆盖上限（不新增命令、不新增全局 flag）。
- **互斥的唯一真源是内核 `flock`（陈旧锁语义就此定死）**：
  1. **`flock` 成功 ⇒ 即可安全持锁**。内核在持有者进程退出（含 `SIGKILL`）或 fd 关闭时自动释放 `flock`，因此「拿到了 `flock`」
     已经等价于「没有活着的写者持锁」。此时锁文件**正文**里的 `pid` / `acquired_at` / `argv` 只是**陈旧诊断信息**，
     一律**原地覆盖**（`Ftruncate` + `Pwrite` + `Fsync`，**同一个已加锁 fd、同一个 inode**，**绝不** `rename` / `unlink`），
     **不做任何额外校验**——**不比 `pid` 是否存活、不比 `host`、不要求 `argv` 可解析**。
     正文不可解析、为空、被截断、来自另一台机器、来自被复制或迁移过的目录，**都不构成阻塞**。
  2. **`flock` 忙 ⇒ 绝不强抢**。此时一律走 §「等待与超时」：退避重试 + `W28`，超时退 `5` + `E16`、零写入。
     **严禁**依据 `pid` 不存在、`host` 不同、`acquired_at` 过久等正文线索去删除锁文件或强行获取（跨容器 / 跨命名空间下
     `pid` 与 `host` 都可能误判，删锁即制造双写）。
  3. **接管后、任何写入之前，必须先跑恢复屏障**（`S2`），因此前一次崩溃留下的未闭合事务先被回滚。
  4. **接管只原地重写锁文件正文（不换 inode、不删锁文件），绝不删除任何权威文件**；**接管本身不产出 `W26`**——`W26 txn_recovered` 严格限定为
     「确实检出并回滚了一个未闭合事务」，覆盖陈旧 `run.lock` 的持有者元数据**不是** `txn_recovered`，不留任何诊断码
     （需要时只进人类可读输出的调试行）。
  由此消除一类死锁：**hostname 变化、vault 目录被复制 / 迁移、容器重建后遗留的已解锁 `run.lock` 正文永不阻塞后续写入**
  （因为它们的 `flock` 早已被内核释放，而正文不参与判定）。
- **panic / 信号安全**：锁在 `defer` 中释放（`Flock(LOCK_UN)` 后 `Close(fd)`，**不删文件**）；进程被 `SIGKILL` 时 `flock` 由内核释放，
  残留的锁文件正文不构成互斥，由下一次接管**原地**覆盖。锁文件**长期存在于磁盘上是正常态**，不需要也不允许被清理。
- **锁粒度**：整 vault 一把锁 + 短临界区。**不按文件加锁**（多文件原子提交下会死锁），**不依赖 `content_hash` 兜底替代锁**
  （M1 的 B3 只防「读后被改」，不防「两个进程同时写」；M6 是**加强**而非替换 B3）。

---

## 4. 事务日志 `.index/txn/` 的格式与保留策略（范围③，A-59）

- **目录布局**：`.index/txn/<txn_id>/{intent.json,commit,abort}`，另有前像副本目录 `pre/`、**新建文件回滚隔离区 `quarantine/`**（§4.1）与全局持久计数器文件 `.index/txn/seq`。
  `commit` / `abort` 为**零字节标记文件**，落盘序**逐字定死为**：`创建 tmp` → **`fsync(tmp)`** → `rename(tmp → commit|abort)` → **`fsync(.index/txn/<txn_id>/ 目录)`**。
  只有这四步全部返回成功，才允许认为标记已持久；缺 `fsync(tmp)` 或缺目录 `fsync` 的实现视为违约（`rename` 只保证名字替换原子，不保证内容与目录项已落盘）。
- **`txn_id` 分配（A-59 的实现口径，只发生在持锁之后；R5 已把进程游标写进公式）**：
  1. **三源取最大**（缺一不可）：
     `seq = max( 读 .index/txn/seq 得到的持久值, 扫描 .index/txn/ 现存目录名解出的最大序号, process-local lastSeq ) + 1`。
     其中 `process-local lastSeq` 是**本进程内**上一次成功分配的 `seq`（进程启动时为 `0`，每次分配成功后原子更新）。
     它**必须进入公式**——否则「同进程内无条件唯一且单调」就只是旁注而非算法后果：磁盘上的 `seq` 与现存目录都可能被
     保留策略清理、被 `.index/` 合法删除、或被外部工具改小，只有进程游标能在这些情况下继续为本进程提供单调性。
  2. **格式**：`txn_id = "t" + <seq 的 16 位零填充小写十六进制>`，即恒匹配 `^t[0-9a-f]{16}$`。
     **R5 撤销**旧的「8 位 `seq` + 8 位 Unix 秒」拼接：一旦 `seq > 0xFFFFFFFF`，`seq` 段会溢出 8 位宽度而**破坏该正则**，
     使报告校验与目录寻址同时失效。改为把全部 16 位十六进制位留给 `seq`（可表示到 `uint64` 上限），
     时间信息不再进入 ID（分配时刻本就已由 `intent.json` 与文件系统 mtime 记录，无需冗余编码）。
  3. **溢出 fail closed**：若 `seq` 计算结果超过 `uint64` 上限（即无法用 16 位十六进制无损表示），
     **不得**回绕、不得截断、不得改格式——直接 **fail closed**：不创建事务目录、**权威零写入**、退 `5` + `E15`（属 A-58 第一类，见 §9）。
  4. 事务目录以 `O_EXCL` 创建，撞名则 `seq + 1` 重试；创建成功后以 `tmp` + `rename` 原子更新 `.index/txn/seq`，并更新 `process-local lastSeq`。
  由此在**当前 `.index/txn` 生命周期内**：**同一 vault 唯一**（持锁串行 + `O_EXCL` 双重保证）、**按字典序单调递增**
  （`seq` 单调 + 定宽零填充使字典序与数值序一致，且持久计数器保证**保留策略清理旧事务目录后**序号不回退——清理**不重置** `seq`）。
  **不使用**「时钟高位 + 随机低位」这类无法由实现保证单调与跨进程唯一的方案。
- **`txn_id` 唯一性 / 单调性的保证域（如实限定，不做超额承诺）**：
  1. **无条件成立**：**同一进程内唯一且单调**（进程内自增游标，`071` 的原始要求，与磁盘状态无关）。
  2. **限定成立**：**同一 vault、当前 `.index/txn` 生命周期内**跨进程唯一且字典序单调不减。
     「生命周期」= 从 `.index/txn/` 被创建（或被合法删除后重新创建）起，到它下一次被整体删除为止。
  3. **明确不保证**：跨生命周期的永久唯一与永久不减。理由是 M5 冻结口径允许「无未闭合事务时删除 `.index/`」，
     删除会同时带走 `seq` 与全部事务目录，重建后序号从头开始，**可能产生与历史 `txn_id` 重复的值**。
     本合同**不**为此引入 vault 级持久 ID 真源（那会把一个不可重建的状态写进 `.index/` 之外，与 F1 冻结布局及
     「`.index/` 可删」两条口径同时冲突），而是**如实登记**：`txn_id` 只用于**当前生命周期内**的报告 ↔ 日志对账与目录寻址，
     **不得**被任何消费方当作跨生命周期的全局主键或审计唯一键（报告 / 日志之外零消费方，见 §14）。
  4. 该限制由 `075` 在对外文档中与「存在未闭合事务时不可删 `.index/`」一并如实登记。
- **`intent.json` 字段**（只增不改）：

| 字段 | 类型 | 语义 |
|-|-|-|
| `txn_id` | string | `^t[0-9a-f]{16}$`，与报告 `txn_id`、目录名逐字相等 |
| `started_at` | string | RFC3339 UTC |
| `argv` | array | 命令行（诊断用，不参与判定） |
| `files[]` | array | **accepted write-set**（本次事务的原子域）；每项 `{path, pre_hash, pre_size, pre_bytes_ref, target_hash, target_size, create, target_op}`。`pre_hash` / `pre_size` / `pre_bytes_ref` 描述**前像**（`pre_bytes_ref` 指向 `.index/txn/<txn_id>/pre/<n>` 的前像字节副本）；**`target_hash` / `target_size` 描述本事务将要写入的目标态字节**（`S3` 最终化 ChangePlan 后即可算出，**必须与前像一并在 `S4` 落盘**，是 §4.1 三分支判定的必要输入）；**新建文件**：`create: true` 且 `pre_hash` 为空串、无 `pre_bytes_ref`（`create: false` 为改写既有文件） |
| `skipped[]` | array | S3 preflight 定盘的跳过集合，每项 `{path, kind}`（`kind` 沿用既有 2 值，**不扩张**）；**不属于原子域**，只用于报告与审计 |
| `git` | object | `{expect_commit: bool}`——是否期望本事务在 `S7` 产出一次 Git 提交（Git 结果不影响 Markdown 生效结论） |
| `journal_version` | number | 恒 `1`；**不支持的版本 ⇒ fail closed**：恢复层拒绝解释该目录、**不猜测、不回滚、不闭合**，并按 §4.2 第二行以 `E15` + 退出码 `5` **阻断本次写命令**（权威文件零写入） |

- **安全序（本合同的核心不变式之一）**：**intent 日志写入先于任何权威文件写入**。`intent.json`（含每项的 `pre_hash` 与 `target_hash`）
  与全部前像副本 `fsync` 落盘之后，才允许发生第一个权威文件的 `rename`。
- **intent 发布屏障（`S4` 的逐字步序，R4 定死；「发布」= 由 `rename` 一次性变为可见且完整）**：
  `intent.json` **必须**以「先备料、后一次性发布」的方式落盘，使**磁盘上永不存在「路径叫 `intent.json` 但内容不完整」的中间态**：
  1. 创建事务目录（`O_EXCL`）与 `pre/`；
  2. 逐个写入全部前像副本 `pre/<n>`：`write` → **`fsync(pre/<n>)`**（每个副本单独 `fsync`，不允许只在末尾统一刷）；
  3. **`fsync(.index/txn/<txn_id>/pre/ 目录)`** 与 **`fsync(.index/txn/<txn_id>/ 目录)`**——此刻全部前像字节及其目录项均已持久；
  4. 写 `intent.json.tmp`（同目录内，全量 JSON 一次写完）→ **`fsync(intent.json.tmp)`**；
  5. **`rename(intent.json.tmp → intent.json)`** → **`fsync(.index/txn/<txn_id>/ 目录)`**。
  **只有第 5 步全部返回成功之后，才允许发生本事务的第一个权威文件 `rename`**（`S5` 的第一次落盘）。
  由此得到 R4 要求的**可证明性质**：
  - 「`intent.json` 在盘且完整」⇒ 全部前像字节**必已**先于它持久（第 2 ~ 3 步在第 4 ~ 5 步之前），恢复所需输入完备；
  - 「`intent.json` **不在盘**」（pre-intent residue，§4.2）⇒ 屏障未完成 ⇒ **必然零权威文件写入**，故 residue 可安全静默清理；
  - 「路径 `intent.json` 存在」永不意味着「半个 JSON」——半写内容只会出现在 `intent.json.tmp` 上，而 `.tmp` **不被**恢复层解释
    （恢复层只 `open` 精确文件名 `intent.json`；遗留 `.tmp` 按 residue 的自有垃圾一并清理，不产任何诊断码）。
  **反面即事故**：若直接以 `O_CREATE` 追加方式写 `intent.json`，崩溃会留下**半个 JSON**——它既不能证明「无权威写入」（可能已有 `rename` 发生），
  又无法解析出 `files[]`，恢复层将失去唯一的判定输入。这正是 §4.2 第二行必须 **fail closed** 的原因。
- **`commit` / `abort` 语义**：`commit` 存在 ⇒ 本事务的 Markdown 写入已生效，恢复层**不得**回滚、**不产出 `W26`**；
  `abort` 存在 ⇒ 事务已主动放弃**且回滚已全部完成并落盘**；两者都不存在 ⇒ **未闭合事务**，恢复层按前像回滚并产出 `W26`。
  Git 提交是否成功**不写入**这三个标记，也**不改变**上述判定。
- **回滚 / 闭合的唯一顺序（崩溃安全 + 全量先判定，`abort` 只能最后写）**：无论是**主动放弃**（`S5` / `S6` 出错）还是**恢复屏障**（`S2` 处理未闭合事务），
  顺序**逐字定死为**：① **保持事务未闭合**（此刻绝不写 `abort`）→ ② 执行 §4.1 的**两遍恢复**——
  **Pass A（只读全量判定，零写入）**先遍历 `files[]` **全部**条目做路径合法性校验（§4.3）与 `current_hash` 重算并逐项分类为 `B-R1` / `B-R2` / `B-R3`；
  **只要存在任意一项 `B-R3`（或任意一项路径违规），整个恢复立刻零写入终止**、保持事务未闭合、不写 `abort`、退 `5` + `E15`；
  **Pass B（仅当 Pass A 全量无冲突才进入）**再对 `B-R2` 项**幂等回滚前像**（每个文件 `tmp` + `fsync(tmp)` + `rename` + `fsync(所在目录)`），
  `B-R1` 项零写入跳过，`create: true` 项按 §4.1 移入 `quarantine/`
  → ③ 只有**全部文件都已回到前像**（含「本就等于前像、无需写」的那些）才**原子写 `abort`**
  （`tmp` → `fsync(tmp)` → `rename` → `fsync(.index/txn/<txn_id>/ 目录)`）。
  **严禁「边判定边回滚」**：那样在 `files[]` 顺序靠后的条目才冲突时，靠前的 `B-R2` 已被写盘，`E15` + 退 `5` 的
  「**零写入**」承诺当场作废（详见 §4.1 的两遍模型与反证用例 `TestRecoverIsAllOrNothingAcrossFiles`）。
  **反面即事故**：若先写 `abort` 再回滚，回滚中崩溃会让下一次启动把它当**已闭合**事务而**跳过剩余恢复**，半应用态被永久固化。
  由此得到的性质：**回滚过程中的任意次崩溃都安全**——事务仍未闭合，下一次写命令会**重复同一流程**；
  回滚按「目标字节 == 前像字节则跳过」实现，故重复执行结果相同（**幂等**）。
### 4.1 两遍恢复（Pass A 全量只读判定 / Pass B 全量幂等回滚）与逐文件三分支

**问题**：崩溃发生后、下一次写命令到来前，磁盘处于「无锁无守护」状态，用户**完全可能**用编辑器修改本事务涉及的文件。
若恢复层**盲目**把 `pre_bytes_ref` 的前像字节写回去，就会**静默覆盖这段崩溃后的新编辑**——这直接违反
「**权威 Markdown 一个字节都不错写**」与「**任何阶段任何授权都不做破坏性还原**」。因此恢复层**必须**先判断当前字节属于哪一分支。

**恢复被定死为「两遍、全量先判定」，`files[]` 是一个不可分割的判定单元（R4 P0）**：

| 遍 | 允许的操作 | 输入 | 终止条件 |
|-|-|-|-|
| **Pass A（只读判定，严格零写入）** | 只做：① 对 `files[].path` 与 `files[].pre_bytes_ref` 做 §4.3 路径合法性校验；② **前像可用性校验（R5 新增，见 §4.1.1）**：`create: false` 的每一项，其 `pre_bytes_ref` **必须存在、必须是普通文件（非目录 / 非 symlink / 非设备）、且全文读出的内容哈希逐字等于 `pre_hash`（`pre_size` 亦须相符）；③ `stat` + **全文读盘**重算每一项的 `current_hash`；④ 按下表把**每一项**分类为 `B-R1` / `B-R2` / `B-R3`。**不得** `write` / `rename` / `unlink` / `truncate` 任何文件（含 `abort` 标记与 `quarantine/` 移动） | `intent.json` 的**全部** `files[]` 条目（**不允许提前 break**，除路径违规直接判失败外必须走完全表以便报告列全冲突项） | 全量分类完成 |
| **Pass B（写盘回滚，仅当 Pass A 判定为「全量可恢复」才进入）** | 按 §4 的顺序逐项处置：`B-R1` 零写入跳过、`B-R2` 幂等回滚前像、`create: true` 原子移入 `quarantine/`；全部 `fsync` 完成后**最后**写 `abort` | Pass A 的分类结果 | 全部条目处置完毕并 `abort` 落盘 |

- **判定与写盘之间是硬屏障**：`Pass A` 的结论是「**全量可恢复**」还是「**存在冲突**」这**一个整体判定**，
  `files[]` **要么整体进入 Pass B、要么一个字节都不写**。
- **任意一项 `B-R3`（或任意一项路径违规）⇒ 整个恢复零写入**：保持事务未闭合、不写 `abort`、不动 `quarantine/`、
  退 `5` + `E15`，并在报告中**列出全部**冲突项（不是只报第一个）。
- **与文件顺序无关（顺序独立性）**：冲突项排在 `files[]` **首位、中间还是最末**，可观察结果**逐字相同**——
  磁盘零变化、事务未闭合、`E15` + 退 `5`。由 `TestRecoverIsAllOrNothingAcrossFiles`
  与 `TestRecoverConflictAtLastFileRollsBackNothing` 两条反证钉死（见 §14 / `T-…-072`）。
- **反面即事故（R4 阻断点原文）**：若「逐文件判定后立即回滚」，当冲突项排在末尾时，前面的 `B-R2` 已被写回前像，
  此时再退 `5` 并声称「零写入」是**假承诺**：磁盘已被改动、`git status --porcelain` 已与执行前不同，
  且用户的部分文件已被静默还原。故该实现形态**严禁**。

判定输入：`current_hash`（Pass A 中**重新读盘**算出）、`pre_hash`、`target_hash`（三者都来自 `intent.json` 与实盘，**不猜测**）。
**分支封闭为恰三支，无第四种处置**：

| 分支 | 条件 | 处置 | 是否写盘 | 事务闭合 | 诊断码 / 退出码 |
|-|-|-|-|-|-|
| **B-R1 无需恢复** | `current_hash == pre_hash` | 该文件本就是前像（`rename` 未发生或已被回滚）：Pass B 中**零写入**跳过，直接视为已回滚 | 否 | 参与正常闭合 | 无（该文件不单独产码） |
| **B-R2 可安全恢复** | `current_hash == target_hash` | 该文件确是**本事务写出的目标态**，**Pass B** 中可安全还原前像：`tmp` + `fsync(tmp)` + `rename` + `fsync(目录)`（Pass A 中**绝不**动它） | 是（**仅在 Pass B**） | 参与正常闭合 | 计入 `W26`（整事务一条） |
| **B-R3 外部编辑冲突** | `current_hash` **既不等于** `pre_hash` **也不等于** `target_hash` | 判为 **post-crash 外部编辑冲突**：**绝不覆盖、绝不删除、绝不合并**；**整个恢复（含同事务其余全部 `B-R2` 条目）直接零写入终止、不进入 Pass B**；**保持事务未闭合**（不写 `abort`、不写 `commit`）；**阻断本次写命令**并把**全部**冲突路径与三个哈希如实进报告，供用户自行处置（人工确认后可自行把文件改回目标态或前像，再重跑写命令） | **否**（**整事务零写入**，`git status --porcelain` 与执行前逐字相等） | **保持未闭合** | **`E15 precheck_failed` + 退出码 `5`** |

- **`B-R3` 归属与退出码合规**：恢复屏障（`S2`）是写命令的**写前安全复核**的一部分，因此 `B-R3` 的阻断**属于 A-58 的第一类
  「写前强校验失败」**，用**既有** `E15 precheck_failed` + 退出码 `5` 表达，**零写入**。
  **不新增任何诊断码**（M6 新增诊断码恒恰 5 条：`E15` / `E16` / `W26` / `W27` / `W28`），**不新增退出码**（全集仍恰 `{0,1,2,3,4,5,6}`），
  `5` 的两类情形语义一字不改。**`B-R3` 不发 `W26`**——没有发生任何恢复。
- **阻断是「阻断新写」而不是「放弃事务」**：未闭合事务**继续留在盘上**，`W26` 也不产出；后续每一次写命令都会再次撞上同一 `B-R3`
  并再次退 `5`，直到用户消除冲突。这保证「**不错写**」优先于「**能继续跑**」。
- **新建文件（`create: true`）的回滚不得物理丢字节**：新建文件没有前像，「回滚」= 让该路径**回到不存在**。
  为不违反「**不做破坏性还原、不物理删除**」，处置**定死为**（分类在 Pass A、移动只在 Pass B）：
  1. `current_hash == target_hash`（确是本事务新建的字节）→ Pass A 判为 `B-R2`；**Pass B** 中**原子移入隔离区**
     `.index/txn/<txn_id>/quarantine/<n>`（同文件系统 `rename`，随后 `fsync` 两侧目录），**字节完整保留**、可人工取回；
     移入完成后该文件才算已回滚，随后才允许闭合 `abort`；
  2. 文件**已不存在** → 视为 `B-R1`，零写入；
  3. `current_hash` 既非 `target_hash` 也非「不存在」（即崩溃后被用户编辑过）→ 落 **`B-R3`**：**绝不移动、绝不删除**，
     整事务零写入、保持未闭合并退 `5` + `E15`（同事务其他条目也一律不进 Pass B）。
  隔离区随事务目录一起受保留策略约束（`abort` 闭合后按 7 天 / 20 个清理），清理前**不删**其中字节。
- **恢复层必须重新读盘**：`current_hash` 只能来自 Pass A 的实盘重算，**不得**复用 `intent.json` 之外的任何缓存，
  也**不得**用文件 mtime / size 代替哈希比较（`pre_size` / `target_size` 只作快速预筛，判定仍以哈希为准）。

#### 4.1.1 Pass A 的前像可用性校验（R5 P0：前像不可信即 fail closed）

**问题**：`Pass A` 原先只校验路径与 `current_hash`，却**默认 `pre_bytes_ref` 指向的前像字节是可用且正确的**。
该假设不成立：`pre/` 副本可能在崩溃中只落了部分字节、被外力截断 / 改写 / 换成目录或 symlink、或整体缺失。
若带着一个**损坏的前像**进入 `Pass B`，`B-R2` 的「还原前像」会把**错误字节**写进权威 Markdown——
这是比不恢复严重得多的事故，直接违反「**权威 Markdown 一个字节都不错写**」。

**因此 `Pass A` 必须在分类的同一遍里逐项校验前像本身，且校验只读、零写入**：

| 校验项 | 要求（`create: false` 的每一项） | 违反时的处置 |
|-|-|-|
| **存在性** | `pre_bytes_ref` 指向的文件**必须存在**（`lstat` 成功） | fail closed |
| **文件类型** | 必须是**普通文件**（`mode.IsRegular()`）——**不得**是目录、symlink、FIFO、设备、socket | fail closed |
| **大小** | `size` 必须等于 `pre_size`（快速预筛，不单独作为判据） | fail closed |
| **内容哈希** | **全文读出后重算的哈希必须逐字等于 `pre_hash`** | fail closed |

- **`create: true` 的项无前像**：按 §4 约定 `pre_hash` 为空串且**无** `pre_bytes_ref`；
  此时**要求 `pre_bytes_ref` 缺席**（若 `create: true` 却带 `pre_bytes_ref`，或 `create: false` 却缺 `pre_bytes_ref`，
  均属**日志自相矛盾**，同样 fail closed）。
- **fail closed 的统一含义**：与 `B-R3` / 路径违规**完全同一处置**——
  **整个恢复零写入、绝不进入 `Pass B`**、保持事务未闭合、不写 `abort`、不动 `quarantine/`，
  退 **`5`** + **`E15 precheck_failed`**，并把违规项路径、期望哈希、实测哈希（或缺失 / 类型错误原因）如实进报告。
  **不发 `W26`**（没有发生任何恢复）。
- **反证**：`TestRecoverRejectsCorruptPreimage`（缺失 / 目录 / symlink / 截断 / 内容篡改各一例，均须零写入 + 退 `5` + `E15`），
  见 §14 与 `T-…-072`。

#### 4.1.2 全局只读分类先于任何回滚写（R5 P0：多事务与损坏并存时整体阻断）

`Scan` 的输出**不止一个事务**：正常协议下同一 vault 最多只应存在**一个** `OpenTxn`（分配在锁内、串行、`O_EXCL`），
但异常来源真实存在——跨机复制 `.index/`、外部工具生成、旧版本残留、手工恢复失误。
**合同不允许把「最多一个」当作可依赖前提去写盘**。因此判定/写盘屏障被**提升到扫描全集这一层**：

1. **全局只读分类阶段**：对 `Scan` 得到的**全部**条目——每一个 `OpenTxn`（逐项跑完 §4.1 `Pass A`）、
   每一个 `CorruptTxn`（§4.2 第二行）、每一个 `PreIntentResidue`——**先全部分类完毕**，全程**严格零写入**。
2. **整体裁决**：只有当「`OpenTxn` **恰一个** 且其 `Pass A` 判定为全量可恢复 且**不存在任何** `CorruptTxn`」时，
   才允许进入 `Pass B`。
3. **否则整体阻断**：只要出现 ①**多于一个** `OpenTxn`、②**任何** `CorruptTxn`、③**任何**一项 `B-R3` / 路径违规 / 前像损坏，
   就在**任何回滚写发生之前**整体阻断——**权威零写入**、全部事务原样保留、不写 `abort`、退 `5` + `E15`，
   报告中**列全**所有问题事务与问题项。
4. **反面即事故（R5 阻断点原文）**：若实现「先恢复第一个 `OpenTxn`，再发现第二个 / 发现 `CorruptTxn` 才返回 `5`」，
   则磁盘**已被改动**在前、退 `5` 在后，「零写入」变成假承诺。故该形态**严禁**。
- **`PreIntentResidue` 不参与阻断**：它已被 §4.2 证明零权威写入，可静默清理 / 就地闭合；
  但清理**只能发生在整体裁决通过之后**（阻断路径下连 residue 目录也不动，以保全人工排查现场）。
- **反证**：`TestMultipleOpenTxnFailsClosedBeforeWrites`——构造两个可解析且**本可回滚**的 `OpenTxn`，
  断言：`git status --porcelain` 前后逐字相等、两个事务目录均无 `abort`、退 `5` + `E15`；
  再构造「一个可回滚 `OpenTxn` + 一个 `CorruptTxn`」，断言同样零写入阻断（**不得**先把可回滚那个恢复掉）。
  即便 `071` 的协议论证表明正常路径下最多一个 `OpenTxn`，**异常多实例仍必须 fail closed**，不得以「不可能发生」省略该分支。

### 4.2 pre-intent residue 与「损坏 intent」的严格区分（前者静默清理，后者 fail closed）

事务目录以 `O_EXCL` **先于** `intent.json` 创建，因此 `P1`（intent 发布屏障完成前被 `SIGKILL`）会留下一个
**只有目录、没有 `intent.json`** 的残留。**它之所以可以被静默清理，唯一依据是 §4 的 intent 发布屏障**：
`intent.json` 由 `rename` 一次性发布，且发布在第一个权威 `rename` 之前 ⇒ **`intent.json` 不在盘 ⇒ 必然零权威文件写入 ⇒ 无任何东西需要回滚**。
反之，**`intent.json` 已在盘但无法解释**（截断 / 非法 JSON / `journal_version` 不支持）时，这个推理**不成立**：
它可能是一个**已经发生过权威 `rename`、随后日志被外力损坏**的事务，此时「无事可做」是**未经证明的乐观假设**。
因此二者**必须严格区分、绝不合并处理**：前者**可静默清理**，后者**必须 fail closed**。

| 磁盘形态 | 判定 | 处置 | 诊断码 |
|-|-|-|-|
| 目录存在，**`intent.json` 缺席**（可能含空 `pre/`、无 `commit` / `abort`） | **pre-intent residue**：由「安全序」保证**此刻绝无任何权威文件写入发生**（intent 先于第一个权威 `rename`），故**无任何东西需要回滚** | **可安全移除 / 就地闭合**：直接删除该 `.index/txn/<txn_id>/` 目录（只删自有目录，权威文件零触碰）；若实现选择保留痕迹，则原地写 `abort` 标记闭合并计入保留策略。二者等价合法，`071` 定稿其一 | **无**（**不发 `W26`**，也不产 `E15`；需要时只进人类可读输出） |
| 目录存在，`intent.json` **在盘但不可解析**（截断 / 非法 JSON / 缺必需字段 / `journal_version` 不支持） | **损坏事务（corrupt txn）**：**可能已发生权威 `rename`**，恢复输入却已丢失 ⇒ **既不能证明安全、也无法安全恢复**；**不猜测、不当作可解析事务** | **fail closed**：① 该目录与 `intent.json` **原样保留**（不删除、不改写、不重写、不参与自动清理）；② **权威文件零写入**（不回滚、不闭合、不写 `abort`、不动 `quarantine/`）；③ **阻断本次写命令**，退 `5` + `E15`，把目录路径与不可解析原因如实进报告，**等待人工处理**；④ 后续每次写命令都会再次撞上并再次退 `5`，直到人工清除 | **`E15 precheck_failed` + 退出码 `5`**（**不发 `W26`**——没有发生任何恢复） |
| 目录存在，`intent.json` 可解析，`commit` / `abort` 皆缺席 | **未闭合事务**（真正需要恢复的唯一形态） | 走 §4.1 **两遍恢复**（Pass A 全量判定 → 无冲突才 Pass B 回滚） | `W26`（Pass B 完成回滚）或 `E15` + 退 `5`（任一 `B-R3` / 任一路径违规，整事务零写入） |

- **禁止把 residue 误当事务**：恢复层的 `Scan` **必须**以「`intent.json` 存在且可解析」为进入 §4.1 两遍恢复的前置条件；
  对 residue **不得**产出 `W26`（没有恢复发生）、**不得**报「日志损坏」错误、**不得**阻断写命令。
- **同样禁止把损坏 intent 当 residue 放行**：判据是**文件是否存在**（`stat(intent.json)`），
  **不是**「是否解析成功」——`stat` 成功但解析失败 ⇒ 走上表第二行 fail closed，**绝不**降级为 residue 静默清理。
  这条区分由反证 `TestCorruptIntentFailsClosed` 与 `TestPreIntentResidueIsNotATransaction` 成对钉死（`T-…-072`）。
- **`seq` 不受影响**：residue 目录名已消耗过一个序号，清理它**不重置** `seq`（与保留策略同口径，见上）。
- **不可删 `.index/` 的限制只针对真正的未闭合事务**：只有 residue（无 `intent.json`）时删除 `.index/` 不损失任何恢复能力；
  存在**损坏 intent** 时同样属于「不可删」范畴（删除等于抹掉唯一的人工排查线索）。

### 4.3 intent 内路径的合同级约束（恢复永不越界）

恢复层是**唯一**会「按日志内容去写盘」的组件，因此 `intent.json` 里的路径字段是一个**必须被当作不可信输入**的攻击面 /
误写面（日志可能被外力编辑、跨机复制、或由旧版本写出）。约束**逐字定死**：

| 字段 | 允许形态 | 拒绝形态 |
|-|-|-|
| `files[].path` | **相对 `vault/` 根的规范化相对路径**（`filepath.Clean` 后与原串**逐字相等**、以 `/` 分隔、非空、不以 `/` 开头、不含 `.` / `..` 分量、且解析后仍位于 `vault/` 之内） | 绝对路径；含 `..` 或 `.` 分量；`Clean` 后与原串不等（未规范化）；空串；Windows 盘符 / UNC；**经 symlink 逃逸出 `vault/`** 的路径 |
| `files[].pre_bytes_ref` | **相对 `.index/txn/<txn_id>/` 的规范化相对路径**，且**必须**位于本事务目录之内（形如 `pre/<n>`） | 绝对路径；含 `..`；指向其他 `txn_id` 目录；指向 `.index/` 之外；**经 symlink 逃逸出本事务目录** |

- **symlink 逃逸的判定手段（实现口径）**：对 `path` 逐层解析（或对最终目标做 `EvalSymlinks` / `openat` + `O_NOFOLLOW` 逐段校验），
  要求**解析结果仍以 `vault/` 的真实路径为前缀**；`pre_bytes_ref` 同理要求以 `.index/txn/<txn_id>/` 的真实路径为前缀。
  写入落盘时**必须**写到校验通过后的那个路径（**先校验后写、且写的就是被校验的对象**，不得校验一个路径又写另一个）。
- **违规即 fail closed**：`files[]` 中**任何一项**路径违规 ⇒ 与 `B-R3` 同一处置——**整个恢复零写入**、保持事务未闭合、
  不写 `abort`、退 `5` + `E15`，并把违规项与原因如实进报告。该校验发生在 **Pass A**，因此**先于任何写盘**。
- **恢复的写入面被合同封闭为两处**：① `files[].path` 校验通过后的**权威文件本体**；② 本事务目录 `.index/txn/<txn_id>/`
  之内（含 `pre/`、`quarantine/`、`abort`）。**除此之外恢复层一个字节都不写**——
  由 `TestRecoverNeverTouchesOutsideTxn` 与 `TestRecoverRejectsEscapingPaths`（绝对路径 / `..` / symlink 三类各一例）钉死。

- **保留与清理**：`commit` / `abort` 闭合后的事务目录保留 **7 天或最近 20 个**（先到者为准）由写命令启动时顺带清理；
  **未闭合事务目录永不自动删除**。清理只删 `.index/txn/` 下的自有目录，**不触碰任何权威文件**，**不重置** `.index/txn/seq`。
- **`.index/txn/` 不是派生索引**：M5 的「Markdown 唯一权威、`.index/` 可重建」在 M6 继续成立，但 `.index/txn/` 是**事务日志而非可重建派生物**
  （`M-006` 风险 **R-31**）。因此：① `eg index rebuild` **不得**删除或重建 `.index/txn/`；② 存在未闭合事务时**不可删** `.index/`，
  该限制由 `A-60` 的三项已知限制逐字对外声明；③ 若用户仍强行删除，恢复能力丢失但权威 Markdown 不受影响（磁盘停在崩溃时的状态）。

---

## 5. 多文件原子提交（范围④）

- **原子域 = accepted write-set**：一次 `eg apply` / `eg proposal approve` / `eg reconcile --repair` 在 `S3` preflight 定盘后，
  其 accepted write-set 内的 N 个 Markdown 文件，**要么全部生效、要么全部不生效**。
  skipped 集合内的文件**不属于原子域**——它们在本次事务中一个字节都不写。
- **实现路径（裁决 ①）**：`tmp` 文件 + `fsync(file)` + 原子 `rename` + `fsync(dir)`，事务日志兜底。
  **不做** vault 整体备份再原地改（违反 §9「不做破坏性还原」），**不依赖** Git 索引做原子性（与退出码 `4` 语义冲突）。
- **顺序**：accepted write-set 内全部目标文件先写成同目录 `*.tmp`（同文件系统，保证 `rename` 原子）→ 逐个 `fsync` →
  逐个 `rename` → `fsync` 目录 → 写 `commit` 标记（标记本身按 §4 的 `tmp` + `fsync(tmp)` + `rename` + `fsync(txn 目录)` 落盘）。
- **幂等**：重复执行同一 ChangePlan 不产生第二次生效、不产生空 Git 提交；`commit` 标记存在的 `txn_id` 再次提交是 no-op。

### 5.1 四级可见性模型（本合同**不**承诺「多文件对任意观察者瞬时同时可见」）

`tmp` + `fsync` + `rename` 是**逐文件**的原子替换，`N > 1` 时 `rename` 序列**不是**一个跨文件的原子操作。
因此保证面必须按观察者分级，逐级都是可实现、可反证的：

| 级别 | 保证对象 | 保证内容 | 机制 | 不保证什么 |
|-|-|-|-|-|
| **L1 单文件原子** | 任意观察者（含编辑器、`rg`、用户脚本、`eg` 只读命令） | 任一 Markdown 文件在任一时刻读到的都是**完整的前像或完整的目标态**，**永不撕裂**（不会读到半写字节、不会读到 0 字节截断） | 同目录 `*.tmp` + `fsync` + 原子 `rename`（POSIX `rename` 覆盖是原子的）；`*.tmp` 后缀不进 Markdown 扫描命名空间 | 不保证多个文件在同一瞬间切换 |
| **L2 协作 `eg` 写者互斥** | 所有走 `eg` 写路径的进程 | 任何两个写事务**串行**执行；后到者要么等待并留痕 `W28`，要么退 `5` + `E16` 且零写入；**没有任何写者能观察到另一个事务的中间态**——它拿不到锁就进不了临界区，且**本次事务的输入快照由 `S3` 在锁内重读产生**（§2），因此不会建立在另一写者 `rename` 序列的混合快照上 | `flock` on `vault/.index/run.lock`（§3）+ `S3` 锁内重读与最终化（§2） | 不约束不走 `eg` 的写者（编辑器直接改盘）与不拿锁的只读命令 |
| **L3 崩溃后一致性（恢复屏障语义）** | 磁盘的**持久态**在**恢复屏障**前后的取值 | 「部分 `rename` 后崩溃」产生的跨文件半应用态**可能一直持续到下一次写命令**（其间进程可退出、机器可重启，只读命令与外部观察者**可以**看到它）；**下一次写命令的恢复屏障（`S2`）完成后，被写文件全部回到前像**；且**任何新写事务都不会建立在半应用态之上**（`S2` 先于 `S3` 重读与 `S4` intent）。`commit` 标记之后崩溃则**保持已提交态** | intent 先写 + 前像副本 + `commit` / `abort` 标记 + 写命令启动恢复 hook（§4 / §6） | **不保证**半应用态在磁盘上「短暂」或「不可见」——它可长期留盘并被非协作观察者读到；只保证**恢复屏障之后**的持久态回到前像，且新事务不叠加在其上 |
| **L4 非协作外部观察者** | 编辑器、`rg` / `grep`、用户脚本、以及**不拿锁的 `eg` 只读命令** | 只享有 **L1**：逐文件不撕裂 | —— | **不保证**跨文件「同时可见」：在 `S5` 的 `rename` 序列执行期间（毫秒级但非零窗口），此类观察者**可能**读到「k 个文件已是目标态、N−k 个仍是前像」 |

**由此本合同明确否弃以下表述**：不得声称「外部观察者（含用户编辑器）永远看不到半应用状态」「多文件对任意观察者瞬时同时可见」
「跨文件半应用态不被持久保留」（它**会**留盘，直到下一次写命令的恢复屏障）。
可实现的完整保证是**四级模型**：`L1` 单文件原子 + `L2` 协作 `eg` 写者互斥 + `L3` 崩溃后一致性 + `L4` 非协作观察者只享 L1。
「**要么全部生效、要么全部不生效**」的**保证面**据此精确定义为：**对协作 `eg` 写者（L2）与磁盘持久态（L3）成立**。

**为什么不换成目录 / 指针切换架构**：要让非协作观察者也获得跨文件瞬时一致，唯一可行形态是「整库写入影子目录 + 一次
`rename` 切换根指针（或 symlink）」。它与 S1 已冻结的三条前提直接冲突：① `vault/` 是 Git 工作树，根指针切换会让每次写入
产出「全库路径变更」的 Git 历史；② F1 冻结目录布局不容许引入影子根与指针层；③ Markdown 唯一权威 + 外部编辑器可直接编辑
的定位要求路径**稳定可寻址**。因此 M6 **不做**根指针切换，改为按四级模型如实承诺——本项差异已按 owner 授权
同步纠正到 `M-006-m6.md` 判据 6 与范围第 4 项、`T-…-072` 的 deliverables 与 Acceptance 措辞（仅改 M6 规划文本，不改 M1 ~ M5 历史）。

### 5.2 B3 跳过与原子性的唯一答案（`skipped[]` / 退出码 `3` 语义不变）

| 问题 | 唯一答案 |
|-|-|
| B3 前像不匹配（文件自读取以来被改）怎么办 | 在 `S3` preflight **定盘**：该文件进 **skipped 集合**（`kind` 沿用既有 2 值），**不进** accepted write-set；不覆盖、不强写、不重试、不排队（M1 的 B3 语义一字不改） |
| 块级合并判定不安全（`W27`）怎么办 | 同样在 `S3` preflight 定盘，进 skipped 集合并留痕 `W27`；**不**触发整事务 abort |
| 有跳过时还提交吗 | 提交。原子性**只覆盖 accepted write-set**：其内 N 个文件全成或全不成；跳过项与它们互不影响 |
| 退出码 | **有跳过且 accepted write-set 非空 → 退 `3`**（「部分写入被跳过，已写入保留并提交，链路继续」的既有语义一字不改）；无跳过且全部成功 → `0`；accepted write-set 为空 → **零写入、不开 intent、不产生空 Git 提交，退 `3`** |
| 是否存在「全事务 abort」路径 | 存在但只在两类情形：① `S3` 的写前强校验失败（退 `5` + `E15`，零写入）；② `S5` / `S6` 期间落盘或标记写失败——此时按 §4「回滚 / 闭合的唯一顺序」执行：**保持未闭合 → 逐文件按 §4.1 三分支判定后幂等回滚前像并 `fsync` → 最后原子写 `abort` 并 `fsync` txn 目录**（**严禁**先写 `abort` 再回滚），本次**零生效**，全部目标逐条进报告 `skipped[]`，退 `3`，链路继续，**不新增码**；回滚中再次崩溃仍安全，下一次写命令重复同一流程；同一命令内的回滚正常不会遇到 `B-R3`（无窗口给外部编辑），但实现**必须**走同一判定，遇 `B-R3` 一律保持未闭合并退 `5` + `E15`。`S7`（Git 提交）失败**不属于** abort 路径——不回滚、退 `4`、磁盘保留当前状态。**跳过本身永远不触发 abort** |

由此 §5 的原子性与 §7 的「跳过不影响其他文件」**不再冲突**：二者的差别只是**是否在原子域内**，
而原子域在 `S3` 定盘后**在本次事务内不再变化**。

---

## 6. 崩溃恢复与「阶段 × 崩溃点 → 恢复后磁盘态 → 退出码」矩阵（范围⑤，A-55）

**恢复触发**：每个写命令启动时（`S2`，即**恢复屏障**）自动扫描 `.index/txn/`，对**未闭合事务**（`commit` / `abort` 皆缺席）按 `intent.json` 的前像
**回滚到前像**，并留痕 `W26 txn_recovered`；只读命令不触发（因此半应用态在**没有写命令**发生时会一直留在盘上，见 §5.1 的 `L3`）。
恢复**只碰本次事务 `files[]`（accepted write-set）内的路径**，绝不触碰事务之外的任何文件。
**恢复的执行顺序与 §4「回滚 / 闭合的唯一顺序」逐字相同，且被定死为两遍**：保持事务未闭合 →
**Pass A：只读遍历 `files[]` 全部条目**，校验路径合法性（§4.3）、重算全部 `current_hash`、逐项分类 `B-R1` / `B-R2` / `B-R3`（**此遍零写入**）→
**Pass B（仅当 Pass A 全量无 `B-R3`、无路径违规才进入）**：对 `B-R2` 幂等回滚前像（逐文件 `tmp` + `fsync(tmp)` + `rename` + `fsync(目录)`）、
`B-R1` 零写入跳过、`create: true` 原子移入 `quarantine/`
→ **全部文件都已回到前像后**才原子写 `abort`（`tmp` + `fsync(tmp)` + `rename` + `fsync(.index/txn/<txn_id>/ 目录)`）。
**任一文件落 `B-R3`（post-crash 外部编辑冲突）或任一路径违规 ⇒ 整个恢复零写入终止（连同事务内其余 `B-R2` 条目一并不写）、
保持未闭合、退 `5` + `E15`，且不发 `W26`**（§4.1 / §4.3）；**严禁**「判一个、回滚一个」——那会在冲突项靠后时把「零写入」变成假承诺。
**恢复过程中再次崩溃可重复同一流程**：因为 `abort` 尚未落盘，事务仍判为未闭合，下一次写命令会重跑同一回滚，
且回滚以「当前字节 == 前像字节则跳过」实现，故**幂等**，重复执行结果相同。
恢复过程本身也在锁内，且**任何阶段任何授权都不做破坏性还原**——回滚的定义是「用日志中的前像字节还原本事务写过的文件」，
不是 `git checkout` / `reset --hard` / 删除文件。

**`W26` 的语义边界（严格四条）**：① `W26` **只**表示「确实检出并**完成**了一个未闭合事务的回滚」；
② **只要 `commit` 标记存在，恢复层就不得回滚，因此也不得产出 `W26`**；
③ 覆盖陈旧 `run.lock` 的持有者元数据、清理已闭合事务目录、Git 侧残留（如 `.git/index.lock`）**都不是** `txn_recovered`，
一律**不产出 `W26`**；④ **`B-R3` 外部编辑冲突、路径违规、损坏 intent 与 pre-intent residue（§4.2 / §4.3）也不产出 `W26`**——
前三者根本没恢复（一律退 `5` + `E15`、零写入），后者没有任何权威写入可回滚（静默清理）。

矩阵的「恢复后磁盘态」列取值**封闭在三值之内**：`回滚到前像`、`保持已提交态`、`磁盘保留当前状态`。

| 阶段 | 崩溃点 | 恢复后磁盘态 | 恢复后退出码 | 承载诊断码 |
|-|-|-|-|-|
| P1 | `S4` 之前：intent **发布屏障完成前**（含取锁后、写前强校验中、`O_EXCL` 建目录后、`pre/` 写入中、`intent.json.tmp` 已写但 `rename` 未成）被 `SIGKILL` | 回滚到前像（此刻权威文件**从未**被写，磁盘本就等于前像；残留目录 + 遗留 `intent.json.tmp` 按 §4.2 作 **pre-intent residue** 安全移除 / 就地闭合） | 下一次写命令退 `0` | 无（无可解释事务、无回滚动作 ⇒ 无 `W26`，也不产 `E15`） |
| P2 | intent 写后、**首个 `rename` 前** | 回滚到前像 | 下一次写命令退 `0` | `W26`（真实回滚：0 个文件需还原，但事务被判未闭合并闭合为 `abort`） |
| P3 | **部分 `rename` 后**（accepted write-set 的 N 个文件中已生效 k 个，0 < k < N） | 回滚到前像 | 下一次写命令退 `0` | `W26`（真实回滚：k 个文件按前像还原，其余 N−k 个零触碰） |
| P4 | **全部 `rename` 后、`commit` 标记前** | 回滚到前像 | 下一次写命令退 `0` | `W26`（真实回滚：N 个文件全部按前像还原） |
| P5 | **`commit` 标记后、Git 提交前** | 保持已提交态 | 下一次写命令退 `0` | 无（`commit` 标记在盘 ⇒ 不回滚 ⇒ **不发 `W26`**）；未提交变更由报告如实呈现 |
| P6 | **Git 提交中**（`.git/index.lock` 可能残留） | 保持已提交态 | 下一次写命令退 `0`；若 Git 侧仍失败则退 `4` | 无（同 P5，**不发 `W26`**）；Git 侧失败时报告记未提交清单 |
| P7 | **锁释放前**（Git 已提交，锁文件残留） | 保持已提交态 | 下一次写命令退 `0`（陈旧锁安全接管） | 无（陈旧锁元数据覆盖**不是** `txn_recovered`，**不发 `W26`**） |
| P8 | **日志清理前**（事务已闭合，旧事务目录未清） | 保持已提交态 | 下一次写命令退 `0` | 无（闭合事务不再恢复，仅顺带清理 ⇒ 无 `W26`） |
| P9 | 非崩溃路径：`S7` Git 提交**失败**（accepted write-set 已生效） | 磁盘保留当前状态 | 本次命令退 `4` | 无新码；未提交清单与原因进报告 |

- **矩阵自证**：`P1` ~ `P9` 恰 9 行（≥ 8），逐行覆盖 `T-…-070` Scope 列出的八个崩溃点 + `P9` 的 `4` 号出口；
  「恢复后磁盘态」三值封闭；矩阵逐字含 `txn_id`、`.index/txn/`、`flock`（见 §3 / §4 与本节正文）。
- **`W26` 的分布是矩阵的硬结论**：只有 `P2` / `P3` / `P4` 三行发 `W26`（它们是唯一「`commit` 标记缺席 ⇒ 真实回滚」的三行）；
  `P1` / `P5` / `P6` / `P7` / `P8` / `P9` **一律不发**。`072` 的 `TestRecoverEmitsW26` 与 `TestCrashRecoveryMatrix` 须按此逐行断言。
- **矩阵的成立前提（横切三分支，不新增矩阵行）**：上表 `P2` / `P3` / `P4` 的「回滚到前像 + 退 `0` + `W26`」
  仅在「**崩溃后无人改动过本事务涉及的文件**」（逐文件落 `B-R1` / `B-R2`）时成立。
  若任一文件落 **`B-R3`**（`current_hash` 既非 `pre_hash` 也非 `target_hash`，即 **post-crash 外部编辑**），
  则该行**整体改判为**：恢复后磁盘态 = **`磁盘保留当前状态`**（仍在三值封闭之内）、退出码 = **`5`**、承载码 = **`E15`**、
  **不发 `W26`**、事务**保持未闭合**、**零写入**。此改判是**横切规则**，不新增矩阵行、不新增诊断码、不新增退出码。
  同一横切规则也适用于**任一路径违规**（§4.3）、**任一前像不可用**（§4.1.1：`pre_bytes_ref` 缺失 / 非普通文件 / 哈希不符）、
  以及**扫描全集异常**（§4.1.2：多于一个 `OpenTxn`，或存在任何 `CorruptTxn`）：一律改判为
  「磁盘保留当前状态 + 退 `5` + `E15` + 不发 `W26` + 零写入」，且**必须在全局只读分类完成后、任何回滚写之前**作出该改判。
  `P1` 的 pre-intent residue 情形另见 §4.2（静默清理，`P1` 行的「无码」不变）；
  而 **`intent.json` 在盘但不可解析 / `journal_version` 不支持**是一条**独立于本矩阵九行的形态**（它不是某个崩溃点，
  而是日志被外力损坏的结果）：按 §4.2 第二行 **fail closed**——原样保留、权威文件零写入、退 `5` + `E15`、不发 `W26`。
- **P3 是本矩阵最强的一条**：它是「多文件原子」的唯一真正难点——恢复层必须把已 `rename` 的 k 个文件用前像字节还原，
  且对剩余 N−k 个文件**不做任何写入**。它也正是 §5.1 中 `L3`「恢复屏障语义」的兑现点：
  半应用态**可能持续到下一次写命令**（其间可跨进程退出与重启留盘，并被只读命令 / 外部观察者读到）；
  **恢复屏障（`S2`）完成后回到前像**；**新写事务不会建立在半应用态之上**。
  「恢复后磁盘态 = 回滚到前像」在本矩阵中一律读作「**下一次写命令的恢复屏障完成之后**的磁盘态」，
  **不**读作「崩溃瞬间磁盘即已一致」。
- **`SIGKILL` 真杀**：八个崩溃点由 `072` 用真实 `kill -9` 注入（`grep -c 'kill -9' test/e2e/m6_crash_recovery.sh` ≥ 1），**不接受 mock**。

---

## 7. 块级安全合并（范围⑥，A-57）

- **触发条件**：仅当 `base_block_hash` **不匹配**时进入安全判定。**匹配路径的行为与 M3 逐字相同**（M6 不改匹配路径一个字节）。
- **安全四判据（全真才合并）**：
  1. 目标分区可按 `mdfile` 的 `Span` 定位，且分区名与 base 时相同；
  2. 「前像 → 当前值」的差异块集合与**本次要写的块字节区间不相交**（区间相交即不安全）；
  3. 目标块**不属于**「用户补充」（任何时候永不写「用户补充」）；
  4. 当期文件 frontmatter 仍是合法 YAML（E4 不成立）。
- **判定时点**：安全判定**只发生在 `S3` preflight**（与 B3 前像比对同一阶段），判定结果参与**定盘** accepted write-set 与 skipped 集合，
  之后在本次事务内不再变化——因此「跳过某个块 / 某个文件」与「accepted write-set 全成或全不成」**不冲突**（§5.2 给出唯一答案）。
- **不安全的处置**：该文件的该块跳过，留痕 **`W27 block_merge_conflict`** 并进报告 `skipped[]`（`kind` 恒 2 值，**不扩张**）；
  跳过项**不进** accepted write-set，因此**不影响** accepted write-set 内其他文件的事务性判定，也**不触发**整事务 abort；
  若跳过后 accepted write-set 变为空集，则本次零写入、不开 intent、退 `3`（§5.2）。
- **粒度**：合并只在**块粒度**发生——块 = 段落 / 顶层列表项（含缩进子项）/ 围栏代码块 / 连续表格行 / H3 小标题（§4.2）。
  **不做行级 diff / patch，不引入任何 diff 依赖**（`go.mod` 直接依赖数 M6 收口 ≤ 2）。
- **用户字节零丢失**：合并路径必须满足「既有『用户补充』与『存疑与待验证』逐字保留」（§9 的 B2），并由 `TestBlockMergeNeverLosesUserBytes` 反证。

---

## 8. 并发冲突处理（范围⑦）

- **两个 `eg` 进程同时写同一 vault 的行为封闭为两种，且只有这两种**：
  ① 后到者在 10s 内拿到锁 → 正常执行，等待事实留痕 `W28`；
  ② 后到者超时 → 退 `5` + `E16`，**零写入**（`git status --porcelain` 与执行前逐字相等）。
- **零半写（L1）**：并发下任一文件都不出现半写 / 撕裂字节（由 §5 的同目录 `tmp` + `fsync` + 原子 `rename` 保证）。
- **写者之间零中间态（L2）**：两个 `eg` 写事务串行，任一写者**不可能**观察到另一个写事务的跨文件中间态——它拿不到锁就进不了临界区。
- **零交叉 `txn_id`**：同一文件在任一时刻只可能被一个 `txn_id` 写入——锁保证临界区串行，`txn_id` 在持锁后按序号分配（§4），
  日志的 `files[]` 因此不会交叉。
- **只读命令属非协作观察者（L4）**：只读命令**不拿锁**，因此只享有 `L1`——逐文件永不撕裂；但在 `S5` 的 `rename` 序列窗口内，
  它**可能**读到「部分文件已是目标态、其余仍是前像」的跨文件中间态。需要跨文件一致读的场景**不在 M6 范围**，
  本合同如实登记该限制而**不做**任何虚假承诺（详见 §5.1 四级可见性模型）。
- **不做多机 / 网络文件系统上的分布式锁**：`run.lock` 只保证**单机多进程**互斥。NFS / SMB / 同步盘（Dropbox / iCloud / OneDrive）上 `flock`
  语义不可靠，作为**已知限制**如实登记，**不承诺、不做探测**（探测不可靠反而给出假保证）。

---

## 9. `eg check` 强校验 + 写前强校验 + 退出码 `5`（范围⑧，A-56 / A-58）

- **开关形态（A-56 = ②）**：默认宽松，`--strict` 显式开启。默认路径**零漂移**，M1 ~ M5 的 50 个既有 e2e 一条不改即继续全绿。
- **升级面恰五条**：`W1` / `W2` / `W3` / `W4` / `W6` 在 `--strict` 下**升 severity 为 error，码不变**；其余 `W*` / `I*` / `Q*` 不变。
  `check` 的**十二值枚举一字不动**；新发现走**新诊断码**而不进 `check` 枚举；`rel data` 五键、`skipped[].kind` 两值、报告 `reconcile` 三键均不动。
- **写前强校验（precheck）**：`internal/plan/precheck.go` 成为**全部写命令的前置**，插入点在 `S3`（取锁与恢复屏障之后、intent 之前）。
  **前置动作是「重新候选发现 + 重读 + 重算 + 最终化」**（§2 的 `S3` ⓪~③）：precheck **必须**基于**锁内重新发现的候选集**与
  **锁内重读**的字节工作，**不得**接受来自 `S0` 的候选集合、hash 或计划缓存——否则「hash 相等」只能证明**已知**文件逐个未变，
  既排除不了跨文件混合快照，也发现不了 `S0` 之后被另一写者新增 / 删除的匹配文件。
  校验内容 = ChangePlan 的 error 级判据（E1 ~ E6）+ **锁内重算的**逐文件 `content_hash` 前像比对（B3）+ 写权限矩阵硬约束 + `--strict` 时的升级面。
- **`E15` 的产出面（全部归入 A-58 第一类「写前强校验失败」，一律零写入）**：`E15` 表达的是**写前安全复核这一语义大类**，
  **当前枚举 8 个原因**（清单可增，**精确形态数不作为门禁判据**——口径见 §18.4；R1 ~ R7 的「恰四形态 / 恰六形态 / 恰七形态」这类精确数字**一律作废**），
  分两大来源，**共用同一个码与同一个退出码**：
  1. **`S3` 写前强校验失败**（含 `--strict` 升级面、E1 ~ E6、B3 前像不匹配的 error 面）；
  2. **`S2` 恢复屏障无法安全完成**，恰五形态（均**零写入**、均**不发 `W26`**、均保持事务未闭合）：
     ① **`B-R3` post-crash 外部编辑冲突**（Pass A 判出任一冲突项 ⇒ 整事务不进 Pass B，§4.1）；
     ② **`intent.json` 在盘但不可解析 / `journal_version` 不支持**（损坏事务，fail closed，§4.2）；
     ③ **`intent` 内路径违规**（绝对路径 / `..` / 未规范化 / symlink 逃逸，§4.3）；
     ④ **前像不可用**（`pre_bytes_ref` 缺失 / 非普通文件 / `size` 或内容哈希与 `pre_hash` 不符，§4.1.1）；
     ⑤ **扫描全集异常**（多于一个 `OpenTxn`，或存在任何 `CorruptTxn`）——在**全局只读分类**完成后整体阻断，§4.1.2。
  另有两个原因同样复用 `E15` + `5`：**原因 7** = `txn_id` 分配时 `seq` 超 `uint64` 上限的 fail closed（§4，零写入、无事务目录创建）；
  **原因 8** = `.index/` runtime reserved entry **类型违规**（`run.lock` 为目录 / `txn` 为普通文件 / 任一项为 symlink，§16.3），
  且**原因 8 有路径限定**：**只在 A 类权威写命令与 B 类 index 维护命令上**映射为 `E15` + `5` + 零写入，
  **C 类只读命令（`index status` / `bench` / `search` / `card` / `rel` / `check` / `report`）绝不因它退 `5`**——
  只读路径沿用 M5 既有语义（`W24 index_corrupt`，读查询再加 `Q5` 降级留痕、退出码不变），分层定稿见 §18.1。
  上述原因共用 `E15` 与退出码 `5`，因此 **A-58 的「恰两类情形」边界一字不改**（`5` = 写前强校验失败 **或** 锁不可用），
  **不新增诊断码**（M6 新增仍恰 5 条）、**不新增退出码**（全集仍恰 `{0,1,2,3,4,5,6}`）。
  报告须逐条列出冲突 / 违规路径：`B-R3` 给 `pre_hash` / `target_hash` / `current_hash` 三值，
  损坏事务给 `txn_id` 与不可解析原因，路径违规给字段名与违规类别，前像不可用给期望 / 实测哈希与失败原因，
  扫描全集异常给全部问题 `txn_id` 列表，便于人工处置。
  **「恢复屏障失败即阻断新写」是 fail closed 的统一表达**：宁可让后续写命令持续退 `5`，也不在证据不足时动权威字节。
- **退出码 `5`**：**退出码 5 只在写前强校验失败与锁不可用两类情形产出，且零写入**。
  `E15 precheck_failed`（校验失败）与 `E16 lock_timeout`（锁不可用）区分原因；
  **落地形态按 R7 定稿（§17）**：`internal/cli/exitcode.go` 定义常量 `ExitPrecheckOrLock = 5` 与两类类型化错误的启用边界，
  `internal/cli/exit.go` 的 `ExitCodeFor` **单点**把这两类错误翻译为 `5`，进程退出仍**只**发生在 `cmd/eg/main.go`（全仓唯一 `os.Exit` 调用点）；
  **`internal/**` 一律不得出现 `os.Exit`**——「`os.Exit(5)` 由 `exitcode.go` 承载」是 R1 ~ R6 的**错误表述**，已由 R7 撤销；
  只读命令**永不**退 `5`（**含** `.index/` reserved entry 类型违规场景，§18.1 C 类分层）。

### 9.1 「退出码 5 零写入」的精确边界（R5 P0：别把它读成「整条命令绝对零磁盘变化」）

**「零写入」的被保护对象是「本次请求事务」，不是「整条进程生命周期内的磁盘」**。逐字口径：

| 断言 | 成立范围 |
|-|-|
| **本次请求事务零权威写入** | **无条件成立**：退 `5` 时，本次命令**自己要写的** accepted write-set 一个字节都没落盘，也没有为它创建过 `commit`；`E16`（锁不可用）连事务都没开始 |
| **整条命令执行前后磁盘逐字不变** | **仅在「命令启动时不存在待恢复事务」的前置下成立** |

- **例外只有一种、且已在册**：`S2` **成功**完成了**前一个**未闭合事务的恢复（产出 `W26`），随后 `S3` 的写前强校验失败退 `5`。
  此时磁盘相对**命令启动时**确实**已改变**（前一事务的文件被还原为前像、其 `abort` 已落盘），
  但这些变化**属于前一事务的恢复结果**、**已由 `W26` 如实记录**，**不是**本次请求事务的写入。
  该组合合法且必要：恢复屏障先于 precheck 是安全序要求（§2 `S2` → `S3`），不能因为「可能随后退 5」而跳过恢复。
- **因此对外文档与验收脚本一律不得声称**「任何 `exit 5` 场景下整个命令绝对零磁盘变化」；
  正确表述恒为「**本次请求事务零权威写入**」（`075` 对外文档按此口径，见 §14）。
- **验收断言的前置条件（`074` 的 `m6_precheck_exit5.sh`）**：
  「执行前后 `git status --porcelain` 逐字相等」这条强断言**必须在「无待恢复事务」的前置下执行**——
  即用例在触发 `exit 5` 前须保证 `.index/txn/` 下**不存在**未闭合事务（或断言前先跑一次干净的恢复使其归零）。
  对「先 `W26` 恢复、后退 `5`」的组合用例，断言改为：① 退出码恒 `5` + `E15`；② `warnings[]` 含 `W26`；
  ③ **本次 write-set 的每个目标文件哈希与命令启动时逐字相等**（而**不是**整仓 `git status` 不变）。
- **`E16`（锁不可用）**：不进入 `S2`，故整条命令磁盘零变化**无条件**成立（`run.lock` 正文不属权威文件，且未取到锁时也不写）。
- **与既有退出码的分界**：`0` 成功；`1` 用法非法；`2` ChangePlan 校验失败（error 级，零写入）；`3` 部分写入被跳过；
  `4` Git 提交失败（**磁盘保留当前状态，不做破坏性还原**）；`5` 写前强校验失败或锁不可用（零写入）；`6` 需确认（白名单恰 `{proposal approve, delete}` 不变）。
  M6 收口后退出码全集恰 `{0,1,2,3,4,5,6}`（**7 值**，新增恰 `5`）。
- **写权限矩阵升为硬约束**：`store.SetStatus` / `SetReplacedBy` / `SetDeleted` 的调用点**恰 5 处**（M4 事实）且只在 `cli` 的五个命令文件内；
  由运行期断言升为编译期 + CI 双侧硬约束。

---

## 10. CI 依赖方向门禁与 M6 工程收口（范围⑨，A-60）

- **§13 三条依赖禁令做成 CI 硬门禁**（`test/ci/dep_direction_gate.sh`，进 `make lint`）：
  ① `index` 不得依赖 `store`；② `rules` / `query/rank` / `query/review` 不得导入 `query/filter`；
  ③ 除 `cli` 的五个命令文件外，任何包不得引用 `store.SetStatus` / `SetReplacedBy` / `SetDeleted`。
- **`internal/txn` 包边界**：`doc.go` 首行逐字 `// [S5]`；`txn` **不得**导入 `cli` / `query` / `index` / `report` / `reconcile`；
  零 `os/exec`；落盘面只有 `run.lock` 与 `.index/txn`。
- **计数面（M6 收口时逐项复算）**：命令恒 **22**；`model.KnownVerbs` 恒 **8**；`plan.AllOpNames` 恒 **17**；`check` 恒 **12** 值；
  `rel data` 恒 **5** 键；`skipped[].kind` 恒 **2** 值；诊断码新增**恰 5** 条；`W21` **继续不分配**；退出码全集 **7** 值；
  e2e 只增不减（M6 新增恰 11 个 `m6_*.sh`，收口时磁盘 ≥ **61**）。
- **版本与产物（A-60 = ①）**：`0.5.0-m5` → **`0.6.0-m6`**，五处同真；dist 在 M6 HEAD 四平台重建并重算 `SHA256SUMS`；
  `CGO_ENABLED=0` 静态构建不破坏；`--version` 的 `commit` == `git rev-parse --short HEAD`。
- **三项已知限制逐字对外**：macOS 产物仅交叉编译**未真机验证**；网络盘 / 同步盘**不保证原子性**（且不做探测）；
  存在未闭合事务时**不可删** `.index/`。
- **落地归属**：本节的实现与复算属 `074`（CI 门禁）与 `075`（版本、产物、文档、验收），**不在 `070`**。

---

## 11. M5 事实基线（七项，实测为准）与差异登记

判定时点：`T-…-070` 开工日；来源 = `docs/specs/2027-01-17-m5-acceptance-report.md` + `evergreen` 仓当期实测。

| # | 基线项 | M5 收口值（实测复核） | 复核命令 | 与 `M-006` 表格是否一致 |
|-|-|-|-|-|
| 1 | 命令数 | **22** | `grep -c 'wantCommandCount = 22' internal/cli/cli_test.go` == 1 | 一致 |
| 2 | `model.KnownVerbs` | **8**（`init` / `reconcile` / `capture` / `process` / `reprocess` / `relate` / `proposal` / `delete`） | `internal/model/enums.go` 的 `KnownVerbs()` 返回 8 项 | 一致 |
| 3 | `plan.AllOpNames` | **17**（M3 期 16 + M4 新增 1） | `internal/plan/m3_test.go` 的 `m3AllOps, m4NewOps = 16, 1` | 一致 |
| 4 | 退出码全集 | **`{0,1,2,3,4,6}`**，`5` 未启用（`ExitCode5Enabled()` 恒 `false`；`exitcode.go` 逐字注明「刻意不定义常量」） | `internal/cli/exitcode.go` | 一致（**M6 去向见 §17**：`074` 新增 `ExitPrecheckOrLock = 5` 并把 `ExitCode5Enabled()` 改恒 `true`，同时按 §17.4 重钉该函数的 6 处消费点；常量表全集断言由 `075` 按 §17.3 重钉为 `{0,1,2,3,4,5,6}`） |
| 5 | 诊断码现状 | `E1..E14` / `W1..W20` / `W22..W25` / `{I1, I2}` / `Q1..Q5`；**`W21` 不分配** | `grep -rn '"W21"' internal/ \| wc -l` == 0 | 一致 |
| 6 | e2e 磁盘总数 | **50**（M1 ~ M4 的 39 + M5 的 11） | `ls test/e2e/*.sh \| wc -l` == 50；`ls test/e2e/m5_*.sh \| wc -l` == 11 | 一致 |
| 7 | 版本 | **`0.5.0-m5`** | `internal/version/version.go` 的 `DefaultVersion` | 一致 |

**差异行**：本次逐项复核**未发现**基线与 `M-006-m6.md` 表格的不一致，故差异集合为**空集**。
M5 遗留到 M6 的两条延期债（`J15` = `m3_final_gate.py` 恰 `135 checks / 3 failed`，失败集合逐字 `{H10k, H10x, H10ad}`；
`I2` 合同漂移）**在本合同只登记、不改判**，终态由 `T-…-075` 给出——`M-006` 判据 18 是最后一道，「继续延期」不再是合法取值。

---

## 12. 诊断码分配表（M6 新增恰 5 条）

| 码 | 名称 | severity | 产出位置 | 语义 |
|-|-|-|-|-|
| `E15` | `precheck_failed` | error | `internal/plan/precheck.go`（+ `internal/txn/recover.go` / `internal/txn/id.go` 上报的冲突 / 损坏 / 路径违规 / 前像不可用 / 扫描异常 / `seq` 溢出经由 precheck 面产出） | 写前安全复核失败 → 退 `5`，**本次请求事务零权威写入**（§9.1 界定边界）。**口径按 R8 §18.4 定稿**：`E15` 是**写前安全复核这一语义大类**，**当前枚举 8 个原因**（清单可增，**精确形态数不作为判据**；R1 ~ R7 的「恰七形态」等精确数字已作废）——① `S3` 强校验失败；② `B-R3` post-crash 外部编辑冲突；③ 损坏 intent（不可解析 / `journal_version` 不支持）；④ intent 路径违规；⑤ 前像不可用（`pre_bytes_ref` 缺失 / 非普通文件 / 哈希不符）；⑥ 扫描全集异常（多 `OpenTxn` 或存在 `CorruptTxn`）；⑦ `txn_id` 的 `seq` 超 `uint64` 上限；⑧ `.index/` runtime reserved entry 类型违规（`run.lock` 为目录 / `txn` 为普通文件 / 任一项为 symlink，§16.3）——**原因 ⑧ 只在 A / B 类写路径产出**，**C 类只读路径不产 `E15`、不退 `5`**，改由 M5 既有 `W24`（+ 读查询 `Q5`）表达（§18.1）（§4 / §4.1 / §4.1.1 / §4.1.2 / §4.2 / §4.3 / §9 / §16.3 / §18） |
| `E16` | `lock_timeout` | error | `internal/txn/lock.go`（A 类经 `plan` / `cli`，B 类经 `internal/cli/index*.go`） | 锁不可用（等待超时）→ 退 `5`，零写入；**R6（§16.1 / §16.2 R6-D5）新增触发面：B 类 index 维护命令（`index build` / `rebuild` / `sync`）遇 `flock` 忙时立即退 `5` + `E16` 且 `.index/` 零字节写入**（同码同退出码，**不新增码**） |
| `W26` | `txn_recovered` | warning | `internal/txn/recover.go` | 启动时检出未闭合事务并**完成** Pass B 回滚（Pass A 判出冲突 / 损坏 / 路径违规时**不产出**） |
| `W27` | `block_merge_conflict` | warning | `internal/mdfile/block_merge.go` / `internal/store/merge.go` | 块级安全判定不通过，跳过该块 |
| `W28` | `lock_wait_retry` | warning | `internal/txn/lock.go` | 锁等待期的退避重试留痕 |

- 上表**恰 5 条**，是 M6 允许新增的**全部**诊断码。
- `W1` / `W2` / `W3` / `W4` / `W6` 在 `--strict` 下**只升 severity、码不变**，**不占新码**。
- **`W21` 继续不分配**；`W29` 及以后在 M6 内**不分配**。
- 新码**不进** `check` 的十二值枚举（枚举一字不动）。

---

## 13. 边界与已知限制（本合同的「不做」清单）

1. **不做多机 / 网络文件系统上的分布式锁**；NFS / SMB / 同步盘上的 `flock` 不可靠，如实登记为已知限制，**不承诺、不做探测**。
2. **不做行级 diff / patch 合并**，不引入任何 diff 库；`go.mod` 直接依赖数 M6 收口 ≤ **2**。
3. **不做破坏性还原、不物理删除**：崩溃恢复只回滚到**本次事务的前像**，且**必须**先跑完 §4.1 的
   **Pass A 全量只读判定**与 §4.1.2 的**全局只读分类**（`B-R3` post-crash 外部编辑冲突、损坏 intent、intent 路径违规、
   前像不可用、多 `OpenTxn` / 并存 `CorruptTxn` 五类一律**整体零写入**并退 `5` + `E15`，
   绝不覆盖用户的崩溃后编辑，也绝不在证据不足时动权威字节）；**不做**「逐文件判定 + 立即回滚」的边走边写形态，
   **也不做**「先恢复一个事务、后发现异常再返回 `5`」的先写后判形态；
   新建文件的回滚**不物理丢字节**——`current == target` 时原子移入 `.index/txn/<txn_id>/quarantine/` 而**不是**删除；
   恢复的写入面被封闭为「校验通过的 `files[].path` + 本事务目录之内」两处（§4.3）；
   退出码 `4` 的「磁盘保留当前状态」语义一字不改；**任何阶段任何授权都不做破坏性还原**。
4. **不新增任何命令**（恒 22）；恢复是写命令的启动 hook，不是新命令。
5. **不把索引变成权威**：Markdown 唯一权威、`.index/` 可重建在 M6 继续成立；`.index/txn/` 是**事务日志而非派生索引**（R-31）。
   由此**不做**「跨 `.index/txn` 生命周期永久唯一的 `txn_id`」：`.index/` 在无未闭合事务时可被合法删除并重置 `seq`，
   `txn_id` 的唯一性 / 单调性只在**当前生命周期内**成立（§4），**不得**被当作跨生命周期主键；同进程唯一且单调无条件成立。
6. **不扩张既有输出契约**：`check` 十二值、`rel data` 五键、`skipped[].kind` 两值、报告 `reconcile` 三键一字不动；`6` 白名单不变。
7. **不改默认校验强度**：强校验只在 `--strict` 下生效（A-56 = ②）。
8. **不改 M1 ~ M5 的历史 Task / 历史 Activity Log / 历史验收结论 / 历史门禁判据**；M6 用独立 manifest `M6_TASK_IDS`（`070`–`075`）与独立门禁脚本。
9. **不删除、不跳过任何 M1 ~ M5 既有用例**；e2e 只增不减。
10. **不引入 cgo、不引入网络请求**；`CGO_ENABLED=0` 四平台静态构建在 M6 收口后仍成立。
11. **不声称「任何 `exit 5` 场景整个命令绝对零磁盘变化」**：`5` 的零写入保证面是「**本次请求事务零权威写入**」；
    `S2` 成功恢复前一事务（`W26`）后 `S3` 再退 `5` 时，磁盘相对命令启动时可能已变，那是**已记录的恢复结果**（§9.1）。
12. **本 task 零产品代码**：`T-…-070` 只产出本文件，`internal/**` 与 `test/**` 一个字节都不改。

---

## 14. 下游 DoR 逐项消费对照（`071` ~ `074` 各自的输入在本合同有唯一答案）

| 下游 task | DoR 要求的输入 | 本合同的唯一答案（章节） |
|-|-|-|
| `071` | `run.lock` 位置与形态（A-53） | `vault/.index/run.lock` + `flock`（**互斥唯一真源**）+ 三字段纯诊断持有者正文（`pid` / `acquired_at` / `argv`，初次写入**不含** `txn_id`，可用同一 fd 原地补写）；**被加锁 inode 持锁期零替换**：正文写法**恰一种**——同一把已加锁 fd 的 `Ftruncate` + `Pwrite` + `Fsync`（允许非原子、读侧容错），**严禁** `tmp` + `rename` / `unlink`，**亦不得**引入 `run.lock.meta` 等旁路文件（R5 撤销该备选）；`flock` 忙时绝不凭 `pid` / `host` 强抢（§1 A-53 行、§3） |
| `071` | 锁超时值与等待策略（A-54） | 默认 10s、六段退避序列、`W28` 留痕、超时退 `5` + `E16`（§1 A-54 行、§3） |
| `071` | 事务边界决定的临界区起止（A-52） | 临界区 = `S1` ~ `S8`，覆盖「**重新候选发现 + 重读** + 校验 + 落盘 + 提交」；锁外（`S0`）只做参数解析与**提示性**候选发现；Markdown 提交点 = `commit` 标记，Git 提交是 `S7` 的后提交步骤（§2） |
| `071` | `txn_id` 格式与分配算法（A-59） | `^t[0-9a-f]{16}$` = `"t"` + **16 位**零填充小写 hex（**无** Unix 秒段）；持锁后按 `max(.index/txn/seq, 现存目录最大序号, **process-local lastSeq**) + 1` 分配、`O_EXCL` 建目录、原子更新 `seq` 与进程游标；`seq` 超 `uint64` 上限 ⇒ fail closed 退 `5` + `E15`；保证域 = **同进程唯一且单调（由进程游标项无条件保证）** + **同 vault 且当前 `.index/txn` 生命周期内跨进程唯一且字典序单调不减**；**不保证**跨生命周期永久唯一（`.index/` 合法删除会重置 `seq`，已在册）（§1 A-59 行、§4） |
| `071` | 事务目录布局与 `intent.json` 字段、发布屏障、保留策略 | `.index/txn/<txn_id>/{intent.json,commit,abort}` + `pre/` + **`quarantine/`** + `.index/txn/seq`；`intent.json` 七字段，`files[]` 每项**必须同时含 `pre_hash` 与 `target_hash`（及 `create` 标记）**以支撑 §4.1 三分支，且路径字段须满足 §4.3 规范化约束；**`S4` 的 intent 发布屏障逐字定死**：全部 `pre/<n>` 写入并逐个 `fsync` → `fsync(pre/)` + `fsync(txn 目录)` → `intent.json.tmp` → `fsync(tmp)` → `rename` → `fsync(txn 目录)`，**只有屏障完成后才允许首个权威 `rename`**；`Scan` 只把「`intent.json` 存在且可解析」当事务，**pre-intent residue（含遗留 `.tmp`）按 §4.2 静默移除 / 就地闭合且不发 `W26`**，**损坏 intent 则 fail closed（退 `5` + `E15`、原样保留、不参与自动清理）**；保留 7 天 / 20 个且不重置 `seq`（§4 / §4.1 / §4.2 / §4.3） |
| `071` | 崩溃点矩阵前四行（日志格式须足以支撑 `072` 的 8 点恢复） | `P1` ~ `P4` 逐行给出「回滚到前像」及其前像来源（§6、§4 的 `pre_bytes_ref`） |
| `072` | 事务边界（A-52） | 一条写命令 = 一个事务；**Markdown 提交点 = `commit` 标记**，Git 提交是持锁下的后提交步骤，失败退 `4` 且不回滚 Markdown（§1 A-52 行、§2） |
| `072` | 崩溃恢复触发时机（A-55）与 `W26` 分布 | 写命令启动 hook（`internal/cli/recover_hook.go`），只读命令不触发；`W26` **只在** `P2` / `P3` / `P4` 三行、且 **Pass A 全量落 `B-R1` / `B-R2` 后 Pass B 完成回滚**时产出（`commit` 标记存在即不回滚、不发 `W26`；陈旧锁接管、pre-intent residue、`B-R3` 冲突、损坏 intent、路径违规都不是 `txn_recovered`）（§1 A-55 行、§4.1 ~ §4.3、§6） |
| `072` | `txn_id` 报告填充口径（A-59） | 报告**必填**，与日志逐字相等（§1 A-59 行、§4） |
| `072` | 崩溃点矩阵全 8 行 | `P1` ~ `P8`（+ `P9` 的 `4` 号出口），磁盘态三值封闭，逐行给出是否发 `W26`（§6） |
| `072` | Pass A 的前像可用性校验与全局只读分类（R5） | §4.1.1 / §4.1.2：`create: false` 的每项 `pre_bytes_ref` **必须存在、是普通文件、`size == pre_size` 且内容哈希 `== pre_hash`**（`create: true` 则必须无 `pre_bytes_ref`），任一不满足 ⇒ **绝不进入 Pass B**、整体零写入、退 `5` + `E15`；`Scan` 得到的**全部**条目（每个 `OpenTxn` / `CorruptTxn` / `PreIntentResidue`）必须**先完成全局只读分类**，只有「恰一个 `OpenTxn` 且其 Pass A 全量可恢复 且无任何 `CorruptTxn`」才允许写盘，否则在**任何回滚写之前**整体阻断；反证 `TestRecoverRejectsCorruptPreimage` / `TestMultipleOpenTxnFailsClosedBeforeWrites` |
| `072` | 恢复的两遍模型、逐文件判定与新建文件回滚 | **两遍**：Pass A 只读全量校验路径 + 重算 `current_hash` + 分类，Pass B 才写盘（**严禁边判边滚**）。三分支：`current == pre_hash` ⇒ 零写入；`current == target_hash` ⇒ Pass B 安全还原前像；两者皆非 ⇒ **`B-R3`**：**整事务零写入**、保持未闭合、阻断新写、退 `5` + `E15`、不发 `W26`（与冲突项在 `files[]` 中的位置无关）。新建文件（`create: true`）：`current == target_hash` ⇒ Pass B **原子移入 `quarantine/`**（字节完整保留）后才闭合 `abort`；已不存在 ⇒ 视为 `B-R1`；被编辑 ⇒ `B-R3` 阻断（§4.1） |
| `072` | residue / 损坏 intent / 路径越界三类非常规日志形态 | §4.2 / §4.3：**无 `intent.json`** ⇒ pre-intent residue，由 intent 发布屏障保证零权威写入，可静默移除 / 就地闭合、不发 `W26`、不阻断；**`intent.json` 在盘但不可解析或 `journal_version` 不支持** ⇒ **fail closed**：原样保留、权威零写入、退 `5` + `E15`、不参与自动清理；**路径字段违规**（绝对路径 / `..` / 未规范化 / symlink 逃逸）⇒ Pass A 即判失败，整事务零写入、退 `5` + `E15`；恢复写入面恒封闭在「校验通过的 `files[].path`」与「本事务 txn 目录内」两处 |
| `072` | 原子提交机制、保证面与 B3 关系 | `tmp` + `fsync` + 原子 `rename` + 日志兜底；保证面 = 四级可见性模型（`L1` 单文件原子 / `L2` 协作写者互斥 / `L3` 崩溃后一致 / `L4` 非协作观察者只享 L1，**不承诺**跨文件瞬时同时可见）；原子域 = accepted write-set，B3 与 `W27` 的跳过在 `S3` 定盘且退 `3` 语义不变（§5.1 / §5.2） |
| `073` | 块级三方合并「安全」判定标准（A-57） | 四判据全真才合并，否则跳过 + `W27`；只在块粒度；判定发生在 `S3` preflight，跳过项不进 accepted write-set 且不触发整事务 abort（§1 A-57 行、§7、§5.2） |
| `073` | 锁超时与等待策略（A-54）、退出码 `5` 语义（A-58） | 等待留痕 `W28` / 超时退 `5` + `E16`；`5` 的两类情形与零写入（§3、§8、§9） |
| `073` | `W28` / `E16` 产出位置不重复实现 | 两码**只在** `internal/txn/lock.go` 产出（§12） |
| `074` | 强校验开关形态（A-56） | 默认宽松 + `--strict`（owner 口径已定，非预判）；升级面恰五条、码不变（§1 A-56 行、§9） |
| `074` | 退出码 `5` 语义边界（A-58） | 写前强校验失败或锁不可用两类；**退出码 5 的落地形态 = 常量 `ExitPrecheckOrLock` + `ExitCodeFor` 单点映射 + `cmd/eg/main.go` 唯一 `os.Exit`（§17，R7 定稿；`internal/**` 零 `os.Exit`）**；**第一类 = 写前安全复核语义大类，当前枚举 8 个原因（R8 §18.4；精确形态数不作判据）**：`S3` 强校验失败、`B-R3` post-crash 外部编辑冲突、损坏 intent（不可解析 / `journal_version` 不支持）、intent 路径违规、前像不可用、扫描全集异常（多 `OpenTxn` / 存在 `CorruptTxn`）、`seq` 溢出、**`.index/` runtime reserved entry 类型违规（R6，§16.3；R8 §18.1 限定为只在 A / B 类写路径映射 `5`，C 类只读路径走 `W24` / `Q5` 且永不退 `5`）**——共用 `E15`，**不新增码、不新增退出码**；并承接 B 类 index 维护命令锁忙的 `E16` + 退 `5` 出口（§1 A-58 行、§4 ~ §4.3、§9、§16.1 / §16.3、§18） |
| `074` | 「零写入」的精确断言口径与验收前置（R5） | §9.1：保证面 = **本次请求事务零权威写入**；「执行前后 `git status --porcelain` 逐字相等」只在**无待恢复事务**的前置下成立，`m6_precheck_exit5.sh` 的该断言**必须**带此前置；「`S2` 成功恢复（`W26`）后 `S3` 退 `5`」的组合改断言退出码 `5` + `E15` + `warnings[]` 含 `W26` + **本次 write-set 目标文件哈希与命令启动时逐字相等**；**不得**声称任何 `exit 5` 场景整个命令绝对零磁盘变化 |
| `074` | 写前校验插入点相对临界区的位置 | `S3`：取锁与恢复屏障之后、intent 之前；且 `S3` **必须先在锁内重新执行候选发现（`S0` 只作提示、不得限定输入），再重读全部权威输入并重算 / 最终化 ChangePlan、`content_hash`、accepted write-set**，precheck 不得复用 `S0` 的候选集合与读结果（§2、§9） |
| `074` | 三条依赖禁令与写权限矩阵硬约束口径 | §13 三禁令做成 `dep_direction_gate.sh` 进 `make lint`；`Set*` 调用点恰 5 处（§10） |
| `075` | 收口版本号与三项已知限制（A-60） | `0.6.0-m6`，三项限制逐字对外（§1 A-60 行、§10） |
| `075` | M5 延期债（`J15` / `I2`）的处置口径 | 本合同只登记不改判，终态由 `075` 给出（§11） |

**结论**：`071` ~ `074` 的 DoR 阻断条件（「任一裁决为『未知 / 待 owner 确认』则置 `blocked`」）**全部不成立**，
`071` 可在本 task `done` 后立即开工。

---

## 15. 五句逐字锚点索引（供门禁 `grep -qF`）

| # | 逐字句 | 首次出现 |
|-|-|-|
| 1 | 要么全部生效、要么全部不生效 | 前言 / §5 |
| 2 | 任何阶段任何授权都不做破坏性还原 | §2 / §6 / §13 |
| 3 | 退出码 5 只在写前强校验失败与锁不可用两类情形产出，且零写入 | §1 A-58 行 / §9 |
| 4 | intent 日志写入先于任何权威文件写入 | §4 |
| 5 | 权威 Markdown 一个字节都不错写 | 前言 |

**合同自证清单**：`^## ` 小节数 **18**（≥ 9，R6 追加 §16、R7 追加 §17）；`A-52` ~ `A-60` 裁决行**恰 9**；五句锚点逐字命中；
崩溃矩阵 `P1` ~ `P9` **9 行**（≥ 8）且磁盘态三值封闭；新增诊断码**恰 5**；全文零占位符（无待填标记、无未定字样、无未决裁决）；
`internal/**` 零改动。

**语义自洽清单（reviewer 五轮阻断 + R6 / R7 / R8 / R9 / R10 五轮 post-close correction 的收口位置，共 27 条）**：
1. **可见性保证面**：§5.1 四级模型（`L1` 单文件原子 / `L2` 协作 `eg` 写者互斥 / `L3` 崩溃后一致性 / `L4` 非协作观察者只享 L1）；
   本合同**不**声称「非协作观察者永远看不到跨文件半应用」，并说明为何不引入目录 / 指针切换架构；
   `M-006` 判据 6 与范围第 4 项、`T-…-072` 与 `T-…-075` 的对应规划正文已按 owner 授权同步纠正为可实现的保证。
2. **提交点归属**：§2 与 §1 A-52 行——Markdown 事务提交点 = `commit` 标记；Git 提交是持锁下的**后提交步骤**，
   失败退 `4`、磁盘保留当前状态且不回滚 Markdown。
3. **`W26` 分布**：§4 / §6——`commit` 标记存在即不回滚、不发 `W26`；矩阵只有 `P2` / `P3` / `P4` 发 `W26`；
   陈旧 `run.lock` 元数据覆盖**不是** `txn_recovered`。
4. **`txn_id` 可实现性与保证域**：§1 A-59 行 / §4——持锁后按 `max(持久 seq, 现存目录最大序号, **process-local lastSeq**) + 1` 分配、
   `txn_id = "t" + 16 位零填充小写 hex`（无 Unix 秒段，避免 `seq > 0xFFFFFFFF` 破坏正则），`seq` 超 `uint64` 上限 ⇒ fail closed 退 `5` + `E15`，
   `O_EXCL` 建目录兜底；保证域 = **同进程唯一且单调（由公式中的进程游标项无条件给出，不是旁注）** + **同 vault、当前 `.index/txn` 生命周期内
   跨进程唯一且字典序单调不减**；**不声称**跨生命周期永久唯一（`.index/` 可被合法删除并重置 `seq`，如实登记）。
5. **B3 与 `W27` 的统一**：§5.2——`S3` preflight 定盘 accepted write-set 与 skipped 集合，原子性只覆盖前者；
   `skipped[].kind` 恒 2 值、退出码 `3` 语义一字不改；跳过永不触发整事务 abort。
6. **锁 inode 安全**：§3 与 §1 A-53 行——被加锁 inode 在持锁期**零替换**，正文**只有一种写法**：同 fd 原地 `Ftruncate` + `Pwrite` + `Fsync`
   （R5 已撤销 `run.lock.meta` 分离备选，`grep -rn 'run\.lock\.meta' internal/` 恰 0 处）；`tmp` + `rename` / `unlink` 会让另一进程在新 inode 上再拿一把 `flock` 而双写，故**严禁**，
   并由 `TestInodeReplacementCannotYieldSecondLock` 正面反证。
7. **恢复不覆盖崩溃后编辑**：§4.1 三分支（`B-R1` 零写入 / `B-R2` 安全还原 / `B-R3` 冲突阻断退 `5` + `E15` 且不发 `W26`）；
   新建文件回滚走 `quarantine/` 原子移入，**不物理删除字节**；矩阵 `P2` / `P3` / `P4` 的成立前提被横切规则显式限定（§6）。
8. **候选集合层漂移**：§2 的 `S3` ⓪——锁内**重新执行候选发现**，`S0` 只作提示；否则另一写者在 `S0` 后新增 / 删除的匹配文件
   会被漏做或对不存在的路径做计划。
9. **pre-intent residue**：§4.2——无 `intent.json` 的孤儿目录可安全移除 / 就地闭合、不发 `W26`、不得当作可解析事务
   （其可清理性由 §4 的 intent 发布屏障**证明**，而非假设）。
10. **恢复的原子性（R4 P0-1）**：§4.1 两遍模型——**Pass A 全量只读判定、Pass B 才写盘**；任一 `B-R3` ⇒ 整事务零写入、
    保持未闭合、退 `5` + `E15`；结果与冲突项在 `files[]` 中的位置**无关**；
    反证 `TestRecoverIsAllOrNothingAcrossFiles` / `TestRecoverConflictAtLastFileRollsBackNothing`（`T-…-072`）。
11. **intent 发布屏障（R4 P0-2）**：§4——`pre/<n>` 全量 `fsync` → `fsync` 两级目录 → `intent.json.tmp` → `fsync(tmp)` →
    `rename` → `fsync(txn 目录)`，**屏障完成后才允许首个权威 `rename`**；由此「无 `intent.json` ⇒ 零权威写入」**可证明**，
    且磁盘上永不出现「名为 `intent.json` 的半个 JSON」；反证 `TestIntentBarrierBeforeFirstAuthoritativeRename`。
12. **损坏 intent fail closed（R4 P0-3）**：§4.2 第二行——`stat` 成功但不可解析 / `journal_version` 不支持 ⇒
    **不降级为 residue**：原样保留、权威零写入、退 `5` + `E15`、不发 `W26`、不参与自动清理，等待人工处理；
    反证 `TestCorruptIntentFailsClosed` 与 `TestPreIntentResidueIsNotATransaction` 成对钉死两者的区分。
13. **恢复永不越界（R4 P0-4）**：§4.3——`files[].path` / `pre_bytes_ref` 必须是**规范化的** vault / txn 相对路径，
    拒绝绝对路径、`.` / `..` 分量、未规范化串与 symlink 逃逸；任一违规在 **Pass A** 即 fail closed（零写入、退 `5` + `E15`）；
    恢复写入面恒为「校验通过的权威路径 + 本事务 txn 目录内」两处；
    反证 `TestRecoverNeverTouchesOutsideTxn` / `TestRecoverRejectsEscapingPaths`。
14. **前像不可信即 fail closed（R5 P0-3a）**：§4.1.1——`Pass A` 除路径与 `current_hash` 外，还必须校验
    `pre_bytes_ref` **存在 / 为普通文件 / `size == pre_size` / 内容哈希 `== pre_hash`**；缺失或损坏时**绝不进入 Pass B**
    （否则会把错误字节写回权威 Markdown），整体零写入、退 `5` + `E15`、不发 `W26`；反证 `TestRecoverRejectsCorruptPreimage`。
15. **全局只读分类先于任何回滚写（R5 P0-3b）**：§4.1.2——`Scan` 全集（含多个 `OpenTxn` 与并存 `CorruptTxn`）必须**先整体分类完毕**；
    只有「恰一个 `OpenTxn` + 全量可恢复 + 无 `CorruptTxn`」才进 `Pass B`，否则在**任何回滚写之前**整体阻断；
    **严禁**「先恢复一个、后发现异常再返回 `5`」（那是先改盘后报错，零写入变假承诺）；
    即便协议论证正常路径最多一个 `OpenTxn`，**异常多实例仍必须 fail closed**；反证 `TestMultipleOpenTxnFailsClosedBeforeWrites`。
16. **`exit 5` 零写入的边界（R5 P0-4）**：§9.1——保证面是「**本次请求事务零权威写入**」，
    **不是**「整条命令绝对零磁盘变化」；唯一例外已在册：`S2` 成功恢复前一事务（`W26`）后 `S3` 退 `5`，
    磁盘相对命令启动时可能已变但那是**已记录的恢复结果**；`m6_precheck_exit5.sh` 的 `git status` 前后相等断言
    **必须**在「无待恢复事务」前置下执行（`T-…-074` / `T-…-075` 已按此口径同步）。
17. **命令类别与锁 / 恢复覆盖面（R6 P0-1）**：§16.1——命令被划为**封闭三类**：**A 类权威写命令**走完整 `S1` ~ `S8` 事务；
    **B 类 index 维护命令**（`eg index build` / `eg index rebuild` / `eg index sync`）**必须拿同一把** `vault/.index/run.lock`
    并**先过 `S2` 恢复屏障**，但**不分配 `txn_id`、不写 `intent`、不写 `commit` / `abort`、不做 Git 提交**，写入面只有派生 DB 家族；
    **C 类只读命令**（`index status` / `bench`（**顶层命令**，R8 §18.2） / `search` / `card` / `rel` / `check` / `report` 等）**不拿锁、不触发恢复**；
    权威写后的增量索引更新（`internal/cli/index_after_write.go`）**必须仍在同一锁持有期内**（`S6` 之后、`S8` 释放锁之前）；
    反证 `TestIndexMaintenanceUsesRunLock`（`T-…-072`）。
18. **`.index/` 共存契约与 `RemoveAll` 禁令（R6 P0-2，删除面口径由 R9 §19.1 / §19.2 定稿）**：§16.2——M6 交付后**任何路径禁止** `os.RemoveAll(vault/.index)`；
    删除面被拆成**两条互不复用**的策略：**① Rebuild 全清扫**——持锁并过 `S2` 与 reserved 类型检查后，逐项删除 `.index/` 下**所有非 runtime-reserved 条目**
    （DB 家族 **+ 外部污染 + 陈留临时产物 + 意外子目录**），再走 `Build` 全量路径（R6 原「只删索引自有物」表述**已作废**，它会留下污染使 rebuild 后仍 `corrupt`，
    与实码 `TestRebuildRemovesPollution` 冲突）；**② Build 失败清理**——**只**删本次 build attempt 亲手创建的产物，**保留**调用前既有污染、**不碰** runtime-reserved；
    `.index/` **目录本身**的口径**由 R10 §20.3 覆盖为条件化保留**（若且仅若目录是本次 `Build` 创建且清理后为空 ⇒ 允许 `os.Remove(indexDir)`，其余一律保留，且**仍禁止** `os.RemoveAll`）；
    `run.lock`（含其 **inode**）、`txn/` 全树（`intent.json` / `pre/` / `quarantine/` / `commit` / `abort`）、`txn/seq`、
    未闭合与损坏事务日志在**两条策略下都必须逐项保留**；`flock` 忙时**不得开始**任何 index 维护（退 `5` + `E16`、零 DB 写入、绝不强抢）；
    反证 `TestRebuildRemovesPollution`（历史用例不改）/ `TestIndexRebuildPreservesTxnAndLockInode`（R9 增强含 `leftover.tmp`）/
    `TestIndexBuildFailurePreservesTxnAndLock`（R9 增强含调用前污染）（`T-…-072`）。
19. **M6 runtime reserved entries 与类型违规的分层处置（R6 P0-3，口径由 R8 §18.1 定稿）**：§16.3——**保留 M5 事实**：`AllowedFiles()` 仍恰 3 个 DB 文件、语义一字不改；
    另立**第二张封闭表** `RuntimeReservedEntries()`（**恰 2 项**：`run.lock` 必须是普通文件、`txn` 必须是目录），
    `unexpectedFiles()` 的判定面改为「`allowed ∪ reserved` 之外才算污染」，`Inspect` 在**保留项类型正确**时**不再**把这两项判 `W24 unexpected_file`；
    **类型错误**（`run.lock` 是目录 / `txn` 是普通文件）或**同名 symlink** 按**三层**处置（R8 §18.1，**不是**无条件退 `5`）：
    ① `internal/index` 只产**结构化底层事实**——`Inspect` 仍**只读、永不返 error**，判 `corrupt` + `W24`，另由专用入口回传结构化违规信息，**不新增对外诊断码**；
    ② **A / B 类写路径**把该事实映射为 `E15` + 退 `5`（零写入、不重建、不删除）——这是「fail closed」的**唯一**落点；
    ③ **C 类只读路径**（`index status` / `search` / `card` / `rel` 等）沿用 M5 语义产 `W24`、读查询再加 `Q5` 降级，**保持既有退出码、绝不退 `5`**；
    反证 `TestIndexInspectAllowsM6RuntimeEntries` / `TestIndexStatusReservedTypeViolationReportsW24WithoutExit5` /
    `TestReadPathDegradesOnReservedTypeViolation`（均归 `T-…-072`），写路径退 `5` 反证归 `T-…-074`。
20. **历史门禁阶段化与现态重钉（R6 P0-4）**：§16.4——`071` ~ `074` **不跑**历史 e2e 全量（R7 §17.5 已把绝对数「50 支」作废，改以实跑清单与静态反证表达），只跑各自 targeted tests + `make lint` + `go test ./... -count=1`；
    `T-…-075` 独家负责把仍逐字断言「M6 能力不存在」「`.index/` 恰 3 项」的历史脚本按**M6 已交付事实**重钉后**全跑 61 支**。
    重钉口径**只许**「保留历史事实 + 新增现态双侧锁」，**禁止**删断言 / `skip` / allowlist / frozen-red；
    至少涉及 `m5_acceptance.sh` / `m5_index_build.sh` / `m5_index_corrupt_rebuild.sh` / `m5_read_path_index.sh`，
    **最终清单以 `075` 实跑发现为准**。A-56 的「M1 ~ M5 的 50 个既有 e2e 一条不改」是**强校验开关面**的承诺
    （默认宽松 ⇒ 退出码期望零漂移），**不**等于「M6 全程不动任何历史脚本」——`.index/` 布局面的现态重钉是本条的显式例外并在此在册。
21. **退出码 `5` 的落地形态（R7 P0-1）**：§17.1——**唯一形态**为「`internal/cli/exitcode.go` 定义常量 `ExitPrecheckOrLock = 5`（值恰 5、常量恰一个）
    + `internal/cli` 定义**恰两类**类型化错误 `PrecheckFailedError`（`E15`）/ `LockUnavailableError`（`E16`）
    + `internal/cli/exit.go` 的 `ExitCodeFor` 增**恰两条** `errors.As` 分支映射为 `5`
    + `cmd/eg/main.go` 保持**全仓唯一** `os.Exit` 调用点」；**`internal/**` 零 `os.Exit`**，`ExitCode5Enabled()` 由恒 `false` 翻转为恒 `true`；
    R1 ~ R6 的「`os.Exit(5)` 由 `exitcode.go` 承载」**已撤销**，全文再无该表述；反证 `TestExitPrecheckOrLockIsFive` /
    `TestExitCodeForPrecheckFailed` / `TestExitCodeForLockUnavailable`（均归 `T-…-074`）。
22. **退出码 `5` 的验证口径（R7 P0-2）**：§17.2——`grep os.Exit(5)` 类判据**一律作废**，替换为四条机器验证
    V-R7-1（常量值恰 5）/ V-R7-2（两类类型化错误各映射为 5，且 `ExitCodeFor` 内 `ExitPrecheckOrLock` 出现恰 2 次）/
    V-R7-3（`internal/**` 零 `os.Exit`）/ V-R7-4（`cmd/eg/main.go` 恰一处 `os.Exit`）；`M-006` 判据 12 与 `T-…-074` 已同步；
    副收益在册：7 支历史门禁脚本的「`internal/` 下 `os.Exit(5)` 恰 0」断言在本形态下**M6 后仍恒真**，无需重钉、不产生互斥死结；但**仍需** `075` 重钉与「`5` 未启用」绑定的另一批断言（`m5_acceptance.sh` 的 `CODES` / `N5C`、`m3_docs_commands.sh` / `m5_docs_commands.sh` 的 INSTALL `5` 行），二者互不重叠。
23. **退 `5` 断言的阶段归属（R7 P0-3）**：§17.3——`071` / `072` / `073` **只**返回或传播可被 `074` 映射的 typed / sentinel 错误与 `E15` / `E16`，
    其 targeted e2e **只允许**断言「非零退出 + 诊断码 + 权威零写入 + 事务状态」，**既不得**断言 `5`（未启用）**也不得**断言恰 `1`（`074` 落地后会反向变红）；
    退出码 `5` 的端到端断言集中在 `074`（单命令面）与 `075`（全量面）；`ExitCode5Enabled()` 的 6 处 Go 消费点由 `074` 重钉，
    shell 常量表断言（`m5_acceptance.sh` 的 `CODES` / `N5C`）由 `075` 按 §16.4 双侧锁口径重钉。
24. **阶段化与「不回归」的自相矛盾清偿（R7 P0-4）**：§17.5——`071` ~ `074` 的「不回归」条**只保留** N-1 数量未减 / N-2 文件名一支不删 /
    N-3 本 task 对历史脚本零改动 / N-4 无 `skip` / allowlist / frozen-red 新增**四项静态反证**，
    **不得**再含「逐支全绿 / 逐个退 0」这类执行面要求，过期绝对数「50 支」不再作为判据数值；
    历史 e2e 的逐支执行与现态重钉**唯一**归 `075`（支数以实跑清单为准，规划期锚定 `≥ 61`）。
25. **只读路径与写路径的分层、`E15` 原因口径、命令名纠正（R8 P0-1 ~ P0-3）**：§18——
    ① `.index/` runtime reserved entry **类型违规**按**三层**处置：`internal/index` 只产结构化底层事实（`Inspect` 只读、永不返 error、判 `corrupt` + `W24`、不新增对外诊断码）；
    **A / B 类写路径**映射 `E15` + 退 `5`（零写入、不重建、不删除）；**C 类只读路径**沿用 `W24`（读查询再加 `Q5`），**保持既有退出码、绝不退 `5`**（§18.1）；
    ② `E15` **撤销**一切精确形态数，统一为「**写前安全复核这一语义大类**，**当前枚举 8 个原因**，清单可增、精确数字不作门禁判据」（§18.4）；
    ③ 顶层命令逐字为 **`eg bench`**，仓库**不存在** `eg index bench`（`IndexSubcommands()` 恰 `build` / `rebuild` / `status` / `sync`），
    全文 C 类清单与 `T-…-074` 的 `TestExit5NeverOnReadOnlyCommands` 用例集已逐处改为 `bench`（§18.2）；
    ④ `T-…-072` 的 `verify.test` **必须**含 `./internal/index`（§18.3）。
26. **两条删除策略定稿与只读断言精确化（R9 P0）**：§19——
    ① **Rebuild = 清除全部非 runtime-reserved 条目 + 全量 Build**（外部污染**必须删除**，`TestRebuildRemovesPollution` 保住），
    `run.lock` **同一 inode** 与 `txn/` **全树**逐项保留（§19.1）；
    ② **Build 失败清理 = 只删本次 attempt 产物**，调用前既有污染**保留**、runtime-reserved **不碰**（§19.2；`.index/` **目录本身**的口径**已被 R10 §20.3 覆盖为条件化保留**）；
    ③ 两者**不得**复用同一函数或同一语义（§19.3）；
    ④ 反证按 R9 增强：`TestIndexRebuildPreservesTxnAndLockInode` 同时构造 `leftover.tmp` 断言污染被删、
    `TestIndexBuildFailurePreservesTxnAndLock` 构造调用前污染断言其保留而本次产物被删（§19.4）；
    ⑤ R8 只读降级断言**精确化**为「**恰一条 `W24` + 恰一条 `Q5`**」，**不得**再写宽泛的「`W22` / `W23` / `W24` + `Q5`」
    ——reserved 类型违规的原因**已确定为 `corrupt`**，只能是 `W24`（§19.5）。
27. **Build 失败清理的「空目录可删」唯一例外（R10 P0）**：§20——
    ① R9 §19.2 的「`.index/` 目录本身**一律**保留」**被覆盖**为**条件化保留**：`Build` 进入时记录 `indexDirExistedBefore` 与 `createdPaths[]`（§20.1）；
    ② 失败清理**第一阶段**仍只删 `createdPaths` 内的 DB / 临时产物，调用前既有污染与 runtime-reserved **永远保留**（§20.2，越权清理禁令**不放松**）；
    ③ **第二阶段**：**若且仅若** `indexDirExistedBefore == false` **且**清理后 `ReadDir(indexDir)` 为空 ⇒ 允许 `os.Remove(indexDir)`；
    其余情形（目录调用前已存在 / 清理后非空 / 仍有 runtime-reserved / `ReadDir` 失败）一律**保留目录**；**仍禁止** `os.RemoveAll(indexDir)`（§20.3）；
    ④ **B 类路径永不满足该例外**——它已先创建并锁住 `.index/run.lock`，`indexDirExistedBefore` 必为 `true` 且清理后目录非空，
    故 B 类与 `Rebuild` 路径**绝不删目录、绝不替换 `run.lock` inode**（§20.3 / F-R10-3）；
    ⑤ 历史实码 `internal/index/build_test.go::TestBuildRejectsUnknownSkippedKind` 列为**历史反证**，**一字不改且必须继续绿**（§20.4 / F-R10-1）；
    ⑥ R10 **零新增测试名**——「预存目录 + 污染 + reserved 必留」与「本次创建的空目录失败后删除」均以 subcase 形式扩充既有
    `TestIndexBuildFailurePreservesTxnAndLock`，post-close 新增 Go 测试**仍累计 10 支**（§20.4 / §20.5）。

---

## 16. R6 定稿：命令类别、`.index/` 共存契约与历史门禁阶段化（`T-…-070` post-close correction）

**本节地位**：R6 是 `T-…-070` **收口（`done`）之后**的 post-close correction，触发源是对 M5 已交付索引层的**实码核对**
（`evergreen` HEAD `db64ce7`）。四条冲突全部 P0，且原 `071` ~ `075` 规划**无任务明确承接**，故在此闭合。
本节**不新增**诊断码 / 退出码 / 命令 / e2e，**不改**任何裁决行的选项与结论，只把 A-53 / A-55 / A-58 的既有裁决**投影到 `.index/` 共存面**。

### 16.0 实码核对事实（登记，不是推测）

| # | 事实（逐字可查） | 位置 | 与 M6 的冲突 |
|-|-|-|-|
| F-R6-1 | `Rebuild` 首句 `os.RemoveAll(dir)`，注释逐字写「先删掉整个 dir（含 `eg.db` / `-wal` / `-shm` 与任何外部混入的污染文件）」 | `internal/index/rebuild.go:25` | 删除未闭合 `txn/`、`pre/`、`quarantine/`、`seq`（销毁恢复证据）；替换正被 `flock` 持有的 `run.lock` inode ⇒ 撞 A-53 |
| F-R6-2 | `Build` 失败清理 `_ = os.RemoveAll(dir)` | `internal/index/build.go:152` | 同 F-R6-1，且发生在**失败路径**上（最需要保留证据的时刻） |
| F-R6-3 | `AllowedFiles()` 恰 3 项 `{eg.db, eg.db-wal, eg.db-shm}`；`unexpectedFiles()` 把集合外条目列为污染并由 `Inspect` 判 `unexpected_file` | `internal/index/schema.go:42`、`internal/index/corrupt.go:200` / `:130` | `run.lock` / `txn/` 被判污染 ⇒ 索引恒 `corrupt` ⇒ 读路径恒降级 ⇒ `EnsureBuilt` 每次走 `Rebuild` ⇒ 与 F-R6-1 组成放大回路 |
| F-R6-4 | index 维护命令实现里**零**锁调用（`grep -rn 'flock\|run\.lock' internal/index internal/cli/index*.go` 恰 0 处） | `internal/cli/index.go`、`internal/cli/index_sync.go` | 可在写事务临界区之外并发删改 `.index/` |
| F-R6-5 | 写后增量索引更新是六条写命令共用的单点 helper | `internal/cli/index_after_write.go` | 若在释放锁之后调用，另一进程可在窗口内 `rebuild` 并删/换文件 |

### 16.1 命令三类别与锁 / 恢复覆盖面（R6 P0-1，A-52 / A-54 / A-55 的类别投影）

§2 的「写命令 = 会改动 `vault/` 下权威文件或 Git 历史」在此被**精确化为 A 类**，并新增 B 类「派生写」：

| 类别 | 成员（封闭） | `run.lock` | `S2` 恢复屏障 | `txn_id` / `intent` / `commit` 标记 | Git 提交 | 写入面 | 锁不可用 |
|-|-|-|-|-|-|-|-|
| **A 类：权威 Markdown 写命令** | 会改动 `vault/` 权威文件或 Git 历史的命令（§2 定义不变） | **必须持有**，走完整 `S1` ~ `S8` | **必须先过** | **三者齐备** | 持锁下的 `S7` 后提交步骤 | 权威 Markdown + `.index/txn/` + 派生 DB（写后增量，见下） | 退 `5` + `E16`、零写入 |
| **B 类：index 维护命令** | `eg index build`、`eg index rebuild`、`eg index sync`（恰 3 条，**不含**任何新命令） | **必须持有同一把** `vault/.index/run.lock` | **必须先过**（否则会在半应用态上建索引） | **一律不分配 / 不写**（它不是权威事务） | **不做** | **只有派生 DB 家族 + 自建临时产物**；`rebuild` 另可删除 `.index/` 下**非 runtime-reserved** 条目（含外部污染，R9 §19.1），**永不**触碰 `run.lock` / `txn/` | 退 `5` + `E16`、**零 DB 写入**，绝不强抢 |
| **C 类：只读命令** | `eg index status`、`eg bench`（**顶层命令，仓库不存在 `eg index bench`**，R8 实码核对）、`search`、`card`、`rel`、`check`、`report` 等 | **不拿锁** | **不触发** | 不涉及 | 不涉及 | **零写入** | 不适用（永不因锁忙失败） |

四条硬规则：

1. **B 类必须先恢复再维护**：`S1` 取锁 → `S2` 恢复屏障 → 才允许建 / 重建 / 同步索引。理由：在未恢复的半应用态上建出的索引
   会把「半应用的中间字节」当权威内容索引进去，而恢复屏障完成后这些字节会回到前像 ⇒ 索引与权威**必然**不一致。
2. **B 类不得伪装成事务**：不分配 `txn_id`、不写 `intent.json` / `commit` / `abort`、不产 `W26`（`W26` 只在真实回滚时由 `S2` 产出）。
   `.index/txn/` 对 B 类是**只读输入**（用于判断「有无未闭合事务」），不是它的写入面。
3. **写后增量索引更新在同一锁持有期内**：`internal/cli/index_after_write.go` 的调用点必须落在 `S6`（`commit` 标记）之后、
   `S8`（释放锁）之前。**严禁**「先释放锁、再补索引」——那会给另一进程留出「在权威已变而索引未更新时 `rebuild`」的窗口。
   增量失败仍按 M5 口径只报 `W22` / `W23` / `W24` 并**不改退出码**（M5 事实不变）。
4. **C 类零锁开销**：只读命令**不得**因为 `flock` 忙而失败或阻塞；读到损坏 / 缺失索引时按 M5 既有降级语义处理（`Q5` / 全量扫描），
   这保证「一个卡住的写者不会让 `search` 不可用」。**R8 补强（断言口径由 R9 §19.5 精确化）**：`.index/` 出现 runtime reserved entry 类型违规时，C 类**同样**只按 M5 语义产**恰一条 `W24`**
   （读查询**再加恰一条 `Q5`**——reserved 类型违规的原因**已确定为 `corrupt`**，因此**只能**是 `W24`，**不得**再写成「`W22` / `W23` / `W24` 三者之一」）、退出码不变，**绝不**退 `5`、**绝不**触发重建或删除（分层定稿见 §18.1）。

### 16.2 `.index/` 共存契约与 `RemoveAll` 禁令（R6 P0-2）

**一句话**：`.index/` 从「纯派生目录」变成「**派生物 + 不可重建运行时证据**混居目录」，因此**目录级删除永久失效**，只允许**按项删除**。

| 规则 | 内容 | 反证 |
|-|-|-|
| **R6-D1** | M6 交付后 `internal/index/**` 与 `internal/cli/index*.go` 中 `os.RemoveAll(<indexDir>)` 命中**恰 0 处**（`scratch.go` 对**自建子目录**的清理不在禁令内） | `grep -rnE 'os\.RemoveAll\(' internal/index internal/cli/index*.go` 的每一处命中都必须指向 DB 家族成员或自建临时目录，**不得**指向 `.index/` 本身 |
| **R6-D2**（**已由 R9 §19.1 替换，本行按 R9 读**） | R6 原表述「只删 `AllowedFiles()` 三项与自建临时产物，其余条目**一律不动**」**错误**：它会把外部污染（如 `leftover.tmp`）留在盘上，使 `rebuild` 后 `Inspect` 仍判 `corrupt`，与实码 `internal/index/rebuild_test.go::TestRebuildRemovesPollution` 直接冲突。**R9 定稿**：重建 = 「**清除全部非 runtime-reserved 条目** + 全量重建」——持锁并过 `S2` 与 reserved 类型检查后，逐项删除 `.index/` 下**所有**不在 `RuntimeReservedEntries()` 内的条目（DB 家族、外部污染文件、陈留临时产物、意外子目录），**`run.lock` 同一 inode 与 `txn/` 全树逐项保留**，随后走 `Build` 全量路径 | `TestRebuildRemovesPollution`（历史用例，一字不改仍绿）+ `TestIndexRebuildPreservesTxnAndLockInode`（R9 增强：同时构造 `leftover.tmp` 并断言其被删除） |
| **R6-D3**（**已由 R9 §19.2 收窄；目录口径再由 R10 §20.3 覆盖，本行按 R9 + R10 读**） | 失败清理**与 R6-D2 语义相反、不得共用同一实现**：`Build` 失败**只**回收**本次 build attempt 亲手创建**的产物（`eg.db` / `-wal` / `-shm` 与本次的临时产物），**不得**删除调用前既已存在的污染，**不得**触碰 runtime-reserved；`.index/` **目录本身默认保留**（M6 后该目录承载 `run.lock`，B 类维护恒在其已存在的前提下运行），**唯一例外**按 **R10 §20.3**：若且仅若 `indexDirExistedBefore == false` 且清理后 `ReadDir` 为空 ⇒ 允许 `os.Remove(indexDir)` 删除本次创建的**空**目录（**仍禁止** `os.RemoveAll(indexDir)`） | `TestIndexBuildFailurePreservesTxnAndLock`（R9 增强：构造调用前污染并断言其**保留**、本次产物**被删**；R10 §20.4 再扩 subcase A / B）+ `TestBuildRejectsUnknownSkippedKind`（历史实码，一字不改，锁住例外分支必须存在） |
| **R6-D4** | **must-preserve 清单**（逐项）：`run.lock` **及其 inode**、`txn/` 全树（`intent.json`、`pre/`、`quarantine/`、`commit`、`abort`）、`txn/seq`、未闭合事务目录、损坏事务目录 | 两个测试均以「维护前后 `stat` 的 `st_ino` 逐字相等 + `txn/` 子树字节逐字相等」为断言 |
| **R6-D5** | `flock` 忙 ⇒ **不得开始** index 维护：退 `5` + `E16`，`.index/` **零字节写入**（含不创建目录、不建临时文件） | `TestIndexMaintenanceUsesRunLock` 的第二组：持锁进程存活时另一进程 `index rebuild` 退 `5` + `E16` 且 DB `mtime` / `sha256` 不变 |
| **R6-D6** | 索引「可重建派生物」这一 M5 结论**不变**，但**适用面**被显式收窄为 **DB 家族**；`.index/txn/` 是**不可重建**的事务证据，`.index/` 目录整体**不再**可被 M6 路径删除 | 与 §4 的「无未闭合事务时 `.index/` 可被用户合法删除」并存：**用户手动** `rm -rf .index`（无未闭合事务）仍合法，`eg` **自身路径**不再这么做 |

### 16.3 M6 runtime reserved entries（R6 P0-3）

| 项 | 内容 |
|-|-|
| **保留 M5 事实** | `AllowedFiles()` **仍恰 3 项** `{eg.db, eg.db-wal, eg.db-shm}`，语义、注释口径与既有测试（`TestCorruptUnexpectedFile` 等）保留的「集合外文件即污染」结论**不被推翻** |
| **新增第二张封闭表** | `RuntimeReservedEntries()` **恰 2 项**：`run.lock`（**必须是普通文件**）、`txn`（**必须是目录**）。表是**封闭**的——新增第三项需改合同 |
| **`Inspect` 判定面** | 污染判定改为「条目 ∉ `AllowedFiles() ∪ RuntimeReservedEntries()`」；两个保留项**类型正确**时在盘**不再**产 `W24 unexpected_file`，索引可保持 `healthy` |
| **类型错误 fail closed（**已由 R8 §18.1 分层，本行按分层读**）** | `run.lock` 是目录 / `txn` 是普通文件 / 任一项是 **symlink**（含指向合法目标的 symlink）⇒ **fail closed**，但**分三层**：① 底层 `internal/index` 只产**结构化事实**（`Inspect` 仍只读、不返 error、判 `corrupt` + `W24`，另由专用入口返回结构化违规错误，**不新增对外诊断码**）；② **A / B 类写路径**把它映射为 `E15` + 退 `5`、零写入、不重建、不删除；③ **C 类只读路径**沿用 `W24`（读查询再加 `Q5`）且**保持既有退出码、绝不退 `5`**。R6 原表述「一律退 `5` + `E15`、不走 `W24`」**只对写路径成立**，对 Inspect 与只读路径**已作废**（理由：`internal/index/corrupt.go` 逐字规定「坏索引是诊断不是失败，调用方不得据此退非 0」、`eg index status` **恒退 `0`**） |
| **零新增码** | 复用既有 `W24`（真污染 + 只读路径下的 reserved 类型违规）/ `E15`（写路径的类型违规 fail closed）/ `E16`（锁忙）/ `W26`（恢复留痕）；M6 新增诊断码**仍恰 5 条**；`internal/index` 的损坏子因集合**仍恰 9 值**（`Reasons()` 不扩张，`TestReasonsClosed` 一字不改仍绿） |
| **原因清单口径（R8 §18.4 替换 R6 的形态计数）** | R6 原写「`E15` 第一类触发形态由六种增至七种」，与同章另列的 `seq` 溢出**自相矛盾**（实为 8 个具体原因）；**R8 撤销一切精确形态数**，统一表述为「`E15` = **写前安全复核这一语义大类**，**当前枚举 8 个原因**，清单可增、数字不作判据」；`E16` 触发面新增「B 类 index 维护锁忙」这一 R6 结论**不变**；两者仍**不新增诊断码、不新增退出码**；§9、§12、§14、`M-006` 判据 12、`T-…-074`、`T-…-075` 已按 R8 口径同步 |
| **退出码出口归属** | 类型违规的**判定**在 `internal/index`（`072`，只产结构化事实），**A / B 类写路径的进程退 `5`** 仍由 `internal/cli/exitcode.go` 定义常量 + `internal/cli/exit.go` 的 `ExitCodeFor` **单点映射**承载（`074`，§17.1），反证测试 `TestIndexReservedTypeViolationExits5` 归 `074`；**C 类只读路径的 `W24` / `Q5` 反证**（`TestIndexStatusReservedTypeViolationReportsW24WithoutExit5` / `TestReadPathDegradesOnReservedTypeViolation`）归 `072`（R8 §18.3）；B 类锁忙的退 `5` 由 `074` 扩充既有 `TestLockUnavailableExits5` 的用例集覆盖（**不新增测试名**） |
| **归属** | `RuntimeReservedEntries()` 的**单点定义**落在 `internal/index/reserved.go`（`T-…-072` 新增），`schema.go` / `corrupt.go` 只消费该单点，**不得**抄第二份字面量 |

### 16.4 历史门禁阶段化与现态重钉（R6 P0-4）

| 阶段 | 门禁范围（封闭） | 明确不做 |
|-|-|-|
| `071` ~ `074` | 各自 `verify.test`（targeted 包）+ `verify.lint`（`make lint`）+ `go test ./... -count=1` + 各自 `m6_*.sh` | **不跑**历史 e2e 全量（绝对数「50 支」已由 R7 §17.5 作废）、**不跑** `-race` 全量、**不跑**历史门禁 16 支、**不**推进版本 |
| `075` | 历史脚本**现态重钉** + 61 支 e2e 全跑 + `-race` + 历史门禁 16 支 + M6 三件套 | —— |

**重钉口径（唯一合法形态）**：

1. **只许**「**保留历史事实** + **新增现态双侧锁**」：历史断言（如「M5 期 `.index/` 恰 3 项」）改写为**带里程碑限定**的两侧断言——
   「M5 基线下恰 3 项」**且**「M6 现态下恰 3 项 DB 家族 + 恰 2 项 runtime reserved entries」，两侧都必须为真。
2. **禁止**四种放宽：删断言、`skip` / 条件跳过、allowlist / 豁免名单、frozen-red（冻结红项 + 失败签名）。
3. **已知至少涉及**：`m5_acceptance.sh`（总控内的 `.index/` 与 M6 缺席断言）、`m5_index_build.sh`、`m5_index_corrupt_rebuild.sh`、
   `m5_read_path_index.sh`；**最终清单以 `075` 实跑发现为准**，实跑新暴露的脚本一律按同一口径重钉并在 M6 验收报告逐条留痕。
4. **A-56 的边界澄清**：A-56 的「M1 ~ M5 的 50 个既有 e2e **一条不改**」是**强校验开关面**的承诺（默认宽松 ⇒ 既有退出码期望零漂移），
   **不**等于「M6 全程不动任何历史脚本」；`.index/` 布局面的现态重钉是该承诺的**显式例外**，在此在册，
   并由 `075` 在 M6 验收报告中逐条对账（改了哪一行、保留了哪条历史事实、新增了哪条现态锁）。

### 16.5 R6 的封闭性自证

- 新增章节**恰 1 个**（§16），`^## ` 小节数 16 → **17**；裁决行**仍恰 9 行**（A-53 / A-55 只追加指向 §16 的定位语，选项与结论一字不改）。
- 新增诊断码**仍恰 5**（`E15` / `E16` / `W26` / `W27` / `W28`）；退出码全集**仍恰** `{0,1,2,3,4,5,6}`；命令数**仍恰 22**；M6 e2e **仍恰 11**、磁盘总数**仍 ≥ 61**。
- 新增 Go 反证测试**恰 5 支**：`TestIndexRebuildPreservesTxnAndLockInode`、`TestIndexBuildFailurePreservesTxnAndLock`、
  `TestIndexInspectAllowsM6RuntimeEntries`、`TestIndexMaintenanceUsesRunLock`（**4 支归 `T-…-072`**，落在 `internal/index` 包，覆盖共存 / 保留 / 取锁三面；其中 `TestIndexInspectAllowsM6RuntimeEntries` 的**类型违规断言已由 R8 §18.1 改为「`Inspect` 判 `corrupt` + `W24` + 结构化违规事实、不返 error、不重建、不删除」**，`E15` 断言归写路径），
  加 `TestIndexReservedTypeViolationExits5`（**1 支归 `T-…-074`**，只覆盖「类型违规 ⇒ 进程退 `5`」的**出口**面，因为退出码 `5` 的常量与 `ExitCodeFor` 映射只属 `074`（§17），`072` 只返回可被映射的类型化错误）；
  B 类锁忙的退 `5` **不新增测试名**，由 `074` 扩充既有 `TestLockUnavailableExits5` 的用例集覆盖。
- 形态计数的**唯一两处**变更已在 §16.3 在册并全链同步：`E15` 第一类的原因清单扩张（R6 写作「6 → 7 形态」，**该精确计数已由 R8 §18.4 撤销**，
  现口径 = 写前安全复核语义大类、当前枚举 8 个原因）、`E16` 新增 **B 类锁忙**触发面；
  二者均**不新增码 / 不新增退出码 / 不新增命令 / 不新增 e2e 文件**（B 类与类型违规的 e2e 断言并入既有 `m6_precheck_exit5.sh` 与 `m6_ci_dep_gate.sh`）。
- 承接归属**唯一**：`internal/index/{build,rebuild,corrupt,schema}.go`（+ 新增 `reserved.go`）与 `internal/cli/{index,index_sync}.go`
  的兼容改造**只属 `072`**；`071` 不碰 `internal/index`；`074` 只承接 `E15` / `E16` 的进程出口；`075` 只承接重钉与全跑。
- 本节**零产品代码**：`internal/**` 在 `T-…-070`（含 R6）内一个字节都不改；`T-…-070` 的 `done` 生命周期**不回退**。

---

## 17. R7 定稿：退出码 `5` 的落地形态与 `os.Exit` 单点（`T-…-070` post-close correction 之二）

**本节地位**：R7 与 R6 同为 `T-…-070` **收口（`done`）之后**的 post-close correction，触发源同样是**实码核对**（`evergreen` HEAD `db64ce7`）。
R1 ~ R6 反复出现的表述「`os.Exit(5)` 由 `internal/cli/exitcode.go` **单点**承载」与本仓既有架构**直接冲突**，且会当场打红 7 支历史静态门禁脚本。
本节**撤销**该表述，改定为「**常量 + 类型化错误 + `ExitCodeFor` 单点映射 + `cmd/eg/main.go` 唯一 `os.Exit`**」。
本节**不改**任何裁决的选项与结论（A-58 仍取①：`5` = 写前强校验失败 **或** 锁不可用，两者均零写入），
**不新增**诊断码 / 退出码 / 命令 / e2e 文件，只改**落地形态与验证口径**。

### 17.0 实码核对事实（登记，不是推测）

| # | 事实（逐字可查） | 位置 | 与 R1 ~ R6 表述的冲突 |
|-|-|-|-|
| F-R7-1 | 文件头硬约束逐字：「业务层只返回带类型的错误，退出码只在本文件翻译一次」「各命令实现禁止调用 os.Exit（唯一允许处是 cmd/eg/main.go）」 | `internal/cli/exit.go:5` ~ `:7` | 要求 `exitcode.go` 出现 `os.Exit(5)` 等于**违反本仓架构第一条硬约束** |
| F-R7-2 | 逐字：「**本文件是全仓唯一允许调用 os.Exit 的地方**（T-…-003 Acceptance 的 grep 反证）」，函数体恰一行 `os.Exit(cli.New().Run(...))` | `cmd/eg/main.go:6`、`:19` | 单点在 `main.go`，不在 `internal/cli` |
| F-R7-3 | `ExitCodeFor` 逐字标注为「**唯一**的退出码翻译点」，`switch` 用 `errors.As` 逐类翻译；`6` 的先例是「`exitcode.go` 定常量 `ExitNeedConfirm = 6` + `NeedConfirmError.ExitCode()`，由 `ExitCodeFor` 分派」 | `internal/cli/exit.go:131` ~ `:171`、`internal/cli/exitcode.go:22`、`:95` ~ `:116` | 已有**可照抄的既有先例**：新增退出码从不靠 `os.Exit` 字面量 |
| F-R7-4 | 逐字：「退出码 5 属 S5（本阶段**全程不启用**）：这里刻意不定义常量，免得有人 import 之后顺手返回它」；`ExitCode5Enabled() bool { return false }` | `internal/cli/exitcode.go:24` ~ `:25`、`:62` | M6 的启用动作 = **定义常量 + 翻转该函数**，不是引入 `os.Exit` |
| F-R7-5 | 7 支历史静态门禁脚本断言 `internal/`（含 `cmd/`）下 `os.Exit(5)` 命中**恰 0** | `m3_acceptance.sh:278` / `:358` / `:461`、`m3_authorization.sh:354`、`m3_lifecycle_state.sh:186`、`m4_acceptance.sh:228`、`m5_acceptance.sh:122`、`m5_degrade_fallback.sh:251`、`m5_read_path_index.sh:276` | 若 M6 真在 `internal/` 写 `os.Exit(5)`，这 7 支历史脚本**当场转红**且**无法**按「保留历史事实」重钉——历史事实与现态**互斥** |
| F-R7-6 | `m5_acceptance.sh:117` ~ `:120` 逐字：「只 grep os.Exit 会得到空集，让判据形同虚设（本轮实测踩到过，故改为**常量表** + 空集反证双侧）」；随后断言常量表 `CODES` 逐字等于 `0 1 2 3 4 6`、取值 `5` 的常量数 `N5C` 为 `0` | `test/e2e/m5_acceptance.sh:117` ~ `:133` | 历史门禁**已经**把「退出码全集」的权威落点定在**常量表**——M6 必须在常量表上落地，才谈得上被机器验证 |
| F-R7-7 | `ExitCode5Enabled()` 有 **6 处**消费点：`internal/cli/exitcode_test.go:62`、`internal/cli/docs_test.go:224` / `:227`、`internal/cli/page_test.go:84`、`internal/cli/reconcile_test.go:623`、`test/e2e/m3_acceptance_test.go:123` | 同左 | 翻转该函数会打红 6 处 Go 测试 ⇒ 归属必须明确，否则 `074` 的 `go test ./...` 不可能绿 |

### 17.1 定稿形态（唯一形态，无备选）

| 层 | 落地物 | 硬约束 |
|-|-|-|
| **常量层** | `internal/cli/exitcode.go` 新增 `ExitPrecheckOrLock = 5`（命名沿用既有 `Exit*` 语义短语风格：`ExitUsage` / `ExitValidation` / `ExitPartialWrite` / `ExitCommitFailed` / `ExitNeedConfirm`；该名逐字表达 A-58 ①的**两类成因共用一码**），并把 `ExitCode5Enabled()` 由恒 `false` 改为恒 `true`；`exitcode.go:24` ~ `:25` 那段「刻意不定义常量」的注释同步改写为「M6 已启用，成因恰两类」 | 常量**恰一个**、值**恰 5**；**不得**为两类成因各定一个值为 5 的常量（否则 `075` 的 `N5C` 现态锁与「一码两成因」自相矛盾） |
| **错误层** | `internal/cli` 新增**恰两类**类型化错误：`PrecheckFailedError`（携 `E15`，承载 §9 / §16.3 的**写前安全复核语义大类，当前枚举 8 个原因**，其中 reserved 类型违规**只在 A / B 类写路径**入此错误，R8 §18.1）与 `LockUnavailableError`（携 `E16`，承载 A 类写命令与 B 类 index 维护命令的锁忙）。两者都实现 `Diagnostics()`，并 `Unwrap()` 到 `071` / `072` 提供的 sentinel（见 §17.3） | 业务层**只返回错误**，不查表、不算退出码；两类错误**各自**在 `ExitCodeFor` 中恰有一条 `errors.As` 分支 |
| **映射层** | `internal/cli/exit.go` 的 `ExitCodeFor` 增**恰两条**分支 → `ExitPrecheckOrLock`；分支位置在 `needCfm` 之后、`usage` 之前（`5` 的前提是「参数与命令已合法、进入写前复核或取锁阶段」，因此不与 `1` / `2` 争先） | `ExitCodeFor` 仍是**唯一**翻译点；未分类错误仍兜底 `1`，**不得**兜底到 `5` |
| **进程层** | `cmd/eg/main.go` **一行不改**，仍是全仓唯一 `os.Exit` 调用点 | `internal/**`（含 `internal/cli`、`internal/txn`、`internal/index`、`internal/plan`）**零** `os.Exit` |

**三条禁令**：① 禁止 `internal/**` 出现任何 `os.Exit`（不限于 `os.Exit(5)`）；② 禁止在 `ExitCodeFor` 之外的任何位置把错误翻译成数字退出码；
③ 禁止新增第二个 `os.Exit` 调用点（含测试辅助二进制以外的任何 `cmd/**` 新文件）。

### 17.2 验证口径的全量替换（R1 ~ R6 的 `grep os.Exit(5)` 判据一律作废）

**旧口径**（作废）：`grep -c 'os.Exit(5)' internal/cli/exitcode.go` ≥ `1`。
**新口径**（四条，全部机器可判，缺一即未达成）：

| # | 断言 | 命令（示意，实测以脚本落地为准） | 期望 |
|-|-|-|-|
| V-R7-1 | 常量存在且**值恰 5** | `go test ./internal/cli -run TestExitPrecheckOrLockIsFive -v`；静态侧 `grep -nE '^[[:space:]]*ExitPrecheckOrLock[[:space:]]*=[[:space:]]*5$' internal/cli/exitcode.go \| wc -l` | 测试绿；静态命中 `1` |
| V-R7-2 | **两类**类型化错误经 `ExitCodeFor` **各映射为 5** | `go test ./internal/cli -run 'TestExitCodeForPrecheckFailed\|TestExitCodeForLockUnavailable' -v`；并断言 `ExitCodeFor` 中 `ExitPrecheckOrLock` 出现次数恰 `2` | 全绿；命中 `2` |
| V-R7-3 | `internal/**` **零** `os.Exit` | `grep -rn 'os\.Exit(' internal/ \| grep -v '_test\.go' \| wc -l` | `0` |
| V-R7-4 | `cmd/eg/main.go` **恰一处** `os.Exit` | `grep -c 'os\.Exit(' cmd/eg/main.go` 与 `grep -rn 'os\.Exit(' cmd/ \| wc -l` | 均为 `1` |

**副收益（在册）**：F-R7-5 的 7 支历史门禁脚本断言（`internal/` 下 `os.Exit(5)` 恰 0）在 R7 形态下**M6 交付后仍恒为真**，
因此**不需要**重钉、也**不产生**「历史事实与现态互斥」的死结——这正是选择本形态而非 `os.Exit(5)` 的决定性理由。

### 17.3 阶段归属：谁返回错误、谁映射、谁断言退 `5`

| task | 允许做 | 禁止做 |
|-|-|-|
| `071` | 定义并返回锁不可用的 **sentinel / 类型化错误**（如 `txn.ErrLockUnavailable`）与 `E16` 诊断；定义恢复屏障阻断结论的类型化错误（如 `txn.RecoveryBlockedError`，携阻断类别与 `E15` 所需明细） | 不定义 `ExitPrecheckOrLock`、不改 `ExitCodeFor`、不出现 `os.Exit`；**targeted e2e 不得断言真实 `eg` 进程退 `5`** |
| `072` | 把恢复屏障的五类阻断与 `.index/` reserved 类型违规**以类型化错误 + `E15` 上报**；`W26` 只在真实回滚时产出 | 同上；`.index/` 相关反证只断言**诊断码 + 磁盘零变化 + 非零退出**，**不断言**退出码具体值 |
| `073` | 传播锁不可用错误、产出 `W27` / `W28` | 同上 |
| `074` | **唯一**落地点：常量 + 两类类型化错误 + `ExitCodeFor` 两条分支 + `ExitCode5Enabled()` 翻转；**并负责重钉 F-R7-7 的 6 处 `ExitCode5Enabled()` 消费点**（`internal/cli` 内 5 处 + `test/e2e/m3_acceptance_test.go` 1 处，均为 **Go 测试**，因为 `074` 的 `go test ./... -count=1` 必须绿），以及 `INSTALL.md` 退出码表中**仅** `5` 那一行的启用状态与两类成因 | 不改 shell 历史门禁脚本；不做全量文档收口与版本推进（`075`） |
| `075` | 按 §16.4 口径重钉 **shell** 历史门禁的常量表断言（`m5_acceptance.sh:127` ~ `:133`：`CODES` 由 `0 1 2 3 4 6` 重钉为**双侧**——「M5 基线下 `0 1 2 3 4 6`」+「M6 现态下 `0 1 2 3 4 5 6`」；`N5C` 由恰 `0` 重钉为「M6 现态下恰 `1`，且该常量名逐字为 `ExitPrecheckOrLock`」），并全跑 61 支 + 复算 V-R7-1 ~ V-R7-4 | 不改实现；不删断言 / 不加 `skip` / 不加 allowlist / 不引入 frozen-red |

**`074` 之前的退出码事实（必须写清，否则 `071` ~ `073` 的 e2e 会写出错误期望）**：`ExitCodeFor` 尚无 `5` 分支，
`071` ~ `073` 返回的类型化错误经**未分类兜底**得到退出码 **`1`**。因此这三个 task 的 e2e **只允许**断言
「**非零退出** + 诊断码含 `E15` / `E16` + 权威零写入 + 事务状态符合预期」，**既不得**断言 `5`（尚未启用），
**也不得**断言恰 `1`（那会在 `074` 落地后反向变红，等于给自己埋一个必然回归）。
退出码 `5` 的端到端断言**集中在 `074`（单命令面）与 `075`（全量面）**。

### 17.4 R7 的封闭性自证

- 新增章节**恰 1 个**（§17），`^## ` 小节数 17 → **18**；裁决行**仍恰 9 行**（A-58 只改**落地形态与反证列**，选项与结论一字不动）。
- 新增诊断码**仍恰 5**（`E15` / `E16` / `W26` / `W27` / `W28`）；退出码全集**仍恰** `{0,1,2,3,4,5,6}`；命令数**仍恰 22**；M6 e2e **仍恰 11**、磁盘总数**仍 ≥ 61**。
- 新增 Go 反证测试**恰 3 支**（全部归 `074`，全部在 `internal/cli` 包）：`TestExitPrecheckOrLockIsFive`、
  `TestExitCodeForPrecheckFailed`、`TestExitCodeForLockUnavailable`；连同 R6 的 5 支，M6 因 post-close correction 新增的 Go 测试共 **8 支**。
- 被撤销的表述**逐处在册**：§1 A-58 行反证列、§9 退出码 `5` 段、§14 消费矩阵 `074` 行、§16.5 的括注、`M-006` 判据 12、
  `T-…-074` 的 deliverable 与 Acceptance——六处已全部改为 V-R7-1 ~ V-R7-4 口径，**全文再无**「`os.Exit(5)` 由 `exitcode.go` 承载」这类表述。
- 本节**零产品代码**：`internal/**` 与 `cmd/**` 在 `T-…-070`（含 R6 / R7）内一个字节都不改；`T-…-070` 的 `done` 生命周期**不回退**。
- §17.5 另清偿一处 R6 遗留的**自相矛盾**（阶段化不跑历史全量 vs「50 支逐个全绿」），口径改为 N-1 ~ N-4 四项静态反证，全量执行唯一归 `075`。

### 17.5 「阶段化不跑历史全量」与「历史 e2e 逐支全绿」的自相矛盾消除（R7 P0-4）

**病灶**：R6 把门禁阶段化写进了 `071` ~ `074` 的「门禁阶段化」条（明确**不跑**历史 e2e 全量、现态重钉归 `075`），
但同一份 Acceptance 的「不回归」条**仍**保留 M2 ~ M5 时期的表述「M1 ~ M5 的 50 支既有 e2e 一支不删、一行不改」，
且部分行读起来等于要求**逐支执行全绿**。两条同时在册 ⇒ 实施者无论跑还是不跑都能被判违规，
且「50」这个绝对数在 M6 新增 11 支后本身也已过期。

**定稿口径（`071` ~ `074` 一律适用）**：「不回归」条**只允许**四项**静态**反证，**不得**包含任何「逐支执行 / 逐个全绿 / 全量退 0」语义：

| 编号 | 静态反证 | 判定命令（示意） |
|-|-|-|
| N-1 | 历史 e2e **数量未减** | `ls test/e2e/*.sh \| wc -l` **不低于** `T-…-070` 收口基线（基线值由 `075` 在验收报告固化，规划期不写死绝对数） |
| N-2 | 历史脚本文件名**一支不删** | 与基线清单 `diff` 为空（只增不减） |
| N-3 | 本 task 对历史脚本**零改动** | `git diff --stat test/e2e/` 对 M1 ~ M5 历史脚本**零命中**（仅允许命中本 task 新增的 `m6_*.sh`） |
| N-4 | 无放宽手段新增 | `t.Skip` / allowlist / 豁免 / frozen-red 相对基线**零新增** |

**执行面归属**：历史 e2e 的**逐支执行**与**现态重钉**（含 §16.4 的 `.index` 共存重钉、§17.2 的常量表 / 文档 `5` 行重钉）
**唯一**归 `T-…-075`；`071` ~ `074` 的执行面**恰为** targeted 单测 + `make lint` + `go test ./... -count=1` + 自身 `m6_*.sh`。
`075` 的全量支数以**实跑清单**为准（规划期锚定 `≥ 61`），**不得**再引用「50 支」这一过期绝对数。

**反证**：`071` ~ `074` 四份 Task 文本中，作为**要求**出现的「逐支全绿」/「逐个退 0」/「50 支历史 e2e 全跑」命中数**恰 0**（仅允许出现在明确标注「过期口径已作废 / 不再引用」的对照句中；`075` 除外，其为唯一全量执行点）；
且四份文本各自同时含 N-1 ~ N-4 四项与「本 task 不执行历史 e2e 逐支全绿」的逐字声明。

---

## 18. R8 定稿：只读路径不退 `5`、`E15` 原因口径与命令名纠正（`T-…-070` post-close correction 之三）

**本节地位**：R8 是 `T-…-070` **收口（`done`）之后**的第三轮 post-close correction，触发源同样是对 M5 已交付索引层与 CLI 层的**实码核对**
（`evergreen` HEAD `db64ce7`）。三条冲突全部 P0：R6 §16.3 把 reserved entry 类型违规写成**无条件** fail closed（退 `5` + `E15`），
与 M5 已交付并被历史门禁钉死的「**只读命令恒不因索引问题失败**」双向冲突；R6 / R7 的 `E15` 形态计数（「恰七形态」）与同章另列的 `seq` 溢出**自相矛盾**；
R6 的 C 类命令清单里写了**仓库不存在**的 `eg index bench`。
本节**不新增**诊断码 / 退出码 / 命令 / e2e，**不改**任何裁决行的选项与结论，**不改**一行产品代码，只把既有裁决**按调用方类别分层投影**并纠正命令名。

### 18.0 实码核对事实（登记，不是推测）

| # | 事实（逐字来源） | 对合同的约束 |
|-|-|-|
| F-R8-1 | `internal/index/corrupt.go` 注释逐字：`Inspect` **只读**、**不删库、不建库**；索引不可用**必须降级全量扫描而非报错退出**；坏索引是 **`W23` / `W24` 诊断**、**不是失败**，调用方据此产 warning 并继续、**不得据此退非 0** | R6 的「reserved 类型违规 ⇒ 无条件退 `5`」**不能**落在 `internal/index` 层，也**不能**落在只读调用方 |
| F-R8-2 | `internal/index/corrupt.go`：`CodeIndexMissing = "W23"`、`CodeIndexCorrupt = "W24"`；`Inspect(dir string) Diagnosis` **永不返回 error** | 分层后 `Inspect` 签名与「永不返 error」性质**必须保持**；结构化违规事实只能经**专用入口**回传 |
| F-R8-3 | `internal/index/corrupt.go` 的 `Reasons()` 损坏子因集合**恰 9 值**：`index_dir_missing` / `db_file_missing` / `unexpected_file` / `truncated_file` / `open_failed` / `integrity_check_failed` / `schema_incomplete` / `schema_version_mismatch` / `watermark_self_contradiction`；`corrupt_test.go` 的 `TestReasonsClosed` 逐字钉住该集合 | M6 **不得**扩张该集合（否则 `TestReasonsClosed` 变红）；reserved 类型违规**复用** `W24` 既有子因表达，**零新增子因** |
| F-R8-4 | `internal/cli/index.go`：`eg index status` **恒退 `0`**，索引旧 / 坏是 `W22` / `W23` / `W24` **诊断、不是失败**；`IndexSubcommands()` **恰 4 项**：`build` / `rebuild` / `status` / `sync` | ① 只读 `index status` 在任何 `.index/` 异常下**必须仍退 `0`**；② **不存在** `eg index bench` |
| F-R8-5 | `internal/query/backend.go`：读路径索引异常**不阻断读**，`missing` / `corrupt` / `stale` 一律**降级全量扫描**、**退出码不变**；降级**必须留痕**——恰一条 `W22` / `W23` / `W24` + **恰一条 `Q5`** | C 类读查询遇 reserved 类型违规时的**唯一**正确表现：`W24` + `Q5` + 退出码不变 |
| F-R8-6 | `internal/cli/bench.go` 的 `wireBench()` 注册**顶层**命令 `"bench"`；`internal/cli/cli_test.go` 的命令集合含顶层 `"bench"` | C 类清单与只读反证用例集**必须**写 `bench`，写 `index bench` 会让实施者按不存在的命令写测试 |

### 18.1 R8 P0-1：reserved entry 类型违规的**三层**处置（替换 R6 的无条件 fail closed）

**病灶**：R6 §16.3 写「类型错误 ⇒ fail closed（退 `5` + `E15`）」，未区分**谁在调用**。若该规则被只读路径继承，
则 `eg index status`（F-R8-4 恒退 `0`）、`search` / `card` / `rel`（F-R8-5 降级不改退出码）会在 M6 后**改变既有退出码**，
直接撞碎 A-56 的「M1 ~ M5 退出码期望零漂移」与多支历史 e2e；若该规则被写进 `internal/index`，
则 `Inspect` 必须返 error，撞碎 F-R8-1 / F-R8-2。

**定稿：同一底层事实、三层不同行为**（**唯一**形态，无备选）：

| 层 | 谁 | 行为 | 硬约束 |
|-|-|-|-|
| **L-A 事实层** | `internal/index` | `Inspect` **不变**：只读、**永不返 error**、把 reserved entry 类型违规判为 `corrupt` + **既有 `W24`**（子因复用 `unexpected_file`，**不新增**子因）；另提供**专用入口**（如 `InspectRuntimeReserved(dir) (ReservedViolation, bool)`）回传**结构化事实**（哪一项、期望类型、实际类型、是否 symlink） | **零新增对外诊断码**；`Reasons()` 仍**恰 9 值**、`TestReasonsClosed` **不扩张**；本层**不**决定退出码、**不**重建、**不**删除 |
| **L-B 写路径层** | **A 类**权威写命令（`S2` / `S3` 前置）+ **B 类** `eg index build` / `rebuild` / `sync` | 读到 L-A 的结构化违规 ⇒ 返回 `PrecheckFailedError`（`E15`，§18.4 原因 ⑧）⇒ 经 `ExitCodeFor` 映射 **退 `5`**；**零权威写入、零 DB 写入、不重建、不删除、不强抢** | 「fail closed」的**唯一**落点；理由不变：`run.lock` / `txn/` 承载互斥与恢复证据，容错等于让锁形同虚设 |
| **L-C 只读层** | **C 类**只读命令（`index status` / `bench` / `search` / `card` / `rel` / `check` / `report` 等） | 沿用 **M5 既有语义**：产**恰一条 `W24`**（`index status` 仍**恒退 `0`**）；**读查询**（`search` / `card` / `rel`）再按 F-R8-5 产**恰一条 `Q5`** 并**降级全量扫描**；**退出码一律不变** | **绝不**退 `5`、**绝不**产 `E15`、**绝不**触发重建或删除、**绝不**拿锁 |

**为什么这不是「放宽 fail closed」**：`.index/` 的 reserved entry 只对**写路径**有安全含义（互斥与恢复证据）。
只读路径**不拿锁、不做恢复、不写任何字节**，它看到 `run.lock` 是目录时，**没有任何**可被破坏的安全不变量——
此时唯一正确行为就是 M5 已定的「只报不改 + 降级」。写路径才是必须 fail closed 的那一侧，且**已经** fail closed。

### 18.2 R8 P0-2：命令名纠正 `index bench` → `bench`

`eg bench` 是**顶层**命令（F-R8-6）；`IndexSubcommands()` 恰 `build` / `rebuild` / `status` / `sync`（F-R8-4），
**不存在** `eg index bench`。R6 遗留的错误命令名已在**三处**逐字改为 `bench`，并在改动处注明「顶层命令，仓库不存在 `eg index bench`，R8 实码核对」：

| # | 位置 | 修正 |
|-|-|-|
| 1 | §16.1 C 类命令表首列 | `eg index bench` → **`eg bench`** |
| 2 | §1 裁决行 A-55 注释 + §15 自洽清单第 17 条的 C 类清单 | `index bench` → **`bench`** |
| 3 | `T-…-074` 的 `TestExit5NeverOnReadOnlyCommands` 用例集 | `index bench` → **`bench`** |

**反证**：`grep -rn 'index bench' projects/evergreen/s1_main_flow/` 命中数**恰 0**（仅允许出现在本节这类明确标注「不存在 / 已纠正」的对照句中）。

### 18.3 R8 P0-3：`T-…-072` 的 `verify.test` 必须覆盖 `internal/index`

`072` 是 §16.2 / §16.3 / §18.1 L-A 的**独家**承接方（`RemoveAll` 禁令、`AllowedFiles ∪ RuntimeReservedEntries` 判定面、专用违规入口、
`Inspect` 兼容改造），其改动**落在 `internal/index` 包内**，但 R6 / R7 的 frontmatter `verify.test` 只列
`./internal/txn ./internal/store ./internal/plan ./internal/cli`——**改哪个包就不测哪个包**，
`TestIndexInspectAllowsM6RuntimeEntries` / `TestReasonsClosed` 的回归会漏到 `075` 才暴露。

**定稿**：`T-…-072` frontmatter `verify.test` 逐字为
`cd evergreen && go test ./internal/txn ./internal/store ./internal/plan ./internal/cli ./internal/index -count=1`。

### 18.4 R8 P0-4：`E15` 原因口径——撤销精确形态计数

**病灶**：R6 §16.3 写「`E15` 第一类触发形态由**六种**增至**七种**」，而同一份合同 §9 / §12 又独立列出 `txn_id` 的 `seq` 溢出，
实际可枚举原因为 **8** 个 ⇒ 「恰七」在自身文本内即为假；更糟的是 `075` 的 `m6_final_gate.py` 曾把「形态计数为七」写成**门禁判据**，
使一处口误可以让最终验收**必然变红**。

**定稿口径（全链唯一）**：`E15` 表达的是「**写前安全复核**」这一**语义大类**，**当前枚举 8 个原因**；
清单**可增**（后续 Milestone 可追加原因而**不**新增诊断码），**精确原因数一律不作为门禁判据**；
R1 ~ R7 出现的「恰四形态 / 恰六形态 / 恰七形态 / 6 → 7」等**精确数字全部作废**。

| # | 原因 | 产出侧 | 备注 |
|-|-|-|-|
| ① | `S3` 强校验失败 | A 类写路径 | `internal/plan/precheck.go` |
| ② | `B-R3` post-crash 外部编辑冲突 | A 类写路径（`S2` 恢复面） | 前像与现盘不符 |
| ③ | 损坏 intent（不可解析 / `journal_version` 不支持） | A / B 类 | fail closed，不猜测 |
| ④ | intent 路径违规（越界 / 非相对 / 含 `..` / symlink） | A / B 类 | 恢复前置校验 |
| ⑤ | 前像不可用（`pre_bytes_ref` 缺失 / 非普通文件 / 哈希不符） | A / B 类 | 无法安全回滚 |
| ⑥ | 扫描全集异常（多 `OpenTxn` 或存在 `CorruptTxn`） | A / B 类 | 单事务不变量被破 |
| ⑦ | `txn_id` 的 `seq` 超 `uint64` 上限 | A 类写路径 | `internal/txn/id.go` |
| ⑧ | `.index/` runtime reserved entry 类型违规（`run.lock` 为目录 / `txn` 为普通文件 / 任一项为 symlink） | **仅 A / B 类写路径** | **C 类只读路径不产 `E15`、不退 `5`**，改由 `W24`（+ 读查询 `Q5`）表达（§18.1 L-C） |

**同步落点（逐处在册）**：§1 A-58 注释、§9 `E15` 产出面与退出码 `5` 段、§12 诊断码表 `E15` 行、§14 `T-…-074` DoR 行、
§16.3、§16.5、§17.1 错误层、§15 自洽清单第 19 / 25 条，以及 `M-006` 判据 12 / 17、`T-…-072`、`T-…-074`、`T-…-075`（`m6_final_gate.py` 判据）。

### 18.5 R8 的封闭性自证

- 新增章节**恰 1 个**（§18），`^## ` 小节数 18 → **19**；裁决行**仍恰 9 行**（A-55 / A-58 只改**注释与反证列**，选项与结论一字不动）。
- 新增诊断码**恰 0**（仍恰 5：`E15` / `E16` / `W26` / `W27` / `W28`）；`internal/index` 的 `Reasons()` **仍恰 9 值**、`TestReasonsClosed` **不扩张**；
  退出码全集**仍恰** `{0,1,2,3,4,5,6}`；命令数**仍恰 22**（`bench` 是既有顶层命令，**不新增**）；M6 e2e **仍恰 11**、磁盘总数**仍 ≥ 61**。
- 新增 Go 反证测试**恰 2 支**（均归 `T-…-072`，均在 `internal/index` / `internal/cli` 只读面）：
  `TestIndexStatusReservedTypeViolationReportsW24WithoutExit5`、`TestReadPathDegradesOnReservedTypeViolation`；
  连同 R6 的 5 支与 R7 的 3 支，M6 因 post-close correction 新增的 Go 测试共 **10 支**。
- 被撤销 / 纠正的表述**逐处在册**：① 「reserved 类型违规无条件退 `5`」→ 三层（§16.3 / §16.5 / §15 第 19 条 / `M-006` / `072` / `074`）；
  ② 「`E15` 恰七形态 / 6 → 7」→ 8 原因语义大类（§9 / §12 / §14 / §16.3 / §16.5 / §17.1 / `M-006` 判据 12 / `074` / `075`）；
  ③ `index bench` → `bench`（§16.1 / A-55 / §15 第 17 条 / `074` 用例集 / `M-006` C 类表）。
- 本节**零产品代码**：`internal/**` 与 `cmd/**` 在 `T-…-070`（含 R6 / R7 / R8）内一个字节都不改；
  `T-…-070` 的 `done` 生命周期**不回退**；`T-…-071` 保持 `created`、**不启动**。

---

## 19. R9 定稿：两条删除策略分离与只读断言精确化（`T-…-070` post-close correction 之四）

**本节地位**：R9 是 `T-…-070` **收口（`done`）之后**的第四轮 post-close correction，触发源同样是对 M5 已交付索引层的**实码核对**
（`evergreen` HEAD `db64ce7`）。两条冲突全部 P0：① R6-D2 把 `rebuild` 的删除面收窄为「只删 DB 家族 + 自建临时产物，其余一律不动」，
这会把**外部混入的污染文件留在盘上**，使 `rebuild` 返回后 `Inspect` **仍判 `corrupt`**——`rebuild` 由此失去「把索引修回 `healthy`」的语义，
并与**已存在的实码用例** `internal/index/rebuild_test.go::TestRebuildRemovesPollution` **直接冲突**（该用例构造 `leftover.tmp`，
断言 `Rebuild` 后污染**不存在**且 `Inspect` **healthy**）；② R8 §18.1 L-C 行沿用了 `internal/query/backend.go` 的**通用**降级留痕口径
「恰一条 `W22` / `W23` / `W24` + 恰一条 `Q5`」，但 reserved entry 类型违规的**原因已被合同自己确定为 `corrupt`**，
写成三者之一会让实施者写出**通不过也测不准**的宽泛断言。
本节**不新增**诊断码 / 退出码 / 命令 / e2e / 测试名，**不改**任何裁决行的选项与结论，**不改**一行产品代码，只把删除面拆成两条互不复用的策略并把只读断言收紧。

### 19.0 实码核对事实（登记，不是推测）

| # | 事实（逐字来源） | 对合同的约束 |
|-|-|-|
| F-R9-1 | `internal/index/rebuild_test.go::TestRebuildRemovesPollution`：先建 `leftover.tmp` 并断言 `Inspect` 为 `ReasonUnexpectedFile`，执行 `index.Rebuild` 后断言 **`leftover.tmp` 不存在** 且 `Inspect` **healthy** | `rebuild` **必须删除外部污染**；R6-D2 的「其余一律不动」**为假**，必须撤销 |
| F-R9-2 | `internal/index/rebuild.go` 注释逐字：重建语义是「**丢弃旧目录**」（含 `eg.db` / `-wal` / `-shm` 与**任何外部混入污染文件**），**不是**「就地覆盖 `eg.db`」 | M6 只能把「删整个目录」降级为「**逐项删除非 runtime-reserved 条目**」，**不能**降级为「只删 DB 家族」 |
| F-R9-3 | `internal/index/build.go`：`Build` 在 `eg.db` 已存在时返回 `ErrIndexExists`；构建失败当前执行 `_ = os.RemoveAll(dir)` | 失败清理必须被收窄为**只删本次 attempt 产物**；它与 `rebuild` 的「全清扫」是**两种相反语义**，不可共用实现 |
| F-R9-4 | `internal/index/scratch.go`：`NewScratch` 创建**一次性 scratch 目录**，其 cleanup 对**该 scratch 目录**执行 `os.RemoveAll` | 该 `RemoveAll` **不在** R6-D1 禁令面内，也**不得**被当作 `.index/` 根删除策略的先例 |
| F-R9-5 | `internal/index/schema.go`：`AllowedFiles()` **仍恰 3 项** `{eg.db, eg.db-wal, eg.db-shm}` | 「非 runtime-reserved」的判定基准是 `RuntimeReservedEntries()`（恰 2 项），**不是** `AllowedFiles()`；污染不在任何一张表里，因此在 rebuild 面上**必删** |
| F-R9-6 | `internal/query/backend.go` 的降级留痕是**通用**口径（`missing` / `corrupt` / `stale` 各对应 `W23` / `W24` / `W22`），并非「三者任一皆可」 | reserved 类型违规既已判 `corrupt`，其读查询留痕**只能**是 `W24` + `Q5` |

### 19.1 R9 P0-1 定稿：Rebuild = 清除全部非 runtime-reserved 条目 + 全量 Build

**唯一形态（无备选）**，`eg index rebuild`（B 类）的执行序：

| 步 | 动作 | 硬约束 |
|-|-|-|
| ① | `S1` 取 `vault/.index/run.lock`（`flock` 非阻塞） | 忙 ⇒ 退 `5` + `E16`、`.index/` **零字节写入**、**不删任何条目**、绝不强抢（R6-D5） |
| ② | `S2` 恢复屏障 | 未恢复的半应用态上**不得**重建（§16.1 规则 1） |
| ③ | **reserved 类型检查** | `run.lock` 非普通文件 / `txn` 非目录 / 任一项为 symlink ⇒ **fail closed**：`E15` + 退 `5`、**一个条目都不删**（§16.3 + §18.1 L-B） |
| ④ | **全清扫**：`ReadDir(.index)` 后**逐项**删除**所有不在 `RuntimeReservedEntries()` 内**的条目 | 删除面**显式包含**：DB 家族三项、**外部污染文件**（如 `leftover.tmp`）、**陈留临时产物**、**意外子目录**（对意外子目录用递归删除该子目录本身，**不是** `.index/`）。**禁止** `os.RemoveAll(<indexDir>)`（R6-D1 不变） |
| ⑤ | **保留面**：`run.lock`（**同一 inode**）、`txn/` **全树**（`intent.json` / `pre/` / `quarantine/` / `commit` / `abort` / `seq`，含未闭合与损坏事务目录） | 断言口径：维护前后 `stat(run.lock).st_ino` **逐字相等**、`txn/` 子树**字节逐字相等**（R6-D4 不变） |
| ⑥ | 走 `Build` 全量路径 | 因 ④ 已删 `eg.db`，`Build` **不会**撞 `ErrIndexExists`（F-R9-3） |
| ⑦ | `S8` 释放锁 | —— |

**语义命名（供实施对齐）**：该策略是**目录级清扫**，建议单点实现为 `purgeNonReserved(dir) error`；
它**只**被 `Rebuild` 调用，**禁止**被 `Build` 的失败路径复用（§19.3）。

**为什么必须删污染**：`Inspect` 把 `AllowedFiles() ∪ RuntimeReservedEntries()` 之外的条目判 `unexpected_file` ⇒ `corrupt`（F-R6-3 / F-R9-5）。
若 `rebuild` 保留污染，则「`rebuild` 后仍 `corrupt`」⇒ `EnsureBuilt` 每次都再 `rebuild`（放大回路）、
`m5_index_corrupt_rebuild.sh` 与 `TestRebuildRemovesPollution` 一并变红。**`rebuild` 的对外承诺是「返回后索引 `healthy`」，这一 M5 事实 M6 不得削弱。**

### 19.2 R9 P0-2 定稿：Build 失败清理 = 只删本次 attempt 产物

**唯一形态（无备选）**，`Build`（含 `eg index build` 与 `EnsureBuilt` 内部调用）失败时：

| 项 | 定稿 |
|-|-|
| **删除面** | **只**删除**本次 build attempt 亲手创建**的产物：本次新建的 `eg.db` / `eg.db-wal` / `eg.db-shm`，以及本次创建的临时产物（含 `scratch.go` 的一次性 scratch 目录）。实现上以「本次调用内记录的已创建路径集合」为唯一依据，**不得**以「目录内现存条目」为依据 |
| **保留面（必须）** | ① **调用前既已存在的污染**（如先前遗留的 `leftover.tmp`）——它不是本次产物，删掉即为**越权清理**，会掩盖故障现场；② **runtime-reserved**：`run.lock`（含 inode）与 `txn/` 全树；③ **`.index/` 目录本身**：默认保留，但**R10 §20.3 追加唯一例外**——若且仅若本次 Build 创建了 `indexDir` 且清理后为空，可 `os.Remove(indexDir)` 删除本次创建的空目录；B 类路径（已持有 `run.lock`）绝不满足该例外 |
| **禁止** | `os.RemoveAll(<indexDir>)`；复用 §19.1 的 `purgeNonReserved`；以「删干净好重来」为理由扩大删除面 |
| **与 `ErrIndexExists` 的关系** | `Build` 在 `eg.db` 已存在时**直接返回** `ErrIndexExists`（F-R9-3），此路径**未创建任何产物** ⇒ **零删除** |

### 19.3 两条策略的**不可复用**约束（本节的结构性判据）

| 判据 | 内容 |
|-|-|
| **N-1** | `Rebuild` 的清扫与 `Build` 的失败清理**必须**是**两个不同函数**，且**不得**互相调用、**不得**由同一函数靠 bool / flag 参数切换语义 |
| **N-2** | 两函数的 doc comment 必须**各自逐字**写明删除面与保留面，并**互相指名**说明差异（「本函数**不**删调用前既有污染，与 `purgeNonReserved` 相反」/ 反之） |
| **N-3** | 反证方向相反且**同时**为真：rebuild 面断言污染**被删**；build 失败面断言（调用前的）污染**被保留**。若两者被同一实现承载，**必有一支变红** |
| **N-4** | R6-D1 的 `RemoveAll` 禁令在两条策略下**都不放松**：`grep -rnE 'os\.RemoveAll\(' internal/index internal/cli/index*.go` 的每处命中只能指向 DB 家族成员、**明确的意外子目录**或自建 scratch 目录，**不得**指向 `.index/` 本身 |

### 19.4 反证增强（**不新增测试名**，只增强既有 R6 两支，均归 `T-…-072`）

| 测试 | R6 原断言（保留） | R9 新增断言 |
|-|-|-|
| `TestIndexRebuildPreservesTxnAndLockInode` | 维护前后 `run.lock` 的 `st_ino` 逐字相等；`txn/` 子树字节逐字相等 | **同时**在 `.index/` 构造 `leftover.tmp`（外部污染），`Rebuild` 成功后断言：① `leftover.tmp` **不存在**；② `Inspect` 回到 **healthy**；③ ①② 与 inode / `txn/` 保留断言**同时**成立 |
| `TestIndexBuildFailurePreservesTxnAndLock` | 失败后 `run.lock` inode 不变、`txn/` 全树字节不变 | **调用 `Build` 之前**先构造污染（如 `stale.tmp`），失败返回后断言：① 该**调用前污染仍存在**；② **本次 attempt 创建**的 DB / 临时产物**已被删除**；③ `.index/` 目录本身**仍存在**（**该第 ③ 条只适用于「调用前目录已存在」的场景**；R10 §20.4 把本支扩为 subcase A / B：**A = 预存目录 ⇒ 目录必留**、**B = 本次创建的空目录 ⇒ 失败后必须已被 `os.Remove` 删除**） |
| `TestRebuildRemovesPollution`（**历史实码，一字不改**） | 构造 `leftover.tmp` ⇒ `Rebuild` ⇒ 污染消失 + `Inspect` healthy | **必须继续绿**；它是 §19.1 的**外部**独立反证，M6 改造**不得**以任何方式修改、跳过或放宽它 |

**归属与测试面**：三支均落在 `internal/index` 包 ⇒ `T-…-072` 的 `verify.test` 含 `./internal/index`（R8 §18.3）已足够覆盖，**无需**再改 frontmatter。

### 19.5 R9 P0-3 定稿：只读断言精确化为「恰一条 `W24` + 恰一条 `Q5`」

| 面 | 定稿断言 | 说明 |
|-|-|-|
| `eg index status`（C 类） | 产**恰一条 `W24`**，**恒退 `0`** | reserved 类型违规的原因已确定为 `corrupt`（§16.3），**不是** `missing`（`W23`）也**不是** `stale`（`W22`） |
| 读查询 `search` / `card` / `rel`（C 类） | 产**恰一条 `W24`** **且** **恰一条 `Q5`**，降级全量扫描，**退出码不变** | 「恰一条」= 数量断言（不得重复告警），`W24` = 唯一码（不得写成 `W22` / `W23` / `W24` 三者之一） |
| 反证 | `TestIndexStatusReservedTypeViolationReportsW24WithoutExit5` / `TestReadPathDegradesOnReservedTypeViolation`（R8 新增，归 `T-…-072`，**测试名不变**） | 断言由「宽泛三码任一」收紧为「恰一条 `W24` + 恰一条 `Q5`」 |

`internal/query/backend.go` 的**通用**三码留痕口径（F-R9-6）**不被推翻**：`missing` ⇒ `W23`、`stale` ⇒ `W22` 依旧成立；
R9 只是明确**在 reserved 类型违规这一具体输入下**，落到的分支**只能**是 `corrupt` ⇒ `W24`。
同步落点：§16.1 规则 4、§18.1 L-C 行、§15 自洽清单第 26 条、`M-006` 判据、`T-…-072`、`T-…-075`。

### 19.6 R9 的封闭性自证

- 新增章节**恰 1 个**（§19），`^## ` 小节数 19 → **20**；裁决行**仍恰 9 行**（A-53 / A-55 只改**注释与反证列**，选项与结论一字不动）。
- 新增诊断码**恰 0**（仍恰 5：`E15` / `E16` / `W26` / `W27` / `W28`）；`internal/index` 的 `Reasons()` **仍恰 9 值**、`TestReasonsClosed` **不扩张**；
  退出码全集**仍恰** `{0,1,2,3,4,5,6}`；命令数**仍恰 22**；M6 e2e **仍恰 11**、磁盘总数**仍 ≥ 61**。
- 新增 Go 测试名**恰 0**：R9 只**增强** R6 既有两支（§19.4），M6 因 post-close correction 新增的 Go 测试**仍共 10 支**（R6 5 支 + R7 3 支 + R8 2 支）。
- 被撤销 / 收紧的表述**逐处在册**：① R6-D2「只删 DB 家族 + 自建临时产物，其余一律不动」⇒ **全清扫非 runtime-reserved**（§16.2 R6-D2 行 / §16.1 B 类写入面 / §15 第 18 条 / `M-006` / `072` / `075`）；
  ② R6-D3 失败清理**收窄**为「只删本次 attempt 产物」（并在 R10 §20.3 下允许“本次创建且清理后为空的 indexDir 用 os.Remove 删除空目录”的唯一例外）并与 ① **禁止共用实现**（§16.2 R6-D3 行 / §19.2 / §19.3 / §20.3 / `072` / `075`）；
  ③ R8 只读降级断言由「`W22` / `W23` / `W24` + `Q5`」⇒ 「**恰一条 `W24` + 恰一条 `Q5`**」（§16.1 规则 4 / §18.1 L-C / `M-006` / `072` / `075`）。
- 本节**零产品代码**：`internal/**` 与 `cmd/**` 在 `T-…-070`（含 R6 / R7 / R8 / R9）内一个字节都不改；
  `T-…-070` 的 `done` 生命周期**不回退**；`T-…-071` 保持 `created`、**不启动**。

---

## 20. R10 定稿：Build 失败清理的“空目录可删”例外（覆盖 R9 §19.2 的目录口径）

**本节地位**：R10 是 `T-…-070` **收口（`done`）之后**的第五轮 post-close correction，严重级别 P0，原因是 R9 §19.2 中「`.index/` 目录本身一律不删」
与 **M5 既有实码单测** `internal/index/build_test.go::TestBuildRejectsUnknownSkippedKind` 的逐字断言冲突：该用例在**初始 `.index/` 不存在**时触发 `Build` 失败，
并逐字断言失败后 `.index/` 目录**不存在**。`T-075` 明确禁止改/跳过历史 Go 断言，故本节在不放宽 R9 的“越权清理禁令”前提下引入**唯一且封闭**的例外：
若且仅若 `indexDir` 是本次 `Build` 亲手创建、且失败清理后目录为空，则允许 `os.Remove(indexDir)` 删除**本次创建的空目录**；
同时明确 B 类 CLI（`eg index build` / `rebuild` / `sync`）已先创建并锁住 `.index/run.lock`，其 `indexDir` **必然预先存在**，因此在 B 类路径下**绝不允许**删除目录或替换 `run.lock` inode。

### 20.0 实码核对事实（登记，不是推测）

| # | 事实（逐字来源） | 对合同的约束 |
|-|-|-|
| F-R10-1 | `internal/index/build_test.go::TestBuildRejectsUnknownSkippedKind`（237 ~ 248 行）逐字：`dir := filepath.Join(t.TempDir(), index.DirName)`（**调用前 `.index/` 不存在**）⇒ 非法 `skipped.kind` 令 `index.Build` 返回 error ⇒ 注释逐字「失败不留半成品：`.index/` 必须已被清掉，下一次 build 从干净状态开始。」⇒ 断言 `os.Stat(dir)` 的 error 满足 `errors.Is(err, os.ErrNotExist)` | R9 §19.2 的「`.index/` 目录本身**一律**保留」**为假**：`Build` 失败清理**必须**允许删除「本次创建的空 `.index/` 目录」，否则该历史 Go 用例必红，而 `T-…-075` 禁止改 / 跳过历史 Go 断言 |
| F-R10-2 | `internal/index/build.go`：`Build` 内 `MkdirAll(dir)` 会在目录缺失时**亲手创建** `indexDir`；`eg.db` 已存在则先返回 `ErrIndexExists`（F-R9-3） | 「目录是否为本次创建」是**可判定**的：进入时做一次 `stat` 快照即可，无需依赖目录内容推断 |
| F-R10-3 | R6 / R9 已定：B 类 index 维护命令（`eg index build` / `rebuild` / `sync`）在进入构建前**必须已取** `vault/.index/run.lock` 并先过 `S2`，且 `run.lock` 须保持**同一 inode**（§16.1 规则 1 / §16.2 R6-D4 / §19.1 步 ①⑤） | B 类路径下 `indexDir` **必然预先存在**且至少含 `run.lock` ⇒ 「本次创建」与「清理后为空」**同时**不成立 ⇒ B 类**绝不允许**删目录、**绝不允许**替换 `run.lock` inode；`Rebuild` 路径同理 |
| F-R10-4 | `internal/index/build_test.go::TestBuildOnlyAllowedFiles` 等历史用例走的是**成功**路径（`buildFixture`），不涉及失败清理 | 本例外只作用于**失败路径**，成功路径语义一字不动 |

### 20.1 R10 P0-1 定稿：Build 进入时的“存在性快照 + 本次创建集合”

进入 `Build(indexDir)` 时必须做两件事（均为**本次调用内**私有变量，不落盘、不跨调用复用）：

1) 记录 `indexDirExistedBefore`（布尔）：进入时 `stat(indexDir)` 是否存在。
2) 记录 `createdPaths[]`（集合）：本次调用内**亲手创建**的路径集合（DB/WAL/SHM 与临时产物，以及若本次调用创建了 `indexDir` 也把 `indexDir` 本身记入该集合）。

这两项是 §20.2 / §20.3 的唯一依据：失败清理的删除面必须严格受 `createdPaths` 约束，禁止“看到目录里有什么就顺手删什么”。

### 20.2 R10 P0-2 定稿：失败清理第一阶段（只删本次 attempt 产物）

失败时先按 R9 §19.2 的“越权清理禁令”执行第一阶段清理：

- **只删除** `createdPaths` 中属于 DB 家族与本次临时产物的条目（含 `scratch.go` 的一次性 scratch 目录）。
- **永远保留**：调用前既有污染、runtime-reserved（`run.lock` 含 inode + `txn/` 全树）。
- **禁止**：`os.RemoveAll(indexDir)`；复用 `purgeNonReserved`；以“删干净好重来”为理由扩大删除面。

### 20.3 R10 P0-3 定稿：失败清理第二阶段（仅本次创建且清理后为空时可删目录）

在 §20.2 清理完成后，允许且仅允许以下**唯一**条件分支：

- 若 `indexDirExistedBefore == false`（即 `indexDir` 是本次 `Build` 调用创建），并且清理后 `ReadDir(indexDir)` 结果为空：
  - 允许执行 `os.Remove(indexDir)` 删除**本次创建的空目录**。
- 其余所有情形（目录调用前已存在；或清理后因并发/外部写入而非空；或目录内仍有 runtime-reserved；或 `ReadDir` 失败）：
  - **必须保留** `indexDir`，不得删除目录。

**特别强调（B 类 CLI 的硬约束）**：B 类命令在进入 Build 之前已创建并锁住 `.index/run.lock`，因此 `indexDirExistedBefore` 必为 true，
并且清理后目录不可能为空（至少包含 `run.lock`）。所以 B 类路径下**绝不允许**删目录或替换 `run.lock` inode；
Rebuild 路径同理（§19.1 仍要求保留 `run.lock` 同一 inode 与 `txn/` 全树）。

### 20.4 历史反证与新增/复用反证（归 `T-…-072`，允许零新增测试名）

| 反证 | 约束 |
|-|-|
| `TestBuildRejectsUnknownSkippedKind`（历史实码，一字不改） | 必须继续绿；它证明“本次创建的空 `.index/` 目录在失败后可被删除”的例外是**必要且正确**的 |
| `TestIndexBuildFailurePreservesTxnAndLock`（增强） | 增强覆盖“预存目录 + 调用前污染 + runtime-reserved 必留”：目录调用前已存在 ⇒ 无论失败清理如何，`.index/` 目录必须保留；调用前污染必须保留；`run.lock` inode 与 `txn/` 全树字节逐字不变；本次 attempt 产物必须被删 |
| `TestIndexBuildFailurePreservesTxnAndLock`（同名扩充 subcase，避免新增测试名） | 在同一测试内追加一个 subcase 覆盖“本次创建的空目录失败后删除”：构造 `indexDirExistedBefore=false` 且失败发生在创建 DB/临时产物之前或全部被清理干净后，断言失败返回后 `.index/` 目录不存在（与 `TestBuildRejectsUnknownSkippedKind` 的断言方向一致） |

### 20.5 R10 的封闭性自证

- 新增章节**恰 1 个**（§20），`^## ` 小节数 20 → **21**；裁决行**仍恰 9 行**。
- 语义更正为“**目录不一刀切保留**”：R9 §19.2 的第三条“目录本身一律保留”在本节下被**覆盖为条件化保留**；
  但 R9 的“越权清理禁令”不放松——**仍然**只删本次 attempt 创建的产物、调用前污染与 runtime-reserved 永远保留。
- 新增 Go 测试名**恰 0**（**定稿**为扩充既有 `TestIndexBuildFailurePreservesTxnAndLock` 的 subcase，而**不**新增测试名）；
  M6 因 post-close correction 新增的 Go 测试**仍共 10 支**（R6 5 + R7 3 + R8 2 + R9 0 + R10 0）；
  若后续实施期确需新增测试名，**必须**同步更新 §15 自洽清单第 27 条 ⑥、`M-006` R10 小节与 `T-…-075` 的计数复算处（三处同改，缺一即视为口径漂移）。
- 新增诊断码**恰 0**（仍恰 5）；退出码全集**仍恰** `{0,1,2,3,4,5,6}`；命令数**仍恰 22**；M6 e2e **仍恰 11**、磁盘总数**仍 ≥ 61**；
  `AllowedFiles()` 仍恰 3 项、`RuntimeReservedEntries()` 仍恰 2 项、`Reasons()` 仍恰 9 值。
- **N-1 ~ N-4（§19.3）全部不变**：`os.Remove(indexDir)`（删空目录、非递归）与 `os.RemoveAll(indexDir)`（递归删全树）是**两个不同调用**，
  R6-D1 / N-4 禁的是后者；本例外**不得**被实现成 `RemoveAll`，静态反证 `grep -rnE 'os\.RemoveAll\(' internal/index internal/cli/index*.go` 中指向 `indexDir` 者**仍恰 0**。
- 本节**零产品代码**：`internal/**` 与 `cmd/**` 在 `T-…-070`（含 R6 / R7 / R8 / R9 / R10）内一个字节都不改；
  `T-…-070` 的 `done` 生命周期**不回退**；`T-…-071` 保持 `created`、**不启动**。
