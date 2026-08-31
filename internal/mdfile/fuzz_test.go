package mdfile

// 两条 fuzz 属性（施工索引 §16.5；Go 原生 fuzz，stdlib，零依赖）。
//
// 它们是「Go 生态无 ruamel 式保真兜底库」的替代防线，**不得以单测通过为由省略**：
//   - FuzzRoundTrip：任意字节输入，Parse 成功后 Render() 与输入**逐字相等**
//     （解析失败则直接 return，不参与断言，也不产出任何输出）。
//   - FuzzAppendPreserves：在任意合法文档的目标分区追加一段字节后，
//     追加点之外的**全部字节保持不变**（含 frontmatter 与「用户补充」）。
//
// 语料沉淀在 internal/mdfile/testdata/fuzz/ 作为回归资产（入库，不进 .gitignore）。

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func fuzzSeeds(tb testing.TB) [][]byte {
	tb.Helper()
	var out [][]byte
	for _, name := range []string{"card.md", "note.md", "source.md", "unprocessed.md"} {
		raw, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			tb.Fatal(err)
		}
		out = append(out, raw)
	}
	out = append(out,
		[]byte(""),
		[]byte("---\n"),
		[]byte("---\nid: k-1\n---\n"),
		[]byte("## 知识内容\n"),
		[]byte("---\nid: k-1\n---\n\n## 知识内容\n\n```\n## 假分区\n```\n\n## 用户补充\n\nu\n"),
		[]byte{0xff, 0xfe, '\n', '#', '#', ' ', 'x', '\n'},
	)
	return out
}

// FuzzRoundTrip：Parse → Render 与输入字节相等。
func FuzzRoundTrip(f *testing.F) {
	for _, seed := range fuzzSeeds(f) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		d, err := Parse(raw)
		if err != nil {
			return // 非法输入：明确返回解析错误，且不产出任何输出
		}
		got := d.Render()
		if !bytes.Equal(got, raw) {
			t.Fatalf("round-trip 非字节相等：\n输入 %d 字节 %q\n输出 %d 字节 %q",
				len(raw), raw, len(got), got)
		}
		// 索引自洽：区间半开、递增、覆盖到 EOF。
		prev := d.BodyFrom
		for _, s := range d.Sections {
			if !(s.Start >= prev && s.Body >= s.Start && s.End >= s.Body && s.End <= len(raw)) {
				t.Fatalf("分区区间不自洽：%+v（prev=%d, len=%d）", s, prev, len(raw))
			}
			prev = s.End
		}
		if len(d.Sections) > 0 && d.Sections[len(d.Sections)-1].End != len(raw) {
			t.Fatalf("末个分区必须覆盖到 EOF：%+v", d.Sections[len(d.Sections)-1])
		}
	})
}

// FuzzAppendPreserves：追加点之外的全部字节保持不变。
func FuzzAppendPreserves(f *testing.F) {
	for _, seed := range fuzzSeeds(f) {
		f.Add(seed, []byte("\n追加的一段。\n"))
	}
	f.Fuzz(func(t *testing.T, raw, payload []byte) {
		d, err := Parse(raw)
		if err != nil {
			return
		}
		if !d.HasFM {
			return
		}
		target := SecRationale
		if _, ok := d.Section(target); !ok {
			return
		}
		user, hasUser := d.Section(SecUserAppend)
		if len(payload) == 0 || payload[len(payload)-1] != '\n' {
			payload = append(append([]byte{}, payload...), '\n')
		}
		at, err := d.AppendPoint(target)
		if err != nil {
			t.Fatalf("目标分区存在却取不到插入点：%v", err)
		}
		out, err := d.AppendToSection(target, payload)
		if err != nil {
			t.Fatalf("追加失败：%v", err)
		}
		if len(out) < len(raw) {
			t.Fatalf("只增不减：%d → %d", len(raw), len(out))
		}
		if !bytes.Equal(out[:d.FMEnd], raw[:d.FMEnd]) {
			t.Fatal("frontmatter 区间必须逐字不变")
		}
		if !bytes.Equal(out[:at], raw[:at]) {
			t.Fatal("追加点之前必须逐字不变")
		}
		if !bytes.Equal(out[at:at+len(payload)], payload) {
			t.Fatal("载荷必须逐字插入")
		}
		if !bytes.Equal(out[at+len(payload):], raw[at:]) {
			t.Fatal("追加点之后必须逐字不变")
		}
		if hasUser && !bytes.Contains(out, raw[user.Start:user.End]) {
			t.Fatal("「用户补充」分区必须逐字保留（安全底线 B2）")
		}
		// 追加结果自身仍须通过写前字节自检。
		if err := SelfCheck(out); err != nil {
			t.Fatalf("追加结果自检失败：%v", err)
		}
	})
}
