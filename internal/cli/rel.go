package cli

// `eg rel` 的命令定义与业务实现：读路径（M2 查询合同 §3；T-…-023）+ `rel add` 写路径
// （同合同 §4；T-…-024，实现在 rel_add.go）+ `rel remove` 写路径（M3 接管 M2 占位，
// 实现在 rel_remove.go；T-…-044）。
//
// 焦点端点跨类型（k- 知识卡 / o- 观点）：正向读**焦点端点**自身 frontmatter 的 `relations[]`，
// 反向靠 **Markdown 全库扫描**反查「谁指向了我」（来源既可能是卡也可能是观点），
// 悬空引用如实报 Q2；`opposing` 单向存储，读路径不补对称条目。
//
// **M1 期口径的变更说明（同 commit 生效，不留自相矛盾的禁令）**：M1 的文件头曾写死
// 「本文件不得引用查询包：门禁对 `internal/cli/rel*` 做 import 反证（P1-2）」——
// 那是「rel 读路径 M1 不实现」的占位守卫。T-…-023 落地读路径后该禁令必然失效，
// 因此改写为 M2 口径：本文件（`rel` 读路径）**不得引用写入 API**
// （`internal/store` / `internal/plan` / `internal/git` 与 `Apply*` / `Write*` / `Commit`）。
// T-…-024 落地 `rel add` 后禁令再收紧一次并**按文件分工**：写路径文件 `rel_add.go`
// 只允许经 `internal/plan` 走 ChangePlan 链路，`internal/store` 的写 API 一律不得直呼——
// 对应反证由 cli_test.go 的 P1-2 用例按新语义承接。
//
// 子命令边界：
//   - `rel add`：本文件只做参数形态校验 + 分发，业务在 rel_add.go（verb = relate，一次 commit）；
//   - `rel remove`：M2 期的阶段占位（退 1 + 固定文案 + 零写入零 commit）**已由 T-…-044 接管**，
//     本文件同样只做参数形态校验 + 分发，业务在 rel_remove.go（verb = relate，一次 commit）。
//     接管是义务而非选择：阶段占位一旦有真实实现就必须整体消失，否则库里同时存在
//     「未实现」宣告与真实写入，Agent 无从判断该不该调它。

import (
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// relSubcommands 是 rel 的两个写子命令（add / remove 自 M3 起均为真实实现）。
var relSubcommands = []string{"add", "remove"}

func relCommand() *Command {
	return &Command{
		Name:    "rel",
		Display: "rel",
		Summary: "论证关系：读 relations[] 与反向视图 / add 写入（verb=relate）",
		Owner:   "T-evergreen.s1_main_flow-158614-024",
		Subs:    relSubcommands,
		Usage: `eg rel <endpoint-id> [--to <id>] [--include-deprecated] [--replaced-by] [--limit <n>] [--offset <n>] [--json]   # 读：正向 relations[] + 反向全库扫描
eg rel add <from> <type> <to> --reason <text> [--domain <d>] [--strict] [--json]   # 写（已实现，verb = relate）
eg rel remove <from> <type> <to> --reason <text> [--strict] [--json]   # 写（已实现，verb = relate）

参数（M2 查询合同 §3.1 读 / §4.1 写）：
  <endpoint-id>     读路径：焦点端点 ID（知识卡 k- / 观点 o-）；端点不存在 → 退 1
  --to <id>         否；只保留对端 == 该 ID 的条目（正反向同时过滤）；ID 不存在 → 结果为空 + 一条 Q2
  --include-deprecated  否；默认隐藏对端 deprecated 的关系条目，加此 flag 才展示（仍带 [失效] 标记，
                    见 owner 裁决 A-38/A-39，归 M4 规划）；只放开 deprecated 维度，不影响已删除维度；与 --to 正交
  --replaced-by     否；把视图从**论证关系**切到**替代指针**：正向 = 谁取代了本卡（至多一条）、
                    反向 = 本卡取代了谁（0..N 条）；条目仍恰五键、type 逐字 replaced_by；
                    与 --to / --include-deprecated 正交（M5 合同 §8.4）
  --limit <n>       否，默认 50；**本次最多返回的关系条数**（0 = 不限量）
  --offset <n>      否，默认 0；跳过的关系条数；超出总数返回空列表且仍退 0；
                    两者为负数 / 非整数 → 参数错，退 1、零写入（M5 合同 §8.2 经 I-…-008 改判，原写 4）
  <from> <to>       写路径：端点 ID（知识卡 k- / 观点 o-，from/target 四组合 k→k/k→o/o→k/o→o）；
                    不可解析 / 不存在 → E2，target 写成 s-… 等非 k/o 端点 → E3（退 2，零写入）
  <type>            derives | supports | limits | opposing（冻结合同 F4）；集合外退 1
  --reason <text>   rel add / rel remove 必填（缺该 flag 退 1；给了空串或等于关系名本身 → W2 照写不拦截）
  --domain <d>      否；只用于 plan 的 domain；未给则取 from 端点所在领域，再回落 default_domain
  --strict          否；仅 rel add / rel remove 写路径生效；M6 写前强校验升级面命中即锁内零写入中止退 5（携 E15）

读（§3.1）data 键序固定：id / relations_out / relations_in / scanned_files / skipped_files；
条目键恰 from / type / target / reason / path 五项，正反向同构。
正向 = 焦点端点 frontmatter relations[]（from 恒为焦点端点）；反向 = 全库 Markdown 扫描（含卡与观点）。
排序（§3.3 + M5 §7.4 四级全序）：type 固定次序 opposing → limits → supports → derives →
对端 ID 升序 → path 升序 → 条目输出全等标识（from|type|target|reason）升序；两个后端可复算。
分页（M5 合同 §8.2，S4 起）：--limit 是**一个全局上限**——正反向两个列表先合并成一条确定序列
（relations_out 段在前、relations_in 段在后）再全局取 [offset, offset+limit)，因此一次读最多返回
limit 条条目，**不会**因为有两个列表而给到 2×limit 条；截断只判一次并产恰一条 W25。
opposing 单向存储：库中只有一条记录，读路径不补对称条目、不去重合并。
悬空引用仍照实展示并标注「目标不存在」，同时记一条 Q2 warning，退出码仍 0。
读路径只读：零文件变化、零 commit。

写（§4）：参数在内存里合成一份 ChangePlan（verb=relate + 单条 add_relation / remove_relation op），
经 plan 校验与 executor 展开落到 store 写口，一次 rel add / rel remove = 一次 relate commit；
opposing 按两端 ID 字典序单向存储（方向被规范化 / 同对已存在均记 W8，不产生第二条）。
rel remove 按 owner 裁决 A-24 **物理移除** relations[] 里匹配 (from,type,target) 的全部记录，
不留墓碑、不新建标识；未命中任何既有关系 → W10 幂等 no-op（零写入、零 commit、退 0）。
Agent 自动路径（无用户显式命令）删关系被矩阵 #11 拦下：退 2、零写入。
自环（from == target）是 plan 级静态语义约束：与 E2/E3/E4 同阶段拦下 → E5、退 2、零写入零 commit
（I-…-010 修订；此前落在写入层被折成 3 / partial，而事实是零写入，会诱导调用方做不存在的补偿）。
退出码：0（含「无实际改动、未提交」）| 1 参数非法 / 未配置领域 |
        2 校验失败（零写入，含自环）| 3 B3 跳过（已写部分保留并提交）| 4 commit 失败（不回滚，B4）|
        5 写前强校验失败（E15）/ run.lock 不可用（E16），两者均零写入（add / remove 才取锁）
`,
		Flags: func(fs *flagSet) {
			fs.String("reason", "", "关系理由（rel add / rel remove 必填）")
			fs.String("to", "", "只保留对端 == 该 ID 的条目（读路径）")
			fs.String("domain", "", "plan.domain（rel add 可选）")
			// --include-deprecated（T-…-061，owner 裁决②）：读路径 flag，布尔，默认 false，
			// 无短选项无别名。作用面恰 eg rel / eg card show 两命令；其余命令未注册该 flag，
			// 传入即被 FlagSet 判为「未定义 flag」→ 退 1、零写入（G7）。与 --to 正交。
			fs.Bool("include-deprecated", false, "展示对端 deprecated 的关系条目（默认隐藏；不影响已删除维度）")
			// --replaced-by / 分页（S4 · T-…-068）：只作用于读路径；写子命令传入即被
			// 各自的 Validate 拦下（rel add / rel remove 的参数面一格不变）。
			fs.Bool(query.ReplacedByFlag, false, "查替代指针的正反双向（谁取代了本卡 / 本卡取代了谁）")
			// --strict（M6 · T-…-074）：只对 rel add / rel remove 两条写子命令的写前强校验
			// 生效；读路径不进事务、Precheck 恒空返回，故设了也无副作用。
			registerStrictFlag(fs)
			pageFlags(fs)
		},
		Validate: func(inv *Invocation) error {
			switch inv.Sub {
			case "add":
				if err := rejectReadPathFlags(inv, "rel add"); err != nil {
					return err
				}
				return validateRelAddArgs(inv)
			case "remove":
				if err := rejectReadPathFlags(inv, "rel remove"); err != nil {
					return err
				}
				return validateRelRemoveArgs(inv)
			}
			if len(inv.Args) != 1 {
				return &UsageError{Msg: fmt.Sprintf(
					"eg rel 需要恰一个位置参数 <endpoint-id>（知识卡 k- / 观点 o-），实际 %d 个：%v", len(inv.Args), inv.Args)}
			}
			return nil
		},
	}
}

// runRel 分发 rel 的三种形态：读路径 / add 写入 / remove 写入。
func (r *Root) runRel(inv *Invocation) (*Result, error) {
	switch inv.Sub {
	case "add":
		return r.runRelAdd(inv)
	case "remove":
		return r.runRelRemove(inv)
	}
	return r.runRelQuery(inv)
}

// runRelQuery 实现 eg rel <endpoint-id> 的读路径（只读，零副作用）。
func (r *Root) runRelQuery(inv *Invocation) (*Result, error) {
	page, err := pageSpecFrom(inv)
	if err != nil {
		return nil, err
	}
	res, err := query.RelView(inv.VaultRoot, query.RelRequest{
		ID: model.RelationEndpoint(inv.Args[0]), To: inv.String("to"),
		IncludeDeprecated: inv.String("include-deprecated") == "true",
		// S4：注入 A-44 水位线口径（B3 content_hash + Git HEAD）。
		Index: readIndexDeps(inv.VaultRoot),
		// S4 · T-…-068：分页（全局 limit/offset）与替代指针视图。
		Page:       page,
		ReplacedBy: inv.String(query.ReplacedByFlag) == "true",
	})
	if err != nil {
		if errors.Is(err, query.ErrInvalidEndpoint) || errors.Is(err, query.ErrEndpointNotFound) {
			return nil, &UsageError{Msg: err.Error()}
		}
		return nil, err
	}

	d := res.Data
	out := &Result{
		Data: map[string]interface{}{
			"id": d.ID, "relations_out": d.RelationsOut, "relations_in": d.RelationsIn,
			"scanned_files": d.ScannedFiles, "skipped_files": d.SkippedFiles,
		},
		DataOrder: query.RelDataKeys(),
	}
	out.Summary = append(out.Summary, relSummaryLines(res)...)
	out.Summary = append(out.Summary, pageSummaryLines(res.Page, "关系条目")...)
	out.Warnings = append(out.Warnings, queryDiagnostics(res.Diagnostics)...)
	return out, nil
}

// relSummaryLines 渲染人类可读关系视图。与 --json **同源同事实**：吃的是同一份 res.Data，
// 每一行都能在 data 里逐字对上（条目数、类型次序、对端 ID、理由、来源路径）。
func relSummaryLines(res *query.RelResult) []string {
	d := res.Data
	lines := []string{fmt.Sprintf(
		"rel：%s 的正向 %d 条 / 反向 %d 条（扫描 %d 个 .md，跳过 %d 个；只读，零写入零 commit）",
		d.ID, len(d.RelationsOut), len(d.RelationsIn), d.ScannedFiles, d.SkippedFiles)}
	missing := map[string]bool{}
	for _, id := range res.MissingTargets {
		missing[id] = true
	}
	deprecated := map[string]bool{}
	for _, id := range res.DeprecatedPeers {
		deprecated[id] = true
	}
	if len(d.RelationsOut) == 0 {
		lines = append(lines, "正向关系 relations_out[]：无")
	}
	for _, e := range d.RelationsOut {
		note := ""
		if missing[e.Target] {
			note = "（目标不存在）"
		}
		lines = append(lines, fmt.Sprintf("正向关系：%s --%s--> %s%s%s  理由：%s",
			e.From, e.Type, e.Target, peerDeprecatedMark(deprecated, e.Target), note, e.Reason))
	}
	if len(d.RelationsIn) == 0 {
		lines = append(lines, "反向关系 relations_in[]：无")
	}
	for _, e := range d.RelationsIn {
		lines = append(lines, fmt.Sprintf("反向关系：%s%s --%s--> %s  理由：%s（来源：%s）",
			e.From, peerDeprecatedMark(deprecated, e.From), e.Type, e.Target, e.Reason, e.Path))
	}
	return lines
}

// peerDeprecatedMark 返回该对端的 [失效] 标记（对端 deprecated 且已显式放开展示时非空）。
// 标记文案与 M2 §2.2 一致，逐字复用 query.MarkerDeprecated，本 task 不改文案与顺序规则。
func peerDeprecatedMark(deprecated map[string]bool, peer string) string {
	if deprecated[peer] {
		return query.MarkerDeprecated
	}
	return ""
}

// rejectReadOnlyVisibilityFlag 拒绝把只读可见性 flag `--include-deprecated` 传给**写子命令**
// （rel add / rel remove）。该 flag 注册在 rel 父命令上是为服务读路径（eg rel <k-id>），
// 但它是**纯读**语义，绝不作用于写路径；显式传入即退 1、**零写入**（在参数形态校验阶段拦下，
// 早于任何 plan 组装与 store 写口）。T-…-061 最终审查补齐：flag 注册在父命令导致 add/remove
// 也能解析到它，必须显式拒绝，不能默默忽略。
func rejectReadOnlyVisibilityFlag(inv *Invocation) error {
	if inv.Set("include-deprecated") {
		return &UsageError{Msg: fmt.Sprintf(
			"eg rel %s 不接受 --include-deprecated：该 flag 是只读可见性筛选"+
				"（仅 eg rel 读路径 / eg card show），不作用于写路径（退 1，零写入）", inv.Sub)}
	}
	return nil
}
