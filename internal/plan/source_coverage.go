package plan

// v2 `write_note` 的**来源覆盖校验**（Schema v2 契约 §4.2 第 4/5 条 / §4.2.1）：
// 只加严 plan_version: 2 且 blocks[] 给出的 write_note，逐字段判 E2（不新增码）。
//
// 本批只做三件事，且都建立在**同一份 Source 正文快照**上（§4.2.1「一次读取、缓存复用」）：
//
//	① 形态：每个 role: source 块必须给出非空、格式严格的 source_ref；v2 blocks 必须显式
//	   给出 omissions[]（无删除也要传 []）；每条 omission 必须有 source_ref + trim 后非空 reason。
//	② 区间：source 块的 refs 按 blocks 数组顺序严格递增、不重叠；omissions 的 refs 按数组
//	   顺序严格递增、不重叠；两组区间之间也不得重复 / 交叉。
//	③ 覆盖：两组区间的并集必须让 Source 的**每个非空正文行恰覆盖一次**——非空行留空洞即 E2；
//	   空白行可不覆盖，但只覆盖空白行的范围仍占用区间、照常参与重叠判定。
//
// 行号相对 frontmatter 之后的正文物理行（从 1 起，闭区间）。末尾换行不制造虚假尾行；
// CRLF 的 \r 不算内容；围栏内的行仍是普通物理行（覆盖计数不看 Markdown 结构，那属 T12-2B）。
//
// 本文件**不**做：结构资产扫描（图片 / 代码块 / 表格）、annotation / label 语义、机器锚点，
// 以及 extraction_coverage 的语义校验（后者由 note_coverage.go 负责，T12-4 §4.2.3）——本 helper
// 只裁定来源覆盖，为覆盖矩阵校验补齐「来源范围合法」这一层背景。也**不**比较 source 块正文是否
// 逐字等于 Source 对应行——忠实翻译允许存在，「语义忠实」无法机械证明，硬比字节只会把合法的翻译整理判成错误。

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// reSourceRef 是 source_ref 的**唯一**合法形态：`L<起>-L<止>`，十进制、无前导零、闭区间。
// 与契约 §4.2 第 2 条逐字一致；任何不匹配它的写法都是字段级 E2，不做任何宽松修复。
var reSourceRef = regexp.MustCompile(`^L[1-9][0-9]*-L[1-9][0-9]*$`)

// lineSpan 是一段闭区间行段（1 基），path 记录它在诊断里的字段路径。
// blockIndex 是该行段对应的 op.Blocks 下标（仅 source 区间有意义；omission 区间为 -1）——
// T12-2B 的资产保真按来源范围逐块比对时，要靠它把「第 k 段来源范围」钉回它所属的 source 块。
type lineSpan struct {
	start      int
	end        int
	path       string
	blockIndex int
}

// noteCoverage 是一次 v2 blocks 来源覆盖校验的结果：snap 供 W21 与资产保真复用同一份 Source
// 快照；srcSpans / omSpans 是已判过形态 / 顺序 / 交叉 / 空洞的两组区间（按各自数组顺序），
// 供 T12-2B 资产保真阶段直接复用，不再二次解析 source_ref。
type noteCoverage struct {
	snap     sourceSnapshot
	srcSpans []lineSpan
	omSpans  []lineSpan
}

// sourceSnapshot 是一份 Source 正文的一次性快照（§4.2.1）：raw 供 W21 数锚点，
// body 供来源覆盖按物理行计数。两者同源，避免「同一校验两次读取」在并发落盘下漂移。
type sourceSnapshot struct {
	raw  []byte
	body []byte
}

// sourceSnapResult 是 sourceSnap 缓存的一格：snap 是快照，ok 记录该 source 是否成功取得。
// 连同失败一起缓存，才能保证「同一既有 Source 在一次 Validate 内 Env.Read 恰一次」——
// 读失败也只读一次，后续引用命中缓存直接复用同一判定，不再触发第二次 Env.Read。
type sourceSnapResult struct {
	snap sourceSnapshot
	ok   bool
}

// sourceSnapshotFor 取被引 Source 的正文快照，**按 source ID 缓存**（§4.2.1）。
//
// 首次引用某 source 时走 loadSourceSnapshot 真读一次，结果（含失败）连同 ok 一并入缓存；
// 同一份 Source 被本 plan 内多条 write_note 引用时，第二次起命中缓存、不再触碰 Env.Read
// —— 这正是「同一既有 Source 一次 Validate 内 Env.Read 总计恰一次」的落点。
func (v *validator) sourceSnapshotFor(op *Op) (sourceSnapshot, bool) {
	if hit, ok := v.sourceSnap[op.Source]; ok {
		return hit.snap, hit.ok
	}
	snap, ok := v.loadSourceSnapshot(op)
	v.sourceSnap[op.Source] = sourceSnapResult{snap: snap, ok: ok}
	return snap, ok
}

// loadSourceSnapshot 真正取一次 Source 正文（缓存未命中时由 sourceSnapshotFor 调用）。
//
// 次序不可换：本 plan 内由 add_source 新建的原文，此刻还没落盘。它的快照不能直接用 add_source
// 的 op.Body 原样字节，而要用 store.PersistedSourceBody 把 op.Body 映射成它**落盘后**再取回的
// 正文布局（前导空白物理行 + writer 补尾换行）—— 否则同一份原文「当次按 op.Body 算 L1、
// 落盘后重处理算 L2」会整体漂移一行（§4.2.1）。既有原文走 resolve + Env.Read 读一次，再由
// store.SourceBody 剥掉 frontmatter 取正文（frontmatter 边界口径与 W21 的锚点口径同源，见
// store.SourceBody）。读不到或结构不可解析返回 ok=false，调用方据此判 E2（无法取得 / 解析
// Source 正文即阻止 v2 blocks 落盘）。
func (v *validator) loadSourceSnapshot(op *Op) (sourceSnapshot, bool) {
	if body, ok := v.pendingSourceBody[op.Source]; ok {
		persisted := store.PersistedSourceBody(body)
		return sourceSnapshot{raw: persisted, body: persisted}, true
	}
	rel, ok := v.resolve(op.Source)
	if !ok {
		return sourceSnapshot{}, false
	}
	raw, ok := v.readExisting(rel)
	if !ok {
		return sourceSnapshot{}, false
	}
	body, err := store.SourceBody(raw)
	if err != nil {
		return sourceSnapshot{}, false
	}
	return sourceSnapshot{raw: raw, body: body}, true
}

// noteSourceValidate 是 v2 blocks 的来源覆盖闸门（§4.2 第 4/5 条）。
//
// 返回的 noteCoverage.snap 供调用方复用给 W21 与资产保真；ok=false 表示已登记至少一条 E2、
// 整条 op 零写入。返回的 srcSpans / omSpans 是已判过形态与顺序的两组区间，供 T12-2B 资产保真
// 直接复用。逐段短路：形态错误（缺 ref / 格式 / 越界 / 缺 reason）先各自钉到字段级路径；只有
// 形态全过才谈区间顺序，顺序全过才谈两组交叉，最后才谈非空行是否留下空洞——把「写错了字」与
// 「区间没排好」与「漏覆盖」分层报，读的人一眼就知道该改哪一层，而不是一次收到一堆互相掩盖的错误。
func (v *validator) noteSourceValidate(op *Op) (noteCoverage, bool) {
	snap, ok := v.sourceSnapshotFor(op)
	if !ok {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "source"),
			"无法取得 / 解析 source %s 的正文：v2 blocks 的 source_ref 覆盖校验无从进行，"+
				"整条 write_note 零写入（契约 §4.2.1）", op.Source))
		return noteCoverage{snap: snap}, false
	}
	lineCount, blank := bodyPhysicalLines(snap.body)

	failed := false
	// v2 blocks 必须**显式**给 omissions（无删除传 []）：缺该字段即 E2。空数组是合法的
	// 「本次无删除」声明，由 OmissionsGiven 与「值为空」区分——前者是漏写，后者是明示。
	if !op.OmissionsGiven {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "omissions"),
			"v2 的 write_note 必须显式给出 omissions[]（本次无删除也要传 []）：省略它无法区分"+
				"「确实一字未删」与「漏声明了删除」，而覆盖并集恰恰依赖这条声明才能闭合（契约 §4.2.1）"))
		failed = true
	}

	srcSpans, srcFail, srcOrder := v.parseSourceSpans(op, lineCount)
	omSpans, omFail, omOrder := v.parseOmissionSpans(op, lineCount)
	cov := noteCoverage{snap: snap, srcSpans: srcSpans, omSpans: omSpans}
	if failed || srcFail || omFail {
		return cov, false
	}
	if srcOrder || omOrder {
		return cov, false
	}
	if v.crossGroupOverlap(op, srcSpans, omSpans) {
		return cov, false
	}
	if v.coverageHole(op, srcSpans, omSpans, lineCount, blank) {
		return cov, false
	}
	return cov, true
}

// parseSourceSpans 解析 source 块的 source_ref，判形态并校验组内严格递增不重叠。
//
// 返回 (spans, formErr, orderErr)：formErr 表示至少一个 ref 缺失 / 格式 / 逆序 / 越界；
// orderErr 表示组内相邻区间未严格递增（后一段起点未越过前一段终点）。两者分开返回，
// 让调用方先把形态问题报完，再决定是否进入顺序 / 覆盖判定。
func (v *validator) parseSourceSpans(op *Op, lineCount int) (spans []lineSpan, formErr, orderErr bool) {
	prevEnd := 0
	for i, b := range op.Blocks {
		if b.Role != NoteBlockSource {
			continue
		}
		path := blockPath(op.Index, i, "source_ref")
		if strings.TrimSpace(b.SourceRef) == "" {
			v.add(errorAt(E2, op.Index, path,
				"role: %s 的块必须给出非空 source_ref：Note 的每段来源正文都要能回指 Source 的行段"+
					"（契约 §4.2 第 2 条）", NoteBlockSource))
			formErr = true
			continue
		}
		sp, msg := parseSourceRef(b.SourceRef, lineCount)
		if msg != "" {
			v.add(errorAt(E2, op.Index, path, "%s", msg))
			formErr = true
			continue
		}
		sp.path = path
		sp.blockIndex = i
		if sp.start <= prevEnd {
			v.add(errorAt(E2, op.Index, path,
				"source 块的 source_ref 必须按 blocks 顺序严格递增且不重叠：本段起点 L%d 未越过"+
					"前一段终点 L%d（契约 §4.2 第 4 条）", sp.start, prevEnd))
			orderErr = true
		}
		prevEnd = sp.end
		spans = append(spans, sp)
	}
	return spans, formErr, orderErr
}

// parseOmissionSpans 解析 omissions[]，判形态（缺 ref / 空 reason / 格式 / 越界）并校验
// 组内严格递增不重叠。返回口径同 parseSourceSpans。
func (v *validator) parseOmissionSpans(op *Op, lineCount int) (spans []lineSpan, formErr, orderErr bool) {
	prevEnd := 0
	for i, o := range op.Omissions {
		refPath := omissionPath(op.Index, i, "source_ref")
		reasonPath := omissionPath(op.Index, i, "reason")
		item := true
		if strings.TrimSpace(o.SourceRef) == "" {
			v.add(errorAt(E2, op.Index, refPath,
				"omission 必须给出非空 source_ref：被删的页面噪声也要标出它在 Source 的行段"+
					"（契约 §4.2.1）"))
			formErr, item = true, false
		}
		if strings.TrimSpace(o.Reason) == "" {
			v.add(errorAt(E2, op.Index, reasonPath,
				"omission 必须给出 trim 后非空的 reason：如实记下这段为什么被判为可删的页面噪声"+
					"（只机械要求非空，不对理由做语义 allowlist；契约 §4.2.1）"))
			formErr, item = true, false
		}
		if !item {
			continue
		}
		sp, msg := parseSourceRef(o.SourceRef, lineCount)
		if msg != "" {
			v.add(errorAt(E2, op.Index, refPath, "%s", msg))
			formErr = true
			continue
		}
		sp.path = refPath
		sp.blockIndex = -1
		if sp.start <= prevEnd {
			v.add(errorAt(E2, op.Index, refPath,
				"omissions 的 source_ref 必须按数组顺序严格递增且不重叠：本项起点 L%d 未越过"+
					"前一项终点 L%d（契约 §4.2.1）", sp.start, prevEnd))
			orderErr = true
		}
		prevEnd = sp.end
		spans = append(spans, sp)
	}
	return spans, formErr, orderErr
}

// crossGroupOverlap 判两组区间之间是否重复 / 交叉。交叉必然浪费 —— 同一段 Source 既被
// 声明为「整理进 Note 的正文」又被声明为「删掉的噪声」，两者只能取一。诊断钉在**起点较大**
// 的那段上（读的人据此知道是哪一段闯进了已被占用的行段）。任一交叉即返回 true。
func (v *validator) crossGroupOverlap(op *Op, src, om []lineSpan) bool {
	found := false
	for _, s := range src {
		for _, o := range om {
			if s.start > o.end || o.start > s.end {
				continue // 不相交
			}
			target := o
			if s.start > o.start {
				target = s
			}
			v.add(errorAt(E2, op.Index, target.path,
				"source 区间与 omission 区间交叉 / 重复（L%d-L%d 与 L%d-L%d）：同一段 Source 行段"+
					"不能既整理进正文又标为删除，二者只能取一（契约 §4.2 第 4 条）",
				s.start, s.end, o.start, o.end))
			found = true
		}
	}
	return found
}

// coverageHole 判两组区间的并集是否漏掉了某个非空正文行（契约 §4.2 第 5 条）。
//
// 前置：组内与跨组均已确认不重叠，因此「每个非空行恰覆盖一次」在此退化为「每个非空行至少
// 覆盖一次」。空白行（去掉 CRLF 的 \r 后 trim 为空）可不覆盖；命中第一处空洞即报并返回 true
// （一次报一处最先出现的漏覆盖行，避免把同一批漏覆盖刷成满屏）。
func (v *validator) coverageHole(op *Op, src, om []lineSpan, lineCount int, blank map[int]bool) bool {
	for ln := 1; ln <= lineCount; ln++ {
		if blank[ln] {
			continue
		}
		if spansCover(ln, src) || spansCover(ln, om) {
			continue
		}
		v.add(errorAt(E2, op.Index, opPath(op.Index, "blocks"),
			"Source 第 %d 行是非空正文行却未被任何 source_ref / omission 区间覆盖：两组区间的并集"+
				"必须让每个非空正文行恰覆盖一次（契约 §4.2 第 5 条）——要么整理进正文，要么标为删除",
			ln))
		return true
	}
	return false
}

// spansCover 报告行号 ln 是否落在任一闭区间内。
func spansCover(ln int, spans []lineSpan) bool {
	for _, sp := range spans {
		if ln >= sp.start && ln <= sp.end {
			return true
		}
	}
	return false
}

// parseSourceRef 解析单个 source_ref 为闭区间行段并做形态 / 逆序 / 越界判定。
// msg 非空即为字段级 E2 的文案（调用方负责登记到对应字段路径）。
func parseSourceRef(ref string, lineCount int) (lineSpan, string) {
	if !reSourceRef.MatchString(ref) {
		return lineSpan{}, fmt.Sprintf(
			"source_ref=%q 格式非法：必须严格匹配 L<起>-L<止>（十进制、无前导零、闭区间，"+
				"行号相对 frontmatter 之后的正文）", ref)
	}
	dash := strings.IndexByte(ref, '-')
	start, _ := strconv.Atoi(ref[1:dash])
	end, _ := strconv.Atoi(ref[dash+2:])
	if start > end {
		return lineSpan{}, fmt.Sprintf(
			"source_ref=%q 逆序：起始行 L%d 大于结束行 L%d（闭区间要求 start<=end）", ref, start, end)
	}
	if end > lineCount {
		return lineSpan{}, fmt.Sprintf(
			"source_ref=%q 越界：结束行 L%d 超过 Source 正文物理行数 %d（末尾换行不制造虚假尾行）",
			ref, end, lineCount)
	}
	return lineSpan{start: start, end: end}, ""
}

// bodyPhysicalLines 把 Source 正文按物理行切开，返回行数与「blank[行号]=是否空白行」。
//
// 全程走 []byte，只用 bytes.Split / bytes.TrimSpace 在原切片上取子切片，不复制、不改写、
// 不重建正文（避免把整段正文额外分配成一份 string）。bytes.TrimSpace 采用 Unicode 空白定义，
// 因此这条空白口径是自洽的，不必先剥某个字符再判空。
//
// 口径（契约 §4.2 第 2/5 条）：
//   - 以 \n 分隔物理行；末尾 \n 不制造虚假尾行（正文以 \n 结束时，最后那个空段不算一行）；
//   - 空白行 = bytes.TrimSpace(line) 为空的行。据此：CRLF 的 \r、多个纯尾随 \r、以及各种
//     ASCII / Unicode 空白都算空白；含非法 UTF-8 的非空字节的行**不**是空白（TrimSpace 不裁它们），
//     因此不会被误判为空而漏覆盖，也不会在计数中丢失；
//   - 围栏代码块内的行仍是普通物理行（本函数不看 Markdown 结构）。
func bodyPhysicalLines(body []byte) (int, map[int]bool) {
	parts := bytes.Split(body, []byte{'\n'})
	if n := len(parts); n > 0 && len(parts[n-1]) == 0 {
		parts = parts[:n-1] // 末尾换行：丢掉它身后那个空段，不算作一行
	}
	blank := make(map[int]bool)
	for i, line := range parts {
		if len(bytes.TrimSpace(line)) == 0 {
			blank[i+1] = true
		}
	}
	return len(parts), blank
}
