// Package skill 把 SKILL.md 随二进制内嵌发布（技术方案 §13 末行）。
//
// `eg init` 把 Content 逐字写入 vault 根并纳入 Git，保证「工具与规程同版本」。
// 文档正文由 T-evergreen.s1_main_flow-158614-017 填写；本包只负责内嵌与逐字取用，
// 不做任何模板渲染或字符串加工。
package skill

import (
	_ "embed"
	"regexp"
)

// FileName 是写入 vault 根的文件名。
const FileName = "SKILL.md"

//go:embed SKILL.md
var content []byte

// Content 返回内嵌 SKILL.md 的字节副本（调用方拿到的是副本，改不到内嵌数据）。
func Content() []byte {
	out := make([]byte, len(content))
	copy(out, content)
	return out
}

// sampleRE 匹配被 `<!-- e2e-sample: N -->` 标注的 ```json 代码块。
var sampleRE = regexp.MustCompile("(?s)<!-- e2e-sample: \\d+ -->\\s*```json\\n(.*?)\\n```")

// ParseSamples 从任意 SKILL.md 文本里抽出被标注的 ChangePlan 样例。
func ParseSamples(doc string) []string {
	var out []string
	for _, m := range sampleRE.FindAllStringSubmatch(doc, -1) {
		out = append(out, m[1])
	}
	return out
}

// Samples 返回内嵌 SKILL.md 里的 ChangePlan 样例，供端到端用例直接当输入跑
// （「文档样例即用例」：样例一旦漂移，e2e 立刻失败）。
func Samples() []string { return ParseSamples(string(content)) }
