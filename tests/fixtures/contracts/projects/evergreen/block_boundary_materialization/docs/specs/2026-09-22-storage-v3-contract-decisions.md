# EverGreen Storage v3 正式合同：结构候选与确定性物化

- 日期：2026-09-22
- 状态：正式合同
- 产品基线：`feature/knowledge_opinion_split/integration@ff9868652b685eae4502ad0b546e3912724117c0`
- Teamwork 基线：`main@72e607f4ef06d3366a09a1f788919eec6eab03e1`
- 目标版本：**`0.8.0-m8`**
- 是否推送远端 / 创建发布 tag：未知；由最终收口任务在聚合回归与真实 Vault 迁移后决定
- 适用范围：H3 首期、后续 L2、派生 sidecar、迁移与最终收口

本文是 Epic `block_boundary_materialization` 的唯一实现合同。旧文档
`docs/specs/2026-09-21-structural-boundary-materialization-plan.md` 与产品仓
`docs/BLOCK_BOUNDARY_STORAGE.md` 只保留调研和历史方案价值；与本文冲突时以本文为准。

## 0. 不变量

1. Markdown 与 Git 是唯一权威。`.index/`、候选 sidecar 和 SQLite 都可删除重建。
2. Note 是完整来源正文、原有顺序、显式批注和用户补充的审阅式学习版。物化不得删除、
   移动、改写候选载荷之外的 Note 内容；候选载荷本身在物化时也不得被改写。
3. Knowledge 使用 `k-*` 与 `domains/<domain>/knowledge/`；Opinion 使用 `o-*` 与
   `domains/<domain>/opinions/`，新 Opinion 恒写 `validation: pending`。
4. 草稿覆盖与最终 `extraction_coverage` 是两种状态。草稿不得伪造尚不存在的 outputs，
   也不得用 `note_only` 或 `missing` 冒充“以后会物化”。
5. 物化不调用模型、不访问网络，只解析、定位、校验、复制确定字节、生成固定模板字节并落盘。
6. 优先复用现有 goldmark、`internal/mdfile` 保字节设施、ID/路径构造、Knowledge/Opinion
   writer、atomic overlay、txn journal v1、Git 提交和写后索引同步。
7. `mark-reviewed` 在真实 Vault 完成迁移并通过最终聚合回归前继续冻结。

## D1. H3 候选线格式与边界

### D1.1 权威落位

H3 候选只允许位于 Note 的 `## 提取结果` 分区。`## 整理正文`、`## 存疑与待验证`
和 `## 用户补充` 内的 H3 永远不是候选；因此来源正文里的原始标题与 Agent 批注标题不会被误认。

候选的可见形态如下：

```markdown
<!-- eg:cd:1 eyJzIjpbIkwxNC1MMjgiXSwiciI6InN1cHBvcnQiLCJ3IjoiLi4uIiwidCI6W119 -->
### ReAct Loop 的执行流程 {#cand-react-loop .eg-candidate .knowledge}

#### 知识内容

ReAct 在一次循环中交替生成推理轨迹与动作，并把环境观察带入下一轮。

#### 条件与边界

这里描述终止条件和工具失败边界。
```

标题属性的封闭合同：

- 标题必须是 ATX H3；候选识别使用已引入的 goldmark，并启用
  `parser.WithAttribute()`。
- `id` 必须匹配 `^cand-[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`，并在同一 Note 内唯一。
  它是稳定候选键，不是实体 ID，不进入 `model.KnownPrefixes()`。
- classes 必须包含 `.eg-candidate`，并且恰含 `.knowledge` 或 `.opinion` 之一。
- 除 `id` 和 classes 外，标题属性不承载可变状态。物化映射不塞进标题行，避免每次状态变化
  改写用户可见标题。

候选前一行必须是版本化机器锚点：

```text
<!-- eg:cd:1 <base64url(JSON)> -->
```

v1 载荷恰含：

```json
{
  "source_refs": ["L14-L28"],
  "rel": "support",
  "reason": "原文逐段定义了该流程",
  "tags": ["agent"],
  "output": ""
}
```

- `source_refs` 非空，逐项精确引用该 Note 已存在的 source block。
- `rel` 必须是现有材料关系枚举；`reason` trim 后非空；`tags` 保序。
- `output` 在草稿态为空，首次物化成功时写入最终 `k-*` 或 `o-*`。
- 未知锚点版本、未知键、重复 JSON 键、锚点与 H3 不相邻、kind 与 output 前缀不一致均
  fail closed。

L1 边界从候选 H3 标题行首字节开始，到下一个 H1/H2/H3 标题行首字节或当前 H2
分区末尾结束。代码围栏内的 `#` 不参与边界。候选锚点不属于载荷；它只参与定位和状态映射。

### D1.2 L2 延后但语法冻结

H3 首期只实现 L1。L2 在 H3 闭环集成后实现，采用 Pandoc fenced div 最小子集：

```markdown
<!-- eg:cd:1 <base64url(JSON)> -->
::: {#cand-cross-section .eg-candidate .opinion}
### 跨小节的候选主张

#### 观点

...
:::
```

开围栏必须为独立行、至少三个冒号、属性合同与 H3 相同；闭围栏使用不少于开围栏数量的
冒号且不得带属性；`eg:cd:1` 锚点必须逐行紧邻开围栏。开围栏下一行必须是无属性的 ATX H3，
其可见文本是 candidate title；后续 H4 仍按 D3 模板解释。这样 plain export 删除开闭围栏后，
保留下来的 H3/H4 与 L1 可见结构相同，而 title 不需要进入机器锚点或新增属性。

不允许嵌套；代码围栏优先，代码围栏内的冒号不生效。容器内 H2/H3/H4/H5、列表、表格、
代码块和多段正文都属于该容器，只有与开围栏匹配的闭围栏结束 candidate；其中紧邻 opener
的首个 H3 只提供 title，D3 模板 H4 仍提供物化 payload。L2 与 L1 使用同一 candidate 结构、
模板映射和物化内核，只替换 Span 提供者。HTML 注释哨兵不实现、不迁移。

### D1.3 推翻条件

只有 goldmark 无法稳定给出 H3/属性源字节位置，且有最小复现证明时，才能改用补充行扫描；
不得因此引入第二套 Markdown 语义。任何语法调整必须先修改本文和对应 golden/fuzz 判据。

## D2. 草稿保存、编辑与覆盖状态

### D2.1 ChangePlan 草稿输入

`write_note` v2 增加两个可选字段：

```json
{
  "candidate_drafts": [
    {
      "key": "cand-react-loop",
      "kind": "knowledge",
      "title": "ReAct Loop 的执行流程",
      "source_refs": ["L14-L28"],
      "rel": "support",
      "reason": "原文逐段定义了该流程",
      "tags": ["agent"],
      "sections": [
        {"name": "知识内容", "body": "..."},
        {"name": "条件与边界", "body": "..."}
      ]
    }
  ],
  "candidate_coverage": [
    {
      "module": "m-002",
      "source_refs": ["L14-L28"],
      "summary": "ReAct 的循环步骤",
      "disposition": "candidate",
      "candidates": ["cand-react-loop"]
    }
  ]
}
```

`candidate_drafts[]` 的数组顺序、`sections[]` 顺序和 body 字节原样保留。
`candidate_coverage[].disposition` 是封闭三值：

- `candidate`：至少引用一个本次或盘上存在的 candidate key，不带 reason。
- `note_only`：不带 candidates，必须给出真实、非空 reason。
- `unresolved`：不带 candidates，必须说明尚未处理的具体缺口；允许保存草稿，但表示提炼尚未完成。

当 `candidate_drafts[]` 给出时：

- `output_cards` 必须省略或为空。
- 最终 `extraction_coverage` 必须省略或为空。
- 不得把未物化 candidate key 或预计的 `k-*` / `o-*` 写进 outputs。
- `candidate` 处置不得降格为 `note_only`；未完成项不得伪装成 `missing` 后尝试 apply。

未使用 `candidate_drafts[]` 的既有 v2 `write_note` 行为保持兼容，供存量 plan 与迁移工具使用；
新 SKILL 主路径只生成候选草稿。

### D2.2 保存与编辑

- 初次保存由 `write_note.candidate_drafts[]` 完成，writer 把候选追加在 `提取结果` 的机器管理区。
- 用户也可直接编辑 Markdown。
- `eg edit` 增加 candidate 模式：
  `eg edit --target <n-id> --candidate <cand-key> --section <模板分区> --content <text|file> --user-request`。
  它复用 `edit_section` op，在该 op 上新增可选 `candidate`；未给 candidate 时旧 H2 行为逐字不变。
- candidate 模式只替换一个 H4 模板分区的 body。候选 H3、其它 H4、机器锚点、
  Note 其它分区与 frontmatter 逐字不动。
- 已有非空 `output` 的候选不可再通过 candidate 模式编辑。用户直接改动后，后续 materialize
  必须报“已物化候选发生漂移”，不得静默覆盖目标卡。

### D2.3 草稿覆盖与最终覆盖

`candidate_coverage` 是草稿事实；`extraction_coverage` 是最终事实，两者不共用状态字段。
仅当同一 Note 满足以下条件时，物化器才生成最终覆盖：

1. `candidate_coverage` 不含 `unresolved`；
2. 每个 `candidate` 行引用的候选都有非空、合法的 `output`；
3. 每个输出只指向已存在且内容校验通过的 Knowledge/Opinion；
4. 每个来源范围至少被一条 candidate 或真实 note_only 行覆盖。

最终矩阵按原 `candidate_coverage` 顺序确定性映射：`candidate` → `outputs`，
合法 `note_only` 原样转入；最终矩阵永不生成 `missing`。在条件未满足前，
Note 明确显示草稿覆盖，不声称 extraction 完成。

草稿覆盖在 `提取结果` 的全部 candidate 之后渲染为 `### 候选覆盖` 表，列固定为
`模块 | 来源范围 | 语义模块 | 草稿处置`。每条可见行对应一条版本化机器锚点：

```text
<!-- eg:cc:1 <base64url(JSON)> -->
```

v1 载荷恰含 `module/source_refs/summary/disposition/candidates/reason` 六键；空的
`candidates` 仍编码为 `[]`，空的 `reason` 仍编码为 `""`。可见表由锚点重渲染后与原字节
交叉核对，未知版本、未知/缺失键、重复 JSON 键、重复 module、可见表漂移均 fail closed。
该协议不复用最终覆盖的 `eg:nc:1`，以保证草稿状态不能被误读为最终 extraction coverage。

## D3. Knowledge/Opinion 模板载荷映射

候选 H3 内只允许以下 H4 直接子分区；H5 及以下属于最近一个 H4 的正文。

| kind | H4 分区 | 约束 | 物化目标 |
| --- | --- | --- | --- |
| knowledge | `知识内容` | 恰一、非空、required | `## 知识内容` |
| knowledge | `条件与边界` | 至多一、optional | `## 条件与边界` |
| opinion | `观点` | 恰一、非空、required | `## 观点` |
| opinion | `论据与推理` | 至多一、optional | `## 论据与推理` |
| opinion | `条件与反例` | 至多一、optional | `## 条件与反例` |
| opinion | `待验证` | 至多一、optional | `## 待验证` |

候选内禁止 `用户补充`。最终文件仍由现有模板创建其 `## 用户补充` 空分区，任何自动路径不得写入。

每个 H4 的 payload 是“标题行结束之后到下一个同级或更高级标题之前”的原始半开字节区间。
候选 authoring 必须提供行结束载荷。物化只生成 canonical H2 标题和模板间隔，再把 payload
原字节复制给现有 writer；不得 trim、翻译、总结、排序、去重或重排。测试以源 payload
区间与目标 H2 body 的 `bytes.Equal` 为准，不以渲染后文本近似为准。

目标 frontmatter 复用现有 writer：

- Knowledge：`id/title/status/created_at/updated_at/sources[]/tags[]`。
- Opinion：同上并固定增加 `validation: pending`。
- `source` 与 `note` 从 Note frontmatter 和被定位的 Note ID 取得。
- `rel/reason/tags` 从 candidate 锚点取得；物化器不得补写语义性默认值。
- 不新增 `candidate`、`type`、`domain` 等 frontmatter 键；现有 blacklist 继续生效。

## D4. 持久映射、跨日幂等与 Note 保真

候选的持久身份是 `(note_id, candidate_key)`。首次物化：

1. 用命令执行时注入的本地日期和候选标题调用现有 `model.NewCardID` 或
   `model.NewOpinionID`。
2. 用现有 `store.CardRel` / `store.OpinionRel` 得到目标路径。
3. 校验目标路径和全库 ID 尚未被其它实体占用。
4. 生成目标文件字节。
5. 只把 candidate 锚点的 `output` 从空串改为最终 ID；标题行、候选 payload、Note
   其它字节不动。
6. 若全部候选已闭合，再更新 `提取结果` 中机器管理的最终 output list 与
   `extraction_coverage`。

`output` 是跨日幂等的唯一映射真源。后续任意日期重跑：

- output 合法、目标存在、kind/path/title/payload hash 均一致：成功 no-op，零权威写、零空 commit。
- output 合法但目标缺失或内容漂移：fail closed，零写入。
- output 为空但按当日生成的 ID 已被占用：fail closed，要求先解决冲突，不追加猜测后缀。
- 同一 Note 内候选 key 重复或同一 output 被两个 candidate 映射：fail closed。

物化前后必须逐区间证明以下字节不变：`整理正文` 全部来源正文与批注、
`存疑与待验证`、`用户补充`、candidate H3 标题和全部 candidate payload。允许变化的
Note 字节只有对应机器锚点的 `output` 与机器生成的最终提取结果区。

## D5. journal v1 多文件事务与恢复

不升级 journal。`SupportedJournalVersion` 保持 1，`Intent` 字段集合不变。

首期物化复用现有完整链路：

```text
run.lock
  -> Recover 屏障
  -> 锁内重读 Note / base
  -> 内存预演
  -> accepted write-set
  -> AllocateTxnID
  -> WriteIntent(journal_version=1)
  -> txn.Commit
  -> Git 单提交
  -> 写后派生同步
```

首次物化的 accepted write-set 至少包含：

1. 新建的 `knowledge/<k-id>.md` 或 `opinions/<o-id>.md`；
2. 更新后的 Note。

多个候选可在一次命令中物化，全部目标和 Note 只出现于同一个 write-set。写集合按“目标路径
字典序，Note 最后”确定性排序；事务正确性不依赖该顺序。每项沿用 v1 的 pre/target hash、
create 位和前像副本；崩溃后沿用 B-R1/B-R2/B-R3 两遍恢复。禁止：

- 在 txn 外先写目标或先改 Note；
- 为 materialize 自造临时文件协议；
- 在 commit marker 前把预演事实报告为已写入；
- 因索引/sidecar 失败回滚已经提交的 Markdown；
- 将并发读取者可能看到的 rename 中间窗口误述为文件系统级多路径原子快照。

恢复出口仍是最终收敛到完整前像或完整目标态；post-crash 外部编辑继续 E15 fail closed。

## D6. CLI、查询、导出、派生层与版本

### D6.1 命令

顶层命令从 23 增至 25：

```text
eg materialize --note <n-id> [--candidate <cand-key>] [--all]
               --user-request [--strict] [--json]
eg export --plain --output <dir> [--json]
```

- `--candidate` 与 `--all` 恰一；materialize 是用户显式写路径，必须带 `--user-request`。
- materialize 参数/结构校验失败退 1/2；B3 skip 退 3；Git 失败退 4；txn/lock 阻断退 5，
  与现有写命令分级一致。
- `export --plain` 对 vault 只读，不创建 Git commit；输出目录不得位于 vault 内、
  不得是 symlink 逃逸路径，非空目录默认拒绝。
- 不新增 `materialize_card` ChangePlan op。`eg materialize` 在进程边界取得
  `--user-request` 后构造类型化 materialize request，并复用 store atomic/txn 编排；
  Agent 计划只负责保存草稿，不能自行把草稿变成正式 Knowledge/Opinion。
- `plan_version` 保持 2；主链路/M3/M4 op 闭集计数不因 Storage v3 改变。

### D6.2 查询

H3 首期直接扫描权威 Markdown：

- `eg context --json` 增加 `draft_candidates`，只返回候选摘要、状态、output 和 payload hash，
  不把候选混入 `knowledge_candidates` / `opinion_candidates`。
- Note 查询结果可列出候选；Knowledge/Opinion 搜索仍只看已物化文件。
- sidecar 落地后可加速，但返回集合、排序和诊断必须与直接扫描逐项等价。

### D6.3 plain export

plain export 保留全部可见正文和顺序，只剥离 EverGreen 专有协议：

- 删除 `eg:nr:*`、`eg:cd:*` 和覆盖矩阵机器锚点；
- 从候选 H3 删除 `.eg-candidate`、kind class 和 `#cand-*` 属性；
- 删除 L2 的开闭围栏行；
- 保留 H3/H4 标题、候选 payload、来源正文、可见 Agent blockquote、用户补充和普通 Markdown。

导出后逐文件做内容守恒检查：除上述专有协议字节外，无任何可见正文丢失或重排。

### D6.4 sidecar 与 L2

sidecar 位于 `.index/blocks/`，是按 Note 生成的确定性 JSON；删除后可由 Markdown 全量重建。
它不保存任何 Markdown 中不存在的身份、映射或正文。`eg index rebuild` 保留 `run.lock/txn`
的现有规则，同时重建 sidecar；sidecar 失败只产派生层诊断，不改变 Markdown 成功结论。

每个可解析 Note 对应 `.index/blocks/<note-id>.json`，v1 canonical JSON 字段固定为：

```json
{
  "schema_version": 1,
  "note_id": "n-20260922-example",
  "note_path": "domains/example/notes/n-20260922-example.md",
  "note_hash": "sha256:<64hex>",
  "candidates": [
    {
      "key": "cand-example",
      "kind": "knowledge",
      "syntax": "h3",
      "title": "Example",
      "status": "draft",
      "output": "",
      "payload_hash": "sha256:<64hex>",
      "span": {
        "anchor_start": 0,
        "anchor_end": 10,
        "boundary_start": 10,
        "boundary_end": 100,
        "heading_start": 10,
        "heading_end": 30,
        "content_end": 90
      }
    }
  ],
  "diagnostics": []
}
```

- `schema_version` 独立于 SQLite `schema_version=2`；sidecar 不新增表、列或 `index_meta` 键。
- 每个可解析 Note 都生成一份文件；无 candidate 时 `candidates` 为 `[]`。candidate 协议
  解析失败时 `candidates` 为 `[]`，`diagnostics` 保存可重算的 Q1，不伪造部分结果。
- `status` 只由 `output` 是否为空确定；`payload_hash` 覆盖与 H3/L2 共用的可见 candidate
  区间，sidecar 不保存 payload 正文。
- JSON 字段、数组与缩进编码均 canonical；未知字段、非法枚举、路径逃逸、文件名与
  `note_id` 不一致、重复 Note/candidate、非 canonical 字节均视为损坏。
- `build` / `rebuild` 全量生成；`sync` 和健康索引的写后同步按 Note 增量更新。
  `.index/blocks/` 是可清扫派生目录，不加入必须跨重建保留的 `run.lock/txn` 名册。
- `status` 对 sidecar 缺失、陈旧/孤儿、损坏分别映射 `W23`、`W22`、`W24`。
  `eg context` 只读取与当前 Markdown 投影逐字一致的 sidecar；任一不健康状态都给出对应
  W 码与 `Q5`，然后使用同一投影函数的直接扫描结果，集合、顺序、状态、output、hash
  与健康 sidecar 路径逐项相同。
- sidecar 的任何同步失败都发生在权威 Markdown 提交之后，只追加派生层 warning；
  不回滚已提交 Markdown，不改变原命令退出码。

L2 只增加 Span provider，不复制模板、物化、事务、查询或导出逻辑。

### D6.5 0.8.0-m8 与 D-3

Storage v3 的用户接口进入 `0.8.0-m8` 时，必须在同一批移除：

- `query.Context.Candidates`；
- `eg context --json` 的 `candidates`；
- D-3 的无条件弃用 `I1`；
- README / README.zh-CN / INSTALL / SKILL 与对应测试中的 0.7.x 兼容说明。

`knowledge_candidates` 与 `opinion_candidates` 保留。不得把 D-3 清理拖到真实 Vault 迁移之后。

## 7. 实现边界

- H3 首期只做本合同需要的窄 API，不扩展为通用 Markdown 编辑框架。
- `internal/mdfile` 继续零 YAML 序列化写回；机器锚点使用结构化 JSON +
  base64url，并沿用重复键/未知字段 fail-closed 纪律。
- 候选解析器复用 goldmark AST 定位；只有 AST 未暴露的精确行尾/属性替换区间允许使用
  受测试约束的局部行扫描。
- `BlockKind` 首期不新增 candidate 值。L2 若需要 fenced container 块型，只在 L2 批次
  以独立封闭合同修改。
- SQLite 仍恰 6 表、`index_meta` 仍恰 6 键；sidecar 不进 SQLite schema。
- 真实 Vault 迁移只执行一次：先完成工具、fixture 和副本演练，最终固定 SHA 聚合回归全绿后执行。
- 迁移输入若含与权威产物同 ID 的 golden Markdown，只能在迁移期间作为 ignored 输入存在；
  apply 与幂等重跑验收后，必须先完整备份到 Vault 外并移出 Vault，再恢复依赖全库 ID
  解析的常规写命令。该边界来自 T-008 完整副本实测：`Store.ScanIDs` 按“文件可移动”的 F2
  合同扫描 Vault 内 Markdown，不能同时把同 ID golden 猜成非权威副本；移出输入后
  `eg materialize --all` 对 25 个既有映射返回 finalized no-op。

## 8. 机械验收

1. H3/属性/锚点 parser 的 golden、negative、fuzz 覆盖围栏内伪标题、重复 key、未知版本、
   重复 JSON 键、畸形属性、重复 candidate、越级标题和 EOF。
2. candidate edit 只改一个 H4 payload；前后字节区间逐段 `bytes.Equal`。
3. Knowledge/Opinion 每个模板分区均有 payload 字节等价测试，Opinion 固定 pending。
4. 首次物化、同日重跑、跨日重跑、目标缺失、目标漂移、ID 冲突均有确定性断言。
5. txn v1 对 2 个及多个目标的 failpoint/recover 覆盖，journal 版本仍为 1。
6. `eg materialize`、`eg export --plain`、context/query 和真实 Note→Knowledge+Opinion
   端到端通过。
7. `0.8.0-m8` 批次断言 `candidates` 与其弃用 I1 均已移除。
8. L2 与 H3 对同一 Candidate 产出逐字等价；sidecar 删除重建逐字等价。
9. 迁移副本演练输出逐文件 diff、哈希和计数；真实 Vault 在最终聚合回归前零写入。
10. 最终固定 SHA 上以 `LC_ALL=C` 跑完整聚合回归；无 skip、xfail、allowlist 或弱断言。
