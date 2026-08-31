// Package skill 把 SKILL.md 随二进制内嵌发布（技术方案 §13 末行）。
//
// `eg init` 把 Content 逐字写入 vault 根并纳入 Git，保证「工具与规程同版本」。
// 文档正文由 T-evergreen.s1_main_flow-158614-017 填写；本包只负责内嵌与逐字取用，
// 不做任何模板渲染或字符串加工。
package skill

import _ "embed"

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
