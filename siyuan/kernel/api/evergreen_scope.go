// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"sort"
	"strings"

	"github.com/88250/lute/ast"
	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
)

const evergreenNotebookScopeHeader = "X-Evergreen-Notebook-Scope"
const evergreenNotebookScopeConfiguredKey = "evergreen.notebook-scope-configured"
const evergreenNotebookScopeBoxesKey = "evergreen.notebook-scope-boxes"

func evergreenRuntimeForRequest(c *gin.Context) (*kernelEvergreen.Runtime, error) {
	if err := configureEvergreenNotebookScope(c); err != nil {
		return nil, err
	}
	return evergreenRuntimeProvider(c.Request.Context())
}

func configureEvergreenNotebookScope(c *gin.Context) error {
	if configured, exists := c.Get(evergreenNotebookScopeConfiguredKey); exists && configured == true {
		return nil
	}
	values, present := c.Request.Header[evergreenNotebookScopeHeader]
	if !present {
		c.Set(evergreenNotebookScopeConfiguredKey, true)
		c.Set(evergreenNotebookScopeBoxesKey, []string{})
		return nil
	}
	boxSet := map[string]struct{}{}
	for _, value := range values {
		for _, boxID := range strings.Split(value, ",") {
			boxID = strings.TrimSpace(boxID)
			if boxID == "" || !ast.IsNodeIDPattern(boxID) {
				return evergreenScopeError(evergreencore.CodeInvalidPlan,
					"notebook scope must contain only valid notebook IDs")
			}
			boxSet[boxID] = struct{}{}
			if len(boxSet) > 64 {
				return evergreenScopeError(evergreencore.CodeInvalidPlan,
					"notebook scope exceeds the request limit")
			}
		}
	}
	if len(boxSet) == 0 {
		return evergreenScopeError(evergreencore.CodeInvalidPlan, "notebook scope must not be empty")
	}
	boxes := make([]string, 0, len(boxSet))
	for boxID := range boxSet {
		boxes = append(boxes, boxID)
	}
	sort.Strings(boxes)
	for _, boxID := range boxes {
		if err := holdEncryptedBoxRequest(c, boxID); err != nil {
			return evergreenScopeError(evergreencore.CodeTransactionBlocked, err.Error())
		}
	}
	ctx, err := filesys.WithEvergreenNotebookScope(c.Request.Context(), boxes)
	if err != nil {
		return evergreenScopeError(evergreencore.CodeInvalidPlan, err.Error())
	}
	c.Request = c.Request.WithContext(ctx)
	c.Set(evergreenNotebookScopeConfiguredKey, true)
	c.Set(evergreenNotebookScopeBoxesKey, boxes)
	return nil
}

func evergreenRequestNotebookScope(c *gin.Context) []string {
	value, _ := c.Get(evergreenNotebookScopeBoxesKey)
	boxes, _ := value.([]string)
	return append([]string(nil), boxes...)
}

func evergreenScopeError(code, message string) error {
	return &evergreencore.DiagnosticError{Diagnostics: []evergreencore.Diagnostic{{
		Code: code, Level: "error", Path: "notebook_scope", Message: message,
	}}}
}
