package store

// 收件区 `unprocessed.md` 的条目登记（技术方案 §4.4；T-…-009）。
//
// 落位（冻结合同 F1）：队列文件在 **vault 根**，与 `sources/` **并列**——
// `sources/` 目录下只放每条来源的独立原文文件，不得出现第二个队列文件。
//
// 写法与 note.go 的「条目移出」同源：只做 mdfile 的字节区间插入 + tmp/fsync/rename 原子替换，
// 条目之外的字节（标题、说明段落、空行、其他条目）逐字不动；插入结果先过 Parse→Render
// 字节自检，不等即拒写并如实上报。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// InboxEntry 描述本次要登记进收件区的条目（§4.4：一个顶层列表项 = 一个条目）。
type InboxEntry struct {
	Rel          string // 空 → UnprocessedFile
	Title        string
	Stamp        model.Stamp
	Reason       string
	TargetDomain model.Domain
	ExpectedHash string // eg context 读到的 content_hash（B3；空 → 不比对）
	Detached     bool   // true → 本次完全不动收件区
}

// AttachEntry 把一条待加工条目登记进收件区（在最后一个条目之后插入，其余字节逐字不动）。
//
// 返回 (Result, *SkipError)：SkipError 非 nil 表示「条目未登记，必须上报」——
// 收件区不可读 / 不可解析、B3 命中、插入结果未通过自检都归此类；
// 同 source_id 的条目已在队列中视为已登记（幂等），不算跳过。
func (s *Store) AttachEntry(spec InboxEntry, id model.SourceID) (Result, *SkipError) {
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
			Detail: "收件区不可读，条目未登记：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	res.Hash = f.Hash
	if spec.ExpectedHash != "" && spec.ExpectedHash != f.Hash {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: fmt.Sprintf("收件区自读取以来已变化（期望 %s，磁盘 %s）：条目未登记",
				spec.ExpectedHash, f.Hash)}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	u, err := mdfile.ParseUnprocessed(f.Bytes)
	if err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: "收件区不可解析，条目未登记：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	if _, ok := u.Find(id); ok {
		res.Detail = fmt.Sprintf("收件区已有 %s 条目：不产生第二条（幂等）", id)
		return res, nil
	}
	item, err := inboxItem(id, spec)
	if err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged, Detail: "条目未登记：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	out, err := u.Append(item)
	if err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged, Detail: "条目未登记：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	if err := mdfile.SelfCheck(out); err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: ErrSelfCheckFailed.Error() + "：" + err.Error()}
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
			Detail: "条目未登记（原子替换失败，磁盘仍是旧版本）：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	res.Written = true
	res.Hash = ContentHash(out)
	res.Detail = fmt.Sprintf("收件区条目 %s 已登记", id)
	return res, nil
}

// inboxItem 按模板拼一个收件区条目（§4.4 五个键，顺序固定）。
func inboxItem(id model.SourceID, spec InboxEntry) ([]byte, error) {
	var out []byte
	for i, kv := range [][2]string{
		{"source_id", string(id)},
		{"title", spec.Title},
		{"saved_at", spec.Stamp.String()},
		{"reason", spec.Reason},
		{"target_domain", string(spec.TargetDomain)},
	} {
		if kv[1] == "" && kv[0] == "target_domain" {
			continue
		}
		value, err := quoted(kv[1])
		if err != nil {
			return nil, fmt.Errorf("收件区条目 %s：%w", kv[0], err)
		}
		if i == 0 {
			out = append(out, '-', ' ')
		} else {
			out = append(out, seqIndent...)
		}
		out = append(out, kv[0]...)
		out = append(out, ':', ' ')
		out = append(out, value...)
		out = append(out, '\n')
	}
	return out, nil
}
