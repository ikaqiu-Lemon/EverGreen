package store

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// ---------- 样例与工具 ----------

// cardSample 是 **Schema v2** 知识卡语料：固定三分区（契约 §3.2）。
const cardSample = `---
id: k-20260901-guard-order
title: 守卫写入固定次序
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: s-20260901-spec
    note: n-20260901-spec
    rel: support
    reason: 技术方案 §16.4
tags:
  - s1
---

## 知识内容

写前自检不等即拒写。

## 条件与边界

仅 S1。

## 用户补充

	这里是用户手写的缩进块。

- 用户自己的列表
    - 未知子结构
`

// noteSample 是 **Schema v2** 材料笔记语料：固定四分区，`用户补充` 收尾（契约 §3.2）。
const noteSample = `---
id: n-20260901-spec
title: 技术方案笔记
created_at: '2026-09-01'
---

## 整理正文

要点一。

> **[Agent 补充]** 分析一。

## 提取结果

### Knowledge

- k-20260901-guard-order

## 存疑与待验证

- 旧的存疑项

## 用户补充

用户原话，别动。
`

// legacyCardV1 / legacyNoteV1 是**真实 v1 存量语料**（T-…-003 的存量兼容底线）：
// 被移除的分区（卡的 `解释与依据` / `理解自检`，笔记的 `材料提炼` / `Agent 分析` /
// `产出知识卡`）必须原样保留、只记 info、字节不变，且解析与结构校验都不得报错。
//
// 注意 v1 笔记的 `用户补充` 排在**第三位**（v2 挪到末位）：这正是 v2 顺序表校验
// v1 文件时会误报「顺序颠倒」的那处差异，因此语料必须逐字保留 v1 的排布。
const legacyCardV1 = `---
id: k-20260801-legacy
title: v1 存量卡
status: active
created_at: '2026-08-01'
updated_at: '2026-08-01T10:00:00+08:00'
sources:
  - source: s-20260801-legacy
    note: n-20260801-legacy
    rel: support
    reason: v1 时代的引用
tags:
  - s1
---

## 知识内容

v1 的知识内容。

## 解释与依据

v1 时代 Agent 写的论证。

## 条件与边界

仅 v1。

## 用户补充

	用户在 v1 时代手写的缩进块。

## 理解自检

- [ ] v1 时代的自检项
`

const legacyNoteV1 = `---
id: n-20260801-legacy
title: v1 存量笔记
created_at: '2026-08-01'
---

## 材料提炼

v1 的要点。

## Agent 分析

v1 的分析。

## 用户补充

用户在 v1 时代写的原话。

## 存疑与待验证

- v1 时代的存疑项

## 产出知识卡

- k-20260801-legacy
`

// malformedSample 的 frontmatter 未闭合：Parse→Render 无法逐字复原，写前自检必须拒写。
const malformedSample = `---
id: k-20260901-broken
title: 未闭合的 frontmatter

## 知识内容

正文看起来正常，但 frontmatter 没有结束分隔符。
`

// cardTemplate 是新建卡的模板：用户补充**必须为空**（自动路径永不写用户内容）。
var cardTemplate = strings.Replace(cardSample,
	"## 用户补充\n\n\t这里是用户手写的缩进块。\n\n- 用户自己的列表\n    - 未知子结构\n", "## 用户补充\n", 1)

func newVault(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	return New(root), root
}

func writeSeed(t *testing.T, root, rel, content string) string {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return abs
}

func mustBytes(t *testing.T, abs string) []byte {
	t.Helper()
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return b
}

func assertNoTmp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), tmpSuffix) {
			t.Fatalf("有 %s 残留：%s", tmpSuffix, e.Name())
		}
	}
}

// ---------- B3：文件自读取以来变化 ----------

func TestGuardB3FileChangedSkipsAndKeepsBytes(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", cardSample)

	f, err := s.Read("cards/k.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// 外部改动同一文件
	changed := cardSample + "\n外部追加的一行\n"
	if err := os.WriteFile(abs, []byte(changed), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}

	res, err := s.WriteGuarded("cards/k.md", f.Hash, Edit{
		Kind:     mdfile.KindCard,
		Sections: []SectionAppend{{Section: mdfile.SecBoundary, Payload: []byte("新增边界。\n")}},
	})
	skip, ok := AsSkip(err)
	if !ok {
		t.Fatalf("期望 *SkipError，得到 %v", err)
	}
	if skip.Reason != SkipFileChanged || res.Reason != SkipFileChanged {
		t.Fatalf("期望 SkipFileChanged，得到 %q / %q", skip.Reason, res.Reason)
	}
	if !skip.Skip() {
		t.Fatal("SkipError.Skip() 必须为 true")
	}
	if skip.Path != "cards/k.md" || !strings.Contains(skip.Error(), "cards/k.md") {
		t.Fatalf("跳过原因必须可定位到文件路径，得到 %q", skip.Error())
	}
	if res.Written {
		t.Fatal("跳过时不得写入")
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, []byte(changed)) {
		t.Fatal("目标文件字节必须不变")
	}
	assertNoTmp(t, filepath.Dir(abs))
}

func TestGuardExpectedHashFromContext(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", cardSample)
	f, _ := s.Read("cards/k.md")
	if f.Hash != ContentHash([]byte(cardSample)) {
		t.Fatal("content_hash 必须只按字节计算")
	}
	res, err := s.WriteGuarded("cards/k.md", f.Hash, Edit{
		Kind:     mdfile.KindCard,
		Sections: []SectionAppend{{Section: mdfile.SecBoundary, Payload: []byte("补充边界。\n")}},
	})
	if err != nil || !res.Written {
		t.Fatalf("hash 一致时应写入：%v / %+v", err, res)
	}
	if res.Hash != ContentHash(mustBytes(t, abs)) {
		t.Fatal("回执 hash 必须等于落盘字节的 hash")
	}
}

// ---------- 写前字节自检 ----------

func TestGuardSelfCheckRejectsWrite(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/broken.md", malformedSample)
	f, err := s.Read("cards/broken.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	res, err := s.WriteGuarded("cards/broken.md", f.Hash, Edit{
		Kind:     mdfile.KindCard,
		Sections: []SectionAppend{{Section: mdfile.SecBoundary, Payload: []byte("x\n")}},
	})
	skip, ok := AsSkip(err)
	if !ok {
		t.Fatalf("自检不等必须返回 *SkipError，得到 %v", err)
	}
	if skip.Reason != SkipFileChanged {
		t.Fatalf("自检失败退化为 SkipFileChanged，得到 %q", skip.Reason)
	}
	if !strings.Contains(res.Detail, "写前字节自检失败") {
		t.Fatalf("回执必须写明自检失败：%q", res.Detail)
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, []byte(malformedSample)) {
		t.Fatal("零写入：文件字节必须不变")
	}
	assertNoTmp(t, filepath.Dir(abs))

	var receipt Receipt
	receipt.Record(res)
	if len(receipt.Skipped()) != 1 || len(receipt.Applied()) != 0 {
		t.Fatal("回执必须落在 skipped[] 里")
	}
}

func TestGuardSelfCheckPositiveWrites(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", cardSample)
	if err := mdfile.SelfCheck([]byte(cardSample)); err != nil {
		t.Fatalf("正例必须自检通过：%v", err)
	}
	res, err := s.WriteGuarded("cards/k.md", "", Edit{
		Kind:     mdfile.KindCard,
		Sections: []SectionAppend{{Section: mdfile.SecBoundary, Payload: []byte("- 新增边界项\n")}},
	})
	if err != nil || !res.Written {
		t.Fatalf("正例应写入：%v / %+v", err, res)
	}
	out := mustBytes(t, abs)
	if !bytes.Contains(out, []byte("- 新增边界项\n")) {
		t.Fatal("追加内容应落盘")
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("写后只读复核不应报警：%v", res.Warnings)
	}
	assertNoTmp(t, filepath.Dir(abs))
}

// ---------- B2：用户分区逐字保留 ----------

func TestGuardB2UserSectionsRoundTrip(t *testing.T) {
	s, root := newVault(t)
	absCard := writeSeed(t, root, "cards/k.md", cardSample)
	absNote := writeSeed(t, root, "notes/n.md", noteSample)

	before := mustBytes(t, absCard)
	userBefore, err := UserSectionBytes(before)
	if err != nil {
		t.Fatalf("user sections: %v", err)
	}
	if len(bytes.TrimSpace(userBefore[mdfile.SecUserAppend])) == 0 {
		t.Fatal("样例的用户补充不应为空")
	}
	if _, err := s.AppendToSection("cards/k.md", mdfile.KindCard, mdfile.SecBoundary, []byte("再加一条边界。\n")); err != nil {
		t.Fatalf("append: %v", err)
	}
	afterUser, err := UserSectionBytes(mustBytes(t, absCard))
	if err != nil {
		t.Fatalf("user sections: %v", err)
	}
	if !bytes.Equal(userBefore[mdfile.SecUserAppend], afterUser[mdfile.SecUserAppend]) {
		t.Fatal("用户补充必须逐字相等（含空行、缩进、未知子结构）")
	}

	noteBefore, err := UserSectionBytes(mustBytes(t, absNote))
	if err != nil {
		t.Fatalf("note user sections: %v", err)
	}
	if _, err := s.AppendToSection("notes/n.md", mdfile.KindNote, mdfile.SecOpenQuest, []byte("- 新的存疑项\n")); err != nil {
		t.Fatalf("append open question: %v", err)
	}
	noteAfter := mustBytes(t, absNote)
	// 「存疑与待验证」允许追加：既有正文主体必须逐字连续保留（追加点在其后）。
	if !bytes.Contains(noteAfter, bytes.TrimRight(noteBefore[mdfile.SecOpenQuest], " \t\r\n")) {
		t.Fatal("存疑与待验证的既有字节必须逐字保留")
	}
	if !bytes.Contains(noteAfter, []byte("- 旧的存疑项\n- 新的存疑项\n")) {
		t.Fatalf("追加应落在既有条目之后：\n%s", noteAfter)
	}
	if !bytes.Contains(noteAfter, noteBefore[mdfile.SecUserAppend]) {
		t.Fatal("笔记的用户补充必须逐字保留")
	}
}

func TestGuardB2UnsafeUserBlockSkips(t *testing.T) {
	s, root := newVault(t)
	unsafe := cardSample + "\n## 用户补充\n\n第二处同名用户分区。\n"
	abs := writeSeed(t, root, "cards/unsafe.md", unsafe)
	res, err := s.AppendToSection("cards/unsafe.md", mdfile.KindCard, mdfile.SecBoundary, []byte("x\n"))
	skip, ok := AsSkip(err)
	if !ok {
		t.Fatalf("期望 *SkipError，得到 %v", err)
	}
	if skip.Reason != SkipUserBlockUnsafe || res.Reason != SkipUserBlockUnsafe {
		t.Fatalf("期望 SkipUserBlockUnsafe，得到 %q / %q", skip.Reason, res.Reason)
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, []byte(unsafe)) {
		t.Fatal("文件字节必须不变")
	}
	assertNoTmp(t, filepath.Dir(abs))
}

func TestGuardUserAppendWriteRejectedOnAllPaths(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", cardSample)
	before := mustBytes(t, abs)

	// 路径一：新建卡时带上用户补充内容
	newCard := strings.Replace(cardSample, "## 用户补充\n", "## 用户补充\n\n新建时就塞进来的用户内容。\n", 1)
	if _, err := s.CreateFile("cards/new.md", mdfile.KindCard, []byte(newCard)); err == nil {
		t.Fatal("新建路径写用户补充必须报错")
	}
	if _, err := os.Stat(filepath.Join(root, "cards/new.md")); err == nil {
		t.Fatal("被拒的新建不得落盘")
	}

	// 路径二：追加到用户补充
	if _, err := s.AppendToSection("cards/k.md", mdfile.KindCard, mdfile.SecUserAppend, []byte("偷偷追加\n")); err == nil {
		t.Fatal("追加路径写用户补充必须报错")
	}

	// 路径三：重新加工（WriteGuarded 多动作，其中一项指向用户补充）
	f, _ := s.Read("cards/k.md")
	if _, err := s.WriteGuarded("cards/k.md", f.Hash, Edit{
		Kind: mdfile.KindCard,
		Sections: []SectionAppend{
			{Section: mdfile.SecBoundary, Payload: []byte("正常一条。\n")},
			{Section: mdfile.SecUserAppend, Payload: []byte("越界一条。\n")},
		},
	}); err == nil {
		t.Fatal("重新加工路径写用户补充必须报错")
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, before) {
		t.Fatal("三条路径被拒后磁盘必须零变化")
	}
	assertNoTmp(t, filepath.Dir(abs))
}

// ---------- 分区写白名单 ----------

// TestSectionWhitelistOnExistingCard 钉住 **Schema v2** 的知识卡自动写白名单。
//
// v2 起白名单只剩 `条件与边界` 一格（契约 §3.3 的 Knowledge 三行 + D-7）：
// `知识内容` 对已有卡只读（矩阵 #12 的 🔴 子情形），`用户补充` 永不写（B2），
// 而 `解释与依据` / `理解自检` 已**不再是固定分区**——它们既不在 KnownSections、
// 也不在 AutoWritableSections，自动路径对存量卡里的同名 H2 一律拒写。
// 这是收紧而非放宽：存量分区只能被读取与迁移（T-…-009），不能继续被 Agent 写大。
func TestSectionWhitelistOnExistingCard(t *testing.T) {
	s, root := newVault(t)
	writeSeed(t, root, "cards/k.md", cardSample)
	if _, err := s.AppendToSection("cards/k.md", mdfile.KindCard, mdfile.SecKnowledge, []byte("自动改知识内容\n")); err == nil {
		t.Fatal("已有卡的知识内容对自动路径只读")
	}
	res, err := s.AppendToSection("cards/k.md", mdfile.KindCard, mdfile.SecBoundary, []byte("追加一行。\n"))
	if err != nil || !res.Written {
		t.Fatalf("分区 %s 应允许追加：%v / %+v", mdfile.SecBoundary, err, res)
	}
	if got := mdfile.AutoWritableSections(mdfile.KindCard); len(got) != 1 || got[0] != mdfile.SecBoundary {
		t.Fatalf("v2 知识卡自动写白名单必须恰为 [%s]，实得 %v", mdfile.SecBoundary, got)
	}
}

// TestLegacyV1SectionsAreNotAutoWritable 钉住存量分区对自动路径的**只读**口径。
//
// 语料是真实 v1 存量卡：`解释与依据` 与 `理解自检` 都在文件里实际存在，
// 因此拒写不可能是「分区找不到」的副产物，只能来自白名单判定。
func TestLegacyV1SectionsAreNotAutoWritable(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/legacy.md", legacyCardV1)
	before := mustBytes(t, abs)
	for _, sec := range mdfile.LegacyV1Sections(mdfile.KindCard) {
		if !bytes.Contains(before, []byte("## "+sec+"\n")) {
			t.Fatalf("语料必须真的含存量分区「%s」", sec)
		}
		res, err := s.AppendToSection("cards/legacy.md", mdfile.KindCard, sec, []byte("不该写入。\n"))
		if err == nil || res.Written {
			t.Fatalf("存量分区「%s」对自动路径必须拒写：%v / %+v", sec, err, res)
		}
		if !bytes.Equal(mustBytes(t, abs), before) {
			t.Fatalf("拒写后字节必须逐字不变（分区 %s）", sec)
		}
	}
}

// TestLegacyV1FilesParseAndKeepBytes 是 T-…-003 的存量兼容底线：
// 真实 v1 存量卡与存量笔记各一份，解析不报错、结构校验通过、
// `UnknownSections` 命中被移除的分区、字节零变化。
func TestLegacyV1FilesParseAndKeepBytes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		kind mdfile.Kind
	}{
		{"v1 存量卡", legacyCardV1, mdfile.KindCard},
		{"v1 存量笔记", legacyNoteV1, mdfile.KindNote},
	}
	for _, c := range cases {
		doc, err := mdfile.Parse([]byte(c.raw))
		if err != nil {
			t.Fatalf("%s 必须可解析：%v", c.name, err)
		}
		if err := doc.ValidateSections(c.kind); err != nil {
			t.Fatalf("%s 的结构校验必须通过（存量兼容底线）：%v", c.name, err)
		}
		if got := doc.SectionSchema(c.kind); got != mdfile.SchemaV1 {
			t.Fatalf("%s 必须被判定为 v1 模板，实得 %v", c.name, got)
		}
		got := map[string]bool{}
		for _, sp := range doc.UnknownSections(c.kind) {
			got[sp.Name] = true
		}
		for _, sec := range mdfile.LegacyV1Sections(c.kind) {
			if !got[sec] {
				t.Fatalf("%s 的被移除分区「%s」必须落入 UnknownSections（只记 info），实得 %v",
					c.name, sec, got)
			}
		}
		if out := doc.Render(); !bytes.Equal(out, []byte(c.raw)) {
			t.Fatalf("%s 的字节必须零变化：\n%q", c.name, out)
		}
	}
}

// TestLegacyV1NoteStillAcceptsOpenQuestion 钉住存量笔记仍可被 `add_open_question` 追加：
// `存疑与待验证` 是两版模板**共有**的分区，模板切换不得连带打死存量笔记的这条主链路。
func TestLegacyV1NoteStillAcceptsOpenQuestion(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "notes/legacy.md", legacyNoteV1)
	res, err := s.AppendToSection("notes/legacy.md", mdfile.KindNote, mdfile.SecOpenQuest,
		[]byte("- 新的存疑项\n"))
	if err != nil || !res.Written {
		t.Fatalf("存量笔记的「%s」应允许追加：%v / %+v", mdfile.SecOpenQuest, err, res)
	}
	got := mustBytes(t, abs)
	if !bytes.Contains(got, []byte("- v1 时代的存疑项\n- 新的存疑项\n")) {
		t.Fatalf("追加应落在既有条目之后：\n%s", got)
	}
	for _, keep := range []string{"## 材料提炼\n", "## Agent 分析\n", "## 产出知识卡\n",
		"用户在 v1 时代写的原话。\n"} {
		if !bytes.Contains(got, []byte(keep)) {
			t.Fatalf("存量分区与用户字节必须逐字保留，丢了 %q", keep)
		}
	}
}

// ---------- B1 结构性验收 ----------

var (
	forbiddenExport = regexp.MustCompile(`^(Replace|Delete|Overwrite|SetBlock|Remove|Truncate)`)
	writeShaped     = regexp.MustCompile(`^(Write|Create|Append|Insert|Save|Update|Put|Render|Rewrite)`)
)

func exportedNames(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse dir: %v", err)
	}
	var names []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Name.IsExported() {
						names = append(names, d.Name.Name)
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
							names = append(names, ts.Name.Name)
							if st, ok := ts.Type.(*ast.StructType); ok {
								for _, f := range st.Fields.List {
									for _, n := range f.Names {
										if n.IsExported() {
											names = append(names, n.Name)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

// m3DestructiveExports 是 M3（T-…-037）**恰**允许命中破坏性黑名单的导出符号。
//
// **重钉理由（事实变了，不是放宽）**：黑名单原话就是「替换 / 删除属 **S2 起的用户显式命令**」——
// M3 正是那个 S2 阶段：`remove_relation`（owner 裁决 A-24 定死为物理移除）与
// `replace_block`（换「理解自检」当前有效块）两条形态**必须**有落盘入口。
// 因此本用例从「一个都不许有」重钉为「**恰这四个**，多一个少一个都失败」：
// 方法名仍以 Apply 开头（走既有守卫入口 mutateGuarded），B1 对 Agent 自动路径的
// 「只追加」约束一字不改（自动路径仍只有 CreateFile / AppendToSection / WriteGuarded 三形态，
// 见 TestB1WriteFormsAreExactlyThree）。
var m3DestructiveExports = map[string]bool{
	"RemoveRelationSpec":   true, // remove_relation 的输入（A-24 口径 1 / 2）
	"RemoveRelationResult": true, // 移除回执
	"Removed":              true, // 回执字段：物理移除的条数
	"ReplaceBlockSpec":     true, // replace_block 的输入（必带 base_block_hash）
	// **T-…-045 重钉**：A-13 的 `eg edit` 是 M3 完成判据「用户显式命令可改核心内容」的
	// 唯一命令载体（授权合同 §9 A-13 + §2 矩阵 #12 的 P-U ✅），整段替换必须有落盘入口。
	// 它同样只经 mutateGuarded（B3 逐文件 hash + B2 用户分区逐字保留），
	// 且 Agent 自动路径拿不到它（矩阵 #12 的 P-A 仍是 🔴），B1 一字不改。
	"ReplaceSectionSpec": true, // edit_section 的输入（仅 P-U 可达）
}

func TestB1NoDestructiveExports(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range exportedNames(t) {
		if !forbiddenExport.MatchString(name) {
			continue
		}
		if !m3DestructiveExports[name] {
			t.Fatalf("导出符号 %q 命中破坏性能力黑名单，且不在 M3 白名单 %v 内", name,
				sortedNames(m3DestructiveExports))
		}
		seen[name] = true
	}
	for name := range m3DestructiveExports {
		if !seen[name] {
			t.Fatalf("白名单符号 %q 已不存在：白名单必须与实际导出面恰等，不留死条目", name)
		}
	}
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestB1WriteFormsAreExactlyThree(t *testing.T) {
	want := map[string]bool{"CreateFile": true, "AppendToSection": true, "WriteGuarded": true}
	got := map[string]bool{}
	for _, name := range exportedNames(t) {
		if writeShaped.MatchString(name) {
			got[name] = true
		}
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("缺少写形态 %q", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Fatalf("多出写形态导出符号 %q：B1 只允许 CreateFile / AppendToSection / WriteGuarded", name)
		}
	}
}

func TestNoSerializerAndNoS5Machinery(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	textMangling := []string{"strings." + "ToLower(", "strings." + "TrimSpace(", "for _, r := range " + "string("}
	serializers := []string{"yaml." + "Marshal", "yaml." + "NewEncoder", "json." + "Marshal"}
	s5Machinery := []string{"run." + "lock", "fl" + "ock", "t" + "xn"}
	for _, f := range files {
		src := string(mustBytes(t, f))
		for _, bad := range serializers {
			if strings.Contains(src, bad) {
				t.Fatalf("%s 出现序列化器 %s：写路径禁用", f, bad)
			}
		}
		for _, bad := range s5Machinery {
			if strings.Contains(src, bad) {
				t.Fatalf("%s 出现 S5 机制 %s：S1 无锁、无事务暂存", f, bad)
			}
		}
		for _, bad := range textMangling {
			if strings.Contains(src, bad) {
				t.Fatalf("%s 对用户内容做了文本规范化 %s", f, bad)
			}
		}
	}
}

// TestStateWritePortIsUniqueToStore 是「写口唯一」护栏（合同 §7.1）。
//
// 护栏升级说明：M1/M2 阶段 TestNoSerializerAndNoS5Machinery 里有一条断言
// 「SetStatus / SetReplacedBy / SetDeleted 三个名字不得出现」（S2 状态写口尚未引入）。
// T-039 正式引入这第四类**受守卫**写形态，于是该断言升级为本测试：三个 setter 允许
// 存在，但**非测试代码里对它们的调用必须全部落在 internal/store/ 内**，
// internal/plan/ 与 internal/cli/ 零命中——写口唯一因此可被机械反证。
//
// 注意 plan.OpSetReplacedBy 这个既有常量不匹配本正则（它是 `.OpSetReplacedBy`），
// 因此无需重命名。
//
// T-042 起正则再加一个名字（**加严**）：`reviewed_at` 的受守卫写口 SetReviewedAt 同样
// 只允许在 store 包内被调用——`eg mark-reviewed` 经 ApplyStateWrite 进来，命令层零命中。
//
// T-…-055（M4 阶段 1）起正则再加第五个名字（**继续加严**）：`stale` / `stale_reason` 的
// 受守卫写口 SetStale 同样只许在 store 包内被调用——R6 的写入经 ApplyStateWrite 进来，
// internal/plan/ 与 internal/reconcile/ 必须零命中。
func TestStateWritePortIsUniqueToStore(t *testing.T) {
	callSite := regexp.MustCompile(`\.(Set` + `Status|Set` + `ReplacedBy|Set` + `Deleted|Set` +
		`ReviewedAt|Set` + `Stale)\(`)
	internalRoot := ".."
	storePrefix := filepath.Join(internalRoot, "store") + string(filepath.Separator)
	hits := 0
	err := filepath.WalkDir(internalRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src := mustBytes(t, path)
		for i, line := range bytes.Split(src, []byte("\n")) {
			if !callSite.Match(line) {
				continue
			}
			hits++
			if !strings.HasPrefix(path, storePrefix) {
				t.Fatalf("状态写口调用出现在 store 之外：%s:%d：%s（写口唯一：plan/ 与 cli/ 必须零命中）",
					path, i+1, bytes.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	if hits == 0 {
		t.Fatal("零命中：三个状态 setter 必须真的被 store 包内入口调用，护栏不能空转")
	}
}

// ---------- 原子替换与中断 ----------

func TestInterruptedWriteKeepsOldVersion(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", cardSample)
	// 模拟「tmp 写了但未 rename」的中断
	tmp := filepath.Join(filepath.Dir(abs), tmpPrefix+"interrupted"+tmpSuffix)
	if err := os.WriteFile(tmp, []byte("半截文件"), 0o644); err != nil {
		t.Fatalf("tmp: %v", err)
	}
	got := mustBytes(t, abs)
	if !bytes.Equal(got, []byte(cardSample)) {
		t.Fatal("未 rename 时目标文件必须是完整旧版本")
	}
	if err := mdfile.SelfCheck(got); err != nil {
		t.Fatalf("旧版本必须仍是完整可解析文档：%v", err)
	}
	if _, err := s.Read("cards/k.md"); err != nil {
		t.Fatalf("旧版本仍可读：%v", err)
	}
}

func TestCreateFileAtomicAndNoOverwrite(t *testing.T) {
	s, root := newVault(t)
	res, err := s.CreateFile("cards/new.md", mdfile.KindCard, []byte(cardTemplate))
	if err != nil || !res.Written {
		t.Fatalf("新建应成功：%v / %+v", err, res)
	}
	if got := mustBytes(t, filepath.Join(root, "cards/new.md")); !bytes.Equal(got, []byte(cardTemplate)) {
		t.Fatal("新建落盘必须逐字相等")
	}
	if _, err := s.CreateFile("cards/new.md", mdfile.KindCard, []byte(cardTemplate)); err == nil {
		t.Fatal("新建不得覆盖既有文件")
	}
	assertNoTmp(t, filepath.Join(root, "cards"))
	if _, err := s.CreateFile("../escape.md", mdfile.KindCard, []byte(cardTemplate)); err == nil {
		t.Fatal("逃出 vault 的路径必须被拒")
	}
}

// ---------- frontmatter 追加 ----------

func TestWriteGuardedAppendsFrontmatter(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", cardSample)
	f, _ := s.Read("cards/k.md")
	res, err := s.WriteGuarded("cards/k.md", f.Hash, Edit{
		Kind:       mdfile.KindCard,
		FMKeys:     []FMKeyAppend{{Key: "reviewed_at", Value: []byte("'2026-09-02T09:00:00+08:00'")}},
		FMSeqItems: []FMSeqAppend{{Key: "tags", Item: []byte("m1\n")}},
	})
	if err != nil || !res.Written {
		t.Fatalf("frontmatter 追加应成功：%v / %+v", err, res)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("写后复核不应报警：%v", res.Warnings)
	}
	out := mustBytes(t, abs)
	doc, err := mdfile.Parse(out)
	if err != nil {
		t.Fatalf("落盘结果必须可解析：%v", err)
	}
	var fm map[string]interface{}
	if err := doc.DecodeFM(&fm); err != nil {
		t.Fatalf("落盘结果必须是合法 YAML：%v", err)
	}
	if _, ok := fm["reviewed_at"]; !ok {
		t.Fatal("追加的键应生效")
	}
	if !bytes.Contains(out, []byte("  - m1\n")) {
		t.Fatalf("序列项缩进应沿用既有风格：\n%s", out)
	}
	if !bytes.Contains(out, []byte("这里是用户手写的缩进块")) {
		t.Fatal("用户补充必须保留")
	}
}

// ---------- id → path 扫描 ----------

func TestScanIDsSurvivesRename(t *testing.T) {
	s, root := newVault(t)
	writeSeed(t, root, "cards/k.md", cardSample)
	writeSeed(t, root, "notes/n.md", noteSample)
	writeSeed(t, root, ".git/objects/junk.md", cardSample) // .git 必须跳过

	idx, err := s.ScanIDs()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, err := idx.Resolve("k-20260901-guard-order"); err != nil || got != "cards/k.md" {
		t.Fatalf("解析失败：%q / %v", got, err)
	}
	// 重命名 + 移动后仍按 frontmatter id 解析
	if err := os.MkdirAll(filepath.Join(root, "archive"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Rename(filepath.Join(root, "cards/k.md"), filepath.Join(root, "archive/任意文件名.md")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	idx2, err := s.ScanIDs()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, err := idx2.Resolve("k-20260901-guard-order"); err != nil || got != "archive/任意文件名.md" {
		t.Fatalf("重命名后应仍能解析：%q / %v", got, err)
	}
	if _, err := idx2.Resolve("k-20260901-missing"); err == nil {
		t.Fatal("不存在的 ID 应报错")
	}
}

func TestScanIDsDuplicateDiagnostic(t *testing.T) {
	s, root := newVault(t)
	writeSeed(t, root, "cards/a.md", cardSample)
	writeSeed(t, root, "cards/b.md", cardSample)
	idx, err := s.ScanIDs()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(idx.Duplicates) != 1 {
		t.Fatalf("期望 1 条重复诊断，得到 %d", len(idx.Duplicates))
	}
	dup := idx.Duplicates[0].String()
	if !strings.Contains(dup, "cards/a.md") || !strings.Contains(dup, "cards/b.md") {
		t.Fatalf("重复诊断必须可定位到两个文件：%q", dup)
	}
	if _, err := idx.Resolve("k-20260901-guard-order"); err == nil {
		t.Fatal("重复 ID 时解析必须报错（供上层出 E1）")
	}
}
