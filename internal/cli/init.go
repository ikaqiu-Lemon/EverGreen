package cli

// eg init 的业务实现（合同 §1.1；vault 骨架 = 冻结合同 F1 的 [S1] 列）。
//
// 只生成 S1 需要存在的七项：.git/、.gitignore（内容 .index/）、evergreen.yml、
// SKILL.md、unprocessed.md、sources/、domains/<d>/{notes,knowledge}/。
// **不创建** reviews/（S2）、proposals/（S2）、.index/（S4）——名字与位置已定死，S1 不需要存在。
// 幂等：已存在的文件一个字节都不改；干净工作区下不产生空 commit。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"evergreen/internal/git"
	"evergreen/internal/mdfile"
	"evergreen/internal/store"
	"evergreen/skill"
)

// vault 骨架里的固定文件名与目录名（F1 的 [S1] 列）。
const (
	UnprocessedFileName = "unprocessed.md"
	SourcesDirName      = "sources"
	DomainsDirName      = "domains"
	NotesDirName        = "notes"
	KnowledgeDirName    = "knowledge"
)

// NotCreatedInS1 是 S1 **不创建**的目录（名字与位置已定死，留给 S2 / S4）。
func NotCreatedInS1() []string { return []string{"reviews", "proposals", ".index"} }

// unprocessedTemplate 是收件区初始内容：一个顶级列表项 = 一条待处理材料，键是 source_id。
const unprocessedTemplate = `# 收件区（unprocessed）

<!-- 一个顶级列表项 = 一条待处理材料；键是 source_id。由 eg capture 追加，勿手工重排。 -->
`

// runInit 实现 eg init。
func (r *Root) runInit(inv *Invocation) (*Result, error) {
	root, err := r.vaultTarget(inv)
	if err != nil {
		return nil, err
	}
	domains, err := parseDomains(inv.String("domain"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf("无法创建 vault 目录 %s：%v", root, err)}
	}
	repo, err := git.Init(root)
	if err != nil {
		return nil, &CommitFailedError{Msg: "git init 失败", Err: err}
	}

	st := store.New(root)
	var created, existed []string
	def := ""
	if len(domains) > 0 {
		def = domains[0]
	}
	files := []struct {
		rel     string
		content []byte
	}{
		{ConfigFileName, renderConfig(domains, def)},
		{skill.FileName, skill.Content()},
		{UnprocessedFileName, []byte(unprocessedTemplate)},
	}
	for _, f := range files {
		ok, err := st.Exists(f.rel)
		if err != nil {
			return nil, err
		}
		if ok {
			existed = append(existed, f.rel)
			continue
		}
		kind := mdfile.Kind("")
		if _, err := st.CreateFile(f.rel, kind, f.content); err != nil {
			return nil, err
		}
		created = append(created, f.rel)
	}

	dirs := []string{SourcesDirName, DomainsDirName}
	for _, d := range domains {
		dirs = append(dirs, domainDirs(d)...)
	}
	for _, d := range dirs {
		abs := filepath.Join(root, d)
		if _, err := os.Stat(abs); err == nil {
			continue
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return nil, err
		}
		created = append(created, d+"/")
	}

	fallbackIdentity, err := repo.EnsureIdentity()
	if err != nil {
		return nil, &CommitFailedError{Msg: "无法准备 Git 提交身份", Err: err}
	}

	commitDomain := def
	if commitDomain == "" {
		commitDomain = "vault"
	}
	info, err := repo.Commit(git.Message{
		Verb:    "init",
		Domain:  commitDomain,
		Subject: "初始化 vault 骨架",
		Reason:  "eg init：建立 vault 骨架与工程配置",
	})
	if err != nil {
		return nil, &CommitFailedError{Msg: "eg init 的 Git 提交失败（磁盘保留现状）", Err: err}
	}

	res := &Result{Data: map[string]interface{}{
		"vault":          root,
		"created":        created,
		"already_exists": existed,
		"domains":        domains,
		"default_domain": def,
		"commit":         commitInfoData(info),
	}}
	res.Warnings = append(res.Warnings, gitWarnings(info)...)
	if fallbackIdentity {
		res.Warnings = append(res.Warnings, Diagnostic{
			Level:   LevelWarning,
			Path:    "git.config",
			OpIndex: NonOpDiagnostic,
			Message: fmt.Sprintf("本机未配置 Git 提交身份，已为该 vault 写入本地兜底身份 %s <%s>",
				git.FallbackUserName, git.FallbackUserEmail),
		})
	}
	res.Summary = append(res.Summary, fmt.Sprintf("vault：%s", root))
	res.Summary = append(res.Summary, fmt.Sprintf("新建 %d 项，已存在 %d 项", len(created), len(existed)))
	if info.Created {
		res.Summary = append(res.Summary, "commit："+info.Subject)
	} else {
		res.Summary = append(res.Summary, "无改动，未产生 commit")
	}
	if def == "" {
		res.Warnings = append(res.Warnings, Diagnostic{
			Level:   LevelWarning,
			Path:    ConfigFileName,
			OpIndex: NonOpDiagnostic,
			Message: "未配置 default_domain：除 init / config 外的命令会提示配置并退 1（CLI 绝不自选默认领域）",
		})
	}
	return res, nil
}

// vaultTarget 决定要初始化 / 操作的 vault 根：--vault 优先，否则取 cwd。
func (r *Root) vaultTarget(inv *Invocation) (string, error) {
	target := inv.VaultFlag
	if target == "" {
		wd, err := r.Getwd()
		if err != nil {
			return "", &UsageError{Msg: "无法确定当前目录：" + err.Error()}
		}
		target = wd
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", &UsageError{Msg: fmt.Sprintf("无法解析路径 %q：%v", target, err)}
	}
	return abs, nil
}

// domainDirs 返回一个领域需要存在的两个目录（S1 不建 reviews/）。
func domainDirs(d string) []string {
	return []string{
		filepath.ToSlash(filepath.Join(DomainsDirName, d, NotesDirName)),
		filepath.ToSlash(filepath.Join(DomainsDirName, d, KnowledgeDirName)),
	}
}

// parseDomains 解析 --domain（可重复）/ 逗号分隔值，去重并保序。
func parseDomains(raw string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		d := strings.TrimSpace(part)
		if d == "" {
			continue
		}
		if err := validDomain(d); err != nil {
			return nil, err
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out, nil
}

// validDomain 限定领域名形态：领域名会成为目录名，也会进 commit 主题。
func validDomain(d string) error {
	if d == "" {
		return &UsageError{Msg: "领域名不能为空"}
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
			if i == 0 {
				return &UsageError{Msg: fmt.Sprintf("领域名 %q 不能以 %q 开头", d, string(c))}
			}
		default:
			return &UsageError{Msg: fmt.Sprintf(
				"领域名 %q 含非法字符 %q：只允许小写字母、数字与 - _ .", d, string(c))}
		}
	}
	return nil
}

// commitInfoData 把 commit 回执整成 data 载荷（报告口径由 T-…-016 统一，此处只放事实）。
func commitInfoData(info git.CommitInfo) map[string]interface{} {
	return map[string]interface{}{
		"created": info.Created,
		"sha":     info.SHA,
		"subject": info.Subject,
		"files":   info.Files,
	}
}

// gitWarnings 把 git 侧 warning 转成诊断（提交信息缺项等，如实上报）。
func gitWarnings(info git.CommitInfo) []Diagnostic {
	var out []Diagnostic
	for _, w := range info.Warnings {
		out = append(out, Diagnostic{
			Level:   LevelWarning,
			Path:    "git.commit",
			OpIndex: NonOpDiagnostic,
			Message: w,
		})
	}
	return out
}
