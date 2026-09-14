package main

// 包结构与依赖方向的机器判据（T-evergreen.s1_main_flow-158614-001 Acceptance）。
//
// 断言三条：
//  ① internal/*/doc.go 首行匹配 ^// \[S[1-5]\]（§13 九包 + 脚手架 version 包
//     + M3 起的 S2 提案包，逐包命中）；
//  ② 每个 doc.go 的「允许依赖：」列表与施工索引 §13 表格逐包**相等**；
//  ③ go list -deps ./internal/<pkg> 的实际本仓 internal/ 依赖集合 ⊆ 该包的允许依赖，
//     且 model 的实际依赖为空集（零依赖硬约束）。
//
// M3（T-…-033）追加第四条（§13 表格覆盖不到的 S2 包边界，只加严）：
//  ④ internal/proposal 的首行标注恰为 [S2]、直接依赖 ⊆ proposalAllowedDeps，
//     且 S1 的底层包不得反向依赖它（TestStage2ProposalPackageBoundary）。
//
// ③ 之所以是「⊆」而不是「==」：允许依赖是**上限**，第一批 task（001–008、011）落地后
// plan / report / rules 等包的业务实现尚未进场（T-…-009/010/012–018），其实际依赖必然是
// 允许集合的真子集。等号只在 M1 收口（T-…-018）时才可能成立；本测试同时打印「已声明但
// 尚未实际使用」的差集，作为收口前的进度证据，不以差集非空判失败。

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// §13 的 S1 九包（顺序同施工索引表格）。
var ch13Packages = []string{
	"model", "mdfile", "store", "git", "plan", "report", "cli", "query", "rules",
}

// 脚手架包：§15.2 明确标注「非 §13 的 S1 九包之一」。
var scaffoldPackages = []string{"version"}

// S2 包：M3（T-…-033）落地的提案控制面包。
//
// **重钉理由（事实变了，不是放宽）**：`proposal` 原先钉在 forbiddenPackages 里，依据是
// 「S1–S3 不得存在的包」——那是 S1 时期的事实。提案控制面是 S2 能力，M3 的 T-…-033 起
// 它**必须**存在（提案合同 §7 的落盘形态、A-23 的 CLI 直写例外都以本包为载体）。
// 因此本行把 `proposal` 从「禁止存在」重钉为「存在且边界受钉」：包集合等号照旧成立，
// 并由 TestStage2ProposalPackageBoundary 追加三条 §13 表格覆盖不到的断言（只加严）。
var stage2Packages = []string{"proposal"}

// stage2Consumers 是**恰**允许消费 S2 提案包的 S1 包集合（M3 · T-…-037 重钉）。
//
// **重钉理由（事实变了，不是放宽）**：§13 表格只枚举 S1 九包，`proposal` 不在表内，
// 因此「谁可以 import 它」在表里**无法表达**——T-…-033 落地时它还是一座孤岛，
// 到 M3 的 T-…-037 起提案事实必须被消费：`delete` op 要判提案 `status=approved`
// 与 E7 / E8 / E9 / W9（提案合同 §8.1 第 4 行、§8.2.1），编号只在 `internal/plan` 发
// （`internal/proposal` 不出现任何编号字面量），CLI 层再经 `plan` 间接依赖。
//
// 等号仍是等号：**恰这两个**包可以依赖 proposal，第三个包出现即失败；
// 且下面的反向依赖禁令同时**从 5 个包扩到 7 个**（加上 report / query），是加严不是放宽。
var stage2Consumers = map[string]bool{"plan": true, "cli": true}

// stage2Forbidden 是**不得**（直接或间接）依赖 S2 提案包的 S1 包：
// 底层五包（model / mdfile / store / git / rules）+ 派生两包（report / query）。
// report 组报告、query 读查询，都不得反向依赖提案控制面（A-23：Git 与报告由 CLI 层负责）。
var stage2Forbidden = []string{"model", "mdfile", "store", "git", "rules", "report", "query"}

// proposalAllowedDeps 是 internal/proposal 允许直接 import 的本仓 internal/ 包。
//
// 依据 A-23（`docs/specs/2026-10-13-m3-prestart-adjudication.md` §7）与提案合同 §8.5.1：
// 提案包只做落盘形态与控制面，**不得**反向依赖 ChangePlan 展开包（`plan`）、
// 不得自己发 commit（`git`）或组报告（`report`）、不得反向依赖 CLI 与查询层。
var proposalAllowedDeps = map[string]bool{"model": true, "mdfile": true, "store": true}

// S3 包：M4（T-…-049）落地的只读对账检查器包。
//
// **重钉理由（事实变了，不是放宽）**：`reconcile` 原先和 `index` 一起钉在
// forbiddenPackages 里，依据是「M3 不做对账」——那是 M3 时期的事实。对账是 S3 能力，
// M4 的 T-…-049 起它**必须**存在（对账合同 §1 的包边界、T-…-048 裁决 §2 的
// 「谁检查 / 谁写盘 / 谁 commit」三段定义都以本包为载体）。因此本行把 `reconcile`
// 从「禁止存在」重钉为「存在且边界受钉」：包集合等号照旧成立，并由
// TestStage3ReconcilePackageBoundary 追加三条 §13 表格覆盖不到的断言（只加严）。
// `index`（S4 索引）在 M1–M4 期**逐名反证不存在**；自 M5 · T-…-065 起它同样被重钉为
// 「存在且边界受钉」，见下面的 stage4Packages。
var stage3Packages = []string{"reconcile"}

// reconcileAllowedDeps 是 internal/reconcile 允许直接 import 的本仓 internal/ 包。
//
// 依据对账合同 §1.2：只允许 `reconcile` → {`model`, `mdfile`, `git`（**只读 API**）, `query`}；
// 禁止 `reconcile` → {`store`, `plan`, `cli`, `report`, `proposal`}——写盘走 plan → store，
// commit 由 CLI 层负责，本包零写盘、零 commit。
var reconcileAllowedDeps = map[string]bool{
	"model": true, "mdfile": true, "git": true, "query": true,
}

// stage3Consumers 是**恰**允许消费 S3 对账包的 S1 包集合（M4 · T-…-051 重钉）。
//
// **重钉理由（事实变了，不是放宽）**：原式把 §13 九包**全部**列进反向依赖禁令，依据是
// T-…-049 落地时对账包还是一座孤岛（检查器已在盘、消费方一个都还没落地）。但合同
// §1.2 的反向禁令原文恰是四个包 —— `query` / `store` / `plan` / `proposal` → `reconcile`
// 成环，**`cli` 不在其内**；且 §1.3 写口归属表第 2 行明文把 R2 的 `reviewed_at` 补齐交给
// 「命令层把修复意向翻成内存 ChangePlan」，§0.1 第 3 条的反证更逐字把命令层的消费面
// 限定在 `internal/cli/(reconcile*|check*).go` 这几个文件里。因此本行把「九包全禁」
// 重钉为「恰一个消费方 + 文件级位置锁」：
//
//   - 反向依赖禁令仍覆盖合同逐字点名的四个包（另加 report / rules / mdfile / model
//     等底层包，共 8 个），一格不放宽；
//   - 新增两条更强的判据：消费面**等号**（恰 cli 一个包，第二个包出现即红）与
//     **文件级位置锁**（cli 内只有 reconcile* / check* 文件可以 import 对账包 ——
//     这正是「对账不作为写命令前置」的机器形态，比包级禁令更贴合同）。
var stage3Consumers = map[string]bool{"cli": true}

// stage3Forbidden 是**不得**（直接或间接）依赖 S3 对账包的包：§13 九包里除消费方之外的
// 全部 + S2 提案包逐名在册。任何这些包反向 import `reconcile` 即成环（合同 §1.2 第 3 条）。
var stage3Forbidden = stage3ForbiddenList()

func stage3ForbiddenList() []string {
	out := []string{}
	for _, pkg := range ch13Packages {
		if !stage3Consumers[pkg] {
			out = append(out, pkg)
		}
	}
	return append(out, stage2Packages...)
}

// reconcileConsumerFilePrefixes 是命令层里**允许** import 对账包的文件名前缀
// （合同 §0.1 第 3 条：`internal/cli/(reconcile|reconcile_commit|reconcile_repair_*|check).go`）。
//
// 位置锁的本体是「对账不作为写命令前置」：`eg capture` / `eg apply` / `eg edit` /
// `eg delete` / `eg rel` 等写命令所在的文件**一个都不得**出现对账包。
var reconcileConsumerFilePrefixes = []string{"reconcile", "check"}

// S4 包：M5（T-…-065）落地的派生索引包。
//
// **重钉理由（事实变了，不是放宽）**：`index` 原先钉在 forbiddenPackages 里，依据是
// 「本仓任何阶段都不落地 S4 索引面」——那是 M1–M4 时期的事实。M5 的 T-…-064 冻结了
// M5 索引架构合同（A-41 纯 Go SQLite / A-43 `.index/eg.db` 单文件布局 / A-48 命令面），
// T-…-065 起本包**必须**存在。因此本行把 `index` 从「禁止存在」重钉为「存在且边界受钉」：
// 包集合等号照旧成立，并由 TestStage4IndexPackageBoundary 追加四条断言（只加严）。
//
// `forbiddenPackages` 随之变成**空集**：这不是放宽 —— 「不得存在的包」这一格的强度改由
// 「包集合等号 + 每个非 §13 包都必须有一条专属边界用例」承担，谁凭空多造一个包，
// TestInternalPackageSetIsExactlyCh13 的等号当场判红。
var stage4Packages = []string{"index"}

// indexAllowedDeps 是 internal/index 允许直接 import 的本仓 internal/ 包。
//
// 依据 M5 索引架构合同 §1.1 三条最高约束 + 施工索引 §13 依赖禁令第一条
// （**index 不得依赖 store**）：索引只吃「调用方已经读好的事实」（中性快照 Snapshot），
// 绝不自己解析权威 Markdown、不自己算第二套 hash、不写盘除 `.index/` 之外的任何路径。
// `model` / `mdfile` 是**上限**而非下限：本包实测零本仓依赖，⊆ 判据因此天然成立。
var indexAllowedDeps = map[string]bool{"model": true, "mdfile": true}

// stage4Consumers 是**恰**允许（直接或间接）依赖 S4 索引包的 §13 / S2 / S3 包集合
// （M5 · T-…-065 首钉；T-…-067 重钉）。
//
// **重钉理由（事实变了，不是放宽）**：T-…-065 时恰 `cli` 一个，依据是「读路径本阶段仍走
// 全量 Markdown 扫描，读路径接入索引属后续 task」——那是 065 / 066 时期的事实。
// T-…-067 起 `search` / `card show` / `rel` 三条读路径经 `SelectBackend` 消费派生索引
// （索引只出候选集，权威仍是 Markdown），因此第二个**直接**消费方 `query` 必须在册；
// `reconcile` 是**传递**在册（它 import query，本身一行索引代码都没有，见下面的直接消费面等号）。
//
// 等号仍是等号：第四个包出现即判红；且直接消费面由 stage4DirectConsumers 单独钉死，
// 传递在册不等于可以直接 import ——「谁能直接开库」这条线一格未松。
var stage4Consumers = map[string]bool{"cli": true, "query": true, "reconcile": true}

// stage4DirectConsumers 是**恰**允许直接 import 索引包的包集合（M5 · T-…-067 新增，只加严）。
//
// 恰两个：`cli`（建 / 修 / 体检索引的唯一命令层）与 `query`（只读消费候选集的唯一读路径层）。
// 有了这一格，stage4Consumers 里因传递依赖而在册的包（如 `reconcile`）**不会**顺带获得
// 「可以直接开库」的许可。
var stage4DirectConsumers = map[string]bool{"cli": true, "query": true}

// indexConsumerFilePrefixes 是命令层里**允许** import 索引包的文件名前缀。
//
// 位置锁的本体是「索引不作为任何命令的前置」：`eg capture` / `eg apply` / `eg search` /
// `eg card show` / `eg rel` 等命令所在的文件**一个都不得**出现索引包 —— 这一条 T-…-067
// 一格未改：读路径接索引发生在 `internal/query`（取数层），命令层依旧只经 query 取数，
// 因此 `internal/cli/search.go` / `card.go` / `relation.go` 仍然零索引 import。
//
// **M5 · T-…-069 具名扩列一项（登记 T-068 的既成事实，手法与 m5Landed 逐字相同）**：
// 新增前缀 `bench`，唯一成员 `internal/cli/bench.go`。事实基础是 M5 索引架构合同 §7.6 /
// A-46：`eg bench` 的五键里 `index_build_ms` 与 `index_incremental_ms` 是**索引构建**
// 两指标，采样必须直接驱动索引包（`index.Build` / `index.Sync` / `index.NewScratch`），
// 不经索引包无从取得该数值。
//
// 这是**扩列而非放宽**，位置锁的语义一格未松：
//   - `bench` 是**诊断/采样**命令，不是任何读写命令的前置——它自身不被任何命令依赖，
//     因此「索引不作为任何命令的前置」这条边界仍然成立；
//   - 白名单仍是封闭的具名前缀集合（恰 `index` / `bench` 两项），命令层其余文件
//     （`search.go` / `card.go` / `relation.go` / `capture.go` / `apply.go` …）出现索引
//     import 依旧当场判红；
//   - 本项不引入任何新命令、不改 065~068 的实现语义。
var indexConsumerFilePrefixes = []string{"index", "bench"}

// queryIndexConsumerFilePrefixes 是取数层里**允许** import 索引包的文件名前缀
// （M5 · T-…-067 新增，只加严）。
//
// 恰三个具名前缀：`backend`（后端选择单点）、`index_backed`（索引后端）、`degrade`（降级落点）。
// 位置锁的本体是「读路径的其余文件只认扫描底座」：`search.go` / `card.go` / `relation.go` /
// `scan.go` / `context.go` / `filter` 等**一个都不得**出现索引包 —— 索引事实只能经
// SelectBackend / loadVault 这两个入口进来，不许在业务文件里各自开库。
var queryIndexConsumerFilePrefixes = []string{"backend", "index_backed", "degrade"}

// S5 包：M6（T-…-071）落地的强原子事务「锁 + 事务日志」包。
//
// **重钉理由（事实变了，不是放宽）**：`txn` 在 M1–M5 期**逐名反证不存在**（它一直被
// 「包集合等号 + 每个非 §13 包都必须有专属边界用例」这一格挡着）。M6 的 T-…-070 冻结了
// M6 原子性与强校验合同（§2 事务边界 / §3 `run.lock` 形态 / §4 `.index/txn/` 布局与
// intent 发布屏障 / §12 诊断码），T-…-071 起本包**必须**存在。因此本行把 `txn` 从
// 「凭空多造即判红」重钉为「存在且边界受钉」：包集合等号照旧成立，并由
// TestStage5TxnPackageBoundary 追加五条 §13 表格覆盖不到的断言（只加严）。
var stage5Packages = []string{"txn"}

// txnAllowedDeps 是 internal/txn 允许直接 import 的本仓 internal/ 包：**空集**。
//
// 依据 M6 合同 §16.5 与技术方案 §13：txn 只吃 Go 标准库（含 syscall），零本仓依赖 ——
// 锁 / txn_id / 事务日志全部自持，`.index` / `run.lock` / `txn` 三个字面量在本包内自持、
// **不**向索引包借。因此 doc.go 的「允许依赖：」行不出现任何 §13 包名，declared 恒为空集。
var txnAllowedDeps = map[string]bool{}

// txnForbiddenDeps 是合同 §16.5 / 技术方案 §13 **逐字点名**、txn 不得（直接或间接）
// 依赖的五包：写命令层（cli）、读查询层（query）、派生索引（index）、报告组装（report）、
// 只读对账（reconcile）。txn 是最底层的原子事务基座，反向依赖其中任何一个都会破坏依赖单向性。
var txnForbiddenDeps = []string{"cli", "query", "index", "report", "reconcile"}

// stage5Consumers 是 **T-…-072 起** 恰允许（直接或间接）依赖 txn 的包集合：恰 `{cli}`。
//
// **重钉理由（事实变了，不是放宽）**：T-071 只落地 internal/txn 包本体（锁 API + txn_id
// 分配 + 事务日志骨架），**尚未**接入任何产品路径，因此当时的等号是**空集**。T-…-072
// 的职责正是「多文件原子提交与崩溃恢复」——把 `run.lock` / `Recover` / `WriteIntent` /
// `Commit` 接进 plan-based 写命令的关键区（M6 合同 §2 事务边界 / §4 intent 发布屏障）。
// 事务编排只能发生在命令层：它是唯一同时握有 vault 路径、plan 预演结果与报告对象的一层。
// 因此本行把等号从 `{}` 精确重钉为 `{cli: true}`。
//
// 等号仍是等号，一格未松：
//   - 恰一个成员。§13 九包（含 store / plan / query / report …）+ S2 / S3 / S4 包里
//     **其余任何一个**出现 txn 依赖（直接或传递）都当场判红；
//   - 方向仍单向。txn 自身的零本仓依赖（txnAllowedDeps 空集）、逐名禁令
//     （txnForbiddenDeps：cli / query / index / report / reconcile）都原封不动 ——
//     `cli → txn` 合法，`txn → cli` 依旧判红，两侧同时钉死才不成环；
//   - 退出码 5 映射仍属 T-…-074，本格只表达「谁可以 import」，不表达「谁可以 fail-closed」。
//
// 与 doc.go 的分工：M6 专属消费面由本处的**等号**表达，不塞进 internal/cli/doc.go 的
// 旧 §13「以上全部」——那一行的语义是「§13 九包全集」，而 txn 不在 §13 表内
// （手法与 proposal / reconcile / index 三个 stage 逐字相同）。
var stage5Consumers = map[string]bool{"cli": true}

// S1–S5 都不得存在的包：**空集**（见 stage4Packages 的重钉说明）。
// 保留这一格与遍历它的反证代码：下一次「某能力被判为永不落地」时直接在此登记即可。
var forbiddenPackages = []string{}

const designDoc = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/" +
	"2026-08-31-evergreen-s1-tech-design.md"

var firstLineRE = regexp.MustCompile(`^// \[S[1-5]\]`)

// parseDeps 把一段「允许依赖」描述归一化成包名集合。
// 同一个函数同时用于 doc.go 的「允许依赖：…」与 §13 表格箭头右侧，保证两边口径一致。
//   - "无（零依赖…）" / "零依赖"          → 空集
//   - "以上全部（…）"                     → 除 self 外的全部 §13 包
//   - "model（S1 直连 mdfile）"           → {model, mdfile}
func parseDeps(self, text string) map[string]bool {
	got := map[string]bool{}
	if strings.Contains(text, "以上全部") {
		for _, p := range ch13Packages {
			if p != self {
				got[p] = true
			}
		}
		return got
	}
	for _, p := range ch13Packages {
		if p == self {
			continue
		}
		if strings.Contains(text, p) {
			got[p] = true
		}
	}
	return got
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// declaredDeps 读 internal/<pkg>/doc.go：返回首行与「允许依赖」集合。
func declaredDeps(t *testing.T, pkg string) (string, map[string]bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../internal", pkg, docFile(pkg)))
	if err != nil {
		t.Fatalf("读取 internal/%s 的包注释文件失败：%v", pkg, err)
	}
	lines := strings.Split(string(raw), "\n")
	var declared string
	for _, l := range lines {
		if strings.Contains(l, "允许依赖：") {
			declared = l[strings.Index(l, "允许依赖：")+len("允许依赖："):]
			break
		}
	}
	if declared == "" {
		t.Fatalf("internal/%s 的包注释缺少「允许依赖：」行", pkg)
	}
	return strings.TrimRight(lines[0], "\r"), parseDeps(pkg, declared)
}

// docFile 返回承载包注释的文件名：§13 九包统一为 doc.go，脚手架 version 包为 version.go。
func docFile(pkg string) string {
	if pkg == "version" {
		return "version.go"
	}
	return "doc.go"
}

// ch13Table 解析施工索引 §13 代码块，返回「包名 → 允许依赖集合」。
// 依赖箭头一律取**最后一个 `]` 之后**的部分：plan 行的说明文字里也有 `→`（"op → store 展开"），
// 只有 `[阶段]` 标注之后的那个箭头才是依赖方向。
func ch13Table(t *testing.T) map[string]map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(designDoc)
	if err != nil {
		t.Skipf("施工索引不可读（%v）：§13 表格比对跳过；本用例要求 teamwork/ 与 evergreen/ 同级", err)
	}
	table := map[string]map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		for _, pkg := range ch13Packages {
			if !strings.HasPrefix(trimmed, pkg+"/") {
				continue
			}
			if !strings.Contains(trimmed, "[S") {
				continue
			}
			cell := trimmed[strings.LastIndex(trimmed, "]")+1:]
			table[pkg] = parseDeps(pkg, cell)
		}
	}
	if len(table) != len(ch13Packages) {
		seen := map[string]bool{}
		for k := range table {
			seen[k] = true
		}
		t.Fatalf("§13 表格解析到 %d 个包（%v），期望 %d 个",
			len(table), sortedKeys(seen), len(ch13Packages))
	}
	return table
}

// directDeps 返回该包 **直接 import** 的本仓 internal/ 包（§13 表格就是直接依赖表：
// store → model mdfile git 指的是 store 自己 import 这三个）。
func directDeps(t *testing.T, pkg string) (map[string]bool, bool) {
	t.Helper()
	return listDeps(t, pkg, `{{join .Imports "\n"}}`)
}

// actualDeps 返回 go list -deps 结果里属于本仓 internal/ 的依赖包（**传递闭包**，排除 self 与脚手架 version）。
func actualDeps(t *testing.T, pkg string) (map[string]bool, bool) {
	t.Helper()
	return listDeps(t, pkg, "")
}

func listDeps(t *testing.T, pkg, format string) (map[string]bool, bool) {
	t.Helper()
	args := []string{"list"}
	if format == "" {
		args = append(args, "-deps")
	} else {
		args = append(args, "-f", format)
	}
	args = append(args, "./internal/"+pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	got := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "github.com/ikaqiu-Lemon/EverGreen/internal/") {
			continue
		}
		name := strings.TrimPrefix(l, "github.com/ikaqiu-Lemon/EverGreen/internal/")
		name = ch13Owner(name)
		if name == pkg || name == "version" {
			continue
		}
		got[name] = true
	}
	return got, true
}

// subpackageOwners 是「§13 九包内部的子包 → 所属 §13 包」的**具名**收窄表。
//
// 为什么需要它：§13 表格枚举的是**九个包**，`internal/query/filter`（ADR-20 的文件级隔离
// 落点，M3 · T-…-042）是 `query` 包树内部的子包，不是第十个包——它在 §13 里没有独立一行，
// 因此依赖比对必须把它归到 `query` 名下，否则等号会被一个「表里根本不存在的名字」判红。
// 表是**逐名**列出的：多出任何未登记的子包仍会以原名参与比对并失败，这是收窄不是放宽。
// 子包自身的依赖方向禁令（谁不得导入 query/filter）由 internal/query 的
// TestADR20_FilterIsolation 单独反证，与本表无关。
var subpackageOwners = map[string]string{
	"query/filter": "query",
	// query/queryset（system_assurance 批次 A）：M5 性能采样的固定查询集，历史上误放在
	// `test/perf/queryset` 却被产品代码 internal/cli/bench.go import —— 被产品 import 的包
	// 按定义是产品代码，测试树外置后旧位置会让产品树无法独立构建（I-…-001）。归位时选择
	// `internal/query` 名下的**数据子包**而不是新建第十个包：它零依赖（只 fmt/strconv）、
	// 语义属查询域，且不改变 §13 的九包等号。与 query/filter 同样按子包归一到 query 名下。
	"query/queryset": "query",
}

// ch13Owner 把子包名归一到 §13 表格里的包名（不在表内的名字逐字返回）。
func ch13Owner(name string) string {
	if owner, ok := subpackageOwners[name]; ok {
		return owner
	}
	return name
}

// TestInternalPackageSetIsExactlyCh13 反证包集合没有被悄悄扩张 / 缩小。
//
// 期望集合 = §13 九包 + 脚手架 version + **S2 的 proposal**（M3 起）+ **S3 的 reconcile**
// （M4 · T-…-049 起）+ **S4 的 index**（M5 · T-…-065 起）+ **S5 的 txn**（M6 · T-…-071 起）。
// 等号仍是等号：多一个包或少一个包都失败；forbiddenPackages 现为空集，凭空多造的包一律
// 由这条等号当场抓住。
func TestInternalPackageSetIsExactlyCh13(t *testing.T) {
	entries, err := os.ReadDir("../../internal")
	if err != nil {
		t.Fatalf("读 internal/ 失败：%v", err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			got[e.Name()] = true
		}
	}
	want := map[string]bool{}
	for _, p := range allPackages() {
		want[p] = true
	}
	if strings.Join(sortedKeys(got), ",") != strings.Join(sortedKeys(want), ",") {
		t.Fatalf("internal/ 包集合 = %v，期望 %v", sortedKeys(got), sortedKeys(want))
	}
	for _, p := range forbiddenPackages {
		if got[p] {
			t.Fatalf("internal/%s 被判为任何阶段都不得创建的包，却已在盘", p)
		}
	}
}

// allPackages 返回 internal/ 下应当存在的全部包名（§13 九包 + 脚手架 + S2 包 + S3 包 + S4 包 + S5 包）。
func allPackages() []string {
	out := append([]string{}, ch13Packages...)
	out = append(out, scaffoldPackages...)
	out = append(out, stage2Packages...)
	out = append(out, stage3Packages...)
	out = append(out, stage4Packages...)
	return append(out, stage5Packages...)
}

// TestDocGoFirstLineDeclaresStage 断言 ① 首行阶段标注。
func TestDocGoFirstLineDeclaresStage(t *testing.T) {
	for _, pkg := range allPackages() {
		first, _ := declaredDeps(t, pkg)
		if !firstLineRE.MatchString(first) {
			t.Errorf("internal/%s 首行 = %q，不匹配 %s", pkg, first, firstLineRE)
		}
	}
}

// TestStage2ProposalPackageBoundary 钉住 S2 提案包的边界（M3 · T-…-033 新增，只加严）。
//
// §13 表格只覆盖 S1 九包，proposal 不在表内，因此它的边界必须在这里单独钉死。三条：
//  1. doc.go 首行阶段标注恰为 [S2]（不是把 S2 能力伪装成 S1）；
//  2. 直接 import 的本仓 internal/ 包 ⊆ proposalAllowedDeps ——
//     A-23：不得反向依赖 ChangePlan 展开包，Git 与报告由 CLI 层负责；
//  3. S1 九包中的底层包（model / mdfile / store / git / rules）**不得**反向依赖 proposal，
//     否则 S1 的依赖方向会被 S2 能力污染。
func TestStage2ProposalPackageBoundary(t *testing.T) {
	for _, pkg := range stage2Packages {
		first, _ := declaredDeps(t, pkg)
		if !strings.HasPrefix(first, "// [S2]") {
			t.Errorf("internal/%s 首行 = %q，S2 包必须标注 [S2]", pkg, first)
		}
		direct, ok := directDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过 S2 包边界比对")
		}
		for dep := range direct {
			if !proposalAllowedDeps[dep] {
				t.Errorf("internal/%s 直接 import internal/%s，允许依赖只有 %v（A-23 / 提案合同 §8.5.1）",
					pkg, dep, sortedKeys(proposalAllowedDeps))
			}
		}
	}
	for _, low := range stage2Forbidden {
		actual, ok := actualDeps(t, low)
		if !ok {
			t.Skipf("go list 不可用，跳过反向依赖比对")
		}
		for _, s2 := range stage2Packages {
			if actual[s2] {
				t.Errorf("internal/%s 反向依赖 S2 包 internal/%s：S1 的依赖方向不得被 S2 能力污染", low, s2)
			}
		}
	}
	// 消费面等号：§13 九包里恰 stage2Consumers 的成员可以依赖 proposal（M3 · T-…-037）。
	for _, pkg := range ch13Packages {
		actual, ok := actualDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过 S2 消费面等号比对")
		}
		for _, s2 := range stage2Packages {
			if actual[s2] != stage2Consumers[pkg] {
				t.Errorf("internal/%s 依赖 internal/%s = %v，但允许的消费面恰为 %v",
					pkg, s2, actual[s2], sortedKeys(stage2Consumers))
			}
		}
	}
}

// TestStage3ReconcilePackageBoundary 钉住 S3 对账包的边界（M4 · T-…-049 新增，只加严）。
//
// §13 表格只覆盖 S1 九包，reconcile 不在表内，因此它的边界必须在这里单独钉死。四条：
//  1. doc.go 首行阶段标注恰为 [S3]（不是把 S3 能力伪装成 S1 / S2）；
//  2. doc.go 声明的允许依赖**恰等于** reconcileAllowedDeps（合同 §1.2 的四个包，不多不少）；
//  3. 直接 import 的本仓 internal/ 包 ⊆ reconcileAllowedDeps ——
//     禁止 store / plan / cli / report / proposal：写盘走 plan → store，commit 由 CLI 层负责；
//  4. §13 九包 + S2 提案包**一个都不得**反向依赖 reconcile（反向依赖即成环，合同 §1.2 第 3 条）。
func TestStage3ReconcilePackageBoundary(t *testing.T) {
	for _, pkg := range stage3Packages {
		first, declared := declaredDeps(t, pkg)
		if !strings.HasPrefix(first, "// [S3]") {
			t.Errorf("internal/%s 首行 = %q，S3 包必须标注 [S3]", pkg, first)
		}
		if strings.Join(sortedKeys(declared), ",") != strings.Join(sortedKeys(reconcileAllowedDeps), ",") {
			t.Errorf("internal/%s：doc.go 声明 %v，合同 §1.2 允许依赖 %v，两者必须相等",
				pkg, sortedKeys(declared), sortedKeys(reconcileAllowedDeps))
		}
		direct, ok := directDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过 S3 包边界比对")
		}
		for dep := range direct {
			if !reconcileAllowedDeps[dep] {
				t.Errorf("internal/%s 直接 import internal/%s，允许依赖只有 %v（对账合同 §1.2）",
					pkg, dep, sortedKeys(reconcileAllowedDeps))
			}
		}
	}
	for _, other := range stage3Forbidden {
		actual, ok := actualDeps(t, other)
		if !ok {
			t.Skipf("go list 不可用，跳过 S3 反向依赖比对")
		}
		for _, s3 := range stage3Packages {
			if actual[s3] {
				t.Errorf("internal/%s 反向依赖 S3 包 internal/%s：依赖方向必须单向（对账合同 §1.2 第 3 条）",
					other, s3)
			}
		}
	}
	// 消费面等号：§13 九包里恰 stage3Consumers 的成员可以依赖 reconcile（M4 · T-…-051）。
	for _, pkg := range ch13Packages {
		actual, ok := actualDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过 S3 消费面等号比对")
		}
		for _, s3 := range stage3Packages {
			if actual[s3] != stage3Consumers[pkg] {
				t.Errorf("internal/%s 依赖 internal/%s = %v，但允许的消费面恰为 %v",
					pkg, s3, actual[s3], sortedKeys(stage3Consumers))
			}
		}
	}
	// 文件级位置锁：命令层只有 reconcile* / check* 文件可以 import 对账包
	// （合同 §0.1 第 3 条「对账不作为写命令前置」的机器形态）。
	for _, f := range reconcileImportingFiles(t, "cli") {
		if !hasAnyPrefix(f, reconcileConsumerFilePrefixes) {
			t.Errorf("internal/cli/%s import 了对账包：只有 %v 前缀的文件可以消费它"+
				"（对账不作为写命令前置，合同 §0.1 第 3 条）", f, reconcileConsumerFilePrefixes)
		}
	}
}

// reconcileImportingFiles 列出 internal/<pkg> 下**逐文件**出现对账包导入路径的文件名。
func reconcileImportingFiles(t *testing.T, pkg string) []string {
	t.Helper()
	return importingFiles(t, pkg, "github.com/ikaqiu-Lemon/EverGreen/internal/reconcile")
}

// importingFiles 列出 internal/<pkg> 下**逐文件**出现给定导入路径的文件名（含测试文件）。
func importingFiles(t *testing.T, pkg, importPath string) []string {
	t.Helper()
	dir := filepath.Join("..", "..", "internal", pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("读 %s 失败：%v", e.Name(), err)
		}
		if strings.Contains(string(raw), "\""+importPath+"\"") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// TestStage4IndexPackageBoundary 钉住 S4 派生索引包的边界（M5 · T-…-065 新增，只加严）。
//
// §13 表格只覆盖 S1 九包，`index` 不在表内，因此它的边界必须在这里单独钉死。六条：
//  1. doc.go 首行阶段标注恰为 `[S4]`（不把 S4 能力伪装成 S1–S3）；
//  2. doc.go 声明的允许依赖**恰等于** indexAllowedDeps（施工索引 §13 禁令第一条：不依赖 store）；
//  3. 直接 import 的本仓 internal/ 包 ⊆ indexAllowedDeps ——
//     禁止 store / plan / cli / report / query / proposal / reconcile：
//     索引只吃调用方喂进来的中性快照，绝不自己解析权威 Markdown；
//  4. §13 九包 + S2 / S3 包里，除 stage4Consumers 之外**一个都不得**依赖 index（消费面等号）；
//  5. 文件级位置锁：命令层只有 `index*` 文件可以 import 索引包 ——
//     这正是「索引不作为任何命令的前置」的机器形态；
//  6. 索引包不得 `os/exec`（合同 §13 禁令：不外调 sqlite3 / git 等子进程）。
func TestStage4IndexPackageBoundary(t *testing.T) {
	for _, pkg := range stage4Packages {
		first, declared := declaredDeps(t, pkg)
		if !strings.HasPrefix(first, "// [S4]") {
			t.Errorf("internal/%s 首行 = %q，S4 包必须标注 [S4]", pkg, first)
		}
		if strings.Join(sortedKeys(declared), ",") != strings.Join(sortedKeys(indexAllowedDeps), ",") {
			t.Errorf("internal/%s：doc.go 声明 %v，M5 合同允许依赖 %v，两者必须相等",
				pkg, sortedKeys(declared), sortedKeys(indexAllowedDeps))
		}
		direct, ok := directDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过 S4 包边界比对")
		}
		for dep := range direct {
			if !indexAllowedDeps[dep] {
				t.Errorf("internal/%s 直接 import internal/%s，允许依赖只有 %v（M5 索引架构合同 §1.1）",
					pkg, dep, sortedKeys(indexAllowedDeps))
			}
		}
	}
	// ④ 消费面等号：§13 九包 + S2 / S3 包里恰 stage4Consumers 的成员可以依赖 index。
	var others []string
	others = append(others, ch13Packages...)
	others = append(others, stage2Packages...)
	others = append(others, stage3Packages...)
	for _, pkg := range others {
		actual, ok := actualDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过 S4 消费面等号比对")
		}
		for _, s4 := range stage4Packages {
			if actual[s4] != stage4Consumers[pkg] {
				t.Errorf("internal/%s 依赖 internal/%s = %v，但允许的消费面恰为 %v",
					pkg, s4, actual[s4], sortedKeys(stage4Consumers))
			}
		}
	}
	// ④' 直接消费面等号（T-…-067 新增，只加严）：恰 stage4DirectConsumers 的成员可以
	// **直接** import 索引包；传递在册的包（如 reconcile）不得直接开库。
	for _, pkg := range others {
		direct, ok := directDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过 S4 直接消费面等号比对")
		}
		for _, s4 := range stage4Packages {
			if direct[s4] != stage4DirectConsumers[pkg] {
				t.Errorf("internal/%s 直接 import internal/%s = %v，但允许直接消费的恰为 %v",
					pkg, s4, direct[s4], sortedKeys(stage4DirectConsumers))
			}
		}
	}
	// ⑤ 文件级位置锁：命令层只有 index* 文件可以 import 索引包
	// （「索引不作为任何命令的前置」的机器形态）。
	for _, f := range importingFiles(t, "cli", "github.com/ikaqiu-Lemon/EverGreen/internal/index") {
		if !hasAnyPrefix(f, indexConsumerFilePrefixes) {
			t.Errorf("internal/cli/%s import 了索引包：只有 %v 前缀的文件可以消费它"+
				"（索引不作为任何命令的前置，M5 索引架构合同 §1.1）", f, indexConsumerFilePrefixes)
		}
	}
	// ⑤' 取数层的文件级位置锁（T-…-067 新增，只加严）：只有 backend / index_backed /
	// degrade 三个具名前缀的文件可以 import 索引包，读路径业务文件一律零索引。
	for _, f := range importingFiles(t, "query", "github.com/ikaqiu-Lemon/EverGreen/internal/index") {
		if !hasAnyPrefix(f, queryIndexConsumerFilePrefixes) {
			t.Errorf("internal/query/%s import 了索引包：只有 %v 前缀的文件可以消费它"+
				"（索引事实只经 SelectBackend / loadVault 进入读路径，M5 索引架构合同 §5.2）",
				f, queryIndexConsumerFilePrefixes)
		}
	}
	// ⑥ 索引包零子进程：SQLite 是**进程内**纯 Go 库，出现 os/exec 说明有人在外调命令行工具。
	if hits := importingFiles(t, "index", "os/exec"); len(hits) > 0 {
		t.Errorf("internal/index 出现 os/exec（%v）：索引必须进程内完成，不得外调子进程", hits)
	}
}

// TestStage5TxnPackageBoundary 钉住 S5 强原子事务包的边界（M6 · T-…-071 新增，只加严）。
//
// §13 表格只覆盖 S1 九包，`txn` 不在表内，因此它的边界必须在这里单独钉死。五条：
//  1. doc.go 首行阶段标注恰为 `[S5]`（不把 S5 能力伪装成 S1–S4）；
//  2. doc.go 声明的允许依赖**恰等于** txnAllowedDeps（空集：合同 §16.5 的零本仓依赖）；
//  3. 直接 import 的本仓 internal/ 包 ⊆ txnAllowedDeps，且**本仓传递依赖恰为空集**
//     （内部依赖闭合：txn 只吃 Go 标准库，一行本仓依赖都没有）；
//  4. 传递闭包里**逐名**不得出现 cli / query / index / report / reconcile
//     （合同 §16.5 依赖禁令：txn 是最底层事务基座，反向依赖任一即破坏依赖单向性）；
//  5. 低层包不得反向依赖 txn（消费面等号恰为 stage5Consumers = `{cli}`：T-…-072 把事务
//     编排接进命令层关键区，除 cli 外 §13 九包 + S2 / S3 / S4 包里任何一个依赖 txn 都判红）。
func TestStage5TxnPackageBoundary(t *testing.T) {
	for _, pkg := range stage5Packages {
		first, declared := declaredDeps(t, pkg)
		if !strings.HasPrefix(first, "// [S5]") {
			t.Errorf("internal/%s 首行 = %q，S5 包必须标注 [S5]", pkg, first)
		}
		if strings.Join(sortedKeys(declared), ",") != strings.Join(sortedKeys(txnAllowedDeps), ",") {
			t.Errorf("internal/%s：doc.go 声明 %v，M6 合同允许依赖 %v，两者必须相等",
				pkg, sortedKeys(declared), sortedKeys(txnAllowedDeps))
		}
		direct, ok := directDeps(t, pkg)
		if !ok {
			t.Fatalf("go list 不可用，无法校验 internal/%s 的直接依赖：S5 边界禁止跳过（本 task 不新增 skip）", pkg)
		}
		for dep := range direct {
			if !txnAllowedDeps[dep] {
				t.Errorf("internal/%s 直接 import internal/%s，允许依赖只有 %v（M6 合同 §16.5：零本仓依赖）",
					pkg, dep, sortedKeys(txnAllowedDeps))
			}
		}
		actual, ok := actualDeps(t, pkg)
		if !ok {
			t.Fatalf("go list 不可用，无法校验 internal/%s 的传递依赖闭合：S5 边界禁止跳过（本 task 不新增 skip）", pkg)
		}
		// ③ 内部依赖闭合：txn 的本仓传递依赖恰为空集。
		if len(actual) != 0 {
			t.Errorf("internal/%s 本仓传递依赖 = %v，M6 合同要求零本仓依赖（内部依赖闭合必须为空集）",
				pkg, sortedKeys(actual))
		}
		// ④ 逐名禁令：cli / query / index / report / reconcile 一个都不得出现在传递闭包里。
		for _, forbidden := range txnForbiddenDeps {
			if actual[forbidden] {
				t.Errorf("internal/%s 依赖 internal/%s：合同 §16.5 逐字禁止 txn 依赖 %v",
					pkg, forbidden, txnForbiddenDeps)
			}
		}
	}
	// ⑤ 反向依赖禁令 + 消费面等号：恰 stage5Consumers（= `{cli}`）的成员可以依赖 txn，
	// §13 九包 + S2 / S3 / S4 包里**其余任何一个**都不得依赖。
	var others []string
	others = append(others, ch13Packages...)
	others = append(others, stage2Packages...)
	others = append(others, stage3Packages...)
	others = append(others, stage4Packages...)
	for _, pkg := range others {
		actual, ok := actualDeps(t, pkg)
		if !ok {
			t.Fatalf("go list 不可用，无法校验 internal/%s 是否反向依赖 txn：S5 边界禁止跳过（本 task 不新增 skip）", pkg)
		}
		for _, s5 := range stage5Packages {
			if actual[s5] != stage5Consumers[pkg] {
				t.Errorf("internal/%s 依赖 internal/%s = %v，但 T-…-072 允许的消费面恰为 %v"+
					"（事务编排只发生在命令层关键区，其余低层包一律不得反向依赖 txn）",
					pkg, s5, actual[s5], sortedKeys(stage5Consumers))
			}
		}
	}
}

// hasAnyPrefix 报告 name 是否以任一前缀开头。
func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// TestDeclaredDepsEqualCh13Table 断言 ② doc.go 声明 == §13 表格。
func TestDeclaredDepsEqualCh13Table(t *testing.T) {
	table := ch13Table(t)
	for _, pkg := range ch13Packages {
		_, declared := declaredDeps(t, pkg)
		want := table[pkg]
		if strings.Join(sortedKeys(declared), ",") != strings.Join(sortedKeys(want), ",") {
			t.Errorf("internal/%s：doc.go 声明 %v，§13 表格 %v，两者必须相等",
				pkg, sortedKeys(declared), sortedKeys(want))
		}
	}
}

// declaredClosure 返回允许依赖表的**传递闭包**：§13 是直接依赖表，
// 「plan → store」自然连带 store 自己的允许依赖（model mdfile git）。
// 闭包只沿允许边扩张，因此仍能抓出任何不在允许图上的间接依赖（例如 plan → report）。
func declaredClosure(t *testing.T, pkg string, table map[string]map[string]bool) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	queue := sortedKeys(table[pkg])
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == pkg || out[cur] {
			continue
		}
		out[cur] = true
		queue = append(queue, sortedKeys(table[cur])...)
	}
	return out
}

// TestActualDepsWithinDeclared 断言 ③ 实际依赖 ⊆ 允许依赖，且 model 零依赖。
//
// 分两层，两层都必须成立（比单层更严，不是放宽）：
//   - **直接 import ⊆ 本包允许依赖**：§13 表格是直接依赖表，据此抓「plan 直连 mdfile」
//     这类跨层直取（plan 只能经 store / query 拿文件结构口径）。
//   - **传递依赖 ⊆ 允许依赖的传递闭包**：抓任何不在允许图上的间接依赖。
//     不能拿本包允许集合直接比传递依赖：plan → store → mdfile 是 §13 明文允许的合法链路。
func TestActualDepsWithinDeclared(t *testing.T) {
	table := ch13Table(t)
	for _, pkg := range ch13Packages {
		_, declared := declaredDeps(t, pkg)
		direct, ok := directDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过实际依赖比对")
		}
		for dep := range direct {
			if stage2Allowed(pkg, dep) || stage3Allowed(pkg, dep) ||
				stage4Allowed(pkg, dep) || stage5Allowed(pkg, dep) {
				continue
			}
			if !declared[dep] {
				t.Errorf("internal/%s 直接 import internal/%s，但允许依赖只有 %v（§13 依赖方向）",
					pkg, dep, sortedKeys(declared))
			}
		}
		actual, ok := actualDeps(t, pkg)
		if !ok {
			t.Skipf("go list 不可用，跳过实际依赖比对")
		}
		closure := declaredClosure(t, pkg, table)
		for dep := range actual {
			if stage2Allowed(pkg, dep) || stage3Allowed(pkg, dep) ||
				stage4Allowed(pkg, dep) || stage5Allowed(pkg, dep) {
				continue
			}
			if !closure[dep] {
				t.Errorf("internal/%s 传递依赖 internal/%s，不在允许依赖闭包 %v 内（§13 依赖方向）",
					pkg, dep, sortedKeys(closure))
			}
		}
		if pkg == "model" && len(actual) != 0 {
			t.Errorf("internal/model 必须零依赖，实测依赖 %v", sortedKeys(actual))
		}
		var pending []string
		for _, dep := range sortedKeys(declared) {
			if !actual[dep] {
				pending = append(pending, dep)
			}
		}
		if len(pending) > 0 {
			t.Logf("internal/%s 已声明但尚未实际使用的依赖（下游 task 落地后收敛）：%v", pkg, pending)
		}
	}
}

// stage2Allowed 报告「pkg 依赖 dep」是否属 M3 明确许可的 S2 消费边
// （dep 是 S2 包 **且** pkg 在 stage2Consumers 内）。§13 表格无法表达 proposal，
// 这条例外由 stage2Consumers 的**等号**与 stage2Forbidden 的禁令双侧钉死，
// 见 TestStage2ProposalPackageBoundary。
// stage3Allowed 报告「pkg 依赖 dep」是否属 M4 明确许可的 S3 消费边
// （dep 是 S3 包 **且** pkg 在 stage3Consumers 内）。§13 表格同样无法表达 reconcile
// （它不在表内），这条例外由 stage3Consumers 的**等号**、stage3Forbidden 的禁令与
// 文件级位置锁三侧钉死，见 TestStage3ReconcilePackageBoundary。
func stage3Allowed(pkg, dep string) bool {
	if !stage3Consumers[pkg] {
		return false
	}
	for _, s3 := range stage3Packages {
		if dep == s3 {
			return true
		}
	}
	return false
}

func stage2Allowed(pkg, dep string) bool {
	if !stage2Consumers[pkg] {
		return false
	}
	for _, s2 := range stage2Packages {
		if dep == s2 {
			return true
		}
	}
	return false
}

// stage4Allowed 报告「pkg 依赖 dep」是否属 M5 明确许可的 S4 消费边
// （dep 是 S4 包 **且** pkg 在 stage4Consumers 内）。§13 表格同样无法表达 index
// （它不在表内），这条例外由 stage4Consumers 的**等号**与文件级位置锁双侧钉死，
// 见 TestStage4IndexPackageBoundary。
func stage4Allowed(pkg, dep string) bool {
	if !stage4Consumers[pkg] {
		return false
	}
	for _, s4 := range stage4Packages {
		if dep == s4 {
			return true
		}
	}
	return false
}

// stage5Allowed 报告「pkg 依赖 dep」是否属 M6 明确许可的 S5 消费边
// （dep 是 S5 包 **且** pkg 在 stage5Consumers 内）。§13 表格同样无法表达 txn
// （它不在表内），这条例外由 stage5Consumers 的**等号**、txnAllowedDeps 的零本仓依赖与
// txnForbiddenDeps 的逐名反向禁令三侧钉死，见 TestStage5TxnPackageBoundary。
//
// 接入位置与 stage2/3/4 逐字相同：只在 TestActualDepsWithinDeclared 的
// 直接 / 传递两处比对里放行**这一条具名边**，其余判定一格不动 ——
// 非 stage5Consumers 成员依赖 txn 仍然落回 declared/closure 比对而判红。
func stage5Allowed(pkg, dep string) bool {
	if !stage5Consumers[pkg] {
		return false
	}
	for _, s5 := range stage5Packages {
		if dep == s5 {
			return true
		}
	}
	return false
}
