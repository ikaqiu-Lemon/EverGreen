package cli

// 非固定分区（unknown sections）的**共享文本渲染** helper（I-…-007）。
//
// card show 与 opinion show 的文本模式共用这一份，使「非固定分区」的呈现口径一致，且与
// --json 的 data.unknown_sections 同源同事实：逐段显示**分区名 + 完整正文**（正文取自
// query 层同一份 UnknownSection.Body，不截首行、不只报数量）。无非固定分区时返回零行——
// 空数组绝不制造噪声。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// unknownSectionLines 把非固定分区折成人类可读行：每段一行「非固定分区 <名称>：<完整正文>」。
func unknownSectionLines(secs []query.UnknownSection) []string {
	lines := []string{}
	for _, s := range secs {
		lines = append(lines, fmt.Sprintf("非固定分区 %s：%s", s.Name, s.Body))
	}
	return lines
}
