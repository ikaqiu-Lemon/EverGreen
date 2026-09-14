package cli

// contract_static_test.go —— M6 · T-…-075 批次 C1b-contract：写权限硬约束与退出码合同的
// **静态反证组**（合同 §2 / §9 / §17.2 ~ §17.4）。
//
// 这一组不跑真实写路径，而是对**源码本身**下断言 —— 它们钉的是「合同落地形态」这一层：
//
//	① 五个状态写口（setter）的**非测试调用点**全部落在 internal/store/：权威状态字段只能
//	   经由这五个口子改写，任何别处直接调它们都会让「写权限矩阵」形同虚设（§2.9 锁定条款）；
//	② 退出码常量表的**取值全集**恰 {0,1,2,3,4,5,6}（七值、无洞、无重复）；
//	③ R7 定稿形态的四项机器证据（V-R7-1 ~ V-R7-4，合同 §17.2）：退出码 5 只由
//	   `exitcode.go` 的 `ExitPrecheckOrLock = 5` + `exit.go` 的两处 `ExitCodeFor` 分支 +
//	   `cmd/eg/main.go` 唯一 `os.Exit` 落地，`internal/**` 一律不触发进程退出。
//
// 复用 userrequest_test.go 已有的 grepNonTest / repoRootForGuard / codeHit，不另造扫描口径。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// —— ① 五个状态写口的非测试调用点全部落在 internal/store/ ——

// stateSetterNames 是权威状态字段的五个写口（合同 §2 状态类字段的唯一改写通道）。
//
// 逐字与 internal/store/state_write.go 的五个 `func (s *Store) SetXxx` 对齐；
// 少一个就说明有写口没被这条约束覆盖，多一个就说明冒出了未登记的状态写口。
func stateSetterNames() []string {
	return []string{"SetStatus", "SetReplacedBy", "SetDeleted", "SetReviewedAt", "SetStale"}
}

// TestStateSetterCallSitesConfinedToStore：五个状态写口的**定义与非测试调用点**无一例外
// 落在 internal/store/ 之内，且调用点（排除定义行）恰五处 —— 即 state_write.go 里那一处
// 按 op 分派的 switch，每个写口恰被引用一次。
//
// 这是「写权限矩阵」在源码层的守门：矩阵判定谁能写哪一格，但若某个命令绕过 store 直接
// 调 SetStatus，矩阵就被架空了。dep_direction_gate.sh 只钉了其中三个（§13），本用例把
// 五个一次性钉齐，并额外锁死「调用点恰五处」这条计数，防止日后新增隐藏分派。
func TestStateSetterCallSitesConfinedToStore(t *testing.T) {
	defRe := regexp.MustCompile(`func \(s \*Store\) (` + strings.Join(stateSetterNames(), "|") + `)\(`)
	callRe := regexp.MustCompile(`\.(` + strings.Join(stateSetterNames(), "|") + `)\(`)

	// 定义面：五个写口的定义恰在 internal/store/，且恰五个（不多不少）。
	var defs []codeHit
	for _, dir := range []string{"internal", "cmd"} {
		defs = append(defs, grepNonTest(t, dir, defRe)...)
	}
	if len(defs) != len(stateSetterNames()) {
		t.Fatalf("状态写口定义应恰 %d 个，实得 %d：%v",
			len(stateSetterNames()), len(defs), defs)
	}
	seen := map[string]bool{}
	for _, h := range defs {
		if !strings.HasPrefix(h.file, "internal/store/") {
			t.Fatalf("状态写口定义越出 internal/store/：%s:%d %s", h.file, h.line, h.text)
		}
		for _, name := range stateSetterNames() {
			if strings.Contains(h.text, name+"(") {
				seen[name] = true
			}
		}
	}
	for _, name := range stateSetterNames() {
		if !seen[name] {
			t.Fatalf("状态写口 %s 的定义没找到：本反证会失去意义", name)
		}
	}

	// 调用面：非测试代码里对五个写口的调用（排除定义行）全部落在 internal/store/，且恰五处。
	var calls []codeHit
	for _, dir := range []string{"internal", "cmd"} {
		for _, h := range grepNonTest(t, dir, callRe) {
			if defRe.MatchString(h.text) {
				continue // 定义行由上面单管，不计入调用点
			}
			calls = append(calls, h)
		}
	}
	for _, h := range calls {
		if !strings.HasPrefix(h.file, "internal/store/") {
			t.Fatalf("状态写口的调用点越出 internal/store/（写权限矩阵被绕过）：%s:%d %s",
				h.file, h.line, h.text)
		}
	}
	if len(calls) != len(stateSetterNames()) {
		t.Fatalf("状态写口的非测试调用点应恰 %d 处（每个写口一处分派），实得 %d：%v",
			len(stateSetterNames()), len(calls), calls)
	}
}

// —— ② 退出码常量表取值全集恰 {0,1,2,3,4,5,6} ——

// TestExitCodeSurfaceIsExactlySeven：七个导出退出码常量的取值排序去重后逐字等于
// [0 1 2 3 4 5 6] —— 七值、无洞、无重复。
//
// 与 TestExitPrecheckOrLockIsFive（钉「取值 5 的常量恰一个」）互补：那支管 5 这一格，
// 本支管**整张表的取值集合**，防止日后新增一个取值 7 的码、或让某两格塌成同一个值。
func TestExitCodeSurfaceIsExactlySeven(t *testing.T) {
	surface := []int{
		ExitOK, ExitUsage, ExitValidation, ExitPartialWrite,
		ExitCommitFailed, ExitPrecheckOrLock, ExitNeedConfirm,
	}
	seen := map[int]bool{}
	for _, v := range surface {
		if seen[v] {
			t.Fatalf("退出码取值 %d 出现在多个常量上：全集必须无重复，实得 %v", v, surface)
		}
		seen[v] = true
	}
	want := []int{0, 1, 2, 3, 4, 5, 6}
	if len(seen) != len(want) {
		t.Fatalf("退出码取值全集大小 = %d，期望恰 7（{0..6}）：%v", len(seen), surface)
	}
	for _, v := range want {
		if !seen[v] {
			t.Fatalf("退出码取值全集缺 %d：必须恰是 {0,1,2,3,4,5,6}，实得 %v", v, surface)
		}
	}
}

// —— ③ R7 定稿形态的四项机器证据（V-R7-1 ~ V-R7-4）——

// TestR7StaticFourProofs：把合同 §17.2 的四项 grep 判据固化为 Go 静态反证，随包一起跑，
// 不必等到 m6_final_gate.py 才发现退出码 5 的落地形态被改动。
//
//	V-R7-1  exitcode.go 里 `ExitPrecheckOrLock = 5` 的行恰一条（缩进后即等号，不夹 const）；
//	V-R7-2  exit.go 里 `ExitPrecheckOrLock` 恰两处（ExitCodeFor 的两条 errors.As 分支）；
//	V-R7-3  internal/** 非测试代码里 `os.Exit（` 恰零处（内层一律不退进程）；
//	V-R7-4  cmd/eg/main.go 里 `os.Exit（` 恰一处，且 cmd/ 全树非测试 `os.Exit（` 恰一处。
func TestR7StaticFourProofs(t *testing.T) {
	root := repoRootForGuard(t)

	// V-R7-1：exitcode.go 的锚点行恰一条。
	v1 := regexp.MustCompile(`^\s*ExitPrecheckOrLock\s*=\s*5\s*$`)
	if n := countMatchingLines(t, filepath.Join(root, "internal/cli/exitcode.go"), v1); n != 1 {
		t.Fatalf("V-R7-1：exitcode.go 的 `ExitPrecheckOrLock = 5` 锚点行 = %d，期望恰 1", n)
	}

	// V-R7-2：exit.go 里 ExitPrecheckOrLock 恰两处。
	v2 := regexp.MustCompile(`ExitPrecheckOrLock`)
	if n := countMatchingLines(t, filepath.Join(root, "internal/cli/exit.go"), v2); n != 2 {
		t.Fatalf("V-R7-2：exit.go 的 ExitPrecheckOrLock 引用 = %d，期望恰 2（ExitCodeFor 两条分支）", n)
	}

	// V-R7-3：internal/** 非测试代码零 os.Exit（。
	exitRe := regexp.MustCompile(`os\.Exit\(`)
	if hits := grepNonTest(t, "internal", exitRe); len(hits) != 0 {
		t.Fatalf("V-R7-3：internal/** 非测试代码必须零 os.Exit（，实得 %d 处：%v", len(hits), hits)
	}

	// V-R7-4：cmd/eg/main.go 恰一处、cmd/ 全树非测试恰一处。
	if n := countMatchingLines(t, filepath.Join(root, "cmd/eg/main.go"), exitRe); n != 1 {
		t.Fatalf("V-R7-4：cmd/eg/main.go 的 os.Exit（ = %d，期望恰 1（唯一进程退出点）", n)
	}
	if hits := grepNonTest(t, "cmd", exitRe); len(hits) != 1 {
		t.Fatalf("V-R7-4：cmd/ 全树非测试 os.Exit（ = %d，期望恰 1：%v", len(hits), hits)
	}
}

// countMatchingLines 数一个文件里匹配 pat 的行数（反证用，不依赖外部 grep）。
func countMatchingLines(t *testing.T, path string, pat *regexp.Regexp) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", path, err)
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if pat.MatchString(line) {
			n++
		}
	}
	return n
}
