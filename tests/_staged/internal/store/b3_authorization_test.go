package store

// B3 与「用户显式路径」的关系（授权合同 §5：**授权不是豁免**）。
//
// M3 放宽的只是「谁可以发起哪个动作」（矩阵 §2 的两列取值），
// 而**写入时怎么做**一字不改：先读盘、逐文件比对 `content_hash`、不一致就跳过该文件、
// 其余文件照常写、退 3 并进报告。store 层因此**根本不认识**授权路径——
// 它连 `--user-request` 这个概念都没有，这正是 B3 不可能被授权绕开的结构性原因。

import (
	"bytes"
	"os"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// TestB3_UserExplicitPathStillHashChecked 断言：用户显式命令 + 文件已被外部改动
// → **跳过该文件**、字节不变、跳过原因是 file_changed（退 3 的唯一来源，进报告）。
func TestB3_UserExplicitPathStillHashChecked(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", cardSample)

	f, err := s.Read("cards/k.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// 用户在 CLI 之外手改了同一个文件：凭据自此过期。
	changed := cardSample + "\n用户自己手动加的一行\n"
	if err := os.WriteFile(abs, []byte(changed), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}

	// 无论这次写入来自哪条路径，调用的都是同一个 WriteGuarded：
	// store 的签名里没有、也不会有任何「授权」参数可供放宽。
	res, err := s.WriteGuarded("cards/k.md", f.Hash, Edit{
		Kind:     mdfile.KindCard,
		Sections: []SectionAppend{{Section: mdfile.SecBoundary, Payload: []byte("用户要求补的边界。\n")}},
	})
	skip, ok := AsSkip(err)
	if !ok {
		t.Fatalf("用户显式路径同样必须跳过（B3 不放宽），实得 err=%v", err)
	}
	if skip.Reason != SkipFileChanged || res.Reason != SkipFileChanged {
		t.Fatalf("跳过原因必须是 %q，实得 %q / %q", SkipFileChanged, skip.Reason, res.Reason)
	}
	if CauseFor(skip.Reason) == "" {
		t.Fatalf("跳过必须有可进报告的 cause，%q 没有", skip.Reason)
	}
	if res.Written {
		t.Fatal("跳过时不得写入")
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, []byte(changed)) {
		t.Fatal("被跳过文件的字节必须逐字不变（外部改动原样保留，不做任何还原）")
	}
	assertNoTmp(t, root+"/cards")

	// 凭据更新后（重新读盘取 hash）同一次写入照常成立：跳过是**凭据过期**，不是拒绝该动作。
	f2, err := s.Read("cards/k.md")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	res2, err := s.WriteGuarded("cards/k.md", f2.Hash, Edit{
		Kind:     mdfile.KindCard,
		Sections: []SectionAppend{{Section: mdfile.SecBoundary, Payload: []byte("用户要求补的边界。\n")}},
	})
	if err != nil || !res2.Written {
		t.Fatalf("凭据刷新后必须写成，实得 written=%v err=%v", res2.Written, err)
	}
}
