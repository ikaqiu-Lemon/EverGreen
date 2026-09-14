package mdfile

// M3 的两处**非追加**区间写：块替换（replace_block）与序列条目移除（remove_relation）。
//
// 阶段边界（S1/M1 只追加，M3/S2 才有这两条）：本文件是 append.go 之外唯一的写路径，
// 机制**仍然只有一条**——`d.Raw[:a]` + 载荷 + `d.Raw[b:]` 的区间拼接，
// 绝不做「结构体 → 文本」的回写，YAML 库在本包只用于只读解析。
//
// 两条硬约束（与 section.go 的 `Unprocessed.Cut` 同源口径）：
//   - **只动被点名的区间**：区间之外的一切字节（frontmatter 其它键、未知字段、注释、
//     空行、缩进风格、其它分区、其它块）逐字不动；
//   - **改完立刻自检**：Parse→Render 字节自检 + 结构复算（块数 / 条目数 / 目标块内容），
//     任一不过即返回错误、拒绝把候选字节交给上层落盘。
//
// 授权边界不在本包：谁有权发起替换 / 移除由 internal/plan 的校验链判定
// （`replace_block` 只动「当前有效自检问题块」，历史记录块只追加、永不改写）。

import (
	"bytes"
	"errors"
	"fmt"
)

var (
	// ErrBlockIndexOutOfRange 块序号越界（分区里没有那么多块）。
	ErrBlockIndexOutOfRange = errors.New("块序号越界")
	// ErrSectionReplaceUnsafe 分区正文替换结果未通过自检，拒绝返回候选字节。
	ErrSectionReplaceUnsafe = errors.New("分区正文替换结果未通过自检，拒绝写入")
	// ErrReplaceUnsafe 块替换结果未通过自检，拒绝返回候选字节。
	ErrReplaceUnsafe = errors.New("块替换结果未通过自检，拒绝写入")
	// ErrSeqCutUnsafe 序列条目移除结果未通过自检，拒绝返回候选字节。
	ErrSeqCutUnsafe = errors.New("序列条目移除结果未通过自检，拒绝写入")
	// ErrSeqItemIndexOutOfRange 序列条目序号越界。
	ErrSeqItemIndexOutOfRange = errors.New("序列条目序号越界")
)

// SeqItem 是块状序列里**一条**条目的半开区间（含其缩进续行）。
type SeqItem struct {
	Index int
	Start int
	End   int
}

// SeqLayout 是块状序列的逐条目布局：键行 + 每条条目的区间。
//
// 与 SeqSpan（append.go，只给「插入点 + 缩进风格」）互补：本结构给出**逐条目**区间，
// 只有移除路径需要它。
type SeqLayout struct {
	Key string
	// KeyLine 是 `key:` 行的起始偏移（移除**全部**条目时连键行一并剪掉，不留空序列墓碑）。
	KeyLine int
	Items   []SeqItem
	// End 是末条条目之后的偏移。
	End int
}

// FMSeqLayout 定位 frontmatter 顶层键 key 之下块状序列的**逐条目**区间。
//
// 条目边界：一条条目从它的 `- ` 行开始，含其后所有「缩进但不是 `- `」的续行。
// 序列不存在 → ErrSeqKeyNotFound；键在但没有条目 → ErrNoIndentStyle（与 FMSeq 同口径）。
func (d *Doc) FMSeqLayout(key string) (SeqLayout, error) {
	if !d.HasFM {
		return SeqLayout{}, ErrNoFrontmatter
	}
	prefix := append([]byte(key), ':')
	for cur := d.FMStart; cur < d.FMEnd; {
		end := clampLine(d.Raw, cur, d.FMEnd)
		if !bytes.HasPrefix(d.Raw[cur:end], prefix) {
			cur = end
			continue
		}
		out := SeqLayout{Key: key, KeyLine: cur, End: end}
		at := end
		for at < d.FMEnd {
			lend := clampLine(d.Raw, at, d.FMEnd)
			line := d.Raw[at:lend]
			if isBlank(line) || !isIndented(line) {
				break
			}
			if item, _ := seqItemIndent(line); item {
				out.Items = append(out.Items, SeqItem{Index: len(out.Items), Start: at, End: lend})
			} else if n := len(out.Items); n > 0 {
				out.Items[n-1].End = lend // 缩进续行归属上一条条目
			}
			out.End = lend
			at = lend
		}
		if len(out.Items) == 0 {
			return out, ErrNoIndentStyle
		}
		return out, nil
	}
	return SeqLayout{}, fmt.Errorf("%w：%s", ErrSeqKeyNotFound, key)
}

// CutFMSeqItems 物理移除 frontmatter 块状序列里指定序号的条目，返回**新的**字节切片。
//
// 口径（提案合同 §8.5.2 A-24 口径 1 / 3）：
//   - 物理移除，**不留墓碑**：被移除条目的字节整段消失，不写任何「已移除」标记位；
//   - 移除**全部**条目时连 `key:` 行一并剪掉——留一个空序列既是墓碑，
//     又会让后续追加失去缩进风格（ErrNoIndentStyle），因此不留；
//   - 条目之外的字节逐字不动；剪除后立刻 Parse→Render 自检 + 条目数复算，不过即拒绝。
func (d *Doc) CutFMSeqItems(key string, indices []int) ([]byte, error) {
	layout, err := d.FMSeqLayout(key)
	if err != nil {
		return nil, err
	}
	if len(indices) == 0 {
		return nil, fmt.Errorf("%w：未指定要移除的条目", ErrSeqItemIndexOutOfRange)
	}
	drop := map[int]bool{}
	for _, i := range indices {
		if i < 0 || i >= len(layout.Items) {
			return nil, fmt.Errorf("%w：%d（共 %d 条）", ErrSeqItemIndexOutOfRange, i, len(layout.Items))
		}
		drop[i] = true
	}
	var out []byte
	if len(drop) == len(layout.Items) {
		out = append(out, d.Raw[:layout.KeyLine]...)
		out = append(out, d.Raw[layout.End:]...)
	} else {
		at := 0
		for _, item := range layout.Items {
			if !drop[item.Index] {
				continue
			}
			out = append(out, d.Raw[at:item.Start]...)
			at = item.End
		}
		out = append(out, d.Raw[at:]...)
	}
	if err := SelfCheck(out); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrSeqCutUnsafe, err)
	}
	after, err := Parse(out)
	if err != nil {
		return nil, fmt.Errorf("%w：%v", ErrSeqCutUnsafe, err)
	}
	want := len(layout.Items) - len(drop)
	got := 0
	if want > 0 {
		post, err := after.FMSeqLayout(key)
		if err != nil {
			return nil, fmt.Errorf("%w：移除后序列不可定位：%v", ErrSeqCutUnsafe, err)
		}
		got = len(post.Items)
	} else if _, err := after.FMSeqLayout(key); !errors.Is(err, ErrSeqKeyNotFound) {
		return nil, fmt.Errorf("%w：移除全部条目后 %s 键应一并消失（不留空序列墓碑）", ErrSeqCutUnsafe, key)
	}
	if got != want {
		return nil, fmt.Errorf("%w：条目数 %d → %d（期望 %d）",
			ErrSeqCutUnsafe, len(layout.Items), got, want)
	}
	return out, nil
}

// ReplaceBlock 用 payload 逐字替换某分区里第 index 个块，返回**新的**字节切片。
//
// 只替换那一个块的区间：同分区其它块（「理解自检」的历史记录块）与其它分区逐字不动。
// payload 必须以 \n 结束（本包绝不替调用方补字节）；「用户补充」等永不可写分区直接拒绝。
// 替换后立刻 Parse→Render 自检 + 复算「块数不变、目标块内容等于 payload」，不过即拒绝。
func (d *Doc) ReplaceBlock(section string, index int, payload []byte) ([]byte, error) {
	for _, never := range NeverWriteSections() {
		if section == never {
			return nil, fmt.Errorf("%w：%s", ErrSectionNeverWrite, section)
		}
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		return nil, ErrPayloadNotLineTerminated
	}
	blocks, err := d.Blocks(section)
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(blocks) {
		return nil, fmt.Errorf("%w：%d（「%s」共 %d 块）", ErrBlockIndexOutOfRange,
			index, section, len(blocks))
	}
	target := blocks[index]
	out := make([]byte, 0, len(d.Raw)-(target.End-target.Start)+len(payload))
	out = append(out, d.Raw[:target.Start]...)
	out = append(out, payload...)
	out = append(out, d.Raw[target.End:]...)

	if err := SelfCheck(out); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrReplaceUnsafe, err)
	}
	after, err := Parse(out)
	if err != nil {
		return nil, fmt.Errorf("%w：%v", ErrReplaceUnsafe, err)
	}
	post, err := after.Blocks(section)
	if err != nil {
		return nil, fmt.Errorf("%w：%v", ErrReplaceUnsafe, err)
	}
	if len(post) != len(blocks) {
		return nil, fmt.Errorf("%w：块数 %d → %d（替换不得增删块）",
			ErrReplaceUnsafe, len(blocks), len(post))
	}
	if !bytes.Equal(NormalizeBlock(post[index].Bytes(out)), NormalizeBlock(payload)) {
		return nil, fmt.Errorf("%w：第 %d 块替换后内容与载荷不一致", ErrReplaceUnsafe, index)
	}
	return out, nil
}

// ReplaceSectionBody 用 payload 逐字替换某个 H2 分区的**整段正文**，返回**新的**字节切片。
//
// 阶段与授权边界（T-…-045 / 授权合同 §5 B1 那一行）：分区级替换是**用户显式路径**才有的
// 能力（「替换与删除只在用户显式发起的命令里存在（S2 起）」）。本包不判断谁在调用——
// 授权判定唯一在 internal/plan（editSectionGate + 写权限矩阵 #12 / #15）；本函数只保证
// 「换的是这一段、别处一个字节不动」。
//
// 硬约束（与 ReplaceBlock 同源）：
//   - 只覆盖 `Body..End` 这一段半开区间：分区标题行、其它分区、frontmatter、未知分区
//     与文件尾部字节逐字不动；
//   - 「用户补充」等永不可写分区直接拒绝（B2 / U-03，任何路径任何时候）；
//   - payload 必须以 \n 结束（本包绝不替调用方补字节）；
//   - 替换后立刻 Parse→Render 自检 + 复算「分区数不变、名字序不变、目标分区正文等于
//     payload」，任一不过即拒绝把候选字节交给上层落盘。
func (d *Doc) ReplaceSectionBody(section string, payload []byte) ([]byte, error) {
	for _, never := range NeverWriteSections() {
		if section == never {
			return nil, fmt.Errorf("%w：%s", ErrSectionNeverWrite, section)
		}
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		return nil, ErrPayloadNotLineTerminated
	}
	span, ok := d.Section(section)
	if !ok {
		return nil, fmt.Errorf("%w：%s", ErrSectionNotFound, section)
	}
	before := d.SectionNames()
	out := make([]byte, 0, len(d.Raw)-(span.End-span.Body)+len(payload))
	out = append(out, d.Raw[:span.Body]...)
	out = append(out, payload...)
	out = append(out, d.Raw[span.End:]...)

	if err := SelfCheck(out); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrSectionReplaceUnsafe, err)
	}
	after, err := Parse(out)
	if err != nil {
		return nil, fmt.Errorf("%w：%v", ErrSectionReplaceUnsafe, err)
	}
	got := after.SectionNames()
	if len(got) != len(before) {
		return nil, fmt.Errorf("%w：分区数 %d → %d（替换正文不得增删分区）",
			ErrSectionReplaceUnsafe, len(before), len(got))
	}
	for i := range before {
		if before[i] != got[i] {
			return nil, fmt.Errorf("%w：分区序列由 %v 变成 %v（替换正文不得重排分区）",
				ErrSectionReplaceUnsafe, before, got)
		}
	}
	post, ok := after.Section(section)
	if !ok {
		return nil, fmt.Errorf("%w：替换后「%s」不可定位", ErrSectionReplaceUnsafe, section)
	}
	if !bytes.Equal(out[post.Body:post.End], payload) {
		return nil, fmt.Errorf("%w：「%s」替换后正文与载荷不一致", ErrSectionReplaceUnsafe, section)
	}
	return out, nil
}
