import { CombinedGraphQLErrors } from "@apollo/client/errors";
import { describe, expect, it, vi } from "vitest";

import { endSession } from "./use-sign-out";

function gqlError(code: string) {
  return new CombinedGraphQLErrors({
    data: null,
    errors: [{ message: code.toLowerCase(), extensions: { code } }],
  });
}

const noSleep = () => Promise.resolve();

describe("endSession", () => {
  it("treats a confirmed logout as signed out", async () => {
    const logout = vi.fn().mockResolvedValue({ data: { logout: true } });
    await expect(endSession(logout, noSleep)).resolves.toEqual({ ok: true });
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it("treats UNAUTHENTICATED (thrown or returned) as signed out", async () => {
    await expect(
      endSession(vi.fn().mockRejectedValue(gqlError("UNAUTHENTICATED")), noSleep),
    ).resolves.toEqual({ ok: true });
    await expect(
      endSession(vi.fn().mockResolvedValue({ error: gqlError("UNAUTHENTICATED") }), noSleep),
    ).resolves.toEqual({ ok: true });
  });

  it("does not sign out on FORBIDDEN and does not retry it", async () => {
    const logout = vi.fn().mockRejectedValue(gqlError("FORBIDDEN"));
    const out = await endSession(logout, noSleep);
    expect(out.ok).toBe(false);
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it("retries transient failures with backoff, then reports the error", async () => {
    const sleep = vi.fn(noSleep);
    const logout = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
    const out = await endSession(logout, sleep, [10, 20]);
    expect(out).toMatchObject({ ok: false, error: { code: "NETWORK" } });
    expect(logout).toHaveBeenCalledTimes(3);
    expect(sleep.mock.calls).toEqual([[10], [20]]);
  });

  it("succeeds when a retry gets through", async () => {
    const logout = vi
      .fn()
      .mockRejectedValueOnce(gqlError("INTERNAL"))
      .mockResolvedValueOnce({ data: { logout: true } });
    await expect(endSession(logout, noSleep, [1])).resolves.toEqual({ ok: true });
    expect(logout).toHaveBeenCalledTimes(2);
  });
});
