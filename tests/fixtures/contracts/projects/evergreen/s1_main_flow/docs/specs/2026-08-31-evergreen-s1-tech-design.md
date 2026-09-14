---
topic: Evergreen v1 技术方案 · S1/M1 施工索引
stage: S1
milestone: M-001
baseline_status: executable
baseline_scope: S1/M1
language: Go
language_decision: docs/specs/2026-08-31-language-selection.md
authority: 飞书《[技术方案]常青（Evergreen）v1 技术方案》
authority_url: https://docs.example.invalid/evergreen/design
authority_role: provenance-only
created: '2026-08-31'
updated: '2026-08-31'
updated_by: 项目维护者
---

# Evergreen v1 技术方案 · S1/M1 施工索引（当前阶段唯一可执行基线）

## <a id="baseline-status"></a>0. 文件定位（本次新增，先读这一节）

1. **本文件是 S1/M1 当前阶段的唯一可执行基线。** §0–§14 是既有施工索引（内容未改，仅新增本节与 §15–§20）；§15–§20 是按 2026-08-31 语言选型决策（结论 **Go**）固化下来的工程基线。开工只需本文件 + 本仓（`teamwork/`）内文件，**不需要访问任何外部链接**。
2. **实现语言已定为 Go，唯一权威依据是本仓文件** `docs/specs/2026-08-31-language-selection.md`（加权总分 Go 8.52 / Python 6.38）。**Python 是已否决选项**，只允许作为历史记录出现在该选型文档中；本基线的活跃方案里**不存在第二套实现路径**。
3. **外部飞书 URL 只作 provenance，不作为开工依赖。** frontmatter 的 `authority_url` 与正文中的「技术方案 §x.y」章节号，都是对外部权威文档的**来源标注**；本阶段未访问该链接（沙箱规则），凡只能由外部文档证明的内容一律进 §19「未知」。若日后权威文档与本文冲突，以权威文档为准并回改本文。
4. **本文件不是任何 Task 的 `deliverables`**，只被 18 个 Task 的 `design_doc:` 字段按锚点引用；门禁脚本对本文件的唯一断言是「被引用的锚点真实存在」（`round3` B12 / `round4` / `round5`）。因此**新增章节与锚点安全，删除既有锚点会直接打红门禁**。
5. **本次未改动**：18 个 Task 文件、`EPIC.md`、`milestones/M-001-m1.md`、`tools/*.py`、五份评审报告的既有内容。选型对它们的影响见 `docs/specs/2026-08-31-post-selection-review.md` 与本文 §20。

**权威文档（只读，不得改写，仅 provenance）**：《[技术方案]常青（Evergreen）v1 技术方案》
<https://docs.example.invalid/evergreen/design>

本文件**不是**技术方案的副本，而是 `s1_main_flow` epic 内的**施工索引**：把 M1 任务需要的章节、冻结合同、范围边界固定下来，供 Task 的 `design_doc:` 锚点引用。任何与权威文档冲突之处，一律以权威文档为准。

需求侧摘要 `digest_prd.md` / `digest_design.md` **不在沙箱内**（本次全盘核查无命中），原引用已作废，**不作为开工依赖**；正文出现的 `EG-*` 需求编号只当**标签**使用，其条文内容在本阶段标记「未知」（§19 U-13）。

## <a id="stage-model"></a>阶段模型（技术方案 §1.1）

| 阶段 | 名称 | 本阶段交付 |
|-|-|-|
| **S1** | 主链路可用 | 收录 → 材料笔记 → 知识卡 → 关系 → Git 提交 → 最终报告，零介入跑通 |
| S2 | 用户编辑与提案 | 失效/恢复/`replaced_by`、逻辑删除与提案、`reviewed_at` |
| S3 | 一致性与对账 | `eg reconcile`、关系校验、孤儿与重复 ID 检查 |
| S4 | 索引与性能 | `.index/` 形态、增量、分页截断、10,000 卡门槛 |
| S5 | 严格验收 | 强原子事务、锁与写前复核、崩溃恢复、error 面扩大 |

**口径**：凡 S2 及以后的能力，在 S1 代码与 `SKILL.md` 中**不得**表述为已提供的保证。

## <a id="frozen"></a>六条冻结合同（技术方案 §1.2 F1–F6）

| # | 冻结内容 |
|-|-|
| F1 | 目录布局：`vault/sources/`、`unprocessed.md`、`domains/<domain>/{notes,knowledge,reviews}/`、`proposals/`、`.index/` |
| F2 | 稳定 ID 规则 + **关系一律引用 ID、不引用路径** |
| F3 | 状态与删除正交：`status: active\|deprecated` + `deleted_at`/`deleted_reason` |
| F4 | 关系类型集合：材料 `support`/`against`/`context`；论证 `derives`/`supports`/`limits`/`opposing`；生命周期 `replaced_by` |
| F5 | 正文固定分区语义：知识卡五分区、材料笔记五分区 |
| F6 | Markdown 是权威、`.index/` 是可重建派生 |

## <a id="m1"></a>M1 范围与完成判据（技术方案 §16.1）

**范围**：`eg init` / `eg config`、`eg capture`（Agent 供正文）、`write_note`、`create_card`、`add_material_rel` / `add_relation` 写入、`git commit`、最终报告；包：`model` `mdfile` `store`（简化）`git` `plan` `report` `cli`。

**完成判据**：一篇真实文章从收录到知识卡与关系落盘，**全程零介入**；产物能被 Obsidian 正常打开；Git 有一次可读的 commit；报告如实写出实际写了什么。**不测性能、不测并发。**

### <a id="m1-not"></a>M1 明确不做（写进各 Task 的范围边界）

- 多文件强原子事务、`.index/txn/`、`run.lock`、短临界区锁、写前复核、崩溃恢复（M6 / S5）
- 块级 `base_block_hash` 合并、`replace_block`（M3 / S2 起，安全合并 S5）
- 外部编辑对账 `eg reconcile`、`eg check`、孤儿与重复 ID 巡检（M4 / S3）
- `.index/` 索引形态、增量、FTS5、分页与排序截断、性能门槛、`eg bench`（M5 / S4）
- 提案子系统与逻辑删除、`deprecate`/`restore`/`replaced_by`/`delete`/`undelete`、`reviewed_at` 与 `eg unreviewed`、主题综述 `reviews/`（M3 / S2）
- 严格校验：W1/W2/W3/W4/W6 升 error、写权限矩阵硬约束、CI 依赖方向门禁（M6 / S5）

### <a id="dead"></a>不得复活的已废弃设计

`candidate`、独立未决问题实体、`open/` 目录、`source_check`、观点倾向 / `lean` 字段、知识卡与笔记 frontmatter **顶层**的 `domain` / `type` 冗余字段、物理（永久）删除、迁移提案、跨领域能力（EG-DOM-04/06/07/08 四条 Deferred）。

**黑名单按 YAML/JSON 字段路径判定，禁止关键词扫描**——`relations[].type`、`sources[].rel`、`plan.domain`、`--domain`、`unprocessed.md` 的 `target_domain`、提案 `type` 都是合法且必需的用法（技术方案 §4.1 白名单表）。

## <a id="ch3"></a>§3 目录布局与配置

S1 只需存在：`vault/.git/`、`.gitignore`（内容 `.index/`）、`evergreen.yml`、`SKILL.md`、`unprocessed.md`、`sources/`、`domains/<d>/notes/`、`domains/<d>/knowledge/`。`reviews/`、`proposals/`、`.index/` 在 S1 **不需要存在**，但名字与位置已定死。

`evergreen.yml`：`version: 1` + `domains: [...]` + `default_domain`。未配置 `default_domain` 时，除 `init` / `config` 外所有命令提示配置并退 `1`，**CLI 绝不自选默认领域**（EG-DOM-03）。

产物 ID 规则：原文 `s-<yyyymmdd>-<slug>`、笔记 `n-<yyyymmdd>-<slug>`、知识卡 `k-<yyyymmdd>-<slug>`、综述 `r-`（S2）、提案 `p-<yyyymmdd>-<seq>`（S2）。默认文件名 `<id>.md`，但文件**允许被重命名或移动**：`id → path` 在 S1 由扫描完成。`slug` 仅供人眼可读，不参与任何判定。

## <a id="ch4"></a>§4 数据合同

- **§4.1 知识卡**：`id`/`status`/`created_at`/`updated_at`/`sources[]` S1 必需，`tags`/`relations[]` S1 可选；`reviewed_at`/`deleted_at`/`deleted_reason`/`replaced_by` S2 引入，S1 **不写、不校验、出现即原样保留**。
- **§4.2 五分区与块**：分区固定 H2、名称逐字固定、顺序固定；第六个 H2 视为用户自建分区原样保留。`create_card` 新建卡五分区都可写（「知识内容」必写）；对**已有卡的自动处理**只允许追加「解释与依据」「条件与边界」「理解自检」，**「用户补充」任何时候永不写**。块 = 段落 / 顶层列表项（含缩进子项）/ 围栏代码块 / 连续表格行 / H3 小标题。
- **§4.3 材料笔记**：五分区「材料提炼」「Agent 分析」「用户补充」「存疑与待验证」「产出知识卡」；笔记**没有** `status`、**不生成理解自检**；「产出知识卡」是本次加工快照，写 `- k-xxx（新建｜复用｜补充）`，此后不回写。
- **§4.4 原文与收件区**：原文 frontmatter `id`/`url`/`title`/`saved_at`，正文收录后不因任何加工改写；`unprocessed.md` 一个顶层列表项 = 一个条目（`source_id`/`title`/`saved_at`/`reason`/`target_domain`），键为 `source_id`。
- **§4.5 ChangePlan**：唯一程序化写入通道，**结构稳定、字段与 op 可增**。S1 七个 op：`add_source`、`write_note`、`create_card`、`append_card`、`add_material_rel`、`add_relation`、`add_open_question`。顶层：`plan_version`(=1)、`verb`、`domain`、`reason`、`requirement_ids`、`convergence[]`（逐卡条目）、`base`（`id/路径 → content_hash`）、`ops[]`。
- **§4.5.1 校验分级**：error 仅 E1–E6（`id` 缺失/重复、关系 `target` 不可解析、`s-` 进 `relations` 或 `k-` 进 `sources`、frontmatter YAML 不可解析、`plan_version` 不匹配或未知 op、写入目标落在自动路径的「知识内容」或任何时候的「用户补充」）；其余 W1–W8 / I1 一律 warning 或 info，**照写 + 进报告**。
- **§4.6 最终报告**：S1 必填子集 `source`/`note`/`cards`/`relations`/`open_questions`/`links`/`git.commit`/`skipped[]`/`default_domain_fallback`/`high_impact[]`/`warnings[]`；`proposals[]` 与 `deprecated_new_support[]` 输出空数组；`support_check[]`/`affected` 输出空值；`reconcile` 输出 `{"ran": false}`；`txn_id` 可省略。**不得输出假数据。**

**round-trip 保真（S1 起硬性）**：未知 frontmatter 字段、未知正文 H2 分区、分区内未知块，一律原样保留——不报错、不丢弃、不重排顺序、不改写缩进与空行，只在报告里记一条 info。

## <a id="ch6"></a>§6 关系模型

- 材料关系落知识卡 `sources[]` 四要素 `source` + `note` + `rel` + `reason`（`note` 存**笔记 ID**，不存路径）。
- 论证关系落来源卡 `relations[]`，单向一条，`type` ∈ `derives`/`supports`/`limits`/`opposing`，带 `target` + `reason`。
- `opposing` **规范化方向**：按两端稳定 ID 字典序，小者为 `from`，记录写在 `from` 端；**写入前去重**：已存在则更新 `reason`，不产生第二条（W8：warning + CLI 自动规范化并去重，S1 就做）。三方及以上对立只保存实际成立的两两 `opposing`，不引入议题组、不自动补全图。
- 反向查询 S1 由全库扫描 Markdown 现算；多跳展开 S2、分页排序截断 S4。
- `context` 在查询层与 `support` 严格分离，任何「证据」类统计不含 `context`。

## <a id="ch7"></a>§7 CLI 设计

**S1 九个命令**：`init`、`config get|set`、`capture`、`context`、`apply --plan`、`search`、`card show`、`rel`、`report --last`。单二进制 `eg`，**不包含任何模型调用**。默认人类可读文本，`--json` 输出结构化结果。

**退出码**：`0` 成功；`1` 用法/参数非法（零写入）；`2` 校验失败（仅 error 级，零写入）；`3` 部分写入被跳过（已写入保留并提交，链路继续）；`4` Git 提交失败（**磁盘保留当前状态，不做破坏性还原**）。`6`（需确认）属 S2、`5`（写前复核/锁）属 S5，**S1 不实现、不出现**。

### <a id="ch75"></a>§7.5 `eg capture` 最小收录合同（S1）

抓网页、解析 HTML、清洗正文**全部在 Agent 侧**，**CLI 不做任何网络请求**。四项输入：`--url`、`--title`、正文（`--body-stdin` / `--body-file`）、`--reason`；可选 `--domain`、`--tag`、`--captured-at`。判重：**URL 规范化后精确匹配，其次标题精确匹配**，命中任一即同一篇。命中已有原文 → 复用、不产生第二份、正文不覆盖、`--reason` 追加到收录理由列表、返回 `deduped: true`。已有材料笔记 → 默认返回 `has_note: true` 并提示跳过，仅 `--reprocess` 才允许重新加工。幂等：至多一份原文、至多一条未处理条目，重复执行不产生空 commit。`--json` 输出 `{source_id, path, deduped, has_note, note_id, commit, warnings[]}`，字段**只增不改**。正文为空/过短、`--url` 与 `--title` 同时缺失 → error 退 `2` 零写入；标题异常、领域缺省、正文疑似截断 → warning，收录照常完成。

## <a id="ch8"></a>§8 S1 端到端主链路

`Agent 取正文 → eg capture → eg context → Agent 生成 ChangePlan → eg apply（逐文件写入）→ git commit → 最终报告`。**任一步失败都只进报告、不阻塞链路、不回滚已完成部分。**

`SKILL.md` 必须写死的调用顺序与禁止项见技术方案 §8.2（十一条边界，EG-AGT-02 逐条可查）。

## <a id="ch9"></a>§9 四条安全底线（S1 必须实现且不可放宽）

| # | 底线 | S1 实现方式 |
|-|-|-|
| B1 | Agent 对已有内容**默认只追加**，不自动替换普通块 | `append_card` 等 op 只做分区尾部追加；渲染器**不向自动路径提供**「替换已有块」能力 |
| B2 | 重新加工时**逐字保留**既有「用户补充」与「存疑与待验证」 | 重新渲染前解析出原始文本（含空行、缩进、未知子结构）原样回填；**永不写入「用户补充」**；单测覆盖 round-trip 相等 |
| B3 | 文件**自读取以来变化时跳过该处并报告** | `eg context` 返回每个文件的 `content_hash`；`apply` 写入前逐文件重算比对，不一致则**跳过该文件**——不覆盖、不强写、不重试、不排队 |
| B4 | Git 提交失败时**不做破坏性回滚** | **不执行 `git checkout -- <paths>`、不 `reset --hard`、不删除文件**；保留磁盘现状，未提交变更清单与原因进报告，退 `4` |

共同形状：**宁可少写、不可错写**。

## <a id="ch13"></a>§13 模块划分（包名与依赖方向现在定死）

```text
cmd/eg/main.go
internal/
  model/    产物结构体、ID 类型、枚举、时间格式；零依赖              [S1]
  mdfile/   frontmatter 解析/序列化、分区切分、块切分与 block_hash   [S1] → model
  store/    读盘、渲染、tmp+fsync+rename、逐文件 content_hash 比对   [S1 简化版] → model mdfile git
  git/      init/add/commit/status/diff、提交信息规范化              [S1，不提供 checkout 回滚入口] → model
  plan/     ChangePlan schema、op → store 展开、校验                 [S1 骨架 + error 级子集] → model rules query store
  report/   最终报告 JSON schema + 人类可读渲染                      [S1] → model query
  cli/      命令定义、参数、退出码、--json 开关                      [S1 九命令] → 以上全部
  query/    检索、单卡视图、关系正反查                               [S1 扫描版] → model（S1 直连 mdfile）
  rules/    opposing 规范化、收敛三维度判据                          [S1 部分] → model query
  index/    SQLite+FTS5                                             [S4，S1–S3 可整包不存在]
  proposal/ [S2]   reconcile/ [S3]
skill/SKILL.md  随二进制发布，eg init 时写入 vault/                  [S1]
```

三条依赖禁令（**S1 起就照此组织**，做成 CI 硬门禁属 S5）：`index` 不得依赖 `store`；`rules` / `query/rank` / `query/review` 不得导入 `query/filter`；除 `cli` 的五个命令文件外任何包不得引用 `store.SetStatus` / `SetReplacedBy` / `SetDeleted`。

## <a id="ch14"></a>§14 追溯矩阵与 S1 验收口径

66 条生效需求逐条映射，分布 S1 44 条 / S2 18 条 / S3 2 条 / S4 1 条 / S5 1 条。

**S1 的验收判据是「主链路端到端可用 + 第九章四条安全底线」，不是逐条 `T-*` 全绿**；逐条严格通过是 S5 的验收对象。M1 阶段各 Task 的验收标准按此口径书写。

---

## <a id="eng-baseline"></a>§15 工程基线（Go，2026-08-31 选型固化）

> 本节把 `docs/specs/2026-08-31-language-selection.md` 的结论固化为**可直接施工**的工程约定。活跃方案**只有 Go 一条实现路径**；Python 仅作为已否决选项保留在该选型文档里。

### 15.1 语言与工具链

| 项 | 固化值 | 依据 |
|-|-|-|
| 实现语言 | **Go（唯一）** | 选型文档 §7（Go 8.52 / Python 6.38） |
| 本机实测工具链 | `go version` → `go1.24.13 linux/amd64` | 本次在沙箱内实测 |
| `go.mod` 的 `go` 指令 | **`1.22`**（= `T-…-001` DoR 的「Go ≥ 1.22」下限；fuzz 自 1.18 起可用，无阻塞） | `tasks/T-…-001` 的 DoR/Scope；选型文档 I-7 |
| 模块名 | **`evergreen`** | 见 15.2 |
| S1 唯一允许的外部依赖 | `gopkg.in/yaml.v3`，**只读用途（只 `Unmarshal`，绝不 `Marshal`）** | 选型文档 I-8、§4.1(1) |
| 脚手架阶段依赖 | **零 require**（仅 stdlib）；`yaml.v3` 由 `T-…-005` 引入时再写进 `go.mod` | 本次实测 `go build ./...` 通过 |
| 禁止依赖 | SQLite / FTS5 / 任何网络库（`eg` 不做网络请求） | §7.5、§13 `index/` 属 S4 |

**模块名 `evergreen` 的理由与代价**：18 个 Task 的 `code_paths` / `deliverables` 前缀、`branches` 的 repo key 全部是 `evergreen`，仓库目录名也是 `evergreen/`；沙箱内**没有任何可证明的托管域名或远端地址**（本次 `evergreen/` 明确不配置远端），因此采用不含域名的本地模块路径，import 形如 `github.com/ikaqiu-Lemon/EverGreen/internal/mdfile`。若日后确定托管域名，改动面 = `go.mod` 首行 + 全量 import 前缀（登记为 §20 A-10、§19 U-14）。

### 15.2 仓库布局（沙箱根下，与 `teamwork/` **平级**）

```text
evergreen/                      代码仓（本次新建，git init -b master，无远端，无软链接）
  go.mod                        module github.com/ikaqiu-Lemon/EverGreen / go 1.22
  Makefile                      build | test | lint | fmt | release | clean | help
  README.md                     项目说明、构建与测试入口、与 teamwork 的关系（只用相对路径）
  .gitignore                    /bin/、/dist/ 等构建产物不入库
  cmd/eg/main.go                单二进制 eg 的唯一入口（当前仅 --help / version）
  cmd/eg/main_test.go
  internal/version/             版本信息（脚手架用，非 §13 的 S1 九包之一）
  internal/model/doc.go         [S1] 零依赖
  internal/mdfile/doc.go        [S1] → model
  internal/store/doc.go         [S1] → model mdfile git
  internal/git/doc.go           [S1] → model
  internal/plan/doc.go          [S1] → model rules query store
  internal/report/doc.go        [S1] → model query
  internal/cli/doc.go           [S1] → 以上全部
  internal/query/doc.go         [S1] → model
  internal/rules/doc.go         [S1] → model query
  skill/                        SKILL.md 由 T-…-017 填写，随二进制发布
  test/e2e/                     e2e 语料与脚本由 T-…-018 填写
  bin/                          make build 产物（gitignore）
  dist/                         make release 产物 + SHA256SUMS（gitignore）
```

**与 `teamwork/` 的关系（硬约定）**：`teamwork/` 只放计划与文档（Task / 里程碑 / spec / 门禁脚本），`evergreen/` 只放代码；两者**同级、互不嵌套、互不 `git add`**，**不建任何软链接**。跨仓引用一律写相对路径：从 `evergreen/` 指向本基线是 `../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-08-31-evergreen-s1-tech-design.md`。

`internal/index/`、`internal/proposal/`、`internal/reconcile/` **本阶段不创建**（S4 / S2 / S3）。

### 15.3 包结构与依赖方向

逐字沿用 §13（[`#ch13`](#ch13)）：包名、首次落地阶段、允许依赖三者以每个包的 `doc.go` **首行**为代码内声明，首行格式必须匹配 `^// \[S[1-5]\]`（`T-…-001` 的机器判据）。三条依赖禁令同 §13，S1 起就照此组织，做成 CI 硬门禁属 S5。

### 15.4 构建 / 测试 / lint / 发布入口（命令字符串固化）

CWD = `evergreen/`：

| 用途 | 命令 | Makefile target |
|-|-|-|
| 构建 | `go build -trimpath -ldflags "-s -w" -o bin/eg ./cmd/eg` | `make build` |
| 测试 | `go test ./...` | `make test` |
| 格式检查 | `gofmt -l .`（**输出必须为空**） | `make lint`（第 1 步） |
| 格式修复 | `gofmt -w .` | `make fmt` |
| 静态检查 | `go vet ./...` | `make lint`（第 2 步） |
| **写路径守卫** | `grep -rn --include='*.go' -E 'yaml\.Marshal\|yaml\.NewEncoder' internal/ cmd/` **必须无命中** | `make lint`（第 3 步，`guard`） |
| 发布 | 四平台交叉编译 + `SHA256SUMS` | `make release` |
| 入口自检 | `./bin/eg --help`、`./bin/eg version`（退出码 0） | — |
| 清理 | 删除 `bin/`、`dist/` | `make clean` |

> 18 个 Task 的 `verify.*` 字符串形如 `cd evergreen && go test ./...`，其隐含 CWD 是**沙箱根**（`teamwork/` 与 `evergreen/` 的公共父目录）；本表以 `evergreen/` 为 CWD，两者等价。该 CWD 语义需在下一阶段显式写明，登记为 §20 A-1。

### 15.5 发布方式（`make release`）

`CGO_ENABLED=0` 交叉编译四平台并生成校验清单：

| 目标 | 产物 |
|-|-|
| `linux/amd64` | `dist/eg_linux_amd64` |
| `linux/arm64` | `dist/eg_linux_arm64` |
| `darwin/amd64` | `dist/eg_darwin_amd64` |
| `darwin/arm64` | `dist/eg_darwin_arm64` |
| 校验清单 | `dist/SHA256SUMS`（`shasum -a 256` / `sha256sum`） |

`skill/SKILL.md` **随二进制发布**（§13 末行），`eg init` 时写入 `vault/`。darwin 产物只保证**能编译产出**，**在 macOS 上的运行未验证**（§19 U-1）。

---

## <a id="write-path"></a>§16 写路径硬约束（选型决策「对下游的影响清单」10 项的落地）

### 16.1 十项影响的落点

| 选型 § 8.2 | 内容 | 本基线落点 | 可执行判据 |
|-|-|-|-|
| I-1 | `mdfile` 实现为**外科式字节区间编辑**，不是 `Parse→Marshal` | §16.2 | `internal/mdfile` 无任何序列化回写路径 |
| I-2 | 写路径**禁用** YAML 序列化器 | §16.3 | `make lint` 的 `guard` 步：`grep -rn --include='*.go' -E 'yaml\.Marshal\|yaml\.NewEncoder' internal/ cmd/` 无命中 |
| I-3 | 新增 **2 条 fuzz 属性**，单测 + fuzz 双轨 | §16.5 | `go test ./internal/mdfile -run Fuzz -fuzz FuzzRoundTrip -fuzztime 30s` |
| I-4 | `store.WriteGuarded` 前做 **`Parse→Render` 字节自检，不等即拒写** | §16.4 | 单测：构造自检失败样例 → 零写入 + `skipped[]` 有记录 |
| I-5 | 用户内容全链路只用 `[]byte` | §16.3 | 禁止对用户内容 `for range string` / `strings.*` 规范化 |
| I-6 | `make release` 四平台产物 + sha256 清单 | §15.5 | `make release` 产出 4 个二进制 + `dist/SHA256SUMS` |
| I-7 | Go 版本基线复核 | §15.1 | `go.mod` 为 `go 1.22`；本机工具链 `go1.24.13` |
| I-8 | S1 唯一外部依赖 `gopkg.in/yaml.v3`（只读） | §15.1 | `go list -m all` 不含 SQLite 相关模块 |
| I-9 | **S4 预警**：SQLite 若走 cgo 将破坏静态二进制 | §16.6 | S4 开工前先评估 `modernc.org/sqlite`；S1–S3 `internal/index` 整包不存在 |
| I-10 | 选型文档只是决策记录，**不是任何 task 的 `deliverables`** | §0 第 4 条 | 门禁扫描面不含 `docs/specs/`（选型文档 §8.3 实测） |

### 16.2 `mdfile` 契约：只读解析 + 字节区间写入

**读**：允许用 `yaml.v3` `Unmarshal` 取字段值、判 E4（frontmatter YAML 不可解析 → error）。
**写**：**任何落盘一律只做字节区间拼接**，绝不经过序列化器。

不复制、不改写任何字节，只在原始 `[]byte` 上建**半开区间**索引：

```go
type Span struct {
    Name  string // H2 分区名
    Start int    // 分区标题行起始
    Body  int    // 分区正文起始（标题行之后）
    End   int    // 下一个 H2 起始 / EOF
}

type Doc struct {
    Raw      []byte // 原始字节，只读，永不原地修改
    FMStart  int    // frontmatter 内容起始（"---\n" 之后）
    FMEnd    int    // frontmatter 内容结束（结束 "---\n" 之前）
    BodyFrom int
    Sections []Span
}

func (d *Doc) Render() []byte // 无逻辑变更 ⇒ 逐字返回 Raw
```

| 要素 | 机制 |
|-|-|
| 未知分区 / 未知字段 / 未知块 | **不进入任何重写路径**；写入只在目标分区 `End` 处插入字节，其余区间以 `d.Raw[a:b]` **原样拼接** |
| 空行 | 从不重写包含它的区间；追加时回退到「最后一个非空行之后」插入，保留原有尾随空行 |
| 缩进 | **探测既有风格并沿用**（取该序列首个 `- ` 的前导空白作 `indent`），绝不规范化；空序列不猜缩进 → warning + 进报告，S1 不处理 |
| 引号风格 / 折叠标量 / 字面标量 / 锚点别名 | 不经过序列化器 → 逐字保留 |
| 键顺序 | 不重排；新键**只追加到 frontmatter 末尾**（`AppendFMKey`） |
| CRLF / 行尾空白 | 不触碰；仅 `block_hash` 计算时做**只读**规范化，不影响落盘字节 |
| 围栏代码块内的 `##` | `inFence` 状态机，不误判为分区标题 |

### 16.3 禁用清单（硬约束，违反即拒收）

1. 写路径**禁止** `yaml.Marshal` / `yaml.NewEncoder` / 任何 YAML 序列化回写（`make lint` 的 `guard` 步是门禁）。
2. 用户内容**禁止** `for range string`、`strings.*` 规范化、rune 迭代重建（非法 UTF-8 会有损）；全链路只用 `[]byte`。
3. 文件读写只走字节接口，不用文本模式的编码转换。

### 16.4 写前字节自检（`store.WriteGuarded` 的固定次序）

`读盘` → `content_hash 比对`（B3，不一致即跳过该文件）→ **`Parse → Render` 与原字节比对，不等即拒写**（退化为 B3 的 `SkipFileChanged` 语义，进报告 `skipped[]`）→ 在目标字节区间插入 → `tmp` + `fsync` + `rename`（并 fsync 目录）→ 写后用 `yaml.v3` **只读复核**「仍是合法 YAML 且新字段语义正确」，失败即记录并进报告。

共同形状仍是 §9 的**宁可少写、不可错写**。Go 生态无 `ruamel` 式保真兜底库，本条自检就是替代防线。

### 16.5 两条 fuzz 属性（Go 原生 fuzz，stdlib，零依赖）

| 属性 | 断言 | 位置 |
|-|-|-|
| `FuzzRoundTrip` | `Parse` 成功后 `Render()` 与输入**字节相等**（非法输入 return，不参与断言） | `internal/mdfile/fuzz_test.go` |
| `FuzzAppendPreserves` | 追加后 `out[:FMEnd] == raw[:FMEnd]`、`bytes.Contains(out, raw[userStart:userEnd])`（「用户补充」逐字不变）、`len(out) >= len(raw)`（只增不减） | 同上 |

语料沉淀到 `internal/mdfile/testdata/fuzz/` 作为回归资产（**入库，不进 `.gitignore`**）；CI/本地按需提高 `-fuzztime`。

### 16.6 S4 cgo/SQLite 预警

S4 的 `.index/`（SQLite + FTS5）若采用 cgo 方案（如 `mattn/go-sqlite3`）**将破坏「单二进制 + 静态 + 可交叉构建」这一核心优势**。S4 开工前必须先评估纯 Go 的 `modernc.org/sqlite`（其 FTS5 支持与性能**未知**，§19 U-7）。S1–S3 `internal/index` **整包不存在**，风险推迟且可重估。

---

## <a id="local-refs"></a>§17 本地引用校验（本次逐一验证，全部相对路径）

### 17.1 存在性核查结果

路径相对本 epic 根 `teamwork/projects/evergreen/s1_main_flow/`（另有标注者除外）：

| 引用 | 存在 | 说明 |
|-|-|-|
| `docs/specs/2026-08-31-language-selection.md` | ✅ | 选型决策，本基线语言结论的唯一权威依据 |
| `docs/specs/2026-08-31-post-selection-review.md` | ✅ | 本次新增的选型后复审报告 |
| `docs/specs/2026-08-31-m1-task-review.md`、`-round2/-round3-final/-round4-final/-round5-final.md` | ✅ 5 份 | 历史评审报告，**既有内容本次未改一字** |
| `tasks/T-evergreen.s1_main_flow-158614-001…018-*.md` | ✅ 18 个 | 本阶段不改 |
| `milestones/M-001-m1.md`、`EPIC.md` | ✅ | 本阶段不改 |
| `tools/validate_m1_tasks.py`、`round3_final_gate.py`、`round4_final_gate.py`、`round5_final_gate.py`、`gate_common.py`、`mutation_test.py`、`derive_eg_baseline.py`、`independent_schedule_check.py` | ✅ 8 个 | 门禁脚本，本阶段不改 |
| `../../../roster.yaml`（teamwork 仓根） | ✅ | 门禁读取 |
| `../../../../evergreen/`（沙箱根下代码仓） | ✅ | 本次新建，见 §15.2 |
| `digest_prd.md`、`digest_design.md` | ❌ **沙箱内不存在** | **原引用已在本次删除**，不作为开工依赖 |

### 17.2 处置说明

- **失效引用**：`digest_prd.md` / `digest_design.md` 已从正文删除（不留占位），相关需求条文改标「未知」（§19 U-13）。
- **沙箱外绝对路径**：本文件全文**不含** `/Users/`、`/home/`、`/tmp/`、`C:\` 等绝对路径；跨仓引用一律相对路径。
- **锚点**：本文件全部 `<a id="…">` 锚点（`stage-model`、`frozen`、`m1`、`m1-not`、`dead`、`ch3`、`ch4`、`ch6`、`ch7`、`ch75`、`ch8`、`ch9`、`ch13`、`ch14` + 本次新增 `baseline-status`、`eng-baseline`、`write-path`、`local-refs`、`lineno-map`、`unknown`、`next-align`）经核对与 18 个 Task 的 `design_doc:` 引用**逐一对应且无缺失**；既有锚点**一个都没删**。
- **外部 URL**：仅 `authority_url` 一条，性质为 provenance（§0 第 3 条）。

---

## <a id="lineno-map"></a>§18 行号引用对照表（选型文档引用的「基线 Lxx」）

本次在文件头部新增 §0 与 frontmatter 字段，正文整体**下移 16 行**（frontmatter 内引用下移 5 行）。`docs/specs/2026-08-31-language-selection.md` 中形如「基线 L92」的引用按下表换算；**后续请改用锚点引用，行号不稳定**。

| 选型文档中的引用 | 原行号 | 现行号 | 内容 | 锚点 |
|-|-:|-:|-|-|
| 基线 L5 | 5 | **10** | `authority_url` | frontmatter |
| 基线 L12 | 12 | **27**（同段落） | 权威文档 URL 行 | §0 |
| 基线 L25 | 25 | **41** | S4「索引与性能」阶段行 | [`#stage-model`](#stage-model) |
| 基线 L39（F6） | 39 | **55** | F6 Markdown 是权威、`.index/` 可重建 | [`#frozen`](#frozen) |
| 基线 L45 | 45 | **61** | M1 完成判据（Obsidian 可打开、不测性能） | [`#m1`](#m1) |
| 基线 L49–L52 | 49–52 | **65–68** | M1 明确不做（S5 事务 / S2 块合并 / S3 对账 / S4 索引） | [`#m1-not`](#m1-not) |
| 基线 L64 | 64 | **80** | §3 S1 只需存在的目录集合 | [`#ch3`](#ch3) |
| 基线 L77 | 77 | **93** | §4.5.1 校验分级（E1–E6 / W1–W8） | [`#ch4`](#ch4) |
| 基线 L80 | 80 | **96** | round-trip 保真（S1 起硬性） | [`#ch4`](#ch4) |
| 基线 L92 | 92 | **108** | S1 九命令 + **单二进制 `eg`** | [`#ch7`](#ch7) |
| 基线 L98 | 98 | **114** | §7.5 CLI 不做任何网络请求 | [`#ch75`](#ch75) |
| 基线 L102 | 102 | **118** | §8 端到端主链路 5 步 | [`#ch8`](#ch8) |
| 基线 L104 | 104 | **120** | `SKILL.md` 调用顺序与禁止项 | [`#ch8`](#ch8) |
| 基线 L108–L115 | 108–115 | **124–131** | §9 四条安全底线 B1–B4 + 宁可少写不可错写 | [`#ch9`](#ch9) |
| 基线 L119–L136 | 119–136 | **135–152** | §13 模块划分代码块 + 三条依赖禁令 | [`#ch13`](#ch13) |
| 基线 L131 | 131 | **147** | `index/ SQLite+FTS5 [S4]` 行 | [`#ch13`](#ch13) |
| 基线 L133 | 133 | **149** | `skill/SKILL.md 随二进制发布` 行 | [`#ch13`](#ch13) |

---

## <a id="unknown"></a>§19 未知清单（不得猜测，不得用默认值顶替）

**沿用选型文档第九节 U-1 ~ U-12**（macOS 全部行为、Go 1.22 与 1.24.13 差异、Obsidian 实际打开表现、真实 vault frontmatter 形态分布、Agent 在 Go/Python 上的实际缺陷率、权威文档是否有更强语言约束、`modernc.org/sqlite` 的 FTS5 支持、`.index/` 在 S4 的形态、10,000 卡扫描性能、`yaml.v3` 维护状态与替代品、owner 对 Go 的熟练度、CI 环境形态），**逐条仍然有效**。本次新增：

| # | 项 | 状态 | 说明 |
|-|-|-|-|
| U-13 | `EG-*` 需求编号的条文内容与「66 条生效 / 4 条 Deferred」原文 | **未知** | 需求侧摘要 `digest_prd.md` / `digest_design.md` **不在沙箱内**；§14 的追溯矩阵在本仓只有统计口径，无逐条原文。`EG-*` 在本阶段只当标签用 |
| U-14 | `evergreen` 仓的最终托管路径 / 远端地址 / 模块域名 | **未知** | 沙箱内无远端信息，本次 `git init -b master` 且**不配置任何远端**；模块名暂定 `evergreen` |
| U-15 | `evergreen` 仓的 CI 形态与发布渠道 | **未知** | 沙箱内无 CI 配置文件；`make release` 是否需要多 runner 取决于此（另见 U-1、U-12） |
| U-16 | `teamwork/` 的 37 个本地提交何时推送、由谁推送 | **未知** | 本阶段禁止 `push`；本次改动同样只做本地 commit |
| U-17 | `gopkg.in/yaml.v3` 在本沙箱能否成功下载入库 | **未知** | 脚手架阶段零依赖，**未执行任何 `go get`**，故未验证模块代理可达性；`T-…-005` 引入依赖时须先验证 |

---

## <a id="next-align"></a>§20 下一阶段需对齐的字段与命令清单（本阶段**只登记、不执行**）

> 18 个 Task 的 **Go 字段（`verify.test` / `verify.lint` 的 `go test` / `gofmt` / `go vet`、`code_paths` 的 `evergreen/...` 前缀、`deliverables` 的 `go.mod` / `cmd/eg/main.go` / `Makefile`、`branches` 的 repo key `evergreen`、`design_doc` 的锚点）经本次核对**全部无需变更**——选型结论与它们的既有假设一致。下表只列**仍需动**的项，精确到字段名与命令字符串。

| # | 对象 | 字段 | 现值（沙箱实测） | 目标值 / 动作 |
|-|-|-|-|-|
| **A-1** | 18 个 Task | `verify.test` / `verify.lint` / `verify.run` 的 **CWD 语义** | `cd evergreen && …`（隐含 CWD = 沙箱根） | **推荐不改字符串**，改为在 `milestones/M-001-m1.md` 或 `evergreen/README.md` 显式声明「`verify.*` 的 CWD = `teamwork/` 与 `evergreen/` 的公共父目录」；备选是把 18 处改成 `cd ../evergreen && …`（改动面大，不推荐） |
| **A-2** | 18 个 Task | `verify.lint` | `cd evergreen && gofmt -l . && go vet ./...` | 二选一：① `cd evergreen && make lint`（推荐，守卫随 Makefile 演进）；② `cd evergreen && gofmt -l . && go vet ./... && ! grep -rn --include='*.go' -E 'yaml\.Marshal\|yaml\.NewEncoder' internal/ cmd/` |
| **A-3** | `T-…-005` | `verify.test` | `cd evergreen && go test ./internal/mdfile/...` | 升级为单测 + fuzz 双轨：`cd evergreen && go test ./internal/mdfile/... && go test ./internal/mdfile -run Fuzz -fuzz FuzzRoundTrip -fuzztime 30s`（`FuzzAppendPreserves` 同法追加一条） |
| **A-4** | `T-…-006` | `verify.test` | `cd evergreen && go test ./internal/store/...` | 保留命令，Acceptance 增一条「`WriteGuarded` 前 `Parse→Render` 字节自检，不等即拒写并进 `skipped[]`」；如需机器判据可加 `-run 'Guard|SelfCheck'` 子测试 |
| **A-5** | `T-…-005` | `deliverables[0].requires` + Scope 实现要点第 1 条 | `…解析与序列化，round-trip 原样保留…` / 「frontmatter：YAML 解析 / 序列化，**保持键顺序**」 | 改为「YAML **只读解析**（`yaml.v3` 只 `Unmarshal`）+ **字节区间写入**，禁止序列化回写；键顺序因无序列化步骤而天然不变」 |
| **A-6** | `T-…-001` | `deliverables[2].requires`（`evergreen/Makefile`） | `build / test / lint 三个统一入口，供各任务 verify 复用` | 改为 `build / test / lint / fmt / release 五个统一入口…`；`release` = `CGO_ENABLED=0` 四平台 + `dist/SHA256SUMS` |
| **A-7** | `T-…-001` | `code_paths` / `deliverables` | `evergreen/go.mod`、`evergreen/cmd/eg/`、`github.com/ikaqiu-Lemon/EverGreen/internal/`、`evergreen/Makefile` | 前缀**无需变更**；可补 `evergreen/README.md`、`evergreen/.gitignore`（本次已实际创建）；并注意 `T-…-002` / `T-…-011` 的 `code_paths` 是 teamwork 仓内路径 `projects/evergreen/s1_main_flow/docs/specs/`，需在里程碑文档声明「`code_paths` 允许跨仓，两种前缀分别指代码仓与 teamwork 仓」 |
| **A-8** | `EPIC.md` | `repos` | `repos: []` | 登记 `repos: [evergreen]`（与 18 个 Task 的 `branches` repo key 一致） |
| **A-9** | 18 个 Task | `branches` | `branches: {evergreen: feature/s1_main_flow/<name>}` | repo key **无需变更**（与目录名 `evergreen/` 一致）；本次仓库只建 `master`、**未建任何 feature 分支**，由 owner 开工时按需创建；远端待定（U-14） |
| **A-10** | `T-…-001` | DoR 第 3 条 + `go.mod` | 「确认 Go 版本基线（≥ 1.22）与 module 路径已与 owner 对齐」 | 回填实测值：module `evergreen`、`go 1.22`、本机工具链 `go1.24.13`；若确定托管域名则同步改 `go.mod` 首行与全量 import 前缀 |
| **A-11** | 18 个 Task | `design_doc` | `docs/specs/2026-08-31-evergreen-s1-tech-design.md#{ch13,ch7,ch4,ch9,ch3,ch75,ch8,ch6,m1}` | 锚点**全部仍存在，无需变更**；可选追加：`T-…-001` → `#eng-baseline`，`T-…-005` / `T-…-006` → `#write-path` |
| **A-12** | `tools/*.py` | 门禁断言 | `round4` I4 / `round5` G7 以 `internal/<pkg>/` 字符串匹配交付物路径 | **无需变更**；若采纳 A-2 的 `make lint`，可在门禁中加一条「18 个 Task 的 `verify.lint` 字符串一致」断言（本阶段不动） |

**每条 `verify` 的标准形状**（下一阶段按此模板对齐）：

```yaml
verify:
  test: cd evergreen && go test ./internal/<pkg>/...      # 合同类 task 可无 test
  lint: cd evergreen && make lint                          # = gofmt -l . + go vet ./... + 写路径守卫
  run:  cd evergreen && go run ./cmd/eg <subcommand> --json # 仅有可运行入口的 task 才写
```

规则：① `test` 只跑本 task 归属的包，避免与其他 task 相互打红；② `lint` 全仓统一、18 处**逐字相同**；③ `run` 必须是**零副作用或在临时 vault 内**可重复执行的命令；④ 所有命令的 CWD 语义见 A-1；⑤ 命令里**不得**出现沙箱外绝对路径。

---

*本基线由 项目维护者 于 2026-08-31 按语言选型决策（结论 Go）更新。§0/§15–§20 为本次新增；§1–§14 既有内容未改。*
