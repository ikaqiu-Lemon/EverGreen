package index

// 水位线与三向 diff（M5 索引架构合同 §5.1 A-44）。
//
// **唯一比对依据**：水位线 = `(head, files_hash)`，其中 `files_hash` 由**每文件
// `content_hash`** 按 `path` 升序聚合（FilesHash，落在 build.go —— 单一真源）。
//
// 三条不许被「优化」掉的裁决（合同 §5.1 排除表，逐条对应本文件的实现）：
//
//	① 不以 mtime / size 作为「未变更」的最终结论：`git checkout` 会换掉内容而 size 可能
//	   相同、mtime 也可能被工具还原；同秒内两次写在 1s 精度下不可分辨 ⇒ 静默漏更新。
//	   本文件因此只提供 QuickUnchanged 这一条**快路径**，且它的语义严格是
//	   「可以跳过重算 hash」，**不是**「文件没变」；最终判定恒走 content_hash（DiffFiles）。
//	② 不只用 HEAD：外部编辑器改了文件但未提交时 HEAD 不变，而 evergreen 的主要工作态
//	   恰恰是「工作区有未提交改动」。
//	③ content_hash 与 M1 `internal/store` 写口的 B3 同源同算法，**由调用方算好喂进来**：
//	   索引层不得自定义第二套 hash（那会复制 I-002「计数双源」型缺陷）。
//
// 零依赖边界（合同 §13 禁令）：本文件不 import 本仓任何包 —— `head` 由 CLI 层用
// `internal/git` 的只读 API 取好后作为普通字符串传入，索引包既不跑 git、也不读 Markdown。

import (
	"fmt"
	"sort"
)

// Watermark 是「索引自认为的现态」与「Markdown 的真实现态」之间的可比对标量对。
//
// 两处取值都恒为 (Git HEAD, files_hash)：前者来自 `index_meta`，后者来自一次现态扫描。
// 二者**任一**不相等即为陈旧（consistency.go 的 `W22`）。
type Watermark struct {
	// Head 是 Git HEAD 全长 40 位十六进制；非 git 仓恒空串（合同 §4.2）。
	Head string
	// FilesHash 是全部文件 content_hash 按 path 升序聚合的 SHA-256（FilesHash 函数）。
	FilesHash string
}

// WatermarkOf 取出索引自己记录的水位线（`index_meta.head` + `index_meta.files_hash`）。
func WatermarkOf(m Meta) Watermark {
	return Watermark{Head: m.Head, FilesHash: m.FilesHash}
}

// WatermarkFrom 按**现态**算出水位线：head 由调用方给（git 只读），files 是现态文件清单。
//
// files 允许乱序：聚合恒在内部按 path 升序做（FilesHash），因此同一现态的水位线唯一。
func WatermarkFrom(head string, files []File) Watermark {
	return Watermark{Head: head, FilesHash: FilesHash(files)}
}

// ReadWatermark 只读地取回索引记录的水位线（索引不可读时返回 error，由调用方转成诊断）。
func ReadWatermark(dir string) (Watermark, error) {
	m, err := ReadMeta(dir)
	if err != nil {
		return Watermark{}, err
	}
	return WatermarkOf(m), nil
}

// Equal 报告两个水位线是否逐字相等（head 与 files_hash 都相等才算相等）。
func (w Watermark) Equal(other Watermark) bool {
	return w.Head == other.Head && w.FilesHash == other.FilesHash
}

// String 是人类可读形态（只用于诊断消息，不参与任何判定）。
func (w Watermark) String() string {
	return fmt.Sprintf("head=%s files_hash=%s", shortHex(w.Head), shortHex(w.FilesHash))
}

// shortHex 把长十六进制截成前 12 位，便于人读；空串原样返回（并显式标注）。
func shortHex(s string) string {
	if s == "" {
		return "(空)"
	}
	if len(s) <= 12 {
		return s
	}
	return s[:12] + "…"
}

// ReadFiles 只读回 `files` 表（水位线的最小单位），按 path 升序。
//
// 它是**快路径的前置**：调用方拿到这份清单后，可用 QuickUnchanged 判断哪些文件可以
// 跳过重算 hash（直接沿用本清单里的 ContentHash），从而做到「未变更文件零读取」。
func ReadFiles(dir string) ([]File, error) {
	rows, err := readFileRows(dir)
	if err != nil {
		return nil, err
	}
	out := make([]File, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.File)
	}
	return out, nil
}

// readFileRows 读回 `files` 表全部行（含 indexed_at_unix），按 path 升序。
func readFileRows(dir string) ([]fileRow, error) {
	db, err := openDB(dbPathIn(dir), true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return readFileRowsFrom(db)
}

// QuickUnchanged 是合同 §5.1 允许的**快路径**：`(path, size, mtime_unix)` 三者与 `files`
// 表完全一致时，调用方**可以**跳过重算 content_hash，直接沿用索引里的 content_hash。
//
// 语义边界（不许被读成「文件没变」）：
//   - 命中 ⇒ 只表示「允许省掉一次 hash 计算」；
//   - 未命中 ⇒ **必须**重算 content_hash 再判定，不得据此直接认定文件已变。
//
// 因此快路径**不改变**最终判定语义：等价性由 TestIncrementalEqualsFullRebuild 与
// `eg index status --strict`（忽略快路径、全量重算）双向反证。
func QuickUnchanged(indexed, current File) bool {
	return indexed.Path == current.Path &&
		indexed.Size == current.Size &&
		indexed.MTimeUnix == current.MTimeUnix
}

// ChangeSet 是「索引记录的文件清单」与「Markdown 现态文件清单」的三向 diff 结果。
//
// 四个字段都是 vault 内相对路径（/ 分隔），各自按字典序升序 —— 增量更新的输入必须确定，
// 否则「增量结果与全量重建逐字等价」无从谈起。
//
// **重命名**在本口径下天然表达为 `Removed(旧路径) + Added(新路径)`：文件系统层面的
// rename 对索引而言就是一处消失、一处出现，卡片 ID 不变而 `cards.path` 换值。
// 不为 rename 另设第三种变更类型 —— 那会引入「rename 检测启发式」这一不可验证态
// （同内容不同路径未必是 rename，也可能是复制 + 删除）。
type ChangeSet struct {
	Added     []string
	Modified  []string
	Removed   []string
	Unchanged []string
}

// Empty 报告是否零变更（新增 / 修改 / 删除三者皆空）。
func (cs ChangeSet) Empty() bool {
	return len(cs.Added) == 0 && len(cs.Modified) == 0 && len(cs.Removed) == 0
}

// Affected 返回**需要重新派生索引行**的路径集合 = Added ∪ Modified（升序）。
// Removed 不在其中：它们只需删行，不需要读 Markdown。
func (cs ChangeSet) Affected() []string {
	out := make([]string, 0, len(cs.Added)+len(cs.Modified))
	out = append(out, cs.Added...)
	out = append(out, cs.Modified...)
	sort.Strings(out)
	return out
}

// Total 返回三类变更的条数之和（进 --json 的计数用；Unchanged 不计入）。
func (cs ChangeSet) Total() int {
	return len(cs.Added) + len(cs.Modified) + len(cs.Removed)
}

// String 是人类可读摘要（只用于诊断消息）。
func (cs ChangeSet) String() string {
	return fmt.Sprintf("新增 %d / 修改 %d / 删除 %d / 未变 %d",
		len(cs.Added), len(cs.Modified), len(cs.Removed), len(cs.Unchanged))
}

// DiffFiles 按 **content_hash** 做三向 diff（indexed = `files` 表，current = 现态清单）。
//
// 判定口径（逐条对应合同 §5.1）：
//   - current 有、indexed 无 ⇒ Added；
//   - 两侧都有且 content_hash **逐字相等** ⇒ Unchanged（mtime / size 一律不参与该结论）；
//   - 两侧都有但 content_hash 不等 ⇒ Modified；
//   - indexed 有、current 无 ⇒ Removed。
//
// 保守兜底：current 里 ContentHash 为空串的条目一律判为 Added / Modified —— 调用方没算
// hash 时，绝不允许把「不知道」静默读成「没变」。
func DiffFiles(indexed, current []File) ChangeSet {
	old := make(map[string]string, len(indexed))
	for _, f := range indexed {
		old[f.Path] = f.ContentHash
	}
	seen := make(map[string]bool, len(current))
	cs := ChangeSet{}
	for _, f := range current {
		if seen[f.Path] {
			continue // 同一路径在现态清单里出现两次：按第一条判，不产生第二条变更
		}
		seen[f.Path] = true
		prev, ok := old[f.Path]
		switch {
		case !ok:
			cs.Added = append(cs.Added, f.Path)
		case f.ContentHash == "" || prev != f.ContentHash:
			cs.Modified = append(cs.Modified, f.Path)
		default:
			cs.Unchanged = append(cs.Unchanged, f.Path)
		}
	}
	for _, f := range indexed {
		if !seen[f.Path] {
			cs.Removed = append(cs.Removed, f.Path)
		}
	}
	sort.Strings(cs.Added)
	sort.Strings(cs.Modified)
	sort.Strings(cs.Removed)
	sort.Strings(cs.Unchanged)
	return cs
}
