package reconcile

// R1（git_uncommitted / W13）的表驱动单测（M4 · T-…-050）。
//
// 四组（deliverables 逐字要求）：
//  1. TestR1GitUncommittedDetect        —— 脏工作区命中：形态、单射码、targets、detail 事实；
//  2. TestR1CleanTreeProducesNoCommit   —— 干净工作区不命中：零 finding、零 repair、零 commit 能力；
//  3. TestR1TargetsSortedAndDeduped     —— targets 去重升序（可逐字复算）；
//  4. TestR1ReadOnlyNoSideEffect        —— 只读无副作用：同输入同输出、入参不被改、磁盘零变化。
//
// 全部用例只用**内存构造**的 git.Status 快照，不起 git 子进程、不建仓库 —— 检查器是纯函数，
// 这正是把它与写口分离的收益（写口侧的「恰一次 commit」在 internal/cli 的对应用例里钉）。

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
)

// st 用 (code, path) 对构造只读状态快照（code 只作事实留痕，不参与筛选）。
func st(pairs ...[2]string) git.Status {
	var s git.Status
	for _, p := range pairs {
		s.Changes = append(s.Changes, git.Change{Code: p[0], Path: p[1]})
	}
	return s
}

// TestR1GitUncommittedDetect：脏工作区命中恰一条 git_uncommitted（W13 / warning）。
func TestR1GitUncommittedDetect(t *testing.T) {
	cases := []struct {
		name    string
		root    string
		status  git.Status
		targets []string
	}{
		{
			name:    "未跟踪的新文件（外部新建）",
			status:  st([2]string{"??", "domains/ai/knowledge/k-a1.md"}),
			targets: []string{"domains/ai/knowledge/k-a1.md"},
		},
		{
			name:    "工作区修改（编辑器直接改）",
			status:  st([2]string{" M", "domains/ai/knowledge/k-a1.md"}),
			targets: []string{"domains/ai/knowledge/k-a1.md"},
		},
		{
			name:    "已暂存但未提交",
			status:  st([2]string{"M ", "sources/s-b2.md"}),
			targets: []string{"sources/s-b2.md"},
		},
		{
			name:    "删除与重命名同样算未提交改动",
			status:  st([2]string{" D", "sources/s-b2.md"}, [2]string{"R ", "notes/n-c3.md"}),
			targets: []string{"notes/n-c3.md", "sources/s-b2.md"},
		},
		{
			name: "多分区多条目（升序）",
			status: st([2]string{"??", "unprocessed.md"},
				[2]string{" M", "domains/ai/knowledge/k-a1.md"},
				[2]string{" M", "evergreen.yml"}),
			targets: []string{"domains/ai/knowledge/k-a1.md", "evergreen.yml", "unprocessed.md"},
		},
		{
			name:    "绝对路径条目折算成 vault 相对路径",
			root:    "/tmp/vault",
			status:  st([2]string{" M", "/tmp/vault/domains/ai/knowledge/k-a1.md"}),
			targets: []string{"domains/ai/knowledge/k-a1.md"},
		},
		{
			name: "vault 外条目 / .git 元数据 / 空路径一律丢弃，剩下的仍命中",
			root: "/tmp/vault",
			status: st([2]string{" M", "../outside/x.md"},
				[2]string{"??", ".git/COMMIT_EDITMSG"},
				[2]string{"??", "   "},
				[2]string{" M", "/elsewhere/y.md"},
				[2]string{" M", "domains/ai/knowledge/k-a1.md"}),
			targets: []string{"domains/ai/knowledge/k-a1.md"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs, rs := checkR1GitUncommitted(R1StatusOf(c.root, c.status))
			if len(fs) != 1 {
				t.Fatalf("finding 条数 = %d，命中时应恰 1 条（合同 §4）", len(fs))
			}
			if len(rs) != 0 {
				t.Fatalf("RepairSpec 条数 = %d，R1 是 Git 纳管、不是知识数据修改，应恰 0 条", len(rs))
			}
			f := fs[0]
			if f.Check != CheckGitUncommitted {
				t.Errorf("check = %q，应为 %q", f.Check, CheckGitUncommitted)
			}
			if f.Severity != SeverityWarning {
				t.Errorf("severity = %q，W13 是 warning（合同 §3 第 1 行）", f.Severity)
			}
			if f.Code() != CodeW13 {
				t.Errorf("诊断码 = %q，应为 %q（单射取自真源表）", f.Code(), CodeW13)
			}
			if !reflect.DeepEqual(f.Targets, c.targets) {
				t.Errorf("targets = %v，期望 %v", f.Targets, c.targets)
			}
			if err := f.Validate(); err != nil {
				t.Errorf("finding 不合规：%v", err)
			}
			// detail 必须含可复算事实：条目数 + 全部路径 + 事实来源命令。
			for _, want := range append([]string{statusCommand, addAllSemantics}, c.targets...) {
				if !strings.Contains(f.Detail, want) {
					t.Errorf("detail 缺可复算事实 %q：%s", want, f.Detail)
				}
			}
			if !strings.Contains(f.Detail, itoa(len(c.targets))) {
				t.Errorf("detail 未含条目数 %d：%s", len(c.targets), f.Detail)
			}
		})
	}
}

// TestR1CleanTreeProducesNoCommit：干净工作区 → 零 finding、零 repair，
// 因此写口侧「零改动即零 commit」有事实基础；并结构性反证本包不具备提交能力。
func TestR1CleanTreeProducesNoCommit(t *testing.T) {
	clean := []struct {
		name   string
		status git.Status
	}{
		{"零条目（工作区与 HEAD 一致）", git.Status{}},
		{"仅 .git 元数据条目", st([2]string{"??", ".git/index.lock"})},
		{"仅 vault 外条目", st([2]string{" M", "../other/x.md"})},
		{"仅空路径条目", st([2]string{"??", ""}, [2]string{"??", "  "})},
	}
	for _, c := range clean {
		t.Run(c.name, func(t *testing.T) {
			if !c.status.IsClean() && len(UncommittedTargets(R1StatusOf("/tmp/vault", c.status))) != 0 {
				t.Fatalf("范围外条目仍被算成未提交改动：%v", c.status.Changes)
			}
			fs, rs := checkR1GitUncommitted(R1StatusOf("/tmp/vault", c.status))
			if len(fs) != 0 || len(rs) != 0 {
				t.Fatalf("干净工作区产出 %d finding / %d repair，应双零（零改动即零 commit）",
					len(fs), len(rs))
			}
			res := Run(R1StatusOf("/tmp/vault", c.status))
			if len(res.Findings) != 0 || len(res.Repairs) != 0 || res.HasError() {
				t.Fatalf("Run 在干净工作区应返回空集合，实得 %+v", res)
			}
			if len(res.CheckSet()) != 0 {
				t.Fatalf("check 集合 = %v，应为空", res.CheckSet())
			}
		})
	}
	// 结构反证：Input 只吃只读状态快照，本包没有任何 *git.Repo 句柄，
	// 因此「提交」这件事在本包内**不可表达**（提交次数只能由写口侧决定）。
	typ := reflect.TypeOf(Input{})
	for i := 0; i < typ.NumField(); i++ {
		if strings.Contains(typ.Field(i).Type.String(), "git.Repo") {
			t.Fatalf("Input.%s 持有 *git.Repo：本包必须只吃只读状态快照", typ.Field(i).Name)
		}
	}
	if _, ok := typ.FieldByName("Status"); !ok {
		t.Fatal("Input 缺 Status 字段：R1 的事实来源必须是只读状态快照")
	}
}

// TestR1TargetsSortedAndDeduped：targets 去重 + 字典序升序，可逐字复算。
func TestR1TargetsSortedAndDeduped(t *testing.T) {
	cases := []struct {
		name    string
		status  git.Status
		targets []string
	}{
		{
			name: "同一路径被多条状态记录命中 → 去重",
			status: st([2]string{"M ", "domains/ai/knowledge/k-a1.md"},
				[2]string{" M", "domains/ai/knowledge/k-a1.md"},
				[2]string{"??", "domains/ai/knowledge/k-a1.md"}),
			targets: []string{"domains/ai/knowledge/k-a1.md"},
		},
		{
			name: "输入逆序 → 输出升序",
			status: st([2]string{" M", "unprocessed.md"},
				[2]string{" M", "sources/s-b2.md"},
				[2]string{" M", "notes/n-c3.md"},
				[2]string{" M", "domains/ai/knowledge/k-a1.md"}),
			targets: []string{"domains/ai/knowledge/k-a1.md", "notes/n-c3.md",
				"sources/s-b2.md", "unprocessed.md"},
		},
		{
			name: "同一文件的两种写法（./x 与 x）归一后仍去重",
			status: st([2]string{" M", "./domains/ai/knowledge/k-a1.md"},
				[2]string{" M", "domains/ai/knowledge/k-a1.md"}),
			targets: []string{"domains/ai/knowledge/k-a1.md"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := UncommittedTargets(R1StatusOf("", c.status))
			if !reflect.DeepEqual(got, c.targets) {
				t.Fatalf("targets = %v，期望 %v（去重 + 升序）", got, c.targets)
			}
			fs, _ := checkR1GitUncommitted(R1StatusOf("", c.status))
			if len(fs) != 1 || !reflect.DeepEqual(fs[0].Targets, c.targets) {
				t.Fatalf("finding 的 targets 与 UncommittedTargets 不同源：%+v", fs)
			}
			// 升序的机器反证：逐位比较而不是「看起来有序」。
			for i := 1; i < len(got); i++ {
				if got[i-1] >= got[i] {
					t.Fatalf("targets 未严格升序去重：%v", got)
				}
			}
		})
	}
}

// TestR1ReadOnlyNoSideEffect：只读无副作用 —— 同输入同输出、入参不被改动、
// 工作目录零文件变化、注册表恰追加 R1 一项。
func TestR1ReadOnlyNoSideEffect(t *testing.T) {
	dir := t.TempDir()
	before := dirSnapshot(t, dir)

	in := R1StatusOf(dir, st([2]string{" M", "b.md"}, [2]string{"??", "a.md"}))
	first := Run(in)
	second := Run(in)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("同一输入两次 Run 结果不同：%+v vs %+v", first, second)
	}
	if len(first.Findings) != 1 || first.Findings[0].Check != CheckGitUncommitted {
		t.Fatalf("Run 未产出 R1 finding：%+v", first.Findings)
	}
	// 入参未被改动（切片内容与顺序逐字不变）。
	if in.Status.Changes[0].Path != "b.md" || in.Status.Changes[1].Path != "a.md" {
		t.Fatalf("入参 Status 被检查器改动：%+v", in.Status.Changes)
	}
	if after := dirSnapshot(t, dir); after != before {
		t.Fatalf("检查器产生了磁盘变化：%q → %q", before, after)
	}
	// 注册表：**R1 恰占一项**。判据形态在 T-…-052（R4 落地）后按实测重钉，T-…-051
	// （R2 落地）再重钉一次，T-…-053（R3 落地）第三次重钉，T-…-054（R5 落地）第四次重钉，
	// T-…-056（R7 落地）第五次重钉，T-…-055（R6 落地）第六次重钉 —— 原式写死
	// `len(checkers) == 1`，R4 / R2 / R3 / R5 / R7 / R6 注册后必然递增；本体（「一个 task
	// 恰追加一项、R1 不得重复注册」）一字不放宽，仍是「已落地项数 = 已落地 R 编号数」
	// +「R1 恰一项」两条更强的判据：项数是加法等式（1 项 R1 + 1 项 R2 + 1 项 R3 +
	// 1 项 R4 + 1 项 R5 + 1 项 R6 + 1 项 R7），且用只含 R1 事实的输入跑注册表，产出的
	// finding 与 check 集合都必须恰一条 —— R1 若被注册两次，这里立刻变红。
	// R6 落地后 R1–R7 七项全在册，本式此后不应再变（第八个 R 不在合同 §3 的封闭表内）。
	const landedR1, landedR2, landedR3, landedR4, landedR5, landedR6, landedR7 = 1, 1, 1, 1, 1, 1, 1
	if want := landedR1 + landedR2 + landedR3 + landedR4 + landedR5 + landedR6 +
		landedR7; len(checkers) != want {
		t.Fatalf("checkers 注册项 = %d，T-…-050（R1）+ T-…-051（R2）+ T-…-052（R4）+ "+
			"T-…-053（R3）+ T-…-054（R5）+ T-…-055（R6）+ T-…-056（R7）落地后应恰 %d",
			len(checkers), want)
	}
	if R1 != "R1" {
		t.Fatalf("R1 编号常量 = %q", R1)
	}
	if R2 != "R2" {
		t.Fatalf("R2 编号常量 = %q", R2)
	}
	if R3 != "R3" {
		t.Fatalf("R3 编号常量 = %q", R3)
	}
	if R4 != "R4" {
		t.Fatalf("R4 编号常量 = %q", R4)
	}
	if R5 != "R5" {
		t.Fatalf("R5 编号常量 = %q", R5)
	}
	if R6 != "R6" {
		t.Fatalf("R6 编号常量 = %q", R6)
	}
	if R7 != "R7" {
		t.Fatalf("R7 编号常量 = %q", R7)
	}
	// 唯一命中项就是 R1：这条脏快照没有 Scan / Sources / Edits / Renames / Recaps 事实
	// （R2 / R3 / R4 / R5 / R6 / R7 因此零产出），所以产出的 finding 恰 1 条、check 集合恰
	// {git_uncommitted}。
	if len(first.Findings) != landedR1 {
		t.Fatalf("finding 数 = %d，期望恰 %d（R1 不得重复注册，R2–R7 在无扫描事实时零产出）",
			len(first.Findings), landedR1)
	}
	if got := first.CheckSet(); !reflect.DeepEqual(got, []string{CheckGitUncommitted}) {
		t.Fatalf("注册表产出的 check 集合 = %v，应恰 [%s]", got, CheckGitUncommitted)
	}
}

// dirSnapshot 把目录内容折成一个可比较字符串（只读遍历）。
func dirSnapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		b.WriteString(p)
		b.WriteString(":")
		if !info.IsDir() {
			b.WriteString(itoa(int(info.Size())))
		}
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s 失败：%v", dir, err)
	}
	return b.String()
}

// itoa 是不引 strconv 的小整数转字符串（用例只用到非负数）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
