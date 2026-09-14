package store

// T-…-013 的验收用例：write_note / create_card / append_card / add_open_question。
//
// 全部断言都落在**字节**上：除追加点新增的字节外，其余字节逐字不变。

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

const unprocessedSample = `# 待加工

以下条目由 eg capture 追加，write_note 成功后移出。

- source_id: s-20260901-attention
  title: Attention 机制入门
  saved_at: '2026-09-01T09:00:00+08:00'
  reason: 补齐注意力机制的基础材料
  target_domain: ai-infra

- source_id: s-20260902-rnn
  title: RNN 回顾
  saved_at: '2026-09-02T09:00:00+08:00'
  reason: 对照材料
`

func stamp(t *testing.T, raw string) model.Stamp {
	t.Helper()
	s, err := model.ParseStamp(raw)
	if err != nil {
		t.Fatalf("stamp %q: %v", raw, err)
	}
	return s
}

func day(t *testing.T, raw string) model.Date {
	t.Helper()
	d, err := model.ParseDate(raw)
	if err != nil {
		t.Fatalf("date %q: %v", raw, err)
	}
	return d
}

func noteSpecFixture(t *testing.T) NoteSpec {
	t.Helper()
	return NoteSpec{
		Rel:      NoteRel("ai-infra", "n-20260901-attention"),
		ID:       "n-20260901-attention",
		SourceID: "s-20260901-attention",
		Title:    "Attention 机制入门",
		Date:     day(t, "2026-09-01"),
		Stamp:    stamp(t, "2026-09-01T10:00:00+08:00"),
		Tags:     []string{"s1"},
		Sections: []SectionAppend{
			{Section: mdfile.SecDigest, Payload: []byte("要点：注意力是加权求和。\n")},
			{Section: mdfile.SecAgentReview, Payload: []byte("与 RNN 的差别在并行度。\n")},
		},
		OutputCards: []string{"k-20260901-attention（新建）"},
	}
}

func cardSpecFixture(t *testing.T) CardSpec {
	t.Helper()
	return CardSpec{
		Rel:   CardRel("ai-infra", "k-20260901-attention"),
		ID:    "k-20260901-attention",
		Title: "注意力机制",
		Date:  day(t, "2026-09-01"),
		Stamp: stamp(t, "2026-09-01T10:00:00+08:00"),
		Tags:  []string{"s1"},
		Sources: []model.SourceRef{{Source: "s-20260901-attention", Note: "n-20260901-attention",
			Rel: "support", Reason: "原文第 3 节"}},
		Sections: []SectionAppend{
			{Section: mdfile.SecKnowledge, Payload: []byte("注意力是对值的加权求和。\n")},
			{Section: mdfile.SecRationale, Payload: []byte("原文给出了 softmax 推导。\n")},
			{Section: mdfile.SecSelfCheck, Payload: []byte("- [ ] 能写出打分函数\n")},
		},
	}
}

// ---------- 机器判据：结构与键集合 ----------

func TestNoteAndCardStructureIsMachineCheckable(t *testing.T) {
	s, root := newVault(t)
	if _, err := s.ApplyNote(noteSpecFixture(t)); err != nil {
		t.Fatalf("write_note: %v", err)
	}
	if _, err := s.ApplyCard(cardSpecFixture(t)); err != nil {
		t.Fatalf("create_card: %v", err)
	}
	cases := []struct {
		rel   string
		kind  mdfile.Kind
		want  []string
		keys  []string
		bans  []string
		order []string
	}{
		{rel: NoteRel("ai-infra", "n-20260901-attention"), kind: mdfile.KindNote,
			keys:  []string{"id", "source", "created_at", "updated_at"},
			bans:  []string{"status", "resolved", "state", "domain", "type"},
			order: mdfile.NoteSections()},
		{rel: CardRel("ai-infra", "k-20260901-attention"), kind: mdfile.KindCard,
			keys:  []string{"id", "status", "created_at", "updated_at", "sources"},
			bans:  []string{"domain", "type", "candidate", "source_check", "reviewed_at"},
			order: mdfile.CardSections()},
	}
	for _, c := range cases {
		raw := mustBytes(t, filepath.Join(root, c.rel))
		doc, err := mdfile.Parse(raw)
		if err != nil {
			t.Fatalf("%s 必须可解析：%v", c.rel, err)
		}
		var fm map[string]interface{}
		if err := doc.DecodeFM(&fm); err != nil {
			t.Fatalf("%s 的 frontmatter 必须可解析为 map：%v", c.rel, err)
		}
		for _, key := range c.keys {
			if _, ok := fm[key]; !ok {
				t.Fatalf("%s 缺 frontmatter 键 %q（实得 %v）", c.rel, key, fm)
			}
		}
		for _, key := range c.bans {
			if _, ok := fm[key]; ok {
				t.Fatalf("%s 不得写 frontmatter 键 %q", c.rel, key)
			}
		}
		names := doc.SectionNames()
		if strings.Join(names, "/") != strings.Join(c.order, "/") {
			t.Fatalf("%s 分区必须逐字等于固定顺序 %v，实得 %v", c.rel, c.order, names)
		}
		if bytes.Contains(raw, []byte("\n# ")) || bytes.HasPrefix(raw[doc.BodyFrom:], []byte("# ")) {
			t.Fatalf("%s 不得出现 H1：\n%s", c.rel, raw)
		}
		if c.kind == mdfile.KindNote && bytes.Contains(raw, []byte("## "+mdfile.SecSelfCheck)) {
			t.Fatalf("材料笔记不得有「%s」分区", mdfile.SecSelfCheck)
		}
	}
}

// ---------- EG-SRC-04：新建卡必须带 sources[] ----------

func TestCardCreateWithoutSourcesIsRejected(t *testing.T) {
	s, root := newVault(t)
	for name, spec := range map[string]CardSpec{
		"缺字段": func() CardSpec { c := cardSpecFixture(t); c.Sources = nil; return c }(),
		"空数组": func() CardSpec { c := cardSpecFixture(t); c.Sources = []model.SourceRef{}; return c }(),
	} {
		res, err := s.ApplyCard(spec)
		if err == nil {
			t.Fatalf("%s：新建卡缺 sources[] 必须拒绝", name)
		}
		if !strings.Contains(err.Error(), "必须建立材料关系") {
			t.Fatalf("%s：错误信息须指出「新建卡必须建立材料关系」，实得 %v", name, err)
		}
		if res.Written {
			t.Fatalf("%s：必须零写入", name)
		}
		if _, err := os.Stat(filepath.Join(root, "domains")); !os.IsNotExist(err) {
			t.Fatalf("%s：domains/ 下不得出现任何文件", name)
		}
	}
	if _, err := s.ApplyCard(cardSpecFixture(t)); err != nil {
		t.Fatalf("带合法四要素 sources[] 的同一 plan 必须通过：%v", err)
	}
}

func TestCardCreateWithoutKnowledgeSectionIsRejected(t *testing.T) {
	s, root := newVault(t)
	spec := cardSpecFixture(t)
	spec.Sections = []SectionAppend{{Section: mdfile.SecRationale, Payload: []byte("只有依据。\n")}}
	res, err := s.ApplyCard(spec)
	if err == nil || res.Written {
		t.Fatalf("缺「%s」必须拒绝建卡：%v / %+v", mdfile.SecKnowledge, err, res)
	}
	if _, err := os.Stat(filepath.Join(root, spec.Rel)); !os.IsNotExist(err) {
		t.Fatal("拒绝建卡时不得留下文件")
	}
}

// ---------- 分区写权限：E6 的两条 ----------

func TestCardAppendToReadOnlySectionsKeepsBytes(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", cardSample)
	before := mustBytes(t, abs)
	for _, section := range []string{mdfile.SecKnowledge, mdfile.SecUserAppend} {
		res, err := s.ApplyCardAppend(CardAppendSpec{Rel: "cards/k.md",
			Stamp:    stamp(t, "2026-09-02T09:00:00+08:00"),
			Sections: []SectionAppend{{Section: section, Payload: []byte("不该被写入。\n")}}})
		if err == nil || res.Written {
			t.Fatalf("「%s」对自动路径必须拒写：%v / %+v", section, err, res)
		}
		if !bytes.Equal(mustBytes(t, abs), before) {
			t.Fatalf("拒写后目标文件字节必须不变（分区 %s）", section)
		}
	}
}

func TestNoteUserSectionIsNeverWritten(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "notes/n.md", noteSample)
	before := mustBytes(t, abs)
	res, err := s.sectionEdit("notes/n.md", "", mdfile.KindNote, model.Stamp{},
		[]SectionAppend{{Section: mdfile.SecUserAppend, Payload: []byte("越权写入。\n")}})
	if err == nil || res.Written {
		t.Fatalf("「用户补充」任何路径都必须拒写：%v / %+v", err, res)
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("拒写后笔记字节必须不变")
	}
	spec := noteSpecFixture(t)
	spec.Sections = append(spec.Sections,
		SectionAppend{Section: mdfile.SecUserAppend, Payload: []byte("越权写入。\n")})
	if _, err := s.ApplyNote(spec); err == nil {
		t.Fatal("新建路径也不得写「用户补充」")
	}
}

// ---------- 收件区条目移出（EG-SRC-02） ----------

func TestNoteWriteDetachesInboxEntry(t *testing.T) {
	s, root := newVault(t)
	inbox := writeSeed(t, root, UnprocessedFile, unprocessedSample)

	out, err := s.ApplyNote(noteSpecFixture(t))
	if err != nil {
		t.Fatalf("write_note: %v", err)
	}
	if !out.Note.Written || out.Reused {
		t.Fatalf("新建笔记应写入：%+v", out)
	}
	if out.InboxSkip != nil || !out.Inbox.Written {
		t.Fatalf("条目应移出：%+v / %v", out.Inbox, out.InboxSkip)
	}
	got := mustBytes(t, inbox)
	if bytes.Contains(got, []byte("s-20260901-attention")) {
		t.Fatalf("该 source_id 条目必须消失：\n%s", got)
	}
	for _, keep := range []string{"# 待加工", "以下条目由 eg capture 追加", "s-20260902-rnn", "  reason: 对照材料\n"} {
		if !bytes.Contains(got, []byte(keep)) {
			t.Fatalf("条目之外的字节必须逐字保留，丢了 %q：\n%s", keep, got)
		}
	}
	note := mustBytes(t, filepath.Join(root, out.Note.Path))
	if !bytes.Contains(note, []byte("- k-20260901-attention（新建）\n")) {
		t.Fatalf("「产出知识卡」应含新建卡 ID 与括注：\n%s", note)
	}
	assertNoTmp(t, root)
}

func TestNoteInboxDetachFailureIsReported(t *testing.T) {
	s, root := newVault(t)
	inbox := writeSeed(t, root, UnprocessedFile, unprocessedSample)
	f, err := s.Read(UnprocessedFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// eg context 之后收件区被改动
	changed := unprocessedSample + "\n- source_id: s-20260903-x\n  title: 新条目\n"
	if err := os.WriteFile(inbox, []byte(changed), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}

	spec := noteSpecFixture(t)
	spec.Inbox = InboxSpec{ExpectedHash: f.Hash}
	out, err := s.ApplyNote(spec)
	if err != nil {
		t.Fatalf("笔记本体应照常写入：%v", err)
	}
	if !out.Note.Written {
		t.Fatal("笔记应照常写入")
	}
	if out.InboxSkip == nil || out.InboxSkip.Reason != SkipFileChanged {
		t.Fatalf("条目未移出必须显式上报：%+v", out.InboxSkip)
	}
	if CauseFor(out.Inbox.Reason) != "content_hash_mismatch" {
		t.Fatalf("skipped[] 归因必须是 content_hash_mismatch，实得 %q", CauseFor(out.Inbox.Reason))
	}
	if !bytes.Equal(mustBytes(t, inbox), []byte(changed)) {
		t.Fatal("条目必须原样保留，收件区字节不变")
	}
}

func TestNoteAlreadyExistsIsReusedNotRewritten(t *testing.T) {
	s, root := newVault(t)
	spec := noteSpecFixture(t)
	abs := writeSeed(t, root, spec.Rel, noteSample)
	before := mustBytes(t, abs)

	out, err := s.ApplyNote(spec)
	if err != nil {
		t.Fatalf("复用路径不应报错：%v", err)
	}
	if !out.Reused || out.Note.Written {
		t.Fatalf("已存在的笔记必须复用、不重写：%+v", out)
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("复用时笔记字节必须不变")
	}
}

// ---------- B2：重新加工逐字保留 ----------

func TestNoteReprocessPreservesUserBytesVerbatim(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "notes/n.md", noteSample)
	before := mustBytes(t, abs)
	beforeUser, err := UserSectionBytes(before)
	if err != nil {
		t.Fatalf("user sections: %v", err)
	}

	res, err := s.sectionEdit("notes/n.md", "", mdfile.KindNote, model.Stamp{},
		[]SectionAppend{{Section: mdfile.SecDigest, Payload: []byte("重新加工补充的要点。\n")}})
	if err != nil || !res.Written {
		t.Fatalf("重新加工应写入：%v / %+v", err, res)
	}
	after := mustBytes(t, abs)
	afterUser, err := UserSectionBytes(after)
	if err != nil {
		t.Fatalf("user sections: %v", err)
	}
	if !bytes.Equal(beforeUser[mdfile.SecUserAppend], afterUser[mdfile.SecUserAppend]) {
		t.Fatalf("「用户补充」必须逐字相等：\n%q\n%q",
			beforeUser[mdfile.SecUserAppend], afterUser[mdfile.SecUserAppend])
	}
	if !bytes.Equal(beforeUser[mdfile.SecOpenQuest], afterUser[mdfile.SecOpenQuest]) {
		t.Fatalf("「存疑与待验证」既有内容必须逐字相等：\n%q\n%q",
			beforeUser[mdfile.SecOpenQuest], afterUser[mdfile.SecOpenQuest])
	}
}

// ---------- 追加只增字节：未知字段 / 不规则缩进 / 未知第六个 H2 ----------

const messyCard = `---
id: k-20260901-messy
title: 含未知键与不规则空行的卡
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
    - source: s-20260901-x
      note: n-20260901-x
      rel: support
      reason: 四空格缩进的既有风格


unknown_key:   保留我
nested:
  a:    1
tags:
    - s1
---

## 知识内容

原样。


## 解释与依据

既有依据。

## 条件与边界

## 用户补充

	制表符开头的用户块

## 理解自检

- [ ] 旧问题一

## 用户自建的第六分区

用户自己加的内容，别动。
`

func TestSectionAppendOnlyAddsBytes(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/messy.md", messyCard)
	before := mustBytes(t, abs)

	res, err := s.ApplyCardAppend(CardAppendSpec{Rel: "cards/messy.md",
		Stamp:    stamp(t, "2026-09-02T09:00:00+08:00"),
		Sections: []SectionAppend{{Section: mdfile.SecBoundary, Payload: []byte("仅限 S1。\n")}}})
	if err != nil || !res.Written {
		t.Fatalf("追加应成功：%v / %+v", err, res)
	}
	after := mustBytes(t, abs)
	// 除插入点与 `updated_at` 那一行外逐字不变：删掉新增字节、把刷新后的时间戳行还原成
	// 原值后，必须与原文完全相等（I-…-009：内容时间戳跟随实际写入刷新，其余字节不动）。
	added := []byte("仅限 S1。\n")
	idx := bytes.Index(after, added)
	if idx < 0 {
		t.Fatalf("追加内容应逐字出现：\n%s", after)
	}
	rest := append(append([]byte{}, after[:idx]...), after[idx+len(added):]...)
	const oldStamp = "updated_at: '2026-09-01T10:00:00+08:00'\n"
	const newStamp = "updated_at: '2026-09-02T09:00:00+08:00'\n"
	if !bytes.Contains(rest, []byte(newStamp)) {
		t.Fatalf("`updated_at` 必须被刷新成本次写入时刻（%q）：\n%s", newStamp, after)
	}
	if n := bytes.Count(after, []byte("updated_at:")); n != 1 {
		t.Fatalf("`updated_at` 必须恰一行，实得 %d 行：\n%s", n, after)
	}
	restored := bytes.Replace(rest, []byte(newStamp), []byte(oldStamp), 1)
	if !bytes.Equal(restored, before) {
		t.Fatalf("除追加字节与 `updated_at` 行外其余字节必须逐字不变：\n%q", restored)
	}
	if len(after) <= len(before) {
		t.Fatal("追加只增字节（时间戳等长替换，净增量仍必须为正）")
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("既有 `updated_at` 已就地刷新，不应再有「不改写」warning：%v", res.Warnings)
	}
}

func TestCardSelfCheckHistoryIsAppendOnly(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/messy.md", messyCard)
	for _, payload := range []string{"- [ ] 新问题二\n", "- [ ] 新问题三\n"} {
		if _, err := s.ApplyCardAppend(CardAppendSpec{Rel: "cards/messy.md",
			Sections: []SectionAppend{{Section: mdfile.SecSelfCheck, Payload: []byte(payload)}}}); err != nil {
			t.Fatalf("理解自检追加应成功：%v", err)
		}
	}
	got := mustBytes(t, abs)
	for _, keep := range []string{"- [ ] 旧问题一\n", "- [ ] 新问题二\n", "- [ ] 新问题三\n"} {
		if !bytes.Contains(got, []byte(keep)) {
			t.Fatalf("历史块只追加、问题文本原样保留，丢了 %q", keep)
		}
	}
	if bytes.Contains(got, []byte("> - [ ] 旧问题一")) {
		t.Fatal("历史问题不得折叠为引用")
	}
}

// ---------- add_open_question（EG-KNW-03 三个「无」） ----------

var idLike = regexp.MustCompile(`^[a-z]-\d{8}-`)

func TestNoteOpenQuestionAppendsBlockWithoutIDOrFile(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "notes/n.md", noteSample)
	beforeFiles := listFiles(t, root)
	before := mustBytes(t, abs)

	block := []byte("- 注意力的计算复杂度在长序列下是否仍可接受？\n")
	res, err := s.ApplyOpenQuestion(OpenQuestionSpec{Rel: "notes/n.md", Question: block})
	if err != nil || !res.Written {
		t.Fatalf("存疑追加应成功：%v / %+v", err, res)
	}
	after := mustBytes(t, abs)
	if !bytes.Contains(after, []byte("- 旧的存疑项\n")) {
		t.Fatal("已有存疑块字节必须不变")
	}
	if !bytes.Contains(after, block) {
		t.Fatalf("新块应逐字出现：\n%s", after)
	}
	if idLike.Match(bytes.TrimLeft(block, "- ")) {
		t.Fatal("存疑不得有独立 ID")
	}
	if got := listFiles(t, root); strings.Join(got, ",") != strings.Join(beforeFiles, ",") {
		t.Fatalf("存疑不得新增任何文件（尤其无 open/ 目录）：%v → %v", beforeFiles, got)
	}
	doc, err := mdfile.Parse(after)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var fm map[string]interface{}
	if err := doc.DecodeFM(&fm); err != nil {
		t.Fatalf("frontmatter: %v", err)
	}
	for _, ban := range []string{"status", "resolved", "state"} {
		if _, ok := fm[ban]; ok {
			t.Fatalf("笔记 frontmatter 不得有状态字段 %q", ban)
		}
	}
	if res.Path != "notes/n.md" || fm["id"] != "n-20260901-spec" {
		t.Fatalf("存疑必须可定位回笔记（id + path）：%q / %v", res.Path, fm["id"])
	}
	if len(before) >= len(after) {
		t.Fatal("只追加，字节只增")
	}
}

// ---------- EG-EDIT-03：无隐式级联 ----------

func TestNoteWriteDoesNotCascadeToCards(t *testing.T) {
	s, root := newVault(t)
	cardAbs := writeSeed(t, root, CardRel("ai-infra", "k-20260901-attention"), cardSample)
	otherNote := writeSeed(t, root, NoteRel("ai-infra", "n-20260815-rnn"), noteSample)
	cardBefore, noteBefore := mustBytes(t, cardAbs), mustBytes(t, otherNote)

	if _, err := s.ApplyNote(noteSpecFixture(t)); err != nil {
		t.Fatalf("write_note: %v", err)
	}
	if !bytes.Equal(mustBytes(t, cardAbs), cardBefore) {
		t.Fatal("只改笔记时卡片字节必须不变（含 updated_at）")
	}
	if !bytes.Equal(mustBytes(t, otherNote), noteBefore) {
		t.Fatal("未被点名的笔记字节必须不变")
	}
}

func TestCardAppendDoesNotCascadeToNotes(t *testing.T) {
	s, root := newVault(t)
	cardRel := CardRel("ai-infra", "k-20260901-attention")
	writeSeed(t, root, cardRel, cardSample)
	noteAbs := writeSeed(t, root, NoteRel("ai-infra", "n-20260901-attention"), noteSample)
	noteBefore := mustBytes(t, noteAbs)

	if _, err := s.ApplyCardAppend(CardAppendSpec{Rel: cardRel,
		Sections: []SectionAppend{{Section: mdfile.SecBoundary, Payload: []byte("仅 S1。\n")}}}); err != nil {
		t.Fatalf("append_card: %v", err)
	}
	if !bytes.Equal(mustBytes(t, noteAbs), noteBefore) {
		t.Fatal("只追加卡时未被点名的笔记字节必须不变")
	}
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sortStrings(out)
	return out
}

func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}
