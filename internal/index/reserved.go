package index

// M6 运行时保留条目（R6 P0，M6 合同 §16.5 + R8 §18.1；T-…-072 批次 B2b1）。
//
// # 为什么要有这个文件
//
// M5 把 `.index/` 定义成「派生索引的家」，白名单恰 3 个 DB 文件（AllowedFiles，schema.go）。
// M6 把**运行时协作面**也放进同一个目录：`.index/run.lock`（进程间互斥锁）与
// `.index/txn/`（事务日志全树）。二者既不是索引、也不是污染 —— 它们是**另一类**在盘事实。
//
// 于是本文件引入一个与 AllowedFiles **正交**的集合：RuntimeReservedEntries。
// 两张表刻意**不合并**（R6 硬要求）：
//
//   - AllowedFiles 回答「这个目录里允许有哪些**索引**文件」——它的元素会被 Rebuild 清掉、
//     会被 Build 重新造出来，是**可重建派生**；
//   - RuntimeReservedEntries 回答「这个目录里有哪些**不属于索引、且索引一个字节都不许动**
//     的运行时条目」——它的元素恒被保留（`run.lock` 连 inode 都不许换、`txn/` 全树字节不变）。
//
// 把锁与日志混进 AllowedFiles 会让这两条相反的语义共用一张表，删除面立刻失去可判定性。
//
// # 类型是这张表的一等公民
//
// 保留只按名字保留是不够的：`run.lock` 若变成**目录**、`txn` 若变成**普通文件**，锁与日志
// 的语义当场失效，而目录看上去仍「有这两项」。因此每一项都带**期望类型**，并由
// InspectRuntimeReserved 用 `lstat` 做**不跟随 symlink** 的实测比对。
//
// symlink 一律违规，**不看目标是否合法**：一个指向合法普通文件的 `run.lock` symlink，会让
// 「锁文件 inode 不得被替换」这条断言退化成「链接没变」，也给了目录外写入一条通道。
// 这是安全判定，不是洁癖。
//
// # 本层只报不改（与 corrupt.go 同一条纪律）
//
// 本文件不删除、不重建、不修复任何条目，也不返回 error 语义的失败：类型违规是一个**诊断**
// （由 Inspect 折成既有的 `W24` + `unexpected_file`，零新增诊断码），写路径据此 fail closed
// 的出口（`E15` / 退出码 5）由 T-…-074 落地，不在本包内。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// EntryKind 是 `.index/` 下一个条目的文件类型（`lstat` 口径，**不跟随 symlink**）。
//
// 只有 EntryRegular / EntryDir 会出现在 ReservedEntry.Kind（即「期望类型」）里；
// 其余取值只作为**实测类型**出现在 ReservedViolation.Got 中。
type EntryKind string

const (
	// EntryRegular 普通文件。
	EntryRegular EntryKind = "regular"
	// EntryDir 目录。
	EntryDir EntryKind = "dir"
	// EntrySymlink 符号链接（无论目标是什么，都以链接本身记类型）。
	EntrySymlink EntryKind = "symlink"
	// EntryOther 既非普通文件也非目录的其它形态（设备 / 套接字 / 命名管道等）。
	EntryOther EntryKind = "other"
	// EntryAbsent 条目不在盘上。**不是**违规：锁与日志按需创建，未创建是正常态。
	EntryAbsent EntryKind = "absent"
	// EntryUnknown `lstat` 因非「不存在」以外的原因失败，类型不可知。
	EntryUnknown EntryKind = "unknown"
)

// ReservedEntry 是一条运行时保留条目的**规格**：名字 + 期望类型。
type ReservedEntry struct {
	// Name 是 `.index/` 下的条目名（不含路径分隔符）。
	Name string
	// Kind 是该条目的期望类型，取值只可能是 EntryRegular 或 EntryDir。
	Kind EntryKind
}

// 运行时保留条目的名字。**逐字写死而不是 import internal/txn**：
// 施工索引 §13 的依赖禁令使 index 不得依赖上层包，两侧同名由 R6 合同与本包测试双侧钉住。
const (
	runtimeLockName   = "run.lock"
	runtimeTxnDirName = "txn"
)

// runtimeReserved 是运行时保留条目的**单点真源**（恰两项，次序即判定次序）。
//
// 声明成数组而非切片：包外拿不到它，包内也不会被某次 append 悄悄改长。
var runtimeReserved = [...]ReservedEntry{
	{Name: runtimeLockName, Kind: EntryRegular},
	{Name: runtimeTxnDirName, Kind: EntryDir},
}

// RuntimeReservedEntries 返回 M6 运行时保留条目（**恰 2 项**，带期望类型）。
//
// 返回的是 runtimeReserved 的**副本**：调用方改了自己那份，下一次调用仍拿到原始名册。
//
// 与 AllowedFiles（schema.go，恰 3 个 DB 文件）是**两张互不相交的表**，永不合并：
// 前者恒被保留，后者可被 Rebuild 清扫后重建。
func RuntimeReservedEntries() []ReservedEntry {
	out := make([]ReservedEntry, len(runtimeReserved))
	copy(out, runtimeReserved[:])
	return out
}

// reservedNameSet 返回保留条目名字的集合（**只按名字**，不看类型）。
//
// 「只按名字」是有意的：类型违规的处置是**报告**（corrupt + W24），绝不是「因为类型不对
// 就把它当污染删掉」—— 删掉一把类型异常的锁，等于把一个可诊断的现场变成不可诊断的现场。
func reservedNameSet() map[string]bool {
	out := make(map[string]bool, len(runtimeReserved))
	for _, e := range runtimeReserved {
		out[e.Name] = true
	}
	return out
}

// reservedNames 返回保留条目的名字（次序即名册次序），仅用于消息渲染。
func reservedNames() []string {
	out := make([]string, 0, len(runtimeReserved))
	for _, e := range runtimeReserved {
		out = append(out, e.Name)
	}
	return out
}

// ReservedViolation 是一条运行时保留条目的**类型违规事实**（结构化，供上层如实转述）。
type ReservedViolation struct {
	// Name 是违规条目名（∈ RuntimeReservedEntries 的 Name）。
	Name string
	// Want 是该条目的期望类型（EntryRegular 或 EntryDir）。
	Want EntryKind
	// Got 是 `lstat` 实测到的类型。
	Got EntryKind
	// Symlink 说明该条目本身是不是符号链接（symlink 无论目标是否合法都算违规）。
	Symlink bool
}

// String 返回一行人类可读的违规说明（与结构化字段同源同事实，不引入新事实）。
func (v ReservedViolation) String() string {
	return fmt.Sprintf("%s 期望是%s、实测是%s（symlink=%v）",
		v.Name, kindText(v.Want), kindText(v.Got), v.Symlink)
}

// kindText 是 EntryKind 的中文说明（只用于消息渲染，机器判定一律用 EntryKind 常量）。
func kindText(k EntryKind) string {
	switch k {
	case EntryRegular:
		return "普通文件"
	case EntryDir:
		return "目录"
	case EntrySymlink:
		return "符号链接"
	case EntryOther:
		return "既非普通文件也非目录的其它形态"
	case EntryAbsent:
		return "不存在"
	default:
		return "不可知（lstat 失败）"
	}
}

// InspectRuntimeReserved 只读体检 dir（= vault/.index）下的运行时保留条目类型。
//
// 返回**第一条**违规（按 RuntimeReservedEntries 的次序，因此结果可复算）与是否存在违规。
// 判定口径：
//
//	不在盘         → 合法（锁与日志按需创建，未创建是正常态）
//	类型与期望相符 → 合法
//	symlink        → **违规**（不跟随、不看目标；见文件头「symlink 一律违规」）
//	类型与期望不符 → 违规（`run.lock` 是目录、`txn` 是普通文件等）
//	lstat 失败     → 违规（Got = EntryUnknown；不可知不等于没问题）
//
// 本函数**只读**：不 open、不删除、不创建、不修复任何条目，也不返回 error ——
// 「保留条目类型不对」是一个诊断（由 Inspect 折成 W24 + unexpected_file），不是一次失败。
func InspectRuntimeReserved(dir string) (ReservedViolation, bool) {
	for _, want := range runtimeReserved {
		got, symlink := lstatKind(filepath.Join(dir, want.Name))
		if got == EntryAbsent || got == want.Kind {
			continue
		}
		return ReservedViolation{
			Name: want.Name, Want: want.Kind, Got: got, Symlink: symlink,
		}, true
	}
	return ReservedViolation{}, false
}

// lstatKind 用 `lstat` 读出 path 的实测类型，并报告它本身是否为 symlink。
//
// 必须是 `lstat` 而不是 `stat`：`stat` 会跟随链接，于是「`run.lock` 是一个指向别处的
// symlink」会被读成「普通文件」，违规当场消失。
func lstatKind(path string) (EntryKind, bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EntryAbsent, false
		}
		return EntryUnknown, false
	}
	mode := fi.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		return EntrySymlink, true
	case mode.IsDir():
		return EntryDir, false
	case mode.IsRegular():
		return EntryRegular, false
	default:
		return EntryOther, false
	}
}
