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
// NoteReviewBytes 是 v2「plan_version:2 且 blocks[]」的新落盘形态：每块一个版本化机器锚点、
// agent 块按 annotation/label 渲染多类型标签、omissions 以机器元数据落盘。它是 mdfile 的
// 审阅式线格式渲染器 mdfile.RenderReviewNote 的**薄适配**——把 store 侧的 NoteBlock/Omission
// 转成 mdfile 的原生输入类型即可，渲染 / 解析 / 锚点编解码的单一真源都在 mdfile，store 不再
// 抄一份线格式。plan 侧在 v2 BlocksGiven 且完成校验后调用本函数拿字节。
//
// # 为什么落盘入口在 store 而渲染实现在 mdfile
//
// §16.3 写路径硬约束要求「按模板拼字节」的落盘入口收在 store（与 NoteBlockBytes 同处一包，
// plan 只调用、不自己拼）。而 mdfile 是 store 与 plan 的共同下游包，审阅式线格式的渲染与解析
// 必须成对存在于同一处才能保证 render↔parse 互逆，因此实现落在 mdfile；store 保留对外入口。

import "github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"

// NoteReviewBytes 把 v2 有序块与遗漏元数据渲染成「整理正文」分区正文字节。
//
// 入参 blocks 的字段语义与 NoteBlock 一致（source 块用 SourceRef；agent 块用 Annotation/
// Label）；omissions 逐条落成机器元数据锚点。字段级合法性（source/agent 字段互斥、annotation
// 非空且合法、内置 key 不得被 label 覆盖）由 plan 侧在调用前判净：本函数只做渲染，遇到非法
// 组合（如 agent 的 annotation 无法解析出显示标签）直接返回 error，绝不编造标签。
func NoteReviewBytes(blocks []NoteBlock, omissions []Omission) ([]byte, error) {
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
	return mdfile.RenderReviewNote(rb, ro)
}
