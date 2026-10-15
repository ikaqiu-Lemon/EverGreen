const assert = require("node:assert/strict");
const {readFileSync, mkdtempSync, rmSync} = require("node:fs");
const path = require("node:path");
const os = require("node:os");

const syntheticSnapshots = () => {
    const kinds = ["knowledge", "opinion"].map(kind => ({kind, schema: `evergreen.claim-kind.${kind}/v1`,
        view_hints: {name: kind, view_id: kind}, allowed_statuses: ["active", "archived"],
        kind_data_schema: {type: "object"}, default_kind_data: {}}));
    const longID = "k-20261010-long-migrated-claim-identifier-preserves-canonical-reference";
    const claims = Array.from({length: 27}, (_, index) => ({
        logical_id: index === 0 ? longID : `k-20261010-migration-${index}`, document_id: `20261010120000-claim${index}`,
        base: "sha256:fixture", title: `Migrated claim ${index}`, body: [], read_only: false,
        envelope: {entity: {entity_type: "claim"}, claim: {claim_kind: "knowledge", status: "active", tags: [], kind_data: {}},
            provenance: [], relations: {outgoing: [{edge_id: `edge-${index}`, schema: "evergreen.argument/v1",
                type: "supports", target: {logical_id: longID, entity_type: "claim"}, reason: "Existing migration evidence"}]}},
    }));
    const reviews = Array.from({length: 3}, (_, review) => ({
        note_id: `n-20261010-review-${review}`, document_id: `20261010120000-review${review}`,
        semantic_hash: "sha256:fixture", revision: 1, can_materialize: true,
        segments: Array.from({length: review === 0 ? 49 : 46}, (_, index) => ({
            segment_id: `seg-${review}-${index}`, block_id: `block-${review}-${index}`, role: "note_segment",
            stored_hash: "sha256:fixture", current_hash: "sha256:fixture", order: index,
            source_ref: {source_id: "s-fixture", locator: {kind: "source-lines", value: `L${index + 1}`}},
        })),
        candidates: Array.from({length: 25}, (_, index) => ({
            candidate_id: `cand-${review}-${index}`, block_id: `candidate-${review}-${index}`, order: index,
            metadata: {candidate_id: `cand-${review}-${index}`, claim_kind: "knowledge", kind_schema: kinds[0].schema,
                title: `Migrated candidate ${index}`, segment_refs: [`seg-${review}-0`], payload_hash: "sha256:fixture",
                ref_hashes: {[`seg-${review}-0`]: "sha256:fixture"}, state: review === 0 ? "materialized" : "draft",
                materialized_claim_id: review === 0 ? claims[index].logical_id : "", relation: "support", reason: "Existing evidence"},
            payload: [{Type: "NodeParagraph", Children: [{Type: "NodeText", Data: "Reviewed content"}]}],
            current_payload_hash: "sha256:fixture", current_ref_hashes: {[`seg-${review}-0`]: "sha256:fixture"},
            stale: false, stale_refs: [],
        })),
        coverage: [{module_id: "coverage-fixture", disposition: "candidate", segment_refs: [`seg-${review}-0`],
            candidate_id: `cand-${review}-0`, reason: ""}],
        summary: {segment_count: review === 0 ? 49 : 46, candidate_count: 25, covered_segments: review === 0 ? 49 : 46,
            missing_segments: 0, duplicate_segments: 0, unresolved_modules: 0, unreferenced_candidates: 0},
    }));
    return {claims, reviews, schemas: {kinds}};
};

const sources = () => {
    const ts = require("typescript");
    const root = path.join(__dirname, "..");
    const compile = file => ts.transpileModule(readFileSync(path.join(root, file), "utf8"), {
        compilerOptions: {module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2021},
    }).outputText;
    const fixture = process.env.EG_MIGRATION_UI_SNAPSHOTS;
    return {
        snapshots: fixture ? JSON.parse(readFileSync(fixture, "utf8")) : syntheticSnapshots(),
        review: compile("src/protyle/review/evergreenReview.ts"),
        model: compile("src/protyle/review/model.ts"),
        claims: compile("src/protyle/claims/evergreenClaims.ts"),
        schema: compile("src/protyle/claims/schemaEditor.ts"),
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
    const load = (code, dependencies) => {
        const exports = {};
        new Function("exports", "require", code)(exports, name => {
            check.ok(name in dependencies, `Unexpected dependency ${name}`);
            return dependencies[name];
        });
        return exports;
    };
    let id = 0;
    let mobile = false;
    const style = document.createElement("style");
    const theme = document.createElement("style");
    document.head.append(style, theme);
    window.siyuan = {languages: source.languages, config: {readonly: false}, dialogs: [], zIndex: 1,
        menus: {menu: {element: document.createElement("div"), remove() {}}}};
    window.Lute = {NewNodeID: () => `20261010130000-${String(++id).padStart(7, "0")}`};
    window.fetch = async () => ({json: async () => ({ok: true, data: source.snapshots.schemas})});
    const fetchSyncPost = async (url, request) => {
        if (url.endsWith("/review/get")) {
            return {ok: true, data: source.snapshots.reviews.find(item => item.document_id === request.document_id)};
        }
        if (url.endsWith("/claims/get")) {
            return {ok: true, data: source.snapshots.claims.find(item => item.logical_id === request.logical_id)};
        }
        if (url.endsWith("/av/query")) {
            const rows = source.snapshots.claims.filter(item => request.view_id === "claims" ||
                item.envelope.claim.claim_kind === request.view_id).map(item => ({
                ref: {logical_id: item.logical_id, semantic_hash: item.base}, read_only: item.read_only,
                cells: {title: {text: item.title}, kind: {text: item.envelope.claim.claim_kind}},
            }));
            return {ok: true, data: {rows, total: rows.length, snapshot: "migration-fixture"}};
        }
        throw new Error(`Migration display attempted a write or unknown route: ${url}`);
    };
    const escape = load(source.escape, {});
    const dialog = load(source.dialog, {
        "../util/genID": {genUUID: () => `migration-${++id}`}, "../util/zIndex": {isAbove: () => false},
        "./moveResize": {moveResize() {}}, "../util/functions": {isMobile: () => mobile},
        "../protyle/util/compatibility": {isNotCtrl: () => true}, "../protyle": {},
        "../constants": {Constants: {TIMEOUT_OPENDIALOG: 0, TIMEOUT_DBLCLICK: 0}},
    });
    const schema = load(source.schema, {"../../util/escape": escape});
    const dependencies = {"../../dialog": dialog, "../../dialog/message": {showMessage: message => { throw new Error(message); }},
        "../../util/escape": escape, "../../util/fetch": {fetchSyncPost}, "../../util/functions": {isMobile: () => mobile}};
    const review = load(source.review, {...dependencies, "./model": load(source.model, {}), "../claims/schemaEditor": schema});
    const claims = load(source.claims, {...dependencies, "./schemaEditor": schema});
    const settled = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const results = [];
    const measure = async (root, label) => {
        const main = root.querySelector(".evergreen-review__main, .evergreen-claims__main");
        const rect = root.closest(".b3-dialog__container").getBoundingClientRect();
        check.ok(rect.left >= 0 && rect.right <= innerWidth + 1 && rect.top >= 0 && rect.bottom <= innerHeight + 1, label);
        check.equal(main.scrollWidth, main.clientWidth, `${label}: horizontal overflow`);
        check.ok(root.querySelectorAll("input, textarea, select").length > 0, "editor displayed no fields");
        await window.captureMigrationTest(label);
        results.push(label);
    };
    check.equal(source.snapshots.claims.length, 27);
    check.equal(source.snapshots.reviews.reduce((sum, item) => sum + item.candidates.length, 0), 75);
    for (const [themeName, css] of [["light", source.light], ["dark", source.dark]]) {
        theme.textContent = css;
        for (const [layout, width, height, font] of [["desktop", 1200, 900, 14], ["mobile", 390, 844, 28]]) {
            mobile = layout === "mobile";
            style.textContent = mobile ? source.mobileCSS : source.css;
            document.documentElement.style.setProperty("--b3-font-size", `${font}px`);
            await window.resizeMigrationTest(width, height);
            for (const snapshot of source.snapshots.reviews) {
                const element = document.createElement("div");
                element.innerHTML = "<div class=\"protyle-breadcrumb\"><div class=\"protyle-breadcrumb__bar\"></div>" +
                    "<div class=\"protyle-breadcrumb__plugin\"></div></div>";
                document.body.append(element);
                const protyle = {element, block: {rootID: snapshot.document_id}, notebookId: "20000101000000-egvault",
                    disabled: false, breadcrumb: {element: element.querySelector(".protyle-breadcrumb__bar")}};
                await review.syncEvergreenReview(protyle);
                element.querySelector("[data-type=evergreen-review]").click();
                await settled();
                const root = document.querySelector(".evergreen-review");
                check.ok(root);
                check.equal(root.querySelectorAll("[data-candidate-id]").length, snapshot.candidates.length);
                await measure(root, `${themeName}-${layout}-review-${snapshot.candidates[0].metadata.state}`);
                window.siyuan.dialogs.at(-1).destroy();
                element.remove();
            }
            const first = source.snapshots.claims[0];
            const instance = await claims.openEvergreenClaims({notebookID: "20000101000000-egvault", logicalID: first.logical_id});
            await settled();
            const root = document.querySelector(".evergreen-claims");
            check.equal(root.querySelectorAll("[data-claim-id]").length, 27);
            await measure(root, `${themeName}-${layout}-claims`);
            instance.destroy();
        }
    }
    return JSON.stringify({combinations: results.length, claims: 27, candidates: 75, results});
};

const run = async () => {
    const {app, BrowserWindow, ipcMain} = require("electron");
    app.setPath("userData", process.argv[2]);
    app.commandLine.appendSwitch("disable-gpu");
    await app.whenReady();
    const window = new BrowserWindow({show: false, width: 1200, height: 900, useContentSize: true,
        webPreferences: {nodeIntegration: true, contextIsolation: false, offscreen: true}});
    ipcMain.handle("migration-resize", async (_event, width, height) => {
        window.setContentSize(width, height);
        await new Promise(resolve => setTimeout(resolve, 30));
    });
    ipcMain.handle("migration-capture", async (_event, name) => {
        if (process.env.EG_MIGRATION_SCREENSHOT_DIR) {
            await new Promise(resolve => setTimeout(resolve, 300));
            const image = await window.webContents.capturePage();
            require("node:fs").writeFileSync(path.join(process.env.EG_MIGRATION_SCREENSHOT_DIR, `${name}.png`), image.toPNG());
        }
    });
    let code = 0;
    try {
        await window.loadURL("data:text/html,<html><body></body></html>");
        const source = sources();
        await window.webContents.executeJavaScript(`${source.icons}; void 0;`);
        await window.webContents.executeJavaScript(`
            window.resizeMigrationTest = (width, height) => require("electron").ipcRenderer.invoke("migration-resize", width, height);
            window.captureMigrationTest = name => require("electron").ipcRenderer.invoke("migration-capture", name);
            void 0;
        `);
        console.log(`Migration UI passed: ${await window.webContents.executeJavaScript(`(${cases.toString()})(${JSON.stringify(source)})`)}`);
    } catch (error) {
        console.error(error);
        code = 1;
    } finally {
        window.destroy();
        app.exit(code);
    }
};

if (process.versions.electron && process.type === "browser") {
    run().catch(error => { console.error(error); require("electron").app.exit(1); });
} else {
    require("node:test").test("full migrated Claims and all three reviews fit native desktop/mobile themes", {timeout: 45000}, async () => {
        const profile = mkdtempSync(path.join(os.tmpdir(), "siyuan-migration-"));
        const env = {...process.env};
        delete env.ELECTRON_RUN_AS_NODE;
        try {
            const {stdout} = await require("node:util").promisify(require("node:child_process").execFile)(
                require("electron"), [__filename, profile], {env, timeout: 40000});
            assert.match(stdout, /Migration UI passed/);
        } finally {
            rmSync(profile, {recursive: true, force: true});
        }
    });
}
