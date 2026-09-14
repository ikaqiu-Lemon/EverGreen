package git

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrDomainRequired 表示提交信息缺少 domain——领域由目录唯一决定，上层必须给出。
var ErrDomainRequired = errors.New("提交信息缺少 domain")

// Message 是一次提交的信息来源。
//
// 主题：`<verb>(<domain>): <subject>`；
// 正文：`Reason:`（取 plan.reason，EG-CVG-04）与 `Requirement:`（取 plan.requirement_ids）。
type Message struct {
	Verb           string
	Domain         string
	Subject        string
	Reason         string
	RequirementIDs []string
}

// 正文字段前缀（写死，供报告与人工检索）。
const (
	ReasonPrefix      = "Reason:"
	RequirementPrefix = "Requirement:"
)

// Build 归一化并渲染提交信息，返回主题、正文与 warning 清单。
//
// verb 未知 → warning 并退化为 process，**不拒绝提交**；
// requirement_ids 缺失 → 正文无 Requirement: 行并返回一条 warning；
// reason 缺失 → 正文无 Reason: 行并返回一条 warning。
func (m Message) Build() (subject, body string, warnings []string, err error) {
	if strings.TrimSpace(m.Domain) == "" {
		return "", "", nil, ErrDomainRequired
	}
	verb, known := model.NormalizeVerb(m.Verb)
	if !known {
		warnings = append(warnings, fmt.Sprintf("verb %q 未知，已退化为 %s", m.Verb, model.VerbProcess))
	}
	subject = fmt.Sprintf("%s(%s): %s", verb, m.Domain, m.Subject)

	var lines []string
	if strings.TrimSpace(m.Reason) != "" {
		lines = append(lines, ReasonPrefix+" "+m.Reason)
	} else {
		warnings = append(warnings, "plan.reason 缺失，正文无 "+ReasonPrefix+" 行")
	}
	ids := nonEmpty(m.RequirementIDs)
	if len(ids) > 0 {
		lines = append(lines, RequirementPrefix+" "+strings.Join(ids, ", "))
	} else {
		warnings = append(warnings, "plan.requirement_ids 缺失，正文无 "+RequirementPrefix+" 行")
	}
	return subject, strings.Join(lines, "\n"), warnings, nil
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
