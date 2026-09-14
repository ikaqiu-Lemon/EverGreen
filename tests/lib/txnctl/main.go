// test/txnctl/main.go：M6 端到端脚本（m6_run_lock.sh / m6_txn_journal.sh）驱动 internal/txn
// 公开 API 的**测试专用**命令行 helper（对标 test/perf/corpus_gen.go 的「go run 驱动」模式）。
//
// # 为什么需要它
//
// T-…-071 只交付 internal/txn 的锁 / txn_id / 事务日志，**尚未**把它们接进 `eg` CLI
// （接线属 T-…-072/074）。因此 e2e 无法经由 `eg` 子命令触达这些语义，只能由本 helper 以
// 进程为单位调用 internal/txn 的导出函数，让 bash 脚本能反证「跨进程互斥、E16 超时、
// 陈旧锁接管、发布屏障、Scan 三分类、marker 互斥 / 幂等」等运行时不变量。
//
// # 四条硬边界
//
//   - **不是产品代码**：位于 test/ 下，`eg` 二进制不 import 它；它只服务于 e2e。
//   - **只碰指定 vault**：所有写入都发生在 --vault 指向的目录内（其 .index/ 运行时子树），
//     不发 Git commit、不碰 domains/ sources/ 权威文件。
//   - **零解释、机器可读、不钉退出码**：命令以稳定单行 `key=value` / `STATE <id> <state>`
//     输出；语义诊断（E15/E16 等）打到 stdout 的 `code=` 行后**正常返回**（进程仍退 0），
//     由脚本用 grep 精确断言输出而非依赖具体退出码；仅「脚本用法错误」才 panic 崩出非零。
//   - **不调用 os.Exit（含间接）**：全仓「只有 cmd/eg/main.go 可调用 os.Exit」的门禁对 test/ 同样
//     生效，故本 helper 一律经正常返回 / panic 结束。flag 解析统一走 flag.ContinueOnError +
//     mustParse（显式检查 Parse 错误后 panic），**绝不用 flag.ExitOnError** —— 后者按 Go 标准库
//     文档会在解析失败时以退出码 2 结束进程（间接 os.Exit），与本边界矛盾。非法参数因此表现为
//     「非零退出但经 panic」，绝不（直接或间接）触达 os.Exit。
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

func main() {
	if len(os.Args) < 2 {
		panic("txnctl: 缺子命令")
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "lock-hold":
		lockHold(args)
	case "lock-try":
		lockTry(args)
	case "lock-inspect":
		lockInspect(args)
	case "alloc-id":
		allocID(args)
	case "write-intent":
		writeIntent(args)
	case "mark":
		mark(args)
	case "scan":
		scan(args)
	case "prune":
		prune(args)
	case "atomic-commit":
		atomicCommit(args)
	case "commit":
		commitExisting(args)
	case "crash-commit":
		crashCommit(args)
	case "recover":
		recoverCmd(args)
	default:
		panic("txnctl: 未知子命令：" + cmd)
	}
}

// mustParse 用 flag.ContinueOnError 解析并**显式检查错误后 panic**，取代 flag.ExitOnError。
//
// flag.ExitOnError 在解析失败时会以退出码 2 结束进程（Go 标准库文档明载），那是**间接**的 os.Exit
// 依赖，违反本 helper「一律经正常返回 / panic 结束、绝不触达 os.Exit」的硬边界（且现有
// TestOnlyMainCallsOsExit 只扫直接字面量，抓不到这条间接路径）。改用 ContinueOnError 后 Parse
// 只返回错误、绝不退出进程；这里把错误转成 panic，使非法参数表现为「非零退出但经 panic」。
func mustParse(fs *flag.FlagSet, args []string) {
	if err := fs.Parse(args); err != nil {
		panic("txnctl: 参数解析失败（" + fs.Name() + "）：" + err.Error())
	}
}

// emitCodedErr 把类型化诊断码打到 stdout 的 code= 行；**不结束进程**，调用方随后 return。
// 脚本据 `code=E15` / `code=E16` 断言语义，不依赖退出码（合同不钉具体退出码）。
func emitCodedErr(err error) {
	code := "UNKNOWN"
	var c interface{ Code() string }
	if errors.As(err, &c) {
		code = c.Code()
	}
	fmt.Printf("code=%s\n", code)
	fmt.Printf("error=%s\n", err.Error())
}

func stateName(s txn.TxnState) string {
	switch s {
	case txn.StateOpen:
		return "open"
	case txn.StateCorrupt:
		return "corrupt"
	case txn.StateResidue:
		return "residue"
	case txn.StateCommitted:
		return "committed"
	case txn.StateAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

func lockHold(args []string) {
	fs := flag.NewFlagSet("lock-hold", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	holdMS := fs.Int("hold-ms", 500, "持锁毫秒数")
	ready := fs.String("ready", "", "取到锁后立刻写的就绪文件（供脚本等待）")
	argv := fs.String("argv", "txnctl lock-hold", "写进锁正文的 argv")
	recordTxn := fs.String("record-txn", "", "取锁后补写的 txn_id（可选）")
	beforeLockMS := fs.Int("before-lock-ms", 0, "Acquire **之前**的锁外停留毫秒数（模拟 S0 长时候选提示）")
	scanReady := fs.String("scan-ready", "", "进入锁外停留（Acquire 前）时立刻写的就绪文件（供脚本反证「锁外期间他人可取锁」）")
	mustParse(fs, args)
	if *vault == "" {
		panic("txnctl lock-hold: --vault 必填")
	}
	// 锁外阶段（S0 提示性候选发现 / 长时扫描）**发生在 Acquire 之前**：此刻本进程尚未持锁，
	// 另一进程应能立即取到并释放锁。scan-ready 让脚本精确捕获这个「已进入锁外、尚未取锁」的窗口。
	if *scanReady != "" {
		if werr := os.WriteFile(*scanReady, []byte("scanning\n"), 0o644); werr != nil {
			panic("txnctl lock-hold: 写 scan-ready 文件失败：" + werr.Error())
		}
	}
	if *beforeLockMS > 0 {
		time.Sleep(time.Duration(*beforeLockMS) * time.Millisecond)
	}
	l, err := txn.Acquire(*vault, txn.LockOptions{Argv: []string{*argv}})
	if err != nil {
		emitCodedErr(err)
		return
	}
	fmt.Printf("acquired=1 pid=%d path=%s\n", os.Getpid(), l.Path())
	if *recordTxn != "" {
		if rerr := l.RecordTxnID(*recordTxn); rerr != nil {
			fmt.Printf("record_txn_err=%s\n", rerr.Error())
		}
	}
	if *ready != "" {
		if werr := os.WriteFile(*ready, []byte("ready\n"), 0o644); werr != nil {
			panic("txnctl lock-hold: 写就绪文件失败：" + werr.Error())
		}
	}
	time.Sleep(time.Duration(*holdMS) * time.Millisecond)
	if rerr := l.Release(); rerr != nil {
		panic("txnctl lock-hold: 释放锁失败：" + rerr.Error())
	}
	fmt.Println("released=1")
}

func lockTry(args []string) {
	fs := flag.NewFlagSet("lock-try", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	timeoutMS := fs.Int("timeout-ms", 200, "取锁等待上限毫秒")
	argv := fs.String("argv", "txnctl lock-try", "写进锁正文的 argv")
	mustParse(fs, args)
	if *vault == "" {
		panic("txnctl lock-try: --vault 必填")
	}
	l, err := txn.Acquire(*vault, txn.LockOptions{
		Timeout: time.Duration(*timeoutMS) * time.Millisecond,
		Argv:    []string{*argv},
	})
	if err != nil {
		emitCodedErr(err)
		return
	}
	fmt.Printf("acquired=1 pid=%d path=%s\n", os.Getpid(), l.Path())
	if rerr := l.Release(); rerr != nil {
		panic("txnctl lock-try: 释放锁失败：" + rerr.Error())
	}
	fmt.Println("released=1")
}

func lockInspect(args []string) {
	fs := flag.NewFlagSet("lock-inspect", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	mustParse(fs, args)
	if *vault == "" {
		panic("txnctl lock-inspect: --vault 必填")
	}
	path := txn.LockPath(*vault)
	fi, err := os.Lstat(path)
	if err != nil {
		fmt.Printf("exists=0 path=%s\n", path)
		return
	}
	fmt.Printf("exists=1 path=%s size=%d mode=%s\n", path, fi.Size(), fi.Mode())
	if h, ok := txn.ReadHolder(*vault); ok {
		fmt.Printf("holder_ok=1 holder_pid=%d holder_txn=%s\n", h.PID, h.TxnID)
	} else {
		fmt.Println("holder_ok=0")
	}
}

func allocID(args []string) {
	fs := flag.NewFlagSet("alloc-id", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	mustParse(fs, args)
	if *vault == "" {
		panic("txnctl alloc-id: --vault 必填")
	}
	id, err := txn.AllocateTxnID(*vault)
	if err != nil {
		emitCodedErr(err)
		return
	}
	fmt.Printf("txn_id=%s\n", id)
}

func writeIntent(args []string) {
	fs := flag.NewFlagSet("write-intent", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	id := fs.String("txn", "", "txn_id")
	create := fs.Bool("create", true, "create 语义（true=新建；false=替换需带 pre）")
	path := fs.String("path", "o.md", "目标文件相对路径")
	mustParse(fs, args)
	if *vault == "" || *id == "" {
		panic("txnctl write-intent: --vault / --txn 必填")
	}
	spec := txn.FileSpec{Path: *path, Create: *create, TargetBytes: []byte("new-content"), TargetOp: "create"}
	if !*create {
		spec.TargetOp = "replace"
		spec.PreBytes = []byte("old-content")
	}
	in := txn.IntentInput{
		Argv:  []string{"txnctl", "write-intent", *path},
		Files: []txn.FileSpec{spec},
	}
	if _, err := txn.WriteIntent(*vault, *id, in); err != nil {
		emitCodedErr(err)
		return
	}
	fmt.Printf("intent_written=1 txn_id=%s\n", *id)
}

func mark(args []string) {
	fs := flag.NewFlagSet("mark", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	id := fs.String("txn", "", "txn_id")
	kind := fs.String("kind", "", "commit|abort")
	mustParse(fs, args)
	if *vault == "" || *id == "" {
		panic("txnctl mark: --vault / --txn 必填")
	}
	var err error
	switch *kind {
	case "commit":
		err = txn.MarkCommit(*vault, *id)
	case "abort":
		err = txn.MarkAbort(*vault, *id)
	default:
		panic("txnctl mark: --kind 必须是 commit|abort")
	}
	if err != nil {
		emitCodedErr(err)
		return
	}
	fmt.Printf("marked=%s txn_id=%s\n", *kind, *id)
}

func scan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	mustParse(fs, args)
	if *vault == "" {
		panic("txnctl scan: --vault 必填")
	}
	res, err := txn.Scan(*vault)
	if err != nil {
		emitCodedErr(err)
		return
	}
	for _, e := range res.Entries {
		fmt.Printf("STATE %s %s\n", e.TxnID, stateName(e.State))
	}
	blocked := "0"
	if res.Blocked() != nil {
		blocked = "1"
	}
	fmt.Printf("entries=%d open=%d corrupt=%d residue=%d blocked=%s\n",
		len(res.Entries), len(res.Open()), len(res.Corrupt()), len(res.Residue()), blocked)
}

func prune(args []string) {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	vault := fs.String("vault", "", "vault 根目录")
	maxKeep := fs.Int("max-keep", 0, "保留最近 N 个闭合事务（≤0 取默认 20）")
	maxAgeH := fs.Int("max-age-h", 0, "保留时长小时（≤0 取默认 7 天）")
	mustParse(fs, args)
	if *vault == "" {
		panic("txnctl prune: --vault 必填")
	}
	opts := txn.PruneOptions{MaxKeep: *maxKeep}
	if *maxAgeH > 0 {
		opts.MaxAge = time.Duration(*maxAgeH) * time.Hour
	}
	removed, err := txn.Prune(*vault, opts)
	if err != nil {
		emitCodedErr(err)
		return
	}
	for _, id := range removed {
		fmt.Printf("REMOVED %s\n", id)
	}
	fmt.Printf("removed=%d\n", len(removed))
}
