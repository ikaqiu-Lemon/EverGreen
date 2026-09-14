package report

// support_check[]：**删除后的支持面建议清单**（合同 §9 三条判定行的代码化）。
//
// 三条反直觉规则是本文件存在的全部理由：
//  1. 删除执行后**没有任何知识卡的状态被自动改变**——本文件是纯函数，不写盘、不调
//     任何 setter，结构上不可能改状态；
//  2. 失去有效 support 的卡**只**得到「建议标记 `deprecated`」提示，用户不处理时它们
//     **仍是 active**（entry.Status 如实回报当前状态，正是「没被改」的证据）；
//  3. 仍保有有效 support 的卡得到「建议重新检查材料关系」提示。
//
// 边界（不得越线）：
//   - 这里产出的是**建议清单**，与 S3 那个「综述材料是否足够」的**标记**是两件不同的事（标记属 S3，
//     本文件不产出标记）；综述过期的自动判定同样属 S3，本文件不涉及。
//   - report 是叶子包：不 import internal/store / internal/query，vault 事实由调用方
//     经 SupportVault 这个**只读**投影口喂进来（读侧接口没有任何写方法）。

import (
	"errors"
	"sort"
)

// errNilVault 没有 vault 投影就无从判定支持面：fail fast，不返回空清单假装「无事发生」。
var errNilVault = errors.New("support_check 需要 vault 只读投影（SupportVault 为 nil）")

// NoAutoStatusChangeNotice 是报告必须能逐字输出的说明（一字不差，供 CLI 消费）。
const NoAutoStatusChangeNotice = "本次删除没有自动改变任何知识卡的状态"

// 两条建议文案的**逐字**字面量（`jq '.data.support_check[]?.recommendation'` 直接命中）。
const (
	// RecommendDeprecateMark 给「失去有效 support」的卡：只是建议，系统绝不自动改状态。
	RecommendDeprecateMark = "建议标记 `deprecated`"
	// RecommendRecheckMaterials 给「仍有有效 support」的卡：材料面变了，值得复核。
	RecommendRecheckMaterials = "建议重新检查材料关系"
)

// SupportRef 是一条 support 材料关系的两个端点（source / note 的 ID，不是路径）。
type SupportRef struct {
	Source string
	Note   string
}

// SupportCard 是一张知识卡在支持面上的只读投影：id + **当前**状态 + support 端点。
//
// 只投影 rel=support 的条目：against / context 不是「支持」，不参与本判定（§6：
// 证据类统计不含 context）。筛选由调用方在喂数据时完成，本层不猜。
type SupportCard struct {
	ID       string
	Status   string
	Supports []SupportRef
}

// SupportVault 是 vault 的**只读**投影口。故意只有一个读方法：本模块拿不到任何写能力。
type SupportVault interface {
	SupportCards() ([]SupportCard, error)
}

// SupportCheckEntry 是 `support_check[]` 的一条建议。
//
// 字段名 `recommendation` 是**对外合同**（验收用 jq 直读该键，不得改名）；
// `status` 如实回报卡当前状态（建议未被执行时它仍是 active），不是「将要变成」的状态。
type SupportCheckEntry struct {
	ID               string   `json:"id"`
	Status           string   `json:"status"`
	LostSupport      []string `json:"lost_support"`
	RemainingSupport int      `json:"remaining_support"`
	Recommendation   string   `json:"recommendation"`
	Detail           string   `json:"detail"`
}

// BuildSupportCheck 由「被删目标集合 + vault 只读投影」产出 support_check[]。
//
// 有效性判据只有一条：一条 support 的**两个端点**（source / note）都不在被删集合里，
// 这条 support 才仍然有效——关系记录本身一条都没被删（U-01），过滤纯靠端点有效性。
//
// 只为**受影响**的卡产出条目（至少有一条 support 的端点被删）：没被影响的卡不该出现在
// 建议清单里，否则清单会淹没真正需要看的内容。卡自己被删则不产条目（它不是「受影响方」）。
// 全程零写盘、零状态变更：返回值是建议，执行与否由用户决定。
func BuildSupportCheck(deleted []string, vault SupportVault) ([]SupportCheckEntry, error) {
	if vault == nil {
		return nil, errNilVault
	}
	gone := make(map[string]bool, len(deleted))
	for _, id := range deleted {
		if id == "" {
			continue
		}
		gone[id] = true
	}
	cards, err := vault.SupportCards()
	if err != nil {
		return nil, err
	}
	out := make([]SupportCheckEntry, 0, len(cards))
	for _, c := range cards {
		if c.ID == "" || gone[c.ID] {
			continue
		}
		lost := make([]string, 0, len(c.Supports))
		remaining := 0
		for _, ref := range c.Supports {
			switch {
			case gone[ref.Source]:
				lost = append(lost, ref.Source)
			case gone[ref.Note]:
				lost = append(lost, ref.Note)
			default:
				remaining++
			}
		}
		if len(lost) == 0 {
			continue
		}
		out = append(out, SupportCheckEntry{
			ID:               c.ID,
			Status:           c.Status,
			LostSupport:      lost,
			RemainingSupport: remaining,
			Recommendation:   recommendationFor(remaining),
			Detail:           detailFor(remaining),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// recommendationFor 把「剩余有效 support 数」映射到两条封闭建议之一。
func recommendationFor(remaining int) string {
	if remaining == 0 {
		return RecommendDeprecateMark
	}
	return RecommendRecheckMaterials
}

// detailFor 复述「系统不自动改状态」这一口径：用户不处理时卡仍是 active。
func detailFor(remaining int) string {
	if remaining == 0 {
		return "该卡已失去全部有效 support；" + NoAutoStatusChangeNotice +
			"，不处理时它仍是 active，是否失效由用户决定"
	}
	return "该卡仍保有有效 support，但部分材料端点已被删除；" + NoAutoStatusChangeNotice
}

// SetSupportCheck 把建议清单挂到报告体的 `support_check` 键上。
//
// 报告只搬运，不在这里重新判定：清单由 BuildSupportCheck **唯一**产出。
// nil 与空清单区分开：nil 表示本次没做支持面检查（键为 null），空清单表示做了但无受影响卡。
func (r *Report) SetSupportCheck(entries []SupportCheckEntry) {
	if entries == nil {
		r.SupportCheck = nil
		return
	}
	r.SupportCheck = entries
}

// SupportCheckLines 渲染人类可读形态：事实与 JSON 同源，不引入 JSON 里没有的事实。
func SupportCheckLines(entries []SupportCheckEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries)+1)
	out = append(out, NoAutoStatusChangeNotice)
	for _, e := range entries {
		out = append(out, "支持面检查："+e.ID+"（status="+e.Status+"）→ "+e.Recommendation)
	}
	return out
}
