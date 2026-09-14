package plan

// matrix_contract_test.go —— M6 · T-…-075 批次 C1b-contract：写权限矩阵的**聚合闭合反证**
// （合同 §2.8）。
//
// 与 matrix_test.go 的 TestWritePermissionMatrix 分工：那支逐行逐格抄合同 §2.1–§2.7（43 行 ×
// 2 格），是**逐格正面对撞**；本支只钉**聚合计数的封闭性**——行数 / 判定格数 / 对象类数 /
// 严格解锁 / 条件解锁 / 两路皆拒 / §2.9 锁定行数——把「矩阵规模一格未动」做成一条独立、
// 快速、可单独复算的等式。两支互不代替：逐格断言防「某一格被悄悄翻转」，聚合断言防
// 「整表规模被增删」。
//
// C1b-contract 把它与 internal/cli 的写口守门（TestStateSetterCallSitesConfinedToStore）
// 合起来，构成「写权限硬约束」的两层证据：矩阵定谁能写哪一格（本支），写口守门保证
// 权威状态只经五个 setter 改写、任何别处不得旁路（cli 侧）。

import "testing"

// TestWritePermissionMatrixCountsClosed 复算写权限矩阵的七项聚合计数，逐项与合同 §2.8 对齐。
func TestWritePermissionMatrixCountsClosed(t *testing.T) {
	if n := len(Matrix()); n != 43 {
		t.Fatalf("写权限矩阵行数 = %d，合同 §2.8 定死为 43", n)
	}
	if n := MatrixCells(); n != 86 {
		t.Fatalf("判定格总数 = %d，期望 43 × 2 = 86", n)
	}
	if n := len(MatrixObjects()); n != 7 {
		t.Fatalf("对象类数 = %d，合同 §2.8 定死为 7", n)
	}
	if n := len(StrictUnlockRows()); n != 16 {
		t.Fatalf("严格解锁行 = %d，期望恰 16", n)
	}
	if n := len(ConditionalUnlockRows()); n != 1 {
		t.Fatalf("条件解锁行 = %d，期望恰 1（#12）", n)
	}
	// 两路皆拒：M4 · T-…-055 阶段 1 依 A-34 把 #33 的 P-A 由 🔴 改 ✅ 后由 5 降为 4。
	if n := len(BothDeniedRows()); n != 4 {
		t.Fatalf("两路皆拒行 = %d，期望恰 4（M4 阶段 1 后 5 − 1 = 4）", n)
	}
	// §2.9 锁定条款覆盖 #35–#39 恰五行。
	if n := len(AdjudicationLockedRows()); n != 5 {
		t.Fatalf("§2.9 锁定行 = %d，期望恰 5（#35–#39）", n)
	}
}
