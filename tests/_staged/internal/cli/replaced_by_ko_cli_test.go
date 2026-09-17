package cli

// T-006 Phase 6B：`eg replaced-by` 命令写链的**跨类型端点**闭环（CLI → plan → executor → store）。
//
// 既有 deprecate_test.go 已覆盖 k→k 的单向写入；本文件补齐宿主 / 指向端为观点（o-）时的
// 三组合 k→o / o→k / o→o：宿主（知识卡或观点）落出 replaced_by，被指向端点逐字节不变。
// 走真实 store 写口与真实 git 仓，唯一注入的是时间（runStateCLI）。

import (
	"strings"
	"testing"
)

const (
	rbCLIKHost = "k-20260901-khost" // k- 宿主
	rbCLIKTo   = "k-20260902-kto"   // k- 指向端
	rbCLIOHost = "o-20260901-ohost" // o- 宿主
	rbCLIOTo   = "o-20260902-oto"   // o- 指向端
)

// rbCLIVault 建一个真实 git 仓：两张知识卡 + 两条观点全部落盘并提交，
// 作为 replaced_by 写入前的干净基线（宿主与指向端都已在盘）。
func rbCLIVault(t *testing.T) string {
	t.Helper()
	dir := captureVault(t)
	seedRelCard(t, dir, "ai-infra", rbCLIKHost, "K 宿主卡", "")
	seedRelCard(t, dir, "ai-infra", rbCLIKTo, "K 指向卡", "")
	seedRelOpinion(t, dir, "ai-infra", rbCLIOHost, "O 宿主观点", "")
	seedRelOpinion(t, dir, "ai-infra", rbCLIOTo, "O 指向观点", "")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "seed: replaced-by k/o 语料")
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：工作区必须干净，得到 %q", got)
	}
	return dir
}

// TestSetReplacedByCLIAcrossKinds：eg replaced-by 的宿主与指向端可为知识卡或观点，
// 三组合 k→o / o→k / o→o 都走通，宿主落出 replaced_by，被指向端点字节不变。
func TestSetReplacedByCLIAcrossKinds(t *testing.T) {
	cases := []struct {
		name, host, pointee string
	}{
		{"k_to_o", rbCLIKHost, rbCLIOTo},
		{"o_to_k", rbCLIOHost, rbCLIKTo},
		{"o_to_o", rbCLIOHost, rbCLIOTo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := rbCLIVault(t)
			hostPath, pointeePath := relKOHostPath(dir, tc.host), relKOHostPath(dir, tc.pointee)
			pointeeBefore := readState(t, pointeePath)

			code, _, errOut := runStateCLI(t, dir, "replaced-by",
				"--target", tc.host, "--to", tc.pointee, "--reason", "新端点已覆盖本对象")
			if code != ExitOK {
				t.Fatalf("%s：eg replaced-by 退出码 = %d，期望 0：%s", tc.name, code, errOut)
			}
			hostAfter := readState(t, hostPath)
			if !strings.Contains(hostAfter, "replaced_by:") ||
				!strings.Contains(hostAfter, "target: "+tc.pointee) {
				t.Fatalf("%s：宿主 %s 应写出指向 %s 的 replaced_by：\n%s",
					tc.name, tc.host, tc.pointee, hostAfter)
			}
			// 单向存储：被指向端点逐字节不变（绝不反写）。
			if got := readState(t, pointeePath); got != pointeeBefore {
				t.Fatalf("%s：被指向端点 %s 字节被改动：\n前\n%s\n后\n%s",
					tc.name, tc.pointee, pointeeBefore, got)
			}
		})
	}
}
