package store

// [M3] internal/store/state_write.go：**M3 状态维度的唯一写口**。
//
// 为什么单独一个文件、且只暴露三个 setter（SetStatus / SetReplacedBy / SetDeleted）：
// 合同 §7.1 把「状态维度落盘」定为第四类**受守卫**写形态。executor 只通过 store 包内
// 的入口（Apply* / 本文件的 setter）调用状态落盘，**不在 internal/plan、internal/cli
// 层直接调用这三个 setter**——这样「写口唯一」就有一条可机械反证的 grep 判据：
//
//	grep -rnE "\.(SetStatus|SetReplacedBy|SetDeleted)\(" internal/ 的非测试命中
//	必须全部落在 internal/store/ 内，plan/ 与 cli/ 零命中（护栏见 store_test.go）。
//
// [M4] 本文件在 M4 期追加**第五种**状态写形态 `SetStale`（`stale` + `stale_reason` 两键
// 一次守卫写，R6 综述失准标记；对账合同 §9 / A-33 / A-34，T-…-055 阶段 1）。护栏同样加严：
// 上面那条 grep 判据的名字集合随之加上 `SetReviewedAt` 与 `SetStale`（只增不减）。
// 形态计数口径是加法等式：**M3 期恰四种 + M4 新增 1 种 = 恰五种**（M3 结论不改写）。
//
// 三条边界（合同硬约束，不得放宽）：
//   - SetStatus 只改 `status` 单键，**绝不碰 deleted_at / deleted_reason**：状态与
//     删除是两个正交维度（冻结合同 F3）；也**不累积任何历史数组**——反复调用只覆盖
//     单键，变更原因由 Git 历史查阅，frontmatter 不许长出历史键。
//
// [C2 · I-…-009] 三条边界之外追加一条**跨形态**的口径：status / replaced_by / deleted
// 三种形态在落盘成功时，把既有 `updated_at` 整行刷新为调用方注入的写入时刻（授权合同
// §2.1 矩阵第 8 行「由 CLI 在实际写入时更新」，两条路径无差别；唯一实现 updated_at.go）。
// `reviewed_at` 与 `stale` 两种形态**结构性豁免**（签名里没有这一格可传）：过目不是对
// 内容的修改（§5.5 EG-CFM-06），失准标记也不改内容（对账合同 §9）。
//   - SetReplacedBy 只写宿主端点（知识卡或观点）自己的 `replaced_by`：**单向存储**，被指向端点文件
//     绝不打开、绝不改写（避免双写产生不一致的两份真相）。
//   - SetDeleted 只写 deleted_at + deleted_reason（ClearDeleted 是其反向），同样
//     **绝不碰 status**（正交维度）；四类产物（知识卡 / 笔记 / 原文 / 综述）字段名与
//     行为完全一致，不按类型分叉；全程无物理删除、关系记录一条不删。
//
// 字节机制与既有写形态同源：复用 mutateGuarded（B1 原子写 + B3 hash 守卫 + 写前
// Parse→Render 自检 + B2 用户分区逐字保留），只在 frontmatter 的目标键区间上做
// `Raw[:a] + 新行 + Raw[b:]` 的区间拼接。**写路径不做任何 YAML 序列化回写**
// （make lint 的 guard 步会 FAIL），全链路 []byte，正文与未知 YAML 字段逐字保留。

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

var (
	// ErrFMKeyNotFound frontmatter 缺该顶层键：本层不猜、不补，直接失败（fail fast）。
	ErrFMKeyNotFound = errors.New("frontmatter 无该顶层键")
	// ErrFMKeyDuplicated frontmatter 出现重复顶层键：改哪一个都可能错，拒写。
	ErrFMKeyDuplicated = errors.New("frontmatter 顶层键重复，拒绝改写")
	// ErrFMKeyNotScalar 目标键不是单行标量（带缩进续行）：单键覆盖的前提被破坏，拒写。
	ErrFMKeyNotScalar = errors.New("frontmatter 目标键不是单行标量，拒绝改写")
	// ErrReplacedByIncomplete replaced_by 必须 target 与 reason 同时给全（缺一即未给）。
	ErrReplacedByIncomplete = errors.New("replaced_by 必须同时给 target 与 reason")
	// ErrDeletedIncomplete 逻辑删除必须 deleted_at 与 deleted_reason 同时给全：
	// 只有时间没有理由的墓碑等于「谁也说不清为什么删」，宁可拒写（fail fast）。
	ErrDeletedIncomplete = errors.New("逻辑删除必须同时给 deleted_at 与 deleted_reason")
	// ErrNotDeleted 清空删除标记时目标本来就没有 deleted_at：本层不猜、不静默成功。
	ErrNotDeleted = errors.New("目标未被逻辑删除（无 deleted_at），无删除标记可清空")
	// ErrReviewedAtRequired 标记已过目必须给非零时刻：本层绝不代入「现在」来补一个值
	// （缺省即缺省，回填默认值会让「从未过目」这一事实消失）。
	ErrReviewedAtRequired = errors.New("标记已过目必须给非零时刻")
	// ErrStaleReasonClosed 综述失准标记的 stale_reason 取值封闭（恰三值，对账合同 §9）：
	// 第四种取值一律拒写。本层不猜、不归一化、不退化成自由文本——取值一开放，
	// 「多因并存取第一个命中值」这条可复算判据当场失效。
	ErrStaleReasonClosed = errors.New("stale_reason 取值封闭（恰三值），拒绝改写")
)

const (
	fmKeyStatus        = "status"
	fmKeyReplacedBy    = "replaced_by"
	fmKeyDeletedAt     = "deleted_at"
	fmKeyDeletedReason = "deleted_reason"
	// staleTrueValue 是 `stale` 的唯一落盘取值（YAML 布尔真，逐字 `true`，不加引号）。
	// 本层不提供 `stale: false` 与整行删除：合同 §9 明文「不自动清除 stale」。
	staleTrueValue = "true"
)

// SetStatus 覆盖失效/恢复维度的 `status` 单键（取值只有 active / deprecated 两种）。
//
// 单键语义：除 `status:` 那一行的字节，文件其余部分（含 deleted_at / deleted_reason /
// 未知键 / 正文 / 空行 / 引号风格）逐字不动；不追加任何历史数组键。
//
// 唯一的例外是元数据时间戳：stamp 非零时 `updated_at` 那一行在**同一次**守卫写里被整行
// 刷新（矩阵第 8 行；缺该键则不新增）—— 状态流转是一次实际写入，`eg unreviewed` 必须
// 能看见它。stamp 为零则连这一行也不动（调用方未要求刷新，本层不读时钟）。
func (s *Store) SetStatus(rel string, expectedHash string, status model.Status,
	stamp model.Stamp) (Result, error) {
	res := Result{Path: rel}
	if rel == "" {
		return res, ErrCardRelRequired
	}
	if _, err := model.ParseStatus(string(status)); err != nil {
		return res, fmt.Errorf("status 取值封闭（合法取值恰 %v）：%w",
			model.ValidStatuses(), err)
	}
	line := fmScalarLine(fmKeyStatus, fmCanonicalScalar(string(status)))
	return s.mutateGuarded(rel, expectedHash,
		withUpdatedAt(stamp, func(_ File, doc *mdfile.Doc) ([]byte, error) {
			return setFMScalarKey(doc, fmKeyStatus, line, true)
		}))
}

// SetReplacedBy 在**宿主端点**（知识卡或观点）上写替代指针 `replaced_by: {target: <id>, reason: "<text>"}`。
//
// 单向存储：只动 rel 这一份文件；target 指向的那份产物不打开、不读、不写。
// target 是跨类型端点（k- / o- 前缀）：迁移会让原 k- 卡的 replaced_by 指向新 o- 观点
// （Schema v2 §9.2 / T-009），因此这里走 relationEndpointOnly 守卫，接受 k/o、拒 s/n/r/p/
// 畸形；reason 必带——缺一视为未给，拒写而不写半个指针。
func (s *Store) SetReplacedBy(rel string, expectedHash string, target model.RelationEndpoint,
	reason string, stamp model.Stamp) (Result, error) {
	res := Result{Path: rel}
	if rel == "" {
		return res, ErrCardRelRequired
	}
	if target == "" || reason == "" {
		return res, fmt.Errorf("%w：得到 target=%q reason=%q", ErrReplacedByIncomplete,
			target, reason)
	}
	if err := relationEndpointOnly(fmKeyReplacedBy+".target", target); err != nil {
		return res, err
	}
	// 流式 mapping 单行落盘：一个键一行，改写区间最小，且与合同给的 YAML 形态逐字一致。
	value := make([]byte, 0, len(target)+len(reason)+24)
	value = append(value, '{')
	value = append(value, "target: "...)
	value = append(value, string(target)...)
	value = append(value, ", reason: "...)
	value = append(value, yamlDoubleQuoted(reason)...)
	value = append(value, '}')
	line := fmScalarLine(fmKeyReplacedBy, value)
	return s.mutateGuarded(rel, expectedHash,
		withUpdatedAt(stamp, func(_ File, doc *mdfile.Doc) ([]byte, error) {
			return setFMScalarKey(doc, fmKeyReplacedBy, line, false)
		}))
}

// SetDeleted 落地**逻辑删除**：只写 deleted_at + deleted_reason 两个 frontmatter 键。
//
// 四条不变式（合同 §5.2 ADR-11 / §9 / U-01 / U-02）：
//   - **绝不碰 status**：删除与失效是两个正交维度（F3），删除执行后没有任何知识卡的
//     状态被自动改变——本函数根本不定位 status 键，因此这条是结构上成立的。
//   - **四类产物同构**：知识卡 / 笔记 / 原文 / 综述用**同一个** rel 参数进来，字段名与
//     行为完全一致，本层不按产物类型分叉（不看目录、不看 id 前缀）。
//   - **无物理删除**：只做 frontmatter 区间拼接，不删任何文件、不删任何目录；
//     relations[] / sources[] / opposing / replaced_by 的**记录一条不删**（关系过滤靠
//     端点有效性，由查询层判定，落盘层不动记录）。
//   - **逐文件、非强原子**（R-9 / B4）：一个文件一次守卫写，失败即保留现状，不回滚、
//     不重试；已写的留着，未写的字节不变。
func (s *Store) SetDeleted(rel string, expectedHash string, at model.Stamp,
	reason string, stamp model.Stamp) (Result, error) {
	res := Result{Path: rel}
	if rel == "" {
		return res, ErrCardRelRequired
	}
	if at.IsZero() {
		return res, fmt.Errorf("%w：deleted_at 必带（零值时间戳不接受）", ErrDeletedIncomplete)
	}
	if reason == "" {
		return res, fmt.Errorf("%w：deleted_reason 必带", ErrDeletedIncomplete)
	}
	// 两个值一律双引号标量：deleted_at 含 `:`（时区），reason 是自由文本，
	// 双引号 + 最小转义是确定性的，且不依赖任何序列化库（写路径禁用 YAML 序列化）。
	atLine := fmScalarLine(fmKeyDeletedAt, fmCanonicalScalar(at.String()))
	reasonLine := fmScalarLine(fmKeyDeletedReason, fmCanonicalScalar(reason))
	return s.mutateGuarded(rel, expectedHash,
		withUpdatedAt(stamp, func(_ File, doc *mdfile.Doc) ([]byte, error) {
			return setFMScalarKeys(doc,
				[]fmKeyLine{{key: fmKeyDeletedAt, line: atLine},
					{key: fmKeyDeletedReason, line: reasonLine}})
		}))
}

// ClearDeleted 清空删除标记（供 `eg undelete` 用）：整行删掉 deleted_at 与 deleted_reason。
//
// 为什么整行删而不是写空值：frontmatter 不留墓碑——「没有这个键」与「键在但为空」必须
// 可区分，恢复后应回到与从未被删完全一致的形态。
// 与 SetDeleted 同一条守卫路径（B3 hash 比对 + Parse→Render 自检 + B1 原子写），
// 同样**绝不碰 status**：恢复删除不改变任何卡的 active / deprecated。
func (s *Store) ClearDeleted(rel string, expectedHash string, stamp model.Stamp) (Result, error) {
	res := Result{Path: rel}
	if rel == "" {
		return res, ErrCardRelRequired
	}
	return s.mutateGuarded(rel, expectedHash,
		withUpdatedAt(stamp, func(_ File, doc *mdfile.Doc) ([]byte, error) {
			return dropFMScalarKeys(doc, []string{fmKeyDeletedAt, fmKeyDeletedReason})
		}))
}

// SetReviewedAt 落地**已过目**信号：只写 `reviewed_at` 单键（M3 唯一的 reviewed_at 落盘口）。
//
// 四条边界（合同 §6.1 / ADR-20 / B1 / B3）：
//   - **单键**：绝不碰 status、绝不碰 deleted_at / deleted_reason，也**绝不顺手更新
//     updated_at**——`reviewed_at` 是第三个正交维度，而「过目」本身不是对内容的修改，
//     更新 updated_at 会让刚标记过的产物立刻又变成未过目。
//   - **四类产物同构**：知识卡 / 笔记 / 原文 / 综述用同一个 rel 进来，不按类型分叉。
//   - **不回填**：at 为零值直接拒写（ErrReviewedAtRequired），本层不代入「现在」。
//   - 与三种既有状态写形态同一条守卫路径（B3 hash 比对 + Parse→Render 自检 + B1 原子写），
//     键不存在则追加到 frontmatter 末尾（AppendFMKey），已存在则整行覆盖。
func (s *Store) SetReviewedAt(rel string, expectedHash string, at model.Stamp) (Result, error) {
	res := Result{Path: rel}
	if rel == "" {
		return res, ErrCardRelRequired
	}
	if at.IsZero() {
		return res, fmt.Errorf("%w：reviewed_at 必带（零值时间戳不接受）", ErrReviewedAtRequired)
	}
	// 双引号标量：时刻含 `:`（时区），双引号 + 最小转义是确定性的，且不依赖任何序列化库。
	line := fmScalarLine(model.FMKeyReviewedAt, fmCanonicalScalar(at.String()))
	return s.mutateGuarded(rel, expectedHash,
		func(_ File, doc *mdfile.Doc) ([]byte, error) {
			return setFMScalarKey(doc, model.FMKeyReviewedAt, line, false)
		})
}

// SetStale 落地**综述失准标记**：只写 `stale` + `stale_reason` 两个 frontmatter 键
// （M4 · R6 的唯一落盘口；对账合同 §9）。
//
// 五条边界（合同 §9 / §1.3 第 3 行 / B1 / B3 / A-34）：
//   - **两键、一次守卫写**：走 setFMScalarKeys 落成**同一次** mutateGuarded（不是两次写），
//     因此不存在「只写了 stale、没写 reason」的半截形态；两键同生（本层不提供清除口径）。
//   - **取值封闭**：reason 必须是 model.ValidStaleReasons() 的三值之一，第四种一律拒写
//     （ErrStaleReasonClosed）——取值一开放，「多因并存取第一个命中值」就不可复算。
//   - **绝不碰别的维度**：不碰 status、不碰 deleted_at / deleted_reason、不碰 replaced_by、
//     不碰 reviewed_at、不碰 updated_at，正文一个字节不动；也**绝不重算综述**
//     （合同 §9：不自动重算、不自动清除 stale）。
//   - **不按对象类型分叉**：本层只认 rel（与 SetDeleted / SetReviewedAt 同构）；
//     「两键只允许落在综述上」由写权限矩阵 #33（对象类恒为主题综述）在 plan 侧收口。
//   - 与四种既有状态写形态同一条守卫路径（B3 hash 比对 + Parse→Render 自检 + B1 原子写），
//     键不存在则追加到 frontmatter 末尾，已存在则整行覆盖（幂等重写同值不改变字节）。
func (s *Store) SetStale(rel string, expectedHash string, reason model.StaleReason) (Result, error) {
	res := Result{Path: rel}
	if rel == "" {
		return res, ErrCardRelRequired
	}
	if _, err := model.ParseStaleReason(string(reason)); err != nil {
		return res, fmt.Errorf("%w（合法取值恰 %v）：%v", ErrStaleReasonClosed,
			model.ValidStaleReasons(), err)
	}
	// stale 是 YAML 布尔真（不加引号）；reason 是封闭枚举里的中文单句，一律双引号标量
	// （双引号 + 最小转义是确定性的，且不依赖任何序列化库——写路径禁用 YAML 序列化）。
	staleLine := fmScalarLine(model.FMKeyStale, []byte(staleTrueValue))
	reasonLine := fmScalarLine(model.FMKeyStaleReason, fmCanonicalScalar(string(reason)))
	return s.mutateGuarded(rel, expectedHash,
		func(_ File, doc *mdfile.Doc) ([]byte, error) {
			return setFMScalarKeys(doc,
				[]fmKeyLine{{key: model.FMKeyStale, line: staleLine},
					{key: model.FMKeyStaleReason, line: reasonLine}})
		})
}

// fmKeyLine 是「某个顶层键要变成这一行」的意图（多键一次写时按序应用）。
type fmKeyLine struct {
	key  string
	line []byte
}

// setFMScalarKeys 依次覆盖/追加多个顶层键，返回**一次性**的新字节。
//
// 为什么每步都重新 Parse：区间偏移在上一步拼接后已失效，重新解析是唯一不靠算术推
// 偏移的做法；两个键因此在**同一次**守卫写里落盘（不是两次写），逐文件语义不变。
func setFMScalarKeys(doc *mdfile.Doc, want []fmKeyLine) ([]byte, error) {
	cur := doc
	var out []byte
	for i, w := range want {
		next, err := setFMScalarKey(cur, w.key, w.line, false)
		if err != nil {
			return nil, err
		}
		out = next
		if i == len(want)-1 {
			break
		}
		cur, err = mdfile.Parse(out)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// dropFMScalarKeys 整行删掉给定顶层键；keys[0] 不存在即报 ErrNotDeleted（不静默成功）。
func dropFMScalarKeys(doc *mdfile.Doc, keys []string) ([]byte, error) {
	cur := doc
	out := doc.Raw
	for i, key := range keys {
		start, end, found, err := fmScalarKeySpan(cur, key)
		if err != nil {
			return nil, err
		}
		if !found {
			if i == 0 {
				return nil, fmt.Errorf("%w：%s", ErrNotDeleted, key)
			}
			continue
		}
		next := make([]byte, 0, len(cur.Raw))
		next = append(next, cur.Raw[:start]...)
		next = append(next, cur.Raw[end:]...)
		out = next
		cur, err = mdfile.Parse(out)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// 五种状态写形态的封闭取值：executor 只能从这五个常量里选，写不出第六种。
//
// 计数口径（加法等式，M3 期结论不改写）：**M3 期恰四种**（status / replaced_by /
// deleted / reviewed_at）**+ M4 新增 1 种**（stale，A-33 的 R6 落盘口）= **恰五种**。
const (
	// StateWriteStatus 覆盖 status 单键（deprecate / restore 共用）。
	StateWriteStatus = "status"
	// StateWriteReplacedBy 在宿主端点（知识卡或观点）上写替代指针（单向存储）。
	StateWriteReplacedBy = "replaced_by"
	// StateWriteDeleted 是逻辑删除维度（写 deleted_at + deleted_reason；Clear 为真时清空）。
	StateWriteDeleted = "deleted"
	// StateWriteReviewedAt 是「已过目」维度（只写 reviewed_at 单键，M3 由 eg mark-reviewed 触发）。
	// 取值直接借 model 的键名常量：键名字面量只有一处，写入路径与筛选侧不会各写一份。
	StateWriteReviewedAt = model.FMKeyReviewedAt
	// StateWriteStale 是**第五形态**：综述失准标记维度（stale + stale_reason 两键一次守卫写，
	// M4 由对账 R6 触发，A-34 不需要 --user-request）。取值同样借 model 的键名常量。
	StateWriteStale = model.FMKeyStale
)

// StateWriteSpec 是一次状态落盘的意图。Op 决定用哪一个形态，其余字段按形态取用：
// status 只用 Status；replaced_by 只用 Target + Reason；deleted 只用 At + Reason
// （Clear 为真时 At / Reason 都不用——那是「清空删除标记」的反向意图，供 undelete）；
// reviewed_at 只用 At；stale 只用 StaleReason（**封闭三值**，没有第二格可塞）。
type StateWriteSpec struct {
	Op           string
	Rel          string
	ExpectedHash string
	Status       model.Status
	Target       model.RelationEndpoint
	Reason       string
	At           model.Stamp
	// StaleReason 只对 stale 形态有意义（封闭三值）。**刻意不复用 Reason 那一格**：
	// 自由文本 reason 与封闭枚举 reason 混用一格，迟早会有人把用户文本塞进来。
	StaleReason model.StaleReason
	// Clear 只对 deleted 形态有意义：true = 清空删除标记（不新增第四种 Op，
	// 因为「删除」与「恢复删除」是同一维度的两个方向，共用同一条守卫路径）。
	Clear bool
	// Stamp 是本次写入时刻，只对 **status / replaced_by / deleted** 三种形态有效：
	// 非零时在同一次守卫写里把既有 `updated_at` 整行刷新（矩阵第 8 行「由 CLI 在实际
	// 写入时更新」，唯一实现见 updated_at.go）。
	//
	// `reviewed_at` 与 `stale` 两种形态**结构性豁免**：分发口根本不把这一格递给
	// SetReviewedAt / SetStale（它们的签名里也没有可接的参数），因此「过目 / 失准标记
	// 顺手刷新内容时间戳」写不出来 —— 判据是 §5.5 EG-CFM-06「过目不是对内容的修改」
	// 与对账合同 §9「不自动重算 / 不自动清除」。
	Stamp model.Stamp
}

// ApplyStateWrite 是状态落盘的**唯一对外入口**（与 ApplyRemoveRelation / ApplyReplaceBlock 同源
// 体例）：plan / cli 层只认得它，五个 setter 的调用点全部留在 store 包内，因此「写口唯一」
// 的 grep 反证（store_test.go 的写口唯一护栏）恒成立。
func (s *Store) ApplyStateWrite(spec StateWriteSpec) (Result, error) {
	return s.stateWrite(spec.Op, spec.Rel, spec.ExpectedHash, spec.Status, spec.Target,
		spec.Reason, spec.At, spec.StaleReason, spec.Clear, spec.Stamp)
}

// stateWrite 是**包内**状态落盘分发口：executor 经 ApplyStateWrite 进来，由这里再分发到
// 各个 setter，而**不在 plan/ 与 cli/ 层直接调用 setter**。
// 为什么保留这层：写口唯一的 grep 反证要求 setter 的调用点全部落在 internal/store/ 内，
// 这层就是那个唯一的包内调用点（护栏见 store_test.go 的写口唯一测试）。
func (s *Store) stateWrite(op string, rel string, expectedHash string,
	status model.Status, target model.RelationEndpoint, reason string, at model.Stamp,
	staleReason model.StaleReason, clear bool, updatedAt model.Stamp) (Result, error) {
	switch op {
	case StateWriteStatus:
		return s.SetStatus(rel, expectedHash, status, updatedAt)
	case StateWriteReplacedBy:
		return s.SetReplacedBy(rel, expectedHash, target, reason, updatedAt)
	case StateWriteDeleted:
		if clear {
			return s.ClearDeleted(rel, expectedHash, updatedAt)
		}
		return s.SetDeleted(rel, expectedHash, at, reason, updatedAt)
	case StateWriteReviewedAt:
		// 只用 At：过目信号没有「理由」这一格，也没有反向清空形态（M3 不提供撤销过目）。
		return s.SetReviewedAt(rel, expectedHash, at)
	case StateWriteStale:
		// 只用 StaleReason：失准标记没有时刻这一格，也没有反向清空形态
		// （合同 §9：不自动清除 stale、不自动重算综述）。
		return s.SetStale(rel, expectedHash, staleReason)
	default:
		return Result{Path: rel}, fmt.Errorf(
			"未知状态写形态 %q（恰五种：status / replaced_by / deleted / %s / %s）",
			op, StateWriteReviewedAt, StateWriteStale)
	}
}

// fmScalarLine 拼一行 `key: value\n`。只拼这一行，绝不重排其它键。
func fmScalarLine(key string, value []byte) []byte {
	line := make([]byte, 0, len(key)+len(value)+3)
	line = append(line, key...)
	line = append(line, ':', ' ')
	line = append(line, value...)
	line = append(line, '\n')
	return line
}

// setFMScalarKey 把 frontmatter 顶层键 key 的整行替换成 line（键不存在时按 mustExist 决定
// 报错还是追加到 frontmatter 末尾），返回**新的**字节切片。
//
// 机制只有一条：`Raw[:start] + line + Raw[end:]`（Doc/Span 半开区间）。键行之外的
// 一切字节逐字不动；键顺序不重排；不做 YAML 序列化。
func setFMScalarKey(doc *mdfile.Doc, key string, line []byte, mustExist bool) ([]byte, error) {
	start, end, found, err := fmScalarKeySpan(doc, key)
	if err != nil {
		return nil, err
	}
	if !found {
		if mustExist {
			return nil, fmt.Errorf("%w：%s", ErrFMKeyNotFound, key)
		}
		return doc.AppendFMKey(key, bytes.TrimSuffix(line[len(key)+2:], []byte("\n")))
	}
	out := make([]byte, 0, len(doc.Raw)+len(line))
	out = append(out, doc.Raw[:start]...)
	out = append(out, line...)
	out = append(out, doc.Raw[end:]...)
	return out, nil
}

// fmScalarKeySpan 定位 frontmatter 顶层键所在**整行**的半开区间 [start, end)。
//
// 只认零缩进的 `key:` 行（顶层键）；带缩进续行的键不是单行标量 → ErrFMKeyNotScalar；
// 同名键出现两次 → ErrFMKeyDuplicated（改哪个都可能错，宁可拒写）。
func fmScalarKeySpan(doc *mdfile.Doc, key string) (int, int, bool, error) {
	if !doc.HasFM {
		return 0, 0, false, mdfile.ErrNoFrontmatter
	}
	prefix := append([]byte(key), ':')
	start, end := -1, -1
	raw := doc.Raw
	for cur := doc.FMStart; cur < doc.FMEnd; {
		lend := fmLineEnd(raw, cur, doc.FMEnd)
		line := raw[cur:lend]
		if !bytes.HasPrefix(line, prefix) {
			cur = lend
			continue
		}
		if start >= 0 {
			return 0, 0, false, fmt.Errorf("%w：%s", ErrFMKeyDuplicated, key)
		}
		start, end = cur, lend
		// 紧随其后的缩进行属该键的结构化值：单键覆盖的前提不成立。
		if next := fmLineEnd(raw, lend, doc.FMEnd); lend < doc.FMEnd &&
			fmIndented(raw[lend:next]) {
			return 0, 0, false, fmt.Errorf("%w：%s", ErrFMKeyNotScalar, key)
		}
		cur = lend
	}
	if start < 0 {
		return 0, 0, false, nil
	}
	return start, end, true, nil
}

// fmLineEnd 返回 at 所在行的行尾偏移（含换行），并夹在 limit 内。
func fmLineEnd(raw []byte, at int, limit int) int {
	i := bytes.IndexByte(raw[at:limit], '\n')
	if i < 0 {
		return limit
	}
	return at + i + 1
}

// fmIndented 判定该行是缩进续行（空行不算续行）。
func fmIndented(line []byte) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

// yamlDoubleQuoted 把自由文本包成 YAML 双引号标量。
//
// 为什么必须引号：reason 是用户自由文本，可能含 `,` `}` `:` `#` 等在流式 mapping 里
// 有语义的字符；双引号 + 最小转义是**确定性**的，不依赖任何序列化库（写路径禁用）。
func yamlDoubleQuoted(text string) []byte {
	out := make([]byte, 0, len(text)+2)
	out = append(out, '"')
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case '\\':
			out = append(out, '\\', '\\')
		case '"':
			out = append(out, '\\', '"')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, c)
		}
	}
	out = append(out, '"')
	return out
}

// —— frontmatter 标量序列化风格的统一（I-evergreen.system_assurance-158614-014）——
//
// 缺陷现场：同一份权威文件里出现三种标量风格 —— 建卡路径（content.go 的 quoted）写
// 单引号、状态写路径的时间戳写双引号（yamlDoubleQuoted）、`status` 直接裸写。
// 于是**同一个字段** status 在建卡是 'active'、在 deprecate/restore 是 active；
// 一次只改状态的 restore 会顺带产出引号变更噪声，掩盖真实语义变更。
// Markdown 是唯一权威来源、其 diff 要被人和 Agent 直接审阅，噪声因此有实质代价。
//
// 规范化的口径（**只统一序列化风格，不改变语义值**）：
//   - canonical = **单引号**。选它而不是双引号，因为建卡路径已经是单引号，
//     库里绝大多数 frontmatter 字节本就是这个风格，选它churn 最小，
//     且能让 status 在「建卡 / deprecate / restore」三条路径上回到同一形态。
//   - **只作用于字符串标量**。布尔量（`stale: true`）必须保持裸写：给它加引号会把
//     YAML 类型从 bool 变成 string —— 那是**语义变更**，越过了本次规范化的边界。
//   - 含换行的字符串**回退到双引号转义形态**：单引号 YAML 无法在一行内表示 \n，
//     强行单引号会丢字节。回退是为了无损，不是为了保留旧风格。
//
// 兼容性边界（append-only / 旧值兼容）：
//   - **读侧一律不收紧**：裸写 / 单引号 / 双引号三种历史形态都继续可读，
//     本改动不含任何解析侧收紧，历史文件不会因风格而变得不可读。
//   - **不批量重写历史文件**：写路径只替换本次真正要写的那个键的整行
//     （setFMScalarKey 的 Raw[:start]+line+Raw[end:]），其余键逐字不动。
//     因此历史文件上的旧风格只会在该键**下一次被真正写入时**顺带归一，
//     产生一次性、可解释的非语义 diff —— 这正是 Acceptance「diff 可解释」允许的形态，
//     且换来的是此后同一字段不再反复抖动。

// fmCanonicalScalar 把字符串标量序列化成 canonical 风格（单引号；含换行时回退双引号）。
//
// 与 content.go 的 quoted 同风格，但不返回 error：状态写路径的取值（时间戳、原因文本）
// 允许含换行，此时回退到 yamlDoubleQuoted 的转义形态以保证无损。
func fmCanonicalScalar(text string) []byte {
	if strings.ContainsAny(text, "\n\r") {
		return yamlDoubleQuoted(text)
	}
	out := make([]byte, 0, len(text)+2)
	out = append(out, '\'')
	for i := 0; i < len(text); i++ {
		if text[i] == '\'' {
			out = append(out, '\'', '\'')
			continue
		}
		out = append(out, text[i])
	}
	return append(out, '\'')
}
