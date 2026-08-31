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

	"evergreen/internal/git"
	"evergreen/internal/model"
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
func (r *Root) runConfig(inv *Invocation) (*Result, error) {
	root, err := r.configVault(inv)
	if err != nil {
		return nil, err
	}
	cfg, err := r.LoadConfig(root)
	if err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf("%s 不可解析：%v", ConfigFileName, err)}
	}
	key := inv.Args[0]
	if inv.Sub == "get" {
		return configGet(cfg, key), nil
	}
	return r.configSet(root, cfg, key, inv.Args[1])
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

// configSet 写 evergreen.yml（字节区间编辑）→ 按需建领域目录 → 产生一次提交。
func (r *Root) configSet(root string, cfg model.Config, key, value string) (*Result, error) {
	path := filepath.Join(root, ConfigFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
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

	out := raw
	// domains 取并集追加，不删除既有领域（S1 不做领域迁移 / 重命名 / 删除）。
	var added []string
	for _, d := range newDomains {
		if cfg.HasDomain(d) {
			continue
		}
		out, err = appendDomain(out, d)
		if err != nil {
			return nil, err
		}
		added = append(added, d)
	}
	if key == KeyDefaultDomain {
		out, err = setScalar(out, KeyDefaultDomain, newDomains[0])
		if err != nil {
			return nil, err
		}
	}

	changed := !bytes.Equal(out, raw)
	if changed {
		if err := writeFileAtomic(path, out); err != nil {
			return nil, err
		}
	}
	var createdDirs []string
	for _, d := range append([]string{}, newDomains...) {
		for _, dir := range domainDirs(d) {
			abs := filepath.Join(root, dir)
			if _, err := os.Stat(abs); err == nil {
				continue
			}
			if err := os.MkdirAll(abs, 0o755); err != nil {
				return nil, err
			}
			createdDirs = append(createdDirs, dir+"/")
		}
	}

	repo := git.New(root)
	info, err := repo.Commit(git.Message{
		Verb:    string(model.VerbReconcile),
		Domain:  newDomains[0],
		Subject: fmt.Sprintf("更新 %s 的 %s", ConfigFileName, key),
		Reason:  fmt.Sprintf("eg config set %s %s", key, value),
	})
	if err != nil {
		return nil, &CommitFailedError{Msg: "eg config set 的 Git 提交失败（磁盘保留现状）", Err: err}
	}

	res := &Result{Data: map[string]interface{}{
		"key":            key,
		"value":          value,
		"domains_added":  added,
		"created":        createdDirs,
		"config_changed": changed,
		"commit":         commitInfoData(info),
	}}
	res.Warnings = append(res.Warnings, gitWarnings(info)...)
	res.Summary = append(res.Summary, fmt.Sprintf("%s = %s", key, value))
	if !changed {
		res.Summary = append(res.Summary, ConfigFileName+" 已是目标值，未改写字节")
	}
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
