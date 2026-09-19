// `eg index` 命令级用例（M5 · T-evergreen.s1_main_flow-158614-065 阶段 3）。
//
// 本文件只测**命令本体**：注册面（恰四子命令、恰 1 个私有 flag `--strict`）、三支 build 语义、
// rebuild 与 fresh build 的等价、status 恒退 0、`eg index sync` 的收敛与退化、
// 写命令的写后同步，以及三条**边界反证**（权威 Markdown 零改动、commit 恒 0 次、
// `.index/` 不进 Git）。索引包自身的 Schema / 确定性 / 增量等价判定用例在
// `internal/index/**`，此处一条不重复。
//
// T-…-066 阶段 B 的增量用例集中在文件尾部（第 ⑦ 组起），前面各组只做**精确重钉**：
// 旧断言一条不删、不放宽，只把「sync / --strict 属下游 task」这一格改成「已落地」。
//
// 三条纪律（写在最前面，便于复核）：
//   - **零 mock**：`eg index` 不发网络请求、不发 commit、没有需要注入的不稳定失败分支，
//     因此全文件**没有一处 mock**（时钟注入是确定性手段，不是替换被测行为）。
//   - **坏索引是真字节**：损坏语料一律往 `.index/eg.db` 里写真实的非法字节 / 截断内容，
//     不靠打桩让 `Inspect` 返回 corrupt。
//   - **权威零改动靠全库字节快照反证**：不是「检查了几个文件」，而是把 `domains/` /
//     `sources/` / `proposals/` 全量文件的字节做前后逐字比对（多写一个字节即红）。
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// idxFixedNow 是本文件的固定时钟：`built_at_unix` 因此可复算，用例全程确定性。
const idxFixedNow = "2026-12-19T10:00:00+08:00"

// runIndexCLI 在 dir 上跑一次 `eg index <sub> …`（恒带 --json 信封）。
// stdin 恒为已关闭状态：本命令没有任何确认点（退出码 6 不在它的码集里），
// 任何读 stdin 的行为都会当场红。
func runIndexCLI(t *testing.T, dir, sub string, extra ...string) (int, string, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.In = closedStdin{}
	r.Now = func() time.Time { return stampAt(t, idxFixedNow) }
	args := append([]string{"index", sub, "--vault", dir, "--json"}, extra...)
	return runCLI(t, r, args...)
}

// idxVault 建一个含一篇笔记 + 两张卡的真实 vault（全部经 `eg apply` 落盘、工作区干净）。
func idxVault(t *testing.T) string {
	t.Helper()
	dir := applyVault(t)
	applyNoteAndCard(t, dir)
	if code, _, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, "")); code != ExitOK {
		t.Fatalf("前置：第二张卡未建成：%s", errOut)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：工作区必须干净，得到 %q", got)
	}
	return dir
}

// idxAuthoritySnapshot 把三个知识产物根下**全部**文件的相对路径 → 字节抓成一份快照。
//
// 这是「权威 Markdown 零改动」的机器形态：比对的是**字节**而不是 mtime / 条数，
// 因此索引哪怕只在某张卡里多写一个空格，下面的 idxAssertAuthorityUnchanged 也会当场红。
func idxAuthoritySnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range KnowledgeRoots() {
		base := filepath.Join(dir, root)
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue
		}
		err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			raw, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			rel, _ := filepath.Rel(dir, p)
			out[filepath.ToSlash(rel)] = string(raw)
			return nil
		})
		if err != nil {
			t.Fatalf("抓取权威快照失败：%v", err)
		}
	}
	if len(out) == 0 {
		t.Fatal("权威快照为空：前置语料没建起来，本反证会失去意义")
	}
	return out
}

func idxAssertAuthorityUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	after := idxAuthoritySnapshot(t, dir)
	if len(before) != len(after) {
		t.Fatalf("权威产物文件数变了：前 %d、后 %d（索引只许写 .index/）", len(before), len(after))
	}
	for rel, want := range before {
		got, ok := after[rel]
		if !ok {
			t.Fatalf("权威产物 %s 消失了：索引绝不许碰权威文件", rel)
		}
		if got != want {
			t.Fatalf("权威产物 %s 字节被改动：索引恒零写权威（Markdown 是唯一权威来源）", rel)
		}
	}
}

// idxIndexArtifactNames 返回「本轮允许出现在 `.index/` 里的**索引产物**」名字集合。
//
// 恰 = index.AllowedFiles()（三个 DB 文件）。M6 运行时保留条目（`run.lock` / `txn/`）
// **不在**这张表里，因为它们根本不是索引产物 —— 见下面 idxAssertNoIndexArtifacts。
func idxIndexArtifactNames() map[string]bool {
	out := map[string]bool{}
	for _, f := range index.AllowedFiles() {
		out[f] = true
	}
	return out
}

// idxRuntimeReservedNames 返回 M6 运行时保留条目名（`run.lock` / `txn`）。
//
// 逐字引用 index.RuntimeReservedEntries() 这个**单点真源**而不是另抄一份名单：
// 名册若增删一项，本文件的两条判据同步跟着走，不会出现「测试比实现旧一版」。
func idxRuntimeReservedNames() map[string]bool {
	out := map[string]bool{}
	for _, e := range index.RuntimeReservedEntries() {
		out[e.Name] = true
	}
	return out
}

// idxAssertNoIndexArtifacts 反证「本次没有产出任何**索引**产物」。
//
// # 为什么判据不是「`.index/` 不存在」（M6 · T-…-072 B2c1 精确重钉）
//
// M5 时期 `.index/` 里只可能有索引，于是「目录不存在」与「没建索引」是同一件事。
// M6 之后同一个目录还住着**事务层**的两样东西：进程间互斥锁 `run.lock` 与事务日志
// `txn/`。合同 §2 要求写命令「先取锁、后校验」，因此**连一次校验失败的写命令都必然
// 留下 run.lock**；夹具里的 `eg apply` 更会留下整棵 `txn/`。此时若继续用「目录不存在」
// 当判据，钉住的就不再是「谁建了索引」，而是「有没有人取过锁」—— 判错了对象。
//
// 因此这里把判据收紧到**语义正确的那一格**：`.index/` 下允许出现的名字**恰**是
// 运行时保留条目；只要冒出任何索引产物（`eg.db` / `-wal` / `-shm`）或任何第三种东西，
// 立刻红。注意这比原判据在「索引」这件事上**只严不松**：原判据允许目录不存在，
// 本判据在目录存在时逐项点名，且**不**把 DB 文件列进允许集合。
func idxAssertNoIndexArtifacts(t *testing.T, dir, what string) {
	t.Helper()
	entries, err := os.ReadDir(index.DirPath(dir))
	if os.IsNotExist(err) {
		return // 连 .index/ 都没有，比逐项点名更严，通过
	}
	if err != nil {
		t.Fatalf("读 %s 失败：%v", index.DirName, err)
	}
	reserved := idxRuntimeReservedNames()
	artifacts := idxIndexArtifactNames()
	for _, e := range entries {
		if reserved[e.Name()] {
			continue
		}
		if artifacts[e.Name()] {
			t.Fatalf("%s：出现了索引产物 %s/%s —— 本次必须零索引写入"+
				"（索引只能由 eg index build / rebuild / sync 显式创建）",
				what, index.DirName, e.Name())
		}
		t.Fatalf("%s：%s/ 下出现了既非索引产物、也非 M6 运行时保留条目的 %q"+
			"（允许集合恰 %v）", what, index.DirName, e.Name(), index.RuntimeReservedEntries())
	}
}

// idxHealth 取 `data.index.health` 的字符串值。
func idxHealth(t *testing.T, out string) string {
	t.Helper()
	return idxString(t, out, "health")
}

// idxString 取 `data.index.<key>` 的字符串值（去掉 JSON 引号）。
func idxString(t *testing.T, out, key string) string {
	t.Helper()
	raw := rcRawAt(t, []byte(out), "data", "index", key)
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

// idxWarnCodes 取信封 `data.report.warnings[]` 里**warning 级**诊断码的集合（排序后便于逐字断言）。
//
// 只取 warning 级是刻意的：同一数组里还躺着 info 级的 I1（两条常驻声明——「Markdown 是唯一
// 权威来源」与「本阶段未做什么」），那是命令的固定说明，不是本次体检结论。
// 两侧都要钉，所以 info 级另有 idxInfoCodes 覆盖。
func idxWarnCodes(t *testing.T, out string) []string {
	t.Helper()
	return idxCodesAtLevel(t, out, "warning")
}

// idxInfoCodes 取 info 级诊断码集合。
func idxInfoCodes(t *testing.T, out string) []string {
	t.Helper()
	return idxCodesAtLevel(t, out, "info")
}

func idxCodesAtLevel(t *testing.T, out, level string) []string {
	t.Helper()
	raw := rcRawAt(t, []byte(out), "data", "report")
	var rep struct {
		Warnings []struct {
			Code  string `json:"code"`
			Level string `json:"level"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("report 不可解析：%v\n%s", err, raw)
	}
	var got []string
	for _, w := range rep.Warnings {
		if w.Code != "" && w.Level == level {
			got = append(got, w.Code)
		}
	}
	sort.Strings(got)
	return got
}

// idxAssertStandingNotices 钉住两条常驻 info 声明恒在：
// 「Markdown 是唯一权威来源」与「本阶段还没做什么」。
// 它们是本命令对用户的诚实交底，不许在任何分支里悄悄消失。
func idxAssertStandingNotices(t *testing.T, out string) {
	t.Helper()
	if got := idxInfoCodes(t, out); len(got) != 2 {
		t.Fatalf("info 级诊断 = %v，期望恰两条常驻声明（权威来源 + 未做清单）", got)
	}
	raw := string(rcRawAt(t, []byte(out), "data", "report"))
	for _, want := range []string{IndexAuthorityNotice, IndexNotDoneNotice} {
		if !strings.Contains(raw, want) {
			t.Fatalf("报告里缺常驻声明 %q", want)
		}
	}
}

// idxCorruptDB 把 `.index/eg.db` 换成**真实**的非法字节（不是打桩）。
func idxCorruptDB(t *testing.T, dir string) {
	t.Helper()
	dbPath := index.DBPath(dir)
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("前置：索引库应已存在：%v", err)
	}
	if err := os.WriteFile(dbPath, []byte("这不是一个 SQLite 文件"), 0o644); err != nil {
		t.Fatalf("写坏索引失败：%v", err)
	}
}

// —— ① 注册面：恰四子命令、恰 1 个私有 flag（--strict 只对 status 有语义）——

// TestIndexSubcommandsAreExactlyFour 把「子命令恰四个」证成**双侧等号**：
// 导出的封闭集合、注册表里的 Subs、以及 `--help` 展示名三处逐字相等，
// 且第四个逐字是 `sync`（T-…-066 阶段 B 补齐，合同 §8.1 的封闭集合到此闭合）。
func TestIndexSubcommandsAreExactlyFour(t *testing.T) {
	want := []string{"build", "rebuild", "status", "sync"}
	if got := IndexSubcommands(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("IndexSubcommands() = %v，期望逐字有序 %v", got, want)
	}
	cmd := New().Lookup("index")
	if cmd == nil {
		t.Fatal("命令 index 未注册")
	}
	if strings.Join(cmd.Subs, ",") != strings.Join(want, ",") {
		t.Fatalf("注册表 Subs = %v，期望逐字有序 %v", cmd.Subs, want)
	}
	if !cmd.SubRequired {
		t.Fatal("eg index 必须要求子命令：缺子命令应退 1，而不是默默做点什么")
	}
	if cmd.Handler == nil {
		t.Fatal("命令 index 已注册但实现未挂载（root.go 漏了 wireIndex）")
	}
	if cmd.Placeholder {
		t.Fatal("命令 index 不得是占位命令：T-…-065 就是它的实现 task")
	}
	if cmd.Display != "index build|rebuild|status|sync" {
		t.Fatalf("展示名 = %q，期望 %q", cmd.Display, "index build|rebuild|status|sync")
	}
	// `sync` 三处都必须在场（做了就要说得出口，和「没做就不许暗示做了」是同一条纪律）。
	if !strings.Contains(cmd.Display, IndexSubSync) ||
		!strings.Contains(cmd.Usage, "eg index "+IndexSubSync) {
		t.Fatalf("sync 已落地，展示名与 Usage 必须逐字在场：%q", cmd.Display)
	}
	// 命令私有 flag 恰 1 个 `--strict`（--json / --vault 是全局 flag，另计）；
	// 分页 / 性能参数（--limit 等属 T-…-068）仍不得出现。
	if got := strings.Join(wantFlags["index"], ","); got != IndexStrictFlag {
		t.Fatalf("index 的命令私有 flag = %q，期望恰 %q", got, IndexStrictFlag)
	}
	if !strings.Contains(cmd.Usage, "--"+IndexStrictFlag) {
		t.Fatal("Usage 必须逐字列出 --strict（参数面要能被用户读到）")
	}
	for _, banned := range []string{"--limit", "--offset", "--page"} {
		if strings.Contains(cmd.Usage, banned) {
			t.Fatalf("%s 属 T-…-068，本 task 不得出现在参数面", banned)
		}
	}
}

// TestCommandCountTwentyOne 把 M5 过程值「命令数 21」证成一条加法等式：
// M4 收口 20 条 + M5 期新增恰 1 条（`index` 属 T-…-065）。
//
// 四件事一起断言：① 摘掉 M5 更晚新增的命令后总量恰 21；② 前 20 条逐字等于 M4 收口基线；
// ③ 差集**恰一条**且逐字是 `index`；④ 该条紧跟 M4 基线（新命令一律追加在尾部）。
//
// **T-…-068 重钉说明（2026-09-08）**：`eg bench` 注册后磁盘实测总数变成 22（M5 终值，
// 合同 §8.1 命令数复算 20 + 1 + 1 = 22），于是本用例改证「把 `bench` 摘掉之后恰 21」
// —— **21 这个 T-…-065 的结论一个字不删**，也不再引用 `wantCommandCount`
// （那是当期值，M5 终值为 22）。22 那一格由 TestCommandCountTwentyTwo 正面对撞，
// 两条等式互不代替。
func TestCommandCountTwentyOne(t *testing.T) {
	// M4 收口（0.4.0-m4）的注册表基线，逐字照抄，**不引用 wantCommands 派生**：
	// 两份清单必须独立，否则 wantCommands 被改错时本用例会跟着一起错。
	m4Baseline := []string{
		"init", "config", "capture", "context", "apply",
		"search", "card", "rel", "report",
		"deprecate", "restore", "replaced-by",
		"proposal", "delete", "undelete",
		"mark-reviewed", "unreviewed", "edit",
		"reconcile", "check",
	}
	m5Added := []string{"index"}
	// M5 更晚新增（T-…-068 阶段 B 的 `bench`、T-…-006-B1a 的 `opinion`）：本用例只负责
	// 065 那一条等式，摘掉后再复算 21。**21 这个过程值结论一个字不删**。
	m5LaterAdded := []string{"bench", "opinion"}

	if len(m4Baseline) != 20 {
		t.Fatalf("M4 基线清单写错了：%d 条，M4 收口时恰 20 条", len(m4Baseline))
	}
	want := len(m4Baseline) + len(m5Added)
	if want != 21 {
		t.Fatalf("加法等式不成立：%d + %d = %d，期望 21", len(m4Baseline), len(m5Added), want)
	}
	if want+len(m5LaterAdded) != wantCommandCount {
		t.Fatalf("M5 过程值 %d + 更晚新增 %d 与 wantCommandCount = %d 不一致",
			want, len(m5LaterAdded), wantCommandCount)
	}

	got := New().Commands()
	if len(got)-len(m5LaterAdded) != 21 {
		t.Fatalf("摘掉 M5 更晚新增 %v 后命令数 = %d，期望 21（M4 收口 20 + M5 期 1）",
			m5LaterAdded, len(got)-len(m5LaterAdded))
	}
	for i, w := range m4Baseline {
		if got[i].Name != w {
			t.Fatalf("第 %d 条命令 = %q，期望 %q（M5 只准在尾部追加，不准重排既有条目）",
				i+1, got[i].Name, w)
		}
	}
	base := map[string]bool{}
	for _, n := range m4Baseline {
		base[n] = true
	}
	later := map[string]bool{}
	for _, n := range m5LaterAdded {
		later[n] = true
	}
	var added []string
	for _, c := range got {
		if !base[c.Name] && !later[c.Name] {
			added = append(added, c.Name)
		}
	}
	if strings.Join(added, ",") != strings.Join(m5Added, ",") {
		t.Fatalf("M5 期新增 = %v，期望逐字有序 %v", added, m5Added)
	}
	if n := len(got) - len(m5LaterAdded); got[n-1].Name != "index" {
		t.Fatalf("摘掉 %v 后的注册表尾条 = %q，期望 %q（新命令一律追加在尾部）",
			m5LaterAdded, got[n-1].Name, "index")
	}
}

// TestIndexRejectsUnknownSubcommandsAndFlags：越界 / 无语义参数一律退 1 且**零写入**。
//
// 这是「不越界到下游 task」的机器形态：`--limit`（属 T-…-068）在参数解析阶段就被判非法；
// `--strict` 虽已落地，但它**只对 status 有语义**，挂在 build / rebuild / sync 上同样当场判非法
// —— 参数在某个子命令上没有意义时必须说清楚，不许默默忽略。磁盘上不会出现任何索引产物。
//
// M6 · T-…-072 B2c1 精确重钉：判据从「`.index/` 都不会出现」收紧为「`.index/` 下恰只有
// M6 运行时保留条目、一个索引产物都没有」。参数校验在 `Command.Validate` 里完成，早于
// `runIndexCritical` 的取锁，因此这几条命令连锁都不会碰；目录里那点东西是夹具的
// `eg apply` 留下的事务层痕迹，与「谁建了索引」无关（详见 idxAssertNoIndexArtifacts）。
func TestIndexRejectsUnknownSubcommandsAndFlags(t *testing.T) {
	dir := idxVault(t)
	before := idxAuthoritySnapshot(t, dir)
	for _, c := range []struct {
		name string
		args []string
	}{
		{"缺子命令", []string{}},
		{"未知子命令", []string{"vacuum"}},
		{"--strict 挂在 build 上（无语义）", []string{"build", "--strict"}},
		{"--strict 挂在 rebuild 上（无语义）", []string{"rebuild", "--strict"}},
		{"--strict 挂在 sync 上（无语义）", []string{"sync", "--strict"}},
		{"--limit（属 T-…-068）", []string{"status", "--limit", "10"}},
		{"--limit 挂在 sync 上（属 T-…-068）", []string{"sync", "--limit", "10"}},
		{"多余位置参数", []string{"status", "extra"}},
		{"sync 多余位置参数", []string{"sync", "extra"}},
	} {
		r := newTestRoot(t, dir)
		r.In = closedStdin{}
		args := append([]string{"index"}, c.args...)
		args = append(args, "--vault", dir, "--json")
		code, _, _ := runCLI(t, r, args...)
		if code != ExitUsage {
			t.Fatalf("%s：退出码 = %d，期望 %d（参数非法）", c.name, code, ExitUsage)
		}
		idxAssertNoIndexArtifacts(t, dir, c.name+"：参数非法必须零索引写入")
	}
	idxAssertAuthorityUnchanged(t, dir, before)
}

// —— ② status 恒退 0（三态都是诊断，不是失败）——

// TestIndexStatusAlwaysExitsZero 把「status 恒退 0」在三态上逐一对撞，并同时钉住只读：
// 三次调用之后 `.index/` 的存在性与内容都不因 status 而改变。
func TestIndexStatusAlwaysExitsZero(t *testing.T) {
	dir := idxVault(t)
	before := idxAuthoritySnapshot(t, dir)

	// 态一：索引不存在 → missing（W23），仍退 0，且 status 自己**不建**索引。
	code, out, errOut := runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("missing 态 status 退出码 = %d，期望 0（索引坏了是诊断不是失败）：%s", code, errOut)
	}
	if h := idxHealth(t, out); h != string(index.HealthMissing) {
		t.Fatalf("health = %q，期望 %q", h, index.HealthMissing)
	}
	if got := idxWarnCodes(t, out); strings.Join(got, ",") != index.CodeIndexMissing {
		t.Fatalf("warnings = %v，期望恰一条 %s", got, index.CodeIndexMissing)
	}
	// status 的只读判据钉的是**索引产物**一个都没有：`.index/` 目录本身可能已被夹具的
	// `eg apply`（合同 §2「先取锁、后校验」）建出来，那是事务层的互斥设施，不是索引。
	idxAssertNoIndexArtifacts(t, dir, "status 是只读命令，绝不许建索引")
	idxAssertStandingNotices(t, out)

	// 态二：建好之后 → healthy，退 0，且**不产**任何 warning。
	if code, _, errOut = runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("build 退出码 = %d，期望 0：%s", code, errOut)
	}
	digestBefore, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("建完的索引应可读：%v", err)
	}
	code, out, errOut = runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("healthy 态 status 退出码 = %d，期望 0：%s", code, errOut)
	}
	if h := idxHealth(t, out); h != string(index.HealthHealthy) {
		t.Fatalf("health = %q，期望 %q", h, index.HealthHealthy)
	}
	if got := idxWarnCodes(t, out); len(got) != 0 {
		t.Fatalf("healthy 态不得产 warning，实得 %v", got)
	}
	// 只读复核：status 跑完之后索引内容逐字未变。
	digestAfter, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("status 之后索引应仍可读：%v", err)
	}
	if digestBefore != digestAfter {
		t.Fatal("status 改动了索引内容：它必须是只读命令")
	}

	// 态三：真字节损坏 → corrupt（W24），仍退 0，且 status **不自动修**。
	idxCorruptDB(t, dir)
	code, out, errOut = runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("corrupt 态 status 退出码 = %d，期望 0：%s", code, errOut)
	}
	if h := idxHealth(t, out); h != string(index.HealthCorrupt) {
		t.Fatalf("health = %q，期望 %q", h, index.HealthCorrupt)
	}
	if got := idxWarnCodes(t, out); strings.Join(got, ",") != index.CodeIndexCorrupt {
		t.Fatalf("warnings = %v，期望恰一条 %s", got, index.CodeIndexCorrupt)
	}
	if _, err := index.Digest(index.DirPath(dir)); err == nil {
		t.Fatal("status 自动修好了坏索引：只读体检只报不改（修复走 eg index rebuild）")
	}
	idxAssertAuthorityUnchanged(t, dir, before)
}

// —— ③ build 三支语义 + rebuild 与 fresh build 等价 ——

// TestIndexBuildThreeBranches 把 `eg index build` 的三支语义逐支对撞：
// 缺失 → built；healthy → noop（**零写入**，Digest 逐字不变）；不可用 → repaired（并留痕 W24）。
func TestIndexBuildThreeBranches(t *testing.T) {
	dir := idxVault(t)
	before := idxAuthoritySnapshot(t, dir)

	// 支一：缺失 → built。
	code, out, errOut := runIndexCLI(t, dir, "build")
	if code != ExitOK {
		t.Fatalf("build（缺失）退出码 = %d，期望 0：%s", code, errOut)
	}
	if a := idxString(t, out, "action"); a != string(index.ActionBuilt) {
		t.Fatalf("action = %q，期望 %q", a, index.ActionBuilt)
	}
	if h := idxHealth(t, out); h != string(index.HealthHealthy) {
		t.Fatalf("建完 health = %q，期望 %q", h, index.HealthHealthy)
	}
	builtDigest, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("建完的索引应可读：%v", err)
	}
	idxAssertStandingNotices(t, out)

	// 支二：healthy → noop，且**一个字节都不写**（Digest 与 mtime 双侧不变）。
	st1, err := os.Stat(index.DBPath(dir))
	if err != nil {
		t.Fatalf("stat 索引库失败：%v", err)
	}
	code, out, errOut = runIndexCLI(t, dir, "build")
	if code != ExitOK {
		t.Fatalf("build（healthy）退出码 = %d，期望 0：%s", code, errOut)
	}
	if a := idxString(t, out, "action"); a != string(index.ActionNoop) {
		t.Fatalf("action = %q，期望 %q（索引原本健康就不该重建）", a, index.ActionNoop)
	}
	noopDigest, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("no-op 之后索引应仍可读：%v", err)
	}
	if noopDigest != builtDigest {
		t.Fatal("no-op 分支改动了索引内容：它必须零写入")
	}
	st2, err := os.Stat(index.DBPath(dir))
	if err != nil {
		t.Fatalf("stat 索引库失败：%v", err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) || st1.Size() != st2.Size() {
		t.Fatalf("no-op 分支动了库文件（mtime %v→%v、size %d→%d）：它必须零写入",
			st1.ModTime(), st2.ModTime(), st1.Size(), st2.Size())
	}
	// no-op 分支**没有**本次写入计数这一格（造一个 0 会让读者误以为「本次写了 0 张卡」）。
	if strings.Contains(string(rcRawAt(t, []byte(out), "data", "index")), `"cards"`) {
		t.Fatal("no-op 分支不得给出本次写入计数：事实是「本次一个字节都没写」")
	}

	// 支三：真字节损坏 → repaired，并把「原本坏了」如实留痕成 W24。
	idxCorruptDB(t, dir)
	code, out, errOut = runIndexCLI(t, dir, "build")
	if code != ExitOK {
		t.Fatalf("build（corrupt）退出码 = %d，期望 0（坏索引不是死局）：%s", code, errOut)
	}
	if a := idxString(t, out, "action"); a != string(index.ActionRepaired) {
		t.Fatalf("action = %q，期望 %q", a, index.ActionRepaired)
	}
	if h := idxHealth(t, out); h != string(index.HealthHealthy) {
		t.Fatalf("修完 health = %q，期望 %q", h, index.HealthHealthy)
	}
	if got := idxWarnCodes(t, out); strings.Join(got, ",") != index.CodeIndexCorrupt {
		t.Fatalf("warnings = %v，期望恰一条 %s（修好了也要说清修的是什么）",
			got, index.CodeIndexCorrupt)
	}
	repaired, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("修完的索引应可读：%v", err)
	}
	if repaired != builtDigest {
		t.Fatal("可恢复重建的结果与全新构建不等价：重建必须走同一条全量路径")
	}

	idxAssertAuthorityUnchanged(t, dir, before)
}

// TestIndexRebuildEqualsFreshBuild：`rebuild` 与「删干净后 fresh build」在 Digest 口径下等价。
//
// Digest 刻意排除天然非确定列（`built_at_unix` / `files.indexed_at_unix`），
// 因此这条等价是**内容等价**，不是「字节碰巧相同」。
func TestIndexRebuildEqualsFreshBuild(t *testing.T) {
	dir := idxVault(t)
	before := idxAuthoritySnapshot(t, dir)

	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("首建退出码 = %d：%s", code, errOut)
	}
	fresh, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("首建索引应可读：%v", err)
	}

	code, out, errOut := runIndexCLI(t, dir, "rebuild")
	if code != ExitOK {
		t.Fatalf("rebuild 退出码 = %d：%s", code, errOut)
	}
	if a := idxString(t, out, "action"); a != string(index.ActionRebuilt) {
		t.Fatalf("action = %q，期望 %q", a, index.ActionRebuilt)
	}
	again, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("重建索引应可读：%v", err)
	}
	if again != fresh {
		t.Fatal("rebuild 与 fresh build 不等价：schema 不兼容一律整库重建，结果必须逐字等价")
	}
	// rebuild 在**坏索引**上同样成立：它根本不读旧库。
	idxCorruptDB(t, dir)
	if code, _, errOut = runIndexCLI(t, dir, "rebuild"); code != ExitOK {
		t.Fatalf("坏索引上 rebuild 退出码 = %d，期望 0：%s", code, errOut)
	}
	third, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("重建索引应可读：%v", err)
	}
	if third != fresh {
		t.Fatal("坏索引上的 rebuild 结果与 fresh build 不等价")
	}
	idxAssertAuthorityUnchanged(t, dir, before)
}

// —— ④ 三条边界反证：零 commit、`.index/` 不进 Git、`.index/` 删了能复原 ——

// TestIndexNeverCommitsAndStaysOutOfGit：`eg index` 恒 0 次 commit，且 `.index/` 不进 Git。
//
// 「不进 Git」判的是**工作区脏度**：`.index/` 已被 `eg init` 写进 vault 的 `.gitignore`，
// 因此三个子命令跑完之后 `git status --porcelain` 仍必须是空 —— 这比只 grep 一行 .gitignore 强，
// 它同时覆盖了「文件真的落在了被忽略的路径下」。
func TestIndexNeverCommitsAndStaysOutOfGit(t *testing.T) {
	dir := idxVault(t)
	before := idxAuthoritySnapshot(t, dir)
	commitsBefore := gitLogCount(t, dir)

	for _, sub := range IndexSubcommands() {
		code, out, errOut := runIndexCLI(t, dir, sub)
		if code != ExitOK {
			t.Fatalf("eg index %s 退出码 = %d：%s", sub, code, errOut)
		}
		// 信封里的 commit 必须如实为 null：本命令不发 commit。
		if raw := strings.TrimSpace(string(rcRawAt(t, []byte(out), "data", "report", "git", "commit"))); raw != "null" {
			t.Fatalf("eg index %s 的报告里 git.commit = %s，期望 null（本命令恒 0 次提交）", sub, raw)
		}
		if n := rcPorcelainCount(t, dir); n != 0 {
			t.Fatalf("eg index %s 之后工作区脏了 %d 行：.index/ 必须被 .gitignore 整目录忽略\n%s",
				sub, n, gitOut(t, dir, "status", "--porcelain"))
		}
	}
	if n := gitLogCount(t, dir); n != commitsBefore {
		t.Fatalf("commit 数从 %d 变成 %d：eg index 恒 0 次提交", commitsBefore, n)
	}
	// `.index/` 里出现的名字恰在「三个索引产物 ∪ 两项 M6 运行时保留条目」之内
	// （没有临时残留、没有额外产物）。
	//
	// M6 · T-…-072 B2c1 精确重钉：允许集合从 index.AllowedFiles() 扩到再并上
	// index.RuntimeReservedEntries()。这**不是**放宽索引这一格 —— 三个 DB 文件的名单
	// 一个不多一个不少；新并进来的 `run.lock` / `txn` 是**事务层**的在盘事实，由
	// `runIndexCritical` 的取锁与崩溃恢复必然产生（合同 §16.5 把它们定为保留条目，
	// 索引侧既不删也不改）。两张表在 internal/index 里本就是刻意分开的两个单点真源，
	// 这里逐字引用它们，不另抄名单。
	entries, err := os.ReadDir(index.DirPath(dir))
	if err != nil {
		t.Fatalf("读 %s 失败：%v", index.DirName, err)
	}
	allowed := idxIndexArtifactNames()
	for name := range idxRuntimeReservedNames() {
		allowed[name] = true
	}
	for _, e := range entries {
		if !allowed[e.Name()] {
			t.Fatalf("%s 出现非白名单条目 %q，允许集合恰 索引产物 %v ∪ 运行时保留条目 %v",
				index.DirName, e.Name(), index.AllowedFiles(), index.RuntimeReservedEntries())
		}
	}
	idxAssertAuthorityUnchanged(t, dir, before)
}

// TestIndexDirIsDisposable：整目录删掉是**安全动作** —— 零信息损失，`build` 一跑即复原。
//
// 这是「索引恒为可重建派生」这条最高约束的命令级形态。
func TestIndexDirIsDisposable(t *testing.T) {
	dir := idxVault(t)
	before := idxAuthoritySnapshot(t, dir)

	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("首建退出码 = %d：%s", code, errOut)
	}
	want, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("首建索引应可读：%v", err)
	}

	// 用户手工 rm -rf 整个目录（这是被明确允许的动作）。
	if err := os.RemoveAll(index.DirPath(dir)); err != nil {
		t.Fatalf("删除派生索引目录失败：%v", err)
	}
	code, out, errOut := runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("删掉之后 status 退出码 = %d，期望 0：%s", code, errOut)
	}
	if h := idxHealth(t, out); h != string(index.HealthMissing) {
		t.Fatalf("删掉之后 health = %q，期望 %q", h, index.HealthMissing)
	}

	if code, _, errOut = runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("复原退出码 = %d：%s", code, errOut)
	}
	got, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("复原后的索引应可读：%v", err)
	}
	if got != want {
		t.Fatal("删掉重建之后内容不一致：索引必须是从权威 Markdown 可完整重算的派生物")
	}
	idxAssertAuthorityUnchanged(t, dir, before)
}

// TestIndexErrIsExistsRecognizesSentinel：`build` 不覆盖既有库这条语义有**具名判定点**。
//
// 判定走 sentinel（`errors.Is`）而不是比对错误文案：文案随时可改，语义不许漂。
func TestIndexErrIsExistsRecognizesSentinel(t *testing.T) {
	dir := t.TempDir()
	snap := index.Snapshot{Head: "deadbeef"}
	if _, err := index.Build(filepath.Join(dir, index.DirName), snap, index.Options{}); err != nil {
		t.Fatalf("空快照也应能建出索引：%v", err)
	}
	_, err := index.Build(filepath.Join(dir, index.DirName), snap, index.Options{})
	if err == nil {
		t.Fatal("Build 在既有库上必须报错（不覆盖既有库）")
	}
	if !indexErrIsExists(err) {
		t.Fatalf("indexErrIsExists 未识别「索引已存在」：%v", err)
	}
	if indexErrIsExists(os.ErrNotExist) {
		t.Fatal("indexErrIsExists 把无关错误也判成了「索引已存在」")
	}
}

// —— ⑦ T-…-066 阶段 B：陈旧判定（W22）、--strict 强校验、sync 收敛与退化、写后同步 ——

// idxFreshness 取 `data.index.freshness`。
func idxFreshness(t *testing.T, out string) string {
	t.Helper()
	return idxString(t, out, "freshness")
}

// idxEditCardExternally 模拟「用户在编辑器里直接改了一张卡」：绕开一切命令，直接改字节。
//
// 这是陈旧判定唯一诚实的语料来源 —— 走 `eg` 写命令改卡会顺带把索引同步掉，
// 那就测不到「索引落后于权威」这件事了。
func idxEditCardExternally(t *testing.T, dir, rel, appended string) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", rel, err)
	}
	if err := os.WriteFile(abs, append(raw, []byte(appended)...), 0o644); err != nil {
		t.Fatalf("写 %s 失败：%v", rel, err)
	}
}

// TestIndexStatusDetectsStaleAndNeverBlocks：外部改卡之后 status 判 stale（W22）、
// 仍退 0、仍**只读**，且明确交代「不阻断读」。
func TestIndexStatusDetectsStaleAndNeverBlocks(t *testing.T) {
	dir := idxVault(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	// 建完立刻体检：fresh，且**不产** warning。
	code, out, errOut := runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("fresh 态 status 退出码 = %d：%s", code, errOut)
	}
	if f := idxFreshness(t, out); f != string(index.FreshnessFresh) {
		t.Fatalf("freshness = %q，期望 %q", f, index.FreshnessFresh)
	}
	if got := idxWarnCodes(t, out); len(got) != 0 {
		t.Fatalf("fresh 态不得产 warning，实得 %v", got)
	}
	digestBefore, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("索引应可读：%v", err)
	}

	// 用户在编辑器里直接改了一张卡（未提交）：索引立刻陈旧。
	idxEditCardExternally(t, dir, store.CardRel("ai-infra", applyCardID), "\n补一句外部编辑。\n")
	code, out, errOut = runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("stale 态 status 退出码 = %d，期望 0（陈旧是诊断不是失败）：%s", code, errOut)
	}
	if f := idxFreshness(t, out); f != string(index.FreshnessStale) {
		t.Fatalf("freshness = %q，期望 %q", f, index.FreshnessStale)
	}
	if got := idxWarnCodes(t, out); strings.Join(got, ",") != index.CodeIndexStale {
		t.Fatalf("warnings = %v，期望恰一条 %s", got, index.CodeIndexStale)
	}
	// 库本身仍是 healthy（陈旧 ≠ 损坏），且这两格必须能各自读出来。
	if h := idxHealth(t, out); h != string(index.HealthHealthy) {
		t.Fatalf("health = %q，期望 %q（陈旧不等于损坏）", h, index.HealthHealthy)
	}
	if got := idxString(t, out, "blocks_read"); got != "false" {
		t.Fatalf("blocks_read = %q，期望 false（索引任何状态都不阻断读）", got)
	}
	if got := idxString(t, out, "use_index"); got != "false" {
		t.Fatalf("use_index = %q，期望 false（陈旧索引不可信，读侧必须走全量扫描）", got)
	}
	if got := idxString(t, out, "changed_modified"); got != "1" {
		t.Fatalf("changed_modified = %q，期望 1（恰一张卡被外部改了）", got)
	}
	// 只读复核：判陈旧要读一遍权威，但一个字节都不许写回索引。
	digestAfter, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("status 之后索引应仍可读：%v", err)
	}
	if digestBefore != digestAfter {
		t.Fatal("status 顺手把索引同步了：它必须只读（收敛走 eg index sync）")
	}
	idxAssertStandingNotices(t, out)
}

// TestIndexStatusStrictIgnoresQuickPath：`--strict` 与默认路径的差别**恰**在快路径上。
//
// 语料是快路径唯一的假阴性场景：内容变了，但 `(size, mtime)` 逐格未变
// （外部工具原地改写 + 还原 mtime 完全做得到）。默认路径因此说 fresh，`--strict` 说 stale ——
// 这不是两套判定口径，而是「省不省一次 hash 计算」的直接后果，合同 §5.1 为此才要求有 --strict。
func TestIndexStatusStrictIgnoresQuickPath(t *testing.T) {
	dir := idxVault(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	rel := store.CardRel("ai-infra", applyCardID)
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	st, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat %s 失败：%v", rel, err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", rel, err)
	}
	// 等长改写：把正文里的「加权求和」改成「加权均值」（同字数 ⇒ 同字节数）。
	edited := strings.Replace(string(raw), "加权求和", "加权均值", 1)
	if edited == string(raw) || len(edited) != len(raw) {
		t.Fatalf("语料前提不成立：需要一次等长改写（前 %d 字节、后 %d 字节）", len(raw), len(edited))
	}
	if err := os.WriteFile(abs, []byte(edited), 0o644); err != nil {
		t.Fatalf("写 %s 失败：%v", rel, err)
	}
	// 还原 mtime：让 (size, mtime) 与索引记录逐格一致。
	if err := os.Chtimes(abs, st.ModTime(), st.ModTime()); err != nil {
		t.Fatalf("还原 mtime 失败：%v", err)
	}
	// 只读性基线**在跑任何 status 之前**取：strict 之后逐字比对（见文末四条判据）。
	beforeStatus := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain"))
	beforeCommits := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD"))
	authBefore := idxAuthoritySnapshot(t, dir)

	code, out, errOut := runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("默认 status 退出码 = %d：%s", code, errOut)
	}
	if f := idxFreshness(t, out); f != string(index.FreshnessFresh) {
		t.Fatalf("默认路径 freshness = %q，期望 %q（快路径按 (size, mtime) 判未变更）",
			f, index.FreshnessFresh)
	}
	if got := idxString(t, out, "strict"); got != "false" {
		t.Fatalf("默认路径 strict = %q，期望 false", got)
	}

	code, out, errOut = runIndexCLI(t, dir, "status", "--strict")
	if code != ExitOK {
		t.Fatalf("--strict status 退出码 = %d，期望 0：%s", code, errOut)
	}
	if got := idxString(t, out, "strict"); got != "true" {
		t.Fatalf("--strict 未进 data.index.strict：%q", got)
	}
	if f := idxFreshness(t, out); f != string(index.FreshnessStale) {
		t.Fatalf("--strict freshness = %q，期望 %q（全量重算 content_hash 必须抓到等长改写）",
			f, index.FreshnessStale)
	}
	if got := idxWarnCodes(t, out); strings.Join(got, ",") != index.CodeIndexStale {
		t.Fatalf("--strict warnings = %v，期望恰一条 %s", got, index.CodeIndexStale)
	}

	// --strict 同样是只读：强校验读一遍全库，但不写权威 Markdown、不写 Git、不发 commit。
	//
	// **T069-FLAKE-1 的根因与确定性化（T-…-069 阶段 A）**：原判据写死「工作区脏度 == 恰 1 行」，
	// 但这一行出不出来**由 Git 的 racy-clean 启发式决定，不由 eg 决定** —— 本用例刻意把
	// (size, mtime) 还原成与索引逐格一致，而 Git 暂存区条目同样先按 (size, mtime) 判「未变更」：
	//   · Git index 写入时刻与文件 mtime **不同秒** ⇒ Git 信 stat cache，报 clean ⇒ 脏度 0；
	//   · **同秒**（entry racily clean）⇒ Git 回落去比内容，报 modified ⇒ 脏度 1。
	// 前置 apply / commit 与这次等长改写落在哪一秒取决于机器负载，于是「16 次整包偶发 1 次红」；
	// 本轮 4 路并发整包实测 2/4 红，根因定量复现（记录在 T-…-069 Activity Log）。
	// 这**不是**产品缺陷：A-44 的新鲜度语义与 --strict 的只读性一格未动，红的是判据的**夹具**。
	//
	// 修法：把判据钉回「eg 自己的效果」，不再引用 Git 的启发式，且四条一起判（比原判据更强）：
	//   ① **只读**：跑完两次 status 后 `git status --porcelain` 与 status 之前**逐字相等**；
	//   ② **路径封闭**：脏度 ≤ 1，且真出现时那一行的路径**恰为**被外部改过的这张卡
	//      —— `.index/` 永远不许泄漏进 Git（这是原判据「.index/ 被忽略」的真正意图）；
	//   ③ **改写为真**：文件已变这件事由**字节**证明（读回磁盘比对等长改写后的期望内容），
	//      不再依赖「Git 报不报脏」来间接证明；
	//   ④ **零 commit**：`git rev-list --count HEAD` 前后相等；权威 Markdown 快照逐字未变。
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != beforeStatus {
		t.Fatalf("status --strict 改动了工作区：前 %q → 后 %q（strict 必须只读）", beforeStatus, got)
	}
	if beforeStatus != "" {
		if lines := len(strings.Split(beforeStatus, "\n")); lines != 1 {
			t.Fatalf("工作区脏度 = %d 行，期望 ≤ 1（只可能是那张被外部改过的卡）：%q", lines, beforeStatus)
		}
		// porcelain 行形如 " M domains/ai-infra/cards/k-….md"：路径必须恰是那张卡。
		if fields := strings.Fields(beforeStatus); len(fields) < 2 || fields[len(fields)-1] != rel {
			t.Fatalf("脏文件路径 = %q，期望恰 %q（.index/ 等派生物绝不许进 Git）", beforeStatus, rel)
		}
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD")); got != beforeCommits {
		t.Fatalf("commit 数变了：前 %s → 后 %s（status 恒 0 次 commit）", beforeCommits, got)
	}
	if raw, err := os.ReadFile(abs); err != nil {
		t.Fatalf("回读 %s 失败：%v", rel, err)
	} else if string(raw) != edited {
		t.Fatalf("磁盘字节与预期改写不符：strict 只读，任何一侧都不许把这张卡改回去")
	}
	idxAssertAuthorityUnchanged(t, dir, authBefore)
}

// TestIndexSyncConvergesAndIsIdempotent：`eg index sync` 把陈旧索引收敛到 fresh，
// 结果与全量重建等价，且第二次 sync 恒 noop（零写入）。
func TestIndexSyncConvergesAndIsIdempotent(t *testing.T) {
	dir := idxVault(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	idxEditCardExternally(t, dir, store.CardRel("ai-infra", applyCardID), "\n增量收敛用的一句。\n")

	code, out, errOut := runIndexCLI(t, dir, "sync")
	if code != ExitOK {
		t.Fatalf("sync 退出码 = %d，期望 0：%s", code, errOut)
	}
	if a := idxString(t, out, "action"); a != string(index.ActionSynced) {
		t.Fatalf("action = %q，期望 %q", a, index.ActionSynced)
	}
	if got := idxString(t, out, "degraded"); got != "false" {
		t.Fatalf("degraded = %q，期望 false（索引健康，无需退化）", got)
	}
	if got := idxString(t, out, "changed_modified"); got != "1" {
		t.Fatalf("changed_modified = %q，期望 1（只碰受影响的那一张卡）", got)
	}
	if got := idxString(t, out, "changed_unchanged"); got != "1" {
		t.Fatalf("changed_unchanged = %q，期望 1（另一张卡的行原样保留）", got)
	}
	synced, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("收敛后索引应可读：%v", err)
	}

	// 收敛后立刻 status：fresh、零 warning。
	code, out, errOut = runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("收敛后 status 退出码 = %d：%s", code, errOut)
	}
	if f := idxFreshness(t, out); f != string(index.FreshnessFresh) {
		t.Fatalf("收敛后 freshness = %q，期望 %q", f, index.FreshnessFresh)
	}
	if got := idxWarnCodes(t, out); len(got) != 0 {
		t.Fatalf("收敛后不得产 warning，实得 %v", got)
	}

	// 幂等：再 sync 一次恒 noop，且 Digest 逐字不变。
	code, out, errOut = runIndexCLI(t, dir, "sync")
	if code != ExitOK {
		t.Fatalf("第二次 sync 退出码 = %d：%s", code, errOut)
	}
	if a := idxString(t, out, "action"); a != string(index.ActionSyncNoop) {
		t.Fatalf("第二次 sync action = %q，期望 %q（幂等：零写入）", a, index.ActionSyncNoop)
	}
	again, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("noop 之后索引应仍可读：%v", err)
	}
	if again != synced {
		t.Fatal("noop 分支改动了索引内容：它必须零写入")
	}

	// 等价：增量收敛的结果与整库重建在 Digest 口径下逐字相同。
	if code, _, errOut = runIndexCLI(t, dir, "rebuild"); code != ExitOK {
		t.Fatalf("对照 rebuild 退出码 = %d：%s", code, errOut)
	}
	full, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("重建索引应可读：%v", err)
	}
	if full != synced {
		t.Fatal("增量收敛的结果与全量重建不等价：这是 M5 最不能出问题的一条")
	}
}

// TestIndexSyncDegradesHonestly：索引缺失 → 退化全量构建（留痕 W23）；
// 索引损坏 → 退化整库重建（留痕 W24）。退化一律**如实交代**，绝不静默。
func TestIndexSyncDegradesHonestly(t *testing.T) {
	dir := idxVault(t)

	// 缺失：sync 直接退化成全量构建。
	code, out, errOut := runIndexCLI(t, dir, "sync")
	if code != ExitOK {
		t.Fatalf("缺失态 sync 退出码 = %d，期望 0：%s", code, errOut)
	}
	if a := idxString(t, out, "action"); a != string(index.ActionSyncBuilt) {
		t.Fatalf("action = %q，期望 %q", a, index.ActionSyncBuilt)
	}
	if got := idxString(t, out, "degraded"); got != "true" {
		t.Fatalf("degraded = %q，期望 true（退化必须写在明面上）", got)
	}
	if got := idxWarnCodes(t, out); strings.Join(got, ",") != index.CodeIndexMissing {
		t.Fatalf("warnings = %v，期望恰一条 %s（原本缺索引要留痕）", got, index.CodeIndexMissing)
	}
	if h := idxHealth(t, out); h != string(index.HealthHealthy) {
		t.Fatalf("退化构建后 health = %q，期望 %q", h, index.HealthHealthy)
	}
	built, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("退化构建后索引应可读：%v", err)
	}

	// 损坏：sync 退化成整库重建，结果与全量构建等价。
	idxCorruptDB(t, dir)
	code, out, errOut = runIndexCLI(t, dir, "sync")
	if code != ExitOK {
		t.Fatalf("损坏态 sync 退出码 = %d，期望 0：%s", code, errOut)
	}
	if a := idxString(t, out, "action"); a != string(index.ActionSyncRebuilt) {
		t.Fatalf("action = %q，期望 %q", a, index.ActionSyncRebuilt)
	}
	if got := idxWarnCodes(t, out); strings.Join(got, ",") != index.CodeIndexCorrupt {
		t.Fatalf("warnings = %v，期望恰一条 %s（修好了也要说清修的是什么）",
			got, index.CodeIndexCorrupt)
	}
	rebuilt, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("退化重建后索引应可读：%v", err)
	}
	if rebuilt != built {
		t.Fatal("退化重建的结果与全量构建不等价：两者必须走同一条全量路径")
	}
}

// TestWriteCommandSyncsIndexAfterMarkdownLanded：写命令在 Markdown 落盘 + commit 成功之后
// **自动**把索引带到 fresh，且不改变退出码、不改变权威内容以外的任何事实。
//
// 走 `eg apply`（runPlan 是 apply / edit / deprecate / restore / replaced-by / rel 的共同写口）：
// 这一条同时钉住了那几条命令的写后同步。
func TestWriteCommandSyncsIndexAfterMarkdownLanded(t *testing.T) {
	dir := idxVault(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	before, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("索引应可读：%v", err)
	}

	// 一次真实写入：新建第三张卡（Markdown 落盘 + 恰一次 commit）。
	code, env, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-sync", ""))
	if code != ExitOK {
		t.Fatalf("写命令退出码 = %d，期望 0：%s", code, errOut)
	}
	// 键面零漂移：写后同步只许往既有报告结构里追加诊断，不许多出 data 键。
	if _, ok := env.Data["index"]; ok {
		t.Fatal("写命令的 data 里出现了 index 键：写后同步只许追加既有诊断结构")
	}
	rep := applyReport(t, env)
	for _, w := range rep.Warnings {
		if w.Code == index.CodeIndexStale {
			t.Fatalf("写后同步本该成功，却报了 %s：%s", w.Code, w.Message)
		}
	}
	after, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("写后索引应可读：%v", err)
	}
	if after == before {
		t.Fatal("写命令没有把新卡同步进索引：写后同步没生效")
	}

	// 索引已经跟上权威：status 判 fresh（不需要用户手工 sync）。
	code, out, errOut := runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("写后 status 退出码 = %d：%s", code, errOut)
	}
	if f := idxFreshness(t, out); f != string(index.FreshnessFresh) {
		t.Fatalf("写后 freshness = %q，期望 %q（写命令应已把索引带到 fresh）",
			f, index.FreshnessFresh)
	}
	// 等价：写后增量的结果与整库重建逐字相同。
	if code, _, errOut = runIndexCLI(t, dir, "rebuild"); code != ExitOK {
		t.Fatalf("对照 rebuild 退出码 = %d：%s", code, errOut)
	}
	full, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("重建索引应可读：%v", err)
	}
	if full != after {
		t.Fatal("写后增量的结果与全量重建不等价")
	}
	if n := rcPorcelainCount(t, dir); n != 0 {
		t.Fatalf("写后工作区脏了 %d 行：.index/ 必须被整目录忽略", n)
	}
}

// TestWriteCommandNeverBuildsOrRepairsIndex：索引缺失 → 写命令**静默跳过**（不替用户建库）；
// 索引损坏 → 写命令照常成功（退出码不变）、如实报 W24、且**不自动修**。
func TestWriteCommandNeverBuildsOrRepairsIndex(t *testing.T) {
	dir := idxVault(t)

	// ① 缺失：写命令不建索引、不产任何索引诊断。
	code, env, errOut := runApplyPlan(t, dir, cardPlan("k-20260901-nobuild", ""))
	if code != ExitOK {
		t.Fatalf("写命令退出码 = %d，期望 0：%s", code, errOut)
	}
	// M6 · T-…-072 B2c1 精确重钉：判据从「`.index/` 不存在」收紧为「一个索引产物都没有」。
	// 写命令走 plan 写链，合同 §2 要求它先取 `.index/run.lock` 再校验，并在 `.index/txn/`
	// 里留下事务日志 —— 那是**事务层**在盘的必然痕迹，与「谁建了索引」是两件事。
	// 本条钉的一直是后者：写命令绝不替用户造索引库。
	idxAssertNoIndexArtifacts(t, dir, "写命令替用户建了索引")
	for _, w := range applyReport(t, env).Warnings {
		if w.Code == index.CodeIndexStale || w.Code == index.CodeIndexMissing ||
			w.Code == index.CodeIndexCorrupt {
			t.Fatalf("索引未建是正常状态，不该产诊断：%s / %s", w.Code, w.Message)
		}
	}

	// ② 损坏：写命令照常退 0 且照常 commit，索引状态如实保持 corrupt。
	if code, _, errOut = runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	idxCorruptDB(t, dir)
	commitsBefore := gitLogCount(t, dir)
	code, env, errOut = runApplyPlan(t, dir, cardPlan("k-20260901-broken", ""))
	if code != ExitOK {
		t.Fatalf("坏索引不许拖垮写命令：退出码 = %d，期望 0：%s", code, errOut)
	}
	if n := gitLogCount(t, dir); n != commitsBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1（索引不是写命令的前置）", commitsBefore, n)
	}
	// **Schema v2 · T-…-003 重钉（事实变了，判据形态不变）**：报告的 warnings[] 是
	// **warning 与 info 共用**的一个篮子（分级由 level 表达）。v2 兼容期起，同一次 apply
	// 会多出 info 级的兼容交代（例如 `plan_version 1 已进入兼容期`），原断言把整篮子
	// 的码拼成字符串比对，等于让「有没有 info 级提示」也变成本用例的判据 —— 而本用例
	// 钉的是「坏索引只产恰一条 W24，且不自动修」。
	//
	// 因此按 level 分流后再断言：**warning 及以上**恰一条且逐字为 W24（判据没放宽，
	// 仍然禁止第二条 warning 混进来），info 级另外单独复核「不含任何索引码」——
	// 否则把索引诊断降级成 info 就能绕过本判据。
	var warnCodes, infoCodes []string
	for _, w := range applyReport(t, env).Warnings {
		if w.Code == "" {
			continue
		}
		if w.Level == report.LevelInfo {
			infoCodes = append(infoCodes, w.Code)
			continue
		}
		warnCodes = append(warnCodes, w.Code)
	}
	if strings.Join(warnCodes, ",") != index.CodeIndexCorrupt {
		t.Fatalf("warning 级码集合 = %v，期望恰一条 %s（不静默跳过，也不自动修）",
			warnCodes, index.CodeIndexCorrupt)
	}
	for _, c := range infoCodes {
		if c == index.CodeIndexCorrupt || c == index.CodeIndexStale || c == index.CodeIndexMissing {
			t.Fatalf("索引诊断不得降级为 info：%v", infoCodes)
		}
	}
	if _, err := index.Digest(index.DirPath(dir)); err == nil {
		t.Fatal("写命令自动修好了坏索引：自动修复会掩盖「库为什么坏了」（修复走 eg index rebuild）")
	}
}

// TestIndexAfterWriteFiltersNonCardPaths：写后同步只把**知识卡**路径算成受影响行。
//
// 提案 / 评审 / 原文 / 笔记不在索引的对象面上，若把它们当成「受影响路径」，
// 就会因为「现态里查不到这些行」而被误判成删除。这条用例把过滤器钉在形态层面。
func TestIndexAfterWriteFiltersNonCardPaths(t *testing.T) {
	snap := index.Snapshot{
		Head: "cafebabe",
		Cards: []index.Card{{
			ID: applyCardID, Path: store.CardRel("ai-infra", applyCardID),
			Kind: index.CardKindKnowledge, Validation: "",
		}},
		Files: []index.File{{Path: store.CardRel("ai-infra", applyCardID), ContentHash: "h1"}},
	}
	d := indexDeltaFor(snap, []string{
		store.CardRel("ai-infra", applyCardID),          // 卡：算受影响
		store.NoteRel("ai-infra", applyNoteID),          // 笔记：索引里没有它
		store.SourceRel(applySourceID),                  // 原文：同上
		"proposals/2026/p-20260901-demo.md",             // 提案：同上
		store.CardRel("ai-infra", "k-20260901-missing"), // 卡但现态已消失 → Removed
	})
	if d.Head != "cafebabe" {
		t.Fatalf("Head = %q，期望逐字取本次落盘后的 HEAD", d.Head)
	}
	if len(d.Files) != 1 || d.Files[0].Path != store.CardRel("ai-infra", applyCardID) {
		t.Fatalf("受影响文件 = %v，期望恰那张卡", d.Files)
	}
	if len(d.Cards) != 1 {
		t.Fatalf("受影响卡行 = %d 条，期望 1", len(d.Cards))
	}
	if strings.Join(d.Removed, ",") != store.CardRel("ai-infra", "k-20260901-missing") {
		t.Fatalf("Removed = %v，期望恰那张已消失的卡（非卡路径一律不进 Removed）", d.Removed)
	}
	if !isIndexedRel(store.CardRel("ai-infra", applyCardID)) {
		t.Fatal("isIndexedRel 认不出标准卡路径")
	}
	if !isIndexedRel(store.OpinionRel("ai-infra", applyOpinionID)) {
		t.Fatal("isIndexedRel 认不出标准观点路径（观点与知识卡同批入索引对象面）")
	}
	for _, notCard := range []string{
		store.NoteRel("ai-infra", applyNoteID), store.SourceRel(applySourceID),
		"unprocessed.md", "domains/ai-infra/knowledge/sub/deep.md", "domains/ai-infra/knowledge",
	} {
		if isIndexedRel(notCard) {
			t.Fatalf("isIndexedRel 把 %q 也判成了索引对象面", notCard)
		}
	}
}
