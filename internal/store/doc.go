// [S1] internal/store：读盘、渲染、tmp+fsync+rename、逐文件 content_hash 比对。
//
// 允许依赖：model、mdfile、git。
// 首次落地：S1（简化版，施工索引 §13）。
// 写前自检（施工索引 §16.4）：content_hash 比对 → Parse→Render 字节自检，
// 不等即拒写（退化为 B3 的 SkipFileChanged 语义并进报告 skipped[]）。
// 本阶段为占位空包，业务实现见 T-evergreen.s1_main_flow-158614-006。
package store
