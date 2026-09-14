---
topic: 常青（Evergreen）M1 Task 评审结果
stage: S1
milestone: M-001
reviewer: 项目维护者
review_date: '2026-08-31'
scope: projects/evergreen/s1_main_flow/tasks/*.md（18 个）+ projects/evergreen/backlog/tasks/（空）
authority: 飞书《[技术方案]常青（Evergreen）v1 技术方案》（只读）
authority_url: https://docs.example.invalid/evergreen/design
review_doc_url: https://docs.example.invalid/evergreen/review
verdict: 有条件通过（4 条 P0 未修复前 T014/T015/T016/T018 不得进入 ready）
---

# [评审]常青（Evergreen）M1 Task 评审结果

> **总体判定：有条件通过。** 规范层 18/18 全合格、依赖图无环无孤儿、Acceptance 客观化程度高；但存在 **4 条 P0 阻塞**，其中 1 条直接违背冻结合同 F4（关系类型集合），照此实现会产出必须迁移的脏数据。**T014 / T015 / T016 / T018 四份文件在 P0 修复前不得进入 `ready`**，其余 14 个 task 可立即开工。需求追溯缺口 4 条 EG-ID，另有 5 个引用了根本不存在的 `EG-REL-*` 编号。

---

## 一、评审范围与依据

### 1.1 评审对象

| 对象 | 路径 | 数量 | 说明 |
| --- | --- | --- | --- |
| M1 Task 文件 | `projects/evergreen/s1_main_flow/tasks/T-evergreen.s1_main_flow-158614-{001..018}-*.md` | 18 | 全部纳入评审 |
| 里程碑 | `projects/evergreen/s1_main_flow/milestones/M-001-m1.md` | 1 | 作为边界基准交叉核对 |
| 施工索引 | `projects/evergreen/s1_main_flow/docs/specs/2026-08-31-evergreen-s1-tech-design.md` | 142 行 | 全部 task 的 `design_doc:` 锚点目标 |
| 另一 epic | `projects/evergreen/backlog/tasks/` | **0** | 目录存在但无 task 文件，无需评审 |

### 1.2 评审依据

| 依据 | 来源 | 用法 |
| --- | --- | --- |
| 技术方案（权威、只读） | 飞书 wiki `LB73w54iOiXN96keHVLcK38JnSe`，revision 124，1715 行 / 80182 字 | 主判据：§1.1 阶段模型、§1.2 六条冻结合同、§3–4 目录与数据合同、§4.5.1 校验分级、§4.6 报告 schema、§7.1 命令表、§7.5 capture 合同、§8 主链路、§9.1 四条安全底线、§13 模块划分、§14 追溯矩阵（S1 44 条）、§16.1 M1/M2 判据 |
| 需求摘要 | `digest_prd.md`（66 条生效 + 4 条 Deferred）、`digest_design.md` | 校验 EG-ID 语义 |
| 仓库规范 | `teamwork/AGENTS.md`、`skills/teamwork-file-task/SKILL.md`、`skills/teamwork-plan-breakdown/SKILL.md`、`projects/evergreen/AGENTS.md`、`s1_main_flow/AGENTS.md` | 规范符合度判据 |
| 仓库 CLI 交叉验证 | `teamwork status`、`teamwork pmo-sweep --dry-run`、`teamwork.deps`、`teamwork.entity_io` | 依赖图与 deliverables 健康度 |

技术方案本次仅通过 `lark-cli docs +fetch` 抓取到本地后分段读取，**未做任何写操作**。抓取结果与仓库外已有副本逐字节一致，可确认评审基线未漂移。

---

## 二、总体结论

| 维度 | 结论 | 关键证据 |
| --- | --- | --- |
| 规范符合度 | ✅ 通过（18/18） | 命名、14 个必填 frontmatter 字段、正文四段、30 条 `{path, requires}` deliverable、18 个 `design_doc` 锚点全部有效；`(fill in)` 占位残留 0 处 |
| 技术方案覆盖度 | 🟡 有条件通过 | §16.1 M1 交付 9 项 + 7 包全部有归属；7 个 op 全覆盖；9 个命令中 `search`/`card show`/`rel` 仅有合同、无实现任务（属 §16.1 的 M2，但阶段标签写错） |
| 需求追溯 | 🔴 不通过 | S1 44 条中 40 条被引用（90.9%），4 条 0 引用；另有 5 个不存在的 `EG-REL-*`、3 处 ID 误挂、3 处越界引用非 S1 需求 |
| 阶段边界 | 🔴 不通过 | 未把 S2–S5 能力实现进 M1（这点很干净），但把 §4.5.1 的 **W1 warning 当成 error** 实现，等于提前执行 S5 的严格化；四条安全底线 B1–B4 齐备 |
| 可验收性 | ✅ 基本通过 | 149 条 Acceptance 中绝大多数带断言（字节比对 / 退出码 / `git log` 条数 / grep 反证）；3 条依赖人工或外部资源，2 条存在到期日不可判定的反向依赖 |
| 依赖图健康度 | 🟡 有条件通过 | 无环、无孤儿、无自依赖、依赖 ID 全部可解析；但硬依赖建模缺失（T001 未被任何任务硬依赖）、Contract/Implementation 软依赖漏 6 处、排期与依赖方向冲突 4 处 |
| 粒度与工作量 | 🔴 不通过 | 18 个 task 全部 `owner: ikaqiu`，9 个日历日内 4 天各有 3 个任务同日开工，而 hard+soft 完成链长 13 层——排期不可执行 |

**判定口径**：P0 = 照此实现会产出与冻结合同或权威合同冲突的结果，必须先改文档；P1 = 会导致漏做需求、门禁失效或排期不可执行；P2 = 表述与一致性问题，不阻塞开工。

问题总量：**P0 4 条 / P1 10 条 / P2 8 条**。

---

## 三、规范符合度检查表

评审对象为 18 个 task 文件的全部字段与正文段落，逐项机器校验（脚本 + `teamwork.entity_io`）。

| # | 检查项 | 判据来源 | 结果 | 证据 |
| --- | --- | --- | --- | --- |
| 1 | 文件命名 `T-evergreen.s1_main_flow-158614-NNN-<slug>.md` | `teamwork-file-task/SKILL.md` | ✅ 18/18 | 序号 001–018 连续无缺号无重号 |
| 2 | frontmatter 必填字段（`id`/`title`/`filer`/`owner`/`status`/`created`/`branches`/`verify`/`code_paths`/`deliverables`/`design_doc`/`depends_on`/`soft_depends_on`/`known_issues`） | `file_task.py` 模板 | ✅ 18/18 | 无缺字段 |
| 3 | 正文四段（Definition of Ready / Scope / Acceptance / Activity Log） | `teamwork-file-task/SKILL.md` | ✅ 18/18 | 段落标题逐字一致 |
| 4 | `deliverables` 结构为 `{path, requires}` 列表且非空 | `AGENTS.md`「Closing a Task」 | ✅ 18/18，共 30 条 | 其中 3 条是 spec 文档（T002 / T011 / T018），27 条是代码路径 |
| 5 | `design_doc` 目标文件存在且锚点可解析 | epic 约定 | ✅ 18/18 | 锚点分布：`#ch4`×5、`#ch7`×2、`#ch9`×3、`#ch13`/`#ch3`/`#ch75`/`#ch6`/`#m1`、`#ch8`×2 |
| 6 | `(fill in)` / 模板占位残留 | `teamwork-file-task/SKILL.md` | ✅ 0 处 | 全库 grep 无命中 |
| 7 | 依赖 ID 可解析、无自依赖、无环 | `AGENTS.md`「Dependency semantics」 | ✅ 通过 | 39 条硬依赖 + 30 条软依赖全部指向本 epic 内实存 task |
| 8 | `depends_on` / `soft_depends_on` 语义与 DoR 表述一致 | `AGENTS.md` | 🔴 3 类不符 | T002 DoR 声明硬依赖 001 但 `depends_on: []`（T002:22-23）；T001 未被任何任务硬依赖；Contract/Implementation 软依赖漏 6 处 |
| 9 | 需求 ID 真实存在且阶段正确 | 技术方案 §14 | 🔴 12 处问题 | 5 个不存在（`EG-REL-01/02/03/04/06`）+ 4 处语义误挂（`EG-CHK-02/04/05` 于 T014、`EG-CHK-03` 于 T015）+ 3 处越界（`EG-AGT-01`/`EG-EDIT-04`/`EG-EDIT-06`） |
| 10 | `deliverables` 产物是否已存在 | `teamwork.deps.missing_deliverables` | 🟡 30/30 待产出 | 预期行为：代码尚未开工。**不构成问题** |
| 11 | 仓库 CLI 自检 | `teamwork status`、`pmo-sweep --dry-run` | ✅ 通过 | `pmo-sweep` 输出 `no tasks to unblock`（18 个 task 均为 `created`，无误置 `blocked`） |
| 12 | 另一 epic 是否有 task | 任务要求 | ✅ 无 | `projects/evergreen/backlog/tasks/` 为空目录 |

---

## 四、M1 完成判据覆盖矩阵

### 4.1 §16.1 M1 交付项 → Task 映射

| §16.1 M1 交付项 | 归属 Task | 覆盖 | 备注 |
| --- | --- | --- | --- |
| `eg init` | T008 | ✅ | Acceptance 逐项断言目录树与 `[S1]` 列一致，且反证 `reviews/`/`proposals/`/`.index/` 不存在 |
| `eg config` | T008 | ✅ | `version`/`domains`/`default_domain` 三键 + `reconcile` commit |
| `eg capture`（Agent 供正文） | T009 | ✅ | 含「全程无网络」反证用例，对齐 §7.5「CLI 不做任何网络请求」 |
| `write_note` | T013 | ✅ | 含 `unprocessed.md` 条目移出 + 移出失败退 3 进报告 |
| `create_card` | T013 | 🟡 | 「知识内容必写」有断言；**「`sources[]` 新建卡必需」只在 Scope 提及，Acceptance 无断言**（见 P1-1） |
| `add_material_rel` 写入 | T014 | 🔴 | `rel` 取值集合与冻结合同 F4 完全不符（见 P0-1） |
| `add_relation` 写入 | T014 | 🔴 | `type` 取值集合与冻结合同 F4 完全不符（见 P0-1） |
| `git commit` | T007 | ✅ | 提交信息规范 + B4 + `grep` 反证无回滚 API |
| 最终报告 | T016 | 🔴 | S1 必填子集字段清单与 §4.6 不符，漏 8 项、自创 4 项（见 P0-3） |
| 包 `model` | T004 | ✅ | 零依赖用 `go list -deps` 反证 |
| 包 `mdfile` | T005 | ✅ | round-trip 字节级相等 + 五种块型切分 |
| 包 `store`（简化） | T006 | ✅ | B1/B2/B3 + `grep` 反证无 `SetStatus`/`flock`/`txn` |
| 包 `git` | T007 | ✅ | 同上 |
| 包 `plan` | T011（Contract）/ T012（Implementation） | ✅ | E1–E6 + W1–W8 + I1 逐条用例 |
| 包 `report` | T016 | 🔴 | 同上 |
| 包 `cli` | T002（Contract）/ T003（Implementation） | ✅ | 九命令注册 + 退出码集中映射 |

### 4.2 §16.1 M1 完成判据 → Task 映射

| M1 完成判据 | 归属 Task | 覆盖 | 证据 |
| --- | --- | --- | --- |
| 一篇真实文章从收录到知识卡与关系落盘，全程零介入 | T018 | ✅ | T018 Acceptance 第 1/2 条：干净环境 `go test ./test/e2e/...`、无交互、三类产物齐备 |
| 产物能被 Obsidian 正常打开 | T013 | 🟡 | T013 Acceptance 第 10 条，人工验证、无机器判据（见 P2-5） |
| Git 有一次可读的 commit | T007 / T018 | ✅ | T018 第 3 条断言 `init → reconcile → capture → <verb>` 链与主题规范 |
| 报告如实写出实际写了什么 | T016 / T018 | 🔴 | 「如实」的机制（诊断数量守恒）有；但必填字段清单本身错，见 P0-3 |
| 不测性能、不测并发 | 全部 task 的「范围边界」 | ✅ | 18/18 均有显式排除段 |

### 4.3 §8 S1 端到端主链路逐环节

| §8 环节 | 归属 Task | 覆盖 | 缺口 |
| --- | --- | --- | --- |
| ① Agent 取正文并清洗（CLI 不做网络请求） | T017（规程）/ T009（反证） | 🔴 | **T017:53 的调用顺序从 `eg capture` 起，漏掉 §8.2 的第 0 步**；§8.1 首行失败分支「抓取失败 / 正文为空 → 不调 capture，直接在报告写明原因与 URL，不落任何文件」无任何 task 承接（见 P1-9） |
| ② `eg capture` | T009 | ✅ | — |
| ③ `eg context` | T010 | 🟡 | 覆盖完整；但 §16.1 把 `eg context` 划在 **M2**，M-001 里程碑描述也未列它（见 P1-10） |
| ④ Agent 生成 ChangePlan | T011 / T017 | ✅ | 七个 op 字段表 + 最小合法样例 JSON + 两份完整 plan 样例 |
| ⑤ `eg apply` 逐文件写入 | T015 | 🔴 | 跨领域写入被写成 error 零写入，与 §4.5.1 W1 冲突（见 P0-4） |
| ⑥ Git commit | T007 / T015 | ✅ | — |
| ⑦ 最终报告 | T016 | 🔴 | 见 P0-3 |
| ⑧ 渲染人类可读报告 | T016 | ✅ | 双形态输出（`--json` + 人类可读）同源同事实 |

### 4.4 S1 的 7 个 op 与 9 个命令归属

| S1 op（§4.5，7 个） | 归属 Task |
| --- | --- |
| `add_source` | T009 |
| `write_note` | T013 |
| `create_card` | T013 |
| `append_card` | T013 |
| `add_material_rel` | T014 🔴 |
| `add_relation` | T014 🔴 |
| `add_open_question` | T013 |

**7/7 有归属**（其中 2 个内容错误）。

| S1 命令（§7.1，9 个） | 合同（T002） | 骨架（T003） | 实现任务 |
| --- | --- | --- | --- |
| `init` | ✅ | ✅ | T008 |
| `config get\|set` | ✅ | ✅ | T008 |
| `capture` | ✅ | ✅ | T009 |
| `context` | ✅ | ✅ | T010 |
| `apply --plan` | ✅ | ✅ | T015 |
| `search` | ✅ | 注册占位 | **无**（§16.1 属 M2） |
| `card show` | ✅ | 注册占位 | **无**（§16.1 属 M2） |
| `rel` | ✅ | 注册占位（**仅读路径**，漏 `rel add\|remove` 的写路径与 `relate` commit） | **无**（§16.1 属 M2） |
| `report --last` | ✅ | ✅ | T016 |

**9/9 有合同归属，6/9 有实现归属**。3 个未实现命令与 §16.1 的 M1/M2 划分一致，但 T003:55/73 把它们描述为「S1 未覆盖」用法错误 —— 阶段标签错（它们是 S1 命令、M1 未实现），见 P1-10。

---

## 五、需求追溯缺口

技术方案 §14 追溯矩阵 66 条生效需求，其中标 **S1 的 44 条**（与施工索引第 140 行「S1 44 条 / S2 18 条 / S3 2 条 / S4 1 条 / S5 1 条」一致）。

### 5.1 覆盖统计

| 指标 | 数值 |
| --- | --- |
| S1 需求总数 | 44 |
| 被至少一个 task 引用 | 40（90.9%） |
| **0 引用** | **4** |
| task 引用的非 S1 需求 | 3（`EG-AGT-01` S5、`EG-EDIT-04` S2、`EG-EDIT-06` S2） |
| task 引用的**不存在**需求 ID | 5（`EG-REL-01/02/03/04/06`） |
| 语义误挂（ID 存在但含义完全不同） | 4 处（`EG-CHK-02`/`EG-CHK-04`/`EG-CHK-05` 于 T014，`EG-CHK-03` 于 T015） |

### 5.2 未被任何 Task 覆盖的 EG-ID

| EG-ID | §14 原文语义 | 技术模块 / 验收测试 | 缺口性质 | 建议归属 |
| --- | --- | --- | --- | --- |
| `EG-SRC-04` | 每次知识加工必须有可回读文章作依据，**新建卡必须建立材料关系** | `plan` V3/V7；T-SRC-04 无材料关系建卡被拒 | **功能级缺口**。T013 Scope 写了「`sources[]`（新建卡必需）」但 Acceptance 无对应断言；T012 又明确「不实现 V1–V14 全量校验」，导致 V3/V7 两头落空 | T013 + T012 |
| `EG-CVG-05` | 对立卡并存与 `opposing` 一对一条记录、无议题组 | `rules.normalizeOpposing`；T-CVG-05 反向重复判定不产生第二条 | **标签级缺口**。T014 实际实现了该行为，但引用的是不存在的 `EG-REL-06` | T014 |
| `EG-NOTE-04` | 材料笔记是材料层产物，不参与知识收敛、关系判断、默认主题输出 | `query`（`kind='note'` 过滤）；T-NOTE-04 笔记不进收敛输入与综述正文 | **半功能缺口**。T010 输出白名单含「本领域已有笔记」，但无任何断言证明笔记不进收敛输入 | T010 |
| `EG-CFM-01` | 首版不设默认审批闸门；卡创建即 active、无中间态 | `plan`/`proposal`（无中间态）；§7.1 把该需求挂在 `eg report --last` 上 | **标签级缺口**。T013 已断言 `status: active`、T002 已断言「S1 全部命令无确认交互」，但都没挂 `EG-CFM-01` | T016（按 §7.1）或 T013 |

### 5.3 不存在的需求 ID

`EG-REL-01`、`EG-REL-02`、`EG-REL-03`、`EG-REL-04`、`EG-REL-06` 在**技术方案全文（0 命中）、`prd.md`（0 命中）、`digest_prd.md`（0 命中）、`digest_design.md`（0 命中）**中均不存在。它们出现在：

- `T-…-014` 第 49 行（`关联需求`）、第 55、56、57 行（正文）
- `T-…-018` 第 55 行（`关联需求`：`EG-REL-01`）
- `s1_main_flow/dashboard.html` 第 1062、1237 行（由 task 内容生成的镜像）

按 §14 的正确映射：

| 误用 ID | 实际语义 | 应改为 |
| --- | --- | --- |
| `EG-REL-01` | `sources[]` 四要素齐全 | `EG-KNW-04` |
| `EG-REL-02` | `relations[].type` 四谓词 | `EG-CVG-06` |
| `EG-REL-03` | `target` 为 `k-…`、`reason` 必填 | `EG-CVG-06` + `EG-EXT-04` |
| `EG-REL-04` | 建卡必须有材料关系 | `EG-SRC-04` |
| `EG-REL-06` | `opposing` 单向存储 + 去重 | `EG-CVG-05` |

---

## 六、阶段边界越界与缺失

### 6.1 S1 必须做的四条安全底线：齐备 ✅

| 底线 | 归属 | 覆盖情况 |
| --- | --- | --- |
| B1 自动路径默认只追加 | T006 | ✅ 结构性验收：`store` 导出 API 中不存在替换 / 删除已有块的函数 |
| B2 逐字保留「用户补充」「存疑与待验证」 | T006 / T013 | ✅ round-trip 逐字相等（含空行、缩进、未知子结构）+ `SkipUserBlockUnsafe` |
| B3 文件自读取以来变化则跳过并报告 | T006 / T010 / T015 | ✅ `content_hash` 三处交叉断言（context 产出 → store 重算 → apply 比对） |
| B4 提交失败不做破坏性回滚 | T007 / T015 | ✅ `grep -rn "checkout\|reset --hard\|clean -" internal/git/` 无匹配 + 字节比对 |
| 四条在真实链路复现 | T018 | ✅ 「B1–B4 四条 e2e 用例全部通过」 |

### 6.2 S2–S5 能力是否被塞进 M1：整体干净，2 处越界

| S2–S5 能力 | 是否被塞进 M1 | 证据 |
| --- | --- | --- |
| 多文件强原子 / `.index/txn/` / `intent.json` | ❌ 未越界 | T015:69 显式排除 + Acceptance `grep` 反证 |
| 锁（`flock` / `.eg.lock`） / 写前复核 | ❌ 未越界 | T006 / T015 双重 `grep` 反证 |
| 崩溃恢复 | ❌ 未越界 | T015:69 |
| 对账 `eg reconcile` / `eg check` | ❌ 未越界 | T001:64「不创建 `internal/reconcile`」 |
| 索引 / FTS5 / 性能门槛 | ❌ 未越界 | T001:64「不创建 `internal/index`」+ `go list -m all` 不含 SQLite |
| 提案与逻辑删除 | ❌ 未越界 | T001:64「不创建 `internal/proposal`」；T013:64 不写 `deleted_at` |
| **严格校验（W 升 error）** | 🔴 **越界** | **T015:62 / T015:83 / T018 把「跨领域写入」实现为 error + 零写入，而 §4.5.1 明写 W1「warning（S5 起 error）」，施工索引第 77 行同样写明「其余 W1–W8 / I1 一律 warning，照写 + 进报告」**。同 epic 的 T012:55 / T012:74 写的是「W1 照写、退出码不受影响」——两份 task 自相矛盾 |
| 跨领域能力 | ❌ 未越界 | T008:66 明确 `EG-DOM-06/08` 属 Deferred，不提供迁移命令 |
| **W3 判定（S2 起）** | 🟡 **轻度越界** | T014:82 把「`target` 卡为 `deprecated` → W3 warning」写成 M1 验收项，但 §4.5.1 W3 标注「S2 起判定」，且 S1 无 `deprecate` 命令、无失效卡（§14 EG-VIEW-07 注「S1 无失效卡」） |

### 6.3 M1 / M2 边界与 §16.1 不一致

| 项 | §16.1 归属 | 实际 task 归属 | 里程碑 M-001 Description 是否声明 | 判定 |
| --- | --- | --- | --- | --- |
| `eg context` | **M2** | T010，`所属阶段：S1 · 里程碑 M1` | ❌ 未列 | 🟡 提前是合理的（§8 主链路与 B3 的 `content_hash` 都依赖它），但**必须在里程碑里写明理由** |
| B1–B4 各有单测 | **M2**（§16.1 M2 完成判据） | T006 / T007 / T018 均在 M1 | ❌ 未列 | 🟡 同上（§9.1 说「S1 必须实现且不可放宽」，提前合理但需声明） |
| `convergence[]` 逐卡记录 | M2 | T011 / T012 / T017 已定义并验收 | ❌ 未列 | 🟡 同上 |
| `search` / `card show` / `rel` | M2 | 无实现任务 | — | ✅ 与 §16.1 一致；但 T003:55/73 的「S1 未覆盖」措辞错，应为「M1 未实现」 |

---

## 七、逐 Task 评审表

结论口径：✅ 通过（可直接开工） / 🟡 有条件通过（开工前修 P1/P2） / 🔴 不通过（P0 未修不得进入 `ready`）。

| Task | 标题 | 结论 | 主要问题 | 具体修改建议 |
| --- | --- | --- | --- | --- |
| 001 | `cmd/eg` 项目脚手架与 Go 模块初始化 | 🟡 | ① 引用 `EG-AGT-01`（S5）未标阶段；② **未被任何 task 硬依赖**，T005/006/007 可在无 `go.mod` 时进 `integration`，`verify.test` 必然失败；③ Acceptance 第 5 条「reviewer 逐条对照 §13 表格」无机器判据 | ① 改写为「EG-AGT-01（S5 整体验收，本任务仅奠定 CLI 二进制内无模型调用的前提）」；② 把 001 加入 T004 的 `depends_on`，或给 005/006/007/008 各加 `soft_depends_on: 001`；③ 改为「`internal/*/doc.go` 首行匹配正则 `^// \[S[1-5]\]`，且允许依赖列表与 §13 表格逐包相等（脚本比对）」 |
| 002 | eg CLI 输出与错误码约定 — Contract | 🟡 | ① **DoR 第 1 行声明硬依赖 001，但第 22–23 行 `depends_on: []` / `soft_depends_on: []`**，门禁失效，且 start 08-31 与 001 due 09-01 冲突；② `code_paths` 写 `evergreen/docs/specs/` 而 deliverable 在 `projects/evergreen/s1_main_flow/docs/specs/`；③ Acceptance 第 7 条「下游实现者只读本文即可开工」在 due 09-01 不可判定（016/017 要到 09-05/09-06 才开工）；④ 九命令参数表未含 `--dry-run`，但 T017 验收依赖它 | ① 加 `soft_depends_on: [T-…-001]` 并把 DoR 第 1 条改为「不阻塞开工，001 落地后回填实际二进制名与 Makefile target」；② `code_paths` 改为 `projects/evergreen/s1_main_flow/docs/specs/`；③ 该条改为「合同发布后 24h 内无下游澄清请求」或移出 Acceptance；④ 在九命令参数表中补 `apply --dry-run`（零写入零 commit） |
| 003 | eg CLI 框架骨架 — Implementation | 🟡 | ① 第 55 行 / Acceptance 第 4 条把 `search`/`card show`/`rel` 说成「**S1 未覆盖**」——它们是 S1 命令、只是 M1 未实现；② 第 56 行只把 `rel` 当只读命令，漏 §7.1 的 `rel add\|remove` 写路径与 `relate` commit；③ 引用 `EG-AGT-01`（S5） | ① 措辞改「M1 未实现（S1 命令，M2 落地）」，错误信息文案同步；② 第 56 行改为「`rel` 的读路径只读；`rel add\|remove` 的写路径 M1 不注册，`--help` 标注 M2」；③ 同 001 ① |
| 004 | model 包：产物类型、枚举、时间格式与稳定 ID 规则 | 🟡 | ① 引用 `EG-EDIT-06`（S2）未标阶段；② 与 001 之间只有软依赖（见 001 ②） | ① 改为「EG-EDIT-06（S2；本任务只按冻结合同 F3 预留 `deleted_at`/`deleted_reason` 字段形态，不实现逻辑删除）」；② 把 001 提为 `depends_on` |
| 005 | mdfile 包：frontmatter 与五分区 round-trip 读写 | ✅ | 无实质问题。round-trip 字节级相等、五种块型切分、`block_hash` 稳定性、块定位符不落盘四条均带明确断言 | 可直接开工。建议补 `soft_depends_on: 001` |
| 006 | store 包：唯一状态写口与安全底线 B1–B3 | ✅ | 无实质问题。B1 用「导出 API 中不存在替换/删除函数」做结构性验收是本批质量最高的写法之一 | 可直接开工。建议补 `soft_depends_on: 001` |
| 007 | git 包：提交信息规范与安全底线 B4 | ✅ | 无实质问题。B4 用「注入 commit 失败 + 字节比对 + `grep` 反证无回滚 API」双重锁定 | 可直接开工。建议补 `soft_depends_on: 001` |
| 008 | eg init 与 eg config：vault 骨架与 evergreen.yml | ✅ | 无实质问题。目录树「存在与不存在」逐项断言、重复 `init` 幂等、`EG-DOM-06/08` 作为 Deferred 正确引用 | 可直接开工 |
| 009 | eg capture：S1 最小收录合同 | ✅ | 无实质问题。9 条 Acceptance 完整覆盖 §7.5 的判重、幂等、`--json` 七键、无网络反证 | 可直接开工 |
| 010 | eg context：加工上下文白名单输出与 content_hash | 🟡 | ① `EG-NOTE-04`（笔记不进收敛输入）无任何断言（S1 需求 0 引用之一）；② §16.1 把 `eg context` 划在 M2，M-001 未声明提前 | ① `关联需求` 加 `EG-NOTE-04`，Acceptance 增「构造一份材料笔记 → 输出中该笔记只出现在材料层字段，不出现在候选相似卡 / 收敛输入字段（字段级断言）」；② 由 M-001 Description 声明提前理由（见 P1-10） |
| 011 | ChangePlan 数据合同与校验分级 — Contract | 🟡 | ① due 09-03 **早于**其软依赖 T010 的 due 09-04；② 末条 Acceptance「下游实现者只读本文即可开工」在 due 09-03 不可判定（016/017 到 09-05/09-06 才开工） | ① 去掉对 010 的软依赖（`base` 的 `id → content_hash` 形态在 §4.5 已定死，合同不必等实现），或把 due 推到 09-05；② 同 002 ③ |
| 012 | ChangePlan 校验与 op 展开 — Implementation | 🟡 | ① 第 63 行「不实现 V1–V14 全量校验」把 `EG-SRC-04` 依赖的 V3/V7 一起排除掉了，而 EG-SRC-04 是 S1；② 第 59 行把「`ops[]` 为空」的 warning 编号成 W5（W5 实际是 `convergence[]` 缺条目） | ① 第 63 行改为「不实现 V1–V14 全量校验与严格化（S5/M6），**但 V3/V7「建卡必须带材料关系」属 S1（EG-SRC-04），本任务实现**」，并在 Acceptance 增对应用例；② 改为「未编号 warning（§4.5 `ops[]` 空列表）」或在 T011 合同中补编号并同步 §4.5.1 |
| 013 | write_note / create_card / append_card / add_open_question 分区写入 | 🟡 | ① Scope 写了「`sources[]`（新建卡必需）」但 **11 条 Acceptance 无一条断言「无 `sources[]` → 拒」**，`EG-SRC-04` 因此 0 覆盖；② 第 84 行括注「EG-CHK-06 属 S2」与 §14 不符（EG-CHK-06 是 S1，只有 `replace_block` 属 S2）；③ Acceptance 第 10 条 Obsidian 打开无机器判据 | ① Acceptance 增「`create_card` 缺 `sources[]` 或 `sources[]` 为空 → 退 `2` 零写入」，`关联需求` 加 `EG-SRC-04`；② 改为「EG-CHK-06 的『历史块只追加、问题文本原样保留』本任务实现；『当前有效问题块替换』（`replace_block`）属 S2」；③ 补一条自动化断言（用 Markdown 解析器断言 5 个 H2 层级与顺序、frontmatter 可解析为 map），人工 Obsidian 结论作为补充证据 |
| 014 | add_material_rel / add_relation 关系写入与 opposing 规范化去重 | 🔴 | ① **P0-1**：第 55 行材料 `rel` 写成 `supports`/`contradicts`/`extends`/`context`、第 56 行论证 `type` 写成 `depends_on`/`refines`/`opposing`，与冻结合同 F4 及自身 `design_doc` 锚点 `#ch6`（施工索引第 85 行）完全不符；② **P0-2**：第 49/55/56/57 行引用 5 个不存在的 `EG-REL-*`；③ **P1-2**：`EG-CHK-02`/`EG-CHK-04`/`EG-CHK-05` 三处误挂（实际语义是自检问题深度 / 自检结果处置 / 自检非前置）；④ 第 81 行 W2 用例把 `"supports"` 当材料关系名，用例本身建立在错误取值上；⑤ 第 82 行 W3 用例越界（S2 起判定） | ① 第 55 行 `rel` 改 `support` / `against` / `context`；第 56 行 `type` 改 `derives` / `supports` / `limits` / `opposing`；Acceptance 增「集合外取值（如 `rel: supports`、`type: depends_on`）一律拒绝，枚举封闭」；② 按 §5.3 映射表逐个替换 ID；③ 改为 `EG-CVG-06`（E3 / ID 类型）、`EG-KNW-04`（四要素）、`EG-CVG-05`（opposing 去重）；E2 悬空引用在 §14 无独立 EG 条目，在 Scope 注明「属 §4.5.1 工程约束，无对应 EG 编号」而非硬挂；④ 第 81 行改为 `reason == "support"`；⑤ 第 82 行改为「W3 判定占位，S1 无失效卡因此不产生该 warning」或标注「仅锁定 warning 形态，需手工构造 deprecated 卡」 |
| 015 | eg apply 写入执行器编排与退出码 3/4 | 🔴 | ① **P0-4**：第 62 行「跨领域写入 → 直接失败」+ Acceptance 第 4 条「整个 apply 失败、零写入」与 §4.5.1 W1「warning（S5 起 error）」冲突，且与 T012:55/74 自相矛盾；② **P1-3**：引用 `EG-EDIT-04`（S2，`eg deprecate`）与本任务无关；③ 第 62 行的 `EG-CHK-03`（自检取材领域约束）应为 `EG-DOM-02`；④ 第 59/85 行用 `written[]`，沿用了 T016 的错误字段名；⑤ `code_paths` 有 `internal/plan/expand.go`、`internal/store/apply.go`，deliverable 却是 `internal/plan/executor.go`（不在 `code_paths` 中） | ① 第 62 行改「跨领域写入 → W1 warning，照写并进报告，不影响退出码；S5 起升 error」，Acceptance 第 4 条改为「跨域 op 照常写入 + 报告含 W1 + 退出码 `0`/`3`」；② 删除 `EG-EDIT-04`；③ 改 `EG-DOM-02`；④ 字段名随 P0-3 一并改为 §4.6 口径；⑤ 对齐 `code_paths` 与 `deliverables` |
| 016 | report 包：最终报告 S1 必填子集与 eg report --last | 🔴 | ① **P0-3**：第 54 行的「S1 必填子集」漏 §4.6 的 `source`/`note`/`cards`/`relations`/`open_questions`/`links`/`default_domain_fallback`/`high_impact[]` 八项，自创 `written[]`/`errors[]`/`commit`/`timestamps`；`high_impact[]` 在 18 个 task 中 **0 命中**；② Acceptance 第 1 条「按键名清单逐项断言，缺一即失败」会把错误 schema 固化成单测；③ `EG-CFM-01`（§7.1 挂在 `eg report --last` 上）未引用；④ hard-dep 011 但无 `soft_depends_on: 012`；⑤ due 09-06 与其软依赖 015 同日，零缓冲 | ① 第 54 行改为 §4.6 原文 11 项，`commit` 改回嵌套 `git.commit`；②Acceptance 增「`high_impact[]` 四类各一例：新建承载新核心含义的卡 / 追加非核心补充 / 建立或调整 `opposing` / 建立或调整论证关系」与「`proposals[]`/`deprecated_new_support[]` 输出空数组、`support_check[]`/`affected` 输出空值、`reconcile` 输出 `{"ran": false}`，且不得输出假数据」；③ 加 `EG-CFM-01`；④ 补 `soft_depends_on: T-…-012`；⑤ due 推到 09-07 |
| 017 | SKILL.md：写给 coding agent 的 S1 调用顺序与禁止项 | 🟡 | ① **P1-9**：第 53 行调用顺序从 `eg capture` 起，漏 §8.2 第 0 步「Agent 自行抓取并清洗正文（CLI 不做网络请求）」及 §8.1 首行失败分支；② 禁止项缺 §8.2 中 3 条：「禁止把 active 说成『已确认』『已入库』」「禁止把多跳推导结论沉淀成新卡」「不虚构材料来源与依据，文章没表达的部分留空」，而 `EG-AGT-02` 的判据是「十一条边界逐条可在文件中定位」；③ Acceptance 第 6 条依赖 `eg apply --dry-run`，但该 flag 未进 T002 合同；④ Acceptance 第 8 条需要「未参与本 epic 的 coding agent」，资源不可控；⑤ hard-dep 011 但无 `soft_depends_on: 012` | ① Scope 第 1 条补第 0 步与抓取失败分支（「不调 capture，直接在报告写明失败原因与 URL，不落任何文件」）；② 禁止项补齐 3 条，Acceptance 改为「与 §8.2 code-block 的十一条边界逐条对齐（编号一致，缺一不通过）」；③ 在 T002 合同补 `--dry-run`；④ 该条降级为「附加验证，不作为 `integration` 门禁」；⑤ 补 `soft_depends_on: T-…-012` |
| 018 | M1 端到端验收：一篇真实文章零介入跑通 | 🔴 | ① **P0-4 连带**：Scope「四条禁止项反例」把「跨领域写入被拒」列为「均断言零写入」，与 §4.5.1 W1 冲突；② **P0-2 连带**：`关联需求` 含 `EG-REL-01`；③ 交付物文件名 `2026-09-05-m1-acceptance-report.md` 与本任务 due 09-08 不符；④ 里程碑 M-001 的 `date: "2026-09-05"` 早于本任务 due 09-08；⑤ 11 个总入度（2 硬 + 9 软）使其成为唯一收敛点，任一上游延期即整体延期 | ① 把「跨领域写入被拒」换成「跨领域写入产出 W1 warning 且报告可见、退出码 0/3」，另补一条真 error 反例（E5 未知 op 或 E1 重复 ID）；② `EG-REL-01` 改 `EG-KNW-04`；③ 交付物改 `2026-09-08-m1-acceptance-report.md`；④ M-001 `date` 改 `2026-09-08`（或之后）；⑤ 建议把「§14 S1 需求逐条核对」拆成独立的前置 task（可与 015/016 并行），使 018 只承担 e2e 跑通 |

**统计**：🔴 4 个（014 / 015 / 016 / 018） · 🟡 9 个（001 / 002 / 003 / 004 / 010 / 011 / 012 / 013 / 017） · ✅ 5 个（005 / 006 / 007 / 008 / 009）。

---

## 八、依赖图与关键路径分析

### 8.1 图健康度

| 检查 | 结果 |
| --- | --- |
| 环 | ✅ 无（硬依赖图与硬+软合并图均无环） |
| 孤儿 task | ✅ 无（18 个节点全部连通） |
| 自依赖 | ✅ 无 |
| 依赖 ID 可解析 | ✅ 39 条硬依赖 + 30 条软依赖全部命中本 epic 内 task |
| 无依赖起点 | `001`（脚手架）、`002`（CLI 合同） |
| 唯一收敛终点 | `018` |
| `pmo-sweep --dry-run` | `no tasks to unblock`（无误置 `blocked`） |

### 8.2 关键路径

| 路径类型 | 长度 | 链路 |
| --- | --- | --- |
| 硬依赖关键路径（阻塞**开工**） | **4 层** | `004` → `005` → `006` → `009`（并列：`004` → `007` → `008` → `009`） |
| 硬+软完成链（阻塞**integration**） | **13 层** | `001` → `004` → `005` → `006` → `009` → `010` → `011` → `012` → `013` → `014` → `015` → `016` → `018` |

**判定**：硬依赖只有 4 层，说明并行度设计本身是合理的（`002`/`004` 两条支线可同时起）。但软依赖把完成顺序串成 **13 层**，而排期只给了 **9 个日历日**（08-31 → 09-08）。软依赖在本仓的语义是「阻塞完成/integration」，因此 13 层链必须至少 13 个可交付时段——**排期与依赖语义在数学上不相容**。

### 8.3 关键节点

| 节点 | 被依赖次数 | 风险 |
| --- | --- | --- |
| `002` CLI 合同 | 8 | 单点。合同延期或返工 → 8 个下游全部受影响 |
| `011` ChangePlan 合同 | 7 | 单点。且 due 09-03 早于其软依赖 `010`（due 09-04），本身排期非法 |
| `006` store | 6 | B1–B3 的实现本体，返工成本最高 |
| `018` | 总入度 11（2 硬 + 9 软） | 唯一收敛点，无任何缓冲 |

### 8.4 并行度与排期冲突

| # | 冲突 | 证据 |
| --- | --- | --- |
| 1 | `011` due `2026-09-03` **早于**其软依赖 `010` due `2026-09-04` | frontmatter 第 8–9 行 + `soft_depends_on` |
| 2 | `014` 与其软依赖 `013` 同为 due `2026-09-05`（零缓冲） | 同上 |
| 3 | `016` 与其软依赖 `015` 同为 due `2026-09-06`（零缓冲） | 同上 |
| 4 | 里程碑 `M-001` `date: "2026-09-05"` 早于其唯一硬依赖 `018` 的 due `2026-09-08` | `milestones/M-001-m1.md` 第 7、8 行 |
| 5 | 18 个 task 全部 `owner: ikaqiu`；08-31 / 09-01 / 09-02 / 09-03 各有 **3 个** 任务同日开工，每个 task 工期只有 1 天 | 全部 frontmatter `owner` + `start` |
| 6 | Contract/Implementation 软依赖漏 6 处：hard-dep `002` 的 `015`/`016`/`017`/`018` 无 `soft_depends_on: 003`；hard-dep `011` 的 `016`/`017` 无 `soft_depends_on: 012` | 与 `s1_main_flow/AGENTS.md` 的 Contract/Implementation 规则不符 |
| 7 | `001` 未被任何 task 硬依赖，`005`/`006`/`007` 甚至无软依赖 → 可在无 `go.mod` 时被判 `integration`，而其 `verify.test` 是 `cd evergreen && go test ./internal/...` | `graph.json` 出边统计 |

### 8.5 粒度与工作量

| 判断 | Task | 理由 | 建议 |
| --- | --- | --- | --- |
| **过大，建议再拆** | `013` | 一个 task 同时承接 4 个 op（`write_note` / `create_card` / `append_card` / `add_open_question`）+ 11 条 Acceptance + 笔记与卡两类产物的分区规则，1 天工期不现实 | 拆为「笔记侧：`write_note` + `add_open_question` + `unprocessed.md` 条目移出」与「卡侧：`create_card` + `append_card` + 分区白名单」两个 task |
| **过大，建议再拆** | `018` | 同时做 e2e 脚本、语料准备、M1 判据逐条核对、§14 追溯矩阵逐条核对、B1–B4 e2e 回归、4 条禁止项反例、幂等回归，共 10 条 Acceptance | 拆为「M1 e2e 主链路跑通」与「M1 判据与 §14 追溯逐条核对报告」两个 task，后者可与 `015`/`016` 并行 |
| **偏大** | `006` | store 写口 + B1 + B2 + B3 + `id → path` 解析，8 条 Acceptance | 可接受（B1–B3 高度耦合于同一写口），但工期应给 2–3 天 |
| **偏小，可考虑合并** | `001` + `003` | `001` 只做 `go.mod`/`main.go`/`Makefile`/`doc.go`，`003` 做命令注册与退出码映射；两者都不含业务逻辑 | 若人力紧张可合并为「CLI 骨架与工程脚手架」；但保持拆分也合理（`003` 需等 `002` 合同） |
| 粒度合适 | `002` `004` `005` `007` `008` `009` `010` `011` `012` `014` `015` `016` `017` | 单一职责、Acceptance 6–9 条、边界清晰 | 无 |

---

## 九、问题清单

### P0 — 阻塞（4 条，修复前对应 task 不得进入 `ready`）

| ID | 问题 | 证据（文件:行） | 具体可执行的修改建议 |
| --- | --- | --- | --- |
| **P0-1** | **T014 的关系类型集合与冻结合同 F4 完全不符**。材料关系写成 `supports`/`contradicts`/`extends`/`context`（正确：`support`/`against`/`context`），论证关系写成 `depends_on`/`refines`/`opposing`（正确：`derives`/`supports`/`limits`/`opposing`）。F4 是六条冻结合同之一，改动等于数据迁移。连带后果：§4.5.1 的 E3 立论基础「`support` vs `supports` 只差一个字母」在 T014 的取值体系下不成立，E3 无法实现 | `tasks/T-…-014:55`、`:56`、`:81`  ⟷  `docs/specs/2026-08-31-evergreen-s1-tech-design.md:37`（F4）、`:85`（§6）；技术方案 §1.2 F4 / §6.1 表 / §4.1 白名单表 | ① `:55` 的 `rel` 取值改为 `support` / `against` / `context`；② `:56` 的 `type` 取值改为 `derives` / `supports` / `limits` / `opposing`；③ `:81` 的 `reason == "supports"` 改为 `reason == "support"`；④ Acceptance 新增一条：「材料 `rel` 与论证 `type` 均为**封闭枚举**：写入集合外取值（如 `rel: supports`、`type: depends_on`）一律拒绝，错误信息列出合法取值集合」；⑤ 同步检查 `dashboard.html` 由 task 内容再生成 |
| **P0-2** | **引用 5 个根本不存在的需求 ID `EG-REL-01/02/03/04/06`**。技术方案全文、`prd.md`、`digest_prd.md`、`digest_design.md` 四处均 0 命中。追溯矩阵因此无法闭环 | `tasks/T-…-014:49`、`:55`、`:56`、`:57`；`tasks/T-…-018:55`；`dashboard.html:1062`、`:1237` | 按 §14 逐个替换：`EG-REL-01` → `EG-KNW-04`（`sources[]` 四要素）；`EG-REL-02` → `EG-CVG-06`（`relations[].type` 四谓词）；`EG-REL-03` → `EG-CVG-06` + `EG-EXT-04`（`reason` 非空）；`EG-REL-04` → `EG-SRC-04`（建卡必须有材料关系）；`EG-REL-06` → `EG-CVG-05`（`opposing` 一对一条）。T018 的 `EG-REL-01` → `EG-KNW-04` |
| **P0-3** | **T016 的「S1 必填子集」与 §4.6 不符**：漏 `source`/`note`/`cards`/`relations`/`open_questions`/`links`/`default_domain_fallback`/`high_impact[]` 八项，自创 `written[]`/`errors[]`/`commit`/`timestamps` 四项。`high_impact[]` 在 18 个 task 中 0 命中。Acceptance 第 1 条「按键名清单逐项断言，缺一即失败」会把错误 schema 固化成单测，M1 报告将无法满足 PRD 十项必备 | `tasks/T-…-016:54`、`:74`  ⟷  `docs/specs/2026-08-31-evergreen-s1-tech-design.md:78`（§4.6）；技术方案 §4.6 表 | ① `:54` 改为 §4.6 原文 11 项：`source` / `note` / `cards` / `relations` / `open_questions` / `links` / `git.commit` / `skipped[]` / `default_domain_fallback` / `high_impact[]` / `warnings[]`（`commit` 恢复为嵌套 `git.commit`）；② Acceptance 增「`high_impact[]` 四类各一例：新建承载新核心含义的卡 / 追加非核心补充 / 建立或调整 `opposing` / 建立或调整论证关系」；③ Acceptance 增「`proposals[]` 与 `deprecated_new_support[]` 输出空数组、`support_check[]` 与 `affected` 输出空值、`reconcile` 输出 `{"ran": false}`、`txn_id` 省略，**不得输出假数据**」；④ T015:59/:85 的 `written[]` 同步改为 §4.6 口径 |
| **P0-4** | **把 §4.5.1 的 W1（领域越界）当 error 实现，提前执行 S5 严格化，并与同 epic 的 T012 自相矛盾**。T015 说「跨领域写入 → 直接失败」「整个 apply 失败、零写入」；T012 说「W1 照写、退出码不受影响」。技术方案与施工索引均写明 W1 是 warning（S5 起 error）。附带：T015 用 `EG-CHK-03`（自检取材领域约束）指代 apply 的单领域约束，需求引用错 | `tasks/T-…-015:62`、`:83`、`:52`；`tasks/T-…-018` Scope「四条禁止项反例…均断言零写入」  ⟷  `tasks/T-…-012:55`、`:74`；`docs/specs/2026-08-31-evergreen-s1-tech-design.md:77`；技术方案 §4.5.1 W1 行 | ① T015:62 改为「单领域约束（EG-DOM-02）：跨领域写入产出 **W1 warning**，照写并进报告，不影响退出码；S5 起升 error」；② T015:83 改为「跨域 op 照常写入 + 报告 `warnings[]` 含 W1（带 op 下标与字段路径）+ 退出码 `0`/`3`」；③ T015:52 的 `EG-CHK-03` 改 `EG-DOM-02`；④ T018 的四条禁止项反例中「跨领域写入被拒」改为「跨领域写入产出 W1 warning 且报告可见」，并补一条真 error 反例（E5 未知 op 或 E1 全库重复 ID） |

### P1 — 应修（10 条）

| ID | 问题 | 证据 | 修改建议 |
| --- | --- | --- | --- |
| **P1-1** | 4 条 S1 需求 0 引用：`EG-SRC-04`、`EG-CVG-05`、`EG-NOTE-04`、`EG-CFM-01`（44 条中覆盖 40 条）。其中 `EG-SRC-04` 是**功能级缺口**：T013 Scope 写了「`sources[]`（新建卡必需）」但 Acceptance 无断言，T012:63 又把 V3/V7 一并排除 | `cov.json` 全库统计；`T-…-013:57`（Scope）vs `:80-90`（Acceptance）；`T-…-012:63` | T013 Acceptance 增「`create_card` 缺 `sources[]` 或为空 → 退 `2` 零写入」+ `关联需求` 加 `EG-SRC-04`；T012:63 改为「不实现 V1–V14 全量严格化（S5/M6），**但 V3/V7『建卡必须带材料关系』属 S1（EG-SRC-04），本任务实现**」；T014 加 `EG-CVG-05`；T010 加 `EG-NOTE-04` + 字段级断言；T016 加 `EG-CFM-01`（按 §7.1 该需求挂在 `eg report --last` 上） |
| **P1-2** | 4 处需求 ID 语义误挂：T014 用 `EG-CHK-02`（自检问题深度）/`EG-CHK-04`（自检结果处置路径）/`EG-CHK-05`（自检非流程前置）指代「ID 类型硬拦 / 悬空引用 / 端点非 active」；T015 用 `EG-CHK-03`（自检取材领域约束）指代 apply 单领域约束 | `T-…-014:49`；`T-…-015:52`  ⟷  `digest_prd.md:92`、`:93`、`:94`、`:95`；技术方案 §14.4 | T014 → `EG-CVG-06`（E3/ID 类型）、`EG-KNW-04`（四要素）、`EG-CVG-05`（opposing 去重）；E2 悬空引用在 §14 无独立 EG 条目，在 Scope 注明「属 §4.5.1 工程约束，无对应 EG 编号」而不硬挂。T015 → `EG-DOM-02` |
| **P1-3** | 3 处越界引用非 S1 需求且未标阶段：`EG-AGT-01`（S5）于 T001/T002/T003；`EG-EDIT-04`（S2）于 T015；`EG-EDIT-06`（S2）于 T004 | `T-…-001:48`、`002:40`、`003:45`、`004:41`、`015:52`  ⟷  `s1_eg_ids.json`（§14 阶段列） | 统一改为带阶段标注写法：「EG-AGT-01（S5 整体验收，本任务仅奠定 CLI 二进制内无模型调用的前提）」；T015 直接删 `EG-EDIT-04`；T004 改「EG-EDIT-06（S2；本任务只按 F3 预留 `deleted_at`/`deleted_reason` 字段形态，不实现）」 |
| **P1-4** | T002 的 DoR 与 frontmatter 互斥：DoR 声明硬依赖 001，`depends_on` 为空数组 → `teamwork status`/`pmo-sweep` 无法门禁；且 T002 start 08-31 与 001 due 09-01 冲突，按 DoR 字面开工日不满足准入 | `T-…-002:32` vs `:22`、`:23`；`:8` start vs `T-…-001:9` due | 二选一：① 加 `soft_depends_on: [T-…-001]` 且 DoR 改为「不阻塞开工，001 落地后回填二进制名与 Makefile target」；② 若确实必须先有骨架，则 `depends_on` 加 001 并把 T002 的 start 推到 09-01 |
| **P1-5** | T001 未被任何 task 硬依赖，T005/006/007 连软依赖都没有 → 可在无 `go.mod`/`Makefile` 时进入 `integration`，而它们的 `verify.test` 是 `cd evergreen && go test ./internal/...`，必然失败 | `graph.json`（001 出边仅 003/004 软）；`T-…-005:12-14` 等 `verify` 段 | 把 `T-…-001` 加入 `T-…-004` 的 `depends_on`；或给 005/006/007/008 各加 `soft_depends_on: T-…-001` |
| **P1-6** | Contract/Implementation 软依赖漏 6 处，违反 `s1_main_flow/AGENTS.md` 的显式规则（下游对 Implementation 建软依赖）：hard-dep 002 的 015/016/017/018 无 `soft_depends_on: 003`；hard-dep 011 的 016/017 无 `soft_depends_on: 012` | `graph.json`；`s1_main_flow/AGENTS.md:100-107` | 按规则补齐 6 条软依赖；确实不消费实现产物的（如 018 已软依赖全链）在对应 DoR 明写「本任务不消费 003/012 的实现产物，故不建软依赖」 |
| **P1-7** | 排期与依赖方向冲突 4 处：① 011 due 09-03 < 其软依赖 010 due 09-04；② 014 与软依赖 013 同 due 09-05；③ 016 与软依赖 015 同 due 09-06；④ M-001 `date: "2026-09-05"` < T018 due 09-08，且 T018 交付物硬编码 `2026-09-05-m1-acceptance-report.md` | 各 task frontmatter `start`/`due`；`milestones/M-001-m1.md:7`、`:8`；`T-…-018:` deliverables | M-001 `date` 改 `2026-09-08`（或之后）；T018 交付物改 `2026-09-08-m1-acceptance-report.md`；011 去掉对 010 的软依赖（`base` 的 `id → content_hash` 形态在 §4.5 已定死，合同不必等实现）或 due 推到 09-05；014/016 各留 1 天缓冲 |
| **P1-8** | 工作量不可行：18 个 task 全部 `owner: ikaqiu`，08-31/09-01/09-02/09-03 各有 3 个任务同日开工，每 task 工期 1 天；而硬+软完成链长 13 层、日历只有 9 天 | 全部 frontmatter `owner`/`start`/`due`；`graph.json` 完成链 | 二选一：① 按 `roster.yaml` 补齐 owner，把 `004→005→006` 与 `002→003→008→009` 两条支线分给不同人；② 保持单人则把 due 整体拉长到约 3 周，并按 13 层完成链重排 due，同时对 013/018 执行 §8.5 的再拆建议 |
| **P1-9** | T017 漏 §8 主链路第 0 步与首行失败分支，且禁止项缺 3 条，导致 `EG-AGT-02`「SKILL.md 十一条边界逐条可在文件中定位」无法验收 | `T-…-017:53`、`:59-65`、Acceptance 第 2/4 条  ⟷  技术方案 §8.2 code-block（13 行禁止项，EG-AGT-02 判据为十一条边界）、§8.1 首行 | Scope 第 1 条补第 0 步「Agent 自行抓取并清洗正文（CLI 不做网络请求）」与失败分支「抓取失败 / 正文为空 → 不调 capture，直接在报告写明失败原因与 URL，不落任何文件」；禁止项补「禁止把 active 说成『已确认』『已入库』」「禁止把多跳推导结论沉淀成新卡」「不虚构材料来源与依据，文章没表达的部分留空」；Acceptance 改为「与 §8.2 code-block 的十一条边界逐条对齐（编号一致，缺一不通过）」 |
| **P1-10** | M1 任务集边界与 §16.1 不一致且里程碑未声明：`eg context`（T010）、B1–B4 单测（T006/T007）、`convergence[]` 逐卡记录在 §16.1 属 **M2**，但全部标为 M1，而 `M-001-m1.md` Description 只列了 init/config/capture/write_note/create_card/关系/commit/报告。反向看 T003 把 S1 命令 `search`/`card show`/`rel` 说成「S1 未覆盖」 | `milestones/M-001-m1.md:13`；18 个 task 的「所属阶段：S1 · 里程碑 M1」；`T-…-003:55`、Acceptance 第 4 条  ⟷  技术方案 §16.1 M1/M2 行 | ① M-001 Description 补一段：「因 §8 主链路与安全底线 B3 的 `content_hash` 均依赖 `eg context`，本 M1 提前纳入 T010 与 B1–B4 单测（§9.1 要求 S1 必须实现且不可放宽）；M2 仅补 `search`/`card show`/`rel` 与 `convergence[]` 逐卡记录的完整体验」；② T003:55 与 Acceptance 第 4 条的措辞改为「M1 未实现（S1 命令，M2 落地）」，错误信息文案同步；③ T003:56 补「`rel add\|remove` 写路径 M1 不注册，`--help` 标注 M2」 |

### P2 — 建议（8 条）

| ID | 问题 | 证据 | 建议 |
| --- | --- | --- | --- |
| **P2-1** | T014 的 W3 用例越界：§4.5.1 标注 W3「S2 起判定」，且 S1 无 `deprecate` 命令、无失效卡 | `T-…-014:82`；技术方案 §4.5.1 W3 行、§14 EG-VIEW-07 注 | 改为「W3 判定占位，S1 无失效卡因此不产生该 warning」；若保留用例，注明「需手工构造 deprecated 卡，仅锁定 warning 形态，不作为 M1 门禁」 |
| **P2-2** | T012 把「`ops[]` 为空」的 warning 编号为 W5，但 W5 实际是 `convergence[]` 缺条目 | `T-…-012:59`；技术方案 §4.5 `ops[]` 行、§4.5.1 W5 行 | 改为「未编号 warning（§4.5 `ops[]` 空列表）」，或在 T011 合同中新增编号并在评审意见中记录「技术方案 §4.5.1 表待补该编号」 |
| **P2-3** | T013 括注「EG-CHK-06 属 S2」与 §14 不符——EG-CHK-06 是 S1（历史块只追加），只有 `replace_block` 属 S2 | `T-…-013:` Scope 倒数第 3 条；技术方案 §14.4 EG-CHK-06 行、§4.5 op 表 `replace_block` 行 | 改为「EG-CHK-06 的『历史块只追加、问题文本原样保留』本任务实现；『当前有效问题块替换』（`replace_block`）属 S2」 |
| **P2-4** | 「交叉验收」条款制造到期日不可判定的反向依赖：T002 due 09-01 / T011 due 09-03，但依赖 016（09-05 起）、017（09-06 起）的实现者确认 | `T-…-002:72`；`T-…-011` Acceptance 末条 | 改为可判定形式：「合同发布后 24h 内无下游澄清请求」；或移出 Acceptance、作为 Activity Log 的软约定 |
| **P2-5** | 3 条 Acceptance 无机器判据：T013 第 10 条（Obsidian 打开）、T017 第 8 条（外部 coding agent 独立跑通）、T001 第 5 条（reviewer 对照 §13 表格） | 各 task Acceptance 段 | T013 补自动化替代（Markdown 解析器断言 5 个 H2 层级与顺序、frontmatter 可解析为 map），人工结论作补充证据；T017 第 8 条降级为「附加验证，不作 `integration` 门禁」；T001 第 5 条改为「`doc.go` 首行匹配 `^// \[S[1-5]\]` 且允许依赖列表与 §13 逐包相等（脚本比对）」 |
| **P2-6** | frontmatter 内部不一致：T002 `code_paths` 为 `evergreen/docs/specs/` 而 deliverable 在 `projects/evergreen/s1_main_flow/docs/specs/`；T015 `code_paths` 含 `internal/plan/expand.go`、`internal/store/apply.go`，deliverable 却是 `internal/plan/executor.go`（不在 `code_paths`） | `T-…-002:16` vs `:19`；`T-…-015:16-19` vs `:22-25` | 逐一对齐 `code_paths` 与 `deliverables[].path` |
| **P2-7** | 18 个 task 的 `references: []` 全空，权威飞书 URL 只出现在施工索引 frontmatter；而本次 3 条 P0 恰恰是「未逐条对照权威表格」造成的（施工索引第 37/77/78 行其实都写对了） | 全部 frontmatter `references` 字段；施工索引 `:5`、`:12` | 在各 task 的 `references` 补一条权威文档 URL；并把 T011 已有的「逐条对照 §x 表格、条目数完全对应」型核对指令复制到 T014（对照 F4 与 §6.1 关系类型表）、T015（对照 §4.5.1 分级表）、T016（对照 §4.6 字段表）的 DoR 中 |
| **P2-8** | `eg apply --dry-run` 是 T015 新增 flag，§7.1/§7.2 未定义，T002 九命令参数表也未提及，但 T017 Acceptance 第 6 条依赖它 | `T-…-015:58`、`:63`；`T-…-002:46`（无 `--dry-run`）；`T-…-017` Acceptance 第 6 条 | 在 T002 合同的 `apply` 参数表中补 `--dry-run`（零写入零 commit），并在 Acceptance 中要求「`--dry-run` 语义写入合同」；否则 T017 验收依赖一个未进合同的能力 |

---

## 十、修复后再评审的判定标准

再评审只看下列 **12 条门禁**，全部满足即判「通过」；任一条不满足维持「不通过」。

### 10.1 P0 门禁（4 条，必须逐条通过）

| # | 门禁 | 机器可执行的核验方式 |
| --- | --- | --- |
| G1 | 关系类型集合与 F4 一致 | `grep -rn "contradicts\|extends\|depends_on\|refines" projects/evergreen/s1_main_flow/tasks/` 仅命中「集合外取值必须拒绝」类反例表述；`T-…-014` 中 `support`/`against`/`context` 与 `derives`/`supports`/`limits`/`opposing` 两组取值逐字出现 |
| G2 | 需求 ID 全部真实存在 | `grep -rno "EG-[A-Z]\+-[0-9]\+" projects/evergreen/s1_main_flow/tasks/` 提取的 ID 集合 ⊆ §14 追溯矩阵 66 条 ∪ 4 条 Deferred；`EG-REL-*` 命中数为 **0** |
| G3 | 报告字段与 §4.6 一致 | `T-…-016` 的必填子集清单与 §4.6「S1 必填」行逐项相等（11 项）；`high_impact[]` 与 `default_domain_fallback` 在 task 中命中数 ≥ 1；`written[]` 命中数为 0 |
| G4 | W 类一律 warning | `grep -rn "跨领域\|跨域\|领域越界" projects/evergreen/s1_main_flow/tasks/` 中不存在「零写入 / 整个 apply 失败 / 退 `2`」表述；T012 与 T015 对 W1 的描述一致；T018 的禁止项反例不含「跨领域写入被拒」 |

### 10.2 P1 门禁（6 条）

| # | 门禁 | 核验方式 |
| --- | --- | --- |
| G5 | S1 44 条需求 100% 被引用 | 重跑覆盖脚本，`miss` 列表为空；或对确实不做的条目在 EPIC/milestone 中显式登记为 known issue 并附 backlog task ID |
| G6 | 无越界与误挂引用 | 每条非 S1 需求引用都带阶段标注（形如「EG-AGT-01（S5 …）」）；`EG-CHK-02/03/04/05` 不再出现在 T014/T015 |
| G7 | DoR 与 frontmatter 依赖一致 | 对 18 个 task：DoR 中提到的每个 `T-…-NNN` 都出现在 `depends_on` 或 `soft_depends_on`；反之 frontmatter 中每条依赖都在 DoR 有对应说明（脚本双向比对） |
| G8 | 依赖建模完整 | `T-…-001` 至少被 1 条 `depends_on` 引用；Contract/Implementation 规则的 6 处缺口补齐或在 DoR 显式豁免 |
| G9 | 排期自洽 | 脚本断言：任一 task 的 due ≥ 其全部硬依赖与软依赖的 due + 1 天；`M-001.date` ≥ `T-…-018.due`；T018 交付物文件名日期 == T018 due |
| G10 | 工作量可执行 | 任一 owner 在任一日历日的在途 task 数 ≤ 2；或 013 / 018 已按 §8.5 建议拆分且 due 已按 13 层完成链重排 |

### 10.3 一致性与规范门禁（2 条）

| # | 门禁 | 核验方式 |
| --- | --- | --- |
| G11 | 结构规范零回归 | `validate_tasks.py` 输出 `OK`（18/18 必填字段、四段正文、`{path, requires}` deliverable、`design_doc` 锚点有效、无环、无 `(fill in)`）；`teamwork pmo-sweep --dry-run` 无异常 |
| G12 | 内部无自相矛盾 | 对下列 5 组同一行为的跨 task 描述做逐组比对，措辞与结论必须一致：① W1 领域越界（012 / 015 / 018）；② 报告必填字段（015 / 016 / 018）；③ 关系类型集合（004 / 012 / 014 / 017）；④ M1 / M2 边界（M-001 / 003 / 010）；⑤ `--dry-run` 语义（002 / 015 / 017） |

### 10.4 再评审范围

- **必须重读**：T014、T015、T016、T018（P0 直接命中）、T012、T013、T010、T017（P1 功能与覆盖缺口）、`M-001-m1.md`。
- **可只做规范回归**：T005、T006、T007、T008、T009（本次 ✅，无需重读正文）。
- **本次已确认无需评审**：`projects/evergreen/backlog/tasks/`（空目录）。

---

*评审人：项目维护者　|　评审日期：2026-08-31　|　技术方案基线：飞书 wiki `LB73w54iOiXN96keHVLcK38JnSe` revision 124（只读，未做任何修改）*

---

## 十一、修复记录（2026-08-31）

> 本节为**追加**内容，上文第一至第十节保持原样未改写。
> 修复范围严格限定在 `projects/evergreen/s1_main_flow/tasks/` 的 18 个 task 文件与 `milestones/M-001-m1.md`；未新增 / 删除 task，未改动飞书文档（技术方案 wiki 仅只读核对）。
> 行号均指**修复后**文件的当前行号。

### 11.1 P0 修复（4 / 4 全部完成）

| # | 修复前 | 修复后 | 涉及文件与行 |
| --- | --- | --- | --- |
| **P0-1** | 材料 `rel` = `supports`/`contradicts`/`extends`/`context`；论证 `type` = `depends_on`/`refines`/`opposing`；W2 用例用 `reason == "supports"` | 材料 `rel` 收敛为**冻结合同 F4 三值** `support` / `against` / `context`；论证 `type` 收敛为**四谓词** `derives` / `supports` / `limits` / `opposing`；W2 用例改判 `reason == "support"`；新增 Acceptance「**封闭枚举**：`rel: supports` / `rel: contradicts` / `type: depends_on` / `type: refines` 等集合外取值一律拒绝，错误信息逐字列出合法取值集合，两组各有独立用例」；`opposing` 表述改为「按**卡 ID 字典序**取小者为写入端，写入前对同一对去重，已存在则更新 `reason` 不产生第二条，输入方向不规范 → W8 + 自动规范化」 | `T-…-014` `:55`（材料三值）、`:56`（论证四谓词）、`:58`（opposing 规范化与去重）、`:78`（封闭枚举 Acceptance）、`:79`（E3 双向反例）、`:81`（opposing 去重断言，含反向重复不产生第二条） |
| **P0-2** | `EG-REL-01/02/03/04/06` 五个 ID 在技术方案 §14、PRD、需求摘要中均 0 命中 | 按 §14 逐个替换为真实 ID：`EG-REL-01`→`EG-KNW-04`；`EG-REL-02`→`EG-CVG-06`；`EG-REL-03`→`EG-CVG-06`+`EG-EXT-04`；`EG-REL-04`→`EG-SRC-04`；`EG-REL-06`→`EG-CVG-05`。**全库 `EG-REL-` 命中数由 8 降为 0**（含由 task 内容再生成的 `dashboard.html`，现为 0） | `T-…-014` `:49`（关联需求整行重写）、`:55`、`:56`、`:58`；`T-…-018` `:56`（`EG-REL-01`→`EG-KNW-04`）；`s1_main_flow/dashboard.html`（`teamwork status --initiative` 再生成） |
| **P0-3** | T016「S1 必填子集」自创 `written[]`/`errors[]`/顶层 `commit`/`timestamps` 四项，漏 §4.6 八项；`high_impact[]` 全库 0 命中；单测「按键名清单逐项断言」会把错 schema 固化 | 改为 **§4.6 原文 11 项**：`source`/`note`/`cards`/`relations`/`open_questions`/`links`/`git.commit`（**嵌套，不是顶层 `commit`**）/`skipped[]`/`default_domain_fallback`/`high_impact[]`/`warnings[]`；单测验收同步改为「按 §4.6『S1 必填』行逐项断言，并**反证不存在**顶层 `commit`、`written`、`errors`、`timestamps` 这类自创键」；新增「`high_impact[]` 四类各一例」与「阶段占位字段不得造假（`proposals[]`/`deprecated_new_support[]` 空数组、`support_check[]`/`affected` 空值、`reconcile: {"ran": false}`、`txn_id` 不出现）」；T015 沿用的 `written[]` 一并改为 §4.6 口径（`cards`/`relations`/`note`/`open_questions`/`links[]`） | `T-…-016` `:58`（必填子集 11 项）、`:80`（单测验收）、`:81`（`high_impact[]` 四类）、`:82`（占位字段不得造假）；`T-…-015` `:61`、`:88`。全库 `written[]` 命中数 **0**，`high_impact` 命中 4 处、`default_domain_fallback` 命中 3 处 |
| **P0-4** | T015「跨领域写入 → 直接失败」「整个 apply 失败、零写入」，与 §4.5.1「W1 warning（S5 起 error）」及同 epic T012「W1 照写、退出码不受影响」自相矛盾；T018 把「跨领域写入被拒」列入「均断言零写入」的禁止项反例；T015 用 `EG-CHK-03` 指代 apply 单领域约束 | T015 改为「**W1 warning**：照常写入、进报告 `warnings[]`（带 op 下标与字段路径）、**不影响退出码**（`0`/`3`），**S5 起才升 error，S1/M1 不得提前严格化**，口径与 T-…-012 完全一致」；Acceptance 改为「op 照常写入 + `warnings[]` 含一条 W1 + 退出码 `0`/`3`，显式断言**非 `2`、非零写入**」；范围边界补「**不把 W1/W2/W3/W4/W6 升为 error**（S5/M6）」；`EG-CHK-03` 改 `EG-DOM-02`。T018 的禁止项反例拆成两条：**真 error 反例**（E3/E6/E1/E5，断言退 `2` 零写入）与 **warning 反例**（跨领域 → W1 可见、照常写入、`0`/`3`，不得零写入） | `T-…-015` `:54`（关联需求 `EG-DOM-02`）、`:64`（W1 分级）、`:76`（不升 error）、`:86`（W1 验收）；`T-…-018` `:68`（真 error 反例）、`:69`（W1 warning 反例） |

### 11.2 P1 修复（任务书列出的 7 条全部完成）

| # | 修复前 | 修复后 | 涉及文件与行 |
| --- | --- | --- | --- |
| **P1-1** 补齐 4 条 0 覆盖 S1 需求 | `EG-SRC-04`、`EG-CVG-05`、`EG-NOTE-04`、`EG-CFM-01` 在 18 个 task 中 0 引用 | 全部挂到语义最贴合的既有 task，并在 **Scope + Acceptance 双侧**落地：<br>· `EG-SRC-04` → T013（`create_card` 缺 / 空 `sources[]` → 退 `2` 零写入，writer 侧兜底）+ T012（V3/V7 属 S1，本任务实现，正反两例）<br>· `EG-CVG-05` → T014（`opposing` 一对一条、反向重复不产生第二条、无议题组）<br>· `EG-NOTE-04` → T010（笔记只出现在材料层字段，打分输入只含 `kind='card'` 且 `active`，字段级断言）<br>· `EG-CFM-01` → T016（§7.1 挂在 `eg report --last`：无审批闸门、卡即 `active`、报告不出现「待确认／待入库」中间态措辞，grep 反证） | `T-…-013` `:48`、`:59`、`:79`；`T-…-012` `:46`、`:63`、`:79`；`T-…-014` `:49`、`:58`、`:81`；`T-…-010` `:49`、`:60`、`:79`；`T-…-016` `:52`、`:64`、`:83` |
| **P1-2** 4 处 `EG-CHK-*` 语义误挂 | T014 用 `EG-CHK-02`/`EG-CHK-04`/`EG-CHK-05` 指代「ID 类型硬拦 / 悬空引用 / 端点非 active」；T015 用 `EG-CHK-03` 指代 apply 单领域约束 | T014 改挂 `EG-CVG-06`（E3 / ID 类型）、`EG-KNW-04`（四要素）、`EG-CVG-05`（opposing 去重）；E2 悬空引用在 §14 无独立 EG 条目，改为在 Scope 注明「属 §4.5.1 工程约束，无对应 EG 编号」；T015 改挂 `EG-DOM-02`。**同时把被摘下来的三条按 §14 的技术模块列归位**（避免制造新的 0 覆盖）：`EG-CHK-02`（`skill`）与 `EG-CHK-04`（`cli`，回答不自动成为知识）→ T017；`EG-CHK-05`（`cli`，未作答不影响 apply 与 commit）→ T015 | `T-…-014` `:49`、`:57`；`T-…-015` `:54`、`:66`、`:89`；`T-…-017` `:50`、`:63`、`:64` |
| **P1-3** 3 处越界引用 S2/S5 需求 ID | `EG-AGT-01`（S5）出现在 T001/T002/T003 的**关联需求**；`EG-EDIT-04`（S2）在 T015；`EG-EDIT-06`（S2）在 T004 | 三个 ID 全部**移出关联需求**：T002/T003 直接删除；T015 的 `EG-EDIT-04` 直接删除；T001 与 T004 保留一句带阶段标注的说明性正文（「§14 的 EG-AGT-01 需命令集补齐后在 **S5** 整体验收，不作为本任务的关联需求」「EG-EDIT-06 在 S2 才验收，本任务只按 F3 预留 `deleted_at`/`deleted_reason` 字段形态」）。**关联需求字段中已无任何非 S1 / 非 Deferred 的 ID** | `T-…-001` `:55`；`T-…-002`、`T-…-003` 关联需求行；`T-…-004` `:50`；`T-…-015` `:54` |
| **P1-4** T002 `depends_on` 与 DoR 不一致 | DoR 写「硬依赖 T-…-001 的骨架产出已在位」，frontmatter 却是 `depends_on: []` / `soft_depends_on: []` | 取「改 DoR」一侧（T002 只产出接口文档、不写代码不编译，确实不消费 001 的产物）：DoR 改为「**无阻塞性上游依赖**……T-…-001 与本任务**并行开工**；001 落地后把实际二进制名与 Makefile target 回填进合同（Activity Log 记一次回填）」，frontmatter 保持空依赖。DoR 与 frontmatter 现已双向自洽 | `T-…-002` `:32`（DoR）对齐 `:22`、`:23` |
| **P1-5** T001 未被任何 task 硬依赖 | 001 出边只有 003/004 的软依赖，005/006/007 连软依赖都没有 → 无 `go.mod` 也能进 `integration` | 按实际构建顺序补：**T003 把 001 提为硬依赖**（命令注册树必须挂在 001 的 `main.go`，`verify` 用 001 的 Makefile target），DoR 同步改写；T005/T006/T007 各补 `soft_depends_on: T-…-001` 并在 DoR 写明「001 未达 `integration` 前本任务不能进 `integration`」。**001 现被 1 条硬依赖 + 4 条软依赖引用** | `T-…-003` `:26-29`、`:39`；`T-…-005` `:23-26`、DoR；`T-…-006` `:23-26`、DoR；`T-…-007` `:23-26`、DoR |
| **P1-6** 6 处 Contract/Implementation 软依赖缺失 | hard-dep 002 的 015/016/017/018 无 `soft_depends_on: 003`；hard-dep 011 的 016/017 无 `soft_depends_on: 012` | 6 条全部补齐，且每条在 DoR 写明消费关系（015/016/017/018 → 003 的命令注册树与退出码映射；016 → 012 产出的 `warnings[]`/`skipped[]`/`convergence[]` 诊断载荷；017 → 012 的 error/warning 分级实现）。DoR ↔ frontmatter 双向比对**无缺口** | `T-…-015`、`T-…-016`、`T-…-017`、`T-…-018` 的 `soft_depends_on` 与 DoR 段 |
| **P1-7** 4 处排期倒挂 | ① 011 due 09-03 < 软依赖 010 due 09-04；② 014 与软依赖 013 同 due 09-05；③ 016 与软依赖 015 同 due 09-06；④ `M-001.date` 2026-09-05 < T018 due 09-08，且 T018 交付物硬编码 `2026-09-05-m1-acceptance-report.md` | ① **T011 去掉对 010 的软依赖**（`base` 的 `{id: content_hash}` 形态在 §4.5 已定死，合同不消费实现产物；DoR 明写这条豁免），011 due 保持 09-03；②③ 沿完成链逐层留 1 天缓冲重排 due；④ `M-001.date` → **2026-09-11**，T018 交付物与正文引用统一改 `2026-09-11-m1-acceptance-report.md`（3 处）。<br>另修掉审阅时未单列、但同属 G9 口径的 2 处：**004 与其软依赖 001 同 due 09-01**（001 收敛为 08-31 当日交付）、**010 与其软依赖 009 同 due 09-04**。<br>最终排期：`001` 08-31/08-31 · `010` 09-04/09-05 · `012` 09-05/09-06 · `013` 09-06/09-07 · `014` 09-07/09-08 · `015` 09-08/09-09 · `016`/`017` 09-09/09-10 · `018` 09-10/09-11（其余 task 日期不变；M1 收口由 09-08 顺延至 09-11，仍早于 EPIC `target` 2026-09-15）。**脚本断言：任一 task 的 due ≥ 其全部硬 / 软依赖 due + 1 天，`M-001.date` ≥ T018 due，T018 交付物文件名日期 == T018 due，全部通过** | `T-…-001`/`010`/`012`/`013`/`014`/`015`/`016`/`017`/`018` 的 `start`/`due`；`T-…-011` `soft_depends_on` + DoR；`T-…-018` `:23`、`:64`、`:93`；`milestones/M-001-m1.md:7` |
| **P1-9** T017 漏 §8 第 0 步与 3 条禁止项 | 调用顺序从 `eg capture` 起；禁止项缺 3 条，`EG-AGT-02`「十一条边界逐条可定位」无法验收 | Scope 补 **第 0 步「Agent 自行抓取并清洗正文（CLI 不做任何网络请求）」** 与 **§8.1 首行失败分支「抓取失败 / 正文为空 → 不调 `eg capture`，直接在报告写明失败原因与 URL，不落任何文件」**；禁止项补齐 3 条：**不得把 `active` 说成「已确认」「已入库」**、**不得把多跳推导结论沉淀成新卡**、**不虚构材料来源与依据，文章没表达的部分留空**；Acceptance 改为「与 §8.2 code-block 的**十一条边界逐条对齐**（编号一致，缺一不通过）」并新增「第 0 步与首行失败分支可 grep 定位」 | `T-…-017` `:56`（第 0 步）、`:57`（失败分支）、`:71`、`:72`、`:73`（三条禁止项）、`:93`（十一条边界对齐）、`:94`（可定位断言） |

**本次未修（超出任务书授权范围，保留为已知项）**：P1-8（工作量不可行：18 个 task 单一 owner）需要调整 `roster` 或整体拉长排期，属排产决策；P1-10 的 M-001 Description 补充说明、P2-1 ~ P2-8（W3 用例越界、`ops[]` 空列表 warning 编号、`EG-CHK-06` 括注、交叉验收条款、3 条无机器判据的 Acceptance、`code_paths`/`deliverables` 对齐、`references` 全空、`apply --dry-run` 未进 T002 合同）均为建议级，本次按「不扩大范围」要求未动。

### 11.3 复验结论

| 门禁 | 结果 |
| --- | --- |
| `validate_tasks.py`（18 个文件必填字段 / 四段正文 / `{path, requires}` deliverable / `design_doc` 锚点 / 依赖 ID 可解析 / 无环） | ✅ `OK: all 18 task files valid` |
| `teamwork status`（overview / project / initiative 三层） | ✅ 三层 dashboard 均正常再生成 |
| `teamwork pmo-sweep --dry-run` | ✅ `[pmo-sweep] no tasks to unblock` |
| 依赖图：无环 / 无孤儿 / 无自依赖 | ✅ 全部通过；`T-…-001` 现被 1 条硬依赖引用 |
| `(fill in)` 占位残留 | ✅ 0 处 |
| G1 关系类型与 F4 一致 | ✅ `contradicts`/`refines`/`depends_on` 仅出现在「集合外取值必须拒绝」的反例表述中 |
| G2 需求 ID 全部真实存在 | ✅ `EG-REL-*` 命中 **0**；task 中出现的 EG-ID 全部 ⊆ §14 追溯矩阵 66 条 ∪ Deferred（`EG-DOM-06`/`EG-DOM-08`） |
| G3 报告字段与 §4.6 一致 | ✅ 11 项逐字齐备；`written[]` 命中 **0**；`high_impact` 命中 4、`default_domain_fallback` 命中 3 |
| G4 W 类一律 warning | ✅ T012 / T015 / T018 对 W1 口径一致，无「零写入 / 整个 apply 失败 / 退 `2`」表述 |
| G5 S1 44 条需求 100% 被引用 | ✅ **44 / 44**，`miss` 为空 |
| G6 无越界与误挂引用 | ✅ 关联需求字段中无非 S1 ID；`EG-CHK-02/03/04/05` 均归位到 §14 指定的技术模块 |
| G7 DoR ↔ frontmatter 依赖一致 | ✅ 双向比对无缺口 |
| G9 排期自洽 | ✅ 全部满足「due ≥ 依赖 due + 1 天」，`M-001.date` ≥ T018 due，交付物文件名日期 == due |

*修复人：项目维护者　|　修复日期：2026-08-31　|　技术方案基线：飞书 wiki `LB73w54iOiXN96keHVLcK38JnSe`（全程只读，未做任何修改）*

---

## 十二、第二轮修复记录（2026-08-31）

> 本节为**追加**内容，第一至第十一节一字未改写。
> 本轮目标：把第九节中第一轮**尚未闭环**的问题（P1-8、P1-10 与 P2-1 ~ P2-8）逐条处理干净。
> 改动范围严格限定在 18 个 task 文件、`milestones/M-001-m1.md`、`EPIC.md` 与本报告；**未新增 / 删除 task，未扩大产品范围，未把 S2–S5 内容塞进 M1，六条冻结合同未动**。技术方案飞书 wiki（revision 124）全程**只读**核对，未做任何修改。
> 行号均指**本轮修复后**文件的当前行号。

### 12.1 P1 剩余 2 条

| # | 修复前 | 修复后 | 涉及文件与行 |
| --- | --- | --- | --- |
| **P1-8** 工作量 / 排产不可行 | 18 个 task 全部 `owner: ikaqiu`；08-31 / 09-01 / 09-02 / 09-03 各有 3 个任务同日开工；每 task 工期基本 1 天；硬 + 软完成链 12–13 层却压在 9 个日历日（08-31 → 09-08，第一轮已顺延至 09-11）内；`EPIC.target` 2026-09-15 早于真实收口日且未给 M2 留时间 | **先核 roster 再定方案**：`roster.yaml` 的 `developers` 与 `EPIC.roster` **均只有 `ikaqiu` 一人**（uid 158614），故「按真实成员重新分配 owner」在文档层面**不可执行**——18 个 task 的 `owner` 与 `filer` **全部保持不变**，并在里程碑与 EPIC 中**显式登记这是真实容量约束而非漏填**。据此按「单人容量 + 依赖顺序」串行化重排：<br>① **容量口径写死**：同一 owner 任一日历日**在途 task ≤ 2**（在途 = `start ≤ 日 ≤ due`）；<br>② **关键路径与完成链登记**：硬依赖关键路径 4 层（`001`→`004`→`005`→`006`，并列支线 `002`→`003`→`008`）；硬 + 软**完成链 12 层**（`001`→`004`→`005`→`006`→`009`→`010`→`012`→`013`→`014`→`015`→`016`／`017`→`018`）；单点风险 `002`（8 下游）/`011`（7 下游）/`006`/`018`（入度 12）；<br>③ **工期按复杂度分配**（不再一律 1 天）：`006`/`013`/`018` 各 3 天，`002`/`003`/`011`/`012`/`014`/`015`/`016` 各 2 天，其余 1 天；<br>④ **全量重排 `start`/`due`**（18 个 task 全部改写）：`001` 08-31/08-31 · `002` 08-31/09-01 · `003` 09-02/09-03 · `004` 09-01/09-01 · `005` 09-02/09-02 · `006` 09-03/09-05 · `007` 09-04/09-04 · `008` 09-05/09-05 · `009` 09-06/09-06 · `010` 09-06/09-07 · `011` 09-07/09-08 · `012` 09-09/09-10 · `013` 09-09/09-11 · `014` 09-11/09-12 · `015` 09-12/09-13 · `016` 09-13/09-14 · `017` 09-14/09-14 · `018` 09-15/09-17；<br>⑤ **真实完成日期 2026-09-17**：`M-001.date` 09-11 → **09-17**，`EPIC.target` 09-15 → **09-30**（= M1 收口 + M2 单人预留 ~2 周，并注明 M2 拆 task 后需重新校准）；<br>⑥ 三份合同 / 报告类交付物文件名日期随 due 同步：`2026-08-31-eg-cli-contract.md` → **`2026-09-01-eg-cli-contract.md`**、`2026-09-02-changeplan-contract.md` → **`2026-09-08-changeplan-contract.md`**、`2026-09-11-m1-acceptance-report.md` → **`2026-09-17-m1-acceptance-report.md`**，并把全部下游引用（DoR / Scope / Acceptance）一并改齐，旧文件名全库残留 **0**。<br>**脚本断言全部通过**：`due` ≥ 全部硬 / 软依赖 `due` + 1 天；`start` ≥ 全部**硬**依赖 `due` + 1 天；任一日在途 ≤ 2；`M-001.date` ≥ `T-…-018.due`；交付物文件名日期 == 对应 due | 18 个 task 的 `start`/`due`（frontmatter 第 8–9 行）；`T-…-002` `:19`、`:44`、`:67`；`T-…-011` `:19`、`:46`、`:69`；`T-…-018` `:24`、`:65`、`:94`；`T-…-003` `:39`、`:72`，`T-…-012` `:39`，`T-…-013` `:41`，`T-…-014` `:42`，`T-…-015` `:46`，`T-…-016` `:43`，`T-…-017` `:42`（合同文件名引用）；`milestones/M-001-m1.md` `:7`、`:33-56`（容量约束 / 关键路径 / 排期表 / 真实完成日期）；`EPIC.md` `:8`、`:18-38`（容量与分工、里程碑与目标日期、排期自洽性口径） |
| **P1-10** M1／§16.1 边界不一致未声明 | `M-001-m1.md` 的 Description 只抄了 §16.1 的 M1 交付列表；`eg context`（T010）、B1–B4 单测（T006/T007）、`convergence[]` 逐卡记录（T011/T012/T017）在 §16.1 属 **M2** 却全标 M1，里程碑对此**零声明**；`append_card`/`add_open_question`、「重复加工幂等」同样未登记 | 只读核对飞书技术方案 **§16.1**（revision 124）后，在 `M-001-m1.md` 新增「**M1 范围与技术方案 §16.1 的对应关系（含有意差异及理由）**」：先确认 18 个 task **完全覆盖** §16.1 的 M1 行（`init`/`config`、`capture`、`write_note`、`create_card`、`add_material_rel`/`add_relation`、`git commit`、最终报告，包 `model`/`mdfile`/`store`(简化)/`git`/`plan`/`report`/`cli`），再用表格逐条登记**全部 6 项差异及理由**：<br>· `eg context` 提前（§8 主链路第 ② 步、安全底线 **B3** 的 `content_hash` 唯一来源，M1 只做扫描版白名单输出）；<br>· B1–B4 单测提前（§9.1 明写四条底线「S1 必须实现且不可放宽」，M1 只做单测 + e2e 复现、不做并发用例）；<br>· `convergence[]` 提前（ChangePlan **顶层键**必须一次定死，M1 只做「逐卡条目 + 三维度 + 缺条目 W5」，不做收敛体验与提案去重）；<br>· `append_card`/`add_open_question`（§16.2 把 S1 的 7 个 op 整体列为「开工前必须定死」，属对 §16.1 交付列表的**补全**）；<br>· 「重复加工幂等」（EG-SRC-01 / EG-NOTE-05 在 §14 标 **S1**，不验则与「报告如实」冲突）；<br>· `search`/`card show`/`rel` **与 §16.1 一致无差异**，仅统一措辞。并明确列出 **M2 剩余范围**与 **M3–M6 不做项**。同时把 T003 的反向措辞改齐：「S1 未覆盖」→「**M1 未实现（S1 命令，M2 落地）**」，并新增 `rel add\|remove` 写路径 M1 不注册、`--help` 标注 M2 | `milestones/M-001-m1.md` `:16-31`（§16.1 对应关系与差异表 + M2/M3–M6 边界）；`T-…-003` `:56`（措辞口径 + 反证 grep）、`:58`（`rel` 读写路径分开）、`:75`（Acceptance 反证） |

### 12.2 P2 全部 8 条

| # | 修复前 | 修复后 | 涉及文件与行 |
| --- | --- | --- | --- |
| **P2-1** T014 W3 用例越界 | 「**W3**：`target` 卡为 `deprecated` → 关系照写 + warning，不拦截」被当作 M1 常规用例，而 §4.5.1 标注 W3「**S2 起判定**、S5 起 error」，且 S1 无 `deprecate` 命令、无失效卡 | **修复**。三处口径统一为「分级形态 S1 就位、**判定 S2 起**」：T014 Scope 注明「§4.5.1 原文 S2 起判定 / S5 起 error，S1 无失效卡（§14 EG-VIEW-07 注），M1 正常链路**不会产生** W3，本任务只锁形态不实现判定语义」，Acceptance 改为「**W3 判定占位（不作 M1 门禁）**：若保留用例须手工构造 `status: deprecated` 卡，用 `t.Skip`／build tag 标注，**不计入 `verify.test` 门禁**」；T012 的 W1–W8 清单内为 W3 补同样标注，并把 W3/W7 从「各有独立用例」中摘出单列为「只锁形态、不作门禁」；T011 合同新增「W3 阶段口径须逐字写明」的 Scope 与 Acceptance | `T-…-014` `:63`（Scope）、`:86`（Acceptance）；`T-…-012` `:56`（W 清单）、`:76`（用例分档）；`T-…-011` `:52`（合同要求）、`:74`（Acceptance） |
| **P2-2** `ops[]` 为空的 warning 误编号 W5 | T012 写「`ops[]` 为空 → **W5 类** warning」，而 §4.5.1 的 W5 是「`convergence[]` 缺条目或三维度不齐」 | **修复**。改为「**未编号 warning**（§4.5 `ops[]` 行：空列表 → warning + 零写入报告）」，并显式写「**不得复用 `W5`**——W5 是 `convergence[]` 缺条目」；同时在 T011 合同侧把它**单列登记**为「未编号 warning（§4.5 `ops[]` 空列表）」并标注「§4.5.1 表未收录该编号」（即评审建议的「技术方案 §4.5.1 表待补该编号」在合同中留痕，**不擅自新编号**）；T012 新增用例断言「该诊断的编号字段**不是 `W5`**」 | `T-…-012` `:60`（Scope）、`:77`（Acceptance）；`T-…-011` `:52`（合同登记）、`:73`（Acceptance grep 反证） |
| **P2-3** T013 括注「EG-CHK-06 属 S2」与 §14 不符 | 「S1 不做『当前有效问题块』的替换（**EG-CHK-06 属 S2**）」 | **修复**。改为引 §14.4 原文（`EG-CHK-06` \| **S1** \| `index.checks` \| 历史块只追加 \| 历史问题文本原样保留，不折叠为引用）后明确：**EG-CHK-06 是 S1，由本任务实现**；只有「当前有效问题块的替换」（`replace_block`）属 **S2**，本任务不做 | `T-…-013` `:63` |
| **P2-4** 「交叉验收」到期日不可判定 | T002（due 09-01）/ T011（due 09-08）的 Acceptance 末条要求「下游实现者（最早 09-09 才开工）只读本文即可开工、无需追问」，在合同 due 当日无法判定 | **修复**。两条均改为可判定形式：「合同达 `integration` 后 **24h 内下游就参数／退出码／输出结构（或字段表／校验分级）提出的澄清请求为 0 条**；有则先回本任务补合同并**重新计时**」，澄清计数与结论记入 Activity Log，并在括注中说明原表述为何不可判定 | `T-…-002` `:74`；`T-…-011` `:79` |
| **P2-5** 3 条 Acceptance 无机器判据 | T001 第 5 条「reviewer 可据此逐条对照 §13 表格」；T013 第 10 条「用 Obsidian 打开目测」；T017 第 8 条「由一名未参与本 epic 的 coding agent 独立跑通」 | **修复**（三条全部换成机器判据或降级为非门禁）：<br>· T001 → 脚本断言 `internal/*/doc.go` **首行匹配 `^// \[S[1-5]\]`**（9/9），并把 `doc.go` 声明的允许依赖、`go list -deps` 实际依赖、§13 表格**三者逐包比对必须相等**，人工对照仅作补充证据；<br>· T013 → Markdown 解析器断言 ① frontmatter 可解析为 map 且键集合与 §4.1／§4.3 一致 ② 正文**恰 5 个 H2**、标题文本与顺序逐字相等 ③ 无 H1、无重复分区，Obsidian 目测降为 Activity Log 补充证据、**不作 `integration` 门禁**；<br>· T017 → 外部 coding agent 验证改为「**附加验证，不作门禁**」（资源不可控），门禁由「十一条边界逐条对齐」「第 0 步与失败分支可 grep 定位」「样例可被 `--dry-run` 接受」三条机器判据承担 | `T-…-001` `:78`；`T-…-013` `:89`；`T-…-017` `:99` |
| **P2-6** frontmatter 内部不一致 | T002 `code_paths` 为 `github.com/ikaqiu-Lemon/EverGreen/internal/cli/` + `evergreen/docs/specs/`，deliverable 在 `projects/evergreen/s1_main_flow/docs/specs/`；T015 `code_paths` 缺 deliverable 中的 `internal/plan/executor.go`；T018 `code_paths` 写 `evergreen/docs/m1-acceptance.md`，deliverable 在 `projects/…/docs/specs/`；T011 同类问题 | **修复**。逐一对齐 `code_paths` ⊇ `deliverables[].path`：T002 `code_paths` 改为 `projects/evergreen/s1_main_flow/docs/specs/`（本任务只出文档、不写代码）；T011 同改；T015 补 `github.com/ikaqiu-Lemon/EverGreen/internal/plan/executor.go`；T018 补 `projects/evergreen/s1_main_flow/docs/specs/`。脚本断言 18 个 task **全部满足 `code_paths` ⊇ deliverables 路径前缀** | `T-…-002` `:15`；`T-…-011` `:15`；`T-…-015` `:18`；`T-…-018` `:18` |
| **P2-7** `references: []` 全空 + 缺逐条核对指令 | 18 个 task 的 `references` 全为空数组，权威飞书 URL 只在施工索引里；本次 3 条 P0 恰由「未逐条对照权威表格」造成 | **修复**。① 18 个 task 的 `references` 全部补权威技术方案 URL（`https://docs.example.invalid/evergreen/design`，只读引用，飞书文档未做任何修改），脚本断言 **18/18 非空**；② 把 T011 已有的「逐条对照 §x 表格、条目数完全对应」型**强制核对指令**复制进三个高风险 task 的 DoR：T014 对照 **§1.2 F4 + §6.1**（材料 `rel` 恰 3 值 / 论证 `type` 恰 4 值）、T015 对照 **§4.5.1**（error 恰 E1–E6、warning 恰 W1–W8 + I1，并特别确认 **W1 是 warning**）、T016 对照 **§4.6**（S1 必填恰 11 项，`git.commit` 是嵌套键不是顶层 `commit`，不得自创键） | 18 个 task 的 `references` 字段；`T-…-014` `:45`；`T-…-015` `:50`；`T-…-016` `:48` |
| **P2-8** `apply --dry-run` 未进合同 | `--dry-run` 是 T015 自带 flag，§7.1／§7.2 未定义，T002 九命令参数表也没有，但 T017 Acceptance 第 6 条以它作验收手段 | **修复**。T002 合同新增专条：`apply --plan <file\|-> [--dry-run]` 进九命令表；语义「完整走 E1–E6 校验与 op 展开、输出**将**写入的文件与分区清单、**零写入零 commit**，退出码语义不变（校验失败仍退 `2`）」；并要求合同**显式标注「§7.1/§7.2 未定义，属 M1 工程 flag」**且说明它**不新增任何产品能力**（不改写入语义、不产新产物、不引入新 `verb`）——以此满足「不扩大产品范围」的硬约束。Acceptance 增「`--dry-run` 语义写入合同（三句明文 + grep 命中 ≥ 1）」，并把「与 §7.1 逐行对齐」收敛为「**命令集**无遗漏无新增，参数层唯一超出项是 `--dry-run` 且已标注出处」。T017 Acceptance 改为「`--dry-run` 语义以 **T002 合同的 `apply` 参数表**为准，不是本任务自造的能力」。三处（002/015/017）口径一致 | `T-…-002` `:15`（九命令表带 `--dry-run`）、`:47`（语义与标注要求）、`:68`（命令集对齐口径）、`:69`（Acceptance）；`T-…-017` `:97` |

### 12.3 接受现状（不修改）+ 理由

| 事项 | 出处 | 结论与理由 |
| --- | --- | --- |
| **按 roster 真实成员重新分配 `owner`** | 第九节 P1-8 建议① | **接受现状**：`roster.yaml` 的 `developers` 与 `EPIC.roster` 均只含 `ikaqiu`，**无第二名可用成员**，重新分配 owner 会写出不存在的责任人（且 `filer` 不得改动）。改按建议②执行：单人容量约束 + 串行化重排 due，并在 `M-001-m1.md` `:33-56` 与 `EPIC.md` `:18-38` **显式记录容量约束、关键路径与串行化后的真实完成日期 2026-09-17**，不留自相矛盾的排期。若后续 roster 新增成员，已在 `EPIC.md` `:24` 写明优先拆分的两条支线与调整方式 |
| **T013 / T018 按 §8.5 再拆分** | 第八节 §8.5 + P1-8 建议② | **接受现状**：本轮硬约束为「**不新增 / 删除 task**」，拆分必然新增 task。改为**延长工期至各 3 天**（`013` 09-09/09-11、`018` 09-15/09-17）并在 `M-001-m1.md` `:54` 登记；若后续确认必须拆分，走 `projects/evergreen/backlog/` 另行立项，不在 M1 内变更 task 集合 |
| **T001 + T003 合并为「CLI 骨架与工程脚手架」** | 第八节 §8.5「偏小，可考虑合并」 | **接受现状**：同属「不新增 / 删除 task」约束；且原表已注明「保持拆分也合理（`003` 需等 `002` 合同）」——`001` 在 08-31 即可与 `002` 并行起，合并反而把它压到 `002` 合同之后，延长关键路径。不改 |
| **技术方案 §4.5.1 表缺「`ops[]` 空列表」warning 编号** | P2-2 建议 | **接受现状（不改飞书文档）**：技术方案为**只读**引用，本轮不得修改，也不擅自新编号。处理方式是在 T011 合同中登记为「未编号 warning（§4.5 `ops[]` 空列表）」并标注「§4.5.1 表未收录该编号」，作为**技术方案待补项**在此留痕，待方案 owner 决定是否补编号 |
| **`apply --dry-run` 未在技术方案 §7.1/§7.2 定义** | P2-8 | **接受现状（不改飞书文档）**：仅在 T002 合同内标注为「§7.1/§7.2 未定义、M1 工程 flag、不新增产品能力」，形成有据可查的**受控偏差**，而非反向修改权威方案 |

### 12.4 第二轮复验结论

| 门禁 / 检查 | 结果 |
| --- | --- |
| `validate_tasks.py`（18/18 必填字段、四段正文、`{path, requires}` deliverable、`design_doc` 锚点、依赖可解析、无环） | ✅ `OK: all 18 task files valid` |
| `teamwork status`（overview / project / initiative 三层） | ✅ 三层 dashboard 均正常再生成 |
| `teamwork pmo-sweep --dry-run` | ✅ `[pmo-sweep] no tasks to unblock` |
| 依赖图：无环 / 无孤儿 / 无自依赖；`T-…-001` 被硬依赖引用 | ✅ 全部通过 |
| `(fill in)` 占位残留 | ✅ 0 处 |
| G1 关系类型与冻结合同 F4 一致 | ✅ `support`/`against`/`context` 与 `derives`/`supports`/`limits`/`opposing` 齐备；`contradicts`/`refines`/`type: depends_on`/`rel: supports` 仅出现在反例表述 |
| G2 `EG-*` ID 全部真实 | ✅ 全部 ⊆ §14 追溯矩阵 66 条 ∪ 4 条 Deferred（共 70 条）；`EG-REL-*` 命中 **0** |
| G3 报告字段与 §4.6 一致 | ✅ S1 必填 11 项逐字齐备；`written[]` 命中 **0**；`high_impact` / `default_domain_fallback` 均命中 ≥ 1 |
| G4 W 类一律 warning | ✅ 012 / 015 / 018 对 W1 口径一致，无「零写入 / 整个 apply 失败 / 退 `2`」表述 |
| G5 S1 需求覆盖 | ✅ **44 / 44**，`miss` 为空 |
| G6 无越界与误挂引用 | ✅ 关联需求字段无非 S1 / 非 Deferred ID；`EG-CHK-02/03/04` 不在 T014/T015 误挂（`EG-CHK-05` 按 §14 模块 `cli` 正确归属 T015） |
| G7 DoR ↔ frontmatter 依赖双向一致 | ✅ 无缺口（含简写与区间形式的 task 引用） |
| G8 依赖建模完整 | ✅ Contract/Implementation 6 处软依赖齐备 |
| G9 排期自洽 | ✅ `due` ≥ 全部依赖 `due` + 1 天；`start` ≥ 全部硬依赖 `due` + 1 天；`M-001.date`(09-17) ≥ `T-…-018.due`(09-17)；三份交付物文件名日期 == 对应 due；旧文件名残留 **0** |
| G10 工作量可执行 | ✅ 任一 owner 任一日历日在途 task ≤ 2；`013`/`018` 未拆分但工期已延长并在里程碑登记 |
| G11 结构规范零回归 | ✅ 见上；`references` 18/18 非空 |
| G12 内部无自相矛盾 | ✅ W1 领域越界（012/015/018）、报告必填字段（015/016/018）、关系类型（004/012/014/017）、M1/M2 边界（M-001/003/010）、`--dry-run` 语义（002/015/017）、W3 阶段口径（011/012/014）、`ops[]` 空列表编号（011/012）、EG-CHK-06 阶段（013）逐组一致 |

**第二轮结论**：第九节 P0（4）+ P1（10）+ P2（8）共 22 条问题**已全部闭环**——18 条按建议修复，4 条「建议但不宜改」在 12.3 登记「接受现状 + 理由」，无悬空未处理项。第十节的 12 条再评审门禁 **G1–G12 全部满足**。

*修复人：项目维护者　|　修复日期：2026-08-31　|　技术方案基线：飞书 wiki `LB73w54iOiXN96keHVLcK38JnSe` revision 124（全程只读，未做任何修改）*
