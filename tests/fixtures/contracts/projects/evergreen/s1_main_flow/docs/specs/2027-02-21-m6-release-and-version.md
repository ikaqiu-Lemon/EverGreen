# M6 版本号与发布口径决策（`eg`）

- 文档 ID：`docs/specs/2027-02-21-m6-release-and-version.md`
- 所属：Epic `evergreen / s1_main_flow` · 阶段 **S5 · 里程碑 M-006（M6）** · Task **T-evergreen.s1_main_flow-158614-075**（M6 文档 / SKILL / 版本 / dist 产物与最终统一验收）
- 状态：**决策已定**（版本号取值、四处同真位置、tag 命名与创建命令）+ **未知 / 待确认**（远端、是否真的打 tag / 推送）
- 关联风险：**R-12**（darwin 产物仅交叉编译、无真机验证，**继续登记、不得声称已解除**）、**R-13 / R-20**（`git add -A`、非强原子 —— M6 已用**强原子事务 + `run.lock` + 崩溃恢复**收口，见 §5 与 M6 合同 §2/§3/§4/§5，本行由「挂起」改判为「已清偿」）
- 消费方：`evergreen/INSTALL.md`、`evergreen/README.md`、`evergreen/skill/SKILL.md`、`github.com/ikaqiu-Lemon/EverGreen/internal/cli/docs_test.go`（`docsReleaseSpec` 指针自 M6 起指向本文件）、`evergreen/test/e2e/m5_docs_commands.sh`（当期版本消费面）、`evergreen/test/e2e/m3_docs_commands.sh`（版本历史/现态双侧锁）、`evergreen/test/e2e/m6_docs_commands.sh`、T-…-075 的 M6 验收报告
- 前序（只读、不改写）：`2026-10-03-m2-release-and-version.md` / `2026-11-08-m3-release-and-version.md` / `2026-12-11-m4-release-and-version.md` / `2027-01-17-m5-release-and-version.md` 分别是 M2 / M3 / M4 / M5 的唯一决策出处，其结论**逐字保留**；自 M6 起「当前版本号」的唯一出处是**本文件**。

本文件是 M6 版本号与发布口径的**唯一**决策出处。凡本文件未给出证据的结论，一律写「未知 / 待确认」，**不做任何假设**。

> **指针承接说明（延续 M4/M5 的 K-062-01 体例）**：`docs_test.go` 的 `docsReleaseSpec` 在 M5 期指向 `2027-01-17-m5-release-and-version.md`，该文档逐字声明的是 `0.5.0-m5`。M6 把版本号推进到 `0.6.0-m6` 后，`TestDocsVersionThreeSourcesConsistent` 的**原判据一字不改**（决策文档必须逐字含当期版本号、必须对推送 / tag 标「未知」），只是当期出处换成本文件 —— M2/M3/M4/M5 四份文档仍在盘、事实一字未改，属**阶段化更新**而非放宽。

---

## 1. 版本号规则

### 1.1 M6 版本取值

**`0.6.0-m6`**

| 位 | 取值 | 含义 |
|-|-|-|
| major | `0` | S1–S5 尚未整体收口成对外正式版；`1.0.0` 何时发属组织决策，本阶段不预设 |
| minor | `6` | 与里程碑序号对齐：M1 = `0.1.x`、M2 = `0.2.x`、M3 = `0.3.x`、M4 = `0.4.x`、M5 = `0.5.x`、**M6 = `0.6.x`** |
| patch | `0` | 该 minor 下的首个产物 |
| 预发布后缀 | `-m6` | 明示这是**里程碑试用版**，不是对外正式发布版（收口前一律带里程碑后缀） |

形态约束（未变）：`^[0-9]+\.[0-9]+\.[0-9]+(-[a-z0-9.]+)?$`；`-m6` 属预发布段，语义化排序高于 `0.5.0-m5`、低于 `0.6.0`。

**为什么是 minor 而不是 patch**：M6 **不新增任何顶层命令**（注册表总量恒 **22**），但新增了强原子事务、`run.lock` 短临界区锁、事务日志与崩溃恢复、块级安全合并、写前强校验与**退出码 `5` 启用**——这是**面向可靠性的能力新增**（error 面从 `{0,1,2,3,4,6}` 扩为 `{0,1,2,3,4,5,6}`），且**改变了失败语义**（从「尽力而为 + 事后可查」升级为「要么全成、要么全不成」）。`0.y.z` 口径下这类可观察能力扩张升 minor。既有命令在**正常路径**的默认输出语义与退出码未做破坏性变更；`5` 只在**写前强校验失败 / 锁不可用**这两类新增失败面出现。

**一处需显式登记的默认值变化（非破坏性，但可观察）**：所有 A 类写命令（`apply` / `edit` / `delete` / `undelete` / `mark-reviewed` / `reconcile` 的写路径与 `proposal approve`）自 M6 起在写前经过**强校验前置**（`--strict` 升级面 + 恢复屏障），并在整个「校验 + 落盘 + 提交」临界区持 `run.lock`；复核失败或锁不可用 → 退 `5` + `E15`/`E16`、**本次请求事务零权威写入**。这是 M6 · T-074（A-56 / A-58）落地的可靠性前置，不是参数语义反转。

### 1.2 四处同真

版本号在仓内**只有四处**出现，必须逐字相等（M5 为「三处同源 + 文档消费面」，M6 收口时按门禁实测把「决策文档 + 两源」与「两份文档消费面」统一登记为**四处同真**，加严非放宽）：

| # | 位置 | 形态 | 生效场景 |
|-|-|-|-|
| ① | `evergreen/Makefile` 的 `VERSION ?= 0.6.0-m6` | Make 变量缺省值 | `make build` 经 `-ldflags` 注入；`make print-version` 直接打印它 |
| ② | `github.com/ikaqiu-Lemon/EverGreen/internal/version/version.go` 的 `const DefaultVersion = "0.6.0-m6"`（`var Version = DefaultVersion`） | Go 常量 | 不经 Makefile 的 `go build` / `go run` / `go test` |
| ③ | 本文件 §1.1 声明的 `0.6.0-m6` | 文档 | 决策依据（`docsReleaseSpec` 当期指针） |
| ④ | `evergreen/README.md` 与 `evergreen/INSTALL.md` 的版本消费面（两文件同一取值） | 文档消费面 | 面向使用者的当期版本声明；由 `TestDocsVersionThreeSourcesConsistent` 与 `m5_docs_commands.sh` 逐字比对 |

- 注入机制**不变**：`-ldflags '-s -w -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Version=$(VERSION) -X …Commit=$(COMMIT) -X …Date=$(DATE)'`。
- 覆盖方式**不变**：`make build VERSION=0.6.1-m6`（`?=` 语义）。
- 旧版本字面量口径（本轮明确写死，供门禁消费）：`Makefile` 内**不留任何**历史版本字面量；`internal/version/version.go` 里 `0.5.0-m5` 只允许出现在**里程碑沿革注释**中，**不得出现在任何赋值行**；`0.4.0-m4` / `0.3.0-m3` / `0.2.0-m2` 在两个文件里**零残留**（M5 期结论原样延续，不放宽）。`0.0.0-dev` 的唯一残留位置仍是 `ScaffoldVersion`，**只作反证基线**。
- 一致性守护（可机器执行）：
  - `go test ./internal/version -run 'TestVersionNotScaffold|TestVersionMatchesMakefile'`；
  - `go test ./internal/cli -run TestDocs`：Makefile / `version.go` / **本文件** / `README.md` / `INSTALL.md` 逐字比对；
  - `bash test/e2e/m5_docs_commands.sh`：`eg --version` == `make print-version` == `version.go` 字面量 == 当期版本 `0.6.0-m6`，且 M5 增量面（22 命令 / `index` 四子命令 / `bench` 五键 / 降级留痕 / 分页）与文档同真；
  - `bash test/e2e/m3_docs_commands.sh`：版本号「历史事实 + 现态」双侧锁——历史里程碑值（`0.2.0-m2` / `0.3.0-m3` / `0.4.0-m4` / `0.5.0-m5`）**不得作为生效值残留**，当期生效值逐字 `0.6.0-m6`；
  - `bash test/e2e/m6_docs_commands.sh`：M6 文档面（退出码 `5` 启用行含 `E15`/`E16`、诊断码新增 5 条、三项已知限制在册、`SKILL.md` 内嵌与源逐字相等）与实现同真。

### 1.3 M5 → M6 版本沿革（历史事实，逐字保留）

| 里程碑 | 版本号 | 唯一决策出处 | 当前状态 |
|-|-|-|-|
| M2 | `0.2.0-m2` | `2026-10-03-m2-release-and-version.md` | 只读历史，事实一字未改 |
| M3 | `0.3.0-m3` | `2026-11-08-m3-release-and-version.md` | 只读历史，事实一字未改 |
| M4 | `0.4.0-m4` | `2026-12-11-m4-release-and-version.md` | 只读历史，事实一字未改 |
| M5 | `0.5.0-m5` | `2027-01-17-m5-release-and-version.md` | 只读历史，事实一字未改 |
| **M6** | **`0.6.0-m6`** | **本文件** | **当期唯一生效值** |

---

## 2. tag 命名与创建命令

| 项 | 结论 |
|-|-|
| 命名规则 | `v<版本号>`，即 **`v0.6.0-m6`**（与 §1.1 一一对应，前缀 `v` 固定，与 M2/M3/M4/M5 同规则） |
| tag 类型 | 附注 tag（annotated） |
| 打在哪 | `evergreen/` 仓 `master` 分支的 M6 收口 commit |
| 本轮是否已创建 | **未创建**（见 §3：属组织决策，本地无依据） |

逐字可执行的创建命令（CWD = `evergreen/`）：

```console
$ git tag -a v0.6.0-m6 -m "eg 0.6.0-m6：S5 里程碑 M6 试用版（22 命令，强原子事务 / run.lock / 崩溃恢复 / 块级安全合并 / 写前强校验 / 退出码 5 启用）"
$ git tag -n1 v0.6.0-m6   # 校验：应打印 tag 名与说明首行
```

删除误打的本地 tag：

```console
$ git tag -d v0.6.0-m6
```

> 上述命令块用 `console` 而非 `bash` 标注：文档命令抽取器只跑 ```bash 块，**本轮不执行**任何 tag 命令（§3 结论为「未知 / 待确认」）。

---

## 3. 远端状态（以实测输出为依据）

在 `evergreen/` 内实测（2027-02-21，沙箱本机）：

```console
$ git remote -v
$ echo "exit=$?"
exit=0
```

→ `git remote -v` **无任何输出**（退 0），本仓**未配置任何远端**。

```console
$ git tag
$ echo "exit=$?"
exit=0
```

→ `git tag` **无任何输出**（退 0），本仓**当前不存在任何 tag**。

### 结论

| 问题 | 结论 | 依据 |
|-|-|-|
| 是否存在远端仓库？ | **否**（本地视角） | `git remote -v` 空输出 |
| 是否已有 tag？ | **否** | `git tag` 空输出 |
| 是否**应该**打 `v0.6.0-m6`？ | **未知 / 待确认** | 属组织决策；本地无依据 |
| 要不要推送、推到哪个远端？ | **未知 / 待确认** | 本地无远端信息，**不得假设存在远端**；本轮不新建远端、不推送任何 tag / 分支 |
| 本轮实际动作 | **只给命令、不执行** | 本文件 §2 与 T-…-075 活动记录 |

> 口径说明（延续 M2/M3/M4/M5）：`README.md` 与 `INSTALL.md` 中**不出现**任何推送类命令。

---

## 4. 四平台产物验证矩阵（M6）

状态口径按阶段如实登记，**不提前声称**。下表在 M6 收口 HEAD 上真实 `make dist` 后由 C2b「dist / provenance」批次逐字回填；构建命令 `cd evergreen && make dist`（`rc=0`），四平台均 `CGO_ENABLED=0 go build -trimpath`（见 `Makefile`）。

- **构建 commit（provenance 锚点）**：`b535b54`
- **构建时刻（UTC）**：`2026-09-11T11:28:43Z`
- **`./dist/eg_linux_amd64 --version`**：`eg 0.6.0-m6 (commit b535b54, built 2026-09-11T11:28:43Z, go1.24.13 X:cacheprog,testenhance)`
- **产物计数**：`eg_*` 恰 4 个可执行文件 + `SHA256SUMS` 恰 1 个。
- **说明**：`make dist` 的 `-ldflags` 注入 `COMMIT = git rev-parse --short HEAD` 与 `DATE`（构建时刻）。因 `DATE` 逐次注入，**同一份源码不同时刻构建的 SHA256 不同**；溯源锚点是 `eg --version` 的 `commit` 段（本次为 `b535b54`，即 C2b-D e2e 收口 commit），而非哈希本身。`dist/` 不入 Git（构建产物，非仓库内容），故本表是该 commit 上一次真实构建的**快照**。C2b-D 相较 C2b-A/B 的 `0ef2bc6` 仅新增 / 重钉 e2e 与文档、测试文件（不进编译产物），故四平台字节数逐字不变、仅 `commit` / `DATE` 注入变化导致 SHA256 更新。

| 平台 | 产物 | 字节数 | `sha256sum -c` | 运行验证 | `file(1)` | 本轮状态 |
|-|-|-|-|-|-|-|
| `linux/amd64` | `dist/eg_linux_amd64` | `9953464` | `OK` | `./dist/eg_linux_amd64 --version` 含 `0.6.0-m6`、`commit` 段 == `b535b54` | `ELF 64-bit LSB executable, x86-64, statically linked, stripped` | 已重建 + 本机运行验证 |
| `linux/arm64` | `dist/eg_linux_arm64` | `9437368` | `OK` | 未做（本机架构 `linux/amd64` 不匹配，不做模拟器代跑） | `ELF 64-bit LSB executable, ARM aarch64, statically linked, stripped` | 已重建 + 静态反证，运行验证未做 |
| `darwin/amd64` | `dist/eg_darwin_amd64` | `10123520` | `OK` | 未做（无 macOS 环境） | `Mach-O 64-bit x86_64 executable` | 仅交叉编译、未经真机验证（R-12 继续挂起） |
| `darwin/arm64` | `dist/eg_darwin_arm64` | `9628674` | `OK` | 未做（同上） | `Mach-O 64-bit arm64 executable` | 仅交叉编译、未经真机验证（R-12 继续挂起） |

**SHA256SUMS（本次构建快照，因 `DATE` 注入逐次不同）**：

```
b3cdd0404b89ef951e51afed4fb31f546c0a50398b0a6939e1363836f3d46cb9  eg_darwin_amd64
511a64dc4697e15869cf947cab5ab743acf0fd57a290a58cf95dadf3c8f850a5  eg_darwin_arm64
8e13fb36c34e98daa5e61af303405bfec1496a47435bc91625ba54c8eeca5596  eg_linux_amd64
18f3755abb0aa53a7782da52870fc4fc268c69d898e40b315005b6ae15f62f36  eg_linux_arm64
```

**静态构建反证（判据 16 的剩余项，正反两侧同时成立）**：

| 反证 | 命令 | 期望 |
|-|-|-|
| 正面：linux 两平台确为静态链接 | `file dist/eg_linux_amd64 dist/eg_linux_arm64` | 两行均含 `statically linked`、均 `stripped` |
| 反面：全量产物无一动态链接 | `file dist/eg_* \| grep -c 'dynamically linked'` | **`0`** |
| 无 cgo SQLite 依赖 | `CGO_ENABLED=0 go build ./...` | 退 `0`（`modernc.org/sqlite` 纯 Go） |
| 工具链 | `go version` | 与 `--version` 内嵌的 `go1.24.x` 逐字同真 |
| 直接依赖 ≤ 2 | `go list -m all` 直接依赖 | 恰 `gopkg.in/yaml.v3` + `modernc.org/sqlite`（≤ 2） |
| 产物计数封闭 | `ls dist/` | `eg_*` 恰 **4** 个 + `SHA256SUMS` 恰 1 个，无第五个平台、无残留旧版本产物 |

- `dist/` **不入 Git**（`git ls-files dist` 空）：它是构建产物，不是仓库内容。M6 不改这一口径。
- **R-12 延续声明（不得声称已解除）**：darwin 两平台运行验证结论仍是「**未做**」。本文件、`INSTALL.md`、`README.md` 均**不**声称 darwin 产物通过真机验证。M6 未引入任何 macOS 专属代码路径；`run.lock` 用 `flock(2)`（在 Linux / macOS 单机本地文件系统上语义一致），但**网络盘 / 同步盘不保证 `flock` 可靠**（见 §5 已知限制）。
- **R-13 / R-20 改判声明**：M6 已用**强原子事务（临时文件 + `fsync` + 原子 `rename` + `.index/txn/` 事务日志兜底）+ `run.lock` 短临界区锁 + 崩溃恢复两遍幂等回滚**收口「非强原子」缺口（M6 合同 §2/§3/§4/§5）。退出码 `4` 的「Git 提交失败时磁盘保留当前状态、不做破坏性还原」语义一字不改。
- 可复现性：`SHA256SUMS` 依赖注入的 `COMMIT` / `DATE`，同一份源码不同时刻构建哈希不同；溯源锚点是 `eg --version` 的 `commit` 段。本阶段**不做**可复现构建承诺。

---

## 5. M6 相对 M5 的口径增量（供 INSTALL / README 引用）

| 项 | M5（`0.5.0-m5`） | M6（`0.6.0-m6`） |
|-|-|-|
| 顶层命令数 | 22 | **不变 22**（M6 不新增命令；崩溃恢复是写命令的启动 hook，不是新命令） |
| 退出码全集 | `{0,1,2,3,4,6}`（`5` 不启用） | **`{0,1,2,3,4,5,6}`**：`5 = ExitPrecheckOrLock` 自 M6 · T-074 **启用**（写前强校验失败 `E15` / 锁不可用 `E16`，均零权威写入）；`6` 白名单不变 |
| 诊断码 | `E1..E14 ∪ W1..W20,W22..W25 ∪ {I1}` + `Q1..Q5` | **新增恰 5 条**：`E15`（precheck_failed）/ `E16`（lock_timeout）/ `W26`（txn_recovered）/ `W27`（block_merge_conflict）/ `W28`（lock_wait_retry）。`W21` 仍**不分配**；不进 `check` 十二值枚举 |
| 写入原子性 | `git add -A`、**非强原子**、整文件跳过保留现状 | **多文件强原子提交**：accepted write-set 要么全部生效、要么全部不生效（临时文件 + `fsync` + 原子 `rename` + `.index/txn/` 事务日志兜底）；四级可见性模型见合同 §5.1 |
| 并发互斥 | 无锁（仅 `content_hash` 前像比对） | **`run.lock` 单机文件锁**（`flock`）：整 vault 一把锁 + 短临界区（校验 + 落盘 + 提交）；忙时退避重试（`W28`）或超时退 `5` + `E16` |
| 崩溃恢复 | 无 | **两遍且整事务原子**：下一次写命令启动时自动检测未闭合事务，Pass A 全量只读判定、Pass B 幂等回滚到前像（`W26`）；post-crash 外部编辑冲突 / 损坏 intent / 前像不可用等一律 fail closed 退 `5` + `E15`、绝不覆盖崩溃后编辑 |
| 写前强校验 | 无 | **A 类写命令写前置**：`--strict` 升级面（`W1`/`W2`/`W3`/`W4`/`W6` 升 error）在锁内重做候选发现后复核；失败退 `5` + `E15`、零权威写入 |
| 块级合并 | `base_block_hash` 不匹配即跳过 | 不匹配时先做**块级三方安全判定**，不安全才跳过并留痕 `W27`；**不做行级 diff / patch、不引入 diff 依赖** |
| 权威来源 | Markdown / frontmatter | **不变**：Markdown 仍唯一权威；`.index/` 可重建派生物 + M6 新增 **runtime reserved entries**（`run.lock` 普通文件、`txn/` 事务日志目录），存在未闭合事务时**不可删 `.index/`** |
| CI 门禁 | `make lint` | **新增 `test/ci/dep_direction_gate.sh`**（§13 三条依赖禁令做成硬门禁，进 `make lint`） |
| `go.mod` 直接依赖 | `gopkg.in/yaml.v3` + `modernc.org/sqlite` | **不变 ≤ 2**（M6 不引入任何 diff 库、不引入 cgo、不引入网络请求） |
| e2e 脚本 | M1–M4 一个不删 + `m5_*.sh` | M1–M5 **一支不删**（历史脚本按 R6/R7 现态重钉：保留历史事实 + 新增现态双侧锁）；新增 `m6_*.sh`（含 `m6_acceptance.sh` / `m6_docs_commands.sh`），磁盘总数只增不减 |
| 已知限制 | R-12 / R-13 / R-20 + M4 延期债登记 | R-12 延续（darwin 未真机验证）；**新增**「网络盘 / 同步盘不保证 `flock` 原子性」「存在未闭合事务时不可删 `.index/`」；M4/M5 延期债在 M6 验收报告终态化（J14/J15/I2） |

---

## 6. 未知 / 待确认（本文件不猜测）

1. 是否真的创建并推送 `v0.6.0-m6`：**未知 / 待确认**（无远端、属组织决策）。
2. 本仓最终托管地址与 CI 形态：**未知 / 待确认**。
3. darwin 真机验证何时补齐、由谁执行：**未知 / 待确认**（在此之前 R-12 保持挂起）。
4. `run.lock`（`flock`）在网络文件系统 / 同步盘上的真实可靠性：**未知 / 待确认**（合同 §13 已登记为「不承诺、不探测」的已知限制，本地无多机 / 网络盘环境实测）。
