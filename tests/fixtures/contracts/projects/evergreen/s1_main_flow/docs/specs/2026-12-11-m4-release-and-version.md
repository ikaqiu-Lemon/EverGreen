# M4 版本号与发布口径决策（`eg`）

- 文档 ID：`docs/specs/2026-12-11-m4-release-and-version.md`
- 所属：Epic `evergreen / s1_main_flow` · 阶段 **S3 · 里程碑 M-004（M4）** · Task **T-evergreen.s1_main_flow-158614-062**（版本推进）/ **-063**（验收收口引用）
- 状态：**决策已定**（版本号口径、tag 命名与创建命令）+ **未知 / 待确认**（远端与是否真的打 tag / 推送）
- 关联风险：**R-12**（darwin 产物仅交叉编译、无真机验证，**继续登记、不得声称已解除**）、**R-13 / R-20**（`git add -A`、非强原子、整文件跳过，继续登记）
- 消费方：`evergreen/INSTALL.md`（引用本文件）、`evergreen/README.md`、`github.com/ikaqiu-Lemon/EverGreen/internal/cli/docs_test.go`（`docsReleaseSpec` 指针自 M4 起指向本文件）、`evergreen/test/e2e/m4_docs_commands.sh`、T-…-063 的 M4 验收报告（逐条核对）
- 前序（只读、不改写）：`2026-11-08-m3-release-and-version.md` 是 **M3** 的唯一决策出处，其结论逐字保留；自 M4 起「当前版本号」的唯一出处是**本文件**。

本文件是 M4 版本号与发布口径的**唯一**决策出处。凡本文件未给出证据的结论，一律写「未知 / 待确认」，**不做任何假设**。

> **K-062-01 收敛说明**：M3 期 `docs_test.go` 的 `docsReleaseSpec` 指针指向 `2026-11-08-m3-release-and-version.md`，该 M3 文档逐字声明的是 `0.3.0-m3`。M4 把版本号推进到 `0.4.0-m4` 后，`TestDocsVersionThreeSourcesConsistent` 要求「决策文档逐字含当前版本号」这一**原判据不变**，但当期出处已换成本文件——故本文件承接指针。M3 文档一字未改、仍在盘，属**阶段化更新**而非放宽。

---

## 1. 版本号规则

### 1.1 M4 版本取值

**`0.4.0-m4`**

| 位 | 取值 | 含义 |
|-|-|-|
| major | `0` | S1–S3 尚未整体收口；`1.0.0` 何时发属组织决策，本阶段不预设 |
| minor | `4` | 与里程碑序号对齐：M1 = `0.1.x`、M2 = `0.2.x`、M3 = `0.3.x`、**M4 = `0.4.x`** |
| patch | `0` | 该 minor 下的首个产物 |
| 预发布后缀 | `-m4` | 明示这是**里程碑试用版**，不是对外正式发布版（收口前一律带里程碑后缀） |

形态约束（未变）：`^[0-9]+\.[0-9]+\.[0-9]+(-[a-z0-9.]+)?$`；`-m4` 属预发布段，语义化排序高于 `0.3.0-m3`、低于 `0.4.0`。

**为什么是 minor 而不是 patch**：M4 新增了 2 条顶层命令（`eg reconcile` / `eg check`，注册表总量 18 → **20**）与 M4 对账诊断码族（`E11`–`E14` / `W13`–`W20` / 查询域 `Q4`），属**向后兼容的能力新增**，`0.y.z` 口径下升 minor。既有命令的参数与退出码语义**未做破坏性变更**；退出码全集仍恰 `{0,1,2,3,4,6}`（`5` 全程不启用）。

### 1.2 三处同源

版本号在仓内**只有三处**出现，必须逐字相等：

| # | 位置 | 形态 | 生效场景 |
|-|-|-|-|
| ① | `evergreen/Makefile` 的 `VERSION ?= 0.4.0-m4` | Make 变量缺省值 | `make build` 经 `-ldflags` 注入；`make print-version` 直接打印它 |
| ② | `github.com/ikaqiu-Lemon/EverGreen/internal/version/version.go` 的 `const DefaultVersion = "0.4.0-m4"`（`var Version = DefaultVersion`） | Go 常量 | 不经 Makefile 的 `go build` / `go run` / `go test` |
| ③ | 本文件 §1.1 声明的 `0.4.0-m4` | 文档 | 决策依据 |

- 注入机制**不变**：`-ldflags '-s -w -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Version=$(VERSION) -X …Commit=$(COMMIT) -X …Date=$(DATE)'`。
- 覆盖方式**不变**：`make build VERSION=0.4.1-m4` 即可临时覆盖（`?=` 语义）。
- `make build` 目标结构：`CGO_ENABLED=0` 四平台交叉编译 dist/ + `SHA256SUMS`。
- 一致性守护（可机器执行）：
  - `go test ./internal/version -run 'TestVersionNotScaffold|TestVersionMatchesMakefile'`：读 `../../Makefile` 的 `VERSION ?=` 行与 `DefaultVersion` 逐字比对，并断言两者都不再是 `0.0.0-dev`；
  - `go test ./internal/cli -run TestDocs`：Makefile / `version.go` / **本文件** / `README.md` / `INSTALL.md` 五处逐字比对；
  - `bash test/e2e/m4_docs_commands.sh`：`eg --version` 实测输出 == `make print-version` == `version.go` 字面量，且 README / SKILL 覆盖 20 命令。
- `0.0.0-dev` 的唯一残留位置仍是 `internal/version/version.go` 的 `const ScaffoldVersion`，**只作为反证基线**，不参与任何构建注入。

---

## 2. tag 命名与创建命令

| 项 | 结论 |
|-|-|
| 命名规则 | `v<版本号>`，即 **`v0.4.0-m4`**（与 §1.1 取值一一对应，前缀 `v` 固定，与 M2/M3 同规则） |
| tag 类型 | 附注 tag（annotated），带说明与作者信息 |
| 打在哪 | `evergreen/` 仓的 `master` 分支上、M4 收口 commit |
| 本轮是否已创建 | **未创建**（见 §3：是否打 tag 属组织决策，本地无依据） |

逐字可执行的创建命令（CWD = `evergreen/`）：

```console
$ git tag -a v0.4.0-m4 -m "eg 0.4.0-m4：S3 里程碑 M4 试用版（20 命令，reconcile/check 对账，退出码全集不变）"
$ git tag -n1 v0.4.0-m4   # 校验：应打印 tag 名与说明首行
```

删除误打的本地 tag：

```console
$ git tag -d v0.4.0-m4
```

> 上述命令块用 `console` 而非 `bash` 标注：文档命令抽取器只跑 ```bash 块，**本轮不执行**任何 tag 命令（§3 结论为「未知 / 待确认」）。

---

## 3. 远端状态（以实测输出为依据）

在 `evergreen/` 内实测（2026-12-11，沙箱本机）：

```console
$ git remote -v
$ echo "exit=$?"
exit=0
```

→ `git remote -v` **无任何输出**（退 0），即本仓**未配置任何远端**。

```console
$ git tag
$ echo "exit=$?"
exit=0
```

→ `git tag` **无任何输出**（退 0），即本仓**当前不存在任何 tag**（含轻量 tag 与附注 tag）。

### 结论

| 问题 | 结论 | 依据 |
|-|-|-|
| 是否存在远端仓库？ | **否**（本地视角） | `git remote -v` 空输出 |
| 是否已有 tag？ | **否** | `git tag` 空输出 |
| 是否**应该**打 `v0.4.0-m4`？ | **未知 / 待确认** | 属组织决策；本地无任何依据可判 |
| 要不要推送、推到哪个远端？ | **未知 / 待确认** | 本地无远端信息，**不得假设存在远端**，本轮不新建远端、不推送任何 tag / 分支 |
| 本轮实际动作 | **只给命令、不执行**：未创建 tag、未新建远端、未推送 | 本文件 §2 与 Task T-…-062 的活动记录 |

> 口径说明（延续 M2/M3）：`evergreen/README.md` 与 `evergreen/INSTALL.md` 中**不出现**任何推送类命令；推送相关决策只在本文件里以「未知 / 待确认」登记。

---

## 4. 四平台产物验证矩阵

本轮实测（构建机：`linux/amd64`，注入 `VERSION=0.4.0-m4`，`COMMIT` 取当期 HEAD 短 SHA）：

| 平台 | 产物 | 交叉编译 | `sha256sum -c SHA256SUMS` | 运行验证 | 结论 |
|-|-|-|-|-|-|
| `linux/amd64` | `dist/eg_linux_amd64` | ✅ 成功 | ✅ `OK` | ✅ 已实跑 `--version`（输出含 `eg 0.4.0-m4` 与当期 `commit`）+ M4 docs e2e | **已验证** |
| `linux/arm64` | `dist/eg_linux_arm64` | ✅ 成功 | ✅ `OK` | ❌ **未做**（本机架构不匹配，沙箱内无 arm64 运行环境） | 仅交叉编译 |
| `darwin/amd64` | `dist/eg_darwin_amd64` | ✅ 成功 | ✅ `OK` | ❌ **未做**（沙箱内无 macOS 运行环境） | 仅交叉编译，**未经真机运行验证** |
| `darwin/arm64` | `dist/eg_darwin_arm64` | ✅ 成功 | ✅ `OK` | ❌ **未做**（同上） | 仅交叉编译，**未经真机运行验证** |

- `ls dist/eg_* | wc -l` = `4`；`cd dist && sha256sum -c SHA256SUMS` 退 `0`，四行全 `OK`。
- **R-12 延续声明（不得声称已解除）**：darwin 两个平台的运行验证结论仍是「**未做**」。`INSTALL.md` §5 已把它登记为已知限制；本文件、`INSTALL.md`、`README.md` 均**不**声称 darwin 产物通过过真机验证。M4 没有改变这一事实，也没有引入任何 macOS 专属代码路径。
- **R-13 / R-20 延续声明**：提交仍走 `git add -A`（整仓暂存）、**非强原子**、失败按整文件跳过并保留现状；M4 的 `eg reconcile` R1 纳管 commit 同样遵守这一口径（`git add -A` + 整文件跳过，不做崩溃恢复、不做多文件事务、不做块级安全合并——后者属 S5/M6）。
- 可复现性：`SHA256SUMS` 依赖注入的 `COMMIT` 与 `DATE`，**同一份源码在不同时刻构建的哈希不同**；使用者应校验**自己拿到的那份** `dist/SHA256SUMS`。溯源锚点是 `eg --version` 输出的 `commit` 段（等于构建时的 `git rev-parse --short HEAD`）。本阶段**不做**可复现构建承诺。

---

## 5. M4 相对 M3 的口径增量（供 INSTALL / README 引用）

| 项 | M3（`0.3.0-m3`） | M4（`0.4.0-m4`） |
|-|-|-|
| 顶层命令数 | 18 | **20**（新增 `eg reconcile` / `eg check`） |
| 退出码全集 | `{0,1,2,3,4,6}`（`5` 不启用） | **不变** `{0,1,2,3,4,6}`：`eg reconcile` 用 `0/1/2/3/4`、`eg check` 用 `0/1/2`；`5` 仍不启用、`6` 白名单仍恰 `{proposal approve, delete}` |
| 对账能力 | 无 | `internal/reconcile/` 包（R1–R7 只读检查）；`eg reconcile` 可对 R2/R6 补写并恰 0 或 1 次 commit；`eg check` 恒 0 commit；**对账不是任何写命令前置**（写前对账属 S5/M6） |
| 诊断码 | E/W/I 域 `E1..E10 ∪ W1..W12 ∪ {I1}` + 查询域 `Q1..Q3` | 新增 `E11`–`E14` / `W13`–`W20`（对账检查域，`check` 十二值单射）+ 查询域 `Q4`（失效端点被隐藏计数）。见 M4 验收报告「计数变更面」逐项复算 |
| 失效端点可见性 | — | `deprecated` 对端**默认隐藏**，`--include-deprecated` 显式筛选展示（带 `[失效]`）；逻辑删除维度正交仍在 |
| e2e 脚本 | 25 个（M1+M2 恰 10、M3 恰 15） | M1–M3 的 25 个**一个不删**；M4 新增 `m4_*.sh`（含 `m4_acceptance.sh`），磁盘总数只增不减 |
| 已知限制 | R-12 / R-13 + 未决 K-041-01 / K-043-01 | R-12 / R-13 原样延续（并入 R-20）；**K-041-01 / K-043-01 已在 M4 收敛**（见 M4 验收报告判据 11 / 12） |

---

## 6. 未知 / 待确认（本文件不猜测）

1. 是否真的创建并推送 `v0.4.0-m4`：**未知 / 待确认**（无远端、属组织决策）。
2. 本仓最终托管地址与 CI 形态：**未知 / 待确认**。
3. darwin 真机验证何时补齐、由谁执行：**未知 / 待确认**（在此之前 R-12 保持挂起）。
