package proposal_test

// T-…-033 的 ID 判据：`p-<yyyymmdd>-<3d>` 形态、同日多提案序号不冲突、
// 以及「ID 是主键、路径不是」（文件允许改名 / 移动，F2）。

import (
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
)

func mustDate(t *testing.T, raw string) model.Date {
	t.Helper()
	d, err := model.ParseDate(raw)
	if err != nil {
		t.Fatalf("ParseDate(%q)：%v", raw, err)
	}
	return d
}

// TestProposalIDShape —— ID 形态：前缀 p-、8 位日期、恰三位序号；非法形态一律拒。
func TestProposalIDShape(t *testing.T) {
	d := mustDate(t, "2026-07-01")
	for seq, want := range map[int]string{1: "p-20260701-001", 42: "p-20260701-042", 999: "p-20260701-999"} {
		got, err := proposal.NewID(d, seq)
		if err != nil {
			t.Fatalf("NewID(%d)：%v", seq, err)
		}
		if string(got) != want {
			t.Fatalf("NewID(%d) = %q，期望 %q", seq, got, want)
		}
		if !got.Valid() {
			t.Fatalf("%q 应合法", got)
		}
		date, back, err := proposal.ParseID(string(got))
		if err != nil || date != "20260701" || back != seq {
			t.Fatalf("ParseID(%q) = (%q, %d, %v)", got, date, back, err)
		}
	}
	// 序号越界：0 与 1000 都不给（三位十进制放不下 → 序号会失去固定宽度）。
	for _, seq := range []int{0, -1, 1000} {
		if _, err := proposal.NewID(d, seq); err == nil {
			t.Fatalf("NewID(%d) 必须报错", seq)
		}
	}
	// 形态非法的 ID 串：比 model.ParseID 更严，第三段必须是恰三位数字。
	for _, bad := range []string{
		"p-20260701-1", "p-20260701-0001", "p-20260701-vendor", "p-20260701-00a",
		"p-2026071-001", "k-20260701-001", "20260701-001", "p-20260701-000", "",
	} {
		if _, _, err := proposal.ParseID(bad); err == nil {
			t.Fatalf("ParseID(%q) 必须报错", bad)
		}
		if proposal.ID(bad).Valid() {
			t.Fatalf("%q 应判非法", bad)
		}
	}
}

// TestProposalIDSameDaySeqNoConflict —— 同日多提案序号不冲突：
// NextSeq 只看「已用序号集合」，不读时钟、不读盘，结果可复算。
func TestProposalIDSameDaySeqNoConflict(t *testing.T) {
	d := mustDate(t, "2026-07-01")
	var used []proposal.ID
	seen := map[proposal.ID]bool{}
	for i := 0; i < 5; i++ {
		seq, err := proposal.NextSeq(d.Compact(), used)
		if err != nil {
			t.Fatalf("NextSeq：%v", err)
		}
		if seq != i+1 {
			t.Fatalf("第 %d 次 NextSeq = %d，期望 %d", i+1, seq, i+1)
		}
		id, err := proposal.NewID(d, seq)
		if err != nil {
			t.Fatalf("NewID：%v", err)
		}
		if seen[id] {
			t.Fatalf("同日序号冲突：%q 重复", id)
		}
		seen[id] = true
		used = append(used, id)
	}
	// 顺序无关、重复无害：打乱 + 重复输入不改变结果。
	shuffled := []proposal.ID{used[3], used[0], used[4], used[4], used[1], used[2]}
	seq, err := proposal.NextSeq(d.Compact(), shuffled)
	if err != nil || seq != 6 {
		t.Fatalf("NextSeq(打乱) = (%d, %v)，期望 6", seq, err)
	}
	// 他日提案不参与当日序号计算。
	other := mustDate(t, "2026-07-02")
	seq, err = proposal.NextSeq(other.Compact(), used)
	if err != nil || seq != 1 {
		t.Fatalf("NextSeq(他日) = (%d, %v)，期望 1", seq, err)
	}
	// 形态非法的 ID 不参与计算（也不让整批失败）。
	seq, err = proposal.NextSeq(d.Compact(), append(append([]proposal.ID{}, used...), "p-20260701-xyz"))
	if err != nil || seq != 6 {
		t.Fatalf("NextSeq(含非法) = (%d, %v)，期望 6", seq, err)
	}
	// 当日用满 999 → 报错而不是回绕。
	if _, err := proposal.NextSeq("20260701", []proposal.ID{"p-20260701-999"}); err == nil {
		t.Fatal("当日用满 999 必须报错")
	}
}

// TestProposalRelIsDefaultNotPrimaryKey —— 路径只是默认落位：
// 提案允许改名 / 移动，定位一律靠 frontmatter 的 id（F2）。
func TestProposalRelIsDefaultNotPrimaryKey(t *testing.T) {
	id := proposal.ID("p-20260701-001")
	if rel := proposal.Rel(id); rel != "proposals/p-20260701-001.md" {
		t.Fatalf("Rel = %q", rel)
	}
	if !strings.HasPrefix(proposal.Rel(id), proposal.DirProposals+"/") {
		t.Fatalf("提案必须落在 %s/ 下", proposal.DirProposals)
	}
	// 改过名的提案仍被识别为提案控制面路径（目录说话，文件名不说话）。
	for _, rel := range []string{
		"proposals/renamed.md", "proposals/2026/p-20260701-001.md", "proposals/x.md",
	} {
		if !proposal.IsProposalRel(rel) {
			t.Fatalf("%q 必须被识别为提案控制面路径（文件允许改名 / 移动）", rel)
		}
	}
	for _, rel := range []string{
		"domains/ai-infra/knowledge/k-1.md", "sources/s-1.md", "unprocessed.md", "proposalsx/a.md",
	} {
		if proposal.IsProposalRel(rel) {
			t.Fatalf("%q 不是提案控制面路径", rel)
		}
	}
}
