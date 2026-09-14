---
topic: Evergreen S1/M1 选型后复审（历史评审报告 supersede 说明）
stage: S1
milestone: M-001
kind: post-selection-review
decision: Go
decision_doc: docs/specs/2026-08-31-language-selection.md
baseline_doc: docs/specs/2026-08-31-evergreen-s1-tech-design.md
created: '2026-08-31'
author: 项目维护者
---

# 选型后复审报告：五份评审报告哪些结论被 supersede、哪些仍然有效

## 一、本文件的定位与方法

1. **为什么有这份文件**：2026-08-31 的语言选型决策（结论 **Go**，`docs/specs/2026-08-31-language-selection.md`）在五份 M1 Task 评审报告**之后**产生。为避免读者误以为历史报告的每条结论都仍然照旧生效，本文集中登记 supersede 关系。
2. **历史报告一字未改**：`docs/specs/2026-08-31-m1-task-review.md`、`-round2.md`、`-round3-final.md`、`-round4-final.md`、`-round5-final.md` **五份的既有内容本次完全未动**（既没有改写，也没有在末尾追加），审计方式：本次 teamwork 侧 commit 的 `git show --stat` 只包含 `2026-08-31-evergreen-s1-tech-design.md`（修改）与本文件（新增）两项。
3. **复核范围**：五份报告 + 18 个 Task + `milestones/M-001-m1.md` + `EPIC.md` + `tools/*.py`（8 个门禁脚本）。**本阶段不修改** Task / 里程碑 / EPIC / 门禁脚本，只登记结论与待办。
4. **依据边界**：全部结论只用沙箱内文件与本机实测证明；无法证明的进第六节「未知」。外部飞书链接仅作 provenance，不作为开工依赖。

---

## 二、关键结论：**18 个 Task 的 Go 字段无需变更**

这是本次复审最重要的结论。选型结论（Go）与 18 个 Task 既有的 Go 假设**完全一致**，因此 Task 侧的语言相关字段**零改动**。

### 2.1 实测证据（本次在沙箱内脚本统计，18/18 覆盖）

| 字段 | 实测值 | 与 Go 结论的关系 |
|-|-|-|
| `verify.test` | 18 个 Task 中 **16 个**含 `go test …`（`T-…-002` / `T-…-011` 是 Contract 型任务，只有 `lint`，本就无 `test` 键） | ✅ 无需变更 |
| `verify.lint` | **18/18 逐字相同**：`cd evergreen && gofmt -l . && go vet ./...`（去重后只有 1 种字符串） | ✅ 无需变更（可选增强见基线 §20 A-2） |
| `verify.run` | 8 个 Task 有 `run`，全部形如 `cd evergreen && go run ./cmd/eg …` | ✅ 无需变更 |
| `code_paths` | 共 38 条：**35 条**以 `evergreen/` 开头（代码仓），3 条以 `projects/evergreen/s1_main_flow/docs/specs/` 开头（teamwork 仓，属 Contract 型任务） | ✅ 无需变更；跨仓语义需在文档层声明（§20 A-7） |
| `deliverables` | 共 **30** 条 `path`，含 `evergreen/go.mod`、`evergreen/cmd/eg/main.go`、`evergreen/Makefile`、`github.com/ikaqiu-Lemon/EverGreen/internal/<pkg>/` | ✅ 路径全部与本次新建的脚手架布局一致 |
| `branches` | repo key 去重后只有 **`evergreen`** 一个，值形如 `feature/s1_main_flow/<name>` | ✅ 与新建仓目录名 `evergreen/` 一致 |
| `design_doc` | 9 个不同锚点（`ch3`/`ch4`/`ch6`/`ch7`/`ch75`/`ch8`/`ch9`/`ch13`/`m1`），**全部在更新后的基线中仍然存在**；正文中出现的锚点引用同样 100% 可解析 | ✅ 无需变更 |

### 2.2 门禁复跑证据（选型前后一致）

在 `teamwork/` 仓库根执行 4 个门禁脚本，本次基线文档改动**前后结果相同**：

```
python3 projects/evergreen/s1_main_flow/tools/validate_m1_tasks.py    →  23 checks, 0 failed
python3 projects/evergreen/s1_main_flow/tools/round3_final_gate.py    →  64 checks, 0 failed
python3 projects/evergreen/s1_main_flow/tools/round4_final_gate.py    → 113 checks, 0 failed（P1 0 / P2 0）
python3 projects/evergreen/s1_main_flow/tools/round5_final_gate.py    →  91 checks, 0 failed（P1 0 / P2 0）
合计 291 checks, 0 failed
```

原因：门禁的扫描面是 `tasks/T-*.md` + `milestones/M-001-m1.md` + `EPIC.md`，**不含 `docs/specs/`**；对基线文档的唯一断言是「被引用的锚点真实存在」（`round3` B12 等）与「施工索引含退出码章节」（`round4` I6）。本次**只新增锚点、未删除任何锚点**，也未删除退出码章节，故门禁不受影响。

---

## 三、被 2026-08-31 选型决策 supersede 的条目

> 「supersede」= 结论的**结果**通常仍成立，但其**机制描述 / 前提 / 风险评估**已被选型决策改写，读者不得再按历史报告的字面执行。

| # | 历史报告位置 | 原结论（摘要） | supersede 内容 | 新的权威表述 |
|-|-|-|-|-|
| S-1 | `2026-08-31-m1-task-review.md` 第 250 行（T-005 行）、`-round2.md` 第 294 行 | T-005「mdfile round-trip」**无实质问题**，验收强度「强（字节级相等）」 | 结果（字节级相等）**仍然有效**；但**实现机制被收紧**：不再允许 `Parse → YAML 序列化 → 写回`，只允许「YAML 只读解析 + 字节区间外科式写入」 | 选型文档 §4.1；基线 [`#write-path`](2026-08-31-evergreen-s1-tech-design.md#write-path) §16.2 |
| S-2 | 同上（T-005 的 Scope 引用） | Task Scope 实现要点第 1 条「frontmatter：YAML 解析 / 序列化，保持键顺序」被历史评审默认接受 | **被 supersede**：写路径**禁用** `yaml.Marshal` / `yaml.NewEncoder`；键顺序因**没有序列化步骤**而天然不变，不再是「保持」出来的 | 基线 §16.3；待改字段见基线 §20 **A-5** |
| S-3 | `2026-08-31-m1-task-review.md` P1-5 / 第 311 行；`-round2.md` 附录 P1-5 闭环说明 | 风险：T-001 未被硬依赖 → T-005/006/007 可能在**没有 `go.mod`/`Makefile`** 的情况下进 `integration`，`verify.test` 必然失败 | 该风险的**物理前提已消失**：本次已实际创建 `evergreen/` 仓（`go.mod` + `Makefile` + 9 个 `internal/` 占位包），`go build ./...` / `go test ./...` / `gofmt -l .` 全部通过。历史报告采纳的「补软依赖」修复**仍然保留且仍然有效**，但它已从「阻塞性风险」降级为「顺序保障」 | 本文第二节；基线 [`#eng-baseline`](2026-08-31-evergreen-s1-tech-design.md#eng-baseline) §15.2 |
| S-4 | `2026-08-31-m1-task-review.md` 第 320 行 | 建议：`T-…-001` 与 `T-…-003` 「偏小，可考虑合并」 | **不再建议合并**：选型固化后 `T-…-001` 的范围实际扩大（`Makefile` 增 `fmt` / `release`、四平台交叉编译、写路径 lint 守卫），已不属「偏小」 | 基线 §15.4、§15.5；待改字段见 §20 **A-6** |
| S-5 | 五份报告对 `verify.lint` 的一致口径（`gofmt -l . && go vet ./...` 视为完备 lint 门禁） | lint 门禁完备 | **被补强**：`gofmt` + `go vet` **不足以**拦住「写路径调用 YAML 序列化器」与「对用户内容做 rune/字符串规范化」两类致命失误，需新增 grep 型守卫 | 基线 §16.1（I-2 / I-5）、§16.3；待改字段见 §20 **A-2** |
| S-6 | `2026-08-31-m1-task-review-round4-final.md` P2-3（Obsidian 判据） | 以机器替代判据覆盖「Obsidian 可正常打开」 | 结论**仍然有效**，但需并列登记：沙箱内无 Obsidian，**该项永远只有替代判据**，属长期「未知」（选型文档 U-3） | 基线 §19（沿用 U-3） |
| S-7 | 五份报告均**未评估**跨平台发布形态 | —（空白项，非错误结论） | **新增硬要求**：`make release` 四平台（`linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`）+ `dist/SHA256SUMS`，`CGO_ENABLED=0` | 基线 §15.5；待改字段见 §20 **A-6** |
| S-8 | 五份报告均**未评估** S4 索引选型对二进制形态的反向约束 | —（空白项） | **新增预警**：SQLite 若走 cgo 将破坏静态单二进制，S4 开工前必须先评估 `modernc.org/sqlite` | 基线 §16.6 |

### 3.1 **未被** supersede（明确声明仍然有效）

| 项 | 说明 |
|-|-|
| 全部依赖图与排期结论 | 硬依赖 35 边 + 软依赖 35 边、无环无孤儿、完成链 12 层、单人在途 ≤ 2、M1 收口日 `2026-09-17` —— 与语言无关，**全部有效** |
| 18 个 Task 的规范符合度结论 | 命名 / `id` 与文件名一致 / `status: created` / `owner`·`filer` ∈ roster / 必填字段非空 —— **全部有效** |
| 安全底线 B1–B4 的归属与判据 | B1→006/013、B2→006/013/018、B3→006/010/015/016、B4→007/015 —— **全部有效**；选型只是给 B2/B3 增加了「第二返回值漏接即编译失败」这一额外保护（选型文档 §7 理由三） |
| 六条冻结合同 F1–F6 的覆盖结论 | **全部有效**，与语言无关 |
| §14 追溯矩阵（S1 44 / S2 18 / S3 2 / S4 1 / S5 1 = 66 生效 + 4 Deferred） | **全部有效**（其条文原文属「未知」，见第六节 U-13） |
| 第五轮终审「0 P0 / 0 P1 / 3 P2，允许进入实施」的总结论 | **仍然有效**：选型未引入任何新的 P0/P1 |
| 「不新增 / 不删除 task」的硬约束与 `013`/`018` 不拆分的决定 | **仍然有效** |

---

## 四、本次实际发生的改动（沙箱内，全部相对路径）

| 文件 | 动作 | 内容 |
|-|-|-|
| `teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-08-31-evergreen-s1-tech-design.md` | **修改** | 新增 §0 文件定位（唯一可执行基线声明）、§15 工程基线（Go）、§16 写路径硬约束、§17 本地引用校验、§18 行号引用对照表、§19 未知清单、§20 下一阶段对齐清单；删除 `digest_prd.md` / `digest_design.md` 两条**沙箱内不存在**的引用；frontmatter 增 `baseline_status` / `language` / `language_decision` / `authority_role: provenance-only` 等字段。**既有 §1–§14 内容与全部既有锚点未改** |
| `teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-08-31-post-selection-review.md` | **新增** | 本文件 |
| `evergreen/`（沙箱根，与 `teamwork/` 平级） | **新建代码仓** | `git init -b master`、无远端、无软链接；最小可构建可测试的 Go 脚手架（详见 `evergreen/README.md`） |

**未改动**：18 个 Task、`EPIC.md`、`milestones/M-001-m1.md`、`tools/*.py`、五份评审报告、`teamwork/tools/`、`teamwork/skills/`、`teamwork/AGENTS.md`、`teamwork/INSTALL.md`、`teamwork/README.md`。

---

## 五、下一阶段必须对齐的字段与命令（清单正文见基线 §20）

完整清单（A-1 ~ A-12，精确到字段名与命令字符串）在基线文档的 [`#next-align`](2026-08-31-evergreen-s1-tech-design.md#next-align) 一节。摘要：

- **无需变更**：`branches` 的 repo key `evergreen`、`code_paths` 的 `evergreen/` 前缀、`deliverables` 的 30 条路径、`design_doc` 的 9 个锚点、`verify.test` 的 `go test` 形态、门禁脚本的 `internal/<pkg>/` 断言。
- **需要动**：`verify.lint`（补写路径守卫 / 或统一改为 `cd evergreen && make lint`）、`T-…-005` 的 `verify.test`（补 fuzz 双轨）与 Scope/`deliverables[0].requires`（「序列化」→「只读解析 + 字节区间写入」）、`T-…-001` 的 Makefile 交付物描述（三入口 → 五入口，含 `release`）、`EPIC.md` 的 `repos: []` → `repos: [evergreen]`、`verify.*` 的 CWD 语义声明。

---

## 六、未知（不得猜测）

沿用选型文档第九节 U-1 ~ U-12，并新增（与基线 §19 一致）：

| # | 项 | 状态 |
|-|-|-|
| U-13 | `EG-*` 需求编号的条文原文与 66 条追溯矩阵原文（`digest_prd.md` / `digest_design.md` 不在沙箱内） | **未知** |
| U-14 | `evergreen` 仓最终托管路径 / 远端 / 模块域名（本次不配置远端，模块名暂定 `evergreen`） | **未知** |
| U-15 | `evergreen` 仓的 CI 形态与发布渠道（沙箱内无 CI 配置） | **未知** |
| U-16 | `teamwork/` 的 37 个本地提交（+ 本次新增 1 个）何时、由谁推送 | **未知** |
| U-17 | `gopkg.in/yaml.v3` 在本沙箱能否成功下载入库（脚手架零依赖，未执行 `go get`） | **未知** |
| U-18 | 五份历史评审报告的作者是否同意本文的 supersede 判定（沙箱内无评审人反馈渠道） | **未知** |

---

*本报告由 项目维护者 于 2026-08-31 撰写。历史评审报告内容一字未改；supersede 关系集中登记于此。*
