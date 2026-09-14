package reconcile

// R5 —— 手工跨领域移动的**只读检测**（对账合同 §8；M4 · T-…-054）。
//
// 唯一职责：把「用户在文件管理器 / 编辑器里手工把一个知识对象挪到别的领域」这件事检出为
// 一条 `domain_moved` finding（**W18 / warning**，分级与诊断码一律取自 check.go 的唯一真源表），
// 并用**固定三元** `targets` 让脚本能直接按位读出「谁从哪个领域挪到了哪个领域」。
//
// # 判定：两条判据，命中任一即产出（合同 §8 逐字）
//
//	① 文件所在的**领域目录段** ≠ 对象自报的（frontmatter / ID 所隐含的）领域；
//	② Git 历史显示该文件发生过**跨领域目录**的 rename（`git log --follow --name-status`
//	   的 `R` 记录，且新旧路径的领域段不同），且该 rename 的 commit **不是** `eg` 产生的。
//
// 两条是**析取**（不是合取）：任一条成立即报，同一个对象**恒只报一条**（见下面的去重）。
//
// 关于条件① 的事实来源，把话说透（诚实性口径，不夸大能力）：
// 领域由**目录**唯一决定（EG-DOM-01），因此 `domain` 是产物 frontmatter 顶层的**黑名单键**
// （`internal/model/blacklist.go`），`eg` 自己**永不**把领域写进产物。于是「对象自报领域」
// 这一事实只能由取数侧给出：M2 扫描底座（`internal/query`）填的是**目录派生值**，
// 此时两个事实恒相等、条件① 恒不命中（正常库因此零误报）；只有当取数侧另有隐含领域来源
// （例如外部编辑残留在 frontmatter 里的黑名单 `domain` 键、或按 ID 反查到的登记领域）
// 时，条件① 才可能命中。本文件**只如实比对进来的两个事实**，不自己去读 frontmatter 的
// 黑名单键（那属扫描层职责），也**不新造领域枚举**（领域名一律走 `internal/model` 的
// `Domain` 类型，本文件零领域字面量清单）。
//
// # 本文件**不做**的事（结构上做不到，不靠自律）
//
//   - **只报告**：不搬文件、不改写 frontmatter 的领域字段、不补关系、不发提交
//     （合同 §8「只报告」+ §1.3 写口归属表第 4 行「R3 / R4 / R5 / R7：无人写」）。
//     因此**零 RepairSpec** —— 产 RepairSpec 的只有 R2（T-…-051）与 R6（T-…-055）。
//     物理改名 / 物理移动属 U-01，任何阶段任何授权都不做。
//   - 不写盘、不起子进程：本文件不引 os 的任何写 API、不引子进程包，也**不持有** Git 仓库句柄。
//     Git 历史事实以 RenameFact（`Input.Renames`）的只读快照形态进来，采样发生在包外
//     （只读封装 `internal/git/log.go` 的 `FollowRenames`，那里只跑 `git log`）。
//   - **不另写一份 verb 白名单**：「是不是 `eg` 产生的」复用 R2 已落地的同一个判定
//     （`IsForeignVerb`，其真源恒是 `internal/model` 的 `KnownVerbs()`，恒 8 值、M4 不增减）。
//     本文件因此不出现任何 verb 字面量。
//   - 不判定 R1 / R2 / R3 / R4 / R6 / R7 任何一项，不注册任何命令，不动报告体。
//
// # 与 R1 / R2 / R3 / R4 不重复计数
//
//   - **R1（`git_uncommitted` / W13）**：R1 看的是**未提交**改动（`Input.Status`），
//     R5 看的是**已提交**的历史 rename 与落盘位置，本文件一个字节都不读 `Status`。
//   - **R2（`reviewed_at_missing` / W14）**：R2 报的是「过目信号滞后」，R5 报的是
//     「跨领域移动」——两件不同的事实，各报一次不算重复；两者的 `targets` 语义也不同
//     （R2 是 `[路径, ID]` 集合，R5 是 `[ID, 旧领域, 新领域]` 三元）。
//   - **R3（四个 relation 码）**：R5 不读 `relations[]` 一个条目（「移动后关系要不要补」
//     属合同 §11.1 明确的「只报告，不自动补齐关系」，M4 不做）。
//   - **R4（`duplicate_id` / E11）**：**同一 ID 落在 ≥ 2 个文件时，R5 整体跳过该 ID** ——
//     那些文件分处不同领域目录只是 ID 冲突这**一件**事实的投影，归 E11 独家承载；
//     若 R5 也报一条 W18，同一件落盘事实就被两码各记一次（T-…-053 在 W16 侧踩过的同一个坑，
//     修法同样是**收紧新检查器的阈值**，不是去放宽别人的判据）。反证见
//     r5_domain_test.go 的 TestR5NoDoubleCountWithR4DuplicateID（同时锁住「别放宽成永不报」）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// R5 是本检查项的 R 编号（与 checkTable 内 CheckDomainMoved 行的 R 列同值）。
const R5 = "R5"

// vault 的领域分区布局（F1 骨架：`domains/<领域>/{knowledge,notes}/<对象>.md`）。
//
// 这三个目录名是**布局常量**，不是领域枚举：领域名本身一个字面量都不在本文件里
// （合同 §8「复用现成解析口径、不新造领域枚举」）。为什么在本包重述一遍而不是引用：
// 布局常量的既有落点在落盘层与命令层的两个包内，按合同 §1.2 它们都是本包的**禁止依赖**
// （反向 / 跨层依赖即成环），而扫描层一侧的同名常量未导出 —— 于是三个目录名只能在本包重述。
const (
	domainsDirName   = "domains"
	knowledgeDirName = "knowledge"
	notesDirName     = "notes"
)

// domainPathDepth 是带领域段的对象路径的**最小**段数：
// `domains` / `<领域>` / `{knowledge|notes}` / `<文件>.md` 恰 4 段。
const domainPathDepth = 4

// RenameFact 是「Git 历史上一次 rename」的只读事实（合同 §8 条件② 的事实来源）。
//
// 三个字段全是可复算的字符串：本包不需要 SHA、作者、时间，也不该持有它们
// （对账只判「跨没跨领域」与「verb 归属」）。逐条由调用方用
// `git log --follow --name-status` 的只读用法采样（封装见 `internal/git/log.go`），
// **空 Verb = 该记录未采到动词位**：条件② 对它不成立（不把缺事实当证据）。
type RenameFact struct {
	// OldPath / NewPath 是 rename 前后的 vault 相对路径（/ 分隔），与扫描面 Path 口径一致。
	OldPath string
	NewPath string
	// Verb 是产生这次 rename 的 commit 的 verb 字面量（不做任何归一化）。
	Verb string
}

// 命中判据的**封闭两值**证据串（进 detail，供逐条复算；不是新 check、不是新诊断码）。
const (
	// DomainEvidenceSegmentMismatch 证据①：领域目录段 ≠ 对象自报领域。
	DomainEvidenceSegmentMismatch = "domain_segment_mismatch"
	// DomainEvidenceCrossDomainRename 证据②：Git 历史有非 `eg` 产生的跨领域 rename。
	DomainEvidenceCrossDomainRename = "cross_domain_rename"
)

// DomainEvidenceCount 是证据的封闭基数：恰 2（合同 §8 的两条判定）。
const DomainEvidenceCount = 2

// domainEvidenceTable 是证据的封闭全集（长度固定数组：第三条证据加不进来），
// 顺序 = 合同 §8 的判定行序，同时是「两条都命中时取哪一条的领域对」的优先级。
var domainEvidenceTable = [DomainEvidenceCount]string{
	DomainEvidenceSegmentMismatch,
	DomainEvidenceCrossDomainRename,
}

// DomainEvidences 返回封闭两值证据集合的副本（顺序即合同 §8 判定行序）。
func DomainEvidences() []string {
	out := make([]string, 0, DomainEvidenceCount)
	out = append(out, domainEvidenceTable[:]...)
	return out
}

// IsKnownDomainEvidence 报告取值是否落在封闭两值内（第三个取值一律 false）。
func IsKnownDomainEvidence(s string) bool {
	for _, v := range domainEvidenceTable {
		if v == s {
			return true
		}
	}
	return false
}

// domainEvidenceLabel 是证据的中文标签（只用于 detail 渲染，不参与任何判定）。
var domainEvidenceLabel = map[string]string{
	DomainEvidenceSegmentMismatch:   "文件所在领域目录段与对象自报领域不一致",
	DomainEvidenceCrossDomainRename: "Git 历史有一次跨领域目录 rename，且那次提交不是 eg 产生的",
}

// followNameStatusSpec 是条件② 的事实口径字面量（合同 §8 逐字），写在这里只为让 detail
// 与用例可逐字复算 —— 本包**不执行**它（起子进程只发生在 `internal/git`）。
const followNameStatusSpec = "git log --follow --name-status"

// DomainOfPath 从 vault 相对路径解出**领域目录段**。
//
// 口径（F1 骨架 + `internal/query` 扫描层的同一层级）：路径必须形如
// `domains/<领域>/{knowledge|notes}/…`，返回 `<领域>`。
// 第二个返回值为 false = 该路径**不带领域段**，R5 对它整体不判定：
//   - `sources/**`（原文分区无领域层级）、`proposals/**`（控制面）、`unprocessed.md`（收件区）；
//   - 段数不足、领域段为空、第三段不是 `knowledge` / `notes` 的路径（层级不符即不判，
//     绝不把「读不出领域」当成「领域变了」）。
//
// 纯字符串运算：零 IO、零正则（不新造解析规则）。返回值用 `internal/model` 的 Domain 类型 ——
// 领域名的类型口径因此与全仓一致，本文件不新造领域集合。
func DomainOfPath(rel string) (model.Domain, bool) {
	p := strings.TrimSpace(rel)
	p = strings.TrimPrefix(p, "./")
	if p == "" {
		return "", false
	}
	seg := strings.Split(p, "/")
	if len(seg) < domainPathDepth || seg[0] != domainsDirName {
		return "", false
	}
	d := model.Domain(strings.TrimSpace(seg[1]))
	if d.Empty() {
		return "", false
	}
	switch seg[2] {
	case knowledgeDirName, notesDirName:
		return d, true
	}
	return "", false
}

// DomainMove 是一条命中 R5 的移动事实（**纯数据**：无函数字段、无句柄、无写方法）。
//
// 为什么导出：报告侧（T-…-057）与命令侧（T-…-058）需要「谁、从哪、到哪」这三个事实，
// 而 Finding 按合同只有四键。两者同源同事实：DomainMoves 与 checkR5DomainMoved
// 走的是同一个判定函数。
type DomainMove struct {
	// ID 是对象 ID（知识卡 / 材料笔记），逐字取自扫描面，不做任何改写。
	ID string
	// Path 是对象当前的 vault 相对路径（/ 分隔）。
	Path string
	// Kind 是对象类别：KindCard 或 KindNote（与 R4 的类别串同源，不另定义一套）。
	Kind string
	// OldDomain / NewDomain 是**旧领域**与**新领域**（targets 第 2 / 第 3 元的来源）。
	OldDomain string
	NewDomain string
	// Evidence 是命中的证据串（封闭两值的子集，去重 + 升序，恒非空）。
	Evidence []string
	// Claim 是对象自报领域的逐字原值（条件① 的事实；未采到即空串）。
	Claim string
	// OldPath / Verb 是条件② 的两个事实：rename 的旧路径与那次提交的 verb
	// （条件② 未命中时均为空串）。
	OldPath string
	Verb    string
}

// DomainMovedTargets 返回这条移动事实的 `targets[]`：**恰三元、顺序固定**
// `[对象 ID, 旧领域, 新领域]`（合同 §8 末句逐字）。
//
// 顺序是**语义**而不是排版：脚本按位取值（`targets[1]` 恒是旧领域、`targets[2]` 恒是新领域），
// 因此这三元**不参与**合同 §2 的「去重 + 字典序升序」——例外在 check.go 的封闭例外表里登记，
// 由 NewFinding / Validate 双侧强制元数与非空（见 finding.go 的 NormalizeOrderedTargets）。
func DomainMovedTargets(m DomainMove) []string {
	return []string{m.ID, m.OldDomain, m.NewDomain}
}

// DomainMoves 返回全部命中 R5 的移动事实（按 (path, id) 升序，可逐字复算）。
//
// 纯函数：同一 Input 恒得同一输出；不改入参、不做任何 IO。
// Scan 为 nil（未扫描）→ 空集合：绝不把「没扫描」当成「没有对象」。
func DomainMoves(in Input) []DomainMove {
	if in.Scan == nil {
		return nil
	}
	x := NewStructureIndex(in)
	renames := renameIndex(in)
	out := make([]DomainMove, 0, len(in.Scan.Cards)+len(in.Scan.Notes))
	add := func(kind, id, path, claim string) {
		if m, ok := domainMoveHit(kind, id, path, claim, x, renames, in.VaultRoot); ok {
			out = append(out, m)
		}
	}
	for _, c := range in.Scan.Cards {
		add(KindCard, c.ID, c.Path, c.Domain)
	}
	for _, n := range in.Scan.Notes {
		add(KindNote, n.ID, n.Path, n.Domain)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// domainMoveHit 是两条判定的**唯一**实现（析取；两条都不成立即 false）。
//
// 去重口径：一个对象**恒只产一条**事实。两条判据同时命中时，领域对取**判定行序在前**者
// （条件① 的「自报领域 → 目录段」），证据两条都进 detail —— 于是输出与输入顺序无关、可复算。
func domainMoveHit(kind, id, path, claim string, x StructureIndex,
	renames map[string][]RenameFact, vaultRoot string) (DomainMove, bool) {
	id, claim = strings.TrimSpace(id), strings.TrimSpace(claim)
	rel, ok := vaultRel(vaultRoot, path)
	if id == "" || !ok {
		return DomainMove{}, false
	}
	// 与 R4 的 duplicate_id 不重复计数：同一 ID 落在 ≥ 2 个文件 → 整体跳过（归 E11）。
	if len(x.ObjectPaths[id]) >= 2 {
		return DomainMove{}, false
	}
	now, ok := DomainOfPath(rel)
	if !ok {
		return DomainMove{}, false // 该路径不带领域段（原文 / 提案 / 收件区 / 层级不符）
	}
	m := DomainMove{ID: id, Path: rel, Kind: kind, NewDomain: now.String(), Claim: claim}
	var ev []string
	// ① 领域目录段 ≠ 对象自报领域（自报领域为空 = 未采到该事实，不判）。
	if claim != "" && claim != now.String() {
		ev = append(ev, DomainEvidenceSegmentMismatch)
		m.OldDomain = claim
	}
	// ② Git 历史有**非 eg 产生**的跨领域 rename，且其新路径就是该对象当前落盘位置。
	if rec, from, ok := crossDomainRename(renames[rel], now, vaultRoot); ok {
		ev = append(ev, DomainEvidenceCrossDomainRename)
		m.OldPath, m.Verb = rec.OldPath, rec.Verb
		if m.OldDomain == "" {
			m.OldDomain = from.String()
		}
	}
	if len(ev) == 0 || m.OldDomain == "" {
		return DomainMove{}, false
	}
	m.Evidence = NormalizeTargets(ev)
	return m, true
}

// crossDomainRename 从该路径的 rename 记录里挑出**跨领域且非 `eg` 产生**的那一条。
//
// 三条筛选（全部成立才算条件② 命中）：
//   - 旧路径能解出领域段，且**不等于**当前领域段（同领域内改名不是跨领域移动）；
//   - 记录的新路径确实是该对象当前落盘位置（由调用方按 vault 相对路径索引保证）；
//   - 那次提交的 verb **不属** `eg` 已知 verb 集合 —— 复用 R2 的 IsForeignVerb
//     （真源恒是 `internal/model` 的 KnownVerbs()，本文件不另写 verb 清单）。
//
// 多条命中时取 (旧路径, verb) 升序的第一条：多跳历史只报**落到当前路径**的那一跳，
// 不把一条移动链拆成多条 finding（同一个对象恒一条）。
func crossDomainRename(recs []RenameFact, now model.Domain,
	vaultRoot string) (RenameFact, model.Domain, bool) {
	type hit struct {
		rec  RenameFact
		from model.Domain
	}
	var hits []hit
	for _, r := range recs {
		if !IsForeignVerb(r.Verb) {
			continue // `eg` 自己产生的 rename 不是「手工移动」（合同 §8 条件② 末句）
		}
		oldRel, ok := vaultRel(vaultRoot, r.OldPath)
		if !ok {
			continue
		}
		from, ok := DomainOfPath(oldRel)
		if !ok || from == now {
			continue // 读不出旧领域 / 同领域内改名：都不是跨领域移动
		}
		hits = append(hits, hit{rec: r, from: from})
	}
	if len(hits) == 0 {
		return RenameFact{}, "", false
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rec.OldPath != hits[j].rec.OldPath {
			return hits[i].rec.OldPath < hits[j].rec.OldPath
		}
		return hits[i].rec.Verb < hits[j].rec.Verb
	})
	return hits[0].rec, hits[0].from, true
}

// renameIndex 把 RenameFact 快照折成「新路径 → 记录集合」的索引（纯字符串运算）。
//
// `Input.Renames` 为 nil（未采样）→ 空索引：条件② 整体不判。
func renameIndex(in Input) map[string][]RenameFact {
	if in.Renames == nil {
		return nil
	}
	out := make(map[string][]RenameFact, len(in.Renames))
	for _, r := range in.Renames {
		rel, ok := vaultRel(in.VaultRoot, r.NewPath)
		if !ok {
			continue
		}
		out[rel] = append(out[rel], r)
	}
	return out
}

// —— finding 的渲染 ——

// r5Detail 渲染 W18 的 detail：对象类别 + ID + 路径 + 旧 / 新领域 + 证据 + 三元 targets 口径
// + 「只报告」的边界（足以逐条复算）。
func r5Detail(m DomainMove) string {
	var extra string
	if m.OldPath != "" {
		extra = fmt.Sprintf("；rename 记录 %s → %s（%s 的 R 记录，"+
			"该提交 verb=%q 不属 eg 已知 verb 集合的 %d 个取值，故判为外部编辑）",
			m.OldPath, m.Path, followNameStatusSpec, m.Verb, len(model.KnownVerbs()))
	}
	return fmt.Sprintf(
		"%s %s 被手工跨领域移动：旧领域 %s → 新领域 %s（当前落盘于 %s；命中判据：%s%s）；"+
			"targets 恰 %d 元且顺序固定 [对象 ID, 旧领域, 新领域]，"+
			"本次**只报告**——不搬动文件、不改写领域字段、不补关系（合同 §8）",
		labelOf(m.Kind), m.ID, m.OldDomain, m.NewDomain, m.Path,
		strings.Join(domainEvidenceLabels(m.Evidence), "、"), extra, DomainMovedTargetArity)
}

// domainEvidenceLabels 把证据串翻成中文标签（未知取值原样返回，保证 detail 恒非空）。
func domainEvidenceLabels(ev []string) []string {
	out := make([]string, 0, len(ev))
	for _, e := range ev {
		if s, ok := domainEvidenceLabel[e]; ok {
			out = append(out, s)
			continue
		}
		out = append(out, e)
	}
	return out
}

// —— 检查项本体与注册 ——

// checkR5DomainMoved 是 R5 检查项本体：每个命中对象产**恰一条** finding、**零 RepairSpec**。
//
// 纯函数：同一 Input 恒得同一输出（含顺序）；不改入参、不产生任何副作用。
// 零命中 → 空集合，于是报告侧 `domain_moved` 条数为 0（R5 永不写入，故无「幂等写」之说）。
func checkR5DomainMoved(in Input) ([]Finding, []RepairSpec) {
	moves := DomainMoves(in)
	if len(moves) == 0 {
		return nil, nil
	}
	fs := make([]Finding, 0, len(moves))
	for _, m := range moves {
		f, err := NewFinding(CheckDomainMoved, DomainMovedTargets(m), r5Detail(m))
		if err != nil {
			// 三种构造错误（未知 check / 空 detail / 三元不合规）在本文件都不可能发生：
			// check 取自封闭表常量、r5Detail 恒产非空串、三元的每一元在判定里都已非空。
			// 防御性丢弃而不 panic：对账是只读检查，任何情况下都不该让进程死在检查器里。
			continue
		}
		fs = append(fs, f)
	}
	if len(fs) == 0 {
		return nil, nil
	}
	return fs, nil
}

// 注册：R5 是本包第五个落地的检查项（R1 / R2 / R4 / R3 分属 T-…-050 / 051 / 052 / 053）。
//
// 注册表 checkers 在 reconcile.go 内声明，一个 task 只在自己的文件里追加自己那一项 ——
// R6 / R7 分属 T-…-055 / 056，本文件因此**恰追加一项**。
func init() { checkers = append(checkers, checkR5DomainMoved) }

// R5ScanOf 是给命令层与用例的便利函数：把扫描快照 + Git 历史 rename 事实折成 Input。
//
// 存在的理由同 R1StatusOf / R2ScanOf / R4ScanOf：R5 只需要「扫描结果 + rename 事实」两样，
// 不需要 Git 未提交状态（Status 恒零值）、不需要 `sources/` 分区快照（Sources 恒 nil）——
// 但扫描底座仍复用 internal/query，本包不另写扫描器（合同 §0.1 第 1 条）。
// renames 传 nil 表示该事实未采样：条件② 整体不判，只留条件①。
func R5ScanOf(vaultRoot string, scan *query.ScanResult, renames []RenameFact) Input {
	return Input{VaultRoot: vaultRoot, Scan: scan, Renames: renames}
}
