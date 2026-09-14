package store

// T-…-009 的 store 侧用例：判重键、原文落盘、收件区条目登记的幂等。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureVaultDir(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, UnprocessedFile),
		[]byte("# 收件区（unprocessed）\n\n<!-- 一个顶级列表项 = 一条待处理材料；键是 source_id。 -->\n"),
		0o644); err != nil {
		t.Fatalf("seed 收件区：%v", err)
	}
	return New(root), root
}

func TestCaptureNormalizeURLIsDeterministic(t *testing.T) {
	cases := [][2]string{
		{"https://Example.COM/a/", "https://example.com/a"},
		{"https://example.com:443/a?utm_source=x&b=2#frag", "https://example.com/a?b=2"},
		{"https://example.com/a?b=2&a=1", "https://example.com/a?a=1&b=2"},
		{"  https://example.com/a  ", "https://example.com/a"},
	}
	for _, c := range cases {
		if got := NormalizeURL(c[0]); got != c[1] {
			t.Fatalf("NormalizeURL(%q) = %q，期望 %q", c[0], got, c[1])
		}
		if NormalizeURL(c[0]) != NormalizeURL(c[0]) {
			t.Fatalf("NormalizeURL(%q) 非确定性", c[0])
		}
	}
}

func TestCaptureSourceRoundTripAndInboxIdempotent(t *testing.T) {
	s, root := captureVaultDir(t)
	spec := SourceSpec{
		ID: "s-20260901-demo", URL: "https://example.com/a", Title: "示例",
		Stamp: stamp(t, "2026-09-01T10:00:00+08:00"), Reasons: []string{"打底"},
		Body: []byte("正文逐字落盘。\n"),
		Inbox: InboxEntry{Title: "示例", Stamp: stamp(t, "2026-09-01T10:00:00+08:00"),
			Reason: "打底", TargetDomain: "ai-infra"},
	}
	out, err := s.ApplySource(spec)
	if err != nil {
		t.Fatalf("ApplySource: %v", err)
	}
	if !out.Source.Written || !out.Inbox.Written || out.InboxSkip != nil {
		t.Fatalf("首次收录应写入原文与条目：%+v", out)
	}
	raw := string(mustBytes(t, filepath.Join(root, SourceRel("s-20260901-demo"))))
	if !strings.HasSuffix(raw, "正文逐字落盘。\n") {
		t.Fatalf("正文必须逐字落盘：\n%s", raw)
	}

	inboxBefore := mustBytes(t, filepath.Join(root, UnprocessedFile))
	res, skip := s.AttachEntry(spec.Inbox, spec.ID)
	if skip != nil || res.Written {
		t.Fatalf("同 source_id 再登记应幂等：%+v / %v", res, skip)
	}
	if after := mustBytes(t, filepath.Join(root, UnprocessedFile)); string(after) != string(inboxBefore) {
		t.Fatal("幂等路径必须零字节改动")
	}
}

func TestCaptureReasonAppendKeepsBodyBytes(t *testing.T) {
	s, root := captureVaultDir(t)
	rel := SourceRel("s-20260901-demo")
	if _, err := s.ApplySource(SourceSpec{
		ID: "s-20260901-demo", URL: "https://example.com/a", Title: "示例",
		Stamp: stamp(t, "2026-09-01T10:00:00+08:00"), Reasons: []string{"打底"},
		Body: []byte("正文逐字落盘。\n"), Inbox: InboxEntry{Detached: true},
	}); err != nil {
		t.Fatalf("ApplySource: %v", err)
	}
	before := string(mustBytes(t, filepath.Join(root, rel)))

	if _, err := s.ApplySourceReason(rel, "", "打底"); err != nil {
		t.Fatalf("重复理由不应报错：%v", err)
	}
	if got := string(mustBytes(t, filepath.Join(root, rel))); got != before {
		t.Fatal("重复理由必须零字节改动（幂等）")
	}
	if _, err := s.ApplySourceReason(rel, "", "第二个理由"); err != nil {
		t.Fatalf("ApplySourceReason: %v", err)
	}
	after := string(mustBytes(t, filepath.Join(root, rel)))
	if !strings.Contains(after, "- '第二个理由'") {
		t.Fatalf("理由列表应新增一条：\n%s", after)
	}
	if !strings.HasSuffix(after, "正文逐字落盘。\n") {
		t.Fatal("正文字节必须不变")
	}
}

func TestCaptureFindDuplicatePrefersURLThenTitle(t *testing.T) {
	sources := []SourceInfo{
		{ID: "s-1", URLKey: NormalizeURL("https://example.com/a"), TitleKey: "甲"},
		{ID: "s-2", URLKey: NormalizeURL("https://example.com/b"), TitleKey: "乙"},
	}
	if hit, ok := FindDuplicate(sources, "https://Example.com/a/", "完全不同"); !ok ||
		hit.Source.ID != "s-1" || hit.Key != "url" {
		t.Fatalf("URL 规范化后应命中 s-1：%+v", hit)
	}
	if hit, ok := FindDuplicate(sources, "https://other.example/z", " 乙 "); !ok ||
		hit.Source.ID != "s-2" || hit.Key != "title" {
		t.Fatalf("标题应命中 s-2：%+v", hit)
	}
	if _, ok := FindDuplicate(sources, "https://other.example/z", "丙"); ok {
		t.Fatal("两键都不命中时不得判重")
	}
}
