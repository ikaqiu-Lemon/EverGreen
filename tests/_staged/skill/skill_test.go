package skill

// skill 包的机器判据（T-…-062，M4 收口新增）。
//
//   - TestEmbeddedSkillMatchesSourceFile：`//go:embed SKILL.md` 内嵌的字节必须与源文件
//     skill/SKILL.md **逐字相等**——关闭 phaseA A-F-02「内嵌 SKILL.md 过期」，防止「改了源文件
//     但二进制里还是旧的」再次发生（`eg init` 会把内嵌副本逐字写进用户 vault）。
//   - TestSkillDocumentsReconcileAndCheck：内嵌规程必须覆盖 M4 新增的 `eg reconcile` / `eg check`
//     两条命令、`--include-deprecated` 只读 flag，以及「对账不是写命令前置」的明确口径。

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestEmbeddedSkillMatchesSourceFile：内嵌字节 == 源文件字节（关闭 A-F-02）。
func TestEmbeddedSkillMatchesSourceFile(t *testing.T) {
	src, err := os.ReadFile(FileName) // 与 //go:embed 同目录同名，测试在包目录内运行
	if err != nil {
		t.Fatalf("读不到源文件 %s：%v", FileName, err)
	}
	got := Content()
	if !bytes.Equal(got, src) {
		t.Fatalf("内嵌 %s 与源文件字节不一致：内嵌 %d 字节 / 源文件 %d 字节（改了源文件必须重编译以刷新 go:embed）",
			FileName, len(got), len(src))
	}
}

// TestSkillDocumentsReconcileAndCheck：内嵌规程覆盖 M4 两条命令、可见性 flag 与「对账非写前置」口径。
func TestSkillDocumentsReconcileAndCheck(t *testing.T) {
	doc := string(Content())

	// ① 覆盖 M4 新增的两条对账命令与只读可见性 flag。
	for _, want := range []string{"eg reconcile", "eg check", "--include-deprecated"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("内嵌 SKILL.md 未覆盖 %q（M4 命令 / flag 必须在规程里查得到）", want)
		}
	}

	// ② 「对账不是写命令前置」必须逐字写明（两种等价措辞任一命中即可）。
	if !strings.Contains(doc, "对账不是写命令的前置") && !strings.Contains(doc, "不作为任何写命令的前置") {
		t.Fatal("内嵌 SKILL.md 未写明「对账不是写命令前置」——agent 可能误以为每次写入前要先跑对账")
	}

	// ③ 命令总数口径：M4 起顶层 20 个（防止规程停留在 M3 的 18）。
	if !strings.Contains(doc, "20") {
		t.Fatal("内嵌 SKILL.md 未出现 M4 命令总数 20 的口径")
	}
}
