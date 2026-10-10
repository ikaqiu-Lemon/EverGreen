package segment

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestDeepSeekLegacyParity46Segments25Candidates(t *testing.T) {
	note := readFixture(t, "testdata/deepseek-review/note.md")
	workspace := readFixture(t, "testdata/deepseek-review/workspace.md")
	first, err := ImportLegacyWorkspace(note, workspace)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ImportLegacyWorkspace(note, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("legacy import is not deterministic")
	}
	if first.Inventory.Stale ||
		len(first.Inventory.Segments) != 46 ||
		len(first.Inventory.Candidates) != 25 ||
		first.Inventory.CoverageCount != 46 {
		t.Fatalf("inventory = %+v", first.Inventory)
	}
	for index, segment := range first.Inventory.Segments {
		want := "B" + itoa(index+1)
		if segment.LegacyRef != want || segment.Order != index ||
			segment.SegmentID == "" || segment.BlockID == "" ||
			segment.Hash == "" {
			t.Fatalf("segment[%d] = %+v, want ref %s", index, segment, want)
		}
	}
	multi := 0
	for _, candidate := range first.Inventory.Candidates {
		if candidate.LegacyKey == "" || candidate.CandidateID == "" ||
			candidate.BlockID == "" || candidate.PayloadHash == "" {
			t.Fatalf("candidate parity is incomplete: %+v", candidate)
		}
		if len(candidate.SegmentIDs) > 1 {
			multi++
		}
	}
	if multi != 14 {
		t.Fatalf("multi-segment candidates = %d, want 14", multi)
	}
	snapshot, err := evergreencore.InspectNoteReview(first.SY, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.CanMaterialize ||
		snapshot.Summary.SegmentCount != 46 ||
		snapshot.Summary.CandidateCount != 25 ||
		snapshot.Summary.MissingSegments != 0 ||
		snapshot.Summary.DuplicateSegments != 0 {
		t.Fatalf("imported snapshot = %+v", snapshot)
	}
	if strings.Contains(string(first.SY), `"ns-20260929-deepseek-harness-review"`) ||
		strings.Contains(string(first.SY), `"note-segments"`) {
		t.Fatal("runtime .sy retained a physical ns-* authority reference")
	}

	index, err := evergreencore.ScanFS(fstest.MapFS{
		"box/note.sy": &fstest.MapFile{Data: first.SY, Mode: fs.FileMode(0o644)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// One Note + 46 segments + 25 Candidates.
	if len(index.Locations) != 72 {
		t.Fatalf("logical index locations = %d, want 72", len(index.Locations))
	}
}

func TestLegacyParitySyntheticStateMatrix(t *testing.T) {
	note := readFixture(t, "testdata/deepseek-review/note.md")
	workspace := readFixture(t, "testdata/deepseek-review/workspace.md")
	imported, err := ImportLegacyWorkspace(note, workspace)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := evergreencore.InspectNoteReview(imported.SY, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("materialized output round trips", func(t *testing.T) {
		metadata := snapshot.Candidates[0].Metadata
		metadata.State = "materialized"
		metadata.MaterializedClaimID = "k-20260929-materialized"
		after, _, err := evergreencore.ApplyReviewCommand(imported.SY,
			evergreencore.ReviewCommand{
				Action:      evergreencore.ReviewActionCandidateUpdate,
				CandidateID: metadata.CandidateID, Metadata: &metadata,
			}, nil)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := evergreencore.InspectNoteReview(after, nil)
		if err != nil {
			t.Fatal(err)
		}
		if roundTrip.Candidates[0].Metadata.MaterializedClaimID !=
			"k-20260929-materialized" {
			t.Fatalf("materialized candidate = %+v", roundTrip.Candidates[0])
		}
	})

	t.Run("unresolved blocks materialization", func(t *testing.T) {
		coverage := append([]evergreencore.CoverageModule(nil), snapshot.Coverage...)
		coverage[0].Disposition = "unresolved"
		coverage[0].CandidateID = ""
		coverage[0].CandidateIDs = nil
		coverage[0].Reason = "Synthetic parity case requires human review."
		after, _, err := evergreencore.ApplyReviewCommand(imported.SY,
			evergreencore.ReviewCommand{
				Action:   evergreencore.ReviewActionCoverageUpdate,
				Coverage: coverage,
			}, nil)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := evergreencore.InspectNoteReview(after, nil)
		if err != nil {
			t.Fatal(err)
		}
		if roundTrip.CanMaterialize ||
			roundTrip.Summary.UnresolvedModules != 1 {
			t.Fatalf("unresolved snapshot = %+v", roundTrip)
		}
	})

	t.Run("stale survives index-free rebuild", func(t *testing.T) {
		var root map[string]json.RawMessage
		if err := json.Unmarshal(imported.SY, &root); err != nil {
			t.Fatal(err)
		}
		var children []map[string]json.RawMessage
		if err := json.Unmarshal(root["Children"], &children); err != nil {
			t.Fatal(err)
		}
		// B3 的受管容器通过逻辑身份定位，前言块不影响编辑目标。
		found := false
		for _, child := range children {
			raw, ok := child["Evergreen"]
			if !ok {
				continue
			}
			envelope, err := evergreencore.UnmarshalBlockEnvelope(raw)
			if err != nil {
				t.Fatal(err)
			}
			if envelope.Segment != nil && envelope.Segment.SegmentID == snapshot.Segments[2].SegmentID {
				child["Data"], _ = json.Marshal("edited after legacy import")
				found = true
			}
		}
		if !found {
			t.Fatal("B3 managed container not found")
		}
		root["Children"], _ = json.Marshal(children)
		edited, _ := json.Marshal(root)
		normalized, err := evergreencore.NormalizeNoteReviewEdit(
			imported.SY, edited, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		rebuilt, err := evergreencore.InspectNoteReview(normalized.After, nil)
		if err != nil {
			t.Fatal(err)
		}
		stale := 0
		for _, candidate := range rebuilt.Candidates {
			if candidate.Stale {
				stale++
			}
		}
		if stale == 0 || rebuilt.CanMaterialize {
			t.Fatalf("stale candidates=%d snapshot=%+v", stale, rebuilt)
		}
	})
}

func readFixture(t *testing.T, rel string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	at := len(digits)
	for value > 0 {
		at--
		digits[at] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[at:])
}
