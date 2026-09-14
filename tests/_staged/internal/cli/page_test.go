package cli

// [S4] 分页参数面在**命令层**的机器判据（M5 索引架构合同 §8.2 A-47；T-…-068）。
//
// 三件事，逐条对应合同：
//
//	① 作用面恰三条读命令（search / card show / rel）：其余命令**不声明**这两个 flag，
//	   传入即由参数解析当场判非法 → 退 1、零写入（既有反证一条不放宽）；
//	② 负数 / 非整数 → 退出码 **4**（合同 §8.2 末行），且不新增第七个退出码；
//	③ `--replaced-by` 与分页只作用于读路径：`rel add` / `rel remove` 传入即退 1。
//
// 本文件是 T-…-068 的**新增**用例文件：M1–M4 的命令面判据（cli_test.go 等）一条不改，
// 只在参数注册表里按事实追加三个 flag（那张表本来就是逐 milestone 追加的事实表）。

import (
	"flag"
	"strings"
	"testing"
)

// pgReadCommands 是分页参数的**封闭作用面**（合同 §8.2 末段逐字：三条读命令）。
var pgReadCommands = map[string]bool{"search": true, "card": true, "rel": true}

// pgFlagNames 收集一个命令注册的私有 flag 名集合。
func pgFlagNames(c *Command) map[string]bool {
	fs := flag.NewFlagSet("eg "+c.Name, flag.ContinueOnError)
	fs.SetOutput(&strings.Builder{})
	if c.Flags != nil {
		c.Flags(fs)
	}
	got := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { got[f.Name] = true })
	return got
}

// —— ① 作用面恰三条读命令 ——

func TestPageFlagsOnlyOnThreeReadCommands(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range New().Commands() {
		flags := pgFlagNames(c)
		hasLimit, hasOffset := flags["limit"], flags["offset"]
		if hasLimit != hasOffset {
			t.Fatalf("eg %s 只注册了半套分页参数（limit=%t offset=%t）：参数集合必须成对封闭",
				c.Display, hasLimit, hasOffset)
		}
		if hasLimit != pgReadCommands[c.Name] {
			t.Fatalf("eg %s 的分页参数注册与合同作用面不符：实际 %t，期望 %t（恰三条读命令）",
				c.Display, hasLimit, pgReadCommands[c.Name])
		}
		if hasLimit {
			seen[c.Name] = true
		}
		// --replaced-by 只属 eg rel（合同 §8.4）。
		if flags["replaced-by"] != (c.Name == "rel") {
			t.Fatalf("eg %s 的 --replaced-by 注册面错：实际 %t，期望 %t",
				c.Display, flags["replaced-by"], c.Name == "rel")
		}
	}
	if len(seen) != len(pgReadCommands) {
		t.Fatalf("分页参数只出现在 %v，期望恰 %v", seen, pgReadCommands)
	}
	// 默认值来自库层单点（不许命令层各写一份 50）。
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	pageFlags(fs)
	if got := fs.Lookup("limit").DefValue; got != "50" {
		t.Fatalf("--limit 默认值 = %q，合同 §8.2 写的是 50", got)
	}
	if got := fs.Lookup("offset").DefValue; got != "0" {
		t.Fatalf("--offset 默认值 = %q，期望 0", got)
	}
}

// —— ② 负数 / 非整数 → 退出码 1（I-…-008：只读命令闭集恰 {0,1}）——
//
// 原判据锁的是「退 4」，那是**被本 issue 判定为错误**的行为：`4` 的对外语义是「Git 提交
// 失败」，一次纯参数打错的只读查询报 4 会让集成方误触发仓库修复 / 回滚 / 告警。这里把期望
// 值改成 1 并**同时加固**两侧（同族同码 + 4 未被挪用），断言数量与强度都只增不减。

func TestPageParamErrorExitsUsage(t *testing.T) {
	if ExitPageParam != 1 {
		t.Fatalf("分页参数错的退出码 = %d，M2 合同 §1.6 / §6 与三条 --help 写的是 1", ExitPageParam)
	}
	// 同族同码：`--limit -1` 与 `--since abc` 都是「参数值非法」，必须落在同一个码上。
	if ExitPageParam != ExitUsage {
		t.Fatalf("分页参数错必须与其余「参数非法」同码（ExitUsage=%d）", ExitUsage)
	}
	// 4 不被挪用：它仍**只**表示 Git 提交失败，只读路径一格都不许碰。
	if ExitPageParam == ExitCommitFailed {
		t.Fatalf("退出码 4 的语义被挪用：只读参数错不得复用「Git 提交失败」的码")
	}
	if !ExitCode5Enabled() {
		t.Fatalf("退出码 5 自 M6（T-074）起已启用；分页参数错仍归 1，不借用 5")
	}
	// 信封 status 由码唯一派生：1 → failed（零命中零写入没有「部分」语义）。
	if got := StatusFor(ExitPageParam); got != StatusFailed {
		t.Fatalf("分页参数错的 status = %q，期望 %q", got, StatusFailed)
	}

	dir := idxVault(t)
	bad := [][]string{
		{"--limit", "-1"},
		{"--limit", "abc"},
		{"--offset", "-2"},
		{"--offset", "1.5"},
		{"--limit", "3", "--offset", "-1"},
	}
	cmds := [][]string{
		{"search", "笔记"},
		{"card", "show", applyCardID},
		{"rel", applyCardID},
	}
	for _, base := range cmds {
		for _, extra := range bad {
			before := idxAuthoritySnapshot(t, dir)
			r := newTestRoot(t, dir)
			r.In = closedStdin{}
			args := append(append([]string{}, base...), "--vault", dir, "--json")
			args = append(args, extra...)
			code, _, _ := runCLI(t, r, args...)
			if code != ExitPageParam {
				t.Fatalf("eg %v %v：退出码 = %d，期望 %d（分页参数错）",
					base, extra, code, ExitPageParam)
			}
			// 参数错**零写入**：读命令本来就零写，退 1 同样一个字节都不许动。
			idxAssertAuthorityUnchanged(t, dir, before)
		}
		// 合法参数必须仍退 0（反证上面不是「一律报错」）。
		r := newTestRoot(t, dir)
		r.In = closedStdin{}
		args := append(append([]string{}, base...), "--vault", dir, "--json", "--limit", "0")
		if code, _, errOut := runCLI(t, r, args...); code != ExitOK {
			t.Fatalf("eg %v --limit 0：退出码 = %d，期望 0\n%s", base, code, errOut)
		}
	}
}

// —— ③ 只读 flag 不作用于写子命令 ——

func TestReadPathFlagsRejectedOnRelWrites(t *testing.T) {
	want := []string{"limit", "offset", "replaced-by"}
	if got := readPathOnlyFlags(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("只读 flag 集合 = %v，期望恰 %v", got, want)
	}
	dir := idxVault(t)
	for _, sub := range []string{"add", "remove"} {
		for _, extra := range [][]string{{"--limit", "1"}, {"--offset", "1"}, {"--replaced-by"}} {
			before := idxAuthoritySnapshot(t, dir)
			r := newTestRoot(t, dir)
			r.In = closedStdin{}
			args := []string{"rel", sub, applyCardID, "supports", applyCard2ID,
				"--reason", "用例", "--vault", dir, "--json"}
			args = append(args, extra...)
			code, _, _ := runCLI(t, r, args...)
			if code != ExitUsage {
				t.Fatalf("eg rel %s %v：退出码 = %d，期望 %d（写子命令不接受只读参数）",
					sub, extra, code, ExitUsage)
			}
			idxAssertAuthorityUnchanged(t, dir, before)
		}
	}
}
