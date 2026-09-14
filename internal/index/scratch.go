package index

// [S4] internal/index/scratch.go：一次性派生目录（scratch）在本仓的**唯一** owner
// （M5 · T-evergreen.s1_main_flow-158614-068 纠正轮）。
//
// # 为什么这个文件必须存在
//
// `eg bench` 要测「全量构建 / 增量同步」的耗时，这两条指标天生要写盘，而 bench 又承诺
// 对当前 vault 零写入 —— 于是它必须在一个一次性目录里放一份 vault 副本，测完即删。
//
// # 为什么签名里**没有**「待删除路径」这个参数
//
// 上一版曾提供 `RemoveScratch(dir string)`。那个形态是错的：它等于本包对外暴露了一个
// **可对任意路径递归删除**的能力，于是 U-02「无破坏性回滚」的派生物例外
// （其正面反证是「`internal/index` 删的那个 dir 只可能是本包自己管的派生目录」）
// 就退化成了一句**注释承诺** —— 调用方完全可以把一个含权威产物的路径传进来，
// 而门禁看不出来。事实上 bench 传进去的正是一个含 `domains` 副本的临时 vault。
//
// 本版把那条性质从「注释承诺」升级为**类型与控制流保证**：
//
//   - `dir` 只可能来自本函数体内的 `os.MkdirTemp` —— 调用方**无从提供**它；
//   - 调用方拿到的是一个**已经绑定了该 dir** 的 closure，改不了删除目标；
//   - `prefix` 只是目录名前缀，不是路径：`os.MkdirTemp` 对含路径分隔符的 pattern
//     直接返回 `ErrPatternHasSeparator`，因此 prefix 也无法用来做目录穿越。
//
// 这三条都由 `scratch_test.go` 机器反证（含一条 AST 级反证：本包任何导出函数都不会
// 把自己的 string 形参喂给递归删除）。

import (
	"fmt"
	"os"
)

// NewScratch 创建一个全新的空一次性目录，并把**它自己的**清理动作一并返回。
//
// 语义与 Rebuild 删 `.index/` 同源：删的是**零信息损失的派生物**。差别在于这里的
// 派生物是本函数刚刚亲手造出来的空目录，因此「删的不是权威产物」这件事不需要信任调用方。
//
// prefix 仅作目录名前缀（可读性用途）。返回的 cleanup **幂等**：目录已不存在时返回 nil。
//
// 调用约定：拿到 cleanup 就应当 `defer` 掉它；本包不做后台回收（那会引入 M6 才有的生命周期管理）。
func NewScratch(prefix string) (string, func() error, error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		// 含路径分隔符的 prefix 会在这里被 os.MkdirTemp 拒掉，且此时没有任何目录被创建。
		return "", nil, fmt.Errorf("创建一次性目录（前缀 %q）失败：%w", prefix, err)
	}
	cleanup := func() error {
		// U-02 派生物例外的唯一形态：删的就是本包上面刚造出来的那个 dir。
		return os.RemoveAll(dir)
	}
	return dir, cleanup, nil
}
