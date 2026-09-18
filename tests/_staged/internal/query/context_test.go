package query_test

// T-…-010 的 internal/query 侧验收：白名单组装、他域隔离、失效卡排除、
// EG-NOTE-04 字段级断言、content_hash 与 store 重算交叉一致、打分确定性、只读零副作用。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— 夹具：一个手写的最小 vault（字节直写，不经写口，保证用例与写路径解耦）——

const srcDemo = `---
id: s-20260412-demo
url: https://example.com/attention
title: 注意力机制入门
saved_at: '2026-04-12T09:00:00+08:00'
---

# 注意力机制入门

正文：Attention 把查询与键值配对。
`

const noteDemo = `---
id: n-20260412-demo
source: s-20260412-demo
created_at: '2026-04-12'
updated_at: '2026-04-12T09:30:00+08:00'
---

## 原文提炼

- 注意力机制入门的要点
`

const cardAttention = `---
id: c-20260412-attention
title: 注意力机制
status: active
created_at: '2026-04-12'
updated_at: '2026-04-12T10:00:00+08:00'
tags:
  - 注意力
  - transformer
---

## 定义
`

const cardOther = `---
id: c-20260412-unrelated
title: 磁盘调度
status: active
created_at: '2026-04-12'
updated_at: '2026-04-12T10:00:00+08:00'
---

## 定义
`

const cardDeprecated = `---
id: c-20260412-old
title: 注意力机制旧版
status: deprecated
created_at: '2026-04-12'
updated_at: '2026-04-12T10:00:00+08:00'
---

## 定义
`

// 他域同标题卡：领域由目录决定，绝不能出现在 ai-infra 的上下文里（EG-DOM-02 / EG-CHK-03）。
const cardCrossDomain = `---
id: c-20260412-crossdomain
title: 注意力机制
status: active
created_at: '2026-04-12'
updated_at: '2026-04-12T10:00:00+08:00'
---

## 定义
`

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"unprocessed.md":                                     "# 未处理\n\n- source_id: s-20260412-demo\n",
		"sources/s-20260412-demo.md":                         srcDemo,
		"domains/ai-infra/notes/n-20260412-demo.md":          noteDemo,
		"domains/ai-infra/knowledge/c-20260412-attention.md": cardAttention,
		"domains/ai-infra/knowledge/c-20260412-unrelated.md": cardOther,
		"domains/ai-infra/knowledge/c-20260412-old.md":       cardDeprecated,
		"domains/infra/knowledge/c-20260412-crossdomain.md":  cardCrossDomain,
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func build(t *testing.T, root string, req query.Request) *query.Context {
	t.Helper()
	req.Root = root
	if req.Domain == "" {
		req.Domain = "ai-infra"
	}
	ctx, err := query.Build(req, store.ContentHash)
	if err != nil {
		t.Fatalf("query.Build: %v", err)
	}
	return ctx
}

// —— ① 白名单：只有原文、本领域已有笔记、同领域 active 卡、候选相似卡 ——

func TestContextWhitelistShape(t *testing.T) {
	root := fixture(t)
	ctx := build(t, root, query.Request{Source: "s-20260412-demo"})

	if ctx.Source == nil || ctx.Source.ID != "s-20260412-demo" {
		t.Fatalf("目标原文缺失：%+v", ctx.Source)
	}
	if !strings.Contains(ctx.Source.Body, "Attention 把查询与键值配对") {
		t.Fatalf("原文正文未原样交出：%q", ctx.Source.Body)
	}
	if len(ctx.Notes) != 1 || ctx.Notes[0].ID != "n-20260412-demo" {
		t.Fatalf("材料笔记 = %+v，期望恰 1 篇 n-20260412-demo", ctx.Notes)
	}
	// 同领域 active 卡恰两张：deprecated 卡与他域卡都不在。
	var cardIDs []string
	for _, c := range ctx.Cards {
		cardIDs = append(cardIDs, c.ID)
	}
	want := []string{"c-20260412-attention", "c-20260412-unrelated"}
	if !reflect.DeepEqual(cardIDs, want) {
		t.Fatalf("同领域 active 卡 = %v，期望 %v", cardIDs, want)
	}
	if len(ctx.Candidates) != 1 || ctx.Candidates[0].ID != "c-20260412-attention" {
		t.Fatalf("候选相似卡 = %+v，期望恰命中 c-20260412-attention", ctx.Candidates)
	}
	if len(ctx.Candidates[0].Reasons) == 0 || ctx.Candidates[0].Score <= 0 {
		t.Fatalf("候选卡必须带得分与命中理由：%+v", ctx.Candidates[0])
	}
}

// —— ② 他域隔离反例：他域同标题卡零出现（ID 与路径都不出现在任何字段）——

func TestContextExcludesOtherDomains(t *testing.T) {
	root := fixture(t)
	ctx := build(t, root, query.Request{Source: "s-20260412-demo"})
	blob := dump(ctx)
	for _, needle := range []string{"c-20260412-crossdomain", "domains/infra/"} {
		if strings.Contains(blob, needle) {
			t.Fatalf("输出泄漏他域产物 %q：\n%s", needle, blob)
		}
	}
}

// —— ③ 失效卡排除：deprecated 既不进同领域卡列表，也不进候选 ——

func TestContextExcludesDeprecatedCard(t *testing.T) {
	root := fixture(t)
	ctx := build(t, root, query.Request{Source: "s-20260412-demo"})
	if strings.Contains(dump(ctx), "c-20260412-old") {
		t.Fatalf("deprecated 卡不应出现在任何字段：\n%s", dump(ctx))
	}
}

// —— ④ EG-NOTE-04 字段级断言 ——
//
// 与目标原文高度相似的材料笔记：其 n-… ID 只出现在**材料层字段** Notes，
// 不出现在候选相似卡、不出现在收敛输入字段（Cards）。
// base 是「可能被本次加工修改的文件」的并发保护映射（§4.5），不是收敛输入：
// 笔记按**路径键**入 base 是 Scope 的明确要求，故此处断言 base 中不得出现裸 ID 键。
func TestContextNoteStaysMaterialLayer(t *testing.T) {
	root := fixture(t)
	// 让笔记标题与原文标题完全一致，制造「高度相似」。
	p := filepath.Join(root, "domains/ai-infra/notes/n-20260412-demo.md")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(raw, []byte("\n# 注意力机制入门\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := build(t, root, query.Request{Source: "s-20260412-demo"})

	const noteID = "n-20260412-demo"
	var where []string
	for _, n := range ctx.Notes {
		if n.ID == noteID {
			where = append(where, "notes[].id")
		}
	}
	for _, c := range ctx.Cards {
		if strings.Contains(c.ID+c.Path+c.Title+strings.Join(c.Tags, ","), noteID) {
			where = append(where, "cards[]")
		}
	}
	for _, c := range ctx.Candidates {
		if strings.Contains(c.ID+c.Path+c.Title+strings.Join(c.Reasons, ","), noteID) {
			where = append(where, "candidates[]")
		}
	}
	if !reflect.DeepEqual(where, []string{"notes[].id"}) {
		t.Fatalf("笔记 ID 的出现位置集合 = %v，期望恰为材料层字段 [notes[].id]", where)
	}
	if _, ok := ctx.Base[noteID]; ok {
		t.Fatalf("base 应以路径为键收录笔记（并发保护），不得出现裸 ID 键")
	}
	if _, ok := ctx.Base["domains/ai-infra/notes/"+noteID+".md"]; !ok {
		t.Fatalf("base 缺已有笔记的 content_hash：%v", ctx.Base)
	}
}

// —— ⑤ base 的每个 content_hash 与随后 store 重算完全一致 ——

func TestContextBaseHashMatchesStoreRecompute(t *testing.T) {
	root := fixture(t)
	ctx := build(t, root, query.Request{Source: "s-20260412-demo"})
	if len(ctx.Base) == 0 {
		t.Fatal("base 为空")
	}
	st := store.New(root)
	for rel, hash := range ctx.Base {
		f, err := st.Read(rel)
		if err != nil {
			t.Fatalf("store.Read(%s): %v", rel, err)
		}
		if got := store.ContentHash(f.Bytes); got != hash {
			t.Fatalf("%s 的 content_hash 与 store 重算不一致：context=%s store=%s", rel, hash, got)
		}
		if !strings.HasPrefix(hash, "sha256:") {
			t.Fatalf("%s 的 content_hash 前缀不对：%s", rel, hash)
		}
	}
	// 收件区属「可能被本次加工修改」的文件，必须在 base 里。
	if _, ok := ctx.Base["unprocessed.md"]; !ok {
		t.Fatalf("base 缺 unprocessed.md：%v", ctx.Base)
	}
}

// —— ⑥ 打分确定性 + 只读零副作用 ——

func TestContextIsDeterministicAndReadOnly(t *testing.T) {
	root := fixture(t)
	before := treeSnapshot(t, root)
	first := dump(build(t, root, query.Request{Source: "s-20260412-demo"}))
	second := dump(build(t, root, query.Request{Source: "s-20260412-demo"}))
	if first != second {
		t.Fatalf("同一输入两次执行输出不同：\n%s\n----\n%s", first, second)
	}
	if after := treeSnapshot(t, root); after != before {
		t.Fatalf("eg context 必须零写入：\n前=%s\n后=%s", before, after)
	}
}

// —— ⑦ --note 入口：从笔记回到它的原文；不存在的目标报 ErrTargetNotFound ——

func TestContextResolvesTargetByNote(t *testing.T) {
	root := fixture(t)
	ctx := build(t, root, query.Request{Note: "n-20260412-demo"})
	if ctx.Source == nil || ctx.Source.ID != "s-20260412-demo" {
		t.Fatalf("--note 未回溯到原文：%+v", ctx.Source)
	}
	for _, req := range []query.Request{{Source: "s-19700101-nope"}, {Note: "n-19700101-nope"}} {
		req.Root, req.Domain = root, "ai-infra"
		if _, err := query.Build(req, store.ContentHash); err == nil ||
			!strings.Contains(err.Error(), "目标对象不存在") {
			t.Fatalf("%+v 期望 ErrTargetNotFound，实际 %v", req, err)
		}
	}
	if _, err := query.Build(query.Request{Root: root, Domain: "ai-infra", Source: "s-20260412-demo"}, nil); err == nil {
		t.Fatal("未注入 Hasher 应报错（content_hash 口径必须由 store 注入）")
	}
}

// —— 辅助 ——

// dump 把上下文摊平成可断言的文本（用于「零出现」类断言）。
func dump(ctx *query.Context) string {
	var b strings.Builder
	b.WriteString("domain=" + ctx.Domain + "\n")
	if ctx.Source != nil {
		b.WriteString("source=" + ctx.Source.ID + " " + ctx.Source.Path + " " + ctx.Source.Title + "\n")
		b.WriteString("body=" + ctx.Source.Body + "\n")
	}
	for _, n := range ctx.Notes {
		b.WriteString("note=" + n.ID + " " + n.Path + " " + n.Source + "\n")
	}
	for _, c := range ctx.Cards {
		b.WriteString("card=" + c.ID + " " + c.Path + " " + c.Title + " " + strings.Join(c.Tags, ",") + "\n")
	}
	for _, c := range ctx.Candidates {
		b.WriteString("cand=" + c.ID + " " + c.Path + " " + c.Title + " " + strings.Join(c.Reasons, ",") + "\n")
	}
	for _, c := range ctx.OpinionCandidates {
		b.WriteString("opcand=" + c.ID + " " + c.Path + " " + c.Title + " " + strings.Join(c.Reasons, ",") + "\n")
	}
	for _, k := range sortedBaseKeys(ctx.Base) {
		b.WriteString("base=" + k + " " + ctx.Base[k] + "\n")
	}
	return b.String()
}

func sortedBaseKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// treeSnapshot 采集路径 + 字节 + mtime，用于「文件一个字节都没动」的断言。
func treeSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b.WriteString(filepath.ToSlash(rel) + " " + store.ContentHash(raw) + " " +
			info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000") + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// ================= T-…-025：候选相似卡打磨（排序全序 / 命中理由 / 确定性） =================
//
// 判据来源：T-…-025 Acceptance「排序唯一且可复现」「命中理由非空且可核对」「确定性反证」；
// 技术方案 §8；M-002-m2.md 完成判据 3 / 7。打分口径本身沿用 M1（T-…-010），本组用例不推翻它，
// 只钉死「同输入同输出、同分可比、理由能回答哪个词命中哪个字段」。

// polishSource 是打磨用例的目标原文：标题「注意力机制入门」的二元词集合是
// 注意 / 意力 / 力机 / 机制 / 制入 / 入门，下面每张卡的得分都由它算出来。
const polishSource = `---
id: s-20260901-polish
url: https://example.com/polish
title: 注意力机制入门
saved_at: '2026-09-01T09:00:00+08:00'
---

# 注意力机制入门

正文占位。
`

// polishCard 造一张可控标题与 tags 的 active 卡。
func polishCard(id, title string, tags ...string) string {
	fm := "---\nid: " + id + "\ntitle: " + title + "\nstatus: active\n" +
		"created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n"
	if len(tags) > 0 {
		fm += "tags:\n"
		for _, tg := range tags {
			fm += "  - " + tg + "\n"
		}
	}
	return fm + "---\n\n## 定义\n\n正文占位。\n"
}

// polishVault 按 files（相对路径 → 内容）铺一个只含目标原文与若干卡的 vault。
func polishVault(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{"sources/s-20260901-polish.md": polishSource}
	for rel, body := range files {
		all[rel] = body
	}
	for rel, body := range all {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func polishContext(t *testing.T, root string) *query.Context {
	t.Helper()
	ctx, err := query.Build(query.Request{
		Root: root, Domain: "ai-infra", Source: "s-20260901-polish",
	}, store.ContentHash)
	if err != nil {
		t.Fatalf("query.Build: %v", err)
	}
	return ctx
}

// candSig 把候选序列摊成「ID|得分|理由条数」，用于顺序与等价断言（不含 Path，
// 因为「打乱输入」用例里两个 vault 的文件名不同，Path 本就应当不同）。
func candSig(ctx *query.Context) []string {
	out := make([]string, 0, len(ctx.Candidates))
	for _, c := range ctx.Candidates {
		out = append(out, c.ID+"|"+itoa(c.Score)+"|"+itoa(len(c.Reasons))+"|"+
			strings.Join(c.Reasons, "；"))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// —— ① 三级全序：得分降序 → 理由条数降序 → ID 升序 ——

func TestContextCandidateTotalOrder(t *testing.T) {
	// 三张卡刻意做成同分 12：
	//   k-…-a / k-…-b：标题「注意力机制」= 注意 / 意力 / 力机 / 机制 四个共同词 ×3 = 12，无 tags → 1 条理由
	//   k-…-c：标题「机制入」= 机制 / 制入 两词 ×3 = 6，tags 命中 注意 / 意力 / 力机 三词 ×2 = 6 → 12，2 条理由
	root := polishVault(t, map[string]string{
		"domains/ai-infra/knowledge/k-20260901-b.md": polishCard("k-20260901-b", "注意力机制"),
		"domains/ai-infra/knowledge/k-20260901-a.md": polishCard("k-20260901-a", "注意力机制"),
		"domains/ai-infra/knowledge/k-20260901-c.md": polishCard("k-20260901-c", "机制入", "注意", "意力", "力机"),
	})
	ctx := polishContext(t, root)
	if len(ctx.Candidates) != 3 {
		t.Fatalf("三张卡都应命中，实际 %d：%+v", len(ctx.Candidates), ctx.Candidates)
	}
	for _, c := range ctx.Candidates {
		if c.Score != 12 {
			t.Fatalf("用例前提被打破：%s 的得分应为 12，实际 %d（%v）", c.ID, c.Score, c.Reasons)
		}
	}
	// 同分：理由条数多者在前（c 有 tags + title 两条）。
	if ctx.Candidates[0].ID != "k-20260901-c" {
		t.Fatalf("同分应按命中理由条数降序，期望 k-20260901-c 在首位，实际 %v", candSig(ctx))
	}
	// 同分且同理由条数：按 ID 升序（a 在 b 前），与文件遍历顺序无关。
	if ctx.Candidates[1].ID != "k-20260901-a" || ctx.Candidates[2].ID != "k-20260901-b" {
		t.Fatalf("同分同理由条数应按 ID 升序，实际 %v", candSig(ctx))
	}
}

// —— ② 打乱输入：文件名（即遍历顺序）变了，输出序列一字不变 ——

func TestContextCandidateOrderIgnoresInputOrder(t *testing.T) {
	cards := []struct{ id, title string }{
		{"k-20260901-a", "注意力机制"},
		{"k-20260901-b", "注意力机制"},
		{"k-20260901-c", "机制入门"},
		{"k-20260901-d", "入门指南"},
	}
	// 两个 vault 里同一张卡的**文件名**不同：一个按 01…04 递增，一个按 zz…ww 递减，
	// 于是 WalkDir 的到达顺序完全相反；候选序列必须逐字相等。
	asc := map[string]string{}
	desc := map[string]string{}
	names := []string{"01", "02", "03", "04"}
	rev := []string{"zz", "yy", "xx", "ww"}
	for i, c := range cards {
		asc["domains/ai-infra/knowledge/"+names[i]+".md"] = polishCard(c.id, c.title)
		desc["domains/ai-infra/knowledge/"+rev[i]+".md"] = polishCard(c.id, c.title)
	}
	first := candSig(polishContext(t, polishVault(t, asc)))
	second := candSig(polishContext(t, polishVault(t, desc)))
	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Fatalf("打乱输入顺序后候选结果变了（排序不是全序）：\n%v\n----\n%v", first, second)
	}
	if len(first) == 0 {
		t.Fatal("用例前提被打破：应有候选卡")
	}
	// 连跑十次仍然逐字相同：无随机、无时间因素、不吃 map 迭代顺序。
	root := polishVault(t, asc)
	want := strings.Join(candSig(polishContext(t, root)), "\n")
	for i := 0; i < 10; i++ {
		if got := strings.Join(candSig(polishContext(t, root)), "\n"); got != want {
			t.Fatalf("第 %d 次执行结果与首次不同：\n%s\n----\n%s", i+2, want, got)
		}
	}
}

// —— ③ 命中理由：非空、逐条只描述一个来源字段、按字段名升序 ——

func TestContextCandidateReasons(t *testing.T) {
	root := polishVault(t, map[string]string{
		// 只在标题命中。
		"domains/ai-infra/knowledge/k-20260901-t.md": polishCard("k-20260901-t", "注意力机制"),
		// 只在 tags 命中（标题与目标无共同词）。
		"domains/ai-infra/knowledge/k-20260901-g.md": polishCard("k-20260901-g", "磁盘调度", "注意"),
		// 标题与 tags 都命中。
		"domains/ai-infra/knowledge/k-20260901-x.md": polishCard("k-20260901-x", "注意力机制", "机制"),
	})
	ctx := polishContext(t, root)
	byID := map[string]query.Candidate{}
	for _, c := range ctx.Candidates {
		if len(c.Reasons) == 0 {
			t.Fatalf("被推荐的卡必须给出命中理由：%+v", c)
		}
		byID[c.ID] = c
	}
	if len(byID) != 3 {
		t.Fatalf("三张卡都应进候选，实际 %v", candSig(ctx))
	}

	title := byID["k-20260901-t"]
	if len(title.Reasons) != 1 || !strings.HasPrefix(title.Reasons[0], "命中卡的 title 字段：") {
		t.Fatalf("只在标题命中的卡应恰一条指向 title 的理由：%v", title.Reasons)
	}
	if !strings.Contains(title.Reasons[0], "注意") {
		t.Fatalf("理由必须写明命中的词：%v", title.Reasons)
	}

	tags := byID["k-20260901-g"]
	if len(tags.Reasons) != 1 || !strings.HasPrefix(tags.Reasons[0], "命中卡的 tags 字段：") {
		t.Fatalf("只在 tags 命中的卡应恰一条指向 tags 的理由：%v", tags.Reasons)
	}
	if !strings.Contains(tags.Reasons[0], "注意") || !strings.Contains(tags.Reasons[0], "tags 值：注意") {
		t.Fatalf("tags 理由必须写明「哪个词命中了哪个 tag 值」：%v", tags.Reasons)
	}

	both := byID["k-20260901-x"]
	if len(both.Reasons) != 2 {
		t.Fatalf("两处都命中应恰两条理由（不得合并成一句）：%v", both.Reasons)
	}
	if !strings.HasPrefix(both.Reasons[0], "命中卡的 tags 字段：") ||
		!strings.HasPrefix(both.Reasons[1], "命中卡的 title 字段：") {
		t.Fatalf("理由必须按来源字段名升序（tags → title）：%v", both.Reasons)
	}
	// 字段内按命中词升序：把理由里的词切出来逐对比较。
	words := strings.Split(strings.TrimPrefix(both.Reasons[1], "命中卡的 title 字段："), "、")
	for i := 1; i < len(words); i++ {
		if words[i-1] >= words[i] {
			t.Fatalf("同一条理由内的命中词必须升序：%v", words)
		}
	}
}

// —— ④ 失效卡不进推荐（M1 口径不回归）+ 零理由的卡不进输出 ——

func TestContextDeprecatedNotRecommended(t *testing.T) {
	dep := strings.Replace(polishCard("k-20260901-old", "注意力机制"),
		"status: active", "status: deprecated", 1)
	root := polishVault(t, map[string]string{
		"domains/ai-infra/knowledge/k-20260901-old.md": dep,
		"domains/ai-infra/knowledge/k-20260901-new.md": polishCard("k-20260901-new", "注意力机制"),
		// 与目标标题零共同词：得分 0，既不进候选，也不该被凑出理由。
		"domains/ai-infra/knowledge/k-20260901-far.md": polishCard("k-20260901-far", "磁盘调度"),
	})
	ctx := polishContext(t, root)
	for _, c := range ctx.Candidates {
		if c.ID == "k-20260901-old" {
			t.Fatalf("deprecated 卡不得进候选：%+v", c)
		}
		if c.ID == "k-20260901-far" {
			t.Fatalf("零命中的卡不得进候选：%+v", c)
		}
		if c.Score <= 0 || len(c.Reasons) == 0 {
			t.Fatalf("候选必须同时有正得分与非空理由：%+v", c)
		}
	}
	if len(ctx.Candidates) != 1 || ctx.Candidates[0].ID != "k-20260901-new" {
		t.Fatalf("只应剩一张 active 且命中的卡，实际 %v", candSig(ctx))
	}
	// cards[] 的口径不变：active 卡照常在列，失效卡不在（M1 既有断言的同款事实）。
	var ids []string
	for _, c := range ctx.Cards {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "k-20260901-far,k-20260901-new" {
		t.Fatalf("cards[] 应恰含两张 active 卡（按扫描口径），实际 %v", ids)
	}
}

// ================= T-…-006 阶段 6E：context 双候选 + candidates 兼容（D-3） =================
//
// 判据来源：schema v2 设计 §5.3 + 决策 D-3、T-…-006 Acceptance「eg context --json 同时输出
// knowledge_candidates / opinion_candidates；candidates 仍在且等于 knowledge_candidates」。
// 本组用例只钉 query.Context 侧事实（字段闭集、legacy alias 深等价、Opinion 候选复用同一
// 确定性评分/排序单点、每类独立 limit、validation 三态正交、他域/失效/删除/零分排除、
// base 同时注入 k/o 候选路径且 store 重算一致）；I1 / CLI 渲染事实在 internal/cli 侧钉。

// polishOpinion 造一条可控标题 / 状态 / validation 的观点（schema v2；必填分区「观点」）。
// 复用打分口径与知识卡同源（同一 candidates()），因此这里只提供 frontmatter 事实，
// 不重复任何评分逻辑。
func polishOpinion(id, title, status, validation string, tags ...string) string {
	fm := "---\nid: " + id + "\nstatus: " + status + "\n" +
		"created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n" +
		"title: " + title + "\nvalidation: " + validation + "\nsources: []\n"
	if len(tags) > 0 {
		fm += "tags:\n"
		for _, tg := range tags {
			fm += "  - " + tg + "\n"
		}
	}
	return fm + "---\n\n# " + title + "\n\n## 观点\n\n正文占位。\n"
}

// polishDeletedOpinion 造一条 active 但已删除（deleted_at 有值）的观点。
func polishDeletedOpinion(id, title string) string {
	return "---\nid: " + id + "\nstatus: active\n" +
		"created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n" +
		"deleted_at: '2026-09-02T10:00:00+08:00'\ndeleted_reason: 结论被推翻\n" +
		"title: " + title + "\nvalidation: rejected\nsources: []\n" +
		"---\n\n# " + title + "\n\n## 观点\n\n正文占位。\n"
}

// opinionSig 把观点候选序列摊成「ID|得分|理由条数|理由」，用于顺序与等价断言。
func opinionSig(ctx *query.Context) []string {
	out := make([]string, 0, len(ctx.OpinionCandidates))
	for _, c := range ctx.OpinionCandidates {
		out = append(out, c.ID+"|"+itoa(c.Score)+"|"+itoa(len(c.Reasons))+"|"+
			strings.Join(c.Reasons, "；"))
	}
	return out
}

// —— ① 字段键闭集 + 三候选字段都是 [] 而非 null + candidates ≡ knowledge_candidates ——

func TestContextCandidateFieldKeysClosedAndAlias(t *testing.T) {
	root := fixture(t) // 该 vault 命中一张知识卡、零观点：正好覆盖「有 knowledge、无 opinion」
	ctx := build(t, root, query.Request{Source: "s-20260412-demo"})

	raw, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("Context 不可序列化：%v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Context JSON 不可解析：%v\n%s", err, raw)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"base", "candidates", "cards", "diagnostics", "domain",
		"knowledge_candidates", "notes", "opinion_candidates", "proposals", "source", "warnings"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("Context 字段键闭集 = %v，期望 %v", keys, want)
	}
	// 三个候选字段即便为空也必须是 []（不是 null）：调用方拿到的恒是数组。
	for _, k := range []string{"candidates", "knowledge_candidates", "opinion_candidates"} {
		if string(m[k]) == "null" {
			t.Fatalf("%s 必须序列化成 [] 而非 null，实得 %s", k, m[k])
		}
	}
	// legacy alias 深等价：candidates 内容 / 顺序逐项恒等于 knowledge_candidates。
	if !reflect.DeepEqual(ctx.Candidates, ctx.KnowledgeCandidates) {
		t.Fatalf("candidates 必须逐项恒等于 knowledge_candidates：\ncandidates=%+v\nknowledge=%+v",
			ctx.Candidates, ctx.KnowledgeCandidates)
	}
	if string(m["candidates"]) != string(m["knowledge_candidates"]) {
		t.Fatalf("candidates 与 knowledge_candidates 的 JSON 必须逐字相等：\n%s\n%s",
			m["candidates"], m["knowledge_candidates"])
	}
	// 该 vault 无观点：opinion_candidates 必须为空数组，且 knowledge 侧仍命中旧语义那张卡。
	if len(ctx.OpinionCandidates) != 0 {
		t.Fatalf("无观点的 vault 里 opinion_candidates 应为空，实得 %+v", ctx.OpinionCandidates)
	}
	if len(ctx.KnowledgeCandidates) != 1 || ctx.KnowledgeCandidates[0].ID != "c-20260412-attention" {
		t.Fatalf("knowledge_candidates 未保持旧语义，实得 %v", candSig(ctx))
	}
}

// polishOpinionVault 在 polishVault（目标原文标题「注意力机制入门」）基础上，
// 额外铺一组覆盖面完整的观点：命中(三种 validation)、失效、删除、零分、他域。
func polishOpinionVault(t *testing.T) string {
	t.Helper()
	return polishVault(t, map[string]string{
		// 一张命中的知识卡：证明 knowledge 与 opinion 两路并存、互不串味。
		"domains/ai-infra/knowledge/k-20260901-kc.md": polishCard("k-20260901-kc", "注意力机制"),
		// 命中的观点，三种 validation 都应召回（validation 与候选评分正交）。
		"domains/ai-infra/opinions/o-20260901-p.md": polishOpinion("o-20260901-p", "注意力机制", "active", "pending"),
		"domains/ai-infra/opinions/o-20260901-v.md": polishOpinion("o-20260901-v", "注意力入门", "active", "validated"),
		"domains/ai-infra/opinions/o-20260901-r.md": polishOpinion("o-20260901-r", "机制入门", "active", "rejected"),
		// 失效（deprecated）观点：不进候选（active 口径）。
		"domains/ai-infra/opinions/o-20260901-dep.md": polishOpinion("o-20260901-dep", "注意力机制", "deprecated", "pending"),
		// 已删除观点：不进候选。
		"domains/ai-infra/opinions/o-20260901-del.md": polishDeletedOpinion("o-20260901-del", "注意力机制"),
		// 零共同词观点：得分 0，不进候选。
		"domains/ai-infra/opinions/o-20260901-far.md": polishOpinion("o-20260901-far", "磁盘调度", "active", "pending"),
		// 他域观点：领域由目录决定，绝不能进 ai-infra 的上下文。
		"domains/mlsys/opinions/o-20260901-x.md": polishOpinion("o-20260901-x", "注意力机制", "active", "pending"),
	})
}

// —— ② Opinion 候选：validation 三态正交召回、失效/删除/零分/他域排除、不混入 cards ——

func TestContextOpinionCandidates(t *testing.T) {
	root := polishOpinionVault(t)
	ctx := polishContext(t, root)

	got := map[string]bool{}
	for _, c := range ctx.OpinionCandidates {
		got[c.ID] = true
		if c.Score <= 0 || len(c.Reasons) == 0 {
			t.Fatalf("观点候选必须同时有正得分与非空理由：%+v", c)
		}
	}
	want := []string{"o-20260901-p", "o-20260901-r", "o-20260901-v"}
	var gotIDs []string
	for id := range got {
		gotIDs = append(gotIDs, id)
	}
	sort.Strings(gotIDs)
	if strings.Join(gotIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("opinion_candidates = %v，期望恰 %v（三种 validation 都召回、失效/删除/零分/他域排除）\nsig=%v",
			gotIDs, want, opinionSig(ctx))
	}
	// 排除项一个都不能出现在任何字段。
	blob := dump(ctx)
	for _, banned := range []string{"o-20260901-dep", "o-20260901-del", "o-20260901-far",
		"o-20260901-x", "domains/mlsys/"} {
		if strings.Contains(blob, banned) {
			t.Fatalf("排除项 %q 泄漏进 context：\n%s", banned, blob)
		}
	}
	// 观点绝不混入 cards[]（cards 只装知识卡）。
	for _, c := range ctx.Cards {
		if strings.HasPrefix(c.ID, "o-") {
			t.Fatalf("观点混进了 cards[]：%+v", c)
		}
	}
	// knowledge 侧不受影响：仅命中知识卡。
	if len(ctx.KnowledgeCandidates) != 1 || ctx.KnowledgeCandidates[0].ID != "k-20260901-kc" {
		t.Fatalf("knowledge_candidates 应只含命中的知识卡，实得 %v", candSig(ctx))
	}
	// base 同时含被选中的知识卡与观点候选路径，且 store 重算一致；未命中/排除项不进 base。
	st := store.New(root)
	mustBase := []string{
		"domains/ai-infra/knowledge/k-20260901-kc.md",
		"domains/ai-infra/opinions/o-20260901-p.md",
		"domains/ai-infra/opinions/o-20260901-v.md",
		"domains/ai-infra/opinions/o-20260901-r.md",
	}
	for _, rel := range mustBase {
		h, ok := ctx.Base[rel]
		if !ok {
			t.Fatalf("被选中的候选路径必须进 base：缺 %s（base=%v）", rel, ctx.Base)
		}
		f, err := st.Read(rel)
		if err != nil {
			t.Fatalf("store.Read(%s)：%v", rel, err)
		}
		if got := store.ContentHash(f.Bytes); got != h {
			t.Fatalf("%s 的 content_hash 与 store 重算不一致：context=%s store=%s", rel, h, got)
		}
	}
	for _, rel := range []string{
		"domains/ai-infra/opinions/o-20260901-far.md",
		"domains/ai-infra/opinions/o-20260901-dep.md",
		"domains/ai-infra/opinions/o-20260901-del.md",
	} {
		if _, ok := ctx.Base[rel]; ok {
			t.Fatalf("未命中/排除的观点不得进 base：%s", rel)
		}
	}
}

// —— ③ 每类独立 limit：知识候选与观点候选各自不超过 CandidateLimit ——

func TestContextCandidatePerTypeLimit(t *testing.T) {
	files := map[string]string{}
	// 各铺 12 张命中卡 / 12 条命中观点（标题「注意力机制」→ 同分 12），各类应各自截到 10。
	for i := 1; i <= 12; i++ {
		suffix := itoa(i)
		if i < 10 {
			suffix = "0" + suffix
		}
		files["domains/ai-infra/knowledge/k-20260901-"+suffix+".md"] =
			polishCard("k-20260901-"+suffix, "注意力机制")
		files["domains/ai-infra/opinions/o-20260901-"+suffix+".md"] =
			polishOpinion("o-20260901-"+suffix, "注意力机制", "active", "pending")
	}
	ctx := polishContext(t, polishVault(t, files))
	if len(ctx.KnowledgeCandidates) != query.CandidateLimit {
		t.Fatalf("knowledge_candidates 应截到 CandidateLimit=%d，实得 %d",
			query.CandidateLimit, len(ctx.KnowledgeCandidates))
	}
	if len(ctx.OpinionCandidates) != query.CandidateLimit {
		t.Fatalf("opinion_candidates 应独立截到 CandidateLimit=%d，实得 %d",
			query.CandidateLimit, len(ctx.OpinionCandidates))
	}
	// candidates 兼容字段跟随 knowledge：同样恰 CandidateLimit。
	if len(ctx.Candidates) != query.CandidateLimit {
		t.Fatalf("candidates（兼容）应跟随 knowledge_candidates 截到 %d，实得 %d",
			query.CandidateLimit, len(ctx.Candidates))
	}
}

// —— ④ Opinion 候选复用同一确定性排序单点：同分全序（理由条数降序 → ID 升序）——

func TestContextOpinionCandidateTotalOrder(t *testing.T) {
	// 造三条同分 12 的观点，与知识卡同款构造：两条纯标题命中（1 条理由）、一条标题+tags（2 条理由）。
	root := polishVault(t, map[string]string{
		"domains/ai-infra/opinions/o-20260901-b.md": polishOpinion("o-20260901-b", "注意力机制", "active", "pending"),
		"domains/ai-infra/opinions/o-20260901-a.md": polishOpinion("o-20260901-a", "注意力机制", "active", "validated"),
		"domains/ai-infra/opinions/o-20260901-c.md": polishOpinion("o-20260901-c", "机制入", "active", "rejected", "注意", "意力", "力机"),
	})
	ctx := polishContext(t, root)
	if len(ctx.OpinionCandidates) != 3 {
		t.Fatalf("三条观点都应命中，实际 %d：%v", len(ctx.OpinionCandidates), opinionSig(ctx))
	}
	for _, c := range ctx.OpinionCandidates {
		if c.Score != 12 {
			t.Fatalf("用例前提被打破：%s 得分应为 12，实际 %d（%v）", c.ID, c.Score, c.Reasons)
		}
	}
	// 同分：理由条数多者在前（c 有 tags + title 两条）。
	if ctx.OpinionCandidates[0].ID != "o-20260901-c" {
		t.Fatalf("同分应按理由条数降序，期望 o-20260901-c 首位，实得 %v", opinionSig(ctx))
	}
	// 同分且同理由条数：按 ID 升序（a 在 b 前），与遍历顺序无关。
	if ctx.OpinionCandidates[1].ID != "o-20260901-a" || ctx.OpinionCandidates[2].ID != "o-20260901-b" {
		t.Fatalf("同分同理由条数应按 ID 升序，实得 %v", opinionSig(ctx))
	}
}

// ================= D-3：candidates 弃用提示 I1 由 query.Context.Diagnostics 产出 =================
//
// 判据来源：schema v2 设计 §5.3 / 决策 D-3 + T-…-006 Scope「弃用 I1 由 query context 的
// diagnostics 产出」。要害是：query.Build 的**直接消费者**（不经 CLI）就必须能看到这条 I1，
// 否则 query 与 CLI 各持一套事实。本组只钉 query 侧结构性事实——码 = I1、level = info、
// path = candidates、消息明确「candidates 已弃用，改读 knowledge_candidates」，**无条件恰一条**，
// 且与 Q 系列（warning、汇总扫描不完整）正交：不重复、不被带偏成 warning、绝不触发 Q3。

// diagsByCode 收集 ctx.Diagnostics 里某个码的全部条目（保序）。
func diagsByCode(ctx *query.Context, code string) []query.Diagnostic {
	var out []query.Diagnostic
	for _, d := range ctx.Diagnostics {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

// diagCodeSeq 摊平诊断码次序（用于「Q3 恒末位」这类次序断言）。
func diagCodeSeq(ctx *query.Context) []string {
	out := make([]string, 0, len(ctx.Diagnostics))
	for _, d := range ctx.Diagnostics {
		out = append(out, d.Code)
	}
	return out
}

// —— ① 干净 vault：query.Build 无条件产出恰一条 I1 info，且**别无它诊断** ——

func TestContextI1EmittedByQueryDiagnostics(t *testing.T) {
	root := fixture(t) // 无坏文件、无悬空引用：诊断集合里只应有这一条 info
	ctx := build(t, root, query.Request{Source: "s-20260412-demo"})

	i1 := diagsByCode(ctx, query.CodeI1)
	if len(i1) != 1 {
		t.Fatalf("query.Build 必须无条件产出恰一条 I1，实得 %d 条（序列 %v）",
			len(i1), diagCodeSeq(ctx))
	}
	d := i1[0]
	if d.Level != query.DiagLevelInfo {
		t.Fatalf("I1 必须是 info 级，实得 %q", d.Level)
	}
	if d.Level == query.DiagLevel {
		t.Fatalf("I1 绝不能被降级成 warning（DiagLevel），实得 %q", d.Level)
	}
	if d.Path != "candidates" {
		t.Fatalf("I1 的 path 应逐字为 candidates，实得 %q", d.Path)
	}
	if !strings.Contains(d.Message, "candidates") || !strings.Contains(d.Message, "已弃用") ||
		!strings.Contains(d.Message, "knowledge_candidates") {
		t.Fatalf("I1 消息必须明确「candidates 已弃用，改读 knowledge_candidates」：%q", d.Message)
	}
	// 干净语料：整份诊断集合就这一条，一个 Q 都不该有（尤其不能冒出 Q3）。
	if len(ctx.Diagnostics) != 1 {
		t.Fatalf("干净 vault 的诊断集合应只含 I1 一条，实得 %v", diagCodeSeq(ctx))
	}
}

// —— ② Q 系列在场：I1 仍恰一条 info，Q3 恒末位，I1 不触发 Q3 ——

func TestContextI1CoexistsWithQSeriesAndQ3StaysLast(t *testing.T) {
	root := polishVault(t, map[string]string{
		// 一张命中的知识卡（保证候选非空）+ 一张坏卡（带出 Q1 → Q3）。
		"domains/ai-infra/knowledge/k-20260901-hit.md": polishCard("k-20260901-hit", "注意力机制"),
		"domains/ai-infra/knowledge/broken.md":         "---\n- 1\n---\n\n# 坏卡\n",
	})
	ctx := polishContext(t, root)

	// I1 仍**恰一条** info：不因 Q 系列在场而重复、也不被带偏成 warning。
	i1 := diagsByCode(ctx, query.CodeI1)
	if len(i1) != 1 || i1[0].Level != query.DiagLevelInfo {
		t.Fatalf("Q 系列在场时 I1 仍须恰一条 info，实得 %+v（序列 %v）", i1, diagCodeSeq(ctx))
	}
	// 坏卡带出恰一条 Q1 + 恰一条 Q3。
	if got := len(diagsByCode(ctx, query.CodeQ1)); got != 1 {
		t.Fatalf("坏卡应带出恰一条 Q1，实得 %d（%v）", got, diagCodeSeq(ctx))
	}
	if got := len(diagsByCode(ctx, query.CodeQ3)); got != 1 {
		t.Fatalf("有 Q1 时应汇总恰一条 Q3，实得 %d（%v）", got, diagCodeSeq(ctx))
	}
	// Q3 恒末位；I1 排在诊断序列最前（code "I1" < "Q…"），可见它未被误当作汇总项去触发 Q3。
	seq := diagCodeSeq(ctx)
	if seq[len(seq)-1] != query.CodeQ3 {
		t.Fatalf("Q3 必须恒末位，实际次序 %v", seq)
	}
	if seq[0] != query.CodeI1 {
		t.Fatalf("I1 应排在诊断序列最前（info，不参与 Q 汇总），实际次序 %v", seq)
	}
}
