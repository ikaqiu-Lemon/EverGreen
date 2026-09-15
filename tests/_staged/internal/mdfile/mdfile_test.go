package mdfile

// T-evergreen.s1_main_flow-158614-005 Acceptance 的机器判据。

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// —— ① round-trip 字节级相等 ——

func TestRoundTripByteIdentical(t *testing.T) {
	for _, name := range []string{"card.md", "note.md", "source.md", "unprocessed.md"} {
		raw := read(t, name)
		d, err := Parse(raw)
		if err != nil {
			t.Fatalf("%s 解析失败：%v", name, err)
		}
		got := d.Render()
		if !bytes.Equal(got, raw) {
			t.Fatalf("%s：Parse→Render 非字节相等\n--- 期望 %d 字节 ---\n%q\n--- 实得 %d 字节 ---\n%q",
				name, len(raw), raw, len(got), got)
		}
		if err := SelfCheck(raw); err != nil {
			t.Fatalf("%s 写前字节自检失败：%v", name, err)
		}
	}
}

func TestRoundTripKeepsUnknownFieldsSectionsAndBlocks(t *testing.T) {
	raw := read(t, "card.md")
	d, card, err := ParseCard(raw)
	if err != nil {
		t.Fatal(err)
	}
	// 未知 frontmatter 字段：进 Extra，原样保留在 Raw 里。
	for _, key := range []string{"unknown_key", "anchor_demo", "alias_demo"} {
		if _, ok := card.Extra[key]; !ok {
			t.Fatalf("未知 frontmatter 字段 %q 应落进 Extra：%v", key, card.Extra)
		}
	}
	// 引号风格 / 折叠标量 / 锚点别名 / 行尾注释：因不经过序列化器而逐字保留。
	for _, lit := range []string{
		`reason: "背景：检索场景差异"`, "reason: |", "&a 复用值", "alias_demo: *a",
		"# 行尾注释也要逐字保留", "tags: [rag, chunking]",
	} {
		if !bytes.Contains(d.Render(), []byte(lit)) {
			t.Fatalf("渲染结果丢失原始写法 %q", lit)
		}
	}
	// 非固定分区原样保留、不重排。card.md 是一份 **v1 存量卡**，因此 Schema v2 下
	// 非固定分区恰有三个：被移除的 `解释与依据` / `理解自检`（契约 D-7，只记 info、
	// 待迁移收口）与用户自建的第六个分区。顺序按文档序，不重排。
	var unknownNames []string
	for _, sp := range d.UnknownSections(KindCard) {
		unknownNames = append(unknownNames, sp.Name)
	}
	wantUnknown := []string{SecRationale, SecSelfCheck, "用户自建的第六个分区"}
	if strings.Join(unknownNames, ",") != strings.Join(wantUnknown, ",") {
		t.Fatalf("非固定分区应恰为 %v（文档序），实际 %v（全部分区 %v）",
			wantUnknown, unknownNames, d.SectionNames())
	}
	// 存量卡按 v1 模板校验（否则每份存量卡都会被误判成结构错误），
	// 但被移除的分区仍属「非固定分区」——这两处口径必须同时成立。
	if got := d.SectionSchema(KindCard); got != SchemaV1 {
		t.Fatalf("含 v1 专有分区的存量卡应判为 v1 模板，实际 %v", got)
	}
	if err := d.ValidateSections(KindCard); err != nil {
		t.Fatalf("v1 存量卡的结构校验必须通过：%v", err)
	}
	// v2 固定三分区的相对顺序不变。
	var fixed []string
	for _, n := range d.SectionNames() {
		for _, k := range CardSections() {
			if n == k {
				fixed = append(fixed, n)
			}
		}
	}
	if strings.Join(fixed, ",") != strings.Join(CardSections(), ",") {
		t.Fatalf("固定分区顺序 = %v，期望 %v", fixed, CardSections())
	}
	// 尾随空行保留。
	if !bytes.HasSuffix(d.Render(), []byte("\n\n\n")) {
		t.Fatal("原有尾随空行必须保留")
	}
}

// —— ② 分区改名即解析失败，错误信息给出期望分区名与位置 ——

func TestSectionRenameFailsWithExpectedNameAndPosition(t *testing.T) {
	raw := bytes.Replace(read(t, "note.md"), []byte("## 材料提炼"), []byte("## 提炼"), 1)
	_, _, err := ParseNote(raw)
	if err == nil {
		t.Fatal("把「材料提炼」改名成「提炼」必须解析失败")
	}
	var se *SectionError
	if !errors.As(err, &se) {
		t.Fatalf("错误类型应为 *SectionError，实际 %T", err)
	}
	msg := err.Error()
	for _, want := range []string{"材料提炼", "提炼", "偏移", "第 ", "行"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误信息缺 %q：%s", want, msg)
		}
	}
	if se.Expected != SecDigest || se.Offset <= 0 || se.Line <= 1 {
		t.Fatalf("错误定位异常：%+v", se)
	}

	// 顺序颠倒同样报错。
	swapped := []byte("---\nid: k-1\n---\n\n## 解释与依据\n\nx\n\n## 知识内容\n\ny\n")
	if _, _, err := ParseCard(swapped); err == nil {
		t.Fatal("固定分区顺序颠倒必须报错")
	}
	// 重复出现同样报错。
	dup := []byte("---\nid: k-1\n---\n\n## 知识内容\n\nx\n\n## 知识内容\n\ny\n")
	if _, _, err := ParseCard(dup); err == nil {
		t.Fatal("固定分区重复必须报错")
	}
	// 缺失（非必需）分区按空处理，不报错。
	sparse := []byte("---\nid: k-1\n---\n\n## 知识内容\n\nx\n")
	if _, _, err := ParseCard(sparse); err != nil {
		t.Fatalf("缺失非必需分区应按空处理，实际报错：%v", err)
	}
}

// —— ③ 五种块型切分 ——

func TestBlockSplittingFiveKinds(t *testing.T) {
	raw := read(t, "card.md")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := d.Blocks(SecRationale)
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []BlockKind{
		BlockListItem, BlockListItem, BlockFence, BlockTable, BlockHeading3, BlockParagraph,
	}
	if len(blocks) != len(wantKinds) {
		var got []string
		for _, b := range blocks {
			got = append(got, string(b.Kind)+":"+strings.ReplaceAll(string(b.Bytes(raw)), "\n", "⏎"))
		}
		t.Fatalf("「解释与依据」切出 %d 块，期望 %d：\n%s", len(blocks), len(wantKinds),
			strings.Join(got, "\n"))
	}
	for i, k := range wantKinds {
		if blocks[i].Kind != k {
			t.Fatalf("第 %d 块块型 = %s，期望 %s（内容 %q）",
				i, blocks[i].Kind, k, blocks[i].Bytes(raw))
		}
	}
	// 列表项的缩进子项不被切成独立块，且空行后的缩进延续行仍属同一块。
	first := string(blocks[0].Bytes(raw))
	for _, want := range []string{"缩进子项一", "缩进子项二", "空行之后仍是缩进的延续行"} {
		if !strings.Contains(first, want) {
			t.Fatalf("顶层列表项应包含 %q，实际 %q", want, first)
		}
	}
	if strings.Contains(first, "第二个顶层列表项") {
		t.Fatalf("两个顶层列表项必须是两块，实际 %q", first)
	}
	// 围栏代码块内空行不切分，围栏内的 ## 不被当作分区标题。
	fence := string(blocks[2].Bytes(raw))
	if !strings.Contains(fence, "def f():") || !strings.Contains(fence, "return 1") ||
		!strings.Contains(fence, "围栏内的这一行不是分区标题") {
		t.Fatalf("围栏块应整段一块，实际 %q", fence)
	}
	if strings.Count(fence, "```") != 2 {
		t.Fatalf("围栏块应含起止两行围栏，实际 %q", fence)
	}
	for _, n := range d.SectionNames() {
		if n == "围栏内的这一行不是分区标题" {
			t.Fatal("围栏内的 ## 不得被识别为 H2 分区")
		}
	}
	// 连续表格行是一个块；H3 自身一个块。
	if got := strings.Count(string(blocks[3].Bytes(raw)), "\n"); got != 3 {
		t.Fatalf("表格块应含 3 行，实际 %q", blocks[3].Bytes(raw))
	}
	if h3 := string(blocks[4].Bytes(raw)); h3 != "### H3 小标题\n" {
		t.Fatalf("H3 块 = %q", h3)
	}
	if p := string(blocks[5].Bytes(raw)); p != "段落跟在 H3 后面。\n" {
		t.Fatalf("段落块 = %q", p)
	}
	// 段落 = 连续非空行（材料笔记「材料提炼」两行是一块）。
	nd, err := Parse(read(t, "note.md"))
	if err != nil {
		t.Fatal(err)
	}
	nb, err := nd.Blocks(SecDigest)
	if err != nil {
		t.Fatal(err)
	}
	if len(nb) != 1 || nb[0].Kind != BlockParagraph {
		t.Fatalf("连续非空行应为一个段落块，实际 %d 块", len(nb))
	}
}

// —— ④ block_hash ——

func TestBlockHashNormalization(t *testing.T) {
	base := BlockHash([]byte("第一行\n第二行\n"))
	same := []string{
		"第一行\r\n第二行\r\n", // CRLF
		"第一行  \n第二行\t\n", // 行尾空白
		"第一行\n第二行",       // 末尾无换行
		"第一行\n第二行\n\n\n", // 尾随空行
	}
	for _, s := range same {
		if got := BlockHash([]byte(s)); got != base {
			t.Fatalf("换行 / 行尾空白差异下 hash 必须相同：%q → %s，期望 %s", s, got, base)
		}
	}
	if BlockHash([]byte("第一行\n第三行\n")) == base {
		t.Fatal("不同文本必须 hash 不同")
	}
	if len(base) != BlockHashLen || BlockHashLen != 16 {
		t.Fatalf("block_hash 长度 = %d，期望 16", len(base))
	}
	// 行内空白与非法 UTF-8 不被规范化（只用 []byte，不做 rune 重建）。
	illegal := []byte{0xff, 0xfe, '\n'}
	if got := BlockHash(illegal); len(got) != BlockHashLen {
		t.Fatalf("非法 UTF-8 也必须能算 hash，实际 %q", got)
	}
	if !bytes.Equal(NormalizeBlock(illegal), []byte{0xff, 0xfe}) {
		t.Fatalf("非法 UTF-8 字节不得被改写：%v", NormalizeBlock(illegal))
	}
}

// —— ⑤ 材料笔记没有 status，也不产出理解自检 ——

func TestNoteHasNoStatusAndNoSelfCheckSection(t *testing.T) {
	d, note, err := ParseNote(read(t, "note.md"))
	if err != nil {
		t.Fatal(err)
	}
	// 解析结果不含 status 字段（model.Note 无该字段；原文里的 status 落进 Extra 原样保留）。
	if _, ok := note.Extra["status"]; !ok {
		t.Fatalf("原文里的 status 键必须原样保留在 Extra：%v", note.Extra)
	}
	for _, n := range NoteSections() {
		if n == SecSelfCheck {
			t.Fatal("材料笔记五分区不得包含「理解自检」")
		}
	}
	for _, n := range AutoWritableSections(KindNote) {
		if n == SecSelfCheck {
			t.Fatal("材料笔记的可写分区不得包含「理解自检」")
		}
	}
	if _, ok := d.Section(SecSelfCheck); ok {
		t.Fatal("样例笔记不应含「理解自检」分区")
	}
	// 「用户补充」任何时候都不得写入。
	if _, err := d.AppendToSection(SecUserAppend, []byte("x\n")); !errors.Is(err, ErrSectionNeverWrite) {
		t.Fatalf("写「用户补充」必须被拒，实际 %v", err)
	}
}

// —— ⑥ unprocessed.md ——

func TestUnprocessedRoundTripAndKeying(t *testing.T) {
	raw := read(t, "unprocessed.md")
	u, err := ParseUnprocessed(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(u.Render(), raw) {
		t.Fatalf("收件区 round-trip 非字节相等：\n%q\n%q", raw, u.Render())
	}
	if len(u.Entries) != 2 {
		t.Fatalf("条目数 = %d，期望 2（一个顶层列表项 = 一个条目）", len(u.Entries))
	}
	e, ok := u.Find(model.SourceID("s-20260901-alt"))
	if !ok {
		t.Fatal("应能按 source_id 取到条目")
	}
	if e.Item.Title != "另一篇" || !strings.Contains(e.Item.Reason, "多行理由") ||
		e.Item.TargetDomain != model.Domain("ai-infra") {
		t.Fatalf("条目解析异常：%+v", e.Item)
	}
	if len(e.Item.MissingFields()) != 0 {
		t.Fatalf("样例条目四要素应齐全，实际缺 %v", e.Item.MissingFields())
	}
	// 追加一条：追加点之外逐字不变。
	item := []byte("- source_id: s-20260902-x\n  title: 新条目\n" +
		"  saved_at: 2026-09-02T08:00:00+08:00\n  reason: 追加\n")
	out, err := u.Append(item)
	if err != nil {
		t.Fatal(err)
	}
	at := u.Entries[len(u.Entries)-1].End
	if !bytes.Equal(out[:at], raw[:at]) || !bytes.Equal(out[at+len(item):], raw[at:]) {
		t.Fatal("追加点之外的字节必须逐字不变")
	}
	u2, err := ParseUnprocessed(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(u2.Entries) != 3 {
		t.Fatalf("追加后条目数 = %d，期望 3", len(u2.Entries))
	}
	// 重复 source_id 报错。
	dup, err := u.Append([]byte("- source_id: s-20260901-alt\n  title: 重复\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseUnprocessed(dup); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("重复 source_id 必须报错，实际 %v", err)
	}
}

// —— ⑦ 写路径：只追加、区间之外逐字不变、缩进沿用 ——

func TestAppendToSectionPreservesEverythingElse(t *testing.T) {
	raw := read(t, "card.md")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("\n新追加的一段。\n")
	out, err := d.AppendToSection(SecBoundary, payload)
	if err != nil {
		t.Fatal(err)
	}
	at, err := d.AppendPoint(SecBoundary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out[:at], raw[:at]) {
		t.Fatal("插入点之前必须逐字不变")
	}
	if !bytes.Equal(out[at:at+len(payload)], payload) {
		t.Fatal("载荷必须逐字插入")
	}
	if !bytes.Equal(out[at+len(payload):], raw[at:]) {
		t.Fatal("插入点之后必须逐字不变")
	}
	if len(out) != len(raw)+len(payload) {
		t.Fatal("追加只增不减")
	}
	// 空分区（「条件与边界」为空）的插入点是分区正文起始，原有空行保留在插入点之后。
	if s, _ := d.Section(SecBoundary); at != s.Body {
		t.Fatalf("空分区插入点 = %d，期望分区正文起始 %d", at, s.Body)
	}
	// 载荷必须以换行结束：本包不替调用方补字节。
	if _, err := d.AppendToSection(SecBoundary, []byte("无换行")); !errors.Is(err, ErrPayloadNotLineTerminated) {
		t.Fatalf("未以换行结束的载荷必须被拒，实际 %v", err)
	}
	// 不存在的分区。
	if _, err := d.AppendToSection("不存在的分区", []byte("x\n")); !errors.Is(err, ErrSectionNotFound) {
		t.Fatalf("分区不存在必须报错，实际 %v", err)
	}
	// 非空分区：插入点在最后一个非空行之后。
	at2, err := d.AppendPoint(SecKnowledge)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(raw[:at2], []byte("Chunk 粒度要按检索目标定。\n")) {
		t.Fatalf("插入点应紧跟最后一个非空行，实际前文 %q", raw[:at2])
	}
}

func TestAppendFMKeyAndSeqItem(t *testing.T) {
	raw := read(t, "card.md")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.AppendFMKey("reviewed_at", []byte("2026-09-02T10:00:00+08:00"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out[:d.FMEnd], raw[:d.FMEnd]) || !bytes.Equal(out[d.FMEnd+len("reviewed_at: 2026-09-02T10:00:00+08:00\n"):], raw[d.FMEnd:]) {
		t.Fatal("新键只能追加到 frontmatter 末尾，其余字节逐字不变")
	}
	if _, err := d.AppendFMKey("status", []byte("deprecated")); !errors.Is(err, ErrFMKeyExists) {
		t.Fatalf("已有键必须拒绝改写，实际 %v", err)
	}

	// 缩进探测：sources 用 4 空格，relations 用 2 空格，各自沿用。
	sq, err := d.FMSeq("sources")
	if err != nil {
		t.Fatal(err)
	}
	if string(sq.Indent) != "    " || sq.Items != 2 {
		t.Fatalf("sources 缩进 = %q，条目数 = %d，期望 4 空格 / 2 条", sq.Indent, sq.Items)
	}
	rq, err := d.FMSeq("relations")
	if err != nil {
		t.Fatal(err)
	}
	if string(rq.Indent) != "  " || rq.Items != 1 {
		t.Fatalf("relations 缩进 = %q，条目数 = %d，期望 2 空格 / 1 条", rq.Indent, rq.Items)
	}
	out, err = d.AppendFMSeqItem("sources", []byte("source: s-20260902-n\n      note: n-20260902-n\n      rel: against\n      reason: 新证据\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("    - source: s-20260902-n\n")) {
		t.Fatalf("追加项必须沿用既有 4 空格缩进：\n%s", out)
	}
	nd, card, err := ParseCard(out)
	if err != nil {
		t.Fatalf("追加后仍须是合法文档：%v", err)
	}
	if len(card.Sources) != 3 {
		t.Fatalf("追加后 sources 条数 = %d，期望 3", len(card.Sources))
	}
	if !bytes.Equal(nd.Render(), out) {
		t.Fatal("追加结果自身必须能通过 round-trip 自检")
	}

	// 空序列不猜缩进 → ErrNoIndentStyle（上层出 warning）。
	empty, err := Parse([]byte("---\nid: k-1\nsources:\ntags: []\n---\n\n## 知识内容\n\nx\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.FMSeq("sources"); !errors.Is(err, ErrNoIndentStyle) {
		t.Fatalf("空序列必须返回 ErrNoIndentStyle，实际 %v", err)
	}
	if _, err := empty.FMSeq("nope"); !errors.Is(err, ErrSeqKeyNotFound) {
		t.Fatalf("不存在的序列键必须报错，实际 %v", err)
	}
}

// —— ⑧ 源码级反证：无序列化回写、块定位符不进写路径 ——

func TestWritePathHasNoSerializer(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// 分片拼接，避免本文件自身成为 make lint guard 步的命中项。
	forbidden := []string{"yaml." + "Marshal", "yaml." + "NewEncoder", "yaml." + "Encoder"}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range forbidden {
			if bytes.Contains(raw, []byte(bad)) {
				t.Fatalf("%s 出现被禁的序列化 API %q", f, bad)
			}
		}
	}
	// 落盘路径只有 insert 一条：写函数一律经由它。
	appendSrc, err := os.ReadFile("append.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(appendSrc, []byte("Locator")) {
		t.Fatal("写路径不得引用块定位符（不稳定、不写入权威侧）")
	}
	if n := bytes.Count(appendSrc, []byte("func insert(")); n != 1 {
		t.Fatalf("落盘字节构造入口应唯一，实际 %d 个", n)
	}
}

func TestLocatorOnlyForDiagnostics(t *testing.T) {
	loc := Locator("k-20260901-chunk-size", SecRationale, 2)
	if loc != "k-20260901-chunk-size#解释与依据#2" {
		t.Fatalf("块定位符形态 = %q", loc)
	}
	raw := read(t, "card.md")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.AppendToSection(SecSelfCheck, []byte("- [ ] 新自检项\n"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(loc)) || bytes.Contains(out, []byte("#解释与依据#")) {
		t.Fatal("块定位符不得出现在任何写回文件的内容中")
	}
}

func TestParseErrorsOnUnterminatedFrontmatter(t *testing.T) {
	if _, err := Parse([]byte("---\nid: k-1\n")); !errors.Is(err, ErrFMUnterminated) {
		t.Fatalf("未闭合 frontmatter 必须报错，实际 %v", err)
	}
	// 无 frontmatter 的文档解析成功（原文正文可以是任意字节）。
	d, err := Parse([]byte("正文\n\n## 分区\n\nx\n"))
	if err != nil || d.HasFM {
		t.Fatalf("无 frontmatter 应解析成功且 HasFM=false：%v", err)
	}
	// frontmatter YAML 不可解析 → E4。
	if _, _, err := ParseCard([]byte("---\nid: [不闭合\n---\n\n## 知识内容\n\nx\n")); !errors.Is(err, ErrFrontmatterYAML) {
		t.Fatalf("非法 YAML 必须报 E4，实际 %v", err)
	}
}
