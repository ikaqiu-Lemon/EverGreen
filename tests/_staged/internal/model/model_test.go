package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// —— 状态枚举只有两值（EG-KNW-01；第三值属已废弃设计）——

func TestStatusThirdValueFailsOnDeserialize(t *testing.T) {
	var card struct {
		Status Status `yaml:"status" json:"status"`
	}
	for _, third := range []string{"candidate", "draft", "Active", "none"} {
		err := yaml.Unmarshal([]byte("status: "+third+"\n"), &card)
		if err == nil {
			t.Fatalf("status: %q 反序列化应报错", third)
		}
		if !strings.Contains(err.Error(), "active") || !strings.Contains(err.Error(), "deprecated") {
			t.Fatalf("错误信息必须含合法取值集合，实际：%v", err)
		}
		if err := json.Unmarshal([]byte(`{"status":"`+third+`"}`), &card); err == nil {
			t.Fatalf("JSON status=%q 也应报错", third)
		}
	}
	if err := yaml.Unmarshal([]byte("status: active\n"), &card); err != nil {
		t.Fatalf("active 应被接受：%v", err)
	}
	if card.Status != StatusActive {
		t.Fatalf("解析结果 = %q", card.Status)
	}
	if err := yaml.Unmarshal([]byte("status: deprecated\n"), &card); err != nil {
		t.Fatalf("deprecated 应被接受：%v", err)
	}
	if !card.Status.Deprecated() {
		t.Fatal("deprecated 应触发 [失效] 显著标记")
	}
	if len(ValidStatuses()) != 2 {
		t.Fatalf("合法状态必须恰两值，实际 %v", ValidStatuses())
	}
	// 缺失 / null 的 status 落成零值：零值不是合法状态，必须由上层判为缺字段。
	var missing struct {
		Status Status `yaml:"status"`
	}
	if err := yaml.Unmarshal([]byte("status:\n"), &missing); err != nil {
		t.Fatalf("null status 由上层按缺字段处理，解析本身不应报错：%v", err)
	}
	if missing.Status.Valid() {
		t.Fatal("空 status 不得判为合法")
	}
	if _, err := ParseStatus(""); err == nil {
		t.Fatal("ParseStatus(\"\") 必须报错")
	}
}

// —— 三类 ID 互不可赋值（编译期）+ 构造期前缀校验 ——

func TestIDTypesAreDistinctAtCompileTime(t *testing.T) {
	src := reflect.TypeOf(SourceID(""))
	note := reflect.TypeOf(NoteID(""))
	card := reflect.TypeOf(CardID(""))
	for _, pair := range [][2]reflect.Type{{note, card}, {card, note}, {src, card}, {note, src}} {
		if pair[0].AssignableTo(pair[1]) {
			t.Fatalf("%s 不得可赋值给 %s（冻结合同 F2：三类 ID 互不可混用）", pair[0], pair[1])
		}
	}
}

func TestIDConstructorsRejectWrongPrefix(t *testing.T) {
	if _, err := ParseNoteID("k-20260901-alpha"); err == nil {
		t.Fatal("把 k-… 构造成 NoteID 必须报错")
	}
	if _, err := ParseCardID("n-20260901-alpha"); err == nil {
		t.Fatal("把 n-… 构造成 CardID 必须报错")
	}
	if _, err := ParseSourceID("k-20260901-alpha"); err == nil {
		t.Fatal("把 k-… 构造成 SourceID 必须报错")
	}
	if id, err := ParseCardID("k-20260901-alpha"); err != nil || id != CardID("k-20260901-alpha") {
		t.Fatalf("合法卡 ID 解析失败：%v / %q", err, id)
	}
	for _, bad := range []string{"k-2026091-alpha", "k-20260901-", "k-20260901", "x-20260901-a", ""} {
		if _, err := ParseCardID(bad); err == nil {
			t.Fatalf("非法 ID %q 应报错", bad)
		}
	}
}

func TestIDGenerationIsIdempotentAndSlugIsASCII(t *testing.T) {
	d, err := ParseDate("2026-09-01")
	if err != nil {
		t.Fatal(err)
	}
	a := NewCardID(d, "RAG 的 Chunk 粒度：Trade-off!")
	b := NewCardID(d, "RAG 的 Chunk 粒度：Trade-off!")
	if a != b {
		t.Fatalf("同一标题两次生成 ID 必须相同：%q vs %q", a, b)
	}
	if !strings.HasPrefix(string(a), PrefixCard+"20260901-") {
		t.Fatalf("ID 形态应为 k-<yyyymmdd>-<slug>，实际 %q", a)
	}
	for i := 0; i < len(a); i++ {
		if a[i] >= 0x80 {
			t.Fatalf("ID / slug 必须是纯 ASCII，实际 %q", a)
		}
	}
	// 纯非 ASCII 标题：slug 退化为确定性十六进制，仍是纯 ASCII 且幂等。
	c1, c2 := NewCardID(d, "中文标题"), NewCardID(d, "中文标题")
	if c1 != c2 {
		t.Fatalf("非 ASCII 标题的 ID 也必须幂等：%q vs %q", c1, c2)
	}
	for i := 0; i < len(c1); i++ {
		if c1[i] >= 0x80 {
			t.Fatalf("slug 退化结果必须是纯 ASCII，实际 %q", c1)
		}
	}
}

func TestSlugDoesNotAffectParseOrEquality(t *testing.T) {
	// slug 仅供人眼可读、不参与任何判定：换掉 slug 不影响解析结果（前缀 + 日期）；
	// ID 的相等判定是整串精确比较，从不按 slug 做模糊匹配。
	p1, err := ParseID("k-20260901-chunk-size")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := ParseID("k-20260901-完全不同的-slug-x9")
	if err != nil {
		t.Fatal(err)
	}
	if p1.Prefix != p2.Prefix || p1.Date != p2.Date {
		t.Fatalf("改 slug 不应改变解析出的前缀与日期：%+v vs %+v", p1, p2)
	}
	if p1.Slug == p2.Slug {
		t.Fatal("用例构造失败：两个 slug 应不同")
	}
	if CardID("k-20260901-chunk-size") == CardID("k-20260901-完全不同的-slug-x9") {
		t.Fatal("不同 ID 串不得判为相等")
	}
	if CardID("k-20260901-chunk-size") != CardID("k-20260901-chunk-size") {
		t.Fatal("同一 ID 串必须相等")
	}
}

func TestReservedPrefixesForS2(t *testing.T) {
	if PrefixReview != "r-" || PrefixProposal != "p-" {
		t.Fatalf("S2 预留前缀应为 r- / p-，实际 %q / %q", PrefixReview, PrefixProposal)
	}
}

// —— 时间格式两类 ——

func TestCreatedAtAcceptsDateOnly(t *testing.T) {
	if _, err := ParseDate("2026-09-01"); err != nil {
		t.Fatalf("YYYY-MM-DD 应被接受：%v", err)
	}
	for _, bad := range []string{
		"2026-09-01T10:00:00Z", "2026-09-01 10:00:00", "2026-9-1", "2026-09-01Z", "",
	} {
		if _, err := ParseDate(bad); err == nil {
			t.Fatalf("created_at = %q 应报错（带时间即非法）", bad)
		}
	}
	var doc struct {
		CreatedAt Date `yaml:"created_at"`
	}
	if err := yaml.Unmarshal([]byte("created_at: 2026-09-01T10:00:00Z\n"), &doc); err == nil {
		t.Fatal("YAML 侧 created_at 带时间也应报错")
	}
}

func TestTimestampsRequireTimezone(t *testing.T) {
	for _, ok := range []string{"2026-09-01T10:30:00+08:00", "2026-09-01T02:30:00Z"} {
		if _, err := ParseStamp(ok); err != nil {
			t.Fatalf("%q 应被接受：%v", ok, err)
		}
	}
	for _, bad := range []string{"2026-09-01T10:30:00", "2026-09-01", "2026-09-01 10:30:00+08:00", ""} {
		if _, err := ParseStamp(bad); err == nil {
			t.Fatalf("updated_at / saved_at = %q 应报错（无时区即非法）", bad)
		}
	}
	var doc struct {
		UpdatedAt Stamp `yaml:"updated_at"`
		SavedAt   Stamp `yaml:"saved_at"`
	}
	if err := yaml.Unmarshal([]byte("updated_at: 2026-09-01T10:30:00\nsaved_at: 2026-09-01T10:30:00Z\n"), &doc); err == nil {
		t.Fatal("YAML 侧无时区 updated_at 应报错")
	}
	if err := yaml.Unmarshal([]byte("updated_at: 2026-09-01T10:30:00+08:00\nsaved_at: 2026-09-01T10:30:00Z\n"), &doc); err != nil {
		t.Fatalf("带时区应被接受：%v", err)
	}
	// updated_at > reviewed_at 在同日多次编辑时仍可比较
	early := NewStamp(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC))
	late := NewStamp(time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC))
	if !early.Before(late) {
		t.Fatal("同日不同时刻必须可比较")
	}
}

// —— 关系枚举（冻结合同 F4）——

func TestRelationEnums(t *testing.T) {
	if len(ValidMaterialRels()) != 3 || len(ValidRelationTypes()) != 4 {
		t.Fatalf("材料关系 3 值 / 论证关系 4 值，实际 %v / %v",
			ValidMaterialRels(), ValidRelationTypes())
	}
	for _, bad := range []string{"refines", "related_to", "supports"} {
		if _, err := ParseMaterialRel(bad); err == nil {
			t.Fatalf("材料关系 %q 应报错", bad)
		}
	}
	for _, bad := range []string{"refines", "related_to", "context"} {
		if _, err := ParseRelationType(bad); err == nil {
			t.Fatalf("论证关系 %q 应报错", bad)
		}
	}
	var rel struct {
		Type RelationType `yaml:"type"`
	}
	if err := yaml.Unmarshal([]byte("type: refines\n"), &rel); err == nil {
		t.Fatal("YAML 侧非法论证关系应报错")
	}
	if err := yaml.Unmarshal([]byte("type: opposing\n"), &rel); err != nil {
		t.Fatalf("opposing 应被接受：%v", err)
	}
}

// —— sources[] 四要素（EG-KNW-04）——

func TestSourceRefCompleteness(t *testing.T) {
	full := SourceRef{Source: "s-20260901-a", Note: "n-20260901-a", Rel: MaterialSupport, Reason: "r"}
	if !full.Complete() || len(full.MissingFields()) != 0 {
		t.Fatalf("四要素齐全应判完整：%v", full.MissingFields())
	}
	for _, tc := range []struct {
		mutate func(*SourceRef)
		want   string
	}{
		{func(r *SourceRef) { r.Source = "" }, "source"},
		{func(r *SourceRef) { r.Note = "" }, "note"},
		{func(r *SourceRef) { r.Rel = "" }, "rel"},
		{func(r *SourceRef) { r.Reason = "" }, "reason"},
	} {
		r := full
		tc.mutate(&r)
		if r.Complete() {
			t.Fatalf("缺 %s 应判不完整", tc.want)
		}
		if got := r.MissingFields(); len(got) != 1 || got[0] != tc.want {
			t.Fatalf("缺失要素应为 [%s]，实际 %v", tc.want, got)
		}
	}
	// note 存笔记 ID，不存路径
	if _, err := ParseNoteID("domains/x/notes/n-20260901-a.md"); err == nil {
		t.Fatal("sources[].note 只接受笔记 ID，不接受路径")
	}
}

// —— 黑名单按字段路径判定（禁止关键词扫描）——

func TestBlacklistIsFieldPathBased(t *testing.T) {
	for _, deprecated := range []string{
		"card.domain", "card.type", "card.candidate", "card.source_check", "card.lean",
		"note.domain", "note.type",
	} {
		if !IsDeprecatedFieldPath(deprecated) {
			t.Fatalf("%s 应在黑名单内", deprecated)
		}
	}
	// 反证：白名单用法一律不得命中黑名单（关键词扫描会把它们误伤）
	for _, allowed := range []string{
		"relations[].type", "sources[].rel", "plan.domain", "target_domain",
		"unprocessed.target_domain", "--domain", "proposal.type",
	} {
		if IsDeprecatedFieldPath(allowed) {
			t.Fatalf("%s 是白名单用法，绝不得判为废弃字段", allowed)
		}
		if !IsAllowedFieldPath(allowed) {
			t.Fatalf("%s 应在白名单列表内", allowed)
		}
	}
	if len(DeprecatedFieldPaths) == 0 {
		t.Fatal("黑名单必须以字段路径列表形式暴露")
	}
	for _, p := range DeprecatedFieldPaths {
		if !strings.Contains(p, ".") {
			t.Fatalf("黑名单项 %q 必须是字段路径（含所属产物前缀），不得是裸关键词", p)
		}
	}
}

// —— 材料笔记没有 status，也不生成理解自检 ——

func TestNoteHasNoStatusField(t *testing.T) {
	rt := reflect.TypeOf(Note{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Name == "Status" || strings.HasPrefix(f.Tag.Get("yaml"), "status") {
			t.Fatalf("材料笔记不得有 status 字段（EG-NOTE-01），实际字段 %s", f.Name)
		}
	}
	if _, ok := rt.FieldByName("Status"); ok {
		t.Fatal("材料笔记不得有 Status 字段")
	}
}

// —— 状态与删除两个正交维度（F3）——

func TestStatusAndDeletionAreOrthogonal(t *testing.T) {
	rt := reflect.TypeOf(Card{})
	for _, name := range []string{"Status", "DeletedAt", "DeletedReason", "ReplacedBy", "ReviewedAt"} {
		if _, ok := rt.FieldByName(name); !ok {
			t.Fatalf("知识卡缺字段 %s（F3 两维度 + S2 预留字段须存在以便原样保留）", name)
		}
	}
	raw := "id: k-20260901-a\nstatus: deprecated\ncreated_at: 2026-09-01\n" +
		"updated_at: 2026-09-01T10:00:00Z\ndeleted_at: 2026-09-01T11:00:00Z\n" +
		"deleted_reason: 用户删除\nunknown_key: 原样保留\n"
	var c Card
	if err := yaml.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("S2 预留字段与未知字段都应能读到而不报错：%v", err)
	}
	if c.Status != StatusDeprecated || c.DeletedAt == nil {
		t.Fatalf("status 与 deleted_at 是正交两维，须各自读到：%+v", c)
	}
	if got := c.Extra["unknown_key"]; got != "原样保留" {
		t.Fatalf("未知字段须落进原样透传容器 Extra，实际 %v", c.Extra)
	}
}

// —— commit verb ——

func TestNormalizeVerbFallsBackToProcess(t *testing.T) {
	// M2（T-…-024）起 relate 纳入 KnownVerbs：五个 → 六个。改动理由见该 task 的 Activity Log
	// ——`eg rel add` 的 commit 主题必须是 `relate(<domain>): …`，relate 若仍属未知
	// verb，Message.Build 会把它退化成 process 并报 warning，M2 完成判据 5 无法成立。
	// M3（T-…-040）起 proposal 纳入 KnownVerbs：六个 → 七个。理由同 relate ——
	// `eg proposal new` / `reject` 的 commit 主题必须是 `proposal(<domain>): …`
	// （提案合同 §10.2 时序第 5 步），否则 Message.Build 会把它退化成 process。
	// M3（T-…-041）起 delete 纳入 KnownVerbs：七个 → 八个。理由同 proposal ——
	// `eg delete` 的 commit 主题必须是 `delete(<domain>): …`（提案与状态合同 §5.3
	// 时序第 10 步），否则 Message.Build 会把它退化成 process。
	if len(KnownVerbs()) != 8 {
		t.Fatalf("verb 恰八个（M2 起含 relate、M3 起含 proposal / delete），实际 %v", KnownVerbs())
	}
	if v, ok := NormalizeVerb("capture"); !ok || v != VerbCapture {
		t.Fatalf("已知 verb 不应退化：%q %v", v, ok)
	}
	for _, unknown := range []string{"frobnicate", ""} {
		v, ok := NormalizeVerb(unknown)
		if ok || v != VerbProcess {
			t.Fatalf("未知 verb %q 应退化为 process 并报 false，实际 %q %v", unknown, v, ok)
		}
	}
}

// TestNormalizeVerbRelateIsKnown —— relate 不再退化（T-…-024 Acceptance）。
func TestNormalizeVerbRelateIsKnown(t *testing.T) {
	v, ok := NormalizeVerb("relate")
	if !ok || v != VerbRelate {
		t.Fatalf("NormalizeVerb(\"relate\") 应返回 (VerbRelate, true)，实际 (%q, %v)", v, ok)
	}
	if !VerbRelate.Known() {
		t.Fatal("VerbRelate 必须属已知 verb，否则 relate commit 主题会写成 process(<domain>)")
	}
	seen := map[Verb]bool{}
	for _, k := range KnownVerbs() {
		if seen[k] {
			t.Fatalf("KnownVerbs 出现重复项 %q", k)
		}
		seen[k] = true
	}
	if !seen[VerbRelate] || !seen[VerbProposal] || !seen[VerbDelete] || len(seen) != 8 {
		t.Fatalf("KnownVerbs 必须恰八个且含 relate / proposal / delete，实际 %v", KnownVerbs())
	}
}

// —— Config ——

func TestConfigShape(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n"), &c); err != nil {
		t.Fatal(err)
	}
	if c.Version != ConfigVersion || !c.HasDomain("ai-infra") || !c.DefaultDomainConfigured() {
		t.Fatalf("配置解析异常：%+v", c)
	}
	var empty Config
	if empty.DefaultDomainConfigured() {
		t.Fatal("未配置 default_domain 时必须判为未配置（CLI 绝不自选默认领域）")
	}
	if len(ConfigKeys()) != 2 {
		t.Fatalf("S1 只两个可操作键，实际 %v", ConfigKeys())
	}
}
