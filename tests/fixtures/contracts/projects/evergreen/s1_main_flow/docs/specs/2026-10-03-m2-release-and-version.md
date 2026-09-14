# M2 版本号与试用 release 决策（`eg`）

- 文档 ID：`docs/specs/2026-10-03-m2-release-and-version.md`
- 所属：Epic `evergreen / s1_main_flow` · 里程碑 **M-002（M2）** · Task **T-evergreen.s1_main_flow-158614-027**
- 状态：**决策已定**（版本号口径）+ **未知 / 待确认**（远端与是否打 tag）
- 关联风险：**R-2**（README 误述与无效命令）、**R-3**（版本仍 `0.0.0-dev` / 无 tag / 无远端 / 无安装说明）、**R-4**（macOS 产物仅交叉编译）
- 消费方：`evergreen/INSTALL.md`（引用本文件）、`evergreen/README.md`、T-…-029 的 M2 验收报告（逐条核对）

本文件是 M2 版本号与发布口径的**唯一**决策出处。凡本文件未给出证据的结论，一律写「未知 / 待确认」，**不做任何假设**。

---

## 1. 版本号规则

### 1.1 M2 试用版本取值

**`0.2.0-m2`**

| 位 | 取值 | 含义 |
|-|-|-|
| major | `0` | S1 尚未整体收口；`1.0.0` 何时发属组织决策，本阶段不预设 |
| minor | `2` | 与里程碑序号对齐：M1 = `0.1.x`（未发布过正式产物，历史缺省值一直是 `0.0.0-dev`）、**M2 = `0.2.x`** |
| patch | `0` | 该 minor 下的首个产物 |
| 预发布后缀 | `-m2` | 明示这是**里程碑试用版**，不是对外正式发布版（S1 未收口前一律带里程碑后缀） |

形态约束：`^[0-9]+\.[0-9]+\.[0-9]+(-[a-z0-9.]+)?$`；语义化版本可比较，`-m2` 属预发布段，排序低于同号正式版。

### 1.2 三处同源（本地开发默认值 / release 注入值）

版本号在仓内**只有三处**出现，必须逐字相等：

| # | 位置 | 形态 | 生效场景 |
|-|-|-|-|
| ① | `evergreen/Makefile` 的 `VERSION ?= 0.2.0-m2` | Make 变量缺省值 | `make build` / `make release` 经 `-ldflags` 注入 |
| ② | `github.com/ikaqiu-Lemon/EverGreen/internal/version/version.go` 的 `const DefaultVersion = "0.2.0-m2"`（`var Version = DefaultVersion`） | Go 常量 | 不经 Makefile 的 `go build` / `go run` / `go test` |
| ③ | 本文件 §1.1 声明的 `0.2.0-m2` | 文档 | 决策依据 |

- 注入机制**不变**：`-ldflags '-s -w -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Version=$(VERSION) -X …Commit=$(COMMIT) -X …Date=$(DATE)'`。
- 覆盖方式**不变**：`make release VERSION=0.2.1-m2` 即可临时覆盖（`?=` 语义）。
- 一致性守护（两道）：
  - `go test ./internal/version/...` 的 `TestVersionNotScaffold`：读 `../../Makefile` 的 `VERSION ?=` 行与 `DefaultVersion` 逐字比对，并断言两者都不再是 `0.0.0-dev`；
  - `bash test/e2e/m2_docs_commands.sh` 与 `go test ./internal/cli/... -run Docs`：把 Makefile、`version.go`、**本文件**三处抽出的版本号做三方逐字比对。
- `0.0.0-dev` 的唯一残留位置是 `internal/version/version.go` 的 `const ScaffoldVersion`，**只作为反证基线**（断言「默认值不得再等于它」），不参与任何构建注入。

---

## 2. tag 命名与创建命令

| 项 | 结论 |
|-|-|
| 命名规则 | `v<版本号>`，即 **`v0.2.0-m2`**（与 §1.1 取值一一对应，前缀 `v` 固定） |
| tag 类型 | 附注 tag（annotated），带说明与作者信息 |
| 打在哪 | `evergreen/` 仓的 `master` 分支上、M2 收口 commit |
| 本轮是否已创建 | **未创建**（见 §3：是否打 tag 属组织决策，本地无依据） |

逐字可执行的创建命令（CWD = `evergreen/`）：

```bash
git tag -a v0.2.0-m2 -m "eg 0.2.0-m2：S1 里程碑 M2 试用版（九命令真实可用，rel remove 归 M3/S2）"
git tag -n1 v0.2.0-m2   # 校验：应打印 tag 名与说明首行
```

删除误打的本地 tag（同样逐字可执行）：

```bash
git tag -d v0.2.0-m2
```

---

## 3. 远端状态（以实测输出为依据）

在 `evergreen/` 内实测（2026-09-01，沙箱本机）：

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

```console
$ git branch -a
* master
```

→ 只有本地 `master`，**无** `remotes/*` 远程跟踪分支，与「未配置远端」互相印证。

### 结论

| 问题 | 结论 | 依据 |
|-|-|-|
| 是否存在远端仓库？ | **否**（本地视角） | `git remote -v` 空输出、`git branch -a` 无 `remotes/*` |
| 是否已有 tag？ | **否** | `git tag` 空输出 |
| 是否**应该**打 `v0.2.0-m2`？ | **未知 / 待确认** | 属组织决策；本地无任何依据可判 |
| 要不要推送、推到哪个远端？ | **未知 / 待确认** | 本地无远端信息，**不得假设存在远端**，本轮不新建远端、不推送任何 tag / 分支 |
| 本轮实际动作 | **只给命令、不执行**：未创建 tag、未新建远端、未推送 | 本文件 §2 与 Task T-…-027 的活动记录 |

> 口径说明：`evergreen/README.md` 与 `evergreen/INSTALL.md` 中**不出现**任何推送类命令（对应 task 的反证断言），推送相关决策只在本文件里以「未知 / 待确认」登记。

---

## 4. 四平台产物验证矩阵

`make release` 目标结构**未改**：`clean` → 逐平台 `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath -ldflags '…'` → `cd dist && sha256sum eg_* > SHA256SUMS`。`PLATFORMS` 列表保持 `linux/amd64 linux/arm64 darwin/amd64 darwin/arm64`。

本轮实测（构建机：`linux/amd64`，Go `go1.24.13`，注入 `VERSION=0.2.0-m2`）：

| 平台 | 产物 | 交叉编译 | `sha256sum -c SHA256SUMS` | 运行验证 | 结论 |
|-|-|-|-|-|-|
| `linux/amd64` | `dist/eg_linux_amd64` | ✅ 成功 | ✅ `OK` | ✅ 已实跑 `--version`（输出 `eg 0.2.0-m2 (commit …, built …, go1.24.13…)`）+ 全套 e2e | **已验证** |
| `linux/arm64` | `dist/eg_linux_arm64` | ✅ 成功 | ✅ `OK` | ❌ **未做**（本机架构不匹配，沙箱内无 arm64 运行环境） | 仅交叉编译 |
| `darwin/amd64` | `dist/eg_darwin_amd64` | ✅ 成功（`Mach-O 64-bit x86_64`） | ✅ `OK` | ❌ **未做**（沙箱内无 macOS 运行环境） | 仅交叉编译，**未经真机运行验证** |
| `darwin/arm64` | `dist/eg_darwin_arm64` | ✅ 成功（`Mach-O 64-bit arm64`） | ✅ `OK` | ❌ **未做**（同上） | 仅交叉编译，**未经真机运行验证** |

- `ls dist/eg_* | wc -l` = `4`；`cd dist && sha256sum -c SHA256SUMS` 退 `0`，四行全 `OK`。
- 反证：`grep -c "0.0.0-dev" dist/eg_*` 四个产物全为 `0`（脚手架版本号已彻底脱离产物）。
- **darwin 声明（R-4 关闭判据）**：darwin 两个平台的运行验证结论是「**未做**」，`INSTALL.md` §4 已把它登记为已知限制；本文件与 `INSTALL.md` / `README.md` 均**不**声称 darwin 产物通过过真机验证。
- 哈希值的可复现性提示：`SHA256SUMS` 依赖注入的 `COMMIT` 与 `DATE`（`date -u`），因此**同一份源码在不同时刻构建的哈希不同**。本轮快照（`COMMIT=918627f`、`DATE=2026-09-01T10:26:06Z`）：

```text
fe20f013e87568a40f98b4e33cb741f33aae876fa20d8f3b8a23dbd4b83a8e9b  eg_darwin_amd64
622fad2c975fbdc5ed55afb9dea09f49408e43dc58be229cf4a5bfe3ce147269  eg_darwin_arm64
62a2bf12a673c0bb91ca8a365c1315137f02305ea680758bb2e0fe3a49a25e4c  eg_linux_amd64
1b41327da5f87f458aa1a2eb7081311ecbaa4e2e0218394081b9fcd55159e24a  eg_linux_arm64
```

  校验步骤本身与该快照无关：使用者应校验**自己拿到的那份** `dist/SHA256SUMS`（`make release` 会一并生成），而不是照抄上表数字。若需字节可复现的产物，须先固定 `COMMIT` 与 `DATE`（例如 `make release DATE=1970-01-01T00:00:00Z`），本阶段**不做**可复现构建承诺。

---

## 5. 本文件不做的事（越界登记）

- 不新建远端仓库、不推送任何 tag / 分支（无远端信息，不得臆造）。
- 不改任何命令的行为、参数、退出码与输出格式；不改 `PLATFORMS` 列表、`release` 目标结构与 `lint` 的写路径守卫。
- 不做 CI / 流水线 / 发布自动化 / 代码签名 / notarization / Homebrew 分发。
- 不在 macOS 真机上验证 darwin 产物（沙箱内无该环境），**不承诺已验证**。
- 不承诺可复现构建（byte-for-byte reproducible build）。
