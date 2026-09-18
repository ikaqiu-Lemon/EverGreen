package query

// [S4] 读路径的**后端选择单点**（M5 索引架构合同 §5.2 / §6.1 / §7.4；T-…-067）。
//
// 本文件回答一个问题、且是全库唯一回答它的地方：**这一次读，取数走索引还是走全量
// Markdown 扫描？** 三条只读命令（`eg search` / `eg card show` / `eg rel`）一律调用
// SelectBackend，选择逻辑不散落到各命令、不在命令层各自 if 一遍。
//
// 三条不可动摇的前提（合同 §1.1 P-2 / §6.1）：
//  1. **Markdown 是唯一权威来源**：索引只是派生候选集与可见性事实的快查表，任何
//     「正文是什么 / 关系理由是什么 / 时间戳是什么」的答案都回权威文件逐字取；
//  2. **索引异常绝不阻断读**：missing / corrupt / stale 一律降级为全量扫描并照常出
//     结果，退出码不变（M5 不启用退出码 5）；
//  3. **降级必须留痕**：降级时由 degrade.go 产出「恰一条 W22|W23|W24 + 恰一条 Q5」。
//
// 明确不做（属 T-…-068）：排序键与四级全序的任何改动、截断 / 分页 / `W25`、
// `replaced_by` 反查的性能形态、`bench` 与 10k 语料门槛。本文件不含任何时间门槛。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// 后端取值域（**恰 2 值**，封闭集合）。
const (
	// BackendIndex 索引后端：由 `.index/eg.db` 给出候选集与可见性事实，
	// 只解析**结果与判定真正需要**的权威文件。
	BackendIndex = "index"
	// BackendScan 扫描后端：M2 的全量 Markdown 扫描底座（VaultScan），
	// 既是降级目的地，也是索引表达不了的查询的正常归宿。
	BackendScan = "scan"
)

// Backends 返回封闭的后端集合（恰 2 值，次序即「优先索引、兜底扫描」）。
func Backends() []string { return []string{BackendIndex, BackendScan} }

// 走扫描后端的原因（封闭集合，机器可读；`BackendIndex` 时恒为空串）。
const (
	// ReasonIndexUnusable 索引缺失 / 不可用（配 W23 / W24），处置 = eg index build|rebuild。
	ReasonIndexUnusable = "index_unusable"
	// ReasonIndexStale 索引可用但落后于权威（配 W22），处置 = eg index sync。
	ReasonIndexStale = "index_stale"
	// ReasonQueryNotExpressible 索引**健康**，但本次查询用得到的字段不在索引里，
	// 索引给不出「无假阴性」的候选超集 —— 这**不是**降级（索引没坏也没旧），
	// 因此不产 W22/W23/W24，也**不产** Q5，只是这次取数本来就该走扫描。
	// 现存唯一实例见 searchNeed()：`eg search` 的 tags 命中维度与 updated_at 区间。
	ReasonQueryNotExpressible = "query_not_expressible"
	// ReasonFreshnessUnverifiable 索引**看起来健康**，但本次读**证不出它跟得上权威**：
	// A-44 的水位线是 `(head, files_hash)`，而 `files_hash` 要按 B3 口径逐文件算
	// `content_hash`、`head` 要问 Git —— 这两处单点都在 `internal/store` / `internal/git`，
	// 查询层依施工索引 §13 一个都不能 import，只能由命令层**注入**（见 IndexDeps）。
	// 没注入就证不出新鲜，**证不出就不用**（宁可多扫一遍，绝不拿证不出新鲜的索引出结果）。
	//
	// 这**不是**降级：索引既没坏也没旧，是本次调用方没给判定口径 ⇒ 不产 W22/W23/W24，
	// 也**不产** Q5（Q5 的语义是「索引病了所以降级」，不是「调用方少给了一个参数」）。
	// 生产路径恒注入（三条读命令都经 cli 的 readIndexDeps），故此值只出现在
	// 未注入的历史调用方（如 ShowCard 的 M2 签名）与包内单测里。
	ReasonFreshnessUnverifiable = "freshness_unverifiable"
)

// ScanReasons 返回封闭的「为什么走扫描」集合（恰 4 值）。
func ScanReasons() []string {
	return []string{ReasonIndexUnusable, ReasonIndexStale, ReasonQueryNotExpressible,
		ReasonFreshnessUnverifiable}
}

// IndexDeps 是读路径判定「索引是否跟得上权威」所必需的两处**外部单点口径**
// （M5 索引架构合同 §5.1 / A-44）。
//
// 为什么要注入而不是自己算：A-44 定死水位线 = `(head, files_hash)`，其中
//   - `content_hash` 与 M1 `internal/store` 写口的 B3 **同源同算法**，合同 §5.1 末段
//     逐字禁止「自定义第二套 hash」；
//   - `head` 是 Git 事实，唯一读口是 `internal/git`。
//
// 而施工索引 §13 里 `query` 的允许依赖只有 `model`（S1 直连 `mdfile`）：store 与 git
// 一个都不能 import。因此这两处口径由**命令层注入**（`internal/cli` 的 readIndexDeps），
// 与 `eg context` 注入 `Hasher` 是同一套既有做法（context.go 的 ErrHasherRequired），
// 不新造机制、也不复制口径。
//
// 注入后的直接后果：读路径与 `eg index status`（默认快路径）**调用同一个** `index.Check`，
// 因此「什么算陈旧」在两处**不可能分叉**；mtime / size 只作快路径过滤（A-44 逐字），
// 三者任一不一致就回权威重算 `content_hash` 再判定，**不以 mtime 作最终结论**。
type IndexDeps struct {
	// Hash 是 B3 content_hash 口径；唯一实现 store.ContentHash。
	Hash Hasher
	// Head 是 Git HEAD 口径；唯一实现 internal/git 的 Head()，取不到时返回空串
	// （与 cli 的 indexHead 逐字同口径：拿不到 head 就当空串参与水位线比对）。
	Head func() string
}

// complete 报告两处口径是否都已注入：缺一即证不出新鲜度。
func (d IndexDeps) complete() bool { return d.Hash != nil && d.Head != nil }

// Need 描述一次读请求要从取数层拿到什么：SelectBackend 的第二个输入。
//
// 三个判定位刻意都是**开发期就能定死的事实**（索引列集合按合同 §4.1 封闭），
// 不是运行期猜测；每条读路径各有一个 Need 构造函数并在注释里写清依据，
// 改判定必须同时改依据。
type Need struct {
	// Path 是读路径名（诊断与留痕用；取值 = 命令名）。
	Path string
	// Focus 是本次读的焦点卡 ID（`card show <id>` / `rel <id>`）；`search` 无焦点即空串。
	// 索引后端据此把「必须回权威解析」的文件收敛到焦点卡与它的反向来源卡。
	Focus string
	// FullCardFields 为真 = **结果集判定**用到了索引未收录的卡字段（tags / updated_at /
	// 正文原值）。此时索引仍然可用，但候选集必须**保守取全部在册卡**并逐个回权威
	// Markdown 解析 —— 宁可多读，绝不容许一条假阴性（窄化召回属 T-…-068 的性能工作，
	// 且要等索引列集合补齐后才可能做到无假阴性）。
	FullCardFields bool
	// NotesInScope 为真 = 本次读的扫描面含**材料笔记**。索引的对象面只有知识卡
	// （`cards` / `cards_fts` / `relations` 三表都以卡为单位，合同 §4.1），
	// 笔记在索引里连行都没有 ⇒ 这类查询**不可由索引表达**，一律走扫描后端。
	// 三条只读命令都不含笔记面；这一位是给未来接入 SelectBackend 的读路径
	// （如 `eg unreviewed` / 综述取材）留的**守卫**：接错了会拿到扫描后端而不是漏结果。
	NotesInScope bool
	// Why 是「不可由索引表达」时的逐字理由（进人类可读诊断与测试断言）。
	Why string
	// ReplacedBy 为真 = 本次 `eg rel` 走的是**替代指针视图**（`--replaced-by`）。
	// 它只影响索引后端「必须回权威解析哪些文件」的解析计划：替代指针的**反向来源**
	// （`replaced_by.target == focus` 的宿主端点）不在 `relations` 表里，索引又不存
	// `replaced_by.reason` 列，故这些宿主必须回权威补齐逐字 reason（与论证关系反向来源
	// 走 needOpinion / need 同一条纪律）。默认视图（论证关系）此位为 false，解析集合一格不变。
	ReplacedBy bool
	// deps 是 A-44 水位线判定所需的注入口径（见 IndexDeps）。
	// 刻意**不导出**：它不是「这次读要什么」的一部分，而是「这次读由谁提供判定口径」，
	// 由各读路径从自己的请求结构体里取出后经 needXxx 构造函数带进来，外部改不到。
	deps IndexDeps
}

// searchNeed 是 `eg search` 的取数要求：走索引，但候选集**保守取全集**。
//
// 依据（合同 §4.1 列集合 + M2 查询合同 §1.1 / §1.3）：
//   - `cards` 表**没有** `tags` 列，`cards_fts` 的四列固定为
//     `id / title / body / bigram_text` —— 而 §1.3 的匹配分含「命中 tags +2」、
//     `--tag` 过滤又是逐字 AND：若拿 FTS5 MATCH 的召回当候选集，会**漏掉**
//     「只在标签命中」的卡，那是假阴性，直接违反「索引与扫描结果逐字等价」；
//   - `cards` 表只有 `mtime_unix`，**没有** `created_at` / `updated_at` ——
//     `--since` / `--until` 是 `updated_at` 日期部分的闭区间，mtime 不是它。
//
// 结论不是「search 不能用索引」，而是「search 的候选集**不得窄化**」：索引照常提供
// 在册卡集合与「哪些文件索引里没有」这两件事实，候选集则取全集并逐个回权威解析，
// 打分 / 过滤 / 排序继续走 Filter / SortEntries 这一处既有单点。等价性因此是**构造性**的：
// 候选集 ⊇ 扫描面上的全部卡，解析口径又是同一个 CardEntryFrom。
//
// 窄化召回（FTS5 MATCH + 列集合补齐）属 T-…-068 的性能工作与后续 schema 变更，
// 本 task 一行不做，也不留时间门槛。
func searchNeed(deps IndexDeps) Need {
	return Need{Path: "search", FullCardFields: true, deps: deps,
		Why: "search 的匹配分含 tags 维度、过滤含 updated_at 区间，" +
			"而索引列集合（合同 §4.1）不含 tags / updated_at：候选集必须取全集，不得窄化"}
}

// cardNeed 是 `eg card show <id>` 的取数要求：索引够用，且**可窄化**。
//
// 依据：本卡的一切字段回权威文件解析（只此一个文件）；反向关系的来源卡由
// `relations.dst_id` 精确给出（表里存全部正向边，故按 dst_id 反查即无假阴性的
// 来源集合），逐条 reason 再回那几个源文件取逐字原值；对端可见性只需
// `cards.deprecated` / `cards.deleted` 两列，索引在册。
func cardNeed(focus string, deps IndexDeps) Need {
	return Need{Path: "card show", Focus: focus, deps: deps}
}

// relNeed 是 `eg rel <id>` 的取数要求：与 cardNeed 同构（同一套正反向关系事实）。
func relNeed(focus string, deps IndexDeps) Need {
	return Need{Path: "rel", Focus: focus, deps: deps}
}

// notesNeed 是**含材料笔记**读路径的取数要求：索引的对象面没有笔记 ⇒ 不可由索引表达。
//
// 现无生产调用方（三条只读命令都不含笔记面）：它是 NotesInScope 这条守卫的构造入口，
// 由 TestBackendSelectionSinglePoint 逐字反证「健康索引 + 笔记面 ⇒ 扫描后端且不产 Q5」。
// 未来把 `eg unreviewed` / 综述取材接进 SelectBackend 时**直接用它**，
// 不要再新写一套判定。
func notesNeed(path string, deps IndexDeps) Need {
	return Need{Path: path, NotesInScope: true, deps: deps,
		Why: "索引的对象面只有知识卡（合同 §4.1 三表都以卡为单位），" +
			"材料笔记在索引里没有行：含笔记的扫描面不可由索引表达"}
}

// Backend 是一次后端选择的完整结论：**机器可读 + 人类可读同源**，不引入第三套事实。
type Backend struct {
	// Kind ∈ Backends()。
	Kind string
	// Path 是读路径名（回填自 Need.Path）。
	Path string
	// Reason 是走扫描后端的原因（∈ ScanReasons()）；走索引时为空串。
	Reason string
	// Code 是降级原因码：`W22`（陈旧）/ `W23`（缺失）/ `W24`（不可用）——
	// 取值一律引用 internal/index 的常量，查询层**不写这三个码的字面量**
	// （码的归属域是索引面，M3 起的越域码门禁逐字反证这一点）。
	// 索引健康时为空串（含 ReasonQueryNotExpressible：健康 ⇒ 无码 ⇒ 无 Q5）。
	Code string
	// Message 是人类可读说明（与 Reason / Code 同源同事实，不新增事实）。
	Message string
	// Health / Freshness 是索引探测的原始结论（如实透出，便于测试与排障）。
	Health    index.Health
	Freshness index.Freshness
	// Probe 是本次探测的现态快照：索引后端据此按需解析文件，
	// 扫描后端不看它。零值表示未探测到可用索引。
	probe indexProbe
}

// UseIndex 报告本次取数是否走索引后端。
func (b Backend) UseIndex() bool { return b.Kind == BackendIndex }

// Degraded 报告本次读是否**因索引不健康**而降级：这正是「该不该产 Q5」的判据
// （合同 §6.3：Q5 与 W22|W23|W24 同现，有降级必有一条原因码）。
//
// 注意 `ReasonQueryNotExpressible` **不算**降级：索引没坏也没旧，只是这次查询用不上它。
func (b Backend) Degraded() bool { return b.Code != "" }

// SelectBackend 是**全库唯一**的后端选择实现（判据 9：本文件恰一处定义，见
// TestBackendSelectionSinglePoint 与 Task 的 `grep -c` 等号断言）。
//
// 判定次序（先健康、后表达力）——次序本身是合同要求：
//
//	① 探测索引健康与新鲜度（index.Inspect + index.Check，见 index_backed.go）；
//	② 索引缺失 / 不可用 → 扫描后端 + W23 / W24（降级，产 Q5）；
//	③ 索引陈旧        → 扫描后端 + W22（降级，产 Q5；合同 §5.2「陈旧也走扫描」，
//	   不允许「先用旧结果再提示」）；
//	④ 新鲜度证不出来（未注入 A-44 口径 / 权威读不动）→ 扫描后端，**无码无 Q5**；
//	⑤ 索引健康但查询不可由索引表达（扫描面含材料笔记）→ 扫描后端，
//	   **无码无 Q5**（不是降级：索引没坏也没旧，只是这次的对象面它表达不了）；
//	⑥ 其余             → 索引后端。
//
// 为什么健康判定在表达力之前：`rm -rf .index/` 之后**三条**读命令都在一个降级环境里
// 出结果，用户与 agent 需要在每条读命令上看到这一事实（合同 §6.1 末段：降级结果不得
// 被当成权威结果）。若先判表达力，search 会因为「反正走扫描」而吞掉索引不可用的事实。
//
// 永不返回 error：索引任何异常都是**诊断**而不是失败（合同 §6.1）。
func SelectBackend(root string, need Need) Backend {
	probe := probeIndex(root, need.deps)
	b := Backend{
		Path: need.Path, Health: probe.diag.Health, Freshness: probe.freshness(), probe: probe,
	}
	switch {
	case !probe.diag.Usable():
		b.Kind, b.Reason, b.Code = BackendScan, ReasonIndexUnusable, probe.diag.Code
		b.Message = fmt.Sprintf("索引不可用（%s）：本次 %s 已降级为全量 Markdown 扫描，"+
			"结果与索引在位时等价；%s", probe.diag.Reason, need.Path, probe.diag.Message)
	case probe.stale:
		b.Kind, b.Reason, b.Code = BackendScan, ReasonIndexStale, index.CodeIndexStale
		b.Message = fmt.Sprintf("索引已陈旧（%s）：本次 %s 已降级为全量 Markdown 扫描，"+
			"结果以权威 Markdown 为准；%s", probe.staleReason, need.Path, probe.staleDetail)
	case probe.unverifiable:
		b.Kind, b.Reason = BackendScan, ReasonFreshnessUnverifiable
		b.Message = fmt.Sprintf("索引看起来健康，但本次 %s 证不出它跟得上权威，"+
			"取数走全量扫描（不是降级，因此无诊断码、无 Q5）：%s", need.Path, probe.unverifiableWhy)
	case need.NotesInScope:
		b.Kind, b.Reason = BackendScan, ReasonQueryNotExpressible
		b.Message = fmt.Sprintf("索引健康但本次 %s 不可由索引表达，取数走全量扫描：%s",
			need.Path, need.Why)
	default:
		b.Kind = BackendIndex
		b.Message = fmt.Sprintf("索引健康且与权威一致：本次 %s 走索引后端（候选集与可见性"+
			"来自索引，正文 / 理由 / 时间戳一律回权威 Markdown 逐字取）", need.Path)
	}
	return b
}
