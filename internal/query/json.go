package query

// 包内 JSON 小工具：给需要**固定键序**的嵌套结构（如 card show 的 sections）编码字符串。
//
// 与 CLI 渲染同款设置（不转义 HTML、无尾换行），保证同一份事实在两处编码逐字一致。

import (
	"bytes"
	"encoding/json"
)

// jsonString 把字符串编码成 JSON 字面量（不转义 HTML）。
func jsonString(s string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
