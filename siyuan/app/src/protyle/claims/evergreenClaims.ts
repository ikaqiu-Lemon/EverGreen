import {Dialog} from "../../dialog";
import {showMessage} from "../../dialog/message";
import type {
    EvergreenAVPage, EvergreenAVRowRef, EvergreenBulkCapabilities, EvergreenClaimEditInput,
    EvergreenClaimSnapshot, EvergreenKindSchema, EvergreenPlannedOperation, EvergreenTypedEdge, JSONValue,
} from "../../types/api";
import {escapeHtml} from "../../util/escape";
import {fetchSyncPost} from "../../util/fetch";
import {isMobile} from "../../util/functions";
import {collectSchemaFields, safeAttr, schemaDefaults, schemaFieldsHTML, validateSchemaFields} from "./schemaEditor";

type Draft = {
    base: string;
    status: string;
    tags: string;
    data: Record<string, JSONValue>;
    provenance: NonNullable<EvergreenClaimEditInput["provenance"]>;
    title: string;
    body: string;
};

type ClaimsState = {
    root: HTMLElement;
    kinds: EvergreenKindSchema[];
    page: EvergreenAVPage;
    viewID: string;
    selectedID: string;
    snapshot?: EvergreenClaimSnapshot;
    drafts: Map<string, Draft>;
    checked: Map<string, EvergreenAVRowRef>;
    capabilities?: EvergreenBulkCapabilities;
    creating?: string;
    busy: boolean;
    closed: boolean;
    readOnly: boolean;
    notebookID: string;
    error: string;
    openDocument?: (id: string) => void;
};

export const evergreenError = (response: unknown) => {
    if (response && typeof response === "object") {
        if ("diagnostics" in response && Array.isArray(response.diagnostics)) {
            const messages = response.diagnostics.map(item =>
                item && typeof item === "object" && "message" in item ?
                    `${"code" in item ? String(item.code) + ": " : ""}${"path" in item && item.path ? String(item.path) + ": " : ""}${String(item.message)}` : "").filter(Boolean);
            if (messages.length) {
                return messages.join("\n");
            }
        }
        if ("msg" in response && typeof response.msg === "string" && response.msg) {
            return response.msg;
        }
    }
    return window.siyuan.languages.invalid;
};

export const applyEvergreenOperation = async (operation: EvergreenPlannedOperation) => {
    if (!operation.images || !operation.plan.base || !operation.plan.writes) {
        throw new Error(window.siyuan.languages.invalid);
    }
    const response = await fetchSyncPost("/api/evergreen/v1/apply", {
        operation: {...operation, images: operation.images,
            plan: {...operation.plan, base: operation.plan.base, writes: operation.plan.writes}},
    }, undefined, false);
    if (!("ok" in response) || !response.ok) {
        throw new Error(evergreenError(response));
    }
};

const splitValues = (value: string) => value.split(",").map(item => item.trim()).filter(Boolean);

const loadPage = async (state: ClaimsState) => {
    const response = await fetchSyncPost("/api/evergreen/v1/av/query", {
        view_id: state.viewID, query: {limit: 500, sorts: [{column: "title", direction: "asc"}]},
    }, undefined, false);
    if (!("ok" in response) || !response.ok) {
        throw new Error(evergreenError(response));
    }
    state.page = response.data;
};

const loadClaim = async (state: ClaimsState) => {
    state.snapshot = undefined;
    if (!state.selectedID || state.creating) {
        return;
    }
    const response = await fetchSyncPost("/api/evergreen/v1/claims/get", {
        logical_id: state.selectedID,
    }, undefined, false);
    if (!("ok" in response) || !response.ok) {
        throw new Error(evergreenError(response));
    }
    state.snapshot = response.data;
};

const currentKind = (state: ClaimsState) => state.kinds.find(kind =>
    kind.kind === (state.creating || state.snapshot?.envelope.claim.claim_kind));

const defaultDraft = (state: ClaimsState): Draft => {
    const snapshot = state.snapshot;
    const descriptor = currentKind(state);
    return {base: snapshot?.base || "", status: snapshot?.envelope.claim.status || descriptor?.default_status || "",
        tags: (snapshot?.envelope.claim.tags || []).join(", "),
        data: snapshot?.envelope.claim.kind_data || (descriptor ? schemaDefaults(descriptor) : {}),
        provenance: JSON.parse(JSON.stringify(snapshot?.envelope.provenance || [])), title: snapshot?.title || "", body: ""};
};

const draftKey = (state: ClaimsState) => state.creating ? `new:${state.creating}` : state.selectedID;
const draftFor = (state: ClaimsState) => state.drafts.get(draftKey(state)) || defaultDraft(state);
const disabledAttr = (state: ClaimsState) => state.busy || state.readOnly || state.snapshot?.read_only ||
    !currentKind(state) ? " disabled" : "";

const provenanceHTML = (draft: Draft, disabled: string) => {
    const lang = window.siyuan.languages;
    return `<section><div class="evergreen-claims__toolbar"><strong>${escapeHtml(lang.evergreenSource)}</strong>
<button type="button" class="b3-button b3-button--text" data-claim-action="add-source"${disabled}>${escapeHtml(lang.addAttr)}</button></div>
${draft.provenance.map((item, index) => `<div class="evergreen-claims__source" data-source-index="${index}">
<label class="evergreen-claims__field"><span>${escapeHtml(lang.evergreenSource)} ID</span><input class="b3-text-field" data-source-field="source_id" value="${safeAttr(item.source_id)}"${disabled}></label>
<label class="evergreen-claims__field"><span>${escapeHtml(lang.doc)} ID</span><input class="b3-text-field" data-source-field="note_id" value="${safeAttr(item.note_id)}"${disabled}></label>
<label class="evergreen-claims__field"><span>${escapeHtml(lang.blockCount)}</span><input class="b3-text-field" data-source-field="segments" value="${safeAttr(item.segment_refs.join(", "))}"${disabled}></label>
<label class="evergreen-claims__field"><span>${escapeHtml(lang.memo)}</span><textarea class="b3-text-field" data-source-field="reason" rows="2"${disabled}>${escapeHtml(item.reason)}</textarea></label>
<button type="button" class="b3-button b3-button--remove" data-claim-action="remove-source" data-source-index="${index}"${disabled}>${escapeHtml(lang.remove)}</button>
</div>`).join("")}</section>`;
};

const relationsHTML = (state: ClaimsState, disabled: string) => {
    if (!state.snapshot) {
        return "";
    }
    const lang = window.siyuan.languages;
    const edges = state.snapshot.envelope.relations.outgoing;
    return `<section><strong>${escapeHtml(lang.relation)}</strong>
${edges.map(edge => `<div class="evergreen-claims__relation">
<span class="b3-chip b3-chip--small">${escapeHtml(edge.type)}</span>
<button type="button" class="b3-button b3-button--text evergreen-claims__target" data-target-id="${safeAttr(edge.target.logical_id)}"><span>${escapeHtml(edge.target.logical_id)}</span></button>
<span class="evergreen-claims__reason">${escapeHtml(edge.reason)}</span>
<button type="button" class="b3-button b3-button--text" data-claim-action="edit-edge" data-edge-id="${safeAttr(edge.edge_id)}"${disabled}>${escapeHtml(lang.edit)}</button>
<button type="button" class="b3-button b3-button--remove" data-claim-action="delete-edge" data-edge-id="${safeAttr(edge.edge_id)}"${disabled}>${escapeHtml(lang.remove)}</button>
</div>`).join("")}
${disabled ? "" : `<details class="evergreen-claims__edge-form"><summary>${escapeHtml(lang.addAttr)}</summary>
<div class="evergreen-claims__fields">
<label class="evergreen-claims__field"><span>${escapeHtml(lang.relation)}</span><select class="b3-select" data-edge-field="schema">
${(currentKind(state)?.allowed_edge_schemas || []).map(schema => `<option value="${safeAttr(schema)}">${escapeHtml(schema)}</option>`).join("")}
</select></label>
<label class="evergreen-claims__field"><span>${escapeHtml(lang.type)}</span><select class="b3-select" data-edge-field="type"></select></label>
<label class="evergreen-claims__field"><span>${escapeHtml(lang.link)}</span><input class="b3-text-field" data-edge-field="target" list="evergreen-target-${safeAttr(state.selectedID)}"></label>
<label class="evergreen-claims__field"><span>${escapeHtml(lang.memo)}</span><input class="b3-text-field" data-edge-field="reason"></label>
<label class="evergreen-claims__field"><span>${escapeHtml(lang.doc)} ID</span><input class="b3-text-field" data-edge-field="note"></label>
<label class="evergreen-claims__field"><span>${escapeHtml(lang.blockCount)}</span><input class="b3-text-field" data-edge-field="segments"></label>
</div><datalist id="evergreen-target-${safeAttr(state.selectedID)}">${state.page.rows.map(row =>
            `<option value="${safeAttr(row.ref.logical_id)}">${escapeHtml(row.cells?.title?.text || row.ref.logical_id)}</option>`).join("")}</datalist>
<button type="button" class="b3-button b3-button--outline" data-claim-action="add-edge">${escapeHtml(lang.save)}</button></details>`}
</section>`;
};

const renderEditor = (state: ClaimsState) => {
    const lang = window.siyuan.languages;
    if (!state.creating && !state.snapshot) {
        return `<div class="b3-list--empty">${escapeHtml(lang.empty)}</div>`;
    }
    const descriptor = currentKind(state);
    const draft = draftFor(state);
    const disabled = disabledAttr(state);
    const unavailable = !descriptor;
    return `<form class="evergreen-claims__editor" data-editor-id="${safeAttr(draftKey(state))}">
<div class="evergreen-claims__toolbar"><strong class="evergreen-claims__title">${escapeHtml(state.creating ? lang.new : draft.title)}</strong>
<span class="b3-chip b3-chip--small${unavailable ? " b3-chip--error" : ""}">${escapeHtml(unavailable ? lang.evergreenUnavailable : descriptor.view_hints.name)}</span>
${state.snapshot && state.openDocument ? `<button type="button" class="b3-button b3-button--text" data-claim-action="open-document">${escapeHtml(lang.openDocument)}</button>` : ""}</div>
${state.creating ? `<label class="evergreen-claims__field"><span>${escapeHtml(lang.title)}</span><input class="b3-text-field" data-claim-field="title" value="${safeAttr(draft.title)}" required${disabled}></label>
<label class="evergreen-claims__field"><span>${escapeHtml(lang.newContentFile)}</span><textarea class="b3-text-field" data-claim-field="body" rows="4"${disabled}>${escapeHtml(draft.body)}</textarea></label>` : ""}
<div class="evergreen-claims__fields">
<label class="evergreen-claims__field"><span>${escapeHtml(lang.cardStatus)}</span><select class="b3-select" data-claim-field="status"${disabled}${state.creating ? " disabled" : ""}>
${(descriptor?.allowed_statuses || [draft.status]).map(status => `<option value="${safeAttr(status)}"${status === draft.status ? " selected" : ""}>${escapeHtml(status)}</option>`).join("")}</select></label>
<label class="evergreen-claims__field"><span>${escapeHtml(lang.tag)}</span><input class="b3-text-field" data-claim-field="tags" value="${safeAttr(draft.tags)}"${disabled}></label>
</div>
<div class="evergreen-claims__fields" data-kind-editor>${descriptor ? schemaFieldsHTML(descriptor.kind_data_schema, draft.data, !!disabled) :
        `<pre class="fn__code">${escapeHtml(JSON.stringify(draft.data, null, 2))}</pre>`}</div>
${!state.creating && state.snapshot ? `<details><summary>${escapeHtml(lang.newContentFile)}</summary>
<pre class="fn__code">${escapeHtml(state.snapshot.body.map(node => {
        const text = (value: JSONValue): string => {
            if (!value || typeof value !== "object" || Array.isArray(value)) {
                return "";
            }
            return typeof value.Data === "string" ? value.Data :
                typeof value.TextMarkTextContent === "string" ? value.TextMarkTextContent :
                    Array.isArray(value.Children) ? value.Children.map(text).join("") : "";
        };
        return text(node);
    }).join("\n\n"))}</pre></details>` : ""}
${provenanceHTML(draft, disabled)}
${relationsHTML(state, disabled)}
<div class="evergreen-claims__toolbar"><button type="submit" class="b3-button" data-claim-action="save"${disabled}>
<svg><use xlink:href="#iconSelect"></use></svg>${escapeHtml(lang.save)}</button>
<button type="button" class="b3-button b3-button--cancel" data-claim-action="reset"${state.busy ? " disabled" : ""}>${escapeHtml(lang.cancel)}</button></div>
${state.snapshot ? `<details><summary>${escapeHtml(lang.more)}</summary><pre class="fn__code">${escapeHtml(JSON.stringify({envelope: state.snapshot.envelope, body: state.snapshot.body}, null, 2))}</pre></details>` : ""}
</form>`;
};

const renderClaims = (state: ClaimsState) => {
    if (state.closed) {
        return;
    }
    const lang = window.siyuan.languages;
    const disabled = state.busy ? " disabled" : "";
    state.root.innerHTML = `<div class="evergreen-claims__toolbar b3-form__space--small">
<select class="b3-select" data-claims-view aria-label="${safeAttr(lang.allViews)}"${disabled}>
<option value="claims"${state.viewID === "claims" ? " selected" : ""}>${escapeHtml(lang.evergreenClaims)}</option>
${state.kinds.filter(kind => kind.view_hints.view_id).map(kind => `<option value="${safeAttr(kind.view_hints.view_id)}"${state.viewID === kind.view_hints.view_id ? " selected" : ""}>${escapeHtml(kind.view_hints.name)}</option>`).join("")}
</select><span class="fn__flex-1"></span>
<select class="b3-select" data-new-kind aria-label="${safeAttr(lang.type)}"${disabled}>${state.kinds.map(kind =>
        `<option value="${safeAttr(kind.kind)}"${state.creating === kind.kind ? " selected" : ""}>${escapeHtml(kind.view_hints.name)}</option>`).join("")}</select>
<button type="button" class="b3-button b3-button--outline" data-claim-action="new"${state.readOnly || !state.notebookID || state.busy ? " disabled" : ""}><svg><use xlink:href="#iconAdd"></use></svg>${escapeHtml(lang.new)}</button>
<button type="button" class="block__icon block__icon--show" data-claim-action="refresh" aria-label="${safeAttr(lang.refresh)}"${disabled}><svg><use xlink:href="#iconRefresh"></use></svg></button></div>
${state.error ? `<div class="b3-form__space--small ft__error evergreen-claims__error" role="alert">${escapeHtml(state.error)}</div>` : ""}
${state.checked.size ? `<div class="evergreen-claims__toolbar b3-form__space--small" data-bulk-editor>
<span>${escapeHtml(lang.selected)} ${state.checked.size}</span>
${state.capabilities?.fields.includes("claim.status") ? `<select class="b3-select" data-bulk-status aria-label="${safeAttr(lang.cardStatus)}"${disabled}><option value=""></option>${state.capabilities.statuses.map(status => `<option value="${safeAttr(status)}">${escapeHtml(status)}</option>`).join("")}</select>` : ""}
${state.capabilities?.fields.includes("claim.tags") ? `<input class="b3-text-field" data-bulk-tags aria-label="${safeAttr(lang.tag)}"${disabled}>` : ""}
<button type="button" class="b3-button b3-button--outline" data-claim-action="bulk"${!state.capabilities || state.readOnly || state.busy ? " disabled" : ""}>${escapeHtml(lang.updateAll)}</button></div>` : ""}
<div class="evergreen-claims__workspace"><aside class="b3-list b3-list--background evergreen-claims__list">
${state.page.rows.map(row => `<div class="b3-list-item b3-list-item--narrow${row.ref.logical_id === state.selectedID && !state.creating ? " b3-list-item--focus" : ""}">
<input type="checkbox" class="b3-switch" data-check-id="${safeAttr(row.ref.logical_id)}" aria-label="${safeAttr(lang.selected)}"${state.checked.has(row.ref.logical_id) ? " checked" : ""}${state.busy || state.readOnly || row.read_only ? " disabled" : ""}>
<button type="button" class="b3-list-item__text" data-claim-id="${safeAttr(row.ref.logical_id)}" title="${safeAttr(row.cells?.title?.text || row.ref.logical_id)}"${disabled}>${escapeHtml(row.cells?.title?.text || row.ref.logical_id)}</button>
<span class="b3-list-item__meta b3-list-item__meta--ellipsis">${escapeHtml(row.read_only ? lang.evergreenUnavailable : row.cells?.kind?.text || "")}</span></div>`).join("")}
${!state.page.rows.length ? `<div class="b3-list--empty">${escapeHtml(lang.empty)}</div>` : ""}
${state.page.total > state.page.rows.length ? `<button type="button" class="b3-button b3-button--text" data-claim-action="load-more"${disabled}>${escapeHtml(lang.loadMore)} (${state.page.rows.length}/${state.page.total})</button>` : ""}
</aside><main class="evergreen-claims__main">${renderEditor(state)}</main></div>`;
    updateEdgeTypes(state);
};

const captureDraft = (state: ClaimsState) => {
    if (state.busy || state.readOnly || state.snapshot?.read_only || !currentKind(state) ||
        !state.root.querySelector("[data-editor-id]")) {
        return;
    }
    const changed = Array.from(state.root.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(
        "[data-editor-id] input, [data-editor-id] textarea, [data-editor-id] select"
    )).some(input => {
        if (input instanceof HTMLSelectElement) {
            const initial = Array.from(input.options).find(option => option.defaultSelected)?.value || input.options[0]?.value;
            return input.value !== initial;
        }
        return input.value !== input.defaultValue;
    });
    if (!state.creating && !state.drafts.has(draftKey(state)) && !changed) {
        return;
    }
    const field = (name: string) => state.root.querySelector<HTMLInputElement>(`[data-claim-field="${name}"]`)?.value || "";
    const prior = draftFor(state);
    state.drafts.set(draftKey(state), {...prior, status: field("status"), tags: field("tags"),
        title: state.creating ? field("title") : prior.title, body: field("body"),
        data: collectSchemaFields(state.root.querySelector("[data-kind-editor]")),
        provenance: Array.from(state.root.querySelectorAll<HTMLElement>(".evergreen-claims__source")).map(row => {
            const value = (name: string) => row.querySelector<HTMLInputElement>(`[data-source-field="${name}"]`).value.trim();
            return {source_id: value("source_id"), note_id: value("note_id"),
                segment_refs: splitValues(value("segments")), reason: value("reason")};
        })});
};

const runOperation = async (state: ClaimsState, action: () => Promise<void>, refresh = false) => {
    if (state.busy || state.closed) {
        return;
    }
    state.busy = true;
    state.error = "";
    renderClaims(state);
    try {
        await action();
        if (refresh) {
            await loadPage(state);
            await loadClaim(state);
        }
    } catch (error) {
        state.error = error instanceof Error ? error.message : String(error);
        // 冲突后读取最新权威版本，本地草稿仍保留原始 base，不自动覆盖并发编辑。
        if (refresh) {
            try {
                await loadPage(state);
                await loadClaim(state);
            } catch {
                // 保留第一次失败的诊断。
            }
        }
    } finally {
        state.busy = false;
        renderClaims(state);
    }
};

const updateEdgeTypes = (state: ClaimsState) => {
    const schema = state.root.querySelector<HTMLSelectElement>("[data-edge-field=schema]")?.value;
    const select = state.root.querySelector<HTMLSelectElement>("[data-edge-field=type]");
    if (select) {
        const types = schema === "evergreen.material/v1" ? ["support", "against", "context"] :
            schema === "evergreen.replacement/v1" ? ["replaced_by"] : ["derives", "supports", "limits", "opposing"];
        select.innerHTML = types.map(type => `<option value="${type}">${type}</option>`).join("");
    }
};

const saveClaim = async (state: ClaimsState) => {
    const draft = draftFor(state);
    if (state.creating) {
        const documentID = Lute.NewNodeID();
        const body: JSONValue[] = draft.body.split(/\n\s*\n/).filter(Boolean).map(text => ({
            ID: Lute.NewNodeID(), Type: "NodeParagraph", Children: [{Type: "NodeText", Data: text}],
        }));
        const response = await fetchSyncPost("/api/evergreen/v1/claims/plan-create", {
            operation_id: `claim-create-${Lute.NewNodeID()}`, logical_id: `c-${documentID}`, document_id: documentID,
            path: `${state.notebookID}/${documentID}.sy`, title: draft.title.trim(), body, kind: state.creating,
            tags: splitValues(draft.tags), kind_data: draft.data, provenance: draft.provenance,
        }, undefined, false);
        if (!("ok" in response) || !response.ok) {
            throw new Error(evergreenError(response));
        }
        await applyEvergreenOperation(response.data);
        state.drafts.delete(draftKey(state));
        state.creating = undefined;
        state.selectedID = `c-${documentID}`;
    } else {
        const response = await fetchSyncPost("/api/evergreen/v1/claims/plan-edit", {
            operation_id: `claim-edit-${Lute.NewNodeID()}`, logical_id: state.selectedID, base: draft.base,
            edit: {status: draft.status, tags: splitValues(draft.tags), kind_data: draft.data, provenance: draft.provenance},
        }, undefined, false);
        if (!("ok" in response) || !response.ok) {
            throw new Error(evergreenError(response));
        }
        await applyEvergreenOperation(response.data);
        state.drafts.delete(draftKey(state));
    }
};

const mutateEdge = async (state: ClaimsState, edgeID?: string) => {
    const value = (name: string) => state.root.querySelector<HTMLInputElement>(`[data-edge-field="${name}"]`)?.value.trim() || "";
    const schema = value("schema");
    const editingID = state.root.querySelector<HTMLElement>(".evergreen-claims__edge-form")?.dataset.editingEdge;
    const existing = state.snapshot.envelope.relations.outgoing.find(item => item.edge_id === (edgeID || editingID));
    const edge: EvergreenTypedEdge = edgeID ? existing :
        {...existing, edge_id: existing?.edge_id || `edge-${Lute.NewNodeID()}`, schema, type: value("type"), reason: value("reason"),
            target: {...existing?.target, entity_type: schema === "evergreen.material/v1" ? "source" : "claim", logical_id: value("target")},
            context: {...existing?.context, note_id: value("note"), segment_refs: splitValues(value("segments"))}};
    const response = await fetchSyncPost("/api/evergreen/v1/av/plan-patch", {
        operation_id: `claim-edge-${Lute.NewNodeID()}`, row_id: state.selectedID,
        base: {logical_id: state.selectedID, semantic_hash: state.snapshot.base},
        column: {direction: "outgoing", edge_schema: edge.schema},
        edge: {action: edgeID ? "delete" : editingID ? "update" : "add", edge},
    }, undefined, false);
    if (!("ok" in response) || !response.ok) {
        throw new Error(evergreenError(response));
    }
    await applyEvergreenOperation(response.data);
};

const bindClaims = (state: ClaimsState) => {
    state.root.addEventListener("submit", event => {
        event.preventDefault();
        if (disabledAttr(state)) {
            return;
        }
        try {
            validateSchemaFields(state.root);
            captureDraft(state);
            void runOperation(state, () => saveClaim(state), true);
        } catch (error) {
            state.error = String(error);
            const errorElement = state.root.querySelector<HTMLElement>("[role=alert]");
            if (errorElement) {
                errorElement.textContent = state.error;
            } else {
                showMessage(escapeHtml(state.error), 7000, "error");
            }
        }
    });
    state.root.addEventListener("change", event => {
        const target = event.target as HTMLInputElement;
        if (state.busy || state.closed) {
            return;
        }
        if (target.matches("[data-edge-field=schema]")) {
            updateEdgeTypes(state);
            return;
        }
        try {
            captureDraft(state);
            if (target.matches("[data-claims-view]")) {
                state.viewID = target.value;
                state.creating = undefined;
                void runOperation(state, async () => {
                    await loadPage(state);
                    if (!state.page.rows.some(row => row.ref.logical_id === state.selectedID)) {
                        state.selectedID = state.page.rows[0]?.ref.logical_id || "";
                    }
                    await loadClaim(state);
                });
            } else if (target.dataset.checkId && !target.disabled) {
                const row = state.page.rows.find(item => item.ref.logical_id === target.dataset.checkId);
                if (target.checked) {
                    state.checked.set(row.ref.logical_id, row.ref);
                } else {
                    state.checked.delete(row.ref.logical_id);
                }
                state.capabilities = undefined;
                void runOperation(state, async () => {
                    if (!state.checked.size) {
                        return;
                    }
                    const response = await fetchSyncPost("/api/evergreen/v1/claims/bulk-capabilities", {
                        rows: Array.from(state.checked.values()),
                    }, undefined, false);
                    if (!("ok" in response) || !response.ok) {
                        throw new Error(evergreenError(response));
                    }
                    state.capabilities = response.data;
                });
            }
        } catch (error) {
            showMessage(escapeHtml(String(error)), 7000, "error");
        }
    });
    state.root.addEventListener("click", event => {
        const target = (event.target as Element).closest<HTMLElement>("button");
        if (!target || target.matches(":disabled") || state.busy || state.closed) {
            return;
        }
        try {
            captureDraft(state);
            const selected = target.dataset.claimId || target.dataset.targetId;
            if (selected) {
                state.creating = undefined;
                state.selectedID = selected;
                void runOperation(state, () => loadClaim(state));
                return;
            }
            const action = target.dataset.claimAction;
            if (action === "new") {
                state.snapshot = undefined;
                state.creating = state.root.querySelector<HTMLSelectElement>("[data-new-kind]").value;
                renderClaims(state);
            } else if (action === "refresh") {
                void runOperation(state, async () => { await loadPage(state); await loadClaim(state); });
            } else if (action === "load-more") {
                void runOperation(state, async () => {
                    const response = await fetchSyncPost("/api/evergreen/v1/av/query", {view_id: state.viewID,
                        query: {offset: state.page.rows.length, limit: 500, sorts: [{column: "title", direction: "asc"}]}},
                    undefined, false);
                    if (!("ok" in response) || !response.ok) {
                        throw new Error(evergreenError(response));
                    }
                    state.page = {...response.data, rows: [...state.page.rows, ...response.data.rows]};
                });
            } else if (action === "reset") {
                state.drafts.delete(draftKey(state));
                state.creating = undefined;
                void runOperation(state, () => loadClaim(state));
            } else if (action === "open-document") {
                state.openDocument?.(state.snapshot.document_id);
            } else if (!disabledAttr(state) && (action === "add-source" || action === "remove-source")) {
                const draft = draftFor(state);
                if (action === "add-source") {
                    draft.provenance.push({source_id: "", note_id: "", segment_refs: [], reason: ""});
                } else {
                    draft.provenance.splice(Number(target.dataset.sourceIndex), 1);
                }
                state.drafts.set(draftKey(state), draft);
                renderClaims(state);
            } else if (!disabledAttr(state) && action === "edit-edge") {
                const edge = state.snapshot.envelope.relations.outgoing.find(item => item.edge_id === target.dataset.edgeId);
                const form = state.root.querySelector<HTMLDetailsElement>(".evergreen-claims__edge-form");
                form.dataset.editingEdge = edge.edge_id;
                form.open = true;
                const field = (name: string, value: string) => {
                    state.root.querySelector<HTMLInputElement>(`[data-edge-field="${name}"]`).value = value || "";
                };
                field("schema", edge.schema);
                updateEdgeTypes(state);
                field("type", edge.type);
                field("target", edge.target.logical_id);
                field("reason", edge.reason);
                field("note", edge.context?.note_id);
                field("segments", edge.context?.segment_refs?.join(", "));
            } else if (!disabledAttr(state) && (action === "add-edge" || action === "delete-edge")) {
                // 表单值在重新渲染前收集。
                const operation = mutateEdge(state, target.dataset.edgeId);
                void runOperation(state, () => operation, true);
            } else if (action === "bulk" && state.capabilities && !state.readOnly) {
                const edit: EvergreenClaimEditInput = {};
                const status = state.root.querySelector<HTMLSelectElement>("[data-bulk-status]")?.value;
                const tags = state.root.querySelector<HTMLInputElement>("[data-bulk-tags]")?.value;
                if (status) {
                    edit.status = status;
                }
                if (tags) {
                    edit.tags = splitValues(tags);
                }
                if (!Object.keys(edit).length) {
                    return;
                }
                void runOperation(state, async () => {
                    const response = await fetchSyncPost("/api/evergreen/v1/claims/plan-bulk", {
                        operation_id: `claim-bulk-${Lute.NewNodeID()}`, rows: Array.from(state.checked.values()), edit,
                    }, undefined, false);
                    if (!("ok" in response) || !response.ok) {
                        throw new Error(evergreenError(response));
                    }
                    await applyEvergreenOperation(response.data);
                    state.checked.clear();
                    state.capabilities = undefined;
                }, true);
            }
        } catch (error) {
            showMessage(escapeHtml(String(error)), 7000, "error");
        }
    });
};

export const openEvergreenClaims = async (options: {
    notebookID: string; logicalID?: string; readOnly?: boolean; openDocument?: (id: string) => void;
}) => {
    const catalog = await fetch("/api/evergreen/v1/schemas", {cache: "no-store"}).then(response => response.json());
    if (!catalog.ok || !Array.isArray(catalog.data?.kinds)) {
        throw new Error(evergreenError(catalog));
    }
    const state: ClaimsState = {
        root: document.createElement("div"), kinds: catalog.data.kinds, viewID: "claims",
        page: {rows: [], snapshot: "", total: 0}, selectedID: options.logicalID || "",
        drafts: new Map(), checked: new Map(), busy: false, closed: false,
        readOnly: !!options.readOnly || window.siyuan.config.readonly, notebookID: options.notebookID,
        error: "", openDocument: options.openDocument,
    };
    await loadPage(state);
    state.selectedID ||= state.page.rows[0]?.ref.logical_id || "";
    await loadClaim(state);
    const dialog = new Dialog({
        title: window.siyuan.languages.evergreenClaims, width: isMobile() ? "96vw" : "min(1040px, 94vw)",
        height: isMobile() ? "88vh" : "min(780px, 88vh)", containerClassName: "evergreen-claims-dialog",
        content: '<div class="evergreen-claims"></div>', destroyCallback: () => { state.closed = true; },
    });
    state.root = dialog.element.querySelector(".evergreen-claims");
    bindClaims(state);
    renderClaims(state);
    return dialog;
};
