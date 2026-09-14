package cli

// M6 运行时保留条目「类型违规」下的**只读侧**行为（T-…-072 批次 B2c2）。
//
// 写入侧的对照面在 index_lock_test.go：同一处磁盘现象在 `eg index build / rebuild / sync`
// 上是 **E15 + fail closed**（零索引写）。本文件钉的是与它互补的另一半 ——
//
//	index status      只读体检：恒退 0、恰一条 W24、不改一个字节
//	search/card/rel   读路径：降级为全量 Markdown 扫描，退出码与健康态**一模一样**，
//	                  结果与全量扫描**逐字等价**，留痕恰一条 W24 + 恰一条 Q5
//
// 这条分层是最高约束「索引不可用时降级而非报错退出」的机器形态：坏掉的是**派生物**，
// 权威 Markdown 一直都在，因此读侧没有任何理由让用户看到失败。
//
// 三条纪律：
//   - **零 mock**：违规语料是真的目录 / 真的普通文件 / 真的符号链接，没有一处打桩让
//     `index.InspectRuntimeReserved` 改口；每条语料落盘后都先自检「它确实被判成违规」。
//   - **两条 symlink 语料的目标一律合法**（指向真普通文件 / 真目录）：这样 `stat` 看它
//     一切正常，只有 `lstat` 口径的判定才认得出违规 —— 正是这一格在防「把锁 / 日志
//     引到目录外」的通道。
//   - **零变化按类型 + 字节 + inode**：`.index/` 全树逐项比对 kind / 内容哈希 /
//     symlink 目标 / dev+ino，只读命令多写一个字节、或把锁换一个 inode 都会当场红。

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// —— ① 四类违规语料 ——

// rsvCase 是一条运行时保留条目**类型违规**语料。
//
// setup 只负责把违规现场摆到盘上；「它确实是一条违规」由 rsvApply 统一自检，
// 避免某条语料哪天悄悄退化成合法状态而让整条用例空转。
type rsvCase struct {
	name  string
	setup func(t *testing.T, dir string)
}

// rsvCases 返回四条语料：两种**错误类型** + 两种**指向合法目标的符号链接**。
//
// 覆盖面刻意与 index.RuntimeReservedEntries()（恰 2 项）× 2 种违规形态对齐：
//
//	run.lock  期望普通文件 → ① 变成目录        ③ 变成指向合法普通文件的 symlink
//	txn       期望目录     → ② 变成普通文件    ④ 变成指向合法目录的 symlink
func rsvCases() []rsvCase {
	return []rsvCase{
		{"run.lock 被换成目录", func(t *testing.T, dir string) {
			t.Helper()
			p := txn.LockPath(dir)
			rsvRemoveIfPresent(t, p)
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatalf("造「run.lock 是目录」语料失败：%v", err)
			}
		}},
		{"txn 被换成普通文件", func(t *testing.T, dir string) {
			t.Helper()
			p := txn.TxnRootPath(dir)
			// 真的事务日志挪到 vault 之外（不是删掉）：语料只改**类型**这一格，
			// 免得顺手把「日志内容也没了」这件无关的事混进判定。
			rsvMoveAside(t, p)
			if err := os.WriteFile(p, []byte("这不是事务日志目录\n"), 0o644); err != nil {
				t.Fatalf("造「txn 是普通文件」语料失败：%v", err)
			}
		}},
		{"run.lock 被换成指向合法普通文件的 symlink", func(t *testing.T, dir string) {
			t.Helper()
			target := filepath.Join(t.TempDir(), "lock-real")
			if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
				t.Fatalf("造链接目标失败：%v", err)
			}
			p := txn.LockPath(dir)
			rsvRemoveIfPresent(t, p)
			if err := os.Symlink(target, p); err != nil {
				t.Fatalf("造「run.lock 是 symlink」语料失败：%v", err)
			}
			// 目标必须是**合法的普通文件**：否则语料退化成「目标也坏了」，
			// 就不再是「只有 lstat 认得出」的那一类违规。
			if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
				t.Fatalf("语料前提不成立：%s 应能 stat 成普通文件，实得 %v / %v",
					txn.LockFileName, fi, err)
			}
		}},
		{"txn 被换成指向合法目录的 symlink", func(t *testing.T, dir string) {
			t.Helper()
			p := txn.TxnRootPath(dir)
			target := rsvMoveAside(t, p)
			if err := os.Symlink(target, p); err != nil {
				t.Fatalf("造「txn 是 symlink」语料失败：%v", err)
			}
			if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
				t.Fatalf("语料前提不成立：%s 应能 stat 成目录，实得 %v / %v",
					txn.TxnDirName, fi, err)
			}
		}},
	}
}

// rsvApply 落一条语料，并**自检**它确实被判成保留条目类型违规。
//
// 自检不是形式：InspectRuntimeReserved 是本文件全部断言的前提，它若因为任何原因
// 判成合法，下面「恰一条 W24」就会变成一条永远为真的空断言。
func rsvApply(t *testing.T, dir string, c rsvCase) {
	t.Helper()
	c.setup(t, dir)
	v, bad := index.InspectRuntimeReserved(index.DirPath(dir))
	if !bad {
		t.Fatalf("语料前提不成立：%q 必须被判为保留条目类型违规", c.name)
	}
	if diag := index.Inspect(index.DirPath(dir)); diag.Code != index.CodeIndexCorrupt ||
		diag.Reason != index.ReasonUnexpectedFile {
		t.Fatalf("语料 %q：体检结论 = %s / %s，期望 %s / %s（违规 %s）",
			c.name, diag.Code, diag.Reason,
			index.CodeIndexCorrupt, index.ReasonUnexpectedFile, v)
	}
}

// rsvRemoveIfPresent 非递归删掉一个条目（不存在即通过）。
func rsvRemoveIfPresent(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatalf("清掉 %s 失败：%v", path, err)
	}
}

// rsvMoveAside 把 path 整个挪到 vault 之外，返回它的新位置（原位置随之空出来）。
func rsvMoveAside(t *testing.T, path string) string {
	t.Helper()
	aside := filepath.Join(t.TempDir(), filepath.Base(path)+"-real")
	if err := os.Rename(path, aside); err != nil {
		t.Fatalf("把 %s 挪出 vault 失败：%v", path, err)
	}
	return aside
}

// —— ② `.index/` 全树「类型 + 字节 + inode」快照 ——

// rsvNode 是 `.index/` 下一个条目的可比对事实（`lstat` 口径，**不跟随 symlink**）。
type rsvNode struct {
	kind string      // dir / file / symlink / other
	hash string      // 普通文件的内容哈希（其余形态为空）
	link string      // symlink 的目标（其余形态为空）
	info os.FileInfo // 用于 os.SameFile 的 dev+ino 比对
}

// rsvTypedTree 抓 `.index/` 全树快照。
//
// 与 index_lock_test.go 的 idxRuntimeSnapshot 分工不同，本函数多做两件事：
//
//	① 记**类型**与 symlink 目标 —— 本文件的语料本身就是「类型被换掉」，
//	   只比内容哈希会让「目录变文件」这种变化在断言里看不出来；
//	② 用 WalkDir（`lstat` 口径）而不是 Walk + ReadFile —— 后者遇到「指向目录的
//	   symlink」会去读一个目录并当场失败，而那恰恰是本文件的第 ④ 条语料。
func rsvTypedTree(t *testing.T, dir string) map[string]rsvNode {
	t.Helper()
	base := index.DirPath(dir)
	out := map[string]rsvNode{}
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		fi, lerr := os.Lstat(p)
		if lerr != nil {
			return lerr
		}
		n := rsvNode{info: fi}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			n.kind = "symlink"
			target, rerr := os.Readlink(p)
			if rerr != nil {
				return rerr
			}
			n.link = target
		case fi.IsDir():
			n.kind = "dir"
		case fi.Mode().IsRegular():
			n.kind = "file"
			raw, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			n.hash = store.ContentHash(raw)
		default:
			n.kind = "other"
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = n
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("抓 %s 全树快照失败：%v", index.DirName, err)
	}
	return out
}

// rsvAssertTreeHasIndex 反证快照**不是空的**：`.index/` 里确实躺着一个真索引库。
//
// 没有这一格，「全树零变化」在一个空目录上会退化成一条恒真断言。
func rsvAssertTreeHasIndex(t *testing.T, tree map[string]rsvNode) {
	t.Helper()
	if n, ok := tree[index.DBFileName]; !ok || n.kind != "file" || n.hash == "" {
		t.Fatalf("快照里没有真索引库 %s（实得 %+v）：「零变化」会退化成恒真断言",
			index.DBFileName, tree)
	}
}

// rsvAssertTreeUnchanged 逐项比对两份 `.index/` 快照：条目集合、类型、内容、
// symlink 目标、dev+ino 五项**全等**才算没变。
//
// dev+ino 这一项是给 `run.lock` 准备的：一把被「删掉重建」的锁在内容上可能一字不差，
// 但它已经是**另一个 inode** —— 两个进程各自锁住一个不同 inode 时，双方都以为自己拿到了锁。
func rsvAssertTreeUnchanged(t *testing.T, before, after map[string]rsvNode, what string) {
	t.Helper()
	var diffs []string
	for rel, a := range after {
		b, ok := before[rel]
		if !ok {
			diffs = append(diffs, "新增 "+rel)
			continue
		}
		switch {
		case a.kind != b.kind:
			diffs = append(diffs, "类型变了 "+rel+"（"+b.kind+" → "+a.kind+"）")
		case a.hash != b.hash:
			diffs = append(diffs, "字节变了 "+rel)
		case a.link != b.link:
			diffs = append(diffs, "链接目标变了 "+rel)
		case !os.SameFile(b.info, a.info):
			diffs = append(diffs, "inode 被换掉 "+rel)
		}
	}
	for rel := range before {
		if _, ok := after[rel]; !ok {
			diffs = append(diffs, "删除 "+rel)
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Fatalf("%s：%s/ 必须逐项不变（类型 / 字节 / 链接目标 / inode），实际变化 %s",
			what, index.DirName, strings.Join(diffs, "；"))
	}
}

// —— ③ 只读命令的运行与取证 ——

// rsvReadCmd 是一条**只读**命令：名字用于报错定位，args 是 `eg` 之后的完整参数。
type rsvReadCmd struct {
	name string
	args []string
}

// rsvReadCmds 返回本文件覆盖的读路径命令。
//
// 选 `card show` 与 `rel` 而不是 `search`：两者的 data 只由**这一张卡**的权威内容决定，
// 不含扫描计数之外的模糊面，因此「降级结果与全量扫描逐字等价」可以按整份 data 比对，
// 而不是只比几个字段。
func rsvReadCmds() []rsvReadCmd {
	return []rsvReadCmd{
		{"card show", []string{"card", "show", applyCardID}},
		{"rel", []string{"rel", applyCardID}},
	}
}

// rsvRunRead 跑一次只读命令（恒带 --json），返回退出码与信封。
func rsvRunRead(t *testing.T, dir string, cmd rsvReadCmd) (int, Envelope) {
	t.Helper()
	r := newTestRoot(t, dir)
	r.In = closedStdin{}
	args := append([]string{"--vault", dir, "--json"}, cmd.args...)
	code, out, errOut := runCLI(t, r, args...)
	if strings.TrimSpace(out) == "" {
		t.Fatalf("eg %s：--json 输出为空（退出码 %d）：%s",
			strings.Join(cmd.args, " "), code, errOut)
	}
	return code, idxEnvelope(t, out)
}

// rsvDataJSON 把信封的 data 归一成可逐字比较的 JSON（map 序列化按键名字典序，稳定）。
func rsvDataJSON(t *testing.T, env Envelope) string {
	t.Helper()
	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatalf("data 不可序列化：%v", err)
	}
	return string(raw)
}

// rsvCodeCount 数信封 warnings[] 里某个诊断码出现了几次。
func rsvCodeCount(env Envelope, code string) int {
	n := 0
	for _, w := range env.Warnings {
		if w.Code == code {
			n++
		}
	}
	return n
}

// rsvWarnCodes 收集信封 warnings[] 的全部诊断码（升序，便于逐字断言）。
func rsvWarnCodes(env Envelope) []string {
	var out []string
	for _, w := range env.Warnings {
		if w.Code != "" {
			out = append(out, w.Code)
		}
	}
	sort.Strings(out)
	return out
}

// rsvAssertNoE15 断言信封里没有写前校验的 fail closed 诊断（E15）。
//
// 这是本文件与写入侧的**分界线**：同一处磁盘现象，写入侧必须 E15 + 阻断，
// 只读侧则一条 E15 都不许有 —— 只读命令没有「写前」可言，报 E15 等于误导用户
// 「这次操作被拒了」。
func rsvAssertNoE15(t *testing.T, env Envelope, what string) {
	t.Helper()
	for _, w := range env.Warnings {
		if w.Code == txn.CodePrecheckFailed {
			t.Fatalf("%s：只读侧不得出现 %s（那是写前校验的裁决）：%+v",
				what, txn.CodePrecheckFailed, w)
		}
	}
	if diags := errorDiagsOf(t, env); diagsHaveCode(diags, txn.CodePrecheckFailed) {
		t.Fatalf("%s：data.errors 里出现了 %s，只读命令不得 fail closed：%+v",
			what, txn.CodePrecheckFailed, diags)
	}
}

// —— ④ 用例一：index status 在四类违规下恒退 0、恰一条 W24、零改动 ——

// TestIndexStatusReservedTypeViolationReportsW24WithoutExit5 逐条语料对撞
// 「只读体检只报不改」这条纪律。
//
// 每条语料四格判据，缺一不可：
//
//	① 退出码恒 0        —— 索引坏了是**诊断**不是失败（合同 §6.1）；
//	                       退出码 5 是写前校验的裁决（T-…-074），只读侧永不启用；
//	② warning 恰一条 W24 —— 不多（不得同时报 W22 / W23，那会把「坏了」和「旧了」混成一团）、
//	                       不少（不得静默）；
//	③ 无 E15            —— 见 rsvAssertNoE15；
//	④ `.index/` 全树零变化 —— 类型 / 字节 / 链接目标 / inode 逐项比对：status 既不修
//	                       违规条目、也不顺手取锁（取锁会改写 run.lock 正文并换 inode）、
//	                       更不跑崩溃恢复（恢复会在 `txn/` 里写 abort 标记）。
func TestIndexStatusReservedTypeViolationReportsW24WithoutExit5(t *testing.T) {
	for _, c := range rsvCases() {
		t.Run(c.name, func(t *testing.T) {
			dir := idxVault(t)
			// 先建一个**健康**的索引：这样「W24」只可能来自保留条目违规，
			// 而不是「反正库也不在」这种同码不同因的巧合。
			if code, _, errOut := runIndexCLI(t, dir, IndexSubBuild); code != ExitOK {
				t.Fatalf("前置 build 退出码 = %d，期望 0：%s", code, errOut)
			}
			rsvApply(t, dir, c)

			authority := idxAuthoritySnapshot(t, dir)
			before := rsvTypedTree(t, dir)
			rsvAssertTreeHasIndex(t, before)

			code, out, errOut := runIndexCLI(t, dir, IndexSubStatus)
			if code != ExitOK {
				t.Fatalf("status 退出码 = %d，期望 0（保留条目违规是诊断不是失败，"+
					"退出码 5 属写前校验）：%s", code, errOut)
			}
			if got := idxWarnCodes(t, out); strings.Join(got, ",") != index.CodeIndexCorrupt {
				t.Fatalf("status 的 warning = %v，期望恰一条 %s", got, index.CodeIndexCorrupt)
			}
			if h := idxHealth(t, out); h != string(index.HealthCorrupt) {
				t.Fatalf("health = %q，期望 %q", h, index.HealthCorrupt)
			}
			if r := idxString(t, out, "reason"); r != index.ReasonUnexpectedFile {
				t.Fatalf("reason = %q，期望 %q", r, index.ReasonUnexpectedFile)
			}
			rsvAssertNoE15(t, idxEnvelope(t, out), "eg index status")

			rsvAssertTreeUnchanged(t, before, rsvTypedTree(t, dir),
				"eg index status 撞上保留条目类型违规")
			idxAssertAuthorityUnchanged(t, dir, authority)
		})
	}
}

// —— ⑤ 用例二：读路径在四类违规下降级为全量扫描 ——

// TestReadPathDegradesOnReservedTypeViolation 逐条语料对撞
// 「索引不可用 ⇒ 降级为全量 Markdown 扫描，而不是报错退出」。
//
// 判据取自三次运行的**对照**，而不是硬编码期望值：
//
//	基线①  索引根本不存在   → 结果就是**全量扫描**的唯一答案（W23 + Q5）
//	基线②  索引健康        → 退出码与 data 必须与基线① 逐字相同（索引只提速不改答案）
//	实测   保留条目类型违规 → 退出码与 data 仍必须与基线① 逐字相同，
//	                        且留痕恰一条 W24 + 恰一条 Q5、无 E15、`.index/` 零变化
//
// 「与基线① 逐字相同」这一格才是「等价全量扫描」的真判据：只断言「退 0」会让一个
// 返回空结果的降级实现照样通过。
func TestReadPathDegradesOnReservedTypeViolation(t *testing.T) {
	for _, c := range rsvCases() {
		t.Run(c.name, func(t *testing.T) {
			dir := idxVault(t)

			// —— 基线①：无索引 ⇒ 全量扫描的唯一答案 ——
			scanCode := map[string]int{}
			scanData := map[string]string{}
			for _, cmd := range rsvReadCmds() {
				code, env := rsvRunRead(t, dir, cmd)
				if code != ExitOK {
					t.Fatalf("eg %s：全量扫描基线退出码 = %d，期望 0（基线本身必须是"+
						"一次成功的读，否则下面的等价比较毫无意义）", cmd.name, code)
				}
				scanCode[cmd.name], scanData[cmd.name] = code, rsvDataJSON(t, env)
			}

			// —— 基线②：索引健康 ⇒ 退出码与 data 必须与全量扫描逐字相同 ——
			if code, _, errOut := runIndexCLI(t, dir, IndexSubBuild); code != ExitOK {
				t.Fatalf("前置 build 退出码 = %d，期望 0：%s", code, errOut)
			}
			for _, cmd := range rsvReadCmds() {
				code, env := rsvRunRead(t, dir, cmd)
				if code != scanCode[cmd.name] {
					t.Fatalf("eg %s：健康态退出码 = %d，全量扫描 = %d（索引只提速，不改结论）",
						cmd.name, code, scanCode[cmd.name])
				}
				if got := rsvDataJSON(t, env); got != scanData[cmd.name] {
					t.Fatalf("eg %s：健康态 data 与全量扫描不等价，本用例的基线不成立\n索引：%s\n扫描：%s",
						cmd.name, got, scanData[cmd.name])
				}
			}

			// —— 实测：违规现场 ——
			rsvApply(t, dir, c)
			authority := idxAuthoritySnapshot(t, dir)
			before := rsvTypedTree(t, dir)
			rsvAssertTreeHasIndex(t, before)

			for _, cmd := range rsvReadCmds() {
				code, env := rsvRunRead(t, dir, cmd)
				what := "eg " + cmd.name + " 撞上保留条目类型违规"
				if code != scanCode[cmd.name] {
					t.Fatalf("%s：退出码 = %d，期望与健康态 / 全量扫描一致的 %d"+
						"（索引不可用必须降级，绝不阻断读）", what, code, scanCode[cmd.name])
				}
				if got := rsvDataJSON(t, env); got != scanData[cmd.name] {
					t.Fatalf("%s：结果与全量扫描不等价 —— 降级的定义就是「答案一字不差，只是慢一点」\n"+
						"降级：%s\n扫描：%s", what, got, scanData[cmd.name])
				}
				if n := rsvCodeCount(env, index.CodeIndexCorrupt); n != 1 {
					t.Fatalf("%s：%s 出现 %d 次，期望恰 1 次（原因码不得缺席、也不得重复）：%v",
						what, index.CodeIndexCorrupt, n, rsvWarnCodes(env))
				}
				if n := rsvCodeCount(env, query.CodeQ5); n != 1 {
					t.Fatalf("%s：%s 出现 %d 次，期望恰 1 次（有降级必有一条降级留痕）：%v",
						what, query.CodeQ5, n, rsvWarnCodes(env))
				}
				// 降级原因码在 W22 / W23 / W24 里**恰取一条**：同时报「旧了」或「缺了」
				// 会让用户无从判断到底发生了什么。
				for _, other := range []string{index.CodeIndexStale, index.CodeIndexMissing} {
					if n := rsvCodeCount(env, other); n != 0 {
						t.Fatalf("%s：不得出现 %s（本次的降级原因恰是 %s）：%v",
							what, other, index.CodeIndexCorrupt, rsvWarnCodes(env))
					}
				}
				rsvAssertNoE15(t, env, what)
				rsvAssertTreeUnchanged(t, before, rsvTypedTree(t, dir), what)
			}
			idxAssertAuthorityUnchanged(t, dir, authority)
		})
	}
}
