package cli

// eg capture 的业务实现（合同 §1.3；技术方案 §7.5 最小收录合同）。
//
// **CLI 不做任何网络请求**：抓网页、解析 HTML、清洗正文全在 Agent 侧，
// 本命令只把「已经拿到的正文字节」确定性地落成原文文件 + 收件区条目。
//
// 判重（EG-SRC-01）：URL 规范化后精确匹配，其次标题精确匹配；命中任一即同一篇 →
// 复用已有原文、不产生第二份、正文不覆盖、`--reason` 追加到收录理由列表。
// 已有材料笔记默认不重复加工（返回 has_note + note_id 并提示跳过），仅 --reprocess 放行。
//
// 一次 capture = 一次 commit（verb=capture）；重复执行的结果是幂等的——至多一份原文、
// 至多一条未处理条目，干净工作区下不产生空 commit。
//
// 措辞登记（T-…-026）：本段原文用「重复执行收敛<全角冒号>」描述幂等性，与逐卡收敛记录
// 无关，但会被「唯一渲染实现」的 grep 反证误判，故改写为「重复执行的结果是幂等的」。

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"evergreen/internal/git"
	"evergreen/internal/model"
	"evergreen/internal/store"
)

// MinBodySize 是正文（去首尾空白后）的最小字节数：低于此值视为「正文为空或过短」，
// 判 error 退 2 零写入（§7.5）。阈值刻意取小：eg 不判断正文质量，只拦截明显的空投喂。
const MinBodySize = 8

// TruncationHintSize 是「正文疑似截断」的 warning 阈值（字节）。只出 warning，收录照常完成。
const TruncationHintSize = 200

// TitleMaxLen 是「标题异常」的 warning 阈值（字节）。
const TitleMaxLen = 200

// runCapture 实现 eg capture。
func (r *Root) runCapture(inv *Invocation) (*Result, error) {
	root := inv.VaultRoot
	body, err := r.captureBody(inv)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) < MinBodySize {
		return nil, &ValidationError{
			Msg: fmt.Sprintf("eg capture 的正文为空或过短（去首尾空白后 %d 字节 < %d）：零写入",
				len(bytes.TrimSpace(body)), MinBodySize),
			Diags: []Diagnostic{{
				Level: LevelError, Path: "--body-stdin|--body-file", OpIndex: NonOpDiagnostic,
				Message: "正文为空或过短：eg 不抓取网页，正文字节必须由 Agent 传入",
			}},
		}
	}
	stamp, err := captureStamp(inv, r.now())
	if err != nil {
		return nil, err
	}

	rawURL, title, reason := inv.String("url"), inv.String("title"), inv.String("reason")
	domain, fallback := captureDomain(inv)
	res := &Result{}
	warn := func(path, msg string) {
		res.Warnings = append(res.Warnings, Diagnostic{
			Level: LevelWarning, Path: path, OpIndex: NonOpDiagnostic, Message: msg,
		})
	}
	if fallback {
		warn(ConfigFileName, fmt.Sprintf(
			"未给 --domain：按 %s 的 default_domain 落位 %s（default_domain_fallback）",
			ConfigFileName, domain))
	}
	if !inv.Config.HasDomain(domain) {
		warn("--domain", fmt.Sprintf("领域 %s 未登记在 %s 的 domains 里：条目照常登记",
			domain, ConfigFileName))
	}
	for _, msg := range titleWarnings(title, rawURL) {
		warn("--title", msg)
	}
	if n := len(bytes.TrimSpace(body)); n < TruncationHintSize {
		warn("--body-stdin|--body-file", fmt.Sprintf(
			"正文仅 %d 字节（< %d）：疑似截断，收录照常完成", n, TruncationHintSize))
	}

	st := store.New(root)
	sources, err := st.ScanSources()
	if err != nil {
		return nil, err
	}
	hit, deduped := store.FindDuplicate(sources, rawURL, title)

	var sourceID model.SourceID
	var rel string
	if deduped {
		sourceID, rel = hit.Source.ID, hit.Source.Rel
		warn("sources/", fmt.Sprintf("判重命中（%s 键）：复用已有原文 %s，正文不覆盖、不产生第二份",
			hit.Key, sourceID))
	} else {
		sourceID = model.NewSourceID(model.NewDate(stamp.Time()), captureSlugSeed(title, rawURL))
		rel = store.SourceRel(string(sourceID))
	}

	note, hasNote, err := st.NoteOf(sourceID)
	if err != nil {
		return nil, err
	}
	reprocess := inv.Set("reprocess")
	if hasNote && !reprocess {
		warn(note.Rel, fmt.Sprintf(
			"原文 %s 已有材料笔记 %s：默认不重复加工，本次跳过加工（要重新加工请显式加 --reprocess）",
			sourceID, note.ID))
	}

	entry := store.InboxEntry{
		Title:        title,
		Stamp:        stamp,
		Reason:       reason,
		TargetDomain: model.Domain(domain),
		// 已有笔记且未显式 --reprocess 时不再把条目放回队列：收件区是「待加工」队列，
		// 笔记已存在说明该篇已加工完（EG-SRC-02 的条目移出由 write_note 完成）。
		Detached: hasNote && !reprocess,
	}

	var inboxSkip *store.SkipError
	if deduped {
		reasonRes, err := st.ApplySourceReason(rel, "", reason)
		if err != nil {
			if skip, ok := store.AsSkip(err); ok {
				return nil, skipToPartial(res, skip)
			}
			return nil, err
		}
		if reasonRes.Written {
			warn(rel, "收录理由已追加到该原文的 reasons 列表（正文字节不变）")
		}
		if !entry.Detached {
			inboxRes, skip := st.AttachEntry(entry, sourceID)
			inboxSkip = skip
			_ = inboxRes
		}
	} else {
		out, err := st.ApplySource(store.SourceSpec{
			Rel:     rel,
			ID:      sourceID,
			URL:     rawURL,
			Title:   title,
			Stamp:   stamp,
			Tags:    captureTags(inv),
			Reasons: []string{reason},
			Body:    body,
			Inbox:   entry,
		})
		if err != nil {
			return nil, err
		}
		inboxSkip = out.InboxSkip
	}

	verb := model.VerbCapture
	if reprocess && hasNote {
		verb = model.VerbReprocess
	}
	info, err := git.New(root).Commit(git.Message{
		Verb:    string(verb),
		Domain:  domain,
		Subject: fmt.Sprintf("收录 %s", sourceID),
		Reason:  reason,
	})
	if err != nil {
		return nil, &CommitFailedError{Msg: "eg capture 的 Git 提交失败（磁盘保留现状）", Err: err}
	}
	res.Warnings = append(res.Warnings, gitWarnings(info)...)

	noteID := ""
	if hasNote {
		noteID = string(note.ID)
	}
	res.Data = map[string]interface{}{
		"source_id": string(sourceID),
		"path":      rel,
		"deduped":   deduped,
		"has_note":  hasNote,
		"note_id":   noteID,
		"commit":    commitInfoData(info),
		"warnings":  warningMessages(res.Warnings),
	}
	res.Summary = append(res.Summary, fmt.Sprintf("原文：%s（%s）", sourceID, rel))
	if deduped {
		res.Summary = append(res.Summary, "判重命中：复用已有原文，未产生第二份")
	}
	if info.Created {
		res.Summary = append(res.Summary, "commit："+info.Subject)
	} else {
		res.Summary = append(res.Summary, "无改动，未产生 commit")
	}
	if inboxSkip != nil {
		res.Data["warnings"] = append(warningMessages(res.Warnings), inboxSkip.Error())
		return res, &PartialWriteError{
			Msg: "收件区条目未登记，其余写入已保留并提交",
			Diags: []Diagnostic{{
				Level: LevelError, Path: inboxSkip.Path, OpIndex: NonOpDiagnostic,
				Code: store.CauseFor(inboxSkip.Reason), Message: inboxSkip.Detail,
			}},
		}
	}
	return res, nil
}

// skipToPartial 把 store 的「必须跳过」信号如实转成退 3（调用方不得吞掉，§9 B3）。
func skipToPartial(res *Result, skip *store.SkipError) error {
	_ = res
	return &PartialWriteError{
		Msg: "写入被跳过：" + skip.Detail,
		Diags: []Diagnostic{{
			Level: LevelError, Path: skip.Path, OpIndex: NonOpDiagnostic,
			Code: store.CauseFor(skip.Reason), Message: skip.Detail,
		}},
	}
}

// captureBody 读取正文字节：--body-stdin 走 Root.In（默认 os.Stdin），--body-file 走磁盘。
// 正文只用 []byte 传递，不做 rune 迭代重建、不做任何规范化。
func (r *Root) captureBody(inv *Invocation) ([]byte, error) {
	if file := inv.String("body-file"); file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, &UsageError{Msg: fmt.Sprintf("--body-file 不可读：%v", err)}
		}
		return raw, nil
	}
	in := r.In
	if in == nil {
		in = os.Stdin
	}
	raw, err := io.ReadAll(in)
	if err != nil {
		return nil, &UsageError{Msg: "读取 stdin 正文失败：" + err.Error()}
	}
	return raw, nil
}

// captureStamp 解析 --captured-at（带时区 RFC3339），缺省取本机时间。
func captureStamp(inv *Invocation, now time.Time) (model.Stamp, error) {
	raw := inv.String("captured-at")
	if raw == "" {
		return model.NewStamp(now), nil
	}
	s, err := model.ParseStamp(raw)
	if err != nil {
		return model.Stamp{}, &UsageError{Msg: "--captured-at 非法：" + err.Error()}
	}
	return s, nil
}

// captureDomain 决定目标领域：--domain 优先，否则回退 default_domain（并如实上报）。
func captureDomain(inv *Invocation) (string, bool) {
	if d := inv.String("domain"); d != "" {
		return d, false
	}
	return inv.Config.DefaultDomain, true
}

// captureTags 取 --tag（可重复）。
func captureTags(inv *Invocation) []string {
	f := inv.Flags.Lookup("tag")
	if f == nil {
		return nil
	}
	list, ok := f.Value.(*stringList)
	if !ok {
		return nil
	}
	return []string(*list)
}

// captureSlugSeed 决定 ID 里 slug 的取值来源：优先标题，标题缺失时退化到 URL。
func captureSlugSeed(title, rawURL string) string {
	if title != "" {
		return title
	}
	return store.NormalizeURL(rawURL)
}

// titleWarnings 汇报「标题异常」（缺失 / 过长 / 与 URL 相同）——只出 warning，收录照常完成。
func titleWarnings(title, rawURL string) []string {
	var out []string
	switch {
	case title == "":
		out = append(out, "缺 --title：仅按 URL 判重，ID 的 slug 退化自 URL")
	case len(title) > TitleMaxLen:
		out = append(out, fmt.Sprintf("标题长 %d 字节（> %d）：疑似正文误入标题", len(title), TitleMaxLen))
	case rawURL != "" && title == rawURL:
		out = append(out, "标题与 URL 完全相同：疑似标题抓取失败")
	}
	return out
}

// warningMessages 把诊断压成字符串列表，作为 --json 的 data.warnings（合同 §7.5 七键之一）。
func warningMessages(diags []Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Message)
	}
	return out
}
