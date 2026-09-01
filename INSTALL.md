# 安装与首次上手（`eg`）

面向「没读过本仓」的使用者：照本文件就能装上并跑通 M2 的 `eg`。

- 版本号与发布口径的唯一决策文档：[`2026-10-03-m2-release-and-version.md`](../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-10-03-m2-release-and-version.md)
- 当前版本：**`0.2.0-m2`**（`Makefile` 的 `VERSION ?=` 缺省值、`internal/version.DefaultVersion`、决策文档三处同源同值）
- 前置条件：Go 工具链（`go.mod` 的 `go` 指令为 `1.22`，本地实测 `go1.24.13 linux/amd64`）、`git`、`make`、`sha256sum`（或 macOS 的 `shasum -a 256`）
- 本仓**未配置远端**（`git remote -v` 实测为空输出）。获取方式：直接使用本地目录副本 / 由持有方分发归档；无远端仓库时**不存在** `clone` 步骤，请按下节从源码构建。

> 命令里的 CWD 一律是本仓根目录（含 `Makefile` 的那一层），除显式写出的 `cd` 之外不切目录。

## 1. 从源码构建

```bash
make build
./bin/eg --version
```

- `make build` 走 `CGO_ENABLED=0 go build -trimpath -ldflags '…'`，产出单二进制 `bin/eg`。
- `./bin/eg --version` 输出**版本号 + commit + 构建时间**三段，例如 `eg 0.2.0-m2 (commit <sha>, built <RFC3339>, go1.24.13)`。
- **注意**：`eg` 没有 `version` 子命令；打印版本的唯一形式是全局 flag `--version`（写成子命令会得到「未知命令」并退 `1`）。

自检（与 CI 口径一致）：

```bash
make test
make lint
```

## 2. 用 `dist/` 预编译产物

`make release` 用 `CGO_ENABLED=0` 交叉编译四平台并生成 `SHA256SUMS`：

```bash
make release
cd dist
sha256sum -c SHA256SUMS
cd ..
mkdir -p bin
cp "dist/eg_$(go env GOOS)_$(go env GOARCH)" bin/eg
chmod +x bin/eg
./bin/eg --version
```

- 四个产物固定为 `dist/eg_linux_amd64`、`dist/eg_linux_arm64`、`dist/eg_darwin_amd64`、`dist/eg_darwin_arm64`。
- `sha256sum -c SHA256SUMS` 必须四行全部 `OK`（macOS 上等价命令为 `shasum -a 256 -c SHA256SUMS`）。
- `make release` 会先执行 `clean`（删 `bin/` 与 `dist/`），因此上面先 `mkdir -p bin` 再拷回本机平台的产物。
- 二进制**零外部运行时依赖**（静态、无 CGO），拷到同架构机器上即可直接执行。

## 3. 首次上手

```bash
VAULT="${VAULT:-$HOME/notes/evergreen}"
mkdir -p "$VAULT"
export PATH="$PWD/bin:$PATH"
eg --vault "$VAULT" init --domain ai-infra
eg --vault "$VAULT" config set default_domain ai-infra
printf '正文：Transformer 用并行注意力替代递归，降低了长序列训练成本。\n' > "$VAULT/article.txt"
eg --vault "$VAULT" capture --url https://example.com/attention --title "注意力机制综述" --body-file "$VAULT/article.txt" --reason "首次上手冒烟"
eg --vault "$VAULT" search 注意力
```

- 第 1 行：把 `VAULT` 换成你自己的 vault 目录（已设置该环境变量时沿用既有值）。
- `eg init`：在 vault 根写 `evergreen.yml` / `SKILL.md` / `unprocessed.md` 等并**初始化 Git 仓 + 一次 commit**（verb=`init`）；`SKILL.md` 与二进制内嵌副本**字节相同**。
- `eg config set default_domain <d>`：CLI **绝不自选默认领域**，未配置时除 `init` / `config` 外一律提示并退 `1`。
- `eg capture`：收录原文并登记收件区，产生一次 commit（verb=`capture`）。
- `eg search <query>`：只读检索；零命中也合法（`hits` 为空、`total = 0`，仍退 `0`）。
- 后续「读上下文 → 语义加工 → 写入」由 coding agent 按 vault 根的 `SKILL.md`（即 [`skill/SKILL.md`](skill/SKILL.md)）执行：`eg context` → 生成 ChangePlan → `eg apply --plan`（**唯一程序化写入通道**）→ `eg report --last`。

### `.eg/` 状态目录与 vault 的关系

- vault 是**知识产物**目录：`domains/**`（知识卡与材料笔记）、`sources/**`（原文）、`unprocessed.md`（收件区）、`evergreen.yml`、`SKILL.md`，全部入 Git。
- `.eg/` 是 `eg` 自己的**工程状态**目录（当前只有 `.eg/last-report.json`，供 `eg report --last` 逐字复现最近一次 `apply`）。它**不是**知识产物：不落在 `domains/**` / `sources/**` 之下，且已被登记进仓库本地忽略清单（`.git/info/exclude`），因此不进 commit、也不污染工作区。
- 删掉 `.eg/` 只会让 `eg report --last` 退 `1`（提示先跑一次 `eg apply`），不影响任何知识产物。

## 4. 已知限制（如实登记，未验证的一律不声称已验证）

| # | 限制 | 现状结论 |
|-|-|-|
| 1 | `dist/eg_darwin_amd64` 与 `dist/eg_darwin_arm64` | **仅交叉编译产出，未经真机运行验证**：构建环境为 Linux，沙箱内无 macOS 运行环境，darwin 两个平台的运行验证结论为「**未做**」。已记录的事实只有「交叉编译成功 + SHA256 校验和」。 |
| 2 | `linux/amd64` | 已在本机（`linux/amd64`）实跑 `--version` 与全套 e2e，结论为已验证。 |
| 3 | `linux/arm64` | 仅交叉编译产出，本机架构不匹配，运行验证「未做」。 |
| 4 | 远端仓库与发布 tag | 本地 `git remote -v` 与 `git tag` 实测均为**空输出**：无远端、无 tag。是否创建 / 推送发布 tag 属组织决策，本地无任何证据 → **未知 / 待确认**（详见版本决策文档「远端状态」一节）。 |
| 5 | `eg rel remove` | 按阶段裁决归 **M3/S2**，M2 不实现：被调用即退 `1`，零写入、零 commit。 |
| 6 | 检索实现 | M2 为**全库扫描版**，不建 `.index/`、不用 SQLite / FTS5，不设性能门槛。 |

> 反向说明：本文件与 `README.md` 均**不**声称 darwin 产物通过了 macOS 真机验证；如需该结论，请在真机上执行第 2 节的校验与 `./eg_darwin_* --version` 后另行登记。
