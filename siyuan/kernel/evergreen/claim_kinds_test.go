package evergreen

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
)

func TestKindDescriptorHydrationAndNativeEnvelopeSave(t *testing.T) {
	root := t.TempDir()
	raw := runtimeClaimFixture(t, "c-hypothesis", evergreencore.KnowledgeKind, nil)
	document, _ := evergreencore.DecodeSY(raw, installedKindRegistry())
	document.Envelope.Claim = &evergreencore.Claim{
		ClaimKind: evergreencore.HypothesisKind, KindSchema: evergreencore.HypothesisKindSchema,
		Status: "testing", Tags: []string{},
		KindData: json.RawMessage(`{"statement":"Prediction","test_method":"Measure","future":{"kept":true}}`),
	}
	raw, err := evergreencore.EncodeSY(raw, document.Envelope, installedKindRegistry())
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeClaim(t, root, "box/hypothesis.sy", raw)
	writeRuntimeClaim(t, root, "box/knowledge.sy", runtimeClaimFixture(t, "k-one", evergreencore.KnowledgeKind, nil))
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{Root: root, Committer: &runtimeAVCommitter{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	schema, err := runtime.DescribeAV(context.Background(), "hypothesis")
	if err != nil {
		t.Fatal(err)
	}
	attrView := av.NewSourceBackedAttributeView("20261010190000-hyp0001", schema)
	if err = runtime.HydrateAttributeView(context.Background(), attrView); err != nil {
		t.Fatal(err)
	}
	if len(attrView.GetBlockKeyValues().Values) != 1 ||
		runtimeHydratedValue(t, attrView, "test_method", "c-hypothesis").Text.Content != "Measure" {
		t.Fatalf("descriptor hydration = %+v", attrView.GetBlockKeyValues())
	}
	captured, err := Capture(raw)
	if err != nil {
		t.Fatal(err)
	}
	var native map[string]json.RawMessage
	if err = json.Unmarshal(raw, &native); err != nil {
		t.Fatal(err)
	}
	delete(native, "Evergreen")
	rendered, _ := json.Marshal(native)
	injected, err := Inject(rendered, captured)
	if err != nil {
		t.Fatalf("native save failed to use installed kind descriptor: %v", err)
	}
	decoded, err := NewHost(nil).Decode(injected)
	if err != nil || decoded.ReadOnly || !bytes.Contains(decoded.Envelope.Claim.KindData, []byte(`"kept":true`)) {
		t.Fatalf("native save lost hypothesis metadata: %+v %v", decoded, err)
	}
}
