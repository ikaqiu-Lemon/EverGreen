package filter

// [M3] internal/query/filter/unreviewed.go：**ADR-20 文件级隔离的唯一落点**
// （提案与状态合同 §6.1 ADR-20 硬约束行；T-evergreen.s1_main_flow-158614-042）。
//
// # 为什么整个判定必须只在这一个文件里
//
// ADR-20 把「`updated_at > reviewed_at`」定成**只读信号**，**只允许出现在筛选条件里**：
// 它不得进入排序权重、收敛判定、关系分析、综述取材。把判定关进单文件单包，
// 「谁用了这个信号」就退化成一条依赖关系事实，可被 `go list -deps` 机械反证
// （用例 TestADR20_FilterIsolation）。因此：
//
//   - 本包**不导入** internal/query（无反向依赖，调用方自己扫描、自己组装 Candidate）；
//   - internal/query/rank、internal/query/relations、internal/rules/converge、
//     internal/query/review 四个包**不得导入本包**（CI 依赖方向门禁属 M6，M3 只保证事实）。
//
// # 判定口径（合同 §6.1 逐行）
//
//   - `updated_at > reviewed_at` → 未过目（严格大于：两者相等表示「过目之后未再变动」）；
//   - **无 `reviewed_at` 的产物计入**：缺省等价于「从未过目」，判定按 `reviewed_at = -∞`
//     处理——本包因此**不回填**任何默认时刻，缺省走的是独立分支；
//   - 可叠加**领域 / 标签 / 时间**三类条件，与未过目判定取**交集**。
//
// # 无副作用
//
// 本包只读：不写文件、不改状态、不产生任何提醒或督促类文案，也不做任何时间上的自动过期
// 推断（后者属 S3）。同一输入两次执行输出逐字相同（含顺序）。

import (
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrStampUnusable 是「时刻不可比较」族：`updated_at` 缺失或不是带时区 RFC3339 时，
// 本包**不猜**（既不当作已过目也不当作未过目），直接失败上抛，由命令层翻译成校验失败。
var ErrStampUnusable = errors.New("无法比较 updated_at 与 reviewed_at")

// Candidate 是一个待判定产物的**只读快照**（四类产物同构，不按类型分叉）。
//
// UpdatedAt / ReviewedAt 都是 frontmatter 的逐字原值；ReviewedAt 为**空串**即
// 「该键缺省」（从未过目）——调用方不得用某个默认时刻替代空串。
type Candidate struct {
	ID         string
	Kind       string
	Path       string
	Domain     string
	Tags       []string
	UpdatedAt  string
	ReviewedAt string
}

// Spec 是可叠加的三类筛选条件（领域 / 标签 / 时间），全部为空即「全库、不限」。
//
// Tags 多值为 AND（产物须同时含全部标签，逐字相等比较）；
// Since / Until 是 `updated_at` 日期部分的闭区间（与 M2 查询合同的 --since / --until 同口径）。
type Spec struct {
	Domain string
	Tags   []string
	Since  string
	Until  string
}

// Unreviewed 判定单个产物是否「未过目」。
//
// 分支恰两条，缺省不与「有值」混流：
//   - ReviewedAt 为空（键缺省）→ true（`reviewed_at = -∞`，从未过目）；
//   - 两者都有值 → `reviewed_at < updated_at` 为 true（严格大于才算未过目）。
func Unreviewed(c Candidate) (bool, error) {
	updated, err := model.ParseStamp(c.UpdatedAt)
	if err != nil {
		return false, fmt.Errorf("%w：%s 的 updated_at=%q 不可用：%v",
			ErrStampUnusable, c.ID, c.UpdatedAt, err)
	}
	if c.ReviewedAt == "" {
		// 缺省即「从未过目」：这里**不**回填任何默认时刻，直接命中。
		return true, nil
	}
	reviewed, err := model.ParseStamp(c.ReviewedAt)
	if err != nil {
		return false, fmt.Errorf("%w：%s 的 %s=%q 不可用：%v",
			ErrStampUnusable, c.ID, model.FMKeyReviewedAt, c.ReviewedAt, err)
	}
	return reviewed.Before(updated), nil
}

// Select 返回既命中三类条件、又判定为未过目的产物（**保持入参顺序**，不做任何重排）。
//
// 为什么不在这里排序：排序口径属查询输出层，ADR-20 明令这个信号不得进入排序权重——
// 本包连排序都不碰，才谈得上「只出现在筛选条件里」。
func Select(in []Candidate, spec Spec) ([]Candidate, error) {
	out := make([]Candidate, 0, len(in))
	for _, c := range in {
		if !Match(c, spec) {
			continue
		}
		hit, err := Unreviewed(c)
		if err != nil {
			return nil, err
		}
		if hit {
			out = append(out, c)
		}
	}
	return out, nil
}

// Match 判定三类叠加条件（领域 / 标签 / 时间），与未过目判定相互独立。
func Match(c Candidate, spec Spec) bool {
	if spec.Domain != "" && c.Domain != spec.Domain {
		return false
	}
	if !hasAllTags(c.Tags, spec.Tags) {
		return false
	}
	day := dateOf(c.UpdatedAt)
	if spec.Since != "" && day < spec.Since {
		return false
	}
	if spec.Until != "" && day > spec.Until {
		return false
	}
	return true
}

// hasAllTags 判 AND 语义：want 里每个标签都得在 have 里逐字出现。
func hasAllTags(have []string, want []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// dateOf 取时刻的日期部分（前 10 字符）。只做字符串截取：时间闭区间比较与 M2 同口径，
// 不引入第二套时区推断。
func dateOf(stamp string) string {
	if len(stamp) < 10 {
		return stamp
	}
	return stamp[:10]
}
