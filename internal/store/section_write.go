package store

// `eg edit` 的**分区级替换**落盘形态（授权合同 §5 B1 那一行、§6 U-03、矩阵 #12 / #15；
// 临时裁决 A-13；T-evergreen.s1_main_flow-158614-045）。
//
// 为什么「替换」不违反 B1：B1 的原文定义本身就把用户显式路径排除在外——「替换与删除
// **只在用户显式发起的命令里存在（S2 起）**」。因此 P-U 的替换能力是 B1 的**内含**，
// 不是对 B1 的突破；Agent 自动路径拿不到这条形态（授权判定唯一在 internal/plan 的
// editSectionGate + 写权限矩阵，本包不判断发起方，也不提供任何「跳过判定」的开关）。
//
// 授权**不是**豁免（合同 §5），本文件逐条沿用既有实现，一条都不放宽：
//   - **B3**：写前读盘 + 逐文件 `content_hash` 比对（mutateGuarded），不一致 →
//     *SkipError{file_changed / content_hash_mismatch}，零写入，上层退 3；
//   - **B2**：「用户补充」与「存疑与待验证」的既有字节由 PreserveUserSectionsAfterCut
//     逐字校验，无法安全保留 → *SkipError{user_block_unsafe}，跳过该文件；
//     「用户补充」本身更是**任何路径任何时候**都拒写（U-03，mdfile 再兜一道）；
//   - **B4**：本包不 commit、不回滚、不重试，失败即保留磁盘现状。
//
// 字节机制仍然只有一条：mdfile 的区间拼接（ReplaceSectionBody），本包不拼 YAML、
// 不做「结构体 → 序列化」回写，正文与未知字段逐字保留。
//
// 三个正交维度一格不碰：本形态只换分区正文字节，`status`、`deleted_at` / `deleted_reason`、
// `reviewed_at` 全部不写（`reviewed_at` 只由 `eg mark-reviewed` 写，A-16 / 矩阵 #7）。
//
// `updated_at` **不属**那三个维度，而是本次写入的元数据：spec.Stamp 非零时它在同一次
// 守卫写里被整行刷新（矩阵第 8 行「由 CLI 在实际写入时更新」，唯一实现见 updated_at.go）。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ReplaceSectionSpec 是一次分区正文替换的落盘输入。
type ReplaceSectionSpec struct {
	// Rel 是目标文件的 vault 内相对路径。
	Rel string
	// ExpectedHash 是调用方读盘时的 content_hash（B3 凭据；空串表示无凭据，
	// 上层校验期已按 W6 跳过，不应走到这里）。
	ExpectedHash string
	// ID 只用于诊断文本（locator），不参与任何字节构造。
	ID model.CardID
	// Section 是被替换的 H2 分区名（白名单在 internal/plan：矩阵 P-U 为 ✅ 的分区）。
	Section string
	// Content 是新的分区正文字节，逐字落盘，必须以 \n 结束（本层不替调用方补字节）。
	Content []byte
	// Stamp 是本次写入时刻：非零时在**同一次**守卫写里把既有 `updated_at` 整行刷新
	// （授权合同 §2.1 矩阵第 8 行「由 CLI 在实际写入时更新」；I-…-009）。
	// 零值 = 调用方不要求刷新，本层绝不代入「现在」。
	Stamp model.Stamp
}

// ApplyReplaceSection 用 spec.Content 逐字替换目标文件里 spec.Section 的整段正文。
//
// 固定次序（与 ApplyReplaceBlock 同源，一步都不得前后调换）：
//
//	读盘 → B3 content_hash 比对 → Parse→Render 字节自检 → 分区正文区间替换
//	→ 候选字节自检 + B2 用户分区逐字保留 → tmp + fsync + rename
//
// 正文之外的一切字节（frontmatter 全部键、其它分区、用户自建第六分区、文件尾）逐字不动。
func (s *Store) ApplyReplaceSection(spec ReplaceSectionSpec) (Result, error) {
	res := Result{Path: spec.Rel}
	if spec.Rel == "" {
		return res, ErrCardRelRequired
	}
	if neverWrite(spec.Section) {
		// U-03：CLI 任何路径、任何时候都不写「用户补充」。这里 fail fast，
		// 不做「跳过并继续」的静默兜底——请求本身就不合法。
		return res, fmt.Errorf("%w：%s", ErrUserSectionWrite, spec.Section)
	}
	if len(spec.Content) == 0 || spec.Content[len(spec.Content)-1] != '\n' {
		return res, mdfile.ErrPayloadNotLineTerminated
	}
	// 正文被整段替换是最典型的「实际写入」→ 同一次守卫写里刷新 `updated_at`：
	// 否则「过目 → 改内容」的卡永远进不了 `eg unreviewed`（I-…-009 的现场）。
	return s.mutateGuarded(spec.Rel, spec.ExpectedHash,
		withUpdatedAt(spec.Stamp, func(_ File, doc *mdfile.Doc) ([]byte, error) {
			return doc.ReplaceSectionBody(spec.Section, sectionBodyFraming(spec.Content))
		}))
}

// sectionBodyFraming 把「分区正文载荷」补成**分区正文区间**的字节形态：
// 标题行之后一个空行 + 载荷 + 结尾一个空行——与新建产物的拼装口径（content.go 的
// document）逐字一致，因此编辑过的文件与新建的文件排版同构，round-trip 稳定。
//
// 这不是「改写用户内容」：载荷本身一个字节都不动，只在其**前后**补分区间的空行；
// 空行属分区骨架（新建时也是这两个字节），不属用户文本。
func sectionBodyFraming(payload []byte) []byte {
	out := make([]byte, 0, len(payload)+2)
	out = append(out, '\n')
	out = append(out, payload...)
	return append(out, '\n')
}

// ReplaceCandidateSectionSpec is the candidate-mode payload for edit_section.
type ReplaceCandidateSectionSpec struct {
	Rel          string
	ExpectedHash string
	ID           model.NoteID
	Candidate    string
	Section      string
	Content      []byte
}

// ApplyReplaceCandidateSection replaces exactly one unmaterialized candidate
// H4 payload. Unlike artifact section edits, candidate edits leave frontmatter,
// including updated_at, byte-identical.
func (s *Store) ApplyReplaceCandidateSection(spec ReplaceCandidateSectionSpec) (Result, error) {
	res := Result{Path: spec.Rel}
	if spec.Rel == "" {
		return res, ErrNoteRelRequired
	}
	if len(spec.Content) == 0 || spec.Content[len(spec.Content)-1] != '\n' {
		return res, mdfile.ErrPayloadNotLineTerminated
	}
	return s.mutateGuarded(spec.Rel, spec.ExpectedHash,
		func(f File, _ *mdfile.Doc) ([]byte, error) {
			return mdfile.ReplaceCandidateSection(
				f.Bytes, spec.Candidate, spec.Section, spec.Content)
		})
}
