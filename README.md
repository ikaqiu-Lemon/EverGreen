# Evergreen `eg`

Evergreen 的**确定性本地 CLI**（单二进制 `eg`）代码仓。`eg` 只做本地确定性的数据面工作：**不调用任何模型、不做任何网络请求**；抓网页、清洗正文、生成 ChangePlan 全部在 Agent 侧。

> **当前状态：脚手架阶段。** 只实现 `eg --help` 与 `eg version`；S1 的九个命令尚未实现（见下文「与 teamwork 的关系」）。

## 快速开始

```bash
make build          # 编译到 bin/eg
./bin/eg --help
./bin/eg version
make test           # go test ./...
make lint           # gofmt -l . + go vet ./... + 写路径守卫
make fmt            # gofmt -w .
make release        # CGO_ENABLED=0 交叉编译四平台到 dist/ + SHA256SUMS
make clean          # 删除 bin/ dist/
```

等价的原生命令（CWD = 本目录）：

```bash
go build -o bin/eg ./cmd/eg
go test ./...
gofmt -l .          # 输出必须为空
go vet ./...
go run ./cmd/eg --help
```

## 工程基线

| 项 | 值 |
|-|-|
| 语言 | **Go**（唯一实现语言，2026-08-31 选型决策结论） |
| 模块名 | `evergreen`（import 形如 `evergreen/internal/version`） |
| `go.mod` 的 `go` 指令 | `1.22`（任务基线下限；本地实测工具链 `go1.24.13 linux/amd64`） |
| 外部依赖 | 脚手架阶段**零依赖**（仅 stdlib）。S1 唯一允许的外部依赖是 `gopkg.in/yaml.v3`，且**只读用途** |
| 主分支 | `master`，**未配置远端** |
| 退出码 | `0` 成功；`1` 用法 / 参数非法（零写入）。`2`/`3`/`4` 由后续命令任务引入 |

## 目录结构

```text
cmd/eg/            单二进制 eg 的唯一入口（当前仅 --help / version）
internal/version/  版本信息（-ldflags 注入）
internal/model/    产物结构体、ID、枚举、时间格式          [S1] 零依赖
internal/mdfile/   frontmatter 只读解析、分区/块切分        [S1] → model
internal/store/    读盘、写盘（tmp+fsync+rename）、hash 比对 [S1] → model mdfile git
internal/git/      init/add/commit/status/diff             [S1] → model
internal/plan/     ChangePlan schema 与 op 展开             [S1] → model rules query store
internal/report/   最终报告 schema 与渲染                   [S1] → model query
internal/cli/      命令定义、参数、退出码、--json           [S1] → 以上全部
internal/query/    检索与关系正反查（S1 扫描版）            [S1] → model
internal/rules/    opposing 规范化、收敛判据                [S1] → model query
skill/             SKILL.md（随二进制发布，由后续任务填写）
test/e2e/          端到端语料与脚本（由后续任务填写）
bin/ dist/         构建 / 发布产物（已 gitignore）
```

`internal/index/`（S4）、`internal/proposal/`（S2）、`internal/reconcile/`（S3）**本阶段不创建**。每个包的 `doc.go` 首行以 `// [S1] …` 声明首次落地阶段与允许依赖，依赖方向写在代码里。

## 写路径硬约束（务必先读）

Markdown 与 frontmatter 是**唯一真相**，用户手写内容不可再生，因此：

1. **写路径禁止 YAML 序列化**：不得使用 `yaml.Marshal` / `yaml.NewEncoder`。YAML 库只用于**只读解析**。`make lint` 内置 grep 守卫，违反即构建失败。
2. **只读解析 + 字节区间写入**：在原始 `[]byte` 上建半开区间索引（`Doc` / `Span`），写入只在目标分区末尾插入字节，其余区间（含未知分区、未知字段、未知块）以 `Raw[a:b]` 原样拼接；缩进探测既有风格并沿用。
3. **写前字节自检**：写盘前先 `Parse → Render` 与原字节比对，不等即**拒写**并进报告，宁可少写、不可错写。
4. **`[]byte` 全链路**：禁止对用户内容做 `for range string` / `strings.*` 规范化。
5. **两条 fuzz 属性**：`FuzzRoundTrip`、`FuzzAppendPreserves`（Go 原生 fuzz，语料沉淀在 `internal/mdfile/testdata/fuzz/`，**入库**）。

## 与 `teamwork/` 的关系

本仓与计划仓 `teamwork/` **同级、互不嵌套、互不 `git add`**，且**不使用任何软链接**：

```text
<沙箱根>/
  teamwork/     计划与文档：Task / 里程碑 / spec / 门禁脚本
  evergreen/    代码（本仓）
```

- 施工基线（唯一可执行基线）：`../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-08-31-evergreen-s1-tech-design.md`
  - 工程基线：`#eng-baseline`　｜　写路径硬约束：`#write-path`　｜　下一阶段对齐清单：`#next-align`
- 语言选型决策（结论 Go）：`../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-08-31-language-selection.md`
- 选型后复审：`../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-08-31-post-selection-review.md`
- 任务清单：`../teamwork/projects/evergreen/s1_main_flow/tasks/`（18 个 Task，其 `verify.*` 命令形如 `cd evergreen && go test ./...`，**CWD 是本仓与 `teamwork/` 的公共父目录**）

未知事项（不猜测）：本仓最终托管地址 / 远端、CI 形态、`gopkg.in/yaml.v3` 在当前环境能否下载入库，均**未验证**。
