package report

// 报告体 `reconcile` 字段的封闭三键 schema 与投影入口（M4 · T-evergreen.s1_main_flow-158614-057）。
//
// # 为什么单独成文
//
// 对账结果是 S3 才有的事实，而报告体本身是 M1 冻结的对外合同。把这块 schema 从 report.go
// 迁出来单独放，是为了让「S1 必填 11 项」与「S3 阶段字段」在**文件层**就分得开：
// report.go 里那 11 项一字不动，本文件承载的三键随 M4 一起生长，两边不会互相污染。
//
// # 三条硬口径（对账合同 §11）
//
//   - **恰三键**：`ran` / `commit` / `findings`，**不增不减**。第四键出现即被
//     reconcile_test.go 的 TestReconcileFieldExactlyThreeKeys 当场判红
//     （字段数 + 标签集合 + 序列化键集合三重比对）。
//   - `ran` 是 bool 必填；`commit` 是字符串或 `null`（零改动即 `null`）；
//     `findings` 是数组且**永不为 null**（空时序列化为 `[]`，Agent 侧不必判空指针）。
//   - **S3 阶段键**：`reconcile` **不进** §4.6 的 S1 必填集合；非对账命令路径下恒为
//     ReconcilePlaceholderJSON 那一串（**键在、值为空**，绝不整键缺席）——
//     下游脚本因此永远不需要区分「键缺席」与「值为空」两种情况。
//
// # 为什么 findings 元素是本包的镜像结构体，而不是直接引对账包的类型
//
// `cmd/eg/arch_test.go` 的 S3 包边界用例把 `report` 逐名列进「不得依赖对账包」的名单：
// 依赖方向是单向的（`reconcile` → {model, mdfile, git 只读, query}），报告体是 S1 的
// 下游叶子包，反向或跨层依赖即成环。因此本文件持有一份**四键逐字同名**的镜像
// （ReconcileFinding），并由 `internal/cli`（唯一允许同时看见两个包的层）用**等号断言**
// 把两侧键集合锁死 —— 与 ExecutionFailed 那个字面量同一套办法：
// 谁改了任一侧的键名，cli 侧的等号当场判红，不可能悄悄漂移。
//
// # 边界（不得越线）
//
//   - 本文件**只做投影**：不跑任何检查、不注册任何命令、不写盘、不提交、不排序、不去重、
//     不重新计数。检查在 `internal/reconcile`，命令在 T-…-058 / T-…-059，
//     人类可读渲染的唯一落点是 `internal/cli/reconcile_render.go`（T-…-056 已定），
//     本文件与 Lines() 都不再拼第二份措辞。
//   - **不新增 `affected` 的字段级定义**：技术方案 §11.2 只点名本文件这三键，
//     `affected` 自 M1 起就是**值为 null 的占位键**，本 task 既不给它字段级定义、
//     也**不删除**那个占位键（删了反而违反 §4.6「阶段字段键必须在、值不得造假」）。

// ReconcileKeyCount 是 `reconcile` 对象的封闭键数：**恰 3**。
const ReconcileKeyCount = 3

// reconcileKeys 是三键的封闭全集（顺序 = 结构体字段顺序 = 合同 §11 的键序）。
var reconcileKeys = [ReconcileKeyCount]string{"ran", "commit", "findings"}

// ReconcileKeys 返回三键集合的副本（顺序即合同 §11 的键序）。
func ReconcileKeys() []string {
	out := make([]string, 0, ReconcileKeyCount)
	out = append(out, reconcileKeys[:]...)
	return out
}

// ReconcileFindingKeyCount 是 finding 的封闭键数：**恰 4**（对账合同 §2）。
const ReconcileFindingKeyCount = 4

// reconcileFindingKeys 是 finding 四键的封闭全集，**逐字**同名于对账包的 Finding。
//
// 两侧相等由 internal/cli 的等号断言锁死（见本文件头「为什么是镜像结构体」）。
var reconcileFindingKeys = [ReconcileFindingKeyCount]string{
	"check", "severity", "targets", "detail",
}

// ReconcileFindingKeys 返回 finding 四键集合的副本（顺序即合同 §2 的键序）。
func ReconcileFindingKeys() []string {
	out := make([]string, 0, ReconcileFindingKeyCount)
	out = append(out, reconcileFindingKeys[:]...)
	return out
}

// ReconcilePlaceholderJSON 是**非对账命令路径**下 `reconcile` 的逐字序列化形态。
//
// 它是单测、e2e 与命令层共用的**唯一**比对源：想改占位形态，只能改这一处，
// 而这一处一改就会同时惊动三层判据（结构层、端到端、门禁 grep）。
const ReconcilePlaceholderJSON = `{"ran":false,"commit":null,"findings":[]}`

// ReconcileFinding 是一条对账发现在报告里的投影，**恰四键**、键名逐字同名于对账包。
//
// 报告只**搬运**：不排序、不去重、不合并、不重新判定 —— 排序与归一化是对账包的职责
// （合同 §2 第 3 条），报告这一层动了它就会出现「两处各排一遍、结果不一致」的经典裂缝。
type ReconcileFinding struct {
	Check    string   `json:"check"`
	Severity string   `json:"severity"`
	Targets  []string `json:"targets"`
	Detail   string   `json:"detail"`
}

// Reconcile 是报告体的对账段落，**恰三键**（合同 §11）。
//
// 三键的空值语义各不相同，且都不许用「零值凑」：
//   - 没跑对账 → `ran` 为 false（不是省略键）；
//   - 跑了但零改动 → commit 键为 `null`（不是空串，空串会让脚本以为有个空 sha）；
//   - 没有任何发现 → findings 是 `[]`（不是 null）。
type Reconcile struct {
	Ran      bool               `json:"ran"`
	Commit   *string            `json:"commit"`
	Findings []ReconcileFinding `json:"findings"`
}

// newReconcilePlaceholder 返回非对账路径下的占位形态：**键在、值为空**。
//
// findings 用**长度 0 的非 nil 切片**——这是「空集合序列化为 [] 而不是 null」的落点。
func newReconcilePlaceholder() Reconcile {
	return Reconcile{Ran: false, Commit: nil, Findings: []ReconcileFinding{}}
}

// SetReconcile 记录本次对账的三件事实：跑没跑、落在哪个 commit、发现了什么。
//
// 三条兜底与合同 §11 一一对应，且都只做「空值归一」，**不改任何调用方给的事实**：
//   - sha 为空串 → commit 键输出 `null`（未产生 commit / 提交失败 / --dry-run）；
//   - findings 为 nil → 输出 `[]`；
//   - 每条 finding 的 targets 为 nil → 输出 `[]`（与对账包 NormalizeTargets 同口径）。
//
// 数量守恒：入参有几条就搬几条，**不折叠、不去重、不排序**（如实原则的代码化）。
func (r *Report) SetReconcile(ran bool, sha string, findings []ReconcileFinding) {
	out := make([]ReconcileFinding, 0, len(findings))
	for _, f := range findings {
		if f.Targets == nil {
			f.Targets = []string{}
		}
		out = append(out, f)
	}
	next := Reconcile{Ran: ran, Commit: nil, Findings: out}
	if sha != "" {
		value := sha
		next.Commit = &value
	}
	r.Reconcile = next
}
