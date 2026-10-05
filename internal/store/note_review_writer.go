package store

// v2 审阅式 Note 的**落盘入口**（Schema v2 契约 §4.2 第 4 条 / §4.2.2 / §5.1）。
//
// # 与 NoteBlockBytes 的分工
//
// NoteBlockBytes 是 v1 兼容期的旧落盘形态：agent 块统一渲染 `> **[Agent 补充]** `，
// 不消费 Annotation/Label/SourceRef，也不写机器锚点。它必须逐字保留——**只有**「plan_version:1
// 且 blocks[]」这一条路径走它（noteBlockWrites 在 plan 版本非 v2 时的分支）；sections{} 无论
// v1 还是 v2 都走 legacyNoteWrites 的固定分区映射、绝不经过本函数，任何字节改动都会破坏那条回归。
//
// NoteReviewBytes 保留 v2 的带锚点线格式。v3 改用 NotePlainReviewBytes 写纯 n-*，并通过
// NoteBlockManifestBytes 把 B 引用、角色和溯源字段写入配对 ns-*。三个入口都是 mdfile
// renderer 的薄适配，store 不复制线格式实现。
//
// # 为什么落盘入口在 store 而渲染实现在 mdfile
//
// §16.3 写路径硬约束要求「按模板拼字节」的落盘入口收在 store（与 NoteBlockBytes 同处一包，
// plan 只调用、不自己拼）。而 mdfile 是 store 与 plan 的共同下游包，审阅式线格式的渲染与解析
// 必须成对存在于同一处才能保证 render↔parse 互逆，因此实现落在 mdfile；store 保留对外入口。

import "github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"

func noteReviewInputs(
	blocks []NoteBlock,
	omissions []Omission,
) ([]mdfile.ReviewBlock, []mdfile.ReviewOmission) {
	rb := make([]mdfile.ReviewBlock, len(blocks))
	for i, b := range blocks {
		rb[i] = mdfile.ReviewBlock{
			Role:       string(b.Role),
			Heading:    b.Heading,
			Body:       b.Body,
			SourceRef:  b.SourceRef,
			Annotation: b.Annotation,
			Label:      b.Label,
		}
	}
	ro := make([]mdfile.ReviewOmission, len(omissions))
	for i, o := range omissions {
		ro[i] = mdfile.ReviewOmission{SourceRef: o.SourceRef, Reason: o.Reason}
	}
	return rb, ro
}

// NoteReviewBytes 把 v2 有序块与遗漏元数据渲染成带 eg:nr 锚点的兼容正文。
func NoteReviewBytes(blocks []NoteBlock, omissions []Omission) ([]byte, error) {
	rb, ro := noteReviewInputs(blocks, omissions)
	return mdfile.RenderReviewNote(rb, ro)
}

// NotePlainReviewBytes 把 v3 有序块渲染成不含 Evergreen 机器锚点的普通 Markdown。
func NotePlainReviewBytes(blocks []NoteBlock, omissions []Omission) ([]byte, error) {
	rb, ro := noteReviewInputs(blocks, omissions)
	return mdfile.RenderPlainReviewNote(rb, ro)
}

// NoteBlockManifestBytes renders the v3 block vocabulary and provenance that
// lives in ns-* instead of the editable n-* body.
func NoteBlockManifestBytes(blocks []NoteBlock, omissions []Omission) ([]byte, error) {
	rb, ro := noteReviewInputs(blocks, omissions)
	return mdfile.RenderNoteBlockManifest(rb, ro)
}
