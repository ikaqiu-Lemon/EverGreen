import * as assert from "node:assert/strict";
import test from "node:test";
import type {EvergreenReviewCandidate} from "../../types/api";
import {
    candidateMetadataFromDraft,
    candidateRefDiff,
    createMaterializationTarget,
    createReviewOperationID,
    formatCandidatePayload,
    parseCandidatePayload,
} from "./model";

const candidate = (): EvergreenReviewCandidate => ({
    candidate_id: "cand-1",
    block_id: "20261010120000-cand001",
    metadata: {
        candidate_id: "cand-1",
        claim_kind: "knowledge",
        kind_schema: "evergreen.claim.knowledge/v1",
        payload_hash: "payload-before",
        ref_hashes: {"seg-1": "before", "seg-2": "same"},
        segment_refs: ["seg-1", "seg-2"],
        state: "stale",
        title: "Old title",
    },
    payload: [{ID: "20261010120001-payload", Type: "NodeParagraph", Data: "Claim"}],
    current_payload_hash: "payload-after",
    current_ref_hashes: {"seg-1": "after", "seg-2": "same"},
    stale: true,
    stale_refs: ["seg-1"],
    order: 0,
});

test("candidate payload parsing keeps native node arrays and rejects non-arrays", () => {
    const payload = candidate().payload;
    assert.deepEqual(parseCandidatePayload(formatCandidatePayload(payload)), payload);
    assert.throws(() => parseCandidatePayload('{"Type":"NodeParagraph"}'), /JSON array/);
});

test("candidate draft updates editable metadata without losing core fingerprints", () => {
    const updated = candidateMetadataFromDraft(candidate(), {
        title: "  Reviewed title ",
        claimKind: " knowledge ",
        kindSchema: " evergreen.claim.knowledge/v1 ",
        relation: " support ",
        reason: " direct evidence ",
    });
    assert.equal(updated.title, "Reviewed title");
    assert.equal(updated.relation, "support");
    assert.equal(updated.reason, "direct evidence");
    assert.equal(updated.payload_hash, "payload-before");
    assert.deepEqual(updated.ref_hashes, {"seg-1": "before", "seg-2": "same"});
});

test("stale diff and materialization identity are deterministic", () => {
    assert.deepEqual(candidateRefDiff(candidate()), [{
        segmentID: "seg-1",
        before: "before",
        after: "after",
    }]);
    assert.equal(createReviewOperationID("candidate.confirm", "node-id"),
        "review-candidate-confirm-node-id");
    assert.deepEqual(
        createMaterializationTarget("box-id", "20261010210000-abcdefg", new Date("2026-10-10T13:00:00Z")),
        {
            claimID: "c-20261010210000-abcdefg",
            claimPath: "box-id/20261010210000-abcdefg.sy",
            claimDocumentID: "20261010210000-abcdefg",
            createdAt: "2026-10-10T13:00:00.000Z",
        },
    );
});
