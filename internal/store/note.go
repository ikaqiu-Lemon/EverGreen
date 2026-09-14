package store

// `write_note` 与 `add_open_question` 的落盘（技术方案 §4.3 / §4.4；T-…-013）。
//
// 口径：
//   - 新建笔记走 CreateFile（字节按模板拼装，不覆盖既有文件）；已有笔记**默认复用不重写**
//     （EG-NOTE-05）——返回 Reused 且目标文件字节不变。
//   - 笔记 frontmatter 恰 `id` / `source`（单值）/ `created_at` / `updated_at`（+ 可选
//     `title` / `tags`）：**没有 status**，正文**没有「理解自检」**（EG-NOTE-01）。
//   - 「产出知识卡」是本次加工快照（`- k-…（新建｜复用｜补充）`），此后不随卡片演进回写（EG-SRC-03）。
//   - 笔记写成功后在同一次调用里把收件区条目移出（EG-SRC-02，键为 `source_id`）；
//     S1 不保证两次写入强原子：条目未成功移出时**如实返回** InboxSkip（*SkipError），
//     调用方必须据此进报告（退 3），不得静默。
//   - 「用户补充」永不写、「存疑与待验证」既有字节逐字保留：由 WriteGuarded / CreateFile 兜底（B2）。

import (
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrNoteRelRequired 缺目标相对路径：路径由上层（plan 展开）按领域算好后传入。
var ErrNoteRelRequired = errors.New("缺笔记目标相对路径")

// InboxSpec 描述本次写入要移出的收件区条目。
type InboxSpec struct {
	Rel          string // 空 → UnprocessedFile
	ExpectedHash string // eg context 读到的 content_hash（B3；空 → 不比对）
	Detached     bool   // true → 本次完全不动收件区
}

// NoteSpec 是一次 write_note 的落盘输入。Sections 的载荷逐字插入，必须以 \n 结束。
// Date / Stamp 分别落到 frontmatter 的 `created_at`（YYYY-MM-DD）与
// `updated_at`（RFC3339）——字段名避开 B1 的写形态命名黑名单（Create* / Update*，故不用 CreatedAt / UpdatedAt）。
type NoteSpec struct {
	Rel         string
	ID          model.NoteID
	SourceID    model.SourceID
	Title       string
	Date        model.Date
	Stamp       model.Stamp
	Tags        []string
	Sections    []SectionAppend
	OutputCards []string // 「产出知识卡」列表项文本，如 `k-20260901-x（新建）`
	Inbox       InboxSpec
}

// NoteOutcome 是 write_note 的显式结果：笔记本体 + 是否复用 + 收件区条目移出情况。
// InboxSkip 非 nil 即「必须上报的跳过」，调用方不得忽略（§9 B3 同源口径）。
type NoteOutcome struct {
	Note      Result
	Reused    bool
	Inbox     Result
	InboxSkip *SkipError
}

// ApplyNote 落盘一篇材料笔记，并移出对应收件区条目。
func (s *Store) ApplyNote(spec NoteSpec) (NoteOutcome, error) {
	if spec.Rel == "" {
		return NoteOutcome{}, ErrNoteRelRequired
	}
	out := NoteOutcome{}

	if f, err := s.Read(spec.Rel); err == nil {
		out.Reused = true
		out.Note = Result{Path: spec.Rel, Hash: f.Hash,
			Detail: "目标笔记已存在：默认复用、不重写（EG-NOTE-05；重新加工只由用户显式发起）"}
	} else {
		content, err := noteContent(spec)
		if err != nil {
			return out, err
		}
		res, err := s.CreateFile(spec.Rel, mdfile.KindNote, content)
		out.Note = res
		if err != nil {
			return out, err
		}
	}

	inbox, skip := s.detachEntry(spec.Inbox, spec.SourceID)
	out.Inbox, out.InboxSkip = inbox, skip
	return out, nil
}

// noteContent 拼装新建笔记的完整字节。
func noteContent(spec NoteSpec) ([]byte, error) {
	sections, err := sectionMap(mdfile.KindNote, spec.Sections)
	if err != nil {
		return nil, err
	}
	if len(spec.OutputCards) > 0 {
		var list []byte
		for _, item := range spec.OutputCards {
			list = append(list, "- "...)
			list = append(list, item...)
			list = append(list, '\n')
		}
		sections[mdfile.SecOutputCards] = append(sections[mdfile.SecOutputCards], list...)
	}

	var fm []byte
	for _, kv := range [][2]string{
		{"id", string(spec.ID)},
		{"source", string(spec.SourceID)},
		{"title", spec.Title},
		{"created_at", spec.Date.String()},
		{"updated_at", spec.Stamp.String()},
	} {
		if kv[1] == "" {
			continue
		}
		line, err := fmLine(kv[0], kv[1])
		if err != nil {
			return nil, err
		}
		fm = append(fm, line...)
	}
	tags, err := fmSeq("tags", spec.Tags)
	if err != nil {
		return nil, err
	}
	fm = append(fm, tags...)
	return document(mdfile.KindNote, fm, sections)
}

// OpenQuestionSpec 是一次 add_open_question 的落盘输入：向笔记「存疑与待验证」**追加**
// 一整块。存疑没有独立 ID、没有独立文件、笔记 frontmatter 也没有状态字段（EG-KNW-03）。
type OpenQuestionSpec struct {
	Rel          string
	ExpectedHash string
	Stamp        model.Stamp
	Question     []byte // 逐字追加，必须以 \n 结束
}

// ApplyOpenQuestion 向材料笔记「存疑与待验证」尾部追加一条存疑。
func (s *Store) ApplyOpenQuestion(spec OpenQuestionSpec) (Result, error) {
	return s.sectionEdit(spec.Rel, spec.ExpectedHash, mdfile.KindNote, spec.Stamp,
		[]SectionAppend{{Section: mdfile.SecOpenQuest, Payload: spec.Question}})
}

// sectionEdit 是「对已有产物追加若干分区 + 尽力刷新 updated_at」的共用实现。
func (s *Store) sectionEdit(rel, expectedHash string, kind mdfile.Kind,
	stamp model.Stamp, sections []SectionAppend) (Result, error) {
	if len(sections) == 0 {
		return Result{Path: rel}, fmt.Errorf("%w：无分区载荷", ErrSectionNotWritable)
	}
	f, err := s.Read(rel)
	if err != nil {
		return Result{Path: rel}, err
	}
	hash := expectedHash
	if hash == "" {
		hash = f.Hash
	}
	keys, err := updatedAtAppendIfMissing(f.Bytes, stamp)
	if err != nil {
		return Result{Path: rel}, err
	}
	// 既有键交给 Edit.Stamp 在同一次守卫写里整行刷新；缺键才走 FMKeys 追加。
	return s.WriteGuarded(rel, hash, Edit{Kind: kind, Sections: sections,
		FMKeys: keys, Stamp: stamp})
}

// updatedAtAppendIfMissing 只负责**缺键补齐**这一半：产物没有 `updated_at` 时给出一条
// 追加意图，让它从此有这个键。
//
// 「键已存在则刷新」那一半**不在这里**——它由 Edit.Stamp 在同一次守卫写的最后一步
// 整行覆盖（唯一实现见 updated_at.go）。这样切分的理由：
//
//   - 授权合同 §2.1 矩阵**第 8 行**要求 `updated_at` 在**实际写入时更新**，两条路径无差别。
//     旧实现在这里对既有键只出一条 warning 就放过（「S1 只追加不改写既有键」），
//     结果是「改了内容但时间戳没动」→ `eg unreviewed`（判据 `updated_at > reviewed_at`）
//     对「过目后又被改写」的产物**静默**漏报（I-…-009）。B1 的「只追加」约束的是
//     **知识内容**，不是这个由 CLI 维护的元数据时间戳，故此处不再自我豁免。
//   - 缺键与既有键的字节机制本就不同（追加到 frontmatter 末尾 vs 整行区间覆盖），
//     分成两处后各自都只有一条路径，不存在「先追加再覆盖」的双写。
func updatedAtAppendIfMissing(raw []byte, stamp model.Stamp) ([]FMKeyAppend, error) {
	if stamp.IsZero() {
		return nil, nil
	}
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return nil, err
	}
	exists, err := doc.HasFMKey("updated_at")
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, nil
	}
	value, err := quoted(stamp.String())
	if err != nil {
		return nil, err
	}
	return []FMKeyAppend{{Key: "updated_at", Value: value}}, nil
}

// detachEntry 把收件区里 source_id 对应的条目移出（区间剪除，条目之外字节逐字不动）。
//
// 返回 (Result, *SkipError)：SkipError 非 nil 表示「条目未移出，必须上报」——
// 文件自 eg context 以来被改动（B3）、收件区不可解析、剪除结果未通过自检都归此类；
// 条目本就不在收件区视为已移出（幂等），不算跳过。
func (s *Store) detachEntry(spec InboxSpec, id model.SourceID) (Result, *SkipError) {
	if spec.Detached || id == "" {
		return Result{}, nil
	}
	rel := spec.Rel
	if rel == "" {
		rel = UnprocessedFile
	}
	res := Result{Path: rel}
	f, err := s.Read(rel)
	if err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: "收件区不可读，条目未移出：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	res.Hash = f.Hash
	if spec.ExpectedHash != "" && spec.ExpectedHash != f.Hash {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: fmt.Sprintf("收件区自读取以来已变化（期望 %s，磁盘 %s）：条目未移出，笔记已照常写入",
				spec.ExpectedHash, f.Hash)}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	u, err := mdfile.ParseUnprocessed(f.Bytes)
	if err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: "收件区不可解析，条目未移出：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	out, err := u.Cut(id)
	if err != nil {
		if errors.Is(err, mdfile.ErrEntryNotFound) {
			res.Detail = fmt.Sprintf("收件区已无 %s 条目：视为已移出（幂等）", id)
			return res, nil
		}
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: "条目未移出：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	abs, err := s.Abs(rel)
	if err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged, Detail: err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	if err := s.persist(rel, abs, out, 0o644); err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: "条目未移出（原子替换失败，磁盘仍是旧版本）：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	res.Written = true
	res.Hash = ContentHash(out)
	res.Detail = fmt.Sprintf("收件区条目 %s 已移出", id)
	return res, nil
}
