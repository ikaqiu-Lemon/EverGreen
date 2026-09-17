package store

// SetReplacedBy 的 target 端点封闭性（Schema v2 §9.2 / T-009 Acceptance）：
//
// 迁移把一条判断从知识卡改成观点时，原 k- 卡逻辑删除并用 `replaced_by` 指向新 o- 观点，
// 因此 `replaced_by.target` 必须接受 k- / o- 两种端点（不再收窄成知识卡 ID）。
// 模型与 store 直接写口上，target 是 k/o 封闭端点：k→k / k→o / o→k / o→o 四组合都接受，
// s- / n- / r- / p- / 畸形 / 空一律拒绝（errors.Is 命中既有错误），且拒绝时零字节改动。
//
// 单向存储不变：只写宿主 rel 这一份文件，被指向的目标文件绝不打开、绝不改写；
// 序列化仍是 `replaced_by: {target: <id>, reason: "<text>"}` 单行两键形态。

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// opinionHostSample 是一份可作为宿主的观点 frontmatter（含 validation 键）。
const opinionHostSample = `---
id: o-20260901-host
title: 观点宿主
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
validation: pending
sources:
  - source: s-20260901-spec
    note: n-20260901-spec
    rel: support
    reason: 合同 §7.1
tags:
  - m3
vendor_unknown_key: 未知字段逐字保留
---

## 观点内容

正文一个字节都不许动。
`

// TestSetReplacedByAcceptsKnowledgeAndOpinionEndpoints：k/o 宿主 × k/o 目标共四组合，
// 全部接受；只改宿主的 replaced_by 单键、目标文件零字节改动、序列化两键形态不变。
func TestSetReplacedByAcceptsKnowledgeAndOpinionEndpoints(t *testing.T) {
	cases := []struct {
		name       string
		hostRel    string
		hostSample string
		hostIsCard bool
		target     string
	}{
		{"k->k", "cards/k-20260901-state.md", stateCardSample, true, "k-20260902-target"},
		{"k->o", "cards/k-20260901-state.md", stateCardSample, true, "o-20260902-target"},
		{"o->k", "opinions/agent/o-20260901-host.md", opinionHostSample, false, "k-20260902-target"},
		{"o->o", "opinions/agent/o-20260901-host.md", opinionHostSample, false, "o-20260902-target"},
	}
	const reason = "口径已更新，见新端点"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, root := newVault(t)
			hostAbs := writeSeed(t, root, tc.hostRel, tc.hostSample)
			hostBefore := mustBytes(t, hostAbs)
			// 被指向的目标另起一份文件，用来证明「单向存储」不反写目标。
			targetRel := "cards/" + tc.target + ".md"
			targetSample := stateCardSample
			if strings.HasPrefix(tc.target, model.PrefixOpinion) {
				targetRel = "opinions/agent/" + tc.target + ".md"
				targetSample = strings.Replace(opinionHostSample, "o-20260901-host", tc.target, 1)
			} else {
				targetSample = strings.Replace(stateCardSample, "k-20260901-state", tc.target, 1)
			}
			targetAbs := writeSeed(t, root, targetRel, targetSample)
			targetBefore := mustBytes(t, targetAbs)

			f, err := s.Read(tc.hostRel)
			if err != nil {
				t.Fatalf("read host: %v", err)
			}
			res, err := s.SetReplacedBy(tc.hostRel, f.Hash,
				model.RelationEndpoint(tc.target), reason, model.Stamp{})
			if err != nil {
				t.Fatalf("SetReplacedBy(%s) 应接受 k/o 端点：%v", tc.name, err)
			}
			if !res.Written {
				t.Fatal("hash 相符时必须落盘")
			}
			after := mustBytes(t, hostAbs)
			fm := fmOf(t, after)
			wantLine := "replaced_by: {target: " + tc.target + ", reason: \"" + reason + "\"}\n"
			if !strings.Contains(fm, wantLine) {
				t.Fatalf("replaced_by 须写成两键单行形态 %q，实得 frontmatter：%s", wantLine, fm)
			}
			// 读回：target 是 RelationEndpoint，逐字等于写入的端点 ID。
			if tc.hostIsCard {
				var c model.Card
				if err := decodeFMInto(after, &c); err != nil {
					t.Fatalf("写后 frontmatter 必须仍是合法 YAML：%v", err)
				}
				if c.ReplacedBy == nil || c.ReplacedBy.Target != model.RelationEndpoint(tc.target) ||
					c.ReplacedBy.Reason != reason {
					t.Fatalf("知识卡宿主须读回两个子字段：%+v", c.ReplacedBy)
				}
			} else {
				var o model.Opinion
				if err := decodeFMInto(after, &o); err != nil {
					t.Fatalf("写后 frontmatter 必须仍是合法 YAML：%v", err)
				}
				if o.ReplacedBy == nil || o.ReplacedBy.Target != model.RelationEndpoint(tc.target) ||
					o.ReplacedBy.Reason != reason {
					t.Fatalf("观点宿主须读回两个子字段：%+v", o.ReplacedBy)
				}
			}
			// 单向存储：目标文件一个字节都不许动。
			if !bytes.Equal(mustBytes(t, targetAbs), targetBefore) {
				t.Fatal("单向存储：被指向的目标文件一个字节都不许动")
			}
			// 只新增了 replaced_by 一行：原 frontmatter 每一行逐字仍在，且恰多出一行。
			assertReplacedByAppendedOnly(t, hostBefore, after, tc.target, reason)
			if got, want := bodyOf(t, after), bodyOf(t, hostBefore); got != want {
				t.Fatal("正文必须逐字不变")
			}
			if strings.Contains(fm, "deleted_at") || strings.Contains(fm, "deleted_reason") {
				t.Fatalf("SetReplacedBy 不得碰删除维度：%s", fm)
			}
		})
	}
}

// assertReplacedByAppendedOnly 断言：写后 frontmatter 恰比原样多一行，多出的正是
// replaced_by 两键单行，其余每一行逐字保留（不重排、不改写、不新增第二个键）。
func assertReplacedByAppendedOnly(t *testing.T, before, after []byte, target, reason string) {
	t.Helper()
	b := strings.Split(strings.TrimRight(fmOf(t, before), "\n"), "\n")
	a := strings.Split(strings.TrimRight(fmOf(t, after), "\n"), "\n")
	if len(a) != len(b)+1 {
		t.Fatalf("frontmatter 应恰多一行（新增 replaced_by），%d → %d", len(b), len(a))
	}
	for i := range b {
		if a[i] != b[i] {
			t.Fatalf("原第 %d 行须逐字保留：%q → %q", i+1, b[i], a[i])
		}
	}
	wantLine := "replaced_by: {target: " + target + ", reason: \"" + reason + "\"}"
	if a[len(a)-1] != wantLine {
		t.Fatalf("新增行须是 %q，实得 %q", wantLine, a[len(a)-1])
	}
}

// TestSetReplacedByRejectsNonEndpointTargets：非 k/o 端点（s/n/r/p/畸形）与空 target /
// 空 reason 一律拒绝，errors.Is 命中既有错误，且宿主字节零改动、目标文件不被创建。
func TestSetReplacedByRejectsNonEndpointTargets(t *testing.T) {
	badTargets := []struct {
		target  string
		wantErr error
	}{
		{"s-20260902-src", ErrRelationTargetType},
		{"n-20260902-note", ErrRelationTargetType},
		{"r-20260902-review", ErrRelationTargetType},
		{"p-20260902-proposal", ErrRelationTargetType},
		{"not-an-id", ErrRelationTargetType},
		{"", ErrReplacedByIncomplete},
	}
	for _, bt := range badTargets {
		t.Run("target="+bt.target, func(t *testing.T) {
			s, _, abs := seedStateCard(t)
			before := mustBytes(t, abs)
			f, err := s.Read("cards/k-20260901-state.md")
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			_, err = s.SetReplacedBy("cards/k-20260901-state.md", f.Hash,
				model.RelationEndpoint(bt.target), "理由", model.Stamp{})
			if err == nil {
				t.Fatalf("非法 target %q 必须拒写", bt.target)
			}
			if !errors.Is(err, bt.wantErr) {
				t.Fatalf("target %q 应命中 %v，实得 %v", bt.target, bt.wantErr, err)
			}
			if !bytes.Equal(mustBytes(t, abs), before) {
				t.Fatal("拒写时宿主字节零改动")
			}
		})
	}
}

// TestSetReplacedByRejectsEmptyReason：reason 缺失仍命中 ErrReplacedByIncomplete，零字节改动
// （合同不变：target 泛化不放宽 reason 必带这条）。
func TestSetReplacedByRejectsEmptyReason(t *testing.T) {
	s, _, abs := seedStateCard(t)
	before := mustBytes(t, abs)
	f, err := s.Read("cards/k-20260901-state.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	_, err = s.SetReplacedBy("cards/k-20260901-state.md", f.Hash,
		model.RelationEndpoint("o-20260902-target"), "", model.Stamp{})
	if !errors.Is(err, ErrReplacedByIncomplete) {
		t.Fatalf("缺 reason 应命中 ErrReplacedByIncomplete，实得 %v", err)
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("拒写时宿主字节零改动")
	}
}
