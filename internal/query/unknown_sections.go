package query

// 非固定分区（unknown sections）的**共享**取数与投影（I-…-007）。
//
// 背景：Schema v2 把知识卡从五分区收敛为三分区（mdfile.CardSections()），`解释与依据` /
// `理解自检` 不再是固定分区。存量 v1 卡里的这两段（以及任何用户自建 H2）此前在 `card show`
// 的 data.sections 里因键集合恒定（只含固定分区）而**完全不可见**——JSON 与文本都读不到，
// 迁移前无从核对。opinion show 亦有同样的盲区（用户在观点里自建 H2 时）。
//
// 本文件是 card show 与 opinion show **共用**的一套口径（同一个 DTO、同一个取数 helper、
// 同一份 body 裁剪规则），因此两条读命令对「非固定分区」的呈现必然一致，不存在只在 card 上
// 特判、opinion 又另写一份的分叉（风险 R-25）。渲染层（internal/cli）再共用一个文本 helper，
// 使 JSON 与文本同源同事实。
//
// 底层事实来源是 mdfile.Doc.UnknownSections(kind)：
//   - 按 Markdown 源码里 H2 的**出现顺序**返回，不重排；
//   - 允许同名未知 H2 多次出现，不去重（它就是这么写的，如实呈现）；
//   - v1 存量被移除的分区仍算「非固定」（UnknownSections 不按 v1 schema 分流），因此这里能
//     稳定地把 `解释与依据` / `理解自检` 暴露出来，直到迁移（T-…-009）人工收口。
//
// 本文件只读：不写文件、不调 git、不碰 schema/model/store/plan/index 写模型。

import (
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// UnknownSection 是一个非固定分区的投影：分区名 + 完整正文。
//
// **card 与 opinion 共用同一型**（不各立一份）：这是「同一套 unknown-section 口径」的类型载体。
// 字段声明序即 JSON 键序——encoding/json 按结构体字段顺序编码，故元素恒为 `{"name":…,"body":…}`，
// 无需自定义 MarshalJSON（用 struct 而非 map，键序天然固定、且恰两个键）。
//
// Body 是该分区的**完整**原始正文（逐字取字节，只裁首尾空白，不做 Markdown 再渲染、不截首行）：
// 与固定分区（cardSections / opinionSections）的裁剪口径逐字一致，内部的列表 / 代码块 /
// 换行 / 甚至围栏代码块里的伪 H2 全部原样保留。
type UnknownSection struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// unknownSections 从文档的非固定 H2 折出 []UnknownSection。
//
// 归一：无非固定分区时返回**空数组**（`[]UnknownSection{}`，非 nil）——对外 JSON 是 `[]` 而不是
// `null`，与 sources / relations 等既有字段的空数组口径一致；文本渲染据此不产生任何噪声。
//
// 越界安全：span 的 Body/End 若越出 raw 边界或首尾倒置，**跳过该段**（与 cardSections /
// opinionSections 的边界判定同口径），绝不 panic、绝不切出错位正文。
func unknownSections(doc *mdfile.Doc, raw []byte, kind mdfile.Kind) []UnknownSection {
	out := []UnknownSection{}
	if doc == nil {
		return out
	}
	for _, span := range doc.UnknownSections(kind) {
		if span.Body > len(raw) || span.End > len(raw) || span.Body > span.End {
			continue
		}
		out = append(out, UnknownSection{
			Name: span.Name,
			Body: strings.TrimSpace(string(raw[span.Body:span.End])),
		})
	}
	return out
}
