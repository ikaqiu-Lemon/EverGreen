// [S2] internal/proposal：提案控制面的落盘形态（目录 / ID / frontmatter 键 / 正文七分区）。
//
// 允许依赖：model、mdfile、store（**只为复用 guarded 写口**，见下 A-23 一节）。
// 首次落地：S2 · 里程碑 M3（T-evergreen.s1_main_flow-158614-033）。
// 依赖禁令：**不得**导入 ChangePlan 展开包（提案控制面与知识数据面不得互相借道）、
// 不得导入报告包与 Git 包（Git 与报告一律由 CLI 层负责）。
//
// # A-23 裁决（owner 项目维护者，2026-09-02；逐字引用 docs/specs/2026-10-13-m3-prestart-adjudication.md §7）
//
//	「仅限 proposals/** 提案控制面。必须复用 guarded store，禁止裸写文件。
//	 CLI 负责 Git 与报告。批准后的知识数据修改仍走 ChangePlan。
//	 Agent 可创建提案，但不可批准或执行。」
//
// 该裁决对本包的三条硬约束（提案合同 §8.5.1 的落地条款，只加严不放宽）：
//   - 本包**不裸写文件**：全包没有任何 os 写调用，落盘一律经 store 的提案 guarded 写口
//     （B1–B4 前置检查 + B3 content_hash 比对）。T-…-035 起本包持有提案控制面的
//     **写入编排**（superseded 三步动作、T1 批准落盘），但原子写原语仍在 store。
//   - 本包**不发 commit、不组报告**：两者都由 CLI 层发起。
//   - 本包**只碰 proposals/**：知识数据面的权威 Markdown 恒走 ChangePlan → guarded 写口，
//     批准后也不例外。
//
// # 本包的交付边界（按 task 累积）
//
// T-…-033：落盘形态与包骨架 —— 目录与 ID（id.go）、frontmatter 键集合与正文七分区
// （schema.go）、round-trip 逐字节保真。
// T-…-034：四态状态机（state.go）—— 合法边恰 5 / 非法边恰 12 / 终态 / 一致性判定。
// T-…-035：`superseded` 两个触发与影响面重算（superseded.go / impact.go）——
// 触发① 部分接受、触发② 执行前提变化、`decision.superseded_by` 必写判定、
// 影响面四项的直接扫描重算与可比对形态。
// **仍不实现**：`execution` 回写与失败报告（036）、任何 `eg proposal *` 子命令外壳（040）、
// 逻辑删除的十一步时序与知识数据落盘（041）、提案的部分应用（永不做）。
// 本包**不引入任何诊断码字面量**：不合规输入一律返回结构化错误（Violation），
// 由 T-…-037 统一映射到 M3 新增的 error / warning 编号。
//
// # 写路径口径（F6 / B1，与 M1 同源）
//
// YAML 库只用于**反序列化**（Unmarshal 一侧）：本包任何路径都不做 YAML 序列化输出，
// 连注释里也不写那两个被 make lint 的 guard 钉住的序列化 API 名。
// 提案模板由**字节拼装**产出（RenderTemplate），既有提案的改写沿用 mdfile 的
// Doc/Span 半开区间 + 字节插入，未知分区与未知 YAML 字段逐字保留。
package proposal
