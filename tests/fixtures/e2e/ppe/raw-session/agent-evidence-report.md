# Evergreen M2 真实 coding-agent 端到端验证 —— 最终证据报告

> 本文件为**最终唯一结论性报告**，取代此前的 `BLOCKER_REPORT.md`（Phase 1 阻塞报告，已归档至 `archive/BLOCKER_REPORT.phase1.md` 备查）。
> 详细语义分析与逐步过程见 `REPORT.md`；本文件负责结论、身份、时间、人工干预与证据核对数值。
> **本次收尾未重跑任何 `eg` 写入命令，未修改任何语义结果**（`plan.json` / `apply` / `rel add` / 卡片内容全部为原始产物）。

## 0. 结论

**PASS —— 验证已完整真实执行并通过。**

主链路 `capture → context → 只读复核 → 逐卡语义判断 → 从零生成 ChangePlan → apply 写入 → rel add → 写入后复查` 全程使用本地真实构建的 `eg 0.2.0-m2`，无任何伪造、无任何自造替代实现。

---

## 1. 执行身份与时间（UTC）

| 项 | 值 |
|---|---|
| **真实 Agent Harness 会话 ID** | `56cc0392-ffa1-41ee-9b89-21d7ae5c0199` |
| 会话工作目录 | `/workspace/iris_56cc0392-ffa1-41ee-9b89-21d7ae5c0199` |
| 执行者 | Agent Harness（用户 `maintainer`） |
| **全流程起（UTC）** | `2026-09-01T14:54:40Z`（Phase 1 环境排查开始） |
| **全流程止（UTC）** | `2026-09-01T15:23:14Z`（Phase 3 收尾核对与打包完成） |
| Phase 1 环境排查（结论：阻塞） | `2026-09-01T14:54:40Z` → `2026-09-01T15:00:12Z` |
| Phase 2 人工干预后续跑（主验证） | `2026-09-01T15:03:44Z` → `2026-09-01T15:15:41Z` |
| Phase 3 收尾核对（只读）+ 打包 | `2026-09-01T15:15:41Z` → `2026-09-01T15:23:14Z` |
| 命令日志首/末条时间戳 | `[UTC 2026-09-01T14:58:28Z]` / `[UTC 2026-09-01T15:15:01Z]` |
| 构建产物版本串 | `eg 0.2.0-m2 (commit unknown, built 2026-09-01T15:04:47Z, go1.24.13 X:cacheprog,testenhance)` |

---

## 2. 全部人工干预（穷尽登记，共 1 次）

### 人工干预 #1 —— 补充 Evergreen 源码包（唯一一次）

| 项 | 内容 |
|---|---|
| UTC 时间 | `2026-09-01T15:03:44Z`（下载动作时间） |
| 触发原因 | Phase 1 环境中不存在 Evergreen 源码，验证在第 0 步被硬阻塞，已如实出具阻塞报告后由用户补料 |
| 形式 | 用户提供 tar.gz 下载地址 + SHA-256 校验值 |
| 提供内容 | `source/`（源码）、`SKILL.md`（同版本规程）、`seed-k-20260918-scaling-compute.md`（会话前既有卡）、`article.txt`（正文备用副本） |
| **明确未提供** | `plan.json` / ChangePlan 模板 / op 组合 / 参数修正 / 命令行修正 / 收敛判断结论或倾向 |
| 性质判定 | **仅补充缺失资源**。ChangePlan 由 Agent 从零独立生成；三维度语义判断、`relation` 取值、op 组合、关系类型与方向均为 Agent 自主决策 |
| 校验结果 | `sha256sum -c` → `input.tar.gz: OK`（exit 0），实测 `448a8a8156002e1a7d2ff26c129728b61caf775be362c374a49ff7672b0ee735` 与给定值完全一致 |

### 其他需登记的过程性事项（非人工干预）

| # | 事件 | 处置 | 是否人工介入 |
|---|---|---|---|
| 1 | 首次 `curl` 抓文章（未显式传 proxy）→ rc=28 超时 | Agent 自行诊断为需显式 `--proxy "$http_proxy"` | 否 |
| 2 | 改用 `https://` → rc=60 自签名证书错误 | Agent 自行判定站点仅 http 可用 | 否 |
| 3 | 探针脚本内抓取 → rc=56 连接重置（代理抖动） | Agent 自行**重试 1 次**即 HTTP 200 | 否 |
| 4 | `make test` → rc=2（4 个 skill 契约用例读不到包外 `../teamwork/**` 规格文档） | 如实记录为打包缺失，**未修改源码、未跳过用例** | 否 |
| 5 | 包内 `vault/domains/ai-infra/cards/` 路径与源码布局不符 | Agent 依 `internal/store/layout.go` 自行改用 `domains/<d>/knowledge/` | 否 |
| 6 | 既有卡 `sources: []` 与 `create_card` 强制非空 `sources[]` 冲突 | Agent 自行判定该卡属「会话前既有状态」，放在 `eg init` 之前由首个 commit 收入，**未虚构材料来源** | 否 |

> **补参数唯一一处**：`curl --proxy "$http_proxy"`，属必要环境适配，非为掩盖失败而放宽条件。
> **零人工编辑**：`plan.json` 自生成后未被任何人（含 Agent 自身）事后编辑；`apply` 一次通过，未出现「退 2 → 改 plan 重投」。

---

## 3. 收尾核对项 ①：五步链路逐条退出码

均从既有 `command_log.txt` 原始记录提取，**本次未重跑**。

| # | 步骤 | 命令语义 | UTC | 退出码 |
|---|---|---|---|---|
| ① | `eg capture` | 收录真实文章（正文为 Agent 自行抓取清洗的 `body.txt`） | `15:08:01Z` | **0** |
| ② | `eg context --source` | 取候选卡与 `base` | `15:08:15Z` | **0** |
| ③ | `eg apply --dry-run` | 校验 + 展开，零写入零 commit | `15:13:38Z` | **0** |
| ④ | `eg apply --plan`（正式） | 唯一程序化写入通道 | `15:13:54Z` | **0** |
| ⑤ | `eg rel add` | 按语义判断新增论证关系 `limits`（新卡 → 既有卡） | `15:14:07Z` | **0** |

**五步链路全 0，无一步失败、无一步重试。**

全会话带退出码步骤统计（`grep -c '^EXIT_CODE=' command_log.txt`）：

| 项 | 数值 |
|---|---|
| 带退出码步骤总数 | **41 条** |
| `EXIT_CODE=0` | **38 条** |
| 非零 | **3 条**：`rc=56`×1（代理抖动，重试即成功）、`rc=2`×2（Phase 1 `ls /workspace/evergreen` 阻塞取证 + `make test` 包外文档缺失） |

> 勘误：`REPORT.md` §1 写作时统计为 40 条 / 37 条 0；后续补录了 1 条 `rc=0` 记录，**最终准确值以本表 41 / 38 / 3 为准**，非零条数与性质不变。

---

## 4. 收尾核对项 ②：plan.json SHA-256

| 项 | 值 |
|---|---|
| 文件 | `/workspace/agent-e2e-evidence/plan.json` |
| **SHA-256** | `990e9685b05eadb0680b40ff3b303315c41db5ab3260e0064b89adb0950d3c05` |
| 大小 | 10398 bytes |
| 顶层键 | **恰 8 键**：`plan_version` / `verb` / `domain` / `reason` / `requirement_ids` / `convergence` / `base` / `ops` |
| `verb` / `domain` | `process` / `ai-infra` |
| ops 序列（4 个） | `write_note` → `create_card` → `add_material_rel` → `add_open_question` |
| `convergence[]` | 1 条，针对 `k-20260918-scaling-compute`：`relation=independent_new`，`core_knowledge=different` / `conditions=different` / `reuse_purpose=different` |
| `base`（逐字取自本次 `eg context`） | `unprocessed.md = sha256:c326eb23a65184c02f657417ea0357131ee927b8411a0424d0df2de2944ced20` |

该文件为**未经人工编辑的原件**，与 `apply` 实际消费的输入完全同一份。

---

## 5. 收尾核对项 ③：git log 旧 → 新 动词序列

`git -C /workspace/eg-vault-m2 log --reverse`：

| 序 | commit | 动词 | 产生者 |
|---|---|---|---|
| 1 | `aaef516` | **init**(ai-infra) | `eg init` |
| 2 | `92de348` | **capture**(ai-infra) | `eg capture` |
| 3 | `9e82cb3` | **process**(ai-infra) | `eg apply --plan` |
| 4 | `cdd3081` | **relate**(ai-infra) | `eg rel add` |

**序列 = `init → capture → process → relate`，恰 4 个 commit，全部由 eg 自身产生；Agent 全程未执行任何 `git commit`（遵守 B-01）。**

附加只读校验：

| 校验 | 结果 |
|---|---|
| `git status --porcelain` | **空**（工作区干净，无绕过 `apply` 的手工改动） |
| 既有卡涉及的 commit（`git log -- .../k-20260918-scaling-compute.md`） | **仅 `aaef516 init`** → 该卡自入库后从未被任何写入操作改动 |
| 既有卡当前内容 sha256 | `b4c93070780b0f03ddcbbfd993dbe09c07d51cfd09d1476891432e186ada42ee`，与 `eg context` 记录的 `base` hash **逐字一致** |
| `cmp input/evergreen-agent-e2e-input/seed-k-20260918-scaling-compute.md` vs vault 内既有卡 | **rc=0，逐字节相同** → 既有卡全程零改动（遵守 B-03） |

---

## 6. 收尾核对项 ④：写入前后同一查询的关键差异

三组查询命令**完全相同**，仅执行时点不同（`apply`/`rel add` 前 vs 后）；完整原始输出见 `query_BEFORE_*.txt` / `query_AFTER_*.txt`。

| 查询 | 写入前 | 写入后 | 关键差异 |
|---|---|---|---|
| `eg search 自验证` | `total=0`，`hits: []`，扫描 1 个 `.md` | `total=1` → `k-20260901-verification-principle`（score 5），扫描 2 个 | **0 → 1**：新卡被检索到，证明写入生效 |
| `eg search 算力` | `total=1` → `k-20260918-scaling-compute`（score 4），扫描 1 个 | `total=1` → **仍仅** `k-20260918-scaling-compute`（score 4），扫描 2 个 | **1 → 1**：新卡零命中，**负向印证** `core_knowledge: different` 判断成立 |
| `eg rel k-20260918-scaling-compute` | 正向 0 / **反向 0** | 正向 0 / **反向 1**：`k-20260901-verification-principle --limits--> k-20260918-scaling-compute`（含完整 reason） | **反向 0 → 1**：关系按语义判断成功建立，且方向为新卡 → 既有卡 |

三组查询均 `exit_code=0`、`ok=true`、只读零 commit；`scanned_files` 由 1 → 2，与「新增恰 1 张卡」一致。

---

## 7. 写入结果核心数值

| 项 | 值 |
|---|---|
| `apply` commit | `9e82cb3f25dfdef2101c91bebf01fc3cef4c9a1b`（rc=0） |
| `rel add` commit | `cdd3081beaac7543070a7944dd0cdeb12f4c8ee1`（rc=0） |
| 新建知识卡 | `k-20260901-verification-principle`（1 张） |
| 新建材料笔记 | `n-20260901-verification-the-key-to-ai` |
| 材料源 | `s-20260901-verification-the-key-to-ai` |
| `apply` 的 `skipped[]` | **空** |
| `high_impact` | `new_core_card` |
| `coverage_gaps` | `["counterexample", "limitation"]`（仅登记原文确实缺失项，未凑七项） |
| 正文规模 | `body.txt` 4399 字符 / **756 词**，与包内 `article.txt` 词级 diff **差异 0** |
| 关键文件 sha256 | `body.txt`=`ce0be92a002fe9db4b7c863f84f1874ce6b8dd89a39a4fd30cec4866bbbb65d3`；`raw/keytoai.html`=`bf97cf9422d18dd3324027b78ca6cee322ac1c0339f8a6df7d28e17f3621a47f`；`command_log.txt`=`224ea95d39716f9160107e73f0bb5bf709143424fee53235edb3ecbc0d72ca2d`；`REPORT.md`=`05d15b14c1b0b86b9d19e315bce76a852fe1f8579200fc48318c2a8506ca2ad6` |

---

## 8. Phase 1 阻塞事实（原 BLOCKER_REPORT.md 摘要，已解除）

Phase 1 结论为 **BLOCKED，未执行任何验证步骤，未伪造任何产物**。能力矩阵中 4 项具备、1 项缺失且为唯一门禁项：

| 能力 | 状态 |
|---|---|
| Shell（bash 5.2.15 / Linux x86_64） | ✅ |
| Go 工具链（go1.24.13，GOPROXY 可达） | ✅ |
| git 2.39.5（且成功 clone 公司 Codebase 仓库） | ✅ |
| 网络抓取目标文章（需显式代理 + 1 次重试） | ✅ `HTTP 200 bytes:5482` |
| **Evergreen 源码** | ❌ **缺失 —— 唯一阻塞原因** |

源码 6 项独立排查全部未命中：`/workspace/evergreen` 不存在（rc=2）、全盘无 `INSTALL.md`、全盘无含 `changeplan|eg vault|eg capture` 的 `SKILL.md`、PATH 无 `eg`、Codebase 4 个同名仓库全不相关（`mengyantong/evergreen` 实为 Kafka 聚合器，已实际 clone 验证）、空间知识库检索无结果。

该阻塞由**人工干预 #1** 解除，随后 Phase 2 完整跑通。原始 Phase 1 报告完整保留于 `archive/BLOCKER_REPORT.phase1.md`。

---

## 9. 判定结论

| 验收要求 | 结果 |
|---|---|
| 按 INSTALL.md 构建真实 `eg 0.2.0-m2` | ✅ |
| 全新隔离 vault + 预置既有卡 | ✅ `/workspace/eg-vault-m2`，既有卡由 `init` commit 收入 |
| 只以该版本 SKILL.md 为规程（与源码 `skill/SKILL.md` 字节相同） | ✅ |
| 正文由 Agent 自行抓取清洗 | ✅ 与包内副本逐词一致 |
| 收录 / context / search+card show+rel 复核 / 逐卡语义判断 | ✅ 全 rc=0 |
| **从零生成 ChangePlan**，未用现成模板 | ✅ sha256 `990e9685…d3c05` |
| `apply` 写入 + 按判断新增关系 + 写入后再查询验证 | ✅ 两个 commit，三组前后对比差异明确 |
| 未手工改 vault Markdown、未自行 git commit | ✅ `git status` 空；4 commit 全由 eg 产生 |
| 完整记录 UTC / 命令 / 退出码 / plan 原件 / 前后输出 / git log | ✅ 见本目录 |
| 重试、补参数、人工干预原样记录 | ✅ 见 §2 |

**最终判定：PASS。**

---

## 10. 文件清单

| 文件 | 说明 |
|---|---|
| `EVIDENCE_REPORT.md` | **本报告（最终结论性报告，取代 BLOCKER_REPORT.md）** |
| `REPORT.md` | Phase 2 详细过程与语义分析报告（含 M2 产品发现、SKILL 自检清单） |
| `archive/BLOCKER_REPORT.phase1.md` | Phase 1 阻塞报告原文，归档备查 |
| `command_log.txt` | 全部 **41 条**命令 + 完整输出 + 退出码 + UTC 时间戳（含所有重试与补记） |
| `plan.json` | **未经人工编辑的 ChangePlan 原件** |
| `context_output.json` | `eg context` 完整输出（`base` 与候选卡来源） |
| `apply_output.txt` | `eg apply` 完整输出（逐卡收敛记录 + 全部诊断） |
| `rel_add_output.txt` | `eg rel add` 完整输出（含 W5 产品发现） |
| `git_log.txt` | vault 完整提交历史（`--stat`） |
| `query_BEFORE_*.txt` / `query_AFTER_*.txt` | 写入前后**同一查询**完整输出（search 自验证 / search 算力 / rel / card show） |
| `body.txt` / `raw/keytoai.html` / `raw/keytoai_clean.txt` | Agent 自行抓取的原始 HTML 与清洗正文 |
| `vault-snapshot/` | 写入后 vault 知识产物快照 + `last-report.json` |
| `probe_capabilities.sh` / `runlog.sh` | 能力探针脚本与命令日志包装器 |
| `input.tar.gz` | 用户提供的源码包（SHA-256 已校验 OK） |
| `input/evergreen-agent-e2e-input/` | 源码包解压内容（含 `seed-k-20260918-scaling-compute.md`、`SKILL.md`、`article.txt`），供逐字节比对复现 |
