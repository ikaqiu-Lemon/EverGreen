// [S1] internal/store：唯一状态写口——读盘、content_hash 比对、写前字节自检、
// 字节区间插入、tmp+fsync+rename 原子替换、id→path 全库扫描。
//
// 允许依赖：model、mdfile、git（S1 实际只用 model、mdfile）。
// 首次落地：S1（简化版：无锁、无事务暂存目录、无写前复核、无崩溃恢复，施工索引 §13）。
//
// 安全底线（§9，代码结构保证而非约定）：
//   - B1：导出的写形态**恰为** CreateFile / AppendToSection / WriteGuarded 三个，
//     自动路径根本调不到「替换已有块」「删除块」；替换 / 删除留给 S2 起的用户显式命令。
//   - B2：「用户补充」任何时候、任何路径都拒写；重新加工时「用户补充」「存疑与待验证」
//     原始字节逐字保留，无法安全保留则 SkipUserBlockUnsafe 跳过。
//   - B3：写前重新读盘并与 expectedHash 比对，不一致返回 SkipFileChanged；
//     不覆盖、不强写、不重试、不排队。
//
// 写路径硬约束（§16.3 / §16.4）：本包所有落盘字节都由 mdfile 的区间拼接产出，
// 禁止任何 YAML 序列化回写；用户内容只用 []byte 传递，不做 rune 迭代重建或
// strings 规范化。写入固定次序见 WriteGuarded 的文档注释。
//
// 显式不做（属 S5 / M6 目标态）：进程锁、事务暂存目录、写前复核、崩溃恢复、
// 块级合并；本包也**不产出、不定义** git add 的范围清单（口径在 internal/git）。
package store
