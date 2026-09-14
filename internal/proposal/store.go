package proposal

// 提案的**读写与按状态检索**（提案合同 §7 落盘形态 + §10.1 原地留存 + A-23 直写例外）。
//
// 合同落点：
//   - 读：`eg proposal list` / `eg proposal show` 的数据来源，以及 T-041 `--proposal`
//     引用校验的存在性判据。
//   - 写：新建走 store.ApplyProposalCreate、改写走 store.ApplyProposalUpdate
//     （A-23 第 2 条「必须复用 guarded store，禁止裸写文件」）。
//
// 四条不变式（写在这里，因为它们是本文件存在的理由）：
//  1. **读是纯只读**：绝不回写 reviewed_at / updated_at（提案根本没有这两键，M-6），
//     不新建文件、不 commit —— 因此 `list` / `show` 在任何仓库状态下都不改变工作区。
//  2. **不依赖索引**：定位与检索一律直接扫描 Markdown + frontmatter（S1/S2 无增量索引，
//     那是 S4 的目标态），本文件也因此不出现任何派生缓存目录字样。
//  3. **路径不是主键**：默认落位是 Rel(id)，但提案允许改名 / 移动（F2），
//     所以 ID 未命中默认路径时退化为全库扫描，绝不因文件名不符就判「不存在」。
//  4. **写只经字节**：候选字节由 schema.go 的模板拼装 / superseded.go 的字节级标量替换产出，
//     本文件一个 YAML 序列化器都不碰（I-2 / I-5，make lint 有 guard）。
//
// 解析口径：外部数据从「未知」出发 —— proposals/ 下的 Markdown 一旦解析或形态校验不过，
// 立即返回错误，不静默跳过、不猜默认值（静默兜底会让「库里到底有哪些提案」这件事失真）。

import (
	"errors"
	"fmt"
	"sort"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

var (
	// ErrProposalNotFound 表示扫遍 proposals/ 也没有该 ID 的提案（引用校验据此判「不存在」）。
	ErrProposalNotFound = errors.New("未找到该提案 ID")
	// ErrUnknownStatusFilter 表示检索条件里的 status 不在四态封闭集合内。
	ErrUnknownStatusFilter = errors.New("检索条件 status 不在四态封闭集合内")
	// ErrUnknownExecFilter 表示检索条件里的 execution.status 不在三态封闭集合内。
	ErrUnknownExecFilter = errors.New("检索条件 execution.status 不在三态封闭集合内")
)

// Record 是一份被读出的提案：ID + 实际落位路径 + content_hash + 解析结果。
//
// Hash 随读盘一起带出，正是为了让「读 → 改 → 写」这条链把 B3 守卫串起来：
// 调用方把它原样交给 Update，磁盘在这期间被人动过就会被拒写（SkipFileChanged）。
// Rel 是**实际**路径（不一定等于 Rel(ID)），因为提案允许改名 / 移动（F2）。
type Record struct {
	ID   ID
	Rel  string
	Hash string
	File *File
}

// Raw 返回该提案的原始字节（逐字，未经任何归一化）。
func (r Record) Raw() []byte { return r.File.Raw }

// Proposal 返回结构化 frontmatter 视图。
func (r Record) Proposal() Proposal { return r.File.P }

// Filter 是按状态维度的检索条件。
//
// 两维**正交**（§4.1：status 是用户决定，execution.status 是执行事实），因此同时给出即取交集。
// 空串表示该维不过滤；非空则必须落在封闭集合内，否则 Validate 报错（拼错的状态名不能被
// 静默当成「查不到」—— 那会让调用方误判库里没有这类提案）。
type Filter struct {
	Status Status
	Exec   ExecStatus
}

// Validate 判定检索条件取值是否在封闭集合内（空串 = 不过滤）。
func (f Filter) Validate() error {
	if f.Status != "" && !f.Status.Valid() {
		return fmt.Errorf("%w：%q", ErrUnknownStatusFilter, f.Status)
	}
	if f.Exec != "" && !f.Exec.Valid() {
		return fmt.Errorf("%w：%q", ErrUnknownExecFilter, f.Exec)
	}
	return nil
}

// Match 报告一份提案是否命中检索条件。
func (f Filter) Match(p Proposal) bool {
	if f.Status != "" && p.Status != f.Status {
		return false
	}
	if f.Exec != "" && p.Execution.Status != f.Exec {
		return false
	}
	return true
}

// —— 读：只读扫描，零副作用 ——

// Load 按 ID 读出一份提案（只读）。
//
// 两步定位：先试默认落位 Rel(id)，未命中再全库扫描 frontmatter 的 id。
// 顺序刻意如此：默认路径命中时只读一个文件（`show` 的常态），扫描只在改过名时付代价。
func Load(s *store.Store, id ID) (Record, error) {
	if s == nil {
		return Record{}, ErrNoStore
	}
	if _, _, err := ParseID(string(id)); err != nil {
		return Record{}, err
	}
	rel := Rel(id)
	ok, err := s.Exists(rel)
	if err != nil {
		return Record{}, err
	}
	if ok {
		rec, err := LoadRel(s, rel)
		if err != nil {
			return Record{}, err
		}
		if rec.ID == id {
			return rec, nil
		}
		// 默认路径上躺着另一个 ID 的提案：ID 才是主键，继续扫描，不认路径。
	}
	rels, err := scanProposalRels(s)
	if err != nil {
		return Record{}, err
	}
	for _, r := range rels {
		rec, err := LoadRel(s, r)
		if err != nil {
			return Record{}, err
		}
		if rec.ID == id {
			return rec, nil
		}
	}
	return Record{}, fmt.Errorf("%w：%s", ErrProposalNotFound, id)
}

// LoadRel 按 vault 内相对路径读出一份提案（只读）。
//
// 路径必须落在 proposals/ 下：读口与写口共用同一条边界判据，
// 免得「读一个知识卡当提案解析」这种事悄悄发生。
func LoadRel(s *store.Store, rel string) (Record, error) {
	if s == nil {
		return Record{}, ErrNoStore
	}
	if !store.IsProposalRel(rel) {
		return Record{}, fmt.Errorf("%w：%s", store.ErrNotProposalRel, rel)
	}
	f, err := s.Read(rel)
	if err != nil {
		return Record{}, err
	}
	pf, err := Parse(f.Bytes)
	if err != nil {
		return Record{}, fmt.Errorf("%s：%w", rel, err)
	}
	return Record{ID: pf.P.ID, Rel: rel, Hash: f.Hash, File: pf}, nil
}

// List 读出全部在库提案（按 ID 升序，只读）。
//
// 升序而非扫描序：ScanIDs 返回 map，遍历顺序随机，报告与测试必须可复算。
func List(s *store.Store) ([]Record, error) {
	if s == nil {
		return nil, ErrNoStore
	}
	rels, err := scanProposalRels(s)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(rels))
	for _, rel := range rels {
		rec, err := LoadRel(s, rel)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Search 按 status / execution.status 检索提案（只读，结果按 ID 升序）。
func Search(s *store.Store, f Filter) ([]Record, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	all, err := List(s)
	if err != nil {
		return nil, err
	}
	return Select(all, f), nil
}

// Select 在已读出的记录集上施加检索条件（纯函数，便于上层复用一次扫描的结果）。
func Select(recs []Record, f Filter) []Record {
	out := make([]Record, 0, len(recs))
	for _, rec := range recs {
		if f.Match(rec.File.P) {
			out = append(out, rec)
		}
	}
	return out
}

// scanProposalRels 扫描出 proposals/ 下**带合法提案 ID** 的文件路径（升序）。
//
// 只用 store 的只读扫描口（ScanIDs）：本包不自己遍历文件系统，也不读任何派生缓存。
// 提案目录下没有 frontmatter id 的文件不在这里报错 —— 那是校验命令（037）的职责，
// 检索口不该因为库里多了一份草稿而整体失败。
func scanProposalRels(s *store.Store) ([]string, error) {
	idx, err := s.ScanIDs()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(idx.ByID))
	for id, rel := range idx.ByID {
		if !store.IsProposalRel(rel) {
			continue
		}
		if !ID(id).Valid() {
			continue
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

// —— 写：两条 guarded 通道，一个字节都不裸写 ——

// Create 新建一份提案：模板字节拼装 → store 的 guarded 新建写口。
//
// 落位固定为 Rel(t.ID)：**新建**时文件名必须是默认形态（MatchRel 的判据），
// 之后允许被改名 —— 两件事不矛盾，前者约束本工具的产出，后者是既有文件的自由。
func Create(s *store.Store, t Template) (store.Result, error) {
	if s == nil {
		return store.Result{}, ErrNoStore
	}
	content, err := RenderTemplate(t)
	if err != nil {
		return store.Result{}, err
	}
	return s.ApplyProposalCreate(Rel(t.ID), content)
}

// Update 用调用方拼好的候选字节改写一份既有提案（B3 守卫 + 写前复核）。
//
// content 必须是**完整文件字节**，且由字节级替换产出（未知 frontmatter 键、注释、空行、
// 正文七分区逐字保留）。写前复核不过一律返回错误、绝不调用写口，因此失败即零写入。
// expectedHash 来自读盘时的 Record.Hash：磁盘在此期间被改动会被拒写（SkipFileChanged）。
func Update(s *store.Store, rel, expectedHash string, content []byte) (store.Result, error) {
	if s == nil {
		return store.Result{}, ErrNoStore
	}
	if err := verifyControlPlane(content, nil); err != nil {
		return store.Result{Path: rel}, err
	}
	return s.ApplyProposalUpdate(store.ProposalUpdateSpec{
		Rel: rel, ExpectedHash: expectedHash, Content: content,
	})
}
