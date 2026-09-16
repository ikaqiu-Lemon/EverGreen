package index_test

// T-…-005（knowledge_opinion_split · 批次 A）的机器判据：**派生索引 v2 的存储合同**。
//
// 判据来源：Schema v2 设计 §7 与决策记录 D-4 —— `cards` / `cards_fts` 各加一列 `kind`
// （取值 `knowledge` | `opinion`）与一列 `validation`（**仅** opinion 行非空），
// 表数量保持恰 6 张、`index_meta` 保持恰 6 键、`IndexSchemaVersion` 由 1 升至 2
// （版本不匹配 ⇒ 整库重建，永不写迁移脚本）。
//
// 本文件只钉**存储合同与无损往返**（批次 A 的全部范围）：opinions 的扫描面（谁把观点
// 喂进快照）属批次 B、`eg status` 的实体分计数与 CLI 观点行为属批次 C，这里一行都不碰。
//
// 两条反作弊约束，写在断言形态里而不是注释里：
//
//	① 拒绝必须是**约束级**拒绝：非法 kind / 非法 validation 的失败断言一律要求错误里出现
//	   `constraint` 字样。否则「列根本不存在」导致的 `no such column` 也会让「应当被拒绝」
//	   假绿 —— 那是最容易骗过评审的一种绿。
//	② 合法值必须**真的写得进去并逐字读得回来**：只证「非法被拒」不证「合法可存」，
//	   一个把整列写死成 `CHECK (0)` 的实现也能全绿。
//
// 先红形态（本文件即先红证据）：批次 A 实现前，`cards` / `cards_fts` 上没有 kind /
// validation 两列，也没有 `cards_kind_idx`，因此列集合等号、索引存在性、约束级拒绝、
// 合法值往返四类断言全部当场失败。

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// cardsColumnsV2 是 `cards` 表在 v2 下的**完整有序列清单**（等号，不是包含）。
//
// 次序即 DDL 次序：新列一律**追加在尾部**，绝不插在既有列中间 —— 既有列的序号是
// build.go 显式 rowid 插入与各处 `SELECT` 列序的共同前提，插队会让「同一份快照两次构建
// 逐字等价」这条判据以最难查的方式变红（Digest 不同但每列都"看着对"）。
var cardsColumnsV2 = []string{
	"id", "path", "domain", "title", "status",
	"deprecated", "deleted", "replaced_by", "content_hash", "mtime_unix",
	"kind", "validation",
}

// cardKindsWant 是 `cards.kind` 的封闭取值域，**在测试里逐字写死**（不从被测包取值）。
//
// 判据不能拿被测实现当期望：若这里写 `index.CardKinds()`，任何人把第三值加进产品代码，
// 断言会跟着放宽 —— 那时"等号"只证明了它自己等于自己。取值域的另一半判据（产品常量
// 必须等于这份字面量）在 TestCardKindsClosed 里单独钉。
var cardKindsWant = []string{"knowledge", "opinion"}

// ftsColumnsV2 是 `cards_fts` 表在 v2 下的完整有序列清单（等号）。
//
// kind / validation 在 FTS 侧必须是 **UNINDEXED**：一旦进倒排，裸 `MATCH 'opinion'`
// 就会命中每一条观点，检索结果被判别列污染。收窄检索面靠 `WHERE kind = ?`
// （UNINDEXED 列照样可比较），不靠把判别值塞进全文索引。
var ftsColumnsV2 = []string{"id", "title", "body", "bigram_text", "kind", "validation"}

// TestCardsTableColumnsClosedV2 反证 `cards` 的列集合在 v2 下**逐字等于** cardsColumnsV2。
//
// 用等号而不是「含 kind 即通过」：多一列同样是漂移 —— 下游（批次 B 的扫描面、批次 C 的
// status 分计数、T-006 的检索收窄）会把任何多出来的列当作可依赖事实消费。
func TestCardsTableColumnsClosedV2(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	if got := tableColumns(t, dir, index.TableCards); !equalOrdered(got, cardsColumnsV2) {
		t.Fatalf("cards 列清单漂移：\n实际 = %v\n期望 = %v", got, cardsColumnsV2)
	}
}

// TestCardsFTSColumnsClosedV2 反证 `cards_fts` 的列集合在 v2 下逐字等于 ftsColumnsV2，
// 且新增两列不进倒排（裸 MATCH 不得因判别值而命中）。
func TestCardsFTSColumnsClosedV2(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	if got := tableColumns(t, dir, index.TableCardsFTS); !equalOrdered(got, ftsColumnsV2) {
		t.Fatalf("cards_fts 列清单漂移：\n实际 = %v\n期望 = %v", got, ftsColumnsV2)
	}

	// UNINDEXED 的实测证据：judgement 值本身不可被全文检索命中。
	db := openFixture(t, dir, true)
	var hits int
	if err := db.QueryRow(`SELECT count(*) FROM `+index.TableCardsFTS+
		` WHERE `+index.TableCardsFTS+` MATCH ?`, "knowledge").Scan(&hits); err != nil {
		t.Fatalf("MATCH 'knowledge' 查询失败：%v", err)
	}
	if hits != 0 {
		t.Fatalf("裸 MATCH 'knowledge' 命中 %d 行：判别列进了倒排，会污染检索结果"+
			"（kind / validation 必须 UNINDEXED，收窄靠 WHERE kind = ?）", hits)
	}
}

// TestCardsKindIndexExists 反证 `cards_kind_idx` 真的建在库上、且真的建在 `kind` 列上。
//
// 只断言「索引名存在」是不够的：一个建在 `path` 上却取名 kind 的索引照样能骗过名字断言，
// 而检索收窄（`WHERE kind = ?`）会退化成全表扫。
func TestCardsKindIndexExists(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	db := openFixture(t, dir, true)

	var name, ddl string
	if err := db.QueryRow(`SELECT name, sql FROM sqlite_master
  WHERE type = 'index' AND name = ?`, "cards_kind_idx").Scan(&name, &ddl); err != nil {
		t.Fatalf("cards_kind_idx 未建在库上（%v）：按 kind 收窄检索面会退化成全表扫", err)
	}
	lower := strings.ToLower(ddl)
	if !strings.Contains(lower, "on "+index.TableCards) || !strings.Contains(lower, "(kind)") {
		t.Fatalf("cards_kind_idx 的 DDL 不是建在 %s(kind) 上：\n%s", index.TableCards, ddl)
	}
}

// TestCardsKindStorageConstraint 反证 **库上**（而不只是 Go 侧）对 kind 取值域的约束：
// 恰 knowledge / opinion 两值可写，空串与第三值一律被约束拒绝。
//
// 空串单独立一格：批次 A 明确禁止「空 kind 默认按 knowledge 落库」这类降级默认 ——
// 那会让任何忘记设 kind 的调用方静默产出「看起来是知识卡」的行，而漂移在读路径才暴露。
func TestCardsKindStorageConstraint(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	db := openFixture(t, dir, false)

	for _, k := range []string{"", "belief", "Knowledge", "opinions"} {
		err := insertCardRow(db, "k-probe-"+k, k, "")
		if err == nil {
			t.Fatalf("kind = %q 竟被库接受：取值域必须封闭在 %v", k, cardKindsWant)
		}
		if !isConstraintErr(err) {
			t.Fatalf("kind = %q 的拒绝不是约束级拒绝（实得 %v）：\n"+
				"「列不存在」之类的错误会让本断言假绿，必须由 CHECK 约束拒绝", k, err)
		}
	}
	// 合法侧：knowledge（validation 空）与 opinion（validation 三值）都必须写得进去。
	if err := insertCardRow(db, "k-ok-knowledge", "knowledge", ""); err != nil {
		t.Fatalf("合法的 knowledge 行被拒：%v（CHECK 不得收得比取值域更紧）", err)
	}
	for _, v := range []string{"pending", "validated", "rejected"} {
		if err := insertCardRow(db, "o-ok-"+v, "opinion", v); err != nil {
			t.Fatalf("合法的 opinion(validation=%s) 行被拒：%v", v, err)
		}
	}
}

// TestCardsValidationStorageConstraint 反证 validation 与 kind 的**交叉**约束落在库上：
//
//	knowledge → validation 必须为空串（知识卡没有论证进度这一维度）；
//	opinion   → validation 必须 ∈ {pending, validated, rejected}（空串亦非法）。
//
// 交叉约束必须在存储层：只在 Go 侧校验的话，任何绕过 writeAll 的写入（外部进程、
// 将来的第二条写路径）都能留下语义自相矛盾的行，而行级核对之前无人发现。
func TestCardsValidationStorageConstraint(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	db := openFixture(t, dir, false)

	illegal := []struct{ id, kind, validation string }{
		{"k-bad-1", "knowledge", "pending"},   // 知识卡不得带论证进度
		{"k-bad-2", "knowledge", "validated"}, //
		{"o-bad-1", "opinion", ""},            // 观点必须有论证进度
		{"o-bad-2", "opinion", "maybe"},       // 第四值一律拒绝
		{"o-bad-3", "opinion", "Pending"},     // 大小写变体亦非法（取值域逐字封闭）
	}
	for _, c := range illegal {
		err := insertCardRow(db, c.id, c.kind, c.validation)
		if err == nil {
			t.Fatalf("(kind=%q, validation=%q) 竟被库接受：交叉约束缺失", c.kind, c.validation)
		}
		if !isConstraintErr(err) {
			t.Fatalf("(kind=%q, validation=%q) 的拒绝不是约束级拒绝（实得 %v）",
				c.kind, c.validation, err)
		}
	}
}

// TestCardKindsClosed 钉住 `CardKinds()` 恰 knowledge / opinion 两值（次序也钉）。
//
// 期望值取自本文件顶部逐字写死的 cardKindsWant，不从被测包回读 —— 见那里的说明。
// 两个常量也一并钉住：调用方（批次 B / C 的扫描面与投影）必须用常量而不是裸字面量，
// 常量本身漂了就在这里当场红。
func TestCardKindsClosed(t *testing.T) {
	if got := index.CardKinds(); !equalOrdered(got, cardKindsWant) {
		t.Fatalf("CardKinds() = %v，期望恰 %v（第三值属独立里程碑变更，不是本 task 的自由度）",
			got, cardKindsWant)
	}
	if index.CardKindKnowledge != "knowledge" || index.CardKindOpinion != "opinion" {
		t.Fatalf("kind 常量漂移：CardKindKnowledge = %q，CardKindOpinion = %q",
			index.CardKindKnowledge, index.CardKindOpinion)
	}
}

// TestCardValidationsClosed 钉住 opinion 的论证进度取值域恰 pending / validated / rejected。
//
// 这三个字面量的**权威定义**在 `internal/model`（Schema v2 §6.1 的 Validation 枚举）。
// 索引层逐字写死而不是 import model：`Card` 是中性 DTO（字段全是标量，不引本仓结构体），
// 与 `SkippedKinds()` 逐字写死 M2 字面量同一处理。字面量一旦两边不一致，
// 批次 B 把观点喂进索引时会被 CHECK 当场拒绝 —— 不存在静默分叉的窗口。
func TestCardValidationsClosed(t *testing.T) {
	want := []string{"pending", "validated", "rejected"}
	if got := index.CardValidations(); !equalOrdered(got, want) {
		t.Fatalf("CardValidations() = %v，期望恰 %v（与 model.ValidValidations() 逐字同序）", got, want)
	}
	if index.ValidationPending != "pending" || index.ValidationValidated != "validated" ||
		index.ValidationRejected != "rejected" {
		t.Fatalf("validation 常量漂移：%q / %q / %q",
			index.ValidationPending, index.ValidationValidated, index.ValidationRejected)
	}
}

// TestWriteRejectsIllegalKindAndValidation 钉住**唯一写入口**上的 Go 侧校验：
// 非法 kind / 非法 (kind, validation) 组合一律在写盘前被拒，且拒绝理由指名道姓。
//
// 为什么库上已有 CHECK 还要这一层：CHECK 的报错是 SQLite 文案（`constraint failed`），
// 对调用方毫无定位价值；而写入口的错误必须说清「哪张卡、哪个值、封闭域是什么」，
// 否则批次 B 接扫描面时只能靠猜。两层都要，且**都不得**用「补个默认值」代替拒绝。
func TestWriteRejectsIllegalKindAndValidation(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		validation string
		wantIn     string // 错误里必须出现的定位串
	}{
		{"空 kind 不得默认按 knowledge 落库", "", "", "kind"},
		{"第三种 kind", "belief", "", "kind"},
		{"kind 大小写变体", "Knowledge", "", "kind"},
		{"knowledge 带 validation", "knowledge", "pending", "validation"},
		{"opinion 缺 validation", "opinion", "", "validation"},
		{"opinion 的第四种 validation", "opinion", "maybe", "validation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := knowledgeSnapshot()
			snap.Cards = append(snap.Cards, index.Card{
				ID: "x-probe", Path: "domains/ai/knowledge/x-probe.md", Domain: "ai",
				Title: "探针", Status: "active", ContentHash: "sha256:probe",
				MTimeUnix: 1_600_000_900, Kind: tc.kind, Validation: tc.validation,
			})
			dir := filepath.Join(t.TempDir(), index.DirName)
			_, err := index.Build(dir, snap, fixedOptions())
			if err == nil {
				t.Fatalf("(kind=%q, validation=%q) 竟建库成功：写入口必须拒绝，"+
					"绝不允许补默认值后放行", tc.kind, tc.validation)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("拒绝理由未指名 %q：%v", tc.wantIn, err)
			}
			// 反「静默降级」：失败后库里不得留下这张卡（尤其不得以 knowledge 身份存在）。
			if cards, readErr := index.ReadCards(dir); readErr == nil {
				for _, c := range cards {
					if c.ID == "x-probe" {
						t.Fatalf("写入口拒绝后库里仍留下 x-probe（kind=%q）：降级默认即漂移",
							c.Kind)
					}
				}
			}
		})
	}
}

// TestApplyRejectsIllegalKind 把同一条校验钉在**增量**入口上。
//
// 全量与增量共用 writeAll 这一个写入口，所以这条断言表面上冗余 —— 它的价值在于
// 「共用」本身是可以被将来某次重构悄悄拆开的（例如给增量加一条"快路径"）。
// 那一刻这条用例红，而不是等到某个用户的库里出现一行没有 kind 的卡。
func TestApplyRejectsIllegalKind(t *testing.T) {
	dir := buildFixture(t, knowledgeSnapshot())

	bad := index.Delta{Head: strings.Repeat("ab", 20)}
	bad.Cards = append(bad.Cards, index.Card{
		ID: "x-probe", Path: "domains/ai/knowledge/x-probe.md", Domain: "ai",
		Title: "探针", Status: "active", ContentHash: "sha256:probe",
		MTimeUnix: 1_600_000_900, Kind: "belief",
	})
	bad.Files = append(bad.Files, index.File{
		Path: "domains/ai/knowledge/x-probe.md", ContentHash: "sha256:probe",
		Size: 10, MTimeUnix: 1_600_000_900})

	if _, err := index.Apply(dir, bad, laterOptions()); err == nil {
		t.Fatalf("增量入口竟接受了非法 kind：全量 / 增量必须共用同一处校验")
	}
}

// TestCardKindValidationRoundtrip 是**无损往返**的正面判据：混合 kind 的快照经全量构建后，
// `cards` 与 `cards_fts` 两侧的 kind / validation 都必须逐字读得回来。
//
// 两侧都查：只读回 `cards` 的实现会让 FTS 侧的判别列静默留空，而 T-006 的检索收窄
// （`WHERE kind = ?` 打在 `cards_fts` 上）到那时才会以「观点搜不到」的形态暴露。
func TestCardKindValidationRoundtrip(t *testing.T) {
	snap := mixedKindSnapshot()
	dir := buildFixture(t, snap)

	want := map[string][2]string{} // id → {kind, validation}
	for _, c := range snap.Cards {
		want[c.ID] = [2]string{c.Kind, c.Validation}
	}

	got, err := index.ReadCards(dir)
	if err != nil {
		t.Fatalf("ReadCards 失败：%v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("读回 %d 张卡，期望 %d 张", len(got), len(want))
	}
	for _, c := range got {
		w, ok := want[c.ID]
		if !ok {
			t.Fatalf("读回了权威里没有的卡 id=%s", c.ID)
		}
		if c.Kind != w[0] || c.Validation != w[1] {
			t.Fatalf("cards 往返丢字段：id=%s 读回 (kind=%q, validation=%q)，期望 (%q, %q)",
				c.ID, c.Kind, c.Validation, w[0], w[1])
		}
	}

	// FTS 侧同款往返（判别列必须与 cards 逐行一致，否则收窄检索会漏 / 串）。
	db := openFixture(t, dir, true)
	rows, err := db.Query(`SELECT id, kind, validation FROM ` + index.TableCardsFTS + ` ORDER BY id`)
	if err != nil {
		t.Fatalf("读 %s 的判别列失败：%v", index.TableCardsFTS, err)
	}
	defer func() { _ = rows.Close() }()
	seen := 0
	for rows.Next() {
		var id, kind, validation string
		if err := rows.Scan(&id, &kind, &validation); err != nil {
			t.Fatalf("扫描 FTS 判别列失败：%v", err)
		}
		w, ok := want[id]
		if !ok {
			t.Fatalf("cards_fts 出现权威里没有的 id=%s", id)
		}
		if kind != w[0] || validation != w[1] {
			t.Fatalf("cards_fts 往返丢字段：id=%s 读回 (kind=%q, validation=%q)，期望 (%q, %q)",
				id, kind, validation, w[0], w[1])
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历 FTS 判别列失败：%v", err)
	}
	if seen != len(want) {
		t.Fatalf("cards_fts 有 %d 行，期望 %d 行", seen, len(want))
	}
}

// TestDigestCoversKindAndValidation 钉住**等价口径本身**必须看得见新列。
//
// Digest 是「增量 == 全量」这条判据的唯一裁判（合同 §4.4）。如果导出行不含 kind /
// validation，那么「增量把判别列丢空」这类漂移会让 Digest 依然相等 —— 裁判瞎了，
// 下面那条增量保留用例也就跟着假绿。所以先证裁判有眼睛：只改判别列，摘要必须变。
func TestDigestCoversKindAndValidation(t *testing.T) {
	base := mixedKindSnapshot()
	baseDigest := digestOf(t, buildFixture(t, base))

	// ① 只把一张知识卡改成观点（其余字段逐一保持原样）。
	flipped := mixedKindSnapshot()
	for i := range flipped.Cards {
		if flipped.Cards[i].ID == "k-beta" {
			flipped.Cards[i].Kind = index.CardKindOpinion
			flipped.Cards[i].Validation = index.ValidationPending
		}
	}
	if d := digestOf(t, buildFixture(t, flipped)); d == baseDigest {
		t.Fatalf("只改 kind 而 Digest 不变：等价口径没有覆盖 kind 列，"+
			"「增量 == 全量」这条判据形同虚设（digest = %s）", d)
	}

	// ② 只把一条观点的论证进度从 pending 改成 validated。
	revalidated := mixedKindSnapshot()
	for i := range revalidated.Cards {
		if revalidated.Cards[i].ID == opinionPendingID {
			revalidated.Cards[i].Validation = index.ValidationValidated
		}
	}
	if d := digestOf(t, buildFixture(t, revalidated)); d == baseDigest {
		t.Fatalf("只改 validation 而 Digest 不变：等价口径没有覆盖 validation 列")
	}
}

// TestIncrementalPreservesKindAndValidation 是本批次最核心的一条：**增量不得丢新列**。
//
// 增量路径是「读回现态 → 清空派生表 → 整体重写」（incremental.go 的
// readStateTx → clearDerived → writeAll）。只要读回那一步漏读 kind / validation，
// **未受影响**的行就会以空判别值被重写回去 —— CHECK 会当场拒绝（好一点的结局），
// 或者在 CHECK 缺位时静默漂移成「无 kind 的卡」（坏结局）。
//
// 判据取两条：① 增量后未受影响的观点行判别列逐字不变；
// ② 增量结果与「对同一现态直接全量构建」在 Digest 口径下逐字等价。
func TestIncrementalPreservesKindAndValidation(t *testing.T) {
	dir := buildFixture(t, mixedKindSnapshot())

	// 只改一张知识卡（观点一张都不碰）——「未受影响的行原样保留」正是要证的。
	now := mixedKindSnapshot()
	const touched = "domains/ai/knowledge/k-beta.md"
	for i := range now.Cards {
		if now.Cards[i].Path == touched {
			now.Cards[i].Body = "这是正文 attention 机制（改）"
			now.Cards[i].ContentHash = "sha256:bbbb-v2"
		}
	}
	for i := range now.Files {
		if now.Files[i].Path == touched {
			now.Files[i].ContentHash = "sha256:bbbb-v2"
			now.Files[i].MTimeUnix = 1_600_009_100
		}
	}

	res, err := index.Apply(dir, deltaFor(now, []string{touched}, nil), laterOptions())
	if err != nil {
		t.Fatalf("Apply 失败：%v", err)
	}
	if res.Action != index.ActionSynced || res.Degraded {
		t.Fatalf("动作 = %q（degraded=%v），期望正常增量 %q",
			res.Action, res.Degraded, index.ActionSynced)
	}

	cards, err := index.ReadCards(dir)
	if err != nil {
		t.Fatalf("ReadCards 失败：%v", err)
	}
	want := map[string][2]string{}
	for _, c := range now.Cards {
		want[c.ID] = [2]string{c.Kind, c.Validation}
	}
	for _, c := range cards {
		w := want[c.ID]
		if c.Kind != w[0] || c.Validation != w[1] {
			t.Fatalf("增量后判别列漂移：id=%s 实得 (kind=%q, validation=%q)，期望 (%q, %q)"+
				"（未受影响的行必须原样保留 —— 读回现态时漏读新列即此结局）",
				c.ID, c.Kind, c.Validation, w[0], w[1])
		}
	}
	if got, fresh := digestOf(t, dir), freshDigestOf(t, now); got != fresh {
		t.Fatalf("增量结果与全量重建不等价：\n增量 = %s\n全量 = %s", got, fresh)
	}
}

// TestCheckDetectsKindAndValidationLies 把行级核对面扩到新列：判别列上的撒谎必须被抓。
//
// 这条用例同时封掉一个**极易假绿**的实现漏洞：`cardColumn` / `ftsColumn` 是
// `switch col` + `default: return ""` 形态。若把 "kind" 加进核对列清单却忘了加 case，
// 期望值与实得值双双取到空串，逐列比对**恒相等** —— 核对面看起来扩大了，实际一格没扩。
// 因此断言不止要求「判 corrupt」，还要求诊断信息**指名到列**。
func TestCheckDetectsKindAndValidationLies(t *testing.T) {
	base := mixedKindSnapshot()
	if c := index.Check(buildFixture(t, base), currentWithCards(base)); !c.Fresh() || c.Code != "" {
		t.Fatalf("健康库 + 权威投影必须 fresh + 零码，实得 %s / %q（%s）",
			c.Freshness, c.Code, c.Message)
	}

	cases := []struct {
		name   string
		column string
		tamper func(t *testing.T, dir string)
	}{
		{
			name:   "把知识卡的 kind 篡改成 opinion",
			column: "kind",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir, `UPDATE `+index.TableCards+
					` SET kind = ?, validation = ? WHERE id = ?`,
					index.CardKindOpinion, index.ValidationPending, "k-beta")
			},
		},
		{
			name:   "把观点的 validation 从 pending 改成 validated",
			column: "validation",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir, `UPDATE `+index.TableCards+
					` SET validation = ? WHERE id = ?`, index.ValidationValidated, opinionPendingID)
			},
		},
		{
			name:   "只篡改 cards_fts 侧的 kind（cards 仍诚实）",
			column: "kind",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir, `UPDATE `+index.TableCardsFTS+
					` SET kind = ? WHERE id = ?`, index.CardKindOpinion, "k-beta")
			},
		},
		{
			name:   "只篡改 cards_fts 侧的 validation",
			column: "validation",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir, `UPDATE `+index.TableCardsFTS+
					` SET validation = ? WHERE id = ?`, index.ValidationRejected, opinionPendingID)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := buildFixture(t, mixedKindSnapshot())
			if c := index.Check(dir, currentWithCards(mixedKindSnapshot())); !c.Fresh() {
				t.Fatalf("篡改前应 fresh，实得 %s（%s）", c.Freshness, c.Message)
			}
			tc.tamper(t, dir)

			c := index.Check(dir, currentWithCards(mixedKindSnapshot()))
			if !c.Unusable() || c.Code != index.CodeIndexCorrupt ||
				c.Reason != index.ReasonRowLevelDivergence {
				t.Fatalf("判别列撒谎必须判 unusable + %s + %s，实得 %s / %q / %q（%s）",
					index.CodeIndexCorrupt, index.ReasonRowLevelDivergence,
					c.Freshness, c.Code, c.Reason, c.Message)
			}
			if !strings.Contains(c.Message, tc.column) {
				t.Fatalf("诊断信息未指名到列 %q（实得 %q）：\n"+
					"逐列比对函数漏了这一列的 case 时，期望与实得双双取空串会导致恒相等假绿",
					tc.column, c.Message)
			}
		})
	}
}

// —— 测试脚手架（只在本文件内使用；不改既有 fixture 的语义）——

// 观点卡的固定 id / 路径（`domains/<域>/opinions/o-*.md` 是 Schema v2 §4 的观点落点）。
const (
	opinionPendingID   = "o-20260916-pending"
	opinionValidatedID = "o-20260916-validated"
	opinionRejectedID  = "o-20260916-rejected"
)

// knowledgeSnapshot 是 sampleSnapshot 的**显式 knowledge 版**：每张卡都写明
// `Kind: knowledge` 与空 validation。
//
// 批次 A 起，快照里的每一张卡都必须显式带 kind —— 既有 fixture 也一并显式化（见
// build_test.go）。本函数只是把那份显式化的样本借过来用，不引入第二套语料。
func knowledgeSnapshot() index.Snapshot { return sampleSnapshot() }

// mixedKindSnapshot 是混合 kind 的快照：3 张知识卡（沿用样本语料）+ 3 条观点，
// 观点各占一个 validation 档位（pending / validated / rejected 全覆盖）。
//
// 观点故意插在卡片切片的**中间与开头**（不是尾部追加）：确定性排序由 index 包负责，
// 输入次序不得影响结果 —— 这一点与 sampleSnapshot 的乱序意图同源。
func mixedKindSnapshot() index.Snapshot {
	snap := sampleSnapshot()
	opinions := []index.Card{
		{
			ID: opinionRejectedID, Path: "domains/ops/opinions/" + opinionRejectedID + ".md",
			Domain: "ops", Title: "被否的判断", Status: "active",
			Body: "论据不足，已确认不成立", ContentHash: "sha256:o-rejected",
			MTimeUnix: 1_600_000_500,
			Kind:      index.CardKindOpinion, Validation: index.ValidationRejected,
		},
		{
			ID: opinionPendingID, Path: "domains/ai/opinions/" + opinionPendingID + ".md",
			Domain: "ai", Title: "待验证的判断", Status: "active",
			Body: "检索增强在长文场景更划算 attention", ContentHash: "sha256:o-pending",
			MTimeUnix: 1_600_000_300,
			Kind:      index.CardKindOpinion, Validation: index.ValidationPending,
		},
		{
			ID: opinionValidatedID, Path: "domains/ai/opinions/" + opinionValidatedID + ".md",
			Domain: "ai", Title: "已验证的判断", Status: "deprecated", Deprecated: true,
			Body: "分词降级到 bigram 是可接受的", ContentHash: "sha256:o-validated",
			MTimeUnix: 1_600_000_400,
			Kind:      index.CardKindOpinion, Validation: index.ValidationValidated,
		},
	}
	snap.Cards = append(opinions[:1:1], append(snap.Cards, opinions[1:]...)...)
	snap.Files = append(snap.Files,
		index.File{Path: "domains/ops/opinions/" + opinionRejectedID + ".md",
			ContentHash: "sha256:o-rejected", Size: 140, MTimeUnix: 1_600_000_500},
		index.File{Path: "domains/ai/opinions/" + opinionPendingID + ".md",
			ContentHash: "sha256:o-pending", Size: 120, MTimeUnix: 1_600_000_300},
		index.File{Path: "domains/ai/opinions/" + opinionValidatedID + ".md",
			ContentHash: "sha256:o-validated", Size: 130, MTimeUnix: 1_600_000_400},
	)
	// 观点参与关系图（`relations` 天然跨类型，决策记录 D-4 的理由 (b)）。
	snap.Relations = append(snap.Relations, index.Relation{
		SrcID: opinionPendingID, Verb: "supports", DstID: "k-beta",
		SrcPath: "domains/ai/opinions/" + opinionPendingID + ".md",
	})
	return snap
}

// tableColumns 按 DDL 次序取一张表的列名（PRAGMA table_info 的 cid 序即列序）。
func tableColumns(t *testing.T, dir, table string) []string {
	t.Helper()
	db := openFixture(t, dir, true)
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatalf("查 %s 的列清单失败：%v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("扫描列名失败：%v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历列名失败：%v", err)
	}
	return out
}

// insertCardRow 直接向 `cards` 写一行（绕过 Go 侧校验）：用来反证约束真的在**库上**。
//
// 除 kind / validation 外的列取无关紧要的定值：本函数只服务约束断言，不参与等价比较。
func insertCardRow(db *sql.DB, id, kind, validation string) error {
	_, err := db.Exec(`INSERT INTO `+index.TableCards+
		`(id, path, domain, title, status, deprecated, deleted, replaced_by,
		  content_hash, mtime_unix, kind, validation)
		 VALUES(?, ?, ?, ?, ?, 0, 0, '', ?, 0, ?, ?)`,
		id, "domains/probe/knowledge/"+id+".md", "probe", "探针", "active",
		"sha256:probe", kind, validation)
	return err
}

// isConstraintErr 判「这是不是一次约束级拒绝」（SQLite 的 CHECK 违约文案含 constraint）。
func isConstraintErr(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "constraint")
}

// equalOrdered 比较两个字符串切片是否**逐位**相等（列序也是合同的一部分）。
func equalOrdered(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
