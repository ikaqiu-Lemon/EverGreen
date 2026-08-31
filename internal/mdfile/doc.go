// [S1] internal/mdfile：frontmatter 只读解析、分区切分、块切分与 block_hash。
//
// 允许依赖：model。
// 首次落地：S1（施工索引 §13）。
// 写路径硬约束（施工索引 §16）：只做 YAML 只读解析 + 字节区间外科式写入，
// 禁止任何 YAML 序列化回写（Marshal / Encoder 一律不得出现在写路径，
// 连注释也不写这两个符号名，以免打红 make lint 的 guard 步）；
// 用户内容全链路只用 []byte。
//
// 文件分工（T-evergreen.s1_main_flow-158614-005）：
//   - doc_index.go    Doc / Span 半开区间索引、Parse、Render（区间拼接）、SelfCheck
//   - sections.go     五分区固定名与顺序（F5）、改名即报错、未知分区原样保留
//   - block.go        五种块型切分、block_hash（只读规范化）、块定位符（仅诊断用）
//   - frontmatter.go  只读解析（DecodeFM / FMKeys）与 ParseCard / ParseNote / ParseSource
//   - append.go       **唯一写路径**：区间拼接 + 插入字节（分区追加 / FM 追加键 / FM 序列追加）
//   - unprocessed.go  收件区「一个顶层列表项 = 一个条目」（键为 source_id）
package mdfile
