package plan

// T-006 Phase 6B：`set_replaced_by` 写链的**跨类型端点**闭环（plan → executor → store）。
//
// Phase 6A 已把 model.ReplacedBy.Target 与 store 直接写口收紧到 k-/o- 端点；本文件锁的是
// plan+CLI 写链把宿主 `target` 与指向端 `replaced_by.target` 一并放宽到知识卡（k-）/观点（o-）：
//   - 四组合 k→k / k→o / o→k / o→o 均成立，且**始终只写宿主一个文件**，指向端零字节变化；
//   - E10 对宿主 / 指向端「已逻辑删除」两象限等价（不论 k- 还是 o-）；
//   - W12 对 deprecated 指向端等价；
//   - o- 自指仍 E2；非 k/o 端点（s-/n-）与不存在端点仍 E2，零展开；
//   - 权限仍复用矩阵 #4（ObjectCard + FieldReplacedBy）：P-U ✅ / P-A 🔴。
//
// 判据落在**落盘字节**与**诊断**上：不满足于「Action 长这样」，而用真实 store 复算。
// 矩阵计数（对象 8 / 50 行 / 100 格）由 matrix_test.go 单独钉死，此处不重复表格。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// rbHostRel 按端点前缀取宿主在 vault 内的落位路径（k- 知识卡 / o- 观点）。
func rbHostRel(id string) string {
	if strings.HasPrefix(id, "o-") {
		return store.OpinionRel("ai-infra", id)
	}
	return "domains/ai-infra/knowledge/" + id + ".md"
}

// rbSetOp 造一条 set_replaced_by op（宿主 target → 指向端 replaced_by.target，带 initiator）。
func rbSetOp(target, pointee string) string {
	return fmt.Sprintf(`{"op":"set_replaced_by","target":%q,"initiator":"user",`+
		`"replaced_by":{"target":%q,"reason":"新版综述已覆盖本对象"}}`, target, pointee)
}

// rbDeprecate 把一份产物的 status: active 改成 deprecated（未删除）。
func rbDeprecate(body string) string {
	return strings.Replace(body, "status: active", "status: deprecated", 1)
}

// rbDeleted 在 created_at 之前插入逻辑删除墓碑两键（deleted_at + deleted_reason）。
func rbDeleted(body string) string {
	return strings.Replace(body, "\ncreated_at:",
		"\ndeleted_at: '2026-10-17T09:00:00+08:00'\ndeleted_reason: 与新对象重复\ncreated_at:", 1)
}

// —— ① 四组合：k→k / k→o / o→k / o→o 全链成立，只写宿主，指向端零字节变化 ——

func TestSetReplacedByChainAcrossKinds(t *testing.T) {
	cases := []struct {
		name, host, pointee string
	}{
		{"k_to_k", "k-20260901-attention", "k-20260815-rnn"},
		{"k_to_o", "k-20260901-attention", "o-20260901-view"},
		{"o_to_k", "o-20260901-view", "k-20260901-attention"},
		{"o_to_o", "o-20260901-view", "o-20260815-alt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := koChainFiles(t)
			hostRel, pointeeRel := rbHostRel(tc.host), rbHostRel(tc.pointee)
			pointeeBefore := files[pointeeRel]

			res := m3Run(t, files, rbSetOp(tc.host, tc.pointee))
			if res.Failed() {
				t.Fatalf("%s：跨类型 set_replaced_by 不应有 error：%v", tc.name, codes(res.Errors))
			}
			if len(res.Actions) != 1 || res.Actions[0].Kind != ActSetReplacedBy {
				t.Fatalf("%s：应恰展开一条替代指针 action，实得 %+v", tc.name, res.Actions)
			}

			dir, out := execOn(t, files, res)
			// 单向存储：只写宿主一个文件。
			if len(out.Written) != 1 || out.Written[0] != hostRel {
				t.Fatalf("%s：set_replaced_by 只应写宿主 %s，实得 %v", tc.name, hostRel, out.Written)
			}
			// 宿主 frontmatter 落出指向该 pointee 的 replaced_by。
			after := readVaultFile(t, dir, hostRel)
			if !strings.Contains(after, "replaced_by:") ||
				!strings.Contains(after, "target: "+tc.pointee) {
				t.Fatalf("%s：宿主应写出指向 %s 的 replaced_by：\n%s", tc.name, tc.pointee, after)
			}
			// 指向端逐字节不变（单向存储，绝不反写）。
			if got := readVaultFile(t, dir, pointeeRel); got != pointeeBefore {
				t.Fatalf("%s：替代指针只写宿主，指向端 %s 不得被改动", tc.name, pointeeRel)
			}
		})
	}
}

// —— ② E10：宿主 / 指向端「已逻辑删除」两象限等价（含 o- 端点）——

func TestSetReplacedByE10DeletedEndpointAcrossKinds(t *testing.T) {
	cases := []struct {
		name, host, pointee, deleted, wantPath string
	}{
		{"pointee_opinion_deleted", "k-20260901-attention", "o-20260901-view",
			"o-20260901-view", "replaced_by.target"},
		{"host_opinion_deleted", "o-20260901-view", "k-20260815-rnn",
			"o-20260901-view", "target"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := koChainFiles(t)
			delRel := rbHostRel(tc.deleted)
			files[delRel] = rbDeleted(files[delRel])
			hostRel := rbHostRel(tc.host)
			before := files[hostRel]

			res := m3Run(t, files, rbSetOp(tc.host, tc.pointee))
			d := requireError(t, res, E10)
			if !strings.Contains(d.Path, tc.wantPath) {
				t.Fatalf("%s：E10 字段路径应指向 %s，实得 %+v", tc.name, tc.wantPath, d)
			}
			if len(res.Actions) != 0 {
				t.Fatalf("%s：E10 必须零展开，实得 %d 条 action", tc.name, len(res.Actions))
			}
			// 零写入用真实 store 复算：即便 error 路径将来被误接上 executor，字节比对也能抓住。
			dir := t.TempDir()
			writeVault(t, dir, files)
			if got := readVaultFile(t, dir, hostRel); got != before {
				t.Fatalf("%s：E10 时宿主 %s 字节必须逐字不变", tc.name, hostRel)
			}
		})
	}
}

// —— ③ W12：deprecated 指向端（含 o-）→ 照常写入 + 进报告提示 ——

func TestSetReplacedByW12DeprecatedPointeeAcrossKinds(t *testing.T) {
	for _, pointee := range []string{"k-20260815-rnn", "o-20260815-alt"} {
		t.Run(pointee, func(t *testing.T) {
			files := koChainFiles(t)
			pRel := rbHostRel(pointee)
			files[pRel] = rbDeprecate(files[pRel])
			pointeeBefore := files[pRel]

			res := m3Run(t, files, rbSetOp("k-20260901-attention", pointee))
			if res.Failed() {
				t.Fatalf("W12 是 warning，不得拦截：%v", codes(res.Errors))
			}
			d := requireWarning(t, res, W12)
			if d.Target != pointee {
				t.Fatalf("W12 的 target 应指向 deprecated 指向端 %s，实得 %+v", pointee, d)
			}
			if len(res.Actions) != 1 || res.Actions[0].Kind != ActSetReplacedBy {
				t.Fatalf("W12 应照常展开一条 action，实得 %+v", res.Actions)
			}
			dir, out := execOn(t, files, res)
			if len(out.Written) != 1 ||
				out.Written[0] != "domains/ai-infra/knowledge/k-20260901-attention.md" {
				t.Fatalf("W12 只应写宿主卡，实得 %v", out.Written)
			}
			// deprecated 指向端仍逐字不变（单向存储）。
			if got := readVaultFile(t, dir, pRel); got != pointeeBefore {
				t.Fatalf("W12 时指向端 %s 不得被改动", pRel)
			}
		})
	}
}

// —— ④ o- 自指仍 E2；非 k/o 端点、不存在端点、真正畸形 ID 仍 E2，零展开 ——

func TestSetReplacedByRejectsSelfAndNonKO(t *testing.T) {
	cases := []struct {
		name, host, pointee string
	}{
		{"self_knowledge", "k-20260901-attention", "k-20260901-attention"},
		{"self_opinion", "o-20260901-view", "o-20260901-view"},
		{"pointee_source", "k-20260901-attention", "s-20260901-attention"},
		{"pointee_note", "k-20260901-attention", "n-20260901-attention"},
		{"host_source", "s-20260901-attention", "k-20260815-rnn"},
		{"pointee_missing_opinion", "k-20260901-attention", "o-20270101-missing"},
		{"pointee_missing_knowledge", "o-20260901-view", "k-20270101-missing"},
		// 真正畸形 ID（既非合法但不存在，也非 s-/n- 前缀）：连端点语法都不成立，
		// relationEndpoint 在 ParseRelationEndpoint 阶段即判 E2，两端逐例覆盖。
		{"host_malformed_date", "k-2026-attention", "k-20260815-rnn"},           // 日期段非 8 位
		{"host_malformed_garble", "garble", "k-20260815-rnn"},                   // 无任何已知前缀
		{"pointee_malformed_noslug", "k-20260901-attention", "k-20260901"},      // 缺 <yyyymmdd>-<slug>
		{"pointee_malformed_empty_slug", "k-20260901-attention", "o-20260901-"}, // slug 段为空
		{"pointee_malformed_garble", "o-20260901-view", "definitely-not-an-id"}, // 无任何已知前缀
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := koChainFiles(t)
			res := m3Run(t, files, rbSetOp(tc.host, tc.pointee))
			requireError(t, res, E2)
			if len(res.Actions) != 0 {
				t.Fatalf("%s：拒绝必须零展开，实得 %d 条 action", tc.name, len(res.Actions))
			}
		})
	}
}

// —— ⑤ 权限矩阵 #4（ObjectCard + FieldReplacedBy）：P-U ✅ / P-A 🔴（含 o- 宿主）——
//
// 矩阵计数（对象 8 / 50 行 / 100 格）与 #4 两格取值由 matrix_test.go 单独钉死；这里只证
// 「宿主是 o- 观点时权限口径与 k- 卡一致」：P-U 不因授权报错、P-A 退 2 零展开（N-1 反伪造，
// 六条 plan 都写了 initiator:user 却没有命令行佐证，故落回 P-A）。

func TestSetReplacedByMatrixGateAcrossKinds(t *testing.T) {
	for _, host := range []string{"k-20260901-attention", "o-20260901-view"} {
		t.Run(host, func(t *testing.T) {
			// P-U：带命令行佐证 + initiator=user → 不因授权（W7 / E6）报错。
			files := koChainFiles(t)
			puRes := m3Run(t, files, rbSetOp(host, "k-20260815-rnn"))
			for _, d := range puRes.Errors {
				if d.Code == E6 || d.Code == W7 {
					t.Fatalf("P-U 下矩阵 #4 不得因授权报错：%s %s", d.Code, d.Message)
				}
			}
			if puRes.Failed() {
				t.Fatalf("P-U 下 set_replaced_by 不应失败：%v", codes(puRes.Errors))
			}

			// P-A：无命令行佐证 → 矩阵 #4 的 auto 列 🔴，退 2、零展开（授权类 error 拦下）。
			paRes := m3RunAgent(t, koChainFiles(t), rbSetOp(host, "k-20260815-rnn"))
			if !paRes.Failed() {
				t.Fatalf("P-A 下矩阵 #4 必须拒绝，实得 warnings=%v", codes(paRes.Warnings))
			}
			if len(paRes.Actions) != 0 {
				t.Fatalf("P-A 被拒后不得展开 action，实得 %d 条", len(paRes.Actions))
			}
			// 拒绝是授权判据（W7 升 error / E6），而非端点解析等其它 error。
			if _, w7 := find(paRes.Errors, W7); !w7 {
				if _, e6 := find(paRes.Errors, E6); !e6 {
					t.Fatalf("P-A 拒绝应由授权判据（W7/E6）产出，实得 errors=%v", codes(paRes.Errors))
				}
			}
		})
	}
}
