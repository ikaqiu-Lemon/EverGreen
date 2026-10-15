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
        ui: compile("src/protyle/review/evergreenReview.ts"),
        model: compile("src/protyle/review/model.ts"),
        schema: compile("src/protyle/claims/schemaEditor.ts"),
        dialog: compile("src/dialog/index.ts"),
        escape: compile("src/util/escape.ts"),
        languages: JSON.parse(readFileSync(path.join(root, "appearance/langs/en.json"), "utf8")),
        css: require("sass").compile(path.join(root, "src/assets/scss/base.scss")).css,
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
            if (!(name in dependencies)) {
                throw new Error(`Unexpected dependency ${name}`);
            }
            return dependencies[name];
        });
        return exports;
    };
    const clone = value => JSON.parse(JSON.stringify(value));
    const base = {
        note_id: "n-review", document_id: "20261010120000-note001",
        semantic_hash: "sha256:base", revision: 1, can_materialize: true,
        segments: Array.from({length: 46}, (_, index) => ({
            segment_id: `seg-${index}`, block_id: `block-${index}`, role: "note_segment",
            stored_hash: "sha256:stored", current_hash: "sha256:stored", order: index,
            source_ref: {source_id: "s-review", locator: {kind: "source-lines", value: `L${index + 1}`}},
        })),
        candidates: Array.from({length: 25}, (_, index) => ({
            candidate_id: `cand-${index}`, block_id: `candidate-${index}`, order: index,
            metadata: {
                candidate_id: `cand-${index}`, claim_kind: "knowledge",
                kind_schema: "evergreen.claim.knowledge/v1", title: `Claim ${index}`,
                segment_refs: ["seg-0"], payload_hash: "sha256:payload",
                ref_hashes: {"seg-0": "sha256:stored"}, state: "draft",
                relation: "support", reason: "Source directly supports the claim.",
            },
            payload: [{ID: `payload-${index}`, Type: "NodeParagraph", Data: `Reviewed claim ${index}`}],
            current_payload_hash: "sha256:payload", current_ref_hashes: {"seg-0": "sha256:stored"},
            stale: false, stale_refs: [],
        })),
        coverage: [{
            module_id: "coverage-main", disposition: "candidate", segment_refs: ["seg-0"],
            candidate_ids: ["cand-0"], reason: "",
        }],
        summary: {segment_count: 46, candidate_count: 25, covered_segments: 46,
            missing_segments: 0, duplicate_segments: 0, unresolved_modules: 0, unreferenced_candidates: 0},
    };
    let mobile = false;
    let id = 0;
    let snapshot = clone(base);
    let rejectPlan = false;
    let pendingPlan;
    const requests = [];
    const messages = [];
    const fetchSyncPost = async (url, request) => {
        requests.push({url, request: clone(request)});
        if (url.endsWith("/get")) {
            return {ok: true, data: clone(snapshot)};
        }
        if (url.endsWith("/plan") || url.endsWith("/plan-materialize")) {
            if (pendingPlan) {
                await pendingPlan;
            }
            if (rejectPlan) {
                return {ok: false, diagnostics: [{message: "EG_CONFLICT_BASE_MISMATCH"}]};
            }
            return {ok: true, data: {operation: {
                images: [], plan: {base: [], writes: [], operation_id: request.operation_id},
                plan_hash: "sha256:plan",
            }}};
        }
        if (url.endsWith("/apply")) {
            const planned = requests.filter(item =>
                item.url.endsWith("/plan") || item.url.endsWith("/plan-materialize")).at(-1);
            const command = planned.request.command;
            if (command?.action === "candidate.update") {
                const candidate = snapshot.candidates.find(item => item.candidate_id === command.candidate_id);
                candidate.metadata = clone(command.metadata);
                candidate.payload = clone(command.payload);
            } else if (command?.action === "coverage.update") {
                snapshot.coverage = clone(command.coverage);
            } else if (command?.action === "candidate.rebase" || command?.action === "candidate.confirm") {
                const candidate = snapshot.candidates.find(item => item.candidate_id === command.candidate_id);
                candidate.stale = false;
                candidate.stale_refs = [];
                candidate.metadata.state = command.action === "candidate.confirm" ? "confirmed" : "draft";
                snapshot.can_materialize = true;
            }
            snapshot.semantic_hash = `sha256:revision-${++id}`;
            return {ok: true, data: {state: "COMPLETED"}};
        }
        throw new Error(`Unexpected route ${url}`);
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
    window.fetch = async () => ({json: async () => ({ok: true, data: {kinds: [{
        kind: "knowledge", schema: "evergreen.claim.knowledge/v1", view_hints: {name: "Knowledge"},
        kind_data_schema: {type: "object"}, default_kind_data: {},
    }, {
        kind: "hypothesis", schema: "evergreen.claim-kind.hypothesis/v1", view_hints: {name: "Hypotheses"},
        kind_data_schema: {type: "object", required: ["statement", "test_method"],
            properties: {statement: {type: "string", minLength: 1}, test_method: {type: "string", minLength: 1}}},
    }]}})});
    const dialogModule = load(source.dialog, {
        "../util/genID": {genUUID: () => `dialog-${++id}`},
        "../util/zIndex": {isAbove: () => false},
        "./moveResize": {moveResize() {}},
        "../util/functions": {isMobile: () => mobile},
        "../protyle/util/compatibility": {isNotCtrl: () => true},
        "../protyle": {},
        "../constants": {Constants: {TIMEOUT_OPENDIALOG: 0, TIMEOUT_DBLCLICK: 0}},
    });
    const api = load(source.ui, {
        "../../dialog": dialogModule,
        "../../dialog/message": {showMessage: message => messages.push(message)},
        "../../util/escape": load(source.escape),
        "../../util/fetch": {fetchSyncPost},
        "../../util/functions": {isMobile: () => mobile},
        "./model": load(source.model),
        "../claims/schemaEditor": load(source.schema, {"../../util/escape": load(source.escape)}),
    });
    const fixture = () => {
        const element = document.createElement("div");
        element.innerHTML = '<div class="protyle-breadcrumb"><div class="protyle-breadcrumb__bar"></div>' +
            '<div class="protyle-breadcrumb__plugin"></div></div>';
        document.body.append(element);
        return {element, block: {rootID: base.document_id}, notebookId: "box-review",
            disabled: false, breadcrumb: {element: element.querySelector(".protyle-breadcrumb__bar")}};
    };
    const protyle = fixture();
    await api.syncEvergreenReview(protyle);
    const trigger = protyle.element.querySelector("[data-type=evergreen-review]");
    check.ok(trigger, "managed Note mounts its entry next to the breadcrumb bar");
    const until = async condition => {
        for (let attempt = 0; attempt < 100; attempt++) {
            if (condition()) {
                return;
            }
            await new Promise(resolve => setTimeout(resolve, 10));
        }
        check.fail("UI condition did not settle");
    };
    trigger.click();
    await until(() => document.querySelector(".evergreen-review"));
    let root = document.querySelector(".evergreen-review");
    const field = name => root.querySelector(`[data-review-field="${name}"]`);
    const action = name => root.querySelector(`[data-review-action="${name}"]`);
    const idle = () => until(() => !action("coverage-save").disabled);
    const requestCount = suffix => requests.filter(item => item.url.endsWith(suffix)).length;

    // 切换候选保留本地草稿，冲突不调用 apply，也不丢弃未保存编辑。
    field("title").value = "Unsaved reviewed title";
    field("payload").value = JSON.stringify([{Type: "NodeParagraph", Data: "Edited payload"}]);
    root.querySelector('[data-candidate-id="cand-1"]').click();
    root.querySelector('[data-candidate-id="cand-0"]').click();
    check.equal(field("title").value, "Unsaved reviewed title");
    rejectPlan = true;
    const applyBefore = requestCount("/apply");
    action("candidate-save").click();
    await idle();
    check.equal(requestCount("/apply"), applyBefore);
    check.equal(field("title").value, "Unsaved reviewed title");
    check.ok(messages.some(message => message.includes("EG_CONFLICT_BASE_MISMATCH")));
    rejectPlan = false;
    action("candidate-save").click();
    await idle();
    check.equal(snapshot.candidates[0].metadata.title, "Unsaved reviewed title");
    check.equal(snapshot.candidates[0].payload[0].Data, "Edited payload");

    // coverage 允许重新绑定候选；busy 期间不能切换候选或再次提交。
    root.querySelector("[data-coverage-candidates]").value = "cand-0, cand-1";
    let release;
    pendingPlan = new Promise(resolve => { release = resolve; });
    action("coverage-save").click();
    root.querySelector('[data-candidate-id="cand-2"]').dispatchEvent(new MouseEvent("click", {bubbles: true}));
    check.equal(root.querySelector("[data-editor-candidate]").dataset.editorCandidate, "cand-0");
    release();
    pendingPlan = undefined;
    await idle();
    check.deepEqual(snapshot.coverage[0].candidate_ids, ["cand-0", "cand-1"]);
    const materializeBase = snapshot.semantic_hash;
    action("candidate-materialize").click();
    await idle();
    const materialize = requests.find(item => item.url.endsWith("/plan-materialize")).request;
    check.equal(materialize.note_id, base.note_id);
    check.equal(materialize.note_base, materializeBase);
    check.ok(materialize.claim_path.startsWith("box-review/"));
    check.equal(materialize.claim_id, `c-${materialize.claim_document_id}`);
    // 第三 kind 的物化信息由同一 schema 编辑器收集，不修改 Candidate 存储结构。
    window.siyuan.dialogs.at(-1).destroy();
    await until(() => !document.querySelector(".evergreen-review"));
    snapshot.candidates[0].metadata.claim_kind = "hypothesis";
    snapshot.candidates[0].metadata.kind_schema = "evergreen.claim-kind.hypothesis/v1";
    trigger.click();
    await until(() => document.querySelector(".evergreen-review"));
    root = document.querySelector(".evergreen-review");
    root.querySelector('[data-kind-field=\'["statement"]\']').value = "CAS prevents lost updates";
    root.querySelector('[data-kind-field=\'["test_method"]\']').value = "Concurrent edits";
    action("candidate-materialize").click();
    await idle();
    check.deepEqual(requests.filter(item => item.url.endsWith("/plan-materialize")).at(-1).request.kind_data,
        {statement: "CAS prevents lost updates", test_method: "Concurrent edits"});
    snapshot.candidates[0].stale = true;
    snapshot.candidates[0].stale_refs = ["seg-0"];
    snapshot.candidates[0].metadata.state = "stale";
    snapshot.can_materialize = false;
    window.siyuan.dialogs.at(-1).destroy();
    await until(() => !document.querySelector(".evergreen-review"));
    trigger.click();
    await until(() => document.querySelector(".evergreen-review"));
    const staleRoot = document.querySelector(".evergreen-review");
    check.ok(staleRoot.querySelector("[data-review-action=candidate-materialize]").disabled);
    const confirm = staleRoot.querySelector("[data-review-action=candidate-confirm]");
    check.equal(confirm.disabled, false, "stale Candidate permits explicit human confirmation");
    confirm.click();
    await until(() => !staleRoot.querySelector("[data-review-action=coverage-save]").disabled);
    check.equal(snapshot.candidates[0].metadata.state, "confirmed");
    window.siyuan.dialogs.at(-1).destroy();
    await until(() => !document.querySelector(".evergreen-review"));
    trigger.click();
    await until(() => document.querySelector(".evergreen-review"));
    root = document.querySelector(".evergreen-review");

    // 每个引用图标都来自现有图标表，操作按钮有可读文本并保留共享 disabled 状态。
    root.querySelectorAll("use").forEach(use => {
        check.ok(document.querySelector(use.getAttribute("xlink:href")), "missing shared icon");
    });
    root.querySelectorAll("[data-review-action]").forEach(button => check.ok(button.textContent.trim()));

    const results = [];
    const settled = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const measure = label => {
        const dialog = root.closest(".b3-dialog__container").getBoundingClientRect();
        const main = root.querySelector(".evergreen-review__main");
        const workspace = root.querySelector(".evergreen-review__workspace").getBoundingClientRect();
        check.ok(dialog.left >= 0 && dialog.right <= innerWidth + 1, label);
        check.ok(dialog.top >= 0 && dialog.bottom <= innerHeight + 1, label);
        check.ok(workspace.bottom <= dialog.bottom + 1, label);
        check.equal(main.scrollWidth, main.clientWidth, `${label}: main horizontal overflow`);
        results.push({label, viewport: [innerWidth, innerHeight], mainWidth: main.clientWidth,
            mainHeight: main.clientHeight, horizontalOverflow: false});
    };
    for (const [themeName, css] of [["light", source.light], ["dark", source.dark]]) {
        theme.textContent = css;
        document.documentElement.dataset.themeMode = themeName;
        for (const fontSize of [14, 28]) {
            document.documentElement.style.setProperty("--b3-font-size", `${fontSize}px`);
            await window.resizeReviewTest(1200, 900);
            await settled();
            measure(`${themeName}-desktop-${fontSize}`);
            await window.resizeReviewTest(390, 844);
            await settled();
            measure(`${themeName}-mobile-${fontSize}`);
        }
    }
    await window.resizeReviewTest(1200, 900);
    document.documentElement.style.setProperty("--b3-font-size", "14px");
    theme.textContent = source.light;
    document.documentElement.dataset.themeMode = "light";
    await settled();
    if (window.captureReviewTest) {
        await new Promise(resolve => setTimeout(resolve, 300));
        await window.captureReviewTest("desktop-light");
        await window.resizeReviewTest(390, 844);
        await settled();
        await window.captureReviewTest("mobile-light");
        theme.textContent = source.dark;
        document.documentElement.dataset.themeMode = "dark";
        await settled();
        await new Promise(resolve => setTimeout(resolve, 300));
        await window.captureReviewTest("mobile-dark");
    }
    window.siyuan.dialogs.at(-1).destroy();
    await until(() => !document.querySelector(".evergreen-review"));
    protyle.disabled = true;
    trigger.click();
    await until(() => document.querySelector(".evergreen-review"));
    document.querySelectorAll("[data-review-action]").forEach(button => check.ok(button.disabled));
    window.siyuan.dialogs.at(-1).destroy();
    await until(() => !document.querySelector(".evergreen-review"));
    protyle.element.remove();
    return JSON.stringify(results);
};

const run = async () => {
    const {app, BrowserWindow, ipcMain} = require("electron");
    app.setPath("userData", process.argv[2]);
    app.commandLine.appendSwitch("disable-gpu");
    await app.whenReady();
    const win = new BrowserWindow({show: false, width: 1200, height: 900, useContentSize: true,
        webPreferences: {nodeIntegration: true, contextIsolation: false, offscreen: true}});
    ipcMain.handle("review-resize", async (_event, width, height) => {
        win.setContentSize(width, height);
        await new Promise(resolve => setTimeout(resolve, 30));
    });
    ipcMain.handle("review-capture", async (_event, name) => {
        if (process.env.REVIEW_SCREENSHOT_DIR) {
            const image = await win.webContents.capturePage();
            require("node:fs").writeFileSync(path.join(process.env.REVIEW_SCREENSHOT_DIR, `${name}.png`), image.toPNG());
        }
    });
    let code = 0;
    try {
        await win.loadURL("data:text/html,<html><body></body></html>");
        await win.webContents.executeJavaScript(`${sources().icons}; void 0;`);
        await win.webContents.executeJavaScript(`
            window.resizeReviewTest = (width, height) => require("electron").ipcRenderer.invoke("review-resize", width, height);
            window.captureReviewTest = name => require("electron").ipcRenderer.invoke("review-capture", name);
            void 0;
        `);
        const result = await win.webContents.executeJavaScript(
            `(${cases.toString()})(${JSON.stringify(sources())}).catch(error => { throw new Error(String(error.stack || error)); })`);
        console.log(`Review UI cases passed: ${result}`);
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
    require("node:test").test("review UI preserves drafts, plans edits, mounts its entry and fits all themes and viewports", {
        skip: process.platform === "linux" && !process.env.DISPLAY && !process.env.WAYLAND_DISPLAY,
        timeout: 45000,
    }, async () => {
        const profile = mkdtempSync(path.join(os.tmpdir(), "siyuan-review-"));
        const env = {...process.env};
        delete env.ELECTRON_RUN_AS_NODE;
        try {
            const {stdout} = await require("node:util").promisify(require("node:child_process").execFile)(
                require("electron"), [__filename, profile], {env, windowsHide: true, timeout: 40000});
            assert.match(stdout, /Review UI cases passed/);
        } finally {
            rmSync(profile, {recursive: true, force: true});
        }
    });
}
