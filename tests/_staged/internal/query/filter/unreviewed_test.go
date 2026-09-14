package filter_test

// ADR-20 判定本体的单元判据（提案与状态合同 §6.1）。
//
// 这里只测「未过目」这一个只读谓词与三类叠加条件的交集：本包既不排序也不写盘，
// 所以用例里没有任何 vault、没有任何时间「自动过期」推断。

import (
	"errors"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query/filter"
)

// TestUnreviewedPredicate 表驱动：命中 / 不命中 / 缺省计入 / 相等不算未过目 / 时刻不可用即失败。
func TestUnreviewedPredicate(t *testing.T) {
	cases := []struct {
		name    string
		cand    filter.Candidate
		want    bool
		wantErr bool
	}{
		{
			name: "updated_at > reviewed_at → 未过目",
			cand: filter.Candidate{ID: "k-1", UpdatedAt: "2026-09-01T10:00:00+08:00",
				ReviewedAt: "2026-08-31T09:00:00+08:00"},
			want: true,
		},
		{
			name: "updated_at < reviewed_at → 已过目",
			cand: filter.Candidate{ID: "k-2", UpdatedAt: "2026-08-01T10:00:00+08:00",
				ReviewedAt: "2026-09-01T09:00:00+08:00"},
			want: false,
		},
		{
			name: "两者相等 → 已过目（严格大于才算未过目：过目之后未再变动）",
			cand: filter.Candidate{ID: "k-3", UpdatedAt: "2026-09-01T10:00:00+08:00",
				ReviewedAt: "2026-09-01T10:00:00+08:00"},
			want: false,
		},
		{
			name: "无 reviewed_at → 计入（缺省视为从未过目，判定按 -∞）",
			cand: filter.Candidate{ID: "k-4", UpdatedAt: "2026-09-01T10:00:00+08:00"},
			want: true,
		},
		{
			name: "同一时刻的不同时区写法 → 按时刻比较，不按字面量比较",
			cand: filter.Candidate{ID: "k-5", UpdatedAt: "2026-09-01T10:00:00+08:00",
				ReviewedAt: "2026-09-01T02:00:00Z"},
			want: false,
		},
		{
			name:    "updated_at 缺失 → 不猜，直接失败",
			cand:    filter.Candidate{ID: "k-6", ReviewedAt: "2026-09-01T10:00:00+08:00"},
			wantErr: true,
		},
		{
			name: "reviewed_at 形态非法 → 不猜，直接失败",
			cand: filter.Candidate{ID: "k-7", UpdatedAt: "2026-09-01T10:00:00+08:00",
				ReviewedAt: "2026-09-01"},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := filter.Unreviewed(c.cand)
			if c.wantErr {
				if !errors.Is(err, filter.ErrStampUnusable) {
					t.Fatalf("期望 ErrStampUnusable，实得 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外失败：%v", err)
			}
			if got != c.want {
				t.Fatalf("Unreviewed = %v，期望 %v", got, c.want)
			}
		})
	}
}

// TestUnreviewedSelectIntersection 断言三类条件与未过目判定取交集，且**保持入参顺序**
// （排序不属本包职责：ADR-20 明令该信号不得进入排序权重）。
func TestUnreviewedSelectIntersection(t *testing.T) {
	in := []filter.Candidate{
		{ID: "k-a", Domain: "ai-infra", Tags: []string{"llm", "attention"},
			UpdatedAt: "2026-09-01T10:00:00+08:00"},
		{ID: "k-b", Domain: "product", Tags: []string{"llm"},
			UpdatedAt: "2026-09-05T10:00:00+08:00", ReviewedAt: "2026-09-06T10:00:00+08:00"},
		{ID: "k-c", Domain: "ai-infra", Tags: []string{"llm"},
			UpdatedAt: "2026-09-09T10:00:00+08:00", ReviewedAt: "2026-09-08T10:00:00+08:00"},
	}
	cases := []struct {
		name string
		spec filter.Spec
		want []string
	}{
		{"不限条件 → 两个未过目产物，顺序即入参顺序", filter.Spec{}, []string{"k-a", "k-c"}},
		{"叠加领域", filter.Spec{Domain: "ai-infra"}, []string{"k-a", "k-c"}},
		{"叠加领域 + 标签（多标签为 AND）",
			filter.Spec{Domain: "ai-infra", Tags: []string{"llm", "attention"}}, []string{"k-a"}},
		{"叠加时间闭区间（只框住 k-a）",
			filter.Spec{Since: "2026-09-01", Until: "2026-09-01"}, []string{"k-a"}},
		{"叠加时间闭区间（只框住 k-c）",
			filter.Spec{Since: "2026-09-09", Until: "2026-09-09"}, []string{"k-c"}},
		{"条件把已过目的产物框住 → 交集仍为空",
			filter.Spec{Domain: "product"}, []string{}},
		{"标签查无 → 交集为空", filter.Spec{Tags: []string{"absent"}}, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := filter.Select(in, c.spec)
			if err != nil {
				t.Fatalf("Select 失败：%v", err)
			}
			var ids []string
			for _, cand := range got {
				ids = append(ids, cand.ID)
			}
			if len(ids) != len(c.want) {
				t.Fatalf("Select = %v，期望 %v", ids, c.want)
			}
			for i := range ids {
				if ids[i] != c.want[i] {
					t.Fatalf("Select = %v，期望 %v（顺序也须逐字相同）", ids, c.want)
				}
			}
		})
	}
}

// TestUnreviewedSelectFailsFast 断言任一候选的时刻不可用即整体失败（无静默跳过）。
func TestUnreviewedSelectFailsFast(t *testing.T) {
	in := []filter.Candidate{
		{ID: "k-ok", UpdatedAt: "2026-09-01T10:00:00+08:00"},
		{ID: "k-bad", UpdatedAt: "昨天"},
	}
	if _, err := filter.Select(in, filter.Spec{}); !errors.Is(err, filter.ErrStampUnusable) {
		t.Fatalf("期望 ErrStampUnusable，实得 %v", err)
	}
}
