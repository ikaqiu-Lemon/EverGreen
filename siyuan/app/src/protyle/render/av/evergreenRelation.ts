import {fetchSyncPost} from "../../../util/fetch";
import {escapeAttr, escapeHtml} from "../../../util/escape";
import {showMessage} from "../../../dialog/message";
import {queueDatabaseRowRefresh} from "./databaseRowRefresh";
import {genCellValueByElement} from "./cell";
import {getFieldsByData} from "./view";
import {getColId} from "./col";
import {getFieldIdByCellElement} from "./row";
import {IEvergreenEnvelope, planAndApplyEvergreenPatch} from "./evergreenPatch";
import {safeAttr} from "../../claims/schemaEditor";

interface IEvergreenPage {
    rows: Array<{
        ref: {
            logical_id: string;
            semantic_hash: string;
        };
        cells: Record<string, {
            text?: string;
        }>;
    }>;
}

const edgeTypes = (schema: string) => {
    if (schema === "evergreen.material/v1") {
        return ["support", "against", "context"];
    }
    if (schema === "evergreen.replacement/v1") {
        return ["replaced_by"];
    }
    return ["derives", "supports", "limits", "opposing"];
};

const edgeFormHTML = (edge: IAVTypedEdge | undefined, binding: IAVColumnBinding, isNew = false, readOnly = false) => {
    const lang = window.siyuan.languages;
    const disabled = readOnly ? " disabled" : "";
    const schema = edge?.schema || binding.edge_schema || "evergreen.argument/v1";
    const type = edge?.type || binding.edge_type || edgeTypes(schema)[0];
    const context = edge?.context || {};
    const edgeData = edge ? escapeAttr(encodeURIComponent(JSON.stringify(edge))) : "";
    const typeOptions = edgeTypes(schema).map((item) =>
        `<option value="${escapeAttr(item)}"${item === type ? " selected" : ""}>${escapeHtml(item)}</option>`
    ).join("");
    return `<div class="av__evergreen-edge" data-edge-id="${safeAttr(edge?.edge_id || "")}" data-edge="${edgeData}" data-new="${isNew}">
<div class="fn__flex-column fn__flex-1">
    <div class="fn__flex">
        <input class="b3-text-field fn__flex-1" data-field="target" name="evergreen-edge-target" list="evergreen-claim-targets" value="${safeAttr(edge?.target.logical_id || "")}" aria-label="${safeAttr(lang.link)}"${disabled}/>
        <span class="fn__space"></span>
        <select class="b3-select" data-field="type" name="evergreen-edge-type" aria-label="${safeAttr(lang.type)}"${disabled}>${typeOptions}</select>
    </div>
    <div class="fn__hr--small"></div>
    <textarea class="b3-text-field" data-field="reason" name="evergreen-edge-reason" rows="2" aria-label="${safeAttr(lang.memo)}"${disabled}>${escapeHtml(edge?.reason || "")}</textarea>
    <div class="fn__hr--small"></div>
    <div class="fn__flex">
        <input class="b3-text-field fn__flex-1" data-field="note" name="evergreen-edge-note" value="${safeAttr(context.note_id || "")}" aria-label="${safeAttr(lang.doc)}"${disabled}/>
        <span class="fn__space"></span>
        <input class="b3-text-field fn__flex-1" data-field="segments" name="evergreen-edge-segments" value="${safeAttr((context.segment_refs || []).join(", "))}" aria-label="${safeAttr(lang.blockCount)}"${disabled}/>
    </div>
    <div class="fn__hr--small"></div>
    <input class="b3-text-field fn__block" data-field="quote" name="evergreen-edge-quote-hash" value="${safeAttr(context.quote_hash || "")}" aria-label="${safeAttr(lang.ref)}"${disabled}/>
</div>
<span class="fn__space"></span>
<div class="fn__flex-column">
    <button type="button" class="b3-button b3-button--text b3-button--icon ariaLabel" data-type="evergreenEdgeSave" aria-label="${safeAttr(lang.save)}"${disabled}><svg><use xlink:href="#iconSelect"></use></svg></button>
    ${isNew ? "" : `<button type="button" class="b3-button b3-button--text b3-button--icon ariaLabel" data-type="evergreenEdgeDelete" aria-label="${safeAttr(lang.remove)}"${disabled}><svg><use xlink:href="#iconTrashcan"></use></svg></button>`}
    ${isNew || binding.direction === "incoming" ? "" : `<button type="button" class="b3-button b3-button--text b3-button--icon ariaLabel" data-type="evergreenEdgeUp" aria-label="${safeAttr(lang.moveToUp)}"${disabled}><svg><use xlink:href="#iconUp"></use></svg></button>
    <button type="button" class="b3-button b3-button--text b3-button--icon ariaLabel" data-type="evergreenEdgeDown" aria-label="${safeAttr(lang.moveToDown)}"${disabled}><svg><use xlink:href="#iconDown"></use></svg></button>`}
</div>
</div>`;
};

export const getEvergreenRelationHTML = (data: IAV, cellElements?: HTMLElement[]) => {
    if (data.provider?.type !== "evergreen.sy" || !cellElements?.[0]) {
        return "";
    }
    const colID = getColId(cellElements[0], data.viewType);
    const column = getFieldsByData(data).find((item) => item.id === colID);
    if (!column?.binding?.direction) {
        return "";
    }
    const value = genCellValueByElement("relation", cellElements[0]);
    const edges = value.relation?.edges || [];
    const readOnly = value.providerReadOnly;
    let html = `<div class="fn__flex-column av__evergreen-relation" data-av-id="${escapeAttr(data.id)}" data-view-id="${escapeAttr(data.viewID)}" data-col-id="${escapeAttr(colID)}" data-binding="${escapeAttr(encodeURIComponent(JSON.stringify(column.binding)))}">
<datalist id="evergreen-claim-targets"></datalist>
<div>`;
    edges.forEach((edge) => {
        html += edgeFormHTML(edge, column.binding, false, readOnly);
    });
    if (!readOnly && column.binding.direction === "outgoing") {
        html += edgeFormHTML(undefined, column.binding, true);
    }
    if (edges.length === 0 && (readOnly || column.binding.direction === "incoming")) {
        html += `<div class="b3-list--empty">${escapeHtml(window.siyuan.languages.emptyContent)}</div>`;
    }
    return html + "</div></div>";
};

const fieldValue = (edgeElement: HTMLElement, field: string) =>
    (edgeElement.querySelector(`[data-field="${field}"]`) as HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement)?.value.trim() || "";

export const bindEvergreenRelationEditor = (options: {
    menuElement: HTMLElement;
    protyle: IProtyle;
    blockElement: Element;
    cellElements: HTMLElement[];
}) => {
    const root = options.menuElement.querySelector(".av__evergreen-relation") as HTMLElement;
    if (!root) {
        return;
    }
    const cellElement = options.cellElements[0];
    const rowID = getFieldIdByCellElement(cellElement, options.blockElement.getAttribute("data-av-type") as TAVView);
    const binding = JSON.parse(decodeURIComponent(root.dataset.binding)) as IAVColumnBinding;
    const avID = root.dataset.avId;
    let busy = false;
    if (options.protyle.disabled || window.siyuan.config.readonly) {
        root.querySelectorAll<HTMLInputElement>("input, select, textarea, button").forEach(input => {
            input.disabled = true;
        });
    }

    const refresh = () => {
        queueDatabaseRowRefresh(options.protyle.id, avID);
    };
    const commit = async (edgeElement: HTMLElement, action: "add" | "update" | "delete" | "reorder", order?: string[]) => {
        if (busy || options.protyle.disabled || window.siyuan.config.readonly || cellElement.dataset.providerReadonly === "true") {
            return;
        }
        busy = true;
        root.querySelectorAll<HTMLButtonElement>("button").forEach(button => { button.disabled = true; });
        const existing = edgeElement.dataset.edge ?
            JSON.parse(decodeURIComponent(edgeElement.dataset.edge)) as IAVTypedEdge : undefined;
        const ownerID = existing?.owner_id || rowID;
        const ownerHash = existing?.owner_hash || cellElement.dataset.providerBase;
        const edgeID = existing?.edge_id || `edge-${Lute.NewNodeID()}`;
        const edge: IAVTypedEdge = {
            ...(existing || {}),
            edge_id: edgeID,
            owner_id: ownerID,
            owner_hash: ownerHash,
            direction: binding.direction,
            schema: existing?.schema || binding.edge_schema || "evergreen.argument/v1",
            target: {
                ...(existing?.target || {}),
                entity_type: existing?.target.entity_type ||
                    (binding.edge_schema === "evergreen.material/v1" ? "source" : "claim"),
                logical_id: fieldValue(edgeElement, "target"),
            },
            type: fieldValue(edgeElement, "type"),
            reason: fieldValue(edgeElement, "reason"),
            context: {
                ...(existing?.context || {}),
                note_id: fieldValue(edgeElement, "note"),
                segment_refs: fieldValue(edgeElement, "segments").split(",").map((item) => item.trim()).filter(Boolean),
                quote_hash: fieldValue(edgeElement, "quote"),
            },
        };
        const patch = {
            operation_id: `op-${Lute.NewNodeID()}`,
            row_id: rowID,
            base: {
                logical_id: ownerID,
                semantic_hash: ownerHash,
            },
            column: binding,
            edge: {
                action,
                owner_id: binding.direction === "incoming" ? ownerID : undefined,
                edge,
                order,
            },
        };
        cellElement.classList.add("av__cell--select");
        cellElement.dataset.evergreenPending = "true";
        let applied = false;
        try {
            await planAndApplyEvergreenPatch(patch, async (path, body) =>
                await fetchSyncPost(path, body, undefined, false) as unknown as IEvergreenEnvelope<unknown>);
            applied = true;
        } catch (error) {
            showMessage(escapeHtml(error instanceof Error ? error.message : String(error)), 7000, "error");
        } finally {
            busy = false;
            delete cellElement.dataset.evergreenPending;
            cellElement.classList.remove("av__cell--select");
            if (applied) {
                options.menuElement.closest(".av__panel")?.dispatchEvent(new CustomEvent("click", {detail: "close"}));
            } else {
                root.querySelectorAll<HTMLButtonElement>("button").forEach(button => { button.disabled = false; });
            }
            refresh();
        }
    };

    const loadTargets = async () => {
        const response = await fetchSyncPost("/api/evergreen/v1/av/query", {
            view_id: "claims",
            query: {limit: 500},
        }, undefined, false) as IEvergreenEnvelope<IEvergreenPage>;
        if (!response.ok) {
            return;
        }
        const datalist = root.querySelector("#evergreen-claim-targets");
        if (datalist) {
            datalist.innerHTML = response.data.rows.map((row) =>
                `<option value="${safeAttr(row.ref.logical_id)}">${escapeHtml(row.cells.title?.text || row.ref.logical_id)}</option>`
            ).join("");
        }
    };
    const onClick = (event: MouseEvent) => {
        const button = (event.target as HTMLElement).closest<HTMLElement>("[data-type]");
        const edgeElement = button?.closest<HTMLElement>(".av__evergreen-edge");
        if (!button || !edgeElement || button.matches(":disabled") || busy ||
            options.protyle.disabled || window.siyuan.config.readonly) {
            return;
        }
        switch (button.dataset.type) {
            case "evergreenEdgeSave":
                void commit(edgeElement, edgeElement.dataset.new === "true" ? "add" : "update");
                break;
            case "evergreenEdgeDelete":
                void commit(edgeElement, "delete");
                break;
            case "evergreenEdgeUp":
            case "evergreenEdgeDown": {
                const sibling = button.dataset.type === "evergreenEdgeUp" ?
                    edgeElement.previousElementSibling : edgeElement.nextElementSibling;
                if (sibling?.classList.contains("av__evergreen-edge") && (sibling as HTMLElement).dataset.new !== "true") {
                    edgeElement.parentElement.insertBefore(
                        edgeElement,
                        button.dataset.type === "evergreenEdgeUp" ? sibling : sibling.nextElementSibling,
                    );
                    const order = Array.from(edgeElement.parentElement.querySelectorAll<HTMLElement>(
                        ".av__evergreen-edge[data-new='false']"
                    )).map((item) => item.dataset.edgeId);
                    void commit(edgeElement, "reorder", order);
                }
                break;
            }
        }
        event.preventDefault();
        event.stopPropagation();
    };
    root.addEventListener("click", onClick);
    void loadTargets().catch(error => showMessage(escapeHtml(String(error)), 7000, "error"));
    return () => root.removeEventListener("click", onClick);
};
