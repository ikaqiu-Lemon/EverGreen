import {Dialog} from "../../dialog";
import {showMessage} from "../../dialog/message";
import type {
    EvergreenCoverageModuleInput,
    EvergreenPlannedOperation,
    EvergreenPlannedOperationInput,
    EvergreenReviewCandidate,
    EvergreenReviewCommandInput,
    EvergreenReviewSnapshot,
    EvergreenKindSchema,
    JSONValue,
} from "../../types/api";
import {escapeAttr, escapeHtml} from "../../util/escape";
import {fetchSyncPost} from "../../util/fetch";
import {isMobile} from "../../util/functions";
import {collectSchemaFields, schemaDefaults, schemaFieldsHTML, validateSchemaFields} from "../claims/schemaEditor";
import {
    candidateMetadataFromDraft,
    candidateRefDiff,
    createMaterializationTarget,
    createReviewOperationID,
    formatCandidatePayload,
    parseCandidatePayload,
} from "./model";

type ReviewDialogState = {
    root: HTMLElement;
    snapshot: EvergreenReviewSnapshot;
    selectedCandidateID: string;
    busy: boolean;
    closed: boolean;
    readOnly: boolean;
    notebookID: string;
    kinds: EvergreenKindSchema[];
    kindDrafts: Map<string, Record<string, JSONValue>>;
    drafts: Map<string, {metadata: ReturnType<typeof candidateMetadataFromDraft>; payload: string}>;
    coverageDraft?: EvergreenCoverageModuleInput[];
};

const reviewRequestVersions = new WeakMap<IProtyle, number>();

const safeAttr = (value: string) => escapeAttr(escapeHtml(value || ""));

const responseError = (response: unknown, fallback: string) => {
    if (!response || typeof response !== "object") {
        return fallback;
    }
    if ("diagnostics" in response && Array.isArray(response.diagnostics) && response.diagnostics.length) {
        return response.diagnostics
            .map(item => item && typeof item === "object" && "message" in item ? String(item.message) : "")
            .filter(Boolean)
            .join("\n") || fallback;
    }
    if ("msg" in response && typeof response.msg === "string" && response.msg) {
        return response.msg;
    }
    return fallback;
};

const operationInput = (operation: EvergreenPlannedOperation): EvergreenPlannedOperationInput => {
    if (!operation.images || !operation.plan.base || !operation.plan.writes) {
        throw new Error(window.siyuan.languages.invalid);
    }
    return {
        ...operation,
        images: operation.images,
        plan: {
            ...operation.plan,
            base: operation.plan.base,
            writes: operation.plan.writes,
        },
    };
};

const fetchReviewSnapshot = async (documentID: string, silent = false) => {
    try {
        const response = await fetchSyncPost("/api/evergreen/v1/review/get", {
            document_id: documentID,
        }, undefined, false);
        if ("ok" in response && response.ok) {
            return response.data;
        }
        if (!silent) {
            throw new Error(responseError(response, window.siyuan.languages.invalid));
        }
    } catch (error) {
        if (!silent) {
            throw error;
        }
    }
};

const applyReviewCommand = async (
    snapshot: EvergreenReviewSnapshot,
    command: EvergreenReviewCommandInput,
) => {
    const planResponse = await fetchSyncPost("/api/evergreen/v1/review/plan", {
        operation_id: createReviewOperationID(command.action, Lute.NewNodeID()),
        note_id: snapshot.note_id,
        base: snapshot.semantic_hash,
        command,
    }, undefined, false);
    if (!("ok" in planResponse) || !planResponse.ok) {
        throw new Error(responseError(planResponse, window.siyuan.languages.invalid));
    }
    const applyResponse = await fetchSyncPost("/api/evergreen/v1/apply", {
        operation: operationInput(planResponse.data.operation),
    }, undefined, false);
    if (!("ok" in applyResponse) || !applyResponse.ok) {
        throw new Error(responseError(applyResponse, window.siyuan.languages.invalid));
    }
};

const materializeCandidate = async (
    notebookID: string,
    snapshot: EvergreenReviewSnapshot,
    candidate: EvergreenReviewCandidate,
    kindData: Record<string, JSONValue>,
) => {
    if (!notebookID) {
        throw new Error(window.siyuan.languages.invalid);
    }
    const documentID = Lute.NewNodeID();
    const target = createMaterializationTarget(notebookID, documentID, new Date());
    const planResponse = await fetchSyncPost("/api/evergreen/v1/review/plan-materialize", {
        operation_id: createReviewOperationID("candidate.materialize", Lute.NewNodeID()),
        note_id: snapshot.note_id,
        candidate_id: candidate.candidate_id,
        note_base: snapshot.semantic_hash,
        claim_id: target.claimID,
        claim_path: target.claimPath,
        claim_document_id: target.claimDocumentID,
        created_at: target.createdAt,
        kind_data: kindData,
    }, undefined, false);
    if (!("ok" in planResponse) || !planResponse.ok) {
        throw new Error(responseError(planResponse, window.siyuan.languages.invalid));
    }
    const applyResponse = await fetchSyncPost("/api/evergreen/v1/apply", {
        operation: operationInput(planResponse.data.operation),
    }, undefined, false);
    if (!("ok" in applyResponse) || !applyResponse.ok) {
        throw new Error(responseError(applyResponse, window.siyuan.languages.invalid));
    }
};

const summaryHTML = (snapshot: EvergreenReviewSnapshot) => {
    const lang = window.siyuan.languages;
    const summary = snapshot.summary;
    return `<div class="evergreen-review__summary">
<span><strong>${summary.candidate_count}</strong>${escapeHtml(lang.rerankCandidateCount)}</span>
<span><strong>${summary.covered_segments}/${summary.segment_count}</strong>${escapeHtml(lang.blockCount)}</span>
<span class="${summary.unresolved_modules ? "ft__error" : ""}"><strong>${summary.unresolved_modules}</strong>${escapeHtml(lang.agentConfirmPending)}</span>
<span class="evergreen-review__summary-status${snapshot.can_materialize ? " evergreen-review__summary-status--ready" : ""}">
<svg><use xlink:href="#${snapshot.can_materialize ? "iconCheck" : "iconInfo"}"></use></svg>${escapeHtml(lang.cardStatus)}
</span></div>`;
};

const candidatesHTML = (state: ReviewDialogState) => state.snapshot.candidates.map(candidate => {
    const selected = candidate.candidate_id === state.selectedCandidateID;
    const title = candidate.metadata.title || candidate.candidate_id;
    return `<button type="button" class="b3-list-item b3-list-item--narrow evergreen-review__candidate${selected ? " b3-list-item--focus" : ""}" aria-pressed="${selected}" data-candidate-id="${safeAttr(candidate.candidate_id)}"${state.busy ? " disabled" : ""}>
<svg class="b3-list-item__graphic"><use xlink:href="#iconShieldCheck"></use></svg>
<span class="b3-list-item__text evergreen-review__candidate-title" title="${safeAttr(title)}">${escapeHtml(title)}</span>
<span class="b3-chip b3-chip--small evergreen-review__state${candidate.stale ? " b3-chip--error" : ""}">${escapeHtml(candidate.metadata.state)}</span>
</button>`;
}).join("");

const candidateEditorHTML = (state: ReviewDialogState, candidate?: EvergreenReviewCandidate) => {
    const lang = window.siyuan.languages;
    if (!candidate) {
        return `<div class="evergreen-review__empty ft__on-surface">${escapeHtml(lang.empty)}</div>`;
    }
    const terminal = ["discarded", "materialized"].includes(candidate.metadata.state);
    const disabled = state.busy || state.readOnly || terminal;
    const draft = state.drafts.get(candidate.candidate_id);
    const metadata = draft?.metadata || candidate.metadata;
    const descriptor = state.kinds.find(kind => kind.kind === metadata.claim_kind);
    const staleDiff = candidateRefDiff(candidate);
    const diffHTML = staleDiff.length ? `<div class="evergreen-review__diff">
${staleDiff.map(item => `<div>
<code>${escapeHtml(item.segmentID)}</code>
<span title="${safeAttr(item.before)}">${escapeHtml(item.before.slice(0, 12) || lang.empty)}</span>
<svg><use xlink:href="#iconRight"></use></svg>
<span title="${safeAttr(item.after)}">${escapeHtml(item.after.slice(0, 12) || lang.empty)}</span>
</div>`).join("")}</div>` : "";
    return `<div class="evergreen-review__candidate-editor" data-editor-candidate="${safeAttr(candidate.candidate_id)}">
<div class="evergreen-review__editor-header">
<input class="b3-text-field" data-review-field="title" value="${safeAttr(metadata.title || "")}" aria-label="${safeAttr(lang.title)}"${disabled ? " disabled" : ""}>
<span class="b3-chip b3-chip--small evergreen-review__state${candidate.stale ? " b3-chip--error" : ""}">${escapeHtml(candidate.metadata.state)}</span>
</div>
<div class="evergreen-review__metadata">
<label><span>${escapeHtml(lang.type)}</span><select class="b3-select" data-review-field="claim-kind" aria-label="${safeAttr(lang.type)}"${disabled ? " disabled" : ""}>
${!descriptor ? `<option value="${safeAttr(metadata.claim_kind)}">${escapeHtml(lang.evergreenUnavailable)}</option>` : ""}
${state.kinds.map(kind => `<option value="${safeAttr(kind.kind)}"${metadata.claim_kind === kind.kind ? " selected" : ""}>${escapeHtml(kind.view_hints.name)}</option>`).join("")}</select></label>
<label><span>${escapeHtml(lang.type)}</span><input class="b3-text-field" data-review-field="kind-schema" value="${safeAttr(metadata.kind_schema)}" aria-label="${safeAttr(lang.type)}" readonly></label>
<label><span>${escapeHtml(lang.relation)}</span><input class="b3-text-field" data-review-field="relation" value="${safeAttr(metadata.relation || "")}"${disabled ? " disabled" : ""}></label>
<label><code>reason</code><input class="b3-text-field" data-review-field="reason" value="${safeAttr(metadata.reason || "")}" aria-label="${safeAttr(lang.edit)}"${disabled ? " disabled" : ""}></label>
</div>
<textarea class="b3-text-field fn__code evergreen-review__payload" data-review-field="payload" spellcheck="false" aria-label="${safeAttr(lang.edit)}"${disabled ? " disabled" : ""}>${escapeHtml(draft?.payload ?? formatCandidatePayload(candidate.payload))}</textarea>
${descriptor ? `<div class="evergreen-claims__fields" data-review-kind-editor data-rendered-kind="${safeAttr(metadata.claim_kind)}">${schemaFieldsHTML(descriptor.kind_data_schema,
        state.kindDrafts.get(`${candidate.candidate_id}:${metadata.claim_kind}`) || schemaDefaults(descriptor), disabled)}</div>` : ""}
${diffHTML}
<div class="evergreen-review__actions">
<button type="button" class="b3-button b3-button--outline" data-review-action="candidate-save"${disabled ? " disabled" : ""}><svg><use xlink:href="#iconSelect"></use></svg>${escapeHtml(lang.save)}</button>
<button type="button" class="b3-button b3-button--outline" data-review-action="candidate-rebase"${disabled || !candidate.stale ? " disabled" : ""}><svg><use xlink:href="#iconRefresh"></use></svg>${escapeHtml(lang.refresh)}</button>
<button type="button" class="b3-button b3-button--outline" data-review-action="candidate-confirm"${disabled ? " disabled" : ""}><svg><use xlink:href="#iconCheck"></use></svg>${escapeHtml(lang.confirm)}</button>
<button type="button" class="b3-button b3-button--remove" data-review-action="candidate-discard"${disabled ? " disabled" : ""}><svg><use xlink:href="#iconTrashcan"></use></svg>${escapeHtml(lang.remove)}</button>
<span class="fn__flex-1"></span>
<button type="button" class="b3-button" data-review-action="candidate-materialize"${disabled || !descriptor || candidate.stale || !state.snapshot.can_materialize ? " disabled" : ""}><svg><use xlink:href="#iconDatabase"></use></svg>${escapeHtml(lang.database)}</button>
</div></div>`;
};

const coverageHTML = (state: ReviewDialogState) => {
    const lang = window.siyuan.languages;
    return `<section class="evergreen-review__coverage">
<div class="evergreen-review__section-header">
<strong>${escapeHtml(lang.blockCount)}</strong>
<button type="button" class="b3-button b3-button--outline" data-review-action="coverage-save"${state.busy || state.readOnly ? " disabled" : ""}><svg><use xlink:href="#iconSelect"></use></svg>${escapeHtml(lang.save)}</button>
</div>
${(state.coverageDraft || state.snapshot.coverage).map((module, index) => `<div class="evergreen-review__coverage-row" data-coverage-index="${index}">
<code title="${safeAttr(module.segment_refs.join(", "))}">${escapeHtml(module.module_id)}</code>
<span>${module.segment_refs.length} ${escapeHtml(lang.blockCount)}</span>
<select class="b3-select" data-coverage-disposition aria-label="${safeAttr(lang.type)}"${state.busy || state.readOnly ? " disabled" : ""}>
${["candidate", "note_only", "unresolved"].map(value => `<option value="${value}"${module.disposition === value ? " selected" : ""}>${value}</option>`).join("")}
</select>
<input class="b3-text-field" data-coverage-candidates value="${safeAttr((module.candidate_ids || (module.candidate_id ? [module.candidate_id] : [])).join(", "))}" aria-label="${safeAttr(lang.rerankCandidateCount)}"${state.busy || state.readOnly ? " disabled" : ""}>
<input class="b3-text-field" data-coverage-reason value="${safeAttr(module.reason || "")}" aria-label="${safeAttr(lang.edit)}"${state.busy || state.readOnly ? " disabled" : ""}>
</div>`).join("")}</section>`;
};

const renderReviewDialog = (state: ReviewDialogState) => {
    if (state.closed) {
        return;
    }
    if (!state.snapshot.candidates.some(candidate => candidate.candidate_id === state.selectedCandidateID)) {
        state.selectedCandidateID = state.snapshot.candidates[0]?.candidate_id || "";
    }
    const selected = state.snapshot.candidates.find(candidate =>
        candidate.candidate_id === state.selectedCandidateID);
    const diagnostics = state.snapshot.diagnostics || [];
    state.root.innerHTML = `${summaryHTML(state.snapshot)}
<div class="evergreen-review__workspace">
<aside class="b3-list b3-list--background evergreen-review__candidates">${candidatesHTML(state)}</aside>
<main class="evergreen-review__main">
${diagnostics.length ? `<div class="evergreen-review__diagnostics">${diagnostics.map(item =>
        `<div><code>${escapeHtml(item.code)}</code><span>${escapeHtml(item.message)}</span></div>`).join("")}</div>` : ""}
${candidateEditorHTML(state, selected)}
${coverageHTML(state)}
<details class="evergreen-review__segments"><summary>${escapeHtml(window.siyuan.languages.blockCount)} · ${state.snapshot.segments.length}</summary>
${state.snapshot.segments.map(segment => `<div class="evergreen-review__segment">
<code>${escapeHtml(segment.segment_id)}</code><span class="b3-chip b3-chip--small${segment.stored_hash !== segment.current_hash ? " b3-chip--error" : ""}">${escapeHtml(segment.role)}</span>
<span>${escapeHtml(segment.source_ref?.locator?.value || segment.annotation?.kind || "")}</span>
<code class="ft__on-surface">${escapeHtml(segment.current_hash)}</code></div>`).join("")}</details>
</main></div>`;
};

const collectCoverage = (state: ReviewDialogState): EvergreenCoverageModuleInput[] =>
    state.snapshot.coverage.map((module, index) => {
        const row = state.root.querySelector<HTMLElement>(`[data-coverage-index="${index}"]`);
        const disposition = row.querySelector<HTMLSelectElement>("[data-coverage-disposition]").value as
            EvergreenCoverageModuleInput["disposition"];
        const reason = row.querySelector<HTMLInputElement>("[data-coverage-reason]").value.trim();
        const candidateIDs = row.querySelector<HTMLInputElement>("[data-coverage-candidates]").value
            .split(",").map(id => id.trim()).filter(Boolean);
        return {...module, disposition, reason, candidate_id: undefined,
            candidate_ids: disposition === "candidate" ? candidateIDs : []};
    });

const captureReviewDraft = (state: ReviewDialogState) => {
    if (state.closed || state.busy || state.readOnly) {
        return;
    }
    const candidate = state.snapshot.candidates.find(item => item.candidate_id === state.selectedCandidateID);
    if (candidate && !["discarded", "materialized"].includes(candidate.metadata.state)) {
        const field = (name: string) =>
            state.root.querySelector<HTMLInputElement>(`[data-review-field="${name}"]`).value;
        state.drafts.set(candidate.candidate_id, {
            metadata: candidateMetadataFromDraft(candidate, {
                title: field("title"), claimKind: field("claim-kind"), kindSchema: field("kind-schema"),
                relation: field("relation"), reason: field("reason"),
            }),
            payload: field("payload"),
        });
        const kindEditor = state.root.querySelector("[data-review-kind-editor]");
        if (kindEditor) {
            state.kindDrafts.set(`${candidate.candidate_id}:${kindEditor.getAttribute("data-rendered-kind")}`, collectSchemaFields(kindEditor));
        }
    }
    state.coverageDraft = collectCoverage(state);
};

const runDialogOperation = async (
    protyle: IProtyle,
    state: ReviewDialogState,
    operation: () => Promise<void>,
    onApplied?: () => void,
) => {
    const documentID = state.snapshot.document_id;
    state.busy = true;
    renderReviewDialog(state);
    try {
        await operation();
        onApplied?.();
        const snapshot = await fetchReviewSnapshot(documentID);
        if (snapshot) {
            state.snapshot = snapshot;
        }
    } catch (error) {
        const snapshot = await fetchReviewSnapshot(documentID, true);
        if (snapshot) {
            state.snapshot = snapshot;
        }
        const message = error instanceof Error ? error.message : window.siyuan.languages.invalid;
        showMessage(escapeHtml(message).replaceAll("\n", "<br>"), 7000, "error");
    } finally {
        state.busy = false;
        renderReviewDialog(state);
        if (protyle.block.rootID === state.snapshot.document_id) {
            updateReviewTrigger(protyle, state.snapshot);
        } else {
            void syncEvergreenReview(protyle);
        }
    }
};

const bindReviewDialog = (protyle: IProtyle, state: ReviewDialogState) => {
    state.root.addEventListener("change", event => {
        const input = event.target as HTMLSelectElement;
        if (!input.matches("[data-review-field=claim-kind]") || state.busy || state.readOnly) {
            return;
        }
        captureReviewDraft(state);
        const descriptor = state.kinds.find(kind => kind.kind === input.value);
        const draft = state.drafts.get(state.selectedCandidateID);
        if (descriptor && draft) {
            draft.metadata.kind_schema = descriptor.schema;
        }
        renderReviewDialog(state);
    });
    state.root.addEventListener("click", event => {
        const target = event.target as Element;
        if (state.closed || state.busy) {
            return;
        }
        const candidateButton = target.closest<HTMLElement>("[data-candidate-id]");
        if (candidateButton) {
            captureReviewDraft(state);
            state.selectedCandidateID = candidateButton.dataset.candidateId;
            renderReviewDialog(state);
            return;
        }
        const action = target.closest<HTMLElement>("[data-review-action]")?.dataset.reviewAction;
        if (!action || state.readOnly || target.closest("button:disabled")) {
            return;
        }
        captureReviewDraft(state);
        const candidate = state.snapshot.candidates.find(item =>
            item.candidate_id === state.selectedCandidateID);
        if (action === "coverage-save") {
            const coverage = collectCoverage(state);
            void runDialogOperation(protyle, state, () => applyReviewCommand(state.snapshot, {
                action: "coverage.update",
                coverage,
            }), () => {
                state.coverageDraft = undefined;
            });
            return;
        }
        if (!candidate) {
            return;
        }
        if (action === "candidate-save") {
            const field = (name: string) =>
                state.root.querySelector<HTMLInputElement>(`[data-review-field="${name}"]`).value;
            let payload: JSONValue[];
            try {
                payload = parseCandidatePayload(
                    state.root.querySelector<HTMLTextAreaElement>('[data-review-field="payload"]').value);
            } catch {
                showMessage(escapeHtml(window.siyuan.languages.invalid), 7000, "error");
                return;
            }
            const metadata = candidateMetadataFromDraft(candidate, {
                title: field("title"),
                claimKind: field("claim-kind"),
                kindSchema: field("kind-schema"),
                relation: field("relation"),
                reason: field("reason"),
            });
            void runDialogOperation(protyle, state, () => applyReviewCommand(state.snapshot, {
                action: "candidate.update",
                candidate_id: candidate.candidate_id,
                metadata,
                payload,
            }), () => {
                state.drafts.delete(candidate.candidate_id);
            });
            return;
        }
        if (action === "candidate-materialize") {
            if (state.drafts.has(candidate.candidate_id)) {
                const metadata = state.drafts.get(candidate.candidate_id).metadata;
                if (metadata.claim_kind !== candidate.metadata.claim_kind ||
                    metadata.kind_schema !== candidate.metadata.kind_schema) {
                    showMessage(escapeHtml(window.siyuan.languages.save), 7000, "error");
                    return;
                }
            }
            let kindData: Record<string, JSONValue>;
            try {
                validateSchemaFields(state.root.querySelector("[data-review-kind-editor]"));
                kindData = collectSchemaFields(state.root.querySelector("[data-review-kind-editor]"));
            } catch (error) {
                showMessage(escapeHtml(String(error)), 7000, "error");
                return;
            }
            void runDialogOperation(protyle, state,
                () => materializeCandidate(state.notebookID, state.snapshot, candidate, kindData));
            return;
        }
        const commandByAction: Record<string, EvergreenReviewCommandInput["action"]> = {
            "candidate-rebase": "candidate.rebase",
            "candidate-confirm": "candidate.confirm",
            "candidate-discard": "candidate.discard",
        };
        const command = commandByAction[action];
        if (command) {
            void runDialogOperation(protyle, state, () => applyReviewCommand(state.snapshot, {
                action: command,
                candidate_id: candidate.candidate_id,
            }));
        }
    });
};

const openReviewDialog = async (protyle: IProtyle) => {
    const documentID = protyle.block.rootID;
    if (!documentID) {
        return;
    }
    const snapshot = await fetchReviewSnapshot(documentID);
    if (!snapshot || protyle.block.rootID !== documentID) {
        return;
    }
    const catalog = await fetch("/api/evergreen/v1/schemas", {cache: "no-store"}).then(response => response.json());
    if (!catalog.ok || protyle.block.rootID !== documentID) {
        return;
    }
    const root = document.createElement("div");
    root.className = "evergreen-review";
    const state: ReviewDialogState = {
        root,
        snapshot,
        selectedCandidateID: snapshot.candidates[0]?.candidate_id || "",
        busy: false,
        closed: false,
        readOnly: protyle.disabled || window.siyuan.config.readonly,
        notebookID: protyle.notebookId,
        kinds: catalog.data.kinds,
        kindDrafts: new Map(),
        drafts: new Map(),
    };
    const dialog = new Dialog({
        title: window.siyuan.languages.configGroupReview,
        width: isMobile() ? "96vw" : "min(1040px, 94vw)",
        height: isMobile() ? "88vh" : "min(780px, 88vh)",
        containerClassName: "evergreen-review-dialog",
        content: root.outerHTML,
        destroyCallback: () => {
            state.closed = true;
        },
    });
    state.root = dialog.element.querySelector<HTMLElement>(".evergreen-review");
    bindReviewDialog(protyle, state);
    renderReviewDialog(state);
};

const updateReviewTrigger = (protyle: IProtyle, snapshot?: EvergreenReviewSnapshot) => {
    const plugin = protyle.breadcrumb?.element.parentElement?.querySelector<HTMLElement>(".protyle-breadcrumb__plugin");
    if (!plugin) {
        return;
    }
    let button = plugin.querySelector<HTMLButtonElement>("[data-type=evergreen-review]");
    if (!snapshot) {
        button?.remove();
        return;
    }
    if (!button) {
        button = document.createElement("button");
        button.type = "button";
        button.className = "block__icon fn__flex-center ariaLabel evergreen-review__trigger";
        button.dataset.type = "evergreen-review";
        button.innerHTML = '<svg><use xlink:href="#iconShieldCheck"></use></svg><span></span>';
        button.addEventListener("click", event => {
            event.stopPropagation();
            void openReviewDialog(protyle).catch(error => {
                showMessage(escapeHtml(error instanceof Error ? error.message : window.siyuan.languages.invalid),
                    7000, "error");
            });
        });
        plugin.prepend(button);
    }
    button.setAttribute("aria-label", window.siyuan.languages.configGroupReview);
    button.dataset.documentId = snapshot.document_id;
    button.classList.toggle("evergreen-review__trigger--blocked", !snapshot.can_materialize);
    button.querySelector("span").textContent = snapshot.summary.candidate_count.toString();
};

export const syncEvergreenReview = async (protyle: IProtyle) => {
    const documentID = protyle.block?.rootID;
    if (!documentID) {
        updateReviewTrigger(protyle);
        return;
    }
    const version = (reviewRequestVersions.get(protyle) || 0) + 1;
    reviewRequestVersions.set(protyle, version);
    const button = protyle.breadcrumb?.element.parentElement?.querySelector<HTMLElement>("[data-type=evergreen-review]");
    if (button?.dataset.documentId !== documentID) {
        button?.remove();
    }
    const snapshot = await fetchReviewSnapshot(documentID, true);
    if (reviewRequestVersions.get(protyle) !== version || protyle.block.rootID !== documentID) {
        return;
    }
    updateReviewTrigger(protyle, snapshot);
};
