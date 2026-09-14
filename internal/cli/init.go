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

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
	"github.com/ikaqiu-Lemon/EverGreen/skill"
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
//
// 注意 `.index` 这一项的**准确含义**（I-…-021 修复之后）：它说的是「**首次引导**
// 一次 `eg init` 不会创建 `.index/`」——`.index/` 里没有任何 S1 产物，索引本体
// （`eg.db`）恒由 `eg index build` 显式创建。而在**已是 vault 的幂等路径**上，
// `eg init` 与其它 A 类写命令一样要先取 `vault/.index/run.lock`（合同 §16.1），
// 因此该目录会作为 runtime reserved entry 出现——那是**锁与事务日志**的容器，
// 不是「S1 创建了索引」。两者的区别由 `eg.db` 是否存在直接可判。
func NotCreatedInS1() []string { return []string{"reviews", "proposals", ".index"} }

// unprocessedTemplate 是收件区初始内容：一个顶级列表项 = 一条待处理材料，键是 source_id。
const unprocessedTemplate = `# 收件区（unprocessed）

<!-- 一个顶级列表项 = 一条待处理材料；键是 source_id。由 eg capture 追加，勿手工重排。 -->
`

// runInit 实现 eg init。
//
// # 两条路径，一条判据（I-…-021）
//
// `eg init` 会写权威文件并产生 commit，因此**在目标已经是 vault 时**它是合同 §16.1 的
// A 类命令：先取 `vault/.index/run.lock`（锁忙 ⇒ 退 5 + E16 + 零写入）、再过 S2 崩溃恢复
// 屏障（有未闭合事务 ⇒ 先回滚并产 W26），之后才允许写骨架并提交。否则 `git add -A` 会把
// 上一次崩溃留下的半应用字节一并提交进权威历史 —— 那正是 I-…-021 的 P0 后果面。
//
// **首次引导是唯一豁免**：目标目录还不是 vault（无 `evergreen.yml`、无 `.index/`）时不取锁。
// 理由有三，都是可检查的事实而不是方便：
//   - 此刻库里不可能有未闭合事务（`.index/txn` 尚不存在），恢复屏障无对象可恢复；
//   - 此刻不存在第二个写者可与之竞争的权威文件（没有任何权威内容可被并发改坏）；
//   - F1 的 [S1] 列逐字要求首次 `init` **不创建** `.index/`（`NotCreatedInS1`），
//     而取锁必然要建出这个目录 —— 两者只能择一，取锁在这一格没有任何可保护的东西。
//
// 幂等路径的写入集合是「只新建、不改既存字节」的骨架，因此**不发 intent、不开事务**：
// 崩溃后重跑 `eg init` 即收敛（缺哪项补哪项），没有前像可丢。S1/S2 的保护面在这里是
// 「不与别的写者同时提交」+「不在半应用态上提交」，这两条已由锁与恢复屏障提供。
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

	res := &Result{Data: map[string]interface{}{}}
	// —— S1 + S2：已是 vault 才取锁 + 过恢复屏障（首次引导为唯一豁免，见函数头）。——
	if vaultAlreadyExists(root) {
		sess, serr := r.enterTxnCriticalAt(inv, root, resultWarnSink{res}, txnCriticalOpts{
			ZeroWrite:     "本次零写入（不补建任何骨架项、无 commit）",
			ReleaseNotice: "本次骨架补建与提交不受影响",
		})
		if serr != nil {
			return nil, serr
		}
		defer sess.release()
	}
	return r.initSkeleton(inv, res, root, domains)
}

// vaultAlreadyExists 判定「目标已经是 vault」——即 `eg init` 这一次落在**幂等路径**上。
//
// 判据取两项事实的并集，都不依赖本次命令的参数：
//   - `evergreen.yml` 在盘（`configVault` 判定 vault 的同一口径）；
//   - `.index/` 在盘（可能只有 `run.lock` / `txn/`：说明这个库已经被 eg 写过，
//     哪怕 `evergreen.yml` 恰好被人删了，也仍可能存在未闭合事务需要先恢复）。
func vaultAlreadyExists(root string) bool {
	if _, err := os.Stat(filepath.Join(root, ConfigFileName)); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(root, txn.IndexDirName)); err == nil {
		return true
	}
	return false
}

// initSkeleton 是 init 的骨架生成与提交段（S3~S7）：一律在临界区内被调用。
func (r *Root) initSkeleton(inv *Invocation, res *Result, root string,
	domains []string) (*Result, error) {
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
	// 骨架文件是本次写入，其余改动即既有改动：采样早于 Add，如实披露（I-…-023）。
	// 首次 init 的库里通常什么都没有，这条恒不产出；幂等 init 才可能命中。
	noteExistingChangesInResult(res, repo, initWriteSetPaths(created))
	info, err := repo.Commit(git.Message{
		Verb:    "init",
		Domain:  commitDomain,
		Subject: "初始化 vault 骨架",
		Reason:  "eg init：建立 vault 骨架与工程配置",
	})
	if err != nil {
		return res, &CommitFailedError{Msg: "eg init 的 Git 提交失败（磁盘保留现状）", Err: err}
	}

	res.Data["vault"] = root
	res.Data["created"] = created
	res.Data["already_exists"] = existed
	res.Data["domains"] = domains
	res.Data["default_domain"] = def
	res.Data["commit"] = commitInfoData(info)
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

// initWriteSetPaths 从 init 的 created 清单里取**文件**路径（供既有改动采样对齐 git 口径）。
//
// created 同时收录目录（以 "/" 结尾）与文件；`git status` 只报文件，因此目录项必须滤掉 ——
// 留着它们只会让「本次写入」的集合永远对不上，把本次新建的骨架文件误报成既有改动。
func initWriteSetPaths(created []string) []string {
	out := make([]string, 0, len(created))
	for _, p := range created {
		if strings.HasSuffix(p, "/") {
			continue
		}
		out = append(out, p)
	}
	return out
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
