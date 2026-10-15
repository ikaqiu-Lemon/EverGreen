const assert = require("node:assert/strict");
const {readFileSync, mkdtempSync, rmSync} = require("node:fs");
const path = require("node:path");
const os = require("node:os");

const sources = () => {
    const ts = require("typescript");
    const root = path.join(__dirname, "..");
    const compile = file => ts.transpileModule(readFileSync(path.join(root, file), "utf8"), {
        compilerOptions: {module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2021},
    }).outputText;
    return {
        ui: compile("src/protyle/claims/evergreenClaims.ts"),
        schema: compile("src/protyle/claims/schemaEditor.ts"),
        entry: compile("src/protyle/claims/entry.ts"),
        relation: compile("src/protyle/render/av/evergreenRelation.ts"),
        patch: compile("src/protyle/render/av/evergreenPatch.ts"),
        dialog: compile("src/dialog/index.ts"), escape: compile("src/util/escape.ts"),
        languages: JSON.parse(readFileSync(path.join(root, "appearance/langs/en.json"), "utf8")),
        css: require("sass").compile(path.join(root, "src/assets/scss/base.scss")).css,
        mobileCSS: require("sass").compile(path.join(root, "src/assets/scss/mobile.scss")).css,
        light: readFileSync(path.join(root, "appearance/themes/daylight/theme.css"), "utf8"),
        dark: readFileSync(path.join(root, "appearance/themes/midnight/theme.css"), "utf8"),
        icons: readFileSync(path.join(root, "appearance/icons/litheness/icon.js"), "utf8"),
    };
};

const cases = async source => {
    const check = require("node:assert/strict");
    const clone = value => JSON.parse(JSON.stringify(value));
    const browserRoundedInteger = JSON.parse('{"value":9007199254740993}').value;
    const load = (code, dependencies = {}) => {
        const exports = {};
        new Function("exports", "require", code)(exports, name => {
            check.ok(name in dependencies, `Unexpected dependency ${name}`);
            return dependencies[name];
        });
        return exports;
    };
    const kinds = ["knowledge", "opinion", "hypothesis"].map(kind => ({
        kind, schema: `evergreen.claim-kind.${kind}/v1`,
        view_hints: {view_id: kind, name: kind === "hypothesis" ? "Hypotheses" : kind},
        allowed_edge_schemas: ["evergreen.argument/v1", "evergreen.material/v1", "evergreen.replacement/v1"],
        default_status: kind === "hypothesis" ? "proposed" : "active",
        allowed_statuses: kind === "hypothesis" ? ["proposed", "testing", "archived"] : ["active", "archived"],
        default_kind_data: kind === "opinion" ? {validation: {status: "pending"}} : {},
        kind_data_schema: kind === "hypothesis" ? {type: "object", required: ["statement", "test_method"],
            properties: {statement: {type: "string", title: "Statement", minLength: 1},
                test_method: {type: "string", title: "Test method", minLength: 1}}} :
            kind === "opinion" ? {type: "object", properties: {validation: {type: "object", title: "Validation",
                properties: {status: {type: "string", enum: ["pending", "validated", "rejected"]},
                    method: {type: "string"}, evidence: {type: "array"}}}}} : {type: "object"},
    }));
    const fixtures = new Map([...kinds, {kind: "future"}].map((kind, index) => [`c-${kind.kind}`, {
        logical_id: `c-${kind.kind}`, document_id: `20261010120000-claim0${index}`, base: `sha256:${index}`,
        title: `Claim ${kind.kind}: independent canonical document`, read_only: kind.kind === "future",
        body: [{ID: `body-${index}`, Type: "NodeParagraph", Data: "Canonical body"}],
        envelope: {spec: "evergreen.sy/v1",
            entity: {logical_id: `c-${kind.kind}`, entity_type: "claim", schema: "evergreen.claim/v1"},
            claim: {claim_kind: kind.kind, kind_schema: kind.schema, status: kind.default_status || "active", tags: ["storage"],
                kind_data: kind.kind === "hypothesis" ? {statement: "CAS prevents lost updates",
                    test_method: "Concurrent writes", unknown: {preserved: true}} :
                    kind.kind === "opinion" ? {validation: {status: "pending"}} : {future: "preserved"}},
            provenance: [{source_id: "s-example", note_id: "n-example", segment_refs: ["seg-1"], reason: "Source evidence",
                ...(kind.kind === "hypothesis" ? {future_provenance: {
                    counter: browserRoundedInteger, values: [null, {kept: true}],
                }} : {})}],
            relations: {outgoing: []}},
    }]));
    let id = 0;
    const requests = [];
    let rejectPlan = false;
    let pending;
    let mobile = false;
    let lastPlan;
    const fetchSyncPost = async (url, request) => {
        requests.push({url, request: clone(request)});
        if (url.endsWith("/av/query")) {
            const rows = [...fixtures.values()].filter(item =>
                request.view_id === "claims" || item.envelope.claim.claim_kind === request.view_id).map(item => ({
                ref: {logical_id: item.logical_id, semantic_hash: item.base}, read_only: item.read_only,
                cells: {title: {text: item.title}, kind: {text: item.envelope.claim.claim_kind}},
            }));
            return {ok: true, data: {rows, total: rows.length, snapshot: "sha256:page"}};
        }
        if (url.endsWith("/claims/get")) {
            const fixture = request.document_id ? [...fixtures.values()].find(item => item.document_id === request.document_id) :
                fixtures.get(request.logical_id);
            return fixture ? {ok: true, data: clone(fixture)} : {ok: false};
        }
        if (url.endsWith("/bulk-capabilities")) {
            return {ok: true, data: {fields: ["claim.status", "claim.tags"], statuses: ["archived"]}};
        }
        if (url.includes("/plan-")) {
            lastPlan = {url, request: clone(request)};
            if (pending) {
                await pending;
            }
            if (rejectPlan) {
                return {ok: false, diagnostics: [{code: "EG_CONFLICT_BASE_MISMATCH", message: "Claim base is stale"}]};
            }
            return {ok: true, data: {images: [], plan: {base: [], writes: [], operation_id: request.operation_id}}};
        }
        if (url.endsWith("/apply")) {
            const {url: plannedURL, request: plan} = lastPlan;
            if (plannedURL.endsWith("/plan-edit")) {
                const fixture = fixtures.get(plan.logical_id);
                Object.assign(fixture.envelope.claim, {status: plan.edit.status, tags: plan.edit.tags});
                Object.assign(fixture.envelope.claim.kind_data, plan.edit.kind_data);
                const before = fixture.envelope.provenance;
                fixture.envelope.provenance = plan.edit.provenance.map(item => {
                    const retained = before.find(current =>
                        current.source_id === item.source_id && current.note_id === item.note_id) || {};
                    const extra = Object.fromEntries(Object.entries(retained).filter(([key]) =>
                        !["source_id", "note_id", "segment_refs", "reason"].includes(key)));
                    return {...clone(extra), ...clone(item)};
                });
                fixture.base = `sha256:new-${++id}`;
            } else if (plannedURL.endsWith("/plan-create")) {
                fixtures.set(plan.logical_id, {logical_id: plan.logical_id, document_id: plan.document_id,
                    base: `sha256:new-${++id}`, title: plan.title, body: plan.body,
                    envelope: {entity: {}, claim: {claim_kind: plan.kind, status: "proposed",
                        kind_data: clone(plan.kind_data), tags: plan.tags}, provenance: plan.provenance,
                    relations: {outgoing: []}}});
            } else if (plannedURL.endsWith("/plan-patch")) {
                const edges = fixtures.get(plan.row_id).envelope.relations.outgoing;
                if (plan.edge.action === "add") {
                    edges.push(plan.edge.edge);
                } else if (plan.edge.action === "update") {
                    edges[edges.findIndex(edge => edge.edge_id === plan.edge.edge.edge_id)] = plan.edge.edge;
                } else {
                    edges.splice(edges.findIndex(edge => edge.edge_id === plan.edge.edge.edge_id), 1);
                }
            } else if (plannedURL.endsWith("/plan-bulk")) {
                plan.rows.forEach(row => Object.assign(fixtures.get(row.logical_id).envelope.claim, plan.edit));
            }
            return {ok: true, data: {state: "COMPLETED"}};
        }
        throw new Error(`Unexpected URL ${url}`);
    };
    const style = document.createElement("style");
    style.textContent = source.css;
    document.head.append(style);
    const theme = document.createElement("style");
    theme.textContent = source.light;
    document.head.append(theme);
    document.documentElement.style.setProperty("--b3-font-size", "14px");
    window.siyuan = {languages: source.languages, config: {readonly: false},
        dialogs: [], zIndex: 1, menus: {menu: {element: document.createElement("div"), remove() {}}}};
    window.Lute = {NewNodeID: () => `20261010130000-${String(++id).padStart(7, "0")}`};
    window.fetch = async () => ({json: async () => ({ok: true, data: {kinds}})});
    const escape = load(source.escape);
    const dialogModule = load(source.dialog, {
        "../util/genID": {genUUID: () => `dialog-${++id}`}, "../util/zIndex": {isAbove: () => false},
        "./moveResize": {moveResize() {}}, "../util/functions": {isMobile: () => mobile},
        "../protyle/util/compatibility": {isNotCtrl: () => true}, "../protyle": {},
        "../constants": {Constants: {TIMEOUT_OPENDIALOG: 0, TIMEOUT_DBLCLICK: 0}},
    });
    const api = load(source.ui, {"../../dialog": dialogModule, "../../dialog/message": {showMessage() {}},
        "../../util/escape": escape, "../../util/fetch": {fetchSyncPost}, "../../util/functions": {isMobile: () => mobile},
        "./schemaEditor": load(source.schema, {"../../util/escape": escape})});
    const entry = load(source.entry, {
        "./evergreenClaims": api, "../../util/fetch": {fetchSyncPost}, "../../util/escape": escape,
        "../../dialog/message": {showMessage() {}}, "../../editor/util": {openFileById() {}},
        "../../mobile/editor": {openMobileFileById() {}}, "../../util/functions": {isMobile: () => mobile},
    });
    const breadcrumb = document.createElement("div");
    breadcrumb.innerHTML = '<div><div class="breadcrumb"></div><div class="protyle-breadcrumb__plugin"></div></div>';
    document.body.append(breadcrumb);
    const protyle = {notebookId: "box-test", block: {rootID: fixtures.get("c-hypothesis").document_id},
        breadcrumb: {element: breadcrumb.querySelector(".breadcrumb")}};
    await entry.syncEvergreenClaim(protyle);
    check.ok(breadcrumb.querySelector("[data-type=evergreen-claim]"));
    protyle.block.rootID = "ordinary-document";
    await entry.syncEvergreenClaim(protyle);
    check.equal(breadcrumb.querySelector("[data-type=evergreen-claim]"), null);
    breadcrumb.remove();
    let openedDocument;
    const dialog = await api.openEvergreenClaims({notebookID: "box-test", logicalID: "c-hypothesis",
        openDocument: id => { openedDocument = id; }});
    const root = document.querySelector(".evergreen-claims");
    const until = async condition => {
        for (let attempt = 0; attempt < 100; attempt++) {
            if (condition()) {
                return;
            }
            await new Promise(resolve => setTimeout(resolve, 10));
        }
        check.fail(`UI did not settle: ${root.textContent.slice(0, 400)}`);
    };
    const action = name => root.querySelector(`[data-claim-action="${name}"]`);
    const field = name => root.querySelector(`[data-kind-field='["${name}"]']`);
    const idle = () => until(() => !action("refresh").disabled);
    const selectClaim = async id => { root.querySelector(`[data-claim-id="${id}"]`).click(); await idle(); };
    field("test_method").value = "Unsaved method";
    await selectClaim("c-knowledge");
    await selectClaim("c-hypothesis");
    check.equal(field("test_method").value, "Unsaved method");
    root.querySelector("[data-source-field=segments]").value = "seg-1, seg-2";
    root.querySelector("[data-source-field=reason]").value = "Unsaved evidence edit";
    rejectPlan = true;
    const applies = () => requests.filter(item => item.url.endsWith("/apply")).length;
    const before = applies();
    action("save").click();
    await idle();
    check.equal(applies(), before);
    check.equal(field("test_method").value, "Unsaved method");
    check.equal(root.querySelector("[data-source-field=segments]").value, "seg-1, seg-2");
    check.equal(root.querySelector("[data-source-field=reason]").value, "Unsaved evidence edit");
    check.match(root.querySelector("[role=alert]").textContent, /EG_CONFLICT_BASE_MISMATCH/);
    rejectPlan = false;
    let release;
    pending = new Promise(resolve => { release = resolve; });
    action("save").click();
    root.querySelector("[data-claim-id=c-knowledge]").dispatchEvent(new MouseEvent("click", {bubbles: true}));
    check.equal(root.querySelector("[data-editor-id]").dataset.editorId, "c-hypothesis");
    release();
    pending = undefined;
    await idle();
    check.equal(fixtures.get("c-hypothesis").envelope.claim.kind_data.unknown.preserved, true);
    const provenanceRequest = requests.filter(item => item.url.endsWith("/plan-edit")).at(-1).request.edit.provenance[0];
    check.deepEqual(Object.keys(provenanceRequest).sort(), ["note_id", "reason", "segment_refs", "source_id"]);
    check.equal(provenanceRequest.reason, "Unsaved evidence edit");
    check.deepEqual(provenanceRequest.segment_refs, ["seg-1", "seg-2"]);
    check.equal(fixtures.get("c-hypothesis").envelope.provenance[0].future_provenance.values[1].kept, true);
    action("open-document").click();
    check.equal(openedDocument, fixtures.get("c-hypothesis").document_id);
    const change = input => input.dispatchEvent(new Event("change", {bubbles: true}));
    for (const kind of ["knowledge", "opinion", "hypothesis"]) {
        const checkbox = root.querySelector(`[data-check-id="c-${kind}"]`);
        checkbox.checked = true;
        change(checkbox);
        await idle();
    }
    check.deepEqual([...root.querySelector("[data-bulk-status]").options].map(option => option.value), ["", "archived"]);
    root.querySelector("[data-bulk-status]").value = "archived";
    action("bulk").click();
    await idle();
    check.equal(fixtures.get("c-opinion").envelope.claim.status, "archived");
    check.equal(root.querySelector("[data-claim-field=status]").value, "archived", "untouched draft follows authority");
    await selectClaim("c-future");
    check.ok(action("save").disabled);
    check.match(root.textContent, /Type unavailable \(read-only\)/);
    check.match(root.textContent, /preserved/);
    await selectClaim("c-hypothesis");
    root.querySelector("[data-edge-field=target]").value = "c-opinion";
    root.querySelector("[data-edge-field=reason]").value = "Supports the opinion";
    action("add-edge").click();
    await idle();
    check.equal(fixtures.get("c-hypothesis").envelope.relations.outgoing[0].target.logical_id, "c-opinion");
    check.equal(requests.filter(item => item.url.endsWith("/plan-patch")).at(-1).request.edge.edge.type, "derives");
    action("edit-edge").click();
    root.querySelector("[data-edge-field=type]").value = "supports";
    root.querySelector("[data-edge-field=reason]").value = "Updated argument";
    action("add-edge").click();
    await idle();
    check.equal(fixtures.get("c-hypothesis").envelope.relations.outgoing[0].type, "supports");
    action("delete-edge").click();
    await idle();
    check.equal(fixtures.get("c-hypothesis").envelope.relations.outgoing.length, 0);
    root.querySelector("[data-new-kind]").value = "hypothesis";
    action("new").click();
    root.querySelector("[data-claim-field=title]").value = "New experimental claim";
    root.querySelector("[data-claim-field=body]").value = "A native paragraph.\n\nAnother paragraph.";
    field("statement").value = "A measurable prediction";
    field("test_method").value = "Run experiment";
    action("add-source").click();
    root.querySelector("[data-source-field=source_id]").value = "s-example";
    root.querySelector("[data-source-field=note_id]").value = "n-example";
    root.querySelector("[data-source-field=segments]").value = "seg-1";
    root.querySelector("[data-source-field=reason]").value = "Evidence";
    action("save").click();
    await idle();
    const create = requests.find(item => item.url.endsWith("/plan-create")).request;
    check.equal(create.kind, "hypothesis");
    check.equal(create.body.length, 2);
    check.equal(create.body[0].Children[0].Data, "A native paragraph.");
    check.ok(create.path.startsWith("box-test/"));
    await selectClaim("c-opinion");
    check.ok(root.querySelector("[data-kind-field='[\"validation\",\"status\"]']"));
    root.querySelector("[data-claims-view]").value = "knowledge";
    change(root.querySelector("[data-claims-view]"));
    await idle();
    check.equal(root.querySelectorAll("[data-claim-id]").length, 1);
    root.querySelector("[data-claims-view]").value = "claims";
    change(root.querySelector("[data-claims-view]"));
    await idle();
    await selectClaim("c-hypothesis");

    const results = [];
    const settled = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    for (const [themeName, css] of [["light", source.light], ["dark", source.dark]]) {
        theme.textContent = css;
        for (const fontSize of [14, 28]) {
            document.documentElement.style.setProperty("--b3-font-size", `${fontSize}px`);
            for (const [label, width, height] of [["desktop", 1200, 900], ["mobile", 390, 844]]) {
                mobile = label === "mobile";
                style.textContent = mobile ? source.mobileCSS : source.css;
                await window.resizeClaimsTest(width, height);
                await settled();
                const rect = root.closest(".b3-dialog__container").getBoundingClientRect();
                const main = root.querySelector(".evergreen-claims__main");
                const name = `${themeName}-${label}-${fontSize}`;
                check.ok(rect.left >= 0 && rect.right <= innerWidth + 1, name);
                check.ok(rect.top >= 0 && rect.bottom <= innerHeight + 1, name);
                check.equal(main.scrollWidth, main.clientWidth, `${name}: horizontal overflow`);
                check.ok(root.querySelector("[data-claim-action=save]").getClientRects().length);
                root.querySelectorAll("use").forEach(use => check.ok(document.querySelector(use.getAttribute("xlink:href"))));
                await window.captureClaimsTest(name);
                results.push(name);
            }
        }
    }
    dialog.destroy();
    await until(() => !document.querySelector(".evergreen-claims"));
    const readOnly = await api.openEvergreenClaims({notebookID: "box-test", logicalID: "c-hypothesis", readOnly: true});
    readOnly.element.querySelectorAll("[data-kind-field], [data-source-field], [data-claim-field]").forEach(input =>
        check.ok(input.disabled, "read-only disables every editable field"));
    check.ok(readOnly.element.querySelector("[data-claim-action=save]").disabled);
    readOnly.destroy();
    await until(() => !document.querySelector(".evergreen-claims"));
    let providerReadOnly = true;
    const relationEdge = {edge_id: "edge-test", schema: "evergreen.argument/v1", type: "supports", reason: "Across kinds",
        target: {logical_id: "c-opinion", entity_type: "claim"}, owner_id: "c-hypothesis", owner_hash: "sha256:base"};
    const relationAPI = load(source.relation, {
        "../../../util/fetch": {fetchSyncPost}, "../../../util/escape": escape, "../../../dialog/message": {showMessage() {}},
        "./databaseRowRefresh": {queueDatabaseRowRefresh() {}}, "./cell": {genCellValueByElement: () =>
            ({relation: {edges: [relationEdge]}, providerReadOnly})},
        "./view": {getFieldsByData: () => [{id: "relations", binding: {direction: "outgoing", edge_schema: "evergreen.argument/v1"}}]},
        "./col": {getColId: () => "relations"}, "./row": {getFieldIdByCellElement: () => "c-hypothesis"},
        "./evergreenPatch": load(source.patch), "../../claims/schemaEditor": load(source.schema, {"../../util/escape": escape}),
    });
    const cell = document.createElement("div");
    const relationRoot = document.createElement("div");
    document.body.append(relationRoot);
    relationRoot.innerHTML = relationAPI.getEvergreenRelationHTML({provider: {type: "evergreen.sy"},
        id: "av-claims", viewID: "claims"}, [cell]);
    check.equal(relationRoot.querySelector(".b3-menu__item"), null, "form uses appropriate shared controls");
    relationRoot.querySelectorAll("input, select, textarea, button").forEach(input => check.ok(input.disabled));
    providerReadOnly = false;
    relationRoot.innerHTML = relationAPI.getEvergreenRelationHTML({provider: {type: "evergreen.sy"},
        id: "av-claims", viewID: "claims"}, [cell]);
    relationRoot.querySelectorAll("use").forEach(use => check.ok(document.querySelector(use.getAttribute("xlink:href"))));
    check.equal(relationRoot.querySelectorAll(".av__evergreen-edge").length, 2);
    relationRoot.remove();
    return JSON.stringify(results);
};

const run = async () => {
    const {app, BrowserWindow, ipcMain} = require("electron");
    app.setPath("userData", process.argv[2]);
    app.commandLine.appendSwitch("disable-gpu");
    await app.whenReady();
    const win = new BrowserWindow({show: false, width: 1200, height: 900, useContentSize: true,
        webPreferences: {nodeIntegration: true, contextIsolation: false, offscreen: true}});
    ipcMain.handle("claims-resize", async (_event, width, height) => {
        win.setContentSize(width, height);
        await new Promise(resolve => setTimeout(resolve, 30));
    });
    ipcMain.handle("claims-capture", async (_event, name) => {
        if (process.env.CLAIMS_SCREENSHOT_DIR) {
            await new Promise(resolve => setTimeout(resolve, 300));
            const image = await win.webContents.capturePage();
            require("node:fs").writeFileSync(path.join(process.env.CLAIMS_SCREENSHOT_DIR, `${name}.png`), image.toPNG());
        }
    });
    let code = 0;
    try {
        await win.loadURL("data:text/html,<html><body></body></html>");
        const source = sources();
        await win.webContents.executeJavaScript(`${source.icons}; void 0;`);
        await win.webContents.executeJavaScript(`
            window.resizeClaimsTest = (width, height) => require("electron").ipcRenderer.invoke("claims-resize", width, height);
            window.captureClaimsTest = name => require("electron").ipcRenderer.invoke("claims-capture", name);
            void 0;
        `);
        const result = await win.webContents.executeJavaScript(
            `(${cases.toString()})(${JSON.stringify(source)}).catch(error => { throw new Error(String(error.stack || error)); })`);
        console.log(`Claims UI cases passed: ${result}`);
    } catch (error) {
        console.error(error);
        code = 1;
    } finally {
        win.destroy();
        app.exit(code);
    }
};

if (process.versions.electron && process.type === "browser") {
    run().catch(error => { console.error(error); require("electron").app.exit(1); });
} else {
    require("node:test").test("Claims UI creates, edits, protects unknown kinds and fits desktop/mobile themes", {
        skip: process.platform === "linux" && !process.env.DISPLAY && !process.env.WAYLAND_DISPLAY, timeout: 45000,
    }, async () => {
        const profile = mkdtempSync(path.join(os.tmpdir(), "siyuan-claims-"));
        const env = {...process.env};
        delete env.ELECTRON_RUN_AS_NODE;
        try {
            const {stdout} = await require("node:util").promisify(require("node:child_process").execFile)(
                require("electron"), [__filename, profile], {env, windowsHide: true, timeout: 40000});
            assert.match(stdout, /Claims UI cases passed/);
        } finally {
            rmSync(profile, {recursive: true, force: true});
        }
    });
}
