# M5 版本号与发布口径决策（`eg`）

- 文档 ID：`docs/specs/2027-01-17-m5-release-and-version.md`
- 所属：Epic `evergreen / s1_main_flow` · 阶段 **S4 · 里程碑 M-005（M5）** · Task **T-evergreen.s1_main_flow-158614-069**（M5 文档 / 版本 / 产物与统一验收）
- 状态：**决策已定**（版本号取值、同源位置、tag 命名与创建命令）+ **未知 / 待确认**（远端、是否真的打 tag / 推送）
- 关联风险：**R-12**（darwin 产物仅交叉编译、无真机验证，**继续登记、不得声称已解除**）、**R-13 / R-20**（`git add -A`、非强原子、整文件跳过 —— M5 一格未动，属 M6 范围）
- 消费方：`evergreen/INSTALL.md`、`evergreen/README.md`、`github.com/ikaqiu-Lemon/EverGreen/internal/cli/docs_test.go`（`docsReleaseSpec` 指针自 M5 起指向本文件）、`evergreen/test/e2e/m4_docs_commands.sh`（版本消费面）、`evergreen/test/e2e/m5_docs_commands.sh`、T-…-069 的 M5 验收报告
- 前序（只读、不改写）：`2026-10-03-m2-release-and-version.md` / `2026-11-08-m3-release-and-version.md` / `2026-12-11-m4-release-and-version.md` 分别是 M2 / M3 / M4 的唯一决策出处，其结论**逐字保留**；自 M5 起「当前版本号」的唯一出处是**本文件**。

本文件是 M5 版本号与发布口径的**唯一**决策出处。凡本文件未给出证据的结论，一律写「未知 / 待确认」，**不做任何假设**。

> **指针承接说明（延续 M4 的 K-062-01 体例）**：`docs_test.go` 的 `docsReleaseSpec` 在 M4 期指向 `2026-12-11-m4-release-and-version.md`，该文档逐字声明的是 `0.4.0-m4`。M5 把版本号推进到 `0.5.0-m5` 后，`TestDocsVersionThreeSourcesConsistent` 的**原判据一字不改**（决策文档必须逐字含当期版本号、必须对推送 / tag 标「未知」），只是当期出处换成本文件 —— M2/M3/M4 三份文档仍在盘、事实一字未改，属**阶段化更新**而非放宽。

---

## 1. 版本号规则

### 1.1 M5 版本取值

**`0.5.0-m5`**

| 位 | 取值 | 含义 |
|-|-|-|
| major | `0` | S1–S5 尚未整体收口；`1.0.0` 何时发属组织决策，本阶段不预设 |
| minor | `5` | 与里程碑序号对齐：M1 = `0.1.x`、M2 = `0.2.x`、M3 = `0.3.x`、M4 = `0.4.x`、**M5 = `0.5.x`** |
| patch | `0` | 该 minor 下的首个产物 |
| 预发布后缀 | `-m5` | 明示这是**里程碑试用版**，不是对外正式发布版（收口前一律带里程碑后缀） |

形态约束（未变）：`^[0-9]+\.[0-9]+\.[0-9]+(-[a-z0-9.]+)?$`；`-m5` 属预发布段，语义化排序高于 `0.4.0-m4`、低于 `0.5.0`。

**为什么是 minor 而不是 patch**：M5 新增 2 条顶层命令（`eg index` 四子命令 / `eg bench`，注册表总量 20 → **22**）、新增 `internal/index` 派生索引层与 `internal/query` 的排序 / 分页面（`--limit` / `--offset`）、新增诊断码 `W22`–`W25` 与查询域 `Q5`，属**向后兼容的能力新增**；`0.y.z` 口径下升 minor。既有命令的默认输出语义与退出码**未做破坏性变更**；退出码全集仍恰 `{0,1,2,3,4,6}`（`5` 全程不启用，其启用属 M6 · T-…-070/074）。

**一处需显式登记的默认值变化（非破坏性，但可观察）**：三条读路径 `eg search` / `eg card show` / `eg rel` 自 M5 起默认 `--limit 50`（`--limit 0` = 不限量），命中截断时报 `W25`。这是 T-…-068 按 A-47 落地的默认值，不是参数语义反转，也不改任何退出码。

### 1.2 三处同源

版本号在仓内**只有三处**出现，必须逐字相等：

| # | 位置 | 形态 | 生效场景 |
|-|-|-|-|
| ① | `evergreen/Makefile` 的 `VERSION ?= 0.5.0-m5` | Make 变量缺省值 | `make build` 经 `-ldflags` 注入；`make print-version` 直接打印它 |
| ② | `github.com/ikaqiu-Lemon/EverGreen/internal/version/version.go` 的 `const DefaultVersion = "0.5.0-m5"`（`var Version = DefaultVersion`） | Go 常量 | 不经 Makefile 的 `go build` / `go run` / `go test` |
| ③ | 本文件 §1.1 声明的 `0.5.0-m5` | 文档 | 决策依据 |

文档消费面（不是第四个「源」，只跟随）：`evergreen/README.md`、`evergreen/INSTALL.md`。

- 注入机制**不变**：`-ldflags '-s -w -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Version=$(VERSION) -X …Commit=$(COMMIT) -X …Date=$(DATE)'`。
- 覆盖方式**不变**：`make build VERSION=0.5.1-m5`（`?=` 语义）。
- 旧版本字面量口径（本轮明确写死，供门禁消费）：`Makefile` 内**不留任何**历史版本字面量；`internal/version/version.go` 里 `0.4.0-m4` 只允许出现在**里程碑沿革注释**中，**不得出现在任何赋值行**；`0.3.0-m3` 在两个文件里**零残留**（M4 期结论原样延续，不放宽）。`0.0.0-dev` 的唯一残留位置仍是 `ScaffoldVersion`，**只作反证基线**。
- 一致性守护（可机器执行）：
  - `go test ./internal/version -run 'TestVersionNotScaffold|TestVersionMatchesMakefile'`；
  - `go test ./internal/cli -run TestDocs`：Makefile / `version.go` / **本文件** / `README.md` / `INSTALL.md` 五处逐字比对；
  - `bash test/e2e/m4_docs_commands.sh`：`eg --version` == `make print-version` == `version.go` 字面量 == 当期版本，且 M4 的 20 命令结论按「摘掉 M5 追加命令后恰 20」复算；
  - `bash test/e2e/m5_docs_commands.sh`：M5 增量面（22 命令 / `index` 四子命令 / `bench` 五键 / 降级留痕 / 分页）与文档同真。

---

## 2. tag 命名与创建命令

| 项 | 结论 |
|-|-|
| 命名规则 | `v<版本号>`，即 **`v0.5.0-m5`**（与 §1.1 一一对应，前缀 `v` 固定，与 M2/M3/M4 同规则） |
| tag 类型 | 附注 tag（annotated） |
| 打在哪 | `evergreen/` 仓 `master` 分支的 M5 收口 commit |
| 本轮是否已创建 | **未创建**（见 §3：属组织决策，本地无依据） |

逐字可执行的创建命令（CWD = `evergreen/`）：

```console
$ git tag -a v0.5.0-m5 -m "eg 0.5.0-m5：S4 里程碑 M5 试用版（22 命令，index 派生索引 / 读路径降级 / 排序分页 / bench 五键；退出码全集不变）"
$ git tag -n1 v0.5.0-m5   # 校验：应打印 tag 名与说明首行
```

删除误打的本地 tag：

```console
$ git tag -d v0.5.0-m5
```

> 上述命令块用 `console` 而非 `bash` 标注：文档命令抽取器只跑 ```bash 块，**本轮不执行**任何 tag 命令（§3 结论为「未知 / 待确认」）。

---

## 3. 远端状态（以实测输出为依据）

在 `evergreen/` 内实测（2027-01-17，沙箱本机）：

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
| 是否**应该**打 `v0.5.0-m5`？ | **未知 / 待确认** | 属组织决策；本地无依据 |
| 要不要推送、推到哪个远端？ | **未知 / 待确认** | 本地无远端信息，**不得假设存在远端**；本轮不新建远端、不推送任何 tag / 分支 |
| 本轮实际动作 | **只给命令、不执行** | 本文件 §2 与 T-…-069 活动记录 |

> 口径说明（延续 M2/M3/M4）：`README.md` 与 `INSTALL.md` 中**不出现**任何推送类命令。

---

## 4. 四平台产物验证矩阵（M5）

状态口径按阶段如实登记，**不提前声称**。下表已由**阶段 B（2026-09-09）真实 `make dist` 证据回填**：
构建命令 `cd evergreen && make dist`（`rc=0`），四平台均 `CGO_ENABLED=0 go build -trimpath`
（见 `Makefile:50`），构建源为 M5 收口 HEAD `db64ce7`，产物恰 4 个可执行文件 + `SHA256SUMS`。

| 平台 | 产物 | 交叉编译 | `sha256sum -c` | 运行验证 | 本轮状态 |
|-|-|-|-|-|-|
| `linux/amd64` | `dist/eg_linux_amd64` | ✅ **已重建**（`CGO_ENABLED=0`，源 = HEAD `db64ce7`，9,539,768 B） | ✅ `OK` | ✅ **已实跑**：`./dist/eg_linux_amd64 --version` → `eg 0.5.0-m5 (commit db64ce7, built 2026-09-08T20:45:50Z, go1.24.13 X:cacheprog,testenhance)`，`commit` 段逐字 == 当期 HEAD `db64ce7` | ✅ **阶段 B 已重建并本机运行验证**（`file(1)`：`ELF 64-bit LSB executable, x86-64 … statically linked … stripped`） |
| `linux/arm64` | `dist/eg_linux_arm64` | ✅ **已重建**（`CGO_ENABLED=0`，9,044,152 B） | ✅ `OK` | ❌ **未做**（本机架构 `linux/amd64` 不匹配，**不做模拟器代跑**） | ✅ 已重建 + 静态反证（`file(1)`：`ELF 64-bit LSB executable, ARM aarch64 … statically linked … stripped`），运行验证仍**未做** |
| `darwin/amd64` | `dist/eg_darwin_amd64` | ✅ **已重建**（`CGO_ENABLED=0`，9,709,072 B） | ✅ `OK` | ❌ **未做**（无 macOS 环境） | ✅ 已重建，**仅交叉编译、未经真机验证**（`file(1)`：`Mach-O 64-bit x86_64 executable`；R-12 继续挂起） |
| `darwin/arm64` | `dist/eg_darwin_arm64` | ✅ **已重建**（`CGO_ENABLED=0`，9,260,530 B） | ✅ `OK` | ❌ **未做**（同上） | ✅ 已重建，**仅交叉编译、未经真机验证**（`file(1)`：`Mach-O 64-bit arm64 executable`；R-12 继续挂起） |

**本次 `SHA256SUMS` 逐字留痕**（`cd evergreen/dist && sha256sum -c SHA256SUMS` → 4 行全 `OK`，`0` 行 `FAILED`）：

```text
5b722b67c5c992e895a332ae6ffe5d37914070547d01a13eaeebdf0168228f92  eg_darwin_amd64
94dc4619f51dc945035b0c688176c36e1ac870415393b5417d95647d7721ceab  eg_darwin_arm64
b05a1087879d0688fc3cac13e4d77b8e17592721c56e2cb171e937e6500bd501  eg_linux_amd64
c83dcf17ce0cafbc051f8ae8e2ca3b3093bba0d11226d08140dc9c045eeb65b3  eg_linux_arm64
```

**静态构建反证（判据 16 的剩余项，正反两侧同时成立）**：

| 反证 | 命令 | 实测 |
|-|-|-|
| 正面：linux 两平台确为静态链接 | `file dist/eg_linux_amd64 dist/eg_linux_arm64` | 两行均含 `statically linked`（`BuildID[sha1]=8c5491ef…` / `e1358feb…`，均 `stripped`） |
| 反面：全量产物无一动态链接 | `file dist/eg_* \| grep -c 'dynamically linked'` | **`0`** |
| 工具链 | `go version` | `go1.24.13 linux/amd64`（与 `--version` 内嵌的 `go1.24.13` 逐字同真） |
| 纯 Go SQLite（无 C 工具链） | `Makefile:41 / :50` | 本机与四平台交叉编译**均**显式 `CGO_ENABLED=0`；`modernc.org/sqlite` 为纯 Go 实现 |
| 产物计数封闭 | `ls dist/` | `eg_*` 恰 **4** 个 + `SHA256SUMS` 恰 1 个，无第五个平台、无残留旧版本产物 |

- `dist/` **不入 Git**（`git ls-files dist` 空）：它是构建产物，不是仓库内容。M5 不改这一口径。
- **R-12 延续声明（不得声称已解除）**：darwin 两平台运行验证结论仍是「**未做**」。本文件、`INSTALL.md`、`README.md` 均**不**声称 darwin 产物通过真机验证。M5 未引入任何 macOS 专属代码路径；`modernc.org/sqlite` 为**纯 Go**（`CGO_ENABLED=0` 静态构建），未新增 C 工具链要求 —— 这一点降低了跨平台构建风险，但**不等于**补上了 darwin 真机验证。
- **R-13 / R-20 延续声明**：提交仍走 `git add -A`、**非强原子**、失败按整文件跳过并保留现状。M5 一格未改；强原子事务 / 锁 / 崩溃恢复 / 块级安全合并属 **M6（S5）· T-…-070～075**。
- 可复现性：`SHA256SUMS` 依赖注入的 `COMMIT` / `DATE`，同一份源码不同时刻构建哈希不同；溯源锚点是 `eg --version` 的 `commit` 段。本阶段**不做**可复现构建承诺。

---

## 5. M5 相对 M4 的口径增量（供 INSTALL / README 引用）

| 项 | M4（`0.4.0-m4`） | M5（`0.5.0-m5`） |
|-|-|-|
| 顶层命令数 | 20 | **22**（新增 `eg index`〔`build` / `sync` / `status` / `rebuild`〕与 `eg bench`） |
| 退出码全集 | `{0,1,2,3,4,6}`（`5` 不启用） | **不变** `{0,1,2,3,4,6}`：`eg index` 用 `0/1/2`（写子命令另可 `3/4`）、`eg bench` 恒 `0/1/2`；`5` 仍不启用（属 M6） |
| 权威来源 | Markdown / frontmatter | **不变**：Markdown 仍是唯一权威来源；`.index/` 是**可删除、可重建的本机派生物**，不入 Git、不随仓库分发 |
| 检索后端 | 全量 Markdown 扫描 | SQLite/FTS5（纯 Go `modernc.org/sqlite`，`CGO_ENABLED=0`）；索引**缺失 / 陈旧 / 损坏**时读路径**降级为全量扫描**，结果以 Markdown 为准 |
| 诊断码 | E/W/I `E1..E14 ∪ W1..W20 ∪ {I1}` + 查询域 `Q1..Q4` | 新增 `W22`（index_stale）/ `W23`（index_unusable）/ `W24`（index_corrupt）/ `W25`（result_truncated）+ 查询域 `Q5`（降级留痕计数）。`W21` 仍**不分配**（留白） |
| 排序与分页 | 无显式分页 | 三条读路径支持 `--limit`（默认 50，`0` = 不限量）/ `--offset`；`card show` / `rel` 的关系分页是**正反向合并后的全局分页**（不允许 `2*limit`） |
| 性能观测 | 无 | `eg bench [--json]` 输出五键：`search_p95_ms` / `card_show_p95_ms` / `rel_p95_ms` / `index_build_ms` / `index_incremental_ms` |
| `go.mod` 的 `go` 指令 | `1.22` | **`1.24.0`**（被 `modernc.org/sqlite v1.45.0` 的 `go >= 1.24.0` 抬上来的硬下限，非主动升级） |
| 外部依赖 | 仅 `gopkg.in/yaml.v3`（只读用途） | 追加 `modernc.org/sqlite`（纯 Go，索引层专用；不引入 CGO） |
| e2e 脚本 | M1–M4 累计只增不减 | M1–M4 一个不删；新增 `m5_*.sh`（含 `m5_acceptance.sh`），磁盘总数只增不减 |
| 已知限制 | R-12 / R-13 / R-20 | 原样延续；另登记 **M4 延期债**：历史 M2/M3 门禁的版本冻结差异与 I2 合同漂移（见 M5 验收报告「延期债登记」一节，**不改历史结论、只在 M5 新文档登记**） |

---

## 6. 未知 / 待确认（本文件不猜测）

1. 是否真的创建并推送 `v0.5.0-m5`：**未知 / 待确认**（无远端、属组织决策）。
2. 本仓最终托管地址与 CI 形态：**未知 / 待确认**。
3. darwin 真机验证何时补齐、由谁执行：**未知 / 待确认**（在此之前 R-12 保持挂起）。
4. 索引后端在**超大库**（10 万卡以上）的实测表现：**未知 / 待确认**（M5 门槛按 A-48 的规模档位取样，未做超大库压测）。
