package proposal

// store.go 的机器判据：读写往返、按两维状态检索、**读取零副作用**、未知内容逐字保留。
//
// 全部用例跑在真实 vault（t.TempDir() + 真实 Markdown + guarded store）上，
// 断言一律基于落盘字节，不靠返回值自证。

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// seedProposal 落一份提案并按需改 status / execution.status（改动一律走产品的字节级替换）。
func seedProposal(t *testing.T, root string, id ID, status Status, exec ExecStatus) []byte {
	t.Helper()
	raw, err := RenderTemplate(Template{
		ID: id, Title: "逻辑删除一张卡", CreatedAt: fxToday.String(),
		Targets: []string{"c-20260101-alpha"},
	})
	if err != nil {
		t.Fatalf("RenderTemplate：%v", err)
	}
	var sets []fmScalar
	if status != StatusPending {
		sets = append(sets,
			fmScalar{Key: KeyStatus, Value: string(status)},
			fmScalar{Block: KeyDecision, Key: KeyResult, Value: string(status)})
		if status == StatusRejected {
			sets = append(sets, fmScalar{Block: KeyDecision, Key: KeyReason, Value: "不采纳"})
		}
		if status == StatusSuperseded {
			sets = append(sets, fmScalar{Block: KeyDecision, Key: KeyReason, Value: "前提变化"},
				fmScalar{Block: KeyDecision, Key: KeySupersededBy, Value: "p-20261017-099"})
		}
	}
	if exec != ExecNotStarted {
		sets = append(sets, fmScalar{Block: KeyExecBlock, Key: KeyExecStatus, Value: string(exec)})
	}
	if len(sets) > 0 {
		out, err := applyFMScalars(raw, sets)
		if err != nil {
			t.Fatalf("夹具改状态失败：%v", err)
		}
		raw = out
	}
	if err := SelfCheck(raw); err != nil {
		t.Fatalf("夹具 %s 自检不过：%v", id, err)
	}
	writeFile(t, root, Rel(id), string(raw))
	return raw
}

// snapshotTree 抓 vault 下每个文件的字节与修改时刻（读取零副作用的反证依据）。
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		snap[filepath.ToSlash(rel)] = info.ModTime().String() + "|" + store.ContentHash(b)
		return nil
	})
	if err != nil {
		t.Fatalf("快照失败：%v", err)
	}
	return snap
}

// TestProposalStore_RoundTrip 写入 → 读出**逐字一致**（写口是 guarded store，不是裸写）。
func TestProposalStore_RoundTrip(t *testing.T) {
	root := t.TempDir()
	s := store.New(root)
	id := ID("p-20261017-001")
	content, err := RenderTemplate(Template{
		ID: id, Title: "逻辑删除一张卡", CreatedAt: fxToday.String(),
		Targets: []string{"c-20260101-alpha"},
	})
	if err != nil {
		t.Fatalf("RenderTemplate：%v", err)
	}
	res, err := Create(s, Template{
		ID: id, Title: "逻辑删除一张卡", CreatedAt: fxToday.String(),
		Targets: []string{"c-20260101-alpha"},
	})
	if err != nil {
		t.Fatalf("Create：%v", err)
	}
	if !res.Written || res.Path != Rel(id) {
		t.Fatalf("落位不对：written=%t path=%s，期望 %s", res.Written, res.Path, Rel(id))
	}
	rec, err := Load(s, id)
	if err != nil {
		t.Fatalf("Load：%v", err)
	}
	if !bytes.Equal(rec.Raw(), content) {
		t.Fatalf("读出字节与写入字节不逐字一致：\n写入=%q\n读出=%q", content, rec.Raw())
	}
	if rec.ID != id || rec.Rel != Rel(id) || rec.Hash != store.ContentHash(content) {
		t.Fatalf("记录字段不对：%+v", rec)
	}
	if rec.Proposal().Status != StatusPending || rec.Proposal().Execution.Status != ExecNotStarted {
		t.Fatalf("初始两维状态不对：%s / %s", rec.Proposal().Status, rec.Proposal().Execution.Status)
	}
	// 磁盘字节同样逐字一致（不信返回值，读文件反证）。
	if got := mustBytes(t, root, Rel(id)); !bytes.Equal(got, content) {
		t.Fatalf("磁盘字节与写入字节不一致")
	}
}

// TestProposalStore_SearchByStatusAndExec 按 status 与 execution.status 两维过滤检索。
func TestProposalStore_SearchByStatusAndExec(t *testing.T) {
	root := t.TempDir()
	s := store.New(root)
	seedProposal(t, root, "p-20261017-001", StatusPending, ExecNotStarted)
	seedProposal(t, root, "p-20261017-002", StatusApproved, ExecSucceeded)
	seedProposal(t, root, "p-20261017-003", StatusApproved, ExecFailed)
	seedProposal(t, root, "p-20261017-004", StatusRejected, ExecNotStarted)

	all, err := List(s)
	if err != nil {
		t.Fatalf("List：%v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List 得 %d 份，期望 4", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].ID >= all[i].ID {
			t.Fatalf("List 未按 ID 升序：%v", all)
		}
	}
	cases := []struct {
		name string
		f    Filter
		want []ID
	}{
		{"仅 status", Filter{Status: StatusApproved}, []ID{"p-20261017-002", "p-20261017-003"}},
		{"仅 execution", Filter{Exec: ExecNotStarted}, []ID{"p-20261017-001", "p-20261017-004"}},
		{"两维交集", Filter{Status: StatusApproved, Exec: ExecFailed}, []ID{"p-20261017-003"}},
		{"不过滤", Filter{}, []ID{"p-20261017-001", "p-20261017-002", "p-20261017-003", "p-20261017-004"}},
	}
	for _, c := range cases {
		got, err := Search(s, c.f)
		if err != nil {
			t.Fatalf("%s：Search：%v", c.name, err)
		}
		var ids []ID
		for _, rec := range got {
			ids = append(ids, rec.ID)
		}
		if len(ids) != len(c.want) {
			t.Fatalf("%s：命中 %v，期望 %v", c.name, ids, c.want)
		}
		for i := range ids {
			if ids[i] != c.want[i] {
				t.Fatalf("%s：命中 %v，期望 %v", c.name, ids, c.want)
			}
		}
	}
	// 拼错的状态名 fail fast：不得静默退化成「查不到」。
	if _, err := Search(s, Filter{Status: Status("applied")}); err == nil {
		t.Fatalf("非法 status 过滤必须报错")
	}
	if _, err := Search(s, Filter{Exec: ExecStatus("done")}); err == nil {
		t.Fatalf("非法 execution.status 过滤必须报错")
	}
}

// TestProposalStore_ReadHasNoSideEffect 读取是纯只读：字节、mtime、文件集合全不变。
//
// 提案根本没有 reviewed_at / updated_at 两键（M-6），本用例连带反证读取没有偷偷加键。
func TestProposalStore_ReadHasNoSideEffect(t *testing.T) {
	root := t.TempDir()
	s := store.New(root)
	seedProposal(t, root, "p-20261017-001", StatusPending, ExecNotStarted)
	seedProposal(t, root, "p-20261017-002", StatusApproved, ExecSucceeded)
	before := snapshotTree(t, root)

	if _, err := Load(s, "p-20261017-001"); err != nil {
		t.Fatalf("Load：%v", err)
	}
	if _, err := List(s); err != nil {
		t.Fatalf("List：%v", err)
	}
	recs, err := Search(s, Filter{Status: StatusApproved})
	if err != nil {
		t.Fatalf("Search：%v", err)
	}
	after := snapshotTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("文件集合变了：%d → %d", len(before), len(after))
	}
	for rel, sig := range before {
		got, ok := after[rel]
		if !ok {
			t.Fatalf("文件消失：%s", rel)
		}
		if got != sig {
			t.Fatalf("%s 被改动（字节或 mtime 变化）：%s → %s", rel, sig, got)
		}
	}
	for _, rec := range recs {
		for _, bad := range append(ForbiddenFMKeys(), "updated_at") {
			if bytes.Contains(rec.Raw(), []byte("\n"+bad+":")) {
				t.Fatalf("%s 出现了不该有的键 %s", rec.Rel, bad)
			}
		}
	}
	var rels []string
	for rel := range after {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	if len(rels) != 2 {
		t.Fatalf("读取后文件数 = %d，期望仍是 2：%v", len(rels), rels)
	}
}

// TestProposalStore_UnknownContentPreserved frontmatter 未知键与正文文字逐字保留。
func TestProposalStore_UnknownContentPreserved(t *testing.T) {
	root := t.TempDir()
	s := store.New(root)
	id := ID("p-20261017-001")
	raw := seedProposal(t, root, id, StatusPending, ExecNotStarted)
	// 塞一个未知顶层键 + 一段用户正文：读出必须与磁盘逐字一致。
	mixed := strings.Replace(string(raw), KeyDecision+":\n",
		"x_unknown_key: '手工加的键'\n"+KeyDecision+":\n", 1)
	mixed = strings.Replace(mixed, "## "+SecAlternatives+"\n",
		"## "+SecAlternatives+"\n\n"+fxUserContent+"\n", 1)
	writeFile(t, root, Rel(id), mixed)

	rec, err := Load(s, id)
	if err != nil {
		t.Fatalf("Load（含未知键的提案必须仍可读出）：%v", err)
	}
	if string(rec.Raw()) != mixed {
		t.Fatalf("读出字节与磁盘不逐字一致")
	}
	if !bytes.Contains(rec.Raw(), []byte("x_unknown_key: '手工加的键'")) ||
		!bytes.Contains(rec.Raw(), []byte(fxUserContent)) {
		t.Fatalf("未知键或用户正文丢失")
	}
	if _, ok := rec.Proposal().Extra["x_unknown_key"]; !ok {
		t.Fatalf("未知键未落进 Extra：%v", rec.Proposal().Extra)
	}
	if _, err := Load(s, "p-20261017-777"); err == nil {
		t.Fatalf("不存在的提案必须报错（引用校验据此判不存在）")
	}
}
