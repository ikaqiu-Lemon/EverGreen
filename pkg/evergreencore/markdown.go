package evergreencore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/88250/lute"
	"github.com/88250/lute/ast"
	"github.com/88250/lute/parse"
	"github.com/88250/lute/render"
	"github.com/siyuan-note/dataparser"
)

// StablePhysicalID 在计划阶段分配确定性 ID；导入与重试均复用相同的 seed。
func StablePhysicalID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "20000101000000-" + hex.EncodeToString(sum[:])[:7]
}

// MarkdownChildren 使用与 SiYuan 相同的 Lute AST，保留表格、图片、代码与正文顺序。
func MarkdownChildren(body []byte, seed string) ([]json.RawMessage, error) {
	engine := lute.New()
	engine.SetSuperBlock(true)
	engine.SetKramdownBlockIAL(true)
	engine.SetKramdownSpanIAL(true)
	engine.SetTag(true)
	tree := parse.Parse("", body, engine.ParseOptions)
	parse.NestedInlines2FlattedSpansHybrid(tree, false)
	index := 0
	ast.Walk(tree.Root, func(node *ast.Node, entering bool) ast.WalkStatus {
		if entering && node.IsBlock() {
			node.ID = StablePhysicalID(fmt.Sprintf("%s/block/%d", seed, index))
			node.SetIALAttr("id", node.ID)
			index++
		}
		return ast.WalkContinue
	})
	raw := render.NewJSONRenderer(tree, engine.RenderOptions, engine.ParseOptions).Render()
	var root struct {
		Children []json.RawMessage `json:"Children"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	if root.Children == nil {
		root.Children = []json.RawMessage{}
	}
	return root.Children, nil
}

func NewMarkdownDocument(id LogicalID, title string, body []byte, envelope *DocumentEnvelope, registry *Registry) ([]byte, error) {
	children, err := MarkdownChildren(body, string(id))
	if err != nil {
		return nil, err
	}
	base, err := json.Marshal(map[string]any{
		"ID": StablePhysicalID(string(id) + "/document"), "Type": "NodeDocument", "Spec": "5",
		"Properties": map[string]string{"title": title}, "Children": children,
	})
	if err != nil {
		return nil, err
	}
	return EncodeSY(base, envelope, registry)
}

// SuperBlockChildren 为原生容器补齐语法节点，供 Protyle 正常渲染并保留所有内容块。
func SuperBlockChildren(children []json.RawMessage) []json.RawMessage {
	result := []json.RawMessage{
		json.RawMessage(`{"Type":"NodeSuperBlockOpenMarker"}`),
		json.RawMessage(`{"Type":"NodeSuperBlockLayoutMarker","Data":"row"}`),
	}
	result = append(result, children...)
	return append(result, json.RawMessage(`{"Type":"NodeSuperBlockCloseMarker"}`))
}

// MarkdownText 从原生 AST 导出当前正文，供检索和语义审阅使用，不读取旧 Markdown 副本。
func MarkdownText(children json.RawMessage) ([]byte, error) {
	root, err := json.Marshal(map[string]any{"ID": "20000101000000-egread0", "Type": "NodeDocument", "Spec": "5", "Children": children})
	if err != nil {
		return nil, err
	}
	engine := lute.New()
	tree, err := dataparser.ParseJSONWithoutFix(root, engine.ParseOptions)
	if err != nil {
		return nil, err
	}
	return render.NewProtyleExportMdRenderer(tree, engine.RenderOptions, engine.ParseOptions).Render(), nil
}
