// PPE 端到端链路的**离线可复算**用例（T-evergreen.s1_main_flow-158614-028）。
//
// 本文件断言三件事，且只断言这三件事：
//  1. 留痕目录 `testdata/ppe/` 齐备、`plan.json` 可被 `internal/plan` 解析且零 error 级诊断；
//  2. 五步链路（收录 → 查询 → 判断结论落盘 → 写入 → 再查询验证）在临时 vault 里可离线复算，
//     动词序列逐字为 capture → process → relate，写入前后同一条查询一反一正；
//  3. 「计划是否由真实 Agent 生成」这一**外部事实**在留痕与证据报告里的登记彼此同真——
//     即：若 `session.md` 声明真实会话未执行，则 `plan.json` 必须被登记为离线回放基线，
//     且证据报告必须把完成判据 8 记为未达成。**本文件不证明真实 Agent 生成这件事本身。**
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
)

const (
	ppeDir        = "testdata/ppe"
	ppeSrcID      = "s-20260901-verification-the-key-to-ai"
	ppeNewCard    = "k-20260901-verification-principle"
	ppeOldCard    = "k-20260918-scaling-compute"
	ppeRelType    = "limits"
	ppeRelReason  = "验证原则限定了既有卡结论的适用前提：可随算力扩展的通用方法要把算力真正转化为知识规模，前提是这些知识能被系统自身验证；原文指出若错误只能由人发现和纠正，知识系统的规模就受限于人所能监控与理解的范围并长期脆弱，因此算力增长本身不足以保证知识规模增长"
	ppeCapturedAt = "2026-09-01T23:08:01+08:00"
	ppeEvidence   = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/" +
		"2026-10-06-m2-ppe-agent-e2e-evidence.md"
	// ppeNotRunMarker 是「真实 Agent Harness E2E 会话未执行」的唯一登记串（诚实出口）。
	ppeNotRunMarker = "真实 Agent Harness E2E 会话未执行"
)

// ppeFiles 是留痕目录必须齐备的六个文件（缺一即不通过）。
var ppeFiles = []string{"plan.json", "commands.log", "query-before.txt",
	"query-after.txt", "git-log.txt", "session.md"}

func ppeRead(t *testing.T, name string) string {
	t.Helper()
	return readFile(t, filepath.Join(ppeDir, name))
}

// TestPPEEvidenceFilesPresent —— 六个留痕文件全部存在且非空（plan.json ≥ 200 字节）。
func TestPPEEvidenceFilesPresent(t *testing.T) {
	for _, name := range ppeFiles {
		info, err := os.Stat(filepath.Join(ppeDir, name))
		if err != nil {
			t.Fatalf("留痕缺失 %s/%s：%v", ppeDir, name, err)
		}
		if info.Size() < 1 {
			t.Fatalf("留痕 %s 为空文件", name)
		}
	}
	if n := len(ppeRead(t, "plan.json")); n < 200 {
		t.Fatalf("plan.json 仅 %d 字节（< 200）", n)
	}
	// commands.log ≥ 5 行，且逐行形如 `<UTC 时间> <命令> exit=<码>`。
	lines := strings.Split(strings.TrimRight(ppeRead(t, "commands.log"), "\n"), "\n")
	if len(lines) < 5 {
		t.Fatalf("commands.log 仅 %d 行（< 5）", len(lines))
	}
	for i, l := range lines {
		if !strings.HasSuffix(l, "exit=0") && !strings.Contains(l, " exit=") {
			t.Fatalf("commands.log 第 %d 行缺 exit=<码>：%s", i+1, l)
		}
		if len(l) < 21 || l[10] != 'T' || l[19] != 'Z' || l[20] != ' ' {
			t.Fatalf("commands.log 第 %d 行未以 UTC 时间戳开头：%s", i+1, l)
		}
	}
}

// TestPPEPlanParsesWithZeroErrorDiagnostics —— plan.json 可解析、零 error 级诊断、无占位串。
func TestPPEPlanParsesWithZeroErrorDiagnostics(t *testing.T) {
	raw := ppeRead(t, "plan.json")
	for _, bad := range []string{"占位串", "范例值", "示例值"} {
		if strings.Contains(raw, bad) {
			t.Fatalf("plan.json 含占位串 %q", bad)
		}
	}
	p, err := plan.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("plan.json 不可解析：%v", err)
	}
	for _, d := range p.Diags {
		if d.Level == plan.LevelError {
			t.Fatalf("plan.json 存在 error 级诊断：%s", d.String())
		}
	}
	if p.Verb != "process" {
		t.Fatalf("plan.verb = %q，回放链路的第三段应为 process", p.Verb)
	}
	if got := ppePlanCards(t, p); got != ppeNewCard {
		t.Fatalf("plan 新建卡 = %q，期望 %q", got, ppeNewCard)
	}
	if len(p.Convergence) != 1 || p.Convergence[0].Card != ppeOldCard {
		t.Fatalf("plan.convergence[] 必须恰一条且指向既有卡 %s", ppeOldCard)
	}
	if len(p.Base) == 0 {
		t.Fatal("plan.base 为空：B3 依赖 base 的 content_hash")
	}
}

// ppePlanCards 返回 plan 里新建知识卡的唯一卡 ID。
//
// **Schema v2 · T-…-003 重钉（事实变了，判据形态不变）**：`testdata/ppe/plan.json` 是
// M2 期的**留痕原件**（`plan_version: 1`、op 写作 `create_card`），字节一个都不改 ——
// 它正是「v1 plan 必须继续可执行」这条兼容判据的证据。契约 §4.4 规定别名在解析的
// 最后一步被改写成规范名，`validate` 与 `executor` 只见 `create_knowledge`，
// 因此本 helper 按**规范名**取卡；「恰一张」这条判据本体逐字未动。
//
// 同时新增三格加严（比原判据更严，不是放宽）：
//   - 留痕原件里必须仍是旧名 `create_card`（一旦有人偷改留痕去迁就代码，这里当场红）；
//   - 解析后**任何** op 都不得再叫别名（别名改写必须彻底，不能只改第一条）；
//   - 每条别名改写必须留一条 info 级迁移提示（兼容是**可观测**的，不是静默行为）。
func ppePlanCards(t *testing.T, p *plan.ChangePlan) string {
	t.Helper()
	raw := ppeRead(t, "plan.json")
	if !strings.Contains(raw, `"`+plan.OpCreateCard+`"`) {
		t.Fatalf("留痕 plan.json 应保留 M2 期原样的旧 op 名 %q（v1 兼容判据的证据，不得为迁就代码而改留痕）",
			plan.OpCreateCard)
	}
	var ids []string
	aliasCount, migrationInfos := 0, 0
	for _, op := range p.Ops {
		if op.Name == plan.OpCreateKnowledge {
			ids = append(ids, op.CardID)
		}
		if _, isAlias := plan.OpAliases()[op.Name]; isAlias {
			aliasCount++
		}
	}
	if aliasCount != 0 {
		t.Fatalf("解析后仍有 %d 条 op 叫兼容别名：契约 §4.4 要求改写在解析末尾一次做完，"+
			"validate / executor 不得见到别名", aliasCount)
	}
	for _, d := range p.Diags {
		if d.Level == plan.LevelInfo && strings.Contains(d.Message, plan.OpCreateCard) {
			migrationInfos++
		}
	}
	if migrationInfos == 0 {
		t.Fatalf("别名 %q 被改写后必须留下 info 级迁移提示（兼容必须可观测），实得诊断 %v",
			plan.OpCreateCard, p.Diags)
	}
	if len(ids) != 1 {
		t.Fatalf("plan 应恰新建一张卡，实际 %v", ids)
	}
	return ids[0]
}

// TestPPEProvenanceRegisteredHonestly —— 留痕与证据报告对「真实会话是否已执行」必须同真。
//
// 这条用例是**诚实性守卫**：把回放基线当成真实 Agent 原件上报，会在这里失败。
func TestPPEProvenanceRegisteredHonestly(t *testing.T) {
	session := ppeRead(t, "session.md")
	for _, must := range []string{"来源登记", "脱敏登记"} {
		if !strings.Contains(session, must) {
			t.Fatalf("session.md 缺「%s」表", must)
		}
	}
	evidence := readFile(t, ppeEvidence)
	sessionNotRun := strings.Contains(session, ppeNotRunMarker)
	evidenceNotRun := strings.Contains(evidence, ppeNotRunMarker)
	if sessionNotRun != evidenceNotRun {
		t.Fatalf("session.md（未执行=%v）与证据报告（未执行=%v）结论不一致",
			sessionNotRun, evidenceNotRun)
	}
	if sessionNotRun {
		for _, must := range []string{"离线回放基线"} {
			if !strings.Contains(session, must) {
				t.Fatalf("真实会话未执行时，session.md 必须把 plan.json 登记为「%s」", must)
			}
		}
		for _, must := range []string{"完成判据 8", "未达成", "阻塞人"} {
			if !strings.Contains(evidence, must) {
				t.Fatalf("真实会话未执行时，证据报告必须写明「%s」", must)
			}
		}
		return
	}
	if !strings.Contains(session, "未经人工编辑") {
		t.Fatal("已执行真实会话时，session.md 必须含「未经人工编辑」声明")
	}
}

// TestPPEReplayVerbSequence —— 在 Go 用例里独立复算一次动词序列（脚本被跳过也拦得住）。
func TestPPEReplayVerbSequence(t *testing.T) {
	vault := ppeReplay(t)
	subjects := strings.Split(strings.TrimSpace(
		gitOut(t, vault, "log", "--reverse", "--pretty=%s")), "\n")
	if len(subjects) != 3 {
		t.Fatalf("git log 应恰 3 条，实际 %d 条：%v", len(subjects), subjects)
	}
	for i, want := range []string{"capture(", "process(", "relate("} {
		if !strings.HasPrefix(subjects[i], want) {
			t.Fatalf("第 %d 条 commit 主题 %q 应以 %q 开头", i+1, subjects[i], want)
		}
	}
	if s := gitOut(t, vault, "status", "--porcelain"); strings.TrimSpace(s) != "" {
		t.Fatalf("回放结束时 vault 应干净，实际：%s", s)
	}
}

// TestPPEQueryAfterContainsNewRelation —— 写入后查询含新关系、写入前不含；两端 ID 与 plan 同源。
func TestPPEQueryAfterContainsNewRelation(t *testing.T) {
	p, err := plan.Parse([]byte(ppeRead(t, "plan.json")))
	if err != nil {
		t.Fatalf("plan.json 不可解析：%v", err)
	}
	from := ppePlanCards(t, p)      // 新建关系的起点 = plan 新建的卡
	target := p.Convergence[0].Card // 新建关系的终点 = plan 逐卡收敛判断指向的既有卡
	line := from + " --" + ppeRelType + "--> " + target
	before, after := ppeRead(t, "query-before.txt"), ppeRead(t, "query-after.txt")
	if strings.Contains(before, line) {
		t.Fatalf("query-before.txt 不应含目标关系：%s", line)
	}
	if !strings.Contains(before, "反向关系 relations_in[]：无") {
		t.Fatalf("query-before.txt 应显示反向关系为空：\n%s", before)
	}
	if !strings.Contains(after, line) {
		t.Fatalf("query-after.txt 应含目标关系 %s：\n%s", line, after)
	}
	if !strings.Contains(after, `"from":"`+from+`","type":"`+ppeRelType+`","target":"`+target+`"`) {
		t.Fatalf("query-after.txt 的关系条目两端 ID 与 plan 不同源：\n%s", after)
	}
}

// ppeReplay 在临时 vault 里回放五步链路，返回 vault 路径。
//
// 计划只有一个来源：`testdata/ppe/plan.json`（与 `m2_ppe_replay.sh` 同一份）。
func ppeReplay(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	vault := filepath.Join(root, "vault")

	// —— 环境准备（会话前的环境交付物，不计入会话命令序列）——
	if code, out, errOut := runEG(t, vault, "init", "--domain", domain); code != 0 {
		t.Fatalf("eg init 退出码 %d\n%s%s", code, out, errOut)
	}
	for _, kv := range [][2]string{{"domains", domain + "," + altDomain},
		{"default_domain", domain}} {
		if code, out, errOut := runEG(t, vault, "config", "set", kv[0], kv[1]); code != 0 {
			t.Fatalf("eg config set %s 退出码 %d\n%s%s", kv[0], code, out, errOut)
		}
	}
	cardDir := filepath.Join(vault, "domains", domain, "knowledge")
	if err := os.MkdirAll(cardDir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	seed := ppeRead(t, "seed-"+ppeOldCard+".md")
	if err := os.WriteFile(filepath.Join(cardDir, ppeOldCard+".md"), []byte(seed), 0o644); err != nil {
		t.Fatalf("写既有卡失败：%v", err)
	}
	// 会话起点：vault 的 git 历史清零，使会话内的三次写入就是全部历史。
	if err := os.RemoveAll(filepath.Join(vault, ".git")); err != nil {
		t.Fatalf("清 git 历史失败：%v", err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "eg"},
		{"config", "user.email", "eg@example.com"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = vault
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败：%v\n%s", args, err, out)
		}
	}

	// —— ① 收录 ——
	if code, out, errOut := runEG(t, vault, "capture", "--url", url2, "--title", title2,
		"--body-file", testdata(t, "verification-key-to-ai.txt"),
		"--reason", "Evergreen M2 端到端验证：收录 Rich Sutton 2001 年《Verification, The Key to AI》原文，用于与既有卡 k-20260918-scaling-compute 做收敛判断",
		"--tag", "ai", "--tag", "method",
		"--captured-at", ppeCapturedAt); code != 0 {
		t.Fatalf("eg capture 退出码 %d\n%s%s", code, out, errOut)
	}
	// —— ② 查询（base / 候选卡 / 既有卡 / 既有关系）——
	for _, args := range [][]string{
		{"context", "--source", ppeSrcID},
		{"search", "自验证"},
		{"search", "算力"},
		{"card", "show", ppeOldCard},
	} {
		if code, out, errOut := runEG(t, vault, args...); code != 0 {
			t.Fatalf("eg %v 退出码 %d\n%s%s", args, code, out, errOut)
		}
	}
	// —— 写入前查询 ——
	code, before, errOut := runEG(t, vault, "rel", ppeOldCard)
	if code != 0 {
		t.Fatalf("写入前 eg rel 退出码 %d\n%s", code, errOut)
	}
	if strings.Contains(before, ppeRelType+"--> "+ppeOldCard) {
		t.Fatalf("写入前不应已存在目标关系：\n%s", before)
	}
	// —— ③ + ④ 判断结论落盘 + 写入（plan 逐字取自留痕目录）——
	planPath, err := filepath.Abs(filepath.Join(ppeDir, "plan.json"))
	if err != nil {
		t.Fatalf("plan 路径解析失败：%v", err)
	}
	if code, out, errOut := runEG(t, vault, "apply", "--plan", planPath); code != 0 {
		t.Fatalf("eg apply 退出码 %d\n%s%s", code, out, errOut)
	}
	// —— ⑤ 写入关系 + 再查询验证 ——
	if code, out, errOut := runEG(t, vault, "rel", "add", ppeNewCard, ppeRelType, ppeOldCard,
		"--reason", ppeRelReason); code != 0 {
		t.Fatalf("eg rel add 退出码 %d\n%s%s", code, out, errOut)
	}
	code, after, errOut := runEG(t, vault, "rel", ppeOldCard)
	if code != 0 {
		t.Fatalf("写入后 eg rel 退出码 %d\n%s", code, errOut)
	}
	if !strings.Contains(after, ppeNewCard+" --"+ppeRelType+"--> "+ppeOldCard) {
		t.Fatalf("写入后查询应含新关系：\n%s", after)
	}
	// 与留痕逐字一致（留痕不是另一套事实）。
	//
	// **M5 · T-…-069 归一（只归一环境量，不归一语义）**：M5 读路径接入索引后（T-…-067），
	// 无 `.index/` 的回放每次读都会留痕 `W23` + `Q5`，而 `W23` 的 detail 里内嵌**索引目录
	// 绝对路径**——它取自 `mktemp -d` / `t.TempDir()`，每次运行都不同，且 Go 用例与
	// `m2_ppe_replay.sh` 两侧的临时根天然不同。逐字比对因此必须先把这**唯一一处环境量**
	// 归一为 `<VAULT>` 占位符（`ppeNormalize`）。
	//
	// 归一面严格封闭：只替换 vault 根路径这一个子串，`W23` / `Q5` 的码、级别、path、
	// 文案与新增的「分页」行**一律逐字入留痕**，不做任何屏蔽或宽松匹配——即「不删除、
	// 不放宽原判据，只把环境相关的那一格钉成确定值」。
	if got, want := ppeNormalize(after, vault), ppeRead(t, "query-after.txt"); got != want {
		t.Fatalf("回放的写入后查询输出与留痕 query-after.txt 不一致：\n--- 回放 ---\n%s\n--- 留痕 ---\n%s",
			got, want)
	}
	return vault
}

// ppeVaultPlaceholder 是留痕里 vault 绝对路径的确定性占位符。
const ppeVaultPlaceholder = "<VAULT>"

// ppeNormalize 把回放输出里的 vault 绝对路径归一为 ppeVaultPlaceholder。
//
// 这是留痕逐字比对的**唯一**归一项：其余字节（含 W23 / Q5 的完整文案与分页行）原样保留。
func ppeNormalize(out, vault string) string {
	return strings.ReplaceAll(out, vault, ppeVaultPlaceholder)
}
