// test/txnctl/commit.go：M6 · T-evergreen.s1_main_flow-158614-072 的 e2e 驱动扩展 ——
// 为 m6_atomic_multifile.sh / m6_crash_recovery.sh 提供「多文件原子提交 / 真实崩溃留态 /
// 崩溃恢复」的**测试专用**子命令。
//
// # 为什么放在 txnctl 而不是产品代码里
//
// 与 lock / journal 的 e2e 同源（见 main.go 头注四条硬边界）：这些子命令只以进程为单位调用
// internal/txn 的**导出** API（AllocateTxnID / WriteIntent / Commit / MarkCommit / Recover），
// 让 bash 脚本能反证运行时不变量。它们**不是产品代码**（eg 二进制不 import test/），也**不**
// 触碰 internal/txn 的包内测试钩子（commitFailpoint 恒 nil，仅包内测试可注入）。
//
// # 真实崩溃点如何复现（crash-commit）
//
// 生产 eg 二进制刻意没有崩溃注入开关，因此 e2e 的「8 个崩溃点」不能靠向 eg 注入实现。
// 本 helper 用**真实进程 + 真实 kill -9** 复现：crash-commit 用导出 API 把事务推进到指定
// 崩溃点（真实写 intent + 真实备料 pre/ + 真实原子 rename 权威文件 / 真实写 commit 标记），
// 到点后 touch 就绪文件并长眠等待被 kill -9。被杀后磁盘留态与「真实崩溃在该点」逐字一致
// （intent.json、pre/ 前像、部分 / 全部权威文件目标态、commit 标记的在盘与否都是真实的）。
// 随后由 `txnctl recover`（或真实 `eg` 写命令的 S2 屏障）跑同一个 txn.Recover 内核收敛。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// fileSpecArg 是一项待提交文件的 e2e 规格：vault 相对路径 + create 语义 + 前像 / 目标字节。
type fileSpecArg struct {
	rel    string
	create bool
	pre    []byte
	target []byte
}

// multiFile 支持 `--file rel|create|pre|target` 可重复传入（flag.Value）。
type multiFile []fileSpecArg

func (m *multiFile) String() string { return "" }

func (m *multiFile) Set(v string) error {
	parts := strings.SplitN(v, "|", 4)
	if len(parts) != 4 {
		return fmt.Errorf("--file 需 4 段 rel|create|pre|target，得 %q", v)
	}
	*m = append(*m, fileSpecArg{
		rel:    parts[0],
		create: parts[1] == "true",
		pre:    []byte(parts[2]),
		target: []byte(parts[3]),
	})
	return nil
}

// intentFilesOf 把 e2e 规格映射成 WriteIntent 的 files[]（一一对应）。
func intentFilesOf(specs multiFile) []txn.FileSpec {
	out := make([]txn.FileSpec, 0, len(specs))
	for _, s := range specs {
		op := "replace"
		if s.create {
			op = "create"
		}
		fs := txn.FileSpec{Path: s.rel, Create: s.create, TargetBytes: s.target, TargetOp: op}
		if !s.create {
			fs.PreBytes = s.pre
		}
		out = append(out, fs)
	}
	return out
}

// commitFilesOf 把 e2e 规格映射成 Commit 的写集合。
func commitFilesOf(specs multiFile) []txn.CommitFile {
	out := make([]txn.CommitFile, 0, len(specs))
	for _, s := range specs {
		out = append(out, txn.CommitFile{Path: s.rel, TargetBytes: s.target})
	}
	return out
}

// seedPreimages 让 create:false 目标先落到**前像**态（模拟事务开始前的权威内容），
// create:true 目标则确保当前不存在。
func seedPreimages(vault string, specs multiFile) {
	for _, s := range specs {
		abs := filepath.Join(vault, s.rel)
		if s.create {
			_ = os.Remove(abs)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(abs, s.pre, 0o644); err != nil {
			panic(err)
		}
	}
}

// reportCommit 把 Commit 回执压成脚本可 grep 的单行标记。
func reportCommit(res *txn.CommitResult, cerr error) {
	if cerr != nil {
		if res != nil && res.RolledBack {
			fmt.Printf("rolled_back=1\n")
		}
		emitCodedErr(cerr)
		return
	}
	fmt.Printf("committed=%v files_written=%d\n", res.Committed, res.FilesWritten)
}

// atomicCommit：alloc-id → WriteIntent（多文件）→ Commit，一次跑完。用于原子提交正证。
//
// --pause-ms>0 时切换到**分阶段手工提交**：WriteIntent → 逐文件原子 rename（每次 rename 之间
// sleep pause-ms）→ MarkCommit。每个权威文件仍是 tmp + rename 的**单次原子替换**（永不出现半写
// 字节），但 rename 之间被人为拉开窗口，供并发读者反证「任一文件内容恒 ∈ {前像, 目标态}、绝不撕裂」
// 以及合同 §5.1 L2/L3 的「跨文件中间态可被非协作观察者看到」。--ready 在**首个** rename 之前 touch，
// 让脚本精准起跑并发读循环。pause-ms==0 时走 txn.Commit 的一次性原子提交（默认，用于快速正证 / 幂等）。
func atomicCommit(args []string) {
	fs := flag.NewFlagSet("atomic-commit", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	seed := fs.Bool("seed-pre", true, "为 create:false 目标先落 pre 内容")
	pauseMS := fs.Int("pause-ms", 0, ">0 时分阶段 rename 并在每次 rename 之间停留该毫秒数")
	ready := fs.String("ready", "", "首个权威 rename 之前 touch 的就绪文件（供并发读者起跑）")
	var files multiFile
	fs.Var(&files, "file", "rel|create|pre|target（可重复）")
	mustParse(fs, args)
	if *vault == "" || len(files) == 0 {
		panic("txnctl atomic-commit: --vault 与至少一个 --file 必填")
	}
	if *seed {
		seedPreimages(*vault, files)
	}
	id, err := txn.AllocateTxnID(*vault)
	if err != nil {
		emitCodedErr(err)
		return
	}
	fmt.Printf("txn_id=%s\n", id)
	if _, werr := txn.WriteIntent(*vault, id, txn.IntentInput{
		Argv: []string{"txnctl", "atomic-commit"}, ExpectCommit: true,
		Files: intentFilesOf(files),
	}); werr != nil {
		emitCodedErr(werr)
		return
	}
	if *pauseMS > 0 {
		if *ready != "" {
			if werr := os.WriteFile(*ready, []byte("go\n"), 0o644); werr != nil {
				panic(werr)
			}
		}
		for i, s := range files {
			if i > 0 {
				time.Sleep(time.Duration(*pauseMS) * time.Millisecond)
			}
			renameToTarget(*vault, s)
		}
		if merr := txn.MarkCommit(*vault, id); merr != nil {
			emitCodedErr(merr)
			return
		}
		fmt.Printf("committed=true files_written=%d\n", len(files))
		return
	}
	res, cerr := txn.Commit(*vault, id, txn.CommitInput{Files: commitFilesOf(files)})
	reportCommit(res, cerr)
}

// commitExisting：对**已存在**的 open 事务再跑一次 Commit（幂等重入反证）。
func commitExisting(args []string) {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	id := fs.String("txn", "", "txn_id")
	var files multiFile
	fs.Var(&files, "file", "rel|create|pre|target（可重复）")
	mustParse(fs, args)
	if *vault == "" || *id == "" {
		panic("txnctl commit: --vault / --txn 必填")
	}
	res, cerr := txn.Commit(*vault, *id, txn.CommitInput{Files: commitFilesOf(files)})
	reportCommit(res, cerr)
}

// renameToTarget 把单个权威文件原子写到目标态（tmp + rename），复现 Commit 第二阶段的一次 rename。
func renameToTarget(vault string, s fileSpecArg) {
	abs := filepath.Join(vault, s.rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		panic(err)
	}
	tmp := abs + ".crashtmp"
	if err := os.WriteFile(tmp, s.target, 0o644); err != nil {
		panic(err)
	}
	if err := os.Rename(tmp, abs); err != nil {
		panic(err)
	}
}

// pauseForKill：到达崩溃点后 touch 就绪文件并长眠等待被 kill -9（真实崩溃留态）。
func pauseForKill(ready string) {
	if ready != "" {
		if err := os.WriteFile(ready, []byte("ready\n"), 0o644); err != nil {
			panic(err)
		}
	}
	time.Sleep(3600 * time.Second)
}

// crashCommit：把事务真实推进到 --crash-at 指定的崩溃点后停下等待被 kill -9。
//
// 崩溃点（对应合同 §6 矩阵 P1~P8 的磁盘可观察投影）：
//
//	pre_intent   —— 只 AllocateTxnID（事务目录在盘），不写 intent ⇒ residue（P1）。
//	after_intent —— intent 已发布、任何权威 rename 之前（P2）。
//	partial      —— 已 rename 首个权威文件、其余未 rename（P3）。
//	all_renamed  —— 全部权威文件已 rename、commit 标记之前（P4）。
//	committed    —— 全部 rename + commit 标记已在盘（P5~P8 的共同磁盘态）。
func crashCommit(args []string) {
	fs := flag.NewFlagSet("crash-commit", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	crashAt := fs.String("crash-at", "", "pre_intent|after_intent|partial|all_renamed|committed")
	ready := fs.String("ready", "", "到达崩溃点后 touch 的就绪文件")
	var files multiFile
	fs.Var(&files, "file", "rel|create|pre|target（可重复）")
	mustParse(fs, args)
	if *vault == "" || *crashAt == "" || len(files) == 0 {
		panic("txnctl crash-commit: --vault / --crash-at / --file 必填")
	}
	seedPreimages(*vault, files)

	id, err := txn.AllocateTxnID(*vault)
	if err != nil {
		panic(err)
	}
	fmt.Printf("txn_id=%s\n", id)

	if *crashAt == "pre_intent" {
		pauseForKill(*ready)
		return
	}

	if _, werr := txn.WriteIntent(*vault, id, txn.IntentInput{
		Argv: []string{"txnctl", "crash-commit"}, ExpectCommit: true,
		Files: intentFilesOf(files),
	}); werr != nil {
		panic(werr)
	}
	if *crashAt == "after_intent" {
		pauseForKill(*ready)
		return
	}

	limit := len(files)
	if *crashAt == "partial" {
		limit = 1
	}
	for i := 0; i < limit; i++ {
		renameToTarget(*vault, files[i])
	}
	if *crashAt == "partial" || *crashAt == "all_renamed" {
		pauseForKill(*ready)
		return
	}
	if *crashAt == "committed" {
		if merr := txn.MarkCommit(*vault, id); merr != nil {
			panic(merr)
		}
		pauseForKill(*ready)
		return
	}
	panic("txnctl crash-commit: 未知 --crash-at " + *crashAt)
}

// recoverCmd：跑 txn.Recover（写命令启动屏障 S2 的同一个内核），把回执压成脚本可 grep 的单行。
func recoverCmd(args []string) {
	fs := flag.NewFlagSet("recover", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	mustParse(fs, args)
	if *vault == "" {
		panic("txnctl recover: --vault 必填")
	}
	res, err := txn.Recover(*vault)
	if err != nil {
		emitCodedErr(err)
		return
	}
	outcome := "noop"
	if res.Outcome == txn.RecoverRolledBack {
		outcome = "rolledback"
	}
	w26 := 0
	for _, d := range res.Diagnostics {
		if d.Code == txn.CodeTxnRecovered {
			w26++
		}
	}
	fmt.Printf("outcome=%s txn_id=%s w26=%d residue_cleaned=%d\n",
		outcome, res.TxnID, w26, len(res.ResidueCleaned))
}
