# M5（S4 检索与性能）索引架构与 SQLite/FTS5 选型裁决合同

- 合同 ID：`2026-12-19-m5-index-architecture-contract`
- 里程碑：`M-005`（M5 检索与性能），完成判据 **1 / 2** 与风险 **R-21 / R-22 / R-24** 的直接交付物
- 责任 task：`T-evergreen.s1_main_flow-158614-064`（Contract 型，`depends_on: []`）
- 目标日：`2026-12-19`（== `T-064.due`）
- 编制日：2026-09-08
- 设计出处：`docs/specs/2026-08-31-evergreen-s1-tech-design.md` §13 / §16.1 / §16.6 / §19；
  `docs/specs/2026-08-31-language-selection.md` 第九节 `U-7`
- 判定统一在 `EvergreenDir` 根执行；`E=evergreen`，`T=teamwork`，
  `D=teamwork/projects/evergreen/s1_main_flow/docs/specs`，`VAULT` = 被操作的 vault 根（一个独立 git 仓）。

> **本合同的效力**：`T-…-065` / `066` / `067` / `068` 的实现者**只读本合同**即可确定 schema 形态、
> 诊断码、降级语义、分页参数与门槛口径，**无需再回问 owner**；`T-…-069` 收口时逐条复述本合同结论。
> 本合同**不含任何产品代码**，`evergreen/` 下零改动，`evergreen/go.mod` 一字未改
> （依赖引入属 `T-…-065` 的施工面）。
>
> **裁决授权留痕（诚实出口的适用判定）**：`T-064` 的 DoD 为「owner 关闭 A-41 ~ A-48 八项」。
> 本轮 owner 于 **2026-09-08** 以实施指令逐字授权：「严格按 T-064 DoR/Scope/Acceptance
> **冻结** M5 索引架构与 SQLite/FTS5 技术决策；必须验证纯 Go、`CGO_ENABLED=0`、静态编译、
> Linux/macOS/Windows 约束，Markdown 保持权威来源，`.index/` 仅为可重建派生」。
> 据此 A-41 ~ A-48 **八项全部关闭**，`T-064` 不走「未知 / 待 owner 确认 → `blocked`」出口。
> **登记型** A-49 / A-50 / A-51 不在本合同 DoD 内，其状态在 §12 如实转录，**不视为已裁决**。

---

## 1. 范围与非目标

### 1.1 权威性总纲（本条为全 M5 的最高约束）

**Markdown 是唯一权威来源，.index/ 恒为可重建派生。**

该句是 S4 全部技术选择的上位约束，展开为四条不可协商的推论：

| # | 推论 | 机器反证形态（由下游 task 落地） |
|-|-|-|
| P-1 | `internal/index` **只读** 权威 Markdown，**从不写**权威 Markdown | `[ "$(grep -rnE 'os\.WriteFile\|os\.Create\|os\.Rename' internal/index/ \| grep -v _test.go \| grep -vc '\.index' )" -eq 0 ]`（M-005 判据 3） |
| P-2 | 删掉整个 `.index/` 后，所有读命令的 `--json` 的 `.data` **逐字不变**，仅多出诊断码 | `bash test/e2e/m5_degrade_fallback.sh`：`diff <(索引在位 .data) <(rm -rf .index 后 .data)` 空差异（M-005 判据 8） |
| P-3 | 索引**不做真相仲裁**：索引与 Markdown 冲突时，**Markdown 胜**，索引判为陈旧并触发 `sync`/`rebuild` | `TestStaleDetectedOnExternalEdit` / `TestSyncConvergesToClean`（M-005 判据 7） |
| P-4 | `.index/` **不入库**，不参与 diff / MR review / `SHA256SUMS`，不作为任何验收产物 | `.gitignore` 含 `.index/` 整目录；`git check-ignore -q .index/eg.db` 退 `0`（§3.4） |

### 1.2 本合同的范围（in scope）

1. A-41 ~ A-48 八项阻塞裁决的**结论**与**机器反证形态**（§10 登记表，恰 8 行）。
2. SQLite 驱动选型的**只读实证记录**：`PRAGMA compile_options` 原始输出、FTS5 与 `trigram` 可用性、
   中文召回实测、降级链（§2）。
3. `.index/` 目录与文件布局、`.gitignore` 粒度、是否入库（§3）。
4. Schema 表集合封闭口径与 `IndexSchemaVersion` 语义（不兼容 = **重建而非迁移**）（§4）。
5. 一致性水位线与陈旧判定粒度（§5）。
6. 降级语义与诊断码 `W22` / `W23` / `W24` / `W25` / `Q5` 分配表（§6）。
7. 五条性能指标的**采样口径**（数值留给 `T-…-068` 实测回填）（§7）。
8. 命令面（`eg index` 子命令集合、`eg bench`）与分页参数形态（§8）。
9. 静态构建保全条款（§9）。

### 1.3 非目标（out of scope，本 task 明确不做）

| 不做 | 归属 |
|-|-|
| 引入任何依赖到 `evergreen/go.mod` | `T-…-065` |
| 新建 `internal/index` 下任何文件（含 `doc.go` / `schema.go`） | `T-…-065` |
| 实现全量索引构建、损坏检测、`eg index rebuild` | `T-…-065` |
| 实现增量索引、水位线落盘、`eg index sync` | `T-…-066` |
| 改写 `internal/query` 读路径、实现后端选择 `SelectBackend` | `T-…-067` |
| 写 `test/perf/corpus_gen.go`、`eg bench`、回填五条门槛**数值** | `T-…-068` |
| 推进版本号到 `0.5.0-m5`、重建 dist、写 M5 验收报告 | `T-…-069` |
| 任何 e2e 脚本（`m5_*.sh` 恰 11 个） | `065` ~ `069` |
| 改 M1 ~ M4 的历史文档、历史 Task、历史 Activity Log、历史验收结论与历史门禁断言 | 全程禁止 |
| 启用退出码 `5`、`run.lock`、事务日志、块级合并 | `M-006`（M6） |
| 关闭 A-49 / A-50 / A-51（登记型） | 非本 task 的 DoD |

### 1.4 S4 的包边界（转录权威侧 §13，本合同不新增也不放宽）

- `internal/index/` 的包头注释首行必须是 `// [S4]`。
- **依赖禁令（单向）**：`internal/index` **不得** import `internal/store` / `internal/plan` /
  `internal/cli` / `internal/report` / `internal/proposal` / `internal/reconcile`。
  方向只允许 `query → index`，**不允许** `index → query`。
- `internal/index` **不得** import `os/exec`（Git 只读能力经 `internal/git` 由调用方注入，
  索引层只接收已解析好的 `head` 字符串）。
- 上述三条的机器反证已固化在 `M-005` 判据 3，本合同不重复定义，只声明**不得放宽**。

---

## 2. 驱动选型与实证记录（A-41 / A-42）

### 2.1 待定项 `U-7` 的原文与本轮处置

`docs/specs/2026-08-31-language-selection.md` 第九节 **`U-7`** 逐字记载
`modernc.org/sqlite` 的「**FTS5 支持与性能未知**」，状态为「未安装、未验证」；
`docs/specs/2026-08-31-evergreen-s1-tech-design.md` §19 同步登记 `U-7` / `U-8` 两条「未知」。

**本轮处置**：`U-7` 由「未知」转为「**已实测**」。实证在
**`/tmp/t064_probe` 的独立临时 Go module** 内完成，`evergreen/go.mod` 一字未改；
完整输出在册：`/tmp/t064_probe_fts5.log`（FTS5 / 分词）与
`/tmp/t064_static_crosscompile.log`（静态 / 交叉编译）。
`U-8`（`.index/` 在 S4 的实际形态未知）由本合同 §3 / §4 关闭。

### 2.2 驱动版本的可用性实测（含一条必须写进合同的约束）

| 试用版本 | 结果 |
|-|-|
| `modernc.org/sqlite@latest`（解析到 `v1.58.0`） | **不可用**：`requires go >= 1.25.0 (running go 1.24.13; GOTOOLCHAIN=local)` |
| `modernc.org/sqlite v1.45.0` | **可用**，本合同**冻结此版本为 M5 下限基线** |

> **裁决（版本约束，`T-…-065` 硬消费）**：`go.mod` 写 `modernc.org/sqlite v1.45.0`。
> **不得**使用 `@latest`：当前工具链为 Go `1.24.13` 且 `GOTOOLCHAIN=local`，
> `v1.58.0` 起要求 Go ≥ 1.25，升级 `latest` 会直接打断构建。
> 升级驱动版本属**独立变更**，需同时给出 Go 工具链升级依据，**不得**在 `065` ~ `068` 内顺手做。

实测运行时 SQLite 版本：`sqlite_version() = 3.51.2`；`sql.Open` 驱动名 = `"sqlite"`。

### 2.3 `PRAGMA compile_options` 原始输出（实证留痕，M-005 判据 2 逐字要求）

以下为 `PRAGMA compile_options` 在 `modernc.org/sqlite v1.45.0` 上的**原始输出**（54 条，未删改、未排序重写）：

```
ATOMIC_INTRINSICS=1
COMPILER=gcc-12.2.0
DEFAULT_AUTOVACUUM
DEFAULT_CACHE_SIZE=-2000
DEFAULT_FILE_FORMAT=4
DEFAULT_JOURNAL_SIZE_LIMIT=-1
DEFAULT_MEMSTATUS=0
DEFAULT_MMAP_SIZE=0
DEFAULT_PAGE_SIZE=4096
DEFAULT_PCACHE_INITSZ=20
DEFAULT_RECURSIVE_TRIGGERS
DEFAULT_SECTOR_SIZE=4096
DEFAULT_SYNCHRONOUS=2
DEFAULT_WAL_AUTOCHECKPOINT=1000
DEFAULT_WAL_SYNCHRONOUS=2
DEFAULT_WORKER_THREADS=0
DIRECT_OVERFLOW_READ
ENABLE_COLUMN_METADATA
ENABLE_DBSTAT_VTAB
ENABLE_FTS5
ENABLE_GEOPOLY
ENABLE_MATH_FUNCTIONS
ENABLE_MEMORY_MANAGEMENT
ENABLE_OFFSET_SQL_FUNC
ENABLE_PREUPDATE_HOOK
ENABLE_RBU
ENABLE_RTREE
ENABLE_SESSION
ENABLE_SNAPSHOT
ENABLE_STAT4
ENABLE_UNLOCK_NOTIFY
LIKE_DOESNT_MATCH_BLOBS
MALLOC_SOFT_LIMIT=1024
MAX_ATTACHED=10
MAX_COLUMN=2000
MAX_COMPOUND_SELECT=500
MAX_DEFAULT_PAGE_SIZE=8192
MAX_EXPR_DEPTH=1000
MAX_FUNCTION_ARG=1000
MAX_LENGTH=1000000000
MAX_LIKE_PATTERN_LENGTH=50000
MAX_MMAP_SIZE=0x7fff0000
MAX_PAGE_COUNT=0xfffffffe
MAX_PAGE_SIZE=65536
MAX_SQL_LENGTH=1000000000
MAX_TRIGGER_DEPTH=1000
MAX_VARIABLE_NUMBER=32766
MAX_VDBE_OP=250000000
MAX_WORKER_THREADS=8
MUTEX_PTHREADS
SOUNDEX
SYSTEM_MALLOC
TEMP_STORE=1
THREADSAFE=1
```

**结论**：`ENABLE_FTS5` **在册**（第 20 行），因此 FTS5 **已编入** `modernc.org/sqlite v1.45.0`，
`U-7` 中「FTS5 支持未知」这一半就此关闭。`COMPILER=gcc-12.2.0` 是 modernc 用
C-to-Go 转译器（`ccgo`）在**生成期**记录的编译器串，**不代表运行期需要 cgo 或 C 工具链**，
§9 的 `CGO_ENABLED=0` 实测（`statically linked` / `not a dynamic executable`）是该判断的机器反证。

### 2.4 A-41 裁决：驱动选型 = `modernc.org/sqlite`（纯 Go）

**采用选项 ①**：`modernc.org/sqlite v1.45.0`（纯 Go）。

- **排除选项 ②** `mattn/go-sqlite3`：cgo 实现，会破坏静态单二进制与交叉编译（§9 已给出
  `CGO_ENABLED=1` 产出 `dynamically linked` 的反证）。**永久禁止入 `go.mod`**，
  硬断言：`go list -m all | grep -c 'mattn/go-sqlite3'` 必须为 `0`（M-005 判据 16 已含此条）。
- **排除选项 ③** 纯 Go 自研倒排索引：仅作为 **R-21 的第三档降级**保留。
  本轮实证证明 SQLite + FTS5 可用，因此**不启用**选项 ③；
  若未来驱动整体不可用，才按 R-21 的处置改挂倒排文件格式（判据条数不减）。

实测引入 `modernc.org/sqlite v1.45.0` 后的间接依赖闭包（11 条，`T-…-065` 应预期到这一组，
不得因「依赖变多」而改选型）：

```
github.com/dustin/go-humanize v1.0.1 // indirect
github.com/google/uuid v1.6.0 // indirect
github.com/mattn/go-isatty v0.0.20 // indirect
github.com/ncruces/go-strftime v1.0.0 // indirect
github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
golang.org/x/exp v0.0.0-20251023183803-a4bb9ffd2546 // indirect
golang.org/x/sys v0.37.0 // indirect
modernc.org/libc v1.67.6 // indirect
modernc.org/mathutil v1.7.1 // indirect
modernc.org/memory v1.11.0 // indirect
modernc.org/sqlite v1.45.0 // indirect
```

> 注意：`github.com/mattn/go-isatty` 与被禁的 `github.com/mattn/go-sqlite3` **是不同模块**，
> 禁令断言必须逐字匹配 `mattn/go-sqlite3`，**不得**写成 `grep mattn` 之类的宽匹配，
> 否则会把合法的 `go-isatty` 误判为违规。

### 2.5 中文分词实测（A-42 的事实依据）

**P3：`tokenize='unicode61'`**——建表成功，但 CJK 召回塌陷，实测：

| 查询 | 命中 |
|-|-|
| `MATCH "中文分词"` | `hits=0` |
| `MATCH 中文*` | `hits=0` |
| `MATCH "水位线"` | `hits=0` |
| `MATCH 索引` | `hits=0` |
| `MATCH tokenizer`（ASCII 对照） | `hits=1` |

**P4：`tokenize='trigram'`**——**可用**（未报 `no such tokenizer`），实测：

| 查询 | 命中 | 说明 |
|-|-|-|
| `MATCH "中文分词"` | `hits=1` | ≥ 3 字符，命中 |
| `MATCH "水位线"` | `hits=1` | ≥ 3 字符，命中 |
| `MATCH "知识卡"` | `hits=1` | ≥ 3 字符，命中 |
| `MATCH "tokenizer"` | `hits=1` | ASCII 命中 |
| `MATCH "分词"` | `hits=0` | **trigram 对 < 3 字符查询不建 token，属上游已知行为，不是缺陷** |
| `MATCH "索引"` | `hits=0` | 同上 |
| `MATCH "卡"` | `hits=0` | 同上 |

**P5：`unicode61` + 写入侧 bigram 预切**——实测可覆盖 2 字词：

| 查询 | 命中 |
|-|-|
| `MATCH 中文分词` | `hits=1` |
| `MATCH 分词` | `hits=1` |
| `MATCH 陈旧` | `hits=1` |
| `MATCH 全文检索` | `hits=1` |

### 2.6 A-42 裁决：`trigram` 主路 + 写入侧 bigram 补路，三档降级链封闭

**采用选项 ②（`trigram`）为主路，并把选项 ①（`unicode61` + 写入侧 bigram 预切）
下沉为同一张表内的补路**——理由：`trigram` 单独无法覆盖 1 ~ 2 字中文查询（P4 实测 `hits=0`），
而知识库检索里「分词」「索引」这类 2 字查询是高频形态，只用 `trigram` 会造成静默漏召回。

**分词与检索的封闭形态（`T-…-065` / `T-…-067` 硬消费，不得自行加档）**：

| 档 | 形态 | 启用条件 | 覆盖 |
|-|-|-|-|
| **D0（主路）** | FTS5 虚表 `cards_fts`，`tokenize='trigram'`，检索 `MATCH` | `ENABLE_FTS5` 在册且 `trigram` 建表成功（本轮实测成立） | ASCII 全部 + 中文 ≥ 3 字符 |
| **D0b（同表补路）** | 同一 FTS5 表额外维护一列 `bigram_text`（写入侧把 CJK 串预切成空格分隔 bigram），该列用 `unicode61` 语义命中 | 与 D0 同时启用，**默认恒开** | 中文 1 ~ 2 字查询 |
| **D1（降级第 1 档）** | 整表退到 `tokenize='unicode61'` + 仅用 `bigram_text` 列检索 | `trigram` 建表失败（`no such tokenizer`） | 中文靠 bigram，ASCII 靠 unicode61 |
| **D2（降级第 2 档）** | 放弃 FTS5，走 `LIKE '%kw%'` + 内存打分（即 A-42 选项 ③） | `ENABLE_FTS5` 不在册 | 全量，性能降级 |
| **D3（终极降级）** | 放弃索引，回落 M2 的全量 Markdown 扫描 | 索引缺失 / 损坏 / 打不开（§6） | 全量，性能最差但**结果等价** |

- 档位选择必须是**单点判定**（与 `M-005` 判据 9 的 `SelectBackend` 单点口径同源），
  `T-…-065` 在建库时把实际生效档位写入 `index_meta`（§4.2 的 `tokenizer_mode` 键），
  `T-…-067` 只读该键，**不得**在读路径重新探测 tokenizer。
- **D0b 的 bigram 预切只写进 `.index/`，绝不回写 Markdown**（P-1）。
- 结果集**不因档位而变**：D0 / D0b / D1 / D2 / D3 之间，同一查询的 `.data` 命中集合必须一致
  （排序与打分可不同，因此 `T-…-068` 的排序键必须是**可复算的确定序**而非依赖 FTS5 `rank`，见 §7.4）。

---

## 3. `.index/` 目录与文件布局（A-43）

### 3.1 A-43 裁决：`.index/eg.db` 单文件 + `.gitignore` 整目录忽略

**采用选项 ①**：`.index/eg.db` 单文件，水位线**存在库内表**（不外挂 `meta.json`），
`.gitignore` **整目录**忽略。

- **排除选项 ②**（`eg.db` + `meta.json` 水位线分离）：水位线与索引内容分处两个文件，
  会引入「两文件不同步」这一**新的自相矛盾态**（`meta.json` 说干净但 `eg.db` 是旧的），
  等于把 M6 才处理的原子性问题提前泄漏到 M5。水位线放在库内表可与索引写入**同事务**提交。
- **排除选项 ③**（索引入库）：与 F6「可重建派生」正面冲突，且会让每次 `eg` 读命令都可能污染
  `git status`，**不可取**。

### 3.2 目录与文件布局（封闭清单）

```
$VAULT/
├── .index/                  # S4 派生目录，整目录 gitignore，可随时 rm -rf
│   ├── eg.db                # 唯一权威索引文件（SQLite），含全部表与水位线
│   ├── eg.db-wal            # SQLite WAL 副文件，运行期自动产生/回收
│   └── eg.db-shm            # SQLite 共享内存副文件，运行期自动产生/回收
└── …（权威 Markdown 树，S1–S3 既有形态，一字不改）
```

- `.index/` 下**允许存在**的文件名集合恰为 `{eg.db, eg.db-wal, eg.db-shm}`；
  出现其他文件视为**外部污染**，按 §6 的 `W24 index_corrupt` 处理（降级 + 建议 `rebuild`），
  **不得**报错退出。
- `eg.db-wal` / `eg.db-shm` 是 `journal_mode=WAL` 的必然产物（实测
  `PRAGMA journal_mode=WAL` → `wal`），**不算污染**，`rebuild` 时随目录一并删除。
- `.index/` **不得**出现在 `dist/` / `SHA256SUMS` / 任何验收产物中。

### 3.3 SQLite 运行期 PRAGMA 口径（实测在册，`T-…-065` 硬消费）

| PRAGMA | 取值 | 实测 | 理由 |
|-|-|-|-|
| `journal_mode` | `WAL` | `wal` | 读写并发；M5 只有单进程写，WAL 主要用于读不阻塞 |
| `synchronous` | `NORMAL`（WAL 下） | 默认观测 `2`（FULL），M5 显式设 `NORMAL` | 索引是可重建派生，**不需要** FULL 的耐久度；崩溃后重建即可 |
| `integrity_check` | 必须为 `ok` | `ok` | §6 `W24` 的判定来源之一 |
| `user_version` | **不使用** | `0` | 版本走**显式表** `index_meta.schema_version`（§4.2），不用 `user_version`，避免与 `PRAGMA` 语义耦合 |
| `foreign_keys` | `ON` | — | schema 内的引用完整性由 SQLite 自检 |

> `synchronous=NORMAL` 的取舍在此明确记录为**有意选择**：M5 允许「崩溃后索引不可用」，
> 因为**索引不可用时必须降级为全量扫描而非报错退出**；权威数据的耐久性由 Markdown + Git 承担，
> 强原子写属 `M-006` 的范围。

### 3.4 `.gitignore` 粒度与是否入库

- vault 侧 `.gitignore` 必须含**整目录**忽略行：`.index/`（M1 现状，本轮**不改**）。
- 机器反证（`T-…-065` 落 e2e）：
  - `git check-ignore -q "$VAULT/.index/eg.db"` 退 `0`；
  - `eg index build` 之后 `git -C "$VAULT" status --porcelain` **为空**；
  - `git -C "$VAULT" ls-files -- .index | wc -l` 为 `0`。
- `eg` **不得**为 vault 自动写/改 `.gitignore`（那属写权威仓的行为）；若 vault 缺该忽略行，
  按 §6 的 `W23` 家族给出一条可读提示即可，**不代替用户改仓**。

---

## 4. Schema 与版本常量口径

### 4.1 表集合封闭（恰 6 张，`T-…-065` 不得增删）

| # | 表 | 类型 | 职责 | 关键列（形态口径，非最终 DDL 逐字） |
|-|-|-|-|-|
| 1 | `index_meta` | 普通表 | schema 版本 + 水位线 + 生效档位（键值对） | `key TEXT PRIMARY KEY`, `value TEXT NOT NULL` |
| 2 | `cards` | 普通表 | 每张知识卡一行（结构化字段） | `id TEXT PRIMARY KEY`, `path TEXT`, `title TEXT`, `status TEXT`, `deprecated INT`, `deleted INT`, `replaced_by TEXT`, `content_hash TEXT`, `mtime_unix INT` |
| 3 | `cards_fts` | **FTS5 虚表** | 全文检索（`trigram` 主路 + `bigram_text` 补路） | `id UNINDEXED`, `title`, `body`, `bigram_text`（`tokenize` 由 §2.6 档位决定） |
| 4 | `relations` | 普通表 | 关系边（正向） | `src_id TEXT`, `verb TEXT`, `dst_id TEXT`, `src_path TEXT`, `line INT`；`UNIQUE(src_id, verb, dst_id)` |
| 5 | `files` | 普通表 | 每个被索引文件一行，增量与陈旧判定的最小单位 | `path TEXT PRIMARY KEY`, `content_hash TEXT NOT NULL`, `size INT`, `mtime_unix INT`, `indexed_at_unix INT` |
| 6 | `skipped` | 普通表 | 被跳过的文件与原因（与 M2 `skipped[].kind` 恰 2 值对齐） | `path TEXT PRIMARY KEY`, `kind TEXT NOT NULL`（取值 ⊆ M2 既有 2 值） |

- **`skipped.kind` 的取值域必须与 M2 既有的恰 2 值逐字相同**，
  索引层**不得**新增第 3 值（M-005 判据 10 有断言 `TestSkippedKindStillTwo`）。
- `relations` 只存**正向边**；`replaced_by` 的**反向查询**由 `cards.replaced_by` 上的索引
  加一次反查实现（§8.4），**不新增第 7 张表**。
- 表集合封闭的机器反证（`T-…-065`）：
  `SELECT name FROM sqlite_master WHERE type IN ('table','view')` 过滤掉 FTS5 影子表
  （`cards_fts_*`）后**恰 6 个名字**，且集合与上表逐字相等（`TestSchemaTablesClosed`）。

### 4.2 `index_meta` 的键集合封闭（恰 6 键）

| 键 | 语义 | 写入时机 |
|-|-|-|
| `schema_version` | `IndexSchemaVersion` 的十进制字符串 | 建库时 |
| `head` | 构建时的 Git HEAD（`git rev-parse HEAD` 全长 40 位；非 git 仓写空串） | 每次 build / sync 提交事务内 |
| `files_hash` | 全部 `files.content_hash` 按 `path` 升序拼接后的 SHA-256（水位线主键） | 同上 |
| `tokenizer_mode` | §2.6 的生效档位，取值 ⊆ `{trigram, unicode61_bigram, like_scan}` | 建库时探测一次 |
| `built_at_unix` | 构建完成时间戳（仅用于 `eg index status` 展示） | 同上 |
| `card_count` | 构建时卡片数（自检用，与 `SELECT count(*) FROM cards` 必须相等） | 同上 |

- `card_count` 与 `SELECT count(*) FROM cards` **不相等**即判 `W24 index_corrupt`
  （这就是 `M-005` 判据 5 中「水位线自相矛盾」这一类损坏的具体形态）。
- **禁止**新增第 7 个键；新增键属 M6 或后续里程碑的独立变更。

### 4.3 `IndexSchemaVersion`：不兼容 = 重建，永不迁移

- 常量落在 `internal/index/schema.go`，形如 `const IndexSchemaVersion = 1`，M5 首版取 **`1`**。
- **语义（唯一）**：`index_meta.schema_version != IndexSchemaVersion` ⇒ 索引判为
  **不可用**（归入 `W24 index_corrupt` 家族的 `schema_version_mismatch` 子因），
  处置 = **整库重建**（删除 `.index/` 后 full build）。
- **永不写迁移脚本**：这是「`.index/` 恒为可重建派生」的直接推论——迁移逻辑会引入
  「旧库半迁移」这一不可验证态，而重建的代价是 O(全量 build)，已有性能门槛兜底（§7）。
- 机器反证：`TestSchemaVersionConstant` / `TestCorruptSchemaVersionMismatch` /
  `TestRebuildEqualsFreshBuild`（M-005 判据 4 / 5）。

### 4.4 构建的确定性（`T-…-065` 的核心口径）

- 同一 vault、同一 HEAD、连续两次 full build 必须产出**逐字节相同**的逻辑内容
  （`TestBuildTwiceByteIdentical`）。因此：
  - 一切插入必须按**确定序**（`cards` 按 `id` 升序、`relations` 按 `(src_id, verb, dst_id)` 升序、
    `files` 按 `path` 升序）；
  - `built_at_unix` 这类**天然非确定**的列**不参与**等价比较，等价比较口径 =
    「除 `built_at_unix` 外的全部行按确定序导出后逐字相等」；
  - `rebuild` 的结果必须与 fresh build 在同一口径下等价（`TestRebuildEqualsFreshBuild`）。
- 空 vault 必须能成功建库（`TestBuildEmptyVaultOK`），产出 0 卡索引而**不是**报错。

---

## 5. 一致性与陈旧判定（A-44）

### 5.1 A-44 裁决：Git HEAD + 每文件 `content_hash`，不以 mtime 为唯一依据

**采用选项 ①**：水位线 = `(head, files_hash)`，其中 `files_hash` 由**每文件 `content_hash`**
按 `path` 升序聚合而来；`files.mtime_unix` / `files.size` **只作为快路径过滤**，
**绝不**单独作为「未变更」的最终结论。

**排除理由（必须写进合同，防止后续被「优化」掉）**：

| 排除项 | 反例 |
|-|-|
| 选项 ② `HEAD + mtime + size` | `git checkout` 会把内容换掉而 `size` 可能相同、`mtime` 也可能被工具还原；**同秒内两次写**在 1s 精度下不可分辨 ⇒ 静默漏更新 |
| 选项 ③ 只用 `HEAD` | 外部编辑器改了文件但**未提交**时 HEAD 不变 ⇒ 索引永远认为自己是干净的，而 evergreen 的主要工作态恰恰是「工作区有未提交改动」 |

- **快路径**（允许，且是性能手段）：若 `(path, size, mtime_unix)` 三者与 `files` 表完全一致，
  **可跳过重算 hash**；但只要三者任一不一致，就必须重算 `content_hash` 再判定。
  该快路径**不改变**最终判定语义（等价性由 `TestIncrementalEqualsFullRebuild` 反证）。
- **强校验路径**（`eg index status --strict` 与 `T-…-066` 的等价性测试用）：**忽略**快路径，
  对全部文件重算 `content_hash` 后与 `files` 表比对。`--strict` 是**读命令**，不改索引。
- `content_hash` 的算法与 M1 `internal/store` 写口的 B3 `content_hash` **同源同算法**，
  索引层**不得**自定义第二套 hash（否则会复制 I-002 「计数双源」型缺陷）。

### 5.2 陈旧的最小单位与三态判定

判定在**每文件**粒度进行，聚合成索引级三态：

| 索引级状态 | 判定 | 诊断码 | 读路径行为 |
|-|-|-|-|
| `healthy` | `.index/eg.db` 可打开、`schema_version` 匹配、`integrity_check=ok`、`card_count` 自洽、`head` 与 `files_hash` 均与现态一致 | 无 | 用索引后端 |
| `stale` | 库可用且自洽，但 `head` 或 `files_hash` 与现态不一致 | `W22 index_stale` | **仍然降级为全量扫描**（结果正确优先），并提示 `eg index sync` |
| `unusable` | 打不开 / 缺失 / `schema_version` 不匹配 / `integrity_check` 非 `ok` / `card_count` 不自洽 / `.index/` 有非法文件 | `W23 index_missing`（缺失）或 `W24 index_corrupt`（其余） | 降级为全量扫描，并提示 `eg index rebuild` |

> **关键裁决（不许折衷）**：`stale` **不允许**「用陈旧索引先返回、再提示」。
> 陈旧索引可能返回**已删除的卡**或**漏掉新卡**，这与 P-3（Markdown 胜）冲突。
> 因此 `stale` 与 `unusable` 在**读结果**上行为一致（都走扫描），只在**诊断码**与**修复建议**上不同。
> 这条也让 `M-005` 判据 8 的 `diff` 断言天然成立。

### 5.3 `eg index sync` 的收敛语义

- `sync` = 「以现态为准，把索引推到 `healthy`」：对 `files` 表做三向 diff
  （新增 / 修改 / 删除），只重建受影响文件对应的 `cards` / `cards_fts` / `relations` /
  `skipped` 行，然后在**同一事务**内更新 `index_meta` 的 `head` / `files_hash` / `card_count`。
- **幂等**：连续两次 `sync` 第二次必须是 no-op（`TestIncrementalIdempotent`）。
- **等价**：`sync` 后的索引与 fresh full build 在 §4.4 口径下等价
  （`TestIncrementalEqualsFullRebuild`）。
- **删除**：源文件删除后，对应 `cards` / `relations` / `cards_fts` 行必须消失
  （`TestIncrementalAfterDeleteRemovesRows`）。
- `sync` 遇到 `unusable` 索引时**自动退化为 full build**（对用户等价于 `rebuild`），
  并在输出里如实说明该退化，**不得**静默。
- `sync` / `build` / `rebuild` **绝不**写权威 Markdown（`TestRebuildNeverTouchesMarkdown`）。

---

## 6. 降级语义与诊断码分配（A-45）

### 6.1 总纲（逐字条款）

**索引不可用时必须降级为全量扫描而非报错退出。**

- 「不可用」= §5.2 的 `stale` ∪ `unusable`，含：`.index/` 整体缺失、`eg.db` 缺失、
  文件截断、`integrity_check` 失败、`schema_version` 不匹配、水位线自相矛盾、
  `.index/` 混入非法文件、打开时权限错误、驱动初始化失败。
- **读命令**（`eg search` / `eg card show` / `eg rel`）在上述任何情形下**退出码必须为 `0`**
  （若查询本身合法），**不得**返回 `1` / `2` / `3` / `4`。
- **M5 不扩张退出码全集**：`{0,1,2,3,4,6}` 一字不变；`5` 全程**不启用**（属 M6），
  `6` 白名单恰 `{proposal approve, delete}` 不变。
  机器反证：`grep -rn 'os.Exit(5)' internal/ | wc -l` 为 `0`。

### 6.2 A-45 裁决：采用选项 ①（查询域新增 `Q5`）+ 同时落 `W22`–`W25`

**采用选项 ①**，并明确其与 `W2x` 的分工（这不是二选一，而是两个不同用途的码域）：

- `Q5`（**查询域**）：出现在**读命令**的 `--json` 里，机器可读地表达「本次结果由降级路径产出」。
- `W22` / `W23` / `W24` / `W25`（**E/W/I 域**）：表达**索引本身的健康状态**与**结果截断**，
  由 `eg index status` / `eg index build|sync` 以及读命令的诊断区共同产出。
- **排除选项 ③**（只走人类可读输出）：agent 消费方无法据此判断结果可信度，
  会导致「降级结果被当成权威结果」。

**授权留痕（A-45 的 owner 授权项）**：本轮 owner 授权把 M2 查询合同
`2026-09-19-m2-query-contract.md` §5.1 的 `Q` 码计数由「**恰四条**」改为
「**恰五条（S4 起）**」，**只追加成员 `Q5`，`Q1` ~ `Q4` 的语义、字面量与顺序一字不改**
（与 A-39 同型的只追加变更）。

- 该计数改写的**落点唯一**：`T-…-069` 在 M5 收口时同步 M2 查询合同的计数行与对应门禁断言，
  **`T-…-065` ~ `068` 不得**擅自改 M2 合同文本。
- **明确边界**：此项**不修改 M1 ~ M4 的历史 Task、历史 Activity Log 与历史验收结论**；
  它是「M2 合同的向后兼容追加」，不是对 M2 验收结论的推翻。若 `069` 发现历史门禁因
  「恰四条」文本锚点而红，处置只允许「同步锚点到恰五条」，**不允许**删断言。

### 6.3 诊断码分配表（M5 新增恰 5 条，封闭）

| 码 | 名 | 域 | 触发条件 | 产出位置 | 退出码影响 |
|-|-|-|-|-|-|
| `W22` | `index_stale` | E/W/I | 索引可用但 `head` 或 `files_hash` 与现态不一致 | 读命令诊断区 + `eg index status` | 无（读命令仍 `0`） |
| `W23` | `index_missing` | E/W/I | `.index/` 或 `.index/eg.db` 不存在 | 同上 | 无 |
| `W24` | `index_corrupt` | E/W/I | 打不开 / `integrity_check` 非 `ok` / `schema_version` 不匹配 / `card_count` 不自洽 / `.index/` 含非法文件 / 文件截断 | 同上 | 无 |
| `W25` | `result_truncated` | E/W/I | 结果因 `--limit` 被截断（`total > offset + limit`） | 读命令诊断区 | 无 |
| `Q5` | `index_degraded` | 查询域（`Q`） | 本次读结果由**降级路径**（扫描 / D2 / D3）产出 | 读命令 `--json` 的查询诊断区 | 无 |

**成对关系（`T-…-067` 硬消费，`M-005` 判据 8 有 `diff` 级断言）**：

- `rm -rf $VAULT/.index` 后执行三条读命令，每条**各多出恰一条 `W23` + 恰一条 `Q5`**；
- 索引 `healthy` 时**必须不产出** `Q5`（`TestQ5NotEmittedOnHealthyIndex`）；
- `W22` / `W23` / `W24` **三者互斥**，一次读命令最多出现其中一条；
- `Q5` 与 `W22|W23|W24` **同现**（有降级就必有一条原因码）；
- `W25` 与降级**正交**：截断与索引健康无关，索引在位时也可能截断。

### 6.4 明确不动的既有计数（防止索引诊断污染既有封闭集合）

| 既有封闭集合 | M5 处置 |
|-|-|
| `eg check` 的**十二值**枚举 | **一字不动**，索引诊断**不进** `check` 枚举 |
| `eg rel --json` 的 `data` 恰**五键** | 不扩张（诊断码走诊断区，不进 `data`） |
| `eg card show --json` 的 `data` 键集合 | 不扩张 |
| `skipped[].kind` 恰 **2** 值 | 不扩张 |
| 报告 `reconcile` 字段恰**三键** | 不扩张 |
| `model.KnownVerbs` = **8** / `plan.AllOpNames` = **17** | 恒定不变 |
| **`W21` 继续不分配** | 沿用 M4 冻结结论，**不是新裁决**；`grep -rn '"W21"' internal/ \| wc -l` 恒为 `0` |

---

## 7. 性能指标与门槛口径（A-46）

### 7.1 A-46 裁决：口径由 `064` 定，数值由 `068` 实测回填

**采用选项 ①**：本合同**只冻结采样口径**，五条门槛的**数值列**留
「**实测回填（由 T-…-068 填）**」；`T-…-068` 在 10,000 卡语料上实测后，
按 `门槛 = 实测 × 1.5，向上取整到 10ms` 的公式回填，并把回填后的数值同步到
`M-005` 判据 14 的判定命令。

- **排除选项 ②**（`064` 直接拍板数值）：这正是风险 **R-24**。本轮沙箱内的参考观测（§7.5）
  是在 1,000 行的玩具语料上取得的，与 10,000 卡语料不可外推，拍板必然要么过松（CI 拦不住回归）
  要么过紧（CI 长期红）。
- **排除选项 ③**（只报告不设门槛）：CI 无法据此阻断，等于没有性能验收。

### 7.2 五条指标（键名封闭，恰 5 键）

`eg bench --json` 的 `data` 必须**恰含**以下 5 个键，一个不多一个不少
（`TestBenchFiveMetricKeys` / `TestBenchJSONEnvelopeFiveKeys`）：

| # | 键 | 含义 | 是否含索引构建耗时 |
|-|-|-|-|
| 1 | `search_p95_ms` | `eg search <kw>` 端到端 P95 | **否** |
| 2 | `card_show_p95_ms` | `eg card show <id>` 端到端 P95 | **否** |
| 3 | `rel_p95_ms` | `eg rel <id>` 端到端 P95 | **否** |
| 4 | `index_build_ms` | 全量 build 的**单次**墙钟耗时（非 P95） | **是**（它本身就是构建耗时） |
| 5 | `index_incremental_ms` | 改动 **1** 个文件后 `eg index sync` 的**单次**墙钟耗时 | **是** |

### 7.3 采样口径（冻结，`T-…-068` 不得自行改动）

| 项 | 口径 |
|-|-|
| **语料规模** | `10,000` 卡 + `30,000` 关系，由 `test/perf/corpus_gen.go -cards 10000 -rels 30000` 确定性生成（**固定随机种子**，同参数两次生成逐字节相同） |
| **索引状态** | 前三条指标在 `healthy` 索引上采样（**不含**降级路径；降级路径不设门槛，只报告） |
| **预热轮数** | 每个指标先跑 **3 轮丢弃**，再采 **50 轮**计入统计（`index_build_ms` / `index_incremental_ms` 例外：**不预热**，各测 **3 次取中位数**，因为构建有强 I/O 缓存效应，预热会失真） |
| **P95 定义** | 50 个样本升序排列，取**第 `ceil(0.95 × 50) = 48` 个**样本值（1-based），即第 48 小值。**不做插值**，避免不同实现算出不同 P95 |
| **计时边界** | 端到端**进程墙钟**（含进程启动、参数解析、打开索引、查询、序列化输出），因为用户与 agent 感知的就是进程时间 |
| **查询集** | `search` 用固定 **20** 个关键词（含 ASCII / 中文 3 字 / 中文 2 字各若干，覆盖 §2.6 的 D0 与 D0b 两条路），循环取用；`card show` / `rel` 用固定 **20** 个 id，循环取用。查询集写死在 `test/perf/`，**不得**随机 |
| **输出单位** | 毫秒，整数（向上取整） |
| **门槛公式** | `门槛 = ceil(实测值 × 1.5 / 10) × 10`（先乘 1.5 再向上取整到 10ms 的整数倍） |
| **回归判定** | `eg bench --json` 的五个键**逐个 ≤ 门槛**；超出即判 P0 回归（`M-005` 判据 14） |
| **环境登记** | `T-…-068` 必须在 M5 验收报告里登记采样机器的 CPU / 内存 / 磁盘类型与 Go 版本；**门槛与环境绑定**，换环境需重新回填而非放宽 |

### 7.4 排序必须可复算，不得依赖 FTS5 `rank`

- `eg search` 的结果序**不得**直接用 FTS5 的 `rank`/`bm25()`：`rank` 在 D0 / D0b / D1 / D2 / D3
  各档下不可比，且随 FTS5 版本变化 ⇒ 会打破「索引与扫描结果等价」。
- 排序键必须是**确定的、可在内存中复算的**四级键（具体键序由 `T-…-068` 按 `M-005` 判据 11
  的 `sortKeyOrder` 落地，且 `TestRelationSortIndexEqualsScan` 反证索引与扫描同序）。
- FTS5 `MATCH` 只负责**候选集召回**，打分与排序在 Go 侧完成。
  这条同时保证 `T-…-067` 的「索引与扫描 `.data` 逐字相等」可达成。

### 7.5 本轮参考观测（**不是门槛**，仅作数量级参考）

在 `/tmp/t064_probe` 的 1,000 行玩具语料上实测（`/tmp/t064_probe_fts5.log` P7 段）：

- 1,000 行**事务内**写入 = `22.072185ms`；
- 100 次 `MATCH` = `2.943862ms`（单次均值 `29.438µs`）。

> **明确声明**：以上为沙箱**单次观测**，语料规模、查询集、预热轮数均与 §7.3 口径不符，
> **不得**被引用为 A-46 的门槛数值，也**不得**据此推断 10,000 卡语料的耗时。
> 门槛数值一律由 `T-…-068` 按 §7.3 实测回填。

### 7.6 实测回填（`T-…-068` 登记，实测 / 门槛双列）

**登记日期**：`2026-09-08`。**语料**：`test/perf/corpus_gen.go -cards 10000 -rels 30000 -seed 0x5EED_2026_1201`（确定性、两遍逐字节一致）。
**采样**：`eg bench --json`，严格按 §7.3 冻结口径（预热 3 轮丢弃 + 采 50 轮、P95 = 第 48 小值不插值、`index_build_ms`/`index_incremental_ms` 各测 3 次取中位数不预热、端到端进程墙钟）。
**门槛公式**：`门槛 = ceil(实测值 × 1.5 / 10) × 10`（§7.3）。

| # | 键 | 实测值（ms） | 门槛值（ms） | 计算 |
|-|-|-|-|-|
| 1 | `search_p95_ms`        | `3198` | `4800`  | `ceil(3198 × 1.5 / 10) × 10` |
| 2 | `card_show_p95_ms`     | `2408` | `3620`  | `ceil(2408 × 1.5 / 10) × 10` |
| 3 | `rel_p95_ms`           | `2428` | `3650`  | `ceil(2428 × 1.5 / 10) × 10` |
| 4 | `index_build_ms`       | `6035` | `9060`  | `ceil(6035 × 1.5 / 10) × 10` |
| 5 | `index_incremental_ms` | `9958` | `14940` | `ceil(9958 × 1.5 / 10) × 10` |

- **同真约束**：以上门槛列与 `test/e2e/m5_bench_p95.sh` 的 `THRESH_*` 常量、与 `M-005` 判据 14 的判定命令**逐字一致**。任何一处修改必须三处同步。
- **复跑佐证**（同一环境、同口径二次运行，仅作稳定性佐证，**不改**上表登记基准）：`search=3254`、`card_show=2450`、`rel=2469`、`index_build=6076`、`index_incremental=9854`，五项均 **< 门槛**，波动幅度均在 ±2% 内，门槛留有 ~1.5× 余量。
- **环境登记（门槛与环境绑定，换环境须重新回填而非放宽）**：CPU `Intel(R) Xeon(R) Platinum 8260 @ 2.40GHz`（96 vCPU）；内存 `16 GiB`；磁盘 容器 overlay（SSD 后端）；Go `go1.24.13 linux/amd64`；`CGO_ENABLED=0` 静态构建。

### 7.7 T-068 放行前纠正登记（owner 2026-09-08 判定「暂不放行」后追加）

owner 对 `T-…-068` 阶段 B 提交 `e66f3a8` 提出两项核心 P1，均以**追加修正提交**处置，
**不重写** `e66f3a8`（Teamwork 活动日志已引用该 hash，重写会使留痕失真）。

| # | 问题 | 处置 | 纠正 commit |
|-|-|-|-|
| 1 | `e66f3a8` 误纳入 `skill/SKILL.md`，越出 `T-…-068` Scope（Scope 明确「不改版本 / dist / 文档 / 门禁脚本」，文档收口归 `069`），且阶段 B 日志自身也称 SKILL 收口归 `069` | 把 `skill/SKILL.md` **精确恢复到 `e66f3a8^` 的字节**，不触碰其他文件。反证：`git diff e66f3a8^ HEAD -- skill/SKILL.md` → **0 行** | `8cd68bf` |
| 2 | `internal/index.RemoveScratch(dir)` 对外暴露了可对**任意调用方路径** `os.RemoveAll` 的 API，且 `benchBuildMetrics` 实际传入的是**含 `domains` 的临时 vault**，使 `U-02` 正面反证「`internal/index` 的 `dir` 只可能是 `.index/`」退化为注释承诺 | 删除该宽 API，改为**窄 API** `index.NewScratch(prefix)`：目录由本包 `os.MkdirTemp` 自建，返回 `dir` + 绑定该目录的 `cleanup` closure，**调用方无从传入待删除路径**（`prefix` 含路径分隔符时 `os.MkdirTemp` 直接失败）。`benchBuildMetrics` 改用该 API | `b781641` |

**由此产生的持久约束（`U-02` 的机检化，M6 及以后一并遵守）**：

- `internal/index` **不得**新增「接受调用方路径并对其递归删除」的导出 API。
  允许接受路径形参且可触达递归删除的导出 API 集合**封闭为 `{Build, Rebuild}`**（M1–M4 历史既有，`dir` 语义即 `.index/`）。
- 一次性目录**只能**经 `NewScratch(prefix)` 获得，清理**只能**经其返回的 closure。
- 上述两条由 `internal/index/scratch_test.go` 的 6 条测试反证（含 AST 层面的允许集合封闭断言、
  `NewScratch` 形参不流向递归删除、`prefix` 非路径、cleanup 可清非空多层树且只清自身目录）。
- **未修改** M1–M4 任何历史门禁以放宽白名单；纠正方向是**收紧 API**，不是放宽断言。

**纠正后门禁复跑**（详见 `audit/T068_HANDOFF.json`）：`make lint` 绿；`CGO_ENABLED=0 go build ./...` 绿；
`go test ./internal/index ./internal/rules -count=1` 绿；`m5_sort_page.sh` 10/10、`m5_replaced_by_reverse.sh` 9/9 绿；
性能 e2e 引用已完成的 6/6 PASS 记录（owner 明示不必重复 10 分钟实跑）。
`go test ./internal/cli` 的**稳定**红项**仅** `TestSkillCommandsExecutable`，成因是第 1 项纠正按 owner 要求恢复 SKILL.md 后其未提及 `bench`
——**这是纠正的机械后果，不是回归**，登记为 `T069-DEBT-2`（见 `M-005` 延期债表），由 `069` 文档收口时清偿。
另观测到 `TestIndexStatusStrictIgnoresQuickPath` **低频**转红（16 次整包运行中 1 次，无法定向复现），
属 mtime 快路径时序敏感的既有不稳定，与本轮纠正触达面（一次性目录 API + `bench.go`）无关，
登记为 `T069-FLAKE-1` 交 `069` 定量复现，**不以删断言方式清偿**。

---

## 8. 命令面与分页参数（A-47 / A-48）

### 8.1 A-48 裁决：`eg index` 一个命令 + 四子命令，另加 `eg bench`，命令数 20 → 22

**采用选项 ①**：`eg index` 一个顶层命令，带 `build` / `rebuild` / `status` / `sync`
四个子命令（与既有 `eg config get|set` 同型）；另新增 `eg bench` 一个顶层命令。

| 子命令 | 语义 | 写 `.index/` | 写权威 Markdown | 退出码 |
|-|-|-|-|-|
| `eg index build` | 索引不存在则全量构建；已存在且 `healthy` 则 no-op | 是 | **否** | `0` / `1`（构建失败）/ `4`（参数错） |
| `eg index rebuild` | **先删** `.index/` 再全量构建（无损重建，结果与 fresh build 等价） | 是 | **否** | 同上 |
| `eg index status` | 只读报告：`healthy` / `stale` / `unusable` + 诊断码 + `index_meta` 摘要；支持 `--strict`（忽略 mtime 快路径，全量重算 hash） | **否** | **否** | `0`（含 `stale`/`unusable`，因为状态查询本身成功） |
| `eg index sync` | 增量收敛到 `healthy`；索引 `unusable` 时退化为 full build 并如实说明 | 是 | **否** | `0` / `1` / `4` |
| `eg bench` | 在当前 vault 上按 §7.3 口径采样，输出五键 | **否**（只读采样；若需构建耗时，在**临时副本**上做，不污染当前 `.index/`） | **否** | `0` / `1` / `4` |

- **排除选项 ②**（四个顶层命令）：命令数 20 → 25，CLI 面扩张更大，与 `eg config` 的既有惯例不一致。
- **排除选项 ③**（不提供 `sync`）：陈旧只能整体 rebuild，10,000 卡语料下每次外部编辑都要付
  全量重建代价，`index_incremental_ms` 这条指标将无从谈起。

**命令数复算（`T-…-069` 逐项核对）**：`20`（M4 收口值）`+ 1`（`eg index`）`+ 1`（`eg bench`）
`= 22`。机器反证：`[ "$(grep -c 'wantCommandCount = 22' internal/cli/cli_test.go)" -eq 1 ]`。
`eg index` 的四个子命令**按既有口径不单独计数**（与 `eg config get|set` 只计 1 同型）。

### 8.2 A-47 裁决：`--limit` / `--offset`，`--limit 0` == 不限量

**采用选项 ①**。参数集合**封闭**（`TestPageParamsClosedSet`；`TestPageParamErrorExitsUsage`；`e2e.cli-core.c2_readonly_exit_closed_set`）：

| 参数 | 类型 | 默认值 | 语义 |
|-|-|-|-|
| `--limit` | 非负整数 | `50` | 最多返回的条数；**`--limit 0` 表示不限量**（返回全部） |
| `--offset` | 非负整数 | `0` | 跳过的条数 |

- **排除选项 ②**（`--page/--page-size`）：与既有 CLI 无同型先例，且页码从 1 起容易与 `offset` 语义混淆。
- **排除选项 ③**（两套都提供）：扩张 CLI 面，**不可取**。
- **边界语义（封闭，`T-…-068` 硬消费）**：

| 情形 | 行为 |
|-|-|
| `--limit 0` | 不限量，且**永不**产出 `W25` |
| `--offset` 超出结果总数 | 返回**空结果 + 退出码 `0`**，**不是**错误（`TestOffsetBeyondEndEmptyNotError`） |
| `--limit` / `--offset` 为负数或非整数 | 参数非法，退出码 **`1`**、`status="failed"`、零写入零 commit（**经 `I-…-008` 修订**：原写 `4`，与 M2 查询合同 §1.6 / §6「只读命令退出码只可能是 0 / 1」以及 `search` / `card` / `rel` 三条 `--help` 的逐字声明冲突；`4` 仍专表「Git 提交失败」，只读路径不得挪用） |
| `total > offset + limit`（发生截断） | 产出恰一条 `W25 result_truncated`（`TestTruncationEmitsW25`） |
| 翻页 | 固定排序键下逐页拼接必须**无重无漏**且等于一次性全量结果（`TestPaginationNoDupNoGap`） |

- 分页作用于**三条读命令**（`search` / `card show` 的关系列表 / `rel`），
  分页**在排序之后**施加，因此必须先有 §7.4 的确定序。
- 分页**不改变** `data` 键集合（`M-005` 判据 10）。

### 8.3 `--json` 信封不扩张

- 分页与降级信息一律进**诊断区**（`W22`–`W25` / `Q5`）与既有信封字段，
  **不新增** `data` 顶层键。`eg rel --json` 的 `data` 仍恰五键
  `{id, relations_out, relations_in, scanned_files, skipped_files}`。
- `eg bench --json` 是**新命令**，其 `data` 恰 5 键（§7.2），与既有读命令信封无关。

### 8.4 `replaced_by` 反向查询（`T-…-068` 的形态口径）

- 数据来源：`cards.replaced_by` 列 + 该列上的普通索引；**不新增表**。
- 正反双向一致：若 `A.replaced_by = B`，则 `eg rel A` 的正向与 `eg rel B` 的反向必须互相可见
  （`TestReplacedByReverseLookup` / `TestReplacedByChainBothDirections`）。
- 与可见性**正交**：反向查询的可见性判定必须复用既有的逻辑删除 / `deprecated` 策略，
  **不得**自定义第二套可见性规则（`TestReplacedByReverseOrthogonalToDeleted` /
  `TestReplacedByReverseRespectsDeprecatedPolicy`）。

---

## 9. 静态构建保全条款

### 9.1 逐字总纲

**CGO_ENABLED=0 静态单二进制不得被破坏。**

### 9.2 实证记录（`/tmp/t064_static_crosscompile.log`）

| # | 验证 | 结果 |
|-|-|-|
| S1 | `CGO_ENABLED=0 go build`（含 `modernc.org/sqlite v1.45.0`） | `exit=0`，构建成功 |
| S2 | `file` / `ldd` | `ELF 64-bit LSB executable, x86-64, statically linked`；`ldd` → `not a dynamic executable` |
| S3 | `CGO_ENABLED=0` 交叉编译 `linux/amd64` | `OK`，`statically linked` |
| S3 | `CGO_ENABLED=0` 交叉编译 `linux/arm64` | `OK`，`ELF 64-bit LSB executable, ARM aarch64, statically linked` |
| S3 | `CGO_ENABLED=0` 交叉编译 `darwin/amd64` | `OK`，`Mach-O 64-bit x86_64 executable` |
| S3 | `CGO_ENABLED=0` 交叉编译 `darwin/arm64` | `OK`，`Mach-O 64-bit arm64 executable` |
| S3 | `CGO_ENABLED=0` 交叉编译 `windows/amd64` | `OK`，`PE32+ executable (console) x86-64, for MS Windows` |
| S4 | `CGO_ENABLED=1` 对照 | 产出 `ELF 64-bit LSB executable, x86-64, dynamically linked` ⇒ **反证：必须显式钉住 `CGO_ENABLED=0`** |
| S5 | `go list -m all \| grep -c 'mattn/go-sqlite3'` | `0`（cgo SQLite 不在依赖树） |

**结论**：`docs/specs/2026-08-31-evergreen-s1-tech-design.md` §16.6 的 cgo 预警
（「SQLite 若走 cgo 会破坏静态单二进制与交叉编译」）**成立且已被规避**——
选 `modernc.org/sqlite`（纯 Go）即可在 `CGO_ENABLED=0` 下静态构建，
并覆盖 **Linux / macOS / Windows** 三平台（`linux/amd64`、`linux/arm64`、
`darwin/amd64`、`darwin/arm64`、`windows/amd64` 五个 target 全部实测通过）。

### 9.3 平台矩阵与 release 口径（明确一处**不改**）

- `Makefile` 现有 `PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64`（**四平台**），
  dist 已用 `CGO_ENABLED=0 GOOS=… GOARCH=… go build`。
- **裁决**：M5 **不扩张** release 平台矩阵，`PLATFORMS` 保持**四平台**一字不改
  （`M-005` 判据 16 的「dist 四平台」断言因此仍成立）。
- **Windows 的定位**：`windows/amd64` 属**可编译性约束**（本合同实测已满足），
  **不进** release 产物矩阵。扩 release 平台属独立变更，需单独里程碑授权。

### 9.4 硬断言形态（`T-…-065` 落测、`T-…-069` 收口复算）

以下断言必须**逐条**在 M5 落地，`T-…-069` 的判据 16 已含前两条：

```sh
# 1) cgo SQLite 永久禁入
[ "$(go list -m all | grep -c 'mattn/go-sqlite3')" -eq 0 ]

# 2) 纯 Go 静态构建可行
CGO_ENABLED=0 go build ./...

# 3) 驱动版本钉死（防 @latest 打断 Go 1.24 工具链）
grep -qF 'modernc.org/sqlite v1.45.0' go.mod

# 4) 交叉编译三平台可编译（Windows 只验编译，不进 release）
for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  CGO_ENABLED=0 GOOS="${t%/*}" GOARCH="${t#*/}" go build -o /dev/null ./cmd/eg || exit 1
done

# 5) release 平台矩阵未被偷偷扩张
grep -qF 'PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64' Makefile
```

- 第 4 条的落点：`T-…-069` 的 `m5_docs_commands.sh` 或独立的静态构建保全 e2e，
  由 `069` 在 11 个 `m5_*.sh` 的既有配额内安排，**不额外扩张脚本数**。

---

## 10. A-41 ~ A-48 裁决登记表（恰 8 行）

> 每行六列：冲突摘要 / 封闭选项 / **裁决结论** / 改动面 / 机器反证形态 / 受影响 task。
> 裁决来源统一为 2026-09-08 owner 实施指令（见文首「裁决授权留痕」），**无一行为本 task 自拍**。

| 编号 | 冲突摘要 | 封闭选项 | 裁决结论 | 改动面 | 机器反证形态 | 受影响 task |
|-|-|-|-|-|-|-|
| **A-41** | SQLite 驱动选型未定，FTS5 支持「未知」（`U-7`） | ① `modernc.org/sqlite`（纯 Go）；② `mattn/go-sqlite3`（cgo）；③ 纯 Go 自研倒排 | **选 ①**，版本钉 `modernc.org/sqlite v1.45.0`（`@latest`=v1.58.0 要求 Go ≥1.25，当前 1.24.13 不可用）；② 永久禁入；③ 仅留作 R-21 第三档 | `go.mod` / `go.sum` 新增 1 直接依赖 + 10 间接依赖；`internal/index` 新包 | `[ "$(go list -m all \| grep -c 'mattn/go-sqlite3')" -eq 0 ]`；`grep -qF 'modernc.org/sqlite v1.45.0' go.mod`；`PRAGMA compile_options` 含 `ENABLE_FTS5` | 064 / 065 / 066 / 067 / 068 |
| **A-42** | FTS5 中文分词形态：`unicode61` 对 CJK 召回塌陷 | ① `unicode61` + 写入侧 bigram 预切；② `trigram`（须实测）；③ 退 `LIKE` + 内存打分 | **选 ② 为主路（D0）+ ① 下沉为同表补路（D0b，恒开）**，`trigram` 实测可用但 <3 字符 `hits=0`，故必须用 bigram 列覆盖 1~2 字中文；③ 降为 D2 档 | `cards_fts` 增 `bigram_text` 列；`index_meta.tokenizer_mode` 键；写入侧预切函数 | `TestFTS5VirtualTableCreated`；`tokenizer_mode ∈ {trigram, unicode61_bigram, like_scan}`；同一查询在各档 `.data` 命中集合一致 | 064 / 065 / 067 |
| **A-43** | `.index/` 文件布局与是否入库未定 | ① `.index/eg.db` 单文件 + 整目录 gitignore；② `eg.db` + `meta.json` 水位线分离；③ 索引入库 | **选 ①**：水位线入库内表 `index_meta`（与索引写入同事务），`.gitignore` **整目录**忽略；② 会引入两文件不同步的新矛盾态；③ 与 F6 冲突，禁用 | vault `.gitignore`（沿用 M1 现状，不改）；`.index/` 白名单 `{eg.db, eg.db-wal, eg.db-shm}` | `git check-ignore -q "$VAULT/.index/eg.db"` 退 `0`；`eg index build` 后 `git -C "$VAULT" status --porcelain` 为空；`git ls-files -- .index \| wc -l` 为 `0` | 064 / 065 / 066 |
| **A-44** | 索引陈旧判定粒度：mtime 会被 `git checkout` / 同秒写入骗过 | ① Git HEAD + 每文件 `content_hash`；② HEAD + mtime + size；③ 只用 HEAD | **选 ①**：水位线 `(head, files_hash)`，`files_hash` = 全部 `content_hash` 按 `path` 升序聚合的 SHA-256；`mtime`/`size` **仅作快路径过滤**，不作最终结论；`content_hash` 与 M1 store 的 B3 同源同算法 | `files` 表；`index_meta.{head, files_hash}`；`eg index status --strict` | `TestWatermarkFromHeadAndHashes`；`TestStaleDetectedOnExternalEdit`；`TestIncrementalEqualsFullRebuild`；`TestSyncConvergesToClean` | 064 / 066 / 067 |
| **A-45** | 降级是否产出机器可读诊断码及归属码域 | ① 查询域新增 `Q5`（`Q` 由恰四条→恰五条）；② E/W/I 域新增 `W2x`；③ 只走人类可读 | **选 ① 且同时落 ②**（两者用途不同，非二选一）：`Q5 index_degraded` 标注「本次结果来自降级路径」；`W22/W23/W24` 标注索引健康、`W25` 标注截断；③ 排除（agent 无法判断结果可信度）。owner 已授权 M2 查询合同 §5.1 计数「恰四条」→「恰五条（S4 起）」，**只追加 `Q5`，`Q1`~`Q4` 一字不改** | `internal/query/diagnostic.go`；M2 查询合同计数行与其门禁锚点（**落点唯一 = `069`**） | `rm -rf .index` 后三读命令各多出恰一条 `W23` + 恰一条 `Q5`；`TestQ5EmittedOnDegrade` / `TestQ5NotEmittedOnHealthyIndex`；`grep -rn 'os.Exit(5)' internal/ \| wc -l` 为 `0`；`grep -rn '"W21"' internal/ \| wc -l` 为 `0` | 064 / 067 / 069 |
| **A-46** | 五条性能门槛的数值与采样口径未定 | ① 口径由 064 定、数值由 068 实测回填（`实测 × 1.5` 向上取整到 10ms）；② 064 直接拍板数值；③ 只报告不设门槛 | **选 ①**：§7.3 冻结语料 10,000 卡/30,000 关系、预热 3 轮丢弃 + 采 50 轮、P95 = 第 48 小值不插值、端到端进程墙钟、固定 20 查询集、`index_build_ms`/`index_incremental_ms` 各测 3 次取中位数不预热；**数值列 = 实测回填（由 T-…-068 填）**；② 即风险 R-24，排除；③ CI 无法阻断，排除 | `test/perf/corpus_gen.go`；`eg bench`；`M-005` 判据 14 的数值 | `eg bench --json` 的 `data` 恰 5 键且逐个 ≤ 回填门槛；`TestBenchFiveMetricKeys`；`TestBenchZeroWriteToMarkdown` | 064 / 068 / 069 |
| **A-47** | 分页参数形态与 `0` 的语义未定 | ① `--limit/--offset`，`--limit 0` == 不限量；② `--page/--page-size`；③ 两套都提供 | **选 ①**：`--limit` 默认 `50`，`--limit 0` == 不限量且永不产 `W25`；`--offset` 默认 `0`，超界返回空 + 退 `0`；负数/非整退 **`1`**（`I-…-008` 修订，原写 `4`；判据取 M2 §1.6/§6 与三条 `--help` 的多数一致声明面）；分页在排序后施加，翻页无重无漏 | `internal/query/page.go`；三条读命令 flag 集合 | `TestPageParamsClosedSet`；`TestLimitZeroMeansNoLimit`；`TestOffsetBeyondEndEmptyNotError`；`TestPaginationNoDupNoGap`；`grep -c '"W25"' internal/query/page.go` ≥ 1 | 064 / 068 |
| **A-48** | `eg index` 一命令四子命令 还是 四顶层命令 | ① 一命令 + 四子命令（20→22）；② 四顶层命令（20→25）；③ 不提供 `sync` | **选 ①**：`eg index build\|rebuild\|status\|sync` + 新增 `eg bench`，命令数 **20 → 22**（子命令按 `eg config get\|set` 惯例不单独计数）；② CLI 面扩张更大且违惯例，排除；③ 无增量则 `index_incremental_ms` 无从谈起，排除 | `internal/cli` 新增 2 顶层命令；`cli_test.go` 的 `wantCommandCount` | `[ "$(grep -c 'wantCommandCount = 22' internal/cli/cli_test.go)" -eq 1 ]`；`eg index status` 在 `stale`/`unusable` 下仍退 `0` | 064 / 065 / 066 / 069 |

**登记表自查**：8 行，编号连续 `A-41` ~ `A-48`，**无一行为「未知 / 待 owner 确认」**，
故 `T-064` 不触发诚实出口，`T-…-065` / `066` / `067` / `068` 可依 `AGENTS.md` 硬依赖语义进入 `ready`，
`M-005` 完成判据 1 判为**达成**。

---

## 11. 下游硬消费索引（065 / 066 / 067 / 068 / 069 各自的开工输入）

| 下游 task | 必须从本合同取走的条款 | 不得自行改动的部分 |
|-|-|-|
| **T-…-065**（Schema / 全量构建 / 损坏检测 / 重建） | §2.2 版本钉死、§2.4 驱动、§2.6 档位与 `tokenizer_mode`、§3.2 布局、§3.3 PRAGMA、§4.1 六表封闭、§4.2 六键封闭、§4.3 重建而非迁移、§4.4 确定性、§9.4 断言 1/2/3 | 六表 / 六键集合；`IndexSchemaVersion` 语义；`skipped.kind` 恰 2 值 |
| **T-…-066**（增量 / 一致性） | §5.1 水位线与快路径、§5.2 三态、§5.3 `sync` 收敛与幂等、§6.3 `W22` | 陈旧判定不得退化为「只看 mtime」；`sync` 不得写 Markdown |
| **T-…-067**（读路径接入 + 降级） | §1.1 P-2/P-3、§5.2「`stale` 也走扫描」、§6.1 总纲、§6.3 成对关系、§6.4 不动的既有计数、§2.6 各档结果等价 | `data` 键集合；`check` 十二值；退出码全集；不得在读路径重探 tokenizer |
| **T-…-068**（排序 / 分页 / 反向 / bench） | §7.2 五键、§7.3 采样口径与门槛公式、§7.4 不用 FTS5 `rank`、§8.2 分页边界、§8.4 反向查询形态 | 采样口径（语料 / 预热 / P95 定义 / 查询集）；`--limit 0` 语义 |
| **T-…-069**（收口） | §6.2 M2 计数追加的**唯一落点**、§8.1 命令数 20→22 复算、§9.3 四平台不扩张、§9.4 五条断言、§7.3 环境登记要求 | 不得放宽任何门槛；不得删历史断言；不得改 M1~M4 历史结论 |

---

## 12. 边界、遗留与本合同不解决的事

### 12.1 登记型未决项的如实转录（**不在本 task DoD 内，未裁决**）

| 编号 | 状态 | 说明 |
|-|-|-|
| `A-49`（登记） | **沿用 A-40 选项 ③**（本轮采用） | `EPIC.md` frontmatter `target: '2026-10-09'` 早于 `M-005.date = 2027-01-17`；frontmatter 一字不改，M5/M6 排期只写正文并在 `M-005` 未决表登记 |
| `A-50`（登记） | **未知 / 待 owner 确认** | 权威侧 §16.1 的 S4 行未给需求 ID，§14 称 S4 恰 1 条需求但具体 ID 沙箱内不可得。**本合同不自造 ID**；M5 各 task 的「关联需求」暂沿用 `EG-VIEW-05`（取自 §14 真实 ID 全集），终态由 `T-…-069` 复述 |
| `A-51`（登记） | **本轮采用选项 ②** | ADR-08 / ADR-09 沙箱内不可得；本仓以**本合同**为 S4 的唯一施工依据，ADR-08/09 只作历史出处引用。若 owner 后续提供原文且与本合同冲突，按 `M-005` 未决表规则回写，**不得**静默改本合同 |

### 12.2 从 M4 继承的延期债（本合同**不处置**，只指明落点）

| 债项 | 内容 | 落点 |
|-|-|-|
| `J14` | `m2_acceptance.sh` / `m3_acceptance.sh` 历史 e2e 冻结事实退 `1` | `T-…-069`（`M-005` 判据 18，三值封闭枚举） |
| `J15` | `m3_final_gate.py` 三条（`H10k` / `H10x` / `H10ad`）历史 P1 失败 | 同上 |
| `I2` | 合同 §14 称新增 14、实测 13 | 同上 |
| `I-…-002` / `003` / `004` | 三条 M2 遗留 issue 仍 `open` | **M5 不接管、不关闭、不新建 task**；`T-…-067` 须先读 `I-002` 并在 Activity Log 记明索引后端选用的计数来源，**不得复制「计数双源」缺陷** |

### 12.3 明确划给 M6 的事（本合同**不定义**）

- `run.lock`、事务日志、多文件原子提交、崩溃恢复、块级安全合并、写前强校验、
  `eg check --strict`、**退出码 `5` 的启用**。
- M5 期间 `.index/` 的写入**只要求单进程正确**，并发写索引的安全性由 M6 的锁机制统一解决；
  M5 的兜底是「索引坏了就重建 / 降级扫描」，这在语义上是**安全**的，因为
  **Markdown 是唯一权威来源，.index/ 恒为可重建派生**。

### 12.4 本合同交付时的零越界事实（`T-064` 自证）

| 断言 | 结果 |
|-|-|
| `cd evergreen && git status --porcelain` | 空 |
| `cd evergreen && git diff --exit-code -- go.mod` | 退 `0`（`go.mod` 一字未改） |
| `ls internal/index 2>/dev/null \| wc -l` | `0`（未新建包） |
| `grep -rn "os.Exit(5)" internal/ \| wc -l` | `0`（退出码 5 未启用） |
| `cd evergreen && make lint && go test ./... -count=1` | 全绿，M1 ~ M4 的 39 个 e2e 一个不删 |
| 实证是否改动仓库 | 未改：全部实证在 `/tmp/t064_probe` 的独立临时 Go module 内完成 |

---

**合同生效**：本文件落盘并随 `T-…-064` 提交后即生效。`T-…-065` ~ `T-…-068` 以本文件为**唯一**
开工输入；任何与本文件冲突的实现一律判为违约，处置方式是**改实现**或**按 `M-005` 未决表流程
回写本合同**，而**不是**默默偏离。
