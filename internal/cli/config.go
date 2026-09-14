package cli

// eg config get|set 的业务实现（合同 §1.2）：evergreen.yml 的唯一读写口。
//
// 键的封闭集合是 model.ConfigKeys()（default_domain / domains），其它键退 1。
// 读走 loadConfigReadOnly（yaml.v3 只读）；写走**字节区间编辑**：定位既有行后替换
// 该行的值或插入一行，注释 / 键顺序 / 未知键 / 空行逐字保留，绝不用序列化器整文件重排。
//
// commit verb 消歧（出处：技术方案 §7.1 命令表 `eg config get|set` 行的「产生 commit」列）：
// 这里的提交动词取 model.VerbReconcile，它**不是** S3 的对账命令（S1 不注册该命令，
// `eg --help` 里也不出现），也**不是** §4.6 报告字段里那个同名布尔字段（S3 才计算）——
// 三者同名分属三层：提交动词 / 命令 / 报告字段。

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// KeyDefaultDomain / KeyDomains 是 S1 允许操作的两个配置键。
const (
	KeyDefaultDomain = "default_domain"
	KeyDomains       = "domains"
)

// configHeader 是 init 生成 evergreen.yml 时的说明注释（工程配置，不属于任何领域）。
const configHeader = "# Evergreen 工程配置（不是知识产物，不属于任何领域；领域由目录唯一决定）\n"

// renderConfig 按字节拼出初始 evergreen.yml：恰三个顶层键 version / domains / default_domain。
// 这里不是序列化器：内容是固定模板 + 已校验过形态的领域名，逐字节拼接。
func renderConfig(domains []string, def string) []byte {
	var b bytes.Buffer
	b.WriteString(configHeader)
	fmt.Fprintf(&b, "version: %d\n", model.ConfigVersion)
	if len(domains) == 0 {
		b.WriteString("domains: []\n")
	} else {
		b.WriteString("domains:\n")
		for _, d := range domains {
			b.WriteString("  - " + d + "\n")
		}
	}
	b.WriteString("default_domain: " + quoteScalar(def) + "\n")
	return b.Bytes()
}

// quoteScalar 只处理两种情形：空值写成 ”，其余（已校验的领域名）写成裸标量。
func quoteScalar(v string) string {
	if v == "" {
		return "''"
	}
	return v
}

// runConfig 实现 eg config get|set。
//
// get 是 C 类只读命令：不取锁、不过恢复屏障（合同 §16.1 C 类「零锁开销」）。
// set 是 **A 类写命令**：改 `evergreen.yml` 并产生 commit，因此必须走 S1~S8
// （见 configSetCritical 的说明与 I-…-021）。
func (r *Root) runConfig(inv *Invocation) (*Result, error) {
	root, err := r.configVault(inv)
	if err != nil {
		return nil, err
	}
	key := inv.Args[0]
	if inv.Sub == "get" {
		cfg, cerr := r.LoadConfig(root)
		if cerr != nil {
			return nil, &UsageError{Msg: fmt.Sprintf("%s 不可解析：%v", ConfigFileName, cerr)}
		}
		return configGet(cfg, key), nil
	}
	// **刻意不在这里读 evergreen.yml**：写路径的每一次业务读都必须晚于 S2 恢复屏障
	// （合同 §1 A-55）。锁外先读一遍再拿去写，就是 I-…-021 与 I-…-022 的同一类根因。
	return r.configSetCritical(inv, root, key, inv.Args[1])
}

// configVault 定位 vault：--vault 优先，否则从 cwd 向上找 evergreen.yml。
// config 不受 default_domain 守卫约束，但必须已有 vault（否则提示先 eg init）。
func (r *Root) configVault(inv *Invocation) (string, error) {
	if inv.VaultFlag != "" {
		abs, err := filepath.Abs(inv.VaultFlag)
		if err != nil {
			return "", &UsageError{Msg: fmt.Sprintf("无法解析路径 %q：%v", inv.VaultFlag, err)}
		}
		if _, err := os.Stat(filepath.Join(abs, ConfigFileName)); err != nil {
			return "", &UsageError{Msg: fmt.Sprintf(
				"%s 不是 vault（缺 %s）：先执行 eg init", abs, ConfigFileName)}
		}
		return abs, nil
	}
	wd, err := r.Getwd()
	if err != nil {
		return "", &UsageError{Msg: "无法确定当前目录：" + err.Error()}
	}
	root, err := r.FindVault(wd)
	if err != nil {
		return "", &UsageError{Msg: fmt.Sprintf(
			"未找到 vault（缺 %s）：%v；先执行 eg init 或用 --vault <path> 指定", ConfigFileName, err)}
	}
	return root, nil
}

// configGet 是只读路径：零文件变化、零 commit。未设置的 default_domain 输出空值并退 0。
func configGet(cfg model.Config, key string) *Result {
	res := &Result{Data: map[string]interface{}{"key": key}}
	switch key {
	case KeyDefaultDomain:
		res.Data["value"] = cfg.DefaultDomain
		res.Summary = append(res.Summary, cfg.DefaultDomain)
		if cfg.DefaultDomain == "" {
			res.Data["configured"] = false
		} else {
			res.Data["configured"] = true
		}
	case KeyDomains:
		domains := cfg.Domains
		if domains == nil {
			domains = []string{}
		}
		res.Data["value"] = domains
		res.Summary = append(res.Summary, strings.Join(domains, "\n"))
	}
	return res
}

// configSetCritical 是 `eg config set` 的**唯一**写入编排：A 类命令的 S0~S9
// （M6 原子性合同 §2 时序 / §16.1 A 类行；修复 I-…-021 P0）。
//
//	S0  参数校验                —— **锁外**，只看命令行给的值，不读盘（因此锁忙时
//	                              「值不能为空」这类用法错误仍退 1，不被锁语义盖成退 5）
//	S1  txn.Acquire(run.lock)   —— 临界区开始；锁忙 ⇒ 退 5 + E16 + 零写入零 commit
//	S2  txn.Recover             —— 临界区内第一件事；真实回滚产 W26
//	S3  锁内重读 evergreen.yml  —— **严禁**复用任何锁外快照：S2 可能刚把文件回滚到前像
//	S4  字节区间编辑            —— 纯内存预演，实盘零变化
//	S5  AllocateTxnID → intent  —— 仅当字节确有变化（零 write-set 不开事务）
//	S6  txn.Commit              —— 单文件也走原子提交：commit marker 在盘才算生效
//	S7  git add -A + commit     —— 严格晚于 commit marker、仍持同一把锁
//	S9  Release                 —— 报告渲染回到锁外
//
// 为什么连「一个 evergreen.yml」也要走事务：崩溃恢复（S2）与互斥（S1）的判据是
// 「这条命令是否改权威文件 / Git 历史」，而不是「改了几个文件」。I-…-021 的后果面正是
// 「不过恢复屏障 ⇒ `git add -A` 把上一次崩溃的半应用字节一并提交进权威历史」，
// 它与本命令改了几个字节完全无关。
//
// 三条边界：不建 `.index/` 之外的任何新目录形态（领域目录仍按 F1 口径建）；
// 不做领域删除 / 迁移（S1 口径不变）；不碰派生索引 —— `config set` 不写任何被索引对象，
// 写后索引同步（W22/W23/W24）留给写卡的命令，不在这里制造噪声。
func (r *Root) configSetCritical(inv *Invocation, root, key, value string) (*Result, error) {
	// —— S0：锁外参数校验。——
	newDomains, err := parseDomains(value)
	if err != nil {
		return nil, err
	}
	if len(newDomains) == 0 {
		return nil, &UsageError{Msg: fmt.Sprintf("eg config set %s 的值不能为空", key)}
	}
	if key == KeyDefaultDomain && len(newDomains) != 1 {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"eg config set %s 只接受一个领域，实际 %d 个", KeyDefaultDomain, len(newDomains))}
	}

	res := &Result{Data: map[string]interface{}{}}

	// —— S1 + S2：取锁 + 崩溃恢复屏障（与七条 plan 写命令共用同一底座）。——
	sess, serr := r.enterTxnCriticalAt(inv, root, resultWarnSink{res}, txnCriticalOpts{
		ZeroWrite: "本次零写入（" + ConfigFileName +
			" 一个字节都不改、不建领域目录、无 commit）",
		ReleaseNotice: "本次配置写入与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S3：锁内重读。S2 若真的回滚过文件，锁外的任何读都已过期。——
	cfg, cerr := r.LoadConfig(root)
	if cerr != nil {
		return res, &UsageError{Msg: fmt.Sprintf("%s 不可解析：%v", ConfigFileName, cerr)}
	}
	path := filepath.Join(root, ConfigFileName)
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		return res, rerr
	}

	// —— S4：字节区间编辑（纯内存）。注释 / 键顺序 / 未知键 / 空行逐字保留。——
	out := raw
	// domains 取并集追加，不删除既有领域（S1 不做领域迁移 / 重命名 / 删除）。
	var added []string
	for _, d := range newDomains {
		if cfg.HasDomain(d) {
			continue
		}
		out, err = appendDomain(out, d)
		if err != nil {
			return res, err
		}
		added = append(added, d)
	}
	if key == KeyDefaultDomain {
		out, err = setScalar(out, KeyDefaultDomain, newDomains[0])
		if err != nil {
			return res, err
		}
	}
	changed := !bytes.Equal(out, raw)

	// —— S5 + S6：字节确有变化才开事务（零 write-set 不开事务、不发 intent、不占号）。——
	if changed {
		ws := []store.AtomicFileSpec{{
			Path: ConfigFileName, TargetBytes: out, TargetHash: store.ContentHash(out),
			IsNew: false, PreBytes: raw, PreHash: store.ContentHash(raw),
		}}
		txnID, oerr := sess.openTxn(inv, intentFilesOf(ws), nil, r.Now, nil)
		if txnID != "" {
			// 审计边界是「号码分配成功」，不是「提交成功」（合同 A-59）。
			res.Data["txn_id"] = txnID
		}
		if oerr != nil {
			return res, oerr
		}
		cres, cmterr := sess.commitWriteSet(txnID, ws)
		switch {
		case cmterr != nil && cres != nil && cres.RolledBack:
			return res, &PartialWriteError{Msg: fmt.Sprintf(
				"原子提交失败，事务 %s 已整体回滚：%s 一个字节都没有写入、"+
					"磁盘保持事务开始前的字节、未产生 commit（原因：%v）",
				txnID, ConfigFileName, cmterr)}
		case cmterr != nil:
			return res, blockedError(fmt.Sprintf(
				"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cmterr)
		}
	}

	// 领域目录只新建、不改任何既有字节，因此不属原子域（崩溃后重跑 config set 即收敛）。
	var createdDirs []string
	for _, d := range append([]string{}, newDomains...) {
		for _, dir := range domainDirs(d) {
			abs := filepath.Join(root, dir)
			if _, serr := os.Stat(abs); serr == nil {
				continue
			}
			if merr := os.MkdirAll(abs, 0o755); merr != nil {
				return res, merr
			}
			createdDirs = append(createdDirs, dir+"/")
		}
	}

	// —— S7：Git。仍持同一把锁；走 r.repo(root) 这个全仓统一的 Git 注入面。——
	repo := r.repo(root)
	// 本次写入只有 evergreen.yml（领域目录是新建、不改既有字节）：其余改动即既有改动。
	// 采样早于 Add，措辞与其余写命令同源（I-…-023：config set 不再是唯一如实的孤例）。
	noteExistingChangesInResult(res, repo, []string{ConfigFileName})
	info, gerr := repo.Commit(git.Message{
		Verb:    string(model.VerbReconcile),
		Domain:  newDomains[0],
		Subject: fmt.Sprintf("更新 %s 的 %s", ConfigFileName, key),
		Reason:  fmt.Sprintf("eg config set %s %s", key, value),
	})
	if gerr != nil {
		return res, &CommitFailedError{Msg: "eg config set 的 Git 提交失败（磁盘保留现状）", Err: gerr}
	}

	res.Data["key"] = key
	res.Data["value"] = value
	res.Data["domains_added"] = added
	res.Data["created"] = createdDirs
	res.Data["config_changed"] = changed
	res.Data["commit"] = commitInfoData(info)
	res.Warnings = append(res.Warnings, gitWarnings(info)...)
	res.Summary = append(res.Summary, fmt.Sprintf("%s = %s", key, value))
	if !changed {
		res.Summary = append(res.Summary, ConfigFileName+" 已是目标值，未改写字节")
	}
	// —— S9：锁内的活已经做完，显式还锁，让渲染回到锁外。——
	sess.release()
	return res, nil
}

// setScalar 把顶层标量键的值替换成 value：只动那一行的值部分，其余字节逐字保留；
// 键不存在时在文件末尾追加一行。
func setScalar(raw []byte, key, value string) ([]byte, error) {
	start, end, valueFrom, ok := topLevelKeyLine(raw, key)
	if !ok {
		out := append([]byte{}, raw...)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		return append(out, []byte(key+": "+quoteScalar(value)+"\n")...), nil
	}
	_ = valueFrom
	out := make([]byte, 0, len(raw)+len(value))
	out = append(out, raw[:start]...)
	out = append(out, []byte(key+": "+quoteScalar(value))...)
	out = append(out, raw[end:]...)
	return out, nil
}

// appendDomain 向 domains 序列尾部追加一项：
//   - 块状序列（`domains:` + `  - x` 行）→ 在最后一项之后插入一行，沿用既有缩进；
//   - 空 flow 序列（`domains: []`）→ 换成块状并写入首项；
//   - 非空 flow 序列（`domains: [a, b]`）→ S1 不改写这种写法，报错让用户改成块状。
func appendDomain(raw []byte, domain string) ([]byte, error) {
	start, end, valueFrom, ok := topLevelKeyLine(raw, KeyDomains)
	if !ok {
		out := append([]byte{}, raw...)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		return append(out, []byte(KeyDomains+":\n  - "+domain+"\n")...), nil
	}
	value := bytes.TrimSpace(raw[valueFrom:end])
	switch {
	case len(value) == 0:
		at, indent := lastSeqItem(raw, end)
		item := indent + "- " + domain + "\n"
		out := make([]byte, 0, len(raw)+len(item))
		out = append(out, raw[:at]...)
		out = append(out, item...)
		out = append(out, raw[at:]...)
		return out, nil
	case bytes.Equal(value, []byte("[]")):
		out := make([]byte, 0, len(raw)+len(domain)+8)
		out = append(out, raw[:start]...)
		out = append(out, []byte(KeyDomains+":\n  - "+domain)...)
		out = append(out, raw[end:]...)
		return out, nil
	default:
		return nil, &UsageError{Msg: fmt.Sprintf(
			"%s 的 %s 是行内序列 %q：S1 不改写这种写法，请手工改成块状（每行一个 `- 领域`）后重试",
			ConfigFileName, KeyDomains, value)}
	}
}

// topLevelKeyLine 定位顶层键所在行：返回行起始、行末（不含换行）、值起始偏移。
func topLevelKeyLine(raw []byte, key string) (start, end, valueFrom int, ok bool) {
	prefix := []byte(key + ":")
	for i := 0; i < len(raw); {
		lineEnd := i
		for lineEnd < len(raw) && raw[lineEnd] != '\n' {
			lineEnd++
		}
		line := raw[i:lineEnd]
		if bytes.HasPrefix(line, prefix) {
			return i, lineEnd, i + len(prefix), true
		}
		i = lineEnd + 1
	}
	return 0, 0, 0, false
}

// lastSeqItem 返回块状序列最后一项之后的插入偏移与既有缩进（默认两空格）。
func lastSeqItem(raw []byte, keyLineEnd int) (int, string) {
	at := keyLineEnd + 1
	if at > len(raw) {
		at = len(raw)
	}
	indent := "  "
	for i := at; i <= len(raw); {
		lineEnd := i
		for lineEnd < len(raw) && raw[lineEnd] != '\n' {
			lineEnd++
		}
		line := raw[i:lineEnd]
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) == 0 || !bytes.HasPrefix(trimmed, []byte("- ")) {
			break
		}
		indent = string(line[:len(line)-len(trimmed)])
		at = lineEnd + 1
		i = at
	}
	return at, indent
}
