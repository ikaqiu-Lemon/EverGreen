export interface IEvergreenEnvelope<T> {
    ok: boolean;
    data: T;
    diagnostics?: Array<{
        code: string;
        level: string;
        path: string;
        message: string;
    }>;
}

export type EvergreenPatchTransport = (path: string, body: unknown) => Promise<IEvergreenEnvelope<unknown>>;

const responseError = (response: IEvergreenEnvelope<unknown>) =>
    response.diagnostics?.[0]?.message || "Evergreen operation failed";

export const planAndApplyEvergreenPatch = async (
    patch: unknown,
    transport: EvergreenPatchTransport,
) => {
    const plan = await transport("/api/evergreen/v1/av/plan-patch", patch);
    if (!plan.ok) {
        throw new Error(responseError(plan));
    }
    const applied = await transport("/api/evergreen/v1/apply", {operation: plan.data});
    if (!applied.ok) {
        throw new Error(responseError(applied));
    }
    return applied.data;
};
