package cli

// 综述事实的**只读采样**（`reconcile.Input.Recaps` 的生产侧填充；M4 · T-…-055 阶段 3）。
//
// 为什么需要这一层：R6 的判定输入分两半 —— 知识卡那一半来自 M2 的全量扫描底座
// （`internal/query`），综述那一半是 S2 落盘的 `reviews/` 分区，而扫描底座的对象面里
// **没有**综述（见 `internal/query/context.go` 的说明：综述面尚未进扫描面）。
// `Input.Recaps` 因此需要一个生产侧入口，且合同把 **nil 语义定成「未采样即不判」** ——
// 所以采样失败时**绝不**退化成空切片交给检查器（那等于把「没采样」谎报成「没有综述」）。
//
// 本文件的边界（最小面，一格不多）：
//   - **只读**：只做 `ScanIDs` + `Read` + frontmatter 解析，不写盘、不提交、不修复；
//   - **不判定**：缺 `updated_at`、`source_cards` 为空、引用卡不在库……一律逐字原样带出，
//     命中与否是 `internal/reconcile` 的事（阶段 2 已定稿，本文件不复制它的任何判定）；
//   - **不注册命令**：对账 / 检查两个子命令本体分属 T-…-058 / T-…-059，本 task 零命令注册；
//   - **不解码正文**：`RecapFact` 里本来就没有正文那一格，「重算综述」在形态上不可表达
//     （合同 §9「不自动重算综述」）。
//
// 取值口径：四个键全部取 frontmatter 的**逐字标量文本**（`recapFM` 直读 YAML 节点值），
// 不做时区归一、不回填默认值、不代入默认理由 —— 判定的可复算性依赖「原值进、原值出」。

import (
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// recapStaleTrueText 是失准标记键的**真值文本**（落盘层写的就是这个 YAML 布尔真）。
const recapStaleTrueText = "true"

// 综述投影要读的另外两个 frontmatter 键名。
//
// 失准标记那两个键**刻意不在这里**：它们的字面量真源恒在 `internal/model`
// （`FMKeyStale` / `FMKeyStaleReason`，判据是「非测试代码里各恰 1 次」），
// 因此本文件按常量去 mapping 里取键，绝不写第二遍字符串 —— 这也是不用
// `yaml:"…"` 结构体标签的原因：标签只接受字面量，写上就多出一处真源。
const (
	recapKeyUpdatedAt   = "updated_at"
	recapKeySourceCards = proposal.KeySourceCards
)

// recapFM 是一篇综述 frontmatter 的**惰性**投影：顶层键 → 原始节点。
//
// 为什么解到 `yaml.Node` 而不是结构体：① 失准两键要按常量取（见上）；② 标量类型解析
// 一处失败就整篇解不出来，而「时刻缺失 / 不可比较」在 R6 里是**明确不命中**的一种情形，
// 必须原样走到检查器手里，不能在采样层被吞成解析错误。
type recapFM map[string]yaml.Node

// text 取某个顶层键的**逐字标量文本**（缺键、非标量一律空串：缺省即缺省，不回填默认值）。
func (m recapFM) text(key string) string {
	node, ok := m[key]
	if !ok || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

// list 取某个顶层键的标量序列（顺序 = 落盘序，不去重、不过滤、不排序：
// 「引用了哪些卡、以什么顺序写着」本身就是判定要复算的原始事实）。
func (m recapFM) list(key string) []string {
	node, ok := m[key]
	if !ok || node.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item == nil || item.Kind != yaml.ScalarNode {
			continue
		}
		out = append(out, item.Value)
	}
	return out
}

// RecapSample 是一次综述采样的全部事实（**只读**，可逐字复算）。
type RecapSample struct {
	// Recaps 是采到的综述事实，按 (path, id) 升序 —— 与检查侧的输出序同一口径。
	// **非 nil 才表示已采样**：零篇综述时它是长度为 0 的**非 nil** 切片。
	Recaps []reconcile.RecapFact `json:"recaps"`
	// Paths 是采样覆盖到的综述文件相对路径（升序），供 e2e 逐条复算采样面。
	Paths []string `json:"paths"`
	// Unparsed 是 frontmatter 无法解析、故未进 Recaps 的综述文件（升序）。
	// 如实登记而不静默丢弃：它们的形态问题由 `eg apply` 的校验链发码，本层不二次发码。
	Unparsed []string `json:"unparsed"`
}

// SampleRecaps 扫描 vault，采出全部主题综述（`r-` 前缀）的只读事实。
//
// 返回 error 时 Recaps 恒为 nil：调用方**必须**把它当作「未采样」传给检查器
// （nil = R6 整体不判），绝不可退化成空集合当成「库里没有综述」。
func SampleRecaps(vaultRoot string) (RecapSample, error) {
	root := strings.TrimSpace(vaultRoot)
	if root == "" {
		return RecapSample{}, &UsageError{Msg: "综述采样缺 vault 根路径"}
	}
	st := store.New(root)
	idx, err := st.ScanIDs()
	if err != nil {
		return RecapSample{}, err
	}
	ids := make([]string, 0, len(idx.ByID))
	for id := range idx.ByID {
		if isRecapID(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := RecapSample{Recaps: []reconcile.RecapFact{}}
	for _, id := range ids {
		rel := idx.ByID[id]
		f, rerr := st.Read(rel)
		if rerr != nil {
			return RecapSample{}, rerr
		}
		doc, perr := mdfile.Parse(f.Bytes)
		if perr != nil || !doc.HasFM {
			out.Unparsed = append(out.Unparsed, rel)
			continue
		}
		var fm recapFM
		if derr := doc.DecodeFM(&fm); derr != nil {
			out.Unparsed = append(out.Unparsed, rel)
			continue
		}
		out.Paths = append(out.Paths, rel)
		out.Recaps = append(out.Recaps, reconcile.RecapFact{
			// ID 取索引里的那个（与 `eg` 全库口径同源）；frontmatter 的 `id` 只做一致性佐证，
			// 两者不一致时以索引为准 —— 定位靠的是「哪份文件」，不是文件自报。
			ID: id, Path: rel, UpdatedAt: fm.text(recapKeyUpdatedAt),
			SourceCards:  fm.list(recapKeySourceCards),
			Marked:       strings.EqualFold(fm.text(model.FMKeyStale), recapStaleTrueText),
			MarkedReason: fm.text(model.FMKeyStaleReason),
		})
	}
	sort.Strings(out.Paths)
	sort.Strings(out.Unparsed)
	sort.SliceStable(out.Recaps, func(i, j int) bool {
		if out.Recaps[i].Path != out.Recaps[j].Path {
			return out.Recaps[i].Path < out.Recaps[j].Path
		}
		return out.Recaps[i].ID < out.Recaps[j].ID
	})
	return out, nil
}

// isRecapID 报告某个 ID 是否是主题综述 ID（前缀 `r-`，真源在 internal/model）。
func isRecapID(id string) bool {
	p, err := model.ParseID(id)
	return err == nil && p.Prefix == model.PrefixReview
}
