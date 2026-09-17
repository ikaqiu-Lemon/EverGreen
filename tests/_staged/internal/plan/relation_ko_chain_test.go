package plan

// T-006-B2c Phase 2：论证关系写链路的**跨类型端点**端到端取证（plan 侧）。
//
// 锁的是 ChangePlan → validate → executor → store 这条链在 from/to 均为 k-/o- 时都通：
//   - add_relation / remove_relation 的 from 端可以是观点（o-），关系恒写在 from 宿主；
//   - 单条关系 op（verb=relate）执行后只写 from 宿主一个文件 —— 单计划一次原子 relate；
//   - 对端（target）文件一个字节都不动（单向存储）；
//   - 非 k/o 端点仍被拒（E2/E3），零展开、零写入。
//
// 全部用**实盘执行**复算（Execute + 真实 store），落到字节与写入清单上，
// 不用「Action 长这样」这类间接证据。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// koChainFiles 是一份含知识卡与观点的库：两张卡 + 两条观点（frontmatter 合法、无 relations 键），
// 两条观点用于覆盖 o→o 这一跨类型组合（from 与 target 均为观点）。
func koChainFiles(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"domains/ai-infra/knowledge/k-20260901-attention.md": card("k-20260901-attention"),
		"domains/ai-infra/knowledge/k-20260815-rnn.md":       card("k-20260815-rnn"),
		store.OpinionRel("ai-infra", "o-20260901-view"):      opinionFile("o-20260901-view", "长序列不经济", model.ValidationPending),
		store.OpinionRel("ai-infra", "o-20260815-alt"):       opinionFile("o-20260815-alt", "短序列足够", model.ValidationPending),
		"domains/ai-infra/notes/n-20260901-attention.md":     note("n-20260901-attention"),
		"sources/s-20260901-attention.md":                    source("s-20260901-attention"),
	}
}

// relatePlan 造一份 verb=relate 的单/多关系 plan，base 覆盖全库（含 id → hash，供 B3 与 baseCheck）。
func relatePlan(t *testing.T, files map[string]string, ops string) string {
	t.Helper()
	var base []string
	for rel, content := range files {
		base = append(base, fmt.Sprintf("%q:%q", rel, store.ContentHash([]byte(content))))
		if id := idOf(content); id != "" {
			base = append(base, fmt.Sprintf("%q:%q", id, store.ContentHash([]byte(content))))
		}
	}
	return fmt.Sprintf(`{"plan_version":%d,"verb":"relate","domain":"ai-infra",`+
		`"reason":"跨类型关系写链路","requirement_ids":["EG-KNW-04"],"convergence":[],`+
		`"base":{%s},"ops":[%s]}`, PlanVersion, strings.Join(base, ","), ops)
}

// idOf 只读取 frontmatter 的 id 字段（供 base 以 id 为键覆盖）。
func idOf(content string) string {
	var fm map[string]interface{}
	if err := store.FrontmatterInto([]byte(content), &fm); err != nil {
		return ""
	}
	if id, ok := fm["id"].(string); ok {
		return id
	}
	return ""
}

// TestAddRelationChainAcrossKinds：add_relation 的 from 端为 k- 或 o- 都能走通全链，
// 关系写在 from 宿主，对端零改动，单关系 relate 只写一个文件。
func TestAddRelationChainAcrossKinds(t *testing.T) {
	cases := []struct {
		name, from, target, fromRel, targetRel string
	}{
		{"k_to_k", "k-20260901-attention", "k-20260815-rnn",
			"domains/ai-infra/knowledge/k-20260901-attention.md",
			"domains/ai-infra/knowledge/k-20260815-rnn.md"},
		{"k_to_o", "k-20260901-attention", "o-20260901-view",
			"domains/ai-infra/knowledge/k-20260901-attention.md",
			store.OpinionRel("ai-infra", "o-20260901-view")},
		{"o_to_k", "o-20260901-view", "k-20260901-attention",
			store.OpinionRel("ai-infra", "o-20260901-view"),
			"domains/ai-infra/knowledge/k-20260901-attention.md"},
		{"o_to_o", "o-20260901-view", "o-20260815-alt",
			store.OpinionRel("ai-infra", "o-20260901-view"),
			store.OpinionRel("ai-infra", "o-20260815-alt")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := koChainFiles(t)
			op := fmt.Sprintf(`{"op":"add_relation","from":%q,"type":"supports","target":%q,`+
				`"reason":"跨类型 supports"}`, tc.from, tc.target)
			res := run(t, vault(t, files), relatePlan(t, files, op))
			if res.Failed() {
				t.Fatalf("%s：跨类型 add_relation 不应有 error：%v", tc.name, codes(res.Errors))
			}
			if len(res.Actions) != 1 {
				t.Fatalf("%s：单关系计划应恰一条 action，实得 %d", tc.name, len(res.Actions))
			}

			dir, out := execOn(t, files, res)
			// 单计划一次原子 relate：只写 from 宿主一个文件。
			if len(out.Written) != 1 || out.Written[0] != tc.fromRel {
				t.Fatalf("%s：单关系 relate 只应写 from 宿主 %s，实得 %v", tc.name, tc.fromRel, out.Written)
			}
			if len(out.Knowledge) != 1 || out.Knowledge[0].From != tc.from {
				t.Fatalf("%s：应恰记录一条关系事实，from=%s，实得 %+v", tc.name, tc.from, out.Knowledge)
			}
			// from 宿主在原有关系之上**新增**一条指向 target 的 supports，条目数恰 +1。
			before, err := store.RelationHostOf(model.RelationEndpoint(tc.from), []byte(files[tc.fromRel]))
			if err != nil {
				t.Fatalf("%s：解析 from 宿主基线：%v", tc.name, err)
			}
			fromHost, err := store.RelationHostOf(model.RelationEndpoint(tc.from),
				[]byte(readVaultFile(t, dir, tc.fromRel)))
			if err != nil {
				t.Fatalf("%s：解析 from 宿主：%v", tc.name, err)
			}
			if len(fromHost.Relations) != len(before.Relations)+1 {
				t.Fatalf("%s：from 宿主关系应恰 +1，实得 %d→%d：%+v",
					tc.name, len(before.Relations), len(fromHost.Relations), fromHost.Relations)
			}
			if !hasRelation(fromHost.Relations, model.RelationSupports, model.RelationEndpoint(tc.target)) {
				t.Fatalf("%s：from 宿主应含指向 %s 的 supports，实得 %+v", tc.name, tc.target, fromHost.Relations)
			}
			// 对端字节逐字不变（单向存储）。
			if got, want := readVaultFile(t, dir, tc.targetRel), files[tc.targetRel]; got != want {
				t.Fatalf("%s：关系只写 from 宿主，对端 %s 不得被改动", tc.name, tc.targetRel)
			}
		})
	}
}

// TestRemoveRelationChainAcrossKinds：remove_relation 的 from/target 覆盖四组合
// （k→k / k→o / o→k / o→o）都能走通全链，物理移除 from 宿主上的匹配条目，对端零改动。
func TestRemoveRelationChainAcrossKinds(t *testing.T) {
	cases := []struct {
		name, from, target, fromRel, targetRel string
	}{
		{"k_to_k", "k-20260901-attention", "k-20260815-rnn",
			"domains/ai-infra/knowledge/k-20260901-attention.md",
			"domains/ai-infra/knowledge/k-20260815-rnn.md"},
		{"k_to_o", "k-20260901-attention", "o-20260815-alt",
			"domains/ai-infra/knowledge/k-20260901-attention.md",
			store.OpinionRel("ai-infra", "o-20260815-alt")},
		{"o_to_k", "o-20260901-view", "k-20260901-attention",
			store.OpinionRel("ai-infra", "o-20260901-view"),
			"domains/ai-infra/knowledge/k-20260901-attention.md"},
		{"o_to_o", "o-20260901-view", "o-20260815-alt",
			store.OpinionRel("ai-infra", "o-20260901-view"),
			store.OpinionRel("ai-infra", "o-20260815-alt")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := koChainFiles(t)
			// from 宿主可能自带存量关系（k- 夹具带一条 limits）：一切断言相对基线计数，
			// 不写死 1/0，才能同时覆盖「宿主本就有关系」与「宿主空关系」两种起点。
			base, err := store.RelationHostOf(model.RelationEndpoint(tc.from), []byte(files[tc.fromRel]))
			if err != nil {
				t.Fatalf("%s：解析 from 宿主基线：%v", tc.name, err)
			}
			baseCount := len(base.Relations)

			// 第一步：在 from 宿主上建一条 supports（复用 add 链路，落到实盘）。
			addRes := run(t, vault(t, files), relatePlan(t, files,
				fmt.Sprintf(`{"op":"add_relation","from":%q,"type":"supports","target":%q,`+
					`"reason":"待删的 supports"}`, tc.from, tc.target)))
			if addRes.Failed() {
				t.Fatalf("%s：前置 add_relation 不应有 error：%v", tc.name, codes(addRes.Errors))
			}
			dir, _ := execOn(t, files, addRes)
			// 把落盘后的 from 宿主字节回填进 files，作为 remove 计划的新基线（对端未被 add 触碰）。
			files[tc.fromRel] = readVaultFile(t, dir, tc.fromRel)
			addedHost, err := store.RelationHostOf(model.RelationEndpoint(tc.from), []byte(files[tc.fromRel]))
			if err != nil {
				t.Fatalf("%s：解析 add 后的 from 宿主：%v", tc.name, err)
			}
			if len(addedHost.Relations) != baseCount+1 ||
				!hasRelation(addedHost.Relations, model.RelationSupports, model.RelationEndpoint(tc.target)) {
				t.Fatalf("%s：前置条件，from 宿主应在基线 %d 上恰 +1 条指向 %s 的 supports，实得 %+v",
					tc.name, baseCount, tc.target, addedHost.Relations)
			}
			targetBefore := files[tc.targetRel]

			// 第二步：点名删除它（remove_relation 属 P-U，需命令行佐证 + initiator=user）。
			rmEnv := vault(t, files)
			rmEnv.UserRequest = true
			rmRes := run(t, rmEnv, relatePlan(t, files,
				fmt.Sprintf(`{"op":"remove_relation","from":%q,"type":"supports",`+
					`"target":%q,"reason":"不再支持","initiator":"user"}`, tc.from, tc.target)))
			if rmRes.Failed() {
				t.Fatalf("%s：remove_relation 不应有 error：%v", tc.name, codes(rmRes.Errors))
			}
			if len(rmRes.Actions) != 1 {
				t.Fatalf("%s：单关系删除应恰一条 action，实得 %d", tc.name, len(rmRes.Actions))
			}
			dir2, out2 := execOnDir(t, files, rmRes)
			if len(out2.Written) != 1 || out2.Written[0] != tc.fromRel {
				t.Fatalf("%s：删除只应写 from 宿主 %s，实得 %v", tc.name, tc.fromRel, out2.Written)
			}
			if len(out2.KnowledgeRemoved) != 1 {
				t.Fatalf("%s：应恰记录一条被移除关系，实得 %+v", tc.name, out2.KnowledgeRemoved)
			}
			// 只移除本轮新增的那条 supports：宿主回落到基线计数，且该 supports 已消失，
			// 存量关系（若有）逐条保留。
			finalHost, err := store.RelationHostOf(model.RelationEndpoint(tc.from),
				[]byte(readVaultFile(t, dir2, tc.fromRel)))
			if err != nil {
				t.Fatalf("%s：解析 remove 后的 from 宿主：%v", tc.name, err)
			}
			if len(finalHost.Relations) != baseCount ||
				hasRelation(finalHost.Relations, model.RelationSupports, model.RelationEndpoint(tc.target)) {
				t.Fatalf("%s：from 宿主应回落到基线 %d 且移除指向 %s 的 supports，实得 %+v",
					tc.name, baseCount, tc.target, finalHost.Relations)
			}
			// 对端仍逐字不变。
			if got := readVaultFile(t, dir2, tc.targetRel); got != targetBefore {
				t.Fatalf("%s：删除只动 from 宿主，对端 %s 不得被改动", tc.name, tc.targetRel)
			}
		})
	}
}

// TestRelationChainRejectsNonKOEndpoint：非 k/o 端点（原文 s- / 笔记 n-）仍被拒，零展开、零写入。
func TestRelationChainRejectsNonKOEndpoint(t *testing.T) {
	for _, bad := range []string{"s-20260901-attention", "n-20260901-attention"} {
		t.Run(bad, func(t *testing.T) {
			files := koChainFiles(t)
			op := fmt.Sprintf(`{"op":"add_relation","from":"o-20260901-view","type":"supports",`+
				`"target":%q,"reason":"非法端点"}`, bad)
			res := run(t, vault(t, files), relatePlan(t, files, op))
			if !res.Failed() {
				t.Fatalf("%s：非 k/o 端点必须判 error，实得 %v", bad, codes(res.Warnings))
			}
			if len(res.Actions) != 0 {
				t.Fatalf("%s：拒绝必须零展开，实得 %d 条 action", bad, len(res.Actions))
			}
		})
	}
}

// execOnDir 与 execOn 同义，但把 files 写进新目录后返回目录与执行回执
// （remove 用例需要独立于 add 的干净盘做二次执行）。
func execOnDir(t *testing.T, files map[string]string, res *Result) (string, *ExecResult) {
	t.Helper()
	if res.Failed() {
		t.Fatalf("执行前提不成立（有 error）：%v", codes(res.Errors))
	}
	dir := t.TempDir()
	writeVault(t, dir, files)
	out := Execute(store.New(dir), res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Failures) != 0 {
		t.Fatalf("执行期不应有失败：%+v", out.Failures)
	}
	return dir, out
}

// hasRelation 报告 relations[] 里是否含指定 (type, target) 的条目（reason 不参与匹配）。
func hasRelation(rels []model.Relation, relType model.RelationType, target model.RelationEndpoint) bool {
	for _, r := range rels {
		if r.Type == relType && r.Target == target {
			return true
		}
	}
	return false
}
