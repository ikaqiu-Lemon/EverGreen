package store

// vault 目录布局（冻结合同 F1 的 [S1] 列）与产物落位路径。
//
// 领域**由目录唯一决定**（EG-DOM-01）：产物 frontmatter 里没有 domain 字段，
// 所属领域只看它在哪个 `domains/<d>/` 下。落位函数是 id → 相对路径的**唯一**拼装点，
// 与 `eg init` 生成的骨架逐字一致；解析既有产物一律走 ScanIDs（文件允许被改名 / 移动，F2）。

import "path"

// 骨架里的固定文件名与目录名。
const (
	// UnprocessedFile 是收件区（vault 根，键为 source_id）。
	UnprocessedFile = "unprocessed.md"
	// DirSources 是原文目录（vault 根，不属于任何领域）。
	DirSources = "sources"
	// DirDomains 是领域根目录。
	DirDomains = "domains"
	// DirNotes 是领域内的材料笔记目录。
	DirNotes = "notes"
	// DirKnowledge 是领域内的知识卡目录。
	DirKnowledge = "knowledge"
)

// SourceRel 是原文的落位路径：`sources/<s-id>.md`。
func SourceRel(id string) string { return path.Join(DirSources, id+".md") }

// NoteRel 是材料笔记的落位路径：`domains/<domain>/notes/<n-id>.md`。
func NoteRel(domain, id string) string {
	return path.Join(DirDomains, domain, DirNotes, id+".md")
}

// CardRel 是知识卡的落位路径：`domains/<domain>/knowledge/<k-id>.md`。
func CardRel(domain, id string) string {
	return path.Join(DirDomains, domain, DirKnowledge, id+".md")
}

// DomainOf 从 vault 内相对路径反推领域名；不在 `domains/<d>/` 下时返回空串
// （如 `sources/…`、`unprocessed.md`、`evergreen.yml` 都不属于任何领域）。
func DomainOf(rel string) string {
	parts := splitSlash(path.Clean(rel))
	if len(parts) < 2 || parts[0] != DirDomains {
		return ""
	}
	return parts[1]
}

func splitSlash(p string) []string {
	var out []string
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			out = append(out, p[start:i])
			start = i + 1
		}
	}
	return append(out, p[start:])
}
