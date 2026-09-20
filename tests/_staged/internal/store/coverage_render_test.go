package store

// coverage_render_test.go —— T-evergreen.knowledge_opinion_split-158614-012 · T12-4
// 「覆盖矩阵接入 store 渲染 + Knowledge/Opinion 前缀不变」的**先红**判据
// （Schema v2 契约 §4.2.3 / §5.1）。
//
// NoteExtraction 扩展 Coverage 承载覆盖矩阵：渲染时在既有 Knowledge、Opinion 两组清单**之后**
// 追加覆盖矩阵（writer 与 parser 共用 mdfile 的单一编码 / 显示函数）。本文件锁死：
//   - 有 Coverage 时，Bytes() 的字节以「Knowledge/Opinion 清单」原样开头（前缀逐字不变），
//     其后接覆盖矩阵，且矩阵块能被 mdfile.ParseCoverageMatrix 逐字读回；
//   - 无 Coverage 时，Bytes() 与既有 ExtractionList 完全一致（旧路径一字节不变）；
//   - 只有 Coverage、没有清单时，Bytes() 直接以覆盖矩阵开头；
//   - Coverage 形态非法时 Bytes() 返回 error（writer fail closed，不静默丢矩阵）。

import (
	"bytes"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

func covExt() *NoteExtraction {
	return &NoteExtraction{
		Knowledge: []string{"k-20260901-cov（新建）"},
		Opinions:  []string{"o-20260901-cov（新建） `[pending]`"},
		Coverage: []ExtractionCoverage{
			{Module: "材料方法", SourceRefs: []string{"L1-L2"}, Summary: "前两行讲方法",
				Disposition: "outputs", Outputs: []string{"k-20260901-cov"}},
			{Module: "核心观点", SourceRefs: []string{"L3-L4"}, Summary: "后两行给观点",
				Disposition: "note_only", Reason: "暂不产出卡"},
		},
	}
}

func TestCoverageExtractionBytesAppendsMatrix(t *testing.T) {
	ext := covExt()
	got, err := ext.Bytes()
	if err != nil {
		t.Fatalf("渲染提取结果出错：%v", err)
	}
	prefix := ExtractionList(ext.Knowledge, ext.Opinions)
	if !bytes.HasPrefix(got, prefix) {
		t.Fatalf("Knowledge/Opinion 前缀被改动：\n--- got ---\n%s\n--- prefix ---\n%s", got, prefix)
	}
	mi := bytes.Index(got, []byte("### 覆盖矩阵"))
	if mi < 0 {
		t.Fatalf("提取结果缺覆盖矩阵：\n%s", got)
	}
	if mi < len(prefix) {
		t.Fatalf("覆盖矩阵未接在 Knowledge/Opinion 清单之后：\n%s", got)
	}
	matrix := got[mi:]
	covs, err := mdfile.ParseCoverageMatrix(matrix)
	if err != nil {
		t.Fatalf("覆盖矩阵不能被窄 parser 逐字读回：%v\n%s", err, matrix)
	}
	if len(covs) != 2 || covs[0].Module != "材料方法" || covs[1].Module != "核心观点" {
		t.Fatalf("覆盖矩阵读回的模块 / 顺序不对：%+v", covs)
	}
	if covs[0].Disposition != "outputs" || !eqSS(covs[0].Outputs, []string{"k-20260901-cov"}) {
		t.Fatalf("outputs 处置读回错误：%+v", covs[0])
	}
	if covs[1].Disposition != "note_only" || covs[1].Reason != "暂不产出卡" {
		t.Fatalf("note_only 处置读回错误：%+v", covs[1])
	}
}

func TestCoverageExtractionBytesNoCoverageUnchanged(t *testing.T) {
	ext := &NoteExtraction{
		Knowledge: []string{"k-a（新建）"},
		Opinions:  []string{"o-b（新建） `[pending]`"},
	}
	got, err := ext.Bytes()
	if err != nil {
		t.Fatalf("渲染出错：%v", err)
	}
	want := ExtractionList(ext.Knowledge, ext.Opinions)
	if !bytes.Equal(got, want) {
		t.Fatalf("无 Coverage 时字节应与 ExtractionList 完全一致：\n got=%q\nwant=%q", got, want)
	}
	if bytes.Contains(got, []byte("覆盖矩阵")) {
		t.Fatalf("无 Coverage 时不得渲染覆盖矩阵：\n%s", got)
	}
}

func TestCoverageExtractionBytesOnlyCoverage(t *testing.T) {
	ext := &NoteExtraction{
		Coverage: []ExtractionCoverage{
			{Module: "唯一模块", SourceRefs: []string{"L1-L1"}, Summary: "只有矩阵",
				Disposition: "outputs", Outputs: []string{"k-a"}},
		},
	}
	got, err := ext.Bytes()
	if err != nil {
		t.Fatalf("渲染出错：%v", err)
	}
	if !bytes.HasPrefix(got, []byte("### 覆盖矩阵")) {
		t.Fatalf("只有 Coverage 时应直接以覆盖矩阵开头：\n%s", got)
	}
}

func TestCoverageExtractionBytesRejectsInvalid(t *testing.T) {
	ext := &NoteExtraction{
		Coverage: []ExtractionCoverage{
			{Module: "坏模块", SourceRefs: []string{"L1-L1"}, Summary: "x",
				Disposition: "archived"},
		},
	}
	if _, err := ext.Bytes(); err == nil {
		t.Fatal("Coverage 形态非法时 Bytes() 必须报错，不得静默丢矩阵")
	}
}

func eqSS(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
