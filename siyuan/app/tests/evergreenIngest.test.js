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
        ui: compile("src/protyle/ingest/evergreenIngest.ts"),
        dialog: compile("src/dialog/index.ts"),
        escape: compile("src/util/escape.ts"),
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
    const load = (code, dependencies = {}) => {
        const exports = {};
        new Function("exports", "require", code)(exports, name => {
            check.ok(name in dependencies, `Unexpected dependency ${name}`);
            return dependencies[name];
        });
        return exports;
    };
    const clone = value => JSON.parse(JSON.stringify(value));
    const requests = [];
    const messages = [];
    let id = 0;
    let rejectSource = true;
    let mobile = false;
    const operation = operationID => ({
        plan: {operation_id: operationID, protocol: "evergreen.changeplan/v1",
            principal: {type: "user", id: "owner"}, command: "test", base: [], writes: []},
        images: [], plan_hash: `sha256:${operationID}`,
    });
    const fetchSyncPost = async (url, request) => {
        requests.push({url, request: clone(request)});
        if (url.endsWith("/sources/plan-capture")) {
            if (rejectSource) {
                return {ok: false, diagnostics: [{code: "EG_CONFLICT_BASE_MISMATCH", message: "Source changed"}]};
            }
            return {ok: true, data: {
                source_id: request.logical_id, path: request.path, deduped: false,
                has_note: false, reason_appended: false, operation: operation(request.operation_id),
            }};
        }
        if (url.endsWith("/notes/plan-create")) {
            return {ok: true, data: {
                note_id: request.logical_id, source_id: request.source_id, path: request.path,
                operation: operation(request.operation_id),
                snapshot: {document_id: request.document_id},
            }};
        }
        if (url.endsWith("/apply")) {
            return {ok: true, data: {
                operation_id: request.operation.plan.operation_id,
                state: "COMPLETED", plan_hash: request.operation.plan_hash,
            }};
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
    window.Lute = {NewNodeID: () => `20261013060000-${String(++id).padStart(7, "0")}`};
    window.fetch = async () => ({json: async () => ({ok: true, data: {kinds: [{
        kind: "knowledge", schema: "evergreen.claim-kind.knowledge/v1",
        view_hints: {name: "Knowledge"},
    }, {
        kind: "opinion", schema: "evergreen.claim-kind.opinion/v1",
        view_hints: {name: "Opinion"},
    }]}})});
    const escape = load(source.escape);
    const dialogModule = load(source.dialog, {
        "../util/genID": {genUUID: () => `dialog-${++id}`},
        "../util/zIndex": {isAbove: () => false},
        "./moveResize": {moveResize() {}},
        "../util/functions": {isMobile: () => mobile},
        "../protyle/util/compatibility": {isNotCtrl: () => true},
        "../protyle": {},
        "../constants": {Constants: {TIMEOUT_OPENDIALOG: 0, TIMEOUT_DBLCLICK: 0}},
    });
    const opened = [];
    const api = load(source.ui, {
        "../../dialog": dialogModule,
        "../../dialog/message": {showMessage: message => messages.push(message)},
        "../../util/escape": escape,
        "../../util/fetch": {fetchSyncPost},
        "../../util/functions": {isMobile: () => mobile},
        "../../editor/util": {openFileById: ({id}) => opened.push(id)},
        "../../mobile/editor": {openMobileFileById: (_app, id) => opened.push(id)},
    });
    const fixture = (readOnly = false) => {
        const element = document.createElement("div");
        element.innerHTML = '<div class="protyle-breadcrumb"><div class="protyle-breadcrumb__bar"></div>' +
            '<div class="protyle-breadcrumb__plugin"></div></div>';
        document.body.append(element);
        return {element, notebookId: "box-ui001", disabled: readOnly, app: {},
            block: {rootID: "20261013060000-current"},
            breadcrumb: {element: element.querySelector(".protyle-breadcrumb__bar")}};
    };
    const protyle = fixture();
    api.syncEvergreenIngest(protyle);
    const trigger = protyle.element.querySelector("[data-type=evergreen-ingest]");
    check.ok(trigger, "writable notebook mounts the Source/Note entry");
    trigger.click();
    const until = async condition => {
        for (let attempt = 0; attempt < 100; attempt++) {
            if (condition()) {
                return;
            }
            await new Promise(resolve => setTimeout(resolve, 10));
        }
        check.fail("ingest UI did not settle");
    };
    await until(() => document.querySelector(".evergreen-ingest"));
    let root = document.querySelector(".evergreen-ingest");
    const field = name => root.querySelector(`[data-ingest-field="${name}"]`);
    const action = name => root.querySelector(`[data-ingest-action="${name}"]`);
    const idle = () => until(() => !action("capture-source")?.disabled && !action("create-note")?.disabled);
    field("source-url").value = "https://example.com/ui";
    field("source-title").value = "UI Source";
    field("source-reason").value = "Capture for review";
    field("source-domain").value = "systems";
    field("source-tags").value = "ui, review";
    field("source-body").value = "Complete source text.";
    action("capture-source").click();
    await idle();
    check.ok(messages.some(message => message.includes("Source changed")));
    check.equal(field("source-body").value, "Complete source text.", "failed capture preserves the draft");
    check.equal(requests.filter(item => item.url.endsWith("/apply")).length, 0);

    rejectSource = false;
    action("capture-source").click();
    await until(() => root.querySelector('[data-ingest-tab="note"].b3-button--text'));
    check.equal(requests.filter(item => item.url.endsWith("/sources/plan-capture")).length, 2);
    check.equal(requests.filter(item => item.url.endsWith("/apply")).length, 1);
    check.equal(field("note-source").value.startsWith("s-"), true);
    check.equal(field("note-title").value, "UI Source");
    check.equal(root.querySelectorAll("[data-segment-row]").length, 1);
    check.equal(root.querySelectorAll("[data-candidate-row]").length, 1);

    field("segment-body").value = "Source-backed segment.";
    field("segment-locator").value = "L1-L1";
    field("candidate-title").value = "Reviewed UI claim";
    field("candidate-body").value = "A self-contained UI claim.";
    field("candidate-reason").value = "The Source segment supports the Claim.";
    action("create-note").click();
    await idle();
    const note = requests.find(item => item.url.endsWith("/notes/plan-create")).request;
    check.equal(note.source_id, field("note-source").value);
    check.equal(note.segments.length, 1);
    check.equal(note.candidates.length, 1);
    check.deepEqual(note.candidates[0].segment_refs, [note.segments[0].segment_id]);
    check.equal(note.coverage.length, 1);
    check.equal(note.coverage[0].disposition, "candidate");
    check.deepEqual(note.coverage[0].candidate_ids, [note.candidates[0].candidate_id]);
    check.deepEqual(opened, [note.document_id]);

    // Shared icons and stable compact layouts remain valid in desktop/mobile, light/dark and 14/28px.
    const settled = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    for (const [themeName, css] of [["light", source.light], ["dark", source.dark]]) {
        theme.textContent = css;
        for (const fontSize of [14, 28]) {
            document.documentElement.style.setProperty("--b3-font-size", `${fontSize}px`);
            for (const [viewport, width, height] of [["desktop", 1200, 900], ["mobile", 390, 844]]) {
                mobile = viewport === "mobile";
                style.textContent = mobile ? source.mobileCSS : source.css;
                await window.resizeIngestTest(width, height);
                await settled();
                const dialogRect = root.closest(".b3-dialog__container").getBoundingClientRect();
                check.ok(dialogRect.left >= -1 && dialogRect.right <= window.innerWidth + 1,
                    `${themeName}/${fontSize}/${viewport} dialog horizontal overflow`);
                check.ok(dialogRect.top >= -1 && dialogRect.bottom <= window.innerHeight + 1,
                    `${themeName}/${fontSize}/${viewport} dialog vertical overflow`);
                root.querySelectorAll("input, textarea, select, button").forEach(element => {
                    const rect = element.getBoundingClientRect();
                    check.ok(rect.width > 0 && rect.right <= dialogRect.right + 1,
                        `${themeName}/${fontSize}/${viewport} control overflow`);
                });
                root.querySelectorAll("use").forEach(use => {
                    check.ok(document.querySelector(use.getAttribute("xlink:href")), "missing shared icon");
                });
                check.ok(root.querySelectorAll(".evergreen-ingest__field").length >= 6);
                await window.captureIngestTest(`${themeName}-${fontSize}-${viewport}`);
            }
        }
    }
    window.siyuan.dialogs.at(-1).destroy();

    const readOnly = fixture(true);
    api.syncEvergreenIngest(readOnly);
    check.ok(readOnly.element.querySelector("[data-type=evergreen-ingest]").disabled);
};

const run = async () => {
    const {app, BrowserWindow, ipcMain} = require("electron");
    app.setPath("userData", process.argv[2]);
    app.commandLine.appendSwitch("disable-gpu");
    await app.whenReady();
    const win = new BrowserWindow({show: false, width: 1200, height: 900, useContentSize: true,
        webPreferences: {nodeIntegration: true, contextIsolation: false, offscreen: true}});
    ipcMain.handle("ingest-resize", async (_event, width, height) => {
        win.setContentSize(width, height);
        await new Promise(resolve => setTimeout(resolve, 30));
    });
    ipcMain.handle("ingest-capture", async (_event, name) => {
        if (process.env.INGEST_SCREENSHOT_DIR) {
            const image = await win.webContents.capturePage();
            require("node:fs").writeFileSync(
                path.join(process.env.INGEST_SCREENSHOT_DIR, `${name}.png`), image.toPNG());
        }
    });
    let code = 0;
    try {
        await win.loadURL("data:text/html,<html><body></body></html>");
        await win.webContents.executeJavaScript(`${sources().icons}; void 0;`);
        await win.webContents.executeJavaScript(`
            window.resizeIngestTest = (width, height) =>
                require("electron").ipcRenderer.invoke("ingest-resize", width, height);
            window.captureIngestTest = name =>
                require("electron").ipcRenderer.invoke("ingest-capture", name);
            void 0;
        `);
        await win.webContents.executeJavaScript(
            `(${cases.toString()})(${JSON.stringify(sources())}).catch(error => { throw new Error(String(error.stack || error)); })`);
        console.log("Evergreen ingest UI cases passed");
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
    require("node:test").test("Source and Note UI uses typed plan/apply and preserves failed drafts", {
        skip: process.platform === "linux" && !process.env.DISPLAY && !process.env.WAYLAND_DISPLAY,
        timeout: 45000,
    }, async () => {
        const profile = mkdtempSync(path.join(os.tmpdir(), "siyuan-evergreen-ingest-"));
        const env = {...process.env};
        delete env.ELECTRON_RUN_AS_NODE;
        try {
            const {stdout} = await require("node:util").promisify(require("node:child_process").execFile)(
                require("electron"), [__filename, profile], {env, windowsHide: true, timeout: 40000});
            assert.match(stdout, /Evergreen ingest UI cases passed/);
        } finally {
            rmSync(profile, {recursive: true, force: true});
        }
    });
}
