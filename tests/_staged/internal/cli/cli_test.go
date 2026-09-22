package cli

// T-evergreen.s1_main_flow-158614-003 Acceptance 的机器判据。
// 参数表比对以 CLI 合同为准：
// teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-09-01-eg-cli-contract.md

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

const contractDoc = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/" +
	"2026-09-01-eg-cli-contract.md"

// m2ContractDoc 是 M2 查询与关系写入合同：M2 落地的 search / card show / rel 的参数表
// 在这里（`--tag` / `--since` / `--until` / `--to`），M1 的 CLI 合同 §1.6–§1.8 只登记了
// 占位形态。参数表比对因此对两份合同取**并集**——实现仍不得自行发明参数，
// 只是「合同」这一侧从一份扩成两份（M2 合同 §8.2 覆盖了 §1.6 / §1.7 的旧标注）。
const m2ContractDoc = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/" +
	"2026-09-19-m2-query-contract.md"

// m3AuthContractDoc 是 M3 授权合同：`--target` 这类状态命令参数登记在这里
// （§2 写权限矩阵 #3 / #4 与 §11 退出码表）。参数表比对因此对三份合同取并集——
// 实现仍不得自行发明参数，只是「合同」这一侧随阶段从两份扩成三份。
const m3AuthContractDoc = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/" +
	"2026-10-10-m3-user-authorization-contract.md"

// m3ProposalContractDoc 是 M3 提案与状态合同：`eg proposal *` 的参数（`--type` / `--status`
// / `--confirm`）登记在这里。参数表比对因此对四份合同取并集——实现仍不得自行发明参数。
const m3ProposalContractDoc = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/" +
	"2026-10-10-m3-proposal-state-contract.md"

// contractUnregisteredFlags 是**合同正文未逐字登记参数名**、但语义有明文出处的参数。
//
// 目前恰一条：`eg proposal list --execution`。提案合同 §4.1 明确 `execution.status` 与
// `status` 是两个正交维度、§7.1 要求 list 可按两维过滤，但合同表只写出了 `--status`
// 这个参数名。这里如实登记「参数名未在合同正文出现」这一事实，而不是悄悄放过整条判据：
// 其余参数照旧必须能在四份合同里逐字找到。
var contractUnregisteredFlags = map[string]string{
	"execution": "提案合同 §4.1 execution.status 正交维度 + §7.1 list 双维过滤（合同表未给出参数名）",
	// T-…-043：提案与状态合同 §5.1 第五列「可显式查看」明写已删除项退出默认视图但可显式查看，
	// task 的 Scope 把入口写成「eg search --include-deleted（或等价显式开关）」，
	// 但四份合同正文都没有逐字给出这个参数名。如实登记这一条，不整体放宽其余参数的判据。
	"include-deleted": "提案与状态合同 §5.1「可显式查看」列 + T-…-043 Scope 的显式开关（合同表未给出参数名）",
	// 【T-…-061 重钉（owner 裁决② A-38/A-39）】§5.1 真值表「作为关系端点默认展示 🔴」要求
	// deprecated 对端默认隐藏、经**显式开关**才展示；作用面恰 eg rel / eg card show 两读命令。
	// 该口径归入 M4 规划，M2/M3 冻结合同正文未逐字给出 `--include-deprecated` 参数名，
	// 故与 include-deleted 同例具名登记，不整体放宽其余参数的逐字可查判据。
	"include-deprecated": "M2 §5.1「作为关系端点默认展示 🔴」+ owner 裁决 A-38/A-39（M2/M3 合同表未给出参数名，归 M4 规划）",
	// T-…-045：授权合同 §9 A-13 判 `eg edit` 属 M3 并明写「用户显式发起的核心内容修改入口」、
	// §2 矩阵 #12 的 P-U 列据此成立、§10.4 判据行要求「改写『知识内容』→ 退 0、内容逐字生效」，
	// 但四份合同正文都**没有**逐字给出 `eg edit` 的参数名（连命令表都只有命令名）。
	// 这里只对 `section` / `content` 两个新参数做**具名**登记，其余参数照旧逐字可查。
	"section": "授权合同 §2 矩阵 #12 / #15 的分区维度 + §9 A-13（合同表未给出 eg edit 的参数名）",
	"content": "授权合同 §9 A-13「核心内容修改入口」+ §10.4「内容逐字生效」（合同表未给出参数名）",
	// Storage v3 D2.2 在新的 block_boundary_materialization Epic 合同中给出该参数；
	// 本判据只装载旧 S1~M3 合同快照，因此在此具名登记，不豁免其它参数。
	"candidate": "Storage v3 正式合同 D2.2：eg edit --candidate <cand-key>",
	// T-…-066：`eg index status --strict` 的出处是 **M5 索引架构合同** §5.1（强校验路径：
	// 忽略 mtime 快路径、全量重算 content_hash）与 §8.1（status 行逐字给出 `--strict`）。
	// 本判据只读 M1~M3 的四份冻结合同，M5 合同不在其中，故与 include-deleted 同例具名登记，
	// 不整体放宽其余参数的逐字可查判据。
	"strict": "M5 索引架构合同 §5.1 强校验路径 + §8.1 status 行；M6 §9 A-56 写命令 --strict 写前强校验 + eg check 恒等开关（M1~M3 冻结合同表未给出参数名）",
	// T-…-068：分页两参数的出处是 **M5 索引架构合同 §8.2 A-47**（逐字给出 `--limit` / `--offset`
	// 的类型、默认值 50 / 0、`--limit 0` == 不限量、超界与负数的边界表），作用面恰
	// search / card show / rel 三条读命令。与 `strict` 同例：本判据只读 M1~M3 的四份冻结合同，
	// M5 合同不在其中，故具名登记这一事实，**不整体放宽**其余参数的逐字可查判据。
	"limit":  "M5 索引架构合同 §8.2 A-47 分页参数表（M1~M3 冻结合同表未给出参数名）",
	"offset": "M5 索引架构合同 §8.2 A-47 分页参数表（M1~M3 冻结合同表未给出参数名）",
	// T-…-068：`--replaced-by` 的语义出处是 M5 合同 §8.4（「若 A.replaced_by = B，则 eg rel A 的
	// 正向与 eg rel B 的反向必须互相可见」），但合同正文只写了**行为**、没有给出参数名 ——
	// 「换一个视图」需要一个显式开关，否则就得改 M2 §3.1 里 relations_out/in 的语义
	// （那是冻结口径，本 task 一格不碰）。如实登记参数名未在合同正文出现这一事实。
	"replaced-by": "M5 索引架构合同 §8.4 replaced_by 正反双向可见（合同正文未给出参数名）",
	// T-…-006-A：`eg search --kind knowledge|opinion|all` 的出处是读路径检索拆分设计 §5.2
	// （scan/index 两后端一致的 kind 收窄，默认 knowledge 绝不泄漏 o-*）。本判据只读 M1~M3 的
	// 四份冻结合同，该设计不在其中，故与 strict/limit/offset 同例具名登记这一事实，
	// 不整体放宽其余参数的逐字可查判据。
	"kind": "读路径检索拆分设计 §5.2 kind 收窄 knowledge|opinion|all（M1~M3 冻结合同表未给出参数名）",
	// T-…-007 批次 A2：`eg opinion validate --reopen` 的出处是**观点 schema v2 设计** §6.1 状态机
	// （rejected/validated → pending 复议边）与 §6.2「回到 pending（复议）复用 eg opinion validate
	// --reopen，避免再加命令」。本判据只读 M1~M3 的四份冻结合同，观点 schema 设计不在其中，
	// 故与 include-deleted / include-deprecated / kind 同例具名登记，不整体放宽其余参数的逐字可查判据。
	"reopen": "观点 schema v2 设计 §6.1 状态机复议边 + §6.2「复用 eg opinion validate --reopen」（M1~M3 冻结合同表未给出参数名）",
}

// 命令展示名（S1 九命令按合同 §1 表格行序，之后是 M3 的三条用户显式状态命令，T-…-039）。
var wantCommands = []string{
	"init", "config get|set", "capture", "context", "apply",
	"search", "card show", "rel", "report --last",
	"deprecate", "restore", "replaced-by",
	// S2 提案子系统（T-…-040）：注册表总量随之 12 → 13。名义上的「S1 九命令」判据
	// 数的是**注册表总量**，因此这里如实加行；proposal 属 S2，不是第十条 S1 命令。
	"proposal new|list|show|approve|reject",
	// M3 逻辑删除（T-…-041）：注册表总量随之 13 → 14。判据仍是「注册表与 --help
	// 命令区逐行相等」，只是行数从 13 变成 14。
	"delete",
	// M3 清空删除标记（T-…-041 收尾）：注册表总量随之 14 → 15。
	"undelete",
	// M3 reviewed_at（T-…-042）：注册表总量随之 15 → 17（mark-reviewed 写入路径 +
	// unreviewed 只读筛选）。判据仍是「注册表与 --help 命令区逐行相等」，只是行数变了。
	"mark-reviewed",
	"unreviewed",
	// M3 用户显式改核心内容（A-13，T-…-045）：注册表总量随之 17 → 18。
	// `eg edit` 是矩阵 #12 的 P-U ✅ 唯一命令载体，判据仍是「注册表与 --help 命令区逐行相等」。
	"edit",
	// M4 全库对账（T-…-058）：注册表总量随之 18 → 19。判据一字未变，仍是
	// 「注册表与 --help 命令区逐行相等」，只是行数按实测从 18 变成 19。
	"reconcile",
	// M4 只读结构体检（T-…-059）：注册表总量随之 19 → 20（M4 收口值）。判据一字未变，
	// 仍是「注册表与 --help 命令区逐行相等」，只是行数按实测从 19 变成 20。
	"check",
	// M5 派生索引（T-…-065）：注册表总量随之 20 → 21（M5 **过程值**；终值 22 由
	// T-…-068 的 `eg bench` 补齐，见 M5 索引架构合同 §8.1）。判据一字未变，仍是
	// 「注册表与 --help 命令区逐行相等」，只是行数按实测从 20 变成 21。
	// 子命令**恰四个** build|rebuild|status|sync —— `sync` 由 T-…-066 阶段 B 补齐
	// （合同 §8.1 的封闭集合），这一格由 TestIndexSubcommandsAreExactlyFour 正面钉住。
	"index build|rebuild|status|sync",
	// M5 性能采样（T-…-068 阶段 B）：注册表总量随之 21 → 22（M5 **终值**，合同 §8.1
	// 命令数复算 20 + 1 + 1 = 22）。判据一字未变，仍是「注册表与 --help 命令区逐行相等」，
	// 只是行数按实测从 21 变成 22。参数面恰 [--json]（无命令私有 flag）。
	"bench",
	// 读路径 CLI 拆分（T-…-006 批次 B1a）：注册表总量随之 22 → 23（设计 §5.4 命令名册）。
	// 判据一字未变，仍是「注册表与 --help 命令区逐行相等」，只是行数按实测从 22 变成 23。
	// 子命令**恰四个** search|show|validate|reject，由 TestOpinionSubcommandsExactlyFour
	// 正面钉住；本批四条均为未实现骨架，参数面恰 [--json]（无命令私有 flag）。
	"opinion search|show|validate|reject",
}

// wantCommandCount 是注册命令总数：S1 九条 + M3 状态三条（deprecate / restore / replaced-by）
// + S2 提案一条（proposal，T-…-040）。**数字变了是事实变了**：判据仍是「注册表与 --help
// 命令区逐行相等」，只是行数从 12 变成 13。
// + M3 逻辑删除一条（delete，T-…-041）：13 → 14。
// + M3 清空删除标记一条（undelete，T-…-041 收尾）：14 → 15。
// + M3 reviewed_at 两条（mark-reviewed / unreviewed，T-…-042）：15 → 17。
// + M3 核心内容编辑一条（edit，T-…-045 / A-13）：17 → 18。
// + M4 对账一条（reconcile，T-…-058）：18 → 19。
// + M4 只读结构体检一条（check，T-…-059）：19 → 20（M4 收口值）。
// 加法等式 18 + 1 = 19（T-…-058 的历史落点）由 TestCommandCountNineteen 逐项复算，
// 18 + 2 = 20（M4 收口）由 TestCommandCountTwenty 逐项复算：两条等式都不是新魔数。
// + M5 派生索引一条（index，T-…-065）：20 → 21（M5 **过程值**，非里程碑收口值）。
// 加法等式 20 + 1 = 21 由 TestCommandCountTwentyOne 逐项复算；M4 收口值 20 那一格
// **原样保留**在 TestCommandCountTwenty 里（它数的是 M4 基线，不引用本常量）。
// + M5 性能采样一条（bench，T-…-068）：21 → 22（M5 **终值**）。
// 加法等式 21 + 1 = 22 由 TestCommandCountTwentyTwo 逐项复算；过程值 21 那一格
// **原样保留**在 TestCommandCountTwentyOne 里（它改证「摘掉 bench 后恰 21」，结论不删）。
// + 读路径 opinion 命令一条（opinion，T-…-006 批次 B1a）：22 → 23（设计 §5.4 名册）。
// 加法等式 22 + 1 = 23 由 TestCommandCountTwentyThree 逐项复算；终值 22 那一格
// **原样保留**在 TestCommandCountTwentyTwo 里（它改证「摘掉 opinion 后恰 22」，结论不删）。
const wantCommandCount = 23

// 每个命令的 flag 集合（逐项对齐合同 §1.1–§1.9；全局 flag 另计）。
var wantFlags = map[string][]string{
	"init":    {"domain"},
	"config":  {},
	"capture": {"url", "title", "body-stdin", "body-file", "reason", "domain", "tag", "captured-at", "reprocess"},
	"context": {"source", "note", "domain"},
	"apply":   {"plan", "dry-run", "strict"},
	// search 的 --include-deleted（T-…-043）：默认视图不返回已删除项，显式开关才带回。
	// S4（T-…-068，M5 合同 §8.2 A-47）：三条读命令各追加封闭的两个分页参数 --limit / --offset；
	// 写命令一律不声明，`eg rel add --limit 1` 由 rel 的 Validate 当场判用法错 → 退 1、零写入。
	// T-…-006-A：search 追加 --kind knowledge|opinion|all（读路径检索面收窄）；封闭值与非法值
	// 文案由 query 层单点定义（query.SearchKind / SearchKindList），命令层只持有开关名字面量。
	"search": {"kind", "domain", "tag", "since", "until", "include-deleted", "limit", "offset"},
	"card":   {"include-deprecated", "limit", "offset"},
	// rel 的 --replaced-by（M5 合同 §8.4）：只作用于读路径的视图开关。
	"rel":    {"reason", "to", "domain", "include-deprecated", "replaced-by", "limit", "offset", "strict"},
	"report": {"last"},
	// M5 性能采样（T-…-068）：`eg bench` 无命令私有 flag —— 采样口径由合同 §7.3 冻结，
	// 能被命令行调小的门槛不是门槛。
	"bench": {},
	// M3 状态三命令（授权合同 §2 矩阵 #3 / #4；三者均不收 --confirm，§3 X1「需确认 = 否」）。
	"deprecate":   {"target", "reason", "strict"},
	"restore":     {"target", "reason", "strict"},
	"replaced-by": {"target", "to", "reason", "strict"},
	// S2 提案子系统（提案合同 §7.1 命令表 S2 行）。
	"proposal": {"type", "target", "reason", "status", "execution", "confirm"},
	// M3 逻辑删除（授权合同 §2 矩阵「逻辑删除」行 + §10.3 V9 / V10；提案合同 §5.3）。
	"delete": {"target", "reason", "proposal", "confirm"},
	// M3 清空删除标记（矩阵 #6）：**不收 --confirm**——退出码 6 的白名单恰含
	// proposal approve 与 delete 两条，undelete 不在其中。
	"undelete": {"target", "reason"},
	// M3 reviewed_at（矩阵 #7 / #22 + 提案与状态合同 §6.1）：mark-reviewed 只收 --target
	// （op 字段恰 target + initiator，没有 reason 这一格）；unreviewed 收三类叠加条件。
	"mark-reviewed": {"target"},
	"unreviewed":    {"domain", "tag", "since", "until"},
	// M3 核心内容编辑（矩阵 #12 P-U ✅ + A-13）：--target / --section / --content 三格，
	// 外加全局 --user-request 佐证（全局 flag 另计，不进本表）。**不收 --confirm**：
	// §3 X1「需确认 = 否」，退出码 6 的白名单恰 proposal approve 与 delete 两条。
	"edit": {"target", "candidate", "section", "content", "strict"},
	// M4 全库对账（对账合同 §12 参数表）：**恰一个**命令私有 flag。全库口径不接受任何
	// 范围收窄参数（收窄属 S4），未声明即由参数解析当场判非法 → 退 1、零写入。
	"reconcile": {"dry-run"},
	// M4 只读结构体检（对账合同 §13 参数表）：**恰 0 个**命令私有 flag。
	// `--include-deprecated` 属不作用面（可见性合同 §3.4），不声明即由参数解析当场判非法
	// → 退 1、零写入；同理不接受任何范围收窄参数。
	"check": {"strict"},
	// M5 派生索引（M5 索引架构合同 §8.1 status 行）：**恰 1 个**命令私有 flag。
	// `--strict` 由 T-…-066 阶段 B 补齐（`eg index status` 的「忽略 (size, mtime) 快路径、
	// 全量重算 content_hash」只读开关，且只对 status 有语义）；`--limit` 等分页参数属
	// T-…-068，仍不声明 → 传入即由参数解析当场判非法 → 退 1、零写入。
	"index": {"strict"},
	// 读路径 opinion 命令（T-…-006 批次 B2b 接通 search + show；D 批补齐 validate/reject 骨架合同；
	// T-…-007 批次 A2 补齐 --reopen 参数面）：父命令注册 **10 个** flag —— 8 个读 flag（search 复用
	// `eg search` 口径的检索 / 分页 flag：domain / 可重复 tag / since / until / include-deleted /
	// limit / offset，外加 show 专属的对端可见性开关 include-deprecated），再加 **2 个写路径 flag**：
	// `--reason`（观点验证 / 驳回理由，作用于 validate / reject）与 `--reopen`（观点复议 bool，默认
	// false，**只作用于 validate**：把验证态复议回 pending；设计 §6.2「复用 eg opinion validate
	// --reopen，避免再加命令」）。**刻意不注册 --kind**：opinion 检索面天然只搜观点
	// （runOpinionSearch 把 Kind 固定成 opinion），再给 kind 开关就是多余且可诱导误用；
	// `eg opinion search --kind …` 因此被参数解析当场判成「未定义 flag」→ 退 1、零写入。
	// 这 10 个 flag 都注册在父命令上（FlagSet 分不清子命令），因此每条子命令都会**解析**到它们；
	// 各子命令按分域显式拒绝不属于自己的 flag（见 validateOpinionArgs 的 rejectOpinionFlags）：
	// search 拒 include-deprecated + reason + reopen、show 拒检索过滤 flag + reason + reopen、
	// reject 拒全部读 flag + reopen 但**必带**非空 --reason，validate 拒全部读 flag、**接受** --reopen
	// 但仍必带非空 --reason，绝不静默接受 ——「参数写了却不生效」比报错更坏。--reason 的合同出处与
	// delete / proposal / capture 同源；--reopen 的合同出处为观点 schema v2 设计 §6.1/§6.2。
	"opinion": {"domain", "tag", "since", "until", "include-deleted", "include-deprecated", "limit", "offset", "reason", "reopen"},
}

var globalFlagNames = []string{"json", "vault", "help", "h"}

func newTestRoot(t *testing.T, wd string) *Root {
	t.Helper()
	r := New()
	r.Getwd = func() (string, error) { return wd, nil }
	return r
}

func runCLI(t *testing.T, r *Root, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := r.Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// —— ① eg --help 列出且仅列出九个 S1 命令 ——

func TestHelpListsExactlyNineS1Commands(t *testing.T) {
	r := New()
	if len(r.Commands()) != wantCommandCount {
		t.Fatalf("注册命令数 = %d，期望 %d", len(r.Commands()), wantCommandCount)
	}
	var got []string
	for _, c := range r.Commands() {
		got = append(got, c.Display)
	}
	if strings.Join(got, ",") != strings.Join(wantCommands, ",") {
		t.Fatalf("命令表 = %v，期望 %v", got, wantCommands)
	}

	help := r.Usage()
	block := helpCommandBlock(t, help)
	if len(block) != wantCommandCount {
		t.Fatalf("--help 命令区 %d 行，期望 %d 行：%v", len(block), wantCommandCount, block)
	}
	for i, line := range block {
		if !strings.HasPrefix(line, wantCommands[i]) {
			t.Fatalf("--help 第 %d 行 = %q，期望以 %q 开头", i+1, line, wantCommands[i])
		}
	}
	// S2 / S3 / S4 命令不得注册，也不得出现在 --help。
	// M4 起 `reconcile` 已是注册命令（T-…-058），因此它从本反证清单里移出 ——
	// 它的正面判据改由上面的「注册表与 --help 命令区逐行相等」+ TestCommandCountNineteen
	// 承担，强度不降。
	// M5 起 `index` 同样已是注册命令（T-…-065），一并移出本反证清单；它的正面判据由
	// 逐行相等 + TestCommandCountTwentyOne + TestIndexSubcommandsAreExactlyThree 承担。
	// 其余四个词仍是零注册反证（review / propose 属 S3、relate / graph 从不是命令名）。
	for _, forbidden := range []string{"review", "propose", "relate", "graph"} {
		if r.Lookup(forbidden) != nil {
			t.Fatalf("命令 %q 不属 S1 九命令，不得注册", forbidden)
		}
		for _, line := range block {
			if strings.HasPrefix(line, forbidden) {
				t.Fatalf("--help 命令区出现非 S1 命令 %q", forbidden)
			}
		}
	}
}

// helpCommandBlock 抽出 --help 的命令区（到空行为止）。
func helpCommandBlock(t *testing.T, help string) []string {
	t.Helper()
	lines := strings.Split(help, "\n")
	var block []string
	in := false
	for _, l := range lines {
		if strings.HasPrefix(l, "命令（") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if strings.TrimSpace(l) == "" {
			break
		}
		block = append(block, strings.TrimSpace(l))
	}
	if len(block) == 0 {
		t.Fatalf("--help 中未找到命令区：\n%s", help)
	}
	return block
}

// —— ② 每个命令的 --help 参数与合同参数表逐项一致 ——

func TestCommandFlagsMatchContract(t *testing.T) {
	contract, readErr := os.ReadFile(contractDoc)
	if readErr != nil {
		t.Logf("合同文档不可读（要求 teamwork/ 与 evergreen/ 同级）：%v；仅做本地表比对", readErr)
	}
	if m2, err := os.ReadFile(m2ContractDoc); err == nil {
		contract = append(contract, m2...)
	} else {
		t.Logf("M2 查询合同不可读：%v；仅做本地表比对", err)
	}
	if m3, err := os.ReadFile(m3AuthContractDoc); err == nil {
		contract = append(contract, m3...)
	} else {
		t.Logf("M3 授权合同不可读：%v；仅做本地表比对", err)
	}
	if m3p, err := os.ReadFile(m3ProposalContractDoc); err == nil {
		contract = append(contract, m3p...)
	} else {
		t.Logf("M3 提案合同不可读：%v；仅做本地表比对", err)
	}
	for _, c := range New().Commands() {
		fs := flag.NewFlagSet("eg "+c.Name, flag.ContinueOnError)
		fs.SetOutput(&bytes.Buffer{})
		fs.Bool("json", false, "")
		fs.String("vault", "", "")
		fs.Bool("help", false, "")
		fs.Bool("h", false, "")
		if c.Flags != nil {
			c.Flags(fs)
		}
		var got []string
		fs.VisitAll(func(f *flag.Flag) {
			for _, g := range globalFlagNames {
				if f.Name == g {
					return
				}
			}
			got = append(got, f.Name)
		})
		want := append([]string{}, wantFlags[c.Name]...)
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("eg %s 的 flag = %v，合同参数表 = %v", c.Display, got, want)
		}
		// --help 文本必须逐项列出这些参数，且合同里也必须有同名参数。
		for _, name := range want {
			if !strings.Contains(c.Usage, "--"+name) {
				t.Errorf("eg %s 的 --help 未列出 --%s", c.Display, name)
			}
			if _, registered := contractUnregisteredFlags[name]; registered {
				continue
			}
			if len(contract) > 0 && !strings.Contains(string(contract), "--"+name) {
				t.Errorf("合同文档缺参数 --%s（实现不得自行发明参数）", name)
			}
		}
		if !strings.Contains(c.Usage, "eg "+c.Name) {
			t.Errorf("eg %s 的 --help 缺用法行", c.Display)
		}
	}
}

func TestEachCommandHelpExitsZeroAndPrintsToStdout(t *testing.T) {
	for _, c := range New().Commands() {
		r := newTestRoot(t, t.TempDir())
		code, out, errOut := runCLI(t, r, c.Name, "--help")
		if code != ExitOK {
			t.Errorf("eg %s --help 退出码 = %d，期望 0", c.Name, code)
		}
		if !strings.Contains(out, "eg "+c.Name) {
			t.Errorf("eg %s --help 未把用法写进 stdout：%q", c.Name, out)
		}
		if errOut != "" {
			t.Errorf("eg %s --help 不应写 stderr：%q", c.Name, errOut)
		}
	}
}

// —— ③ 占位调用形态：M3 接管后清单**清零** ——
//
// M1 期是五条（search / card show / rel / rel add / rel remove）。M2 逐个落地后清单
// **只减不增**：T-…-021 摘掉 search、T-…-022 摘 card show、T-…-023 摘 rel 读路径、
// T-…-024 摘 rel add，摘掉的形态各自有真实实现与用例接管，绝不是「不再检查」。
// 最后一条 `rel remove` 由 **T-…-044** 接管成真实写路径（verb=relate、一次 commit、
// A-24 物理移除），因此本清单归零——占位形态的反面判据改由本用例的第二段承担：
// 任何命令的输出与 `--help` 里都不得再出现阶段「未实现」宣告。
var placeholderInvocations = [][]string{}

func TestPlaceholderCommandsExitOneWithFixedWording(t *testing.T) {
	if len(placeholderInvocations) != 0 {
		t.Fatalf("M3 起占位用例集合必须为空（rel remove 已由 T-…-044 接管），实际 %d",
			len(placeholderInvocations))
	}
	for _, args := range placeholderInvocations {
		dir := t.TempDir()
		before := snapshot(t, dir)
		r := newTestRoot(t, dir)
		code, out, errOut := runCLI(t, r, args...)
		if code != ExitUsage {
			t.Fatalf("eg %v 退出码 = %d，期望 1", args, code)
		}
		if strings.Contains(errOut, MilestoneTag) {
			t.Fatalf("eg %v 的占位文案不得再写成 %q", args, MilestoneTag)
		}
		if strings.Contains(errOut+out, forbiddenWording) {
			t.Fatalf("eg %v 输出出现被禁措辞 %q", args, forbiddenWording)
		}
		if out != "" {
			t.Fatalf("占位命令不得写 stdout，实际 %q", out)
		}
		if after := snapshot(t, dir); after != before {
			t.Fatalf("eg %v 产生了文件变化：%q → %q", args, before, after)
		}
	}
	// 反面判据：`rel remove` 的 --help 与真实调用都不得再宣告阶段未实现。
	dir := t.TempDir()
	r := newTestRoot(t, dir)
	_, help, _ := runCLI(t, r, "rel", "--help")
	if !strings.Contains(help, "rel remove") {
		t.Fatalf("eg rel --help 必须列出 rel remove 的真实用法：%q", help)
	}
	if strings.Contains(help, "未实现") {
		t.Fatalf("eg rel --help 不得再出现阶段「未实现」宣告：%q", help)
	}
}

// TestUsageErrorJSONEnvelope —— 用法错误（退 1）的 JSON 信封形状。
//
// 本用例原名 TestPlaceholderJSONEnvelope，取材是 `rel remove` 的占位错误；
// T-…-044 接管后占位不存在了，改用**同一命令的参数形态错误**（位置参数少一个）继续取证：
// 覆盖面不变（信封键 / exit_code / status / data.errors），只是不再依赖占位文案。
func TestUsageErrorJSONEnvelope(t *testing.T) {
	r := newTestRoot(t, t.TempDir())
	code, out, _ := runCLI(t, r, "--json", "rel", "remove", "k-20260901-a", "supports")
	if code != ExitUsage {
		t.Fatalf("退出码 = %d，期望 1", code)
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v（%q）", err, out)
	}
	assertEnvelopeKeys(t, env)
	if env["exit_code"].(float64) != 1 || env["status"] != StatusFailed || env["ok"].(bool) {
		t.Fatalf("信封与退出码不一致：%v", env)
	}
	data, _ := env["data"].(map[string]interface{})
	raw, _ := json.Marshal(data["errors"])
	if !strings.Contains(string(raw), "eg rel remove 需要恰三个位置参数") {
		t.Fatalf("data.errors 缺可定位的用法错误：%s", raw)
	}
}

// TestRelSubcommandUsageErrorsStayZeroEffect —— `rel remove` 的**参数形态错误**一律
// 退 1、零副作用、且**不落进读路径**（不泄漏 relations_out 等读路径字段）。
//
// 本用例原名 TestRelQuerySubcommandsUnwired，取材是「未实现」分支；T-…-044 接管后
// 该分支消失，改由参数形态错误承接同一批断言（退 1 / 零文件变化 / 不落读路径）。
func TestRelSubcommandUsageErrorsStayZeroEffect(t *testing.T) {
	for _, args := range [][]string{
		{"rel", "remove", "k-20260901-a", "supports"},
		{"rel", "remove", "k-20260901-a", "不存在的关系", "k-20260901-b"},
		{"rel", "remove", "k-20260901-a", "supports", "k-20260901-b"}, // 缺 --reason
	} {
		dir := t.TempDir()
		before := snapshot(t, dir)
		r := newTestRoot(t, dir)
		code, out, errOut := runCLI(t, r, args...)
		if code != ExitUsage {
			t.Fatalf("eg %v 退出码 = %d，期望 1", args, code)
		}
		if strings.Contains(errOut, "未实现") {
			t.Fatalf("eg %v 的 stderr 不得再出现阶段「未实现」宣告：%q", args, errOut)
		}
		for _, leak := range []string{"relations_out", "relations_in", "scanned_files"} {
			if strings.Contains(out+errOut, leak) {
				t.Fatalf("eg %v 泄漏了读路径字段 %q：%q", args, leak, out+errOut)
			}
		}
		if after := snapshot(t, dir); after != before {
			t.Fatalf("eg %v 产生了文件变化：%q → %q", args, before, after)
		}
	}
	relCmd := New().Lookup("rel")
	// Usage 的两条写路径行（T-…-024 落 add、T-…-044 接管 remove）：
	//   ① `add` 行不再带任何未实现 / 里程碑占位标注（它已经是真实实现）；
	//   ② `remove` 行同样必须标注为已实现——阶段占位被接管后不得留任何未实现宣告。
	var sawAdd, sawRemove bool
	for _, line := range strings.Split(relCmd.Usage, "\n") {
		switch {
		case strings.HasPrefix(line, "eg rel add"):
			sawAdd = true
			if strings.Contains(line, MilestoneTag) || strings.Contains(line, PlaceholderNotice) {
				t.Fatalf("rel add 行不得再带未实现标注：%q", line)
			}
			if !strings.Contains(line, "已实现") {
				t.Fatalf("rel add 行应标注为已实现：%q", line)
			}
		case strings.HasPrefix(line, "eg rel remove"):
			sawRemove = true
			if !strings.Contains(line, "已实现") {
				t.Fatalf("rel remove 行应标注为已实现（T-…-044 已接管占位）：%q", line)
			}
			if strings.Contains(line, "未实现") || strings.Contains(line, MilestoneTag) {
				t.Fatalf("rel remove 行不得再带任何未实现标注：%q", line)
			}
		}
	}
	if !sawAdd || !sawRemove {
		t.Fatalf("rel --help 必须同时给出 add / remove 两行用法（add=%v remove=%v）", sawAdd, sawRemove)
	}
	if relCmd.Placeholder {
		t.Fatal("rel 读路径已落地，命令级 Placeholder 必须摘除（否则无法 Wire 实现）")
	}
}

func TestPlaceholderCannotBeWired(t *testing.T) {
	r := New()
	for _, c := range r.Commands() {
		if !c.Placeholder {
			continue
		}
		if err := r.Wire(c.Name, func(*Invocation) (*Result, error) { return &Result{}, nil }); err == nil {
			t.Fatalf("占位命令 %q 不得允许挂载实现", c.Name)
		}
	}
	for _, name := range []string{"context", "search", "card", "rel"} {
		if err := r.Wire(name, func(*Invocation) (*Result, error) { return &Result{}, nil }); err != nil {
			t.Fatalf("%s 应允许挂载实现（非占位命令）：%v", name, err)
		}
	}
}

// —— ④ 退出码集中映射：0/1/2/3/4 五条路径 ——

func TestExitCodeForCoversFivePaths(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, ExitOK},
		{&UsageError{Msg: "u"}, ExitUsage},
		{&NotWiredError{Command: "init", Owner: "T-…-008"}, ExitUsage},
		{&ValidationError{Msg: "v"}, ExitValidation},
		{&PartialWriteError{Msg: "p"}, ExitPartialWrite},
		{&CommitFailedError{Msg: "c", Err: errors.New("git")}, ExitCommitFailed},
		{errors.New("未分类"), ExitUsage},
	}
	for _, c := range cases {
		if got := ExitCodeFor(c.err); got != c.want {
			t.Errorf("ExitCodeFor(%v) = %d，期望 %d", c.err, got, c.want)
		}
	}
	for code, want := range map[int]string{
		ExitOK: StatusCompleted, ExitUsage: StatusFailed, ExitValidation: StatusFailed,
		ExitPartialWrite: StatusPartial, ExitCommitFailed: StatusPartial,
	} {
		if got := StatusFor(code); got != want {
			t.Errorf("StatusFor(%d) = %q，期望 %q", code, got, want)
		}
	}
	// 5 / 6 不得出现在 S1 的映射结果里。
	for _, err := range []error{nil, &UsageError{}, &ValidationError{}, &PartialWriteError{},
		&CommitFailedError{}, &NotWiredError{}, errors.New("x")} {
		if got := ExitCodeFor(err); got < 0 || got > 4 {
			t.Fatalf("退出码 %d 越出 S1 的封闭集合 0–4（5 属 S5、6 属 S2）", got)
		}
	}
}

func TestRunMapsTypedErrorsEndToEnd(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"ok", nil, ExitOK},
		{"usage", &UsageError{Msg: "参数非法"}, ExitUsage},
		{"validation", &ValidationError{Msg: "校验失败"}, ExitValidation},
		{"partial", &PartialWriteError{Msg: "部分写入被跳过"}, ExitPartialWrite},
		{"commit", &CommitFailedError{Msg: "Git 提交失败", Err: errors.New("exit 128")}, ExitCommitFailed},
	}
	for _, c := range cases {
		dir := configuredVault(t)
		r := newTestRoot(t, dir)
		err := c.err
		if e := r.Wire("apply", func(*Invocation) (*Result, error) {
			return &Result{Data: map[string]interface{}{"probe": c.name}}, err
		}); e != nil {
			t.Fatal(e)
		}
		code, out, errOut := runCLI(t, r, "--json", "apply", "--plan", "-")
		if code != c.want {
			t.Fatalf("%s：退出码 = %d，期望 %d（stderr=%q）", c.name, code, c.want, errOut)
		}
		var env map[string]interface{}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%s：--json 输出非法：%v（%q）", c.name, err, out)
		}
		if int(env["exit_code"].(float64)) != c.want {
			t.Fatalf("%s：exit_code 与进程退出码必须逐字相同", c.name)
		}
		if env["status"] != StatusFor(c.want) {
			t.Fatalf("%s：status 必须由 exit_code 派生", c.name)
		}
	}
}

// —— ⑤ 双渲染事实一致 ——

func TestDualRenderingSameFacts(t *testing.T) {
	res := &Result{
		Data: map[string]interface{}{
			"source_id": "s-20260901-a",
			"deduped":   true,
			"path":      "sources/s-20260901-a.md",
		},
		Warnings: []Diagnostic{{
			Code: "W6", Level: LevelWarning, Path: "ops[2].reason", OpIndex: 2,
			Message: "base 未覆盖 k-20260901-bar，已跳过该文件", Target: "k-20260901-bar",
		}},
		Summary: []string{"已收录 1 篇原文"},
	}
	env := NewEnvelope(res, ExitOK)

	var jsonBuf, humanBuf bytes.Buffer
	if err := RenderJSON(&jsonBuf, env); err != nil {
		t.Fatal(err)
	}
	if err := RenderHuman(&humanBuf, env, res.Summary); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	assertEnvelopeKeys(t, decoded)

	human := humanBuf.String()
	for k, v := range res.Data {
		if !strings.Contains(human, k) {
			t.Fatalf("人类可读渲染缺 data 键 %q：\n%s", k, human)
		}
		if !strings.Contains(human, scalarString(v)) {
			t.Fatalf("人类可读渲染缺 data 值 %v：\n%s", v, human)
		}
	}
	for _, want := range []string{
		StatusCompleted, "exit_code=0", res.Summary[0],
		res.Warnings[0].Code, res.Warnings[0].Path, res.Warnings[0].Message, res.Warnings[0].Target,
	} {
		if !strings.Contains(human, want) {
			t.Fatalf("人类可读渲染缺事实 %q：\n%s", want, human)
		}
	}
	if !strings.Contains(human, "警告（1 条）") {
		t.Fatalf("人类可读渲染缺警告条数：\n%s", human)
	}
}

func assertEnvelopeKeys(t *testing.T, env map[string]interface{}) {
	t.Helper()
	var got []string
	for k := range env {
		got = append(got, k)
	}
	want := append([]string{}, EnvelopeKeys()...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("信封键 = %v，合同 §3 规定恰五项 %v", got, want)
	}
}

// —— ⑥ 参数非法退 1 且零文件变化 ——

func TestInvalidArgsExitOneWithZeroFileChange(t *testing.T) {
	cases := [][]string{
		{"apply"},                           // 缺必填 --plan
		{"apply", "--nope"},                 // 未知 flag
		{"config"},                          // 缺子命令
		{"config", "frobnicate", "x"},       // 非法子命令
		{"config", "get", "unknown_key"},    // 非法配置键
		{"config", "set", "default_domain"}, // 缺 value
		{"context"},                         // 缺 --source/--note
		{"context", "--source", "s-1", "--note", "n-1"}, // 互斥
		{"capture", "--reason", "r", "--body-stdin"},    // url/title 同缺 → 2（另测）
		{"report"},                   // 缺 --last
		{"init", "extra-positional"}, // 多余位置参数
		{"nosuchcommand"},            // 未知命令
	}
	for _, args := range cases {
		dir := configuredVault(t)
		before := snapshot(t, dir)
		r := newTestRoot(t, dir)
		code, out, errOut := runCLI(t, r, append([]string{"--vault", dir}, args...)...)
		wantCode := ExitUsage
		if args[0] == "capture" {
			wantCode = ExitValidation // 合同 §1.3：--url 与 --title 同时缺失 → 校验失败
		}
		if code != wantCode {
			t.Fatalf("eg %v 退出码 = %d，期望 %d（stderr=%q）", args, code, wantCode, errOut)
		}
		if out != "" {
			t.Fatalf("eg %v 参数非法时 stdout 必须为空，实际 %q", args, out)
		}
		if after := snapshot(t, dir); after != before {
			t.Fatalf("eg %v 参数非法却改了文件：%q → %q", args, before, after)
		}
	}
}

// —— ⑦ default_domain 守卫 ——

func TestDefaultDomainGuard(t *testing.T) {
	// 未配置 default_domain：context 退 1 且 stderr 含配置提示。
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName), "version: 1\ndomains:\n  - ai-infra\n")
	r := newTestRoot(t, dir)
	code, _, errOut := runCLI(t, r, "context", "--source", "s-20260901-a")
	if code != ExitUsage {
		t.Fatalf("未配置 default_domain 时 eg context 退出码 = %d，期望 1", code)
	}
	if !strings.Contains(errOut, "default_domain") || !strings.Contains(errOut, "eg config set") {
		t.Fatalf("stderr 缺配置提示：%q", errOut)
	}

	// init / config 不受该守卫影响（实现由 T-…-008 挂载后应正常执行，退 0）。
	setGitIdentity(t)
	for _, args := range [][]string{{"init"}, {"config", "get", "default_domain"}} {
		code, _, errOut := runCLI(t, newTestRoot(t, dir), args...)
		if strings.Contains(errOut, "未配置 default_domain") {
			t.Fatalf("eg %v 不应被 default_domain 守卫拦截：%q", args, errOut)
		}
		if code != ExitOK {
			t.Fatalf("eg %v 应正常执行（T-…-008 已挂载实现），实际 code=%d stderr=%q", args, code, errOut)
		}
	}

	// 已配置 default_domain：守卫放行，落到 context 自身的校验
	// （T-…-010 挂载后：库里没有该原文 → 退 2「对象不存在」，而不是被守卫拦在 1）。
	ok := configuredVault(t)
	code, _, errOut = runCLI(t, newTestRoot(t, ok), "context", "--source", "s-20260901-a")
	if code != ExitValidation || strings.Contains(errOut, "default_domain") {
		t.Fatalf("守卫应放行，实际 code=%d stderr=%q", code, errOut)
	}
	// 找不到 vault：退 1 并提示 evergreen.yml。
	empty := t.TempDir()
	code, _, errOut = runCLI(t, newTestRoot(t, empty), "context", "--vault", empty, "--source", "s-1")
	if code != ExitUsage || !strings.Contains(errOut, ConfigFileName) {
		t.Fatalf("缺 %s 时应退 1 并提示，实际 code=%d stderr=%q", ConfigFileName, code, errOut)
	}
}

func TestGuardReadsConfigReadOnly(t *testing.T) {
	dir := configuredVault(t)
	raw, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfigReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != model.ConfigVersion || cfg.DefaultDomain != "ai-infra" {
		t.Fatalf("配置解析异常：%+v", cfg)
	}
	if cfg.Extra["custom_key"] != "保留" {
		t.Fatalf("未知键必须落进 Extra：%v", cfg.Extra)
	}
	after, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, after) {
		t.Fatal("只读解析不得改动 evergreen.yml 一个字节")
	}
}

// —— ⑧ 只读命令零副作用：git status 为空、git log 条数不变 ——

func TestReadOnlyCommandsLeaveGitClean(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("无 git，跳过 git 层零副作用断言")
	}
	dir := configuredVault(t)
	run := func(args ...string) string {
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v 失败：%v（%s）", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	run("-c", "user.email=eg@example.com", "-c", "user.name=eg", "add", ".")
	run("-c", "user.email=eg@example.com", "-c", "user.name=eg", "commit", "-q", "-m", "init")
	if s := run("status", "--porcelain"); s != "" {
		t.Fatalf("前置条件失败，工作区应干净：%q", s)
	}
	logBefore := run("rev-list", "--count", "HEAD")

	readOnly := [][]string{
		{"context", "--source", "s-20260901-a"},
		{"report", "--last"},
		{"search", "rag"},
		{"card", "show", "k-20260901-x"},
		{"rel", "k-20260901-x"},
	}
	for _, args := range readOnly {
		r := newTestRoot(t, dir)
		runCLI(t, r, append([]string{"--vault", dir}, args...)...)
		if s := run("status", "--porcelain"); s != "" {
			t.Fatalf("eg %v 之后 git status 非空：%q", args, s)
		}
		if got := run("rev-list", "--count", "HEAD"); got != logBefore {
			t.Fatalf("eg %v 改变了 git log 条数：%q → %q", args, logBefore, got)
		}
	}
	for _, c := range New().Commands() {
		if c.Placeholder && !c.ReadOnly {
			t.Fatalf("占位命令 %s 必须标记为只读（零副作用保证覆盖它们）", c.Display)
		}
	}
}

// —— ⑨ 源码级反证：os.Exit / 被禁措辞 / rel 不引用查询包 ——

func TestOnlyMainCallsOsExit(t *testing.T) {
	root := "../.."
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if name := info.Name(); name == "bin" || name == "dist" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// 反证串同样分片拼接，避免本测试文件自己成为命中项。
		if !strings.Contains(string(raw), "os."+"Exit(") {
			return nil
		}
		if filepath.ToSlash(path) == "../../cmd/eg/main.go" {
			return nil
		}
		offenders = append(offenders, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("除 cmd/eg/main.go 外不得调用 os.Exit，命中：%v", offenders)
	}
}

// forbiddenWording 是被禁措辞。**分片拼接**而非字面量：门禁口径是
// `grep -rn "<该措辞>" internal/cli/` 必须零匹配，字面量会让本文件自己成为命中项。
var forbiddenWording = "S1 " + "未" + "覆盖"

// TestForbiddenWordingAbsent 对 internal/cli 全部 .go 文件（含测试）反证被禁措辞。
func TestForbiddenWordingAbsent(t *testing.T) {
	for _, f := range goFilesHere(t) {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), forbiddenWording) {
			t.Fatalf("%s 出现被禁措辞 %q（应为 %q）", f, forbiddenWording, PlaceholderNotice)
		}
	}
}

// TestRelFilesDoNotReferenceWriteAPI —— P1-2 反证第三次按阶段口径改写（不是删除覆盖面）：
//
//	M1：禁「rel* 引用查询包」（读路径 M1 不实现）；
//	T-…-023：读路径落地后收紧为「rel* 不得引用写入 API」；
//	T-…-024：`rel add` 落地写路径，禁令**按文件分工**——
//	  ① 全部 rel* 文件（含 rel_add.go）一律**不得 import internal/store**：
//	     写盘只能经 ChangePlan → internal/plan → executor → store 写口，
//	     禁止绕过 ChangePlan 直接调 store（合同 §4.2，本条即其机器反证）；
//	  ② 读路径文件 rel.go 额外禁 internal/plan / internal/git 与 Apply* / Write* / Commit(，
//	     保证 `eg rel <k-id>` 永远只读（合同 §6）；
//	  ③ rel_add.go 必须**经 runPlan 复用** apply 的同一条链路（不得复制平行实现）。
func TestRelFilesDoNotReferenceWriteAPI(t *testing.T) {
	matches, err := filepath.Glob("rel*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("internal/cli 下应有 rel* 文件承载 rel 命令")
	}
	const storeImport = `github.com/ikaqiu-Lemon/EverGreen/internal/store"`
	readOnlyBanned := []string{`github.com/ikaqiu-Lemon/EverGreen/internal/plan"`, `github.com/ikaqiu-Lemon/EverGreen/internal/git"`,
		"ApplyPlan(", "WriteFile(", "Commit("}
	for _, f := range matches {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, storeImport) {
			t.Fatalf("%s 不得 import internal/store（禁止绕过 ChangePlan 直接调 store）", f)
		}
		// 具名收窄：写路径文件恰两个（rel_add.go / rel_remove.go），它们允许经
		// internal/plan 走 ChangePlan 链路；其余 rel* 文件仍是只读路径，一律不得碰写入 API。
		if f == "rel_add.go" || f == "rel_remove.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, b := range readOnlyBanned {
			if strings.Contains(text, b) {
				t.Fatalf("%s 不得引用写入 API %q（P1-2 反证：rel 读路径只读）", f, b)
			}
		}
	}
	// rel add 的写入链路反证：走 plan 包 + 复用 apply 的 runPlan，且 apply 里只有一份实现。
	addSrc, err := os.ReadFile("rel_add.go")
	if err != nil {
		t.Fatalf("rel add 的实现文件缺失：%v", err)
	}
	if !strings.Contains(string(addSrc), `github.com/ikaqiu-Lemon/EverGreen/internal/plan"`) {
		t.Fatal("rel_add.go 必须经 internal/plan 组装并校验 ChangePlan")
	}
	if !strings.Contains(string(addSrc), "runPlan(r, inv, p)") {
		t.Fatal("rel_add.go 必须调用 apply 的 runPlan（两条入口共用同一条执行链）")
	}
	// rel remove 的写入链路同款反证（T-…-044）：经 plan 包 + 复用同一个 runPlan，
	// 且**不得** import internal/store（上面的 storeImport 断言已对全部 rel* 文件生效）。
	rmSrc, err := os.ReadFile("rel_remove.go")
	if err != nil {
		t.Fatalf("rel remove 的实现文件缺失：%v", err)
	}
	if !strings.Contains(string(rmSrc), `github.com/ikaqiu-Lemon/EverGreen/internal/plan"`) {
		t.Fatal("rel_remove.go 必须经 internal/plan 组装并校验 ChangePlan")
	}
	if !strings.Contains(string(rmSrc), "runPlan(r, inv, p)") {
		t.Fatal("rel_remove.go 必须调用 apply 的 runPlan（三条入口共用同一条执行链）")
	}
	applySrc, err := os.ReadFile("apply.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(applySrc), "func runPlan"); n != 1 {
		t.Fatalf("apply.go 里 runPlan 的定义必须恰一处（无平行副本），实际 %d 处", n)
	}
}

func goFilesHere(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			out = append(out, e.Name())
		}
	}
	return out
}

// —— 测试辅助 ——

func configuredVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName),
		"version: 1\n# 用户注释：只读解析不得动它\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\ncustom_key: 保留\n")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// snapshot 返回目录树的稳定快照（路径 + 内容长度），用于零副作用断言。
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if info.IsDir() {
			lines = append(lines, "d "+rel)
			return nil
		}
		lines = append(lines, "f "+rel+" "+itoa(info.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// —— M4 注册表总量：18 + 1 = 19（T-…-058）与 18 + 2 = 20（T-…-059 收口）——

// TestCommandCountNineteen 把 T-…-058 收口当日的事实「命令数 19」证成一条**加法等式**：
// M3 收口基线恰 18 条，T-…-058 只新增 `reconcile` 一条，故 18 + 1 = 19。
//
// **T-…-059 重钉说明（2026-09-07）**：`eg check` 注册后磁盘实测总数变成 20，
// 本用例的上游注释早就写明「那时应把等式改写成 18 + 2 = 20 并把差集扩成两条」。
// 这里按更强的写法重钉：**19 这个历史结论一个字不删**，改证成
// 「把 M4 收口新增的 `check` 摘掉之后恰 19」——
// ① 前 18 条逐字等于 M3 基线；② M4 期差集去掉 `check` 后**恰一条**且逐字是 `reconcile`；
// ③ `reconcile` 落在 M3 基线之后的**第一位**（既有 18 条的次序与名称一字未动）；
// ④ `reconcile` 已 Wire 且非占位。
// 总量 20 那一格由 TestCommandCountTwenty 正面对撞，两条等式互不代替。
func TestCommandCountNineteen(t *testing.T) {
	// M3 收口（0.3.0-m3）的注册表基线，逐字照抄，**不引用 wantCommands 派生**：
	// 两份清单必须独立，否则 wantCommands 被改错时本用例会跟着一起错。
	m3Baseline := []string{
		"init", "config", "capture", "context", "apply",
		"search", "card", "rel", "report",
		"deprecate", "restore", "replaced-by",
		"proposal", "delete", "undelete",
		"mark-reviewed", "unreviewed", "edit",
	}
	// M4 T-…-058 新增的命令名（逐字，含数量）。
	const m4Added = "reconcile"
	const m4AddedCount = 1
	// M4 收口（T-…-059）追加的命令名：本用例只负责 T-…-058 那一条等式，
	// 因此把它从差集里**摘掉后**再复算 19（历史结论不动，也不放宽）。
	const m4LaterAdded = "check"
	// M5 期（T-…-065）追加的命令名：同理摘掉后再复算 19。
	// **19 这个历史结论一个字不删**，只是「要摘掉的后来者」从一条变成两条。
	const m5LaterAdded = "index"
	// M5 阶段 B（T-…-068）追加的命令名：同理摘掉后再复算 19。
	// **19 这个历史结论一个字不删**，只是「要摘掉的后来者」从两条变成三条。
	const m5LaterAdded2 = "bench"
	// T-…-006 批次 B1a 追加的命令名：同理摘掉后再复算 19。
	// **19 这个历史结论一个字不删**，只是「要摘掉的后来者」从三条变成四条。
	const m5LaterAdded3 = "opinion"

	if len(m3Baseline) != 18 {
		t.Fatalf("M3 基线清单写错了：%d 条，M3 收口时恰 18 条", len(m3Baseline))
	}
	if want := len(m3Baseline) + m4AddedCount + 1 + 1 + 1 + 1; want != wantCommandCount {
		t.Fatalf("加法等式不成立：%d + %d（058）+ 1（059 的 %s）+ 1（065 的 %s）+ 1（068 的 %s）+ 1（006-B1a 的 %s）= %d，"+
			"但 wantCommandCount = %d",
			len(m3Baseline), m4AddedCount, m4LaterAdded, m5LaterAdded, m5LaterAdded2, m5LaterAdded3, want, wantCommandCount)
	}

	got := New().Commands()
	if len(got)-4 != 19 {
		t.Fatalf("摘掉后来新增的 %q / %q / %q / %q 后命令数 = %d，期望 19（M3 期 18 + T-…-058 新增 1）",
			m4LaterAdded, m5LaterAdded, m5LaterAdded2, m5LaterAdded3, len(got)-4)
	}

	// ③ 前 18 条逐字等于 M3 基线（次序与名称都不许动）。
	for i, want := range m3Baseline {
		if got[i].Name != want {
			t.Fatalf("第 %d 条命令 = %q，期望 %q（M4 只准在尾部追加，不准重排既有条目）",
				i+1, got[i].Name, want)
		}
	}
	// ② 差集恰一条且逐字是 reconcile。
	base := map[string]bool{}
	for _, n := range m3Baseline {
		base[n] = true
	}
	var added []string
	for _, c := range got {
		if base[c.Name] || c.Name == m4LaterAdded || c.Name == m5LaterAdded || c.Name == m5LaterAdded2 || c.Name == m5LaterAdded3 {
			continue
		}
		added = append(added, c.Name)
	}
	if len(added) != m4AddedCount {
		t.Fatalf("相对 M3 基线、摘掉 %q / %q / %q / %q 后新增 %v（%d 条），期望恰 %d 条",
			m4LaterAdded, m5LaterAdded, m5LaterAdded2, m5LaterAdded3, added, len(added), m4AddedCount)
	}
	if added[0] != m4Added {
		t.Fatalf("新增命令名 = %q，期望逐字 %q", added[0], m4Added)
	}
	// T-…-058 那一条必须紧跟 M3 基线（既有 18 条次序未动，新命令一律追加在尾部）。
	if got[len(m3Baseline)].Name != m4Added {
		t.Fatalf("第 %d 条命令 = %q，期望 %q（M4 第一条新增紧跟 M3 基线）",
			len(m3Baseline)+1, got[len(m3Baseline)].Name, m4Added)
	}

	// 新增那条必须真的可执行：注册了却没 Wire 会让 `eg reconcile` 退 1 并报「实现未挂载」。
	cmd := New().Lookup(m4Added)
	if cmd == nil {
		t.Fatalf("命令 %q 未注册", m4Added)
	}
	if cmd.Handler == nil {
		t.Fatalf("命令 %q 已注册但实现未挂载（wireImplemented 漏了一行）", m4Added)
	}
	if cmd.Placeholder {
		t.Fatalf("命令 %q 不得是占位命令：T-…-058 就是它的实现 task", m4Added)
	}
}

// TestCommandCountTwenty 把 M4 收口值「命令数 20」证成一条加法等式：
// M3 收口基线恰 18 条 + M4 期新增恰 2 条（`reconcile` 属 T-…-058、`check` 属 T-…-059）。
//
// 五件事一起断言：① M4 收口总量恰 20（把 M5 期新增摘掉后复算）；② 前 18 条逐字等于
// M3 基线；③ M4 期差集**恰两条**且顺序逐字 `[reconcile, check]`；④ M4 期尾条逐字是
// `check`；⑤ `check` 已 Wire、非占位，且**没有命令私有 flag**（合同 §13 参数面恰 `[--json]`）。
//
// **T-…-065 重钉说明（2026-09-08）**：`eg index` 注册后磁盘实测总数变成 21，
// 于是本用例改证「把 M5 期新增摘掉之后恰 20」——**20 这个 M4 收口结论一个字不删**，
// 也不再引用 `wantCommandCount`（那是当期过程值，M5 之后不再等于 20）。
// 21 那一格由 TestCommandCountTwentyOne 正面对撞，两条等式互不代替。
func TestCommandCountTwenty(t *testing.T) {
	m3Baseline := []string{
		"init", "config", "capture", "context", "apply",
		"search", "card", "rel", "report",
		"deprecate", "restore", "replaced-by",
		"proposal", "delete", "undelete",
		"mark-reviewed", "unreviewed", "edit",
	}
	// M4 期新增命令名，逐字且**有序**（注册次序 = 交付次序）。
	m4Added := []string{"reconcile", "check"}
	// M5 期新增命令名：本用例只负责 M4 收口那一条等式，摘掉后再复算 20。
	// T-…-068 追加 `bench`、T-…-006-B1a 追加 `opinion` 后这份清单从一条变三条 ——
	// **20 这个 M4 收口结论一个字不删**。
	m5Added := []string{"index", "bench", "opinion"}

	want := len(m3Baseline) + len(m4Added)
	if want != 20 {
		t.Fatalf("加法等式不成立：%d + %d = %d，期望 20", len(m3Baseline), len(m4Added), want)
	}
	if want+len(m5Added) != wantCommandCount {
		t.Fatalf("M4 收口 %d + M5 期新增 %d 与 wantCommandCount = %d 不一致",
			want, len(m5Added), wantCommandCount)
	}

	got := New().Commands()
	if len(got)-len(m5Added) != 20 {
		t.Fatalf("摘掉 M5 期新增 %v 后命令数 = %d，期望 20（M3 期 18 + M4 期 2）",
			m5Added, len(got)-len(m5Added))
	}
	for i, want := range m3Baseline {
		if got[i].Name != want {
			t.Fatalf("第 %d 条命令 = %q，期望 %q（M4 只准在尾部追加，不准重排既有条目）",
				i+1, got[i].Name, want)
		}
	}
	base := map[string]bool{}
	for _, n := range m3Baseline {
		base[n] = true
	}
	later := map[string]bool{}
	for _, n := range m5Added {
		later[n] = true
	}
	var added []string
	for _, c := range got {
		if !base[c.Name] && !later[c.Name] {
			added = append(added, c.Name)
		}
	}
	if strings.Join(added, ",") != strings.Join(m4Added, ",") {
		t.Fatalf("M4 期新增 = %v，期望逐字有序 %v", added, m4Added)
	}
	if n := len(got) - len(m5Added); got[n-1].Name != "check" {
		t.Fatalf("M4 收口时的注册表尾条 = %q，期望 %q（新命令一律追加在尾部）", got[n-1].Name, "check")
	}

	cmd := New().Lookup("check")
	if cmd == nil {
		t.Fatalf("命令 %q 未注册", "check")
	}
	if cmd.Handler == nil {
		t.Fatalf("命令 %q 已注册但实现未挂载（wireImplemented 漏了一行）", "check")
	}
	if cmd.Placeholder {
		t.Fatalf("命令 %q 不得是占位命令：T-…-059 就是它的实现 task", "check")
	}
	// M4 收口时 check 的命令私有 flag 为 0；M6（T-…-074）接入强校验开关后恰 1 个（--strict），
	// 且它对本命令 finding 是恒等变换（升级面 W1~W6 与 R3/R4 结构码不相交）。历史意图保留：
	// check 的参数面仍是**最小面**（除全局 flag 外只此一个只读开关，不新增范围收窄 / 可见性参数）。
	if got := wantFlags["check"]; strings.Join(got, ",") != "strict" {
		t.Fatalf("check 的命令私有 flag = %v，期望恰 [--strict]（M6 强校验开关，全局 flag 另计）", got)
	}
}
