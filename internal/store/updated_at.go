package store

// internal/store/updated_at.go：**`updated_at` 刷新的唯一实现**（I-…-009 / 批次 C2）。
//
// # 判据（声明面，逐字）
//
// `2026-10-10-m3-user-authorization-contract.md` §2.1 写权限矩阵**第 8 行**：
//
//	updated_at | ✅ 由 CLI 在实际写入时更新 | ✅ 同左 | §5.5 …两条路径无差别
//
// 因此刷新的触发条件恰是「**这次真的把字节写进了盘**」，与发起方（Agent 自动路径 /
// 用户显式路径）无关，也与写的是正文、序列还是状态键无关。
//
// # 为什么必须集中在这一个文件里
//
// `eg unreviewed` 的判据是 `updated_at > reviewed_at`。刷新一旦按命令分散实现，
// 就会出现「改了内容但时间戳没动」的产物：它既不在未过目清单里，也没有任何 warning，
// 复核闭环**静默**失效（这正是 I-…-009 的现场）。集中成一个函数 + 两个接线点
// （`applyEdit` 的追加族、`mutateGuarded` 的改写族）后，「哪条写路径会刷新」
// 变成可 grep 的事实，而不是靠每个 setter 的作者记得。
//
// # 三条封闭规则（本层不越界）
//
//  1. **不读时钟**：时刻一律由调用方注入（`ExecOptions.Stamp` / `Root.now()`），
//     零值 = 调用方明确不要求刷新 → 原样返回。本包 import 不含 time，因此
//     「store 自己偷偷取现在」在结构上不可能。
//  2. **有则整行覆盖，无则不新增**：键存在 → `Raw[:a] + 新行 + Raw[b:]`（键顺序不重排、
//     其余字节逐字不动、恒保持恰一行）；键不存在 → **不发明键**（提案 `proposals/` 与
//     部分历史产物本就没有 `updated_at`，落盘层无权给它们长出新键）。追加族要求
//     「缺键时补齐」的场景由调用方显式给 `FMKeys` 承担，不在本文件放宽。
//  3. **豁免只有过目与失准两处**，且是**结构性**豁免而非约定：`SetReviewedAt` 与
//     `SetStale` 的签名里根本没有时刻可传（前者的 `at` 是 `reviewed_at` 的值本身）。
//     判据：§5.5 EG-CFM-06「过目不是对内容的修改」——若过目也刷新 `updated_at`，
//     刚标记完的产物会立刻又变成未过目；对账合同 §9「不自动重算 / 不自动清除 stale」
//     同理要求 R6 标记不动内容时间戳。
//
// 字节机制复用 state_write.go 的 `setFMScalarKey` 与 content.go 的 `fmLine`：**不做任何
// YAML 序列化回写**（make lint 的 guard 步会 FAIL），全链路 []byte。

import (
	"bytes"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// fmKeyUpdatedAt 是内容时间戳的 frontmatter 键名（字面量只此一处）。
const fmKeyUpdatedAt = "updated_at"

// refreshIfChanged 是刷新的**唯一判据入口**：只有「候选字节确实与盘上字节不同」时才刷新。
//
// 为什么要这一格（I-…-009 收口）：矩阵第 8 行说的是「**实际写入**时更新」。若一次写路径
// 走完之后候选字节与原字节逐字相同（例如 replace_block 把当前有效块**写回同样的内容**、
// remove_relation 幂等 no-op），那就没有任何内容被改动 —— 此时刷新时间戳会造出与
// I-…-009 相反的假阳性：卡片内容一字未变，却因为 `updated_at > reviewed_at` 重新进入
// 未过目清单，等于让复核闭环开始报假警。故零 delta = 不刷新。
func refreshIfChanged(raw, cur []byte, stamp model.Stamp) ([]byte, error) {
	if bytes.Equal(raw, cur) {
		return cur, nil
	}
	return refreshUpdatedAt(cur, stamp)
}

// refreshUpdatedAt 在候选字节 cur 上把 `updated_at` 刷新为 stamp，返回新的字节。
//
// 语义（对应文件头规则 1 / 2）：
//   - stamp 为零值 → 原样返回 cur（调用方未要求刷新，本层绝不代入「现在」）；
//   - 键存在 → 整行覆盖（值为**单引号**标量，与建卡时 content.go 的 fmLine 逐字同风格）；
//   - 键不存在 → 原样返回 cur（不新增键，键集合不变）；
//   - 键重复 / 键不是单行标量 → 沿用 setFMScalarKey 的拒写语义（改哪个都可能错，
//     宁可让整次写入失败，也不猜）。
func refreshUpdatedAt(cur []byte, stamp model.Stamp) ([]byte, error) {
	if stamp.IsZero() {
		return cur, nil
	}
	doc, err := mdfile.Parse(cur)
	if err != nil {
		return nil, err
	}
	if _, _, found, err := fmScalarKeySpan(doc, fmKeyUpdatedAt); err != nil {
		return nil, err
	} else if !found {
		return cur, nil
	}
	// 引号风格与**建卡时**逐字一致（content.go 的 fmLine → quoted：单引号标量），
	// 这样「新建的 updated_at」与「刷新过的 updated_at」在盘上长得一模一样，
	// Git diff 只有值在变、不掺杂引号风格的噪声。
	line, err := fmLine(fmKeyUpdatedAt, stamp.String())
	if err != nil {
		return nil, err
	}
	return setFMScalarKey(doc, fmKeyUpdatedAt, line, true)
}

// withUpdatedAt 把「刷新 updated_at」接在 build 之后，落成**同一次** mutateGuarded：
// 因此不存在「内容写进去了、时间戳没写进去」的半截形态，也不多出一次守卫写。
//
// build 返回 *SkipError（B3 冲突 / B2 用户分区不安全 / 块级合并不安全）时原样透传：
// 被跳过的写**零写入**，自然也不刷新时间戳（「刷新只跟随实际写入」的负半边）。
func withUpdatedAt(stamp model.Stamp,
	build func(f File, doc *mdfile.Doc) ([]byte, error)) func(File, *mdfile.Doc) ([]byte, error) {
	return func(f File, doc *mdfile.Doc) ([]byte, error) {
		cur, err := build(f, doc)
		if err != nil {
			return nil, err
		}
		return refreshIfChanged(f.Bytes, cur, stamp)
	}
}
