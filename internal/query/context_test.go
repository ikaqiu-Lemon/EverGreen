package query_test

// T-…-010 的 internal/query 侧验收：白名单组装、他域隔离、失效卡排除、
// EG-NOTE-04 字段级断言、content_hash 与 store 重算交叉一致、打分确定性、只读零副作用。

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"evergreen/internal/query"
	"evergreen/internal/store"
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
