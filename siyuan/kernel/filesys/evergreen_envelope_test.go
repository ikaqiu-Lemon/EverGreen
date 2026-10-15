// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package filesys

import (
	"encoding/json"
	"path/filepath"
	"testing"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	egstorage "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenEnvelopeSurvivesTreeParseRender(t *testing.T) {
	originalDataDir := util.DataDir
	util.DataDir = t.TempDir()
	t.Cleanup(func() { util.DataDir = originalDataDir })

	const (
		boxID = "20261010115900-box0001"
		path  = "/20261010120000-a1b2c3d.sy"
	)
	raw := []byte(`{
		"ID":"20261010120000-a1b2c3d",
		"Type":"NodeDocument",
		"Spec":"5",
		"Properties":{"id":"20261010120000-a1b2c3d","title":"Evergreen"},
		"Evergreen":{
			"spec":"evergreen.sy/v1",
			"entity":{
				"logical_id":"c-20261010-claim",
				"entity_type":"claim",
				"schema":"evergreen.claim/v1",
				"semantic_revision":1
			},
			"claim":{
				"claim_kind":"knowledge",
				"kind_schema":"evergreen.claim-kind.knowledge/v1",
				"status":"active",
				"tags":[],
				"kind_data":{},
				"future_claim":{"keep":true}
			},
			"provenance":[{
				"source_id":"s-20261010-source",
				"note_id":"n-20261010-note",
				"segment_refs":["seg-01"],
				"reason":"source"
			}],
			"relations":{"outgoing":[]},
			"future_envelope":{"keep":true}
		},
		"Children":[{
			"ID":"20261010120001-a1b2c3d",
			"Type":"NodeParagraph",
			"Properties":{"id":"20261010120001-a1b2c3d","updated":"20261010120001"},
			"Evergreen":{
				"spec":"evergreen.block/v1",
				"role":"note_segment",
				"segment":{
					"segment_id":"seg-01",
					"source_ref":{
						"source_id":"s-20261010-source",
						"locator":{"kind":"source-block","value":"block-1"}
					},
					"normalized_hash":"sha256:segment"
				}
			},
			"Data":"body"
		}]
	}`)

	tree, err := LoadTreeByData(raw, boxID, path, util.NewLute())
	if err != nil {
		t.Fatal(err)
	}
	tree.Root.FirstChild.Tokens = []byte("edited")
	rendered, filePath, err := prepareWriteTree(tree)
	if err != nil {
		t.Fatal(err)
	}
	if filePath != filepath.Join(util.DataDir, boxID, path) {
		t.Fatalf("file path = %q", filePath)
	}
	document, err := evergreencore.DecodeSY(rendered, evergreencore.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if document.Envelope == nil || document.Envelope.Entity.LogicalID != "c-20261010-claim" {
		t.Fatalf("document envelope missing after render: %+v", document)
	}
	var root map[string]json.RawMessage
	if err = json.Unmarshal(rendered, &root); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(root["Evergreen"], &envelope); err != nil {
		t.Fatal(err)
	}
	if _, ok := envelope["future_envelope"]; !ok {
		t.Fatal("unknown document envelope field was dropped")
	}
	var children []map[string]json.RawMessage
	if err = json.Unmarshal(root["Children"], &children); err != nil {
		t.Fatal(err)
	}
	if _, ok := children[0]["Evergreen"]; !ok {
		t.Fatal("block envelope was dropped")
	}
	if _, leaked := children[0]["Properties"]; leaked {
		var properties map[string]string
		if err = json.Unmarshal(children[0]["Properties"], &properties); err != nil {
			t.Fatal(err)
		}
		if _, exists := properties[egstorage.CarrierAttribute]; exists {
			t.Fatal("internal Evergreen carrier leaked into persisted Properties")
		}
	}
}

func TestEvergreenEnvelopeSurvivesTreeJSONFixWriteback(t *testing.T) {
	originalDataDir := util.DataDir
	util.DataDir = t.TempDir()
	t.Cleanup(func() { util.DataDir = originalDataDir })

	const (
		boxID = "20261010115900-box0001"
		path  = "/20261010120000-fix0002.sy"
	)
	raw := []byte(`{
		"ID":"20261010120000-fix0001",
		"Type":"NodeDocument",
		"Spec":"3",
		"Properties":{"id":"20261010120000-fix0001","title":"Evergreen Fix"},
		"Evergreen":{
			"spec":"evergreen.sy/v1",
			"entity":{
				"logical_id":"c-fix-claim",
				"entity_type":"claim",
				"schema":"evergreen.claim/v1",
				"semantic_revision":1
			},
			"claim":{
				"claim_kind":"knowledge",
				"kind_schema":"evergreen.claim-kind.knowledge/v1",
				"status":"active",
				"tags":[],
				"kind_data":{}
			},
			"relations":{"outgoing":[]}
		},
		"Children":[{
			"ID":"20261010120001-fixbody",
			"Type":"NodeParagraph",
			"Properties":{"id":"20261010120001-fixbody"},
			"Data":"body"
		}]
	}`)

	fixed, needFix, err := fixTreeJSONData(boxID, path, raw, util.NewLute(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !needFix {
		t.Fatal("legacy tree fixture did not exercise the fix writeback path")
	}
	document, err := evergreencore.DecodeSY(fixed, evergreencore.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if document.Envelope == nil || document.Envelope.Entity.LogicalID != "c-fix-claim" {
		t.Fatalf("document envelope missing after fix writeback: %+v", document)
	}
}
