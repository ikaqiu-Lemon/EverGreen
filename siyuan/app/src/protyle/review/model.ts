import type {
    EvergreenCandidateMetadataInput,
    EvergreenReviewCandidate,
    JSONValue,
} from "../../types/api";

export type CandidateDraft = {
    title: string;
    claimKind: string;
    kindSchema: string;
    relation: string;
    reason: string;
};

export type CandidateRefDiff = {
    segmentID: string;
    before: string;
    after: string;
};

export const formatCandidatePayload = (payload: JSONValue[]) => JSON.stringify(payload, null, 2);

export const parseCandidatePayload = (value: string): JSONValue[] => {
    const payload: unknown = JSON.parse(value);
    if (!Array.isArray(payload)) {
        throw new Error("candidate payload must be a JSON array");
    }
    return payload as JSONValue[];
};

export const candidateMetadataFromDraft = (
    candidate: EvergreenReviewCandidate,
    draft: CandidateDraft,
): EvergreenCandidateMetadataInput => ({
    ...candidate.metadata,
    title: draft.title.trim(),
    claim_kind: draft.claimKind.trim(),
    kind_schema: draft.kindSchema.trim(),
    relation: draft.relation.trim(),
    reason: draft.reason.trim(),
    ref_hashes: candidate.metadata.ref_hashes || {},
});

export const candidateRefDiff = (candidate: EvergreenReviewCandidate): CandidateRefDiff[] =>
    candidate.stale_refs.map(segmentID => ({
        segmentID,
        before: candidate.metadata.ref_hashes?.[segmentID] || "",
        after: candidate.current_ref_hashes?.[segmentID] || "",
    }));

export const createReviewOperationID = (action: string, nodeID: string) =>
    `review-${action.replaceAll(".", "-")}-${nodeID}`;

export const createMaterializationTarget = (
    notebookID: string,
    documentID: string,
    createdAt: Date,
) => ({
    claimID: `c-${documentID}`,
    claimPath: `${notebookID}/${documentID}.sy`,
    claimDocumentID: documentID,
    createdAt: createdAt.toISOString(),
});
