package reconcile

// check 十三值封闭枚举 / 诊断码双向单射 / 第 14 值必失败三组用例（M-004 判据 1 与判据 2 消费；A-62 新增 W29），
// 外加三条包边界自守用例（零写盘 / 零 commit / 零子进程 + 依赖方向单向 + 越界能力零命中）：
// 手册与合同 §1.1 的 grep 反证在包内以 Go 用例形态复跑一遍，防止「门禁在别处、包内失守」。

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// nonTestSources 返回本包**非测试**源文件的「文件名 → 内容」。
// 三条 grep 反证的口径与 Acceptance 逐字一致：`grep -v _test.go`。
func nonTestSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读包目录失败：%v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("读 %s 失败：%v", name, err)
		}
		out[name] = string(raw)
	}
	if len(out) < 4 {
		t.Fatalf("非测试源文件仅 %d 个，期望 ≥ 4（doc.go / finding.go / check.go / reconcile.go）",
			len(out))
	}
	return out
}

// allSources 返回本包**全部** .go 文件（含测试）的「文件名 → 内容」。
func allSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读包目录失败：%v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("读 %s 失败：%v", e.Name(), err)
		}
		out[e.Name()] = string(raw)
	}
	return out
}

// TestCheckEnumExactlyThirteenValues 反证 check 是恰 13 值的封闭枚举：
// 值与顺序逐字等于合同 §3 表格、互不重复、逐个 IsKnownCheck 为真，
// 且真源表是**长度固定的数组**（第 14 行编译不过，这是编译期封闭的落点）。
func TestCheckEnumExactlyThirteenValues(t *testing.T) {
	want := []string{
		"git_uncommitted",
		"reviewed_at_missing",
		"duplicate_id",
		"dangling_ref",
		"orphan",
		"relation_target_missing",
		"relation_prefix_invalid",
		"relation_opposing_asymmetric",
		"relation_duplicate",
		"domain_moved",
		"recap_stale",
		"support_insufficient",
		"opinion_unsupported_validated",
	}
	if len(want) != CheckCount {
		t.Fatalf("用例期望表 %d 项，CheckCount = %d（两侧必须同为 13）", len(want), CheckCount)
	}
	got := AllChecks()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AllChecks() = %v，期望逐字 %v（顺序 = 合同 §3 表格行序）", got, want)
	}
	seen := map[string]bool{}
	for i, c := range got {
		if seen[c] {
			t.Fatalf("check %q 重复出现（枚举必须互不相同）", c)
		}
		seen[c] = true
		if !IsKnownCheck(c) {
			t.Errorf("IsKnownCheck(%q) = false", c)
		}
		if v, err := ParseCheck(c); err != nil || v != c {
			t.Errorf("ParseCheck(%q) = %q, %v", c, v, err)
		}
		if spec, ok := SpecOf(c); !ok || spec.Check != c || spec.Code == "" || spec.R == "" {
			t.Errorf("SpecOf(%q) = %+v, %v：四元组不完整", c, spec, ok)
		}
		if i >= CheckCount {
			t.Fatalf("枚举越界：第 %d 项", i+1)
		}
	}
	if n := len(checkIndex); n != CheckCount {
		t.Fatalf("check 索引 %d 项，期望恰 %d", n, CheckCount)
	}
	// 编译期封闭的机器反证：真源表类型是 [13]CheckSpec 数组，不是可追加的 slice。
	tt := reflect.TypeOf(checkTable)
	if tt.Kind() != reflect.Array || tt.Len() != CheckCount {
		t.Fatalf("checkTable 类型 = %s，必须是长度恰 %d 的数组（第 14 行编译不过）",
			tt, CheckCount)
	}
	if st := reflect.TypeOf(severityTable); st.Kind() != reflect.Array || st.Len() != SeverityCount {
		t.Fatalf("severityTable 类型 = %s，必须是长度恰 %d 的数组", st, SeverityCount)
	}
	// 七项 R 检查逐个在册（R1 ~ R7 全覆盖，无第八个 R）。
	rs := map[string]bool{}
	for _, s := range Specs() {
		rs[s.R] = true
	}
	for _, r := range []string{"R1", "R2", "R3", "R4", "R5", "R6", "R7"} {
		if !rs[r] {
			t.Errorf("真源表未覆盖 %s", r)
		}
	}
	if len(rs) != 7 {
		t.Fatalf("R 编号集合 = %d 项，期望恰 7", len(rs))
	}
}

// TestCheckDiagnosticBijection 反证 check ↔ 诊断码**双向单射**：13 对 13、正反查互为逆、
// 码集合恰 E11–E14 ∪ W13–W20 ∪ {W29}，且 severity 与码段一致（error 段 E、warning 段 W）。
func TestCheckDiagnosticBijection(t *testing.T) {
	want := map[string]string{
		"git_uncommitted":               "W13",
		"reviewed_at_missing":           "W14",
		"duplicate_id":                  "E11",
		"dangling_ref":                  "E12",
		"orphan":                        "W17",
		"relation_target_missing":       "E13",
		"relation_prefix_invalid":       "E14",
		"relation_opposing_asymmetric":  "W15",
		"relation_duplicate":            "W16",
		"domain_moved":                  "W18",
		"recap_stale":                   "W19",
		"support_insufficient":          "W20",
		"opinion_unsupported_validated": "W29",
	}
	if len(want) != CodeCount {
		t.Fatalf("用例期望表 %d 项，CodeCount = %d", len(want), CodeCount)
	}
	codes := map[string]string{} // code → check（反向唯一性）
	for check, code := range want {
		t.Run(check, func(t *testing.T) {
			got, ok := CodeOf(check)
			if !ok || got != code {
				t.Fatalf("CodeOf(%q) = %q, %v，期望 %q", check, got, ok, code)
			}
			back, ok := CheckOfCode(code)
			if !ok || back != check {
				t.Fatalf("CheckOfCode(%q) = %q, %v，期望 %q", code, back, ok, check)
			}
			spec, ok := SpecOfCode(code)
			if !ok || spec.Check != check {
				t.Fatalf("SpecOfCode(%q) = %+v, %v", code, spec, ok)
			}
			sev, ok := SeverityOf(check)
			if !ok {
				t.Fatalf("SeverityOf(%q) 未命中", check)
			}
			wantPrefix := "W"
			if sev == SeverityError {
				wantPrefix = "E"
			}
			if !strings.HasPrefix(code, wantPrefix) {
				t.Fatalf("check %q 的 severity = %q 但码 = %q（error 段用 E、warning 段用 W）",
					check, sev, code)
			}
		})
		if prev, dup := codes[code]; dup {
			t.Fatalf("诊断码 %q 同时对应 %q 与 %q：单射被破坏", code, prev, check)
		}
		codes[code] = check
	}
	if len(codes) != CodeCount || len(codeIndex) != CodeCount {
		t.Fatalf("码集合 %d 项 / 索引 %d 项，期望均为恰 %d", len(codes), len(codeIndex), CodeCount)
	}
	all := AllCodes()
	if len(all) != CodeCount {
		t.Fatalf("AllCodes() = %d 项，期望恰 %d", len(all), CodeCount)
	}
	for i, c := range AllChecks() {
		if all[i] != want[c] {
			t.Fatalf("AllCodes()[%d] = %q，与 AllChecks()[%d] = %q 不逐位对应", i, all[i], i, c)
		}
	}
	// 码段闭合：E 段恰 4（E11–E14）、W 段恰 9（W13–W20 ∪ W29，A-62 新增）。
	nE, nW := 0, 0
	for code := range codes {
		switch code[:1] {
		case "E":
			nE++
		case "W":
			nW++
		default:
			t.Fatalf("诊断码 %q 落在 E / W 之外的段（Q 段属只读查询域，不进本表）", code)
		}
	}
	if nE != 4 || nW != 9 {
		t.Fatalf("E 段 %d / W 段 %d，期望恰 4 / 9", nE, nW)
	}
}

// TestCheckEnumRejectsFourteenthValue 反证第 14 个取值必失败：
// 未分配的 check、越号的诊断码、未启用的第三个分级一律被拒，且计数不因此增长。
func TestCheckEnumRejectsFourteenthValue(t *testing.T) {
	thirteenth := []struct {
		name  string
		value string
	}{
		{"第 14 个 check（关系环）", "relation_" + "cycle"},
		{"第 14 个 check（材料过载）", "support_" + "excessive"},
		{"第 14 个 check（索引失效）", "cache_" + "stale"},
		{"空串", ""},
		{"大小写变形", "Duplicate_ID"},
		{"前后空白变形", " duplicate_id "},
		{"复数变形", "duplicate_ids"},
		{"诊断码冒充 check", "W13"},
	}
	for _, c := range thirteenth {
		t.Run(c.name, func(t *testing.T) {
			if IsKnownCheck(c.value) {
				t.Fatalf("IsKnownCheck(%q) = true：枚举必须恰 %d 值封闭", c.value, CheckCount)
			}
			if _, err := ParseCheck(c.value); err == nil {
				t.Fatalf("ParseCheck(%q) 未报错", c.value)
			}
			if _, ok := SpecOf(c.value); ok {
				t.Fatalf("SpecOf(%q) 命中了表外取值", c.value)
			}
			if _, err := NewFinding(c.value, []string{"k-a1"}, "x"); err == nil {
				t.Fatalf("NewFinding(%q) 未报错：表外 check 不得构造出 finding", c.value)
			}
			if _, err := NewRepairSpec(c.value, "p.md", []string{"k"}, "r"); err == nil {
				t.Fatalf("NewRepairSpec(%q) 未报错", c.value)
			}
		})
	}
	// 越号诊断码：E 段止于 E14、W 段止于 W20 再加 A-62 新增的 W29；W21 属 plan 域、W30 未分配，
	// 查询域的码不进本表；信息级码不对应任何 check。
	for _, code := range []string{"E" + "15", "E" + "10", "W" + "21", "W" + "12",
		"W" + "30", "I" + "2", "Q" + "4", ""} {
		if IsKnownCode(code) {
			t.Errorf("IsKnownCode(%q) = true：本包诊断码是恰 %d 值封闭集合", code, CodeCount)
		}
		if _, err := ParseCode(code); err == nil {
			t.Errorf("ParseCode(%q) 未报错", code)
		}
		if _, ok := CheckOfCode(code); ok {
			t.Errorf("CheckOfCode(%q) 命中了表外码", code)
		}
	}
	// 计数没被上面的尝试撑大（封闭表是只读真源）。
	if len(AllChecks()) != CheckCount || len(AllCodes()) != CodeCount ||
		len(AllSeverities()) != SeverityCount {
		t.Fatalf("封闭计数被改动：check %d / code %d / severity %d",
			len(AllChecks()), len(AllCodes()), len(AllSeverities()))
	}
	// 副本语义：改 Specs() 的返回值不污染真源表。
	sp := Specs()
	sp[0].Check = "tampered"
	if AllChecks()[0] != CheckGitUncommitted {
		t.Fatal("Specs() 返回的不是副本：真源表被外部改动")
	}
}

// TestPackageBoundaryZeroWriteZeroCommitZeroExec 是包边界自守用例：
// 合同 §1.1 的三条 grep 反证在包内复跑（零写盘 / 零 commit / 零子进程），
// 口径与 Acceptance 逐字一致（只扫非测试源文件）。
func TestPackageBoundaryZeroWriteZeroCommitZeroExec(t *testing.T) {
	forbidden := []struct {
		name  string
		token string
	}{
		{"写文件", "os.WriteFile"},
		{"建文件", "os.Create"},
		{"删文件", "os.Remove"},
		{"改名", "os.Rename"},
		{"打开可写句柄", "os.OpenFile"},
		{"建目录", "os.Mkdir"},
		{"写权限位", "io/ioutil"},
		{"发提交", "Commit("},
		{"暂存", ".Add()"},
		{"子进程", "os" + "/exec"},
		{"子进程调用", "exec.Command"},
		{"进程退出", "os." + "Exit"},
	}
	src := nonTestSources(t)
	for _, f := range forbidden {
		t.Run(f.name, func(t *testing.T) {
			for name, body := range src {
				if strings.Contains(body, f.token) {
					t.Errorf("%s 出现 %q：本包零写盘、零 commit、零子进程（合同 §1.1）",
						name, f.token)
				}
			}
		})
	}
}

// TestPackageDependencyDirectionOneWay 是依赖方向自守用例：
// 只允许 reconcile → {model, mdfile, git 只读 API, query}；
// store / plan / cli / report / proposal 一律零命中（合同 §1.2）。
func TestPackageDependencyDirectionOneWay(t *testing.T) {
	allowed := map[string]bool{
		"github.com/ikaqiu-Lemon/EverGreen/internal/model":   true,
		"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile":  true,
		"github.com/ikaqiu-Lemon/EverGreen/internal/git":     true,
		"github.com/ikaqiu-Lemon/EverGreen/internal/query":   true,
		"github.com/ikaqiu-Lemon/EverGreen/internal/version": true,
	}
	fset := token.NewFileSet()
	for name := range nonTestSources(t) {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, "github.com/ikaqiu-Lemon/EverGreen/") {
				continue // 标准库不受依赖方向约束
			}
			if !allowed[path] {
				t.Errorf("%s import %q：允许依赖只有 model / mdfile / git（只读 API） / query",
					name, path)
			}
		}
	}
	// 禁止依赖的五个包逐名反证（含子包前缀），口径与 Acceptance 的 grep 一致。
	for _, bad := range []string{"internal/store", "internal/plan", "internal/cli",
		"internal/report", "internal/proposal"} {
		for name, body := range nonTestSources(t) {
			if strings.Contains(body, bad) {
				t.Errorf("%s 出现 %q：依赖方向单向，反向或跨层依赖即成环", name, bad)
			}
		}
	}
	// ADR-20 的文件级隔离不放宽：三个被隔离的落点在**全部**文件（含测试）零命中。
	for _, iso := range []string{"query" + "/rank", "query" + "/relations",
		"rules" + "/converge", "query" + "/review"} {
		for name, body := range allSources(t) {
			if strings.Contains(body, iso) {
				t.Errorf("%s 出现 %q：ADR-20 文件级隔离在本包一字不放宽", name, iso)
			}
		}
	}
	// 反向依赖：本包不得被上游包 import —— Go 侧由 cmd/eg/arch_test.go 的
	// TestStage3ReconcilePackageBoundary 用 go list 逐包反证（本用例只钉正向）。
}

// TestPackageNoOutOfScopeCapability 反证 S4 / M5 与 S5 / M6 的能力一条未提前：
// 缓存目录 / 全文检索引擎 / 嵌入式数据库 / 文件锁 / 性能门槛 / 第五个退出码在**全部**文件零命中。
// 词表用拼接构造，避免本用例自身成为门禁的命中项。
func TestPackageNoOutOfScopeCapability(t *testing.T) {
	tokens := []struct{ name, token string }{
		{"缓存目录", "." + "index/"},
		{"全文检索引擎", "FTS" + "5"},
		{"嵌入式数据库（小写）", "s" + "qlite"},
		{"嵌入式数据库（驼峰）", "S" + "QLite"},
		{"文件锁", "f" + "lock"},
		{"运行锁文件", "run" + ".lock"},
		{"事务目录", "txn" + "/"},
		{"性能门槛", "P" + "95"},
		{"第五个退出码", "os." + "Exit(" + "5)"},
	}
	for _, tk := range tokens {
		t.Run(tk.name, func(t *testing.T) {
			for name, body := range allSources(t) {
				if strings.Contains(body, tk.token) {
					t.Errorf("%s 出现越界能力字样 %q（属 S4 / M5 或 S5 / M6，本包不做）",
						name, tk.token)
				}
			}
		})
	}
}

// TestRepairSpecIsDescriptionOnly 反证 RepairSpec 只描述「该怎么修」、不含任何落盘动作：
// 全部字段都是数据字段（无函数字段、无接口句柄），方法集恰只有 Validate。
func TestRepairSpecIsDescriptionOnly(t *testing.T) {
	typ := reflect.TypeOf(RepairSpec{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		switch f.Type.Kind() {
		case reflect.String:
		case reflect.Slice:
			if f.Type.Elem().Kind() != reflect.String {
				t.Errorf("字段 %s 的元素类型 = %s，只允许字符串", f.Name, f.Type.Elem().Kind())
			}
		default:
			t.Errorf("字段 %s 的类型 = %s：修复意向只允许数据字段（不得挂函数 / 句柄）",
				f.Name, f.Type.Kind())
		}
	}
	var methods []string
	for i := 0; i < typ.NumMethod(); i++ {
		methods = append(methods, typ.Method(i).Name)
	}
	if len(methods) != 1 || methods[0] != "Validate" {
		t.Fatalf("RepairSpec 方法集 = %v，期望恰 [Validate]（不得有 Apply / Write 类动作）",
			methods)
	}
	// 构造与校验：check 必须在封闭枚举内，待写键去重升序，缺任一要素即报错。
	rs, err := NewRepairSpec(CheckReviewedAtMissing, "domains/ai/cards/k-a1.md",
		[]string{"reviewed_at", "reviewed_at"}, "用户直接编辑后 reviewed_at 缺失")
	if err != nil {
		t.Fatalf("NewRepairSpec 失败：%v", err)
	}
	if !reflect.DeepEqual(rs.Keys, []string{"reviewed_at"}) {
		t.Fatalf("Keys = %v，期望去重后的 [reviewed_at]", rs.Keys)
	}
	if err := rs.Validate(); err != nil {
		t.Fatalf("Validate 失败：%v", err)
	}
	for _, bad := range []RepairSpec{
		{Check: CheckReviewedAtMissing, Path: "", Keys: []string{"reviewed_at"}, Reason: "r"},
		{Check: CheckReviewedAtMissing, Path: "p.md", Keys: nil, Reason: "r"},
		{Check: CheckReviewedAtMissing, Path: "p.md", Keys: []string{"reviewed_at"}, Reason: " "},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v：Validate 应报错", bad)
		}
	}
	// Run 的骨架语义：本 task 零检查项注册 → 空集合（非 nil），且 findings / repairs 均已排序。
	res := Run(Input{VaultRoot: filepath.Join("testdata", "vault")})
	if res.Findings == nil || res.Repairs == nil {
		t.Fatal("Run 返回 nil 集合：空结果必须是 []（与报告侧 findings 恒非 null 同源）")
	}
	if len(res.Findings) != 0 || len(res.Repairs) != 0 {
		t.Fatalf("Run 产出 %d finding / %d repair：T-…-049 只交付骨架，R1–R7 一项未实现",
			len(res.Findings), len(res.Repairs))
	}
	if res.HasError() || len(res.CheckSet()) != 0 {
		t.Fatal("空结果不得有 error 级 finding，check 集合必须为空")
	}
}
