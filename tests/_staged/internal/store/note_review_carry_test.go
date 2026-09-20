package store

// note_review_carry_test.go —— T-evergreen.knowledge_opinion_split-158614-012 · T12-1
// store 侧「解析承载」判据：新增的 Note 元数据字段与两个覆盖数据结构，字段可编译、可回读，
// 字段命名逐字对齐 Schema v2 契约（§4.2 / §4.2.1 / §4.2.3）。
//
// 本文件**只做结构体字段的编译 + 回读锚点**：字段一旦被改名/删除，测试连编译都过不了。
//
// 刻意**不**断言「NoteBlockBytes 忽略新字段」这类持久不变量：那会把「永远不渲染批注」
// 写死，与后续批次（按 annotation 渲染、source_ref round-trip）的最终合同冲突。
// 「本批未改旧落盘输出」由既有 note_blocks_test.go 的渲染用例保证，无需在此重复锁死。

import "testing"

// TestReviewCarryStructsExposeContractFields 锚定三处解析承载字段的存在与回读：
// NoteBlock.{SourceRef,Annotation,Label} 与 Omission / ExtractionCoverage 的契约字段。
func TestReviewCarryStructsExposeContractFields(t *testing.T) {
	// NoteBlock 的审阅式 Note 元数据字段（§4.2）。
	nb := NoteBlock{
		Role:       NoteBlockAgent,
		Body:       []byte("辨析批注。"),
		SourceRef:  "L14-L28",
		Annotation: "custom_note",
		Label:      "我的自定义标签",
	}
	if nb.SourceRef != "L14-L28" || nb.Annotation != "custom_note" || nb.Label != "我的自定义标签" {
		t.Fatalf("NoteBlock 元数据字段回读错误：%+v", nb)
	}

	// Omission 字段（§4.2.1）。
	om := Omission{SourceRef: "L52-L55", Reason: "页脚导航噪声"}
	if om.SourceRef != "L52-L55" || om.Reason != "页脚导航噪声" {
		t.Fatalf("Omission 字段回读错误：%+v", om)
	}

	// ExtractionCoverage 字段（§4.2.3）。
	cov := ExtractionCoverage{
		Module:      "m-001",
		SourceRefs:  []string{"L14-L28", "L29-L51"},
		Summary:     "方法三步",
		Disposition: "outputs",
		Outputs:     []string{"k-a", "o-b"},
		Reason:      "",
	}
	if cov.Module != "m-001" || len(cov.SourceRefs) != 2 ||
		cov.Summary != "方法三步" || cov.Disposition != "outputs" ||
		len(cov.Outputs) != 2 {
		t.Fatalf("ExtractionCoverage 字段回读错误：%+v", cov)
	}
}
