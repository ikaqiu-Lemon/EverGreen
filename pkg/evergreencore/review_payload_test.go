package evergreencore

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMaterializedPayloadOwnsDeterministicBlockIDsAndInternalReferences(t *testing.T) {
	input := json.RawMessage(`{
		"ID":"20261010120001-old0001","Type":"NodeParagraph",
		"Properties":{"id":"20261010120001-old0001","custom-user":"retained"},
		"Children":[
			{"Type":"NodeTextMark","TextMarkBlockRefID":"20261010120002-old0002"},
			{"ID":"20261010120002-old0002","Type":"NodeParagraph","Data":"Keep original prose"},
			{"Type":"NodeBlockRef","NodeBlockRefID":"20261010120003-external"}
		]
	}`)
	node, err := parseReviewNode(input)
	if err != nil {
		t.Fatal(err)
	}
	first, err := materializedPayload([]*reviewNode{node}, "20261010130000-claim01")
	if err != nil {
		t.Fatal(err)
	}
	second, err := materializedPayload([]*reviewNode{node}, "20261010130000-claim01")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("materialized block IDs are not deterministic")
	}
	var output struct {
		ID         string            `json:"ID"`
		Properties map[string]string `json:"Properties"`
		Children   []struct {
			ID                 string `json:"ID"`
			Data               string `json:"Data"`
			TextMarkBlockRefID string `json:"TextMarkBlockRefID"`
			NodeBlockRefID     string `json:"NodeBlockRefID"`
		} `json:"Children"`
	}
	if err = json.Unmarshal(first[0], &output); err != nil {
		t.Fatal(err)
	}
	if output.ID == "20261010120001-old0001" ||
		output.Children[1].ID == "20261010120002-old0002" ||
		output.ID == output.Children[1].ID ||
		output.Properties["id"] != output.ID ||
		output.Children[0].TextMarkBlockRefID != output.Children[1].ID ||
		output.Children[2].NodeBlockRefID != "20261010120003-external" ||
		output.Properties["custom-user"] != "retained" ||
		output.Children[1].Data != "Keep original prose" {
		t.Fatalf("materialized payload = %s", first[0])
	}
	original, _ := node.encode()
	var before, after any
	_ = json.Unmarshal(input, &before)
	_ = json.Unmarshal(original, &after)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("materialization mutated the Candidate subtree")
	}
}
