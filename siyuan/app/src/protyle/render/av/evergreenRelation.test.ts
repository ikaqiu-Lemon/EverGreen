import {strict as assert} from "node:assert";
import test from "node:test";
import {planAndApplyEvergreenPatch} from "./evergreenPatch";

test("typed edge patch plans before apply", async () => {
    const calls: Array<{path: string; body: unknown}> = [];
    const result = await planAndApplyEvergreenPatch({operation_id: "op-1"}, async (path, body) => {
        calls.push({path, body});
        if (path.endsWith("plan-patch")) {
            return {ok: true, data: {plan_hash: "sha256:plan"}};
        }
        return {ok: true, data: {state: "COMPLETED"}};
    });
    assert.deepEqual(calls, [
        {path: "/api/evergreen/v1/av/plan-patch", body: {operation_id: "op-1"}},
        {
            path: "/api/evergreen/v1/apply",
            body: {operation: {plan_hash: "sha256:plan"}},
        },
    ]);
    assert.deepEqual(result, {state: "COMPLETED"});
});

test("rejected plan never reaches apply and exposes the kernel diagnostic", async () => {
    const calls: string[] = [];
    await assert.rejects(
        planAndApplyEvergreenPatch({operation_id: "op-stale"}, async (path) => {
            calls.push(path);
            return {
                ok: false,
                data: null,
                diagnostics: [{
                    code: "EG_CONFLICT_BASE_MISMATCH",
                    level: "error",
                    path: "base.semantic_hash",
                    message: "stale projection",
                }],
            };
        }),
        /stale projection/,
    );
    assert.deepEqual(calls, ["/api/evergreen/v1/av/plan-patch"]);
});
