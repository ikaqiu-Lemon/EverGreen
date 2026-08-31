package store

import (
	"errors"
	"fmt"
)

// SkipReason 是「本次未写入」的机器可读原因（进报告 skipped[]）。
type SkipReason string

// 跳过原因取值。S1 只有这几种，且都不是错写：宁可少写、不可错写。
const (
	// SkipNone 表示没有跳过（已写入）。
	SkipNone SkipReason = ""
	// SkipFileChanged 是 B3：文件自读取以来变化，或写前字节自检不等（退化为同一语义）。
	SkipFileChanged SkipReason = "file_changed"
	// SkipUserBlockUnsafe 是 B2：用户分区无法安全逐字保留。
	SkipUserBlockUnsafe SkipReason = "user_block_unsafe"
)

// CauseFor 是 `skipped[].kind` → `skipped[].cause` 的**一一对应**表
// （合同 `docs/specs/2026-09-08-changeplan-contract.md` §8，S1 全库唯一口径）。
// S1 的 kind 恰两值，没有第三种；`stale` 一类同义词任何时候不得使用。
func CauseFor(reason SkipReason) string {
	switch reason {
	case SkipFileChanged:
		return "content_hash_mismatch"
	case SkipUserBlockUnsafe:
		return "user_block_not_preserved"
	default:
		return ""
	}
}

// SkipError 是「必须跳过」的显式信号：调用方拿到它就必须把该文件计入报告 skipped[]，
// 不得当成成功、也不得重试或强写（B2 / B3）。
type SkipError struct {
	Path   string
	Reason SkipReason
	Detail string
}

func (e *SkipError) Error() string {
	return fmt.Sprintf("跳过 %s（%s）：%s", e.Path, e.Reason, e.Detail)
}

// Skip 是显式标记方法：拿到 *SkipError 即「必须跳过」。
func (e *SkipError) Skip() bool { return true }

// AsSkip 从 error 链里取出跳过信号；调用方不得忽略返回的 ok。
func AsSkip(err error) (*SkipError, bool) {
	var se *SkipError
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}

// Result 是**单文件**的本次写入回执：实际写入 / 跳过 + 原因，供上层编排与报告如实上报。
// Warnings 记录「已写入但写后只读复核有问题」这类必须披露、但不改变落盘事实的信息。
type Result struct {
	Path     string
	Written  bool
	Reason   SkipReason
	Detail   string
	Hash     string // 写入后的 content_hash（未写入时是磁盘现状的 hash）
	Warnings []string
}

// Skipped 表示本文件未写入。
func (r Result) Skipped() bool { return !r.Written }

// Receipt 是一次链路的逐文件回执集合。
//
// 注意：Receipt **不是** `git add` 的范围清单——`Add` 的范围以 internal/git 的
// `git add -A` 为唯一口径，路径清单由 git 包的 Status / Diff 在 Add 前采样产出。
type Receipt struct {
	Files []Result
}

// Record 追加一条单文件回执。
func (r *Receipt) Record(res Result) { r.Files = append(r.Files, res) }

// Applied 返回实际写入的回执。
func (r *Receipt) Applied() []Result { return filter(r.Files, true) }

// Skipped 返回被跳过的回执（含原因，供报告 skipped[]）。
func (r *Receipt) Skipped() []Result { return filter(r.Files, false) }

func filter(in []Result, written bool) []Result {
	var out []Result
	for _, res := range in {
		if res.Written == written {
			out = append(out, res)
		}
	}
	return out
}
