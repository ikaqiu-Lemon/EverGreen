// internal/query/queryset 的固定查询集语义保护（system_assurance 批次 A · I-…-001）。
//
// 本文件是「`"sqlite"` 是数据而不是索引实现」这句话的**正向证据**：它把 M5 合同 §7.3
// 冻结的采样口径变成机器判据 —— 查询集固定、恰 20 个、确定性、零依赖、语料映射可复算。
//
// 为什么必须有它：`test/e2e/m2_acceptance_test.go` 的越界符号判据已从「按文件名具名豁免」
// 改成「按依赖闭包 + 出现形态判定」（见 index_symbol_judge_test.go）。那条判据成立的前提
// 正是本包**保持**纯数据包形态：一旦本包 import 了 os / database/sql / 本仓 internal 包，
// 或把关键词改成随机 / 读时钟，本文件与那条判据会**同时**翻红，而不是静默变绿。
package queryset

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestKeywordSetIsFixedAndClosed：查询集固定、封闭、去重、确定性。
func TestKeywordSetIsFixedAndClosed(t *testing.T) {
	kw := Keywords()
	if len(kw) != KeywordCount {
		t.Fatalf("Keywords() 有 %d 个，KeywordCount = %d：采样集漂移", len(kw), KeywordCount)
	}
	if KeywordCount != 20 {
		t.Fatalf("KeywordCount = %d，合同 §7.3 冻结为 20", KeywordCount)
	}
	seen := map[string]bool{}
	ascii := 0
	for i, k := range kw {
		if strings.TrimSpace(k) == "" {
			t.Fatalf("Keywords()[%d] 为空", i)
		}
		if seen[k] {
			t.Fatalf("Keywords() 出现重复词 %q：命中数不再可复算", k)
		}
		seen[k] = true
		if isASCIIWord(k) {
			ascii++
		}
	}
	if ascii != 8 {
		t.Fatalf("ASCII 词（D0 召回路）%d 个，合同冻结为 8", ascii)
	}
	// `sqlite` 是 D0 采样集里的**关键词数据**（被检索的词），不是索引实现符号。
	// 这条断言与越界符号判据互为反证：删掉它等于偷偷改采样口径。
	if !seen["sqlite"] {
		t.Fatal("固定查询集缺少关键词 \"sqlite\"：采样口径被改（合同 §7.3 冻结不得改字）")
	}
	// 确定性：两次调用逐字相等（无随机、无时钟、无环境依赖）。
	if !reflect.DeepEqual(kw, Keywords()) {
		t.Fatal("Keywords() 两次调用结果不同：采样集不再确定")
	}
}

// TestCorpusMappingIsRecomputable：语料 ↔ 查询集的映射可复算，命中数不为 0 也不是全库。
func TestCorpusMappingIsRecomputable(t *testing.T) {
	if got := CardID(0); got != "k-"+CorpusDate+"-"+CorpusSlugPrefix+"-00000" {
		t.Fatalf("CardID(0) = %q：语料 id 规则变了", got)
	}
	const cards = 10000
	hitCount := map[string]int{}
	for i := 0; i < cards; i++ {
		hitCount[KeywordForCard(i)]++
	}
	if len(hitCount) != KeywordCount {
		t.Fatalf("10,000 卡只覆盖 %d 个关键词，应为 %d：会有词零命中（假性变快）", len(hitCount), KeywordCount)
	}
	want := ExpectedHits(cards)
	if want != cards/KeywordCount {
		t.Fatalf("ExpectedHits(%d) = %d，与 cards/KeywordCount = %d 不一致", cards, want, cards/KeywordCount)
	}
	for k, n := range hitCount {
		if n != want {
			t.Fatalf("关键词 %q 命中 %d 条，理论值 %d：语料与采样集漂移", k, n, want)
		}
	}
	ids := SampleIDs(cards)
	if len(ids) != IDSampleCount {
		t.Fatalf("SampleIDs(%d) 返回 %d 个，应为 %d", cards, len(ids), IDSampleCount)
	}
	if !reflect.DeepEqual(ids, SampleIDs(cards)) {
		t.Fatal("SampleIDs 两次调用结果不同：id 采样不再确定")
	}
	if ids[0] == ids[len(ids)-1] {
		t.Fatal("SampleIDs 首尾相同：采样没有铺开 id 空间")
	}
}

// TestPackageStaysPureData 把「本包零依赖、无副作用能力」这条行为事实变成判据。
//
// 这正是 `TestNoOutOfScopeImplementation` 行为判据所依赖的前提：本包只 import
// 纯计算标准库，且不出现 `.index/` 路径 token。任何一条被破坏都必须让本测试翻红。
func TestPackageStaysPureData(t *testing.T) {
	allowed := map[string]bool{"fmt": true, "strconv": true}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir：%v", err)
	}
	fset := token.NewFileSet()
	files := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		body, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatalf("read %s：%v", name, rerr)
		}
		if strings.Contains(string(body), ".index/") {
			t.Fatalf("%s 出现索引路径 token \".index/\"：数据包不得引用索引落盘面", name)
		}
		af, perr := parser.ParseFile(fset, filepath.Base(name), body, parser.ImportsOnly)
		if perr != nil {
			t.Fatalf("parse %s：%v", name, perr)
		}
		for _, spec := range af.Imports {
			path, uerr := strconv.Unquote(spec.Path.Value)
			if uerr != nil {
				t.Fatalf("%s import 字面量不可解析：%s", name, spec.Path.Value)
			}
			if !allowed[path] {
				t.Fatalf("%s import 了 %q：本包必须保持零依赖纯数据（否则 sqlite 字面量不再可证为数据）",
					name, path)
			}
		}
	}
	if files == 0 {
		t.Fatal("包目录下没有非测试源：本测试的判据对象消失了")
	}
}

func isASCIIWord(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}
