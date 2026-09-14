package reconcile

// check 十二值封闭枚举、severity 恰两值、check ↔ 诊断码双向单射（对账合同 §3）。
//
// 本文件是 M4 全部 R 检查项的**唯一枚举真源**：T-…-050 ~ T-…-056 构造 finding 时一律
// 引用这里的常量与 checkTable，不得在各自文件里另写一份字面量（「同一事实两处定义」
// 在 M4 内因此不可能发生）。
//
// 封闭性的三重保证：
//   - **编译期**：checkTable 是长度恰 CheckCount 的**数组**（不是 slice），
//     多写第 13 行或漏写一行都编译不过；severityTable 同型（长度恰 SeverityCount）。
//   - **运行期**：ParseCheck / ParseSeverity / ParseCode 拒收表外取值。
//   - **表驱动用例**：check_test.go 的 TestCheckEnumExactlyTwelveValues /
//     TestCheckDiagnosticBijection / TestCheckEnumRejectsThirteenthValue 三组。

import "fmt"

// check 的十二值封闭枚举（值与顺序 = 合同 §3 表格第 1 ~ 12 行，逐字）。
const (
	CheckGitUncommitted             = "git_uncommitted"              // 1 · R1
	CheckReviewedAtMissing          = "reviewed_at_missing"          // 2 · R2
	CheckDuplicateID                = "duplicate_id"                 // 3 · R4
	CheckDanglingRef                = "dangling_ref"                 // 4 · R4
	CheckOrphan                     = "orphan"                       // 5 · R4
	CheckRelationTargetMissing      = "relation_target_missing"      // 6 · R3
	CheckRelationPrefixInvalid      = "relation_prefix_invalid"      // 7 · R3
	CheckRelationOpposingAsymmetric = "relation_opposing_asymmetric" // 8 · R3
	CheckRelationDuplicate          = "relation_duplicate"           // 9 · R3
	CheckDomainMoved                = "domain_moved"                 // 10 · R5
	CheckRecapStale                 = "recap_stale"                  // 11 · R6
	CheckSupportInsufficient        = "support_insufficient"         // 12 · R7
)

// severity 恰两值（合同 §2 / §3）：error 在前、warning 在后。
// 第三个分级（信息级）在 M4 **不启用**，登记为 S3 之后；本文件因此不出现它的字面量。
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// 诊断码：error 段 E11–E14、warning 段 W13–W20（合同 §3，与 check 双向单射）。
//
// W 段**恰止于 W20**：被默认隐藏的失效端点提示属只读查询域（Q 段，归 T-…-061），
// 既不进本表、也不占 W 段的下一个号（合同 §3 的 2026-11-12 阶段 B 修正）。
// 「本次对账零 finding」的信息条目同样不对应任何 check，故不在本表内（归 T-…-057）。
const (
	CodeE11 = "E11" // duplicate_id
	CodeE12 = "E12" // dangling_ref
	CodeE13 = "E13" // relation_target_missing
	CodeE14 = "E14" // relation_prefix_invalid
	CodeW13 = "W13" // git_uncommitted
	CodeW14 = "W14" // reviewed_at_missing
	CodeW15 = "W15" // relation_opposing_asymmetric
	CodeW16 = "W16" // relation_duplicate
	CodeW17 = "W17" // orphan
	CodeW18 = "W18" // domain_moved
	CodeW19 = "W19" // recap_stale
	CodeW20 = "W20" // support_insufficient
)

// 封闭计数（判据与用例逐字消费；改动这三个数字必须同步合同 §3 与 M-004 判据 3）。
const (
	// CheckCount 是 check 枚举的封闭基数：恰 12。
	CheckCount = 12
	// SeverityCount 是 severity 的封闭基数：恰 2。
	SeverityCount = 2
	// CodeCount 是本包新增诊断码的封闭基数：恰 12（E11–E14 四个 + W13–W20 八个）。
	CodeCount = CheckCount
)

// CheckSpec 是一个 check 的封闭四元组：check 值 + severity + 诊断码 + 所属 R 编号。
// R 只作可读标注（哪一项检查产它），不参与任何判定。
type CheckSpec struct {
	Check    string
	Severity string
	Code     string
	R        string
}

// checkTable 是**唯一真源**：恰 CheckCount 行，行序 = 合同 §3 表格行序。
//
// 类型是长度固定的数组而不是 slice —— 这就是「第 13 个取值在编译期失败」的落点：
// 追加第 13 行会得到 "index 12 out of bounds" 编译错误，删一行则少一个初始值、
// 表内出现零值行并被 check_test.go 的表驱动用例判红。
var checkTable = [CheckCount]CheckSpec{
	{CheckGitUncommitted, SeverityWarning, CodeW13, "R1"},
	{CheckReviewedAtMissing, SeverityWarning, CodeW14, "R2"},
	{CheckDuplicateID, SeverityError, CodeE11, "R4"},
	{CheckDanglingRef, SeverityError, CodeE12, "R4"},
	{CheckOrphan, SeverityWarning, CodeW17, "R4"},
	{CheckRelationTargetMissing, SeverityError, CodeE13, "R3"},
	{CheckRelationPrefixInvalid, SeverityError, CodeE14, "R3"},
	{CheckRelationOpposingAsymmetric, SeverityWarning, CodeW15, "R3"},
	{CheckRelationDuplicate, SeverityWarning, CodeW16, "R3"},
	{CheckDomainMoved, SeverityWarning, CodeW18, "R5"},
	{CheckRecapStale, SeverityWarning, CodeW19, "R6"},
	{CheckSupportInsufficient, SeverityWarning, CodeW20, "R7"},
}

// severityTable 是 severity 的封闭全集（长度固定数组：第三个分级加不进来）。
var severityTable = [SeverityCount]string{SeverityError, SeverityWarning}

// —— `targets` 顺序固定的封闭例外（合同 §8；M4 · T-…-054）——
//
// 合同 §2 第 3 条把 `targets[]` 定死为「去重 + 字典序升序」，那是**默认口径**：
// 十二个 check 里十一个的 targets 是**集合**（一批可定位标识，次序不承载语义）。
// 合同 §8 给 `domain_moved` 另定了一条：`targets[]` = `[对象 ID, 旧领域, 新领域]`
// —— **恰三元、顺序固定**，脚本按位取值（`targets[1]` 恒是旧领域、`targets[2]` 恒是新领域）。
// 这两条口径不可能同时成立（领域名与 ID 的字典序关系是任意的），故本文件把「顺序固定」
// 登记成一张**封闭例外表**：
//
//   - 表长是编译期固定的数组（`OrderedTargetsCheckCount` 恰 1）：想把第二个 check 也
//     豁免掉排序，编译期就过不去；
//   - 元数逐 check 写死（`domain_moved` 恰 3）：多一元少一元都会被 NewFinding 与
//     Validate 当场拒收（finding_test.go / r5_domain_test.go 双侧锁死）；
//   - 例外**只关掉排序与去重**，不关掉任何别的东西：非空、非 nil、check ↔ severity 单射
//     一格不放宽。
const (
	// OrderedTargetsCheckCount 是「targets 顺序固定」的 check 数：**恰 1**（`domain_moved`）。
	OrderedTargetsCheckCount = 1
	// DomainMovedTargetArity 是 `domain_moved` 的 targets 元数：**恰 3**
	// （`[对象 ID, 旧领域, 新领域]`，合同 §8 末句逐字）。
	DomainMovedTargetArity = 3
)

// orderedTargetsSpec 是一条顺序固定例外：哪个 check、恰几元。
type orderedTargetsSpec struct {
	Check string
	Arity int
}

// orderedTargetsTable 是例外的封闭全集（长度固定数组：第二条例外加不进来）。
var orderedTargetsTable = [OrderedTargetsCheckCount]orderedTargetsSpec{
	{CheckDomainMoved, DomainMovedTargetArity},
}

// OrderedTargetsChecks 返回 targets 顺序固定的 check 集合副本（恒恰一个元素）。
func OrderedTargetsChecks() []string {
	out := make([]string, 0, OrderedTargetsCheckCount)
	for _, s := range orderedTargetsTable {
		out = append(out, s.Check)
	}
	return out
}

// TargetsArity 返回该 check 的 targets 固定元数。
//
// 第二个返回值为 false = 该 check 走**默认口径**（集合语义：去重 + 字典序升序）。
func TargetsArity(check string) (int, bool) {
	for _, s := range orderedTargetsTable {
		if s.Check == check {
			return s.Arity, true
		}
	}
	return 0, false
}

// TargetsOrdered 报告该 check 的 targets 是否顺序固定（合同 §8 的封闭例外）。
func TargetsOrdered(check string) bool {
	_, ok := TargetsArity(check)
	return ok
}

// 单射索引：check → spec、code → spec。两张表由 checkTable 派生，成对建立即成对封闭。
var checkIndex, codeIndex = buildIndexes()

func buildIndexes() (map[string]CheckSpec, map[string]CheckSpec) {
	byCheck := make(map[string]CheckSpec, CheckCount)
	byCode := make(map[string]CheckSpec, CheckCount)
	for _, s := range checkTable {
		byCheck[s.Check] = s
		byCode[s.Code] = s
	}
	return byCheck, byCode
}

// Specs 返回十二行封闭表的副本（顺序 = 合同 §3 表格行序；调用方改副本不影响真源）。
func Specs() []CheckSpec {
	out := make([]CheckSpec, 0, CheckCount)
	out = append(out, checkTable[:]...)
	return out
}

// AllChecks 返回 check 的封闭全集（恰 CheckCount 个，表格行序）。
func AllChecks() []string {
	out := make([]string, 0, CheckCount)
	for _, s := range checkTable {
		out = append(out, s.Check)
	}
	return out
}

// AllSeverities 返回 severity 的封闭全集（恰 SeverityCount 个，error 在前）。
func AllSeverities() []string {
	out := make([]string, 0, SeverityCount)
	out = append(out, severityTable[:]...)
	return out
}

// AllCodes 返回诊断码的封闭全集（恰 CodeCount 个，与 AllChecks 逐位对应）。
func AllCodes() []string {
	out := make([]string, 0, CodeCount)
	for _, s := range checkTable {
		out = append(out, s.Code)
	}
	return out
}

// SpecOf 按 check 取封闭四元组。
func SpecOf(check string) (CheckSpec, bool) {
	s, ok := checkIndex[check]
	return s, ok
}

// SpecOfCode 按诊断码取封闭四元组（单射的反向）。
func SpecOfCode(code string) (CheckSpec, bool) {
	s, ok := codeIndex[code]
	return s, ok
}

// SeverityOf 返回该 check 的 severity（severity 由表定死，调用方不得自带）。
func SeverityOf(check string) (string, bool) {
	s, ok := checkIndex[check]
	if !ok {
		return "", false
	}
	return s.Severity, true
}

// CodeOf 返回该 check 的诊断码（单射的正向）。
func CodeOf(check string) (string, bool) {
	s, ok := checkIndex[check]
	if !ok {
		return "", false
	}
	return s.Code, true
}

// CheckOfCode 返回该诊断码对应的 check（单射的反向）。
func CheckOfCode(code string) (string, bool) {
	s, ok := codeIndex[code]
	if !ok {
		return "", false
	}
	return s.Check, true
}

// IsKnownCheck 报告取值是否落在十二值封闭枚举内。
func IsKnownCheck(check string) bool {
	_, ok := checkIndex[check]
	return ok
}

// IsKnownSeverity 报告取值是否落在两值封闭集合内。
func IsKnownSeverity(severity string) bool {
	for _, s := range severityTable {
		if s == severity {
			return true
		}
	}
	return false
}

// IsKnownCode 报告取值是否落在本包十二值诊断码集合内。
func IsKnownCode(code string) bool {
	_, ok := codeIndex[code]
	return ok
}

// ParseCheck 把外部字符串收敛到封闭枚举：第 13 个取值一律报错（不静默通过）。
func ParseCheck(s string) (string, error) {
	if !IsKnownCheck(s) {
		return "", fmt.Errorf("未知 check %q：check 是恰 %d 值的封闭枚举（合同 §3）", s, CheckCount)
	}
	return s, nil
}

// ParseSeverity 把外部字符串收敛到两值封闭集合（第三个分级在 M4 不启用，一律报错）。
func ParseSeverity(s string) (string, error) {
	if !IsKnownSeverity(s) {
		return "", fmt.Errorf("未知 severity %q：severity 是恰 %d 值的封闭集合（合同 §2）",
			s, SeverityCount)
	}
	return s, nil
}

// ParseCode 把外部字符串收敛到本包诊断码集合（W 段恰止于 W20，越号一律报错）。
func ParseCode(s string) (string, error) {
	if !IsKnownCode(s) {
		return "", fmt.Errorf("未知诊断码 %q：本包诊断码是恰 %d 值的封闭集合（合同 §3）",
			s, CodeCount)
	}
	return s, nil
}
