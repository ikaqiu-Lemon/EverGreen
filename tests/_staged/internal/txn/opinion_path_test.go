package txn

// [S5] opinion_path_test.go —— T-004-B：事务层对 `domains/<d>/opinions/o-*.md` 的
// staging → commit → rollback → 崩溃恢复全链路合同（Schema v2 观点落位）。
//
// # 这批用例要证的第一件事：事务层**按路径**工作，不枚举实体种类
//
// T-004 的 DoR 把「Intent 是按路径索引、不带 entity 种类字段」列成**待证事实**而不是
// 可以直接假设的前提。因此本文件刻意把观点路径与知识路径**混在同一笔事务里**：
// 若哪天有人在事务层按目录名分流（例如只给 `knowledge/` 备料、只对卡算前像），
// 混合写集合会立刻在「全成或全不成」这一格上变红。
//
// # 为什么观点路径需要自己的一组判据（不是把 cards 用例复制一遍）
//
// 观点目录是 v2 新增的：`eg init` 的骨架里**没有** `domains/<d>/opinions/`，
// 因此「一个领域里的第一条观点」必然触发**权威目录的新建**。这条路径与既有
// cards / notes 用例（目录早已由骨架建好）走的不是同一段代码：新建目录的**目录项**
// 需要在其父目录里被 fsync 才算落盘，否则崩溃后可能出现「commit 标记在盘、
// 但整个 opinions/ 目录连同已提交的观点一起消失」——而恢复层对已提交事务
// 恰好**不回滚**（§6 P5~P8），于是这份丢失既无人回滚也无人报错。
// TestOpinionCommitFsyncsCreatedDirChain 就是这条持久性判据。
//
// 判据来源：M6 原子性与强校验合同 §4（安全序 / 三分支恢复）、§4.1.1（前像可用性）、
// §5.1（四级可见性 / 无撕裂）、§6（崩溃矩阵 P2/P4）；Schema v2 §3.1（观点落位目录）；
// Teamwork T-…-004 Acceptance 第 3 条（staging 之后、commit 标记之前强制中断）。

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// 观点与知识的真实落位形态（与 store.OpinionRel / store.CardRel 的拼装口径逐字一致）。
// 本包按依赖禁令不 import store，因此这里只复制**路径字面量**，不复制任何逻辑。
const (
	opinionDomainDir = "domains/ai-infra/opinions"
	opinionRelA      = opinionDomainDir + "/o-20261017-a.md"
	opinionRelB      = opinionDomainDir + "/o-20261017-b.md"
	knowledgeRelA    = "domains/ai-infra/knowledge/k-20260901-a.md"
)

// opinionBytes 造一份形态像真实观点文件的字节（含 frontmatter 的 validation 键）。
//
// 事务层是**字节无关**的：它只搬运调用方给的字节。用真实形态而不是 "A1\n" 的理由只有一个——
// 让「前像 / 目标态」两份字节在长度与结构上都不对称，避免某个错误实现靠「长度恰好相同」
// 蒙过哈希以外的判据。
func opinionBytes(id, claim, validation string) []byte {
	return []byte("---\nid: " + id + "\nstatus: active\nvalidation: " + validation +
		"\ncreated_at: '2026-10-17'\nsources: []\nrelations: []\n---\n\n" +
		"## 观点\n\n" + claim + "\n\n## 论据与推理\n\n占位。\n")
}

func knowledgeBytes(id, body string) []byte {
	return []byte("---\nid: " + id + "\nstatus: active\ncreated_at: '2026-09-01'\n" +
		"sources: []\nrelations: []\n---\n\n## 知识内容\n\n" + body + "\n")
}

// opinionMixedSet 是本文件的标准写集合：**改写一条既有观点 + 新建一条观点 + 改写一张知识卡**。
//
// 三项的次序是判据的一部分：下标 1 是新建项且**夹在两个改写项中间**，
// 于是「在下标 2 的 rename 之前失败」这一个崩溃点能同时考到
// 「已 rename 的改写项回前像」与「已 rename 的新建项进 quarantine」两支。
func opinionMixedSet() []wsFile {
	return []wsFile{
		{path: opinionRelA, pre: opinionBytes("o-20261017-a", "旧主张。", "pending"),
			target: opinionBytes("o-20261017-a", "修订后的主张，更长一些。", "pending")},
		{path: opinionRelB, create: true,
			target: opinionBytes("o-20261017-b", "新建观点的主张。", "pending")},
		{path: knowledgeRelA, pre: knowledgeBytes("k-20260901-a", "旧正文。"),
			target: knowledgeBytes("k-20260901-a", "新正文。")},
	}
}

// stageTempsIn 列出目录 dir 下属于事务 id 的备料临时文件名（升序）。
func stageTempsIn(t *testing.T, vault, dir, id string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(vault, filepath.FromSlash(dir)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("读目录 %s 失败：%v", dir, err)
	}
	prefix := ".eg-txn-" + id + "-"
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), prefix) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// —— 判据 B-1：观点路径的 staging → commit ——
//
// 备料阶段：临时文件必须落在**观点自己的目录**里（同文件系统才能原子 rename），
// 且此刻权威面一个字节都还没动（既有观点仍是前像、新建观点尚不存在）。
// 提交之后：三个路径全部为目标态、commit 标记在盘、本事务临时文件零残留。
func TestOpinionCommitStagesInPlaceThenPublishes(t *testing.T) {
	vault := newVault(t)
	files := opinionMixedSet()
	id := makeOpenTxnWithSet(t, vault, files)

	staged := false
	setCommitFailpoint(t, func(step string, index int) error {
		if step != fpCommitAfterStage {
			return nil
		}
		staged = true
		// 权威面零变化：改写项仍是前像，新建项尚不存在。
		if got := readAuth(t, vault, opinionRelA); string(got) != string(files[0].pre) {
			t.Fatalf("备料阶段既有观点应仍为前像，得 %q", got)
		}
		if authExists(t, vault, opinionRelB) {
			t.Fatalf("备料阶段新建观点 %s 不得出现在权威路径上", opinionRelB)
		}
		// 备料临时文件恰落在观点目录内（不是 vault 根、不是 .index/）。
		tmps := stageTempsIn(t, vault, opinionDomainDir, id)
		if len(tmps) != 2 {
			t.Fatalf("观点目录内应有 2 个本事务备料临时文件（1 改写 + 1 新建），得 %v", tmps)
		}
		return nil
	})

	res, err := Commit(vault, id, commitInputFor(files))
	if err != nil {
		t.Fatalf("观点路径提交失败：%v", err)
	}
	if !staged {
		t.Fatal("备料阶段判据未被执行：fpCommitAfterStage 应恰好触发一次")
	}
	if !res.Committed || res.FilesWritten != len(files) {
		t.Fatalf("期望三个路径全部提交，得 %+v", res)
	}
	for _, f := range files {
		if got := readAuth(t, vault, f.path); string(got) != string(f.target) {
			t.Fatalf("%s 应为目标态，得 %q", f.path, got)
		}
	}
	if !markerPresent(t, vault, id, CommitMarker) {
		t.Fatal("commit 标记应在盘（标记落盘之前 Markdown 一律不算生效）")
	}
	if left := txnTmpsLeft(t, vault, id); len(left) != 0 {
		t.Fatalf("提交成功后不得残留本事务备料临时文件，得 %v", left)
	}
}

// —— 判据 B-2：新建权威目录链的目录项必须被 fsync ——
//
// `eg init` 的骨架里没有 `opinions/`，因此一个领域的**第一条观点**会让提交路径
// `MkdirAll` 出一整条新目录（`domains/` → `domains/<d>/` → `domains/<d>/opinions/`）。
// 目录项住在**父目录**里：只 fsync 叶子目录只能保证「文件项落盘」，不能保证
// 「这个目录本身在它父目录里落盘」。崩溃后果是不可自愈的一格：commit 标记已在盘
// ⇒ 恢复层判已提交、按 §6 P5~P8 **不回滚**、也不报错，而目标文件连目录一起没了。
//
// 因此判据是：每个**新建目录**的父目录都被 fsync 过（连带叶子目录本身，
// 那一格由既有备料序保证）。观测面走包内 dirSyncObserver（生产恒 nil）。
func TestOpinionCommitFsyncsCreatedDirChain(t *testing.T) {
	vault := newVault(t)
	// 只写一条新建观点：整条 `domains/ai-infra/opinions/` 目录链都由本次提交创建。
	files := []wsFile{{path: opinionRelB, create: true,
		target: opinionBytes("o-20261017-b", "领域里的第一条观点。", "pending")}}
	id := makeOpenTxnWithSet(t, vault, files)

	if authExists(t, vault, "domains") {
		t.Fatal("前置条件：本用例要求 domains/ 尚不存在（考的是新建目录链的持久性）")
	}

	var synced []string
	setDirSyncObserver(t, func(dir string) { synced = append(synced, dir) })

	if _, err := Commit(vault, id, commitInputFor(files)); err != nil {
		t.Fatalf("提交失败：%v", err)
	}

	seen := map[string]bool{}
	for _, d := range synced {
		abs, err := filepath.Abs(d)
		if err != nil {
			t.Fatalf("规范化被 fsync 的目录 %q 失败：%v", d, err)
		}
		seen[filepath.Clean(abs)] = true
	}
	// 新建的三层目录 + 它们各自的父目录（vault 根是最浅一层的父目录）。
	for _, rel := range []string{".", "domains", "domains/ai-infra", opinionDomainDir} {
		want := filepath.Clean(filepath.Join(vault, filepath.FromSlash(rel)))
		if !seen[want] {
			t.Fatalf("目录 %s 未被 fsync：新建目录项只在其父目录 fsync 后才落盘，"+
				"否则崩溃后 commit 标记在盘而观点文件连目录一起丢失（恢复层对已提交事务不回滚）\n"+
				"实际被 fsync 的目录：%v", rel, synced)
		}
	}
}

// —— 判据 B-3：提交失败 ⇒ 观点路径回滚到前像、新建观点零残留 ——
//
// 崩溃点取「下标 2 的 rename 之前失败」：此刻下标 0（既有观点）与下标 1（新建观点）
// 都已 rename 生效。回滚必须把既有观点**逐字节**还原成前像、把新建观点从权威路径
// 移进 quarantine（不物理删字节），最后才写 abort。
func TestOpinionCommitFailureRollsBackOpinionPaths(t *testing.T) {
	vault := newVault(t)
	files := opinionMixedSet()
	id := makeOpenTxnWithSet(t, vault, files)

	boom := errors.New("注入：观点提交在第三个 rename 之前失败")
	setCommitFailpoint(t, func(step string, index int) error {
		if step == fpCommitBeforeRename && index == 2 {
			return boom
		}
		return nil
	})

	res, err := Commit(vault, id, commitInputFor(files))
	if !errors.Is(err, boom) {
		t.Fatalf("应回传注入的原始失败原因，得 %v", err)
	}
	if res == nil || !res.RolledBack {
		t.Fatalf("提交失败必须收敛为已回滚（全部还原 + abort），得 %+v", res)
	}
	// 既有观点逐字节回前像。
	if got := readAuth(t, vault, opinionRelA); string(got) != string(files[0].pre) {
		t.Fatalf("既有观点应逐字节回到前像：\n want %q\n got  %q", files[0].pre, got)
	}
	// 新建观点零残留，字节进 quarantine（可人工取回）。
	if authExists(t, vault, opinionRelB) {
		t.Fatalf("新建观点回滚后权威路径 %s 必须消失", opinionRelB)
	}
	q := filepath.Join(TxnDirPath(vault, id), QuarantineDir, "1")
	if got := mustReadFile(t, q); string(got) != string(files[1].target) {
		t.Fatalf("新建观点的字节应原样进 quarantine（不物理删除），得 %q", got)
	}
	if !markerPresent(t, vault, id, AbortMarker) || markerPresent(t, vault, id, CommitMarker) {
		t.Fatal("回滚完成后应只有 abort 标记（严禁先写 abort 再回滚，也不得留下 commit）")
	}
	if left := txnTmpsLeft(t, vault, id); len(left) != 0 {
		t.Fatalf("回滚后不得残留本事务备料临时文件，得 %v", left)
	}
}

// —— 判据 B-4：staging 已写、commit 标记之前中断 ⇒ 重跑恢复回到前像 ——
//
// 这是 Teamwork T-…-004 Acceptance 第 3 条的两个真实崩溃点（进程直接死亡，
// 没有任何回滚代码跑过，因此磁盘态由用例手工摆出来，与 §6 矩阵同法）：
//
//	P2  备料已完成（观点目录里留着 stage tmp）、任何 rename 之前中断；
//	P4  三个路径全部 rename 完成、commit 标记之前中断。
//
// 两者恢复后的收敛态相同：既有观点逐字节回前像、新建观点在权威路径零残留、
// abort 在盘且 commit 缺席、恰一条 W26、本事务备料临时文件零残留，
// 且事务在 Scan 里已是**已闭合**（不再占用「未闭合事务只允许 1 个」的名额）。
func TestOpinionCrashBeforeCommitMarkerRecovers(t *testing.T) {
	cases := []struct {
		name  string
		crash func(t *testing.T, vault, id string, files []wsFile)
	}{
		{
			name: "P2_staged_before_any_rename",
			crash: func(t *testing.T, vault, id string, files []wsFile) {
				for i, f := range files {
					placeStageTemp(t, vault, id, i, f.path, f.target)
				}
			},
		},
		{
			name: "P4_all_renamed_marker_absent",
			crash: func(t *testing.T, vault, id string, files []wsFile) {
				for _, f := range files {
					setAuth(t, vault, f.path, f.target)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := newVault(t)
			files := opinionMixedSet()
			id := makeOpenTxnWithSet(t, vault, files)
			tc.crash(t, vault, id, files)

			res, err := Recover(vault)
			if err != nil {
				t.Fatalf("恢复失败：%v", err)
			}
			if res.Outcome != RecoverRolledBack {
				t.Fatalf("commit 标记缺席的未闭合事务必须被回滚，得 %+v", res)
			}
			if n := countW26(res); n != 1 {
				t.Fatalf("真实回滚应恰产一条 W26，得 %d 条（%+v）", n, res.Diagnostics)
			}
			// 权威 Markdown 回到前像 / 新建项零残留 + abort 在盘、commit 缺席。
			assertMatrixRolledBack(t, vault, id, files)
			if got := readAuth(t, vault, opinionRelA); string(got) != string(files[0].pre) {
				t.Fatalf("既有观点应逐字节回到前像：\n want %q\n got  %q", files[0].pre, got)
			}
			if left := txnTmpsLeft(t, vault, id); len(left) != 0 {
				t.Fatalf("恢复后不得残留本事务备料临时文件，得 %v", left)
			}
			if tmps := stageTempsIn(t, vault, opinionDomainDir, id); len(tmps) != 0 {
				t.Fatalf("观点目录内不得残留本事务备料临时文件，得 %v", tmps)
			}
			// journal 状态符合既有协议：本事务已闭合（aborted），零未闭合、零损坏。
			scan, serr := Scan(vault)
			if serr != nil {
				t.Fatalf("恢复后 Scan 失败：%v", serr)
			}
			if len(scan.Open()) != 0 {
				t.Fatalf("恢复后不应再有未闭合事务，得 %v", scan.Open())
			}
			state := TxnState(-1)
			for _, e := range scan.Entries {
				if e.TxnID == id {
					state = e.State
				}
			}
			if state != StateAborted {
				t.Fatalf("恢复后事务 %s 应分类为 aborted，得 %s", id, stateName(state))
			}
			// 恢复真的动过字节的路径必须被如实点名（调用方据此重采写前凭据）。
			if tc.name == "P4_all_renamed_marker_absent" {
				restored := map[string]bool{}
				for _, p := range res.Restored {
					restored[p] = true
				}
				for _, rel := range []string{opinionRelA, opinionRelB} {
					if !restored[rel] {
						t.Fatalf("Restored 应点名被恢复动过的观点路径 %s，得 %v", rel, res.Restored)
					}
				}
			}
		})
	}
}

// setDirSyncObserver 装上包内目录 fsync 观测钩子并在用例结束时恢复（与 setCommitFailpoint 同法）。
//
// 为什么持久性判据只能这样证：目录 fsync 是一次**没有返回值的副作用**，从进程外看不见
// （既不改文件内容，也不改任何可 Stat 的元数据）。若不观测，「新建目录的父目录被 fsync 过」
// 这条判据就只能靠读代码相信——而它恰好是崩溃后丢数据与不丢数据的分界。
func setDirSyncObserver(t *testing.T, fn func(dir string)) {
	t.Helper()
	prev := dirSyncObserver
	dirSyncObserver = fn
	t.Cleanup(func() { dirSyncObserver = prev })
}

// countW26 数一次恢复回执里的 W26 条数（判据要「恰一条」，不是「至少一条」）。
func countW26(res *RecoverResult) int {
	n := 0
	for _, d := range res.Diagnostics {
		if d.Code == CodeTxnRecovered {
			n++
		}
	}
	return n
}
