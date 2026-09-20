package reconcile

// Finding 封闭四键 schema 与 findings[] 的可复算排序（对账合同 §2）。
//
// 本文件是 M4 **唯一**的 finding 载体：T-…-050 ~ T-…-056 六项 R 检查与 T-…-057 的报告
// 投影全部消费本结构体，不得各自另定一份「像 finding 的结构体」。

import (
	"fmt"
	"sort"
	"strings"
)

// Finding 是一条对账发现，恰四键（合同 §2 逐字：check / severity / targets / detail）。
//
// 键集合**不增不减**：新增第五个字段会被 finding_test.go 的
// TestFindingSchemaExactlyFourKeys 当场判红（字段数 + JSON tag 集合 + 序列化键集合三重比对）。
type Finding struct {
	Check    string   `json:"check"`    // 十三值封闭枚举，见 check.go
	Severity string   `json:"severity"` // 恰两值：error | warning
	Targets  []string `json:"targets"`  // 非 nil；空集合序列化为 []，不得为 null
	Detail   string   `json:"detail"`   // 非空单句，含足以复算的事实
}

// FindingKeyCount 是 Finding 的封闭键数：恰 4。
const FindingKeyCount = 4

// findingKeys 是 JSON 键的封闭全集（顺序 = 结构体字段顺序 = 合同 §2 的键序）。
var findingKeys = [FindingKeyCount]string{"check", "severity", "targets", "detail"}

// FindingKeys 返回封闭键集合的副本（顺序即合同 §2 的键序）。
func FindingKeys() []string {
	out := make([]string, 0, FindingKeyCount)
	out = append(out, findingKeys[:]...)
	return out
}

// NormalizeTargets 归一化 targets：**非 nil** + 去空串 + 去重 + 字典序升序（合同 §2 第 3 条）。
//
// 空输入返回**长度 0 的非 nil 切片**——这是「空集合序列化为 [] 而不是 null」的落点。
func NormalizeTargets(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// NormalizeOrderedTargets 归一化**顺序固定**的 targets（合同 §8 的封闭例外，见 check.go）。
//
// 与 NormalizeTargets 的差别只有一处：**次序逐字保留、不去重**——因为次序本身承载语义
// （`domain_moved` 的 `targets[1]` / `targets[2]` 恒是旧 / 新领域，脚本按位取值）。
// 其余一格不放宽，反而更严：
//   - 元数必须**恰等于** arity（多一元少一元都报错，不静默截断、不补空位）；
//   - 每一元去首尾空白后必须**非空**（空位会让按位取值读出空串）；
//   - 返回值恒是**新切片**（不改入参，调用方后续改自己的切片不会污染 finding）。
func NormalizeOrderedTargets(in []string, arity int) ([]string, error) {
	if len(in) != arity {
		return nil, fmt.Errorf("targets 元数 = %d，顺序固定的 targets 必须恰 %d 元（合同 §8）",
			len(in), arity)
	}
	out := make([]string, 0, arity)
	for i, t := range in {
		v := strings.TrimSpace(t)
		if v == "" {
			return nil, fmt.Errorf("targets[%d] 为空：顺序固定的 targets 每一元都必须非空", i)
		}
		out = append(out, v)
	}
	return out, nil
}

// NewFinding 按枚举真源构造一条 finding。
//
// severity **不接受调用方自带**：一律从 checkTable 取，杜绝「同一 check 两处分级」；
// 未知 check（第 14 个取值）与空 detail 一律报错，不静默产出半成品 finding。
//
// targets 走两条口径之一，由 check 决定（不由调用方决定）：
//   - 默认（十二个 check）：集合语义 —— 去重 + 字典序升序（合同 §2 第 3 条）；
//   - 顺序固定例外（恰 `domain_moved` 一个，合同 §8）：次序逐字保留、元数恰
//     `DomainMovedTargetArity`，元数不符或有空位一律**报错**（不静默降级成集合语义）。
func NewFinding(check string, targets []string, detail string) (Finding, error) {
	spec, ok := SpecOf(check)
	if !ok {
		return Finding{}, fmt.Errorf("未知 check %q：check 是恰 %d 值的封闭枚举（合同 §3）",
			check, CheckCount)
	}
	if strings.TrimSpace(detail) == "" {
		return Finding{}, fmt.Errorf("check %q 的 detail 为空：detail 必须是含可复算事实的非空单句",
			check)
	}
	norm := NormalizeTargets(targets)
	if arity, ordered := TargetsArity(spec.Check); ordered {
		fixed, err := NormalizeOrderedTargets(targets, arity)
		if err != nil {
			return Finding{}, fmt.Errorf("check %q 的 %w", spec.Check, err)
		}
		norm = fixed
	}
	return Finding{
		Check:    spec.Check,
		Severity: spec.Severity,
		Targets:  norm,
		Detail:   detail,
	}, nil
}

// Validate 逐键反证四键封闭：check ∈ 十三值、severity == 表内分级、
// targets 非 nil 且已归一化（默认口径：去重 + 升序；顺序固定例外：元数恰 arity + 元素非空
// + 次序不做任何要求，见 check.go 的封闭例外表）、detail 非空。
func (f Finding) Validate() error {
	spec, ok := SpecOf(f.Check)
	if !ok {
		return fmt.Errorf("未知 check %q：check 是恰 %d 值的封闭枚举（合同 §3）",
			f.Check, CheckCount)
	}
	if f.Severity != spec.Severity {
		return fmt.Errorf("check %q 的 severity = %q，表内定死为 %q（合同 §3 单射）",
			f.Check, f.Severity, spec.Severity)
	}
	if f.Targets == nil {
		return fmt.Errorf("check %q 的 targets 为 nil：空集合必须是 []，不得为 null", f.Check)
	}
	if arity, ordered := TargetsArity(spec.Check); ordered {
		if _, err := NormalizeOrderedTargets(f.Targets, arity); err != nil {
			return fmt.Errorf("check %q 的 %w", f.Check, err)
		}
	} else if got := NormalizeTargets(f.Targets); strings.Join(got, "\x00") !=
		strings.Join(f.Targets, "\x00") {
		return fmt.Errorf("check %q 的 targets = %v 未归一化（须去重且字典序升序）", f.Check, f.Targets)
	}
	if strings.TrimSpace(f.Detail) == "" {
		return fmt.Errorf("check %q 的 detail 为空", f.Check)
	}
	return nil
}

// Code 返回本条 finding 对应的诊断码（单射的正向；未知 check 返回空串）。
func (f Finding) Code() string {
	code, _ := CodeOf(f.Check)
	return code
}

// severityRank 是 severity 的排序位次：**error 在前**（合同 §2 末条）。
// 表外取值排在末尾，保证排序对脏数据也是全序、不 panic。
func severityRank(severity string) int {
	for i, s := range severityTable {
		if s == severity {
			return i
		}
	}
	return SeverityCount
}

// checkRank 是 check 的排序位次 = 合同 §3 表格行序（0 ~ 11），表外取值排末尾。
func checkRank(check string) int {
	for i, s := range checkTable {
		if s.Check == check {
			return i
		}
	}
	return CheckCount
}

// firstTarget 返回 targets[0]（空集合返回空串，空串恒排在任何非空路径之前）。
func firstTarget(f Finding) string {
	if len(f.Targets) == 0 {
		return ""
	}
	return f.Targets[0]
}

// SortFindings 就地排序 findings[]：排序键 = (severity, check, targets[0])，error 在前。
//
// 三键之后再比「targets 全序列 + detail」——那不是新排序键，只是把合同三键**相等**的
// 并列项也钉成确定次序（否则同 check 同首目标的两条 finding 次序取决于输入顺序，
// 报告就不可逐字复算）。用 SliceStable：完全相等的两条保持输入次序。
func SortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra < rb
		}
		if ra, rb := checkRank(a.Check), checkRank(b.Check); ra != rb {
			return ra < rb
		}
		if ta, tb := firstTarget(a), firstTarget(b); ta != tb {
			return ta < tb
		}
		if ta, tb := strings.Join(a.Targets, "\x00"), strings.Join(b.Targets, "\x00"); ta != tb {
			return ta < tb
		}
		return a.Detail < b.Detail
	})
}

// SortedFindings 返回排好序的**副本**（不改动入参切片），并把 nil 收敛成空集合。
func SortedFindings(fs []Finding) []Finding {
	out := make([]Finding, 0, len(fs))
	out = append(out, fs...)
	SortFindings(out)
	return out
}
