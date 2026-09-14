package index

// 无损重建（合同 §4.3「不兼容 = 重建，永不迁移」+ §4.4「rebuild 与 fresh build 等价」）。
//
// 语义只有一句：**丢弃旧索引 + 走 build.go 的全量路径**。
//
// 为什么不写迁移：迁移逻辑会引入「旧库半迁移」这一不可验证态；而重建的代价只是
// O(全量 build)，且索引恒为可重建派生 —— 删掉它零信息损失。这条推论是 F6 的直接结果，
// 不是本 task 的取舍。
//
// 零写权威：重建全程只动 `.index/` 这一个目录；权威 Markdown 一个字节都不碰
// （`TestRebuildNeverTouchesMarkdown` 用「全库字节快照前后逐字比对」反证）。
//
// # M6 之后「丢弃旧索引」不再等于「删掉整个目录」（T-…-072 批次 B2b2）
//
// M6 把运行时协作面搬进了同一个目录：`.index/run.lock`（进程间互斥锁）与
// `.index/txn/`（事务日志全树）。它们**不是**派生索引，删掉它们不是「零信息损失」：
//
//   - `run.lock` 被换掉 inode，等于把「同一把锁」偷换成两把 —— 正持锁的进程与后来者
//     会各自锁住一个不同的 inode，互斥当场失效（而两边都以为自己拿到了锁）；
//   - `txn/` 被删掉，等于把「崩溃后可回滚的前像」连同 `seq`、`quarantine` 一起抹掉 ——
//     一次崩溃恢复所需的唯一依据没了，半提交状态从可恢复变成不可恢复。
//
// 因此「整目录一把梭删除」这条老实现在 M6 语境下是**错的**，本文件用
// purgeNonReserved 取代它：删除面从「整棵目录」收窄到「逐项、且只删非保留条目」。
//
// # 本文件不使用标准库的整树删除 API（T-…-072 批次 B2c2）
//
// U-02（`TestU02_NoDestructiveRollback`）把「破坏性回滚动作」封成一条可 grep 的硬门禁，
// 其中对 `internal/index/` 唯一的豁免形态是「删的就是索引目录形参本身」。B2b2 之后本文件
// 删的已经**不是** `dir` 而是它下面的某个具名子条目，于是那条豁免不再覆盖它 ——
// 正确的解法是让实现回到门禁之内，而不是把门禁改宽。
//
// 因此递归删除改由本文件私有的 removeTree 落地：深度优先遍历（不跟随符号链接）、
// 先删子项再删空目录，每一步都是**非递归**的 os.Remove。它与标准库整树删除的区别不在效率，
// 而在**可判定性**：删除面逐项可见、每一次删除都落在 root 子树内，评审时不需要相信任何注释。

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Rebuild 先**逐项**清掉 dir 里的非保留条目（`eg.db` / `-wal` / `-shm`、外部污染、
// 遗留 tmp、意外子目录），再按 Build 的全量路径重建。
//
// M6 运行时保留条目（`run.lock` / `txn/`，见 reserved.go）一律**原样留下**：
// 锁连 inode 都不换，日志全树一个字节都不动。
//
// 结果与「在空目录上 fresh build」在 Digest 口径下**逐字等价**（同一 vault 同一快照）。
// 旧库不可读 / 已损坏 / 版本不匹配都不影响本函数：它根本不读旧库。
func Rebuild(dir string, snap Snapshot, opt Options) (*Result, error) {
	if err := purgeNonReserved(dir); err != nil {
		return nil, err
	}
	return Build(dir, snap, opt)
}

// purgeNonReserved 把 dir 清成「只剩 M6 运行时保留条目」的状态（**Rebuild 专用**）。
//
// # 与 cleanupBuildAttempt 是两条**方向相反**的删除策略，永不互调
//
//	purgeNonReserved（本函数）   删除面 = 目录里**除保留条目之外的一切**，
//	                            不关心这些条目是谁造的、什么时候造的；
//	                            用途：重建前把旧派生物与污染一次清空。
//	cleanupBuildAttempt（build.go） 删除面 = **本次 Build 调用亲手造出来的那几个路径**，
//	                            调用前就在盘的一律保留（含污染）；
//	                            用途：一次失败的构建不留半成品，也不顺手替用户打扫。
//
// 两者刻意做成两个独立函数、而不是同一个函数加一个 bool 开关：删除面是本 task 里
// 最危险的一格，用参数切语义会让「这次到底会删掉什么」在调用点上不可读、在评审时不可判。
//
// # 保留条目**即使类型违规也不删**
//
// 判定只看名字（reservedNameSet），不看类型。一把变成了目录的 `run.lock` 是一个需要
// **人来看**的现场：删掉它，可诊断的现场就变成了不可诊断的现场，而 Inspect 那条
// W24 / `unexpected_file` 诊断也会在下一次体检时凭空消失。只报不改的纪律在这里同样成立。
//
// # `.index` 根**永不**进递归删除
//
// 递归面只覆盖「`.index/` 下某一个具名的非保留子条目」（意外子目录可能非空）。
// 目录本身在本函数里只可能被 os.Remove 删掉一次 —— 而且仅当它压根不是目录的时候
// （被外部换成了普通文件 / 符号链接，那是目录位置上的污染，装不下任何保留条目）。
func purgeNonReserved(dir string) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil // 索引缺失是重建的正常起点，不是错误。
	}
	if err != nil {
		return fmt.Errorf("检查旧索引目录 %s 失败：%w", dir, err)
	}
	if !fi.IsDir() {
		// `.index` 这个**位置**被外部占成了普通文件 / 符号链接：它连保留条目都装不下，
		// 因此就地删掉这一个条目（非递归即可），让 Build 重新把目录建出来。
		if err := os.Remove(dir); err != nil {
			return fmt.Errorf("清除占据 %s 位置的非目录条目失败：%w", dir, err)
		}
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("读旧索引目录 %s 失败：%w", dir, err)
	}
	reserved := reservedNameSet()
	for _, e := range entries {
		if reserved[e.Name()] {
			continue // run.lock / txn：inode 与全树都必须活到重建之后。
		}
		child := filepath.Join(dir, e.Name())
		if err := purgeEntry(child); err != nil {
			return fmt.Errorf("清除 %s 失败：%w", child, err)
		}
	}
	return nil
}

// purgeEntry 删掉 `.index/` 下**一个具名的非保留子条目**。
//
// 先试非递归 os.Remove：文件、符号链接、空目录一次搞定，也顺带把「非空目录」这件事
// 暴露成一个明确的失败。只有确认它是一棵非空目录（遗留 tmp 目录、意外子目录）时才整棵删。
//
// 调用点保证 path 恒为 `filepath.Join(indexDir, <某个条目名>)`，因此这里的递归删除
// 永远够不到 `.index` 根，更够不到任何权威产物。
func purgeEntry(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	st, statErr := os.Lstat(path)
	if statErr != nil {
		return err // 连类型都读不到：如实回传第一现场的错误。
	}
	if !st.IsDir() {
		return err // 不是目录却删不掉（权限 / 占用）：不该被递归删除掩盖。
	}
	return removeTree(path)
}

// removeTree 深度优先删掉以 root 为根的**单棵**子树（含 root 自身）。
//
// # 为什么自己写而不是调标准库的整树删除
//
// 见文件头「本文件不使用标准库的整树删除 API」：U-02 只豁免「删索引目录形参本身」这一种
// 形态，而本文件删的是它的某个具名子条目。把实现写成「遍历 + 逐条 os.Remove」让删除面
// 从一次不可见的库内递归，变成一串**逐项可见**的删除。
//
// # 三条结构性护栏
//
//	① 不跟随符号链接：filepath.WalkDir 用 `lstat` 判类型，链接一律按「非目录条目」处理 ——
//	   于是 `.index/tmp/x -> /etc` 这种条目被删掉的只是链接本身，链接指向的目录纹丝不动；
//	② 先子后父：WalkDir 是先序遍历，目录只登记不删，等遍历结束后**逆序**（深的在前）
//	   逐个 os.Remove —— 每一次目录删除都发生在它已空之后，因此每一步都是非递归的；
//	③ 删除范围恒 ⊆ root：所有被删路径都由 WalkDir 从 root 向下产出，构造上不可能上溯。
//
// 「不存在」在两处都被吸收成成功：并发清理与遍历期间的消失不该把一次重建判失败。
func removeTree(root string) error {
	var dirs []string // 先序登记的目录，删除时逆序取用（深度优先的后序删除）。
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil // 已经不在了：目标状态已达成。
			}
			return walkErr
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		// 普通文件 / 符号链接 / 其它形态：就地非递归删除。
		return removeIfPresent(p)
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := removeIfPresent(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

// removeIfPresent 非递归删除 path，并把「本来就不存在」吸收成成功。
func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// EnsureBuilt 是 `eg index build` 的语义落点（合同 §8.1 第 1 行）：
//
//	索引不存在                → 全量构建，Action = ActionBuilt
//	索引在位且 healthy        → no-op（不重建、不写一个字节），Action = ActionNoop
//	索引在位但不可用（W24）   → **可恢复重建**（先清再全量建），Action = ActionRepaired
//
// 第三支就是「损坏可恢复」这条 Acceptance 的形态：坏索引不是死局，也不需要用户手工
// `rm -rf`；但它**必须留痕** —— 返回值里的 Diagnosis 会被 CLI 层转成一条 W24 warning。
func EnsureBuilt(dir string, snap Snapshot, opt Options) (*Result, Action, Diagnosis, error) {
	diag := Inspect(dir)
	switch diag.Health {
	case HealthHealthy:
		return nil, ActionNoop, diag, nil
	case HealthMissing:
		res, err := Build(dir, snap, opt)
		return res, ActionBuilt, diag, err
	default:
		res, err := Rebuild(dir, snap, opt)
		return res, ActionRepaired, diag, err
	}
}

// Action 是一次 `eg index build|rebuild` 实际做了什么（机器可读，进 --json 的 data.action）。
type Action string

const (
	// ActionBuilt 索引原本缺失，本次全量构建。
	ActionBuilt Action = "built"
	// ActionNoop 索引原本 healthy，本次一个字节都没写。
	ActionNoop Action = "noop"
	// ActionRepaired 索引原本不可用，本次先清再全量重建（可恢复重建）。
	ActionRepaired Action = "repaired"
	// ActionRebuilt 用户显式 `eg index rebuild`：无条件先清再全量重建。
	ActionRebuilt Action = "rebuilt"
)

// Actions 返回封闭的动作集合（恰 4 值）。
func Actions() []string {
	return []string{string(ActionBuilt), string(ActionNoop),
		string(ActionRepaired), string(ActionRebuilt)}
}
