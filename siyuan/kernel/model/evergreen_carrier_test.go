// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"testing"

	"github.com/88250/lute/ast"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
)

func TestEvergreenCarrierAttributeCannotBeInjectedOrRemoved(t *testing.T) {
	node := &ast.Node{ID: "20261011120100-native1", Type: ast.NodeParagraph}
	node.SetIALAttr(kernelEvergreen.CarrierAttribute, "existing")
	for _, key := range []string{kernelEvergreen.CarrierAttribute, strings.ToUpper(kernelEvergreen.CarrierAttribute)} {
		for _, value := range []string{"", "forged"} {
			_, err := setNodeAttrs0(node, map[string]string{"custom-user": "attempted", key: value}, "")
			if !core.HasDiagnostic(err, core.CodeUnauthorized) {
				t.Fatalf("carrier attr mutation accepted: %v", err)
			}
			if node.IALAttr(kernelEvergreen.CarrierAttribute) != "existing" || node.IALAttr("custom-user") != "" {
				t.Fatal("attribute mutation applied a subset before rejection")
			}
		}
	}
}
