package mdfile

// 审阅式 Note 的**机器锚点协议**（Schema v2 契约 §4.2 第 4 条 / §4.2.1 / T-012）：
// 单一、版本化、单行 HTML 注释，载荷为 base64url(JSON struct)。
//
// # 为什么是「固定前缀 + base64url(JSON)」而不是字符串切分
//
// 锚点要让 source_ref / annotation / 自定义 key/label / heading / block 顺序、以及
// omissions[] 的 source_ref/reason 可**严格读回**。若把这些字段用分隔符拼进注释再靠
// split 猜字段，任一字段里出现分隔符（heading 里的 `|`、label 里的空格、reason 里的 `-->`）
// 就会读错，且读错时无法与合法输入区分。改用结构化载荷：JSON 负责字段边界与转义，
// base64url 负责把载荷压成**单行且不含 `-->`** 的安全字节（base64url 字母表只有
// [A-Za-z0-9_-]，天然不产生注释结束符、不含换行）。读侧用 DisallowUnknownFields 关闭
// 「多字段静默忽略」，用精确的 open 前缀关闭「未知版本静默当合法」。
//
// # 单一归属
//
// 协议常量（family / version / open / close）、编码、解码只在本文件定义一次；writer
// （internal/store）与 parser（review.go）都调用这里的函数，不各抄一套字面量。

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// 协议常量。family 用于**识别**一条注释是否属于本协议（据此对未知版本 fail closed），
// version 固定为 1；open 是 v1 的精确前缀，close 是注释结束符。
const (
	reviewAnchorFamily  = "eg:nr:"
	reviewAnchorVersion = "1"
	reviewAnchorTag     = reviewAnchorFamily + reviewAnchorVersion // eg:nr:1
	reviewAnchorOpen    = "<!-- " + reviewAnchorTag + " "
	reviewAnchorClose   = " -->"
)

// 锚点种类（载荷 kind 字段的封闭取值）。s/a = source/agent 块边界锚点；o = omission 元数据。
const (
	anchorKindSource   = "s"
	anchorKindAgent    = "a"
	anchorKindOmission = "o"
)

// reviewAnchor 是锚点载荷的结构化形态。字段声明序即 JSON 编码序（Go 的 struct 编码
// 确定性 + omitempty），保证同一结构体每次 Marshal 出同一份字节 → render 稳定。
// json tag 用短名压缩注释长度；omitempty 让缺省字段不占字节（空 heading 不写 h）。
type reviewAnchor struct {
	Kind       string `json:"k"`
	Heading    string `json:"h,omitempty"`
	SourceRef  string `json:"s,omitempty"`
	Annotation string `json:"a,omitempty"`
	Label      string `json:"l,omitempty"`
	Reason     string `json:"r,omitempty"`
}

// encodeReviewAnchor 把载荷编码成单行 HTML 注释。struct 编码不会失败，故不返回 error。
func encodeReviewAnchor(a reviewAnchor) string {
	raw, err := json.Marshal(a)
	if err != nil { // 理论不可达：字段全为 string；保留分支以防未来加入不可编码类型时静默出错。
		panic(fmt.Sprintf("review anchor 编码失败（不可达）：%v", err))
	}
	return reviewAnchorOpen + base64.RawURLEncoding.EncodeToString(raw) + reviewAnchorClose
}

// isReviewAnchorLine 报告一行（trim 后）是否**声称**是本协议的锚点（任意版本）。
// 只看 family 前缀与注释结束符：据此把「疑似本协议但版本不对」的行也识别出来，
// 交给 decodeReviewAnchor 去 fail closed，而不是当成普通可见正文放过。
func isReviewAnchorLine(line []byte) bool {
	t := bytes.TrimSpace(line)
	return bytes.HasPrefix(t, []byte("<!-- "+reviewAnchorFamily)) &&
		bytes.HasSuffix(t, []byte(reviewAnchorClose))
}

// decodeReviewAnchor 解码一行锚点。任一环节不满足即 error（fail closed）：
// 未知版本（family 命中但不是精确 v1 open）、base64 非法、JSON 非法、含未知字段、
// 对象含重复键、载荷尾部有多余 token / 第二个顶层值、kind 越界——都拒绝，绝不猜。
func decodeReviewAnchor(line []byte) (reviewAnchor, error) {
	t := bytes.TrimSpace(line)
	if !bytes.HasPrefix(t, []byte(reviewAnchorOpen)) {
		return reviewAnchor{}, fmt.Errorf("未知的 review 锚点版本（仅支持 %s）：%q", reviewAnchorTag, t)
	}
	if !bytes.HasSuffix(t, []byte(reviewAnchorClose)) {
		return reviewAnchor{}, fmt.Errorf("review 锚点缺注释结束符：%q", t)
	}
	enc := t[len(reviewAnchorOpen) : len(t)-len(reviewAnchorClose)]
	raw, err := base64.RawURLEncoding.DecodeString(string(enc))
	if err != nil {
		return reviewAnchor{}, fmt.Errorf("review 锚点载荷 base64url 非法：%v", err)
	}
	// 显式拒绝重复键：encoding/json 默认「后者覆盖前者」静默取最后一个值，一份
	// {"k":"s","k":"a"} 会被悄悄读成 agent。锚点是机器契约，同名键出现两次即畸形。
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return reviewAnchor{}, fmt.Errorf("review 锚点载荷含重复键：%v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a reviewAnchor
	if err := dec.Decode(&a); err != nil {
		return reviewAnchor{}, fmt.Errorf("review 锚点载荷 JSON 非法：%v", err)
	}
	// 必须恰好一个顶层值：dec.More() 无法可靠识别「第一个值后还有第二个顶层值」，
	// 改为再 Decode 一次——只有读到 io.EOF 才说明流里就一个对象，否则即畸形（多余数据）。
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return reviewAnchor{}, fmt.Errorf("review 锚点载荷含多余数据（应恰一个 JSON 对象）")
	}
	switch a.Kind {
	case anchorKindSource, anchorKindAgent, anchorKindOmission:
	default:
		return reviewAnchor{}, fmt.Errorf("review 锚点 kind 越界：%q", a.Kind)
	}
	return a, nil
}

// rejectDuplicateJSONKeys 遍历载荷的 token 流，对任意对象内重复出现的键返回 error。
// encoding/json 本身不视重复键为错误（last-wins），故单独走一遍 Token() 做去重校验；
// 递归覆盖嵌套对象 / 数组，虽然当前载荷是扁平对象，但不对结构形态做假设。
func rejectDuplicateJSONKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	return checkNoDuplicateKeys(dec)
}

// checkNoDuplicateKeys 消费 dec 中的下一个 JSON 值；遇到对象时逐键查重、遇到数组时逐元素递归。
func checkNoDuplicateKeys(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // 标量（string/number/bool/null）：无键可查。
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("对象键不是字符串")
			}
			if seen[key] {
				return fmt.Errorf("键 %q 出现多次", key)
			}
			seen[key] = true
			if err := checkNoDuplicateKeys(dec); err != nil { // 键对应的值
				return err
			}
		}
		if _, err := dec.Token(); err != nil { // 消费 '}'
			return err
		}
	case '[':
		for dec.More() {
			if err := checkNoDuplicateKeys(dec); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil { // 消费 ']'
			return err
		}
	}
	return nil
}
