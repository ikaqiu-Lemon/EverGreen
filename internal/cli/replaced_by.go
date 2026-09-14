package cli

// `eg replaced-by` 的写路径（授权合同 §2 矩阵 #4；状态合同 §8.1 的 `set_replaced_by` op
// 与 §5.1 真值表的 E10 / W12 两象限；T-…-039）。
//
// 语义方向**只有一个**：在**失效卡**（`--target`）上写 `replaced_by`，指向新卡（`--to`）。
// 被指向的新卡**一个字节都不动**——反向写入会让「谁替代了谁」出现两份可漂移的记录，
// 反向查询归 S4，本阶段既不写反向指针、也不建索引。
//
// 与另两条状态命令共用 buildStateOpPlan → runPlan：E10（目标或指向端已逻辑删除 → 退 2 零写入）
// 与 W12（指向端是 deprecated 且未删除 → 退 0 + 提示）全部由既有 plan 校验产出，
// 本文件只如实透出，不复制判定。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
)

// runSetReplacedBy 实现 eg replaced-by --target <失效卡id> --to <新卡id> --reason <text>。
func (r *Root) runSetReplacedBy(inv *Invocation) (*Result, error) {
	target := strings.TrimSpace(inv.String("target"))
	to := strings.TrimSpace(inv.String("to"))
	reason := inv.String("reason")

	// reason 同时进 op.reason（供 commit 主题与报告）与 replaced_by.reason
	// （合同 §8.1 该 op 的理由字段落在子对象上，store 侧要求 target 与 reason 同时给全）。
	p, err := r.buildStateOpPlan(inv, plan.OpSetReplacedBy, target, reason,
		&plan.ReplacedBy{Target: to, Reason: reason, Given: true})
	if err != nil {
		return nil, err
	}

	res, runErr := runPlan(r, inv, p)
	if res != nil {
		res.Summary = append([]string{fmt.Sprintf(
			"replaced-by：在失效卡 %s 上写 replaced_by → %s（单向；被指向卡字节不变）",
			target, to)}, res.Summary...)
	}
	return res, runErr
}
