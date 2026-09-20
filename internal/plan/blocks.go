package plan

// v2 写口的**解析层**：`write_note.blocks[]` 的反序列化与两个兼容别名的名称改写
// （Schema v2 契约 §4.2 / §4.4）。
//
// 本文件只做解析与改名，**不做任何语义判定**：block 的 role 是否越界、数组是否为空、
// 是否与 v1 `sections` 互斥、来源块数够不够（`W21`）——全部由 validate.go 的
// noteBlocks 负责。分层的理由与本包既有惯例一致：解析只回答「字段长什么样」，
// 校验才回答「这样写允不允许」，两件事混在一处会让「解析失败」与「校验不通过」
// 共用同一条 error 分支，从而无法区分「plan 坏了」与「plan 写错了」。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// NoteBlock 是 `write_note.blocks[]` 的一项。
//
// **类型别名**而不是新结构体：落盘渲染的实现都在 store（写路径硬约束 §16.3 把「按模板拼字节」
// 收在 store）——v1 兼容入口是 store.NoteBlockBytes，v2「plan_version:2 且 blocks[]」的审阅式
// 渲染是 store.NoteReviewBytes；plan 只做解析/校验，把用户给的块按 plan 版本原样传给对应入口。
// 若在此再定义一份同形结构体，就必须写一个逐字段拷贝的转换函数，
// 而那个函数是「两处结构漂移」的第一个落点：加字段的人只会改一处。
type NoteBlock = store.NoteBlock

// 块角色的两个取值（转发 store 的封闭二值枚举，契约 §4.2 第 2 条）。
const (
	NoteBlockSource = store.NoteBlockSource
	NoteBlockAgent  = store.NoteBlockAgent
)

// NoteBlockRoles 转发封闭枚举的全部取值（诊断文案与用例共用同一份）。
func NoteBlockRoles() []store.NoteBlockRole { return store.NoteBlockRoles() }

// noteBlockKnownKeys 是单个 block 的字段表（契约 §4.2 的 v2 形态）。
//
// source/agent 两类块共用同一张字段表：source 块用 source_ref 标行段、agent 块用
// annotation/label 标批注类型，解析层不按 role 分表（是否「source 块才允许 source_ref」
// 这类语义判定属后续批次的 validate，解析只负责把键读进结构体）。
func noteBlockKnownKeys() []string {
	return []string{"role", "heading", "body", "source_ref", "annotation", "label"}
}

// parseNoteBlocks 解析 `blocks[]`。
//
// 顺序**逐字保留**：本函数按下标顺序 append，不排序、不去重、不合并、不丢空块。
// 空块与越界 role 都照样进 op.Blocks —— 它们必须被 validate 看见并报 `E2`，
// 在解析期悄悄丢掉等于把一条错误 plan 改成一条能跑的 plan。
func parseNoteBlocks(opIndex int, v interface{}) ([]NoteBlock, []Diagnostic) {
	var diags []Diagnostic
	items, ok := asList(v)
	if !ok && v != nil {
		return nil, append(diags, errorAt(E5, opIndex, opPath(opIndex, "blocks"),
			"blocks 必须是有序块列表（数组顺序即落盘顺序）"))
	}
	known := set(noteBlockKnownKeys())
	out := make([]NoteBlock, 0, len(items))
	for i, item := range items {
		m, ok := asMap(item)
		if !ok {
			diags = append(diags, errorAt(E5, opIndex, blockPath(opIndex, i, ""),
				"blocks 的每一项必须是对象（恰 %v）", noteBlockKnownKeys()))
			continue
		}
		b := NoteBlock{}
		role, _ := asString(m["role"])
		b.Role = store.NoteBlockRole(role)
		b.Heading, _ = asString(m["heading"])
		b.SourceRef, _ = asString(m["source_ref"])
		b.Annotation, _ = asString(m["annotation"])
		b.Label, _ = asString(m["label"])
		if bv, ok := m["body"]; ok {
			body, _ := asString(bv)
			b.Body = []byte(body)
		}
		for k := range m {
			if known[k] {
				continue
			}
			diags = append(diags, infoAt(opIndex, blockPath(opIndex, i, k),
				"未知附加字段已原样忽略（前向兼容：blocks 的字段表恰 %v）", noteBlockKnownKeys()))
		}
		out = append(out, b)
	}
	return out, diags
}

// blockPath 是 block 级诊断的字段路径（`ops[N].blocks[M]` / `…blocks[M].key`）。
func blockPath(opIndex, blockIndex int, key string) string {
	base := fmt.Sprintf("ops[%d].blocks[%d]", opIndex, blockIndex)
	if key == "" {
		return base
	}
	return base + "." + key
}
