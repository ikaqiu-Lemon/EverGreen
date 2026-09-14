package proposal

// T2：`pending → rejected` 的落盘（提案合同 §2.3 第 3 行；由 `eg proposal reject` 调用）。
//
// 为什么单独一支而不是复用 MarkApproved：两条边的**必写字段列不同** —— T2 除
// `decision.result` 外还必须写 `decision.reason`（§4.4 十项必备 ⑨「拒绝必带 reason」），
// 而 T1 不要求 reason。把两者揉成一个带布尔开关的函数，会让「哪条边必写什么」这件事
// 从状态表漂到调用点。
//
// 本文件**不新增任何判定**：合法边由 CheckTransition 说话（`rejected` 是终态，
// 任意再次迁移命中 §2.4 的 12 条非法边 → 上层映射为 E9），
// `status ⟷ decision.result` 一致性由 verifyControlPlane 里的 CheckDecisionConsistency
// 说话（不一致 → 上层映射为 E8）。写口仍是 A-23 的 guarded 提案写口，一个字节都不裸写。

import "github.com/ikaqiu-Lemon/EverGreen/internal/store"

// RejectSpec 是一次 T2 落盘的输入。
//
// Reason 必填：T2 的必写字段列含 decision.reason，缺失时写前复核会拒写（零写入）。
type RejectSpec struct {
	Store       *store.Store
	Original    ID
	OriginalRel string
	Reason      string
}

// MarkRejected 落地 T2（pending → rejected）：只改 status 与 decision 两处。
//
// `execution` 块一个字节都不碰（矩阵 #39：execution 是 CLI 执行器独占）——
// 被拒绝的提案永远停在 `not_started`，这正是可达矩阵里 `rejected` 行的唯一 ✅ 格。
func MarkRejected(spec RejectSpec) (store.Result, error) {
	if spec.Store == nil {
		return store.Result{}, ErrNoStore
	}
	rel := spec.OriginalRel
	if rel == "" {
		rel = Rel(spec.Original)
	}
	f, err := spec.Store.Read(rel)
	if err != nil {
		return store.Result{Path: rel}, err
	}
	orig, err := Parse(f.Bytes)
	if err != nil {
		return store.Result{Path: rel}, err
	}
	if err := ValidateLayout(orig); err != nil {
		return store.Result{Path: rel}, err
	}
	// 终态出边在这里就被拦住：rejected → rejected 是 §2.4 的自环非法边（E9），
	// 拦在写盘之前 ⇒ 失败即零写入，不靠回滚。
	if err := CheckTransition(orig.P.Status, StatusRejected); err != nil {
		return store.Result{Path: rel}, err
	}
	sets := []fmScalar{
		{Key: KeyStatus, Value: string(StatusRejected)},
		{Block: KeyDecision, Key: KeyResult, Value: string(StatusRejected)},
		{Block: KeyDecision, Key: KeyReason, Value: spec.Reason},
	}
	out, err := applyFMScalars(f.Bytes, sets)
	if err != nil {
		return store.Result{Path: rel}, err
	}
	if err := verifyControlPlane(out, nil); err != nil {
		return store.Result{Path: rel}, err
	}
	return spec.Store.ApplyProposalUpdate(store.ProposalUpdateSpec{
		Rel: rel, ExpectedHash: f.Hash, Content: out,
	})
}
