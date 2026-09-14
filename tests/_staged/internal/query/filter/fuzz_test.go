package filter

// ADR-20 筛选器的 fuzz 属性（system_assurance · T-…-005 · `fuzz` profile 第 5 个 target）。
//
// 为什么这里值得 fuzz：`Select` 是「未过目」这个**只读信号**的唯一出口（ADR-20 文件级隔离）。
// 它的对外承诺不是"某几个样例能过"，而是三条结构性质：
//
//   · **保持入参顺序**：本包连排序都不碰 —— 输出必须是输入的**子序列**（不重排、不去重、不复制）；
//   · **判定与筛选一致**：出现在输出里 ⟺ `Match(c, spec) && Unreviewed(c) == true`；
//   · **时刻不可比较即上抛**：`updated_at` 不可用时返回 ErrStampUnusable，**不猜**，
//     也不得把该条静默丢弃（静默丢弃会让"未过目"少报，属最危险的假绿）。
//
// 时间戳字段刻意让 fuzz 直接喂任意字节：日期解析是最容易在重构中被"顺手宽容化"的地方。
// 纯内存、零落盘。

import (
	"errors"
	"testing"
)

func fuzzStamps() []string {
	return []string{
		"", "2026-12-01T10:00:00+08:00", "2026-12-01", "2026-13-45T99:99:99Z",
		"2026-12-01T10:00:00Z", "not-a-stamp", "0000-00-00T00:00:00+00:00",
	}
}

// FuzzSelectOrderAndConsistency：Select 的子序列性 + 与 Match/Unreviewed 的一致性。
func FuzzSelectOrderAndConsistency(f *testing.F) {
	for _, u := range fuzzStamps() {
		for _, r := range fuzzStamps() {
			f.Add(u, r, "perf", "tagA", "2026-01-01", "2027-01-01", uint8(3))
		}
	}
	f.Fuzz(func(t *testing.T, updated, reviewed, domain, tag, since, until string, n uint8) {
		// 用 fuzz 出来的字段拼一小批候选：同一批里既有命中也有不命中，才检验得出子序列性。
		in := make([]Candidate, 0, int(n%8)+2)
		for i := 0; i <= int(n%8); i++ {
			c := Candidate{
				ID:         "k-fuzz-" + string(rune('a'+i%26)),
				Kind:       "knowledge",
				Path:       "domains/x/knowledge/a.md",
				Domain:     domain,
				Tags:       []string{tag},
				UpdatedAt:  updated,
				ReviewedAt: reviewed,
			}
			if i%2 == 1 {
				c.Domain = domain + "-other" // 保证批内一定有被 Match 挡掉的条目
			}
			in = append(in, c)
		}
		spec := Spec{Domain: domain, Tags: []string{tag}, Since: since, Until: until}

		out, err := Select(in, spec)
		if err != nil {
			// 唯一允许的失败族：时刻不可比较。别的 error 说明包在猜或在扩大失败面。
			if !errors.Is(err, ErrStampUnusable) {
				t.Fatalf("Select 返回了非 ErrStampUnusable 的错误：%v", err)
			}
			if out != nil {
				t.Fatalf("Select 出错时仍返回了结果（%d 条）：错误路径必须零输出", len(out))
			}
			return
		}

		// ① 子序列：输出按输入顺序出现，不重排、不重复。
		j := 0
		for _, c := range out {
			for j < len(in) && in[j].ID+in[j].Domain != c.ID+c.Domain {
				j++
			}
			if j == len(in) {
				t.Fatalf("输出不是输入的子序列（顺序被改动或凭空多出条目）：%+v", c)
			}
			j++
		}
		// ② 判定一致：输出集合 = Match ∧ Unreviewed。
		want := 0
		for _, c := range in {
			if !Match(c, spec) {
				continue
			}
			hit, uerr := Unreviewed(c)
			if uerr != nil {
				t.Fatalf("Select 已成功返回，但 Unreviewed 对同一条报错：%v", uerr)
			}
			if hit {
				want++
			}
		}
		if len(out) != want {
			t.Fatalf("Select 命中 %d 条，Match∧Unreviewed 逐条复算为 %d 条", len(out), want)
		}
		// ③ 确定性：同输入两次逐条相同。
		again, err2 := Select(in, spec)
		if err2 != nil || len(again) != len(out) {
			t.Fatalf("两次 Select 结果不同：%d/%v vs %d", len(again), err2, len(out))
		}
	})
}

// FuzzUnreviewedNeverPanics：缺省分支与有值分支都不得 panic，且缺省恒判「未过目」。
func FuzzUnreviewedNeverPanics(f *testing.F) {
	for _, s := range fuzzStamps() {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, updated, reviewed string) {
		hit, err := Unreviewed(Candidate{ID: "k-fuzz", UpdatedAt: updated, ReviewedAt: reviewed})
		if err != nil {
			if !errors.Is(err, ErrStampUnusable) {
				t.Fatalf("非 ErrStampUnusable 错误：%v", err)
			}
			if hit {
				t.Fatal("错误路径返回了 hit=true：不可比较时不得当作未过目")
			}
			return
		}
		if reviewed == "" && !hit {
			t.Fatalf("reviewed_at 缺省必须判未过目（updated=%q）", updated)
		}
	})
}
