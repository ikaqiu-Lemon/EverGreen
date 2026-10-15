import {Dialog} from "../../dialog";
import {showMessage} from "../../dialog/message";
import type {
    EvergreenKindSchema,
    EvergreenNoteCreateCandidateInput,
    EvergreenNoteCreateRequestInput,
    EvergreenNoteCreateSegmentInput,
    EvergreenPlannedOperation,
    EvergreenSourceCaptureRequestInput,
    JSONValue,
} from "../../types/api";
import {escapeAttr, escapeHtml} from "../../util/escape";
import {fetchSyncPost} from "../../util/fetch";
import {isMobile} from "../../util/functions";
import {openFileById} from "../../editor/util";
import {openMobileFileById} from "../../mobile/editor";

type SourceDraft = {
    url: string;
    title: string;
    reason: string;
    domain: string;
    tags: string;
    body: string;
};

type SegmentDraft = {
    id: string;
    role: "note_segment" | "agent_annotation";
    locator: string;
    annotation: string;
    label: string;
    body: string;
    disposition: "candidate" | "note_only" | "unresolved";
    coverageReason: string;
};

type CandidateDraft = {
    id: string;
    kind: string;
    title: string;
    refs: string;
    relation: "support" | "against" | "context";
    reason: string;
    tags: string;
    body: string;
};

type IngestState = {
    root: HTMLElement;
    notebookID: string;
    source: SourceDraft;
    sourceID: string;
    noteTitle: string;
    segments: SegmentDraft[];
    candidates: CandidateDraft[];
    kinds: EvergreenKindSchema[];
    active: "source" | "note";
    busy: boolean;
    closed: boolean;
    readOnly: boolean;
    error: string;
    openDocument?: (id: string) => void;
};

const safeAttr = (value: string) => escapeAttr(escapeHtml(value || ""));
const splitValues = (value: string) => value.split(",").map(item => item.trim()).filter(Boolean);
const newLogicalID = (prefix: string, documentID: string) => `${prefix}-${documentID}`;

const responseError = (response: unknown) => {
    if (response && typeof response === "object") {
        if ("diagnostics" in response && Array.isArray(response.diagnostics) && response.diagnostics.length) {
            return response.diagnostics.map(item =>
                item && typeof item === "object" && "message" in item ? String(item.message) : "")
                .filter(Boolean).join("\n");
        }
        if ("msg" in response && typeof response.msg === "string" && response.msg) {
            return response.msg;
        }
    }
    return window.siyuan.languages.invalid;
};

const applyOperation = async (operation: EvergreenPlannedOperation) => {
    const response = await fetchSyncPost("/api/evergreen/v1/apply", {operation}, undefined, false);
    if (!("ok" in response) || !response.ok) {
        throw new Error(responseError(response));
    }
};

const nativeParagraphs = (body: string): JSONValue[] =>
    body.split(/\n\s*\n/).map(item => item.trim()).filter(Boolean).map(text => ({
        ID: Lute.NewNodeID(), Type: "NodeParagraph", Data: text,
    }));

const newSegment = (): SegmentDraft => ({
    id: `seg-${Lute.NewNodeID()}`,
    role: "note_segment",
    locator: "L1-L1",
    annotation: "context",
    label: "",
    body: "",
    disposition: "candidate",
    coverageReason: "",
});

const newCandidate = (segmentID: string, kind: string): CandidateDraft => ({
    id: `cand-${Lute.NewNodeID()}`,
    kind,
    title: "",
    refs: segmentID,
    relation: "support",
    reason: "",
    tags: "",
    body: "",
});

const fieldHTML = (
    label: string,
    name: string,
    value: string,
    disabled: boolean,
    options?: string[],
) => `<label class="evergreen-ingest__field"><span>${escapeHtml(label)}</span>${
    options ? `<select class="b3-select" data-ingest-field="${safeAttr(name)}"${disabled ? " disabled" : ""}>${
        options.map(option => `<option value="${safeAttr(option)}"${option === value ? " selected" : ""}>${escapeHtml(option)}</option>`).join("")
    }</select>` : `<input class="b3-text-field" data-ingest-field="${safeAttr(name)}" value="${safeAttr(value)}"${disabled ? " disabled" : ""}>`
}</label>`;

const sourceHTML = (state: IngestState) => {
    const lang = window.siyuan.languages;
    const disabled = state.busy || state.readOnly;
    return `<form class="evergreen-ingest__form" data-ingest-form="source">
<div class="evergreen-ingest__grid">
${fieldHTML("URL", "source-url", state.source.url, disabled)}
${fieldHTML(lang.title, "source-title", state.source.title, disabled)}
${fieldHTML(lang.memo, "source-reason", state.source.reason, disabled)}
${fieldHTML("Domain", "source-domain", state.source.domain, disabled)}
${fieldHTML(lang.tag, "source-tags", state.source.tags, disabled)}
</div>
<label class="evergreen-ingest__field"><span>${escapeHtml(lang.newContentFile)}</span>
<textarea class="b3-text-field" data-ingest-field="source-body" rows="12"${disabled ? " disabled" : ""}>${escapeHtml(state.source.body)}</textarea></label>
<div class="evergreen-ingest__actions"><span class="fn__flex-1"></span>
<button type="submit" class="b3-button" data-ingest-action="capture-source"${disabled ? " disabled" : ""}>
<svg><use xlink:href="#iconInbox"></use></svg>${escapeHtml(lang.save)}</button></div>
</form>`;
};

const segmentHTML = (state: IngestState, segment: SegmentDraft, index: number) => {
    const disabled = state.busy || state.readOnly;
    return `<section class="evergreen-ingest__row" data-segment-row="${index}">
<div class="evergreen-ingest__row-header"><strong>${escapeHtml(segment.id)}</strong><span class="fn__flex-1"></span>
<button type="button" class="block__icon ariaLabel" data-ingest-action="remove-segment" data-index="${index}"
 aria-label="${escapeAttr(window.siyuan.languages.remove)}"${disabled || state.segments.length === 1 ? " disabled" : ""}>
<svg><use xlink:href="#iconTrashcan"></use></svg></button></div>
<div class="evergreen-ingest__grid">
${fieldHTML(window.siyuan.languages.type, "segment-role", segment.role, disabled, ["note_segment", "agent_annotation"])}
${fieldHTML("Locator", "segment-locator", segment.locator, disabled || segment.role !== "note_segment")}
${fieldHTML("Annotation", "segment-annotation", segment.annotation, disabled || segment.role !== "agent_annotation")}
${fieldHTML(window.siyuan.languages.title, "segment-label", segment.label, disabled || segment.role !== "agent_annotation")}
${fieldHTML("Coverage", "segment-disposition", segment.disposition, disabled, ["candidate", "note_only", "unresolved"])}
${fieldHTML(window.siyuan.languages.memo, "segment-coverage-reason", segment.coverageReason,
        disabled || segment.disposition === "candidate")}
</div>
<label class="evergreen-ingest__field"><span>${escapeHtml(window.siyuan.languages.newContentFile)}</span>
<textarea class="b3-text-field" data-ingest-field="segment-body" rows="4"${disabled ? " disabled" : ""}>${escapeHtml(segment.body)}</textarea></label>
</section>`;
};

const candidateHTML = (state: IngestState, candidate: CandidateDraft, index: number) => {
    const disabled = state.busy || state.readOnly;
    return `<section class="evergreen-ingest__row" data-candidate-row="${index}">
<div class="evergreen-ingest__row-header"><strong>${escapeHtml(candidate.id)}</strong><span class="fn__flex-1"></span>
<button type="button" class="block__icon ariaLabel" data-ingest-action="remove-candidate" data-index="${index}"
 aria-label="${escapeAttr(window.siyuan.languages.remove)}"${disabled || state.candidates.length === 1 ? " disabled" : ""}>
<svg><use xlink:href="#iconTrashcan"></use></svg></button></div>
<div class="evergreen-ingest__grid">
${fieldHTML(window.siyuan.languages.type, "candidate-kind", candidate.kind, disabled,
        state.kinds.map(kind => kind.kind))}
${fieldHTML(window.siyuan.languages.title, "candidate-title", candidate.title, disabled)}
${fieldHTML(window.siyuan.languages.blockCount, "candidate-refs", candidate.refs, disabled)}
${fieldHTML(window.siyuan.languages.relation, "candidate-relation", candidate.relation, disabled,
        ["support", "against", "context"])}
${fieldHTML(window.siyuan.languages.memo, "candidate-reason", candidate.reason, disabled)}
${fieldHTML(window.siyuan.languages.tag, "candidate-tags", candidate.tags, disabled)}
</div>
<label class="evergreen-ingest__field"><span>${escapeHtml(window.siyuan.languages.newContentFile)}</span>
<textarea class="b3-text-field" data-ingest-field="candidate-body" rows="4"${disabled ? " disabled" : ""}>${escapeHtml(candidate.body)}</textarea></label>
</section>`;
};

const noteHTML = (state: IngestState) => {
    const disabled = state.busy || state.readOnly;
    return `<form class="evergreen-ingest__form" data-ingest-form="note">
<div class="evergreen-ingest__grid">
${fieldHTML(`${window.siyuan.languages.evergreenSource} ID`, "note-source", state.sourceID, disabled)}
${fieldHTML(window.siyuan.languages.title, "note-title", state.noteTitle, disabled)}
</div>
<div class="evergreen-ingest__section-header"><strong>Segments</strong><span class="fn__flex-1"></span>
<button type="button" class="block__icon ariaLabel" data-ingest-action="add-segment"
 aria-label="${escapeAttr(window.siyuan.languages.addAttr)}"${disabled ? " disabled" : ""}>
<svg><use xlink:href="#iconAdd"></use></svg></button></div>
${state.segments.map((segment, index) => segmentHTML(state, segment, index)).join("")}
<div class="evergreen-ingest__section-header"><strong>Candidates</strong><span class="fn__flex-1"></span>
<button type="button" class="block__icon ariaLabel" data-ingest-action="add-candidate"
 aria-label="${escapeAttr(window.siyuan.languages.addAttr)}"${disabled ? " disabled" : ""}>
<svg><use xlink:href="#iconAdd"></use></svg></button></div>
${state.candidates.map((candidate, index) => candidateHTML(state, candidate, index)).join("")}
<div class="evergreen-ingest__actions"><span class="fn__flex-1"></span>
<button type="submit" class="b3-button" data-ingest-action="create-note"${disabled ? " disabled" : ""}>
<svg><use xlink:href="#iconSelect"></use></svg>${escapeHtml(window.siyuan.languages.save)}</button></div>
</form>`;
};

const render = (state: IngestState) => {
    if (state.closed) {
        return;
    }
    const lang = window.siyuan.languages;
    state.root.innerHTML = `<div class="evergreen-ingest__tabs">
<button type="button" class="b3-button ${state.active === "source" ? "b3-button--text" : "b3-button--outline"}"
 data-ingest-tab="source"${state.busy ? " disabled" : ""}>${escapeHtml(lang.evergreenSource)}</button>
<button type="button" class="b3-button ${state.active === "note" ? "b3-button--text" : "b3-button--outline"}"
 data-ingest-tab="note"${state.busy ? " disabled" : ""}>${escapeHtml(lang.configGroupReview)}</button>
</div>
${state.error ? `<div class="evergreen-ingest__error ft__error" role="alert">${escapeHtml(state.error)}</div>` : ""}
<div class="evergreen-ingest__content">${state.active === "source" ? sourceHTML(state) : noteHTML(state)}</div>`;
};

const value = (root: ParentNode, name: string) =>
    root.querySelector<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(
        `[data-ingest-field="${name}"]`)?.value.trim() || "";

const captureDraft = (state: IngestState) => {
    const sourceForm = state.root.querySelector("[data-ingest-form=source]");
    if (sourceForm) {
        state.source = {
            url: value(sourceForm, "source-url"),
            title: value(sourceForm, "source-title"),
            reason: value(sourceForm, "source-reason"),
            domain: value(sourceForm, "source-domain"),
            tags: value(sourceForm, "source-tags"),
            body: value(sourceForm, "source-body"),
        };
    }
    const noteForm = state.root.querySelector("[data-ingest-form=note]");
    if (!noteForm) {
        return;
    }
    state.sourceID = value(noteForm, "note-source");
    state.noteTitle = value(noteForm, "note-title");
    state.segments = Array.from(noteForm.querySelectorAll<HTMLElement>("[data-segment-row]")).map((row, index) => ({
        id: state.segments[index].id,
        role: value(row, "segment-role") as SegmentDraft["role"],
        locator: value(row, "segment-locator"),
        annotation: value(row, "segment-annotation"),
        label: value(row, "segment-label"),
        body: value(row, "segment-body"),
        disposition: value(row, "segment-disposition") as SegmentDraft["disposition"],
        coverageReason: value(row, "segment-coverage-reason"),
    }));
    state.candidates = Array.from(noteForm.querySelectorAll<HTMLElement>("[data-candidate-row]")).map((row, index) => ({
        id: state.candidates[index].id,
        kind: value(row, "candidate-kind"),
        title: value(row, "candidate-title"),
        refs: value(row, "candidate-refs"),
        relation: value(row, "candidate-relation") as CandidateDraft["relation"],
        reason: value(row, "candidate-reason"),
        tags: value(row, "candidate-tags"),
        body: value(row, "candidate-body"),
    }));
};

const runOperation = async (state: IngestState, operation: () => Promise<void>) => {
    if (state.busy || state.closed || state.readOnly) {
        return;
    }
    captureDraft(state);
    state.busy = true;
    state.error = "";
    render(state);
    try {
        await operation();
    } catch (error) {
        state.error = error instanceof Error ? error.message : String(error);
        showMessage(escapeHtml(state.error).replaceAll("\n", "<br>"), 7000, "error");
    } finally {
        state.busy = false;
        render(state);
    }
};

const captureSource = async (state: IngestState) => {
    if ((!state.source.url && !state.source.title) || !state.source.body || !state.source.reason) {
        throw new Error(window.siyuan.languages.invalid);
    }
    const documentID = Lute.NewNodeID();
    const request: EvergreenSourceCaptureRequestInput = {
        operation_id: `source-ui-${Lute.NewNodeID()}`,
        logical_id: newLogicalID("s", documentID),
        document_id: documentID,
        path: `${state.notebookID}/${documentID}.sy`,
        url: state.source.url,
        title: state.source.title,
        body: state.source.body,
        reason: state.source.reason,
        domain: state.source.domain,
        tags: splitValues(state.source.tags),
        captured_at: new Date().toISOString(),
    };
    const response = await fetchSyncPost(
        "/api/evergreen/v1/sources/plan-capture", request, undefined, false);
    if (!("ok" in response) || !response.ok) {
        throw new Error(responseError(response));
    }
    if (response.data.operation) {
        await applyOperation(response.data.operation);
    }
    state.sourceID = response.data.source_id;
    state.noteTitle ||= state.source.title;
    state.active = "note";
};

const createNote = async (state: IngestState) => {
    if (!state.sourceID || !state.noteTitle || !state.segments.length) {
        throw new Error(window.siyuan.languages.invalid);
    }
    const segments: EvergreenNoteCreateSegmentInput[] = state.segments.map(segment => {
        const content = nativeParagraphs(segment.body);
        if (!content.length) {
            throw new Error(window.siyuan.languages.invalid);
        }
        return {
            segment_id: segment.id,
            role: segment.role,
            content,
            ...(segment.role === "note_segment" ?
                {locator: {kind: "source-lines", value: segment.locator}} :
                {annotation: {kind: segment.annotation, label: segment.label}}),
        };
    });
    const candidates: EvergreenNoteCreateCandidateInput[] = state.candidates.map(candidate => {
        const payload = nativeParagraphs(candidate.body);
        const refs = splitValues(candidate.refs);
        if (!candidate.kind || !candidate.title || !candidate.reason || !payload.length || !refs.length) {
            throw new Error(window.siyuan.languages.invalid);
        }
        return {
            candidate_id: candidate.id,
            claim_kind: candidate.kind,
            title: candidate.title,
            segment_refs: refs,
            payload,
            relation: candidate.relation,
            reason: candidate.reason,
            tags: splitValues(candidate.tags),
        };
    });
    const coverage = state.segments.map((segment, index) => {
        const candidateIDs = state.candidates.filter(candidate =>
            splitValues(candidate.refs).includes(segment.id)).map(candidate => candidate.id);
        if (segment.disposition === "candidate" && !candidateIDs.length) {
            throw new Error(window.siyuan.languages.invalid);
        }
        if (segment.disposition !== "candidate" && !segment.coverageReason) {
            throw new Error(window.siyuan.languages.invalid);
        }
        return {
            module_id: `coverage-${index + 1}-${segment.id}`,
            disposition: segment.disposition,
            segment_refs: [segment.id],
            candidate_ids: segment.disposition === "candidate" ? candidateIDs : [],
            reason: segment.disposition === "candidate" ? undefined : segment.coverageReason,
        };
    });
    const documentID = Lute.NewNodeID();
    const request: EvergreenNoteCreateRequestInput = {
        operation_id: `note-ui-${Lute.NewNodeID()}`,
        logical_id: newLogicalID("n", documentID),
        document_id: documentID,
        path: `${state.notebookID}/${documentID}.sy`,
        source_id: state.sourceID,
        title: state.noteTitle,
        segments,
        candidates,
        coverage,
    };
    const response = await fetchSyncPost(
        "/api/evergreen/v1/notes/plan-create", request, undefined, false);
    if (!("ok" in response) || !response.ok) {
        throw new Error(responseError(response));
    }
    await applyOperation(response.data.operation);
    state.openDocument?.(documentID);
};

const bind = (state: IngestState) => {
    state.root.addEventListener("submit", event => {
        event.preventDefault();
        const form = event.target as HTMLElement;
        if (form.dataset.ingestForm === "source") {
            void runOperation(state, () => captureSource(state));
        } else if (form.dataset.ingestForm === "note") {
            void runOperation(state, () => createNote(state));
        }
    });
    state.root.addEventListener("change", event => {
        if (state.busy || state.readOnly) {
            return;
        }
        const target = event.target as HTMLElement;
        if (target.matches("[data-ingest-field=segment-role], [data-ingest-field=segment-disposition]")) {
            captureDraft(state);
            render(state);
        }
    });
    state.root.addEventListener("click", event => {
        const target = (event.target as Element).closest<HTMLButtonElement>("button");
        if (!target || target.disabled || state.busy || state.closed) {
            return;
        }
        if (target.dataset.ingestTab) {
            captureDraft(state);
            state.active = target.dataset.ingestTab as IngestState["active"];
            render(state);
            return;
        }
        const action = target.dataset.ingestAction;
        if (!action || action === "capture-source" || action === "create-note" || state.readOnly) {
            return;
        }
        captureDraft(state);
        const index = Number(target.dataset.index);
        if (action === "add-segment") {
            state.segments.push(newSegment());
        } else if (action === "remove-segment" && state.segments.length > 1) {
            state.segments.splice(index, 1);
        } else if (action === "add-candidate") {
            state.candidates.push(newCandidate(state.segments[0]?.id || "", state.kinds[0]?.kind || ""));
        } else if (action === "remove-candidate" && state.candidates.length > 1) {
            state.candidates.splice(index, 1);
        }
        render(state);
    });
};

export const openEvergreenIngest = async (options: {
    notebookID: string;
    readOnly?: boolean;
    openDocument?: (id: string) => void;
}) => {
    const catalog = await fetch("/api/evergreen/v1/schemas", {cache: "no-store"}).then(response => response.json());
    if (!catalog.ok || !Array.isArray(catalog.data?.kinds) || !catalog.data.kinds.length) {
        throw new Error(responseError(catalog));
    }
    const segment = newSegment();
    const state: IngestState = {
        root: document.createElement("div"),
        notebookID: options.notebookID,
        source: {url: "", title: "", reason: "", domain: "", tags: "", body: ""},
        sourceID: "",
        noteTitle: "",
        segments: [segment],
        candidates: [newCandidate(segment.id, catalog.data.kinds[0].kind)],
        kinds: catalog.data.kinds,
        active: "source",
        busy: false,
        closed: false,
        readOnly: !!options.readOnly || window.siyuan.config.readonly,
        error: "",
        openDocument: options.openDocument,
    };
    const dialog = new Dialog({
        title: `${window.siyuan.languages.evergreenSource} / ${window.siyuan.languages.configGroupReview}`,
        width: isMobile() ? "96vw" : "min(960px, 92vw)",
        height: isMobile() ? "88vh" : "min(780px, 88vh)",
        containerClassName: "evergreen-ingest-dialog",
        content: '<div class="evergreen-ingest"></div>',
        destroyCallback: () => {
            state.closed = true;
        },
    });
    state.root = dialog.element.querySelector<HTMLElement>(".evergreen-ingest");
    bind(state);
    render(state);
    return dialog;
};

export const syncEvergreenIngest = (protyle: IProtyle) => {
    const plugin = protyle.breadcrumb?.element.parentElement?.querySelector<HTMLElement>(
        ".protyle-breadcrumb__plugin");
    if (!plugin || !protyle.notebookId) {
        return;
    }
    let button = plugin.querySelector<HTMLButtonElement>("[data-type=evergreen-ingest]");
    if (!button) {
        button = document.createElement("button");
        button.type = "button";
        button.className = "block__icon fn__flex-center ariaLabel";
        button.dataset.type = "evergreen-ingest";
        button.innerHTML = '<svg><use xlink:href="#iconAdd"></use></svg>';
        button.addEventListener("click", event => {
            event.stopPropagation();
            void openEvergreenIngest({
                notebookID: protyle.notebookId,
                readOnly: protyle.disabled,
                openDocument: id => {
                    if (isMobile()) {
                        openMobileFileById(protyle.app, id);
                    } else {
                        void openFileById({app: protyle.app, id});
                    }
                },
            }).catch(error => showMessage(escapeHtml(String(error)), 7000, "error"));
        });
        plugin.prepend(button);
    }
    button.disabled = protyle.disabled || window.siyuan.config.readonly;
    button.setAttribute("aria-label", `${window.siyuan.languages.evergreenSource} / ${window.siyuan.languages.configGroupReview}`);
};
