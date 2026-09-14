# M3 开工前置裁决（A-23 提案写入通道 / A-24 `remove_relation` 落盘语义）

- **产出 task**：T-evergreen.s1_main_flow-158614-032（Contract 型，不写代码）
- **设计依据**：`docs/specs/2026-10-10-m3-proposal-state-contract.md`（§4.5 引用、§5.2、§8.1、§10 登记表）、`docs/specs/2026-10-10-m3-user-authorization-contract.md`（§1、§2.6、§6 U-07 / U-09、§9 A-15）
- **关联需求**：EG-EDIT-06、EG-CFM-05
- **代码事实核对基线**：`evergreen/` HEAD `f456b0e`。§3 / §4 论据里指向**既有**代码的 grep 与行号
  均在该提交上实测（`internal/cli/apply.go`、`rel_add.go`、`init.go`、`capture.go`、
  `internal/model/artifact.go`、`internal/model/enums.go`、`internal/store/relation.go`、
  `internal/mdfile/section.go`、`internal/query/relation.go`）；
  而第 8 列「机器反证」中指向 `internal/proposal/` 的命令属**未来验收形态**——该目录在
  `f456b0e` 下**尚不存在**（M3 未开工），故其期望值是施工后才可测的判据，**不是**已实测结论。

## 1. 本文档的性质与自律条款

本文档**只提供封闭选项、改动面与机器反证形态**，**不得替 owner 拍板**。因此：

- 第 4 列 `裁决结论（owner 正式裁决）` 只能由 owner 填写；owner 发话前**只能**逐字写 `未知 / 待 owner 确认`（2026-09-02 owner 已发话，见 §7）；
- Agent 依据实证给出的推荐值只进第 9 列 `Agent 建议裁决（仅建议，不构成裁决）`，两列**严格分离**、机器判定各查各列；**两列取值即使相同，其效力也不同**：第 4 列带裁决人与裁决日期，第 9 列**永远不得**带裁决人或日期；
- 产出 task T-…-032 的升 `done` 条件**只有一个**：owner 在第 4 列逐字填入封闭选项之一并署名日期（Acceptance「未裁决时的状态一致性」第一分支）；**建议值本身永不构成升 `done` 的理由**；
- 9 个硬下游对 032 的 `depends_on` **一条不摘**（裁决落地后依旧不摘，硬依赖边是结构事实，不随裁决消失）。

## 2. 裁决登记表（两行，九列）

> 第 4 列 = **owner 正式裁决**（已由 owner 于 2026-09-02 填入，附加约束逐字见 **§7**）；
> 第 9 列 = **Agent 建议**（历史留痕，仅供追溯，**不构成**裁决依据）。两列**分列不同表列**，机器判定按列位取值。

| 编号 | 冲突摘要 | 封闭选项 | 裁决结论（owner 正式裁决） | 裁决人 | 裁决日期 | 受影响 task | 机器反证 | Agent 建议裁决（仅建议，不构成裁决） |
|-|-|-|-|-|-|-|-|-|
| **A-23** | 提案的创建与状态变更是否走 ChangePlan：提案合同 §4.5「ChangePlan 是 **Agent → CLI** 的唯一程序化写入通道」 vs §10.2 时序第 4 步「`C->>FS: 写 proposals`」（CLI 直接写 `proposals/`）；§4.5 op 表**没有** `create_proposal` / `approve_proposal` / `reject_proposal` 三个 op | `走 ChangePlan` \| `CLI 直写例外` | `CLI 直写例外` | `项目维护者 <maintainer@example.com>` | `2026-09-02` | 033 / 034 / 035 / 036 / 037 / 040 / 041 / 046 / 047 | 选项①：`grep -rn "os.WriteFile\|store\." internal/proposal/ \| grep -v _test.go \| wc -l` → **0**；选项②（**已裁决采用**，且按 §7 加严）：`grep -rn "content_hash\|ContentHash" internal/proposal/ \| wc -l` → **≥ 1**、`grep -rn "internal/plan" internal/proposal/ \| wc -l` → **0**、`grep -rnE "os\.(WriteFile\|Create\|OpenFile)\|ioutil\.WriteFile" internal/proposal/ \| grep -v _test.go \| wc -l` → **0** | `CLI 直写例外` |
| **A-24** | `remove_relation` 是物理移除还是标记删除：§5.2 逐字「关系记录不被物理删除」「端点有效性过滤，记录不动」讲的是**逻辑删除**旁路，`remove_relation` 自身的落盘语义原文未定义；F4 关系类型集合里也没有「已移除」维度 | `物理移除` \| `标记删除` | `物理移除` | `项目维护者 <maintainer@example.com>` | `2026-09-02` | 037 / 038 / 041 / 043 / 044 / 047 | 选项①（**已裁决采用**，且按 §7 加严）：删除后目标卡 frontmatter 的 `relations[]` 条目数 **−1** 且 Git diff 可见；未命中时零写入零 commit；`grep -rn "removed_at\|RelationID" internal/ \| grep -v _test.go \| wc -l` → **0**；选项②：新增 frontmatter 子字段 + `eg rel` 查询过滤（需 F4 另行豁免） | `物理移除` |

## 3. A-23：`proposal` 包与 `plan` 包的边界

无论 owner 选哪一侧，下述边界都必须成立（这是本节的不可动摇结论）——**`proposal` 包与 `plan` 包的边界**按「校验 / 写盘 / commit」三职责逐项定死：

| 职责 | 选项①`走 ChangePlan` | 选项②`CLI 直写例外` |
|-|-|-|
| 提案 schema 与状态机校验 | `internal/proposal/`（纯函数） | `internal/proposal/`（纯函数） |
| 提案文件写盘 | `internal/plan` 展开 → `internal/store` | `internal/proposal` 自带写口，复用 `store.ContentHash` 与 B1–B4 前置检查 |
| commit | `runPlan` 统一编排 | 提案命令自发 commit（与 `eg init` / `eg capture` 同形） |
| 权威 Markdown（`sources/` / `domains/`）的写入 | 恒为 `internal/plan` → `internal/store` | 恒为 `internal/plan` → `internal/store`（提案写路径**一个字节都不碰**权威 Markdown，反证 `grep -rn "vault/cards\|vault/notes\|vault/sources" internal/proposal/ \| wc -l` → **0**） |

**U-07 的精确适用范围**：U-07「绕过 ChangePlan 直接调 `store` 属不可授权项」约束的是 **Agent 自动路径**；用户发起的 CLI 命令（`eg init` / `eg capture` / 提案三命令）不在其列。若不写清这一句，U-07 会被误读为全局禁令，而 `eg init` / `eg capture` 在 M1 / M2 已按「CLI 直连 `internal/store` + 自发 commit」验收通过。

**Agent 建议裁决（待 owner 确认）= `CLI 直写例外`**，依据：① §4.5 原句主语是 Agent；② 全仓调 `runPlan` 的**恰两处**是 `internal/cli/apply.go` 与 `internal/cli/rel_add.go`，而 `internal/cli/init.go`、`internal/cli/capture.go` 已是 CLI 直连 `internal/store` 的既成形态；③ 提案是**授权凭证**、不是知识产物，`proposals/` 既不属五分区也不属四类产物；④ 授权后的那次真实知识改动仍是 ChangePlan 的 `delete` op，原则由该处守住。

**若 owner 改判 `走 ChangePlan`**：返工面 = 033 / 034 / 037（大）+ 040 / 041 / 046 / 047（中）+ 038 授权矩阵行数重算（大），并撞上 `internal/model/enums.go` 的 `Verb` 恰 6 值封闭（`internal/model/model_test.go` 逐字断言 `len(seen) != 6`）——需一次显式豁免申请。

## 4. A-24：`remove_relation` 的落盘语义

**原文口径复述**：§5.2 五动作表第 4 行「逻辑删除」的「保留什么」列逐字写「关系记录不被物理删除」，同表第 3 行写「端点有效性过滤，记录不动」。该口径的适用场景是**逻辑删除**（删卡时不连带清理指向它的关系记录），而 `remove_relation` 是用户显式点名删一条关系的**第六个动作**，主语与场景都不同。

**W10 不变**：无论 owner 选哪一侧，「`remove_relation` 未命中任何既有关系 → 幂等 no-op + warning + 零 commit、不拦截其余 op」这条口径**一字不改**；W10 只定义「找不到目标时怎么办」，不预设命中时的落盘语义。

| 项 | 选项①`物理移除` | 选项②`标记删除` |
|-|-|-|
| F4 兼容性 | 兼容（不新增字段） | 需为 `Relation` 增第 4 个子字段 → F4 封闭集合须 owner 另行豁免 |
| 既有幂等去重 | 不受影响 | `internal/store/relation.go` 的整结构体相等去重必须改成逐字段比较并特判墓碑 |
| 新增写原语 | 需要「frontmatter 序列条目按区间剪除」，可照 `internal/mdfile/section.go` 的 `(*Unprocessed).Cut` 范式（区间拼接 + 四道自检 + `ErrEntryNotFound` 供上层判幂等） | 需要「就地改写子字段」原语，量级相当 |
| B3 | 不放宽：剪除同样走 `ExpectedHash` 前置比对，冲突进 `skipped[kind=file_changed]` | 同左 |
| 读路径影响 | 无（`internal/query/relation.go` 已有悬空 / 缺失关系的既定诊断口径） | `card show` / `search` / `context` / `rel` 四条读路径的 JSON 输出形状需复核 |

**Agent 建议裁决（待 owner 确认）= `物理移除`**，依据：① `internal/model/artifact.go` 的 `Relation` 恰 `Type` / `Target` / `Reason` 三字段，而同文件的 `Card` / `Note` 都带 S2 预留的删除位——schema 作者为**产物**预埋逻辑删除位、唯独没为**关系条目**预埋；② F3「恢复时被迫猜删除前状态」的立论前提在关系上不成立（关系没有 status 维度，重新 `eg rel add` 即可完整复原，原因由 Git 历史承载）；③ 加墓碑字段会把既有幂等去重变成 bug（墓碑参与比较 → 同对出现两条记录；不参与比较 → 删了再加静默 no-op）。

**若 owner 改判 `标记删除`**：返工面 = 044（大）、037 / 043（中）、047（小），外加 1 处冻结产物 schema 扩字段、4 条读路径输出形状复核、1 次 F4 豁免申请。

## 5. 两条结论的机器判定入口

| 判定对象 | 命令 | 期望 |
|-|-|-|
| 两行登记表存在且恰两行 | `grep -cE '^\|\s*\*\*A-2[34]\*\*\s*\|' "$DOC"` | `2` |
| 结论列取值封闭 | `grep -oE '走 ChangePlan\|CLI 直写例外\|物理移除\|标记删除\|未知 / 待 owner 确认' "$DOC" \| sort -u` | ⊆ 五值集合 |
| 第 9 列与第 4 列分离（**按列位判定，不按取值判定**） | 表头第 4 列逐字 == `裁决结论（owner 正式裁决）`、第 9 列逐字 == `Agent 建议裁决（仅建议，不构成裁决）`；A-23 / A-24 两行的第 4 列 ∈ 四个封闭选项、第 5 列含裁决人、第 6 列含裁决日期；第 9 列**不含**裁决人与日期 | 成立 |
| owner 裁决已落地（2026-09-02 起） | A-23 行第 4 列逐字 == `CLI 直写例外`、A-24 行第 4 列逐字 == `物理移除`；裁决人逐字含 `项目维护者 <maintainer@example.com>`、裁决日期逐字 `2026-09-02` | 成立 |
| 10 条附加约束逐字在册 | §7 的 5 + 5 条逐字命中（见 §7 的判定命令表） | `10` |
| 自律条款成文 | `grep -qF '不得替 owner 拍板' "$DOC"` | 退 `0` |
| 包边界成文 | `grep -qF 'proposal 包与 plan 包的边界' "$DOC"` | 退 `0` |
| 原文口径复述 | `grep -qF '关系记录不被物理删除' "$DOC"` | 退 `0` |

## 6. 施工期的诚实登记（**历史快照**，2026-09-02 owner 裁决**之前**；已被 §7 取代）

> **本节整节为历史留痕**：它记录的是 owner 发话**之前**的状态与一次已撤回的虚假陈述。
> 本节任何一句都**不得**被引用为当前事实——当前事实一律以 **§7** 为准。

**更正声明**：本节曾写「用户方显式授权按第 9 列建议裁决先行施工，033–037 已落地」。
该陈述**查无实据，现予撤回**——2026-09-02 收口复核逐条实测：

| 原陈述 | 实测 | 结论 |
|-|-|-|
| 用户方显式授权「按第 9 列建议先行施工」 | 全仓无任何授权留痕（`grep -rn '依 Agent 建议裁决施工' teamwork/` → **0** 命中） | **不实，撤回** |
| 033 / 034 / 035 / 036 / 037 已按建议落地 | 五者 frontmatter 均为 `status: created`，Activity Log 只有 `2026-10-10` 规划段、无施工段条目 | **不实，撤回** |
| 033（proposal 包骨架）已落地 | `evergreen/` HEAD `f456b0e` 下 `internal/proposal/` **目录不存在** | **不实，撤回** |

**该历史快照当时的真实状态（截至 2026-09-02 owner 发话前，逐条已失效）**：

- A-23 / A-24 当时**均未经 owner 裁决**，第 4 列当时逐字为 `未知 / 待 owner 确认`；
- 第 9 列仅为 **Agent 建议**，当时**未获任何授权**，因此当时**尚不构成施工依据**；
- T-…-032 当时为 `blocked`；M3 当时**未开工**，033–047 全部 `created`，9 条 `depends_on` 一条未摘；
- 本文档在 owner 发话前**不得**被解读为 owner 已裁决、亦**不得**被解读为已获「按建议先行施工」的授权。

**解除阻塞的唯一入口**：owner 在第 4 列逐字填入封闭选项之一并署名日期，之后才允许 033 起施工。
该入口已于 **2026-09-02** 被 owner 走完，落地记录见 **§7**。


## 7. owner 正式裁决（2026-09-02 落地，逐字记录，**不得增删语义**）

**裁决人**：`项目维护者 <maintainer@example.com>`　|　**裁决日期**：`2026-09-02`　|　**记录人**：项目维护者（技术负责人，仅转录）
**性质**：本节是 **owner 正式裁决**，与 §2 第 9 列的 **Agent 建议** 是两回事——建议是历史留痕，裁决是施工依据。
**效力范围**：本节 10 条约束是**加严条款**。owner 裁决**采用了** Agent 建议的两个选项，但**附加了 5 + 5 条约束**；
凡建议文本（§3 / §4 / §2 第 9 列）与本节冲突之处，**一律以本节为准**，且只允许朝**更严**的方向解释。

### 7.1 A-23：采用 `CLI 直写例外`（附加约束 5 条，逐字）

1. 仅限 `proposals/**` 提案控制面。
2. 必须复用 guarded store，禁止裸写文件。
3. CLI 负责 Git 与报告。
4. 批准后的知识数据修改仍走 ChangePlan。
5. Agent 可创建提案，但不可批准或执行。

**逐条的加严含义（不新增语义，只指出它比 Agent 建议严在哪）**：

| # | 约束 | 比建议严在哪 | 落点 |
|-|-|-|-|
| 1 | 仅限 `proposals/**` 提案控制面 | 建议只说「提案是 CLI 直写的例外」，未圈死路径面；本条把例外**收窄成路径白名单**，`proposals/**` 之外一律无例外 | 提案合同 §8.5；033 / 040 |
| 2 | 必须复用 guarded store，禁止裸写文件 | 建议只要求「复用 `store.ContentHash` 与 B1–B4 前置检查」；本条把它升级为**禁止裸写文件**——直写例外**不等于**绕过 B3 `content_hash` 校验 | 提案合同 §8.5；033 / 040 |
| 3 | CLI 负责 Git 与报告 | 建议只说「提案命令自发 commit」；本条把 **Git 与报告**两项职责一并钉给 CLI，`internal/proposal` 不得自发 commit、不得自组报告 | 提案合同 §8.5；040 / 036 |
| 4 | 批准后的知识数据修改仍走 ChangePlan | 建议已提到「授权后的那次真实知识改动仍是 ChangePlan 的 `delete` op」；本条把它升为**硬约束**：提案控制面与知识数据面**两条路径分离**，批准不带来任何知识数据直写口 | 提案合同 §8.5；041 |
| 5 | Agent 可创建提案，但不可批准或执行 | 建议未涉及授权矩阵；本条要求与写权限矩阵**逐格对齐**（`approve` 属用户显式路径 P-U） | 授权合同 §2.6 / §2.9；040 |

### 7.2 A-24：采用 `物理移除`（附加约束 5 条，逐字）

1. 从 `relations[]` 删除规范化 `(from,type,target)` 匹配的全部记录。
2. `opposing` 先按 ID 字典序规范化。
3. 不留墓碑，不创建 RelationID。
4. 未命中按 W10 幂等处理，零写入、零 commit。
5. 逻辑删除实体时不得级联删除关系。

**逐条的加严含义**：

| # | 约束 | 比建议严在哪 | 落点 |
|-|-|-|-|
| 1 | 从 `relations[]` 删除规范化 `(from,type,target)` 匹配的全部记录 | 建议只说「条目数 −1」；本条定死**匹配键**（规范化三元组）与**删除范围**（匹配的**全部**记录，不是首条） | 提案合同 §8.5；037 / 044 |
| 2 | `opposing` 先按 ID 字典序规范化 | 建议未提方向；本条与既有 `opposing` **单向存储**语义对齐（`internal/rules/opposing.go` 唯一实现、W8、`store.ErrOpposingNotNormalized`）：删除前先按两端 ID 字典序归一，再做匹配 | 提案合同 §8.5；037 / 044 |
| 3 | 不留墓碑，不创建 RelationID | 建议只论证「不加墓碑字段」；本条追加**不创建 RelationID**——不得借「删除需要稳定标识」为由给关系条目引入新主键 | 提案合同 §8.5；037 / 044 |
| 4 | 未命中按 W10 幂等处理，零写入、零 commit | 建议只说「W10 一字不改」；本条把幂等语义**定死为零写入 + 零 commit**（不产生空 commit、不改字节） | 提案合同 §8.5；044 |
| 5 | 逻辑删除实体时不得级联删除关系 | 建议只区分了两种场景；本条把「不级联」写成**禁令**，与 F3「状态与删除正交」及 §5.2「关系记录不被物理删除」一致 | 提案合同 §8.5；041 / 043 / 044 |

### 7.3 本节的机器判定入口

| 判定对象 | 命令（在 teamwork 仓根执行，`DOC` 同 §5） | 期望 |
|-|-|-|
| A-23 五条约束逐字在册 | `for s in '仅限 `proposals/**` 提案控制面' '必须复用 guarded store，禁止裸写文件' 'CLI 负责 Git 与报告' '批准后的知识数据修改仍走 ChangePlan' 'Agent 可创建提案，但不可批准或执行'; do grep -qF "$s" "$DOC" \|\| echo MISS; done \| wc -l` | `0` |
| A-24 五条约束逐字在册 | 同法用 `grep -qF` 逐条比对 §7.2 的五条编号项（第 1–5 条整句，含反引号与全角逗号） | `0` |
| 裁决人与日期在册 | `grep -qF '项目维护者 <maintainer@example.com>' "$DOC" && grep -qF '2026-09-02' "$DOC"` | 退 `0` |
| 10 条已下沉到两份合同 | 提案合同 §8.5 命中 A-23 第 1–4 条 + A-24 全 5 条；授权合同 §2.9 命中 A-23 第 5 条 | 各自 `0` MISS |
| 建议 ⟷ 裁决未混写 | 第 9 列表头逐字含 `仅建议，不构成裁决`；§7 标题逐字含 `owner 正式裁决` | 成立 |

### 7.4 裁决落地后的事实变更（供门禁重钉，逐条为**当前**事实）

- **T-…-032 恰 `done`**：DoD「owner 关闭 A-23 与 A-24」已满足，按 Acceptance「未裁决时的状态一致性」**第一分支**（两行结论均非「未知 / 待 owner 确认」）收口；
- **033–047 恰 `created`**：本轮**只落地裁决与合同约束，不写任何产品代码**，`evergreen/` HEAD 仍为 `f456b0e`、`internal/proposal/` **仍不存在**；
- **9 个硬下游对 032 的 `depends_on` 一条不摘**，且其 DoR 中「等待 A-23 / A-24 裁决」条目**已满足**（上游已 `done` ≥ `integration`），可以开工；
- **只加严不放宽**：本节没有放宽任何既有条款——`.index/` 仍不建、退出码 `5` 仍不启用、`skipped[].kind` 仍恰两值、F1–F6 / B1–B4 / 五分区语义一字未动。
