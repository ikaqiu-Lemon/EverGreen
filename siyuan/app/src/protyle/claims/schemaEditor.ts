import type {EvergreenJSONSchema, EvergreenKindSchema, JSONValue} from "../../types/api";
import {escapeAttr, escapeHtml} from "../../util/escape";

export const safeAttr = (value: string) => escapeAttr(escapeHtml(value || ""));

export const schemaDefaults = (descriptor: EvergreenKindSchema): Record<string, JSONValue> => {
    const value = descriptor.default_kind_data;
    return value && typeof value === "object" && !Array.isArray(value) ?
        JSON.parse(JSON.stringify(value)) : {};
};

const valueAt = (data: Record<string, JSONValue>, path: string[]): JSONValue => {
    let value: JSONValue = data;
    for (const key of path) {
        if (!value || typeof value !== "object" || Array.isArray(value)) {
            return undefined;
        }
        if (!Object.prototype.hasOwnProperty.call(value, key)) {
            return undefined;
        }
        value = value[key];
    }
    return value;
};

export const schemaFieldsHTML = (
    schema: EvergreenJSONSchema,
    data: Record<string, JSONValue>,
    disabled = false,
    path: string[] = [],
): string => Object.entries(schema.properties || {}).map(([key, field]) => {
    const fieldPath = [...path, key];
    const label = field.title || key;
    if (field.type === "object") {
        return `<fieldset class="evergreen-claims__fieldset"><legend>${escapeHtml(label)}</legend>
${schemaFieldsHTML(field, data, disabled, fieldPath)}</fieldset>`;
    }
    const value = valueAt(data, fieldPath);
    const attrs = ` data-kind-field="${safeAttr(JSON.stringify(fieldPath))}" data-field-type="${safeAttr(field.type)}"${disabled ? " disabled" : ""}`;
    const required = schema.required?.includes(key);
    let control: string;
    if (field.enum?.length) {
        control = `<select class="b3-select"${attrs}><option value=""></option>${field.enum.map(item =>
            `<option value="${safeAttr(item)}"${value === item ? " selected" : ""}>${escapeHtml(item)}</option>`
        ).join("")}</select>`;
    } else if (field.type === "boolean") {
        control = `<select class="b3-select"${attrs}><option value=""></option>${["true", "false"].map(item =>
            `<option value="${item}"${String(value) === item ? " selected" : ""}>${item}</option>`
        ).join("")}</select>`;
    } else if (field.type === "array") {
        control = `<textarea class="b3-text-field fn__code"${attrs} rows="3">${escapeHtml(JSON.stringify(value ?? [], null, 2))}</textarea>`;
    } else {
        control = `<input class="b3-text-field" type="${field.type === "number" ? "number" : "text"}"${attrs} value="${safeAttr(value === undefined ? "" : String(value))}"${field.type === "number" ? ' step="any"' : ""}${field.minLength ? ` minlength="${field.minLength}"` : ""}${required ? " required" : ""}>`;
    }
    return `<label class="evergreen-claims__field"><span>${escapeHtml(label)}${required ? " *" : ""}</span>${control}</label>`;
}).join("");

// 仅提交声明字段的变化，未声明字段由内核递归合并保留。
export const collectSchemaFields = (root: Element): Record<string, JSONValue> => {
    const patch: Record<string, JSONValue> = Object.create(null);
    root.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>("[data-kind-field]").forEach(input => {
        if (input.disabled) {
            return;
        }
        const path = JSON.parse(input.dataset.kindField) as string[];
        const value = input.value;
        const type = input.dataset.fieldType;
        if (value === "" && type !== "string") {
            return;
        }
        let parsed: JSONValue = value;
        if (type === "array") {
            parsed = JSON.parse(value);
            if (!Array.isArray(parsed)) {
                throw new Error(window.siyuan.languages.invalid);
            }
        } else if (type === "boolean") {
            parsed = value === "true";
        } else if (type === "number") {
            parsed = Number(value);
            if (!Number.isFinite(parsed)) {
                throw new Error(window.siyuan.languages.invalid);
            }
        }
        let object = patch;
        path.slice(0, -1).forEach(key => {
            if (!Object.prototype.hasOwnProperty.call(object, key)) {
                object[key] = Object.create(null);
            }
            object = object[key] as Record<string, JSONValue>;
        });
        object[path[path.length - 1]] = parsed;
    });
    return patch;
};

export const validateSchemaFields = (root: Element) => {
    for (const input of Array.from(root.querySelectorAll<HTMLInputElement>("[data-kind-field]"))) {
        if (!input.disabled && !input.reportValidity()) {
            throw new Error(window.siyuan.languages.invalid);
        }
    }
};
