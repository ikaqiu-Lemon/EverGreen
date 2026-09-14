package git

// `git log` 的**只读**参数面扩展：`--follow` + `--name-status` 的 rename 记录采样
// （M4 · T-…-054，对账合同 §8 的条件② 需要「该文件是否发生过跨领域目录 rename、
// 那次 rename 的 commit 是不是 `eg` 产生的」这两条 Git 历史事实）。
//
// # 本文件是**纯只读**面（结构上做不到写，不靠自律）
//
//   - 只有 `log` 一个子命令，且只带 `--follow` / `--name-status` / `--pretty=format:` 三个
//     **只读**参数：不含任何写口 / 撤销类子命令（提交、暂存、推送、重置、签出一个都没有），
//     因此本文件里不存在、也无法出现 Git 写方法（task Acceptance 的
//     `func .*\b(Commit|Add|Push|Reset|Checkout)\(` 恒 0 命中）。
//   - 不写盘、不改工作区、不动 index：`git log` 本身不触碰工作区。
//   - 解析部分（parseFollowNameStatus / SubjectVerb）是**纯字符串运算**，零 IO。
//
// # 为什么放在 `internal/git` 而不是 `internal/reconcile`
//
// 对账包（`internal/reconcile`）按合同 §1.1 **零 `os/exec`**：它不持有 `*Repo`、
// 不起子进程，Git 事实一律以只读快照的形态从 `Input` 进来。起子进程这件事只许发生在
// 本包内 —— 于是「采样」与「判定」彻底分离：R5 拿到的是结构化的 rename 事实，
// 而不是一个可以拿来跑任意 git 命令的句柄。

import "strings"

// RenameRecord 是一条 rename 落盘事实（`git log --name-status` 输出里的 `R` 记录）。
//
// 四个字段全是**可复算的字符串事实**：无句柄、无函数字段，因此这条记录传给谁都不可能
// 变成一次写操作。
type RenameRecord struct {
	// OldPath / NewPath 是 rename 前后的仓库相对路径（vault 即仓库根，故等同 vault 相对路径）。
	OldPath string
	NewPath string
	// Subject 是该 rename 所在 commit 的主题行逐字原值（`<verb>(<domain>): <subject>`）。
	Subject string
	// Verb 是主题行动词位的逐字原值（读不出动词位即空串）。
	//
	// **本文件不判断这个 verb 是不是 `eg` 的**：已知 verb 集合的唯一真源是
	// `internal/model` 的 `KnownVerbs()`，归属判定在 `internal/reconcile` 侧完成
	// （R2 已落地的同一个 helper），本包只如实把字面量带出来。
	Verb string
}

// `git log` 只读参数面的字面量（写成常量便于逐字 grep 复算，也避免拼字符串时手误）。
const (
	// logFollowFlag 让历史跨 rename 继续跟随同一个文件（合同 §8 条件② 逐字口径）。
	logFollowFlag = "--follow"
	// logNameStatusFlag 让每次提交带出「状态字母 + 路径」行，rename 记录即 `R<相似度>`。
	logNameStatusFlag = "--name-status"
	// logSubjectMarker 是主题行的哨兵前缀：`--name-status` 的输出与主题行混在同一个流里，
	// 用一个正常提交主题不可能出现的控制字符（US, 0x1f）把两者分开，解析因此不依赖空行。
	logSubjectMarker = "\x1f"
	// renameStatusPrefix 是 rename 记录的状态字母（`R100` / `R087` …）。
	renameStatusPrefix = "R"
)

// FollowRenames 返回该路径在 Git 历史上的全部 rename 记录（新 → 旧，Git 自身的日志序）。
//
// 口径逐字 = `git log --follow --name-status --pretty=format:<哨兵>%s -- <路径>`
// （合同 §8 条件② 的事实来源）。**只读**：不改工作区、不动 index、不产生提交。
//
// 两条诚实性口径：
//   - 空路径 → 空集合（没有可采样的目标，不代表「没有 rename」）；
//   - `git log` 读不动（空仓、路径无历史、仓库不可读）→ 空集合 + nil error，
//     与既有 `LogSubjects` 的先例同口径：**采不到事实就是没采到**，
//     绝不把「读失败」当成「发生过 rename」。
func (r *Repo) FollowRenames(rel string) ([]RenameRecord, error) {
	p := strings.TrimSpace(rel)
	if p == "" {
		return nil, nil
	}
	stdout, _, err := r.run(r.root, "log", logFollowFlag, logNameStatusFlag,
		"--pretty=format:"+logSubjectMarker+"%s", "--", p)
	if err != nil {
		return nil, nil
	}
	return parseFollowNameStatus(stdout), nil
}

// parseFollowNameStatus 把 `--pretty=format:<哨兵>%s` + `--name-status` 的输出折成记录集合。
//
// 纯字符串运算（零 IO、零子进程）：
//   - 以哨兵开头的行 = 一次提交的主题行，后续记录行都归属这个主题；
//   - `R<相似度>\t<旧路径>\t<新路径>` = 一条 rename 记录（三段齐备才收）；
//   - 其余状态行（`A` / `M` / `D` / `C` …）与空行一律忽略。
func parseFollowNameStatus(out []byte) []RenameRecord {
	var (
		res     []RenameRecord
		subject string
	)
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, logSubjectMarker) {
			subject = strings.TrimSpace(strings.TrimPrefix(line, logSubjectMarker))
			continue
		}
		if !strings.HasPrefix(line, renameStatusPrefix) {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}
		oldPath, newPath := strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		if oldPath == "" || newPath == "" {
			continue
		}
		res = append(res, RenameRecord{
			OldPath: oldPath, NewPath: newPath,
			Subject: subject, Verb: SubjectVerb(subject),
		})
	}
	return res
}

// SubjectVerb 取提交主题行 `<verb>(<domain>): <subject>` 的**动词位**逐字原值。
//
// 纯字符串运算：截到第一个 `(` 或 `:`（取更靠前者）为止；截出来的片段含空白、
// 或整行没有这两个分隔符 → 返回空串（= 读不出动词位，不猜）。
// **不做归属判定、不做归一化**：`eg` 已知 verb 集合的唯一真源在 `internal/model`。
func SubjectVerb(subject string) string {
	s := strings.TrimSpace(subject)
	if s == "" {
		return ""
	}
	cut := -1
	for i, ch := range s {
		if ch == '(' || ch == ':' {
			cut = i
			break
		}
	}
	if cut <= 0 {
		return ""
	}
	verb := s[:cut]
	if strings.ContainsAny(verb, " \t") {
		return ""
	}
	return verb
}
