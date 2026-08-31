// [S1] internal/mdfile：frontmatter 只读解析、分区切分、块切分与 block_hash。
//
// 允许依赖：model。
// 首次落地：S1（施工索引 §13）。
// 写路径硬约束（施工索引 §16）：只做 YAML 只读解析 + 字节区间外科式写入，
// 禁止任何 YAML 序列化回写（Marshal / Encoder 一律不得出现在写路径，
// 连注释也不写这两个符号名，以免打红 make lint 的 guard 步）；
// 用户内容全链路只用 []byte。
// 本阶段为占位空包，业务实现见 T-evergreen.s1_main_flow-158614-005。
package mdfile
